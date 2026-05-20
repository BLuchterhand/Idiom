package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"idiom/internal/config"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
	"idiom/internal/workspace"
)

func TestExtractStructuredMarkdownFromReasoningHappyPath(t *testing.T) {
	reasoning := "OK so the brief says X. Let me check Y. Actually let me think...\n" +
		"Hmm, the draft is missing a few things. Let me write the review:\n\n" +
		"# Review of task-XYZ\n\n" +
		"## Bugs found\n\n" +
		"1. **Off-by-one in loop**: file.go:42 should be < not <=\n" +
		"2. **Missing import**: stale `time` reference\n\n" +
		"## OK\n\n" +
		"- Anchor verification looks complete\n" +
		"- Test file naming is correct\n\n" +
		"## Verdict\n\nfix-bugs-then-ship\n"
	out := extractStructuredMarkdownFromReasoning(reasoning)
	if out == "" {
		t.Fatal("expected extraction to succeed")
	}
	if !strings.Contains(out, "## Bugs found") {
		t.Fatalf("output missing Bugs found section: %s", out)
	}
	if !strings.Contains(out, "## Verdict") {
		t.Fatalf("output missing Verdict section: %s", out)
	}
	if strings.Contains(out, "OK so the brief") {
		t.Fatalf("output should not include pre-title chatter: %s", out)
	}
}

func TestExtractStructuredMarkdownFromReasoningRefusesUnstructuredText(t *testing.T) {
	cases := map[string]string{
		"no markers":        "I think the draft is OK. No bugs to find. Done.",
		"only Bugs found":   "## Bugs found\n\n1. Bug A\n\nThat's all I have time for.",
		"only Verdict":      "I'll think about this.\n\n## Verdict\n\nship-as-is\n",
		"too short":         "## Bugs found\n## Verdict\nship-as-is\n",
		"only Files Modify": "I should put it in ## Files To Modify section. But no Required Behavior to corroborate.",
		"empty":             "",
		"whitespace only":   "   \n\n\t   ",
	}
	for name, reasoning := range cases {
		got := extractStructuredMarkdownFromReasoning(reasoning)
		if got != "" {
			t.Errorf("case %q: expected empty extraction, got %d chars: %q", name, len(got), got)
		}
	}
}

func TestExtractStructuredMarkdownFromReasoningRecognizesDraftShape(t *testing.T) {
	reasoning := "OK so the spec asks for a --mode flag. Let me think through the brief...\n\n" +
		"I'll need to touch lane.go and lane_history.go. Let me write it up:\n\n" +
		"# Task lane-history-mode — Add `--lane-mode` filter\n\n" +
		"## Problem\n\n" +
		"`idiom lane history` lacks a mode filter. Add `--lane-mode <name>` to filter the summary.\n\n" +
		"## Files To Modify\n\n" +
		"| File | Change |\n|------|--------|\n" +
		"| `cmd/idiom/lane.go` | ADD `--lane-mode` flag |\n" +
		"| `cmd/idiom/lane_history.go` | ADD `ModeFilter` field |\n\n" +
		"## Verified Current State\n\n" +
		"`laneHistoryFilter` is at `cmd/idiom/lane_history.go:120`.\n\n" +
		"## Required Behavior\n\n" +
		"1. Add `ModeFilter string` to laneHistoryFilter.\n" +
		"2. Skip events whose Mode doesn't match.\n\n" +
		"## Acceptance\n\n`go test ./cmd/idiom/` passes.\n"
	out := extractStructuredMarkdownFromReasoning(reasoning)
	if out == "" {
		t.Fatal("expected draft-shape extraction to succeed")
	}
	if !strings.Contains(out, "## Files To Modify") {
		t.Fatalf("output missing Files To Modify: %s", out)
	}
	if !strings.Contains(out, "## Required Behavior") {
		t.Fatalf("output missing Required Behavior: %s", out)
	}
	if !strings.Contains(out, "# Task lane-history-mode") {
		t.Fatalf("output should include the # title from look-back: %s", out)
	}
	if strings.Contains(out, "OK so the spec asks") {
		t.Fatalf("output should not include pre-title chatter: %s", out)
	}
}

