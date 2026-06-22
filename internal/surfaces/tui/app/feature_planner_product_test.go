package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
	"idiom/internal/workflows/featureplanner"
	"idiom/internal/workflows/featureplannerproduct"
	"idiom/internal/workflows/planexecute"
	"idiom/internal/workflows/productworkflow"
)

func TestFeaturePlannerTUIDirectCommandUsesProductContextFromMentionedEffortRoot(t *testing.T) {
	projectRoot := t.TempDir()
	effortRel := filepath.Join("docs", "product-workflows", "efforts", "started", "native-quality")
	effortRoot := filepath.Join(projectRoot, effortRel)
	writeNativeQualityEffort(t, effortRoot)

	m := &Model{
		client: &scriptedLLMClient{responses: []string{featurePlannerCommandBadGoPlanJSON(t)}},
		cfg: config.Config{
			Chat:      config.Chat{ProjectRoot: projectRoot},
			Workspace: config.Workspace{Root: projectRoot, CWD: projectRoot},
		},
		workspace: workspace.NewManager(config.Workspace{Root: projectRoot, CWD: projectRoot}),
	}

	answer, err := m.runFeaturePlannerCommandOnce(context.Background(), "Continue existing effort at "+filepath.ToSlash(effortRel)+".")
	if err != nil {
		t.Fatalf("runFeaturePlannerCommandOnce: %v", err)
	}

	if !strings.Contains(answer, "feature-planner: "+featureplanner.FeatureStatePlanningRepair) {
		t.Fatalf("expected planning-repair answer, got:\n%s", answer)
	}

	for _, want := range []string{"internal/qcb/binding.go", "TDD-1"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected blocker to mention %q, got:\n%s", want, answer)
		}
	}

	if m.lastPlanExecuteRunResult != nil {
		t.Fatalf("plan-execute should not be dispatched after product context rejection")
	}
}

func TestFeaturePlannerTUIDirectPlanValidatesBeforeProductNormalization(t *testing.T) {
	projectRoot := t.TempDir()
	effortRel := filepath.Join("docs", "product-workflows", "efforts", "started", "native-quality")
	effortRoot := filepath.Join(projectRoot, effortRel)
	writeNativeQualityEffort(t, effortRoot)

	m := &Model{
		client: &scriptedLLMClient{responses: []string{featurePlannerCommandDemotedFilesPlanJSON(t)}},
		cfg: config.Config{
			Chat:      config.Chat{ProjectRoot: projectRoot},
			Workspace: config.Workspace{Root: projectRoot, CWD: projectRoot},
		},
		workspace: workspace.NewManager(config.Workspace{Root: projectRoot, CWD: projectRoot}),
	}

	answer, err := m.runFeaturePlannerCommandOnce(context.Background(), "Continue existing effort at "+filepath.ToSlash(effortRel)+".")
	if err != nil {
		t.Fatalf("runFeaturePlannerCommandOnce: %v", err)
	}

	for _, want := range []string{
		"feature-planner: " + featureplanner.FeatureStatePlanningRepair,
		"native/tests/test_quality_inventory_bindings.cpp",
		"not omitted or demoted to allowed_edit_paths",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected answer to contain %q, got:\n%s", want, answer)
		}
	}

	if m.lastPlanExecuteRunResult != nil {
		t.Fatalf("plan-execute should not be dispatched after raw product scope rejection")
	}
}

func TestFeaturePlannerTUIDirectAnswerResumesOpenQuestionSession(t *testing.T) {
	projectRoot := t.TempDir()
	writeFile(t, projectRoot, "go.mod", "module example.com/tui\n")
	plan := featurePlannerCommandStandaloneCollapsePlan()
	plan.Atoms[0].SDDRefs = nil

	client := &scriptedLLMClient{responses: []string{
		featurePlannerCommandNewFileQuestionJSON(t),
		featurePlannerCommandPlanJSON(t, plan),
	}}
	m := &Model{
		client: client,
		cfg: config.Config{
			Chat:      config.Chat{ProjectRoot: projectRoot},
			Workspace: config.Workspace{Root: projectRoot, CWD: projectRoot},
		},
		workspace: workspace.NewManager(config.Workspace{Root: projectRoot, CWD: projectRoot}),
	}

	answer, err := m.runFeaturePlannerCommandOnce(context.Background(), "Implement CollapseEvent helper.")
	if err != nil {
		t.Fatalf("runFeaturePlannerCommandOnce: %v", err)
	}
	if !strings.Contains(answer, "Feature planner needs your approval before it can continue.") ||
		!strings.Contains(answer, "Proposed paths:") {
		t.Fatalf("expected planning question answer, got:\n%s", answer)
	}

	sessionID := m.directFeaturePlannerSessionID
	if sessionID == "" {
		t.Fatal("direct feature-planner session should be remembered after open question")
	}
	if !m.directFeaturePlannerHasOpenQuestion() {
		t.Fatal("direct feature-planner session should report an open question")
	}

	answer, err = m.runDirectFeaturePlannerAnswerOnce(context.Background(), "Approved.")
	if err != nil {
		t.Fatalf("runDirectFeaturePlannerAnswerOnce: %v", err)
	}
	if len(client.messages) < 2 || !strings.Contains(client.messages[1][len(client.messages[1])-1].Content, "User request:\nImplement CollapseEvent helper.") {
		t.Fatalf("resumed planner prompt lost original objective: %#v", client.messages)
	}
	if strings.Contains(client.messages[1][len(client.messages[1])-1].Content, "User request:\nApproved.") {
		t.Fatalf("resumed planner prompt used approval answer as objective:\n%s", client.messages[1][len(client.messages[1])-1].Content)
	}
	if !strings.Contains(answer, "feature-planner: "+featureplanner.FeatureStatePlanningRepair) ||
		!strings.Contains(answer, "missing sdd_refs") {
		t.Fatalf("expected resumed planning-repair answer with SDD blocker, got:\n%s", answer)
	}

	if m.directFeaturePlannerSessionID != sessionID {
		t.Fatalf("direct session changed from %q to %q", sessionID, m.directFeaturePlannerSessionID)
	}

	state := loadFeaturePlannerCommandStateFile(t, filepath.Join(projectRoot, ".idiom", "feature-planner", sessionID, "state.json"))
	if len(state.OpenQuestions) != 1 || state.OpenQuestions[0].Status != "answered" {
		t.Fatalf("question was not answered in durable state: %+v", state.OpenQuestions)
	}
	if len(state.AuthorityDeltas) != 1 || !slices.Equal(state.AuthorityDeltas[0].AffectedPaths, []string{"pkg/collapse_event.go", "pkg/collapse_event_test.go"}) {
		t.Fatalf("typed authority delta was not preserved: %+v", state.AuthorityDeltas)
	}
	if len(state.RejectedPlanCandidates) != 1 || !strings.Contains(state.RejectedPlanCandidates[0].Diagnostic, "missing sdd_refs") {
		t.Fatalf("rejected candidate evidence missing: %+v", state.RejectedPlanCandidates)
	}
	if strings.TrimSpace(state.PlanPath) == "" {
		t.Fatal("rejected feature plan candidate should be written to the Plan tab")
	}
	planTabBody, err := os.ReadFile(filepath.Join(projectRoot, filepath.FromSlash(state.PlanPath)))
	if err != nil {
		t.Fatalf("read feature planner plan tab artifact: %v", err)
	}
	for _, want := range []string{
		"# Feature Planner Candidate",
		"Status: " + featureplanner.FeatureStatePlanningRepair,
		"Route collapse dispatch through a slim event helper.",
		"pkg/collapse_event.go",
		"pkg/collapse_event_test.go",
		"## Test Obligations",
		"TDD-COLLAPSE-EVENT - Collapse dispatch routes through helper.",
		"## Invariants",
		"go test ./pkg",
		"CollapseEvent helper",
	} {
		if !strings.Contains(string(planTabBody), want) {
			t.Fatalf("plan tab artifact missing %q:\n%s", want, string(planTabBody))
		}
	}
	if strings.Contains(string(planTabBody), "**") {
		t.Fatalf("plan tab artifact should avoid raw inline markdown styling:\n%s", string(planTabBody))
	}

	m.planPath = filepath.Join(projectRoot, filepath.FromSlash(state.PlanPath))
	m.panelTab = panelTabPlan
	m.history.panelOpen = true
	m.history.focused = true
	m.planViewport = viewport.New(100, 80)
	m.renderPlanPanelContent()

	renderedPlan := m.planViewport.View()
	for _, want := range []string{"▾ Feature Planner Candidate", "▾ Test Obligations", "▾ Atoms", "CollapseEvent helper"} {
		if !strings.Contains(renderedPlan, want) {
			t.Fatalf("rendered plan missing %q:\n%s", want, renderedPlan)
		}
	}

	atomsIdx := planSelectableRowIndex(m.currentPlanSelectableRows(), "Atoms")
	if atomsIdx < 0 {
		t.Fatalf("Atoms heading should be selectable in rendered plan:\n%s", renderedPlan)
	}
	m.plan.cursor = atomsIdx
	if handled, _ := m.handlePlanPanelKey(tea.KeyMsg{Type: tea.KeyEnter}); !handled {
		t.Fatal("Enter should collapse generated Atoms section")
	}

	collapsedPlan := m.planViewport.View()
	if !strings.Contains(collapsedPlan, "▸ Atoms") || strings.Contains(collapsedPlan, "Summary: Create CollapseEvent helper and route dispatch through it.") {
		t.Fatalf("generated Atoms section did not collapse cleanly:\n%s", collapsedPlan)
	}
}

func TestRunPromptOnceFeaturePlannerCommandUsesSharedEntryPoint(t *testing.T) {
	projectRoot := t.TempDir()
	writeFile(t, projectRoot, "go.mod", "module example.com/tui\n")

	client := &scriptedLLMClient{responses: []string{featurePlannerCommandNewFileQuestionJSON(t)}}
	m := New(config.Config{
		Chat: config.Chat{
			ProjectRoot:            projectRoot,
			AutoStartServer:        false,
			AttachInstructionFiles: false,
		},
		Workspace: config.Workspace{Root: projectRoot, CWD: projectRoot},
	})
	defer m.Close()
	m.client = client
	m.ready = true

	answer, err := m.RunPromptOnce(context.Background(), "/feature-planner Implement CollapseEvent helper.")
	if err != nil {
		t.Fatalf("RunPromptOnce: %v", err)
	}

	if !strings.Contains(answer, "Feature planner needs your approval before it can continue.") {
		t.Fatalf("expected feature-planner approval answer, got:\n%s", answer)
	}
	if strings.TrimSpace(m.directFeaturePlannerSessionID) == "" {
		t.Fatal("shared entry point should remember direct feature-planner session")
	}
	if len(m.messages) == 0 || m.messages[0].Content != "/feature-planner Implement CollapseEvent helper." {
		t.Fatalf("feature-planner typed command should be logged once as user message, got %#v", m.messages)
	}
}

func TestFeaturePlannerTUIEnterCommandUsesSharedEntryPoint(t *testing.T) {
	projectRoot := t.TempDir()
	writeFile(t, projectRoot, "go.mod", "module example.com/tui\n")

	client := &scriptedLLMClient{responses: []string{featurePlannerCommandNewFileQuestionJSON(t)}}
	m := New(config.Config{
		Chat: config.Chat{
			ProjectRoot:            projectRoot,
			AutoStartServer:        false,
			AttachInstructionFiles: false,
		},
		Workspace: config.Workspace{Root: projectRoot, CWD: projectRoot},
	})
	defer m.Close()
	m.client = client
	m.ready = true
	m.events = make(chan tea.Msg, 16)
	m.input = textarea.New()
	m.width = 100
	m.height = 24
	m.resize()

	m.input.SetValue("/feature-planner Implement CollapseEvent helper.")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	m = waitForFeaturePlannerTUIStreamDone(t, m)

	if client.callCount != 1 {
		t.Fatalf("feature-planner slash command should make one planner call, got %d", client.callCount)
	}
	if strings.TrimSpace(m.directFeaturePlannerSessionID) == "" {
		t.Fatal("interactive slash command should remember direct feature-planner session")
	}
	if len(m.messages) != 2 || m.messages[0].Role != "user" || m.messages[0].Content != "/feature-planner Implement CollapseEvent helper." {
		t.Fatalf("interactive slash command should be recorded once in chat messages, got %#v", m.messages)
	}
}

