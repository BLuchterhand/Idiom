package architect

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Architect packets", func() {
	ginkgo.It("loads profile rules and leaves collectors pending by default", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeFile(filepath.Join(root, "RULES.md"), "Use exact tests.\n")
		writeFile(filepath.Join(root, ".idiom/architect/profile.json"), `{
  "name": "demo",
  "description": "Demo adapter",
  "rules": [{"label": "rules", "path": "RULES.md", "required": true}],
  "collectors": [{"name": "symbols", "command": "echo", "args": ["ok"]}],
  "gates": [{"name": "exact_tests_required", "required": true}],
  "constraints": ["Use verified anchors only."]
}`)
		packet, err := BuildPacket(context.Background(), BuildOptions{
			Root: root, Task: "Plan a feature",
			Files: []FileRef{{Path: "cmd/demo.go", Role: "modify"}},
			Tests: []CommandRef{{Command: "go test ./...", Reason: "full suite"}},
			Now:   time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC),
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(packet.SchemaVersion).To(gomega.Equal(SchemaVersion))
		gomega.Expect(packet.Profile.Name).To(gomega.Equal("demo"))
		gomega.Expect(packet.Evidence).To(gomega.HaveExactElements(
			gomega.And(gomega.HaveField("Kind", "rule"), gomega.HaveField("Content", gomega.ContainSubstring("Use exact tests"))),
			gomega.HaveField("Kind", "collector_pending"),
		))
		gomega.Expect(LintPacket(packet).Findings).To(gomega.BeEmpty())
	})

	ginkgo.It("runs collectors when requested and strips ANSI output", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeFile(filepath.Join(root, "profile.json"), `{
  "name":"demo",
	  "collectors":[{"name":"echo","command":"/bin/sh","args":["-c","printf '\\033[31mcollector-output\\033[0m\n'"]}]
}`)
		packet, err := BuildPacket(context.Background(), BuildOptions{Root: root, Task: "Collect facts", ProfilePath: filepath.Join(root, "profile.json"), RunCollectors: true})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(packet.Evidence).To(gomega.ConsistOf(gomega.And(
			gomega.HaveField("Kind", "collector"),
			gomega.HaveField("Content", gomega.ContainSubstring("collector-output")),
		)))
		gomega.Expect(packet.Evidence[0].Content).NotTo(gomega.ContainSubstring("\x1b["))
	})

	ginkgo.It("preserves collector stdout and stderr after a nonzero exit", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeFile(filepath.Join(root, "profile.json"), `{
  "name":"demo",
  "collectors":[{"name":"failing","command":"/bin/sh","args":["-c","printf 'stdout-line\n'; printf 'stderr-line\n' >&2; exit 7"]}]
}`)
		packet, err := BuildPacket(context.Background(), BuildOptions{Root: root, Task: "Collect failure facts", ProfilePath: filepath.Join(root, "profile.json"), RunCollectors: true})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(packet.Evidence).To(gomega.HaveLen(1))
		gomega.Expect(packet.Evidence[0].Kind).To(gomega.Equal("collector"))
		gomega.Expect(packet.Evidence[0].Content).To(gomega.And(gomega.ContainSubstring("stdout-line"), gomega.ContainSubstring("stderr-line")))
		gomega.Expect(packet.Evidence[0].Error).To(gomega.ContainSubstring("exit status 7"))
	})

	ginkgo.It("reports a collector timeout", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeFile(filepath.Join(root, "profile.json"), `{
  "name":"demo",
  "collectors":[{"name":"slow","command":"/bin/sh","args":["-c","sleep 2"],"timeout_seconds":1}]
}`)
		packet, err := BuildPacket(context.Background(), BuildOptions{Root: root, Task: "Collect timeout facts", ProfilePath: filepath.Join(root, "profile.json"), RunCollectors: true})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(packet.Evidence).To(gomega.ConsistOf(gomega.And(
			gomega.HaveField("Kind", "collector"),
			gomega.HaveField("Error", "collector timed out after 1s"),
		)))
	})

	ginkgo.It("resolves a collector working directory relative to the workspace", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.Mkdir(filepath.Join(root, "nested"), 0o755)).To(gomega.Succeed())
		writeFile(filepath.Join(root, "profile.json"), `{
  "name":"demo",
  "collectors":[{"name":"pwd","command":"/bin/sh","args":["-c","pwd"],"cwd":"nested"}]
}`)
		packet, err := BuildPacket(context.Background(), BuildOptions{Root: root, Task: "Collect cwd facts", ProfilePath: filepath.Join(root, "profile.json"), RunCollectors: true})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(packet.Evidence).To(gomega.HaveLen(1))
		gomega.Expect(strings.TrimSpace(packet.Evidence[0].Content)).To(gomega.Equal(filepath.Join(root, "nested")))
	})

	ginkgo.It("strips ANSI before applying the collector output limit", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeFile(filepath.Join(root, "profile.json"), `{
  "name":"demo",
	  "collectors":[{"name":"color","command":"/bin/sh","args":["-c","printf '\\033[31mabcdefghijklmnop\\033[0m\n'"],"max_bytes":15}]
}`)
		packet, err := BuildPacket(context.Background(), BuildOptions{Root: root, Task: "Collect truncated facts", ProfilePath: filepath.Join(root, "profile.json"), RunCollectors: true})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(packet.Evidence).To(gomega.HaveLen(1))
		gomega.Expect(packet.Evidence[0].Content).To(gomega.Equal("abc\n[truncated]"))
		gomega.Expect(packet.Evidence[0].Content).NotTo(gomega.ContainSubstring("\x1b["))
	})

	ginkgo.It("includes a context view as material evidence", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeFile(filepath.Join(root, ".idiom/comprehension/views/pack.json"), `{"pack_id":"abc","candidate_files":[{"path":"pkg/pkg.go"}]}`+"\n")
		packet, err := BuildPacket(context.Background(), BuildOptions{Root: root, Task: "Use context", ContextViews: []string{".idiom/comprehension/views/pack.json"}})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(packet.Evidence).To(gomega.ConsistOf(gomega.And(
			gomega.HaveField("Kind", "context_view"),
			gomega.HaveField("Content", gomega.ContainSubstring("candidate_files")),
		)))
		packet.Gates = []Gate{{Name: "evidence_required", Required: true}}
		gomega.Expect(LintPacket(packet).OK).To(gomega.BeTrue())
	})

	ginkgo.It("builds planning-v2 budget, freshness, contracts, and candidate scope", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeFile(filepath.Join(root, ".idiom/comprehension/views/pack.json"), `{
  "pack_id":"abc","index_id":"idx1","fresh":false,
  "candidate_files":[{"path":"internal/capabilities/workspace/context_tools.go","role":"modify","reason":"owns context tools","language":"go","confidence":"precise_backend","handles":["file://internal/capabilities/workspace/context_tools.go"]}],
  "candidate_symbols":[{"id":"symbol://internal/capabilities/workspace/context_tools.go#contextPack","path":"internal/capabilities/workspace/context_tools.go","qualified_name":"contextPack","kind":"method","language":"go","confidence":"precise_backend","reason":"builds context packets"}],
  "tests":[{"command":"go test ./internal/capabilities/workspace","reason":"context tool behavior"}],
  "unresolved_gaps":["refresh stale context before edits"]
}`+"\n")
		packet, err := BuildPacket(context.Background(), BuildOptions{
			Root: root, Task: "Plan context packet quality", Schema: "planning_v2", BudgetTier: "brief",
			ContextViews: []string{".idiom/comprehension/views/pack.json"},
			Tests:        []CommandRef{{Command: "go test ./internal/workflows/architect", Reason: "architect packet behavior"}},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(packet.SchemaVersion).To(gomega.Equal(PlanningPacketV2Version))
		gomega.Expect(packet.ContextBudget).NotTo(gomega.BeNil())
		gomega.Expect(packet.ContextBudget.BudgetTier).To(gomega.Equal("brief"))
		gomega.Expect(packet.ContextBudget.EstimatedTokens).To(gomega.BeNumerically(">", 0))
		gomega.Expect(packet.Contracts).NotTo(gomega.BeNil())
		gomega.Expect(packet.Contracts.ValidationRequirements).To(gomega.HaveLen(2))
		gomega.Expect(packet.Freshness).NotTo(gomega.BeNil())
		gomega.Expect(packet.Freshness.Fresh).NotTo(gomega.BeNil())
		gomega.Expect(*packet.Freshness.Fresh).To(gomega.BeFalse())
		gomega.Expect(hasCandidateScope(packet, "file://internal/capabilities/workspace/context_tools.go")).To(gomega.BeTrue())
		gomega.Expect(hasCandidateScope(packet, "symbol://internal/capabilities/workspace/context_tools.go#contextPack")).To(gomega.BeTrue())
		gomega.Expect(packet.LanguageCapabilities).To(gomega.ConsistOf(gomega.And(
			gomega.HaveField("Language", "go"),
			gomega.HaveField("PrecisionTier", "precise_backend"),
		)))
		result := LintPacket(packet)
		gomega.Expect(result.OK).To(gomega.BeTrue())
		gomega.Expect(hasLintFinding(result, "planning_v2_freshness", "warning")).To(gomega.BeTrue())
	})

	ginkgo.It("parses full candidate scope from a context view whose evidence projection is truncated", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeFile(filepath.Join(root, ".idiom/comprehension/views/large.json"), `{
  "pack_id":"large","index_id":"idx-large","fresh":true,
  "candidate_files":[{"path":"cmd/idiom/architect.go","role":"modify","reason":"owns architect CLI","language":"go","confidence":"precise_backend","handles":["file://cmd/idiom/architect.go"]}],
  "tests":[{"command":"go test ./cmd/idiom","reason":"architect CLI smoke coverage"}],
  "padding":"`+strings.Repeat("x", 4096)+`"
}`+"\n")
		packet, err := BuildPacket(context.Background(), BuildOptions{
			Root: root, Task: "Plan architect CLI smoke coverage", Schema: "planning_v2", BudgetTier: "brief",
			ContextViews: []string{".idiom/comprehension/views/large.json"}, MaxOutputBytes: 128,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(packet.Evidence[0].Content).To(gomega.ContainSubstring("[truncated]"))
		gomega.Expect(hasCandidateScope(packet, "file://cmd/idiom/architect.go")).To(gomega.BeTrue())
		gomega.Expect(packet.Unknowns).To(gomega.BeEmpty())
	})

	ginkgo.It("projects candidate scope and precision across multiple languages", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeFile(filepath.Join(root, ".idiom/comprehension/views/multi.json"), `{
  "pack_id":"multi","index_id":"idx-multi","fresh":true,
  "candidate_files":[
    {"path":"internal/runtime/agent/runner.go","role":"modify","reason":"Go runner contract","language":"go","confidence":"precise_backend","handles":["file://internal/runtime/agent/runner.go"]},
    {"path":"native/tests/test_production_graph.cpp","role":"test","reason":"C++ production regression","language":"cpp","confidence":"syntax_backend","handles":["file://native/tests/test_production_graph.cpp"]},
    {"path":"scripts/world/colony_panel.gd","role":"modify","reason":"GDScript UI wiring","language":"gdscript","confidence":"heuristic","handles":["file://scripts/world/colony_panel.gd"]}
  ],
  "candidate_symbols":[
    {"id":"symbol://internal/runtime/agent/runner.go#Runner.Run","path":"internal/runtime/agent/runner.go","qualified_name":"Runner.Run","kind":"method","language":"go","confidence":"precise_backend","reason":"runner loop"},
    {"id":"symbol://native/tests/test_production_graph.cpp#production_graph","path":"native/tests/test_production_graph.cpp","qualified_name":"production_graph","kind":"test","language":"cpp","confidence":"syntax_backend","reason":"native regression"},
    {"id":"symbol://scripts/world/colony_panel.gd#_ready","path":"scripts/world/colony_panel.gd","qualified_name":"_ready","kind":"function","language":"gdscript","confidence":"heuristic","reason":"scene initialization"}
  ],
  "tests":[{"command":"go test ./internal/runtime/agent","reason":"Go runner behavior"},{"command":"scons test=production_graph","reason":"C++ native regression"}]
}`+"\n")
		packet, err := BuildPacket(context.Background(), BuildOptions{Root: root, Task: "Plan a cross-language logistics tracker change", Schema: "planning_v2", BudgetTier: "brief", ContextViews: []string{".idiom/comprehension/views/multi.json"}})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		for _, language := range []string{"go", "cpp", "gdscript"} {
			gomega.Expect(hasLanguageCapability(packet, language)).To(gomega.BeTrue(), "language: %s", language)
		}
		for _, handle := range []string{
			"file://internal/runtime/agent/runner.go",
			"file://native/tests/test_production_graph.cpp",
			"file://scripts/world/colony_panel.gd",
			"symbol://scripts/world/colony_panel.gd#_ready",
		} {
			gomega.Expect(hasCandidateScope(packet, handle)).To(gomega.BeTrue(), "handle: %s", handle)
		}
		gomega.Expect(packet.ContextBudget).NotTo(gomega.BeNil())
		gomega.Expect(packet.ContextBudget.BudgetTier).To(gomega.Equal("brief"))
	})
})

