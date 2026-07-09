package recovery

import "testing"

func TestRecoveryFailureVocabularyIsKnown(t *testing.T) {
	for _, lane := range []SourceLane{SourceLanePlanExecute, SourceLaneImplement} {
		if !KnownSourceLane(lane) {
			t.Fatalf("KnownSourceLane(%q) = false", lane)
		}
	}
	for _, class := range FailureClasses() {
		if !KnownFailureClass(class) {
			t.Fatalf("KnownFailureClass(%q) = false", class)
		}
	}
	for _, status := range DirtyStatuses() {
		if got, ok := ParseDirtyStatus(string(status)); !ok || got != status {
			t.Fatalf("ParseDirtyStatus(%q) = %q %v, want %q true", status, got, ok, status)
		}
	}
	for _, status := range ScopeStatuses() {
		if got, ok := ParseScopeStatus(string(status)); !ok || got != status {
			t.Fatalf("ParseScopeStatus(%q) = %q %v, want %q true", status, got, ok, status)
		}
	}

	if KnownSourceLane(SourceLane("typo")) {
		t.Fatal("KnownSourceLane accepted unknown lane")
	}
	if KnownFailureClass(FailureClass("typo")) {
		t.Fatal("KnownFailureClass accepted unknown class")
	}
	if _, ok := ParseDirtyStatus("typo"); ok {
		t.Fatal("ParseDirtyStatus accepted unknown status")
	}
	if _, ok := ParseScopeStatus("typo"); ok {
		t.Fatal("ParseScopeStatus accepted unknown status")
	}
}

func TestRecoveryFailureVocabularyIncludesCanonicalConstants(t *testing.T) {
	classes := map[FailureClass]bool{}
	for _, class := range FailureClasses() {
		classes[class] = true
	}
	for _, class := range []FailureClass{
		FailureClassPlannerFailed,
		FailureClassPlannerBlocked,
		FailureClassPlanMissing,
		FailureClassPlanInvalid,
		FailureClassScopeTooLarge,
		FailureClassExecutorEdit,
		FailureClassVerification,
		FailureClassReviewRepair,
		FailureClassStaleContext,
		FailureClassBudgetExhausted,
		FailureClassDirtyWorkspace,
		FailureClassNeedsHumanAnswer,
		FailureClassUnsafePolicyBlock,
		FailureClassUnknown,
	} {
		if !classes[class] {
			t.Fatalf("FailureClasses() missing %q: %#v", class, FailureClasses())
		}
	}
}

func TestRecoveryFailureVocabularyParsersTrimButRemainCaseSensitive(t *testing.T) {
	if got, ok := ParseFailureClass(" \t" + string(FailureClassPlannerFailed) + "\n"); !ok || got != FailureClassPlannerFailed {
		t.Fatalf("ParseFailureClass trimmed planner_failed = %q %v", got, ok)
	}
	if got, ok := ParseDirtyStatus(" \t" + string(DirtyStatusDirtyInScope) + "\n"); !ok || got != DirtyStatusDirtyInScope {
		t.Fatalf("ParseDirtyStatus trimmed dirty_in_scope = %q %v", got, ok)
	}
	if got, ok := ParseScopeStatus(" \t" + string(ScopeStatusOutOfScope) + "\n"); !ok || got != ScopeStatusOutOfScope {
		t.Fatalf("ParseScopeStatus trimmed out_of_scope = %q %v", got, ok)
	}

	for _, raw := range []string{"PLANNER_FAILED", "DIRTY_IN_SCOPE", "OUT_OF_SCOPE"} {
		if class, ok := ParseFailureClass(raw); ok {
			t.Fatalf("ParseFailureClass(%q) = %q, true", raw, class)
		}
		if status, ok := ParseDirtyStatus(raw); ok {
			t.Fatalf("ParseDirtyStatus(%q) = %q, true", raw, status)
		}
		if status, ok := ParseScopeStatus(raw); ok {
			t.Fatalf("ParseScopeStatus(%q) = %q, true", raw, status)
		}
	}
}
