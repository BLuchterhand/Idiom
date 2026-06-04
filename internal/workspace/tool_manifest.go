package workspace

import (
	"slices"
	"strings"

	"idiom/internal/autonomy"
	"idiom/internal/util"
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
	// apply_patch, delegate_edit). Used by the plan-then-execute
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
}

func ToolManifestTools(opts ToolManifestToolOptions) []string {
	var tools []string
	if opts.IncludeTaskLedger {
		tools = append(tools, "task_ledger")
	}
	if opts.IncludeTask {
		tools = append(tools, "task")
	}
	tools = append(tools, defaultToolManifestTools()...)
	if opts.IncludeWriteTools && !opts.IncludePlanThenExecute {
		tools = append(tools, workspaceWriteToolNamesWithoutLanguageSpecific()...)
	}
	if opts.IncludePlanThenExecute {
		if metadata, ok := builtInToolSurfaceMetadata("submit_plan"); ok {
			tools = append(tools, metadata.Tool)
		}
	}
	if opts.IncludeGo {
		tools = appendLanguageManifestTools(tools, "go")
		if opts.IncludeWriteTools {
			tools = append(tools, "go_move_declarations")
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
	if opts.PlanThenExecutePlannerSurface {
		tools = filterManifestToolNames(tools, planThenExecutePlannerManifestToolNames()...)
	}
	return tools
}

type languageManifestToolToken struct {
	Capability   Capability
	Supplemental string
}

var defaultLanguageManifestToolOrder = []languageManifestToolToken{
	{Capability: CapOverview},
	{Capability: CapEntrypoints},
	{Capability: CapFindSymbol},
	{Capability: CapReadSymbol},
	{Capability: CapRelatedFiles},
	{Capability: CapDefinition},
	{Capability: CapImplementations},
	{Capability: CapReferences},
	{Capability: CapCallsFrom},
	{Capability: CapCallsInto},
	{Capability: CapWorkspaceSymbol},
	{Capability: CapDiagnostics},
	{Capability: CapFileOutline},
}

var languageManifestToolOrders = map[string][]languageManifestToolToken{
	"go": {
		{Capability: CapOverview},
		{Capability: CapEntrypoints},
		{Capability: CapFindSymbol},
		{Supplemental: "go_find_function"},
		{Supplemental: "go_find_type"},
		{Supplemental: "go_find_const"},
		{Supplemental: "go_find_package"},
		{Capability: CapRelatedFiles},
		{Capability: CapWorkspaceSymbol},
		{Capability: CapReferences},
		{Capability: CapDefinition},
		{Capability: CapImplementations},
		{Capability: CapCallsFrom},
		{Capability: CapCallsInto},
		{Capability: CapReadSymbol},
		{Supplemental: "go_extract_body"},
		{Supplemental: "go_importers"},
		{Capability: CapDiagnostics},
		{Capability: CapFileOutline},
	},
	"cpp": {
		{Capability: CapOverview},
		{Capability: CapEntrypoints},
		{Capability: CapFindSymbol},
		{Capability: CapReadSymbol},
		{Capability: CapRelatedFiles},
		{Capability: CapReferences},
		{Capability: CapDefinition},
		{Supplemental: "cpp_declaration"},
		{Capability: CapImplementations},
		{Capability: CapCallsFrom},
		{Capability: CapCallsInto},
		{Supplemental: "cpp_extract_body"},
		{Supplemental: "cpp_includes"},
		{Supplemental: "cpp_who_includes"},
		{Supplemental: "cpp_blast_radius"},
		{Supplemental: "cpp_dead_symbol"},
		{Capability: CapWorkspaceSymbol},
		{Capability: CapDiagnostics},
		{Capability: CapFileOutline},
	},
	"python": {
		{Capability: CapOverview},
		{Capability: CapEntrypoints},
		{Capability: CapFindSymbol},
		{Capability: CapReadSymbol},
		{Capability: CapRelatedFiles},
		{Capability: CapReferences},
		{Capability: CapDefinition},
		{Capability: CapImplementations},
		{Capability: CapCallsFrom},
		{Capability: CapCallsInto},
		{Capability: CapWorkspaceSymbol},
		{Capability: CapDiagnostics},
		{Capability: CapFileOutline},
	},
	"typescript": {
		{Capability: CapOverview},
		{Capability: CapEntrypoints},
		{Capability: CapFindSymbol},
		{Capability: CapReadSymbol},
		{Capability: CapRelatedFiles},
		{Capability: CapDefinition},
		{Capability: CapImplementations},
		{Capability: CapReferences},
		{Capability: CapCallsFrom},
		{Capability: CapCallsInto},
		{Capability: CapWorkspaceSymbol},
		{Capability: CapDiagnostics},
		{Capability: CapFileOutline},
	},
	"rust": {
		{Capability: CapOverview},
		{Capability: CapEntrypoints},
		{Capability: CapFindSymbol},
		{Capability: CapWorkspaceSymbol},
		{Capability: CapReadSymbol},
		{Capability: CapRelatedFiles},
		{Capability: CapFileOutline},
		{Capability: CapDefinition},
		{Capability: CapReferences},
		{Capability: CapCallsFrom},
		{Capability: CapCallsInto},
		{Capability: CapImplementations},
		{Capability: CapDiagnostics},
	},
	"java": {
		{Capability: CapOverview},
		{Capability: CapEntrypoints},
		{Capability: CapFindSymbol},
		{Capability: CapReadSymbol},
		{Capability: CapRelatedFiles},
		{Capability: CapWorkspaceSymbol},
		{Capability: CapDefinition},
		{Capability: CapReferences},
		{Capability: CapImplementations},
		{Capability: CapCallsFrom},
		{Capability: CapCallsInto},
		{Capability: CapDiagnostics},
		{Capability: CapFileOutline},
	},
}

func appendLanguageManifestTools(tools []string, languageName string) []string {
	lang, ok := LanguageForName(languageName)
	if !ok {
		return tools
	}
	order := defaultLanguageManifestToolOrder
	if languageOrder := languageManifestToolOrders[lang.Name]; len(languageOrder) > 0 {
		order = languageOrder
	}
	for _, token := range order {
		switch {
		case token.Capability != "":
			if tool := strings.TrimSpace(lang.Tools[token.Capability]); tool != "" {
				tools = append(tools, tool)
			}
		case token.Supplemental != "":
			tool := strings.TrimSpace(token.Supplemental)
			if !slices.Contains(supplementalLanguageToolsForManifest(lang.Name), tool) {
				continue
			}
			tools = append(tools, tool)
		}
	}
	return tools
}

func planThenExecutePlannerManifestToolNames() []string {
	return autonomy.PlanExecutePlannerToolNames()
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
	}
	tools := ToolManifestTools(opts)
	opts.IncludePlanThenExecute = true
	tools = append(tools, ToolManifestTools(opts)...)
	return util.UniqueNonEmpty(tools)
}

func WorkspaceWriteToolNames() []string {
	return builtInToolSurfaceToolsByCategory(CategoryWrite)
}

func workspaceWriteToolNamesWithoutLanguageSpecific() []string {
	return builtInToolSurfaceToolsByCategoryWithoutLanguageSpecific(CategoryWrite)
}

func IsWorkspaceWriteTool(tool string) bool {
	return slices.Contains(WorkspaceWriteToolNames(), strings.TrimSpace(tool))
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
		{Tool: "task_ledger", Request: `{"tool":"task_ledger","todos":[{"id":"inspect","content":"Inspect target files","status":"in_progress"}]}`, Purpose: "track complex multi-step work"},
		{Tool: "task", Request: `{"tool":"task","agent":"explorer","mode":"read_only","prompt":"Inspect one focused architecture/review question, answer once the evidence is sufficient, and summarize grounded evidence with repo paths."}`, Purpose: "delegate broad evidence gathering to a bounded read-only explorer"},
		{Tool: "pwd", Request: `{"tool":"pwd"}`, Purpose: "show current workspace directory"},
		{Tool: "list_files", Request: `{"tool":"list_files","path":".","recursive":true,"max_depth":2}`, Purpose: "bounded file discovery"},
		{Tool: "search_files", Request: `{"tool":"search_files","pattern":"name","path":".","named_gap":"locate the exact file that defines the child-task handoff before answering"}`, Purpose: "find files by name"},
		{Tool: "search_content", Request: `{"tool":"search_content","pattern":"symbol|text","path":".","glob":"*.go","named_gap":"find one exact branch in a known scope after a semantic probe or packet did not fully resolve the question","why_not_answering":"without the deciding branch, the explanation may overstate how delegation is triggered"}`, Purpose: "find exact text when semantic tools or discovered handles are not enough"},
		{Tool: "read_file", Request: `{"tool":"read_file","path":"relative/file.go","start_line":1,"end_line":120,"named_gap":"verify one exact function body or narrow range now that the target file is known","why_not_answering":"I still need the implementation body, not just the file name"}`, Purpose: "read exact file ranges"},
		{Tool: "file_stats", Request: `{"tool":"file_stats","path":".","recursive":true,"glob":"*.go","max_depth":3,"limit":1000}`, Purpose: "count files, bytes, and lines without shell wc/find pipelines"},
		{Tool: "code_navigate", Request: `{"tool":"code_navigate","capability":"read_symbol","path":"relative/file.go","pattern":"Run"}`, Purpose: "request a code navigation capability; the harness routes by file language to the best symbol/navigation tool or a bounded generic fallback"},
		{Tool: "read_only_capability", Request: `{"tool":"read_only_capability","capability":"definition","path":"relative/file.go","pattern":"Run"}`, Purpose: "request a stable read-only capability ID; the harness resolves it through existing read-only adapters and reports any fallback reason"},
		{Tool: "source_sanity", Request: `{"tool":"source_sanity","path":"relative/file.cpp"}`, Purpose: "source-aware lane sanity: dispatch to the file language's diagnostics when available, otherwise use a generic source syntax sanity fallback"},
		{Tool: "harness_audit", Request: `{"tool":"harness_audit","purpose":"all","path":"logs/conversations","limit":25}`, Purpose: "audit idiom's own tool surface, backend availability, and recent behavior logs for evidence-backed improvement questions"},
		{Tool: "harness_feedback", Request: `{"tool":"harness_feedback","category":"packet_quality","component":"context_pack","expected":"one task-ready packet that made the edit target clear","observed":"the packet lacked the symbol edge needed to choose between two files","workaround":"used exact symbol tools and a narrow file read","impact":"extra read loop before editing","evidence":["logs/conversations/session.jsonl step 8"],"confidence":"verified","severity":"medium"}`, Purpose: "record structured agent-observed idiom harness/tool/prompt gaps into telemetry for later audit triage. REQUIRED fields: category, component, expected, observed, impact, confidence (all strings, all non-empty); evidence must be a non-empty array of repo-relative paths or step ids; category must come from the documented enum exactly. Validation rejects partial submissions silently — fill every required field on the first call."},
		{Tool: "recovery_inspect", Request: `{"tool":"recovery_inspect","session_id":"session-001"}`, Purpose: "read a durable recovery failure record and live workspace state without mutating files"},
		{Tool: "recovery_status", Request: `{"tool":"recovery_status","packet_path":".idiom/recovery/plan-execute-session-001/packet.json"}`, Purpose: "read recovery packet and reconciliation status, blockers, gates, candidate atoms, and next action"},
		{Tool: "recovery_decompose", Request: `{"tool":"recovery_decompose","session_id":"session-001"}`, Purpose: "write or rebuild a local .idiom/recovery decomposition packet from an existing failure record"},
		{Tool: "product_efforts", Request: `{"tool":"product_efforts","root":"docs/product-workflows/efforts","status":"all","query":""}`, Purpose: "read product workflow effort summaries by status bucket without running inference or mutating workflow state"},
		{Tool: "product_status", Request: `{"tool":"product_status","effort_root":"docs/product-workflows/efforts/closed/prd-adr-brief-workflow","operator_view":true}`, Purpose: "read one product workflow effort's status or operator continuation view without running inference or mutating workflow state"},
		{Tool: "context_index", Request: `{"tool":"context_index","path":".","language":"auto","mode":"standard"}`, Purpose: "build or refresh the persistent language-agnostic codebase comprehension graph with backend/build-system enrichment"},
		{Tool: "context_status", Request: `{"tool":"context_status"}`, Purpose: "report context index freshness, stale files, stale symbols, and collector-version freshness"},
		{Tool: "context_query", Request: `{"tool":"context_query","query":"what calls Run","resolution":"symbol","include_evidence":true}`, Purpose: "query the persistent codebase graph, including durable summary nodes, without dumping source files"},
		{Tool: "context_zoom", Request: `{"tool":"context_zoom","id":"symbol://internal/app/app.go#Run","resolution":"symbol","include_source":false}`, Purpose: "zoom into one context graph handle and related nodes"},
		{Tool: "context_pack", Request: `{"tool":"context_pack","task":"plan a bounded change to Run","mode":"plan","limit":8}`, Purpose: "generate a saved packet-ready context view with candidate files, symbols, tests, risks, and gaps"},
		{Tool: "context_impact", Request: `{"tool":"context_impact","task":"plan a bounded change to Run","limit":8}`, Purpose: "rank task-relevant files, symbols, and tests with signal explanations from the context graph"},
		{Tool: "context_trace_pack", Request: `{"tool":"context_trace_pack","command":"go test ./... -json","language":"auto","limit":64}`, Purpose: "run a command and save language-agnostic runtime evidence such as test events, stack frames, executed files, and coverage"},
		{Tool: "discovery_query", Request: `{"tool":"discovery_query","purpose":"impact_analysis","path":"file.go","pattern":"FunctionName","language":"go","max_depth":2,"named_gap":"gather one bounded evidence packet for the remaining uncertainty before choosing a specific file read","why_not_answering":"I need one reproducible map of the surface before drilling into a single implementation point"}`, Purpose: "run a deterministic layered discovery recipe and persist the evidence packet"},
		{Tool: "structural_search", Request: `{"tool":"structural_search","pattern":"if $COND { $BODY }","language":"go","path":"."}`, Purpose: "AST structural search via ast-grep"},
		{Tool: "dependency_graph", Request: `{"tool":"dependency_graph","language":"go","path":"."}`, Purpose: "summarize package/module/import dependencies"},
		{Tool: "history_range", Request: `{"tool":"history_range","path":"file.go"}`, Purpose: "show git history for a file, or add paired start_line/end_line for a narrower range"},
		{Tool: "history_symbol", Request: `{"tool":"history_symbol","path":"file.go","pattern":"FunctionName"}`, Purpose: "show git history for a function or symbol"},
		{Tool: "blame_range", Request: `{"tool":"blame_range","path":"file.go","start_line":10,"end_line":40}`, Purpose: "show git blame for a line range"},
		{Tool: "ctags_symbols", Request: `{"tool":"ctags_symbols","path":".","pattern":"Handler"}`, Purpose: "fallback symbol index via Universal Ctags"},
		{Tool: "semgrep_scan", Request: `{"tool":"semgrep_scan","path":".","pattern":"auto"}`, Purpose: "dataflow/security scan via Semgrep"},
		{Tool: "joern_scan", Request: `{"tool":"joern_scan","path":"."}`, Purpose: "heavy code-property-graph security scan via Joern"},
		{Tool: "go_overview", Request: `{"tool":"go_overview"}`, Purpose: "summarize Go workspace"},
		{Tool: "go_entrypoints", Request: `{"tool":"go_entrypoints"}`, Purpose: "find Go entrypoints"},
		{Tool: "go_find_symbol", Request: `{"tool":"go_find_symbol","pattern":"Name"}`, Purpose: "find Go symbols"},
		{Tool: "go_find_function", Request: `{"tool":"go_find_function","pattern":"FuncName"}`, Purpose: "find Go functions"},
		{Tool: "go_find_type", Request: `{"tool":"go_find_type","pattern":"TypeName"}`, Purpose: "find Go types"},
		{Tool: "go_find_const", Request: `{"tool":"go_find_const","pattern":"ConstName"}`, Purpose: "find Go constants"},
		{Tool: "go_find_package", Request: `{"tool":"go_find_package","pattern":"internal/app"}`, Purpose: "find Go packages"},
		{Tool: "go_related_files", Request: `{"tool":"go_related_files","pattern":"routing","path":"internal/app"}`, Purpose: "find related Go files"},
		{Tool: "go_workspace_symbol", Request: `{"tool":"go_workspace_symbol","pattern":"Name","named_gap":"locate one exact symbol that could prove or falsify the remaining claim before answering","why_not_answering":"without the exact symbol, the follow-up read would still be a guess"}`, Purpose: "workspace-wide Go symbol lookup"},
		{Tool: "go_references", Request: `{"tool":"go_references","pattern":"Name","path":"."}`, Purpose: "find Go references"},
		{Tool: "go_definition", Request: `{"tool":"go_definition","pattern":"Name","path":"."}`, Purpose: "find Go definitions"},
		{Tool: "go_implementations", Request: `{"tool":"go_implementations","pattern":"InterfaceName","path":"."}`, Purpose: "find Go implementations"},
		{Tool: "go_calls_from", Request: `{"tool":"go_calls_from","pattern":"FuncName","path":"."}`, Purpose: "find outgoing Go calls"},
		{Tool: "go_calls_into", Request: `{"tool":"go_calls_into","pattern":"FuncName","path":"."}`, Purpose: "find incoming Go calls"},
		{Tool: "go_read_symbol", Request: `{"tool":"go_read_symbol","pattern":"Name","path":"relative/file.go"}`, Purpose: "read a Go symbol body. ANCHOR FIRST via go_find_symbol/go_workspace_symbol/lookup or a prior tool result; speculative pattern guesses fail. For literal text or expressions inside a file, use search_content instead — read_symbol is for symbol bodies, not substring search."},
		{Tool: "go_extract_body", Request: `{"tool":"go_extract_body","pattern":"FuncName","path":"relative/file.go"}`, Purpose: "extract a Go body"},
		{Tool: "go_importers", Request: `{"tool":"go_importers","pattern":"internal/app"}`, Purpose: "find Go importer packages"},
		{Tool: "go_diagnostics", Request: `{"tool":"go_diagnostics","path":"relative/file.go"}`, Purpose: "run Go diagnostics"},
		{Tool: "go_file_outline", Request: `{"tool":"go_file_outline","path":"relative/file.go"}`, Purpose: "inspect declaration boundaries for Go refactors"},
		{Tool: "go_move_declarations", Request: `{"tool":"go_move_declarations","path":"source.go","destination":"target.go","pattern":"tag:responsibility"}`, Purpose: "mechanically move Go declarations"},
		{Tool: "cpp_overview", Request: `{"tool":"cpp_overview"}`, Purpose: "summarize C/C++ workspace"},
		{Tool: "cpp_entrypoints", Request: `{"tool":"cpp_entrypoints"}`, Purpose: "find C/C++ entrypoints"},
		{Tool: "cpp_find_symbol", Request: `{"tool":"cpp_find_symbol","pattern":"Name"}`, Purpose: "find C/C++ symbols"},
		{Tool: "cpp_read_symbol", Request: `{"tool":"cpp_read_symbol","pattern":"Name","path":"src/file.cpp"}`, Purpose: "read a C/C++ symbol body. ANCHOR FIRST via cpp_find_symbol/cpp_workspace_symbol/lookup or a prior tool result; speculative pattern guesses fail ~56% of the time. For literal text or expressions inside a file, use search_content instead — read_symbol is for symbol bodies, not substring search of code fragments like 'static_cast<int>(X)' or assertion text."},
		{Tool: "cpp_related_files", Request: `{"tool":"cpp_related_files","pattern":"Name","path":"src"}`, Purpose: "find related C/C++ files"},
		{Tool: "cpp_references", Request: `{"tool":"cpp_references","pattern":"Name","path":"."}`, Purpose: "find C/C++ references"},
		{Tool: "cpp_definition", Request: `{"tool":"cpp_definition","pattern":"Name","path":"."}`, Purpose: "find C/C++ definitions"},
		{Tool: "cpp_declaration", Request: `{"tool":"cpp_declaration","pattern":"Name","path":"."}`, Purpose: "find C/C++ declarations"},
		{Tool: "cpp_implementations", Request: `{"tool":"cpp_implementations","pattern":"Name","path":"."}`, Purpose: "find C/C++ implementations"},
		{Tool: "cpp_calls_from", Request: `{"tool":"cpp_calls_from","pattern":"Name","path":"."}`, Purpose: "find outgoing C/C++ calls"},
		{Tool: "cpp_calls_into", Request: `{"tool":"cpp_calls_into","pattern":"Name","path":"."}`, Purpose: "find incoming C/C++ calls"},
		{Tool: "cpp_extract_body", Request: `{"tool":"cpp_extract_body","pattern":"Name","path":"src/file.cpp"}`, Purpose: "extract a C/C++ body"},
		{Tool: "cpp_includes", Request: `{"tool":"cpp_includes","path":"src/file.cpp"}`, Purpose: "inspect includes"},
		{Tool: "cpp_who_includes", Request: `{"tool":"cpp_who_includes","path":"include/file.h"}`, Purpose: "find include users"},
		{Tool: "cpp_blast_radius", Request: `{"tool":"cpp_blast_radius","pattern":"Name"}`, Purpose: "estimate C/C++ blast radius"},
		{Tool: "cpp_dead_symbol", Request: `{"tool":"cpp_dead_symbol","pattern":"Name","path":"."}`, Purpose: "check for dead C/C++ symbols"},
		{Tool: "cpp_workspace_symbol", Request: `{"tool":"cpp_workspace_symbol","pattern":"Name"}`, Purpose: "workspace-wide C/C++ symbol lookup"},
		{Tool: "cpp_diagnostics", Request: `{"tool":"cpp_diagnostics","path":"src/file.cpp"}`, Purpose: "run C/C++ diagnostics"},
		{Tool: "cpp_file_outline", Request: `{"tool":"cpp_file_outline","path":"src/file.cpp"}`, Purpose: "list C/C++ declarations with line ranges"},
		{Tool: "python_overview", Request: `{"tool":"python_overview"}`, Purpose: "summarize Python workspace"},
		{Tool: "python_entrypoints", Request: `{"tool":"python_entrypoints"}`, Purpose: "find Python entrypoints"},
		{Tool: "python_find_symbol", Request: `{"tool":"python_find_symbol","pattern":"Name"}`, Purpose: "find Python symbols"},
		{Tool: "python_read_symbol", Request: `{"tool":"python_read_symbol","pattern":"Name","path":"src/pkg/file.py"}`, Purpose: "read a Python symbol body. ANCHOR FIRST via python_find_symbol or a prior tool result; speculative pattern guesses fail. For literal text or expressions inside a file, use search_content instead — read_symbol is for symbol bodies, not substring search."},
		{Tool: "python_related_files", Request: `{"tool":"python_related_files","pattern":"term","path":"src"}`, Purpose: "find related Python files"},
		{Tool: "python_references", Request: `{"tool":"python_references","pattern":"Name","path":"."}`, Purpose: "find Python references"},
		{Tool: "python_definition", Request: `{"tool":"python_definition","pattern":"Name","path":"."}`, Purpose: "find Python definitions"},
		{Tool: "python_implementations", Request: `{"tool":"python_implementations","pattern":"BaseClass","path":"."}`, Purpose: "find Python subclasses"},
		{Tool: "python_calls_from", Request: `{"tool":"python_calls_from","pattern":"Name","path":"."}`, Purpose: "find outgoing Python calls"},
		{Tool: "python_calls_into", Request: `{"tool":"python_calls_into","pattern":"Name","path":"."}`, Purpose: "find incoming Python calls"},
		{Tool: "python_workspace_symbol", Request: `{"tool":"python_workspace_symbol","pattern":"Name"}`, Purpose: "workspace-wide Python symbol lookup"},
		{Tool: "python_diagnostics", Request: `{"tool":"python_diagnostics","path":"src/pkg/file.py"}`, Purpose: "run Python diagnostics"},
		{Tool: "python_file_outline", Request: `{"tool":"python_file_outline","path":"src/pkg/file.py"}`, Purpose: "list Python declarations with line ranges"},
		{Tool: "ts_overview", Request: `{"tool":"ts_overview"}`, Purpose: "summarize TypeScript/JavaScript workspace"},
		{Tool: "ts_entrypoints", Request: `{"tool":"ts_entrypoints"}`, Purpose: "find TypeScript/JavaScript entrypoints"},
		{Tool: "ts_find_symbol", Request: `{"tool":"ts_find_symbol","pattern":"Name"}`, Purpose: "find TypeScript/JavaScript symbols"},
		{Tool: "ts_read_symbol", Request: `{"tool":"ts_read_symbol","pattern":"ClassName.method","path":"src/file.ts"}`, Purpose: "read a TypeScript/JavaScript symbol body. ANCHOR FIRST via ts_find_symbol or a prior tool result; speculative pattern guesses fail. For literal text or expressions inside a file, use search_content instead — read_symbol is for symbol bodies, not substring search."},
		{Tool: "ts_related_files", Request: `{"tool":"ts_related_files","pattern":"Name","path":"src"}`, Purpose: "find related TypeScript/JavaScript files"},
		{Tool: "ts_definition", Request: `{"tool":"ts_definition","pattern":"Name","path":"."}`, Purpose: "find TypeScript/JavaScript definitions"},
		{Tool: "ts_implementations", Request: `{"tool":"ts_implementations","pattern":"InterfaceName","path":"."}`, Purpose: "find TypeScript/JavaScript implementations"},
		{Tool: "ts_references", Request: `{"tool":"ts_references","pattern":"Name","path":"."}`, Purpose: "find TypeScript/JavaScript references"},
		{Tool: "ts_calls_from", Request: `{"tool":"ts_calls_from","pattern":"funcName","path":"."}`, Purpose: "find outgoing TypeScript/JavaScript calls"},
		{Tool: "ts_calls_into", Request: `{"tool":"ts_calls_into","pattern":"funcName","path":"."}`, Purpose: "find incoming TypeScript/JavaScript calls"},
		{Tool: "ts_workspace_symbol", Request: `{"tool":"ts_workspace_symbol","pattern":"Name"}`, Purpose: "workspace-wide TypeScript/JavaScript symbol lookup"},
		{Tool: "ts_diagnostics", Request: `{"tool":"ts_diagnostics","path":"src/file.ts"}`, Purpose: "run TypeScript diagnostics"},
		{Tool: "ts_file_outline", Request: `{"tool":"ts_file_outline","path":"src/file.ts"}`, Purpose: "list TypeScript/JavaScript declarations with line ranges"},
		{Tool: "rust_overview", Request: `{"tool":"rust_overview"}`, Purpose: "summarize Rust workspace"},
		{Tool: "rust_entrypoints", Request: `{"tool":"rust_entrypoints"}`, Purpose: "find Rust entrypoints (main, lib, test, bench)"},
		{Tool: "rust_find_symbol", Request: `{"tool":"rust_find_symbol","pattern":"Name"}`, Purpose: "find Rust symbols"},
		{Tool: "rust_workspace_symbol", Request: `{"tool":"rust_workspace_symbol","pattern":"Name"}`, Purpose: "workspace-wide Rust symbol lookup"},
		{Tool: "rust_read_symbol", Request: `{"tool":"rust_read_symbol","pattern":"Name","path":"src/file.rs"}`, Purpose: "read a Rust symbol body. ANCHOR FIRST via rust_find_symbol or a prior tool result; speculative pattern guesses fail. For literal text or expressions inside a file, use search_content instead — read_symbol is for symbol bodies, not substring search."},
		{Tool: "rust_related_files", Request: `{"tool":"rust_related_files","pattern":"Name","path":"src"}`, Purpose: "find related Rust files (follows mod tree)"},
		{Tool: "rust_file_outline", Request: `{"tool":"rust_file_outline","path":"src/file.rs"}`, Purpose: "list Rust declarations with line ranges"},
		{Tool: "rust_definition", Request: `{"tool":"rust_definition","pattern":"Name","path":"."}`, Purpose: "find Rust definitions"},
		{Tool: "rust_references", Request: `{"tool":"rust_references","pattern":"Name","path":"."}`, Purpose: "find Rust references"},
		{Tool: "rust_calls_from", Request: `{"tool":"rust_calls_from","pattern":"Name","path":"."}`, Purpose: "find outgoing Rust calls"},
		{Tool: "rust_calls_into", Request: `{"tool":"rust_calls_into","pattern":"Name","path":"."}`, Purpose: "find incoming Rust calls"},
		{Tool: "rust_implementations", Request: `{"tool":"rust_implementations","pattern":"TraitName","path":"."}`, Purpose: "find Rust trait impls"},
		{Tool: "rust_diagnostics", Request: `{"tool":"rust_diagnostics"}`, Purpose: "run cargo check"},
		{Tool: "java_overview", Request: `{"tool":"java_overview"}`, Purpose: "summarize Java workspace"},
		{Tool: "java_entrypoints", Request: `{"tool":"java_entrypoints"}`, Purpose: "find Java entrypoints and test classes"},
		{Tool: "java_find_symbol", Request: `{"tool":"java_find_symbol","pattern":"Name"}`, Purpose: "find Java symbols"},
		{Tool: "java_read_symbol", Request: `{"tool":"java_read_symbol","pattern":"Name","path":"src/main/java/com/example/File.java"}`, Purpose: "read a Java symbol body. ANCHOR FIRST via java_find_symbol/java_workspace_symbol or a prior tool result; speculative pattern guesses fail. For literal text or expressions inside a file, use search_content instead — read_symbol is for symbol bodies, not substring search."},
		{Tool: "java_related_files", Request: `{"tool":"java_related_files","pattern":"service","path":"src/main/java"}`, Purpose: "find related Java files"},
		{Tool: "java_workspace_symbol", Request: `{"tool":"java_workspace_symbol","pattern":"Name"}`, Purpose: "workspace-wide Java symbol lookup"},
		{Tool: "java_definition", Request: `{"tool":"java_definition","pattern":"Name","path":"."}`, Purpose: "find Java definitions"},
		{Tool: "java_references", Request: `{"tool":"java_references","pattern":"Name","path":"."}`, Purpose: "find Java references"},
		{Tool: "java_implementations", Request: `{"tool":"java_implementations","pattern":"InterfaceName","path":"."}`, Purpose: "find Java implementations"},
		{Tool: "java_calls_from", Request: `{"tool":"java_calls_from","pattern":"methodName","path":"."}`, Purpose: "find outgoing Java calls"},
		{Tool: "java_calls_into", Request: `{"tool":"java_calls_into","pattern":"methodName","path":"."}`, Purpose: "find incoming Java calls"},
		{Tool: "java_diagnostics", Request: `{"tool":"java_diagnostics","path":"src/main/java/com/example/File.java"}`, Purpose: "run Java diagnostics"},
		{Tool: "java_file_outline", Request: `{"tool":"java_file_outline","path":"src/main/java/com/example/File.java"}`, Purpose: "list Java declarations with line ranges"},
		{Tool: "mkdir", Request: `{"tool":"mkdir","path":"relative/dir"}`, Purpose: "create a directory"},
		{Tool: "edit_file", Request: `{"tool":"edit_file","path":"relative/file","old_text":"exact substring from file","new_text":"replacement text","description":"replace the confirmed text to satisfy the requested behavior"}`, Purpose: "bounded edit by exact substring match (preferred for existing source/test files; old_text must appear verbatim; description is required)"},
		{Tool: "edit_file", Request: `{"tool":"edit_file","path":"relative/file","start_line":10,"end_line":12,"content":"replacement\n","description":"replace the confirmed line range to satisfy the requested behavior"}`, Purpose: "bounded edit by line range (use when you know exact lines). Must provide content — NOT old_text/new_text with this shape. Description is required."},
		{Tool: "write_file", Request: `{"tool":"write_file","path":"relative/file","content":"full file contents","description":"write the complete requested file content"}`, Purpose: "whole-file replacement for new files, small files, or non-source deliverables; prefer edit_file/apply_patch for existing source/test files; description is required"},
		{Tool: "append_file", Request: `{"tool":"append_file","path":"relative/file","content":"text to append","description":"append the requested content at the existing file boundary"}`, Purpose: "append text only when end-of-file append is explicitly intended; prefer edit_file/apply_patch for anchored source/test edits; description is required"},
		{Tool: "apply_patch", Request: `{"tool":"apply_patch","content":"*** Begin Patch\n*** Update File: relative/file\n@@\n-old\n+new\n*** End Patch","description":"apply the requested patch across the affected file set"}`, Purpose: "multi-file patch envelope. Use ONLY these *** directives: Begin Patch, End Patch, Add File, Update File, Delete File, Move to. Hunks (`@@`) inside Update File. Do not invent extras like *** Sanity Check / *** Note / *** Verification. Description is required."},
		{Tool: "delegate_edit", Request: `{"tool":"delegate_edit","path":"relative/file","old_text":"exact substring","new_text":"replacement","purpose":"why this edit"}`, Purpose: "hand a single edit spec to the configured executor-role model and return its dispatch result. Use this when you have already decided the exact change and want to keep your own context focused on planning the next edit instead of re-reading what you just wrote. Falls back to direct dispatch when no executor is configured."},
		{Tool: "submit_plan", Request: `{"tool":"submit_plan","rationale":"one paragraph on the overall plan","edits":[{"path":"relative/file","old_text":"exact current substring","new_text":"complete replacement text","purpose":"why this edit"}]}`, Purpose: "submit a structured executable edit plan (plan-then-execute mode only). `edits` is a JSON array, not a quoted string. Edit bodies must be complete final content, not placeholders or prose. Use only for supported planner edits or review-repair corrective edits; clean review-repair closes with Decision: pass. Large bodies are spilled to artifact refs after submission."},
		{Tool: "git_status", Request: `{"tool":"git_status","workdir":"."}`, Purpose: "inspect git state"},
		{Tool: "git_diff", Request: `{"tool":"git_diff","workdir":".","path":"optional/path"}`, Purpose: "inspect git diff"},
		{Tool: "shell", Request: `{"tool":"shell","command":"go test ./...","workdir":"."}`, Purpose: "run an allowed shell command"},
	}
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
	return util.ContainsAny(lower,
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

func RenderToolManifestInstructions(allowedTools []string) string {
	manifest := BuildToolManifest(allowedTools)
	if len(manifest) == 0 {
		return ""
	}
	lines := make([]string, 0, len(manifest))
	for _, entry := range manifest {
		line := "- " + entry.Tool
		if strings.TrimSpace(entry.Request) != "" {
			line += " " + entry.Request
		}
		if entry.Purpose != "" {
			line += " (" + entry.Purpose + ")"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
