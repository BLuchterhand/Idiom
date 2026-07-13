package root

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	cmdtelemetry "idiom/internal/surfaces/cli/telemetry"
)

const (
	signalExitHelperEnv       = "IDIOM_SIGNAL_EXIT_HELPER"
	signalExitHelperReadyPath = "IDIOM_SIGNAL_EXIT_READY_PATH"
)

// runSignalExitHelperProcess is launched through the package's single Ginkgo
// suite entrypoint. Its dispatch intentionally ignores cancellation forever,
// proving the root signal arbiter can return the shell signal code without
// cooperation from a command implementation.
func runSignalExitHelperProcess(t *testing.T) {
	recorder := cmdtelemetry.OpenRecorder("signal-helper", nil, cmdtelemetry.RecorderOptions{
		Dir: os.Getenv("IDIOM_TELEMETRY_DIR"),
	})
	if recorder == nil {
		t.Fatal("open signal helper telemetry recorder")
	}

	code := runWithTelemetrySignalArbitration(recorder, func() int {
		readyPath := os.Getenv(signalExitHelperReadyPath)
		if err := os.WriteFile(readyPath, []byte("ready\n"), 0o600); err != nil {
			return 91
		}

		select {}
	})
	os.Exit(code)
}

var _ = ginkgo.Describe("Process signal arbitration", ginkgo.Label("e2e", "smoke", "race-contract"), func() {
	ginkgo.It("terminates a non-cooperative dispatch with one signal exit record", ginkgo.SpecTimeout(15*time.Second), func(ctx ginkgo.SpecContext) {
		if runtime.GOOS == "windows" {
			ginkgo.Skip("process signals are not portable to Windows")
		}

		t := ginkgo.GinkgoT()
		executable, err := os.Executable()
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		telemetryDir := t.TempDir()
		readyPath := filepath.Join(t.TempDir(), "ready")
		command := exec.Command(executable, "-test.run=^TestRoot$")
		command.Env = rootTestEnvironmentWithOverrides(os.Environ(), map[string]string{
			"IDIOM_TELEMETRY":         "1",
			"IDIOM_TELEMETRY_DIR":     telemetryDir,
			signalExitHelperEnv:       "1",
			signalExitHelperReadyPath: readyPath,
		})

		var stdout bytes.Buffer
		var stderr bytes.Buffer
		command.Stdout = &stdout
		command.Stderr = &stderr
		gomega.Expect(command.Start()).To(gomega.Succeed())

		waitResult := make(chan error, 1)
		waitDone := make(chan struct{})
		go func() {
			waitResult <- command.Wait()
			close(waitDone)
		}()
		ginkgo.DeferCleanup(func() {
			select {
			case <-waitDone:
				return
			default:
			}
			_ = command.Process.Kill()
			<-waitDone
		})

		gomega.Eventually(ctx, func() bool {
			_, statErr := os.Stat(readyPath)

			return statErr == nil
		}).Should(gomega.BeTrue())
		gomega.Expect(command.Process.Signal(syscall.SIGTERM)).To(gomega.Succeed())

		var waitErr error
		gomega.Eventually(ctx, waitResult).Should(gomega.Receive(&waitErr))
		var exitErr *exec.ExitError
		gomega.Expect(errors.As(waitErr, &exitErr)).To(gomega.BeTrue(), "stdout=%s\nstderr=%s\nerror=%v", stdout.String(), stderr.String(), waitErr)
		gomega.Expect(exitErr.ExitCode()).To(gomega.Equal(143), "stdout=%s\nstderr=%s", stdout.String(), stderr.String())

		entries, readErr := os.ReadDir(telemetryDir)
		gomega.Expect(readErr).NotTo(gomega.HaveOccurred())
		gomega.Expect(entries).To(gomega.HaveLen(1))
		records := readMainTelemetryRecords(t, filepath.Join(telemetryDir, entries[0].Name()))
		exits := rootProcessExitRecords(records)
		gomega.Expect(exits).To(gomega.HaveLen(1), "records=%#v", records)
		gomega.Expect(exits[0]).To(gomega.And(
			gomega.HaveKeyWithValue("exit_code", float64(143)),
			gomega.HaveKeyWithValue("reason", "signal"),
			gomega.HaveKeyWithValue("signal", syscall.SIGTERM.String()),
		))
	})

	ginkgo.It("preserves panic closeout when dispatch panics", func() {
		recorder := cmdtelemetry.OpenRecorder("panic-helper", nil, cmdtelemetry.RecorderOptions{Dir: ginkgo.GinkgoT().TempDir()})
		gomega.Expect(recorder).NotTo(gomega.BeNil())

		var recovered any
		func() {
			defer func() { recovered = recover() }()
			_ = runWithTelemetrySignalArbitration(recorder, func() int {
				panic("dispatch panic")
			})
		}()
		gomega.Expect(recovered).To(gomega.Equal("dispatch panic"))

		exits := rootProcessExitRecords(readMainTelemetryRecords(ginkgo.GinkgoT(), recorder.Path()))
		gomega.Expect(exits).To(gomega.HaveLen(1))
		gomega.Expect(exits[0]).To(gomega.And(
			gomega.HaveKeyWithValue("exit_code", float64(2)),
			gomega.HaveKeyWithValue("reason", "panic"),
			gomega.HaveKeyWithValue("panic", "dispatch panic"),
		))
	})
})

func rootProcessExitRecords(records []map[string]any) []map[string]any {
	exits := make([]map[string]any, 0, 1)
	for _, record := range records {
		if record["type"] == "process_exit" {
			exits = append(exits, record)
		}
	}

	return exits
}

func rootTestEnvironmentWithOverrides(base []string, overrides map[string]string) []string {
	environment := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[key]; replaced {
			continue
		}
		environment = append(environment, entry)
	}
	for key, value := range overrides {
		environment = append(environment, key+"="+value)
	}

	return environment
}
