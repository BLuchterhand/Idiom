//go:build qwen_e2e
// +build qwen_e2e

package agent

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"idiom/internal/config"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
	"idiom/internal/promptstack"
	"idiom/internal/workspace"
)

func thinkingOff() *bool { v := false; return &v }

// TestQwenE2EReadOnlyWithLines verifies a Qwen model can complete a
// read-only conversation lane query against a real Go codebase.
//
// Requires a running Qwen backend at QWEN_E2E_URL (default
// http://127.0.0.1:47780).  Skip with -short or when the backend is
// unreachable.
func TestQwenE2EReadOnlyWithLines(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	baseURL := os.Getenv("QWEN_E2E_URL")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:47780"
	}

	client := llama.NewClient(baseURL, 30*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	models, err := client.ListModels(ctx)
	if err != nil {
		t.Skipf("Qwen backend not reachable at %s: %v", baseURL, err)
	}
	modelID := ""
	if len(models) > 0 {
		modelID = models[0].ID
	}
	t.Logf("Qwen model: %s", modelID)

	// --- workspace ---
	root := t.TempDir()
	writeTestFile(root, "go.mod", "module example.com/idiom\n\ngo 1.24\n")
	writeTestFile(root, "internal/comprehension/pack.go", `package comprehension

import "fmt"

// Packet bundles ranked candidates for a task query.
type Packet struct {
	Task    string   `+"`"+`json:"task"`+"`"+`
	Files   []string `+"`"+`json:"files"`+"`"+`
	Symbols []string `+"`"+`json:"symbols"`+"`"+`
}

// NewPacket creates a Packet with the given task string.
func NewPacket(task string) *Packet {
	return &Packet{Task: task}
}

// AddFile appends a candidate file path to the packet.
func (p *Packet) AddFile(path string) {
	p.Files = append(p.Files, path)
}

// Summary returns a one-line description of the packet contents.
func (p *Packet) Summary() string {
	return fmt.Sprintf("packet %q: %d files, %d symbols", p.Task, len(p.Files), len(p.Symbols))
}
`)

	mgr := workspace.NewManager(config.Workspace{
		Root:         root,
		CWD:          root,
		EnableWrites: false,
	})
	if err := mgr.SetMode(workspace.ModeExplore); err != nil {
		t.Fatalf("set explore mode: %v", err)
	}

	systemPrompt := `You are a coding agent. Use the provided tools to inspect the codebase, then answer the user's question. Cite file paths in your answer.`

	// --- runner ---
	events := []RunEvent(nil)
	results := []workspace.ToolResult(nil)

	runner := &Runner{
		Client:    client,
		Workspace: mgr,
		Policy:    RunPolicy{MaxToolSteps: 0, MaxNoProgressTurns: 8, MaxModelCalls: 12},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	start := time.Now()
	answer, err := runner.Run(
		ctx,
		[]llama.ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: "What does the Packet type in internal/comprehension/pack.go do? Show me the key methods."},
		},
		llama.ChatOptions{
			MaxTokens:          8192,
			TopP:               0.95,
			Temperature:        0.7,
			EnableThinking:     thinkingOff(),
			ChatTemplateKwargs: true,
			ForceNativeTools:   true, // send OpenAI tools array to Qwen
		},
		nil,
		nil,
		func(event ToolResultEvent) {
			results = append(results, event.Result)
		},
	)
	elapsed := time.Since(start)
	t.Logf("completed in %v, answer length=%d", elapsed.Round(time.Millisecond), len(answer))

	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// --- assertions ---
	if len(answer) < 50 {
		t.Fatalf("answer too short (%d chars): %q", len(answer), answer)
	}

	hasPacket := strings.Contains(answer, "Packet") || strings.Contains(answer, "packet")
	hasPath := strings.Contains(answer, "pack.go") || strings.Contains(answer, "comprehension")
	t.Logf("mentions Packet=%v path=%v", hasPacket, hasPath)

	if !hasPacket {
		t.Logf("full answer:\n%s", answer)
		t.Fatalf("answer does not mention Packet type")
	}

	// Check that the model used at least one discovery tool.
	toolsUsed := map[string]int{}
	for _, r := range results {
		toolsUsed[r.Tool]++
	}
	t.Logf("tools used: %v", toolsUsed)
	if toolsUsed["read_file"]+toolsUsed["code_navigate"] == 0 {
		t.Fatalf("expected at least one read_file or code_navigate call, got tools=%v", toolsUsed)
	}

	// Check for doom-loop guard.
	for _, ev := range events {
		if ev.Role == "doom_loop_guard" {
			t.Fatalf("doom loop guard triggered; model got stuck repeating tools")
		}
	}
}