func TestRunPromptOnceDirectFeaturePlannerRevisionDoesNotBecomeChat(t *testing.T) {
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
	if _, err := featureplanner.SaveState(projectRoot, &state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	client := &scriptedLLMClient{responses: []string{
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
	m.client = client
	m.ready = true
	m.directFeaturePlannerSessionID = state.SessionID

	revision := "Please revise the scope to be a full MVP, as minimal as possible."
	answer, err := m.RunPromptOnce(context.Background(), revision)
	if err != nil {
		t.Fatalf("RunPromptOnce: %v", err)
	}

	for _, message := range m.messages {
		if message.Role == "user" && message.Content == revision {
			t.Fatalf("direct feature-planner operator input should not be appended as user chat history: %#v", m.messages)
		}
	}
	if len(client.messages) == 0 || !featurePlannerRequestMessagesContain(client.messages[0], "Operator plan revision instruction:") {
		t.Fatalf("revision did not reach feature-planner resume prompt: %#v", client.messages)
	}
	if !strings.Contains(answer, "Define runnable MVP requirements") {
		t.Fatalf("expected replanning question answer, got:\n%s", answer)
	}
}

func TestFeaturePlannerTUIDirectPlanningRepairAcceptsPlainOperatorInput(t *testing.T) {
	projectRoot := t.TempDir()
	state := featureplanner.NewState("repair-session", "Implement brewery cistern atom.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningRepair
	state.PlanningLoop.State = featureplanner.FeatureStatePlanningRepair
	state.Blockers = []string{`atom 0 prompt mentions repo path "fluid_network_system.h/cpp" outside scope`}
	if _, err := featureplanner.SaveState(projectRoot, &state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	m := &Model{
		cfg: config.Config{
			Chat:      config.Chat{ProjectRoot: projectRoot},
			Workspace: config.Workspace{Root: projectRoot, CWD: projectRoot},
		},
		directFeaturePlannerSessionID: state.SessionID,
	}

	if m.directFeaturePlannerHasOpenQuestion() {
		t.Fatal("repair-only state should not masquerade as an open planner question")
	}
	if !m.directFeaturePlannerAcceptsOperatorInput() {
		t.Fatal("plain typed input should resume direct feature-planner planning-repair state")
	}
	if !m.directFeaturePlannerShouldConsumeOperatorInput("Treat fluid_network_system.h/cpp as the two explicit scoped files.") {
		t.Fatal("repair state should still consume plain repair instructions")
	}
	if m.directFeaturePlannerShouldConsumeOperatorInput("Can we discuss why this is blocked first?") {
		t.Fatal("repair state should leave ordinary questions interactive")
	}

	resumePrompt := directFeaturePlannerResumePrompt(state, "Treat fluid_network_system.h/cpp as the two explicit scoped files.")
	for _, want := range []string{
		"Implement brewery cistern atom.",
		"Operator repair instruction:",
		"Treat fluid_network_system.h/cpp as the two explicit scoped files.",
	} {
		if !strings.Contains(resumePrompt, want) {
			t.Fatalf("repair resume prompt missing %q:\n%s", want, resumePrompt)
		}
	}
}

func TestFeaturePlannerTUIDirectOperatorQuestionDoesNotConsumeChatQuestions(t *testing.T) {
	projectRoot := t.TempDir()
	state := featureplanner.NewState("question-session", "Implement CollapseEvent helper.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningQuestion
	state.OpenQuestions = []featureplanner.PlannerQuestion{{
		ID:                "question-new-files",
		Status:            "open",
		QuestionSummary:   "Authorize CollapseEvent new files",
		QuestionKind:      "external_answer_required",
		BlockingClaim:     "Missing helper and test files require operator approval.",
		RequestedDecision: "Approve creating pkg/collapse_event.go and pkg/collapse_event_test.go.",
		AuthorityRequired: "operator_approval",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     []string{"pkg/collapse_event.go", "pkg/collapse_event_test.go"},
		AffectedFields:    []string{"new_file_authorization"},
	}}
	if _, err := featureplanner.SaveState(projectRoot, &state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	m := &Model{
		cfg: config.Config{
			Chat:      config.Chat{ProjectRoot: projectRoot},
			Workspace: config.Workspace{Root: projectRoot, CWD: projectRoot},
		},
		directFeaturePlannerSessionID: state.SessionID,
	}

	for _, input := range []string{
		"Is this information model-generated, or is it a harness template?",
		"Can we talk about the design plan first?",
		"What paths are you asking me to approve?",
	} {
		if m.directFeaturePlannerShouldConsumeOperatorInput(input) {
			t.Fatalf("chat question should not be consumed as feature-planner approval: %q", input)
		}
	}

	for _, input := range []string{
		"Approved.",
		"Reject this scope.",
		"Use pkg/event.go and pkg/event_test.go instead.",
		"You may create these files.",
		"Please revise the scope to be a full MVP, as minimal as possible.",
	} {
		if !m.directFeaturePlannerShouldConsumeOperatorInput(input) {
			t.Fatalf("explicit operator answer should resume feature planner: %q", input)
		}
	}
}

func TestFeaturePlannerTUIDirectRevisionInstructionReplansOpenQuestion(t *testing.T) {
	projectRoot := t.TempDir()
	writeFile(t, projectRoot, "go.mod", "module example.com/tui\n")

	client := &scriptedLLMClient{responses: []string{
		featurePlannerCommandNewFileQuestionJSON(t),
		`{"tool":"ask_planner_question","question_summary":"Define minimal runnable MVP requirements","blocking_claim":"The requested full MVP scope is broader than the current scaffolding plan and needs explicit operator requirements before a safe feature plan can be submitted.","requested_decision":"Choose the minimal runnable MVP behavior to target: CLI prompt loop, memory retention format, persistence location, and verification command.","question_kind":"external_answer_required","authority_required":"operator_approval","required_before":"plan_approval","affected_paths":["src/model.py","tests/test_model.py","src/tokenizer.py","tests/test_tokenizer.py"],"affected_fields":["requirements_scope","verification_command"]}`,
	}}
	m := &Model{
		client: client,
		cfg: config.Config{
			Chat:      config.Chat{ProjectRoot: projectRoot},
			Workspace: config.Workspace{Root: projectRoot, CWD: projectRoot},
		},
		workspace: workspace.NewManager(config.Workspace{Root: projectRoot, CWD: projectRoot}),
	}

	if _, err := m.runFeaturePlannerCommandOnce(context.Background(), "Lay out the project bones."); err != nil {
		t.Fatalf("runFeaturePlannerCommandOnce: %v", err)
	}
	answer, err := m.runDirectFeaturePlannerAnswerOnce(context.Background(), "Please revise the scope to be a full MVP, as minimal as possible.")
	if err != nil {
		t.Fatalf("runDirectFeaturePlannerAnswerOnce: %v", err)
	}
	if len(client.messages) < 2 {
		t.Fatalf("expected resumed planner model call, got %#v", client.messages)
	}
	resumedPrompt := client.messages[1][len(client.messages[1])-1].Content
	for _, want := range []string{
		"User request:\nLay out the project bones.",
		"Operator plan revision instruction:",
		"Please revise the scope to be a full MVP, as minimal as possible.",
	} {
		if !strings.Contains(resumedPrompt, want) {
			t.Fatalf("resumed planner prompt missing %q:\n%s", want, resumedPrompt)
		}
	}
	if !strings.Contains(answer, "Define minimal runnable MVP requirements") ||
		!strings.Contains(answer, "Choose the minimal runnable MVP behavior to target") {
		t.Fatalf("feature-planner answer did not surface planner follow-up question:\n%s", answer)
	}

	state := loadFeaturePlannerCommandStateFile(t, filepath.Join(projectRoot, ".idiom", "feature-planner", m.directFeaturePlannerSessionID, "state.json"))
	if len(state.OpenQuestions) != 2 || state.OpenQuestions[0].Status != "answered" || state.OpenQuestions[1].Status != "open" {
		t.Fatalf("revision instruction should answer the prior question and allow a new planner question: %+v", state.OpenQuestions)
	}
	if !strings.Contains(state.OpenQuestions[0].Answer, "full MVP") {
		t.Fatalf("revision instruction not retained on planner question: %+v", state.OpenQuestions[0])
	}
	if !strings.Contains(state.OpenQuestions[1].QuestionSummary, "minimal runnable MVP") {
		t.Fatalf("new planner question should own MVP requirement discovery: %+v", state.OpenQuestions[1])
	}
}

func TestFeaturePlannerTUIE2EOpenApprovalQuestionKeepsChatInteractive(t *testing.T) {
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
	client := &scriptedLLMClient{responses: []string{
		"This is normal chat output, not a feature-planner approval answer.",
		featurePlannerCommandPlanJSON(t, plan),
	}}

	m := New(cfg)
	defer m.Close()
	m.client = client
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
		RequestedDecision: "Approve creating pkg/collapse_event.go and pkg/collapse_event_test.go.",
		AuthorityRequired: "operator_approval",
		RequiredBefore:    "plan_approval",
		AffectedPaths:     []string{"pkg/collapse_event.go", "pkg/collapse_event_test.go"},
		AffectedFields:    []string{"new_file_authorization"},
	}}
	if _, err := featureplanner.SaveState(projectRoot, &state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	m.directFeaturePlannerSessionID = state.SessionID

	m.input.SetValue("Is this information model-generated, or is it a harness template?")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	m = waitForFeaturePlannerTUIStreamDone(t, m)

	if client.callCount != 1 {
		t.Fatalf("ordinary chat question should make exactly one normal model call, got %d", client.callCount)
	}
	if len(client.messages) != 1 || !featurePlannerRequestMessagesContain(client.messages[0], "model-generated") {
		t.Fatalf("ordinary chat question did not reach normal chat model path: %#v", client.messages)
	}

	afterChat := loadFeaturePlannerCommandStateFile(t, filepath.Join(projectRoot, ".idiom", "feature-planner", state.SessionID, "state.json"))
	if len(afterChat.OpenQuestions) != 1 || afterChat.OpenQuestions[0].Status != "open" {
		t.Fatalf("ordinary chat must not answer planner approval question: %+v", afterChat.OpenQuestions)
	}
	if len(afterChat.AuthorityDeltas) != 0 {
		t.Fatalf("ordinary chat must not create authority deltas: %+v", afterChat.AuthorityDeltas)
	}

	m.input.SetValue("Approved.")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	m = waitForFeaturePlannerTUIStreamDone(t, m)

	if client.callCount != 2 {
		t.Fatalf("explicit approval should resume feature planner with one more model call, got %d", client.callCount)
	}

	afterApproval := loadFeaturePlannerCommandStateFile(t, filepath.Join(projectRoot, ".idiom", "feature-planner", state.SessionID, "state.json"))
	if len(afterApproval.OpenQuestions) != 1 || afterApproval.OpenQuestions[0].Status != "answered" {
		t.Fatalf("explicit approval should answer planner question: %+v", afterApproval.OpenQuestions)
	}
	if len(afterApproval.AuthorityDeltas) != 1 || afterApproval.AuthorityDeltas[0].Actual != "Approved." {
		t.Fatalf("explicit approval should create typed authority delta: %+v", afterApproval.AuthorityDeltas)
	}
	if strings.TrimSpace(afterApproval.PlanPath) == "" {
		t.Fatal("resumed feature planner should write the factual candidate to the Plan tab")
	}
	planTabBody, err := os.ReadFile(filepath.Join(projectRoot, filepath.FromSlash(afterApproval.PlanPath)))
	if err != nil {
		t.Fatalf("read feature planner plan tab artifact: %v", err)
	}
	for _, want := range []string{
		"# Feature Planner Candidate",
		"Status: " + featureplanner.FeatureStatePlanningRepair,
		"Route collapse dispatch through a slim event helper.",
		"pkg/collapse_event.go",
		"pkg/collapse_event_test.go",
		"## Test Obligations",
		"## Invariants",
		"go test ./pkg",
	} {
		if !strings.Contains(string(planTabBody), want) {
			t.Fatalf("plan tab artifact missing %q:\n%s", want, string(planTabBody))
		}
	}
	if strings.Contains(string(planTabBody), "**") {
		t.Fatalf("plan tab artifact should avoid raw inline markdown styling:\n%s", string(planTabBody))
	}
}

func TestFeaturePlannerTUIFollowupQuestionUsesActivePlanArtifactEvidence(t *testing.T) {
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
	if _, err := featureplanner.SaveState(projectRoot, &state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	client := &scriptedLLMClient{responses: []string{"The active plan does not create durable memory yet. Its proposed files to create are `src/model_wrapper.py` and `tests/test_model_wrapper.py`; those are proposed plan paths, not existing source files."}}
	m := New(cfg)
	defer m.Close()
	m.client = client
	m.runner.Client = client
	m.ready = true
	m.directFeaturePlannerSessionID = state.SessionID

	answer, err := m.RunPromptOnce(context.Background(), "how are we planning on retaining memory?")
	if err != nil {
		t.Fatalf("RunPromptOnce: %v", err)
	}
	if strings.Contains(answer, "I could not produce a supported final answer") ||
		strings.Contains(answer, "source-body evidence") {
		t.Fatalf("follow-up should not leak source-grounding blocker:\n%s", answer)
	}
	if !strings.Contains(answer, "does not create durable memory yet") {
		t.Fatalf("unexpected answer:\n%s", answer)
	}
	if len(client.messages) == 0 || !featurePlannerRequestMessagesContain(client.messages[0], "Active feature-planner plan artifact") ||
		!featurePlannerRequestMessagesContain(client.messages[0], "Proposed paths to create:") {
		t.Fatalf("normal chat request did not receive active plan context: %#v", client.messages)
	}
}

func TestFeaturePlannerTUIFollowupQuestionAnswersPlannedTestCoverageFromActivePlan(t *testing.T) {
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

	client := &scriptedLLMClient{responses: []string{"The planned tests are `tests/test_models.py`, `tests/test_config.py`, and `tests/test_orchestrator.py`. I would expand the intended test suite with persistence variants, malformed-config cases, and orchestration failure invariants before approval."}}
	m := New(cfg)
	defer m.Close()
	m.client = client
	m.runner.Client = client
	m.ready = true
	if _, err := m.saveFeaturePlannerStateAndPlanTab(&state); err != nil {
		t.Fatalf("saveFeaturePlannerStateAndPlanTab: %v", err)
	}
	m.directFeaturePlannerSessionID = state.SessionID

	answer, err := m.RunPromptOnce(context.Background(), "what tests do w have planned? should we expand the intended test suite?")
	if err != nil {
		t.Fatalf("RunPromptOnce: %v", err)
	}
	for _, forbidden := range []string{"source-body evidence", "does not exist in this workspace", "I could not produce a supported final answer"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("planned-test follow-up leaked repair state %q:\n%s", forbidden, answer)
		}
	}
	for _, want := range []string{"planned tests", "tests/test_models.py", "tests/test_config.py", "tests/test_orchestrator.py", "persistence variants", "malformed-config", "failure invariants"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("planned-test follow-up missing %q:\n%s", want, answer)
		}
	}
	if client.callCount != 1 {
		t.Fatalf("planned-test follow-up should be accepted without repair or nonexistent test reads, got %d model calls", client.callCount)
	}
	if len(client.messages) == 0 || !featurePlannerRequestMessagesContain(client.messages[0], "Active feature-planner plan artifact") ||
		!featurePlannerRequestMessagesContain(client.messages[0], "tests/test_models.py") {
		t.Fatalf("planned-test follow-up did not receive active plan context: %#v", client.messages)
	}

	afterChat := loadFeaturePlannerCommandStateFile(t, filepath.Join(projectRoot, ".idiom", "feature-planner", state.SessionID, "state.json"))
	if len(afterChat.OpenQuestions) != 1 || afterChat.OpenQuestions[0].Status != "open" {
		t.Fatalf("ordinary planned-test chat must not answer planner approval question: %+v", afterChat.OpenQuestions)
	}
	if len(afterChat.AuthorityDeltas) != 0 {
		t.Fatalf("ordinary planned-test chat must not create authority deltas: %+v", afterChat.AuthorityDeltas)
	}
}

func TestFeaturePlannerTUIResumeE2EFollowupQuestionReceivesActivePlanContext(t *testing.T) {
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
	if _, err := featureplanner.SaveState(projectRoot, &state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	writeSessionLog(t, logDir, "resumed.jsonl", []string{
		`{"type":"message","role":"user","content":"/feature-planner Lay out the LLM brain project bones."}`,
		`{"type":"message","role":"assistant","content":"feature-planner: planning-question-open\nstate: .idiom/feature-planner/resume-question-session/state.json"}`,
	})

	client := &scriptedLLMClient{responses: []string{"The resumed active plan still does not include durable memory; it proposes model wrapper files only."}}
	m := New(cfg)
	defer m.Close()
	m.client = client
	m.runner.Client = client
	m.ready = true

	if err := m.ResumeSession("resumed"); err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}
	if m.directFeaturePlannerSessionID != state.SessionID {
		t.Fatalf("resume did not restore active feature-planner session: %q", m.directFeaturePlannerSessionID)
	}

	answer, err := m.RunPromptOnce(context.Background(), "how are we planning on retaining memory?")
	if err != nil {
		t.Fatalf("RunPromptOnce: %v", err)
	}
	if !strings.Contains(answer, "does not include durable memory") {
		t.Fatalf("unexpected answer:\n%s", answer)
	}
	if len(client.messages) == 0 || !featurePlannerRequestMessagesContain(client.messages[0], "Active feature-planner plan artifact") ||
		!featurePlannerRequestMessagesContain(client.messages[0], "Proposed paths to create:") {
		t.Fatalf("resumed chat follow-up did not receive active feature-planner plan context: %#v", client.messages)
	}

	afterChat := loadFeaturePlannerCommandStateFile(t, filepath.Join(projectRoot, ".idiom", "feature-planner", state.SessionID, "state.json"))
	if len(afterChat.OpenQuestions) != 1 || afterChat.OpenQuestions[0].Status != "open" {
		t.Fatalf("ordinary resumed chat must not answer planner question: %+v", afterChat.OpenQuestions)
	}
}

func TestFeaturePlannerActivePlanArtifactRequiresOpenQuestion(t *testing.T) {
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
	if _, err := featureplanner.SaveState(projectRoot, &state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	m := New(cfg)
	defer m.Close()
	m.directFeaturePlannerSessionID = state.SessionID

	if context := m.activeFeaturePlannerPlanContext(); context != "" {
		t.Fatalf("closed planner question should not expose active plan context:\n%s", context)
	}
	if seed, ok := m.activeFeaturePlannerPlanSeedResult(); ok {
		t.Fatalf("closed planner question should not expose active plan seed: %#v", seed)
	}
}

func waitForFeaturePlannerTUIStreamDone(t *testing.T, m *Model) *Model {
	t.Helper()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case msg := <-m.events:
			if done, ok := msg.(streamDoneMsg); ok {
				if done.err != nil {
					t.Fatalf("stream failed: %v", done.err)
				}
				updated, _ := m.Update(done)

				return updated.(*Model)
			}
		case <-deadline:
			t.Fatal("timed out waiting for scripted TUI stream")
		}
	}
}

func featurePlannerRequestMessagesContain(messages []llama.ChatMessage, text string) bool {
	for _, message := range messages {
		if strings.Contains(message.Content, text) {
			return true
		}
	}

	return false
}

func TestFeaturePlannerTUIDirectAcceptedSourceQuestionConsumesOnlyStructuredAnswers(t *testing.T) {
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
	if _, err := featureplanner.SaveState(projectRoot, &state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	m := &Model{
		cfg: config.Config{
			Chat:      config.Chat{ProjectRoot: projectRoot},
			Workspace: config.Workspace{Root: projectRoot, CWD: projectRoot},
		},
		directFeaturePlannerSessionID: state.SessionID,
	}

	for _, input := range []string{
		"What source facts do you need?",
		"Can we discuss why this is blocked first?",
		"Use the source facts from the current branch.",
	} {
		if m.directFeaturePlannerShouldConsumeOperatorInput(input) {
			t.Fatalf("unstructured accepted-source chat should not be consumed: %q", input)
		}
	}

	for _, input := range []string{
		`{"answer":"accepted","answer_source_kind":"accepted_source","evidence_refs":["pkg/collapse_event.go"],"source_facts":[{"path":"pkg/collapse_event.go","symbol":"NewCollapseEvent","signature":"CollapseEvent NewCollapseEvent(EntityID id)"}]}`,
		`answer_source_kind=accepted_source evidence_refs=pkg/collapse_event.go source_facts=NewCollapseEvent`,
	} {
		if !m.directFeaturePlannerShouldConsumeOperatorInput(input) {
			t.Fatalf("structured accepted-source answer should be consumed: %q", input)
		}
	}
}

func TestFeaturePlannerPlanTabPrefersAcceptedPlanAfterRejectedCandidate(t *testing.T) {
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
	if !ok {
		t.Fatal("expected displayable plan tab candidate")
	}
	if candidate.PlanHash != "" {
		t.Fatalf("ready state should prefer accepted state plan over stale rejected candidate: %+v", candidate)
	}
	if plan.Summary != "Accepted current plan" {
		t.Fatalf("selected plan summary = %q, want accepted current plan", plan.Summary)
	}

	state.Status = featureplanner.FeatureStatePlanningRepair
	plan, candidate, ok = featurePlannerPlanTabCandidate(state)
	if !ok {
		t.Fatal("expected displayable rejected plan candidate")
	}
	if candidate.PlanHash != "rejected-hash" || plan.Summary != "Rejected candidate" {
		t.Fatalf("repair state should show latest rejected candidate, plan=%q candidate=%+v", plan.Summary, candidate)
	}
}

func TestFeaturePlannerPlanTabHidesRawSummaryAndRendersStructuredCoverage(t *testing.T) {
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
		if strings.Contains(body, hidden) {
			t.Fatalf("plan tab leaked raw display text %q:\n%s", hidden, body)
		}
	}

	for _, want := range []string{
		"# Feature Planner Candidate",
		"Status: " + featureplanner.FeatureStatePlanningRepair,
		"Candidate: candidate-hash",
		"## Requirements",
		"REQ-COLLAPSE: When collapse dispatch is requested, the system shall route through CollapseEvent. (event-driven)",
		"Objective: Route collapse dispatch through a slim event helper.",
		"Success criterion: Collapse dispatch uses the helper.",
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
		if !strings.Contains(body, want) {
			t.Fatalf("plan tab missing %q:\n%s", want, body)
		}
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
		if strings.Contains(body, rawBlob) {
			t.Fatalf("plan tab should render structured coverage fields, not raw blob key %q:\n%s", rawBlob, body)
		}
	}
}

func featurePlannerStructuredCoverageDraftForTest() *featureplanner.BehavioralCoverageDraft {
	return &featureplanner.BehavioralCoverageDraft{
		Authority: "standalone_provisional",
		Status:    "draft",
		Tier:      "contract_high",
		Requirements: []featureplanner.CoverageDraftRequirement{{
			ID:        "REQ-COLLAPSE",
			Pattern:   "event-driven",
			Statement: "When collapse dispatch is requested, the system shall route through CollapseEvent.",
		}},
		ExampleRules: []featureplanner.CoverageDraftExampleRule{{
			ID:            "RULE-COLLAPSE",
			RequirementID: "REQ-COLLAPSE",
			Rule:          "Collapse dispatch delegates through the helper",
			Examples: []featureplanner.CoverageDraftExample{{
				ID:           "EX-COLLAPSE",
				Description:  "collapse request with local event payload",
				ConcreteData: map[string]string{"event": "collapse"},
			}},
		}},
		Scenarios: []featureplanner.CoverageDraftScenario{{
			ID:            "BDD-COLLAPSE",
			RequirementID: "REQ-COLLAPSE",
			Given:         []string{"collapse dispatcher receives event payload"},
			When:          []string{"dispatch runs"},
			Then:          []string{"CollapseEvent helper is invoked"},
			ConcreteData:  map[string]string{"event": "collapse"},
			Obligations:   []string{"TDD-COLLAPSE-EVENT"},
		}},
		Invariants: []featureplanner.CoverageDraftInvariant{{
			ID:          "INV-COLLAPSE",
			Statement:   "helper invocation preserves event identity",
			Obligations: []string{"TDD-COLLAPSE-EVENT"},
		}},
		FailureModes: []featureplanner.CoverageDraftFailureMode{{
			ID:          "FM-COLLAPSE",
			Mode:        "helper not invoked",
			Mitigation:  "route through helper or fail test",
			Obligations: []string{"TDD-COLLAPSE-EVENT"},
		}},
		StateMachines: []featureplanner.CoverageDraftStateMachine{{
			ID:     "SM-COLLAPSE",
			States: []string{"requested", "dispatched"},
			Transitions: []featureplanner.CoverageDraftTransition{{
				From:  "requested",
				Event: "dispatch",
				To:    "dispatched",
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
			ID:       "TDD-COLLAPSE-EVENT",
			Type:     "contract",
			Behavior: "Collapse dispatch routes through helper.",
			Command:  "go test ./pkg",
		}},
		Verification: []featureplanner.CoverageDraftVerification{{
			ID:      "VERIFY-COLLAPSE",
			Gate:    "contract",
			Command: "go test ./pkg",
		}},
		OpenQuestions: []featureplanner.CoverageDraftOpenQuestion{{
			ID:       "Q-COLLAPSE",
			Question: "Should the helper support queued collapse events in this slice?",
		}},
	}
}

func TestFeaturePlannerPlanTabDoesNotFabricateCoverageForUnmappedTDD(t *testing.T) {
	plan := featurePlannerCommandStandaloneCollapsePlan()
	plan.TDDContract.Obligations = nil
	plan.Atoms[0].Summary = ""
	plan.Atoms[0].TDDObligations = []string{"TDD-MISSING"}
	plan.Atoms[0].TestFirst.ObligationRefs = []string{"TDD-MISSING"}
	state := featureplanner.NewState("plan-session", "Implement CollapseEvent helper.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningRepair

	body := renderFeaturePlannerPlanTabMarkdown(state, "state.json", plan, featureplanner.RejectedPlanCandidate{})

	if strings.Contains(body, "TDD-MISSING: Collapse") {
		t.Fatalf("unmapped TDD refs should not produce behavior coverage:\n%s", body)
	}
	for _, want := range []string{
		"## Failure Modes",
		"Atom CollapseEvent helper: Unmapped TDD ref: TDD-MISSING",
		"## Invariants",
		"Atom CollapseEvent helper: TDD refs: TDD-MISSING",
		"## Test Obligations",
		"Atom CollapseEvent helper TDD ref: TDD-MISSING",
		"Atom CollapseEvent helper required test: go test ./pkg",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("unmapped coverage plan missing %q:\n%s", want, body)
		}
	}
}

func TestFeaturePlannerPlanTabRendersAcceptedProductCoverageWithoutDraft(t *testing.T) {
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
			PropertyBasedTestRefs: []string{"TestAcceptedCoverageAuthority"},
		}},
		FailureModes: []featureplanner.AcceptedCoverageFailureMode{{
			ID:                 "FM-PRODUCT",
			Mode:               "planner fabricates accepted coverage",
			Mitigation:         "reject accepted_coverage submissions",
			RPN:                80,
			MitigationTestRefs: []string{"TestValidatePlanProductWorkflowContextRejectsPlannerAuthoredAcceptedCoverage"},
		}},
		HAZOPDeviations: []featureplanner.AcceptedCoverageHAZOPDeviation{{
			ID:        "HAZOP-PRODUCT",
			GuideWord: "Other Than",
			Deviation: "chat renders draft instead of accepted coverage",
			TestRefs:  []string{"TestFeaturePlannerPlanTabRendersAcceptedProductCoverageWithoutDraft"},
		}},
		FaultTreeLeaves: []featureplanner.AcceptedCoverageFaultTreeLeaf{{
			ID:       "FTA-PRODUCT",
			TopEvent: "accepted coverage hidden",
			Cause:    "adapter drops product-owned coverage detail",
			TestRefs: []string{"TestFeaturePlannerPlanTabRendersAcceptedProductCoverageWithoutDraft"},
		}},
		StateMachines: []featureplanner.AcceptedCoverageStateMachine{{
			ID:     "SM-PRODUCT",
			States: []string{"accepted", "rendered"},
			Transitions: []featureplanner.AcceptedCoverageTransition{{
				ID:       "TR-PRODUCT",
				From:     "accepted",
				Event:    "render",
				To:       "rendered",
				TestRefs: []string{"TestFeaturePlannerPlanTabRendersAcceptedProductCoverageWithoutDraft"},
			}},
		}},
		DecisionTables: []featureplanner.AcceptedCoverageDecisionTable{{
			ID:         "DT-PRODUCT",
			Conditions: []string{"product_context", "draft_present"},
			TestRefs:   []string{"TestFeaturePlannerPlanTabRendersAcceptedProductCoverageWithoutDraft"},
		}},
		BoundaryValues: []featureplanner.AcceptedCoverageBoundaryValue{{
			ID:        "BV-PRODUCT",
			Parameter: "coverage_sections",
			Values:    []string{"0", "1", "9"},
			TestRefs:  []string{"TestFeaturePlannerPlanTabRendersAcceptedProductCoverageWithoutDraft"},
		}},
		TestObligations: []featureplanner.AcceptedCoverageTestObligation{{
			ID:       "TDD-PRODUCT",
			Type:     "contract",
			Behavior: "accepted product coverage renders in the plan tab",
			Command:  "go test ./internal/surfaces/tui/app -run TestFeaturePlannerPlanTabRendersAcceptedProductCoverageWithoutDraft",
		}},
		Verification: []featureplanner.AcceptedCoverageVerification{{
			ID:      "VERIFY-PRODUCT",
			Gate:    "contract",
			Command: "go test ./internal/surfaces/tui/app",
		}},
		PostMortems: []featureplanner.AcceptedCoveragePostMortem{{
			ID:               "PM-PRODUCT",
			Incident:         "accepted coverage was previously hidden",
			CatchingTestRefs: []string{"TestFeaturePlannerPlanTabRendersAcceptedProductCoverageWithoutDraft"},
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
			TestRefs:    []string{"TestValidatePlanProductWorkflowContextRejectsPlannerAuthoredAcceptedCoverage"},
			Obligations: []string{"TDD-PRODUCT"},
		}},
		PrivacyControls: []featureplanner.AcceptedCoveragePrivacyControl{{
			ID:            "PRIV-PRODUCT",
			Status:        "tested",
			DataClass:     "product workflow prompt context",
			Policy:        "render structured fields only",
			RedactionRefs: []string{"redaction/product_workflow_context"},
			Retention:     "no raw prompt blob",
			TestRefs:      []string{"TestFeaturePlannerPlanTabRendersAcceptedProductCoverageWithoutDraft"},
			Obligations:   []string{"TDD-PRODUCT"},
		}},
		ProviderContracts: []featureplanner.AcceptedCoverageProviderContract{{
			ID:           "PROVIDER-PRODUCT",
			Status:       "tested",
			Provider:     "product-workflow",
			Consumer:     "feature-planner",
			ContractRefs: []string{"contracts/product_featureplanner_coverage.json"},
			TestRefs:     []string{"TestBuildProductWorkflowBriefCarriesAcceptedCoverage"},
			Obligations:  []string{"TDD-PRODUCT"},
		}},
		Observability: []featureplanner.AcceptedCoverageObservability{{
			ID:               "OBS-PRODUCT",
			Status:           "tested",
			Signal:           "featureplanner.coverage.accepted",
			InstrumentedPath: "internal/surfaces/tui/app/feature_planner_plan_tab.go",
			RequiredFields:   []string{"contract_id", "coverage_tier"},
			TestRefs:         []string{"TestFeaturePlannerPlanTabRendersAcceptedProductCoverageWithoutDraft"},
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
		Mutation: featureplanner.AcceptedCoverageMutation{Requirement: "required", MinScore: 80, Score: &score},
	}
	state := featureplanner.NewState("plan-session", "Implement product-backed coverage.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStateReady

	body := renderFeaturePlannerPlanTabMarkdown(state, "state.json", plan, featureplanner.RejectedPlanCandidate{})

	sharedSections := featureplanner.AcceptedCoveragePresentation(plan.AcceptedCoverage)
	for _, section := range sharedSections {
		if !strings.Contains(body, "## "+section.Label) {
			t.Fatalf("plan tab missing shared accepted coverage section label %q:\n%s", section.Label, body)
		}
	}
	for _, sharedRow := range []string{
		"REQ-PRODUCT: When product dispatch is approved, the system shall execute through the accepted plan. (event-driven)",
		"BDD-PRODUCT: Given accepted product coverage exists / When feature planner renders the plan / Then accepted BDD detail is visible (concrete data)",
		"Security SEC-PRODUCT: coverage authority injection attack=planner-authored accepted coverage expected=rejected [tested] test: TestValidatePlanProductWorkflowContextRejectsPlannerAuthoredAcceptedCoverage",
	} {
		if !slices.Contains(featureplanner.AcceptedCoverageRowsBySection(sharedSections, featureplanner.CoverageSectionRequirements), sharedRow) &&
			!slices.Contains(featureplanner.AcceptedCoverageRowsBySection(sharedSections, featureplanner.CoverageSectionScenarios), sharedRow) &&
			!slices.Contains(featureplanner.AcceptedCoverageRowsBySection(sharedSections, featureplanner.CoverageSectionSecurityPrivacyRAGEvals), sharedRow) {
			t.Fatalf("shared projection missing row %q", sharedRow)
		}
		if !strings.Contains(body, sharedRow) {
			t.Fatalf("plan tab missing shared accepted coverage row %q:\n%s", sharedRow, body)
		}
	}

	for _, want := range []string{
		"Accepted coverage authority: product_workflow",
		"REQ-PRODUCT: When product dispatch is approved",
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
		if !strings.Contains(body, want) {
			t.Fatalf("accepted coverage plan tab missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "Coverage draft authority") {
		t.Fatalf("product accepted coverage render must not depend on draft coverage:\n%s", body)
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
		if strings.Contains(body, rawBlob) {
			t.Fatalf("accepted coverage plan tab should render typed fields, not raw blob key %q:\n%s", rawBlob, body)
		}
	}
}

func TestFeaturePlannerPlanTabRejectsSummaryOnlyCandidate(t *testing.T) {
	state := featureplanner.NewState("plan-session", "Implement CollapseEvent helper.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStateReady
	state.Plan = featureplanner.FeaturePlan{
		Summary: "RAW_MODEL_SUMMARY this is not a displayable plan contract by itself.",
	}

	_, _, ok := featurePlannerPlanTabCandidate(state)
	if ok {
		t.Fatal("summary-only plan should not be displayable in the Plan tab")
	}
}

func TestFeaturePlannerPlanTabRenderedPanelShowsStructuredCoverageOnly(t *testing.T) {
	root := t.TempDir()
	plan := featurePlannerCommandStandaloneCollapsePlan()
	plan.Summary = "RAW_MODEL_SUMMARY dense model-facing paragraph that should not be rendered in the plan panel."
	state := featureplanner.NewState("plan-session", "Implement CollapseEvent helper.", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningRepair

	path := filepath.Join(root, "plan.md")
	body := renderFeaturePlannerPlanTabMarkdown(state, "state.json", plan, featureplanner.RejectedPlanCandidate{
		Diagnostic: "RAW_DIAGNOSTIC model-facing validation dump that should stay out of the plan panel.",
	})
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write plan tab artifact: %v", err)
	}

	m := planPanelTestModel(path)
	m.planViewport = viewport.New(120, 80)
	m.renderPlanPanelContent()

	rendered := m.planViewport.View()
	for _, hidden := range []string{"RAW_MODEL_SUMMARY", "RAW_DIAGNOSTIC", "Diagnostic:"} {
		if strings.Contains(rendered, hidden) {
			t.Fatalf("rendered plan leaked raw display text %q:\n%s", hidden, rendered)
		}
	}
	for _, want := range []string{
		"▾ Feature Planner Candidate",
		"▾ Requirements",
		"▾ Test Obligations",
		"TDD-COLLAPSE-EVENT - Collapse dispatch routes through helper.",
		"Atom CollapseEvent helper test artifact:",
		"Atom CollapseEvent helper required test:",
		"go test ./pkg",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered plan missing %q:\n%s", want, rendered)
		}
	}

	coverageIdx := planSelectableRowIndex(m.currentPlanSelectableRows(), "Test Obligations")
	if coverageIdx < 0 {
		t.Fatalf("Test Obligations heading should be selectable:\n%s", rendered)
	}
	m.plan.cursor = coverageIdx
	if handled, _ := m.handlePlanPanelKey(tea.KeyMsg{Type: tea.KeyEnter}); !handled {
		t.Fatal("Enter should collapse generated Test Obligations section")
	}

	collapsed := m.planViewport.View()
	if !strings.Contains(collapsed, "▸ Test Obligations") || strings.Contains(collapsed, "Atom CollapseEvent helper test artifact:") {
		t.Fatalf("generated Test Obligations section did not collapse cleanly:\n%s", collapsed)
	}
}

func TestRunPromptOnceAcceptedSourceQuestionLeavesUnstructuredChatInteractive(t *testing.T) {
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
	if _, err := featureplanner.SaveState(projectRoot, &state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	client := &scriptedLLMClient{responses: []string{"We can discuss the accepted-source blocker before you provide structured evidence."}}
	m := New(cfg)
	defer m.Close()
	m.client = client
	m.runner.Client = client
	m.ready = true
	m.directFeaturePlannerSessionID = state.SessionID

	answer, err := m.RunPromptOnce(context.Background(), "What source facts do you need?")
	if err != nil {
		t.Fatalf("RunPromptOnce: %v", err)
	}
	if !strings.Contains(answer, "accepted-source blocker") {
		t.Fatalf("unexpected chat answer:\n%s", answer)
	}
	if len(client.messages) != 1 || !featurePlannerRequestMessagesContain(client.messages[0], "What source facts do you need?") {
		t.Fatalf("unstructured accepted-source question did not route through normal chat: %#v", client.messages)
	}

	afterChat := loadFeaturePlannerCommandStateFile(t, filepath.Join(projectRoot, ".idiom", "feature-planner", state.SessionID, "state.json"))
	if len(afterChat.OpenQuestions) != 1 || afterChat.OpenQuestions[0].Status != "open" {
		t.Fatalf("unstructured chat must not answer accepted-source planner question: %+v", afterChat.OpenQuestions)
	}
	if len(afterChat.AuthorityDeltas) != 0 {
		t.Fatalf("unstructured chat must not create accepted-source authority deltas: %+v", afterChat.AuthorityDeltas)
	}
}

func TestFeaturePlannerTUIDirectAcceptedSourceAnswerCarriesAuthorityIntoResumePrompt(t *testing.T) {
	projectRoot := t.TempDir()
	writeFile(t, projectRoot, "go.mod", "module example.com/tui\n")
	plan := featurePlannerCommandStandaloneCollapsePlan()
	plan.Atoms[0].SDDRefs = nil

	client := &scriptedLLMClient{responses: []string{
		featurePlannerCommandAcceptedSourceQuestionJSON(t),
		featurePlannerCommandPlanJSON(t, plan),
	}}
	m := &Model{
		client: client,
		cfg: config.Config{
			Chat:      config.Chat{ProjectRoot: projectRoot},
			Workspace: config.Workspace{Root: projectRoot, CWD: projectRoot},
		},
		workspace: workspace.NewManager(config.Workspace{Root: projectRoot, CWD: projectRoot}),
	}

	answer, err := m.runFeaturePlannerCommandOnce(context.Background(), "Implement CollapseEvent helper.")
	if err != nil {
		t.Fatalf("runFeaturePlannerCommandOnce: %v", err)
	}
	if !strings.Contains(answer, `answer_format: JSON object with answer, answer_source_kind="accepted_source", evidence_refs, and source_facts [{path, symbol, signature}]`) {
		t.Fatalf("question summary should show typed answer format, got:\n%s", answer)
	}

	sessionID := m.directFeaturePlannerSessionID
	typedAnswer := `{"answer":"Accepted source fact approved for CollapseEvent helper usage.","answer_source_kind":"accepted_source","evidence_refs":["pkg/collapse_event.go"],"affected_paths":["pkg/collapse_event.go"],"affected_fields":["source_localization"],"source_facts":[{"path":"pkg/collapse_event.go","symbol":"NewCollapseEvent","signature":"CollapseEvent NewCollapseEvent(EntityID id)"}]}`
	answer, err = m.runDirectFeaturePlannerAnswerOnce(context.Background(), typedAnswer)
	if err != nil {
		t.Fatalf("runDirectFeaturePlannerAnswerOnce: %v", err)
	}
	if !strings.Contains(answer, "feature-planner: "+featureplanner.FeatureStatePlanningRepair) {
		t.Fatalf("expected planning repair after resumed invalid plan, got:\n%s", answer)
	}

	if len(client.messages) < 2 {
		t.Fatalf("expected resumed planner prompt, got %#v", client.messages)
	}
	resumePrompt := client.messages[1][len(client.messages[1])-1].Content
	for _, want := range []string{
		"User request:\nImplement CollapseEvent helper.",
		"Accepted source authority constraints:",
		"signature: CollapseEvent NewCollapseEvent(EntityID id)",
		"Use only these accepted source facts for helper signatures and source-localized call shapes.",
	} {
		if !strings.Contains(resumePrompt, want) {
			t.Fatalf("resumed planner prompt missing %q:\n%s", want, resumePrompt)
		}
	}
	if strings.Contains(resumePrompt, "User request:\n"+typedAnswer) {
		t.Fatalf("resumed planner prompt used typed answer as objective:\n%s", resumePrompt)
	}

	state := loadFeaturePlannerCommandStateFile(t, filepath.Join(projectRoot, ".idiom", "feature-planner", sessionID, "state.json"))
	if len(state.AuthorityDeltas) != 1 || state.AuthorityDeltas[0].AuthorityKind != "accepted_source" {
		t.Fatalf("accepted-source authority delta was not preserved: %+v", state.AuthorityDeltas)
	}
	hasSignatureFact := false
	for _, fact := range state.AuthorityDeltas[0].SourceFacts {
		if fact.Signature == "CollapseEvent NewCollapseEvent(EntityID id)" {
			hasSignatureFact = true
			break
		}
	}
	if !hasSignatureFact {
		t.Fatalf("accepted-source source facts were not preserved: %+v", state.AuthorityDeltas[0])
	}
	if len(state.OpenQuestions) != 1 || state.OpenQuestions[0].Status != "answered" || state.OpenQuestions[0].AnswerSourceKind != "accepted_source" {
		t.Fatalf("accepted-source question was not answered durably: %+v", state.OpenQuestions)
	}
}

func TestFeaturePlannerTUIE2EProductCoveragePromotionContextResumePlanDisplay(t *testing.T) {
	projectRoot := t.TempDir()
	writeFile(t, projectRoot, "go.mod", "module example.com/tui\n")
	writeFile(t, projectRoot, "pkg/collapse_event.go", "package pkg\n")
	writeFile(t, projectRoot, "pkg/collapse_event_test.go", "package pkg\n")

	score := 90.0
	contract := productworkflow.BehavioralCoverageContract{
		SchemaVersion: "idiom.behavioral_coverage.v1",
		ContractID:    "cov-collapse-contract",
		EventID:       "evt-collapse-coverage-promoted",
		CoverageTier:  "behavior_standard",
		EARS: []productworkflow.EARSRequirementCoverage{{
			ID:          "REQ-COLLAPSE",
			Pattern:     "event-driven",
			Statement:   "When collapse dispatch runs, the system shall route through CollapseEvent.",
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
	promotion, err := productworkflow.PromoteBehavioralCoverageContractWithResult(ledger, contract)
	if err != nil {
		t.Fatalf("PromoteBehavioralCoverageContract: %v", err)
	}
	promoted := promotion.Ledger
	if promoted.BehavioralCoverage == nil {
		t.Fatal("promotion should stamp behavioral coverage into tdd.json ledger")
	}

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
	if coverage.Status != productworkflow.ProductWorkflowStatusBlocked || !productworkflow.CoverageGateHasOnlyTDDEvidenceBlockers(coverage) {
		t.Fatalf("expected only pending TDD evidence blockers after evaluation, got status=%q blockers=%#v findings=%#v", coverage.Status, coverage.Blockers, coverage.Findings)
	}

	handoff := productworkflow.BuildImplementationHandoff(productworkflow.BriefGenerationInput{
		EffortRoot:       filepath.Join(projectRoot, "docs", "product-workflows", "efforts", "started", "collapse-effort"),
		ArtifactRootPath: "docs/product-workflows/efforts/started/collapse-effort",
		WorkflowState:    productworkflow.WorkflowState{EffortID: "collapse-effort", TaskID: "collapse-task", StatusBucket: "started", CurrentPhase: productworkflow.PhaseTDDSynthesis, CurrentState: "planning"},
		PRD:              prd,
		ADRIndex:         adrIndex,
		ADRs:             adrs,
		TDD:              promoted,
		Traceability:     traceability,
		ImplementationRecords: []productworkflow.ImplementationLedgerRecord{{
			RecordType: "atom_planned",
			TaskID:     "collapse-task",
			EffortID:   "collapse-effort",
			Scope:      &productworkflow.AtomScope{ExpectedFiles: []string{"pkg/collapse_event.go", "pkg/collapse_event_test.go"}},
		}},
		PromotionEvents: []productworkflow.BehavioralCoveragePromotionEvent{{
			Event:   "behavioral_coverage_accepted",
			EventID: promotion.EventID,
			State:   productworkflow.BehavioralCoveragePromotionStateAccepted,
			Digest:  promotion.Digest,
		}},
		CoverageEvaluation:    coverage,
		ExistingWorkspacePath: map[string]bool{"pkg/collapse_event.go": true, "pkg/collapse_event_test.go": true},
	})
	brief := featureplannerproduct.BriefFromImplementationHandoff(handoff)
	if brief.CoverageContract == nil {
		t.Fatal("feature planner product context should include coverage contract")
	}

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

	if err := featureplanner.ValidatePlanProductWorkflowContext(plan, *brief, projectRoot); err != nil {
		t.Fatalf("ValidatePlanProductWorkflowContext: %v", err)
	}
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

	model := &Model{
		cfg: config.Config{
			Chat:      config.Chat{ProjectRoot: projectRoot},
			Workspace: config.Workspace{Root: projectRoot, CWD: projectRoot},
		},
		workspace: workspace.NewManager(config.Workspace{Root: projectRoot, CWD: projectRoot}),
	}
	statePath, err := model.saveFeaturePlannerStateAndPlanTab(&state)
	if err != nil {
		t.Fatalf("saveFeaturePlannerStateAndPlanTab: %v", err)
	}

	resumed := loadFeaturePlannerCommandStateFile(t, filepath.Join(projectRoot, filepath.FromSlash(statePath)))
	if resumed.Plan.CoverageContract == nil || resumed.Plan.CoverageContract.ContractID != brief.CoverageContract.ContractID {
		t.Fatalf("resume lost coverage contract identity: %+v", resumed.Plan.CoverageContract)
	}

	planTabBody, err := os.ReadFile(filepath.Join(projectRoot, filepath.FromSlash(resumed.PlanPath)))
	if err != nil {
		t.Fatalf("read plan tab: %v", err)
	}
	for _, want := range []string{
		"Coverage contract status: accepted",
		"Coverage contract event: " + brief.CoverageContract.EventID,
		"Coverage contract digest: " + brief.CoverageContract.Digest,
		"Accepted coverage authority: product_workflow",
		"BDD-COLLAPSE: Given accepted collapse coverage exists / When feature planner resumes / Then coverage contract identity is preserved in the plan tab (concrete data)",
		"TDD-COLLAPSE-EVENT - collapse plan preserves accepted product coverage identity (go test ./pkg)",
	} {
		if !strings.Contains(string(planTabBody), want) {
			t.Fatalf("plan tab missing %q:\n%s", want, string(planTabBody))
		}
	}
	if strings.Contains(string(planTabBody), "behavioral_coverage_draft") {
		t.Fatalf("plan tab should not render stale draft coverage authority:\n%s", string(planTabBody))
	}
}

func TestFeaturePlannerTUITurnRetriesEmptyPlannerResponse(t *testing.T) {
	client := &scriptedLLMClient{
		responses:     []string{"", `{"tool":"submit_feature_plan","summary":"ok"}`},
		finishReasons: []string{"length", "stop"},
	}
	m := &Model{client: client}

	answer, err := m.runFeaturePlannerTUITurn(context.Background(), "Plan one feature atom.", string(workerruntime.TaskFamilyFeaturePlannerPlanner))
	if err != nil {
		t.Fatalf("runFeaturePlannerTUITurn: %v", err)
	}

	if answer != `{"tool":"submit_feature_plan","summary":"ok"}` {
		t.Fatalf("answer = %q", answer)
	}

	if client.callCount != 2 {
		t.Fatalf("model calls = %d, want 2", client.callCount)
	}

	if len(client.options) != 2 || !client.options[0].ResponseFormatJSON || client.options[1].ResponseFormatJSON {
		t.Fatalf("unexpected response format options: %#v", client.options)
	}

	if len(client.messages) != 2 || !strings.Contains(client.messages[1][0].Content, "empty visible content") {
		t.Fatalf("retry prompt did not explain empty response: %#v", client.messages)
	}
}

func TestFeaturePlannerConversationEventSanitizerOmitsInternalDiagnostics(t *testing.T) {
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
	if strings.Contains(got.Message, "src/brain_core/types.py") || strings.Contains(got.Message, "pytest project evidence") {
		t.Fatalf("sanitized event leaked diagnostic: %+v", got)
	}
	if _, ok := got.Fields["validation_diagnostic"]; ok {
		t.Fatalf("sanitized event leaked validation_diagnostic: %+v", got.Fields)
	}
	if got.Fields["candidate_id"] != "candidate-1" {
		t.Fatalf("sanitized event lost existing fields: %+v", got.Fields)
	}
	if got.Fields["submitted_atom_count"] != 3 {
		t.Fatalf("sanitized event lost safe count field: %+v", got.Fields)
	}
	if got.Fields["diagnostic_chars"] != len(event.Message) {
		t.Fatalf("diagnostic_chars = %v, want %d", got.Fields["diagnostic_chars"], len(event.Message))
	}
	if got.Fields["diagnostic_fields_omitted"] != 1 {
		t.Fatalf("diagnostic_fields_omitted = %v, want 1", got.Fields["diagnostic_fields_omitted"])
	}
	if _, ok := event.Fields["diagnostic_chars"]; ok {
		t.Fatalf("sanitizer mutated original fields: %+v", event.Fields)
	}

	ordinary := featureplanner.Event{Type: "idiom.feature_planner.planner_question_opened", Message: "question text"}
	if sanitized := sanitizeFeaturePlannerEventForConversation(ordinary); sanitized.Message != ordinary.Message {
		t.Fatalf("ordinary event was sanitized: %+v", sanitized)
	}
}

func TestFeaturePlannerDiagnosticEventLogsAsRunEventNotMessage(t *testing.T) {
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

	model.logFeaturePlannerEvent(featureplanner.Event{
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
	if err != nil {
		t.Fatalf("read debug log: %v", err)
	}

	var foundRunEvent bool
	for _, record := range decodeJSONEvents(t, string(body)) {
		if record["role"] != "idiom.feature_planner.standalone_context_blocked" {
			continue
		}

		if record["type"] == "message" {
			t.Fatalf("feature-planner diagnostic must not be logged as conversation message: %+v", record)
		}
		if record["type"] != "run_event" {
			t.Fatalf("feature-planner diagnostic logged with unexpected type: %+v", record)
		}

		foundRunEvent = true
		content := fmt.Sprint(record["content"])
		if strings.Contains(content, "validation_diagnostic") ||
			strings.Contains(content, "src/brain_core/types.py") ||
			strings.Contains(content, "pytest project evidence") {
			t.Fatalf("feature-planner run_event leaked raw diagnostic: %s", content)
		}
	}

	if !foundRunEvent {
		t.Fatalf("expected sanitized feature-planner run_event in log:\n%s", string(body))
	}
}

func TestFeaturePlannerCommandKeepsInternalRecordsOutOfConversationMessages(t *testing.T) {
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
	model.client = &scriptedLLMClient{responses: []string{`{
		"tool": "ask_planner_question",
		"question_summary": "Approve greenfield scope",
		"blocking_claim": "new files need operator authority before a safe plan can be approved",
		"requested_decision": "Approve creating src/brain.py and tests/test_brain.py, or revise the greenfield project setup.",
		"question_kind": "external_answer_required",
		"authority_required": "operator_approval",
		"required_before": "plan_approval",
		"affected_paths": ["src/brain.py", "tests/test_brain.py"],
		"affected_fields": ["new_file_authorization", "verification_command"]
	}`}}

	answer, err := model.runFeaturePlannerCommandOnce(context.Background(), "Lay out the bones for this LLM brain project.")
	if err != nil {
		t.Fatalf("runFeaturePlannerCommandOnce: %v", err)
	}
	if !strings.Contains(answer, "Feature planner needs your approval before it can continue.") ||
		!strings.Contains(answer, "Also provide the exact verification command for this project.") {
		t.Fatalf("expected planning-question answer, got:\n%s", answer)
	}

	logPath := model.DebugLogPath()
	model.Close()

	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read debug log: %v", err)
	}

	foundRunEvents := map[string]bool{}
	for _, record := range decodeJSONEvents(t, string(body)) {
		role := fmt.Sprint(record["role"])
		if record["type"] == "message" && featurePlannerInternalLogRole(role) {
			t.Fatalf("feature-planner internal role logged as conversation message: %+v", record)
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
		if !foundRunEvents[want] {
			t.Fatalf("expected feature-planner internal run_event %q in log:\n%s", want, string(body))
		}
	}
}

func TestFeaturePlannerTUIConvertsGreenfieldMVPRepairIntoApprovalPrompt(t *testing.T) {
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
	model.client = &scriptedLLMClient{responses: []string{featurePlannerCommandPlanJSON(t, plan)}}

	answer, err := model.runFeaturePlannerCommandOnce(context.Background(), `Can you work out an MVP to an effective "llm brain" sort of project?`)
	if err != nil {
		t.Fatalf("runFeaturePlannerCommandOnce: %v", err)
	}
	model.Close()

	for _, want := range []string{
		"Feature planner needs your approval before it can continue.",
		"What this proposed plan entails:",
		"Objective: Establish the minimal LLM brain project structure.",
		"Proposed paths:",
		"- src/brain/knowledge_store.py",
		"- tests/test_knowledge_store.py",
		"Also provide the exact verification command for this project.",
		"Reply with approval, revised paths/setup, or rejection. Other questions will stay in chat.",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("approval prompt missing %q:\n%s", want, answer)
		}
	}
	for _, forbidden := range []string{
		"feature-planner: planning-repair",
		"blockers:",
		"atom 0 edit path",
		"has no pytest project evidence",
		"missing standalone new-file authorization",
		"next: repair_feature_plan",
	} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("approval prompt leaked repair text %q:\n%s", forbidden, answer)
		}
	}
}

func featurePlannerInternalLogRole(role string) bool {
	role = strings.TrimSpace(role)

	return strings.HasPrefix(role, "feature_planner_") ||
		strings.HasPrefix(role, "idiom.feature_planner.") ||
		strings.HasPrefix(role, "idiom.plan_execute.")
}

func TestFeaturePlannerTUIOperatorQuestionUsesApprovalPrompt(t *testing.T) {
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

	answer := formatFeaturePlannerTUIAnswer(state, ".idiom/feature-planner/question-session/state.json")
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
		"Also provide the exact verification command for this project.",
		"Reply with approval, revised paths/setup, or rejection. Other questions will stay in chat.",
		"State saved at .idiom/feature-planner/question-session/state.json",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("TUI answer missing %q:\n%s", want, answer)
		}
	}

	for _, forbidden := range []string{
		"feature-planner: planning-question-open",
		"requested_decision:",
		"blockers:",
		"next:",
		"atom 0 edit path",
	} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("TUI answer leaked state-summary/internal text %q:\n%s", forbidden, answer)
		}
	}
}

func TestFeaturePlannerTUIGenericOperatorQuestionKeepsStateSummary(t *testing.T) {
	state := featureplanner.NewState("question-session", "need approval", "", 3, time.Unix(1, 0).UTC())
	state.Status = featureplanner.FeatureStatePlanningQuestion
	state.OpenQuestions = []featureplanner.PlannerQuestion{{
		Status:            "open",
		QuestionSummary:   "Confirm product decision",
		AuthorityRequired: "operator_approval",
		RequestedDecision: "Confirm whether this belongs in the current effort.",
		AffectedFields:    []string{"product_scope"},
	}}

	answer := formatFeaturePlannerTUIAnswer(state, ".idiom/feature-planner/question-session/state.json")
	if !strings.Contains(answer, "feature-planner: "+featureplanner.FeatureStatePlanningQuestion) ||
		!strings.Contains(answer, "question: Confirm product decision") {
		t.Fatalf("generic operator question should keep state summary:\n%s", answer)
	}
	if strings.Contains(answer, "Feature planner needs your approval before it can continue.") ||
		strings.Contains(answer, "Proposed paths:") {
		t.Fatalf("generic operator question should not use greenfield scope authorization presenter:\n%s", answer)
	}
}

func TestFeaturePlannerProductWriteEnvelopeExcludesContextEvidencePaths(t *testing.T) {
	brief := &featureplanner.ProductWorkflowBrief{
		FilesToModify:             []string{"./native/src/core/colony_sim.h", "native/src/core/colony_sim.h"},
		ContextEvidencePaths:      []string{"docs/product-workflows/efforts/started/native-quality/prd.md"},
		AuthorizedNewPackageRoots: []string{"./native/tests/generated/", "../outside"},
	}

	got := featureplanner.ProductWriteEnvelopePaths(brief)
	for _, want := range []string{"native/src/core/colony_sim.h", "native/tests/generated/**"} {
		if !slices.Contains(got, want) {
			t.Fatalf("write envelope missing authorized mutation path %q: %#v", want, got)
		}
	}

	if slices.Contains(got, "docs/product-workflows/efforts/started/native-quality/prd.md") {
		t.Fatalf("context evidence path must not become write authority: %#v", got)
	}

	for _, forbidden := range []string{"./native/src/core/colony_sim.h", "../outside/**"} {
		if slices.Contains(got, forbidden) {
			t.Fatalf("write envelope should normalize or drop invalid path %q: %#v", forbidden, got)
		}
	}
}

func TestFeaturePlannerTUIAnswerIncludesOpenQuestionDetails(t *testing.T) {
	state := featureplanner.NewState("question-session", "need scope", "", 3, time.Unix(1, 0).UTC())

	state.Status = featureplanner.FeatureStatePlanningQuestion
	state.OpenQuestions = []featureplanner.PlannerQuestion{{
		Status:          "open",
		QuestionSummary: "Which tooltip path owns the row?",
	}}

	answer := formatFeaturePlannerTUIAnswer(state, ".idiom/feature-planner/question-session/state.json")
	if !strings.Contains(answer, "open questions: 1") || !strings.Contains(answer, "question: Which tooltip path owns the row?") {
		t.Fatalf("TUI answer omitted question details:\n%s", answer)
	}
}

func TestFeaturePlannerTUIDirectCommandSkipsClosedSourceSatisfiedEffort(t *testing.T) {
	projectRoot := t.TempDir()
	effortRel := filepath.Join("docs", "product-workflows", "efforts", "started", "native-quality")
	effortRoot := filepath.Join(projectRoot, effortRel)
	writeNativeQualityEffort(t, effortRoot)

	workflowState, err := productworkflow.LoadWorkflowState(effortRoot)
	if err != nil {
		t.Fatalf("load workflow state: %v", err)
	}

	workflowState.StatusBucket = "closed"
	workflowState.CurrentPhase = "source-satisfied-closeout"
	workflowState.CurrentState = "closed"
	workflowState.NextValidActions = []string{"inspect_closed_effort"}
	writeFeaturePlannerCommandJSON(t, filepath.Join(effortRoot, "workflow_state.json"), workflowState)

	m := &Model{
		client: &scriptedLLMClient{},
		cfg: config.Config{
			Chat:      config.Chat{ProjectRoot: projectRoot},
			Workspace: config.Workspace{Root: projectRoot, CWD: projectRoot},
		},
		workspace: workspace.NewManager(config.Workspace{Root: projectRoot, CWD: projectRoot}),
	}

	answer, err := m.runFeaturePlannerCommandOnce(context.Background(), "Continue existing effort at "+filepath.ToSlash(effortRel)+".")
	if err != nil {
		t.Fatalf("runFeaturePlannerCommandOnce: %v", err)
	}

	if !strings.Contains(answer, "feature-planner: "+featureplanner.FeatureStateSourceSatisfied) {
		t.Fatalf("expected source-satisfied answer, got:\n%s", answer)
	}

	if m.lastPlanExecuteRunResult != nil {
		t.Fatalf("plan-execute should not be dispatched for closed/source-satisfied effort")
	}

	statePath := featurePlannerCommandAnswerStatePath(answer)
	if statePath == "" {
		t.Fatalf("answer missing state path:\n%s", answer)
	}

	state := loadFeaturePlannerCommandStateFile(t, filepath.Join(projectRoot, filepath.FromSlash(statePath)))
	if state.Status != featureplanner.FeatureStateSourceSatisfied || state.EffortID != "native-quality" {
		t.Fatalf("unexpected saved state: %#v", state)
	}
}

func featurePlannerCommandAnswerStatePath(answer string) string {
	for _, line := range strings.Split(answer, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "state:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "state:"))
		}
	}

	return ""
}

func loadFeaturePlannerCommandStateFile(t *testing.T, path string) featureplanner.FeatureState {
	t.Helper()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved feature planner state: %v", err)
	}

	var state featureplanner.FeatureState

	if err := json.Unmarshal(body, &state); err != nil {
		t.Fatalf("decode saved feature planner state: %v", err)
	}

	return state
}

func TestSanitizeEffortSlug(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"build user auth", "build-user-auth"},
		{"Add OAuth 2.0 support!!!", "add-oauth-2-0-support"},
		{"  spaces  everywhere  ", "spaces-everywhere"},
		{"UPPERCASE", "uppercase"},
		{"", ""}, // empty returns a timestamp-based slug, not testable here
		{"a", "a"},
		{"a very long feature description that goes on and on and on and on forever", "a-very-long-feature-description-that-goes-on-and-on-and-on"},
		{"already-slugged", "already-slugged"},
		{"-leading-and-trailing-", "leading-and-trailing"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeEffortSlug(tt.input)
			if tt.input == "" {
				if got == "" {
					t.Error("empty input should produce a timestamp-based slug")
				}

				return
			}

			if got != tt.expected {
				t.Errorf("sanitizeEffortSlug(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func writeNativeQualityEffort(t *testing.T, effortRoot string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Join(effortRoot, "adrs"), 0o755); err != nil {
		t.Fatalf("mkdir adrs: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(effortRoot, "evidence"), 0o755); err != nil {
		t.Fatalf("mkdir evidence: %v", err)
	}

	writeFeaturePlannerCommandJSON(t, filepath.Join(effortRoot, "workflow_state.json"), productworkflow.WorkflowState{
		SchemaVersion: "idiom.product_workflow_state.v1",
		EffortID:      "native-quality",
		TaskID:        "mwe-1.3b-native-quality",
		Title:         "Native quality contributor breakdown",
		CreatedAt:     "2026-06-05T00:00:00Z",
		UpdatedAt:     "2026-06-05T00:00:00Z",
		StatusBucket:  "started",
		CurrentPhase:  productworkflow.PhaseTDDSynthesis,
		CurrentState:  "planning",
		ArtifactPaths: map[string]string{"prd": "prd.md", "tdd": "tdd.json"},
		Context: productworkflow.WorkflowContext{
			ContextPackID:   "native-pack",
			FreshnessState:  "current",
			ReferencedPaths: []string{"native/src/core/colony_sim.h", "native/tests/test_quality_inventory_bindings.cpp"},
		},
		Coverage:         productworkflow.CoverageState{CoverageTier: "unit"},
		Privacy:          productworkflow.PrivacyState{RedactionStatus: "clean"},
		FolderPath:       effortRoot,
		DurabilityLevel:  "source_controlled",
		NextValidActions: []string{"feature-planner"},
	})

	if err := productworkflow.WritePRDMarkdown(effortRoot, productworkflow.PRDDocument{
		FrontMatter: productworkflow.PRDFrontMatter{
			SchemaVersion:     "idiom.prd.v1",
			PRDID:             "native-quality",
			Status:            "accepted",
			TaskID:            "mwe-1.3b-native-quality",
			CreatedAt:         "2026-06-05T00:00:00Z",
			AcceptedAt:        "2026-06-05T00:01:00Z",
			AcceptedBy:        "test",
			AcceptanceProfile: "human-review",
		},
		Requirements: []productworkflow.PRDRequirement{{
			ID:             "REQ-NATIVE",
			Priority:       "P1",
			Statement:      "Expose quality contributor breakdown through the native inventory binding.",
			EvidenceRefs:   []string{"EV-NATIVE"},
			TDDObligations: []string{"TDD-NATIVE"},
			OwnerAlignment: "Players need legible quality provenance.",
			Testability:    "bin/colony_tests -ts=quality_inventory_bindings",
			Metric:         "Native binding reports contributor rows.",
		}},
	}); err != nil {
		t.Fatalf("write prd.md: %v", err)
	}

	writeFeaturePlannerCommandJSON(t, filepath.Join(effortRoot, "adrs", "index.json"), productworkflow.ADRIndex{
		SchemaVersion: "idiom.adr_index.v1",
		EffortID:      "native-quality",
		ADRs: []productworkflow.ADRIndexEntry{{
			ADRID:        "ADR-NATIVE",
			Title:        "Use native bindings",
			Status:       "accepted",
			Path:         "001-native-binding.md",
			CoverageTier: "unit",
			PRDRefs:      []string{"REQ-NATIVE"},
		}},
	})

	if err := productworkflow.WriteADRMarkdown(effortRoot, "001-native-binding.md", productworkflow.ADRDocument{
		FrontMatter: productworkflow.ADRFrontMatter{
			SchemaVersion:     "idiom.adr.v1",
			ADRID:             "ADR-NATIVE",
			Status:            "accepted",
			PRDRefs:           []string{"REQ-NATIVE"},
			EvidenceRefs:      []string{"EV-NATIVE"},
			TDDObligations:    []string{"TDD-NATIVE"},
			CoverageTier:      "unit",
			AcceptedAt:        "2026-06-05T00:02:00Z",
			AcceptedBy:        "test",
			AcceptanceProfile: "human-review",
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
	}); err != nil {
		t.Fatalf("write ADR: %v", err)
	}

	writeFeaturePlannerCommandJSON(t, filepath.Join(effortRoot, "tdd.json"), productworkflow.TDDLedger{
		SchemaVersion: "idiom.tdd_ledger.v1",
		EffortID:      "native-quality",
		TaskID:        "mwe-1.3b-native-quality",
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
		TaskID:        "mwe-1.3b-native-quality",
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

	records := []productworkflow.ImplementationLedgerRecord{{
		RecordType:     "atom_planned",
		TaskID:         "mwe-1.3b-native-quality",
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
		RecordType:              "verification_completed",
		TaskID:                  "mwe-1.3b-native-quality",
		EffortID:                "native-quality",
		Timestamp:               "2026-06-05T00:05:00Z",
		TDDObligationsSatisfied: []string{"TDD-NATIVE"},
	}}
	writeFeaturePlannerCommandJSONL(t, filepath.Join(effortRoot, "implementation_ledger.jsonl"), records)
}

func featurePlannerCommandFloatPtr(value float64) *float64 {
	return &value
}

func featurePlannerCommandBadGoPlanJSON(t *testing.T) string {
	t.Helper()

	plan := featureplanner.FeaturePlan{
		Tool:          featureplanner.ToolSubmitFeaturePlan,
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

func featurePlannerCommandDemotedFilesPlanJSON(t *testing.T) string {
	t.Helper()

	plan := featurePlannerCommandNativePlan()

	plan.Atoms[0].RequiredEditPaths = []string{"native/src/core/colony_sim.h"}
	plan.Atoms[0].AllowedEditPaths = []string{"native/tests/test_quality_inventory_bindings.cpp"}

	return featurePlannerCommandPlanJSON(t, plan)
}

func featurePlannerCommandNativePlan() featureplanner.FeaturePlan {
	return featureplanner.FeaturePlan{
		Tool:          featureplanner.ToolSubmitFeaturePlan,
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

func featurePlannerCommandNewFileQuestionJSON(t *testing.T) string {
	t.Helper()
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
		Tool:            featureplanner.ToolAskPlannerQuestion,
		PlannerQuestion: question,
	})
	if err != nil {
		t.Fatalf("marshal question: %v", err)
	}
	return string(body)
}

func featurePlannerCommandAcceptedSourceQuestionJSON(t *testing.T) string {
	t.Helper()
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
		Tool:            featureplanner.ToolAskPlannerQuestion,
		PlannerQuestion: question,
	})
	if err != nil {
		t.Fatalf("marshal question: %v", err)
	}
	return string(body)
}

func featurePlannerCommandPlanJSON(t *testing.T, plan featureplanner.FeaturePlan) string {
	t.Helper()

	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal feature plan: %v", err)
	}

	return string(data)
}

func writeFeaturePlannerCommandJSON(t *testing.T, path string, value any) {
	t.Helper()

	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}

	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeFeaturePlannerCommandJSONL(t *testing.T, path string, records []productworkflow.ImplementationLedgerRecord) {
	t.Helper()

	var lines []string

	for _, record := range records {
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatalf("marshal ledger record: %v", err)
		}

		lines = append(lines, string(data))
	}

	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestSplitAndParseADRs(t *testing.T) {
	prd := productworkflow.PRDDocument{
		Requirements: []productworkflow.PRDRequirement{
			{ID: "REQ-001", Priority: "P1"},
			{ID: "REQ-002", Priority: "P2"},
		},
	}

	t.Run("single ADR", func(t *testing.T) {
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

		docs, entries, err := splitAndParseADRs(output, prd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(docs) != 1 {
			t.Fatalf("expected 1 doc, got %d", len(docs))
		}

		if docs[0].FrontMatter.ADRID != "ADR-001" {
			t.Errorf("expected ADR-001, got %s", docs[0].FrontMatter.ADRID)
		}

		if !docs[0].Sections.HasContext {
			t.Error("expected HasContext to be true")
		}

		if !docs[0].Sections.HasDecision {
			t.Error("expected HasDecision to be true")
		}

		if len(entries) != 1 {
			t.Fatalf("expected 1 index entry, got %d", len(entries))
		}

		if entries[0].Path != "ADR-001.md" {
			t.Errorf("expected ADR-001.md, got %s", entries[0].Path)
		}
	})

	t.Run("multiple ADRs with === separator", func(t *testing.T) {
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

		docs, entries, err := splitAndParseADRs(output, prd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(docs) != 2 {
			t.Fatalf("expected 2 docs, got %d", len(docs))
		}

		if docs[0].FrontMatter.ADRID != "ADR-001" {
			t.Errorf("expected ADR-001 first, got %s", docs[0].FrontMatter.ADRID)
		}

		if docs[1].FrontMatter.ADRID != "ADR-002" {
			t.Errorf("expected ADR-002 second, got %s", docs[1].FrontMatter.ADRID)
		}

		if len(entries) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(entries))
		}
	})

	t.Run("auto-assigns ADR ID when missing", func(t *testing.T) {
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

		docs, _, err := splitAndParseADRs(output, prd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if docs[0].FrontMatter.ADRID != "ADR-001" {
			t.Errorf("expected auto-assigned ADR-001, got %s", docs[0].FrontMatter.ADRID)
		}
	})

	t.Run("skips invalid ADRs", func(t *testing.T) {
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

		docs, _, err := splitAndParseADRs(output, prd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(docs) != 2 {
			t.Fatalf("expected 2 valid docs (skipping garbage), got %d", len(docs))
		}
	})
}

func TestPlanExecuteAnswerIsBlocked(t *testing.T) {
	for _, input := range []string{
		"",
		"<<QUESTION>>: Plan-execute could not produce a plan artifact.",
		"plan-execute: recovery-open\nblockers: required_edit_paths_remaining",
	} {
		if !planExecuteAnswerIsBlocked(input) {
			t.Fatalf("expected blocked plan-execute answer for %q", input)
		}
	}

	if planExecuteAnswerIsBlocked("Decision: source-satisfied\n- internal/example/example.go: already satisfied") {
		t.Fatal("source-satisfied answer should remain eligible for completion wrapper")
	}
}

func TestProductWorkflowFeaturePlannerRecoveryBlocksCompletion(t *testing.T) {
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
	if !blocked {
		t.Fatal("recovery-open child must block product completion")
	}

	for _, want := range []string{
		"feature-planner: recovery_open",
		"session_id: feature-session",
		"state_path: .idiom/feature-planner/feature-session/state.json",
		"atom=atom-1 state=recovery-open session=plan-execute-session-1 required_paths=internal/surfaces/tui/app/product_status.go",
		"blockers=source sanity failed",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("recovery message missing %q:\n%s", want, msg)
		}
	}
}

func TestProductWorkflowFeaturePlannerRecoverySurfacesOpenQuestionDetails(t *testing.T) {
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
	if !blocked {
		t.Fatal("open child question must block product completion")
	}

	for _, want := range []string{
		"progress_state: .idiom/plan-execute/pe-session/state.json",
		"source_sanity: internal/surfaces/tui/app/product_status.go failed compile failed",
		"evidence_refs=.idiom/plan-execute/pe-session/state.json",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("recovery message missing question detail %q:\n%s", want, msg)
		}
	}
}

func TestProductWorkflowFeaturePlannerRecoveryAllowsCompleteState(t *testing.T) {
	state := featureplanner.NewState("feature-session", "prompt", "", 3, time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC))

	state.Status = featureplanner.FeatureStateComplete
	state.Atoms = []featureplanner.FeatureAtomState{{
		FeatureAtomPlan: featureplanner.FeatureAtomPlan{ID: "atom-1"},
		State:           featureplanner.AtomStateComplete,
	}}

	if msg, blocked := formatProductWorkflowFeaturePlannerRecovery(state, ""); blocked {
		t.Fatalf("complete children should not block product completion: %s", msg)
	}
}

func TestFeaturePlannerReconcilesStaleActiveRunFromTerminalPlanExecuteProgress(t *testing.T) {
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
		State:         "running",
		StartedAt:     startedAt.Format(time.RFC3339Nano),
	}}

	progress := planexecute.NewProgressState("atom-1-run-1", "prompt", "", []string{"pkg/feature.go"}, nil, startedAt.Add(time.Minute))

	progress.Phase = planexecute.ProgressPhasePlanning
	progress.Status = planexecute.ProgressStatusFailed
	progress.FailureKind = planexecute.RunFailurePlannerBlocked
	progress.FailureReason = "<<QUESTION>>: pkg/feature.go source evidence unavailable"

	progressPath, err := planexecute.SaveProgressState(root, &progress)
	if err != nil {
		t.Fatalf("SaveProgressState: %v", err)
	}

	model := &Model{cfg: config.Config{Chat: config.Chat{ProjectRoot: root}, Workspace: config.Workspace{Root: root}}}
	events := model.reconcileFeaturePlannerActivePlanExecuteRuns(&state)

	if state.Status != featureplanner.FeatureStateRecoveryOpen {
		t.Fatalf("feature state = %q, want recovery-open", state.Status)
	}

	if state.Atoms[0].State != featureplanner.AtomStateRecoveryOpen {
		t.Fatalf("atom state = %q, want recovery-open", state.Atoms[0].State)
	}

	if len(state.ActiveRuns) != 0 {
		t.Fatalf("active run should be folded after durable terminal progress: %+v", state.ActiveRuns)
	}

	if len(events) != 1 || events[0].Type != "idiom.feature_planner.plan_execute_completion_rejected" {
		t.Fatalf("unexpected events: %+v", events)
	}

	for _, want := range []string{
		"nested plan-execute reported failure details",
		"planner_blocked",
		"pkg/feature.go source evidence unavailable",
	} {
		if !strings.Contains(events[0].Message, want) {
			t.Fatalf("event message missing %q: %+v", want, events[0])
		}
	}

	if got := events[0].Fields["completion_source"]; got != "durable_plan_execute_progress" {
		t.Fatalf("completion_source = %v, want durable_plan_execute_progress; fields=%+v", got, events[0].Fields)
	}

	if got := events[0].Fields["progress_path"]; got != progressPath {
		t.Fatalf("progress_path = %v, want %s; fields=%+v", got, progressPath, events[0].Fields)
	}
}

func TestFeaturePlannerReconcilesSkippedReviewProgressAsComplete(t *testing.T) {
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
		State:                "running",
		StartedAt:            startedAt.Format(time.RFC3339Nano),
	}}

	progress := planexecute.NewProgressState("atom-1-run-1", "prompt", "", []string{"pkg/feature.go"}, nil, startedAt.Add(time.Minute))

	progress.Phase = planexecute.ProgressPhaseComplete
	progress.Status = planexecute.ProgressStatusCompleted
	progress.ChangedPaths = []string{"pkg/feature.go"}
	progress.Executor.SourceSanity = []planexecute.PhaseEvidenceCheck{{
		Kind:   "source_sanity",
		Path:   "pkg/feature.go",
		Status: "ok",
	}}
	progress.Review.Status = planexecute.ProgressStatusSkipped

	progressPath, err := planexecute.SaveProgressState(root, &progress)
	if err != nil {
		t.Fatalf("SaveProgressState: %v", err)
	}

	model := &Model{cfg: config.Config{Chat: config.Chat{ProjectRoot: root}, Workspace: config.Workspace{Root: root}}}
	events := model.reconcileFeaturePlannerActivePlanExecuteRuns(&state)

	if state.Status != featureplanner.FeatureStateComplete {
		t.Fatalf("feature state = %q, want complete; events=%+v blockers=%+v", state.Status, events, state.Blockers)
	}

	if state.Atoms[0].State != featureplanner.AtomStateComplete {
		t.Fatalf("atom state = %q, want complete; events=%+v blockers=%+v", state.Atoms[0].State, events, state.Atoms[0].Blockers)
	}

	if len(state.ActiveRuns) != 0 {
		t.Fatalf("active run should be folded after durable terminal progress: %+v", state.ActiveRuns)
	}

	if len(events) != 1 || events[0].Type != "idiom.feature_planner.atom_completed" {
		t.Fatalf("unexpected events: %+v", events)
	}

	if got := events[0].Fields["review_repair_skipped"]; got != true {
		t.Fatalf("review_repair_skipped = %v, want true; fields=%+v", got, events[0].Fields)
	}

	if got := events[0].Fields["progress_path"]; got != progressPath {
		t.Fatalf("progress_path = %v, want %s; fields=%+v", got, progressPath, events[0].Fields)
	}
}

func TestPlanExecuteExecutionRecoveryOpenMessageSurfacesSourceSanity(t *testing.T) {
	root := t.TempDir()
	state := planexecute.NewProgressState("pe-session", "prompt", "", []string{"internal/surfaces/tui/app/product_status.go"}, nil, time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC))

	state.Phase = planexecute.ProgressPhaseReviewRepair
	state.Status = planexecute.ProgressStatusFailed
	state.FailureKind = planexecute.RunFailureSourceSanityFailed
	state.FailureReason = "source_sanity failed for generated source"
	state.Executor.SourceSanity = []planexecute.PhaseEvidenceCheck{{
		Kind:   "source_sanity",
		Path:   "internal/surfaces/tui/app/product_status.go",
		Status: "failed",
		Error:  "compile failed",
	}}

	statePath, err := planexecute.SaveProgressState(root, &state)
	if err != nil {
		t.Fatalf("SaveProgressState: %v", err)
	}

	msg, ok := humanPlanExecuteExecutionRecoveryOpenMessage(root, planexecute.RunResult{
		ExitCode:        1,
		SessionID:       "pe-session",
		ResumeStatePath: statePath,
		FailureKind:     planexecute.RunFailureSourceSanityFailed,
		FailureReason:   "source_sanity failed for generated source",
	}, nil)
	if !ok {
		t.Fatal("expected source-sanity failure to surface as recovery-open")
	}

	for _, want := range []string{
		"plan-execute: recovery_open",
		"session_id: pe-session",
		"failure_kind: source_sanity_failed",
		"required_edit_paths:\n- internal/surfaces/tui/app/product_status.go",
		"source_sanity:\n- internal/surfaces/tui/app/product_status.go failed compile failed",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("source-sanity recovery message missing %q:\n%s", want, msg)
		}
	}
}

func TestBuildProductWorkflowBriefIncludesWorkflowReferencedPaths(t *testing.T) {
	brief := buildProductWorkflowBrief(
		"docs/product-workflows/efforts/example",
		productworkflow.WorkflowState{
			Context: productworkflow.WorkflowContext{
				ReferencedPaths: []string{"internal/surfaces/tui/app/product_status.go"},
			},
		},
		productworkflow.PRDDocument{},
		productworkflow.ADRIndex{},
		nil,
		productworkflow.TDDLedger{EffortID: "effort-1", TaskID: "task-1"},
		productworkflow.TraceabilityMatrix{},
		nil,
	)

	if !slices.Contains(brief.ContextEvidencePaths, "internal/surfaces/tui/app/product_status.go") {
		t.Fatalf("expected workflow referenced path in ContextEvidencePaths, got %#v", brief.ContextEvidencePaths)
	}
}

func TestBuildProductWorkflowBriefCarriesAcceptedBehavioralCoverageWhileTDDEvidencePending(t *testing.T) {
	effortRoot := filepath.Join(t.TempDir(), "effort")
	contract := productworkflow.BehavioralCoverageContract{
		ContractID:   "cov-product",
		EffortID:     "effort-product",
		TaskID:       "task-product",
		CoverageTier: "behavior_standard",
		EventID:      "evt-product-coverage",
		EARS: []productworkflow.EARSRequirementCoverage{{
			ID:          "REQ-1",
			Statement:   "When accepted coverage exists, the system shall display it in feature planning.",
			MappedRefs:  []string{"BDD-1"},
			Obligations: []string{"TDD-1"},
		}},
		BDDScenarios: []productworkflow.BDDScenarioCoverage{{
			ID:               "BDD-1",
			RequirementID:    "REQ-1",
			Given:            []string{"accepted behavioral coverage exists"},
			When:             []string{"the TUI builds the feature planner brief"},
			Then:             []string{"accepted coverage appears in the plan context"},
			ConcreteDataRefs: []string{"fixtures/accepted-coverage.json"},
			Obligations:      []string{"TDD-1"},
		}},
		DesignContracts: []productworkflow.DesignContractCoverage{{
			ID:             "DBC-1",
			Boundary:       "productworkflow to featureplanner",
			Preconditions:  []string{"behavioral coverage is accepted"},
			Postconditions: []string{"coverage detail is read-only planner context"},
			Invariants:     []string{"planner cannot author accepted coverage"},
			TestRefs:       []string{"TestBuildProductWorkflowBriefCarriesAcceptedBehavioralCoverageWhileTDDEvidencePending"},
			Obligations:    []string{"TDD-1"},
		}},
		FaultTreeLeaves: []productworkflow.FaultTreeLeafCoverage{{
			ID:          "FTA-1",
			TopEvent:    "accepted coverage dropped",
			Cause:       "adapter omits non-BDD coverage detail",
			TestRefs:    []string{"TestBuildProductWorkflowBriefCarriesAcceptedBehavioralCoverageWhileTDDEvidencePending"},
			Obligations: []string{"TDD-1"},
		}},
		PostMortems: []productworkflow.PostMortemCoverage{{
			ID:               "PM-1",
			Incident:         "coverage detail was missing from planner context",
			CatchingTestRefs: []string{"TestBuildProductWorkflowBriefCarriesAcceptedBehavioralCoverageWhileTDDEvidencePending"},
			Obligations:      []string{"TDD-1"},
		}},
		FormalModels: []productworkflow.FormalModelCoverage{{
			ID:               "FORMAL-1",
			Model:            "accepted_coverage_boundary",
			VerificationRefs: []string{"models/accepted_coverage_boundary.tla"},
			Obligations:      []string{"TDD-1"},
		}},
		TestObligations: []productworkflow.TestObligationCoverage{{
			ID:             "TDD-1",
			Type:           "contract",
			Behavior:       "accepted behavioral coverage reaches the feature planner brief",
			Command:        "go test ./internal/surfaces/tui/app",
			RequiredBefore: "implementation",
		}},
	}
	ledger := productworkflow.ProjectBehavioralCoverageContractLedger(contract)
	ledger.Obligations[0].Status = "planned"
	prd := productworkflow.PRDDocument{
		FrontMatter: productworkflow.PRDFrontMatter{Status: "accepted"},
		Requirements: []productworkflow.PRDRequirement{{
			ID:             "REQ-1",
			Priority:       "must",
			Statement:      "Display accepted coverage in feature planning.",
			EvidenceRefs:   []string{"EV-1"},
			TDDObligations: []string{"TDD-1"},
			Testability:    "go test ./internal/surfaces/tui/app",
		}},
	}
	adrIdx := productworkflow.ADRIndex{ADRs: []productworkflow.ADRIndexEntry{{
		ADRID:  "ADR-1",
		Status: "accepted",
		Path:   "001-coverage.md",
	}}}
	adrs := []productworkflow.ParsedADR{{
		RelPath: "adrs/001-coverage.md",
		Doc: productworkflow.ADRDocument{
			FrontMatter: productworkflow.ADRFrontMatter{
				ADRID:          "ADR-1",
				Status:         "accepted",
				EvidenceRefs:   []string{"EV-1"},
				TDDObligations: []string{"TDD-1"},
			},
			BodyMarkdown: "## Decision\nCarry accepted coverage into feature planning.\n\n## Open Questions\nNone.",
		},
	}}
	traceability := productworkflow.TraceabilityMatrix{Matrix: []productworkflow.TraceabilityRow{{
		EvidenceCard:   "EV-1",
		PRDRequirement: "REQ-1",
		ADR:            "ADR-1",
		TDDObligation:  "TDD-1",
	}}}

	workflowState := productworkflow.WorkflowState{LastEventID: "evt-product-coverage"}
	implementationRecords := []productworkflow.ImplementationLedgerRecord{{
		RecordType: "atom_planned",
		TaskID:     "task-product",
		Scope:      &productworkflow.AtomScope{ExpectedFiles: []string{"internal/surfaces/tui/app/feature_planner_product.go"}},
	}}
	promotionEvents := []productworkflow.BehavioralCoveragePromotionEvent{{
		Event:   "behavioral_coverage_accepted",
		EventID: "evt-product-coverage",
		State:   productworkflow.BehavioralCoveragePromotionStateAccepted,
		Digest:  productworkflow.BehavioralCoverageContractDigest(contract),
	}}
	brief := buildProductWorkflowBriefWithPromotionEvents(
		effortRoot,
		workflowState,
		prd,
		adrIdx,
		adrs,
		ledger,
		traceability,
		implementationRecords,
		promotionEvents,
	)

	if brief.CoverageGatePassed {
		t.Fatal("TUI brief should preserve real pending TDD evidence gate status")
	}
	if !slices.Contains(brief.CoverageBlockerKinds, "coverage_tdd_evidence_missing") {
		t.Fatalf("expected TDD evidence blocker kind, got %#v", brief.CoverageBlockerKinds)
	}
	if brief.CoverageContract == nil ||
		brief.CoverageContract.ContractID != "cov-product" ||
		brief.CoverageContract.Status != productworkflow.BehavioralCoverageStatusAccepted {

		t.Fatalf("expected accepted coverage contract ref, got %+v", brief.CoverageContract)
	}
	if brief.AcceptedCoverage == nil ||
		brief.AcceptedCoverage.Authority != "product_workflow" ||
		len(brief.AcceptedCoverage.Scenarios) != 1 ||
		brief.AcceptedCoverage.Scenarios[0].Then[0] != "accepted coverage appears in the plan context" ||
		len(brief.AcceptedCoverage.FaultTreeLeaves) != 1 ||
		brief.AcceptedCoverage.FaultTreeLeaves[0].TopEvent != "accepted coverage dropped" ||
		len(brief.AcceptedCoverage.PostMortems) != 1 ||
		brief.AcceptedCoverage.PostMortems[0].Incident != "coverage detail was missing from planner context" ||
		len(brief.AcceptedCoverage.FormalModels) != 1 ||
		brief.AcceptedCoverage.FormalModels[0].Model != "accepted_coverage_boundary" ||
		len(brief.AcceptedCoverage.TestObligations) != 1 {

		t.Fatalf("expected accepted behavioral coverage in TUI brief, got %+v", brief.AcceptedCoverage)
	}
	if !brief.AllowsFeaturePlanDispatch() {
		t.Fatalf("pending implementation TDD evidence should allow feature planning, blockers=%#v coverage_blockers=%#v kinds=%#v tdd=%#v", brief.Blockers, brief.CoverageBlockers, brief.CoverageBlockerKinds, brief.TDDObligations)
	}

	coverage := productworkflow.EvaluateCoverageGate(prd, adrIdx, adrs, ledger, traceability)
	expected := featureplannerproduct.BriefFromImplementationHandoff(productworkflow.BuildImplementationHandoff(productworkflow.BriefGenerationInput{
		EffortRoot:            effortRoot,
		ArtifactRootPath:      effortRoot,
		WorkflowState:         workflowState,
		PRD:                   prd,
		ADRIndex:              adrIdx,
		ADRs:                  adrs,
		TDD:                   ledger,
		Traceability:          traceability,
		ImplementationRecords: implementationRecords,
		PromotionEvents:       promotionEvents,
		CoverageEvaluation:    coverage,
	}))
	for _, check := range []struct {
		name string
		got  any
		want any
	}{
		{"coverage_contract", brief.CoverageContract, expected.CoverageContract},
		{"accepted_coverage", brief.AcceptedCoverage, expected.AcceptedCoverage},
		{"prd_requirements", brief.PRDRequirements, expected.PRDRequirements},
		{"accepted_adr_ids", brief.AcceptedADRIDs, expected.AcceptedADRIDs},
		{"accepted_adr_paths", brief.AcceptedADRPaths, expected.AcceptedADRPaths},
		{"tdd_obligations", brief.TDDObligations, expected.TDDObligations},
		{"required_behavior", brief.RequiredBehavior, expected.RequiredBehavior},
		{"tests", brief.Tests, expected.Tests},
		{"acceptance", brief.Acceptance, expected.Acceptance},
		{"decisions", brief.Decisions, expected.Decisions},
		{"coverage_blockers", brief.CoverageBlockers, expected.CoverageBlockers},
		{"coverage_blocker_kinds", brief.CoverageBlockerKinds, expected.CoverageBlockerKinds},
	} {
		if !reflect.DeepEqual(check.got, check.want) {
			t.Fatalf("TUI product brief drifted from handoff adapter for %s:\ngot  %#v\nwant %#v", check.name, check.got, check.want)
		}
	}
}

func TestBuildProductWorkflowBriefBlocksUngroundedImplementationClaims(t *testing.T) {
	brief := buildProductWorkflowBrief(
		filepath.Join(t.TempDir(), "effort"),
		productworkflow.WorkflowState{},
		productworkflow.PRDDocument{Requirements: []productworkflow.PRDRequirement{{
			ID:        "REQ-1",
			Statement: "Use the ExampleCloud platform for the live TUI document viewer.",
		}}},
		productworkflow.ADRIndex{},
		[]productworkflow.ParsedADR{{
			RelPath: "adrs/ADR-001.md",
			Doc: productworkflow.ADRDocument{
				FrontMatter:  productworkflow.ADRFrontMatter{ADRID: "ADR-001", Status: "accepted"},
				BodyMarkdown: "## Decision\nUse the ExampleSDK tooling for the document viewer implementation.",
			},
		}},
		productworkflow.TDDLedger{EffortID: "effort-1", TaskID: "task-1"},
		productworkflow.TraceabilityMatrix{},
		nil,
	)

	joined := strings.Join(brief.Blockers, "\n")
	for _, want := range []string{
		"implementation-affecting claim in PRD requirement REQ-1 lacks evidence_refs",
		"implementation-affecting claim in accepted ADR ADR-001 lacks evidence_refs",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected blocker %q, got %#v", want, brief.Blockers)
		}
	}
}

func TestBuildProductWorkflowBriefAllowsResolvedImplementationEvidence(t *testing.T) {
	brief := buildProductWorkflowBrief(
		filepath.Join(t.TempDir(), "effort"),
		productworkflow.WorkflowState{TaskID: "task-1"},
		productworkflow.PRDDocument{
			FrontMatter: productworkflow.PRDFrontMatter{Status: "accepted"},
			Requirements: []productworkflow.PRDRequirement{{
				ID:             "REQ-1",
				Statement:      "Use the ExampleCloud platform for the live TUI document viewer.",
				EvidenceRefs:   []string{"EV-1"},
				TDDObligations: []string{"TDD-1"},
			}},
		},
		productworkflow.ADRIndex{ADRs: []productworkflow.ADRIndexEntry{{
			ADRID:  "ADR-001",
			Status: "accepted",
			Path:   "ADR-001.md",
		}}},
		[]productworkflow.ParsedADR{{
			RelPath: "adrs/ADR-001.md",
			Doc: productworkflow.ADRDocument{
				FrontMatter: productworkflow.ADRFrontMatter{
					ADRID:          "ADR-001",
					Status:         "accepted",
					EvidenceRefs:   []string{"EV-1"},
					TDDObligations: []string{"TDD-1"},
				},
				BodyMarkdown: "## Decision\nUse the ExampleSDK tooling for the document viewer implementation.\n\n## Open Questions\nNone.",
			},
		}},
		productworkflow.TDDLedger{
			EffortID: "effort-1",
			TaskID:   "task-1",
			Obligations: []productworkflow.TDDObligation{{
				ID:             "TDD-1",
				Command:        "go test ./internal/surfaces/tui/app",
				RequiredBefore: "implementation",
				Status:         "planned",
			}},
		},
		productworkflow.TraceabilityMatrix{
			Matrix: []productworkflow.TraceabilityRow{{
				EvidenceCard:   "EV-1",
				PRDRequirement: "REQ-1",
				ADR:            "ADR-001",
				TDDObligation:  "TDD-1",
			}},
		},
		[]productworkflow.ImplementationLedgerRecord{{
			RecordType: "atom_planned",
			TaskID:     "task-1",
			Scope:      &productworkflow.AtomScope{ExpectedFiles: []string{"internal/surfaces/tui/app/feature_planner_product.go"}},
		}, {
			RecordType:              "verification_completed",
			TaskID:                  "task-1",
			EffortID:                "effort-1",
			TDDObligationsSatisfied: []string{"TDD-1"},
			VerificationStatus:      "pass",
		}},
	)

	if len(brief.Blockers) != 0 {
		t.Fatalf("resolved implementation evidence should not block TUI brief dispatch, got %#v", brief.Blockers)
	}
	if !brief.CoverageGatePassed {
		t.Fatalf("resolved implementation evidence should pass TUI coverage gate, blockers=%#v kinds=%#v", brief.CoverageBlockers, brief.CoverageBlockerKinds)
	}
}

func TestBuildProductWorkflowBriefUsesVerificationRecordsForCoverageGate(t *testing.T) {
	brief := buildProductWorkflowBrief(
		filepath.Join(t.TempDir(), "effort"),
		productworkflow.WorkflowState{TaskID: "task-verified"},
		productworkflow.PRDDocument{
			FrontMatter: productworkflow.PRDFrontMatter{Status: "accepted"},
			Requirements: []productworkflow.PRDRequirement{{
				ID:             "REQ-VERIFIED",
				Statement:      "Use satisfied TDD verification evidence in TUI product brief coverage.",
				EvidenceRefs:   []string{"EV-VERIFIED"},
				TDDObligations: []string{"TDD-VERIFIED"},
			}},
		},
		productworkflow.ADRIndex{ADRs: []productworkflow.ADRIndexEntry{{
			ADRID:  "ADR-VERIFIED",
			Status: "accepted",
			Path:   "verified.md",
		}}},
		[]productworkflow.ParsedADR{{
			RelPath: "adrs/verified.md",
			Doc: productworkflow.ADRDocument{
				FrontMatter: productworkflow.ADRFrontMatter{
					ADRID:          "ADR-VERIFIED",
					Status:         "accepted",
					EvidenceRefs:   []string{"EV-VERIFIED"},
					TDDObligations: []string{"TDD-VERIFIED"},
				},
				BodyMarkdown: "## Decision\nUse verification records as coverage evidence.\n\n## Open Questions\nNone.",
			},
		}},
		productworkflow.TDDLedger{
			EffortID:     "effort-verified",
			TaskID:       "task-verified",
			CoverageTier: "unit",
			Obligations: []productworkflow.TDDObligation{{
				ID:             "TDD-VERIFIED",
				Command:        "go test ./internal/surfaces/tui/app",
				RequiredBefore: "implementation",
				Status:         "planned",
			}},
		},
		productworkflow.TraceabilityMatrix{Matrix: []productworkflow.TraceabilityRow{{
			EvidenceCard:   "EV-VERIFIED",
			PRDRequirement: "REQ-VERIFIED",
			ADR:            "ADR-VERIFIED",
			TDDObligation:  "TDD-VERIFIED",
		}}},
		[]productworkflow.ImplementationLedgerRecord{{
			RecordType: "atom_planned",
			TaskID:     "task-verified",
			Scope:      &productworkflow.AtomScope{ExpectedFiles: []string{"internal/surfaces/tui/app/feature_planner_product.go"}},
		}, {
			RecordType:              "verification_completed",
			TaskID:                  "task-verified",
			EffortID:                "effort-verified",
			TDDObligationsSatisfied: []string{"TDD-VERIFIED"},
			VerificationStatus:      "pass",
			VerificationCommandsRun: []string{"go test ./internal/surfaces/tui/app"},
		}},
	)

	if !brief.CoverageGatePassed {
		t.Fatalf("expected TUI coverage gate to use verification_completed records, blockers=%#v kinds=%#v", brief.CoverageBlockers, brief.CoverageBlockerKinds)
	}
	if slices.Contains(brief.CoverageBlockerKinds, "coverage_tdd_evidence_missing") {
		t.Fatalf("verified TDD evidence should clear coverage_tdd_evidence_missing, got %#v", brief.CoverageBlockerKinds)
	}
	if !brief.AllowsFeaturePlanDispatch() {
		t.Fatalf("verified product workflow brief should allow dispatch, blockers=%#v coverage=%#v", brief.Blockers, brief.CoverageBlockers)
	}
}

func TestInferProductWorkflowFilesToModifyFromADRBody(t *testing.T) {
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

		if !found {
			t.Fatalf("missing inferred path %q from %#v", want, got)
		}
	}
}

