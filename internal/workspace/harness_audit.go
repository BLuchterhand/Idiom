package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"idiom/internal/comprehension"
	"idiom/internal/discovery"
	"idiom/internal/eval"
)

const (
	harnessAuditDefaultLogLimit = 25
	harnessAuditMaxLogLimit     = 200
)

type harnessAuditReport struct {
	GeneratedAt             string                   `json:"generated_at"`
	WorkspaceRoot           string                   `json:"workspace_root,omitempty"`
	Purpose                 string                   `json:"purpose"`
	Findings                []harnessAuditFinding    `json:"findings,omitempty"`
	ToolSurface             *harnessAuditToolSurface `json:"tool_surface,omitempty"`
	LanguageTooling         []harnessAuditLanguage   `json:"language_tooling,omitempty"`
	BackendAvailability     []harnessAuditBackend    `json:"backend_availability,omitempty"`
	LogBehavior             *harnessAuditLogBehavior `json:"log_behavior,omitempty"`
	ImprovementQuestions    []harnessAuditQuestion   `json:"improvement_questions,omitempty"`
	ConsolidationCandidates []harnessAuditQuestion   `json:"consolidation_candidates,omitempty"`
}

type harnessAuditFinding struct {
	ID             string   `json:"id"`
	Severity       string   `json:"severity"`
	Evidence       string   `json:"evidence"`
	Recommendation string   `json:"recommendation,omitempty"`
	Tools          []string `json:"tools,omitempty"`
}

type harnessAuditToolSurface struct {
	ManifestToolCount      int      `json:"manifest_tool_count"`
	SchemaToolCount        int      `json:"schema_tool_count"`
	DuplicateManifestTools []string `json:"duplicate_manifest_tools,omitempty"`
	ManifestWithoutSchema  []string `json:"manifest_without_schema,omitempty"`
	SchemaWithoutManifest  []string `json:"schema_without_manifest,omitempty"`
	InvalidSchemas         []string `json:"invalid_schemas,omitempty"`
	SuggestionToolCount    int      `json:"suggestion_tool_count"`
	SuggestionMissingTools []string `json:"suggestion_missing_tools,omitempty"`
}

type harnessAuditLanguage struct {
	Language     string                     `json:"language"`
	Active       bool                       `json:"active"`
	Extensions   []string                   `json:"extensions,omitempty"`
	Capabilities []harnessAuditLanguageTool `json:"capabilities"`
	Gaps         []string                   `json:"gaps,omitempty"`
}

type harnessAuditLanguageTool struct {
	Capability string `json:"capability"`
	Tool       string `json:"tool,omitempty"`
	InManifest bool   `json:"in_manifest"`
	HasSchema  bool   `json:"has_schema"`
}

type harnessAuditBackend struct {
	Name      string   `json:"name"`
	Binaries  []string `json:"binaries"`
	Available bool     `json:"available"`
	Path      string   `json:"path,omitempty"`
	Tools     []string `json:"tools,omitempty"`
	Impact    string   `json:"impact,omitempty"`
	Active    bool     `json:"active,omitempty"`
}

type harnessAuditLogBehavior struct {
	LogLimit           int                             `json:"log_limit"`
	LogsConsidered     int                             `json:"logs_considered"`
	LogsScanned        int                             `json:"logs_scanned"`
	Warnings           []string                        `json:"warnings,omitempty"`
	Aggregate          eval.PerfAggregate              `json:"aggregate"`
	TopPatterns        []eval.PerfPatternHit           `json:"top_patterns,omitempty"`
	TopTools           []eval.PerfToolMix              `json:"top_tools,omitempty"`
	WorstOffenders     []eval.PerfWorstOffender        `json:"worst_offenders,omitempty"`
	IncompleteSessions []harnessAuditIncompleteSession `json:"incomplete_sessions,omitempty"`
}

type harnessAuditIncompleteSession struct {
	TelemetryLog    string   `json:"telemetry_log,omitempty"`
	ConversationLog string   `json:"conversation_log,omitempty"`
	SessionID       string   `json:"session_id,omitempty"`
	Command         string   `json:"command,omitempty"`
	Missing         []string `json:"missing"`
}

type harnessAuditQuestion struct {
	Area      string `json:"area"`
	Question  string `json:"question"`
	Evidence  string `json:"evidence,omitempty"`
	Candidate string `json:"candidate,omitempty"`
}

var harnessAuditStandardCapabilities = []Capability{
	CapOverview,
	CapEntrypoints,
	CapFindSymbol,
	CapWorkspaceSymbol,
	CapDefinition,
	CapReferences,
	CapCallsFrom,
	CapCallsInto,
	CapReadSymbol,
	CapRelatedFiles,
	CapImplementations,
	CapDiagnostics,
	CapFileOutline,
}

