package workerruntime

import (
	"fmt"
	"slices"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
)

var _ = ginkgo.Describe("runtime contracts for shared task-family profiles", func() {
	ginkgo.It("artifact final answer", func() {
		spec := RuntimeContractForTaskFamily(ArtifactFinalAnswerTaskFamily)
		gomega.Expect(spec.TaskFamily).To(gomega.Equal(autonomy.TaskFamilyArtifactFinalAnswer), fmt.Sprintf("unexpected artifact final-answer contract: %#v", spec))
		gomega.Expect(spec.Contract).To(gomega.Equal(ContractArtifactFinalAnswer), fmt.Sprintf("unexpected artifact final-answer contract: %#v", spec))

		gomega.Expect(spec.PermissionProfile != "conversation_only").To(gomega.BeFalse(), fmt.Sprintf("permission = %q, want conversation_only", spec.PermissionProfile))

		gomega.Expect(runtimeLabelContains(spec.FinalCritic, "no_tool_calls")).To(gomega.BeTrue(), fmt.Sprintf("artifact contract should require no tool calls, got %#v", spec.FinalCritic))
	})

	ginkgo.It("artifact distiller", func() {
		spec := RuntimeContractForTaskFamily(ArtifactDistillerTaskFamily)
		gomega.Expect(spec.TaskFamily).To(gomega.Equal(autonomy.TaskFamilyArtifactDistiller), fmt.Sprintf("unexpected artifact distiller contract: %#v", spec))
		gomega.Expect(spec.Contract).To(gomega.Equal(ContractEditIntentDistill), fmt.Sprintf("unexpected artifact distiller contract: %#v", spec))

		gomega.Expect(spec.PermissionProfile).To(gomega.Equal(PermissionConversationOnly), fmt.Sprintf("artifact distiller must be tool-free/no-write, got %#v", spec))
		gomega.Expect(spec.ToolProfile).To(gomega.Equal(workspace.ToolSurfaceProfileArtifactDistiller), fmt.Sprintf("artifact distiller must be tool-free/no-write, got %#v", spec))

		for _, want := range []string{"raw_output_parser_failure", "active_mutation_contract", "required_edit_scope"} {
			gomega.Expect(runtimeLabelContains(spec.RequiredEvidence, want)).To(gomega.BeTrue(), fmt.Sprintf("artifact distiller missing evidence %q in %#v", want, spec.RequiredEvidence))
		}

		for _, want := range []string{"outputs_only_edit_intent_or_typed_blocker", "no_tool_calls", "no_write_authority", "no_submit_plan"} {
			gomega.Expect(runtimeLabelContains(spec.FinalCritic, want)).To(gomega.BeTrue(), fmt.Sprintf("artifact distiller missing critic %q in %#v", want, spec.FinalCritic))
		}
	})

	ginkgo.It("conversation", func() {
		spec := RuntimeContractForTaskFamily("conversation")
		gomega.Expect(spec.TaskFamily).To(gomega.Equal(autonomy.TaskFamilyConversation), fmt.Sprintf("unexpected conversation contract: %#v", spec))
		gomega.Expect(spec.ToolProfile).To(gomega.Equal(workspace.ToolSurfaceProfileConversationReadOnly), fmt.Sprintf("unexpected conversation contract: %#v", spec))

		gomega.Expect(spec.PermissionProfile != "read_only_workspace").To(gomega.BeFalse(), fmt.Sprintf("permission = %q, want read_only_workspace", spec.PermissionProfile))

		gomega.Expect(spec.ReadinessGate).To(gomega.BeEmpty(), fmt.Sprintf("expected conversation contract to be answer-oriented and efficient, got %#v", spec))
		gomega.Expect(runtimeLabelContains(spec.FinalCritic, "answers_actual_user_request")).To(gomega.BeTrue(), fmt.Sprintf("expected conversation contract to be answer-oriented and efficient, got %#v", spec))
		gomega.Expect(runtimeLabelContains(spec.FinalCritic, "well_formatted_informative_on_topic")).To(gomega.BeTrue(), fmt.Sprintf("expected conversation contract to be answer-oriented and efficient, got %#v", spec))
		gomega.Expect(runtimeLabelContains(spec.SearchPolicy, "answer_from_visible_context_when_sufficient")).To(gomega.BeTrue(), fmt.Sprintf("expected conversation contract to be answer-oriented and efficient, got %#v", spec))
	})

	ginkgo.It("direct question alias", func() {
		spec := RuntimeContractForTaskFamily("direct_question")
		gomega.Expect(spec.TaskFamily).To(gomega.Equal(autonomy.TaskFamilyConversation), fmt.Sprintf("direct_question should normalize to conversation contract, got %#v", spec))
		gomega.Expect(spec.Contract).To(gomega.Equal(ContractDirectAnswer), fmt.Sprintf("direct_question should normalize to conversation contract, got %#v", spec))
		gomega.Expect(spec.PermissionProfile).To(gomega.Equal(PermissionReadOnlyWorkspace), fmt.Sprintf("direct_question should normalize to conversation contract, got %#v", spec))
	})

	ginkgo.It("debug logs", func() {
		spec := RuntimeContractForTaskFamily("debug_logs")
		gomega.Expect(spec.TaskFamily).To(gomega.Equal(autonomy.TaskFamilyConversation), fmt.Sprintf("debug_logs should be a compatibility alias for conversation, got %#v", spec))
		gomega.Expect(spec.Contract).To(gomega.Equal(ContractDirectAnswer), fmt.Sprintf("debug_logs should be a compatibility alias for conversation, got %#v", spec))
		gomega.Expect(spec.ToolProfile).To(gomega.Equal(workspace.ToolSurfaceProfileConversationReadOnly), fmt.Sprintf("debug_logs should be a compatibility alias for conversation, got %#v", spec))

		gomega.Expect(len(spec.ReadinessGate) != 0).To(gomega.BeFalse(), fmt.Sprintf("debug_logs alias should not retain separate operational gates, got %#v", spec))
	})

	ginkgo.It("implementation", func() {
		spec := RuntimeContractForTaskFamily("implementation")
		gomega.Expect(spec.ToolProfile).To(gomega.Equal(workspace.ToolSurfaceProfileGuardedWrite), fmt.Sprintf("unexpected implementation contract: %#v", spec))
		gomega.Expect(spec.PermissionProfile).To(gomega.Equal(PermissionGuardedWorkspaceWrite), fmt.Sprintf("unexpected implementation contract: %#v", spec))

		gomega.Expect(spec.ReadinessGate).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected implementation contract to keep readiness/final critic, got %#v", spec))
		gomega.Expect(spec.FinalCritic).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected implementation contract to keep readiness/final critic, got %#v", spec))

		gomega.Expect(runtimeLabelContains(spec.RequiredEvidence, string(RequiredEvidenceGitHistoryRationale))).To(gomega.BeTrue(), fmt.Sprintf("implementation must require git history rationale before writes, got %#v", spec.RequiredEvidence))

		gomega.Expect(slices.Contains(spec.RequiredTools, workspace.RequiredToolGitHistoryLookup)).To(gomega.BeTrue(), fmt.Sprintf("implementation must require history lookup before writes, got %#v", spec.RequiredTools))

		for _, want := range []string{"consume_target_packet_first", "trust_preloaded_current_state_anchors", "write_once_edit_location_is_supported"} {
			gomega.Expect(runtimeLabelContains(spec.SearchPolicy, want)).To(gomega.BeTrue(), fmt.Sprintf("expected implementation search policy to include %q, got %#v", want, spec.SearchPolicy))
		}

		gomega.Expect(runtimeLabelContains(spec.SearchPolicy, "history_required_before_writes")).To(gomega.BeTrue(), fmt.Sprintf("expected implementation search policy to require history before writes, got %#v", spec.SearchPolicy))
	})

	ginkgo.It("implementation repair", func() {
		spec := RuntimeContractForTaskFamily(string(autonomy.TaskFamilyImplementationRepair))
		gomega.Expect(spec.TaskFamily).To(gomega.Equal(autonomy.TaskFamilyImplementationRepair), fmt.Sprintf("unexpected implementation repair contract: %#v", spec))
		gomega.Expect(spec.Contract).To(gomega.Equal(ContractRepairSummary), fmt.Sprintf("unexpected implementation repair contract: %#v", spec))

		gomega.Expect(spec.ToolProfile).To(gomega.Equal(workspace.ToolSurfaceProfileGuardedWrite), fmt.Sprintf("implementation repair should use guarded write tools and permissions, got %#v", spec))
		gomega.Expect(spec.PermissionProfile).To(gomega.Equal(PermissionGuardedWorkspaceWrite), fmt.Sprintf("implementation repair should use guarded write tools and permissions, got %#v", spec))

		for _, want := range []string{"failed_verification_output", "repair_changed_paths_or_blocker", "verification_status"} {
			gomega.Expect(runtimeLabelContains(spec.RequiredEvidence, want)).To(gomega.BeTrue(), fmt.Sprintf("implementation repair missing evidence %q in %#v", want, spec.RequiredEvidence))
		}

		for _, want := range []string{"use_failed_output_first", "prefer_smallest_followup_edit", "avoid_scope_widening"} {
			gomega.Expect(runtimeLabelContains(spec.SearchPolicy, want)).To(gomega.BeTrue(), fmt.Sprintf("implementation repair missing search policy %q in %#v", want, spec.SearchPolicy))
		}
	})

	ginkgo.It("feature planner planner", func() {
		spec := RuntimeContractForTaskFamily(string(autonomy.TaskFamilyFeaturePlannerPlanner))
		gomega.Expect(spec.TaskFamily).To(gomega.Equal(autonomy.TaskFamilyFeaturePlannerPlanner), fmt.Sprintf("feature planner planner must be first-class, got %#v", spec))
		gomega.Expect(spec.Contract).To(gomega.Equal(ContractFeaturePlan), fmt.Sprintf("feature planner planner must be first-class, got %#v", spec))

		gomega.Expect(spec.PermissionProfile).To(gomega.Equal(PermissionConversationOnly), fmt.Sprintf("feature planner planner should be JSON-only/no-tool model turn, got %#v", spec))
		gomega.Expect(spec.ToolProfile).To(gomega.Equal(workspace.ToolSurfaceProfileConversationReadOnly), fmt.Sprintf("feature planner planner should be JSON-only/no-tool model turn, got %#v", spec))

		gomega.Expect(runtimeLabelContains(spec.RequiredEvidence, string(RequiredEvidenceFeaturePlanOrPlannerQuestion))).To(gomega.BeTrue(), fmt.Sprintf("feature planner planner missing feature-plan evidence requirement, got %#v", spec.RequiredEvidence))
	})

	ginkgo.It("feature planner implementor", func() {
		spec := RuntimeContractForTaskFamily(string(autonomy.TaskFamilyFeaturePlannerImplementor))
		gomega.Expect(spec.TaskFamily).To(gomega.Equal(autonomy.TaskFamilyFeaturePlannerImplementor), fmt.Sprintf("feature planner implementor must be first-class, got %#v", spec))
		gomega.Expect(spec.Contract).To(gomega.Equal(ContractImplementationResult), fmt.Sprintf("feature planner implementor must be first-class, got %#v", spec))

		gomega.Expect(spec.ToolProfile != workspace.ToolSurfaceProfilePlanSubmissionPlanner).To(gomega.BeFalse(), fmt.Sprintf("feature planner implementor should route through plan-execute submission, got %#v", spec))

		gomega.Expect(runtimeLabelContains(spec.RequiredEvidence, string(RequiredEvidencePlanExecuteCompletion))).To(gomega.BeTrue(), fmt.Sprintf("feature planner implementor missing plan-execute completion evidence, got %#v", spec.RequiredEvidence))
	})

	ginkgo.It("delegated isolated implementation", func() {
		spec := RuntimeContractForTaskFamily(string(autonomy.TaskFamilyDelegatedIsolatedImpl))
		gomega.Expect(spec.TaskFamily).To(gomega.Equal(autonomy.TaskFamilyDelegatedIsolatedImpl), fmt.Sprintf("delegated isolated implementation must stay write-capable, got %#v", spec))
		gomega.Expect(spec.Contract).To(gomega.Equal(ContractPatchSummary), fmt.Sprintf("delegated isolated implementation must stay write-capable, got %#v", spec))
		gomega.Expect(spec.PermissionProfile).To(gomega.Equal(PermissionIsolatedWorkbranchWrite), fmt.Sprintf("delegated isolated implementation must stay write-capable, got %#v", spec))
		gomega.Expect(spec.ToolProfile).To(gomega.Equal(workspace.ToolSurfaceProfileIsolatedWrite), fmt.Sprintf("delegated isolated implementation must stay write-capable, got %#v", spec))
	})

	ginkgo.It("delegated read-only", func() {
		spec := RuntimeContractForTaskFamily(string(autonomy.TaskFamilyDelegatedReadOnly))
		gomega.Expect(spec.TaskFamily).To(gomega.Equal(autonomy.TaskFamilyDelegatedReadOnly), fmt.Sprintf("delegated read-only must use delegated read-only profile, got %#v", spec))
		gomega.Expect(spec.Contract).To(gomega.Equal(ContractGroundedEvidence), fmt.Sprintf("delegated read-only must use delegated read-only profile, got %#v", spec))
		gomega.Expect(spec.PermissionProfile).To(gomega.Equal(PermissionReadOnlyWorkspace), fmt.Sprintf("delegated read-only must use delegated read-only profile, got %#v", spec))
		gomega.Expect(spec.ToolProfile).To(gomega.Equal(workspace.ToolSurfaceProfileDelegatedReadOnly), fmt.Sprintf("delegated read-only must use delegated read-only profile, got %#v", spec))
	})

	ginkgo.It("unknown family", func() {
		spec := RuntimeContractForTaskFamily("unknown_family")
		gomega.Expect(spec.TaskFamily).To(gomega.Equal(autonomy.TaskFamilyConversation), fmt.Sprintf("unexpected unknown-family fallback: %#v", spec))
		gomega.Expect(spec.Contract).To(gomega.Equal(ContractDirectAnswer), fmt.Sprintf("unexpected unknown-family fallback: %#v", spec))

		gomega.Expect(spec.PermissionProfile != "read_only_workspace").To(gomega.BeFalse(), fmt.Sprintf("permission = %q, want read_only_workspace", spec.PermissionProfile))
	})

	ginkgo.It("text-mutation families opt out of verification gate", func() {
		// These families produce artifacts the generic verification gate
		// can't usefully verify; they should advertise that via the
		// VerificationContract field so the runner skips the
		// post-mutation gate cleanly.
		for _, family := range []string{"docs_edit", "prompt_edit_security"} {
			spec := RuntimeContractForTaskFamily(family)
			gomega.Expect(spec.VerificationContract != VerificationNotRequiredTextMutation).To(gomega.BeFalse(), fmt.Sprintf("%s VerificationContract = %q, want not_required_for_text_mutations", family, spec.VerificationContract))
		}

		// Code-producing families keep the verification requirement.
		for _, family := range []string{"implementation", "test_authoring", "config_edit", "script_edit", "gdscript_edit", "failure_injection"} {
			spec := RuntimeContractForTaskFamily(family)
			gomega.Expect(spec.VerificationContract != VerificationTestsPassedFailedSkipped).To(gomega.BeFalse(), fmt.Sprintf("%s VerificationContract = %q, want tests_passed_or_failed_or_skipped_with_blocker", family, spec.VerificationContract))
		}
	})
})

