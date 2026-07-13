package product

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/evidence/telemetry"
	"idiom/internal/workflows/productworkflow"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.It("product brief render uses authority preflight coverage gate", func() {
	t := ginkgo.GinkgoT()
	t.Setenv("IDIOM_TELEMETRY", "1")

	dir := writeValidTestEffort(t)
	acceptedPRD := strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1)

	acceptedPRD = strings.Replace(acceptedPRD, "created_at: 2026-05-17T00:00:00Z", "created_at: 2026-05-17T00:00:00Z\naccepted_at: 2026-05-18T00:00:00Z\naccepted_by: reviewer\nacceptance_profile: human-review", 1)
	writeTestFile(t, dir, "prd.md", acceptedPRD)
	writeTestFile(t, dir, "adrs/index.json", `{"schema_version":"v1","effort_id":"test-effort","adrs":[{"adr_id":"ADR-001","title":"Test ADR 1","status":"accepted","path":"ADR-001-test.md","coverage_tier":"unit","prd_refs":["REQ-TEST-001"]},{"adr_id":"ADR-002","title":"Test ADR 2","status":"accepted","path":"ADR-002-test.md","coverage_tier":"unit","prd_refs":["REQ-TEST-001"]}]}`)
	writeTestFile(t, dir, "adrs/ADR-001-test.md", strings.Replace(validTestADRMarkdown(), "coverage_tier: unit", "coverage_tier: unit\nsupersedes:\n  - ADR-002", 1))
	writeTestFile(t, dir, "adrs/ADR-002-test.md", strings.Replace(validTestADRMarkdown(), "adr_id: ADR-001", "adr_id: ADR-002", 1))
	writeTestFile(t, dir, "evidence/traceability_matrix.json", `{
		"schema_version":"idiom.traceability_matrix.v1.fixture",
		"effort_id":"test-effort",
		"task_id":"impl-test",
		"created_at":"2026-05-17T16:00:00Z",
		"matrix":[
			{"evidence_card":"EV-001","prd_requirement":"REQ-TEST-001","adr":"ADR-001","tdd_obligation":"TDD-001","brief":"briefs/test.md","report":"reports/test.json"},
			{"evidence_card":"EV-001","prd_requirement":"REQ-TEST-001","adr":"ADR-002","tdd_obligation":"TDD-001","brief":"briefs/test.md","report":"reports/test.json"}
		]
	}`)

	rec, err := telemetry.Open(telemetry.Options{
		Dir:           t.TempDir(),
		Command:       "product_brief",
		Args:          []string{"product", "brief", "--render"},
		WorkspaceRoot: dir,
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "telemetry.Open: %v", err)

	var stdout, stderr bytes.Buffer

	code := runProductBrief([]string{
		"--effort-root", dir,
		"--render",
		"--brief-output", "briefs/generated.md",
		"--json",
	}, &stdout, &stderr, rec)

	{
		err := rec.CloseWithFields(code, nil)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "rec.Close: %v", err)
	}

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected blocked exit, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out productworkflow.BriefRenderResult

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "expected JSON output: %v\n%s", err, stdout.String())
	}

	gomega.Expect(out.CoverageEvaluation).NotTo(gomega.BeNil(), "brief render should expose coverage gate evaluation: %#v", out)

	found := false

	for _, blocker := range out.CoverageEvaluation.Blockers {
		if blocker.Criterion == "authority_preflight" && blocker.Kind == "adr_supersession_non_reciprocal" {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected authority_preflight supersession blocker, got %#v", out.CoverageEvaluation.Blockers)

	{
		_, err := os.Stat(filepath.Join(dir, "briefs", "generated.md"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "blocked render must not write requested output path, stat err=%v", err)
	}

	record := findTelemetryRecord(readMainTelemetryRecords(t, rec.Path()), "coverage_gate_evaluated")
	gomega.Expect(record).NotTo(gomega.BeNil(), "expected blocked product_brief_render coverage telemetry, got %#v", record)
	gomega.Expect(record["source"]).To(gomega.Equal("product_brief_render"), "expected blocked product_brief_render coverage telemetry, got %#v", record)
	gomega.Expect(record["gate_result"]).To(gomega.Equal("blocked"), "expected blocked product_brief_render coverage telemetry, got %#v", record)
})

var _ = ginkgo.It("product brief render fails closed on missing inputs", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()

	var stdout, stderr bytes.Buffer

	code := runProductBrief([]string{
		"--effort-root", dir,
		"--render",
		"--brief-output", "briefs/blocked.md",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected blocked code 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out productworkflow.BriefRenderResult

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "expected JSON output: %v\n%s", err, stdout.String())
	}

	gomega.Expect(out.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusBlocked), "expected blocked status, got %q", out.Status)

	gomega.Expect(strings.TrimSpace(out.Blocker)).NotTo(gomega.BeEquivalentTo(""), "expected blocker text")

	{
		_, err := os.Stat(filepath.Join(dir, "briefs", "blocked.md"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "blocked render must not write requested output path, stat err=%v", err)
	}
})

