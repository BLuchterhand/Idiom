package eval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/evidence/eval"
)

func evalCLITestRepoRoot() string {

	dir, err := os.Getwd()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("get cwd: %v", err))

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		gomega.Expect(parent).NotTo(gomega.Equal(dir), fmt.Sprintf("repo root not found from cwd"))

		dir = parent
	}
}

var _ = ginkgo.Describe("eval", func() {
	ginkgo.It("run prints metrics json", func() {
		dir := ginkgo.GinkgoT().TempDir()
		logPath := filepath.Join(dir, "conversation.jsonl")
		writeQwenEvalLog(logPath)

		var stdout, stderr bytes.Buffer

		code := Run([]string{"-log", logPath, "-json"}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d stderr=%s", code, stderr.String()))

		out := stdout.String()
		for _, want := range []string{`"last_user":"review routing"`, `"budget_ledgers"`, `"route_ambiguities"`, `"control_rails"`} {
			gomega.Expect(out).To(gomega.ContainSubstring(want), fmt.Sprintf("expected JSON output to contain %q, got %s", want, out))

		}
	})
	ginkgo.It("run writes suggested scenario", func() {
		dir := ginkgo.GinkgoT().TempDir()
		logPath := filepath.Join(dir, "conversation.jsonl")
		scenarioPath := filepath.Join(dir, "suggested.json")

		writeQwenEvalLog(logPath)

		var stdout, stderr bytes.Buffer

		code := Run([]string{"-log", logPath, "-write-scenario", scenarioPath}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d stderr=%s", code, stderr.String()))

		data, err := os.ReadFile(scenarioPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read suggested scenario: %v", err))

		for _, want := range []string{`"name": "suggested"`, `"require_budget_ledger"`, `"require_route_ambiguity"`, `"require_control_rails"`} {
			gomega.Expect(string(data)).To(gomega.ContainSubstring(want), fmt.Sprintf("expected suggested scenario to contain %q, got %s", want, string(data)))

		}
	})
	ginkgo.It("run returns failure for scenario mismatch", func() {
		dir := ginkgo.GinkgoT().TempDir()
		logPath := filepath.Join(dir, "conversation.jsonl")
		scenarioPath := filepath.Join(dir, "scenario.json")

		writeQwenEvalLog(logPath)

		scenario := `{
  "name": "wrong-prompt",
  "prompt_contains": ["different prompt"]
}
`
		gomega.Expect(os.WriteFile(scenarioPath, []byte(scenario), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		code := Run([]string{"-log", logPath, "-scenario", scenarioPath}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected exit 1, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("FAIL wrong-prompt"), fmt.Sprintf("expected failure report, got stdout=%s", stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("last user prompt does not contain"), fmt.Sprintf("expected failure report, got stdout=%s", stdout.String()))

	})
	ginkgo.It("run prints localization corpus report", func() {
		corpusPath := filepath.Join(evalCLITestRepoRoot(), "testdata", "localization", "recent-implementation-corpus.json")

		var stdout, stderr bytes.Buffer

		code := Run([]string{"-localization-corpus", corpusPath}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		for _, want := range []string{
			"Localization corpus: PASS recent-implementation-localization tasks=5 experimental=true",
			"Sources: conversation=1, telemetry=5",
			"Redaction: corpus_redacted=1, redacted=5",
			"Expected anchors: files=",
			"Benchmark metrics: experimental=true manual_corpus_review_required=true",
			"Top-k edit-path coverage: k=1",
			"Broad-read count:",
			"Zero-hit search count:",
			"Candidate/path waste:",
			"Competitive conditions:",
			"- raw_search_read status=not_available",
			"- aider_repo_map status=not_available external=true",
			"Metric definitions:",
		} {
			gomega.Expect(stdout.String()).To(gomega.ContainSubstring(want), fmt.Sprintf("expected localization report to contain %q, got %s", want, stdout.String()))

		}
	})
	ginkgo.It("run prints localization corpus json", func() {
		corpusPath := filepath.Join(evalCLITestRepoRoot(), "testdata", "localization", "recent-implementation-corpus.json")

		var stdout, stderr bytes.Buffer

		code := Run([]string{"-localization-corpus", corpusPath, "-json"}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		for _, want := range []string{`"total_tasks": 5`, `"source_coverage"`, `"redaction_status"`, `"top_k_edit_path_coverage"`, `"broad_read_count"`, `"candidate_path_waste"`, `"competitive_conditions"`, `"definitions"`} {
			gomega.Expect(stdout.String()).To(gomega.ContainSubstring(want), fmt.Sprintf("expected JSON localization report to contain %q, got %s", want, stdout.String()))

		}
	})
	ginkgo.It("run prints unified localization report", func() {
		corpusPath := filepath.Join(evalCLITestRepoRoot(), "testdata", "localization", "recent-implementation-corpus.json")
		dir := ginkgo.GinkgoT().TempDir()
		logPath := filepath.Join(dir, "verify.jsonl")
		writeVerificationEvalLog(logPath)

		var stdout, stderr bytes.Buffer

		code := Run([]string{"-localization-corpus", corpusPath, "-localization-report", "-log", logPath}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		for _, want := range []string{
			"Localization report: PASS recent-implementation-localization schema=localization_unified_report.v1",
			"Outcome: samples=5",
			"Trajectory: baseline=not_available",
			"Competitive conditions:",
			"- external_agent_native status=not_available external=true",
			"Cache/freshness:",
			"Ranker:",
			"Verification: state=available available=true status=passed checks=1",
			"Audit metrics: joins=",
			"Product-success gates:",
			"- icp status=partial",
			"- client_demo_proof status=not_available",
			"Client/product: generic=true client_specific_branches=false",
			"Client classifications:",
			"- finance label=\"finance\" status=not_available samples=0 reason=classification_sample_missing",
			"- healthcare_life_sciences label=\"healthcare/life sciences\" status=not_available samples=0 reason=classification_sample_missing",
			"- public_sector label=\"public-sector\" status=not_available samples=0 reason=classification_sample_missing",
			"- enterprise_saas label=\"enterprise SaaS\" status=not_available samples=0 reason=classification_sample_missing",
			"- tooling_vendor label=\"tooling-vendor\" status=not_available samples=0 reason=classification_sample_missing",
			"- local_model_private_code label=\"local-model/private-code\" status=not_available samples=0 reason=classification_sample_missing",
			"- open_source label=\"open-source\" status=not_available samples=0 reason=classification_sample_missing",
			"Privacy: policy=aggregate_counts_relative_paths_no_bodies_or_prompts prompt_text=false file_bodies=false log_excerpts=false secrets=false",
		} {
			gomega.Expect(stdout.String()).To(gomega.ContainSubstring(want), fmt.Sprintf("expected unified localization report to contain %q, got %s", want, stdout.String()))

		}
		gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring("PROMPT_SENTINEL BODY_SENTINEL"), fmt.Sprintf("unified localization report echoed raw sentinel text: %s", stdout.String()))
		gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring("BODY_SENTINEL"), fmt.Sprintf("unified localization report echoed raw sentinel text: %s", stdout.String()))

	})
	ginkgo.It("run prints unified localization report json", func() {
		corpusPath := filepath.Join(evalCLITestRepoRoot(), "testdata", "localization", "recent-implementation-corpus.json")

		var stdout, stderr bytes.Buffer

		code := Run([]string{"-localization-corpus", corpusPath, "-localization-report", "-json"}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		for _, want := range []string{`"schema_version": "localization_unified_report.v1"`, `"competitive_conditions"`, `"baseline": "aider_repo_map"`, `"cache_freshness"`, `"ranker"`, `"verification"`, `"audit_metrics"`, `"client_product"`, `"classification_rows"`, `"product_success_gates"`, `"gate": "icp"`, `"gate": "competitive_proof"`, `"gate": "local_product_evidence_export"`, `"prompt_text_included": false`} {
			gomega.Expect(stdout.String()).To(gomega.ContainSubstring(want), fmt.Sprintf("expected unified JSON report to contain %q, got %s", want, stdout.String()))

		}
	})
	ginkgo.DescribeTable("suppresses an unknown competitive client classification",
		func(jsonOutput bool) {
			corpusPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "adversarial-localization-corpus.json")
			writeCompetitiveClientClassificationCorpus(corpusPath, []string{"customer-acme-session"})
			args := []string{"-localization-corpus", corpusPath, "-localization-report"}
			if jsonOutput {
				args = append(args, "-json")
			}
			var stdout, stderr bytes.Buffer

			code := Run(args, &stdout, &stderr)
			gomega.Expect(code).To(gomega.Equal(1), "stdout=%s stderr=%s", stdout.String(), stderr.String())
			out := stdout.String()
			for _, forbidden := range []string{"customer-acme-session", "customer_acme_session"} {
				gomega.Expect(out).NotTo(gomega.ContainSubstring(forbidden))
				gomega.Expect(stderr.String()).NotTo(gomega.ContainSubstring(forbidden))
			}
			gomega.Expect(out).To(gomega.ContainSubstring("competitive_condition_client_classification_invalid"))
			gomega.Expect(out).To(gomega.ContainSubstring("competitive_proof"))
			gomega.Expect(out).To(gomega.ContainSubstring("invalid_client_classifications"))
			gomega.Expect(out).NotTo(gomega.ContainSubstring("- competitive_proof status=pass"))
			gomega.Expect(out).NotTo(gomega.ContainSubstring(`"reason": "competitive_conditions_covered"`))
		},
		ginkgo.Entry("in text output", false),
		ginkgo.Entry("in JSON output", true),
	)
	ginkgo.It("run prints configured localization top k", func() {
		corpusPath := filepath.Join(evalCLITestRepoRoot(), "testdata", "localization", "recent-implementation-corpus.json")

		var stdout, stderr bytes.Buffer

		code := Run([]string{"-localization-corpus", corpusPath, "-localization-top-k", "5,1"}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		out := stdout.String()
		gomega.Expect(out).To(gomega.ContainSubstring("Top-k edit-path coverage: k=1"), fmt.Sprintf("expected configured top-k output, got %s", out))
		gomega.Expect(out).To(gomega.ContainSubstring(" k=5 "), fmt.Sprintf("expected configured top-k output, got %s", out))
		gomega.Expect(out).NotTo(gomega.ContainSubstring(" k=3 "), fmt.Sprintf("did not expect default k=3 when top-k configured, got %s", out))

	})
	ginkgo.It("run rejects invalid localization top k", func() {
		corpusPath := filepath.Join(evalCLITestRepoRoot(), "testdata", "localization", "recent-implementation-corpus.json")

		var stdout, stderr bytes.Buffer

		code := Run([]string{"-localization-corpus", corpusPath, "-localization-top-k", "0"}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(2), fmt.Sprintf("expected exit 2, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("invalid -localization-top-k"), fmt.Sprintf("expected invalid top-k error, got %s", stderr.String()))

	})
	ginkgo.It("run requires log path", func() {
		var stdout, stderr bytes.Buffer

		code := Run(nil, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(2), fmt.Sprintf("expected exit 2, got %d", code))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("missing required -log path"), fmt.Sprintf("expected missing log error, got %s", stderr.String()))

	})
})

func writeQwenEvalLog(path string) {

	const log = `{"type":"message","role":"user","content":"review routing"}
{"type":"message","role":"route_plan","content":"initial=review reason=review_request contract=review_findings thoroughness=thorough"}
{"type":"message","role":"budget_ledger","content":"{\"lane\":\"review\",\"used_model_calls\":2,\"total_evidence_paths\":1,\"stop_reason\":\"ready_after_retry\",\"budget_exhausted\":false}"}
{"type":"message","role":"route_ambiguity","content":"{\"selected_lane\":\"review\",\"reason\":\"review_request\",\"signals\":[\"review\"],\"ambiguous\":false}"}
{"type":"message","role":"control_rails","content":"{\"lane\":\"review\",\"contract\":\"review_findings\",\"task_family\":\"review\",\"permission_profile\":\"read_only_workspace\",\"required_evidence\":[\"candidate_path\"],\"required_tools\":[\"workspace_inspection\"],\"forbidden_tools\":[\"edit_file\"],\"readiness_gate\":[\"required_evidence_present\"],\"search_policy\":[\"rank_candidates\"],\"verification_contract\":\"not_required_for_read_only_lane\",\"stop_conditions\":[\"required_evidence_satisfied\"],\"final_critic\":[\"findings_first\"],\"failure_to_eval\":false}"}
{"type":"message","role":"assistant","content":"No bounded finding."}
`
	gomega.Expect(os.WriteFile(path, []byte(log), 0o644)).To(gomega.Succeed())

}

func writeVerificationEvalLog(path string) {

	const log = `{"type":"message","role":"user","content":"PROMPT_SENTINEL BODY_SENTINEL"}
{"type":"message","role":"verification_plan","content":"{\"commands\":[\"go test ./...\"],\"uses_full_suite\":true,\"coverage\":\"full-suite\",\"confidence\":\"high\",\"risk_level\":\"medium\"}"}
{"type":"tool_result","agent":"implement_verify","summary":"go test ./... passed","error":""}
{"type":"message","role":"verification_feedback","content":"{\"commands\":[\"go test ./...\"],\"failed_commands\":[],\"confidence\":\"high\"}"}
`
	gomega.Expect(os.WriteFile(path, []byte(log), 0o644)).To(gomega.Succeed())

}

func writeCompetitiveClientClassificationCorpus(path string, classifications []string) {

	broadReads := 0
	zeroHitSearches := 0
	corpus := eval.LocalizationCorpus{
		Name:         "adversarial-localization",
		Version:      1,
		Experimental: true,
		SourceWindow: eval.LocalizationSourceWindow{
			StartedAt:   "2026-05-12T00:00:00Z",
			EndedAt:     "2026-05-13T00:00:00Z",
			SourceRoots: []string{"logs/telemetry"},
		},
		PrivacyReview: eval.LocalizationPrivacyReview{
			Status:                   "redacted",
			ReviewedAt:               "2026-05-13T00:00:00Z",
			SensitiveContentExcluded: true,
		},
		Tasks: []eval.LocalizationCorpusTask{{
			ID:                "competitive-client-classification",
			Name:              "Competitive Client Classification",
			Summary:           "Exercises competitive-condition client classification privacy boundaries.",
			Themes:            []string{"competitive_conditions"},
			ClientProductArea: "finance",
			SourceRefs: []eval.LocalizationSourceRef{{
				Kind:     "telemetry",
				Ref:      "fixture",
				LogPath:  "logs/telemetry/fixture.jsonl",
				Digest:   "sha256:fixture",
				Evidence: []string{"sanitized fixture"},
			}},
			Expected: eval.LocalizationExpectedAnchors{
				Files:   []eval.LocalizationExpectedFile{{Path: "go.mod"}},
				Symbols: []eval.LocalizationExpectedSymbol{{Path: "go.mod", Name: "module"}},
				Tests:   []eval.LocalizationExpectedTest{{Path: "go.mod", Name: "fixture"}},
			},
			Benchmark: &eval.LocalizationBenchmarkInput{
				ContextPacket: &eval.LocalizationContextPacketEvidence{
					Files: []eval.LocalizationCandidateFile{{Path: "go.mod"}},
				},
				Trajectory: &eval.LocalizationTrajectoryEvidence{
					BroadReadCount:     &broadReads,
					ZeroHitSearchCount: &zeroHitSearches,
				},
				CompetitiveConditions: competitiveConditionsForCommandTest("go.mod", classifications),
			},
			Privacy: eval.LocalizationTaskPrivacy{
				Status:                   "redacted",
				SensitiveContentExcluded: true,
			},
			SensitiveExclusionNote: "Excluded prompt text, file bodies, transcript excerpts, secrets, and customer-identifying fields.",
			Provenance: eval.LocalizationTaskProvenance{
				SelectionReason: "sanitized fixture",
				SanitizedFields: []string{"classification"},
				SourceCoverage:  []string{"telemetry"},
			},
		}},
	}

	data, err := json.MarshalIndent(corpus, "", "  ")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal corpus: %v", err))
	gomega.Expect(os.WriteFile(path, data, 0o644)).To(gomega.Succeed())

}

func competitiveConditionsForCommandTest(expectedPath string, classifications []string) []eval.LocalizationCompetitiveConditionInput {
	conditions := make([]eval.LocalizationCompetitiveConditionInput, 0, 6)
	for _, baseline := range []string{"raw_search_read", "generated_baseline_discovery_report", "generated_current_discovery_report", "context_pack"} {
		conditions = append(conditions, eval.LocalizationCompetitiveConditionInput{
			Baseline:              baseline,
			Status:                "measured",
			CandidateFiles:        []eval.LocalizationCandidateFile{{Path: expectedPath}},
			Budget:                &eval.LocalizationCompetitiveBudgetEvidence{ToolCallBudget: 8, WallClockBudgetMS: 60000, UsedToolCalls: 3, ElapsedMS: 1200},
			CommandID:             baseline + "_fixture",
			ArtifactPath:          "testdata/localization/competitive/" + baseline + ".json",
			ClientClassifications: append([]string(nil), classifications...),
		})
	}

	for _, baseline := range []string{"aider_repo_map", "external_agent_native"} {
		conditions = append(conditions, eval.LocalizationCompetitiveConditionInput{
			Baseline:              baseline,
			Status:                "infeasible",
			CommandID:             baseline + "_probe",
			ArtifactPath:          "testdata/localization/competitive/" + baseline + ".json",
			InfeasibilityReason:   "tool_unavailable",
			ClaimImpact:           "claim_weakened_until_external_run",
			ClientClassifications: append([]string(nil), classifications...),
		})
	}

	return conditions
}