func (m *Manager) harnessAudit(req ToolRequest) ToolResult {
	purpose := normalizeHarnessAuditPurpose(req.Purpose)
	report := harnessAuditReport{
		GeneratedAt:   time.Now().Format(time.RFC3339),
		WorkspaceRoot: m.cfg.Root,
		Purpose:       purpose,
	}

	if purpose == "all" || purpose == "tool_surface" {
		toolSurface := m.auditToolSurface()
		report.ToolSurface = &toolSurface
		report.LanguageTooling = m.auditLanguageTooling()
		report.BackendAvailability = m.auditBackendAvailability(report.LanguageTooling)
		report.Findings = append(report.Findings, toolSurfaceFindings(toolSurface)...)
		report.Findings = append(report.Findings, backendFindings(report.BackendAvailability)...)
		report.ConsolidationCandidates = append(report.ConsolidationCandidates, staticConsolidationCandidates(m.cfg.Root)...)
	}

	if purpose == "all" || purpose == "behavior" {
		behavior, findings, questions, err := m.auditLogBehavior(req)
		if err != nil {
			return ToolResult{Tool: "harness_audit", Summary: "Harness audit rejected", Error: err.Error()}
		}
		report.LogBehavior = behavior
		report.Findings = append(report.Findings, findings...)
		report.ImprovementQuestions = append(report.ImprovementQuestions, questions...)
	}
	report.ImprovementQuestions = append(report.ImprovementQuestions, staticImprovementQuestions(report)...)

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return ToolResult{Tool: "harness_audit", Summary: "Harness audit failed", Error: err.Error()}
	}

	return ToolResult{
		Tool:    "harness_audit",
		Summary: harnessAuditSummary(report),
		Output:  string(data),
	}
}

func normalizeHarnessAuditPurpose(purpose string) string {
	switch strings.ToLower(strings.TrimSpace(purpose)) {
	case "", "all", "audit":
		return "all"
	case "tool", "tools", "tooling", "tool_surface", "surface", "static":
		return "tool_surface"
	case "behavior", "behaviour", "logs", "log_behavior", "efficiency", "perf":
		return "behavior"
	default:
		return "all"
	}
}

func (m *Manager) auditToolSurface() harnessAuditToolSurface {
	manifest := ToolManifestTools(ToolManifestToolOptions{
		IncludeTaskLedger: true,
		IncludeTask:       true,
		IncludeWriteTools: true,
		IncludeGo:         true,
		IncludeCpp:        true,
		IncludePython:     true,
		IncludeTypescript: true,
		IncludeRust:       true,
		IncludeJava:       true,
	})
	manifestSet := auditSet(manifest)
	schemaDefs := BuildToolDefinitions(nil)
	schemaNames := make([]string, 0, len(schemaDefs))
	invalidSchemas := make([]string, 0)
	for _, def := range schemaDefs {
		schemaNames = append(schemaNames, def.Name)
		if !json.Valid([]byte(def.ParametersJSON)) {
			invalidSchemas = append(invalidSchemas, def.Name)
		}
	}
	schemaSet := auditSet(schemaNames)
	suggestSet := auditSet(builtinToolNamesForSuggest())

	return harnessAuditToolSurface{
		ManifestToolCount:      len(manifestSet),
		SchemaToolCount:        len(schemaSet),
		DuplicateManifestTools: auditDuplicates(manifest),
		ManifestWithoutSchema:  auditSetDiff(manifestSet, schemaSet),
		SchemaWithoutManifest:  auditSetDiff(schemaSet, manifestSet),
		InvalidSchemas:         invalidSchemas,
		SuggestionToolCount:    len(suggestSet),
		SuggestionMissingTools: auditSetDiff(manifestSet, suggestSet),
	}
}

func (m *Manager) auditLanguageTooling() []harnessAuditLanguage {
	manifest := auditSet(ToolManifestTools(ToolManifestToolOptions{
		IncludeTaskLedger: true,
		IncludeTask:       true,
		IncludeWriteTools: true,
		IncludeGo:         true,
		IncludeCpp:        true,
		IncludePython:     true,
		IncludeTypescript: true,
		IncludeRust:       true,
		IncludeJava:       true,
	}))
	defs := BuildToolDefinitions(nil)
	schemas := make(map[string]struct{}, len(defs))
	for _, def := range defs {
		schemas[def.Name] = struct{}{}
	}
	active := make(map[string]bool)
	for _, lang := range m.ActiveLanguages() {
		active[lang.Name] = true
	}

	out := make([]harnessAuditLanguage, 0, len(LanguageRegistry()))
	for _, lang := range LanguageRegistry() {
		row := harnessAuditLanguage{
			Language:   lang.Name,
			Active:     active[lang.Name],
			Extensions: append([]string(nil), lang.Extensions...),
		}
		for _, cap := range harnessAuditStandardCapabilities {
			tool := lang.Tool(cap)
			item := harnessAuditLanguageTool{
				Capability: string(cap),
				Tool:       tool,
			}
			if tool == "" {
				row.Gaps = append(row.Gaps, "missing tool for capability "+string(cap))
			} else {
				_, item.InManifest = manifest[tool]
				_, item.HasSchema = schemas[tool]
				if !item.InManifest {
					row.Gaps = append(row.Gaps, tool+" missing from manifest")
				}
				if !item.HasSchema {
					row.Gaps = append(row.Gaps, tool+" missing schema")
				}
			}
			row.Capabilities = append(row.Capabilities, item)
		}
		out = append(out, row)
	}
	return out
}

