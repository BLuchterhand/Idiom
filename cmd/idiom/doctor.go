package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"idiom/internal/autodetect"
	"idiom/internal/backends"
	"idiom/internal/config"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/runner"
	"idiom/internal/telemetry"
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

type readinessCheck struct {
	Name     string `json:"name"`
	State    string `json:"state"`
	Detail   string `json:"detail,omitempty"`
	Remedy   string `json:"remedy,omitempty"`
	Optional bool   `json:"optional,omitempty"`
}

type readinessProfileSelectionError struct {
	Selected string
}

func (e readinessProfileSelectionError) Error() string {
	return fmt.Sprintf("doctor: unknown readiness profile %q (expected all, discovery, plan, implement, eval, or sidecar)", e.Selected)
}

func isReadinessProfileSelectionError(err error) bool {
	var selectionErr readinessProfileSelectionError
	return errors.As(err, &selectionErr)
}

type readinessUnavailable struct {
	Check  string `json:"check"`
	Reason string `json:"reason"`
	Remedy string `json:"remedy,omitempty"`
}

type readinessGateDecision struct {
	Name              string `json:"name"`
	Status            string `json:"status"`
	Severity          string `json:"severity"`
	Enforcement       string `json:"enforcement"`
	RequiredForLaunch bool   `json:"required_for_launch"`
	Evidence          string `json:"evidence"`
	Reason            string `json:"reason,omitempty"`
	Remedy            string `json:"remedy,omitempty"`
}

type readinessProfileReport struct {
	Name                 string                  `json:"name"`
	State                string                  `json:"state"`
	Detail               string                  `json:"detail,omitempty"`
	Enforcement          string                  `json:"enforcement"`
	RequiredForLaunch    bool                    `json:"required_for_launch"`
	TaskFamily           string                  `json:"task_family"`
	Contract             string                  `json:"contract"`
	PermissionProfile    string                  `json:"permission_profile,omitempty"`
	ToolProfile          string                  `json:"tool_profile,omitempty"`
	RequiredEvidence     []string                `json:"required_evidence,omitempty"`
	ReadinessGate        []string                `json:"readiness_gate,omitempty"`
	VerificationContract string                  `json:"verification_contract,omitempty"`
	RequiredRoles        []string                `json:"required_roles,omitempty"`
	RoleAliases          map[string]string       `json:"role_aliases,omitempty"`
	Gates                []readinessGateDecision `json:"gates,omitempty"`
	HardFailures         []readinessGateDecision `json:"hard_failures,omitempty"`
	SoftDegradations     []readinessGateDecision `json:"soft_degradations,omitempty"`
	Checks               []readinessCheck        `json:"checks,omitempty"`
	Unavailable          []readinessUnavailable  `json:"unavailable,omitempty"`
	Metrics              readinessMetrics        `json:"metrics,omitempty"`
}

type readinessMetrics struct {
	PassChecks         int    `json:"pass_checks,omitempty"`
	DegradedChecks     int    `json:"degraded_checks,omitempty"`
	BlockedChecks      int    `json:"blocked_checks,omitempty"`
	UnavailableChecks  int    `json:"unavailable_checks,omitempty"`
	QueuePools         int    `json:"queue_pools,omitempty"`
	QueueInFlight      int    `json:"queue_in_flight,omitempty"`
	QueueWaiting       int    `json:"queue_waiting,omitempty"`
	QueueIdle          int    `json:"queue_idle,omitempty"`
	ContextTokens      int    `json:"context_tokens,omitempty"`
	MaxOutputTokens    int    `json:"max_output_tokens,omitempty"`
	ClassifierState    string `json:"classifier_state,omitempty"`
	BackendState       string `json:"backend_state,omitempty"`
	QueueState         string `json:"queue_state,omitempty"`
	ContextBudgetState string `json:"context_budget_state,omitempty"`
	HardFailures       int    `json:"hard_failures,omitempty"`
	SoftDegradations   int    `json:"soft_degradations,omitempty"`
}

