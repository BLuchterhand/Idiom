package workerruntime

import (
	"strings"

	"idiom/internal/workspace"
)

type ToolCapabilityProfile string

const (
	CapabilityConversationReadOnly ToolCapabilityProfile = "conversation_read_only"
	CapabilityReadOnlyDiscovery    ToolCapabilityProfile = "read_only_discovery"
	CapabilityDebugLogInvestigator ToolCapabilityProfile = "debug_log_investigator"
	CapabilityVerificationOnly     ToolCapabilityProfile = "verification_only"
	CapabilityLocateOnly           ToolCapabilityProfile = "locate_only"
	CapabilityGuardedWrite         ToolCapabilityProfile = "guarded_write"
	CapabilityIsolatedWrite        ToolCapabilityProfile = "isolated_write"
)

type RuntimeContractSpec struct {
	TaskFamily           string
	Contract             string
	PermissionProfile    string
	ToolProfile          ToolCapabilityProfile
	ForbiddenTools       []string
	RequiredEvidence     []string
	RequiredTools        []string
	ReadinessGate        []string
	SearchPolicy         []string
	VerificationContract string
	StopConditions       []string
	FinalCritic          []string
}

func RuntimeContractForTaskFamily(family string) RuntimeContractSpec {
	switch strings.TrimSpace(family) {
	case "direct_question":
		return RuntimeContractSpec{
			TaskFamily:           "direct_answer",
			Contract:             "direct_answer",
			PermissionProfile:    "conversation_only",
			ToolProfile:          CapabilityConversationReadOnly,
			ForbiddenTools:       append([]string(nil), workspace.WorkspaceWriteToolNames()...),
			RequiredEvidence:     []string{"answer_addresses_prompt"},
			VerificationContract: "not_required_for_read_only_lane",
			StopConditions:       []string{"required_evidence_satisfied"},
			FinalCritic:          []string{"answers_actual_user_request"},
		}
	case "debug_logs":
		return RuntimeContractSpec{
			TaskFamily:           "debug_logs",
			Contract:             "grounded_explanation",
			PermissionProfile:    "read_only_workspace",
			ToolProfile:          CapabilityDebugLogInvestigator,
			ForbiddenTools:       append([]string(nil), workspace.WorkspaceWriteToolNames()...),
			RequiredEvidence:     []string{"log_record", "failure_output", "timestamp_or_latest_selection"},
			RequiredTools:        []string{"log_inspection"},
			ReadinessGate:        []string{"latest_log_selected", "failure_cause_supported_by_log", "low_confidence_triggers_deepening"},
			SearchPolicy:         []string{"start_with_most_recent_log", "extract_failure_window"},
			VerificationContract: "not_required_for_read_only_lane",
			StopConditions:       []string{"log_cause_identified_or_log_gap_declared"},
			FinalCritic:          []string{"answers_actual_user_request", "required_evidence_cited"},
		}
	case "verification_only":
		return RuntimeContractSpec{
			TaskFamily:           "verification_only",
			Contract:             "direct_answer",
			PermissionProfile:    "read_only_verification_shell",
			ToolProfile:          CapabilityVerificationOnly,
			ForbiddenTools:       append(append([]string(nil), workspace.WorkspaceWriteToolNames()...), "task"),
			RequiredEvidence:     []string{"verification_command", "pass_fail_status"},
			RequiredTools:        []string{"shell"},
			ReadinessGate:        []string{"verification_status_reported"},
			SearchPolicy:         []string{"run_requested_check", "capture_failure_output", "do_not_edit"},
			VerificationContract: "not_required_for_read_only_lane",
			StopConditions:       []string{"required_evidence_satisfied"},
			FinalCritic:          []string{"answers_actual_user_request"},
		}
	case "locate":
		return RuntimeContractSpec{
			TaskFamily:           "locate",
			Contract:             "locate_targets",
			PermissionProfile:    "read_only_workspace",
			ToolProfile:          CapabilityLocateOnly,
			RequiredEvidence:     []string{"exact_workspace_path", "line_or_snippet", "target_match_reason"},
			SearchPolicy:         []string{"literal_search_first", "prefer_symbol_or_handle_before_raw_file_read", "fallback_pattern_search", "stop_when_exact_target_is_supported"},
			VerificationContract: "not_required_for_read_only_lane",
			StopConditions:       []string{"required_evidence_satisfied", "blocked_by_missing_information"},
		}
	case "locate_then_implement", "explore_then_implement", "investigate_then_implement", "review_then_implement", "architecture_then_implement":
		return RuntimeContractSpec{
			TaskFamily:           strings.TrimSpace(family),
			Contract:             "implementation_result",
			PermissionProfile:    "analysis_read_only_then_guarded_writes",
			ToolProfile:          CapabilityGuardedWrite,
			RequiredEvidence:     []string{"target_location", "changed_paths", "verification_status"},
			RequiredTools:        []string{"edit_file_or_apply_patch", "verification_shell"},
			ReadinessGate:        []string{"target_location_confirmed", "changed_paths_reported", "verification_status_reported"},
			SearchPolicy:         []string{"delegate_broad_discovery_when_needed", "locate_target_before_edit", "verify_changed_surface"},
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
			StopConditions:       []string{"change_verified", "blocked_with_failed_verification_or_scope"},
			FinalCritic:          []string{"answers_actual_user_request", "changed_paths_reported", "verification_status_reported"},
		}
	case "security_review", "performance_review":
		searchPolicy := []string{"prefer_discovery_query_for_composite_review", "rank_candidates", "answer_when_bounded_finding_or_no_finding_is_supported", "if_continuing_name_one_exact_missing_claim_and_probe", "deepen_if_no_bounded_finding", "use_structural_search_for_syntax_shapes", "use_dependency_graph_for_boundary_or_import_questions"}
		if strings.TrimSpace(family) == "security_review" {
			searchPolicy = append(searchPolicy, "prefer_discovery_query_security_review_before_raw_scans", "prefer_semgrep_scan_for_security_patterns", "reserve_joern_scan_for_heavy_dataflow_questions")
		}
		return RuntimeContractSpec{
			TaskFamily:           strings.TrimSpace(family),
			Contract:             "review_findings",
			PermissionProfile:    "read_only_workspace",
			ToolProfile:          CapabilityReadOnlyDiscovery,
			RequiredEvidence:     []string{"inspected_paths", "bounded_findings_or_no_finding", "confidence"},
			RequiredTools:        []string{"workspace_inspection"},
			ReadinessGate:        []string{"inspected_paths_reported", "bounded_findings_or_no_finding_supported"},
			SearchPolicy:         searchPolicy,
			VerificationContract: "not_required_for_read_only_lane",
			StopConditions:       []string{"required_evidence_satisfied", "budget_exhausted_with_uncertainty_declared"},
			FinalCritic:          []string{"answers_actual_user_request", "required_evidence_cited"},
		}
	case "architecture":
		return RuntimeContractSpec{
			TaskFamily:           "architecture",
			Contract:             "architecture_assessment",
			PermissionProfile:    "read_only_workspace",
			ToolProfile:          CapabilityReadOnlyDiscovery,
			RequiredEvidence:     []string{"inspected_paths", "architecture_boundary_evidence", "confidence"},
			RequiredTools:        []string{"workspace_inspection"},
			ReadinessGate:        []string{"inspected_paths_reported", "architecture_boundary_evidence_supported_or_uncertainty_declared"},
			SearchPolicy:         []string{"prefer_discovery_query_for_architecture_boundary", "trace_boundaries_from_named_components", "answer_when_boundary_evidence_is_sufficient", "if_not_answering_name_one_exact_missing_claim_and_probe", "use_dependency_graph_for_boundary_or_import_questions", "use_history_only_for_evolution_questions"},
			VerificationContract: "not_required_for_read_only_lane",
			StopConditions:       []string{"required_evidence_satisfied", "budget_exhausted_with_uncertainty_declared"},
			FinalCritic:          []string{"answers_actual_user_request", "required_evidence_cited"},
		}
	case "implementation", "test_authoring", "config_edit", "script_edit", "gdscript_edit", "docs_edit", "prompt_edit_security", "failure_injection":
		family = strings.TrimSpace(family)
		// Verification contract differs by family: code-producing
		// families (implementation, test_authoring, config_edit,
		// script_edit, failure_injection) need tests/builds run after
		// the mutation. Families whose edited file is the practical
		// deliverable for this atom (docs_edit, prompt_edit_security,
		// gdscript_edit) opt out of the generic post-mutation gate. The
		// runner reads VerificationContract to decide whether the gate
		// fires; the prefix "not_required_for_*" short-circuits it.
		verificationContract := "tests_passed_or_failed_or_skipped_with_blocker"
		switch family {
		case "docs_edit", "prompt_edit_security", "gdscript_edit":
			verificationContract = "not_required_for_text_mutations"
		}
		return RuntimeContractSpec{
			TaskFamily:           family,
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			ToolProfile:          CapabilityGuardedWrite,
			RequiredEvidence:     []string{"changed_paths_or_noop_reason", "verification_status"},
			RequiredTools:        []string{"edit_file_or_apply_patch", "verification_shell"},
			ReadinessGate:        []string{"changed_paths_reported", "verification_contract_resolved"},
			SearchPolicy:         []string{"consume_target_packet_first", "prefer_small_bounded_edits", "verify_changed_surface"},
			VerificationContract: verificationContract,
			StopConditions:       []string{"implementation_verified_or_failure_reported", "blocked_by_missing_permission_or_information"},
			FinalCritic:          []string{"answers_actual_user_request", "changed_paths_reported", "verification_status_reported"},
		}
	default:
		return RuntimeContractSpec{
			TaskFamily:           "direct_answer",
			Contract:             "direct_answer",
			PermissionProfile:    "conversation_only",
			ToolProfile:          CapabilityConversationReadOnly,
			RequiredEvidence:     []string{"answer_addresses_user_request"},
			VerificationContract: "not_required_for_read_only_lane",
			StopConditions:       []string{"answered_user_request", "blocked_by_missing_information"},
		}
	}
}

