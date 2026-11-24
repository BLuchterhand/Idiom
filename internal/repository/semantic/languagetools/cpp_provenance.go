package languagetools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"idiom/internal/platform/commandexec"
	"idiom/internal/platform/gitstate"
	"idiom/internal/platform/hosttools"
	"idiom/internal/platform/safepath"
	"idiom/internal/repository/discovery"
	semanticcpp "idiom/internal/repository/semantic/cpp"
)

const (
	// cppCollectorVersion changes whenever the syntax collector's output changes,
	// so an index identity never spans two collector behaviours.
	cppCollectorVersion = "cpp-syntax-index/2"
	// cppIndexBudget bounds one in-process C++ index build; beyond it the tool
	// fails with cpp_index_budget_exceeded and the phase timings.
	cppIndexBudget = 10 * time.Second
	// cppClangdBudget bounds the optional clangd call-hierarchy attempt so it can
	// never consume the tool's whole timeout before the syntax index answers.
	cppClangdBudget = 5 * time.Second
	// cppReferencesClangdBudget bounds the optional cross-file readiness probe;
	// name-search fallback must retain visible time within the sidecar deadline
	// even on the first call that also builds the syntax index.
	cppReferencesClangdBudget = 1500 * time.Millisecond
	// cppFreshnessPathLimit caps the dirty paths echoed in a freshness report.
	cppFreshnessPathLimit = 20
)

var cppGitPathspecs = []string{
	"*.c", "*.cc", "*.cpp", "*.cxx", "*.h", "*.hh", "*.hpp", "*.hxx", "*.ipp", "*.inl", "*.tpp",
}

// What produced an answer: the index in this report, or clangd (whose result is
// not derived from the index).
const (
	cppAnsweredBySyntax = "syntax-index"
	cppAnsweredByClangd = "clangd"
)

const (
	cppCodeBudgetExceeded = "cpp_index_budget_exceeded"
	cppCodeAmbiguous      = "ambiguous_symbol"
	cppCodeNotFound       = "symbol_not_found"
)

// cppIndexInfo is the identity and cost of the index a C++ tool answered from.
// The identity (Key) names the content, not the checkout: two worktrees of one
// repository at the same tree with the same dirty C++ files share a key.
type cppIndexInfo struct {
	Key               string
	Root              string
	GitCommonDir      string
	Commit            string
	Tree              string
	TrackedDirtyPaths []string
	DirtyPaths        []string
	IgnoredPaths      []string
	GitError          string
	Git               time.Duration
	Build             semanticcpp.CppBuildTimings
	IndexedPaths      []string
	Symbols           int
	Memoized          bool
}

// cppSnapshot reads the Git identity of the workspace and a content hash of the
// C++ files that the syntax index can see. Git only supplies the repository
// identity and tracked diff; the bounded walk supplies untracked files without
// descending into discovery-excluded directories.
func cppSnapshot(ctx context.Context, root string) cppIndexInfo {
	started := time.Now()
	info := cppIndexInfo{Root: root}

	output, err := gitstate.Output(ctx, root, "rev-parse", "--git-common-dir", "HEAD", "HEAD^{tree}")
	if err != nil {
		info.GitError = err.Error()
		info.Git = time.Since(started)

		return info
	}

	lines := strings.Fields(output)
	if len(lines) < 3 {
		info.GitError = "unexpected git rev-parse output"
		info.Git = time.Since(started)

		return info
	}

	common := lines[0]
	if !filepath.IsAbs(common) {
		common = filepath.Join(root, common)
	}

	if resolved, resolveErr := filepath.EvalSymlinks(common); resolveErr == nil {
		common = resolved
	}

	info.GitCommonDir, info.Commit, info.Tree = filepath.ToSlash(common), lines[1], lines[2]

	dirty, err := cppChangedTrackedPaths(ctx, root)
	if err != nil {
		info.GitError = err.Error()
		info.Git = time.Since(started)

		return info
	}

	info.DirtyPaths = dirty
	info.TrackedDirtyPaths = append([]string(nil), dirty...)

	// The index skips the same noisy directories as repository discovery. Do not
	// ask Git to enumerate ignored files across those directories: on Windows an
	// ignored worktree forest can take longer than the whole C++ index budget.
	tracked, err := cppTrackedPaths(ctx, root)
	if err != nil {
		info.GitError = err.Error()
		info.Git = time.Since(started)

		return info
	}

	untracked, err := cppUntrackedIndexedPaths(ctx, root, tracked, info.DirtyPaths)
	if err != nil {
		info.GitError = err.Error()
		info.Git = time.Since(started)

		return info
	}

	nonIgnored, err := cppNonIgnoredPaths(ctx, root)
	if err != nil {
		info.GitError = err.Error()
		info.Git = time.Since(started)

		return info
	}
	nonIgnoredSet := make(map[string]struct{}, len(nonIgnored))
	for _, path := range nonIgnored {
		nonIgnoredSet[path] = struct{}{}
	}
	for _, path := range untracked {
		if _, ok := nonIgnoredSet[path]; ok {
			info.DirtyPaths = append(info.DirtyPaths, path)

			continue
		}

		info.IgnoredPaths = append(info.IgnoredPaths, path)
	}
	slices.Sort(info.DirtyPaths)
	slices.Sort(info.IgnoredPaths)

	info.Key = cppIndexKey(root, info)
	info.Git = time.Since(started)

	return info
}

