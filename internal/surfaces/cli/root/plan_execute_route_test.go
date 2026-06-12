package root

import (
	"bytes"
	"testing"
)

func TestRunMainRoutesPlanExecuteCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := runMain([]string{"plan-execute"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("expected plan-execute usage failure 2, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}