func RuntimeBriefForTaskFamily(family string, lane string, allowedWorkspaceTools []string) WorkerRuntimeBrief {
	spec := RuntimeContractForTaskFamily(family)
	if strings.TrimSpace(lane) == "" {
		lane = "main"
	}
	brief := WorkerRuntimeBrief{
		Lane:                 strings.TrimSpace(lane),
		TaskFamily:           spec.TaskFamily,
		Contract:             spec.Contract,
		PermissionProfile:    spec.PermissionProfile,
		AllowedTools:         AllowedToolsForCapabilityProfile(allowedWorkspaceTools, spec.ToolProfile),
		ForbiddenTools:       append([]string(nil), spec.ForbiddenTools...),
		RequiredEvidence:     append([]string(nil), spec.RequiredEvidence...),
		RequiredTools:        append([]string(nil), spec.RequiredTools...),
		ReadinessGate:        append([]string(nil), spec.ReadinessGate...),
		SearchPolicy:         append([]string(nil), spec.SearchPolicy...),
		VerificationContract: spec.VerificationContract,
		StopConditions:       append([]string(nil), spec.StopConditions...),
		FinalCritic:          append([]string(nil), spec.FinalCritic...),
	}
	if spec.PermissionProfile == "conversation_only" {
		// Built-in tools are stripped (no edit/shell/discovery_query in
		// conversation mode) but project-defined external tools survive.
		// Externals are read-only by intent — host projects register
		// them so the agent can answer questions about the codebase
		// without entering full implementation mode. The CLI/prompt
		// path now picks up host-registered external discovery tools
		// even for direct-question routes.
		preserved := make([]string, 0)
		for _, name := range brief.AllowedTools {
			if isExternalToolName(strings.TrimSpace(name)) {
				preserved = append(preserved, name)
			}
		}
		brief.AllowedTools = preserved
		brief.ToolManifest = nil
	}
	if spec.PermissionProfile == "read_only_workspace" {
		brief.ForbiddenTools = mergeStrings(brief.ForbiddenTools, []string{"mutating_shell"})
	}
	if spec.PermissionProfile == "read_only_verification_shell" {
		brief.ForbiddenTools = mergeStrings(brief.ForbiddenTools, []string{"mutating_shell"})
	}
	return NormalizeRuntimeBrief(brief)
}

