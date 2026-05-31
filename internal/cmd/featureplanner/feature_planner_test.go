package featureplanner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"idiom/internal/featureplanner"
	"idiom/internal/productworkflow"
)

func TestRunFeaturePlannerMainReadsPromptFileAndHonorsJSON(t *testing.T) {
	configPath := writeMainConfig(t)
	promptPath := filepath.Join(t.TempDir(), "feature.md")
	if err := os.WriteFile(promptPath, []byte("plan feature\n"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	original := runHeadlessFeaturePlanner
	t.Cleanup(func() { runHeadlessFeaturePlanner = original })

	var got featurePlannerRequest
	runHeadlessFeaturePlanner = func(_ context.Context, req featurePlannerRequest, w io.Writer) (featurePlannerResult, error) {
		got = req
		_, _ = w.Write([]byte("{\"type\":\"message\",\"role\":\"assistant\",\"content\":\"planned\"}\n"))
		return featurePlannerResult{Answer: "planned", Fields: map[string]any{"plan_id": "fp-test"}}, nil
	}

	var stdout, stderr bytes.Buffer
	code := RunMain(
		[]string{"-config", configPath, "-prompt-file", promptPath, "-json", "-resume", "prior"},
		&stdout,
		&stderr,
	)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", code, stderr.String())
	}
	if got.Prompt != "plan feature" || got.Resume != "prior" || got.SessionID != "prior" {
		t.Fatalf("unexpected request: %#v", got)
	}
	if !strings.Contains(stdout.String(), `"type":"idiom.feature_planner.start"`) ||
		!strings.Contains(stdout.String(), `"type":"idiom.feature_planner.complete"`) ||
		!strings.Contains(stdout.String(), `"plan_id":"fp-test"`) {
		t.Fatalf("missing feature-planner JSON events: %s", stdout.String())
	}
	if strings.Count(stdout.String(), `"session_id":"prior"`) < 2 {
		t.Fatalf("resume run should keep prior session id in JSON stream: %s", stdout.String())
	}
	if strings.Contains(stdout.String(), "planned\n") && !strings.Contains(stdout.String(), `"content":"planned"`) {
		t.Fatalf("JSON mode should not append plain final answer, got %q", stdout.String())
	}
}

func TestRunFeaturePlannerMainReportsRunnerError(t *testing.T) {
	configPath := writeMainConfig(t)

	original := runHeadlessFeaturePlanner
	t.Cleanup(func() { runHeadlessFeaturePlanner = original })
	runHeadlessFeaturePlanner = func(_ context.Context, _ featurePlannerRequest, _ io.Writer) (featurePlannerResult, error) {
		return featurePlannerResult{}, io.ErrUnexpectedEOF
	}

	var stdout, stderr bytes.Buffer
	code := RunMain(
		[]string{"-config", configPath, "-prompt", "plan feature", "-json"},
		&stdout,
		&stderr,
	)

	if code != 1 {
		t.Fatalf("expected unavailable exit 1, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stderr.String(), io.ErrUnexpectedEOF.Error()) {
		t.Fatalf("stderr should explain runner error, got %s", stderr.String())
	}
	if !strings.Contains(stdout.String(), `"type":"idiom.feature_planner.error"`) {
		t.Fatalf("missing error JSON event: %s", stdout.String())
	}
}

func TestParseFeaturePlannerOptionsRejectsPromptConflicts(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseFeaturePlannerOptions([]string{"-prompt", "x", "-prompt-file", "brief.md"}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected prompt conflict, got %v", err)
	}

	_, err = parseFeaturePlannerOptions(nil, &stderr)
	if err == nil || !strings.Contains(err.Error(), "provide --prompt") {
		t.Fatalf("expected missing prompt error, got %v", err)
	}
}

func TestFeaturePlannerMainDispatchesThroughAdapter(t *testing.T) {
	configPath := writeMainConfig(t)

	original := runHeadlessFeaturePlanner
	t.Cleanup(func() { runHeadlessFeaturePlanner = original })
	called := false
	runHeadlessFeaturePlanner = func(_ context.Context, req featurePlannerRequest, _ io.Writer) (featurePlannerResult, error) {
		called = true
		if req.Config.Workspace.SessionID == "" {
			t.Fatal("session id should be set before adapter call")
		}
		return featurePlannerResult{Answer: "ok"}, nil
	}

	var stdout, stderr bytes.Buffer
	code := RunMain([]string{"-config", configPath, "-prompt", "plan"}, &stdout, &stderr)
	if code != 0 || !called || strings.TrimSpace(stdout.String()) != "ok" {
		t.Fatalf("expected feature-planner dispatch success, code=%d called=%t stdout=%q stderr=%s", code, called, stdout.String(), stderr.String())
	}
}

func TestDefaultHeadlessFeaturePlannerDispatchesAtomsThroughPlanExecute(t *testing.T) {
	root := t.TempDir()
	configPath := writeMainConfigRoot(t, root)

	originalModelTurn := runFeaturePlannerModelTurn
	originalPlanExecute := runFeaturePlannerPlanExecute
	t.Cleanup(func() {
		runFeaturePlannerModelTurn = originalModelTurn
		runFeaturePlannerPlanExecute = originalPlanExecute
	})

	modelCalls := 0
	planExecuteCalls := 0
	runFeaturePlannerModelTurn = func(_ featurePlannerRequest, _ string, taskFamily string, _ io.Writer) (string, error) {
		modelCalls++
		if taskFamily != "feature_planner_planner" {
			t.Fatalf("feature-planner execution must not use direct model task family %q", taskFamily)
		}
		return featurePlannerPlanExecuteDispatchPlanJSON(), nil
	}
	runFeaturePlannerPlanExecute = func(_ context.Context, _ featurePlannerRequest, prompt string, _ io.Writer) (string, error) {
		planExecuteCalls++
		if !strings.Contains(prompt, "Implement one feature atom") || !strings.Contains(prompt, "docs/feature-planner-plan-execute.txt") {
			t.Fatalf("plan-execute prompt missing atom contract: %s", prompt)
		}
		for _, want := range []string{
			"Git history evidence required before writes:",
			"`history_range` or `history_symbol`",
			"read the commit descriptions",
			"Paths whose existing logic needs history evidence before mutation:",
		} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("plan-execute prompt missing git history evidence requirement %q: %s", want, prompt)
			}
		}
		body, _ := json.Marshal(map[string]any{
			"tool":        featureplanner.ToolPlanExecuteComplete,
			"lane_family": "plan-execute",
			"summary":     "plan-execute wrote docs/feature-planner-plan-execute.txt and ran go test ./internal/featureplanner",
		})
		return string(body), nil
	}

	var stdout, stderr bytes.Buffer
	code := RunMain(
		[]string{"-config", configPath, "-prompt", "send atom through plan-execute", "-json"},
		&stdout,
		&stderr,
	)

	if code != 0 {
		t.Fatalf("runFeaturePlannerMain exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if modelCalls != 1 {
		t.Fatalf("model calls = %d, want planner-only call", modelCalls)
	}
	if planExecuteCalls != 1 {
		t.Fatalf("plan-execute calls = %d, want one atom execution", planExecuteCalls)
	}
	if strings.Contains(stdout.String(), "feature-planner-implementor") {
		t.Fatalf("stdout should not expose legacy direct implementor lane: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"lane_family":"plan-execute"`) {
		t.Fatalf("feature planner event should record plan-execute lane: %s", stdout.String())
	}
	for _, want := range []string{
		`"type":"idiom.feature_planner.plan_execute_git_history_requirement"`,
		`"evidence_tools":["history_range","history_symbol"]`,
		`"commit_descriptions_required":true`,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing git history requirement event field %q: %s", want, stdout.String())
		}
	}
}

func TestFeaturePlannerPlanExecuteProgressPathUsesLatestStateEvent(t *testing.T) {
	output := strings.Join([]string{
		`{"type":"idiom.plan_execute.resume_state_saved","state_path":".idiom/plan-execute/first/state.json"}`,
		`not json`,
		`{"type":"idiom.plan_execute.resume_state_saved","state_path":".idiom/plan-execute/final/state.json"}`,
		`{"type":"message","state_path":"ignored"}`,
	}, "\n")

	if got := featurePlannerPlanExecuteProgressPath(output); got != ".idiom/plan-execute/final/state.json" {
		t.Fatalf("progress path = %q", got)
	}
}

func featurePlannerPlanExecuteDispatchPlanJSON() string {
	return `{
  "tool": "submit_feature_plan",
  "schema_version": "idiom.feature_plan.v1",
  "feature_id": "feature-plan-execute-dispatch",
  "source_task": {"kind": "test", "summary": "Route feature-planner atom execution through plan-execute"},
  "summary": "Route atom execution through plan-execute",
  "top_level_scope": {
    "objective": "Exercise feature-planner atom dispatch",
    "success_criteria": ["the atom execution callback is plan-execute"]
  },
  "sdd_contract": {
    "refs": ["SDD-PlanExecuteDispatch"],
    "acceptance": ["ready atoms execute through plan-execute"]
  },
  "tdd_contract": {
    "coverage_tier": "contract_high",
    "required_before_implementation": true,
    "obligations": [
      {
        "id": "TDD-PlanExecuteDispatch",
        "behavior": "feature-planner dispatch delegates code writing to plan-execute",
        "command": "go test ./cmd/idiom -run TestDefaultHeadlessFeaturePlannerDispatchesAtomsThroughPlanExecute",
        "required_before": "implementation"
      }
    ]
  },
  "atoms": [
    {
      "schema_version": "idiom.feature_atom.v1",
      "feature_id": "feature-plan-execute-dispatch",
      "id": "atom-plan-execute-dispatch",
      "title": "Dispatch through plan-execute",
      "summary": "Prove feature-planner does not directly model-call implementation atoms",
      "required_edit_paths": ["docs/feature-planner-plan-execute.txt"],
      "allowed_edit_paths": ["docs/feature-planner-plan-execute.txt"],
      "sdd_refs": ["SDD-PlanExecuteDispatch"],
      "tdd_obligations": ["TDD-PlanExecuteDispatch"],
      "required_tests": ["go test ./cmd/idiom -run TestDefaultHeadlessFeaturePlannerDispatchesAtomsThroughPlanExecute"],
      "test_first": {
        "obligation_refs": ["TDD-PlanExecuteDispatch"],
        "commands": ["go test ./cmd/idiom -run TestDefaultHeadlessFeaturePlannerDispatchesAtomsThroughPlanExecute"],
        "baseline_required": true,
        "test_artifact_paths": ["cmd/idiom/feature_planner_test.go"],
        "pre_execution_event_ids": ["evt-plan-execute-dispatch"],
        "expected_pre_execution_result": "red-confirmed",
        "coverage_gate_event_id": "evt-plan-execute-dispatch"
      },
      "planned_tdd_snapshot": {
        "snapshot_id": "snap-plan-execute-dispatch",
        "snapshot_digest": "sha256:plan-execute-dispatch",
        "obligation_ids": ["TDD-PlanExecuteDispatch"],
        "test_commands": ["go test ./cmd/idiom -run TestDefaultHeadlessFeaturePlannerDispatchesAtomsThroughPlanExecute"],
        "test_artifact_paths": ["cmd/idiom/feature_planner_test.go"],
        "expected_pre_execution_results": ["red-confirmed"],
        "created_before_dispatch_event_id": "evt-plan-execute-dispatch"
      },
      "prompt": "Update docs/feature-planner-plan-execute.txt and keep the atom bounded."
    }
  ]
}`
}

func writeMainConfig(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("IDIOM_QUEUE_SOCKET", filepath.Join(root, "missing-runner.sock"))
	path := filepath.Join(root, "config.json")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func writeMainConfigRoot(t *testing.T, root string) string {
	t.Helper()
	t.Setenv("IDIOM_QUEUE_SOCKET", filepath.Join(root, "missing-runner.sock"))
	path := filepath.Join(t.TempDir(), "config.json")
	configJSON := fmt.Sprintf(`{"workspace":{"root":%q,"cwd":%q}}`, root, root)
	if err := os.WriteFile(path, []byte(configJSON+"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadProductWorkflowBriefCarriesClaimBlockersWithoutImplementationRecords(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "adrs"), 0o755); err != nil {
		t.Fatalf("mkdir adrs: %v", err)
	}
	workflowState := productworkflow.WorkflowState{
		SchemaVersion:        "idiom.product_workflow_state.v0.fixture",
		EffortID:             "effort-headless",
		TaskID:               "task-headless",
		Title:                "Headless claim blocker",
		StatusBucket:         "started",
		CurrentPhase:         "phase-4",
		CurrentState:         "planning",
		ArtifactPaths:        map[string]string{"events": "events.jsonl"},
		Context:              productworkflow.WorkflowContext{ContextPackID: "pack", FreshnessState: "current", RefreshPolicy: "on_demand"},
		Coverage:             productworkflow.CoverageState{CoverageTier: "unit"},
		Privacy:              productworkflow.PrivacyState{RedactionStatus: "applied"},
		FolderPath:           root,
		NextValidActions:     []string{"feature-planner"},
		DurabilityLevel:      "source_controlled",
		WorkspaceFingerprint: "test",
	}
	writeJSONFile(t, filepath.Join(root, "workflow_state.json"), workflowState)
	prd := productworkflow.PRDDocument{
		FrontMatter: productworkflow.PRDFrontMatter{
			SchemaVersion:     "idiom.prd.v1",
			PRDID:             "PRD-HEADLESS",
			Status:            "accepted",
			TaskID:            "task-headless",
			CreatedAt:         "2026-05-31T00:00:00Z",
			AcceptedAt:        "2026-05-31T00:01:00Z",
			AcceptedBy:        "test",
			AcceptanceProfile: "human-review",
		},
		Requirements: []productworkflow.PRDRequirement{{
			ID:                   "REQ-HEADLESS",
			Priority:             "must",
			Statement:            "Use the ExampleCloud platform for implementation.",
			EvidenceRefs:         []string{"EV-MISSING"},
			TDDObligations:       []string{"TDD-HEADLESS"},
			OwnerAlignment:       "operator-approved",
			Testability:          "go test",
			Metric:               "claim is backed before dispatch",
			OpenQuestionsPresent: true,
		}},
	}
	if err := os.WriteFile(filepath.Join(root, "prd.md"), []byte(productworkflow.RenderPRDMarkdown(prd)), 0o644); err != nil {
		t.Fatalf("write prd: %v", err)
	}
	tdd := productworkflow.TDDLedger{
		SchemaVersion: "idiom.tdd_ledger.v1",
		EffortID:      "effort-headless",
		TaskID:        "task-headless",
		CoverageTier:  "unit",
		Coverage:      productworkflow.TDDCoverage{MutationMinScore: 1},
		Obligations: []productworkflow.TDDObligation{{
			ID:             "TDD-HEADLESS",
			Type:           "regression",
			Behavior:       "Claim is validated",
			Owner:          "internal/cmd/featureplanner/feature_planner_test.go",
			Command:        "go test ./internal/cmd/featureplanner",
			RequiredBefore: "implementation",
			Status:         "planned",
		}},
	}
	writeJSONFile(t, filepath.Join(root, "tdd.json"), tdd)

	brief, err := loadProductWorkflowBrief(root)
	if err != nil {
		t.Fatalf("loadProductWorkflowBrief: %v", err)
	}
	if brief == nil {
		t.Fatal("expected product workflow brief")
	}
	if !strings.Contains(strings.Join(brief.Blockers, "\n"), `implementation-affecting claim in PRD requirement REQ-HEADLESS references unresolved evidence "EV-MISSING"`) {
		t.Fatalf("expected implementation claim blocker without implementation records, got %#v", brief.Blockers)
	}
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestFeaturePlanner_ProductWorkflowIntegration_BuildBrief(t *testing.T) {
	// Verify that ProductWorkflowBrief can be constructed from fixture data
	// and that BuildPlannerPrompt injects real artifact IDs.

	brief := &featureplanner.ProductWorkflowBrief{
		EffortID:   "test-fixture-effort",
		TaskID:     "test-task",
		EffortRoot: "testdata/feature-planner-pw-integration/effort",
		PRDRequirements: []featureplanner.PRDRequirementBrief{
			{ID: "REQ-001", Priority: "P0", Statement: "Add a thing that works",
				Testability: "automated", Obligations: []string{"TDD-1"}},
		},
		AcceptedADRIDs:   []string{"ADR-001"},
		AcceptedADRPaths: []string{"adrs/001-test.md"},
		TDDObligations: []featureplanner.TDDObligationBrief{
			{ID: "TDD-1", Behavior: "Thing works", Command: "go test ./...",
				RequiredBefore: "implementation", Status: "open"},
		},
		CoverageTier:       "behavior_standard",
		CoverageGatePassed: true,
		FilesToModify:      []string{"internal/example/thing.go"},
		RequiredBehavior:   []string{"REQ-001: Add a thing that works"},
		Tests:              []string{"go test ./internal/example/..."},
		Acceptance:         []string{"go test ./internal/example/... passes before implementation dispatch"},
		Decisions:          []string{"ADR-001 (adrs/001-test.md): Keep the thing simple."},
		ArtifactRootPath:   "testdata/feature-planner-pw-integration/effort",
	}

	state := featureplanner.NewState("test-session", "build feature", "", 3, time.Unix(1, 0).UTC())
	prompt := featureplanner.BuildPlannerPrompt("Add a thing", state, brief)

	// With brief, prompt should contain real artifact IDs
	if !strings.Contains(prompt, "REQ-001") {
		t.Error("prompt missing PRD requirement REQ-001")
	}
	if !strings.Contains(prompt, "ADR-001") {
		t.Error("prompt missing ADR ID ADR-001")
	}
	if !strings.Contains(prompt, "TDD-1") {
		t.Error("prompt missing TDD obligation TDD-1")
	}
	if !strings.Contains(prompt, "Product Workflow Context") {
		t.Error("prompt missing Product Workflow Context section")
	}
	if !strings.Contains(prompt, "Required behavior: REQ-001: Add a thing that works") {
		t.Error("prompt missing required behavior from product workflow brief")
	}
	if !strings.Contains(prompt, "Accepted decisions:") || !strings.Contains(prompt, "Keep the thing simple") {
		t.Error("prompt missing accepted ADR decisions")
	}
	if !strings.Contains(prompt, "Acceptance: go test ./internal/example/... passes before implementation dispatch") {
		t.Error("prompt missing acceptance from product workflow brief")
	}
	if !strings.Contains(prompt, "idiom.feature_plan.v2") {
		t.Error("prompt should reference v2 schema")
	}

	// Should NOT contain the hallucination template
	if strings.Contains(prompt, "docs/path.md") {
		t.Error("prompt must not contain the hallucination template docs/path.md")
	}
}

func TestFeaturePlanner_ProductWorkflowIntegration_CoverageGateBlocked(t *testing.T) {
	// When coverage gate has NOT passed, the prompt should tell the model
	// not to produce a plan.

	brief := &featureplanner.ProductWorkflowBrief{
		EffortID:           "test-effort",
		CoverageTier:       "contract_high",
		CoverageGatePassed: false,
	}

	state := featureplanner.NewState("test-session", "build feature", "", 3, time.Unix(1, 0).UTC())
	prompt := featureplanner.BuildPlannerPrompt("Add a thing", state, brief)

	if !strings.Contains(prompt, "coverage gate has NOT passed") {
		t.Error("prompt should warn when coverage gate is blocked")
	}
	if !strings.Contains(prompt, "ask_planner_question") {
		t.Error("prompt should suggest ask_planner_question when gate is blocked")
	}
}

func TestFeaturePlanner_ProductWorkflowIntegration_StandaloneMode(t *testing.T) {
	// Without ProductWorkflowBrief (nil), the prompt should use acceptance
	// criteria fallback and NOT contain the hallucination template.

	state := featureplanner.NewState("test-session", "build feature", "", 3, time.Unix(1, 0).UTC())
	prompt := featureplanner.BuildPlannerPrompt("Add a thing", state, nil)

	if strings.Contains(prompt, `"refs":["docs/path.md:line"]`) {
		t.Error("standalone JSON template must not use docs/path.md as a ref value")
	}
	if strings.Contains(prompt, `"sdd_refs":["docs/path.md:line"]`) {
		t.Error("standalone JSON template must not use docs/path.md as an atom sdd_ref")
	}
	if !strings.Contains(prompt, "acceptance criteria") {
		t.Error("standalone prompt should mention acceptance criteria as fallback")
	}
}

func TestFeaturePlanner_ProductWorkflowIntegration_JSONRoundTrip(t *testing.T) {
	brief := featureplanner.ProductWorkflowBrief{
		EffortID: "test-effort",
		PRDRequirements: []featureplanner.PRDRequirementBrief{
			{ID: "REQ-001", Priority: "P0", Statement: "Do the thing",
				Testability: "automated", Obligations: []string{"TDD-1"}},
		},
		AcceptedADRIDs: []string{"ADR-001"},
		TDDObligations: []featureplanner.TDDObligationBrief{
			{ID: "TDD-1", Behavior: "test passes", Command: "go test ./...",
				RequiredBefore: "implementation", Status: "open"},
		},
		CoverageTier:       "behavior_standard",
		CoverageGatePassed: true,
	}

	data, err := json.Marshal(brief)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var roundTripped featureplanner.ProductWorkflowBrief
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if roundTripped.EffortID != "test-effort" {
		t.Errorf("EffortID: got %q want test-effort", roundTripped.EffortID)
	}
	if len(roundTripped.PRDRequirements) != 1 {
		t.Fatalf("PRDRequirements length: got %d want 1", len(roundTripped.PRDRequirements))
	}
	if roundTripped.PRDRequirements[0].ID != "REQ-001" {
		t.Errorf("PRDRequirements[0].ID: got %q want REQ-001", roundTripped.PRDRequirements[0].ID)
	}
}
