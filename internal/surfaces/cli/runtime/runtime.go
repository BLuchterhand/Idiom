package cmdruntime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"

	"idiom/internal/capabilities/workspace"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/sessionlog"
	"idiom/internal/runtime/workerruntime"
	"idiom/internal/surfaces/cli/runrecord"
	"idiom/internal/surfaces/tui/app"
)

type RunOptions struct {
	JSONEvents                  bool
	Resume                      string
	TaskFamily                  string
	PlannerOwnedVerification    bool
	MaxTokensOverride           int
	DisableThinking             bool
	ReviewRepairOpenObligations []string
	UseActiveModel              bool
	Telemetry                   *telemetry.Recorder
	ScopeContract               plancontract.PlanScopeContract
}

type ApplicationRequestMode string

const (
	ApplicationRequestVersion      ApplicationRequestMode = "version"
	ApplicationRequestListSessions ApplicationRequestMode = "list_sessions"
	ApplicationRequestServer       ApplicationRequestMode = "server"
	ApplicationRequestTurnsFile    ApplicationRequestMode = "turns_file"
	ApplicationRequestPrompt       ApplicationRequestMode = "prompt"
	ApplicationRequestTUI          ApplicationRequestMode = "tui"
)

type ApplicationRequest struct {
	Mode          ApplicationRequestMode
	ConfigPath    string
	ModelOverride string
	Prompt        string
	TurnsFile     string
	Options       RunOptions
}

type PromptRunner func(config.Config, string, RunOptions, io.Writer) (string, error)
type PromptsRunner func(config.Config, []string, RunOptions, io.Writer) ([]string, error)
type ModeRunner func(config.Config, RunOptions) error
type ListSessionsRunner func(config.Config, io.Writer) error
type VersionRunner func([]string, io.Writer, io.Writer) int

func RunWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, promptRun PromptRunner, promptsRun PromptsRunner, tuiRun ModeRunner, serverRun ModeRunner, listSessions ListSessionsRunner, versionRun VersionRunner, rec *telemetry.Recorder) int {
	var (
		configPath       string
		serverOnly       bool
		prompt           string
		turnsFile        string
		jsonEvents       bool
		resume           string
		listSessionsFlag bool
		modelOverride    string
		showVersion      bool
	)

	fs := flag.NewFlagSet("idiom", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&configPath, "config", "config.json", "path to config file")
	fs.BoolVar(&serverOnly, "server-only", false, "run only llama-server and block")
	fs.StringVar(&prompt, "prompt", "", "run one prompt headlessly, print the answer, and exit")
	fs.StringVar(&turnsFile, "turns-file", "", "run newline-delimited or JSON-array prompts headlessly in one session, print answers, and exit")
	fs.BoolVar(&jsonEvents, "json", false, "emit structured JSONL events to stdout (only with --prompt)")
	fs.StringVar(&resume, "resume", "", "resume a prior session by id (filename without .jsonl) before running")
	fs.BoolVar(&listSessionsFlag, "list-sessions", false, "print recent session ids from the debug log directory and exit")
	fs.StringVar(&modelOverride, "model", "", "override llama_server.model from config (e.g. an alias the proxy resolves)")
	fs.BoolVar(&showVersion, "version", false, "print version and exit (same output as `idiom version`)")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	req, err := newApplicationRequest(applicationRequestFlags{
		ConfigPath:       configPath,
		ServerOnly:       serverOnly,
		Prompt:           prompt,
		TurnsFile:        turnsFile,
		JSONEvents:       jsonEvents,
		Resume:           resume,
		ListSessionsFlag: listSessionsFlag,
		ModelOverride:    modelOverride,
		ShowVersion:      showVersion,
		Telemetry:        rec,
	})
	if err != nil {
		fmt.Fprintln(stderr, err)

		return 2
	}

	if req.Mode == ApplicationRequestVersion {
		return versionRun(nil, stdout, stderr)
	}

	cfg, err := config.Load(req.ConfigPath)
	if err != nil {
		fmt.Fprintln(stderr, err)

		return 1
	}

	if strings.TrimSpace(req.ModelOverride) != "" {
		cfg.LlamaServer.Model = req.ModelOverride
	}

	if req.Mode == ApplicationRequestListSessions {
		telemetry.LogPhase(rec, "list_sessions", nil)

		if err := listSessions(cfg, stdout); err != nil {
			fmt.Fprintln(stderr, err)

			return 1
		}

		return 0
	}

	if req.Mode == ApplicationRequestServer {
		if err := serverRun(cfg, req.Options); err != nil {
			fmt.Fprintln(stderr, err)

			return 1
		}

		return 0
	}

	if req.Mode == ApplicationRequestTurnsFile {
		started := time.Now().UTC()
		runID, runIDErr := ensureLegacyRunID(&cfg)
		if runIDErr != nil {
			fmt.Fprintln(stderr, runIDErr)

			return 2
		}

		prompts, err := LoadPromptTurns(req.TurnsFile)
		if err != nil {
			record := legacyRunCompletionRecord(cfg, req, runID, started, "idiom --turns-file", "", nil)
			record.ExitCode = 1
			record.TerminalClass = runrecord.TerminalClassFailed
			record.Error = err.Error()
			if saveErr := saveLegacyRunCompletion(record); saveErr != nil {
				fmt.Fprintln(stderr, saveErr)

				return 1
			}
			fmt.Fprintln(stderr, err)

			return 1
		}

		answers, err := promptsRun(cfg, prompts, req.Options, stdout)
		record := legacyRunCompletionRecord(cfg, req, runID, started, "idiom --turns-file", strings.Join(prompts, "\n\n"), answers)
		if err != nil {
			record.ExitCode = 1
			record.TerminalClass = runrecord.TerminalClassFailed
			record.Error = err.Error()
			if saveErr := saveLegacyRunCompletion(record); saveErr != nil {
				fmt.Fprintln(stderr, saveErr)

				return 1
			}
			fmt.Fprintln(stderr, err)

			return 1
		}

		record.ExitCode = 0
		record.TerminalClass = runrecord.TerminalClassCompleted
		if saveErr := saveLegacyRunCompletion(record); saveErr != nil {
			fmt.Fprintln(stderr, saveErr)

			return 1
		}

		if !req.Options.JSONEvents {
			for _, answer := range answers {
				fmt.Fprintln(stdout, answer)
			}
		}

		return 0
	}

	if req.Mode == ApplicationRequestPrompt {
		started := time.Now().UTC()
		runID, runIDErr := ensureLegacyRunID(&cfg)
		if runIDErr != nil {
			fmt.Fprintln(stderr, runIDErr)

			return 2
		}

		answer, err := promptRun(cfg, req.Prompt, req.Options, stdout)
		record := legacyRunCompletionRecord(cfg, req, runID, started, "idiom --prompt", req.Prompt, []string{answer})
		if err != nil {
			record.ExitCode = 1
			record.TerminalClass = runrecord.TerminalClassFailed
			record.Error = err.Error()
			if saveErr := saveLegacyRunCompletion(record); saveErr != nil {
				fmt.Fprintln(stderr, saveErr)

				return 1
			}
			fmt.Fprintln(stderr, err)

			return 1
		}

		record.ExitCode = 0
		record.TerminalClass = runrecord.TerminalClassCompleted
		if saveErr := saveLegacyRunCompletion(record); saveErr != nil {
			fmt.Fprintln(stderr, saveErr)

			return 1
		}

		if !req.Options.JSONEvents {
			fmt.Fprintln(stdout, answer)
		}

		return 0
	}

	if err := tuiRun(cfg, req.Options); err != nil {
		fmt.Fprintln(stderr, err)

		return 1
	}

	return 0
}

func ensureLegacyRunID(cfg *config.Config) (string, error) {
	runID := strings.TrimSpace(cfg.Workspace.SessionID)
	if runID == "" {
		runID = runrecord.NewRunID()
	}
	if err := runrecord.ValidateRunID(runID); err != nil {
		return "", err
	}

	cfg.Workspace.SessionID = runID

	return runID, nil
}

