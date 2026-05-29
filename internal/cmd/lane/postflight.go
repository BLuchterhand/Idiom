package lane
import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/app"
	"idiom/internal/postflight"
)

// runLanePostflight handles `idiom lane postflight`. It loads a
// unified diff, runs the generic check pipeline plus any project-
// declared checks under .idiom/postflight/checks/, and prints
// findings (or JSONL events with --json). Exit code:
//
//	0 — no error-severity findings
//	1 — IO/setup failure
//	2 — flag parse error
//	3 — at least one error-severity finding
//
// The 3 vs 1 split lets a calling shell (Colony's loop, CI) tell
// "postflight ran and found bugs" apart from "postflight couldn't
// even start." Both are non-zero, but only the latter is a wrapper
// problem to investigate.
func runLanePostflight(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("idiom lane postflight", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		diffPath          string
		briefPath         string
		projectRoot       string
		requiredEditPaths string
		skipList          string
		jsonOut           bool
		listChecks        bool
	)
	fs.StringVar(&diffPath, "diff", "", "path to a unified diff (required unless --list is set)")
	fs.StringVar(&briefPath, "brief", "", "brief markdown — required_edit_paths are extracted via the same logic the runner uses")
	fs.StringVar(&projectRoot, "project-root", "", "project root (defaults to cwd) — used for resolving paths and loading project checks")
	fs.StringVar(&requiredEditPaths, "required-edit-paths", "", "comma-separated allowlist; overrides --brief extraction")
	fs.StringVar(&skipList, "skip", "", "comma-separated check names to skip (use --list to see all available names)")
	fs.BoolVar(&jsonOut, "json", false, "emit one JSONL event per finding instead of human-readable output")
	fs.BoolVar(&listChecks, "list", false, "print the names of every check (generic + project) and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if listChecks {
		return runLanePostflightList(projectRoot, stdout, stderr)
	}
	if strings.TrimSpace(diffPath) == "" {
		fmt.Fprintln(stderr, "lane postflight: --diff is required")
		fs.Usage()
		return 2
	}

	root := projectRoot
	if strings.TrimSpace(root) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		root = cwd
	}

	d, err := postflight.ParseDiffFile(diffPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	// Resolve required_edit_paths: explicit flag wins, else extract
	// from brief, else leave empty (the allowlist check no-ops).
	var allow []string
	switch {
	case strings.TrimSpace(requiredEditPaths) != "":
		for _, p := range strings.Split(requiredEditPaths, ",") {
			if t := strings.TrimSpace(p); t != "" {
				allow = append(allow, t)
			}
		}
	case strings.TrimSpace(briefPath) != "":
		body, err := os.ReadFile(briefPath)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		allow = app.RequiredEditPathsFromPrompt(string(body))
	}

	checks := postflight.GenericChecks()
	projectChecks, err := postflight.LoadProjectChecks(root)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	checks = append(checks, projectChecks...)

	if strings.TrimSpace(skipList) != "" {
		var skips []string
		for _, s := range strings.Split(skipList, ",") {
			if t := strings.TrimSpace(s); t != "" {
				skips = append(skips, t)
			}
		}
		checks = postflight.FilterChecks(checks, skips)
	}

	ctx := postflight.RunContext{
		ProjectRoot:       root,
		RequiredEditPaths: allow,
		BriefPath:         briefPath,
		DiffPath:          diffPath,
	}
	res := postflight.Run(d, ctx, checks)

	if jsonOut {
		emitPostflightJSON(stdout, res)
	} else {
		emitPostflightHuman(stdout, res, root)
	}
	if !res.Pass {
		return 3
	}
	return 0
}

func emitPostflightJSON(stdout io.Writer, res postflight.Result) {
	for _, f := range res.Findings {
		event := map[string]any{
			"type":     "idiom.lane.postflight.finding",
			"check":    f.Check,
			"severity": f.Severity,
			"message":  f.Message,
		}
		// Honor the Finding struct's omitempty intent for path/line:
		// whole-diff findings without positional info shouldn't emit
		// noisy `"path":""` / `"line":0` keys downstream consumers
		// have to filter.
		if f.Path != "" {
			event["path"] = f.Path
		}
		if f.Line > 0 {
			event["line"] = f.Line
		}
		body, err := json.Marshal(event)
		if err != nil {
			continue
		}
		fmt.Fprintln(stdout, string(body))
	}
	summary, _ := json.Marshal(map[string]any{
		"type":     "idiom.lane.postflight.summary",
		"pass":     res.Pass,
		"findings": len(res.Findings),
	})
	fmt.Fprintln(stdout, string(summary))
}

// runLanePostflightList prints the names of every check the
// postflight pipeline would run for the resolved project root,
// then exits. Operators use this to discover names for `--skip`
// without having to author a fake diff.
func runLanePostflightList(projectRoot string, stdout io.Writer, stderr io.Writer) int {
	root := projectRoot
	if strings.TrimSpace(root) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		root = cwd
	}
	for _, c := range postflight.GenericChecks() {
		fmt.Fprintf(stdout, "generic %s\n", c.Name())
	}
	projectChecks, err := postflight.LoadProjectChecks(root)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	for _, c := range projectChecks {
		fmt.Fprintf(stdout, "project %s\n", c.Name())
	}
	return 0
}

func emitPostflightHuman(stdout io.Writer, res postflight.Result, root string) {
	if len(res.Findings) == 0 {
		fmt.Fprintln(stdout, "postflight: PASS — no findings")
		return
	}
	for _, f := range res.Findings {
		path := f.Path
		if path != "" && root != "" && !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		switch {
		case f.Path != "" && f.Line > 0:
			fmt.Fprintf(stdout, "[%s] %s:%d: %s — %s\n", f.Severity, path, f.Line, f.Check, f.Message)
		case f.Path != "":
			fmt.Fprintf(stdout, "[%s] %s: %s — %s\n", f.Severity, path, f.Check, f.Message)
		default:
			fmt.Fprintf(stdout, "[%s] %s — %s\n", f.Severity, f.Check, f.Message)
		}
	}
	if res.Pass {
		fmt.Fprintln(stdout, "postflight: PASS (warnings only)")
	} else {
		fmt.Fprintln(stdout, "postflight: FAIL")
	}
}