func TestExtractStructuredMarkdownFromReasoningRecognizesRequiredEditsDraftShape(t *testing.T) {
	reasoning := "Now I have both inputs. Let me apply the review comments to the previous draft.\n\n" +
		"# Corrected implementation brief\n\n" +
		"## Problem\n\n" +
		"Use the exact anchors already captured by the packet.\n\n" +
		"## Files To Modify\n\n" +
		"| File | Change |\n|------|--------|\n" +
		"| `native/src/appraisal.cpp` | MODIFY: add symmetric branch |\n\n" +
		"## Verified Current State\n\n" +
		"`native/src/appraisal.cpp:12` contains `if (result.valence < 0.0f) {`.\n\n" +
		"## Required Edits\n\n" +
		"1. Insert the positive-valence branch after the existing negative branch.\n\n" +
		"## Acceptance\n\n" +
		"- focused tests pass\n"
	out := extractStructuredMarkdownFromReasoning(reasoning)
	if out == "" {
		t.Fatal("expected required-edits draft-shape extraction to succeed")
	}
	if strings.Contains(out, "Now I have both inputs") {
		t.Fatalf("output should not include prose preface: %s", out)
	}
	if !strings.Contains(out, "## Required Edits") || !strings.Contains(out, "# Corrected implementation brief") {
		t.Fatalf("output missing corrected brief sections: %s", out)
	}
}

func TestExtractStructuredMarkdownFromReasoningPicksLastReviewWhenIterated(t *testing.T) {
	reasoning := strings.Repeat("filler-thinking-content. ", 30) +
		"\n\n## Bugs found\n\n1. First attempt bug\n\n## Verdict\n\nfix-bugs\n\n" +
		"Actually wait, let me reconsider. Hmm.\n\n" +
		"# Review (revised)\n\n## Bugs found\n\n1. Better bug A\n2. Better bug B\n\n## OK\n- Anchors look right\n\n## Verdict\n\nfix-bugs-then-ship\n"
	out := extractStructuredMarkdownFromReasoning(reasoning)
	if out == "" {
		t.Fatal("expected extraction")
	}
	if !strings.Contains(out, "# Review (revised)") {
		t.Fatalf("output should pick the revised pass: %s", out)
	}
	if strings.Contains(out, "First attempt bug") {
		t.Fatalf("output should not include the earlier iteration: %s", out)
	}
}

func TestAutoSalvageRecoversFromReasoningWhenReplyIsEmpty(t *testing.T) {
	dir := t.TempDir()
	mgr := workspace.NewManager(config.Workspace{Root: dir, CWD: dir, EnableWrites: true})

	required := "review.md"
	reasoning := "OK so the brief has 3 file-allowlist rows for a single feature. Let me check the anchors are right.\n\n" +
		"Looking at lane_history.go around line 100 — the laneHistoryFilter struct exists already from #76. Good.\n\n" +
		"# Review of test-atom\n\n" +
		"## Bugs found\n\n" +
		"1. **Bug A**: needs a fix to anchor at lane.go:200\n" +
		"2. **Bug B**: another fix to test file at lane_history_test.go:144\n\n" +
		"## OK\n\n- Anchor verification looks complete\n- Test fixture pattern matches existing helpers\n\n" +
		"## Verdict\n\nfix-bugs-then-ship\n"

	r := &Runner{
		Workspace: mgr,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			RequiredEditPaths: []string{required},
		},
		LastAssistantMessage: llama.ChatMessage{
			Content:          "",
			ReasoningContent: reasoning,
		},
	}

	if !r.tryAutoSalvageMarkdownDeliverable("") {
		t.Fatal("expected auto-salvage to succeed from reasoning content")
	}
	body, err := os.ReadFile(filepath.Join(dir, required))
	if err != nil {
		t.Fatalf("read salvaged file: %v", err)
	}
	if !strings.Contains(string(body), "## Bugs found") {
		t.Fatalf("salvaged file should contain Bugs found: %s", body)
	}
	if !strings.Contains(string(body), "## Verdict") {
		t.Fatalf("salvaged file should contain Verdict: %s", body)
	}
	_ = context.Background()
}

