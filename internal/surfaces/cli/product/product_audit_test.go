package product

import (
	"bytes"
	"encoding/json"
	"strings"

	"idiom/internal/workflows/productworkflow"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.It("product audit passes on current valid effort fixture", func() {
	t := ginkgo.GinkgoT()
	effortRoot := writeValidTestEffort(t)

	var stdout, stderr bytes.Buffer

	code := runProductAudit([]string{"--effort-root", effortRoot, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected audit to pass on current valid effort fixture, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result productworkflow.ProductAuditResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusPass), "expected status pass, got %q", result.Status)

	gomega.Expect(result.EffortRoot).NotTo(gomega.BeEquivalentTo(""), "effort_root should be set")

	gomega.Expect(len(result.CheckedFiles)).NotTo(gomega.BeEquivalentTo(0), "should have checked at least one file")

	requiredSections := []string{"prd", "adr", "tdd", "traceability", "reports", "obsidian", "scope_gate", "metrics"}
	gomega.Expect(len(result.Sections)).To(gomega.BeEquivalentTo(len(requiredSections)), "expected %d sections, got %d", len(requiredSections), len(result.Sections))

	for i, name := range requiredSections {
		gomega.Expect(result.Sections[i].Name).To(gomega.BeEquivalentTo(name), "expected section %d to be %q, got %q", i, name, result.Sections[i].Name)

		gomega.Expect(result.Sections[i].Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusPass), "expected section %q to pass, got %q", name, result.Sections[i].Status)
	}
})

var _ = ginkgo.It("product audit json includes all required sections", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)

	var stdout, stderr bytes.Buffer

	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected audit to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result productworkflow.ProductAuditResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	sectionNames := map[string]bool{}
	for _, sec := range result.Sections {
		sectionNames[sec.Name] = true
	}

	for _, name := range []string{"prd", "adr", "tdd", "traceability", "reports", "obsidian", "scope_gate"} {
		gomega.Expect(sectionNames[name]).To(gomega.BeTrue(), "expected section %q in audit result", name)
	}
})

var _ = ginkgo.It("product audit metrics", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "reports/test-implementation.json", validTestImplementationReportJSON())
	writeTestFile(t, dir, "reports/test-review.json", validTestReviewReportJSON())
	writeTestFile(t, dir, "reports/test-investigation.json", validTestTestInvestigationReportJSON())

	var stdout, stderr bytes.Buffer

	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected audit to pass, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result productworkflow.ProductAuditResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result.Metrics).NotTo(gomega.BeNil(), "expected metrics in product audit JSON")

	gomega.Expect(result.Metrics.CurrentPhase).NotTo(gomega.BeEmpty(), "expected phase/state metrics, got %#v", result.Metrics)
	gomega.Expect(result.Metrics.CurrentState).NotTo(gomega.BeEmpty(), "expected phase/state metrics, got %#v", result.Metrics)

	gomega.Expect(result.Metrics.NextValidActionCount).NotTo(gomega.BeNumerically("<=", 0), "expected next_valid_action_count > 0, got %d", result.Metrics.NextValidActionCount)

	gomega.Expect(result.Metrics.TDDObligationTotal).To(gomega.BeEquivalentTo(1), "expected tdd_obligation_total=1, got %d", result.Metrics.TDDObligationTotal)
})

var _ = ginkgo.It("product audit metrics error uses relative artifact path", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "tdd.json", `{not valid json`)

	var stdout, stderr bytes.Buffer

	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected audit failure, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result productworkflow.ProductAuditResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	found := false

	for _, finding := range result.Findings {
		if finding.Code != "metrics_error" {
			continue
		}

		found = true

		gomega.Expect(finding.Path).To(gomega.BeEquivalentTo("tdd.json"), "expected metrics_error path tdd.json, got %#v", finding)

		gomega.Expect(strings.Contains(finding.Message, dir)).To(gomega.BeFalse(), "metrics_error should not leak absolute effort path, got %q", finding.Message)
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected metrics_error finding, got %#v", result.Findings)
})