var _ = ginkgo.Describe("Architect packet lint", func() {
	ginkgo.It("enforces every required structural gate", func() {
		result := LintPacket(Packet{SchemaVersion: SchemaVersion, Task: "x", Gates: []Gate{
			{Name: "exact_tests_required", Required: true},
			{Name: "file_allowlist_required", Required: true},
			{Name: "question_stop_required", Required: true},
		}})
		gomega.Expect(result.OK).To(gomega.BeFalse())
		gomega.Expect(result.Findings).To(gomega.HaveLen(3))
	})

	ginkgo.It("enforces a required context view", func() {
		result := LintPacket(Packet{SchemaVersion: SchemaVersion, Task: "x", Gates: []Gate{{Name: "context_view_required", Required: true}}})
		gomega.Expect(result.OK).To(gomega.BeFalse())
		gomega.Expect(hasLintFinding(result, "context_view_required", "error")).To(gomega.BeTrue())
	})

	ginkgo.It("warns on stale optional context and unresolved gaps", func() {
		result := LintPacket(Packet{SchemaVersion: SchemaVersion, Task: "x", Evidence: []Evidence{{
			Kind: "context_view", Label: "pack", Content: `{"pack_id":"abc","fresh":false,"unresolved_gaps":["run context_index"]}`,
		}}})
		gomega.Expect(result.OK).To(gomega.BeTrue())
		gomega.Expect(hasLintFinding(result, "stale_context_view", "warning")).To(gomega.BeTrue())
		gomega.Expect(hasLintFinding(result, "context_view_gaps", "warning")).To(gomega.BeTrue())
	})

	ginkgo.It("enforces fresh context when required", func() {
		result := LintPacket(Packet{
			SchemaVersion: SchemaVersion, Task: "x", Gates: []Gate{{Name: "fresh_context_required", Required: true}},
			Evidence: []Evidence{{Kind: "context_view", Label: "pack", Content: `{"pack_id":"abc","fresh":false}`}},
		})
		gomega.Expect(result.OK).To(gomega.BeFalse())
		gomega.Expect(hasLintFinding(result, "fresh_context_required", "error")).To(gomega.BeTrue())
	})

	ginkgo.It("warns on a malformed optional context view", func() {
		result := LintPacket(Packet{SchemaVersion: SchemaVersion, Task: "x", Evidence: []Evidence{{Kind: "context_view", Label: "pack", Content: `{not-json`}}})
		gomega.Expect(result.OK).To(gomega.BeTrue())
		gomega.Expect(hasLintFinding(result, "context_view_parse", "warning")).To(gomega.BeTrue())
	})

	ginkgo.It("fails when required evidence could not be loaded", func() {
		result := LintPacket(Packet{SchemaVersion: SchemaVersion, Task: "x", Evidence: []Evidence{{Kind: "rule", Label: "missing rules", Required: true, Error: "open RULES.md: no such file"}}})
		gomega.Expect(result.OK).To(gomega.BeFalse())
		gomega.Expect(result.Findings).To(gomega.ConsistOf(gomega.HaveField("Message", gomega.ContainSubstring("missing rules"))))
	})

	ginkgo.It("warns on empty optional evidence", func() {
		result := LintPacket(Packet{SchemaVersion: SchemaVersion, Task: "x", Evidence: []Evidence{{Kind: "collector", Label: "empty collector"}}})
		gomega.Expect(result.OK).To(gomega.BeTrue())
		gomega.Expect(result.Findings).To(gomega.ConsistOf(gomega.And(
			gomega.HaveField("Gate", "empty_evidence"),
			gomega.HaveField("Level", "warning"),
		)))
	})

	ginkgo.It("reports duplicate context evidence in the budget", func() {
		report := BuildContextBudget(Packet{
			SchemaVersion: PlanningPacketV2Version, Task: "x",
			Evidence: []Evidence{{Kind: "rule", Label: "a", Path: "RULES.md", Content: "same"}, {Kind: "rule", Label: "b", Path: "RULES.md", Content: "same"}},
		}, ContextBudgetOptions{Tier: "standard"})
		gomega.Expect(report).NotTo(gomega.BeNil())
		gomega.Expect(report.Warnings).To(gomega.ContainElement(gomega.ContainSubstring("duplicate evidence")))
	})
})