func TestAutoSalvageExtractsStructuredMarkdownFromReplyProse(t *testing.T) {
	dir := t.TempDir()
	mgr := workspace.NewManager(config.Workspace{Root: dir, CWD: dir, EnableWrites: true})

	required := "draft.md"
	reply := "Now I have both inputs. I will apply the review comments before writing the corrected draft.\n\n" +
		"# Corrected atom brief\n\n" +
		"## Problem\n\nUse the exact anchors already provided.\n\n" +
		strings.Repeat("Long evidence paragraph keeps the H1 far away from the final allowlist section.\n", 20) +
		"## Files To Modify\n\n| File | Change |\n|------|--------|\n| `a.go` | MODIFY: add guard |\n\n" +
		"## Verified Current State\n\n`a.go:12` contains `return nil`.\n\n" +
		"## Required Edits\n\n1. Replace the guard with checked behavior.\n\n" +
		"## Acceptance\n\n- `go test ./...`\n"

	r := &Runner{
		Workspace: mgr,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			RequiredEditPaths: []string{required},
		},
	}

	if !r.tryAutoSalvageMarkdownDeliverable(reply) {
		t.Fatal("expected auto-salvage to extract structured markdown from reply prose")
	}
	body, err := os.ReadFile(filepath.Join(dir, required))
	if err != nil {
		t.Fatalf("read salvaged file: %v", err)
	}
	text := string(body)
	if strings.Contains(text, "Now I have both inputs") {
		t.Fatalf("salvage should omit pre-deliverable prose: %s", text)
	}
	if !strings.Contains(text, "# Corrected atom brief") || !strings.Contains(text, "## Problem") || !strings.Contains(text, "## Required Edits") {
		t.Fatalf("salvaged file missing corrected draft shape: %s", text)
	}
}

func TestRunAutoSalvageRecordsMarkdownDeliverableWrite(t *testing.T) {
	dir := t.TempDir()
	required := "draft.md"
	reply := "# Corrected atom brief\n\n" +
		"## Problem\n\nUse the exact anchors already provided and record blockers in the brief.\n\n" +
		strings.Repeat("Evidence line keeps this close to the long refine drafts seen in practice.\n", 40) +
		"## Files To Modify\n\n| File | Change |\n|------|--------|\n| `a.go` | MODIFY: add guard |\n\n" +
		"## Required Edits\n\n1. Replace the guard with checked behavior.\n\n" +
		"## Acceptance\n\n- Planner-owned verification remains pending.\n"

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var completion runnerCompletionStatus
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         dir,
			CWD:          dir,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			VerificationContract: "not_required_for_text_mutations",
			PermissionProfile:    "guarded_workspace_write",
			RequiredEditPaths:    []string{required},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "completion_status" {
				if err := json.Unmarshal([]byte(event.Content), &completion); err != nil {
					t.Fatalf("decode completion status: %v", err)
				}
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "write the corrected draft"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.HasPrefix(strings.TrimSpace(answer), "<<QUESTION>>") {
		t.Fatalf("auto-salvage should satisfy required path blocker, got: %s", answer)
	}
	body, err := os.ReadFile(filepath.Join(dir, required))
	if err != nil {
		t.Fatalf("read salvaged file: %v", err)
	}
	if !strings.Contains(string(body), "## Required Edits") {
		t.Fatalf("salvaged file missing expected body: %s", body)
	}
	if completion.Status != "completed" || completion.Reason != "auto_salvage_markdown_deliverable" {
		t.Fatalf("expected completed auto-salvage status, got %#v", completion)
	}
	if requests != 1 {
		t.Fatalf("expected one model request, got %d", requests)
	}
}

func TestAutoSalvageSkipsQuestionBlockerEvenWithMarkdownShape(t *testing.T) {
	dir := t.TempDir()
	mgr := workspace.NewManager(config.Workspace{Root: dir, CWD: dir, EnableWrites: true})
	r := &Runner{
		Workspace: mgr,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			RequiredEditPaths: []string{"draft.md"},
		},
	}
	reply := "<<QUESTION>>: blocked on missing evidence.\n\n" +
		"## Files To Modify\n\n| File | Change |\n|------|--------|\n| `a.go` | MODIFY |\n\n" +
		"## Required Edits\n\n1. Cannot write safely.\n"
	if r.tryAutoSalvageMarkdownDeliverable(reply) {
		t.Fatal("question blockers must not be salvaged into deliverable files")
	}
	if _, err := os.Stat(filepath.Join(dir, "draft.md")); !os.IsNotExist(err) {
		t.Fatalf("draft.md should not exist after skipped salvage, stat err=%v", err)
	}
}

