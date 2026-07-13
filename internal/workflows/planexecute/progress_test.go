package planexecute

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	plancontract "idiom/internal/contracts/plan"
)

var _ = ginkgo.It("try load progress state rejects session mismatch", func() {
	root := ginkgo.GinkgoT().TempDir()
	state := NewProgressState("actual-session", "prompt", "", nil, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))
	state.SessionID = "other-session"

	fullPath, _, err := progressStatePath(root, "actual-session")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("progressStatePath: %v", err))

	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir: %v", err))
	}
	body, err := json.Marshal(state)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal: %v", err))

	if err := os.WriteFile(fullPath, body, 0o600); err != nil {
		ginkgo.Fail(fmt.Sprintf("write: %v", err))
	}

	_, _, found, err := TryLoadProgressState(root, "actual-session")
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected session mismatch error and found=false, found=%v err=%v", found, err))
	gomega.Expect(found).To(gomega.BeFalse(), fmt.Sprintf("expected session mismatch error and found=false, found=%v err=%v", found, err))
	gomega.Expect(strings.Contains(err.Error(), "does not match requested session")).To(gomega.BeTrue(), fmt.Sprintf("unexpected error: %v", err))
})

var _ = ginkgo.It("try load progress state rejects unknown lifecycle status", func() {
	root := ginkgo.GinkgoT().TempDir()
	state := NewProgressState("bad-status", "prompt", "", nil, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))
	state.Status = plancontract.PlanExecuteProgressStatus("typo")

	writeProgressStateFixture(root, state)

	_, _, found, err := TryLoadProgressState(root, state.SessionID)
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected invalid status error and found=false, found=%v err=%v", found, err))
	gomega.Expect(found).To(gomega.BeFalse(), fmt.Sprintf("expected invalid status error and found=false, found=%v err=%v", found, err))
	gomega.Expect(strings.Contains(err.Error(), "invalid plan-execute progress status")).To(gomega.BeTrue(), fmt.Sprintf("unexpected error: %v", err))
})

var _ = ginkgo.It("try load progress state accepts missing schema version", func() {
	root := ginkgo.GinkgoT().TempDir()
	state := NewProgressState("legacy-schema", "prompt", "", nil, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))
	state.SchemaVersion = ""
	writeProgressStateFixture(root, state)

	loaded, _, found, err := TryLoadProgressState(root, state.SessionID)
	gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("expected legacy progress state to load, found=%v err=%v", found, err))
	gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprintf("expected legacy progress state to load, found=%v err=%v", found, err))
	gomega.Expect(loaded.SchemaVersion != ProgressStateSchemaVersion).To(gomega.BeFalse(), fmt.Sprintf("SchemaVersion = %q, want %q", loaded.SchemaVersion, ProgressStateSchemaVersion))
})

var _ = ginkgo.It("try load progress state rejects unsupported schema version", func() {
	root := ginkgo.GinkgoT().TempDir()
	state := NewProgressState("future-schema", "prompt", "", nil, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))
	state.SchemaVersion = "idiom.plan_execute.progress.v99"
	writeProgressStateFixture(root, state)

	_, _, found, err := TryLoadProgressState(root, state.SessionID)
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected unsupported schema error and found=false, found=%v err=%v", found, err))
	gomega.Expect(found).To(gomega.BeFalse(), fmt.Sprintf("expected unsupported schema error and found=false, found=%v err=%v", found, err))
	gomega.Expect(strings.Contains(err.Error(), "unsupported plan-execute progress schema")).To(gomega.BeTrue(), fmt.Sprintf("unexpected error: %v", err))
})

var _ = ginkgo.It("try load progress state rejects impossible phase status", func() {
	root := ginkgo.GinkgoT().TempDir()
	state := NewProgressState("bad-lifecycle", "prompt", "", nil, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))
	state.Phase = plancontract.PlanExecuteProgressPhasePlanning
	state.Status = plancontract.PlanExecuteProgressStatusCompleted
	writeProgressStateFixture(root, state)

	_, _, found, err := TryLoadProgressState(root, state.SessionID)
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected invalid lifecycle error and found=false, found=%v err=%v", found, err))
	gomega.Expect(found).To(gomega.BeFalse(), fmt.Sprintf("expected invalid lifecycle error and found=false, found=%v err=%v", found, err))
	gomega.Expect(strings.Contains(err.Error(), "invalid plan-execute progress lifecycle")).To(gomega.BeTrue(), fmt.Sprintf("unexpected error: %v", err))
})

