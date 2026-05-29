package lane
import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/cmdtelemetry"
)

func cliAssetPath(elem ...string) string {
	parts := append([]string{"..", "..", "cliassets"}, elem...)
	return filepath.Join(parts...)
}

func stageDoctorBackends(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, ".idiom", "backends.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir backends dir: %v", err)
	}
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
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("stage backends: %v", err)
	}
}

func commandTelemetryName(args []string) string {
	return cmdtelemetry.CommandName(args)
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

func planExecuteJSONEvents(t *testing.T, stdout string) []map[string]any {
	t.Helper()
	var events []map[string]any
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode JSON event %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

func planExecuteEventByType(events []map[string]any, eventType string) (map[string]any, bool) {
	for _, event := range events {
		if event["type"] == eventType {
			return event, true
		}
	}
	return nil, false
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