func TestMutationCompletionRejectionRequiresQuestionForMissingPathBlocker(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			RequiredEditPaths: []string{"draft.md"},
		},
	}
	result, rejected := runner.mutationCompletionRejection("blocked because draft.md needs more evidence", nil)
	if !rejected {
		t.Fatalf("missing required path should reject blocker-like prose without <<QUESTION>>, got %#v", result)
	}
	if !strings.Contains(result.Error, "no successful write tool result") {
		t.Fatalf("expected no-write gate error, got: %s", result.Error)
	}
	result, rejected = runner.mutationCompletionRejection("<<QUESTION>>: blocked because draft.md needs more evidence", nil)
	if rejected {
		t.Fatalf("path-specific <<QUESTION>> blocker should be accepted, got %#v", result)
	}
}

func TestCompletionGateRecoveryKeepsScratchpadDeliverableOutputBudget(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		RequiredEditPaths: []string{".idiom/refine/example/iter-3/draft.md"},
	}
	opts := completionGateRecoveryChatOptions(llama.ChatOptions{MaxTokens: 16384}, brief, nil)
	if opts.MaxTokens != 16384 {
		t.Fatalf("scratchpad recovery must preserve full deliverable budget, got %d", opts.MaxTokens)
	}
	opts = completionGateRecoveryChatOptions(llama.ChatOptions{}, brief, nil)
	if opts.MaxTokens != 16384 {
		t.Fatalf("scratchpad recovery should default to full deliverable budget, got %d", opts.MaxTokens)
	}
}

func TestAutoSalvageSkipsPlanExecutePlannerPhase(t *testing.T) {
	dir := t.TempDir()
	mgr := workspace.NewManager(config.Workspace{Root: dir, CWD: dir, EnableWrites: true})
	r := &Runner{
		Workspace: mgr,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"draft.md"},
		},
	}
	reply := "# Proposed Plan\n\n" +
		"## Files To Modify\n\n| File | Change |\n|------|--------|\n| `draft.md` | NEW FILE |\n\n" +
		"## Required Edits\n\n1. Write this markdown file.\n\n" +
		"## Acceptance\n\n- Review the generated content.\n"
	if r.tryAutoSalvageMarkdownDeliverable(reply) {
		t.Fatal("plan-execute planner phase must not run markdown deliverable auto-salvage")
	}
	if _, err := os.Stat(filepath.Join(dir, "draft.md")); !os.IsNotExist(err) {
		t.Fatalf("draft.md should not exist after skipped plan-execute salvage, stat err=%v", err)
	}
}

func TestExtractStructuredMarkdownFromReasoningRejectsUnfilledTemplate(t *testing.T) {
	// Real shape observed in v10 dispatch: the model emits 0 visible
	// content and its reasoning contains a copy of the meta-brief
	// skeleton with `<bracket>` placeholders still unfilled. The
	// salvage must refuse to ship that as a deliverable.
	reasoning := "OK so the user wants me to write a brief. Let me look at the template structure.\n\n" +
		"The skeleton says:\n\n" +
		"## Files To Modify\n\n" +
		"| File | Change |\n|------|--------|\n" +
		"| `RELATIVE_PATH` | <one action per row; NEW FILE rows are exempt from the one-action rule (ADD, MODIFY, or NEW FILE: ...)> |\n\n" +
		"## Verified Current State\n\n<quote relevant existing code with `<file:LINE>` anchors. Verify line numbers exist before citing.>\n\n" +
		"## Required Behavior\n\n1. <numbered step with EXACT old_text/new_text or full new-file content in ```text fences>\n2. <next step>\n\n" +
		"## Acceptance\n\n- `<shell command that verifies the change>`\n\nI need to fill in the placeholders.\n"
	got := extractStructuredMarkdownFromReasoning(reasoning)
	if got != "" {
		t.Fatalf("expected salvage to refuse unfilled-template content; got %d chars: %q", len(got), got)
	}
}

func TestAutoSalvageStillSkipsWhenNeitherReplyNorReasoningHasMarkdown(t *testing.T) {
	dir := t.TempDir()
	mgr := workspace.NewManager(config.Workspace{Root: dir, CWD: dir, EnableWrites: true})

	r := &Runner{
		Workspace: mgr,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			RequiredEditPaths: []string{"review.md"},
		},
		LastAssistantMessage: llama.ChatMessage{
			Content:          "",
			ReasoningContent: "just some chatter, no structure at all",
		},
	}
	if r.tryAutoSalvageMarkdownDeliverable("") {
		t.Fatal("expected salvage to skip when neither reply nor reasoning is structured")
	}
}