type readinessProfileSpec struct {
	name          string
	taskFamily    string
	requiredRoles []string
	needsContext  bool
}

var doctorReadinessProfileSpecs = []readinessProfileSpec{
	{name: "discovery", taskFamily: "architecture", needsContext: true},
	{name: "plan", taskFamily: "plan_then_execute_planner", requiredRoles: []string{backends.RolePlanner, backends.RoleExecutor}, needsContext: true},
	{name: "implement", taskFamily: "implementation", requiredRoles: []string{backends.RoleExecutor}, needsContext: true},
	{name: "eval", taskFamily: "verification_only", requiredRoles: []string{backends.RoleExecutor}},
	{name: "sidecar", taskFamily: "debug_logs", requiredRoles: []string{backends.RoleClassifier}},
}

type readinessGatePolicy struct {
	name              string
	checkName         string
	hardProfiles      map[string]bool
	softProfiles      map[string]bool
	requiredForLaunch bool
}

var doctorReadinessGatePolicies = []readinessGatePolicy{
	{
		name:              "backend_roles_resolved",
		checkName:         "backend role config",
		hardProfiles:      profileSet("plan", "implement", "eval", "sidecar"),
		softProfiles:      profileSet("discovery"),
		requiredForLaunch: true,
	},
	{
		name:              "queue_admission_known",
		checkName:         "queue admission",
		hardProfiles:      profileSet("eval"),
		softProfiles:      profileSet("discovery", "plan", "implement", "sidecar"),
		requiredForLaunch: true,
	},
	{
		name:              "context_budget_valid",
		checkName:         "context budget",
		hardProfiles:      profileSet("plan", "implement", "eval"),
		softProfiles:      profileSet("discovery", "sidecar"),
		requiredForLaunch: true,
	},
	{
		name:              "route_classifier_available_or_fallback_recorded",
		checkName:         "route classifier",
		softProfiles:      profileSet("discovery", "plan", "implement", "eval", "sidecar"),
		requiredForLaunch: false,
	},
}

func profileSet(names ...string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[name] = true
	}
	return out
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
	return runDoctorMainWithTelemetry(args, stdout, stderr, nil)
}

func runDoctorMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	fs := flag.NewFlagSet("idiom doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", ".idiom/config.json", "path to project config (relative or absolute)")
	asJSON := fs.Bool("json", false, "emit results as one JSON object instead of human-readable text")
	readinessProfile := fs.String("profile", "all", "readiness profile to report: all, discovery, plan, implement, eval, or sidecar")
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
	profiles, err := buildDoctorReadinessProfiles(*configPath, *readinessProfile, results)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	logDoctorReadinessTelemetry(rec, profiles)

	if *asJSON {
		emitDoctorJSON(stdout, results, profiles)
	} else {
		emitDoctorText(stdout, results, profiles)
	}

	for _, r := range results {
		if r.status == "fail" {
			return 1
		}
	}
	return 0
}

func logDoctorReadinessTelemetry(rec *telemetry.Recorder, profiles []readinessProfileReport) {
	for _, profile := range profiles {
		logCLIPhase(rec, "doctor_readiness_profile", map[string]any{
			"profile":                 profile.Name,
			"state":                   profile.State,
			"pass_checks":             profile.Metrics.PassChecks,
			"degraded_checks":         profile.Metrics.DegradedChecks,
			"blocked_checks":          profile.Metrics.BlockedChecks,
			"unavailable_checks":      profile.Metrics.UnavailableChecks,
			"backend_state":           profile.Metrics.BackendState,
			"queue_state":             profile.Metrics.QueueState,
			"queue_pools":             profile.Metrics.QueuePools,
			"queue_in_flight":         profile.Metrics.QueueInFlight,
			"queue_waiting":           profile.Metrics.QueueWaiting,
			"queue_idle":              profile.Metrics.QueueIdle,
			"context_budget_state":    profile.Metrics.ContextBudgetState,
			"context_tokens":          profile.Metrics.ContextTokens,
			"max_output_tokens":       profile.Metrics.MaxOutputTokens,
			"route_classifier_state":  profile.Metrics.ClassifierState,
			"hard_failures":           profile.Metrics.HardFailures,
			"soft_degradations":       profile.Metrics.SoftDegradations,
			"enforcement":             profile.Enforcement,
			"required_for_launch":     profile.RequiredForLaunch,
			"degraded":                profile.State == "degraded",
			"blocked":                 profile.State == "blocked",
			"metadata_only_contract":  true,
			"contains_prompt_content": false,
		})
	}
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
func emitDoctorText(w io.Writer, results []checkResult, profiles []readinessProfileReport) {
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
		emitDoctorReadinessText(w, profiles)
		return
	}
	fmt.Fprintln(w, "\nnext steps:")
	for _, r := range results {
		if r.status == "pass" || r.remedy == "" {
			continue
		}
		fmt.Fprintf(w, "  - %s: %s\n", r.name, r.remedy)
	}
	emitDoctorReadinessText(w, profiles)
}

