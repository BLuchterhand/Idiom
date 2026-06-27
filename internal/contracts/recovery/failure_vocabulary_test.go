package recovery

import "testing"

func TestRecoveryFailureVocabularyIsKnown(t *testing.T) {
	for _, lane := range SourceLanes() {
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
	lanes := map[SourceLane]bool{}
	for _, lane := range SourceLanes() {
		lanes[lane] = true
	}
	for _, lane := range []SourceLane{SourceLanePlanExecute, SourceLaneImplement} {
		if !lanes[lane] {
			t.Fatalf("SourceLanes() missing %q: %#v", lane, SourceLanes())
		}
	}

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
