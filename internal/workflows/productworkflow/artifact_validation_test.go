package productworkflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateProductArtifactRejectsInvalidPRD(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "prd.md")
	writeProductArtifactValidationFile(t, path, `---
schema_version: idiom.prd.v1
prd_id: prd-test
status: draft
task_id: impl-test
created_at: 2026-05-17T00:00:00Z
---

# PRD: Test

## Requirements

| id | priority | statement |
| --- | --- | --- |
| REQ-1 | must | do it |
`)

	result := ValidateProductArtifact(ProductArtifactValidationOptions{
		Kind: "prd",
		Path: path,
	})

	if result.Status != ProductWorkflowStatusFail || !strings.Contains(result.Error, "PRD has no requirements") {
		t.Fatalf("expected parser failure, got %#v", result)
	}
}

func TestValidateProductArtifactRejectsUnknownADRTDDObligation(t *testing.T) {
	root := t.TempDir()
	prdPath := filepath.Join(root, "prd.md")
	tddPath := filepath.Join(root, "tdd.json")
	adrPath := filepath.Join(root, "adr.md")

	writeProductArtifactValidationFile(t, prdPath, RenderPRDMarkdown(validPRDDoc()))
	writeProductArtifactValidationJSON(t, tddPath, TDDLedger{
		SchemaVersion: "idiom.tdd_obligations.v2.fixture",
		EffortID:      "test-effort",
		TaskID:        "task-001",
		CoverageTier:  "unit",
		Coverage:      TDDCoverage{MutationMinScore: 80},
		Obligations: []TDDObligation{{
			ID:             "T1",
			Type:           "acceptance",
			Behavior:       "test",
			Owner:          "test",
			Command:        "go test",
			RequiredBefore: "close",
			Status:         "pending",
		}},
	})

	adr := validADRDoc()
	adr.FrontMatter.TDDObligations = []string{"missing-generated-obligation"}
	writeProductArtifactValidationFile(t, adrPath, RenderADRMarkdown(adr))

	result := ValidateProductArtifact(ProductArtifactValidationOptions{
		Kind:          "adr",
		Path:          adrPath,
		PRDPath:       prdPath,
		TDDLedgerPath: tddPath,
	})

	if result.Status != ProductWorkflowStatusFail || !strings.Contains(result.Error, "tdd_obligation") {
		t.Fatalf("expected ADR TDD cross-link failure, got %#v", result)
	}
}

func writeProductArtifactValidationFile(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeProductArtifactValidationJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	writeProductArtifactValidationFile(t, path, string(data))
}
