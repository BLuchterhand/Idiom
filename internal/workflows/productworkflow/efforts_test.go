package productworkflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("efforts", func() {
	ginkgo.It("ProductEffortsBrowserListsSearchesAndCountsByStatus", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeEffortsTestState(root, "started", "beta", effortsTestWorkflowState("beta-effort", "Beta Work", "started", "implementing", "beta"))
		writeEffortsTestState(root, "unstarted", "alpha", effortsTestWorkflowState("alpha-effort", "Alpha Work", "unstarted", "planned", "alpha"))
		writeEffortsTestState(root, "closed", "done", effortsTestWorkflowState("closed-effort", "Closed Work", "closed", "closed", "done"))

		result, err := BrowseProductEfforts(ProductEffortsOptions{Root: root})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected browse error: %v", err))
		gomega.Expect(result.Status != "pass").To(gomega.BeFalse(), fmt.Sprintf("expected pass, got %#v", result))
		gomega.Expect(len(result.Efforts) != 3).To(gomega.BeFalse(), fmt.Sprintf("expected 3 efforts, got %#v", result.Efforts))
		{

			got := []string{result.Efforts[0].EffortID, result.Efforts[1].EffortID, result.Efforts[2].EffortID}
			gomega.Expect(strings.Join(got, ",") != "alpha-effort,beta-effort,closed-effort").To(gomega.BeFalse(), fmt.Sprintf("expected deterministic bucket/id order, got %#v", got))
		}
		gomega.Expect(result.Counts.Unstarted).To(gomega.Equal(1), fmt.Sprintf("unexpected counts: %#v", result.Counts))
		gomega.Expect(result.Counts.Started).To(gomega.Equal(1), fmt.Sprintf("unexpected counts: %#v", result.Counts))
		gomega.Expect(result.Counts.Closed).To(gomega.Equal(1), fmt.Sprintf("unexpected counts: %#v", result.Counts))
		gomega.Expect(result.Counts.EffortCount).To(gomega.Equal(3), fmt.Sprintf("unexpected counts: %#v", result.Counts))

		started := result.Efforts[1]
		gomega.Expect(started.ContextPackID).To(gomega.Equal("pack-beta-effort"), fmt.Sprintf("expected context summary, got %#v", started))
		gomega.Expect(started.FreshnessState).To(gomega.Equal("current"), fmt.Sprintf("expected context summary, got %#v", started))
		gomega.Expect(started.BlockerCount).To(gomega.Equal(2), fmt.Sprintf("expected blocker and obligation counts, got %#v", started))
		gomega.Expect(started.OpenObligationCount).To(gomega.Equal(1), fmt.Sprintf("expected blocker and obligation counts, got %#v", started))
		gomega.Expect(started.RelativeFolderPath != "started/beta").To(gomega.BeFalse(), fmt.Sprintf("expected relative folder path, got %q", started.RelativeFolderPath))

		filtered, err := BrowseProductEfforts(ProductEffortsOptions{Root: root, Status: "started", Query: "beta"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected filtered browse error: %v", err))
		gomega.Expect(filtered.Efforts).To(gomega.HaveLen(1), fmt.Sprintf("expected query to select beta effort, got %#v", filtered.Efforts))
		gomega.Expect(filtered.Efforts[0].EffortID).To(gomega.Equal("beta-effort"), fmt.Sprintf("expected query to select beta effort, got %#v", filtered.Efforts))

	})
	ginkgo.It("ProductEffortsBrowserFindsMissingAndMalformedWorkflowState", func() {
		root := ginkgo.GinkgoT().TempDir()
		{
			err := os.MkdirAll(filepath.Join(root, "started", "missing"), 0o755)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprint(err))
		}

		writeEffortsTestFile(root, filepath.Join("started", "bad", "workflow_state.json"), `{not valid json`)
		writeEffortsTestState(root, "started", "invalid-state", effortsTestWorkflowState("invalid-state-effort", "Invalid State", "started", "nonsense", "invalid-state"))

		mismatched := strings.Replace(effortsTestWorkflowState("mismatch-effort", "Mismatch Work", "started", "implementing", "mismatch"), `"folder_path":"docs/product-workflows/efforts/started/mismatch"`, `"folder_path":"docs/product-workflows/efforts/started/other-effort"`, 1)
		writeEffortsTestFile(root, filepath.Join("started", "mismatch", "workflow_state.json"), mismatched)

		result, err := BrowseProductEfforts(ProductEffortsOptions{Root: root, Status: "started"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("malformed efforts should be reported as findings, not usage errors: %v", err))
		gomega.Expect(result.Status != "fail").To(gomega.BeFalse(), fmt.Sprintf("expected fail, got %#v", result))
		gomega.Expect(len(result.Findings) != 4).To(gomega.BeFalse(), fmt.Sprintf("expected four findings, got %#v", result.Findings))

		codes := []string{result.Findings[0].Code, result.Findings[1].Code, result.Findings[2].Code, result.Findings[3].Code}
		gomega.Expect(strings.Join(codes, ",") != "invalid_workflow_state,invalid_workflow_state,invalid_workflow_state,missing_workflow_state").To(gomega.BeFalse(), fmt.Sprintf("expected typed malformed-state findings, got %#v", result.Findings))
		gomega.Expect(!containsEffortsTestFinding(result.Findings, "invalid_workflow_state", "started/invalid-state/workflow_state.json")).To(gomega.BeFalse(), fmt.Sprintf("expected invalid current_state finding, got %#v", result.Findings))
		gomega.Expect(!containsEffortsTestFinding(result.Findings, "invalid_workflow_state", "started/mismatch/workflow_state.json")).To(gomega.BeFalse(), fmt.Sprintf("expected folder_path mismatch finding, got %#v", result.Findings))
		gomega.Expect(result.Counts.FindingCount).To(gomega.Equal(4), fmt.Sprintf("unexpected counts: %#v", result.Counts))
		gomega.Expect(result.Counts.EffortCount).To(gomega.Equal(0), fmt.Sprintf("unexpected counts: %#v", result.Counts))
		gomega.Expect(!strings.Contains(result.Findings[0].ID, result.Findings[0].Code+":")).To(gomega.BeFalse(), fmt.Sprintf("expected typed finding identity, got %#v", result.Findings[0]))

	})
	ginkgo.It("ProductEffortsBrowserRejectsUnsafeRootAndUnknownStatus", func() {
		{
			_, err := BrowseProductEfforts(ProductEffortsOptions{Root: filepath.Join("..", "efforts")})
			gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected unsafe traversal root to fail"))
		}

		root := ginkgo.GinkgoT().TempDir()

		result, err := BrowseProductEfforts(ProductEffortsOptions{Root: root, Status: "later"})
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected unknown status to fail"))
		gomega.Expect(result.Status).To(gomega.Equal(ProductWorkflowStatusFail), fmt.Sprintf("expected typed invalid status finding, got %#v", result))
		gomega.Expect(result.Findings).To(gomega.HaveLen(1), fmt.Sprintf("expected typed invalid status finding, got %#v", result))
		gomega.Expect(result.Findings[0].Code).To(gomega.Equal("invalid_status_filter"), fmt.Sprintf("expected typed invalid status finding, got %#v", result))

	})
	ginkgo.It("HasPathTraversalTreatsBackslashAsSeparator", func() {
		gomega.Expect(!hasPathTraversal(`..\efforts`)).To(gomega.BeFalse(), fmt.Sprint("expected leading backslash traversal to be rejected"))
		gomega.Expect(!hasPathTraversal(`started\..\closed`)).To(gomega.BeFalse(), fmt.Sprint("expected nested backslash traversal to be rejected"))
		gomega.Expect(hasPathTraversal(`started\test-effort`)).To(gomega.BeFalse(), fmt.Sprint("expected ordinary backslash-separated effort path to be accepted"))

	})
	ginkgo.It("ProductEffortsBrowserJSONRoundTripOnFindings", func() {
		root := ginkgo.GinkgoT().TempDir()
		{
			err := os.MkdirAll(filepath.Join(root, "started", "missing"), 0o755)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprint(err))
		}

		result, err := BrowseProductEfforts(ProductEffortsOptions{Root: root})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected browse error: %v", err))

		out, err := json.Marshal(result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("result must marshal: %v", err))

		var parsed ProductEffortsResult
		{

			err := json.Unmarshal(out, &parsed)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("result must round-trip: %v", err))
		}
		gomega.Expect(parsed.Status).To(gomega.Equal(ProductWorkflowStatusFail), fmt.Sprintf("unexpected parsed result: %#v", parsed))
		gomega.Expect(parsed.Findings[0].Code).To(gomega.Equal("missing_workflow_state"), fmt.Sprintf("unexpected parsed result: %#v", parsed))

	})
})

