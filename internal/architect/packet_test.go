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

func TestBuildPlanningPacketV2IncludesBudgetFreshnessAndScope(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".idiom/comprehension/views/pack.json"), `{
  "pack_id": "abc",
  "index_id": "idx1",
  "fresh": false,
  "candidate_files": [
    {"path": "internal/workspace/context_tools.go", "role": "modify", "reason": "owns context tools", "language": "go", "confidence": "precise_backend", "handles": ["file://internal/workspace/context_tools.go"]}
  ],
  "candidate_symbols": [
    {"id": "symbol://internal/workspace/context_tools.go#contextPack", "path": "internal/workspace/context_tools.go", "qualified_name": "contextPack", "kind": "method", "language": "go", "confidence": "precise_backend", "reason": "builds context packets"}
  ],
  "tests": [
    {"command": "go test ./internal/workspace", "reason": "context tool behavior"}
  ],
  "unresolved_gaps": ["refresh stale context before edits"]
}`+"\n")

	packet, err := BuildPacket(context.Background(), BuildOptions{
		Root:         root,
		Task:         "Plan context packet quality",
		Schema:       "planning_v2",
		BudgetTier:   "brief",
		ContextViews: []string{".idiom/comprehension/views/pack.json"},
		Tests:        []CommandRef{{Command: "go test ./internal/architect", Reason: "architect packet behavior"}},
	})
	if err != nil {
		t.Fatalf("BuildPacket: %v", err)
	}
	if packet.SchemaVersion != PlanningPacketV2Version {
		t.Fatalf("expected planning v2 schema, got %q", packet.SchemaVersion)
	}
	if packet.ContextBudget == nil || packet.ContextBudget.BudgetTier != "brief" || packet.ContextBudget.EstimatedTokens == 0 {
		t.Fatalf("missing budget report: %#v", packet.ContextBudget)
	}
	if packet.Contracts == nil || len(packet.Contracts.ValidationRequirements) != 2 {
		t.Fatalf("validation requirements were not preserved in contracts: %#v", packet.Contracts)
	}
	if packet.Freshness == nil || packet.Freshness.Fresh == nil || *packet.Freshness.Fresh {
		t.Fatalf("expected stale freshness metadata: %#v", packet.Freshness)
	}
	if !hasCandidateScope(packet, "file://internal/workspace/context_tools.go") || !hasCandidateScope(packet, "symbol://internal/workspace/context_tools.go#contextPack") {
		t.Fatalf("candidate scope missing context handles: %#v", packet.CandidateScope)
	}
	if len(packet.LanguageCapabilities) != 1 || packet.LanguageCapabilities[0].Language != "go" || packet.LanguageCapabilities[0].PrecisionTier != "precise_backend" {
		t.Fatalf("language capabilities missing precision: %#v", packet.LanguageCapabilities)
	}
	result := LintPacket(packet)
	if !result.OK {
		t.Fatalf("planning v2 packet should lint without errors: %#v", result)
	}
	if !hasLintFinding(result, "planning_v2_freshness", "warning") {
		t.Fatalf("expected stale freshness warning, got %#v", result.Findings)
	}
}

