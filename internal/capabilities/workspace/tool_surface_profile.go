package workspace

import (
	"strings"

	"idiom/internal/platform/textutil"
)

type ToolSurfaceProfile string

const (
	ToolSurfaceProfileConversationReadOnly     ToolSurfaceProfile = "conversation_read_only"
	ToolSurfaceProfileReadOnlyDiscovery        ToolSurfaceProfile = "read_only_discovery"
	ToolSurfaceProfileDelegatedReadOnly        ToolSurfaceProfile = "delegated_read_only"
	ToolSurfaceProfileDebugLogInvestigator     ToolSurfaceProfile = "debug_log_investigator"
	ToolSurfaceProfileVerificationOnly         ToolSurfaceProfile = "verification_only"
	ToolSurfaceProfileLocateOnly               ToolSurfaceProfile = "locate_only"
	ToolSurfaceProfilePlanSubmissionPlanner    ToolSurfaceProfile = "plan_submission_planner"
	ToolSurfaceProfilePlanSubmissionReview     ToolSurfaceProfile = "plan_submission_review"
	ToolSurfaceProfileArtifactDistiller        ToolSurfaceProfile = "artifact_distiller"
	ToolSurfaceProfileGuardedWrite             ToolSurfaceProfile = "guarded_write"
	ToolSurfaceProfileIsolatedWrite            ToolSurfaceProfile = "isolated_write"
	ToolSurfaceProfileImplementationScopedEdit ToolSurfaceProfile = "implementation_scoped_edit"
)

type toolSurfaceProjectionID string

const (
	toolSurfaceProjectionReadOnlyDiscovery                toolSurfaceProjectionID = "read_only_discovery"
	toolSurfaceProjectionDirectAnswer                     toolSurfaceProjectionID = "direct_answer"
	toolSurfaceProjectionDelegatedReadOnlyDiscovery       toolSurfaceProjectionID = "delegated_read_only_discovery"
	toolSurfaceProjectionDebugLogInvestigator             toolSurfaceProjectionID = "debug_log_investigator"
	toolSurfaceProjectionVerificationOnly                 toolSurfaceProjectionID = "verification_only"
	toolSurfaceProjectionLocateOnly                       toolSurfaceProjectionID = "locate_only"
	toolSurfaceProjectionPlanSubmissionPlanner            toolSurfaceProjectionID = "plan_submission_planner"
	toolSurfaceProjectionPlanSubmissionReview             toolSurfaceProjectionID = "plan_submission_review"
	toolSurfaceProjectionGuardedWrite                     toolSurfaceProjectionID = "guarded_write"
	toolSurfaceProjectionImplementationScopedEdit         toolSurfaceProjectionID = "implementation_scoped_edit"
	toolSurfaceProjectionImplementationPreWrite           toolSurfaceProjectionID = "implementation_pre_write"
	toolSurfaceProjectionImplementationTargetSource       toolSurfaceProjectionID = "implementation_target_source"
	toolSurfaceProjectionImplementationTargetWriteReady   toolSurfaceProjectionID = "implementation_target_write_ready"
	toolSurfaceProjectionImplementationVerificationReady  toolSurfaceProjectionID = "implementation_verification_ready"
	toolSurfaceProjectionImplementationVerificationRepair toolSurfaceProjectionID = "implementation_verification_repair"
	toolSurfaceProjectionPlanExecuteReviewRepairInitial   toolSurfaceProjectionID = "plan_execute_review_repair_initial"
	toolSurfaceProjectionPlanExecuteReviewRepairTerminal  toolSurfaceProjectionID = "plan_execute_review_repair_terminal"
	toolSurfaceProjectionPlanExecuteSubmitPlanOnly        toolSurfaceProjectionID = "plan_execute_submit_plan_only"
	toolSurfaceProjectionPlanExecuteRequiredSource        toolSurfaceProjectionID = "plan_execute_required_source"
	toolSurfaceProjectionPlanExecuteHistoryPending        toolSurfaceProjectionID = "plan_execute_history_pending"
	toolSurfaceProjectionPlanExecuteCandidateSource       toolSurfaceProjectionID = "plan_execute_candidate_source"
	toolSurfaceProjectionPlanExecuteAnchorRefresh         toolSurfaceProjectionID = "plan_execute_anchor_refresh"
)

type toolSurfaceProjectionSpec struct {
	Steps                  []toolSurfaceProjectionStep
	Exclude                []string
	ExcludeWorkspaceWrites bool
}

