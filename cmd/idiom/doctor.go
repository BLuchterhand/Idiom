package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"idiom/internal/autodetect"
)

// checkResult is one diagnostic outcome. Status is "pass" | "warn" |
// "fail"; remedy is the actionable next step a user takes when status
// isn't "pass" — empty when nothing needs doing.
type checkResult struct {
	name   string
	status string
	detail string
	remedy string
}

// runDoctorMain implements `idiom doctor` — a preflight diagnostic that
// scans the host for backend runners, model files, project config,
// workspace permissions, and supporting tools, then prints a report
// with exact remedy lines for anything that isn't ready.
//
// Exit codes:
//
//	0 — every check passed (warns allowed)
//	1 — at least one check failed (a hard blocker for `idiom start`)
//	2 — argument parse error
func runDoctorMain(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("idiom doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", ".idiom/config.json", "path to project config (relative or absolute)")
	asJSON := fs.Bool("json", false, "emit results as one JSON object instead of human-readable text")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	results := []checkResult{
		checkBackends(),
		checkModels(),
		checkConfigFile(*configPath),
		checkWorkspaceWritable(*configPath),
		checkPython3(),
		checkGit(),
	}

	if *asJSON {
		emitDoctorJSON(stdout, results)
	} else {
		emitDoctorText(stdout, results)
	}

	for _, r := range results {
		if r.status == "fail" {
			return 1
		}
	}
	return 0
}

// checkBackends looks for at least one supported runner on $PATH. A
// missing backend is fatal — `idiom start` has nothing to launch
// without one.
func checkBackends() checkResult {
	backends := autodetect.DetectBackends()
	if len(backends) == 0 {
		return checkResult{
			name:   "backend runner",
			status: "fail",
			detail: "no supported backend found on PATH",
			remedy: fmt.Sprintf("install one of: %s (e.g. `pip install mlx-lm` for mlx_lm.server)", strings.Join(autodetect.KnownBackendNames(), ", ")),
		}
	}
	names := make([]string, 0, len(backends))
	for _, b := range backends {
		names = append(names, b.BinaryName)
	}
	return checkResult{
		name:   "backend runner",
		status: "pass",
		detail: "found on PATH: " + strings.Join(names, ", "),
	}
}

// checkModels looks for at least one local model in well-known cache
// locations. Missing models is a "warn" not "fail" — the user might
// pull a model immediately after `idiom doctor`, and we don't want to
// gate startup on cache-warming.
func checkModels() checkResult {
	models := autodetect.DetectModels()
	if len(models) == 0 {
		return checkResult{
			name:   "local models",
			status: "warn",
			detail: "no models in ~/.cache/huggingface, ~/models, or ~/.ollama/models",
			remedy: "run `idiom models pull <alias>` (Phase 9 deliverable) or download a model into one of the standard cache dirs",
		}
	}
	return checkResult{
		name:   "local models",
		status: "pass",
		detail: fmt.Sprintf("found %d candidate(s); first: %s (%s)", len(models), models[0].Alias, models[0].Source),
	}
}

// checkConfigFile verifies the project config exists and is parseable
// JSON. It does NOT validate the schema — a stricter check belongs in
// a future config-validation pass.
func checkConfigFile(path string) checkResult {
	abs := path
	if !filepath.IsAbs(abs) {
		if cwd, err := os.Getwd(); err == nil {
			abs = filepath.Join(cwd, path)
		}
	}
	body, err := os.ReadFile(abs)
	if err != nil {
		return checkResult{
			name:   "project config",
			status: "fail",
			detail: fmt.Sprintf("cannot read %s: %v", abs, err),
			remedy: "run `idiom init` to scaffold .idiom/config.json (Phase 7)",
		}
	}
	var probe map[string]any
	if err := json.Unmarshal(body, &probe); err != nil {
		return checkResult{
			name:   "project config",
			status: "fail",
			detail: fmt.Sprintf("config at %s is not valid JSON: %v", abs, err),
			remedy: "fix the JSON syntax error or restore from a backup",
		}
	}
	return checkResult{
		name:   "project config",
		status: "pass",
		detail: "readable JSON at " + abs,
	}
}

