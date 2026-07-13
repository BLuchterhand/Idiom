package probe

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("idiom probe", func() {
	var stdout, stderr bytes.Buffer

	ginkgo.BeforeEach(func() {
		stdout.Reset()
		stderr.Reset()
	})

	ginkgo.It("passes the implement and plan-execute families by default", func() {
		gomega.Expect(RunWithTelemetry([]string{"lane"}, &stdout, &stderr, nil)).To(gomega.Equal(0), stderr.String())
		gomega.Expect(stdout.String()).To(gomega.SatisfyAll(
			gomega.ContainSubstring("PASS implement"),
			gomega.ContainSubstring("PASS plan-execute"),
		))
	})

	ginkgo.It("runs only the explicitly selected family", func() {
		gomega.Expect(RunWithTelemetry([]string{"lane", "--mode", "implement"}, &stdout, &stderr, nil)).To(gomega.Equal(0))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("PASS implement"))
		gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring("plan-execute"))
	})

	ginkgo.It("rejects an unknown lane mode", func() {
		gomega.Expect(RunWithTelemetry([]string{"lane", "--mode", "nonsense"}, &stdout, &stderr, nil)).To(gomega.Equal(2))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("not recognized"))
	})

	ginkgo.It("falls back to the compiled tool surface when an explicit config is missing", func() {
		gomega.Expect(RunWithTelemetry(
			[]string{"lane", "--config", "/definitely/no/config/here.json"},
			&stdout,
			&stderr,
			nil,
		)).To(gomega.Equal(0), stderr.String())
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("tool surface from --config="))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("PASS implement"))
	})

	ginkgo.It("resolves the project configuration beneath the project root", func() {
		root := ginkgo.GinkgoT().TempDir()
		cfgDir := filepath.Join(root, ".idiom")
		gomega.Expect(os.MkdirAll(cfgDir, 0o755)).To(gomega.Succeed())
		body := `{
  "app_name": "probe-test",
  "llama_server": {"kind": "mlx", "host": "127.0.0.1", "port": 1, "model": "x"},
  "workspace": {"root": "` + root + `", "cwd": "` + root + `", "shell": "/bin/sh", "command_timeout_seconds": 60, "max_output_bytes": 1024, "enable_writes": false}
}`
		gomega.Expect(os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(body), 0o644)).To(gomega.Succeed())

		gomega.Expect(RunWithTelemetry(
			[]string{"lane", "--project-root", root},
			&stdout,
			&stderr,
			nil,
		)).To(gomega.Equal(0), stderr.String())
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("tool surface from project-root"))
		gomega.Expect(stderr.String()).NotTo(gomega.ContainSubstring("falling back to synthetic"))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("PASS implement"))
	})

	ginkgo.It("gives an explicit config precedence over the project root", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(
			filepath.Join(root, ".idiom", "config.json"),
			[]byte(`{"workspace": {"root": "/x"}}`),
			0o644,
		)).To(gomega.Succeed())

		gomega.Expect(RunWithTelemetry(
			[]string{"lane", "--project-root", root, "--config", "/no/such/explicit.json"},
			&stdout,
			&stderr,
			nil,
		)).To(gomega.Equal(0))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("tool surface from --config="))
	})

	ginkgo.It("does not claim cross-mode coverage", func() {
		gomega.Expect(RunWithTelemetry([]string{"lane"}, &stdout, &stderr, nil)).To(gomega.Equal(0), stderr.String())
		gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring("(build)"))
		gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring("(plan)"))
	})

	ginkgo.It("returns success for lane help", func() {
		gomega.Expect(RunWithTelemetry([]string{"lane", "--help"}, &stdout, &stderr, nil)).To(gomega.Equal(0))
	})

	ginkgo.It("rejects an unknown subcommand as a usage error", func() {
		gomega.Expect(RunWithTelemetry(
			[]string{"definitely-not-a-subcommand"},
			&stdout,
			&stderr,
			nil,
		)).To(gomega.Equal(2))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("unknown subcommand"))
	})
})
