package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func TestRunnerEndToEndAcceptsLowConfidenceSourceBackedDirectAnswer(t *testing.T) {
	root := newGoModuleTestWorkspace(t)
	if err := writeTestFile(root, "docs/decisioning-north-star.md", strings.Join([]string{
		"# Decisioning North Star",
		"The model owns strategy; the harness owns contracts.",
		"Final answers must be grounded in verified source evidence rather than unverified model prose.",
		"Deterministic closeouts prevent raw unverified information from leaking to users.",
	}, "\n")); err != nil {
		t.Fatalf("write source doc: %v", err)
	}

	finalAnswer := "No. Free-form prose moves the closeout away from deterministic contract enforcement."
	replies := []string{
		`{"tool":"read_file","path":"docs/decisioning-north-star.md","start_line":1,"end_line":20}`,
		finalAnswer,
	}

	server, requestBodies := newScriptedChatServer(t, replies, false)
	defer server.Close()

	objective := "Why would replacing this with model prose fit our north star or risk leaking unverified information to the user?"
	var events []RunEvent
	runner := &Runner{
		Client: llama.NewClient(server.URL, 5*time.Second),
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
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if answer != finalAnswer {
		t.Fatalf("runner returned unexpected answer:\n%s", answer)
	}

	bodies := requestBodies()
	if len(bodies) < 2 {
		t.Fatalf("expected read request plus final answer request, got %d request(s)", len(bodies))
	}

	if !strings.Contains(strings.Join(bodies, "\n---\n"), "The model owns strategy; the harness owns contracts.") {
		t.Fatalf("expected read_file source body to reach the model before final answer; bodies:\n%s", strings.Join(bodies, "\n---\n"))
	}

	var sawLowConfidenceAccept bool
	for _, event := range events {
		if event.Role == "final_answer_blocker_override" {
			t.Fatalf("low-confidence source-backed answer must not be replaced with blocker override: %s", event.Content)
		}

		if event.Role == "final_answer_critic" &&
			strings.Contains(event.Content, `"passed":true`) &&
			strings.Contains(event.Content, `"objective_relevance":"checked_low_confidence_term_match"`) &&
			strings.Contains(event.Content, "docs/decisioning-north-star.md") {

			sawLowConfidenceAccept = true
		}
	}

	if !sawLowConfidenceAccept {
		t.Fatalf("expected accepted final-answer telemetry with checked_low_confidence_term_match; events=%#v", events)
	}
}

func TestRunnerEndToEndRejectsCodingRulesLocalizationOutsideWorkspaceEvidence(t *testing.T) {
	root := newGoModuleTestWorkspace(t)
	if err := writeTestFile(root, "internal/labels/labels.go", strings.Join([]string{
		"package labels",
		"",
		"const ShortLabel = \"idiom\"",
		"",
		"func CurrentShortLabel() string {",
		"\treturn ShortLabel",
		"}",
	}, "\n")); err != nil {
		t.Fatalf("write source doc: %v", err)
	}

	badAnswer := "The short label is `failed`, based on `/tmp/outside/labels.go:6`."
	finalAnswer := "The short label is `idiom`; `internal/labels/labels.go` defines `ShortLabel` with that value and `CurrentShortLabel` returns it."
	replies := []string{
		`{"tool":"task","description":"Review coding rules localization evidence for the short label."}`,
		badAnswer,
		`{"tool":"read_file","path":"internal/labels/labels.go","start_line":1,"end_line":10}`,
		finalAnswer,
	}

	server, requestBodies := newScriptedChatServer(t, replies, true)
	defer server.Close()

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
		Client: llama.NewClient(server.URL, 5*time.Second),
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
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if answer != finalAnswer {
		t.Fatalf("runner returned unexpected answer:\n%s", answer)
	}
	if strings.Contains(answer, "/tmp/outside") || strings.Contains(answer, "failed") {
		t.Fatalf("runner surfaced outside-evidence answer:\n%s", answer)
	}

	bodies := strings.Join(requestBodies(), "\n---\n")
	if !strings.Contains(bodies, badAnswer) {
		t.Fatalf("expected bad answer attempt before repair; bodies:\n%s", bodies)
	}
	if !strings.Contains(bodies, "internal/labels/labels.go") || !strings.Contains(bodies, "CurrentShortLabel") {
		t.Fatalf("expected repo-local read_file source body before accepted answer; bodies:\n%s", bodies)
	}

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

	if !sawReject {
		t.Fatalf("expected final-answer critic to reject outside coding-rules localization evidence; events=%#v", events)
	}
	if !sawAccept {
		t.Fatalf("expected final-answer critic to accept only repo-local source-body evidence; events=%#v", events)
	}
}

func TestRunnerEndToEndAllowsContextQueryAfterBroadTextSearch(t *testing.T) {
	root := newGoModuleTestWorkspace(t)
	if err := writeTestFile(root, "internal/contracts/workflowstate/machine.go", strings.Join([]string{
		"package workflowstate",
		"",
		"type Machine struct{}",
		"const PhaseTerminal = \"terminal\"",
	}, "\n")); err != nil {
		t.Fatalf("write workflow source: %v", err)
	}

	finalAnswer := "The lifecycle state surface is in `internal/contracts/workflowstate/machine.go`: it defines `Machine` and the terminal phase constant `PhaseTerminal`, so review should focus there."
	replies := []string{
		`{"tool":"search_content","path":".","pattern":"workflow state"}`,
		`{"tool":"context_query","query":"find all types and constants related to lifecycle state, phase, terminal/nonterminal status, and workflow state machines","resolution":"symbol","include_evidence":true}`,
		`{"tool":"read_file","path":"internal/contracts/workflowstate/machine.go","start_line":1,"end_line":20}`,
		finalAnswer,
	}

	server, _ := newScriptedChatServer(t, replies, true)
	defer server.Close()

	var toolEvents []ToolResultEvent
	runner := &Runner{
		Client: llama.NewClient(server.URL, 5*time.Second),
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
						"internal/runtime/agent/discovery_consumption.go:12:type discoveryConsumptionStrength int",
					}, "\n"),
				}
			case "context_query":
				return &workspace.ToolResult{
					Tool:    req.Tool,
					Summary: "Context query returned lifecycle candidates.",
					Output:  `{"source_owner_found":false,"candidates":[{"path":"internal/contracts/workflowstate/machine.go","symbol":"Machine"}]}`,
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
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if answer != finalAnswer {
		t.Fatalf("unexpected final answer:\n%s", answer)
	}

	var sawContextQuery bool
	for _, event := range toolEvents {
		if event.Result.Tool != "context_query" {
			continue
		}

		if strings.Contains(event.Result.Summary, "Discovery consumption required before more broad discovery") || strings.TrimSpace(event.Result.Error) != "" {
			t.Fatalf("context_query was rejected instead of executed: %#v", event.Result)
		}

		sawContextQuery = true
	}

	if !sawContextQuery {
		t.Fatalf("expected scripted context_query to execute; tool events=%#v", toolEvents)
	}
}

