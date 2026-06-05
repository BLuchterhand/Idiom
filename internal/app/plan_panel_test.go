package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"idiom/internal/config"
	"idiom/internal/workspace"
)

func TestLoadPlanContentUsesCacheUntilFileChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(path, []byte("first body"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	m := &Model{planPath: path}
	if got := m.loadPlanContent(); got != "first body" {
		t.Fatalf("first load = %q, want %q", got, "first body")
	}

	m.planContent = "cached body"
	if got := m.loadPlanContent(); got != "cached body" {
		t.Fatalf("expected unchanged plan to reuse cache, got %q", got)
	}

	if err := os.WriteFile(path, []byte("second body"), 0o644); err != nil {
		t.Fatalf("rewrite plan: %v", err)
	}
	modTime := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if got := m.loadPlanContent(); got != "second body" {
		t.Fatalf("expected changed plan to reload from disk, got %q", got)
	}
}

func TestLoadPlanContentClearsCacheWhenFileDisappears(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(path, []byte("body"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	m := &Model{planPath: path}
	if got := m.loadPlanContent(); got != "body" {
		t.Fatalf("loadPlanContent = %q, want %q", got, "body")
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got := m.loadPlanContent(); got != "" {
		t.Fatalf("expected missing plan to return empty string, got %q", got)
	}
	if m.planContent != "" {
		t.Fatalf("expected missing plan to clear cached content, got %q", m.planContent)
	}
	if !m.planMTime.IsZero() {
		t.Fatalf("expected missing plan to clear cached mtime, got %v", m.planMTime)
	}
}

func TestDocViewerRendersPlanArtifactMetadataAndCollapsedEdits(t *testing.T) {
	root := t.TempDir()
	writeDocViewerPlanArtifact(t, root)

	m := docViewerTestModel(root)
	m.renderDocViewerPanelContent()

	got := m.docViewer.viewport.View()
	for _, want := range []string{
		"Doc Viewer",
		"Plan: plan_test",
		"Schema: " + workspace.PlanArtifactSchemaVersion,
		"Contract: plan_then_execute",
		"Approval: implicit",
		"▸ Edit 1",
		"old_text",
		"app.txt",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("doc viewer missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "-old value") || strings.Contains(got, "+new value") {
		t.Fatalf("collapsed edit should not render diff body:\n%s", got)
	}
}

func TestDocViewerExpandedEditRendersDiffLikeBody(t *testing.T) {
	root := t.TempDir()
	writeDocViewerPlanArtifact(t, root)

	m := docViewerTestModel(root)
	m.docViewer.expanded = map[int]bool{0: true}
	m.renderDocViewerPanelContent()

	got := m.docViewer.viewport.View()
	for _, want := range []string{
		"▾ Edit 1",
		"--- a/app.txt",
		"+++ b/app.txt",
		"@@ old_text @@",
		"-old value",
		"+new value",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expanded doc viewer missing %q:\n%s", want, got)
		}
	}
}

func TestDocViewerEnterTogglesSelectedEdit(t *testing.T) {
	root := t.TempDir()
	writeDocViewerPlanArtifact(t, root)

	m := docViewerTestModel(root)
	if handled, _ := m.handleDocViewerPanelKey(tea.KeyMsg{Type: tea.KeyEnter}); !handled {
		t.Fatal("Enter was not handled")
	}
	if !m.docViewer.expanded[0] {
		t.Fatal("expected selected edit to expand")
	}
	if !strings.Contains(m.docViewer.viewport.View(), "+new value") {
		t.Fatalf("expanded viewport did not render diff:\n%s", m.docViewer.viewport.View())
	}
}

func TestMouseClickPanelTabSwitchesAndFocusesPanel(t *testing.T) {
	m := docViewerTestModel(t.TempDir())
	m.panelTab = panelTabHistory
	m.viewport.Width = 60

	docViewerTabX := lipgloss.Width(" History ") + lipgloss.Width(" Plan ") + 1
	m.handleMouse(tea.MouseMsg{
		X:      m.panelStartX() + docViewerTabX,
		Y:      2,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})

	if m.panelTab != panelTabDocViewer {
		t.Fatalf("panel tab = %d, want doc viewer", m.panelTab)
	}
	if !m.history.focused {
		t.Fatal("panel click should focus the panel")
	}
}

func TestMouseClickDocViewerEditTogglesExpansion(t *testing.T) {
	root := t.TempDir()
	writeDocViewerPlanArtifact(t, root)

	m := docViewerTestModel(root)
	m.viewport.Width = 60
	m.renderDocViewerPanelContent()

	row := -1
	for i, line := range strings.Split(m.docViewer.viewport.View(), "\n") {
		if strings.Contains(line, "Edit 1") {
			row = i
			break
		}
	}
	if row < 0 {
		t.Fatalf("could not find doc viewer edit row:\n%s", m.docViewer.viewport.View())
	}

	m.handleMouse(tea.MouseMsg{
		X:      m.panelStartX() + 1,
		Y:      3 + row,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})

	if !m.docViewer.expanded[0] {
		t.Fatal("mouse click should expand the clicked edit")
	}
	if !strings.Contains(m.docViewer.viewport.View(), "+new value") {
		t.Fatalf("expanded viewport did not render diff:\n%s", m.docViewer.viewport.View())
	}
}

func docViewerTestModel(root string) *Model {
	return &Model{
		cfg: config.Config{Workspace: config.Workspace{Root: root, CWD: root}},
		workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		panelTab: panelTabDocViewer,
		history: historyState{
			panelOpen: true,
			focused:   true,
		},
		docViewer: docViewerState{
			viewport: viewport.New(80, 20),
			expanded: make(map[int]bool),
		},
	}
}

func writeDocViewerPlanArtifact(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "app.txt"), []byte("old value\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	planDir := filepath.Join(root, ".idiom", "plans")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatalf("mkdir plan dir: %v", err)
	}
	artifact := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		PlanID:        "plan_test",
		Created:       "2026-06-05T17:00:00Z",
		Rationale:     "Update the visible value.",
		SessionID:     "session-test",
		Task: workspace.PlanArtifactTask{
			Summary: "doc viewer fixture",
		},
		Contract: workspace.PlanArtifactContract{
			Kind:              "plan_then_execute",
			StrategyOwner:     "model",
			ContractOwner:     "harness",
			PermissionProfile: "plan_then_execute",
			VerificationOwner: "planner",
		},
		Packet: workspace.PlanArtifactPacket{
			Status: "not_provided",
		},
		Paths: workspace.PlanArtifactPaths{
			Editable: []string{"app.txt"},
			Changed:  []string{"app.txt"},
		},
		Evidence: []workspace.PlanArtifactEvidence{{
			ID:      "evidence-1",
			Kind:    "source_body",
			Path:    "app.txt",
			Summary: "read app.txt",
		}},
		VerificationObligations: []workspace.PlanArtifactVerificationObligation{{
			ID:       "verification-1",
			Kind:     "executor_lane_sanity",
			Owner:    "planner",
			Status:   "open",
			Required: true,
		}},
		Approval: workspace.PlanArtifactApproval{
			Required:            false,
			Status:              "implicit",
			ScopeID:             "plan_test:submit_plan",
			Mode:                "plan_then_execute",
			ExpirationPolicy:    "workspace_fingerprint",
			ExecutionPermission: "execute_all",
		},
		Handoff: workspace.PlanArtifactHandoff{
			ID:     "plan_test:handoff",
			From:   "planner",
			To:     "plan_execute_executor",
			Status: "ready",
			Ready:  true,
		},
		Edits: []workspace.PlanEdit{{
			Path:    "app.txt",
			OldText: "old value",
			NewText: "new value",
			Purpose: "replace value",
		}},
	}
	body, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatalf("marshal artifact: %v", err)
	}
	if err := os.WriteFile(filepath.Join(planDir, "last.plan.json"), body, 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
}
