package gitstate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"idiom/internal/platform/commandexec"
)

type Snapshot struct {
	Head        string
	Status      string
	StatusLines []string
	Dirty       bool
}

// DirtyPathOptions controls how DirtyPaths projects git porcelain status into
// repository-relative paths.
type DirtyPathOptions struct {
	IgnorePrefixes []string
	Pathspecs      []string
	Sort           bool
}

type HistoryNotApplicableReason string

const (
	HistoryReasonNonGitRoot             HistoryNotApplicableReason = "non_git_root"
	HistoryReasonNoHead                 HistoryNotApplicableReason = "no_head"
	HistoryReasonUntrackedPath          HistoryNotApplicableReason = "untracked_path"
	HistoryReasonPathNoCommittedHistory HistoryNotApplicableReason = "path_no_committed_history"
)

type HistoryApplicabilityResult struct {
	Applicable bool
	Reason     HistoryNotApplicableReason
	Output     string
}

type HistoryLogResult struct {
	Output                string
	HasCommitDescriptions bool
}

type CommandRunner func(ctx context.Context, root string, args ...string) (string, error)

type Runner struct {
	Root       string
	RunCommand CommandRunner
}

func (r Runner) run(ctx context.Context, args ...string) (string, error) {
	if r.RunCommand != nil {
		return r.RunCommand(ctx, r.Root, args...)
	}

	result := CombinedOutput(ctx, r.Root, args...)
	if result.Err != nil {
		return result.Stdout, gitCommandError(args, result)
	}

	return result.Stdout, nil
}

func Current(ctx context.Context, root string) (Snapshot, error) {
	head, err := Output(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return Snapshot{}, err
	}

	status, err := Output(ctx, root, "status", "--porcelain", "--untracked-files=normal", "--ignore-submodules=all")
	if err != nil {
		return Snapshot{}, err
	}

	lines := PorcelainLines(status)

	return Snapshot{
		Head:        strings.TrimSpace(head),
		Status:      strings.TrimSpace(status),
		StatusLines: lines,
		Dirty:       len(lines) > 0,
	}, nil
}

func (s Snapshot) Token() string {
	return strings.TrimSpace(s.Head) + "\n" + strings.TrimSpace(s.Status)
}

func Output(ctx context.Context, root string, args ...string) (string, error) {
	result := commandexec.Run(ctx, gitSpec(root, args...))
	if result.Err != nil {
		return "", gitCommandError(args, result)
	}

	return result.Stdout, nil
}

// DirtyPaths returns normalized repository-relative paths from git status
// porcelain output, including every untracked file.
func DirtyPaths(ctx context.Context, root string, opts DirtyPathOptions) ([]string, error) {
	args := []string{"status", "--porcelain", "--untracked-files=all"}
	var pathspecs []string
	for _, path := range opts.Pathspecs {
		path = strings.TrimSpace(filepath.ToSlash(path))
		if path != "" {
			pathspecs = append(pathspecs, path)
		}
	}
	if len(pathspecs) > 0 {
		args = append(args, "--")
		args = append(args, pathspecs...)
	}

	output, err := Output(ctx, root, args...)
	if err != nil {
		return nil, err
	}

	return dirtyPathsFromPorcelain(output, opts), nil
}

// ChangedPathsSinceFirstPathCommit returns repo-relative paths changed between
// HEAD and the commit before the first commit that touched pathspec. The
// pathspec selects the baseline commit; the diff remains repository-wide so
// callers can apply their own relevance policy.
func ChangedPathsSinceFirstPathCommit(ctx context.Context, root string, pathspec string) ([]string, error) {
	args := []string{"log", "--reverse", "--format=%H"}
	pathspec = strings.TrimSpace(filepath.ToSlash(pathspec))
	if pathspec != "" && pathspec != "." {
		args = append(args, "--", pathspec)
	}

	output, err := Output(ctx, root, args...)
	if err != nil {
		return nil, err
	}

	commits := nonEmptyOutputLines(output)
	if len(commits) == 0 {
		return nil, nil
	}

	paths, err := changedPathsBetween(ctx, root, commits[0]+"^", "HEAD")
	if err != nil {
		paths, err = changedPathsBetween(ctx, root, commits[0], "HEAD")
	}

	return paths, err
}

