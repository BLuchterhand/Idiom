package architect

import (
	"context"
	"encoding/json"
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

func TestBuildPacketCollectorPreservesOutputOnNonzeroExit(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "profile.json"), `{
  "name": "demo",
  "collectors": [{"name": "failing", "command": "/bin/sh", "args": ["-c", "printf 'stdout-line\n'; printf 'stderr-line\n' >&2; exit 7"]}]
}`)

	packet, err := BuildPacket(context.Background(), BuildOptions{
		Root:          root,
		Task:          "Collect failure facts",
		ProfilePath:   filepath.Join(root, "profile.json"),
		RunCollectors: true,
	})
	if err != nil {
		t.Fatalf("BuildPacket: %v", err)
	}

	if len(packet.Evidence) != 1 || packet.Evidence[0].Kind != "collector" {
		t.Fatalf("collector evidence missing: %#v", packet.Evidence)
	}

	got := packet.Evidence[0]
	if !strings.Contains(got.Content, "stdout-line") || !strings.Contains(got.Content, "stderr-line") {
		t.Fatalf("collector output should include stdout and stderr: %#v", got)
	}
	if !strings.Contains(got.Error, "exit status 7") {
		t.Fatalf("collector error = %q, want exit status", got.Error)
	}
}

func TestBuildPacketCollectorReportsTimeout(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "profile.json"), `{
  "name": "demo",
  "collectors": [{"name": "slow", "command": "/bin/sh", "args": ["-c", "sleep 2"], "timeout_seconds": 1}]
}`)

	packet, err := BuildPacket(context.Background(), BuildOptions{
		Root:          root,
		Task:          "Collect timeout facts",
		ProfilePath:   filepath.Join(root, "profile.json"),
		RunCollectors: true,
	})
	if err != nil {
		t.Fatalf("BuildPacket: %v", err)
	}

	if len(packet.Evidence) != 1 || packet.Evidence[0].Kind != "collector" {
		t.Fatalf("collector evidence missing: %#v", packet.Evidence)
	}

	if packet.Evidence[0].Error != "collector timed out after 1s" {
		t.Fatalf("collector error = %q, want timeout", packet.Evidence[0].Error)
	}
}

func TestBuildPacketCollectorUsesRelativeCwd(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	writeFile(t, filepath.Join(root, "profile.json"), `{
  "name": "demo",
  "collectors": [{"name": "pwd", "command": "/bin/sh", "args": ["-c", "pwd"], "cwd": "nested"}]
}`)

	packet, err := BuildPacket(context.Background(), BuildOptions{
		Root:          root,
		Task:          "Collect cwd facts",
		ProfilePath:   filepath.Join(root, "profile.json"),
		RunCollectors: true,
	})
	if err != nil {
		t.Fatalf("BuildPacket: %v", err)
	}

	want := filepath.Join(root, "nested")
	if len(packet.Evidence) != 1 || strings.TrimSpace(packet.Evidence[0].Content) != want {
		t.Fatalf("collector content = %#v, want cwd %q", packet.Evidence, want)
	}
}

