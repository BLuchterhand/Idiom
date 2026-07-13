package recovery

import (
	"encoding/json"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Durable mutation projections", func() {
	ginkgo.It("round trips canonical scope and partial commit evidence without changing JSON", func() {
		state := ImplementationState{
			CanonicalMutationScope: &CanonicalMutationScopeProjection{
				RequiredCoverageIDs: []string{"physical/a.go"},
				Rules: []CanonicalMutationScopeRuleProjection{{
					Kind: "exact_file", Source: "required_path", CanonicalID: "physical/a.go",
				}},
				Presentations: []CanonicalMutationScopePresentationProjection{{
					PresentedPath: "alias/a.go", CanonicalID: "physical/a.go", Source: "required_path",
				}},
			},
			CommitReceipts: []MutationCommitProjection{{
				Scope: &CanonicalMutationScopeProjection{
					RequiredCoverageIDs: []string{"physical/a.go"},
				},
				Effects: []MutationCommitEffectProjection{{
					CanonicalID: "physical/a.go", Kind: "replaced_file", Role: "source",
				}},
				PostimageFileIDs: []string{"physical/a.go"},
				AffectedFileIDs:  []string{"physical/a.go"},
				FailedTargets: []MutationTargetProjection{{
					CanonicalID: "physical/b.go", Role: "destination", PreimageClass: "absent",
				}},
				UnattemptedTargets: []MutationTargetProjection{{
					CanonicalID: "physical/c.go", Role: "destination", PreimageClass: "absent",
				}},
			}},
		}

		body, err := json.Marshal(state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		var decoded ImplementationState
		gomega.Expect(json.Unmarshal(body, &decoded)).To(gomega.Succeed())
		gomega.Expect(ValidateImplementationState(&decoded)).To(gomega.Succeed())
		roundTripped, err := json.Marshal(decoded)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(roundTripped).To(gomega.MatchJSON(body))
	})

	ginkgo.It("rejects absolute or escaping identities at the durable boundary", func() {
		state := ImplementationState{
			CanonicalMutationScope: &CanonicalMutationScopeProjection{
				RequiredCoverageIDs: []string{"/private/source.go"},
			},
		}
		gomega.Expect(ValidateImplementationState(&state)).To(gomega.MatchError(gomega.ContainSubstring("unsafe path")))

		state = ImplementationState{CommitReceipts: []MutationCommitProjection{{
			AffectedFileIDs: []string{"../outside.go"},
		}}}
		gomega.Expect(ValidateImplementationState(&state)).To(gomega.MatchError(gomega.ContainSubstring("unsafe path")))
	})
})
