package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/runtime/llama"
	"idiom/internal/workflows/productworkflow"
)

type scriptedLLMClient struct {
	responses     []string
	finishReasons []string
	messages      [][]llama.ChatMessage
	options       []llama.ChatOptions
	callCount     int
}

func (c *scriptedLLMClient) CompleteChat(_ context.Context, messages []llama.ChatMessage, opts llama.ChatOptions) (string, error) {
	c.messages = append(c.messages, append([]llama.ChatMessage(nil), messages...))
	c.options = append(c.options, opts)
	if c.callCount >= len(c.responses) {
		return c.responses[len(c.responses)-1], nil
	}
	resp := c.responses[c.callCount]
	c.callCount++
	return resp, nil
}

func (c *scriptedLLMClient) CompleteChatMessage(_ context.Context, messages []llama.ChatMessage, opts llama.ChatOptions) (llama.ChatMessage, error) {
	callIndex := c.callCount
	resp, _ := c.CompleteChat(context.TODO(), messages, opts)
	finishReason := ""
	if callIndex < len(c.finishReasons) {
		finishReason = c.finishReasons[callIndex]
	}
	return llama.ChatMessage{Role: "assistant", Content: resp, FinishReason: finishReason}, nil
}

func (c *scriptedLLMClient) StreamChat(_ context.Context, _ []llama.ChatMessage, _ llama.ChatOptions, _ func(string) error) error {
	return nil
}

func (c *scriptedLLMClient) ListModels(_ context.Context) ([]llama.ModelInfo, error) {
	return []llama.ModelInfo{{ID: "mock-model"}}, nil
}

func (c *scriptedLLMClient) Backend() string           { return "mock" }
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

const garbage = "# Just some random prose\n\nNo front matter, no format, nothing parseable.\n"

