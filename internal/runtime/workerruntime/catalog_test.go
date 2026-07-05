package workerruntime

import (
	"slices"
	"testing"

	"idiom/internal/contracts/autonomy"
)

func TestRuntimeContractForTaskFamilyUsesSharedProfiles(t *testing.T) {
	t.Run("artifact final answer", func(t *testing.T) {
		spec := RuntimeContractForTaskFamily(ArtifactFinalAnswerTaskFamily)
		if spec.TaskFamily != autonomy.TaskFamilyArtifactFinalAnswer || spec.Contract != ContractArtifactFinalAnswer {
			t.Fatalf("unexpected artifact final-answer contract: %#v", spec)
		}

		if spec.PermissionProfile != "conversation_only" {
			t.Fatalf("permission = %q, want conversation_only", spec.PermissionProfile)
		}

		if !runtimeLabelContains(spec.FinalCritic, "no_tool_calls") {
			t.Fatalf("artifact contract should require no tool calls, got %#v", spec.FinalCritic)
		}
	})

	t.Run("artifact distiller", func(t *testing.T) {
		spec := RuntimeContractForTaskFamily(ArtifactDistillerTaskFamily)
		if spec.TaskFamily != autonomy.TaskFamilyArtifactDistiller || spec.Contract != ContractEditIntentDistill {
			t.Fatalf("unexpected artifact distiller contract: %#v", spec)
		}

		if spec.PermissionProfile != "conversation_only" || spec.ToolProfile != CapabilityArtifactDistiller {
			t.Fatalf("artifact distiller must be tool-free/no-write, got %#v", spec)
		}

		for _, want := range []string{"raw_output_parser_failure", "active_mutation_contract", "required_edit_scope"} {
			if !runtimeLabelContains(spec.RequiredEvidence, want) {
				t.Fatalf("artifact distiller missing evidence %q in %#v", want, spec.RequiredEvidence)
			}
		}

		for _, want := range []string{"outputs_only_edit_intent_or_typed_blocker", "no_tool_calls", "no_write_authority", "no_submit_plan"} {
			if !runtimeLabelContains(spec.FinalCritic, want) {
				t.Fatalf("artifact distiller missing critic %q in %#v", want, spec.FinalCritic)
			}
		}
	})

	t.Run("conversation", func(t *testing.T) {
		spec := RuntimeContractForTaskFamily("conversation")
		if spec.TaskFamily != autonomy.TaskFamilyConversation || spec.ToolProfile != CapabilityConversationReadOnly {
			t.Fatalf("unexpected conversation contract: %#v", spec)
		}

		if spec.PermissionProfile != "read_only_workspace" {
			t.Fatalf("permission = %q, want read_only_workspace", spec.PermissionProfile)
		}

		if len(spec.ReadinessGate) != 0 ||
			!runtimeLabelContains(spec.FinalCritic, "answers_actual_user_request") ||
			!runtimeLabelContains(spec.FinalCritic, "well_formatted_informative_on_topic") ||
			!runtimeLabelContains(spec.SearchPolicy, "answer_from_visible_context_when_sufficient") {

			t.Fatalf("expected conversation contract to be answer-oriented and efficient, got %#v", spec)
		}
	})

	t.Run("direct question alias", func(t *testing.T) {
		spec := RuntimeContractForTaskFamily("direct_question")
		if spec.TaskFamily != autonomy.TaskFamilyConversation || spec.Contract != ContractDirectAnswer || spec.PermissionProfile != PermissionReadOnlyWorkspace {
			t.Fatalf("direct_question should normalize to conversation contract, got %#v", spec)
		}
	})

	t.Run("debug logs", func(t *testing.T) {
		spec := RuntimeContractForTaskFamily("debug_logs")
		if spec.TaskFamily != autonomy.TaskFamilyConversation || spec.Contract != ContractDirectAnswer || spec.ToolProfile != CapabilityConversationReadOnly {
			t.Fatalf("debug_logs should be a compatibility alias for conversation, got %#v", spec)
		}

		if len(spec.ReadinessGate) != 0 {
			t.Fatalf("debug_logs alias should not retain separate operational gates, got %#v", spec)
		}
	})

	t.Run("implementation", func(t *testing.T) {
		spec := RuntimeContractForTaskFamily("implementation")
		if spec.ToolProfile != CapabilityGuardedWrite || spec.PermissionProfile != "guarded_workspace_write" {
			t.Fatalf("unexpected implementation contract: %#v", spec)
		}

		if len(spec.ReadinessGate) == 0 || len(spec.FinalCritic) == 0 {
			t.Fatalf("expected implementation contract to keep readiness/final critic, got %#v", spec)
		}

		if !runtimeLabelContains(spec.RequiredEvidence, string(requiredEvidenceGitHistoryRationale)) {
			t.Fatalf("implementation must require git history rationale before writes, got %#v", spec.RequiredEvidence)
		}

		if !slices.Contains(spec.RequiredTools, requiredToolGitHistoryLookup) {
			t.Fatalf("implementation must require history lookup before writes, got %#v", spec.RequiredTools)
		}

		for _, want := range []string{"consume_target_packet_first", "trust_preloaded_current_state_anchors", "write_once_edit_location_is_supported"} {
			if !runtimeLabelContains(spec.SearchPolicy, want) {
				t.Fatalf("expected implementation search policy to include %q, got %#v", want, spec.SearchPolicy)
			}
		}

		if !runtimeLabelContains(spec.SearchPolicy, "history_required_before_writes") {
			t.Fatalf("expected implementation search policy to require history before writes, got %#v", spec.SearchPolicy)
		}
	})

	t.Run("feature planner planner", func(t *testing.T) {
		spec := RuntimeContractForTaskFamily(string(autonomy.TaskFamilyFeaturePlannerPlanner))
		if spec.TaskFamily != autonomy.TaskFamilyFeaturePlannerPlanner || spec.Contract != ContractFeaturePlan {
			t.Fatalf("feature planner planner must be first-class, got %#v", spec)
		}

		if spec.PermissionProfile != PermissionConversationOnly || spec.ToolProfile != CapabilityConversationReadOnly {
			t.Fatalf("feature planner planner should be JSON-only/no-tool model turn, got %#v", spec)
		}

		if !runtimeLabelContains(spec.RequiredEvidence, string(RequiredEvidenceFeaturePlanOrPlannerQuestion)) {
			t.Fatalf("feature planner planner missing feature-plan evidence requirement, got %#v", spec.RequiredEvidence)
		}
	})

	t.Run("feature planner implementor", func(t *testing.T) {
		spec := RuntimeContractForTaskFamily(string(autonomy.TaskFamilyFeaturePlannerImplementor))
		if spec.TaskFamily != autonomy.TaskFamilyFeaturePlannerImplementor || spec.Contract != ContractImplementationResult {
			t.Fatalf("feature planner implementor must be first-class, got %#v", spec)
		}

		if spec.ToolProfile != CapabilityPlanSubmissionPlanner {
			t.Fatalf("feature planner implementor should route through plan-execute submission, got %#v", spec)
		}

		if !runtimeLabelContains(spec.RequiredEvidence, string(RequiredEvidencePlanExecuteCompletion)) {
			t.Fatalf("feature planner implementor missing plan-execute completion evidence, got %#v", spec.RequiredEvidence)
		}
	})

	t.Run("unknown family", func(t *testing.T) {
		spec := RuntimeContractForTaskFamily("unknown_family")
		if spec.TaskFamily != autonomy.TaskFamilyConversation || spec.Contract != ContractDirectAnswer {
			t.Fatalf("unexpected unknown-family fallback: %#v", spec)
		}

		if spec.PermissionProfile != "read_only_workspace" {
			t.Fatalf("permission = %q, want read_only_workspace", spec.PermissionProfile)
		}
	})

	t.Run("text-mutation families opt out of verification gate", func(t *testing.T) {
		// These families produce artifacts the generic verification gate
		// can't usefully verify; they should advertise that via the
		// VerificationContract field so the runner skips the
		// post-mutation gate cleanly.
		for _, family := range []string{"docs_edit", "prompt_edit_security"} {
			spec := RuntimeContractForTaskFamily(family)
			if spec.VerificationContract != VerificationNotRequiredTextMutation {
				t.Errorf("%s VerificationContract = %q, want not_required_for_text_mutations", family, spec.VerificationContract)
			}
		}

		// Code-producing families keep the verification requirement.
		for _, family := range []string{"implementation", "test_authoring", "config_edit", "script_edit", "gdscript_edit", "failure_injection"} {
			spec := RuntimeContractForTaskFamily(family)
			if spec.VerificationContract != VerificationTestsPassedFailedSkipped {
				t.Errorf("%s VerificationContract = %q, want tests_passed_or_failed_or_skipped_with_blocker", family, spec.VerificationContract)
			}
		}
	})
}

