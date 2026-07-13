package product

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/workflows/productworkflow"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.It("product research json surface", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	profilesDir := filepath.Join(dir, ".idiom", "research", "profiles")

	{
		err := os.MkdirAll(profilesDir, 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "mkdir: %v", err)
	}

	{
		err := os.WriteFile(filepath.Join(profilesDir, "test.json"), []byte(validTestResearchProfileJSON()), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "write profile: %v", err)
	}

	var stdout, stderr bytes.Buffer

	code := runProductResearch([]string{"--project-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected code 0, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	profiles, ok := result["profiles"].([]any)
	gomega.Expect(ok).To(gomega.BeTrue(), "expected profiles array, got %T", result["profiles"])

	gomega.Expect(len(profiles)).To(gomega.BeEquivalentTo(1), "expected 1 profile, got %d", len(profiles))

	p := profiles[0].(map[string]any)
	gomega.Expect(p["name"]).To(gomega.BeEquivalentTo("test-profile"), "expected name test-profile, got %v", p["name"])

	{
		_, ok := p["researchers"]
		gomega.Expect(ok).To(gomega.BeTrue(), "expected researchers field")
	}

	{
		_, ok := p["merge_policy"]
		gomega.Expect(ok).To(gomega.BeTrue(), "expected merge_policy field")
	}

	gomega.Expect(result["profile_count"].(float64)).To(gomega.BeEquivalentTo(1), "expected profile_count 1, got %v", result["profile_count"])

	findings, ok := result["findings"].([]any)
	gomega.Expect(ok).To(gomega.BeTrue(), "expected findings array, got %T", result["findings"])

	gomega.Expect(len(findings)).To(gomega.BeEquivalentTo(0), "expected 0 findings, got %d", len(findings))
})

var _ = ginkgo.It("product research returns exit 1 for an invalid profile", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	profilesDir := filepath.Join(dir, ".idiom", "research", "profiles")

	{
		err := os.MkdirAll(profilesDir, 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "mkdir: %v", err)
	}

	{
		err := os.WriteFile(filepath.Join(profilesDir, "bad.json"), []byte(`{not valid json`), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "write profile: %v", err)
	}

	var stdout, stderr bytes.Buffer

	code := runProductResearch([]string{"--project-root", dir, "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1, got %d", code)

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	findings, ok := result["findings"].([]any)
	gomega.Expect(ok).To(gomega.BeTrue(), "expected at least one finding, got %v", result["findings"])
	gomega.Expect(findings).NotTo(gomega.BeEmpty(), "expected at least one finding, got %v", result["findings"])
})

