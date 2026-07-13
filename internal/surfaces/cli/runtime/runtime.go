package cmdruntime

import (
	"context"
	"errors"
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
	runtimesession "idiom/internal/runtime/session"
	"idiom/internal/runtime/sessionlog"
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
	OnSourceSatisfied           func([]string)
}

type PromptRunner func(config.Config, string, RunOptions, io.Writer) (string, error)
type PromptsRunner func(config.Config, []string, RunOptions, io.Writer) ([]string, error)
type ModeRunner func(config.Config, RunOptions) error
type ListSessionsRunner func(config.Config, io.Writer) error
type VersionRunner func([]string, io.Writer, io.Writer) int

// InteractiveModel is the composition-root contract needed to launch the TUI.
// It keeps the CLI runtime independent of the concrete TUI app package.
type InteractiveModel interface {
	tea.Model
	DebugLogPath() string
	ResumeSession(string) error
	Close() error
}

type InteractiveModelFactory func(config.Config) InteractiveModel

type SessionLifecycle interface {
	DebugLogPath() string
	Close() error
}

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

func NewTUIRunner(factory InteractiveModelFactory) ModeRunner {
	return func(cfg config.Config, opts RunOptions) (resultErr error) {
		if !stdoutIsTerminal() || !stderrIsTerminal() {
			return fmt.Errorf("interactive TUI requires a terminal; use `idiom run --prompt ...` for non-interactive runs")
		}
		if factory == nil {
			return fmt.Errorf("interactive model factory is required")
		}

		model := factory(cfg)
		if model == nil {
			return fmt.Errorf("interactive model factory returned no model")
		}

		return runInteractiveModel(
			context.Background(),
			model,
			opts,
			tea.WithAltScreen(),
			tea.WithMouseCellMotion(),
		)
	}
}

func runInteractiveModel(ctx context.Context, model InteractiveModel, opts RunOptions, programOptions ...tea.ProgramOption) (resultErr error) {
	closeModel := ModelCloseout(opts.Telemetry, model, "tui", opts.TaskFamily)
	programContext, cancelProgram := context.WithCancel(ctx)
	runnerDone := make(chan struct{})
	unregisterCloseout := opts.Telemetry.RegisterCloseout(func() {
		cancelProgram()
		<-runnerDone
		_ = closeModel()
	})

	defer func() {
		cancelProgram()
		close(runnerDone)
		unregisterCloseout()
		resultErr = errors.Join(resultErr, closeModel())
	}()

	LogModelSessionLink(opts.Telemetry, "tui", model.DebugLogPath())

	if opts.Resume != "" {
		if err := model.ResumeSession(opts.Resume); err != nil {
			return fmt.Errorf("resume %q: %w", opts.Resume, err)
		}
	}

	options := make([]tea.ProgramOption, 0, len(programOptions)+2)
	options = append(options, programOptions...)
	options = append(options, tea.WithContext(programContext))
	if opts.Telemetry != nil {
		options = append(options, tea.WithoutSignalHandler())
	}
	program := tea.NewProgram(model, options...)
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

func NewHeadlessRunners(factory runtimesession.Factory) (PromptRunner, PromptsRunner) {
	runPrompt := func(cfg config.Config, prompt string, opts RunOptions, stdout io.Writer) (answer string, resultErr error) {
		s, err := openHeadlessSession(cfg, factory, opts, stdout, "prompt")
		if err != nil {
			return "", err
		}
		defer func() {
			resultErr = errors.Join(resultErr, s.close())
		}()

		return s.runtime.RunPrompt(context.Background(), prompt)
	}

	runPrompts := func(cfg config.Config, prompts []string, opts RunOptions, stdout io.Writer) (answers []string, resultErr error) {
		s, err := openHeadlessSession(cfg, factory, opts, stdout, "turns_file")
		if err != nil {
			return nil, err
		}
		defer func() {
			resultErr = errors.Join(resultErr, s.close())
		}()

		return s.runtime.RunPrompts(context.Background(), prompts)
	}

	return runPrompt, runPrompts
}

type headlessSession struct {
	runtime            *runtimesession.Session
	closeout           func() error
	unregisterCloseout func()
}

func openHeadlessSession(cfg config.Config, factory runtimesession.Factory, opts RunOptions, stdout io.Writer, mode string) (*headlessSession, error) {
	runtimeSession, err := runtimesession.Open(cfg, factory, runtimesession.Options{
		Resume:                      opts.Resume,
		JSONEvents:                  opts.JSONEvents,
		TaskFamily:                  opts.TaskFamily,
		PlannerOwnedVerification:    opts.PlannerOwnedVerification,
		MaxTokensOverride:           opts.MaxTokensOverride,
		DisableThinking:             opts.DisableThinking,
		ReviewRepairOpenObligations: append([]string(nil), opts.ReviewRepairOpenObligations...),
		UseActiveModel:              opts.UseActiveModel,
		ScopeContract:               opts.ScopeContract,
		AuthorizedNewFiles:          append([]string(nil), opts.AuthorizedNewFiles...),
		OnSourceSatisfied:           opts.OnSourceSatisfied,
	}, stdout)
	if err != nil {
		return nil, err
	}

	closeout := ModelCloseout(opts.Telemetry, runtimeSession, mode, opts.TaskFamily)
	unregisterCloseout := opts.Telemetry.RegisterCloseout(func() { _ = closeout() })
	LogModelSessionLink(opts.Telemetry, mode, runtimeSession.DebugLogPath())

	return &headlessSession{
		runtime:            runtimeSession,
		closeout:           closeout,
		unregisterCloseout: unregisterCloseout,
	}, nil
}

func (s *headlessSession) close() error {
	if s == nil {
		return nil
	}
	var closeErr error
	if s.closeout != nil {
		closeErr = s.closeout()
	}
	if s.unregisterCloseout != nil {
		s.unregisterCloseout()
	}

	return closeErr
}

func ModelCloseout(rec *telemetry.Recorder, model SessionLifecycle, mode string, taskFamily string) func() error {
	var once sync.Once
	var closeErr error

	return func() error {
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
				closeErr = model.Close()
				if closeErr == nil {
					fields["model_closed"] = true
				} else {
					fields["model_close_error"] = closeErr.Error()
				}
			}

			telemetry.LogPhase(rec, "model_session_closeout", fields)

			if strings.TrimSpace(taskFamily) == "implementation" {
				telemetry.LogPhase(rec, "implement_session_closeout", fields)
			}
		})

		return closeErr
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