type toolSurfaceProjectionStep struct {
	Projection             toolSurfaceProjectionID
	Tools                  []string
	IncludeRuntimeSource   bool
	IncludeRuntimeContext  bool
	IncludeRuntimeHistory  bool
	IncludeRuntimeReadOnly bool
	IncludeWorkspaceWrites bool
	IncludePlannerTools    bool
}

func projectionTools(tools ...string) toolSurfaceProjectionStep {
	return toolSurfaceProjectionStep{Tools: tools}
}

func projectionRef(id toolSurfaceProjectionID) toolSurfaceProjectionStep {
	return toolSurfaceProjectionStep{Projection: id}
}

func projectionRuntimeSourceTools() toolSurfaceProjectionStep {
	return toolSurfaceProjectionStep{IncludeRuntimeSource: true}
}

func projectionRuntimeContextTools() toolSurfaceProjectionStep {
	return toolSurfaceProjectionStep{IncludeRuntimeContext: true}
}

func projectionRuntimeHistoryTools() toolSurfaceProjectionStep {
	return toolSurfaceProjectionStep{IncludeRuntimeHistory: true}
}

func projectionRuntimeReadOnlyTools() toolSurfaceProjectionStep {
	return toolSurfaceProjectionStep{IncludeRuntimeReadOnly: true}
}

func projectionWorkspaceWriteTools() toolSurfaceProjectionStep {
	return toolSurfaceProjectionStep{IncludeWorkspaceWrites: true}
}

func projectionPlanExecutePlannerTools() toolSurfaceProjectionStep {
	return toolSurfaceProjectionStep{IncludePlannerTools: true}
}

