//go:build darwin || linux

package commandexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const managedProcessTreeSpecTimeout = 10 * time.Second

type managedProcessTreeFixture struct {
	directPIDPath     string
	grandchildPIDPath string
	readyPath         string
	blockPath         string
	listenerPath      string
}

var _ = ginkgo.Describe(
	"Managed process-tree lifecycle",
	ginkgo.Label("development-scenario", "e2e", "integration", "smoke", "race-contract"),
	func() {
		ginkgo.It("surfaces a terminal group-kill failure without entering quiescence", ginkgo.SpecTimeout(managedProcessTreeSpecTimeout), func(ctx ginkgo.SpecContext) {
			fixture := newManagedProcessTreeFixture()
			cmd, processTree, err := managedCommand(ctx, fixture.spec())
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			terminationErr := errors.New("injected process-group termination failure")
			processTree.terminateGroup = func(int) error { return terminationErr }
			gomega.Expect(cmd.Start()).To(gomega.Succeed())
			waited := false
			ginkgo.DeferCleanup(func() {
				_ = processTree.terminate()
				if !waited {
					_ = cmd.Wait()
				}
				_ = processTree.close()
			})
			gomega.Expect(processTree.attach()).To(gomega.Succeed())
			directPID, grandchildPID := fixture.waitUntilReady(ctx)
			deferManagedProcessCleanup(directPID, grandchildPID)

			gotTerminationErr := processTree.terminate()
			gomega.Expect(errors.Is(gotTerminationErr, terminationErr)).To(gomega.BeTrue())
			teardownErr := processTree.waitBeforeReap()
			gomega.Expect(errors.Is(teardownErr, terminationErr)).To(gomega.BeTrue())
			// The injected group failure leaves the descendant active. Returning
			// while it is still active proves teardown did not enter the group
			// quiescence wait after terminal signaling failure.
			gomega.Expect(managedProcessAlive(grandchildPID)).To(gomega.BeTrue())
			waitErr := cmd.Wait()
			waited = true
			gomega.Expect(waitErr).To(gomega.HaveOccurred())
			gomega.Expect(processTree.close()).To(gomega.Succeed())
			expectManagedProcessGoneImmediately(directPID)
		})

		ginkgo.It("drains descendants when the leader exits before process-group attachment", ginkgo.SpecTimeout(managedProcessTreeSpecTimeout), func(ctx ginkgo.SpecContext) {
			fixture := newManagedProcessTreeFixture()
			cmd, processTree, err := managedCommand(ctx, helperSpec(
				"managed-tree-natural-parent",
				fixture.directPIDPath,
				fixture.grandchildPIDPath,
				fixture.readyPath,
				fixture.listenerPath,
			))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			observedPID := 0
			processTree.readProcessGroup = func(pid int) (int, error) {
				observedPID = pid
				return 0, syscall.ESRCH
			}
			gomega.Expect(cmd.Start()).To(gomega.Succeed())
			waited := false
			ginkgo.DeferCleanup(func() {
				_ = processTree.terminate()
				if !waited {
					_ = cmd.Wait()
					waited = true
				}
				_ = processTree.close()
			})

			directPID, grandchildPID := fixture.waitUntilRecorded(ctx)
			deferManagedProcessCleanup(directPID, grandchildPID)
			address := fixture.waitUntilListenerReady(ctx)
			// Observe the direct exit without consuming it so attachment is
			// forced through the verified pre-reap recovery path.
			gomega.Expect(waitManagedProcessExit(directPID)).To(gomega.Succeed())
			gomega.Expect(processTree.attach()).To(gomega.Succeed())
			gomega.Expect(observedPID).To(gomega.Equal(directPID))
			gomega.Expect(processTree.waitBeforeReap()).To(gomega.Succeed())

			waitErr := cmd.Wait()
			waited = true
			gomega.Expect(waitErr).NotTo(gomega.HaveOccurred())
			gomega.Expect(processTree.close()).To(gomega.Succeed())
			expectManagedProcessGoneImmediately(directPID)
			expectManagedProcessGoneImmediately(grandchildPID)
			successor, err := net.Listen("tcp", address)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			ginkgo.DeferCleanup(successor.Close)
		})

		ginkgo.DescribeTable(
			"rejecting process-group lookup failures without a verified pre-reap exit",
			func(ctx ginkgo.SpecContext, lookupErr syscall.Errno) {
				fixture := newManagedProcessTreeFixture()
				cmd, processTree, err := managedCommand(ctx, fixture.spec())
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				processTree.readProcessGroup = func(int) (int, error) {
					return 0, lookupErr
				}
				gomega.Expect(cmd.Start()).To(gomega.Succeed())
				waited := false
				ginkgo.DeferCleanup(func() {
					_ = processTree.terminate()
					if !waited {
						_ = cmd.Wait()
						waited = true
					}
					_ = processTree.close()
				})

				directPID, grandchildPID := fixture.waitUntilReady(ctx)
				deferManagedProcessCleanup(directPID, grandchildPID)
				attachErr := processTree.attach()
				gomega.Expect(errors.Is(attachErr, lookupErr)).To(gomega.BeTrue())
				gomega.Expect(processTree.waitBeforeReap()).To(gomega.Succeed())

				waitErr := cmd.Wait()
				waited = true
				gomega.Expect(waitErr).To(gomega.HaveOccurred())
				gomega.Expect(processTree.close()).To(gomega.Succeed())
				expectManagedProcessGoneImmediately(directPID)
				expectManagedProcessGoneImmediately(grandchildPID)
			},
			ginkgo.Entry("a live leader reported as missing", syscall.ESRCH),
			ginkgo.Entry("an unrelated permission failure", syscall.EPERM),
		)

		ginkgo.It("rejects an invalid wrapper group before signaling it", func() {
			processTree := &managedProcessTree{originalForegroundPGID: 1}
			gomega.Expect(processTree.handleStoppedForegroundProcess()).To(
				gomega.MatchError(gomega.ContainSubstring("invalid process group 1")),
			)
		})

		ginkgo.It("drains a post-Start process-group validation failure before returning", ginkgo.SpecTimeout(managedProcessTreeSpecTimeout), func(ctx ginkgo.SpecContext) {
			cmd, err := configuredCommand(ctx, helperSpec("sleep"))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(cmd.Start()).To(gomega.Succeed())
			pid := cmd.Process.Pid
			deferManagedProcessCleanup(pid)

			// Construct ownership after Start so the already-running child stays
			// in the caller's group and attach exercises its mismatch cleanup.
			processTree, err := newManagedProcessTree(cmd, -1)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(processTree.attach()).To(gomega.MatchError(gomega.ContainSubstring("does not match leader")))
			gomega.Expect(processTree.waitBeforeReap()).To(gomega.Succeed())
			gomega.Expect(cmd.Wait()).To(gomega.HaveOccurred())
			gomega.Expect(processTree.close()).To(gomega.Succeed())
			expectManagedProcessGoneImmediately(pid)
		})

		ginkgo.It("terminates nested RunForeground descendants on cancellation", ginkgo.SpecTimeout(managedProcessTreeSpecTimeout), func(ctx ginkgo.SpecContext) {
			fixture := newManagedProcessTreeFixture()
			runContext, cancel := context.WithCancel(ctx)
			result := make(chan Result, 1)
			go func() {
				result <- RunForeground(runContext, fixture.spec(), ForegroundIO{
					Stdout: io.Discard,
					Stderr: io.Discard,
				})
			}()

			directPID, grandchildPID := fixture.waitUntilReady(ctx)
			deferManagedProcessCleanup(directPID, grandchildPID)
			cancel()

			var runResult Result
			gomega.Eventually(ctx, result).Should(gomega.Receive(&runResult))
			gomega.Expect(runResult.Err).To(gomega.HaveOccurred())
			expectManagedProcessGoneImmediately(directPID)
			expectManagedProcessGoneImmediately(grandchildPID)
		})

		ginkgo.It("tears down descendants that inherit captured output before reaping a naturally-exited Run leader", ginkgo.SpecTimeout(managedProcessTreeSpecTimeout), func(ctx ginkgo.SpecContext) {
			fixture := newManagedProcessTreeFixture()
			result := make(chan Result, 1)
			go func() {
				result <- Run(ctx, fixture.naturalExitSpec(false))
			}()

			directPID, grandchildPID := fixture.waitUntilRecorded(ctx)
			deferManagedProcessCleanup(directPID, grandchildPID)

			var runResult Result
			gomega.Eventually(ctx, result).Should(gomega.Receive(&runResult))
			gomega.Expect(runResult.Err).NotTo(gomega.HaveOccurred())
			gomega.Expect(runResult.Stdout).To(gomega.ContainSubstring("natural-stdout"))
			gomega.Expect(runResult.Stderr).To(gomega.ContainSubstring("natural-stderr"))
			expectManagedProcessGoneImmediately(directPID)
			expectManagedProcessGoneImmediately(grandchildPID)
		})

		ginkgo.It("tears down a naturally-exited Start leader before an inherited stderr pipe can block Wait", ginkgo.SpecTimeout(managedProcessTreeSpecTimeout), func(ctx ginkgo.SpecContext) {
			fixture := newManagedProcessTreeFixture()
			process, err := Start(ctx, fixture.naturalExitSpec(true))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			ginkgo.DeferCleanup(process.Close)

			directPID, grandchildPID := fixture.waitUntilRecorded(ctx)
			deferManagedProcessCleanup(directPID, grandchildPID)
			waitResult := make(chan error, 1)
			go func() {
				waitResult <- process.wait()
			}()

			var waitErr error
			gomega.Eventually(ctx, waitResult).Should(gomega.Receive(&waitErr))
			gomega.Expect(waitErr).NotTo(gomega.HaveOccurred())
			stderr, truncated := process.Stderr()
			gomega.Expect(stderr).To(gomega.ContainSubstring("natural-stderr"))
			gomega.Expect(truncated).To(gomega.BeFalse())
			expectManagedProcessGoneImmediately(directPID)
			expectManagedProcessGoneImmediately(grandchildPID)
		})

		ginkgo.It("terminates Run descendants when the context is canceled", ginkgo.SpecTimeout(managedProcessTreeSpecTimeout), func(ctx ginkgo.SpecContext) {
			fixture := newManagedProcessTreeFixture()
			runContext, cancel := context.WithCancel(ctx)
			result := make(chan Result, 1)
			go func() {
				result <- Run(runContext, fixture.spec())
			}()

			directPID, grandchildPID := fixture.waitUntilReady(ctx)
			deferManagedProcessCleanup(directPID, grandchildPID)
			cancel()

			var runResult Result
			gomega.Eventually(ctx, result).Should(gomega.Receive(&runResult))
			gomega.Expect(runResult.Err).To(gomega.HaveOccurred())
			expectManagedProcessGoneImmediately(directPID)
			expectManagedProcessGoneImmediately(grandchildPID)
		})

		ginkgo.It("terminates Start descendants before Close and Wait return", ginkgo.SpecTimeout(managedProcessTreeSpecTimeout), func(ctx ginkgo.SpecContext) {
			fixture := newManagedProcessTreeFixture()
			process, err := Start(ctx, fixture.spec())
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			waitResult := make(chan error, 1)
			go func() {
				waitResult <- process.wait()
			}()

			directPID, grandchildPID := fixture.waitUntilReady(ctx)
			deferManagedProcessCleanup(directPID, grandchildPID)
			gomega.Expect(process.Close()).To(gomega.Succeed())

			var waitErr error
			gomega.Eventually(ctx, waitResult).Should(gomega.Receive(&waitErr))
			gomega.Expect(waitErr).To(gomega.HaveOccurred())
			expectManagedProcessGoneImmediately(directPID)
			expectManagedProcessGoneImmediately(grandchildPID)
		})

		ginkgo.It("releases descendant-owned resources before a successor starts", ginkgo.SpecTimeout(managedProcessTreeSpecTimeout), func(ctx ginkgo.SpecContext) {
			fixture := newManagedProcessTreeFixture()
			process, err := Start(ctx, fixture.resourceHandoffSpec())
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			directPID, grandchildPID := fixture.waitUntilReady(ctx)
			deferManagedProcessCleanup(directPID, grandchildPID)
			address := fixture.waitUntilListenerReady(ctx)
			gomega.Expect(process.Close()).To(gomega.Succeed())
			expectManagedProcessGoneImmediately(directPID)
			expectManagedProcessGoneImmediately(grandchildPID)

			successor, err := net.Listen("tcp", address)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			ginkgo.DeferCleanup(successor.Close)
		})
	},
)

