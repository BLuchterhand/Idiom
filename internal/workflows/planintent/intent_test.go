package planintent

import (
	"errors"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Plan intent validation", func() {
	ginkgo.DescribeTable("rejects invalid scope and intent shapes",
		func(mutate func(*Artifact), want string) {
			artifact := validReplaceArtifact()
			mutate(&artifact)
			expectValidationError(artifact, want)
		},
		ginkgo.Entry("required scope mismatch", func(artifact *Artifact) {
			artifact.Scope.RequiredEditPaths = []string{"internal/b.go"}
		}, CodeRequiredScopeMismatch),
		ginkgo.Entry("candidate outside contract", func(artifact *Artifact) {
			artifact.Scope.CandidateEditPaths = []string{"internal/other.go"}
		}, CodeCandidateScopeRejected),
		ginkgo.Entry("intent outside contract", func(artifact *Artifact) {
			artifact.Intents[0].Path = "internal/other.go"
		}, CodeIntentScopeRejected),
		ginkgo.Entry("mixed intent and blocker", func(artifact *Artifact) {
			artifact.Blockers = []Blocker{{Code: CodeInvalidBlocker, Message: "source evidence is missing", Paths: []string{"internal/a.go"}}}
		}, CodeInvalidBlocker),
		ginkgo.Entry("malformed operation", func(artifact *Artifact) {
			artifact.Intents[0].OldText = ""
		}, CodeInvalidIntentShape),
		ginkgo.Entry("malformed evidence reference", func(artifact *Artifact) {
			artifact.Scope.EvidenceRefs = []string{"source:read_file\nraw body"}
		}, CodeInvalidEvidenceRef),
	)

	ginkgo.It("allows a typed blocker without an executable intent", func() {
		artifact := Artifact{
			SchemaVersion: SchemaVersion,
			Source:        SourceArtifactDistiller,
			Scope:         Scope{RequiredEditPaths: []string{"internal/a.go"}},
			Blockers:      []Blocker{{Code: "needs_source_evidence", Message: "planner did not provide a current source body read", Paths: []string{"internal/a.go"}}},
		}
		gomega.Expect(Validate(artifact, Contract{RequiredEditPaths: []string{"internal/a.go"}})).To(gomega.Succeed())
	})
})

func expectValidationError(artifact Artifact, want string) {
	err := Validate(artifact, Contract{RequiredEditPaths: []string{"internal/a.go"}})
	var validationErr *ValidationError
	gomega.Expect(errors.As(err, &validationErr)).To(gomega.BeTrue())
	gomega.Expect(validationErr.Code).To(gomega.Equal(want))
}

func validReplaceArtifact() Artifact {
	return Artifact{
		SchemaVersion: SchemaVersion,
		Source:        SourcePlanner,
		TaskSummary:   "replace label",
		Scope:         Scope{RequiredEditPaths: []string{"internal/a.go"}},
		Intents: []Intent{{
			Path: "internal/a.go", Operation: OperationReplaceExactText,
			OldText: "return old", NewText: "return next", Purpose: "replace old label",
		}},
	}
}
