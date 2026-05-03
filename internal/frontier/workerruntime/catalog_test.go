package workerruntime

import "testing"

func TestRuntimeContractForTaskFamilyUsesSharedProfiles(t *testing.T) {
	t.Run("direct question", func(t *testing.T) {
		spec := RuntimeContractForTaskFamily("direct_question")
		if spec.TaskFamily != "direct_answer" || spec.ToolProfile != CapabilityConversationReadOnly {
			t.Fatalf("unexpected direct question contract: %#v", spec)
		}
		if spec.PermissionProfile != "conversation_only" {
			t.Fatalf("permission = %q, want conversation_only", spec.PermissionProfile)
		}
		if len(spec.ReadinessGate) != 0 || !containsString(spec.FinalCritic, "answers_actual_user_request") {
			t.Fatalf("expected direct question contract to avoid readiness gate and use final critic only, got %#v", spec)
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
	})

	t.Run("unknown family", func(t *testing.T) {
		spec := RuntimeContractForTaskFamily("unknown_family")
		if spec.TaskFamily != "direct_answer" || spec.Contract != "direct_answer" {
			t.Fatalf("unexpected unknown-family fallback: %#v", spec)
		}
		if spec.PermissionProfile != "conversation_only" {
			t.Fatalf("permission = %q, want conversation_only", spec.PermissionProfile)
		}
	})

	t.Run("text-mutation families opt out of verification gate", func(t *testing.T) {
		// These families produce artifacts the generic verification gate
		// can't usefully verify; they should advertise that via the
		// VerificationContract field so the runner skips the
		// post-mutation gate cleanly.
		for _, family := range []string{"docs_edit", "prompt_edit_security", "gdscript_edit"} {
			spec := RuntimeContractForTaskFamily(family)
			if spec.VerificationContract != "not_required_for_text_mutations" {
				t.Errorf("%s VerificationContract = %q, want not_required_for_text_mutations", family, spec.VerificationContract)
			}
		}

		// Code-producing families keep the verification requirement.
		for _, family := range []string{"implementation", "test_authoring", "config_edit", "script_edit", "failure_injection"} {
			spec := RuntimeContractForTaskFamily(family)
			if spec.VerificationContract != "tests_passed_or_failed_or_skipped_with_blocker" {
				t.Errorf("%s VerificationContract = %q, want tests_passed_or_failed_or_skipped_with_blocker", family, spec.VerificationContract)
			}
		}
	})
}

func TestRuntimeBriefForTaskFamilyFiltersAllowedToolsByProfile(t *testing.T) {
	available := []string{"task", "pwd", "list_files", "search_files", "search_content", "read_file", "go_workspace_symbol", "shell", "edit_file", "write_file"}

	brief := RuntimeBriefForTaskFamily("direct_question", "main", available)
	if brief.PermissionProfile != "conversation_only" {
		t.Fatalf("permission = %q, want conversation_only", brief.PermissionProfile)
	}
	if len(brief.AllowedTools) != 0 {
		t.Fatalf("direct_question should not advertise tools in conversation-only mode, got %#v", brief.AllowedTools)
	}
	if len(brief.ToolManifest) != 0 {
		t.Fatalf("direct_question should not emit a tool manifest in conversation-only mode, got %#v", brief.ToolManifest)
	}

	verify := RuntimeBriefForTaskFamily("verification_only", "main", available)
	if containsString(verify.AllowedTools, "pwd") || containsString(verify.AllowedTools, "edit_file") {
		t.Fatalf("verification_only allowed unexpected tools: %#v", verify.AllowedTools)
	}
	for _, expected := range []string{"shell", "read_file", "search_files", "search_content"} {
		if !containsString(verify.AllowedTools, expected) {
			t.Fatalf("verification_only missing %q in %#v", expected, verify.AllowedTools)
		}
	}

	archAvailable := []string{"task", "list_files", "search_files", "search_content", "read_file", "discovery_query", "structural_search", "dependency_graph", "history_range", "history_symbol", "blame_range", "ctags_symbols", "semgrep_scan", "joern_scan", "go_overview", "go_workspace_symbol", "go_read_symbol", "go_file_outline", "shell"}
	arch := RuntimeBriefForTaskFamily("architecture", "main", archAvailable)
	if containsString(arch.AllowedTools, "go_overview") || containsString(arch.AllowedTools, "shell") {
		t.Fatalf("architecture should advertise the lean read-only discovery surface, got %#v", arch.AllowedTools)
	}
	for _, expected := range []string{"task", "search_content", "read_file", "discovery_query", "structural_search", "dependency_graph", "history_range", "history_symbol", "blame_range", "ctags_symbols", "semgrep_scan", "joern_scan", "go_workspace_symbol", "go_read_symbol", "go_file_outline"} {
		if !containsString(arch.AllowedTools, expected) {
			t.Fatalf("architecture missing %q in %#v", expected, arch.AllowedTools)
		}
	}
	if containsString(arch.SearchPolicy, "delegate_broad_discovery_when_needed") {
		t.Fatalf("architecture should not bias toward delegated broad discovery, got %#v", arch.SearchPolicy)
	}
	if !containsString(arch.SearchPolicy, "prefer_discovery_query_for_architecture_boundary") || !containsString(arch.SearchPolicy, "use_dependency_graph_for_boundary_or_import_questions") || !containsString(arch.SearchPolicy, "use_history_only_for_evolution_questions") {
		t.Fatalf("architecture search policy should steer generic discovery tools, got %#v", arch.SearchPolicy)
	}
	for _, expected := range []string{"answer_when_boundary_evidence_is_sufficient", "if_not_answering_name_one_exact_missing_claim_and_probe"} {
		if !containsString(arch.SearchPolicy, expected) {
			t.Fatalf("architecture missing %q in %#v", expected, arch.SearchPolicy)
		}
	}
	if len(arch.ReadinessGate) == 0 || len(arch.FinalCritic) == 0 {
		t.Fatalf("architecture should keep closure gates and final critic, got %#v", arch)
	}

	security := RuntimeBriefForTaskFamily("security_review", "main", archAvailable)
	if containsString(security.SearchPolicy, "delegate_broad_discovery_when_needed") {
		t.Fatalf("security review should not bias toward delegated broad discovery, got %#v", security.SearchPolicy)
	}
	if !containsString(security.SearchPolicy, "prefer_semgrep_scan_for_security_patterns") || !containsString(security.SearchPolicy, "reserve_joern_scan_for_heavy_dataflow_questions") {
		t.Fatalf("security review should explicitly balance semgrep/joern usage, got %#v", security.SearchPolicy)
	}
	for _, expected := range []string{"answer_when_bounded_finding_or_no_finding_is_supported", "if_continuing_name_one_exact_missing_claim_and_probe"} {
		if !containsString(security.SearchPolicy, expected) {
			t.Fatalf("security review missing %q in %#v", expected, security.SearchPolicy)
		}
	}
	if len(security.ReadinessGate) == 0 || len(security.FinalCritic) == 0 {
		t.Fatalf("security review should keep closure gates and final critic, got %#v", security)
	}
}

func TestManifestToolsForCapabilityProfileExposeExpectedSurfaces(t *testing.T) {
	readOnly := ManifestToolsForCapabilityProfile(CapabilityConversationReadOnly)
	if !containsString(readOnly, "task") {
		t.Fatalf("conversation read-only manifest should include task, got %#v", readOnly)
	}
	if containsString(readOnly, "edit_file") {
		t.Fatalf("conversation read-only manifest should not include edit_file, got %#v", readOnly)
	}

	guardedWrite := ManifestToolsForCapabilityProfile(CapabilityGuardedWrite)
	for _, expected := range []string{"task", "task_ledger", "edit_file", "apply_patch"} {
		if !containsString(guardedWrite, expected) {
			t.Fatalf("guarded write manifest missing %q in %#v", expected, guardedWrite)
		}
	}
}
