package architectcmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/architect"
)

func TestArchitectPacketCommandWritesPacket(t *testing.T) {
	root := t.TempDir()
	writeArchitectTestFile(t, filepath.Join(root, ".idiom/architect/profile.json"), `{
  "name": "demo",
  "gates": [{"name": "exact_tests_required", "required": true}]
}`)
	writeArchitectTestFile(t, filepath.Join(root, ".idiom/comprehension/views/pack.json"), `{"pack_id":"abc","candidate_files":[{"path":"cmd/idiom/main.go"}]}`+"\n")
	outputPath := filepath.Join(t.TempDir(), "packet.json")
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"packet",
		"--root", root,
		"--task", "Plan feature",
		"--file", "cmd/idiom/main.go:modify:entrypoint",
		"--test", "go test ./cmd/idiom:cli",
		"--anchor", `{"path":"src/simulation/job_queue.cpp","symbol":"JobQueue::posted_job_for","lines":"37","why":"C++ method anchor"}`,
		"--context-view", ".idiom/comprehension/views/pack.json",
		"--output", outputPath,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected success, code=%d stderr=%s", code, stderr.String())
	}
	if stdout.String() != "" {
		t.Fatalf("output file mode should not print stdout: %q", stdout.String())
	}
	var packet architect.Packet
	readJSONFile(t, outputPath, &packet)
	if packet.Task != "Plan feature" || packet.Profile.Name != "demo" {
		t.Fatalf("unexpected packet: %#v", packet)
	}
	if len(packet.Files) != 1 || packet.Files[0].Role != "modify" {
		t.Fatalf("file flag not parsed: %#v", packet.Files)
	}
	if len(packet.Anchors) != 1 || packet.Anchors[0].Symbol != "JobQueue::posted_job_for" {
		t.Fatalf("JSON anchor flag not parsed: %#v", packet.Anchors)
	}
	if len(packet.Evidence) != 1 || packet.Evidence[0].Kind != "context_view" {
		t.Fatalf("context view evidence not parsed: %#v", packet.Evidence)
	}
}

func TestArchitectPacketCommandWritesPlanningV2(t *testing.T) {
	root := t.TempDir()
	writeArchitectTestFile(t, filepath.Join(root, ".idiom/comprehension/views/pack.json"), `{
  "pack_id": "abc",
  "index_id": "idx1",
  "fresh": true,
  "candidate_files": [{"path":"cmd/idiom/architect.go","language":"go","confidence":"precise_backend"}],
  "tests": [{"command":"go test ./cmd/idiom","reason":"cli behavior"}]
}`+"\n")
	outputPath := filepath.Join(t.TempDir(), "packet.json")
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"packet",
		"--root", root,
		"--schema", "planning_v2",
		"--budget-tier", "brief",
		"--task", "Plan feature",
		"--context-view", ".idiom/comprehension/views/pack.json",
		"--output", outputPath,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected success, code=%d stderr=%s", code, stderr.String())
	}
	var packet architect.Packet
	readJSONFile(t, outputPath, &packet)
	if packet.SchemaVersion != architect.PlanningPacketV2Version {
		t.Fatalf("expected planning v2 packet, got %#v", packet.SchemaVersion)
	}
	if packet.ContextBudget == nil || packet.ContextBudget.BudgetTier != "brief" {
		t.Fatalf("planning v2 budget missing: %#v", packet.ContextBudget)
	}
	if packet.Contracts == nil || len(packet.Contracts.ValidationRequirements) != 1 {
		t.Fatalf("context view validation not carried into contracts: %#v", packet.Contracts)
	}
}

func TestArchitectLintCommandFailsRequiredGate(t *testing.T) {
	packetPath := filepath.Join(t.TempDir(), "packet.json")
	writeArchitectTestFile(t, packetPath, `{
  "schema_version": "idiom.architect.packet.v1",
  "task": "x",
  "gates": [{"name": "exact_tests_required", "required": true}]
}`)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"lint", "--packet", packetPath}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected lint failure, code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "exact_tests_required") {
		t.Fatalf("lint output missing gate: %s", stdout.String())
	}
}

func TestArchitectPlanAuditCommandFailsMissingFile(t *testing.T) {
	root := t.TempDir()
	packetPath := filepath.Join(t.TempDir(), "packet.json")
	writeArchitectTestFile(t, packetPath, `{
  "schema_version": "idiom.architect.planning_packet.v2",
  "task": "Implement packet work",
  "workspace": {"root": "`+filepath.ToSlash(root)+`"}
}`)
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"plan-audit",
		"--packet", packetPath,
		"--plan", "Modify internal/architect/missing.go.",
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected audit failure, code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "missing_file") {
		t.Fatalf("audit output missing finding: %s", stdout.String())
	}
}

