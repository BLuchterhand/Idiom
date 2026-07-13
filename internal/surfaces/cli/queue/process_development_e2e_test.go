//go:build darwin || linux

package queue

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

	"idiom/internal/runtime/runner"
)

const compiledQueueSpecTimeout = 30 * time.Second

type compiledQueueProcess struct {
	lifecycleMu        sync.Mutex
	command            *exec.Cmd
	processGroupID     int
	stdout             compiledQueueOutput
	stderr             compiledQueueOutput
	exitObserved       chan struct{}
	exitObservationErr error
	ownedDescendants   []compiledQueueProcessIdentity
	reaped             bool
	waitErr            error
}

type compiledQueueProcessIdentity struct {
	PID        int
	ParentPID  int
	GroupID    int
	StartToken string
}

type compiledQueueOutput struct {
	mu          sync.Mutex
	buffer      bytes.Buffer
	readyMarker string
	ready       chan struct{}
	readyOnce   sync.Once
}

type compiledQueueResult struct {
	code   int
	stdout string
	stderr string
}

var _ = ginkgo.Describe("Compiled queue development workflows", ginkgo.Ordered, ginkgo.Label("development-scenario", "e2e", "integration", "smoke"), func() {
	var bin string
	var repoRoot string

	ginkgo.BeforeAll(func(ctx ginkgo.SpecContext) {
		repoRoot = queueE2ERepoRoot()
		bin = filepath.Join(ginkgo.GinkgoT().TempDir(), "idiom")
		buildArgs := []string{"build"}
		if compiledQueueRaceBuild {
			buildArgs = append(buildArgs, "-race")
		}
		buildArgs = append(buildArgs, "-o", bin, "./cmd/idiom")
		build := exec.CommandContext(ctx, "go", buildArgs...)
		build.Dir = repoRoot
		output, err := build.CombinedOutput()
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "build idiom:\n%s", string(output))

		buildInfo, err := buildinfo.ReadFile(bin)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(compiledQueueBinaryUsesRace(buildInfo)).To(gomega.Equal(compiledQueueRaceBuild), "compiled queue child race instrumentation must match the Ginkgo test binary")
	})

	ginkgo.It("serializes contending development commands and promotes the waiter after release", ginkgo.SpecTimeout(compiledQueueSpecTimeout), func(ctx ginkgo.SpecContext) {
		t := ginkgo.GinkgoT()
		daemon, socketPath := startCompiledQueueDaemon(ctx, t, bin, repoRoot, runner.DefaultConfig())

		controlDir := t.TempDir()
		holderPIDPath := filepath.Join(controlDir, "holder-pid")
		holderStarted := filepath.Join(controlDir, "holder-started")
		holderRelease := filepath.Join(controlDir, "holder-release")
		waiterRan := filepath.Join(controlDir, "waiter-ran")
		gomega.Expect(syscall.Mkfifo(holderRelease, 0o600)).To(gomega.Succeed())

		holder := startCompiledQueueProcess(ctx, t, bin, repoRoot, "",
			"queue", "run", "--socket", socketPath, "--pool", "default", "--label", "compile-holder", "--quiet", "--",
			"/bin/sh", "-c", `printf '%s' "$$" > "$1"; printf started > "$2"; IFS= read -r _ < "$3"`, "holder", holderPIDPath, holderStarted, holderRelease,
		)
		gomega.Eventually(ctx, func() string {
			body, _ := os.ReadFile(holderStarted)
			return string(body)
		}).Should(gomega.Equal("started"))
		holderIdentity := captureCompiledQueueProcessIdentity(holder, holderPIDPath)

		waiter := startCompiledQueueProcess(ctx, t, bin, repoRoot, "",
			"queue", "run", "--socket", socketPath, "--pool", "default", "--label", "test-waiter", "--quiet", "--",
			"/bin/sh", "-c", `printf ran > "$1"`, "waiter", waiterRan,
		)

		var snapshot runner.StatusPayload
		gomega.Eventually(ctx, func() int {
			snapshot = compiledQueueStatus(ctx, t, bin, repoRoot, socketPath)
			return len(snapshot.Waiting)
		}).Should(gomega.Equal(1))
		gomega.Expect(snapshot.InFlight).To(gomega.HaveLen(1))
		gomega.Expect(snapshot.InFlight[0].Label).To(gomega.Equal("compile-holder"))
		gomega.Expect(snapshot.Waiting[0].Label).To(gomega.Equal("test-waiter"))
		_, err := os.Stat(waiterRan)
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "waiting child must not execute before admission")

		releaseQueueHolder(ctx, t, holderRelease)
		expectCompiledQueueExit(ctx, holder, 0)
		expectCompiledQueueProcessIdentityGone(ctx, holderIdentity)
		removeCompiledQueuePIDRecords(holderPIDPath)
		expectCompiledQueueExit(ctx, waiter, 0)
		body, err := os.ReadFile(waiterRan)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(body)).To(gomega.Equal("ran"))

		snapshot = compiledQueueStatus(ctx, t, bin, repoRoot, socketPath)
		gomega.Expect(snapshot.InFlight).To(gomega.BeEmpty())
		gomega.Expect(snapshot.Waiting).To(gomega.BeEmpty())
		stopCompiledQueueDaemon(ctx, daemon)
	})

	ginkgo.It("cancels a waiting development command without executing it and keeps the queue usable", ginkgo.SpecTimeout(compiledQueueSpecTimeout), func(ctx ginkgo.SpecContext) {
		t := ginkgo.GinkgoT()
		daemon, socketPath := startCompiledQueueDaemon(ctx, t, bin, repoRoot, runner.DefaultConfig())

		controlDir := t.TempDir()
		holderPIDPath := filepath.Join(controlDir, "holder-pid")
		holderStarted := filepath.Join(controlDir, "holder-started")
		holderRelease := filepath.Join(controlDir, "holder-release")
		canceledRan := filepath.Join(controlDir, "canceled-ran")
		recoveryRan := filepath.Join(controlDir, "recovery-ran")
		gomega.Expect(syscall.Mkfifo(holderRelease, 0o600)).To(gomega.Succeed())

		holder := startCompiledQueueProcess(ctx, t, bin, repoRoot, "",
			"queue", "run", "--socket", socketPath, "--pool", "default", "--label", "migration-holder", "--quiet", "--",
			"/bin/sh", "-c", `printf '%s' "$$" > "$1"; printf started > "$2"; IFS= read -r _ < "$3"`, "holder", holderPIDPath, holderStarted, holderRelease,
		)
		gomega.Eventually(ctx, func() string {
			body, _ := os.ReadFile(holderStarted)
			return string(body)
		}).Should(gomega.Equal("started"))
		holderIdentity := captureCompiledQueueProcessIdentity(holder, holderPIDPath)

		canceled := startCompiledQueueProcess(ctx, t, bin, repoRoot, "",
			"queue", "run", "--socket", socketPath, "--pool", "default", "--label", "migration-waiter", "--quiet", "--",
			"/bin/sh", "-c", `printf should-not-run > "$1"`, "waiter", canceledRan,
		)

		var snapshot runner.StatusPayload
		gomega.Eventually(ctx, func() int {
			snapshot = compiledQueueStatus(ctx, t, bin, repoRoot, socketPath)
			return len(snapshot.Waiting)
		}).Should(gomega.Equal(1))
		waitingID := snapshot.Waiting[0].ID
		cancelResult := runCompiledQueueCommand(ctx, t, bin, repoRoot, "queue", "cancel", "--socket", socketPath, waitingID)
		gomega.Expect(cancelResult.code).To(gomega.BeZero(), "stdout=%s\nstderr=%s", cancelResult.stdout, cancelResult.stderr)
		gomega.Expect(cancelResult.stdout).To(gomega.ContainSubstring("cancelled " + waitingID))
		gomega.Expect(cancelResult.stderr).To(gomega.BeEmpty())

		expectCompiledQueueExit(ctx, canceled, 1)
		gomega.Expect(canceled.stderr.String()).To(gomega.And(
			gomega.ContainSubstring("lease error before grant"),
			gomega.ContainSubstring("canceled while waiting"),
		))
		_, err := os.Stat(canceledRan)
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "canceled child must never execute")

		snapshot = compiledQueueStatus(ctx, t, bin, repoRoot, socketPath)
		gomega.Expect(snapshot.InFlight).To(gomega.HaveLen(1))
		gomega.Expect(snapshot.Waiting).To(gomega.BeEmpty())
		releaseQueueHolder(ctx, t, holderRelease)
		expectCompiledQueueExit(ctx, holder, 0)
		expectCompiledQueueProcessIdentityGone(ctx, holderIdentity)
		removeCompiledQueuePIDRecords(holderPIDPath)

		recovery := runCompiledQueueCommand(ctx, t, bin, repoRoot,
			"queue", "run", "--socket", socketPath, "--pool", "default", "--label", "post-cancel-recovery", "--quiet", "--",
			"/bin/sh", "-c", `printf recovered > "$1"`, "recovery", recoveryRan,
		)
		gomega.Expect(recovery.code).To(gomega.BeZero(), "stdout=%s\nstderr=%s", recovery.stdout, recovery.stderr)
		body, err := os.ReadFile(recoveryRan)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(body)).To(gomega.Equal("recovered"))

		stopCompiledQueueDaemon(ctx, daemon)
	})

	ginkgo.It("preserves pool isolation and FIFO promotion through cancellation and active process termination", ginkgo.SpecTimeout(compiledQueueSpecTimeout), func(ctx ginkgo.SpecContext) {
		t := ginkgo.GinkgoT()
		daemon, socketPath := startCompiledQueueDaemon(ctx, t, bin, repoRoot, runner.Config{Pools: map[string]runner.PoolConfig{
			"executor": {MaxInFlight: 1},
			"planner":  {MaxInFlight: 1},
		}})

		controlDir := t.TempDir()
		holderPIDPath := filepath.Join(controlDir, "holder-pid")
		holderStarted := filepath.Join(controlDir, "holder-started")
		holderRelease := filepath.Join(controlDir, "holder-release")
		nestedRan := filepath.Join(controlDir, "same-pool-nested-ran")
		executorRan := filepath.Join(controlDir, "executor-ran")
		firstStarted := filepath.Join(controlDir, "first-started")
		firstPIDPath := filepath.Join(controlDir, "first-pid")
		firstDescendantPIDPath := filepath.Join(controlDir, "first-descendant-pid")
		firstBlock := filepath.Join(controlDir, "first-block")
		middleRan := filepath.Join(controlDir, "middle-ran")
		lastRan := filepath.Join(controlDir, "last-ran")
		gomega.Expect(syscall.Mkfifo(holderRelease, 0o600)).To(gomega.Succeed())
		gomega.Expect(syscall.Mkfifo(firstBlock, 0o600)).To(gomega.Succeed())

		holder := startCompiledQueueProcess(ctx, t, bin, repoRoot, "",
			"queue", "run", "--socket", socketPath, "--pool", "planner", "--label", "planner-holder", "--quiet", "--",
			"/bin/sh", "-c", `set -eu; "$1" queue run --socket "$2" --pool planner --label same-pool-nested --quiet -- /bin/sh -c 'printf nested > "$1"' nested "$3"; printf '%s' "$$" > "$4"; printf started > "$5"; IFS= read -r _ < "$6"`,
			"holder", bin, socketPath, nestedRan, holderPIDPath, holderStarted, holderRelease,
		)
		gomega.Eventually(ctx, func() string {
			body, _ := os.ReadFile(holderStarted)
			return string(body)
		}).Should(gomega.Equal("started"))
		holderIdentity := captureCompiledQueueProcessIdentity(holder, holderPIDPath)
		nestedBody, err := os.ReadFile(nestedRan)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(nestedBody)).To(gomega.Equal("nested"))

		snapshot := compiledQueueStatus(ctx, t, bin, repoRoot, socketPath)
		gomega.Expect(snapshot.InFlight).To(gomega.HaveLen(1))
		gomega.Expect(snapshot.InFlight[0]).To(gomega.And(
			gomega.HaveField("Pool", "planner"),
			gomega.HaveField("Label", "planner-holder"),
		))
		gomega.Expect(snapshot.Waiting).To(gomega.BeEmpty(), "same-pool nested execution must reuse the ancestor lease")

		isolated := runCompiledQueueCommand(ctx, t, bin, repoRoot,
			"queue", "run", "--socket", socketPath, "--pool", "executor", "--label", "independent-executor", "--quiet", "--",
			"/bin/sh", "-c", `printf executor > "$1"`, "executor", executorRan,
		)
		gomega.Expect(isolated.code).To(gomega.BeZero(), "stdout=%s\nstderr=%s", isolated.stdout, isolated.stderr)
		executorBody, err := os.ReadFile(executorRan)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(executorBody)).To(gomega.Equal("executor"), "executor pool must progress while planner is occupied")

		first := startCompiledQueueProcess(ctx, t, bin, repoRoot, "",
			"queue", "run", "--socket", socketPath, "--pool", "planner", "--label", "first-waiter", "--quiet", "--",
			"/bin/sh", "-c", `set -eu; printf '%s' "$$" > "$1"; /bin/sh -c 'trap "" HUP INT TERM; printf "%s" "$$" > "$1"; IFS= read -r _ < "$2"' nested "$2" "$4" & nested_pid=$!; printf started > "$3"; wait "$nested_pid"`,
			"first", firstPIDPath, firstDescendantPIDPath, firstStarted, firstBlock,
		)
		waitForCompiledQueueWaitingLabels(ctx, t, bin, repoRoot, socketPath, "first-waiter")

		middle := startCompiledQueueProcess(ctx, t, bin, repoRoot, "",
			"queue", "run", "--socket", socketPath, "--pool", "planner", "--label", "middle-waiter", "--quiet", "--",
			"/bin/sh", "-c", `printf middle > "$1"`, "middle", middleRan,
		)
		waitForCompiledQueueWaitingLabels(ctx, t, bin, repoRoot, socketPath, "first-waiter", "middle-waiter")

		last := startCompiledQueueProcess(ctx, t, bin, repoRoot, "",
			"queue", "run", "--socket", socketPath, "--pool", "planner", "--label", "last-waiter", "--quiet", "--",
			"/bin/sh", "-c", `printf last > "$1"`, "last", lastRan,
		)
		snapshot = waitForCompiledQueueWaitingLabels(ctx, t, bin, repoRoot, socketPath, "first-waiter", "middle-waiter", "last-waiter")
		gomega.Expect(snapshot.Waiting).To(gomega.HaveExactElements(
			gomega.HaveField("Position", 1),
			gomega.HaveField("Position", 2),
			gomega.HaveField("Position", 3),
		))

		middleID := snapshot.Waiting[1].ID
		cancelResult := runCompiledQueueCommand(ctx, t, bin, repoRoot, "queue", "cancel", "--socket", socketPath, middleID)
		gomega.Expect(cancelResult.code).To(gomega.BeZero(), "stdout=%s\nstderr=%s", cancelResult.stdout, cancelResult.stderr)
		expectCompiledQueueExit(ctx, middle, 1)
		_, err = os.Stat(middleRan)
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "the canceled middle waiter's child must never start")
		snapshot = waitForCompiledQueueWaitingLabels(ctx, t, bin, repoRoot, socketPath, "first-waiter", "last-waiter")
		gomega.Expect(snapshot.Waiting).To(gomega.HaveExactElements(
			gomega.HaveField("Position", 1),
			gomega.HaveField("Position", 2),
		))

		releaseQueueHolder(ctx, t, holderRelease)
		expectCompiledQueueExit(ctx, holder, 0)
		expectCompiledQueueProcessIdentityGone(ctx, holderIdentity)
		removeCompiledQueuePIDRecords(holderPIDPath)
		gomega.Eventually(ctx, func() string {
			body, _ := os.ReadFile(firstStarted)
			return string(body)
		}).Should(gomega.Equal("started"))
		gomega.Eventually(ctx, func() []string {
			snapshot = compiledQueueStatus(ctx, t, bin, repoRoot, socketPath)
			labels := make([]string, 0, len(snapshot.InFlight)+len(snapshot.Waiting))
			for _, job := range snapshot.InFlight {
				labels = append(labels, job.Label)
			}
			for _, job := range snapshot.Waiting {
				labels = append(labels, job.Label)
			}
			return labels
		}).Should(gomega.Equal([]string{"first-waiter", "last-waiter"}))
		gomega.Expect(snapshot.Waiting[0].Position).To(gomega.Equal(1))
		_, err = os.Stat(lastRan)
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "the final waiter must remain blocked behind the FIFO head")

		firstPID, err := readCompiledQueuePID(firstPIDPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		var firstDescendantPID int
		gomega.Eventually(ctx, func() error {
			firstDescendantPID, err = readCompiledQueuePID(firstDescendantPIDPath)

			return err
		}).Should(gomega.Succeed())
		gomega.Expect(firstDescendantPID).NotTo(gomega.Equal(firstPID))
		firstIdentity, err := compiledQueueProcessIdentityForPID(firstPID)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		firstDescendantIdentity, err := compiledQueueProcessIdentityForPID(firstDescendantPID)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		trackCompiledQueueProcessIdentity(first, firstIdentity)
		trackCompiledQueueProcessIdentity(first, firstDescendantIdentity)
		gomega.Expect(first.command.Process.Signal(syscall.SIGTERM)).To(gomega.Succeed())
		firstExitErr := finishCompiledQueueProcess(ctx, first)
		var firstExit *exec.ExitError
		gomega.Expect(errors.As(firstExitErr, &firstExit)).To(gomega.BeTrue(), "stdout=%s\nstderr=%s\nerror=%v", &first.stdout, &first.stderr, firstExitErr)
		gomega.Expect(firstExit.ExitCode()).NotTo(gomega.BeZero())
		expectCompiledQueueProcessIdentityGone(ctx, firstIdentity)
		expectCompiledQueueProcessIdentityGone(ctx, firstDescendantIdentity)
		removeCompiledQueuePIDRecords(firstPIDPath, firstDescendantPIDPath)

		expectCompiledQueueExit(ctx, last, 0)
		lastBody, err := os.ReadFile(lastRan)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(lastBody)).To(gomega.Equal("last"))
		gomega.Eventually(ctx, func() [2]int {
			snapshot = compiledQueueStatus(ctx, t, bin, repoRoot, socketPath)
			return [2]int{len(snapshot.InFlight), len(snapshot.Waiting)}
		}).Should(gomega.Equal([2]int{}))
		for _, pool := range snapshot.Pools {
			gomega.Expect(pool.InFlight).To(gomega.BeZero(), pool.Name)
			gomega.Expect(pool.Waiting).To(gomega.BeZero(), pool.Name)
		}

		stopCompiledQueueDaemon(ctx, daemon)
	})

	ginkgo.It("observes an immediate wrapper exit before its single reap", ginkgo.Label("race-contract"), ginkgo.SpecTimeout(compiledQueueSpecTimeout), func(ctx ginkgo.SpecContext) {
		process := startCompiledQueueProcess(ctx, ginkgo.GinkgoT(), bin, repoRoot, "",
			"queue", "ping", "--socket", filepath.Join(ginkgo.GinkgoT().TempDir(), "missing.sock"),
		)

		firstErr := finishCompiledQueueProcess(ctx, process)
		var exitErr *exec.ExitError
		gomega.Expect(errors.As(firstErr, &exitErr)).To(gomega.BeTrue())
		gomega.Expect(exitErr.ExitCode()).To(gomega.Equal(1))
		gomega.Expect(finishCompiledQueueProcess(ctx, process)).To(gomega.BeIdenticalTo(firstErr), "repeated observation must publish the immutable first reap result")
		gomega.Expect(signalCompiledQueueProcessGroup(process, syscall.SIGKILL)).To(gomega.MatchError(os.ErrProcessDone), "a reaped leader must never be targeted by a later group signal")
	})

	ginkgo.It("fails queued work on daemon crash and restarts cleanly from the stale socket", ginkgo.Label("race-contract"), ginkgo.SpecTimeout(compiledQueueSpecTimeout), func(ctx ginkgo.SpecContext) {
		t := ginkgo.GinkgoT()
		daemon, socketPath := startCompiledQueueDaemon(ctx, t, bin, repoRoot, runner.DefaultConfig())

		controlDir := t.TempDir()
		holderPIDPath := filepath.Join(controlDir, "holder-pid")
		holderStarted := filepath.Join(controlDir, "holder-started")
		holderRelease := filepath.Join(controlDir, "holder-release")
		holderFinished := filepath.Join(controlDir, "holder-finished")
		waiterRan := filepath.Join(controlDir, "waiter-ran")
		restartedRan := filepath.Join(controlDir, "restarted-ran")
		gomega.Expect(syscall.Mkfifo(holderRelease, 0o600)).To(gomega.Succeed())

		holder := startCompiledQueueProcess(ctx, t, bin, repoRoot, "",
			"queue", "run", "--socket", socketPath, "--pool", "default", "--label", "crash-holder", "--quiet", "--",
			"/bin/sh", "-c", `printf '%s' "$$" > "$1"; printf started > "$2"; IFS= read -r _ < "$3"; printf finished > "$4"`,
			"holder", holderPIDPath, holderStarted, holderRelease, holderFinished,
		)
		gomega.Eventually(ctx, func() string {
			body, _ := os.ReadFile(holderStarted)

			return string(body)
		}).Should(gomega.Equal("started"))
		holderChildPID, err := readCompiledQueuePID(holderPIDPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		holderChildIdentity, err := compiledQueueProcessIdentityForPID(holderChildPID)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		trackCompiledQueueProcessIdentity(holder, holderChildIdentity)

		waiter := startCompiledQueueProcess(ctx, t, bin, repoRoot, "",
			"queue", "run", "--socket", socketPath, "--pool", "default", "--label", "crash-waiter", "--quiet", "--",
			"/bin/sh", "-c", `printf should-not-run > "$1"`, "waiter", waiterRan,
		)
		snapshot := waitForCompiledQueueWaitingLabels(ctx, t, bin, repoRoot, socketPath, "crash-waiter")
		gomega.Expect(snapshot.InFlight).To(gomega.HaveExactElements(gomega.HaveField("Label", "crash-holder")))
		_, err = os.Stat(waiterRan)
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "the queued child must not start before admission")

		gomega.Expect(signalCompiledQueueProcessGroup(daemon, syscall.SIGKILL)).To(gomega.Succeed())
		expectCompiledQueueSignalExit(ctx, daemon, syscall.SIGKILL)
		staleSocket, err := os.Lstat(socketPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "a hard-killed daemon must leave the Unix socket entry for restart recovery")
		gomega.Expect(staleSocket.Mode() & os.ModeSocket).NotTo(gomega.BeZero())

		expectCompiledQueueExit(ctx, waiter, 1)
		gomega.Expect(waiter.stderr.String()).To(gomega.And(
			gomega.ContainSubstring("lease error before grant"),
			gomega.ContainSubstring("EOF"),
		))
		_, err = os.Stat(waiterRan)
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "daemon failure must not launch queued work")

		gomega.Expect(syscall.Kill(holderChildPID, 0)).To(gomega.Succeed(), "the queue wrapper, not the daemon, owns the granted child")
		releaseQueueHolder(ctx, t, holderRelease)
		expectCompiledQueueExit(ctx, holder, 0)
		holderFinishedBody, err := os.ReadFile(holderFinished)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(holderFinishedBody)).To(gomega.Equal("finished"))
		expectCompiledQueueProcessIdentityGone(ctx, holderChildIdentity)
		removeCompiledQueuePIDRecords(holderPIDPath)

		restarted := startCompiledQueueDaemonAt(ctx, t, bin, repoRoot, socketPath, runner.DefaultConfig())
		snapshot = compiledQueueStatus(ctx, t, bin, repoRoot, socketPath)
		gomega.Expect(snapshot.InFlight).To(gomega.BeEmpty(), "crashed in-memory admission state must not survive restart")
		gomega.Expect(snapshot.Waiting).To(gomega.BeEmpty(), "the failed waiter must not be reconstructed from the stale socket")

		recovery := runCompiledQueueCommand(ctx, t, bin, repoRoot,
			"queue", "run", "--socket", socketPath, "--pool", "default", "--label", "post-crash-restart", "--quiet", "--",
			"/bin/sh", "-c", `printf restarted > "$1"`, "recovery", restartedRan,
		)
		gomega.Expect(recovery.code).To(gomega.BeZero(), "stdout=%s\nstderr=%s", recovery.stdout, recovery.stderr)
		restartedBody, err := os.ReadFile(restartedRan)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(restartedBody)).To(gomega.Equal("restarted"))
		_, err = os.Stat(waiterRan)
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), "restart must never resurrect the failed waiter")

		snapshot = compiledQueueStatus(ctx, t, bin, repoRoot, socketPath)
		gomega.Expect(snapshot.InFlight).To(gomega.BeEmpty())
		gomega.Expect(snapshot.Waiting).To(gomega.BeEmpty())
		for _, pool := range snapshot.Pools {
			gomega.Expect(pool.InFlight).To(gomega.BeZero(), pool.Name)
			gomega.Expect(pool.Waiting).To(gomega.BeZero(), pool.Name)
		}
		stopCompiledQueueDaemon(ctx, restarted)
	})
})

