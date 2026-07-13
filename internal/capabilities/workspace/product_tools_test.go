package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/platform/config"
	"idiom/internal/workflows/productworkflow"
)

var _ = ginkgo.Describe("product tools", func() {
	ginkgo.It("product efforts tool browses read only efforts", func() {
		root := ginkgo.GinkgoT().TempDir()
		effortDir := filepath.Join(root, "docs", "product-workflows", "efforts", "started", "sample-effort")
		gomega.Expect(os.MkdirAll(effortDir, 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(effortDir, "workflow_state.json"), []byte(productToolWorkflowState("sample-effort", "started", "sample-effort")), 0o644)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:   "product_efforts",
			Root:   "docs/product-workflows/efforts",
			Status: "started",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("product_efforts error = %s output=%s", result.Error, result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring(`"effort_id": "sample-effort"`), fmt.Sprintf("unexpected product_efforts output:\n%s", result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring(`"status_filter": "started"`), fmt.Sprintf("unexpected product_efforts output:\n%s", result.Output))

	})
	ginkgo.It("product status tool reads operator view", func() {
		repo := workspaceTestRepoRoot()
		mgr := NewManager(config.Workspace{Root: repo, CWD: repo})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:         "product_status",
			EffortRoot:   "testdata/product-workflows/prd-adr-brief-workflow",
			OperatorView: true,
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("product_status error = %s output=%s", result.Error, result.Output))

		var payload struct {
			SchemaVersion string `json:"schema_version"`
			EffortID      string `json:"effort_id"`
		}
		gomega.Expect(json.Unmarshal([]byte(result.Output), &payload)).To(gomega.Succeed())
		gomega.Expect(payload.SchemaVersion).To(gomega.Equal("idiom.product_operator_status.v1"), fmt.Sprintf("unexpected product_status operator payload: %#v", payload))
		gomega.Expect(payload.EffortID).NotTo(gomega.BeEmpty(), fmt.Sprintf("unexpected product_status operator payload: %#v", payload))

	})
	ginkgo.It("product status tool preserves workflow next valid actions", func() {
		repo := workspaceTestRepoRoot()
		effortRoot := "testdata/product-workflows/prd-adr-brief-workflow"
		wantView, _, err := productworkflow.LoadCheckedProductStatus(filepath.Join(repo, effortRoot))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load workflow status: %v", err))

		mgr := NewManager(config.Workspace{Root: repo, CWD: repo})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:       "product_status",
			EffortRoot: effortRoot,
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("product_status error = %s output=%s", result.Error, result.Output))

		var payload productworkflow.ProductStatusView
		gomega.Expect(json.Unmarshal([]byte(result.Output), &payload)).To(gomega.Succeed())
		gomega.Expect(reflect.DeepEqual(payload.NextValidActions, wantView.NextValidActions)).To(gomega.BeTrue(), fmt.Sprintf("workspace adapter must preserve workflow next actions: got %#v want %#v", payload.NextValidActions, wantView.NextValidActions))

	})
	ginkgo.It("product workflow tools are read only discovery tools", func() {
		for _, tool := range []string{"product_efforts", "product_status"} {
			gomega.Expect(IsBuiltInToolName(tool)).To(gomega.BeTrue(), fmt.Sprintf("%s should be a built-in tool", tool))

			if decision := ToolAllowedForProfile(tool, ToolProfileDiscovery); !decision.Allowed {
				ginkgo.Fail(fmt.Sprintf("%s should be allowed by discovery profile: %#v", tool, decision))
			}
			gomega.Expect(BuildToolDefinitions([]string{tool})).To(gomega.HaveLen(1), fmt.Sprintf("%s should have a schema definition", tool))

		}
	})
})

func productToolWorkflowState(effortID, bucket, folderName string) string {
	return `{
		"schema_version":"idiom.product_workflow_state.v0.fixture",
		"effort_id":"` + effortID + `",
		"task_id":"impl-test",
		"title":"Sample Effort",
		"status_bucket":"` + bucket + `",
		"current_phase":"phase-1",
		"current_state":"implementing",
		"state_revision":7,
		"last_event_id":"evt-007",
		"artifact_paths":{"events":"events.jsonl"},
		"context":{"context_pack_id":"pack-` + effortID + `","freshness_state":"current"},
		"coverage":{"coverage_tier":"unit"},
		"open_obligations":["TDD-001"],
		"blockers":["blocked-a"],
		"privacy":{"redaction_status":"applied"},
		"folder_path":"docs/product-workflows/efforts/` + bucket + `/` + folderName + `",
		"next_valid_actions":["submit_for_verification"],
		"durability_level":"source_controlled"
	}`
}

func workspaceTestRepoRoot() string {
	ginkgo.GinkgoHelper()

	wd, err := os.Getwd()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("getwd: %v", err))

	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}

		parent := filepath.Dir(wd)
		gomega.Expect(parent).NotTo(gomega.Equal(wd), fmt.Sprintf("could not find repo root from %s", wd))

		wd = parent
	}
}