func legacyRunCompletionRecord(cfg config.Config, req ApplicationRequest, runID string, started time.Time, implementation string, prompt string, answers []string) runrecord.RunCompletionRecord {
	outputMode := "text"
	if req.Options.JSONEvents {
		outputMode = "jsonl"
	}

	metadata := map[string][]string{
		"legacy_mode": {string(req.Mode)},
	}
	for key, value := range workspace.HarnessAuditBuildIdentityMetadata() {
		metadata[key] = value
	}
	metadata["entrypoint"] = []string{"idiom"}
	metadata["active_mode"] = []string{string(req.Mode)}
	metadata["timeout_owner"] = []string{"chat_request_timeout"}
	if strings.TrimSpace(req.TurnsFile) != "" {
		metadata["turns_file"] = []string{filepath.Clean(req.TurnsFile)}
	}
	if strings.TrimSpace(req.Options.Resume) != "" {
		metadata["resume"] = []string{strings.TrimSpace(req.Options.Resume)}
	}

	root := strings.TrimSpace(cfg.Workspace.Root)
	if root == "" {
		root = "."
	}

	return runrecord.RunCompletionRecord{
		SchemaVersion:  runrecord.RunCompletionSchemaVersion,
		Type:           runrecord.RunCompletionRecordType,
		RunID:          runID,
		SessionID:      runID,
		PromptSHA256:   runrecord.SHA256Hex(prompt),
		AnswerSHA256:   runrecord.SHA256Hex(strings.Join(answers, "\n\n")),
		OutputMode:     outputMode,
		ConfigPath:     filepath.Clean(req.ConfigPath),
		WorkspaceRoot:  root,
		StartedAt:      started,
		Implementation: implementation,
		Metadata:       metadata,
	}
}

func saveLegacyRunCompletion(record runrecord.RunCompletionRecord) error {
	record.CompletedAt = time.Now().UTC()
	record.DurationMS = record.CompletedAt.Sub(record.StartedAt).Milliseconds()

	completionRecord := record
	runrecord.AddArtifact(&completionRecord, "completion", runrecord.RunCompletionRelPath(record.RunID))
	if _, err := runrecord.SaveRunCompletionRecord(record.WorkspaceRoot, completionRecord); err != nil {
		return err
	}

	return nil
}

type applicationRequestFlags struct {
	ConfigPath       string
	ServerOnly       bool
	Prompt           string
	TurnsFile        string
	JSONEvents       bool
	Resume           string
	ListSessionsFlag bool
	ModelOverride    string
	ShowVersion      bool
	Telemetry        *telemetry.Recorder
}

func newApplicationRequest(flags applicationRequestFlags) (ApplicationRequest, error) {
	req := ApplicationRequest{
		Mode:          ApplicationRequestTUI,
		ConfigPath:    flags.ConfigPath,
		ModelOverride: strings.TrimSpace(flags.ModelOverride),
		Prompt:        flags.Prompt,
		TurnsFile:     flags.TurnsFile,
		Options: RunOptions{
			JSONEvents: flags.JSONEvents,
			Resume:     flags.Resume,
			Telemetry:  flags.Telemetry,
		},
	}

	switch {
	case flags.ShowVersion:
		req.Mode = ApplicationRequestVersion
	case flags.ListSessionsFlag:
		req.Mode = ApplicationRequestListSessions
	case flags.ServerOnly:
		req.Mode = ApplicationRequestServer
		req.Options.JSONEvents = false
		req.Options.Resume = ""
	case strings.TrimSpace(flags.Prompt) != "" && strings.TrimSpace(flags.TurnsFile) != "":
		return ApplicationRequest{}, fmt.Errorf("--prompt and --turns-file are mutually exclusive")
	case strings.TrimSpace(flags.TurnsFile) != "":
		req.Mode = ApplicationRequestTurnsFile
	case strings.TrimSpace(flags.Prompt) != "":
		req.Mode = ApplicationRequestPrompt
	default:
		req.Mode = ApplicationRequestTUI
		req.Options.JSONEvents = false
	}

	return req, nil
}

func LoadPromptTurns(path string) ([]string, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read turns file: %w", err)
	}

	var prompts []string

	if err := json.Unmarshal(data, &prompts); err == nil {
		return CleanPromptTurns(prompts)
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		prompts = append(prompts, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan turns file: %w", err)
	}

	return CleanPromptTurns(prompts)
}

func CleanPromptTurns(prompts []string) ([]string, error) {
	cleaned := make([]string, 0, len(prompts))
	for _, prompt := range prompts {
		prompt = strings.TrimSpace(prompt)
		if prompt != "" {
			cleaned = append(cleaned, prompt)
		}
	}

	if len(cleaned) == 0 {
		return nil, fmt.Errorf("turns file did not contain any prompts")
	}

	return cleaned, nil
}