var _ = ginkgo.It("product brief render text failure includes next action", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()

	var stdout, stderr bytes.Buffer

	code := runProductBrief([]string{
		"--effort-root", dir,
		"--render",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected blocked code 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	text := stdout.String()
	gomega.Expect(text).To(gomega.ContainSubstring("product brief render: blocked"), "expected actionable text failure output, got stdout=%s stderr=%s", text, stderr.String())
	gomega.Expect(text).To(gomega.ContainSubstring("next:"), "expected actionable text failure output, got stdout=%s stderr=%s", text, stderr.String())
})

var _ = ginkgo.It("product brief render rejects unsafe output path", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	acceptedPRD := strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1)

	acceptedPRD = strings.Replace(acceptedPRD, "created_at: 2026-05-17T00:00:00Z", "created_at: 2026-05-17T00:00:00Z\naccepted_at: 2026-05-18T00:00:00Z\naccepted_by: reviewer\nacceptance_profile: human-review", 1)
	writeTestFile(t, dir, "prd.md", acceptedPRD)

	var stdout, stderr bytes.Buffer

	code := runProductBrief([]string{
		"--effort-root", dir,
		"--render",
		"--brief-output", "../bad.md",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected blocked code 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out productworkflow.BriefRenderResult

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "expected JSON output: %v\n%s", err, stdout.String())
	}

	gomega.Expect(strings.Contains(out.Blocker, "must be effort-relative")).To(gomega.BeTrue(), "expected traversal blocker, got %q", out.Blocker)

	gomega.Expect(strings.Contains(out.NextValidAction, "dispatch from this brief output")).To(gomega.BeFalse(), "blocked output must not claim dispatch readiness, got next action %q", out.NextValidAction)
})

var _ = ginkgo.It("product brief render rejects non briefs output path", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	acceptedPRD := strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1)

	acceptedPRD = strings.Replace(acceptedPRD, "created_at: 2026-05-17T00:00:00Z", "created_at: 2026-05-17T00:00:00Z\naccepted_at: 2026-05-18T00:00:00Z\naccepted_by: reviewer\nacceptance_profile: human-review", 1)
	writeTestFile(t, dir, "prd.md", acceptedPRD)

	var stdout, stderr bytes.Buffer

	code := runProductBrief([]string{
		"--effort-root", dir,
		"--render",
		"--brief-output", "prd.md",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected blocked code 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out productworkflow.BriefRenderResult

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "expected JSON output: %v\n%s", err, stdout.String())
	}

	gomega.Expect(strings.Contains(out.Blocker, "must be under briefs/")).To(gomega.BeTrue(), "expected briefs/ blocker, got %q", out.Blocker)
})

var _ = ginkgo.It("product brief render rejects symlink output path", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	acceptedPRD := strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1)

	acceptedPRD = strings.Replace(acceptedPRD, "created_at: 2026-05-17T00:00:00Z", "created_at: 2026-05-17T00:00:00Z\naccepted_at: 2026-05-18T00:00:00Z\naccepted_by: reviewer\nacceptance_profile: human-review", 1)
	writeTestFile(t, dir, "prd.md", acceptedPRD)

	{
		err := os.MkdirAll(filepath.Join(dir, "briefs"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "mkdir briefs: %v", err)
	}

	{
		err := os.WriteFile(filepath.Join(dir, "target.md"), []byte("target"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "write symlink target: %v", err)
	}

	if err := os.Symlink(filepath.Join(dir, "target.md"), filepath.Join(dir, "briefs", "linked.md")); err != nil {
		ginkgo.Skip(fmt.Sprintf("symlink unavailable: %v", err))
	}

	var stdout, stderr bytes.Buffer

	code := runProductBrief([]string{
		"--effort-root", dir,
		"--render",
		"--brief-output", "briefs/linked.md",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected blocked code 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out productworkflow.BriefRenderResult

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "expected JSON output: %v\n%s", err, stdout.String())
	}

	gomega.Expect(strings.Contains(out.Blocker, "must not be a symlink")).To(gomega.BeTrue(), "expected symlink blocker, got %q", out.Blocker)
})

