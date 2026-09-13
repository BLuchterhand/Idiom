package transcript

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"idiom/internal/contracts/runprofile"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/redaction"
)

type DebugLoggerOptions struct {
	Dir           string
	Mode          runprofile.DebugLogMode
	WorkspaceRoot string
	// ResumePath, when non-empty, opens that existing log file in
	// append mode instead of creating a fresh one under Dir. The file
	// must already exist; callers use this to continue writing into a
	// prior session's log on --resume. No SessionStart is emitted on
	// resume — the resumed session's own start record already lives
	// at the top of the file.
	ResumePath string
	// SessionID, when non-empty, is appended to the timestamp in the
	// generated log filename (e.g.
	// `20260510-143000.000-<sid>.jsonl`). Without this, two
	// concurrent sessions in the same workspace can generate logs
	// whose timestamps round to the same millisecond, making
	// per-session attribution best-effort. Ignored when ResumePath
	// is set (resume reuses the existing filename verbatim).
	SessionID string
}

type DebugLogger struct {
	mu                 sync.Mutex
	path               string
	file               *os.File
	mode               runprofile.DebugLogMode
	workspaceRoot      string
	lastMessageRole    string
	lastMessageContent string
	lastSummaryContent string
}

type SessionStart struct {
	AppName             string         `json:"app_name,omitempty"`
	ContextSources      []string       `json:"context_sources,omitempty"`
	PromptSources       []PromptSource `json:"prompt_sources,omitempty"`
	SystemPrompt        string         `json:"system_prompt,omitempty"`
	SystemPromptDigest  string         `json:"system_prompt_digest,omitempty"`
	WorkspaceRoot       string         `json:"workspace_root,omitempty"`
	WorkspaceCWD        string         `json:"workspace_cwd,omitempty"`
	WorkspaceMode       string         `json:"workspace_mode,omitempty"`
	ExploreThoroughness string         `json:"explore_thoroughness,omitempty"`
	CtxSize             int            `json:"ctx_size,omitempty"`
}