// emitDoctorJSON writes a single JSON object: { "checks": [...],
// "summary": {pass, warn, fail} }. CI / wrappers consume this without
// parsing the human-readable form.
func emitDoctorJSON(w io.Writer, results []checkResult, profiles []readinessProfileReport) {
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
		Checks            []entry                  `json:"checks"`
		Summary           summary                  `json:"summary"`
		ReadinessProfiles []readinessProfileReport `json:"readiness_profiles"`
	}{
		Checks:            make([]entry, 0, len(results)),
		ReadinessProfiles: profiles,
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

func emitDoctorReadinessText(w io.Writer, profiles []readinessProfileReport) {
	if len(profiles) == 0 {
		return
	}
	fmt.Fprintln(w, "\nreadiness profiles:")
	for _, profile := range profiles {
		fmt.Fprintf(w, "  - %s: %s", profile.Name, profile.State)
		if profile.Detail != "" {
			fmt.Fprintf(w, " (%s)", profile.Detail)
		}
		if len(profile.RequiredRoles) > 0 {
			fmt.Fprintf(w, " roles=%s", strings.Join(profile.RequiredRoles, ","))
		}
		if len(profile.Unavailable) > 0 {
			fmt.Fprintf(w, " unavailable=%d", len(profile.Unavailable))
		}
		if profile.Enforcement != "" {
			fmt.Fprintf(w, " enforcement=%s", profile.Enforcement)
		}
		if len(profile.HardFailures) > 0 {
			fmt.Fprintf(w, " hard_failures=%d", len(profile.HardFailures))
		}
		if len(profile.SoftDegradations) > 0 {
			fmt.Fprintf(w, " soft_degradations=%d", len(profile.SoftDegradations))
		}
		fmt.Fprintln(w)
		for _, gate := range profile.Gates {
			if gate.Status == "pass" {
				continue
			}
			fmt.Fprintf(w, "      gate %s %s/%s: %s\n", gate.Name, gate.Status, gate.Severity, gate.Reason)
			if gate.Remedy != "" {
				fmt.Fprintf(w, "        remedy: %s\n", gate.Remedy)
			}
		}
		for _, unavailable := range profile.Unavailable {
			fmt.Fprintf(w, "      unavailable %s: %s\n", unavailable.Check, unavailable.Reason)
			if unavailable.Remedy != "" {
				fmt.Fprintf(w, "        remedy: %s\n", unavailable.Remedy)
			}
		}
		for _, check := range profile.Checks {
			if check.State == "pass" || check.Remedy == "" {
				continue
			}
			fmt.Fprintf(w, "      %s %s: %s\n", check.State, check.Name, check.Remedy)
		}
	}
}

func buildDoctorReadinessProfiles(configPath, selected string, hostChecks []checkResult) ([]readinessProfileReport, error) {
	selected = strings.ToLower(strings.TrimSpace(selected))
	if selected == "" {
		selected = "all"
	}
	specs := make([]readinessProfileSpec, 0, len(doctorReadinessProfileSpecs))
	for _, spec := range doctorReadinessProfileSpecs {
		if selected == "all" || selected == spec.name {
			specs = append(specs, spec)
		}
	}
	if len(specs) == 0 {
		return nil, readinessProfileSelectionError{Selected: selected}
	}

	root := workspaceRootFromConfig(configPath)
	if strings.TrimSpace(root) == "" {
		if cwd, err := os.Getwd(); err == nil {
			root = cwd
		}
	}
	backendFile, backendFound, backendErr := backends.LoadFromProjectRoot(root)
	cfg, cfgOK := loadDoctorConfig(configPath)
	contextReady, contextDetail := doctorContextIndexReadiness(root)
	queueCheck, queueMetrics, queueUnavailable := doctorQueueReadiness()

	reports := make([]readinessProfileReport, 0, len(specs))
	for _, spec := range specs {
		reports = append(reports, buildDoctorReadinessProfile(spec, hostChecks, backendFile, backendFound, backendErr, cfg, cfgOK, contextReady, contextDetail, queueCheck, queueMetrics, queueUnavailable))
	}
	return reports, nil
}

func buildDoctorReadinessProfile(spec readinessProfileSpec, hostChecks []checkResult, backendFile backends.File, backendFound bool, backendErr error, cfg config.Config, cfgOK bool, contextReady bool, contextDetail string, queueCheck readinessCheck, queueMetrics readinessMetrics, queueUnavailable *readinessUnavailable) readinessProfileReport {
	contract := workerruntime.RuntimeContractForTaskFamily(spec.taskFamily)
	report := readinessProfileReport{
		Name:                 spec.name,
		TaskFamily:           contract.TaskFamily,
		Contract:             contract.Contract,
		PermissionProfile:    contract.PermissionProfile,
		ToolProfile:          string(contract.ToolProfile),
		RequiredEvidence:     append([]string(nil), contract.RequiredEvidence...),
		ReadinessGate:        append([]string(nil), contract.ReadinessGate...),
		VerificationContract: contract.VerificationContract,
		RequiredRoles:        append([]string(nil), spec.requiredRoles...),
		RoleAliases:          map[string]string{},
		Enforcement:          "report_only",
		RequiredForLaunch:    true,
	}
	report.Checks = append(report.Checks, readinessCheck{
		Name:   "runtime contract",
		State:  "pass",
		Detail: fmt.Sprintf("%s / %s", contract.Contract, contract.PermissionProfile),
	})
	report.Checks = append(report.Checks, readinessChecksFromHost(hostChecks)...)
	if spec.needsContext {
		if contextReady {
			report.Checks = append(report.Checks, readinessCheck{Name: "context index", State: "pass", Detail: contextDetail, Optional: true})
		} else {
			report.Checks = append(report.Checks, readinessCheck{
				Name:     "context index",
				State:    "degraded",
				Detail:   contextDetail,
				Remedy:   "run `idiom context status --refresh` or `idiom context build` before relying on cached discovery evidence",
				Optional: true,
			})
		}
	}
	report.Checks = append(report.Checks, queueCheck)
	if queueUnavailable != nil {
		report.Unavailable = append(report.Unavailable, *queueUnavailable)
	}
	report.Checks = append(report.Checks, doctorContextBudgetCheck(cfg, cfgOK))
	report.Checks = append(report.Checks, doctorRouteClassifierCheck(cfg, cfgOK))
	report.Unavailable = append(report.Unavailable, readinessUnavailable{
		Check:  "route classifier live probe",
		Reason: "doctor reports route-classifier configuration state but defers live classifier calls",
		Remedy: "use a deep readiness or route-classifier-specific probe when live classification health is required",
	})
	report = applyBackendRoleReadiness(report, backendFile, backendFound, backendErr)
	report = applyDoctorReadinessGatePolicy(report)
	report.State = readinessProfileState(report.Checks, report.Gates)
	report.Detail = readinessProfileDetail(report.State)
	report.Metrics = readinessMetricsFor(report.Checks, report.Gates, queueMetrics)
	if len(report.RoleAliases) == 0 {
		report.RoleAliases = nil
	}
	return report
}

func loadDoctorConfig(configPath string) (config.Config, bool) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return config.Config{}, false
	}
	return cfg, true
}

