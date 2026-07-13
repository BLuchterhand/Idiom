package lane

import (
	"encoding/json"
	"fmt"
	gomega "github.com/onsi/gomega"
	"os"
	"path/filepath"
	"strings"
)

func cliAssetPath(elem ...string) string {
	parts := append([]string{"..", "..", "cliassets"}, elem...)
	return filepath.Join(parts...)
}

func laneTestRepoRoot() string {

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

func stageDoctorBackends(root string) {

	path := filepath.Join(root, ".idiom", "backends.json")
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())

	body := `{
  "default_profile": "local",
  "profiles": {
    "local": {
      "models": [
        {"alias": "planner", "binary": "mlx_lm.server", "model_path": "/models/planner", "port": 47780, "roles": ["planner", "classifier"]},
        {"alias": "coder", "binary": "mlx_lm.server", "model_path": "/models/coder", "port": 47781, "roles": ["executor"]}
      ],
      "proxy": {
        "default_planner": "planner",
        "default_executor": "coder",
        "default_classifier": "planner"
      }
    }
  }
}`
	gomega.Expect(os.WriteFile(path, []byte(body), 0o644)).To(gomega.Succeed())

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
			if fmt.Sprint(item) == want {
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