type PromptSource struct {
	Kind        string `json:"kind,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Path        string `json:"path,omitempty"`
}

// ToolResultDetail extends the basic tool_result record with size and
// resolved-target metadata. Lets a session log answer "what filled the
// context" without re-reading every tool payload.
type ToolResultDetail struct {
	Step           int
	Agent          string
	Tool           string
	Summary        string
	Error          string
	OutputChars    int
	OutputLines    int
	Path           string
	StartLine      int
	EndLine        int
	Truncated      bool
	DiscoveryClass string
	DiscoveryScope string
	DiscoveryCost  string
	ExtraFields    map[string]any
	Facts          *toolfacts.ToolResultFacts
}

type ToolRequestDetail struct {
	Step           int
	Agent          string
	Raw            string
	Tool           string
	WorkspaceMode  string
	Broad          bool
	Narrowing      bool
	Action         string
	DiscoveryClass string
	DiscoveryScope string
	DiscoveryCost  string
}

// ModelCall records one CompleteChatMessage round-trip.
type ModelCall struct {
	Step                       int
	Agent                      string
	PromptTokens               int
	CompletionTokens           int
	TotalTokens                int
	PromptCacheHitTokens       int
	PromptCacheMissTokens      int
	PromptCacheAccountingKnown bool
	ReasoningTokens            int
	CtxSize                    int
	MessageCount               int
	RoleChars                  map[string]int
	ChosenTool                 string // tool the model picked, or "<answer>" / "<parse_error>"
	DurationMillis             int64
}

// TurnSummary mirrors the existing budget_ledger payload but is written
// to its own type=turn_summary record so analysis tools can pick it out
// without parsing through message records.
type TurnSummary struct {
	Lane                       string
	UsedModelCalls             int
	MaxModelCalls              int
	UsedToolResults            int
	MaxToolSteps               int
	PromptTokens               int
	CompletionTokens           int
	TotalTokens                int
	PromptCacheHitTokens       int
	PromptCacheMissTokens      int
	PromptCacheAccountingKnown bool
	PromptCacheHitRate         float64
	ReasoningTokens            int
	ProgressTurns              int
	RejectedActions            int
	CompletionGateRejections   int
	ContextCompactions         int
	DurationMillis             int64
	StopReason                 string
	BudgetExhausted            bool
}

type TurnSummaryRecordOptions struct {
	IncludeZeroBudgetLimits bool
}

func BuildTurnSummaryRecord(s TurnSummary, timestamp time.Time, options ...TurnSummaryRecordOptions) map[string]any {
	includeZeroBudgetLimits := false
	if len(options) > 0 {
		includeZeroBudgetLimits = options[0].IncludeZeroBudgetLimits
	}

	record := map[string]any{
		"ts":                            timestamp.Format(time.RFC3339Nano),
		"type":                          "turn_summary",
		"lane":                          s.Lane,
		"used_model_calls":              s.UsedModelCalls,
		"used_tool_results":             s.UsedToolResults,
		"prompt_tokens":                 s.PromptTokens,
		"completion_tokens":             s.CompletionTokens,
		"total_tokens":                  s.TotalTokens,
		"prompt_cache_hit_tokens":       s.PromptCacheHitTokens,
		"prompt_cache_miss_tokens":      s.PromptCacheMissTokens,
		"prompt_cache_accounting_known": s.PromptCacheAccountingKnown,
		"reasoning_tokens":              s.ReasoningTokens,
		"progress_turns":                s.ProgressTurns,
		"rejected_actions":              s.RejectedActions,
		"completion_gate_rejections":    s.CompletionGateRejections,
		"context_compactions":           s.ContextCompactions,
		"duration_ms":                   s.DurationMillis,
		"stop_reason":                   s.StopReason,
		"budget_exhausted":              s.BudgetExhausted,
	}
	if s.MaxModelCalls > 0 || includeZeroBudgetLimits {
		record["max_model_calls"] = s.MaxModelCalls
	}

	if s.MaxToolSteps > 0 || includeZeroBudgetLimits {
		record["max_tool_steps"] = s.MaxToolSteps
	}

	return record
}

// SessionSummary is the end-of-session aggregate written on shutdown.
type SessionSummary struct {
	Turns                      int
	ModelCalls                 int
	ToolCalls                  int
	PromptTokens               int
	CompletionTokens           int
	TotalTokens                int
	PromptCacheHitTokens       int
	PromptCacheMissTokens      int
	PromptCacheAccountingKnown bool
	PromptCacheHitRate         float64
	ReasoningTokens            int
	PeakPromptTokens           int
	CtxSize                    int
	PeakCtxPct                 float64
	Compactions                int
	TopToolsByOutput           []ToolOutputAggregate
	DiscoveryByClass           []DiscoveryAggregate
	DiscoveryByCost            []DiscoveryAggregate
	DurationMillis             int64
}

type ToolOutputAggregate struct {
	Tool        string `json:"tool"`
	Calls       int    `json:"calls"`
	OutputChars int    `json:"output_chars"`
}

type DiscoveryAggregate struct {
	Name  string `json:"name"`
	Calls int    `json:"calls"`
}

func NewDebugLogger(opts DebugLoggerOptions) (*DebugLogger, error) {
	if strings.TrimSpace(opts.Dir) == "" {
		opts.Dir = "logs/conversations"
	}

	if opts.Mode == "" {
		opts.Mode = runprofile.DebugLogModeRedacted
	}

	var filename string

	if strings.TrimSpace(opts.ResumePath) != "" {
		if _, err := os.Stat(opts.ResumePath); err != nil {
			return nil, fmt.Errorf("resume log %q: %w", opts.ResumePath, err)
		}

		filename = opts.ResumePath
	} else {
		if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
			return nil, fmt.Errorf("create debug log dir: %w", err)
		}

		stem := time.Now().Format("20060102-150405.000")
		if sid := strings.TrimSpace(opts.SessionID); sid != "" {
			stem = stem + "-" + sid
		}

		filename = filepath.Join(opts.Dir, stem+".jsonl")
	}

	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open debug log: %w", err)
	}
	// Resumed logs may predate private file modes. Restrict the open file before
	// appending any more conversation content.
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("restrict debug log permissions: %w", err)
	}

	return &DebugLogger{
		path:          filename,
		file:          file,
		mode:          opts.Mode,
		workspaceRoot: filepath.Clean(strings.TrimSpace(opts.WorkspaceRoot)),
	}, nil
}

func (l *DebugLogger) Path() string {
	if l == nil {
		return ""
	}

	return l.path
}

func (l *DebugLogger) Mode() runprofile.DebugLogMode {
	if l == nil {
		return runprofile.DebugLogModeRedacted
	}

	return l.mode
}

func (l *DebugLogger) SetMode(mode runprofile.DebugLogMode) {
	if l == nil {
		return
	}

	if mode == "" {
		mode = runprofile.DebugLogModeRedacted
	}

	l.mode = mode
}

func (l *DebugLogger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}

	return l.file.Close()
}

func (l *DebugLogger) LogSessionStart(start SessionStart) error {
	record := map[string]any{
		"ts":                   time.Now().Format(time.RFC3339Nano),
		"type":                 "session_start",
		"app_name":             start.AppName,
		"context_sources":      start.ContextSources,
		"prompt_sources":       l.sanitizePromptSources(start.PromptSources),
		"workspace_root":       l.sanitizeText(start.WorkspaceRoot),
		"workspace_cwd":        l.sanitizeText(start.WorkspaceCWD),
		"workspace_mode":       start.WorkspaceMode,
		"explore_thoroughness": start.ExploreThoroughness,
	}
	if start.CtxSize > 0 {
		record["ctx_size"] = start.CtxSize
	}

	if l.mode == runprofile.DebugLogModeFull {
		record["system_prompt"] = start.SystemPrompt
	} else if digest := digestText(start.SystemPrompt); digest != "" {
		record["system_prompt_digest"] = digest
	}

	return l.writeRecord(record)
}

func (l *DebugLogger) sanitizePromptSources(sources []PromptSource) []PromptSource {
	if len(sources) == 0 {
		return nil
	}

	out := make([]PromptSource, 0, len(sources))
	for _, source := range sources {
		out = append(out, PromptSource{
			Kind:        strings.TrimSpace(source.Kind),
			DisplayName: strings.TrimSpace(source.DisplayName),
			Path:        l.sanitizeText(source.Path),
		})
	}

	return out
}

func (l *DebugLogger) LogContext(contextSources []string, systemPrompt string, workspaceMode string, thoroughness string) error {
	record := map[string]any{
		"ts":                   time.Now().Format(time.RFC3339Nano),
		"type":                 "context_update",
		"context_sources":      contextSources,
		"workspace_mode":       workspaceMode,
		"explore_thoroughness": thoroughness,
	}
	if l.mode == runprofile.DebugLogModeFull {
		record["system_prompt"] = systemPrompt
	} else if digest := digestText(systemPrompt); digest != "" {
		record["system_prompt_digest"] = digest
	}

	return l.writeRecord(record)
}

func (l *DebugLogger) LogToolRequest(step int, agent string, raw string, tool string, workspaceMode string, broad bool, narrowing bool, action string) error {
	return l.LogToolRequestDetail(ToolRequestDetail{
		Step:          step,
		Agent:         agent,
		Raw:           raw,
		Tool:          tool,
		WorkspaceMode: workspaceMode,
		Broad:         broad,
		Narrowing:     narrowing,
		Action:        action,
	})
}

func (l *DebugLogger) LogToolRequestDetail(d ToolRequestDetail) error {
	record := map[string]any{
		"ts":             time.Now().Format(time.RFC3339Nano),
		"type":           "tool_request",
		"step":           d.Step,
		"agent":          d.Agent,
		"tool":           d.Tool,
		"raw":            l.sanitizeToolRaw(d.Raw),
		"workspace_mode": d.WorkspaceMode,
		"broad":          d.Broad,
		"narrowing":      d.Narrowing,
		"action":         d.Action,
	}
	if strings.TrimSpace(d.DiscoveryClass) != "" {
		record["discovery_class"] = strings.TrimSpace(d.DiscoveryClass)
	}

	if strings.TrimSpace(d.DiscoveryScope) != "" {
		record["discovery_scope"] = strings.TrimSpace(d.DiscoveryScope)
	}

	if strings.TrimSpace(d.DiscoveryCost) != "" {
		record["discovery_cost"] = strings.TrimSpace(d.DiscoveryCost)
	}

	return l.writeRecord(record)
}

func (l *DebugLogger) LogToolResult(step int, agent string, tool string, summary string, errText string) error {
	return l.LogToolResultDetail(ToolResultDetail{
		Step:    step,
		Agent:   agent,
		Tool:    tool,
		Summary: summary,
		Error:   errText,
	})
}

func (l *DebugLogger) LogToolResultDetail(d ToolResultDetail) error {
	record := map[string]any{
		"ts":      time.Now().Format(time.RFC3339Nano),
		"type":    "tool_result",
		"step":    d.Step,
		"agent":   d.Agent,
		"tool":    d.Tool,
		"summary": l.sanitizeText(d.Summary),
		"error":   l.sanitizeText(d.Error),
	}
	if d.OutputChars > 0 {
		record["output_chars"] = d.OutputChars
	}

	if d.OutputLines > 0 {
		record["output_lines"] = d.OutputLines
	}

	if path := strings.TrimSpace(d.Path); path != "" {
		record["path"] = l.sanitizeText(path)
	}

	if d.StartLine > 0 {
		record["start_line"] = d.StartLine
	}

	if d.EndLine > 0 {
		record["end_line"] = d.EndLine
	}

	if d.Truncated {
		record["truncated"] = true
	}

	if strings.TrimSpace(d.DiscoveryClass) != "" {
		record["discovery_class"] = strings.TrimSpace(d.DiscoveryClass)
	}

	if strings.TrimSpace(d.DiscoveryScope) != "" {
		record["discovery_scope"] = strings.TrimSpace(d.DiscoveryScope)
	}

	if strings.TrimSpace(d.DiscoveryCost) != "" {
		record["discovery_cost"] = strings.TrimSpace(d.DiscoveryCost)
	}

	for key, value := range PrivacySafeToolResultFactFields(d.Facts) {
		record[key] = value
	}

	l.addToolResultExtraFields(record, d.ExtraFields)

	return l.writeRecord(record)
}

func (l *DebugLogger) addToolResultExtraFields(record map[string]any, fields map[string]any) {
	for key, value := range fields {
		key = strings.TrimSpace(key)
		if !strings.HasPrefix(key, "context_") {
			continue
		}

		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				record[key] = l.sanitizeText(typed)
			}
		case bool, int, int64, float64:
			record[key] = typed
		case []string:
			record[key] = typed
		}
	}
}

func (l *DebugLogger) LogModelCall(c ModelCall) error {
	record := map[string]any{
		"ts":            time.Now().Format(time.RFC3339Nano),
		"type":          "model_call",
		"step":          c.Step,
		"agent":         c.Agent,
		"message_count": c.MessageCount,
	}
	if c.PromptTokens > 0 {
		record["prompt_tokens"] = c.PromptTokens
	}

	if c.CompletionTokens > 0 {
		record["completion_tokens"] = c.CompletionTokens
	}

	if c.TotalTokens > 0 {
		record["total_tokens"] = c.TotalTokens
	}

	if c.PromptCacheAccountingKnown {
		record["prompt_cache_hit_tokens"] = c.PromptCacheHitTokens
		record["prompt_cache_miss_tokens"] = c.PromptCacheMissTokens
		record["prompt_cache_accounting_known"] = true
	}

	if c.ReasoningTokens > 0 {
		record["reasoning_tokens"] = c.ReasoningTokens
	}

	if c.CtxSize > 0 {
		record["ctx_size"] = c.CtxSize
		if c.PromptTokens > 0 {
			record["ctx_pct"] = roundPct(float64(c.PromptTokens) / float64(c.CtxSize))
		}
	}

	if len(c.RoleChars) > 0 {
		record["role_chars"] = c.RoleChars
	}

	if strings.TrimSpace(c.ChosenTool) != "" {
		record["chosen_tool"] = c.ChosenTool
	}

	if c.DurationMillis > 0 {
		record["duration_ms"] = c.DurationMillis
	}

	return l.writeRecord(record)
}

func (l *DebugLogger) LogTurnSummary(s TurnSummary) error {
	return l.writeRecord(BuildTurnSummaryRecord(s, time.Now()))
}

func (l *DebugLogger) LogSessionSummary(s SessionSummary) error {
	record := map[string]any{
		"ts":                            time.Now().Format(time.RFC3339Nano),
		"type":                          "session_summary",
		"turns":                         s.Turns,
		"model_calls":                   s.ModelCalls,
		"tool_calls":                    s.ToolCalls,
		"prompt_tokens":                 s.PromptTokens,
		"completion_tokens":             s.CompletionTokens,
		"total_tokens":                  s.TotalTokens,
		"prompt_cache_hit_tokens":       s.PromptCacheHitTokens,
		"prompt_cache_miss_tokens":      s.PromptCacheMissTokens,
		"prompt_cache_accounting_known": s.PromptCacheAccountingKnown,
		"reasoning_tokens":              s.ReasoningTokens,
		"peak_prompt_tokens":            s.PeakPromptTokens,
		"compactions":                   s.Compactions,
		"duration_ms":                   s.DurationMillis,
	}
	if s.CtxSize > 0 {
		record["ctx_size"] = s.CtxSize
		record["peak_ctx_pct"] = roundPct(s.PeakCtxPct)
	}

	if s.PromptCacheAccountingKnown && s.PromptCacheHitTokens+s.PromptCacheMissTokens > 0 {
		record["prompt_cache_hit_rate"] = roundPct(s.PromptCacheHitRate)
	}

	if len(s.TopToolsByOutput) > 0 {
		record["top_tools_by_output"] = s.TopToolsByOutput
	}

	if len(s.DiscoveryByClass) > 0 {
		record["discovery_by_class"] = s.DiscoveryByClass
	}

	if len(s.DiscoveryByCost) > 0 {
		record["discovery_by_cost"] = s.DiscoveryByCost
	}

	return l.writeRecord(record)
}

func roundPct(v float64) float64 {
	// Round to 4 decimals so JSON stays compact.
	return float64(int(v*10000+0.5)) / 10000
}

func (l *DebugLogger) LogMessage(role, content string) error {
	content = l.sanitizeText(content)

	record := map[string]any{
		"ts":      time.Now().Format(time.RFC3339Nano),
		"type":    "message",
		"role":    role,
		"content": content,
	}

	return l.writeMessageRecord(role, content, record)
}

func (l *DebugLogger) LogInputSubmission(content string) error {
	content = l.sanitizeText(content)

	return l.writeRecord(map[string]any{
		"ts":      time.Now().Format(time.RFC3339Nano),
		"type":    "input_submission",
		"content": content,
	})
}

func (l *DebugLogger) LogRunEvent(role, content string) error {
	content = l.sanitizeText(content)

	return l.writeRecord(map[string]any{
		"ts":      time.Now().Format(time.RFC3339Nano),
		"type":    "run_event",
		"role":    role,
		"content": content,
	})
}

func (l *DebugLogger) LogError(kind, message string) error {
	return l.writeRecord(map[string]any{
		"ts":         time.Now().Format(time.RFC3339Nano),
		"type":       "error",
		"error_kind": kind,
		"message":    l.sanitizeText(message),
	})
}

func (l *DebugLogger) LogServer(running bool, modelID string, owned bool, errText string) error {
	return l.writeRecord(map[string]any{
		"ts":       time.Now().Format(time.RFC3339Nano),
		"type":     "server",
		"running":  running,
		"model_id": modelID,
		"owned":    owned,
		"error":    l.sanitizeText(errText),
	})
}

func (l *DebugLogger) LogArtifact(kind, path string) error {
	return l.writeRecord(map[string]any{
		"ts":   time.Now().Format(time.RFC3339Nano),
		"type": "artifact",
		"kind": kind,
		"path": l.sanitizeText(path),
	})
}

func (l *DebugLogger) LogPhase(lane string, phase string, state string, detail string) error {
	return l.writeRecord(map[string]any{
		"ts":     time.Now().Format(time.RFC3339Nano),
		"type":   "phase",
		"lane":   lane,
		"phase":  phase,
		"state":  state,
		"detail": l.sanitizeText(detail),
	})
}

func (l *DebugLogger) sanitizeToolRaw(raw string) string {
	if l == nil || l.mode == runprofile.DebugLogModeFull || strings.TrimSpace(raw) == "" {
		return raw
	}

	var payload any

	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return l.sanitizeText(raw)
	}

	sanitized := l.sanitizeValue("", payload)

	encoded, err := json.Marshal(sanitized)
	if err != nil {
		return l.sanitizeText(raw)
	}

	return string(encoded)
}

func (l *DebugLogger) sanitizeValue(key string, value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, v := range typed {
			out[k] = l.sanitizeValue(k, v)
		}

		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, l.sanitizeValue("", item))
		}

		return out
	case string:
		switch key {
		case "content", "old_text", "new_text", "system_prompt":
			return redactedBlob(typed)
		case "command":
			return "<redacted shell command>"
		default:
			return l.sanitizeText(typed)
		}
	default:
		return value
	}
}

func (l *DebugLogger) sanitizeText(text string) string {
	if l == nil || l.mode == runprofile.DebugLogModeFull || strings.TrimSpace(text) == "" {
		return text
	}

	text = redactWorkspacePaths(text, l.workspaceRoot)
	text = redaction.RedactSecrets(text)

	return text
}

func (l *DebugLogger) writeRecord(record map[string]any) error {
	if l == nil || l.file == nil {
		return nil
	}

	marshal := json.Marshal
	if l.mode != runprofile.DebugLogModeFull {
		marshal = redaction.MarshalJSON
	}
	line, err := marshal(record)
	if err != nil {
		return fmt.Errorf("marshal debug log record: %w", err)
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if _, err := l.file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write debug log: %w", err)
	}

	return nil
}

func (l *DebugLogger) writeMessageRecord(role, content string, record map[string]any) error {
	if l == nil || l.file == nil {
		return nil
	}

	marshal := json.Marshal
	if l.mode != runprofile.DebugLogModeFull {
		marshal = redaction.MarshalJSON
	}
	line, err := marshal(record)
	if err != nil {
		return fmt.Errorf("marshal debug log record: %w", err)
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if isDuplicateAssistantSummaryMessage(role, content, l.lastSummaryContent) {
		l.lastSummaryContent = ""
		return nil
	}

	if _, err := l.file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write debug log: %w", err)
	}

	l.lastMessageRole = role
	l.lastMessageContent = content

	if isLaneSummaryRole(role) {
		l.lastSummaryContent = content
	} else if role == "assistant" {
		l.lastSummaryContent = ""
	}

	return nil
}

func isDuplicateAssistantSummaryMessage(role, content, previousSummaryContent string) bool {
	return role == "assistant" &&
		strings.TrimSpace(content) != "" &&
		content == previousSummaryContent
}

func isLaneSummaryRole(role string) bool {
	switch role {
	case "locate_summary", "explore_summary", "review_summary", "architecture_summary":
		return true
	default:
		return false
	}
}

func digestText(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}

	sum := sha256.Sum256([]byte(text))

	return fmt.Sprintf("sha256:%x", sum[:8])
}

func redactedBlob(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}

	return fmt.Sprintf("<redacted %d chars>", len(text))
}

func redactWorkspacePaths(text string, workspaceRoot string) string {
	workspaceRoot = filepath.Clean(strings.TrimSpace(workspaceRoot))
	if workspaceRoot == "" {
		return text
	}

	text = strings.ReplaceAll(text, workspaceRoot+string(filepath.Separator), "./")
	text = strings.ReplaceAll(text, workspaceRoot, ".")

	return text
}
