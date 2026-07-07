package gitstate

import (
	"context"
	"path/filepath"
	"strings"

	"idiom/internal/platform/commandexec"
)

type Snapshot struct {
	Head        string
	Status      string
	StatusLines []string
	Dirty       bool
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
	result := Run(ctx, root, args...)
	if result.Err != nil {
		return "", result.Err
	}

	return result.Stdout, nil
}

func CombinedOutput(ctx context.Context, root string, args ...string) commandexec.Result {
	spec := gitSpec(root, args...)
	spec.MergeOutput = true

	return commandexec.Run(ctx, spec)
}

func Run(ctx context.Context, root string, args ...string) commandexec.Result {
	return commandexec.Run(ctx, gitSpec(root, args...))
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

func PathFromPorcelainLine(line string) string {
	line = strings.TrimRight(line, "\r\n")
	if len(line) < 4 {
		return ""
	}

	payload := strings.TrimSpace(line[3:])
	if payload == "" {
		return ""
	}

	if idx := strings.LastIndex(payload, " -> "); idx >= 0 {
		payload = payload[idx+4:]
	}

	return filepath.ToSlash(strings.TrimSpace(payload))
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