var _ = ginkgo.It("product audit metrics fails closed on unknown gate status", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "reports/test-implementation.json", validTestImplementationReportJSON())
	writeTestFile(t, dir, "reports/test-review.json", validTestReviewReportJSON())
	writeTestFile(t, dir, "reports/test-investigation.json", validTestTestInvestigationReportJSON())
	writeTestFile(t, dir, "workflow_state.json", strings.Replace(
		validTestWorkflowStateJSON(),
		`"coverage":{"coverage_tier":"unit"}`,
		`"coverage":{"coverage_tier":"unit","gate_results":[{"gate":"metrics","status":"surprise"}]}`,
		1,
	))

	var stdout, stderr bytes.Buffer

	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected audit to fail closed, got code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result productworkflow.ProductAuditResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusFail), "expected status fail, got %q", result.Status)

	metricsSection := findAuditSection(t, result, "metrics")
	gomega.Expect(metricsSection.Status).To(gomega.Equal(productworkflow.ProductWorkflowStatusFail), "expected metrics section failure, got %#v", metricsSection)
	gomega.Expect(metricsSection.Findings).To(gomega.HaveLen(1), "expected metrics section failure, got %#v", metricsSection)

	gomega.Expect(result.Metrics).To(gomega.BeNil(), "expected metrics omitted on failure, got %#v", result.Metrics)

	gomega.Expect(strings.Contains(metricsSection.Findings[0].Message, "unsupported")).To(gomega.BeTrue(), "expected unsupported-value metrics finding, got %#v", metricsSection.Findings[0])
})

var _ = ginkgo.It("product audit surfaces parse error in prd section", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "prd.md", `not valid prd`)

	var stdout, stderr bytes.Buffer

	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected audit to fail on invalid PRD, got code=%d", code)

	var result productworkflow.ProductAuditResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	prdSection := findAuditSection(t, result, "prd")
	gomega.Expect(prdSection.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusFail), "expected prd section to fail, got %q", prdSection.Status)

	found := false

	for _, f := range prdSection.Findings {
		if f.Code == "parse_error" && f.Path == "prd.md" {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected parse_error finding in prd section, got: %#v", prdSection.Findings)
})

var _ = ginkgo.It("product audit surfaces parse error in adr section", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "adrs/index.json", `not valid json`)

	var stdout, stderr bytes.Buffer

	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected audit to fail on invalid ADR index, got code=%d", code)

	var result productworkflow.ProductAuditResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	adrSection := findAuditSection(t, result, "adr")
	gomega.Expect(adrSection.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusFail), "expected adr section to fail, got %q", adrSection.Status)

	found := false

	for _, f := range adrSection.Findings {
		if f.Code == "parse_error" && f.Path == "adrs/index.json" {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected parse_error finding in adr section, got: %#v", adrSection.Findings)
})

var _ = ginkgo.It("product audit surfaces parse error in tdd section", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "tdd.json", `{not valid json`)

	var stdout, stderr bytes.Buffer

	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected audit to fail on invalid TDD, got code=%d", code)

	var result productworkflow.ProductAuditResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	tddSection := findAuditSection(t, result, "tdd")
	gomega.Expect(tddSection.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusFail), "expected tdd section to fail, got %q", tddSection.Status)

	found := false

	for _, f := range tddSection.Findings {
		if f.Code == "parse_error" && f.Path == "tdd.json" {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected parse_error finding in tdd section, got: %#v", tddSection.Findings)
})

