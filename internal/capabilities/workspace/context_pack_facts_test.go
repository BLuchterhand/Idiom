package workspace

import (
	"fmt"
	"slices"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/repository/comprehension"
)

var _ = ginkgo.Describe("context pack facts", func() {
	ginkgo.It("carries producer-owned artifact identities even without candidates", func() {
		facts := contextPackToolResultFacts(comprehension.PackResult{
			PackID:       "pack-123",
			ArtifactPath: ".idiom/context/pack-123.json",
		})

		gomega.Expect(facts).NotTo(gomega.BeNil())
		gomega.Expect(facts.ArtifactIDs).To(gomega.ConsistOf("pack-123", ".idiom/context/pack-123.json"))
	})

	ginkgo.It("context pack facts candidate paths prefer packet quality evidence", func() {
		facts := contextPackToolResultFacts(comprehension.PackResult{
			Task: "chat input text box newline wrapping first line invisible",
			Quality: comprehension.PackQuality{
				LocalizationConfidence: "low",
				SourceOwnerFound:       false,
				EvidencePaths:          []string{"internal/surfaces/tui/app/rendering_test.go"},
				Reasons:                []string{"related_evidence_without_source_owner"},
			},
			CandidateFiles: []comprehension.PackFile{
				{Path: "internal/evidence/transcript/debuglog.go", Reason: "matched task terms"},
				{Path: "internal/surfaces/tui/app/plan.go", Reason: "matched task terms"},
				{Path: "internal/surfaces/tui/app/theme_style.go", Reason: "matched task terms"},
			},
			CandidateSymbols: []comprehension.PackSymbol{
				{Path: "internal/surfaces/tui/app/rendering_test.go", Kind: "test", QualifiedName: "TestInputHeightUsesTextareaWrappedLineCount"},
			},
			Tests: []comprehension.PackTest{
				{Path: "internal/surfaces/tui/app/rendering_test.go", Symbol: "TestInputHeightUsesTextareaWrappedLineCount", Command: "go test ./internal/surfaces/tui/app"},
			},
		})
		gomega.Expect(facts).NotTo(gomega.BeNil(), "expected context pack facts")
		gomega.Expect(facts.CandidatePaths).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected quality evidence path first, got %#v", facts.CandidatePaths))
		gomega.Expect(facts.CandidatePaths[0]).To(gomega.Equal("internal/surfaces/tui/app/rendering_test.go"), fmt.Sprintf("expected quality evidence path first, got %#v", facts.CandidatePaths))
		gomega.Expect(facts.CandidatePaths[0]).NotTo(gomega.Equal("internal/evidence/transcript/debuglog.go"), fmt.Sprintf("candidate files must not saturate recovery ahead of stronger packet evidence: %#v", facts.CandidatePaths))

	})
	ginkgo.It("context pack facts candidate symbols prefer top level quality evidence", func() {
		facts := contextPackToolResultFacts(comprehension.PackResult{
			Task: "chat input text box newline wrapping first line invisible",
			Quality: comprehension.PackQuality{
				LocalizationConfidence: "low",
				SourceOwnerFound:       false,
				EvidencePaths:          []string{"internal/surfaces/tui/app/rendering_test.go"},
				Reasons:                []string{"related_evidence_without_source_owner"},
			},
			CandidateFiles: []comprehension.PackFile{
				{Path: "internal/evidence/transcript/debuglog.go", Role: "editable", Reason: "matched task terms", Handles: []string{"symbol://internal/evidence/transcript/debuglog.go#DebugLogger.LogContext"}},
				{Path: "internal/surfaces/tui/app/mocha_style.go", Role: "editable", Reason: "matched task terms", Handles: []string{"symbol://internal/surfaces/tui/app/mocha_style.go#mochaText"}},
			},
			CandidateSymbols: []comprehension.PackSymbol{
				{Path: "internal/surfaces/tui/app/rendering_test.go", Kind: "test", QualifiedName: "TestInputHeightUsesTextareaWrappedLineCount", Reason: "matched task terms"},
				{Path: "internal/runtime/agent/runner_core.go", Kind: "func", QualifiedName: "textOnlyChatOptions", Reason: "matched task terms"},
			},
			Tests: []comprehension.PackTest{
				{Path: "internal/surfaces/tui/app/rendering_test.go", Symbol: "TestInputHeightUsesTextareaWrappedLineCount", Command: "go test ./internal/surfaces/tui/app"},
			},
		})
		gomega.Expect(facts).NotTo(gomega.BeNil(), "expected context pack facts")
		gomega.Expect(facts.CandidateSymbols).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected top-level quality evidence symbol first, got %#v", facts.CandidateSymbols))
		gomega.Expect(facts.CandidateSymbols[0]).To(gomega.Equal("internal/surfaces/tui/app/rendering_test.go#TestInputHeightUsesTextareaWrappedLineCount"), fmt.Sprintf("expected top-level quality evidence symbol first, got %#v", facts.CandidateSymbols))
		gomega.Expect(facts.CandidateSymbols[0]).NotTo(gomega.Equal("internal/evidence/transcript/debuglog.go#DebugLogger.LogContext"), fmt.Sprintf("candidate-file handles must not outrank top-level quality evidence: %#v", facts.CandidateSymbols))

	})
	ginkgo.It("context query facts carry candidate paths and symbols", func() {
		facts := contextQueryToolResultFacts(comprehension.QueryResult{
			Nodes: []comprehension.NodeSummary{
				{Path: "internal/runtime/agent/runner_core.go", QualifiedName: "Runner.Run"},
				{Path: "internal/runtime/agent/runner_core.go", QualifiedName: "Runner.Run"},
				{Path: "README.md"},
			},
		})
		gomega.Expect(facts).NotTo(gomega.BeNil(), "expected context query facts")

		if got, want := facts.CandidatePaths, []string{"internal/runtime/agent/runner_core.go", "README.md"}; !slices.Equal(got, want) {
			ginkgo.Fail(fmt.Sprintf("candidate paths = %#v, want %#v", got, want))
		}
		if got, want := facts.CandidateSymbols, []string{"internal/runtime/agent/runner_core.go#Runner.Run"}; !slices.Equal(got, want) {
			ginkgo.Fail(fmt.Sprintf("candidate symbols = %#v, want %#v", got, want))
		}
		gomega.Expect(facts.HasDiscoveryTargets).To(gomega.BeTrue(), fmt.Sprintf("unexpected context query discovery facts: %#v", facts))
		gomega.Expect(facts.DiscoveryStrength).To(gomega.Equal(toolfacts.DiscoveryStrengthCandidateOnly), fmt.Sprintf("unexpected context query discovery facts: %#v", facts))
		gomega.Expect(facts.SourceOwnerFound).To(gomega.BeFalse(), fmt.Sprintf("unexpected context query discovery facts: %#v", facts))

	})
})
