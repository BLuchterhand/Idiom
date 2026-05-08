package architect

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildPacketLoadsProfileRulesAndPendingCollectors(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "RULES.md"), "Use exact tests.\n")
	writeFile(t, filepath.Join(root, ".idiom/architect/profile.json"), `{
  "name": "demo",
  "description": "Demo adapter",
  "rules": [{"label": "rules", "path": "RULES.md", "required": true}],
  "collectors": [{"name": "symbols", "command": "echo", "args": ["ok"]}],
  "gates": [{"name": "exact_tests_required", "required": true}],
  "constraints": ["Use verified anchors only."]
}`)

	packet, err := BuildPacket(context.Background(), BuildOptions{
		Root:  root,
		Task:  "Plan a feature",
		Files: []FileRef{{Path: "cmd/demo.go", Role: "modify"}},
		Tests: []CommandRef{{Command: "go test ./...", Reason: "full suite"}},
		Now:   time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("BuildPacket: %v", err)
	}
	if packet.SchemaVersion != SchemaVersion || packet.Profile.Name != "demo" {
		t.Fatalf("unexpected packet identity: %#v", packet)
	}
	if len(packet.Evidence) != 2 {
		t.Fatalf("expected rule + pending collector evidence, got %#v", packet.Evidence)
	}
	if packet.Evidence[0].Kind != "rule" || !strings.Contains(packet.Evidence[0].Content, "Use exact tests") {
		t.Fatalf("rule evidence not loaded: %#v", packet.Evidence[0])
	}
	if packet.Evidence[1].Kind != "collector_pending" {
		t.Fatalf("collector should be pending without RunCollectors: %#v", packet.Evidence[1])
	}
	if len(LintPacket(packet).Findings) != 0 {
		t.Fatalf("packet should pass lint: %#v", LintPacket(packet))
	}
}

func TestBuildPacketRunsCollectorsWhenRequested(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "profile.json"), `{
  "name": "demo",
  "collectors": [{"name": "echo", "command": "/bin/sh", "args": ["-c", "printf '\\033[31mcollector-output\\033[0m\\n'"]}]
}`)
	packet, err := BuildPacket(context.Background(), BuildOptions{
		Root:          root,
		Task:          "Collect facts",
		ProfilePath:   filepath.Join(root, "profile.json"),
		RunCollectors: true,
	})
	if err != nil {
		t.Fatalf("BuildPacket: %v", err)
	}
	if len(packet.Evidence) != 1 || packet.Evidence[0].Kind != "collector" {
		t.Fatalf("collector evidence missing: %#v", packet.Evidence)
	}
	if !strings.Contains(packet.Evidence[0].Content, "collector-output") {
		t.Fatalf("collector output missing: %#v", packet.Evidence[0])
	}
	if strings.Contains(packet.Evidence[0].Content, "\x1b[") {
		t.Fatalf("collector output should be ANSI-stripped: %#v", packet.Evidence[0])
	}
}

func TestBuildPacketIncludesContextViewEvidence(t *testing.T) {
	root := t.TempDir()
	viewPath := filepath.Join(root, ".idiom", "comprehension", "views", "pack.json")
	writeFile(t, viewPath, `{"pack_id":"abc","candidate_files":[{"path":"pkg/pkg.go"}]}`+"\n")
	packet, err := BuildPacket(context.Background(), BuildOptions{
		Root:         root,
		Task:         "Use context",
		ContextViews: []string{".idiom/comprehension/views/pack.json"},
	})
	if err != nil {
		t.Fatalf("BuildPacket: %v", err)
	}
	if len(packet.Evidence) != 1 || packet.Evidence[0].Kind != "context_view" {
		t.Fatalf("context view evidence missing: %#v", packet.Evidence)
	}
	if !strings.Contains(packet.Evidence[0].Content, "candidate_files") {
		t.Fatalf("context view content missing: %#v", packet.Evidence[0])
	}
	packet.Gates = []Gate{{Name: "evidence_required", Required: true}}
	if result := LintPacket(packet); !result.OK {
		t.Fatalf("context view should satisfy material evidence gate: %#v", result)
	}
}

func TestLintPacketEnforcesRequiredGates(t *testing.T) {
	packet := Packet{
		SchemaVersion: SchemaVersion,
		Task:          "x",
		Gates: []Gate{
			{Name: "exact_tests_required", Required: true},
			{Name: "file_allowlist_required", Required: true},
			{Name: "question_stop_required", Required: true},
		},
	}
	result := LintPacket(packet)
	if result.OK {
		t.Fatalf("expected lint failure")
	}
	if len(result.Findings) != 3 {
		t.Fatalf("expected 3 findings, got %#v", result.Findings)
	}
}