func (m *Manager) auditBackendAvailability(languages []harnessAuditLanguage) []harnessAuditBackend {
	active := make(map[string]bool)
	for _, lang := range languages {
		active[lang.Language] = lang.Active
	}
	backends := []harnessAuditBackend{
		{
			Name:     "content_search",
			Binaries: contentSearchBackendCandidates(),
			Tools:    []string{"search_content"},
			Impact:   "search_content needs ripgrep or GNU grep; without it, generic text search is unavailable.",
			Active:   true,
		},
		{
			Name:     "git",
			Binaries: []string{"git"},
			Tools:    []string{"git_status", "git_diff", "history_range", "history_symbol", "blame_range"},
			Impact:   "history and blame tools require git.",
			Active:   true,
		},
		{
			Name:     "ast_grep",
			Binaries: []string{"ast-grep", "sg"},
			Tools:    []string{"structural_search"},
			Impact:   "structural_search is unavailable without ast-grep.",
			Active:   true,
		},
		{
			Name:     "universal_ctags",
			Binaries: []string{"ctags"},
			Tools:    []string{"ctags_symbols"},
			Impact:   "ctags_symbols is the fallback symbol index for uncovered file types.",
			Active:   true,
		},
		{
			Name:     "semgrep",
			Binaries: []string{"semgrep"},
			Tools:    []string{"semgrep_scan"},
			Impact:   "security_review discovery packets degrade when semgrep is absent.",
			Active:   true,
		},
		{
			Name:     "joern",
			Binaries: []string{"joern-scan"},
			Tools:    []string{"joern_scan"},
			Impact:   "heavy code-property-graph scanning is unavailable without joern-scan.",
			Active:   true,
		},
		{
			Name:     "gopls",
			Binaries: []string{"gopls"},
			Tools:    []string{"go_definition", "go_references", "go_calls_from", "go_calls_into", "go_implementations"},
			Impact:   "Go tools still have syntax-index fallbacks, but implementation queries and highest precision require gopls.",
			Active:   active["go"],
		},
		{
			Name:     "clangd",
			Binaries: []string{"clangd"},
			Tools:    []string{"cpp_definition", "cpp_references", "cpp_calls_from", "cpp_calls_into"},
			Impact:   "C/C++ precision improves when clangd is available.",
			Active:   active["cpp"],
		},
		{
			Name:     "python_lsp",
			Binaries: []string{"pyright-langserver", "pylsp"},
			Tools:    []string{"python_definition", "python_references", "python_diagnostics"},
			Impact:   "Python location and diagnostics precision improves with pyright or pylsp.",
			Active:   active["python"],
		},
		{
			Name:     "typescript_language_server",
			Binaries: []string{"typescript-language-server"},
			Tools:    []string{"ts_definition", "ts_references", "ts_diagnostics"},
			Impact:   "TypeScript/JavaScript precision improves with typescript-language-server.",
			Active:   active["typescript"],
		},
		{
			Name:     "rust_analyzer",
			Binaries: []string{"rust-analyzer"},
			Tools:    []string{"rust_definition", "rust_references", "rust_diagnostics"},
			Impact:   "Rust precision improves with rust-analyzer; fallbacks use the syntactic index or rg.",
			Active:   active["rust"],
		},
		{
			Name:     "jdtls",
			Binaries: []string{"jdtls"},
			Tools:    []string{"java_definition", "java_references", "java_diagnostics"},
			Impact:   "Java precision improves with Eclipse JDT-LS.",
			Active:   active["java"],
		},
		{
			Name:     "madge",
			Binaries: []string{"madge"},
			Tools:    []string{"dependency_graph"},
			Impact:   "TypeScript dependency_graph falls back to local import extraction when madge is absent.",
			Active:   active["typescript"],
		},
		{
			Name:     "pydeps",
			Binaries: []string{"pydeps"},
			Tools:    []string{"dependency_graph"},
			Impact:   "Python dependency_graph falls back to local import extraction when pydeps is absent.",
			Active:   active["python"],
		},
	}
	for i := range backends {
		if backends[i].Name == "content_search" {
			if backend, err := resolveContentSearchBackend(); err == nil {
				backends[i].Available = true
				backends[i].Path = backend.bin
			}
			continue
		}
		if backends[i].Name == "universal_ctags" {
			if path := auditUniversalCtagsPath(); path != "" {
				backends[i].Available = true
				backends[i].Path = path
			}
			continue
		}
		if path := firstAvailableBinary(backends[i].Binaries...); path != "" {
			backends[i].Available = true
			backends[i].Path = path
		}
	}
	backends = append(backends, m.auditContextGraphBackend())
	return backends
}

func (m *Manager) auditContextGraphBackend() harnessAuditBackend {
	backend := harnessAuditBackend{
		Name:     "context_graph_sqlite",
		Tools:    []string{"context_index", "context_status", "context_query", "context_zoom", "context_pack", "context_trace_pack", "discovery_query"},
		Impact:   "Context graph tools use the embedded SQLite graph when present; discovery_query falls back to live probes when the graph is stale, collector-version-mismatched, missing, or incompatible.",
		Active:   true,
		Binaries: []string{"embedded:modernc.org/sqlite"},
	}
	status, err := comprehension.Status(m.cfg.Root)
	if err != nil {
		backend.Impact = backend.Impact + " context_status currently errors: " + err.Error()
		return backend
	}
	for _, storage := range status.StorageBackends {
		if storage.Active {
			backend.Path = storage.Path
			backend.Available = status.Indexed && storage.Compatible
			backend.Impact = fmt.Sprintf("%s Active backend %s is %s.", backend.Impact, storage.Kind, storage.Status)
			return backend
		}
	}
	backend.Available = false
	if !status.Indexed {
		backend.Impact = backend.Impact + " No context index is currently present."
	}
	return backend
}

