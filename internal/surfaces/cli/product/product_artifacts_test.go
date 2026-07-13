package product

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/workflows/productworkflow"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.It("product prd render prompt writes prompt only", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())

	var stdout, stderr bytes.Buffer

	code := runProductPRD([]string{
		"--effort-root", dir,
		"--render-prompt",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected code 0, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result["status"]).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusPass), "expected status pass, got %v", result["status"])

	gomega.Expect(strings.Contains(stdout.String(), "Draft a Product Requirements Document")).To(gomega.BeFalse(), "JSON output should not include rendered prompt body:\n%s", stdout.String())

	gomega.Expect(result["prompt_write_count"].(float64)).To(gomega.BeEquivalentTo(1), "expected prompt_write_count=1, got %v", result["prompt_write_count"])

	promptData, err := os.ReadFile(filepath.Join(dir, "prd.prompt.md"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "read prompt: %v", err)

	prompt := string(promptData)
	gomega.Expect(prompt).To(gomega.ContainSubstring("PRD parser/audit contract"), "missing PRD contract guidance in prompt:\n%s", prompt)
	gomega.Expect(prompt).To(gomega.ContainSubstring("Do not claim acceptance"), "missing PRD contract guidance in prompt:\n%s", prompt)
	gomega.Expect(prompt).To(gomega.ContainSubstring("Known TDD obligations"), "missing PRD contract guidance in prompt:\n%s", prompt)
	gomega.Expect(prompt).To(gomega.ContainSubstring("TDD-001"), "missing PRD contract guidance in prompt:\n%s", prompt)

	{
		_, err := os.Stat(filepath.Join(dir, "prd.md"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "expected no prd.md write, stat err=%v", err)
	}

	{
		_, err := os.Stat(filepath.Join(dir, "events", "prd-draft.events.jsonl"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "expected no events write, stat err=%v", err)
	}

	{
		_, err := os.Stat(filepath.Join(dir, "reports", "prd-draft.report.json"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "expected no report write, stat err=%v", err)
	}
})

var _ = ginkgo.It("product prd render prompt fails closed on invalid evidence ledger", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeTestFile(t, dir, "evidence/evidence.jsonl", "{bad json")
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())

	var stdout, stderr bytes.Buffer

	code := runProductPRD([]string{
		"--effort-root", dir,
		"--render-prompt",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for invalid evidence ledger, got %d", code)

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result["status"]).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusBlocked), "expected blocked status, got %v", result["status"])

	plan := result["plan"].(map[string]any)
	findings := plan["findings"].([]any)

	gomega.Expect(findings[0].(map[string]any)["kind"]).To(gomega.BeEquivalentTo("validation_error"), "expected validation_error finding, got %#v", findings)

	{
		_, err := os.Stat(filepath.Join(dir, "prd.prompt.md"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "expected no prompt write, stat err=%v", err)
	}
})

var _ = ginkgo.It("product prd render prompt fails closed when evidence ledger missing", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())

	var stdout, stderr bytes.Buffer

	code := runProductPRD([]string{
		"--effort-root", dir,
		"--render-prompt",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for missing evidence ledger, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	plan := result["plan"].(map[string]any)
	findings := plan["findings"].([]any)

	gomega.Expect(findings[0].(map[string]any)["kind"]).To(gomega.BeEquivalentTo("missing_evidence_ledger"), "expected missing_evidence_ledger finding, got %#v", findings)

	{
		_, err := os.Stat(filepath.Join(dir, "prd.prompt.md"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "expected no prompt write, stat err=%v", err)
	}
})

var _ = ginkgo.It("product prd render prompt rejects unsafe prompt or output path", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())

	var stdout, stderr bytes.Buffer

	code := runProductPRD([]string{
		"--effort-root", dir,
		"--render-prompt",
		"--prd-prompt", "../bad.prompt.md",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected blocked code for traversal prompt path, got %d", code)

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v", err)
	}

	plan := result["plan"].(map[string]any)
	findings := plan["findings"].([]any)

	gomega.Expect(findings[0].(map[string]any)["kind"]).To(gomega.BeEquivalentTo("invalid_prd_prompt_path"), "expected invalid_prd_prompt_path, got %#v", findings)

	stdout.Reset()
	stderr.Reset()

	code = runProductPRD([]string{
		"--effort-root", dir,
		"--render-prompt",
		"--prd-output", "../bad.md",
		"--json",
	}, &stdout, &stderr, nil)
	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected blocked code for traversal output path, got %d", code)
})

