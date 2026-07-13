package product

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/workflows/productworkflow"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.It("product feedback passes with valid references", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "references/lane_history.json", `[{"lane":"implement","event":"closed","status":"pass","timestamp":"2026-05-18T00:00:00Z"}]`)
	writeTestFile(t, dir, "references/plan_progress.json", `{"phase":"phase-5","state":"feedback","status":"completed"}`)
	writeTestFile(t, dir, "references/verification.json", `{"status":"passed","commands":["go test ./cmd/idiom"]}`)

	var stdout, stderr bytes.Buffer

	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--lane-history-file", "references/lane_history.json",
		"--plan-progress-file", "references/plan_progress.json",
		"--verification-file", "references/verification.json",
		"--product-evidence-id", "pe-1",
		"--product-evidence-path", "product_evidence/export-1.json",
		"--release-evidence-id", "re-1",
		"--release-evidence-path", "release_evidence/bundle-1.json",
		"--brief-path", "briefs/test.md",
		"--changed-path", "cmd/idiom/product_workflow.go",
		"--tdd-obligation-ref", "TDD-001",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected code 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out productFeedbackOutput

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "expected JSON output: %v\n%s", err, stdout.String())
	}

	gomega.Expect(out.Status).To(gomega.Equal(productworkflow.ProductWorkflowStatusPass), "expected pass output, got %#v", out)
	gomega.Expect(out.Summary.Status).To(gomega.Equal(productworkflow.ProductWorkflowStatusPass), "expected pass output, got %#v", out)

	gomega.Expect(out.Summary.ChangedPaths).To(gomega.HaveLen(1), "expected changed paths in summary, got %#v", out.Summary.ChangedPaths)
	gomega.Expect(out.Summary.ChangedPaths[0]).To(gomega.Equal("cmd/idiom/product_workflow.go"), "expected changed paths in summary, got %#v", out.Summary.ChangedPaths)

	gomega.Expect(out.Summary.Verification.Status).To(gomega.Equal("passed"), "expected verification summary details, got %#v", out.Summary.Verification)
	gomega.Expect(out.Summary.Verification.Commands).To(gomega.HaveLen(1), "expected verification summary details, got %#v", out.Summary.Verification)

	gomega.Expect(out.Summary.BriefPath).To(gomega.BeEquivalentTo("briefs/test.md"), "expected brief path in summary, got %q", out.Summary.BriefPath)

	gomega.Expect(out.Summary.ProductEvidence.Path).To(gomega.Equal("product_evidence/export-1.json"), "expected evidence references in summary, got product=%#v release=%#v", out.Summary.ProductEvidence, out.Summary.ReleaseEvidence)
	gomega.Expect(out.Summary.ReleaseEvidence.Path).To(gomega.Equal("release_evidence/bundle-1.json"), "expected evidence references in summary, got product=%#v release=%#v", out.Summary.ProductEvidence, out.Summary.ReleaseEvidence)

	gomega.Expect(out.Summary.TDDObligations).To(gomega.HaveLen(1), "expected TDD refs in summary, got %#v", out.Summary.TDDObligations)
	gomega.Expect(out.Summary.TDDObligations[0]).To(gomega.Equal("TDD-001"), "expected TDD refs in summary, got %#v", out.Summary.TDDObligations)
})