func ManifestToolsForCapabilityProfile(profile ToolCapabilityProfile) []string {
	return workspace.ToolManifestTools(toolManifestOptionsForProfile(profile))
}

func AllowedToolsForCapabilityProfile(available []string, profile ToolCapabilityProfile) []string {
	if len(available) == 0 {
		return nil
	}
	switch profile {
	case CapabilityDebugLogInvestigator:
		return filterAllowedTools(available, "search_files", "search_content", "read_file")
	case CapabilityVerificationOnly:
		return filterAllowedTools(available, "shell", "read_file", "search_files", "search_content")
	case CapabilityLocateOnly:
		return filterAllowedTools(available, "pwd", "search_files", "search_content", "read_file")
	case CapabilityGuardedWrite, CapabilityIsolatedWrite:
		return selectAllowedTools(available, true, true, true)
	case CapabilityReadOnlyDiscovery:
		return filterAllowedTools(available, readOnlyDiscoveryToolNames()...)
	case CapabilityConversationReadOnly:
		return filterAllowedTools(available, readOnlyConversationToolNames()...)
	default:
		return append([]string(nil), available...)
	}
}

func toolManifestOptionsForProfile(profile ToolCapabilityProfile) workspace.ToolManifestToolOptions {
	opts := workspace.ToolManifestToolOptions{
		IncludeGo:         true,
		IncludeCpp:        true,
		IncludePython:     true,
		IncludeTypescript: true,
		IncludeRust:       true,
		IncludeJava:       true,
	}
	switch profile {
	case CapabilityGuardedWrite, CapabilityIsolatedWrite:
		opts.IncludeTaskLedger = true
		opts.IncludeTask = true
		opts.IncludeWriteTools = true
	case CapabilityReadOnlyDiscovery, CapabilityConversationReadOnly:
		opts.IncludeTask = true
	case CapabilityVerificationOnly, CapabilityLocateOnly, CapabilityDebugLogInvestigator:
		// no extra options
	}
	return opts
}

