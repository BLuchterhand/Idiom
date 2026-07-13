package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/platform/config"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("workspace-owned mutation rollback", ginkgo.Label("development-scenario", "integration", "race-contract"), func() {
	ginkgo.It("restores one compound non-root mutation from its rooted commit receipt", func() {
		root := ginkgo.GinkgoT().TempDir()
		workdir := filepath.Join(root, "subdir")
		gomega.Expect(os.Mkdir(workdir, 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(workdir, "existing.txt"), []byte("old\n"), 0o640)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(workdir, "delete.txt"), []byte("doomed\n"), 0o600)).To(gomega.Succeed())
		gomega.Expect(os.Chmod(filepath.Join(workdir, "existing.txt"), 0o640)).To(gomega.Succeed())
		gomega.Expect(os.Chmod(filepath.Join(workdir, "delete.txt"), 0o600)).To(gomega.Succeed())
		initialInfo, err := os.Stat(filepath.Join(workdir, "existing.txt"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(initialInfo.Mode().Perm()).To(gomega.Equal(os.FileMode(0o640)))

		manager := NewManager(config.Workspace{Root: root, CWD: workdir, EnableWrites: true})
		ctx := WithExecutionSessionID(context.Background(), "rollback-session")
		result := manager.Execute(ctx, ToolRequest{
			Tool:        ToolNameApplyPatch,
			Description: "exercise compound rollback",
			Content: "*** Begin Patch\n" +
				"*** Update File: existing.txt\n" +
				"@@\n-old\n+new\n" +
				"*** Add File: generated/new.txt\n+created\n" +
				"*** Delete File: delete.txt\n" +
				"*** End Patch",
		})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), result.Error)
		receipt := result.CommitReceipt()
		gomega.Expect(receipt).NotTo(gomega.BeNil())
		changes := receipt.FileChanges()
		gomega.Expect(changes).To(gomega.HaveLen(3))
		changesByPath := make(map[string]MutationFileChange, len(changes))
		for _, change := range changes {
			changesByPath[change.CanonicalID()] = change
			serializedChange, marshalErr := json.Marshal(change)
			gomega.Expect(marshalErr).NotTo(gomega.HaveOccurred())
			gomega.Expect(string(serializedChange)).NotTo(gomega.ContainSubstring("old"))
			gomega.Expect(string(serializedChange)).NotTo(gomega.ContainSubstring("doomed"))
			gomega.Expect(string(serializedChange)).NotTo(gomega.ContainSubstring("created"))
			gomega.Expect(string(serializedChange)).NotTo(gomega.ContainSubstring(root))
		}
		before, existed := changesByPath["subdir/existing.txt"].BeforeBody()
		gomega.Expect(existed).To(gomega.BeTrue())
		gomega.Expect(before).To(gomega.Equal([]byte("old\n")))
		after, exists := changesByPath["subdir/existing.txt"].AfterBody()
		gomega.Expect(exists).To(gomega.BeTrue())
		gomega.Expect(after).To(gomega.Equal([]byte("new\n")))
		before[0] = 'x'
		beforeAgain, _ := receipt.FileChanges()[0].BeforeBody()
		gomega.Expect(string(beforeAgain)).NotTo(gomega.ContainSubstring("xld"))
		createdBefore, existed := changesByPath["subdir/generated/new.txt"].BeforeBody()
		gomega.Expect(existed).To(gomega.BeFalse())
		gomega.Expect(createdBefore).To(gomega.BeEmpty())
		createdAfter, exists := changesByPath["subdir/generated/new.txt"].AfterBody()
		gomega.Expect(exists).To(gomega.BeTrue())
		gomega.Expect(createdAfter).To(gomega.Equal([]byte("created\n")))
		removedAfter, exists := changesByPath["subdir/delete.txt"].AfterBody()
		gomega.Expect(exists).To(gomega.BeFalse())
		gomega.Expect(removedAfter).To(gomega.BeEmpty())
		rollback, err := receipt.Rollback()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(rollback).NotTo(gomega.BeNil())
		gomega.Expect(rollback.Paths()).To(gomega.ConsistOf(
			"subdir/existing.txt",
			"subdir/generated",
			"subdir/generated/new.txt",
			"subdir/delete.txt",
		))

		serialized, err := json.Marshal(rollback)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(serialized)).NotTo(gomega.ContainSubstring(root))
		gomega.Expect(string(serialized)).NotTo(gomega.ContainSubstring("old"))
		gomega.Expect(string(serialized)).NotTo(gomega.ContainSubstring("doomed"))
		paths := rollback.Paths()
		paths[0] = "tampered"
		gomega.Expect(rollback.Paths()).NotTo(gomega.ContainElement("tampered"))
		for _, entry := range rollback.state.entries {
			if entry.canonicalID == "subdir/existing.txt" {
				gomega.Expect(entry.preimage.info).NotTo(gomega.BeNil())
				gomega.Expect(entry.preimage.info.Mode().Perm()).To(gomega.Equal(os.FileMode(0o640)))
			}
		}

		rollbackResult, err := manager.RollbackMutation(ctx, rollback)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(rollbackResult.Status).To(gomega.Equal(MutationRollbackComplete))
		gomega.Expect(rollbackResult.PendingPaths).To(gomega.BeEmpty())
		serializedResult, err := json.Marshal(rollbackResult)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(serializedResult)).NotTo(gomega.ContainSubstring(root))
		gomega.Expect(string(serializedResult)).NotTo(gomega.ContainSubstring("old"))
		gomega.Expect(string(serializedResult)).NotTo(gomega.ContainSubstring("doomed"))
		existing, err := os.ReadFile(filepath.Join(workdir, "existing.txt"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(existing).To(gomega.Equal([]byte("old\n")))
		info, err := os.Stat(filepath.Join(workdir, "existing.txt"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(info.Mode().Perm()).To(gomega.Equal(os.FileMode(0o640)))
		deleted, err := os.ReadFile(filepath.Join(workdir, "delete.txt"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(deleted).To(gomega.Equal([]byte("doomed\n")))
		info, err = os.Stat(filepath.Join(workdir, "delete.txt"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(info.Mode().Perm()).To(gomega.Equal(os.FileMode(0o600)))
		_, err = os.Stat(filepath.Join(workdir, "generated", "new.txt"))
		gomega.Expect(errors.Is(err, os.ErrNotExist)).To(gomega.BeTrue())
		_, err = os.Stat(filepath.Join(workdir, "generated"))
		gomega.Expect(errors.Is(err, os.ErrNotExist)).To(gomega.BeTrue())
		clone, err := receipt.Rollback()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		_, err = manager.RollbackMutation(ctx, clone)
		gomega.Expect(err).To(gomega.MatchError(errMutationRollbackConsumed))
	})

	ginkgo.It("fails closed before restoring any path when a committed postimage is stale", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("a0\n"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "b.txt"), []byte("b0\n"), 0o644)).To(gomega.Succeed())
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx := WithExecutionSessionID(context.Background(), "stale-session")
		result := manager.Execute(ctx, ToolRequest{
			Tool:        ToolNameApplyPatch,
			Description: "update both files",
			Content: "*** Begin Patch\n" +
				"*** Update File: a.txt\n@@\n-a0\n+a1\n" +
				"*** Update File: b.txt\n@@\n-b0\n+b1\n" +
				"*** End Patch",
		})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), result.Error)
		rollback, err := result.CommitReceipt().Rollback()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(rollback).NotTo(gomega.BeNil())
		gomega.Expect(os.WriteFile(filepath.Join(root, "b.txt"), []byte("external\n"), 0o644)).To(gomega.Succeed())

		rollbackResult, err := manager.RollbackMutation(ctx, rollback)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("changed after commit")))
		gomega.Expect(rollbackResult.Status).To(gomega.Equal(MutationRollbackRejectedStale))
		a, readErr := os.ReadFile(filepath.Join(root, "a.txt"))
		gomega.Expect(readErr).NotTo(gomega.HaveOccurred())
		gomega.Expect(a).To(gomega.Equal([]byte("a1\n")), "validation must finish for every postimage before restoration begins")
		b, readErr := os.ReadFile(filepath.Join(root, "b.txt"))
		gomega.Expect(readErr).NotTo(gomega.HaveOccurred())
		gomega.Expect(b).To(gomega.Equal([]byte("external\n")))
		_, err = manager.RollbackMutation(ctx, rollback)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("changed after commit")))
	})

	ginkgo.It("does not consume rollback authority when its context is canceled while waiting for the mutation gate", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx := WithExecutionSessionID(context.Background(), "cancel-session")
		result := manager.Execute(ctx, ToolRequest{Tool: ToolNameWriteFile, Path: "created.txt", Content: "created\n", Description: "create file"})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), result.Error)
		rollback, err := result.CommitReceipt().Rollback()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(rollback).NotTo(gomega.BeNil())

		gomega.Expect(manager.pathAccess.mutationGate.Lock(context.Background(), nil)).To(gomega.Succeed())
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		_, err = manager.RollbackMutation(canceled, rollback)
		gomega.Expect(err).To(gomega.MatchError(context.Canceled))
		manager.pathAccess.mutationGate.Unlock()

		rollbackResult, err := manager.RollbackMutation(ctx, rollback)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(rollbackResult.Status).To(gomega.Equal(MutationRollbackComplete))
		_, err = os.Stat(filepath.Join(root, "created.txt"))
		gomega.Expect(errors.Is(err, os.ErrNotExist)).To(gomega.BeTrue())
	})

	ginkgo.It("rejects another manager, invocation, and symlink-retargeted path without touching the external target", func() {
		root := ginkgo.GinkgoT().TempDir()
		external := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.Mkdir(filepath.Join(root, "dir"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "dir", "target.txt"), []byte("before\n"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(external, "target.txt"), []byte("external\n"), 0o644)).To(gomega.Succeed())
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx := WithExecutionSessionID(context.Background(), "owner-session")
		result := manager.Execute(ctx, ToolRequest{Tool: ToolNameWriteFile, Path: "dir/target.txt", Content: "after\n", Description: "replace target"})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), result.Error)
		rollback, err := result.CommitReceipt().Rollback()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(rollback).NotTo(gomega.BeNil())

		other := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		_, err = other.RollbackMutation(ctx, rollback)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("different manager")))
		_, err = manager.RollbackMutation(WithExecutionSessionID(context.Background(), "other-session"), rollback)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("does not match")))
		_, err = manager.RollbackMutation(WithExecutionSessionID(context.Background(), ""), rollback)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("does not match")))

		gomega.Expect(os.RemoveAll(filepath.Join(root, "dir"))).To(gomega.Succeed())
		if err := os.Symlink(external, filepath.Join(root, "dir")); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}
		_, err = manager.RollbackMutation(ctx, rollback)
		gomega.Expect(err).To(gomega.HaveOccurred())
		gomega.Expect(strings.ToLower(err.Error())).To(gomega.Or(
			gomega.ContainSubstring("symlink"),
			gomega.ContainSubstring("different"),
			gomega.ContainSubstring("changed"),
			gomega.ContainSubstring("stale"),
		))
		externalBody, readErr := os.ReadFile(filepath.Join(external, "target.txt"))
		gomega.Expect(readErr).NotTo(gomega.HaveOccurred())
		gomega.Expect(externalBody).To(gomega.Equal([]byte("external\n")))
	})

	ginkgo.It("rejects a swapped workspace root without leaking its absolute path", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx := WithExecutionSessionID(context.Background(), "root-session")
		result := manager.Execute(ctx, ToolRequest{Tool: ToolNameWriteFile, Path: "created.txt", Content: "new\n", Description: "create target"})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), result.Error)
		rollback, err := result.CommitReceipt().Rollback()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		oldRoot := root + "-old"
		ginkgo.DeferCleanup(func() { _ = os.RemoveAll(oldRoot) })
		gomega.Expect(os.Rename(root, oldRoot)).To(gomega.Succeed())
		gomega.Expect(os.Mkdir(root, 0o755)).To(gomega.Succeed())

		rollbackResult, err := manager.RollbackMutation(ctx, rollback)
		gomega.Expect(err).To(gomega.HaveOccurred())
		gomega.Expect(rollbackResult.Status).To(gomega.Equal(MutationRollbackFailedBeforeCommit))
		gomega.Expect(err.Error()).NotTo(gomega.ContainSubstring(root))
	})

	ginkgo.It("refuses rollback authority when the committed path is replaced before receipt capture", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx := WithExecutionSessionID(context.Background(), "receipt-race-session")
		replaced := false
		ctx = withWorkspaceMutationCheckpoint(ctx, func(checkpoint workspaceMutationCheckpoint) {
			if replaced || checkpoint.Stage != workspaceMutationBeforeReceipt || checkpoint.Path != "target.txt" {
				return
			}
			replaced = true
			path := filepath.Join(root, "target.txt")
			gomega.Expect(os.Remove(path)).To(gomega.Succeed())
			gomega.Expect(os.WriteFile(path, []byte("committed\n"), 0o644)).To(gomega.Succeed())
		})
		result := manager.Execute(ctx, ToolRequest{Tool: ToolNameWriteFile, Path: "target.txt", Content: "committed\n", Description: "write target"})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), result.Error)
		gomega.Expect(result.ChangedPaths).To(gomega.Equal([]string{"target.txt"}))
		rollback, err := result.CommitReceipt().Rollback()
		gomega.Expect(rollback).To(gomega.BeNil())
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("postimage changed before receipt capture")))
		body, readErr := os.ReadFile(filepath.Join(root, "target.txt"))
		gomega.Expect(readErr).NotTo(gomega.HaveOccurred())
		gomega.Expect(body).To(gomega.Equal([]byte("committed\n")))
	})

	ginkgo.It("reverts only the harness append segment and preserves an earlier commuting append", func() {
		root := ginkgo.GinkgoT().TempDir()
		path := filepath.Join(root, "events.log")
		gomega.Expect(os.WriteFile(path, []byte("old\n"), 0o644)).To(gomega.Succeed())
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx := WithExecutionSessionID(context.Background(), "append-session")
		injected := false
		ctx = withWorkspaceMutationCheckpoint(ctx, func(checkpoint workspaceMutationCheckpoint) {
			if injected || checkpoint.Stage != workspaceMutationAfterParentBind || checkpoint.Path != "events.log" {
				return
			}
			injected = true
			file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			_, err = file.WriteString("external\n")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(file.Close()).To(gomega.Succeed())
		})
		result := manager.Execute(ctx, ToolRequest{Tool: ToolNameAppendFile, Path: "events.log", Content: "model\n", Description: "append model event"})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), result.Error)
		body, err := os.ReadFile(path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(body).To(gomega.Equal([]byte("old\nexternal\nmodel\n")))
		changes := result.CommitReceipt().FileChanges()
		gomega.Expect(changes).To(gomega.HaveLen(1))
		before, existed := changes[0].BeforeBody()
		gomega.Expect(existed).To(gomega.BeTrue())
		gomega.Expect(before).To(gomega.Equal([]byte("old\nexternal\n")))
		after, exists := changes[0].AfterBody()
		gomega.Expect(exists).To(gomega.BeTrue())
		gomega.Expect(after).To(gomega.Equal([]byte("old\nexternal\nmodel\n")))
		rollback, err := result.CommitReceipt().Rollback()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		rollbackResult, err := manager.RollbackMutation(ctx, rollback)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(rollbackResult.Effects).To(gomega.ContainElement(MutationRollbackEffect{CanonicalID: "events.log", Kind: MutationRollbackRevertedAppend}))
		body, err = os.ReadFile(path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(body).To(gomega.Equal([]byte("old\nexternal\n")))
	})

	ginkgo.It("rejects an append rollback when another append follows the harness segment", func() {
		root := ginkgo.GinkgoT().TempDir()
		path := filepath.Join(root, "events.log")
		gomega.Expect(os.WriteFile(path, []byte("old\n"), 0o644)).To(gomega.Succeed())
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx := WithExecutionSessionID(context.Background(), "append-stale-session")
		result := manager.Execute(ctx, ToolRequest{Tool: ToolNameAppendFile, Path: "events.log", Content: "model\n", Description: "append model event"})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), result.Error)
		rollback, err := result.CommitReceipt().Rollback()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		_, err = file.WriteString("later\n")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(file.Close()).To(gomega.Succeed())

		rollbackResult, err := manager.RollbackMutation(ctx, rollback)
		gomega.Expect(err).To(gomega.HaveOccurred())
		gomega.Expect(rollbackResult.Status).To(gomega.Equal(MutationRollbackRejectedStale))
		body, readErr := os.ReadFile(path)
		gomega.Expect(readErr).NotTo(gomega.HaveOccurred())
		gomega.Expect(body).To(gomega.Equal([]byte("old\nmodel\nlater\n")))
	})

	ginkgo.It("removes a newly created append target only while its exact committed identity remains", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx := WithExecutionSessionID(context.Background(), "append-new-session")
		result := manager.Execute(ctx, ToolRequest{Tool: ToolNameAppendFile, Path: "new.log", Content: "model\n", Description: "create append target"})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), result.Error)
		rollback, err := result.CommitReceipt().Rollback()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		rolledBack, err := manager.RollbackMutation(ctx, rollback)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(rolledBack.Effects).To(gomega.ContainElement(MutationRollbackEffect{CanonicalID: "new.log", Kind: MutationRollbackRemovedCreatedFile}))
		_, err = os.Stat(filepath.Join(root, "new.log"))
		gomega.Expect(errors.Is(err, os.ErrNotExist)).To(gomega.BeTrue())
	})

	ginkgo.It("resumes after a post-effect failure and ignores cancellation after its first committed reverse effect", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("a0\n"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "b.txt"), []byte("b0\n"), 0o644)).To(gomega.Succeed())
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx := WithExecutionSessionID(context.Background(), "resume-session")
		write := manager.Execute(ctx, ToolRequest{Tool: ToolNameApplyPatch, Description: "update both", Content: "*** Begin Patch\n*** Update File: a.txt\n@@\n-a0\n+a1\n*** Update File: b.txt\n@@\n-b0\n+b1\n*** End Patch"})
		gomega.Expect(write.Error).To(gomega.BeEmpty(), write.Error)
		rollback, err := write.CommitReceipt().Rollback()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		epochAfterWrite := manager.pathAccess.mutationEpoch.Load()

		injected := errors.New("injected post-effect failure")
		partialCtx := withMutationRollbackCheckpoint(ctx, func(checkpoint mutationRollbackCheckpoint) error {
			if checkpoint.EffectCount == 1 {
				return injected
			}
			return nil
		})
		partial, err := manager.RollbackMutation(partialCtx, rollback)
		gomega.Expect(err).To(gomega.MatchError(injected))
		gomega.Expect(partial.Status).To(gomega.Equal(MutationRollbackPartial))
		gomega.Expect(partial.Effects).To(gomega.HaveLen(1))
		gomega.Expect(partial.PendingPaths).To(gomega.HaveLen(1))
		epochAfterPartial := manager.pathAccess.mutationEpoch.Load()
		gomega.Expect(epochAfterPartial).To(gomega.BeNumerically(">", epochAfterWrite), "a partial rollback must invalidate source evidence")

		complete, err := manager.RollbackMutation(ctx, rollback)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(complete.Status).To(gomega.Equal(MutationRollbackComplete))
		gomega.Expect(complete.Effects).To(gomega.HaveLen(2))
		gomega.Expect(manager.pathAccess.mutationEpoch.Load()).To(gomega.BeNumerically(">", epochAfterPartial), "resumed rollback effects must invalidate source evidence again")
		for path, expected := range map[string]string{"a.txt": "a0\n", "b.txt": "b0\n"} {
			body, readErr := os.ReadFile(filepath.Join(root, path))
			gomega.Expect(readErr).NotTo(gomega.HaveOccurred())
			gomega.Expect(string(body)).To(gomega.Equal(expected))
		}

		write = manager.Execute(ctx, ToolRequest{Tool: ToolNameApplyPatch, Description: "update both again", Content: "*** Begin Patch\n*** Update File: a.txt\n@@\n-a0\n+a2\n*** Update File: b.txt\n@@\n-b0\n+b2\n*** End Patch"})
		gomega.Expect(write.Error).To(gomega.BeEmpty(), write.Error)
		rollback, err = write.CommitReceipt().Rollback()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		cancelable, cancel := context.WithCancel(ctx)
		cancelable = withMutationRollbackCheckpoint(cancelable, func(checkpoint mutationRollbackCheckpoint) error {
			if checkpoint.EffectCount == 1 {
				cancel()
			}
			return nil
		})
		complete, err = manager.RollbackMutation(cancelable, rollback)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(complete.Status).To(gomega.Equal(MutationRollbackComplete))
	})

	ginkgo.It("binds absent postimages to the committed parent identity and committed files to inode and mode", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.Mkdir(filepath.Join(root, "dir"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "dir", "delete.txt"), []byte("before\n"), 0o644)).To(gomega.Succeed())
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx := WithExecutionSessionID(context.Background(), "identity-session")
		deleted := manager.Execute(ctx, ToolRequest{Tool: ToolNameApplyPatch, Description: "delete target", Content: "*** Begin Patch\n*** Delete File: dir/delete.txt\n*** End Patch"})
		gomega.Expect(deleted.Error).To(gomega.BeEmpty(), deleted.Error)
		deleteRollback, err := deleted.CommitReceipt().Rollback()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(os.Rename(filepath.Join(root, "dir"), filepath.Join(root, "old-dir"))).To(gomega.Succeed())
		gomega.Expect(os.Mkdir(filepath.Join(root, "dir"), 0o755)).To(gomega.Succeed())
		result, err := manager.RollbackMutation(ctx, deleteRollback)
		gomega.Expect(err).To(gomega.HaveOccurred())
		gomega.Expect(result.Status).To(gomega.Equal(MutationRollbackRejectedStale))
		_, statErr := os.Stat(filepath.Join(root, "dir", "delete.txt"))
		gomega.Expect(errors.Is(statErr, os.ErrNotExist)).To(gomega.BeTrue())

		gomega.Expect(os.WriteFile(filepath.Join(root, "target.txt"), []byte("before\n"), 0o644)).To(gomega.Succeed())
		written := manager.Execute(ctx, ToolRequest{Tool: ToolNameWriteFile, Path: "target.txt", Content: "after\n", Description: "replace target"})
		gomega.Expect(written.Error).To(gomega.BeEmpty(), written.Error)
		writeRollback, err := written.CommitReceipt().Rollback()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(os.Remove(filepath.Join(root, "target.txt"))).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "target.txt"), []byte("after\n"), 0o644)).To(gomega.Succeed())
		result, err = manager.RollbackMutation(ctx, writeRollback)
		gomega.Expect(err).To(gomega.HaveOccurred())
		gomega.Expect(result.Status).To(gomega.Equal(MutationRollbackRejectedStale))
	})

	ginkgo.It("rolls back only the committed subset of a partially failed original mutation", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "a.txt"), []byte("a0\n"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "b.txt"), []byte("b0\n"), 0o644)).To(gomega.Succeed())
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx := WithExecutionSessionID(context.Background(), "partial-origin-session")
		mutatedSecond := false
		mutationCtx := withWorkspaceMutationCheckpoint(ctx, func(checkpoint workspaceMutationCheckpoint) {
			if mutatedSecond || checkpoint.Stage != workspaceMutationAfterParentBind || checkpoint.OperationIndex != 1 {
				return
			}
			mutatedSecond = true
			gomega.Expect(os.WriteFile(filepath.Join(root, "b.txt"), []byte("external\n"), 0o644)).To(gomega.Succeed())
		})
		result := manager.Execute(mutationCtx, ToolRequest{Tool: ToolNameApplyPatch, Description: "partially update", Content: "*** Begin Patch\n*** Update File: a.txt\n@@\n-a0\n+a1\n*** Update File: b.txt\n@@\n-b0\n+b1\n*** End Patch"})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty())
		gomega.Expect(result.ChangedPaths).To(gomega.Equal([]string{"a.txt"}))
		rollback, err := result.CommitReceipt().Rollback()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(rollback.Paths()).To(gomega.Equal([]string{"a.txt"}))
		rolledBack, err := manager.RollbackMutation(ctx, rollback)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(rolledBack.Status).To(gomega.Equal(MutationRollbackComplete))
		a, err := os.ReadFile(filepath.Join(root, "a.txt"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(a).To(gomega.Equal([]byte("a0\n")))
		b, err := os.ReadFile(filepath.Join(root, "b.txt"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(b).To(gomega.Equal([]byte("external\n")))
	})

	ginkgo.It("resumes implicit parent removal after its created child was already reversed", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx := WithExecutionSessionID(context.Background(), "directory-resume-session")
		write := manager.Execute(ctx, ToolRequest{Tool: ToolNameWriteFile, Path: "generated/new.txt", Content: "new\n", Description: "create nested file"})
		gomega.Expect(write.Error).To(gomega.BeEmpty(), write.Error)
		rollback, err := write.CommitReceipt().Rollback()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		injected := errors.New("pause after child removal")
		partialCtx := withMutationRollbackCheckpoint(ctx, func(checkpoint mutationRollbackCheckpoint) error {
			if checkpoint.Stage == mutationRollbackAfterEffect && checkpoint.CanonicalID == "generated/new.txt" {
				return injected
			}
			return nil
		})
		partial, err := manager.RollbackMutation(partialCtx, rollback)
		gomega.Expect(err).To(gomega.MatchError(injected))
		gomega.Expect(partial.Status).To(gomega.Equal(MutationRollbackPartial))
		_, err = os.Stat(filepath.Join(root, "generated", "new.txt"))
		gomega.Expect(errors.Is(err, os.ErrNotExist)).To(gomega.BeTrue())
		_, err = os.Stat(filepath.Join(root, "generated"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		complete, err := manager.RollbackMutation(ctx, rollback)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(complete.Status).To(gomega.Equal(MutationRollbackComplete))
		_, err = os.Stat(filepath.Join(root, "generated"))
		gomega.Expect(errors.Is(err, os.ErrNotExist)).To(gomega.BeTrue())
	})

	ginkgo.It("publishes explicit and implicit directories without replacing a concurrent path", func() {
		for _, scenario := range []struct {
			name    string
			request ToolRequest
			path    string
		}{
			{name: "explicit", request: ToolRequest{Tool: ToolNameMkdir, Path: "generated"}, path: "generated"},
			{name: "implicit", request: ToolRequest{Tool: ToolNameWriteFile, Path: "generated/new.txt", Content: "new\n", Description: "create nested file"}, path: "generated"},
		} {
			ginkgo.By(scenario.name)
			root := ginkgo.GinkgoT().TempDir()
			manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
			ctx := WithExecutionSessionID(context.Background(), "directory-race-"+scenario.name)
			inserted := false
			ctx = withWorkspaceMutationCheckpoint(ctx, func(checkpoint workspaceMutationCheckpoint) {
				if inserted || checkpoint.Stage != workspaceMutationBeforeDirectoryPublish || checkpoint.Path != scenario.path {
					return
				}
				inserted = true
				gomega.Expect(os.Mkdir(filepath.Join(root, scenario.path), 0o755)).To(gomega.Succeed())
				gomega.Expect(os.WriteFile(filepath.Join(root, scenario.path, "foreign.txt"), []byte("foreign\n"), 0o644)).To(gomega.Succeed())
			})
			result := manager.Execute(ctx, scenario.request)
			gomega.Expect(result.Error).NotTo(gomega.BeEmpty())
			gomega.Expect(result.ChangedPaths).To(gomega.BeEmpty())
			foreign, err := os.ReadFile(filepath.Join(root, scenario.path, "foreign.txt"))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(foreign).To(gomega.Equal([]byte("foreign\n")))
		}
	})

	ginkgo.It("reconciles an interruption after quarantine relocation", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx := WithExecutionSessionID(context.Background(), "quarantine-resume-session")
		write := manager.Execute(ctx, ToolRequest{Tool: ToolNameWriteFile, Path: "created.txt", Content: "created\n", Description: "create rollback target"})
		gomega.Expect(write.Error).To(gomega.BeEmpty(), write.Error)
		rollback, err := write.CommitReceipt().Rollback()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		interrupted := errors.New("interrupt after quarantine")
		fired := false
		partialCtx := withMutationRollbackCheckpoint(ctx, func(checkpoint mutationRollbackCheckpoint) error {
			if !fired && checkpoint.Stage == mutationRollbackAfterQuarantine && checkpoint.CanonicalID == "created.txt" {
				fired = true
				return interrupted
			}
			return nil
		})
		partial, err := manager.RollbackMutation(partialCtx, rollback)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("interrupt after quarantine")))
		gomega.Expect(partial.Status).To(gomega.Equal(MutationRollbackPartial))
		gomega.Expect(partial.PendingPaths).To(gomega.Equal([]string{"created.txt"}))
		_, err = os.Stat(filepath.Join(root, "created.txt"))
		gomega.Expect(errors.Is(err, os.ErrNotExist)).To(gomega.BeTrue())

		complete, err := manager.RollbackMutation(ctx, rollback)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(complete.Status).To(gomega.Equal(MutationRollbackComplete))
		entries, err := os.ReadDir(root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		for _, entry := range entries {
			gomega.Expect(entry.Name()).NotTo(gomega.HavePrefix(".idiom-rollback-"))
		}
	})

	ginkgo.It("honors cancellation immediately before the first quarantine commit", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx := WithExecutionSessionID(context.Background(), "quarantine-cancel-session")
		write := manager.Execute(ctx, ToolRequest{Tool: ToolNameWriteFile, Path: "created.txt", Content: "created\n", Description: "create rollback target"})
		gomega.Expect(write.Error).To(gomega.BeEmpty(), write.Error)
		rollback, err := write.CommitReceipt().Rollback()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		cancelCtx, cancel := context.WithCancel(ctx)
		cancelCtx = withMutationRollbackCheckpoint(cancelCtx, func(checkpoint mutationRollbackCheckpoint) error {
			if checkpoint.Stage == mutationRollbackBeforeQuarantine {
				cancel()
			}
			return nil
		})
		failed, err := manager.RollbackMutation(cancelCtx, rollback)
		gomega.Expect(err).To(gomega.MatchError(context.Canceled))
		gomega.Expect(failed.Status).To(gomega.Equal(MutationRollbackFailedBeforeCommit))
		body, err := os.ReadFile(filepath.Join(root, "created.txt"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(body).To(gomega.Equal([]byte("created\n")))
		complete, err := manager.RollbackMutation(ctx, rollback)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(complete.Status).To(gomega.Equal(MutationRollbackComplete))
	})

	ginkgo.It("removes an exactly created empty append target", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx := WithExecutionSessionID(context.Background(), "empty-append-session")
		result := manager.Execute(ctx, ToolRequest{Tool: ToolNameAppendFile, Path: "empty.log", Content: "", Description: "create empty log"})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), result.Error)
		rollback, err := result.CommitReceipt().Rollback()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		rolledBack, err := manager.RollbackMutation(ctx, rollback)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(rolledBack.Status).To(gomega.Equal(MutationRollbackComplete))
		_, err = os.Stat(filepath.Join(root, "empty.log"))
		gomega.Expect(errors.Is(err, os.ErrNotExist)).To(gomega.BeTrue())
	})

	ginkgo.It("refuses to mint a rollback capability without an explicit invocation identity", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		result := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameWriteFile, Path: "unbound.txt", Content: "value\n", Description: "write without explicit session"})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), result.Error)
		rollback, err := result.CommitReceipt().Rollback()
		gomega.Expect(rollback).To(gomega.BeNil())
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("explicit invocation identity")))
	})

	ginkgo.It("restores supported special file mode bits", func() {
		root := ginkgo.GinkgoT().TempDir()
		path := filepath.Join(root, "mode.txt")
		gomega.Expect(os.WriteFile(path, []byte("old\n"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.Chmod(path, 0o4755)).To(gomega.Succeed())
		before, err := os.Stat(path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		if before.Mode()&os.ModeSetuid == 0 {
			ginkgo.Skip("filesystem does not preserve setuid in the test fixture")
		}
		manager := NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx := WithExecutionSessionID(context.Background(), "special-mode-session")
		result := manager.Execute(ctx, ToolRequest{Tool: ToolNameApplyPatch, Description: "change mode file", Content: "*** Begin Patch\n*** Update File: mode.txt\n@@\n-old\n+new\n*** End Patch"})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), result.Error)
		rollback, err := result.CommitReceipt().Rollback()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		_, err = manager.RollbackMutation(ctx, rollback)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		after, err := os.Stat(path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(rollbackRestorableMode(after.Mode())).To(gomega.Equal(rollbackRestorableMode(before.Mode())))
	})
})