// CommitChangedPaths returns repo-relative paths named by a commit.
func CommitChangedPaths(ctx context.Context, root string, commit string) ([]string, error) {
	commit = strings.TrimSpace(commit)
	if commit == "" {
		return nil, fmt.Errorf("commit is required")
	}

	output, err := Output(ctx, root, "show", "--name-only", "--format=", commit)
	if err != nil {
		return nil, err
	}

	return nonEmptyOutputLines(output), nil
}

func FilterPorcelainStatusByPathPrefix(output string, ignorePrefixes ...string) string {
	var lines []string

	for _, line := range PorcelainLines(output) {
		path := PathFromPorcelainLine(line)
		ignored := false
		for _, prefix := range ignorePrefixes {
			if strings.HasPrefix(path, filepath.ToSlash(prefix)) {
				ignored = true
				break
			}
		}
		if !ignored {
			lines = append(lines, strings.TrimSpace(line))
		}
	}

	return strings.Join(lines, "\n")
}

func changedPathsBetween(ctx context.Context, root string, from string, to string) ([]string, error) {
	output, err := Output(ctx, root, "diff", "--name-only", from, to)
	if err != nil {
		return nil, err
	}

	return nonEmptyOutputLines(output), nil
}

func CombinedOutput(ctx context.Context, root string, args ...string) commandexec.Result {
	spec := gitSpec(root, args...)
	spec.MergeOutput = true

	return commandexec.Run(ctx, spec)
}

func CombinedOutputText(ctx context.Context, root string, args ...string) (string, error) {
	result := CombinedOutput(ctx, root, args...)
	if result.Err != nil {
		return "", gitCommandError(args, result)
	}

	return result.Stdout, nil
}

func gitCommandError(args []string, result commandexec.Result) error {
	output := result.CombinedOutput()
	if output == "" {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), result.Err)
	}

	return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), result.Err, output)
}

func GitPath(ctx context.Context, root string, path string) (string, error) {
	output, err := Output(ctx, root, "rev-parse", "--git-path", path)
	if err != nil {
		return "", err
	}

	resolved := strings.TrimSpace(output)
	if resolved == "" {
		return "", nil
	}

	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(root, resolved)
	}

	return resolved, nil
}

func (r Runner) Status(ctx context.Context) (string, error) {
	return r.run(ctx, "status", "--porcelain=v1", "--branch", "--untracked-files=normal")
}

func (r Runner) WorktreeDiff(ctx context.Context, pathspec string) (string, error) {
	args := []string{"diff", "--no-color"}
	if strings.TrimSpace(pathspec) != "" {
		args = append(args, "--", filepath.ToSlash(pathspec))
	}

	return r.run(ctx, args...)
}

func (r Runner) CheckHistoryApplicability(ctx context.Context, rel string) (HistoryApplicabilityResult, error) {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if !HasMetadataAncestor(r.Root) {
		return HistoryApplicabilityResult{Reason: HistoryReasonNonGitRoot}, nil
	}

	output, err := r.run(ctx, "rev-parse", "--git-dir")
	if err != nil {
		return HistoryApplicabilityResult{Output: output}, err
	}

	output, err = r.run(ctx, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		if strings.TrimSpace(output) == "" {
			return HistoryApplicabilityResult{Reason: HistoryReasonNoHead}, nil
		}

		return HistoryApplicabilityResult{Output: output}, err
	}

	output, err = r.run(ctx, "ls-files", "--", rel)
	if err != nil {
		return HistoryApplicabilityResult{Output: output}, err
	}
	if !outputContainsPath(output, rel) {
		return HistoryApplicabilityResult{Reason: HistoryReasonUntrackedPath}, nil
	}

	output, err = r.run(ctx, "ls-tree", "--name-only", "-r", "HEAD", "--", rel)
	if err != nil {
		return HistoryApplicabilityResult{Output: output}, err
	}
	if !outputContainsPath(output, rel) {
		return HistoryApplicabilityResult{Reason: HistoryReasonPathNoCommittedHistory}, nil
	}

	return HistoryApplicabilityResult{Applicable: true}, nil
}

func (r Runner) HistoryRange(ctx context.Context, rel string, startLine int, endLine int) (HistoryLogResult, error) {
	spec := fmt.Sprintf("%d,%d:%s", startLine, endLine, filepath.ToSlash(rel))
	return r.historyLog(ctx, spec)
}

