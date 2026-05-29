package root
import (
	"bytes"
	"testing"
)

func TestRunMainRoutesRecoverCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runMain([]string{"recover", "inspect", "--json"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected recover usage failure 2, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestRecoverTelemetryNameIncludesSubcommand(t *testing.T) {
	if got := commandTelemetryName([]string{"recover", "inspect"}); got != "recover:inspect" {
		t.Fatalf("telemetry command=%q", got)
	}
}
