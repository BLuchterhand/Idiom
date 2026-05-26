package featureplannercmd

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
