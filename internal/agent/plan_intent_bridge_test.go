package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/config"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/planintent"
	"idiom/internal/workspace"
)

func TestCompilePlanIntentToolRequestRoutesThroughSubmitPlanShape(t *testing.T) {
	root := t.TempDir()
	writePlanIntentFixture(t, root, "internal/a.go", "package internal\n\nfunc label() string {\n\treturn \"old\"\n}\n")
	artifact := planintent.Artifact{
		SchemaVersion: planintent.SchemaVersion,
		Source:        planintent.SourcePlanner,
		TaskSummary:   "replace label",
		Scope:         planintent.Scope{RequiredEditPaths: []string{"internal/a.go"}},
		Intents: []planintent.Intent{{
			Path:      "internal/a.go",
			Operation: planintent.OperationReplaceExactText,
			OldText:   "return \"old\"",
			NewText:   "return \"next\"",
			Purpose:   "replace label",
		}},
	}
	body := marshalPlanIntentArtifact(t, artifact)
	var events []RunEvent
	runner := &Runner{
		Workspace:    workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: planIntentPlannerBrief("internal/a.go"),
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	req, raw, ok := runner.compilePlanIntentToolRequest(body)

	if !ok {
		t.Fatal("expected edit_intent.v1 artifact to compile")
	}
	if req.Tool != "submit_plan" || len(req.Edits) != 1 || req.Edits[0].Path != "internal/a.go" {
		t.Fatalf("unexpected compiled request: %#v", req)
	}
	if !strings.Contains(raw, `"tool":"submit_plan"`) {
		t.Fatalf("raw compiled request should be parser-readable submit_plan JSON: %s", raw)
	}
	if !runEventsContainRole(events, "edit_intent_validated") || !runEventsContainRole(events, "patch_compile_started") || !runEventsContainRole(events, "patch_compile_result") {
		t.Fatalf("expected bounded plan-intent stage events, got %#v", events)
	}
	if !runEventsContainContent(events, `"compile_outcome":"accepted"`) {
		t.Fatalf("expected compile_outcome event, got %#v", events)
	}
}

func TestCompilePlanIntentToolRequestAcceptsExactFencedArtifact(t *testing.T) {
	root := t.TempDir()
	writePlanIntentFixture(t, root, "internal/a.go", "one\ntwo\n")
	artifact := planintent.Artifact{
		SchemaVersion: planintent.SchemaVersion,
		Source:        planintent.SourcePlanner,
		Scope:         planintent.Scope{RequiredEditPaths: []string{"internal/a.go"}},
		Intents: []planintent.Intent{{
			Path:       "internal/a.go",
			Operation:  planintent.OperationInsertAfterLineAnchor,
			LineAnchor: "one",
			Content:    "middle\n",
			Purpose:    "insert middle",
		}},
	}
	runner := &Runner{
		Workspace:    workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: planIntentPlannerBrief("internal/a.go"),
	}

	req, _, ok := runner.compilePlanIntentToolRequest("```json\n" + marshalPlanIntentArtifact(t, artifact) + "\n```")

	if !ok || req.Tool != "submit_plan" || len(req.Edits) != 1 {
		t.Fatalf("expected exact fenced edit intent artifact to compile, ok=%v req=%#v", ok, req)
	}
}

func TestCompilePlanIntentToolRequestRejectsScopeWidening(t *testing.T) {
	root := t.TempDir()
	writePlanIntentFixture(t, root, "internal/a.go", "package internal\n")
	artifact := planintent.Artifact{
		SchemaVersion: planintent.SchemaVersion,
		Source:        planintent.SourcePlanner,
		Scope:         planintent.Scope{RequiredEditPaths: []string{"internal/other.go"}},
		Intents: []planintent.Intent{{
			Path:      "internal/other.go",
			Operation: planintent.OperationReplaceExactText,
			OldText:   "old",
			NewText:   "new",
		}},
	}
	var events []RunEvent
	runner := &Runner{
		Workspace:    workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: planIntentPlannerBrief("internal/a.go"),
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	req, raw, ok := runner.compilePlanIntentToolRequest(marshalPlanIntentArtifact(t, artifact))

	if ok || req.Tool != "" || raw != "" {
		t.Fatalf("scope-widening edit intent must not compile: ok=%v req=%#v raw=%q", ok, req, raw)
	}
	if !runEventsContainContent(events, "required_scope_mismatch") {
		t.Fatalf("expected scope mismatch event, got %#v", events)
	}
}

func TestCompilePlanIntentToolRequestReportsMalformedIntentJSON(t *testing.T) {
	var events []RunEvent
	runner := &Runner{
		RuntimeBrief: planIntentPlannerBrief("internal/a.go"),
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	req, raw, ok := runner.compilePlanIntentToolRequest(`{"schema_version":"idiom.edit_intent.v1",`)

	if ok || req.Tool != "" || raw != "" {
		t.Fatalf("malformed edit intent JSON must not compile: ok=%v req=%#v raw=%q", ok, req, raw)
	}
	if !runEventsContainContent(events, "malformed_json") {
		t.Fatalf("expected malformed_json event, got %#v", events)
	}
}

func TestCompilePlanIntentToolRequestDoesNotRouteTypedBlockerToSubmitPlan(t *testing.T) {
	root := t.TempDir()
	artifact := planintent.Artifact{
		SchemaVersion: planintent.SchemaVersion,
		Source:        planintent.SourcePlanner,
		Scope:         planintent.Scope{RequiredEditPaths: []string{"internal/a.go"}},
		Blockers: []planintent.Blocker{{
			Code:         planintent.CodeIntentBlocker,
			Message:      "required source evidence is unavailable",
			Paths:        []string{"internal/a.go"},
			EvidenceRefs: []string{"conversation:parser_failure"},
		}},
	}
	var events []RunEvent
	runner := &Runner{
		Workspace:    workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: planIntentPlannerBrief("internal/a.go"),
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	req, raw, ok := runner.compilePlanIntentToolRequest(marshalPlanIntentArtifact(t, artifact))

	if ok || req.Tool != "" || raw != "" {
		t.Fatalf("typed blocker must not compile into submit_plan: ok=%v req=%#v raw=%q", ok, req, raw)
	}
	if !runEventsContainContent(events, planintent.CodeIntentBlocker) {
		t.Fatalf("expected typed blocker compile event, got %#v", events)
	}
}

func TestCompilePlanIntentToolRequestDoesNotCompileOutsidePlannerSubmitPlanSurface(t *testing.T) {
	artifact := planintent.Artifact{
		SchemaVersion: planintent.SchemaVersion,
		Source:        planintent.SourceArtifactDistiller,
		Scope:         planintent.Scope{RequiredEditPaths: []string{"internal/a.go"}},
		Intents: []planintent.Intent{{
			Path:      "internal/a.go",
			Operation: planintent.OperationReplaceExactText,
			OldText:   "old",
			NewText:   "new",
		}},
	}
	var events []RunEvent
	runner := &Runner{
		RuntimeBrief: workerruntime.RuntimeBriefForTaskFamily(workerruntime.ArtifactDistillerTaskFamily, "main", []string{"submit_plan"}),
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	req, raw, ok := runner.compilePlanIntentToolRequest(marshalPlanIntentArtifact(t, artifact))

	if ok || req.Tool != "" || raw != "" {
		t.Fatalf("artifact distiller output must remain a non-executable artifact: ok=%v req=%#v raw=%q", ok, req, raw)
	}
	if len(events) != 0 {
		t.Fatalf("non-planner bridge should not emit compile events, got %#v", events)
	}
}

func planIntentPlannerBrief(paths ...string) workerruntime.WorkerRuntimeBrief {
	return workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: paths,
		AllowedTools:      []string{"submit_plan"},
		RequiredTools:     []string{"submit_plan"},
	}
}

func marshalPlanIntentArtifact(t *testing.T, artifact planintent.Artifact) string {
	t.Helper()
	body, err := json.Marshal(artifact)
	if err != nil {
		t.Fatalf("marshal artifact: %v", err)
	}
	return string(body)
}

func writePlanIntentFixture(t *testing.T, root string, path string, content string) {
	t.Helper()
	fullPath := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir fixture parent: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func runEventsContainRole(events []RunEvent, role string) bool {
	for _, event := range events {
		if event.Role == role {
			return true
		}
	}
	return false
}

func runEventsContainContent(events []RunEvent, text string) bool {
	for _, event := range events {
		if strings.Contains(event.Content, text) {
			return true
		}
	}
	return false
}