var toolSurfaceProjectionSpecs = map[toolSurfaceProjectionID]toolSurfaceProjectionSpec{
	toolSurfaceProjectionReadOnlyDiscovery: {
		Steps: []toolSurfaceProjectionStep{
			projectionRuntimeSourceTools(),
			projectionTools(ToolNameReadOnlyCapability, ToolNameHarnessAudit),
			projectionRuntimeContextTools(),
			projectionTools(ToolNameTask, ToolNameNote),
		},
	},
	toolSurfaceProjectionDirectAnswer: {
		Steps:                  []toolSurfaceProjectionStep{projectionRuntimeReadOnlyTools()},
		ExcludeWorkspaceWrites: true,
		Exclude:                []string{ToolNameSubmitPlan, ToolNameNote, ToolNameTaskLedger, ToolNameShell},
	},
	toolSurfaceProjectionDelegatedReadOnlyDiscovery: {
		Steps:   []toolSurfaceProjectionStep{projectionRef(toolSurfaceProjectionReadOnlyDiscovery)},
		Exclude: []string{ToolNameTask, ToolNameListFiles},
	},
	toolSurfaceProjectionDebugLogInvestigator: {
		Steps: []toolSurfaceProjectionStep{
			projectionTools(ToolNameSearchFiles, ToolNameSearchContent, ToolNameReadFile),
		},
	},
	toolSurfaceProjectionVerificationOnly: {
		Steps: []toolSurfaceProjectionStep{
			projectionTools(ToolNameSourceSanity, ToolNameShell, ToolNameReadFile, ToolNameSearchFiles, ToolNameSearchContent),
		},
	},
	toolSurfaceProjectionLocateOnly: {
		Steps: []toolSurfaceProjectionStep{
			projectionTools(ToolNamePwd, ToolNameSearchFiles, ToolNameSearchContent, ToolNameReadFile, ToolNameCodeNavigate, ToolNameContextQuery, ToolNameContextZoom),
		},
	},
	toolSurfaceProjectionPlanSubmissionPlanner: {
		Steps: []toolSurfaceProjectionStep{
			projectionPlanExecutePlannerTools(),
		},
	},
	toolSurfaceProjectionPlanSubmissionReview: {
		Steps: []toolSurfaceProjectionStep{
			projectionTools(ToolNameReadFile, ToolNameFileStats, ToolNameCodeNavigate, ToolNameSourceSanity),
			projectionTools(ToolNameHarnessAudit, ToolNameHarnessFeedback),
			projectionRuntimeContextTools(),
			projectionRuntimeHistoryTools(),
			projectionTools(ToolNameSubmitPlan),
		},
	},
	toolSurfaceProjectionGuardedWrite: {
		Steps: []toolSurfaceProjectionStep{
			projectionTools(ToolNameTaskLedger, ToolNameTask, ToolNamePwd),
			projectionRuntimeSourceTools(),
			projectionRuntimeContextTools(),
			projectionTools(ToolNameGitStatus, ToolNameGitDiff),
			projectionRuntimeHistoryTools(),
			projectionTools(ToolNameShell),
			projectionWorkspaceWriteTools(),
			projectionTools(ToolNameSubmitPlan),
		},
	},
	toolSurfaceProjectionImplementationScopedEdit: {
		Steps: []toolSurfaceProjectionStep{
			projectionWorkspaceWriteTools(),
			projectionTools(ToolNameReadFile, ToolNameCodeNavigate, ToolNameSubmitPlan),
			projectionRuntimeContextTools(),
			projectionTools(ToolNameDependencyGraph, ToolNameCtagsSymbols),
			projectionRuntimeHistoryTools(),
			projectionTools(ToolNameSourceSanity),
		},
	},
	toolSurfaceProjectionImplementationPreWrite: {
		Steps: []toolSurfaceProjectionStep{
			projectionWorkspaceWriteTools(),
			projectionTools(ToolNameSubmitPlan),
		},
	},
	toolSurfaceProjectionImplementationTargetSource: {
		Steps: []toolSurfaceProjectionStep{
			projectionWorkspaceWriteTools(),
			projectionTools(ToolNameListFiles, ToolNameSearchFiles, ToolNameSearchContent, ToolNameRg, ToolNameContextPack, ToolNameContextQuery, ToolNameContextZoom, ToolNameDiscoveryQuery, ToolNameReadFile, ToolNameFileStats, ToolNameCodeNavigate, ToolNameReadOnlyCapability),
			projectionRuntimeHistoryTools(),
		},
	},
	toolSurfaceProjectionImplementationTargetWriteReady: {
		Steps: []toolSurfaceProjectionStep{
			projectionTools(ToolNameReadFile, ToolNameSearchContent, ToolNameFileStats),
			projectionWorkspaceWriteTools(),
			projectionRuntimeHistoryTools(),
		},
	},
	toolSurfaceProjectionImplementationVerificationReady: {
		Steps: []toolSurfaceProjectionStep{
			projectionTools(ToolNameSourceSanity, ToolNameShell, ToolNameGitDiff, ToolNameGitStatus, ToolNameWriteFile, ToolNameApplyPatch, ToolNameMkdir),
		},
	},
	toolSurfaceProjectionImplementationVerificationRepair: {
		Steps: []toolSurfaceProjectionStep{
			projectionTools(ToolNameEditFile, ToolNameWriteFile, ToolNameAppendFile, ToolNameApplyPatch, ToolNameDelegateEdit, ToolNameGoMoveDeclarations, ToolNameReadFile, ToolNameCodeNavigate, ToolNameContextQuery, ToolNameContextZoom, ToolNameSearchContent, ToolNameSourceSanity, ToolNameShell, ToolNameGitDiff, ToolNameGitStatus),
		},
	},
	toolSurfaceProjectionPlanExecuteReviewRepairInitial: {
		Steps: []toolSurfaceProjectionStep{
			projectionTools(ToolNameReadFile, ToolNameCodeNavigate, ToolNameContextStatus, ToolNameContextQuery, ToolNameContextZoom, ToolNameSubmitPlan),
		},
	},
	toolSurfaceProjectionPlanExecuteReviewRepairTerminal: {
		Steps: []toolSurfaceProjectionStep{
			projectionTools(ToolNameReadFile, ToolNameSearchFiles, ToolNameSearchContent, ToolNameFileStats, ToolNameCodeNavigate, ToolNameContextStatus, ToolNameContextQuery, ToolNameContextZoom, ToolNameContextPack, ToolNameContextImpact, ToolNameDiscoveryQuery, ToolNameStructuralSearch),
		},
	},
	toolSurfaceProjectionPlanExecuteSubmitPlanOnly: {
		Steps: []toolSurfaceProjectionStep{projectionTools(ToolNameSubmitPlan)},
	},
	toolSurfaceProjectionPlanExecuteRequiredSource: {
		Steps: []toolSurfaceProjectionStep{projectionTools(ToolNameReadFile, ToolNameCodeNavigate)},
	},
	toolSurfaceProjectionPlanExecuteHistoryPending: {
		Steps: []toolSurfaceProjectionStep{projectionTools(ToolNameHistoryRange, ToolNameHistorySymbol)},
	},
	toolSurfaceProjectionPlanExecuteCandidateSource: {
		Steps: []toolSurfaceProjectionStep{
			projectionTools(ToolNameContextPack, ToolNameContextQuery, ToolNameContextZoom, ToolNameDiscoveryQuery, ToolNameSearchFiles, ToolNameSearchContent, ToolNameReadFile, ToolNameCodeNavigate, ToolNameProductEfforts, ToolNameProductStatus, ToolNameRecoveryInspect, ToolNameRecoveryStatus),
		},
	},
	toolSurfaceProjectionPlanExecuteAnchorRefresh: {
		Steps: []toolSurfaceProjectionStep{projectionTools(ToolNameReadFile, ToolNameCodeNavigate)},
	},
}

