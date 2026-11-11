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

	"idiom/internal/platform/gitstate"
	semanticcpp "idiom/internal/repository/semantic/cpp"
)

const (
	// cppCollectorVersion changes whenever the syntax collector's output changes,
	// so an index identity never spans two collector behaviours.
	cppCollectorVersion = "cpp-syntax-index/3"
	// cppIndexBudget bounds one in-process C++ index build; beyond it the tool
	// fails with cpp_index_budget_exceeded and the phase timings.
	cppIndexBudget = 10 * time.Second
	// cppClangdBudget bounds the optional clangd call-hierarchy attempt so it can
	// never consume the tool's whole timeout before the syntax index answers.
	cppClangdBudget = 5 * time.Second
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
	Key          string
	Root         string
	GitCommonDir string
	Commit       string
	Tree         string
	DirtyPaths   []string
	IgnoredPaths []string
	GitError     string
	Git          time.Duration
	Build        semanticcpp.CppBuildTimings
	Symbols      int
	Memoized     bool
}

// cppSnapshot reads the Git identity of the workspace and a content hash of its
// dirty C++ files. It is the only per-call Git work and is cheap (two commands).
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

	dirty, err := gitstate.DirtyPaths(ctx, root, gitstate.DirtyPathOptions{Pathspecs: cppGitPathspecs, Sort: true})
	if err != nil {
		info.GitError = err.Error()
		info.Git = time.Since(started)

		return info
	}

	info.DirtyPaths = dirty

	// The walk indexes gitignored C++ files too, so they are part of the identity.
	ignoredArgs := append([]string{"ls-files", "-o", "-i", "--exclude-standard", "--"}, cppGitPathspecs...)

	ignored, err := gitstate.Output(ctx, root, ignoredArgs...)
	if err != nil {
		info.GitError = err.Error()
		info.Git = time.Since(started)

		return info
	}

	for _, line := range strings.Split(ignored, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			info.IgnoredPaths = append(info.IgnoredPaths, line)
		}
	}

	slices.Sort(info.IgnoredPaths)

	info.Key = cppIndexKey(root, info)
	info.Git = time.Since(started)

	return info
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
			"git":   info.Git.Milliseconds(),
			"walk":  info.Build.Walk.Milliseconds(),
			"parse": info.Build.Parse.Milliseconds(),
			"link":  info.Build.Link.Milliseconds(),
		},
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
