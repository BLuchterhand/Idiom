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

func OpenRecorder(args []string, opts RecorderOptions) *telemetry.Recorder {
	wd, _ := os.Getwd()

	wd = config.CanonicalizeExistingPath(wd)

	rec, err := telemetry.Open(telemetry.Options{
		Dir:           opts.Dir,
		Command:       CommandName(args),
		Args:          CommandArgs(args),
		WorkspaceRoot: wd,
		Version:       opts.Version,
	})
	if err != nil {
		return nil
	}

	return rec
}

func InstallSignalHandler(rec *telemetry.Recorder) func() {
	if rec == nil {
		return func() {}
	}

	signals := make(chan os.Signal, 1)
	done := make(chan struct{})

	var stopOnce sync.Once

	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	go func() {
		select {
		case sig := <-signals:
			exitCode := SignalExitCode(sig)

			_ = rec.CloseWithFields(exitCode, map[string]any{
				"reason": "signal",
				"signal": sig.String(),
			})
			os.Exit(exitCode)
		case <-done:
			return
		}
	}()

	return func() {
		stopOnce.Do(func() {
			signal.Stop(signals)
			close(done)
		})
	}
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