func TestMergeADRIndex(t *testing.T) {
	makeDoc := func(id string) productworkflow.ADRDocument {
		return productworkflow.ADRDocument{
			FrontMatter: productworkflow.ADRFrontMatter{ADRID: id, Status: "draft"},
		}
	}
	makeEntry := func(id, path string) productworkflow.ADRIndexEntry {
		return productworkflow.ADRIndexEntry{ADRID: id, Path: path, Status: "draft"}
	}

	t.Run("all new entries", func(t *testing.T) {
		existing := productworkflow.ADRIndex{}
		newDocs := []productworkflow.ADRDocument{makeDoc("ADR-001"), makeDoc("ADR-002")}
		newEntries := []productworkflow.ADRIndexEntry{makeEntry("ADR-001", "ADR-001.md"), makeEntry("ADR-002", "ADR-002.md")}

		docs, entries := mergeADRIndex(existing, newDocs, newEntries)
		if len(docs) != 2 || len(entries) != 2 {
			t.Fatalf("expected 2 each, got %d docs, %d entries", len(docs), len(entries))
		}
	})

	t.Run("replaces existing entries with same ID", func(t *testing.T) {
		existing := productworkflow.ADRIndex{
			ADRs: []productworkflow.ADRIndexEntry{
				{ADRID: "ADR-001", Path: "ADR-001.md", Status: "draft"},
			},
		}
		newDocs := []productworkflow.ADRDocument{makeDoc("ADR-001")}

		newDocs[0].FrontMatter.Status = "updated"

		newEntries := []productworkflow.ADRIndexEntry{makeEntry("ADR-001", "ADR-001.md")}

		newEntries[0].Status = "updated"

		docs, entries := mergeADRIndex(existing, newDocs, newEntries)
		if len(entries) != 1 {
			t.Fatalf("expected 1, got %d", len(entries))
		}

		if entries[0].Status != "updated" {
			t.Errorf("expected status updated, got %s", entries[0].Status)
		}

		_ = docs
	})

	t.Run("appends new entries to existing", func(t *testing.T) {
		existing := productworkflow.ADRIndex{
			ADRs: []productworkflow.ADRIndexEntry{
				{ADRID: "ADR-001", Path: "ADR-001.md", Status: "draft"},
			},
		}
		newDocs := []productworkflow.ADRDocument{makeDoc("ADR-002")}
		newEntries := []productworkflow.ADRIndexEntry{makeEntry("ADR-002", "ADR-002.md")}

		_, entries := mergeADRIndex(existing, newDocs, newEntries)
		if len(entries) != 2 {
			t.Fatalf("expected 2, got %d", len(entries))
		}

		found := map[string]bool{}
		for _, e := range entries {
			found[e.ADRID] = true
		}

		if !found["ADR-001"] || !found["ADR-002"] {
			t.Errorf("expected both ADR-001 and ADR-002, got %v", found)
		}
	})
}