var _ = ginkgo.It("product audit does not change lint behavior", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)

	var lintStdout, lintStderr bytes.Buffer

	lintCode := runProductLint([]string{"--effort-root", dir, "--json"}, &lintStdout, &lintStderr, nil)

	var auditStdout, auditStderr bytes.Buffer

	auditCode := runProductAudit([]string{"--effort-root", dir, "--json"}, &auditStdout, &auditStderr, nil)

	gomega.Expect(lintCode).To(gomega.BeEquivalentTo(0), "expected lint to pass, got code=%d stdout=%s", lintCode, lintStdout.String())

	gomega.Expect(auditCode).To(gomega.BeEquivalentTo(0), "expected audit to pass, got code=%d stdout=%s", auditCode, auditStdout.String())

	var lintResult productworkflow.LintSummaryResult

	{
		err := json.Unmarshal(lintStdout.Bytes(), &lintResult)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "lint output should be valid JSON: %v", err)
	}

	var auditResult productworkflow.ProductAuditResult

	{
		err := json.Unmarshal(auditStdout.Bytes(), &auditResult)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "audit output should be valid JSON: %v", err)
	}

	gomega.Expect(lintResult.Status).To(gomega.BeEquivalentTo(auditResult.Status), "lint status %q != audit status %q", lintResult.Status, auditResult.Status)

	gomega.Expect(len(lintResult.Findings)).To(gomega.BeEquivalentTo(len(auditResult.Findings)), "lint findings count %d != audit findings count %d", len(lintResult.Findings), len(auditResult.Findings))

	lintCode2 := runProductLint([]string{"--effort-root", dir, "--json"}, &lintStdout, &lintStderr, nil)
	gomega.Expect(lintCode).To(gomega.BeEquivalentTo(lintCode2), "lint is not deterministic: first=%d second=%d", lintCode, lintCode2)
})

var _ = ginkgo.It("product audit fail next valid action parity", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "tdd.json", `{not valid json`)

	var jsonStdout, jsonStderr bytes.Buffer

	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &jsonStdout, &jsonStderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected fail, got code=%d stdout=%s stderr=%s", code, jsonStdout.String(), jsonStderr.String())

	var jsonResult productworkflow.ProductAuditResult

	{
		err := json.Unmarshal(jsonStdout.Bytes(), &jsonResult)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "invalid json: %v", err)
	}

	gomega.Expect(strings.TrimSpace(jsonResult.NextValidAction)).NotTo(gomega.BeEquivalentTo(""), "expected next_valid_action in audit json output")

	var textStdout, textStderr bytes.Buffer

	code = runProductAudit([]string{"--effort-root", dir}, &textStdout, &textStderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected fail text run, got code=%d stdout=%s stderr=%s", code, textStdout.String(), textStderr.String())

	gomega.Expect(strings.Contains(textStdout.String(), "next: "+jsonResult.NextValidAction)).To(gomega.BeTrue(), "expected text next-action parity, got:\n%s", textStdout.String())
})

var _ = ginkgo.It("product audit usage mirrors lint", func() {
	var stdout, stderr bytes.Buffer

	code := runProductAudit([]string{}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(2), "expected code 2 for missing --effort-root, got %d", code)
})