func auditUniversalCtagsPath() string {
	path := firstAvailableBinary("ctags")
	if path == "" {
		return ""
	}
	output, err := exec.Command(path, "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	if !strings.Contains(strings.ToLower(string(output)), "universal ctags") {
		return ""
	}
	return path
}

func (m *Manager) auditLogBehavior(req ToolRequest) (*harnessAuditLogBehavior, []harnessAuditFinding, []harnessAuditQuestion, error) {
	limit := harnessAuditLogLimit(req.Limit)
	paths, warnings, err := m.auditLogPaths(req, limit)
	if err != nil {
		return nil, nil, nil, err
	}
	reports := make([]eval.PerfReport, 0, len(paths))
	for _, path := range paths {
		report, err := eval.AnalyzeLog(path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", m.displayPath(path), err))
			continue
		}
		report.SessionID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		report.LogPath = m.displayPath(path)
		reports = append(reports, report)
	}
	incompleteSessions, lifecycleWarnings := m.auditIncompleteImplementSessions(paths)
	warnings = append(warnings, lifecycleWarnings...)
	aggregate := eval.Aggregate(reports)
	behavior := &harnessAuditLogBehavior{
		LogLimit:           limit,
		LogsConsidered:     len(paths),
		LogsScanned:        len(reports),
		Warnings:           warnings,
		Aggregate:          aggregate,
		TopPatterns:        firstAuditPatterns(aggregate.PatternHitRate, 8),
		TopTools:           firstAuditTools(aggregate.ToolMix, 10),
		WorstOffenders:     aggregate.WorstOffenders,
		IncompleteSessions: incompleteSessions,
	}
	return behavior, logBehaviorFindings(aggregate, incompleteSessions), logBehaviorQuestions(aggregate), nil
}

type harnessAuditLogFacts struct {
	Command                 string
	WorkspaceRoot           string
	SessionID               string
	DebugLogPath            string
	HasProcessStart         bool
	HasProcessExit          bool
	HasModelSession         bool
	HasSessionSummary       bool
	IsImplementConversation bool
}

func (m *Manager) auditIncompleteImplementSessions(paths []string) ([]harnessAuditIncompleteSession, []string) {
	var incomplete []harnessAuditIncompleteSession
	var warnings []string
	seen := make(map[string]struct{})

	for _, path := range paths {
		facts, err := readHarnessAuditLogFacts(path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s lifecycle audit: %v", m.displayPath(path), err))
			continue
		}
		displayPath := m.displayPath(path)
		if facts.HasProcessStart && strings.EqualFold(strings.TrimSpace(facts.Command), "implement") {
			session := harnessAuditIncompleteSession{
				TelemetryLog: displayPath,
				SessionID:    strings.TrimSpace(facts.SessionID),
				Command:      "implement",
			}
			if !facts.HasProcessExit {
				session.Missing = append(session.Missing, "process_exit")
			}
			if facts.HasModelSession {
				if conversationPath := m.auditConversationPathFromTelemetry(facts); conversationPath != "" {
					session.ConversationLog = m.displayPath(conversationPath)
					conversationFacts, err := readHarnessAuditLogFacts(conversationPath)
					if err != nil {
						warnings = append(warnings, fmt.Sprintf("%s lifecycle audit: %v", session.ConversationLog, err))
					} else if !conversationFacts.HasSessionSummary {
						session.Missing = append(session.Missing, "session_summary")
					}
				} else if strings.TrimSpace(facts.DebugLogPath) != "" {
					warnings = append(warnings, fmt.Sprintf("%s lifecycle audit: debug log path %q is outside the audited workspace root or does not exist", displayPath, facts.DebugLogPath))
				} else {
					session.Missing = append(session.Missing, "session_summary")
				}
			}
			if len(session.Missing) > 0 {
				key := session.TelemetryLog + "|" + session.ConversationLog
				if _, ok := seen[key]; !ok {
					seen[key] = struct{}{}
					incomplete = append(incomplete, session)
				}
			}
			continue
		}
		if facts.IsImplementConversation && !facts.HasSessionSummary {
			session := harnessAuditIncompleteSession{
				ConversationLog: displayPath,
				SessionID:       strings.TrimSpace(facts.SessionID),
				Command:         "implement",
				Missing:         []string{"session_summary"},
			}
			key := session.TelemetryLog + "|" + session.ConversationLog
			if _, ok := seen[key]; !ok {
				seen[key] = struct{}{}
				incomplete = append(incomplete, session)
			}
		}
	}

	sort.Slice(incomplete, func(i, j int) bool {
		left := firstNonEmptyAuditSessionPath(incomplete[i])
		right := firstNonEmptyAuditSessionPath(incomplete[j])
		return left < right
	})
	return incomplete, warnings
}

func (m *Manager) auditConversationPathFromTelemetry(facts harnessAuditLogFacts) string {
	path := strings.TrimSpace(facts.DebugLogPath)
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(m.cfg.CWD, path)
	}
	path = filepath.Clean(path)
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	if m.pathWithinWorkspace(path) || auditPathWithinRoot(path, facts.WorkspaceRoot) {
		return path
	}
	return ""
}

