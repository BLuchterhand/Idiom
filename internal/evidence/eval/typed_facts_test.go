package eval

import (
	"encoding/json"
	"fmt"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("matches privacy-safe typed lifecycle facts and exact tool execution counts", func() {
	const (
		revisionDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		windowDigest   = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	one := 1
	minEpoch := uint64(3)
	attempts := 1

	lifecycle, err := json.Marshal(map[string]any{
		"phase": "implementation.verify_ready",
		"commit_receipts": []any{map[string]any{
			"effects": []any{
				map[string]any{"canonical_id": "pkg/a.go", "kind": "replaced_file", "role": "file"},
			},
			"failed_targets": []any{
				map[string]any{"canonical_id": "pkg/b.go", "role": "file", "preimage_class": "required"},
			},
			"unattempted_targets": []any{
				map[string]any{"canonical_id": "pkg/c.go", "role": "file", "preimage_class": "absent"},
			},
		}},
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	records := []Record{
		{Type: "tool_result", Data: map[string]any{
			"tool":                      "apply_patch",
			"rejection_disposition":     "deferred_by_prerequisite",
			"rejected_write_paths":      []any{"pkg/b.go"},
			"pending_source_body_paths": []any{"pkg/b.go"},
			"pending_history_paths":     []any{"pkg/b.go"},
			"error":                     "typed rejection",
		}},
		{Type: "tool_result", Data: map[string]any{
			"tool": "read_file",
			"source_windows": []any{map[string]any{
				"canonical_id":    "pkg/b.go",
				"start_line":      10,
				"end_line":        20,
				"total_lines":     40,
				"revision_sha256": revisionDigest,
				"window_sha256":   windowDigest,
				"mutation_epoch":  4,
			}},
		}},
		{Type: "message", Data: map[string]any{
			"role":    "implementation_lifecycle_state",
			"content": string(lifecycle),
		}},
		{Type: "message", Data: map[string]any{
			"role":    "completion_gate_recovery_state",
			"content": `{"completion_recovery":{"active_violation":{"class":"mutation","attempts_since_progress":1}}}`,
		}},
		{Type: "tool_result", Data: map[string]any{
			"tool":    "apply_patch",
			"summary": "Committed pkg/a.go",
			"error":   "",
		}},
	}

	report := Evaluate("typed-facts.jsonl", records, Scenario{
		Name: "typed-facts",
		RequireTypedFacts: &TypedFactsExpectation{
			ToolRejections: []ToolRejectionFactsExpectation{{
				Tool:                "apply_patch",
				Disposition:         "deferred_by_prerequisite",
				PendingWritePaths:   []string{"pkg/b.go"},
				PendingSourcePaths:  []string{"pkg/b.go"},
				PendingHistoryPaths: []string{"pkg/b.go"},
				ExactCount:          &one,
			}},
			SourceWindows: []SourceWindowFactsExpectation{{
				Tool:                     "read_file",
				CanonicalID:              "pkg/b.go",
				StartLine:                10,
				EndLine:                  20,
				MinMutationEpoch:         &minEpoch,
				RevisionSHA256:           revisionDigest,
				WindowSHA256:             windowDigest,
				RequireDurableBodyAbsent: true,
				ExactCount:               &one,
			}},
			MutationCommits: []MutationCommitFactsExpectation{{
				Role: "implementation_lifecycle_state",
				Effects: []MutationCommitEffectExpectation{{
					CanonicalID: "pkg/a.go", Kind: "replaced_file", Role: "file",
				}},
				FailedTargets: []MutationTargetFactsExpectation{{
					CanonicalID: "pkg/b.go", Role: "file", PreimageClass: "required",
				}},
				UnattemptedTargets: []MutationTargetFactsExpectation{{
					CanonicalID: "pkg/c.go", Role: "file", PreimageClass: "absent",
				}},
				ExactCount: &one,
			}},
			CompletionRecoveries: []CompletionRecoveryFactsExpectation{{
				Role:                  "completion_gate_recovery_state",
				Class:                 "mutation",
				AttemptsSinceProgress: &attempts,
				ExactCount:            &one,
			}},
			ToolExecutions: []ToolExecutionCountExpectation{{
				Tool: "apply_patch", ExactCount: intPointer(2),
			}},
		},
	})

	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("typed facts should pass: %#v", report.Findings))
})

var _ = ginkgo.It("rejects source-window body leakage and hidden tool replay", func() {
	one := 1
	epoch := uint64(2)
	records := []Record{
		{Type: "tool_result", Data: map[string]any{
			"tool": "read_file",
			"source_windows": []any{map[string]any{
				"canonical_id":    "pkg/a.go",
				"start_line":      1,
				"end_line":        3,
				"mutation_epoch":  2,
				"revision_sha256": "sha256:" + strings.Repeat("a", 64),
				"window_sha256":   "sha256:" + strings.Repeat("b", 64),
				"body":            "secret source must not be durable",
			}},
		}},
		{Type: "tool_result", Data: map[string]any{"tool": "apply_patch", "error": ""}},
		{Type: "tool_result", Data: map[string]any{"tool": "apply_patch", "error": ""}},
	}

	report := Evaluate("typed-facts-leak.jsonl", records, Scenario{
		Name: "typed-facts-leak",
		RequireTypedFacts: &TypedFactsExpectation{
			SourceWindows: []SourceWindowFactsExpectation{{
				Tool:                     "read_file",
				CanonicalID:              "pkg/a.go",
				StartLine:                1,
				EndLine:                  3,
				MutationEpoch:            &epoch,
				RequireDigests:           true,
				RequireDurableBodyAbsent: true,
				ExactCount:               &one,
			}},
			ToolExecutions: []ToolExecutionCountExpectation{{
				Tool: "apply_patch", Outcome: "success", ExactCount: &one,
			}},
		},
	})

	gomega.Expect(report.Passed).To(gomega.BeFalse())
	joined := strings.Join(report.Findings, "\n")
	gomega.Expect(joined).To(gomega.ContainSubstring("required typed source window 1 count = 0, want exactly 1"))
	gomega.Expect(joined).To(gomega.ContainSubstring("required typed tool execution 1 count = 2, want exactly 1"))
})

var _ = ginkgo.It("matches exact privacy-safe diagnostic-log reads without source-window authority", func() {
	one := 1
	truncated := true
	notTruncated := false
	records := []Record{
		{Type: "tool_result", Data: map[string]any{
			"tool": "read_file",
			"diagnostic_log_reads": []any{
				map[string]any{
					"canonical_id": "logs/physical/session.jsonl", "policy_subject": "logs/alias/session.jsonl", "policy_decision": "ask",
					"start_line": 10, "end_line": 20, "total_lines": 40, "truncated": true,
				},
				map[string]any{
					"canonical_id": "logs/empty.log", "policy_subject": "logs/empty.log", "policy_decision": "",
					"start_line": 0, "end_line": 0, "total_lines": 0,
				},
			},
		}},
	}

	report := Evaluate("typed-diagnostic-log.jsonl", records, Scenario{
		Name: "typed-diagnostic-log",
		RequireTypedFacts: &TypedFactsExpectation{DiagnosticLogReads: []DiagnosticLogReadFactsExpectation{
			{
				Tool: "read_file", CanonicalID: "logs/physical/session.jsonl", PolicySubject: "logs/alias/session.jsonl", PolicyDecision: "ask",
				StartLine: 10, EndLine: 20, TotalLines: 40, Truncated: &truncated,
				RequirePrivateFieldsAbsent: true, RequireSourceWindowAbsent: true, ExactCount: &one,
			},
			{
				Tool: "read_file", CanonicalID: "logs/empty.log", PolicySubject: "logs/empty.log", PolicyDecision: "",
				StartLine: 0, EndLine: 0, TotalLines: 0, Truncated: &notTruncated,
				RequirePrivateFieldsAbsent: true, RequireSourceWindowAbsent: true, ExactCount: &one,
			},
		}},
	})

	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("diagnostic facts should pass: %#v", report.Findings))
})

var _ = ginkgo.DescribeTable("rejects malformed, private, or source-eligible diagnostic-log facts",
	func(mutate func(map[string]any)) {
		one := 1
		notTruncated := false
		read := map[string]any{
			"canonical_id": "logs/session.jsonl", "policy_subject": "logs/session.jsonl", "policy_decision": "",
			"start_line": 1, "end_line": 5, "total_lines": 10,
		}
		record := map[string]any{
			"tool":                 "read_file",
			"diagnostic_log_reads": []any{read},
		}
		mutate(record)

		report := Evaluate("typed-diagnostic-log-invalid.jsonl", []Record{{Type: "tool_result", Data: record}}, Scenario{
			Name: "typed-diagnostic-log-invalid",
			RequireTypedFacts: &TypedFactsExpectation{DiagnosticLogReads: []DiagnosticLogReadFactsExpectation{{
				Tool: "read_file", CanonicalID: "logs/session.jsonl", PolicySubject: "logs/session.jsonl", PolicyDecision: "",
				StartLine: 1, EndLine: 5, TotalLines: 10, Truncated: &notTruncated,
				RequirePrivateFieldsAbsent: true, RequireSourceWindowAbsent: true, ExactCount: &one,
			}}},
		})

		gomega.Expect(report.Passed).To(gomega.BeFalse())
		gomega.Expect(strings.Join(report.Findings, "\n")).To(gomega.ContainSubstring("required typed diagnostic log read 1 count = 0, want exactly 1"))
	},
	ginkgo.Entry("missing policy decision", func(record map[string]any) {
		delete(record["diagnostic_log_reads"].([]any)[0].(map[string]any), "policy_decision")
	}),
	ginkgo.Entry("wrong tool", func(record map[string]any) {
		record["tool"] = "search_content"
	}),
	ginkgo.Entry("rejected read", func(record map[string]any) {
		record["runner_rejected"] = true
		record["error"] = "rejected"
	}),
	ginkgo.Entry("failed read", func(record map[string]any) {
		record["error"] = "read failed"
	}),
	ginkgo.Entry("wrong canonical identity", func(record map[string]any) {
		record["diagnostic_log_reads"].([]any)[0].(map[string]any)["canonical_id"] = "logs/other.jsonl"
	}),
	ginkgo.Entry("wrong policy subject", func(record map[string]any) {
		record["diagnostic_log_reads"].([]any)[0].(map[string]any)["policy_subject"] = "logs/alias.jsonl"
	}),
	ginkgo.Entry("wrong range", func(record map[string]any) {
		record["diagnostic_log_reads"].([]any)[0].(map[string]any)["end_line"] = 6
	}),
	ginkgo.Entry("wrong total", func(record map[string]any) {
		record["diagnostic_log_reads"].([]any)[0].(map[string]any)["total_lines"] = 11
	}),
	ginkgo.Entry("wrong truncation", func(record map[string]any) {
		record["diagnostic_log_reads"].([]any)[0].(map[string]any)["truncated"] = true
	}),
	ginkgo.Entry("durable body leakage", func(record map[string]any) {
		record["diagnostic_log_reads"].([]any)[0].(map[string]any)["body"] = "private diagnostic content"
	}),
	ginkgo.Entry("durable output leakage", func(record map[string]any) {
		record["diagnostic_log_reads"].([]any)[0].(map[string]any)["output"] = "private diagnostic content"
	}),
	ginkgo.Entry("absolute path leakage", func(record map[string]any) {
		record["diagnostic_log_reads"].([]any)[0].(map[string]any)["absolute_path"] = "/private/logs/session.jsonl"
	}),
	ginkgo.Entry("source revision authority", func(record map[string]any) {
		record["diagnostic_log_reads"].([]any)[0].(map[string]any)["revision_sha256"] = "sha256:" + strings.Repeat("a", 64)
	}),
	ginkgo.Entry("source mutation epoch authority", func(record map[string]any) {
		record["diagnostic_log_reads"].([]any)[0].(map[string]any)["mutation_epoch"] = 1
	}),
	ginkgo.Entry("matching source-window authority", func(record map[string]any) {
		record["source_windows"] = []any{map[string]any{
			"canonical_id": "logs/session.jsonl",
			"authority":    map[string]any{"policy_subject": "logs/session.jsonl"},
		}}
	}),
	ginkgo.Entry("unrelated source-window authority", func(record map[string]any) {
		record["source_windows"] = []any{map[string]any{
			"canonical_id": "internal/unrelated.go",
			"authority":    map[string]any{"policy_subject": "internal/unrelated.go"},
		}}
	}),
)

var _ = ginkgo.It("does not infer typed rejection facts from prose", func() {
	records := []Record{{Type: "tool_result", Data: map[string]any{
		"tool":    "edit_file",
		"summary": "deferred_by_prerequisite for pkg/a.go",
		"error":   "pending source and history for pkg/a.go",
	}}}

	report := Evaluate("typed-prose.jsonl", records, Scenario{
		Name: "typed-prose",
		RequireTypedFacts: &TypedFactsExpectation{ToolRejections: []ToolRejectionFactsExpectation{{
			Tool: "edit_file", Disposition: "deferred_by_prerequisite", PendingWritePaths: []string{"pkg/a.go"},
		}}},
	})

	gomega.Expect(report.Passed).To(gomega.BeFalse())
	gomega.Expect(strings.Join(report.Findings, "\n")).To(gomega.ContainSubstring("required typed tool rejection 1 was not observed"))
})

var _ = ginkgo.It("deduplicates mirrored typed tool results by runner step", func() {
	one := 1
	rejection := map[string]any{
		"step":                      7,
		"tool":                      "edit_file",
		"rejection_disposition":     "deferred_by_prerequisite",
		"rejected_write_paths":      []any{"pkg/a.go"},
		"pending_source_body_paths": []any{"pkg/a.go"},
	}
	records := []Record{
		{Type: "tool_result", Data: rejection},
		{Type: "tool_result", Data: map[string]any{
			"step":                      7,
			"tool":                      "edit_file",
			"rejection_disposition":     "deferred_by_prerequisite",
			"rejected_write_paths":      []any{"pkg/a.go"},
			"pending_source_body_paths": []any{"pkg/a.go"},
			"schema_version":            "idiom.headless.event.v1",
		}},
	}
	report := Evaluate("mirrored-typed-results.jsonl", records, Scenario{
		Name: "mirrored-typed-results",
		RequireTypedFacts: &TypedFactsExpectation{
			ToolRejections: []ToolRejectionFactsExpectation{{
				Tool: "edit_file", Disposition: "deferred_by_prerequisite",
				PendingWritePaths: []string{"pkg/a.go"}, PendingSourcePaths: []string{"pkg/a.go"}, ExactCount: &one,
			}},
			ToolExecutions: []ToolExecutionCountExpectation{{Tool: "edit_file", ExactCount: &one}},
		},
	})
	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("mirrored tool results should count once: %#v", report.Findings))
})

var _ = ginkgo.It("validates and round trips typed fact expectations", func() {
	zero := 0
	epoch := uint64(1)
	notTruncated := false
	scenario := Scenario{
		Name: "typed-round-trip",
		RequireTypedFacts: &TypedFactsExpectation{
			SourceWindows: []SourceWindowFactsExpectation{{
				CanonicalID: "pkg/a.go", StartLine: 1, EndLine: 2, MutationEpoch: &epoch,
				RequireDigests: true, RequireDurableBodyAbsent: true,
			}},
			DiagnosticLogReads: []DiagnosticLogReadFactsExpectation{{
				Tool: "read_file", CanonicalID: "logs/session.jsonl", PolicySubject: "logs/session.jsonl", PolicyDecision: "",
				StartLine: 0, EndLine: 0, TotalLines: 0, Truncated: &notTruncated,
				RequirePrivateFieldsAbsent: true, RequireSourceWindowAbsent: true,
			}},
			ToolExecutions: []ToolExecutionCountExpectation{{Tool: "apply_patch", ExactCount: &zero}},
		},
	}

	data, err := json.Marshal(scenario)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	var decoded Scenario
	gomega.Expect(json.Unmarshal(data, &decoded)).To(gomega.Succeed())
	gomega.Expect(decoded).To(gomega.Equal(scenario))

	suite := SWESuite{Name: "typed-lint", Tasks: []SWETask{{
		ID: "typed", Family: "general", Prompt: "Inspect the workspace.", Scenario: scenario,
	}}}
	gomega.Expect(LintSWESuite(suite)).To(gomega.BeEmpty())
	gomega.Expect(taskScenarioAssertions(suite.Tasks[0])).To(gomega.ContainElement("typed_facts"))

	invalid := scenario
	invalid.RequireTypedFacts = &TypedFactsExpectation{SourceWindows: []SourceWindowFactsExpectation{{
		CanonicalID: "../outside.go", StartLine: 3, EndLine: 2,
	}}}
	findings := strings.Join(LintSWESuite(SWESuite{Name: "invalid", Tasks: []SWETask{{
		ID: "invalid", Family: "general", Prompt: "Inspect.", Scenario: invalid,
	}}}), "\n")
	gomega.Expect(findings).To(gomega.ContainSubstring("canonical workspace-relative path"))
	gomega.Expect(findings).To(gomega.ContainSubstring("positive, non-reversed line range"))
	gomega.Expect(findings).To(gomega.ContainSubstring("exactly one of mutation_epoch or min_mutation_epoch"))
	gomega.Expect(findings).To(gomega.ContainSubstring("explicitly require durable body absence"))

	truncated := true
	invalid.RequireTypedFacts = &TypedFactsExpectation{DiagnosticLogReads: []DiagnosticLogReadFactsExpectation{{
		Tool: "", CanonicalID: "../outside.log", PolicySubject: "/private/outside.log", PolicyDecision: "future",
		StartLine: 0, EndLine: 0, TotalLines: 0, Truncated: &truncated,
	}}}
	findings = strings.Join(LintSWESuite(SWESuite{Name: "invalid-diagnostic", Tasks: []SWETask{{
		ID: "invalid-diagnostic", Family: "general", Prompt: "Inspect.", Scenario: invalid,
	}}}), "\n")
	gomega.Expect(findings).To(gomega.ContainSubstring("missing tool"))
	gomega.Expect(findings).To(gomega.ContainSubstring("canonical workspace-relative path"))
	gomega.Expect(findings).To(gomega.ContainSubstring("invalid policy_decision"))
	gomega.Expect(findings).To(gomega.ContainSubstring("cannot mark an empty log truncated"))
	gomega.Expect(findings).To(gomega.ContainSubstring("explicitly require private-field absence"))
	gomega.Expect(findings).To(gomega.ContainSubstring("explicitly require matching source-window absence"))
})

func intPointer(value int) *int {
	return &value
}
