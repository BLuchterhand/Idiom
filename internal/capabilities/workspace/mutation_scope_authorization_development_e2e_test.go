package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/config"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
)

var _ = ginkgo.Describe("workspace mutation scope and authorization", ginkgo.Label("development-scenario", "e2e", "integration", "race-contract"), func() {
	ginkgo.It("distinguishes absent scope from an explicitly empty scope", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})

		unbound := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameWriteFile, Path: "unbound.txt", Content: "allowed\n"})
		gomega.Expect(unbound.Error).To(gomega.BeEmpty(), fmt.Sprintf("unbound write result: %#v", unbound))

		ctx, scope, err := manager.BindMutationScope(context.Background(), MutationScopePaths{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(scope.RequiredCoverage).To(gomega.BeEmpty())
		gomega.Expect(scope.Rules).To(gomega.BeEmpty())

		denied := manager.Execute(ctx, ToolRequest{Tool: ToolNameWriteFile, Path: "denied.txt", Content: "denied\n"})
		gomega.Expect(denied.Error).To(gomega.ContainSubstring("outside the invocation's typed"))
		_, statErr := os.Stat(filepath.Join(root, "denied.txt"))
		gomega.Expect(os.IsNotExist(statErr)).To(gomega.BeTrue(), fmt.Sprintf("explicitly empty scope wrote denied.txt: %v", statErr))
	})

	ginkgo.It("rejects the workspace root as an explicit mutation scope", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})

		for _, paths := range []MutationScopePaths{
			{RequiredPaths: []string{"."}},
			{AllowedPaths: []string{"."}},
			{AuthorizedNewFiles: []string{"."}},
		} {
			_, _, err := manager.BindMutationScope(context.Background(), paths)
			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("workspace root cannot be a mutation scope")))
		}
	})

	ginkgo.It("rejects a symlink presentation of the canonical workspace root", func() {
		root := ginkgo.GinkgoT().TempDir()
		if err := os.Symlink(".", filepath.Join(root, "root-alias")); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})

		for _, paths := range []MutationScopePaths{
			{RequiredPaths: []string{"root-alias"}},
			{AllowedPaths: []string{"root-alias"}},
			{AuthorizedNewFiles: []string{"root-alias"}},
		} {
			_, _, err := manager.BindMutationScope(context.Background(), paths)
			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("workspace root cannot be a mutation scope")))
		}
	})

	ginkgo.It("uses one canonical identity for an alias scope and physical commit", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "real", "target.txt"), "before\n")
		if err := os.Symlink("real", filepath.Join(root, "alias")); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx, scope, err := manager.BindMutationScope(context.Background(), MutationScopePaths{
			RequiredPaths: []string{"alias/target.txt"},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(scope.RequiredCoverage).To(gomega.Equal([]CanonicalWorkspacePath{{
			CanonicalID: "real/target.txt", AbsolutePath: filepath.Join(manager.Root(), "real", "target.txt"), DisplayPath: "real/target.txt",
		}}))
		gomega.Expect(scope.Rules).To(gomega.Equal([]MutationScopeRule{{
			Kind: MutationScopeExactFile, Source: MutationScopeRequiredPath, Identity: scope.RequiredCoverage[0],
		}}))

		result := manager.Execute(ctx, ToolRequest{Tool: ToolNameWriteFile, Path: "real/target.txt", Content: "after\n"})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), fmt.Sprintf("canonical scoped write result: %#v", result))
		gomega.Expect(result.ChangedPaths).To(gomega.Equal([]string{"real/target.txt"}))
		receipt := result.CommitReceipt()
		gomega.Expect(receipt).NotTo(gomega.BeNil())
		gomega.Expect(receipt.Scope).NotTo(gomega.BeNil())
		gomega.Expect(receipt.Scope.RequiredCoverage).To(gomega.Equal(scope.RequiredCoverage))
		gomega.Expect(receipt.Targets).To(gomega.HaveLen(1))
		gomega.Expect(receipt.Targets[0].Identity.CanonicalID).To(gomega.Equal("real/target.txt"))
		gomega.Expect(receipt.Commits).To(gomega.HaveLen(1))
		gomega.Expect(receipt.Commits[0].Identity.CanonicalID).To(gomega.Equal("real/target.txt"))
		gomega.Expect(readTestFile(filepath.Join(root, "alias", "target.txt"))).To(gomega.Equal("after\n"))

		gomega.Expect(os.MkdirAll(filepath.Join(root, "other"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.Remove(filepath.Join(root, "alias"))).To(gomega.Succeed())
		gomega.Expect(os.Symlink("other", filepath.Join(root, "alias"))).To(gomega.Succeed())
		encodedScope, err := json.Marshal(scope)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		var resumedScope CanonicalMutationScopeProjection
		gomega.Expect(json.Unmarshal(encodedScope, &resumedScope)).To(gomega.Succeed())
		claims := resumedScope.ProjectClaims([]string{"alias/target.txt", "real/target.txt", "real/sibling.txt"})
		gomega.Expect(claims.CanonicalIDs).To(gomega.Equal([]string{"real/target.txt"}), "captured alias and canonical claims did not converge")
		gomega.Expect(claims.UnsupportedPaths).To(gomega.Equal([]string{"real/sibling.txt"}))
	})

	ginkgo.It("rejects a lexical scoped path whose parent retargets to another in-workspace identity", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "allowed"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.MkdirAll(filepath.Join(root, "other"), 0o755)).To(gomega.Succeed())
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx, scope, err := manager.BindMutationScope(context.Background(), MutationScopePaths{
			AllowedPaths: []string{"allowed/new.txt"},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(scope.Rules).To(gomega.HaveLen(2))
		gomega.Expect(scope.Rules[0].Kind).To(gomega.Equal(MutationScopeExactFile))
		gomega.Expect(scope.Rules[0].Identity.CanonicalID).To(gomega.Equal("allowed/new.txt"))
		gomega.Expect(scope.Rules[1].Kind).To(gomega.Equal(MutationScopeParentDirectoryCreation))
		gomega.Expect(scope.Rules[1].Identity.CanonicalID).To(gomega.Equal("allowed"))

		gomega.Expect(os.Rename(filepath.Join(root, "allowed"), filepath.Join(root, "allowed-original"))).To(gomega.Succeed())
		if err := os.Symlink("other", filepath.Join(root, "allowed")); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}

		result := manager.Execute(ctx, ToolRequest{Tool: ToolNameWriteFile, Path: "allowed/new.txt", Content: "must not write\n"})
		gomega.Expect(result.Error).To(gomega.ContainSubstring("outside the invocation's typed"))
		_, statErr := os.Stat(filepath.Join(root, "other", "new.txt"))
		gomega.Expect(os.IsNotExist(statErr)).To(gomega.BeTrue(), fmt.Sprintf("retargeted scope wrote other/new.txt: %v", statErr))
	})

	ginkgo.It("authorizes directory descendants without widening to a sibling", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "allowed"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.MkdirAll(filepath.Join(root, "sibling"), 0o755)).To(gomega.Succeed())
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx, scope, err := manager.BindMutationScope(context.Background(), MutationScopePaths{
			AllowedPaths: []string{"allowed"},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(scope.RequiredCoverage).To(gomega.BeEmpty(), "directory authorization became a coverage obligation")
		gomega.Expect(scope.Rules).To(gomega.HaveLen(1))
		gomega.Expect(scope.Rules[0].Kind).To(gomega.Equal(MutationScopeDirectorySubtree))
		gomega.Expect(scope.Rules[0].Identity.CanonicalID).To(gomega.Equal("allowed"))

		inside := manager.Execute(ctx, ToolRequest{Tool: ToolNameWriteFile, Path: "allowed/nested/new.txt", Content: "inside\n"})
		gomega.Expect(inside.Error).To(gomega.BeEmpty(), fmt.Sprintf("directory descendant result: %#v", inside))
		outside := manager.Execute(ctx, ToolRequest{Tool: ToolNameWriteFile, Path: "sibling/new.txt", Content: "outside\n"})
		gomega.Expect(outside.Error).To(gomega.ContainSubstring("outside the invocation's typed"))
		_, statErr := os.Stat(filepath.Join(root, "sibling", "new.txt"))
		gomega.Expect(os.IsNotExist(statErr)).To(gomega.BeTrue())
	})

	ginkgo.It("treats an existing required directory as authorization-only subtree scope", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "required-dir"), 0o755)).To(gomega.Succeed())
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx, scope, err := manager.BindMutationScope(context.Background(), MutationScopePaths{RequiredPaths: []string{"required-dir"}})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(scope.RequiredCoverage).To(gomega.BeEmpty(), "required directory became a permanent file coverage obligation")
		gomega.Expect(scope.Rules).To(gomega.HaveLen(1))
		gomega.Expect(scope.Rules[0].Kind).To(gomega.Equal(MutationScopeDirectorySubtree))
		gomega.Expect(scope.Rules[0].Source).To(gomega.Equal(MutationScopeRequiredPath))

		result := manager.Execute(ctx, ToolRequest{Tool: ToolNameWriteFile, Path: "required-dir/child.go", Content: "package required\n"})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), fmt.Sprintf("required directory descendant result: %#v", result))
	})

	ginkgo.It("canonicalizes an allowed directory alias into one subtree rule", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "physical"), 0o755)).To(gomega.Succeed())
		if err := os.Symlink("physical", filepath.Join(root, "alias-dir")); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx, scope, err := manager.BindMutationScope(context.Background(), MutationScopePaths{AllowedPaths: []string{"alias-dir"}})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(scope.Rules).To(gomega.HaveLen(1))
		gomega.Expect(scope.Rules[0].Kind).To(gomega.Equal(MutationScopeDirectorySubtree))
		gomega.Expect(scope.Rules[0].Identity.CanonicalID).To(gomega.Equal("physical"))

		result := manager.Execute(ctx, ToolRequest{Tool: ToolNameWriteFile, Path: "alias-dir/child.txt", Content: "alias\n"})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), fmt.Sprintf("alias directory result: %#v", result))
		gomega.Expect(result.ChangedPaths).To(gomega.ContainElement("physical/child.txt"))

		gomega.Expect(os.MkdirAll(filepath.Join(root, "other"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.Remove(filepath.Join(root, "alias-dir"))).To(gomega.Succeed())
		gomega.Expect(os.Symlink("other", filepath.Join(root, "alias-dir"))).To(gomega.Succeed())
		claims := scope.DurableProjection().ProjectClaims([]string{
			"alias-dir/child.txt",
			"physical/child.txt",
			"alias-dir/nested/future.txt",
			"physical/nested/future.txt",
			"alias-dir/../other/sibling.txt",
			"other/sibling.txt",
		})
		gomega.Expect(claims.CanonicalIDs).To(gomega.Equal([]string{"physical/child.txt", "physical/nested/future.txt"}))
		gomega.Expect(claims.UnsupportedPaths).To(gomega.Equal([]string{"alias-dir/../other/sibling.txt", "other/sibling.txt"}))

		unsafeScope := cloneCanonicalMutationScope(scope)
		unsafeScope.Presentations = append(unsafeScope.Presentations,
			MutationScopePresentation{PresentedPath: filepath.Join(root, "private"), CanonicalID: "physical", Source: MutationScopeAllowedPath},
			MutationScopePresentation{PresentedPath: "../escape", CanonicalID: "physical", Source: MutationScopeAllowedPath},
			MutationScopePresentation{PresentedPath: "", CanonicalID: "physical", Source: MutationScopeAllowedPath},
		)
		gomega.Expect(unsafeScope.DurableProjection().Presentations).To(gomega.Equal([]CanonicalMutationScopePresentationProjection{{
			PresentedPath: "alias-dir", CanonicalID: "physical", Source: MutationScopeAllowedPath,
		}}))

		var boxed any = scope
		for _, subject := range []any{&scope, scope, boxed} {
			encoded, err := json.Marshal(subject)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			serialized := string(encoded)
			gomega.Expect(serialized).NotTo(gomega.ContainSubstring(manager.Root()))
			gomega.Expect(strings.ToLower(serialized)).NotTo(gomega.ContainSubstring("absolute"))
			gomega.Expect(serialized).To(gomega.ContainSubstring("alias-dir"), "privacy-safe relative presentation was not retained for resume")
			gomega.Expect(serialized).To(gomega.ContainSubstring("physical"))
			var resumed CanonicalMutationScopeProjection
			gomega.Expect(json.Unmarshal(encoded, &resumed)).To(gomega.Succeed())
			gomega.Expect(resumed.ProjectClaims([]string{"alias-dir/resumed.txt", "physical/resumed.txt"}).CanonicalIDs).
				To(gomega.Equal([]string{"physical/resumed.txt"}))
		}
	})

	ginkgo.It("projects nested directory aliases through the most specific durable presentation", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "outer"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.MkdirAll(filepath.Join(root, "physical", "other"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.MkdirAll(filepath.Join(root, "sibling"), 0o755)).To(gomega.Succeed())
		if err := os.Symlink(filepath.Join("..", "physical", "other"), filepath.Join(root, "outer", "link")); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})

		orders := [][]string{
			{"outer", "outer/link"},
			{"outer/link", "outer"},
		}
		scopes := make([]CanonicalMutationScope, 0, len(orders))
		for _, allowedPaths := range orders {
			_, scope, err := manager.BindMutationScope(context.Background(), MutationScopePaths{AllowedPaths: allowedPaths})
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			scopes = append(scopes, scope)
		}

		gomega.Expect(os.Remove(filepath.Join(root, "outer", "link"))).To(gomega.Succeed())
		gomega.Expect(os.Symlink(filepath.Join("..", "sibling"), filepath.Join(root, "outer", "link"))).To(gomega.Succeed())
		for _, scope := range scopes {
			encoded, err := json.Marshal(scope)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			serialized := string(encoded)
			gomega.Expect(serialized).NotTo(gomega.ContainSubstring(root))
			gomega.Expect(strings.ToLower(serialized)).NotTo(gomega.ContainSubstring("absolute"))

			var resumed CanonicalMutationScopeProjection
			gomega.Expect(json.Unmarshal(encoded, &resumed)).To(gomega.Succeed())
			claims := resumed.ProjectClaims([]string{
				"outer/link/file.go",
				"physical/other/file.go",
				"outer/kept.go",
				"outer/link",
				"physical/other",
				"sibling/file.go",
				"outer/link/../sibling.go",
			})
			gomega.Expect(claims.CanonicalIDs).To(gomega.Equal([]string{
				"physical/other/file.go",
				"outer/kept.go",
				"physical/other",
			}))
			gomega.Expect(claims.UnsupportedPaths).To(gomega.Equal([]string{
				"sibling/file.go",
				"outer/link/../sibling.go",
			}))
		}
	})

	ginkgo.It("projects nested canonical directory rules independent of insertion order", func() {
		outer := CanonicalMutationScopeRuleProjection{
			Kind: MutationScopeDirectorySubtree, Source: MutationScopeAllowedPath, CanonicalID: "physical",
		}
		nested := CanonicalMutationScopeRuleProjection{
			Kind: MutationScopeDirectorySubtree, Source: MutationScopeAllowedPath, CanonicalID: "physical/other",
		}
		for _, rules := range [][]CanonicalMutationScopeRuleProjection{{outer, nested}, {nested, outer}} {
			claims := (CanonicalMutationScopeProjection{Rules: rules}).ProjectClaims([]string{
				"physical/other/file.go",
				"physical/kept.go",
				"sibling/file.go",
				"physical/other/../sibling.go",
			})
			gomega.Expect(claims.CanonicalIDs).To(gomega.Equal([]string{"physical/other/file.go", "physical/kept.go"}))
			gomega.Expect(claims.UnsupportedPaths).To(gomega.Equal([]string{"sibling/file.go", "physical/other/../sibling.go"}))
		}
	})

	ginkgo.It("rejects ambiguous equally specific durable presentations", func() {
		physicalA := CanonicalMutationScopePresentationProjection{
			PresentedPath: "alias", CanonicalID: "physical/a", Source: MutationScopeAllowedPath,
		}
		physicalB := CanonicalMutationScopePresentationProjection{
			PresentedPath: "alias", CanonicalID: "physical/b", Source: MutationScopeAllowedPath,
		}
		rules := []CanonicalMutationScopeRuleProjection{
			{Kind: MutationScopeDirectorySubtree, Source: MutationScopeAllowedPath, CanonicalID: "physical/a"},
			{Kind: MutationScopeDirectorySubtree, Source: MutationScopeAllowedPath, CanonicalID: "physical/b"},
		}
		for _, presentations := range [][]CanonicalMutationScopePresentationProjection{{physicalA, physicalB}, {physicalB, physicalA}} {
			claims := (CanonicalMutationScopeProjection{Rules: rules, Presentations: presentations}).ProjectClaims([]string{
				"alias",
				"alias/file.go",
				"physical/a/file.go",
			})
			gomega.Expect(claims.CanonicalIDs).To(gomega.Equal([]string{"physical/a/file.go"}))
			gomega.Expect(claims.UnsupportedPaths).To(gomega.Equal([]string{"alias", "alias/file.go"}))
		}
	})

	ginkgo.It("intersects durable directory presentations with matching rule authority", func() {
		projection := CanonicalMutationScopeProjection{
			Rules: []CanonicalMutationScopeRuleProjection{{
				Kind: MutationScopeDirectorySubtree, Source: MutationScopeRequiredPath, CanonicalID: "physical",
			}},
			Presentations: []CanonicalMutationScopePresentationProjection{{
				PresentedPath: "alias", CanonicalID: "physical", Source: MutationScopeAllowedPath,
			}},
		}
		claims := projection.ProjectClaims([]string{"alias", "alias/file.go", "physical/file.go"})
		gomega.Expect(claims.CanonicalIDs).To(gomega.Equal([]string{"physical", "physical/file.go"}), "exact and canonical direct matches changed")
		gomega.Expect(claims.UnsupportedPaths).To(gomega.Equal([]string{"alias/file.go"}), "presentation widened without matching directory authority")
	})

	ginkgo.It("allows only parent mkdirs and the exact explicitly authorized new file", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx, scope, err := manager.BindMutationScope(context.Background(), MutationScopePaths{
			AuthorizedNewFiles: []string{"generated/nested/new.go"},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(scope.RequiredCoverage).To(gomega.BeEmpty())
		gomega.Expect(scope.Rules).To(gomega.ContainElements(
			gomega.HaveField("Kind", MutationScopeExactFile),
			gomega.HaveField("Kind", MutationScopeParentDirectoryCreation),
		))

		parent := manager.Execute(ctx, ToolRequest{Tool: ToolNameMkdir, Path: "generated"})
		gomega.Expect(parent.Error).To(gomega.BeEmpty(), fmt.Sprintf("authorized parent mkdir: %#v", parent))
		gomega.Expect(parent.CommitReceipt().VerifiablePaths()).To(gomega.BeEmpty())
		nested := manager.Execute(ctx, ToolRequest{Tool: ToolNameMkdir, Path: "generated/nested"})
		gomega.Expect(nested.Error).To(gomega.BeEmpty(), fmt.Sprintf("authorized nested mkdir: %#v", nested))
		gomega.Expect(nested.CommitReceipt().VerifiablePaths()).To(gomega.BeEmpty())
		created := manager.Execute(ctx, ToolRequest{Tool: ToolNameWriteFile, Path: "generated/nested/new.go", Content: "package generated\n"})
		gomega.Expect(created.Error).To(gomega.BeEmpty(), fmt.Sprintf("authorized new file: %#v", created))
		sibling := manager.Execute(ctx, ToolRequest{Tool: ToolNameWriteFile, Path: "generated/nested/sibling.go", Content: "package generated\n"})
		gomega.Expect(sibling.Error).To(gomega.ContainSubstring("outside the invocation's typed"))
	})

	ginkgo.It("separates implicit parent directory effects from verifiable file commits", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx, scope, err := manager.BindMutationScope(context.Background(), MutationScopePaths{
			AuthorizedNewFiles: []string{"generated/new.go"},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		result := manager.Execute(ctx, ToolRequest{Tool: ToolNameWriteFile, Path: "generated/new.go", Content: "package generated\n"})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), fmt.Sprintf("implicit parent write result: %#v", result))
		receipt := result.CommitReceipt()
		gomega.Expect(receipt.Commits).To(gomega.HaveLen(2))
		gomega.Expect(receipt.Commits).To(gomega.ContainElements(
			gomega.HaveField("Kind", MutationCommitCreatedDirectory),
			gomega.HaveField("Kind", MutationCommitReplacedFile),
		))
		gomega.Expect(result.ChangedPaths).To(gomega.ConsistOf("generated", "generated/new.go"))
		gomega.Expect(receipt.VerifiablePaths()).To(gomega.Equal([]string{"generated/new.go"}))
		durableScope := scope.DurableProjection()
		durableReceipt := receipt.DurableProjection()
		gomega.Expect(durableReceipt).To(gomega.Equal(MutationCommitProjection{
			Scope: &durableScope,
			Effects: []MutationCommitEffectProjection{
				{CanonicalID: "generated", Kind: MutationCommitCreatedDirectory, Role: MutationTargetDirectory},
				{CanonicalID: "generated/new.go", Kind: MutationCommitReplacedFile, Role: MutationTargetFile},
			},
			PostimageFileIDs: []string{"generated/new.go"},
			AffectedFileIDs:  []string{"generated/new.go"},
		}))
		gomega.Expect(manager.ValidateMutationCommitProjection(ctx, durableReceipt)).To(gomega.Succeed())
	})

	ginkgo.It("keeps removed files affected but outside postimage verification", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "victim.txt"), "remove me\n")
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx, scope, err := manager.BindMutationScope(context.Background(), MutationScopePaths{AllowedPaths: []string{"victim.txt"}})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		result := manager.Execute(ctx, ToolRequest{
			Tool:    ToolNameApplyPatch,
			Content: "*** Begin Patch\n*** Delete File: victim.txt\n*** End Patch",
		})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), fmt.Sprintf("delete patch result: %#v", result))
		receipt := result.CommitReceipt()
		gomega.Expect(receipt.VerifiablePaths()).To(gomega.BeEmpty(), "removed file was exposed as a postimage verification target")
		durableScope := scope.DurableProjection()
		durableReceipt := receipt.DurableProjection()
		gomega.Expect(durableReceipt).To(gomega.Equal(MutationCommitProjection{
			Scope:           &durableScope,
			Effects:         []MutationCommitEffectProjection{{CanonicalID: "victim.txt", Kind: MutationCommitRemovedFile, Role: MutationTargetSource}},
			AffectedFileIDs: []string{"victim.txt"},
		}))
		gomega.Expect(manager.ValidateMutationCommitProjection(ctx, durableReceipt)).To(gomega.Succeed())
	})

	ginkgo.It("retains alias-collapsed partial effects in each exact outcome classification", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "real", "target.txt"), "one\ntwo\nthree\n")
		if err := os.Symlink("real", filepath.Join(root, "alias")); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		patch := "*** Begin Patch\n" +
			"*** Update File: alias/target.txt\n" +
			"@@\n" +
			"-one\n" +
			"+ONE\n" +
			"*** Update File: real/target.txt\n" +
			"@@\n" +
			"-two\n" +
			"+TWO\n" +
			"*** Update File: alias/target.txt\n" +
			"@@\n" +
			"-three\n" +
			"+THREE\n" +
			"*** End Patch"

		result := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameApplyPatch, Content: patch})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty())
		gomega.Expect(result.ChangedPaths).To(gomega.Equal([]string{"real/target.txt"}))
		gomega.Expect(readTestFile(filepath.Join(root, "real", "target.txt"))).To(gomega.Equal("ONE\ntwo\nthree\n"))
		receipt := result.CommitReceipt()
		gomega.Expect(receipt).NotTo(gomega.BeNil())
		projection := receipt.DurableProjection()
		gomega.Expect(projection.Effects).To(gomega.Equal([]MutationCommitEffectProjection{{
			CanonicalID: "real/target.txt", Kind: MutationCommitReplacedFile, Role: MutationTargetSource,
		}}))
		gomega.Expect(projection.FailedTargets).To(gomega.Equal([]MutationTargetProjection{{
			CanonicalID: "real/target.txt", Role: MutationTargetSource, PreimageClass: MutationPreimageRequired,
		}}))
		gomega.Expect(projection.UnattemptedTargets).To(gomega.Equal([]MutationTargetProjection{{
			CanonicalID: "real/target.txt", Role: MutationTargetSource, PreimageClass: MutationPreimageRequired,
		}}))
		gomega.Expect(manager.ValidateMutationCommitProjection(context.Background(), projection)).To(gomega.Succeed())
	})

	ginkgo.It("keeps authorized-new-file must-be-absent authority over overlapping exact rules", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx, scope, err := manager.BindMutationScope(context.Background(), MutationScopePaths{
			RequiredPaths:      []string{"new.go"},
			AllowedPaths:       []string{"new.go"},
			AuthorizedNewFiles: []string{"new.go"},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(scope.RequiredCoverage).To(gomega.HaveLen(1))

		first := manager.Execute(ctx, ToolRequest{Tool: ToolNameWriteFile, Path: "new.go", Content: "package first\n"})
		gomega.Expect(first.Error).To(gomega.BeEmpty(), fmt.Sprintf("first new-file result: %#v", first))
		second := manager.Execute(ctx, ToolRequest{Tool: ToolNameWriteFile, Path: "new.go", Content: "package second\n"})
		gomega.Expect(second.Error).NotTo(gomega.BeEmpty(), "overlapping required/allowed rule bypassed must-be-absent authority")
		gomega.Expect(readTestFile(filepath.Join(root, "new.go"))).To(gomega.Equal("package first\n"))
	})

	ginkgo.It("revalidates authorized-new-file absence inside the rooted transaction", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		manager.SetPermissionRules(map[string][]toolpolicy.Rule{
			ToolNameWriteFile: {{Pattern: "new.go", Decision: toolpolicy.Ask}},
		})
		ctx, _, err := manager.BindMutationScope(context.Background(), MutationScopePaths{AuthorizedNewFiles: []string{"new.go"}})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		approver := NewMockMutationApprover(gomock.NewController(ginkgo.GinkgoT()))
		approver.EXPECT().ApproveWorkspaceMutation(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ MutationApprovalRequest) (MutationApprovalDecision, error) {
			writeTestFile(filepath.Join(root, "new.go"), "external\n")
			return MutationApprovalDecision{Approved: true}, nil
		})

		result := manager.Execute(WithMutationApprover(ctx, approver), ToolRequest{Tool: ToolNameWriteFile, Path: "new.go", Content: "manager\n"})
		gomega.Expect(result.Error).To(gomega.ContainSubstring("appeared after scope binding"), fmt.Sprintf("new-file race result: %#v", result))
		gomega.Expect(result.ChangedPaths).To(gomega.BeEmpty())
		receipt := result.CommitReceipt()
		gomega.Expect(receipt).NotTo(gomega.BeNil())
		gomega.Expect(receipt.FailedTargets).To(gomega.HaveLen(1))
		gomega.Expect(receipt.Commits).To(gomega.BeEmpty())
		gomega.Expect(readTestFile(filepath.Join(root, "new.go"))).To(gomega.Equal("external\n"))
	})

	ginkgo.It("derives non-must-be-absent parent authority for absent exact required or allowed files", func() {
		for _, paths := range []MutationScopePaths{
			{RequiredPaths: []string{"required/new.go"}},
			{AllowedPaths: []string{"allowed/new.go"}},
		} {
			root := ginkgo.GinkgoT().TempDir()
			manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
			ctx, scope, err := manager.BindMutationScope(context.Background(), paths)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			target := paths.RequiredPaths
			source := MutationScopeRequiredPath
			if len(target) == 0 {
				target = paths.AllowedPaths
				source = MutationScopeAllowedPath
			}
			gomega.Expect(scope.Rules).To(gomega.ContainElement(gomega.And(
				gomega.HaveField("Kind", MutationScopeParentDirectoryCreation),
				gomega.HaveField("Source", source),
			)))
			result := manager.Execute(ctx, ToolRequest{Tool: ToolNameWriteFile, Path: target[0], Content: "package generated\n"})
			gomega.Expect(result.Error).To(gomega.BeEmpty(), fmt.Sprintf("absent exact parent result: %#v", result))
			gomega.Expect(result.CommitReceipt().VerifiablePaths()).To(gomega.Equal([]string{target[0]}))
		}
	})

	ginkgo.It("uses whole-contract descendant roles for an absent required directory", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx, scope, err := manager.BindMutationScope(context.Background(), MutationScopePaths{
			RequiredPaths: []string{"generated", "generated/a.go"},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(scope.RequiredCoverage).To(gomega.HaveLen(1))
		gomega.Expect(scope.RequiredCoverage[0].CanonicalID).To(gomega.Equal("generated/a.go"))
		gomega.Expect(scope.Rules).To(gomega.ContainElement(gomega.And(
			gomega.HaveField("Kind", MutationScopeDirectorySubtree),
			gomega.HaveField("Identity.CanonicalID", "generated"),
		)))

		mkdir := manager.Execute(ctx, ToolRequest{Tool: ToolNameMkdir, Path: "generated"})
		gomega.Expect(mkdir.Error).To(gomega.BeEmpty(), fmt.Sprintf("typed directory mkdir result: %#v", mkdir))
		write := manager.Execute(ctx, ToolRequest{Tool: ToolNameWriteFile, Path: "generated/a.go", Content: "package generated\n"})
		gomega.Expect(write.Error).To(gomega.BeEmpty(), fmt.Sprintf("typed descendant write result: %#v", write))
	})

	ginkgo.It("infers an absent directory from descendants across contract path sets", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx, scope, err := manager.BindMutationScope(context.Background(), MutationScopePaths{
			RequiredPaths:      []string{"generated"},
			AllowedPaths:       []string{"generated/optional.go"},
			AuthorizedNewFiles: []string{"generated/new.go"},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(scope.RequiredCoverage).To(gomega.BeEmpty(), "cross-set directory inference became exact-file coverage")
		gomega.Expect(scope.Rules).To(gomega.ContainElement(gomega.And(
			gomega.HaveField("Kind", MutationScopeDirectorySubtree),
			gomega.HaveField("Source", MutationScopeRequiredPath),
			gomega.HaveField("Identity.CanonicalID", "generated"),
		)))

		write := manager.Execute(ctx, ToolRequest{Tool: ToolNameWriteFile, Path: "generated/optional.go", Content: "package generated\n"})
		gomega.Expect(write.Error).To(gomega.BeEmpty(), fmt.Sprintf("cross-set descendant write result: %#v", write))
	})

	ginkgo.It("fails closed on Ask without an invocation approver and binds approved canonical targets", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "protected.txt"), "before\n")
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		manager.SetPermissionRules(map[string][]toolpolicy.Rule{
			ToolNameWriteFile: {{Pattern: "protected.txt", Decision: toolpolicy.Ask}},
		})
		req := ToolRequest{Tool: ToolNameWriteFile, Path: "protected.txt", Content: "after\n"}

		denied := manager.Execute(context.Background(), req)
		gomega.Expect(denied.Error).To(gomega.ContainSubstring("invocation-bound mutation approver"))
		gomega.Expect(readTestFile(filepath.Join(root, "protected.txt"))).To(gomega.Equal("before\n"))

		approver := NewMockMutationApprover(gomock.NewController(ginkgo.GinkgoT()))
		var approvalRequest MutationApprovalRequest
		approver.EXPECT().ApproveWorkspaceMutation(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, req MutationApprovalRequest) (MutationApprovalDecision, error) {
			approvalRequest = req
			return MutationApprovalDecision{Approved: true}, nil
		})
		approved := manager.Execute(WithMutationApprover(context.Background(), approver), req)
		gomega.Expect(approved.Error).To(gomega.BeEmpty(), fmt.Sprintf("approved result: %#v", approved))
		gomega.Expect(approvalRequest.Targets).To(gomega.HaveLen(1))
		gomega.Expect(approvalRequest.Targets[0].Identity.CanonicalID).To(gomega.Equal("protected.txt"))
	})

	ginkgo.It("keeps approvers invocation-bound across sessions while allowing explicit context reuse", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		manager.SetPermissionRules(map[string][]toolpolicy.Rule{
			ToolNameWriteFile: {{Pattern: "*", Decision: toolpolicy.Ask}},
		})
		approver := NewMockMutationApprover(gomock.NewController(ginkgo.GinkgoT()))
		var approvalRequests []MutationApprovalRequest
		approver.EXPECT().ApproveWorkspaceMutation(gomock.Any(), gomock.Any()).Times(2).DoAndReturn(func(_ context.Context, req MutationApprovalRequest) (MutationApprovalDecision, error) {
			approvalRequests = append(approvalRequests, req)
			return MutationApprovalDecision{Approved: true}, nil
		})
		sessionA := WithExecutionSessionID(WithMutationApprover(context.Background(), approver), "session-a")

		first := manager.Execute(sessionA, ToolRequest{Tool: ToolNameWriteFile, Path: "a.txt", Content: "a\n"})
		gomega.Expect(first.Error).To(gomega.BeEmpty(), fmt.Sprintf("session A first result: %#v", first))
		withoutApprover := manager.Execute(WithExecutionSessionID(context.Background(), "session-b"), ToolRequest{Tool: ToolNameWriteFile, Path: "b.txt", Content: "b\n"})
		gomega.Expect(withoutApprover.Error).To(gomega.ContainSubstring("invocation-bound mutation approver"))
		reused := manager.Execute(sessionA, ToolRequest{Tool: ToolNameWriteFile, Path: "c.txt", Content: "c\n"})
		gomega.Expect(reused.Error).To(gomega.BeEmpty(), fmt.Sprintf("session A reused context result: %#v", reused))
		gomega.Expect(approvalRequests).To(gomega.HaveLen(2))
		gomega.Expect(approvalRequests[0].SessionID).To(gomega.Equal("session-a"))
		gomega.Expect(approvalRequests[1].SessionID).To(gomega.Equal("session-a"))
		_, statErr := os.Stat(filepath.Join(root, "b.txt"))
		gomega.Expect(os.IsNotExist(statErr)).To(gomega.BeTrue())
	})

	ginkgo.It("rejects direct mutation calls without a receipt and receipt mismatch or replay", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "append.txt"), "base\n")
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})

		absent := manager.writeFile(context.Background(), ToolRequest{Tool: ToolNameAppendFile, Path: "append.txt", Content: "absent\n"})
		gomega.Expect(absent.Error).To(gomega.ContainSubstring("authorization receipt is absent"))

		req := ToolRequest{Tool: ToolNameAppendFile, Path: "append.txt", Content: "once\n"}
		ctx := manager.snapshotExecutionSession(context.Background())
		ctx = manager.snapshotMutationApprover(ctx)
		ctx, authorizationFailure := manager.authorizeMutationRequest(ctx, req)
		gomega.Expect(authorizationFailure).To(gomega.BeNil())
		first := manager.writeFile(ctx, req)
		gomega.Expect(first.Error).To(gomega.BeEmpty(), fmt.Sprintf("first authorized append: %#v", first))
		replayed := manager.writeFile(ctx, req)
		gomega.Expect(replayed.Error).To(gomega.ContainSubstring("already consumed"))
		gomega.Expect(readTestFile(filepath.Join(root, "append.txt"))).To(gomega.Equal("base\nonce\n"))

		requestA := ToolRequest{Tool: ToolNameWriteFile, Path: "a.txt", Content: "a\n"}
		requestB := ToolRequest{Tool: ToolNameWriteFile, Path: "b.txt", Content: "b\n"}
		mismatchCtx := manager.snapshotExecutionSession(context.Background())
		mismatchCtx = manager.snapshotMutationApprover(mismatchCtx)
		mismatchCtx, authorizationFailure = manager.authorizeMutationRequest(mismatchCtx, requestA)
		gomega.Expect(authorizationFailure).To(gomega.BeNil())
		mismatch := manager.writeFile(mismatchCtx, requestB)
		gomega.Expect(mismatch.Error).To(gomega.ContainSubstring("does not match authorization receipt"))
		afterMismatch := manager.writeFile(mismatchCtx, requestA)
		gomega.Expect(afterMismatch.Error).To(gomega.ContainSubstring("already consumed"))
		for _, name := range []string{"a.txt", "b.txt"} {
			_, statErr := os.Stat(filepath.Join(root, name))
			gomega.Expect(os.IsNotExist(statErr)).To(gomega.BeTrue(), fmt.Sprintf("mismatched receipt wrote %s: %v", name, statErr))
		}

		scopedRequest := ToolRequest{Tool: ToolNameWriteFile, Path: "scoped.txt", Content: "scoped\n"}
		scopeCtx, _, scopeErr := manager.BindMutationScope(context.Background(), MutationScopePaths{RequiredPaths: []string{"scoped.txt"}})
		gomega.Expect(scopeErr).NotTo(gomega.HaveOccurred())
		scopeCtx = manager.snapshotExecutionSession(scopeCtx)
		scopeCtx = manager.snapshotMutationApprover(scopeCtx)
		scopeCtx, authorizationFailure = manager.authorizeMutationRequest(scopeCtx, scopedRequest)
		gomega.Expect(authorizationFailure).To(gomega.BeNil())
		changedScopeCtx, _, scopeErr := manager.BindMutationScope(scopeCtx, MutationScopePaths{RequiredPaths: []string{"different.txt"}})
		gomega.Expect(scopeErr).NotTo(gomega.HaveOccurred())
		scopeMismatch := manager.writeFile(changedScopeCtx, scopedRequest)
		gomega.Expect(scopeMismatch.Error).To(gomega.ContainSubstring("mutation scope does not match authorization receipt"))
		_, statErr := os.Stat(filepath.Join(root, "scoped.txt"))
		gomega.Expect(os.IsNotExist(statErr)).To(gomega.BeTrue())
	})
})
