package util

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// RunCmd executes a command in dir and returns its trimmed stdout.
func RunCmd(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(out))
	if err != nil {
		return trimmed, fmt.Errorf("%s %v: %w (%s)", name, args, err, trimmed)
	}
	return trimmed, nil
}

// GitOutput runs a git command in root and returns trimmed stdout.
func GitOutput(ctx context.Context, root string, args ...string) (string, error) {
	allArgs := append([]string{"-C", root}, args...)
	return RunCmd(ctx, "", "git", allArgs...)
}

// GitSnapshot returns the current HEAD commit and porcelain status.
func GitSnapshot(ctx context.Context, root string) (head string, status string, err error) {
	head, err = GitOutput(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	status, err = GitOutput(ctx, root, "status", "--porcelain")
	if err != nil {
		return head, "", err
	}
	return head, status, nil
}