func TestBuildPacketCollectorTruncatesAfterANSIStripping(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "profile.json"), `{
  "name": "demo",
  "collectors": [{"name": "color", "command": "/bin/sh", "args": ["-c", "printf '\\033[31mabcdefghijklmnop\\033[0m\\n'"], "max_bytes": 15}]
}`)

	packet, err := BuildPacket(context.Background(), BuildOptions{
		Root:          root,
		Task:          "Collect truncated facts",
		ProfilePath:   filepath.Join(root, "profile.json"),
		RunCollectors: true,
	})
	if err != nil {
		t.Fatalf("BuildPacket: %v", err)
	}

	if len(packet.Evidence) != 1 {
		t.Fatalf("collector evidence missing: %#v", packet.Evidence)
	}
	content := packet.Evidence[0].Content
	if strings.Contains(content, "\x1b[") {
		t.Fatalf("collector content should strip ANSI before truncating: %#v", packet.Evidence[0])
	}
	if content != "abc\n[truncated]" {
		t.Fatalf("collector content = %q, want stripped truncation marker", content)
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
    {"path": "internal/capabilities/workspace/context_tools.go", "role": "modify", "reason": "owns context tools", "language": "go", "confidence": "precise_backend", "handles": ["file://internal/capabilities/workspace/context_tools.go"]}
  ],
  "candidate_symbols": [
    {"id": "symbol://internal/capabilities/workspace/context_tools.go#contextPack", "path": "internal/capabilities/workspace/context_tools.go", "qualified_name": "contextPack", "kind": "method", "language": "go", "confidence": "precise_backend", "reason": "builds context packets"}
  ],
  "tests": [
    {"command": "go test ./internal/capabilities/workspace", "reason": "context tool behavior"}
  ],
  "unresolved_gaps": ["refresh stale context before edits"]
}`+"\n")

	packet, err := BuildPacket(context.Background(), BuildOptions{
		Root:         root,
		Task:         "Plan context packet quality",
		Schema:       "planning_v2",
		BudgetTier:   "brief",
		ContextViews: []string{".idiom/comprehension/views/pack.json"},
		Tests:        []CommandRef{{Command: "go test ./internal/workflows/architect", Reason: "architect packet behavior"}},
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

	if !hasCandidateScope(packet, "file://internal/capabilities/workspace/context_tools.go") || !hasCandidateScope(packet, "symbol://internal/capabilities/workspace/context_tools.go#contextPack") {
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

func TestBuildPlanningPacketV2ParsesTruncatedContextViewFromPath(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".idiom/comprehension/views/large.json"), `{
  "pack_id": "large",
  "index_id": "idx-large",
  "fresh": true,
  "candidate_files": [
    {"path": "cmd/idiom/architect.go", "role": "modify", "reason": "owns architect CLI", "language": "go", "confidence": "precise_backend", "handles": ["file://cmd/idiom/architect.go"]}
  ],
  "tests": [
    {"command": "go test ./cmd/idiom", "reason": "architect CLI smoke coverage"}
  ],
  "padding": "`+strings.Repeat("x", 4096)+`"
}`+"\n")

	packet, err := BuildPacket(context.Background(), BuildOptions{
		Root:           root,
		Task:           "Plan architect CLI smoke coverage",
		Schema:         "planning_v2",
		BudgetTier:     "brief",
		ContextViews:   []string{".idiom/comprehension/views/large.json"},
		MaxOutputBytes: 128,
	})
	if err != nil {
		t.Fatalf("BuildPacket: %v", err)
	}

	if !strings.Contains(packet.Evidence[0].Content, "[truncated]") {
		t.Fatalf("test setup expected truncated evidence content: %#v", packet.Evidence[0])
	}

	if !hasCandidateScope(packet, "file://cmd/idiom/architect.go") {
		t.Fatalf("candidate scope should be parsed from full context view path, got %#v", packet.CandidateScope)
	}

	if len(packet.Unknowns) != 0 {
		t.Fatalf("full context view fallback should avoid parse unknowns, got %#v", packet.Unknowns)
	}
}

func TestBuildPlanningPacketV2IncludesMultiLanguageFixtureScope(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".idiom/comprehension/views/multi.json"), `{
  "pack_id": "multi",
  "index_id": "idx-multi",
  "fresh": true,
  "candidate_files": [
    {"path": "internal/runtime/agent/runner.go", "role": "modify", "reason": "Go runner contract", "language": "go", "confidence": "precise_backend", "handles": ["file://internal/runtime/agent/runner.go"]},
    {"path": "native/tests/test_production_graph.cpp", "role": "test", "reason": "C++ production regression", "language": "cpp", "confidence": "syntax_backend", "handles": ["file://native/tests/test_production_graph.cpp"]},
    {"path": "scripts/world/colony_panel.gd", "role": "modify", "reason": "GDScript UI wiring", "language": "gdscript", "confidence": "heuristic", "handles": ["file://scripts/world/colony_panel.gd"]}
  ],
  "candidate_symbols": [
    {"id": "symbol://internal/runtime/agent/runner.go#Runner.Run", "path": "internal/runtime/agent/runner.go", "qualified_name": "Runner.Run", "kind": "method", "language": "go", "confidence": "precise_backend", "reason": "runner loop"},
    {"id": "symbol://native/tests/test_production_graph.cpp#production_graph", "path": "native/tests/test_production_graph.cpp", "qualified_name": "production_graph", "kind": "test", "language": "cpp", "confidence": "syntax_backend", "reason": "native regression"},
    {"id": "symbol://scripts/world/colony_panel.gd#_ready", "path": "scripts/world/colony_panel.gd", "qualified_name": "_ready", "kind": "function", "language": "gdscript", "confidence": "heuristic", "reason": "scene initialization"}
  ],
  "tests": [
    {"command": "go test ./internal/runtime/agent", "reason": "Go runner behavior"},
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
		"file://internal/runtime/agent/runner.go",
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
	writeFile(t, filepath.Join(root, "internal/workflows/architect/packet.go"), "package architect\n")

	packet := Packet{
		SchemaVersion: PlanningPacketV2Version,
		Task:          "Implement packet work",
		Workspace:     WorkspaceSummary{Root: root},
		Files:         []FileRef{{Path: "internal/workflows/architect/packet.go", Role: "modify"}},
	}
	result := AuditPlan(packet, "Modify internal/workflows/architect/packet.go and internal/workflows/architect/missing.go.")

	if result.OK {
		t.Fatalf("expected audit failure")
	}

	if !hasAuditFinding(result, "missing_file", "error") {
		t.Fatalf("expected missing_file error, got %#v", result.Findings)
	}
}

func TestAuditPlanDoesNotTruncateLongerExtensions(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{
		"traces/run.json",
		"src/feature.cpp",
		"scenes/main.tscn",
		"web/view.jsx",
	} {
		writeFile(t, filepath.Join(root, path), "fixture\n")
	}

	packet := Packet{
		SchemaVersion: PlanningPacketV2Version,
		Task:          "Review plan paths",
		Workspace:     WorkspaceSummary{Root: root},
	}
	result := AuditPlan(packet, "Review traces/run.json, src/feature.cpp, scenes/main.tscn, and web/view.jsx.")

	if !result.OK {
		t.Fatalf("expected existing long-extension paths to pass, got %#v", result.Findings)
	}

	if hasAuditFinding(result, "missing_file", "error") {
		t.Fatalf("path extraction truncated longer extension: %#v", result.Findings)
	}
}

func TestAuditPlanWarnsOnMissingValidationAndStaleFacts(t *testing.T) {
	stale := false
	packet := Packet{
		SchemaVersion: PlanningPacketV2Version,
		Task:          "Change shared context behavior",
		Tests:         []CommandRef{{Command: "go test ./internal/workflows/architect", Reason: "packet behavior"}},
		Freshness:     &FreshnessReport{Fresh: &stale, StaleNodes: []string{"internal/workflows/architect/packet.go"}},
	}
	result := AuditPlan(packet, "Update the packet builder in a bounded atom.")

	if !result.OK {
		t.Fatalf("warnings should not fail audit: %#v", result)
	}

	if !hasAuditFinding(result, "validation_gap", "warning") || !hasAuditFinding(result, "stale_fact", "warning") {
		t.Fatalf("expected validation and stale warnings, got %#v", result.Findings)
	}
}

func TestAuditPlanRejectsMissingCommandPath(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "internal/workflows/architect/packet.go"), "package architect\n")

	packet := Packet{
		SchemaVersion: PlanningPacketV2Version,
		Task:          "Implement packet work",
		Workspace:     WorkspaceSummary{Root: root},
		Tests:         []CommandRef{{Command: "bash scripts/missing.sh", Reason: "script smoke"}},
	}
	result := AuditPlan(packet, "Validate with `bash scripts/missing.sh` after editing internal/workflows/architect/packet.go.")

	if result.OK {
		t.Fatalf("expected missing command path failure")
	}

	if !hasAuditFinding(result, "unknown_command_path", "error") {
		t.Fatalf("expected unknown_command_path error, got %#v", result.Findings)
	}
}

