package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/sessionlog"
	"idiom/internal/workflows/featureplanner"
)

func writeSessionLog(t ginkgo.FullGinkgoTInterface, dir string, name string, lines []string) string {
	ginkgo.GinkgoHelper()

	{
		err := os.MkdirAll(dir, 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir: %v", err))
	}

	path := filepath.Join(dir, name)
	body := strings.Join(lines, "\n")

	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}

	{
		err := os.WriteFile(path, []byte(body), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write session log: %v", err))
	}

	return path
}

var _ = ginkgo.It("resolve session log path from id", func() {
	cfg := config.Config{Chat: config.Chat{DebugLogDir: "/tmp/conv"}}
	got := sessionlog.ResolveSessionLogPath(cfg, "20260418-055543.504")
	want := filepath.Join("/tmp/conv", "20260418-055543.504.jsonl")

	gomega.Expect(got).To(gomega.Equal(want), fmt.Sprintf("ResolveSessionLogPath id-form = %q, want %q", got, want))
})

var _ = ginkgo.It("resolve session log path from path", func() {
	cfg := config.Config{Chat: config.Chat{DebugLogDir: "/tmp/conv"}}
	{
		got := sessionlog.ResolveSessionLogPath(cfg, "logs/conversations/foo.jsonl")
		gomega.Expect(got).To(gomega.Equal("logs/conversations/foo.jsonl"), fmt.Sprintf("path-form unchanged but got %q", got))
	}

	{
		got := sessionlog.ResolveSessionLogPath(cfg, "/abs/foo")
		gomega.Expect(got).To(gomega.Equal("/abs/foo.jsonl"), fmt.Sprintf("abs-form should append .jsonl: %q", got))
	}
})

var _ = ginkgo.It("list recorded sessions summarizes newest first", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	cfg := config.Config{Chat: config.Chat{DebugLogDir: dir}}
	writeSessionLog(t, dir, "20260418-055543.504.jsonl", []string{
		`{"type":"message","role":"user","content":"hello world\nsecond line"}`,
		`{"type":"message","role":"assistant","content":"hi"}`,
		`{"type":"tool_request","tool":"read_file"}`,
	})
	writeSessionLog(t, dir, "20260419-100000.000.jsonl", []string{
		`{"type":"message","role":"user","content":"second session prompt"}`,
	})
	writeSessionLog(t, dir, "20260420-000000.000.jsonl", []string{
		`{"type":"session_start","app_name":"idiom-test"}`,
		`{"type":"server","running":true}`,
		`{"type":"message","role":"ui_trace","content":"ui.refresh_sessions duration_ms=55"}`,
	})
	writeSessionLog(t, dir, "skipme.txt", []string{"not a session"})

	got, err := sessionlog.ListRecordedSessions(cfg)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ListRecordedSessions: %v", err))

	gomega.Expect(len(got)).To(gomega.Equal(2), fmt.Sprintf("expected 2 sessions, got %d (%v)", len(got), got))

	gomega.Expect(got[0].ID).To(gomega.Equal("20260419-100000.000"), fmt.Sprintf("expected newest first, got %q", got[0].ID))

	older := got[1]
	gomega.Expect(older.UserMessages).To(gomega.Equal(1), fmt.Sprintf("older session counts = %d/%d, want 1/1", older.UserMessages, older.AssistMessage))
	gomega.Expect(older.AssistMessage).To(gomega.Equal(1), fmt.Sprintf("older session counts = %d/%d, want 1/1", older.UserMessages, older.AssistMessage))

	gomega.Expect(older.FirstPrompt).To(gomega.Equal("hello world"), fmt.Sprintf("older first prompt = %q, want %q", older.FirstPrompt, "hello world"))
})

var _ = ginkgo.It("list recorded sessions caps summarized history", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	cfg := config.Config{Chat: config.Chat{DebugLogDir: dir}}

	for i := 0; i < sessionlog.MaxRecordedSessionsScanned+1; i++ {
		writeSessionLog(t, dir, fmt.Sprintf("20260419-100000.%03d.jsonl", i), []string{
			`{"type":"message","role":"user","content":"prompt"}`,
		})
	}

	got, err := sessionlog.ListRecordedSessions(cfg)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ListRecordedSessions: %v", err))

	gomega.Expect(len(got)).To(gomega.Equal(sessionlog.MaxRecordedSessionsScanned), fmt.Sprintf("expected capped session count %d, got %d", sessionlog.MaxRecordedSessionsScanned, len(got)))
})