// cppChangedTrackedPaths returns tracked C++ paths changed from HEAD. Unlike
// git status with untracked-files=all, this does not enumerate ignored files.
func cppChangedTrackedPaths(ctx context.Context, root string) ([]string, error) {
	args := append([]string{"diff", "--name-only", "--diff-filter=ACMRDTU", "HEAD", "--"}, cppGitPathspecs...)
	output, err := gitstate.Output(ctx, root, args...)
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, 16)
	for _, line := range strings.Split(output, "\n") {
		path := strings.TrimSpace(line)
		if path == "" {
			continue
		}

		paths = append(paths, filepath.ToSlash(filepath.Clean(path)))
	}

	slices.Sort(paths)

	return paths, nil
}

// cppTrackedPaths returns the tracked C++ paths without asking Git to descend
// through ignored directories. The latter collapses ignored directories into
// directory entries, but still scans all of their contents on Windows.
func cppTrackedPaths(ctx context.Context, root string) (map[string]struct{}, error) {
	args := append([]string{"ls-files", "--cached", "--"}, cppGitPathspecs...)
	output, err := gitstate.Output(ctx, root, args...)
	if err != nil {
		return nil, err
	}

	tracked := make(map[string]struct{})
	for _, line := range strings.Split(output, "\n") {
		path := strings.TrimSpace(line)
		if path == "" {
			continue
		}

		tracked[filepath.ToSlash(filepath.Clean(path))] = struct{}{}
	}

	return tracked, nil
}

// cppNonIgnoredPaths returns non-ignored untracked C++ paths without asking
// Git to enumerate ignored descendants. The index-scope walk still supplies
// ignored paths, which are the files the provenance key must also cover.
func cppNonIgnoredPaths(ctx context.Context, root string) ([]string, error) {
	args := append([]string{"ls-files", "-o", "--exclude-standard", "--"}, cppGitPathspecs...)
	output, err := gitstate.Output(ctx, root, args...)
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, 16)
	for _, line := range strings.Split(output, "\n") {
		path := strings.TrimSpace(line)
		if path == "" {
			continue
		}

		paths = append(paths, filepath.ToSlash(filepath.Clean(path)))
	}

	slices.Sort(paths)

	return paths, nil
}

// cppUntrackedIndexedPaths finds untracked C++ files in the same scope the
// syntax index actually walks. It deliberately does not ask Git to classify
// them: both ignored and non-ignored files are inputs to the live index.
func cppUntrackedIndexedPaths(ctx context.Context, root string, tracked map[string]struct{}, dirty []string) ([]string, error) {
	if rg := hosttools.FirstAvailable("rg"); rg != "" {
		if paths, ok := cppUntrackedIndexedPathsByRipgrep(ctx, root, rg, tracked, dirty); ok {
			return paths, nil
		}
	}

	return cppUntrackedIndexedPathsByWalk(ctx, root, tracked, dirty)
}