func TestAuditPlanDoesNotTreatAbsoluteCommandPathAsWorkspacePath(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "internal/workflows/architect/packet.go"), "package architect\n")

	packet := Packet{
		SchemaVersion: PlanningPacketV2Version,
		Task:          "Validate external helper",
		Workspace:     WorkspaceSummary{Root: root},
	}
	result := AuditPlan(packet, "Validate with `bash /tmp/idiom-missing-helper.sh` after checking internal/workflows/architect/packet.go.")

	if hasAuditFinding(result, "unknown_command_path", "error") {
		t.Fatalf("absolute external command path should not be audited as workspace path: %#v", result.Findings)
	}
}

func TestAuditPlanRejectsUnresolvedPlaceholder(t *testing.T) {
	packet := Packet{
		SchemaVersion: PlanningPacketV2Version,
		Task:          "Draft an atom",
	}
	result := AuditPlan(packet, "Update `<path>` and replace `<count>` once evidence is available.")

	if result.OK {
		t.Fatalf("expected unresolved placeholder failure")
	}

	if !hasAuditFinding(result, "unresolved_placeholder", "error") {
		t.Fatalf("expected unresolved_placeholder error, got %#v", result.Findings)
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

func TestAuditPlanReportsPacketScopeAndIdentityMismatches(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "internal/workflows/architect/packet.go"), "package architect\n")
	writeFile(t, filepath.Join(root, "internal/workflows/architect/outside.go"), "package architect\n")

	fresh := true
	packet := Packet{
		SchemaVersion: PlanningPacketV2Version,
		Task:          "Implement packet work",
		Workspace:     WorkspaceSummary{Root: root},
		CandidateScope: []RankedScopeItem{{
			Handle: "file://internal/workflows/architect/packet.go",
			Path:   "internal/workflows/architect/packet.go",
			Kind:   "file",
			Rank:   0.9,
		}},
		Freshness: &FreshnessReport{Fresh: &fresh, ContextViews: []ContextFreshnessSource{{PackID: "feedfacecafebeef"}}},
	}
	result := AuditPlan(packet, "Modify internal/workflows/architect/outside.go using context_pack://deadbeefdeadbeef.")

	if result.OK {
		t.Fatalf("expected packet/scope mismatch failure")
	}

	if !hasAuditFinding(result, "packet_identity_mismatch", "error") || !hasAuditFinding(result, "scope_path_mismatch", "error") {
		t.Fatalf("expected packet identity and scope findings, got %#v", result.Findings)
	}

	if result.MismatchMetrics.PacketIdentityMismatches == 0 || result.MismatchMetrics.ScopePathMismatches == 0 {
		t.Fatalf("expected mismatch metrics, got %#v", result.MismatchMetrics)
	}
}

