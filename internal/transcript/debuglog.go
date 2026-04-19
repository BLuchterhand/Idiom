package transcript

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type DebugLogMode string

const (
	DebugLogModeRedacted DebugLogMode = "redacted"
	DebugLogModeFull     DebugLogMode = "full"
)

type DebugLoggerOptions struct {
	Dir           string
	Mode          DebugLogMode
	WorkspaceRoot string
}

type DebugLogger struct {
	mu                 sync.Mutex
	path               string
	file               *os.File
	mode               DebugLogMode
	workspaceRoot      string
	lastMessageRole    string
	lastMessageContent string
	lastSummaryContent string
}

type SessionStart struct {
	AppName             string   `json:"app_name,omitempty"`
	ContextSources      []string `json:"context_sources,omitempty"`
	SystemPrompt        string   `json:"system_prompt,omitempty"`
	SystemPromptDigest  string   `json:"system_prompt_digest,omitempty"`
	WorkspaceRoot       string   `json:"workspace_root,omitempty"`
	WorkspaceCWD        string   `json:"workspace_cwd,omitempty"`
	WorkspaceMode       string   `json:"workspace_mode,omitempty"`
	ExploreThoroughness string   `json:"explore_thoroughness,omitempty"`
}

func NewDebugLogger(opts DebugLoggerOptions) (*DebugLogger, error) {
	if strings.TrimSpace(opts.Dir) == "" {
		opts.Dir = "logs/conversations"
	}
	if opts.Mode == "" {
		opts.Mode = DebugLogModeRedacted
	}
	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("create debug log dir: %w", err)
	}

	filename := filepath.Join(opts.Dir, time.Now().Format("20060102-150405.000")+".jsonl")
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open debug log: %w", err)
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

func (l *DebugLogger) Mode() DebugLogMode {
	if l == nil {
		return DebugLogModeRedacted
	}
	return l.mode
}

func (l *DebugLogger) SetMode(mode DebugLogMode) {
	if l == nil {
		return
	}
	if mode == "" {
		mode = DebugLogModeRedacted
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
		"workspace_root":       l.sanitizeText(start.WorkspaceRoot),
		"workspace_cwd":        l.sanitizeText(start.WorkspaceCWD),
		"workspace_mode":       start.WorkspaceMode,
		"explore_thoroughness": start.ExploreThoroughness,
	}
	if l.mode == DebugLogModeFull {
		record["system_prompt"] = start.SystemPrompt
	} else if digest := digestText(start.SystemPrompt); digest != "" {
		record["system_prompt_digest"] = digest
	}
	return l.writeRecord(record)
}

func (l *DebugLogger) LogContext(contextSources []string, systemPrompt string, workspaceMode string, thoroughness string) error {
	record := map[string]any{
		"ts":                   time.Now().Format(time.RFC3339Nano),
		"type":                 "context_update",
		"context_sources":      contextSources,
		"workspace_mode":       workspaceMode,
		"explore_thoroughness": thoroughness,
	}
	if l.mode == DebugLogModeFull {
		record["system_prompt"] = systemPrompt
	} else if digest := digestText(systemPrompt); digest != "" {
		record["system_prompt_digest"] = digest
	}
	return l.writeRecord(record)
}

func (l *DebugLogger) LogToolRequest(step int, agent string, raw string, tool string, workspaceMode string, broad bool, narrowing bool, action string) error {
	return l.writeRecord(map[string]any{
		"ts":             time.Now().Format(time.RFC3339Nano),
		"type":           "tool_request",
		"step":           step,
		"agent":          agent,
		"tool":           tool,
		"raw":            l.sanitizeToolRaw(raw),
		"workspace_mode": workspaceMode,
		"broad":          broad,
		"narrowing":      narrowing,
		"action":         action,
	})
}

func (l *DebugLogger) LogToolResult(step int, agent string, tool string, summary string, errText string) error {
	return l.writeRecord(map[string]any{
		"ts":      time.Now().Format(time.RFC3339Nano),
		"type":    "tool_result",
		"step":    step,
		"agent":   agent,
		"tool":    tool,
		"summary": summary,
		"error":   errText,
	})
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
	if l == nil || l.mode == DebugLogModeFull || strings.TrimSpace(raw) == "" {
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
	if l == nil || l.mode == DebugLogModeFull || strings.TrimSpace(text) == "" {
		return text
	}
	text = redactWorkspacePaths(text, l.workspaceRoot)
	text = redactSecrets(text)
	return text
}

func (l *DebugLogger) writeRecord(record map[string]any) error {
	if l == nil || l.file == nil {
		return nil
	}

	line, err := json.Marshal(record)
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

	line, err := json.Marshal(record)
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

var (
	apiKeyPattern    = regexp.MustCompile(`(?i)\b(api[_-]?key|token|secret|password)\b\s*[:=]\s*([^\s,;]+)`)
	bearerPattern    = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._-]+\b`)
	openAIKeyPattern = regexp.MustCompile(`\bsk-[A-Za-z0-9]{12,}\b`)
)

func redactSecrets(text string) string {
	text = apiKeyPattern.ReplaceAllString(text, `$1=<redacted>`)
	text = bearerPattern.ReplaceAllString(text, `Bearer <redacted>`)
	text = openAIKeyPattern.ReplaceAllString(text, `<redacted-secret>`)
	return text
}
