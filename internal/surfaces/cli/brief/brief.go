package brief

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"idiom/internal/evidence/telemetry"
	"idiom/internal/surfaces/cliassets"
	"idiom/internal/workflows/brieflint"
	"idiom/internal/workflows/commandcheck"
)

func RunMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printBriefUsage(stderr)

		return 0
	}

	telemetry.LogPhase(rec, "brief_start", map[string]any{"subcommand": args[0]})

	switch args[0] {
	case "lint":
		return RunLint(args[1:], stdout, stderr, rec)
	case "preload":
		return runBriefPreload(args[1:], stdout, stderr)
	case "preview":
		return runBriefPreview(args[1:], stdout, stderr, rec)
	default:
		fmt.Fprintf(stderr, "unknown brief command: %s\n", args[0])
		printBriefUsage(stderr)

		return 2
	}
}

func printBriefUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "Usage:")
	fmt.Fprintln(stderr, "  idiom brief lint <brief.md>")
	fmt.Fprintln(stderr, "  idiom brief preview <brief.md>")
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

	body, err := cliassets.BriefPreload(kind)
	if err != nil {
		return "", "", fmt.Errorf("brief preload: unknown kind %q (use --list)", kind)
	}

	return body, "canonical", nil
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

	if strings.ContainsAny(kind, `/\`+"\x00") || strings.Contains(kind, "..") {
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

	if names, err := cliassets.BriefPreloadKinds(); err == nil {
		for _, name := range names {
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

	slices.Sort(names)

	for _, n := range names {
		fmt.Fprintf(stdout, "%s\t%s\n", n, kinds[n])
	}
}

func RunLint(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	emitJSON := false
	briefArgs := args

	for i, a := range args {
		if a == "--json" {
			emitJSON = true

			briefArgs = append([]string{}, args[:i]...)
			briefArgs = append(briefArgs, args[i+1:]...)

			break
		}
	}

	if len(briefArgs) != 1 {
		fmt.Fprintln(stderr, "brief lint: exactly one brief path is required")
		printBriefUsage(stderr)

		return 2
	}

	briefPath := strings.TrimSpace(briefArgs[0])
	if briefPath == "" {
		fmt.Fprintln(stderr, "brief lint: empty brief path")

		return 2
	}

	if _, err := os.Stat(briefPath); err != nil {
		fmt.Fprintf(stderr, "brief lint: cannot read brief: %v\n", err)

		return 2
	}

	if emitJSON {
		report, err := brieflint.BuildReport(context.Background(), briefPath, "", 0)
		if err != nil {
			fmt.Fprintf(stderr, "brief lint: %v\n", err)

			return 2
		}
		blob, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "brief lint: json marshal failed: %v\n", err)

			return 2
		}

		fmt.Fprintln(stdout, string(blob))
		telemetry.LogPhase(rec, "brief_lint", map[string]any{"exit": report.Exit, "json": true, "findings": len(report.Findings)})

		return report.Exit
	}

	result, err := brieflint.RunEmbeddedPreflight(context.Background(), briefPath, "", 0)
	if err != nil {
		fmt.Fprintf(stderr, "brief lint: %v\n", err)

		return 2
	}

	if result.Stdout != "" {
		fmt.Fprint(stdout, result.Stdout)
	}
	if result.Stderr != "" {
		fmt.Fprint(stderr, result.Stderr)
	}

	if result.Failure != commandcheck.FailureNone {
		if result.Failure == commandcheck.FailureExit {
			telemetry.LogPhase(rec, "brief_lint", map[string]any{"exit": result.ExitCode})

			return result.ExitCode
		}

		fmt.Fprintf(stderr, "brief lint: failed to run python3: %v\n", result.Err)

		return result.ExitCode
	}

	telemetry.LogPhase(rec, "brief_lint", map[string]any{"exit": 0})

	return 0
}

func runBriefPreview(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "brief preview: exactly one brief path is required")
		printBriefUsage(stderr)

		return 2
	}

	briefPath := strings.TrimSpace(args[0])
	if briefPath == "" {
		fmt.Fprintln(stderr, "brief preview: empty brief path")

		return 2
	}

	data, err := os.ReadFile(briefPath)
	if err != nil {
		fmt.Fprintf(stderr, "brief preview: cannot read brief: %v\n", err)

		return 2
	}

	content := string(data)

	// H1 title.
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "# ") {
			fmt.Fprintf(stdout, "Title: %s\n", strings.TrimPrefix(line, "# "))

			break
		}
	}

	// Files To Modify count and paths.
	fmCount := 0
	fmPaths := []string{}
	inFM := false

	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "## Files To Modify") {
			inFM = true
			continue
		}

		if inFM && strings.HasPrefix(line, "|") && !strings.HasPrefix(line, "|------") {
			parts := strings.Split(line, "|")
			if len(parts) >= 2 {
				first := strings.TrimSpace(parts[1])
				// Skip empty rows and the header (first cell == "File"/"Path").
				if first == "" || strings.EqualFold(first, "File") || strings.EqualFold(first, "Path") {
					continue
				}

				fmCount++

				fmPaths = append(fmPaths, first)
			}
		}

		if inFM && strings.HasPrefix(line, "## ") && !strings.HasPrefix(line, "## Files To Modify") {
			inFM = false
		}
	}

	fmt.Fprintf(stdout, "Files To Modify: %d\n", fmCount)

	for _, p := range fmPaths {
		fmt.Fprintf(stdout, "  - %s\n", p)
	}

	// Code-fence count.
	fenceCount := 0
	inFence := false

	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "```") {
			if inFence {
				inFence = false
			} else {
				inFence = true
				fenceCount++
			}
		}
	}

	fmt.Fprintf(stdout, "Code fences: %d\n", fenceCount)

	// Required Behavior step count.
	rbCount := 0
	inRB := false

	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "## Required Behavior") {
			inRB = true
			continue
		}

		if inRB && strings.HasPrefix(line, "## ") && !strings.HasPrefix(line, "## Required Behavior") {
			inRB = false
		}

		if inRB {
			// top-level numbered items: "1.", "2.", etc.
			if strings.HasPrefix(line, "### Required Edit") {
				rbCount++
			}
		}
	}

	fmt.Fprintf(stdout, "Required Behavior steps: %d\n", rbCount)

	// Lint result.
	fmt.Fprintln(stdout, "---")

	lintExit, lintFailures := runBriefLintResult(content, stderr)
	if lintExit == 0 {
		fmt.Fprintln(stdout, "Lint: OK")
	} else {
		fmt.Fprintf(stdout, "Lint: FAIL (exit %d)\n", lintExit)

		for _, f := range lintFailures {
			fmt.Fprintf(stdout, "  - %s\n", f)
		}
	}

	telemetry.LogPhase(rec, "brief_preview", map[string]any{"path": briefPath})

	return 0
}