// cppUntrackedIndexedPathsByRipgrep enumerates the same C++ scope without
// asking Git to visit ignored worktree forests. ripgrep does not follow
// symlinked directories by default; every returned path is still checked
// against the safe root before it enters provenance.
func cppUntrackedIndexedPathsByRipgrep(ctx context.Context, root string, rg string, tracked map[string]struct{}, dirty []string) ([]string, bool) {
	args := []string{"--files", "--hidden", "--no-ignore", "--no-messages"}
	for _, glob := range discovery.RipgrepExcludeGlobs() {
		args = append(args, "--glob", glob)
	}
	for _, extension := range cppGitPathspecs {
		args = append(args, "--glob", extension)
	}
	args = append(args, "--", ".")

	result := commandexec.Run(ctx, commandexec.Spec{
		Name:           rg,
		Args:           args,
		Dir:            root,
		MaxOutputBytes: 64 * 1024 * 1024,
	})
	if result.TimedOut || result.ExitCode > 1 || (result.Err != nil && strings.TrimSpace(result.Stdout) == "") {
		return nil, false
	}

	dirtySet := make(map[string]struct{}, len(dirty))
	for _, path := range dirty {
		dirtySet[filepath.ToSlash(filepath.Clean(path))] = struct{}{}
	}

	paths := make([]string, 0, 16)
	for _, line := range strings.Split(result.Stdout, "\n") {
		rel := filepath.ToSlash(filepath.Clean(strings.TrimSpace(line)))
		rel = strings.TrimPrefix(rel, "./")
		if rel == "" || rel == "." || cppSnapshotPathSkipped(rel) {
			continue
		}
		if _, ok := tracked[rel]; ok {
			continue
		}
		if _, ok := dirtySet[rel]; ok {
			continue
		}

		abs, _, err := safepath.UnderRoot(root, filepath.Join(root, filepath.FromSlash(rel)), "C++ snapshot", safepath.Options{
			RejectSymlinkAncestors: true,
			RejectSymlinkTarget:    true,
		})
		if err != nil {
			continue
		}
		info, err := os.Stat(abs)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		paths = append(paths, rel)
	}

	slices.Sort(paths)

	return paths, true
}

func cppSnapshotPathSkipped(rel string) bool {
	if discovery.ShouldSkipRelFile(rel) {
		return true
	}

	parts := strings.Split(filepath.ToSlash(filepath.Clean(rel)), "/")
	for _, part := range parts[:max(0, len(parts)-1)] {
		if discovery.ShouldSkipDirName(part) {
			return true
		}
	}

	return false
}

func cppUntrackedIndexedPathsByWalk(ctx context.Context, root string, tracked map[string]struct{}, dirty []string) ([]string, error) {
	dirtySet := make(map[string]struct{}, len(dirty))
	for _, path := range dirty {
		dirtySet[filepath.ToSlash(filepath.Clean(path))] = struct{}{}
	}

	paths := make([]string, 0, 16)
	err := safepath.WalkDirUnderRoot(root, root, "C++ snapshot", func(path string, d os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}

		if walkErr != nil {
			return walkErr
		}

		if d.IsDir() {
			if discovery.ShouldSkipDirName(d.Name()) && path != root {
				return filepath.SkipDir
			}

			return nil
		}

		if !isCppExtension(filepath.Ext(path)) {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}

		rel = filepath.ToSlash(filepath.Clean(rel))
		if discovery.ShouldSkipRelFile(rel) {
			return nil
		}
		if _, ok := tracked[rel]; ok {
			return nil
		}
		if _, ok := dirtySet[rel]; ok {
			return nil
		}

		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}

	slices.Sort(paths)

	return paths, nil
}

func cppIndexKey(root string, info cppIndexInfo) string {
	hash := sha256.New()
	fmt.Fprintf(hash, "%s\n%s\n%s\n", cppCollectorVersion, info.GitCommonDir, info.Tree)

	for _, group := range []struct {
		label string
		paths []string
	}{{"dirty", info.DirtyPaths}, {"ignored", info.IgnoredPaths}} {
		for _, rel := range group.paths {
			content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
			if err != nil {
				fmt.Fprintf(hash, "%s\x00%s\x00deleted\n", group.label, rel)

				continue
			}

			sum := sha256.Sum256(content)
			fmt.Fprintf(hash, "%s\x00%s\x00%x\n", group.label, rel, sum)
		}
	}

	return hex.EncodeToString(hash.Sum(nil))[:16]
}

