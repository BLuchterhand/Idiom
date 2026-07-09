package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/workflows/featureplanner"
)

// TestRehydratePlanStateRecoversLedgerAndHits drives the core resume
// invariant: reading a plan markdown that was produced by a prior
// session rebuilds the in-memory ledger bookkeeping faithfully enough
// that (a) a re-submitted task_ledger call is recognized as already
// rendered (no duplicates), and (b) prior writes count toward
// completion so a fresh write attaches under the same tid line.
func TestRehydratePlanStateRecoversLedgerAndHits(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.go", "a\n")
	writeFile(t, root, "b.go", "b\n")

	m := snapshotModel(t, root)

	// Seed: ledger with a multi-path item, one path written, plan and
	// diffs log carry the anchors the rehydrator relies on.
	m.recordTaskLedger(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "t1", Content: "rename across files", Status: "pending", Paths: []string{"a.go", "b.go"}},
		},
	}, workspace.ToolResult{Summary: "ok"})

	req := workspace.ToolRequest{Tool: "edit_file", Path: "a.go", Description: "rename a"}
	m.capturePlanSnapshot(1, req)
	writeFile(t, root, "a.go", "a; renamed\n")
	m.recordWriteSuccess(1, req, workspace.ToolResult{Summary: "ok"})

	planBefore, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))

	// Simulate restart: blow away the in-memory state a fresh process
	// would have, then rehydrate.
	m.plan.ledgerItems = nil
	m.plan.ledgerRenderedIDs = nil
	m.plan.ledgerHits = nil
	m.plan.sessionHeadingWritten = false
	m.planPath = ""

	rehydratePlanState(m)

	if !m.plan.sessionHeadingWritten {
		t.Errorf("session heading flag should be restored")
	}

	if !m.plan.ledgerRenderedIDs["t1"] {
		t.Errorf("rendered id set should include t1")
	}

	if got := m.plan.ledgerItems["t1"]; got.ID != "t1" || len(got.Paths) != 2 {
		t.Errorf("ledger item not rebuilt: %#v", got)
	}

	if !m.plan.ledgerHits["t1"]["a.go"] {
		t.Errorf("a.go should remain marked hit after rehydrate")
	}

	if m.plan.ledgerHits["t1"]["b.go"] {
		t.Errorf("b.go was not written pre-restart — should not be hit")
	}

	// Now finish the item: writing b.go should attach to the same line
	// (not fall to Unplanned) and flip the box.
	req2 := workspace.ToolRequest{Tool: "edit_file", Path: "b.go", Description: "rename b"}
	m.capturePlanSnapshot(2, req2)
	writeFile(t, root, "b.go", "b; renamed\n")
	m.recordWriteSuccess(2, req2, workspace.ToolResult{Summary: "ok"})

	planAfter, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	if strings.Contains(string(planAfter), "### Unplanned") {
		t.Errorf("post-rehydrate write fell to Unplanned:\nbefore=%s\nafter=%s", planBefore, planAfter)
	}

	if !strings.Contains(string(planAfter), "- [x] rename across files") {
		t.Errorf("box should flip after final path is hit:\n%s", planAfter)
	}
}

// TestRehydratePlanStatePreservesCheckedItems verifies that an item
// already rendered completed in the plan comes back marked completed,
// so a fresh task_ledger submission with the same id doesn't reopen it.
func TestRehydratePlanStatePreservesCheckedItems(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.go", "a\n")

	m := snapshotModel(t, root)

	m.recordTaskLedger(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "t1", Content: "touch a", Status: "pending", Paths: []string{"a.go"}},
		},
	}, workspace.ToolResult{Summary: "ok"})

	req := workspace.ToolRequest{Tool: "edit_file", Path: "a.go", Description: "touch"}
	m.capturePlanSnapshot(1, req)
	writeFile(t, root, "a.go", "a; v2\n")
	m.recordWriteSuccess(1, req, workspace.ToolResult{Summary: "ok"})

	// Wipe and rehydrate.
	m.plan.ledgerItems = nil
	m.plan.ledgerRenderedIDs = nil
	m.plan.ledgerHits = nil
	rehydratePlanState(m)

	item, ok := m.plan.ledgerItems["t1"]
	if !ok {
		t.Fatalf("t1 missing from rehydrated ledger")
	}

	if strings.ToLower(string(item.Status)) != "completed" {
		t.Errorf("checked item should rehydrate with status=completed, got %q", item.Status)
	}
}