// checkWorkspaceWritable verifies the workspace.root from config (or
// cwd when config is unreadable) is writable. A read-only workspace is
// fatal: the lane runner cannot apply edits.
func checkWorkspaceWritable(configPath string) checkResult {
	root := workspaceRootFromConfig(configPath)
	if root == "" {
		// Fall back to cwd when config didn't yield a root — doctor is
		// still useful pre-init, and cwd writability is the next-best
		// proxy for "can the lane edit files."
		if cwd, err := os.Getwd(); err == nil {
			root = cwd
		}
	}
	if root == "" {
		return checkResult{
			name:   "workspace writable",
			status: "warn",
			detail: "could not resolve a workspace root to test",
			remedy: "set workspace.root in .idiom/config.json",
		}
	}
	probe := filepath.Join(root, ".idiom-doctor-write-probe")
	if err := os.WriteFile(probe, []byte("probe"), 0o644); err != nil {
		return checkResult{
			name:   "workspace writable",
			status: "fail",
			detail: fmt.Sprintf("%s is not writable: %v", root, err),
			remedy: "check filesystem permissions on the workspace root, or change workspace.root in config",
		}
	}
	_ = os.Remove(probe)
	return checkResult{
		name:   "workspace writable",
		status: "pass",
		detail: root,
	}
}

// workspaceRootFromConfig reads workspace.root from the config file
// without invoking the full config loader. Returns "" on any failure
// — caller falls back to cwd or a warn result.
func workspaceRootFromConfig(path string) string {
	abs := path
	if !filepath.IsAbs(abs) {
		if cwd, err := os.Getwd(); err == nil {
			abs = filepath.Join(cwd, path)
		}
	}
	body, err := os.ReadFile(abs)
	if err != nil {
		return ""
	}
	var shape struct {
		Workspace struct {
			Root string `json:"root"`
		} `json:"workspace"`
	}
	if err := json.Unmarshal(body, &shape); err != nil {
		return ""
	}
	return strings.TrimSpace(shape.Workspace.Root)
}

// checkPython3 looks for python3 on $PATH. Required for `idiom brief
// lint` (which shells out to brief-preflight.py) and for the mlx_lm
// backend if that's the chosen runner. Missing python3 is "warn" not
// "fail" because idiom can still run plan-execute lanes without it
// against a different backend.
func checkPython3() checkResult {
	if _, err := exec.LookPath("python3"); err != nil {
		return checkResult{
			name:   "python3",
			status: "warn",
			detail: "python3 not on PATH",
			remedy: "install python3 (required for `idiom brief lint` and mlx_lm.server)",
		}
	}
	return checkResult{
		name:   "python3",
		status: "pass",
		detail: "on PATH",
	}
}

// checkGit looks for git on $PATH. Required for `idiom lane postflight`
// (diff inspection) and for any project workflow that gates on the
// repo's clean state. Missing git is "warn" not "fail" because lanes
// themselves don't require git.
func checkGit() checkResult {
	if _, err := exec.LookPath("git"); err != nil {
		return checkResult{
			name:   "git",
			status: "warn",
			detail: "git not on PATH",
			remedy: "install git (required for `idiom lane postflight` and most project workflows)",
		}
	}
	return checkResult{
		name:   "git",
		status: "pass",
		detail: "on PATH",
	}
}

// emitDoctorText writes a human-readable report. Format is intentionally
// simple — one line per check with status icon, name, and detail; a
// trailing block of remedy lines for anything non-passing.
func emitDoctorText(w io.Writer, results []checkResult) {
	icon := map[string]string{
		"pass": "ok  ",
		"warn": "warn",
		"fail": "FAIL",
	}
	var passes, warns, fails int
	for _, r := range results {
		fmt.Fprintf(w, "[%s] %-22s %s\n", icon[r.status], r.name, r.detail)
		switch r.status {
		case "pass":
			passes++
		case "warn":
			warns++
		case "fail":
			fails++
		}
	}
	fmt.Fprintf(w, "\nsummary: %d pass, %d warn, %d fail\n", passes, warns, fails)
	if warns+fails == 0 {
		return
	}
	fmt.Fprintln(w, "\nnext steps:")
	for _, r := range results {
		if r.status == "pass" || r.remedy == "" {
			continue
		}
		fmt.Fprintf(w, "  - %s: %s\n", r.name, r.remedy)
	}
}

// emitDoctorJSON writes a single JSON object: { "checks": [...],
// "summary": {pass, warn, fail} }. CI / wrappers consume this without
// parsing the human-readable form.
func emitDoctorJSON(w io.Writer, results []checkResult) {
	type entry struct {
		Name   string `json:"name"`
		Status string `json:"status"`
		Detail string `json:"detail,omitempty"`
		Remedy string `json:"remedy,omitempty"`
	}
	type summary struct {
		Pass int `json:"pass"`
		Warn int `json:"warn"`
		Fail int `json:"fail"`
	}
	out := struct {
		Checks  []entry `json:"checks"`
		Summary summary `json:"summary"`
	}{
		Checks: make([]entry, 0, len(results)),
	}
	for _, r := range results {
		out.Checks = append(out.Checks, entry{r.name, r.status, r.detail, r.remedy})
		switch r.status {
		case "pass":
			out.Summary.Pass++
		case "warn":
			out.Summary.Warn++
		case "fail":
			out.Summary.Fail++
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}
