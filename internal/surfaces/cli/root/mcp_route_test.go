package root

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunMainMCPServeCommandExists(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runMain([]string{"mcp", "serve", "--profile", "discovery"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected usage exit 2 without --stdio, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "requires --stdio") && !strings.Contains(stderr.String(), "--stdio") {
		t.Fatalf("expected mcp serve usage diagnostic, got %q", stderr.String())
	}
}