var _ = ginkgo.It("product audit help", func() {
	var stdout, stderr bytes.Buffer

	code := runProductAudit([]string{"-h"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected code 0 for -h, got %d", code)
})

var _ = ginkgo.It("product audit fails with prd broken link when evidence ref missing", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "prd.md", `---
schema_version: idiom.prd.v1
prd_id: prd-test
status: draft
task_id: impl-test
created_at: 2026-05-17T00:00:00Z
---

# PRD: Test

## Requirements

### REQ-TEST-001

- **id**: REQ-TEST-001
- **priority**: must
- **statement**: Test requirement
- **owner_alignment**: team
- **testability**: automated
- **metric**: coverage > 80%
- **evidence_refs**: ["EV-001", "EV-MISSING"]
- **tdd_obligations**: ["TDD-001"]
- **open_questions**: []
`)

	var stdout, stderr bytes.Buffer

	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected audit to fail with broken PRD evidence ref, got code=%d", code)

	var result productworkflow.ProductAuditResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	prdSection := findAuditSection(t, result, "prd")
	gomega.Expect(prdSection.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusFail), "expected prd section to fail, got %q", prdSection.Status)

	found := false

	for _, f := range prdSection.Findings {
		if f.Code == "broken_link" && f.Path == "prd.md" && strings.Contains(f.Message, "EV-MISSING") {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected broken_link finding in prd section mentioning EV-MISSING, got: %#v", prdSection.Findings)
})

var _ = ginkgo.It("product audit fails on adr missing evidence ref", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "adrs/ADR-001-test.md", `---
schema_version: idiom.adr.v1
adr_id: ADR-001
status: accepted
accepted_at: 2026-05-17T00:00:00Z
accepted_by: test-reviewer
acceptance_profile: human-review
prd_refs:
  - REQ-TEST-001
evidence_refs:
  - EV-001
  - EV-MISSING
tdd_obligations:
  - TDD-001
coverage_tier: unit
---

# ADR-001: Test

## Context

Test.

## Decision

Test.

## Alternatives Considered

None.

## Evidence

- EV-001: Test.

## Consequences

None.

## Compatibility and Migration

Compatible.

## TDD Obligations

- TDD-001: Test.

## Rollback

Revert.

## Open Questions

None.
`)

	var stdout, stderr bytes.Buffer

	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected audit to fail with broken ADR evidence ref, got code=%d", code)

	var result productworkflow.ProductAuditResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	adrSection := findAuditSection(t, result, "adr")
	gomega.Expect(adrSection.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusFail), "expected adr section to fail, got %q", adrSection.Status)

	found := false

	for _, f := range adrSection.Findings {
		if f.Code == "broken_link" && strings.Contains(f.Message, "EV-MISSING") {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected broken_link finding in adr section mentioning EV-MISSING, got: %#v", adrSection.Findings)
})

var _ = ginkgo.It("product audit fails on adr missing tdd obligation ref", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "adrs/ADR-001-test.md", `---
schema_version: idiom.adr.v1
adr_id: ADR-001
status: accepted
accepted_at: 2026-05-17T00:00:00Z
accepted_by: test-reviewer
acceptance_profile: human-review
prd_refs:
  - REQ-TEST-001
evidence_refs:
  - EV-001
tdd_obligations:
  - TDD-001
  - TDD-MISSING
coverage_tier: unit
---

# ADR-001: Test

## Context

Test.

## Decision

Test.

## Alternatives Considered

None.

## Evidence

- EV-001: Test.

## Consequences

None.

## Compatibility and Migration

Compatible.

## TDD Obligations

- TDD-001: Test.

## Rollback

Revert.

## Open Questions

None.
`)

	var stdout, stderr bytes.Buffer

	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected audit to fail with broken ADR TDD obligation ref, got code=%d", code)

	var result productworkflow.ProductAuditResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	adrSection := findAuditSection(t, result, "adr")
	gomega.Expect(adrSection.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusFail), "expected adr section to fail, got %q", adrSection.Status)

	found := false

	for _, f := range adrSection.Findings {
		if f.Code == "broken_link" && strings.Contains(f.Message, "TDD-MISSING") {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected broken_link finding in adr section mentioning TDD-MISSING, got: %#v", adrSection.Findings)
})

var _ = ginkgo.It("product audit fails on adr missing prd ref", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "adrs/ADR-001-test.md", `---
schema_version: idiom.adr.v1
adr_id: ADR-001
status: accepted
accepted_at: 2026-05-17T00:00:00Z
accepted_by: test-reviewer
acceptance_profile: human-review
prd_refs:
  - REQ-MISSING
evidence_refs:
  - EV-001
tdd_obligations:
  - TDD-001
coverage_tier: unit
---

# ADR-001: Test

## Context

Test.

## Decision

Test.

## Alternatives Considered

None.

## Evidence

- EV-001: Test.

## Consequences

None.

## Compatibility and Migration

Compatible.

## TDD Obligations

- TDD-001: Test.

## Rollback

Revert.

## Open Questions

None.
`)

	var stdout, stderr bytes.Buffer

	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected audit to fail with broken ADR PRD ref, got code=%d", code)

	var result productworkflow.ProductAuditResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	adrSection := findAuditSection(t, result, "adr")
	gomega.Expect(adrSection.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusFail), "expected adr section to fail, got %q", adrSection.Status)

	found := false

	for _, f := range adrSection.Findings {
		if f.Code == "broken_link" && strings.Contains(f.Message, "REQ-MISSING") {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected broken_link finding in adr section mentioning REQ-MISSING, got: %#v", adrSection.Findings)
})