func (r Runner) HistorySymbol(ctx context.Context, rel string, symbol string) (HistoryLogResult, error) {
	spec := fmt.Sprintf(":%s:%s", symbol, filepath.ToSlash(rel))
	return r.historyLog(ctx, spec)
}

func (r Runner) historyLog(ctx context.Context, spec string) (HistoryLogResult, error) {
	output, err := r.run(ctx, "log", "--no-color", "--decorate=short", "-L", spec)
	return HistoryLogResult{
		Output:                output,
		HasCommitDescriptions: historyLogHasCommitDescription(output),
	}, err
}

func historyLogHasCommitDescription(output string) bool {
	output = strings.TrimSpace(output)
	return strings.HasPrefix(output, "commit ") || strings.Contains(output, "\ncommit ")
}

func (r Runner) BlameRange(ctx context.Context, rel string, startLine int, endLine int) (string, error) {
	args := []string{"blame", "--date=short"}

	if startLine > 0 || endLine > 0 {
		start := startLine
		if start <= 0 {
			start = 1
		}

		if endLine > 0 {
			args = append(args, "-L", fmt.Sprintf("%d,%d", start, endLine))
		} else {
			args = append(args, "-L", fmt.Sprintf("%d,+80", start))
		}
	}

	args = append(args, "--", filepath.ToSlash(rel))

	return r.run(ctx, args...)
}

// MetadataRoot returns the absolute nearest ancestor containing Git metadata.
func MetadataRoot(root string) (string, bool) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", false
	}

	dir, err := filepath.Abs(root)
	if err != nil {
		dir = root
	}

	for {
		gitPath := filepath.Join(dir, ".git")
		if info, err := os.Stat(gitPath); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return dir, true
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}

		dir = parent
	}
}

func HasMetadataAncestor(root string) bool {
	_, ok := MetadataRoot(root)
	return ok
}

func PorcelainLines(output string) []string {
	var lines []string

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}

	return lines
}

func dirtyPathsFromPorcelain(output string, opts DirtyPathOptions) []string {
	seen := map[string]bool{}
	paths := []string{}

	for _, line := range PorcelainLines(output) {
		path := PathFromPorcelainLine(line)
		ignored := false
		for _, prefix := range opts.IgnorePrefixes {
			if strings.HasPrefix(path, prefix) {
				ignored = true
				break
			}
		}
		if path == "" || ignored || seen[path] {
			continue
		}

		seen[path] = true
		paths = append(paths, path)
	}

	if opts.Sort {
		sort.Strings(paths)
	}

	return paths
}

func nonEmptyOutputLines(output string) []string {
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, filepath.ToSlash(line))
		}
	}

	return lines
}

func PathFromPorcelainLine(line string) string {
	line = strings.TrimRight(line, "\r\n")
	if len(line) < 4 {
		return ""
	}

	status := line[:2]
	payload := strings.TrimSpace(line[3:])
	if payload == "" {
		return ""
	}

	if strings.ContainsAny(status, "RC") {
		idx := porcelainRenameSeparatorIndex(payload)
		if idx >= 0 {
			payload = payload[idx+4:]
		}
	}

	payload = strings.TrimSpace(payload)
	if unquoted, err := strconv.Unquote(payload); err == nil {
		payload = unquoted
	} else {
		payload = strings.Trim(payload, `"`)
	}

	return filepath.ToSlash(payload)
}

func outputContainsPath(output string, rel string) bool {
	rel = strings.TrimSpace(filepath.ToSlash(rel))
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(filepath.ToSlash(line)) == rel {
			return true
		}
	}

	return false
}

func porcelainRenameSeparatorIndex(payload string) int {
	inQuote := false
	escaped := false
	last := -1

	for i := 0; i < len(payload); i++ {
		c := payload[i]
		if escaped {
			escaped = false
			continue
		}
		if inQuote && c == '\\' {
			escaped = true
			continue
		}
		if c == '"' {
			inQuote = !inQuote
			continue
		}
		if !inQuote && strings.HasPrefix(payload[i:], " -> ") {
			last = i
			i += len(" -> ") - 1
		}
	}

	return last
}

func gitSpec(root string, args ...string) commandexec.Spec {
	gitArgs := make([]string, 0, len(args)+2)
	if strings.TrimSpace(root) != "" {
		gitArgs = append(gitArgs, "-C", root)
	}

	gitArgs = append(gitArgs, args...)

	return commandexec.Spec{
		Name: "git",
		Args: gitArgs,
	}
}
