package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	featureplannercontract "idiom/internal/contracts/featureplanner"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/config"
	"idiom/internal/platform/textutil"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/llama/llamamocks"
	"idiom/internal/workflows/featureplanner"
	"idiom/internal/workflows/featureplannerproduct"
	"idiom/internal/workflows/planexecute"
	"idiom/internal/workflows/productworkflow"
)

var _ = ginkgo.It("feature planner tui direct command uses product context from mentioned effort root", func() {
	t := ginkgo.GinkgoT()
	projectRoot := t.TempDir()
	effortRel := filepath.Join("docs", "product-workflows", "efforts", "started", "native-quality")
	effortRoot := filepath.Join(projectRoot, effortRel)
	writeNativeQualityEffort(t, effortRoot)

	m := newDetachedPromptExecutionSession(context.Background(), &Model{
		client: (&scriptedLLMRecorder{responses: []string{featurePlannerCommandBadGoPlanJSON(t)}}).Mock(t),
		cfg: config.Config{
			Chat:      config.Chat{ProjectRoot: projectRoot},
			Workspace: config.Workspace{Root: projectRoot, CWD: projectRoot},
		},
		workspace: workspace.NewManager(config.Workspace{Root: projectRoot, CWD: projectRoot}),
	})

	answer, err := m.runFeaturePlannerCommandOnce(context.Background(), "Continue existing effort at "+filepath.ToSlash(effortRel)+".")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runFeaturePlannerCommandOnce: %v", err))

	gomega.Expect(strings.Contains(answer, "feature-planner: "+featureplanner.FeatureStatePlanningRepair)).To(gomega.BeTrue(), fmt.Sprintf("expected planning-repair answer, got:\n%s", answer))
	gomega.Expect(strings.Contains(answer, "diagnostics: 1 internal planner diagnostic(s) saved in feature-planner state")).To(gomega.BeTrue(), fmt.Sprintf("expected sanitized diagnostic count, got:\n%s", answer))
	for _, forbidden := range []string{"internal/qcb/binding.go", "TDD-1", "blockers:"} {
		gomega.Expect(strings.Contains(answer, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("answer leaked raw repair diagnostic %q:\n%s", forbidden, answer))
	}

	gomega.Expect(m.lastPlanExecuteRunResult).To(gomega.BeNil(), fmt.Sprintf("plan-execute should not be dispatched after product context rejection"))
})

var _ = ginkgo.It("overlay feature planner plan execute run result preserves exit code", func() {
	t := ginkgo.GinkgoT()
	summary := overlayFeaturePlannerPlanExecuteRunResult(featureplanner.PlanExecuteCompletionSummary{}, "", &planexecute.RunResult{
		ExitCode:      7,
		FailureReason: "plan-execute failed",
	})
	fields := summary.Fields()

	{
		got := fields[plancontract.PlanExecuteTerminalFieldExitCode]
		gomega.Expect(got).To(gomega.Equal(7), fmt.Sprintf("exit_code = %#v, want 7; fields=%+v", got, fields))
	}

	{
		got := featureplanner.CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldStatus)
		gomega.Expect(got).To(gomega.Equal("failed"), fmt.Sprintf("status = %q, want failed; fields=%+v", got, fields))
	}

	{
		got := plancontract.DecidePlanExecuteTerminalCompletionFields(fields, nil).Class
		gomega.Expect(got).To(gomega.Equal(plancontract.PlanExecuteTerminalDecisionBlocked), fmt.Sprintf("terminal class = %q, want blocked; fields=%+v", got, fields))
	}

	root := t.TempDir()
	progress := planexecute.NewProgressState("pe-session", "prompt", "", []string{"pkg/feature.go"}, nil, time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC))
	progress.Phase = plancontract.PlanExecuteProgressPhasePlanning
	progress.Status = plancontract.PlanExecuteProgressStatusRunning
	statePath, err := planexecute.SaveProgressState(root, &progress)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveProgressState: %v", err))

	summary = overlayFeaturePlannerPlanExecuteRunResult(featureplanner.PlanExecuteCompletionSummary{}, root, &planexecute.RunResult{
		ExitCode:        7,
		SessionID:       "pe-session",
		ResumeStatePath: statePath,
		FailureReason:   "plan-execute failed",
	})
	fields = summary.Fields()

	{
		got := fields[plancontract.PlanExecuteTerminalFieldExitCode]
		gomega.Expect(got).To(gomega.Equal(7), fmt.Sprintf("durable progress exit_code = %#v, want 7; fields=%+v", got, fields))
	}

	{
		got := plancontract.DecidePlanExecuteTerminalCompletionFields(fields, nil).Class
		gomega.Expect(got).To(gomega.Equal(plancontract.PlanExecuteTerminalDecisionBlocked), fmt.Sprintf("durable progress terminal class = %q, want blocked; fields=%+v", got, fields))
	}
})

var _ = ginkgo.It("feature planner tui direct plan validates before product normalization", func() {
	t := ginkgo.GinkgoT()
	projectRoot := t.TempDir()
	effortRel := filepath.Join("docs", "product-workflows", "efforts", "started", "native-quality")
	effortRoot := filepath.Join(projectRoot, effortRel)
	writeNativeQualityEffort(t, effortRoot)

	m := newDetachedPromptExecutionSession(context.Background(), &Model{
		client: (&scriptedLLMRecorder{responses: []string{featurePlannerCommandDemotedFilesPlanJSON(t)}}).Mock(t),
		cfg: config.Config{
			Chat:      config.Chat{ProjectRoot: projectRoot},
			Workspace: config.Workspace{Root: projectRoot, CWD: projectRoot},
		},
		workspace: workspace.NewManager(config.Workspace{Root: projectRoot, CWD: projectRoot}),
	})

	answer, err := m.runFeaturePlannerCommandOnce(context.Background(), "Continue existing effort at "+filepath.ToSlash(effortRel)+".")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runFeaturePlannerCommandOnce: %v", err))

	for _, want := range []string{
		"feature-planner: " + featureplanner.FeatureStatePlanningRepair,
		"diagnostics: 1 internal planner diagnostic(s) saved in feature-planner state",
	} {
		gomega.Expect(strings.Contains(answer, want)).To(gomega.BeTrue(), fmt.Sprintf("expected answer to contain %q, got:\n%s", want, answer))
	}
	for _, forbidden := range []string{"native/tests/test_quality_inventory_bindings.cpp", "not omitted or demoted to allowed_edit_paths", "blockers:"} {
		gomega.Expect(strings.Contains(answer, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("answer leaked raw repair diagnostic %q:\n%s", forbidden, answer))
	}

	gomega.Expect(m.lastPlanExecuteRunResult).To(gomega.BeNil(), fmt.Sprintf("plan-execute should not be dispatched after raw product scope rejection"))
})

var _ = ginkgo.It("feature planner tui direct command retries model repairable standalone plan", func() {
	t := ginkgo.GinkgoT()
	projectRoot := t.TempDir()
	writeFile(t, projectRoot, "llm_brain/query_handler.py", "class QueryHandler:\n    pass\n")
	writeFile(t, projectRoot, "tests/test_query_handler.py", "def test_placeholder():\n    assert True\n")
	writeFile(t, projectRoot, "pyproject.toml", "[tool.pytest.ini_options]\ntestpaths = [\"tests\"]\n")

	rejectedPlan := featurePlannerCommandRunningExampleExistingPathPlan()
	secondResponse := `{"tool":"ask_planner_question","question_summary":"Confirm LLM brain MVP scope","blocking_claim":"The repaired standalone plan needs an explicit operator approval boundary before implementation.","requested_decision":"Confirm the minimal memory behavior and verification command for the LLM brain MVP.","question_kind":"external_answer_required","authority_required":"operator_approval","required_before":"plan_approval","affected_paths":["llm_brain/query_handler.py","tests/test_query_handler.py"],"affected_fields":["requirements_scope","verification_command"]}`

	client := &scriptedLLMRecorder{responses: []string{
		featurePlannerCommandPlanJSON(t, rejectedPlan),
		secondResponse,
	}}
	m := newDetachedPromptExecutionSession(context.Background(), &Model{
		client: client.Mock(t),
		cfg: config.Config{
			Chat:      config.Chat{ProjectRoot: projectRoot},
			Workspace: config.Workspace{Root: projectRoot, CWD: projectRoot},
		},
		workspace: workspace.NewManager(config.Workspace{Root: projectRoot, CWD: projectRoot}),
	})

	answer, err := m.runFeaturePlannerCommandOnce(context.Background(), `Can you work out an MVP to an effective "llm brain" sort of project to manage long-term agentic memory?`)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runFeaturePlannerCommandOnce: %v", err))

	gomega.Expect(client.callCount).To(gomega.Equal(2), fmt.Sprintf("planner should retry after model-repairable standalone rejection, calls=%d", client.callCount))
	gomega.Expect(strings.Contains(answer, "feature-planner: "+featureplanner.FeatureStatePlanningRepair)).To(gomega.BeFalse(), fmt.Sprintf("model-repairable standalone rejection should not surface terminal planning-repair:\n%s", answer))
	gomega.Expect(strings.Contains(answer, "Feature planner needs your input before it can continue.")).To(gomega.BeTrue(), fmt.Sprintf("expected repaired follow-up question, got:\n%s", answer))

	gomega.Expect(m.directFeaturePlannerSessionID).NotTo(gomega.Equal(""), fmt.Sprint("direct feature-planner session should be remembered"))
	statePath := filepath.Join(projectRoot, ".idiom", "feature-planner", m.directFeaturePlannerSessionID, "state.json")
	state := loadFeaturePlannerCommandStateFile(t, statePath)
	gomega.Expect(state.Status).To(gomega.Equal(featureplanner.FeatureStatePlanningQuestion), fmt.Sprintf("state should preserve rejected candidate and open repaired question, got status=%q rejected=%d questions=%+v", state.Status, len(state.RejectedPlanCandidates), state.OpenQuestions))
	gomega.Expect(len(state.RejectedPlanCandidates)).To(gomega.Equal(1), fmt.Sprintf("state should preserve rejected candidate and open repaired question, got status=%q rejected=%d questions=%+v", state.Status, len(state.RejectedPlanCandidates), state.OpenQuestions))
})

var _ = ginkgo.It("feature planner tui direct answer resumes open question session", func() {
	t := ginkgo.GinkgoT()
	projectRoot := t.TempDir()
	writeFile(t, projectRoot, "go.mod", "module example.com/tui\n")
	plan := featurePlannerCommandStandaloneCollapsePlan()
	plan.Atoms[0].SDDRefs = nil

	client := &scriptedLLMRecorder{responses: []string{
		featurePlannerCommandNewFileQuestionJSON(t),
		featurePlannerCommandPlanJSON(t, plan),
		featurePlannerCommandPlanJSON(t, plan),
	}}
	m := newDetachedPromptExecutionSession(context.Background(), &Model{
		client: client.Mock(t),
		cfg: config.Config{
			Chat:      config.Chat{ProjectRoot: projectRoot},
			Workspace: config.Workspace{Root: projectRoot, CWD: projectRoot},
		},
		workspace: workspace.NewManager(config.Workspace{Root: projectRoot, CWD: projectRoot}),
	})

	answer, err := m.runFeaturePlannerCommandOnce(context.Background(), "Implement CollapseEvent helper.")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runFeaturePlannerCommandOnce: %v", err))
	gomega.Expect(answer).To(gomega.ContainSubstring("Feature planner needs your approval before it can continue."), fmt.Sprintf("expected planning question answer, got:\n%s", answer))
	gomega.Expect(answer).To(gomega.ContainSubstring("Proposed paths:"), fmt.Sprintf("expected planning question answer, got:\n%s", answer))

	sessionID := m.directFeaturePlannerSessionID
	gomega.Expect(sessionID).NotTo(gomega.Equal(""), fmt.Sprint("direct feature-planner session should be remembered after open question"))
	gomega.Expect(directFeaturePlannerShouldConsumeOperatorInput(resolveProjectRoot(m.cfg.Chat.ProjectRoot, m.workspace), m.directFeaturePlannerSessionID, "Approved.")).To(gomega.BeTrue(), fmt.Sprint("direct feature-planner session should consume an approval answer"))

	answer, err = m.runDirectFeaturePlannerAnswerOnce(context.Background(), "Approved.")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runDirectFeaturePlannerAnswerOnce: %v", err))
	gomega.Expect(len(client.messages)).To(gomega.BeNumerically(">=", 2), fmt.Sprintf("resumed planner prompt lost original objective: %#v", client.messages))
	gomega.Expect(client.messages[1][len(client.messages[1])-1].Content).To(gomega.ContainSubstring("User request:\nImplement CollapseEvent helper."), fmt.Sprintf("resumed planner prompt lost original objective: %#v", client.messages))
	gomega.Expect(strings.Contains(client.messages[1][len(client.messages[1])-1].Content, "User request:\nApproved.")).To(gomega.BeFalse(), fmt.Sprintf("resumed planner prompt used approval answer as objective:\n%s", client.messages[1][len(client.messages[1])-1].Content))
	gomega.Expect(answer).To(gomega.ContainSubstring("feature-planner: "+featureplanner.FeatureStatePlanningStuck), fmt.Sprintf("expected resumed planning-stuck answer with sanitized diagnostics, got:\n%s", answer))
	gomega.Expect(answer).To(gomega.ContainSubstring("diagnostics: 2 internal planner diagnostic(s) saved in feature-planner state"), fmt.Sprintf("expected resumed planning-stuck answer with sanitized diagnostics, got:\n%s", answer))
	gomega.Expect(answer).To(gomega.Not(gomega.ContainSubstring("behavioral_coverage_draft")), fmt.Sprintf("resumed planning-repair answer leaked raw diagnostics:\n%s", answer))
	gomega.Expect(answer).To(gomega.Not(gomega.ContainSubstring("blockers:")), fmt.Sprintf("resumed planning-repair answer leaked raw diagnostics:\n%s", answer))

	gomega.Expect(m.directFeaturePlannerSessionID).To(gomega.Equal(""), fmt.Sprintf("terminal planning-stuck state should clear direct session, got %q", m.directFeaturePlannerSessionID))

	state := loadFeaturePlannerCommandStateFile(t, filepath.Join(projectRoot, ".idiom", "feature-planner", sessionID, "state.json"))
	gomega.Expect(len(state.OpenQuestions)).To(gomega.Equal(1), fmt.Sprintf("question was not answered in durable state: %+v", state.OpenQuestions))
	gomega.Expect(state.OpenQuestions[0].Status).To(gomega.Equal("answered"), fmt.Sprintf("question was not answered in durable state: %+v", state.OpenQuestions))
	gomega.Expect(len(state.AuthorityDeltas)).To(gomega.Equal(1), fmt.Sprintf("typed authority delta was not preserved: %+v", state.AuthorityDeltas))
	gomega.Expect(slices.Equal(state.AuthorityDeltas[0].AffectedPaths, []string{"pkg/collapse_event.go", "pkg/collapse_event_test.go"})).To(gomega.BeTrue(), fmt.Sprintf("typed authority delta was not preserved: %+v", state.AuthorityDeltas))
	gomega.Expect(len(state.RejectedPlanCandidates)).To(gomega.Equal(2), fmt.Sprintf("rejected candidate evidence missing: %+v", state.RejectedPlanCandidates))
	gomega.Expect(state.RejectedPlanCandidates[len(state.RejectedPlanCandidates)-1].Diagnostic).To(gomega.ContainSubstring("behavioral_coverage_draft"), fmt.Sprintf("rejected candidate evidence missing: %+v", state.RejectedPlanCandidates))
	gomega.Expect(strings.TrimSpace(state.PlanPath)).NotTo(gomega.Equal(""), fmt.Sprint("rejected feature plan candidate should be written to the Plan tab"))
	planTabBody, err := os.ReadFile(filepath.Join(projectRoot, filepath.FromSlash(state.PlanPath)))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read feature planner plan tab artifact: %v", err))
	for _, want := range []string{
		"# Feature Planner Candidate",
		"Status: " + featureplanner.FeatureStatePlanningStuck,
		"Route collapse dispatch through a slim event helper.",
		"pkg/collapse_event.go",
		"pkg/collapse_event_test.go",
		"## Coverage Review Summary",
		"- Behaviors: 1",
		"- Concrete test cases: 0",
		"- Test files: 1",
		"- Required commands: 1",
		"- Behaviors missing concrete test cases: 1",
		"## Behavior Review",
		"### Behavior: TDD-COLLAPSE-EVENT - Collapse dispatch routes through helper.",
		"- Concrete tests: No concrete test cases recorded.",
		"## Traceability Matrix",
		"| TDD-COLLAPSE-EVENT |   |   |   |   | pkg/collapse_event_test.go | go test ./pkg | CollapseEvent helper |",
		"## Test Obligations",
		"TDD-COLLAPSE-EVENT - Collapse dispatch routes through helper.",
		"## Invariants",
		"go test ./pkg",
		"CollapseEvent helper",
	} {
		gomega.Expect(strings.Contains(string(planTabBody), want)).To(gomega.BeTrue(), fmt.Sprintf("plan tab artifact missing %q:\n%s", want, string(planTabBody)))
	}
	gomega.Expect(strings.Contains(string(planTabBody), "**")).To(gomega.BeFalse(), fmt.Sprintf("plan tab artifact should avoid raw inline markdown styling:\n%s", string(planTabBody)))

	model := &Model{}
	model.planPath = filepath.Join(projectRoot, filepath.FromSlash(state.PlanPath))
	model.panelTab = panelTabPlan
	model.history.panelOpen = true
	model.history.focused = true
	model.planViewport = viewport.New(100, 80)
	model.renderPlanPanelContent()

	renderedPlan := model.planViewport.View()
	for _, want := range []string{"▾ Feature Planner Candidate", "▾ Coverage Review Summary", "▾ Behavior Review", "▾ Traceability Matrix", "CollapseEvent helper"} {
		gomega.Expect(strings.Contains(renderedPlan, want)).To(gomega.BeTrue(), fmt.Sprintf("rendered plan missing %q:\n%s", want, renderedPlan))
	}

	atomsIdx := planSelectableRowIndex(model.currentPlanSelectableRows(), "Atoms")
	gomega.Expect(atomsIdx < 0).To(gomega.BeFalse(), fmt.Sprintf("Atoms heading should be selectable in rendered plan:\n%s", renderedPlan))
	model.planUI.cursor = atomsIdx
	{
		handled, _ := model.handlePlanPanelKey(tea.KeyMsg{Type: tea.KeyEnter})
		gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("Enter should collapse generated Atoms section"))
	}

	{
		rows := model.currentPlanSelectableRows()
		gomega.Expect(atomsIdx).To(gomega.BeNumerically("<", len(rows)), fmt.Sprintf("generated Atoms section was not marked collapsed: %+v", rows))
		gomega.Expect(model.planUI.collapsedHeadings[rows[atomsIdx].HeadingKey]).To(gomega.BeTrue(), fmt.Sprintf("generated Atoms section was not marked collapsed: %+v", rows))
	}
})

var _ = ginkgo.It("run prompt once feature planner command uses shared entry point", func() {
	t := ginkgo.GinkgoT()
	projectRoot := t.TempDir()
	writeFile(t, projectRoot, "go.mod", "module example.com/tui\n")

	client := &scriptedLLMRecorder{responses: []string{featurePlannerCommandNewFileQuestionJSON(t)}}
	m := New(config.Config{
		Chat: config.Chat{
			ProjectRoot:            projectRoot,
			AutoStartServer:        false,
			AttachInstructionFiles: false,
		},
		Workspace: config.Workspace{Root: projectRoot, CWD: projectRoot},
	})
	defer m.Close()
	m.client = client.Mock(t)
	m.ready = true

	answer, err := m.RunPromptOnce(context.Background(), "/feature-planner Implement CollapseEvent helper.")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("RunPromptOnce: %v", err))

	gomega.Expect(strings.Contains(answer, "Feature planner needs your approval before it can continue.")).To(gomega.BeTrue(), fmt.Sprintf("expected feature-planner approval answer, got:\n%s", answer))
	gomega.Expect(strings.TrimSpace(m.directFeaturePlannerSessionID)).NotTo(gomega.Equal(""), fmt.Sprint("shared entry point should remember direct feature-planner session"))
	gomega.Expect(len(m.messages)).To(gomega.Not(gomega.Equal(0)), fmt.Sprintf("feature-planner typed command should be logged once as user message, got %#v", m.messages))
	gomega.Expect(m.messages[0].Content).To(gomega.Equal("/feature-planner Implement CollapseEvent helper."), fmt.Sprintf("feature-planner typed command should be logged once as user message, got %#v", m.messages))
})

var _ = ginkgo.It("feature planner tui enter command uses shared entry point", ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	projectRoot := t.TempDir()
	writeFile(t, projectRoot, "go.mod", "module example.com/tui\n")

	client := &scriptedLLMRecorder{responses: []string{featurePlannerCommandNewFileQuestionJSON(t)}}
	m := New(config.Config{
		Chat: config.Chat{
			ProjectRoot:            projectRoot,
			AutoStartServer:        false,
			AttachInstructionFiles: false,
		},
		Workspace: config.Workspace{Root: projectRoot, CWD: projectRoot},
	})
	defer m.Close()
	m.client = client.Mock(t)
	m.ready = true
	m.events = make(chan tea.Msg, 16)
	m.input = textarea.New()
	m.width = 100
	m.height = 24
	m.resize()

	m.input.SetValue("/feature-planner Implement CollapseEvent helper.")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	m = waitForFeaturePlannerTUIStreamDone(ctx, t, m)

	gomega.Expect(client.callCount).To(gomega.Equal(1), fmt.Sprintf("feature-planner slash command should make one planner call, got %d", client.callCount))
	gomega.Expect(strings.TrimSpace(m.directFeaturePlannerSessionID)).NotTo(gomega.Equal(""), fmt.Sprint("interactive slash command should remember direct feature-planner session"))
	gomega.Expect(len(m.messages)).To(gomega.Equal(2), fmt.Sprintf("interactive slash command should be recorded once in chat messages, got %#v", m.messages))
	gomega.Expect(m.messages[0].Role).To(gomega.Equal("user"), fmt.Sprintf("interactive slash command should be recorded once in chat messages, got %#v", m.messages))
	gomega.Expect(m.messages[0].Content).To(gomega.Equal("/feature-planner Implement CollapseEvent helper."), fmt.Sprintf("interactive slash command should be recorded once in chat messages, got %#v", m.messages))
})