var _ = ginkgo.It("list recorded sessions missing dir", func() {
	t := ginkgo.GinkgoT()
	cfg := config.Config{Chat: config.Chat{DebugLogDir: filepath.Join(t.TempDir(), "does-not-exist")}}

	got, err := sessionlog.ListRecordedSessions(cfg)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("missing dir should not error: %v", err))

	gomega.Expect(len(got)).To(gomega.Equal(0), fmt.Sprintf("expected zero sessions for missing dir, got %d", len(got)))
})

var _ = ginkgo.It("load history from session replays user assistant", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	path := writeSessionLog(t, dir, "session.jsonl", []string{
		`{"type":"message","role":"user","content":"first prompt"}`,
		`{"type":"message","role":"assistant","content":"first reply"}`,
		`{"type":"message","role":"system","content":"ignored"}`,
		`{"type":"tool_request","tool":"read_file"}`,
		`{"type":"message","role":"user","content":"second prompt"}`,
	})

	model := &Model{}
	{
		err := model.LoadHistoryFromSession(path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("LoadHistoryFromSession: %v", err))
	}

	gomega.Expect(len(model.messages)).To(gomega.Equal(3), fmt.Sprintf("expected 3 messages, got %d (%v)", len(model.messages), model.messages))

	want := []llama.ChatMessage{
		{Role: "user", Content: "first prompt"},
		{Role: "assistant", Content: "first reply"},
		{Role: "user", Content: "second prompt"},
	}
	for i, w := range want {
		got := model.messages[i]
		gomega.Expect(got.Role).To(gomega.Equal(w.Role), fmt.Sprintf("messages[%d] = %#v, want %#v", i, got, w))
		gomega.Expect(got.Content).To(gomega.Equal(w.Content), fmt.Sprintf("messages[%d] = %#v, want %#v", i, got, w))
	}
})

var _ = ginkgo.It("load history from session restores governance history", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	path := writeSessionLog(t, dir, "governed.jsonl", []string{
		`{"type":"session_start","app_name":"idiom"}`,
		`{"type":"message","role":"user","content":"inspect the control path"}`,
		`{"type":"message","role":"route_plan","content":"initial=explore followup=implement contract=implementation_result"}`,
		`{"type":"tool_request","step":1,"tool":"context_pack","action":"rank repository evidence","discovery_class":"context","discovery_scope":"repo","discovery_cost":"low"}`,
		`{"type":"tool_result","step":1,"tool":"context_pack","summary":"ranked 8 files","output_lines":12,"output_chars":900,"context_cache_state":"miss_recompute","context_cache_source":"recompute","context_cache_reason":"cache_miss","context_cache_pack_id":"pack-123","context_auto_refresh_attempted":true,"context_auto_refresh_files_indexed":12,"context_auto_refresh_reason":"stale_unbounded_graph"}`,
		`{"type":"run_event","role":"autonomy_event","content":"{\"type\":\"contract_compiled\",\"contract_id\":\"contract_123\",\"correlation\":{\"lane\":\"implement\",\"phase\":\"execute\",\"runtime_contract\":\"implementation_result\"}}"}`,
		`{"type":"message","role":"assistant","content":"The control path is inspectable."}`,
	})

	model := newHistoryPanelModel()

	model.history.viewport = viewport.New(96, 24)
	model.history.expanded = true

	{
		err := model.LoadHistoryFromSession(path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("LoadHistoryFromSession: %v", err))
	}

	gomega.Expect(len(model.messages)).To(gomega.Equal(2), fmt.Sprintf("expected 2 model messages, got %d (%#v)", len(model.messages), model.messages))

	gomega.Expect(len(model.history.entries)).To(gomega.Equal(2), fmt.Sprintf("expected user and agent history entries, got %d: %#v", len(model.history.entries), model.history.entries))

	agentEntry := model.history.entries[1]
	gomega.Expect(agentEntry.inProgress).To(gomega.BeFalse(), fmt.Sprint("replayed agent history should be finalized"))

	gomega.Expect(len(agentEntry.toolCalls)).To(gomega.Equal(1), fmt.Sprintf("expected replayed tool call, got %#v", agentEntry.toolCalls))

	gomega.Expect(len(agentEntry.auditEvents) < 3).To(gomega.BeFalse(), fmt.Sprintf("expected route/cache/contract audit events, got %#v", agentEntry.auditEvents))

	view := collapseWhitespace(model.history.viewport.View())
	for _, want := range []string{
		"Route: explore -> implement (implementation_result)",
		"Tools: 1 call(s) (1 discovery)",
		"Latest: context_pack ok - rank repository evidence",
		"Contracts: contract compiled",
		"Signals: cache miss_recompute, cache refreshed",
	} {
		gomega.Expect(strings.Contains(view, want)).To(gomega.BeTrue(), fmt.Sprintf("history viewport missing %q:\n%s", want, view))
	}
})

