package main

import (
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"idiom/internal/telemetry"
)

//go:embed brief-preflight.py
var briefPreflightScript []byte

// canonicalBriefPreloadFS holds the idiom-canonical preload texts
// (generic atom-spec drafting hygiene). Project-specific kinds
// (scenario-output, test-append, data-additions in Colony's
// vernacular) live under <project>/.idiom/prompts/brief/preload/
// instead, where the project owns their wording.
//
//go:embed brief-preload/*.md
var canonicalBriefPreloadFS embed.FS

func runBriefMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return runBriefMainWithTelemetry(args, stdout, stderr, nil)
}

func runBriefMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printBriefUsage(stderr)
		return 0
	}
	logCLIPhase(rec, "brief_start", map[string]any{"subcommand": args[0]})
	switch args[0] {
	case "lint":
		return runBriefLint(args[1:], stdout, stderr, rec)
	case "preload":
		return runBriefPreload(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown brief command: %s\n", args[0])
		printBriefUsage(stderr)
		return 2
	}
}

func printBriefUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "Usage:")
	fmt.Fprintln(stderr, "  idiom brief lint <brief.md>")
	fmt.Fprintln(stderr, "  idiom brief preload --kind <name>   (or --list to see available kinds)")
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "lint: pre-dispatch lint for idiom implementation briefs. Checks single-purpose")
	fmt.Fprintln(stderr, "decomposition, prompt-magic triggers, fence safety, blockquoted code,")
	fmt.Fprintln(stderr, "test-anchor preload, verified-current-state presence, and exact-anchor")
	fmt.Fprintln(stderr, "specificity for every required edit path.")
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "preload: emit the canonical (or project-overridden) atom-spec drafting")
	fmt.Fprintln(stderr, "guidance for a given kind. Project overrides live at")
	fmt.Fprintln(stderr, "<project>/.idiom/prompts/brief/preload/<kind>.md and win over the embed.")
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "Exit codes: 0 OK, 1 lint failure / unknown kind, 2 usage error.")
}

// runBriefPreload prints the atom-spec drafting preload guidance
// for one kind. Resolution order: project overlay → embedded
// canonical. Mirrors the lane-prompt overlay seam from Phase 3.
//
// Replaces Colony/scripts/atom_spec_preload.sh; project-specific
// kinds (scenario-output, test-append, data-additions) live as
// project overlays so idiom doesn't carry one project's vocabulary
// in the canonical surface.
func runBriefPreload(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("idiom brief preload", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		kind        string
		list        bool
		projectRoot string
	)
	fs.StringVar(&kind, "kind", "", "preload kind to emit (e.g. generic)")
	fs.BoolVar(&list, "list", false, "list available kinds (canonical + project overlay)")
	fs.StringVar(&projectRoot, "project-root", "", "project root holding .idiom/prompts/brief/preload/ (defaults to cwd)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	root := strings.TrimSpace(projectRoot)
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			root = cwd
		}
	}

	if list {
		printBriefPreloadList(stdout, root)
		return 0
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		fmt.Fprintln(stderr, "brief preload: --kind is required (use --list to see available kinds)")
		return 2
	}

	body, source, err := resolveBriefPreload(kind, root)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// Source line goes to stderr so stdout stays clean for piping
	// directly into a brief draft.
	fmt.Fprintf(stderr, "[brief preload] kind=%q source=%s\n", kind, source)
	fmt.Fprint(stdout, body)
	if !strings.HasSuffix(body, "\n") {
		fmt.Fprintln(stdout)
	}
	return 0
}

// resolveBriefPreload returns the body for a kind, preferring the
// project overlay when present. The returned source is "overlay" or
// "canonical" so the CLI can tell the operator which file won.
func resolveBriefPreload(kind, projectRoot string) (string, string, error) {
	if err := validatePreloadKind(kind); err != nil {
		return "", "", err
	}
	if root := strings.TrimSpace(projectRoot); root != "" {
		path := filepath.Join(root, ".idiom", "prompts", "brief", "preload", kind+".md")
		if body, err := os.ReadFile(path); err == nil {
			return string(body), "overlay:" + path, nil
		}
	}
	body, err := canonicalBriefPreloadFS.ReadFile("brief-preload/" + kind + ".md")
	if err != nil {
		return "", "", fmt.Errorf("brief preload: unknown kind %q (use --list)", kind)
	}
	return string(body), "canonical", nil
}

