package root
import (
	"fmt"
	"io"

	"idiom/internal/cmdtelemetry"
	"idiom/internal/telemetry"
	toolcmd "idiom/internal/cmd/tool"
)

type Options struct {
	Version      string
	Commit       string
	Date         string
	TelemetryDir string
}

var commandTelemetryDir string

func RunMain(args []string, stdout io.Writer, stderr io.Writer, opts Options) int {
	applyBuildInfo(opts)
	return runMainWithTelemetryDir(args, stdout, stderr, opts.TelemetryDir)
}

func runMain(args []string, stdout io.Writer, stderr io.Writer) (code int) {
	return runMainWithTelemetryDir(args, stdout, stderr, commandTelemetryDir)
}

func runMainWithTelemetryDir(args []string, stdout io.Writer, stderr io.Writer, telemetryDir string) (code int) {
	rec := cmdtelemetry.OpenRecorder(args, cmdtelemetry.RecorderOptions{Dir: telemetryDir, Version: version})
	if rec != nil {
		stopTelemetrySignalHandler := cmdtelemetry.InstallSignalHandler(rec)
		defer func() {
			stopTelemetrySignalHandler()
			if recovered := recover(); recovered != nil {
				_ = rec.CloseWithFields(2, map[string]any{
					"reason": "panic",
					"panic":  fmt.Sprint(recovered),
				})
				panic(recovered)
			}
			_ = rec.Close(code)
		}()
	}
	if len(args) > 0 {
		subcommand := args[0]
		subcommandArgs := args[1:]
		switch subcommand {
		case "tool":
			return runToolMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "mcp":
			return runMCPMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "context":
			return runContextMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "atom":
			return runAtomMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "artifact":
			return runArtifactMainWithTelemetry(subcommandArgs, stdout, stderr, runPromptOnce, rec)
		case "implement":
			return runImplementMainWithTelemetry(subcommandArgs, stdout, stderr, runPromptOnce, rec)
		case "plan-execute":
			return runPlanExecuteMainWithTelemetry(subcommandArgs, stdout, stderr, runPromptOnce, rec)
		case "feature-planner":
			return runFeaturePlannerMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "recover":
			return runRecoverMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "lane":
			return runLaneMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "backend":
			return runBackendMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "profile":
						return runProfileMain(subcommandArgs, stdout, stderr)
		case "architect":
			return runArchitectMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "audit":
			return runAuditMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "ranker":
			return runRankerMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "brief":
			return runBriefMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "probe":
			return runProbeMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "queue":
			return runQueueMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "local-ops":
			return runLocalOpsMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "product":
			return runProductMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "product-evidence":
			return runProductEvidenceMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "release":
			return runReleaseMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "init":
			return runInitMain(subcommandArgs, stdout, stderr)
		case "skills":
			return runSkillsMain(subcommandArgs, stdout, stderr)
		case "version":
			return runVersionMain(subcommandArgs, stdout, stderr)
		case "doctor":
			return runDoctorMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		}
	}
	return runWithTelemetry(args, stdout, stderr, runPromptOnce, runPromptsOnce, runTUI, runServerOnly, runListSessions, rec)
}

func applyBuildInfo(opts Options) {
	if opts.Version != "" {
		version = opts.Version
	}
	commit = opts.Commit
	date = opts.Date
}

func runToolMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return toolcmd.Run(args, stdout, stderr)
}

func runToolMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return toolcmd.RunWithTelemetry(args, stdout, stderr, rec)
}

func toolHelpRequested(args []string) bool {
	return toolcmd.HelpRequested(args)
}

func commandTelemetryName(args []string) string {
	return cmdtelemetry.CommandName(args)
}

func logCLIPhase(rec *telemetry.Recorder, eventType string, fields map[string]any) {
	toolcmd.LogCLIPhase(rec, eventType, fields)
}
