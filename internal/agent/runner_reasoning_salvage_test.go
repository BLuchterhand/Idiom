package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		"no markers":      "I think the draft is OK. No bugs to find. Done.",
		"only Bugs found": "## Bugs found\n\n1. Bug A\n\nThat's all I have time for.",
		"only Verdict":    "I'll think about this.\n\n## Verdict\n\nship-as-is\n",
		"too short":       "## Bugs found\n## Verdict\nship-as-is\n",
		"empty":           "",
		"whitespace only": "   \n\n\t   ",
	}
	for name, reasoning := range cases {
		got := extractStructuredMarkdownFromReasoning(reasoning)
		if got != "" {
			t.Errorf("case %q: expected empty extraction, got %d chars: %q", name, len(got), got)
		}
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
