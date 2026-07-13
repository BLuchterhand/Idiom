package root

import (
	"bytes"
	"runtime"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

// TestRunVersionMainPrintsVersion verifies the default unset-ldflags
// build prints "version: dev" — the development sentinel — plus the
// runtime version line. A go-install or go-build user without
// goreleaser's -ldflags should still get a recognizable answer.
var _ = ginkgo.Describe("Version output", func() {
	ginkgo.It("prints the development version and runtime details", func() {
		var stdout, stderr bytes.Buffer
		gomega.Expect(runVersionMain(nil, &stdout, &stderr)).To(gomega.Equal(0), "stderr=%s", stderr.String())

		gomega.Expect(stdout.String()).To(gomega.And(
			gomega.ContainSubstring("version: "),
			gomega.ContainSubstring("go: "),
			gomega.ContainSubstring(runtime.Version()),
			gomega.ContainSubstring("os/arch: "),
		))
	})

	// TestRunVersionMainHonorsLdflagsInjection verifies that the package-
	// level `version` variable is what gets printed. goreleaser injects
	// this at build time via -ldflags '-X main.version=...', so the
	// build-time override path is the contract this test guards.
	ginkgo.It("honors the version injected by build ldflags", func() {
		original := version
		ginkgo.DeferCleanup(func() { version = original })
		version = "v9.9.9-test"

		var stdout, stderr bytes.Buffer
		gomega.Expect(runVersionMain(nil, &stdout, &stderr)).To(gomega.Equal(0), "stderr=%s", stderr.String())
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("version: v9.9.9-test"))
	})
})