var _ = ginkgo.It("run prompt once direct feature planner revision does not become chat", func() {
	t := ginkgo.GinkgoT()
	projectRoot := t.TempDir()
	writeFile(t, projectRoot, "go.mod", "module example.com/tui\n")

	state := featureplanner.NewState("question-session", "Lay out the project bones.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningQuestion
	state.OpenQuestions = []featureplanner.PlannerQuestion{{
		ID:                "question-new-files",
		Status:            "open",
		QuestionSummary:   "Approve or revise greenfield project scope",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "The empty workspace requires explicit authorization before creating proposed files.",
		RequestedDecision: "Approve creation of src/model.py and tests/test_model.py.",
		AuthorityRequired: "operator_approval",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     []string{"src/model.py", "tests/test_model.py"},
		AffectedFields:    []string{"new_file_authorization"},
	}}
	{
		_, err := featureplanner.SaveState(projectRoot, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveState: %v", err))
	}

	client := &scriptedLLMRecorder{responses: []string{
		`{"tool":"ask_planner_question","question_summary":"Define runnable MVP requirements","blocking_claim":"The revised full-MVP scope needs explicit runtime behavior before a safe feature plan can be submitted.","requested_decision":"Choose the minimal runnable behavior and verification command.","question_kind":"external_answer_required","authority_required":"operator_approval","required_before":"plan_approval","affected_paths":["src/model.py","tests/test_model.py"],"affected_fields":["requirements_scope","verification_command"]}`,
	}}
	m := New(config.Config{
		Chat: config.Chat{
			ProjectRoot:            projectRoot,
			AutoStartServer:        false,
			AttachInstructionFiles: false,
		},
		Workspace: config.Workspace{Root: projectRoot, CWD: projectRoot},
	})
	defer m.Close()
	m.client = client.Mock(t)
	m.ready = true
	m.directFeaturePlannerSessionID = state.SessionID

	revision := "Please revise the scope to be a full MVP, as minimal as possible."
	answer, err := m.RunPromptOnce(context.Background(), revision)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("RunPromptOnce: %v", err))

	for _, message := range m.messages {
		gomega.Expect(message).NotTo(gomega.And(
			gomega.HaveField("Role", gomega.Equal("user")),
			gomega.HaveField("Content", gomega.Equal(revision)),
		), fmt.Sprintf("direct feature-planner operator input should not be appended as user chat history: %#v", m.messages))
	}
	gomega.Expect(len(client.messages)).To(gomega.Not(gomega.Equal(0)), fmt.Sprintf("revision did not reach feature-planner resume prompt: %#v", client.messages))
	gomega.Expect(featurePlannerRequestMessagesContain(client.messages[0], "Operator plan revision instruction:")).To(gomega.BeTrue(), fmt.Sprintf("revision did not reach feature-planner resume prompt: %#v", client.messages))
	gomega.Expect(strings.Contains(answer, "Define runnable MVP requirements")).To(gomega.BeTrue(), fmt.Sprintf("expected replanning question answer, got:\n%s", answer))
})

var _ = ginkgo.It("feature planner tui direct planning repair accepts plain operator input", func() {
	t := ginkgo.GinkgoT()
	projectRoot := t.TempDir()
	state := featureplanner.NewState("repair-session", "Implement brewery cistern atom.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningRepair
	state.PlanningLoop.State = featureplanner.FeatureStatePlanningRepair
	state.Blockers = []string{`atom 0 prompt mentions repo path "fluid_network_system.h/cpp" outside scope`}
	{
		_, err := featureplanner.SaveState(projectRoot, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveState: %v", err))
	}

	m := &Model{
		cfg: config.Config{
			Chat:      config.Chat{ProjectRoot: projectRoot},
			Workspace: config.Workspace{Root: projectRoot, CWD: projectRoot},
		},
		promptRuntimeState: promptRuntimeState{
			promptRuntimeCompletionState: promptRuntimeCompletionState{directFeaturePlannerSessionID: state.SessionID},
		},
	}

	gomega.Expect(featureplanner.CountOpenPlannerQuestions(state)).To(gomega.Equal(0), fmt.Sprint("repair-only state should not masquerade as an open planner question"))
	gomega.Expect(featurePlannerStateAcceptsDirectOperatorInput(state)).To(gomega.BeTrue(), fmt.Sprint("plain typed input should resume direct feature-planner planning-repair state"))
	gomega.Expect(directFeaturePlannerShouldConsumeOperatorInput(resolveProjectRoot(m.cfg.Chat.ProjectRoot, m.workspace), m.directFeaturePlannerSessionID, "Treat fluid_network_system.h/cpp as the two explicit scoped files.")).To(gomega.BeTrue(), fmt.Sprint("repair state should still consume plain repair instructions"))
	gomega.Expect(directFeaturePlannerShouldConsumeOperatorInput(resolveProjectRoot(m.cfg.Chat.ProjectRoot, m.workspace), m.directFeaturePlannerSessionID, "Can we discuss why this is blocked first?")).To(gomega.BeFalse(), fmt.Sprint("repair state should leave ordinary questions interactive"))

	resumePrompt := directFeaturePlannerResumePrompt(state, "Treat fluid_network_system.h/cpp as the two explicit scoped files.")
	for _, want := range []string{
		"Implement brewery cistern atom.",
		"Operator repair instruction:",
		"Treat fluid_network_system.h/cpp as the two explicit scoped files.",
	} {
		gomega.Expect(strings.Contains(resumePrompt, want)).To(gomega.BeTrue(), fmt.Sprintf("repair resume prompt missing %q:\n%s", want, resumePrompt))
	}
})

var _ = ginkgo.It("feature planner tui direct operator question does not consume chat questions", func() {
	t := ginkgo.GinkgoT()
	projectRoot := t.TempDir()
	state := featureplanner.NewState("question-session", "Implement CollapseEvent helper.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningQuestion
	state.OpenQuestions = []featureplanner.PlannerQuestion{{
		ID:                "question-new-files",
		Status:            "open",
		QuestionSummary:   "Authorize CollapseEvent new files",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "Missing helper and test files require operator approval.",
		RequestedDecision: "Approve creating pkg/collapse_event.go and pkg/collapse_event_test.go and provide the exact verification command for this project.",
		AuthorityRequired: "operator_approval",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     []string{"pkg/collapse_event.go", "pkg/collapse_event_test.go"},
		AffectedFields:    []string{"new_file_authorization", "verification_command"},
	}}
	{
		_, err := featureplanner.SaveState(projectRoot, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveState: %v", err))
	}

	m := &Model{
		cfg: config.Config{
			Chat:      config.Chat{ProjectRoot: projectRoot},
			Workspace: config.Workspace{Root: projectRoot, CWD: projectRoot},
		},
		promptRuntimeState: promptRuntimeState{
			promptRuntimeCompletionState: promptRuntimeCompletionState{directFeaturePlannerSessionID: state.SessionID},
		},
	}

	for _, input := range []string{
		"Is this information model-generated, or is it a harness template?",
		"Can we talk about the design plan first?",
		"What paths are you asking me to approve?",
		"Update me on the plan.",
	} {
		gomega.Expect(directFeaturePlannerShouldConsumeOperatorInput(resolveProjectRoot(m.cfg.Chat.ProjectRoot, m.workspace), m.directFeaturePlannerSessionID, input)).To(gomega.BeFalse(), fmt.Sprintf("chat question should not be consumed as feature-planner approval: %q", input))
	}

	for _, input := range []string{
		"Approved.",
		"Reject this scope.",
		"Use pkg/event.go and pkg/event_test.go instead.",
		"You may create these files.",
		"Please revise the scope to be a full MVP, as minimal as possible.",
	} {
		gomega.Expect(directFeaturePlannerShouldConsumeOperatorInput(resolveProjectRoot(m.cfg.Chat.ProjectRoot, m.workspace), m.directFeaturePlannerSessionID, input)).To(gomega.BeTrue(), fmt.Sprintf("explicit operator answer should resume feature planner: %q", input))
	}

	for _, input := range []string{
		"Yes, use existing files instead.",
		"Approved, but change the paths before continuing.",
	} {
		gomega.Expect(directFeaturePlannerShouldConsumeOperatorInput(resolveProjectRoot(m.cfg.Chat.ProjectRoot, m.workspace), m.directFeaturePlannerSessionID, input)).To(gomega.BeTrue(), fmt.Sprintf("revision-shaped approval should stay in feature-planner lane: %q", input))
		gomega.Expect(directFeaturePlannerInputLooksPlanRevision(input)).To(gomega.BeTrue(), fmt.Sprintf("revision-shaped approval should be routed as plan revision: %q", input))
	}
})

var _ = ginkgo.It("feature planner operator input classifies polite plan revisions", func() {
	for _, input := range []string{
		"Can you add more info to the plan?",
		"Could you add more detail to the plan?",
		"Please flesh out the plan with persistence and multi-repo support.",
		"Add persistent memory requirements to the plan.",
		"Can you update the plan with this in mind?",
	} {
		{
			got := classifyFeaturePlannerOperatorInput(input)
			gomega.Expect(got).To(gomega.Equal(featurePlannerOperatorInputRevision), fmt.Sprintf("plan refinement request should classify as revision, input=%q got=%q", input, got))
		}
	}

	for _, input := range []string{
		"Can we discuss why this is blocked first?",
		"What paths are you asking me to approve?",
		"Update me on the plan.",
		"Should we expand the test suite?",
		"Use the source facts from the current branch.",
	} {
		{
			got := classifyFeaturePlannerOperatorInput(input)
			gomega.Expect(got).To(gomega.Equal(featurePlannerOperatorInputNone), fmt.Sprintf("discussion/source-evidence question should stay in chat, input=%q got=%q", input, got))
		}
	}
})

var _ = ginkgo.It("feature planner tui direct revision instruction replans open question", func() {
	t := ginkgo.GinkgoT()
	projectRoot := t.TempDir()
	writeFile(t, projectRoot, "go.mod", "module example.com/tui\n")

	client := &scriptedLLMRecorder{responses: []string{
		featurePlannerCommandNewFileQuestionJSON(t),
		`{"tool":"ask_planner_question","question_summary":"Define minimal runnable MVP requirements","blocking_claim":"The requested full MVP scope is broader than the current scaffolding plan and needs explicit operator requirements before a safe feature plan can be submitted.","requested_decision":"Choose the minimal runnable MVP behavior to target: CLI prompt loop, memory retention format, persistence location, and verification command.","question_kind":"external_answer_required","authority_required":"operator_approval","required_before":"plan_approval","affected_paths":["src/model.py","tests/test_model.py","src/tokenizer.py","tests/test_tokenizer.py"],"affected_fields":["requirements_scope","verification_command"]}`,
	}}
	m := newDetachedPromptExecutionSession(context.Background(), &Model{
		client: client.Mock(t),
		cfg: config.Config{
			Chat:      config.Chat{ProjectRoot: projectRoot},
			Workspace: config.Workspace{Root: projectRoot, CWD: projectRoot},
		},
		workspace: workspace.NewManager(config.Workspace{Root: projectRoot, CWD: projectRoot}),
	})

	{
		_, err := m.runFeaturePlannerCommandOnce(context.Background(), "Lay out the project bones.")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runFeaturePlannerCommandOnce: %v", err))
	}
	answer, err := m.runDirectFeaturePlannerAnswerOnce(context.Background(), "Please revise the scope to be a full MVP, as minimal as possible.")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runDirectFeaturePlannerAnswerOnce: %v", err))
	gomega.Expect(len(client.messages) < 2).To(gomega.BeFalse(), fmt.Sprintf("expected resumed planner model call, got %#v", client.messages))
	resumedPrompt := client.messages[1][len(client.messages[1])-1].Content
	for _, want := range []string{
		"User request:\nLay out the project bones.",
		"Operator plan revision instruction:",
		"Please revise the scope to be a full MVP, as minimal as possible.",
	} {
		gomega.Expect(strings.Contains(resumedPrompt, want)).To(gomega.BeTrue(), fmt.Sprintf("resumed planner prompt missing %q:\n%s", want, resumedPrompt))
	}
	gomega.Expect(answer).To(gomega.ContainSubstring("Define minimal runnable MVP requirements"), fmt.Sprintf("feature-planner answer did not surface planner follow-up question:\n%s", answer))
	gomega.Expect(answer).To(gomega.ContainSubstring("Choose the minimal runnable MVP behavior to target"), fmt.Sprintf("feature-planner answer did not surface planner follow-up question:\n%s", answer))

	state := loadFeaturePlannerCommandStateFile(t, filepath.Join(projectRoot, ".idiom", "feature-planner", m.directFeaturePlannerSessionID, "state.json"))
	gomega.Expect(len(state.OpenQuestions)).To(gomega.Equal(2), fmt.Sprintf("revision instruction should answer the prior question and allow a new planner question: %+v", state.OpenQuestions))
	gomega.Expect(state.OpenQuestions[0].Status).To(gomega.Equal("answered"), fmt.Sprintf("revision instruction should answer the prior question and allow a new planner question: %+v", state.OpenQuestions))
	gomega.Expect(state.OpenQuestions[1].Status).To(gomega.Equal("open"), fmt.Sprintf("revision instruction should answer the prior question and allow a new planner question: %+v", state.OpenQuestions))
	gomega.Expect(strings.Contains(state.OpenQuestions[0].Answer, "full MVP")).To(gomega.BeTrue(), fmt.Sprintf("revision instruction not retained on planner question: %+v", state.OpenQuestions[0]))
	gomega.Expect(strings.Contains(state.OpenQuestions[1].QuestionSummary, "minimal runnable MVP")).To(gomega.BeTrue(), fmt.Sprintf("new planner question should own MVP requirement discovery: %+v", state.OpenQuestions[1]))
})

var _ = ginkgo.It("keeps an open approval question interactive across chat and approval", ginkgo.Label("e2e"), ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	projectRoot := t.TempDir()
	cfg := config.Default()
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.MaxToolSteps = 1
	cfg.Workspace.Root = projectRoot
	cfg.Workspace.CWD = projectRoot
	cfg.MCP = nil

	plan := featurePlannerCommandStandaloneCollapsePlan()
	plan.Atoms[0].SDDRefs = nil
	planReply := featurePlannerCommandPlanJSON(t, plan)
	// One ordinary chat reply is followed by the initial candidate and two
	// bounded repair attempts for the intentionally incomplete plan.
	client := &scriptedLLMRecorder{responses: []string{
		"This is normal chat output, not a feature-planner approval answer.",
		planReply,
		planReply,
		planReply,
	}}

	m := New(cfg)
	defer m.Close()
	m.client = client.Mock(t)
	m.ready = true
	m.events = make(chan tea.Msg, 16)
	m.input = textarea.New()
	m.width = 100
	m.height = 24
	m.resize()

	state := featureplanner.NewState("question-session", "Implement CollapseEvent helper.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningQuestion
	state.OpenQuestions = []featureplanner.PlannerQuestion{{
		ID:                "question-new-files",
		Status:            "open",
		QuestionSummary:   "Authorize CollapseEvent new files",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "Missing helper and test files require operator approval.",
		RequestedDecision: "Approve creating pkg/collapse_event.go and pkg/collapse_event_test.go and provide the exact verification command for this project.",
		AuthorityRequired: "operator_approval",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     []string{"pkg/collapse_event.go", "pkg/collapse_event_test.go"},
		AffectedFields:    []string{"new_file_authorization", "verification_command"},
	}}
	{
		_, err := featureplanner.SaveState(projectRoot, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveState: %v", err))
	}
	m.directFeaturePlannerSessionID = state.SessionID

	m.input.SetValue("Is this information model-generated, or is it a harness template?")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	m = waitForFeaturePlannerTUIStreamDone(ctx, t, m)

	gomega.Expect(client.callCount).To(gomega.Equal(1))
	gomega.Expect(client.messages).To(gomega.HaveLen(1))
	gomega.Expect(featurePlannerRequestMessagesContain(client.messages[0], "model-generated")).To(gomega.BeTrue())

	afterChat := loadFeaturePlannerCommandStateFile(t, filepath.Join(projectRoot, ".idiom", "feature-planner", state.SessionID, "state.json"))
	gomega.Expect(len(afterChat.OpenQuestions)).To(gomega.Equal(1), fmt.Sprintf("ordinary chat must not answer planner approval question: %+v", afterChat.OpenQuestions))
	gomega.Expect(afterChat.OpenQuestions[0].Status).To(gomega.Equal("open"), fmt.Sprintf("ordinary chat must not answer planner approval question: %+v", afterChat.OpenQuestions))
	gomega.Expect(len(afterChat.AuthorityDeltas)).To(gomega.Equal(0), fmt.Sprintf("ordinary chat must not create authority deltas: %+v", afterChat.AuthorityDeltas))

	m.input.SetValue("Approved. Use go test ./pkg.")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	waitForFeaturePlannerTUIStreamDone(ctx, t, m)

	gomega.Expect(client.callCount).To(gomega.Equal(4))

	afterApproval := loadFeaturePlannerCommandStateFile(t, filepath.Join(projectRoot, ".idiom", "feature-planner", state.SessionID, "state.json"))
	gomega.Expect(len(afterApproval.OpenQuestions)).To(gomega.Equal(1), fmt.Sprintf("explicit approval should answer planner question: %+v", afterApproval.OpenQuestions))
	gomega.Expect(afterApproval.OpenQuestions[0].Status).To(gomega.Equal("answered"), fmt.Sprintf("explicit approval should answer planner question: %+v", afterApproval.OpenQuestions))
	gomega.Expect(len(afterApproval.AuthorityDeltas)).To(gomega.Equal(1), fmt.Sprintf("explicit approval should create typed authority delta: %+v", afterApproval.AuthorityDeltas))
	gomega.Expect(afterApproval.AuthorityDeltas[0].Actual).To(gomega.Equal("Approved. Use go test ./pkg."), fmt.Sprintf("explicit approval should create typed authority delta: %+v", afterApproval.AuthorityDeltas))
	gomega.Expect(afterApproval.AuthorityDeltas[0].AffectedFields).To(gomega.ContainElement("verification_command"), fmt.Sprintf("explicit approval should create typed authority delta: %+v", afterApproval.AuthorityDeltas))
	gomega.Expect(strings.TrimSpace(afterApproval.PlanPath)).NotTo(gomega.Equal(""), fmt.Sprint("resumed feature planner should write the factual candidate to the Plan tab"))
	planTabBody, err := os.ReadFile(filepath.Join(projectRoot, filepath.FromSlash(afterApproval.PlanPath)))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	for _, want := range []string{
		"# Feature Planner Candidate",
		"Status: " + featureplanner.FeatureStatePlanningStuck,
		"Route collapse dispatch through a slim event helper.",
		"pkg/collapse_event.go",
		"pkg/collapse_event_test.go",
		"## Test Obligations",
		"## Invariants",
		"go test ./pkg",
	} {
		gomega.Expect(strings.Contains(string(planTabBody), want)).To(gomega.BeTrue(), fmt.Sprintf("plan tab artifact missing %q:\n%s", want, string(planTabBody)))
	}
	gomega.Expect(strings.Contains(string(planTabBody), "**")).To(gomega.BeFalse(), fmt.Sprintf("plan tab artifact should avoid raw inline markdown styling:\n%s", string(planTabBody)))
})

var _ = ginkgo.It("feature planner tui followup question uses active plan artifact evidence", func() {
	t := ginkgo.GinkgoT()
	projectRoot := t.TempDir()
	cfg := config.Default()
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.MaxToolSteps = 0
	cfg.Workspace.Root = projectRoot
	cfg.Workspace.CWD = projectRoot
	cfg.MCP = nil

	plan := featurePlannerCommandStandaloneCollapsePlan()
	plan.Summary = "Define the feature skeleton, including model and inference components."
	plan.TopLevelScope.Objective = "Create a proposed model wrapper and inference loop."
	plan.Atoms[0].RequiredEditPaths = []string{"src/model_wrapper.py", "tests/test_model_wrapper.py"}
	plan.Atoms[0].Title = "Create model wrapper"
	plan.Atoms[0].Summary = "Store the selected model name and expose it to callers."
	state := featureplanner.NewState("question-session", "Let's lay out the bones for this LLM brain project as you've outlined above", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningQuestion
	state.RejectedPlanCandidates = []featureplanner.RejectedPlanCandidate{{
		Plan:       plan,
		PlanHash:   "candidate-hash",
		Diagnostic: "missing new-file authorization",
	}}
	state.OpenQuestions = []featureplanner.PlannerQuestion{{
		ID:                "question-new-files",
		Status:            "open",
		QuestionSummary:   "Approve or revise greenfield project scope",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "The empty workspace requires explicit authorization before creating proposed files.",
		RequestedDecision: "Approve creation of src/model_wrapper.py and tests/test_model_wrapper.py.",
		AuthorityRequired: "operator_approval",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     []string{"src/model_wrapper.py", "tests/test_model_wrapper.py"},
		AffectedFields:    []string{"new_file_authorization", "verification_command"},
	}}
	{
		_, err := featureplanner.SaveState(projectRoot, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveState: %v", err))
	}

	client := &scriptedLLMRecorder{responses: []string{"The active plan does not create durable memory yet. Its proposed files to create are `src/model_wrapper.py` and `tests/test_model_wrapper.py`; those are proposed plan paths, not existing source files."}}
	m := New(cfg)
	defer m.Close()
	m.client = client.Mock(t)
	m.ready = true
	m.directFeaturePlannerSessionID = state.SessionID

	answer, err := m.RunPromptOnce(context.Background(), "how are we planning on retaining memory?")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("RunPromptOnce: %v", err))
	gomega.Expect(answer).To(gomega.Not(gomega.ContainSubstring("I could not produce a supported final answer")), fmt.Sprintf("follow-up should not leak source-grounding blocker:\n%s", answer))
	gomega.Expect(answer).To(gomega.Not(gomega.ContainSubstring("source-body evidence")), fmt.Sprintf("follow-up should not leak source-grounding blocker:\n%s", answer))
	gomega.Expect(strings.Contains(answer, "does not create durable memory yet")).To(gomega.BeTrue(), fmt.Sprintf("unexpected answer:\n%s", answer))
	gomega.Expect(len(client.messages)).To(gomega.Not(gomega.Equal(0)), fmt.Sprintf("normal chat request did not receive active plan context: %#v", client.messages))
	gomega.Expect(featurePlannerRequestMessagesContain(client.messages[0], "Active feature-planner plan artifact")).To(gomega.BeTrue(), fmt.Sprintf("normal chat request did not receive active plan context: %#v", client.messages))
	gomega.Expect(featurePlannerRequestMessagesContain(client.messages[0], "Proposed paths to create:")).To(gomega.BeTrue(), fmt.Sprintf("normal chat request did not receive active plan context: %#v", client.messages))
})

var _ = ginkgo.It("feature planner plan execute runner does not feed prompt to direct feature planner", func() {
	t := ginkgo.GinkgoT()
	projectRoot := t.TempDir()
	state := featureplanner.NewState("question-session", "Plan greenfield LLM brain MVP.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningQuestion
	state.PlanningLoop.State = featureplanner.FeatureStatePlanningQuestion
	state.OpenQuestions = []featureplanner.PlannerQuestion{{
		ID:                "q-001",
		Status:            "open",
		QuestionSummary:   "Approve new files",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "The empty workspace requires explicit authorization.",
		RequestedDecision: "Approve creating brain/core.py and tests/test_brain.py.",
		AuthorityRequired: "operator_approval",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     []string{"brain/core.py", "tests/test_brain.py"},
		AffectedFields:    []string{"new_file_authorization", "verification_command"},
	}}
	{
		_, err := featureplanner.SaveState(projectRoot, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveState: %v", err))
	}

	client := &scriptedLLMRecorder{
		responses:         []string{"plan-execute model response"},
		assertCallCount:   true,
		expectedCallCount: 2,
	}
	m := newDetachedPromptExecutionSession(context.Background(), &Model{
		client: client.Mock(t),
		promptRuntimeState: promptRuntimeState{
			promptRuntimeCompletionState: promptRuntimeCompletionState{directFeaturePlannerSessionID: state.SessionID},
		},
		cfg: config.Config{
			Chat:      config.Chat{ProjectRoot: projectRoot},
			Workspace: config.Workspace{Root: projectRoot, CWD: projectRoot},
		},
		workspace: workspace.NewManager(config.Workspace{Root: projectRoot, CWD: projectRoot}),
	})

	runner := m.newPlanExecutePromptRunner(context.Background())
	_, _ = runner(m.cfg, "# Plan-then-execute (planner phase)\n\nmodel-facing child prompt", planexecute.RunOptions{
		TaskFamily: string(autonomy.TaskFamilyPlanThenExecutePlanner),
	}, nil)

	gomega.Expect(m.directFeaturePlannerSessionID).To(gomega.Equal(state.SessionID), fmt.Sprintf("direct feature-planner session was not restored: %q", m.directFeaturePlannerSessionID))
	after := loadFeaturePlannerCommandStateFile(t, filepath.Join(projectRoot, ".idiom", "feature-planner", state.SessionID, "state.json"))
	gomega.Expect(len(after.OpenQuestions)).To(gomega.Equal(1), fmt.Sprintf("nested plan-execute prompt was consumed as feature-planner answer: %+v", after.OpenQuestions))
	gomega.Expect(after.OpenQuestions[0].Status).To(gomega.Equal("open"), fmt.Sprintf("nested plan-execute prompt was consumed as feature-planner answer: %+v", after.OpenQuestions))
	gomega.Expect(after.OpenQuestions[0].Answer).To(gomega.Equal(""), fmt.Sprintf("nested plan-execute prompt was consumed as feature-planner answer: %+v", after.OpenQuestions))
})

var _ = ginkgo.It("feature planner tui interactive plan update resumes direct feature planner", ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	projectRoot := t.TempDir()
	cfg := config.Default()
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.ProjectRoot = projectRoot
	cfg.Workspace.Root = projectRoot
	cfg.Workspace.CWD = projectRoot
	cfg.MCP = nil

	state := featureplanner.NewState("question-session", "Plan greenfield LLM brain MVP.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningQuestion
	state.OpenQuestions = []featureplanner.PlannerQuestion{{
		ID:                "question-new-files",
		Status:            "open",
		QuestionSummary:   "Approve or revise greenfield project scope",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "The empty workspace requires explicit authorization before creating proposed files.",
		RequestedDecision: "Approve creation of src/memory.py and tests/test_memory.py, or revise the greenfield project setup.",
		AuthorityRequired: "operator_approval",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     []string{"src/memory.py", "tests/test_memory.py"},
		AffectedFields:    []string{"new_file_authorization", "verification_command"},
	}}
	{
		_, err := featureplanner.SaveState(projectRoot, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveState: %v", err))
	}

	client := &scriptedLLMRecorder{responses: []string{
		`{"tool":"ask_planner_question","question_summary":"Confirm persistence and multi-repo MVP boundaries","blocking_claim":"The revised plan now targets persistent memory and multi-repo support, so it needs operator confirmation of the MVP boundary before approval.","requested_decision":"Confirm that the MVP should include disk-backed memory storage, repository-scoped namespaces, and tests for cross-repo isolation.","question_kind":"external_answer_required","authority_required":"operator_approval","required_before":"plan_approval","affected_paths":["src/memory.py","src/repository.py","tests/test_memory.py","tests/test_repository.py"],"affected_fields":["requirements_scope","new_file_authorization","verification_command"]}`,
	}}
	m := New(cfg)
	defer m.Close()
	m.client = client.Mock(t)
	m.ready = true
	m.events = make(chan tea.Msg, 16)
	m.input = textarea.New()
	m.width = 100
	m.height = 24
	m.resize()
	m.directFeaturePlannerSessionID = state.SessionID

	revision := "Can you update the plan with persistent memory and multi-repo support in mind?"
	m.input.SetValue(revision)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	m = waitForFeaturePlannerTUIStreamDone(ctx, t, m)

	gomega.Expect(len(client.messages)).To(gomega.Equal(1), fmt.Sprintf("plan update should resume feature-planner exactly once, got %#v", client.messages))
	gomega.Expect(featurePlannerRequestMessagesContain(client.messages[0], "Operator plan revision instruction:")).To(gomega.BeTrue(), fmt.Sprintf("interactive plan update did not reach feature-planner revision prompt: %#v", client.messages))
	gomega.Expect(featurePlannerRequestMessagesContain(client.messages[0], revision)).To(gomega.BeTrue(), fmt.Sprintf("interactive plan update did not reach feature-planner revision prompt: %#v", client.messages))
	for _, message := range m.messages {
		gomega.Expect(message).NotTo(gomega.And(
			gomega.HaveField("Role", gomega.Equal("user")),
			gomega.HaveField("Content", gomega.Equal(revision)),
		), fmt.Sprintf("direct feature-planner revision must not be appended as normal chat history: %#v", m.messages))
	}

	after := loadFeaturePlannerCommandStateFile(t, filepath.Join(projectRoot, ".idiom", "feature-planner", state.SessionID, "state.json"))
	gomega.Expect(len(after.OpenQuestions)).To(gomega.Equal(2), fmt.Sprintf("plan update should answer prior approval question and open revised planner question: %+v", after.OpenQuestions))
	gomega.Expect(after.OpenQuestions[0].Status).To(gomega.Equal("answered"), fmt.Sprintf("plan update should answer prior approval question and open revised planner question: %+v", after.OpenQuestions))
	gomega.Expect(after.OpenQuestions[1].Status).To(gomega.Equal("open"), fmt.Sprintf("plan update should answer prior approval question and open revised planner question: %+v", after.OpenQuestions))
	gomega.Expect(after.OpenQuestions[0].Answer).To(gomega.Equal(revision), fmt.Sprintf("prior question did not retain plan update answer: %+v", after.OpenQuestions[0]))
	gomega.Expect(strings.Contains(after.OpenQuestions[1].QuestionSummary, "persistence and multi-repo")).To(gomega.BeTrue(), fmt.Sprintf("revised planner question not surfaced: %+v", after.OpenQuestions[1]))
})

var _ = ginkgo.It("feature planner tui followup question answers planned test coverage from active plan", func() {
	t := ginkgo.GinkgoT()
	projectRoot := t.TempDir()
	cfg := config.Default()
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.MaxToolSteps = 0
	cfg.Chat.ProjectRoot = projectRoot
	cfg.Workspace.Root = projectRoot
	cfg.Workspace.CWD = projectRoot
	cfg.MCP = nil

	plan := featurePlannerCommandStandaloneCollapsePlan()
	plan.Summary = "Define the LLM brain skeleton with models, config, and orchestration."
	plan.TopLevelScope.Objective = "Establish the minimal viable architecture for an LLM brain system."
	plan.Atoms[0].RequiredEditPaths = []string{"src/llm_brain/models.py", "tests/test_models.py"}
	plan.Atoms[0].Title = "Define core data models"
	plan.Atoms[0].Summary = "Create Pydantic models and their validation tests."
	plan.Atoms = append(plan.Atoms, plan.Atoms[0])
	plan.Atoms[1].RequiredEditPaths = []string{"src/llm_brain/config.py", "tests/test_config.py"}
	plan.Atoms[1].Title = "Add configuration loading"
	plan.Atoms[1].Summary = "Load YAML/JSON config and validate error cases."
	plan.Atoms = append(plan.Atoms, plan.Atoms[0])
	plan.Atoms[2].RequiredEditPaths = []string{"src/llm_brain/orchestrator.py", "tests/test_orchestrator.py"}
	plan.Atoms[2].Title = "Add orchestrator skeleton"
	plan.Atoms[2].Summary = "Compose models, config, memory, and actions behind a minimal runner."

	state := featureplanner.NewState("coverage-question-session", "Let's lay out the bones for this LLM brain project as you've outlined above", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningQuestion
	state.RejectedPlanCandidates = []featureplanner.RejectedPlanCandidate{{
		Plan:       plan,
		PlanHash:   "candidate-hash",
		Diagnostic: "missing new-file authorization",
	}}
	state.OpenQuestions = []featureplanner.PlannerQuestion{{
		ID:                "question-new-files",
		Status:            "open",
		QuestionSummary:   "Approve or revise greenfield project scope",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "The empty workspace requires explicit authorization before creating proposed files.",
		RequestedDecision: "Approve creation of the proposed source and test files.",
		AuthorityRequired: "operator_approval",
		RequiredBefore:    "plan_approval",
		AffectedPaths: []string{
			"src/llm_brain/models.py",
			"tests/test_models.py",
			"src/llm_brain/config.py",
			"tests/test_config.py",
			"src/llm_brain/orchestrator.py",
			"tests/test_orchestrator.py",
		},
		AffectedFields: []string{"new_file_authorization", "verification_command"},
	}}

	client := &scriptedLLMRecorder{responses: []string{"The planned tests are `tests/test_models.py`, `tests/test_config.py`, and `tests/test_orchestrator.py`. I would expand the intended test suite with persistence variants, malformed-config cases, and orchestration failure invariants before approval."}}
	m := New(cfg)
	defer m.Close()
	m.client = client.Mock(t)
	m.ready = true
	session := newDetachedPromptExecutionSession(context.Background(), m)
	{
		_, err := session.saveFeaturePlannerStateAndPlanTab(&state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("saveFeaturePlannerStateAndPlanTab: %v", err))
	}
	m.directFeaturePlannerSessionID = state.SessionID

	answer, err := m.RunPromptOnce(context.Background(), "what tests do w have planned? should we expand the intended test suite?")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("RunPromptOnce: %v", err))
	for _, forbidden := range []string{"source-body evidence", "does not exist in this workspace", "I could not produce a supported final answer"} {
		gomega.Expect(strings.Contains(answer, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("planned-test follow-up leaked repair state %q:\n%s", forbidden, answer))
	}
	for _, want := range []string{"planned tests", "tests/test_models.py", "tests/test_config.py", "tests/test_orchestrator.py", "persistence variants", "malformed-config", "failure invariants"} {
		gomega.Expect(strings.Contains(answer, want)).To(gomega.BeTrue(), fmt.Sprintf("planned-test follow-up missing %q:\n%s", want, answer))
	}
	gomega.Expect(client.callCount).To(gomega.Equal(1), fmt.Sprintf("planned-test follow-up should be accepted without repair or nonexistent test reads, got %d model calls", client.callCount))
	gomega.Expect(len(client.messages)).To(gomega.Not(gomega.Equal(0)), fmt.Sprintf("planned-test follow-up did not receive active plan context: %#v", client.messages))
	gomega.Expect(featurePlannerRequestMessagesContain(client.messages[0], "Active feature-planner plan artifact")).To(gomega.BeTrue(), fmt.Sprintf("planned-test follow-up did not receive active plan context: %#v", client.messages))
	gomega.Expect(featurePlannerRequestMessagesContain(client.messages[0], "tests/test_models.py")).To(gomega.BeTrue(), fmt.Sprintf("planned-test follow-up did not receive active plan context: %#v", client.messages))

	afterChat := loadFeaturePlannerCommandStateFile(t, filepath.Join(projectRoot, ".idiom", "feature-planner", state.SessionID, "state.json"))
	gomega.Expect(len(afterChat.OpenQuestions)).To(gomega.Equal(1), fmt.Sprintf("ordinary planned-test chat must not answer planner approval question: %+v", afterChat.OpenQuestions))
	gomega.Expect(afterChat.OpenQuestions[0].Status).To(gomega.Equal("open"), fmt.Sprintf("ordinary planned-test chat must not answer planner approval question: %+v", afterChat.OpenQuestions))
	gomega.Expect(len(afterChat.AuthorityDeltas)).To(gomega.Equal(0), fmt.Sprintf("ordinary planned-test chat must not create authority deltas: %+v", afterChat.AuthorityDeltas))
})

var _ = ginkgo.It("restores active plan context for a resumed follow-up", ginkgo.Label("e2e"), func() {
	t := ginkgo.GinkgoT()
	projectRoot := t.TempDir()
	logDir := filepath.Join(projectRoot, "logs", "conversations")
	cfg := config.Default()
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.MaxToolSteps = 0
	cfg.Chat.DebugLogDir = logDir
	cfg.Chat.DebugLogMode = "redacted"
	cfg.Chat.ProjectRoot = projectRoot
	cfg.Workspace.Root = projectRoot
	cfg.Workspace.CWD = projectRoot
	cfg.MCP = nil

	plan := featurePlannerCommandStandaloneCollapsePlan()
	plan.Summary = "Define the feature skeleton, including model and inference components."
	plan.TopLevelScope.Objective = "Create a proposed model wrapper and inference loop."
	plan.Atoms[0].RequiredEditPaths = []string{"src/model_wrapper.py", "tests/test_model_wrapper.py"}
	plan.Atoms[0].Title = "Create model wrapper"
	plan.Atoms[0].Summary = "Store the selected model name and expose it to callers."

	state := featureplanner.NewState("resume-question-session", "Lay out the LLM brain project bones.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningQuestion
	state.RejectedPlanCandidates = []featureplanner.RejectedPlanCandidate{{
		Plan:       plan,
		PlanHash:   "candidate-hash",
		Diagnostic: "missing new-file authorization",
	}}
	state.OpenQuestions = []featureplanner.PlannerQuestion{{
		ID:                "question-new-files",
		Status:            "open",
		QuestionSummary:   "Approve or revise greenfield project scope",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "The empty workspace requires explicit authorization before creating proposed files.",
		RequestedDecision: "Approve creation of src/model_wrapper.py and tests/test_model_wrapper.py.",
		AuthorityRequired: "operator_approval",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     []string{"src/model_wrapper.py", "tests/test_model_wrapper.py"},
		AffectedFields:    []string{"new_file_authorization", "verification_command"},
	}}
	{
		_, err := featureplanner.SaveState(projectRoot, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveState: %v", err))
	}

	writeSessionLog(t, logDir, "resumed.jsonl", []string{
		`{"type":"message","role":"user","content":"/feature-planner Lay out the LLM brain project bones."}`,
		`{"type":"message","role":"assistant","content":"feature-planner: planning-question-open\nstate: .idiom/feature-planner/resume-question-session/state.json"}`,
	})

	client := &scriptedLLMRecorder{responses: []string{"The resumed active plan still does not include durable memory; it proposes model wrapper files only."}}
	m := New(cfg)
	defer m.Close()
	m.client = client.Mock(t)
	m.ready = true

	gomega.Expect(m.ResumeSession("resumed")).To(gomega.Succeed())
	gomega.Expect(m.directFeaturePlannerSessionID).To(gomega.Equal(state.SessionID))

	answer, err := m.RunPromptOnce(context.Background(), "how are we planning on retaining memory?")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(answer).To(gomega.ContainSubstring("does not include durable memory"))
	gomega.Expect(len(client.messages)).To(gomega.Not(gomega.Equal(0)), fmt.Sprintf("resumed chat follow-up did not receive active feature-planner plan context: %#v", client.messages))
	gomega.Expect(featurePlannerRequestMessagesContain(client.messages[0], "Active feature-planner plan artifact")).To(gomega.BeTrue(), fmt.Sprintf("resumed chat follow-up did not receive active feature-planner plan context: %#v", client.messages))
	gomega.Expect(featurePlannerRequestMessagesContain(client.messages[0], "Proposed paths to create:")).To(gomega.BeTrue(), fmt.Sprintf("resumed chat follow-up did not receive active feature-planner plan context: %#v", client.messages))

	afterChat := loadFeaturePlannerCommandStateFile(t, filepath.Join(projectRoot, ".idiom", "feature-planner", state.SessionID, "state.json"))
	gomega.Expect(len(afterChat.OpenQuestions)).To(gomega.Equal(1), fmt.Sprintf("ordinary resumed chat must not answer planner question: %+v", afterChat.OpenQuestions))
	gomega.Expect(afterChat.OpenQuestions[0].Status).To(gomega.Equal("open"), fmt.Sprintf("ordinary resumed chat must not answer planner question: %+v", afterChat.OpenQuestions))
})

var _ = ginkgo.It("feature planner active plan artifact requires open question", func() {
	t := ginkgo.GinkgoT()
	projectRoot := t.TempDir()
	cfg := config.Default()
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Workspace.Root = projectRoot
	cfg.Workspace.CWD = projectRoot

	plan := featurePlannerCommandStandaloneCollapsePlan()
	state := featureplanner.NewState("closed-question-session", "plan this", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStateReady
	state.RejectedPlanCandidates = []featureplanner.RejectedPlanCandidate{{
		Plan:       plan,
		PlanHash:   "candidate-hash",
		Diagnostic: "closed question should not leak active plan context",
	}}
	{
		_, err := featureplanner.SaveState(projectRoot, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveState: %v", err))
	}

	m := New(cfg)
	defer m.Close()
	m.directFeaturePlannerSessionID = state.SessionID
	session := newDetachedPromptExecutionSession(context.Background(), m)

	{
		context := session.activeFeaturePlannerPlanContext()
		gomega.Expect(context).To(gomega.Equal(""), fmt.Sprintf("closed planner question should not expose active plan context:\n%s", context))
	}
	{
		seed, ok := session.activeFeaturePlannerPlanSeedResult()
		gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("closed planner question should not expose active plan seed: %#v", seed))
	}
})

func waitForFeaturePlannerTUIStreamDone(ctx context.Context, t ginkgo.FullGinkgoTInterface, m *Model) *Model {
	ginkgo.GinkgoHelper()

	return waitForPromptWorkerResult(ctx, m)
}

func featurePlannerTestConfig(projectRoot string) config.Config {
	cfg := config.Default()
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.ProjectRoot = projectRoot
	cfg.Workspace.Root = projectRoot
	cfg.Workspace.CWD = projectRoot
	cfg.MCP = nil

	return cfg
}

func saveAcceptedSourceFeaturePlannerQuestionState(t ginkgo.FullGinkgoTInterface, projectRoot string, sessionID string) featureplanner.FeatureState {
	ginkgo.GinkgoHelper()

	state := featureplanner.NewState(sessionID, "Plan LLM brain MVP.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningQuestion
	state.PlanningLoop.State = featureplanner.FeatureStatePlanningQuestion
	state.OpenQuestions = []featureplanner.PlannerQuestion{{
		ID:                "question-source-facts",
		Status:            "open",
		QuestionSummary:   "Provide accepted source facts for helper signatures",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "The plan needs accepted source facts before helper calls can be approved.",
		RequestedDecision: "Provide accepted source facts for helper signatures.",
		AuthorityRequired: "accepted_source",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     []string{"llm_brain/query_handler.py"},
		AffectedFields:    []string{"source_localization"},
	}}
	{
		_, err := featureplanner.SaveState(projectRoot, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveState: %v", err))
	}

	return state
}

func newReadyFeaturePlannerTUITestModel(t ginkgo.FullGinkgoTInterface, cfg config.Config, client *scriptedLLMRecorder, sessionID string, interactive bool) *Model {
	ginkgo.GinkgoHelper()

	m := New(cfg)
	ginkgo.DeferCleanup(m.Close)
	m.client = client.Mock(t)
	m.ready = true
	m.directFeaturePlannerSessionID = sessionID
	if interactive {
		m.events = make(chan tea.Msg, 16)
		m.input = textarea.New()
		m.width = 100
		m.height = 24
		m.resize()
	}

	return m
}

func featurePlannerRequestMessagesContain(messages []llama.ChatMessage, text string) bool {
	for _, message := range messages {
		if strings.Contains(message.Content, text) {
			return true
		}
	}

	return false
}

var _ = ginkgo.It("feature planner tui direct accepted source question consumes only structured answers", func() {
	t := ginkgo.GinkgoT()
	projectRoot := t.TempDir()
	state := featureplanner.NewState("accepted-source-session", "Implement CollapseEvent helper.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningQuestion
	state.OpenQuestions = []featureplanner.PlannerQuestion{{
		ID:                "question-source-facts",
		Status:            "open",
		QuestionSummary:   "Provide accepted source facts for CollapseEvent",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "Concrete CollapseEvent helper usage requires source-localized signature evidence.",
		RequestedDecision: "Provide accepted source facts for the current CollapseEvent signature.",
		AuthorityRequired: "accepted_source",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     []string{"pkg/collapse_event.go"},
		AffectedFields:    []string{"source_localization"},
	}}
	{
		_, err := featureplanner.SaveState(projectRoot, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveState: %v", err))
	}

	m := &Model{
		cfg: config.Config{
			Chat:      config.Chat{ProjectRoot: projectRoot},
			Workspace: config.Workspace{Root: projectRoot, CWD: projectRoot},
		},
		promptRuntimeState: promptRuntimeState{
			promptRuntimeCompletionState: promptRuntimeCompletionState{directFeaturePlannerSessionID: state.SessionID},
		},
	}

	for _, input := range []string{
		"What source facts do you need?",
		"Can we discuss why this is blocked first?",
		"Use the source facts from the current branch.",
	} {
		gomega.Expect(directFeaturePlannerShouldConsumeOperatorInput(resolveProjectRoot(m.cfg.Chat.ProjectRoot, m.workspace), m.directFeaturePlannerSessionID, input)).To(gomega.BeFalse(), fmt.Sprintf("unstructured accepted-source chat should not be consumed: %q", input))
	}

	for _, input := range []string{
		`{"answer":"accepted","answer_source_kind":"accepted_source","evidence_refs":["pkg/collapse_event.go"],"source_facts":[{"path":"pkg/collapse_event.go","symbol":"NewCollapseEvent","signature":"CollapseEvent NewCollapseEvent(EntityID id)"}]}`,
		`answer_source_kind=accepted_source evidence_refs=pkg/collapse_event.go source_facts=NewCollapseEvent`,
	} {
		gomega.Expect(directFeaturePlannerShouldConsumeOperatorInput(resolveProjectRoot(m.cfg.Chat.ProjectRoot, m.workspace), m.directFeaturePlannerSessionID, input)).To(gomega.BeTrue(), fmt.Sprintf("structured accepted-source answer should be consumed: %q", input))
	}

	for _, input := range []string{
		"Can you add more detail to the plan?",
		"Please update the plan with persistent memory and multi-repo support.",
	} {
		gomega.Expect(directFeaturePlannerShouldConsumeOperatorInput(resolveProjectRoot(m.cfg.Chat.ProjectRoot, m.workspace), m.directFeaturePlannerSessionID, input)).To(gomega.BeTrue(), fmt.Sprintf("explicit plan revision should resume feature planner even while accepted-source question is open: %q", input))
		gomega.Expect(directFeaturePlannerInputLooksPlanRevision(input)).To(gomega.BeTrue(), fmt.Sprintf("accepted-source plan revision should be classified as revision: %q", input))
	}
})

var _ = ginkgo.It("feature planner plan tab prefers accepted plan after rejected candidate", func() {
	rejected := featurePlannerCommandStandaloneCollapsePlan()
	rejected.Summary = "Rejected candidate"
	accepted := featurePlannerCommandStandaloneCollapsePlan()
	accepted.Summary = "Accepted current plan"

	state := featureplanner.NewState("plan-session", "Implement CollapseEvent helper.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStateReady
	state.Plan = accepted
	state.RejectedPlanCandidates = []featureplanner.RejectedPlanCandidate{{
		Plan:       rejected,
		PlanHash:   "rejected-hash",
		Diagnostic: "old rejected candidate",
	}}

	plan, candidate, ok := featurePlannerPlanTabCandidate(state)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprint("expected displayable plan tab candidate"))
	gomega.Expect(candidate.PlanHash).To(gomega.Equal(""), fmt.Sprintf("ready state should prefer accepted state plan over stale rejected candidate: %+v", candidate))
	gomega.Expect(plan.Summary).To(gomega.Equal("Accepted current plan"), fmt.Sprintf("selected plan summary = %q, want accepted current plan", plan.Summary))

	state.Status = featureplanner.FeatureStatePlanningRepair
	plan, candidate, ok = featurePlannerPlanTabCandidate(state)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprint("expected displayable rejected plan candidate"))
	gomega.Expect(candidate.PlanHash).To(gomega.Equal("rejected-hash"), fmt.Sprintf("repair state should show latest rejected candidate, plan=%q candidate=%+v", plan.Summary, candidate))
	gomega.Expect(plan.Summary).To(gomega.Equal("Rejected candidate"), fmt.Sprintf("repair state should show latest rejected candidate, plan=%q candidate=%+v", plan.Summary, candidate))
})

var _ = ginkgo.It("feature planner plan tab hides raw summary and renders structured coverage", func() {
	plan := featurePlannerCommandStandaloneCollapsePlan()
	plan.BehavioralCoverageDraft = featurePlannerStructuredCoverageDraftForTest()
	plan.Summary = "RAW_MODEL_SUMMARY dense model-facing paragraph that should not be rendered in the plan tab."
	state := featureplanner.NewState("plan-session", "Implement CollapseEvent helper.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningRepair
	state.Blockers = []string{"RAW_BLOCKER_VALIDATION_DUMP atom 0 missing sdd_refs"}

	body := renderFeaturePlannerPlanTabMarkdown(state, "state.json", plan, featureplanner.RejectedPlanCandidate{
		PlanHash:   "candidate-hash",
		Diagnostic: "RAW_DIAGNOSTIC model-facing validation dump that should stay out of the plan tab.",
	})

	for _, hidden := range []string{
		"## Summary",
		"RAW_MODEL_SUMMARY",
		"RAW_DIAGNOSTIC",
		"RAW_BLOCKER_VALIDATION_DUMP",
		"Diagnostic:",
	} {
		gomega.Expect(strings.Contains(body, hidden)).To(gomega.BeFalse(), fmt.Sprintf("plan tab leaked raw display text %q:\n%s", hidden, body))
	}

	for _, want := range []string{
		"# Feature Planner Candidate",
		"Status: " + featureplanner.FeatureStatePlanningRepair,
		"Candidate: candidate-hash",
		"## Coverage Review Summary",
		"- Behaviors: 1",
		"- Behavior surfaces: 2",
		"- In-scope surfaces: 1",
		"- In-scope surfaces missing TDD obligations: 0",
		"- Open surface questions: 0",
		"- Variants: 11",
		"- Domain invariants: 2",
		"- Concrete test cases: 8",
		"- Test files: 1",
		"- Required commands: 1",
		"- Behaviors missing concrete test cases: 0",
		"## Behavior Review",
		"### Behavior: TDD-COLLAPSE-EVENT - Collapse dispatch routes through helper.",
		"- Variants: BDD-COLLAPSE; FM-COLLAPSE; SM-COLLAPSE; FUZZ-COLLAPSE; RAG-COLLAPSE; SEC-COLLAPSE; PRIV-COLLAPSE; PROVIDER-COLLAPSE; OBS-COLLAPSE; PERF-COLLAPSE; E2E-COLLAPSE",
		"- Invariants: INV-COLLAPSE; event identity is preserved",
		"- Concrete tests: TDD-COLLAPSE-EVENT; FuzzCollapseEventPayload; TestCollapseRAGEval; TestCollapseRejectsOversizedPayload; TestCollapsePayloadRedaction; TestCollapseProviderContract; TestCollapseDispatchTelemetry; e2e/collapse_dispatch_test.go",
		"- Surfaces: SURF-COLLAPSE-DOMAIN: domain_logic [accepted]",
		"## Traceability Matrix",
		"| TDD-COLLAPSE-EVENT | SDD-COLLAPSE-EVENT-1 | BDD-COLLAPSE<br>FM-COLLAPSE<br>SM-COLLAPSE<br>FUZZ-COLLAPSE<br>RAG-COLLAPSE<br>SEC-COLLAPSE<br>PRIV-COLLAPSE<br>PROVIDER-COLLAPSE<br>OBS-COLLAPSE<br>PERF-COLLAPSE<br>E2E-COLLAPSE | INV-COLLAPSE<br>event identity is preserved | TDD-COLLAPSE-EVENT<br>FuzzCollapseEventPayload",
		"## Requirements",
		"REQ-COLLAPSE: When collapse dispatch is requested, the system shall route through CollapseEvent. (event-driven)",
		"Objective: Route collapse dispatch through a slim event helper.",
		"Success criterion: Collapse dispatch uses the helper.",
		"## Behavior Surface Inventory",
		"SURF-COLLAPSE-DOMAIN: domain_logic [in_scope] - collapse dispatch is the requested behavior",
		"Surface SURF-COLLAPSE-DOMAIN: domain_logic [in_scope] - collapse dispatch is the requested behavior obligation: TDD-COLLAPSE-EVENT",
		"SURF-COLLAPSE-TRANSPORT: api_transport [not_required] - no external transport boundary is changed",
		"## Rules & Examples",
		"RULE-COLLAPSE: Collapse dispatch delegates through the helper [requirement: REQ-COLLAPSE]",
		"Example EX-COLLAPSE: collapse request with local event payload (concrete data)",
		"Acceptance: SDD-COLLAPSE-EVENT-1: collapse dispatch routes through a slim event helper",
		"## Scenarios",
		"BDD-COLLAPSE: Given collapse dispatcher receives event payload / When dispatch runs / Then CollapseEvent helper is invoked (concrete data)",
		"TDD-COLLAPSE-EVENT: Collapse dispatch routes through helper.",
		"## E2E Journeys",
		"E2E-COLLAPSE: scripted collapse dispatch journey [scripted]",
		"E2E E2E-COLLAPSE: scripted collapse dispatch journey [scripted] script: e2e/collapse_dispatch_test.go",
		"## Invariants",
		"Coverage draft authority: standalone_provisional",
		"Coverage draft status: draft",
		"INV-COLLAPSE: helper invocation preserves event identity",
		"TDD-COLLAPSE-EVENT: required before implementation",
		"Atom CollapseEvent helper: TDD refs: TDD-COLLAPSE-EVENT",
		"Atom CollapseEvent helper: Acceptance refs: SDD-COLLAPSE-EVENT-1",
		"Atom CollapseEvent helper: Required edit paths: pkg/collapse_event.go",
		"Atom CollapseEvent helper: Required edit paths: pkg/collapse_event_test.go",
		"Atom CollapseEvent helper: Baseline: required",
		"Atom CollapseEvent helper: Expected pre-execution result: red-confirmed",
		"Atom CollapseEvent helper: Snapshot expected pre-execution results: red-confirmed",
		"## Fuzz & Property Coverage",
		"FUZZ-COLLAPSE: CollapseEvent payload parser [planned]",
		"Fuzz FUZZ-COLLAPSE: CollapseEvent payload parser [planned] invariant: event identity is preserved",
		"## Failure Modes",
		"FM-COLLAPSE: helper not invoked -> route through helper or fail test",
		"1 blocker recorded in state",
		"## Security, Privacy & RAG Evals",
		"SEC-COLLAPSE: malformed collapse payload attack=oversized payload expected=rejected without dispatch [planned]",
		"PRIV-COLLAPSE: collapse payload policy=redact raw payloads retention=structured events only [planned]",
		"Privacy PRIV-COLLAPSE: collapse payload policy=redact raw payloads retention=structured events only [planned] redaction: redaction/collapse_payload",
		"RAG RAG-COLLAPSE [planned] metric: answer_relevance=0.94",
		"RAG RAG-COLLAPSE [planned] threshold: answer_relevance=0.9",
		"## Provider Contracts",
		"PROVIDER-COLLAPSE: collapse-api -> collapse-dispatcher [planned]",
		"## State Model",
		"SM-COLLAPSE states: requested, dispatched",
		"Transition: requested --dispatch--> dispatched",
		"Status: " + featureplanner.FeatureStatePlanningRepair,
		"Candidate hash: candidate-hash",
		"## Observability",
		"OBS-COLLAPSE: collapse.dispatch at pkg/collapse_event.go [planned]",
		"Observability OBS-COLLAPSE: collapse.dispatch at pkg/collapse_event.go [planned] required field: event_id",
		"## Performance Bounds",
		"PERF-COLLAPSE: collapse dispatch <= p95 < 50ms [planned]",
		"## Test Obligations",
		"TDD-COLLAPSE-EVENT - Collapse dispatch routes through helper. (go test ./pkg) [contract]",
		"TDD-COLLAPSE-EVENT - Collapse dispatch routes through helper. (go test ./pkg)",
		"Atom CollapseEvent helper test artifact: pkg/collapse_event_test.go",
		"Atom CollapseEvent helper required test: go test ./pkg",
		"## Verification Gates",
		"VERIFY-COLLAPSE: contract (go test ./pkg)",
		"Atom CollapseEvent helper baseline: required",
		"Atom CollapseEvent helper coverage gate event: evt-tdd-collapse-event",
		"Atom CollapseEvent helper TDD snapshot: snap-collapse-event",
		"## Waivers/Open Questions",
		"Coverage question: Q-COLLAPSE: Should the helper support queued collapse events in this slice?",
		"## Atoms",
		"Route collapse dispatch through a slim event helper.",
	} {
		gomega.Expect(strings.Contains(body, want)).To(gomega.BeTrue(), fmt.Sprintf("plan tab missing %q:\n%s", want, body))
	}
	for _, rawBlob := range []string{
		"fuzz_targets",
		"rag_evals",
		"security_cases",
		"privacy_controls",
		"provider_contracts",
		"performance_bounds",
		"e2e_journeys",
		"dataset_refs",
	} {
		gomega.Expect(strings.Contains(body, rawBlob)).To(gomega.BeFalse(), fmt.Sprintf("plan tab should render structured coverage fields, not raw blob key %q:\n%s", rawBlob, body))
	}
})

func featurePlannerStructuredCoverageDraftForTest() *featureplanner.BehavioralCoverageDraft {
	return &featureplanner.BehavioralCoverageDraft{
		Authority: "standalone_provisional",
		Status:    "draft",
		Tier:      "contract_high",
		Requirements: []featureplanner.CoverageDraftRequirement{{
			ID:          "REQ-COLLAPSE",
			Pattern:     "event-driven",
			Statement:   "When collapse dispatch is requested, the system shall route through CollapseEvent.",
			Obligations: []string{"TDD-COLLAPSE-EVENT"},
		}},
		ExampleRules: []featureplanner.CoverageDraftExampleRule{{
			ID:            "RULE-COLLAPSE",
			RequirementID: "REQ-COLLAPSE",
			Rule:          "Collapse dispatch delegates through the helper",
			Examples: []featureplanner.CoverageDraftExample{{
				ID:               "EX-COLLAPSE",
				Description:      "collapse request with local event payload",
				ConcreteData:     map[string]string{"event": "collapse"},
				ConcreteDataRefs: []string{"fixtures/collapse-event.json"},
			}, {
				ID:               "EX-COLLAPSE-REJECT",
				Description:      "malformed collapse request is rejected",
				ConcreteData:     map[string]string{"event": ""},
				ConcreteDataRefs: []string{"fixtures/collapse-event-malformed.json"},
				Negative:         true,
			}},
		}},
		Scenarios: []featureplanner.CoverageDraftScenario{{
			ID:               "BDD-COLLAPSE",
			RequirementID:    "REQ-COLLAPSE",
			RuleID:           "RULE-COLLAPSE",
			Given:            []string{"collapse dispatcher receives event payload"},
			When:             []string{"dispatch runs"},
			Then:             []string{"CollapseEvent helper is invoked"},
			ConcreteData:     map[string]string{"event": "collapse"},
			ConcreteDataRefs: []string{"fixtures/collapse-scenario.json"},
			Obligations:      []string{"TDD-COLLAPSE-EVENT"},
			Tags:             []string{"contract"},
		}},
		Invariants: []featureplanner.CoverageDraftInvariant{{
			ID:                    "INV-COLLAPSE",
			Statement:             "helper invocation preserves event identity",
			PropertyBasedTestRefs: []string{"TestCollapsePreservesEventIdentity"},
			Obligations:           []string{"TDD-COLLAPSE-EVENT"},
		}},
		FailureModes: []featureplanner.CoverageDraftFailureMode{{
			ID:                 "FM-COLLAPSE",
			Mode:               "helper not invoked",
			Mitigation:         "route through helper or fail test",
			RPN:                1,
			MitigationTestRefs: []string{"TestCollapseRequiresHelper"},
			Obligations:        []string{"TDD-COLLAPSE-EVENT"},
		}},
		SurfaceInventory: []featureplanner.CoverageDraftSurfaceDecision{
			{ID: "SURF-COLLAPSE-DOMAIN", Surface: "domain_logic", Status: "in_scope", Rationale: "collapse dispatch is the requested behavior", Obligations: []string{"TDD-COLLAPSE-EVENT"}},
			{ID: "SURF-COLLAPSE-TRANSPORT", Surface: "api_transport", Status: "not_required", Rationale: "no external transport boundary is changed"},
		},
		StateMachines: []featureplanner.CoverageDraftStateMachine{{
			ID:     "SM-COLLAPSE",
			States: []string{"requested", "dispatched"},
			Transitions: []featureplanner.CoverageDraftTransition{{
				ID:       "TR-COLLAPSE-DISPATCH",
				From:     "requested",
				Event:    "dispatch",
				To:       "dispatched",
				TestRefs: []string{"TestCollapseDispatchTransition"},
			}},
			Obligations: []string{"TDD-COLLAPSE-EVENT"},
		}},
		FuzzTargets: []featureplanner.CoverageDraftFuzzTarget{{
			ID:          "FUZZ-COLLAPSE",
			Status:      "planned",
			Target:      "CollapseEvent payload parser",
			Generators:  []string{"random event payloads"},
			Invariants:  []string{"event identity is preserved"},
			TestRefs:    []string{"FuzzCollapseEventPayload"},
			Obligations: []string{"TDD-COLLAPSE-EVENT"},
		}},
		RAGEvals: []featureplanner.CoverageDraftRAGEval{{
			ID:          "RAG-COLLAPSE",
			Status:      "planned",
			DatasetRefs: []string{"evals/collapse_rag.jsonl"},
			Metrics:     map[string]float64{"answer_relevance": 0.94},
			Thresholds:  map[string]float64{"answer_relevance": 0.90},
			EvalRefs:    []string{"TestCollapseRAGEval"},
			Obligations: []string{"TDD-COLLAPSE-EVENT"},
		}},
		SecurityCases: []featureplanner.CoverageDraftSecurityCase{{
			ID:          "SEC-COLLAPSE",
			Status:      "planned",
			Risk:        "malformed collapse payload",
			Attack:      "oversized payload",
			Expected:    "rejected without dispatch",
			TestRefs:    []string{"TestCollapseRejectsOversizedPayload"},
			Obligations: []string{"TDD-COLLAPSE-EVENT"},
		}},
		PrivacyControls: []featureplanner.CoverageDraftPrivacyControl{{
			ID:            "PRIV-COLLAPSE",
			Status:        "planned",
			DataClass:     "collapse payload",
			Policy:        "redact raw payloads",
			RedactionRefs: []string{"redaction/collapse_payload"},
			Retention:     "structured events only",
			TestRefs:      []string{"TestCollapsePayloadRedaction"},
			Obligations:   []string{"TDD-COLLAPSE-EVENT"},
		}},
		ProviderContracts: []featureplanner.CoverageDraftProviderContract{{
			ID:           "PROVIDER-COLLAPSE",
			Status:       "planned",
			Provider:     "collapse-api",
			Consumer:     "collapse-dispatcher",
			ContractRefs: []string{"contracts/collapse_dispatcher.json"},
			TestRefs:     []string{"TestCollapseProviderContract"},
			Obligations:  []string{"TDD-COLLAPSE-EVENT"},
		}},
		Observability: []featureplanner.CoverageDraftObservability{{
			ID:               "OBS-COLLAPSE",
			Status:           "planned",
			Signal:           "collapse.dispatch",
			InstrumentedPath: "pkg/collapse_event.go",
			RequiredFields:   []string{"event_id", "collapse_reason"},
			TestRefs:         []string{"TestCollapseDispatchTelemetry"},
			Obligations:      []string{"TDD-COLLAPSE-EVENT"},
		}},
		PerformanceBounds: []featureplanner.CoverageDraftPerformanceBound{{
			ID:              "PERF-COLLAPSE",
			Status:          "planned",
			Operation:       "collapse dispatch",
			Bound:           "p95 < 50ms",
			MeasurementRefs: []string{"benchmarks/collapse_dispatch.json"},
			Obligations:     []string{"TDD-COLLAPSE-EVENT"},
		}},
		E2EJourneys: []featureplanner.CoverageDraftE2EJourney{{
			ID:          "E2E-COLLAPSE",
			Status:      "scripted",
			Journey:     "scripted collapse dispatch journey",
			Steps:       []string{"create collapse request", "dispatch event", "observe helper invocation"},
			ScriptRefs:  []string{"e2e/collapse_dispatch_test.go"},
			Obligations: []string{"TDD-COLLAPSE-EVENT"},
		}},
		TestObligations: []featureplanner.CoverageDraftTestObligation{{
			ID:             "TDD-COLLAPSE-EVENT",
			Type:           "contract",
			Behavior:       "Collapse dispatch routes through helper.",
			Owner:          "featureplanner",
			Command:        "go test ./pkg",
			RequiredBefore: "implementation",
		}},
		Verification: []featureplanner.CoverageDraftVerification{{
			ID:          "VERIFY-COLLAPSE",
			Gate:        "contract",
			Command:     "go test ./pkg",
			Obligations: []string{"TDD-COLLAPSE-EVENT"},
		}},
		OpenQuestions: []featureplanner.CoverageDraftOpenQuestion{{
			ID:       "Q-COLLAPSE",
			Question: "Should the helper support queued collapse events in this slice?",
		}},
	}
}

var _ = ginkgo.It("feature planner plan tab does not fabricate coverage for unmapped tdd", func() {
	plan := featurePlannerCommandStandaloneCollapsePlan()
	plan.TDDContract.Obligations = nil
	plan.Atoms[0].Summary = ""
	plan.Atoms[0].TDDObligations = []string{"TDD-MISSING"}
	plan.Atoms[0].TestFirst.ObligationRefs = []string{"TDD-MISSING"}
	plan.Atoms[0].PlannedTDDSnapshot.ObligationIDs = []string{"TDD-MISSING"}
	state := featureplanner.NewState("plan-session", "Implement CollapseEvent helper.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningRepair

	body := renderFeaturePlannerPlanTabMarkdown(state, "state.json", plan, featureplanner.RejectedPlanCandidate{})

	gomega.Expect(strings.Contains(body, "TDD-MISSING: Collapse")).To(gomega.BeFalse(), fmt.Sprintf("unmapped TDD refs should not produce behavior coverage:\n%s", body))
	for _, want := range []string{
		"## Coverage Review Summary",
		"- Concrete test cases: 0",
		"- Behaviors missing concrete test cases: 1",
		"### Behavior: TDD-MISSING",
		"- Concrete tests: No concrete test cases recorded.",
		"| TDD-MISSING | SDD-COLLAPSE-EVENT-1 |   |   |   | pkg/collapse_event_test.go | go test ./pkg | CollapseEvent helper |",
		"## Failure Modes",
		"Atom CollapseEvent helper: Unmapped TDD ref: TDD-MISSING",
		"## Invariants",
		"Atom CollapseEvent helper: TDD refs: TDD-MISSING",
		"## Test Obligations",
		"Atom CollapseEvent helper TDD ref: TDD-MISSING",
		"Atom CollapseEvent helper required test: go test ./pkg",
	} {
		gomega.Expect(strings.Contains(body, want)).To(gomega.BeTrue(), fmt.Sprintf("unmapped coverage plan missing %q:\n%s", want, body))
	}
})

var _ = ginkgo.It("feature planner plan tab counts commands separately from concrete tests", func() {
	plan := featurePlannerCommandStandaloneCollapsePlan()
	plan.TDDContract.Obligations = []featureplanner.TDDObligation{{
		ID:             "TDD-MEMORY",
		Behavior:       "Memory storage and retrieval functions work correctly with a vector store.",
		Command:        "python -m pytest tests/test_memory.py",
		RequiredBefore: "implementation",
	}}
	plan.Atoms = []featureplanner.FeatureAtomPlan{
		{
			ID:                "atom-memory-core",
			Title:             "Implement memory storage and retrieval core",
			RequiredEditPaths: []string{"src/memory.py", "tests/test_memory.py"},
			TDDObligations:    []string{"TDD-MEMORY"},
			RequiredTests:     []string{"python -m pytest tests/test_memory.py"},
			TestFirst: featureplanner.TestFirstPlan{
				ObligationRefs:    []string{"TDD-MEMORY"},
				Commands:          []string{"python -m pytest tests/test_memory.py"},
				BaselineRequired:  true,
				TestArtifactPaths: []string{"tests/test_memory.py"},
			},
		},
		{
			ID:                "atom-agent-context",
			Title:             "Integrate memory into agent context",
			RequiredEditPaths: []string{"src/agent.py", "tests/test_agent.py"},
			TDDObligations:    []string{"TDD-MEMORY"},
			RequiredTests:     []string{"python -m pytest tests/test_agent.py"},
			TestFirst: featureplanner.TestFirstPlan{
				ObligationRefs:    []string{"TDD-MEMORY"},
				Commands:          []string{"python -m pytest tests/test_agent.py"},
				BaselineRequired:  true,
				TestArtifactPaths: []string{"tests/test_agent.py"},
			},
		},
	}

	state := featureplanner.NewState("plan-session", "Build an LLM brain MVP.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningQuestion
	body := renderFeaturePlannerPlanTabMarkdown(state, "state.json", plan, featureplanner.RejectedPlanCandidate{})

	for _, want := range []string{
		"## Coverage Review Summary",
		"- Behaviors: 1",
		"- Variants: 0",
		"- Domain invariants: 0",
		"- Concrete test cases: 0",
		"- Test files: 2",
		"- Required commands: 2",
		"- Behaviors missing concrete test cases: 1",
		"### Behavior: TDD-MEMORY - Memory storage and retrieval functions work correctly with a vector store.",
		"- Atoms: Implement memory storage and retrieval core; Integrate memory into agent context",
		"- Concrete tests: No concrete test cases recorded.",
		"- Test files: tests/test_memory.py; tests/test_agent.py",
		"- Commands: python -m pytest tests/test_memory.py; python -m pytest tests/test_agent.py",
		"| TDD-MEMORY |   |   |   |   | tests/test_memory.py<br>tests/test_agent.py | python -m pytest tests/test_memory.py<br>python -m pytest tests/test_agent.py | Implement memory storage and retrieval core<br>Integrate memory into agent context |",
	} {
		gomega.Expect(strings.Contains(body, want)).To(gomega.BeTrue(), fmt.Sprintf("plan tab missing %q:\n%s", want, body))
	}
})

var _ = ginkgo.It("feature planner plan tab shows proposed files near top", func() {
	t := ginkgo.GinkgoT()
	plan := featurePlannerCommandStandaloneCollapsePlan()
	plan.Atoms = []featureplanner.FeatureAtomPlan{
		{
			ID:                "atom-memory-core",
			Title:             "Implement memory core",
			RequiredEditPaths: []string{"src/memory.py", "tests/test_memory.py"},
			AllowedEditPaths:  []string{"pyproject.toml"},
			TestFirst: featureplanner.TestFirstPlan{
				TestArtifactPaths: []string{"tests/test_memory.py"},
			},
			PlannedTDDSnapshot: featureplanner.PlannedTDDSnapshot{
				TestArtifactPaths: []string{"tests/test_memory_integration.py"},
			},
		},
	}
	state := featureplanner.NewState("plan-session", "Build an LLM brain MVP.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningQuestion

	body := renderFeaturePlannerPlanTabMarkdown(state, "state.json", plan, featureplanner.RejectedPlanCandidate{})
	for _, want := range []string{
		"## Proposed Files",
		"- Required edit paths (2):",
		"  - src/memory.py",
		"  - tests/test_memory.py",
		"- Allowed candidate edit paths (1):",
		"  - pyproject.toml",
		"- Test artifact paths (2):",
		"  - tests/test_memory.py",
		"  - tests/test_memory_integration.py",
	} {
		gomega.Expect(strings.Contains(body, want)).To(gomega.BeTrue(), fmt.Sprintf("plan tab missing proposed file entry %q:\n%s", want, body))
	}
	{
		proposedIdx, coverageIdx := strings.Index(body, "## Proposed Files"), strings.Index(body, "## Coverage Review Summary")
		gomega.Expect(proposedIdx).To(gomega.BeNumerically(">=", 0), fmt.Sprintf("proposed files should render before coverage summary:\n%s", body))
		gomega.Expect(coverageIdx).To(gomega.BeNumerically(">=", 0), fmt.Sprintf("proposed files should render before coverage summary:\n%s", body))
		gomega.Expect(proposedIdx).To(gomega.BeNumerically("<=", coverageIdx), fmt.Sprintf("proposed files should render before coverage summary:\n%s", body))
	}

	path := filepath.Join(t.TempDir(), "plan.md")
	{
		err := os.WriteFile(path, []byte(body), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write plan tab artifact: %v", err))
	}
	m := planPanelTestModel(path)
	m.planViewport = viewport.New(100, 80)
	m.renderPlanPanelContent()
	rendered := m.planViewport.View()
	for _, want := range []string{"▾ Proposed Files", "Required edit paths (2):", "src/memory.py", "tests/test_memory_integration.py"} {
		gomega.Expect(strings.Contains(rendered, want)).To(gomega.BeTrue(), fmt.Sprintf("rendered plan missing proposed file entry %q:\n%s", want, rendered))
	}
})

var _ = ginkgo.It("feature planner plan tab renders accepted product coverage without draft", func() {
	plan := featurePlannerCommandStandaloneCollapsePlan()
	plan.BehavioralCoverageDraft = nil
	plan.CoverageContract = &featureplanner.CoverageContractRef{
		ContractID:   "cov-product",
		Digest:       "sha256:product",
		EventID:      "evt-product-coverage",
		Status:       "pass",
		EffortID:     "effort-product",
		CoverageTier: "contract_high",
	}
	score := 91.0
	plan.AcceptedCoverage = &featureplanner.AcceptedBehavioralCoverage{
		Authority: "product_workflow",
		Status:    "pass",
		Tier:      "contract_high",
		Requirements: []featureplanner.AcceptedCoverageRequirement{{
			ID:        "REQ-PRODUCT",
			Pattern:   "event-driven",
			Statement: "When product dispatch is approved, the system shall execute through the accepted plan.",
		}},
		ExampleRules: []featureplanner.AcceptedCoverageExampleRule{{
			ID:            "RULE-PRODUCT",
			RequirementID: "REQ-PRODUCT",
			Rule:          "Approved dispatch uses accepted coverage.",
			Examples: []featureplanner.AcceptedCoverageExample{{
				ID:               "EX-PRODUCT",
				Description:      "approved product atom",
				ConcreteDataRefs: []string{"fixtures/product_atom.json"},
			}},
		}},
		Scenarios: []featureplanner.AcceptedCoverageScenario{{
			ID:               "BDD-PRODUCT",
			RequirementID:    "REQ-PRODUCT",
			Given:            []string{"accepted product coverage exists"},
			When:             []string{"feature planner renders the plan"},
			Then:             []string{"accepted BDD detail is visible"},
			ConcreteDataRefs: []string{"fixtures/product_atom.json"},
			Obligations:      []string{"TDD-PRODUCT"},
		}},
		Invariants: []featureplanner.AcceptedCoverageInvariant{{
			ID:                    "INV-PRODUCT",
			Statement:             "accepted coverage remains product-owned",
			PropertyBasedTestRefs: []string{"normalize plan product workflow context stamps accepted coverage authority"},
		}},
		FailureModes: []featureplanner.AcceptedCoverageFailureMode{{
			ID:                 "FM-PRODUCT",
			Mode:               "planner fabricates accepted coverage",
			Mitigation:         "reject accepted_coverage submissions",
			RPN:                80,
			MitigationTestRefs: []string{"validate plan product workflow context rejects planner authored accepted coverage"},
		}},
		HAZOPDeviations: []featureplanner.AcceptedCoverageHAZOPDeviation{{
			ID:        "HAZOP-PRODUCT",
			GuideWord: "Other Than",
			Deviation: "chat renders draft instead of accepted coverage",
			TestRefs:  []string{"feature planner plan tab renders accepted product coverage without draft"},
		}},
		FaultTreeLeaves: []featureplanner.AcceptedCoverageFaultTreeLeaf{{
			ID:       "FTA-PRODUCT",
			TopEvent: "accepted coverage hidden",
			Cause:    "adapter drops product-owned coverage detail",
			TestRefs: []string{"feature planner plan tab renders accepted product coverage without draft"},
		}},
		StateMachines: []featureplanner.AcceptedCoverageStateMachine{{
			ID:     "SM-PRODUCT",
			States: []string{"accepted", "rendered"},
			Transitions: []featureplanner.AcceptedCoverageTransition{{
				ID:       "TR-PRODUCT",
				From:     "accepted",
				Event:    "render",
				To:       "rendered",
				TestRefs: []string{"feature planner plan tab renders accepted product coverage without draft"},
			}},
		}},
		DecisionTables: []featureplanner.AcceptedCoverageDecisionTable{{
			ID:         "DT-PRODUCT",
			Conditions: []string{"product_context", "draft_present"},
			TestRefs:   []string{"feature planner plan tab renders accepted product coverage without draft"},
		}},
		BoundaryValues: []featureplanner.AcceptedCoverageBoundaryValue{{
			ID:        "BV-PRODUCT",
			Parameter: "coverage_sections",
			Values:    []string{"0", "1", "9"},
			TestRefs:  []string{"feature planner plan tab renders accepted product coverage without draft"},
		}},
		TestObligations: []featureplanner.AcceptedCoverageTestObligation{{
			ID:       "TDD-PRODUCT",
			Type:     "contract",
			Behavior: "accepted product coverage renders in the plan tab",
			Command:  "go test ./internal/surfaces/tui/app -ginkgo.focus='feature planner plan tab renders accepted product coverage without draft' -ginkgo.fail-on-empty",
		}},
		Verification: []featureplanner.AcceptedCoverageVerification{{
			ID:      "VERIFY-PRODUCT",
			Gate:    "contract",
			Command: "go test ./internal/surfaces/tui/app",
		}},
		PostMortems: []featureplanner.AcceptedCoveragePostMortem{{
			ID:               "PM-PRODUCT",
			Incident:         "accepted coverage was previously hidden",
			CatchingTestRefs: []string{"feature planner plan tab renders accepted product coverage without draft"},
		}},
		FormalModels: []featureplanner.AcceptedCoverageFormalModel{{
			ID:               "FORMAL-PRODUCT",
			Model:            "accepted_coverage_authority_state",
			VerificationRefs: []string{"models/accepted_coverage_authority.tla"},
		}},
		FuzzTargets: []featureplanner.AcceptedCoverageFuzzTarget{{
			ID:          "FUZZ-PRODUCT",
			Status:      "covered",
			Target:      "accepted coverage parser",
			Generators:  []string{"coverage contract mutations"},
			Invariants:  []string{"accepted authority is preserved"},
			TestRefs:    []string{"FuzzAcceptedCoverageParser"},
			Obligations: []string{"TDD-PRODUCT"},
		}},
		RAGEvals: []featureplanner.AcceptedCoverageRAGEval{{
			ID:          "RAG-PRODUCT",
			Status:      "measured",
			DatasetRefs: []string{"evals/product_coverage_rag.jsonl"},
			Metrics:     map[string]float64{"faithfulness": 0.97},
			Thresholds:  map[string]float64{"faithfulness": 0.95},
			EvalRefs:    []string{"TestProductCoverageRAGEval"},
			Obligations: []string{"TDD-PRODUCT"},
		}},
		SecurityCases: []featureplanner.AcceptedCoverageSecurityCase{{
			ID:          "SEC-PRODUCT",
			Status:      "tested",
			Risk:        "coverage authority injection",
			Attack:      "planner-authored accepted coverage",
			Expected:    "rejected",
			TestRefs:    []string{"validate plan product workflow context rejects planner authored accepted coverage"},
			Obligations: []string{"TDD-PRODUCT"},
		}},
		PrivacyControls: []featureplanner.AcceptedCoveragePrivacyControl{{
			ID:            "PRIV-PRODUCT",
			Status:        "tested",
			DataClass:     "product workflow prompt context",
			Policy:        "render structured fields only",
			RedactionRefs: []string{"redaction/product_workflow_context"},
			Retention:     "no raw prompt blob",
			TestRefs:      []string{"feature planner plan tab renders accepted product coverage without draft"},
			Obligations:   []string{"TDD-PRODUCT"},
		}},
		ProviderContracts: []featureplanner.AcceptedCoverageProviderContract{{
			ID:           "PROVIDER-PRODUCT",
			Status:       "tested",
			Provider:     "product-workflow",
			Consumer:     "feature-planner",
			ContractRefs: []string{"contracts/product_featureplanner_coverage.json"},
			TestRefs:     []string{"carries implementation grounding and accepted coverage"},
			Obligations:  []string{"TDD-PRODUCT"},
		}},
		Observability: []featureplanner.AcceptedCoverageObservability{{
			ID:               "OBS-PRODUCT",
			Status:           "tested",
			Signal:           "featureplanner.coverage.accepted",
			InstrumentedPath: "internal/surfaces/tui/app/feature_planner_plan_tab.go",
			RequiredFields:   []string{"contract_id", "coverage_tier"},
			TestRefs:         []string{"feature planner plan tab renders accepted product coverage without draft"},
			Obligations:      []string{"TDD-PRODUCT"},
		}},
		PerformanceBounds: []featureplanner.AcceptedCoveragePerformanceBound{{
			ID:              "PERF-PRODUCT",
			Status:          "tested",
			Operation:       "plan tab coverage render",
			Bound:           "under 100ms for accepted coverage fixture",
			MeasurementRefs: []string{"benchmarks/featureplanner_plan_tab.json"},
			Obligations:     []string{"TDD-PRODUCT"},
		}},
		E2EJourneys: []featureplanner.AcceptedCoverageE2EJourney{{
			ID:          "E2E-PRODUCT",
			Status:      "scripted",
			Journey:     "accepted coverage reaches feature planner plan tab",
			Steps:       []string{"load product coverage contract", "build feature planner plan", "render plan tab"},
			ScriptRefs:  []string{"internal/surfaces/tui/app/feature_planner_product_test.go"},
			Obligations: []string{"TDD-PRODUCT"},
		}},
		SurfaceInventory: []featureplanner.AcceptedCoverageSurfaceDecision{{
			ID:          "SURF-PRODUCT-DOMAIN",
			Surface:     "domain_logic",
			Status:      "in_scope",
			Rationale:   "accepted product behavior is in scope",
			Obligations: []string{"TDD-PRODUCT"},
		}, {
			ID:        "SURF-PRODUCT-TRANSPORT",
			Surface:   "api_transport",
			Status:    "not_required",
			Rationale: "no external transport boundary is changed",
		}},
		Mutation: featureplanner.AcceptedCoverageMutation{Requirement: "required", MinScore: 80, Score: &score},
	}
	state := featureplanner.NewState("plan-session", "Implement product-backed coverage.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStateReady

	body := renderFeaturePlannerPlanTabMarkdown(state, "state.json", plan, featureplanner.RejectedPlanCandidate{})

	sharedSections := featureplanner.AcceptedCoveragePresentation(plan.AcceptedCoverage)
	for _, section := range sharedSections {
		gomega.Expect(strings.Contains(body, "## "+section.Label)).To(gomega.BeTrue(), fmt.Sprintf("plan tab missing shared accepted coverage section label %q:\n%s", section.Label, body))
	}
	for _, sharedRow := range []string{
		"REQ-PRODUCT: When product dispatch is approved, the system shall execute through the accepted plan. (event-driven)",
		"BDD-PRODUCT: Given accepted product coverage exists / When feature planner renders the plan / Then accepted BDD detail is visible (concrete data)",
		"Security SEC-PRODUCT: coverage authority injection attack=planner-authored accepted coverage expected=rejected [tested] test: validate plan product workflow context rejects planner authored accepted coverage",
	} {
		gomega.Expect([][]string{
			featureplanner.CoverageRowsBySection(sharedSections, featureplanner.CoverageSectionRequirements),
			featureplanner.CoverageRowsBySection(sharedSections, featureplanner.CoverageSectionScenarios),
			featureplanner.CoverageRowsBySection(sharedSections, featureplanner.CoverageSectionSecurityPrivacyRAGEvals),
		}).To(gomega.ContainElement(gomega.ContainElement(sharedRow)), fmt.Sprintf("shared projection missing row %q", sharedRow))
		gomega.Expect(strings.Contains(body, sharedRow)).To(gomega.BeTrue(), fmt.Sprintf("plan tab missing shared accepted coverage row %q:\n%s", sharedRow, body))
	}

	for _, want := range []string{
		"Accepted coverage authority: product_workflow",
		"- Behavior surfaces: 2",
		"- In-scope surfaces: 1",
		"- In-scope surfaces missing TDD obligations: 0",
		"- Open surface questions: 0",
		"- Surfaces: SURF-PRODUCT-DOMAIN: domain_logic [accepted]",
		"REQ-PRODUCT: When product dispatch is approved",
		"## Behavior Surface Inventory",
		"SURF-PRODUCT-DOMAIN: domain_logic [in_scope] - accepted product behavior is in scope",
		"Surface SURF-PRODUCT-DOMAIN: domain_logic [in_scope] - accepted product behavior is in scope obligation: TDD-PRODUCT",
		"SURF-PRODUCT-TRANSPORT: api_transport [not_required] - no external transport boundary is changed",
		"BDD-PRODUCT: Given accepted product coverage exists / When feature planner renders the plan / Then accepted BDD detail is visible (concrete data)",
		"E2E-PRODUCT: accepted coverage reaches feature planner plan tab [scripted]",
		"E2E E2E-PRODUCT: accepted coverage reaches feature planner plan tab [scripted] script: internal/surfaces/tui/app/feature_planner_product_test.go",
		"INV-PRODUCT: accepted coverage remains product-owned",
		"FUZZ-PRODUCT: accepted coverage parser [covered]",
		"Fuzz FUZZ-PRODUCT: accepted coverage parser [covered] test: FuzzAcceptedCoverageParser",
		"FM-PRODUCT: planner fabricates accepted coverage -> reject accepted_coverage submissions (RPN 80)",
		"HAZOP-PRODUCT [Other Than]",
		"FTA-PRODUCT: adapter drops product-owned coverage detail [top event: accepted coverage hidden]",
		"SEC-PRODUCT: coverage authority injection attack=planner-authored accepted coverage expected=rejected [tested]",
		"PRIV-PRODUCT: product workflow prompt context policy=render structured fields only retention=no raw prompt blob [tested]",
		"Privacy PRIV-PRODUCT: product workflow prompt context policy=render structured fields only retention=no raw prompt blob [tested] redaction: redaction/product_workflow_context",
		"RAG RAG-PRODUCT [measured] metric: faithfulness=0.97",
		"RAG RAG-PRODUCT [measured] threshold: faithfulness=0.95",
		"PROVIDER-PRODUCT: product-workflow -> feature-planner [tested]",
		"SM-PRODUCT states: accepted, rendered",
		"DT-PRODUCT conditions: product_context, draft_present",
		"BV-PRODUCT: coverage_sections values: 0, 1, 9",
		"OBS-PRODUCT: featureplanner.coverage.accepted at internal/surfaces/tui/app/feature_planner_plan_tab.go [tested]",
		"Observability OBS-PRODUCT: featureplanner.coverage.accepted at internal/surfaces/tui/app/feature_planner_plan_tab.go [tested] required field: contract_id",
		"PERF-PRODUCT: plan tab coverage render <= under 100ms for accepted coverage fixture [tested]",
		"TDD-PRODUCT - accepted product coverage renders in the plan tab",
		"VERIFY-PRODUCT: contract",
		"Post-mortem coverage: PM-PRODUCT: accepted coverage was previously hidden",
		"Formal coverage: FORMAL-PRODUCT: accepted_coverage_authority_state",
		"Mutation: required min=80 score=91.0",
	} {
		gomega.Expect(strings.Contains(body, want)).To(gomega.BeTrue(), fmt.Sprintf("accepted coverage plan tab missing %q:\n%s", want, body))
	}
	gomega.Expect(strings.Contains(body, "Coverage draft authority")).To(gomega.BeFalse(), fmt.Sprintf("product accepted coverage render must not depend on draft coverage:\n%s", body))
	for _, rawBlob := range []string{
		"fuzz_targets",
		"rag_evals",
		"security_cases",
		"privacy_controls",
		"provider_contracts",
		"performance_bounds",
		"e2e_journeys",
		"dataset_refs",
	} {
		gomega.Expect(strings.Contains(body, rawBlob)).To(gomega.BeFalse(), fmt.Sprintf("accepted coverage plan tab should render typed fields, not raw blob key %q:\n%s", rawBlob, body))
	}
})

var _ = ginkgo.It("feature planner plan tab rejects summary only candidate", func() {
	state := featureplanner.NewState("plan-session", "Implement CollapseEvent helper.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStateReady
	state.Plan = featureplanner.FeaturePlan{
		Summary: "RAW_MODEL_SUMMARY this is not a displayable plan contract by itself.",
	}

	_, _, ok := featurePlannerPlanTabCandidate(state)
	gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprint("summary-only plan should not be displayable in the Plan tab"))
})

var _ = ginkgo.It("feature planner plan tab renders canonical artifact and approval", func() {
	plan := featurePlannerCommandStandaloneCollapsePlan()
	plan.Artifact = featureplanner.FeaturePlanArtifact{
		ArtifactID:           "feature-collapse-event:feature-plan",
		Revision:             3,
		Digest:               "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		WorkspaceFingerprint: "sha256:workspace",
		CreatedAt:            "2026-06-22T19:00:00Z",
		UpdatedAt:            "2026-06-22T19:01:00Z",
	}
	plan.Approval = featureplanner.FeaturePlanApproval{
		Status:               "approved",
		Mode:                 "harness_policy",
		EventID:              "approval-plan-session-rev-3",
		Actor:                "harness",
		Reason:               "durable_plan_contract_passed",
		ApprovedAt:           "2026-06-22T19:01:00Z",
		PlanRevision:         3,
		PlanDigest:           plan.Artifact.Digest,
		WorkspaceFingerprint: plan.Artifact.WorkspaceFingerprint,
	}
	state := featureplanner.NewState("plan-session", "Implement CollapseEvent helper.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStateDispatching
	state.VerificationClosures = []featureplanner.VerificationClosure{{
		EventID:              "verification-feature-collapse-event-atom-1-r3",
		AtomID:               "atom-1",
		Status:               "passed",
		PlanRevision:         3,
		PlanDigest:           plan.Artifact.Digest,
		PlanArtifactID:       plan.Artifact.ArtifactID,
		ApprovalEventID:      plan.Approval.EventID,
		ChangedPaths:         []string{"pkg/collapse_event.go", "pkg/collapse_event_test.go"},
		RequiredTestResults:  []featureplanner.VerificationCommandResult{{Command: "go test ./pkg", Status: "passed", Passed: true}},
		TDDCommandResults:    []featureplanner.VerificationCommandResult{{Command: "go test ./pkg", Status: "passed", Passed: true, ObligationRefs: []string{"TDD-1"}}},
		Verification:         []featureplanner.VerificationCheckResult{{Kind: "verification", Command: "go test ./pkg", Status: "ok"}},
		WorkspaceFingerprint: plan.Artifact.WorkspaceFingerprint,
	}}
	state.ReleaseReadiness = featureplanner.ReleaseReadinessDecision{
		Status:                      "ready",
		EventID:                     "release-feature-collapse-event-feature-plan-r3",
		PlanArtifactID:              plan.Artifact.ArtifactID,
		PlanRevision:                3,
		PlanDigest:                  plan.Artifact.Digest,
		WorkspaceFingerprint:        plan.Artifact.WorkspaceFingerprint,
		ApprovalEventID:             plan.Approval.EventID,
		VerificationClosureEventIDs: []string{"verification-feature-collapse-event-atom-1-r3"},
	}

	body := renderFeaturePlannerPlanTabMarkdown(state, "state.json", plan, featureplanner.RejectedPlanCandidate{})

	for _, want := range []string{
		"Artifact: feature-collapse-event:feature-plan",
		"Revision: 3",
		"Digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"Workspace: sha256:workspace",
		"Approval: approved",
		"Approval mode: harness_policy",
		"Approval event: approval-plan-session-rev-3",
		"## Verification Closures",
		"atom-1: passed (plan r3)",
		"Closure: verification-feature-collapse-event-atom-1-r3",
		"Changed paths: pkg/collapse_event.go, pkg/collapse_event_test.go",
		"Commands: go test ./pkg [passed]",
		"Checks: verification `go test ./pkg` [ok]",
		"## Release Readiness",
		"Status: ready",
		"Decision: release-feature-collapse-event-feature-plan-r3",
		"Verification closures: verification-feature-collapse-event-atom-1-r3",
	} {
		gomega.Expect(strings.Contains(body, want)).To(gomega.BeTrue(), fmt.Sprintf("plan tab missing %q:\n%s", want, body))
	}
})

var _ = ginkgo.It("feature planner plan tab does not mix rejected candidate with stale release state", func() {
	accepted := featurePlannerCommandStandaloneCollapsePlan()
	accepted.Artifact = featureplanner.FeaturePlanArtifact{
		ArtifactID:           "accepted:feature-plan",
		Revision:             1,
		Digest:               "sha256:accepted",
		WorkspaceFingerprint: "sha256:workspace",
	}
	rejected := featurePlannerCommandStandaloneCollapsePlan()
	rejected.Summary = "Rejected replacement candidate"

	state := featureplanner.NewState("plan-session", "Implement CollapseEvent helper.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningRepair
	state.VerificationClosures = []featureplanner.VerificationClosure{{
		EventID:        "verification-accepted-r1",
		AtomID:         "atom-1",
		Status:         "passed",
		PlanRevision:   1,
		PlanDigest:     accepted.Artifact.Digest,
		PlanArtifactID: accepted.Artifact.ArtifactID,
	}}
	state.ReleaseReadiness = featureplanner.ReleaseReadinessDecision{
		Status:                      "ready",
		EventID:                     "release-accepted-r1",
		PlanArtifactID:              accepted.Artifact.ArtifactID,
		PlanRevision:                1,
		PlanDigest:                  accepted.Artifact.Digest,
		WorkspaceFingerprint:        accepted.Artifact.WorkspaceFingerprint,
		VerificationClosureEventIDs: []string{"verification-accepted-r1"},
	}

	body := renderFeaturePlannerPlanTabMarkdown(state, "state.json", rejected, featureplanner.RejectedPlanCandidate{Plan: rejected, PlanHash: "sha256:rejected"})
	gomega.Expect(strings.Contains(body, "Candidate: sha256:rejected")).To(gomega.BeTrue(), fmt.Sprintf("rejected candidate did not render:\n%s", body))
	for _, stale := range []string{"## Verification Closures", "## Release Readiness", "release-accepted-r1", "verification-accepted-r1"} {
		gomega.Expect(strings.Contains(body, stale)).To(gomega.BeFalse(), fmt.Sprintf("plan tab mixed rejected candidate with stale lifecycle marker %q:\n%s", stale, body))
	}
})

var _ = ginkgo.It("feature planner plan tab rendered panel shows structured coverage only", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	plan := featurePlannerCommandStandaloneCollapsePlan()
	plan.Summary = "RAW_MODEL_SUMMARY dense model-facing paragraph that should not be rendered in the plan panel."
	state := featureplanner.NewState("plan-session", "Implement CollapseEvent helper.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningRepair

	path := filepath.Join(root, "plan.md")
	body := renderFeaturePlannerPlanTabMarkdown(state, "state.json", plan, featureplanner.RejectedPlanCandidate{
		Diagnostic: "RAW_DIAGNOSTIC model-facing validation dump that should stay out of the plan panel.",
	})
	{
		err := os.WriteFile(path, []byte(body), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write plan tab artifact: %v", err))
	}

	m := planPanelTestModel(path)
	m.planViewport = viewport.New(120, 160)
	m.renderPlanPanelContent()

	rendered := m.planViewport.View()
	for _, hidden := range []string{"RAW_MODEL_SUMMARY", "RAW_DIAGNOSTIC", "Diagnostic:"} {
		gomega.Expect(strings.Contains(rendered, hidden)).To(gomega.BeFalse(), fmt.Sprintf("rendered plan leaked raw display text %q:\n%s", hidden, rendered))
	}
	for _, want := range []string{
		"▾ Feature Planner Candidate",
		"▾ Coverage Review Summary",
		"Concrete test cases: 0",
		"▾ Behavior Review",
		"▾ Behavior: TDD-COLLAPSE-EVENT - Collapse dispatch routes through helper.",
		"Concrete tests: No concrete test cases recorded.",
		"▾ Traceability Matrix",
		"▾ Requirements",
		"▾ Test Obligations",
		"TDD-COLLAPSE-EVENT - Collapse dispatch routes through helper.",
		"Atom CollapseEvent helper test artifact:",
		"Atom CollapseEvent helper required test:",
		"go test ./pkg",
	} {
		gomega.Expect(strings.Contains(rendered, want)).To(gomega.BeTrue(), fmt.Sprintf("rendered plan missing %q:\n%s", want, rendered))
	}

	coverageIdx := planSelectableRowIndex(m.currentPlanSelectableRows(), "Test Obligations")
	gomega.Expect(coverageIdx < 0).To(gomega.BeFalse(), fmt.Sprintf("Test Obligations heading should be selectable:\n%s", rendered))

	clickRow := -1
	for i, line := range strings.Split(m.planViewport.View(), "\n") {
		if strings.Contains(line, "Test Obligations") {
			clickRow = i
			break
		}
	}
	gomega.Expect(clickRow < 0).To(gomega.BeFalse(), fmt.Sprintf("Test Obligations heading should be clickable:\n%s", rendered))
	m.viewport.Width = 80
	{
		_, handled := m.handleMouse(tea.MouseMsg{
			X:      m.panelStartX() + 1,
			Y:      3 + clickRow,
			Button: tea.MouseButtonLeft,
			Action: tea.MouseActionPress,
		})
		gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("mouse click should collapse generated Test Obligations section"))
	}
	mouseCollapsed := m.planViewport.View()
	gomega.Expect(mouseCollapsed).To(gomega.ContainSubstring("▸ Test Obligations"), fmt.Sprintf("generated Test Obligations section did not collapse on click:\n%s", mouseCollapsed))
	gomega.Expect(mouseCollapsed).To(gomega.Not(gomega.ContainSubstring("Atom CollapseEvent helper test artifact:")), fmt.Sprintf("generated Test Obligations section did not collapse on click:\n%s", mouseCollapsed))
	{
		_, handled := m.handleMouse(tea.MouseMsg{
			X:      m.panelStartX() + 1,
			Y:      3 + clickRow,
			Button: tea.MouseButtonLeft,
			Action: tea.MouseActionPress,
		})
		gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("second mouse click should expand generated Test Obligations section"))
	}
	{
		expanded := m.planViewport.View()
		gomega.Expect(expanded).To(gomega.ContainSubstring("▾ Test Obligations"), fmt.Sprintf("generated Test Obligations section did not expand on second click:\n%s", expanded))
		gomega.Expect(expanded).To(gomega.ContainSubstring("Atom CollapseEvent helper test artifact:"), fmt.Sprintf("generated Test Obligations section did not expand on second click:\n%s", expanded))
	}

	m.planUI.cursor = coverageIdx
	{
		handled, _ := m.handlePlanPanelKey(tea.KeyMsg{Type: tea.KeyEnter})
		gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("Enter should collapse generated Test Obligations section"))
	}

	collapsed := m.planViewport.View()
	gomega.Expect(collapsed).To(gomega.ContainSubstring("▸ Test Obligations"), fmt.Sprintf("generated Test Obligations section did not collapse cleanly:\n%s", collapsed))
	gomega.Expect(collapsed).To(gomega.Not(gomega.ContainSubstring("Atom CollapseEvent helper test artifact:")), fmt.Sprintf("generated Test Obligations section did not collapse cleanly:\n%s", collapsed))
})

var _ = ginkgo.It("run prompt once accepted source question leaves unstructured chat interactive", func() {
	t := ginkgo.GinkgoT()
	projectRoot := t.TempDir()
	cfg := config.Default()
	cfg.Chat.AutoStartServer = false
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.MaxToolSteps = 0
	cfg.Chat.ProjectRoot = projectRoot
	cfg.Workspace.Root = projectRoot
	cfg.Workspace.CWD = projectRoot
	cfg.MCP = nil

	state := featureplanner.NewState("accepted-source-session", "Implement CollapseEvent helper.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningQuestion
	state.OpenQuestions = []featureplanner.PlannerQuestion{{
		ID:                "question-source-facts",
		Status:            "open",
		QuestionSummary:   "Provide accepted source facts for CollapseEvent",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "Concrete CollapseEvent helper usage requires source-localized signature evidence.",
		RequestedDecision: "Provide accepted source facts for the current CollapseEvent signature.",
		AuthorityRequired: "accepted_source",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     []string{"pkg/collapse_event.go"},
		AffectedFields:    []string{"source_localization"},
	}}
	{
		_, err := featureplanner.SaveState(projectRoot, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveState: %v", err))
	}

	client := &scriptedLLMRecorder{responses: []string{"We can discuss the accepted-source blocker before you provide structured evidence."}}
	m := New(cfg)
	defer m.Close()
	m.client = client.Mock(t)
	m.ready = true
	m.directFeaturePlannerSessionID = state.SessionID

	answer, err := m.RunPromptOnce(context.Background(), "What source facts do you need?")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("RunPromptOnce: %v", err))
	gomega.Expect(strings.Contains(answer, "accepted-source blocker")).To(gomega.BeTrue(), fmt.Sprintf("unexpected chat answer:\n%s", answer))
	gomega.Expect(len(client.messages)).To(gomega.Equal(1), fmt.Sprintf("unstructured accepted-source question did not route through normal chat: %#v", client.messages))
	gomega.Expect(featurePlannerRequestMessagesContain(client.messages[0], "What source facts do you need?")).To(gomega.BeTrue(), fmt.Sprintf("unstructured accepted-source question did not route through normal chat: %#v", client.messages))

	afterChat := loadFeaturePlannerCommandStateFile(t, filepath.Join(projectRoot, ".idiom", "feature-planner", state.SessionID, "state.json"))
	gomega.Expect(len(afterChat.OpenQuestions)).To(gomega.Equal(1), fmt.Sprintf("unstructured chat must not answer accepted-source planner question: %+v", afterChat.OpenQuestions))
	gomega.Expect(afterChat.OpenQuestions[0].Status).To(gomega.Equal("open"), fmt.Sprintf("unstructured chat must not answer accepted-source planner question: %+v", afterChat.OpenQuestions))
	gomega.Expect(len(afterChat.AuthorityDeltas)).To(gomega.Equal(0), fmt.Sprintf("unstructured chat must not create accepted-source authority deltas: %+v", afterChat.AuthorityDeltas))
})

var _ = ginkgo.It("feature planner tui accepted source plan revision question resumes planner", func() {
	t := ginkgo.GinkgoT()
	projectRoot := t.TempDir()
	cfg := featurePlannerTestConfig(projectRoot)
	state := saveAcceptedSourceFeaturePlannerQuestionState(t, projectRoot, "accepted-source-revision-session")

	client := &scriptedLLMRecorder{responses: []string{
		`{"tool":"ask_planner_question","question_summary":"Confirm persistent memory and multi-repo plan additions","blocking_claim":"The operator asked to add more detail to the plan, so the revised MVP boundary needs confirmation before approval.","requested_decision":"Confirm the plan should add persistent memory storage, repository-scoped namespaces, and explicit multi-repo isolation tests.","question_kind":"external_answer_required","authority_required":"operator_approval","required_before":"plan_approval","affected_paths":["llm_brain/query_handler.py","tests/test_query_handler.py"],"affected_fields":["requirements_scope","test_obligations"]}`,
	}}
	m := newReadyFeaturePlannerTUITestModel(t, cfg, client, state.SessionID, false)

	revision := "Can you add more detail to the plan for persistent memory and multi-repo support?"
	answer, err := m.RunPromptOnce(context.Background(), revision)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("RunPromptOnce: %v", err))
	gomega.Expect(len(client.messages)).To(gomega.Equal(1), fmt.Sprintf("plan revision question should route to feature-planner exactly once, got %#v", client.messages))
	gomega.Expect(featurePlannerRequestMessagesContain(client.messages[0], "Operator plan revision instruction:")).To(gomega.BeTrue(), fmt.Sprintf("question-form plan revision did not reach feature-planner resume prompt: %#v", client.messages))
	gomega.Expect(featurePlannerRequestMessagesContain(client.messages[0], revision)).To(gomega.BeTrue(), fmt.Sprintf("question-form plan revision did not reach feature-planner resume prompt: %#v", client.messages))
	gomega.Expect(answer).To(gomega.Not(gomega.ContainSubstring("I could not produce a supported final answer")), fmt.Sprintf("plan revision should not fall through to direct-answer completion gate:\n%s", answer))
	gomega.Expect(answer).To(gomega.Not(gomega.ContainSubstring("source_grounding_unmet")), fmt.Sprintf("plan revision should not fall through to direct-answer completion gate:\n%s", answer))
	gomega.Expect(strings.Contains(answer, "Confirm persistent memory and multi-repo plan additions")).To(gomega.BeTrue(), fmt.Sprintf("revised planner question not surfaced:\n%s", answer))

	after := loadFeaturePlannerCommandStateFile(t, filepath.Join(projectRoot, ".idiom", "feature-planner", state.SessionID, "state.json"))
	gomega.Expect(len(after.OpenQuestions)).To(gomega.Equal(2), fmt.Sprintf("question-form revision should answer prior question and open revised planner question: %+v", after.OpenQuestions))
	gomega.Expect(after.OpenQuestions[0].Status).To(gomega.Equal("answered"), fmt.Sprintf("question-form revision should answer prior question and open revised planner question: %+v", after.OpenQuestions))
	gomega.Expect(after.OpenQuestions[1].Status).To(gomega.Equal("open"), fmt.Sprintf("question-form revision should answer prior question and open revised planner question: %+v", after.OpenQuestions))
	gomega.Expect(after.OpenQuestions[0].Answer).To(gomega.Equal(revision), fmt.Sprintf("prior question did not retain revision request: %+v", after.OpenQuestions[0]))
	gomega.Expect(len(after.AuthorityDeltas)).To(gomega.Equal(0), fmt.Sprintf("plan revision must not create accepted-source authority deltas: %+v", after.AuthorityDeltas))
})

var _ = ginkgo.It("feature planner tui direct accepted source answer carries authority into resume prompt", func() {
	t := ginkgo.GinkgoT()
	projectRoot := t.TempDir()
	writeFile(t, projectRoot, "go.mod", "module example.com/tui\n")
	plan := featurePlannerCommandStandaloneCollapsePlan()
	plan.Atoms[0].SDDRefs = nil

	client := &scriptedLLMRecorder{responses: []string{
		featurePlannerCommandAcceptedSourceQuestionJSON(t),
		featurePlannerCommandPlanJSON(t, plan),
	}}
	m := newDetachedPromptExecutionSession(context.Background(), &Model{
		client: client.Mock(t),
		cfg: config.Config{
			Chat:      config.Chat{ProjectRoot: projectRoot},
			Workspace: config.Workspace{Root: projectRoot, CWD: projectRoot},
		},
		workspace: workspace.NewManager(config.Workspace{Root: projectRoot, CWD: projectRoot}),
	})

	answer, err := m.runFeaturePlannerCommandOnce(context.Background(), "Implement CollapseEvent helper.")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runFeaturePlannerCommandOnce: %v", err))
	for _, want := range []string{
		"feature-planner: " + featureplanner.FeatureStatePlanningQuestion,
		"open questions: 1",
	} {
		gomega.Expect(strings.Contains(answer, want)).To(gomega.BeTrue(), fmt.Sprintf("accepted-source question summary missing %q:\n%s", want, answer))
	}
	for _, forbidden := range []string{
		"Feature planner needs your input before it can continue.",
		"answer_format:",
		"Provide accepted source facts",
	} {
		gomega.Expect(strings.Contains(answer, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("accepted-source question leaked internal prompt %q:\n%s", forbidden, answer))
	}

	sessionID := m.directFeaturePlannerSessionID
	typedAnswer := `{"answer":"Accepted source fact approved for CollapseEvent helper usage.","answer_source_kind":"accepted_source","evidence_refs":["pkg/collapse_event.go"],"affected_paths":["pkg/collapse_event.go"],"affected_fields":["source_localization"],"source_facts":[{"path":"pkg/collapse_event.go","symbol":"NewCollapseEvent","signature":"CollapseEvent NewCollapseEvent(EntityID id)"}]}`
	answer, err = m.runDirectFeaturePlannerAnswerOnce(context.Background(), typedAnswer)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runDirectFeaturePlannerAnswerOnce: %v", err))
	gomega.Expect(strings.Contains(answer, "feature-planner: "+featureplanner.FeatureStatePlanningRepair)).To(gomega.BeTrue(), fmt.Sprintf("expected planning repair after resumed invalid plan, got:\n%s", answer))

	gomega.Expect(len(client.messages) < 2).To(gomega.BeFalse(), fmt.Sprintf("expected resumed planner prompt, got %#v", client.messages))
	resumePrompt := client.messages[1][len(client.messages[1])-1].Content
	for _, want := range []string{
		"User request:\nImplement CollapseEvent helper.",
		"Accepted source authority constraints:",
		"signature: CollapseEvent NewCollapseEvent(EntityID id)",
		"Use only these accepted source facts for helper signatures and source-localized call shapes.",
	} {
		gomega.Expect(strings.Contains(resumePrompt, want)).To(gomega.BeTrue(), fmt.Sprintf("resumed planner prompt missing %q:\n%s", want, resumePrompt))
	}
	gomega.Expect(strings.Contains(resumePrompt, "User request:\n"+typedAnswer)).To(gomega.BeFalse(), fmt.Sprintf("resumed planner prompt used typed answer as objective:\n%s", resumePrompt))

	state := loadFeaturePlannerCommandStateFile(t, filepath.Join(projectRoot, ".idiom", "feature-planner", sessionID, "state.json"))
	gomega.Expect(len(state.AuthorityDeltas)).To(gomega.Equal(1), fmt.Sprintf("accepted-source authority delta was not preserved: %+v", state.AuthorityDeltas))
	gomega.Expect(state.AuthorityDeltas[0].AuthorityKind).To(gomega.Equal("accepted_source"), fmt.Sprintf("accepted-source authority delta was not preserved: %+v", state.AuthorityDeltas))
	hasSignatureFact := false
	for _, fact := range state.AuthorityDeltas[0].SourceFacts {
		if fact.Signature == "CollapseEvent NewCollapseEvent(EntityID id)" {
			hasSignatureFact = true
			break
		}
	}
	gomega.Expect(hasSignatureFact).To(gomega.BeTrue(), fmt.Sprintf("accepted-source source facts were not preserved: %+v", state.AuthorityDeltas[0]))
	gomega.Expect(len(state.OpenQuestions)).To(gomega.Equal(1), fmt.Sprintf("accepted-source question was not answered durably: %+v", state.OpenQuestions))
	gomega.Expect(state.OpenQuestions[0].Status).To(gomega.Equal("answered"), fmt.Sprintf("accepted-source question was not answered durably: %+v", state.OpenQuestions))
	gomega.Expect(state.OpenQuestions[0].AnswerSourceKind).To(gomega.Equal("accepted_source"), fmt.Sprintf("accepted-source question was not answered durably: %+v", state.OpenQuestions))
})

var _ = ginkgo.It("preserves promoted product coverage through resume and plan display", ginkgo.Label("e2e"), func() {
	t := ginkgo.GinkgoT()
	projectRoot := t.TempDir()
	writeFixture := func(relPath string, body string) {
		path := filepath.Join(projectRoot, filepath.FromSlash(relPath))
		gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(path, []byte(body), 0o644)).To(gomega.Succeed())
	}
	writeFixture("go.mod", "module example.com/tui\n")
	writeFixture("pkg/collapse_event.go", "package pkg\n")
	writeFixture("pkg/collapse_event_test.go", "package pkg\n")

	score := 90.0
	contract := productworkflow.BehavioralCoverageContract{
		SchemaVersion: "idiom.behavioral_coverage.v1",
		ContractID:    "cov-collapse-contract",
		EffortID:      "collapse-effort",
		TaskID:        "collapse-task",
		CoverageTier:  "behavior_standard",
		EARS: []productworkflow.EARSRequirementCoverage{{
			ID:          "REQ-COLLAPSE",
			Pattern:     "event-driven",
			Statement:   "When collapse dispatch runs, the system shall route through CollapseEvent.",
			Status:      productworkflow.BehavioralCoverageStatusAccepted,
			MappedRefs:  []string{"BDD-COLLAPSE"},
			Obligations: []string{"TDD-COLLAPSE-EVENT"},
		}},
		ExampleRules: []productworkflow.ExampleRuleCoverage{{
			ID:            "RULE-COLLAPSE",
			RequirementID: "REQ-COLLAPSE",
			Rule:          "Collapse dispatch uses the helper.",
			Examples: []productworkflow.ConcreteExampleCoverage{
				{ID: "EX-COLLAPSE-1", Description: "collapse accepted", ConcreteDataRefs: []string{"fixtures/collapse.json"}},
				{ID: "EX-COLLAPSE-2", Description: "collapse rejected", ConcreteDataRefs: []string{"fixtures/collapse_rejected.json"}, Negative: true},
			},
		}},
		BDDScenarios: []productworkflow.BDDScenarioCoverage{{
			ID:               "BDD-COLLAPSE",
			Status:           productworkflow.BehavioralCoverageStatusAccepted,
			RequirementID:    "REQ-COLLAPSE",
			RuleID:           "RULE-COLLAPSE",
			Given:            []string{"accepted collapse coverage exists"},
			When:             []string{"feature planner resumes"},
			Then:             []string{"coverage contract identity is preserved in the plan tab"},
			ConcreteDataRefs: []string{"fixtures/collapse.json"},
			Obligations:      []string{"TDD-COLLAPSE-EVENT"},
		}},
		DesignContracts: []productworkflow.DesignContractCoverage{{
			ID:             "DBC-COLLAPSE",
			Status:         productworkflow.BehavioralCoverageStatusAccepted,
			Boundary:       "product workflow to feature planner",
			Preconditions:  []string{"coverage contract is promoted"},
			Postconditions: []string{"plan cites coverage contract"},
			Invariants:     []string{"accepted coverage remains product-owned"},
			TestRefs:       []string{"TestFeaturePlannerTUIE2EProductCoveragePromotionContextResumePlanDisplay"},
			Obligations:    []string{"TDD-COLLAPSE-EVENT"},
		}},
		TestObligations: []productworkflow.TestObligationCoverage{{
			ID:             "TDD-COLLAPSE-EVENT",
			Type:           "contract",
			Behavior:       "collapse plan preserves accepted product coverage identity",
			Owner:          "pkg/collapse_event_test.go",
			Command:        "go test ./pkg",
			RequiredBefore: "implementation",
		}},
		Verification: []productworkflow.VerificationGateCoverage{{
			ID:      "VERIFY-COLLAPSE",
			Gate:    "contract",
			Command: "go test ./pkg",
		}},
		Mutation: productworkflow.BehavioralMutationCoverage{Requirement: "required", MinScore: 80, Score: &score},
	}

	ledger := productworkflow.TDDLedger{
		SchemaVersion: "idiom.tdd_obligations.v2",
		EffortID:      "collapse-effort",
		TaskID:        "collapse-task",
		CoverageTier:  "behavior_standard",
	}
	gomega.Expect(productworkflow.ValidateBehavioralCoverageContract(contract)).To(gomega.Succeed())
	acceptedContract := productworkflow.AcceptedOnlyBehavioralCoverageContract(contract)
	promotionDigest := productworkflow.BehavioralCoverageContractDigest(acceptedContract)
	promotionEventID := "evt-collapse-coverage-promoted"
	acceptedContract.EventID = promotionEventID
	acceptedContract.PromotionState = productworkflow.BehavioralCoveragePromotionStateAccepted
	promoted := ledger
	promoted.BehavioralCoverage = &acceptedContract
	promoted.Coverage = productworkflow.ProjectBehavioralCoverageContract(acceptedContract)
	for _, obligation := range acceptedContract.TestObligations {
		promoted.Obligations = append(promoted.Obligations, productworkflow.TDDObligation{
			ID:             obligation.ID,
			Type:           obligation.Type,
			Behavior:       obligation.Behavior,
			Owner:          obligation.Owner,
			Command:        obligation.Command,
			RequiredBefore: obligation.RequiredBefore,
		})
	}
	promotion := productworkflow.BehavioralCoveragePromotionResult{
		State:           productworkflow.BehavioralCoveragePromotionStateAccepted,
		CommitStatus:    productworkflow.BehavioralCoveragePromotionCommitStatusCommitted,
		LedgerCommitted: true,
		AuditStatus:     productworkflow.BehavioralCoveragePromotionAuditStatusRecorded,
		EventID:         promotionEventID,
		Digest:          promotionDigest,
		Ledger:          promoted,
	}
	gomega.Expect(promoted.BehavioralCoverage).NotTo(gomega.BeNil(), "promotion should stamp behavioral coverage into tdd.json ledger")

	prd := productworkflow.PRDDocument{
		FrontMatter: productworkflow.PRDFrontMatter{Status: "accepted", PRDID: "collapse-prd", TaskID: "collapse-task"},
		Requirements: []productworkflow.PRDRequirement{{
			ID:             "REQ-COLLAPSE",
			Priority:       "P1",
			Statement:      "Route collapse dispatch through CollapseEvent.",
			EvidenceRefs:   []string{"EV-COLLAPSE"},
			TDDObligations: []string{"TDD-COLLAPSE-EVENT"},
			Testability:    "go test ./pkg",
			Metric:         "collapse tests pass",
		}},
	}
	adrIndex := productworkflow.ADRIndex{ADRs: []productworkflow.ADRIndexEntry{{
		ADRID:        "ADR-COLLAPSE",
		Status:       "accepted",
		Path:         "001-collapse.md",
		CoverageTier: "behavior_standard",
		PRDRefs:      []string{"REQ-COLLAPSE"},
	}}}
	adrs := []productworkflow.ParsedADR{{
		RelPath: "adrs/001-collapse.md",
		Doc: productworkflow.ADRDocument{FrontMatter: productworkflow.ADRFrontMatter{
			ADRID:        "ADR-COLLAPSE",
			Status:       "accepted",
			EvidenceRefs: []string{"EV-COLLAPSE"},
		}},
	}}
	traceability := productworkflow.TraceabilityMatrix{Matrix: []productworkflow.TraceabilityRow{{
		EvidenceCard:   "EV-COLLAPSE",
		PRDRequirement: "REQ-COLLAPSE",
		ADR:            "ADR-COLLAPSE",
		TDDObligation:  "TDD-COLLAPSE-EVENT",
	}}}
	coverage := productworkflow.EvaluateCoverageGate(prd, adrIndex, adrs, promoted, traceability, map[string]bool{})
	gomega.Expect(coverage.Status).To(gomega.Equal(productworkflow.ProductWorkflowStatusBlocked))
	gomega.Expect(productworkflow.CoverageGateHasOnlyTDDEvidenceBlockers(coverage)).To(gomega.BeTrue())

	handoff := productworkflow.BuildImplementationHandoff(productworkflow.BriefGenerationInput{
		EffortRoot:       filepath.Join(projectRoot, "docs", "product-workflows", "efforts", "started", "collapse-effort"),
		ArtifactRootPath: "docs/product-workflows/efforts/started/collapse-effort",
		WorkflowState: productworkflow.WorkflowState{
			EffortID:         "collapse-effort",
			TaskID:           "collapse-task",
			StatusBucket:     "started",
			CurrentPhase:     string(productworkflow.PhaseTDDSynthesis),
			CurrentState:     productworkflow.WorkflowStatePlanned,
			LastEventID:      promotion.EventID,
			NextValidActions: []string{"finalize"},
		},
		PRD:          prd,
		ADRIndex:     adrIndex,
		ADRs:         adrs,
		TDD:          promoted,
		Traceability: traceability,
		ImplementationRecords: []productworkflow.ImplementationLedgerRecord{{
			RecordType: productworkflow.LedgerRecordAtomPlanned,
			TaskID:     "collapse-task",
			EffortID:   "collapse-effort",
			Scope:      &productworkflow.AtomScope{ExpectedFiles: []string{"pkg/collapse_event.go", "pkg/collapse_event_test.go"}},
		}},
		PromotionEvents: []productworkflow.BehavioralCoveragePromotionEvent{{
			Event:        string(productworkflow.ProductWorkflowEventBehavioralCoverageAccepted),
			EventID:      promotion.EventID,
			State:        productworkflow.BehavioralCoveragePromotionStateAccepted,
			CommitStatus: productworkflow.BehavioralCoveragePromotionCommitStatusCommitted,
			EffortID:     "collapse-effort",
			TaskID:       "collapse-task",
			Digest:       promotion.Digest,
			Timestamp:    "2026-06-22T00:00:00Z",
			Source:       string(productworkflow.ProductWorkflowEventSourceTDDBriefPromotion),
		}},
		CoverageEvaluation:    coverage,
		ExistingWorkspacePath: map[string]bool{"pkg/collapse_event.go": true, "pkg/collapse_event_test.go": true},
	})
	brief := featureplannerproduct.BriefFromImplementationHandoff(handoff)
	gomega.Expect(brief.CoverageContract).NotTo(gomega.BeNil(), "feature planner product context should include coverage contract")

	plan := featurePlannerCommandStandaloneCollapsePlan()
	plan.SDDContract = featureplanner.SDDContract{
		PRDRequirementRefs: []string{"REQ-COLLAPSE"},
		ADRRefs:            []string{"ADR-COLLAPSE"},
		Acceptance:         []string{"Collapse dispatch routes through CollapseEvent."},
	}
	plan.TDDContract.CoverageTier = "behavior_standard"
	plan.Atoms[0].SDDRefs = []string{"REQ-COLLAPSE", "ADR-COLLAPSE"}
	plan.CoverageContract = &featureplanner.CoverageContractRef{
		ContractID:   brief.CoverageContract.ContractID,
		Digest:       brief.CoverageContract.Digest,
		EventID:      brief.CoverageContract.EventID,
		Status:       brief.CoverageContract.Status,
		EffortID:     brief.CoverageContract.EffortID,
		CoverageTier: brief.CoverageContract.CoverageTier,
	}

	gomega.Expect(featureplanner.ValidatePlanProductWorkflowContext(plan, *brief, projectRoot)).To(gomega.Succeed())
	plan = featureplanner.NormalizePlanProductWorkflowContext(plan, *brief, projectRoot)

	state := featureplanner.NewState("coverage-resume-session", "Implement CollapseEvent helper.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningRepair
	state.Plan = plan
	state.EffortID = brief.EffortID
	state.EffortRoot = brief.EffortRoot
	state.RejectedPlanCandidates = []featureplanner.RejectedPlanCandidate{{
		Plan:       plan,
		PlanHash:   "candidate-collapse",
		Diagnostic: "resume display candidate",
		EventType:  "idiom.feature_planner.plan_candidate_rejected",
		RejectedAt: "2026-06-22T00:00:00Z",
	}}

	model := newDetachedPromptExecutionSession(context.Background(), &Model{
		cfg: config.Config{
			Chat:      config.Chat{ProjectRoot: projectRoot},
			Workspace: config.Workspace{Root: projectRoot, CWD: projectRoot},
		},
		workspace: workspace.NewManager(config.Workspace{Root: projectRoot, CWD: projectRoot}),
	})
	statePath, err := model.saveFeaturePlannerStateAndPlanTab(&state)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	resumed := loadFeaturePlannerCommandStateFile(t, filepath.Join(projectRoot, filepath.FromSlash(statePath)))
	gomega.Expect(resumed.Plan.CoverageContract).NotTo(gomega.BeNil(), "resume lost coverage contract identity")
	gomega.Expect(resumed.Plan.CoverageContract.ContractID).To(gomega.Equal(brief.CoverageContract.ContractID))

	planTabBody, err := os.ReadFile(filepath.Join(projectRoot, filepath.FromSlash(resumed.PlanPath)))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	for _, want := range []string{
		"Coverage contract status: accepted",
		"Coverage contract event: " + brief.CoverageContract.EventID,
		"Coverage contract digest: " + brief.CoverageContract.Digest,
		"Accepted coverage authority: product_workflow",
		"BDD-COLLAPSE: Given accepted collapse coverage exists / When feature planner resumes / Then coverage contract identity is preserved in the plan tab (concrete data)",
		"TDD-COLLAPSE-EVENT - collapse plan preserves accepted product coverage identity (go test ./pkg)",
	} {
		gomega.Expect(string(planTabBody)).To(gomega.ContainSubstring(want))
	}
	gomega.Expect(string(planTabBody)).NotTo(gomega.ContainSubstring("behavioral_coverage_draft"))
})

var _ = ginkgo.It("feature planner tui turn retries empty planner response", func() {
	t := ginkgo.GinkgoT()
	client := &scriptedLLMRecorder{
		responses:     []string{"", `{"tool":"submit_feature_plan","summary":"ok"}`},
		finishReasons: []string{"length", "stop"},
	}
	m := newDetachedPromptExecutionSession(context.Background(), &Model{client: client.Mock(t)})

	answer, err := m.runFeaturePlannerTUITurn(context.Background(), "Plan one feature atom.", string(autonomy.TaskFamilyFeaturePlannerPlanner))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runFeaturePlannerTUITurn: %v", err))

	gomega.Expect(answer).To(gomega.Equal(`{"tool":"submit_feature_plan","summary":"ok"}`), fmt.Sprintf("answer = %q", answer))

	gomega.Expect(client.callCount).To(gomega.Equal(2), fmt.Sprintf("model calls = %d, want 2", client.callCount))

	gomega.Expect(len(client.options)).To(gomega.Equal(2), fmt.Sprintf("unexpected response format options: %#v", client.options))
	gomega.Expect(client.options[0].ResponseFormatJSON).To(gomega.BeTrue(), fmt.Sprintf("unexpected response format options: %#v", client.options))
	gomega.Expect(client.options[1].ResponseFormatJSON).To(gomega.BeFalse(), fmt.Sprintf("unexpected response format options: %#v", client.options))

	gomega.Expect(len(client.messages)).To(gomega.Equal(2), fmt.Sprintf("retry prompt did not explain empty response: %#v", client.messages))
	gomega.Expect(client.messages[1][0].Content).To(gomega.ContainSubstring("empty visible content"), fmt.Sprintf("retry prompt did not explain empty response: %#v", client.messages))
})

var _ = ginkgo.It("feature planner conversation event sanitizer omits internal diagnostics", func() {
	event := featureplanner.Event{
		Type:    "idiom.feature_planner.standalone_context_blocked",
		Message: "atom 0 edit path src/brain_core/types.py is missing from workspace and has no pytest project evidence",
		Fields: map[string]any{
			"candidate_id":          "candidate-1",
			"submitted_atom_count":  3,
			"validation_diagnostic": "atom 0 edit path src/brain_core/types.py is missing from workspace and has no pytest project evidence",
		},
	}

	got := sanitizeFeaturePlannerEventForConversation(event)
	gomega.Expect(got.Message).To(gomega.Not(gomega.ContainSubstring("src/brain_core/types.py")), fmt.Sprintf("sanitized event leaked diagnostic: %+v", got))
	gomega.Expect(got.Message).To(gomega.Not(gomega.ContainSubstring("pytest project evidence")), fmt.Sprintf("sanitized event leaked diagnostic: %+v", got))
	{
		_, ok := got.Fields["validation_diagnostic"]
		gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("sanitized event leaked validation_diagnostic: %+v", got.Fields))
	}
	gomega.Expect(got.Fields["candidate_id"]).To(gomega.Equal("candidate-1"), fmt.Sprintf("sanitized event lost existing fields: %+v", got.Fields))
	gomega.Expect(got.Fields["submitted_atom_count"]).To(gomega.Equal(3), fmt.Sprintf("sanitized event lost safe count field: %+v", got.Fields))
	gomega.Expect(got.Fields["diagnostic_chars"]).To(gomega.Equal(len(event.Message)), fmt.Sprintf("diagnostic_chars = %v, want %d", got.Fields["diagnostic_chars"], len(event.Message)))
	gomega.Expect(got.Fields["diagnostic_fields_omitted"]).To(gomega.Equal(1), fmt.Sprintf("diagnostic_fields_omitted = %v, want 1", got.Fields["diagnostic_fields_omitted"]))
	{
		_, ok := event.Fields["diagnostic_chars"]
		gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprintf("sanitizer mutated original fields: %+v", event.Fields))
	}

	ordinary := featureplanner.Event{Type: "idiom.feature_planner.planner_question_opened", Message: "question text"}
	{
		sanitized := sanitizeFeaturePlannerEventForConversation(ordinary)
		gomega.Expect(sanitized.Message).To(gomega.Equal(ordinary.Message), fmt.Sprintf("ordinary event was sanitized: %+v", sanitized))
	}
})

var _ = ginkgo.It("feature planner diagnostic event logs as run event not message", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	model := New(config.Config{
		AppName: "Idiom",
		Chat: config.Chat{
			DebugLogDir: filepath.Join(root, "logs", "conversations"),
		},
		Workspace: config.Workspace{
			Root: root,
			CWD:  root,
		},
	})
	session := newDetachedPromptExecutionSession(context.Background(), model)

	session.logFeaturePlannerEvent(featureplanner.Event{
		Type:    "idiom.feature_planner.standalone_context_blocked",
		Message: "atom 0 edit path src/brain_core/types.py is missing from workspace and has no pytest project evidence",
		Fields: map[string]any{
			"submitted_atom_count":  1,
			"validation_diagnostic": "atom 0 edit path src/brain_core/types.py is missing from workspace and has no pytest project evidence",
		},
	})

	logPath := model.DebugLogPath()
	model.Close()

	body, err := os.ReadFile(logPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read debug log: %v", err))

	var foundRunEvent bool
	for _, record := range decodeJSONEvents(t, string(body)) {
		if record["role"] != "idiom.feature_planner.standalone_context_blocked" {
			continue
		}

		gomega.Expect(record["type"]).NotTo(gomega.Equal("message"), fmt.Sprintf("feature-planner diagnostic must not be logged as conversation message: %+v", record))
		gomega.Expect(record["type"]).To(gomega.Equal("run_event"), fmt.Sprintf("feature-planner diagnostic logged with unexpected type: %+v", record))

		foundRunEvent = true
		content := fmt.Sprint(record["content"])
		gomega.Expect(content).To(gomega.Not(gomega.ContainSubstring("validation_diagnostic")), fmt.Sprintf("feature-planner run_event leaked raw diagnostic: %s", content))
		gomega.Expect(content).To(gomega.Not(gomega.ContainSubstring("src/brain_core/types.py")), fmt.Sprintf("feature-planner run_event leaked raw diagnostic: %s", content))
		gomega.Expect(content).To(gomega.Not(gomega.ContainSubstring("pytest project evidence")), fmt.Sprintf("feature-planner run_event leaked raw diagnostic: %s", content))
	}

	gomega.Expect(foundRunEvent).To(gomega.BeTrue(), fmt.Sprintf("expected sanitized feature-planner run_event in log:\n%s", string(body)))
})

var _ = ginkgo.It("feature planner command keeps internal records out of conversation messages", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	model := New(config.Config{
		AppName: "Idiom",
		Chat: config.Chat{
			DebugLogDir: filepath.Join(root, "logs", "conversations"),
			ProjectRoot: root,
		},
		Workspace: config.Workspace{
			Root: root,
			CWD:  root,
		},
	})
	model.client = (&scriptedLLMRecorder{responses: []string{`{
		"tool": "ask_planner_question",
		"question_summary": "Approve greenfield scope",
		"blocking_claim": "new files need operator authority before a safe plan can be approved",
		"requested_decision": "Approve creating src/brain.py and tests/test_brain.py, or revise the greenfield project setup.",
		"question_kind": "external_answer_required",
		"authority_required": "operator_approval",
		"required_before": "plan_approval",
		"affected_paths": ["src/brain.py", "tests/test_brain.py"],
		"affected_fields": ["new_file_authorization", "verification_command"]
	}`}}).Mock(t)

	session := newDetachedPromptExecutionSession(context.Background(), model)
	answer, err := session.runFeaturePlannerCommandOnce(context.Background(), "Lay out the bones for this LLM brain project.")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runFeaturePlannerCommandOnce: %v", err))
	gomega.Expect(answer).To(gomega.ContainSubstring("Feature planner needs your approval before it can continue."), fmt.Sprintf("expected planning-question answer, got:\n%s", answer))
	gomega.Expect(answer).To(gomega.ContainSubstring("Also provide the exact verification command for this project."), fmt.Sprintf("expected planning-question answer, got:\n%s", answer))

	logPath := model.DebugLogPath()
	model.Close()

	body, err := os.ReadFile(logPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read debug log: %v", err))

	foundRunEvents := map[string]bool{}
	for _, record := range decodeJSONEvents(t, string(body)) {
		role := fmt.Sprint(record["role"])
		if record["type"] == "message" {
			gomega.Expect(role).NotTo(gomega.Or(
				gomega.HavePrefix("feature_planner_"),
				gomega.HavePrefix("idiom.feature_planner."),
				gomega.HavePrefix("idiom.plan_execute."),
			), fmt.Sprintf("feature-planner internal role logged as conversation message: %+v", record))
		}
		if record["type"] == "run_event" && featurePlannerInternalLogRole(role) {
			foundRunEvents[role] = true
		}
	}

	for _, want := range []string{
		"idiom.feature_planner.start",
		"feature_planner_prompt",
		"feature_planner_model",
		"idiom.feature_planner.planner_question_opened",
		"idiom.feature_planner.question_open",
	} {
		gomega.Expect(foundRunEvents[want]).To(gomega.BeTrue(), fmt.Sprintf("expected feature-planner internal run_event %q in log:\n%s", want, string(body)))
	}
})

var _ = ginkgo.It("feature planner tui converts greenfield mvp repair into approval prompt", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	plan := featurePlannerCommandStandaloneCollapsePlan()
	plan.FeatureID = "llm-brain-mvp"
	plan.SourceTask = featureplanner.SourceTask{Kind: "operator_prompt", Summary: `Can you work out an MVP to an effective "llm brain" sort of project?`}
	plan.Summary = "Work out an MVP for an LLM brain project."
	plan.TopLevelScope.Objective = "Establish the minimal LLM brain project structure."
	plan.TopLevelScope.SuccessCriteria = []string{"Knowledge storage and retrieval have focused tests."}
	plan.Atoms[0].ID = "atom-knowledge-store"
	plan.Atoms[0].Title = "Knowledge store"
	plan.Atoms[0].Summary = "Create the initial knowledge store module and tests."
	plan.Atoms[0].RequiredEditPaths = []string{"src/brain/knowledge_store.py", "tests/test_knowledge_store.py"}
	plan.Atoms[0].AllowedEditPaths = nil
	plan.Atoms[0].TestFirst.Commands = []string{"pytest tests/test_knowledge_store.py"}
	plan.Atoms[0].RequiredTests = []string{"pytest tests/test_knowledge_store.py"}
	plan.Atoms[0].TestFirst.TestArtifactPaths = []string{"tests/test_knowledge_store.py"}
	plan.Atoms[0].PlannedTDDSnapshot.TestCommands = []string{"pytest tests/test_knowledge_store.py"}
	plan.Atoms[0].PlannedTDDSnapshot.TestArtifactPaths = []string{"tests/test_knowledge_store.py"}
	plan.Atoms[0].Prompt = "Create src/brain/knowledge_store.py and tests/test_knowledge_store.py for the MVP."

	model := New(config.Config{
		AppName: "Idiom",
		Chat: config.Chat{
			DebugLogDir: filepath.Join(root, "logs", "conversations"),
			ProjectRoot: root,
		},
		Workspace: config.Workspace{
			Root: root,
			CWD:  root,
		},
	})
	model.client = (&scriptedLLMRecorder{
		responses: []string{featurePlannerCommandPlanJSON(t, plan)},
	}).Mock(t)

	session := newDetachedPromptExecutionSession(context.Background(), model)
	answer, err := session.runFeaturePlannerCommandOnce(context.Background(), `Can you work out an MVP to an effective "llm brain" sort of project?`)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runFeaturePlannerCommandOnce: %v", err))
	model.Close()

	for _, want := range []string{
		"Feature planner needs your approval before it can continue.",
		"What this proposed plan entails:",
		"Objective: Establish the minimal LLM brain project structure.",
		"Proposed paths:",
		"- src/brain/knowledge_store.py",
		"- tests/test_knowledge_store.py",
		"Proposed verification: go test ./pkg; pytest tests/test_knowledge_store.py",
		"Reply with approval, revised paths/setup, or rejection. Other questions will stay in chat.",
	} {
		gomega.Expect(strings.Contains(answer, want)).To(gomega.BeTrue(), fmt.Sprintf("approval prompt missing %q:\n%s", want, answer))
	}
	for _, forbidden := range []string{
		"feature-planner: planning-repair",
		"blockers:",
		"atom 0 edit path",
		"has no pytest project evidence",
		"missing standalone new-file authorization",
		"next: repair_feature_plan",
	} {
		gomega.Expect(strings.Contains(answer, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("approval prompt leaked repair text %q:\n%s", forbidden, answer))
	}
})

func featurePlannerInternalLogRole(role string) bool {
	role = strings.TrimSpace(role)

	return strings.HasPrefix(role, "feature_planner_") ||
		strings.HasPrefix(role, "idiom.feature_planner.") ||
		strings.HasPrefix(role, "idiom.plan_execute.")
}

var _ = ginkgo.It("feature planner tui operator question uses approval prompt", func() {
	state := featureplanner.NewState("question-session", "need scope", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningQuestion
	plan := featurePlannerCommandStandaloneCollapsePlan()
	plan.Summary = "Route collapse dispatch through a slim event helper."
	plan.TopLevelScope.Objective = "Add the CollapseEvent helper and matching test coverage."
	state.RejectedPlanCandidates = []featureplanner.RejectedPlanCandidate{{
		Plan:       plan,
		PlanHash:   "candidate-hash",
		Diagnostic: "missing new-file authorization",
	}}
	state.Blockers = []string{
		`atom 0 edit path "src/brain.py" is missing from the workspace and is not authorized as a new file`,
	}
	state.OpenQuestions = []featureplanner.PlannerQuestion{{
		Status:            "open",
		QuestionSummary:   "Approve greenfield scope",
		AuthorityRequired: "operator_approval",
		RequestedDecision: "Approve creating src/brain.py or revise scope.",
		AffectedPaths:     []string{"src/brain.py", "tests/test_brain.py"},
		AffectedFields:    []string{"new_file_authorization", "verification_command"},
	}}

	answer := formatFeaturePlannerTUIAnswerForRoot(state, ".idiom/feature-planner/question-session/state.json", "")
	for _, want := range []string{
		"Feature planner needs your approval before it can continue.",
		"Question: Approve greenfield scope",
		"What this proposed plan entails:",
		"Objective: Add the CollapseEvent helper and matching test coverage.",
		"Work breakdown: CollapseEvent helper",
		"These are proposed files to create in the empty workspace, not existing source files.",
		"Proposed paths:",
		"- src/brain.py",
		"- tests/test_brain.py",
		"Proposed verification: go test ./pkg",
		"Reply with approval, revised paths/setup, or rejection. Other questions will stay in chat.",
		"State saved at .idiom/feature-planner/question-session/state.json",
	} {
		gomega.Expect(strings.Contains(answer, want)).To(gomega.BeTrue(), fmt.Sprintf("TUI answer missing %q:\n%s", want, answer))
	}

	for _, forbidden := range []string{
		"feature-planner: planning-question-open",
		"requested_decision:",
		"blockers:",
		"next:",
		"atom 0 edit path",
	} {
		gomega.Expect(strings.Contains(answer, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("TUI answer leaked state-summary/internal text %q:\n%s", forbidden, answer))
	}
})

var _ = ginkgo.It("feature planner tui auto repairs standalone project missing new files in partial workspace", func() {
	t := ginkgo.GinkgoT()
	projectRoot := t.TempDir()
	writeFile(t, projectRoot, "go.mod", "module example.com/tui\n")

	model := New(config.Config{
		AppName: "Idiom",
		Chat: config.Chat{
			DebugLogDir: filepath.Join(projectRoot, "logs", "conversations"),
			ProjectRoot: projectRoot,
		},
		Workspace: config.Workspace{
			Root: projectRoot,
			CWD:  projectRoot,
		},
	})
	plan := featurePlannerCommandStandaloneCollapsePlan()
	model.client = (&scriptedLLMRecorder{responses: []string{featurePlannerCommandPlanJSON(t, plan)}}).Mock(t)

	session := newDetachedPromptExecutionSession(context.Background(), model)
	answer, err := session.runFeaturePlannerCommandOnce(context.Background(), `Can you work out an MVP to an effective "llm brain" sort of project to manage long-term agentic memory?`)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runFeaturePlannerCommandOnce: %v", err))
	model.Close()

	for _, want := range []string{
		"Feature planner needs your approval before it can continue.",
		"Question: Authorize proposed new files for feature plan",
		"What this proposed plan entails:",
		"Proposed paths:",
		"- pkg/collapse_event.go",
		"- pkg/collapse_event_test.go",
		"These are proposed missing paths to create only after approval; they are not authorized workspace edits yet.",
		"Reply with approval, revised paths/setup, or rejection. Other questions will stay in chat.",
	} {
		gomega.Expect(strings.Contains(answer, want)).To(gomega.BeTrue(), fmt.Sprintf("answer missing %q:\n%s", want, answer))
	}
	for _, forbidden := range []string{
		"feature-planner: planning-repair",
		"blockers:",
		"atom 0 edit path",
		"missing standalone new-file authorization",
		"next: repair_feature_plan",
		"What existing source files or test files should be edited",
		"empty workspace",
	} {
		gomega.Expect(strings.Contains(answer, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("answer leaked failed-repair text %q:\n%s", forbidden, answer))
	}
})

var _ = ginkgo.It("feature planner tui opens verification question for existing path running example", func() {
	t := ginkgo.GinkgoT()
	projectRoot := t.TempDir()
	writeFile(t, projectRoot, "llm_brain/query_handler.py", "class QueryHandler:\n    pass\n")
	writeFile(t, projectRoot, "tests/test_query_handler.py", "def test_placeholder():\n    assert True\n")

	model := New(config.Config{
		AppName: "Idiom",
		Chat: config.Chat{
			DebugLogDir: filepath.Join(projectRoot, "logs", "conversations"),
			ProjectRoot: projectRoot,
		},
		Workspace: config.Workspace{
			Root: projectRoot,
			CWD:  projectRoot,
		},
	})
	plan := featurePlannerCommandRunningExampleExistingPathPlan()
	model.client = (&scriptedLLMRecorder{responses: []string{featurePlannerCommandPlanJSON(t, plan)}}).Mock(t)

	session := newDetachedPromptExecutionSession(context.Background(), model)
	answer, err := session.runFeaturePlannerCommandOnce(context.Background(), `Can you work out an MVP to an effective "llm brain" sort of project to manage long-term agentic memory?`)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runFeaturePlannerCommandOnce: %v", err))
	sessionID := session.directFeaturePlannerSessionID
	model.Close()

	for _, want := range []string{
		"Feature planner needs your input before it can continue.",
		"Question: Confirm standalone verification command",
		"Decision needed:",
		"pytest tests/test_query_handler.py",
		"Also provide the exact verification command for this project.",
	} {
		gomega.Expect(strings.Contains(answer, want)).To(gomega.BeTrue(), fmt.Sprintf("answer missing %q:\n%s", want, answer))
	}
	for _, forbidden := range []string{
		"feature-planner: planning-repair",
		"blockers:",
		"behavioral_coverage_draft",
		"create_memory(content)",
		"get_memory(id)",
		"next: repair_feature_plan",
		"Feature planner needs your approval before it can continue.",
	} {
		gomega.Expect(strings.Contains(answer, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("answer leaked repair or wrong approval surface %q:\n%s", forbidden, answer))
	}

	state := loadFeaturePlannerCommandStateFile(t, filepath.Join(projectRoot, ".idiom", "feature-planner", sessionID, "state.json"))
	gomega.Expect(len(state.OpenQuestions)).To(gomega.Equal(1), fmt.Sprintf("state should persist one verification command question: %+v", state.OpenQuestions))
	gomega.Expect(state.OpenQuestions[0].AuthorityRequired).To(gomega.Equal("operator_approval"), fmt.Sprintf("state should persist one verification command question: %+v", state.OpenQuestions))
	gomega.Expect(state.OpenQuestions[0].AffectedFields).To(gomega.ContainElement("verification_command"), fmt.Sprintf("state should persist one verification command question: %+v", state.OpenQuestions))
	gomega.Expect(len(state.RejectedPlanCandidates)).To(gomega.Equal(1), fmt.Sprintf("state should retain model-owned BDD repair diagnostic: %+v", state.RejectedPlanCandidates))
	gomega.Expect(state.RejectedPlanCandidates[0].Diagnostic).To(gomega.ContainSubstring("behavioral_coverage_draft"), fmt.Sprintf("state should retain model-owned BDD repair diagnostic: %+v", state.RejectedPlanCandidates))
})

var _ = ginkgo.It("feature planner tui repair summary does not leak raw diagnostics", func() {
	state := featureplanner.NewState("repair-session", "repair feature", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningRepair
	state.PlanningLoop.State = featureplanner.FeatureStatePlanningRepair
	state.Blockers = []string{
		`atom 0 edit path "src/brain.py" is missing from the workspace and is not authorized as a new file`,
		"behavioral_coverage_draft missing concrete_data",
	}

	answer := formatFeaturePlannerTUIAnswerForRoot(state, ".idiom/feature-planner/repair-session/state.json", "")
	for _, want := range []string{
		"feature-planner: planning-repair",
		"state: .idiom/feature-planner/repair-session/state.json",
		"diagnostics: 2 internal planner diagnostic(s) saved in feature-planner state",
	} {
		gomega.Expect(strings.Contains(answer, want)).To(gomega.BeTrue(), fmt.Sprintf("repair answer missing %q:\n%s", want, answer))
	}
	for _, forbidden := range []string{
		"blockers:",
		"atom 0 edit path",
		"behavioral_coverage_draft",
		"next: repair_feature_plan",
	} {
		gomega.Expect(strings.Contains(answer, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("repair answer leaked raw diagnostic %q:\n%s", forbidden, answer))
	}
})

var _ = ginkgo.It("feature planner tui generic operator question renders chat question", func() {
	state := featureplanner.NewState("question-session", "need approval", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningQuestion
	state.OpenQuestions = []featureplanner.PlannerQuestion{{
		Status:            "open",
		QuestionSummary:   "Confirm product decision",
		AuthorityRequired: "operator_approval",
		RequestedDecision: "Confirm whether this belongs in the current effort.",
		AffectedFields:    []string{"product_scope"},
	}}

	answer := formatFeaturePlannerTUIAnswerForRoot(state, ".idiom/feature-planner/question-session/state.json", "")
	for _, want := range []string{
		"Feature planner needs your input before it can continue.",
		"Question: Confirm product decision",
		"Decision needed: Confirm whether this belongs in the current effort.",
		"Reply with the requested answer, a revised scope, or a rejection. Other questions will stay in chat.",
		"State saved at .idiom/feature-planner/question-session/state.json",
	} {
		gomega.Expect(strings.Contains(answer, want)).To(gomega.BeTrue(), fmt.Sprintf("generic operator question answer missing %q:\n%s", want, answer))
	}
	for _, forbidden := range []string{
		"feature-planner: " + featureplanner.FeatureStatePlanningQuestion,
		"Proposed paths:",
		"blockers:",
		"next:",
	} {
		gomega.Expect(strings.Contains(answer, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("generic operator question leaked internal text %q:\n%s", forbidden, answer))
	}
})

var _ = ginkgo.It("feature planner tui non operator source localization question is hidden by default", func() {
	state := featureplanner.NewState("question-session", "Implement CollapseEvent helper.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningQuestion
	state.OpenQuestions = []featureplanner.PlannerQuestion{{
		Status:            "open",
		QuestionSummary:   "Provide accepted source facts for CollapseEvent",
		BlockingClaim:     "The planner needs source-localized signature evidence.",
		RequestedDecision: "Provide accepted source facts for the current CollapseEvent signature.",
		AuthorityRequired: "accepted_source",
		AffectedPaths:     []string{"pkg/collapse_event.go"},
		AffectedFields:    []string{"source_localization"},
	}}

	answer := formatFeaturePlannerTUIAnswerForRoot(state, ".idiom/feature-planner/question-session/state.json", "")
	for _, want := range []string{
		"feature-planner: " + featureplanner.FeatureStatePlanningQuestion,
		"state: .idiom/feature-planner/question-session/state.json",
		"open questions: 1",
	} {
		gomega.Expect(strings.Contains(answer, want)).To(gomega.BeTrue(), fmt.Sprintf("hidden internal question summary missing %q:\n%s", want, answer))
	}
	for _, forbidden := range []string{
		"Feature planner needs your input before it can continue.",
		"Question: Provide accepted source facts",
		"answer_format:",
		"source_facts",
	} {
		gomega.Expect(strings.Contains(answer, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("internal accepted-source question leaked %q:\n%s", forbidden, answer))
	}
})

var _ = ginkgo.It("feature planner tui explicit source localization question can render", func() {
	state := featureplanner.NewState("question-session", "Please do source localization and collect accepted source facts for the CollapseEvent helper.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningQuestion
	state.OpenQuestions = []featureplanner.PlannerQuestion{{
		Status:            "open",
		QuestionSummary:   "Provide accepted source facts for CollapseEvent",
		BlockingClaim:     "The operator asked for source localization.",
		RequestedDecision: "Provide accepted source facts for the current CollapseEvent signature.",
		AuthorityRequired: "accepted_source",
		AffectedPaths:     []string{"pkg/collapse_event.go"},
		AffectedFields:    []string{"source_localization"},
		UserFacing:        true,
	}}

	answer := formatFeaturePlannerTUIAnswerForRoot(state, ".idiom/feature-planner/question-session/state.json", "")
	for _, want := range []string{
		"Feature planner needs your input before it can continue.",
		"Question: Provide accepted source facts for CollapseEvent",
		`answer_format: JSON object with answer, answer_source_kind="accepted_source", evidence_refs, and source_facts [{path, symbol, signature}]`,
		"State saved at .idiom/feature-planner/question-session/state.json",
	} {
		gomega.Expect(strings.Contains(answer, want)).To(gomega.BeTrue(), fmt.Sprintf("explicit source-localization question answer missing %q:\n%s", want, answer))
	}
})

var _ = ginkgo.It("feature planner tui state path display uses workspace relative path", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	insidePath := filepath.Join(root, ".idiom", "feature-planner", "question-session", "state.json")
	outsidePath := filepath.Join(t.TempDir(), "state.json")

	{
		got := featurePlannerTUIDisplayPath(root, insidePath)
		gomega.Expect(got).To(gomega.Equal(".idiom/feature-planner/question-session/state.json"), fmt.Sprintf("inside-root display path = %q", got))
	}
	{
		got := featurePlannerTUIDisplayPath(root, outsidePath)
		gomega.Expect(got).To(gomega.Equal(outsidePath), fmt.Sprintf("outside-root display path = %q, want %q", got, outsidePath))
	}
	{
		got := featurePlannerTUIDisplayPath(root, ".idiom/feature-planner/question-session/state.json")
		gomega.Expect(got).To(gomega.Equal(".idiom/feature-planner/question-session/state.json"), fmt.Sprintf("relative display path = %q", got))
	}
})

var _ = ginkgo.It("feature planner command demotes unanchored path authority question", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	model := New(config.Config{
		AppName: "Idiom",
		Chat: config.Chat{
			DebugLogDir: filepath.Join(root, "logs", "conversations"),
			ProjectRoot: root,
		},
		Workspace: config.Workspace{
			Root: root,
			CWD:  root,
		},
	})
	model.client = (&scriptedLLMRecorder{responses: []string{`{
		"tool": "ask_planner_question",
		"question_summary": "Define minimal MVP requirements",
		"blocking_claim": "A safe plan needs explicit runtime behavior before file creation can be proposed.",
		"requested_decision": "Choose the minimal runnable behavior and verification command for the MVP.",
		"question_kind": "external_answer_required",
		"authority_required": "operator_approval",
		"required_before": "plan_approval",
		"affected_fields": ["new_file_authorization", "verification_command"]
	}`}}).Mock(t)

	session := newDetachedPromptExecutionSession(context.Background(), model)
	answer, err := session.runFeaturePlannerCommandOnce(context.Background(), `Can you work out an MVP to an effective "llm brain" sort of project?`)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runFeaturePlannerCommandOnce: %v", err))
	model.Close()

	for _, want := range []string{
		"Feature planner needs your input before it can continue.",
		"Question: Define minimal MVP requirements",
		"Decision needed: Choose the minimal runnable behavior and verification command for the MVP.",
		"Also provide the exact verification command for this project.",
	} {
		gomega.Expect(strings.Contains(answer, want)).To(gomega.BeTrue(), fmt.Sprintf("answer missing %q:\n%s", want, answer))
	}
	for _, forbidden := range []string{
		"feature-planner: planning-repair",
		"planner question path-level authority requires affected_paths",
		"blockers:",
		"next: repair_feature_plan",
		"Feature planner needs your approval before it can continue.",
	} {
		gomega.Expect(strings.Contains(answer, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("answer leaked rejected-question state %q:\n%s", forbidden, answer))
	}
})

var _ = ginkgo.It("feature planner command rejects operator owned source path discovery question", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	model := New(config.Config{
		AppName: "Idiom",
		Chat: config.Chat{
			DebugLogDir: filepath.Join(root, "logs", "conversations"),
			ProjectRoot: root,
		},
		Workspace: config.Workspace{
			Root: root,
			CWD:  root,
		},
	})
	model.client = (&scriptedLLMRecorder{responses: []string{`{
		"tool": "ask_planner_question",
		"question_summary": "What existing source files or test files should be edited for persistent memory and multi-repo support?",
		"blocking_claim": "No existing source or test paths are available in the workspace context to plan edits for the requested features.",
		"requested_decision": "Please provide the repo-relative paths of existing source files and test files that should be modified.",
		"question_kind": "external_answer_required",
		"authority_required": "operator_approval",
		"required_before": "plan_approval",
		"affected_fields": ["requirements_scope", "source_localization"]
	}`}}).Mock(t)

	session := newDetachedPromptExecutionSession(context.Background(), model)
	answer, err := session.runFeaturePlannerCommandOnce(context.Background(), `We need persistent memory and multi-repo support. Please update the plan with this in mind.`)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runFeaturePlannerCommandOnce: %v", err))
	model.Close()

	for _, want := range []string{
		"feature-planner: planning-repair",
		"diagnostics: 1 internal planner diagnostic(s) saved in feature-planner state",
	} {
		gomega.Expect(strings.Contains(answer, want)).To(gomega.BeTrue(), fmt.Sprintf("answer missing rejected-question repair detail %q:\n%s", want, answer))
	}
	for _, forbidden := range []string{
		"must not ask the operator to discover existing source or test paths",
		"next: repair_feature_plan",
		"blockers:",
		"Feature planner needs your input before it can continue.",
		"Question: What existing source files or test files should be edited",
		"Decision needed: Please provide the repo-relative paths",
	} {
		gomega.Expect(strings.Contains(answer, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("answer surfaced invalid source discovery question %q:\n%s", forbidden, answer))
	}
})

var _ = ginkgo.It("feature planner product write envelope excludes context evidence paths", func() {
	brief := &featureplanner.ProductWorkflowBrief{
		FilesToModify:             []string{"./native/src/core/colony_sim.h", "native/src/core/colony_sim.h"},
		ContextEvidencePaths:      []string{"docs/product-workflows/efforts/started/native-quality/prd.md"},
		AuthorizedNewPackageRoots: []string{"./native/tests/generated/", "../outside"},
	}

	got := featureplanner.ProductWriteEnvelopePaths(brief)
	for _, want := range []string{"native/src/core/colony_sim.h", "native/tests/generated/**"} {
		gomega.Expect(slices.Contains(got, want)).To(gomega.BeTrue(), fmt.Sprintf("write envelope missing authorized mutation path %q: %#v", want, got))
	}

	gomega.Expect(slices.Contains(got, "docs/product-workflows/efforts/started/native-quality/prd.md")).To(gomega.BeFalse(), fmt.Sprintf("context evidence path must not become write authority: %#v", got))

	for _, forbidden := range []string{"./native/src/core/colony_sim.h", "../outside/**"} {
		gomega.Expect(slices.Contains(got, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("write envelope should normalize or drop invalid path %q: %#v", forbidden, got))
	}
})

var _ = ginkgo.It("feature planner tui answer includes open question details", func() {
	state := featureplanner.NewState("question-session", "need scope", "", 3, time.Unix(1, 0).UTC())

	state.Status = featureplanner.FeatureStatePlanningQuestion
	state.OpenQuestions = []featureplanner.PlannerQuestion{{
		Status:          "open",
		QuestionSummary: "Which tooltip path owns the row?",
	}}

	answer := formatFeaturePlannerTUIAnswerForRoot(state, ".idiom/feature-planner/question-session/state.json", "")
	gomega.Expect(answer).To(gomega.ContainSubstring("Feature planner needs your input before it can continue."), fmt.Sprintf("TUI answer omitted question details:\n%s", answer))
	gomega.Expect(answer).To(gomega.ContainSubstring("Question: Which tooltip path owns the row?"), fmt.Sprintf("TUI answer omitted question details:\n%s", answer))
	gomega.Expect(answer).To(gomega.Not(gomega.ContainSubstring("open questions: 1")), fmt.Sprintf("TUI answer leaked state summary text:\n%s", answer))
	gomega.Expect(answer).To(gomega.Not(gomega.ContainSubstring("feature-planner: planning-question-open")), fmt.Sprintf("TUI answer leaked state summary text:\n%s", answer))
})

var _ = ginkgo.It(
	"feature planner product TDD synthesis records source-satisfied delivery once through the real lifecycle",
	ginkgo.Label("development-scenario", "e2e", "integration", "smoke", "race-contract"),
	func() {
		t := ginkgo.GinkgoT()
		projectRoot := t.TempDir()
		disableQueueDaemonForTurnPlannerTest(t, projectRoot)
		effortRoot := filepath.Join(projectRoot, "docs", "product-workflows", "efforts", "started", "native-quality")
		writeNativeQualityEffort(t, effortRoot)
		writeFile(t, projectRoot, "native/src/core/colony_sim.h", "#pragma once\n\ninline bool quality_contributors_visible() { return true; }\n")
		writeFile(t, projectRoot, "native/tests/test_quality_inventory_bindings.cpp", "// quality contributor inventory binding regression is already covered\n")
		for _, args := range [][]string{
			{"init"},
			{"config", "user.email", "test@example.com"},
			{"config", "user.name", "Test User"},
			{"add", "--all"},
			{"commit", "-m", "establish native quality baseline"},
		} {
			command := exec.Command("git", append([]string{"-C", projectRoot}, args...)...)
			output, err := command.CombinedOutput()
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), "git %s: %s", strings.Join(args, " "), string(output))
		}

		plannerResponse := featurePlannerCommandPlanJSON(t, featurePlannerCommandNativePlan())
		var responseState struct {
			sync.Mutex
			chatRequests int
			sourceReads  map[string]bool
			historyReads map[string]bool
		}
		responseState.sourceReads = map[string]bool{}
		responseState.historyReads = map[string]bool{}
		requiredPaths := []string{"native/src/core/colony_sim.h", "native/tests/test_quality_inventory_bindings.cpp"}
		sourceSatisfiedReply := strings.Join([]string{
			"Decision: source-satisfied",
			"- native/src/core/colony_sim.h: the requested final state is already present; the current source exposes contributor visibility.",
			"- native/tests/test_quality_inventory_bindings.cpp: the requested final state is already present; the current regression source covers the binding.",
		}, "\n")

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer ginkgo.GinkgoRecover()
			switch r.URL.Path {
			case "/v1/models":
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "mock-model"}}})
			case "/v1/chat/completions":
				body, err := io.ReadAll(r.Body)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				requestText := string(body)

				responseState.Lock()
				responseState.chatRequests++
				requestNumber := responseState.chatRequests
				reply := ""
				switch {
				case requestNumber == 1:
					reply = plannerResponse
				case strings.Contains(requestText, "Plan submitted with"):
					reply = "Plan submitted."
				default:
					if readReply, ok := planExecuteSourceReadFixtureReply(requestText, responseState.sourceReads, requiredPaths...); ok {
						reply = readReply
					} else {
						allSourcesRead := true
						for _, path := range requiredPaths {
							if !responseState.sourceReads[path] {
								allSourcesRead = false
								break
							}
						}
						gomega.Expect(allSourcesRead).To(gomega.BeTrue(), "planner requested a non-source action before both required source windows were observed")
						for _, path := range requiredPaths {
							if responseState.historyReads[path] {
								continue
							}
							responseState.historyReads[path] = true
							reply = "<tool_call>\n" + fmt.Sprintf(`{"tool":"history_range","path":%q}`, path)
							break
						}
						if reply == "" {
							reply = sourceSatisfiedReply
						}
					}
				}
				responseState.Unlock()

				_ = json.NewEncoder(w).Encode(map[string]any{
					"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
				})
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		cfg := config.Default()
		cfg.LlamaServer.BaseURL = server.URL
		cfg.LlamaServer.Model = "mock-model"
		cfg.Chat.AutoStartServer = false
		cfg.Chat.AttachInstructionFiles = false
		cfg.Chat.SystemPromptFiles = nil
		cfg.Chat.DefaultProfiles = nil
		cfg.Chat.ProjectRoot = projectRoot
		cfg.Chat.DebugLogDir = filepath.Join(projectRoot, "logs", "conversations")
		cfg.Chat.ImplementationDispatch = "plan_execute"
		cfg.Workspace.Root = projectRoot
		cfg.Workspace.CWD = projectRoot
		cfg.Workspace.EnableWrites = true
		cfg.Workspace.EnableShell = false
		cfg.RouteClassifier.Enabled = false
		cfg.MCP = nil

		model := New(cfg)
		defer model.Close()
		session := newDetachedPromptExecutionSession(context.Background(), model)
		session.featurePlannerPhase = productworkflow.PhaseTDDSynthesis
		session.featurePlannerEffortRoot = effortRoot

		answer, err := session.completeProductTDDSynthesis(context.Background(), effortRoot)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "complete product TDD synthesis: %s", answer)
		statePaths, err := filepath.Glob(filepath.Join(projectRoot, ".idiom", "feature-planner", "*", "state.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(statePaths).To(gomega.HaveLen(1))
		plannerState := loadFeaturePlannerCommandStateFile(t, statePaths[0])
		responseState.Lock()
		chatRequests := responseState.chatRequests
		sourceReads := maps.Clone(responseState.sourceReads)
		historyReads := maps.Clone(responseState.historyReads)
		responseState.Unlock()
		gomega.Expect(answer).To(
			gomega.ContainSubstring("feature-planner: "+featureplanner.FeatureStateComplete),
			"status=%s blockers=%v chats=%d source_reads=%v history_requests=%v last_plan_execute=%#v",
			plannerState.Status,
			plannerState.Blockers,
			chatRequests,
			sourceReads,
			historyReads,
			session.lastPlanExecuteRunResult,
		)
		gomega.Expect(session.featurePlannerPhase).To(gomega.BeEmpty())
		gomega.Expect(session.featurePlannerEffortRoot).To(gomega.BeEmpty())

		completed, err := productworkflow.LoadWorkflowState(effortRoot)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(completed.CurrentPhase).To(gomega.Equal(string(productworkflow.PhaseComplete)))
		gomega.Expect(completed.SourceSatisfiedDelivery).NotTo(gomega.BeNil())
		gomega.Expect(completed.SourceSatisfiedDelivery.EvidencePaths).To(gomega.Equal(requiredPaths))
		firstRevision := completed.StateRevision
		firstDigest := completed.SourceSatisfiedDelivery.CompletionDigest

		gomega.Expect(plannerState.Status).To(gomega.Equal(featureplanner.FeatureStateComplete))
		gomega.Expect(plannerState.VerificationClosures).To(gomega.HaveLen(1))
		gomega.Expect(plannerState.VerificationClosures[0].SourceSatisfiedEvidence).To(gomega.Equal([]plancontract.SourceSatisfiedEvidenceRef{
			{Path: requiredPaths[0], Kind: plancontract.SourceSatisfiedEvidenceKindHarnessObservedSource},
			{Path: requiredPaths[1], Kind: plancontract.SourceSatisfiedEvidenceKindHarnessObservedSource},
		}))

		completedBrief, err := featureplannerproduct.LoadBriefFromEffort(effortRoot)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		recorded, err := featureplannerproduct.FinalizeSourceSatisfiedDeliveryFromFeatureState(completedBrief, plannerState)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(recorded).To(gomega.BeTrue())
		repeated, err := productworkflow.LoadWorkflowState(effortRoot)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(repeated.StateRevision).To(gomega.Equal(firstRevision), "replaying the same typed completion must not create another product revision")
		gomega.Expect(repeated.SourceSatisfiedDelivery.CompletionDigest).To(gomega.Equal(firstDigest))

		responseState.Lock()
		gomega.Expect(responseState.sourceReads).To(gomega.HaveLen(len(requiredPaths)))
		gomega.Expect(responseState.historyReads).To(gomega.HaveLen(len(requiredPaths)))
		responseState.Unlock()
	},
)

var _ = ginkgo.It("feature planner tui direct command skips closed source satisfied effort", func() {
	t := ginkgo.GinkgoT()
	projectRoot := t.TempDir()
	effortRel := filepath.Join("docs", "product-workflows", "efforts", "started", "native-quality")
	effortRoot := filepath.Join(projectRoot, effortRel)
	writeNativeQualityEffort(t, effortRoot)

	brief, err := featureplannerproduct.LoadBriefFromEffort(effortRoot)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load product brief: %v", err))
	producerState := featureplanner.NewState("native-source-satisfied-producer", "inspect native implementation", "", 3, time.Unix(1, 0).UTC())
	producerState.EffortRoot = effortRoot
	producerState.EffortID = "native-quality"
	producerState.TaskID = brief.TaskID
	producerState.Status = featureplanner.FeatureStateComplete
	producerState.PlanningLoop.State = featureplanner.FeatureStateComplete
	producerState.Plan.Artifact = featureplanner.FeaturePlanArtifact{
		ArtifactID:           "native-source-satisfied-plan",
		Revision:             1,
		Digest:               "sha256:" + strings.Repeat("b", 64),
		WorkspaceFingerprint: "workspace-native-source-satisfied",
	}
	producerState.Plan.Approval = featureplanner.FeaturePlanApproval{
		Status:               "approved",
		EventID:              "evt-native-source-satisfied-approved",
		PlanRevision:         producerState.Plan.Artifact.Revision,
		PlanDigest:           producerState.Plan.Artifact.Digest,
		WorkspaceFingerprint: producerState.Plan.Artifact.WorkspaceFingerprint,
	}
	atomPlan := featureplanner.FeatureAtomPlan{
		ID:                "atom-native-existing",
		RequiredEditPaths: []string{"native/src/core/colony_sim.h", "native/tests/test_quality_inventory_bindings.cpp"},
	}
	producerState.Plan.Atoms = []featureplanner.FeatureAtomPlan{atomPlan}
	producerState.Atoms = []featureplanner.FeatureAtomState{{
		FeatureAtomPlan: atomPlan,
		State:           featureplanner.AtomStateSourceSatisfied,
	}}
	producerState.VerificationClosures = []featureplanner.VerificationClosure{{
		EventID:                      "evt-native-existing",
		AtomID:                       "atom-native-existing",
		Status:                       featureplanner.VerificationClosureStatusSourceSatisfied,
		Outcome:                      string(plancontract.PlanExecuteTerminalOutcomeSourceSatisfied),
		PlanArtifactID:               producerState.Plan.Artifact.ArtifactID,
		PlanRevision:                 producerState.Plan.Artifact.Revision,
		PlanDigest:                   producerState.Plan.Artifact.Digest,
		ApprovalEventID:              producerState.Plan.Approval.EventID,
		WorkspaceFingerprint:         producerState.Plan.Artifact.WorkspaceFingerprint,
		SourceSatisfiedEvidencePaths: []string{"native/src/core/colony_sim.h", "native/tests/test_quality_inventory_bindings.cpp"},
		SourceSatisfiedEvidence: []plancontract.SourceSatisfiedEvidenceRef{
			{Path: "native/src/core/colony_sim.h", Kind: plancontract.SourceSatisfiedEvidenceKindHarnessObservedSource},
			{Path: "native/tests/test_quality_inventory_bindings.cpp", Kind: plancontract.SourceSatisfiedEvidenceKindHarnessObservedSource},
		},
		CreatedAt: "2026-07-12T12:00:00Z",
	}}
	producerState.Continuation.CompletedAtoms = []string{"atom-native-existing"}
	producerState.ReleaseReadiness = featureplanner.EvaluateReleaseReadiness(producerState, "2026-07-12T12:00:01Z")
	gomega.Expect(producerState.ReleaseReadiness.Status).To(gomega.Equal(featureplanner.ReleaseReadinessStatusReady))
	gomega.Expect(featureplanner.ValidateDurableState(producerState)).To(gomega.Succeed())
	recorded, err := featureplannerproduct.FinalizeSourceSatisfiedDeliveryFromFeatureState(brief, producerState)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("record source-satisfied product delivery: %v", err))
	gomega.Expect(recorded).To(gomega.BeTrue())
	workflowState, err := productworkflow.LoadWorkflowState(effortRoot)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load recorded workflow state: %v", err))
	gomega.Expect(workflowState.CurrentPhase).To(gomega.Equal(string(productworkflow.PhaseComplete)))
	gomega.Expect(workflowState.SourceSatisfiedDelivery).NotTo(gomega.BeNil())

	controller := gomock.NewController(t)
	client := llamamocks.NewMockLLMClient(controller)
	client.EXPECT().ListModels(gomock.Any()).Return([]llama.ModelInfo{{ID: "mock-model"}}, nil).Times(1)
	m := newDetachedPromptExecutionSession(context.Background(), &Model{
		client: client,
		cfg: config.Config{
			Chat:      config.Chat{ProjectRoot: projectRoot},
			Workspace: config.Workspace{Root: projectRoot, CWD: projectRoot},
		},
		workspace: workspace.NewManager(config.Workspace{Root: projectRoot, CWD: projectRoot}),
	})

	answer, err := m.runFeaturePlannerCommandOnce(context.Background(), "Continue existing effort at "+filepath.ToSlash(effortRel)+".")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("runFeaturePlannerCommandOnce: %v", err))

	gomega.Expect(strings.Contains(answer, "feature-planner: "+featureplanner.FeatureStateSourceSatisfied)).To(gomega.BeTrue(), fmt.Sprintf("expected source-satisfied answer, got:\n%s", answer))

	gomega.Expect(m.lastPlanExecuteRunResult).To(gomega.BeNil(), fmt.Sprintf("plan-execute should not be dispatched for closed/source-satisfied effort"))

	statePath := featurePlannerCommandAnswerStatePath(answer)
	gomega.Expect(statePath).NotTo(gomega.Equal(""), fmt.Sprintf("answer missing state path:\n%s", answer))

	state := loadFeaturePlannerCommandStateFile(t, filepath.Join(projectRoot, filepath.FromSlash(statePath)))
	gomega.Expect(state.Status).To(gomega.Equal(featureplanner.FeatureStateSourceSatisfied), fmt.Sprintf("unexpected saved state: %#v", state))
	gomega.Expect(state.EffortID).To(gomega.Equal("native-quality"), fmt.Sprintf("unexpected saved state: %#v", state))
})

func featurePlannerCommandAnswerStatePath(answer string) string {
	for _, line := range strings.Split(answer, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "state:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "state:"))
		}
	}

	return ""
}

func loadFeaturePlannerCommandStateFile(t ginkgo.FullGinkgoTInterface, path string) featureplanner.FeatureState {
	ginkgo.GinkgoHelper()

	body, err := os.ReadFile(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read saved feature planner state: %v", err))

	var state featureplanner.FeatureState

	{
		err := json.Unmarshal(body, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode saved feature planner state: %v", err))
	}

	return state
}

func writeNativeQualityEffort(t ginkgo.FullGinkgoTInterface, effortRoot string) {
	ginkgo.GinkgoHelper()

	_, err := productworkflow.InitializeEffort(productworkflow.CreateEffortOptions{
		EffortRoot: effortRoot,
		EffortID:   "native-quality",
		Title:      "Native quality contributor breakdown",
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	draftContext, err := productworkflow.LoadPRDDraftContext(effortRoot)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	prd := productworkflow.PRDDocument{
		FrontMatter: productworkflow.PRDFrontMatter{
			SchemaVersion: "idiom.prd.v1",
			PRDID:         draftContext.Identity.PRDID,
			Status:        string(productworkflow.ArtifactStateDraft),
			TaskID:        draftContext.Identity.TaskID,
			CreatedAt:     draftContext.Identity.CreatedAt,
		},
		Requirements: []productworkflow.PRDRequirement{{
			ID:                   "REQ-NATIVE",
			Priority:             "P1",
			Statement:            "Expose quality contributor breakdown through the native inventory binding.",
			EvidenceRefs:         []string{"EV-NATIVE"},
			TDDObligations:       []string{"TDD-NATIVE"},
			OwnerAlignment:       "Players need legible quality provenance.",
			Testability:          "bin/colony_tests -ts=quality_inventory_bindings",
			Metric:               "Native binding reports contributor rows.",
			OpenQuestionsPresent: true,
		}},
	}
	prdIntake, err := productworkflow.PersistPRDDraft(productworkflow.PRDDraftPersistenceRequest{
		EffortRoot:         effortRoot,
		Document:           prd,
		ExpectedGeneration: draftContext.Generation,
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(prdIntake.Passed).To(gomega.BeTrue(), "failures=%+v", prdIntake.Failures)
	_, err = productworkflow.SubmitPRDForReview(effortRoot)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	_, prdIntake, err = productworkflow.ApprovePRDGate(effortRoot)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(prdIntake.Passed).To(gomega.BeTrue(), "failures=%+v", prdIntake.Failures)

	adr := productworkflow.ADRDocument{
		FrontMatter: productworkflow.ADRFrontMatter{
			SchemaVersion:  "idiom.adr.v1",
			ADRID:          "ADR-NATIVE",
			Status:         string(productworkflow.ArtifactStateDraft),
			PRDRefs:        []string{"REQ-NATIVE"},
			EvidenceRefs:   []string{"EV-NATIVE"},
			TDDObligations: []string{"TDD-NATIVE"},
			CoverageTier:   "unit",
		},
		BodyMarkdown: strings.Join([]string{
			"## Context",
			"The product atom targets Colony native bindings.",
			"## Decision",
			"Keep implementation scope in native C++ and test files.",
			"## Alternatives Considered",
			"Creating a new Go package was rejected.",
			"## Evidence",
			"EV-NATIVE",
			"## Consequences",
			"The planner must keep edit paths in native scope.",
			"## Compatibility and Migration",
			"No migration.",
			"## TDD Obligations",
			"TDD-NATIVE",
			"## Rollback",
			"Revert native binding changes.",
			"## Open Questions",
			"None.",
		}, "\n\n"),
	}
	adrGenerationContext, err := productworkflow.LoadADRGenerationContext(effortRoot)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	_, err = productworkflow.PersistADRDrafts(productworkflow.ADRDraftPersistenceRequest{
		EffortRoot: effortRoot,
		Documents:  []productworkflow.ADRDocument{adr},
		IndexEntries: []productworkflow.ADRIndexEntry{{
			ADRID: "ADR-NATIVE", Title: "Use native bindings", Status: string(productworkflow.ArtifactStateDraft),
			Path: "001-native-binding.md", CoverageTier: "unit", PRDRefs: []string{"REQ-NATIVE"},
		}},
		ExpectedGeneration: adrGenerationContext.Generation,
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	_, err = productworkflow.SubmitADRsForReview(effortRoot)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	_, adrIntakes, err := productworkflow.ApproveADRGate(effortRoot)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	for _, intake := range adrIntakes {
		gomega.Expect(intake.Passed).To(gomega.BeTrue(), "failures=%+v", intake.Failures)
	}

	workflowState, err := productworkflow.LoadWorkflowState(effortRoot)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(workflowState.CurrentPhase).To(gomega.Equal(string(productworkflow.PhaseTDDSynthesis)))
	workflowState.ArtifactPaths["tdd"] = "tdd.json"
	workflowState.Context = productworkflow.WorkflowContext{
		ContextPackID:   "native-pack",
		FreshnessState:  "current",
		ReferencedPaths: []string{"native/src/core/colony_sim.h", "native/tests/test_quality_inventory_bindings.cpp"},
	}
	workflowState.Coverage = productworkflow.CoverageState{CoverageTier: "unit"}
	workflowState.DurabilityLevel = "source_controlled"
	workflowState.NextValidActions = []string{"finalize"}
	writeFeaturePlannerCommandJSON(t, filepath.Join(effortRoot, "workflow_state.json"), workflowState)
	taskID := workflowState.TaskID

	writeFeaturePlannerCommandJSON(t, filepath.Join(effortRoot, "tdd.json"), productworkflow.TDDLedger{
		SchemaVersion: "idiom.tdd_ledger.v1",
		EffortID:      "native-quality",
		TaskID:        taskID,
		CoverageTier:  "unit",
		Coverage: productworkflow.TDDCoverage{
			EARSTotal:        1,
			EARSMapped:       1,
			GatesPassed:      []string{"contract"},
			MutationScore:    featurePlannerCommandFloatPtr(1),
			MutationMinScore: 1,
		},
		Obligations: []productworkflow.TDDObligation{{
			ID:             "TDD-NATIVE",
			Type:           "regression",
			Behavior:       "Quality contributor rows are visible through native bindings.",
			Owner:          "native/tests/test_quality_inventory_bindings.cpp",
			Command:        "bin/colony_tests -ts=quality_inventory_bindings",
			RequiredBefore: "implementation",
			Status:         "planned",
		}},
	})
	writeFeaturePlannerCommandJSON(t, filepath.Join(effortRoot, "evidence", "traceability_matrix.json"), productworkflow.TraceabilityMatrix{
		SchemaVersion: "idiom.traceability_matrix.v1",
		EffortID:      "native-quality",
		TaskID:        taskID,
		CreatedAt:     "2026-06-05T00:03:00Z",
		Matrix: []productworkflow.TraceabilityRow{{
			EvidenceCard:   "EV-NATIVE",
			PRDRequirement: "REQ-NATIVE",
			ADR:            "ADR-NATIVE",
			TDDObligation:  "TDD-NATIVE",
			Brief:          "briefs/mwe-1.3b-quality-contributor-breakdown.md",
			Report:         "reports/mwe-1.3b.json",
		}},
	})
	gomega.Expect(os.MkdirAll(filepath.Join(effortRoot, "briefs"), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(
		filepath.Join(effortRoot, "briefs", "mwe-1.3b-quality-contributor-breakdown.md"),
		[]byte("# Native quality contributor implementation brief\n"),
		0o644,
	)).To(gomega.Succeed())
	gomega.Expect(os.MkdirAll(filepath.Join(effortRoot, "reports"), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(
		filepath.Join(effortRoot, "reports", "mwe-1.3b.json"),
		[]byte("{}\n"),
		0o644,
	)).To(gomega.Succeed())

	records := []productworkflow.ImplementationLedgerRecord{{
		RecordType:     productworkflow.LedgerRecordAtomPlanned,
		TaskID:         taskID,
		EffortID:       "native-quality",
		Timestamp:      "2026-06-05T00:04:00Z",
		ComplexityTier: "C1",
		ContextPackID:  "native-pack",
		Implementor:    &productworkflow.ImplementorAssignment{ID: "impl-native", Runtime: "idiom", Model: "qwen", Command: "idiom lane plan-execute"},
		Reviewer:       &productworkflow.ReviewerAssignment{ID: "review-native", Runtime: "codex", Model: "gpt-5", ReasoningEffort: "medium"},
		Scope: &productworkflow.AtomScope{
			ExpectedFiles:     []string{"native/src/core/colony_sim.h", "native/tests/test_quality_inventory_bindings.cpp"},
			ForbiddenPatterns: []string{"docs/product-workflows/**"},
		},
		RequiredVerification: []string{"bin/colony_tests -ts=quality_inventory_bindings"},
		ReportPaths: &productworkflow.ReportPaths{
			Implementation: "reports/mwe-1.3b-implementation.json",
			Review:         "reports/mwe-1.3b-review.json",
			Investigation:  "reports/mwe-1.3b-investigation.json",
		},
	}, {
		RecordType:              productworkflow.LedgerRecordVerificationCompleted,
		TaskID:                  taskID,
		EffortID:                "native-quality",
		Timestamp:               "2026-06-05T00:05:00Z",
		TDDObligationsSatisfied: []string{"TDD-NATIVE"},
	}}
	writeFeaturePlannerCommandJSONL(t, filepath.Join(effortRoot, "implementation_ledger.jsonl"), records)
}

func featurePlannerCommandFloatPtr(value float64) *float64 {
	return &value
}

func featurePlannerCommandBadGoPlanJSON(t ginkgo.FullGinkgoTInterface) string {
	ginkgo.GinkgoHelper()

	plan := featureplanner.FeaturePlan{
		Tool:          string(featureplannercontract.ToolSubmitFeaturePlan),
		SchemaVersion: featureplanner.FeaturePlanSchemaVersion,
		FeatureID:     "feature-qcb",
		SourceTask:    featureplanner.SourceTask{Kind: "operator_prompt", Summary: "native quality contributor breakdown"},
		Summary:       "Expose quality contributor breakdown.",
		TopLevelScope: featureplanner.TopLevelScope{
			Objective:       "Expose quality contributor breakdown.",
			SuccessCriteria: []string{"Contributor rows are visible."},
		},
		SDDContract: featureplanner.SDDContract{
			PRDRequirementRefs: []string{"REQ-NATIVE"},
			ADRRefs:            []string{"ADR-NATIVE"},
			Acceptance:         []string{"Contributor rows are visible."},
		},
		TDDContract: featureplanner.TDDContract{
			CoverageTier:                 "unit",
			RequiredBeforeImplementation: true,
			Obligations: []featureplanner.TDDObligation{{
				ID:             "TDD-1",
				Behavior:       "Go binding returns contributors.",
				Command:        "go test ./internal/qcb/...",
				RequiredBefore: "implementation",
			}},
		},
		Atoms: []featureplanner.FeatureAtomPlan{{
			SchemaVersion:     featureplanner.FeatureAtomSchemaVersion,
			ID:                "atom-qcb-go",
			Title:             "Add Go QCB binding",
			Summary:           "Adds an invented Go binding.",
			RequiredEditPaths: []string{"internal/qcb/binding.go"},
			AllowedEditPaths:  []string{"internal/qcb/binding.go", "internal/qcb/binding_test.go"},
			SDDRefs:           []string{"REQ-NATIVE", "ADR-NATIVE"},
			TDDObligations:    []string{"TDD-1"},
			RequiredTests:     []string{"go test ./internal/qcb/..."},
			TestFirst: featureplanner.TestFirstPlan{
				ObligationRefs:             []string{"TDD-1"},
				Commands:                   []string{"go test ./internal/qcb/..."},
				BaselineRequired:           true,
				TestArtifactPaths:          []string{"internal/qcb/binding_test.go"},
				PreExecutionEventIDs:       []string{"evt-tdd-go"},
				ExpectedPreExecutionResult: "red-confirmed",
				CoverageGateEventID:        "evt-tdd-go",
			},
			PlannedTDDSnapshot: featureplanner.PlannedTDDSnapshot{
				SnapshotID:                   "snap-qcb-go",
				SnapshotDigest:               "sha256:test",
				ObligationIDs:                []string{"TDD-1"},
				TestCommands:                 []string{"go test ./internal/qcb/..."},
				TestArtifactPaths:            []string{"internal/qcb/binding_test.go"},
				ExpectedPreExecutionResults:  []string{"red-confirmed"},
				CreatedBeforeDispatchEventID: "evt-tdd-go",
			},
			Prompt: "Implement the binding in Go.",
		}},
	}

	return featurePlannerCommandPlanJSON(t, plan)
}

func featurePlannerCommandDemotedFilesPlanJSON(t ginkgo.FullGinkgoTInterface) string {
	ginkgo.GinkgoHelper()

	plan := featurePlannerCommandNativePlan()

	plan.Atoms[0].RequiredEditPaths = []string{"native/src/core/colony_sim.h"}
	plan.Atoms[0].AllowedEditPaths = []string{"native/tests/test_quality_inventory_bindings.cpp"}

	return featurePlannerCommandPlanJSON(t, plan)
}

func featurePlannerCommandNativePlan() featureplanner.FeaturePlan {
	return featureplanner.FeaturePlan{
		Tool:          string(featureplannercontract.ToolSubmitFeaturePlan),
		SchemaVersion: featureplanner.FeaturePlanSchemaVersion,
		FeatureID:     "feature-qcb",
		SourceTask:    featureplanner.SourceTask{Kind: "operator_prompt", Summary: "native quality contributor breakdown"},
		Summary:       "Expose quality contributor breakdown.",
		TopLevelScope: featureplanner.TopLevelScope{
			Objective:       "Expose quality contributor breakdown.",
			SuccessCriteria: []string{"Contributor rows are visible."},
		},
		SDDContract: featureplanner.SDDContract{
			PRDRequirementRefs: []string{"REQ-NATIVE"},
			ADRRefs:            []string{"ADR-NATIVE"},
			Acceptance:         []string{"Contributor rows are visible."},
		},
		TDDContract: featureplanner.TDDContract{
			CoverageTier:                 "unit",
			RequiredBeforeImplementation: true,
			Obligations: []featureplanner.TDDObligation{{
				ID:             "TDD-NATIVE",
				Behavior:       "Quality contributor rows render.",
				Command:        "bin/colony_tests -ts=quality_inventory_bindings",
				RequiredBefore: "implementation",
			}},
		},
		Atoms: []featureplanner.FeatureAtomPlan{featurePlannerCommandNativeAtom()},
	}
}

func featurePlannerCommandNativeAtom() featureplanner.FeatureAtomPlan {
	return featureplanner.FeatureAtomPlan{
		SchemaVersion:     featureplanner.FeatureAtomSchemaVersion,
		ID:                "atom-qcb-native",
		Title:             "Expose native quality contributors",
		Summary:           "Adds native contributor rows.",
		RequiredEditPaths: []string{"native/src/core/colony_sim.h", "native/tests/test_quality_inventory_bindings.cpp"},
		SDDRefs:           []string{"REQ-NATIVE", "ADR-NATIVE"},
		TDDObligations:    []string{"TDD-NATIVE"},
		RequiredTests:     []string{"bin/colony_tests -ts=quality_inventory_bindings"},
		TestFirst: featureplanner.TestFirstPlan{
			ObligationRefs:             []string{"TDD-NATIVE"},
			Commands:                   []string{"bin/colony_tests -ts=quality_inventory_bindings"},
			BaselineRequired:           true,
			TestArtifactPaths:          []string{"native/tests/test_quality_inventory_bindings.cpp"},
			PreExecutionEventIDs:       []string{"evt-tdd-native"},
			ExpectedPreExecutionResult: "red-confirmed",
			CoverageGateEventID:        "evt-tdd-native",
		},
		PlannedTDDSnapshot: featureplanner.PlannedTDDSnapshot{
			SnapshotID:                   "snap-qcb-native",
			SnapshotDigest:               "sha256:test",
			ObligationIDs:                []string{"TDD-NATIVE"},
			TestCommands:                 []string{"bin/colony_tests -ts=quality_inventory_bindings"},
			TestArtifactPaths:            []string{"native/tests/test_quality_inventory_bindings.cpp"},
			ExpectedPreExecutionResults:  []string{"red-confirmed"},
			CreatedBeforeDispatchEventID: "evt-tdd-native",
		},
		Prompt: "Implement the native quality contributor rows.",
	}
}

func featurePlannerCommandStandaloneCollapsePlan() featureplanner.FeaturePlan {
	plan := featurePlannerCommandNativePlan()
	plan.FeatureID = "feature-collapse-event"
	plan.SourceTask = featureplanner.SourceTask{Kind: "operator_prompt", Summary: "Implement CollapseEvent helper."}
	plan.Summary = "Route collapse dispatch through a slim event helper."
	plan.TopLevelScope = featureplanner.TopLevelScope{
		Objective:       "Route collapse dispatch through a slim event helper.",
		SuccessCriteria: []string{"Collapse dispatch uses the helper."},
	}
	plan.SDDContract = featureplanner.SDDContract{
		Acceptance: []string{"SDD-COLLAPSE-EVENT-1: collapse dispatch routes through a slim event helper"},
	}
	plan.TDDContract.Obligations = []featureplanner.TDDObligation{{
		ID:             "TDD-COLLAPSE-EVENT",
		Behavior:       "Collapse dispatch routes through helper.",
		Command:        "go test ./pkg",
		RequiredBefore: "implementation",
	}}
	plan.Atoms = []featureplanner.FeatureAtomPlan{{
		SchemaVersion:     featureplanner.FeatureAtomSchemaVersion,
		ID:                "atom-collapse-event",
		Title:             "CollapseEvent helper",
		Summary:           "Create CollapseEvent helper and route dispatch through it.",
		RequiredEditPaths: []string{"pkg/collapse_event.go", "pkg/collapse_event_test.go"},
		SDDRefs:           []string{"SDD-COLLAPSE-EVENT-1"},
		TDDObligations:    []string{"TDD-COLLAPSE-EVENT"},
		RequiredTests:     []string{"go test ./pkg"},
		TestFirst: featureplanner.TestFirstPlan{
			ObligationRefs:             []string{"TDD-COLLAPSE-EVENT"},
			Commands:                   []string{"go test ./pkg"},
			BaselineRequired:           true,
			TestArtifactPaths:          []string{"pkg/collapse_event_test.go"},
			PreExecutionEventIDs:       []string{"evt-tdd-collapse-event"},
			ExpectedPreExecutionResult: "red-confirmed",
			CoverageGateEventID:        "evt-tdd-collapse-event",
		},
		PlannedTDDSnapshot: featureplanner.PlannedTDDSnapshot{
			SnapshotID:                   "snap-collapse-event",
			SnapshotDigest:               "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			ObligationIDs:                []string{"TDD-COLLAPSE-EVENT"},
			TestCommands:                 []string{"go test ./pkg"},
			TestArtifactPaths:            []string{"pkg/collapse_event_test.go"},
			ExpectedPreExecutionResults:  []string{"red-confirmed"},
			CreatedBeforeDispatchEventID: "evt-tdd-collapse-event",
		},
		Prompt: "Implement the CollapseEvent helper.",
	}}
	return plan
}

func featurePlannerCommandRunningExampleExistingPathPlan() featureplanner.FeaturePlan {
	plan := featurePlannerCommandNativePlan()
	plan.FeatureID = "llm-brain-memory-mvp"
	plan.SourceTask = featureplanner.SourceTask{Kind: "operator_prompt", Summary: "Plan an LLM brain MVP."}
	plan.Summary = "MVP for LLM Brain long-term agentic memory management."
	plan.TopLevelScope = featureplanner.TopLevelScope{
		Objective: "Implement an MVP of an LLM Brain that stores and retrieves agentic memory entries with basic query handling and local persistence.",
		SuccessCriteria: []string{
			"Memory entries can be created with content and metadata.",
			"Memory entries can be retrieved by ID or query handler flow.",
			"Persistence to a local file store works across sessions.",
			"Project verification runs with the approved test command.",
		},
	}
	plan.SDDContract = featureplanner.SDDContract{
		Acceptance: []string{"SDD-ACCEPTANCE-1: memory entry creation and retrieval through query_handler."},
	}
	plan.TDDContract.Obligations = []featureplanner.TDDObligation{{
		ID:             "TDD-1",
		Behavior:       "Memory entry creation and retrieval via query_handler.",
		Command:        "pytest tests/test_query_handler.py",
		RequiredBefore: "implementation",
	}}
	plan.Atoms = []featureplanner.FeatureAtomPlan{{
		SchemaVersion:     featureplanner.FeatureAtomSchemaVersion,
		ID:                "atom-memory-query-handler",
		Title:             "Implement memory storage and retrieval in query_handler",
		Summary:           "Add memory entry creation, retrieval, and persistence behavior.",
		RequiredEditPaths: []string{"llm_brain/query_handler.py", "tests/test_query_handler.py"},
		SDDRefs:           []string{"SDD-ACCEPTANCE-1"},
		TDDObligations:    []string{"TDD-1"},
		RequiredTests:     []string{"pytest tests/test_query_handler.py"},
		TestFirst: featureplanner.TestFirstPlan{
			ObligationRefs:             []string{"TDD-1"},
			Commands:                   []string{"pytest tests/test_query_handler.py"},
			BaselineRequired:           true,
			TestArtifactPaths:          []string{"tests/test_query_handler.py"},
			PreExecutionEventIDs:       []string{"evt-tdd-memory-query-handler"},
			ExpectedPreExecutionResult: "red-confirmed",
			CoverageGateEventID:        "evt-tdd-memory-query-handler",
		},
		PlannedTDDSnapshot: featureplanner.PlannedTDDSnapshot{
			SnapshotID:                   "snap-memory-query-handler",
			SnapshotDigest:               "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			ObligationIDs:                []string{"TDD-1"},
			TestCommands:                 []string{"pytest tests/test_query_handler.py"},
			TestArtifactPaths:            []string{"tests/test_query_handler.py"},
			ExpectedPreExecutionResults:  []string{"red-confirmed"},
			CreatedBeforeDispatchEventID: "evt-tdd-memory-query-handler",
		},
		Prompt: "Implement memory storage and retrieval in llm_brain/query_handler.py. Add methods: create_memory(content) -> MemoryEntry, get_memory(id) -> MemoryEntry. Use a simple in-memory dict for storage, and add persistence to a JSON file 'memory_store.json'. In tests/test_query_handler.py, write tests for creation and retrieval, including edge cases like empty content. Run tests with 'pytest tests/test_query_handler.py' and ensure they pass.",
	}}
	plan.BehavioralCoverageDraft = &featureplanner.BehavioralCoverageDraft{
		Authority: "standalone_provisional",
		Status:    "draft",
		Tier:      "contract_high",
		Requirements: []featureplanner.CoverageDraftRequirement{{
			ID:          "REQ-1",
			Pattern:     "event-driven",
			Statement:   "When a memory entry is created, the system shall store it and allow retrieval by query handler flow.",
			Refs:        []string{"SDD-ACCEPTANCE-1"},
			Obligations: []string{"TDD-1"},
		}},
		ExampleRules: []featureplanner.CoverageDraftExampleRule{{
			ID:            "RULE-1",
			RequirementID: "REQ-1",
			Rule:          "Memory entries are stored with a unique ID, content, and timestamp.",
			Examples: []featureplanner.CoverageDraftExample{{
				ID:           "EX-1",
				Description:  "Create and retrieve a memory entry",
				ConcreteData: map[string]string{"input": `{"content":"test memory"}`, "expected": `{"content":"test memory"}`},
			}},
		}},
		Scenarios: []featureplanner.CoverageDraftScenario{{
			ID:            "BDD-1",
			RequirementID: "REQ-1",
			RuleID:        "RULE-1",
			Given:         []string{"A query_handler instance"},
			When:          []string{"A memory entry is created with content 'test memory'"},
			Then:          []string{"The entry is stored and can be retrieved by its ID"},
			ConcreteData:  map[string]string{"input": `{"content":"test memory"}`, "expected": `{"content":"test memory"}`},
			Obligations:   []string{"TDD-1"},
			Tags:          []string{"mvp"},
		}},
		Invariants: []featureplanner.CoverageDraftInvariant{{
			ID:                    "INV-1",
			Statement:             "All memory entries have a unique ID.",
			PropertyBasedTestRefs: []string{"TestMemoryIDsUnique"},
			Obligations:           []string{"TDD-1"},
		}},
		FailureModes: []featureplanner.CoverageDraftFailureMode{{
			ID:                 "FM-1",
			Mode:               "Empty content",
			Mitigation:         "Reject with an error message.",
			RPN:                1,
			MitigationTestRefs: []string{"TestEmptyContentRejected"},
			Obligations:        []string{"TDD-1"},
		}},
		SurfaceInventory: []featureplanner.CoverageDraftSurfaceDecision{
			{ID: "SURF-1", Surface: "domain_logic", Status: "in_scope", Rationale: "memory creation and retrieval are core behavior", Obligations: []string{"TDD-1"}},
			{ID: "SURF-2", Surface: "persistence", Status: "in_scope", Rationale: "local persistence is part of the proposed MVP", Obligations: []string{"TDD-1"}},
			{ID: "SURF-3", Surface: "api_transport", Status: "not_required", Rationale: "no external API or transport boundary is requested"},
		},
		TestObligations: []featureplanner.CoverageDraftTestObligation{{
			ID:             "TDD-1",
			Type:           "unit",
			Behavior:       "Memory entry creation and retrieval via query_handler.",
			Owner:          "standalone feature planner",
			Command:        "pytest tests/test_query_handler.py",
			RequiredBefore: "implementation",
		}},
		Verification: []featureplanner.CoverageDraftVerification{{
			ID:          "VERIFY-1",
			Gate:        "required test command passes",
			Command:     "pytest tests/test_query_handler.py",
			Obligations: []string{"TDD-1"},
		}},
	}
	return plan
}

func featurePlannerCommandNewFileQuestionJSON(t ginkgo.FullGinkgoTInterface) string {
	ginkgo.GinkgoHelper()
	question := featureplanner.PlannerQuestion{
		QuestionSummary:   "Authorize CollapseEvent new files",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "Missing helper and test files require operator approval.",
		RequestedDecision: "Approve creating pkg/collapse_event.go and pkg/collapse_event_test.go.",
		AuthorityRequired: "operator_approval",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     []string{"pkg/collapse_event.go", "pkg/collapse_event_test.go"},
		AffectedFields:    []string{"new_file_authorization"},
	}
	body, err := json.Marshal(struct {
		Tool string `json:"tool"`
		featureplanner.PlannerQuestion
	}{
		Tool:            string(featureplannercontract.ToolAskPlannerQuestion),
		PlannerQuestion: question,
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal question: %v", err))
	return string(body)
}

func featurePlannerCommandAcceptedSourceQuestionJSON(t ginkgo.FullGinkgoTInterface) string {
	ginkgo.GinkgoHelper()
	question := featureplanner.PlannerQuestion{
		QuestionSummary:   "Provide accepted source facts for CollapseEvent",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "Concrete CollapseEvent helper usage requires source-localized signature evidence.",
		RequestedDecision: "Provide accepted source facts for the current CollapseEvent signature.",
		AuthorityRequired: "accepted_source",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     []string{"pkg/collapse_event.go"},
		AffectedFields:    []string{"source_localization"},
	}
	body, err := json.Marshal(struct {
		Tool string `json:"tool"`
		featureplanner.PlannerQuestion
	}{
		Tool:            string(featureplannercontract.ToolAskPlannerQuestion),
		PlannerQuestion: question,
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal question: %v", err))
	return string(body)
}

func featurePlannerCommandPlanJSON(t ginkgo.FullGinkgoTInterface, plan featureplanner.FeaturePlan) string {
	ginkgo.GinkgoHelper()

	data, err := json.Marshal(plan)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal feature plan: %v", err))

	return string(data)
}

func writeFeaturePlannerCommandJSON(t ginkgo.FullGinkgoTInterface, path string, value any) {
	ginkgo.GinkgoHelper()

	data, err := json.MarshalIndent(value, "", "  ")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal %s: %v", path, err))

	{
		err := os.WriteFile(path, append(data, '\n'), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write %s: %v", path, err))
	}
}

func writeFeaturePlannerCommandJSONL(t ginkgo.FullGinkgoTInterface, path string, records []productworkflow.ImplementationLedgerRecord) {
	ginkgo.GinkgoHelper()

	var lines []string

	for _, record := range records {
		data, err := json.Marshal(record)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal ledger record: %v", err))

		lines = append(lines, string(data))
	}

	{
		err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write %s: %v", path, err))
	}
}

var _ = ginkgo.It("split and parse ad rs", func() {
	prd := productworkflow.PRDDocument{
		Requirements: []productworkflow.PRDRequirement{
			{ID: "REQ-001", Priority: "P1"},
			{ID: "REQ-002", Priority: "P2"},
		},
	}

	ginkgo.By(fmt.Sprint("single ADR"), func() {
		output := `---
schema_version: "idiom.adr.v1"
adr_id: "ADR-001"
status: "draft"
prd_refs: ["REQ-001"]
evidence_refs: ["inference"]
tdd_obligations: ["TDD-ADR-001"]
coverage_tier: "unit"
---
## Context
This is the context for the decision.

## Decision
We chose option A.

## Alternatives Considered
Option B was considered but rejected.

## Evidence
Based on PRD requirement REQ-001.

## Consequences
This will affect the auth module.

## Compatibility and Migration
No migration needed.

## TDD Obligations
TDD-ADR-001: unit test for auth flow.

## Rollback
Revert the auth module changes.

## Open Questions
- None
`

		docs, entries, _, err := productworkflow.TrySplitAndParseADRs(output, prd)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected error: %v", err))

		gomega.Expect(len(docs)).To(gomega.Equal(1), fmt.Sprintf("expected 1 doc, got %d", len(docs)))

		gomega.Expect(docs[0].FrontMatter.ADRID).To(gomega.Equal("ADR-001"), fmt.Sprintf("expected ADR-001, got %s", docs[0].FrontMatter.ADRID))

		gomega.Expect(docs[0].Sections.HasContext).To(gomega.BeTrue(), fmt.Sprint("expected HasContext to be true"))

		gomega.Expect(docs[0].Sections.HasDecision).To(gomega.BeTrue(), fmt.Sprint("expected HasDecision to be true"))

		gomega.Expect(len(entries)).To(gomega.Equal(1), fmt.Sprintf("expected 1 index entry, got %d", len(entries)))

		gomega.Expect(entries[0].Path).To(gomega.Equal("ADR-001.md"), fmt.Sprintf("expected ADR-001.md, got %s", entries[0].Path))
	})

	ginkgo.By(fmt.Sprint("multiple ADRs with === separator"), func() {
		output := `---
schema_version: "idiom.adr.v1"
adr_id: "ADR-001"
status: "draft"
prd_refs: ["REQ-001"]
evidence_refs: ["inference"]
tdd_obligations: ["TDD-ADR-001"]
coverage_tier: "unit"
---
## Context
First ADR context.

## Decision
First decision.

## Alternatives Considered
Alt 1.

## Evidence
Evidence 1.

## Consequences
Consequence 1.

## Compatibility and Migration
Compat 1.

## TDD Obligations
TDD 1.

## Rollback
Rollback 1.

## Open Questions
None.
===
---
schema_version: "idiom.adr.v1"
adr_id: "ADR-002"
status: "draft"
prd_refs: ["REQ-002"]
evidence_refs: ["inference"]
tdd_obligations: ["TDD-ADR-002"]
coverage_tier: "unit"
---
## Context
Second ADR context.

## Decision
Second decision.

## Alternatives Considered
Alt 2.

## Evidence
Evidence 2.

## Consequences
Consequence 2.

## Compatibility and Migration
Compat 2.

## TDD Obligations
TDD 2.

## Rollback
Rollback 2.

## Open Questions
None.
`

		docs, entries, _, err := productworkflow.TrySplitAndParseADRs(output, prd)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected error: %v", err))

		gomega.Expect(len(docs)).To(gomega.Equal(2), fmt.Sprintf("expected 2 docs, got %d", len(docs)))

		gomega.Expect(docs[0].FrontMatter.ADRID).To(gomega.Equal("ADR-001"), fmt.Sprintf("expected ADR-001 first, got %s", docs[0].FrontMatter.ADRID))

		gomega.Expect(docs[1].FrontMatter.ADRID).To(gomega.Equal("ADR-002"), fmt.Sprintf("expected ADR-002 second, got %s", docs[1].FrontMatter.ADRID))

		gomega.Expect(len(entries)).To(gomega.Equal(2), fmt.Sprintf("expected 2 entries, got %d", len(entries)))
	})

	ginkgo.By(fmt.Sprint("auto-assigns ADR ID when missing"), func() {
		output := `---
schema_version: "idiom.adr.v1"
status: "draft"
prd_refs: ["REQ-001"]
evidence_refs: ["inference"]
tdd_obligations: ["TDD-ADR-001"]
coverage_tier: "unit"
---
## Context
Test.

## Decision
Test.

## Alternatives Considered
Test.

## Evidence
Test.

## Consequences
Test.

## Compatibility and Migration
Test.

## TDD Obligations
Test.

## Rollback
Test.

## Open Questions
Test.
`

		docs, _, _, err := productworkflow.TrySplitAndParseADRs(output, prd)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected error: %v", err))

		gomega.Expect(docs[0].FrontMatter.ADRID).To(gomega.Equal("ADR-001"), fmt.Sprintf("expected auto-assigned ADR-001, got %s", docs[0].FrontMatter.ADRID))
	})

	ginkgo.By(fmt.Sprint("skips invalid ADRs"), func() {
		output := `---
schema_version: "idiom.adr.v1"
adr_id: "ADR-001"
status: "draft"
prd_refs: ["REQ-001"]
evidence_refs: ["inference"]
tdd_obligations: ["TDD-ADR-001"]
coverage_tier: "unit"
---
## Context
Valid ADR.

## Decision
Valid.

## Alternatives Considered
Valid.

## Evidence
Valid.

## Consequences
Valid.

## Compatibility and Migration
Valid.

## TDD Obligations
Valid.

## Rollback
Valid.

## Open Questions
Valid.
===
garbage not an ADR
===
---
schema_version: "idiom.adr.v1"
adr_id: "ADR-002"
status: "draft"
prd_refs: ["REQ-002"]
evidence_refs: ["inference"]
tdd_obligations: ["TDD-ADR-002"]
coverage_tier: "unit"
---
## Context
Second valid ADR.

## Decision
Second.

## Alternatives Considered
Second.

## Evidence
Second.

## Consequences
Second.

## Compatibility and Migration
Second.

## TDD Obligations
Second.

## Rollback
Second.

## Open Questions
Second.
`

		docs, _, _, err := productworkflow.TrySplitAndParseADRs(output, prd)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unexpected error: %v", err))

		gomega.Expect(len(docs)).To(gomega.Equal(2), fmt.Sprintf("expected 2 valid docs (skipping garbage), got %d", len(docs)))
	})
})

var _ = ginkgo.It("plan execute answer is blocked", func() {
	for _, input := range []string{
		"",
		"<<QUESTION>>: Plan-execute could not produce a plan artifact.",
		"plan-execute: recovery-open\nblockers: required_edit_paths_remaining",
	} {
		gomega.Expect(planExecuteAnswerIsBlocked(input)).To(gomega.BeTrue(), fmt.Sprintf("expected blocked plan-execute answer for %q", input))
	}

	gomega.Expect(planExecuteAnswerIsBlocked("Decision: source-satisfied\n- internal/example/example.go: already satisfied")).To(gomega.BeFalse(), fmt.Sprint("source-satisfied answer should remain eligible for completion wrapper"))
})

var _ = ginkgo.It("product workflow feature planner recovery blocks completion", func() {
	state := featureplanner.NewState("feature-session", "prompt", "", 3, time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC))

	state.Status = featureplanner.FeatureStateDispatching
	state.Atoms = []featureplanner.FeatureAtomState{{
		FeatureAtomPlan: featureplanner.FeatureAtomPlan{
			ID:                "atom-1",
			RequiredEditPaths: []string{"internal/surfaces/tui/app/product_status.go"},
		},
		State:     featureplanner.AtomStateRecoveryOpen,
		LastRunID: "plan-execute-session-1",
		Blockers:  []string{"source sanity failed"},
	}}

	msg, blocked := formatProductWorkflowFeaturePlannerRecovery(state, ".idiom/feature-planner/feature-session/state.json")
	gomega.Expect(blocked).To(gomega.BeTrue(), fmt.Sprint("recovery-open child must block product completion"))

	for _, want := range []string{
		"feature-planner: recovery_open",
		"session_id: feature-session",
		"state_path: .idiom/feature-planner/feature-session/state.json",
		"atom=atom-1 state=recovery-open session=plan-execute-session-1 required_paths=internal/surfaces/tui/app/product_status.go",
		"diagnostics=1 internal planner diagnostic(s) saved in feature-planner state",
	} {
		gomega.Expect(strings.Contains(msg, want)).To(gomega.BeTrue(), fmt.Sprintf("recovery message missing %q:\n%s", want, msg))
	}
	gomega.Expect(strings.Contains(msg, "source sanity failed")).To(gomega.BeFalse(), fmt.Sprintf("recovery message leaked raw blocker:\n%s", msg))
})

var _ = ginkgo.It("product workflow feature planner recovery surfaces open question details", func() {
	state := featureplanner.NewState("feature-session", "prompt", "", 3, time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC))

	state.Status = featureplanner.FeatureStatePlanningQuestion
	state.Atoms = []featureplanner.FeatureAtomState{{
		FeatureAtomPlan: featureplanner.FeatureAtomPlan{
			ID:                "atom-1",
			RequiredEditPaths: []string{"internal/surfaces/tui/app/product_status.go"},
		},
		State:        featureplanner.AtomStateQuestionForPlanner,
		LastRunID:    "plan-execute-session-1",
		LastQuestion: "Plan-execute could not produce a plan artifact.",
	}}
	state.OpenQuestions = []featureplanner.PlannerQuestion{{
		AtomID:          "atom-1",
		QuestionSummary: "progress_state: .idiom/plan-execute/pe-session/state.json; failure_kind: source_sanity_failed",
		BlockingClaim:   "source_sanity: internal/surfaces/tui/app/product_status.go failed compile failed",
		EvidenceRefs:    []string{".idiom/plan-execute/pe-session/state.json"},
		Status:          "open",
	}}

	msg, blocked := formatProductWorkflowFeaturePlannerRecovery(state, ".idiom/feature-planner/feature-session/state.json")
	gomega.Expect(blocked).To(gomega.BeTrue(), fmt.Sprint("open child question must block product completion"))

	for _, want := range []string{
		"progress_state: .idiom/plan-execute/pe-session/state.json",
		"source_sanity: internal/surfaces/tui/app/product_status.go failed compile failed",
		"evidence_refs=.idiom/plan-execute/pe-session/state.json",
	} {
		gomega.Expect(strings.Contains(msg, want)).To(gomega.BeTrue(), fmt.Sprintf("recovery message missing question detail %q:\n%s", want, msg))
	}
})

var _ = ginkgo.It("product workflow feature planner recovery allows complete state", func() {
	state := featureplanner.NewState("feature-session", "prompt", "", 3, time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC))

	state.Status = featureplanner.FeatureStateComplete
	state.Atoms = []featureplanner.FeatureAtomState{{
		FeatureAtomPlan: featureplanner.FeatureAtomPlan{ID: "atom-1"},
		State:           featureplanner.AtomStateComplete,
	}}

	{
		msg, blocked := formatProductWorkflowFeaturePlannerRecovery(state, "")
		gomega.Expect(blocked).To(gomega.BeFalse(), fmt.Sprintf("complete children should not block product completion: %s", msg))
	}
})

var _ = ginkgo.It("feature planner reconciles stale active run from terminal plan execute progress", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	startedAt := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	state := featureplanner.NewState("feature-session", "prompt", "", 3, startedAt)

	state.Status = featureplanner.FeatureStateDispatching
	state.Atoms = []featureplanner.FeatureAtomState{{
		FeatureAtomPlan: featureplanner.FeatureAtomPlan{
			ID:                "atom-1",
			RequiredEditPaths: []string{"pkg/feature.go"},
		},
		State:     featureplanner.AtomStateImplementing,
		LastRunID: "atom-1-run-1",
	}}
	state.ActiveRuns = []featureplanner.ActiveRun{{
		RunID:         "atom-1-run-1",
		AtomID:        "atom-1",
		LaneSessionID: "atom-1-run-1",
		LaneFamily:    "plan-execute",
		State:         featureplanner.ActiveRunStateRunning,
		StartedAt:     startedAt.Format(time.RFC3339Nano),
	}}

	progress := planexecute.NewProgressState("atom-1-run-1", "prompt", "", []string{"pkg/feature.go"}, nil, startedAt.Add(time.Minute))

	progress.Phase = plancontract.PlanExecuteProgressPhasePlanning
	progress.Status = plancontract.PlanExecuteProgressStatusFailed
	progress.FailureKind = plancontract.PlanExecuteFailurePlannerBlocked
	progress.FailureReason = "<<QUESTION>>: pkg/feature.go source evidence unavailable"

	progressPath, err := planexecute.SaveProgressState(root, &progress)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveProgressState: %v", err))

	model := &Model{cfg: config.Config{Chat: config.Chat{ProjectRoot: root}, Workspace: config.Workspace{Root: root}}}
	session := newDetachedPromptExecutionSession(context.Background(), model)
	events := session.reconcileFeaturePlannerActivePlanExecuteRuns(&state)

	gomega.Expect(state.Status).To(gomega.Equal(featureplanner.FeatureStateRecoveryOpen), fmt.Sprintf("feature state = %q, want recovery-open", state.Status))

	gomega.Expect(state.Atoms[0].State).To(gomega.Equal(featureplanner.AtomStateRecoveryOpen), fmt.Sprintf("atom state = %q, want recovery-open", state.Atoms[0].State))

	gomega.Expect(len(state.ActiveRuns)).To(gomega.Equal(0), fmt.Sprintf("active run should be folded after durable terminal progress: %+v", state.ActiveRuns))

	gomega.Expect(len(events)).To(gomega.Equal(1), fmt.Sprintf("unexpected events: %+v", events))
	gomega.Expect(events[0].Type).To(gomega.Equal("idiom.feature_planner.plan_execute_completion_rejected"), fmt.Sprintf("unexpected events: %+v", events))

	for _, want := range []string{
		"nested plan-execute reported failure details",
		"planner_blocked",
		"pkg/feature.go source evidence unavailable",
	} {
		gomega.Expect(strings.Contains(events[0].Message, want)).To(gomega.BeTrue(), fmt.Sprintf("event message missing %q: %+v", want, events[0]))
	}

	{
		got := events[0].Fields["completion_source"]
		gomega.Expect(got).To(gomega.Equal("durable_plan_execute_progress"), fmt.Sprintf("completion_source = %v, want durable_plan_execute_progress; fields=%+v", got, events[0].Fields))
	}

	{
		got := events[0].Fields["progress_path"]
		gomega.Expect(got).To(gomega.Equal(progressPath), fmt.Sprintf("progress_path = %v, want %s; fields=%+v", got, progressPath, events[0].Fields))
	}
})

var _ = ginkgo.It("feature planner reconciles skipped review progress as complete", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	startedAt := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	plan := featureplanner.FeaturePlan{
		SchemaVersion: featureplanner.FeaturePlanSchemaVersion,
		FeatureID:     "feature-session",
		Atoms: []featureplanner.FeatureAtomPlan{{
			ID:                "atom-1",
			RequiredEditPaths: []string{"pkg/feature.go"},
		}},
	}
	contract := featureplanner.StampPlanAuthorityContract(&plan, nil)
	state := featureplanner.NewState("feature-session", "prompt", "", 3, startedAt)

	state.Status = featureplanner.FeatureStateDispatching
	state.Plan = plan
	state.Atoms = []featureplanner.FeatureAtomState{{
		FeatureAtomPlan: plan.Atoms[0],
		State:           featureplanner.AtomStateImplementing,
		LastRunID:       "atom-1-run-1",
	}}
	state.ActiveRuns = []featureplanner.ActiveRun{{
		RunID:                "atom-1-run-1",
		AtomID:               "atom-1",
		LaneSessionID:        "atom-1-run-1",
		LaneFamily:           "plan-execute",
		InvocationContractID: plan.Atoms[0].RuntimeEnvelope.ContractID,
		RuntimeEnvelopeID:    contract.ContractHash,
		State:                featureplanner.ActiveRunStateRunning,
		StartedAt:            startedAt.Format(time.RFC3339Nano),
	}}

	progress := planexecute.NewProgressState("atom-1-run-1", "prompt", "", []string{"pkg/feature.go"}, nil, startedAt.Add(time.Minute))

	progress.Phase = plancontract.PlanExecuteProgressPhaseComplete
	progress.Status = plancontract.PlanExecuteProgressStatusCompleted
	progress.ChangedPaths = []string{"pkg/feature.go"}
	progress.Executor.SourceSanity = []plancontract.PhaseEvidenceCheck{{
		Kind:   "source_sanity",
		Path:   "pkg/feature.go",
		Status: "ok",
	}}
	progress.Review.Status = plancontract.PlanExecuteReviewStatusSkipped

	progressPath, err := planexecute.SaveProgressState(root, &progress)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveProgressState: %v", err))

	model := &Model{cfg: config.Config{Chat: config.Chat{ProjectRoot: root}, Workspace: config.Workspace{Root: root}}}
	session := newDetachedPromptExecutionSession(context.Background(), model)
	events := session.reconcileFeaturePlannerActivePlanExecuteRuns(&state)

	gomega.Expect(state.Status).To(gomega.Equal(featureplanner.FeatureStateComplete), fmt.Sprintf("feature state = %q, want complete; events=%+v blockers=%+v", state.Status, events, state.Blockers))

	gomega.Expect(state.Atoms[0].State).To(gomega.Equal(featureplanner.AtomStateComplete), fmt.Sprintf("atom state = %q, want complete; events=%+v blockers=%+v", state.Atoms[0].State, events, state.Atoms[0].Blockers))

	gomega.Expect(len(state.ActiveRuns)).To(gomega.Equal(0), fmt.Sprintf("active run should be folded after durable terminal progress: %+v", state.ActiveRuns))

	gomega.Expect(len(events)).To(gomega.Equal(2), fmt.Sprintf("unexpected events: %+v", events))
	gomega.Expect(events[0].Type).To(gomega.Equal("idiom.feature_planner.atom_completed"), fmt.Sprintf("unexpected events: %+v", events))
	gomega.Expect(events[1].Type).To(gomega.Equal("idiom.feature_planner.release_blocked"), fmt.Sprintf("unexpected events: %+v", events))

	{
		got := events[0].Fields["review_repair_skipped"]
		gomega.Expect(got).To(gomega.Equal(true), fmt.Sprintf("review_repair_skipped = %v, want true; fields=%+v", got, events[0].Fields))
	}

	{
		got := events[0].Fields["progress_path"]
		gomega.Expect(got).To(gomega.Equal(progressPath), fmt.Sprintf("progress_path = %v, want %s; fields=%+v", got, progressPath, events[0].Fields))
	}
})

var _ = ginkgo.It("plan execute execution recovery open message surfaces source sanity", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	state := planexecute.NewProgressState("pe-session", "prompt", "", []string{"internal/surfaces/tui/app/product_status.go"}, nil, time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC))

	state.Phase = plancontract.PlanExecuteProgressPhaseReviewRepair
	state.Status = plancontract.PlanExecuteProgressStatusFailed
	state.FailureKind = plancontract.PlanExecuteFailureSourceSanityFailed
	state.FailureReason = "source_sanity failed for generated source"
	state.Executor.SourceSanity = []plancontract.PhaseEvidenceCheck{{
		Kind:   "source_sanity",
		Path:   "internal/surfaces/tui/app/product_status.go",
		Status: "failed",
		Error:  "compile failed",
	}}

	statePath, err := planexecute.SaveProgressState(root, &state)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("SaveProgressState: %v", err))

	msg, ok := humanPlanExecuteExecutionRecoveryOpenMessage(root, planexecute.RunResult{
		ExitCode:        1,
		SessionID:       "pe-session",
		ResumeStatePath: statePath,
		FailureKind:     string(plancontract.PlanExecuteFailureSourceSanityFailed),
		FailureReason:   "source_sanity failed for generated source",
	}, nil)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprint("expected source-sanity failure to surface as recovery-open"))

	for _, want := range []string{
		"plan-execute: recovery_open",
		"session_id: pe-session",
		"failure_kind: source_sanity_failed",
		"required_edit_paths:\n- internal/surfaces/tui/app/product_status.go",
		"source_sanity:\n- internal/surfaces/tui/app/product_status.go failed compile failed",
	} {
		gomega.Expect(strings.Contains(msg, want)).To(gomega.BeTrue(), fmt.Sprintf("source-sanity recovery message missing %q:\n%s", want, msg))
	}
})

var _ = ginkgo.It("infer product workflow files to modify from adr body", func() {
	body := "## Consequences\n- New package internal/greeting with greeting.go and greeting_test.go\n"
	got := inferProductWorkflowFilesToModify(body)

	for _, want := range []string{"internal/greeting/greeting.go", "internal/greeting/greeting_test.go"} {
		found := false

		for _, path := range got {
			if path == want {
				found = true
				break
			}
		}

		gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprintf("missing inferred path %q from %#v", want, got))
	}
})

var _ = ginkgo.It("build prd generation prompt", func() {
	identity := productworkflow.PRDDraftIdentity{
		PRDID:     "PRD-AUTH-001",
		TaskID:    "TASK-AUTH-001",
		CreatedAt: "2026-07-12T00:00:00Z",
	}
	prompt := buildPRDGenerationPrompt("build user authentication", identity)
	gomega.Expect(strings.Contains(prompt, "build user authentication")).To(gomega.BeTrue(), fmt.Sprint("prompt should contain feature description"))

	gomega.Expect(strings.Contains(prompt, "idiom.prd.v1")).To(gomega.BeTrue(), fmt.Sprint("prompt should contain schema version"))
	gomega.Expect(prompt).To(gomega.ContainSubstring("prd_id: " + identity.PRDID))
	gomega.Expect(prompt).To(gomega.ContainSubstring("task_id: " + identity.TaskID))
	gomega.Expect(prompt).To(gomega.ContainSubstring("created_at: " + identity.CreatedAt))

	gomega.Expect(strings.Contains(prompt, "**id**: REQ-001")).To(gomega.BeTrue(), fmt.Sprint("prompt should contain example requirement format"))

	gomega.Expect(strings.Contains(prompt, "open_questions")).To(gomega.BeTrue(), fmt.Sprint("prompt should contain open_questions field"))
})

var _ = ginkgo.It("build adr generation prompt", func() {
	prompt := buildADRGenerationPrompt("PRD content here")
	gomega.Expect(strings.Contains(prompt, "PRD content here")).To(gomega.BeTrue(), fmt.Sprint("prompt should contain PRD content"))

	gomega.Expect(strings.Contains(prompt, "===")).To(gomega.BeTrue(), fmt.Sprint("prompt should mention === separator"))

	gomega.Expect(strings.Contains(prompt, "## Context")).To(gomega.BeTrue(), fmt.Sprint("prompt should list required sections"))

	gomega.Expect(strings.Contains(prompt, "idiom.adr.v1")).To(gomega.BeTrue(), fmt.Sprint("prompt should contain schema version"))
})

var _ = ginkgo.It("first non empty", func() {
	{
		got := firstNonEmpty("", "b", "c")
		gomega.Expect(got).To(gomega.Equal("b"), fmt.Sprintf("expected b, got %s", got))
	}

	{
		got := firstNonEmpty("a", "b")
		gomega.Expect(got).To(gomega.Equal("a"), fmt.Sprintf("expected a, got %s", got))
	}

	{
		got := firstNonEmpty("", "", "")
		gomega.Expect(got).To(gomega.Equal(""), fmt.Sprintf("expected empty, got %s", got))
	}
})

var _ = ginkgo.It("truncate", func() {
	{
		got := textutil.Truncate("hello", 10)
		gomega.Expect(got).To(gomega.Equal("hello"), fmt.Sprintf("expected hello, got %s", got))
	}

	{
		got := textutil.Truncate("hello world", 5)
		gomega.Expect(got).To(gomega.Equal("he..."), fmt.Sprintf("expected he..., got %s", got))
	}

	{
		got := textutil.Truncate("hello world", 11)
		gomega.Expect(got).To(gomega.Equal("hello world"), fmt.Sprintf("expected hello world, got %s", got))
	}
})