func newManagedProcessTreeFixture() managedProcessTreeFixture {
	ginkgo.GinkgoHelper()

	root := ginkgo.GinkgoT().TempDir()
	fixture := managedProcessTreeFixture{
		directPIDPath:     filepath.Join(root, "direct.pid"),
		grandchildPIDPath: filepath.Join(root, "grandchild.pid"),
		readyPath:         filepath.Join(root, "ready"),
		blockPath:         filepath.Join(root, "block"),
		listenerPath:      filepath.Join(root, "listener"),
	}
	gomega.Expect(syscall.Mkfifo(fixture.blockPath, 0o600)).To(gomega.Succeed())

	return fixture
}

func (fixture managedProcessTreeFixture) spec() Spec {
	return Spec{
		Name: "/bin/sh",
		Args: []string{
			"-c",
			`set -eu
printf '%s\n' "$$" > "$1"
/bin/sh -c 'set -eu; printf "%s\n" "$$" > "$1"; trap "" HUP INT TERM; IFS= read -r _ < "$2"' managed-grandchild "$2" "$4" </dev/null >/dev/null 2>&1 &
grandchild_pid=$!
printf 'ready\n' > "$3"
wait "$grandchild_pid"`,
			"managed-owner",
			fixture.directPIDPath,
			fixture.grandchildPIDPath,
			fixture.readyPath,
			fixture.blockPath,
		},
	}
}