func collectToolSurfaceProjectionToolNames(id toolSurfaceProjectionID, seen map[toolSurfaceProjectionID]struct{}) []string {
	spec, ok := toolSurfaceProjectionSpecs[id]
	if !ok {
		return nil
	}
	if seen == nil {
		seen = make(map[toolSurfaceProjectionID]struct{})
	}
	if _, recursive := seen[id]; recursive {
		return nil
	}
	seen[id] = struct{}{}
	defer delete(seen, id)

	var tools []string
	for _, step := range spec.Steps {
		if step.Projection != "" {
			tools = textutil.Merge(tools, collectToolSurfaceProjectionToolNames(step.Projection, seen))
		}
		if step.IncludeWorkspaceWrites {
			tools = textutil.Merge(tools, WorkspaceWriteToolNames())
		}
		if step.IncludePlannerTools {
			tools = textutil.Merge(tools, PlanExecutePlannerToolNames())
		}
		if step.IncludeRuntimeSource {
			tools = textutil.Merge(tools, RuntimeSourceInspectionToolNames())
		}
		if step.IncludeRuntimeContext {
			tools = textutil.Merge(tools, RuntimeContextDiscoveryToolNames())
		}
		if step.IncludeRuntimeHistory {
			tools = textutil.Merge(tools, RuntimeHistoryInspectionToolNames())
		}
		if step.IncludeRuntimeReadOnly {
			tools = textutil.Merge(tools, RuntimeReadOnlyInvestigationToolNames())
		}
		if len(step.Tools) > 0 {
			tools = textutil.Merge(tools, step.Tools)
		}
	}

	if spec.ExcludeWorkspaceWrites {
		tools = toolSurfaceProjectionWithout(tools, WorkspaceWriteToolNames()...)
	}
	if len(spec.Exclude) > 0 {
		tools = toolSurfaceProjectionWithout(tools, spec.Exclude...)
	}

	return tools
}

func toolSurfaceProjectionWithout(tools []string, excluded ...string) []string {
	if len(tools) == 0 || len(excluded) == 0 {
		return textutil.Unique(tools)
	}
	blocked := make(map[string]struct{}, len(excluded))
	for _, tool := range excluded {
		tool = strings.TrimSpace(tool)
		if tool != "" {
			blocked[tool] = struct{}{}
		}
	}

	var out []string
	for _, tool := range tools {
		trimmed := strings.TrimSpace(tool)
		if _, ok := blocked[trimmed]; ok {
			continue
		}
		out = append(out, trimmed)
	}

	return textutil.Unique(out)
}

type toolSurfaceProfileSpec struct {
	Projection           toolSurfaceProjectionID
	ManifestOptions      ToolManifestToolOptions
	RequiredTools        []string
	ExternalToolsAllowed bool
}