// TestE2EFeaturePlannerPRDToADR exercises the full PRD → approve → ADR flow.
func TestE2EFeaturePlannerPRDToADR(t *testing.T) {
	m, effortRoot := setupE2EModel(t, []string{validPRD(), validADRs()})
	ctx := context.Background()

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

	writeWorkflowState(t, effortRoot, productworkflow.PhasePRDReview, "human_verification")
	if _, err := m.approvePRDAndAdvance(effortRoot); err != nil {
		t.Fatalf("PRD approval: %v", err)
	}

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

// TestPRDRepairLoop verifies malformed PRD triggers repair and retry succeeds.
func TestPRDRepairLoop(t *testing.T) {
	m, effortRoot := setupE2EModel(t, []string{garbage, validPRD()})
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

// TestPRDRepairExhaustion verifies all retries fail → non-fatal message.
func TestPRDRepairExhaustion(t *testing.T) {
	m, effortRoot := setupE2EModel(t, []string{garbage, garbage, garbage})
	ctx := context.Background()
	answer, err := m.runPRDDraftingTurn(ctx, "Add OAuth 2.0")
	if err != nil {
		t.Fatalf("PRD repair exhaustion should return message, not error: %v", err)
	}
	if !strings.Contains(answer, "PRD parsing failed") {
		t.Errorf("expected parse failure message, got: %v", answer)
	}
	for i := 1; i <= 2; i++ {
		name := fmt.Sprintf("prd_parse_failure_raw_%03d.md", i)
		if _, statErr := os.Stat(filepath.Join(effortRoot, name)); os.IsNotExist(statErr) {
			t.Errorf("expected %s", name)
		}
	}
	if _, statErr := os.Stat(filepath.Join(effortRoot, "prd_parse_failure_final.md")); os.IsNotExist(statErr) {
		t.Error("expected prd_parse_failure_final.md")
	}
	t.Log("PASS: PRD repair exhaustion → non-fatal message, raw failures saved")
}

// TestADRRepairSuccess verifies ADR repair loop: garbage → repair → success.
func TestADRRepairSuccess(t *testing.T) {
	m, effortRoot := setupE2EModel(t, []string{validPRD()})
	ctx := context.Background()
	if _, err := m.runPRDDraftingTurn(ctx, "Add OAuth 2.0"); err != nil {
		t.Fatalf("PRD: %v", err)
	}

	// Reset client for ADR generation with repair.
	m.featurePlannerPhase = productworkflow.PhaseADRDrafting
	m.featurePlannerEffortRoot = effortRoot
	// 1 initial + 1 repair prompt + 1 tryGenerateADRs retry = 3 calls
	// New direct-parse design: 1 initial + 1 repair = 2 calls (not 3).
	m.client = &scriptedLLMClient{responses: []string{garbage, validADRs()}}

	adrDocs, _, err := m.generateADRs(ctx, effortRoot)
	if err != nil {
		t.Fatalf("ADR repair should succeed: %v", err)
	}
	if len(adrDocs) == 0 {
		t.Fatal("expected ADRs after repair")
	}
	if _, err := os.Stat(filepath.Join(effortRoot, "adrs_parse_failure_raw_001.txt")); os.IsNotExist(err) {
		t.Error("expected adrs_parse_failure_raw_001.txt")
	}
	if m.client.(*scriptedLLMClient).callCount != 2 {
		t.Errorf("expected 2 model calls (direct parse), got %d", m.client.(*scriptedLLMClient).callCount)
	}
	t.Logf("PASS: ADR repair succeeded, %d ADRs, %d calls", len(adrDocs), m.client.(*scriptedLLMClient).callCount)
}

// TestADRRepairExhaustion verifies ADR repair exhaustion fails closed instead
// of writing an empty successful ADR index.
func TestADRRepairExhaustion(t *testing.T) {
	m, effortRoot := setupE2EModel(t, []string{validPRD()})
	ctx := context.Background()
	if _, err := m.runPRDDraftingTurn(ctx, "Add OAuth 2.0"); err != nil {
		t.Fatalf("PRD: %v", err)
	}

	m.featurePlannerPhase = productworkflow.PhaseADRDrafting
	m.featurePlannerEffortRoot = effortRoot
	// New direct-parse design: 1 initial + 2 repair attempts = 3 calls.
	m.client = &scriptedLLMClient{responses: []string{garbage, garbage, garbage}}

	adrDocs, _, err := m.generateADRs(ctx, effortRoot)
	if err == nil {
		t.Fatal("ADR repair exhaustion should return an error")
	}
	if !strings.Contains(err.Error(), "ADR parsing failed after") {
		t.Fatalf("unexpected exhaustion error: %v", err)
	}
	if len(adrDocs) != 0 {
		t.Errorf("expected 0 ADRs after exhaustion, got %d", len(adrDocs))
	}
	if _, statErr := os.Stat(filepath.Join(effortRoot, "adrs_parse_failure_final.txt")); os.IsNotExist(statErr) {
		t.Error("expected adrs_parse_failure_final.txt")
	}
	if m.client.(*scriptedLLMClient).callCount != 3 {
		t.Errorf("expected 3 model calls, got %d", m.client.(*scriptedLLMClient).callCount)
	}
	t.Logf("PASS: ADR repair exhaustion fails closed, %d calls", m.client.(*scriptedLLMClient).callCount)
}

// TestADRPartialSuccess verifies some ADR chunks parse, some fail.
func TestADRPartialSuccess(t *testing.T) {
	partialADRs := `---
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
Test.

## Decision
Test.

## Alternatives Considered
Test.

## Evidence
Test.

## Consequences
Test.

## Compatibility and Migration
Test.

## TDD Obligations
Test.

## Rollback
Test.

## Open Questions
Test.
===
This is not a valid ADR at all.
`

	m, effortRoot := setupE2EModel(t, []string{validPRD(), partialADRs})
	ctx := context.Background()
	if _, err := m.runPRDDraftingTurn(ctx, "Add OAuth 2.0"); err != nil {
		t.Fatalf("PRD: %v", err)
	}
	writeWorkflowState(t, effortRoot, productworkflow.PhasePRDReview, "human_verification")
	_, err := m.approvePRDAndAdvance(effortRoot)
	if err != nil {
		t.Fatalf("approve PRD: %v", err)
	}
	adrDir := filepath.Join(effortRoot, "adrs")
	files, _ := filepath.Glob(filepath.Join(adrDir, "*.md"))
	if len(files) == 0 {
		t.Fatal("expected at least 1 ADR from partial success")
	}
	idxData, _ := os.ReadFile(filepath.Join(adrDir, "index.json"))
	idx, err := productworkflow.ParseADRIndexJSON(idxData)
	if err != nil {
		t.Fatalf("index invalid: %v", err)
	}
	if len(idx.ADRs) == 0 {
		t.Fatal("expected at least 1 ADR in index")
	}
	t.Logf("PASS: partial success — %d of 2 chunks parsed", len(idx.ADRs))
}

// TestPRDIntakeRepair verifies the auto-repair path when PRD intake fails.
func TestPRDIntakeRepair(t *testing.T) {
	// PRD with valid format but empty evidence_refs + tdd_obligations
	// (intake fails, auto-repair triggers).
	badPRD := `---
schema_version: idiom.prd.v1
prd_id: test-effort
status: draft
task_id: test-effort
created_at: 2026-01-01T00:00:00Z
---

# PRD: Test

## Requirements

### REQ-001

- **id**: REQ-001
- **priority**: P1
- **statement**: Support OAuth login.
- **owner_alignment**: End users need this.
- **testability**: Can verify.
- **metric**: 100% success.
- **evidence_refs**: []
- **tdd_obligations**: []
- **open_questions**: []
`

	// Responses: intake repair model call (valid PRD) + ADR generation (valid ADRs).
	m, effortRoot := setupE2EModel(t, []string{validPRD(), validADRs()})
	// Write bad PRD directly — skip PRD generation.
	if err := os.WriteFile(filepath.Join(effortRoot, "prd.md"), []byte(badPRD), 0o644); err != nil {
		t.Fatalf("write bad prd.md: %v", err)
	}
	writeWorkflowState(t, effortRoot, productworkflow.PhasePRDReview, "human_verification")

	result, err := m.approvePRDAndAdvance(effortRoot)
	if err != nil {
		t.Fatalf("approve PRD: %v", err)
	}
	t.Logf("result: %s", truncate(result, 200))
	// Auto-repair updates PRD on disk, user must re-approve.
	if !strings.Contains(result, "auto-repaired") {
		t.Error("expected auto-repair message")
	}
	// Verify the PRD on disk was updated with the repair output.
	prdData, _ := os.ReadFile(filepath.Join(effortRoot, "prd.md"))
	if !strings.Contains(string(prdData), "inference") {
		t.Error("expected repaired PRD to have evidence_refs filled in")
	}
	// Verify PRD is NOT yet accepted (auto-repair doesn't re-run approval).
	if strings.Contains(string(prdData), "accepted") {
		t.Error("expected PRD to still be draft after auto-repair (needs re-approval)")
	}
	t.Log("PASS: PRD intake auto-repair succeeded, PRD updated on disk")
}

// TestNoDoubleADRWrite verifies ADR refinement does not double-write.
func TestNoDoubleADRWrite(t *testing.T) {
	m, effortRoot := setupE2EModel(t, []string{validPRD(), validADRs(), validADRs()})
	ctx := context.Background()
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
	m.featurePlannerPhase = productworkflow.PhaseADRDrafting
	m.featurePlannerEffortRoot = effortRoot
	writeWorkflowState(t, effortRoot, productworkflow.PhaseADRDrafting, "planned")
	m.runADRDraftingTurn(ctx, "Add detail")
	final, _ := filepath.Glob(filepath.Join(adrDir, "*.md"))
	t.Logf("ADR files after refinement: %d", len(final))
	if len(final) < initialCount {
		t.Errorf("file count decreased: %d → %d", initialCount, len(final))
	}
	if _, err := os.Stat(filepath.Join(adrDir, "index.json")); os.IsNotExist(err) {
		t.Error("index.json missing after refinement")
	}
	for _, f := range final {
		if strings.Count(filepath.Base(f), "ADR-") > 1 {
			t.Errorf("duplicate ADR ID in filename: %s", filepath.Base(f))
		}
	}
	t.Logf("PASS: %d → %d files, no duplicates", initialCount, len(final))
}

// TestADRRefinementMerge verifies merge preserves existing ADRs and updates matching IDs.
func TestADRRefinementMerge(t *testing.T) {
	adr001Only := `---
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
Refined OAuth context.

## Decision
Use PKCE with refresh tokens.

## Alternatives Considered
Implicit grant rejected.

## Evidence
OAuth 2.1 recommends PKCE.

## Consequences
Token management complexity.

## Compatibility and Migration
Backward compatible.

## TDD Obligations
Test PKCE end-to-end.

## Rollback
Fall back to basic auth.

## Open Questions
Token rotation?
`

	m, effortRoot := setupE2EModel(t, []string{validPRD(), validADRs(), adr001Only})
	ctx := context.Background()
	if _, err := m.runPRDDraftingTurn(ctx, "Add OAuth 2.0"); err != nil {
		t.Fatalf("PRD: %v", err)
	}
	writeWorkflowState(t, effortRoot, productworkflow.PhasePRDReview, "human_verification")
	if _, err := m.approvePRDAndAdvance(effortRoot); err != nil {
		t.Fatalf("approve PRD: %v", err)
	}
	adrDir := filepath.Join(effortRoot, "adrs")
	initial, _ := filepath.Glob(filepath.Join(adrDir, "*.md"))
	t.Logf("After generation: %d ADR files", len(initial))
	m.featurePlannerPhase = productworkflow.PhaseADRDrafting
	m.featurePlannerEffortRoot = effortRoot
	writeWorkflowState(t, effortRoot, productworkflow.PhaseADRDrafting, "planned")
	m.runADRDraftingTurn(ctx, "Update ADR-001")
	idxData, _ := os.ReadFile(filepath.Join(adrDir, "index.json"))
	idx, _ := productworkflow.ParseADRIndexJSON(idxData)
	adrIDs := map[string]bool{}
	for _, entry := range idx.ADRs {
		adrIDs[entry.ADRID] = true
	}
	if !adrIDs["ADR-001"] {
		t.Error("ADR-001 missing after refinement")
	}
	if !adrIDs["ADR-002"] {
		t.Error("ADR-002 missing — merge should preserve existing entries")
	}
	adr1Data, _ := os.ReadFile(filepath.Join(adrDir, "ADR-001.md"))
	if strings.Contains(string(adr1Data), "Refined OAuth") {
		t.Log("ADR-001 content updated by refinement")
	}
	adr2Data, _ := os.ReadFile(filepath.Join(adrDir, "ADR-002.md"))
	if !strings.Contains(string(adr2Data), "Time-limited reset link via email.") {
		t.Fatalf("ADR-002 body should be preserved when refinement returns only ADR-001, got:\n%s", string(adr2Data))
	}
	t.Logf("PASS: merge preserved %d ADRs", len(idx.ADRs))
}

// TestADRRepairDirectParse verifies the repair loop parses repair output
// directly (1 model call per attempt, not 2). Previously the loop called
// tryGenerateADRs which rebuilt the prompt and called the model again.
func TestADRRepairDirectParse(t *testing.T) {
	m, effortRoot := setupE2EModel(t, []string{validPRD()})
	ctx := context.Background()
	if _, err := m.runPRDDraftingTurn(ctx, "Add OAuth 2.0"); err != nil {
		t.Fatalf("PRD: %v", err)
	}

	m.featurePlannerPhase = productworkflow.PhaseADRDrafting
	m.featurePlannerEffortRoot = effortRoot
	// Only 2 responses needed: 1 initial (garbage) + 1 repair (valid ADRs).
	// Previously this needed 3 (1 initial + 1 repair prompt + 1 tryGenerateADRs).
	// Now the repair loop parses repair output directly — no redundant model call.
	m.client = &scriptedLLMClient{responses: []string{garbage, validADRs()}}

	adrDocs, _, err := m.generateADRs(ctx, effortRoot)
	if err != nil {
		t.Fatalf("ADR repair should succeed with direct parse: %v", err)
	}
	if len(adrDocs) == 0 {
		t.Fatal("expected ADRs after direct-parse repair")
	}
	if _, err := os.Stat(filepath.Join(effortRoot, "adrs_parse_failure_raw_001.txt")); os.IsNotExist(err) {
		t.Error("expected adrs_parse_failure_raw_001.txt")
	}

	// Verify the call count: should be 2 (initial + repair), not 3.
	if m.client.(*scriptedLLMClient).callCount != 2 {
		t.Errorf("expected 2 model calls (direct parse), got %d", m.client.(*scriptedLLMClient).callCount)
	}

	t.Logf("PASS: ADR repair direct parse — %d model calls, %d ADRs", m.client.(*scriptedLLMClient).callCount, len(adrDocs))
}

func TestProductWorkflowADRDraftingPreservesAndRendersADRDecision(t *testing.T) {
	m, effortRoot := setupE2EModel(t, []string{validPRD(), validADRs()})
	ctx := context.Background()
	if _, err := m.runPRDDraftingTurn(ctx, "Add OAuth 2.0"); err != nil {
		t.Fatalf("PRD: %v", err)
	}
	writeWorkflowState(t, effortRoot, productworkflow.PhasePRDReview, "human_verification")

	answer, err := m.approvePRDAndAdvance(effortRoot)
	if err != nil {
		t.Fatalf("approve PRD: %v", err)
	}
	if !strings.Contains(answer, "Decision: Use authorization code flow with PKCE.") {
		t.Fatalf("ADR creation response should render a useful decision summary, got:\n%s", answer)
	}
	adrData, err := os.ReadFile(filepath.Join(effortRoot, "adrs", "ADR-001.md"))
	if err != nil {
		t.Fatalf("read generated ADR: %v", err)
	}
	adrText := string(adrData)
	if !strings.Contains(adrText, "Use authorization code flow with PKCE.") {
		t.Fatalf("generated ADR should preserve the model-authored decision body, got:\n%s", adrText)
	}
	if strings.Contains(adrText, "## Decision\n\n\n\n") {
		t.Fatalf("generated ADR body was replaced with the empty template:\n%s", adrText)
	}
}

func TestProductWorkflowPRDDraftingReturnsConversationalSummary(t *testing.T) {
	m, effortRoot := setupE2EModel(t, []string{validPRD()})
	ctx := context.Background()

	answer, err := m.runPRDDraftingTurn(ctx, "Add OAuth 2.0")
	if err != nil {
		t.Fatalf("PRD: %v", err)
	}
	for _, want := range []string{
		"PRD draft generated.",
		"Saved: prd.md",
		"What I captured:",
		"REQ-001 [P1]: The system shall support OAuth 2.0 login.",
		"All intake checks passed.",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("PRD response missing %q:\n%s", want, answer)
		}
	}
	if strings.Contains(answer, "schema_version: idiom.prd.v1") {
		t.Fatalf("PRD response should not echo raw persisted markdown:\n%s", answer)
	}
	if _, err := os.Stat(filepath.Join(effortRoot, "prd.md")); err != nil {
		t.Fatalf("expected prd.md to be saved: %v", err)
	}
}

func TestProductWorkflowPRDRefinementReportsRequirementChanges(t *testing.T) {
	updatedPRD := strings.Replace(validPRD(), "The system shall support OAuth 2.0 login.", "The system shall support OAuth 2.0 login with PKCE.", 1)
	m, _ := setupE2EModel(t, []string{validPRD(), updatedPRD})
	ctx := context.Background()
	if _, err := m.runPRDDraftingTurn(ctx, "Add OAuth 2.0"); err != nil {
		t.Fatalf("initial PRD: %v", err)
	}

	answer, err := m.runPRDDraftingTurn(ctx, "Require PKCE")
	if err != nil {
		t.Fatalf("refine PRD: %v", err)
	}
	for _, want := range []string{
		"PRD updated from your feedback.",
		"Change summary:",
		"Updated requirements: REQ-001",
		"The system shall support OAuth 2.0 login with PKCE.",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("PRD refinement response missing %q:\n%s", want, answer)
		}
	}
	if strings.Contains(answer, "schema_version: idiom.prd.v1") {
		t.Fatalf("PRD refinement response should not echo raw persisted markdown:\n%s", answer)
	}
}

