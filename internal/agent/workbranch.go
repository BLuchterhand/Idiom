package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"
)

type WorkbranchManager struct {
	Root    string
	Scratch string
	Prefix  string
}

type WorkbranchCheckout struct {
	Name    string
	Branch  string
	Path    string
	BaseSHA string
}

type WorkbranchDiff struct {
	ChangedPaths []string
	Stat         string
}

func (m WorkbranchManager) Create(ctx context.Context, name string) (WorkbranchCheckout, error) {
	root := strings.TrimSpace(m.Root)
	if root == "" {
		return WorkbranchCheckout{}, fmt.Errorf("workbranch root is required")
	}
	scratch := strings.TrimSpace(m.Scratch)
	if scratch == "" {
		scratch = os.TempDir()
	}
	prefix := strings.Trim(strings.TrimSpace(m.Prefix), "/")
	if prefix == "" {
		prefix = "codex/work"
	}
	safeName := sanitizeWorkbranchName(name)
	if safeName == "" {
		safeName = "task"
	}
	baseSHA, err := gitOutput(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return WorkbranchCheckout{}, err
	}
	branch := prefix + "/" + safeName
	path := filepath.Join(scratch, "qwen-bubbles-"+safeName)
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return WorkbranchCheckout{}, err
	}
	if _, err := gitOutput(ctx, root, "worktree", "add", "-b", branch, path, strings.TrimSpace(baseSHA)); err != nil {
		return WorkbranchCheckout{}, err
	}
	return WorkbranchCheckout{
		Name:    safeName,
		Branch:  branch,
		Path:    path,
		BaseSHA: strings.TrimSpace(baseSHA),
	}, nil
}

func (m WorkbranchManager) Diff(ctx context.Context, checkout WorkbranchCheckout) (WorkbranchDiff, error) {
	path := strings.TrimSpace(checkout.Path)
	if path == "" {
		return WorkbranchDiff{}, fmt.Errorf("workbranch path is required")
	}
	stat, err := gitOutput(ctx, path, "diff", "--stat")
	if err != nil {
		return WorkbranchDiff{}, err
	}
	names, err := gitOutput(ctx, path, "diff", "--name-only")
	if err != nil {
		return WorkbranchDiff{}, err
	}
	var changed []string
	for _, line := range strings.Split(names, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			changed = append(changed, trimmed)
		}
	}
	return WorkbranchDiff{ChangedPaths: changed, Stat: strings.TrimSpace(stat)}, nil
}

func (m WorkbranchManager) Remove(ctx context.Context, checkout WorkbranchCheckout) error {
	root := strings.TrimSpace(m.Root)
	if root == "" {
		return fmt.Errorf("workbranch root is required")
	}
	path := strings.TrimSpace(checkout.Path)
	if path == "" {
		return fmt.Errorf("workbranch path is required")
	}
	_, err := gitOutput(ctx, root, "worktree", "remove", "--force", path)
	return err
}

func gitOutput(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func sanitizeWorkbranchName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		keep := unicode.IsLetter(r) || unicode.IsDigit(r)
		if keep {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