var _ = ginkgo.It("product brief render writes requested path only", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	acceptedPRD := strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1)

	acceptedPRD = strings.Replace(acceptedPRD, "created_at: 2026-05-17T00:00:00Z", "created_at: 2026-05-17T00:00:00Z\naccepted_at: 2026-05-18T00:00:00Z\naccepted_by: reviewer\nacceptance_profile: human-review", 1)
	writeTestFile(t, dir, "prd.md", acceptedPRD)
	writeTestFile(t, dir, "workflow_state.json", strings.Replace(validTestWorkflowStateJSON(), `"coverage":{"coverage_tier":"unit"}`, `"coverage":{"coverage_tier":"behavior_standard"}`, 1))
	writeTestFile(t, dir, "tdd.json", `{
		"schema_version":"idiom.tdd_obligations.v2.fixture",
		"effort_id":"test-effort",
		"task_id":"impl-test",
		"coverage_tier":"behavior_standard",
		"coverage":{"ears_total":1,"ears_mapped":1,"bdd_total":1,"bdd_with_concrete_data":1,"mutation_min_score":80,"mutation_score":90,"gates_passed":["contract"]},
		"behavioral_coverage":{
			"contract_id":"cov-test",
			"effort_id":"test-effort",
			"task_id":"impl-test",
			"coverage_tier":"behavior_standard",
			"ears":[{"id":"REQ-TEST-001","statement":"When the test requirement is accepted, the system shall preserve typed behavioral coverage.","mapped_refs":["BDD-001"]}],
			"bdd_scenarios":[{"id":"BDD-001","requirement_id":"REQ-TEST-001","given":["accepted PRD and ADR exist"],"when":["brief renders"],"then":["typed behavioral coverage is present"],"concrete_data_refs":["fixtures/test.json"],"tdd_obligations":["TDD-001"]}],
			"design_contracts":[{"id":"DBC-001","boundary":"product brief render","preconditions":["coverage tier is behavior_standard"],"postconditions":["behavioral coverage contract exists"],"invariants":["coverage counters are projections"],"test_refs":["TestProductBriefRenderWritesRequestedPathOnly"],"tdd_obligations":["TDD-001"]}],
			"test_obligations":[{"id":"TDD-001","type":"acceptance","behavior":"test","owner":"test","command":"go test ./...","required_before":"close"}],
			"mutation":{"requirement":"required","min_score":80,"score":90}
		},
		"obligations":[{"id":"TDD-001","type":"acceptance","behavior":"test","owner":"test","command":"go test ./...","required_before":"close","status":"pending"}]
	}`)

	var stdout, stderr bytes.Buffer

	code := runProductBrief([]string{
		"--effort-root", dir,
		"--render",
		"--brief-output", "briefs/generated.md",
		"--json",
	}, &stdout, &stderr, nil)

	var out productworkflow.BriefRenderResult

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "expected JSON output: %v\n%s", err, stdout.String())
	}

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected pass exit, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	gomega.Expect(out.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusPass), "expected pass status, got %#v", out)

	gomega.Expect(out.BriefOutputPath).To(gomega.BeEquivalentTo("briefs/generated.md"), "expected relative output path, got %q", out.BriefOutputPath)

	gomega.Expect(out.Lint).NotTo(gomega.BeNil(), "expected passing brief lint result, got %#v", out.Lint)
	gomega.Expect(out.Lint.Exit).To(gomega.Equal(0), "expected passing brief lint result, got %#v", out.Lint)

	{
		_, err := os.Stat(filepath.Join(dir, "briefs", "generated.md"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "expected brief write at requested path: %v", err)
	}

	{
		_, err := os.Stat(filepath.Join(dir, "briefs", "other.md"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "unexpected write to unrequested path, stat err=%v", err)
	}
})