func TestAuditPlanArtifactComparesSubmittedFieldsPrivately(t *testing.T) {
	fresh := false
	packet := Packet{
		SchemaVersion: PlanningPacketV2Version,
		Task:          "Implement submitted artifact audit",
		Contracts: &PlanningContracts{
			ValidationRequirements: []CommandRef{{Command: "go test ./internal/workflows/architect"}},
		},
		CandidateScope: []RankedScopeItem{{
			Handle: "file://internal/workflows/architect/packet.go",
			Path:   "internal/workflows/architect/packet.go",
			Kind:   "file",
			Rank:   0.9,
		}},
		Freshness: &FreshnessReport{
			IndexID:      "idx-current",
			Fresh:        &fresh,
			StaleNodes:   []string{"logs/telemetry/secrets/customer-acme/feedback.jsonl"},
			ContextViews: []ContextFreshnessSource{{PackID: "feedfacecafebeef", IndexID: "idx-current", Fresh: &fresh}},
		},
	}
	result := AuditPlanArtifact(packet, PlanArtifactAuditSubject{
		PackID: "deadbeefdeadbeef",
		Packet: PlanArtifactPacketAuditSubject{
			PackID:    "deadbeefdeadbeef",
			Status:    "available",
			IndexID:   "idx-old",
			Freshness: "fresh",
		},
		Paths: PlanArtifactPathsAuditSubject{
			Editable: []string{"internal/workflows/architect/packet.go"},
			Required: []string{"internal/workflows/architect/packet.go"},
		},
		Evidence: []PlanArtifactEvidenceAuditSubject{{
			ID:        "evidence-1",
			Kind:      "context_packet",
			Handle:    "context_pack://deadbeefdeadbeef",
			Path:      "logs/telemetry/secrets/customer-acme/feedback.jsonl",
			Freshness: "fresh",
		}},
		UnresolvedQuestions: []PlanArtifactQuestionAuditSubject{{
			ID:                    "question-1",
			Claim:                 "raw_prompt patient transcript body",
			Status:                "open",
			RequiredBeforeHandoff: true,
		}},
		VerificationObligations: []PlanArtifactVerifyAuditSubject{{
			ID:       "verification-1",
			Kind:     "focused_tests",
			Command:  "go test ./internal/capabilities/workspace",
			Status:   "open",
			Required: true,
		}},
		Approval: PlanArtifactApprovalAuditSubject{Required: true, Status: "pending", ScopeID: "scope-1", ExecutionPermission: "blocked"},
		Handoff:  PlanArtifactHandoffAuditSubject{ID: "handoff-1", Status: "ready", Ready: true},
		Edits:    []PlanArtifactEditAuditSubject{{Path: "internal/workflows/architect/outside.go"}},
	})

	if result.OK {
		t.Fatalf("expected artifact audit failure")
	}

	for _, want := range []string{"packet_identity_mismatch", "packet_freshness_mismatch", "scope_path_mismatch", "unresolved_question_mismatch", "validation_gap"} {
		if !hasAuditFinding(result, want, "") {
			t.Fatalf("expected finding kind %q, got %#v", want, result.Findings)
		}
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}

	for _, leaked := range []string{"customer-acme", "customer_acme", "raw_prompt", "transcript", "logs/telemetry/secrets"} {
		if strings.Contains(string(encoded), leaked) {
			t.Fatalf("artifact audit leaked sensitive marker %q: %s", leaked, encoded)
		}
	}
}

