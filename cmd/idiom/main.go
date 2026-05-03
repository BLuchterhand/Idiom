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
	"idiom/internal/workspace"
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
	if len(os.Args) > 1 && os.Args[1] == "tool" {
		os.Exit(runToolMain(os.Args[2:], os.Stdout, os.Stderr))
	}
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

func runToolMain(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		printToolUsage(stderr)
		return 0
	}
	if args[0] == "list" {
		return runToolList(stdout)
	}

	toolName := args[0]
	toolFs := flag.NewFlagSet("idiom tool "+toolName, flag.ContinueOnError)
	toolFs.SetOutput(stderr)
	configPath := toolFs.String("config", "config.json", "path to config file")
	inputJSON := toolFs.String("input", "", "JSON object with tool request parameters (reads from stdin if omitted and not a terminal)")
	if err := toolFs.Parse(args[1:]); err != nil {
		return 2
	}

	cfg, err := loadToolConfig(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	req := workspace.ToolRequest{Tool: toolName}
	var rawInput []byte
	if *inputJSON != "" {
		rawInput = []byte(*inputJSON)
	} else {
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				fmt.Fprintf(stderr, "read stdin: %v\n", err)
				return 1
			}
			rawInput = data
		}
	}
	if len(rawInput) > 0 {
		if err := json.Unmarshal(rawInput, &req); err != nil {
			fmt.Fprintf(stderr, "invalid input JSON: %v\n", err)
			return 1
		}
		// Preserve the full input as Args so external tools (and any
		// future MCP-shaped tools) can pull arbitrary fields, not just
		// the ones declared on ToolRequest.
		req.Args = json.RawMessage(rawInput)
	}
	req.Tool = toolName

	mgr := workspace.NewManager(cfg.Workspace)
	mgr.SetBypassReadBeforeEdit(true)
	if extReg, extErr := loadExternalToolsRegistry(cfg.Workspace); extErr != nil {
		fmt.Fprintf(stderr, "external tools setup: %v\n", extErr)
		return 1
	} else if extReg != nil {
		mgr.SetExternalToolsRegistry(extReg)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result := mgr.Execute(ctx, req)

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		fmt.Fprintf(stderr, "encode result: %v\n", err)
		return 1
	}

	if result.Error != "" {
		return 1
	}
	return 0
}

func runToolList(stdout io.Writer) int {
	allTools := workspace.ToolManifestTools(workspace.ToolManifestToolOptions{
		IncludeTaskLedger: true,
		IncludeTask:       true,
		IncludeWriteTools: true,
		IncludeGo:         true,
		IncludeCpp:        true,
		IncludePython:     true,
		IncludeTypescript: true,
		IncludeRust:       true,
		IncludeJava:       true,
	})
	for _, t := range allTools {
		fmt.Fprintln(stdout, t)
	}
	return 0
}

func loadToolConfig(path string) (config.Config, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		cfg := config.Default()
		if wd, e := os.Getwd(); e == nil {
			cfg.Workspace.Root = wd
			cfg.Workspace.CWD = wd
		}
		return cfg, nil
	}
	return config.Load(path)
}

func printToolUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "Usage:")
	fmt.Fprintln(stderr, "  idiom tool <tool-name> [--input '<json>']")
	fmt.Fprintln(stderr, "  idiom tool <tool-name> [flags]  # reads JSON from stdin when piped")
	fmt.Fprintln(stderr, "  idiom tool list")
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "Run a single workspace tool and print its result as JSON.")
	fmt.Fprintln(stderr, "Does not require llama-server.")
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "Discovery tools:")
	fmt.Fprintln(stderr, "  discovery_query     Multi-layer analysis (impact, tracing, architecture, security, history)")
	fmt.Fprintln(stderr, "  structural_search   AST-aware pattern search (requires ast-grep)")
	fmt.Fprintln(stderr, "  dependency_graph    Language-aware dependency analysis")
	fmt.Fprintln(stderr, "  history_range       Git log for a line range")
	fmt.Fprintln(stderr, "  history_symbol      Git log for a symbol")
	fmt.Fprintln(stderr, "  blame_range         Git blame for a line range")
	fmt.Fprintln(stderr, "  ctags_symbols       Symbol indexing (requires ctags)")
	fmt.Fprintln(stderr, "  semgrep_scan        Semgrep security/pattern scan")
	fmt.Fprintln(stderr, "  joern_scan          Joern code property graph scan")
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "Use \"idiom tool list\" to see all available tools.")
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "Flags:")
	fmt.Fprintln(stderr, "  -config string  path to config file (default \"config.json\")")
	fmt.Fprintln(stderr, "  -input string   JSON object with tool request parameters")
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

// loadExternalToolsRegistry mirrors internal/app/external_tools_setup.go's
// setup function for the standalone `idiom tool` CLI path. Kept here as a
// thin local helper so cmd/ stays decoupled from internal/app — the
// shared semantics are documented in workspace.NewExternalToolRegistry.
func loadExternalToolsRegistry(ws config.Workspace) (*workspace.ExternalToolRegistry, error) {
	tools := make([]workspace.ExternalTool, 0, len(ws.ExternalTools))
	for _, def := range ws.ExternalTools {
		tools = append(tools, workspace.ExternalTool{
			Name:           def.Name,
			Description:    def.Description,
			Command:        def.Command,
			Args:           append([]string(nil), def.Args...),
			Cwd:            def.Cwd,
			TimeoutSeconds: def.TimeoutSeconds,
			InputSchema:    def.InputSchema,
		})
	}
	if dir := ws.ExternalToolsDir; dir != "" {
		dirPath := dir
		if !filepath.IsAbs(dirPath) && ws.Root != "" {
			dirPath = filepath.Join(ws.Root, dirPath)
		}
		entries, err := os.ReadDir(dirPath)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("external_tools_dir %q: %w", dirPath, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			path := filepath.Join(dirPath, entry.Name())
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("external tool file %q: %w", path, err)
			}
			var def config.ExternalToolDef
			if err := json.Unmarshal(raw, &def); err != nil {
				return nil, fmt.Errorf("external tool file %q: parse: %w", path, err)
			}
			tools = append(tools, workspace.ExternalTool{
				Name:           def.Name,
				Description:    def.Description,
				Command:        def.Command,
				Args:           append([]string(nil), def.Args...),
				Cwd:            def.Cwd,
				TimeoutSeconds: def.TimeoutSeconds,
				InputSchema:    def.InputSchema,
			})
		}
	}
	if len(tools) == 0 {
		return nil, nil
	}
	return workspace.NewExternalToolRegistry(tools)
}

func runServerOnly(cfg config.Config, _ runOptions) error {
	client := llama.NewClient(cfg.BaseURL(), 0).WithBackend(cfg.LlamaServer.Kind)
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