func doctorQueueReadiness() (readinessCheck, readinessMetrics, *readinessUnavailable) {
	socket := runner.DefaultSocketPath()
	snap, err := runner.Status(socket)
	if err != nil {
		check := readinessCheck{
			Name:     "queue admission",
			State:    "unavailable",
			Detail:   "queue daemon not reachable",
			Remedy:   "start the daemon with `idiom queue serve` or treat queue gating as opportunistic",
			Optional: true,
		}
		unavailable := readinessUnavailable{
			Check:  "queue admission",
			Reason: "queue daemon not reachable",
			Remedy: "start the daemon with `idiom queue serve` for live queue capacity evidence",
		}
		return check, readinessMetrics{QueueState: "unavailable"}, &unavailable
	}
	metrics := readinessMetrics{QueueState: "pass", QueuePools: len(snap.Pools), QueueWaiting: len(snap.Waiting), QueueInFlight: len(snap.InFlight)}
	for _, job := range snap.InFlight {
		if job.Idle {
			metrics.QueueIdle++
		}
	}
	state := "pass"
	detail := fmt.Sprintf("reachable, pools=%d, in_flight=%d, waiting=%d", metrics.QueuePools, metrics.QueueInFlight, metrics.QueueWaiting)
	remedy := ""
	if metrics.QueueIdle > 0 {
		state = "degraded"
		metrics.QueueState = "degraded"
		detail = fmt.Sprintf("%s, idle=%d", detail, metrics.QueueIdle)
		remedy = "inspect `idiom queue status --json` and cancel or restart stale in-flight jobs if they are wedged"
	}
	return readinessCheck{Name: "queue admission", State: state, Detail: detail, Remedy: remedy, Optional: true}, metrics, nil
}