func runtimeLabelContains[T ~string](values []T, want string) bool {
	for _, value := range values {
		if string(value) == want {
			return true
		}
	}

	return false
}

func TestRuntimeBriefForTaskFamilyFiltersAllowedToolsByProfile(t *testing.T) {
	available := []string{"task", "pwd", "list_files", "search_files", "search_content", "read_file", "file_stats", "code_navigate", "read_only_capability", "harness_audit", "context_status", "context_query", "context_zoom", "context_pack", "context_impact", "context_trace_pack", "discovery_query", "structural_search", "go_workspace_symbol", "request_system_search_access", "shell", "git_status", "git_diff", "history_range", "history_symbol", "blame_range", "edit_file", "write_file"}

	artifact := RuntimeBriefForTaskFamily(ArtifactFinalAnswerTaskFamily, "main", available)
	if artifact.Contract != "artifact_final_answer" || artifact.PermissionProfile != "conversation_only" {
		t.Fatalf("unexpected artifact brief: %#v", artifact)
	}

	if len(artifact.AllowedTools) != 0 || len(artifact.ToolManifest) != 0 || len(artifact.RequiredTools) != 0 {
		t.Fatalf("artifact final-answer brief must be tool-free, got tools=%#v manifest=%#v required=%#v", artifact.AllowedTools, artifact.ToolManifest, artifact.RequiredTools)
	}

	distiller := RuntimeBriefForTaskFamily(ArtifactDistillerTaskFamily, "main", available)
	if distiller.Contract != "edit_intent_distillation" || distiller.PermissionProfile != "conversation_only" {
		t.Fatalf("unexpected artifact distiller brief: %#v", distiller)
	}

	if len(distiller.AllowedTools) != 0 || len(distiller.ToolManifest) != 0 || len(distiller.RequiredTools) != 0 {
		t.Fatalf("artifact distiller brief must not advertise tools, got tools=%#v manifest=%#v required=%#v", distiller.AllowedTools, distiller.ToolManifest, distiller.RequiredTools)
	}

	for _, forbidden := range []string{"submit_plan", "edit_file", "write_file", "shell", "task"} {
		if slices.Contains(distiller.AllowedTools, forbidden) {
			t.Fatalf("artifact distiller allowed forbidden tool %q in %#v", forbidden, distiller.AllowedTools)
		}
	}

	brief := RuntimeBriefForTaskFamily("conversation", "main", available)
	if brief.PermissionProfile != "read_only_workspace" {
		t.Fatalf("permission = %q, want read_only_workspace", brief.PermissionProfile)
	}

	if brief.Budget.MaxToolSteps != 0 || brief.Budget.MaxModelCalls != 0 || brief.Budget.MaxOperationalModelCalls != 0 {
		t.Fatalf("conversation should carry a tight read-only budget, got %#v", brief.Budget)
	}

	for _, expected := range []string{"task", "read_file", "search_files", "search_content", "file_stats", "code_navigate", "read_only_capability", "harness_audit", "context_status", "context_query", "context_zoom", "structural_search", "history_range", "history_symbol", "blame_range", "request_system_search_access"} {
		if !slices.Contains(brief.AllowedTools, expected) {
			t.Fatalf("conversation missing read-only tool %q in %#v", expected, brief.AllowedTools)
		}
	}

	for _, forbidden := range []string{"edit_file", "write_file", "shell", "context_pack", "context_impact", "context_trace_pack", "discovery_query"} {
		if slices.Contains(brief.AllowedTools, forbidden) {
			t.Fatalf("conversation should not allow write tool %q in %#v", forbidden, brief.AllowedTools)
		}
	}

	if slices.Contains(brief.SearchPolicy, "prefer context_pack for initial workspace-localization before ad-hoc search/read") {
		t.Fatalf("conversation search policy should not advertise context_pack when it is outside the ordinary read-only surface: %#v", brief.SearchPolicy)
	}

	verify := RuntimeBriefForTaskFamily("verification_only", "main", available)
	if slices.Contains(verify.AllowedTools, "pwd") || slices.Contains(verify.AllowedTools, "edit_file") || slices.Contains(verify.AllowedTools, "read_only_capability") {
		t.Fatalf("verification_only allowed unexpected tools: %#v", verify.AllowedTools)
	}

	for _, expected := range []string{"shell", "read_file", "search_files", "search_content"} {
		if !slices.Contains(verify.AllowedTools, expected) {
			t.Fatalf("verification_only missing %q in %#v", expected, verify.AllowedTools)
		}
	}

	impl := RuntimeBriefForTaskFamily("implementation", "main", available)
	for _, expected := range []string{"history_range", "history_symbol", "blame_range", "edit_file", "write_file"} {
		if !slices.Contains(impl.AllowedTools, expected) {
			t.Fatalf("implementation missing %q in %#v", expected, impl.AllowedTools)
		}
	}

	archAvailable := []string{"task", "list_files", "search_files", "search_content", "read_file", "file_stats", "code_navigate", "read_only_capability", "harness_audit", "context_index", "context_status", "context_query", "context_zoom", "context_pack", "context_trace_pack", "discovery_query", "structural_search", "dependency_graph", "history_range", "history_symbol", "blame_range", "ctags_symbols", "semgrep_scan", "joern_scan", "go_overview", "go_workspace_symbol", "go_read_symbol", "go_file_outline", "shell"}
	arch := RuntimeBriefForTaskFamily("architecture", "main", archAvailable)

	for _, unexpected := range []string{"go_overview", "shell", "context_trace_pack", "context_index", "dependency_graph", "history_range", "history_symbol", "blame_range", "ctags_symbols", "semgrep_scan", "joern_scan", "list_files", "go_workspace_symbol", "go_read_symbol", "go_file_outline"} {
		if slices.Contains(arch.AllowedTools, unexpected) {
			t.Fatalf("architecture should advertise the lean read-only discovery surface, got unexpected %q in %#v", unexpected, arch.AllowedTools)
		}
	}

	for _, expected := range []string{"task", "search_content", "read_file", "file_stats", "code_navigate", "read_only_capability", "context_status", "context_query", "context_zoom", "context_pack", "discovery_query", "structural_search"} {
		if !slices.Contains(arch.AllowedTools, expected) {
			t.Fatalf("architecture missing %q in %#v", expected, arch.AllowedTools)
		}
	}

	if !slices.Contains(arch.SearchPolicy, "delegate_broad_discovery_when_needed") {
		t.Fatalf("architecture should delegate broad read-only discovery before local probing, got %#v", arch.SearchPolicy)
	}

	if !slices.Contains(arch.SearchPolicy, "prefer_discovery_query_for_architecture_boundary") || !slices.Contains(arch.SearchPolicy, "use_discovery_query_for_boundary_import_or_evolution_questions") || !slices.Contains(arch.SearchPolicy, "name_heavy_graph_or_history_need_if_unresolved") {
		t.Fatalf("architecture search policy should steer generic discovery tools, got %#v", arch.SearchPolicy)
	}

	for _, expected := range []string{"answer_when_boundary_evidence_is_sufficient", "if_not_answering_name_one_exact_missing_claim_and_probe"} {
		if !slices.Contains(arch.SearchPolicy, expected) {
			t.Fatalf("architecture missing %q in %#v", expected, arch.SearchPolicy)
		}
	}

	if len(arch.ReadinessGate) == 0 || len(arch.FinalCritic) == 0 {
		t.Fatalf("architecture should keep closure gates and final critic, got %#v", arch)
	}

	security := RuntimeBriefForTaskFamily("security_review", "main", archAvailable)
	if slices.Contains(security.SearchPolicy, "delegate_broad_discovery_when_needed") {
		t.Fatalf("security review should not bias toward delegated broad discovery, got %#v", security.SearchPolicy)
	}

	if !slices.Contains(security.SearchPolicy, "prefer_discovery_query_security_review_before_raw_scans") || !slices.Contains(security.SearchPolicy, "name_heavy_scanner_need_instead_of_default_semgrep_or_joern") {
		t.Fatalf("security review should keep heavy scanners out of the default surface, got %#v", security.SearchPolicy)
	}

	for _, expected := range []string{"answer_when_bounded_finding_or_no_finding_is_supported", "if_continuing_name_one_exact_missing_claim_and_probe"} {
		if !slices.Contains(security.SearchPolicy, expected) {
			t.Fatalf("security review missing %q in %#v", expected, security.SearchPolicy)
		}
	}

	if len(security.ReadinessGate) == 0 || len(security.FinalCritic) == 0 {
		t.Fatalf("security review should keep closure gates and final critic, got %#v", security)
	}
}

