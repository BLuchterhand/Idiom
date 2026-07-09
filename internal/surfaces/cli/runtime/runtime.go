package cmdruntime

import (
	"context"
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

	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/sessionlog"
	"idiom/internal/runtime/workerruntime"
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
	AuthorizedNewFiles          []string
}

type PromptRunner func(config.Config, string, RunOptions, io.Writer) (string, error)
type PromptsRunner func(config.Config, []string, RunOptions, io.Writer) ([]string, error)
type ModeRunner func(config.Config, RunOptions) error
type ListSessionsRunner func(config.Config, io.Writer) error
type VersionRunner func([]string, io.Writer, io.Writer) int

func RunWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, tuiRun ModeRunner, serverRun ModeRunner, listSessions ListSessionsRunner, versionRun VersionRunner, rec *telemetry.Recorder) int {
	var (
		configPath       string
		serverOnly       bool
		resume           string
		listSessionsFlag bool
		modelOverride    string
		showVersion      bool
	)

	fs := flag.NewFlagSet("idiom", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&configPath, "config", "config.json", "path to config file")
	fs.BoolVar(&serverOnly, "server-only", false, "run only llama-server and block")
	fs.StringVar(&resume, "resume", "", "resume a prior session by id (filename without .jsonl) before running")
	fs.BoolVar(&listSessionsFlag, "list-sessions", false, "print recent session ids from the debug log directory and exit")
	fs.StringVar(&modelOverride, "model", "", "override llama_server.model from config (e.g. an alias the proxy resolves)")
	fs.BoolVar(&showVersion, "version", false, "print version and exit (same output as `idiom version`)")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if showVersion {
		return versionRun(nil, stdout, stderr)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)

		return 1
	}

	if strings.TrimSpace(modelOverride) != "" {
		cfg.LlamaServer.Model = strings.TrimSpace(modelOverride)
	}

	if listSessionsFlag {
		telemetry.LogPhase(rec, "list_sessions", nil)

		if err := listSessions(cfg, stdout); err != nil {
			fmt.Fprintln(stderr, err)

			return 1
		}

		return 0
	}

	if serverOnly {
		if err := serverRun(cfg, RunOptions{Telemetry: rec}); err != nil {
			fmt.Fprintln(stderr, err)

			return 1
		}

		return 0
	}

	if err := tuiRun(cfg, RunOptions{Resume: resume, Telemetry: rec}); err != nil {
		fmt.Fprintln(stderr, err)

		return 1
	}

	return 0
}

func RunTUI(cfg config.Config, opts RunOptions) error {
	if !stdoutIsTerminal() || !stderrIsTerminal() {
		return fmt.Errorf("interactive TUI requires a terminal; use `idiom run --prompt ...` for non-interactive runs")
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
		model.SetPlanExecuteScope(opts.ScopeContract, opts.AuthorizedNewFiles)
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
