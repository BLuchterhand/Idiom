package productevidence

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	recoverycontract "idiom/internal/contracts/recovery"
)

var _ = ginkgo.Describe("recovery evidence", func() {
	ginkgo.It("add malformed recovery artifact", func() {
		s := &productEvidenceRecovery{
			FailureClasses: map[string]int{},
			NextActions:    map[string]int{},
		}

		s.addMalformedRecoveryArtifact("decode_error", ".idiom/recovery/abc/failure.json")
		gomega.Expect(s.MalformedArtifactCount).To(gomega.Equal(1), fmt.Sprintf("MalformedArtifactCount: got %d, want 1", s.MalformedArtifactCount))
		gomega.Expect(s.BlockedPacketCount).To(gomega.Equal(1), fmt.Sprintf("BlockedPacketCount: got %d, want 1", s.BlockedPacketCount))
		gomega.Expect(s.Blockers).To(gomega.HaveLen(1), fmt.Sprintf("expected 1 blocker, got %#v", s.Blockers))

		got := s.Blockers[0]
		gomega.Expect(got).To(gomega.ContainSubstring("decode_error"), fmt.Sprintf("blocker missing code: %q", got))
		gomega.Expect(got).To(gomega.ContainSubstring("path_id:"), fmt.Sprintf("blocker missing path evidence (expected path_id: prefix): %q", got))

	})
	ginkgo.It("add malformed recovery artifact multiple accumulate", func() {
		s := &productEvidenceRecovery{
			FailureClasses: map[string]int{},
			NextActions:    map[string]int{},
		}
		s.addMalformedRecoveryArtifact("err_a", "path/a.json")
		s.addMalformedRecoveryArtifact("err_b", "path/b.json")
		gomega.Expect(s.MalformedArtifactCount).To(gomega.Equal(2), fmt.Sprintf("MalformedArtifactCount: got %d, want 2", s.MalformedArtifactCount))
		gomega.Expect(s.BlockedPacketCount).To(gomega.Equal(2), fmt.Sprintf("BlockedPacketCount: got %d, want 2", s.BlockedPacketCount))
		gomega.Expect(s.Blockers).To(gomega.HaveLen(2), fmt.Sprintf("expected 2 blockers, got %d", len(s.Blockers)))

	})
	ginkgo.Describe("recovery evidence artifact hash part", func() {
		ginkgo.It("returns empty for an empty relative path", func() {
			root := ginkgo.GinkgoT().TempDir()
			got := recoveryEvidenceArtifactHashPart(root, "")
			gomega.Expect(got).To(gomega.Equal(""), fmt.Sprintf("expected empty for empty path, got %q", got))

		})

		ginkgo.It("returns empty for a whitespace relative path", func() {
			root := ginkgo.GinkgoT().TempDir()
			got := recoveryEvidenceArtifactHashPart(root, "   ")
			gomega.Expect(got).To(gomega.Equal(""), fmt.Sprintf("expected empty for whitespace path, got %q", got))

		})

		ginkgo.It("marks a missing artifact", func() {
			root := ginkgo.GinkgoT().TempDir()
			got := recoveryEvidenceArtifactHashPart(root, ".idiom/recovery/nonexistent.json")
			gomega.Expect(got).To(gomega.ContainSubstring(":missing"), fmt.Sprintf("expected :missing for nonexistent file, got %q", got))

		})

		ginkgo.It("hashes a present artifact deterministically", func() {
			root := ginkgo.GinkgoT().TempDir()
			relPath := ".idiom/recovery/test-failure/failure.json"
			fullPath := filepath.Join(root, filepath.FromSlash(relPath))
			gomega.Expect(os.MkdirAll(filepath.Dir(fullPath), 0o755)).To(gomega.Succeed())
			gomega.Expect(os.WriteFile(fullPath, []byte("test content"), 0o644)).To(gomega.Succeed())

			got := recoveryEvidenceArtifactHashPart(root, relPath)
			gomega.Expect(strings.HasPrefix(got, relPath+":")).To(gomega.BeTrue(), fmt.Sprintf("expected prefix %q, got %q", relPath+":", got))
			gomega.Expect(got).NotTo(gomega.ContainSubstring(":missing"), fmt.Sprintf("should not be missing: %q", got))

			// Same content should produce same hash
			got2 := recoveryEvidenceArtifactHashPart(root, relPath)
			gomega.Expect(got2).To(gomega.Equal(got), fmt.Sprintf("same path should produce same hash: %q vs %q", got, got2))

		})
	})
	ginkgo.It("recovery evidence artifact id", func() {
		tests := []struct {
			kind    string
			relPath string
		}{
			{"failure_record", ".idiom/recovery/abc/failure.json"},
			{"decomposition_packet", ".idiom/recovery/abc/decomposition_packet.json"},
			{"reconciliation", ".idiom/recovery/abc/reconciliation.json"},
		}
		for _, tt := range tests {
			got := recoveryEvidenceArtifactID(tt.kind, tt.relPath)
			gomega.Expect(strings.HasPrefix(got, tt.kind+":")).To(gomega.BeTrue(), fmt.Sprintf("expected ID to start with %q, got %q", tt.kind+":", got))
			gomega.Expect(len(got) <= len(tt.kind)+1).To(gomega.BeFalse(), fmt.Sprintf("expected stable ID component after prefix, got %q", got))

		}
	})
})
var _ = ginkgo.Describe("add next action", func() {
	ginkgo.It("known action", func() {
		s := &productEvidenceRecovery{
			FailureClasses: map[string]int{},
			NextActions:    map[string]int{},
		}
		s.addNextAction("review_decomposition")
		gomega.Expect(s.NextActions["review_decomposition"]).To(gomega.Equal(1), fmt.Sprintf("expected review_decomposition count 1, got %#v", s.NextActions))

	})
	ginkgo.It("empty action becomes unknown", func() {
		s := &productEvidenceRecovery{
			FailureClasses: map[string]int{},
			NextActions:    map[string]int{},
		}
		s.addNextAction("")
		gomega.Expect(s.NextActions["unknown"]).To(gomega.Equal(1), fmt.Sprintf("empty action should map to unknown, got %#v", s.NextActions))

	})
	ginkgo.It("accumulates counts", func() {
		s := &productEvidenceRecovery{
			FailureClasses: map[string]int{},
			NextActions:    map[string]int{},
		}
		s.addNextAction("review_decomposition")
		s.addNextAction("review_decomposition")
		s.addNextAction("answer_blocker")
		gomega.Expect(s.NextActions["review_decomposition"]).To(gomega.Equal(2), fmt.Sprintf("expected review_decomposition count 2, got %#v", s.NextActions))
		gomega.Expect(s.NextActions["answer_blocker"]).To(gomega.Equal(1), fmt.Sprintf("expected answer_blocker count 1, got %#v", s.NextActions))

	})
})
var _ = ginkgo.Describe("finish recovery evidence status", func() {
	ginkgo.It("not_available when no failure records", func() {
		s := &productEvidenceRecovery{
			FailureClasses: map[string]int{},
			NextActions:    map[string]int{},
		}
		s.finishRecoveryEvidenceStatus()
		gomega.Expect(s.Status).To(gomega.Equal("not_available"), fmt.Sprintf("Status: got %q, want not_available", s.Status))
		gomega.Expect(s.ReleaseImpact).To(gomega.Equal("no local recovery artifacts found"), fmt.Sprintf("ReleaseImpact: got %q", s.ReleaseImpact))
		gomega.Expect(s.FailureClasses).To(gomega.BeNil(), fmt.Sprintf("empty FailureClasses should be nil after finish, got %#v", s.FailureClasses))
		gomega.Expect(s.NextActions).To(gomega.BeNil(), fmt.Sprintf("empty NextActions should be nil after finish, got %#v", s.NextActions))

	})
	ginkgo.It("blocked when malformed artifacts present", func() {
		s := &productEvidenceRecovery{
			MalformedArtifactCount: 1,
			BlockedPacketCount:     1,
			FailureClasses:         map[string]int{},
			NextActions:            map[string]int{},
			Blockers:               []string{"err:path"},
		}
		s.finishRecoveryEvidenceStatus()
		gomega.Expect(s.Status).To(gomega.Equal("blocked"), fmt.Sprintf("Status: got %q, want blocked", s.Status))
		gomega.Expect(s.ReleaseImpact).To(gomega.ContainSubstring("malformed"), fmt.Sprintf("ReleaseImpact should mention malformed: %q", s.ReleaseImpact))

	})
	ginkgo.It("blocked when blocked packets present", func() {
		s := &productEvidenceRecovery{
			BlockedPacketCount: 2,
			FailureClasses:     map[string]int{},
			NextActions:        map[string]int{},
			Blockers:           []string{"missing"},
		}
		s.finishRecoveryEvidenceStatus()
		gomega.Expect(s.Status).To(gomega.Equal("blocked"), fmt.Sprintf("Status: got %q, want blocked", s.Status))

	})
	ginkgo.It("partial when open packets present", func() {
		s := &productEvidenceRecovery{
			FailureRecordCount: 1,
			OpenPacketCount:    1,
			RemainingAtomCount: 3,
			FailureClasses:     map[string]int{"executor_edit_failed": 1},
			NextActions:        map[string]int{"review_decomposition": 1},
		}
		s.finishRecoveryEvidenceStatus()
		gomega.Expect(s.Status).To(gomega.Equal("partial"), fmt.Sprintf("Status: got %q, want partial", s.Status))
		gomega.Expect(s.ReleaseImpact).To(gomega.ContainSubstring("open recovery packets"), fmt.Sprintf("ReleaseImpact should mention open packets: %q", s.ReleaseImpact))

	})
	ginkgo.It("partial when partial reconciliation packets", func() {
		s := &productEvidenceRecovery{
			FailureRecordCount: 1,
			PartialPacketCount: 1,
			RemainingAtomCount: 2,
			FailureClasses:     map[string]int{"executor_edit_failed": 1},
			NextActions:        map[string]int{"review_decomposition": 1},
		}
		s.finishRecoveryEvidenceStatus()
		gomega.Expect(s.Status).To(gomega.Equal("partial"), fmt.Sprintf("Status: got %q, want partial", s.Status))

	})
	ginkgo.It("pass when all reconciled", func() {
		s := &productEvidenceRecovery{
			FailureRecordCount:       1,
			DecompositionPacketCount: 1,
			ReconciliationCount:      1,
			RecoveredPacketCount:     1,
			CompletedAtomCount:       2,
			FailureClasses:           map[string]int{"executor_edit_failed": 1},
			NextActions:              map[string]int{"none": 1},
		}
		s.finishRecoveryEvidenceStatus()
		gomega.Expect(s.Status).To(gomega.Equal("pass"), fmt.Sprintf("Status: got %q, want pass", s.Status))
		gomega.Expect(s.ReleaseImpact).To(gomega.Equal("all local recovery packets with evidence are reconciled"), fmt.Sprintf("ReleaseImpact: got %q", s.ReleaseImpact))

	})
})
var _ = ginkgo.Describe("recovery evidence packet blockers", func() {
	ginkgo.It("packet with non-review next action blocks", func() {
		packet := recoverycontract.DecompositionPacket{
			NextAction: recoverycontract.NextActionInspectFailure,
			PartialState: recoverycontract.PartialState{
				DirtyStatus: recoverycontract.DirtyStatusClean,
				ScopeStatus: recoverycontract.ScopeStatusInScope,
			},
			CandidateAtoms: []recoverycontract.CandidateAtom{{ID: "atom-1"}},
		}
		blockers := recoveryEvidencePacketBlockers(packet)
		gomega.Expect(blockers).NotTo(gomega.BeEmpty(), "expected blockers for non-review next action")
		gomega.Expect(blockers[0]).To(gomega.ContainSubstring("inspect_failure"), fmt.Sprintf("expected blocker to mention inspect_failure, got %#v", blockers))

	})
	ginkgo.It("empty candidate atoms blocks", func() {
		packet := recoverycontract.DecompositionPacket{
			NextAction:     recoverycontract.NextActionReviewDecomposition,
			CandidateAtoms: nil,
		}
		blockers := recoveryEvidencePacketBlockers(packet)
		gomega.Expect(blockers).NotTo(gomega.BeEmpty(), "expected blockers for empty candidate atoms")
		gomega.Expect(stringSliceContains(blockers, "packet has no candidate recovery atoms")).To(gomega.BeTrue(), fmt.Sprintf("expected 'no candidate recovery atoms' blocker, got %#v", blockers))

	})
	ginkgo.It("dirty out of scope blocks", func() {
		packet := recoverycontract.DecompositionPacket{
			NextAction: recoverycontract.NextActionReviewDecomposition,
			PartialState: recoverycontract.PartialState{
				DirtyStatus: recoverycontract.DirtyStatusDirtyOutScope,
				ScopeStatus: recoverycontract.ScopeStatusInScope,
			},
			CandidateAtoms: []recoverycontract.CandidateAtom{{ID: "atom-1"}},
		}
		blockers := recoveryEvidencePacketBlockers(packet)
		gomega.Expect(stringSliceContains(blockers, "partial state includes out-of-scope dirty paths")).To(gomega.BeTrue(), fmt.Sprintf("expected dirty out-of-scope blocker, got %#v", blockers))

	})
	ginkgo.It("scope out of scope blocks", func() {
		packet := recoverycontract.DecompositionPacket{
			NextAction: recoverycontract.NextActionReviewDecomposition,
			PartialState: recoverycontract.PartialState{
				DirtyStatus: recoverycontract.DirtyStatusClean,
				ScopeStatus: recoverycontract.ScopeStatusOutOfScope,
			},
			CandidateAtoms: []recoverycontract.CandidateAtom{{ID: "atom-1"}},
		}
		blockers := recoveryEvidencePacketBlockers(packet)
		gomega.Expect(stringSliceContains(blockers, "partial state is out of recovery scope")).To(gomega.BeTrue(), fmt.Sprintf("expected out-of-scope blocker, got %#v", blockers))

	})
	ginkgo.It("clean in-scope packet with review action has no blockers", func() {
		packet := recoverycontract.DecompositionPacket{
			NextAction: recoverycontract.NextActionReviewDecomposition,
			PartialState: recoverycontract.PartialState{
				DirtyStatus: recoverycontract.DirtyStatusClean,
				ScopeStatus: recoverycontract.ScopeStatusInScope,
			},
			CandidateAtoms: []recoverycontract.CandidateAtom{{ID: "atom-1"}},
		}
		blockers := recoveryEvidencePacketBlockers(packet)
		gomega.Expect(blockers).To(gomega.BeEmpty(), fmt.Sprintf("expected no blockers for clean packet, got %#v", blockers))

	})
})
