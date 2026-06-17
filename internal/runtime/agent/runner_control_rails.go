package agent

import (
	"encoding/json"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
	"strings"
	"slices"
)

type runnerControlRails struct {
	Lane                 string   `json:"lane,omitempty"`
	FollowupLane         string   `json:"followup_lane,omitempty"`
	Contract             string   `json:"contract,omitempty"`
	AgentMode            string   `json:"agent_mode,omitempty"`
	WorkflowKind         string   `json:"workflow_kind,omitempty"`
	WorkflowPhase        string   `json:"workflow_phase,omitempty"`
	TaskFamily           string   `json:"task_family,omitempty"`
	PermissionProfile    string   `json:"permission_profile,omitempty"`
	TargetRefs           []string `json:"target_refs,omitempty"`
	RequiredEditPaths    []string `json:"required_edit_paths,omitempty"`
	RequiredEvidence     []string `json:"required_evidence,omitempty"`
	RequiredTools        []string `json:"required_tools,omitempty"`
	AllowedTools         []string `json:"allowed_tools,omitempty"`
	ForbiddenTools       []string `json:"forbidden_tools,omitempty"`
	ReadinessGate        []string `json:"readiness_gate,omitempty"`
	SearchPolicy         []string `json:"search_policy,omitempty"`
	VerificationContract string   `json:"verification_contract,omitempty"`
	StopConditions       []string `json:"stop_conditions,omitempty"`
	FinalCritic          []string `json:"final_critic,omitempty"`
	DynamicInstructions  bool     `json:"dynamic_instruction_attachment,omitempty"`
	FailureToEval        bool     `json:"failure_to_eval"`
}

func (r *Runner) emitControlRailsForBrief(messages []llama.ChatMessage, brief workerruntime.WorkerRuntimeBrief) {
	if r == nil || r.OnEvent == nil {
		return
	}

	rails := r.controlRailsForBrief(brief)

	data, err := json.Marshal(rails)
	if err != nil {
		return
	}

	r.emitEvent(RunEvent{Role: "control_rails", Content: string(data)})
}

func (r *Runner) controlRails(messages []llama.ChatMessage) runnerControlRails {
	if r == nil {
		return runnerControlRails{}
	}

	return r.controlRailsForBrief(r.RuntimeBrief)
}