func TestArchitectPlanAuditCommandChecksSubmittedPlanArtifact(t *testing.T) {
	dir := t.TempDir()
	packetPath := filepath.Join(dir, "packet.json")
	artifactPath := filepath.Join(dir, "last.plan.json")
	writeArchitectTestFile(t, packetPath, `{
  "schema_version": "idiom.architect.planning_packet.v2",
  "task": "Implement submitted artifact audit",
  "candidate_scope": [{"handle":"file://internal/architect/packet.go","path":"internal/architect/packet.go","kind":"file","rank":0.9}],
  "freshness": {"index_id":"idx-current","fresh":true,"context_views":[{"pack_id":"feedfacecafebeef","index_id":"idx-current","fresh":true}]},
  "contracts": {"validation_requirements":[{"command":"go test ./internal/architect"}]}
}`)
	writeArchitectTestFile(t, artifactPath, `{
  "schema_version": "idiom.plan.v2",
  "pack_id": "deadbeefdeadbeef",
  "packet": {"pack_id":"deadbeefdeadbeef","status":"available","index_id":"idx-old","freshness":"fresh"},
  "paths": {"editable":["internal/architect/packet.go"],"required":["internal/architect/packet.go"]},
  "evidence": [{"kind":"context_packet","handle":"context_pack://deadbeefdeadbeef","path":"logs/telemetry/secrets/customer-acme/feedback.jsonl"}],
  "unresolved_questions": [{"id":"question-1","claim":"raw_prompt transcript body","status":"open","required_before_handoff":true}],
  "verification_obligations": [{"id":"verification-1","kind":"focused_tests","command":"go test ./cmd/idiom","status":"open","required":true}],
  "approval": {"required":true,"status":"pending"},
  "handoff": {"id":"handoff-1","status":"ready","ready":true},
  "edits": [{"path":"internal/architect/outside.go"}]
}`)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"plan-audit", "--packet", packetPath, "--plan-artifact", artifactPath}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected artifact audit failure, code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	for _, want := range []string{"packet_identity_mismatch", "scope_path_mismatch", "unresolved_question_mismatch", "mismatch_metrics"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("artifact audit output missing %q: %s", want, stdout.String())
		}
	}
	for _, leaked := range []string{"customer-acme", "customer_acme", "raw_prompt", "transcript", "logs/telemetry/secrets"} {
		if strings.Contains(stdout.String(), leaked) {
			t.Fatalf("artifact audit leaked sensitive marker %q: %s", leaked, stdout.String())
		}
	}
}

func TestArchitectPlanAuditCommandBlocksReadyHandoffWithoutApproval(t *testing.T) {
	dir := t.TempDir()
	packetPath := filepath.Join(dir, "packet.json")
	artifactPath := filepath.Join(dir, "last.plan.json")
	writeArchitectTestFile(t, packetPath, `{
  "schema_version": "idiom.architect.planning_packet.v2",
  "task": "Audit approval handoff",
  "candidate_scope": [{"handle":"file://internal/architect/packet.go","path":"internal/architect/packet.go","kind":"file","rank":0.9}]
}`)
	writeArchitectTestFile(t, artifactPath, `{
  "schema_version": "idiom.plan.v2",
  "paths": {"editable":["internal/architect/packet.go"],"required":["internal/architect/packet.go"]},
  "approval": {"required":true,"status":"pending raw_prompt customer-acme transcript"},
  "handoff": {"id":"handoff-1","status":"ready","ready":true},
  "edits": [{"path":"internal/architect/packet.go"}]
}`)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"plan-audit", "--packet", packetPath, "--plan-artifact", artifactPath}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected approval handoff audit failure, code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	for _, want := range []string{`"ok": false`, "approval_handoff_mismatch", `"approval_handoff_mismatches": 1`} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("approval handoff audit output missing %q: %s", want, stdout.String())
		}
	}
	for _, leaked := range []string{"customer-acme", "customer_acme", "raw_prompt", "transcript"} {
		if strings.Contains(stdout.String(), leaked) {
			t.Fatalf("approval handoff audit leaked sensitive marker %q: %s", leaked, stdout.String())
		}
	}
}

