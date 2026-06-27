package implement

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/runtime/runner"
)

func writeMainConfig(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	socket := filepath.Join("/tmp", "idiom-"+strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())+".sock")
	_ = os.Remove(socket)
	srv := runner.NewServer(runner.Config{Pools: map[string]runner.PoolConfig{"qwen-35b": {MaxInFlight: 1}}}, socket)
	if err := srv.Start(); err != nil {
		t.Fatalf("start queue server: %v", err)
	}
	t.Cleanup(func() {
		srv.Stop()
		_ = os.Remove(socket)
	})
	t.Setenv("IDIOM_QUEUE_POOL", "qwen-35b")
	t.Setenv("IDIOM_QUEUE_SOCKET", socket)

	path := filepath.Join(root, "config.json")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return path
}

func readMainTelemetryRecords(t *testing.T, path string) []map[string]any {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read telemetry log: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	records := make([]map[string]any, 0, len(lines))

	for _, line := range lines {
		var record map[string]any

		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("parse telemetry line %q: %v", line, err)
		}

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
