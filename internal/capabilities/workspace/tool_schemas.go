package workspace

import (
	"encoding/json"
	"strings"

	"idiom/internal/repository/semantic/languagetools"
)

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
		tool = strings.TrimSpace(tool)
		if tool != "" {
			allowed[tool] = struct{}{}
		}
	}

	includeAll := len(allowed) == 0
	defs := mergeLanguageToolDefinitions(baseToolDefinitions())
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

func stringEnumJSON(values []string) string {
	encoded, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}

	return string(encoded)
}

func readOnlyCapabilityIDsForSchema() []string {
	registry := BuildReadOnlyCapabilityRegistry()
	ids := make([]string, 0, len(registry.Capabilities))

	for _, capability := range registry.Capabilities {
		ids = append(ids, capability.ID)
	}

	return ids
}

func mergeLanguageToolDefinitions(defs []ToolDefinition) []ToolDefinition {
	catalog := languageToolDefinitions()
	if len(catalog) == 0 {
		return defs
	}

	out := make([]ToolDefinition, 0, len(defs)+len(catalog))
	inserted := false

	for _, def := range defs {
		if IsLanguageToolName(def.Name) {
			if !inserted {
				out = append(out, catalog...)
				inserted = true
			}

			continue
		}

		out = append(out, def)
	}

	if !inserted {
		out = append(out, catalog...)
	}

	return out
}

func submitPlanEditSchema() string {
	properties := `"path":` + strProp("file path") +
		`,"start_line":` + intProp("1-based start for the line-range shape") +
		`,"end_line":` + intProp("1-based end for the line-range shape") +
		`,"old_text":` + strProp("exact current text to replace for the substring shape; copy from source evidence, no placeholders/ellipses") +
		`,"new_text":` + strProp("complete replacement text for the substring shape; may be empty when deleting old_text; OR the complete missing/new-file body when paired with replace_all:true and empty old_text; no placeholders/ellipses") +
		`,"new_text_ref":` + strProp("artifact ref for replacement or missing/new-file body; normally produced automatically after submit_plan spills a large new_text") +
		`,"content":` + strProp("complete non-empty replacement content for line-range shape, or inserted content for insert_after_line shape; no placeholders/ellipses") +
		`,"content_ref":` + strProp("artifact ref for line-range content; normally produced automatically after submit_plan spills large content") +
		`,"insert_after_line":` + intProp("existing 1-based line after which to insert small inline non-empty content; insertion-only shape, not a replacement range, not valid for new files, and not valid with content_ref") +
		`,"replace_all":` + boolProp("with old_text set: replace every occurrence; with old_text empty: write new_text as the entire body for missing/new files only; existing source/test files require bounded anchors") +
		`,"purpose":` + strProp("one sentence: what this edit does and why")

	return `{"type":"object","properties":{` + properties + `},"required":["path"],"anyOf":[{"required":["old_text","new_text"]},{"required":["start_line","end_line","content"]},{"required":["start_line","end_line","content_ref"]},{"required":["insert_after_line","content"]},{"required":["replace_all","new_text"]},{"required":["replace_all","new_text_ref"]}]}`
}

func submitPlanParametersSchema() string {
	editSchema := submitPlanEditSchema()
	properties := `"rationale":` + strProp("legacy path: bounded paragraph explaining the overall plan and why these edits in this order") +
		`,"pack_id":` + strProp("legacy path: optional context_pack pack_id (16-hex) used to localize this plan; lets the executor rehydrate the same cached view") +
		`,"plan_artifact":{"type":"object","description":"advanced structured callers only: optional durable idiom.plan.v3 artifact. It must be a complete valid artifact, not only an edits wrapper. Planner/tool-call lanes should prefer top-level edits unless they can fill task, contract, verification_obligations, and edits.","properties":{"task":{"type":"object","description":"task id or bounded summary"},"contract":{"type":"object","description":"plan contract, including kind plan_then_execute"},"verification_obligations":{"type":"array","minItems":1,"description":"planner-owned verification obligations"},"edits":{"type":"array","minItems":1,"items":` + editSchema + `}},"required":["task","contract","verification_obligations","edits"]}` +
		`,"source_satisfied_paths":{"type":"array","items":{"type":"string"},"description":"mixed-plan only: required edit paths whose requested final state is already present while other required paths still need real edits. These satisfy required-path coverage but are not executable edits. Use only with rationale/current-state evidence, do not include a no-op edit for the same path, and do not use this field as an all-source-satisfied closeout; if every required path is already final, answer Decision: source-satisfied instead of calling submit_plan"}` +
		`,"edits":{"type":"array","minItems":1,"description":"ordered JSON array of edits the executor will dispatch; required unless plan_artifact.edits is provided; do not emit this array as a quoted string","items":` + editSchema + `}` +
		`,"named_gap":` + strProp("optional: after answer-readiness or a child packet, name one exact unresolved claim this follow-up will verify or falsify; make it specific enough that the next probe is obvious") +
		`,"why_not_answering":` + strProp("optional: short reason describing what would be materially wrong or missing if you answered now")

	return `{"type":"object","properties":{` + properties + `},"anyOf":[{"required":["edits"]},{"required":["plan_artifact"]}]}`
}

