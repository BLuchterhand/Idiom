package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	planartifact "idiom/internal/contracts/planartifact"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
)

var _ = ginkgo.It("load plan content uses cache until file changes", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")

	{
		err := os.WriteFile(path, []byte("first body"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write plan: %v", err))
	}

	m := &Model{promptRuntimeState: promptRuntimeState{
		promptRuntimeCompletionState: promptRuntimeCompletionState{planPath: path},
	}}
	{
		got := m.loadPlanContent()
		gomega.Expect(got).To(gomega.Equal("first body"), fmt.Sprintf("first load = %q, want %q", got, "first body"))
	}

	m.planContent = "cached body"
	{
		got := m.loadPlanContent()
		gomega.Expect(got).To(gomega.Equal("cached body"), fmt.Sprintf("expected unchanged plan to reuse cache, got %q", got))
	}

	{
		err := os.WriteFile(path, []byte("second body"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("rewrite plan: %v", err))
	}

	modTime := time.Now().Add(2 * time.Second)
	{
		err := os.Chtimes(path, modTime, modTime)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("chtimes: %v", err))
	}

	{
		got := m.loadPlanContent()
		gomega.Expect(got).To(gomega.Equal("second body"), fmt.Sprintf("expected changed plan to reload from disk, got %q", got))
	}
})

var _ = ginkgo.It("load plan content clears cache when file disappears", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")

	{
		err := os.WriteFile(path, []byte("body"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write plan: %v", err))
	}

	m := &Model{promptRuntimeState: promptRuntimeState{
		promptRuntimeCompletionState: promptRuntimeCompletionState{planPath: path},
	}}
	{
		got := m.loadPlanContent()
		gomega.Expect(got).To(gomega.Equal("body"), fmt.Sprintf("loadPlanContent = %q, want %q", got, "body"))
	}

	{
		err := os.Remove(path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("remove: %v", err))
	}

	{
		got := m.loadPlanContent()
		gomega.Expect(got).To(gomega.Equal(""), fmt.Sprintf("expected missing plan to return empty string, got %q", got))
	}

	gomega.Expect(m.planContent).To(gomega.Equal(""), fmt.Sprintf("expected missing plan to clear cached content, got %q", m.planContent))

	gomega.Expect(m.planMTime.IsZero()).To(gomega.BeTrue(), fmt.Sprintf("expected missing plan to clear cached mtime, got %v", m.planMTime))
})

var _ = ginkgo.It("plan panel headings collapse with enter", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	path := filepath.Join(root, "plan.md")
	content := "# Plan\n\n## Objective\nkeep this readable\n- [ ] hidden item\n\n## Steps\n- [ ] visible item\n"
	{
		err := os.WriteFile(path, []byte(content), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write plan: %v", err))
	}

	m := planPanelTestModel(path)
	m.renderPlanPanelContent()

	before := m.planViewport.View()
	for _, want := range []string{"▾ Objective", "keep this readable", "hidden item", "▾ Steps"} {
		gomega.Expect(strings.Contains(before, want)).To(gomega.BeTrue(), fmt.Sprintf("expanded plan missing %q:\n%s", want, before))
	}

	rows := m.currentPlanSelectableRows()
	objectiveIdx := planSelectableRowIndex(rows, "Objective")
	gomega.Expect(objectiveIdx < 0).To(gomega.BeFalse(), fmt.Sprintf("Objective heading not selectable: %#v", rows))

	m.planUI.cursor = objectiveIdx
	{
		handled, _ := m.handlePlanPanelKey(tea.KeyMsg{Type: tea.KeyEnter})
		gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("Enter should be handled for plan heading"))
	}

	after := m.planViewport.View()
	for _, want := range []string{"▸ Objective", "▾ Steps", "visible item"} {
		gomega.Expect(strings.Contains(after, want)).To(gomega.BeTrue(), fmt.Sprintf("collapsed plan missing %q:\n%s", want, after))
	}
	for _, hidden := range []string{"keep this readable", "hidden item"} {
		gomega.Expect(strings.Contains(after, hidden)).To(gomega.BeFalse(), fmt.Sprintf("collapsed section leaked %q:\n%s", hidden, after))
	}
})

var _ = ginkgo.It("plan panel collapse hides nested headings only until peer", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	path := filepath.Join(root, "plan.md")
	content := "## Parent\nparent body\n### Child\nchild body\n## Peer\npeer body\n"
	{
		err := os.WriteFile(path, []byte(content), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write plan: %v", err))
	}

	m := planPanelTestModel(path)
	rows := m.currentPlanSelectableRows()
	parentIdx := planSelectableRowIndex(rows, "Parent")
	gomega.Expect(parentIdx < 0).To(gomega.BeFalse(), fmt.Sprintf("Parent heading not selectable: %#v", rows))

	m.planUI.cursor = parentIdx
	m.activateSelectedPlanRow()

	got := m.planViewport.View()
	for _, hidden := range []string{"parent body", "Child", "child body"} {
		gomega.Expect(strings.Contains(got, hidden)).To(gomega.BeFalse(), fmt.Sprintf("collapsed parent leaked nested content %q:\n%s", hidden, got))
	}
	for _, want := range []string{"▸ Parent", "▾ Peer", "peer body"} {
		gomega.Expect(strings.Contains(got, want)).To(gomega.BeTrue(), fmt.Sprintf("collapsed parent should leave peer visible; missing %q:\n%s", want, got))
	}
})

var _ = ginkgo.It("plan panel click toggles heading using rendered rows", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	path := filepath.Join(root, "plan.md")
	content := "# Plan\n\n## Objective\nbody\n\n## Steps\n- [ ] visible item\n"
	{
		err := os.WriteFile(path, []byte(content), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write plan: %v", err))
	}

	m := planPanelTestModel(path)
	m.renderPlanPanelContent()

	clickRow := -1
	for i, line := range strings.Split(m.planViewport.View(), "\n") {
		if strings.Contains(line, "Objective") {
			clickRow = i
			break
		}
	}
	gomega.Expect(clickRow < 0).To(gomega.BeFalse(), fmt.Sprintf("Objective row not rendered:\n%s", m.planViewport.View()))

	gomega.Expect(m.handlePlanPanelClick(clickRow)).To(gomega.BeTrue(), fmt.Sprint("heading click should be handled"))

	got := m.planViewport.View()
	gomega.Expect(got).To(gomega.ContainSubstring("▸ Objective"), fmt.Sprintf("heading click did not collapse section:\n%s", got))
	gomega.Expect(got).To(gomega.Not(gomega.ContainSubstring("body")), fmt.Sprintf("heading click did not collapse section:\n%s", got))
	gomega.Expect(strings.Contains(got, "visible item")).To(gomega.BeTrue(), fmt.Sprintf("peer content should remain visible after heading click:\n%s", got))
})

var _ = ginkgo.It("plan panel click uses rendered row metadata when rows wrap", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	path := filepath.Join(root, "plan.md")
	content := "# Plan\n\n## Objective\nbody\n\n## Steps\n- [ ] visible item\n"
	{
		err := os.WriteFile(path, []byte(content), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write plan: %v", err))
	}

	m := planPanelTestModel(path)
	m.renderPlanPanelContent()

	stepsSourceLine := -1
	for _, row := range m.currentPlanSelectableRows() {
		if row.Kind == planSelectableHeading && row.Heading == "Steps" {
			stepsSourceLine = row.LineIdx
			break
		}
	}
	gomega.Expect(stepsSourceLine < 0).To(gomega.BeFalse(), fmt.Sprintf("Steps heading not selectable:\n%s", m.planViewport.View()))

	// Simulate a rendered plan where earlier long rows occupy extra viewport
	// rows. Click dispatch must use render-owned geometry instead of assuming
	// source lines and visible rows have the same index.
	m.planUI.renderedRows = []planRenderedRow{
		{SourceLineIdx: -1},
		{SourceLineIdx: 0},
		{SourceLineIdx: 0},
		{SourceLineIdx: 0},
		{SourceLineIdx: 2},
		{SourceLineIdx: 3},
		{SourceLineIdx: 4},
		{SourceLineIdx: stepsSourceLine},
	}

	gomega.Expect(m.handlePlanPanelClick(7)).To(gomega.BeTrue(), fmt.Sprint("heading click should be handled through rendered row metadata"))

	got := m.planViewport.View()
	gomega.Expect(got).To(gomega.ContainSubstring("▸ Steps"), fmt.Sprintf("metadata-routed heading click did not collapse Steps:\n%s", got))
	gomega.Expect(got).To(gomega.Not(gomega.ContainSubstring("visible item")), fmt.Sprintf("metadata-routed heading click did not collapse Steps:\n%s", got))
})

var _ = ginkgo.It("plan panel mouse click toggles heading through panel hit test", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	path := filepath.Join(root, "plan.md")
	content := "# Plan\n\n## Objective\nbody\n\n## Steps\n- [ ] visible item\n"
	{
		err := os.WriteFile(path, []byte(content), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write plan: %v", err))
	}

	m := planPanelTestModel(path)
	m.viewport.Width = 100
	m.renderPlanPanelContent()

	clickRow := -1
	for i, line := range strings.Split(m.planViewport.View(), "\n") {
		if strings.Contains(line, "Objective") {
			clickRow = i
			break
		}
	}
	gomega.Expect(clickRow < 0).To(gomega.BeFalse(), fmt.Sprintf("Objective row not rendered:\n%s", m.planViewport.View()))

	_, handled := m.handleMouse(tea.MouseMsg{
		X:      m.panelStartX() + 1,
		Y:      3 + clickRow,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("panel mouse click should be handled"))

	got := m.planViewport.View()
	gomega.Expect(got).To(gomega.ContainSubstring("▸ Objective"), fmt.Sprintf("mouse click did not collapse section:\n%s", got))
	gomega.Expect(got).To(gomega.Not(gomega.ContainSubstring("body")), fmt.Sprintf("mouse click did not collapse section:\n%s", got))
})

var _ = ginkgo.It("plan panel mouse left type click toggles heading", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	path := filepath.Join(root, "plan.md")
	content := "# Plan\n\n## Objective\nbody\n\n## Steps\n- [ ] visible item\n"
	{
		err := os.WriteFile(path, []byte(content), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write plan: %v", err))
	}

	m := planPanelTestModel(path)
	m.viewport.Width = 100
	m.renderPlanPanelContent()

	clickRow := -1
	for i, line := range strings.Split(m.planViewport.View(), "\n") {
		if strings.Contains(line, "Objective") {
			clickRow = i
			break
		}
	}
	gomega.Expect(clickRow < 0).To(gomega.BeFalse(), fmt.Sprintf("Objective row not rendered:\n%s", m.planViewport.View()))

	_, handled := m.handleMouse(tea.MouseMsg{
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
		X:      m.panelStartX() + 1,
		Y:      3 + clickRow,
	})
	gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("MouseLeft-typed panel click should be handled"))

	got := m.planViewport.View()
	gomega.Expect(got).To(gomega.ContainSubstring("▸ Objective"), fmt.Sprintf("MouseLeft-typed click did not collapse section:\n%s", got))
	gomega.Expect(got).To(gomega.Not(gomega.ContainSubstring("body")), fmt.Sprintf("MouseLeft-typed click did not collapse section:\n%s", got))
})

var _ = ginkgo.It("primary mouse press recognizes supported encodings", func() {
	tests := []struct {
		name string
		msg  tea.MouseMsg
		want bool
	}{
		{
			name: "button action left press",
			msg: tea.MouseMsg{
				Button: tea.MouseButtonLeft,
				Action: tea.MouseActionPress,
			},
			want: true,
		},
		{
			name: "left release",
			msg: tea.MouseMsg{
				Button: tea.MouseButtonLeft,
				Action: tea.MouseActionRelease,
			},
			want: false,
		},
		{
			name: "wheel",
			msg: tea.MouseMsg{
				Button: tea.MouseButtonWheelUp,
				Action: tea.MouseActionPress,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		ginkgo.By(fmt.Sprint(tt.name), func() {
			{
				got := isPrimaryMousePress(tt.msg)
				gomega.Expect(got).To(gomega.Equal(tt.want), fmt.Sprintf("isPrimaryMousePress() = %t, want %t", got, tt.want))
			}
		})
	}
})

var _ = ginkgo.It("doc viewer renders plan artifact metadata and collapsed edits", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	writeDocViewerPlanArtifact(t, root)

	m := docViewerTestModel(root)
	m.renderDocViewerPanelContent()

	got := m.docViewer.viewport.View()
	for _, want := range []string{
		"Doc Viewer",
		"Plan: plan_test",
		"Schema: " + planartifact.SchemaVersion,
		"Contract: plan_then_execute",
		"Approval: implicit",
		"▸ Edit 1",
		"old_text",
		"app.txt",
	} {
		gomega.Expect(strings.Contains(got, want)).To(gomega.BeTrue(), fmt.Sprintf("doc viewer missing %q:\n%s", want, got))
	}

	gomega.Expect(got).To(gomega.Not(gomega.ContainSubstring("-old value")), fmt.Sprintf("collapsed edit should not render diff body:\n%s", got))
	gomega.Expect(got).To(gomega.Not(gomega.ContainSubstring("+new value")), fmt.Sprintf("collapsed edit should not render diff body:\n%s", got))
})

var _ = ginkgo.It("doc viewer expanded edit renders diff like body", func() {
	t := ginkgo.GinkgoT()
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
		gomega.Expect(strings.Contains(got, want)).To(gomega.BeTrue(), fmt.Sprintf("expanded doc viewer missing %q:\n%s", want, got))
	}
})

var _ = ginkgo.It("doc viewer enter toggles selected edit", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	writeDocViewerPlanArtifact(t, root)

	m := docViewerTestModel(root)
	{
		handled, _ := m.handleDocViewerPanelKey(tea.KeyMsg{Type: tea.KeyEnter})
		gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("Enter was not handled"))
	}

	gomega.Expect(m.docViewer.expanded[0]).To(gomega.BeTrue(), fmt.Sprint("expected selected edit to expand"))

	gomega.Expect(strings.Contains(m.docViewer.viewport.View(), "+new value")).To(gomega.BeTrue(), fmt.Sprintf("expanded viewport did not render diff:\n%s", m.docViewer.viewport.View()))
})