var _ = ginkgo.It("load history from session does not overwrite active feature planner session", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	logDir := filepath.Join(root, "logs", "conversations")

	active := featureplanner.NewState("active-session", "Current plan.", "", 3, time.Unix(1, 0).UTC())
	active.Status = featureplanner.FeatureStatePlanningQuestion
	active.OpenQuestions = []featureplanner.PlannerQuestion{{
		ID:                "question-active",
		Status:            "open",
		QuestionSummary:   "Approve current scope",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "Current new files require approval.",
		RequestedDecision: "Approve creating src/current.py.",
		AuthorityRequired: "operator_approval",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     []string{"src/current.py"},
		AffectedFields:    []string{"new_file_authorization"},
	}}
	{
		_, err := featureplanner.SaveState(root, &active)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveState active: %v", err))
	}

	old := featureplanner.NewState("old-session", "Old plan.", "", 3, time.Unix(2, 0).UTC())
	old.Status = featureplanner.FeatureStatePlanningQuestion
	old.OpenQuestions = []featureplanner.PlannerQuestion{{
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
		_, err := featureplanner.SaveState(root, &old)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveState old: %v", err))
	}

	path := writeSessionLog(t, logDir, "old.jsonl", []string{
		`{"type":"message","role":"user","content":"/feature-planner Old plan."}`,
		`{"type":"message","role":"assistant","content":"feature-planner: planning-question-open\nstate: .idiom/feature-planner/old-session/state.json"}`,
	})

	model := snapshotModel(t, root)
	model.cfg = config.Config{
		Chat:      config.Chat{ProjectRoot: root},
		Workspace: config.Workspace{Root: root, CWD: root},
	}
	model.directFeaturePlannerSessionID = active.SessionID

	{
		err := model.LoadHistoryFromSession(path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("LoadHistoryFromSession: %v", err))
	}

	gomega.Expect(model.directFeaturePlannerSessionID).To(gomega.Equal(active.SessionID), fmt.Sprintf("LoadHistoryFromSession overwrote active planner with %q", model.directFeaturePlannerSessionID))
})

var _ = ginkgo.It("load history from session restores direct feature planner when none active", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	logDir := filepath.Join(root, "logs", "conversations")

	state := featureplanner.NewState("history-session", "History plan.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningQuestion
	state.OpenQuestions = []featureplanner.PlannerQuestion{{
		ID:                "question-history",
		Status:            "open",
		QuestionSummary:   "Approve history scope",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "New files require operator approval.",
		RequestedDecision: "Approve creating src/history.py.",
		AuthorityRequired: "operator_approval",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     []string{"src/history.py"},
		AffectedFields:    []string{"new_file_authorization"},
	}}
	{
		_, err := featureplanner.SaveState(root, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveState: %v", err))
	}

	path := writeSessionLog(t, logDir, "history.jsonl", []string{
		`{"type":"message","role":"user","content":"/feature-planner History plan."}`,
		`{"type":"message","role":"assistant","content":"feature-planner: planning-question-open\nstate: .idiom/feature-planner/history-session/state.json"}`,
	})

	model := snapshotModel(t, root)
	model.cfg = config.Config{
		Chat:      config.Chat{ProjectRoot: root},
		Workspace: config.Workspace{Root: root, CWD: root},
	}

	{
		err := model.LoadHistoryFromSession(path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("LoadHistoryFromSession: %v", err))
	}

	gomega.Expect(model.directFeaturePlannerSessionID).To(gomega.Equal(state.SessionID), fmt.Sprintf("direct feature-planner session = %q, want %q", model.directFeaturePlannerSessionID, state.SessionID))
})

var _ = ginkgo.It("load history from session rejects empty log", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	path := writeSessionLog(t, dir, "empty.jsonl", []string{
		`{"type":"tool_request","tool":"read_file"}`,
	})
	model := &Model{}

	{
		err := model.LoadHistoryFromSession(path)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected error for log with no user/assistant messages"))
	}
})