var _ = ginkgo.Describe("workspace tool-surface override profiles", func() {
	tests := []struct {
		name   string
		family string
		want   workspace.ToolSurfaceProfile
		wantOK bool
	}{
		{name: "blank", family: "", wantOK: false},
		{name: "conversation", family: string(autonomy.TaskFamilyConversation), want: workspace.ToolSurfaceProfileConversationReadOnly, wantOK: true},
		{name: "verification only", family: "verification_only", want: workspace.ToolSurfaceProfileVerificationOnly, wantOK: true},
		{name: "locate", family: "locate", want: workspace.ToolSurfaceProfileLocateOnly, wantOK: true},
		{name: "planner", family: string(autonomy.TaskFamilyPlanThenExecutePlanner), want: workspace.ToolSurfaceProfilePlanSubmissionPlanner, wantOK: true},
		{name: "review repair", family: string(autonomy.TaskFamilyPlanThenExecuteReview), want: workspace.ToolSurfaceProfilePlanSubmissionReview, wantOK: true},
		{name: "implementation keeps default workspace surface", family: string(autonomy.TaskFamilyImplementation), wantOK: false},
		{name: "delegated isolated write keeps default workspace surface", family: string(autonomy.TaskFamilyDelegatedIsolatedImpl), wantOK: false},
		{name: "delegated read-only keeps default workspace surface", family: string(autonomy.TaskFamilyDelegatedReadOnly), wantOK: false},
		{name: "artifact distiller stays tool-free", family: ArtifactDistillerTaskFamily, wantOK: false},
	}

	for _, tt := range tests {
		tt := tt
		ginkgo.It(tt.name, func() {
			got, ok := WorkspaceToolSurfaceOverrideProfileForTaskFamily(tt.family)
			gomega.Expect(ok).To(gomega.Equal(tt.wantOK), fmt.Sprintf("override profile = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.wantOK))
			gomega.Expect(got).To(gomega.Equal(tt.want), fmt.Sprintf("override profile = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.wantOK))
		})
	}
})

func runtimeLabelContains[T ~string](values []T, want string) bool {
	for _, value := range values {
		if string(value) == want {
			return true
		}
	}

	return false
}

var _ = ginkgo.It("runtime brief for task family filters allowed tools by profile", func() {
	available := []string{"task", "pwd", "list_files", "search_files", "search_content", "read_file", "file_stats", "code_navigate", "read_only_capability", "harness_audit", "context_status", "context_query", "context_zoom", "context_pack", "context_impact", "context_trace_pack", "discovery_query", "structural_search", "go_workspace_symbol", "request_system_search_access", "shell", "git_status", "git_diff", "history_range", "history_symbol", "blame_range", "edit_file", "write_file"}

	artifact := RuntimeBriefForTaskFamily(ArtifactFinalAnswerTaskFamily, "main", available)
	gomega.Expect(artifact.Contract).To(gomega.Equal("artifact_final_answer"), fmt.Sprintf("unexpected artifact brief: %#v", artifact))
	gomega.Expect(artifact.PermissionProfile).To(gomega.Equal(string(PermissionConversationOnly)), fmt.Sprintf("unexpected artifact brief: %#v", artifact))

	gomega.Expect(artifact.AllowedTools).To(gomega.BeEmpty(), fmt.Sprintf("artifact final-answer brief must be tool-free, got tools=%#v manifest=%#v required=%#v", artifact.AllowedTools, artifact.ToolManifest, artifact.RequiredTools))
	gomega.Expect(artifact.ToolManifest).To(gomega.BeEmpty(), fmt.Sprintf("artifact final-answer brief must be tool-free, got tools=%#v manifest=%#v required=%#v", artifact.AllowedTools, artifact.ToolManifest, artifact.RequiredTools))
	gomega.Expect(artifact.RequiredTools).To(gomega.BeEmpty(), fmt.Sprintf("artifact final-answer brief must be tool-free, got tools=%#v manifest=%#v required=%#v", artifact.AllowedTools, artifact.ToolManifest, artifact.RequiredTools))

	distiller := RuntimeBriefForTaskFamily(ArtifactDistillerTaskFamily, "main", available)
	gomega.Expect(distiller.Contract).To(gomega.Equal("edit_intent_distillation"), fmt.Sprintf("unexpected artifact distiller brief: %#v", distiller))
	gomega.Expect(distiller.PermissionProfile).To(gomega.Equal(string(PermissionConversationOnly)), fmt.Sprintf("unexpected artifact distiller brief: %#v", distiller))

	gomega.Expect(distiller.AllowedTools).To(gomega.BeEmpty(), fmt.Sprintf("artifact distiller brief must not advertise tools, got tools=%#v manifest=%#v required=%#v", distiller.AllowedTools, distiller.ToolManifest, distiller.RequiredTools))
	gomega.Expect(distiller.ToolManifest).To(gomega.BeEmpty(), fmt.Sprintf("artifact distiller brief must not advertise tools, got tools=%#v manifest=%#v required=%#v", distiller.AllowedTools, distiller.ToolManifest, distiller.RequiredTools))
	gomega.Expect(distiller.RequiredTools).To(gomega.BeEmpty(), fmt.Sprintf("artifact distiller brief must not advertise tools, got tools=%#v manifest=%#v required=%#v", distiller.AllowedTools, distiller.ToolManifest, distiller.RequiredTools))

	for _, forbidden := range []string{"submit_plan", "edit_file", "write_file", "shell", "task"} {
		gomega.Expect(slices.Contains(distiller.AllowedTools, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("artifact distiller allowed forbidden tool %q in %#v", forbidden, distiller.AllowedTools))
	}

	brief := RuntimeBriefForTaskFamily("conversation", "main", available)
	gomega.Expect(brief.PermissionProfile != "read_only_workspace").To(gomega.BeFalse(), fmt.Sprintf("permission = %q, want read_only_workspace", brief.PermissionProfile))

	gomega.Expect(brief.Budget.MaxToolSteps).To(gomega.Equal(0), fmt.Sprintf("conversation should carry a tight read-only budget, got %#v", brief.Budget))
	gomega.Expect(brief.Budget.MaxModelCalls).To(gomega.Equal(0), fmt.Sprintf("conversation should carry a tight read-only budget, got %#v", brief.Budget))
	gomega.Expect(brief.Budget.MaxOperationalModelCalls).To(gomega.Equal(0), fmt.Sprintf("conversation should carry a tight read-only budget, got %#v", brief.Budget))

	for _, expected := range []string{"task", "read_file", "search_files", "search_content", "file_stats", "code_navigate", "read_only_capability", "harness_audit", "context_status", "context_query", "context_zoom", "structural_search", "history_range", "history_symbol", "blame_range", "request_system_search_access"} {
		gomega.Expect(slices.Contains(brief.AllowedTools, expected)).To(gomega.BeTrue(), fmt.Sprintf("conversation missing read-only tool %q in %#v", expected, brief.AllowedTools))
	}

	for _, forbidden := range []string{"edit_file", "write_file", "shell", "context_pack", "context_impact", "context_trace_pack", "discovery_query"} {
		gomega.Expect(slices.Contains(brief.AllowedTools, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("conversation should not allow write tool %q in %#v", forbidden, brief.AllowedTools))
	}

	gomega.Expect(slices.Contains(brief.SearchPolicy, "prefer context_pack for initial workspace-localization before ad-hoc search/read")).To(gomega.BeFalse(), fmt.Sprintf("conversation search policy should not advertise context_pack when it is outside the ordinary read-only surface: %#v", brief.SearchPolicy))

	verify := RuntimeBriefForTaskFamily("verification_only", "main", available)
	gomega.Expect(verify.AllowedTools).NotTo(gomega.ContainElement("pwd"), fmt.Sprintf("verification_only allowed unexpected tools: %#v", verify.AllowedTools))
	gomega.Expect(verify.AllowedTools).NotTo(gomega.ContainElement("edit_file"), fmt.Sprintf("verification_only allowed unexpected tools: %#v", verify.AllowedTools))
	gomega.Expect(verify.AllowedTools).NotTo(gomega.ContainElement("read_only_capability"), fmt.Sprintf("verification_only allowed unexpected tools: %#v", verify.AllowedTools))

	for _, expected := range []string{"shell", "read_file", "search_files", "search_content"} {
		gomega.Expect(slices.Contains(verify.AllowedTools, expected)).To(gomega.BeTrue(), fmt.Sprintf("verification_only missing %q in %#v", expected, verify.AllowedTools))
	}

	impl := RuntimeBriefForTaskFamily("implementation", "main", available)
	for _, expected := range []string{"history_range", "history_symbol", "blame_range", "edit_file", "write_file"} {
		gomega.Expect(slices.Contains(impl.AllowedTools, expected)).To(gomega.BeTrue(), fmt.Sprintf("implementation missing %q in %#v", expected, impl.AllowedTools))
	}

	archAvailable := []string{"task", "list_files", "search_files", "search_content", "read_file", "file_stats", "code_navigate", "read_only_capability", "harness_audit", "context_index", "context_status", "context_query", "context_zoom", "context_pack", "context_trace_pack", "discovery_query", "structural_search", "dependency_graph", "history_range", "history_symbol", "blame_range", "ctags_symbols", "semgrep_scan", "joern_scan", "go_overview", "go_workspace_symbol", "go_read_symbol", "go_file_outline", "shell"}
	arch := RuntimeBriefForTaskFamily("architecture", "main", archAvailable)

	for _, unexpected := range []string{"go_overview", "shell", "context_trace_pack", "context_index", "dependency_graph", "history_range", "history_symbol", "blame_range", "ctags_symbols", "semgrep_scan", "joern_scan", "list_files", "go_workspace_symbol", "go_read_symbol", "go_file_outline"} {
		gomega.Expect(slices.Contains(arch.AllowedTools, unexpected)).To(gomega.BeFalse(), fmt.Sprintf("architecture should advertise the lean read-only discovery surface, got unexpected %q in %#v", unexpected, arch.AllowedTools))
	}

	for _, expected := range []string{"task", "search_content", "read_file", "file_stats", "code_navigate", "read_only_capability", "context_status", "context_query", "context_zoom", "context_pack", "discovery_query", "structural_search"} {
		gomega.Expect(slices.Contains(arch.AllowedTools, expected)).To(gomega.BeTrue(), fmt.Sprintf("architecture missing %q in %#v", expected, arch.AllowedTools))
	}

	gomega.Expect(slices.Contains(arch.SearchPolicy, "delegate_broad_discovery_when_needed")).To(gomega.BeTrue(), fmt.Sprintf("architecture should delegate broad read-only discovery before local probing, got %#v", arch.SearchPolicy))

	gomega.Expect(arch.SearchPolicy).To(gomega.ContainElement("prefer_discovery_query_for_architecture_boundary"), fmt.Sprintf("architecture search policy should steer generic discovery tools, got %#v", arch.SearchPolicy))
	gomega.Expect(arch.SearchPolicy).To(gomega.ContainElement("use_discovery_query_for_boundary_import_or_evolution_questions"), fmt.Sprintf("architecture search policy should steer generic discovery tools, got %#v", arch.SearchPolicy))
	gomega.Expect(arch.SearchPolicy).To(gomega.ContainElement("name_heavy_graph_or_history_need_if_unresolved"), fmt.Sprintf("architecture search policy should steer generic discovery tools, got %#v", arch.SearchPolicy))

	for _, expected := range []string{"answer_when_boundary_evidence_is_sufficient", "if_not_answering_name_one_exact_missing_claim_and_probe"} {
		gomega.Expect(slices.Contains(arch.SearchPolicy, expected)).To(gomega.BeTrue(), fmt.Sprintf("architecture missing %q in %#v", expected, arch.SearchPolicy))
	}

	gomega.Expect(arch.ReadinessGate).NotTo(gomega.BeEmpty(), fmt.Sprintf("architecture should keep closure gates and final critic, got %#v", arch))
	gomega.Expect(arch.FinalCritic).NotTo(gomega.BeEmpty(), fmt.Sprintf("architecture should keep closure gates and final critic, got %#v", arch))

	security := RuntimeBriefForTaskFamily("security_review", "main", archAvailable)
	gomega.Expect(slices.Contains(security.SearchPolicy, "delegate_broad_discovery_when_needed")).To(gomega.BeFalse(), fmt.Sprintf("security review should not bias toward delegated broad discovery, got %#v", security.SearchPolicy))

	gomega.Expect(security.SearchPolicy).To(gomega.ContainElement("prefer_discovery_query_security_review_before_raw_scans"), fmt.Sprintf("security review should keep heavy scanners out of the default surface, got %#v", security.SearchPolicy))
	gomega.Expect(security.SearchPolicy).To(gomega.ContainElement("name_heavy_scanner_need_instead_of_default_semgrep_or_joern"), fmt.Sprintf("security review should keep heavy scanners out of the default surface, got %#v", security.SearchPolicy))

	for _, expected := range []string{"answer_when_bounded_finding_or_no_finding_is_supported", "if_continuing_name_one_exact_missing_claim_and_probe"} {
		gomega.Expect(slices.Contains(security.SearchPolicy, expected)).To(gomega.BeTrue(), fmt.Sprintf("security review missing %q in %#v", expected, security.SearchPolicy))
	}

	gomega.Expect(security.ReadinessGate).NotTo(gomega.BeEmpty(), fmt.Sprintf("security review should keep closure gates and final critic, got %#v", security))
	gomega.Expect(security.FinalCritic).NotTo(gomega.BeEmpty(), fmt.Sprintf("security review should keep closure gates and final critic, got %#v", security))
})
