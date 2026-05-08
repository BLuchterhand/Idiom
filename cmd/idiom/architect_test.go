package main

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
	code := runArchitectMain([]string{
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
	code := runArchitectMain([]string{
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
	code := runArchitectMain([]string{"lint", "--packet", packetPath}, &stdout, &stderr)
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
	code := runArchitectMain([]string{
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

func TestArchitectPromptCommandRendersBrief(t *testing.T) {
	packetPath := filepath.Join(t.TempDir(), "packet.json")
	writeArchitectTestFile(t, packetPath, `{
  "schema_version": "idiom.architect.packet.v1",
  "task": "x",
  "profile": {"name": "demo"}
}`)
	var stdout, stderr bytes.Buffer
	code := runArchitectMain([]string{"prompt", "--kind", "brief", "--packet", packetPath, "--prior", "approved plan"}, &stdout, &stderr)
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
	code := runArchitectMain([]string{"plan", "--packet", packetPath}, &stdout, &stderr)
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