func TestAuditPlanArtifactReportsPacketStateMetrics(t *testing.T) {
	fresh := true
	packet := Packet{
		SchemaVersion: PlanningPacketV2Version,
		Task:          "Audit packet state metrics",
		CandidateScope: []RankedScopeItem{{
			Handle: "file://internal/workflows/architect/packet.go",
			Path:   "internal/workflows/architect/packet.go",
			Kind:   "file",
			Rank:   0.9,
		}},
		Freshness: &FreshnessReport{
			IndexID:      "idx-current",
			Fresh:        &fresh,
			ContextViews: []ContextFreshnessSource{{PackID: "feedfacecafebeef", IndexID: "idx-current", Fresh: &fresh}},
		},
	}
	base := PlanArtifactAuditSubject{
		Paths: PlanArtifactPathsAuditSubject{
			Editable: []string{"internal/workflows/architect/packet.go"},
			Required: []string{"internal/workflows/architect/packet.go"},
		},
		Approval: PlanArtifactApprovalAuditSubject{Status: "implicit", ScopeID: "scope-1", ExecutionPermission: "execute_all"},
		Handoff:  PlanArtifactHandoffAuditSubject{ID: "handoff-1", Status: "ready", Ready: true},
		Edits:    []PlanArtifactEditAuditSubject{{Path: "internal/workflows/architect/packet.go"}},
	}
	inherited := base

	inherited.PackID = "feedfacecafebeef"
	inherited.Packet = PlanArtifactPacketAuditSubject{PackID: "feedfacecafebeef", Status: "available", Freshness: "fresh", IndexID: "idx-current"}

	if result := AuditPlanArtifact(packet, inherited); result.MismatchMetrics.PacketState != "inherited" {
		t.Fatalf("expected inherited packet state, got %#v", result.MismatchMetrics)
	}

	missing := base
	if result := AuditPlanArtifact(packet, missing); result.MismatchMetrics.PacketState != "missing" {
		t.Fatalf("expected missing packet state, got %#v", result.MismatchMetrics)
	}

	overridden := inherited

	overridden.Packet.PackID = "deadc0decafef00d"

	if result := AuditPlanArtifact(packet, overridden); result.MismatchMetrics.PacketState != "overridden" || result.MismatchMetrics.PacketReason != "packet_id_mirror" {
		t.Fatalf("expected overridden packet state, got %#v", result.MismatchMetrics)
	}

	staleFresh := false
	stalePacket := packet

	stalePacket.Freshness = &FreshnessReport{
		IndexID:      "idx-current",
		Fresh:        &staleFresh,
		StaleNodes:   []string{"internal/workflows/architect/packet.go"},
		ContextViews: []ContextFreshnessSource{{PackID: "feedfacecafebeef", IndexID: "idx-current", Fresh: &staleFresh}},
	}

	if result := AuditPlanArtifact(stalePacket, inherited); result.MismatchMetrics.PacketState != "stale" {
		t.Fatalf("expected stale packet state, got %#v", result.MismatchMetrics)
	}
}

