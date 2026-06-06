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

	"idiom/internal/config"
	"idiom/internal/featureplanner"
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
		if !strings.Contains(prompt, "Canonical plan-execute edit scope") ||
			!strings.Contains(prompt, "required_edit_paths:") ||
			!strings.Contains(prompt, "cmd/idiom/feature_planner_test.go") {
			t.Fatalf("plan-execute prompt missing normalized canonical scope: %s", prompt)
		}
		if strings.Contains(prompt, "allowed_edit_paths:") {
			t.Fatalf("promoted required paths must not also be emitted as allowed scope: %s", prompt)
		}
		body, _ := json.Marshal(map[string]any{
			"tool":              featureplanner.ToolPlanExecuteComplete,
			"lane_family":       "plan-execute",
			"status":            "completed",
			"completion_source": "test_plan_execute_adapter",
			"summary":           "plan-execute wrote docs/feature-planner-plan-execute.txt and ran go test ./internal/featureplanner",
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
}

func TestFeaturePlannerPlanExecuteProgressPathUsesLatestStateEvent(t *testing.T) {
	output := strings.Join([]string{
		`{"type":"idiom.plan_execute.resume_state_saved","phase":"executor","status":"running","state_path":".idiom/plan-execute/first/state.json"}`,
		`not json`,
		`{"type":"idiom.plan_execute.resume_state_saved","phase":"complete","status":"completed","review_status":"skipped","review_repair_skipped":true,"state_path":".idiom/plan-execute/final/state.json"}`,
		`{"type":"message","state_path":"ignored"}`,
	}, "\n")

	fields := featurePlannerPlanExecuteCompletionFields(output)
	if got := featureplanner.CompletionFieldString(fields, "progress_path"); got != ".idiom/plan-execute/final/state.json" {
		t.Fatalf("progress path = %q", got)
	}
	if got := fmt.Sprint(fields["status"]); got != "completed" {
		t.Fatalf("status = %q, want completed; fields=%+v", got, fields)
	}
	if got := fmt.Sprint(fields["phase"]); got != "complete" {
		t.Fatalf("phase = %q, want complete; fields=%+v", got, fields)
	}
	if got := fmt.Sprint(fields["review_status"]); got != "skipped" {
		t.Fatalf("review_status = %q, want skipped; fields=%+v", got, fields)
	}
	if got := fields["review_repair_skipped"]; got != true {
		t.Fatalf("review_repair_skipped = %v, want true; fields=%+v", got, fields)
	}
}

func TestFeaturePlannerPlanExecuteCompletionFieldsMarksReviewDegradedWithoutStatePath(t *testing.T) {
	output := strings.Join([]string{
		`{"type":"idiom.plan_execute.review_repair_degraded","status":"review_unavailable","reason_class":"review_model_error"}`,
		`{"type":"message","content":"review unavailable"}`,
	}, "\n")

	fields := featurePlannerPlanExecuteCompletionFields(output)

	if got := fmt.Sprint(fields["status"]); got != "degraded" {
		t.Fatalf("status = %q, want degraded; fields=%+v", got, fields)
	}
	if got := fmt.Sprint(fields["review_status"]); got != "review_unavailable" {
		t.Fatalf("review_status = %q, want review_unavailable; fields=%+v", got, fields)
	}
	if got := fmt.Sprint(fields["failure_kind"]); got != "review_model_error" {
		t.Fatalf("failure_kind = %q, want review_model_error; fields=%+v", got, fields)
	}
}

func TestDefaultFeaturePlannerPlanExecuteReturnsTypedSkippedReviewCompletion(t *testing.T) {
	req := featurePlannerRequest{
		ConfigPath: "config.json",
		SessionID:  "feature-session",
		Deps: Dependencies{
			RunPlanExecute: func(args []string, stdout io.Writer, _ io.Writer) int {
				if !testContainsString(args, "-skip-review-repair") {
					t.Fatalf("expected explicit skipped review flag, args=%+v", args)
				}
				_, _ = stdout.Write([]byte(strings.Join([]string{
					`{"type":"idiom.plan_execute.resume_state_saved","phase":"complete","status":"completed","review_status":"skipped","state_path":".idiom/plan-execute/session/state.json"}`,
					`{"type":"message","content":"done"}`,
				}, "\n")))
				return 0
			},
		},
	}

	raw, err := defaultFeaturePlannerPlanExecute(context.Background(), req, "implement atom", io.Discard)
	if err != nil {
		t.Fatalf("defaultFeaturePlannerPlanExecute returned error: %v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatalf("decode completion envelope: %v", err)
	}
	if got := fmt.Sprint(envelope["status"]); got != "completed" {
		t.Fatalf("status = %q, want completed; envelope=%+v", got, envelope)
	}
	if got := fmt.Sprint(envelope["review_status"]); got != "skipped" {
		t.Fatalf("review_status = %q, want skipped; envelope=%+v", got, envelope)
	}
	if got := envelope["review_repair_skipped"]; got != true {
		t.Fatalf("review_repair_skipped = %v, want true; envelope=%+v", got, envelope)
	}
}

func TestDefaultFeaturePlannerPlanExecuteMarksMissingTypedStatusAmbiguous(t *testing.T) {
	req := featurePlannerRequest{
		ConfigPath: "config.json",
		SessionID:  "feature-session",
		Deps: Dependencies{
			RunPlanExecute: func(_ []string, stdout io.Writer, _ io.Writer) int {
				_, _ = stdout.Write([]byte("plain nested stdout without typed completion status"))
				return 0
			},
		},
	}

	raw, err := defaultFeaturePlannerPlanExecute(context.Background(), req, "implement atom", io.Discard)
	if err != nil {
		t.Fatalf("defaultFeaturePlannerPlanExecute returned error: %v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatalf("decode completion envelope: %v", err)
	}
	if got := fmt.Sprint(envelope["status"]); got != "ambiguous" {
		t.Fatalf("status = %q, want ambiguous; envelope=%+v", got, envelope)
	}
	if got := fmt.Sprint(envelope["failure_kind"]); got != "missing_nested_plan_execute_status" {
		t.Fatalf("failure_kind = %q, want missing_nested_plan_execute_status; envelope=%+v", got, envelope)
	}
}

func TestDefaultFeaturePlannerPlanExecuteReturnsTypedFailureCompletion(t *testing.T) {
	req := featurePlannerRequest{
		ConfigPath: "config.json",
		SessionID:  "feature-session",
		Deps: Dependencies{
			RunPlanExecute: func(_ []string, stdout io.Writer, stderr io.Writer) int {
				_, _ = stdout.Write([]byte(`{"type":"idiom.plan_execute.resume_state_saved","phase":"review_repair","status":"degraded","review_status":"degraded","state_path":".idiom/plan-execute/session/state.json"}`))
				_, _ = stderr.Write([]byte("review repair degraded"))
				return 1
			},
		},
	}

	raw, err := defaultFeaturePlannerPlanExecute(context.Background(), req, "implement atom", io.Discard)
	if err != nil {
		t.Fatalf("typed nested failure should be returned to feature planner, got error: %v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatalf("decode completion envelope: %v", err)
	}
	if got := fmt.Sprint(envelope["status"]); got != "degraded" {
		t.Fatalf("status = %q, want degraded; envelope=%+v", got, envelope)
	}
	if got := fmt.Sprint(envelope["failure_reason"]); !strings.Contains(got, "exit 1") {
		t.Fatalf("failure_reason = %q, want exit status; envelope=%+v", got, envelope)
	}
}

func TestDefaultFeaturePlannerPlanExecuteEmitsBlockedTelemetryForFailure(t *testing.T) {
	req := featurePlannerRequest{
		ConfigPath: "config.json",
		SessionID:  "feature-session",
		JSONEvents: true,
		Deps: Dependencies{
			RunPlanExecute: func(_ []string, stdout io.Writer, stderr io.Writer) int {
				_, _ = stdout.Write([]byte(`{"type":"idiom.plan_execute.resume_state_saved","phase":"review_repair","status":"failed","review_status":"review_model_error","state_path":".idiom/plan-execute/session/state.json"}`))
				_, _ = stderr.Write([]byte("review model error"))
				return 1
			},
		},
	}

	var stdout bytes.Buffer
	raw, err := defaultFeaturePlannerPlanExecute(context.Background(), req, "implement atom", &stdout)
	if err != nil {
		t.Fatalf("typed nested failure should be returned to feature planner, got error: %v", err)
	}
	if !strings.Contains(raw, `"status":"failed"`) {
		t.Fatalf("completion should preserve failed status: %s", raw)
	}
	if !strings.Contains(stdout.String(), `"type":"internal_idiom.plan_execute.blocked"`) {
		t.Fatalf("expected blocked telemetry, stdout=%s", stdout.String())
	}
	if strings.Contains(stdout.String(), `"type":"internal_idiom.plan_execute.completed"`) {
		t.Fatalf("failure completion must not emit completed telemetry, stdout=%s", stdout.String())
	}
}

func TestDefaultHeadlessFeaturePlannerRequiresExplicitProductContext(t *testing.T) {
	root := t.TempDir()
	originalModelTurn := runFeaturePlannerModelTurn
	t.Cleanup(func() { runFeaturePlannerModelTurn = originalModelTurn })
	runFeaturePlannerModelTurn = func(_ featurePlannerRequest, _ string, _ string, _ io.Writer) (string, error) {
		t.Fatal("model planner must not run when explicit product effort context cannot load")
		return "", nil
	}

	_, err := defaultHeadlessFeaturePlanner(context.Background(), featurePlannerRequest{
		Config:                config.Config{Workspace: config.Workspace{Root: root}},
		Prompt:                "implement product feature",
		MaxPlanningIterations: 3,
		EffortRoot:            filepath.Join(root, "missing-effort"),
	}, io.Discard)

	if err == nil || !strings.Contains(err.Error(), "product workflow context unavailable") {
		t.Fatalf("expected product context error, got %v", err)
	}
}

func testContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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
		Tests:              []string{"go test ./internal/example/..."},
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

func TestFeaturePlanner_ProductWorkflowIntegration_TDDVerificationPendingCanDispatch(t *testing.T) {
	brief := &featureplanner.ProductWorkflowBrief{
		EffortID:           "test-effort",
		CoverageTier:       "contract_high",
		CoverageGatePassed: false,
		CoverageBlockers:   []string{"expected all 1 TDD obligations to have verification evidence"},
		TDDObligations: []featureplanner.TDDObligationBrief{{
			ID:             "TDD-1",
			Behavior:       "prove the feature",
			Command:        "go test ./...",
			RequiredBefore: "implementation",
			Status:         "planned",
		}},
		FilesToModify: []string{"internal/labels/labels.go"},
		Tests:         []string{"go test ./..."},
	}

	state := featureplanner.NewState("test-session", "build feature", "", 3, time.Unix(1, 0).UTC())
	prompt := featureplanner.BuildPlannerPrompt("Add a thing", state, brief)

	if !strings.Contains(prompt, "Required submit_feature_plan shape") {
		t.Fatalf("prompt should allow implementation dispatch when only TDD verification evidence is pending:\n%s", prompt)
	}
	if !strings.Contains(prompt, "pending only because implementation verification evidence is not recorded yet") {
		t.Fatalf("prompt should explain pending verification evidence:\n%s", prompt)
	}
	if strings.Contains(prompt, "Do not produce a feature plan") {
		t.Fatalf("prompt must not block implementation dispatch for pending TDD verification evidence:\n%s", prompt)
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