var _ = ginkgo.It("product audit supersession detects non-reciprocal ADR links", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "adrs/ADR-001-test.md", `---
schema_version: idiom.adr.v1
adr_id: ADR-001
status: accepted
accepted_at: 2026-05-17T00:00:00Z
accepted_by: test-reviewer
acceptance_profile: human-review
prd_refs:
  - REQ-TEST-001
evidence_refs:
  - EV-001
tdd_obligations:
  - TDD-001
coverage_tier: unit
supersedes:
  - ADR-002
---

# ADR-001: Test

## Context
Test.
## Decision
Test.
## Alternatives Considered
None.
## Evidence
- EV-001: Test.
## Consequences
None.
## Compatibility and Migration
Compatible.
## TDD Obligations
- TDD-001: Test.
## Rollback
Revert.
## Open Questions
None.
`)
	writeTestFile(t, dir, "adrs/index.json", `{"schema_version":"v1","effort_id":"test-effort","adrs":[{"adr_id":"ADR-001","title":"Test ADR 1","status":"accepted","path":"ADR-001-test.md","coverage_tier":"unit","prd_refs":["REQ-TEST-001"]},{"adr_id":"ADR-002","title":"Test ADR 2","status":"accepted","path":"ADR-002-test.md","coverage_tier":"unit","prd_refs":["REQ-TEST-001"]}]}`)
	writeTestFile(t, dir, "adrs/ADR-002-test.md", `---
schema_version: idiom.adr.v1
adr_id: ADR-002
status: accepted
accepted_at: 2026-05-17T00:00:00Z
accepted_by: test-reviewer
acceptance_profile: human-review
prd_refs:
  - REQ-TEST-001
evidence_refs:
  - EV-001
tdd_obligations:
  - TDD-001
coverage_tier: unit
---

# ADR-002: Test
## Context
Test.
## Decision
Test.
## Alternatives Considered
None.
## Evidence
- EV-001: Test.
## Consequences
None.
## Compatibility and Migration
Compatible.
## TDD Obligations
- TDD-001: Test.
## Rollback
Revert.
## Open Questions
None.
`)

	var stdout, stderr bytes.Buffer

	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected audit failure, got %d", code)

	var result productworkflow.ProductAuditResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	adrSection := findAuditSection(t, result, "adr")
	found := false

	for _, f := range adrSection.Findings {
		if f.Code == "adr_supersession_non_reciprocal" {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected supersession non-reciprocal finding, got %#v", adrSection.Findings)
})

var _ = ginkgo.It("product audit supersession detects broken adr index links", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "adrs/index.json", `{"schema_version":"v1","effort_id":"test-effort","adrs":[{"adr_id":"ADR-001","title":"Test ADR","status":"accepted","path":"ADR-001-test.md","coverage_tier":"unit","prd_refs":["REQ-TEST-001"],"supersedes":["ADR-MISSING"]}]}`)

	var stdout, stderr bytes.Buffer

	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected audit failure, got %d", code)

	var result productworkflow.ProductAuditResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	adrSection := findAuditSection(t, result, "adr")
	found := false

	for _, f := range adrSection.Findings {
		if f.Path == "adrs/index.json" && f.Code == "parse_error" && strings.Contains(f.Message, "ADR-MISSING") {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected ADR index supersession finding, got %#v", adrSection.Findings)
})