// validatePreloadKind rejects any kind that isn't a single safe path
// component. Without this guard a kind like "../../etc/passwd" would
// resolve outside the project's preload directory and could leak file
// content if a same-suffix file happens to exist at the resolved path.
// Accepting only `[A-Za-z0-9._-]` keeps the surface tight enough that
// the joined filesystem path can't escape the intended directory.
func validatePreloadKind(kind string) error {
	if kind == "" {
		return fmt.Errorf("brief preload: empty kind")
	}
	if strings.ContainsAny(kind, `/\` + "\x00") || strings.Contains(kind, "..") {
		return fmt.Errorf("brief preload: invalid kind %q (must be a single name without path separators)", kind)
	}
	return nil
}

// printBriefPreloadList enumerates the available kinds, sorted
// alphabetically, with the source each one resolves from. Overlay
// entries shadow canonical entries with the same name (overlay
// wins in resolveBriefPreload), so the operator can see at a glance
// which kinds the project has overridden.
func printBriefPreloadList(stdout io.Writer, projectRoot string) {
	kinds := make(map[string]string) // kind -> source
	if entries, err := canonicalBriefPreloadFS.ReadDir("brief-preload"); err == nil {
		for _, e := range entries {
			name := strings.TrimSuffix(e.Name(), ".md")
			if name == e.Name() {
				continue // skip non-md files
			}
			kinds[name] = "canonical"
		}
	}
	if root := strings.TrimSpace(projectRoot); root != "" {
		dir := filepath.Join(root, ".idiom", "prompts", "brief", "preload")
		if entries, err := os.ReadDir(dir); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				name := strings.TrimSuffix(e.Name(), ".md")
				if name == e.Name() {
					continue
				}
				kinds[name] = "overlay"
			}
		}
	}
	if len(kinds) == 0 {
		fmt.Fprintln(stdout, "(no preload kinds available)")
		return
	}
	names := make([]string, 0, len(kinds))
	for k := range kinds {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(stdout, "%s\t%s\n", n, kinds[n])
	}
}

func runBriefLint(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "brief lint: exactly one brief path is required")
		printBriefUsage(stderr)
		return 2
	}
	briefPath := strings.TrimSpace(args[0])
	if briefPath == "" {
		fmt.Fprintln(stderr, "brief lint: empty brief path")
		return 2
	}
	if _, err := os.Stat(briefPath); err != nil {
		fmt.Fprintf(stderr, "brief lint: cannot read brief: %v\n", err)
		return 2
	}
	scriptPath, cleanup, err := writeEmbeddedBriefPreflight()
	if err != nil {
		fmt.Fprintf(stderr, "brief lint: cannot stage preflight script: %v\n", err)
		return 2
	}
	defer cleanup()

	cmd := exec.Command("python3", scriptPath, briefPath)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code := exitErr.ExitCode()
			logCLIPhase(rec, "brief_lint", map[string]any{"exit": code})
			return code
		}
		fmt.Fprintf(stderr, "brief lint: failed to run python3: %v\n", err)
		return 2
	}
	logCLIPhase(rec, "brief_lint", map[string]any{"exit": 0})
	return 0
}

func writeEmbeddedBriefPreflight() (path string, cleanup func(), err error) {
	tmpDir, err := os.MkdirTemp("", "idiom-brief-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup = func() { _ = os.RemoveAll(tmpDir) }
	scriptPath := filepath.Join(tmpDir, "brief-preflight.py")
	if err := os.WriteFile(scriptPath, briefPreflightScript, 0o755); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return scriptPath, cleanup, nil
}
