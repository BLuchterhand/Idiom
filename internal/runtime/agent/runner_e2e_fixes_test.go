package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/readapproval"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func TestRunnerEndToEndAllowsDocsReadAfterReadinessUnderCap(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/runtime/agent/foo.go", "package agent\n\nfunc Foo() {}\n"); err != nil {
		t.Fatalf("write seed source: %v", err)
	}
	if err := writeTestFile(root, "docs/design-context.md", "# Design\n\nWidget details live here.\n"); err != nil {
		t.Fatalf("write seed doc: %v", err)
	}
	if err := writeTestFile(root, "go.mod", "module idiomtest\n\ngo 1.22\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"internal/runtime/agent/foo.go","start_line":1,"end_line":40}`,
		`{"tool":"read_file","path":"docs/design-context.md"}`,
		"Decision: source-satisfied\n\ninternal/runtime/agent/foo.go already covers Foo().",
	}
	requests := 0
	var requestBodies []string
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		mu.Lock()
		idx := requests
		requests++
		// Read all body bytes instead of a single Read call
		var bodyBuf strings.Builder
		tmp := make([]byte, 1<<18)
		for {
			n, err := r.Body.Read(tmp)
			if n > 0 {
				bodyBuf.Write(tmp[:n])
			}
			if err != nil {
				break
			}
		}
		requestBodies = append(requestBodies, bodyBuf.String())
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

	runner := &Runner{
		Client: llama.NewClient(server.URL, 5*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			Objective:         "Plan a change to Foo().",
			PermissionProfile: "guarded_workspace_write",
			AllowedEditPaths:  []string{"internal/runtime/agent/foo.go"},
			AllowedTools:      []string{"read_file"},
		},
	}

	_, _ = runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Plan a change to Foo()."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)

	mu.Lock()
	defer mu.Unlock()

	var sawCandidateScopeRejection bool
	var sawDocsContentReachModel bool
	for _, body := range requestBodies {
		if strings.Contains(body, "Plan-execute read outside allowed candidate scope") &&
			strings.Contains(body, "docs/design-context.md") {
			sawCandidateScopeRejection = true
		}
		if strings.Contains(body, "Widget details live here") {
			sawDocsContentReachModel = true
		}
	}

	if sawCandidateScopeRejection {
		t.Fatal("Fix A regression: a docs read under the cap was rejected with the candidate-scope summary when it should have been auto-approved")
	}
	if !sawDocsContentReachModel {
		t.Fatal("expected the docs read content to reach the model in a subsequent request, proving the read was dispatched; the read did not go through")
	}
}

func TestRunnerEndToEndRejectsNonDocReadAfterReadinessWithoutOperatorGate(t *testing.T) {
	t.Skip("placeholder to keep file structure; replaced by TestRunnerEndToEndReadApprovalGate* below")
}

func TestRunnerEndToEndFeaturePlannerPlanTabReadSupportsTDDCoverageFollowup(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module idiomtest\n\ngo 1.22\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := writeTestFile(root, ".planning/20260621-161128.627.md", featurePlannerPlanMarkdownForTest()); err != nil {
		t.Fatalf("write plan tab artifact: %v", err)
	}

	finalAnswer := featurePlannerPlanTableAnswerForTest()
	replies := []string{
		`{"tool":"read_file","path":".planning/20260621-161128.627.md","start_line":1,"end_line":120}`,
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

	objective := "what tests do w have planned? should we expand the intended test suite?"
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
	gotRequests := requests
	bodies := append([]string(nil), requestBodies...)
	mu.Unlock()

	if gotRequests != 2 {
		t.Fatalf("planned test follow-up should need only plan read plus final answer, got %d request(s); bodies:\n%s", gotRequests, strings.Join(bodies, "\n---\n"))
	}
	if !strings.Contains(strings.Join(bodies, "\n---\n"), "Feature Planner Candidate") {
		t.Fatalf("expected plan-tab body to reach the model before final answer; bodies:\n%s", strings.Join(bodies, "\n---\n"))
	}

	for _, event := range events {
		sourceGroundingRepair := strings.Contains(event.Content, "source_grounding_unmet") ||
			strings.Contains(event.Content, "does not exist in this workspace") ||
			strings.Contains(event.Content, "tests/test_llm_client.py") && strings.Contains(event.Content, "source-body evidence")
		if sourceGroundingRepair {
			t.Fatalf("planned future tests must not enter source-grounding repair; event=%s content=%s", event.Role, event.Content)
		}
	}
}

func TestRunnerEndToEndFeaturePlannerTDDGateMetaFinalDoesNotLeak(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module idiomtest\n\ngo 1.22\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := writeTestFile(root, ".planning/20260621-161128.627.md", featurePlannerPlanMarkdownForTest()); err != nil {
		t.Fatalf("write plan tab artifact: %v", err)
	}

	leaked := "The answer above is grounded in the inspected evidence (`.planning/20260621-161128.627.md` lines 25-39 for the planned tests). The proposed additions are explicitly marked as `unverified - proposed` since they don't exist on disk. This satisfies the gate requirements."
	finalAnswer := "The planned tests are `tests/test_models.py`, `tests/test_config.py`, and `tests/test_orchestrator.py`. I would expand the intended suite with persistence variants, config default/error cases, and orchestrator failure-propagation invariants."
	replies := []string{
		`{"tool":"read_file","path":".planning/20260621-161128.627.md","start_line":1,"end_line":120}`,
		leaked,
		finalAnswer,
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

	objective := "what tests do w have planned? should we expand the intended test suite?"
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
		t.Fatalf("runner should retry protocol-meta final and return the substantive planned-tests answer:\n%s", answer)
	}
	for _, forbidden := range []string{"The answer above", "gate requirements", "<<QUESTION>>", "Recovery card"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("gate-meta final leaked forbidden text %q:\n%s", forbidden, answer)
		}
	}
	if strings.TrimSpace(answer) == "" {
		t.Fatal("runner returned empty answer")
	}
}

