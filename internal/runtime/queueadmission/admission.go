package queueadmission

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	queuecontract "idiom/internal/contracts/queue"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/platform/jsonevents"
	"idiom/internal/runtime/runner"
)

const defaultQueueAdmissionWaitTimeout = 30 * time.Minute

type Options struct {
	Command        string
	EventPrefix    queuecontract.CommandEventPrefix
	TelemetryEvent string
	Label          string
	Client         string
	JSONEvents     bool
	WaitTimeout    time.Duration
	Mode           queuecontract.AdmissionMode
}

type Result struct {
	Allowed bool
	State   queuecontract.AdmissionState
	Pool    string
	Reason  string
	Err     error
	Release func()
}

func Acquire(cfg config.Config, opts Options, rec *telemetry.Recorder, stdout io.Writer, stderr io.Writer) Result {
	pool := strings.TrimSpace(os.Getenv("IDIOM_QUEUE_POOL"))
	if pool == "" {
		pool = runner.PoolForModelAlias(cfg.LlamaServer.Model)
	}

	if pool == "" {
		emit(rec, stdout, stderr, opts, queuecontract.AdmissionStateDisabled, "", 0, string(queuecontract.ReasonClassPoolDisabled), false)

		return Result{Allowed: true, State: queuecontract.AdmissionStateDisabled, Reason: string(queuecontract.ReasonClassPoolDisabled), Release: func() {}}
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
	ctx, cancel := context.WithTimeout(context.Background(), queueAdmissionWaitTimeout(opts))
	defer cancel()

	result := runner.MaybeAcquire(ctx, socket, pool, label, client, stderr)
	waitMillis := time.Since(startedAt).Milliseconds()

	state := result.State
	if state == "" {
		state = queuecontract.AdmissionStateError
	}

	degraded := state == queuecontract.AdmissionStateUnavailable ||
		state == queuecontract.AdmissionStateDegraded ||
		state == queuecontract.AdmissionStateNestedCrossPool ||
		state == queuecontract.AdmissionStateError
	emit(rec, stdout, stderr, opts, state, pool, waitMillis, result.Reason, degraded)

	required := queuecontract.NormalizeAdmissionMode(opts.Mode) == queuecontract.AdmissionModeRequired
	allowed := !required || queuecontract.AdmissionStateAllowsDispatch(state)
	if result.Err != nil {
		if required {
			fmt.Fprintf(stderr, "[idiom queue %s] acquire failed: %v - queue admission is required\n", pool, result.Err)
		} else {
			fmt.Fprintf(stderr, "[idiom queue %s] acquire failed: %v - proceeding un-gated\n", pool, result.Err)
		}
	}

	if result.Release == nil {
		return Result{Allowed: allowed, State: state, Pool: pool, Reason: result.Reason, Err: result.Err, Release: func() {}}
	}

	return Result{Allowed: allowed, State: state, Pool: pool, Reason: result.Reason, Err: result.Err, Release: result.Release}
}

func queueAdmissionWaitTimeout(opts Options) time.Duration {
	if opts.WaitTimeout > 0 {
		return opts.WaitTimeout
	}
	return defaultQueueAdmissionWaitTimeout
}

func emit(rec *telemetry.Recorder, stdout io.Writer, stderr io.Writer, opts Options, state queuecontract.AdmissionState, pool string, waitMillis int64, reason string, degraded bool) {
	fields := map[string]any{
		"state":        string(state),
		"pool":         pool,
		"wait_ms":      waitMillis,
		"degraded":     degraded,
		"reason_class": string(queuecontract.ClassifyReason(reason)),
	}
	if strings.TrimSpace(opts.Label) != "" {
		fields["label"] = strings.TrimSpace(opts.Label)
	}

	prefix := opts.EventPrefix
	if prefix == "" {
		prefix = queuecontract.CommandEventPrefix("idiom." + strings.TrimSpace(opts.Command))
	}
	if err := jsonevents.Emit(stdout, opts.JSONEvents, queuecontract.CommandAdmissionEventName(prefix, queuecontract.AdmissionEventName(state)), fields); err != nil && stderr != nil {
		fmt.Fprintf(stderr, "queue admission: write json event: %v\n", err)
	}

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
