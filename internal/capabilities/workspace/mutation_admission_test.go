package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/config"
)

var _ = ginkgo.Describe("non-executing mutation admission", func() {
	ginkgo.It("classifies unbound write-file targets from the live rooted preimage", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "existing.go"), "package existing\n")
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})

		for _, test := range []struct {
			path     string
			preimage MutationPreimageClass
		}{
			{path: "nested/new.go", preimage: MutationPreimageAbsent},
			{path: "existing.go", preimage: MutationPreimageRequired},
		} {
			assessment := manager.AssessMutationAdmission(context.Background(), ToolRequest{
				Tool: ToolNameWriteFile, Path: test.path, Content: "package replacement\n", Description: "write package",
			})
			gomega.Expect(assessment.Disposition).To(gomega.Equal(MutationAdmissionAdmittedWithoutApproval), test.path)
			gomega.Expect(assessment.Analysis.Targets).To(gomega.HaveLen(1), test.path)
			target := assessment.Analysis.Targets[0]
			gomega.Expect(target.Identity.CanonicalID).To(gomega.Equal(test.path), test.path)
			gomega.Expect(target.Preimage).To(gomega.Equal(test.preimage), test.path)
		}
	})

	ginkgo.It("reclassifies write-file target existence on every assessment", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		req := ToolRequest{
			Tool: ToolNameWriteFile, Path: "target.go", Content: "package target\n", Description: "write package",
		}

		missing := manager.AssessMutationAdmission(context.Background(), req)
		gomega.Expect(missing.Disposition).To(gomega.Equal(MutationAdmissionAdmittedWithoutApproval))
		gomega.Expect(missing.Analysis.Targets).To(gomega.HaveLen(1))
		gomega.Expect(missing.Analysis.Targets[0].Preimage).To(gomega.Equal(MutationPreimageAbsent))

		writeTestFile(filepath.Join(root, "target.go"), "package existing\n")
		existing := manager.AssessMutationAdmission(context.Background(), req)
		gomega.Expect(existing.Disposition).To(gomega.Equal(MutationAdmissionAdmittedWithoutApproval))
		gomega.Expect(existing.Analysis.Targets).To(gomega.HaveLen(1))
		gomega.Expect(existing.Analysis.Targets[0].Preimage).To(gomega.Equal(MutationPreimageRequired))
	})

	ginkgo.It("carries the admitted write-file preimage into execution receipts", func() {
		for _, test := range []struct {
			name     string
			exists   bool
			preimage MutationPreimageClass
		}{
			{name: "missing", preimage: MutationPreimageAbsent},
			{name: "existing", exists: true, preimage: MutationPreimageRequired},
		} {
			root := ginkgo.GinkgoT().TempDir()
			if test.exists {
				writeTestFile(filepath.Join(root, "target.txt"), "before\n")
			}
			manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
			result := manager.Execute(context.Background(), ToolRequest{
				Tool: ToolNameWriteFile, Path: "target.txt", Content: "after\n", Description: "write target",
			})
			gomega.Expect(result.Error).To(gomega.BeEmpty(), test.name)
			receipt := result.CommitReceipt()
			gomega.Expect(receipt).NotTo(gomega.BeNil(), test.name)
			gomega.Expect(receipt.Targets).To(gomega.HaveLen(1), test.name)
			gomega.Expect(receipt.Targets[0].Preimage).To(gomega.Equal(test.preimage), test.name)
			gomega.Expect(receipt.Commits).To(gomega.HaveLen(1), test.name)
			gomega.Expect(receipt.Commits[0].Preimage.Class).To(gomega.Equal(test.preimage), test.name)
			gomega.Expect(receipt.Commits[0].Preimage.Exists).To(gomega.Equal(test.exists), test.name)
		}
	})

	ginkgo.It("classifies internal aliases by physical identity and denies bound alias retargeting", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "first"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.MkdirAll(filepath.Join(root, "second"), 0o755)).To(gomega.Succeed())
		alias := filepath.Join(root, "alias")
		if err := os.Symlink("first", alias); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx, _, err := manager.BindMutationScope(context.Background(), MutationScopePaths{AuthorizedNewFiles: []string{"alias/new.go"}})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		req := ToolRequest{
			Tool: ToolNameWriteFile, Path: "alias/new.go", Content: "package generated\n", Description: "write generated package",
		}

		missing := manager.AssessMutationAdmission(ctx, req)
		gomega.Expect(missing.Disposition).To(gomega.Equal(MutationAdmissionAdmittedWithoutApproval))
		gomega.Expect(missing.Analysis.Targets).To(gomega.HaveLen(1))
		gomega.Expect(missing.Analysis.Targets[0].Identity.CanonicalID).To(gomega.Equal("first/new.go"))
		gomega.Expect(missing.Analysis.Targets[0].Preimage).To(gomega.Equal(MutationPreimageAbsent))

		gomega.Expect(os.Remove(alias)).To(gomega.Succeed())
		gomega.Expect(os.Symlink("second", alias)).To(gomega.Succeed())
		writeTestFile(filepath.Join(root, "second", "new.go"), "package existing\n")
		retargeted := manager.AssessMutationAdmission(ctx, req)
		gomega.Expect(retargeted.Disposition).To(gomega.Equal(MutationAdmissionDenied))
		gomega.Expect(retargeted.Analysis.Targets).To(gomega.HaveLen(1))
		gomega.Expect(retargeted.Analysis.Targets[0].Identity.CanonicalID).To(gomega.Equal("second/new.go"))
		gomega.Expect(retargeted.Analysis.Targets[0].Preimage).To(gomega.Equal(MutationPreimageRequired))
	})

	ginkgo.It("does not classify unsafe symlink presentations as absent", func() {
		root := ginkgo.GinkgoT().TempDir()
		outside := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(outside, "outside.go"), "package outside\n")
		if err := os.Symlink(filepath.Join(outside, "outside.go"), filepath.Join(root, "outside.go")); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}
		gomega.Expect(os.Symlink("missing.go", filepath.Join(root, "dangling.go"))).To(gomega.Succeed())
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})

		for _, path := range []string{"outside.go", "dangling.go"} {
			assessment := manager.AssessMutationAdmission(context.Background(), ToolRequest{
				Tool: ToolNameWriteFile, Path: path, Content: "package unsafe\n", Description: "write package",
			})
			gomega.Expect(assessment.Disposition).To(gomega.Equal(MutationAdmissionInvalid), path)
			if len(assessment.Analysis.Targets) > 0 {
				gomega.Expect(assessment.Analysis.Targets[0].Preimage).NotTo(gomega.Equal(MutationPreimageAbsent), path)
			}
		}
	})

	ginkgo.It("keeps authorized-new-file absence bound to the admitted live fact", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx, _, err := manager.BindMutationScope(context.Background(), MutationScopePaths{AuthorizedNewFiles: []string{"generated/new.go"}})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		req := ToolRequest{
			Tool: ToolNameWriteFile, Path: "generated/new.go", Content: "package generated\n", Description: "write generated package",
		}

		missing := manager.AssessMutationAdmission(ctx, req)
		gomega.Expect(missing.Disposition).To(gomega.Equal(MutationAdmissionAdmittedWithoutApproval))
		gomega.Expect(missing.Analysis.Targets).To(gomega.HaveLen(1))
		gomega.Expect(missing.Analysis.Targets[0].Preimage).To(gomega.Equal(MutationPreimageAbsent))

		writeTestFile(filepath.Join(root, "generated", "new.go"), "package external\n")
		appeared := manager.AssessMutationAdmission(ctx, req)
		gomega.Expect(appeared.Disposition).To(gomega.Equal(MutationAdmissionDenied))
		gomega.Expect(appeared.Analysis.Targets).To(gomega.HaveLen(1))
		gomega.Expect(appeared.Analysis.Targets[0].Preimage).To(gomega.Equal(MutationPreimageRequired))
	})

	ginkgo.It("admits a live bound target without minting execution authority", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "target.go"), "package sample\n")
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx, _, err := manager.BindMutationScope(context.Background(), MutationScopePaths{RequiredPaths: []string{"target.go"}})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		req := ToolRequest{
			Tool: ToolNameEditFile, Path: "target.go", OldText: "package sample", NewText: "package changed", Description: "rename package",
		}

		assessment := manager.AssessMutationAdmission(ctx, req)
		gomega.Expect(assessment.Disposition).To(gomega.Equal(MutationAdmissionAdmittedWithoutApproval))
		gomega.Expect(assessment.Analysis.Targets).To(gomega.HaveLen(1))
		gomega.Expect(assessment.Analysis.Targets[0].Identity.CanonicalID).To(gomega.Equal("target.go"))

		withoutReceipt := manager.editFile(ctx, req)
		gomega.Expect(withoutReceipt.Error).To(gomega.ContainSubstring("authorization receipt is absent"))
		gomega.Expect(readTestFile(filepath.Join(root, "target.go"))).To(gomega.Equal("package sample\n"))
	})

	ginkgo.It("distinguishes non-mutation invalid denied and approval-required requests", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "target.go"), "package sample\n")
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})

		nonMutation := manager.AssessMutationAdmission(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: "target.go"})
		gomega.Expect(nonMutation.Disposition).To(gomega.Equal(MutationAdmissionNotMutation))

		invalid := manager.AssessMutationAdmission(context.Background(), ToolRequest{
			Tool: ToolNameApplyPatch, Content: "*** Begin Patch\n*** Update File: target.go\nmalformed\n*** End Patch",
		})
		gomega.Expect(invalid.Disposition).To(gomega.Equal(MutationAdmissionInvalid))

		emptyScope, _, err := manager.BindMutationScope(context.Background(), MutationScopePaths{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		denied := manager.AssessMutationAdmission(emptyScope, ToolRequest{
			Tool: ToolNameEditFile, Path: "target.go", OldText: "sample", NewText: "changed", Description: "rename package",
		})
		gomega.Expect(denied.Disposition).To(gomega.Equal(MutationAdmissionDenied))

		manager.SetProtectedWritePaths([]string{"target.go"})
		approval := manager.AssessMutationAdmission(context.Background(), ToolRequest{
			Tool: ToolNameWriteFile, Path: "target.go", Content: "package changed\n", Description: "replace package",
		})
		gomega.Expect(approval.Disposition).To(gomega.Equal(MutationAdmissionApprovalRequired))
	})

	ginkgo.It("keeps unbound and explicitly empty scope authority distinct", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "target.go"), "package sample\n")
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		req := ToolRequest{
			Tool: ToolNameEditFile, Path: "target.go", OldText: "sample", NewText: "changed", Description: "rename package",
		}

		unbound := manager.AssessMutationAdmission(context.Background(), req)
		gomega.Expect(unbound.Disposition).To(gomega.Equal(MutationAdmissionAdmittedWithoutApproval))

		emptyScope, _, err := manager.BindMutationScope(context.Background(), MutationScopePaths{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		explicitlyEmpty := manager.AssessMutationAdmission(emptyScope, req)
		gomega.Expect(explicitlyEmpty.Disposition).To(gomega.Equal(MutationAdmissionDenied))
	})

	ginkgo.It("does not invoke approval or mint a receipt before Execute reauthorizes", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		manager.SetPermissionRules(map[string][]toolpolicy.Rule{
			ToolNameWriteFile: {{Pattern: "approved.txt", Decision: toolpolicy.Ask}},
		})
		approver := NewMockMutationApprover(gomock.NewController(ginkgo.GinkgoT()))
		approver.EXPECT().ApproveWorkspaceMutation(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, request MutationApprovalRequest) (MutationApprovalDecision, error) {
			gomega.Expect(request.Targets).To(gomega.HaveLen(1))
			gomega.Expect(request.Targets[0].Identity.CanonicalID).To(gomega.Equal("approved.txt"))
			gomega.Expect(request.Targets[0].Preimage).To(gomega.Equal(MutationPreimageAbsent))
			return MutationApprovalDecision{Approved: true}, nil
		}).Times(1)
		ctx := WithExecutionSessionID(WithMutationApprover(context.Background(), approver), "session-a")
		req := ToolRequest{Tool: ToolNameWriteFile, Path: "approved.txt", Content: "approved\n", Description: "write approved file"}

		assessment := manager.AssessMutationAdmission(ctx, req)
		gomega.Expect(assessment.Disposition).To(gomega.Equal(MutationAdmissionApprovalRequired))
		withoutReceipt := manager.writeFile(ctx, req)
		gomega.Expect(withoutReceipt.Error).To(gomega.ContainSubstring("authorization receipt is absent"))
		_, statErr := os.Stat(filepath.Join(root, "approved.txt"))
		gomega.Expect(os.IsNotExist(statErr)).To(gomega.BeTrue())

		executed := manager.Execute(ctx, req)
		gomega.Expect(executed.Error).To(gomega.BeEmpty(), fmt.Sprintf("reauthorized execution result: %#v", executed))
		gomega.Expect(readTestFile(filepath.Join(root, "approved.txt"))).To(gomega.Equal("approved\n"))
	})

	ginkgo.It("denies configured mode write-disabled and explore authority", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "target.go"), "package sample\n")
		req := ToolRequest{
			Tool: ToolNameEditFile, Path: "target.go", OldText: "sample", NewText: "changed", Description: "rename package",
		}

		configured := NewManager(config.Workspace{
			Root: root, CWD: root, EnableWrites: true,
			ToolRules: map[string][]config.ToolRule{ToolNameEditFile: {{Pattern: "target.go", Decision: "deny"}}},
		})
		gomega.Expect(configured.AssessMutationAdmission(context.Background(), req).Disposition).To(gomega.Equal(MutationAdmissionDenied))

		modeDenied := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		modeDenied.SetPermissionRules(map[string][]toolpolicy.Rule{
			ToolNameEditFile: {{Pattern: "target.go", Decision: toolpolicy.Deny}},
		})
		gomega.Expect(modeDenied.AssessMutationAdmission(context.Background(), req).Disposition).To(gomega.Equal(MutationAdmissionDenied))

		writesDisabled := NewManager(config.Workspace{Root: root, CWD: root})
		gomega.Expect(writesDisabled.AssessMutationAdmission(context.Background(), req).Disposition).To(gomega.Equal(MutationAdmissionDenied))

		explore := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		gomega.Expect(explore.SetMode(ModeExplore)).To(gomega.Succeed())
		gomega.Expect(explore.AssessMutationAdmission(context.Background(), req).Disposition).To(gomega.Equal(MutationAdmissionDenied))
	})

	ginkgo.It("fails closed when the bound workspace root identity changes", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "target.go"), "package sample\n")
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx, _, err := manager.BindMutationScope(context.Background(), MutationScopePaths{RequiredPaths: []string{"target.go"}})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		oldRoot := root + "-replaced"
		gomega.Expect(os.Rename(root, oldRoot)).To(gomega.Succeed())
		ginkgo.DeferCleanup(os.RemoveAll, oldRoot)
		gomega.Expect(os.Mkdir(root, 0o755)).To(gomega.Succeed())
		writeTestFile(filepath.Join(root, "target.go"), "package replacement\n")

		assessment := manager.AssessMutationAdmission(ctx, ToolRequest{
			Tool: ToolNameEditFile, Path: "target.go", OldText: "replacement", NewText: "changed", Description: "rename package",
		})
		gomega.Expect(assessment.Disposition).To(gomega.Equal(MutationAdmissionDenied))
	})
})
