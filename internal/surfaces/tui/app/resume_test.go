package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

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
var _ = ginkgo.It("rehydrate plan state recovers ledger and hits", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	writeFile(t, root, "a.go", "package a\n")
	writeFile(t, root, "b.go", "package b\n")

	m := snapshotModel(t, root)
	writer := newDetachedPromptExecutionSession(context.Background(), m)

	// Seed: ledger with a multi-path item, one path written, plan and
	// diffs log carry the anchors the rehydrator relies on.
	writer.recordTaskLedger(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "t1", Content: "rename across files", Status: "pending", Paths: []string{"a.go", "b.go"}},
		},
	}, workspace.ToolResult{Summary: "ok"})

	req := workspace.ToolRequest{Tool: "edit_file", Path: "a.go", OldText: "package a\n", NewText: "package a\n// renamed\n", Description: "rename a"}
	executeAndRecordPlanWrite(t, writer, 1, req)

	planBefore, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))

	// Simulate restart: blow away the in-memory state a fresh process
	// would have, then rehydrate.
	m.plan.ledgerItems = nil
	m.plan.ledgerRenderedIDs = nil
	m.plan.ledgerHits = nil
	m.plan.sessionHeadingWritten = false
	m.planPath = ""

	rehydratePlanState(m)

	gomega.Expect(m.plan.sessionHeadingWritten).To(gomega.BeTrue(), fmt.Sprintf("session heading flag should be restored"))

	gomega.Expect(m.plan.ledgerRenderedIDs["t1"]).To(gomega.BeTrue(), fmt.Sprintf("rendered id set should include t1"))

	{
		got := m.plan.ledgerItems["t1"]
		gomega.Expect(got.ID).To(gomega.Equal("t1"), fmt.Sprintf("ledger item not rebuilt: %#v", got))
		gomega.Expect(len(got.Paths)).To(gomega.Equal(2), fmt.Sprintf("ledger item not rebuilt: %#v", got))
	}

	gomega.Expect(m.plan.ledgerHits["t1"]["a.go"]).To(gomega.BeTrue(), fmt.Sprintf("a.go should remain marked hit after rehydrate"))

	gomega.Expect(m.plan.ledgerHits["t1"]["b.go"]).To(gomega.BeFalse(), fmt.Sprintf("b.go was not written pre-restart — should not be hit"))

	// Now finish the item: writing b.go should attach to the same line
	// (not fall to Unplanned) and flip the box.
	req2 := workspace.ToolRequest{Tool: "edit_file", Path: "b.go", OldText: "package b\n", NewText: "package b\n// renamed\n", Description: "rename b"}
	resumedWriter := newDetachedPromptExecutionSession(context.Background(), m)
	executeAndRecordPlanWrite(t, resumedWriter, 2, req2)

	planAfter, _ := os.ReadFile(filepath.Join(root, ".planning", "sessA.md"))
	gomega.Expect(strings.Contains(string(planAfter), "### Unplanned")).To(gomega.BeFalse(), fmt.Sprintf("post-rehydrate write fell to Unplanned:\nbefore=%s\nafter=%s", planBefore, planAfter))

	gomega.Expect(strings.Contains(string(planAfter), "- [x] rename across files")).To(gomega.BeTrue(), fmt.Sprintf("box should flip after final path is hit:\n%s", planAfter))
})

// TestRehydratePlanStatePreservesCheckedItems verifies that an item
// already rendered completed in the plan comes back marked completed,
// so a fresh task_ledger submission with the same id doesn't reopen it.
var _ = ginkgo.It("rehydrate plan state preserves checked items", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	writeFile(t, root, "a.go", "package a\n")

	m := snapshotModel(t, root)
	writer := newDetachedPromptExecutionSession(context.Background(), m)

	writer.recordTaskLedger(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "t1", Content: "touch a", Status: "pending", Paths: []string{"a.go"}},
		},
	}, workspace.ToolResult{Summary: "ok"})

	req := workspace.ToolRequest{Tool: "edit_file", Path: "a.go", OldText: "package a\n", NewText: "package a\n// v2\n", Description: "touch"}
	executeAndRecordPlanWrite(t, writer, 1, req)

	// Wipe and rehydrate.
	m.plan.ledgerItems = nil
	m.plan.ledgerRenderedIDs = nil
	m.plan.ledgerHits = nil
	rehydratePlanState(m)

	item, ok := m.plan.ledgerItems["t1"]
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("t1 missing from rehydrated ledger"))

	gomega.Expect(strings.ToLower(string(item.Status))).To(gomega.Equal("completed"), fmt.Sprintf("checked item should rehydrate with status=completed, got %q", item.Status))
})