func queueE2ERepoRoot() string {
	ginkgo.GinkgoHelper()

	dir, err := os.Getwd()
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		gomega.Expect(parent).NotTo(gomega.Equal(dir), "go.mod not found above %s", dir)
		dir = parent
	}
}

func compiledQueueBinaryUsesRace(buildInfo *buildinfo.BuildInfo) bool {
	for _, setting := range buildInfo.Settings {
		if setting.Key == "-race" {
			return setting.Value == "true"
		}
	}

	return false
}

func startCompiledQueueDaemon(ctx context.Context, t ginkgo.FullGinkgoTInterface, bin, repoRoot string, cfg runner.Config) (*compiledQueueProcess, string) {
	t.Helper()

	runtimeDir, err := os.MkdirTemp("/tmp", "idiom-queue-process-e2e-")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	ginkgo.DeferCleanup(func() { gomega.Expect(os.RemoveAll(runtimeDir)).To(gomega.Succeed()) })
	socketPath := filepath.Join(runtimeDir, "queue.sock")
	daemon := startCompiledQueueDaemonAt(ctx, t, bin, repoRoot, socketPath, cfg)

	return daemon, socketPath
}

func startCompiledQueueDaemonAt(ctx context.Context, t ginkgo.FullGinkgoTInterface, bin, repoRoot, socketPath string, cfg runner.Config) *compiledQueueProcess {
	t.Helper()

	configPath := filepath.Join(filepath.Dir(socketPath), "runner.json")
	configBody, err := json.Marshal(cfg)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	configBody = append(configBody, '\n')
	gomega.Expect(os.WriteFile(configPath, configBody, 0o600)).To(gomega.Succeed())

	daemon := startCompiledQueueProcess(ctx, t, bin, repoRoot, "idiom queue daemon listening on", "queue", "serve", "--socket", socketPath, "--config", configPath)
	select {
	case <-daemon.stdout.ready:
	case <-daemon.exitObserved:
		processErr := finishCompiledQueueProcess(ctx, daemon)
		gomega.Expect(processErr).NotTo(gomega.HaveOccurred(), "daemon stdout=%s stderr=%s", &daemon.stdout, &daemon.stderr)
		ginkgo.Fail(fmt.Sprintf("queue daemon exited before readiness; stdout=%s stderr=%s", &daemon.stdout, &daemon.stderr))
	case <-ctx.Done():
		ginkgo.Fail(fmt.Sprintf("queue daemon readiness interrupted: %v; stdout=%s stderr=%s", ctx.Err(), &daemon.stdout, &daemon.stderr))
	}
	ping := runCompiledQueueCommand(ctx, t, bin, repoRoot, "queue", "ping", "--socket", socketPath)
	gomega.Expect(ping.code).To(gomega.BeZero(), "stdout=%s\nstderr=%s", ping.stdout, ping.stderr)

	return daemon
}

