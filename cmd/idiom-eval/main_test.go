package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPrintsMetricsJSON(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "conversation.jsonl")
	writeQwenEvalLog(t, logPath)

	var stdout, stderr bytes.Buffer
	code := run([]string{"-log", logPath, "-json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{`"last_user":"review routing"`, `"budget_ledgers"`, `"route_ambiguities"`, `"control_rails"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected JSON output to contain %q, got %s", want, out)
		}
	}
}

func TestRunWritesSuggestedScenario(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "conversation.jsonl")
	scenarioPath := filepath.Join(dir, "suggested.json")
	writeQwenEvalLog(t, logPath)

	var stdout, stderr bytes.Buffer
	code := run([]string{"-log", logPath, "-write-scenario", scenarioPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(scenarioPath)
	if err != nil {
		t.Fatalf("read suggested scenario: %v", err)
	}
	for _, want := range []string{`"name": "suggested"`, `"require_budget_ledger"`, `"require_route_ambiguity"`, `"require_control_rails"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("expected suggested scenario to contain %q, got %s", want, string(data))
		}
	}
}

func TestRunReturnsFailureForScenarioMismatch(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "conversation.jsonl")
	scenarioPath := filepath.Join(dir, "scenario.json")
	writeQwenEvalLog(t, logPath)
	scenario := `{
  "name": "wrong-prompt",
  "prompt_contains": ["different prompt"]
}
`
	if err := os.WriteFile(scenarioPath, []byte(scenario), 0o644); err != nil {
		t.Fatalf("write scenario: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"-log", logPath, "-scenario", scenarioPath}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "FAIL wrong-prompt") || !strings.Contains(stdout.String(), "last user prompt does not contain") {
		t.Fatalf("expected failure report, got stdout=%s", stdout.String())
	}
}

func TestRunRequiresLogPath(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(nil, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), "missing required -log path") {
		t.Fatalf("expected missing log error, got %s", stderr.String())
	}
}

func writeQwenEvalLog(t *testing.T, path string) {
	t.Helper()
	const log = `{"type":"message","role":"user","content":"review routing"}
{"type":"message","role":"route_plan","content":"initial=review reason=review_request contract=review_findings thoroughness=thorough"}
{"type":"message","role":"budget_ledger","content":"{\"lane\":\"review\",\"used_model_calls\":2,\"total_evidence_paths\":1,\"stop_reason\":\"ready_after_retry\",\"budget_exhausted\":false}"}
{"type":"message","role":"route_ambiguity","content":"{\"selected_lane\":\"review\",\"reason\":\"review_request\",\"signals\":[\"review\"],\"ambiguous\":false}"}
{"type":"message","role":"control_rails","content":"{\"lane\":\"review\",\"contract\":\"review_findings\",\"task_family\":\"review\",\"permission_profile\":\"read_only_workspace\",\"required_evidence\":[\"candidate_path\"],\"required_tools\":[\"workspace_inspection\"],\"forbidden_tools\":[\"edit_file\"],\"readiness_gate\":[\"required_evidence_present\"],\"search_policy\":[\"rank_candidates\"],\"verification_contract\":\"not_required_for_read_only_lane\",\"stop_conditions\":[\"required_evidence_satisfied\"],\"final_critic\":[\"findings_first\"],\"failure_to_eval\":false}"}
{"type":"message","role":"assistant","content":"No bounded finding."}
`
	if err := os.WriteFile(path, []byte(log), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
}
