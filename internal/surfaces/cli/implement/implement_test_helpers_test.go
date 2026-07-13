package implement

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/runtime/runner"
)

func writeMainConfig() string {

	root := ginkgo.GinkgoT().TempDir()
	socket := filepath.Join("/tmp", "idiom-"+strings.NewReplacer("/", "_", " ", "_").Replace(ginkgo.GinkgoT().Name())+".sock")
	_ = os.Remove(socket)
	srv := runner.NewServer(runner.Config{Pools: map[string]runner.PoolConfig{"qwen-35b": {MaxInFlight: 1}}}, socket)
	gomega.Expect(srv.Start()).To(gomega.Succeed())
	ginkgo.DeferCleanup(func() {
		srv.Stop()
		_ = os.Remove(socket)
	})
	ginkgo.GinkgoT().
		Setenv("IDIOM_QUEUE_POOL", "qwen-35b")
	ginkgo.GinkgoT().
		Setenv("IDIOM_QUEUE_SOCKET", socket)

	path := filepath.Join(root, "config.json")
	gomega.Expect(os.WriteFile(path, []byte("{}\n"), 0o644)).To(gomega.Succeed())

	return path
}

func readMainTelemetryRecords(path string) []map[string]any {

	data, err := os.ReadFile(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read telemetry log: %v", err))

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	records := make([]map[string]any, 0, len(lines))

	for _, line := range lines {
		var record map[string]any
		gomega.Expect(json.Unmarshal([]byte(line), &record)).To(gomega.Succeed())

		records = append(records, record)
	}

	return records
}

func jsonEventStringSliceContains(value any, want string) bool {
	switch items := value.(type) {
	case []any:
		for _, item := range items {
			if item == want {
				return true
			}
		}
	case []string:
		for _, item := range items {
			if item == want {
				return true
			}
		}
	}

	return false
}