func (fixture managedProcessTreeFixture) resourceHandoffSpec() Spec {
	return helperSpec(
		"managed-tree-parent",
		fixture.directPIDPath,
		fixture.grandchildPIDPath,
		fixture.readyPath,
		fixture.listenerPath,
	)
}

func (fixture managedProcessTreeFixture) naturalExitSpec(redirectGrandchildStdout bool) Spec {
	grandchildRedirect := ""
	if redirectGrandchildStdout {
		grandchildRedirect = " >/dev/null"
	}

	return Spec{
		Name: "/bin/sh",
		Args: []string{
			"-c",
			fmt.Sprintf(`set -eu
printf '%%s\n' "$$" > "$1"
/bin/sh -c 'trap "" HUP INT TERM; IFS= read -r _ < "$1"' natural-grandchild "$4"%s &
grandchild_pid=$!
printf '%%s\n' "$grandchild_pid" > "$2"
printf 'ready\n' > "$3"
printf 'natural-stdout\n'
printf 'natural-stderr\n' >&2
exit 0`, grandchildRedirect),
			"natural-owner",
			fixture.directPIDPath,
			fixture.grandchildPIDPath,
			fixture.readyPath,
			fixture.blockPath,
		},
	}
}

func (fixture managedProcessTreeFixture) waitUntilReady(ctx context.Context) (int, int) {
	ginkgo.GinkgoHelper()

	gomega.Eventually(ctx, func() bool {
		_, err := os.Stat(fixture.readyPath)
		return err == nil
	}).Should(gomega.BeTrue())

	var directPID int
	var grandchildPID int
	gomega.Eventually(ctx, func() error {
		var err error
		directPID, err = readManagedProcessPID(fixture.directPIDPath)
		if err != nil {
			return err
		}
		grandchildPID, err = readManagedProcessPID(fixture.grandchildPIDPath)
		return err
	}).Should(gomega.Succeed())
	gomega.Expect(directPID).To(gomega.BeNumerically(">", 1))
	gomega.Expect(grandchildPID).To(gomega.BeNumerically(">", 1))
	gomega.Expect(grandchildPID).NotTo(gomega.Equal(directPID))
	gomega.Expect(managedProcessAlive(directPID)).To(gomega.BeTrue())
	gomega.Expect(managedProcessAlive(grandchildPID)).To(gomega.BeTrue())
	directProcessGroup, err := syscall.Getpgid(directPID)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	grandchildProcessGroup, err := syscall.Getpgid(grandchildPID)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(directProcessGroup).To(gomega.Equal(directPID))
	gomega.Expect(grandchildProcessGroup).To(gomega.Equal(directProcessGroup))

	return directPID, grandchildPID
}

