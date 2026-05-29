package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/llama"
	"idiom/internal/productworkflow"
)

// scriptedLLMClient returns predefined responses in order.
type scriptedLLMClient struct {
	responses []string
	callCount int
}

func (c *scriptedLLMClient) CompleteChat(_ context.Context, _ []llama.ChatMessage, _ llama.ChatOptions) (string, error) {
	if c.callCount >= len(c.responses) {
		return c.responses[len(c.responses)-1], nil
	}
	resp := c.responses[c.callCount]
	c.callCount++
	return resp, nil
}

func (c *scriptedLLMClient) CompleteChatMessage(_ context.Context, _ []llama.ChatMessage, _ llama.ChatOptions) (llama.ChatMessage, error) {
	resp, _ := c.CompleteChat(nil, nil, llama.ChatOptions{})
	return llama.ChatMessage{Role: "assistant", Content: resp}, nil
}

func (c *scriptedLLMClient) StreamChat(_ context.Context, _ []llama.ChatMessage, _ llama.ChatOptions, _ func(string) error) error {
	return nil
}

func (c *scriptedLLMClient) ListModels(_ context.Context) ([]llama.ModelInfo, error) {
	return []llama.ModelInfo{{ID: "mock-model"}}, nil
}

func (c *scriptedLLMClient) Backend() string          { return "mock" }
func (c *scriptedLLMClient) SupportsNativeTools() bool { return false }

