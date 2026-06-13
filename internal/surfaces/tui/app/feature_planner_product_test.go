package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/workerruntime"
	"idiom/internal/workflows/featureplanner"
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
	if !strings.Contains(answer, "feature-planner: "+featureplanner.FeatureStatePlanningQuestion) {
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
	)

	if !slices.Contains(brief.ContextEvidencePaths, "internal/surfaces/tui/app/product_status.go") {
		t.Fatalf("expected workflow referenced path in ContextEvidencePaths, got %#v", brief.ContextEvidencePaths)
	}
}

func TestBuildProductWorkflowBriefBlocksUngroundedImplementationClaims(t *testing.T) {
	brief := buildProductWorkflowBrief(
		"docs/product-workflows/efforts/example",
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
		"docs/product-workflows/efforts/example",
		productworkflow.WorkflowState{},
		productworkflow.PRDDocument{Requirements: []productworkflow.PRDRequirement{{
			ID:           "REQ-1",
			Statement:    "Use the ExampleCloud platform for the live TUI document viewer.",
			EvidenceRefs: []string{"EV-1"},
		}}},
		productworkflow.ADRIndex{},
		[]productworkflow.ParsedADR{{
			RelPath: "adrs/ADR-001.md",
			Doc: productworkflow.ADRDocument{
				FrontMatter:  productworkflow.ADRFrontMatter{ADRID: "ADR-001", Status: "accepted", EvidenceRefs: []string{"EV-1"}},
				BodyMarkdown: "## Decision\nUse the ExampleSDK tooling for the document viewer implementation.\n\n## Open Questions\nNone.",
			},
		}},
		productworkflow.TDDLedger{EffortID: "effort-1", TaskID: "task-1"},
		productworkflow.TraceabilityMatrix{
			Matrix: []productworkflow.TraceabilityRow{{EvidenceCard: "EV-1"}},
		},
	)

	if len(brief.Blockers) != 0 {
		t.Fatalf("resolved implementation evidence should not block TUI brief dispatch, got %#v", brief.Blockers)
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
