package queueadmission

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"idiom/internal/config"
	"idiom/internal/runner"
	"idiom/internal/telemetry"
)

type Options struct {
	Command        string
	EventPrefix    string
	TelemetryEvent string
	Label          string
	Client         string
	JSONEvents     bool
}

func Acquire(cfg config.Config, opts Options, rec *telemetry.Recorder, stdout io.Writer, stderr io.Writer) func() {
	pool := strings.TrimSpace(os.Getenv("IDIOM_QUEUE_POOL"))
	if pool == "" {
		pool = runner.PoolForModelAlias(cfg.LlamaServer.Model)
	}
	if pool == "" {
		emit(rec, stdout, opts, "disabled", "", 0, "pool_disabled", false)
		return func() {}
	}
	socket := strings.TrimSpace(os.Getenv("IDIOM_QUEUE_SOCKET"))
	label := strings.TrimSpace(opts.Label)
	client := strings.TrimSpace(opts.Client)
	if client == "" {
		command := strings.TrimSpace(opts.Command)
		if command == "" {
			command = "idiom"
		}
		client = fmt.Sprintf("%s/%d", command, os.Getpid())
	}
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
	degraded := state == "unavailable" || state == "degraded" || state == "error"
	emit(rec, stdout, opts, state, pool, waitMillis, result.Reason, degraded)
	if result.Release == nil {
		return func() {}
	}
	return result.Release
}

func emit(rec *telemetry.Recorder, stdout io.Writer, opts Options, state string, pool string, waitMillis int64, reason string, degraded bool) {
	fields := map[string]any{
		"state":        state,
		"pool":         pool,
		"wait_ms":      waitMillis,
		"degraded":     degraded,
		"reason_class": ReasonClass(reason),
	}
	if strings.TrimSpace(opts.Label) != "" {
		fields["label"] = strings.TrimSpace(opts.Label)
	}
	emitJSONEvent(stdout, opts, EventName(state), fields)
	if rec != nil {
		eventType := strings.TrimSpace(opts.TelemetryEvent)
		if eventType == "" {
			eventType = strings.TrimSpace(opts.Command) + "_queue"
		}
		if eventType != "_queue" {
			_ = rec.Log(eventType, fields)
		}
	}
}

func EventName(state string) string {
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

func emitJSONEvent(stdout io.Writer, opts Options, eventType string, fields map[string]any) {
	if !opts.JSONEvents {
		return
	}
	prefix := strings.TrimSpace(opts.EventPrefix)
	if prefix == "" {
		prefix = "idiom." + strings.TrimSpace(opts.Command)
	}
	payload := map[string]any{
		"type": prefix + "." + eventType,
		"ts":   time.Now().UTC().Format(time.RFC3339Nano),
	}
	for key, value := range fields {
		payload[key] = value
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fmt.Fprintln(stdout, string(body))
}

func ReasonClass(reason string) string {
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
