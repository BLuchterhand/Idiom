package doctor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/runtime/readiness"
)

var _ = ginkgo.Describe("doctor", func() {
	ginkgo.It("run doctor main emits text report", func() {
		dir := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Chdir(dir)
		ginkgo.GinkgoT().
			Setenv("PATH", "")

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry(nil, &stdout, &stderr, nil)
		out := stdout.String()
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("expected exit 1 (backend missing is a fail), got %d; stdout=%s stderr=%s", code, out, stderr.String()))

		for _, marker := range []string{"[FAIL]", "summary:", "next steps:"} {
			gomega.Expect(out).To(gomega.ContainSubstring(marker), fmt.Sprintf("doctor text output missing %q; got: %s", marker, out))

		}
	})
	ginkgo.It("run doctor main json format", func() {
		dir := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Chdir(dir)

		cfg := filepath.Join(dir, ".idiom", "config.json")
		gomega.Expect(os.MkdirAll(filepath.Dir(cfg), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(cfg, []byte(`{"workspace": {"root": "`+dir+`"}}`), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		_ = RunWithTelemetry([]string{"--json"}, &stdout, &stderr, nil)

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
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &got)).To(gomega.Succeed())
		gomega.Expect(got.Checks).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected at least one check in JSON output"))

		total := got.Summary.Pass + got.Summary.Warn + got.Summary.Fail
		gomega.Expect(total).To(gomega.Equal(len(got.Checks)), fmt.Sprintf("summary counts (%d) don't match check count (%d)", total, len(got.Checks)))

		wantProfiles := map[string]bool{
			"discovery": false,
			"plan":      false,
			"implement": false,
			"eval":      false,
			"sidecar":   false,
		}
		gomega.Expect(got.ReadinessProfiles).To(gomega.HaveLen(len(wantProfiles)), fmt.Sprintf("expected all readiness profiles in JSON output, got %#v", got.ReadinessProfiles))

		for _, profile := range got.ReadinessProfiles {
			if _, ok := wantProfiles[profile.Name]; !ok {
				ginkgo.Fail(fmt.Sprintf("unexpected readiness profile %q in JSON output", profile.Name))
			}
			wantProfiles[profile.Name] = true
			gomega.Expect(profile.Name).NotTo(gomega.BeEmpty(), fmt.Sprintf("profile readiness missing machine-readable fields: %#v", profile))
			gomega.Expect(profile.State).NotTo(gomega.BeEmpty(), fmt.Sprintf("profile readiness missing machine-readable fields: %#v", profile))
			gomega.Expect(profile.Enforcement).NotTo(gomega.BeEmpty(), fmt.Sprintf("profile readiness missing machine-readable fields: %#v", profile))
			gomega.Expect(profile.TaskFamily).NotTo(gomega.BeEmpty(), fmt.Sprintf("profile readiness missing machine-readable fields: %#v", profile))
			gomega.Expect(profile.Contract).NotTo(gomega.BeEmpty(), fmt.Sprintf("profile readiness missing machine-readable fields: %#v", profile))
			gomega.Expect(validReadinessState(profile.State)).To(gomega.BeTrue(), fmt.Sprintf("profile %q has invalid state %q", profile.Name, profile.State))
			gomega.Expect(profile.Gates).NotTo(gomega.BeEmpty(), fmt.Sprintf("profile %q should carry readiness gate decisions", profile.Name))

			for _, gate := range profile.Gates {
				gomega.Expect(gate.Name).NotTo(gomega.BeEmpty(), fmt.Sprintf("profile %q has incomplete gate decision: %#v", profile.Name, gate))
				gomega.Expect(gate.Status).NotTo(gomega.BeEmpty(), fmt.Sprintf("profile %q has incomplete gate decision: %#v", profile.Name, gate))
				gomega.Expect(gate.Severity).NotTo(gomega.BeEmpty(), fmt.Sprintf("profile %q has incomplete gate decision: %#v", profile.Name, gate))
				gomega.Expect(gate.Enforcement).NotTo(gomega.BeEmpty(), fmt.Sprintf("profile %q has incomplete gate decision: %#v", profile.Name, gate))
				gomega.Expect(gate.Evidence).NotTo(gomega.BeEmpty(), fmt.Sprintf("profile %q has incomplete gate decision: %#v", profile.Name, gate))
				if gate.Status != "pass" {
					gomega.Expect(gate.Reason).NotTo(gomega.BeEmpty(), fmt.Sprintf("profile %q non-pass gate must carry reason and remedy: %#v", profile.Name, gate))
					gomega.Expect(gate.Remedy).NotTo(gomega.BeEmpty(), fmt.Sprintf("profile %q non-pass gate must carry reason and remedy: %#v", profile.Name, gate))
				}

			}
			gomega.Expect(profile.Metrics.QueueState).NotTo(gomega.BeEmpty(), fmt.Sprintf("profile %q should carry local-ops metrics, got %#v", profile.Name, profile))
			gomega.Expect(profile.Metrics.ContextBudgetState).NotTo(gomega.BeEmpty(), fmt.Sprintf("profile %q should carry local-ops metrics, got %#v", profile.Name, profile))
			gomega.Expect(profile.Metrics.ClassifierState).NotTo(gomega.BeEmpty(), fmt.Sprintf("profile %q should carry local-ops metrics, got %#v", profile.Name, profile))

			for _, want := range []string{"queue admission", "context budget", "route classifier"} {
				gomega.Expect(doctorProfileHasCheck(profile.Checks, want)).To(gomega.BeTrue(), fmt.Sprintf("profile %q missing readiness check %q in %#v", profile.Name, want, profile.Checks))

			}
		}
		for name, seen := range wantProfiles {
			gomega.Expect(seen).To(gomega.BeTrue(), fmt.Sprintf("missing readiness profile %q in JSON output", name))

		}
	})
	ginkgo.It("check workspace writable does not clobber fixed probe name", func() {
		dir := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Chdir(dir)
		stageDoctorConfig(dir)

		probe := filepath.Join(dir, ".idiom-doctor-write-probe")
		gomega.Expect(os.WriteFile(probe, []byte("keep"), 0o644)).To(gomega.Succeed())

		result := checkWorkspaceWritable(filepath.Join(dir, ".idiom", "config.json"))
		gomega.Expect(result.status).To(gomega.Equal("pass"), fmt.Sprintf("expected writable workspace, got %#v", result))

		data, err := os.ReadFile(probe)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("fixed probe file should still exist: %v", err))
		gomega.Expect(string(data)).To(gomega.Equal("keep"), fmt.Sprintf("fixed probe file was clobbered: %q", string(data)))

	})
	ginkgo.It("doctor readiness gate policy blocks missing backend roles for plan and implement", func() {
		dir := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Chdir(dir)
		stageDoctorConfig(dir)

		for _, profileName := range []string{"plan", "implement"} {
			var stdout, stderr bytes.Buffer

			_ = RunWithTelemetry([]string{"--json", "--profile", profileName}, &stdout, &stderr, nil)

			profile := decodeSingleDoctorProfile(stdout.Bytes())
			gomega.Expect(profile.State).To(gomega.Equal("blocked"), fmt.Sprintf("%s profile should be blocked when backend role config is missing, got %#v", profileName, profile))

			gate := findDoctorGate(profile.Gates, "backend_roles_resolved")
			gomega.Expect(gate.Status).To(gomega.Equal("blocked"), fmt.Sprintf("%s backend gate should be a hard launch blocker, got %#v", profileName, gate))
			gomega.Expect(gate.Severity).To(gomega.Equal("hard"), fmt.Sprintf("%s backend gate should be a hard launch blocker, got %#v", profileName, gate))
			gomega.Expect(gate.RequiredForLaunch).To(gomega.BeTrue(), fmt.Sprintf("%s backend gate should be a hard launch blocker, got %#v", profileName, gate))
			gomega.Expect(gate.Enforcement).To(gomega.Equal("report_only"), fmt.Sprintf("P6.3 gate enforcement should stay report_only, got %#v", gate))

		}
	})
	ginkgo.It("doctor readiness queue unavailable policy is profile specific", func() {
		dir := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Chdir(dir)
		ginkgo.GinkgoT().
			Setenv("IDIOM_RUNNER_SOCKET", filepath.Join(dir, "missing-runner.sock"))
		stageDoctorConfig(dir)
		stageDoctorBackends(dir)

		implement := runDoctorProfileJSON("implement")
		implementGate := findDoctorGate(implement.Gates, "queue_admission_known")
		gomega.Expect(implementGate.Status).To(gomega.Equal("degraded"), fmt.Sprintf("implement queue daemon absence should be a soft degraded gate, got %#v", implementGate))
		gomega.Expect(implementGate.Severity).To(gomega.Equal("soft"), fmt.Sprintf("implement queue daemon absence should be a soft degraded gate, got %#v", implementGate))

		eval := runDoctorProfileJSON("eval")
		evalGate := findDoctorGate(eval.Gates, "queue_admission_known")
		gomega.Expect(evalGate.Status).To(gomega.Equal("blocked"), fmt.Sprintf("eval queue daemon absence should be a hard report-only blocker, got %#v", evalGate))
		gomega.Expect(evalGate.Severity).To(gomega.Equal("hard"), fmt.Sprintf("eval queue daemon absence should be a hard report-only blocker, got %#v", evalGate))
		gomega.Expect(eval.Enforcement).To(gomega.Equal("report_only"), fmt.Sprintf("queue gate must remain visible/report-only until lane-start enforcement lands, got profile=%#v gate=%#v", eval, evalGate))
		gomega.Expect(evalGate.Enforcement).To(gomega.Equal("report_only"), fmt.Sprintf("queue gate must remain visible/report-only until lane-start enforcement lands, got profile=%#v gate=%#v", eval, evalGate))

	})
	ginkgo.It("workspace root from config resolves relative root against config dir", func() {
		dir := ginkgo.GinkgoT().TempDir()
		configDir := filepath.Join(dir, "config")
		gomega.Expect(os.MkdirAll(configDir, 0o755)).To(gomega.Succeed())

		configPath := filepath.Join(configDir, "config.json")
		gomega.Expect(os.WriteFile(configPath, []byte(`{"workspace":{"root":"../repo"}}`), 0o644)).To(gomega.Succeed())

		want := filepath.Clean(filepath.Join(dir, "repo"))
		if got := readiness.WorkspaceRootFromConfig(configPath); got != want {
			ginkgo.Fail(fmt.Sprintf("WorkspaceRootFromConfig() = %q, want %q", got, want))
		}
	})
	ginkgo.It("doctor readiness classifier disabled is soft degradation", func() {
		dir := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Chdir(dir)
		ginkgo.GinkgoT().
			Setenv("IDIOM_RUNNER_SOCKET", filepath.Join(dir, "missing-runner.sock"))
		stageDoctorConfig(dir)
		stageDoctorBackends(dir)

		profile := runDoctorProfileJSON("implement")
		gate := findDoctorGate(profile.Gates, "route_classifier_available_or_fallback_recorded")
		gomega.Expect(gate.Status).To(gomega.Equal("degraded"), fmt.Sprintf("disabled classifier should be a soft degradation with deterministic fallback, got %#v", gate))
		gomega.Expect(gate.Severity).To(gomega.Equal("soft"), fmt.Sprintf("disabled classifier should be a soft degradation with deterministic fallback, got %#v", gate))
		gomega.Expect(gate.RequiredForLaunch).To(gomega.BeFalse(), fmt.Sprintf("disabled classifier should be a soft degradation with deterministic fallback, got %#v", gate))

	})
	ginkgo.It("doctor readiness invalid context budget blocks write profiles only", func() {
		dir := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Chdir(dir)
		ginkgo.GinkgoT().
			Setenv("IDIOM_RUNNER_SOCKET", filepath.Join(dir, "missing-runner.sock"))
		stageDoctorConfigWithBudget(dir, 1024, 2048)
		stageDoctorBackends(dir)

		discovery := runDoctorProfileJSON("discovery")
		discoveryGate := findDoctorGate(discovery.Gates, "context_budget_valid")
		gomega.Expect(discoveryGate.Status).To(gomega.Equal("degraded"), fmt.Sprintf("discovery invalid context budget should degrade, got %#v", discoveryGate))
		gomega.Expect(discoveryGate.Severity).To(gomega.Equal("soft"), fmt.Sprintf("discovery invalid context budget should degrade, got %#v", discoveryGate))

		implement := runDoctorProfileJSON("implement")
		implementGate := findDoctorGate(implement.Gates, "context_budget_valid")
		gomega.Expect(implementGate.Status).To(gomega.Equal("blocked"), fmt.Sprintf("implement invalid context budget should block by policy, got %#v", implementGate))
		gomega.Expect(implementGate.Severity).To(gomega.Equal("hard"), fmt.Sprintf("implement invalid context budget should block by policy, got %#v", implementGate))

	})
	ginkgo.It("run doctor main profile flag filters readiness profiles", func() {
		dir := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Chdir(dir)
		stageDoctorConfig(dir)
		stageDoctorBackends(dir)

		var stdout, stderr bytes.Buffer

		_ = RunWithTelemetry([]string{"--json", "--profile", "implement"}, &stdout, &stderr, nil)

		var got struct {
			ReadinessProfiles []struct {
				Name          string            `json:"name"`
				RequiredRoles []string          `json:"required_roles"`
				RoleAliases   map[string]string `json:"role_aliases"`
			} `json:"readiness_profiles"`
		}
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &got)).To(gomega.Succeed())
		gomega.Expect(got.ReadinessProfiles).To(gomega.HaveLen(1), fmt.Sprintf("expected only implement profile, got %#v", got.ReadinessProfiles))
		gomega.Expect(got.ReadinessProfiles[0].Name).To(gomega.Equal("implement"), fmt.Sprintf("expected only implement profile, got %#v", got.ReadinessProfiles))
		gomega.Expect(got.ReadinessProfiles[0].RoleAliases["executor"]).To(gomega.Equal("coder"), fmt.Sprintf("implement readiness should resolve executor role, got %#v", got.ReadinessProfiles[0]))

	})
	ginkgo.It("run doctor main unknown profile exits two", func() {
		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"--profile", "finance"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(2), fmt.Sprintf("unknown readiness profile must exit 2, got %d", code))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("unknown readiness profile"), fmt.Sprintf("expected profile error, got stderr=%q", stderr.String()))

	})
	ginkgo.It("doctor readiness profiles are generic across client classes", func() {
		dir := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Chdir(dir)
		stageDoctorConfig(dir)
		stageDoctorBackends(dir)

		var stdout, stderr bytes.Buffer

		_ = RunWithTelemetry([]string{"--json"}, &stdout, &stderr, nil)

		var got struct {
			ReadinessProfiles []map[string]any `json:"readiness_profiles"`
		}
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &got)).To(gomega.Succeed())

		names := map[string]bool{}

		for _, profile := range got.ReadinessProfiles {
			name, _ := profile["name"].(string)

			names[name] = true

			if _, ok := profile["client_class"]; ok {
				ginkgo.Fail(fmt.Sprintf("readiness profile must not hardcode client classes: %#v", profile))
			}

			if _, ok := profile["client_classes"]; ok {
				ginkgo.Fail(fmt.Sprintf("readiness profile must not hardcode client classes: %#v", profile))
			}
		}

		for _, want := range []string{"discovery", "plan", "implement", "eval", "sidecar"} {
			gomega.Expect(names[want]).To(gomega.BeTrue(), fmt.Sprintf("missing generic readiness profile %q in %#v", want, got.ReadinessProfiles))

		}

		classes := []string{"finance", "healthcare/life sciences", "public-sector", "enterprise SaaS", "tooling-vendor", "local-model/private-code", "open-source"}
		output := stdout.String()

		for _, class := range classes {
			gomega.Expect(output).NotTo(gomega.ContainSubstring(class), fmt.Sprintf("doctor readiness output should stay generic; leaked client class %q in %s", class, output))

		}
	})
	ginkgo.It("check backends fails when path empty", func() {
		if runtime.GOOS == "windows" {
			ginkgo.Skip("PATH manipulation differs on windows; backend check is unix-host today")
		}
		ginkgo.GinkgoT().
			Setenv("PATH", "")

		r := checkBackends()
		gomega.Expect(r.status).To(gomega.Equal("fail"), fmt.Sprintf("expected fail with empty PATH, got %s (%s)", r.status, r.detail))
		gomega.Expect(r.remedy).NotTo(gomega.Equal(""), fmt.Sprintf("backend fail must carry a remedy line"))
		gomega.Expect(r.remedy).To(gomega.ContainSubstring("mlx_lm.server"), fmt.Sprintf("remedy should name a candidate runner; got: %s", r.remedy))

	})
	ginkgo.It("host tool checks use path", func() {
		if runtime.GOOS == "windows" {
			ginkgo.Skip("executable fixture uses POSIX mode bits")
		}

		binDir := ginkgo.GinkgoT().TempDir()
		writeDoctorExecutable(filepath.Join(binDir, "python3"))
		writeDoctorExecutable(filepath.Join(binDir, "git"))
		ginkgo.GinkgoT().
			Setenv("PATH", binDir)

		if result := checkPython3(); result.status != "pass" {
			ginkgo.Fail(fmt.Sprintf("checkPython3 status = %s (%s), want pass", result.status, result.detail))
		}
		if result := checkGit(); result.status != "pass" {
			ginkgo.Fail(fmt.Sprintf("checkGit status = %s (%s), want pass", result.status, result.detail))
		}
	})
	ginkgo.It("check config file missing", func() {

		dir := ginkgo.GinkgoT().TempDir()
		r := checkConfigFile(filepath.Join(dir, "nonexistent.json"))
		gomega.Expect(r.status).To(gomega.Equal("fail"), fmt.Sprintf("expected fail for missing config, got %s", r.status))
		gomega.Expect(r.remedy).To(gomega.ContainSubstring("idiom init"), fmt.Sprintf("remedy should point at idiom init; got: %s", r.remedy))

	})
	ginkgo.It("check config file invalid json", func() {
		dir := ginkgo.GinkgoT().TempDir()
		bad := filepath.Join(dir, "config.json")
		gomega.Expect(os.WriteFile(bad, []byte("{ this is not json"), 0o644)).To(gomega.Succeed())

		r := checkConfigFile(bad)
		gomega.Expect(r.status).To(gomega.Equal("fail"), fmt.Sprintf("expected fail for malformed config, got %s", r.status))
		gomega.Expect(r.detail).To(gomega.ContainSubstring("not valid JSON"), fmt.Sprintf("detail should call out the JSON parse failure; got: %s", r.detail))

	})
	ginkgo.It("check workspace writable passes when writable", func() {
		dir := ginkgo.GinkgoT().TempDir()
		cfg := filepath.Join(dir, "config.json")
		gomega.Expect(os.WriteFile(cfg, []byte(`{"workspace": {"root": "`+dir+`"}}`), 0o644)).To(gomega.Succeed())

		r := checkWorkspaceWritable(cfg)
		gomega.Expect(r.status).To(gomega.Equal("pass"), fmt.Sprintf("expected pass with writable workspace, got %s (%s)", r.status, r.detail))
		gomega.Expect(r.detail).To(gomega.ContainSubstring(dir), fmt.Sprintf("expected detail to name the workspace root; got: %s", r.detail))

	})
	ginkgo.It("check workspace writable fails on read only", func() {
		if os.Getuid() == 0 {
			ginkgo.Skip("running as root; chmod doesn't constrain writes")
		}

		dir := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.Chmod(dir, 0o555)).To(gomega.Succeed())
		ginkgo.DeferCleanup(func() { _ = os.Chmod(dir, 0o755) })

		cfg := filepath.Join(ginkgo.GinkgoT().TempDir(), "config.json")
		gomega.Expect(os.WriteFile(cfg, []byte(`{"workspace": {"root": "`+dir+`"}}`), 0o644)).To(gomega.Succeed())

		r := checkWorkspaceWritable(cfg)
		gomega.Expect(r.status).To(gomega.Equal("fail"), fmt.Sprintf("expected fail for read-only workspace, got %s (%s)", r.status, r.detail))
		gomega.Expect(r.remedy).NotTo(gomega.Equal(""), fmt.Sprintf("remedy should be set on fail"))

	})
})

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

