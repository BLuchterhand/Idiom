package agent

import (
	"context"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.Describe("Final-answer acceptance", ginkgo.Label("e2e"), func() {
	ginkgo.It("accepts a low-confidence source-backed direct answer", func() {
		root := newGoModuleTestWorkspace()
		gomega.Expect(writeTestFile(root, "docs/decisioning-north-star.md", strings.Join([]string{
			"# Decisioning North Star",
			"The model owns strategy; the harness owns contracts.",
			"Final answers must be grounded in verified source evidence rather than unverified model prose.",
			"Deterministic closeouts prevent raw unverified information from leaking to users.",
		}, "\n"))).To(gomega.Succeed())

		finalAnswer := "No. Free-form prose moves the closeout away from deterministic contract enforcement."
		replies := []string{
			`{"tool":"read_file","path":"docs/decisioning-north-star.md","start_line":1,"end_line":20}`,
			finalAnswer,
		}

		server, requestBodies := newScriptedChatServer(replies)

		objective := "Why would replacing this with model prose fit our north star or risk leaking unverified information to the user?"
		var events []RunEvent
		runner := &Runner{
			Client: llama.NewClient(server.URL(), 5*time.Second),
			Workspace: workspace.NewManager(config.Workspace{
				Root: root,
				CWD:  root,
			}),
			Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "conversation",
				Contract:          "direct_answer",
				Objective:         objective,
				PermissionProfile: "read_only_workspace",
				AllowedTools:      []string{"read_file"},
			},
			OnEvent: func(event RunEvent) {
				events = append(events, event)
			},
		}

		answer, err := runner.Run(
			context.Background(),
			[]llama.ChatMessage{{Role: "user", Content: objective}},
			llama.ChatOptions{},
			nil,
			nil,
			nil,
		)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(answer).To(gomega.Equal(finalAnswer))

		bodies := requestBodies()
		gomega.Expect(len(bodies)).To(gomega.BeNumerically(">=", 2))
		gomega.Expect(strings.Join(bodies, "\n---\n")).To(gomega.ContainSubstring("The model owns strategy; the harness owns contracts."))

		var sawLowConfidenceAccept bool
		for _, event := range events {
			gomega.Expect(event.Role).NotTo(gomega.Equal("final_answer_blocker_override"), event.Content)

			if event.Role == "final_answer_critic" &&
				strings.Contains(event.Content, `"passed":true`) &&
				strings.Contains(event.Content, `"objective_relevance":"checked_low_confidence_term_match"`) &&
				strings.Contains(event.Content, "docs/decisioning-north-star.md") {

				sawLowConfidenceAccept = true
			}
		}

		gomega.Expect(sawLowConfidenceAccept).To(gomega.BeTrue(), "events=%#v", events)
	})

	ginkgo.It("rejects coding-rules localization outside workspace evidence", func() {
		root := newGoModuleTestWorkspace()
		gomega.Expect(writeTestFile(root, "internal/labels/labels.go", strings.Join([]string{
			"package labels",
			"",
			"const ShortLabel = \"idiom\"",
			"",
			"func CurrentShortLabel() string {",
			"\treturn ShortLabel",
			"}",
		}, "\n"))).To(gomega.Succeed())

		badAnswer := "The short label is `failed`, based on `/tmp/outside/labels.go:6`."
		finalAnswer := "The short label is `idiom`; `internal/labels/labels.go` defines `ShortLabel` with that value and `CurrentShortLabel` returns it."
		replies := []string{
			`{"tool":"task","description":"Review coding rules localization evidence for the short label."}`,
			badAnswer,
			`{"tool":"read_file","path":"internal/labels/labels.go","start_line":1,"end_line":10}`,
			finalAnswer,
		}

		server, requestBodies := newScriptedChatServer(replies)

		codingRulesReport := strings.Join([]string{
			`{`,
			`  "schema_version": "idiom.coding_rules.report.v1",`,
			`  "workspace_root": "` + root + `",`,
			`  "status": "warn",`,
			`  "summary": {"files_scanned": 1, "findings": 1, "warnings": 1},`,
			`  "findings": [{`,
			`    "rule_id": "go.no_pass_through_wrapper",`,
			`    "language": "go",`,
			`    "severity": "warning",`,
			`    "enforcement": "advisory",`,
			`    "path": "/tmp/outside/labels.go",`,
			`    "line": 6,`,
			`    "symbol": "CurrentShortLabel",`,
			`    "message": "outside localization candidate",`,
			`    "evidence": [{"kind": "single_statement", "path": "/tmp/outside/labels.go", "line": 6}]`,
			`  }]`,
			`}`,
		}, "\n")

		objective := "What is the current short label?"
		var events []RunEvent
		runner := &Runner{
			Client: llama.NewClient(server.URL(), 5*time.Second),
			Workspace: workspace.NewManager(config.Workspace{
				Root: root,
				CWD:  root,
			}),
			Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4, MaxModelCalls: 8},
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "conversation",
				Contract:          "direct_answer",
				Objective:         objective,
				PermissionProfile: "read_only_workspace",
				AllowedTools:      []string{"task", "read_file"},
			},
			PreExecute: func(step int, req workspace.ToolRequest) *workspace.ToolResult {
				if req.Tool != "task" {
					return nil
				}

				return &workspace.ToolResult{
					Tool:        req.Tool,
					Summary:     "Coding rules warn: files=1 findings=1 blocking=0 suppressed=0",
					Output:      codingRulesReport,
					PayloadType: "application/vnd.idiom.coding_rules.report+json",
					Payload:     []byte(codingRulesReport),
				}
			},
			OnEvent: func(event RunEvent) {
				events = append(events, event)
			},
		}

		answer, err := runner.Run(
			context.Background(),
			[]llama.ChatMessage{{Role: "user", Content: objective}},
			llama.ChatOptions{},
			nil,
			nil,
			nil,
		)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(answer).To(gomega.Equal(finalAnswer))
		gomega.Expect(answer).NotTo(gomega.Or(
			gomega.ContainSubstring("/tmp/outside"),
			gomega.ContainSubstring("failed"),
		))

		bodies := strings.Join(requestBodies(), "\n---\n")
		gomega.Expect(bodies).NotTo(gomega.ContainSubstring(badAnswer))
		gomega.Expect(bodies).To(gomega.And(
			gomega.ContainSubstring("internal/labels/labels.go"),
			gomega.ContainSubstring("CurrentShortLabel"),
		))

		var sawReject bool
		var sawAccept bool
		for _, event := range events {
			if (event.Role == "next_action_state" || event.Role == "failure_feedback" || event.Role == "completion_gate_recovery") &&
				strings.Contains(event.Content, "source-body read") &&
				strings.Contains(event.Content, "/tmp/outside/labels.go") {

				sawReject = true
			}
			if event.Role == "final_answer_critic" &&
				strings.Contains(event.Content, `"passed":true`) &&
				strings.Contains(event.Content, "internal/labels/labels.go") &&
				!strings.Contains(event.Content, "/tmp/outside/labels.go") {

				sawAccept = true
			}
		}

		gomega.Expect(sawReject).To(gomega.BeTrue(), "events=%#v", events)
		gomega.Expect(sawAccept).To(gomega.BeTrue(), "events=%#v", events)
	})

	ginkgo.It("allows context_query after broad text search", func() {
		root := newGoModuleTestWorkspace()
		gomega.Expect(writeTestFile(root, "internal/contracts/workflowstate/machine.go", strings.Join([]string{
			"package workflowstate",
			"",
			"type Machine struct{}",
			"const PhaseTerminal = \"terminal\"",
		}, "\n"))).To(gomega.Succeed())

		finalAnswer := "The lifecycle state surface is in `internal/contracts/workflowstate/machine.go`: it defines `Machine` and the terminal phase constant `PhaseTerminal`, so review should focus there."
		replies := []string{
			`{"tool":"search_content","path":".","pattern":"workflow state"}`,
			`{"tool":"context_query","query":"find all types and constants related to lifecycle state, phase, terminal/nonterminal status, and workflow state machines","resolution":"symbol","include_evidence":true}`,
			`{"tool":"read_file","path":"internal/contracts/workflowstate/machine.go","start_line":1,"end_line":20}`,
			finalAnswer,
		}

		server, _ := newScriptedChatServer(replies)

		var toolEvents []ToolResultEvent
		runner := &Runner{
			Client: llama.NewClient(server.URL(), 5*time.Second),
			Workspace: workspace.NewManager(config.Workspace{
				Root: root,
				CWD:  root,
			}),
			Policy: RunPolicy{MaxToolSteps: 5, MaxNoProgressTurns: 4, MaxModelCalls: 8},
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "conversation",
				Contract:          "direct_answer",
				Objective:         "Review lifecycle state machine surfaces.",
				PermissionProfile: "read_only_workspace",
				AllowedTools:      []string{"search_content", "context_query", "read_file"},
			},
			PreExecute: func(step int, req workspace.ToolRequest) *workspace.ToolResult {
				switch req.Tool {
				case "search_content":
					return &workspace.ToolResult{
						Tool:    req.Tool,
						Summary: "Found 400 matching line(s) (showing first 50).",
						Output: strings.Join([]string{
							"internal/contracts/workflowstate/machine.go:3:type Machine struct{}",
							"internal/runtime/agent/discovery_consumption.go:15:type discoveryResultOutcome struct",
						}, "\n"),
						Facts: &toolfacts.ToolResultFacts{
							CandidatePaths:        []string{"internal/contracts/workflowstate/machine.go", "internal/runtime/agent/discovery_consumption.go"},
							HasDiscoveryTargets:   true,
							DiscoveryStrength:     toolfacts.DiscoveryStrengthCandidateOnly,
							SearchHitCount:        400,
							SearchHighCardinality: true,
						},
					}
				case "context_query":
					return &workspace.ToolResult{
						Tool:    req.Tool,
						Summary: "Context query returned lifecycle candidates.",
						Output:  `{"candidates":[{"path":"internal/contracts/workflowstate/machine.go","symbol":"Machine"}]}`,
						Facts:   discoveryCandidateFactsForTest("internal/contracts/workflowstate/machine.go"),
					}
				default:
					return nil
				}
			},
		}

		answer, err := runner.Run(
			context.Background(),
			[]llama.ChatMessage{{Role: "user", Content: "Review lifecycle state machine surfaces."}},
			llama.ChatOptions{},
			nil,
			nil,
			func(event ToolResultEvent) {
				toolEvents = append(toolEvents, event)
			},
		)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(answer).To(gomega.Equal(finalAnswer))

		var sawContextQuery bool
		for _, event := range toolEvents {
			if event.Result.Tool != "context_query" {
				continue
			}

			gomega.Expect(event.Result.Summary).NotTo(gomega.ContainSubstring("Discovery consumption required before more broad discovery"))
			gomega.Expect(event.Result.Error).To(gomega.BeEmpty())

			sawContextQuery = true
		}

		gomega.Expect(sawContextQuery).To(gomega.BeTrue(), "tool events=%#v", toolEvents)
	})

	ginkgo.It("returns control to the model after a read-only non-answer", func() {
		root := newReadOnlyStrategicThesisWorkspace()

		nonAnswer := "The user is asking me to inspect `docs/idiom-strategic-thesis.md`, but I should gather more evidence before answering."
		finalAnswer := "`docs/idiom-strategic-thesis.md` describes Idiom as an evidence-first harness: repository evidence feeds the runtime brief, while the harness owns contracts, freshness, verification, and auditability."
		replies := []string{
			readOnlyStrategicThesisReadFileToolCall,
			nonAnswer,
			finalAnswer,
		}

		server, requestBodies := newScriptedChatServer(replies)

		objective := "What is the role of evidence in this harness? Where does it come from and what is it used for?"
		var events []RunEvent
		runner := &Runner{
			Client: llama.NewClient(server.URL(), 5*time.Second),
			Workspace: workspace.NewManager(config.Workspace{
				Root: root,
				CWD:  root,
			}),
			Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 2, MaxModelCalls: 6},
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "conversation",
				Contract:          "direct_answer",
				Objective:         objective,
				PermissionProfile: "read_only_workspace",
				AllowedTools:      []string{"read_file"},
			},
			OnEvent: func(event RunEvent) {
				events = append(events, event)
			},
		}

		answer, err := runner.Run(
			context.Background(),
			[]llama.ChatMessage{{Role: "user", Content: objective}},
			llama.ChatOptions{},
			nil,
			nil,
			nil,
		)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(answer).To(gomega.Equal(finalAnswer))
		gomega.Expect(requestBodies()).To(gomega.HaveLen(len(replies)))
		gomega.Expect(answer).NotTo(gomega.ContainSubstring(nonAnswer))

		for _, event := range events {
			if event.Role == "completion_status" {
				gomega.Expect(event.Content).NotTo(gomega.ContainSubstring(`"user_visible_blocker":true`))
			}
		}
	})

	ginkgo.It("returns typed rejection feedback until the model authors an accepted read-only answer", func() {
		root := newReadOnlyStrategicThesisWorkspace()

		rejectedFinal := strings.Join([]string{
			"Now I have sufficient evidence to answer. Let me synthesize.",
			"",
			"**Confirmed: evidence is the harness contract boundary.**",
			"",
			"The inspected source in `docs/idiom-strategic-thesis.md` says Idiom is evidence-first and that the harness owns contracts, repository memory, verification, auditability, and the learning loop.",
		}, "\n")
		finalAnswer := "`docs/idiom-strategic-thesis.md` explains that repository evidence feeds the runtime brief and that the harness owns contracts, freshness, verification, and auditability."
		replies := []string{
			readOnlyStrategicThesisReadFileToolCall,
			rejectedFinal,
			rejectedFinal,
			finalAnswer,
		}

		server, requestBodies := newScriptedChatServer(replies)

		objective := "What is the role of evidence in this harness? Where does it come from and what is it used for?"
		var (
			events               []RunEvent
			lastAssistantMessage llama.ChatMessage
		)
		runner := &Runner{
			Client: llama.NewClient(server.URL(), 5*time.Second),
			Workspace: workspace.NewManager(config.Workspace{
				Root: root,
				CWD:  root,
			}),
			Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 4, MaxModelCalls: 8},
			CompletionGate: func(input CompletionGateInput) CompletionGateResult {
				if strings.TrimSpace(input.Reply) == strings.TrimSpace(rejectedFinal) {
					return CompletionGateResult{
						Summary: "Read-only completion gate rejected search-hit inventory",
						Error:   "the reply inventories search handles or raw hit lines instead of answering the user's question",
					}
				}

				return CompletionGateResult{Accept: true}
			},
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "conversation",
				Contract:          "direct_answer",
				Objective:         objective,
				PermissionProfile: "read_only_workspace",
				AllowedTools:      []string{"read_file"},
			},
			OnEvent: func(event RunEvent) {
				events = append(events, event)
			},
			OnAssistantMessage: func(message llama.ChatMessage) {
				lastAssistantMessage = message
			},
		}

		answer, err := runner.Run(
			context.Background(),
			[]llama.ChatMessage{{Role: "user", Content: objective}},
			llama.ChatOptions{},
			nil,
			nil,
			nil,
		)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(answer).To(gomega.Equal(finalAnswer))
		gomega.Expect(requestBodies()).To(gomega.HaveLen(len(replies)))
		gomega.Expect(strings.TrimSpace(lastAssistantMessage.Content)).To(gomega.Equal(strings.TrimSpace(answer)))

		var sawAcceptedCriticWithEvidence bool
		for _, event := range events {
			if event.Role == "completion_status" {
				gomega.Expect(event.Content).NotTo(gomega.ContainSubstring(`"user_visible_blocker":true`))
			}
			if event.Role == "final_answer_critic" &&
				strings.Contains(event.Content, `"passed":true`) &&
				strings.Contains(event.Content, readOnlyStrategicThesisPath) {

				sawAcceptedCriticWithEvidence = true
			}
		}
		gomega.Expect(sawAcceptedCriticWithEvidence).To(gomega.BeTrue(), "events=%#v", events)
	})

	ginkgo.It("does not override a read-only hedge after completion-gate retry", func() {
		root := newReadOnlyStrategicThesisWorkspace()

		firstAnswer := "The inspected source in `docs/idiom-strategic-thesis.md` says Idiom is evidence-first."
		hedgeAnswer := "I cannot provide a supported answer because the retained source context is not sufficient."
		replies := []string{
			readOnlyStrategicThesisReadFileToolCall,
			firstAnswer,
			hedgeAnswer,
		}

		server, requestBodies := newScriptedChatServer(replies)

		objective := "What is the role of evidence in this harness? Where does it come from and what is it used for?"
		gateAttempts := 0
		var events []RunEvent
		runner := &Runner{
			Client: llama.NewClient(server.URL(), 5*time.Second),
			Workspace: workspace.NewManager(config.Workspace{
				Root: root,
				CWD:  root,
			}),
			Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 3, MaxModelCalls: 6},
			CompletionGate: func(input CompletionGateInput) CompletionGateResult {
				gateAttempts++
				if gateAttempts == 1 {
					return CompletionGateResult{
						Summary: "Read-only completion gate rejected final answer",
						Error:   "the answer cites source path/line/symbol detail not backed by retained gate proof",
					}
				}

				return CompletionGateResult{Accept: true}
			},
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "conversation",
				Contract:          "direct_answer",
				Objective:         objective,
				PermissionProfile: "read_only_workspace",
				AllowedTools:      []string{"read_file"},
			},
			OnEvent: func(event RunEvent) {
				events = append(events, event)
			},
		}

		answer, err := runner.Run(
			context.Background(),
			[]llama.ChatMessage{{Role: "user", Content: objective}},
			llama.ChatOptions{},
			nil,
			nil,
			nil,
		)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(answer).To(gomega.Equal(hedgeAnswer))
		gomega.Expect(requestBodies()).To(gomega.HaveLen(len(replies)))

		for _, event := range events {
			if event.Role == "completion_status" {
				gomega.Expect(event.Content).NotTo(gomega.ContainSubstring(`"user_visible_blocker":true`))
				gomega.Expect(event.Content).NotTo(gomega.And(
					gomega.ContainSubstring(`"status":"accepted"`),
					gomega.ContainSubstring(`"reason":"read_only_terminal_recovery"`),
				))
			}
		}
		gomega.Expect(gateAttempts).To(gomega.BeNumerically(">=", 2))
	})

	ginkgo.It("returns control to the model after a read-only readiness-nudge echo", func() {
		root := newReadOnlyStrategicThesisWorkspace()

		nudgeEcho := strings.Join([]string{
			"The read-only evidence is sufficient for a grounded answer to this question.",
			"",
			"Why a final answer is now valid: the current evidence already names inspected source paths `docs/idiom-strategic-thesis.md`.",
			"",
			"Core evidence paths: `docs/idiom-strategic-thesis.md`",
			"",
			"A final answer is available unless one exact unresolved claim remains.",
			"",
			"If one more source read would materially change the answer, take a bounded follow-up through the normal tool policy.",
		}, "\n")
		finalAnswer := "`docs/idiom-strategic-thesis.md` says Idiom is evidence-first: task-scoped repository evidence feeds the runtime brief, and the harness owns contracts, freshness, verification, and auditability."
		replies := []string{
			readOnlyStrategicThesisReadFileToolCall,
			nudgeEcho,
			finalAnswer,
		}

		server, requestBodies := newScriptedChatServer(replies)

		objective := "What is the role of evidence in this harness? Where does it come from and what is it used for?"
		var events []RunEvent
		runner := &Runner{
			Client: llama.NewClient(server.URL(), 5*time.Second),
			Workspace: workspace.NewManager(config.Workspace{
				Root: root,
				CWD:  root,
			}),
			Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 3, MaxModelCalls: 5},
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "conversation",
				Contract:          "direct_answer",
				Objective:         objective,
				PermissionProfile: "read_only_workspace",
				AllowedTools:      []string{"read_file"},
			},
			OnEvent: func(event RunEvent) {
				events = append(events, event)
			},
		}

		answer, err := runner.Run(
			context.Background(),
			[]llama.ChatMessage{{Role: "user", Content: objective}},
			llama.ChatOptions{},
			nil,
			nil,
			nil,
		)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(answer).To(gomega.Equal(finalAnswer))
		gomega.Expect(answer).NotTo(gomega.ContainSubstring(nudgeEcho))
		gomega.Expect(requestBodies()).To(gomega.HaveLen(len(replies)))

		for _, event := range events {
			switch event.Role {
			case "completion_status":
				gomega.Expect(event.Content).NotTo(gomega.ContainSubstring(`"user_visible_blocker":true`))
			case "budget_ledger":
				gomega.Expect(event.Content).NotTo(gomega.And(
					gomega.ContainSubstring(`"stop_reason":"read_only_terminal_recovery"`),
					gomega.ContainSubstring(`"budget_exhausted":true`),
				))
			}
		}
	})
})