func RunTUI(cfg config.Config, opts RunOptions) error {
	if !stdoutIsTerminal() || !stderrIsTerminal() {
		return fmt.Errorf("interactive TUI requires a terminal; use `idiom run --prompt ...` or legacy `idiom -prompt ...` for non-interactive runs")
	}

	model := app.New(cfg)
	closeModel := ModelCloseout(opts.Telemetry, model, "tui", opts.TaskFamily)
	unregisterCloseout := opts.Telemetry.RegisterCloseout(closeModel)

	defer closeModel()
	defer unregisterCloseout()

	LogModelSessionLink(opts.Telemetry, "tui", model.DebugLogPath())

	if opts.Resume != "" {
		if err := model.ResumeSession(opts.Resume); err != nil {
			return fmt.Errorf("resume %q: %w", opts.Resume, err)
		}
	}

	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := program.Run(); err != nil {
		return err
	}

	return nil
}

func stdoutIsTerminal() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

func stderrIsTerminal() bool {
	return term.IsTerminal(int(os.Stderr.Fd()))
}

func RunPromptOnce(cfg config.Config, prompt string, opts RunOptions, stdout io.Writer) (string, error) {
	model := app.New(cfg)
	closeModel := ModelCloseout(opts.Telemetry, model, "prompt", opts.TaskFamily)
	unregisterCloseout := opts.Telemetry.RegisterCloseout(closeModel)

	defer closeModel()
	defer unregisterCloseout()

	model.SetForcedTaskFamily(opts.TaskFamily)
	model.SetPlannerOwnedVerification(opts.PlannerOwnedVerification)
	model.SetMaxTokensOverride(opts.MaxTokensOverride)
	model.SetDisableThinkingOverride(opts.DisableThinking)
	model.SetForcedReadinessOpenObligations(opts.ReviewRepairOpenObligations)
	model.SetNonInteractive(true)
	if opts.UseActiveModel {
		model.UseConfigPlannerModel()
	}

	if workerruntime.TaskFamilyIsPlanThenExecutePlanner(opts.TaskFamily) {
		model.SetPlanExecuteScopeContract(opts.ScopeContract)
	}

	LogModelSessionLink(opts.Telemetry, "prompt", model.DebugLogPath())

	if opts.Resume != "" {
		path := sessionlog.ResolveSessionLogPath(cfg, opts.Resume)
		if err := model.LoadHistoryFromSession(path); err != nil {
			return "", fmt.Errorf("resume %q: %w", opts.Resume, err)
		}
	}

	if opts.JSONEvents {
		sessionID := ""
		if path := model.DebugLogPath(); path != "" {
			sessionID = sessionlog.SessionIDFromPath(path)
		}

		model.SetJSONEventStream(stdout, sessionID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Chat.RequestTimeoutSeconds+cfg.Chat.ServerStartTimeoutSecs)*time.Second)
	defer cancel()

	return model.RunPromptOnce(ctx, prompt)
}

func RunPromptsOnce(cfg config.Config, prompts []string, opts RunOptions, stdout io.Writer) ([]string, error) {
	model := app.New(cfg)
	closeModel := ModelCloseout(opts.Telemetry, model, "turns_file", opts.TaskFamily)
	unregisterCloseout := opts.Telemetry.RegisterCloseout(closeModel)

	defer closeModel()
	defer unregisterCloseout()

	model.SetForcedTaskFamily(opts.TaskFamily)
	model.SetPlannerOwnedVerification(opts.PlannerOwnedVerification)
	model.SetMaxTokensOverride(opts.MaxTokensOverride)
	model.SetDisableThinkingOverride(opts.DisableThinking)
	model.SetForcedReadinessOpenObligations(opts.ReviewRepairOpenObligations)
	model.SetNonInteractive(true)
	LogModelSessionLink(opts.Telemetry, "turns_file", model.DebugLogPath())

	if opts.Resume != "" {
		path := sessionlog.ResolveSessionLogPath(cfg, opts.Resume)
		if err := model.LoadHistoryFromSession(path); err != nil {
			return nil, fmt.Errorf("resume %q: %w", opts.Resume, err)
		}
	}

	if opts.JSONEvents {
		sessionID := ""
		if path := model.DebugLogPath(); path != "" {
			sessionID = sessionlog.SessionIDFromPath(path)
		}

		model.SetJSONEventStream(stdout, sessionID)
	}

	answers := make([]string, 0, len(prompts))
	for _, prompt := range prompts {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Chat.RequestTimeoutSeconds+cfg.Chat.ServerStartTimeoutSecs)*time.Second)
		answer, err := model.RunPromptOnce(ctx, prompt)

		cancel()

		if err != nil {
			return answers, err
		}

		answers = append(answers, answer)
	}

	return answers, nil
}