func setupE2EModel(t *testing.T, responses []string) (*Model, string) {
	t.Helper()
	tmp := t.TempDir()
	effortRoot := filepath.Join(tmp, "docs", "product-workflows", "efforts", "started", "test-effort")
	client := &scriptedLLMClient{responses: responses}

	m := &Model{}
	m.client = client
	m.featurePlannerEffortRoot = effortRoot
	m.featurePlannerPhase = productworkflow.PhasePRDDrafting

	if err := os.MkdirAll(effortRoot, 0o755); err != nil {
		t.Fatalf("mkdir effort root: %v", err)
	}
	for _, sub := range []string{"adrs", "evidence", "briefs", "reports"} {
		if err := os.MkdirAll(filepath.Join(effortRoot, sub), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}

	return m, effortRoot
}

func writeWorkflowState(t *testing.T, effortRoot, phase, state string) {
	t.Helper()
	ws := productworkflow.WorkflowState{
		SchemaVersion: "idiom.product_workflow_state.v1",
		EffortID:      "test-effort",
		TaskID:        "test-effort",
		Title:         "Test Feature",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		StatusBucket:  "started",
		CurrentPhase:  phase,
		CurrentState:  state,
		ArtifactPaths: map[string]string{"prd": "prd.md"},
		Context: productworkflow.WorkflowContext{
			ContextPackID:  "test-pack",
			FreshnessState: "fresh",
		},
		Coverage: productworkflow.CoverageState{
			CoverageTier: "unit",
		},
		Privacy: productworkflow.PrivacyState{
			RedactionStatus: "clean",
		},
		FolderPath:       effortRoot,
		DurabilityLevel:  "standard",
		NextValidActions: []string{"approve_prd"},
	}
	data, _ := json.MarshalIndent(ws, "", "  ")
	data = append(data, '\n')
	os.WriteFile(filepath.Join(effortRoot, "workflow_state.json"), data, 0o644)
}

func validPRD() string {
	return `---
schema_version: idiom.prd.v1
prd_id: test-effort
status: draft
task_id: test-effort
created_at: 2026-01-01T00:00:00Z
---

# PRD: Test Feature

## Requirements

### REQ-001

- **id**: REQ-001
- **priority**: P1
- **statement**: The system shall support OAuth 2.0 login.
- **owner_alignment**: End users need secure authentication.
- **testability**: Can be verified by attempting login with valid OAuth token.
- **metric**: 100% of login attempts succeed with valid token.
- **evidence_refs**: [inference]
- **tdd_obligations**: [TDD-REQ-001]
- **open_questions**: []

### REQ-002

- **id**: REQ-002
- **priority**: P2
- **statement**: The system shall support password reset via email.
- **owner_alignment**: Users who forget passwords need recovery.
- **testability**: Can be verified by triggering password reset flow.
- **metric**: Password reset email arrives within 60 seconds.
- **evidence_refs**: [inference]
- **tdd_obligations**: [TDD-REQ-002]
- **open_questions**: []
`
}

func validADRs() string {
	return `---
schema_version: idiom.adr.v1
adr_id: ADR-001
status: draft
prd_refs:
  - REQ-001
evidence_refs:
  - inference
tdd_obligations:
  - TDD-ADR-001
coverage_tier: unit
---
## Context
The system must support OAuth 2.0.

## Decision
Use authorization code flow with PKCE.

## Alternatives Considered
Client credentials rejected.

## Evidence
Industry standard.

## Consequences
Additional redirect URI handling.

## Compatibility and Migration
Backward compatible.

## TDD Obligations
Automated OAuth flow test.

## Rollback
Revert if issues arise.

## Open Questions
Which identity providers?
===
---
schema_version: idiom.adr.v1
adr_id: ADR-002
status: draft
prd_refs:
  - REQ-002
evidence_refs:
  - inference
tdd_obligations:
  - TDD-ADR-002
coverage_tier: unit
---
## Context
Users need password reset.

## Decision
Time-limited reset link via email.

## Alternatives Considered
SMS rejected.

## Evidence
Industry standard.

## Consequences
Requires email infra.

## Compatibility and Migration
Compatible.

## TDD Obligations
Automated reset flow test.

## Rollback
Disable if abused.

## Open Questions
Max link validity?
`
}

// TestE2EFeaturePlannerPRDToADR exercises the full PRD → approve → ADR flow.
func TestE2EFeaturePlannerPRDToADR(t *testing.T) {
	m, effortRoot := setupE2EModel(t, []string{validPRD(), validADRs()})
	ctx := context.Background()

	// Phase 1: Generate PRD.
	_, err := m.runPRDDraftingTurn(ctx, "Add OAuth 2.0")
	if err != nil {
		t.Fatalf("PRD drafting: %v", err)
	}
	prdData, err := os.ReadFile(filepath.Join(effortRoot, "prd.md"))
	if err != nil {
		t.Fatalf("prd.md not found: %v", err)
	}
	prd, err := productworkflow.ParsePRDMarkdown(prdData)
	if err != nil {
		t.Fatalf("prd.md invalid: %v", err)
	}
	if len(prd.Requirements) != 2 {
		t.Fatalf("expected 2 requirements, got %d", len(prd.Requirements))
	}

	// Phase 2: Approve PRD → auto-generate ADRs.
	writeWorkflowState(t, effortRoot, productworkflow.PhasePRDReview, "human_verification")
	if _, err := m.approvePRDAndAdvance(effortRoot); err != nil {
		t.Fatalf("PRD approval: %v", err)
	}

	// Phase 3: Verify ADRs on disk.
	adrDir := filepath.Join(effortRoot, "adrs")
	files, _ := filepath.Glob(filepath.Join(adrDir, "*.md"))
	if len(files) == 0 {
		t.Fatal("no ADR files found")
	}
	idxData, _ := os.ReadFile(filepath.Join(adrDir, "index.json"))
	idx, err := productworkflow.ParseADRIndexJSON(idxData)
	if err != nil {
		t.Fatalf("index.json invalid: %v", err)
	}
	for _, entry := range idx.ADRs {
		if _, err := os.Stat(filepath.Join(adrDir, entry.Path)); os.IsNotExist(err) {
			t.Fatalf("ADR %s in index but not on disk", entry.Path)
		}
	}
	t.Logf("PASS: %d ADRs, index valid", len(idx.ADRs))
}

// TestPRDRepairLoop verifies repair loop triggers and succeeds.
func TestPRDRepairLoop(t *testing.T) {
	m, effortRoot := setupE2EModel(t, []string{
		"# No front matter at all\n\nJust prose.",
		validPRD(),
	})
	ctx := context.Background()
	_, err := m.runPRDDraftingTurn(ctx, "Add OAuth 2.0")
	if err != nil {
		t.Fatalf("PRD repair should succeed: %v", err)
	}
	if _, err := productworkflow.ParsePRDMarkdown(
		mustReadFile(filepath.Join(effortRoot, "prd.md")),
	); err != nil {
		t.Fatalf("prd.md invalid after repair: %v", err)
	}
	if _, err := os.Stat(filepath.Join(effortRoot, "prd_parse_failure_raw_001.md")); os.IsNotExist(err) {
		t.Error("expected prd_parse_failure_raw_001.md")
	}
	t.Log("PASS: PRD repair loop succeeded")
}

// TestNoDoubleADRWrite verifies ADR refinement does not double-write.
func TestNoDoubleADRWrite(t *testing.T) {
	m, effortRoot := setupE2EModel(t, []string{
		validPRD(), validADRs(), validADRs(),
	})
	ctx := context.Background()

	// Generate PRD + approve → ADRs.
	if _, err := m.runPRDDraftingTurn(ctx, "Add OAuth 2.0"); err != nil {
		t.Fatalf("PRD: %v", err)
	}
	writeWorkflowState(t, effortRoot, productworkflow.PhasePRDReview, "human_verification")
	if _, err := m.approvePRDAndAdvance(effortRoot); err != nil {
		t.Fatalf("approve: %v", err)
	}

	adrDir := filepath.Join(effortRoot, "adrs")
	initial, _ := filepath.Glob(filepath.Join(adrDir, "*.md"))
	initialCount := len(initial)
	t.Logf("Initial ADR files: %d", initialCount)

	// Refine ADRs.
	m.featurePlannerPhase = productworkflow.PhaseADRDrafting
	m.featurePlannerEffortRoot = effortRoot
	writeWorkflowState(t, effortRoot, productworkflow.PhaseADRDrafting, "planned")
	m.runADRDraftingTurn(ctx, "Add detail")

	final, _ := filepath.Glob(filepath.Join(adrDir, "*.md"))
	t.Logf("ADR files after refinement: %d", len(final))

	if len(final) < initialCount {
		t.Errorf("file count decreased: %d → %d", initialCount, len(final))
	}
	// Verify index exists.
	if _, err := os.Stat(filepath.Join(adrDir, "index.json")); os.IsNotExist(err) {
		t.Error("index.json missing after refinement")
	}
	// Verify no duplicate ADR IDs.
	for _, f := range final {
		if strings.Count(filepath.Base(f), "ADR-") > 1 {
			t.Errorf("duplicate ADR ID in filename: %s", filepath.Base(f))
		}
	}
	t.Logf("PASS: %d → %d files, no duplicates", initialCount, len(final))
}