func readHarnessAuditLogFacts(path string) (harnessAuditLogFacts, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return harnessAuditLogFacts{}, err
	}
	var facts harnessAuditLogFacts
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		eventType := auditStringField(record, "type")
		role := auditStringField(record, "role")
		switch eventType {
		case "process_start":
			facts.HasProcessStart = true
			facts.Command = firstNonEmptyAuditString(facts.Command, auditStringField(record, "command"))
			facts.WorkspaceRoot = firstNonEmptyAuditString(facts.WorkspaceRoot, auditStringField(record, "workspace_root"))
		case "session_start":
			facts.Command = firstNonEmptyAuditString(facts.Command, auditStringField(record, "command"))
			facts.WorkspaceRoot = firstNonEmptyAuditString(facts.WorkspaceRoot, auditStringField(record, "workspace_root"))
		case "process_exit":
			facts.HasProcessExit = true
		case "model_session":
			facts.HasModelSession = true
			facts.SessionID = firstNonEmptyAuditString(facts.SessionID, auditStringField(record, "session_id"))
			facts.DebugLogPath = firstNonEmptyAuditString(facts.DebugLogPath, auditStringField(record, "debug_log_path"))
		case "session_summary":
			facts.HasSessionSummary = true
		}
		if role == "control_rails" || eventType == "control_rails" {
			if auditControlRailsIndicateImplementation(record) {
				facts.IsImplementConversation = true
			}
		}
		if eventType == "session_start" && strings.EqualFold(auditStringField(record, "command"), "implement") {
			facts.IsImplementConversation = true
		}
	}
	if facts.SessionID == "" {
		facts.SessionID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	return facts, nil
}

func auditControlRailsIndicateImplementation(record map[string]any) bool {
	if strings.Contains(strings.ToLower(auditStringField(record, "content")), "implementation_result") {
		return true
	}
	for _, key := range []string{"contract", "task_family", "lane", "permission_profile"} {
		value := strings.ToLower(auditStringField(record, key))
		if strings.Contains(value, "implementation") || strings.Contains(value, "implement") {
			return true
		}
	}
	return false
}

func auditPathWithinRoot(path string, root string) bool {
	path = filepath.Clean(strings.TrimSpace(path))
	root = filepath.Clean(strings.TrimSpace(root))
	if path == "" || root == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../") && !strings.HasPrefix(rel, `..\`)
}

func auditStringField(record map[string]any, key string) string {
	if value, ok := record[key]; ok {
		if s, ok := value.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func firstNonEmptyAuditString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstNonEmptyAuditSessionPath(session harnessAuditIncompleteSession) string {
	if strings.TrimSpace(session.TelemetryLog) != "" {
		return strings.TrimSpace(session.TelemetryLog)
	}
	return strings.TrimSpace(session.ConversationLog)
}

func (m *Manager) auditLogPaths(req ToolRequest, limit int) ([]string, []string, error) {
	var warnings []string
	var paths []string
	defaultScope := false
	switch {
	case strings.TrimSpace(req.Glob) != "":
		matches, err := m.auditGlob(strings.TrimSpace(req.Glob))
		if err != nil {
			return nil, nil, err
		}
		paths = append(paths, matches...)
	case strings.TrimSpace(req.Path) != "":
		path := strings.TrimSpace(req.Path)
		if hasAuditGlobMagic(path) {
			matches, err := m.auditGlob(path)
			if err != nil {
				return nil, nil, err
			}
			paths = append(paths, matches...)
			break
		}
		target, err := m.resolvePath(path, req.Workdir)
		if err != nil {
			return nil, nil, err
		}
		info, err := os.Stat(target)
		if err != nil {
			return nil, nil, fmt.Errorf("audit log path %q: %w", path, err)
		}
		if info.IsDir() {
			matches, err := auditJSONLFilesUnder(target)
			if err != nil {
				return nil, nil, err
			}
			paths = append(paths, matches...)
		} else {
			paths = append(paths, target)
		}
	default:
		defaultScope = true
		for _, pattern := range []string{"logs/conversations/*.jsonl", "logs/e2e/*.jsonl", "logs/telemetry/*.jsonl"} {
			matches, err := m.auditGlob(pattern)
			if err != nil {
				warnings = append(warnings, err.Error())
				continue
			}
			matches = auditUniqueExistingFiles(matches)
			sortAuditLogPaths(matches)
			if len(matches) > limit {
				warnings = append(warnings, fmt.Sprintf("log scan capped at %d newest file(s) out of %d matched for %s", limit, len(matches), pattern))
				matches = matches[:limit]
			}
			paths = append(paths, matches...)
		}
	}
	paths = auditUniqueExistingFiles(paths)
	sortAuditLogPaths(paths)
	if len(paths) == 0 {
		warnings = append(warnings, "no JSONL behavior logs matched the audit scope")
		return paths, warnings, nil
	}
	if len(paths) > limit && !defaultScope {
		warnings = append(warnings, fmt.Sprintf("log scan capped at %d newest file(s) out of %d matched", limit, len(paths)))
		paths = paths[:limit]
	}
	return paths, warnings, nil
}

func (m *Manager) auditGlob(pattern string) ([]string, error) {
	if strings.TrimSpace(pattern) == "" {
		return nil, nil
	}
	var absPattern string
	if filepath.IsAbs(pattern) {
		absPattern = filepath.Clean(pattern)
	} else {
		absPattern = filepath.Clean(filepath.Join(m.cfg.CWD, pattern))
	}
	matches, err := filepath.Glob(absPattern)
	if err != nil {
		return nil, fmt.Errorf("audit glob %q: %w", pattern, err)
	}
	for _, match := range matches {
		if err := m.ensureAuditPathInWorkspace(match); err != nil {
			return nil, err
		}
	}
	return matches, nil
}

func (m *Manager) ensureAuditPathInWorkspace(path string) error {
	rel, err := filepath.Rel(m.cfg.Root, filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("resolve audit path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("audit path %q escapes workspace root", path)
	}
	return nil
}

func auditJSONLFilesUnder(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && discovery.ShouldSkipDirName(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".jsonl") {
			paths = append(paths, path)
		}
		return nil
	})
	return paths, err
}

func harnessAuditLogLimit(limit int) int {
	if limit <= 0 {
		return harnessAuditDefaultLogLimit
	}
	if limit > harnessAuditMaxLogLimit {
		return harnessAuditMaxLogLimit
	}
	return limit
}

func hasAuditGlobMagic(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

func auditUniqueExistingFiles(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.Clean(path)
		if _, dup := seen[path]; dup {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}

func sortAuditLogPaths(paths []string) {
	sort.Slice(paths, func(i, j int) bool {
		infoI, errI := os.Stat(paths[i])
		infoJ, errJ := os.Stat(paths[j])
		if errI == nil && errJ == nil && !infoI.ModTime().Equal(infoJ.ModTime()) {
			return infoI.ModTime().After(infoJ.ModTime())
		}
		return paths[i] < paths[j]
	})
}

func toolSurfaceFindings(surface harnessAuditToolSurface) []harnessAuditFinding {
	var findings []harnessAuditFinding
	if len(surface.ManifestWithoutSchema) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "manifest_without_schema",
			Severity:       "high",
			Evidence:       fmt.Sprintf("%d exported tool(s) have no function schema", len(surface.ManifestWithoutSchema)),
			Recommendation: "Add schemas or remove the tools from the exported manifest.",
			Tools:          surface.ManifestWithoutSchema,
		})
	}
	if len(surface.SchemaWithoutManifest) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "schema_without_manifest",
			Severity:       "medium",
			Evidence:       fmt.Sprintf("%d schema tool(s) are not listed in the exported manifest", len(surface.SchemaWithoutManifest)),
			Recommendation: "Add them to the manifest or delete stale schemas.",
			Tools:          surface.SchemaWithoutManifest,
		})
	}
	if len(surface.InvalidSchemas) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "invalid_tool_schema",
			Severity:       "high",
			Evidence:       fmt.Sprintf("%d tool schema(s) are not valid JSON", len(surface.InvalidSchemas)),
			Recommendation: "Fix the hand-authored JSON schema strings before advertising these tools.",
			Tools:          surface.InvalidSchemas,
		})
	}
	if len(surface.SuggestionMissingTools) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "unsupported_tool_suggestion_gap",
			Severity:       "low",
			Evidence:       fmt.Sprintf("%d manifest tool(s) are absent from typo suggestions", len(surface.SuggestionMissingTools)),
			Recommendation: "Generate typo suggestions from the manifest inventory.",
			Tools:          surface.SuggestionMissingTools,
		})
	}
	return findings
}

