package transcript

import (
	"encoding/json"

	"idiom/internal/contracts/toolfacts"
	"idiom/internal/contracts/toolpolicy"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Tool result fact event projection", func() {
	ginkgo.It("emits only known dispositions and canonical relative obligation paths", func() {
		fields := PrivacySafeToolResultFactFields(&toolfacts.ToolResultFacts{
			RejectionDisposition:   toolfacts.ToolRejectionPolicyDenied,
			RejectedWritePaths:     []string{"internal/feature/target.go", "./internal/feature/target.go", "/tmp/secret.go", "../secret.go"},
			DeferredPlanPaths:      []string{"internal/feature/plan.go", "plans/../internal/feature/plan.go"},
			PendingSourceBodyPaths: []string{"internal/feature/source.go"},
			PendingHistoryPaths:    []string{"internal/feature/history.go"},
			ShellCommand:           "TOKEN=secret sed -i bad internal/feature/target.go",
		})

		gomega.Expect(fields).To(gomega.Equal(map[string]any{
			"rejection_disposition":     "policy_denied",
			"rejected_write_paths":      []string{"internal/feature/target.go"},
			"deferred_plan_paths":       []string{"internal/feature/plan.go"},
			"pending_source_body_paths": []string{"internal/feature/source.go"},
			"pending_history_paths":     []string{"internal/feature/history.go"},
		}))
		gomega.Expect(fields).NotTo(gomega.HaveKey("shell_command"))
	})

	ginkgo.It("drops unknown dispositions and unsafe paths", func() {
		fields := PrivacySafeToolResultFactFields(&toolfacts.ToolResultFacts{
			RejectionDisposition: toolfacts.ToolRejectionDisposition("future_untrusted_value"),
			RejectedWritePaths:   []string{"/private/secret.go", "../../secret.go"},
		})

		gomega.Expect(fields).To(gomega.BeNil())
	})

	ginkgo.It("emits privacy-safe source-window and diagnostic-log metadata without bodies", func() {
		windowBody := []byte("package feature")
		fields := PrivacySafeToolResultFactFields(&toolfacts.ToolResultFacts{
			SourceWindows: []toolfacts.SourceWindowEvidence{{
				CanonicalID: "internal/feature/target.go",
				Authority: toolfacts.SourceWindowAuthorityEvidence{
					PolicySubject:  "internal/feature/target.go",
					PolicyDecision: toolpolicy.Allow,
				},
				StartLine: 1, EndLine: 1, TotalLines: 1,
				RevisionSHA256: toolfacts.SourceWindowSHA256(windowBody),
				WindowSHA256:   toolfacts.SourceWindowSHA256(windowBody),
				MutationEpoch:  3,
			}},
			DiagnosticLogReads: []toolfacts.DiagnosticLogReadEvidence{{
				CanonicalID: "logs/session.jsonl", PolicySubject: "logs/session.jsonl", PolicyDecision: toolpolicy.Allow,
				StartLine: 10, EndLine: 20, TotalLines: 40, Truncated: true,
			}},
		})

		gomega.Expect(fields).To(gomega.HaveKey("source_windows"))
		gomega.Expect(fields).To(gomega.HaveKey("diagnostic_log_reads"))
		encoded, err := json.Marshal(fields)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(encoded)).NotTo(gomega.ContainSubstring("package feature"))
		gomega.Expect(string(encoded)).NotTo(gomega.ContainSubstring("/Users/"))
	})
})
