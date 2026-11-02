package workspace

import (
	"strings"

	"idiom/internal/platform/textutil"
	semanticlanguage "idiom/internal/repository/semantic/language"
	"idiom/internal/repository/semantic/languagetools"
)

type ToolManifestEntry struct {
	Tool                     string   `json:"tool"`
	Request                  string   `json:"request"`
	Purpose                  string   `json:"purpose,omitempty"`
	EvidenceCapabilities     []string `json:"evidence_capabilities,omitempty"`
	CapabilityClassification string   `json:"capability_classification,omitempty"`
}

type ToolManifestToolOptions struct {
	IncludeTaskLedger bool
	IncludeTask       bool
	IncludeWriteTools bool
	// IncludePlanThenExecute exposes the submit_plan tool and hides
	// the per-edit write tools (edit_file, write_file, append_file,
	// apply_patch). Used by the plan-then-execute
	// orchestrator's planner phase.
	IncludePlanThenExecute bool
	// PlanThenExecutePlannerSurface narrows the built-in manifest to the
	// planner lane's actual contract. It is intentionally separate from
	// IncludePlanThenExecute so review/repair can share submit_plan while
	// keeping its own review-shaped read surface.
	PlanThenExecutePlannerSurface bool
	IncludeGo                     bool
	IncludeCpp                    bool
	IncludePython                 bool
	IncludeTypescript             bool
	IncludeRust                   bool
	IncludeJava                   bool
	IncludeGDScript               bool
}

func ToolManifestTools(opts ToolManifestToolOptions) []string {
	var tools []string

	if opts.IncludeTaskLedger {
		tools = append(tools, ToolNameTaskLedger)
	}

	if opts.IncludeTask {
		tools = append(tools, ToolNameTask)
	}

	tools = append(tools, builtInToolSurfaceCatalog().toolsWhere(func(spec toolSurfaceSpec) bool {
		return spec.DefaultManifest
	})...)
	if opts.IncludeWriteTools && !opts.IncludePlanThenExecute {
		tools = append(tools, builtInToolSurfaceCatalog().toolsWhere(func(spec toolSurfaceSpec) bool {
			return spec.Category == CategoryWrite && strings.TrimSpace(spec.Language) == ""
		})...)
	}

	if opts.IncludePlanThenExecute {
		if metadata, ok := builtInToolSurfaceCatalog().lookup(ToolNameSubmitPlan); ok {
			tools = append(tools, metadata.Tool)
		}
	}

	if opts.IncludeGo {
		tools = appendLanguageManifestTools(tools, "go")
		if opts.IncludeWriteTools {
			tools = append(tools, ToolNameGoMoveDeclarations)
		}
	}

	if opts.IncludeCpp {
		tools = appendLanguageManifestTools(tools, "cpp")
	}

	if opts.IncludePython {
		tools = appendLanguageManifestTools(tools, "python")
	}

	if opts.IncludeTypescript {
		tools = appendLanguageManifestTools(tools, "typescript")
	}

	if opts.IncludeRust {
		tools = appendLanguageManifestTools(tools, "rust")
	}

	if opts.IncludeJava {
		tools = appendLanguageManifestTools(tools, "java")
	}

	if opts.IncludeGDScript {
		tools = appendLanguageManifestTools(tools, "gdscript")
	}

	if opts.PlanThenExecutePlannerSurface {
		tools = filterManifestToolNames(tools, PlanExecutePlannerToolNames()...)
	}

	return tools
}

func appendLanguageManifestTools(tools []string, languageName string) []string {
	lang, ok := semanticlanguage.LanguageForName(languageName)
	if !ok {
		return tools
	}

	for _, spec := range languagetools.ToolCatalog() {
		if spec.Language == lang.Name {
			tools = append(tools, spec.Tool)
		}
	}

	return tools
}

func filterManifestToolNames(tools []string, names ...string) []string {
	allowed := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			allowed[name] = struct{}{}
		}
	}

	var out []string

	for _, tool := range tools {
		if _, ok := allowed[strings.TrimSpace(tool)]; ok {
			out = append(out, tool)
		}
	}

	return out
}