// TestRehydratePlanStateMissingFileIsSilent protects the first-turn
// case: a resumed session whose plan file never materialized should
// leave the ledger maps empty rather than erroring.
func TestRehydratePlanStateMissingFileIsSilent(t *testing.T) {
	root := t.TempDir()
	m := snapshotModel(t, root)
	// Don't create .planning/. Call rehydrate and verify no panic,
	// no state populated.
	rehydratePlanState(m)

	if len(m.plan.ledgerItems) != 0 || len(m.plan.ledgerRenderedIDs) != 0 {
		t.Errorf("rehydrate without plan file should leave maps empty")
	}
}

// TestReplaySessionMessagesReadsUserAssistantOnly verifies that the
// replay helper ignores non-message records (tool_result, error, …)
// and blank-content messages. Drift here would leak tool-result JSON
// into the replayed history, confusing the model.
func TestReplaySessionMessagesReadsUserAssistantOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	contents := strings.Join([]string{
		`{"type":"session_start","app_name":"idiom"}`,
		`{"type":"message","role":"user","content":"hello"}`,
		`{"type":"tool_result","tool":"read_file","summary":"ok"}`,
		`{"type":"message","role":"assistant","content":"hi there"}`,
		`{"type":"message","role":"system","content":"should not leak"}`,
		`{"type":"message","role":"user","content":"   "}`,
		`{"type":"message","role":"user","content":"second question"}`,
	}, "\n")

	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	replay, err := replaySessionLog(path)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	msgs := replay.messages

	if len(msgs) != 3 {
		t.Fatalf("want 3 replayed messages (user, assistant, user), got %d: %#v", len(msgs), msgs)
	}

	if msgs[0].Role != "user" || msgs[0].Content != "hello" {
		t.Errorf("msgs[0] wrong: %#v", msgs[0])
	}

	if msgs[1].Role != "assistant" || msgs[1].Content != "hi there" {
		t.Errorf("msgs[1] wrong: %#v", msgs[1])
	}

	if msgs[2].Role != "user" || msgs[2].Content != "second question" {
		t.Errorf("msgs[2] wrong: %#v", msgs[2])
	}
}

func TestResumeSessionRestoresHistoryPanelGovernance(t *testing.T) {
	root := t.TempDir()
	logDir := filepath.Join(root, "logs", "conversations")
	writeSessionLog(t, logDir, "governed.jsonl", []string{
		`{"type":"message","role":"user","content":"show the guardrails"}`,
		`{"type":"message","role":"route_plan","content":"initial=explore followup=implement contract=implementation_result"}`,
		`{"type":"tool_request","step":1,"tool":"read_file","action":"inspect the harness state","discovery_class":"source","discovery_scope":"file","discovery_cost":"low"}`,
		`{"type":"tool_result","step":1,"tool":"read_file","summary":"read internal/surfaces/tui/app/sessions.go","output_lines":40,"output_chars":1200}`,
		`{"type":"message","role":"completion_status","content":"{\"lane\":\"implement\",\"contract\":\"implementation_result\",\"status\":\"satisfied\",\"edit_attempted\":true,\"verification_seen\":true}"}`,
		`{"type":"message","role":"assistant","content":"The loaded session includes the governance trail."}`,
	})

	m := snapshotModel(t, root)

	m.cfg = config.Config{Chat: config.Chat{DebugLogDir: logDir, DebugLogMode: "redacted"}}
	m.history.panelOpen = true
	m.history.viewport = viewport.New(96, 24)
	m.history.expanded = true
	m.panelTab = panelTabHistory

	if err := m.ResumeSession("governed"); err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}

	if len(m.messages) != 2 {
		t.Fatalf("expected resumed model messages, got %d: %#v", len(m.messages), m.messages)
	}

	if len(m.history.entries) != 2 {
		t.Fatalf("expected resumed history entries, got %d: %#v", len(m.history.entries), m.history.entries)
	}

	view := collapseWhitespace(m.history.viewport.View())
	for _, want := range []string{
		"Route: explore -> implement (implementation_result)",
		"Tools: 1 call(s) (1 discovery)",
		"Latest: read_file ok - inspect the harness state",
		"Contracts: contract satisfied",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("resumed history missing %q:\n%s", want, view)
		}
	}
}

