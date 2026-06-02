package ranker

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRollbackRequiresEvent(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"rollback"}, &stdout, &stderr, nil, Deps{})
	if code != 2 {
		t.Fatalf("expected usage exit for missing event, got %d", code)
	}
	if !strings.Contains(stderr.String(), "--event is required") {
		t.Fatalf("missing event error not surfaced: %s", stderr.String())
	}
}

func TestRunShowEmitsJSON(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf(`{"workspace":{"root":%q}}`, root)), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"show", "--config", configPath, "--json"}, &stdout, &stderr, nil, Deps{})
	if code != 0 {
		t.Fatalf("show failed code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	for _, marker := range []string{`"workspace_root"`, `"weights"`, `"log_summary"`} {
		if !strings.Contains(stdout.String(), marker) {
			t.Fatalf("show JSON missing %s: %s", marker, stdout.String())
		}
	}
}