func TestLintPacketEnforcesContextViewRequiredGate(t *testing.T) {
	packet := Packet{
		SchemaVersion: SchemaVersion,
		Task:          "x",
		Gates:         []Gate{{Name: "context_view_required", Required: true}},
	}
	result := LintPacket(packet)
	if result.OK {
		t.Fatalf("expected missing context view to fail")
	}
	if !hasLintFinding(result, "context_view_required", "error") {
		t.Fatalf("expected context_view_required error, got %#v", result.Findings)
	}
}

func TestLintPacketWarnsOnStaleContextView(t *testing.T) {
	packet := Packet{
		SchemaVersion: SchemaVersion,
		Task:          "x",
		Evidence: []Evidence{{
			Kind:    "context_view",
			Label:   "pack",
			Content: `{"pack_id":"abc","fresh":false,"unresolved_gaps":["run context_index"]}`,
		}},
	}
	result := LintPacket(packet)
	if !result.OK {
		t.Fatalf("stale optional context view should warn, not fail: %#v", result)
	}
	if !hasLintFinding(result, "stale_context_view", "warning") || !hasLintFinding(result, "context_view_gaps", "warning") {
		t.Fatalf("expected stale and gap warnings, got %#v", result.Findings)
	}
}

func TestLintPacketEnforcesFreshContextGate(t *testing.T) {
	packet := Packet{
		SchemaVersion: SchemaVersion,
		Task:          "x",
		Gates:         []Gate{{Name: "fresh_context_required", Required: true}},
		Evidence: []Evidence{{
			Kind:    "context_view",
			Label:   "pack",
			Content: `{"pack_id":"abc","fresh":false}`,
		}},
	}
	result := LintPacket(packet)
	if result.OK {
		t.Fatalf("expected stale context view to fail freshness gate")
	}
	if !hasLintFinding(result, "fresh_context_required", "error") {
		t.Fatalf("expected fresh_context_required error, got %#v", result.Findings)
	}
}

func TestLintPacketWarnsOnMalformedContextView(t *testing.T) {
	packet := Packet{
		SchemaVersion: SchemaVersion,
		Task:          "x",
		Evidence: []Evidence{{
			Kind:    "context_view",
			Label:   "pack",
			Content: `{not-json`,
		}},
	}
	result := LintPacket(packet)
	if !result.OK {
		t.Fatalf("malformed optional context view should warn, not fail: %#v", result)
	}
	if !hasLintFinding(result, "context_view_parse", "warning") {
		t.Fatalf("expected parse warning, got %#v", result.Findings)
	}
}

func TestLintPacketFailsRequiredEvidenceErrors(t *testing.T) {
	packet := Packet{
		SchemaVersion: SchemaVersion,
		Task:          "x",
		Evidence: []Evidence{{
			Kind:     "rule",
			Label:    "missing rules",
			Required: true,
			Error:    "open RULES.md: no such file",
		}},
	}
	result := LintPacket(packet)
	if result.OK {
		t.Fatalf("expected lint failure")
	}
	if len(result.Findings) != 1 || !strings.Contains(result.Findings[0].Message, "missing rules") {
		t.Fatalf("unexpected findings: %#v", result.Findings)
	}
}

func TestLintPacketWarnsOnEmptyEvidence(t *testing.T) {
	packet := Packet{
		SchemaVersion: SchemaVersion,
		Task:          "x",
		Evidence: []Evidence{{
			Kind:  "collector",
			Label: "empty collector",
		}},
	}
	result := LintPacket(packet)
	if !result.OK {
		t.Fatalf("empty optional evidence should warn, not fail: %#v", result)
	}
	if len(result.Findings) != 1 || result.Findings[0].Gate != "empty_evidence" || result.Findings[0].Level != "warning" {
		t.Fatalf("unexpected findings: %#v", result.Findings)
	}
}

func TestRenderPromptIncludesPacketAndPrior(t *testing.T) {
	packet := Packet{SchemaVersion: SchemaVersion, Task: "x", Profile: ProfileSummary{Name: "demo"}}
	prompt, err := RenderPrompt(PromptCritique, packet, "candidate plan")
	if err != nil {
		t.Fatalf("RenderPrompt: %v", err)
	}
	if !strings.Contains(prompt, "Architect Critique Task") || !strings.Contains(prompt, "candidate plan") || !strings.Contains(prompt, SchemaVersion) {
		t.Fatalf("prompt missing expected content:\n%s", prompt)
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func hasLintFinding(result LintResult, gate string, level string) bool {
	for _, finding := range result.Findings {
		if finding.Gate == gate && finding.Level == level {
			return true
		}
	}
	return false
}
