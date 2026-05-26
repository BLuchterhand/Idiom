package contextcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/config"
	"idiom/internal/telemetry"
	"idiom/internal/workspace"
)

type ConfigLoader func(path string) (config.Config, error)
type PhaseLogger func(rec *telemetry.Recorder, eventType string, fields map[string]any)
type ToolRequestLogger func(rec *telemetry.Recorder, req workspace.ToolRequest, rawInputBytes int, profile string, caller workspace.ToolSidecarCaller)
type ToolResultLogger func(rec *telemetry.Recorder, req workspace.ToolRequest, result workspace.ToolResult, durationMillis int64, profile string, caller workspace.ToolSidecarCaller)
type OutputWriter func(path string, data []byte, stdout io.Writer) error

type Deps struct {
	LoadConfig     ConfigLoader
	LogPhase       PhaseLogger
	LogToolRequest ToolRequestLogger
	LogToolResult  ToolResultLogger
	WriteOutput    OutputWriter
}

type options struct {
	Config            string
	Path              string
	Language          string
	Mode              string
	Task              string
	TaskFile          string
	OutputPath        string
	Query             string
	Pattern           string
	ID                string
	Command           string
	Resolution        string
	Limit             int
	BudgetTokens      int
	TimeoutSeconds    int
	IncludeEvidence   bool
	IncludeSource     bool
	IncludeFiles      bool
	Refresh           bool
	RequiredTests     multiFlag
	RequiredEditPaths multiFlag
	ForbiddenPaths    multiFlag
}

type multiFlag []string

func (m *multiFlag) String() string {
	if m == nil {
		return ""
	}
	return strings.Join(*m, ",")
}

func (m *multiFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}

