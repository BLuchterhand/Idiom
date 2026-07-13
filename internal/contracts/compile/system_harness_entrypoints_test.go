package contractcompile_test

import (
	"os"
	"path/filepath"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = It("routes assertion-bearing system harnesses through their Ginkgo package", Label("architecture", "system-harness-architecture"), func() {
	automationPaths := []string{
		"Makefile",
		".github/workflows/ci.yml",
		".github/workflows/release.yml",
	}
	fixturePaths := []string{
		"scripts/test-discovery-tools-e2e.py",
		"scripts/test-discovery-tools-e2e.sh",
		"scripts/test-install-dependencies.ps1",
		"scripts/test-install-dependencies.sh",
		"scripts/test-install-docker.sh",
		"scripts/test-install-entrypoint.sh",
		"scripts/test-install-macos.sh",
		"scripts/test-install-queue-daemon.sh",
		"scripts/test-install-tools-local.sh",
		"scripts/test-post-install-harness.py",
	}

	root := systemHarnessRepositoryRoot()
	for _, path := range automationPaths {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		Expect(err).NotTo(HaveOccurred(), "read automation entrypoint %s", path)
		Expect(string(contents)).To(
			ContainSubstring("./internal/systemtests"),
			"%s must route external harness assertions through internal/systemtests",
			path,
		)
		Expect(string(contents)).To(
			ContainSubstring("--tags=system_test"),
			"%s must compile the build-tagged system harness package",
			path,
		)
		for _, fixturePath := range fixturePaths {
			Expect(string(contents)).NotTo(
				ContainSubstring(fixturePath),
				"%s must select internal/systemtests instead of invoking %s directly",
				path,
				fixturePath,
			)
		}
	}
})

func systemHarnessRepositoryRoot() string {
	_, sourcePath, _, ok := runtime.Caller(0)
	Expect(ok).To(BeTrue(), "resolve system-harness guard source path")
	return filepath.Clean(filepath.Join(filepath.Dir(sourcePath), "..", "..", ".."))
}