func TestResumeSessionRestoresDirectFeaturePlannerSession(t *testing.T) {
	root := t.TempDir()
	logDir := filepath.Join(root, "logs", "conversations")
	sessionID := "feature-session"

	state := featureplanner.NewState(sessionID, "Plan greenfield memory project.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningQuestion
	state.OpenQuestions = []featureplanner.PlannerQuestion{{
		ID:                "question-new-files",
		Status:            "open",
		QuestionSummary:   "Approve greenfield scope",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "New files require operator approval.",
		RequestedDecision: "Approve creating src/memory.py and tests/test_memory.py.",
		AuthorityRequired: "operator_approval",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     []string{"src/memory.py", "tests/test_memory.py"},
		AffectedFields:    []string{"new_file_authorization"},
	}}
	if _, err := featureplanner.SaveState(root, &state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	writeSessionLog(t, logDir, "resumed.jsonl", []string{
		`{"type":"message","role":"user","content":"/feature-planner Plan greenfield memory project."}`,
		`{"type":"message","role":"idiom.feature_planner.question_open","content":"feature-planner: planning-question-open\nstate: .idiom/feature-planner/feature-session/state.json\nquestion: Approve greenfield scope"}`,
		`{"type":"message","role":"assistant","content":"Feature planner needs your approval before it can continue."}`,
	})

	m := snapshotModel(t, root)
	m.cfg = config.Config{
		Chat:      config.Chat{DebugLogDir: logDir, DebugLogMode: "redacted", ProjectRoot: root},
		Workspace: config.Workspace{Root: root, CWD: root},
	}
	m.history.viewport = viewport.New(96, 24)

	if err := m.ResumeSession("resumed"); err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}

	if m.directFeaturePlannerSessionID != sessionID {
		t.Fatalf("direct feature-planner session = %q, want %q", m.directFeaturePlannerSessionID, sessionID)
	}

	if !m.directFeaturePlannerShouldConsumeOperatorInput("Approved.") {
		t.Fatal("restored feature-planner session should consume explicit approval")
	}
	if m.directFeaturePlannerShouldConsumeOperatorInput("Can we discuss the plan first?") {
		t.Fatal("restored feature-planner session should leave ordinary questions interactive")
	}
}

func TestResumeSessionDoesNotReviveOlderFeaturePlannerWhenLatestIsClosed(t *testing.T) {
	root := t.TempDir()
	logDir := filepath.Join(root, "logs", "conversations")

	oldState := featureplanner.NewState("old-open-session", "Old plan.", "", 3, time.Unix(1, 0).UTC())
	oldState.Status = featureplanner.FeatureStatePlanningQuestion
	oldState.OpenQuestions = []featureplanner.PlannerQuestion{{
		ID:                "question-old",
		Status:            "open",
		QuestionSummary:   "Approve old scope",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "Old new files require approval.",
		RequestedDecision: "Approve creating src/old.py.",
		AuthorityRequired: "operator_approval",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     []string{"src/old.py"},
		AffectedFields:    []string{"new_file_authorization"},
	}}
	if _, err := featureplanner.SaveState(root, &oldState); err != nil {
		t.Fatalf("SaveState old: %v", err)
	}

	closedState := featureplanner.NewState("latest-complete-session", "Latest plan.", "", 3, time.Unix(2, 0).UTC())
	closedState.Status = featureplanner.FeatureStateSourceSatisfied
	if _, err := featureplanner.SaveState(root, &closedState); err != nil {
		t.Fatalf("SaveState latest: %v", err)
	}

	writeSessionLog(t, logDir, "resumed.jsonl", []string{
		`{"type":"message","role":"assistant","content":"feature-planner: planning-question-open\nstate: .idiom/feature-planner/old-open-session/state.json"}`,
		`{"type":"message","role":"assistant","content":"feature-planner: source-satisfied\nstate: .idiom/feature-planner/latest-complete-session/state.json"}`,
	})

	m := snapshotModel(t, root)
	m.cfg = config.Config{
		Chat:      config.Chat{DebugLogDir: logDir, DebugLogMode: "redacted", ProjectRoot: root},
		Workspace: config.Workspace{Root: root, CWD: root},
	}

	if err := m.ResumeSession("resumed"); err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}

	if m.directFeaturePlannerSessionID != "" {
		t.Fatalf("closed latest feature-planner state should clear active session, got %q", m.directFeaturePlannerSessionID)
	}
}

