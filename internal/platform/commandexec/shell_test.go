package commandexec

import (
	"runtime"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Shell invocation", func() {
	ginkgo.DescribeTable("uses the configured executable's command protocol",
		func(shell string, mode ShellMode, flags []string) {
			const command = `echo "value with spaces"; exit 7`
			name, args, err := commandArgv(Spec{Mode: mode, Shell: shell, Command: command})
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(name).To(gomega.Equal(shell))
			gomega.Expect(args).To(gomega.Equal(append(flags, command)))
		},
		ginkgo.Entry("POSIX plain", "/bin/sh", ModePlainShell, []string{"-c"}),
		ginkgo.Entry("explicit zsh login", "/bin/zsh", ModeLoginShell, []string{"-lc"}),
		ginkgo.Entry("Unix shell installed on Windows", `C:\Program Files\Git\bin\bash.exe`, ModeLoginShell, []string{"-lc"}),
		ginkgo.Entry("Windows PowerShell plain", "powershell.exe", ModePlainShell, []string{"-NoLogo", "-NonInteractive", "-NoProfile", "-Command"}),
		ginkgo.Entry("PowerShell login profiles", "pwsh", ModeLoginShell, []string{"-NoLogo", "-NonInteractive", "-Command"}),
		ginkgo.Entry("PowerShell executable path", `C:\Program Files\PowerShell\7\PWSH.EXE`, ModePlainShell, []string{"-NoLogo", "-NonInteractive", "-NoProfile", "-Command"}),
		ginkgo.Entry("PowerShell on Unix", "/usr/local/bin/pwsh", ModePlainShell, []string{"-NoLogo", "-NonInteractive", "-NoProfile", "-Command"}),
		ginkgo.Entry("cmd plain", "cmd.exe", ModePlainShell, []string{"/D", "/S", "/C"}),
		ginkgo.Entry("cmd startup commands", `C:\Windows\System32\cmd.exe`, ModeLoginShell, []string{"/S", "/C"}),
	)

	ginkgo.It("uses the platform default for an unspecified shell", func() {
		name, args, err := commandArgv(Spec{Mode: ModePlainShell, Command: "command"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		if runtime.GOOS == "windows" {
			gomega.Expect(name).To(gomega.Equal("powershell.exe"))
			gomega.Expect(args).To(gomega.Equal([]string{"-NoLogo", "-NonInteractive", "-NoProfile", "-Command", "command"}))
		} else {
			gomega.Expect(name).To(gomega.Equal("/bin/sh"))
			gomega.Expect(args).To(gomega.Equal([]string{"-c", "command"}))
		}
	})

	ginkgo.It("rejects an empty command for every shell family", func() {
		for _, shell := range []string{"/bin/sh", "powershell.exe", "cmd.exe"} {
			_, _, err := commandArgv(Spec{Mode: ModePlainShell, Shell: shell, Command: "  "})
			gomega.Expect(err).To(gomega.MatchError("shell command is required"))
		}
	})
})
