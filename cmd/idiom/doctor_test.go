package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestRunDoctorMainEmitsTextReport verifies the human-readable format
// — a status-prefixed line per check and a summary line. Test pins
// only the structural anchors (status icons, "summary:", "next steps:")
// so check copy can evolve without breaking the contract.
func TestRunDoctorMainEmitsTextReport(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)         // doctor uses cwd for path resolution
	t.Setenv("PATH", "") // force backend check to fail so we exercise remedy emission

	var stdout, stderr bytes.Buffer
	code := runDoctorMain(nil, &stdout, &stderr)
	out := stdout.String()
	if code != 1 {
		t.Fatalf("expected exit 1 (backend missing is a fail), got %d; stdout=%s stderr=%s", code, out, stderr.String())
	}
	for _, marker := range []string{"[FAIL]", "summary:", "next steps:"} {
		if !strings.Contains(out, marker) {
			t.Fatalf("doctor text output missing %q; got: %s", marker, out)
		}
	}
}

// TestRunDoctorMainJSONFormat verifies --json emits one parseable JSON
// object with the expected top-level shape. Wrappers (CI, dashboards)
// consume this; structural drift breaks every consumer.
func TestRunDoctorMainJSONFormat(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// Stage a config so checkConfigFile passes — we want non-fail
	// results too, not just failures, to verify the JSON encoder handles
	// every status.
	cfg := filepath.Join(dir, ".idiom", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(cfg, []byte(`{"workspace": {"root": "`+dir+`"}}`), 0o644); err != nil {
		t.Fatalf("stage config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	_ = runDoctorMain([]string{"--json"}, &stdout, &stderr)

	var got struct {
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Detail string `json:"detail"`
			Remedy string `json:"remedy"`
		} `json:"checks"`
		Summary struct {
			Pass int `json:"pass"`
			Warn int `json:"warn"`
			Fail int `json:"fail"`
		} `json:"summary"`
		ReadinessProfiles []struct {
			Name        string `json:"name"`
			State       string `json:"state"`
			Enforcement string `json:"enforcement"`
			TaskFamily  string `json:"task_family"`
			Contract    string `json:"contract"`
			Gates       []struct {
				Name              string `json:"name"`
				Status            string `json:"status"`
				Severity          string `json:"severity"`
				Enforcement       string `json:"enforcement"`
				RequiredForLaunch bool   `json:"required_for_launch"`
				Evidence          string `json:"evidence"`
				Reason            string `json:"reason"`
				Remedy            string `json:"remedy"`
			} `json:"gates"`
			HardFailures []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"hard_failures"`
			SoftDegradations []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"soft_degradations"`
			Checks []struct {
				Name  string `json:"name"`
				State string `json:"state"`
			} `json:"checks"`
			Unavailable []struct {
				Check  string `json:"check"`
				Reason string `json:"reason"`
			} `json:"unavailable"`
			Metrics struct {
				QueueState         string `json:"queue_state"`
				ContextBudgetState string `json:"context_budget_state"`
				ClassifierState    string `json:"classifier_state"`
			} `json:"metrics"`
		} `json:"readiness_profiles"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("doctor --json output not parseable: %v\noutput: %s", err, stdout.String())
	}
	if len(got.Checks) == 0 {
		t.Fatalf("expected at least one check in JSON output")
	}
	total := got.Summary.Pass + got.Summary.Warn + got.Summary.Fail
	if total != len(got.Checks) {
		t.Fatalf("summary counts (%d) don't match check count (%d)", total, len(got.Checks))
	}
	if len(got.ReadinessProfiles) != len(doctorReadinessProfileSpecs) {
		t.Fatalf("expected all readiness profiles in JSON output, got %#v", got.ReadinessProfiles)
	}
	for _, profile := range got.ReadinessProfiles {
		if profile.Name == "" || profile.State == "" || profile.Enforcement == "" || profile.TaskFamily == "" || profile.Contract == "" {
			t.Fatalf("profile readiness missing machine-readable fields: %#v", profile)
		}
		if !validReadinessState(profile.State) {
			t.Fatalf("profile %q has invalid state %q", profile.Name, profile.State)
		}
		if len(profile.Gates) == 0 {
			t.Fatalf("profile %q should carry readiness gate decisions", profile.Name)
		}
		for _, gate := range profile.Gates {
			if gate.Name == "" || gate.Status == "" || gate.Severity == "" || gate.Enforcement == "" || gate.Evidence == "" {
				t.Fatalf("profile %q has incomplete gate decision: %#v", profile.Name, gate)
			}
			if gate.Status != "pass" && (gate.Reason == "" || gate.Remedy == "") {
				t.Fatalf("profile %q non-pass gate must carry reason and remedy: %#v", profile.Name, gate)
			}
		}
		if profile.Metrics.QueueState == "" || profile.Metrics.ContextBudgetState == "" || profile.Metrics.ClassifierState == "" {
			t.Fatalf("profile %q should carry local-ops metrics, got %#v", profile.Name, profile)
		}
		for _, want := range []string{"queue admission", "context budget", "route classifier"} {
			if !doctorProfileHasCheck(profile.Checks, want) {
				t.Fatalf("profile %q missing readiness check %q in %#v", profile.Name, want, profile.Checks)
			}
		}
	}
}

func TestCheckWorkspaceWritableDoesNotClobberFixedProbeName(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	stageDoctorConfig(t, dir)
	probe := filepath.Join(dir, ".idiom-doctor-write-probe")
	if err := os.WriteFile(probe, []byte("keep"), 0o644); err != nil {
		t.Fatalf("stage fixed probe file: %v", err)
	}

	result := checkWorkspaceWritable(filepath.Join(dir, ".idiom", "config.json"))

	if result.status != "pass" {
		t.Fatalf("expected writable workspace, got %#v", result)
	}
	data, err := os.ReadFile(probe)
	if err != nil {
		t.Fatalf("fixed probe file should still exist: %v", err)
	}
	if string(data) != "keep" {
		t.Fatalf("fixed probe file was clobbered: %q", string(data))
	}
}

func TestDoctorReadinessGatePolicyBlocksMissingBackendRolesForPlanAndImplement(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	stageDoctorConfig(t, dir)

	for _, profileName := range []string{"plan", "implement"} {
		var stdout, stderr bytes.Buffer
		_ = runDoctorMain([]string{"--json", "--profile", profileName}, &stdout, &stderr)
		profile := decodeSingleDoctorProfile(t, stdout.Bytes())
		if profile.State != "blocked" {
			t.Fatalf("%s profile should be blocked when backend role config is missing, got %#v", profileName, profile)
		}
		gate := findDoctorGate(t, profile.Gates, "backend_roles_resolved")
		if gate.Status != "blocked" || gate.Severity != "hard" || !gate.RequiredForLaunch {
			t.Fatalf("%s backend gate should be a hard launch blocker, got %#v", profileName, gate)
		}
		if gate.Enforcement != "report_only" {
			t.Fatalf("P6.3 gate enforcement should stay report_only, got %#v", gate)
		}
	}
}

func TestDoctorReadinessQueueUnavailablePolicyIsProfileSpecific(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("IDIOM_RUNNER_SOCKET", filepath.Join(dir, "missing-runner.sock"))
	stageDoctorConfig(t, dir)
	stageDoctorBackends(t, dir)

	implement := runDoctorProfileJSON(t, "implement")
	implementGate := findDoctorGate(t, implement.Gates, "queue_admission_known")
	if implementGate.Status != "degraded" || implementGate.Severity != "soft" {
		t.Fatalf("implement queue daemon absence should be a soft degraded gate, got %#v", implementGate)
	}

	eval := runDoctorProfileJSON(t, "eval")
	evalGate := findDoctorGate(t, eval.Gates, "queue_admission_known")
	if evalGate.Status != "blocked" || evalGate.Severity != "hard" {
		t.Fatalf("eval queue daemon absence should be a hard report-only blocker, got %#v", evalGate)
	}
	if eval.Enforcement != "report_only" || evalGate.Enforcement != "report_only" {
		t.Fatalf("queue gate must remain visible/report-only until lane-start enforcement lands, got profile=%#v gate=%#v", eval, evalGate)
	}
}

func TestWorkspaceRootFromConfigResolvesRelativeRootAgainstConfigDir(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"workspace":{"root":"../repo"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	want := filepath.Clean(filepath.Join(dir, "repo"))
	if got := workspaceRootFromConfig(configPath); got != want {
		t.Fatalf("workspaceRootFromConfig() = %q, want %q", got, want)
	}
}

func TestDoctorReadinessClassifierDisabledIsSoftDegradation(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("IDIOM_RUNNER_SOCKET", filepath.Join(dir, "missing-runner.sock"))
	stageDoctorConfig(t, dir)
	stageDoctorBackends(t, dir)

	profile := runDoctorProfileJSON(t, "implement")
	gate := findDoctorGate(t, profile.Gates, "route_classifier_available_or_fallback_recorded")
	if gate.Status != "degraded" || gate.Severity != "soft" || gate.RequiredForLaunch {
		t.Fatalf("disabled classifier should be a soft degradation with deterministic fallback, got %#v", gate)
	}
}

func TestDoctorReadinessInvalidContextBudgetBlocksWriteProfilesOnly(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("IDIOM_RUNNER_SOCKET", filepath.Join(dir, "missing-runner.sock"))
	stageDoctorConfigWithBudget(t, dir, 1024, 2048)
	stageDoctorBackends(t, dir)

	discovery := runDoctorProfileJSON(t, "discovery")
	discoveryGate := findDoctorGate(t, discovery.Gates, "context_budget_valid")
	if discoveryGate.Status != "degraded" || discoveryGate.Severity != "soft" {
		t.Fatalf("discovery invalid context budget should degrade, got %#v", discoveryGate)
	}

	implement := runDoctorProfileJSON(t, "implement")
	implementGate := findDoctorGate(t, implement.Gates, "context_budget_valid")
	if implementGate.Status != "blocked" || implementGate.Severity != "hard" {
		t.Fatalf("implement invalid context budget should block by policy, got %#v", implementGate)
	}
}

func doctorProfileHasCheck(checks []struct {
	Name  string `json:"name"`
	State string `json:"state"`
}, name string) bool {
	for _, check := range checks {
		if check.Name == name && check.State != "" {
			return true
		}
	}
	return false
}

func TestRunDoctorMainProfileFlagFiltersReadinessProfiles(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	stageDoctorConfig(t, dir)
	stageDoctorBackends(t, dir)

	var stdout, stderr bytes.Buffer
	_ = runDoctorMain([]string{"--json", "--profile", "implement"}, &stdout, &stderr)

	var got struct {
		ReadinessProfiles []struct {
			Name          string            `json:"name"`
			RequiredRoles []string          `json:"required_roles"`
			RoleAliases   map[string]string `json:"role_aliases"`
		} `json:"readiness_profiles"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("doctor --json --profile implement not parseable: %v\noutput: %s", err, stdout.String())
	}
	if len(got.ReadinessProfiles) != 1 || got.ReadinessProfiles[0].Name != "implement" {
		t.Fatalf("expected only implement profile, got %#v", got.ReadinessProfiles)
	}
	if got.ReadinessProfiles[0].RoleAliases["executor"] != "coder" {
		t.Fatalf("implement readiness should resolve executor role, got %#v", got.ReadinessProfiles[0])
	}
}

func TestRunDoctorMainUnknownProfileExitsTwo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runDoctorMain([]string{"--profile", "finance"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("unknown readiness profile must exit 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), "unknown readiness profile") {
		t.Fatalf("expected profile error, got stderr=%q", stderr.String())
	}
}

func TestDoctorReadinessProfilesAreGenericAcrossClientClasses(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	stageDoctorConfig(t, dir)
	stageDoctorBackends(t, dir)

	var stdout, stderr bytes.Buffer
	_ = runDoctorMain([]string{"--json"}, &stdout, &stderr)

	var got struct {
		ReadinessProfiles []map[string]any `json:"readiness_profiles"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("doctor --json output not parseable: %v\noutput: %s", err, stdout.String())
	}
	names := map[string]bool{}
	for _, profile := range got.ReadinessProfiles {
		name, _ := profile["name"].(string)
		names[name] = true
		if _, ok := profile["client_class"]; ok {
			t.Fatalf("readiness profile must not hardcode client classes: %#v", profile)
		}
		if _, ok := profile["client_classes"]; ok {
			t.Fatalf("readiness profile must not hardcode client classes: %#v", profile)
		}
	}
	for _, want := range []string{"discovery", "plan", "implement", "eval", "sidecar"} {
		if !names[want] {
			t.Fatalf("missing generic readiness profile %q in %#v", want, got.ReadinessProfiles)
		}
	}
	classes := []string{"finance", "healthcare/life sciences", "public-sector", "enterprise SaaS", "tooling-vendor", "local-model/private-code", "open-source"}
	output := stdout.String()
	for _, class := range classes {
		if strings.Contains(output, class) {
			t.Fatalf("doctor readiness output should stay generic; leaked client class %q in %s", class, output)
		}
	}
}

// TestCheckBackendsFailsWhenPathEmpty forces an empty PATH and verifies
// the backend check returns "fail" with a remedy that names the
// supported runners. Doctor's first job is to get a fresh user from
// "broken" to "fixed" with one actionable line.
func TestCheckBackendsFailsWhenPathEmpty(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH manipulation differs on windows; backend check is unix-host today")
	}
	t.Setenv("PATH", "")
	r := checkBackends()
	if r.status != "fail" {
		t.Fatalf("expected fail with empty PATH, got %s (%s)", r.status, r.detail)
	}
	if r.remedy == "" {
		t.Fatalf("backend fail must carry a remedy line")
	}
	if !strings.Contains(r.remedy, "mlx_lm.server") {
		t.Fatalf("remedy should name a candidate runner; got: %s", r.remedy)
	}
}

// TestCheckConfigFileMissing verifies a missing config produces a fail
// pointing at `idiom init`. This is the path a fresh user hits before
// running init at all.
func TestCheckConfigFileMissing(t *testing.T) {
	dir := t.TempDir()
	r := checkConfigFile(filepath.Join(dir, "nonexistent.json"))
	if r.status != "fail" {
		t.Fatalf("expected fail for missing config, got %s", r.status)
	}
	if !strings.Contains(r.remedy, "idiom init") {
		t.Fatalf("remedy should point at idiom init; got: %s", r.remedy)
	}
}

// TestCheckConfigFileInvalidJSON verifies a malformed config surfaces
// the parse error as fail with a syntax-fix remedy.
func TestCheckConfigFileInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "config.json")
	if err := os.WriteFile(bad, []byte("{ this is not json"), 0o644); err != nil {
		t.Fatalf("stage bad config: %v", err)
	}
	r := checkConfigFile(bad)
	if r.status != "fail" {
		t.Fatalf("expected fail for malformed config, got %s", r.status)
	}
	if !strings.Contains(r.detail, "not valid JSON") {
		t.Fatalf("detail should call out the JSON parse failure; got: %s", r.detail)
	}
}

// TestCheckWorkspaceWritablePassesWhenWritable verifies the happy path
// — a config with a writable workspace.root produces a pass with the
// path in the detail line.
func TestCheckWorkspaceWritablePassesWhenWritable(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfg, []byte(`{"workspace": {"root": "`+dir+`"}}`), 0o644); err != nil {
		t.Fatalf("stage config: %v", err)
	}
	r := checkWorkspaceWritable(cfg)
	if r.status != "pass" {
		t.Fatalf("expected pass with writable workspace, got %s (%s)", r.status, r.detail)
	}
	if !strings.Contains(r.detail, dir) {
		t.Fatalf("expected detail to name the workspace root; got: %s", r.detail)
	}
}

// TestCheckWorkspaceWritableFailsOnReadOnly verifies the failure path
// surfaces both detail and remedy. Tested by chmod'ing the workspace
// to read-only; skipped on platforms where the test process retains
// override permissions (root, some CI envs).
func TestCheckWorkspaceWritableFailsOnReadOnly(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root; chmod doesn't constrain writes")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	cfg := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfg, []byte(`{"workspace": {"root": "`+dir+`"}}`), 0o644); err != nil {
		t.Fatalf("stage config: %v", err)
	}
	r := checkWorkspaceWritable(cfg)
	if r.status != "fail" {
		t.Fatalf("expected fail for read-only workspace, got %s (%s)", r.status, r.detail)
	}
	if r.remedy == "" {
		t.Fatalf("remedy should be set on fail")
	}
}

func validReadinessState(state string) bool {
	switch state {
	case "pass", "degraded", "blocked", "unavailable":
		return true
	default:
		return false
	}
}

type doctorProfileJSON struct {
	Name              string `json:"name"`
	State             string `json:"state"`
	Enforcement       string `json:"enforcement"`
	RequiredForLaunch bool   `json:"required_for_launch"`
	Gates             []struct {
		Name              string `json:"name"`
		Status            string `json:"status"`
		Severity          string `json:"severity"`
		Enforcement       string `json:"enforcement"`
		RequiredForLaunch bool   `json:"required_for_launch"`
		Evidence          string `json:"evidence"`
		Reason            string `json:"reason"`
		Remedy            string `json:"remedy"`
	} `json:"gates"`
	HardFailures []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"hard_failures"`
	SoftDegradations []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"soft_degradations"`
}

func runDoctorProfileJSON(t *testing.T, profileName string) doctorProfileJSON {
	t.Helper()
	var stdout, stderr bytes.Buffer
	_ = runDoctorMain([]string{"--json", "--profile", profileName}, &stdout, &stderr)
	return decodeSingleDoctorProfile(t, stdout.Bytes())
}

func decodeSingleDoctorProfile(t *testing.T, body []byte) doctorProfileJSON {
	t.Helper()
	var got struct {
		ReadinessProfiles []doctorProfileJSON `json:"readiness_profiles"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("doctor --json profile output not parseable: %v\noutput: %s", err, string(body))
	}
	if len(got.ReadinessProfiles) != 1 {
		t.Fatalf("expected one readiness profile, got %#v", got.ReadinessProfiles)
	}
	return got.ReadinessProfiles[0]
}

func findDoctorGate(t *testing.T, gates []struct {
	Name              string `json:"name"`
	Status            string `json:"status"`
	Severity          string `json:"severity"`
	Enforcement       string `json:"enforcement"`
	RequiredForLaunch bool   `json:"required_for_launch"`
	Evidence          string `json:"evidence"`
	Reason            string `json:"reason"`
	Remedy            string `json:"remedy"`
}, name string) struct {
	Name              string `json:"name"`
	Status            string `json:"status"`
	Severity          string `json:"severity"`
	Enforcement       string `json:"enforcement"`
	RequiredForLaunch bool   `json:"required_for_launch"`
	Evidence          string `json:"evidence"`
	Reason            string `json:"reason"`
	Remedy            string `json:"remedy"`
} {
	t.Helper()
	for _, gate := range gates {
		if gate.Name == name {
			return gate
		}
	}
	t.Fatalf("missing gate %q in %#v", name, gates)
	return struct {
		Name              string `json:"name"`
		Status            string `json:"status"`
		Severity          string `json:"severity"`
		Enforcement       string `json:"enforcement"`
		RequiredForLaunch bool   `json:"required_for_launch"`
		Evidence          string `json:"evidence"`
		Reason            string `json:"reason"`
		Remedy            string `json:"remedy"`
	}{}
}

func stageDoctorConfig(t *testing.T, root string) {
	t.Helper()
	cfg := filepath.Join(root, ".idiom", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(cfg, []byte(`{"workspace": {"root": "`+root+`"}}`), 0o644); err != nil {
		t.Fatalf("stage config: %v", err)
	}
}

func stageDoctorConfigWithBudget(t *testing.T, root string, ctxSize int, maxTokens int) {
	t.Helper()
	cfg := filepath.Join(root, ".idiom", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	body := fmt.Sprintf(`{
  "workspace": {"root": %q},
  "llama_server": {"ctx_size": %d},
  "chat": {"max_tokens": %d}
}`, root, ctxSize, maxTokens)
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatalf("stage config: %v", err)
	}
}

func stageDoctorBackends(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, ".idiom", "backends.json")
	body := `{
  "default_profile": "local",
  "profiles": {
    "local": {
      "models": [
        {"alias": "planner", "binary": "mlx_lm.server", "model_path": "/models/planner", "port": 47780, "roles": ["planner", "classifier"]},
        {"alias": "coder", "binary": "mlx_lm.server", "model_path": "/models/coder", "port": 47781, "roles": ["executor"]}
      ],
      "proxy": {
        "default_planner": "planner",
        "default_executor": "coder",
        "default_classifier": "planner"
      }
    }
  }
}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("stage backends: %v", err)
	}
}