func TestAuditPlanArtifactBlocksReadyHandoffWithoutRequiredApproval(t *testing.T) {
	packet := Packet{
		SchemaVersion: PlanningPacketV2Version,
		Task:          "Audit approval handoff",
		CandidateScope: []RankedScopeItem{{
			Handle: "file://internal/workflows/architect/packet.go",
			Path:   "internal/workflows/architect/packet.go",
			Kind:   "file",
			Rank:   0.9,
		}},
	}
	result := AuditPlanArtifact(packet, PlanArtifactAuditSubject{
		Paths: PlanArtifactPathsAuditSubject{
			Editable: []string{"internal/workflows/architect/packet.go"},
			Required: []string{"internal/workflows/architect/packet.go"},
		},
		Approval: PlanArtifactApprovalAuditSubject{
			Required:            true,
			Status:              "pending raw_prompt customer-acme transcript",
			ScopeID:             "scope-1",
			ExecutionPermission: "blocked",
		},
		Handoff: PlanArtifactHandoffAuditSubject{
			ID:     "handoff-1",
			Status: "ready",
			Ready:  true,
		},
		Edits: []PlanArtifactEditAuditSubject{{Path: "internal/workflows/architect/packet.go"}},
	})

	if result.OK {
		t.Fatalf("expected ready handoff with unsatisfied required approval to fail")
	}

	if !hasAuditFinding(result, "approval_handoff_mismatch", "error") {
		t.Fatalf("expected blocking approval handoff finding, got %#v", result.Findings)
	}

	if result.MismatchMetrics.ApprovalHandoffMismatches != 1 {
		t.Fatalf("expected approval handoff metric, got %#v", result.MismatchMetrics)
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}

	for _, leaked := range []string{"customer-acme", "customer_acme", "raw_prompt", "transcript"} {
		if strings.Contains(string(encoded), leaked) {
			t.Fatalf("approval handoff audit leaked sensitive marker %q: %s", leaked, encoded)
		}
	}
}