func Run(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder, deps Deps) int {
	deps = deps.withDefaults()
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printUsage(stderr)
		return 0
	}
	deps.LogPhase(rec, "context_start", map[string]any{"subcommand": args[0]})
	switch args[0] {
	case "build":
		return runTool(args[1:], stdout, stderr, rec, deps, "context_index")
	case "status":
		return runTool(args[1:], stdout, stderr, rec, deps, "context_status")
	case "query":
		return runTool(args[1:], stdout, stderr, rec, deps, "context_query")
	case "zoom":
		return runTool(args[1:], stdout, stderr, rec, deps, "context_zoom")
	case "pack":
		return runTool(args[1:], stdout, stderr, rec, deps, "context_pack")
	case "impact":
		return runTool(args[1:], stdout, stderr, rec, deps, "context_impact")
	case "trace":
		return runTool(args[1:], stdout, stderr, rec, deps, "context_trace_pack")
	default:
		fmt.Fprintf(stderr, "unknown context command: %s\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func (deps Deps) withDefaults() Deps {
	if deps.LoadConfig == nil {
		deps.LoadConfig = loadConfig
	}
	if deps.LogPhase == nil {
		deps.LogPhase = func(*telemetry.Recorder, string, map[string]any) {}
	}
	if deps.LogToolRequest == nil {
		deps.LogToolRequest = func(*telemetry.Recorder, workspace.ToolRequest, int, string, workspace.ToolSidecarCaller) {}
	}
	if deps.LogToolResult == nil {
		deps.LogToolResult = func(*telemetry.Recorder, workspace.ToolRequest, workspace.ToolResult, int64, string, workspace.ToolSidecarCaller) {
		}
	}
	if deps.WriteOutput == nil {
		deps.WriteOutput = writeOutput
	}
	return deps
}

func runTool(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder, deps Deps, tool string) int {
	opts := options{Config: "config.json", Language: "auto"}
	modeDefault := "standard"
	if tool == "context_pack" || tool == "context_impact" {
		modeDefault = "plan"
	}
	fs := flag.NewFlagSet("idiom context "+strings.TrimPrefix(tool, "context_"), flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.Config, "config", "config.json", "path to config file")
	fs.StringVar(&opts.Path, "path", "", "optional workspace-relative path scope")
	fs.StringVar(&opts.Language, "language", "auto", "language hint: auto, go, cpp, gdscript, python, typescript, javascript, rust, java, or another registered source family")
	fs.StringVar(&opts.Mode, "mode", modeDefault, "indexing mode for build or packet mode for pack")
	fs.StringVar(&opts.Task, "task", "", "task statement for context pack or impact ranking")
	fs.StringVar(&opts.TaskFile, "task-file", "", "read task statement for context pack or impact ranking from file")
	fs.StringVar(&opts.OutputPath, "output", "", "write command JSON to path; default stdout")
	fs.StringVar(&opts.Query, "query", "", "context query text")
	fs.StringVar(&opts.Pattern, "pattern", "", "symbol or pattern hint")
	fs.StringVar(&opts.ID, "id", "", "context graph handle")
	fs.StringVar(&opts.Command, "command", "", "runtime command for context trace packs")
	fs.StringVar(&opts.Resolution, "resolution", "", "view resolution: repository, package, file, symbol, or any")
	fs.IntVar(&opts.Limit, "limit", 0, "result limit; for context status, caps displayed stale/missing path samples and unscoped stale refresh attempts")
	fs.IntVar(&opts.BudgetTokens, "budget-tokens", 0, "optional intended downstream context budget")
	fs.IntVar(&opts.TimeoutSeconds, "timeout-seconds", 60, "tool timeout in seconds; set to 0 to disable")
	fs.BoolVar(&opts.IncludeEvidence, "include-evidence", false, "include relationship evidence")
	fs.BoolVar(&opts.IncludeSource, "include-source", false, "reserved for exact source excerpts")
	fs.BoolVar(&opts.IncludeFiles, "include-files", true, "include parsed executed-file and stack-frame evidence for trace packs")
	fs.BoolVar(&opts.Refresh, "refresh", false, "for context status, attempt bounded or capped stale graph refresh before reporting")
	fs.Var(&opts.RequiredTests, "required-test", "exact validation command to carry into context pack or impact ranking; repeatable")
	fs.Var(&opts.RequiredEditPaths, "required-edit-path", "workspace-relative edit path to surface and prioritize in context pack or impact ranking; repeatable")
	fs.Var(&opts.ForbiddenPaths, "forbidden-path", "workspace-relative path to omit from context pack or impact candidates; repeatable")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected positional argument: %s\n", fs.Arg(0))
		return 2
	}

	cfg, err := deps.LoadConfig(opts.Config)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	task, err := resolveTask(opts.Task, opts.TaskFile)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	req := workspace.ToolRequest{
		Tool:              tool,
		ID:                opts.ID,
		Path:              opts.Path,
		Pattern:           opts.Pattern,
		Query:             opts.Query,
		Command:           opts.Command,
		Task:              task,
		Language:          opts.Language,
		Mode:              opts.Mode,
		Resolution:        opts.Resolution,
		Limit:             opts.Limit,
		BudgetTokens:      opts.BudgetTokens,
		IncludeEvidence:   opts.IncludeEvidence,
		IncludeSource:     opts.IncludeSource,
		IncludeFiles:      opts.IncludeFiles,
		Refresh:           opts.Refresh,
		RequiredTests:     []string(opts.RequiredTests),
		RequiredEditPaths: []string(opts.RequiredEditPaths),
		ForbiddenPaths:    []string(opts.ForbiddenPaths),
	}
	deps.LogToolRequest(rec, req, 0, workspace.ToolProfileDefault, workspace.ToolSidecarCaller{})
	mgr := workspace.NewManager(cfg.Workspace)
	ctx := context.Background()
	cancel := func() {}
	timeout := time.Duration(opts.TimeoutSeconds) * time.Second
	if opts.TimeoutSeconds > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	startedAt := time.Now()
	result := executeWithTimeout(ctx, mgr, req, timeout)
	deps.LogToolResult(rec, req, result, time.Since(startedAt).Milliseconds(), workspace.ToolProfileDefault, workspace.ToolSidecarCaller{})
	if result.Error != "" {
		fmt.Fprintln(stderr, result.Error)
		return 1
	}
	if strings.TrimSpace(opts.OutputPath) != "" {
		data, err := resultBytes(result)
		if err != nil {
			fmt.Fprintf(stderr, "encode result: %v\n", err)
			return 1
		}
		if err := deps.WriteOutput(opts.OutputPath, data, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	if strings.TrimSpace(result.Output) != "" {
		fmt.Fprintln(stdout, result.Output)
		return 0
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		fmt.Fprintf(stderr, "encode result: %v\n", err)
		return 1
	}
	return 0
}

func printUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "Usage:")
	fmt.Fprintln(stderr, "  idiom context build [--path .] [--language auto]")
	fmt.Fprintln(stderr, "  idiom context status [--refresh] [--path <bounded-root>] [--limit <path-sample-cap>]")
	fmt.Fprintln(stderr, "  idiom context query --query 'what calls X' [--resolution symbol]")
	fmt.Fprintln(stderr, "  idiom context zoom --id 'symbol://path.go#Name'")
	fmt.Fprintln(stderr, "  idiom context pack --task 'plan a bounded change' [--path .] [--output view.json]")
	fmt.Fprintln(stderr, "  idiom context impact --task 'plan a bounded change' [--path .]")
	fmt.Fprintln(stderr, "  idiom context trace --command 'go test ./...' [--language auto]")
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "Builds, queries, and enriches idiom's persistent codebase comprehension graph.")
}

