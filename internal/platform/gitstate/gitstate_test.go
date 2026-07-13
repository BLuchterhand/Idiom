package gitstate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Git repository state", ginkgo.Label("integration"), func() {
	ginkgo.It("captures HEAD and porcelain status", func(ctx ginkgo.SpecContext) {
		root := initGitStateRepo()
		head := strings.TrimSpace(gitStateGit(root, "rev-parse", "HEAD"))
		writeGitStateFile(filepath.Join(root, "tracked.txt"), "changed\n")
		writeGitStateFile(filepath.Join(root, "untracked.txt"), "new\n")

		snapshot, err := Current(ctx, root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(snapshot.Head).To(gomega.Equal(head))
		gomega.Expect(snapshot.Dirty).To(gomega.BeTrue())
		gomega.Expect(snapshot.StatusLines).To(gomega.ContainElement(gomega.ContainSubstring("M tracked.txt")))
		gomega.Expect(snapshot.StatusLines).To(gomega.ContainElement(gomega.ContainSubstring("?? untracked.txt")))
		gomega.Expect(snapshot.Token()).To(gomega.Equal(snapshot.Head + "\n" + snapshot.Status))
	})

	ginkgo.It("returns the Git command error outside a repository", func(ctx ginkgo.SpecContext) {
		_, err := Current(ctx, ginkgo.GinkgoT().TempDir())
		gomega.Expect(err).To(gomega.MatchError(gomega.And(
			gomega.ContainSubstring("git rev-parse HEAD:"),
			gomega.ContainSubstring("not a git repository"),
		)))
	})

	ginkgo.It("preserves combined output and wraps Git failures", func(ctx ginkgo.SpecContext) {
		root := initGitStateRepo()
		head := gitStateGit(root, "rev-parse", "HEAD")

		got, err := CombinedOutputText(ctx, root, "rev-parse", "HEAD")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(got).To(gomega.Equal(head))

		_, err = CombinedOutputText(ctx, ginkgo.GinkgoT().TempDir(), "rev-parse", "HEAD")
		gomega.Expect(err).To(gomega.MatchError(gomega.And(
			gomega.ContainSubstring("git rev-parse HEAD:"),
			gomega.ContainSubstring("not a git repository"),
		)))
	})

	ginkgo.It("does not merge stderr into successful stdout", func(ctx ginkgo.SpecContext) {
		root := initGitStateRepo()
		got, err := Output(ctx, root, "-c", "alias.noisy=!sh -c 'printf stdout; printf stderr >&2'", "noisy")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(got).To(gomega.Equal("stdout"))
	})

	ginkgo.It("resolves relative Git metadata paths", func(ctx ginkgo.SpecContext) {
		root := initGitStateRepo()
		path, err := GitPath(ctx, root, "info/exclude")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(path).To(gomega.Equal(filepath.Join(root, ".git", "info", "exclude")))
	})

	ginkgo.DescribeTable("parsing porcelain paths",
		func(line string, want string) {
			gomega.Expect(PathFromPorcelainLine(line)).To(gomega.Equal(want))
		},
		ginkgo.Entry("modified", " M tracked.txt", "tracked.txt"),
		ginkgo.Entry("untracked nested", "?? nested/file.txt", "nested/file.txt"),
		ginkgo.Entry("renamed", "R  old/name.txt -> new/name.txt", "new/name.txt"),
		ginkgo.Entry("quoted rename arrows", `R  "old -> name.txt" -> "new -> name.txt"`, "new -> name.txt"),
		ginkgo.Entry("quoted trailing spaces", `R  old.go -> " trailing.go "`, " trailing.go "),
		ginkgo.Entry("path with spaces", ` M "space path.txt"`, "space path.txt"),
		ginkgo.Entry("quoted quote", `?? "quote\"path.txt"`, `quote"path.txt`),
		ginkgo.Entry("leading space", `?? " leading.go"`, " leading.go"),
		ginkgo.Entry("literal arrow", `?? a -> b.txt`, "a -> b.txt"),
		ginkgo.Entry("added", "A  folder/file.txt", "folder/file.txt"),
		ginkgo.Entry("missing path", " M", ""),
	)

	ginkgo.It("filters, deduplicates, and sorts dirty porcelain paths", func() {
		got := dirtyPathsFromPorcelain(strings.Join([]string{
			" M tracked.go",
			"?? nested/new.go",
			"R  old.go -> renamed.go",
			` M "space path.go"`,
			" M .idiom/recovery/packet.json",
			" M .idiom/plan-execute/progress.json",
			" M tracked.go",
		}, "\n"), DirtyPathOptions{
			IgnorePrefixes: []string{".idiom/recovery/", ".idiom/plan-execute/"},
			Sort:           true,
		})
		gomega.Expect(got).To(gomega.Equal([]string{"nested/new.go", "renamed.go", "space path.go", "tracked.go"}))
	})

	ginkgo.It("can preserve Git's dirty-path order", func() {
		got := dirtyPathsFromPorcelain(" M z.go\n M a.go", DirtyPathOptions{})
		gomega.Expect(got).To(gomega.Equal([]string{"z.go", "a.go"}))
	})

	ginkgo.It("includes every untracked path while honoring ignored prefixes", func(ctx ginkgo.SpecContext) {
		root := initGitStateRepo()
		writeGitStateFile(filepath.Join(root, "tracked.txt"), "changed\n")
		writeGitStateFile(filepath.Join(root, "nested", "new.txt"), "new\n")
		writeGitStateFile(filepath.Join(root, `quote"path.txt`), "quoted\n")
		writeGitStateFile(filepath.Join(root, "a -> b.txt"), "arrow\n")
		writeGitStateFile(filepath.Join(root, ".idiom", "recovery", "packet.json"), "{}\n")

		got, err := DirtyPaths(ctx, root, DirtyPathOptions{IgnorePrefixes: []string{".idiom/recovery/"}, Sort: true})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(got).To(gomega.Equal([]string{"a -> b.txt", "nested/new.txt", `quote"path.txt`, "tracked.txt"}))
	})

	ginkgo.It("scopes dirty paths with Git pathspecs", func(ctx ginkgo.SpecContext) {
		root := initGitStateRepo()
		writeGitStateFile(filepath.Join(root, "other.txt"), "other\n")
		gitStateGit(root, "add", "other.txt")
		gitStateGit(root, "commit", "-m", "add other")
		writeGitStateFile(filepath.Join(root, "tracked.txt"), "changed\n")
		writeGitStateFile(filepath.Join(root, "other.txt"), "changed\n")
		writeGitStateFile(filepath.Join(root, "nested", "new.txt"), "new\n")

		got, err := DirtyPaths(ctx, root, DirtyPathOptions{Pathspecs: []string{"tracked.txt", "nested"}, Sort: true})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(got).To(gomega.Equal([]string{"nested/new.txt", "tracked.txt"}))
	})

	ginkgo.It("uses the first path commit only to choose the repository-wide baseline", func(ctx ginkgo.SpecContext) {
		root := initGitStateRepo()
		writeGitStateFile(filepath.Join(root, "effort", "initial.go"), "package effort\n")
		gitStateGit(root, "add", "effort/initial.go")
		gitStateGit(root, "commit", "-m", "add effort")
		writeGitStateFile(filepath.Join(root, "outside.go"), "package outside\n")
		gitStateGit(root, "add", "outside.go")
		gitStateGit(root, "commit", "-m", "add outside")
		writeGitStateFile(filepath.Join(root, "effort", "later.go"), "package effort\n")
		gitStateGit(root, "add", "effort/later.go")
		gitStateGit(root, "commit", "-m", "add later effort")

		got, err := ChangedPathsSinceFirstPathCommit(ctx, root, "effort")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(got).To(gomega.Equal([]string{"effort/initial.go", "effort/later.go", "outside.go"}))
	})

	ginkgo.It("returns the name-only paths for a commit", func(ctx ginkgo.SpecContext) {
		root := initGitStateRepo()
		writeGitStateFile(filepath.Join(root, "nested", "new.go"), "package nested\n")
		gitStateGit(root, "add", "nested/new.go")
		gitStateGit(root, "commit", "-m", "add nested")
		head := strings.TrimSpace(gitStateGit(root, "rev-parse", "HEAD"))

		got, err := CommitChangedPaths(ctx, root, head)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(got).To(gomega.Equal([]string{"nested/new.go"}))
	})

	ginkgo.It("finds Git metadata through ancestor directories", func() {
		root := initGitStateRepo()
		nested := filepath.Join(root, "a", "b")
		gomega.Expect(os.MkdirAll(nested, 0o755)).To(gomega.Succeed())

		got, ok := MetadataRoot(nested)
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(got).To(gomega.Equal(root))
		gomega.Expect(HasMetadataAncestor(nested)).To(gomega.BeTrue())
	})

	ginkgo.It("filters porcelain status by parsed path instead of raw text", func() {
		status := strings.Join([]string{
			" M .idiom/recovery/packet.json",
			` M "docs/.idiom-note.md"`,
			"?? src/main.go",
			`R  "old -> path.go" -> ".idiom/generated.go"`,
		}, "\n")
		want := "M \"docs/.idiom-note.md\"\n?? src/main.go"
		gomega.Expect(FilterPorcelainStatusByPathPrefix(status, ".idiom/")).To(gomega.Equal(want))
	})

	ginkgo.It("routes status and diff through the injected command policy", func(ctx ginkgo.SpecContext) {
		var calls []string
		runner := Runner{
			Root: "/repo",
			RunCommand: func(_ context.Context, root string, args ...string) (string, error) {
				gomega.Expect(root).To(gomega.Equal("/repo"))
				call := strings.Join(args, " ")
				calls = append(calls, call)
				return call, nil
			},
		}

		status, err := runner.Status(ctx)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(status).To(gomega.Equal("status --porcelain=v1 --branch --untracked-files=normal"))
		diff, err := runner.WorktreeDiff(ctx, "src/file.go")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(diff).To(gomega.Equal("diff --no-color -- src/file.go"))
		gomega.Expect(calls).To(gomega.Equal([]string{
			"status --porcelain=v1 --branch --untracked-files=normal",
			"diff --no-color -- src/file.go",
		}))
	})

	ginkgo.It("classifies the repository states that make history inapplicable", func(ctx ginkgo.SpecContext) {
		nonGit := ginkgo.GinkgoT().TempDir()
		writeGitStateFile(filepath.Join(nonGit, "sample.go"), "package main\n")
		expectHistoryApplicability(ctx, nonGit, "sample.go", false, HistoryReasonNonGitRoot)

		noHead := ginkgo.GinkgoT().TempDir()
		gitStateGit(noHead, "init")
		writeGitStateFile(filepath.Join(noHead, "sample.go"), "package main\n")
		expectHistoryApplicability(ctx, noHead, "sample.go", false, HistoryReasonNoHead)

		untracked := initGitStateRepo()
		writeGitStateFile(filepath.Join(untracked, "sample.go"), "package main\n")
		expectHistoryApplicability(ctx, untracked, "sample.go", false, HistoryReasonUntrackedPath)

		noCommittedHistory := initGitStateRepo()
		writeGitStateFile(filepath.Join(noCommittedHistory, "sample.go"), "package main\n")
		gitStateGit(noCommittedHistory, "add", "sample.go")
		expectHistoryApplicability(ctx, noCommittedHistory, "sample.go", false, HistoryReasonPathNoCommittedHistory)

		applicable := initGitStateRepo()
		expectHistoryApplicability(ctx, applicable, "tracked.txt", true, "")
	})

	ginkgo.It("matches history applicability against exact paths", func(ctx ginkgo.SpecContext) {
		root := initGitStateRepo()
		writeGitStateFile(filepath.Join(root, "dir", "space path.go"), "package main\n")
		writeGitStateFile(filepath.Join(root, "dir", "space path.go.bak"), "package main\n")
		gitStateGit(root, "add", "dir/space path.go.bak")
		gitStateGit(root, "commit", "-m", "add sibling")

		expectHistoryApplicability(ctx, root, "dir/space path.go", false, HistoryReasonUntrackedPath)
		expectHistoryApplicability(ctx, root, "dir/space path.go.bak", true, "")
	})

	ginkgo.It("uses stable Git forms for range, symbol, and blame history", func(ctx ginkgo.SpecContext) {
		root := ginkgo.GinkgoT().TempDir()
		gitStateGit(root, "init")
		gitStateGit(root, "config", "user.email", "test@example.com")
		gitStateGit(root, "config", "user.name", "Test User")
		writeGitStateFile(filepath.Join(root, "sample.go"), "package main\n\nfunc add(a int, b int) int {\n\treturn a + b\n}\n")
		gitStateGit(root, "add", "sample.go")
		gitStateGit(root, "commit", "-m", "initial")
		writeGitStateFile(filepath.Join(root, "sample.go"), "package main\n\nfunc add(a int, b int) int {\n\treturn a - b\n}\n")
		gitStateGit(root, "commit", "-am", "change add")

		runner := Runner{Root: root}
		history, err := runner.HistoryRange(ctx, "sample.go", 3, 5)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(history.HasCommitDescriptions).To(gomega.BeTrue())
		gomega.Expect(history.Output).To(gomega.And(gomega.ContainSubstring("change add"), gomega.ContainSubstring("initial")))

		symbol, err := runner.HistorySymbol(ctx, "sample.go", "add")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(symbol.HasCommitDescriptions).To(gomega.BeTrue())
		gomega.Expect(symbol.Output).To(gomega.And(gomega.ContainSubstring("change add"), gomega.ContainSubstring("initial")))

		blame, err := runner.BlameRange(ctx, "sample.go", 4, 4)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(blame).To(gomega.ContainSubstring("return a - b"))
	})
})

func expectHistoryApplicability(ctx context.Context, root string, path string, applicable bool, reason HistoryNotApplicableReason) {
	result, err := (Runner{Root: root}).CheckHistoryApplicability(ctx, path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(result.Applicable).To(gomega.Equal(applicable))
	gomega.Expect(result.Reason).To(gomega.Equal(reason))
}

func initGitStateRepo() string {
	root := ginkgo.GinkgoT().TempDir()
	gitStateGit(root, "init")
	gitStateGit(root, "config", "user.name", "Idiom Test")
	gitStateGit(root, "config", "user.email", "idiom@example.test")
	writeGitStateFile(filepath.Join(root, "tracked.txt"), "initial\n")
	gitStateGit(root, "add", "tracked.txt")
	gitStateGit(root, "commit", "-m", "initial")
	return root
}

func gitStateGit(root string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "git %s\n%s", strings.Join(args, " "), string(out))
	return string(out)
}

func writeGitStateFile(path string, content string) {
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, []byte(content), 0o644)).To(gomega.Succeed())
}