var _ = ginkgo.It("product brief render scopes read first paths to effort root", func() {
	t := ginkgo.GinkgoT()
	repo := t.TempDir()
	{
		err := os.Mkdir(filepath.Join(repo, ".git"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "mkdir .git: %v", err)
	}

	effort := filepath.Join(repo, "custom", "efforts", "brief-case")
	writeMinimalPassingBriefEffort(t, effort)

	var stdout, stderr bytes.Buffer

	code := runProductBrief([]string{
		"--effort-root", effort,
		"--render",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected pass exit, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out productworkflow.BriefRenderResult

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "expected JSON output: %v\n%s", err, stdout.String())
	}

	gomega.Expect(containsString(out.Generated.FilesToReadFirst, "custom/efforts/brief-case/prd.md")).To(gomega.BeTrue(), "read-first paths must be scoped to requested effort root, got %#v", out.Generated.FilesToReadFirst)

	for _, path := range out.Generated.FilesToReadFirst {
		gomega.Expect(strings.Contains(path, "docs/product-workflows/efforts/closed/prd-adr-brief-workflow/prd.md")).To(gomega.BeFalse(), "read-first path leaked durable fixture path: %#v", out.Generated.FilesToReadFirst)
	}
})

var _ = ginkgo.It("product brief render scopes external read first paths to effort root", func() {
	t := ginkgo.GinkgoT()
	effort := filepath.Join(t.TempDir(), "brief-case")
	writeMinimalPassingBriefEffort(t, effort)

	var stdout, stderr bytes.Buffer

	code := runProductBrief([]string{
		"--effort-root", effort,
		"--render",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected pass exit, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out productworkflow.BriefRenderResult

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "expected JSON output: %v\n%s", err, stdout.String())
	}

	want := filepath.ToSlash(filepath.Join(effort, "prd.md"))
	gomega.Expect(containsString(out.Generated.FilesToReadFirst, want)).To(gomega.BeTrue(), "external read-first paths must be scoped to effort root %q, got %#v", want, out.Generated.FilesToReadFirst)
})

var _ = ginkgo.It("product brief render existing file detection does not depend on cwd", func() {
	t := ginkgo.GinkgoT()
	repo := t.TempDir()
	{
		err := os.Mkdir(filepath.Join(repo, ".git"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "mkdir .git: %v", err)
	}

	writeTestFile(t, repo, "cmd/idiom/product_workflow.go", "package main\n")

	effort := filepath.Join(repo, "docs", "efforts", "brief-case")
	writeMinimalPassingBriefEffort(t, effort)
	writeTestFile(t, effort, "implementation_ledger.jsonl", strings.Replace(validTestImplementationLedgerJSONL(), `"expected_files":["test.go"]`, `"expected_files":["cmd/idiom/product_workflow.go"]`, 1))

	oldWD, err := os.Getwd()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)

	otherWD := t.TempDir()
	{
		err := os.Chdir(otherWD)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)
	}

	defer func() {
		{
			err := os.Chdir(oldWD)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), "restore cwd: %v", err)
		}
	}()

	var stdout, stderr bytes.Buffer

	code := runProductBrief([]string{
		"--effort-root", effort,
		"--render",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected pass exit, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out productworkflow.BriefRenderResult

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "expected JSON output: %v\n%s", err, stdout.String())
	}

	{
		got := out.Generated.FileActions["cmd/idiom/product_workflow.go"]
		gomega.Expect(strings.Contains(got, "Modify existing file")).To(gomega.BeTrue(), "expected existing file action independent of cwd, got %q", got)
	}
})

var _ = ginkgo.It("product brief render text output is actionable", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	acceptedPRD := strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1)

	acceptedPRD = strings.Replace(acceptedPRD, "created_at: 2026-05-17T00:00:00Z", "created_at: 2026-05-17T00:00:00Z\naccepted_at: 2026-05-18T00:00:00Z\naccepted_by: reviewer\nacceptance_profile: human-review", 1)
	writeTestFile(t, dir, "prd.md", acceptedPRD)

	var stdout, stderr bytes.Buffer

	code := runProductBrief([]string{
		"--effort-root", dir,
		"--render",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected pass exit, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	text := stdout.String()
	for _, want := range []string{
		"product brief render: pass",
		"# Brief: Product Workflow Dispatch",
		"## Files To Modify",
		"## Acceptance",
	} {
		gomega.Expect(strings.Contains(text, want)).To(gomega.BeTrue(), "expected text output to contain %q, got:\n%s", want, text)
	}

	{
		_, err := os.Stat(filepath.Join(dir, "briefs", "generated.md"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "render without --brief-output must not write a durable brief, stat err=%v", err)
	}
})

var _ = ginkgo.It("product brief render passes current valid effort fixture", func() {
	t := ginkgo.GinkgoT()
	effortRoot := writeValidTestEffort(t)
	acceptedPRD := strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1)
	acceptedPRD = strings.Replace(acceptedPRD, "created_at: 2026-05-17T00:00:00Z", "created_at: 2026-05-17T00:00:00Z\naccepted_at: 2026-05-18T00:00:00Z\naccepted_by: reviewer\nacceptance_profile: human-review", 1)
	writeTestFile(t, effortRoot, "prd.md", acceptedPRD)

	var stdout, stderr bytes.Buffer

	code := runProductBrief([]string{
		"--effort-root", effortRoot,
		"--render",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected valid fixture brief render to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out productworkflow.BriefRenderResult

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "expected JSON output: %v\n%s", err, stdout.String())
	}

	gomega.Expect(out.Status).To(gomega.Equal(productworkflow.ProductWorkflowStatusPass), "expected pass with lint success, got %#v", out)
	gomega.Expect(out.Lint).NotTo(gomega.BeNil(), "expected pass with lint success, got %#v", out)
	gomega.Expect(out.Lint.Exit).To(gomega.Equal(0), "expected pass with lint success, got %#v", out)

	gomega.Expect(strings.Contains(out.Generated.RenderedBrief, "## Files To Modify")).To(gomega.BeTrue(), "expected rendered brief body in JSON output")
})