var _ = ginkgo.It("product feedback emits ranker-learning evidence JSON", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestWorkflowStateFixture(t, dir, func(state *productworkflow.WorkflowState) {
		state.CurrentPhase = "phase-5-execution-feedback"
	})
	writeTestFile(t, dir, "references/feedback_summary.json", validRankerLearningFeedbackSummaryJSON("pass"))

	var stdout, stderr bytes.Buffer

	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--ranker-learning-evidence-summary", "references/feedback_summary.json",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected code 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out productFeedbackRankerLearningOutput

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "expected JSON output: %v\n%s", err, stdout.String())
	}

	gomega.Expect(out.Status).To(gomega.Equal(productworkflow.ProductWorkflowStatusPass), "expected pass output, got %#v", out)
	gomega.Expect(out.RankerLearningEvidence.Status).To(gomega.Equal(productworkflow.ProductWorkflowStatusPass), "expected pass output, got %#v", out)

	gomega.Expect(out.RankerLearningEvidence.Request).NotTo(gomega.BeNil(), "expected bounded learning request, got %#v", out)

	gomega.Expect(out.RankerLearningEvidence.Summary.ContextPackID).To(gomega.BeEquivalentTo("pack"), "expected context pack id, got %#v", out.RankerLearningEvidence.Summary)

	gomega.Expect(out.RankerLearningEvidence.Request.Action).To(gomega.Equal("boost"), "expected deterministic request shape, got %#v", out.RankerLearningEvidence.Request)
	gomega.Expect(out.RankerLearningEvidence.Request.ResultID).To(gomega.Equal("feedback_r1"), "expected deterministic request shape, got %#v", out.RankerLearningEvidence.Request)

	gomega.Expect(strings.Contains(out.NextValidAction, "no ranker promotion was applied")).To(gomega.BeTrue(), "expected bounded next action, got %q", out.NextValidAction)

	{
		_, err := os.Stat(filepath.Join(dir, ".idiom"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "ranker-learning evidence mode must not write .idiom state, err=%v", err)
	}
})

var _ = ginkgo.It("product feedback ranker learning evidence text output", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestWorkflowStateFixture(t, dir, func(state *productworkflow.WorkflowState) {
		state.CurrentPhase = "phase-5-execution-feedback"
	})
	writeTestFile(t, dir, "references/feedback_summary.json", validRankerLearningFeedbackSummaryJSON("pass"))

	var stdout, stderr bytes.Buffer

	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--ranker-learning-evidence-summary", "references/feedback_summary.json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected code 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	text := stdout.String()
	for _, want := range []string{
		"product feedback ranker-learning-evidence: pass",
		"context_pack: pack (current)",
		"request: boost",
		"findings: 1",
		"[ranker_learning_request_prepared]",
		"next: review bounded ranker-learning evidence",
	} {
		gomega.Expect(strings.Contains(text, want)).To(gomega.BeTrue(), "expected text output to contain %q, got:\n%s", want, text)
	}
})

var _ = ginkgo.It("product feedback ranker learning evidence fails closed", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestWorkflowStateFixture(t, dir, func(state *productworkflow.WorkflowState) {
		state.CurrentPhase = "phase-5-execution-feedback"
	})
	writeTestFile(t, dir, "references/feedback_summary.json", validRankerLearningFeedbackSummaryJSON("blocked"))

	var stdout, stderr bytes.Buffer

	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--ranker-learning-evidence-summary", "references/feedback_summary.json",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out productFeedbackRankerLearningOutput

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "expected JSON output: %v\n%s", err, stdout.String())
	}

	gomega.Expect(out.Status).To(gomega.Equal(productworkflow.ProductWorkflowStatusBlocked), "expected blocked adapter output without request, got %#v", out)
	gomega.Expect(out.RankerLearningEvidence.Request).To(gomega.BeNil(), "expected blocked adapter output without request, got %#v", out)

	kinds := map[string]bool{}
	for _, blocker := range out.RankerLearningEvidence.Blockers {
		kinds[blocker.Kind] = true
	}

	for _, want := range []string{"feedback_not_pass", "feedback_contains_blockers", "unknown_tdd_obligation", "unsafe_artifact_reference"} {
		gomega.Expect(kinds[want]).To(gomega.BeTrue(), "expected blocker %s, got %#v", want, out.RankerLearningEvidence.Blockers)
	}

	{
		_, err := os.Stat(filepath.Join(dir, ".idiom"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "blocked adapter output must not write .idiom state, err=%v", err)
	}
})