func TestBuildPRDGenerationPrompt(t *testing.T) {
	prompt := buildPRDGenerationPrompt("build user authentication", "/tmp/effort")
	if !strings.Contains(prompt, "build user authentication") {
		t.Error("prompt should contain feature description")
	}

	if !strings.Contains(prompt, "idiom.prd.v1") {
		t.Error("prompt should contain schema version")
	}

	if !strings.Contains(prompt, "**id**: REQ-001") {
		t.Error("prompt should contain example requirement format")
	}

	if !strings.Contains(prompt, "open_questions") {
		t.Error("prompt should contain open_questions field")
	}
}

func TestBuildADRGenerationPrompt(t *testing.T) {
	prompt := buildADRGenerationPrompt("PRD content here", "/tmp/effort")
	if !strings.Contains(prompt, "PRD content here") {
		t.Error("prompt should contain PRD content")
	}

	if !strings.Contains(prompt, "===") {
		t.Error("prompt should mention === separator")
	}

	if !strings.Contains(prompt, "## Context") {
		t.Error("prompt should list required sections")
	}

	if !strings.Contains(prompt, "idiom.adr.v1") {
		t.Error("prompt should contain schema version")
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "b", "c"); got != "b" {
		t.Errorf("expected b, got %s", got)
	}

	if got := firstNonEmpty("a", "b"); got != "a" {
		t.Errorf("expected a, got %s", got)
	}

	if got := firstNonEmpty("", "", ""); got != "" {
		t.Errorf("expected empty, got %s", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := util.Truncate("hello", 10); got != "hello" {
		t.Errorf("expected hello, got %s", got)
	}

	if got := util.Truncate("hello world", 5); got != "he..." {
		t.Errorf("expected he..., got %s", got)
	}

	if got := util.Truncate("hello world", 11); got != "hello world" {
		t.Errorf("expected hello world, got %s", got)
	}
}
