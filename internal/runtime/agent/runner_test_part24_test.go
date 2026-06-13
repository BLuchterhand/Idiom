package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/workerruntime"
)

func TestFinalAnswerBlockerKindRejectsLineClaimWithoutSourceBody(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "internal", "surfaces", "tui", "app", "lifecycle.go")

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := os.WriteFile(path, []byte("package app\n\nfunc Update() {}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer := "The root cause is in `internal/surfaces/tui/app/lifecycle.go` lines 418-424 where `WindowSizeMsg` is handled."
	results := []workspace.ToolResult{
		{Tool: "context_pack", Summary: "Context pack wrote packet", Output: "internal/surfaces/tui/app/lifecycle.go"},
	}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "source_grounding_unmet" {
		t.Fatalf("ungrounded source line claim got blocker_kind=%q, want source_grounding_unmet", kind)
	}

	if !strings.Contains(reason, "source-body read") {
		t.Fatalf("blocker reason should ask for source-body read, got %q", reason)
	}
}

func TestFinalAnswerBlockerKindRejectsTopLevelLineClaimWithoutSourceBody(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")

	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer := "The root cause is in `main.go:3`, where `main` starts the process."

	kind, reason := runner.finalAnswerBlockerKind(answer, []workspace.ToolResult{
		{Tool: "context_pack", Summary: "Context pack wrote packet", Output: "main.go"},
	})
	if kind != "source_grounding_unmet" {
		t.Fatalf("top-level line claim without source body got blocker_kind=%q reason=%q, want source_grounding_unmet", kind, reason)
	}
}

func TestFinalAnswerBlockerKindRejectsCandidateLineClaimWithoutSourceBody(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "internal", "surfaces", "tui", "app", "lifecycle.go")

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := os.WriteFile(path, []byte("package app\n\nfunc Update() {}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer := "The candidate root cause is in `internal/surfaces/tui/app/lifecycle.go` lines 418-424 where `WindowSizeMsg` is handled."
	results := []workspace.ToolResult{
		{Tool: "context_pack", Summary: "Context pack wrote packet", Output: "internal/surfaces/tui/app/lifecycle.go"},
	}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "source_grounding_unmet" {
		t.Fatalf("candidate line claim without source body got blocker_kind=%q reason=%q, want source_grounding_unmet", kind, reason)
	}
}

func TestFinalAnswerBlockerKindAllowsEvidencePathListWithoutSourceBody(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "internal", "runtime", "agent", "runner.go")

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := os.WriteFile(path, []byte("package agent\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer := "Evidence paths: `internal/runtime/agent/runner.go`. The available evidence is not enough to name an exact function."

	kind, reason := runner.finalAnswerBlockerKind(answer, []workspace.ToolResult{
		{Tool: "context_pack", Summary: "Context pack wrote packet", Output: "internal/runtime/agent/runner.go"},
	})
	if kind != "" || reason != "" {
		t.Fatalf("plain evidence path list should not require source body: kind=%q reason=%q", kind, reason)
	}
}

func TestFinalAnswerBlockerKindRejectsMissingCanonicalRootFile(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer := "The root cause is in `go.mod`, which pins the wrong dependency."

	kind, reason := runner.finalAnswerBlockerKind(answer, nil)
	if kind != "source_grounding_unmet" {
		t.Fatalf("missing canonical root file got blocker_kind=%q reason=%q, want source_grounding_unmet", kind, reason)
	}
}

func TestFinalAnswerBlockerKindRejectsMissingCanonicalRootLineCitation(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer := "The root cause is in `go.mod:4`, which pins the wrong dependency."

	kind, reason := runner.finalAnswerBlockerKind(answer, nil)
	if kind != "source_grounding_unmet" {
		t.Fatalf("missing canonical root line citation got blocker_kind=%q reason=%q, want source_grounding_unmet", kind, reason)
	}
}

func TestFinalAnswerBlockerKindRejectsMissingTopLevelLineCitation(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer := "The root cause is in `main.go:42`, where startup resets the viewport."

	kind, reason := runner.finalAnswerBlockerKind(answer, nil)
	if kind != "source_grounding_unmet" {
		t.Fatalf("missing top-level line citation got blocker_kind=%q reason=%q, want source_grounding_unmet", kind, reason)
	}
}

func TestFinalAnswerBlockerKindRejectsMissingTopLevelSourceDetailCitation(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer := "The root cause is in `main.go`, where startup resets the viewport."

	kind, reason := runner.finalAnswerBlockerKind(answer, nil)
	if kind != "source_grounding_unmet" {
		t.Fatalf("missing top-level source-detail citation got blocker_kind=%q reason=%q, want source_grounding_unmet", kind, reason)
	}
}

func TestFinalAnswerBlockerKindIgnoresBareExtensionMentions(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer := "Evidence is attached to source-body reads for Go source files (`.go`) before the final answer cites implementation behavior."

	kind, reason := runner.finalAnswerBlockerKind(answer, nil)
	if kind != "" || reason != "" {
		t.Fatalf("bare extension mention should not be treated as a missing workspace path: kind=%q reason=%q", kind, reason)
	}
}

func TestFinalAnswerBlockerKindAllowsLineClaimWithSourceBody(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "internal", "surfaces", "tui", "app", "lifecycle.go")

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := os.WriteFile(path, []byte("package app\n\nfunc Update() {}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer := "The resize path is in `internal/surfaces/tui/app/lifecycle.go` lines 1-3 where `Update` handles messages."
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/surfaces/tui/app/lifecycle.go lines 1-3 (total lines: 3)",
			Output:  "1:package app\n2:\n3:func Update() {}",
		},
	}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "" || reason != "" {
		t.Fatalf("source-body-backed answer blocked: kind=%q reason=%q", kind, reason)
	}
}

func TestFinalAnswerBlockerKindAllowsUniqueBasenameBackedBySourceBody(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "internal", "surfaces", "tui", "app", "model_state.go")

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := os.WriteFile(path, []byte("package app\n\nfunc (m *Model) resize() {}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer := "The resize path is in `model_state.go` lines 1-3 where `Model.resize` handles viewport dimensions."
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "capability: read_symbol\nroute_summary: Read Model.resize in internal/surfaces/tui/app/model_state.go lines 1-3\n1:package app\n2:\n3:func (m *Model) resize() {}",
		},
	}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "" || reason != "" {
		t.Fatalf("unique basename backed by source-body evidence blocked: kind=%q reason=%q", kind, reason)
	}
}

func TestFinalAnswerBlockerKindRejectsAmbiguousBasenameSourceCitation(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"internal/surfaces/tui/app/model_state.go", "internal/other/model_state.go"} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}

		if err := os.WriteFile(path, []byte("package app\n\nfunc Resize() {}\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	}
	answer := "The resize path is in `model_state.go` lines 1-3 where `Resize` handles viewport dimensions."
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read Model.resize in internal/surfaces/tui/app/model_state.go lines 1-3\n1:package app\n3:func Resize() {}",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read Other.resize in internal/other/model_state.go lines 1-3\n1:package app\n3:func Resize() {}",
		},
	}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "source_grounding_unmet" {
		t.Fatalf("ambiguous basename source citation got blocker_kind=%q reason=%q, want source_grounding_unmet", kind, reason)
	}
}