// freshness reports what the answer reflects. The index is always built from
// the live working tree, so dirty files are included rather than overlaid and
// the answer is never silently stale.
func (info cppIndexInfo) freshness(query time.Duration, budgetExceeded bool, answeredBy string) map[string]any {
	dirty := info.DirtyPaths
	if len(dirty) > cppFreshnessPathLimit {
		dirty = dirty[:cppFreshnessPathLimit]
	}

	build := "built"
	if info.Memoized {
		build = "memoized"
	}

	report := map[string]any{
		"schema":            "idiom.cpp_index.v1",
		"fresh":             !budgetExceeded && info.GitError == "",
		"source":            "working_tree",
		"collector":         cppCollectorVersion,
		"index_key":         info.Key,
		"root":              info.Root,
		"git_common_dir":    info.GitCommonDir,
		"commit":            info.Commit,
		"tree":              info.Tree,
		"working_tree":      "dirty and gitignored C++ files included",
		"dirty_cpp_paths":   dirty,
		"dirty_cpp_count":   len(info.DirtyPaths),
		"ignored_cpp_count": len(info.IgnoredPaths),
		"files":             info.Build.Files,
		"symbols":           info.Symbols,
		"bytes":             info.Build.Bytes,
		"index_build":       build,
		"phase_ms": map[string]int64{
			"git":         info.Git.Milliseconds(),
			"walk":        info.Build.Walk.Milliseconds(),
			"parse":       info.Build.Parse.Milliseconds(),
			"link":        info.Build.Link.Milliseconds(),
			"calibration": info.Build.Calibration.Milliseconds(),
			"parse_io":    info.Build.ParseIO.Milliseconds(),
			"parse_cpu":   info.Build.ParseCPU.Milliseconds(),
		},
		"workers":     info.Build.Workers,
		"answered_by": answeredBy,
		"query_ms":    query.Milliseconds(),
		"budget_ms":   cppIndexBudget.Milliseconds(),
	}
	if info.GitError != "" {
		report["identity_error"] = info.GitError
	}

	if budgetExceeded {
		report["failed"] = "index budget exceeded"
	}

	return report
}

// cppBudgetFailure builds the failure result for an index build that ran out of
// its budget, carrying the phase timings the caller needs to act on it.
func cppBudgetFailure(tool string, summary string, err error, info cppIndexInfo) (ToolResult, bool) {
	var budget *semanticcpp.BudgetError
	if !errors.As(err, &budget) {
		return ToolResult{}, false
	}

	info.Build = budget.Timings

	return ToolResult{
		Tool:      tool,
		Code:      cppCodeBudgetExceeded,
		Summary:   summary,
		Error:     fmt.Sprintf("%s: %v (budget %s)", cppCodeBudgetExceeded, budget, cppIndexBudget),
		Freshness: info.freshness(0, true, cppAnsweredBySyntax),
	}, true
}

func cppOwnerDisplay(match semanticcpp.CppSymbolMatch) string {
	return fmt.Sprintf("%s:%d %s %s", match.Path, match.Line, match.Kind, cppDisplayName(match.Qualified, match.Name))
}

// cppResolutionFailure converts a non-resolved ResolveSymbol outcome into a
// fail-closed result that lists candidates instead of answering for a guess.
func cppResolutionFailure(tool string, summary string, resolution semanticcpp.CppResolution, freshness map[string]any) ToolResult {
	lines := make([]string, 0, len(resolution.Candidates))
	for _, candidate := range resolution.Candidates {
		lines = append(lines, cppOwnerDisplay(candidate))
	}

	slices.Sort(lines)

	result := ToolResult{Tool: tool, Summary: summary, Output: strings.Join(lines, "\n"), Freshness: freshness}

	switch resolution.Status {
	case semanticcpp.ResolutionAmbiguous:
		result.Code = cppCodeAmbiguous
		result.Error = fmt.Sprintf("%s: %q names %d different symbols; pass a qualified name (Owner::name) or a path. Candidates are listed in output.", cppCodeAmbiguous, resolution.Requested, len(resolution.Candidates))
	default:
		result.Code = cppCodeNotFound
		result.Error = fmt.Sprintf("%s: no C++ symbol is named exactly %q. Nearest matches (if any) are listed in output.", cppCodeNotFound, resolution.Requested)
	}

	return result
}
