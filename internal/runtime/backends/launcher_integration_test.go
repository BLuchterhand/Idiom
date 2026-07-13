//go:build integration && !windows

package backends

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

func tempLog() string {
	ginkgo.GinkgoHelper()

	path := filepath.Join(ginkgo.GinkgoT().TempDir(), "spawn.log")
	gomega.Expect(os.WriteFile(path, nil, 0o644)).To(gomega.Succeed())

	return path
}

// These tests cover the otherwise-bypassed production functions:
// realSpawn (starts an actual detached subprocess), portTCPReady (real TCP
// connect), and realPIDForPort (real `lsof` call). They're all
// build-tagged !windows because they rely on POSIX primitives the
// stub Windows launcher doesn't provide.

var _ = ginkgo.Describe("Backend launcher process boundaries", ginkgo.Label("integration"), func() {
	ginkgo.It("starts a detached process in its own process group", func() {
		if _, err := exec.LookPath("sleep"); err != nil {
			ginkgo.Skip("sleep binary not available")
		}

		pid, err := realSpawn(LaunchSpec{
			Alias: "sleeper",
			Argv:  []string{"sleep", "5"},
			Port:  0,
		}, tempLog())
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		ginkgo.DeferCleanup(func() {
			if pid > 0 {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		})

		gomega.Eventually(func() bool { return pidIsAlive(pid) }).
			WithTimeout(500 * time.Millisecond).
			WithPolling(20 * time.Millisecond).
			Should(gomega.BeTrue())

		pgid, err := syscall.Getpgid(pid)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(pgid).To(gomega.Equal(pid))
	})

	ginkgo.It("reports an unresolvable binary", func() {
		_, err := realSpawn(LaunchSpec{
			Alias: "nope",
			Argv:  []string{"definitely-not-a-real-binary-86753"},
			Port:  0,
		}, tempLog())

		gomega.Expect(err).To(gomega.HaveOccurred())
	})

	ginkgo.It("detects a live TCP listener and its disappearance", func() {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		ginkgo.DeferCleanup(func() { _ = listener.Close() })

		port := listener.Addr().(*net.TCPAddr).Port
		gomega.Expect(portTCPReady(port, time.Now().Add(500*time.Millisecond))).To(gomega.BeTrue())

		gomega.Expect(listener.Close()).To(gomega.Succeed())
		gomega.Expect(portTCPReady(port, time.Now().Add(400*time.Millisecond))).To(gomega.BeFalse())
	})

	ginkgo.It("identifies the process that owns an active listener", func() {
		if _, err := exec.LookPath("lsof"); err != nil {
			ginkgo.Skip("lsof not available")
		}

		listener, err := net.Listen("tcp", "127.0.0.1:0")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		ginkgo.DeferCleanup(func() { _ = listener.Close() })

		pid, err := realPIDForPort(listener.Addr().(*net.TCPAddr).Port)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(pid).To(gomega.Equal(getPID()))
	})

	ginkgo.It("reports zero when no process owns a port", func() {
		if _, err := exec.LookPath("lsof"); err != nil {
			ginkgo.Skip("lsof not available")
		}

		listener, err := net.Listen("tcp", "127.0.0.1:0")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		port := listener.Addr().(*net.TCPAddr).Port
		gomega.Expect(listener.Close()).To(gomega.Succeed())

		pid, err := realPIDForPort(port)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(pid).To(gomega.BeZero())
	})

	ginkgo.It("delivers SIGTERM to a child process", func() {
		if _, err := exec.LookPath("sleep"); err != nil {
			ginkgo.Skip("sleep not available")
		}

		cmd := exec.Command("sleep", "30")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		gomega.Expect(cmd.Start()).To(gomega.Succeed())

		pid := cmd.Process.Pid
		ginkgo.DeferCleanup(func() {
			if pid > 0 {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		})

		gomega.Expect(realStopSignal(pid, SignalTerm)).To(gomega.Succeed())

		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		gomega.Eventually(done).WithTimeout(2 * time.Second).Should(gomega.Receive())
	})
})

func pidIsAlive(pid int) bool {
	// signal 0: no-op, used to probe whether the process exists
	// without actually killing it. Errno ESRCH means "no such pid."
	//
	// Reject non-positive pids explicitly: kill(0, …) signals the
	// caller's process group and kill(-1, …) broadcasts to every
	// user-owned process. A passing test must not be one
	// caller-bug away from nuking the developer's login session.
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil
}

func getPID() int {
	pid, _ := strconv.Atoi(extractSelfPID())
	return pid
}

func extractSelfPID() string {
	out, _ := exec.Command("sh", "-c", "echo $PPID").Output()
	// $PPID from a child shell is *our* pid (the shell's parent).
	pid := string(out)
	for i, b := range pid {
		if b == '\n' || b == '\r' {
			pid = pid[:i]
			break
		}
	}
	return pid
}