func backendFindings(backends []harnessAuditBackend) []harnessAuditFinding {
	var findings []harnessAuditFinding
	for _, backend := range backends {
		if backend.Available || !backend.Active {
			continue
		}
		severity := "medium"
		if backend.Name == "content_search" || backend.Name == "git" {
			severity = "high"
		}
		findings = append(findings, harnessAuditFinding{
			ID:             "backend_unavailable_" + backend.Name,
			Severity:       severity,
			Evidence:       fmt.Sprintf("%s not found on PATH; checked %s", backend.Name, strings.Join(backend.Binaries, ", ")),
			Recommendation: backend.Impact,
			Tools:          backend.Tools,
		})
	}
	return findings
}

func logBehaviorFindings(aggregate eval.PerfAggregate, incompleteSessions []harnessAuditIncompleteSession) []harnessAuditFinding {
	var findings []harnessAuditFinding
	for _, pattern := range aggregate.PatternHitRate {
		if pattern.LogsHit == 0 {
			continue
		}
		severity := "low"
		if pattern.HitRate >= 0.5 || pattern.TotalCount >= 5 {
			severity = "medium"
		}
		findings = append(findings, harnessAuditFinding{
			ID:       "behavior_" + pattern.Name,
			Severity: severity,
			Evidence: fmt.Sprintf("%s appeared in %d log(s), %d total hit(s)", pattern.Name, pattern.LogsHit, pattern.TotalCount),
		})
	}
	if aggregate.Feedback != nil && aggregate.Feedback.Total > 0 {
		severity := "low"
		if aggregate.Feedback.HighSeverity > 0 {
			severity = "high"
		} else if aggregate.Feedback.MediumSeverity > 0 || aggregate.Feedback.Total >= 3 {
			severity = "medium"
		}
		findings = append(findings, harnessAuditFinding{
			ID:             "agent_harness_feedback_reported",
			Severity:       severity,
			Evidence:       fmt.Sprintf("%d feedback event(s), %d verified, top category %s, top component %s", aggregate.Feedback.Total, aggregate.Feedback.Verified, auditTopCount(aggregate.Feedback.ByCategory), auditTopCount(aggregate.Feedback.ByComponent)),
			Recommendation: "Triage structured feedback before adding scenario-specific prompt text; prefer generic tool, telemetry, or contract fixes.",
			Tools:          []string{"harness_feedback", "harness_audit"},
		})
	}
	if len(incompleteSessions) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "incomplete_implement_session_lifecycle",
			Severity:       "high",
			Evidence:       fmt.Sprintf("%d implement session(s) missing process/session lifecycle event(s)", len(incompleteSessions)),
			Recommendation: "Inspect termination reasons and keep lifecycle closeout generic: every implement run should end with process_exit telemetry and a session_summary conversation event.",
			Tools:          []string{"harness_audit"},
		})
	}
	return findings
}