func readOnlyDiscoveryToolNames() []string {
	return []string{
		"search_files",
		"search_content",
		"read_file",
		"discovery_query",
		"structural_search",
		"dependency_graph",
		"history_range",
		"history_symbol",
		"blame_range",
		"ctags_symbols",
		"semgrep_scan",
		"joern_scan",
		"list_files",
		"go_related_files",
		"go_workspace_symbol",
		"go_references",
		"go_definition",
		"go_calls_from",
		"go_calls_into",
		"go_read_symbol",
		"go_extract_body",
		"go_file_outline",
		"cpp_workspace_symbol",
		"cpp_read_symbol",
		"cpp_related_files",
		"cpp_references",
		"cpp_definition",
		"cpp_calls_from",
		"cpp_calls_into",
		"cpp_extract_body",
		"cpp_file_outline",
		"python_workspace_symbol",
		"python_read_symbol",
		"python_related_files",
		"python_references",
		"python_definition",
		"python_calls_from",
		"python_calls_into",
		"python_file_outline",
		"ts_workspace_symbol",
		"ts_read_symbol",
		"ts_related_files",
		"ts_definition",
		"ts_references",
		"ts_calls_from",
		"ts_calls_into",
		"ts_file_outline",
		"rust_workspace_symbol",
		"rust_read_symbol",
		"rust_related_files",
		"rust_file_outline",
		"rust_definition",
		"rust_references",
		"rust_calls_from",
		"rust_calls_into",
		"java_workspace_symbol",
		"java_read_symbol",
		"java_related_files",
		"java_definition",
		"java_references",
		"java_calls_from",
		"java_calls_into",
		"java_file_outline",
		"task",
	}
}