var _ = ginkgo.It("product feedback ranker learning evidence rejects wrapped blocked status", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestWorkflowStateFixture(t, dir, func(state *productworkflow.WorkflowState) {
		state.CurrentPhase = "phase-5-execution-feedback"
	})
	writeTestFile(t, dir, "references/feedback_summary.json", `{"status":"blocked","summary":`+validRankerLearningFeedbackSummaryJSON("pass")+`}`)

	var stdout, stderr bytes.Buffer

	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--ranker-learning-evidence-summary", "references/feedback_summary.json",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	gomega.Expect(stdout.Len()).To(gomega.BeEquivalentTo(0), "expected no JSON request for blocked wrapper, got stdout=%s", stdout.String())

	gomega.Expect(strings.Contains(stderr.String(), `summary status must be pass`)).To(gomega.BeTrue(), "expected wrapped status error, got stderr=%s", stderr.String())

	{
		_, err := os.Stat(filepath.Join(dir, ".idiom"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "blocked wrapper must not write .idiom state, err=%v", err)
	}
})

var _ = ginkgo.It("product feedback ranker learning evidence rejects ranker state path", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestWorkflowStateFixture(t, dir, func(state *productworkflow.WorkflowState) {
		state.CurrentPhase = "phase-5-execution-feedback"
	})

	summary := strings.Replace(validRankerLearningFeedbackSummaryJSON("pass"), `"changed_paths":["cmd/idiom/product_workflow.go"]`, `"changed_paths":[".idiom/ranker/weights.json"]`, 1)
	writeTestFile(t, dir, "references/feedback_summary.json", summary)

	var stdout, stderr bytes.Buffer

	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--ranker-learning-evidence-summary", "references/feedback_summary.json",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out productFeedbackRankerLearningOutput

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "expected JSON output: %v\n%s", err, stdout.String())
	}

	gomega.Expect(out.Status).To(gomega.Equal(productworkflow.ProductWorkflowStatusBlocked), "expected blocked output without request, got %#v", out)
	gomega.Expect(out.RankerLearningEvidence.Request).To(gomega.BeNil(), "expected blocked output without request, got %#v", out)

	found := false
	for _, blocker := range out.RankerLearningEvidence.Blockers {
		if blocker.Kind == "unsupported_authority_reference" && strings.Contains(blocker.Message, ".idiom/ranker") {
			found = true
			break
		}
	}
	gomega.Expect(found).To(gomega.BeTrue(), "expected ranker state path blocker, got %#v", out.RankerLearningEvidence.Blockers)
})

var _ = ginkgo.It("product feedback reconcile passes and writes output", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestWorkflowStateFixture(t, dir, func(state *productworkflow.WorkflowState) {
		state.CurrentPhase = "phase-5-execution-feedback"
	})
	writeTestFile(t, dir, "references/feedback_summary.json", `{
  "status":"pass",
  "effort_id":"test-effort",
  "task_id":"impl-test",
	  "state_revision":1,
	  "current_phase":"phase-5-execution-feedback",
	  "current_state":"planned",
  "lane_history":[{"lane":"implement","event":"closed","status":"pass","timestamp":"2026-05-18T00:00:00Z"}],
  "plan_phase":"phase-5",
  "plan_state":"feedback",
  "plan_status":"completed",
  "product_evidence":{"id":"pe-1","path":"product_evidence/export-1.json"},
  "release_evidence":{"id":"re-1","path":"release_evidence/bundle-1.json"},
  "brief_path":"briefs/test.md",
  "changed_paths":["cmd/idiom/product_workflow.go"],
  "verification":{"status":"pass","commands":["go test ./cmd/idiom"]},
  "tdd_obligations":["TDD-001"],
  "findings":[],
  "blockers":[],
  "next_valid_action":"publish execution feedback summary as deterministic closeout evidence"
}`)

	var stdout, stderr bytes.Buffer

	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--reconcile-summary", "references/feedback_summary.json",
		"--reconciled-state-output", "reports/workflow_state.reconciled.json",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected code 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out productFeedbackReconcileOutput

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "expected JSON output: %v\n%s", err, stdout.String())
	}

	gomega.Expect(out.Status).To(gomega.Equal(productworkflow.ProductWorkflowStatusPass), "expected pass reconciliation output, got %#v", out)
	gomega.Expect(out.Reconciliation.Status).To(gomega.Equal(productworkflow.ProductWorkflowStatusPass), "expected pass reconciliation output, got %#v", out)

	gomega.Expect(out.Reconciliation.EvidenceLinks.LinkedArtifactPathCount).To(gomega.BeEquivalentTo(4), "expected linked path count 4, got %#v", out.Reconciliation.EvidenceLinks)

	data, err := os.ReadFile(filepath.Join(dir, "reports", "workflow_state.reconciled.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "expected reconciled output file: %v", err)

	var state map[string]any

	{
		err := json.Unmarshal(data, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "expected JSON output state: %v", err)
	}

	artifactPaths, _ := state["artifact_paths"].(map[string]any)
	gomega.Expect(artifactPaths["execution_feedback_summary"]).To(gomega.BeEquivalentTo("references/feedback_summary.json"), "expected summary artifact link, got %#v", artifactPaths)
})

