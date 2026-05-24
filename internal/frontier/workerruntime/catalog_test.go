package workerruntime

import (
	"slices"
	"testing"
)

func TestRuntimeContractForTaskFamilyUsesSharedProfiles(t *testing.T) {
	t.Run("artifact final answer", func(t *testing.T) {
		spec := RuntimeContractForTaskFamily(ArtifactFinalAnswerTaskFamily)
		if spec.TaskFamily != ArtifactFinalAnswerTaskFamily || spec.Contract != "artifact_final_answer" {
			t.Fatalf("unexpected artifact final-answer contract: %#v", spec)
		}
		if spec.PermissionProfile != "conversation_only" {
			t.Fatalf("permission = %q, want conversation_only", spec.PermissionProfile)
		}
		if !slices.Contains(spec.FinalCritic, "no_tool_calls") {
			t.Fatalf("artifact contract should require no tool calls, got %#v", spec.FinalCritic)
		}
	})

	t.Run("conversation", func(t *testing.T) {
		spec := RuntimeContractForTaskFamily("conversation")
		if spec.TaskFamily != "conversation" || spec.ToolProfile != CapabilityConversationReadOnly {
			t.Fatalf("unexpected conversation contract: %#v", spec)
		}
		if spec.PermissionProfile != "read_only_workspace" {
			t.Fatalf("permission = %q, want read_only_workspace", spec.PermissionProfile)
		}
		if len(spec.ReadinessGate) != 0 ||
			!slices.Contains(spec.FinalCritic, "answers_actual_user_request") ||
			!slices.Contains(spec.FinalCritic, "well_formatted_informative_on_topic") ||
			!slices.Contains(spec.SearchPolicy, "answer_from_visible_context_when_sufficient") {
			t.Fatalf("expected conversation contract to be answer-oriented and efficient, got %#v", spec)
		}
	})

	t.Run("direct question alias", func(t *testing.T) {
		spec := RuntimeContractForTaskFamily("direct_question")
		if spec.TaskFamily != "conversation" || spec.Contract != "direct_answer" || spec.PermissionProfile != "read_only_workspace" {
			t.Fatalf("direct_question should normalize to conversation contract, got %#v", spec)
		}
	})

	t.Run("debug logs", func(t *testing.T) {
		spec := RuntimeContractForTaskFamily("debug_logs")
		if spec.ToolProfile != CapabilityDebugLogInvestigator {
			t.Fatalf("unexpected debug_logs tool profile: %#v", spec)
		}
		if len(spec.ReadinessGate) == 0 || len(spec.FinalCritic) == 0 {
			t.Fatalf("expected debug_logs to keep stricter operational gates, got %#v", spec)
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
		for _, want := range []string{"consume_target_packet_first", "trust_preloaded_current_state_anchors", "write_once_edit_location_is_supported"} {
			if !slices.Contains(spec.SearchPolicy, want) {
				t.Fatalf("expected implementation search policy to include %q, got %#v", want, spec.SearchPolicy)
			}
		}
	})

	t.Run("unknown family", func(t *testing.T) {
		spec := RuntimeContractForTaskFamily("unknown_family")
		if spec.TaskFamily != "conversation" || spec.Contract != "direct_answer" {
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
			if spec.VerificationContract != "not_required_for_text_mutations" {
				t.Errorf("%s VerificationContract = %q, want not_required_for_text_mutations", family, spec.VerificationContract)
			}
		}

		// Code-producing families keep the verification requirement.
		for _, family := range []string{"implementation", "test_authoring", "config_edit", "script_edit", "gdscript_edit", "failure_injection"} {
			spec := RuntimeContractForTaskFamily(family)
			if spec.VerificationContract != "tests_passed_or_failed_or_skipped_with_blocker" {
				t.Errorf("%s VerificationContract = %q, want tests_passed_or_failed_or_skipped_with_blocker", family, spec.VerificationContract)
			}
		}
	})
}

func TestRuntimeBriefForTaskFamilyFiltersAllowedToolsByProfile(t *testing.T) {
	available := []string{"task", "pwd", "list_files", "search_files", "search_content", "read_file", "context_query", "context_trace_pack", "go_workspace_symbol", "shell", "edit_file", "write_file"}

	artifact := RuntimeBriefForTaskFamily(ArtifactFinalAnswerTaskFamily, "main", available)
	if artifact.Contract != "artifact_final_answer" || artifact.PermissionProfile != "conversation_only" {
		t.Fatalf("unexpected artifact brief: %#v", artifact)
	}
	if len(artifact.AllowedTools) != 0 || len(artifact.ToolManifest) != 0 || len(artifact.RequiredTools) != 0 {
		t.Fatalf("artifact final-answer brief must be tool-free, got tools=%#v manifest=%#v required=%#v", artifact.AllowedTools, artifact.ToolManifest, artifact.RequiredTools)
	}

	brief := RuntimeBriefForTaskFamily("conversation", "main", available)
	if brief.PermissionProfile != "read_only_workspace" {
		t.Fatalf("permission = %q, want read_only_workspace", brief.PermissionProfile)
	}
	if brief.Budget.MaxToolSteps != 4 || brief.Budget.MaxModelCalls != 6 {
		t.Fatalf("conversation should carry a tight read-only budget, got %#v", brief.Budget)
	}
	for _, expected := range []string{"read_file", "search_files", "search_content", "context_query"} {
		if !slices.Contains(brief.AllowedTools, expected) {
			t.Fatalf("conversation missing read-only tool %q in %#v", expected, brief.AllowedTools)
		}
	}
	for _, forbidden := range []string{"edit_file", "write_file", "shell"} {
		if slices.Contains(brief.AllowedTools, forbidden) {
			t.Fatalf("conversation should not allow write tool %q in %#v", forbidden, brief.AllowedTools)
		}
	}

	verify := RuntimeBriefForTaskFamily("verification_only", "main", available)
	if slices.Contains(verify.AllowedTools, "pwd") || slices.Contains(verify.AllowedTools, "edit_file") {
		t.Fatalf("verification_only allowed unexpected tools: %#v", verify.AllowedTools)
	}
	for _, expected := range []string{"shell", "read_file", "search_files", "search_content"} {
		if !slices.Contains(verify.AllowedTools, expected) {
			t.Fatalf("verification_only missing %q in %#v", expected, verify.AllowedTools)
		}
	}

	archAvailable := []string{"task", "list_files", "search_files", "search_content", "read_file", "file_stats", "harness_audit", "context_index", "context_status", "context_query", "context_zoom", "context_pack", "context_trace_pack", "discovery_query", "structural_search", "dependency_graph", "history_range", "history_symbol", "blame_range", "ctags_symbols", "semgrep_scan", "joern_scan", "go_overview", "go_workspace_symbol", "go_read_symbol", "go_file_outline", "shell"}
	arch := RuntimeBriefForTaskFamily("architecture", "main", archAvailable)
	for _, unexpected := range []string{"go_overview", "shell", "context_trace_pack", "context_index", "dependency_graph", "history_range", "history_symbol", "blame_range", "ctags_symbols", "semgrep_scan", "joern_scan", "list_files", "go_workspace_symbol", "go_read_symbol", "go_file_outline"} {
		if slices.Contains(arch.AllowedTools, unexpected) {
			t.Fatalf("architecture should advertise the lean read-only discovery surface, got unexpected %q in %#v", unexpected, arch.AllowedTools)
		}
	}
	for _, expected := range []string{"task", "search_content", "read_file", "file_stats", "context_status", "context_query", "context_zoom", "context_pack", "discovery_query", "structural_search"} {
		if !slices.Contains(arch.AllowedTools, expected) {
			t.Fatalf("architecture missing %q in %#v", expected, arch.AllowedTools)
		}
	}
	if slices.Contains(arch.SearchPolicy, "delegate_broad_discovery_when_needed") {
		t.Fatalf("architecture should not bias toward delegated broad discovery, got %#v", arch.SearchPolicy)
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
	readOnly := ManifestToolsForCapabilityProfile(CapabilityConversationReadOnly)
	if !slices.Contains(readOnly, "task") {
		t.Fatalf("conversation read-only manifest should include task, got %#v", readOnly)
	}
	if slices.Contains(readOnly, "edit_file") {
		t.Fatalf("conversation read-only manifest should not include edit_file, got %#v", readOnly)
	}

	guardedWrite := ManifestToolsForCapabilityProfile(CapabilityGuardedWrite)
	for _, expected := range []string{"task", "task_ledger", "edit_file", "apply_patch"} {
		if !slices.Contains(guardedWrite, expected) {
			t.Fatalf("guarded write manifest missing %q in %#v", expected, guardedWrite)
		}
	}
}
