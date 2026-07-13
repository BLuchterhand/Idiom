package architect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/workflows/architect"
)

var _ = ginkgo.Describe("architect", func() {

	ginkgo.It("architect packet command writes packet", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeArchitectTestFile(filepath.Join(root, ".idiom/architect/profile.json"), `{
  "name": "demo",
  "gates": [{"name": "exact_tests_required", "required": true}]
}`)
		writeArchitectTestFile(filepath.Join(root, ".idiom/comprehension/views/pack.json"), `{"pack_id":"abc","candidate_files":[{"path":"cmd/idiom/main.go"}]}`+"\n")

		outputPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "packet.json")

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{
			"packet",
			"--root", root,
			"--task", "Plan feature",
			"--file", "cmd/idiom/main.go:modify:entrypoint",
			"--test", "go test ./cmd/idiom:cli",
			"--anchor", `{"path":"src/simulation/job_queue.cpp","symbol":"JobQueue::posted_job_for","lines":"37","why":"C++ method anchor"}`,
			"--context-view", ".idiom/comprehension/views/pack.json",
			"--output", outputPath,
		}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected success, code=%d stderr=%s", code, stderr.String()))
		gomega.Expect(stdout.String()).To(gomega.Equal(""), fmt.Sprintf("output file mode should not print stdout: %q", stdout.String()))

		var packet architect.Packet

		readJSONFile(outputPath, &packet)
		gomega.Expect(packet.Task).To(gomega.Equal("Plan feature"), fmt.Sprintf("unexpected packet: %#v", packet))
		gomega.Expect(packet.Profile.Name).To(gomega.Equal("demo"), fmt.Sprintf("unexpected packet: %#v", packet))
		gomega.Expect(packet.Files).To(gomega.HaveLen(1), fmt.Sprintf("file flag not parsed: %#v", packet.Files))
		gomega.Expect(packet.Files[0].Role).To(gomega.Equal("modify"), fmt.Sprintf("file flag not parsed: %#v", packet.Files))
		gomega.Expect(packet.Anchors).To(gomega.HaveLen(1), fmt.Sprintf("JSON anchor flag not parsed: %#v", packet.Anchors))
		gomega.Expect(packet.Anchors[0].Symbol).To(gomega.Equal("JobQueue::posted_job_for"), fmt.Sprintf("JSON anchor flag not parsed: %#v", packet.Anchors))
		gomega.Expect(packet.Evidence).To(gomega.HaveLen(1), fmt.Sprintf("context view evidence not parsed: %#v", packet.Evidence))
		gomega.Expect(packet.Evidence[0].Kind).To(gomega.Equal("context_view"), fmt.Sprintf("context view evidence not parsed: %#v", packet.Evidence))

	})
	ginkgo.It("architect packet command writes planning v2", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeArchitectTestFile(filepath.Join(root, ".idiom/comprehension/views/pack.json"), `{
  "pack_id": "abc",
  "index_id": "idx1",
  "fresh": true,
  "candidate_files": [{"path":"cmd/idiom/architect.go","language":"go","confidence":"precise_backend"}],
  "tests": [{"command":"go test ./cmd/idiom","reason":"cli behavior"}]
}`+"\n")

		outputPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "packet.json")

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{
			"packet",
			"--root", root,
			"--schema", "planning_v2",
			"--budget-tier", "brief",
			"--task", "Plan feature",
			"--context-view", ".idiom/comprehension/views/pack.json",
			"--output", outputPath,
		}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected success, code=%d stderr=%s", code, stderr.String()))

		var packet architect.Packet

		readJSONFile(outputPath, &packet)
		gomega.Expect(packet.SchemaVersion).To(gomega.Equal(architect.PlanningPacketV2Version), fmt.Sprintf("expected planning v2 packet, got %#v", packet.SchemaVersion))
		gomega.Expect(packet.ContextBudget).NotTo(gomega.BeNil(), fmt.Sprintf("planning v2 budget missing: %#v", packet.ContextBudget))
		gomega.Expect(packet.ContextBudget.BudgetTier).To(gomega.Equal("brief"), fmt.Sprintf("planning v2 budget missing: %#v", packet.ContextBudget))
		gomega.Expect(packet.Contracts).NotTo(gomega.BeNil(), fmt.Sprintf("context view validation not carried into contracts: %#v", packet.Contracts))
		gomega.Expect(packet.Contracts.ValidationRequirements).To(gomega.HaveLen(1), fmt.Sprintf("context view validation not carried into contracts: %#v", packet.Contracts))

	})
	ginkgo.It("architect lint command fails required gate", func() {
		packetPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "packet.json")
		writeArchitectTestFile(packetPath, `{
  "schema_version": "idiom.architect.packet.v1",
  "task": "x",
  "gates": [{"name": "exact_tests_required", "required": true}]
}`)

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"lint", "--packet", packetPath}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected lint failure, code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("exact_tests_required"), fmt.Sprintf("lint output missing gate: %s", stdout.String()))

	})
	ginkgo.It("architect plan audit command fails missing file", func() {
		root := ginkgo.GinkgoT().TempDir()
		packetPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "packet.json")
		writeArchitectTestFile(packetPath, `{
  "schema_version": "idiom.architect.planning_packet.v2",
  "task": "Implement packet work",
  "workspace": {"root": "`+filepath.ToSlash(root)+`"}
}`)

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{
			"plan-audit",
			"--packet", packetPath,
			"--plan", "Modify internal/workflows/architect/missing.go.",
		}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected audit failure, code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("missing_file"), fmt.Sprintf("audit output missing finding: %s", stdout.String()))

	})
	ginkgo.It("architect plan audit command checks submitted plan artifact", func() {
		dir := ginkgo.GinkgoT().TempDir()
		packetPath := filepath.Join(dir, "packet.json")
		artifactPath := filepath.Join(dir, "last.plan.json")

		writeArchitectTestFile(packetPath, `{
  "schema_version": "idiom.architect.planning_packet.v2",
  "task": "Implement submitted artifact audit",
  "candidate_scope": [{"handle":"file://internal/workflows/architect/packet.go","path":"internal/workflows/architect/packet.go","kind":"file","rank":0.9}],
  "freshness": {"index_id":"idx-current","fresh":true,"context_views":[{"pack_id":"feedfacecafebeef","index_id":"idx-current","fresh":true}]},
  "contracts": {"validation_requirements":[{"command":"go test ./internal/workflows/architect"}]}
}`)
		writeArchitectTestFile(artifactPath, `{
  "schema_version": "idiom.plan.v2",
  "pack_id": "deadbeefdeadbeef",
  "packet": {"pack_id":"deadbeefdeadbeef","status":"available","index_id":"idx-old","freshness":"fresh"},
  "paths": {"editable":["internal/workflows/architect/packet.go"],"required":["internal/workflows/architect/packet.go"]},
  "evidence": [{"kind":"context_packet","handle":"context_pack://deadbeefdeadbeef","path":"logs/telemetry/secrets/customer-acme/feedback.jsonl"}],
  "unresolved_questions": [{"id":"question-1","claim":"raw_prompt transcript body","status":"open","required_before_handoff":true}],
  "verification_obligations": [{"id":"verification-1","kind":"focused_tests","command":"go test ./cmd/idiom","status":"open","required":true}],
  "approval": {"required":true,"status":"pending"},
  "handoff": {"id":"handoff-1","status":"ready","ready":true},
  "edits": [{"path":"internal/workflows/architect/outside.go"}]
}`)

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"plan-audit", "--packet", packetPath, "--plan-artifact", artifactPath}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected artifact audit failure, code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))

		for _, want := range []string{"packet_identity_mismatch", "scope_path_mismatch", "unresolved_question_mismatch", "mismatch_metrics"} {
			gomega.Expect(stdout.String()).To(gomega.ContainSubstring(want), fmt.Sprintf("artifact audit output missing %q: %s", want, stdout.String()))

		}

		for _, leaked := range []string{"customer-acme", "customer_acme", "raw_prompt", "transcript", "logs/telemetry/secrets"} {
			gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring(leaked), fmt.Sprintf("artifact audit leaked sensitive marker %q: %s", leaked, stdout.String()))

		}
	})
	ginkgo.It("architect plan audit command blocks ready handoff without approval", func() {
		dir := ginkgo.GinkgoT().TempDir()
		packetPath := filepath.Join(dir, "packet.json")
		artifactPath := filepath.Join(dir, "last.plan.json")

		writeArchitectTestFile(packetPath, `{
  "schema_version": "idiom.architect.planning_packet.v2",
  "task": "Audit approval handoff",
  "candidate_scope": [{"handle":"file://internal/workflows/architect/packet.go","path":"internal/workflows/architect/packet.go","kind":"file","rank":0.9}]
}`)
		writeArchitectTestFile(artifactPath, `{
  "schema_version": "idiom.plan.v2",
  "paths": {"editable":["internal/workflows/architect/packet.go"],"required":["internal/workflows/architect/packet.go"]},
  "approval": {"required":true,"status":"pending raw_prompt customer-acme transcript"},
  "handoff": {"id":"handoff-1","status":"ready","ready":true},
  "edits": [{"path":"internal/workflows/architect/packet.go"}]
}`)

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"plan-audit", "--packet", packetPath, "--plan-artifact", artifactPath}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected approval handoff audit failure, code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))

		for _, want := range []string{`"ok": false`, "approval_handoff_mismatch", `"approval_handoff_mismatches": 1`} {
			gomega.Expect(stdout.String()).To(gomega.ContainSubstring(want), fmt.Sprintf("approval handoff audit output missing %q: %s", want, stdout.String()))

		}

		for _, leaked := range []string{"customer-acme", "customer_acme", "raw_prompt", "transcript"} {
			gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring(leaked), fmt.Sprintf("approval handoff audit leaked sensitive marker %q: %s", leaked, stdout.String()))

		}
	})
	ginkgo.It("architect budget command reports tiers", func() {
		packetPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "packet.json")
		writeArchitectTestFile(packetPath, `{
  "schema_version": "idiom.architect.planning_packet.v2",
  "task": "Plan packet budget",
  "context_budget": {"budget_tier":"brief","token_limit":24000,"estimated_tokens":120,"reserved_output_tokens":6000,"risk":"low"},
  "candidate_scope": [{"handle":"file://internal/workflows/architect/packet.go","path":"internal/workflows/architect/packet.go","kind":"file","rank":0.9}]
}`)

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"budget", "--packet", packetPath, "--tiers", "brief,standard"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected budget success, code=%d stderr=%s", code, stderr.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"budget_tier": "brief"`), fmt.Sprintf("budget output missing tiers or placement probes: %s", stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"placement"`), fmt.Sprintf("budget output missing tiers or placement probes: %s", stdout.String()))

	})
	ginkgo.It("architect packet compare command reports v2 deltas", func() {
		dir := ginkgo.GinkgoT().TempDir()
		basePath := filepath.Join(dir, "base.json")
		candidatePath := filepath.Join(dir, "candidate.json")

		writeArchitectTestFile(basePath, `{
  "schema_version": "idiom.architect.packet.v1",
  "task": "Plan packet compare",
  "tests": [{"command":"go test ./internal/workflows/architect"}],
  "evidence": [{"kind":"rule","content":"Use exact tests."}]
}`)
		writeArchitectTestFile(candidatePath, `{
  "schema_version": "idiom.architect.planning_packet.v2",
  "task": "Plan packet compare",
  "contracts": {"validation_requirements":[{"command":"go test ./internal/workflows/architect"}]},
  "context_budget": {"budget_tier":"brief","token_limit":24000,"estimated_tokens":120,"reserved_output_tokens":6000,"risk":"low"},
  "freshness": {"fresh": true},
  "language_capabilities": [{"language":"go","precision_tier":"precise_backend"}],
  "candidate_scope": [{"handle":"file://internal/workflows/architect/packet.go","path":"internal/workflows/architect/packet.go","kind":"file","rank":0.9}]
}`)

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"packet-compare", "--base", basePath, "--candidate", candidatePath}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected compare success, code=%d stderr=%s", code, stderr.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("candidate adds ranked candidate_scope"), fmt.Sprintf("compare output missing expected delta: %s", stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"label": "candidate"`), fmt.Sprintf("compare output missing expected delta: %s", stdout.String()))

	})
	ginkgo.It("architect replay compare command audits plan variants", func() {
		dir := ginkgo.GinkgoT().TempDir()
		root := ginkgo.GinkgoT().TempDir()
		writeArchitectTestFile(filepath.Join(root, "internal/workflows/architect/packet.go"), "package architect\n")

		basePath := filepath.Join(dir, "base.json")
		candidatePath := filepath.Join(dir, "candidate.json")
		basePlanPath := filepath.Join(dir, "base-plan.md")
		candidatePlanPath := filepath.Join(dir, "candidate-plan.md")

		writeArchitectTestFile(basePath, `{
  "schema_version": "idiom.architect.packet.v1",
  "task": "Implement replay compare",
  "workspace": {"root": "`+filepath.ToSlash(root)+`"},
  "files": [{"path":"internal/workflows/architect/packet.go","role":"modify"}],
  "tests": [{"command":"go test ./internal/workflows/architect"}]
}`)
		writeArchitectTestFile(candidatePath, `{
  "schema_version": "idiom.architect.planning_packet.v2",
  "task": "Implement replay compare",
  "workspace": {"root": "`+filepath.ToSlash(root)+`"},
  "files": [{"path":"internal/workflows/architect/packet.go","role":"modify"}],
  "tests": [{"command":"go test ./internal/workflows/architect"}],
  "contracts": {"validation_requirements":[{"command":"go test ./internal/workflows/architect"}]},
  "candidate_scope": [{"handle":"file://internal/workflows/architect/packet.go","path":"internal/workflows/architect/packet.go","kind":"file","rank":0.9}]
}`)
		writeArchitectTestFile(basePlanPath, "Modify internal/workflows/architect/packet.go.\n")
		writeArchitectTestFile(candidatePlanPath, "Modify internal/workflows/architect/packet.go and validate with `go test ./internal/workflows/architect`.\n")

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"replay-compare", "--base", basePath, "--candidate", candidatePath, "--base-plan", basePlanPath, "--candidate-plan", candidatePlanPath}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected replay compare success, code=%d stderr=%s", code, stderr.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("candidate plan has fewer audit warnings"), fmt.Sprintf("replay compare output missing expected improvement: %s", stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"preferred_variant": "candidate"`), fmt.Sprintf("replay compare output missing expected improvement: %s", stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"mismatch_metrics"`), fmt.Sprintf("replay compare output should expose bounded mismatch metrics: %s", stdout.String()))

	})
	ginkgo.It("architect prompt command renders brief", func() {
		packetPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "packet.json")
		writeArchitectTestFile(packetPath, `{
  "schema_version": "idiom.architect.packet.v1",
  "task": "x",
  "profile": {"name": "demo"}
}`)

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"prompt", "--kind", "brief", "--packet", packetPath, "--prior", "approved plan"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected success, code=%d stderr=%s", code, stderr.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("Architect Brief Task"), fmt.Sprintf("unexpected prompt: %s", stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("approved plan"), fmt.Sprintf("unexpected prompt: %s", stdout.String()))

	})
	ginkgo.It("architect plan alias renders plan prompt", func() {
		packetPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "packet.json")
		writeArchitectTestFile(packetPath, `{
  "schema_version": "idiom.architect.packet.v1",
  "task": "x",
  "profile": {"name": "demo"}
}`)

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"plan", "--packet", packetPath}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected success, code=%d stderr=%s", code, stderr.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("Architect Plan Task"), fmt.Sprintf("unexpected prompt: %s", stdout.String()))

	})
})

func writeArchitectTestFile(path string, content string) {
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, []byte(content), 0o644)).To(gomega.Succeed())

}

func readJSONFile(path string, dst any) {

	data, err := os.ReadFile(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read %s: %v", path, err))
	gomega.Expect(json.Unmarshal(data, dst)).To(gomega.Succeed())

}