func runBriefLintResult(content string, stderr io.Writer) (int, []string) {
	tmpBrief, err := os.CreateTemp("", "idiom-preview-*.md")
	if err != nil {
		return 2, []string{fmt.Sprintf("cannot create temp brief: %v", err)}
	}

	tmpPath := tmpBrief.Name()

	tmpBrief.Close()

	if err := os.WriteFile(tmpPath, []byte(content), 0o644); err != nil {
		return 2, []string{fmt.Sprintf("cannot write temp brief: %v", err)}
	}

	defer os.Remove(tmpPath)

	result, err := brieflint.RunEmbeddedPreflight(context.Background(), tmpPath, "", 0)
	if err != nil {
		return 2, []string{fmt.Sprintf("python3 failed: %v", err)}
	}

	if result.Failure != commandcheck.FailureNone {
		if result.Failure == commandcheck.FailureExit {
			return result.ExitCode, parseLintFailures(result.Stderr)
		}

		return result.ExitCode, []string{fmt.Sprintf("python3 failed: %v", result.Err)}
	}

	return 0, nil
}

func parseLintFailures(output string) []string {
	lines := strings.Split(output, "\n")

	var failures []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		failures = append(failures, line)
		if len(failures) >= 3 {
			break
		}
	}

	return failures
}