var _ = ginkgo.It("product feedback reconcile blocks malformed pass summary", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "references/feedback_summary.json", `{
  "status":"pass",
  "effort_id":"test-effort",
  "task_id":"impl-test",
  "state_revision":1,
  "product_evidence":{"id":"pe-1","path":"product_evidence/export-1.json"},
  "release_evidence":{"id":"re-1","path":"release_evidence/bundle-1.json"},
  "brief_path":"briefs/test.md"
}`)

	var stdout, stderr bytes.Buffer

	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--reconcile-summary", "references/feedback_summary.json",
		"--reconciled-state-output", "reports/workflow_state.reconciled.json",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for malformed pass summary, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out productFeedbackReconcileOutput

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "expected JSON output: %v\n%s", err, stdout.String())
	}

	gomega.Expect(out.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusBlocked), "expected blocked output, got %#v", out)

	{
		_, err := os.Stat(filepath.Join(dir, "reports", "workflow_state.reconciled.json"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "expected malformed summary not to write output file, got err=%v", err)
	}
})

var _ = ginkgo.It("product feedback reconcile fails closed", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "references/feedback_summary.json", `{
  "status":"blocked",
  "effort_id":"wrong-effort",
  "task_id":"wrong-task",
  "lane_history":[{"lane":"implement","event":"closed","status":"pass","timestamp":"2026-05-18T00:00:00Z"}],
  "product_evidence":{"id":"pe-1","path":"../escape.json"},
  "release_evidence":{"id":"re-1","path":"release_evidence/bundle-1.json"},
  "brief_path":"briefs/test.md",
  "verification":{"status":"pass","commands":[]},
  "tdd_obligations":["TDD-001"],
  "findings":[],
  "blockers":[{"kind":"x","message":"y","next_valid_action":"z"}],
  "next_valid_action":"dispatch lane now"
}`)

	var stdout, stderr bytes.Buffer

	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--reconcile-summary", "references/feedback_summary.json",
		"--reconciled-state-output", "reports/workflow_state.reconciled.json",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out productFeedbackReconcileOutput

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "expected JSON output: %v\n%s", err, stdout.String())
	}

	gomega.Expect(out.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusBlocked), "expected blocked, got %#v", out)

	gomega.Expect(len(out.Reconciliation.Blockers)).NotTo(gomega.BeEquivalentTo(0), "expected reconciliation blockers, got %#v", out)

	{
		_, err := os.Stat(filepath.Join(dir, "reports", "workflow_state.reconciled.json"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "expected blocked reconciliation not to write output file, got err=%v", err)
	}
})