var _ = ginkgo.It("mouse click panel tab switches and focuses panel", func() {
	t := ginkgo.GinkgoT()
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

	gomega.Expect(m.panelTab).To(gomega.Equal(panelTabDocViewer), fmt.Sprintf("panel tab = %d, want doc viewer", m.panelTab))

	gomega.Expect(m.history.focused).To(gomega.BeTrue(), fmt.Sprint("panel click should focus the panel"))
})

var _ = ginkgo.It("mouse click doc viewer edit toggles expansion", func() {
	t := ginkgo.GinkgoT()
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

	gomega.Expect(row < 0).To(gomega.BeFalse(), fmt.Sprintf("could not find doc viewer edit row:\n%s", m.docViewer.viewport.View()))

	m.handleMouse(tea.MouseMsg{
		X:      m.panelStartX() + 1,
		Y:      3 + row,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})

	gomega.Expect(m.docViewer.expanded[0]).To(gomega.BeTrue(), fmt.Sprint("mouse click should expand the clicked edit"))

	gomega.Expect(strings.Contains(m.docViewer.viewport.View(), "+new value")).To(gomega.BeTrue(), fmt.Sprintf("expanded viewport did not render diff:\n%s", m.docViewer.viewport.View()))
})

func planPanelTestModel(path string) *Model {
	return &Model{
		promptRuntimeState: promptRuntimeState{
			promptRuntimeCompletionState: promptRuntimeCompletionState{planPath: path},
		},
		panelTab: panelTabPlan,
		history: historyState{
			panelOpen: true,
			focused:   true,
		},
		planViewport: viewport.New(80, 20),
	}
}