func TestRunnerEndToEndEmbeddedQuestionProtocolFinalDoesNotLeak(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "docs/harness.md", strings.Join([]string{
		"# Harness",
		"The harness owns final-answer acceptance and user-facing presentation.",
		"Protocol markers are internal transport details, not user-facing answers.",
	}, "\n")); err != nil {
		t.Fatalf("write harness doc: %v", err)
	}
	if err := writeTestFile(root, "go.mod", "module idiomtest\n\ngo 1.22\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	leaked := "I identified the newest conversation log at `/Users/ben/.idiom/logs/conversations/20260622-064537.192-20260622-104535.851.jsonl`.\n### <<QUESTION>> — Exact Blocker\nI need permission to read the conversation log from the user-level path."
	finalAnswer := "Final-answer acceptance and presentation are the shared boundary: `docs/harness.md` says the harness owns final-answer acceptance and that protocol markers are internal transport details, not user-facing answers."
	replies := []string{
		`{"tool":"read_file","path":"docs/harness.md","start_line":1,"end_line":20}`,
		leaked,
		finalAnswer,
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

	objective := "What boundary owns leaked protocol text in user-facing final answers?"
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
		t.Fatalf("runner should retry embedded protocol final and return substantive answer:\n%s", answer)
	}
	for _, forbidden := range []string{"<<QUESTION>>", "user-level path", "newest conversation log"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("embedded protocol final leaked forbidden text %q:\n%s", forbidden, answer)
		}
	}
}