var _ = ginkgo.Describe("Architect plan audit", func() {
	ginkgo.It("finds a referenced file that is missing", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeFile(filepath.Join(root, "internal/workflows/architect/packet.go"), "package architect\n")
		packet := Packet{SchemaVersion: PlanningPacketV2Version, Task: "Implement packet work", Workspace: WorkspaceSummary{Root: root}, Files: []FileRef{{Path: "internal/workflows/architect/packet.go", Role: "modify"}}}
		result := AuditPlan(packet, "Modify internal/workflows/architect/packet.go and internal/workflows/architect/missing.go.")
		gomega.Expect(result.OK).To(gomega.BeFalse())
		gomega.Expect(hasAuditFinding(result, "missing_file", "error")).To(gomega.BeTrue())
	})

	ginkgo.It("extracts long file extensions without truncation", func() {
		root := ginkgo.GinkgoT().TempDir()
		for _, path := range []string{"traces/run.json", "src/feature.cpp", "scenes/main.tscn", "web/view.jsx"} {
			writeFile(filepath.Join(root, path), "fixture\n")
		}
		result := AuditPlan(Packet{SchemaVersion: PlanningPacketV2Version, Task: "Review plan paths", Workspace: WorkspaceSummary{Root: root}}, "Review traces/run.json, src/feature.cpp, scenes/main.tscn, and web/view.jsx.")
		gomega.Expect(result.OK).To(gomega.BeTrue())
		gomega.Expect(hasAuditFinding(result, "missing_file", "error")).To(gomega.BeFalse())
	})

	ginkgo.It("warns on missing validation and stale facts", func() {
		stale := false
		result := AuditPlan(Packet{
			SchemaVersion: PlanningPacketV2Version, Task: "Change shared context behavior",
			Tests:     []CommandRef{{Command: "go test ./internal/workflows/architect", Reason: "packet behavior"}},
			Freshness: &FreshnessReport{Fresh: &stale, StaleNodes: []string{"internal/workflows/architect/packet.go"}},
		}, "Update the packet builder in a bounded atom.")
		gomega.Expect(result.OK).To(gomega.BeTrue())
		gomega.Expect(hasAuditFinding(result, "validation_gap", "warning")).To(gomega.BeTrue())
		gomega.Expect(hasAuditFinding(result, "stale_fact", "warning")).To(gomega.BeTrue())
	})

	ginkgo.It("rejects a missing workspace command path", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeFile(filepath.Join(root, "internal/workflows/architect/packet.go"), "package architect\n")
		result := AuditPlan(Packet{
			SchemaVersion: PlanningPacketV2Version, Task: "Implement packet work", Workspace: WorkspaceSummary{Root: root},
			Tests: []CommandRef{{Command: "bash scripts/missing.sh", Reason: "script smoke"}},
		}, "Validate with `bash scripts/missing.sh` after editing internal/workflows/architect/packet.go.")
		gomega.Expect(result.OK).To(gomega.BeFalse())
		gomega.Expect(hasAuditFinding(result, "unknown_command_path", "error")).To(gomega.BeTrue())
	})

	ginkgo.It("does not treat an absolute external command path as workspace scope", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeFile(filepath.Join(root, "internal/workflows/architect/packet.go"), "package architect\n")
		result := AuditPlan(Packet{SchemaVersion: PlanningPacketV2Version, Task: "Validate external helper", Workspace: WorkspaceSummary{Root: root}}, "Validate with `bash /tmp/idiom-missing-helper.sh` after checking internal/workflows/architect/packet.go.")
		gomega.Expect(hasAuditFinding(result, "unknown_command_path", "error")).To(gomega.BeFalse())
	})

	ginkgo.It("rejects unresolved placeholders", func() {
		result := AuditPlan(Packet{SchemaVersion: PlanningPacketV2Version, Task: "Draft an atom"}, "Update `<path>` and replace `<count>` once evidence is available.")
		gomega.Expect(result.OK).To(gomega.BeFalse())
		gomega.Expect(hasAuditFinding(result, "unresolved_placeholder", "error")).To(gomega.BeTrue())
	})

	ginkgo.It("rejects edits to a protected path", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeFile(filepath.Join(root, "internal/secret.go"), "package internal\n")
		result := AuditPlan(Packet{
			SchemaVersion: PlanningPacketV2Version, Task: "Implement a change", Workspace: WorkspaceSummary{Root: root},
			Contracts: &PlanningContracts{ProtectedPaths: []string{"internal/secret.go"}},
		}, "Modify internal/secret.go to expose the value.")
		gomega.Expect(result.OK).To(gomega.BeFalse())
		gomega.Expect(hasAuditFinding(result, "protected_path", "error")).To(gomega.BeTrue())
	})

	ginkgo.It("reports packet identity and candidate-scope mismatches", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeFile(filepath.Join(root, "internal/workflows/architect/packet.go"), "package architect\n")
		writeFile(filepath.Join(root, "internal/workflows/architect/outside.go"), "package architect\n")
		fresh := true
		packet := Packet{
			SchemaVersion: PlanningPacketV2Version, Task: "Implement packet work", Workspace: WorkspaceSummary{Root: root},
			CandidateScope: []RankedScopeItem{{Handle: "file://internal/workflows/architect/packet.go", Path: "internal/workflows/architect/packet.go", Kind: "file", Rank: 0.9}},
			Freshness:      &FreshnessReport{Fresh: &fresh, ContextViews: []ContextFreshnessSource{{PackID: "feedfacecafebeef"}}},
		}
		result := AuditPlan(packet, "Modify internal/workflows/architect/outside.go using context_pack://deadbeefdeadbeef.")
		gomega.Expect(result.OK).To(gomega.BeFalse())
		gomega.Expect(hasAuditFinding(result, "packet_identity_mismatch", "error")).To(gomega.BeTrue())
		gomega.Expect(hasAuditFinding(result, "scope_path_mismatch", "error")).To(gomega.BeTrue())
		gomega.Expect(result.MismatchMetrics.PacketIdentityMismatches).To(gomega.BeNumerically(">", 0))
		gomega.Expect(result.MismatchMetrics.ScopePathMismatches).To(gomega.BeNumerically(">", 0))
	})
})

