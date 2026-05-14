package workspace

// ToolDefinition is the neutral form of an OpenAI-function-calling tool
// spec. The agent layer converts this to llama.Tool before sending.
// ParametersJSON is a raw JSON schema string; we keep it as text so
// hand-authored schemas read naturally and so we don't pull a JSON
// schema library in here.
type ToolDefinition struct {
	Name           string
	Description    string
	ParametersJSON string
}

// BuildToolDefinitions returns the OpenAI-shaped function specs for each
// allowed tool. Pass an empty slice to get the full set. Tool names not
// in this map are silently skipped — keep the name list here in sync
// with ToolManifestTools in tool_manifest.go.
//
// Schemas are hand-tuned per tool. Kept terse: the model's training has
// strong priors on the native tool-call channel, so minimal fields +
// good descriptions beat an exhaustive parameter list.
func BuildToolDefinitions(allowedTools []string) []ToolDefinition {
	allowed := make(map[string]struct{}, len(allowedTools))
	for _, tool := range allowedTools {
		if tool != "" {
			allowed[tool] = struct{}{}
		}
	}
	includeAll := len(allowed) == 0
	defs := baseToolDefinitions()
	out := make([]ToolDefinition, 0, len(defs))
	for _, def := range defs {
		if !includeAll {
			if _, ok := allowed[def.Name]; !ok {
				continue
			}
		}
		out = append(out, def)
	}
	return out
}

// schemaStr is a tiny helper to keep the parameter JSON readable.
func schemaObj(required []string, properties string) string {
	if properties != "" {
		properties += ","
	}
	properties += `"named_gap":` + strProp("optional: after answer-readiness or a child packet, name one exact unresolved claim this follow-up will verify or falsify; make it specific enough that the next probe is obvious") + `,"why_not_answering":` + strProp("optional: short reason describing what would be materially wrong or missing if you answered now")
	req := ""
	if len(required) > 0 {
		req = `,"required":[`
		for i, name := range required {
			if i > 0 {
				req += ","
			}
			req += `"` + name + `"`
		}
		req += `]`
	}
	return `{"type":"object","properties":{` + properties + `}` + req + `}`
}

func strProp(desc string) string {
	return `{"type":"string","description":"` + desc + `"}`
}
func intProp(desc string) string {
	return `{"type":"integer","description":"` + desc + `"}`
}
func boolProp(desc string) string {
	return `{"type":"boolean","description":"` + desc + `"}`
}

func freshnessPolicyProps() string {
	return `"freshness_policy":{"type":"string","enum":["read_through","require_fresh","report_only","allow_stale"],"description":"optional graph freshness mode; read_through is the default for graph evidence tools, report_only returns stale evidence with a stale_evidence state instead of blocking"},"freshness_max_files":` + intProp("optional freshness refresh budget for graph-backed tools; when exceeded, require_fresh/read_through report a stale-evidence blocker")
}

func baseToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "task_ledger",
			Description: "Track complex multi-step work. Keep at most one item in_progress. When an item will touch files, list them in `paths` so the plan panel attaches the completed write to this item instead of adding a duplicate line.",
			ParametersJSON: schemaObj(
				[]string{"todos"},
				`"todos":{"type":"array","items":{"type":"object","properties":{"id":`+strProp("stable id")+`,"content":`+strProp("what needs doing")+`,"status":{"type":"string","enum":["pending","in_progress","completed","blocked","cancelled"]},"paths":{"type":"array","items":{"type":"string"},"description":"workspace-relative files this item will touch"}},"required":["id","content","status"]}}`,
			),
		},
		{
			Name:        "task",
			Description: "Delegate a bounded question to a sub-agent. Read-only sub-agents return inspected paths and concise findings; write-mode sub-agents may also make changes within their scope.",
			ParametersJSON: schemaObj(
				[]string{"agent", "prompt"},
				`"agent":`+strProp("agent name, e.g. explorer")+`,"mode":{"type":"string","enum":["read_only","write"]},"prompt":`+strProp("focused question"),
			),
		},
		{
			Name:           "pwd",
			Description:    "Show the current working directory.",
			ParametersJSON: `{"type":"object","properties":{}}`,
		},
		{
			Name:        "list_files",
			Description: "List files in a directory.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("relative or absolute workspace path")+`,"recursive":`+boolProp("walk subdirectories")+`,"max_depth":`+intProp("recursion depth limit"),
			),
		},
		{
			Name:        "search_files",
			Description: "Find files by name pattern (glob-like).",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("name pattern, e.g. *.go")+`,"path":`+strProp("root to search under"),
			),
		},
		{
			Name:        "search_content",
			Description: "Find text matches in files (prose, config, comments, strings).",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("regex or plain text")+`,"path":`+strProp("root to search under")+`,"glob":`+strProp("file-name glob filter"),
			),
		},
		{
			Name:        "read_file",
			Description: "Read a file or a line range.",
			ParametersJSON: schemaObj(
				nil,
				`"path":`+strProp("relative or absolute path")+`,"id":`+strProp("discovered-file handle like f3")+`,"start_line":`+intProp("1-based start")+`,"end_line":`+intProp("1-based end"),
			),
		},
		{
			Name:        "file_stats",
			Description: "Count files, directories, bytes, and lines for a file or bounded directory scope. Use for size/count questions instead of shell wc/find pipelines.",
			ParametersJSON: schemaObj(
				nil,
				`"path":`+strProp("file or directory to count; defaults to workspace root")+`,"recursive":{"type":"boolean","description":"walk directories recursively"},"glob":`+strProp("optional file glob, e.g. *.go")+`,"max_depth":`+intProp("directory depth limit; default 1, or 8 when recursive=true")+`,"limit":`+intProp("max files to count; default 1000, cap 10000"),
			),
		},
		{
			Name:        "code_navigate",
			Description: "Route a language-agnostic code navigation request to the best language-aware symbol/navigation tool for the given path or language, with a bounded generic source-text fallback.",
			ParametersJSON: schemaObj(
				[]string{"capability"},
				`"capability":{"type":"string","enum":["file_outline","find_symbol","workspace_symbol","read_symbol","definition","references","calls_from","calls_into","implementations","related_files"],"description":"navigation capability the model needs"},"path":`+strProp("source file or narrow source directory used to infer language and scope")+`,"pattern":`+strProp("symbol/name/pattern for symbol-shaped capabilities")+`,"query":`+strProp("alternate symbol/name/pattern when pattern is not used")+`,"language":`+strProp("optional language override such as go, cpp, python, typescript, rust, java")+`,"start_line":`+intProp("optional start line for file_outline/read_symbol-capable routes")+`,"end_line":`+intProp("optional end line for file_outline/read_symbol-capable routes")+`,"limit":`+intProp("max generic fallback matches; default 50"),
			),
		},
		{
			Name:        "read_only_capability",
			Description: "Invoke a stable read-only capability ID from the capability registry. Routes through existing read-only adapters, preserves adapter fallback/uncertainty in the result wrapper, and leaves direct adapter tools available.",
			ParametersJSON: schemaObj(
				[]string{"capability"},
				`"capability":{"type":"string","enum":["file-outline","symbol-lookup","definition","references","related-files"],"description":"read-only capability ID from the registry; source-sanity is intentionally not accepted here"},"path":`+strProp("source file or narrow source directory used by the underlying adapter")+`,"pattern":`+strProp("symbol/name/pattern for symbol-shaped capabilities")+`,"query":`+strProp("alternate symbol/name/pattern when pattern is not used")+`,"language":`+strProp("optional language override such as go, cpp, python, typescript, rust, java")+`,"start_line":`+intProp("optional start line for file-outline routes")+`,"end_line":`+intProp("optional end line for file-outline routes")+`,"limit":`+intProp("max generic fallback matches; default 50"),
			),
		},
		{
			Name:        "source_sanity",
			Description: "Run source-aware sanity for edited source files. Routes to the file language's diagnostics when available; otherwise falls back to a generic source syntax sanity check.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("source file or narrow source directory")+`,"limit":`+intProp("max files to check when path is a directory; default 24"),
			),
		},
		{
			Name:        "harness_audit",
			Description: "Audit idiom itself: exported tool/schema coverage, language capability wiring, backend availability, recent JSONL behavior logs, and passive autonomy contract event coverage when present. Use only for harness/tooling improvement questions, not as a replacement for code search.",
			ParametersJSON: schemaObj(
				nil,
				`"purpose":{"type":"string","enum":["all","tool_surface","behavior","planning_quality"],"description":"audit scope; all includes static tool-surface checks, recent log behavior, and planning-quality feedback"},"path":`+strProp("optional log file or directory to scan for behavior signals; defaults to recent logs/conversations JSONL logs")+`,"glob":`+strProp("optional workspace-relative glob of JSONL logs to scan")+`,"limit":`+intProp("max recent log files to scan; default 80, cap 5000"),
			),
		},
		{
			Name:        "harness_feedback",
			Description: "Record structured feedback about idiom harness/tool/prompt behavior that affected the current task. Use only for generic idiom capability gaps, confusing contracts, stale-context failures, telemetry gaps, or workflow friction; do not use for product-code bugs. ALL of `category`, `component`, `expected`, `observed`, `impact`, `confidence` are REQUIRED; `evidence` should be a non-empty array of repo-relative paths or step ids; `category` and `confidence` must come from the documented enum values exactly. Validation rejects partial submissions silently — fill every required field on the first call.",
			ParametersJSON: schemaObj(
				[]string{"category", "component", "expected", "observed", "impact", "confidence"},
				`"category":{"type":"string","enum":["tool_gap","prompt_gap","workflow_gap","context_gap","stale_context","language_support","routing_gap","verification_gap","telemetry_gap","audit_gap","planning_quality","packet_quality","impact_ranking","plan_audit_gap","budget_gap","replay_eval_gap","missing_affordance","bad_scope_rank","stale_fact","backend_gap","packet_bloat","read_loop","edit_readiness_failure","unsupported_plan_claim","validation_gap","language_precision_gap","other"],"description":"generic feedback class"},"component":`+strProp("idiom surface involved, e.g. context_pack, context_impact, architect/plan-audit, prompt/base, direct tool CLI")+`,"expected":`+strProp("capability or contract the agent reasonably expected")+`,"observed":`+strProp("what actually happened, stated as evidence rather than speculation")+`,"workaround":`+strProp("how the agent proceeded, if any")+`,"impact":`+strProp("task effect such as extra reads, blocked edit, wrong routing, or missing audit signal")+`,"evidence":{"type":"array","items":{"type":"string"},"description":"short log paths, step ids, tool outputs, or file references backing the observation"},"confidence":{"type":"string","enum":["verified","inferred"],"description":"verified when backed by logs/tool output; inferred when observed indirectly"},"severity":{"type":"string","enum":["low","medium","high"],"description":"task impact; default low"},"suggested_fix":`+strProp("optional generic harness/tool/prompt improvement, not a one-off task-specific workaround"),
			),
		},
		{
			Name:        "context_index",
			Description: "Build or refresh idiom's persistent, language-agnostic codebase comprehension graph. This writes the active SQLite graph plus a JSON export; use it when context_status is missing/stale or before generating packet-ready context.",
			ParametersJSON: schemaObj(
				nil,
				`"path":`+strProp("optional file or directory scope; defaults to workspace root")+`,"language":`+strProp("language hint; auto indexes recognized source files, with rich Go/C++/Python/TypeScript/JavaScript/Rust/Java/GDScript collectors, Godot project resource support, and generic cross-language fallback")+`,"mode":{"type":"string","enum":["quick","standard","deep"],"description":"indexing depth; standard records fast language/backend/build-system enrichment, deep also runs bounded live language-tool/LSP probes and normalizes parseable relationship evidence"},"limit":`+intProp("optional max files to index"),
			),
		},
		{
			Name:        "context_status",
			Description: "Report whether the persistent SQLite-backed codebase comprehension graph exists, whether indexed files/symbols or collector versions are stale, and whether storage backends are compatible. Stale/missing path lists are sample-capped by default while count fields preserve the full size. With refresh=true, repair bounded stale scopes, stale file sets, missing indexes, and collector-version mismatches before reporting; unscoped indexed stale-file refreshes honor a default budget unless freshness_max_files or limit overrides it, and report budget_exceeded when exceeded.",
			ParametersJSON: schemaObj(
				nil,
				`"refresh":`+boolProp("optional: attempt stale graph repair before reporting; uses path when provided, otherwise refreshes a stale/missing file set or rebuilds the root for missing indexes/collector-version mismatches")+`,"path":`+strProp("optional bounded file or directory refresh scope; omit for workspace-level status/repair")+`,"language":`+strProp("optional language hint for refresh; auto remains language-agnostic")+`,"mode":{"type":"string","enum":["quick","standard","deep"],"description":"refresh indexing depth; default standard"},"limit":`+intProp("optional max stale/missing path samples to display; also caps unscoped stale-file refresh attempts when refresh=true")+`,`+freshnessPolicyProps(),
			),
		},
		{
			Name:        "context_query",
			Description: "Query idiom's persistent codebase graph for repository, package, file, symbol, summary, import, call, or test relationships without dumping raw source. Results are observational and include confidence/staleness.",
			ParametersJSON: schemaObj(
				nil,
				`"query":`+strProp("natural-language or keyword query")+`,"pattern":`+strProp("optional symbol or handle hint")+`,"path":`+strProp("optional path scope")+`,"resolution":{"type":"string","enum":["repository","package","file","symbol","summary","any"],"description":"requested abstraction level"},"limit":`+intProp("max nodes to return; default 12")+`,"include_evidence":`+boolProp("include edge evidence such as path/line")+`,`+freshnessPolicyProps(),
			),
		},
		{
			Name:        "context_zoom",
			Description: "Return a deeper or broader view for one context graph handle, path, or symbol. Use after context_query returns a promising handle.",
			ParametersJSON: schemaObj(
				nil,
				`"id":`+strProp("context graph handle such as symbol://path.go#Name")+`,"handle":`+strProp("alias for id; accepts file://, symbol://, package://, and other context graph handles")+`,"pattern":`+strProp("optional symbol fallback when id/handle is not known")+`,"path":`+strProp("optional path fallback when id/handle is not known")+`,"resolution":{"type":"string","enum":["repository","package","file","symbol","summary","any"],"description":"requested abstraction level"},"limit":`+intProp("max related nodes/edges to return; default 20")+`,"include_source":`+boolProp("reserved for future exact source excerpts")+`,`+freshnessPolicyProps(),
			),
		},
		{
			Name:        "context_pack",
			Description: "Generate a saved, packet-ready context view for a task from the persistent graph. Returns candidate files, symbols, tests, evidence handles, risks, unknowns, and stale-context gaps without prescribing edits.",
			ParametersJSON: schemaObj(
				[]string{"task"},
				`"task":`+strProp("task statement to localize")+`,"query":`+strProp("optional task/query fallback")+`,"path":`+strProp("optional path scope")+`,"mode":{"type":"string","enum":["plan","critique","brief","implement"],"description":"packet mode; default plan"},"limit":`+intProp("max candidate files; default 8")+`,"budget_tokens":`+intProp("optional intended context budget for downstream packet rendering")+`,"required_tests":{"type":"array","items":{"type":"string"},"description":"exact validation commands that must be carried into the pack"},"required_edit_paths":{"type":"array","items":{"type":"string"},"description":"caller-declared workspace-relative edit paths to surface and prioritize as scope evidence"},"forbidden_paths":{"type":"array","items":{"type":"string"},"description":"workspace-relative paths to omit from candidate files"},`+freshnessPolicyProps(),
			),
		},
		{
			Name:        "context_impact",
			Description: "Rank task-relevant files, symbols, and validation candidates with signal explanations from the persistent graph. Use before planner packet generation when you need affected-scope evidence without raw source flooding.",
			ParametersJSON: schemaObj(
				nil,
				`"task":`+strProp("optional task statement to localize; path-only requests derive a bounded evidence task from path")+`,"query":`+strProp("optional task/query fallback")+`,"path":`+strProp("optional path scope")+`,"mode":{"type":"string","enum":["plan","critique","brief","implement"],"description":"ranking mode; default plan"},"limit":`+intProp("max impact items; default 8")+`,"budget_tokens":`+intProp("optional intended downstream context budget")+`,"required_tests":{"type":"array","items":{"type":"string"},"description":"exact validation commands to include in ranking evidence"},"required_edit_paths":{"type":"array","items":{"type":"string"},"description":"caller-declared workspace-relative edit paths to surface and prioritize as scope evidence"},"forbidden_paths":{"type":"array","items":{"type":"string"},"description":"workspace-relative paths to omit from candidate files"},`+freshnessPolicyProps(),
			),
		},
		{
			Name:        "context_trace_pack",
			Description: "Run a command and save language-agnostic runtime evidence under .idiom/comprehension/traces. Parses generic stack frames, test output, Go test JSON, Go coverprofiles, and LCOV without overwriting static graph facts.",
			ParametersJSON: schemaObj(
				[]string{"command"},
				`"command":`+strProp("runtime command to execute, such as a focused test command")+`,"path":`+strProp("optional workspace-relative scope for matching the trace to a task")+`,"language":`+strProp("optional language hint; auto keeps the output language-agnostic while enabling collector-specific parsers when recognizable")+`,"workdir":`+strProp("optional workspace-relative working directory")+`,"limit":`+intProp("max runtime events/files to return; default 64")+`,"include_files":`+boolProp("include parsed executed-file and stack-frame evidence; default true"),
			),
		},
		{
			Name:        "discovery_query",
			Description: "Run a deterministic layered discovery recipe for a configured purpose and persist the full evidence packet under .idiom/discovery/queries. Uses context_pack orientation when the graph is fresh or a bounded stale scope can be refreshed, then falls back to live probes for stale, missing, or too-coarse claims.",
			ParametersJSON: schemaObj(
				[]string{"purpose"},
				`"purpose":{"type":"string","enum":["impact_analysis","function_tracing","architecture_boundary","security_review","history_context"],"description":"deterministic recipe to run"},"path":`+strProp("optional target file/directory")+`,"pattern":`+strProp("optional symbol, function, type, package, or search pattern")+`,"language":`+strProp("optional language/ecosystem hint, e.g. go, typescript, python, cpp, rust, java")+`,"max_depth":`+intProp("optional relationship depth/budget; default 2")+`,`+freshnessPolicyProps(),
			),
		},
		{
			Name:        "structural_search",
			Description: "Search code by AST shape via ast-grep. Use for syntax-shaped questions that grep and symbol tools cannot answer precisely.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("ast-grep pattern, e.g. if $COND { $BODY }")+`,"language":`+strProp("ast-grep language id, e.g. go, ts, python, cpp, rust, java")+`,"path":`+strProp("file or directory to search"),
			),
		},
		{
			Name:        "dependency_graph",
			Description: "Summarize package/module/import dependencies. Uses native Go/Rust tools where available, Madge/Pydeps if installed, or lightweight local import extraction for TS/JS and Python.",
			ParametersJSON: schemaObj(
				nil,
				`"language":`+strProp("optional ecosystem: go, rust, typescript, python, java")+`,"path":`+strProp("file or directory to analyze"),
			),
		},
		{
			Name:        "history_range",
			Description: "Show git history for a specific line range using git log -L.",
			ParametersJSON: schemaObj(
				[]string{"path", "start_line", "end_line"},
				`"path":`+strProp("file path")+`,"start_line":`+intProp("1-based start")+`,"end_line":`+intProp("1-based end"),
			),
		},
		{
			Name:        "history_symbol",
			Description: "Show git history for a function/symbol using git log -L :symbol:path.",
			ParametersJSON: schemaObj(
				[]string{"path", "pattern"},
				`"path":`+strProp("file path")+`,"pattern":`+strProp("function or symbol regex understood by git log -L"),
			),
		},
		{
			Name:        "blame_range",
			Description: "Show git blame for a file or line range.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("file path")+`,"start_line":`+intProp("optional 1-based start")+`,"end_line":`+intProp("optional 1-based end"),
			),
		},
		{
			Name:        "ctags_symbols",
			Description: "Fallback symbol discovery via Universal Ctags JSON output. Use when the primary language-aware route does not cover a file type.",
			ParametersJSON: schemaObj(
				nil,
				`"path":`+strProp("file or directory to index")+`,"pattern":`+strProp("optional symbol name filter"),
			),
		},
		{
			Name:        "semgrep_scan",
			Description: "Run Semgrep for security/dataflow-shaped discovery. Requires semgrep on PATH; pattern is the Semgrep config, defaulting to auto.",
			ParametersJSON: schemaObj(
				nil,
				`"path":`+strProp("file or directory to scan")+`,"pattern":`+strProp("Semgrep config, e.g. auto or p/default"),
			),
		},
		{
			Name:        "joern_scan",
			Description: "Run a heavyweight Joern code-property-graph vulnerability scan. Requires joern-scan on PATH.",
			ParametersJSON: schemaObj(
				nil,
				`"path":`+strProp("file or directory to scan"),
			),
		},
		{
			Name:        "git_status",
			Description: "Inspect the git working tree status.",
			ParametersJSON: schemaObj(
				nil,
				`"workdir":`+strProp("working directory, defaults to workspace root"),
			),
		},
		{
			Name:        "git_diff",
			Description: "Inspect the git diff for the working tree or a path.",
			ParametersJSON: schemaObj(
				nil,
				`"workdir":`+strProp("working directory")+`,"path":`+strProp("optional path to scope the diff"),
			),
		},
		{
			Name:        "shell",
			Description: "Run a shell command in the workspace. Same OS permissions as this process.",
			ParametersJSON: schemaObj(
				[]string{"command"},
				`"command":`+strProp("the command line")+`,"workdir":`+strProp("working directory"),
			),
		},
		{
			Name:        "mkdir",
			Description: "Create a directory.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("directory to create"),
			),
		},
		{
			Name:        "edit_file",
			Description: "Replace a line range or exact old_text in an existing file. Preferred for localized edits. Use either old_text+new_text or start_line+end_line+content, plus a one-sentence description of the change's intent.",
			ParametersJSON: schemaObj(
				[]string{"path", "description"},
				`"path":`+strProp("file path")+`,"start_line":`+intProp("1-based start for the line-range shape; use with end_line and content")+`,"end_line":`+intProp("1-based end for the line-range shape; use with start_line and content")+`,"old_text":`+strProp("exact text to replace for the exact-match shape; use with new_text and omit line range fields")+`,"new_text":`+strProp("replacement text for the exact-match shape; use with old_text")+`,"content":`+strProp("replacement content for the line-range shape; use with start_line and end_line")+`,"description":`+strProp("one sentence on what this change is and why")+`,"task_id":`+strProp("optional: id of the planned task_ledger item this write fulfills, so the plan panel can check it off in place"),
			),
		},
		{
			Name:        "write_file",
			Description: "Overwrite a file with exact contents. Requires a one-sentence description of the change's intent.",
			ParametersJSON: schemaObj(
				[]string{"path", "content", "description"},
				`"path":`+strProp("file path")+`,"content":`+strProp("full file contents")+`,"description":`+strProp("one sentence on what this file is for and why it's being written")+`,"task_id":`+strProp("optional: id of the planned task_ledger item this write fulfills, so the plan panel can check it off in place"),
			),
		},
		{
			Name:        "append_file",
			Description: "Append text to an existing file. Requires a one-sentence description of the change's intent.",
			ParametersJSON: schemaObj(
				[]string{"path", "content", "description"},
				`"path":`+strProp("file path")+`,"content":`+strProp("text to append")+`,"description":`+strProp("one sentence on what's being appended and why")+`,"task_id":`+strProp("optional: id of the planned task_ledger item this write fulfills, so the plan panel can check it off in place"),
			),
		},
		{
			Name:        "apply_patch",
			Description: "Apply a multi-file patch envelope. The envelope grammar is exactly: outer `*** Begin Patch` / `*** End Patch` markers; per-file sections introduced by `*** Add File: <path>`, `*** Update File: <path>`, or `*** Delete File: <path>`; `*** Update File:` may carry an optional `*** Move to: <new-path>`; hunks inside an Update File start with `@@` and contain lines prefixed by ` `, `+`, or `-`. Do NOT invent additional `*** Foo` directives such as `*** Sanity Check`, `*** Note`, `*** Verification`, or commentary headers — the parser tolerates and skips them but they signal a brief misread. Requires a one-sentence description of the patch's intent.",
			ParametersJSON: schemaObj(
				[]string{"content", "description"},
				`"content":`+strProp("patch envelope using only *** Begin/End Patch, *** Add/Update/Delete File, *** Move to, and @@ hunks")+`,"description":`+strProp("one sentence covering the patch's overall intent")+`,"task_id":`+strProp("optional: id of the planned task_ledger item this patch fulfills, so the plan panel can check it off in place"),
			),
		},
		{
			Name:        "submit_plan",
			Description: "Submit a durable structured edit plan in plan-then-execute mode. The planner enumerates every edit up front; the executor phase dispatches them in order against the workspace. Legacy callers may send rationale, pack_id, and edits at the top level; structured callers may send plan_artifact with task, contract, packet, paths, evidence, unresolved_questions, verification_obligations, approval, handoff, and edits. Approval carries stable scope_id, approved_paths, pending_paths, and execution_permission; partial approval may execute only approved scope. Handoff carries a stable id used by executor/audit events. Use this in lieu of per-edit tool calls when the lane is plan-only. Three edit shapes: (1) substring — path+old_text+new_text replaces the first match; empty new_text deletes old_text; set replace_all:true to replace every occurrence; (2) line-range — path+start_line+end_line+non-empty content replaces lines [start_line, end_line]; use substring shape for deletions; (3) whole-file or new-file — path+new_text+replace_all:true with EMPTY old_text writes new_text as the entire file body, creating the file if it doesn't exist. Large new_text/content bodies are automatically spilled to artifact refs after submission. When you used context_pack to localize the work, pass its pack_id back so the executor can rehydrate the same cached view instead of re-discovering.",
			ParametersJSON: schemaObj(
				nil,
				`"rationale":`+strProp("legacy path: bounded paragraph explaining the overall plan and why these edits in this order")+`,"pack_id":`+strProp("legacy path: optional context_pack pack_id (16-hex) used to localize this plan; lets the executor rehydrate the same cached view")+`,"plan_artifact":{"type":"object","description":"optional durable idiom.plan.v2 artifact with task, contract, packet, paths, evidence, unresolved_questions, verification_obligations, approval, handoff, and edits fields"},"edits":{"type":"array","description":"ordered list of edits the executor will dispatch; required unless plan_artifact.edits is provided","items":{"type":"object","properties":{"path":`+strProp("file path")+`,"start_line":`+intProp("1-based start for the line-range shape")+`,"end_line":`+intProp("1-based end for the line-range shape")+`,"old_text":`+strProp("exact text to replace for the substring shape; leave empty only when using replace_all+new_text to write a whole file or create a new file")+`,"new_text":`+strProp("replacement text for the substring shape; may be empty when deleting old_text; OR the whole file body when paired with replace_all:true and empty old_text")+`,"new_text_ref":`+strProp("artifact ref for replacement/whole-file body; normally produced automatically after submit_plan spills a large new_text")+`,"content":`+strProp("non-empty replacement content for the line-range shape")+`,"content_ref":`+strProp("artifact ref for line-range content; normally produced automatically after submit_plan spills large content")+`,"replace_all":`+boolProp("with old_text set: replace every occurrence; with old_text empty: write new_text as the entire file body (creates the file if missing)")+`,"purpose":`+strProp("one sentence: what this edit does and why")+`},"required":["path"]}}`,
			),
		},
		{
			Name:        "delegate_edit",
			Description: "Hand a single file edit to the configured executor model (the executor-role backend, when one is declared in .idiom/backends.json or as the cfg.Implementer block). The executor receives the spec, emits the workspace tool call, and the runner dispatches it. Use this when you have already decided the exact change and want to keep your own context focused on planning the next path instead of re-reading the file you just wrote. Falls back to direct dispatch if no executor is configured or it returns an unparseable reply, so the lane never stalls. Requires a path plus EITHER old_text+new_text OR start_line+end_line+content.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("file path")+`,"start_line":`+intProp("1-based start for the line-range shape; use with end_line and content")+`,"end_line":`+intProp("1-based end for the line-range shape; use with start_line and content")+`,"old_text":`+strProp("exact text to replace for the exact-match shape; use with new_text")+`,"new_text":`+strProp("replacement text for the exact-match shape; use with old_text")+`,"content":`+strProp("replacement content for the line-range shape; use with start_line and end_line")+`,"replace_all":`+boolProp("replace every occurrence of old_text instead of just the first")+`,"purpose":`+strProp("one sentence: what the executor should do and why"),
			),
		},
		{
			Name:           "go_overview",
			Description:    "Summarize the Go workspace structure.",
			ParametersJSON: `{"type":"object","properties":{}}`,
		},
		{
			Name:           "go_entrypoints",
			Description:    "List Go program entrypoints.",
			ParametersJSON: `{"type":"object","properties":{}}`,
		},
		{
			Name:        "go_find_symbol",
			Description: "Find Go symbols matching a pattern.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol name or regex"),
			),
		},
		{
			Name:        "go_find_function",
			Description: "Find Go functions matching a pattern.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("function name or regex"),
			),
		},
		{
			Name:        "go_find_type",
			Description: "Find Go types matching a pattern.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("type name or regex"),
			),
		},
		{
			Name:        "go_find_const",
			Description: "Find Go constants matching a pattern.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("constant name or regex"),
			),
		},
		{
			Name:        "go_find_package",
			Description: "Find Go packages matching a pattern.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("package import path or regex"),
			),
		},
		{
			Name:        "go_related_files",
			Description: "Find Go files related to a pattern, scoped to a path.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("term")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "go_workspace_symbol",
			Description: "Workspace-wide Go symbol lookup.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol name or regex"),
			),
		},
		{
			Name:        "go_references",
			Description: "Find references to a Go symbol.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "go_definition",
			Description: "Find a Go symbol definition.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "go_implementations",
			Description: "Find implementations of a Go interface.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("interface name")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "go_calls_from",
			Description: "Find outgoing calls from a Go function.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("function name")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "go_calls_into",
			Description: "Find incoming calls to a Go function.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("function name")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "go_read_symbol",
			Description: "Read the body of a Go symbol. ANCHOR FIRST: confirm the symbol name and file via go_find_symbol, go_workspace_symbol, or a prior tool result before calling this — speculative pattern guesses fail more than half the time and waste a tool step.",
			ParametersJSON: schemaObj(
				[]string{"pattern", "path"},
				`"pattern":`+strProp("symbol name")+`,"path":`+strProp("file path"),
			),
		},
		{
			Name:        "go_extract_body",
			Description: "Extract the body of a Go function or method.",
			ParametersJSON: schemaObj(
				[]string{"pattern", "path"},
				`"pattern":`+strProp("function name")+`,"path":`+strProp("file path"),
			),
		},
		{
			Name:        "go_importers",
			Description: "Find packages that import a given Go package.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("package path"),
			),
		},
		{
			Name:        "go_diagnostics",
			Description: "Run Go diagnostics (build/vet/errors) on a file.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("file path"),
			),
		},
		{
			Name:           "cpp_overview",
			Description:    "Summarize the C/C++ workspace structure.",
			ParametersJSON: `{"type":"object","properties":{}}`,
		},
		{
			Name:        "cpp_entrypoints",
			Description: "List likely C/C++ anchor files and entrypoints.",
			ParametersJSON: schemaObj(
				nil,
				`"max_depth":`+intProp("optional result limit"),
			),
		},
		{
			Name:        "cpp_find_symbol",
			Description: "Find C/C++ symbols matching a pattern.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol name or regex")+`,"path":`+strProp("optional file or directory scope")+`,"max_depth":`+intProp("optional result limit"),
			),
		},
		{
			Name:        "cpp_read_symbol",
			Description: "Read a C/C++ symbol snippet. ANCHOR FIRST: confirm the symbol name and file via cpp_find_symbol, cpp_workspace_symbol, or a prior tool result before calling this — speculative pattern guesses fail more than half the time (observed ~56% failure rate) and waste a tool step.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol name or regex")+`,"path":`+strProp("optional file or directory scope")+`,"start_line":`+intProp("optional 1-based start")+`,"end_line":`+intProp("optional 1-based end"),
			),
		},
		{
			Name:        "cpp_related_files",
			Description: "Find C/C++ files related to a pattern, scoped to a path.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("term")+`,"path":`+strProp("optional file or directory scope")+`,"max_depth":`+intProp("optional result limit"),
			),
		},
		{
			Name:        "cpp_workspace_symbol",
			Description: "Workspace-wide C/C++ symbol lookup.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol name or regex")+`,"path":`+strProp("optional file or directory scope")+`,"max_depth":`+intProp("optional result limit"),
			),
		},
		{
			Name:        "cpp_definition",
			Description: "Find a C/C++ symbol definition.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol")+`,"path":`+strProp("optional file or directory scope"),
			),
		},
		{
			Name:        "cpp_declaration",
			Description: "Find a C/C++ symbol declaration.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol")+`,"path":`+strProp("optional file or directory scope"),
			),
		},
		{
			Name:        "cpp_implementations",
			Description: "Find C/C++ implementations or overrides for a symbol.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol")+`,"path":`+strProp("optional file or directory scope"),
			),
		},
		{
			Name:        "cpp_references",
			Description: "Find references to a C/C++ symbol.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol")+`,"path":`+strProp("optional file or directory scope"),
			),
		},
		{
			Name:        "cpp_calls_from",
			Description: "Find outgoing calls from a C/C++ function or method.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("function or method name")+`,"path":`+strProp("optional file or directory scope"),
			),
		},
		{
			Name:        "cpp_calls_into",
			Description: "Find incoming calls to a C/C++ function or method.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("function or method name")+`,"path":`+strProp("optional file or directory scope"),
			),
		},
		{
			Name:        "cpp_extract_body",
			Description: "Extract the body of a C/C++ function or method.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol name")+`,"path":`+strProp("optional file or directory scope")+`,"start_line":`+intProp("optional 1-based start")+`,"end_line":`+intProp("optional 1-based end"),
			),
		},
		{
			Name:        "cpp_includes",
			Description: "Find direct and transitive C/C++ include dependencies for a file or include pattern.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("file path, header name, or include pattern")+`,"path":`+strProp("optional file or directory scope"),
			),
		},
		{
			Name:        "cpp_who_includes",
			Description: "Find C/C++ files that directly or transitively include a file or include pattern.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("file path, header name, or include pattern")+`,"path":`+strProp("optional file or directory scope"),
			),
		},
		{
			Name:        "cpp_blast_radius",
			Description: "Estimate C/C++ translation units affected by a file or include pattern.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("file path, header name, symbol, or include pattern")+`,"path":`+strProp("optional file or directory scope"),
			),
		},
		{
			Name:        "cpp_dead_symbol",
			Description: "Check whether a C/C++ symbol appears unused.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol name")+`,"path":`+strProp("optional file or directory scope"),
			),
		},
		{
			Name:        "cpp_diagnostics",
			Description: "Run C/C++ diagnostics with clangd when compile_commands.json is available.",
			ParametersJSON: schemaObj(
				nil,
				`"path":`+strProp("optional file or directory scope"),
			),
		},
		{
			Name:        "cpp_file_outline",
			Description: "List declarations with line ranges in a C/C++ file.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("file path"),
			),
		},
		{
			Name:        "go_file_outline",
			Description: "Inspect declaration boundaries for Go refactors.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("file path"),
			),
		},
		{
			Name:        "go_move_declarations",
			Description: "Mechanically move Go declarations between files by tag or explicit list.",
			ParametersJSON: schemaObj(
				[]string{"path", "destination"},
				`"path":`+strProp("source file")+`,"destination":`+strProp("destination file")+`,"pattern":`+strProp("tag:responsibility or explicit list"),
			),
		},
		{
			Name:           "python_overview",
			Description:    "Summarize the Python workspace structure.",
			ParametersJSON: `{"type":"object","properties":{}}`,
		},
		{
			Name:           "python_entrypoints",
			Description:    "List likely Python entrypoint files (main blocks, app.py, cli.py).",
			ParametersJSON: `{"type":"object","properties":{}}`,
		},
		{
			Name:        "python_find_symbol",
			Description: "Find Python symbols (functions, classes, methods) matching a pattern.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol name or regex"),
			),
		},
		{
			Name:        "python_workspace_symbol",
			Description: "Workspace-wide Python symbol lookup (uses pyright/pylsp when available).",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol name or regex"),
			),
		},
		{
			Name:        "python_definition",
			Description: "Find a Python symbol definition.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "python_references",
			Description: "Find references to a Python symbol.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "python_implementations",
			Description: "Find subclass implementations of a Python base class.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("base class name")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "python_calls_from",
			Description: "Find outgoing calls from a Python function or method.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("function or method name")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "python_calls_into",
			Description: "Find incoming calls to a Python function or method.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("function or method name")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "python_read_symbol",
			Description: "Read the body of a Python symbol. ANCHOR FIRST: confirm the symbol name and file via python_find_symbol or a prior tool result before calling this — speculative pattern guesses fail and waste a tool step.",
			ParametersJSON: schemaObj(
				[]string{"pattern", "path"},
				`"pattern":`+strProp("symbol name")+`,"path":`+strProp("file path"),
			),
		},
		{
			Name:        "python_related_files",
			Description: "Find Python files related to a pattern, scoped to a path.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("term")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "python_diagnostics",
			Description: "Run Python diagnostics when a Python LSP server is available.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("file path"),
			),
		},
		{
			Name:        "python_file_outline",
			Description: "List declarations with line ranges in a Python file.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("file path"),
			),
		},
		{
			Name:           "ts_overview",
			Description:    "Summarize the TypeScript/JavaScript workspace (likely anchors, counts).",
			ParametersJSON: `{"type":"object","properties":{}}`,
		},
		{
			Name:           "ts_entrypoints",
			Description:    "List likely TypeScript/JavaScript entrypoint files.",
			ParametersJSON: `{"type":"object","properties":{}}`,
		},
		{
			Name:        "ts_find_symbol",
			Description: "Find TypeScript/JavaScript symbols by name.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol name")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "ts_read_symbol",
			Description: "Read the body of a TypeScript/JavaScript symbol. ANCHOR FIRST: confirm the symbol name and file via ts_find_symbol or a prior tool result before calling this — speculative pattern guesses fail and waste a tool step.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol, e.g. ClassName.method")+`,"path":`+strProp("file path"),
			),
		},
		{
			Name:        "ts_related_files",
			Description: "Find TypeScript/JavaScript files related to a concept or symbol.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("keyword or symbol")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "ts_definition",
			Description: "Find a TypeScript/JavaScript symbol definition.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "ts_implementations",
			Description: "Find implementations of a TypeScript interface or overriding methods.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("interface or method")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "ts_references",
			Description: "Find TypeScript/JavaScript references to a symbol.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "ts_calls_from",
			Description: "Find outgoing calls from a TypeScript/JavaScript function or method.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("function or method")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "ts_calls_into",
			Description: "Find incoming calls to a TypeScript/JavaScript function or method.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("function or method")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "ts_workspace_symbol",
			Description: "Workspace-wide TypeScript/JavaScript symbol lookup via typescript-language-server when available.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol pattern")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "ts_diagnostics",
			Description: "Run TypeScript diagnostics (type errors, lints) on a file or directory.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("file or directory path"),
			),
		},
		{
			Name:        "ts_file_outline",
			Description: "List declarations with line ranges in a TypeScript/JavaScript file.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("file path"),
			),
		},
		{
			Name:           "rust_overview",
			Description:    "Summarize the Rust workspace structure.",
			ParametersJSON: `{"type":"object","properties":{}}`,
		},
		{
			Name:           "rust_entrypoints",
			Description:    "List Rust entrypoints (main, lib root, #[test], #[bench]).",
			ParametersJSON: `{"type":"object","properties":{}}`,
		},
		{
			Name:        "rust_find_symbol",
			Description: "Find Rust symbols (fn, struct, enum, trait, impl, macro) matching a pattern.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol name or regex")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "rust_workspace_symbol",
			Description: "Workspace-wide Rust symbol lookup (uses rust-analyzer when available).",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol name or regex"),
			),
		},
		{
			Name:        "rust_read_symbol",
			Description: "Read the body of a Rust symbol. ANCHOR FIRST: confirm the symbol name and file via rust_find_symbol or a prior tool result before calling this — speculative pattern guesses fail and waste a tool step.",
			ParametersJSON: schemaObj(
				[]string{"pattern", "path"},
				`"pattern":`+strProp("symbol name")+`,"path":`+strProp("file path"),
			),
		},
		{
			Name:        "rust_related_files",
			Description: "Find Rust files related to a pattern. Follows the mod-tree from the current file.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("term")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "rust_file_outline",
			Description: "List declarations with line ranges in a Rust file.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("file path"),
			),
		},
		{
			Name:        "rust_definition",
			Description: "Find a Rust symbol definition.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "rust_references",
			Description: "Find references to a Rust symbol.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "rust_calls_from",
			Description: "Find outgoing calls from a Rust function.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("function name")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "rust_calls_into",
			Description: "Find incoming calls to a Rust function.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("function name")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "rust_implementations",
			Description: "Find implementations of a Rust trait.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("trait name")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "rust_diagnostics",
			Description: "Run Rust diagnostics via cargo check.",
			ParametersJSON: schemaObj(
				nil,
				`"path":`+strProp("optional path to scope the check"),
			),
		},
		{
			Name:           "java_overview",
			Description:    "Summarize the Java workspace structure.",
			ParametersJSON: `{"type":"object","properties":{}}`,
		},
		{
			Name:           "java_entrypoints",
			Description:    "List Java program entrypoints and test classes.",
			ParametersJSON: `{"type":"object","properties":{}}`,
		},
		{
			Name:        "java_find_symbol",
			Description: "Find Java symbols matching a pattern.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol name or regex"),
			),
		},
		{
			Name:        "java_read_symbol",
			Description: "Read the body of a Java symbol. ANCHOR FIRST: confirm the symbol name and file via java_find_symbol or java_workspace_symbol or a prior tool result before calling this — speculative pattern guesses fail and waste a tool step.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol name")+`,"path":`+strProp("file path"),
			),
		},
		{
			Name:        "java_related_files",
			Description: "Find Java files related to a pattern, scoped to a path.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("term")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "java_workspace_symbol",
			Description: "Workspace-wide Java symbol lookup.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol name or regex"),
			),
		},
		{
			Name:        "java_definition",
			Description: "Find a Java symbol definition.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "java_references",
			Description: "Find references to a Java symbol.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("symbol")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "java_implementations",
			Description: "Find implementations of a Java interface or class.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("interface or class name")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "java_calls_from",
			Description: "Find outgoing calls from a Java method.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("method name")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "java_calls_into",
			Description: "Find incoming calls to a Java method.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("method name")+`,"path":`+strProp("scope"),
			),
		},
		{
			Name:        "java_diagnostics",
			Description: "Run Java diagnostics (mvn/gradle/javac) on a file or directory.",
			ParametersJSON: schemaObj(
				nil,
				`"path":`+strProp("file or directory path"),
			),
		},
		{
			Name:        "java_file_outline",
			Description: "List declarations with line ranges in a Java file.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("file path"),
			),
		},
	}
}
