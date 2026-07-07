package workspace

import "strings"

const (
	RequiredToolGitHistoryLookup                         = "history_range_or_history_symbol"
	RequiredToolEditFileOrApplyPatch                     = "edit_file_or_apply_patch"
	RequiredToolSourceSanityOrVerificationShell          = "source_sanity_or_verification_shell"
	RequiredToolVerificationShell                        = "verification_shell"
	RequiredToolBoundedWorkspaceEvidence                 = "bounded_workspace_evidence_tool"
	RequiredToolSemanticDiscoveryOrNarrowReadForNamedGap = "semantic_discovery_or_narrow_read_for_named_gap"
	RequiredToolExactPendingSourceBodyFollowupOrEdit     = "exact_pending_source_body_followup_or_edit_file_or_apply_patch"
)

func ConcreteRequiredTools(requiredTool string) []string {
	switch strings.TrimSpace(requiredTool) {
	case RequiredToolGitHistoryLookup:
		return ToolSurfacePhaseToolNames(ToolSurfacePhasePlanExecuteHistoryPending)
	case RequiredToolEditFileOrApplyPatch:
		return WorkspacePerEditWriteToolNames()
	case RequiredToolSourceSanityOrVerificationShell, RequiredToolVerificationShell:
		return []string{ToolNameSourceSanity, ToolNameShell}
	case RequiredToolBoundedWorkspaceEvidence:
		return ToolSurfacePhaseToolNames(ToolSurfacePhasePlanExecuteCandidateSource)
	default:
		tool := strings.TrimSpace(requiredTool)
		if tool == "" {
			return nil
		}

		return []string{tool}
	}
}
