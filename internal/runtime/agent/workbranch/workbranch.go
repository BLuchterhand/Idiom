package workbranch

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

type Manager struct {
	Root    string
	Scratch string
	Prefix  string
}

type Checkout struct {
	Name    string
	Branch  string
	Path    string
	BaseSHA string
}

type Diff struct {
	ChangedPaths []string
	Stat         string
	Patch        string
}

func (m Manager) Create(ctx context.Context, name string) (Checkout, error) {
	root := strings.TrimSpace(m.Root)
	if root == "" {
		return Checkout{}, fmt.Errorf("workbranch root is required")
	}

	scratch := strings.TrimSpace(m.Scratch)
	if scratch == "" {
		scratch = os.TempDir()
	}

	prefix := strings.Trim(strings.TrimSpace(m.Prefix), "/")
	if prefix == "" {
		prefix = "codex/work"
	}

	safeName := sanitizeName(name)
	if safeName == "" {
		safeName = "task"
	}

	baseSHA, err := gitOutput(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return Checkout{}, err
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	branch := prefix + "/" + safeName + "-" + suffix
	path := filepath.Join(scratch, "idiom-"+safeName+"-"+suffix)

	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return Checkout{}, err
	}

	if _, err := gitOutput(ctx, root, "worktree", "add", "-b", branch, path, strings.TrimSpace(baseSHA)); err != nil {
		return Checkout{}, err
	}

	return Checkout{
		Name:    safeName,
		Branch:  branch,
		Path:    path,
		BaseSHA: strings.TrimSpace(baseSHA),
	}, nil
}

func (m Manager) Diff(ctx context.Context, checkout Checkout) (Diff, error) {
	path := strings.TrimSpace(checkout.Path)
	if path == "" {
		return Diff{}, fmt.Errorf("workbranch path is required")
	}

	base := strings.TrimSpace(checkout.BaseSHA)
	if base == "" {
		base = "HEAD"
	}

	if _, err := gitOutput(ctx, path, "add", "-N", "."); err != nil {
		return Diff{}, err
	}

	stat, err := gitOutput(ctx, path, "diff", "--stat", base, "--")
	if err != nil {
		return Diff{}, err
	}

	names, err := gitOutput(ctx, path, "diff", "--name-only", base, "--")
	if err != nil {
		return Diff{}, err
	}

	patch, err := gitOutput(ctx, path, "diff", "--binary", base, "--")
	if err != nil {
		return Diff{}, err
	}

	var changed []string

	for _, line := range strings.Split(names, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			changed = append(changed, trimmed)
		}
	}

	return Diff{ChangedPaths: changed, Stat: strings.TrimSpace(stat), Patch: strings.TrimSpace(patch)}, nil
}

func (m Manager) Remove(ctx context.Context, checkout Checkout) error {
	root := strings.TrimSpace(m.Root)
	if root == "" {
		return fmt.Errorf("workbranch root is required")
	}

	path := strings.TrimSpace(checkout.Path)
	if path == "" {
		return fmt.Errorf("workbranch path is required")
	}

	if _, err := gitOutput(ctx, root, "worktree", "remove", "--force", path); err != nil {
		return err
	}

	if branch := strings.TrimSpace(checkout.Branch); branch != "" {
		if _, err := gitOutput(ctx, root, "branch", "-D", branch); err != nil {
			return err
		}
	}

	return nil
}

func gitOutput(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}

	return string(output), nil
}

func sanitizeName(name string) string {
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
