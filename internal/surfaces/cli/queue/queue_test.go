package queue

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/runtime/runner"
)

func startDaemonForCLI(cfg runner.Config) string {

	if cfg.Pools == nil {
		cfg = runner.DefaultConfig()
	}

	dir, err := os.MkdirTemp("/tmp", "idr-cli-")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir tmp: %v", err))
	ginkgo.DeferCleanup(func() { _ = os.RemoveAll(dir) })

	sock := filepath.Join(dir, "s")
	srv := runner.NewServer(cfg, sock)
	gomega.Expect(srv.Start()).To(gomega.Succeed())
	ginkgo.DeferCleanup(srv.Stop)

	return sock
}

var _ = ginkgo.Describe("queue", func() {
	ginkgo.It("queue usage on no subcommand", func() {
		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("usage exit code = %d, want 0", code))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("subcommands:"), fmt.Sprintf("usage missing subcommands: %s", stderr.String()))

	})
	ginkgo.It("queue ping hits live daemon", func() {
		sock := startDaemonForCLI(runner.DefaultConfig())

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"ping", "--socket", sock}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("ping exit=%d stderr=%s", code, stderr.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("ok (uptime"), fmt.Sprintf("expected 'ok (uptime ...)' in stdout, got %q", stdout.String()))

	})
	ginkgo.It("queue ping fails without daemon", func() {
		dir, err := os.MkdirTemp("/tmp", "idr-cli-")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)
		ginkgo.DeferCleanup(func() { _ = os.RemoveAll(dir) })

		missing := filepath.Join(dir, "missing.sock")

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"ping", "--socket", missing}, &stdout, &stderr, nil)
		gomega.Expect(code).NotTo(gomega.Equal(0), fmt.Sprintf("expected nonzero exit, got 0; stderr=%s", stderr.String()))

	})
	ginkgo.It("queue status json shape", func() {
		sock := startDaemonForCLI(runner.Config{
			Pools: map[string]runner.PoolConfig{
				"qwen-35b":   {MaxInFlight: 1},
				"qwen-coder": {MaxInFlight: 2},
			},
		})

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"status", "--socket", sock, "--json"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("status exit=%d stderr=%s", code, stderr.String()))

		out := stdout.String()
		for _, want := range []string{`"pools"`, `"qwen-35b"`, `"qwen-coder"`, `"max_in_flight": 2`} {
			gomega.Expect(out).To(gomega.ContainSubstring(want), fmt.Sprintf("status JSON missing %q: %s", want, out))

		}
	})
	ginkgo.It("queue status table shape", func() {
		sock := startDaemonForCLI(runner.DefaultConfig())

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"status", "--socket", sock}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("status exit=%d stderr=%s", code, stderr.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("POOL"), fmt.Sprintf("table header missing: %s", stdout.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("default"), fmt.Sprintf("default pool row missing: %s", stdout.String()))

	})
	ginkgo.It("queue run requires command", func() {
		sock := startDaemonForCLI(runner.DefaultConfig())

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"run", "--socket", sock}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(2), fmt.Sprintf("expected exit 2 for missing command, got %d", code))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("command required"), fmt.Sprintf("error message wrong: %s", stderr.String()))

	})
	ginkgo.It("queue run executes under slot", func() {
		sock := startDaemonForCLI(runner.DefaultConfig())

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{
			"run", "--socket", sock, "--quiet",
			"--", "/usr/bin/true",
		}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("run /usr/bin/true exit=%d stderr=%s", code, stderr.String()))

	})
	ginkgo.It("queue run propagates exit code", func() {
		sock := startDaemonForCLI(runner.DefaultConfig())

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{
			"run", "--socket", sock, "--quiet",
			"--", "/bin/sh", "-c", "exit 7",
		}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(7), fmt.Sprintf("expected exact child exit code 7, got %d; stderr=%s", code, stderr.String()))

	})
	ginkgo.It("queue run streams child stdin stdout and stderr", func() {
		sock := startDaemonForCLI(runner.DefaultConfig())

		readEnd, writeEnd, err := os.Pipe()
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("pipe: %v", err))

		_, _ = writeEnd.WriteString("payload\n")
		_ = writeEnd.Close()

		origStdin := os.Stdin
		os.Stdin = readEnd
		ginkgo.DeferCleanup(func() {
			os.Stdin = origStdin
			_ = readEnd.Close()
		})

		var stdout, stderr bytes.Buffer
		code := RunMainWithTelemetry([]string{
			"run", "--socket", sock, "--quiet",
			"--", "/bin/sh", "-c", `read value; printf 'out=%s\n' "$value"; printf 'err=%s\n' "$value" >&2`,
		}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("queue run exit=%d stderr=%s", code, stderr.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("out=payload"), fmt.Sprintf("stdout missing child output: %q", stdout.String()))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("err=payload"), fmt.Sprintf("stderr missing child output: %q", stderr.String()))

	})
	ginkgo.It("queue run releases slot after exit", func() {
		sock := startDaemonForCLI(runner.Config{
			Pools: map[string]runner.PoolConfig{"default": {MaxInFlight: 1}},
		})

		for i := range 2 {
			var stdout, stderr bytes.Buffer

			done := make(chan int, 1)

			go func() {
				done <- RunMainWithTelemetry([]string{
					"run", "--socket", sock, "--quiet",
					"--", "/usr/bin/true",
				}, &stdout, &stderr, nil)
			}()

			select {
			case code := <-done:
				if code != 0 {
					ginkgo.Fail(fmt.Sprintf("iter %d exit=%d stderr=%s", i, code, stderr.String()))
				}
			case <-time.After(3 * time.Second):
				ginkgo.Fail(fmt.Sprintf("iter %d timed out — slot likely not released", i))
			}
		}

		snap, err := runner.Status(sock)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("status: %v", err))
		gomega.Expect(len(snap.InFlight)+len(snap.Waiting)).To(gomega.Equal(0), fmt.Sprintf("expected empty queue after both runs, got %+v", snap))

	})
	ginkgo.It("queue cancel on unknown id fails", func() {
		sock := startDaemonForCLI(runner.DefaultConfig())

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"cancel", "--socket", sock, "j-does-not-exist"}, &stdout, &stderr, nil)
		gomega.Expect(code).NotTo(gomega.Equal(0), fmt.Sprintf("expected nonzero exit, got 0"))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("unknown job_id"), fmt.Sprintf("error should mention unknown job_id: %s", stderr.String()))

	})
	ginkgo.It("queue run exports held pools env for children", func() {
		sock := startDaemonForCLI(runner.Config{
			Pools: map[string]runner.PoolConfig{"qwen-coder": {MaxInFlight: 1}},
		})

		outPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "held.txt")
		args := []string{
			"run", "--socket", sock, "--pool", "qwen-coder", "--quiet",
			"--label", "parent",
			"--", "/bin/sh", "-c", `printenv IDIOM_QUEUE_HELD_POOLS > "$0"`, outPath,
		}

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry(args, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("queue run exit=%d stderr=%s", code, stderr.String()))

		body, err := os.ReadFile(outPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read child output: %v", err))

		got := strings.TrimSpace(string(body))
		gomega.Expect(got).To(gomega.Equal("qwen-coder"), fmt.Sprintf("child saw IDIOM_QUEUE_HELD_POOLS=%q, want %q", got, "qwen-coder"))

	})
	ginkgo.It("queue run appends held pool for nested ancestor", func() {

		sock := startDaemonForCLI(runner.Config{
			Pools: map[string]runner.PoolConfig{"qwen-coder": {MaxInFlight: 1}},
		})
		ginkgo.GinkgoT().
			Setenv(runner.HeldPoolsEnv, "qwen-35b")

		outPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "held.txt")
		args := []string{
			"run", "--socket", sock, "--pool", "qwen-coder", "--quiet",
			"--label", "nested",
			"--", "/bin/sh", "-c", `printenv IDIOM_QUEUE_HELD_POOLS > "$0"`, outPath,
		}

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry(args, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("queue run exit=%d stderr=%s", code, stderr.String()))

		body, err := os.ReadFile(outPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read child output: %v", err))

		got := strings.TrimSpace(string(body))
		gomega.Expect(got).To(gomega.Equal("qwen-35b:qwen-coder"), fmt.Sprintf("child saw IDIOM_QUEUE_HELD_POOLS=%q, want qwen-35b:qwen-coder", got))

	})
	ginkgo.It("queue run skips duplicate held pool", func() {

		sock := startDaemonForCLI(runner.Config{
			Pools: map[string]runner.PoolConfig{"qwen-coder": {MaxInFlight: 1}},
		})
		ginkgo.GinkgoT().
			Setenv(runner.HeldPoolsEnv, "qwen-coder")

		outPath := filepath.Join(ginkgo.GinkgoT().TempDir(), "held.txt")
		args := []string{
			"run", "--socket", sock, "--pool", "qwen-coder", "--quiet",
			"--label", "dup",
			"--", "/bin/sh", "-c", `printenv IDIOM_QUEUE_HELD_POOLS > "$0"`, outPath,
		}

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry(args, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("queue run exit=%d stderr=%s", code, stderr.String()))

		body, err := os.ReadFile(outPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read child output: %v", err))

		got := strings.TrimSpace(string(body))
		gomega.Expect(got).To(gomega.Equal("qwen-coder"), fmt.Sprintf("child saw IDIOM_QUEUE_HELD_POOLS=%q, want qwen-coder (no dup)", got))

	})
})