func (fixture managedProcessTreeFixture) waitUntilRecorded(ctx context.Context) (int, int) {
	ginkgo.GinkgoHelper()

	gomega.Eventually(ctx, func() bool {
		_, err := os.Stat(fixture.readyPath)
		return err == nil
	}).Should(gomega.BeTrue())

	var directPID int
	var grandchildPID int
	gomega.Eventually(ctx, func() error {
		var err error
		directPID, err = readManagedProcessPID(fixture.directPIDPath)
		if err != nil {
			return err
		}
		grandchildPID, err = readManagedProcessPID(fixture.grandchildPIDPath)
		return err
	}).Should(gomega.Succeed())
	gomega.Expect(directPID).To(gomega.BeNumerically(">", 1))
	gomega.Expect(grandchildPID).To(gomega.BeNumerically(">", 1))
	gomega.Expect(grandchildPID).NotTo(gomega.Equal(directPID))

	return directPID, grandchildPID
}

func (fixture managedProcessTreeFixture) waitUntilListenerReady(ctx context.Context) string {
	ginkgo.GinkgoHelper()

	var address string
	gomega.Eventually(ctx, func() error {
		body, err := os.ReadFile(fixture.listenerPath)
		if err != nil {
			return err
		}
		address = strings.TrimSpace(string(body))
		if address == "" {
			return errors.New("managed listener address is empty")
		}

		return nil
	}).Should(gomega.Succeed())

	return address
}

func readManagedProcessPID(path string) (int, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(body)))
	if err != nil {
		return 0, err
	}
	if pid <= 1 {
		return 0, errors.New("managed process PID must be greater than one")
	}

	return pid, nil
}

func expectManagedProcessGoneImmediately(pid int) {
	ginkgo.GinkgoHelper()
	gomega.Expect(managedProcessAlive(pid)).To(gomega.BeFalse())
}

func managedProcessAlive(pid int) bool {
	return managedProcessActiveForTest(pid)
}

func deferManagedProcessCleanup(pids ...int) {
	ginkgo.GinkgoHelper()
	ginkgo.DeferCleanup(func() {
		for _, pid := range pids {
			if pid > 1 {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
}