func doctorContextBudgetCheck(cfg config.Config, cfgOK bool) readinessCheck {
	if !cfgOK {
		return readinessCheck{
			Name:     "context budget",
			State:    "unavailable",
			Detail:   "project config did not load",
			Remedy:   "fix project config so context capacity can be computed",
			Optional: true,
		}
	}
	ctxSize := cfg.LlamaServer.CtxSize
	maxOutput := cfg.Chat.MaxTokens
	switch {
	case ctxSize <= 0:
		return readinessCheck{Name: "context budget", State: "degraded", Detail: "llama_server.ctx_size is not configured", Remedy: "set llama_server.ctx_size to the backend-supported context window", Optional: true}
	case maxOutput <= 0:
		return readinessCheck{Name: "context budget", State: "degraded", Detail: fmt.Sprintf("ctx_size=%d but chat.max_tokens is not configured", ctxSize), Remedy: "set chat.max_tokens to reserve output space per lane", Optional: true}
	case maxOutput >= ctxSize:
		return readinessCheck{Name: "context budget", State: "degraded", Detail: fmt.Sprintf("ctx_size=%d max_output=%d leaves no prompt budget", ctxSize, maxOutput), Remedy: "lower chat.max_tokens or raise the configured backend context", Optional: true}
	default:
		return readinessCheck{Name: "context budget", State: "pass", Detail: fmt.Sprintf("ctx_size=%d max_output=%d", ctxSize, maxOutput), Optional: true}
	}
}