func TestResumeSessionDoesNotReviveOlderFeaturePlannerWhenLatestIsMissing(t *testing.T) {
	root := t.TempDir()
	logDir := filepath.Join(root, "logs", "conversations")

	oldState := featureplanner.NewState("old-open-session", "Old plan.", "", 3, time.Unix(1, 0).UTC())
	oldState.Status = featureplanner.FeatureStatePlanningQuestion
	oldState.OpenQuestions = []featureplanner.PlannerQuestion{{
		ID:                "question-old",
		Status:            "open",
		QuestionSummary:   "Approve old scope",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "Old new files require approval.",
		RequestedDecision: "Approve creating src/old.py.",
		AuthorityRequired: "operator_approval",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     []string{"src/old.py"},
		AffectedFields:    []string{"new_file_authorization"},
	}}
	if _, err := featureplanner.SaveState(root, &oldState); err != nil {
		t.Fatalf("SaveState old: %v", err)
	}

	writeSessionLog(t, logDir, "resumed.jsonl", []string{
		`{"type":"message","role":"assistant","content":"feature-planner: planning-question-open\nstate: .idiom/feature-planner/old-open-session/state.json"}`,
		`{"type":"message","role":"assistant","content":"feature-planner: planning-question-open\nstate: .idiom/feature-planner/missing-latest-session/state.json"}`,
	})

	m := snapshotModel(t, root)
	m.cfg = config.Config{
		Chat:      config.Chat{DebugLogDir: logDir, DebugLogMode: "redacted", ProjectRoot: root},
		Workspace: config.Workspace{Root: root, CWD: root},
	}

	if err := m.ResumeSession("resumed"); err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}

	if m.directFeaturePlannerSessionID != "" {
		t.Fatalf("missing latest feature-planner state should clear active session, got %q", m.directFeaturePlannerSessionID)
	}
}