func planSelectableRowIndex(rows []planSelectableRow, heading string) int {
	for i, row := range rows {
		if row.Kind == planSelectableHeading && row.Heading == heading {
			return i
		}
	}

	return -1
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

func writeDocViewerPlanArtifact(t ginkgo.FullGinkgoTInterface, root string) {
	ginkgo.GinkgoHelper()

	{
		err := os.WriteFile(filepath.Join(root, "app.txt"), []byte("old value\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write source: %v", err))
	}

	planDir := filepath.Join(root, ".idiom", "plans")
	{
		err := os.MkdirAll(planDir, 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir plan dir: %v", err))
	}

	artifact := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		PlanID:        "plan_test",
		Created:       "2026-06-05T17:00:00Z",
		Rationale:     "Update the visible value.",
		SessionID:     "session-test",
		Task: planartifact.PlanArtifactTask{
			Summary: "doc viewer fixture",
		},
		Contract: planartifact.PlanArtifactContract{
			Kind:              "plan_then_execute",
			StrategyOwner:     "model",
			ContractOwner:     "harness",
			PermissionProfile: "plan_then_execute",
			VerificationOwner: "planner",
		},
		Packet: planartifact.PlanArtifactPacket{
			Status: "not_provided",
		},
		Paths: planartifact.PlanArtifactPaths{
			Editable: []string{"app.txt"},
			Changed:  []string{"app.txt"},
		},
		Evidence: []planartifact.PlanArtifactEvidence{{
			ID:      "evidence-1",
			Kind:    "source_body",
			Path:    "app.txt",
			Summary: "read app.txt",
		}},
		VerificationObligations: []planartifact.PlanArtifactVerificationObligation{{
			ID:       "verification-1",
			Kind:     "executor_lane_sanity",
			Owner:    "planner",
			Status:   "open",
			Required: true,
		}},
		Approval: planartifact.PlanArtifactApproval{
			Required:            false,
			Status:              "implicit",
			ScopeID:             "plan_test:submit_plan",
			Mode:                "plan_then_execute",
			ExpirationPolicy:    "workspace_fingerprint",
			ExecutionPermission: "execute_all",
		},
		Handoff: planartifact.PlanArtifactHandoff{
			ID:     "plan_test:handoff",
			From:   "planner",
			To:     "plan_execute_executor",
			Status: "ready",
			Ready:  true,
		},
		Edits: []planartifact.PlanEdit{{
			Path:    "app.txt",
			OldText: "old value",
			NewText: "new value",
			Purpose: "replace value",
		}},
	}

	body, err := json.MarshalIndent(artifact, "", "  ")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal artifact: %v", err))

	{
		err := os.WriteFile(filepath.Join(planDir, "last.plan.json"), body, 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write artifact: %v", err))
	}
}