func doctorRouteClassifierCheck(cfg config.Config, cfgOK bool) readinessCheck {
	if !cfgOK {
		return readinessCheck{Name: "route classifier", State: "unavailable", Detail: "project config did not load", Remedy: "fix project config so classifier fallback state can be reported", Optional: true}
	}
	if !cfg.RouteClassifier.Enabled {
		return readinessCheck{Name: "route classifier", State: "degraded", Detail: "disabled; deterministic routing fallback active", Remedy: "enable route_classifier when classifier-backed routing or summaries are required", Optional: true}
	}
	if strings.TrimSpace(cfg.RouteClassifier.BaseURL) == "" && cfg.RouteClassifier.Port <= 0 {
		return readinessCheck{Name: "route classifier", State: "degraded", Detail: "enabled but no base_url or server port is configured", Remedy: "set route_classifier.base_url or route_classifier.port", Optional: true}
	}
	return readinessCheck{Name: "route classifier", State: "pass", Detail: "configured; live JSON probe is deferred to deep readiness", Optional: true}
}

func readinessMetricsFor(checks []readinessCheck, gates []readinessGateDecision, queue readinessMetrics) readinessMetrics {
	metrics := queue
	for _, check := range checks {
		switch check.State {
		case "pass":
			metrics.PassChecks++
		case "blocked":
			metrics.BlockedChecks++
		case "degraded":
			metrics.DegradedChecks++
		case "unavailable":
			metrics.UnavailableChecks++
		}
		switch check.Name {
		case "backend role config":
			metrics.BackendState = check.State
		case "context budget":
			metrics.ContextBudgetState = check.State
			fmt.Sscanf(check.Detail, "ctx_size=%d max_output=%d", &metrics.ContextTokens, &metrics.MaxOutputTokens)
		case "route classifier":
			metrics.ClassifierState = check.State
		case "queue admission":
			if metrics.QueueState == "" {
				metrics.QueueState = check.State
			}
		}
	}
	for _, gate := range gates {
		switch gate.Status {
		case "blocked":
			metrics.HardFailures++
		case "degraded":
			metrics.SoftDegradations++
		}
	}
	return metrics
}

func applyDoctorReadinessGatePolicy(report readinessProfileReport) readinessProfileReport {
	checks := map[string]readinessCheck{}
	for _, check := range report.Checks {
		checks[check.Name] = check
	}
	for _, policy := range doctorReadinessGatePolicies {
		check, ok := checks[policy.checkName]
		if !ok {
			continue
		}
		severity := readinessGateSeverity(report.Name, policy)
		if severity == "" {
			continue
		}
		gate := readinessGateDecision{
			Name:              policy.name,
			Status:            readinessGateStatus(check, severity),
			Severity:          severity,
			Enforcement:       report.Enforcement,
			RequiredForLaunch: policy.requiredForLaunch && severity == "hard",
			Evidence:          check.Name,
			Reason:            readinessGateReason(check),
			Remedy:            check.Remedy,
		}
		if gate.Status == "pass" {
			gate.Reason = ""
			gate.Remedy = ""
		}
		report.Gates = append(report.Gates, gate)
		switch gate.Status {
		case "blocked":
			report.HardFailures = append(report.HardFailures, gate)
		case "degraded":
			report.SoftDegradations = append(report.SoftDegradations, gate)
		}
	}
	return report
}

func readinessGateSeverity(profile string, policy readinessGatePolicy) string {
	profile = strings.ToLower(strings.TrimSpace(profile))
	switch {
	case policy.hardProfiles[profile]:
		return "hard"
	case policy.softProfiles[profile]:
		return "soft"
	default:
		return ""
	}
}

func readinessGateStatus(check readinessCheck, severity string) string {
	switch check.State {
	case "pass":
		return "pass"
	case "blocked":
		return "blocked"
	case "degraded", "unavailable":
		if severity == "hard" {
			return "blocked"
		}
		return "degraded"
	default:
		if severity == "hard" {
			return "blocked"
		}
		return "degraded"
	}
}

func readinessGateReason(check readinessCheck) string {
	if strings.TrimSpace(check.Detail) != "" {
		return check.Detail
	}
	return check.State
}

func readinessChecksFromHost(hostChecks []checkResult) []readinessCheck {
	out := make([]readinessCheck, 0, len(hostChecks))
	for _, check := range hostChecks {
		state := "pass"
		switch check.status {
		case "fail":
			state = "blocked"
		case "warn":
			state = "degraded"
		}
		out = append(out, readinessCheck{
			Name:   check.name,
			State:  state,
			Detail: check.detail,
			Remedy: check.remedy,
		})
	}
	return out
}