func executeWithTimeout(ctx context.Context, mgr *workspace.Manager, req workspace.ToolRequest, timeout time.Duration) workspace.ToolResult {
	if timeout <= 0 {
		return mgr.Execute(ctx, req)
	}
	done := make(chan workspace.ToolResult, 1)
	go func() {
		done <- mgr.Execute(ctx, req)
	}()
	select {
	case result := <-done:
		return result
	case <-ctx.Done():
		return workspace.ToolResult{
			Tool:    req.Tool,
			Summary: "Context tool timed out",
			Error:   fmt.Sprintf("%s timed out after %s; retry with a narrower --path/--language or --limit, or raise --timeout-seconds", req.Tool, timeout),
		}
	}
}

func resultBytes(result workspace.ToolResult) ([]byte, error) {
	if strings.TrimSpace(result.Output) != "" {
		return []byte(strings.TrimRight(result.Output, "\n") + "\n"), nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func resolveTask(task string, taskFile string) (string, error) {
	if strings.TrimSpace(task) != "" && strings.TrimSpace(taskFile) != "" {
		return "", fmt.Errorf("--task and --task-file are mutually exclusive")
	}
	if strings.TrimSpace(taskFile) == "" {
		return strings.TrimSpace(task), nil
	}
	data, err := os.ReadFile(filepath.Clean(taskFile))
	if err != nil {
		return "", fmt.Errorf("read task file: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func loadConfig(path string) (config.Config, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		cfg := config.Default()
		if wd, e := os.Getwd(); e == nil {
			wd = config.CanonicalizeExistingPath(wd)
			cfg.Workspace.Root = wd
			cfg.Workspace.CWD = wd
		}
		return cfg, nil
	}
	return config.Load(path)
}

func writeOutput(path string, data []byte, stdout io.Writer) error {
	if strings.TrimSpace(path) == "" || path == "-" {
		_, err := stdout.Write(data)
		return err
	}
	clean := filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(clean), 0o755); err != nil {
		return fmt.Errorf("create parent directory for %s: %w", clean, err)
	}
	if err := os.WriteFile(clean, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", clean, err)
	}
	return nil
}
