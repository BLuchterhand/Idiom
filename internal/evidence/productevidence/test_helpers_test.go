package productevidence

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	cmdconfig "idiom/internal/surfaces/cli/config"
	localopscmd "idiom/internal/surfaces/cli/localops"
	toolcmd "idiom/internal/surfaces/cli/tool"
)

var p711TestDemoDependencies = Dependencies{
	RunMainNoTelemetry:  p711RunMainNoTelemetry,
	DemoCommandRunnerID: "productevidence-test:cli-adapter",
}

func p711RunMainNoTelemetry(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "missing command")

		return 2
	}

	switch args[0] {
	case "tool":
		return toolcmd.RunWithTelemetry(args[1:], stdout, stderr, nil)
	case "local-ops":
		return localopscmd.RunMainWithTelemetry(args[1:], stdout, stderr, nil, localopscmd.Dependencies{
			Version:    "test",
			Commit:     "test",
			LoadConfig: cmdconfig.Load,
		})
	default:
		fmt.Fprintf(stderr, "unsupported product evidence demo command %q\n", args[0])

		return 2
	}
}

func p711TestRepoRoot() string {
	ginkgo.GinkgoHelper()

	dir, err := os.Getwd()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("get cwd: %v", err))

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		gomega.Expect(parent).NotTo(gomega.Equal(dir), fmt.Sprintf("repo root not found from cwd"))

		dir = parent
	}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}

	return false
}