var toolSurfaceProfileSpecs = map[ToolSurfaceProfile]toolSurfaceProfileSpec{
	ToolSurfaceProfileArtifactDistiller: {
		Projection: "",
	},
	ToolSurfaceProfileDebugLogInvestigator: {
		Projection: toolSurfaceProjectionDebugLogInvestigator,
	},
	ToolSurfaceProfileVerificationOnly: {
		Projection: toolSurfaceProjectionVerificationOnly,
	},
	ToolSurfaceProfileLocateOnly: {
		Projection:           toolSurfaceProjectionLocateOnly,
		ExternalToolsAllowed: true,
	},
	ToolSurfaceProfileGuardedWrite: {
		Projection:           toolSurfaceProjectionGuardedWrite,
		ManifestOptions:      ToolManifestToolOptions{IncludeTaskLedger: true, IncludeTask: true, IncludeWriteTools: true},
		ExternalToolsAllowed: true,
	},
	ToolSurfaceProfileIsolatedWrite: {
		Projection:           toolSurfaceProjectionGuardedWrite,
		ManifestOptions:      ToolManifestToolOptions{IncludeTaskLedger: true, IncludeTask: true, IncludeWriteTools: true},
		ExternalToolsAllowed: true,
	},
	ToolSurfaceProfilePlanSubmissionPlanner: {
		Projection:           toolSurfaceProjectionPlanSubmissionPlanner,
		ManifestOptions:      ToolManifestToolOptions{IncludePlanThenExecute: true, PlanThenExecutePlannerSurface: true},
		RequiredTools:        []string{ToolNameSubmitPlan},
		ExternalToolsAllowed: true,
	},
	ToolSurfaceProfilePlanSubmissionReview: {
		Projection:      toolSurfaceProjectionPlanSubmissionReview,
		ManifestOptions: ToolManifestToolOptions{IncludePlanThenExecute: true},
		RequiredTools:   []string{ToolNameSubmitPlan},
	},
	ToolSurfaceProfileReadOnlyDiscovery: {
		Projection:           toolSurfaceProjectionReadOnlyDiscovery,
		ManifestOptions:      ToolManifestToolOptions{IncludeTask: true},
		ExternalToolsAllowed: true,
	},
	ToolSurfaceProfileDelegatedReadOnly: {
		Projection:           toolSurfaceProjectionDelegatedReadOnlyDiscovery,
		ExternalToolsAllowed: true,
	},
	ToolSurfaceProfileConversationReadOnly: {
		Projection:           toolSurfaceProjectionDirectAnswer,
		ManifestOptions:      ToolManifestToolOptions{IncludeTask: true},
		ExternalToolsAllowed: true,
	},
	ToolSurfaceProfileImplementationScopedEdit: {
		Projection:      toolSurfaceProjectionImplementationScopedEdit,
		ManifestOptions: ToolManifestToolOptions{IncludeWriteTools: true},
	},
}

func ManifestToolsForSurfaceProfile(profile ToolSurfaceProfile) []string {
	available := ToolManifestTools(toolManifestOptionsForSurfaceProfile(profile))
	return AllowedToolsForSurfaceProfile(available, profile)
}

func AllowedToolsForSurfaceProfile(available []string, profile ToolSurfaceProfile) []string {
	if len(available) == 0 {
		return requiredToolsForSurfaceProfile(profile)
	}

	spec, ok := toolSurfaceProfileSpecs[profile]
	if !ok {
		return append([]string(nil), available...)
	}

	return completeAllowedToolsForSurfaceProfile(
		filterAllowedToolsForSurfaceProfile(available, spec.ExternalToolsAllowed, collectToolSurfaceProjectionToolNames(spec.Projection, nil)...),
		profile,
	)
}

func completeAllowedToolsForSurfaceProfile(tools []string, profile ToolSurfaceProfile) []string {
	return textutil.Merge(tools, requiredToolsForSurfaceProfile(profile))
}

func requiredToolsForSurfaceProfile(profile ToolSurfaceProfile) []string {
	spec, ok := toolSurfaceProfileSpecs[profile]
	if !ok || len(spec.RequiredTools) == 0 {
		return nil
	}

	return append([]string(nil), spec.RequiredTools...)
}

func toolManifestOptionsForSurfaceProfile(profile ToolSurfaceProfile) ToolManifestToolOptions {
	opts := ToolManifestToolOptions{
		IncludeGo:         true,
		IncludeCpp:        true,
		IncludePython:     true,
		IncludeTypescript: true,
		IncludeRust:       true,
		IncludeJava:       true,
	}

	spec, ok := toolSurfaceProfileSpecs[profile]
	if !ok {
		return opts
	}

	opts.IncludeTaskLedger = spec.ManifestOptions.IncludeTaskLedger
	opts.IncludeTask = spec.ManifestOptions.IncludeTask
	opts.IncludeWriteTools = spec.ManifestOptions.IncludeWriteTools
	opts.IncludePlanThenExecute = spec.ManifestOptions.IncludePlanThenExecute
	opts.PlanThenExecutePlannerSurface = spec.ManifestOptions.PlanThenExecutePlannerSurface

	return opts
}

func ReadOnlyDiscoveryToolNames() []string {
	return collectToolSurfaceProjectionToolNames(toolSurfaceProjectionReadOnlyDiscovery, nil)
}

func DirectAnswerSurfaceToolNames() []string {
	return collectToolSurfaceProjectionToolNames(toolSurfaceProjectionDirectAnswer, nil)
}

func filterAllowedToolsForSurfaceProfile(available []string, externalToolsAllowed bool, names ...string) []string {
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

		if externalToolsAllowed && IsExternalToolName(trimmed) {
			out = append(out, tool)
		}
	}

	return out
}

func IsExternalToolName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}

	if strings.HasPrefix(name, "mcp:") {
		return true
	}

	if name == ToolNameRg || name == ToolNameNote {
		return false
	}

	return !IsBuiltInToolName(name)
}
