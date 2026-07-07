package postflight

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"idiom/internal/platform/commandexec"
)

// ProjectCheck is the on-disk representation of a check declared
// at <project-root>/.idiom/postflight/checks/<name>.json. Phase 5
// supports Kind="shell"; Kind="ast_grep" is reserved for a later
// phase (the schema accepts it but the runner returns a single
// `unsupported_kind` finding so projects don't silently ship
// rules that no-op).
type ProjectCheck struct {
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	// FailOnNonZero defaults to true; set false for a check that
	// always reports its findings via stdout JSON regardless of
	// exit code.
	FailOnNonZero *bool `json:"fail_on_non_zero,omitempty"`
}

// shouldFailOnNonZero applies the default-true semantics for
// FailOnNonZero so callers don't carry the nil check everywhere.
func (p ProjectCheck) shouldFailOnNonZero() bool {
	if p.FailOnNonZero == nil {
		return true
	}

	return *p.FailOnNonZero
}

// LoadProjectChecks reads every `*.json` file under
// `<projectRoot>/.idiom/postflight/checks/` and returns one Check
// per file. Files that fail to parse return as an error so a
// project misconfiguration is loud rather than silently skipping
// the rule. Returns (empty, nil) when the directory doesn't exist.
func LoadProjectChecks(projectRoot string) ([]Check, error) {
	if strings.TrimSpace(projectRoot) == "" {
		return nil, nil
	}

	dir := filepath.Join(projectRoot, ".idiom", "postflight", "checks")

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("postflight: read %q: %w", dir, err)
	}

	var checks []Check

	// Sort so the run order is deterministic across operating
	// systems (os.ReadDir order is platform-dependent).
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return cmp.Compare(a.Name(), b.Name()) })

	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}

		path := filepath.Join(dir, ent.Name())

		body, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("postflight: read project check %q: %w", path, err)
		}

		var pc ProjectCheck

		if err := json.Unmarshal(body, &pc); err != nil {
			return nil, fmt.Errorf("postflight: parse project check %q: %w", path, err)
		}

		if strings.TrimSpace(pc.Name) == "" {
			pc.Name = strings.TrimSuffix(ent.Name(), ".json")
		}

		checks = append(checks, &projectCheckRunner{spec: pc})
	}

	return checks, nil
}

// projectCheckRunner adapts a ProjectCheck spec to the Check
// interface. Phase 5 ships the shell runner; ast_grep is
// recognized at parse time but produces an explicit unsupported
// finding rather than silently no-op'ing.
type projectCheckRunner struct {
	spec ProjectCheck
}

func (r *projectCheckRunner) Name() string { return r.spec.Name }

func (r *projectCheckRunner) Run(d Diff, ctx RunContext) ([]Finding, error) {
	switch strings.TrimSpace(r.spec.Kind) {
	case "shell":
		return r.runShell(d, ctx)
	case "ast_grep":
		return []Finding{{
			Check:    r.Name(),
			Severity: SeverityWarning,
			Message:  "ast_grep checks are reserved for a later phase; this rule is being skipped",
		}}, nil
	default:
		return []Finding{{
			Check:    r.Name(),
			Severity: SeverityError,
			Message:  fmt.Sprintf("unknown project check kind %q (supported: shell, ast_grep)", r.spec.Kind),
		}}, nil
	}
}

// shellOutputCap bounds how much of a shell check's combined
// stdout+stderr is embedded in a finding's message. A misbehaving
// rule that prints megabytes shouldn't inflate the postflight
// report or flood downstream JSONL consumers; ~4 KB is enough to
// see what the rule reported and tells the operator to rerun for
// full output.
const shellOutputCap = 4096
const shellOutputCaptureBytes = 1024 * 1024

// runShell invokes the configured command with substituted args
// and treats a non-zero exit as a check failure (configurable via
// fail_on_non_zero). Combined stdout+stderr is truncated to
// shellOutputCap bytes before embedding in the finding's message.
//
// Trust note: `kind: shell` rules execute arbitrary commands with
// the operator's privileges. Reviewing pulled `.idiom/postflight/
// checks/*.json` is the project's responsibility — see the
// security note in docs/lane-architecture.md.
func (r *projectCheckRunner) runShell(d Diff, ctx RunContext) ([]Finding, error) {
	cmd := strings.TrimSpace(r.spec.Command)
	if cmd == "" {
		return []Finding{{
			Check:    r.Name(),
			Severity: SeverityError,
			Message:  "shell project check has empty `command`",
		}}, nil
	}

	// 60-second hard cap on shell checks: this matches Colony's
	// default check timeout. If a project rule legitimately needs
	// longer it can spawn its own background work and report
	// quickly.
	args := substituteShellArgs(r.spec.Args, ctx, d)
	workdir := ""
	if strings.TrimSpace(ctx.ProjectRoot) != "" {
		workdir = ctx.ProjectRoot
	}

	result := commandexec.Run(nil, commandexec.Spec{
		Name:           cmd,
		Args:           args,
		Dir:            workdir,
		Timeout:        60 * time.Second,
		MaxOutputBytes: shellOutputCaptureBytes,
		MergeOutput:    true,
	})
	output := commandexec.CombineOutput(result.Stdout, result.Stderr)

	if result.TimedOut {
		return []Finding{{
			Check:    r.Name(),
			Severity: SeverityError,
			Message:  "shell project check exceeded 60s timeout",
		}}, nil
	}

	if result.Err != nil && !result.ExitError {
		// non-exit error: command not found, can't spawn, etc.
		return []Finding{{
			Check:    r.Name(),
			Severity: SeverityError,
			Message:  fmt.Sprintf("shell project check failed to run: %v", result.Err),
		}}, nil
	}

	exitCode := result.ExitCode

	if exitCode != 0 && r.spec.shouldFailOnNonZero() {
		return []Finding{{
			Check:    r.Name(),
			Severity: SeverityError,
			Message:  fmt.Sprintf("exit %d: %s", exitCode, truncateOutput(output)),
		}}, nil
	}

	if exitCode != 0 {
		return []Finding{{
			Check:    r.Name(),
			Severity: SeverityWarning,
			Message:  fmt.Sprintf("exit %d (non-blocking): %s", exitCode, truncateOutput(output)),
		}}, nil
	}

	return nil, nil
}

// truncateOutput trims the combined stdout+stderr to shellOutputCap
// bytes and appends a marker so the operator knows there's more.
// Whitespace is trimmed before the cap so a script that prints
// trailing newlines doesn't waste budget on those.
func truncateOutput(out string) string {
	s := strings.TrimSpace(out)
	if len(s) <= shellOutputCap {
		return s
	}

	return s[:shellOutputCap] + "... (truncated; rerun the check for full output)"
}

// substituteShellArgs expands placeholders the project may pass in
// `args`. Supported placeholders:
//
//	{diff}         path to the diff file (RunContext.DiffPath)
//	{brief}        path to the brief file (RunContext.BriefPath)
//	{project_root} resolved project root
//	{paths}        space-joined modified paths
//
// Unknown placeholders pass through unchanged so a project can
// later add new substitutions without breaking older configs.
func substituteShellArgs(args []string, ctx RunContext, d Diff) []string {
	out := make([]string, len(args))
	paths := strings.Join(d.ModifiedPaths(), " ")
	replacer := strings.NewReplacer(
		"{diff}", ctx.DiffPath,
		"{brief}", ctx.BriefPath,
		"{project_root}", ctx.ProjectRoot,
		"{paths}", paths,
	)

	for i, a := range args {
		out[i] = replacer.Replace(a)
	}

	return out
}