var _ = ginkgo.It("save progress state rejects unknown lifecycle phase", func() {
	state := NewProgressState("bad-phase", "prompt", "", nil, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))
	state.Phase = plancontract.PlanExecuteProgressPhase("typo")

	if _, err := SaveProgressState(ginkgo.GinkgoT().TempDir(), &state); err == nil || !strings.Contains(err.Error(), "invalid plan-execute progress phase") {
		ginkgo.Fail(fmt.Sprintf("expected invalid phase error, got %v", err))
	}
})

var _ = ginkgo.It("save progress state rejects unknown lifecycle status", func() {
	state := NewProgressState("bad-status-save", "prompt", "", nil, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))
	state.Status = plancontract.PlanExecuteProgressStatus("typo")

	if _, err := SaveProgressState(ginkgo.GinkgoT().TempDir(), &state); err == nil || !strings.Contains(err.Error(), "invalid plan-execute progress status") {
		ginkgo.Fail(fmt.Sprintf("expected invalid status error, got %v", err))
	}
})

var _ = ginkgo.It("save progress state rejects impossible phase status", func() {
	state := NewProgressState("bad-lifecycle-save", "prompt", "", nil, nil, time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))
	state.Phase = plancontract.PlanExecuteProgressPhasePlanning
	state.Status = plancontract.PlanExecuteProgressStatusCompleted

	if _, err := SaveProgressState(ginkgo.GinkgoT().TempDir(), &state); err == nil || !strings.Contains(err.Error(), "invalid plan-execute progress lifecycle") {
		ginkgo.Fail(fmt.Sprintf("expected invalid lifecycle error, got %v", err))
	}
})

var _ = ginkgo.It("mark review complete merges repair changed paths", func() {
	state := NewProgressState("session", "prompt", "", []string{"a.go"}, nil, time.Now())
	state.ChangedPaths = []string{"a.go"}

	state.MarkReviewComplete(ReviewRepairPhaseReport{
		Status:               plancontract.PlanExecuteReviewStatusRepaired,
		RepairPaths:          []string{"b.go"},
		CombinedChangedPaths: []string{"a.go", "b.go"},
	}, nil)

	if got := strings.Join(state.ChangedPaths, ","); got != "a.go,b.go" {
		ginkgo.Fail(fmt.Sprintf("ChangedPaths = %q, want a.go,b.go", got))
	}
})

func writeProgressStateFixture(root string, state ProgressState) {
	ginkgo.GinkgoHelper()

	fullPath, _, err := progressStatePath(root, state.SessionID)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("progressStatePath: %v", err))

	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir: %v", err))
	}
	body, err := json.Marshal(state)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal: %v", err))

	if err := os.WriteFile(fullPath, body, 0o600); err != nil {
		ginkgo.Fail(fmt.Sprintf("write: %v", err))
	}
}

var _ = ginkgo.It("mark review complete persists typed degraded failure kind", func() {
	state := NewProgressState("session", "prompt", "", nil, nil, time.Now())

	state.MarkReviewComplete(ReviewRepairPhaseReport{
		Status:       plancontract.PlanExecuteReviewStatusDegraded,
		StatusDetail: "review unavailable",
		ReasonClass:  "backend_timeout",
	}, nil)
	gomega.Expect(state.FailureKind != plancontract.PlanExecuteFailureReviewRepairDegraded).To(gomega.BeFalse(), fmt.Sprintf("FailureKind = %q, want %q", state.FailureKind, plancontract.PlanExecuteFailureReviewRepairDegraded))
	gomega.Expect(state.Review.ReasonClass != "backend_timeout").To(gomega.BeFalse(), fmt.Sprintf("Review.ReasonClass = %q", state.Review.ReasonClass))
})