func toolManifestToolsAcrossBuiltInSurfaces() []string {
	opts := ToolManifestToolOptions{
		IncludeTaskLedger: true,
		IncludeTask:       true,
		IncludeWriteTools: true,
		IncludeGo:         true,
		IncludeCpp:        true,
		IncludePython:     true,
		IncludeTypescript: true,
		IncludeRust:       true,
		IncludeJava:       true,
		IncludeGDScript:   true,
	}
	tools := ToolManifestTools(opts)

	opts.IncludePlanThenExecute = true
	tools = append(tools, ToolManifestTools(opts)...)

	return textutil.UniqueNonEmpty(tools)
}

func WorkspaceWriteToolNames() []string {
	return builtInToolSurfaceToolsByCategory(CategoryWrite)
}

func WorkspacePerEditWriteToolNames() []string {
	return builtInToolSurfaceToolsByFacet(toolSurfaceFacetDirectEditAdapter)
}

func IsWorkspacePerEditWriteTool(tool string) bool {
	return builtInToolSurfaceHasFacet(tool, toolSurfaceFacetDirectEditAdapter)
}

func IsWorkspaceWriteTool(tool string) bool {
	return ClassifyTool(tool) == CategoryWrite
}

func toolNameSet(tools []string) map[string]struct{} {
	out := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if tool != "" {
			out[tool] = struct{}{}
		}
	}
	return out
}

