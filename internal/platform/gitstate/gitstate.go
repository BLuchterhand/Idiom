package gitstate

import (
	"context"
	"fmt"
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
	Sort           bool
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
	output, err := Output(ctx, root, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return nil, err
	}

	return dirtyPathsFromPorcelain(output, opts), nil
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
	output := strings.TrimSpace(commandexec.CombineOutput(result.Stdout, result.Stderr))
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
