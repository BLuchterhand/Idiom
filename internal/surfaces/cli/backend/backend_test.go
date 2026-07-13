package backend

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/runtime/backends"
)

const sampleBackendsForCLI = `{
  "profiles": {
    "qwen-pair": {
      "models": [
        {"alias": "qwen3.6-35b", "binary": "mlx_lm.server", "port": 47780, "max_tokens": 8192},
        {"alias": "qwen3-coder-30b", "binary": "mlx_lm.server", "port": 47781, "max_tokens": 8192}
      ],
      "proxy": {"listen": 47680, "default_alias": "qwen3.6-35b", "default_executor": "qwen3-coder-30b"}
    }
  }
}`

var _ = ginkgo.Describe("backend", func() {
	ginkgo.It("run backend list prints profiles from project root", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(sampleBackendsForCLI), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"list", "--project-root", root}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("backend list expected exit 0, got %d (stderr=%s)", code, stderr.String()))

		out := stdout.String()
		for _, marker := range []string{
			`profile "qwen-pair"`,
			`proxy listen=47680 default="qwen3.6-35b" default_executor="qwen3-coder-30b"`,
			`executor_alias="qwen3-coder-30b"`,
			`alias="qwen3.6-35b"`,
			`alias="qwen3-coder-30b"`,
		} {
			gomega.Expect(out).To(gomega.ContainSubstring(marker), fmt.Sprintf("backend list missing %q; full output:\n%s", marker, out))

		}
	})
	ginkgo.It("run backend list uses executor role resolver", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)).To(gomega.Succeed())

		body := `{"profiles":{"single":{"models":[{"alias":"solo-model","binary":"llama-server","port":47780}]}}}`
		gomega.Expect(os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(body), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"list", "--project-root", root}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("backend list expected exit 0, got %d (stderr=%s)", code, stderr.String()))

		want := `executor_alias="solo-model"`
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(want), fmt.Sprintf("backend list must use shared executor resolver; want %q in:\n%s", want, stdout.String()))

	})
	ginkgo.It("run backend list shows default executor", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)).To(gomega.Succeed())

		body := `{"profiles":{"pair":{"proxy":{"listen":47770,"default_alias":"base","default_executor":"exec"},"models":[{"alias":"base"},{"alias":"exec"}]}}}`
		gomega.Expect(os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(body), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"list", "--project-root", root}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("backend list expected exit 0, got %d (stderr=%s)", code, stderr.String()))

		out := stdout.String()
		gomega.Expect(out).To(gomega.ContainSubstring(`proxy listen=47770 default="base" default_executor="exec"`), fmt.Sprintf("backend list must show default_executor, got:\n%s", out))
		gomega.Expect(out).NotTo(gomega.ContainSubstring("default_coder"), fmt.Sprintf("backend list must not advertise legacy default_coder, got:\n%s", out))
		gomega.Expect(out).To(gomega.ContainSubstring(`executor_alias="exec"`), fmt.Sprintf("backend list must resolve default_executor through executor_alias, got:\n%s", out))

	})
	ginkgo.It("run backend list missing file exits one", func() {
		root := ginkgo.GinkgoT().TempDir()

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"list", "--project-root", root}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("missing backends.json must exit 1 (parity with start/stop/status), got %d", code))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("no backends.json at"), fmt.Sprintf("stderr should explain the missing file; got: %s", stderr.String()))

	})
	ginkgo.It("run backend list rejects both file and project root", func() {
		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"list", "--file", "/x.json", "--project-root", "/y"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(2), fmt.Sprintf("--file + --project-root must exit 2, got %d", code))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("not both"), fmt.Sprintf("error should explain the conflict; got: %s", stderr.String()))

	})
	ginkgo.It("run backend list malformed file exits one", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(`{`), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"list", "--project-root", root}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("malformed backends.json must exit 1, got %d", code))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("backends:"), fmt.Sprintf("error must be tagged with package name; got: %s", stderr.String()))

	})
	ginkgo.It("run backend unknown subcommand exits two", func() {
		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"definitely-not-a-subcommand"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(2), fmt.Sprintf("unknown subcommand must exit 2, got %d", code))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("unknown subcommand"), fmt.Sprintf("error must name the subcommand as unknown; got: %s", stderr.String()))

	})
	ginkgo.It("run backend list explicit file flag", func() {
		dir := ginkgo.GinkgoT().TempDir()
		path := filepath.Join(dir, "custom-backends.json")
		gomega.Expect(os.WriteFile(path, []byte(sampleBackendsForCLI), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"list", "--file", path}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("--file form expected exit 0, got %d (stderr=%s)", code, stderr.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`profile "qwen-pair"`), fmt.Sprintf("expected profile header in output: %s", stdout.String()))

	})
	ginkgo.It("run backend start reports ready for each model", func() {

		root := ginkgo.GinkgoT().TempDir()
		writeBackendsForRoot(root, sampleBackendsForCLI)
		withStubbedBackendOps(
			func(_ backends.Profile, _ backends.LauncherOptions) []backends.LaunchResult {
				return []backends.LaunchResult{
					{Alias: "qwen3.6-35b", PID: 1001, Port: 47780, Ready: true, LogPath: "/tmp/a.log"},
					{Alias: "qwen3-coder-30b", PID: 1002, Port: 47781, Ready: true, LogPath: "/tmp/b.log"},
				}
			}, nil, nil)

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"start", "--project-root", root}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("clean start must exit 0, got %d (stderr=%s)", code, stderr.String()))

		out := stdout.String()
		for _, want := range []string{"READY qwen3.6-35b", "READY qwen3-coder-30b", "pid=1001", "pid=1002"} {
			gomega.Expect(out).To(gomega.ContainSubstring(want), fmt.Sprintf("output missing %q: %s", want, out))

		}
	})
	ginkgo.It("run backend start fails when any model times out", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeBackendsForRoot(root, sampleBackendsForCLI)
		withStubbedBackendOps(
			func(_ backends.Profile, _ backends.LauncherOptions) []backends.LaunchResult {
				return []backends.LaunchResult{
					{Alias: "qwen3.6-35b", PID: 1001, Port: 47780, Ready: true, LogPath: "/tmp/a.log"},
					{Alias: "qwen3-coder-30b", PID: 1002, Port: 47781, Ready: false, LogPath: "/tmp/b.log"},
				}
			}, nil, nil)

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"start", "--project-root", root}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("partial failure must exit 1, got %d", code))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("TIMEOUT qwen3-coder-30b"), fmt.Sprintf("output should explain the timing-out backend: %s", stdout.String()))

	})
	ginkgo.It("run backend start treats already running as non failure", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeBackendsForRoot(root, sampleBackendsForCLI)
		withStubbedBackendOps(
			func(_ backends.Profile, _ backends.LauncherOptions) []backends.LaunchResult {
				return []backends.LaunchResult{
					{Alias: "qwen3.6-35b", Port: 47780, Ready: true, Err: makeAlreadyRunningErr()},
					{Alias: "qwen3-coder-30b", Port: 47781, Ready: true, PID: 999, LogPath: "/tmp/x.log"},
				}
			}, nil, nil)

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"start", "--project-root", root}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("already-running must not fail; got code=%d stdout=%s", code, stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("NOTE qwen3.6-35b"), fmt.Sprintf("already-running should report NOTE: %s", stdout.String()))

	})
	ginkgo.It("run backend start missing backends json exits one", func() {
		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"start", "--project-root", ginkgo.GinkgoT().TempDir()}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("missing backends.json must exit 1, got %d", code))

	})
	ginkgo.It("run backend start multi profile without flag exits two", func() {
		root := ginkgo.GinkgoT().TempDir()
		body := `{"profiles": {"a": {"models": []}, "b": {"models": []}}}`
		writeBackendsForRoot(root, body)

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"start", "--project-root", root}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(2), fmt.Sprintf("multi-profile without --profile must exit 2, got %d", code))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("multiple profiles"), fmt.Sprintf("error should explain ambiguity: %s", stderr.String()))

	})
	ginkgo.It("run backend start uses default profile when configured", func() {
		root := ginkgo.GinkgoT().TempDir()
		body := `{
		"default_profile": "qwen-pair",
		"profiles": {
			"qwen-pair": {"models": [{"alias": "qwen3.6-35b", "port": 47780}]},
			"qwen-27b-pair": {"models": [{"alias": "qwen3.6-27b", "port": 47783}]}
		}
	}`
		writeBackendsForRoot(root, body)

		var got []string

		withStubbedBackendOps(
			func(p backends.Profile, _ backends.LauncherOptions) []backends.LaunchResult {
				for _, m := range p.Models {
					got = append(got, m.Alias)
				}

				return []backends.LaunchResult{{Alias: "qwen3.6-35b", PID: 1001, Port: 47780, Ready: true}}
			}, nil, nil)

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"start", "--project-root", root}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("default-profile start must exit 0, got %d stderr=%s", code, stderr.String()))
		gomega.Expect(got).To(gomega.Equal([]string{"qwen3.6-35b"}), fmt.Sprintf("launched aliases = %v, want default qwen-pair only", got))

	})
	ginkgo.It("run backend stop reports results per model", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeBackendsForRoot(root, sampleBackendsForCLI)
		withStubbedBackendOps(nil,
			func(_ backends.Profile, _ backends.StopOptions) []backends.StopResult {
				return []backends.StopResult{
					{Alias: "qwen3.6-35b", Port: 47780, PID: 1001, Stopped: true},
					{Alias: "qwen3-coder-30b", Port: 47781, PID: 0, Stopped: true},
				}
			}, nil)

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"stop", "--project-root", root}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("clean stop must exit 0, got %d (stderr=%s)", code, stderr.String()))

		out := stdout.String()
		gomega.Expect(out).To(gomega.ContainSubstring("STOPPED qwen3.6-35b"), fmt.Sprintf("output should distinguish stopped vs already-free: %s", out))
		gomega.Expect(out).To(gomega.ContainSubstring("FREE qwen3-coder-30b"), fmt.Sprintf("output should distinguish stopped vs already-free: %s", out))

	})
	ginkgo.It("run backend status formats up and down lines", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeBackendsForRoot(root, sampleBackendsForCLI)
		withStubbedBackendOps(nil, nil,
			func(_ backends.Profile, _ backends.StopOptions) []backends.StatusResult {
				return []backends.StatusResult{
					{Alias: "qwen3.6-35b", Port: 47780, Up: true, PID: 5555},
					{Alias: "qwen3-coder-30b", Port: 47781, Up: false},
					{Alias: "glm-5.2", Kind: "glm", Hosted: true},
				}
			})

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"status", "--project-root", root}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("status must exit 0, got %d (stderr=%s)", code, stderr.String()))

		out := stdout.String()
		gomega.Expect(out).To(gomega.ContainSubstring("UP   qwen3.6-35b"), fmt.Sprintf("UP line missing or malformed: %s", out))
		gomega.Expect(out).To(gomega.ContainSubstring("pid=5555"), fmt.Sprintf("UP line missing or malformed: %s", out))
		gomega.Expect(out).To(gomega.ContainSubstring("DOWN qwen3-coder-30b"), fmt.Sprintf("DOWN line missing: %s", out))
		gomega.Expect(out).To(gomega.ContainSubstring("HOSTED glm-5.2 (kind=glm)"), fmt.Sprintf("HOSTED line missing: %s", out))

	})
	ginkgo.It("run backend proxy serves profile with ready line", func() {

		root := ginkgo.GinkgoT().TempDir()
		body := `{
  "profiles": {
    "qwen-pair": {
      "models": [
        {"alias": "qwen3.6-35b", "binary": "mlx_lm.server", "model_path": "/abs/q35", "port": 47780},
        {"alias": "qwen3-coder-30b", "binary": "mlx_lm.server", "model_path": "/abs/qcoder", "port": 47781}
      ],
      "proxy": {"listen": 47680, "default_alias": "qwen3.6-35b"}
    }
  }
}`
		writeBackendsForRoot(root, body)

		cap := captureProxyServe()

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"proxy", "--project-root", root}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("proxy must exit 0 on clean shutdown, got %d (stderr=%s)", code, stderr.String()))
		gomega.Expect(cap.called).To(gomega.BeTrue(), "ServeProxy stub was not invoked")
		gomega.Expect(cap.cfg.Listen).To(gomega.Equal(47680), fmt.Sprintf("resolved ProxyConfig wrong: %+v", cap.cfg))
		gomega.Expect(cap.cfg.DefaultAlias).To(gomega.Equal("qwen3.6-35b"), fmt.Sprintf("resolved ProxyConfig wrong: %+v", cap.cfg))
		gomega.Expect(cap.cfg.Routes).To(gomega.HaveLen(2), fmt.Sprintf("resolved ProxyConfig wrong: %+v", cap.cfg))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("LISTEN 127.0.0.1:fake"), fmt.Sprintf("stdout missing LISTEN/STOPPED markers: %s", stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("STOPPED proxy"), fmt.Sprintf("stdout missing LISTEN/STOPPED markers: %s", stdout.String()))

	})
	ginkgo.It("run backend proxy listen flag overrides schema", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeBackendsForRoot(root, `{"profiles":{"p":{"models":[{"alias":"a","binary":"b","model_path":"/m","port":1000}],"proxy":{"listen":47680,"default_alias":"a"}}}}`)

		cap := captureProxyServe()

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"proxy", "--project-root", root, "--listen", "55555"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected 0, got %d (stderr=%s)", code, stderr.String()))
		gomega.Expect(cap.cfg.Listen).To(gomega.Equal(55555), fmt.Sprintf("--listen override should win: got %d", cap.cfg.Listen))

	})
	ginkgo.It("run backend proxy default flag overrides schema", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeBackendsForRoot(root, `{"profiles":{"p":{"models":[{"alias":"a","binary":"b","model_path":"/m","port":1},{"alias":"c","binary":"b","model_path":"/n","port":2}],"proxy":{"listen":1234,"default_alias":"a"}}}}`)

		cap := captureProxyServe()

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"proxy", "--project-root", root, "--default", "c"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("expected 0, got %d (stderr=%s)", code, stderr.String()))
		gomega.Expect(cap.cfg.DefaultAlias).To(gomega.Equal("c"), fmt.Sprintf("--default override should win: got %q", cap.cfg.DefaultAlias))

		withStubbedProxyServe(func(context.Context, backends.ProxyConfig, backends.ProxyOptions, func(string)) error {
			ginkgo.Fail("ServeProxy must not be called with a bogus --default")

			return nil
		})
		stdout.Reset()
		stderr.Reset()

		code = RunWithTelemetry([]string{"proxy", "--project-root", root, "--default", "no-such"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(2), fmt.Sprintf("bogus --default must exit 2, got %d", code))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("not a registered alias"), fmt.Sprintf("error must explain mismatch: %s", stderr.String()))

	})
	ginkgo.It("run backend proxy missing listen exits two", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeBackendsForRoot(root, `{"profiles":{"p":{"models":[{"alias":"a","binary":"b","model_path":"/m","port":1}]}}}`)
		withStubbedProxyServe(func(context.Context, backends.ProxyConfig, backends.ProxyOptions, func(string)) error {
			ginkgo.Fail("ServeProxy must not be called when listen is unconfigured")

			return nil
		})

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"proxy", "--project-root", root}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(2), fmt.Sprintf("missing listen must exit 2, got %d (stderr=%s)", code, stderr.String()))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("proxy.listen"), fmt.Sprintf("error must point at proxy.listen: %s", stderr.String()))

	})
	ginkgo.It("run backend proxy missing backends json exits one", func() {
		withStubbedProxyServe(func(context.Context, backends.ProxyConfig, backends.ProxyOptions, func(string)) error {
			ginkgo.Fail("ServeProxy must not be called when backends.json is missing")

			return nil
		})

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"proxy", "--project-root", ginkgo.GinkgoT().TempDir()}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("missing backends.json must exit 1, got %d", code))

	})
	ginkgo.It("run backend proxy build config failure exits one", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeBackendsForRoot(root, `{"profiles":{"p":{"models":[{"alias":"a","binary":"b","port":1}],"proxy":{"listen":1}}}}`)
		withStubbedProxyServe(func(context.Context, backends.ProxyConfig, backends.ProxyOptions, func(string)) error {
			ginkgo.Fail("ServeProxy must not be called when BuildProxyConfig fails")

			return nil
		})

		var stdout, stderr bytes.Buffer

		code := RunWithTelemetry([]string{"proxy", "--project-root", root}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("BuildProxyConfig failure must exit 1, got %d", code))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("model_path"), fmt.Sprintf("error must explain the validation failure: %s", stderr.String()))

	})
})

