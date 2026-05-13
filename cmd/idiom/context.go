package main

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

	"idiom/internal/telemetry"
	"idiom/internal/workspace"
)

type contextOptions struct {
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

func runContextMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return runContextMainWithTelemetry(args, stdout, stderr, nil)
}

func runContextMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printContextUsage(stderr)
		return 0
	}
	logCLIPhase(rec, "context_start", map[string]any{"subcommand": args[0]})
	switch args[0] {
	case "build":
		return runContextTool(args[1:], stdout, stderr, rec, "context_index")
	case "status":
		return runContextTool(args[1:], stdout, stderr, rec, "context_status")
	case "query":
		return runContextTool(args[1:], stdout, stderr, rec, "context_query")
	case "zoom":
		return runContextTool(args[1:], stdout, stderr, rec, "context_zoom")
	case "pack":
		return runContextTool(args[1:], stdout, stderr, rec, "context_pack")
	case "impact":
		return runContextTool(args[1:], stdout, stderr, rec, "context_impact")
	case "trace":
		return runContextTool(args[1:], stdout, stderr, rec, "context_trace_pack")
	default:
		fmt.Fprintf(stderr, "unknown context command: %s\n", args[0])
		printContextUsage(stderr)
		return 2
	}
}

func runContextTool(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder, tool string) int {
	opts := contextOptions{Config: "config.json", Language: "auto"}
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
	fs.IntVar(&opts.Limit, "limit", 0, "result limit")
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

	cfg, err := loadToolConfig(opts.Config)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	task, err := resolveContextTask(opts.Task, opts.TaskFile)
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
	logDirectToolRequest(rec, req, 0)
	mgr := workspace.NewManager(cfg.Workspace)
	ctx := context.Background()
	cancel := func() {}
	timeout := time.Duration(opts.TimeoutSeconds) * time.Second
	if opts.TimeoutSeconds > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	startedAt := time.Now()
	result := executeContextToolWithTimeout(ctx, mgr, req, timeout)
	logDirectToolResult(rec, req, result, time.Since(startedAt).Milliseconds())
	if result.Error != "" {
		fmt.Fprintln(stderr, result.Error)
		return 1
	}
	if strings.TrimSpace(opts.OutputPath) != "" {
		data, err := contextResultBytes(result)
		if err != nil {
			fmt.Fprintf(stderr, "encode result: %v\n", err)
			return 1
		}
		if err := writeArchitectOutput(opts.OutputPath, data, stdout); err != nil {
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

func printContextUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "Usage:")
	fmt.Fprintln(stderr, "  idiom context build [--path .] [--language auto]")
	fmt.Fprintln(stderr, "  idiom context status [--refresh] [--path <bounded-root>]")
	fmt.Fprintln(stderr, "  idiom context query --query 'what calls X' [--resolution symbol]")
	fmt.Fprintln(stderr, "  idiom context zoom --id 'symbol://path.go#Name'")
	fmt.Fprintln(stderr, "  idiom context pack --task 'plan a bounded change' [--path .] [--output view.json]")
	fmt.Fprintln(stderr, "  idiom context impact --task 'plan a bounded change' [--path .]")
	fmt.Fprintln(stderr, "  idiom context trace --command 'go test ./...' [--language auto]")
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "Builds, queries, and enriches idiom's persistent codebase comprehension graph.")
}

func executeContextToolWithTimeout(ctx context.Context, mgr *workspace.Manager, req workspace.ToolRequest, timeout time.Duration) workspace.ToolResult {
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

func contextResultBytes(result workspace.ToolResult) ([]byte, error) {
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

func resolveContextTask(task string, taskFile string) (string, error) {
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
