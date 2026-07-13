package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/platform/config"
)

func patchWorkspaceConfig() (*Manager, string) {
	ginkgo.GinkgoHelper()

	dir := ginkgo.GinkgoT().TempDir()
	cfg := config.Workspace{
		Root:                  dir,
		CWD:                   dir,
		Shell:                 "/bin/sh",
		CommandTimeoutSeconds: 5,
		MaxOutputBytes:        65536,
		MaxFileBytes:          65536,
		MaxDirectoryEntries:   100,
		EnableShell:           true,
		EnableWrites:          true,
	}

	return NewManager(cfg), dir
}

var _ = ginkgo.Describe("patch", func() {
	ginkgo.Describe("write tools report changed paths", func() {
		for _, tc := range []struct {
			name string
			req  ToolRequest
			want string
		}{
			{name: "write", req: ToolRequest{Tool: ToolNameWriteFile, Path: "created.txt", Content: "hello\n"}, want: "created.txt"},
			{name: "append", req: ToolRequest{Tool: ToolNameAppendFile, Path: "append.txt", Content: "tail\n"}, want: "append.txt"},
			{name: "mkdir", req: ToolRequest{Tool: ToolNameMkdir, Path: "generated"}, want: "generated"},
			{name: "edit", req: ToolRequest{Tool: ToolNameEditFile, Path: "existing.txt", OldText: "before\n", NewText: "after\n"}, want: "existing.txt"},
		} {
			ginkgo.It(tc.name, func() {
				mgr, dir := patchWorkspaceConfig()
				gomega.Expect(os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("before\n"), 0o644)).To(gomega.Succeed())
				mgr.MarkPathObserved("existing.txt")

				result := mgr.Execute(context.Background(), tc.req)
				gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("%s failed: %#v", tc.req.Tool, result))
				gomega.Expect(result.ChangedPaths).To(gomega.HaveLen(1), fmt.Sprintf("%s changed paths = %#v, want %q", tc.req.Tool, result.ChangedPaths, tc.want))
				gomega.Expect(result.ChangedPaths[0]).To(gomega.Equal(tc.want), fmt.Sprintf("%s changed paths = %#v, want %q", tc.req.Tool, result.ChangedPaths, tc.want))

			})
		}
	})
	ginkgo.It("workspace write rejects symlinked parent", func() {
		mgr, dir := patchWorkspaceConfig()
		outside := ginkgo.GinkgoT().TempDir()
		if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlink unavailable: %v", err))
		}

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:    ToolNameWriteFile,
			Path:    "linked/escape.txt",
			Content: "outside\n",
		})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected symlinked parent rejection, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("escapes workspace root through a symlink"), fmt.Sprintf("expected symlinked parent rejection, got %#v", result))

		if _, err := os.Stat(filepath.Join(outside, "escape.txt")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("write_file should not create outside target, stat err=%v", err))
		}
	})
	ginkgo.It("workspace write rejects symlink target", func() {
		mgr, dir := patchWorkspaceConfig()
		outside := filepath.Join(ginkgo.GinkgoT().TempDir(), "target.txt")
		gomega.Expect(os.WriteFile(outside, []byte("original\n"), 0o644)).To(gomega.Succeed())

		if err := os.Symlink(outside, filepath.Join(dir, "target.txt")); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlink unavailable: %v", err))
		}

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:    ToolNameWriteFile,
			Path:    "target.txt",
			Content: "mutated\n",
		})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected symlink target rejection, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("escapes workspace root through a symlink"), fmt.Sprintf("expected symlink target rejection, got %#v", result))

		if data, err := os.ReadFile(outside); err != nil || string(data) != "original\n" {
			ginkgo.Fail(fmt.Sprintf("outside target mutated: data=%q err=%v", data, err))
		}
	})
	ginkgo.It("apply patch rejects symlinked parent before mutation", func() {
		mgr, dir := patchWorkspaceConfig()
		outside := ginkgo.GinkgoT().TempDir()
		if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlink unavailable: %v", err))
		}

		patch := "*** Begin Patch\n" +
			"*** Add File: linked/escape.txt\n" +
			"+outside\n" +
			"*** End Patch"
		result := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameApplyPatch, Content: patch})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected symlinked parent rejection, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("escapes workspace root through a symlink"), fmt.Sprintf("expected symlinked parent rejection, got %#v", result))

		if _, err := os.Stat(filepath.Join(outside, "escape.txt")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("apply_patch should not create outside target, stat err=%v", err))
		}
	})
	ginkgo.It("apply patch add update delete", func() {
		mgr, dir := patchWorkspaceConfig()
		gomega.Expect(os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("alpha\nbeta\ngamma\n"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(dir, "doomed.txt"), []byte("bye\n"), 0o644)).To(gomega.Succeed())

		patch := "*** Begin Patch\n" +
			"*** Add File: created/note.md\n" +
			"+# Note\n" +
			"+body\n" +
			"*** Update File: existing.txt\n" +
			"@@\n" +
			" alpha\n" +
			"-beta\n" +
			"+BETA\n" +
			" gamma\n" +
			"*** Delete File: doomed.txt\n" +
			"*** End Patch"

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "apply_patch", Content: patch})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("apply_patch failed: %#v", result))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("Applied patch"), fmt.Sprintf("unexpected summary: %q", result.Summary))

		wantChanged := []string{"created/note.md", "existing.txt", "doomed.txt", "created"}
		gomega.Expect(result.ChangedPaths).To(gomega.HaveLen(len(wantChanged)), fmt.Sprintf("changed paths = %#v, want %#v", result.ChangedPaths, wantChanged))

		for i, want := range wantChanged {
			gomega.Expect(result.ChangedPaths[i]).To(gomega.Equal(want), fmt.Sprintf("changed paths = %#v, want %#v", result.ChangedPaths, wantChanged))

		}

		if data, err := os.ReadFile(filepath.Join(dir, "created", "note.md")); err != nil || string(data) != "# Note\nbody\n" {
			ginkgo.Fail(fmt.Sprintf("created file mismatch: data=%q err=%v", string(data), err))
		}

		if data, err := os.ReadFile(filepath.Join(dir, "existing.txt")); err != nil || string(data) != "alpha\nBETA\ngamma\n" {
			ginkgo.Fail(fmt.Sprintf("updated file mismatch: data=%q err=%v", string(data), err))
		}

		if _, err := os.Stat(filepath.Join(dir, "doomed.txt")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("doomed.txt should be removed, stat err=%v", err))
		}
	})
	ginkgo.It("apply patch repairs missing envelope for file section patch", func() {
		mgr, dir := patchWorkspaceConfig()
		gomega.Expect(os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("alpha\nbeta\ngamma\n"), 0o644)).To(gomega.Succeed())

		patch := "*** Update File: existing.txt\n" +
			"@@\n" +
			" alpha\n" +
			"-beta\n" +
			"+BETA\n" +
			" gamma\n"

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "apply_patch", Content: patch})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("apply_patch should repair unambiguous missing envelope, got %#v", result))

		if data, err := os.ReadFile(filepath.Join(dir, "existing.txt")); err != nil || string(data) != "alpha\nBETA\ngamma\n" {
			ginkgo.Fail(fmt.Sprintf("updated file mismatch: data=%q err=%v", string(data), err))
		}
	})
	ginkgo.It("apply patch does not repair git diff without envelope", func() {
		mgr, dir := patchWorkspaceConfig()
		gomega.Expect(os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("alpha\nbeta\n"), 0o644)).To(gomega.Succeed())

		patch := "diff --git a/existing.txt b/existing.txt\n" +
			"--- a/existing.txt\n" +
			"+++ b/existing.txt\n" +
			"@@\n" +
			"-beta\n" +
			"+BETA\n"

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "apply_patch", Content: patch})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected git diff to remain rejected without envelope, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("patch must start"), fmt.Sprintf("expected git diff to remain rejected without envelope, got %#v", result))

	})
	ginkgo.It("apply patch move to", func() {
		mgr, dir := patchWorkspaceConfig()
		gomega.Expect(os.WriteFile(filepath.Join(dir, "src.txt"), []byte("hello\n"), 0o644)).To(gomega.Succeed())

		patch := "*** Begin Patch\n" +
			"*** Update File: src.txt\n" +
			"*** Move to: dst.txt\n" +
			"@@\n" +
			"-hello\n" +
			"+goodbye\n" +
			"*** End Patch"

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "apply_patch", Content: patch})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("apply_patch failed: %#v", result))

		if _, err := os.Stat(filepath.Join(dir, "src.txt")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("src.txt should be removed, stat err=%v", err))
		}

		if data, err := os.ReadFile(filepath.Join(dir, "dst.txt")); err != nil || string(data) != "goodbye\n" {
			ginkgo.Fail(fmt.Sprintf("dst.txt mismatch: data=%q err=%v", string(data), err))
		}
	})
	ginkgo.It("apply patch rejects existing add target", func() {
		mgr, dir := patchWorkspaceConfig()
		gomega.Expect(os.WriteFile(filepath.Join(dir, "exists.txt"), []byte("a\n"), 0o644)).To(gomega.Succeed())

		patch := "*** Begin Patch\n" +
			"*** Add File: exists.txt\n" +
			"+x\n" +
			"*** End Patch"
		result := mgr.Execute(context.Background(), ToolRequest{Tool: "apply_patch", Content: patch})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), "expected error for Add File on existing target")

		if data, _ := os.ReadFile(filepath.Join(dir, "exists.txt")); string(data) != "a\n" {
			ginkgo.Fail(fmt.Sprintf("file should be untouched: %q", string(data)))
		}
	})
	ginkgo.It("apply patch ambiguous hunk rejected", func() {
		mgr, dir := patchWorkspaceConfig()
		gomega.Expect(os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\nx\nx\n"), 0o644)).To(gomega.Succeed())

		patch := "*** Begin Patch\n" +
			"*** Update File: f.txt\n" +
			"@@\n" +
			"-x\n" +
			"+y\n" +
			"*** End Patch"
		result := mgr.Execute(context.Background(), ToolRequest{Tool: "apply_patch", Content: patch})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), "expected ambiguous-hunk error")

		if data, _ := os.ReadFile(filepath.Join(dir, "f.txt")); string(data) != "x\nx\nx\n" {
			ginkgo.Fail(fmt.Sprintf("file should be untouched: %q", string(data)))
		}
	})
	ginkgo.It("apply patch rejects invalid go before write", func() {
		mgr, dir := patchWorkspaceConfig()
		path := filepath.Join(dir, "analysis_agents.go")
		original := "package app\n\nfunc ok() {}\n"
		gomega.Expect(os.WriteFile(path, []byte(original), 0o644)).To(gomega.Succeed())

		patch := "*** Begin Patch\n" +
			"*** Update File: analysis_agents.go\n" +
			"@@\n" +
			"-package app\n" +
			"+\n" +
			" \n" +
			" func ok() {}\n" +
			"*** End Patch"

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "apply_patch", Content: patch})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected invalid Go patch rejection, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("preflight syntax validation failed"), fmt.Sprintf("expected invalid Go patch rejection, got %#v", result))

		if data, _ := os.ReadFile(path); string(data) != original {
			ginkgo.Fail(fmt.Sprintf("file should be untouched: %q", string(data)))
		}
	})
	ginkgo.It("apply patch explore mode rejected", func() {
		mgr, _ := patchWorkspaceConfig()
		gomega.Expect(mgr.SetMode(ModeExplore)).To(gomega.Succeed())

		patch := "*** Begin Patch\n*** Add File: x.txt\n+y\n*** End Patch"
		result := mgr.Execute(context.Background(), ToolRequest{Tool: "apply_patch", Content: patch})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), "expected explore-mode rejection")

	})
})
