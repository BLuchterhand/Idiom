package workspace

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	rulescontract "idiom/internal/contracts/codingrules"
	"idiom/internal/platform/config"
)

func TestCodingRulesCheckReturnsStructuredReport(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, ".idiom", "coding_rules.json"), `{
  "schema_version": "idiom.coding_rules.v1",
  "defaults": {
    "enforcement": "blocking",
    "severity": "error"
  },
  "rules": [
    {
      "id": "go.no_pass_through_wrapper",
      "languages": ["go"],
      "kind": "structure"
    }
  ]
}`)
	writeTestFile(t, filepath.Join(root, "internal", "sample", "wrapper.go"), `package sample

type store struct{}

func (store) Save(ctx string, user string) error { return nil }

var repo store

func Save(ctx string, user string) error {
	return repo.Save(ctx, user)
}
`)

	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool: ToolNameCodingRulesCheck,
		Path: filepath.ToSlash(filepath.Join("internal", "sample")),
	})

	if result.Error != "" {
		t.Fatalf("coding_rules_check error: %s", result.Error)
	}
	if result.PayloadType != codingRulesReportPayloadType {
		t.Fatalf("payload type = %q, want %q", result.PayloadType, codingRulesReportPayloadType)
	}
	if !strings.Contains(result.Summary, "fail") || !strings.Contains(result.Summary, "blocking=1") {
		t.Fatalf("summary does not report blocking failure: %q", result.Summary)
	}

	var report rulescontract.Report
	if err := json.Unmarshal(result.Payload, &report); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	if report.Status != rulescontract.StatusFail || report.Summary.Blocking != 1 || len(report.Findings) != 1 {
		t.Fatalf("unexpected report: %#v", report)
	}
	finding := report.Findings[0]
	if finding.RuleID != rulescontract.RuleNoPassThroughWrapper || finding.Symbol != "Save" || finding.Hash == "" {
		t.Fatalf("unexpected finding: %#v", finding)
	}
	if strings.Contains(result.Output, "return repo.Save") {
		t.Fatalf("coding rules report must not include raw source bodies: %s", result.Output)
	}
}