func TestRunnerEndToEndRepairRewritesLeakedImplementationBlockerTemplate(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module idiomtest\n\ngo 1.22\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	leaked := "Changed paths: none\n\nVerification: not run or not detected.\n\nBlocker:\n- File search rejected: pattern is required"
	rewritten := "The implementation lane ended before a successful workspace edit was recorded; no files were changed."

	replies := []string{leaked, rewritten}
	requests := 0
	var requestBodies []string
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		mu.Lock()
		idx := requests
		requests++
		// Read all body bytes
		var bodyBuf strings.Builder
		tmp := make([]byte, 1<<18)
		for {
			n, err := r.Body.Read(tmp)
			if n > 0 {
				bodyBuf.Write(tmp[:n])
			}
			if err != nil {
				break
			}
		}
		requestBodies = append(requestBodies, bodyBuf.String())
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

	runner := &Runner{
		Client: llama.NewClient(server.URL, 5*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy:    RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
		AgentName: "main",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Implement the change."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	mu.Lock()
	bodies := append([]string(nil), requestBodies...)
	finalRequests := requests
	mu.Unlock()

	if finalRequests < 2 {
		t.Fatalf("expected at least 2 model calls (origin + repair); got %d. Bodies seen:\n%s", finalRequests, strings.Join(bodies, "\n---\n"))
	}

	if strings.Contains(answer, "Changed paths: none") || strings.Contains(answer, "Blocker:") {
		t.Fatalf("leaked implementation blocker template must not surface as the user-facing answer; got:\n%s", answer)
	}

	if len(bodies) >= 2 {
		secondBody := bodies[1]
		if !strings.Contains(secondBody, "File search rejected") &&
			!strings.Contains(secondBody, "Changed paths: none") {
			t.Fatalf("second model call should carry the leaked template as evidence for rewrite; got body:\n%s", secondBody)
		}
	}
}

func TestRunnerEndToEndReadApprovalGateApprovesOutOfScopeRead(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/runtime/agent/foo.go", "package agent\n\nfunc Foo() {}\n"); err != nil {
		t.Fatalf("write seed source: %v", err)
	}
	if err := writeTestFile(root, "internal/runtime/agent/other.go", "package agent\n\nfunc Other() {}\n"); err != nil {
		t.Fatalf("write seed out-of-scope source: %v", err)
	}
	if err := writeTestFile(root, "go.mod", "module idiomtest\n\ngo 1.22\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	decider := &fakeDecider{decision: readapproval.Decision{
		Outcome: readapproval.OutcomeApproved,
		Reason:  readapproval.ReasonOperatorApprovedLive,
		Source:  readapproval.SourceOperator,
	}}
	cache := newFakeCache()
	gate := &ReadApprovalGate{Decider: decider, Cache: cache}

	replies := []string{
		`{"tool":"read_file","path":"internal/runtime/agent/foo.go","start_line":1,"end_line":40}`,
		`{"tool":"read_file","path":"internal/runtime/agent/other.go"}`,
		"Decision: source-satisfied\n\ninternal/runtime/agent/foo.go covers Foo().",
	}
	requests := 0
	var requestBodies []string
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		mu.Lock()
		idx := requests
		requests++
		// Read all body bytes
		var bodyBuf strings.Builder
		tmp := make([]byte, 1<<18)
		for {
			n, err := r.Body.Read(tmp)
			if n > 0 {
				bodyBuf.Write(tmp[:n])
			}
			if err != nil {
				break
			}
		}
		requestBodies = append(requestBodies, bodyBuf.String())
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

	runner := &Runner{
		Client: llama.NewClient(server.URL, 5*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy:           RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 4},
		ReadApprovalGate: gate,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			Objective:         "Plan a change to Foo().",
			PermissionProfile: "guarded_workspace_write",
			AllowedEditPaths:  []string{"internal/runtime/agent/foo.go"},
			AllowedTools:      []string{"read_file"},
		},
	}

	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Plan a change."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if decider.calls == 0 {
		t.Fatal("expected the gate to consult the decider for the out-of-scope source read; decider was never called")
	}
	if decider.lastReq.Path != "internal/runtime/agent/other.go" {
		t.Fatalf("decider was called for an unexpected path: %q", decider.lastReq.Path)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, body := range requestBodies {
		if strings.Contains(body, "func Other()") {
			return
		}
	}
	t.Fatalf("expected the operator-approved read content to reach the model in a subsequent request; the read was not dispatched despite decider approval. Bodies:\n%s", strings.Join(requestBodies, "\n---\n"))
}

func TestRunnerEndToEndReadApprovalGateAppendsOperatorDenialToRejection(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "internal/runtime/agent/foo.go", "package agent\n\nfunc Foo() {}\n"); err != nil {
		t.Fatalf("write seed source: %v", err)
	}
	if err := writeTestFile(root, "internal/runtime/agent/other.go", "package agent\n\nfunc Other() {}\n"); err != nil {
		t.Fatalf("write seed out-of-scope source: %v", err)
	}
	if err := writeTestFile(root, "go.mod", "module idiomtest\n\ngo 1.22\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	decider := &fakeDecider{decision: readapproval.Decision{
		Outcome: readapproval.OutcomeDenied,
		Reason:  readapproval.ReasonOperatorDeniedLive,
		Source:  readapproval.SourceOperator,
	}}
	gate := &ReadApprovalGate{Decider: decider, Cache: newFakeCache()}

	replies := []string{
		`{"tool":"read_file","path":"internal/runtime/agent/foo.go","start_line":1,"end_line":40}`,
		`{"tool":"read_file","path":"internal/runtime/agent/other.go"}`,
		"Decision: source-satisfied\n\ninternal/runtime/agent/foo.go covers Foo().",
	}
	requests := 0
	var operatorDenialObservedByModel bool
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		idx := requests
		requests++
		// Read all body bytes
		var bodyBuf strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Body.Read(buf)
			if n > 0 {
				bodyBuf.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		body := bodyBuf.String()
		if strings.Contains(body, "Operator denied") && strings.Contains(body, "do not retry") {
			mu.Lock()
			operatorDenialObservedByModel = true
			mu.Unlock()
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
	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, 5*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy:           RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 4},
		ReadApprovalGate: gate,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			Objective:         "Plan a change to Foo().",
			PermissionProfile: "guarded_workspace_write",
			AllowedEditPaths:  []string{"internal/runtime/agent/foo.go"},
			AllowedTools:      []string{"read_file"},
		},
	}

	_, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Plan a change."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if decider.calls == 0 {
		t.Fatal("expected the gate to consult the decider for the out-of-scope source read")
	}
	mu.Lock()
	observed := operatorDenialObservedByModel
	mu.Unlock()
	if !observed {
		t.Fatal("expected the operator-denial framing to be appended to the rejection that the model receives")
	}
}

var _ = fmt.Sprintf