var _ = ginkgo.It("product feedback reconcile text output is actionable", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestWorkflowStateFixture(t, dir, func(state *productworkflow.WorkflowState) {
		state.CurrentPhase = "phase-5-execution-feedback"
	})
	writeTestFile(t, dir, "references/feedback_summary.json", `{
  "status":"pass",
  "effort_id":"test-effort",
  "task_id":"impl-test",
  "state_revision":1,
  "current_phase":"phase-5-execution-feedback",
  "current_state":"planned",
  "lane_history":[{"lane":"implement","event":"closed","status":"pass","timestamp":"2026-05-18T00:00:00Z"}],
  "plan_phase":"phase-5",
  "plan_state":"feedback",
  "plan_status":"completed",
  "product_evidence":{"id":"pe-1","path":"product_evidence/export-1.json"},
  "release_evidence":{"id":"re-1","path":"release_evidence/bundle-1.json"},
  "brief_path":"briefs/test.md",
  "changed_paths":["cmd/idiom/product_workflow.go"],
  "verification":{"status":"pass","commands":["go test ./cmd/idiom"]},
  "tdd_obligations":["TDD-001"],
  "findings":[],
  "blockers":[],
  "next_valid_action":"publish execution feedback summary as deterministic closeout evidence"
}`)

	var stdout, stderr bytes.Buffer

	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--reconcile-summary", "references/feedback_summary.json",
		"--reconciled-state-output", "reports/workflow_state.reconciled.json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected code 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	text := stdout.String()
	for _, want := range []string{"product feedback reconcile: pass", "evidence links: 4", "next: review reconciled workflow-state output"} {
		gomega.Expect(strings.Contains(text, want)).To(gomega.BeTrue(), "expected text output to contain %q, got:\n%s", want, text)
	}
})

var _ = ginkgo.It("product feedback reconcile rejects workflow state overwrite", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "references/feedback_summary.json", `{"status":"pass"}`)

	var stdout, stderr bytes.Buffer

	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--reconcile-summary", "references/feedback_summary.json",
		"--reconciled-state-output", "workflow_state.json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for workflow_state overwrite, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	gomega.Expect(stderr.String()).To(gomega.ContainSubstring("must not overwrite authority input workflow_state.json"))
})

var _ = ginkgo.It("product feedback reconcile rejects output symlink ancestor", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestWorkflowStateFixture(t, dir, func(state *productworkflow.WorkflowState) {
		state.CurrentPhase = "phase-5-execution-feedback"
	})
	writeTestFile(t, dir, "references/feedback_summary.json", `{
  "status":"pass",
  "effort_id":"test-effort",
  "task_id":"impl-test",
  "state_revision":1,
  "current_phase":"phase-5-execution-feedback",
  "current_state":"planned",
  "lane_history":[{"lane":"implement","event":"closed","status":"pass","timestamp":"2026-05-18T00:00:00Z"}],
  "plan_phase":"phase-5",
  "plan_state":"feedback",
  "plan_status":"completed",
  "product_evidence":{"id":"pe-1","path":"product_evidence/export-1.json"},
  "release_evidence":{"id":"re-1","path":"release_evidence/bundle-1.json"},
  "brief_path":"briefs/test.md",
  "changed_paths":["cmd/idiom/product_workflow.go"],
  "verification":{"status":"pass","commands":["go test ./cmd/idiom"]},
  "tdd_obligations":["TDD-001"],
  "findings":[],
  "blockers":[],
  "next_valid_action":"publish execution feedback summary as deterministic closeout evidence"
}`)

	outside := t.TempDir()
	err := os.Symlink(outside, filepath.Join(dir, "linked-output"))
	if os.IsPermission(err) {
		ginkgo.Skip(fmt.Sprintf("symlink unavailable: %v", err))
	}
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "create symlink")

	var stdout, stderr bytes.Buffer

	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--reconcile-summary", "references/feedback_summary.json",
		"--reconciled-state-output", "linked-output/workflow_state.reconciled.json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for output symlink ancestor, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	gomega.Expect(stderr.String()).To(gomega.ContainSubstring("must not traverse symlinked directories"))
})