func startCompiledQueueProcess(ctx context.Context, t ginkgo.FullGinkgoTInterface, bin, repoRoot, readyMarker string, args ...string) *compiledQueueProcess {
	t.Helper()
	gomega.Expect(ctx.Err()).NotTo(gomega.HaveOccurred())

	process := &compiledQueueProcess{
		exitObserved: make(chan struct{}),
	}
	if readyMarker != "" {
		process.stdout.readyMarker = readyMarker
		process.stdout.ready = make(chan struct{})
	}
	// The test harness owns these long-running processes. Keep them independent
	// from SpecContext cancellation so cleanup can signal and reap the complete
	// group instead of letting exec.CommandContext SIGKILL only the wrapper.
	process.command = exec.Command(bin, args...)
	process.command.Dir = repoRoot
	process.command.Env = compiledQueueEnvironment(t.TempDir())
	process.command.Stdout = &process.stdout
	process.command.Stderr = &process.stderr
	process.command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	gomega.Expect(process.command.Start()).To(gomega.Succeed())
	// Setpgid with a zero Pgid makes the started child its own group leader.
	// Publish that identity and install the non-reaping observer/cleanup before
	// any subsequent assertion can abort setup and strand the process.
	process.processGroupID = process.command.Process.Pid
	go func() {
		process.exitObservationErr = observeCompiledQueueProcessExit(process.command.Process.Pid)
		close(process.exitObserved)
	}()
	ginkgo.DeferCleanup(func() { cleanupCompiledQueueProcess(process) })

	processGroupID, err := syscall.Getpgid(process.command.Process.Pid)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(processGroupID).To(gomega.Equal(process.command.Process.Pid), "each test-owned queue process must lead its cleanup group")
	process.processGroupID = processGroupID

	return process
}