func writeDoctorExecutable(path string) {
	gomega.Expect(os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755)).To(gomega.Succeed())

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

func runDoctorProfileJSON(profileName string) doctorProfileJSON {

	var stdout, stderr bytes.Buffer

	_ = RunWithTelemetry([]string{"--json", "--profile", profileName}, &stdout, &stderr, nil)

	return decodeSingleDoctorProfile(stdout.Bytes())
}

func decodeSingleDoctorProfile(body []byte) doctorProfileJSON {

	var got struct {
		ReadinessProfiles []doctorProfileJSON `json:"readiness_profiles"`
	}
	gomega.Expect(json.Unmarshal(body, &got)).To(gomega.Succeed())
	gomega.Expect(got.ReadinessProfiles).To(gomega.HaveLen(1), fmt.Sprintf("expected one readiness profile, got %#v", got.ReadinessProfiles))

	return got.ReadinessProfiles[0]
}

func findDoctorGate(gates []struct {
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

	for _, gate := range gates {
		if gate.Name == name {
			return gate
		}
	}
	ginkgo.Fail(fmt.Sprintf("missing gate %q in %#v", name, gates))

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

func stageDoctorConfig(root string) {

	cfg := filepath.Join(root, ".idiom", "config.json")
	gomega.Expect(os.MkdirAll(filepath.Dir(cfg), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(cfg, []byte(`{"workspace": {"root": "`+root+`"}}`), 0o644)).To(gomega.Succeed())

}

func stageDoctorConfigWithBudget(root string, ctxSize int, maxTokens int) {

	cfg := filepath.Join(root, ".idiom", "config.json")
	gomega.Expect(os.MkdirAll(filepath.Dir(cfg), 0o755)).To(gomega.Succeed())

	body := fmt.Sprintf(`{
  "workspace": {"root": %q},
  "llama_server": {"ctx_size": %d},
  "chat": {"max_tokens": %d}
}`, root, ctxSize, maxTokens)
	gomega.Expect(os.WriteFile(cfg, []byte(body), 0o644)).To(gomega.Succeed())

}

func stageDoctorBackends(root string) {

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
	gomega.Expect(os.WriteFile(path, []byte(body), 0o644)).To(gomega.Succeed())

}