var _ = ginkgo.It("product feedback reconcile blocks symlink ancestor", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	outside := t.TempDir()
	writeTestFile(t, outside, "feedback_summary.json", `{"status":"pass"}`)

	link := filepath.Join(dir, "linked")
	err := os.Symlink(outside, link)
	if os.IsPermission(err) {
		ginkgo.Skip(fmt.Sprintf("symlink unavailable: %v", err))
	}
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "create symlink")

	var stdout, stderr bytes.Buffer

	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--reconcile-summary", "linked/feedback_summary.json",
		"--reconciled-state-output", "reports/workflow_state.reconciled.json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for symlink ancestor, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	gomega.Expect(stderr.String()).To(gomega.ContainSubstring("must not traverse symlinked directories"))
})

var _ = ginkgo.It("product feedback text output is actionable", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "references/lane_history.json", `[{"lane":"implement","event":"closed","status":"completed","timestamp":"2026-05-18T00:00:00Z"}]`)
	writeTestFile(t, dir, "references/plan_progress.json", `{"phase":"phase-5","state":"feedback","status":"completed"}`)
	writeTestFile(t, dir, "references/verification.json", `{"status":"passed","commands":["go test ./cmd/idiom"]}`)

	var stdout, stderr bytes.Buffer

	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--lane-history-file", "references/lane_history.json",
		"--plan-progress-file", "references/plan_progress.json",
		"--verification-file", "references/verification.json",
		"--product-evidence-id", "pe-1",
		"--product-evidence-path", "product_evidence/export-1.json",
		"--release-evidence-id", "re-1",
		"--release-evidence-path", "release_evidence/bundle-1.json",
		"--brief-path", "briefs/test.md",
		"--changed-path", "cmd/idiom/product_workflow.go",
		"--tdd-obligation-ref", "TDD-001",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected code 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	text := stdout.String()
	for _, want := range []string{"product feedback: pass", "verification: passed", "next:"} {
		gomega.Expect(strings.Contains(text, want)).To(gomega.BeTrue(), "expected text output to contain %q, got:\n%s", want, text)
	}
})

var _ = ginkgo.It("product feedback fails closed on malformed references", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "references/lane_history.json", `{"bad":"shape"}`)
	writeTestFile(t, dir, "references/plan_progress.json", `{"phase":"phase-5","state":"feedback","status":"pass"}`)
	writeTestFile(t, dir, "references/verification.json", `{"status":"pass","commands":["go test ./cmd/idiom"]}`)

	var stdout, stderr bytes.Buffer

	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--lane-history-file", "references/lane_history.json",
		"--plan-progress-file", "references/plan_progress.json",
		"--verification-file", "references/verification.json",
		"--product-evidence-id", "pe-1",
		"--product-evidence-path", "product_evidence/export-1.json",
		"--release-evidence-id", "re-1",
		"--release-evidence-path", "release_evidence/bundle-1.json",
		"--brief-path", "briefs/test.md",
		"--changed-path", "cmd/idiom/product_workflow.go",
		"--tdd-obligation-ref", "TDD-001",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for malformed lane history, got %d", code)

	gomega.Expect(strings.Contains(stderr.String(), "lane history reference invalid")).To(gomega.BeTrue(), "expected lane history parse failure in stderr, got %s", stderr.String())
})