func freshnessPolicyProps() string {
	return `"freshness_policy":{"type":"string","enum":["read_through","require_fresh","report_only","allow_stale"],"description":"optional graph freshness mode; read_through is the default for graph evidence tools, report_only returns stale evidence with a stale_evidence state instead of blocking"},"freshness_max_files":` + intProp("optional freshness refresh budget for graph-backed tools; when exceeded, require_fresh/read_through report a stale-evidence blocker")
}

// contextToolInputSchema returns the shared schema property prefix used by all
// context_* comprehension tools. Tools that accept freshness policy callers
// should append freshnessPolicyProps() after their unique fields.
func contextToolInputSchema() string {
	return `"path":` + strProp("optional workspace-relative file or directory scope; omitted means workspace root") +
		`,"limit":` + intProp("optional max items to return; varies by tool")
}

func baseToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        ToolNameTaskLedger,
			Description: "Track complex multi-step work. Keep at most one item in_progress. When an item will touch files, list them in `paths` so the plan panel attaches the completed write to this item instead of adding a duplicate line. When marking mutation work completed, cite host-backed evidence refs such as `write:path`, `read:path`, `verify:command`, or `artifact:id`.",
			ParametersJSON: schemaObj(
				[]string{"todos"},
				`"todos":{"type":"array","items":{"type":"object","properties":{"id":`+strProp("stable id")+`,"content":`+strProp("what needs doing")+`,"status":{"type":"string","enum":["pending","in_progress","completed","blocked","cancelled"]},"evidence":`+strProp("for completed mutation items, refs backed by prior tool results, e.g. write:README.md; verify:go test ./...")+`,"paths":{"type":"array","items":{"type":"string"},"description":"workspace-relative files this item will touch"}},"required":["id","content","status"]}}`,
			),
		},
		{
			Name:        ToolNameTask,
			Description: "Delegate a bounded question to a sub-agent. Read-only sub-agents return inspected paths and concise findings; write-mode sub-agents may also make changes within their scope.",
			ParametersJSON: schemaObj(
				[]string{"agent", "prompt"},
				`"agent":`+strProp("agent name, e.g. explorer")+`,"mode":{"type":"string","enum":["read_only","write"]},"prompt":`+strProp("focused question"),
			),
		},
		{
			Name:        ToolNameProductEfforts,
			Description: "Read product workflow effort summaries by status bucket without running inference or mutating workflow state.",
			ParametersJSON: schemaObj(
				nil,
				`"root":`+strProp("product efforts root; defaults to docs/product-workflows/efforts")+`,"status":{"type":"string","enum":["unstarted","started","closed","all"],"description":"optional status bucket filter; defaults to all"},"query":`+strProp("optional substring filter over effort summaries"),
			),
		},
		{
			Name:        ToolNameProductStatus,
			Description: "Read one product workflow effort's status or operator continuation view without running inference or mutating workflow state.",
			ParametersJSON: schemaObj(
				nil,
				`"effort_root":`+strProp("effort folder path containing workflow_state.json")+`,"path":`+strProp("alias for effort_root")+`,"operator_view":`+boolProp("when true, return the shared operator continuation status view"),
			),
		},
		{
			Name:           ToolNamePwd,
			Description:    "Show the current working directory.",
			ParametersJSON: `{"type":"object","properties":{}}`,
		},
		{
			Name:        ToolNameListFiles,
			Description: "List files in a directory.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("workspace path to list")+`,"recursive":`+boolProp("walk subdirectories")+`,"max_depth":`+intProp("recursion depth limit"),
			),
		},
		{
			Name:        ToolNameSearchFiles,
			Description: "Find files by name or path pattern.",
			ParametersJSON: schemaObj(
				nil,
				`"pattern":`+strProp("filename/path substring or simple glob, e.g. app or *.go; one of pattern or glob is required")+`,"path":`+strProp("root to search under")+`,"glob":`+strProp("optional glob pattern; accepted as the file search pattern when pattern is omitted"),
			),
		},
		{
			Name:        ToolNameSearchContent,
			Description: "Find text matches in files (prose, config, comments, strings).",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("regex by default, or exact literal text when fixed_strings is true")+`,"path":`+strProp("root to search under")+`,"glob":`+strProp("file-name glob filter")+`,"fixed_strings":`+boolProp("treat pattern as exact literal text instead of a regex; useful for punctuation-heavy source snippets and typed IDs"),
			),
		},
		{
			Name:        ToolNameRg,
			Description: "Ripgrep-compatible text search fallback over workspace files.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("regex by default, or exact literal text when fixed_strings is true")+`,"path":`+strProp("root to search under")+`,"glob":`+strProp("file-name glob filter")+`,"fixed_strings":`+boolProp("treat pattern as exact literal text instead of a regex; useful for punctuation-heavy source snippets and typed IDs"),
			),
		},
		{
			Name:        ToolNameReadFile,
			Description: "Read a file or a line range.",
			ParametersJSON: schemaObj(
				nil,
				`"path":`+strProp("workspace path to read")+`,"id":`+strProp("discovered-file handle like f3")+`,"start_line":`+intProp("1-based start")+`,"end_line":`+intProp("1-based end"),
			),
		},
		{
			Name:        ToolNameFileStats,
			Description: "Count files, directories, bytes, lines, and optional line-range byte/character lengths for a file or bounded directory scope. Use for size/count questions instead of shell wc/find pipelines.",
			ParametersJSON: schemaObj(
				nil,
				`"path":`+strProp("file or directory to count; defaults to workspace root")+`,"start_line":`+intProp("optional 1-based start line for measuring byte/character length of a line range in one file")+`,"end_line":`+intProp("optional 1-based end line for measuring byte/character length of a line range in one file")+`,"recursive":{"type":"boolean","description":"walk directories recursively"},"glob":`+strProp("optional file glob, e.g. *.go")+`,"max_depth":`+intProp("directory depth limit; default 1, or 8 when recursive=true")+`,"limit":`+intProp("max files to count; default 1000, cap 10000"),
			),
		},
		{
			Name:        ToolNameSystemSearchAccess,
			Description: "Ask the operator to approve read-only access to a file or directory outside the configured workspace, such as a sibling repository. Approval is session-scoped, may be limited to the selected path or widened to the selected folder's contents, and does not permit writes or shell outside the workspace.",
			ParametersJSON: schemaObj(
				[]string{"path", "purpose"},
				`"path":`+strProp("operator-approved external path or path relative to the current workspace working directory, e.g. ../idiom, ../another-repo, or ../another-repo/README.md")+`,"purpose":`+strProp("why this external read-only access is necessary for the user request"),
			),
		},
		{
			Name:        ToolNameCodeNavigate,
			Description: "Route a language-agnostic code navigation request to the best language-aware symbol/navigation tool for the given path or language, with a bounded generic source-text fallback.",
			ParametersJSON: schemaObj(
				[]string{"capability"},
				`"capability":{"type":"string","enum":`+stringEnumJSON(languagetools.CodeNavigationCapabilityNames())+`,"description":"navigation capability the model needs"},"path":`+strProp("source file or narrow source directory used to infer language and scope")+`,"pattern":`+strProp("symbol/name/pattern for symbol-shaped capabilities")+`,"language":`+strProp("optional language override such as go, cpp, python, typescript, rust, java")+`,"start_line":`+intProp("optional start line for file_outline/read_symbol/extract_body-capable routes")+`,"end_line":`+intProp("optional end line for file_outline/read_symbol/extract_body-capable routes")+`,"limit":`+intProp("max generic fallback matches; default 50"),
			),
		},
		{
			Name:        ToolNameReadOnlyCapability,
			Description: "Invoke a stable read-only capability ID from the capability registry. Routes through existing read-only adapters, preserves adapter fallback/uncertainty in the result wrapper, and leaves direct adapter tools available.",
			ParametersJSON: schemaObj(
				[]string{"capability"},
				`"capability":{"type":"string","enum":`+stringEnumJSON(readOnlyCapabilityIDsForSchema())+`,"description":"read-only capability ID from the registry; source-sanity is intentionally not accepted here"},"path":`+strProp("source file or narrow source directory used by the underlying adapter")+`,"pattern":`+strProp("symbol/name/pattern for symbol-shaped capabilities")+`,"language":`+strProp("optional language override such as go, cpp, python, typescript, rust, java")+`,"start_line":`+intProp("optional start line for file-outline/inspect-symbol routes")+`,"end_line":`+intProp("optional end line for file-outline/inspect-symbol routes")+`,"limit":`+intProp("max generic fallback matches; default 50"),
			),
		},
		{
			Name:        ToolNameSourceSanity,
			Description: "Run source-aware sanity for edited source files. Routes to the file language's diagnostics when available; generic fallback is advisory and does not satisfy C/C++ terminal source_sanity when concrete diagnostics are unavailable.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("source file or narrow source directory")+`,"limit":`+intProp("max files to check when path is a directory; default 24"),
			),
		},
		{
			Name:        ToolNameCodingRulesCheck,
			Description: "Run configured coding rules and return typed source-pattern findings without raw source bodies.",
			ParametersJSON: schemaObj(
				nil,
				`"path":`+strProp("optional workspace-relative Go file or directory to check; defaults to workspace root")+`,"config_path":`+strProp("optional workspace-relative policy path; defaults to .idiom/coding_rules.json"),
			),
		},
		{
			Name:        ToolNameHarnessAudit,
			Description: "Audit idiom itself: exported tool/schema coverage, language capability wiring, backend availability, recent JSONL behavior logs, and passive autonomy contract event coverage when present. Use only for harness/tooling improvement questions, not as a replacement for code search.",
			ParametersJSON: schemaObj(
				nil,
				`"purpose":{"type":"string","enum":["all","tool_surface","behavior","planning_quality"],"description":"audit scope; all includes static tool-surface checks, recent log behavior, and planning-quality feedback"},"path":`+strProp("optional log file or directory to scan for behavior signals; defaults to recent logs/conversations JSONL logs")+`,"glob":`+strProp("optional workspace-relative glob of JSONL logs to scan")+`,"limit":`+intProp("max recent log files to scan; default 80, cap 5000"),
			),
		},
		{
			Name:        ToolNameHarnessFeedback,
			Description: "Record structured feedback about idiom harness/tool/prompt behavior that affected the current task. Use only for generic idiom capability gaps, confusing contracts, stale-context failures, telemetry gaps, or workflow friction; do not use for product-code bugs. ALL of `category`, `component`, `expected`, `observed`, `impact`, `confidence` are REQUIRED; `evidence` should be a non-empty array of repo-relative paths or step ids; `category` and `confidence` must come from the documented enum values exactly. Validation rejects partial submissions silently — fill every required field on the first call.",
			ParametersJSON: schemaObj(
				[]string{"category", "component", "expected", "observed", "impact", "confidence"},
				`"category":{"type":"string","enum":["tool_gap","prompt_gap","workflow_gap","context_gap","stale_context","language_support","routing_gap","verification_gap","telemetry_gap","audit_gap","planning_quality","packet_quality","impact_ranking","plan_audit_gap","budget_gap","replay_eval_gap","missing_affordance","bad_scope_rank","stale_fact","backend_gap","packet_bloat","read_loop","edit_readiness_failure","unsupported_plan_claim","validation_gap","language_precision_gap","other"],"description":"generic feedback class"},"component":`+strProp("idiom surface involved, e.g. context_pack, context_impact, architect/plan-audit, prompt/base, direct tool CLI")+`,"expected":`+strProp("capability or contract the agent reasonably expected")+`,"observed":`+strProp("what actually happened, stated as evidence rather than speculation")+`,"workaround":`+strProp("how the agent proceeded, if any")+`,"impact":`+strProp("task effect such as extra reads, blocked edit, wrong routing, or missing audit signal")+`,"evidence":{"type":"array","items":{"type":"string"},"description":"short log paths, step ids, tool outputs, or file references backing the observation"},"confidence":{"type":"string","enum":["verified","inferred"],"description":"verified when backed by logs/tool output; inferred when observed indirectly"},"severity":{"type":"string","enum":["low","medium","high"],"description":"task impact; default low"},"suggested_fix":`+strProp("optional generic harness/tool/prompt improvement, not a one-off task-specific workaround"),
			),
		},
		{
			Name:        ToolNameRecoveryInspect,
			Description: "Read a durable recovery failure record and current workspace partial-state classification without running models or mutating files.",
			ParametersJSON: schemaObj(
				nil,
				`"session_id":`+strProp("plan-execute session id; either session_id or failure_id is required")+`,"failure_id":`+strProp("recovery failure id such as plan-execute-session-001; either failure_id or session_id is required"),
			),
		},
		{
			Name:        ToolNameRecoveryStatus,
			Description: "Read a recovery decomposition packet plus any valid reconciliation artifact, and return status, blockers, gates, candidate atoms, product links, and next valid action.",
			ParametersJSON: schemaObj(
				nil,
				`"packet_path":`+strProp("workspace-relative path to packet.json; either packet_path or session_id/failure_id is required")+`,"session_id":`+strProp("plan-execute session id used to derive the default packet path")+`,"failure_id":`+strProp("recovery failure id used to derive the default packet path"),
			),
		},
		{
			Name:        ToolNameRecoveryDecompose,
			Description: "Write or rebuild a local .idiom/recovery decomposition packet from an existing durable failure record. Not available in discovery profile.",
			ParametersJSON: schemaObj(
				nil,
				`"session_id":`+strProp("plan-execute session id; either session_id or failure_id is required")+`,"failure_id":`+strProp("recovery failure id such as plan-execute-session-001; either failure_id or session_id is required")+`,"output_path":`+strProp("optional workspace-relative output path under .idiom/recovery; defaults to .idiom/recovery/<failure-id>/packet.json"),
			),
		},
		{
			Name:        ToolNameContextIndex,
			Description: "Build or refresh idiom's persistent, language-agnostic codebase comprehension graph. This writes the active SQLite graph plus a JSON export; use it when context_status is missing/stale or before generating packet-ready context.",
			ParametersJSON: schemaObj(
				nil,
				contextToolInputSchema()+`,"language":`+strProp("language hint; auto indexes recognized source files, with rich Go/C++/Python/TypeScript/JavaScript/Rust/Java/GDScript collectors, Godot project resource support, and generic cross-language fallback")+`,"mode":{"type":"string","enum":["quick","standard","deep"],"description":"indexing depth; standard records fast language/backend/build-system enrichment, deep also runs bounded live language-tool/LSP probes and normalizes parseable relationship evidence"}`,
			),
		},
		{
			Name:        ToolNameContextStatus,
			Description: "Report whether the persistent SQLite-backed codebase comprehension graph exists, whether indexed files/symbols or collector versions are stale, and whether storage backends are compatible. Stale/missing path lists are sample-capped by default while count fields preserve the full size. With refresh=true, repair bounded stale scopes, stale file sets, missing indexes, and collector-version mismatches before reporting; explicit freshness_max_files or limit values cap unscoped stale-file refreshes and report budget_exceeded when exceeded.",
			ParametersJSON: schemaObj(
				nil,
				`"refresh":`+boolProp("optional: attempt stale graph repair before reporting; uses path when provided, otherwise refreshes a stale/missing file set or rebuilds the root for missing indexes/collector-version mismatches")+`,`+contextToolInputSchema()+`,"language":`+strProp("optional language hint for refresh; auto remains language-agnostic")+`,"mode":{"type":"string","enum":["quick","standard","deep"],"description":"refresh indexing depth; default standard"},`+freshnessPolicyProps(),
			),
		},
		{
			Name:        ToolNameContextQuery,
			Description: "Query idiom's persistent codebase graph for repository, package, file, symbol, summary, import, call, or test relationships without dumping raw source. Results are observational and include confidence/staleness.",
			ParametersJSON: schemaObj(
				nil,
				`"query":`+strProp("natural-language or keyword query")+`,"pattern":`+strProp("optional symbol or handle hint")+`,`+contextToolInputSchema()+`,"resolution":{"type":"string","enum":["repository","package","file","symbol","summary","any"],"description":"requested abstraction level"},"include_evidence":`+boolProp("include edge evidence such as path/line")+`,`+freshnessPolicyProps(),
			),
		},
		{
			Name:        ToolNameContextZoom,
			Description: "Return a deeper or broader view for one context graph handle, path, or symbol. Use after context_query returns a promising handle.",
			ParametersJSON: schemaObj(
				nil,
				`"id":`+strProp("context graph handle such as symbol://path.go#Name")+`,"handle":`+strProp("alias for id; accepts file://, symbol://, package://, and other context graph handles")+`,"pattern":`+strProp("optional symbol fallback when id/handle is not known")+`,`+contextToolInputSchema()+`,"resolution":{"type":"string","enum":["repository","package","file","symbol","summary","any"],"description":"requested abstraction level"},"include_source":`+boolProp("reserved for future exact source excerpts")+`,`+freshnessPolicyProps(),
			),
		},
		{
			Name:        ToolNameContextPack,
			Description: "Generate a saved, packet-ready context view for a task from the persistent graph. Returns candidate files, symbols, tests, evidence handles, risks, unknowns, and stale-context gaps without prescribing edits.",
			ParametersJSON: schemaObj(
				[]string{"task"},
				`"task":`+strProp("task statement to localize")+`,"query":`+strProp("optional task/query fallback")+`,`+contextToolInputSchema()+`,"mode":{"type":"string","enum":["plan","critique","brief","implement"],"description":"packet mode; default plan"},"budget_tokens":`+intProp("optional intended context budget for downstream packet rendering")+`,"required_tests":{"type":"array","items":{"type":"string"},"description":"exact validation commands that must be carried into the pack"},"required_edit_paths":{"type":"array","items":{"type":"string"},"description":"caller-declared workspace-relative edit paths to surface and prioritize as scope evidence"},"forbidden_paths":{"type":"array","items":{"type":"string"},"description":"workspace-relative paths to omit from candidate files"},`+freshnessPolicyProps(),
			),
		},
		{
			Name:        ToolNameContextImpact,
			Description: "Rank task-relevant files, symbols, and validation candidates with signal explanations from the persistent graph. Use before planner packet generation when you need affected-scope evidence without raw source flooding.",
			ParametersJSON: schemaObj(
				nil,
				`"task":`+strProp("optional task statement to localize; path-only requests derive a bounded evidence task from path")+`,"query":`+strProp("optional task/query fallback")+`,`+contextToolInputSchema()+`,"mode":{"type":"string","enum":["plan","critique","brief","implement"],"description":"ranking mode; default plan"},"budget_tokens":`+intProp("optional intended downstream context budget")+`,"required_tests":{"type":"array","items":{"type":"string"},"description":"exact validation commands to include in ranking evidence"},"required_edit_paths":{"type":"array","items":{"type":"string"},"description":"caller-declared workspace-relative edit paths to surface and prioritize as scope evidence"},"forbidden_paths":{"type":"array","items":{"type":"string"},"description":"workspace-relative paths to omit from candidate files"},`+freshnessPolicyProps(),
			),
		},
		{
			Name:        ToolNameContextTracePack,
			Description: "Run a command and save language-agnostic runtime evidence under .idiom/comprehension/traces. Parses generic stack frames, test output, Go test JSON, Go coverprofiles, and LCOV without overwriting static graph facts.",
			ParametersJSON: schemaObj(
				[]string{"command"},
				`"command":`+strProp("runtime command to execute, such as a focused test command")+`,`+contextToolInputSchema()+`,"language":`+strProp("optional language hint; auto keeps the output language-agnostic while enabling collector-specific parsers when recognizable")+`,"workdir":`+strProp("optional workspace-relative working directory")+`,"include_files":`+boolProp("include parsed executed-file and stack-frame evidence; default true"),
			),
		},
		{
			Name:        ToolNameDiscoveryQuery,
			Description: "Run a deterministic layered discovery recipe for a configured purpose and persist the full evidence packet under .idiom/discovery/queries. Uses context_pack orientation when the graph is fresh or a bounded stale scope can be refreshed, then falls back to live probes for stale, missing, or too-coarse claims.",
			ParametersJSON: schemaObj(
				[]string{"purpose"},
				`"purpose":{"type":"string","enum":`+stringEnumJSON(DiscoveryQueryPurposeValues())+`,"description":"deterministic recipe to run"},"path":`+strProp("optional target file/directory")+`,"pattern":`+strProp("optional symbol, function, type, package, or search pattern")+`,"language":`+strProp("optional language/ecosystem hint, e.g. go, typescript, python, cpp, rust, java")+`,"max_depth":`+intProp("optional relationship depth/budget; default 2")+`,`+freshnessPolicyProps(),
			),
		},
		{
			Name:        ToolNameStructuralSearch,
			Description: "Search code by AST shape via ast-grep. Use for syntax-shaped questions that grep and symbol tools cannot answer precisely.",
			ParametersJSON: schemaObj(
				[]string{"pattern"},
				`"pattern":`+strProp("ast-grep pattern, e.g. if $COND { $BODY }")+`,"language":`+strProp("ast-grep language id, e.g. go, ts, python, cpp, rust, java")+`,"path":`+strProp("file or directory to search"),
			),
		},
		{
			Name:        ToolNameDependencyGraph,
			Description: "Summarize package/module/import dependencies. Uses native Go/Rust tools where available, Madge/Pydeps if installed, or lightweight local import extraction for TS/JS and Python.",
			ParametersJSON: schemaObj(
				nil,
				`"language":`+strProp("optional ecosystem: go, rust, typescript, python, java")+`,"path":`+strProp("file or directory to analyze"),
			),
		},
		{
			Name:        ToolNameHistoryRange,
			Description: "Show git history for a file or specific line range using git log -L. Path-only requests default to a bounded current-file range.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("file path")+`,"start_line":`+intProp("optional 1-based start; if provided, end_line is also required")+`,"end_line":`+intProp("optional 1-based end; if provided, start_line is also required"),
			),
		},
		{
			Name:        ToolNameHistorySymbol,
			Description: "Show git history for a function/symbol using git log -L :symbol:path.",
			ParametersJSON: schemaObj(
				[]string{"path", "pattern"},
				`"path":`+strProp("file path")+`,"pattern":`+strProp("function or symbol regex understood by git log -L"),
			),
		},
		{
			Name:        ToolNameBlameRange,
			Description: "Show git blame for a file or line range.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("file path")+`,"start_line":`+intProp("optional 1-based start")+`,"end_line":`+intProp("optional 1-based end"),
			),
		},
		{
			Name:        ToolNameCtagsSymbols,
			Description: "Fallback symbol discovery via Universal Ctags JSON output. Use when the primary language-aware route does not cover a file type.",
			ParametersJSON: schemaObj(
				nil,
				`"path":`+strProp("file or directory to index")+`,"pattern":`+strProp("optional symbol name filter"),
			),
		},
		{
			Name:        ToolNameSemgrepScan,
			Description: "Run Semgrep for security/dataflow-shaped discovery. Requires semgrep on PATH; pattern is the Semgrep config, defaulting to auto.",
			ParametersJSON: schemaObj(
				nil,
				`"path":`+strProp("file or directory to scan")+`,"pattern":`+strProp("Semgrep config, e.g. auto or p/default"),
			),
		},
		{
			Name:        ToolNameJoernScan,
			Description: "Run a heavyweight Joern code-property-graph vulnerability scan. Requires joern-scan on PATH.",
			ParametersJSON: schemaObj(
				nil,
				`"path":`+strProp("file or directory to scan"),
			),
		},
		{
			Name:        ToolNameGitStatus,
			Description: "Inspect the git working tree status.",
			ParametersJSON: schemaObj(
				nil,
				`"workdir":`+strProp("working directory, defaults to workspace root"),
			),
		},
		{
			Name:        ToolNameGitDiff,
			Description: "Inspect the git diff for the working tree or a path.",
			ParametersJSON: schemaObj(
				nil,
				`"workdir":`+strProp("working directory")+`,"path":`+strProp("optional path to scope the diff"),
			),
		},
		{
			Name:        ToolNameShell,
			Description: "Run a shell command in the workspace. Same OS permissions as this process.",
			ParametersJSON: schemaObj(
				[]string{"command"},
				`"command":`+strProp("the command line")+`,"workdir":`+strProp("working directory"),
			),
		},
		{
			Name:        ToolNameMkdir,
			Description: "Create a directory.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("directory to create"),
			),
		},
		{
			Name:        ToolNameEditFile,
			Description: "Bounded edit for an existing file. Use exactly one body shape: old_text+new_text for an exact substring replacement, OR start_line+end_line+content for a line-range replacement. Preferred for existing source/test edits. Do not pass content without start_line/end_line; do not use this for whole-file replacement. Requires a one-sentence description of the change's intent.",
			ParametersJSON: schemaObj(
				[]string{"path", "description"},
				`"path":`+strProp("file path")+`,"start_line":`+intProp("1-based start for the line-range shape; use with end_line and content")+`,"end_line":`+intProp("1-based end for the line-range shape; use with start_line and content")+`,"old_text":`+strProp("exact text to replace for the exact-match shape; use with new_text and omit line range fields")+`,"new_text":`+strProp("replacement text for the exact-match shape; use with old_text")+`,"content":`+strProp("replacement content for the line-range shape; use with start_line and end_line")+`,"description":`+strProp("one sentence on what this change is and why")+`,"task_id":`+strProp("optional: id of the planned task_ledger item this write fulfills, so the plan panel can check it off in place"),
			),
		},
		{
			Name:        ToolNameWriteFile,
			Description: "Full-body write with exact contents. Use for missing/new files or non-source deliverables where complete file authorship is intended; prefer edit_file or apply_patch for existing source/test edits. Requires a one-sentence description of the change's intent.",
			ParametersJSON: schemaObj(
				[]string{"path", "content", "description"},
				`"path":`+strProp("file path")+`,"content":`+strProp("full file contents")+`,"description":`+strProp("one sentence on what this file is for and why it's being written")+`,"task_id":`+strProp("optional: id of the planned task_ledger item this write fulfills, so the plan panel can check it off in place"),
			),
		},
		{
			Name:        ToolNameAppendFile,
			Description: "Append text to an existing file boundary. Use only when appending at the end is explicitly intended; prefer edit_file or apply_patch for source/test changes at known anchors. Requires a one-sentence description of the change's intent.",
			ParametersJSON: schemaObj(
				[]string{"path", "content", "description"},
				`"path":`+strProp("file path")+`,"content":`+strProp("text to append")+`,"description":`+strProp("one sentence on what's being appended and why")+`,"task_id":`+strProp("optional: id of the planned task_ledger item this write fulfills, so the plan panel can check it off in place"),
			),
		},
		{
			Name:        ToolNameApplyPatch,
			Description: "Apply a multi-file patch envelope. The envelope grammar is exactly: outer `*** Begin Patch` / `*** End Patch` markers; per-file sections introduced by `*** Add File: <path>`, `*** Update File: <path>`, or `*** Delete File: <path>`; `*** Update File:` may carry an optional `*** Move to: <new-path>`; hunks inside an Update File start with `@@` and contain lines prefixed by ` `, `+`, or `-`. Do NOT invent additional `*** Foo` directives such as `*** Sanity Check`, `*** Note`, `*** Verification`, or commentary headers — the parser tolerates and skips them but they signal a brief misread. Requires a one-sentence description of the patch's intent.",
			ParametersJSON: schemaObj(
				[]string{"content", "description"},
				`"content":`+strProp("patch envelope using only *** Begin/End Patch, *** Add/Update/Delete File, *** Move to, and @@ hunks")+`,"description":`+strProp("one sentence covering the patch's overall intent")+`,"task_id":`+strProp("optional: id of the planned task_ledger item this patch fulfills, so the plan panel can check it off in place"),
			),
		},
		{
			Name:           ToolNameSubmitPlan,
			Description:    "Submit a durable structured edit plan in plan-then-execute mode. Planner phases use it for supported edit plans; review-repair phases use it only for new corrective edits when a checklist item fails. Clean review-repair closes with Decision: pass instead. Legacy callers may send rationale, pack_id, and edits at the top level; edits must be a JSON array, not a quoted JSON string. Structured callers may send plan_artifact with task, contract, packet, paths, evidence, unresolved_questions, verification_obligations, approval, handoff, and edits. Approval carries stable scope_id, approved_paths, pending_paths, and execution_permission; partial approval may execute only approved scope. Handoff carries a stable id used by executor/audit events. Use this in lieu of per-edit tool calls when the lane is plan-only. Every edit body must be executable final content: no placeholders, ellipses, TODO markers, existing-code comments, omitted bodies, pseudo-code, or prose descriptions. Four edit shapes: (1) substring — path+old_text+new_text replaces the first match; use for exact replacements; empty new_text deletes old_text; set replace_all:true to replace every occurrence of a non-empty old_text; (2) line-range — path+start_line+end_line+non-empty content replaces exactly lines [start_line, end_line] with complete replacement content; use for replacements, including large additions by replacing the anchor line(s) with the unchanged anchor line(s) plus inserted content. For source files, content-only line-range additions must preserve selected anchor line(s) or use old_text/new_text instead, and must not duplicate a source block already present elsewhere in the file; (3) insertion — path+insert_after_line+small inline non-empty content inserts after an existing 1-based line in an existing file and does not replace current lines; do not combine with old_text/new_text, start_line/end_line, replace_all, or content_ref; (4) missing/new-file — path+new_text+replace_all:true with EMPTY old_text writes new_text as the complete file body, creating the file if it doesn't exist. Existing source/test files must use bounded anchored edits, not whole-file replace_all. Large new_text/content bodies are automatically spilled to artifact refs after submission, except insert_after_line which requires inline content in this change. When you used context_pack to localize the work, pass its pack_id back so the executor can rehydrate the same cached view instead of re-discovering.",
			ParametersJSON: submitPlanParametersSchema(),
		},
		{
			Name:        ToolNameDelegateEdit,
			Description: "Hand a single file edit to the configured executor model (the executor-role backend, when one is declared in .idiom/backends.json or as the cfg.Implementer block). The executor receives the spec, emits the workspace tool call, and the runner dispatches it. Use this when you have already decided the exact change and want to keep your own context focused on planning the next path instead of re-reading the file you just wrote. Falls back to direct dispatch if no executor is configured or it returns an unparseable reply, so the lane never stalls. Requires a path plus EITHER old_text+new_text OR start_line+end_line+content.",
			ParametersJSON: schemaObj(
				[]string{"path"},
				`"path":`+strProp("file path")+`,"start_line":`+intProp("1-based start for the line-range shape; use with end_line and content")+`,"end_line":`+intProp("1-based end for the line-range shape; use with start_line and content")+`,"old_text":`+strProp("exact text to replace for the exact-match shape; use with new_text")+`,"new_text":`+strProp("replacement text for the exact-match shape; use with old_text")+`,"content":`+strProp("replacement content for the line-range shape; use with start_line and end_line")+`,"replace_all":`+boolProp("replace every occurrence of old_text instead of just the first")+`,"purpose":`+strProp("one sentence: what the executor should do and why"),
			),
		},
		{
			Name:        ToolNameGoMoveDeclarations,
			Description: "Mechanically move Go declarations between files by tag or explicit list.",
			ParametersJSON: schemaObj(
				[]string{"path", "destination"},
				`"path":`+strProp("source file")+`,"destination":`+strProp("destination file")+`,"pattern":`+strProp("tag:responsibility or explicit list"),
			),
		},
	}
}