func (r *Runner) controlRailsForBrief(brief workerruntime.WorkerRuntimeBrief) runnerControlRails {
	brief = workerruntime.NormalizeRuntimeBrief(brief)

	allowedTools := []string(nil)

	if workerruntime.PermissionProfileIsConversationOnly(brief.PermissionProfile) {
		allowedTools = nil
	} else if len(brief.AllowedTools) > 0 {
		allowedTools = append([]string(nil), brief.AllowedTools...)
	} else if r.Workspace != nil {
		allowedTools = r.Workspace.AllowedTools()
	}

	if implementationPreWriteToolSurfaceActive(brief, nil) {
		allowedTools = implementationPreWriteAllowedTools(allowedTools)
	} else if implementationScopedEditSurfaceActive(brief, nil) {
		var extReg *workspace.ExternalToolRegistry

		if r.Workspace != nil {
			extReg = r.Workspace.ExternalToolsRegistry()
		}

		allowedTools = implementationScopedEditAllowedToolsWithRegistry(allowedTools, extReg)
	}

	profile := strings.TrimSpace(brief.PermissionProfile)
	if profile == "" {
		if len(allowedTools) == 0 {
			profile = string(workerruntime.PermissionConversationOnly)
		} else {
			profile = string(workerruntime.PermissionReadOnlyWorkspace)
		}
	}

	rails := runnerControlRails{
		Lane:                 util.FirstNonEmpty(strings.TrimSpace(brief.Lane), r.agentName()),
		Contract:             util.FirstNonEmpty(strings.TrimSpace(brief.Contract), string(workerruntime.ContractDirectAnswer)),
		TaskFamily:           util.FirstNonEmpty(strings.TrimSpace(brief.TaskFamily), string(workerruntime.TaskFamilyDirectAnswer)),
		PermissionProfile:    profile,
		RequiredEditPaths:    append([]string(nil), brief.RequiredEditPaths...),
		RequiredEvidence:     append([]string(nil), brief.RequiredEvidence...),
		RequiredTools:        append([]string(nil), brief.RequiredTools...),
		AllowedTools:         allowedTools,
		ForbiddenTools:       append([]string(nil), brief.ForbiddenTools...),
		ReadinessGate:        append([]string(nil), brief.ReadinessGate...),
		SearchPolicy:         append([]string(nil), brief.SearchPolicy...),
		VerificationContract: util.FirstNonEmpty(strings.TrimSpace(brief.VerificationContract), string(workerruntime.VerificationNotRequiredConversation)),
		StopConditions:       append([]string(nil), brief.StopConditions...),
		FinalCritic:          append([]string(nil), brief.FinalCritic...),
		DynamicInstructions:  r.InstructionAttacher != nil,
	}
	if len(rails.RequiredEvidence) == 0 {
		rails.RequiredEvidence = []string{string(workerruntime.RequiredEvidenceAnswerAddressesUserRequest)}
	}

	if len(rails.StopConditions) == 0 {
		rails.StopConditions = []string{string(workerruntime.StopAnsweredUserRequest), string(workerruntime.StopBlockedByMissingInformation)}
	}

	if runnerBriefRequiresMutation(brief) {
		rails.Lane = util.FirstNonEmpty(strings.TrimSpace(brief.Lane), "main")
		rails.Contract = util.FirstNonEmpty(strings.TrimSpace(brief.Contract), string(workerruntime.ContractImplementationResult))
		rails.TaskFamily = util.FirstNonEmpty(strings.TrimSpace(brief.TaskFamily), string(workerruntime.TaskFamilyImplementation))
		rails.PermissionProfile = util.FirstNonEmpty(strings.TrimSpace(brief.PermissionProfile), string(workerruntime.PermissionGuardedWorkspaceWrite))

		if planThenExecutePlannerBrief(brief) {
			rails.RequiredEvidence = util.Merge([]string{string(workerruntime.RequiredEvidencePlanSubmittedOrSourceSatisfied)}, rails.RequiredEvidence)
			rails.RequiredTools = util.Merge([]string{workspace.ToolNameSubmitPlan}, rails.RequiredTools)
			rails.ForbiddenTools = util.AppendUnique(rails.ForbiddenTools, "unbounded_write")
			rails.ReadinessGate = util.Merge([]string{string(workerruntime.ReadinessSubmitPlanOrSourceSatisfiedOrBlocker)}, rails.ReadinessGate)
			rails.SearchPolicy = util.Merge([]string{"consume_target_packet_first", "trust_preloaded_current_state_anchors_for_scope_not_source_body", "required_existing_paths_need_source_body_before_submit_plan", "submit_plan_when_edit_set_is_supported", "source_satisfied_when_current_state_already_matches", "do_not_call_per_edit_writes"}, rails.SearchPolicy)
			rails.VerificationContract = util.FirstNonEmpty(strings.TrimSpace(rails.VerificationContract), string(workerruntime.VerificationNotRequiredTextMutation))
			rails.StopConditions = util.Merge([]string{string(workerruntime.StopSubmitPlanReturnedOrSourceSatisfiedOrBlocker), string(workerruntime.StopBlockedByMissingPermissionOrInformation)}, rails.StopConditions)
			rails.FinalCritic = []string{"answers_actual_user_request", "submit_plan_called", "source_satisfied_or_blocker_only_with_per_path_evidence"}

			return rails
		}

		rails.RequiredEvidence = defaultRunnerStateSlice(rails.RequiredEvidence, []string{string(workerruntime.RequiredEvidenceChangedPathsOrNoopReason), string(workerruntime.RequiredEvidenceVerificationStatus)})
		if implementationPreWriteToolSurfaceActive(brief, nil) {
			rails.RequiredTools = []string{"edit_file_or_apply_patch"}
		} else if implementationScopedEditSurfaceActive(brief, nil) {
			rails.RequiredTools = []string{"edit_file_or_apply_patch", "semantic_discovery_or_narrow_read_for_named_gap"}
		} else {
			rails.RequiredTools = defaultRunnerStateSlice(rails.RequiredTools, []string{"edit_file_or_apply_patch", "source_sanity_or_verification_shell"})
		}

		rails.ForbiddenTools = util.AppendUnique(rails.ForbiddenTools, "unbounded_write")
		rails.ReadinessGate = defaultRunnerStateSlice(rails.ReadinessGate, []string{string(workerruntime.ReadinessChangedPathsReported), string(workerruntime.ReadinessVerificationStatusReported)})
		rails.SearchPolicy = defaultRunnerStateSlice(rails.SearchPolicy, []string{"consume_target_packet_first", "prefer_small_bounded_edits", "verify_changed_surface"})
		rails.VerificationContract = util.FirstNonEmpty(strings.TrimSpace(rails.VerificationContract), string(workerruntime.VerificationTestsPassedFailedSkipped))
		rails.StopConditions = defaultRunnerStateSlice(rails.StopConditions, []string{string(workerruntime.StopChangeVerified), string(workerruntime.StopBlockedWithFailedVerificationOrScope)})
		rails.FinalCritic = []string{"answers_actual_user_request", "changed_paths_reported", "verification_status_reported"}

		return rails
	}

	if slices.Contains(rails.AllowedTools, workspace.ToolNameShell) && !runnerHasWorkspaceWriteCapability(brief, rails.AllowedTools) && runnerVerificationRequired(brief) {
		rails.TaskFamily = util.FirstNonEmpty(strings.TrimSpace(brief.TaskFamily), "verification")
		rails.Contract = util.FirstNonEmpty(strings.TrimSpace(brief.Contract), string(workerruntime.ContractGroundedExplanation))
		rails.PermissionProfile = util.FirstNonEmpty(strings.TrimSpace(brief.PermissionProfile), string(workerruntime.PermissionReadOnlyVerificationShell))
		rails.RequiredEvidence = defaultRunnerStateSlice(rails.RequiredEvidence, []string{string(workerruntime.RequiredEvidenceVerificationCommand), string(workerruntime.RequiredEvidencePassFailStatus)})
		rails.RequiredTools = defaultRunnerStateSlice(rails.RequiredTools, []string{workspace.ToolNameShell})
		rails.ForbiddenTools = appendRunnerTools(rails.ForbiddenTools, workspace.ToolNameEditFile, workspace.ToolNameWriteFile, workspace.ToolNameAppendFile, workspace.ToolNameMkdir, "mutating_shell")
		rails.ReadinessGate = defaultRunnerStateSlice(rails.ReadinessGate, []string{string(workerruntime.ReadinessVerificationStatusReported)})
		rails.SearchPolicy = defaultRunnerStateSlice(rails.SearchPolicy, []string{"run_requested_check", "capture_failure_output", "do_not_edit"})
		rails.FinalCritic = []string{"answers_actual_user_request", "required_evidence_cited"}

		return rails
	}

	if util.EqualTrimFold(rails.TaskFamily, "debug_logs") {
		rails.FailureToEval = true
		rails.ReadinessGate = defaultRunnerStateSlice(rails.ReadinessGate, []string{"latest_log_selected", "failure_cause_supported_by_log", "low_confidence_triggers_deepening"})

		if len(rails.FinalCritic) == 0 {
			rails.FinalCritic = []string{"answers_actual_user_request", "required_evidence_cited"}
		}
	}

	if rails.Contract == string(workerruntime.ContractArchitecture) {
		rails.ReadinessGate = defaultRunnerStateSlice(rails.ReadinessGate, []string{"inspected_paths_reported", "architecture_boundary_evidence_supported_or_uncertainty_declared"})
		if len(rails.FinalCritic) == 0 {
			rails.FinalCritic = []string{"answers_actual_user_request", "required_evidence_cited"}
		}
	}

	if rails.Contract == string(workerruntime.ContractReviewFindings) {
		rails.ReadinessGate = defaultRunnerStateSlice(rails.ReadinessGate, []string{"inspected_paths_reported", "bounded_findings_or_no_finding_supported"})
		if len(rails.FinalCritic) == 0 {
			rails.FinalCritic = []string{"answers_actual_user_request", "required_evidence_cited"}
		}
	}

	if rails.Contract == string(workerruntime.ContractDirectAnswer) {
		rails.TaskFamily = util.FirstNonEmpty(strings.TrimSpace(brief.TaskFamily), string(workerruntime.TaskFamilyDirectAnswer))
		if len(rails.AllowedTools) == 0 {
			rails.PermissionProfile = string(workerruntime.PermissionConversationOnly)
			rails.ForbiddenTools = util.AppendUnique(rails.ForbiddenTools, workspace.ToolNameShell)
		}

		rails.RequiredEvidence = defaultRunnerStateSlice(rails.RequiredEvidence, []string{string(workerruntime.RequiredEvidenceAnswerAddressesPrompt)})
		rails.VerificationContract = util.FirstNonEmpty(strings.TrimSpace(rails.VerificationContract), string(workerruntime.VerificationNotRequiredReadOnlyLane))
		rails.StopConditions = defaultRunnerStateSlice(rails.StopConditions, []string{string(workerruntime.StopRequiredEvidenceSatisfied)})

		if runnerUsesSoftReadOnlyShapingOnly(brief, rails) {
			rails.ReadinessGate = nil
			rails.FinalCritic = nil
		}
	}

	if runnerUsesSoftReadOnlyShapingOnly(brief, rails) {
		rails.ReadinessGate = nil
		rails.FinalCritic = nil
	}

	return rails
}

func runnerUsesSoftReadOnlyShapingOnly(brief workerruntime.WorkerRuntimeBrief, rails runnerControlRails) bool {
	if runnerBriefRequiresMutation(brief) {
		return false
	}

	if util.EqualTrimFold(rails.TaskFamily, "debug_logs") {
		return false
	}

	switch strings.TrimSpace(rails.Contract) {
	case string(workerruntime.ContractArchitecture), string(workerruntime.ContractReviewFindings):
		return false
	}

	if runnerVerificationRequired(brief) {
		return false
	}

	switch strings.TrimSpace(rails.PermissionProfile) {
	case string(workerruntime.PermissionReadOnlyWorkspace):
		return true
	default:
		return false
	}
}