func staticImprovementQuestions(report harnessAuditReport) []harnessAuditQuestion {
	var questions []harnessAuditQuestion
	if report.ToolSurface != nil &&
		len(report.ToolSurface.ManifestWithoutSchema) == 0 &&
		len(report.ToolSurface.SchemaWithoutManifest) == 0 &&
		len(report.ToolSurface.InvalidSchemas) == 0 &&
		report.ToolSurface.ManifestToolCount > 0 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "tool_surface",
			Question:  "Should tool manifest, schema, and recovery inventories be generated from one source of truth?",
			Evidence:  fmt.Sprintf("%d manifest tools and %d schema tools currently align", report.ToolSurface.ManifestToolCount, report.ToolSurface.SchemaToolCount),
			Candidate: "Keep the audit as the drift detector first; generate only after repeated drift shows up.",
		})
	}
	activeLanguages := 0
	for _, lang := range report.LanguageTooling {
		if lang.Active {
			activeLanguages++
		}
	}
	if len(report.LanguageTooling) > 0 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "language_tooling",
			Question:  "Which language capabilities are only syntactic fallbacks in practice, and do they need LSP setup, better fallbacks, or clearer docs?",
			Evidence:  fmt.Sprintf("%d language families registered; %d active in this workspace", len(report.LanguageTooling), activeLanguages),
			Candidate: "Use behavior logs plus precision_tier results to prioritize one language/backend at a time.",
		})
	}
	return questions
}

func staticConsolidationCandidates(root string) []harnessAuditQuestion {
	candidates := []harnessAuditQuestion{
		{
			Area:      "language_tooling",
			Question:  "Can repeated per-language capability schemas and examples be derived from LanguageRegistry?",
			Evidence:  "Go, C/C++, Python, TypeScript, Rust, and Java expose the same standard capability matrix with language-prefixed tools.",
			Candidate: "Generate conformance checks and manifest examples from the capability registry before generating implementation code.",
		},
	}
	if fileExists(filepath.Join(root, "cmd", "idiom-analyze", "main.go")) &&
		fileExists(filepath.Join(root, "cmd", "idiom-perf-eval", "main.go")) {
		candidates = append(candidates, harnessAuditQuestion{
			Area:      "behavior_logs",
			Question:  "Should legacy log-shape mining and perf anti-pattern reporting converge behind one audit command?",
			Evidence:  "Both cmd/idiom-analyze and cmd/idiom-perf-eval inspect JSONL behavior logs for improvement opportunities.",
			Candidate: "Keep specialized CLIs while harness_audit becomes the shared evidence packet for agents.",
		})
	}
	return candidates
}

func logBehaviorQuestions(aggregate eval.PerfAggregate) []harnessAuditQuestion {
	if aggregate.LogCount == 0 {
		return []harnessAuditQuestion{{
			Area:      "behavior_logs",
			Question:  "Which behavior logs should be captured to make harness improvement evidence-based?",
			Evidence:  "No JSONL logs were scanned.",
			Candidate: "Run representative tasks with JSONL logging, then re-run harness_audit with purpose=behavior.",
		}}
	}
	var questions []harnessAuditQuestion
	if hit := auditPatternHit(aggregate, "zero_hit_search"); hit.TotalCount > 0 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "discovery_tooling",
			Question:  "Do repeated zero-hit searches point to missing query normalization, better symbol fallback, or a dedicated question tool?",
			Evidence:  fmt.Sprintf("zero_hit_search hit %d time(s) across %d log(s)", hit.TotalCount, hit.LogsHit),
			Candidate: "Cluster the raw failed patterns before adding a new tool; if they share shape, add one bounded deterministic probe.",
		})
	}
	if hit := auditPatternHit(aggregate, "dir_walk_streak"); hit.TotalCount > 0 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "discovery_behavior",
			Question:  "Should broad directory walking redirect earlier into discovery_query or language overview tools?",
			Evidence:  fmt.Sprintf("dir_walk_streak hit %d time(s) across %d log(s)", hit.TotalCount, hit.LogsHit),
			Candidate: "Prefer nudges and manifest shaping before hard-blocking safe reads.",
		})
	}
	if hit := auditPatternHit(aggregate, "rejection_retry"); hit.TotalCount > 0 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "control_rails",
			Question:  "Are rejection packets clear enough, or should the harness hide the rejected tool/shape sooner?",
			Evidence:  fmt.Sprintf("rejection_retry hit %d time(s) across %d log(s)", hit.TotalCount, hit.LogsHit),
			Candidate: "Keep at least one safe model-directed path open; hide only repeated wasteful shapes.",
		})
	}
	if hit := auditPatternHit(aggregate, "malformed_edit_args"); hit.TotalCount > 0 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "tool_schema",
			Question:  "Do write tools need stricter schemas, clearer examples, or host-side argument repair?",
			Evidence:  fmt.Sprintf("malformed_edit_args hit %d time(s) across %d log(s)", hit.TotalCount, hit.LogsHit),
			Candidate: "Tighten schema/examples first; add repair only for unambiguous transformations.",
		})
	}
	if aggregate.Feedback != nil && aggregate.Feedback.Total > 0 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "agent_feedback",
			Question:  "Which reported idiom harness/tool/prompt gap should be fixed before adding more task-specific guidance?",
			Evidence:  fmt.Sprintf("%d feedback event(s); top category %s; top component %s", aggregate.Feedback.Total, auditTopCount(aggregate.Feedback.ByCategory), auditTopCount(aggregate.Feedback.ByComponent)),
			Candidate: "Cluster by component and fix the smallest generic contract that removes the workaround.",
		})
	}
	if hit := auditPatternHit(aggregate, "prompt_surface_overlap"); hit.TotalCount > 0 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "prompt_surface",
			Question:  "Is degraded behavior coming from overlapping prompt ownership rather than missing guidance?",
			Evidence:  fmt.Sprintf("prompt_surface_overlap hit %d time(s) across %d log(s)", hit.TotalCount, hit.LogsHit),
			Candidate: "Move repeated obligations into typed runtime briefs, deterministic checks, or one canonical prompt owner; delete duplicate prose before adding a new rule.",
		})
	}
	genericReads := auditToolCalls(aggregate, "list_files", "search_files", "search_content", "read_file")
	languageReads := auditToolPrefixCalls(aggregate, "go_", "cpp_", "python_", "ts_", "rust_", "java_")
	if genericReads >= 10 && genericReads > languageReads*2 {
		questions = append(questions, harnessAuditQuestion{
			Area:      "language_tool_selection",
			Question:  "Are agents missing language-specific tools, or are those tools failing and causing fallback to generic reads?",
			Evidence:  fmt.Sprintf("generic read/search calls=%d, language-specific calls=%d", genericReads, languageReads),
			Candidate: "Sample logs with high generic-read share and inspect precision_tier/unavailable results before adding tools.",
		})
	}
	return questions
}

