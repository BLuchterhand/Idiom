package product

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/evidence/telemetry"
	"idiom/internal/workflows/productworkflow"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.It("product tdd coverage gate reports missing tdd evidence", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "implementation_ledger.jsonl", implementationLedgerWithoutTDDEvidenceJSONL())

	var stdout, stderr bytes.Buffer

	code := runProductLint([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected lint to fail on missing TDD evidence, got code=%d", code)

	var result productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	found := false

	for _, f := range result.Findings {
		if f.Code == "coverage_tdd_evidence_missing" && f.Path == "tdd.json" {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected coverage_tdd_evidence_missing finding, got: %#v", result.Findings)
})

var _ = ginkgo.It("product tdd plan obligations synthesizes plan", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	acceptedPRD := strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1)

	acceptedPRD = strings.Replace(acceptedPRD, "created_at: 2026-05-17T00:00:00Z", "created_at: 2026-05-17T00:00:00Z\naccepted_at: 2026-05-18T00:00:00Z\naccepted_by: reviewer\nacceptance_profile: human-review", 1)
	writeTestFile(t, dir, "prd.md", acceptedPRD)

	beforeTDD, err := os.ReadFile(filepath.Join(dir, "tdd.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)

	beforePRD, err := os.ReadFile(filepath.Join(dir, "prd.md"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)

	beforeADR, err := os.ReadFile(filepath.Join(dir, "adrs", "ADR-001-test.md"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)

	var stdout, stderr bytes.Buffer

	code := runProductTDD([]string{
		"--effort-root", dir,
		"--plan-obligations",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected code 0, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out struct {
		Status string `json:"status"`
		Plan   struct {
			Obligations []productworkflow.TDDObligationPlanItem    `json:"obligations"`
			Findings    []productworkflow.TDDObligationPlanFinding `json:"findings"`
		} `json:"plan"`
	}

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(out.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusPass), "expected pass, got %q", out.Status)

	gomega.Expect(len(out.Plan.Obligations)).NotTo(gomega.BeEquivalentTo(0), "expected obligations, got %#v", out.Plan)

	afterTDD, err := os.ReadFile(filepath.Join(dir, "tdd.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)

	afterPRD, err := os.ReadFile(filepath.Join(dir, "prd.md"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)

	afterADR, err := os.ReadFile(filepath.Join(dir, "adrs", "ADR-001-test.md"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)

	gomega.Expect(bytes.Equal(beforeTDD, afterTDD)).To(gomega.BeTrue(), "product tdd plan-obligations must not mutate TDD, PRD, or ADR artifacts")
	gomega.Expect(bytes.Equal(beforePRD, afterPRD)).To(gomega.BeTrue(), "product tdd plan-obligations must not mutate TDD, PRD, or ADR artifacts")
	gomega.Expect(bytes.Equal(beforeADR, afterADR)).To(gomega.BeTrue(), "product tdd plan-obligations must not mutate TDD, PRD, or ADR artifacts")
})

var _ = ginkgo.It("product tdd plan obligations blocks when no accepted adr maps requirement", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	acceptedPRD := strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1)

	acceptedPRD = strings.Replace(acceptedPRD, "created_at: 2026-05-17T00:00:00Z", "created_at: 2026-05-17T00:00:00Z\naccepted_at: 2026-05-18T00:00:00Z\naccepted_by: reviewer\nacceptance_profile: human-review", 1)
	writeTestFile(t, dir, "prd.md", acceptedPRD)
	writeTestFile(t, dir, "adrs/index.json", strings.Replace(validTestADRIndexJSON(), "REQ-TEST-001", "REQ-OTHER-001", 1))
	writeTestFile(t, dir, "adrs/ADR-001-test.md", strings.Replace(validTestADRMarkdown(), "REQ-TEST-001", "REQ-OTHER-001", 1))

	var stdout, stderr bytes.Buffer

	code := runProductTDD([]string{
		"--effort-root", dir,
		"--plan-obligations",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out struct {
		Status string                                     `json:"status"`
		Plan   productworkflow.TDDObligationSynthesisPlan `json:"plan"`
	}

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(out.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusBlocked), "expected blocked, got %q", out.Status)

	found := false

	for _, finding := range out.Plan.Findings {
		if finding.Kind == "missing_accepted_adr_for_prd_requirement" && finding.File == "prd.md" {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected missing accepted ADR finding, got %#v", out.Plan.Findings)
})

var _ = ginkgo.It("product tdd plan obligations fails closed on missing inputs", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()

	var stdout, stderr bytes.Buffer

	code := runProductTDD([]string{
		"--effort-root", dir,
		"--plan-obligations",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1, got %d", code)

	var out struct {
		Status string                                     `json:"status"`
		Plan   productworkflow.TDDObligationSynthesisPlan `json:"plan"`
	}

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(out.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusBlocked), "expected blocked, got %q", out.Status)

	gomega.Expect(len(out.Plan.Findings)).NotTo(gomega.BeEquivalentTo(0), "expected fail-closed findings")
})

var _ = ginkgo.It("product tdd plan obligations usage errors", func() {
	var stdout, stderr bytes.Buffer

	code := runProductTDD([]string{"--effort-root", "."}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(2), "expected code 2 without --plan-obligations, got %d", code)
})

var _ = ginkgo.Describe("product tdd JSON usage errors", func() {
	assertUsageError := func(args []string, wantCode string, wantPath string, wantMessage string) {
		ginkgo.GinkgoHelper()

		var stdout, stderr bytes.Buffer

		code := runProductTDD(args, &stdout, &stderr, nil)

		gomega.Expect(code).To(gomega.BeEquivalentTo(2), "expected code 2 for args %v, got %d stdout=%s stderr=%s", args, code, stdout.String(), stderr.String())

		gomega.Expect(strings.TrimSpace(stderr.String())).To(gomega.BeEquivalentTo(""), "expected JSON usage error to keep stderr empty, got %q", stderr.String())

		var result productStatusUsageJSONResult

		{
			err := json.Unmarshal(stdout.Bytes(), &result)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), "usage error output should be valid JSON: %v\n%s", err, stdout.String())
		}

		gomega.Expect(result.Status).To(gomega.Equal(productworkflow.ProductWorkflowStatusBlocked), "expected blocked status with one blocker, got %#v", result)
		gomega.Expect(result.Blockers).To(gomega.HaveLen(1), "expected blocked status with one blocker, got %#v", result)

		blocker := result.Blockers[0]
		gomega.Expect(blocker.Code).To(gomega.Equal(wantCode), "unexpected usage blocker: %#v", blocker)
		gomega.Expect(blocker.Path).To(gomega.Equal(wantPath), "unexpected usage blocker: %#v", blocker)
		gomega.Expect(blocker.Message).To(gomega.Equal(wantMessage), "unexpected usage blocker: %#v", blocker)

		gomega.Expect(strings.TrimSpace(result.NextValidAction)).NotTo(gomega.BeEquivalentTo(""), "expected next valid action, got %#v", result)
	}

	ginkgo.It("rejects a missing effort root", func() {
		assertUsageError([]string{"--json", "--plan-obligations"}, "tdd_missing_effort_root", "--effort-root", "--effort-root is required")
	})

	ginkgo.It("rejects a missing mode flag", func() {
		assertUsageError([]string{"--json", "--effort-root", "."}, "tdd_missing_mode_flag", "mode", "exactly one of --plan-obligations, --evaluate-coverage, or --promote-coverage is required")
	})

	ginkgo.It("rejects multiple mode flags", func() {
		assertUsageError([]string{"--json", "--effort-root", ".", "--plan-obligations", "--evaluate-coverage"}, "tdd_multiple_mode_flags", "mode", "exactly one of --plan-obligations, --evaluate-coverage, or --promote-coverage is required")
	})

	ginkgo.It("reports flag parse errors", func() {
		assertUsageError([]string{"--json", "--unknown-flag"}, "tdd_flag_parse_error", "flags", "flag provided but not defined: -unknown-flag")
	})
})

var _ = ginkgo.It("product tdd promote coverage previews and writes projected ledger", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeBehaviorStandardWorkflowStateFixture(t, dir)
	writeTestFile(t, dir, "tdd.json", `{
		"schema_version":"idiom.tdd_obligations.v2.fixture",
		"effort_id":"test-effort",
		"task_id":"impl-test",
		"coverage_tier":"behavior_standard",
		"coverage":{"mutation_min_score":80},
		"obligations":[{"id":"TDD-KEEP","type":"acceptance","behavior":"keep existing","owner":"test","command":"go test ./keep","required_before":"implementation","status":"pending"}]
	}`)
	writeTestFile(t, dir, "coverage/behavioral.json", `{
		"contract_id":"cov-test",
		"ears":[{"id":"REQ-TEST-001","pattern":"event-driven","statement":"When the test requirement is accepted, the system shall preserve typed behavioral coverage.","mapped_refs":["BDD-001"],"tdd_obligations":["TDD-001"]}],
		"example_rules":[{"id":"RULE-001","requirement_id":"REQ-TEST-001","rule":"Accepted coverage is projected into tdd.json.","examples":[{"id":"EX-001","description":"coverage accepted","concrete_data_refs":["fixtures/accepted.json"]},{"id":"EX-002","description":"coverage missing","concrete_data_refs":["fixtures/missing.json"],"negative":true}]}],
		"bdd_scenarios":[{"id":"BDD-001","requirement_id":"REQ-TEST-001","rule_id":"RULE-001","given":["accepted product artifacts"],"when":["coverage is promoted"],"then":["tdd.json contains projected coverage"],"concrete_data_refs":["fixtures/accepted.json"],"tdd_obligations":["TDD-001"]}],
		"design_contracts":[{"id":"DBC-001","boundary":"product tdd promotion","preconditions":["coverage contract validates"],"postconditions":["ledger projection matches"],"invariants":["productworkflow owns accepted coverage"],"test_refs":["TestProductTDDPromoteCoveragePreviewsAndWritesProjectedLedger"],"tdd_obligations":["TDD-001"]}],
		"test_obligations":[{"id":"TDD-001","type":"acceptance","behavior":"promote accepted behavioral coverage","owner":"productworkflow","command":"go test ./internal/workflows/productworkflow","required_before":"implementation"}],
		"mutation":{"requirement":"required","min_score":80,"score":90}
	}`)

	beforeTDD, err := os.ReadFile(filepath.Join(dir, "tdd.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)

	var previewStdout, previewStderr bytes.Buffer
	code := runProductTDD([]string{
		"--effort-root", dir,
		"--promote-coverage", "coverage/behavioral.json",
		"--json",
	}, &previewStdout, &previewStderr, nil)
	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected preview code 0, got %d stdout=%s stderr=%s", code, previewStdout.String(), previewStderr.String())
	afterPreviewTDD, err := os.ReadFile(filepath.Join(dir, "tdd.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)
	gomega.Expect(bytes.Equal(beforeTDD, afterPreviewTDD)).To(gomega.BeTrue(), "promote-coverage without --write must not mutate tdd.json")

	var preview struct {
		Status  string                    `json:"status"`
		State   string                    `json:"state"`
		EventID string                    `json:"event_id"`
		Digest  string                    `json:"digest"`
		Ledger  productworkflow.TDDLedger `json:"ledger"`
	}
	{
		err := json.Unmarshal(previewStdout.Bytes(), &preview)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "preview output should be valid JSON: %v\n%s", err, previewStdout.String())
	}
	gomega.Expect(preview.Status).To(gomega.Equal("pass"), "expected projected preview ledger, got %#v", preview)
	gomega.Expect(preview.State).To(gomega.Equal(productworkflow.BehavioralCoveragePromotionStateReviewed), "expected projected preview ledger, got %#v", preview)
	gomega.Expect(preview.EventID).To(gomega.BeEmpty(), "preview must not claim a durable event ID, got %#v", preview)
	gomega.Expect(preview.Digest).NotTo(gomega.BeEmpty(), "expected projected preview ledger, got %#v", preview)
	gomega.Expect(preview.Ledger.BehavioralCoverage).NotTo(gomega.BeNil(), "expected projected preview ledger, got %#v", preview)
	gomega.Expect(preview.Ledger.Coverage.BDDTotal).To(gomega.Equal(1), "expected projected preview ledger, got %#v", preview)

	var writeStdout, writeStderr bytes.Buffer
	code = runProductTDD([]string{
		"--effort-root", dir,
		"--promote-coverage", "coverage/behavioral.json",
		"--write",
		"--json",
	}, &writeStdout, &writeStderr, nil)
	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected write code 0, got %d stdout=%s stderr=%s", code, writeStdout.String(), writeStderr.String())

	data, err := os.ReadFile(filepath.Join(dir, "tdd.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)
	var written productworkflow.TDDLedger
	{
		err := json.Unmarshal(data, &written)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "written tdd.json should be valid JSON: %v\n%s", err, string(data))
	}
	gomega.Expect(written.BehavioralCoverage).NotTo(gomega.BeNil(), "expected written behavioral coverage projection, got %#v", written)
	gomega.Expect(written.BehavioralCoverage.EffortID).To(gomega.Equal("test-effort"), "expected written behavioral coverage projection, got %#v", written)
	gomega.Expect(written.Coverage.BDDWithConcreteData).To(gomega.Equal(1), "expected written behavioral coverage projection, got %#v", written)
	gomega.Expect(written.Obligations).To(gomega.HaveLen(2), "expected deterministic obligation merge, got %#v", written.Obligations)
	gomega.Expect(written.Obligations[0].ID).To(gomega.Equal("TDD-KEEP"), "expected deterministic obligation merge, got %#v", written.Obligations)
	gomega.Expect(written.Obligations[1].ID).To(gomega.Equal("TDD-001"), "expected deterministic obligation merge, got %#v", written.Obligations)
	var writeOut struct {
		Status          string `json:"status"`
		State           string `json:"state"`
		CommitStatus    string `json:"commit_status"`
		LedgerCommitted bool   `json:"ledger_committed"`
		EventID         string `json:"event_id"`
		Digest          string `json:"digest"`
	}
	{
		err := json.Unmarshal(writeStdout.Bytes(), &writeOut)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "write output should be valid JSON: %v\n%s", err, writeStdout.String())
	}
	gomega.Expect(writeOut.State).To(gomega.Equal(productworkflow.BehavioralCoveragePromotionStateAccepted), "expected accepted lifecycle output, got %#v", writeOut)
	gomega.Expect(writeOut.EventID).NotTo(gomega.BeEmpty(), "expected accepted lifecycle output, got %#v", writeOut)
	gomega.Expect(writeOut.Digest).NotTo(gomega.BeEmpty(), "expected accepted lifecycle output, got %#v", writeOut)
	gomega.Expect(writeOut.CommitStatus).To(gomega.Equal(productworkflow.BehavioralCoveragePromotionCommitStatusCommitted), "expected committed promotion output, got %#v", writeOut)
	gomega.Expect(writeOut.LedgerCommitted).To(gomega.BeTrue(), "expected committed promotion output, got %#v", writeOut)
	eventsData, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)
	gomega.Expect(string(eventsData)).NotTo(gomega.ContainSubstring(`"event":"behavioral_coverage_prepared"`), "durable event stream should contain only committed authority, got %s", string(eventsData))
	gomega.Expect(string(eventsData)).To(gomega.ContainSubstring(`"event":"behavioral_coverage_accepted"`), "expected durable accepted event, got %s", string(eventsData))
	gomega.Expect(string(eventsData)).To(gomega.ContainSubstring(`"commit_status":"committed"`), "expected durable accepted event, got %s", string(eventsData))
	gomega.Expect(string(eventsData)).To(gomega.ContainSubstring(writeOut.EventID), "expected durable accepted event, got %s", string(eventsData))
})

var _ = ginkgo.It("product tdd promote coverage rejects invalid contract without durable event", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "coverage/invalid.json", `{
		"contract_id":"cov-invalid",
		"bdd_scenarios":[{"id":"BDD-BAD","status":"accepted"}]
	}`)

	var stdout, stderr bytes.Buffer
	code := runProductTDD([]string{
		"--effort-root", dir,
		"--promote-coverage", "coverage/invalid.json",
		"--write",
		"--json",
	}, &stdout, &stderr, nil)
	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected rejected invalid coverage to return code 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out struct {
		Status   string                                     `json:"status"`
		State    string                                     `json:"state"`
		Findings []productworkflow.TDDObligationPlanFinding `json:"findings"`
	}
	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "rejection output should be valid JSON: %v\n%s", err, stdout.String())
	}
	gomega.Expect(out.Status).To(gomega.Equal("blocked"), "expected rejected lifecycle state, got %#v", out)
	gomega.Expect(out.State).To(gomega.Equal(productworkflow.BehavioralCoveragePromotionStateRejected), "expected rejected lifecycle state, got %#v", out)
	found := false
	for _, finding := range out.Findings {
		if finding.Kind == "coverage_contract_invalid" && finding.File == "coverage/invalid.json" && strings.Contains(finding.Message, "bdd_scenarios[0] accepted coverage missing") {
			found = true
			break
		}
	}
	gomega.Expect(found).To(gomega.BeTrue(), "expected structured invalid coverage finding, got %#v", out.Findings)
	{
		data, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
		if err != nil {
			gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "read durable events after rejected promotion: %v", err)
		} else {
			gomega.Expect(string(data)).NotTo(gomega.ContainSubstring("behavioral_coverage_accepted"), "rejected promotion must not append accepted event, got %s", string(data))
		}
	}
})

var _ = ginkgo.It("product tdd promote coverage fails closed before ledger commit when event authority is unavailable", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeBehaviorStandardWorkflowStateFixture(t, dir)
	writeTestFile(t, dir, "tdd.json", `{
		"schema_version":"idiom.tdd_obligations.v2.fixture",
		"effort_id":"test-effort",
		"task_id":"impl-test",
		"coverage_tier":"behavior_standard",
		"coverage":{"mutation_min_score":80},
		"obligations":[{"id":"TDD-KEEP","type":"acceptance","behavior":"keep existing","owner":"test","command":"go test ./keep","required_before":"implementation","status":"pending"}]
	}`)
	writeTestFile(t, dir, "coverage/behavioral.json", `{
		"contract_id":"cov-event-fail",
		"ears":[{"id":"REQ-TEST-001","pattern":"event-driven","statement":"When the event sink fails, the ledger shall remain unchanged.","mapped_refs":["BDD-001"],"tdd_obligations":["TDD-001"]}],
		"bdd_scenarios":[{"id":"BDD-001","requirement_id":"REQ-TEST-001","given":["accepted product artifacts"],"when":["coverage is promoted"],"then":["tdd.json contains projected coverage"],"concrete_data_refs":["fixtures/accepted.json"],"tdd_obligations":["TDD-001"]}],
		"test_obligations":[{"id":"TDD-001","type":"acceptance","behavior":"promote accepted behavioral coverage","owner":"productworkflow","command":"go test ./internal/workflows/productworkflow","required_before":"implementation"}],
		"mutation":{"requirement":"required","min_score":80,"score":90}
	}`)
	beforeTDD, err := os.ReadFile(filepath.Join(dir, "tdd.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)
	{
		err := os.Remove(filepath.Join(dir, "events.jsonl"))
		if err != nil {
			gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), err)
		}
	}
	{
		err := os.Mkdir(filepath.Join(dir, "events.jsonl"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)
	}

	var stdout, stderr bytes.Buffer
	code := runProductTDD([]string{
		"--effort-root", dir,
		"--promote-coverage", "coverage/behavioral.json",
		"--write",
		"--json",
	}, &stdout, &stderr, nil)
	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected unavailable event authority to reject promotion, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	afterTDD, err := os.ReadFile(filepath.Join(dir, "tdd.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)
	gomega.Expect(bytes.Equal(beforeTDD, afterTDD)).To(gomega.BeTrue(), "event authority preflight failure must not mutate the ledger\nbefore=%s\nafter=%s", string(beforeTDD), string(afterTDD))

	var out struct {
		Status          string                                     `json:"status"`
		State           string                                     `json:"state"`
		CommitStatus    string                                     `json:"commit_status"`
		LedgerCommitted bool                                       `json:"ledger_committed"`
		AuditStatus     string                                     `json:"audit_status"`
		AuditError      string                                     `json:"audit_error"`
		Ledger          productworkflow.TDDLedger                  `json:"ledger"`
		Findings        []productworkflow.TDDObligationPlanFinding `json:"findings"`
	}
	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "event failure output should be valid JSON: %v\n%s", err, stdout.String())
	}
	gomega.Expect(out.Status).To(gomega.Equal("blocked"), "expected unavailable event authority to block promotion, got %#v", out)
	gomega.Expect(out.State).To(gomega.Equal(productworkflow.BehavioralCoveragePromotionStateRejected), "expected unavailable event authority to reject promotion, got %#v", out)
	gomega.Expect(out.CommitStatus).To(gomega.Equal(productworkflow.BehavioralCoveragePromotionCommitStatusBlocked), "expected blocked promotion output, got %#v", out)
	gomega.Expect(out.LedgerCommitted).To(gomega.BeFalse(), "event authority preflight failure must not claim a ledger commit, got %#v", out)
	gomega.Expect(out.AuditStatus).To(gomega.Equal(productworkflow.BehavioralCoveragePromotionAuditStatusNotAttempted), "event authority preflight failure must not claim an audit append, got %#v", out)
	gomega.Expect(out.AuditError).To(gomega.BeEmpty(), "preflight failure belongs in typed findings, got %#v", out)
	gomega.Expect(out.Ledger.BehavioralCoverage).To(gomega.BeNil(), "rejected promotion output must expose the unchanged ledger, got %#v", out.Ledger)
	gomega.Expect(out.Ledger.Obligations).To(gomega.HaveLen(1), "rejected promotion output must retain only existing obligations, got %#v", out.Ledger)
	gomega.Expect(out.Ledger.Obligations[0].ID).To(gomega.Equal("TDD-KEEP"), "rejected promotion output must retain the existing obligation, got %#v", out.Ledger)
	gomega.Expect(out.Findings).To(gomega.HaveLen(1), "event authority preflight must return one typed finding, got %#v", out.Findings)
	gomega.Expect(out.Findings[0].Message).To(gomega.ContainSubstring("events.jsonl"), "expected event authority finding, got %#v", out.Findings)
	gomega.Expect(out.Findings[0].Message).To(gomega.ContainSubstring("regular file"), "expected regular-file authority finding, got %#v", out.Findings)
})

var _ = ginkgo.It("product tdd coverage gate passes deterministic fixture", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	acceptedPRD := strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1)

	acceptedPRD = strings.Replace(acceptedPRD, "created_at: 2026-05-17T00:00:00Z", "created_at: 2026-05-17T00:00:00Z\naccepted_at: 2026-05-18T00:00:00Z\naccepted_by: reviewer\nacceptance_profile: human-review", 1)
	writeTestFile(t, dir, "prd.md", acceptedPRD)
	writeTestFile(t, dir, "tdd.json", `{
		"schema_version":"idiom.tdd_obligations.v2.fixture",
		"effort_id":"test-effort",
		"task_id":"impl-test",
		"coverage_tier":"behavior_standard",
		"coverage":{
			"ears_total":1,
			"ears_mapped":1,
			"bdd_total":1,
			"bdd_with_concrete_data":1,
			"mutation_min_score":80,
			"mutation_score":90,
			"gates_passed":["contract"]
		},
		"behavioral_coverage":{
			"contract_id":"cov-test",
			"effort_id":"test-effort",
			"task_id":"impl-test",
			"coverage_tier":"behavior_standard",
			"ears":[{"id":"REQ-TEST-001","statement":"When the test requirement is accepted, the system shall preserve typed behavioral coverage.","mapped_refs":["BDD-001"]}],
			"bdd_scenarios":[{"id":"BDD-001","requirement_id":"REQ-TEST-001","given":["accepted PRD and ADR exist"],"when":["coverage gate evaluates"],"then":["typed behavioral coverage is present"],"concrete_data_refs":["fixtures/test.json"],"tdd_obligations":["TDD-001"]}],
			"design_contracts":[{"id":"DBC-001","boundary":"product tdd gate","preconditions":["coverage tier is behavior_standard"],"postconditions":["behavioral coverage contract exists"],"invariants":["coverage counters are projections"],"test_refs":["TestProductTDDCoverageGatePassesDeterministicFixture"],"tdd_obligations":["TDD-001"]}],
			"test_obligations":[{"id":"TDD-001","type":"acceptance","behavior":"test","owner":"test","command":"go test","required_before":"close"}],
			"mutation":{"requirement":"required","min_score":80,"score":90}
		},
		"obligations":[{
			"id":"TDD-001","type":"acceptance","behavior":"test","owner":"test","command":"go test","required_before":"close","status":"pending"
		}]
	}`)

	beforeTDD, err := os.ReadFile(filepath.Join(dir, "tdd.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)

	beforePRD, err := os.ReadFile(filepath.Join(dir, "prd.md"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)

	beforeADR, err := os.ReadFile(filepath.Join(dir, "adrs", "ADR-001-test.md"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)

	beforeTraceability, err := os.ReadFile(filepath.Join(dir, "evidence", "traceability_matrix.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)

	var stdout, stderr bytes.Buffer

	code := runProductTDD([]string{
		"--effort-root", dir,
		"--evaluate-coverage",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected code 0, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out struct {
		Status     string                                 `json:"status"`
		Evaluation productworkflow.CoverageGateEvaluation `json:"evaluation"`
	}

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(out.Status).To(gomega.Equal("pass"), "expected pass output, got %#v", out)
	gomega.Expect(out.Evaluation.Status).To(gomega.Equal(productworkflow.ProductWorkflowStatusPass), "expected pass output, got %#v", out)

	afterTDD, err := os.ReadFile(filepath.Join(dir, "tdd.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)

	afterPRD, err := os.ReadFile(filepath.Join(dir, "prd.md"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)

	afterADR, err := os.ReadFile(filepath.Join(dir, "adrs", "ADR-001-test.md"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)

	afterTraceability, err := os.ReadFile(filepath.Join(dir, "evidence", "traceability_matrix.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)

	gomega.Expect(bytes.Equal(beforeTDD, afterTDD)).To(gomega.BeTrue(), "product tdd evaluate-coverage must not mutate TDD, PRD, ADR, or traceability artifacts")
	gomega.Expect(bytes.Equal(beforePRD, afterPRD)).To(gomega.BeTrue(), "product tdd evaluate-coverage must not mutate TDD, PRD, ADR, or traceability artifacts")
	gomega.Expect(bytes.Equal(beforeADR, afterADR)).To(gomega.BeTrue(), "product tdd evaluate-coverage must not mutate TDD, PRD, ADR, or traceability artifacts")
	gomega.Expect(bytes.Equal(beforeTraceability, afterTraceability)).To(gomega.BeTrue(), "product tdd evaluate-coverage must not mutate TDD, PRD, ADR, or traceability artifacts")
})

var _ = ginkgo.It("product tdd coverage gate emits coverage gate evaluated telemetry", func() {
	t := ginkgo.GinkgoT()
	t.Setenv("IDIOM_TELEMETRY", "1")

	dir := writeValidTestEffort(t)
	acceptedPRD := strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1)

	acceptedPRD = strings.Replace(acceptedPRD, "created_at: 2026-05-17T00:00:00Z", "created_at: 2026-05-17T00:00:00Z\naccepted_at: 2026-05-18T00:00:00Z\naccepted_by: reviewer\nacceptance_profile: human-review", 1)
	writeTestFile(t, dir, "prd.md", acceptedPRD)
	writeTestFile(t, dir, "tdd.json", `{
		"schema_version":"idiom.tdd_obligations.v2.fixture",
		"effort_id":"test-effort",
		"task_id":"impl-test",
		"coverage_tier":"behavior_standard",
		"coverage":{
			"ears_total":1,
			"ears_mapped":1,
			"bdd_total":1,
			"bdd_with_concrete_data":1,
			"mutation_min_score":80,
			"mutation_score":90,
			"gates_passed":["contract"]
		},
		"behavioral_coverage":{
			"contract_id":"cov-test",
			"effort_id":"test-effort",
			"task_id":"impl-test",
			"coverage_tier":"behavior_standard",
			"ears":[{"id":"REQ-TEST-001","statement":"When the test requirement is accepted, the system shall preserve typed behavioral coverage.","mapped_refs":["BDD-001"]}],
			"bdd_scenarios":[{"id":"BDD-001","requirement_id":"REQ-TEST-001","given":["accepted PRD and ADR exist"],"when":["coverage gate evaluates"],"then":["typed behavioral coverage is present"],"concrete_data_refs":["fixtures/test.json"],"tdd_obligations":["TDD-001"]}],
			"design_contracts":[{"id":"DBC-001","boundary":"product tdd gate","preconditions":["coverage tier is behavior_standard"],"postconditions":["behavioral coverage contract exists"],"invariants":["coverage counters are projections"],"test_refs":["TestProductTDDCoverageGateEmitsCoverageGateEvaluatedTelemetry"],"tdd_obligations":["TDD-001"]}],
			"test_obligations":[{"id":"TDD-001","type":"acceptance","behavior":"test","owner":"test","command":"go test","required_before":"close"}],
			"mutation":{"requirement":"required","min_score":80,"score":90}
		},
		"obligations":[{
			"id":"TDD-001","type":"acceptance","behavior":"test","owner":"test","command":"go test","required_before":"close","status":"pending"
		}]
	}`)

	rec, err := telemetry.Open(telemetry.Options{
		Dir:           t.TempDir(),
		Command:       "product_tdd",
		Args:          []string{"product", "tdd", "--evaluate-coverage"},
		WorkspaceRoot: dir,
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "telemetry.Open: %v", err)

	var stdout, stderr bytes.Buffer

	code := runProductTDD([]string{
		"--effort-root", dir,
		"--evaluate-coverage",
		"--json",
	}, &stdout, &stderr, rec)

	{
		err := rec.CloseWithFields(code, nil)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "rec.Close: %v", err)
	}

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected code 0, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	records := readMainTelemetryRecords(t, rec.Path())
	{
		legacy := findTelemetryRecord(records, "product_tdd_coverage_gate_evaluated")
		gomega.Expect(legacy).To(gomega.BeNil(), "coverage gate must not emit legacy raw telemetry event: %#v", legacy)
	}

	record := findTelemetryRecord(records, string(productworkflow.ProductWorkflowEventCoverageGateEvaluated))

	gomega.Expect(record).NotTo(gomega.BeNil(), "missing coverage_gate_evaluated telemetry: %#v", records)

	gomega.Expect(record["event"]).To(gomega.Equal(string(productworkflow.ProductWorkflowEventCoverageGateEvaluated)), "unexpected coverage gate event identity: %#v", record)
	gomega.Expect(record["task_id"]).To(gomega.Equal("impl-test"), "unexpected coverage gate event identity: %#v", record)
	gomega.Expect(record["gate_result"]).To(gomega.Equal("pass"), "unexpected coverage gate event identity: %#v", record)

	coverage, ok := record["coverage"].(map[string]any)
	gomega.Expect(ok).To(gomega.BeTrue(), "coverage should be an object, got %#v", record["coverage"])

	gomega.Expect(coverage["ears_coverage_pct"]).To(gomega.Equal(float64(100)), "unexpected coverage summary: %#v", coverage)
	gomega.Expect(coverage["bdd_coverage_pct"]).To(gomega.Equal(float64(100)), "unexpected coverage summary: %#v", coverage)
	gomega.Expect(coverage["mutation_score"]).To(gomega.Equal(float64(90)), "unexpected coverage summary: %#v", coverage)
})

var _ = ginkgo.It("product tdd coverage gate passes current valid effort fixture", func() {
	t := ginkgo.GinkgoT()
	effortRoot := writeValidTestEffort(t)
	acceptedPRD := strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1)
	acceptedPRD = strings.Replace(acceptedPRD, "created_at: 2026-05-17T00:00:00Z", "created_at: 2026-05-17T00:00:00Z\naccepted_at: 2026-05-18T00:00:00Z\naccepted_by: reviewer\nacceptance_profile: human-review", 1)
	writeTestFile(t, effortRoot, "prd.md", acceptedPRD)

	var stdout, stderr bytes.Buffer

	code := runProductTDD([]string{
		"--effort-root", effortRoot,
		"--evaluate-coverage",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected valid coverage gate to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out struct {
		Status     string                                 `json:"status"`
		Evaluation productworkflow.CoverageGateEvaluation `json:"evaluation"`
	}

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(out.Status).To(gomega.Equal("pass"), "expected pass output, got %#v", out)
	gomega.Expect(out.Evaluation.Status).To(gomega.Equal(productworkflow.ProductWorkflowStatusPass), "expected pass output, got %#v", out)
})

var _ = ginkgo.It("product TDD coverage gate blocks an unknown PRD TDD obligation", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	acceptedPRD := strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1)

	acceptedPRD = strings.Replace(acceptedPRD, "created_at: 2026-05-17T00:00:00Z", "created_at: 2026-05-17T00:00:00Z\naccepted_at: 2026-05-18T00:00:00Z\naccepted_by: reviewer\nacceptance_profile: human-review", 1)
	writeTestFile(t, dir, "prd.md", acceptedPRD)
	writeTestFile(t, dir, "tdd.json", strings.Replace(validTestTDDJSON(), `"id":"TDD-001"`, `"id":"TDD-OTHER"`, 1))
	writeTestFile(t, dir, "implementation_ledger.jsonl", implementationLedgerWithoutTDDEvidenceJSONL())

	var stdout, stderr bytes.Buffer

	code := runProductTDD([]string{
		"--effort-root", dir,
		"--evaluate-coverage",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out struct {
		Status     string                                 `json:"status"`
		Evaluation productworkflow.CoverageGateEvaluation `json:"evaluation"`
	}

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(out.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusBlocked), "expected blocked status, got %q", out.Status)

	found := false

	for _, blocker := range out.Evaluation.Blockers {
		if blocker.Criterion == "evidence_acceptance" {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected evidence_acceptance blocker, got %#v", out.Evaluation.Blockers)
})

var _ = ginkgo.It("product tdd coverage gate ignores other task historical tdd obligations", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	acceptedPRD := strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1)

	acceptedPRD = strings.Replace(acceptedPRD, "created_at: 2026-05-17T00:00:00Z", "created_at: 2026-05-17T00:00:00Z\naccepted_at: 2026-05-18T00:00:00Z\naccepted_by: reviewer\nacceptance_profile: human-review", 1)
	acceptedPRD = strings.ReplaceAll(acceptedPRD, "TDD-001", "TDD-OTHER-TASK")
	writeTestFile(t, dir, "prd.md", acceptedPRD)
	writeTestFile(t, dir, "adrs/ADR-001-test.md", strings.ReplaceAll(validTestADRMarkdown(), "TDD-001", "TDD-OTHER-TASK"))
	writeTestFile(t, dir, "evidence/traceability_matrix.json", strings.ReplaceAll(validTestTraceabilityMatrixJSON(), "TDD-001", "TDD-OTHER-TASK"))
	writeTestFile(t, dir, "tdd.json", strings.Replace(validTestTDDJSON(), `"id":"TDD-001"`, `"id":"TDD-DIFFERENT"`, 1))
	writeTestFile(t, dir, "implementation_ledger.jsonl", validTestImplementationLedgerJSONL()+"\n"+`{"record_type":"verification_completed","task_id":"other-task","effort_id":"test-effort","timestamp":"2026-05-18T00:06:00Z","tdd_obligations_satisfied":["TDD-OTHER-TASK"]}`)

	var stdout, stderr bytes.Buffer

	code := runProductTDD([]string{
		"--effort-root", dir,
		"--evaluate-coverage",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out struct {
		Status     string                                 `json:"status"`
		Evaluation productworkflow.CoverageGateEvaluation `json:"evaluation"`
	}

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(out.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusBlocked), "expected blocked status, got %q", out.Status)
})

var _ = ginkgo.It("product tdd coverage gate preflights adr supersession links", func() {
	t := ginkgo.GinkgoT()
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

	var stdout, stderr bytes.Buffer

	code := runProductTDD([]string{
		"--effort-root", dir,
		"--evaluate-coverage",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out struct {
		Status     string                                 `json:"status"`
		Evaluation productworkflow.CoverageGateEvaluation `json:"evaluation"`
	}

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(out.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusBlocked), "expected blocked status, got %q", out.Status)

	found := false

	for _, blocker := range out.Evaluation.Blockers {
		if blocker.Criterion == "authority_preflight" && blocker.Kind == "adr_supersession_non_reciprocal" {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected authority_preflight supersession blocker, got %#v; stdout=%s", out.Evaluation.Blockers, stdout.String())
})

var _ = ginkgo.It("product tdd coverage gate preflights authority when inputs fail", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "prd.md", strings.Replace(validTestPRDMarkdown(), "status: draft", "status: accepted", 1))

	var stdout, stderr bytes.Buffer

	code := runProductTDD([]string{
		"--effort-root", dir,
		"--evaluate-coverage",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out struct {
		Status     string                                 `json:"status"`
		Evaluation productworkflow.CoverageGateEvaluation `json:"evaluation"`
	}

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	found := false

	for _, blocker := range out.Evaluation.Blockers {
		if blocker.Criterion == "authority_preflight" && blocker.Kind == "parse_error" && blocker.File == "prd.md" {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected PRD parse error to be carried as authority_preflight blocker, got %#v", out.Evaluation.Blockers)
})

var _ = ginkgo.It("product tdd coverage gate fails closed on missing traceability", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	{
		err := os.Remove(filepath.Join(dir, "evidence", "traceability_matrix.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "remove traceability: %v", err)
	}

	var stdout, stderr bytes.Buffer

	code := runProductTDD([]string{
		"--effort-root", dir,
		"--evaluate-coverage",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out struct {
		Status     string                                 `json:"status"`
		Evaluation productworkflow.CoverageGateEvaluation `json:"evaluation"`
	}

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(out.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusBlocked), "expected blocked status, got %q", out.Status)
})

var _ = ginkgo.It("product tdd coverage gate usage errors", func() {
	var stdout, stderr bytes.Buffer

	code := runProductTDD([]string{"--effort-root", ".", "--plan-obligations", "--evaluate-coverage"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(2), "expected code 2 for mutually exclusive flags, got %d", code)
})
