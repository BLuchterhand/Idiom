package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const (
	e2eTestGoMod                            = "module idiomtest\n\ngo 1.22\n"
	readOnlyStrategicThesisPath             = "docs/idiom-strategic-thesis.md"
	readOnlyStrategicThesisReadFileToolCall = `{"tool":"read_file","path":"docs/idiom-strategic-thesis.md","start_line":1,"end_line":20}`
)

func newGoModuleTestWorkspace(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", e2eTestGoMod); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	return root
}

func newReadOnlyStrategicThesisWorkspace(t *testing.T) string {
	t.Helper()

	root := newGoModuleTestWorkspace(t)
	if err := writeTestFile(root, readOnlyStrategicThesisPath, strings.Join([]string{
		"# Idiom Strategic Thesis",
		"Idiom sells the missing substrate: repo memory, freshness, contracts, audit, and evals.",
		"Idiom is the local, evidence-first coding-agent harness for teams that need agents to do real work in real repositories.",
		"The model owns strategy; the harness owns contracts, repository memory, verification, auditability, and the learning loop.",
		"The harness decides whether that action is allowed, whether evidence is fresh enough, and whether readiness is satisfied.",
		"Task-scoped repository evidence feeds the runtime brief, including tools, editable paths, evidence paths, verification, and budget.",
	}, "\n")); err != nil {
		t.Fatalf("write source doc: %v", err)
	}

	return root
}

func assertRecoveredStrategicThesisAnswer(t *testing.T, answer string, forbidden ...string) {
	t.Helper()

	for _, text := range forbidden {
		if strings.Contains(answer, text) {
			t.Fatalf("recovered strategic thesis answer leaked forbidden text %q:\n%s", text, answer)
		}
	}
	for _, want := range []string{readOnlyStrategicThesisPath, "evidence-first", "contracts", "runtime brief"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("recovered strategic thesis answer missing %q:\n%s", want, answer)
		}
	}
}

func newScriptedChatServer(t *testing.T, replies []string, failOnExtraRequest bool) (*httptest.Server, func() []string) {
	t.Helper()

	var mu sync.Mutex
	requests := 0
	var requestBodies []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		idx := requests
		requests++
		requestBodies = append(requestBodies, string(body))
		mu.Unlock()

		if failOnExtraRequest && idx >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}

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

	return server, func() []string {
		mu.Lock()
		defer mu.Unlock()

		return append([]string(nil), requestBodies...)
	}
}
