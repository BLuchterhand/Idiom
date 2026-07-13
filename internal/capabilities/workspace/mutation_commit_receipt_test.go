package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/platform/config"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("mutation commit receipt", func() {
	ginkgo.It("marshals only its durable canonical projection", func() {
		receipt := &MutationCommitReceipt{
			Tool: "write_file",
			Root: "/private/workspace-root",
			Commits: []MutationCommit{{
				Identity: CanonicalWorkspacePath{
					CanonicalID:  "src/current.go",
					AbsolutePath: "/private/workspace-root/src/current.go",
					DisplayPath:  "f1 src/current.go",
				},
				Role: MutationTargetFile,
				Kind: MutationCommitReplacedFile,
				Preimage: MutationPreimageReceipt{
					Class:  MutationPreimageRequired,
					Exists: true,
					SHA256: "private-preimage-digest",
				},
			}},
			FailedTargets: []MutationRequestTarget{{
				Identity: CanonicalWorkspacePath{CanonicalID: "src/failed.go", AbsolutePath: "/private/workspace-root/src/failed.go"},
				Role:     MutationTargetFile,
				Preimage: MutationPreimageRequired,
			}},
			UnattemptedTargets: []MutationRequestTarget{{
				Identity: CanonicalWorkspacePath{CanonicalID: "src/later.go", AbsolutePath: "/private/workspace-root/src/later.go"},
				Role:     MutationTargetDestination,
				Preimage: MutationPreimageAbsent,
			}},
		}

		var boxed any = *receipt
		serializedReceipts := make([][]byte, 0, 3)
		for _, subject := range []any{receipt, *receipt, boxed} {
			encoded, err := json.Marshal(subject)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			serialized := string(encoded)
			gomega.Expect(serialized).NotTo(gomega.ContainSubstring("/private/workspace-root"))
			gomega.Expect(serialized).NotTo(gomega.ContainSubstring("private-preimage-digest"))
			gomega.Expect(strings.ToLower(serialized)).NotTo(gomega.ContainSubstring("sha256"))
			serializedReceipts = append(serializedReceipts, encoded)
		}
		for _, encoded := range serializedReceipts[1:] {
			gomega.Expect(encoded).To(gomega.Equal(serializedReceipts[0]))
		}

		var durable MutationCommitProjection
		gomega.Expect(json.Unmarshal(serializedReceipts[0], &durable)).To(gomega.Succeed())
		gomega.Expect(durable.Effects).To(gomega.Equal([]MutationCommitEffectProjection{{
			CanonicalID: "src/current.go",
			Kind:        MutationCommitReplacedFile,
			Role:        MutationTargetFile,
		}}))
		gomega.Expect(durable.PostimageFileIDs).To(gomega.Equal([]string{"src/current.go"}))
		gomega.Expect(durable.AffectedFileIDs).To(gomega.Equal([]string{"src/current.go"}))
		gomega.Expect(durable.FailedTargets).To(gomega.Equal([]MutationTargetProjection{{
			CanonicalID:   "src/failed.go",
			Role:          MutationTargetFile,
			PreimageClass: MutationPreimageRequired,
		}}))
		gomega.Expect(durable.UnattemptedTargets).To(gomega.Equal([]MutationTargetProjection{{
			CanonicalID:   "src/later.go",
			Role:          MutationTargetDestination,
			PreimageClass: MutationPreimageAbsent,
		}}))
	})

	ginkgo.It("validates unbound durable receipts structurally", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		valid := MutationCommitProjection{
			Effects: []MutationCommitEffectProjection{{
				CanonicalID: "src/current.go", Kind: MutationCommitReplacedFile, Role: MutationTargetFile,
			}},
			PostimageFileIDs: []string{"src/current.go"},
			AffectedFileIDs:  []string{"src/current.go"},
		}
		gomega.Expect(manager.ValidateMutationCommitProjection(context.Background(), valid)).To(gomega.Succeed())

		invalidKind := valid
		invalidKind.Effects = append([]MutationCommitEffectProjection(nil), valid.Effects...)
		invalidKind.Effects[0].Kind = MutationCommitKind("tampered")
		gomega.Expect(manager.ValidateMutationCommitProjection(context.Background(), invalidKind)).To(
			gomega.MatchError(gomega.ContainSubstring("effects[0].kind is invalid")),
		)

		invalidPath := valid
		invalidPath.Effects = append([]MutationCommitEffectProjection(nil), valid.Effects...)
		invalidPath.Effects[0].CanonicalID = "../escape.go"
		gomega.Expect(manager.ValidateMutationCommitProjection(context.Background(), invalidPath)).To(
			gomega.MatchError(gomega.ContainSubstring("normalized relative workspace path")),
		)
	})

	ginkgo.It("rejects duplicate canonical identities only within each receipt classification", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx := context.Background()

		duplicateEffects := MutationCommitProjection{
			Effects: []MutationCommitEffectProjection{
				{CanonicalID: "src/partial.go", Kind: MutationCommitReplacedFile, Role: MutationTargetFile},
				{CanonicalID: "src/partial.go", Kind: MutationCommitRemovedFile, Role: MutationTargetFile},
			},
			PostimageFileIDs: []string{"src/partial.go"},
			AffectedFileIDs:  []string{"src/partial.go"},
		}
		gomega.Expect(manager.ValidateMutationCommitProjection(ctx, duplicateEffects)).To(
			gomega.MatchError(gomega.ContainSubstring("effects[1] duplicates an earlier canonical identity")),
		)

		duplicateFailedTargets := MutationCommitProjection{
			FailedTargets: []MutationTargetProjection{
				{CanonicalID: "src/partial.go", Role: MutationTargetFile, PreimageClass: MutationPreimageRequired},
				{CanonicalID: "src/partial.go", Role: MutationTargetSource, PreimageClass: MutationPreimageOptional},
			},
		}
		gomega.Expect(manager.ValidateMutationCommitProjection(ctx, duplicateFailedTargets)).To(
			gomega.MatchError(gomega.ContainSubstring("failed_targets[1] duplicates an earlier canonical identity")),
		)

		duplicateUnattemptedTargets := MutationCommitProjection{
			UnattemptedTargets: []MutationTargetProjection{
				{CanonicalID: "src/partial.go", Role: MutationTargetFile, PreimageClass: MutationPreimageOptional},
				{CanonicalID: "src/partial.go", Role: MutationTargetDestination, PreimageClass: MutationPreimageAbsent},
			},
		}
		gomega.Expect(manager.ValidateMutationCommitProjection(ctx, duplicateUnattemptedTargets)).To(
			gomega.MatchError(gomega.ContainSubstring("unattempted_targets[1] duplicates an earlier canonical identity")),
		)

		partialSameIdentity := MutationCommitProjection{
			Effects: []MutationCommitEffectProjection{{
				CanonicalID: "src/partial.go", Kind: MutationCommitReplacedFile, Role: MutationTargetFile,
			}},
			PostimageFileIDs: []string{"src/partial.go"},
			AffectedFileIDs:  []string{"src/partial.go"},
			FailedTargets: []MutationTargetProjection{{
				CanonicalID: "src/partial.go", Role: MutationTargetSource, PreimageClass: MutationPreimageRequired,
			}},
			UnattemptedTargets: []MutationTargetProjection{{
				CanonicalID: "src/partial.go", Role: MutationTargetDestination, PreimageClass: MutationPreimageAbsent,
			}},
		}
		gomega.Expect(manager.ValidateMutationCommitProjection(ctx, partialSameIdentity)).To(gomega.Succeed())
	})

	ginkgo.It("validates loaded receipts against the context-bound workspace scope", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "allowed.go"), "package allowed\n")
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx, scope, err := manager.BindMutationScope(context.Background(), MutationScopePaths{AllowedPaths: []string{"allowed.go"}})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		validProjection := func() MutationCommitProjection {
			durableScope := scope.DurableProjection()
			return MutationCommitProjection{
				Scope: &durableScope,
				Effects: []MutationCommitEffectProjection{{
					CanonicalID: "allowed.go", Kind: MutationCommitReplacedFile, Role: MutationTargetFile,
				}},
				PostimageFileIDs: []string{"allowed.go"},
				AffectedFileIDs:  []string{"allowed.go"},
			}
		}
		gomega.Expect(manager.ValidateMutationCommitProjection(ctx, validProjection())).To(gomega.Succeed())

		invalidScopeKind := validProjection()
		invalidScopeKind.Scope.Rules[0].Kind = MutationScopeRuleKind("tampered")
		gomega.Expect(manager.ValidateMutationCommitProjection(ctx, invalidScopeKind)).To(
			gomega.MatchError(gomega.ContainSubstring("rules[0].kind is invalid")),
		)

		invalidScopeSource := validProjection()
		invalidScopeSource.Scope.Rules[0].Source = MutationScopeRuleSource("tampered")
		gomega.Expect(manager.ValidateMutationCommitProjection(ctx, invalidScopeSource)).To(
			gomega.MatchError(gomega.ContainSubstring("rules[0].source is invalid")),
		)

		invalidEffectRole := validProjection()
		invalidEffectRole.Effects[0].Role = MutationTargetRole("tampered")
		gomega.Expect(manager.ValidateMutationCommitProjection(ctx, invalidEffectRole)).To(
			gomega.MatchError(gomega.ContainSubstring("effects[0].role is invalid")),
		)

		invalidPreimage := validProjection()
		invalidPreimage.FailedTargets = []MutationTargetProjection{{
			CanonicalID: "allowed.go", Role: MutationTargetFile, PreimageClass: MutationPreimageClass("tampered"),
		}}
		gomega.Expect(manager.ValidateMutationCommitProjection(ctx, invalidPreimage)).To(
			gomega.MatchError(gomega.ContainSubstring("failed_targets[0].preimage_class is invalid")),
		)

		mismatchedScope := validProjection()
		mismatchedScope.Scope.Rules[0].CanonicalID = "other.go"
		mismatchedScope.Scope.Presentations[0].CanonicalID = "other.go"
		gomega.Expect(manager.ValidateMutationCommitProjection(ctx, mismatchedScope)).To(
			gomega.MatchError(gomega.ContainSubstring("scope does not match")),
		)

		outOfScopeProjections := []MutationCommitProjection{
			func() MutationCommitProjection {
				projection := validProjection()
				projection.Effects = []MutationCommitEffectProjection{{
					CanonicalID: "outside", Kind: MutationCommitCreatedDirectory, Role: MutationTargetDirectory,
				}}
				projection.PostimageFileIDs = nil
				projection.AffectedFileIDs = nil
				return projection
			}(),
			func() MutationCommitProjection {
				projection := validProjection()
				projection.Effects = nil
				projection.PostimageFileIDs = nil
				projection.AffectedFileIDs = nil
				projection.FailedTargets = []MutationTargetProjection{{
					CanonicalID: "outside.go", Role: MutationTargetFile, PreimageClass: MutationPreimageOptional,
				}}
				return projection
			}(),
			func() MutationCommitProjection {
				projection := validProjection()
				projection.Effects = nil
				projection.PostimageFileIDs = nil
				projection.AffectedFileIDs = nil
				projection.UnattemptedTargets = []MutationTargetProjection{{
					CanonicalID: "outside.go", Role: MutationTargetFile, PreimageClass: MutationPreimageOptional,
				}}
				return projection
			}(),
		}
		for _, projection := range outOfScopeProjections {
			gomega.Expect(manager.ValidateMutationCommitProjection(ctx, projection)).To(
				gomega.MatchError(gomega.ContainSubstring("outside the bound workspace scope")),
			)
		}
	})

	ginkgo.It("rejects loaded receipts after the bound workspace root identity changes", func() {
		parent := ginkgo.GinkgoT().TempDir()
		root := filepath.Join(parent, "workspace")
		gomega.Expect(os.Mkdir(root, 0o755)).To(gomega.Succeed())
		writeTestFile(filepath.Join(root, "allowed.go"), "package allowed\n")
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx, scope, err := manager.BindMutationScope(context.Background(), MutationScopePaths{AllowedPaths: []string{"allowed.go"}})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		durableScope := scope.DurableProjection()

		gomega.Expect(os.Rename(root, filepath.Join(parent, "original-workspace"))).To(gomega.Succeed())
		gomega.Expect(os.Mkdir(root, 0o755)).To(gomega.Succeed())
		gomega.Expect(manager.ValidateMutationCommitProjection(ctx, MutationCommitProjection{Scope: &durableScope})).To(
			gomega.MatchError(gomega.ContainSubstring("root identity changed")),
		)
	})
})