// TestQwenE2ETextualToolsEndToEnd verifies a complete read-only
// conversation lane query against a live Qwen backend using the
// textual key-value tool format (no ForceNativeTools). This is
// the default MLX path where tools are stripped and the model
// must follow overlay instructions.
func TestQwenE2ETextualToolsEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	baseURL := os.Getenv("QWEN_E2E_URL")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:47780"
	}

	client := llama.NewClient(baseURL, 30*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	models, err := client.ListModels(ctx)
	if err != nil {
		t.Skipf("Qwen backend not reachable at %s: %v", baseURL, err)
	}
	modelID := ""
	if len(models) > 0 {
		modelID = models[0].ID
	}
	t.Logf("model: %s", modelID)

	// --- workspace ---
	root := t.TempDir()
	writeTestFile(root, "go.mod", "module example.com/idiom\n\ngo 1.24\n")
	writeTestFile(root, "internal/comprehension/pack.go", "package comprehension\n\nimport \"fmt\"\n\n// Packet bundles ranked candidates for a task query.\ntype Packet struct {\n\tTask    string   `json:\"task\"`\n\tFiles   []string `json:\"files\"`\n\tSymbols []string `json:\"symbols\"`\n}\n\nfunc NewPacket(task string) *Packet { return &Packet{Task: task} }\nfunc (p *Packet) Summary() string { return fmt.Sprintf(\"packet: %d files\", len(p.Files)) }\n")

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: false})
	if err := mgr.SetMode(workspace.ModeExplore); err != nil {
		t.Fatalf("set explore mode: %v", err)
	}

	// --- prompt with Qwen overlay ---
	overlays := promptstack.ModelCompatibilityOverlays("qwen", modelID)
	stack := promptstack.AppendOverlays(promptstack.Stack{}, overlays)
	systemPrompt := "You are a coding agent. Answer the user. Use workspace tools to inspect the codebase.\n\n" + stack.Text

	// --- runner ---
	events := []RunEvent(nil)
	results := []workspace.ToolResult(nil)
	runner := &Runner{
		Client:    client,
		Workspace: mgr,
		Policy:    RunPolicy{MaxToolSteps: 0, MaxNoProgressTurns: 8, MaxModelCalls: 15},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
			switch event.Role {
			case "tool_request_normalizer":
				t.Logf("  normalizer: %s", event.Content[:min(200, len(event.Content))])
			case "completion_gate":
				t.Logf("  completion_gate: summary=%s error=%s",
					event.Content[:min(100, len(event.Content))], "")
			}
		},
	}

	t.Logf("starting run...")
	start := time.Now()
	answer, err := runner.Run(
		ctx,
		[]llama.ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: "What does the Packet type in internal/comprehension/pack.go do?"},
		},
		llama.ChatOptions{
			MaxTokens:          4096,
			TopP:               0.95,
			Temperature:        0.7,
			EnableThinking:     thinkingOff(),
			ChatTemplateKwargs: true,
			// No ForceNativeTools: model uses textual key-value format.
		},
		nil, nil,
		func(event ToolResultEvent) {
			results = append(results, event.Result)
			s := event.Result.Summary
			if event.Result.Error != "" {
				s = event.Result.Error
			}
			t.Logf("  tool[%s]: %s", event.Result.Tool, s[:min(200, len(s))])
		},
	)
	elapsed := time.Since(start)
	t.Logf("completed in %v, answer=%d chars", elapsed.Round(time.Millisecond), len(answer))

	if err != nil {
		t.Logf("full answer on error: %s", answer)
		t.Fatalf("Run: %v", err)
	}

	// --- assertions ---
	t.Logf("answer: %s", answer[:min(500, len(answer))])

	if len(answer) < 50 {
		t.Fatalf("answer too short: %q", answer)
	}
	if !strings.Contains(answer, "Packet") && !strings.Contains(answer, "packet") {
		t.Fatalf("answer does not mention Packet type")
	}

	toolsUsed := map[string]int{}
	for _, r := range results {
		toolsUsed[r.Tool]++
	}
	t.Logf("tools used: %v", toolsUsed)
	if toolsUsed["read_file"]+toolsUsed["code_navigate"]+toolsUsed["discovery_query"] == 0 {
		t.Fatalf("no discovery tool used; tools=%v", toolsUsed)
	}

	for _, ev := range events {
		if strings.Contains(ev.Role, "doom_loop") {
			t.Fatalf("doom loop triggered: %s", ev.Content)
		}
		if ev.Role == "completion_gate" {
			t.Logf("completion gate fired (may be expected for final answer routing)")
		}
	}
}
