package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func TestRunnerEndToEndAcceptsLowConfidenceSourceBackedDirectAnswer(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "docs/decisioning-north-star.md", strings.Join([]string{
		"# Decisioning North Star",
		"The model owns strategy; the harness owns contracts.",
		"Final answers must be grounded in verified source evidence rather than unverified model prose.",
		"Deterministic closeouts prevent raw unverified information from leaking to users.",
	}, "\n")); err != nil {
		t.Fatalf("write source doc: %v", err)
	}
	if err := writeTestFile(root, "go.mod", "module idiomtest\n\ngo 1.22\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	finalAnswer := "No. Free-form prose moves the closeout away from deterministic contract enforcement."
	replies := []string{
		`{"tool":"read_file","path":"docs/decisioning-north-star.md","start_line":1,"end_line":20}`,
		finalAnswer,
	}

	requests := 0
	var requestBodies []string
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		var body strings.Builder
		buf := make([]byte, 1<<16)
		for {
			n, err := r.Body.Read(buf)
			if n > 0 {
				body.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}

		mu.Lock()
		idx := requests
		requests++
		requestBodies = append(requestBodies, body.String())
		mu.Unlock()

		reply := replies[len(replies)-1]
		if idx < len(replies) {
			reply = replies[idx]
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
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

	mu.Lock()
	bodies := append([]string(nil), requestBodies...)
	mu.Unlock()

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

func TestRunnerEndToEndRecoversReadOnlyNonAnswerFromAnswerableEvidence(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "docs/idiom-strategic-thesis.md", strings.Join([]string{
		"# Idiom Strategic Thesis",
		"Idiom sells the missing substrate: repo memory, freshness, contracts, audit, and evals.",
		"Idiom is the local, evidence-first coding-agent harness for teams that need agents to do real work in real repositories.",
		"The model owns strategy; the harness owns contracts, repository memory, verification, auditability, and the learning loop.",
		"The harness decides whether that action is allowed, whether evidence is fresh enough, and whether readiness is satisfied.",
		"Task-scoped repository evidence feeds the runtime brief, including tools, editable paths, evidence paths, verification, and budget.",
	}, "\n")); err != nil {
		t.Fatalf("write source doc: %v", err)
	}
	if err := writeTestFile(root, "go.mod", "module idiomtest\n\ngo 1.22\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	nonAnswer := "The user is asking me to inspect `docs/idiom-strategic-thesis.md`, but I should gather more evidence before answering."
	replies := []string{
		`{"tool":"read_file","path":"docs/idiom-strategic-thesis.md","start_line":1,"end_line":20}`,
		nonAnswer,
		nonAnswer,
		nonAnswer,
		nonAnswer,
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		reply := replies[len(replies)-1]
		if requests < len(replies) {
			reply = replies[requests]
		}
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
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

	for _, forbidden := range []string{"I could not produce a supported final answer", "Reply with substantive prose", nonAnswer} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("read-only terminal recovery leaked forbidden text %q:\n%s", forbidden, answer)
		}
	}
	for _, want := range []string{"docs/idiom-strategic-thesis.md", "evidence-first", "contracts", "runtime brief"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("recovered answer missing %q:\n%s", want, answer)
		}
	}

	for _, event := range events {
		if event.Role == "completion_status" && strings.Contains(event.Content, `"user_visible_blocker":true`) {
			t.Fatalf("read-only terminal recovery must not emit a user-visible blocker: %s", event.Content)
		}
	}
}

func TestRunnerEndToEndRecoversReadOnlyRepeatedCompletionGateRejection(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "docs/idiom-strategic-thesis.md", strings.Join([]string{
		"# Idiom Strategic Thesis",
		"Idiom sells the missing substrate: repo memory, freshness, contracts, audit, and evals.",
		"Idiom is the local, evidence-first coding-agent harness for teams that need agents to do real work in real repositories.",
		"The model owns strategy; the harness owns contracts, repository memory, verification, auditability, and the learning loop.",
		"The harness decides whether that action is allowed, whether evidence is fresh enough, and whether readiness is satisfied.",
		"Task-scoped repository evidence feeds the runtime brief, including tools, editable paths, evidence paths, verification, and budget.",
	}, "\n")); err != nil {
		t.Fatalf("write source doc: %v", err)
	}
	if err := writeTestFile(root, "go.mod", "module idiomtest\n\ngo 1.22\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	rejectedFinal := strings.Join([]string{
		"Now I have sufficient evidence to answer. Let me synthesize.",
		"",
		"**Confirmed: evidence is the harness contract boundary.**",
		"",
		"The inspected source in `docs/idiom-strategic-thesis.md` says Idiom is evidence-first and that the harness owns contracts, repository memory, verification, auditability, and the learning loop.",
	}, "\n")
	replies := []string{
		`{"tool":"read_file","path":"docs/idiom-strategic-thesis.md","start_line":1,"end_line":20}`,
		rejectedFinal,
		rejectedFinal,
		rejectedFinal,
		rejectedFinal,
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		reply := replies[len(replies)-1]
		if requests < len(replies) {
			reply = replies[requests]
		}
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
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
			if strings.Contains(input.Reply, "docs/idiom-strategic-thesis.md") {
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

	for _, forbidden := range []string{"I could not produce a supported final answer", "reply inventories search handles", "<<QUESTION>>"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("read-only repeated gate recovery leaked forbidden text %q:\n%s", forbidden, answer)
		}
	}
	for _, want := range []string{"docs/idiom-strategic-thesis.md", "evidence-first", "contracts", "runtime brief"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("recovered answer missing %q:\n%s", want, answer)
		}
	}
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
			strings.Contains(event.Content, "docs/idiom-strategic-thesis.md") {

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