var _ = ginkgo.It("product feedback blocks unsafe artifact references", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "references/lane_history.json", `[{"lane":"implement","event":"closed","status":"completed","timestamp":"2026-05-18T00:00:00Z"}]`)
	writeTestFile(t, dir, "references/plan_progress.json", `{"phase":"phase-5","state":"feedback","status":"completed"}`)
	writeTestFile(t, dir, "references/verification.json", `{"status":"passed","commands":["go test ./cmd/idiom"]}`)

	var stdout, stderr bytes.Buffer

	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--lane-history-file", "references/lane_history.json",
		"--plan-progress-file", "references/plan_progress.json",
		"--verification-file", "references/verification.json",
		"--product-evidence-id", "pe-1",
		"--product-evidence-path", "../product.json",
		"--release-evidence-id", "re-1",
		"--release-evidence-path", "release_evidence/bundle-1.json",
		"--brief-path", "briefs/test.md",
		"--changed-path", "cmd/idiom/product_workflow.go",
		"--tdd-obligation-ref", "TDD-001",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for unsafe artifact ref, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out productFeedbackOutput

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "expected JSON output: %v\n%s", err, stdout.String())
	}

	gomega.Expect(strings.Contains(out.NextValidAction, "safe relative evidence artifact path")).To(gomega.BeTrue(), "expected actionable unsafe path next action, got %#v", out)
})

var _ = ginkgo.It("product feedback does not override referenced verification status", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "references/lane_history.json", `[{"lane":"implement","event":"closed","status":"pass","timestamp":"2026-05-18T00:00:00Z"}]`)
	writeTestFile(t, dir, "references/plan_progress.json", `{"phase":"phase-5","state":"feedback","status":"pass"}`)
	writeTestFile(t, dir, "references/verification.json", `{"status":"fail","commands":["go test ./cmd/idiom"]}`)

	var stdout, stderr bytes.Buffer

	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--lane-history-file", "references/lane_history.json",
		"--plan-progress-file", "references/plan_progress.json",
		"--verification-file", "references/verification.json",
		"--verification-status", "pass",
		"--product-evidence-id", "pe-1",
		"--product-evidence-path", "product_evidence/export-1.json",
		"--release-evidence-id", "re-1",
		"--release-evidence-path", "release_evidence/bundle-1.json",
		"--brief-path", "briefs/test.md",
		"--changed-path", "cmd/idiom/product_workflow.go",
		"--tdd-obligation-ref", "TDD-001",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(2), "expected unknown override flag to be rejected, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	gomega.Expect(strings.Contains(stderr.String(), "flag provided but not defined")).To(gomega.BeTrue(), "expected unknown flag failure, got %s", stderr.String())
})

var _ = ginkgo.It("product feedback usage errors", func() {
	var stdout, stderr bytes.Buffer

	code := runProductFeedback([]string{}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(2), "expected code 2 for usage error, got %d", code)
})

var _ = ginkgo.It("product feedback blocks traversal reference path", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)

	var stdout, stderr bytes.Buffer

	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--lane-history-file", "../lane_history.json",
		"--plan-progress-file", "references/plan_progress.json",
		"--verification-file", "references/verification.json",
		"--product-evidence-id", "pe-1",
		"--product-evidence-path", "product_evidence/export-1.json",
		"--release-evidence-id", "re-1",
		"--release-evidence-path", "release_evidence/bundle-1.json",
		"--brief-path", "briefs/test.md",
		"--changed-path", "cmd/idiom/product_workflow.go",
		"--tdd-obligation-ref", "TDD-001",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for traversal path, got %d", code)

	gomega.Expect(strings.Contains(stderr.String(), "must be effort-relative")).To(gomega.BeTrue(), "expected effort-relative failure, got %s", stderr.String())
})

var _ = ginkgo.It("product feedback blocks absolute reference path", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	absLane := filepath.Join(dir, "references", "lane_history.json")

	var stdout, stderr bytes.Buffer

	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--lane-history-file", absLane,
		"--plan-progress-file", "references/plan_progress.json",
		"--verification-file", "references/verification.json",
		"--product-evidence-id", "pe-1",
		"--product-evidence-path", "product_evidence/export-1.json",
		"--release-evidence-id", "re-1",
		"--release-evidence-path", "release_evidence/bundle-1.json",
		"--brief-path", "briefs/test.md",
		"--changed-path", "cmd/idiom/product_workflow.go",
		"--tdd-obligation-ref", "TDD-001",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for absolute path, got %d", code)

	gomega.Expect(strings.Contains(stderr.String(), "must be effort-relative")).To(gomega.BeTrue(), "expected effort-relative failure, got %s", stderr.String())
})

