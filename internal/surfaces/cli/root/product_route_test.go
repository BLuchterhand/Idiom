package root

import (
	"bytes"
	"testing"
)

func TestProductWorkflowCommandDelegatesToInternalPackage(t *testing.T) {
	var stdout, stderr bytes.Buffer

	if code := runProductMainWithTelemetry([]string{"help"}, &stdout, &stderr, nil); code != 0 {
		t.Fatalf("product help failed with code %d: %s", code, stderr.String())
	}

	if stdout.Len() == 0 {
		t.Fatal("expected product help text on stdout")
	}
}
