package cmdtelemetry

import (
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"idiom/internal/evidence/productevidence"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/localops"
)

type RecorderOptions struct {
	Dir     string
	Version string
}

func OpenRecorder(command string, args []string, opts RecorderOptions) *telemetry.Recorder {
	wd, _ := os.Getwd()

	wd = config.CanonicalizeExistingPath(wd)

	rec, err := telemetry.Open(telemetry.Options{
		Dir:           opts.Dir,
		Command:       command,
		Args:          CommandArgs(args),
		WorkspaceRoot: wd,
		Version:       opts.Version,
	})
	if err != nil {
		return nil
	}

	return rec
}

// SignalResult is the process-exit decision produced after a caught signal has
// finished telemetry closeout. The command root remains the sole owner of the
// process return code; the signal worker never calls os.Exit.
type SignalResult struct {
	Signal     os.Signal
	ExitCode   int
	CloseError error
}

// SignalHandler arbitrates process completion against SIGINT/SIGTERM. Stopping
// the handler is synchronous: after StopAndWait returns, either a signal has
// won and its telemetry closeout is complete, or signal delivery has stopped
// and normal command closeout may proceed.
type SignalHandler struct {
	recorder        *telemetry.Recorder
	signals         chan os.Signal
	stopRequested   chan struct{}
	done            chan struct{}
	stopOnce        sync.Once
	stopSignalsOnce sync.Once
	stopSignals     func()
	result          *SignalResult
}

func InstallSignalHandler(rec *telemetry.Recorder) *SignalHandler {
	if rec == nil {
		done := make(chan struct{})
		close(done)

		return &SignalHandler{done: done}
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	return newSignalHandler(rec, signals, func() { signal.Stop(signals) })
}

func newSignalHandler(rec *telemetry.Recorder, signals chan os.Signal, stopSignals func()) *SignalHandler {
	handler := &SignalHandler{
		recorder:      rec,
		signals:       signals,
		stopRequested: make(chan struct{}),
		done:          make(chan struct{}),
		stopSignals:   stopSignals,
	}

	go handler.run()

	return handler
}

func (h *SignalHandler) run() {
	defer close(h.done)

	select {
	case sig := <-h.signals:
		h.stopSignalDelivery()
		h.recordSignal(sig)
	case <-h.stopRequested:
		// Stop delivery before checking the buffered channel. signal.Stop
		// guarantees no later delivery, so a signal already queued always wins
		// over a concurrent normal return instead of being dropped at random by
		// select arbitration.
		h.stopSignalDelivery()
		select {
		case sig := <-h.signals:
			h.recordSignal(sig)
		default:
		}
	}
}

func (h *SignalHandler) stopSignalDelivery() {
	if h == nil {
		return
	}

	h.stopSignalsOnce.Do(func() {
		if h.stopSignals != nil {
			h.stopSignals()
		}
	})
}

func (h *SignalHandler) recordSignal(sig os.Signal) {
	if h == nil || sig == nil {
		return
	}

	exitCode := SignalExitCode(sig)
	result := &SignalResult{Signal: sig, ExitCode: exitCode}
	if h.recorder != nil {
		result.CloseError = h.recorder.CloseWithFields(exitCode, map[string]any{
			"reason": "signal",
			"signal": sig.String(),
		})
	}
	h.result = result
}

// Done closes after either signal closeout completes or normal shutdown stops
// signal delivery. It lets the command root race a dispatch outcome against a
// signal without transferring process-exit authority to this worker.
func (h *SignalHandler) Done() <-chan struct{} {
	if h == nil || h.done == nil {
		done := make(chan struct{})
		close(done)

		return done
	}

	return h.done
}

// StopAndWait stops accepting signals and waits for any signal-owned recorder
// closeout already in flight. The boolean reports whether a signal won the
// normal-return race.
func (h *SignalHandler) StopAndWait() (SignalResult, bool) {
	if h == nil || h.done == nil {
		return SignalResult{}, false
	}

	if h.stopRequested != nil {
		h.stopOnce.Do(func() { close(h.stopRequested) })
	}
	<-h.done

	if h.result == nil {
		return SignalResult{}, false
	}

	return *h.result, true
}

func SignalExitCode(sig os.Signal) int {
	switch sig {
	case os.Interrupt:
		return 130
	case syscall.SIGTERM:
		return 143
	default:
		return 1
	}
}

func CommandArgs(args []string) []string {
	if len(args) >= 2 && args[0] == "local-ops" && args[1] == "benchmark" {
		return sanitizeLocalOpsBenchmarkArgs(args)
	}

	if len(args) >= 2 && args[0] == "product-evidence" && args[1] == "export" {
		return productevidence.SanitizeProductEvidenceTelemetryArgs(args)
	}

	if len(args) >= 2 && args[0] == "release" && args[1] == "evidence" {
		return productevidence.SanitizeReleaseEvidenceTelemetryArgs(args)
	}

	return args
}

func sanitizeLocalOpsBenchmarkArgs(args []string) []string {
	out := append([]string(nil), args...)
	for i := 0; i < len(out); i++ {
		arg := strings.TrimSpace(out[i])
		if flag, value, ok := strings.Cut(arg, "="); ok {
			switch flag {
			case "--replay-log", "--replay-glob":
				out[i] = flag + "=" + redactedLocalOpsReplayArg(value)
			}

			continue
		}

		switch arg {
		case "--replay-log", "--replay-glob":
			if i+1 < len(out) {
				i++

				out[i] = redactedLocalOpsReplayArg(out[i])
			}
		}
	}

	return out
}

func redactedLocalOpsReplayArg(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "<empty>"
	}

	return "<redacted_replay_log id=" + localops.StableID(value) + ">"
}