var _ = ginkgo.It("product validate artifact fails closed for invalid generated prd", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeTestFile(t, dir, "prd.md", `---
schema_version: idiom.prd.v1
prd_id: prd-test
status: draft
task_id: impl-test
created_at: 2026-05-17T00:00:00Z
---

# PRD: Test

## Requirements

| id | priority | statement |
| --- | --- | --- |
| REQ-1 | must | do it |
`)

	var stdout, stderr bytes.Buffer

	code := runProductValidateArtifact([]string{
		"--kind", "prd",
		"--path", filepath.Join(dir, "prd.md"),
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for parser-invalid PRD, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out productworkflow.ProductArtifactValidationResult

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(out.Status).To(gomega.Equal(productworkflow.ProductWorkflowStatusFail), "expected PRD parser failure, got %#v", out)
	gomega.Expect(out.Error).To(gomega.ContainSubstring("PRD has no requirements"), "expected PRD parser failure, got %#v", out)
})

var _ = ginkgo.It("product artifact validation fails closed for an unknown PRD TDD obligation", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())
	writeTestFile(t, dir, "prd.md", strings.Replace(validTestPRDMarkdown(), `["TDD-001"]`, `["The generated test must pass."]`, 1))

	var stdout, stderr bytes.Buffer

	code := runProductValidateArtifact([]string{
		"--kind", "prd",
		"--path", filepath.Join(dir, "prd.md"),
		"--tdd-ledger", filepath.Join(dir, "tdd.json"),
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for traceability-invalid PRD, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out productworkflow.ProductArtifactValidationResult

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(out.Status).To(gomega.Equal(productworkflow.ProductWorkflowStatusFail), "expected PRD TDD cross-link failure, got %#v", out)
	gomega.Expect(out.Error).To(gomega.ContainSubstring("references TDD obligation"), "expected PRD TDD cross-link failure, got %#v", out)
})

var _ = ginkgo.It("product adr render prompt writes prompt only", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "prd.md", validTestPRDMarkdown())

	var stdout, stderr bytes.Buffer

	code := runProductADR([]string{
		"--effort-root", dir,
		"--render-prompt",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected code 0, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result["status"]).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusPass), "expected status pass, got %v", result["status"])

	gomega.Expect(strings.Contains(stdout.String(), "Draft an Architecture Decision Record")).To(gomega.BeFalse(), "JSON output should not include rendered prompt body:\n%s", stdout.String())

	gomega.Expect(result["prompt_write_count"].(float64)).To(gomega.BeEquivalentTo(1), "expected prompt_write_count=1, got %v", result["prompt_write_count"])

	promptData, err := os.ReadFile(filepath.Join(dir, "adrs", "adr.prompt.md"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "read prompt: %v", err)

	prompt := string(promptData)
	gomega.Expect(prompt).To(gomega.ContainSubstring("ADR parser/audit contract"), "missing ADR contract guidance in prompt:\n%s", prompt)
	gomega.Expect(prompt).To(gomega.ContainSubstring("Do not claim acceptance, approval, or supersession."), "missing ADR contract guidance in prompt:\n%s", prompt)

	{
		_, err := os.Stat(filepath.Join(dir, "adrs", "ADR-draft.md"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "expected no ADR write, stat err=%v", err)
	}

	{
		_, err := os.Stat(filepath.Join(dir, "events", "adr-draft.events.jsonl"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "expected no events write, stat err=%v", err)
	}

	{
		_, err := os.Stat(filepath.Join(dir, "reports", "adr-draft.report.json"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "expected no report write, stat err=%v", err)
	}
})

var _ = ginkgo.It("product adr render prompt fails closed on invalid evidence or prd", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeTestFile(t, dir, "evidence/evidence.jsonl", "{bad json")
	writeTestFile(t, dir, "prd.md", validTestPRDMarkdown())

	var stdout, stderr bytes.Buffer

	code := runProductADR([]string{
		"--effort-root", dir,
		"--render-prompt",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for invalid evidence, got %d", code)

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	plan := result["plan"].(map[string]any)
	findings := plan["findings"].([]any)

	gomega.Expect(findings[0].(map[string]any)["kind"]).To(gomega.BeEquivalentTo("validation_error"), "expected validation_error finding, got %#v", findings)

	dir2 := t.TempDir()
	writeTestFile(t, dir2, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir2, "prd.md", "not valid prd")
	stdout.Reset()
	stderr.Reset()

	code = runProductADR([]string{
		"--effort-root", dir2,
		"--render-prompt",
		"--json",
	}, &stdout, &stderr, nil)
	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for invalid prd, got %d", code)

	{
		_, err := os.Stat(filepath.Join(dir2, "adrs", "adr.prompt.md"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "expected no prompt write for invalid prd, stat err=%v", err)
	}
})

var _ = ginkgo.It("product adr render prompt fails closed when inputs missing", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()

	var stdout, stderr bytes.Buffer

	code := runProductADR([]string{
		"--effort-root", dir,
		"--render-prompt",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for missing inputs, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	plan := result["plan"].(map[string]any)
	findings := plan["findings"].([]any)

	var sawEvidence, sawPRD bool

	for _, item := range findings {
		kind := item.(map[string]any)["kind"]
		if kind == "missing_evidence_ledger" {
			sawEvidence = true
		}

		if kind == "missing_prd" {
			sawPRD = true
		}
	}

	gomega.Expect(sawEvidence).To(gomega.BeTrue(), "expected missing evidence and PRD findings, got %#v", findings)
	gomega.Expect(sawPRD).To(gomega.BeTrue(), "expected missing evidence and PRD findings, got %#v", findings)

	{
		_, err := os.Stat(filepath.Join(dir, "adrs", "adr.prompt.md"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "expected no prompt write for missing inputs, stat err=%v", err)
	}
})

var _ = ginkgo.It("product adr render prompt rejects unsafe prompt or output path", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "prd.md", validTestPRDMarkdown())

	var stdout, stderr bytes.Buffer

	code := runProductADR([]string{
		"--effort-root", dir,
		"--render-prompt",
		"--adr-prompt", "../bad.prompt.md",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected blocked code for traversal prompt path, got %d", code)

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v", err)
	}

	plan := result["plan"].(map[string]any)
	findings := plan["findings"].([]any)

	gomega.Expect(findings[0].(map[string]any)["kind"]).To(gomega.BeEquivalentTo("invalid_adr_prompt_path"), "expected invalid_adr_prompt_path, got %#v", findings)

	stdout.Reset()
	stderr.Reset()

	code = runProductADR([]string{
		"--effort-root", dir,
		"--render-prompt",
		"--adr-output", "../bad.md",
		"--json",
	}, &stdout, &stderr, nil)
	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected blocked code for traversal output path, got %d", code)
})

var _ = ginkgo.It("product validate artifact fails closed for invalid generated adr", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeTestFile(t, dir, "adr.md", strings.Replace(validTestADRMarkdown(), "coverage_tier: unit", "coverage_tier: must", 1))

	var stdout, stderr bytes.Buffer

	code := runProductValidateArtifact([]string{
		"--kind", "adr",
		"--path", filepath.Join(dir, "adr.md"),
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for parser-invalid ADR, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out productworkflow.ProductArtifactValidationResult

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(out.Status).To(gomega.Equal(productworkflow.ProductWorkflowStatusFail), "expected ADR coverage tier failure, got %#v", out)
	gomega.Expect(out.Error).To(gomega.ContainSubstring("invalid coverage_tier"), "expected ADR coverage tier failure, got %#v", out)
})

var _ = ginkgo.It("product artifact validation fails closed for an unknown ADR TDD obligation", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeTestFile(t, dir, "prd.md", validTestPRDMarkdown())
	writeTestFile(t, dir, "tdd.json", validTestTDDJSON())
	writeTestFile(t, dir, "adr.md", strings.Replace(validTestADRMarkdown(), "TDD-001", "The generated test must pass.", 1))

	var stdout, stderr bytes.Buffer

	code := runProductValidateArtifact([]string{
		"--kind", "adr",
		"--path", filepath.Join(dir, "adr.md"),
		"--prd", filepath.Join(dir, "prd.md"),
		"--tdd-ledger", filepath.Join(dir, "tdd.json"),
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for traceability-invalid ADR, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var out productworkflow.ProductArtifactValidationResult

	{
		err := json.Unmarshal(stdout.Bytes(), &out)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(out.Status).To(gomega.Equal(productworkflow.ProductWorkflowStatusFail), "expected ADR TDD cross-link failure, got %#v", out)
	gomega.Expect(out.Error).To(gomega.ContainSubstring("tdd_obligation"), "expected ADR TDD cross-link failure, got %#v", out)
})
