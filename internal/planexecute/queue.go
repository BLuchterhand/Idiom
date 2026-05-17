package planexecute

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"idiom/internal/config"
	"idiom/internal/runner"
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
	pool := strings.TrimSpace(os.Getenv("IDIOM_QUEUE_POOL"))
	if pool == "" {
		pool = runner.PoolForModelAlias(cfg.LlamaServer.Model)
	}
	if pool == "" {
		return func() {}
	}
	socket := strings.TrimSpace(os.Getenv("IDIOM_QUEUE_SOCKET"))
	label := strings.TrimSpace(opts.Resume)
	if label == "" {
		label = "plan-execute:" + strings.TrimSpace(opts.SessionID)
	}
	client := fmt.Sprintf("idiom-plan-execute/%d", os.Getpid())
	startedAt := time.Now()
	result := runner.MaybeAcquireWithResult(context.Background(), socket, pool, label, client, stderr)
	waitMillis := time.Since(startedAt).Milliseconds()
	if result.Err != nil {
		fmt.Fprintf(stderr, "[idiom queue %s] acquire failed: %v - proceeding un-gated\n", pool, result.Err)
	}
	state := string(result.State)
	if state == "" {
		state = "error"
	}
	reason := result.Reason
	degraded := state == "unavailable" || state == "degraded" || state == "error"
	fields := map[string]any{
		"state":        state,
		"pool":         pool,
		"label":        label,
		"wait_ms":      waitMillis,
		"degraded":     degraded,
		"reason_class": planExecuteQueueReasonClass(reason),
	}
	logCLIPhase(rec, "plan_execute_queue", fields)
	emitPlanExecuteJSONEvent(stdout, opts.JSONEvents, planExecuteQueueEventName(state), fields)
	if result.Release == nil {
		return func() {}
	}
	return result.Release
}

func planExecuteQueueEventName(state string) string {
	switch state {
	case "acquired":
		return "queue_acquired"
	case "unavailable":
		return "queue_unavailable"
	case "disabled":
		return "queue_disabled"
	case "degraded":
		return "queue_degraded"
	default:
		return "queue_error"
	}
}

func planExecuteQueueReasonClass(reason string) string {
	reason = strings.ToLower(strings.TrimSpace(reason))
	switch {
	case reason == "":
		return ""
	case strings.Contains(reason, "pool_disabled"):
		return "pool_disabled"
	case strings.Contains(reason, "nested_same_pool"):
		return "nested_same_pool"
	case strings.Contains(reason, "nested_cross_pool"):
		return "nested_cross_pool"
	case strings.Contains(reason, "unknown pool"):
		return "unknown_pool"
	case strings.Contains(reason, "daemon_unreachable"), strings.Contains(reason, "unreachable"), strings.Contains(reason, "connection refused"):
		return "daemon_unreachable"
	case strings.Contains(reason, "context canceled"), strings.Contains(reason, "deadline"):
		return "timeout_or_canceled"
	default:
		return "queue_error"
	}
}