func TestRunnerEndToEndRecoversReadOnlyNonAnswerFromAnswerableEvidence(t *testing.T) {
	root := newReadOnlyStrategicThesisWorkspace(t)

	nonAnswer := "The user is asking me to inspect `docs/idiom-strategic-thesis.md`, but I should gather more evidence before answering."
	replies := []string{
		readOnlyStrategicThesisReadFileToolCall,
		nonAnswer,
		nonAnswer,
		nonAnswer,
		nonAnswer,
	}

	server, _ := newScriptedChatServer(t, replies, false)
	defer server.Close()

	objective := "What is the role of evidence in this harness? Where does it come from and what is it used for?"
	var events []RunEvent
	runner := &Runner{
		Client: llama.NewClient(server.URL, 5*time.Second),
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
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	assertRecoveredStrategicThesisAnswer(t, answer, "I could not produce a supported final answer", "Reply with substantive prose", nonAnswer)

	for _, event := range events {
		if event.Role == "completion_status" && strings.Contains(event.Content, `"user_visible_blocker":true`) {
			t.Fatalf("read-only terminal recovery must not emit a user-visible blocker: %s", event.Content)
		}
	}
}

func TestRunnerEndToEndRecoversReadOnlyRepeatedCompletionGateRejection(t *testing.T) {
	root := newReadOnlyStrategicThesisWorkspace(t)

	rejectedFinal := strings.Join([]string{
		"Now I have sufficient evidence to answer. Let me synthesize.",
		"",
		"**Confirmed: evidence is the harness contract boundary.**",
		"",
		"The inspected source in `docs/idiom-strategic-thesis.md` says Idiom is evidence-first and that the harness owns contracts, repository memory, verification, auditability, and the learning loop.",
	}, "\n")
	replies := []string{
		readOnlyStrategicThesisReadFileToolCall,
		rejectedFinal,
		rejectedFinal,
		rejectedFinal,
		rejectedFinal,
	}

	server, _ := newScriptedChatServer(t, replies, false)
	defer server.Close()

	objective := "What is the role of evidence in this harness? Where does it come from and what is it used for?"
	var events []RunEvent
	runner := &Runner{
		Client: llama.NewClient(server.URL, 5*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 4, MaxModelCalls: 8},
		CompletionGate: func(input CompletionGateInput) CompletionGateResult {
			if strings.Contains(input.Reply, readOnlyStrategicThesisPath) {
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
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: objective}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	assertRecoveredStrategicThesisAnswer(t, answer, "I could not produce a supported final answer", "reply inventories search handles", "<<QUESTION>>")
	if strings.TrimSpace(runner.LastAssistantMessage.Content) != strings.TrimSpace(answer) {
		t.Fatalf("last assistant message should store recovered answer, got:\n%s\nwant:\n%s", runner.LastAssistantMessage.Content, answer)
	}

	var sawAcceptedRecovery bool
	var sawAcceptedCriticWithEvidence bool
	for _, event := range events {
		if event.Role == "completion_status" && strings.Contains(event.Content, `"user_visible_blocker":true`) {
			t.Fatalf("read-only repeated gate recovery must not emit a user-visible blocker: %s", event.Content)
		}
		if event.Role == "completion_status" &&
			strings.Contains(event.Content, `"status":"accepted"`) &&
			strings.Contains(event.Content, `"reason":"read_only_terminal_recovery"`) {

			sawAcceptedRecovery = true
		}
		if event.Role == "final_answer_critic" &&
			strings.Contains(event.Content, `"passed":true`) &&
			strings.Contains(event.Content, readOnlyStrategicThesisPath) {

			sawAcceptedCriticWithEvidence = true
		}
	}
	if !sawAcceptedRecovery {
		t.Fatalf("expected accepted completion status for recovered read-only closeout; events=%#v", events)
	}
	if !sawAcceptedCriticWithEvidence {
		t.Fatalf("expected accepted final-answer critic with source evidence path; events=%#v", events)
	}
}

func TestRunnerEndToEndDoesNotOverrideReadOnlyHedgeAfterCompletionGateRetry(t *testing.T) {
	root := newReadOnlyStrategicThesisWorkspace(t)

	firstAnswer := "The inspected source in `docs/idiom-strategic-thesis.md` says Idiom is evidence-first."
	hedgeAnswer := "I cannot provide a supported answer because the retained source context is not sufficient."
	replies := []string{
		readOnlyStrategicThesisReadFileToolCall,
		firstAnswer,
		hedgeAnswer,
	}

	server, requestBodies := newScriptedChatServer(t, replies, false)
	defer server.Close()

	objective := "What is the role of evidence in this harness? Where does it come from and what is it used for?"
	gateAttempts := 0
	var events []RunEvent
	runner := &Runner{
		Client: llama.NewClient(server.URL, 5*time.Second),
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
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if answer != hedgeAnswer {
		t.Fatalf("read-only hedge should remain model-authored, got:\n%s", answer)
	}
	if got := len(requestBodies()); got != len(replies) {
		t.Fatalf("expected scripted first answer and hedge retry to run, used %d/%d replies", got, len(replies))
	}

	for _, event := range events {
		if event.Role == "completion_status" && strings.Contains(event.Content, `"user_visible_blocker":true`) {
			t.Fatalf("read-only hedge recovery must not emit a user-visible blocker: %s", event.Content)
		}
		if event.Role == "completion_status" &&
			strings.Contains(event.Content, `"status":"accepted"`) &&
			strings.Contains(event.Content, `"reason":"read_only_terminal_recovery"`) {

			t.Fatalf("read-only hedge should not be host-recovered: %s", event.Content)
		}
	}
	if gateAttempts < 2 {
		t.Fatalf("expected first final answer rejection plus hedge retry acceptance, got %d gate attempt(s)", gateAttempts)
	}
}

func TestRunnerEndToEndRecoversReadOnlyReadinessNudgeEcho(t *testing.T) {
	root := newReadOnlyStrategicThesisWorkspace(t)

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
	replies := []string{
		readOnlyStrategicThesisReadFileToolCall,
		nudgeEcho,
	}

	server, requestBodies := newScriptedChatServer(t, replies, true)
	defer server.Close()

	objective := "What is the role of evidence in this harness? Where does it come from and what is it used for?"
	var events []RunEvent
	runner := &Runner{
		Client: llama.NewClient(server.URL, 5*time.Second),
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
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	assertRecoveredStrategicThesisAnswer(t, answer, "The read-only evidence is sufficient", "A final answer is available unless", "<<QUESTION>>")
	if got := len(requestBodies()); got != len(replies) {
		t.Fatalf("expected read request plus nudge echo final, got %d request(s)", got)
	}

	for _, event := range events {
		if event.Role == "completion_status" && strings.Contains(event.Content, `"user_visible_blocker":true`) {
			t.Fatalf("readiness nudge recovery must not emit a user-visible blocker: %s", event.Content)
		}
		if event.Role == "budget_ledger" &&
			strings.Contains(event.Content, `"stop_reason":"read_only_terminal_recovery"`) &&
			strings.Contains(event.Content, `"budget_exhausted":true`) {

			t.Fatalf("accepted read-only recovery must not be reported as budget-exhausted: %s", event.Content)
		}
	}
}