func cleanupCompiledQueueProcess(process *compiledQueueProcess) {
	ginkgo.GinkgoHelper()
	if process == nil {
		return
	}

	process.lifecycleMu.Lock()
	var cleanupErrors []error
	record := func(err error) {
		if err != nil && !errors.Is(err, os.ErrProcessDone) {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	if process.reaped {
		record(terminateTrackedCompiledQueueDescendantGroups(process.ownedDescendants))
		process.lifecycleMu.Unlock()
		gomega.Expect(errors.Join(cleanupErrors...)).NotTo(gomega.HaveOccurred())

		return
	}

	if !compiledQueueProcessExitObserved(process) {
		record(terminateCompiledQueueDescendantGroups(process.command.Process.Pid))
		record(terminateTrackedCompiledQueueDescendantGroups(process.ownedDescendants))
		if err := signalReservedCompiledQueueProcessGroup(process, syscall.SIGKILL); err != nil {
			record(err)
			// The exec.Process handle still names the exact child even when a
			// process-group identity check fails. Use it as the final route to
			// unblock observation and guarantee the single reap below.
			record(process.command.Process.Kill())
		}
	}
	<-process.exitObserved
	record(process.exitObservationErr)
	record(signalReservedCompiledQueueProcessGroup(process, syscall.SIGKILL))
	reapCompiledQueueProcessLocked(process)
	record(terminateTrackedCompiledQueueDescendantGroups(process.ownedDescendants))
	process.lifecycleMu.Unlock()

	gomega.Expect(errors.Join(cleanupErrors...)).NotTo(gomega.HaveOccurred())
}

func compiledQueueProcessExitObserved(process *compiledQueueProcess) bool {
	select {
	case <-process.exitObserved:
		return true
	default:
		return false
	}
}

func finishCompiledQueueProcess(ctx context.Context, process *compiledQueueProcess) error {
	ginkgo.GinkgoHelper()

	gomega.Eventually(ctx, process.exitObserved).Should(gomega.BeClosed())
	gomega.Expect(process.exitObservationErr).NotTo(gomega.HaveOccurred())

	process.lifecycleMu.Lock()
	defer process.lifecycleMu.Unlock()
	if !process.reaped {
		gomega.Expect(signalReservedCompiledQueueProcessGroup(process, syscall.SIGKILL)).To(gomega.Succeed())
		reapCompiledQueueProcessLocked(process)
	}

	return process.waitErr
}

func reapCompiledQueueProcessLocked(process *compiledQueueProcess) {
	process.waitErr = process.command.Wait()
	process.reaped = true
}

func signalCompiledQueueProcessGroup(process *compiledQueueProcess, signal syscall.Signal) error {
	if process == nil {
		return nil
	}

	process.lifecycleMu.Lock()
	defer process.lifecycleMu.Unlock()
	if process.reaped {
		return os.ErrProcessDone
	}

	return signalReservedCompiledQueueProcessGroup(process, signal)
}

func signalReservedCompiledQueueProcessGroup(process *compiledQueueProcess, signal syscall.Signal) error {
	if process.processGroupID <= 1 {
		return nil
	}
	if !compiledQueueProcessExitObserved(process) {
		currentGroupID, err := syscall.Getpgid(process.command.Process.Pid)
		if err != nil {
			return fmt.Errorf("inspect reserved queue process group: %w", err)
		}
		if currentGroupID != process.processGroupID {
			return fmt.Errorf("queue process group changed from %d to %d before reap", process.processGroupID, currentGroupID)
		}
	}
	// After exit, Darwin no longer exposes Getpgid for the zombie. The native
	// observer has not reaped it, so its PID (and matching process-group identity)
	// remains reserved until the single Wait below.
	err := syscall.Kill(-process.processGroupID, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if compiledQueueProcessExitObserved(process) && errors.Is(err, syscall.EPERM) {
		// Darwin reports EPERM when the reserved group contains only the
		// non-signalable zombie leader. Descendants owned by this user would keep
		// the group signalable, so this is the quiescent pre-reap state.
		return nil
	}

	return err
}

func (output *compiledQueueOutput) Write(p []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()

	written, err := output.buffer.Write(p)
	if output.ready != nil && strings.Contains(output.buffer.String(), output.readyMarker) {
		output.readyOnce.Do(func() { close(output.ready) })
	}

	return written, err
}

func (output *compiledQueueOutput) String() string {
	output.mu.Lock()
	defer output.mu.Unlock()

	return output.buffer.String()
}

func runCompiledQueueCommand(ctx context.Context, t ginkgo.FullGinkgoTInterface, bin, repoRoot string, args ...string) compiledQueueResult {
	t.Helper()

	command := exec.CommandContext(ctx, bin, args...)
	command.Dir = repoRoot
	command.Env = compiledQueueEnvironment(t.TempDir())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	result := compiledQueueResult{stdout: stdout.String(), stderr: stderr.String()}
	if err == nil {
		return result
	}

	var exitErr *exec.ExitError
	gomega.Expect(errors.As(err, &exitErr)).To(gomega.BeTrue(), "run %v: %v", args, err)
	result.code = exitErr.ExitCode()

	return result
}

func compiledQueueStatus(ctx context.Context, t ginkgo.FullGinkgoTInterface, bin, repoRoot, socketPath string) runner.StatusPayload {
	t.Helper()

	result := runCompiledQueueCommand(ctx, t, bin, repoRoot, "queue", "status", "--socket", socketPath, "--json")
	gomega.Expect(result.code).To(gomega.BeZero(), "stdout=%s\nstderr=%s", result.stdout, result.stderr)
	var snapshot runner.StatusPayload
	gomega.Expect(json.Unmarshal([]byte(result.stdout), &snapshot)).To(gomega.Succeed(), result.stdout)

	return snapshot
}

func waitForCompiledQueueWaitingLabels(ctx context.Context, t ginkgo.FullGinkgoTInterface, bin, repoRoot, socketPath string, expected ...string) runner.StatusPayload {
	t.Helper()

	var snapshot runner.StatusPayload
	gomega.Eventually(ctx, func() []string {
		snapshot = compiledQueueStatus(ctx, t, bin, repoRoot, socketPath)
		labels := make([]string, 0, len(snapshot.Waiting))
		for _, job := range snapshot.Waiting {
			labels = append(labels, job.Label)
		}

		return labels
	}).Should(gomega.Equal(expected))

	return snapshot
}

func releaseQueueHolder(ctx context.Context, t ginkgo.FullGinkgoTInterface, fifoPath string) {
	t.Helper()

	type releaseResult struct {
		fifo *os.File
		err  error
	}
	released := make(chan releaseResult)
	go func() {
		// Opening a FIFO read-write is nonblocking even if the child has written
		// its started marker but has not entered read(2) yet. The buffered write
		// remains available while this descriptor is held for spec cleanup.
		fifo, err := os.OpenFile(fifoPath, os.O_RDWR, 0)
		if err == nil {
			_, err = fifo.WriteString("release\n")
		}
		select {
		case released <- releaseResult{fifo: fifo, err: err}:
		case <-ctx.Done():
			if fifo != nil {
				_ = fifo.Close()
			}
		}
	}()

	select {
	case result := <-released:
		if result.err != nil && result.fifo != nil {
			_ = result.fifo.Close()
		}
		gomega.Expect(result.err).NotTo(gomega.HaveOccurred())
		gomega.Expect(result.fifo).NotTo(gomega.BeNil())
		ginkgo.DeferCleanup(func() { gomega.Expect(result.fifo.Close()).To(gomega.Succeed()) })
	case <-ctx.Done():
		ginkgo.Fail(fmt.Sprintf("queue holder release interrupted: %v", ctx.Err()))
	}
}

func readCompiledQueuePID(path string) (int, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(body)))
	if err != nil {
		return 0, err
	}
	if pid <= 1 {
		return 0, fmt.Errorf("unsafe queue fixture PID %d", pid)
	}

	return pid, nil
}

func terminateCompiledQueueDescendantGroups(rootPID int) error {
	identities, err := compiledQueueProcessSnapshot()
	if err != nil {
		return err
	}

	childrenByParent := make(map[int][]compiledQueueProcessIdentity)
	for _, identity := range identities {
		childrenByParent[identity.ParentPID] = append(childrenByParent[identity.ParentPID], identity)
	}
	descendantGroups := make(map[int][]compiledQueueProcessIdentity)
	pendingParents := []int{rootPID}
	for len(pendingParents) > 0 {
		parentPID := pendingParents[0]
		pendingParents = pendingParents[1:]
		for _, child := range childrenByParent[parentPID] {
			pendingParents = append(pendingParents, child.PID)
			if child.GroupID > 1 {
				descendantGroups[child.GroupID] = append(descendantGroups[child.GroupID], child)
			}
		}
	}

	return terminateCompiledQueueIdentityGroups(descendantGroups)
}

func terminateTrackedCompiledQueueDescendantGroups(identities []compiledQueueProcessIdentity) error {
	descendantGroups := make(map[int][]compiledQueueProcessIdentity)
	for _, identity := range identities {
		if identity.GroupID > 1 {
			descendantGroups[identity.GroupID] = append(descendantGroups[identity.GroupID], identity)
		}
	}

	return terminateCompiledQueueIdentityGroups(descendantGroups)
}

func terminateCompiledQueueIdentityGroups(descendantGroups map[int][]compiledQueueProcessIdentity) error {
	var cleanupErrors []error
	for groupID, expectedMembers := range descendantGroups {
		owned := false
		for _, expected := range expectedMembers {
			current, identityErr := compiledQueueProcessIdentityForPID(expected.PID)
			if errors.Is(identityErr, os.ErrNotExist) {
				continue
			}
			if identityErr != nil {
				cleanupErrors = append(cleanupErrors, identityErr)
				continue
			}
			if sameCompiledQueueProcessIdentity(current, expected) {
				owned = true
			}
		}
		if !owned {
			continue
		}
		if err := syscall.Kill(-groupID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("terminate queue descendant group %d: %w", groupID, err))
		}
	}

	return errors.Join(cleanupErrors...)
}

