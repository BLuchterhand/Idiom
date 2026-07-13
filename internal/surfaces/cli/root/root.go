package root

import (
	"fmt"
	"io"
	"os"
	"strings"

	"idiom/internal/capabilities/workspace"
	lanecontract "idiom/internal/contracts/lane"
	"idiom/internal/evidence/productevidence"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	runtimesession "idiom/internal/runtime/session"
	architectcmd "idiom/internal/surfaces/cli/architect"
	atomcmd "idiom/internal/surfaces/cli/atom"
	auditcmd "idiom/internal/surfaces/cli/audit"
	backendcmd "idiom/internal/surfaces/cli/backend"
	briefcmd "idiom/internal/surfaces/cli/brief"
	codingrulescmd "idiom/internal/surfaces/cli/codingrules"
	doctorcmd "idiom/internal/surfaces/cli/doctor"
	featureplannercmd "idiom/internal/surfaces/cli/featureplanner"
	headlesscmd "idiom/internal/surfaces/cli/headless"
	implementcmd "idiom/internal/surfaces/cli/implement"
	initcmd "idiom/internal/surfaces/cli/init"
	lanecmd "idiom/internal/surfaces/cli/lane"
	mcpcmd "idiom/internal/surfaces/cli/mcp"
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
	"idiom/internal/surfaces/tui/app"
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

func runMainWithTelemetryDir(args []string, stdout io.Writer, stderr io.Writer, telemetryDir string) int {
	promptRun, promptsRun := cmdruntime.NewHeadlessRunners(func(cfg config.Config) runtimesession.Engine {
		return app.New(cfg)
	})
	tuiRun := cmdruntime.NewTUIRunner(func(cfg config.Config) cmdruntime.InteractiveModel {
		return app.New(cfg)
	})

	rec := cmdtelemetry.OpenRecorder(rootTelemetryCommandName(args), args, cmdtelemetry.RecorderOptions{Dir: telemetryDir, Version: version})
	dispatch := func() int {
		if len(args) > 0 {
			subcommand := args[0]
			subcommandArgs := args[1:]

			switch subcommand {
			case "run":
				return headlesscmd.Run(subcommandArgs, os.Stdin, stdout, stderr, promptRun, promptsRun, rec)
			case "serve":
				return headlesscmd.RunServe(subcommandArgs, stdout, stderr)
			case "session":
				return headlesscmd.RunSession(subcommandArgs, stdout, stderr)
			case "contract":
				return headlesscmd.RunContract(subcommandArgs, stdout, stderr)
			case "decision":
				return headlesscmd.RunDecision(subcommandArgs, stdout, stderr)
			case "tool":
				return toolcmd.RunWithTelemetry(subcommandArgs, stdout, stderr, rec)
			case "mcp":
				return mcpcmd.RunWithTelemetryAndVersion(subcommandArgs, stdout, stderr, rec, version)
			case "atom":
				return atomcmd.RunMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
			case "artifact":
				return runArtifactMainWithTelemetry(subcommandArgs, stdout, stderr, promptRun, rec)
			case string(lanecontract.LaneImplement):
				return implementcmd.RunMainWithTelemetry(subcommandArgs, stdout, stderr, promptRun, rec)
			case string(lanecontract.LanePlanExecute):
				return planexecutecmd.RunMainWithTelemetry(subcommandArgs, stdout, stderr, promptRun, rec)
			case "feature-planner":
				return featureplannercmd.RunMainWithTelemetry(subcommandArgs, stdout, stderr, rec, featurePlannerDependencies(promptRun))
			case "recover":
				return recovercmd.RunMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
			case "lane":
				return lanecmd.RunMainWithTelemetry(subcommandArgs, stdout, stderr, promptRun, rec)
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
			case "coding-rules":
				return codingrulescmd.RunWithTelemetry(subcommandArgs, stdout, stderr, rec)
			case "probe":
				return probecmd.RunWithTelemetry(subcommandArgs, stdout, stderr, rec)
			case "queue":
				return queuecmd.RunMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
			case "local-ops":
				return runLocalOpsMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
			case "product":
				return productcmd.RunMainWithTelemetry(subcommandArgs, stdout, stderr, rec)
			case "product-evidence":
				return productevidence.RunProductEvidenceMainWithTelemetry(subcommandArgs, stdout, stderr, rec, productEvidenceDeps())
			case "release":
				return productevidence.RunReleaseMainWithTelemetry(subcommandArgs, stdout, stderr, rec, productEvidenceDeps())
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

		return cmdruntime.RunWithTelemetry(args, stdout, stderr, tuiRun, cmdruntime.RunServerOnlyWithTelemetry, cmdruntime.RunListSessions, runVersionMain, rec)
	}

	if rec == nil {
		return dispatch()
	}

	return runWithTelemetrySignalArbitration(rec, dispatch)
}

type rootDispatchOutcome struct {
	code      int
	recovered any
}

func runWithTelemetrySignalArbitration(rec *telemetry.Recorder, dispatch func() int) (code int) {
	signalHandler := cmdtelemetry.InstallSignalHandler(rec)
	defer func() {
		recovered := recover()
		signalResult, signalHandled := signalHandler.StopAndWait()

		switch {
		case recovered != nil:
			// A signal-owned closeout has already emitted the sole process-exit
			// record. Otherwise preserve the panic record before rethrowing.
			if !signalHandled {
				_ = rec.CloseWithFields(2, map[string]any{
					"reason": "panic",
					"panic":  fmt.Sprint(recovered),
				})
			}
			panic(recovered)
		case signalHandled:
			code = signalResult.ExitCode
		default:
			_ = rec.CloseWithFields(code, nil)
		}
	}()

	outcomes := make(chan rootDispatchOutcome, 1)
	go func() {
		outcome := rootDispatchOutcome{}
		defer func() {
			outcome.recovered = recover()
			outcomes <- outcome
		}()
		outcome.code = dispatch()
	}()

	select {
	case outcome := <-outcomes:
		if outcome.recovered != nil {
			panic(outcome.recovered)
		}

		return outcome.code
	case <-signalHandler.Done():
		// The deferred arbitration adopts the signal result after its recorder
		// closeout has completed. A non-cooperative dispatch may still be
		// running, but main now owns the single process return that terminates it.
		return 0
	}
}

func rootTelemetryCommandName(args []string) string {
	if len(args) == 0 {
		return runtimeCommandName(args)
	}

	subcommand := args[0]
	switch subcommand {
	case "architect", "atom", "audit", "backend", "brief", "coding-rules",
		"feature-planner", "lane", "local-ops", "mcp", "product", "product-evidence",
		"probe", "queue", "ranker", "recover", "release", "tool":
		if len(args) > 1 {
			return subcommand + ":" + args[1]
		}
		return subcommand
	case "artifact", "contract", "decision", "doctor", string(lanecontract.LaneImplement), "init",
		string(lanecontract.LanePlanExecute), "profile", "run", "serve", "session",
		"skills", "version":
		return subcommand
	default:
		return runtimeCommandName(args)
	}
}

func runtimeCommandName(args []string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-server-only=") || strings.HasPrefix(arg, "--server-only=") {
			return "server"
		}

		switch arg {
		case "-server-only", "--server-only":
			return "server"
		case "-list-sessions", "--list-sessions":
			return "list_sessions"
		}
	}

	return "tui"
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