var _ = ginkgo.It("product audit fails on a missing MUST-priority PRD TDD obligation", func() {
	t := ginkgo.GinkgoT()
	dir := writeValidTestEffort(t)
	writeTestFile(t, dir, "prd.md", `---
schema_version: idiom.prd.v1
prd_id: prd-test
status: draft
task_id: impl-test
created_at: 2026-05-17T00:00:00Z
---

# PRD: Test

## Requirements

### REQ-TEST-001

- **id**: REQ-TEST-001
- **priority**: must
- **statement**: Test requirement
- **owner_alignment**: team
- **testability**: automated
- **metric**: coverage > 80%
- **evidence_refs**: ["EV-001"]
- **tdd_obligations**: ["TDD-001", "TDD-MISSING"]
- **open_questions**: []
`)

	var stdout, stderr bytes.Buffer

	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected audit to fail on missing PRD TDD obligation, got code=%d", code)

	var result productworkflow.ProductAuditResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	tddSection := findAuditSection(t, result, "tdd")
	gomega.Expect(tddSection.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusFail), "expected tdd section to fail, got %q", tddSection.Status)

	found := false

	for _, f := range tddSection.Findings {
		if f.Code == "broken_link" && strings.Contains(f.Message, "TDD-MISSING") {
			found = true
			break
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected broken_link finding in tdd section mentioning TDD-MISSING, got: %#v", tddSection.Findings)
})

var _ = ginkgo.It("product audit next valid action parity", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()

	var jsonStdout, jsonStderr bytes.Buffer

	jsonCode := runProductAudit([]string{"--effort-root", dir, "--json"}, &jsonStdout, &jsonStderr, nil)

	gomega.Expect(jsonCode).To(gomega.BeEquivalentTo(1), "expected audit failure exit code 1, got code=%d stdout=%s stderr=%s", jsonCode, jsonStdout.String(), jsonStderr.String())

	var out productworkflow.ProductAuditResult

	{
		err := json.Unmarshal(jsonStdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "expected valid audit JSON: %v", err)
	}

	gomega.Expect(strings.TrimSpace(out.NextValidAction)).NotTo(gomega.BeEquivalentTo(""), "expected next_valid_action in JSON, got %#v", out)

	var textStdout, textStderr bytes.Buffer

	textCode := runProductAudit([]string{"--effort-root", dir}, &textStdout, &textStderr, nil)

	gomega.Expect(textCode).To(gomega.BeEquivalentTo(jsonCode), "expected text and JSON exit code parity, got text=%d json=%d", textCode, jsonCode)

	text := textStdout.String()
	gomega.Expect(strings.Contains(text, "  status: FAIL")).To(gomega.BeTrue(), "expected deterministic FAIL status label, got:\n%s", text)

	gomega.Expect(strings.Contains(text, "  [missing_file] workflow_state.json: required file not found")).To(gomega.BeTrue(), "expected typed finding identity in text output, got:\n%s", text)

	gomega.Expect(strings.Contains(text, "  next: "+out.NextValidAction)).To(gomega.BeTrue(), "expected text next action parity with JSON value %q, got:\n%s", out.NextValidAction, text)
})

var _ = ginkgo.It("product audit scope gate failing includes section", func() {
	t := ginkgo.GinkgoT()
	dir := writeScopeGateEffort(t, scopeGateLedgerForbiddenExactPathJSONL())

	var stdout, stderr bytes.Buffer

	code := runProductAudit([]string{"--effort-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected exit code 1, got %d", code)

	var result productworkflow.ProductAuditResult

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	scopeSection := findAuditSection(t, result, "scope_gate")
	gomega.Expect(scopeSection.Status).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusFail), "expected scope_gate section to fail, got %q", scopeSection.Status)

	found := false

	for _, f := range scopeSection.Findings {
		if f.Code == "scope_gate_forbidden_path" && strings.Contains(f.Message, "impl-scope-test") {
			found = true
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected scope_gate_forbidden_path in scope_gate section, got %v", scopeSection.Findings)
})