func BuildToolManifest(allowedTools []string) []ToolManifestEntry {
	allowed := make(map[string]struct{}, len(allowedTools))
	for _, tool := range allowedTools {
		tool = strings.TrimSpace(tool)
		if tool != "" {
			allowed[tool] = struct{}{}
		}
	}

	includeAll := len(allowed) == 0
	entries := []ToolManifestEntry{
		{Tool: ToolNameTaskLedger, Request: `{"tool":"task_ledger","todos":[{"id":"inspect","content":"Inspect target files","status":"in_progress"}]}`, Purpose: "track complex multi-step work"},
		{Tool: ToolNameTask, Request: `{"tool":"task","agent":"explorer","mode":"read_only","prompt":"Inspect one focused architecture/review question, answer once the evidence is sufficient, and summarize grounded evidence with repo paths."}`, Purpose: "delegate broad evidence gathering to a bounded read-only explorer"},
		{Tool: ToolNamePwd, Request: `{"tool":"pwd"}`, Purpose: "show current workspace directory"},
		{Tool: ToolNameListFiles, Request: `{"tool":"list_files","path":".","recursive":true,"max_depth":2}`, Purpose: "bounded file discovery"},
		{Tool: ToolNameSearchFiles, Request: `{"tool":"search_files","pattern":"name","path":".","named_gap":"locate the exact file that defines the child-task handoff before answering"}`, Purpose: "find files by name"},
		{Tool: ToolNameSearchContent, Request: `{"tool":"search_content","pattern":"symbol|text","path":".","glob":"*.go","fixed_strings":false,"named_gap":"find one exact branch in a known scope after a semantic probe or packet did not fully resolve the question","why_not_answering":"without the deciding branch, the explanation may overstate how delegation is triggered"}`, Purpose: "find regex or exact literal text when semantic tools or discovered handles are not enough; set fixed_strings=true for punctuation-heavy snippets"},
		{Tool: ToolNameRg, Request: `{"tool":"rg","pattern":"symbol|text","path":".","glob":"*.go","fixed_strings":false,"named_gap":"run the literal-search fallback when semantic tools do not fully resolve the target"}`, Purpose: "ripgrep-compatible literal or regex content search fallback"},
		{Tool: ToolNameReadFile, Request: `{"tool":"read_file","path":"relative/file.go","start_line":1,"end_line":120,"named_gap":"verify one exact function body or narrow range now that the target file is known","why_not_answering":"I still need the implementation body, not just the file name"}`, Purpose: "read exact file ranges"},
		{Tool: ToolNameFileStats, Request: `{"tool":"file_stats","path":".","recursive":true,"glob":"*.go","max_depth":3,"limit":1000}`, Purpose: "count files, bytes, and lines without shell wc/find pipelines"},
		{Tool: ToolNameCodeNavigate, Request: CodeNavigateRequestExample(semanticlanguage.CapReadSymbol, "relative/file.go", "Run"), Purpose: "request a code navigation capability; the harness routes by file language to the best symbol/navigation tool or a bounded generic fallback"},
		{Tool: ToolNameReadOnlyCapability, Request: `{"tool":"read_only_capability","capability":"definition","path":"relative/file.go","pattern":"Run"}`, Purpose: "request a stable read-only capability ID; the harness resolves it through existing read-only adapters and reports any fallback reason"},
		{Tool: ToolNameSourceSanity, Request: `{"tool":"source_sanity","path":"relative/file.cpp"}`, Purpose: "source-aware lane sanity: dispatch to the file language's diagnostics when available; generic fallback is advisory and does not satisfy C/C++ terminal sanity without concrete diagnostics"},
		{Tool: ToolNameCodingRulesCheck, Request: `{"tool":"coding_rules_check","path":"."}`, Purpose: "run project coding rules and return typed findings without raw source bodies"},
		{Tool: ToolNameHarnessAudit, Request: `{"tool":"harness_audit","purpose":"all","path":"logs/conversations","limit":25}`, Purpose: "audit idiom's own tool surface, backend availability, and recent behavior logs for evidence-backed improvement questions"},
		{Tool: ToolNameHarnessFeedback, Request: `{"tool":"harness_feedback","category":"packet_quality","component":"context_pack","expected":"one task-ready packet that made the edit target clear","observed":"the packet lacked the symbol edge needed to choose between two files","workaround":"used exact symbol tools and a narrow file read","impact":"extra read loop before editing","evidence":["logs/conversations/session.jsonl step 8"],"confidence":"verified","severity":"medium"}`, Purpose: "record structured agent-observed idiom harness/tool/prompt gaps into telemetry for later audit triage. REQUIRED fields: category, component, expected, observed, impact, confidence (all strings, all non-empty); evidence must be a non-empty array of repo-relative paths or step ids; category must come from the documented enum exactly. Validation rejects partial submissions silently — fill every required field on the first call."},
		{Tool: ToolNameRecoveryInspect, Request: `{"tool":"recovery_inspect","session_id":"session-001"}`, Purpose: "read a durable recovery failure record and live workspace state without mutating files"},
		{Tool: ToolNameRecoveryStatus, Request: `{"tool":"recovery_status","packet_path":".idiom/recovery/plan-execute-session-001/packet.json"}`, Purpose: "read recovery packet and reconciliation status, blockers, gates, candidate atoms, and next action"},
		{Tool: ToolNameRecoveryDecompose, Request: `{"tool":"recovery_decompose","session_id":"session-001"}`, Purpose: "write or rebuild a local .idiom/recovery decomposition packet from an existing failure record"},
		{Tool: ToolNameProductEfforts, Request: `{"tool":"product_efforts","root":"docs/product-workflows/efforts","status":"all","query":""}`, Purpose: "read product workflow effort summaries by status bucket without running inference or mutating workflow state"},
		{Tool: ToolNameProductStatus, Request: `{"tool":"product_status","effort_root":"docs/product-workflows/efforts/closed/prd-adr-brief-workflow","operator_view":true}`, Purpose: "read one product workflow effort's status or operator continuation view without running inference or mutating workflow state"},
		{Tool: ToolNameContextIndex, Request: `{"tool":"context_index","path":".","language":"auto","mode":"standard"}`, Purpose: "comprehension admin: build or refresh the persistent language-agnostic codebase comprehension graph with backend/build-system enrichment"},
		{Tool: ToolNameContextStatus, Request: `{"tool":"context_status"}`, Purpose: "comprehension admin: report context index freshness, stale files, stale symbols, and collector-version freshness"},
		{Tool: ToolNameContextQuery, Request: `{"tool":"context_query","query":"find all PermissionProfile constants and their usages across the codebase","resolution":"symbol","include_evidence":true}`, Purpose: "comprehension query: query the persistent codebase graph, including durable summary nodes, without dumping source files. Especially useful for broad investigation questions before reading individual files."},
		{Tool: ToolNameContextZoom, Request: `{"tool":"context_zoom","id":"symbol://internal/surfaces/tui/app/app.go#Run","resolution":"symbol","include_source":false}`, Purpose: "comprehension query: zoom into one context graph handle and related nodes"},
		{Tool: ToolNameContextPack, Request: `{"tool":"context_pack","task":"plan a bounded change to Run","mode":"plan","limit":8}`, Purpose: "comprehension query: generate a saved packet-ready context view with candidate files, symbols, tests, risks, and gaps"},
		{Tool: ToolNameContextImpact, Request: `{"tool":"context_impact","task":"plan a bounded change to Run","limit":8}`, Purpose: "rank task-relevant files, symbols, and tests with signal explanations from the context graph"},
		{Tool: ToolNameContextTracePack, Request: `{"tool":"context_trace_pack","command":"go test ./... -json","language":"auto","limit":64}`, Purpose: "run a command and save language-agnostic runtime evidence such as test events, stack frames, executed files, and coverage"},
		{Tool: ToolNameDiscoveryQuery, Request: `{"tool":"discovery_query","purpose":"architecture_trace","path":".","pattern":"PermissionProfile","language":"go","max_depth":2,"named_gap":"gather one bounded evidence packet for the remaining uncertainty before choosing a specific file read","why_not_answering":"I need one reproducible map of the surface before drilling into a single implementation point"}`, Purpose: "run a deterministic layered discovery recipe and persist the evidence packet. Use this to answer broad architecture/review questions in 1-2 calls instead of 50+ separate symbol lookups."},
		{Tool: ToolNameStructuralSearch, Request: `{"tool":"structural_search","pattern":"if $COND { $BODY }","language":"go","path":"."}`, Purpose: "AST structural search via ast-grep"},
		{Tool: ToolNameDependencyGraph, Request: `{"tool":"dependency_graph","language":"go","path":"."}`, Purpose: "summarize package/module/import dependencies"},
		{Tool: ToolNameHistoryRange, Request: `{"tool":"history_range","path":"file.go"}`, Purpose: "show git history for a file, or add paired start_line/end_line for a narrower range"},
		{Tool: ToolNameHistorySymbol, Request: `{"tool":"history_symbol","path":"file.go","pattern":"FunctionName"}`, Purpose: "show git history for a function or symbol"},
		{Tool: ToolNameBlameRange, Request: `{"tool":"blame_range","path":"file.go","start_line":10,"end_line":40}`, Purpose: "show git blame for a line range"},
		{Tool: ToolNameCtagsSymbols, Request: `{"tool":"ctags_symbols","path":".","pattern":"Handler"}`, Purpose: "fallback symbol index via Universal Ctags"},
		{Tool: ToolNameSemgrepScan, Request: `{"tool":"semgrep_scan","path":".","pattern":"auto"}`, Purpose: "dataflow/security scan via Semgrep"},
		{Tool: ToolNameJoernScan, Request: `{"tool":"joern_scan","path":"."}`, Purpose: "heavy code-property-graph security scan via Joern"},
		{Tool: ToolNameGoMoveDeclarations, Request: `{"tool":"go_move_declarations","path":"source.go","destination":"target.go","pattern":"tag:responsibility"}`, Purpose: "mechanically move Go declarations"},
		{Tool: ToolNameMkdir, Request: `{"tool":"mkdir","path":"relative/dir"}`, Purpose: "create a directory"},
		{Tool: ToolNameEditFile, Request: `{"tool":"edit_file","path":"relative/file","old_text":"exact substring from file","new_text":"replacement text","description":"replace the confirmed text to satisfy the requested behavior"}`, Purpose: "bounded edit by exact substring match (preferred for existing source/test files; old_text must appear verbatim; description is required)"},
		{Tool: ToolNameEditFile, Request: `{"tool":"edit_file","path":"relative/file","start_line":10,"end_line":12,"content":"replacement\n","description":"replace the confirmed line range to satisfy the requested behavior"}`, Purpose: "bounded edit by line range (use when you know exact lines). Must provide content — NOT old_text/new_text with this shape. Description is required."},
		{Tool: ToolNameWriteFile, Request: `{"tool":"write_file","path":"relative/file","content":"full file contents","description":"write the complete requested file content"}`, Purpose: "full-body write for missing/new files or non-source deliverables; prefer edit_file/apply_patch for existing source/test files; description is required"},
		{Tool: ToolNameAppendFile, Request: `{"tool":"append_file","path":"relative/file","content":"text to append","description":"append the requested content at the existing file boundary"}`, Purpose: "append text only when end-of-file append is explicitly intended; prefer edit_file/apply_patch for anchored source/test edits; description is required"},
		{Tool: ToolNameApplyPatch, Request: `{"tool":"apply_patch","content":"*** Begin Patch\n*** Update File: relative/file\n@@\n-old\n+new\n*** End Patch","description":"apply the requested patch across the affected file set"}`, Purpose: "multi-file patch envelope. Use ONLY these *** directives: Begin Patch, End Patch, Add File, Update File, Delete File, Move to. Hunks (`@@`) inside Update File. Do not invent extras like *** Sanity Check / *** Note / *** Verification. Description is required."},
		{Tool: ToolNameSubmitPlan, Request: `{"tool":"submit_plan","rationale":"one paragraph on the overall plan","edits":[{"path":"relative/file","old_text":"exact current substring","new_text":"complete replacement text","purpose":"why this edit"}]}`, Purpose: "submit a structured executable edit plan (plan-then-execute mode only). `edits` is a JSON array, not a quoted string. Edit bodies must be complete final content, not placeholders or prose. Use only for supported planner edits or review-repair corrective edits; clean review-repair closes with Decision: pass. Large bodies are spilled to artifact refs after submission."},
		{Tool: ToolNameGitStatus, Request: `{"tool":"git_status","workdir":"."}`, Purpose: "inspect git state"},
		{Tool: ToolNameGitDiff, Request: `{"tool":"git_diff","workdir":".","path":"optional/path"}`, Purpose: "inspect git diff"},
		{Tool: ToolNameSystemSearchAccess, Request: `{"tool":"request_system_search_access","path":"../idiom","purpose":"locate the sibling repository the user asked about before reading or editing anything"}`, Purpose: "ask the operator to approve read-only access to a filesystem file or directory outside the configured workspace. Approval is session-scoped; approve grants only the selected target, while approve all grants the selected directory's contents. It does not permit shell commands or writes outside the workspace."},
		{Tool: ToolNameShell, Request: `{"tool":"shell","command":"go test ./...","workdir":"."}`, Purpose: "run an allowed shell command"},
	}

	entries = mergeLanguageToolManifestEntries(entries)

	out := make([]ToolManifestEntry, 0, len(entries))

	for _, entry := range entries {
		entry = attachToolManifestCapabilityMetadata(entry)
		if includeAll {
			out = append(out, entry)
			continue
		}

		if _, ok := allowed[entry.Tool]; ok {
			out = append(out, entry)
		}
	}

	return SanitizeToolManifestRequests(out)
}