func TestResumeSessionRestoresLatestOpenFeaturePlannerSession(t *testing.T) {
	root := t.TempDir()
	logDir := filepath.Join(root, "logs", "conversations")

	oldState := featureplanner.NewState("old-open-session", "Old plan.", "", 3, time.Unix(1, 0).UTC())
	oldState.Status = featureplanner.FeatureStatePlanningQuestion
	oldState.OpenQuestions = []featureplanner.PlannerQuestion{{
		ID:                "question-old",
		Status:            "open",
		QuestionSummary:   "Approve old scope",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "Old new files require approval.",
		RequestedDecision: "Approve creating src/old.py.",
		AuthorityRequired: "operator_approval",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     []string{"src/old.py"},
		AffectedFields:    []string{"new_file_authorization"},
	}}
	if _, err := featureplanner.SaveState(root, &oldState); err != nil {
		t.Fatalf("SaveState old: %v", err)
	}

	latestState := featureplanner.NewState("latest-open-session", "Latest plan.", "", 3, time.Unix(2, 0).UTC())
	latestState.Status = featureplanner.FeatureStatePlanningQuestion
	latestState.OpenQuestions = []featureplanner.PlannerQuestion{{
		ID:                "question-latest",
		Status:            "open",
		QuestionSummary:   "Approve latest scope",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "Latest new files require approval.",
		RequestedDecision: "Approve creating src/latest.py.",
		AuthorityRequired: "operator_approval",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     []string{"src/latest.py"},
		AffectedFields:    []string{"new_file_authorization"},
	}}
	if _, err := featureplanner.SaveState(root, &latestState); err != nil {
		t.Fatalf("SaveState latest: %v", err)
	}

	writeSessionLog(t, logDir, "resumed.jsonl", []string{
		`{"type":"message","role":"assistant","content":"feature-planner: planning-question-open\nstate: .idiom/feature-planner/old-open-session/state.json"}`,
		`{"type":"message","role":"assistant","content":"feature-planner: planning-question-open\nstate: .idiom/feature-planner/latest-open-session/state.json"}`,
	})

	m := snapshotModel(t, root)
	m.cfg = config.Config{
		Chat:      config.Chat{DebugLogDir: logDir, DebugLogMode: "redacted", ProjectRoot: root},
		Workspace: config.Workspace{Root: root, CWD: root},
	}

	if err := m.ResumeSession("resumed"); err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}

	if m.directFeaturePlannerSessionID != latestState.SessionID {
		t.Fatalf("direct feature-planner session = %q, want latest %q", m.directFeaturePlannerSessionID, latestState.SessionID)
	}
}

func TestResumeSessionIgnoresOutsideFeaturePlannerStatePath(t *testing.T) {
	root := t.TempDir()
	logDir := filepath.Join(root, "logs", "conversations")

	state := featureplanner.NewState("outside-session", "Outside plan.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningQuestion
	state.OpenQuestions = []featureplanner.PlannerQuestion{{
		ID:                "question-outside",
		Status:            "open",
		QuestionSummary:   "Approve outside scope",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "Outside new files require approval.",
		RequestedDecision: "Approve creating src/outside.py.",
		AuthorityRequired: "operator_approval",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     []string{"src/outside.py"},
		AffectedFields:    []string{"new_file_authorization"},
	}}
	if _, err := featureplanner.SaveState(root, &state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	writeSessionLog(t, logDir, "resumed.jsonl", []string{
		`{"type":"message","role":"user","content":"resume outside path"}`,
		`{"type":"message","role":"assistant","content":"feature-planner state: /tmp/outside/.idiom/feature-planner/outside-session/state.json"}`,
	})

	m := snapshotModel(t, root)
	m.cfg = config.Config{
		Chat:      config.Chat{DebugLogDir: logDir, DebugLogMode: "redacted", ProjectRoot: root},
		Workspace: config.Workspace{Root: root, CWD: root},
	}

	if err := m.ResumeSession("resumed"); err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}

	if m.directFeaturePlannerSessionID != "" {
		t.Fatalf("outside transcript path should not restore active feature-planner session, got %q", m.directFeaturePlannerSessionID)
	}
}

func TestResumeSessionRestoresSubmittedInputHistory(t *testing.T) {
	root := t.TempDir()
	logDir := filepath.Join(root, "logs", "conversations")
	writeSessionLog(t, logDir, "resumed.jsonl", []string{
		`{"type":"input_submission","content":"/plan-execute fix the up arrow history"}`,
		`{"type":"message","role":"user","content":"fix the up arrow history"}`,
		`{"type":"message","role":"assistant","content":"done"}`,
	})

	m := snapshotModel(t, root)
	m.cfg = config.Config{Chat: config.Chat{DebugLogDir: logDir, DebugLogMode: "redacted"}}
	m.events = make(chan tea.Msg, 4)
	m.input = textarea.New()

	if err := m.ResumeSession("resumed"); err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}

	want := "/plan-execute fix the up arrow history"
	if got := strings.Join(m.inputHistory, "|"); got != want {
		t.Fatalf("input history = %q, want %q", got, want)
	}

	if _, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyUp}); !handled {
		t.Fatal("up arrow should navigate restored input history")
	}

	if got := m.input.Value(); got != want {
		t.Fatalf("up arrow recalled %q, want %q", got, want)
	}
}