func ModelCloseout(rec *telemetry.Recorder, model *app.Model, mode string, taskFamily string) func() {
	var once sync.Once

	return func() {
		once.Do(func() {
			sessionID := ""
			debugLogPath := ""

			if model != nil {
				debugLogPath = model.DebugLogPath()
				if debugLogPath != "" {
					sessionID = sessionlog.SessionIDFromPath(debugLogPath)
				}
			}

			fields := map[string]any{
				"mode":          mode,
				"task_family":   taskFamily,
				"session_id":    sessionID,
				"summary_event": "session_summary",
			}
			if strings.TrimSpace(debugLogPath) != "" {
				fields["debug_log_path"] = filepath.Clean(debugLogPath)
			}

			if model != nil {
				model.Close()

				fields["model_closed"] = true
			}

			telemetry.LogPhase(rec, "model_session_closeout", fields)

			if strings.TrimSpace(taskFamily) == "implementation" {
				telemetry.LogPhase(rec, "implement_session_closeout", fields)
			}
		})
	}
}

func LogModelSessionLink(rec *telemetry.Recorder, mode string, debugLogPath string) {
	if rec == nil || strings.TrimSpace(debugLogPath) == "" {
		return
	}

	_ = rec.Log("model_session", map[string]any{
		"mode":           mode,
		"debug_log_path": filepath.Clean(debugLogPath),
		"session_id":     sessionlog.SessionIDFromPath(debugLogPath),
	})
}

func RunListSessions(cfg config.Config, stdout io.Writer) error {
	sessions, err := sessionlog.ListRecordedSessions(cfg)
	if err != nil {
		return err
	}

	if len(sessions) == 0 {
		fmt.Fprintln(stdout, "no sessions found")

		return nil
	}

	for _, s := range sessions {
		summary := s.FirstPrompt
		if summary == "" {
			summary = "(no user prompt recorded)"
		}

		fmt.Fprintf(stdout, "%s\tmsgs=%d/%d\t%s\n", s.ID, s.UserMessages, s.AssistMessage, summary)
	}

	return nil
}

func RunServerOnlyWithTelemetry(cfg config.Config, opts RunOptions) error {
	if !cfg.LlamaServer.IsLaunchable() {
		return fmt.Errorf("backend kind %q is hosted at %s; --server-only can only launch local llama/mlx backends", cfg.LlamaServer.Kind, cfg.LlamaServer.EndpointBaseURL())
	}

	client := llama.NewClientForServer(cfg.LlamaServer, 0)
	controller := llama.NewServerController(cfg.LlamaServer, client)
	telemetry.LogPhase(opts.Telemetry, "server_starting", map[string]any{
		"base_url": cfg.LlamaServer.EndpointBaseURL(),
		"backend":  cfg.LlamaServer.Kind,
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Chat.ServerStartTimeoutSecs)*time.Second)
	defer cancel()

	if err := controller.Start(ctx); err != nil {
		telemetry.LogPhase(opts.Telemetry, "server_error", map[string]any{"stage": "start", "error": err.Error()})

		return err
	}

	if err := controller.WaitUntilReady(ctx); err != nil {
		telemetry.LogPhase(opts.Telemetry, "server_error", map[string]any{"stage": "wait_until_ready", "error": err.Error()})

		return err
	}

	telemetry.LogPhase(opts.Telemetry, "server_ready", map[string]any{
		"base_url": cfg.LlamaServer.EndpointBaseURL(),
		"log_path": controller.LogPath(),
	})

	fmt.Printf("llama-server ready on %s\n", cfg.LlamaServer.EndpointBaseURL())
	fmt.Printf("model: %s\n", cfg.LlamaServer.Model)
	fmt.Printf("log: %s\n", controller.LogPath())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer stopCancel()

	if err := controller.Stop(stopCtx); err != nil {
		telemetry.LogPhase(opts.Telemetry, "server_error", map[string]any{"stage": "stop", "error": err.Error()})

		return err
	}

	telemetry.LogPhase(opts.Telemetry, "server_stopped", map[string]any{
		"base_url": cfg.LlamaServer.EndpointBaseURL(),
	})

	return nil
}