func TestBuildPlanningPacketV2IncludesMultiLanguageFixtureScope(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".idiom/comprehension/views/multi.json"), `{
  "pack_id": "multi",
  "index_id": "idx-multi",
  "fresh": true,
  "candidate_files": [
    {"path": "internal/agent/runner.go", "role": "modify", "reason": "Go runner contract", "language": "go", "confidence": "precise_backend", "handles": ["file://internal/agent/runner.go"]},
    {"path": "native/tests/test_production_graph.cpp", "role": "test", "reason": "C++ production regression", "language": "cpp", "confidence": "syntax_backend", "handles": ["file://native/tests/test_production_graph.cpp"]},
    {"path": "scripts/world/colony_panel.gd", "role": "modify", "reason": "GDScript UI wiring", "language": "gdscript", "confidence": "heuristic", "handles": ["file://scripts/world/colony_panel.gd"]}
  ],
  "candidate_symbols": [
    {"id": "symbol://internal/agent/runner.go#Runner.Run", "path": "internal/agent/runner.go", "qualified_name": "Runner.Run", "kind": "method", "language": "go", "confidence": "precise_backend", "reason": "runner loop"},
    {"id": "symbol://native/tests/test_production_graph.cpp#production_graph", "path": "native/tests/test_production_graph.cpp", "qualified_name": "production_graph", "kind": "test", "language": "cpp", "confidence": "syntax_backend", "reason": "native regression"},
    {"id": "symbol://scripts/world/colony_panel.gd#_ready", "path": "scripts/world/colony_panel.gd", "qualified_name": "_ready", "kind": "function", "language": "gdscript", "confidence": "heuristic", "reason": "scene initialization"}
  ],
  "tests": [
    {"command": "go test ./internal/agent", "reason": "Go runner behavior"},
    {"command": "scons test=production_graph", "reason": "C++ native regression"}
  ]
}`+"\n")

	packet, err := BuildPacket(context.Background(), BuildOptions{
		Root:         root,
		Task:         "Plan a cross-language logistics tracker change",
		Schema:       "planning_v2",
		BudgetTier:   "brief",
		ContextViews: []string{".idiom/comprehension/views/multi.json"},
	})
	if err != nil {
		t.Fatalf("BuildPacket: %v", err)
	}
	for _, want := range []string{"go", "cpp", "gdscript"} {
		if !hasLanguageCapability(packet, want) {
			t.Fatalf("expected language capability %q, got %#v", want, packet.LanguageCapabilities)
		}
	}
	for _, want := range []string{
		"file://internal/agent/runner.go",
		"file://native/tests/test_production_graph.cpp",
		"file://scripts/world/colony_panel.gd",
		"symbol://scripts/world/colony_panel.gd#_ready",
	} {
		if !hasCandidateScope(packet, want) {
			t.Fatalf("expected candidate scope %q, got %#v", want, packet.CandidateScope)
		}
	}
	if packet.ContextBudget == nil || packet.ContextBudget.BudgetTier != "brief" {
		t.Fatalf("expected brief context budget, got %#v", packet.ContextBudget)
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

func TestContextBudgetWarnsOnDuplicateEvidence(t *testing.T) {
	packet := Packet{
		SchemaVersion: PlanningPacketV2Version,
		Task:          "x",
		Evidence: []Evidence{
			{Kind: "rule", Label: "a", Path: "RULES.md", Content: "same"},
			{Kind: "rule", Label: "b", Path: "RULES.md", Content: "same"},
		},
	}
	report := BuildContextBudget(packet, ContextBudgetOptions{Tier: "standard"})
	if report == nil || len(report.Warnings) == 0 {
		t.Fatalf("expected duplicate evidence warning, got %#v", report)
	}
	if !containsWarning(report.Warnings, "duplicate evidence") {
		t.Fatalf("expected duplicate evidence warning, got %#v", report.Warnings)
	}
}

func TestAuditPlanFindsMissingFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "internal/architect/packet.go"), "package architect\n")
	packet := Packet{
		SchemaVersion: PlanningPacketV2Version,
		Task:          "Implement packet work",
		Workspace:     WorkspaceSummary{Root: root},
		Files:         []FileRef{{Path: "internal/architect/packet.go", Role: "modify"}},
	}
	result := AuditPlan(packet, "Modify internal/architect/packet.go and internal/architect/missing.go.")
	if result.OK {
		t.Fatalf("expected audit failure")
	}
	if !hasAuditFinding(result, "missing_file", "error") {
		t.Fatalf("expected missing_file error, got %#v", result.Findings)
	}
}

func TestAuditPlanWarnsOnMissingValidationAndStaleFacts(t *testing.T) {
	stale := false
	packet := Packet{
		SchemaVersion: PlanningPacketV2Version,
		Task:          "Change shared context behavior",
		Tests:         []CommandRef{{Command: "go test ./internal/architect", Reason: "packet behavior"}},
		Freshness:     &FreshnessReport{Fresh: &stale, StaleNodes: []string{"internal/architect/packet.go"}},
	}
	result := AuditPlan(packet, "Update the packet builder in a bounded atom.")
	if !result.OK {
		t.Fatalf("warnings should not fail audit: %#v", result)
	}
	if !hasAuditFinding(result, "validation_gap", "warning") || !hasAuditFinding(result, "stale_fact", "warning") {
		t.Fatalf("expected validation and stale warnings, got %#v", result.Findings)
	}
}

func TestAuditPlanRejectsProtectedPathEdit(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "internal/secret.go"), "package internal\n")
	packet := Packet{
		SchemaVersion: PlanningPacketV2Version,
		Task:          "Implement a change",
		Workspace:     WorkspaceSummary{Root: root},
		Contracts:     &PlanningContracts{ProtectedPaths: []string{"internal/secret.go"}},
	}
	result := AuditPlan(packet, "Modify internal/secret.go to expose the value.")
	if result.OK {
		t.Fatalf("expected protected path failure")
	}
	if !hasAuditFinding(result, "protected_path", "error") {
		t.Fatalf("expected protected_path error, got %#v", result.Findings)
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

func hasCandidateScope(packet Packet, handle string) bool {
	for _, item := range packet.CandidateScope {
		if item.Handle == handle {
			return true
		}
	}
	return false
}

func hasLanguageCapability(packet Packet, language string) bool {
	for _, capability := range packet.LanguageCapabilities {
		if capability.Language == language {
			return true
		}
	}
	return false
}

func containsWarning(warnings []string, pattern string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, pattern) {
			return true
		}
	}
	return false
}

func hasAuditFinding(result PlanAuditResult, kind string, severity string) bool {
	for _, finding := range result.Findings {
		if finding.Kind == kind && finding.Severity == severity {
			return true
		}
	}
	return false
}
