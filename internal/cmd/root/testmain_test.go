package root

import (
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	telemetryDir, err := os.MkdirTemp("", "idiom-cmd-telemetry-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create test telemetry dir: %v\n", err)
		os.Exit(1)
	}
	commandTelemetryDir = telemetryDir

	code := m.Run()
	_ = os.RemoveAll(telemetryDir)
	os.Exit(code)
}