func TestArchitectBudgetCommandReportsTiers(t *testing.T) {
	packetPath := filepath.Join(t.TempDir(), "packet.json")
	writeArchitectTestFile(t, packetPath, `{
  "schema_version": "idiom.architect.planning_packet.v2",
  "task": "Plan packet budget",
  "context_budget": {"budget_tier":"brief","token_limit":24000,"estimated_tokens":120,"reserved_output_tokens":6000,"risk":"low"},
  "candidate_scope": [{"handle":"file://internal/architect/packet.go","path":"internal/architect/packet.go","kind":"file","rank":0.9}]
}`)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"budget", "--packet", packetPath, "--tiers", "brief,standard"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected budget success, code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"budget_tier": "brief"`) || !strings.Contains(stdout.String(), `"placement"`) {
		t.Fatalf("budget output missing tiers or placement probes: %s", stdout.String())
	}
}

func TestArchitectPacketCompareCommandReportsV2Deltas(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "base.json")
	candidatePath := filepath.Join(dir, "candidate.json")
	writeArchitectTestFile(t, basePath, `{
  "schema_version": "idiom.architect.packet.v1",
  "task": "Plan packet compare",
  "tests": [{"command":"go test ./internal/architect"}],
  "evidence": [{"kind":"rule","content":"Use exact tests."}]
}`)
	writeArchitectTestFile(t, candidatePath, `{
  "schema_version": "idiom.architect.planning_packet.v2",
  "task": "Plan packet compare",
  "contracts": {"validation_requirements":[{"command":"go test ./internal/architect"}]},
  "context_budget": {"budget_tier":"brief","token_limit":24000,"estimated_tokens":120,"reserved_output_tokens":6000,"risk":"low"},
  "freshness": {"fresh": true},
  "language_capabilities": [{"language":"go","precision_tier":"precise_backend"}],
  "candidate_scope": [{"handle":"file://internal/architect/packet.go","path":"internal/architect/packet.go","kind":"file","rank":0.9}]
}`)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"packet-compare", "--base", basePath, "--candidate", candidatePath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected compare success, code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "candidate adds ranked candidate_scope") || !strings.Contains(stdout.String(), `"label": "candidate"`) {
		t.Fatalf("compare output missing expected delta: %s", stdout.String())
	}
}

func TestArchitectReplayCompareCommandAuditsPlanVariants(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	writeArchitectTestFile(t, filepath.Join(root, "internal/architect/packet.go"), "package architect\n")
	basePath := filepath.Join(dir, "base.json")
	candidatePath := filepath.Join(dir, "candidate.json")
	basePlanPath := filepath.Join(dir, "base-plan.md")
	candidatePlanPath := filepath.Join(dir, "candidate-plan.md")
	writeArchitectTestFile(t, basePath, `{
  "schema_version": "idiom.architect.packet.v1",
  "task": "Implement replay compare",
  "workspace": {"root": "`+filepath.ToSlash(root)+`"},
  "files": [{"path":"internal/architect/packet.go","role":"modify"}],
  "tests": [{"command":"go test ./internal/architect"}]
}`)
	writeArchitectTestFile(t, candidatePath, `{
  "schema_version": "idiom.architect.planning_packet.v2",
  "task": "Implement replay compare",
  "workspace": {"root": "`+filepath.ToSlash(root)+`"},
  "files": [{"path":"internal/architect/packet.go","role":"modify"}],
  "tests": [{"command":"go test ./internal/architect"}],
  "contracts": {"validation_requirements":[{"command":"go test ./internal/architect"}]},
  "candidate_scope": [{"handle":"file://internal/architect/packet.go","path":"internal/architect/packet.go","kind":"file","rank":0.9}]
}`)
	writeArchitectTestFile(t, basePlanPath, "Modify internal/architect/packet.go.\n")
	writeArchitectTestFile(t, candidatePlanPath, "Modify internal/architect/packet.go and validate with `go test ./internal/architect`.\n")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"replay-compare", "--base", basePath, "--candidate", candidatePath, "--base-plan", basePlanPath, "--candidate-plan", candidatePlanPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected replay compare success, code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "candidate plan has fewer audit warnings") || !strings.Contains(stdout.String(), `"preferred_variant": "candidate"`) {
		t.Fatalf("replay compare output missing expected improvement: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"mismatch_metrics"`) {
		t.Fatalf("replay compare output should expose bounded mismatch metrics: %s", stdout.String())
	}
}

func TestArchitectPromptCommandRendersBrief(t *testing.T) {
	packetPath := filepath.Join(t.TempDir(), "packet.json")
	writeArchitectTestFile(t, packetPath, `{
  "schema_version": "idiom.architect.packet.v1",
  "task": "x",
  "profile": {"name": "demo"}
}`)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"prompt", "--kind", "brief", "--packet", packetPath, "--prior", "approved plan"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected success, code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Architect Brief Task") || !strings.Contains(stdout.String(), "approved plan") {
		t.Fatalf("unexpected prompt: %s", stdout.String())
	}
}

func TestArchitectPlanAliasRendersPlanPrompt(t *testing.T) {
	packetPath := filepath.Join(t.TempDir(), "packet.json")
	writeArchitectTestFile(t, packetPath, `{
  "schema_version": "idiom.architect.packet.v1",
  "task": "x",
  "profile": {"name": "demo"}
}`)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"plan", "--packet", packetPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected success, code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Architect Plan Task") {
		t.Fatalf("unexpected prompt: %s", stdout.String())
	}
}

func writeArchitectTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readJSONFile(t *testing.T, path string, dst any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(data, dst); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}