var _ = ginkgo.It("product research merge evidence writes merged ledger", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeProductResearchProfile(t, dir, validTestResearchProfileJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "research/outputs/technical.json", validTestResearchCardsJSON("EV-002", "r1", "Merged research claim"))

	var stdout, stderr bytes.Buffer

	code := runProductResearch([]string{"--project-root", dir, "--effort-root", dir, "--merge-evidence", "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected code 0, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result["status"]).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusPass), "expected status pass, got %v", result["status"])

	gomega.Expect(result["wrote_evidence_ledger"]).To(gomega.BeEquivalentTo(true), "expected wrote_evidence_ledger true, got %v", result["wrote_evidence_ledger"])

	merged, err := os.ReadFile(filepath.Join(dir, "evidence", "evidence.jsonl"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "read merged evidence: %v", err)

	cards, err := productworkflow.ParseEvidenceCardsJSONL(merged)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "merged evidence should parse: %v\n%s", err, string(merged))

	gomega.Expect(len(cards)).To(gomega.BeEquivalentTo(2), "expected 2 merged cards, got %d: %#v", len(cards), cards)

	gomega.Expect(cards[1].CardID).To(gomega.BeEquivalentTo("EV-002"), "expected EV-002 to be merged after existing card, got %#v", cards)
})

var _ = ginkgo.It("product research merge evidence blocks on conflict", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeProductResearchProfile(t, dir, validTestResearchProfileJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "research/outputs/product.json", validTestResearchCardsJSON("EV-001", "r1", "Conflicting claim"))

	var stdout, stderr bytes.Buffer

	code := runProductResearch([]string{"--project-root", dir, "--effort-root", dir, "--merge-evidence", "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for conflict, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result["status"]).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusBlocked), "expected status blocked, got %v", result["status"])

	conflicts, ok := result["conflicts"].([]any)
	gomega.Expect(ok).To(gomega.BeTrue(), "expected one conflict, got %v", result["conflicts"])
	gomega.Expect(conflicts).To(gomega.HaveLen(1), "expected one conflict, got %v", result["conflicts"])

	gomega.Expect(result["wrote_evidence_ledger"]).To(gomega.BeEquivalentTo(false), "expected no write on conflict, got %v", result["wrote_evidence_ledger"])
})

var _ = ginkgo.It("product research merge evidence blocks on invalid research output", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeProductResearchProfile(t, dir, validTestResearchProfileJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "research/outputs/bad.json", `{"schema_version":"idiom.research_cards.v1","researcher_id":"r1","alignment":"technical","cards":[]}`)

	var stdout, stderr bytes.Buffer

	code := runProductResearch([]string{"--project-root", dir, "--effort-root", dir, "--merge-evidence", "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for invalid research output, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	findings, ok := result["findings"].([]any)
	gomega.Expect(ok).To(gomega.BeTrue(), "expected findings for invalid research output, got %v", result["findings"])
	gomega.Expect(findings).NotTo(gomega.BeEmpty(), "expected findings for invalid research output, got %v", result["findings"])
})

var _ = ginkgo.It("product research merge evidence defers unsupported claims to prd audit", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeProductResearchProfile(t, dir, validTestResearchProfileJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())

	unsupported := strings.Replace(
		validTestResearchCardsJSON("EV-002", "r1", "Unsupported claim"),
		`"kind": "finding"`,
		`"kind": "unsupported_claim"`,
		1,
	)
	writeTestFile(t, dir, "research/outputs/product.json", unsupported)

	var stdout, stderr bytes.Buffer

	code := runProductResearch([]string{"--project-root", dir, "--effort-root", dir, "--merge-evidence", "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected code 0 when unsupported claims are deferred to PRD audit, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result["wrote_evidence_ledger"]).To(gomega.BeEquivalentTo(true), "expected merge to write ledger, got %v", result["wrote_evidence_ledger"])

	findings := result["findings"].([]any)
	gomega.Expect(len(findings)).To(gomega.BeEquivalentTo(1), "expected one unsupported_claim warning, got %v", findings)

	first := findings[0].(map[string]any)
	gomega.Expect(first["kind"]).To(gomega.Equal("unsupported_claim"), "expected unsupported_claim warning, got %v", first)
	gomega.Expect(first["severity"]).To(gomega.Equal("warning"), "expected unsupported_claim warning, got %v", first)
})

var _ = ginkgo.It("product research merge evidence requires profile when multiple profiles exist", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeProductResearchProfileFile(t, dir, "a.json", validTestResearchProfileJSONWithPolicy("warn-profile", "warn-profile", "warn"))
	writeProductResearchProfileFile(t, dir, "b.json", validTestResearchProfileJSONWithPolicy("block-profile", "block-profile", "block_research_merge"))
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "research/outputs/product.json", validTestResearchCardsJSON("EV-002", "r1", "Merged research claim"))

	var stdout, stderr bytes.Buffer

	code := runProductResearch([]string{"--project-root", dir, "--effort-root", dir, "--merge-evidence", "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for ambiguous research profile, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	findings, ok := result["findings"].([]any)
	gomega.Expect(ok).To(gomega.BeTrue(), "expected findings for ambiguous research profile, got %v", result["findings"])
	gomega.Expect(findings).NotTo(gomega.BeEmpty(), "expected findings for ambiguous research profile, got %v", result["findings"])

	found := false

	for _, raw := range findings {
		finding := raw.(map[string]any)
		if finding["kind"] == "ambiguous_research_profile" && finding["severity"] == "error" {
			found = true
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected ambiguous_research_profile error, got %v", findings)

	gomega.Expect(result["wrote_evidence_ledger"]).To(gomega.BeEquivalentTo(false), "expected no ledger write for ambiguous profile, got %v", result["wrote_evidence_ledger"])
})

var _ = ginkgo.It("product research merge evidence uses selected profile policy", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeProductResearchProfileFile(t, dir, "a.json", validTestResearchProfileJSONWithPolicy("warn-profile", "warn-profile", "warn"))
	writeProductResearchProfileFile(t, dir, "b.json", validTestResearchProfileJSONWithPolicy("block-profile", "block-profile", "block_research_merge"))
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())

	unsupported := strings.Replace(
		validTestResearchCardsJSON("EV-002", "r1", "Unsupported claim"),
		`"kind": "finding"`,
		`"kind": "unsupported_claim"`,
		1,
	)
	writeTestFile(t, dir, "research/outputs/product.json", unsupported)

	var stdout, stderr bytes.Buffer

	code := runProductResearch([]string{"--project-root", dir, "--effort-root", dir, "--merge-evidence", "--profile", "block-profile", "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 from selected block-profile policy, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result["profile"]).To(gomega.BeEquivalentTo("block-profile"), "expected selected profile in output, got %v", result["profile"])

	findings, ok := result["findings"].([]any)
	gomega.Expect(ok).To(gomega.BeTrue(), "expected unsupported claim finding, got %v", result["findings"])
	gomega.Expect(findings).NotTo(gomega.BeEmpty(), "expected unsupported claim finding, got %v", result["findings"])

	found := false

	for _, raw := range findings {
		finding := raw.(map[string]any)
		if finding["kind"] == "unsupported_claim" && finding["severity"] == "error" {
			found = true
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected unsupported_claim error from selected profile, got %v", findings)

	gomega.Expect(result["wrote_evidence_ledger"]).To(gomega.BeEquivalentTo(false), "expected no ledger write for selected blocking profile, got %v", result["wrote_evidence_ledger"])
})

var _ = ginkgo.It("product research merge evidence rejects traversal output dir", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeProductResearchProfile(t, dir, validTestResearchProfileJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())

	var stdout, stderr bytes.Buffer

	code := runProductResearch([]string{"--project-root", dir, "--effort-root", dir, "--merge-evidence", "--research-output-dir", "../outside", "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for traversal output dir, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	findings, ok := result["findings"].([]any)
	gomega.Expect(ok).To(gomega.BeTrue(), "expected findings for traversal output dir, got %v", result["findings"])
	gomega.Expect(findings).NotTo(gomega.BeEmpty(), "expected findings for traversal output dir, got %v", result["findings"])

	first := findings[0].(map[string]any)
	gomega.Expect(first["kind"]).To(gomega.BeEquivalentTo("invalid_research_output_dir"), "expected invalid_research_output_dir, got %v", first)
})

var _ = ginkgo.It("product research merge evidence rejects symlink output dir", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	outside := t.TempDir()
	writeProductResearchProfile(t, dir, validTestResearchProfileJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())

	if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil {
		ginkgo.Skip(fmt.Sprintf("symlink unavailable: %v", err))
	}

	var stdout, stderr bytes.Buffer

	code := runProductResearch([]string{"--project-root", dir, "--effort-root", dir, "--merge-evidence", "--research-output-dir", "linked", "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for symlink output dir, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	findings, ok := result["findings"].([]any)
	gomega.Expect(ok).To(gomega.BeTrue(), "expected findings for symlink output dir, got %v", result["findings"])
	gomega.Expect(findings).NotTo(gomega.BeEmpty(), "expected findings for symlink output dir, got %v", result["findings"])

	first := findings[0].(map[string]any)
	gomega.Expect(first["kind"]).To(gomega.BeEquivalentTo("research_output_dir_outside_effort"), "expected research_output_dir_outside_effort, got %v", first)
})

var _ = ginkgo.It("product research plan runs json surface", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeProductResearchProfile(t, dir, validTestResearchProfileJSON())

	var stdout, stderr bytes.Buffer

	code := runProductResearch([]string{"--project-root", dir, "--plan-runs", "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected code 0, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result["selected_profile_id"]).To(gomega.BeEquivalentTo("test-profile"), "expected selected_profile_id test-profile, got %v", result["selected_profile_id"])

	gomega.Expect(result["profile_count"].(float64)).To(gomega.BeEquivalentTo(1), "expected profile_count 1, got %v", result["profile_count"])

	gomega.Expect(result["run_count"].(float64)).To(gomega.BeEquivalentTo(1), "expected run_count 1, got %v", result["run_count"])

	runs := result["runs"].([]any)
	gomega.Expect(len(runs)).To(gomega.BeEquivalentTo(1), "expected one run, got %d", len(runs))

	run := runs[0].(map[string]any)
	gomega.Expect(run["output_path"]).To(gomega.BeEquivalentTo("research/outputs/test-profile--r1.json"), "unexpected output_path: %v", run["output_path"])

	contract := run["output_contract"].(map[string]any)
	gomega.Expect(contract["schema_version"]).To(gomega.BeEquivalentTo("idiom.research_cards.v1"), "unexpected output contract schema: %v", contract["schema_version"])
})

var _ = ginkgo.It("product research plan runs requires profile when multiple profiles exist", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeProductResearchProfileFile(t, dir, "a.json", validTestResearchProfileJSONWithPolicy("warn-profile", "warn-profile", "warn"))
	writeProductResearchProfileFile(t, dir, "b.json", validTestResearchProfileJSONWithPolicy("block-profile", "block-profile", "block_research_merge"))

	var stdout, stderr bytes.Buffer

	code := runProductResearch([]string{"--project-root", dir, "--plan-runs", "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for ambiguous profile, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	findings := result["findings"].([]any)
	found := false

	for _, raw := range findings {
		f := raw.(map[string]any)
		if f["kind"] == "ambiguous_research_profile" && f["severity"] == "error" {
			found = true
		}
	}

	gomega.Expect(found).To(gomega.BeTrue(), "expected ambiguous_research_profile error, got %v", findings)
})

var _ = ginkgo.It("product research plan runs uses selected profile", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeProductResearchProfileFile(t, dir, "a.json", validTestResearchProfileJSONWithPolicy("warn-profile", "warn-profile", "warn"))
	writeProductResearchProfileFile(t, dir, "b.json", validTestResearchProfileJSONWithPolicy("block-profile", "block-profile", "block_research_merge"))

	var stdout, stderr bytes.Buffer

	code := runProductResearch([]string{"--project-root", dir, "--plan-runs", "--profile", "block-profile", "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected code 0, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result["selected_profile_id"]).To(gomega.BeEquivalentTo("block-profile"), "expected selected_profile_id block-profile, got %v", result["selected_profile_id"])

	runs := result["runs"].([]any)
	run := runs[0].(map[string]any)

	gomega.Expect(run["output_path"]).To(gomega.BeEquivalentTo("research/outputs/block-profile--r1.json"), "unexpected output path for selected profile: %v", run["output_path"])
})

var _ = ginkgo.It("product research plan runs does not write evidence ledger", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeProductResearchProfile(t, dir, validTestResearchProfileJSON())

	var stdout, stderr bytes.Buffer

	code := runProductResearch([]string{"--project-root", dir, "--plan-runs", "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected code 0, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	{
		_, err := os.Stat(filepath.Join(dir, "evidence", "evidence.jsonl"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "expected no evidence/evidence.jsonl write during planning, stat err=%v", err)
	}
})

var _ = ginkgo.It("product research plan runs rejects merge evidence flag", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeProductResearchProfile(t, dir, validTestResearchProfileJSON())
	writeTestFile(t, dir, "evidence/evidence.jsonl", validTestEvidenceJSONL())
	writeTestFile(t, dir, "research/outputs/technical.json", validTestResearchCardsJSON("EV-002", "r1", "Should not merge"))

	var stdout, stderr bytes.Buffer

	code := runProductResearch([]string{"--project-root", dir, "--effort-root", dir, "--plan-runs", "--merge-evidence", "--json"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(2), "expected usage code 2 for mutually exclusive flags, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	gomega.Expect(strings.Contains(stderr.String(), "mutually exclusive")).To(gomega.BeTrue(), "expected mutual exclusion error, got stderr=%q", stderr.String())

	merged, err := os.ReadFile(filepath.Join(dir, "evidence", "evidence.jsonl"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "read evidence ledger: %v", err)

	cards, err := productworkflow.ParseEvidenceCardsJSONL(merged)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "evidence ledger should remain parseable: %v", err)

	gomega.Expect(cards).To(gomega.HaveLen(1), "expected merge not to run, got cards %#v", cards)
	gomega.Expect(cards[0].CardID).To(gomega.Equal("EV-001"), "expected merge not to run, got cards %#v", cards)
})

var _ = ginkgo.It("product research render prompts json surface writes prompt files only", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeProductResearchProfile(t, dir, validTestResearchProfileJSON())

	var stdout, stderr bytes.Buffer

	code := runProductResearch([]string{
		"--project-root", dir,
		"--effort-root", dir,
		"--render-prompts",
		"--research-prompt-dir", "research/prompts",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected code 0, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result["status"]).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusPass), "expected status pass, got %v", result["status"])

	gomega.Expect(result["prompt_write_count"].(float64)).To(gomega.BeEquivalentTo(1), "expected prompt_write_count 1, got %v", result["prompt_write_count"])

	plan := result["plan"].(map[string]any)
	runs := plan["runs"].([]any)
	run := runs[0].(map[string]any)

	gomega.Expect(run["prompt_path"]).To(gomega.BeEquivalentTo("research/prompts/test-profile--r1.prompt.txt"), "unexpected prompt path: %v", run["prompt_path"])

	command := run["command"].(string)
	gomega.Expect(command).To(gomega.ContainSubstring("idiom artifact --prompt-file research/prompts/test-profile--r1.prompt.txt"), "unexpected command recipe: %q", command)
	gomega.Expect(command).To(gomega.ContainSubstring("--no-tools --mechanical-repair"), "unexpected command recipe: %q", command)

	promptData, err := os.ReadFile(filepath.Join(dir, "research", "prompts", "test-profile--r1.prompt.txt"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "read prompt file: %v", err)

	prompt := string(promptData)
	gomega.Expect(prompt).To(gomega.ContainSubstring("Write JSON only."), "prompt missing no-tools/json requirements:\n%s", prompt)
	gomega.Expect(prompt).To(gomega.ContainSubstring("Do not call tools or inspect the workspace"), "prompt missing no-tools/json requirements:\n%s", prompt)

	{
		_, err := os.Stat(filepath.Join(dir, "research", "outputs", "test-profile--r1.json"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "expected no research output write, stat err=%v", err)
	}

	{
		_, err := os.Stat(filepath.Join(dir, "evidence", "evidence.jsonl"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "expected no evidence write, stat err=%v", err)
	}
})

var _ = ginkgo.It("product research render prompts uses selected profile", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeProductResearchProfileFile(t, dir, "a.json", validTestResearchProfileJSONWithPolicy("warn-profile", "warn-profile", "warn"))
	writeProductResearchProfileFile(t, dir, "b.json", validTestResearchProfileJSONWithPolicy("block-profile", "block-profile", "block_research_merge"))

	var stdout, stderr bytes.Buffer

	code := runProductResearch([]string{
		"--project-root", dir,
		"--effort-root", dir,
		"--render-prompts",
		"--profile", "block-profile",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected code 0, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	plan := result["plan"].(map[string]any)
	gomega.Expect(plan["selected_profile_id"]).To(gomega.BeEquivalentTo("block-profile"), "expected selected_profile_id block-profile, got %v", plan["selected_profile_id"])
})

var _ = ginkgo.It("product research render prompts rejects mutually exclusive modes", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	writeProductResearchProfile(t, dir, validTestResearchProfileJSON())

	var stdout, stderr bytes.Buffer

	code := runProductResearch([]string{
		"--project-root", dir,
		"--effort-root", dir,
		"--render-prompts",
		"--plan-runs",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(2), "expected usage code 2 for mutually exclusive flags, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	gomega.Expect(strings.Contains(stderr.String(), "mutually exclusive")).To(gomega.BeTrue(), "expected mutual exclusion error, got stderr=%q", stderr.String())
})

var _ = ginkgo.It("product research render prompts fails closed when profiles missing", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()

	var stdout, stderr bytes.Buffer

	code := runProductResearch([]string{
		"--project-root", dir,
		"--effort-root", dir,
		"--render-prompts",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 when profiles are missing, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	gomega.Expect(result["status"]).To(gomega.BeEquivalentTo(productworkflow.ProductWorkflowStatusBlocked), "expected blocked status, got %v", result["status"])

	gomega.Expect(result["prompt_write_count"].(float64)).To(gomega.BeEquivalentTo(0), "expected no prompt writes, got %v", result["prompt_write_count"])

	plan := result["plan"].(map[string]any)
	findings := plan["findings"].([]any)
	first := findings[0].(map[string]any)

	gomega.Expect(first["kind"]).To(gomega.BeEquivalentTo("missing_research_profile"), "expected missing_research_profile, got %v", first)

	{
		_, err := os.Stat(filepath.Join(dir, "research", "prompts"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "expected no prompt directory write, stat err=%v", err)
	}
})

var _ = ginkgo.It("product research render prompts rejects symlink prompt dir", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	outside := t.TempDir()
	writeProductResearchProfile(t, dir, validTestResearchProfileJSON())

	if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil {
		ginkgo.Skip(fmt.Sprintf("symlink unavailable: %v", err))
	}

	var stdout, stderr bytes.Buffer

	code := runProductResearch([]string{
		"--project-root", dir,
		"--effort-root", dir,
		"--render-prompts",
		"--research-prompt-dir", "linked/prompts",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for symlink prompt dir, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	plan := result["plan"].(map[string]any)
	findings := plan["findings"].([]any)
	first := findings[0].(map[string]any)

	gomega.Expect(first["kind"]).To(gomega.BeEquivalentTo("research_prompt_dir_outside_effort"), "expected research_prompt_dir_outside_effort, got %v", first)

	{
		_, err := os.Stat(filepath.Join(outside, "prompts"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "expected no prompt directory outside effort, stat err=%v", err)
	}
})

var _ = ginkgo.It("product research render prompts rejects symlink prompt file before partial writes", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	outside := t.TempDir()
	writeProductResearchProfileFile(t, dir, "profile.json", `{
  "schema_version": "idiom.research_profile.v1",
  "name": "multi",
  "profile_id": "multi",
  "researchers": [
    {"id": "a", "alignment": "technical", "question": "qa", "required_sources": ["repo"], "allowed_capabilities": ["context_query"], "output": "evidence_cards"},
    {"id": "b", "alignment": "product", "question": "qb", "required_sources": ["repo"], "allowed_capabilities": ["context_query"], "output": "evidence_cards"}
  ],
  "merge_policy": {"conflicts": "preserve", "unsupported_claims": "block_research_merge", "stale_local_context": "warn"}
}`)

	{
		err := os.MkdirAll(filepath.Join(dir, "research", "prompts"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "mkdir prompts: %v", err)
	}

	if err := os.Symlink(filepath.Join(outside, "b.prompt.txt"), filepath.Join(dir, "research", "prompts", "multi--b.prompt.txt")); err != nil {
		ginkgo.Skip(fmt.Sprintf("symlink unavailable: %v", err))
	}

	var stdout, stderr bytes.Buffer

	code := runProductResearch([]string{
		"--project-root", dir,
		"--effort-root", dir,
		"--render-prompts",
		"--json",
	}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), "expected code 1 for symlink prompt file, got %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())

	var result map[string]any

	{
		err := json.Unmarshal(stdout.Bytes(), &result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "output should be valid JSON: %v\n%s", err, stdout.String())
	}

	plan := result["plan"].(map[string]any)
	findings := plan["findings"].([]any)
	first := findings[0].(map[string]any)

	gomega.Expect(first["kind"]).To(gomega.BeEquivalentTo("research_prompt_symlink"), "expected research_prompt_symlink, got %v", first)

	{
		_, err := os.Stat(filepath.Join(dir, "research", "prompts", "multi--a.prompt.txt"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "expected no partial prompt write for first run, stat err=%v", err)
	}
})

var _ = ginkgo.It("product research default project root succeeds", func() {
	var stdout, stderr bytes.Buffer

	code := RunMainWithTelemetry([]string{"research"}, &stdout, &stderr, nil)

	gomega.Expect(code).To(gomega.BeEquivalentTo(0), "expected code 0 (defaults to current dir), got %d", code)
})
