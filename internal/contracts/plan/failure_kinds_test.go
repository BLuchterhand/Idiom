package plan

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestKnownPlanExecuteFailureKindCoversPlanExecuteFailures(t *testing.T) {
	seen := map[PlanExecuteFailureKind]struct{}{}
	for _, kind := range PlanExecuteFailureKinds() {
		if _, ok := seen[kind]; ok {
			t.Fatalf("duplicate PlanExecuteFailureKinds entry %q", kind)
		}
		seen[kind] = struct{}{}
		if !KnownPlanExecuteFailureKind(string(kind)) {
			t.Fatalf("KnownPlanExecuteFailureKind(%q) = false", kind)
		}
		if parsed, ok := ParsePlanExecuteFailureKind(string(kind)); !ok || parsed != kind {
			t.Fatalf("ParsePlanExecuteFailureKind(%q) = %q %v, want %q true", kind, parsed, ok, kind)
		}
	}

	if KnownPlanExecuteFailureKind("review_repair_typo") {
		t.Fatal("KnownPlanExecuteFailureKind accepted unknown kind")
	}
}

func TestParsePlanExecuteFailureKindAcceptsLegacyHyphenatedSpelling(t *testing.T) {
	got, ok := ParsePlanExecuteFailureKind("missing-requested-test-edit")
	if !ok || got != PlanExecuteFailureMissingRequestedTestEdit {
		t.Fatalf("ParsePlanExecuteFailureKind legacy spelling = %q %v", got, ok)
	}

	got, ok = ParsePlanExecuteFailureKind("review_repair_blocked")
	if !ok || got != PlanExecuteFailureReviewRepairBlocked {
		t.Fatalf("ParsePlanExecuteFailureKind exact spelling = %q %v", got, ok)
	}

	if got, ok := ParsePlanExecuteFailureKind("review-repair-typo"); ok {
		t.Fatalf("ParsePlanExecuteFailureKind accepted unknown kind %q", got)
	}
}

func TestPlanExecuteFailureKindsCoverDeclaredConstants(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	sourcePath := filepath.Join(filepath.Dir(filename), "failure_kinds.go")
	body, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read %s: %v", sourcePath, err)
	}

	declared := map[PlanExecuteFailureKind]string{}
	declRE := regexp.MustCompile(`(?m)(PlanExecuteFailure[A-Za-z0-9]+)\s+PlanExecuteFailureKind\s*=\s*"([^"]+)"`)
	for _, match := range declRE.FindAllStringSubmatch(string(body), -1) {
		declared[PlanExecuteFailureKind(match[2])] = match[1]
	}
	if len(declared) == 0 {
		t.Fatal("no PlanExecuteFailureKind constants found")
	}

	known := map[PlanExecuteFailureKind]struct{}{}
	for _, kind := range PlanExecuteFailureKinds() {
		known[kind] = struct{}{}
	}

	var missing []string
	for kind, name := range declared {
		if _, ok := known[kind]; !ok {
			missing = append(missing, name+"="+string(kind))
		}
	}
	if len(missing) > 0 {
		t.Fatalf("PlanExecuteFailureKinds missing declared constants:\n%s", strings.Join(missing, "\n"))
	}
}