func withStubbedBackendOps(launch func(backends.Profile, backends.LauncherOptions) []backends.LaunchResult, stop func(backends.Profile, backends.StopOptions) []backends.StopResult, status func(backends.Profile, backends.StopOptions) []backends.StatusResult) {

	origLaunch, origStop, origStatus := backendLaunch, backendStop, backendStatus
	if launch != nil {
		backendLaunch = launch
	}

	if stop != nil {
		backendStop = stop
	}

	if status != nil {
		backendStatus = status
	}
	ginkgo.DeferCleanup(func() {
		backendLaunch, backendStop, backendStatus = origLaunch, origStop, origStatus
	})
}

func withStubbedProxyServe(serve func(context.Context, backends.ProxyConfig, backends.ProxyOptions, func(string)) error) {

	orig := backendServeProxy

	backendServeProxy = serve
	ginkgo.DeferCleanup(func() { backendServeProxy = orig })
}

func writeBackendsForRoot(root, body string) {
	gomega.Expect(os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(body), 0o644)).To(gomega.Succeed())

}

func makeAlreadyRunningErr() error {
	return &alreadyRunningErr{}
}

type alreadyRunningErr struct{}

func (e *alreadyRunningErr) Error() string {
	return "backends: port 47780 already in use; assuming qwen3.6-35b is already running"
}

type proxyCapturedConfig struct {
	cfg    backends.ProxyConfig
	called bool
}

func captureProxyServe() *proxyCapturedConfig {
	cap := &proxyCapturedConfig{}

	withStubbedProxyServe(func(_ context.Context, cfg backends.ProxyConfig, _ backends.ProxyOptions, ready func(string)) error {
		cap.cfg = cfg
		cap.called = true

		if ready != nil {
			ready("127.0.0.1:fake")
		}

		return nil
	})

	return cap
}
