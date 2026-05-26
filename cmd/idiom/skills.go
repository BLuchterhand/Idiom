package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/cliassets"
)

// runSkillsMain dispatches `idiom skills <subcommand>`. Phase 7.3
// ships `install` and `list`; the surface is structured for future
// `update` / `diff` commands without rewiring main.go.
func runSkillsMain(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printSkillsUsage(stderr)
		return 0
	}
	switch args[0] {
	case "list":
		return runSkillsList(args[1:], stdout, stderr)
	case "install":
		return runSkillsInstall(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "skills: unknown subcommand %q\n", args[0])
		printSkillsUsage(stderr)
		return 2
	}
}

// runSkillsList enumerates the canonical skills idiom can install.
// Operators discover what's available before picking one with
// install. Output is one skill per line, sorted alphabetically so
// scripted callers get stable output.
func runSkillsList(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("idiom skills list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	names, err := canonicalSkillNames()
	if err != nil {
		fmt.Fprintf(stderr, "skills list: %v\n", err)
		return 1
	}
	for _, n := range names {
		fmt.Fprintln(stdout, n)
	}
	return 0
}

// runSkillsInstall copies a canonical skill template into the
// project's orchestrator-skills directory. Default target is
// .claude/skills/ (the convention claude-code uses); --target
// overrides for projects on a different orchestrator harness.
//
// Refuses to overwrite an existing SKILL.md without --force; the
// canonical version is starting-point content the operator
// customizes, and silent overwrites would lose that work.
func runSkillsInstall(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("idiom skills install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		target      string
		projectRoot string
		force       bool
	)
	fs.StringVar(&target, "target", ".claude/skills", "directory under project root to install into (e.g. .claude/skills, .codex/skills)")
	fs.StringVar(&projectRoot, "project-root", "", "project root (defaults to cwd)")
	fs.BoolVar(&force, "force", false, "overwrite an existing SKILL.md (default: refuse and exit 2)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	tail := fs.Args()
	if len(tail) != 1 {
		fmt.Fprintln(stderr, "skills install: exactly one skill name is required")
		printSkillsUsage(stderr)
		return 2
	}
	name := strings.TrimSpace(tail[0])
	if name == "" || strings.ContainsAny(name, `/\`+"\x00") || strings.Contains(name, "..") {
		fmt.Fprintf(stderr, "skills install: invalid skill name %q (must be a single name without path separators)\n", name)
		return 2
	}

	body, err := cliassets.Skill(name)
	if err != nil {
		fmt.Fprintf(stderr, "skills install: unknown skill %q (run `idiom skills list` to see available)\n", name)
		return 1
	}

	root := strings.TrimSpace(projectRoot)
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "skills install: %v\n", err)
			return 1
		}
		root = cwd
	}
	dest := filepath.Join(root, target, name, "SKILL.md")

	if _, err := os.Stat(dest); err == nil && !force {
		fmt.Fprintf(stderr, "skills install: %s already exists (use --force to overwrite)\n", dest)
		return 2
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		fmt.Fprintf(stderr, "skills install: mkdir: %v\n", err)
		return 1
	}
	if err := os.WriteFile(dest, body, 0o644); err != nil {
		fmt.Fprintf(stderr, "skills install: write: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "installed %s\n", dest)
	fmt.Fprintf(stdout, "Customize for this project, then commit alongside any orchestrator config.\n")
	return 0
}

// canonicalSkillNames returns the sorted list of skill names idiom
// ships templates for. Each name corresponds to a subdirectory under
// the embedded skills/ root containing a SKILL.md.
func canonicalSkillNames() ([]string, error) {
	return cliassets.SkillNames()
}

func printSkillsUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "usage: idiom skills <subcommand> [flags]")
	fmt.Fprintln(stderr, "subcommands:")
	fmt.Fprintln(stderr, "  list                       print available canonical skill templates")
	fmt.Fprintln(stderr, "  install <name>             copy a skill template into <project>/.claude/skills/<name>/SKILL.md")
	fmt.Fprintln(stderr, "                             flags: --target <dir>  --project-root <path>  --force")
}