func harnessAuditSummary(report harnessAuditReport) string {
	parts := []string{fmt.Sprintf("Ran harness audit (%s)", report.Purpose)}
	if report.ToolSurface != nil && report.ToolSurface.ManifestToolCount > 0 {
		drift := len(report.ToolSurface.ManifestWithoutSchema) + len(report.ToolSurface.SchemaWithoutManifest) + len(report.ToolSurface.InvalidSchemas)
		parts = append(parts, fmt.Sprintf("tool_surface=%d tools/%d drift", report.ToolSurface.ManifestToolCount, drift))
	}
	if report.LogBehavior != nil {
		parts = append(parts, fmt.Sprintf("logs=%d scanned", report.LogBehavior.LogsScanned))
	}
	if len(report.Findings) > 0 {
		parts = append(parts, fmt.Sprintf("findings=%d", len(report.Findings)))
	}
	if len(report.ImprovementQuestions) > 0 {
		parts = append(parts, fmt.Sprintf("questions=%d", len(report.ImprovementQuestions)))
	}
	return strings.Join(parts, "; ")
}

func firstAuditPatterns(in []eval.PerfPatternHit, n int) []eval.PerfPatternHit {
	if len(in) <= n {
		return append([]eval.PerfPatternHit(nil), in...)
	}
	return append([]eval.PerfPatternHit(nil), in[:n]...)
}

func firstAuditTools(in []eval.PerfToolMix, n int) []eval.PerfToolMix {
	if len(in) <= n {
		return append([]eval.PerfToolMix(nil), in...)
	}
	return append([]eval.PerfToolMix(nil), in[:n]...)
}

func auditTopCount(values map[string]int) string {
	bestKey := "none"
	bestValue := 0
	for key, value := range values {
		if value > bestValue || value == bestValue && key < bestKey {
			bestKey = key
			bestValue = value
		}
	}
	if bestValue == 0 {
		return "none"
	}
	return fmt.Sprintf("%s=%d", bestKey, bestValue)
}

func auditPatternHit(aggregate eval.PerfAggregate, name string) eval.PerfPatternHit {
	for _, hit := range aggregate.PatternHitRate {
		if hit.Name == name {
			return hit
		}
	}
	return eval.PerfPatternHit{Name: name}
}

func auditToolCalls(aggregate eval.PerfAggregate, names ...string) int {
	want := make(map[string]struct{}, len(names))
	for _, name := range names {
		want[name] = struct{}{}
	}
	total := 0
	for _, tool := range aggregate.ToolMix {
		if _, ok := want[tool.Tool]; ok {
			total += tool.Calls
		}
	}
	return total
}

func auditToolPrefixCalls(aggregate eval.PerfAggregate, prefixes ...string) int {
	total := 0
	for _, tool := range aggregate.ToolMix {
		for _, prefix := range prefixes {
			if strings.HasPrefix(tool.Tool, prefix) {
				total += tool.Calls
				break
			}
		}
	}
	return total
}

func auditSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = struct{}{}
		}
	}
	return out
}

func auditSetDiff(left map[string]struct{}, right map[string]struct{}) []string {
	var out []string
	for value := range left {
		if _, ok := right[value]; !ok {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func auditDuplicates(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	dups := make(map[string]struct{})
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			dups[value] = struct{}{}
			continue
		}
		seen[value] = struct{}{}
	}
	out := make([]string, 0, len(dups))
	for value := range dups {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