func containsEffortsTestFinding(findings []ProductEffortFinding, code, path string) bool {
	for _, finding := range findings {
		if finding.Code == code && finding.Path == path {
			return true
		}
	}

	return false
}

func writeEffortsTestState(root, bucket, name, content string) {
	ginkgo.GinkgoHelper()
	writeEffortsTestFile(root, filepath.Join(bucket, name, "workflow_state.json"), content)
}

func writeEffortsTestFile(root, relPath, content string) {
	ginkgo.GinkgoHelper()

	fullPath := filepath.Join(root, relPath)
	{
		err := os.MkdirAll(filepath.Dir(fullPath), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir %s: %v", filepath.Dir(fullPath), err))
	}
	{

		err := os.WriteFile(fullPath, []byte(content), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write %s: %v", fullPath, err))
	}

}

func effortsTestWorkflowState(effortID, title, bucket, state, folderName string) string {
	return `{
		"schema_version":"idiom.product_workflow_state.v0.fixture",
		"effort_id":"` + effortID + `",
		"task_id":"impl-test",
		"title":"` + title + `",
		"status_bucket":"` + bucket + `",
		"current_phase":"phase-1",
		"current_state":"` + state + `",
		"state_revision":7,
		"last_event_id":"evt-007",
		"artifact_paths":{"events":"events.jsonl"},
		"context":{"context_pack_id":"pack-` + effortID + `","freshness_state":"current"},
		"coverage":{"coverage_tier":"unit"},
		"open_obligations":["TDD-001"],
		"blockers":["blocked-a","blocked-b"],
		"privacy":{"redaction_status":"applied"},
		"folder_path":"docs/product-workflows/efforts/` + bucket + `/` + folderName + `",
		"next_valid_actions":["submit_for_verification"],
		"durability_level":"source_controlled"
	}`
}