func TestManifestToolsForCapabilityProfileExposeExpectedSurfaces(t *testing.T) {
	distiller := ManifestToolsForCapabilityProfile(CapabilityArtifactDistiller)
	if len(distiller) != 0 {
		t.Fatalf("artifact distiller manifest must be empty, got %#v", distiller)
	}

	readOnly := ManifestToolsForCapabilityProfile(CapabilityConversationReadOnly)
	if !slices.Contains(readOnly, "task") {
		t.Fatalf("conversation read-only manifest should include task, got %#v", readOnly)
	}

	if !slices.Contains(readOnly, "read_only_capability") {
		t.Fatalf("conversation read-only manifest should include read_only_capability, got %#v", readOnly)
	}

	for _, expected := range []string{"harness_audit", "context_status", "context_query", "context_zoom", "structural_search", "history_range", "history_symbol", "blame_range"} {
		if !slices.Contains(readOnly, expected) {
			t.Fatalf("conversation read-only manifest should include diagnostic tool %q, got %#v", expected, readOnly)
		}
	}

	for _, forbidden := range []string{"edit_file", "context_pack", "context_impact", "context_trace_pack", "discovery_query"} {
		if slices.Contains(readOnly, forbidden) {
			t.Fatalf("conversation read-only manifest should not include %q, got %#v", forbidden, readOnly)
		}
	}

	verificationOnly := ManifestToolsForCapabilityProfile(CapabilityVerificationOnly)
	if slices.Contains(verificationOnly, "read_only_capability") {
		t.Fatalf("verification-only manifest should not include read_only_capability, got %#v", verificationOnly)
	}

	guardedWrite := ManifestToolsForCapabilityProfile(CapabilityGuardedWrite)
	for _, expected := range []string{"task", "task_ledger", "edit_file", "apply_patch"} {
		if !slices.Contains(guardedWrite, expected) {
			t.Fatalf("guarded write manifest missing %q in %#v", expected, guardedWrite)
		}
	}
}