// TestMissingRequiredEvidenceEnforcesTargetLocation verifies that
// the target_location evidence label is structurally enforced.
func TestMissingRequiredEvidenceEnforcesTargetLocation(t *testing.T) {
	// Answer with no file paths, no changed paths in results
	missing := missingRequiredEvidence(
		[]string{"target_location"},
		"no paths here",
		[]workspace.ToolResult{},
	)
	if len(missing) != 1 || missing[0] != "target_location" {
		t.Fatalf("expected missing=[target_location], got %v", missing)
	}

	// Answer with a file path — satisfied
	missing = missingRequiredEvidence(
		[]string{"target_location"},
		"the code is in internal/runtime/agent/runner.go",
		[]workspace.ToolResult{},
	)
	if len(missing) != 0 {
		t.Fatalf("expected satisfied, got missing=%v", missing)
	}
}

// TestMissingRequiredEvidenceSkipsSemanticLabels verifies that
// non-deterministic labels (answer_addresses_user_request) are
// never enforced — they are model guidance only.
func TestMissingRequiredEvidenceSkipsSemanticLabels(t *testing.T) {
	missing := missingRequiredEvidence(
		[]string{"answer_addresses_user_request"},
		"completely irrelevant answer about something else",
		[]workspace.ToolResult{},
	)
	if len(missing) != 0 {
		t.Fatalf("semantic label should never be enforced: got missing=%v", missing)
	}
}

