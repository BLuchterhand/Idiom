//go:build darwin || linux

package commandexec

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/creack/pty"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
)

const foregroundJobControlPrompt = "idiom-pty-prompt> "

var _ = ginkgo.Describe(
	"Foreground terminal job control",
	ginkgo.Label("development-scenario", "e2e", "integration", "smoke", "race-contract"),
	func() {
		ginkgo.It("returns terminal control to the shell across stop and foreground resume", ginkgo.SpecTimeout(managedProcessTreeSpecTimeout), func(ctx ginkgo.SpecContext) {
			root := ginkgo.GinkgoT().TempDir()
			childPIDPath := filepath.Join(root, "foreground-child.pid")
			backgroundChildPIDPath := filepath.Join(root, "background-child.pid")
			testBinary, err := filepath.Abs(os.Args[0])
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			shell := exec.Command("/bin/sh", "-i")
			shell.Env = envWithOverrides(os.Environ(), []string{
				"ENV=/dev/null",
				"PS1=" + foregroundJobControlPrompt,
				"PS2=idiom-pty-continue> ",
			})
			terminal, err := pty.Start(shell)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			shellPID := shell.Process.Pid
			shellPGID, err := syscall.Getpgid(shellPID)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(shellPGID).To(gomega.Equal(shellPID))

			output := gbytes.BufferReader(terminal)
			shellDone := make(chan struct{})
			var shellWaitErr error
			go func() {
				shellWaitErr = shell.Wait()
				close(shellDone)
			}()

			childExited := false
			backgroundChildExited := false
			shellExited := false
			ginkgo.DeferCleanup(func() {
				if !childExited {
					if childPID, readErr := readManagedProcessPID(childPIDPath); readErr == nil {
						_ = syscall.Kill(-childPID, syscall.SIGKILL)
					}
				}
				if !backgroundChildExited {
					if childPID, readErr := readManagedProcessPID(backgroundChildPIDPath); readErr == nil {
						_ = syscall.Kill(-childPID, syscall.SIGKILL)
					}
				}
				if !shellExited {
					_ = syscall.Kill(-shellPGID, syscall.SIGKILL)
					<-shellDone
				}
				_ = terminal.Close()
			})

			expectForegroundPTYOutput(ctx, output, foregroundJobControlPrompt)
			writeForegroundPTY(terminal, "set -m\n")
			expectForegroundPTYOutput(ctx, output, foregroundJobControlPrompt)

			helperCommand := fmt.Sprintf(
				"GO_WANT_COMMAND_EXEC_HELPER=1 %s -test.run=^TestCommandExecHelperProcess$ -- foreground-job-wrapper %s\n",
				quoteForegroundShellArg(testBinary),
				quoteForegroundShellArg(childPIDPath),
			)
			writeForegroundPTY(terminal, helperCommand)
			expectForegroundPTYOutput(ctx, output, "child-ready")

			var childPID int
			gomega.Eventually(ctx, func() error {
				var readErr error
				childPID, readErr = readManagedProcessPID(childPIDPath)
				return readErr
			}).Should(gomega.Succeed())
			childPGID, err := syscall.Getpgid(childPID)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(childPGID).To(gomega.Equal(childPID))

			writeForegroundPTY(terminal, "before-stop\n")
			expectForegroundPTYOutput(ctx, output, "child-input:before-stop")

			_, err = terminal.Write([]byte{26})
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			expectForegroundPTYOutput(ctx, output, foregroundJobControlPrompt)

			writeForegroundPTY(terminal, "printf 'shell-control:%s\\n' after-stop\n")
			expectForegroundPTYOutput(ctx, output, "shell-control:after-stop")
			expectForegroundPTYOutput(ctx, output, foregroundJobControlPrompt)

			writeForegroundPTY(terminal, "fg\n")
			writeForegroundPTY(terminal, "after-resume\n")
			expectForegroundPTYOutput(ctx, output, "child-input:after-resume")

			writeForegroundPTY(terminal, "exit\n")
			expectForegroundPTYOutput(ctx, output, "child-input:exit")
			expectForegroundPTYOutput(ctx, output, "wrapper-exit")
			expectForegroundPTYOutput(ctx, output, foregroundJobControlPrompt)
			expectManagedProcessGoneImmediately(childPID)
			childExited = true

			writeForegroundPTY(terminal, "printf 'shell-control:%s\\n' final\n")
			expectForegroundPTYOutput(ctx, output, "shell-control:final")
			expectForegroundPTYOutput(ctx, output, foregroundJobControlPrompt)

			backgroundHelperCommand := fmt.Sprintf(
				"GO_WANT_COMMAND_EXEC_HELPER=1 %s -test.run=^TestCommandExecHelperProcess$ -- foreground-job-wrapper %s &\n",
				quoteForegroundShellArg(testBinary),
				quoteForegroundShellArg(backgroundChildPIDPath),
			)
			writeForegroundPTY(terminal, backgroundHelperCommand)
			expectForegroundPTYOutput(ctx, output, "child-ready")

			var backgroundChildPID int
			gomega.Eventually(ctx, func() error {
				var readErr error
				backgroundChildPID, readErr = readManagedProcessPID(backgroundChildPIDPath)
				return readErr
			}).Should(gomega.Succeed())
			writeForegroundPTY(terminal, "printf 'background-shell-control\\n'\n")
			expectForegroundPTYOutput(ctx, output, "background-shell-control")
			expectForegroundPTYOutput(ctx, output, foregroundJobControlPrompt)

			writeForegroundPTY(terminal, "fg\n")
			writeForegroundPTY(terminal, "background-resumed\n")
			expectForegroundPTYOutput(ctx, output, "child-input:background-resumed")
			writeForegroundPTY(terminal, "exit\n")
			expectForegroundPTYOutput(ctx, output, "child-input:exit")
			expectForegroundPTYOutput(ctx, output, "wrapper-exit")
			expectForegroundPTYOutput(ctx, output, foregroundJobControlPrompt)
			expectManagedProcessGoneImmediately(backgroundChildPID)
			backgroundChildExited = true

			writeForegroundPTY(terminal, "exit\n")
			gomega.Eventually(shellDone).WithContext(ctx).Should(gomega.BeClosed())
			gomega.Expect(shellWaitErr).NotTo(gomega.HaveOccurred())
			shellExited = true
		})
	},
)

func expectForegroundPTYOutput(ctx ginkgo.SpecContext, output *gbytes.Buffer, literal string) {
	ginkgo.GinkgoHelper()
	gomega.Eventually(output).WithContext(ctx).Should(gbytes.Say(regexp.QuoteMeta(literal)))
}

func writeForegroundPTY(terminal *os.File, input string) {
	ginkgo.GinkgoHelper()
	_, err := io.WriteString(terminal, input)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
}

func quoteForegroundShellArg(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
