// idiom-e2e drives a real multi-turn session against a live llama-server and
// prints a verification summary. Use to confirm the runner's decisioning loop
// actually works end-to-end. Requires llama-server running at cfg.BaseURL().
package e2e
import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/agent"
	"idiom/internal/config"
	"idiom/internal/llama"
	"idiom/internal/workspace"
)

func Main() {
	var (
		cfgPath  = flag.String("config", "config.json", "path to config.json")
		prompt   = flag.String("prompt", "Can you identify an opportunity to make a small refactor in this codebase? Be specific about files and lines.", "user prompt (single-turn)")
		prompts  multiPrompt
		modeName = flag.String("mode", "build", "agent mode: build|plan|general")
		logPath  = flag.String("log", "", "path to write session JSONL (default: logs/e2e/<timestamp>.jsonl)")
		timeout  = flag.Duration("timeout", 5*time.Minute, "overall run timeout")
	)
	flag.Var(&prompts, "turn", "user prompt for a turn (repeatable; when set, -prompt is ignored)")
	flag.Parse()
	if len(prompts) == 0 {
		prompts = multiPrompt{*prompt}
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fatalf("load config: %v", err)
	}

	if strings.TrimSpace(*logPath) == "" {
		_ = os.MkdirAll("logs/e2e", 0o755)
		*logPath = filepath.Join("logs/e2e", time.Now().Format("20060102-150405")+".jsonl")
	}
	logFile, err := os.Create(*logPath)
	if err != nil {
		fatalf("create log: %v", err)
	}
	defer logFile.Close()
	logEnc := json.NewEncoder(logFile)
	emit := func(kind string, payload any) {
		_ = logEnc.Encode(map[string]any{
			"ts":      time.Now().Format(time.RFC3339Nano),
			"kind":    kind,
			"payload": payload,
		})
	}

	client := llama.NewClient(cfg.BaseURL(), 0).WithBackend(cfg.LlamaServer.Kind)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// Ensure server is reachable + capture the model ID.
	models, err := client.ListModels(ctx)
	if err != nil {
		fatalf("llama-server not reachable at %s: %v", cfg.BaseURL(), err)
	}
	modelID := ""
	if len(models) > 0 {
		modelID = models[0].ID
	}

	ws := workspace.NewManager(cfg.Workspace)
	mode := agent.ModeOrDefault(*modeName)
	ws.SetPermissionRules(mode.PermissionRules)

	// Build a minimal system prompt — same composition as lifecycle.go:
	// base.md + mode prompt files. NO workspace.Instructions() (the
	// post-parity shape).
	system := loadFile("prompts/base.md")
	for _, f := range mode.SystemPromptFiles {
		if s := loadFile(f); s != "" {
			system = system + "\n\n" + s
		}
	}

	allowedTools := ws.AllowedTools()
	emit("config", map[string]any{
		"base_url":              cfg.BaseURL(),
		"mode":                  mode.Name,
		"workspace_mode":        string(ws.Mode()),
		"workspace_root":        ws.Root(),
		"system_prompt_chars":   len(system),
		"turns":                 []string(prompts),
		"model":                 modelID,
		"allowed_tools_count":   len(allowedTools),
		"allowed_tools_preview": firstN(allowedTools, 10),
	})

	history := []llama.ChatMessage{
		{Role: "system", Content: system},
	}

	gateCalls := 0
	runner := &agent.Runner{
		Client:              client,
		Workspace:           ws,
		MaxToolSteps:        mode.StepCap,
		Policy:              agent.ModePolicy(mode),
		AgentName:           "main",
		InstructionAttacher: e2eInstructionAttacher(cfg.Chat, ws.Root()),
		OnEvent: func(event agent.RunEvent) {
			emit("run_event", event)
		},
		DoomLoopGate: func(req workspace.ToolRequest, count int) bool {
			gateCalls++
			emit("doom_loop_gate", map[string]any{
				"tool":  req.Tool,
				"count": count,
			})
			// Auto-deny in e2e to prove the path fires cleanly if a loop happens.
			return false
		},
	}

	opts := llama.ChatOptions{
		Model:              modelID,
		Temperature:        cfg.Chat.Temperature,
		TopP:               cfg.Chat.TopP,
		TopK:               cfg.Chat.TopK,
		MinP:               cfg.Chat.MinP,
		PresencePenalty:    cfg.Chat.PresencePenalty,
		RepetitionPenalty:  cfg.Chat.RepetitionPenalty,
		EnableThinking:     cfg.Chat.EnableThinking,
		PreserveThinking:   cfg.Chat.PreserveThinking,
		ChatTemplateKwargs: cfg.Chat.EnableThinking != nil && !*cfg.Chat.EnableThinking,
		MaxTokens:          cfg.Chat.MaxTokens,
	}

	toolReqs := 0
	toolResults := 0
	fingerprints := map[string]int{}
	start := time.Now()

	var lastAnswer string
	for i, userPrompt := range prompts {
		emit("user_turn", map[string]any{"index": i, "prompt": userPrompt})
		history = append(history, llama.ChatMessage{Role: "user", Content: userPrompt})

		answer, rerr := runner.Run(
			ctx,
			history,
			opts,
			func(status string) { emit("status", status) },
			func(event agent.ToolRequestEvent) {
				toolReqs++
				fp := event.Request.Tool + "|" + event.Raw
				fingerprints[fp]++
				emit("tool_request", map[string]any{
					"turn":   i,
					"step":   event.Step,
					"tool":   event.Request.Tool,
					"raw":    event.Raw,
					"action": event.Action,
				})
			},
			func(event agent.ToolResultEvent) {
				toolResults++
				emit("tool_result", map[string]any{
					"turn":    i,
					"step":    event.Step,
					"tool":    event.Result.Tool,
					"summary": event.Result.Summary,
					"error":   event.Result.Error,
				})
			},
		)
		if rerr != nil {
			err = rerr
			emit("turn_error", map[string]any{"index": i, "error": rerr.Error()})
			break
		}
		emit("assistant_turn", map[string]any{"index": i, "chars": len(answer)})
		lastAnswer = answer
		history = append(history, llama.ChatMessage{Role: "assistant", Content: answer})
	}
	answer := lastAnswer
	elapsed := time.Since(start)

	runErr := ""
	if err != nil {
		runErr = err.Error()
	}
	maxFpCount := 0
	for _, n := range fingerprints {
		if n > maxFpCount {
			maxFpCount = n
		}
	}
	summary := map[string]any{
		"elapsed_ms":          elapsed.Milliseconds(),
		"tool_requests":       toolReqs,
		"tool_results":        toolResults,
		"unique_tool_calls":   len(fingerprints),
		"max_duplicate_calls": maxFpCount,
		"doom_loop_gate_hits": gateCalls,
		"answer_chars":        len(answer),
		"run_error":           runErr,
	}
	emit("summary", summary)

	fmt.Println("=== e2e session summary ===")
	fmt.Printf("log          : %s\n", *logPath)
	fmt.Printf("elapsed      : %s\n", elapsed)
	fmt.Printf("mode         : %s\n", mode.Name)
	fmt.Printf("system chars : %d\n", len(system))
	fmt.Printf("tool requests: %d\n", toolReqs)
	fmt.Printf("tool results : %d\n", toolResults)
	fmt.Printf("unique calls : %d\n", len(fingerprints))
	fmt.Printf("max dup calls: %d\n", maxFpCount)
	fmt.Printf("gate hits    : %d\n", gateCalls)
	fmt.Printf("answer chars : %d\n", len(answer))
	if runErr != "" {
		fmt.Printf("run error    : %s\n", runErr)
	}
	fmt.Println("--- answer ---")
	fmt.Println(answer)
}

type multiPrompt []string

func (m *multiPrompt) String() string     { return strings.Join(*m, " || ") }
func (m *multiPrompt) Set(s string) error { *m = append(*m, s); return nil }

func e2eInstructionAttacher(chat config.Chat, root string) *agent.InstructionAttacher {
	if !chat.AttachInstructionFiles {
		return nil
	}
	if chat.ProjectPromptCandidates == nil {
		return agent.NewInstructionAttacher(root)
	}
	candidates := e2eInstructionCandidates(chat.ProjectPromptCandidates)
	if len(candidates) == 0 {
		return nil
	}
	return agent.NewInstructionAttacher(root, candidates...)
}

func e2eInstructionCandidates(names []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || name != filepath.Base(name) {
			continue
		}
		switch name {
		case ".qwen.md", "QWEN.md", "IDIOM.md", "AGENTS.md", "CLAUDE.md":
		default:
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func firstN[T any](s []T, n int) []T {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func loadFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "idiom-e2e: "+format+"\n", args...)
	os.Exit(1)
}