// TestConversationLaneWithoutSemanticEval verifies correct
// identification of conversation/direct_answer lanes for
// confidence demotion.
func TestConversationLaneWithoutSemanticEval(t *testing.T) {
	cases := []struct {
		name       string
		family     string
		contract   string
		permission string
		objective  string
		want       bool
	}{
		{"legacy conversation without read-only permission", "conversation", "direct_answer", "", "Explain readiness.", true},
		{"read-only conversation with relevance contract", "conversation", "direct_answer", "read_only_workspace", "Explain readiness.", false},
		{"direct-answer alias with relevance contract", "direct_answer", "direct_answer", "read_only_workspace", "Explain readiness.", false},
		{"empty objective cannot be semantically checked", "conversation", "direct_answer", "read_only_workspace", "", true},
		{"conversation implementation result", "conversation", "implementation_result", "read_only_workspace", "Explain readiness.", false},
		{"architecture direct answer", "architecture", "direct_answer", "read_only_workspace", "Explain readiness.", false},
		{"architecture assessment", "architecture", "architecture_assessment", "read_only_workspace", "Explain readiness.", false},
		{"implementation result", "investigate_then_implement", "implementation_result", "guarded_workspace_write", "Explain readiness.", false},
	}
	for _, c := range cases {
		brief := workerruntime.WorkerRuntimeBrief{
			TaskFamily:        c.family,
			Contract:          c.contract,
			PermissionProfile: c.permission,
			Objective:         c.objective,
		}
		got := conversationLaneWithoutSemanticEval(brief)

		if got != c.want {
			t.Errorf("%s: conversationLaneWithoutSemanticEval(%q, %q, %q) = %v, want %v",
				c.name, c.family, c.contract, c.permission, got, c.want)
		}
	}
}

