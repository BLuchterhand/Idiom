package toolfacts

import (
	"encoding/json"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"idiom/internal/contracts/toolpolicy"
)

var _ = ginkgo.Describe("Tool-result facts", func() {
	ginkgo.It("normalizes and merges revision-bound source-window duplicates", func() {
		content := "func current() {}\n"
		revision := SourceWindowSHA256([]byte("package app\n" + content))
		window := SourceWindowSHA256([]byte(content))
		authority := SourceWindowAuthorityEvidence{PolicySubject: "alias/app.go", PolicyDecision: toolpolicy.Ask}
		actual := NormalizeSourceWindows([]SourceWindowEvidence{
			{CanonicalID: "../outside.go", Authority: authority, StartLine: 1, EndLine: 1, TotalLines: 1, RevisionSHA256: revision, WindowSHA256: window, MutationEpoch: 7},
			{CanonicalID: "`internal/app.go`", Authority: SourceWindowAuthorityEvidence{PolicySubject: "`alias/app.go`", PolicyDecision: toolpolicy.Ask}, StartLine: 10, EndLine: 20, TotalLines: 100, RevisionSHA256: revision, WindowSHA256: window, MutationEpoch: 7},
			{CanonicalID: "internal/app.go", Authority: authority, StartLine: 10, EndLine: 20, TotalLines: 100, Truncated: true, RevisionSHA256: revision, WindowSHA256: window, MutationEpoch: 7},
			{CanonicalID: "internal/app.go", Authority: authority, StartLine: 10, EndLine: 20, TotalLines: 100, RevisionSHA256: "bad", WindowSHA256: window, MutationEpoch: 7},
		})

		gomega.Expect(actual).To(gomega.HaveLen(1))
		gomega.Expect(actual[0]).To(gomega.Equal(SourceWindowEvidence{
			CanonicalID: "internal/app.go", Authority: authority, StartLine: 10, EndLine: 20, TotalLines: 100, Truncated: true,
			RevisionSHA256: revision, WindowSHA256: window, MutationEpoch: 7,
		}))
	})

	ginkgo.It("rejects non-private authority subjects and keeps strict aliases distinct", func() {
		revision := SourceWindowSHA256([]byte("body\n"))
		window := SourceWindowSHA256([]byte("body"))
		base := SourceWindowEvidence{
			CanonicalID: "physical/source.go", StartLine: 1, EndLine: 1, TotalLines: 1,
			RevisionSHA256: revision, WindowSHA256: window, MutationEpoch: 4,
		}
		absolute := base
		absolute.Authority = SourceWindowAuthorityEvidence{PolicySubject: "/private/source.go", PolicyDecision: toolpolicy.Ask}
		traversal := base
		traversal.Authority = SourceWindowAuthorityEvidence{PolicySubject: "../private/source.go", PolicyDecision: toolpolicy.Deny}
		empty := base
		empty.Authority = SourceWindowAuthorityEvidence{PolicyDecision: toolpolicy.Allow}
		invalidDecision := base
		invalidDecision.Authority = SourceWindowAuthorityEvidence{PolicySubject: "alias/source.go", PolicyDecision: toolpolicy.Decision("maybe")}
		ask := base
		ask.Authority = SourceWindowAuthorityEvidence{PolicySubject: "alias/source.go", PolicyDecision: toolpolicy.Ask}
		allow := base
		allow.Authority = SourceWindowAuthorityEvidence{PolicySubject: "physical/source.go", PolicyDecision: toolpolicy.Allow}

		normalized := NormalizeSourceWindows([]SourceWindowEvidence{absolute, traversal, empty, invalidDecision, ask, allow})
		gomega.Expect(normalized).To(gomega.Equal([]SourceWindowEvidence{ask, allow}))

		encoded, err := json.Marshal(normalized)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(encoded)).NotTo(gomega.ContainSubstring("/private"))
		gomega.Expect(string(encoded)).NotTo(gomega.ContainSubstring(".."))
		gomega.Expect(string(encoded)).To(gomega.ContainSubstring("alias/source.go"))
	})

	ginkgo.It("retains a normalized slash-relative source-window policy subject", func() {
		evidence := validSourceWindowEvidence("subdir/alias/source.go")

		gomega.Expect(NormalizeSourceWindows([]SourceWindowEvidence{evidence})).To(gomega.Equal([]SourceWindowEvidence{evidence}))
	})

	ginkgo.DescribeTable("rejects non-relative or non-normalized source-window policy subjects",
		func(policySubject string) {
			gomega.Expect(NormalizeSourceWindows([]SourceWindowEvidence{validSourceWindowEvidence(policySubject)})).To(gomega.BeEmpty())
		},
		ginkgo.Entry("empty", ""),
		ginkgo.Entry("dot", "."),
		ginkgo.Entry("parent", ".."),
		ginkgo.Entry("Unix absolute", "/private/source.go"),
		ginkgo.Entry("Windows drive absolute with backslashes", `C:\private\source.go`),
		ginkgo.Entry("Windows drive absolute with slashes", "C:/private/source.go"),
		ginkgo.Entry("backslash UNC", `\\server\share\source.go`),
		ginkgo.Entry("slash UNC", "//server/share/source.go"),
		ginkgo.Entry("Windows device path", `\\?\C:\private\source.go`),
		ginkgo.Entry("leading backslash", `\private\source.go`),
		ginkgo.Entry("leading traversal segment", "../private/source.go"),
		ginkgo.Entry("nested traversal segment", "subdir/../source.go"),
		ginkgo.Entry("dot segment", "subdir/./source.go"),
		ginkgo.Entry("empty segment", "subdir//source.go"),
		ginkgo.Entry("mixed separators", `subdir/alias\source.go`),
	)

	ginkgo.It("normalizes and merges metadata-only diagnostic-log read facts", func() {
		valid := DiagnosticLogReadEvidence{
			CanonicalID: "logs/session.log", PolicySubject: "alias/session.log", PolicyDecision: toolpolicy.Ask,
			StartLine: 21, EndLine: 100, TotalLines: 100,
		}
		truncated := valid
		truncated.Truncated = true
		absolute := valid
		absolute.CanonicalID = "/private/session.log"
		traversal := valid
		traversal.PolicySubject = "../private/session.log"
		invalidRange := valid
		invalidRange.EndLine = 101
		invalidDecision := valid
		invalidDecision.PolicyDecision = toolpolicy.Decision("maybe")

		actual := NormalizeDiagnosticLogReads([]DiagnosticLogReadEvidence{
			absolute, traversal, invalidRange, invalidDecision, valid, truncated,
		})

		gomega.Expect(actual).To(gomega.Equal([]DiagnosticLogReadEvidence{truncated}))
	})

	ginkgo.It("serializes diagnostic-log read facts without private paths bodies or source authority", func() {
		valid := DiagnosticLogReadEvidence{
			CanonicalID: "logs/session.log", PolicySubject: "logs/session.log", PolicyDecision: toolpolicy.Allow,
			StartLine: 1, EndLine: 2, TotalLines: 2,
		}
		private := valid
		private.CanonicalID = "/private/workspace/session.log"
		facts := NewDiagnosticLogReadFacts(private, valid)
		gomega.Expect(facts).NotTo(gomega.BeNil())

		encoded, err := json.Marshal(facts)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		serialized := string(encoded)
		gomega.Expect(serialized).To(gomega.ContainSubstring(`"diagnostic_log_reads"`))
		gomega.Expect(serialized).To(gomega.ContainSubstring(`"canonical_id":"logs/session.log"`))
		for _, forbidden := range []string{"/private", `"body"`, `"output"`, "sha256", "mutation_epoch"} {
			gomega.Expect(serialized).NotTo(gomega.ContainSubstring(forbidden))
		}
	})

	ginkgo.It("normalizes typed submit-plan diagnostics without repairing producer identity", func() {
		valid := SubmitPlanDiagnostic{
			Code:        "submit_plan.invalid_anchor",
			Kind:        SubmitPlanDiagnosticInvalidAnchor,
			CanonicalID: "internal/runtime/agent/plan_execute.go",
			StartLine:   40,
			EndLine:     44,
		}
		duplicate := valid
		absolute := valid
		absolute.CanonicalID = "/private/workspace/plan_execute.go"
		traversal := valid
		traversal.CanonicalID = "internal/../private.go"
		invalidRange := valid
		invalidRange.EndLine = 39
		unknownKind := valid
		unknownKind.Kind = SubmitPlanDiagnosticKind("old_text_not_found")
		invalidCode := valid
		invalidCode.Code = "submit_plan.invalid_anchor injected"
		pathlessKindWithMalformedPath := SubmitPlanDiagnostic{
			Code:        "submit_plan.invalid_edit_shape",
			Kind:        SubmitPlanDiagnosticInvalidEditShape,
			CanonicalID: "internal/../private.go",
		}
		missingSnippet := SubmitPlanDiagnostic{
			Code:         "submit_plan.missing_required_snippet",
			Kind:         SubmitPlanDiagnosticMissingRequiredSnippet,
			ObligationID: "required-snippet-1",
		}

		actual := NormalizeSubmitPlanDiagnostics([]SubmitPlanDiagnostic{
			absolute, traversal, invalidRange, unknownKind, invalidCode, pathlessKindWithMalformedPath, valid, duplicate, missingSnippet,
		})

		gomega.Expect(actual).To(gomega.Equal([]SubmitPlanDiagnostic{valid, missingSnippet}))
	})

	ginkgo.It("clones typed submit-plan diagnostics without sharing storage", func() {
		original := &ToolResultFacts{SubmitPlanDiagnostics: []SubmitPlanDiagnostic{{
			Code:        "submit_plan.no_op",
			Kind:        SubmitPlanDiagnosticNoOpEdit,
			CanonicalID: "a.go",
		}}}

		clone := original.Clone()
		clone.SubmitPlanDiagnostics[0].CanonicalID = "changed.go"

		gomega.Expect(original.SubmitPlanDiagnostics[0].CanonicalID).To(gomega.Equal("a.go"))
	})

	ginkgo.It("clones slice-backed evidence without sharing storage", func() {
		original := &ToolResultFacts{
			DiagnosticEvidencePaths: []string{"diagnostic.log"},
			VerificationEvidence: []VerificationEvidence{{
				Kind:  VerificationEvidenceHTMLParse,
				Paths: []string{"web/index.html"},
			}},
			SourceWindows: []SourceWindowEvidence{{
				CanonicalID: "a.go", Authority: SourceWindowAuthorityEvidence{PolicySubject: "a.go", PolicyDecision: toolpolicy.Allow}, StartLine: 1, EndLine: 2, TotalLines: 2,
				RevisionSHA256: SourceWindowSHA256([]byte("a\nb")), WindowSHA256: SourceWindowSHA256([]byte("a\nb")), MutationEpoch: 1,
			}},
			DiagnosticLogReads: []DiagnosticLogReadEvidence{{
				CanonicalID: "diagnostics/app.log", PolicySubject: "logs/app.log", PolicyDecision: toolpolicy.Allow,
				StartLine: 1, EndLine: 2, TotalLines: 2,
			}},
			ArtifactIDs:      []string{"artifact-1"},
			CandidatePaths:   []string{"b.go"},
			CandidateSymbols: []string{"Run"},
			RankedDiscoveryCandidates: []DiscoveryCandidateEvidence{{
				Path:         "b.go",
				LayerName:    "text_anchor_search",
				Tool:         "search_content",
				Summary:      "Found b",
				EvidenceLine: "b.go:1:func Run",
			}},
			HasDiscoveryTargets:    true,
			DiscoveryStrength:      DiscoveryStrengthSourceOwner,
			SourceOwnerFound:       true,
			LocalizationConfidence: "high",
			SearchHitCount:         12,
			SearchHighCardinality:  true,
			GitHistoryPath:         "a.go",
			GitHistoryStatus:       GitHistoryStatusCommitDescriptions,
			ShellCommand:           "go test ./...",
			ShellWorkdir:           "internal/runtime/agent",
		}

		clone := original.Clone()
		gomega.Expect(clone).NotTo(gomega.BeNil())

		clone.DiagnosticEvidencePaths[0] = "changed.log"
		clone.VerificationEvidence[0].Paths[0] = "changed.html"
		clone.SourceWindows[0].CanonicalID = "changed.go"
		clone.DiagnosticLogReads[0].CanonicalID = "changed.log"
		clone.ArtifactIDs[0] = "changed-artifact"
		clone.CandidatePaths[0] = "changed_candidate.go"
		clone.CandidateSymbols[0] = "Changed"
		clone.RankedDiscoveryCandidates[0].Path = "changed_ranked.go"

		gomega.Expect(original.DiagnosticEvidencePaths).To(gomega.Equal([]string{"diagnostic.log"}))
		gomega.Expect(original.VerificationEvidence[0].Paths).To(gomega.Equal([]string{"web/index.html"}))
		gomega.Expect(original.SourceWindows[0].CanonicalID).To(gomega.Equal("a.go"))
		gomega.Expect(original.DiagnosticLogReads[0].CanonicalID).To(gomega.Equal("diagnostics/app.log"))
		gomega.Expect(original.ArtifactIDs).To(gomega.Equal([]string{"artifact-1"}))
		gomega.Expect(original.CandidatePaths).To(gomega.Equal([]string{"b.go"}))
		gomega.Expect(original.CandidateSymbols).To(gomega.Equal([]string{"Run"}))
		gomega.Expect(original.RankedDiscoveryCandidates[0].Path).To(gomega.Equal("b.go"))
		gomega.Expect(original.SearchHitCount).To(gomega.Equal(12))
		gomega.Expect(original.SearchHighCardinality).To(gomega.BeTrue())
		gomega.Expect(clone.ShellWorkdir).To(gomega.Equal("internal/runtime/agent"))
	})
})

func validSourceWindowEvidence(policySubject string) SourceWindowEvidence {
	body := []byte("package source\n")
	return SourceWindowEvidence{
		CanonicalID: "physical/source.go",
		Authority: SourceWindowAuthorityEvidence{
			PolicySubject:  policySubject,
			PolicyDecision: toolpolicy.Allow,
		},
		StartLine:      1,
		EndLine:        1,
		TotalLines:     1,
		RevisionSHA256: SourceWindowSHA256(body),
		WindowSHA256:   SourceWindowSHA256(body),
		MutationEpoch:  1,
	}
}