// TestRehydratePlanStateMissingFileIsSilent protects the first-turn
// case: a resumed session whose plan file never materialized should
// leave the ledger maps empty rather than erroring.
var _ = ginkgo.It("rehydrate plan state missing file is silent", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	m := snapshotModel(t, root)
	// Don't create .planning/. Call rehydrate and verify no panic,
	// no state populated.
	rehydratePlanState(m)

	gomega.Expect(len(m.plan.ledgerItems)).To(gomega.Equal(0), fmt.Sprintf("rehydrate without plan file should leave maps empty"))
	gomega.Expect(len(m.plan.ledgerRenderedIDs)).To(gomega.Equal(0), fmt.Sprintf("rehydrate without plan file should leave maps empty"))
})

// TestReplaySessionMessagesReadsUserAssistantOnly verifies that the
// replay helper ignores non-message records (tool_result, error, …)
// and blank-content messages. Drift here would leak tool-result JSON
// into the replayed history, confusing the model.
var _ = ginkgo.It("replay session messages reads user assistant only", func() {
	t := ginkgo.GinkgoT()
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

	{
		err := os.WriteFile(path, []byte(contents), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write fixture: %v", err))
	}

	replay, err := replaySessionLog(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("replay: %v", err))
	msgs := replay.messages

	gomega.Expect(len(msgs)).To(gomega.Equal(3), fmt.Sprintf("want 3 replayed messages (user, assistant, user), got %d: %#v", len(msgs), msgs))

	gomega.Expect(msgs[0].Role).To(gomega.Equal("user"), fmt.Sprintf("msgs[0] wrong: %#v", msgs[0]))
	gomega.Expect(msgs[0].Content).To(gomega.Equal("hello"), fmt.Sprintf("msgs[0] wrong: %#v", msgs[0]))

	gomega.Expect(msgs[1].Role).To(gomega.Equal("assistant"), fmt.Sprintf("msgs[1] wrong: %#v", msgs[1]))
	gomega.Expect(msgs[1].Content).To(gomega.Equal("hi there"), fmt.Sprintf("msgs[1] wrong: %#v", msgs[1]))

	gomega.Expect(msgs[2].Role).To(gomega.Equal("user"), fmt.Sprintf("msgs[2] wrong: %#v", msgs[2]))
	gomega.Expect(msgs[2].Content).To(gomega.Equal("second question"), fmt.Sprintf("msgs[2] wrong: %#v", msgs[2]))
})

var _ = ginkgo.It("resume session restores history panel governance", ginkgo.Label("development-scenario", "integration", "contract"), func() {
	t := ginkgo.GinkgoT()
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

	{
		err := m.ResumeSession("governed")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ResumeSession: %v", err))
	}

	gomega.Expect(len(m.messages)).To(gomega.Equal(2), fmt.Sprintf("expected resumed model messages, got %d: %#v", len(m.messages), m.messages))

	gomega.Expect(len(m.history.entries)).To(gomega.Equal(2), fmt.Sprintf("expected resumed history entries, got %d: %#v", len(m.history.entries), m.history.entries))

	view := collapseWhitespace(m.history.viewport.View())
	for _, want := range []string{
		"Route: explore -> implement (implementation_result)",
		"Tools: 1 call(s) (1 discovery)",
		"Latest: read_file ok - inspect the harness state",
		"Contracts: contract satisfied",
	} {
		gomega.Expect(strings.Contains(view, want)).To(gomega.BeTrue(), fmt.Sprintf("resumed history missing %q:\n%s", want, view))
	}
})

var _ = ginkgo.It("resume session restores direct feature planner session", ginkgo.Label("development-scenario", "integration", "contract"), func() {
	t := ginkgo.GinkgoT()
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
	{
		_, err := featureplanner.SaveState(root, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveState: %v", err))
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

	{
		err := m.ResumeSession("resumed")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ResumeSession: %v", err))
	}

	gomega.Expect(m.directFeaturePlannerSessionID).To(gomega.Equal(sessionID), fmt.Sprintf("direct feature-planner session = %q, want %q", m.directFeaturePlannerSessionID, sessionID))

	gomega.Expect(directFeaturePlannerShouldConsumeOperatorInput(resolveProjectRoot(m.cfg.Chat.ProjectRoot, m.workspace), m.directFeaturePlannerSessionID, "Approved.")).To(gomega.BeTrue(), fmt.Sprint("restored feature-planner session should consume explicit approval"))
	gomega.Expect(directFeaturePlannerShouldConsumeOperatorInput(resolveProjectRoot(m.cfg.Chat.ProjectRoot, m.workspace), m.directFeaturePlannerSessionID, "Can we discuss the plan first?")).To(gomega.BeFalse(), fmt.Sprint("restored feature-planner session should leave ordinary questions interactive"))
})

var _ = ginkgo.It("resume session does not revive older feature planner when latest is closed", func() {
	t := ginkgo.GinkgoT()
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
	{
		_, err := featureplanner.SaveState(root, &oldState)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveState old: %v", err))
	}

	closedState := featureplanner.NewState("latest-complete-session", "Latest plan.", "", 3, time.Unix(2, 0).UTC())
	closedState.Status = featureplanner.FeatureStateSourceSatisfied
	{
		_, err := featureplanner.SaveState(root, &closedState)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveState latest: %v", err))
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

	{
		err := m.ResumeSession("resumed")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ResumeSession: %v", err))
	}

	gomega.Expect(m.directFeaturePlannerSessionID).To(gomega.Equal(""), fmt.Sprintf("closed latest feature-planner state should clear active session, got %q", m.directFeaturePlannerSessionID))
})

var _ = ginkgo.It("resume session does not revive older feature planner when latest is missing", func() {
	t := ginkgo.GinkgoT()
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
	{
		_, err := featureplanner.SaveState(root, &oldState)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveState old: %v", err))
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

	{
		err := m.ResumeSession("resumed")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ResumeSession: %v", err))
	}

	gomega.Expect(m.directFeaturePlannerSessionID).To(gomega.Equal(""), fmt.Sprintf("missing latest feature-planner state should clear active session, got %q", m.directFeaturePlannerSessionID))
})

var _ = ginkgo.It("resume session restores latest open feature planner session", func() {
	t := ginkgo.GinkgoT()
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
	{
		_, err := featureplanner.SaveState(root, &oldState)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveState old: %v", err))
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
	{
		_, err := featureplanner.SaveState(root, &latestState)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveState latest: %v", err))
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

	{
		err := m.ResumeSession("resumed")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ResumeSession: %v", err))
	}

	gomega.Expect(m.directFeaturePlannerSessionID).To(gomega.Equal(latestState.SessionID), fmt.Sprintf("direct feature-planner session = %q, want latest %q", m.directFeaturePlannerSessionID, latestState.SessionID))
})

var _ = ginkgo.It("resume session ignores outside feature planner state path", func() {
	t := ginkgo.GinkgoT()
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
	{
		_, err := featureplanner.SaveState(root, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveState: %v", err))
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

	{
		err := m.ResumeSession("resumed")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ResumeSession: %v", err))
	}

	gomega.Expect(m.directFeaturePlannerSessionID).To(gomega.Equal(""), fmt.Sprintf("outside transcript path should not restore active feature-planner session, got %q", m.directFeaturePlannerSessionID))
})

var _ = ginkgo.It("resume session restores submitted input history", func() {
	t := ginkgo.GinkgoT()
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

	{
		err := m.ResumeSession("resumed")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ResumeSession: %v", err))
	}

	want := "/plan-execute fix the up arrow history"
	{
		got := strings.Join(m.inputHistory, "|")
		gomega.Expect(got).To(gomega.Equal(want), fmt.Sprintf("input history = %q, want %q", got, want))
	}

	{
		_, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyUp})
		gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("up arrow should navigate restored input history"))
	}

	{
		got := m.input.Value()
		gomega.Expect(got).To(gomega.Equal(want), fmt.Sprintf("up arrow recalled %q, want %q", got, want))
	}
})