// TestlooksLikeNonAnswerProseRejectsLiveFailures verifies that all
// five failure patterns from today's conversation logs are caught.
func TestLooksLikeNonAnswerProseRejectsLiveFailures(t *testing.T) {
	cases := []struct {
		name   string
		answer string
		want   bool
	}{
		{
			"05:23 - unexecuted tool call in prose",
			`[Calling tool: search_content({"tool":"search_content","pattern":"Evidence"})]`,
			true,
		},
		{
			"07:29 - echoed user question",
			"Question: Can you investigate the strange spacing issues?",
			true,
		},
		{
			"08:09 - file search rejected",
			"Changed paths: none\n\nVerification: not run.\n\nBlocker:\n- File search rejected: pattern is required",
			true,
		},
		{
			"08:28 - command failed exit status",
			"Changed paths: none\n\nVerification: not run.\n\nBlocker:\n- Content search failed: command failed: exit status 2",
			true,
		},
		{
			"18:06 - hidden reasoning meta-intent restatement",
			`The user is asking me to investigate why a prompt asking "which files and sections should be changed" might be routed as investigate_then_implement with write_intent=yes. I need to find the specific route-planner files/tests responsible and the smallest generic fix shape.`,
			true,
		},
		{
			"18:10 - source echo only",
			"The inspected source in `internal/runtime/agent/runner_test.go` contains `func TestLooksLikeNonAnswerProseRejectsLiveFailures(t *testing.T) {`, `cases := []struct {` and `name   string`. No files were changed.",
			true,
		},
		{
			"15:41 - short post-readiness source inventory",
			"For `what would it take to enable up and down arrow keys to iterate through the user's chat history?`, `internal/surfaces/tui/app/app.go` shows defines `truncateText`, defines `normalizeHistoryRole`, and `normalizeHistoryRole` returns `strings.ToUpper(value[:1]) + value[1:]`.",
			true,
		},
		{
			"16:13 - inspected body source inventory",
			"`Model.handleKeyPress` in `internal/surfaces/tui/app/update_handlers.go` is the clearest inspected implementation anchor. The inspected body shows that it defines `Model.handleKeyPress`, sets `m.history.expanded` to `false`, `Model.handleKeyPress` calls `renderHistoryPanelContent`, sets `m.history.expanded` to `!m.history.expanded`, and sets `m.history.focused` to `false`.",
			true,
		},
		{
			"legitimate answer (should NOT be blocked)",
			"Evidence in Idiom is composed of source paths, tool results, and verification status. The harness uses these across all lanes to enforce contracts.",
			false,
		},
		{
			"legitimate answer with task framing (should NOT be blocked)",
			"The user is asking me to explain the route choice. The answer is that `internal/surfaces/tui/app/turn_planner.go` classifies read-only evidence requests before mutation intent, so this should stay in a read-only lane.",
			false,
		},
		{
			"legitimate source-citing synthesis (should NOT be blocked)",
			"The inspected source in `internal/runtime/agent/runner.go` contains `looksLikeNonAnswerProse`, which is the shared final-answer guard because `finalAnswerBlockerKind` calls it before accepting answer-tool content.",
			false,
		},
		{
			"legitimate path-citing mechanism answer (should NOT be blocked)",
			"For chat-history navigation, `internal/surfaces/tui/app/model_state.go` would need selection state while `internal/surfaces/tui/app/update_handlers.go` would route up/down keys through the existing submit flow. That preserves the current message-send path while adding editable recall.",
			false,
		},
		{
			"legitimate blocker explanation (should NOT be blocked)",
			"Blocker: the model had not read the source body yet, so the harness correctly asked for evidence before accepting the answer.",
			false,
		},
		{
			"legitimate command failure explanation (should NOT be blocked)",
			"The command failed with exit status 2 because the search pattern was empty; the root cause was the missing query text, not the repository state.",
			false,
		},
		{
			"legitimate diagnostic answer with failure wording (should NOT be blocked)",
			"The failure was in the acceptance boundary: a diagnostic log was treated as source evidence even though it did not prove the code-level claim.",
			false,
		},
		{
			"<<QUESTION>> blocker is handled by the explicit escape hatch",
			"<<QUESTION>>: Cannot locate the target file. The path does not exist.",
			false,
		},
	}
	for _, c := range cases {
		got := looksLikeNonAnswerProse(c.answer)
		if got != c.want {
			t.Errorf("%s: looksLikeNonAnswerProse = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestFinalAnswerBlockerKindAllowsSubstantiveBlockerAndFailureAnswers(t *testing.T) {
	runner := &Runner{}
	cases := []string{
		"Blocker: the reviewer could not approve the patch because the diagnostic evidence did not match the user's source-code question.",
		"The command failed with exit status 2 because the supplied search pattern was empty; rerunning with a concrete pattern is the next action.",
	}

	for _, answer := range cases {
		blocker, reason := runner.finalAnswerBlockerKind(answer, nil)
		if blocker != "" || reason != "" {
			t.Fatalf("finalAnswerBlockerKind(%q) = (%q, %q), want accepted", answer, blocker, reason)
		}
	}
}

func TestFinalAnswerBlockerRejectsLiveNonAnswerShapes(t *testing.T) {
	runner := &Runner{}
	cases := []string{
		`The user is asking me to investigate why a prompt asking "which files and sections should be changed" might be routed as investigate_then_implement with write_intent=yes. I need to find the specific route-planner files/tests responsible and the smallest generic fix shape.`,
		"The inspected source in `internal/runtime/agent/runner_test.go` contains `func TestLooksLikeNonAnswerProseRejectsLiveFailures(t *testing.T) {`, `cases := []struct {` and `name   string`. No files were changed.",
		"For `what would it take to enable up and down arrow keys to iterate through the user's chat history?`, `internal/surfaces/tui/app/app.go` shows defines `truncateText`, defines `normalizeHistoryRole`, and `normalizeHistoryRole` returns `strings.ToUpper(value[:1]) + value[1:]`.",
		"`Model.handleKeyPress` in `internal/surfaces/tui/app/update_handlers.go` is the clearest inspected implementation anchor. The inspected body shows that it defines `Model.handleKeyPress`, sets `m.history.expanded` to `false`, `Model.handleKeyPress` calls `renderHistoryPanelContent`, sets `m.history.expanded` to `!m.history.expanded`, and sets `m.history.focused` to `false`.",
	}

	for _, answer := range cases {
		blocker, reason := runner.finalAnswerBlockerKind(answer, nil)
		if blocker != "non_answer_prose" || reason != "non_answer_prose" {
			t.Fatalf("finalAnswerBlockerKind(%q) = (%q, %q), want non_answer_prose", answer, blocker, reason)
		}
	}
}