func captureCompiledQueueProcessIdentity(process *compiledQueueProcess, path string) compiledQueueProcessIdentity {
	ginkgo.GinkgoHelper()

	pid, err := readCompiledQueuePID(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	identity, err := compiledQueueProcessIdentityForPID(pid)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	trackCompiledQueueProcessIdentity(process, identity)

	return identity
}

func trackCompiledQueueProcessIdentity(process *compiledQueueProcess, identity compiledQueueProcessIdentity) {
	if process == nil || identity.PID <= 1 || identity.GroupID <= 1 || identity.StartToken == "" {
		return
	}

	process.lifecycleMu.Lock()
	defer process.lifecycleMu.Unlock()
	for _, existing := range process.ownedDescendants {
		if sameCompiledQueueProcessIdentity(existing, identity) {
			return
		}
	}
	process.ownedDescendants = append(process.ownedDescendants, identity)
}

func removeCompiledQueuePIDRecords(paths ...string) {
	ginkgo.GinkgoHelper()

	for _, path := range paths {
		gomega.Expect(os.Remove(path)).To(gomega.Succeed())
	}
}

func expectCompiledQueueProcessIdentityGone(ctx context.Context, expected compiledQueueProcessIdentity) {
	ginkgo.GinkgoHelper()

	gomega.Eventually(ctx, func() (bool, error) {
		current, err := compiledQueueProcessIdentityForPID(expected.PID)
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		if err != nil {
			return false, err
		}

		return !sameCompiledQueueProcessIdentity(current, expected), nil
	}).Should(gomega.BeTrue(), "queue fixture process %d survived owner shutdown", expected.PID)
}

func sameCompiledQueueProcessIdentity(left, right compiledQueueProcessIdentity) bool {
	return left.PID == right.PID &&
		left.GroupID == right.GroupID &&
		left.StartToken == right.StartToken
}

func expectCompiledQueueExit(ctx context.Context, process *compiledQueueProcess, expectedCode int) {
	ginkgo.GinkgoHelper()

	err := finishCompiledQueueProcess(ctx, process)
	if expectedCode == 0 {
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "stdout=%s\nstderr=%s", &process.stdout, &process.stderr)

		return
	}

	var exitErr *exec.ExitError
	gomega.Expect(errors.As(err, &exitErr)).To(gomega.BeTrue(), "stdout=%s\nstderr=%s\nerror=%v", &process.stdout, &process.stderr, err)
	gomega.Expect(exitErr.ExitCode()).To(gomega.Equal(expectedCode), "stdout=%s\nstderr=%s", &process.stdout, &process.stderr)
}

