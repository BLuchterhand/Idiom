package planexecute

import (
	"fmt"
	"io"
	"os"
	"strings"

	"idiom/internal/config"
	"idiom/internal/queueadmission"
	"idiom/internal/telemetry"
)

type QueueAcquireOptions struct {
	JSONEvents bool
	Resume     string
	SessionID  string
}

// AcquireQueueSlot applies the same best-effort queue admission contract used
// by the pre-migration CLI path. If no queue pool can be resolved, or if the
// daemon is unavailable, plan-execute continues un-gated and emits diagnostic
// JSON/telemetry when requested.
func AcquireQueueSlot(cfg config.Config, opts QueueAcquireOptions, rec *telemetry.Recorder, stdout io.Writer, stderr io.Writer) func() {
	label := strings.TrimSpace(opts.Resume)
	if label == "" {
		label = "plan-execute:" + strings.TrimSpace(opts.SessionID)
	}
	client := fmt.Sprintf("idiom-plan-execute/%d", os.Getpid())
	return queueadmission.Acquire(cfg, queueadmission.Options{
		Command:        "plan_execute",
		EventPrefix:    "idiom.plan_execute",
		TelemetryEvent: "plan_execute_queue",
		Label:          label,
		Client:         client,
		JSONEvents:     opts.JSONEvents,
	}, rec, stdout, stderr)
}