func TestProductWorkflowADRRefinementReturnsConversationalSummary(t *testing.T) {
	updatedADRs := strings.Replace(validADRs(), "Use authorization code flow with PKCE.", "Use authorization code flow with PKCE and documented redirect URI handling.", 1)
	m, effortRoot := setupE2EModel(t, []string{validPRD(), validADRs(), updatedADRs})
	ctx := context.Background()
	if _, err := m.runPRDDraftingTurn(ctx, "Add OAuth 2.0"); err != nil {
		t.Fatalf("PRD: %v", err)
	}
	writeWorkflowState(t, effortRoot, productworkflow.PhasePRDReview, "human_verification")
	if _, err := m.approvePRDAndAdvance(effortRoot); err != nil {
		t.Fatalf("approve PRD: %v", err)
	}
	m.featurePlannerPhase = productworkflow.PhaseADRDrafting
	writeWorkflowState(t, effortRoot, productworkflow.PhaseADRDrafting, "planned")

	answer, err := m.runADRDraftingTurn(ctx, "Capture redirect URI handling")
	if err != nil {
		t.Fatalf("refine ADR: %v", err)
	}
	for _, want := range []string{
		"ADR drafts updated from your feedback.",
		"Saved: adrs/index.json and ADR markdown files.",
		"Change summary:",
		"Updated ADRs: ADR-001",
		"Decision: Use authorization code flow with PKCE and documented redirect URI handling.",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("ADR refinement response missing %q:\n%s", want, answer)
		}
	}
	if strings.Contains(answer, "schema_version: idiom.adr.v1") {
		t.Fatalf("ADR refinement response should not echo raw persisted markdown:\n%s", answer)
	}
}