func TestAuditPlanArtifactAllowsPendingApprovalWhenHandoffNotReady(t *testing.T) {
	packet := Packet{
		SchemaVersion: PlanningPacketV2Version,
		Task:          "Audit non-ready approval handoff",
		CandidateScope: []RankedScopeItem{{
			Handle: "file://internal/workflows/architect/packet.go",
			Path:   "internal/workflows/architect/packet.go",
			Kind:   "file",
			Rank:   0.9,
		}},
	}
	result := AuditPlanArtifact(packet, PlanArtifactAuditSubject{
		Paths: PlanArtifactPathsAuditSubject{
			Editable: []string{"internal/workflows/architect/packet.go"},
			Required: []string{"internal/workflows/architect/packet.go"},
		},
		Approval: PlanArtifactApprovalAuditSubject{Required: true, Status: "pending", ScopeID: "scope-1", ExecutionPermission: "blocked"},
		Handoff:  PlanArtifactHandoffAuditSubject{ID: "handoff-1", Status: "blocked", Ready: false},
		Edits:    []PlanArtifactEditAuditSubject{{Path: "internal/workflows/architect/packet.go"}},
	})

	if !result.OK {
		t.Fatalf("pending approval should remain non-blocking while handoff is not ready: %#v", result)
	}

	if result.MismatchMetrics.ApprovalHandoffMismatches != 1 {
		t.Fatalf("expected visible approval handoff metric, got %#v", result.MismatchMetrics)
	}

	if hasAuditFinding(result, "approval_handoff_mismatch", "error") {
		t.Fatalf("non-ready handoff should not emit blocking approval finding: %#v", result.Findings)
	}

	if !hasAuditFinding(result, "approval_handoff_mismatch", "warning") {
		t.Fatalf("non-ready handoff should keep warning signal visible: %#v", result.Findings)
	}
}

func TestAuditPlanArtifactBlocksPartialApprovalDrift(t *testing.T) {
	packet := Packet{
		SchemaVersion: PlanningPacketV2Version,
		Task:          "Audit partial approval drift",
		CandidateScope: []RankedScopeItem{{
			Handle: "file://internal/workflows/architect/packet.go",
			Path:   "internal/workflows/architect/packet.go",
			Kind:   "file",
			Rank:   0.9,
		}},
	}
	result := AuditPlanArtifact(packet, PlanArtifactAuditSubject{
		Paths: PlanArtifactPathsAuditSubject{
			Editable: []string{"internal/workflows/architect/packet.go", "internal/workflows/architect/pending.go"},
		},
		Approval: PlanArtifactApprovalAuditSubject{
			Required:            true,
			Status:              "partial",
			ScopeID:             "scope-partial",
			ApprovedPaths:       []string{"internal/workflows/architect/packet.go"},
			PendingPaths:        []string{"internal/workflows/architect/pending.go"},
			ExecutionPermission: "execute_approved_scope",
		},
		Handoff: PlanArtifactHandoffAuditSubject{ID: "handoff-partial", Status: "ready", Ready: true},
		Edits:   []PlanArtifactEditAuditSubject{{Path: "internal/workflows/architect/pending.go"}},
	})

	if result.OK {
		t.Fatalf("partial approval should block edits outside approved scope")
	}

	if !hasAuditFinding(result, "approval_scope_mismatch", "error") {
		t.Fatalf("expected approval scope mismatch, got %#v", result.Findings)
	}

	if result.MismatchMetrics.ApprovalScopeMismatches == 0 {
		t.Fatalf("expected approval scope mismatch metric, got %#v", result.MismatchMetrics)
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
		if finding.Kind == kind && (severity == "" || finding.Severity == severity) {
			return true
		}
	}

	return false
}