func expectCompiledQueueSignalExit(ctx context.Context, process *compiledQueueProcess, expectedSignal syscall.Signal) {
	ginkgo.GinkgoHelper()

	err := finishCompiledQueueProcess(ctx, process)
	var exitErr *exec.ExitError
	gomega.Expect(errors.As(err, &exitErr)).To(gomega.BeTrue(), "stdout=%s\nstderr=%s\nerror=%v", &process.stdout, &process.stderr, err)
	waitStatus, ok := exitErr.Sys().(syscall.WaitStatus)
	gomega.Expect(ok).To(gomega.BeTrue(), "unexpected process status %T", exitErr.Sys())
	gomega.Expect(waitStatus.Signaled()).To(gomega.BeTrue(), "stdout=%s\nstderr=%s", &process.stdout, &process.stderr)
	gomega.Expect(waitStatus.Signal()).To(gomega.Equal(expectedSignal), "stdout=%s\nstderr=%s", &process.stdout, &process.stderr)
}

func stopCompiledQueueDaemon(ctx context.Context, daemon *compiledQueueProcess) {
	ginkgo.GinkgoHelper()

	gomega.Expect(signalCompiledQueueProcessGroup(daemon, syscall.SIGTERM)).To(gomega.Succeed())
	expectCompiledQueueExit(ctx, daemon, 0)
}

func compiledQueueEnvironment(home string) []string {
	overrides := map[string]string{
		"HOME":                   home,
		"IDIOM_QUEUE_HELD_POOLS": "",
		"IDIOM_TELEMETRY":        "0",
	}
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[key]; !replaced {
			environment = append(environment, entry)
		}
	}
	for key, value := range overrides {
		environment = append(environment, fmt.Sprintf("%s=%s", key, value))
	}

	return environment
}
