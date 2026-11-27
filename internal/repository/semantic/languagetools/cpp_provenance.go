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
	"idiom/internal/platform/safepath"
	"idiom/internal/repository/discovery"
	semanticcpp "idiom/internal/repository/semantic/cpp"
)

const (
	// cppCollectorVersion changes whenever the syntax collector's output changes,
	// so an index identity never spans two collector behaviours.
	// /4: main (alias maps, comment blanking) and the resumable per-file
	// checkpoint each moved /2 to /3 independently; merged files carry aliases.
	cppCollectorVersion = "cpp-syntax-index/4"
	// Leave room inside the 30s tool envelope for checkpoint IO and the optional
	// clangd attempt. A timeout retains parsed files for the next process.
	cppDefaultIndexBudget = 20 * time.Second
	// Fresh CLI processes can spend the whole attempt initializing clangd. Keep
	// this below the warm-query target; syntax/text evidence remains available.
	cppDefaultClangdBudget = 500 * time.Millisecond
	// cppFreshnessPathLimit caps the dirty paths echoed in a freshness report.
	cppFreshnessPathLimit = 20
)

var cppGitPathspecs = []string{
	":(icase)*.c", ":(icase)*.cc", ":(icase)*.cpp", ":(icase)*.cxx", ":(icase)*.h", ":(icase)*.hh", ":(icase)*.hpp", ":(icase)*.hxx", ":(icase)*.ipp", ":(icase)*.inl", ":(icase)*.tpp",
}

func cppIndexPathspecs() []string {
	paths := slices.Clone(cppGitPathspecs)
	for _, name := range discovery.SkippedDirNames() {
		paths = append(paths, ":(exclude,glob)**/"+name+"/**")
	}
	return paths
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
	Key           string
	Root          string
	GitCommonDir  string
	Commit        string
	Tree          string
	DirtyPaths    []string
	IgnoredPaths  []string
	GitError      string
	Git           time.Duration
	Build         semanticcpp.CppBuildTimings
	Symbols       int
	Memoized      bool
	ContentHashes map[string]string
	Cache         string
	CacheError    string
	Load          time.Duration
	Budget        time.Duration
	Checkpoint    bool
}

func cppBudgetSetting(name string, fallback time.Duration) time.Duration {
	if raw := strings.TrimSpace(os.Getenv(name)); raw != "" {
		if budget, err := time.ParseDuration(raw); err == nil && budget > 0 {
			return budget
		}
	}
	return fallback
}

// cppSnapshot reads the Git identity of the workspace and a content hash of its
// dirty C++ files. Three Git commands collect identity, status and ignored paths.
func cppSnapshot(ctx context.Context, root string) cppIndexInfo {
	started := time.Now()
	info := cppIndexInfo{Root: root}

	output, err := gitstate.Output(ctx, root, "rev-parse", "--git-common-dir", "HEAD", "HEAD^{tree}")
	if err != nil {
		info.GitError = err.Error()
		info.Git = time.Since(started)

		return info
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
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

	pathspecs := cppIndexPathspecs()
	dirty, err := gitstate.DirtyPaths(ctx, root, gitstate.DirtyPathOptions{Pathspecs: pathspecs, Sort: true})
	if err != nil {
		info.GitError = err.Error()
		info.Git = time.Since(started)

		return info
	}

	for _, path := range dirty {
		if semanticcpp.IsIndexPath(path) {
			info.DirtyPaths = append(info.DirtyPaths, path)
		}
	}

	// The walk indexes gitignored C++ files too, so they are part of the identity.
	// Negative pathspecs alone do not stop ls-files from descending ignored
	// nested worktrees. Positive top-level roots prune those trees before Git
	// enumerates them, matching the collector's directory filter.
	entries, err := os.ReadDir(root)
	if err != nil {
		info.GitError = err.Error()
		info.Git = time.Since(started)
		return info
	}
	var ignoredRoots []string
	for _, entry := range entries {
		if (entry.IsDir() && !discovery.ShouldSkipDirName(entry.Name())) || (!entry.IsDir() && isCppExtension(filepath.Ext(entry.Name()))) {
			ignoredRoots = append(ignoredRoots, ":(literal)"+entry.Name())
		}
	}
	if len(ignoredRoots) == 0 {
		ignoredRoots = []string{":(exclude)**"}
	}
	ignoredArgs := append([]string{"ls-files", "-z", "-o", "-i", "--exclude-standard", "--"}, ignoredRoots...)

	ignored, err := gitstate.Output(ctx, root, ignoredArgs...)
	if err != nil {
		info.GitError = err.Error()
		info.Git = time.Since(started)

		return info
	}

	for _, line := range strings.Split(ignored, "\x00") {
		if line != "" && semanticcpp.IsIndexPath(line) {
			info.IgnoredPaths = append(info.IgnoredPaths, line)
		}
	}

	slices.Sort(info.IgnoredPaths)

	info.ContentHashes = make(map[string]string)
	info.Key, err = cppIndexKey(ctx, root, info)
	if err != nil {
		info.GitError = err.Error()
	}
	info.Git = time.Since(started)

	return info
}

func cppIndexKey(ctx context.Context, root string, info cppIndexInfo) (string, error) {
	hash := sha256.New()
	fmt.Fprintf(hash, "%s\n%s\n%s\n", cppCollectorVersion, info.GitCommonDir, info.Tree)

	for _, group := range []struct {
		label string
		paths []string
	}{{"dirty", info.DirtyPaths}, {"ignored", info.IgnoredPaths}} {
		for _, rel := range group.paths {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			content, err := safepath.ReadFileUnderRoot(root, filepath.Join(root, filepath.FromSlash(rel)), "C++ cache identity", safepath.Options{})
			if err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					return "", err
				}
				fmt.Fprintf(hash, "%s\x00%s\x00deleted\n", group.label, rel)
				if info.ContentHashes != nil {
					info.ContentHashes[rel] = "deleted"
				}

				continue
			}

			sum := sha256.Sum256(content)
			if info.ContentHashes != nil {
				info.ContentHashes[rel] = hex.EncodeToString(sum[:])
			}
			fmt.Fprintf(hash, "%s\x00%s\x00%x\n", group.label, rel, sum)
		}
	}

	return hex.EncodeToString(hash.Sum(nil))[:16], nil
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
	} else if info.Cache != "" {
		build = info.Cache
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
		"answered_by":      answeredBy,
		"query_ms":         query.Milliseconds(),
		"budget_ms":        info.Budget.Milliseconds(),
		"cache_load_ms":    info.Load.Milliseconds(),
		"parsed_files":     info.Build.Parsed,
		"reused_files":     info.Build.Reused,
		"checkpoint_saved": info.Checkpoint,
	}
	if info.CacheError != "" {
		report["cache_error"] = info.CacheError
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
		Error:     fmt.Sprintf("%s: %v (budget %s; checkpoint saved: %t)", cppCodeBudgetExceeded, budget, info.Budget, info.Checkpoint),
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
