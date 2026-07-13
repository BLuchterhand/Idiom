package sidecarvalidation

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

func writeMainConfig() string {
	ginkgo.GinkgoHelper()

	root := ginkgo.GinkgoT().TempDir()
	path := filepath.Join(root, "config.json")
	gomega.Expect(os.WriteFile(path, []byte("{}\n"), 0o644)).To(gomega.Succeed())

	return path
}

func runMain(args []string, stdout io.Writer, stderr io.Writer) int {
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
		fmt.Fprintf(stderr, "unsupported proof command %q\n", args[0])

		return 2
	}
}

func writeMainConfigRoot(root string) string {
	ginkgo.GinkgoHelper()

	path := filepath.Join(ginkgo.GinkgoT().TempDir(), "config.json")
	configJSON := fmt.Sprintf(`{"workspace":{"root":%q,"cwd":%q}}`, root, root)
	gomega.Expect(os.WriteFile(path, []byte(configJSON+"\n"), 0o644)).To(gomega.Succeed())

	return path
}

func stageDoctorConfigWithBudget(root string, ctxSize int, maxTokens int) {
	ginkgo.GinkgoHelper()

	cfg := filepath.Join(root, ".idiom", "config.json")
	gomega.Expect(os.MkdirAll(filepath.Dir(cfg), 0o755)).To(gomega.Succeed())

	body := fmt.Sprintf(`{
  "workspace": {"root": %q, "cwd": %q},
  "llama_server": {"ctx_size": %d},
  "chat": {"max_tokens": %d}
}`, root, root, ctxSize, maxTokens)
	gomega.Expect(os.WriteFile(cfg, []byte(body), 0o644)).To(gomega.Succeed())
}
