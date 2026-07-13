package backends

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Backend launcher", func() {
	ginkgo.Describe("launch specifications", func() {
		ginkgo.It("builds the canonical MLX arguments", func() {
			model := Model{
				Alias:      "qwen3-coder-30b",
				Binary:     "mlx_lm.server",
				ModelPath:  "/abs/models/qwen3-coder-30b",
				Port:       47781,
				CacheSize:  4,
				CacheBytes: 21474836480,
				MaxTokens:  8192,
				ExtraArgs:  []string{"--temp", "0.7"},
			}

			spec, err := BuildLaunchSpec(model)

			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(spec.Argv).To(gomega.Equal([]string{
				"mlx_lm.server",
				"--model", "/abs/models/qwen3-coder-30b",
				"--host", "127.0.0.1",
				"--port", "47781",
				"--prompt-cache-size", "4",
				"--prompt-cache-bytes", "21474836480",
				"--max-tokens", "8192",
				"--temp", "0.7",
			}))
		})

		ginkgo.It("adds a request timeout for a vmlx-compatible launcher", func() {
			spec, err := BuildLaunchSpec(Model{
				Alias:                 "qwen3.6-35b",
				Binary:                "/usr/local/bin/vmlx-idiom-launch.sh",
				ModelPath:             "/abs/models/qwen3.6-35b",
				Port:                  47780,
				MaxTokens:             8192,
				RequestTimeoutSeconds: 900,
			})

			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(valueAfter(spec.Argv, "--timeout")).To(gomega.Equal("900"))
		})

		ginkgo.It("uses vmlx serve and its native cache flags", func() {
			spec, err := BuildLaunchSpec(Model{
				Alias:                 "minimax-m2.7-jang",
				Binary:                "/usr/local/bin/vmlx",
				ModelPath:             "/abs/models/minimax-m2.7-jang",
				Port:                  47784,
				CacheSize:             4,
				CacheBytes:            12884901888,
				MaxTokens:             8192,
				RequestTimeoutSeconds: 900,
				ExtraArgs:             []string{"--served-model-name", "minimax-m2.7-jang"},
			})

			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(spec.Argv).To(gomega.Equal([]string{
				"/usr/local/bin/vmlx",
				"serve", "/abs/models/minimax-m2.7-jang",
				"--host", "127.0.0.1",
				"--port", "47784",
				"--prefix-cache-size", "4",
				"--prefix-cache-max-bytes", "12884901888",
				"--max-tokens", "8192",
				"--timeout", "900",
				"--served-model-name", "minimax-m2.7-jang",
			}))
		})

		ginkgo.It("does not pass an unsupported timeout to mlx_lm.server", func() {
			spec, err := BuildLaunchSpec(Model{
				Alias:                 "qwen3.6-35b",
				Binary:                "mlx_lm.server",
				ModelPath:             "/abs/models/qwen3.6-35b",
				Port:                  47780,
				RequestTimeoutSeconds: 900,
			})

			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(spec.Argv).NotTo(gomega.ContainElement("--timeout"))
		})

		ginkgo.It("omits flags for zero-valued optional fields", func() {
			spec, err := BuildLaunchSpec(Model{Alias: "minimal", Binary: "some-server", ModelPath: "/m", Port: 8080})

			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			for _, flag := range []string{"--prompt-cache-size", "--prompt-cache-bytes", "--max-tokens", "--timeout"} {
				gomega.Expect(spec.Argv).NotTo(gomega.ContainElement(flag))
			}
		})

		ginkgo.It("expands a home-relative model path", func() {
			spec, err := BuildLaunchSpec(Model{Alias: "tilde", Binary: "x", ModelPath: "~/models/foo", Port: 1})

			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			modelPath := valueAfter(spec.Argv, "--model")
			gomega.Expect(modelPath).NotTo(gomega.HavePrefix("~"))
			gomega.Expect(modelPath).To(gomega.HaveSuffix("models/foo"))
		})

		ginkgo.It("rejects an empty binary", func() {
			_, err := BuildLaunchSpec(Model{Alias: "bad", Port: 1234})
			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("empty binary")))
		})

		ginkgo.It("rejects a non-positive port", func() {
			_, err := BuildLaunchSpec(Model{Alias: "bad", Binary: "x", Port: 0})
			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("invalid port")))
		})

		ginkgo.It("honors a host override", func() {
			spec, err := BuildLaunchSpec(Model{
				Alias: "x", Binary: "b", ModelPath: "/m", Host: "10.0.0.4", Port: 9000,
			})

			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(valueAfter(spec.Argv, "--host")).To(gomega.Equal("10.0.0.4"))
		})
	})

	ginkgo.Describe("lsof PID lookup", func() {
		ginkgo.It("parses the first reported PID", func() {
			writeFakeLsof("printf '4242\\n9999\\n'\n")

			pid, err := pidForPortViaLsof(47781)

			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(pid).To(gomega.Equal(4242))
		})

		ginkgo.It("treats exit code one as no listener", func() {
			writeFakeLsof("exit 1\n")

			pid, err := pidForPortViaLsof(47781)

			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(pid).To(gomega.BeZero())
		})

		ginkgo.It("reports an unavailable lsof binary", func() {
			ginkgo.GinkgoT().Setenv("PATH", ginkgo.GinkgoT().TempDir())

			pid, err := pidForPortViaLsof(47781)

			gomega.Expect(err).To(gomega.MatchError(errLsofUnavailable))
			gomega.Expect(pid).To(gomega.BeZero())
		})

		ginkgo.It("surfaces malformed lsof output", func() {
			writeFakeLsof("printf 'not-a-pid\\n'\n")

			_, err := pidForPortViaLsof(47781)

			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("parse lsof output")))
		})
	})

	ginkgo.Describe("launching profiles", func() {
		ginkgo.It("spawns every model in declaration order", func() {
			profile := Profile{Models: []Model{
				{Alias: "a", Binary: "x", Port: 1001},
				{Alias: "b", Binary: "y", Port: 1002},
			}}
			var probes int32
			var spawns int32

			results := Launch(profile, LauncherOptions{
				LogDir:       ginkgo.GinkgoT().TempDir(),
				ReadyTimeout: 100 * time.Millisecond,
				Spawner: func(spec LaunchSpec, _ string) (int, error) {
					atomic.AddInt32(&spawns, 1)
					return 1000 + spec.Port, nil
				},
				PortReady: func(int, time.Time) bool {
					return atomic.AddInt32(&probes, 1)%2 == 0
				},
			})

			gomega.Expect(results).To(gomega.HaveLen(2))
			gomega.Expect(atomic.LoadInt32(&spawns)).To(gomega.Equal(int32(2)))
			gomega.Expect(results[0].Alias).To(gomega.Equal("a"))
			gomega.Expect(results[1].Alias).To(gomega.Equal("b"))
			for _, result := range results {
				gomega.Expect(result.Ready).To(gomega.BeTrue())
				gomega.Expect(result.PID).NotTo(gomega.BeZero())
			}
		})

		ginkgo.It("does not spawn when the port is already active", func() {
			spawnCalled := false
			results := Launch(Profile{Models: []Model{{Alias: "a", Binary: "x", Port: 1234}}}, LauncherOptions{
				LogDir:       ginkgo.GinkgoT().TempDir(),
				ReadyTimeout: 100 * time.Millisecond,
				Spawner: func(LaunchSpec, string) (int, error) {
					spawnCalled = true
					return 1, nil
				},
				PortReady: func(int, time.Time) bool { return true },
			})

			gomega.Expect(spawnCalled).To(gomega.BeFalse())
			gomega.Expect(results).To(gomega.HaveLen(1))
			gomega.Expect(results[0].Ready).To(gomega.BeTrue())
			gomega.Expect(results[0].Err).To(gomega.MatchError(gomega.ContainSubstring("already")))
		})

		ginkgo.It("continues after an individual spawn failure", func() {
			results := Launch(Profile{Models: []Model{
				{Alias: "a", Binary: "x", Port: 1001},
				{Alias: "b", Binary: "y", Port: 1002},
			}}, LauncherOptions{
				LogDir: ginkgo.GinkgoT().TempDir(),
				Spawner: func(spec LaunchSpec, _ string) (int, error) {
					if spec.Alias == "a" {
						return 0, errors.New("synthetic")
					}
					return 42, nil
				},
				PortReady:    func(int, time.Time) bool { return false },
				ReadyTimeout: 50 * time.Millisecond,
			})

			gomega.Expect(results).To(gomega.HaveLen(2))
			gomega.Expect(results[0].Err).To(gomega.MatchError("synthetic"))
			gomega.Expect(results[1].Err).NotTo(gomega.HaveOccurred())
		})
	})

	ginkgo.Describe("stopping and status", func() {
		ginkgo.It("sends SIGTERM then SIGKILL to a stuck process", func() {
			var signals []Signal
			results := Stop(Profile{Models: []Model{{Alias: "stuck", Binary: "x", Port: 9999}}}, StopOptions{
				PIDForPort:   func(int) (int, error) { return 4242, nil },
				IsAlive:      func(int) bool { return true },
				Kill:         func(_ int, signal Signal) error { signals = append(signals, signal); return nil },
				GraceTimeout: 50 * time.Millisecond,
			})

			gomega.Expect(results).To(gomega.HaveLen(1))
			gomega.Expect(results[0].Stopped).To(gomega.BeTrue())
			gomega.Expect(signals).To(gomega.Equal([]Signal{SignalTerm, SignalKill}))
		})

		ginkgo.It("does nothing when the port is already free", func() {
			killCalled := false
			results := Stop(Profile{Models: []Model{{Alias: "nothing", Binary: "x", Port: 1}}}, StopOptions{
				PIDForPort: func(int) (int, error) { return 0, nil },
				Kill:       func(int, Signal) error { killCalled = true; return nil },
			})

			gomega.Expect(killCalled).To(gomega.BeFalse())
			gomega.Expect(results[0].Stopped).To(gomega.BeTrue())
		})

		ginkgo.It("does not escalate after a clean SIGTERM exit", func() {
			var signals []Signal
			results := Stop(Profile{Models: []Model{{Alias: "clean", Binary: "x", Port: 8080}}}, StopOptions{
				PIDForPort: func(int) (int, error) { return 1234, nil },
				Kill: func(_ int, signal Signal) error {
					signals = append(signals, signal)
					return nil
				},
				IsAlive:      func(int) bool { return false },
				GraceTimeout: 200 * time.Millisecond,
			})

			gomega.Expect(results[0].Stopped).To(gomega.BeTrue())
			gomega.Expect(signals).To(gomega.Equal([]Signal{SignalTerm}))
		})

		ginkgo.It("reports local models according to PID lookup", func() {
			results := Status(Profile{Models: []Model{
				{Alias: "running", Port: 100},
				{Alias: "down", Port: 200},
			}}, StopOptions{PIDForPort: func(port int) (int, error) {
				if port == 100 {
					return 7777, nil
				}
				return 0, nil
			}})

			gomega.Expect(results).To(gomega.HaveLen(2))
			gomega.Expect(results[0].Up).To(gomega.BeTrue())
			gomega.Expect(results[0].PID).To(gomega.Equal(7777))
			gomega.Expect(results[1].Up).To(gomega.BeFalse())
		})

		ginkgo.It("reports hosted models without local PID lookup", func() {
			lookupCalled := false
			results := Status(Profile{Models: []Model{{Alias: "glm-5.2", Kind: "glm"}}}, StopOptions{
				PIDForPort: func(int) (int, error) {
					lookupCalled = true
					return 0, nil
				},
			})

			gomega.Expect(lookupCalled).To(gomega.BeFalse())
			gomega.Expect(results).To(gomega.ConsistOf(StatusResult{
				Alias: "glm-5.2", Kind: "glm", Hosted: true,
			}))
		})

		ginkgo.It("re-verifies liveness before SIGKILL", func() {
			var aliveCalls int32
			var signals []Signal
			results := Stop(Profile{Models: []Model{{Alias: "racy", Binary: "x", Port: 9000}}}, StopOptions{
				PIDForPort: func(int) (int, error) { return 1234, nil },
				IsAlive: func(int) bool {
					return atomic.AddInt32(&aliveCalls, 1) < 2
				},
				Kill:         func(_ int, signal Signal) error { signals = append(signals, signal); return nil },
				GraceTimeout: 50 * time.Millisecond,
			})

			gomega.Expect(results[0].Stopped).To(gomega.BeTrue())
			gomega.Expect(signals).To(gomega.Equal([]Signal{SignalTerm}))
		})

		ginkgo.It("prefers an active PID file over port lookup", func() {
			runDir := ginkgo.GinkgoT().TempDir()
			gomega.Expect(writePIDFile(runDir, "racy", 4242)).To(gomega.Succeed())
			var killedPID int

			_ = Stop(Profile{Models: []Model{{Alias: "racy", Binary: "x", Port: 9000}}}, StopOptions{
				RunDir:  runDir,
				IsAlive: func(pid int) bool { return pid == 4242 },
				PIDForPort: func(int) (int, error) {
					ginkgo.Fail("PIDForPort must not be consulted for an active PID file")
					return 0, nil
				},
				Kill: func(pid int, _ Signal) error {
					killedPID = pid
					return nil
				},
				GraceTimeout: 50 * time.Millisecond,
			})

			gomega.Expect(killedPID).To(gomega.Equal(4242))
			_, err := os.Stat(pidFilePath(runDir, "racy"))
			gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue())
		})

		ginkgo.It("falls back to port lookup when the PID file is missing", func() {
			var killedPID int

			_ = Stop(Profile{Models: []Model{{Alias: "legacy", Binary: "x", Port: 9000}}}, StopOptions{
				RunDir:     ginkgo.GinkgoT().TempDir(),
				IsAlive:    func(int) bool { return false },
				PIDForPort: func(int) (int, error) { return 7777, nil },
				Kill: func(pid int, signal Signal) error {
					if signal == SignalTerm {
						killedPID = pid
					}
					return nil
				},
				GraceTimeout: 50 * time.Millisecond,
			})

			gomega.Expect(killedPID).To(gomega.Equal(7777))
		})

		ginkgo.It("removes a stale PID file", func() {
			runDir := ginkgo.GinkgoT().TempDir()
			gomega.Expect(writePIDFile(runDir, "ghost", 99999)).To(gomega.Succeed())

			pid, err := readPIDFile(runDir, "ghost", func(int) bool { return false })

			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(pid).To(gomega.BeZero())
			_, statErr := os.Stat(pidFilePath(runDir, "ghost"))
			gomega.Expect(os.IsNotExist(statErr)).To(gomega.BeTrue())
		})

		ginkgo.It("disables PID file reads for an empty run directory", func() {
			pid, err := readPIDFile("", "anything", nil)

			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(pid).To(gomega.BeZero())
		})
	})

	ginkgo.DescribeTable("sanitizes aliases for filenames",
		func(alias, expected string) {
			gomega.Expect(sanitizeAlias(alias)).To(gomega.Equal(expected))
		},
		ginkgo.Entry("preserves safe punctuation", "qwen3-coder-30b", "qwen3-coder-30b"),
		ginkgo.Entry("normalizes spaces and case", "My Cool Model", "my_cool_model"),
		ginkgo.Entry("normalizes path separators", "a/b/c", "a_b_c"),
		ginkgo.Entry("preserves periods", "v3.6", "v3.6"),
		ginkgo.Entry("supplies a fallback", "", "backend"),
	)
})

func writeFakeLsof(body string) {
	ginkgo.GinkgoHelper()

	if runtime.GOOS == "windows" {
		ginkgo.Skip("fake lsof fixture uses a POSIX shell script")
	}

	binDir := ginkgo.GinkgoT().TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(binDir, "lsof"), []byte("#!/bin/sh\n"+body), 0o755)).To(gomega.Succeed())
	ginkgo.GinkgoT().Setenv("PATH", binDir)
}

func valueAfter(values []string, key string) string {
	ginkgo.GinkgoHelper()

	for index := range values {
		if values[index] == key && index+1 < len(values) {
			return values[index+1]
		}
	}

	ginkgo.Fail("missing value after " + key)
	return ""
}
