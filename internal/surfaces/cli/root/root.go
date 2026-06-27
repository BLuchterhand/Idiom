package root

import (
	"fmt"
	"io"

	"idiom/internal/capabilities/workspace"
	lanecontract "idiom/internal/contracts/lane"
	"idiom/internal/evidence/telemetry"
	architectcmd "idiom/internal/surfaces/cli/architect"
	auditcmd "idiom/internal/surfaces/cli/audit"
	backendcmd "idiom/internal/surfaces/cli/backend"
	briefcmd "idiom/internal/surfaces/cli/brief"
	doctorcmd "idiom/internal/surfaces/cli/doctor"
	headlesscmd "idiom/internal/surfaces/cli/headless"
	implementcmd "idiom/internal/surfaces/cli/implement"
	initcmd "idiom/internal/surfaces/cli/init"
	lanecmd "idiom/internal/surfaces/cli/lane"
	planexecutecmd "idiom/internal/surfaces/cli/planexecute"
	probecmd "idiom/internal/surfaces/cli/probe"
	productcmd "idiom/internal/surfaces/cli/product"
	profilecmd "idiom/internal/surfaces/cli/profile"
	queuecmd "idiom/internal/surfaces/cli/queue"
	recovercmd "idiom/internal/surfaces/cli/recover"
	cmdruntime "idiom/internal/surfaces/cli/runtime"
	skillscmd "idiom/internal/surfaces/cli/skills"
	cmdtelemetry "idiom/internal/surfaces/cli/telemetry"
	toolcmd "idiom/internal/surfaces/cli/tool"
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
		case "run":
			return runHeadlessMainWithTelemetry(subcommandArgs, stdout, stderr, cmdruntime.RunPromptOnce, rec)
		case "serve":
			return headlesscmd.RunServe(subcommandArgs, stdout, stderr)
		case "session":
			return headlesscmd.RunSession(subcommandArgs, stdout, stderr)
		case "contract":
			return headlesscmd.RunContract(subcommandArgs, stdout, stderr)
		case "decision":
			return headlesscmd.RunDecision(subcommandArgs, stdout, stderr)
		case "tool":
			return runToolMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "mcp":
			return runMCPMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "context":
			return runContextMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "atom":
			return runAtomMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "artifact":
			return runArtifactMainWithTelemetry(subcommandArgs, stdout, stderr, cmdruntime.RunPromptOnce, rec)
		case string(lanecontract.LaneImplement):
			return implementcmd.RunMainWithTelemetry(subcommandArgs, stdout, stderr, cmdruntime.RunPromptOnce, rec)
		case string(lanecontract.LanePlanExecute):
			return planexecutecmd.RunMainWithTelemetry(subcommandArgs, stdout, stderr, cmdruntime.RunPromptOnce, rec)
		case "feature-planner":
			return runFeaturePlannerMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "recover":
			return recovercmd.RunMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "lane":
			return lanecmd.RunMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "backend":
			return backendcmd.RunWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "profile":
			return profilecmd.Run(subcommandArgs, stdout, stderr)
		case "architect":
			return architectcmd.RunWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "audit":
			return auditcmd.RunMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "ranker":
			return runRankerMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "brief":
			return briefcmd.RunMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "probe":
			return probecmd.RunWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "queue":
			return queuecmd.RunMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "local-ops":
			return runLocalOpsMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "product":
			return productcmd.RunMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "product-evidence":
			return runProductEvidenceMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "release":
			return runReleaseMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
		case "init":
			return initcmd.RunMain(subcommandArgs, stdout, stderr)
		case "skills":
			return skillscmd.RunMain(subcommandArgs, stdout, stderr)
		case "version":
			return runVersionMain(subcommandArgs, stdout, stderr)
		case "doctor":
			return doctorcmd.RunWithTelemetry(subcommandArgs, stdout, stderr, rec)
		}
	}

	return cmdruntime.RunWithTelemetry(args, stdout, stderr, cmdruntime.RunPromptOnce, cmdruntime.RunPromptsOnce, cmdruntime.RunTUI, cmdruntime.RunServerOnly, cmdruntime.RunListSessions, runVersionMain, rec)
}

func applyBuildInfo(opts Options) {
	if opts.Version != "" {
		version = opts.Version
	}

	commit = opts.Commit
	date = opts.Date

	resolvedVersion, resolvedCommit, resolvedDate := resolvedBuildInfo()
	workspace.SetHarnessAuditBuildIdentity(resolvedVersion, resolvedCommit, resolvedDate)
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