func readOnlyConversationToolNames() []string {
	return readOnlyDiscoveryToolNames()
}

func selectAllowedTools(available []string, allowWrites bool, allowShell bool, allowTask bool) []string {
	if len(available) == 0 {
		return nil
	}
	var out []string
	for _, tool := range available {
		switch {
		case workspace.IsWorkspaceWriteTool(tool):
			if allowWrites {
				out = append(out, tool)
			}
		case tool == "shell":
			if allowShell {
				out = append(out, tool)
			}
		case tool == "task", tool == "task_ledger":
			if allowTask {
				out = append(out, tool)
			}
		default:
			out = append(out, tool)
		}
	}
	return out
}

func filterAllowedTools(available []string, names ...string) []string {
	if len(available) == 0 || len(names) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			allowed[name] = struct{}{}
		}
	}
	var out []string
	for _, tool := range available {
		trimmed := strings.TrimSpace(tool)
		if _, ok := allowed[trimmed]; ok {
			out = append(out, tool)
			continue
		}
		// Pass project-defined external tools through every capability
		// profile. Built-ins are gated by the hardcoded allow-lists
		// because their semantics (read vs write, mutating vs not) are
		// known to idiom; externals are opaque shell invocations whose
		// safety the host project owns. Always-passing-through means
		// `external_tools` registered in the workspace config remain
		// callable from any turn route, which is the contract host
		// projects expect when registering project-specific discovery
		// tools.
		//
		// MCP tools (`mcp:<server>/<tool>`) are also pass-through here
		// to preserve their existing behavior; the qualifier lets the
		// dispatcher route them through executeMCP regardless of the
		// turn's capability profile.
		if isExternalToolName(trimmed) {
			out = append(out, tool)
		}
	}
	return out
}

// isExternalToolName reports whether a tool name belongs to a host-project
// external tool or an MCP-discovered tool, as opposed to a built-in
// idiom tool. Built-ins are enumerated in the workspace's tool manifest;
// anything that isn't a built-in (and isn't reserved by the runner) is
// treated as a registered external/MCP tool the project owns.
//
// Implementation: built-ins follow a small set of stable name patterns
// (`go_*`, `python_*`, `ts_*`, `rust_*`, `java_*`, `cpp_*`, plus the
// hardcoded utility names like `pwd`, `read_file`, `discovery_query`,
// etc.). MCP tools always carry the `mcp:` prefix. Anything that doesn't
// match either is assumed to be a project external.
func isExternalToolName(name string) bool {
	if name == "" {
		return false
	}
	if strings.HasPrefix(name, "mcp:") {
		return true
	}
	if _, ok := builtinToolNames[name]; ok {
		return false
	}
	// Language-prefixed built-ins (kept here, not in the map, because
	// their full set is large and updated in lock-step with language
	// support additions in workspace/).
	for _, prefix := range []string{"go_", "python_", "py_", "ts_", "rust_", "java_", "cpp_"} {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	return true
}

// builtinToolNames enumerates non-language-prefixed built-in tools.
// Kept in lock-step with the dispatch switch in workspace/manager.go's
// Manager.Execute. New built-ins must be added here so external-tool
// pass-through doesn't accidentally include them.
var builtinToolNames = map[string]struct{}{
	"task_ledger":        {},
	"task":               {},
	"pwd":                {},
	"list_files":         {},
	"search_files":       {},
	"search_content":     {},
	"rg":                 {},
	"read_file":          {},
	"discovery_query":    {},
	"structural_search":  {},
	"dependency_graph":   {},
	"history_range":      {},
	"history_symbol":     {},
	"blame_range":        {},
	"ctags_symbols":      {},
	"semgrep_scan":       {},
	"joern_scan":         {},
	"git_status":         {},
	"git_diff":           {},
	"shell":              {},
	"mkdir":              {},
	"edit_file":          {},
	"write_file":         {},
	"append_file":        {},
	"apply_patch":        {},
}
