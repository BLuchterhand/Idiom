package root

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunMainRoutesCodingRulesCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := runMain([]string{"coding-rules", "--help"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected coding-rules help to exit 0, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "idiom coding-rules check") {
		t.Fatalf("expected coding-rules usage, got stderr=%s", stderr.String())
	}
}
