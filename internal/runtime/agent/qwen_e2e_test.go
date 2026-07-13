//go:build qwen_e2e
// +build qwen_e2e

package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/promptstack"
	"idiom/internal/runtime/workerruntime"
)

func thinkingOff() *bool { v := false; return &v }

var _ = ginkgo.Describe("live Qwen end-to-end behavior", ginkgo.Label("e2e", "live"), func() {
	ginkgo.It("completes a read-only query with source lines", func() {
		baseURL := os.Getenv("QWEN_E2E_URL")
		if baseURL == "" {
			baseURL = "http://127.0.0.1:47780"
		}

		client := llama.NewClient(baseURL, 30*time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()

		models, err := client.ListModels(ctx)
		if err != nil {
			ginkgo.Skip(fmt.Sprintf("Qwen backend not reachable at %s: %v", baseURL, err))
		}
		modelID := ""
		if len(models) > 0 {
			modelID = models[0].ID
		}
		fmt.Fprintf(ginkgo.GinkgoWriter, "Qwen model: %s\n", modelID)

		// --- workspace ---
		root := ginkgo.GinkgoT().TempDir()
		writeRunnerE2EFile(root, "go.mod", "module example.com/idiom\n\ngo 1.24\n")
		writeRunnerE2EFile(root, "internal/repository/comprehension/pack.go", `package comprehension

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
		gomega.Expect(mgr.SetMode(workspace.ModeExplore)).To(gomega.Succeed())

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
				{Role: "user", Content: "What does the Packet type in internal/repository/comprehension/pack.go do? Show me the key methods."},
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
		fmt.Fprintf(ginkgo.GinkgoWriter, "completed in %v, answer length=%d\n", elapsed.Round(time.Millisecond), len(answer))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		// --- assertions ---
		gomega.Expect(len(answer)).To(gomega.BeNumerically(">=", 50), answer)

		hasPacket := strings.Contains(answer, "Packet") || strings.Contains(answer, "packet")
		hasPath := strings.Contains(answer, "pack.go") || strings.Contains(answer, "comprehension")
		fmt.Fprintf(ginkgo.GinkgoWriter, "mentions Packet=%v path=%v\n", hasPacket, hasPath)
		gomega.Expect(hasPacket).To(gomega.BeTrue(), "full answer:\n%s", answer)

		// Check that the model used at least one discovery tool.
		toolsUsed := map[string]int{}
		for _, r := range results {
			toolsUsed[r.Tool]++
		}
		fmt.Fprintf(ginkgo.GinkgoWriter, "tools used: %v\n", toolsUsed)
		gomega.Expect(toolsUsed["read_file"] + toolsUsed["code_navigate"]).To(gomega.BeNumerically(">", 0))

		// Check for doom-loop guard.
		for _, ev := range events {
			gomega.Expect(ev.Role).NotTo(gomega.Equal("doom_loop_guard"))
		}
	})

	ginkgo.It("completes a read-only query through textual tools", func() {
		baseURL := os.Getenv("QWEN_E2E_URL")
		if baseURL == "" {
			baseURL = "http://127.0.0.1:47780"
		}

		client := llama.NewClient(baseURL, 30*time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		models, err := client.ListModels(ctx)
		if err != nil {
			ginkgo.Skip(fmt.Sprintf("Qwen backend not reachable at %s: %v", baseURL, err))
		}
		modelID := ""
		if len(models) > 0 {
			modelID = models[0].ID
		}
		fmt.Fprintf(ginkgo.GinkgoWriter, "model: %s\n", modelID)

		// --- workspace ---
		root := ginkgo.GinkgoT().TempDir()
		writeRunnerE2EFile(root, "go.mod", "module example.com/idiom\n\ngo 1.24\n")
		writeRunnerE2EFile(root, "internal/repository/comprehension/pack.go", "package comprehension\n\nimport \"fmt\"\n\n// Packet bundles ranked candidates for a task query.\ntype Packet struct {\n\tTask    string   `json:\"task\"`\n\tFiles   []string `json:\"files\"`\n\tSymbols []string `json:\"symbols\"`\n}\n\nfunc NewPacket(task string) *Packet { return &Packet{Task: task} }\nfunc (p *Packet) Summary() string { return fmt.Sprintf(\"packet: %d files\", len(p.Files)) }\n")

		mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: false})
		gomega.Expect(mgr.SetMode(workspace.ModeExplore)).To(gomega.Succeed())

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
					fmt.Fprintf(ginkgo.GinkgoWriter, "  normalizer: %s\n", event.Content[:min(200, len(event.Content))])
				case "completion_gate":
					fmt.Fprintf(ginkgo.GinkgoWriter, "  completion_gate: summary=%s error=%s\n",
						event.Content[:min(100, len(event.Content))], "")
				}
			},
		}

		fmt.Fprintln(ginkgo.GinkgoWriter, "starting run...")
		start := time.Now()
		answer, err := runner.Run(
			ctx,
			[]llama.ChatMessage{
				{Role: "system", Content: systemPrompt},
				{Role: "user", Content: "What does the Packet type in internal/repository/comprehension/pack.go do?"},
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
				fmt.Fprintf(ginkgo.GinkgoWriter, "  tool[%s]: %s\n", event.Result.Tool, s[:min(200, len(s))])
			},
		)
		elapsed := time.Since(start)
		fmt.Fprintf(ginkgo.GinkgoWriter, "completed in %v, answer=%d chars\n", elapsed.Round(time.Millisecond), len(answer))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "full answer on error: %s", answer)

		// --- assertions ---
		fmt.Fprintf(ginkgo.GinkgoWriter, "answer: %s\n", answer[:min(500, len(answer))])
		gomega.Expect(len(answer)).To(gomega.BeNumerically(">=", 50), answer)
		gomega.Expect(strings.ToLower(answer)).To(gomega.ContainSubstring("packet"))

		toolsUsed := map[string]int{}
		for _, r := range results {
			toolsUsed[r.Tool]++
		}
		fmt.Fprintf(ginkgo.GinkgoWriter, "tools used: %v\n", toolsUsed)
		gomega.Expect(toolsUsed["read_file"] + toolsUsed["code_navigate"] + toolsUsed["discovery_query"]).To(gomega.BeNumerically(">", 0))

		for _, ev := range events {
			gomega.Expect(ev.Role).NotTo(gomega.ContainSubstring("doom_loop"), ev.Content)
			if ev.Role == "completion_gate" {
				fmt.Fprintln(ginkgo.GinkgoWriter, "completion gate fired (may be expected for final answer routing)")
			}
		}
	})
})