func applyBackendRoleReadiness(report readinessProfileReport, backendFile backends.File, backendFound bool, backendErr error) readinessProfileReport {
	if len(report.RequiredRoles) == 0 {
		report.Checks = append(report.Checks, readinessCheck{
			Name:     "backend role config",
			State:    "pass",
			Detail:   "profile does not require a dedicated backend role",
			Optional: true,
		})
		return report
	}
	if backendErr != nil {
		report.Checks = append(report.Checks, readinessCheck{
			Name:   "backend role config",
			State:  "blocked",
			Detail: backendErr.Error(),
			Remedy: "fix .idiom/backends.json so roles can be resolved before launch",
		})
		return report
	}
	if !backendFound {
		report.Checks = append(report.Checks, readinessCheck{
			Name:   "backend role config",
			State:  "unavailable",
			Detail: ".idiom/backends.json not found",
			Remedy: "create .idiom/backends.json or run with legacy config while treating role-specific readiness as unavailable",
		})
		report.Unavailable = append(report.Unavailable, readinessUnavailable{
			Check:  "backend role config",
			Reason: ".idiom/backends.json not found",
			Remedy: "create .idiom/backends.json to make role readiness machine-readable",
		})
		return report
	}
	profile, ok := backendFile.Profile("")
	if !ok {
		report.Checks = append(report.Checks, readinessCheck{
			Name:   "backend role config",
			State:  "blocked",
			Detail: "no default or unambiguous backend profile selected",
			Remedy: "set default_profile in .idiom/backends.json or keep only one backend profile",
		})
		return report
	}
	var missing []string
	for _, role := range report.RequiredRoles {
		alias, ok := profile.AliasForRole(role)
		if !ok {
			missing = append(missing, role)
			continue
		}
		report.RoleAliases[role] = alias
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		report.Checks = append(report.Checks, readinessCheck{
			Name:   "backend role config",
			State:  "blocked",
			Detail: "missing role aliases: " + strings.Join(missing, ", "),
			Remedy: "add role tags or proxy.default_<role> aliases in .idiom/backends.json",
		})
		return report
	}
	report.Checks = append(report.Checks, readinessCheck{
		Name:   "backend role config",
		State:  "pass",
		Detail: "resolved roles: " + strings.Join(sortedRoleAliases(report.RoleAliases), ", "),
	})
	return report
}

func sortedRoleAliases(aliases map[string]string) []string {
	keys := make([]string, 0, len(aliases))
	for key := range aliases {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+aliases[key])
	}
	return out
}

func doctorContextIndexReadiness(root string) (bool, string) {
	if strings.TrimSpace(root) == "" {
		return false, "workspace root unavailable"
	}
	for _, rel := range []string{
		filepath.Join(".idiom", "comprehension", "graph.sqlite"),
		filepath.Join(".idiom", "comprehension", "graph.json"),
	} {
		path := filepath.Join(root, rel)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return true, rel + " present"
		}
	}
	return false, "no local context graph found under .idiom/comprehension"
}

func readinessProfileState(checks []readinessCheck, gates []readinessGateDecision) string {
	state := "pass"
	for _, gate := range gates {
		switch gate.Status {
		case "blocked":
			return "blocked"
		case "degraded":
			state = "degraded"
		}
	}
	for _, check := range checks {
		switch check.State {
		case "blocked":
			return "blocked"
		case "unavailable":
			if state == "pass" {
				state = "degraded"
			}
		case "degraded":
			if state == "pass" {
				state = "degraded"
			}
		}
	}
	return state
}

func readinessProfileDetail(state string) string {
	switch state {
	case "pass":
		return "all required static checks passed"
	case "blocked":
		return "one or more required checks block launch"
	case "degraded":
		return "required contract is visible but one or more checks are degraded or unavailable"
	default:
		return "readiness state unavailable"
	}
}