var _ = ginkgo.Describe("Architect artifact audit", func() {
	ginkgo.It("compares submitted packet, scope, questions, validation, and approval without leaking private text", func() {
		fresh := false
		packet := Packet{
			SchemaVersion: PlanningPacketV2Version, Task: "Implement submitted artifact audit",
			Contracts:      &PlanningContracts{ValidationRequirements: []CommandRef{{Command: "go test ./internal/workflows/architect"}}},
			CandidateScope: []RankedScopeItem{{Handle: "file://internal/workflows/architect/packet.go", Path: "internal/workflows/architect/packet.go", Kind: "file", Rank: 0.9}},
			Freshness:      &FreshnessReport{IndexID: "idx-current", Fresh: &fresh, StaleNodes: []string{"logs/telemetry/secrets/customer-acme/feedback.jsonl"}, ContextViews: []ContextFreshnessSource{{PackID: "feedfacecafebeef", IndexID: "idx-current", Fresh: &fresh}}},
		}
		result := AuditPlanArtifact(packet, PlanArtifactAuditSubject{
			PackID:                  "deadbeefdeadbeef",
			Packet:                  PlanArtifactPacketAuditSubject{PackID: "deadbeefdeadbeef", Status: "available", IndexID: "idx-old", Freshness: "fresh"},
			Paths:                   PlanArtifactPathsAuditSubject{Editable: []string{"internal/workflows/architect/packet.go"}, Required: []string{"internal/workflows/architect/packet.go"}},
			Evidence:                []PlanArtifactEvidenceAuditSubject{{ID: "evidence-1", Kind: "context_packet", Handle: "context_pack://deadbeefdeadbeef", Path: "logs/telemetry/secrets/customer-acme/feedback.jsonl", Freshness: "fresh"}},
			UnresolvedQuestions:     []PlanArtifactQuestionAuditSubject{{ID: "question-1", Claim: "raw_prompt patient transcript body", Status: "open", RequiredBeforeHandoff: true}},
			VerificationObligations: []PlanArtifactVerifyAuditSubject{{ID: "verification-1", Kind: "focused_tests", Command: "go test ./internal/capabilities/workspace", Status: "open", Required: true}},
			Approval:                PlanArtifactApprovalAuditSubject{Required: true, Status: "pending", ScopeID: "scope-1", ExecutionPermission: "blocked"},
			Handoff:                 PlanArtifactHandoffAuditSubject{ID: "handoff-1", Status: "ready", Ready: true},
			Edits:                   []PlanArtifactEditAuditSubject{{Path: "internal/workflows/architect/outside.go"}},
		})
		gomega.Expect(result.OK).To(gomega.BeFalse())
		for _, kind := range []string{"packet_identity_mismatch", "packet_freshness_mismatch", "scope_path_mismatch", "unresolved_question_mismatch", "validation_gap"} {
			gomega.Expect(hasAuditFinding(result, kind, "")).To(gomega.BeTrue(), "finding: %s", kind)
		}
		encoded, err := json.Marshal(result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(encoded)).NotTo(gomega.Or(
			gomega.ContainSubstring("customer-acme"), gomega.ContainSubstring("customer_acme"),
			gomega.ContainSubstring("raw_prompt"), gomega.ContainSubstring("transcript"),
			gomega.ContainSubstring("logs/telemetry/secrets"),
		))
	})

	ginkgo.It("classifies inherited, missing, overridden, and stale packet state", func() {
		fresh := true
		packet := Packet{
			SchemaVersion: PlanningPacketV2Version, Task: "Audit packet state metrics",
			CandidateScope: []RankedScopeItem{{Handle: "file://internal/workflows/architect/packet.go", Path: "internal/workflows/architect/packet.go", Kind: "file", Rank: 0.9}},
			Freshness:      &FreshnessReport{IndexID: "idx-current", Fresh: &fresh, ContextViews: []ContextFreshnessSource{{PackID: "feedfacecafebeef", IndexID: "idx-current", Fresh: &fresh}}},
		}
		base := PlanArtifactAuditSubject{
			Paths:    PlanArtifactPathsAuditSubject{Editable: []string{"internal/workflows/architect/packet.go"}, Required: []string{"internal/workflows/architect/packet.go"}},
			Approval: PlanArtifactApprovalAuditSubject{Status: "implicit", ScopeID: "scope-1", ExecutionPermission: "execute_all"},
			Handoff:  PlanArtifactHandoffAuditSubject{ID: "handoff-1", Status: "ready", Ready: true},
			Edits:    []PlanArtifactEditAuditSubject{{Path: "internal/workflows/architect/packet.go"}},
		}
		inherited := base
		inherited.PackID = "feedfacecafebeef"
		inherited.Packet = PlanArtifactPacketAuditSubject{PackID: "feedfacecafebeef", Status: "available", Freshness: "fresh", IndexID: "idx-current"}
		gomega.Expect(AuditPlanArtifact(packet, inherited).MismatchMetrics.PacketState).To(gomega.Equal("inherited"))
		gomega.Expect(AuditPlanArtifact(packet, base).MismatchMetrics.PacketState).To(gomega.Equal("missing"))

		overridden := inherited
		overridden.Packet.PackID = "deadc0decafef00d"
		overriddenResult := AuditPlanArtifact(packet, overridden)
		gomega.Expect(overriddenResult.MismatchMetrics.PacketState).To(gomega.Equal("overridden"))
		gomega.Expect(overriddenResult.MismatchMetrics.PacketReason).To(gomega.Equal("packet_id_mirror"))

		staleFresh := false
		stalePacket := packet
		stalePacket.Freshness = &FreshnessReport{IndexID: "idx-current", Fresh: &staleFresh, StaleNodes: []string{"internal/workflows/architect/packet.go"}, ContextViews: []ContextFreshnessSource{{PackID: "feedfacecafebeef", IndexID: "idx-current", Fresh: &staleFresh}}}
		gomega.Expect(AuditPlanArtifact(stalePacket, inherited).MismatchMetrics.PacketState).To(gomega.Equal("stale"))
	})

	ginkgo.It("blocks a ready handoff whose required approval is unsatisfied without leaking its status text", func() {
		packet := Packet{SchemaVersion: PlanningPacketV2Version, Task: "Audit approval handoff", CandidateScope: []RankedScopeItem{{Handle: "file://internal/workflows/architect/packet.go", Path: "internal/workflows/architect/packet.go", Kind: "file", Rank: 0.9}}}
		result := AuditPlanArtifact(packet, PlanArtifactAuditSubject{
			Paths:    PlanArtifactPathsAuditSubject{Editable: []string{"internal/workflows/architect/packet.go"}, Required: []string{"internal/workflows/architect/packet.go"}},
			Approval: PlanArtifactApprovalAuditSubject{Required: true, Status: "pending raw_prompt customer-acme transcript", ScopeID: "scope-1", ExecutionPermission: "blocked"},
			Handoff:  PlanArtifactHandoffAuditSubject{ID: "handoff-1", Status: "ready", Ready: true},
			Edits:    []PlanArtifactEditAuditSubject{{Path: "internal/workflows/architect/packet.go"}},
		})
		gomega.Expect(result.OK).To(gomega.BeFalse())
		gomega.Expect(hasAuditFinding(result, "approval_handoff_mismatch", "error")).To(gomega.BeTrue())
		gomega.Expect(result.MismatchMetrics.ApprovalHandoffMismatches).To(gomega.Equal(1))
		encoded, err := json.Marshal(result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(encoded)).NotTo(gomega.Or(
			gomega.ContainSubstring("customer-acme"), gomega.ContainSubstring("customer_acme"),
			gomega.ContainSubstring("raw_prompt"), gomega.ContainSubstring("transcript"),
		))
	})

	ginkgo.It("keeps pending approval nonblocking while handoff is not ready", func() {
		packet := Packet{SchemaVersion: PlanningPacketV2Version, Task: "Audit non-ready approval handoff", CandidateScope: []RankedScopeItem{{Handle: "file://internal/workflows/architect/packet.go", Path: "internal/workflows/architect/packet.go", Kind: "file", Rank: 0.9}}}
		result := AuditPlanArtifact(packet, PlanArtifactAuditSubject{
			Paths:    PlanArtifactPathsAuditSubject{Editable: []string{"internal/workflows/architect/packet.go"}, Required: []string{"internal/workflows/architect/packet.go"}},
			Approval: PlanArtifactApprovalAuditSubject{Required: true, Status: "pending", ScopeID: "scope-1", ExecutionPermission: "blocked"},
			Handoff:  PlanArtifactHandoffAuditSubject{ID: "handoff-1", Status: "blocked", Ready: false},
			Edits:    []PlanArtifactEditAuditSubject{{Path: "internal/workflows/architect/packet.go"}},
		})
		gomega.Expect(result.OK).To(gomega.BeTrue())
		gomega.Expect(result.MismatchMetrics.ApprovalHandoffMismatches).To(gomega.Equal(1))
		gomega.Expect(hasAuditFinding(result, "approval_handoff_mismatch", "error")).To(gomega.BeFalse())
		gomega.Expect(hasAuditFinding(result, "approval_handoff_mismatch", "warning")).To(gomega.BeTrue())
	})

	ginkgo.It("blocks edits outside a partial approval's approved paths", func() {
		packet := Packet{SchemaVersion: PlanningPacketV2Version, Task: "Audit partial approval drift", CandidateScope: []RankedScopeItem{{Handle: "file://internal/workflows/architect/packet.go", Path: "internal/workflows/architect/packet.go", Kind: "file", Rank: 0.9}}}
		result := AuditPlanArtifact(packet, PlanArtifactAuditSubject{
			Paths:    PlanArtifactPathsAuditSubject{Editable: []string{"internal/workflows/architect/packet.go", "internal/workflows/architect/pending.go"}},
			Approval: PlanArtifactApprovalAuditSubject{Required: true, Status: "partial", ScopeID: "scope-partial", ApprovedPaths: []string{"internal/workflows/architect/packet.go"}, PendingPaths: []string{"internal/workflows/architect/pending.go"}, ExecutionPermission: "execute_approved_scope"},
			Handoff:  PlanArtifactHandoffAuditSubject{ID: "handoff-partial", Status: "ready", Ready: true},
			Edits:    []PlanArtifactEditAuditSubject{{Path: "internal/workflows/architect/pending.go"}},
		})
		gomega.Expect(result.OK).To(gomega.BeFalse())
		gomega.Expect(hasAuditFinding(result, "approval_scope_mismatch", "error")).To(gomega.BeTrue())
		gomega.Expect(result.MismatchMetrics.ApprovalScopeMismatches).To(gomega.BeNumerically(">", 0))
	})
})

var _ = ginkgo.Describe("Architect prompt rendering", func() {
	ginkgo.It("includes the packet identity and prior candidate in a critique prompt", func() {
		packet := Packet{SchemaVersion: SchemaVersion, Task: "x", Profile: ProfileSummary{Name: "demo"}}
		prompt, err := RenderPrompt(PromptCritique, packet, "candidate plan")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(prompt).To(gomega.And(
			gomega.ContainSubstring("Architect Critique Task"),
			gomega.ContainSubstring("candidate plan"),
			gomega.ContainSubstring(SchemaVersion),
		))
	})
})

func writeFile(path string, content string) {
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, []byte(content), 0o644)).To(gomega.Succeed())
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

func hasAuditFinding(result PlanAuditResult, kind string, severity string) bool {
	for _, finding := range result.Findings {
		if finding.Kind == kind && (severity == "" || finding.Severity == severity) {
			return true
		}
	}
	return false
}
