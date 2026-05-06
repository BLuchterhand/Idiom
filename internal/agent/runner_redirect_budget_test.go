package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"idiom/internal/config"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
	"idiom/internal/workspace"
)

func TestDiscoveryRedirectDoesNotConsumeMutationReadBudget(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.test\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := writeTestFile(root, "main.go", "package main\nfunc main() {}\n"+strings.Repeat("\n", 500)); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	if err := writeTestFile(root, "small.go", "package main\nfunc small() {}\n"); err != nil {
		t.Fatalf("write small.go: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"main.go"}`,
		`{"tool":"read_file","path":"small.go"}`,
		"blocked: no edit in redirect budget regression harness",
	}
	requests := 0
	var events []RunEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request (%d)", requests)
		}
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))
	t.Cleanup(server.Close)

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy: RunPolicy{
			MaxNoProgressTurns: 8,
			ReadBudgetHard:     1,
		},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "implementation",
			Contract:   "implementation_result",
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "redirect budget regression"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := countEventsByRole(events, "discovery_redirect"); got != 1 {
		t.Fatalf("expected one redirect, got %d; roles=%v", got, eventRolesFor(events))
	}
	if got := countEventsByRole(events, "read_budget_hard_block"); got != 0 {
		t.Fatalf("redirect should not consume mutation read budget, got %d hard block(s); roles=%v", got, eventRolesFor(events))
	}
	if requests != len(replies) {
		t.Fatalf("expected %d model requests, got %d", len(replies), requests)
	}
}
