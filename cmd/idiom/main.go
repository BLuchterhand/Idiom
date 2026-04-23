package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"idiom/internal/app"
	"idiom/internal/config"
	"idiom/internal/llama"
)

type runOptions struct {
	JSONEvents bool
	Resume     string
}

type promptRunner func(config.Config, string, runOptions, io.Writer) (string, error)
type promptsRunner func(config.Config, []string, runOptions, io.Writer) ([]string, error)
type modeRunner func(config.Config, runOptions) error
type listSessionsRunner func(config.Config, io.Writer) error

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, runPromptOnce, runPromptsOnce, runTUI, runServerOnly, runListSessions))
}

func run(args []string, stdout io.Writer, stderr io.Writer, promptRun promptRunner, promptsRun promptsRunner, tuiRun modeRunner, serverRun modeRunner, listSessions listSessionsRunner) int {
	var configPath string
	var serverOnly bool
	var prompt string
	var turnsFile string
	var jsonEvents bool
	var resume string
	var listSessionsFlag bool

	fs := flag.NewFlagSet("idiom", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&configPath, "config", "config.json", "path to config file")
	fs.BoolVar(&serverOnly, "server-only", false, "run only llama-server and block")
	fs.StringVar(&prompt, "prompt", "", "run one prompt headlessly, print the answer, and exit")
	fs.StringVar(&turnsFile, "turns-file", "", "run newline-delimited or JSON-array prompts headlessly in one session, print answers, and exit")
	fs.BoolVar(&jsonEvents, "json", false, "emit structured JSONL events to stdout (only with --prompt)")
	fs.StringVar(&resume, "resume", "", "resume a prior session by id (filename without .jsonl) before running")
	fs.BoolVar(&listSessionsFlag, "list-sessions", false, "print recent session ids from the debug log directory and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	if listSessionsFlag {
		if err := listSessions(cfg, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}

	if serverOnly {
		if err := serverRun(cfg, runOptions{}); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}

	if turnsFile != "" {
		if prompt != "" {
			fmt.Fprintln(stderr, "--prompt and --turns-file are mutually exclusive")
			return 2
		}
		prompts, err := loadPromptTurns(turnsFile)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		opts := runOptions{JSONEvents: jsonEvents, Resume: resume}
		answers, err := promptsRun(cfg, prompts, opts, stdout)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if !jsonEvents {
			for _, answer := range answers {
				fmt.Fprintln(stdout, answer)
			}
		}
		return 0
	}

	if prompt != "" {
		opts := runOptions{JSONEvents: jsonEvents, Resume: resume}
		answer, err := promptRun(cfg, prompt, opts, stdout)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if !jsonEvents {
			fmt.Fprintln(stdout, answer)
		}
		return 0
	}

	if err := tuiRun(cfg, runOptions{Resume: resume}); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func loadPromptTurns(path string) ([]string, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read turns file: %w", err)
	}
	var prompts []string
	if err := json.Unmarshal(data, &prompts); err == nil {
		return cleanPromptTurns(prompts)
	}
	scanner := bufio.NewScanner(bytesReader(data))
	for scanner.Scan() {
		prompts = append(prompts, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan turns file: %w", err)
	}
	return cleanPromptTurns(prompts)
}

func bytesReader(data []byte) io.Reader {
	return strings.NewReader(string(data))
}

func cleanPromptTurns(prompts []string) ([]string, error) {
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

func runTUI(cfg config.Config, opts runOptions) error {
	model := app.New(cfg)
	defer model.Close()

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

func runPromptOnce(cfg config.Config, prompt string, opts runOptions, stdout io.Writer) (string, error) {
	model := app.New(cfg)
	defer model.Close()

	if opts.Resume != "" {
		path := app.ResolveSessionLogPath(cfg, opts.Resume)
		if err := model.LoadHistoryFromSession(path); err != nil {
			return "", fmt.Errorf("resume %q: %w", opts.Resume, err)
		}
	}

	if opts.JSONEvents {
		sessionID := ""
		if path := model.DebugLogPath(); path != "" {
			sessionID = sessionIDFromPath(path)
		}
		model.SetJSONEventStream(stdout, sessionID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Chat.RequestTimeoutSeconds+cfg.Chat.ServerStartTimeoutSecs)*time.Second)
	defer cancel()
	return model.RunPromptOnce(ctx, prompt)
}

func runPromptsOnce(cfg config.Config, prompts []string, opts runOptions, stdout io.Writer) ([]string, error) {
	model := app.New(cfg)
	defer model.Close()

	if opts.Resume != "" {
		path := app.ResolveSessionLogPath(cfg, opts.Resume)
		if err := model.LoadHistoryFromSession(path); err != nil {
			return nil, fmt.Errorf("resume %q: %w", opts.Resume, err)
		}
	}

	if opts.JSONEvents {
		sessionID := ""
		if path := model.DebugLogPath(); path != "" {
			sessionID = sessionIDFromPath(path)
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

func sessionIDFromPath(path string) string {
	base := filepath.Base(path)
	if ext := filepath.Ext(base); ext != "" {
		base = base[:len(base)-len(ext)]
	}
	return base
}

func runListSessions(cfg config.Config, stdout io.Writer) error {
	sessions, err := app.ListRecordedSessions(cfg)
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

func runServerOnly(cfg config.Config, _ runOptions) error {
	client := llama.NewClient(cfg.BaseURL(), 0)
	controller := llama.NewServerController(cfg.LlamaServer, client)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Chat.ServerStartTimeoutSecs)*time.Second)
	defer cancel()

	if err := controller.Start(ctx); err != nil {
		return err
	}
	if err := controller.WaitUntilReady(ctx); err != nil {
		return err
	}

	fmt.Printf("llama-server ready on %s\n", cfg.BaseURL())
	fmt.Printf("model: %s\n", cfg.LlamaServer.Model)
	fmt.Printf("log: %s\n", controller.LogPath())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer stopCancel()

	if err := controller.Stop(stopCtx); err != nil {
		return err
	}
	return nil
}
