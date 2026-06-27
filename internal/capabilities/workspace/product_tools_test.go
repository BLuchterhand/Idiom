package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"idiom/internal/platform/config"
	"idiom/internal/workflows/productworkflow"
)

func TestProductEffortsToolBrowsesReadOnlyEfforts(t *testing.T) {
	root := t.TempDir()
	effortDir := filepath.Join(root, "docs", "product-workflows", "efforts", "started", "sample-effort")

	if err := os.MkdirAll(effortDir, 0o755); err != nil {
		t.Fatalf("mkdir effort: %v", err)
	}

	if err := os.WriteFile(filepath.Join(effortDir, "workflow_state.json"), []byte(productToolWorkflowState("sample-effort", "started", "sample-effort")), 0o644); err != nil {
		t.Fatalf("write workflow_state: %v", err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:   "product_efforts",
		Root:   "docs/product-workflows/efforts",
		Status: "started",
	})

	if result.Error != "" {
		t.Fatalf("product_efforts error = %s output=%s", result.Error, result.Output)
	}

	if !strings.Contains(result.Output, `"effort_id": "sample-effort"`) || !strings.Contains(result.Output, `"status_filter": "started"`) {
		t.Fatalf("unexpected product_efforts output:\n%s", result.Output)
	}
}

func TestProductStatusToolReadsOperatorView(t *testing.T) {
	repo := workspaceTestRepoRoot(t)
	mgr := NewManager(config.Workspace{Root: repo, CWD: repo})
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:         "product_status",
		EffortRoot:   "docs/product-workflows/efforts/closed/prd-adr-brief-workflow",
		OperatorView: true,
	})

	if result.Error != "" {
		t.Fatalf("product_status error = %s output=%s", result.Error, result.Output)
	}

	var payload struct {
		SchemaVersion string `json:"schema_version"`
		EffortID      string `json:"effort_id"`
	}

	if err := json.Unmarshal([]byte(result.Output), &payload); err != nil {
		t.Fatalf("decode product_status output: %v\n%s", err, result.Output)
	}

	if payload.SchemaVersion != "idiom.product_operator_status.v1" || payload.EffortID == "" {
		t.Fatalf("unexpected product_status operator payload: %#v", payload)
	}
}

func TestProductStatusToolPreservesWorkflowNextValidActions(t *testing.T) {
	repo := workspaceTestRepoRoot(t)
	effortRoot := "docs/product-workflows/efforts/closed/prd-adr-brief-workflow"
	wantView, _, err := productworkflow.LoadCheckedProductStatus(filepath.Join(repo, effortRoot))
	if err != nil {
		t.Fatalf("load workflow status: %v", err)
	}

	mgr := NewManager(config.Workspace{Root: repo, CWD: repo})
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:       "product_status",
		EffortRoot: effortRoot,
	})
	if result.Error != "" {
		t.Fatalf("product_status error = %s output=%s", result.Error, result.Output)
	}

	var payload productworkflow.ProductStatusView
	if err := json.Unmarshal([]byte(result.Output), &payload); err != nil {
		t.Fatalf("decode product_status output: %v\n%s", err, result.Output)
	}

	if !reflect.DeepEqual(payload.NextValidActions, wantView.NextValidActions) {
		t.Fatalf("workspace adapter must preserve workflow next actions: got %#v want %#v", payload.NextValidActions, wantView.NextValidActions)
	}
}

func TestProductWorkflowToolsAreReadOnlyDiscoveryTools(t *testing.T) {
	for _, tool := range []string{"product_efforts", "product_status"} {
		if !IsBuiltInToolName(tool) {
			t.Fatalf("%s should be a built-in tool", tool)
		}

		if decision := ToolAllowedForProfile(tool, ToolProfileDiscovery); !decision.Allowed {
			t.Fatalf("%s should be allowed by discovery profile: %#v", tool, decision)
		}

		if len(BuildToolDefinitions([]string{tool})) != 1 {
			t.Fatalf("%s should have a schema definition", tool)
		}
	}
}

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

func workspaceTestRepoRoot(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}

		parent := filepath.Dir(wd)
		if parent == wd {
			t.Fatalf("could not find repo root from %s", wd)
		}

		wd = parent
	}
}