func mergeLanguageToolManifestEntries(entries []ToolManifestEntry) []ToolManifestEntry {
	catalog := languageToolManifestEntries()
	if len(catalog) == 0 {
		return entries
	}

	out := make([]ToolManifestEntry, 0, len(entries)+len(catalog))
	inserted := false

	for _, entry := range entries {
		if languagetools.IsToolName(entry.Tool) {
			if !inserted {
				out = append(out, catalog...)
				inserted = true
			}

			continue
		}

		out = append(out, entry)
	}

	if !inserted {
		out = append(out, catalog...)
	}

	return out
}

// SanitizeToolManifestRequests preserves manifest tools/purposes while removing
// copyable request examples that contain schema placeholders. The tool manifest
// is a model-facing boundary, so every caller should share this predicate.
func SanitizeToolManifestRequests(manifest []ToolManifestEntry) []ToolManifestEntry {
	if len(manifest) == 0 {
		return nil
	}

	out := append([]ToolManifestEntry(nil), manifest...)
	for i := range out {
		if toolManifestRequestLooksPlaceholder(out[i].Request) {
			out[i].Request = ""
		}
	}

	return out
}

func toolManifestRequestLooksPlaceholder(request string) bool {
	lower := strings.ToLower(strings.TrimSpace(request))

	return textutil.ContainsAny(lower,
		"relative/file",
		"candidate/path.go",
		"symbol|text",
		`"pattern":"name"`,
		`"pattern":"funcname"`,
		`"pattern":"functionname"`,
		`"pattern":"typename"`,
		`"pattern":"constname"`,
		`"pattern":"interfacename"`,
		`"pattern":"classname`,
		`"pattern":"methodname"`,
		`"path":"file.go"`,
		`"path":"source.go"`,
		`"destination":"target.go"`,
		`"path":"src/file`,
		`"path":"src/pkg/file.py"`,
		`"path":"src/main/java/com/example/file.java"`,
		`"path":"include/file.h"`,
		`"path":"relative/dir"`,
		`"path":"optional/path"`,
		"session-001",
		"session.jsonl",
		"exact current",
		"exact substring",
		"replacement text",
		"complete replacement",
		"one paragraph",
		"why this edit",
		"<",
	)
}