var _ = ginkgo.It("product feedback blocks symlink reference path", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	outside := filepath.Join(t.TempDir(), "outside.json")
	writeTestFile(t, filepath.Dir(outside), filepath.Base(outside), `[]`)

	{
		err := os.MkdirAll(filepath.Join(dir, "references"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "mkdir references: %v", err)
	}

	err := os.Symlink(outside, filepath.Join(dir, "references", "lane_history.json"))
	if os.IsPermission(err) {
		ginkgo.Skip(fmt.Sprintf("symlink unavailable: %v", err))
	}
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "create symlink")

	var stdout, stderr bytes.Buffer

	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--lane-history-file", "references/lane_history.json",
		"--plan-progress-file", "references/plan_progress.json",
		"--verification-file", "references/verification.json",
		"--product-evidence-id", "pe-1",
		"--product-evidence-path", "product_evidence/export-1.json",
		"--release-evidence-id", "re-1",
		"--release-evidence-path", "release_evidence/bundle-1.json",
		"--brief-path", "briefs/test.md",
		"--changed-path", "cmd/idiom/product_workflow.go",
		"--tdd-obligation-ref", "TDD-001",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for symlink path, got %d", code)

	gomega.Expect(strings.Contains(stderr.String(), "must not be a symlink")).To(gomega.BeTrue(), "expected symlink failure, got %s", stderr.String())
})

var _ = ginkgo.It("product feedback blocks privacy markers in references", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "references/lane_history.json", `[{"lane":"implement","event":"closed","status":"pass","timestamp":"2026-05-18T00:00:00Z","note":"raw transcript attached"}]`)

	var stdout, stderr bytes.Buffer

	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--lane-history-file", "references/lane_history.json",
		"--plan-progress-file", "references/plan_progress.json",
		"--verification-file", "references/verification.json",
		"--product-evidence-id", "pe-1",
		"--product-evidence-path", "product_evidence/export-1.json",
		"--release-evidence-id", "re-1",
		"--release-evidence-path", "release_evidence/bundle-1.json",
		"--brief-path", "briefs/test.md",
		"--changed-path", "cmd/idiom/product_workflow.go",
		"--tdd-obligation-ref", "TDD-001",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for privacy marker, got %d", code)

	gomega.Expect(strings.Contains(stderr.String(), "forbidden privacy marker")).To(gomega.BeTrue(), "expected privacy marker failure, got %s", stderr.String())
})

var _ = ginkgo.It("product feedback blocks secret only marker in references", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "references/lane_history.json", `[{"lane":"implement","event":"closed","status":"pass","timestamp":"2026-05-18T00:00:00Z","note":"secret"}]`)

	var stdout, stderr bytes.Buffer

	code := runProductFeedback([]string{
		"--effort-root", dir,
		"--lane-history-file", "references/lane_history.json",
		"--plan-progress-file", "references/plan_progress.json",
		"--verification-file", "references/verification.json",
		"--product-evidence-id", "pe-1",
		"--product-evidence-path", "product_evidence/export-1.json",
		"--release-evidence-id", "re-1",
		"--release-evidence-path", "release_evidence/bundle-1.json",
		"--brief-path", "briefs/test.md",
		"--changed-path", "cmd/idiom/product_workflow.go",
		"--tdd-obligation-ref", "TDD-001",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for secret marker, got %d", code)

	gomega.Expect(strings.Contains(stderr.String(), `forbidden privacy marker "secret"`)).To(gomega.BeTrue(), "expected secret marker failure, got %s", stderr.String())
})