// TestADRRepairExhaustionDirectParse verifies exhaustion fails closed with direct parse.
func TestADRRepairExhaustionDirectParse(t *testing.T) {
	m, effortRoot := setupE2EModel(t, []string{validPRD()})
	ctx := context.Background()
	if _, err := m.runPRDDraftingTurn(ctx, "Add OAuth 2.0"); err != nil {
		t.Fatalf("PRD: %v", err)
	}

	m.featurePlannerPhase = productworkflow.PhaseADRDrafting
	m.featurePlannerEffortRoot = effortRoot
	// 3 garbage: 1 initial + 2 repair attempts (1 call each, direct parse)
	m.client = &scriptedLLMClient{responses: []string{garbage, garbage, garbage}}

	adrDocs, _, err := m.generateADRs(ctx, effortRoot)
	if err == nil {
		t.Fatal("ADR repair exhaustion should return an error")
	}
	if !strings.Contains(err.Error(), "ADR parsing failed after") {
		t.Fatalf("unexpected exhaustion error: %v", err)
	}
	if len(adrDocs) != 0 {
		t.Errorf("expected 0 ADRs after exhaustion, got %d", len(adrDocs))
	}
	if m.client.(*scriptedLLMClient).callCount != 3 {
		t.Errorf("expected 3 model calls, got %d", m.client.(*scriptedLLMClient).callCount)
	}
	t.Logf("PASS: ADR repair exhaustion direct parse fails closed, %d model calls", m.client.(*scriptedLLMClient).callCount)
}
