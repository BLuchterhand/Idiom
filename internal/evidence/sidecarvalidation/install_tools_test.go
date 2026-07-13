package sidecarvalidation

import (
	"fmt"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"os"
	"path/filepath"
)

var _ = ginkgo.It("install tools scripts expose sidecar only launchers", func() {
	root := sidecarValidationRepoRoot()

	unixScript := readInstallToolFile(root, "install-tools.sh")
	for _, want := range []string{
		"idiom-tool-bin",
		"exec \"$DIR/idiom-tool-bin\" tool \"$@\"",
		"exec \"$DIR/idiom-tool-bin\" mcp serve --stdio --profile discovery \"$@\"",
		"use the Windows installer",
		"list --profile discovery",
	} {
		gomega.Expect(unixScript).To(gomega.ContainSubstring(want), fmt.Sprintf("install-tools.sh missing %q", want))
	}

	gomega.Expect(unixScript).NotTo(gomega.ContainSubstring("${INSTALL_DIR}/idiom\""), fmt.Sprint("tools installer must not expose the full idiom command name"))

	powerShellScript := readInstallToolFile(root, "install-tools.ps1")
	for _, want := range []string{
		"install.ps1",
		`"-Profile", "Tools"`,
		"Programs\\idiom-tools\\bin",
		"ReleaseUrlBase",
		"InstallerArgs",
	} {
		gomega.Expect(powerShellScript).To(gomega.ContainSubstring(want), fmt.Sprintf("install-tools.ps1 missing %q", want))
	}
	for _, forbidden := range []string{
		"Get-FileHash -Algorithm SHA256",
		"Expand-Archive",
		"windows_amd64.zip",
	} {
		gomega.Expect(powerShellScript).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("install-tools.ps1 should delegate install mechanics, still contains %q", forbidden))
	}
})

func readInstallToolFile(root string, rel string) string {
	ginkgo.GinkgoHelper()

	data, err := os.ReadFile(filepath.Join(root, rel))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read %s: %v", rel, err))

	return string(data)
}
