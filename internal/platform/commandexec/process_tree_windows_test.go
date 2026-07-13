//go:build windows

package commandexec

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"golang.org/x/sys/windows"
)

const managedProcessTreeSpecTimeout = 10 * time.Second

type managedProcessTreeFixture struct {
	directPIDPath     string
	grandchildPIDPath string
	readyPath         string
	listenerPath      string
}

var _ = ginkgo.Describe(
	"Managed process-tree lifecycle on Windows",
	ginkgo.Label("development-scenario", "e2e", "integration", "smoke", "race-contract"),
	func() {
		ginkgo.It("proves job quiescence while direct process handles remain open", ginkgo.SpecTimeout(managedProcessTreeSpecTimeout), func(ctx ginkgo.SpecContext) {
			cmd, processTree, err := managedCommand(ctx, helperSpec("sleep"))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(cmd.Start()).To(gomega.Succeed())

			attachDoneClosed := false
			waited := false
			ginkgo.DeferCleanup(func() {
				if !attachDoneClosed {
					close(processTree.attachDone)
					attachDoneClosed = true
				}
				_ = processTree.terminate()
				_ = processTree.waitForJobQuiescence()
				if !waited {
					_ = cmd.Wait()
				}
				_ = processTree.close()
			})

			gomega.Expect(processTree.attachSuspendedProcess()).To(gomega.Succeed())
			close(processTree.attachDone)
			attachDoneClosed = true
			gomega.Expect(processTree.terminate()).To(gomega.Succeed())
			gomega.Expect(processTree.waitForJobQuiescence()).To(gomega.Succeed())

			processTree.handlesMu.RLock()
			directHandle := processTree.process
			jobHandle := processTree.job
			processTree.handlesMu.RUnlock()
			gomega.Expect(directHandle).NotTo(gomega.Equal(windows.Handle(0)))
			gomega.Expect(jobHandle).NotTo(gomega.Equal(windows.Handle(0)))
			event, err := windows.WaitForSingleObject(directHandle, 0)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(event).To(gomega.Equal(uint32(windows.WAIT_OBJECT_0)))

			var accounting jobObjectAccountingForTest
			gomega.Expect(windows.QueryInformationJobObject(
				jobHandle,
				windows.JobObjectBasicAccountingInformation,
				uintptr(unsafe.Pointer(&accounting)),
				uint32(unsafe.Sizeof(accounting)),
				nil,
			)).To(gomega.Succeed())
			gomega.Expect(accounting.ActiveProcesses).To(gomega.BeNumerically(">", 0))

			waitErr := cmd.Wait()
			waited = true
			gomega.Expect(waitErr).To(gomega.HaveOccurred())
			gomega.Expect(processTree.close()).To(gomega.Succeed())
		})

		ginkgo.It("falls back to kill-on-close when Job termination fails", ginkgo.SpecTimeout(managedProcessTreeSpecTimeout), func(ctx ginkgo.SpecContext) {
			fixture := newManagedProcessTreeFixture()
			cmd, processTree, err := managedCommand(ctx, fixture.spec())
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			terminationErr := errors.New("injected Job termination failure")
			observerWaiting := make(chan struct{})
			fallbackCloseCalls := 0
			pinnedProcessCount := 0
			processTree.terminateJobObject = func(windows.Handle) error {
				return terminationErr
			}
			processTree.closeJobOnTerminationFailure = func(job windows.Handle) error {
				fallbackCloseCalls++
				pinnedProcessCount = len(processTree.jobProcesses)
				return windows.CloseHandle(job)
			}
			processTree.waitForDirectProcessExitEvent = func(process windows.Handle, milliseconds uint32) (uint32, error) {
				close(observerWaiting)
				return windows.WaitForSingleObject(process, milliseconds)
			}

			started := false
			attachReleased := false
			waited := false
			ginkgo.DeferCleanup(func() {
				if !attachReleased {
					_ = processTree.abortAttach()
				}
				if started {
					_ = processTree.terminate()
					if !waited {
						_ = cmd.Wait()
						waited = true
					}
				}
				_ = processTree.close()
			})

			startErr := cmd.Start()
			started = startErr == nil
			gomega.Expect(startErr).NotTo(gomega.HaveOccurred())
			attachErr := processTree.attach()
			attachReleased = true
			gomega.Expect(attachErr).NotTo(gomega.HaveOccurred())
			gomega.Eventually(ctx, observerWaiting).Should(gomega.BeClosed())
			directPID, grandchildPID := fixture.waitUntilReady(ctx)
			deferManagedProcessCleanup(directPID, grandchildPID)

			gotTerminationErr := processTree.terminate()
			gomega.Expect(errors.Is(gotTerminationErr, terminationErr)).To(gomega.BeTrue())
			teardownErr := processTree.waitBeforeReap()
			gomega.Expect(errors.Is(teardownErr, terminationErr)).To(gomega.BeTrue())
			processTree.handlesMu.RLock()
			fallbackState := processTree.jobTerminationFallback
			jobHandle := processTree.job
			processTree.handlesMu.RUnlock()
			gomega.Expect(fallbackState).To(gomega.Equal(jobTerminationFallbackClosed))
			gomega.Expect(jobHandle).To(gomega.Equal(windows.Handle(0)))

			waitErr := cmd.Wait()
			waited = true
			gomega.Expect(waitErr).To(gomega.HaveOccurred())
			gomega.Expect(processTree.close()).To(gomega.Succeed())
			gomega.Expect(fallbackCloseCalls).To(gomega.Equal(1))
			gomega.Expect(pinnedProcessCount).To(gomega.BeNumerically(">=", 2))
			processTree.handlesMu.RLock()
			processHandle := processTree.process
			jobHandle = processTree.job
			processTree.handlesMu.RUnlock()
			gomega.Expect(processHandle).To(gomega.Equal(windows.Handle(0)))
			gomega.Expect(jobHandle).To(gomega.Equal(windows.Handle(0)))
			expectManagedProcessGoneImmediately(directPID)
			expectManagedProcessGoneImmediately(grandchildPID)
		})

		ginkgo.It("fails visibly and closes retained handles when Job termination and kill-on-close both fail", ginkgo.SpecTimeout(managedProcessTreeSpecTimeout), func(ctx ginkgo.SpecContext) {
			fixture := newManagedProcessTreeFixture()
			cmd, processTree, err := managedCommand(ctx, fixture.spec())
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			terminationErr := errors.New("injected Job termination failure")
			fallbackCloseErr := errors.New("injected Job close failure")
			observerWaiting := make(chan struct{})
			fallbackCloseCalls := 0
			pinnedProcessCount := 0
			processTree.terminateJobObject = func(windows.Handle) error {
				return terminationErr
			}
			processTree.closeJobOnTerminationFailure = func(windows.Handle) error {
				fallbackCloseCalls++
				pinnedProcessCount = len(processTree.jobProcesses)
				return fallbackCloseErr
			}
			processTree.waitForDirectProcessExitEvent = func(process windows.Handle, milliseconds uint32) (uint32, error) {
				close(observerWaiting)
				return windows.WaitForSingleObject(process, milliseconds)
			}

			started := false
			attachReleased := false
			waited := false
			ginkgo.DeferCleanup(func() {
				if !attachReleased {
					_ = processTree.abortAttach()
				}
				if started {
					_ = processTree.terminate()
					if !waited {
						_ = cmd.Wait()
						waited = true
					}
				}
				_ = processTree.close()
			})

			startErr := cmd.Start()
			started = startErr == nil
			gomega.Expect(startErr).NotTo(gomega.HaveOccurred())
			attachErr := processTree.attach()
			attachReleased = true
			gomega.Expect(attachErr).NotTo(gomega.HaveOccurred())
			gomega.Eventually(ctx, observerWaiting).Should(gomega.BeClosed())
			directPID, grandchildPID := fixture.waitUntilReady(ctx)
			deferManagedProcessCleanup(directPID, grandchildPID)
			grandchildHandle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(grandchildPID))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			ginkgo.DeferCleanup(func() {
				_ = windows.CloseHandle(grandchildHandle)
			})

			gotTerminationErr := processTree.terminate()
			gomega.Expect(errors.Is(gotTerminationErr, terminationErr)).To(gomega.BeTrue())
			gomega.Expect(errors.Is(gotTerminationErr, fallbackCloseErr)).To(gomega.BeTrue())
			teardownErr := processTree.waitBeforeReap()
			gomega.Expect(errors.Is(teardownErr, terminationErr)).To(gomega.BeTrue())
			gomega.Expect(errors.Is(teardownErr, fallbackCloseErr)).To(gomega.BeTrue())
			processTree.handlesMu.RLock()
			fallbackState := processTree.jobTerminationFallback
			jobHandle := processTree.job
			processTree.handlesMu.RUnlock()
			gomega.Expect(fallbackState).To(gomega.Equal(jobTerminationFallbackCloseFailed))
			gomega.Expect(jobHandle).NotTo(gomega.Equal(windows.Handle(0)))
			grandchildAlive, err := windowsManagedProcessAlive(grandchildPID)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(grandchildAlive).To(gomega.BeTrue())

			waitErr := cmd.Wait()
			waited = true
			gomega.Expect(waitErr).To(gomega.HaveOccurred())
			gomega.Expect(processTree.close()).To(gomega.Succeed())
			gomega.Expect(fallbackCloseCalls).To(gomega.Equal(1))
			gomega.Expect(pinnedProcessCount).To(gomega.BeNumerically(">=", 2))
			processTree.handlesMu.RLock()
			processHandle := processTree.process
			jobHandle = processTree.job
			processTree.handlesMu.RUnlock()
			gomega.Expect(processHandle).To(gomega.Equal(windows.Handle(0)))
			gomega.Expect(jobHandle).To(gomega.Equal(windows.Handle(0)))
			event, err := windows.WaitForSingleObject(grandchildHandle, windows.INFINITE)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(event).To(gomega.Equal(uint32(windows.WAIT_OBJECT_0)))
		})

		ginkgo.It("drains a post-Start attachment failure before returning", ginkgo.SpecTimeout(managedProcessTreeSpecTimeout), func(ctx ginkgo.SpecContext) {
			cmd, processTree, err := managedCommand(ctx, helperSpec("sleep"))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(cmd.Start()).To(gomega.Succeed())
			pid := cmd.Process.Pid
			deferManagedProcessCleanup(pid)
			gomega.Expect(cmd.Process.Kill()).To(gomega.Succeed())

			gomega.Expect(processTree.attach()).To(gomega.HaveOccurred())
			gomega.Expect(processTree.waitBeforeReap()).To(gomega.Succeed())
			gomega.Expect(cmd.Wait()).To(gomega.HaveOccurred())
			gomega.Expect(processTree.close()).To(gomega.Succeed())
			expectManagedProcessGoneImmediately(pid)
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

		ginkgo.It("drains a naturally-exited Run job whose descendant inherits output handles", ginkgo.SpecTimeout(managedProcessTreeSpecTimeout), func(ctx ginkgo.SpecContext) {
			fixture := newManagedProcessTreeFixture()
			result := make(chan Result, 1)
			go func() {
				result <- Run(ctx, fixture.naturalExitSpec())
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

		ginkgo.It("drains a naturally-exited Start job before an inherited handle can block Wait", ginkgo.SpecTimeout(managedProcessTreeSpecTimeout), func(ctx ginkgo.SpecContext) {
			fixture := newManagedProcessTreeFixture()
			process, err := Start(ctx, fixture.naturalExitSpec())
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			ginkgo.DeferCleanup(process.Close)

			directPID, grandchildPID := fixture.waitUntilRecorded(ctx)
			deferManagedProcessCleanup(directPID, grandchildPID)
			gomega.Expect(process.wait()).To(gomega.Succeed())
			stderr, truncated := process.Stderr()
			gomega.Expect(stderr).To(gomega.ContainSubstring("natural-stderr"))
			gomega.Expect(truncated).To(gomega.BeFalse())
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

type jobObjectAccountingForTest struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

func newManagedProcessTreeFixture() managedProcessTreeFixture {
	ginkgo.GinkgoHelper()

	root := ginkgo.GinkgoT().TempDir()
	return managedProcessTreeFixture{
		directPIDPath:     filepath.Join(root, "direct.pid"),
		grandchildPIDPath: filepath.Join(root, "grandchild.pid"),
		readyPath:         filepath.Join(root, "ready"),
		listenerPath:      filepath.Join(root, "listener"),
	}
}

func (fixture managedProcessTreeFixture) spec() Spec {
	return helperSpec(
		"managed-tree-parent",
		fixture.directPIDPath,
		fixture.grandchildPIDPath,
		fixture.readyPath,
	)
}

func (fixture managedProcessTreeFixture) naturalExitSpec() Spec {
	return helperSpec(
		"managed-tree-natural-parent",
		fixture.directPIDPath,
		fixture.grandchildPIDPath,
		fixture.readyPath,
	)
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
	directAlive, err := windowsManagedProcessAlive(directPID)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(directAlive).To(gomega.BeTrue())
	grandchildAlive, err := windowsManagedProcessAlive(grandchildPID)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(grandchildAlive).To(gomega.BeTrue())

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
	alive, err := windowsManagedProcessAlive(pid)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(alive).To(gomega.BeFalse())
}

func windowsManagedProcessAlive(pid int) (bool, error) {
	if pid <= 1 {
		return false, nil
	}

	process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open process %d: %w", pid, err)
	}

	event, err := windows.WaitForSingleObject(process, 0)
	if err != nil {
		_ = windows.CloseHandle(process)
		return false, fmt.Errorf("observe process %d: %w", pid, err)
	}
	if err := windows.CloseHandle(process); err != nil {
		return false, fmt.Errorf("close process %d observation handle: %w", pid, err)
	}
	switch event {
	case uint32(windows.WAIT_TIMEOUT):
		return true, nil
	case windows.WAIT_OBJECT_0:
		return false, nil
	default:
		return false, fmt.Errorf("observe process %d returned event %#x", pid, event)
	}
}

func deferManagedProcessCleanup(pids ...int) {
	ginkgo.GinkgoHelper()
	ginkgo.DeferCleanup(func() {
		for _, pid := range pids {
			if pid <= 1 {
				continue
			}
			process, err := os.FindProcess(pid)
			if err == nil {
				_ = process.Kill()
				_ = process.Release()
			}
		}
	})
}
