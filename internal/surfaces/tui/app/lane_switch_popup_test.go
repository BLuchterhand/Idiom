package app

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("open lane switch popup builds four options and pre selects recommended", func() {
	m := &Model{}

	m.openLaneSwitchPopup("fix the bug", laneSwitchPlanExecute)

	gomega.Expect(m.laneSwitchPopup.open).To(gomega.BeTrue(), fmt.Sprint("expected popup open"))

	{
		got := len(m.laneSwitchPopup.options)
		gomega.Expect(got).To(gomega.Equal(4), fmt.Sprintf("expected 4 options, got %d", got))
	}

	wantKeys := []string{laneSwitchImplement, laneSwitchPlanExecute, laneSwitchFeaturePlanner, laneSwitchNone}
	for i, want := range wantKeys {
		gomega.Expect(m.laneSwitchPopup.options[i].key).To(gomega.Equal(want), fmt.Sprintf("option[%d].key = %q, want %q", i, m.laneSwitchPopup.options[i].key, want))
	}

	gomega.Expect(m.laneSwitchPopup.cursor).To(gomega.Equal(1), fmt.Sprintf("expected cursor on plan-execute (idx 1), got %d", m.laneSwitchPopup.cursor))

	gomega.Expect(m.laneSwitchPopup.options[1].recommended).To(gomega.BeTrue(), fmt.Sprint("expected plan-execute option flagged recommended"))
})

var _ = ginkgo.It("open lane switch popup clamps unknown recommended", func() {
	m := &Model{}

	m.openLaneSwitchPopup("obj", "bogus")

	gomega.Expect(m.laneSwitchPopup.options[0].key).To(gomega.Equal(laneSwitchImplement), fmt.Sprintf("expected unknown recommended to clamp to implement, got cursor=%d", m.laneSwitchPopup.cursor))
	gomega.Expect(m.laneSwitchPopup.options[0].recommended).To(gomega.BeTrue(), fmt.Sprintf("expected unknown recommended to clamp to implement, got cursor=%d", m.laneSwitchPopup.cursor))
})

var _ = ginkgo.It("lane switch popup key down moves cursor", func() {
	m := &Model{}
	m.openLaneSwitchPopup("obj", laneSwitchImplement)

	_, handled := m.handleLaneSwitchPopupKey(tea.KeyMsg{Type: tea.KeyDown})
	gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("expected KeyDown handled"))

	gomega.Expect(m.laneSwitchPopup.cursor).To(gomega.Equal(1), fmt.Sprintf("expected cursor=1 after KeyDown, got %d", m.laneSwitchPopup.cursor))
})

var _ = ginkgo.It("lane switch popup key up clamps at zero", func() {
	m := &Model{}
	m.openLaneSwitchPopup("obj", laneSwitchImplement)

	_, _ = m.handleLaneSwitchPopupKey(tea.KeyMsg{Type: tea.KeyUp})
	gomega.Expect(m.laneSwitchPopup.cursor).To(gomega.Equal(0), fmt.Sprintf("expected cursor clamped at 0, got %d", m.laneSwitchPopup.cursor))
})

var _ = ginkgo.It("lane switch popup esc closes", func() {
	m := &Model{}
	m.openLaneSwitchPopup("obj", laneSwitchImplement)

	_, handled := m.handleLaneSwitchPopupKey(tea.KeyMsg{Type: tea.KeyEsc})
	gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("expected Esc handled"))

	gomega.Expect(m.laneSwitchPopup.open).To(gomega.BeFalse(), fmt.Sprint("expected popup closed after Esc"))
})

var _ = ginkgo.It("lane switch popup selection streaming blocks dispatch", func() {
	m := &Model{streaming: true}
	m.openLaneSwitchPopup("obj", laneSwitchImplement)

	// Pre-render so optionRowYs is populated for the mouse path too.
	_ = m.renderLaneSwitchPopup(120, 40)

	m.applyLaneSwitchSelection(0)

	gomega.Expect(m.laneSwitchPopup.open).To(gomega.BeFalse(), fmt.Sprint("expected popup closed even when streaming blocks dispatch"))

	gomega.Expect(m.lastLaneSwitchSelection).To(gomega.Equal(laneSwitchImplement), fmt.Sprintf("expected selection recorded even when dispatch blocked, got %q", m.lastLaneSwitchSelection))

	// streaming must remain true: no async dispatch path was entered.
	gomega.Expect(m.streaming).To(gomega.BeTrue(), fmt.Sprint("streaming flag must not be cleared when dispatch is blocked"))
})

var _ = ginkgo.It("lane switch popup number key dispatches and closes", func() {
	m := &Model{}
	m.openLaneSwitchPopup("obj", laneSwitchImplement)

	// "4" maps to the "no" option (stay read-only): popup should close with
	// no dispatch and no streaming state change.
	_, handled := m.handleLaneSwitchPopupKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4")})
	gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("expected number key handled"))

	gomega.Expect(m.laneSwitchPopup.open).To(gomega.BeFalse(), fmt.Sprint("expected popup closed after number-key confirm"))

	gomega.Expect(m.streaming).To(gomega.BeFalse(), fmt.Sprint("expected no dispatch for the 'no' option"))

	gomega.Expect(m.lastLaneSwitchSelection).To(gomega.Equal(laneSwitchNone), fmt.Sprintf("expected lastLaneSwitchSelection=%q, got %q", laneSwitchNone, m.lastLaneSwitchSelection))
})

var _ = ginkgo.It("lane switch popup number key records implement selection without dispatch", func() {
	// Stub context (nil client/runner): selection is recorded, dispatch is
	// skipped so the test does not need a live model server.
	m := &Model{}
	m.openLaneSwitchPopup("fix the bug", laneSwitchImplement)

	_, handled := m.handleLaneSwitchPopupKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("expected number key handled"))

	gomega.Expect(m.laneSwitchPopup.open).To(gomega.BeFalse(), fmt.Sprint("expected popup closed after implement selection"))

	gomega.Expect(m.lastLaneSwitchSelection).To(gomega.Equal(laneSwitchImplement), fmt.Sprintf("expected lastLaneSwitchSelection=%q, got %q", laneSwitchImplement, m.lastLaneSwitchSelection))

	gomega.Expect(m.streaming).To(gomega.BeFalse(), fmt.Sprint("expected no async dispatch in stub context"))
})

var _ = ginkgo.It("lane switch popup ignores unhandled keys", func() {
	m := &Model{}
	m.openLaneSwitchPopup("obj", laneSwitchImplement)

	_, handled := m.handleLaneSwitchPopupKey(tea.KeyMsg{Type: tea.KeySpace})
	gomega.Expect(handled).To(gomega.BeFalse(), fmt.Sprint("expected unhandled key to fall through"))

	gomega.Expect(m.laneSwitchPopup.open).To(gomega.BeTrue(), fmt.Sprint("expected popup still open after unhandled key"))
})

var _ = ginkgo.It("handle key press dispatches to lane switch popup when open", func() {
	m := &Model{}
	m.openLaneSwitchPopup("obj", laneSwitchImplement)

	_, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyEsc})
	gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("expected handleKeyPress to dispatch to lane-switch popup when open"))

	gomega.Expect(m.laneSwitchPopup.open).To(gomega.BeFalse(), fmt.Sprint("expected popup closed via handleKeyPress dispatch"))
})

var _ = ginkgo.It("render lane switch popup records option row geometry", func() {
	m := &Model{}
	m.openLaneSwitchPopup("fix the bug in pkg/auth", laneSwitchImplement)

	out := m.renderLaneSwitchPopup(120, 40)
	gomega.Expect(out).NotTo(gomega.Equal(""), fmt.Sprint("expected non-empty popup render"))

	gomega.Expect(len(m.laneSwitchPopup.optionRowYs)).To(gomega.Equal(4), fmt.Sprintf("expected 4 option row Ys recorded, got %d", len(m.laneSwitchPopup.optionRowYs)))

	// Rows must be strictly ascending and spaced two lines apart (label +
	// description per option).
	for i := 1; i < len(m.laneSwitchPopup.optionRowYs); i++ {
		prev := m.laneSwitchPopup.optionRowYs[i-1]
		cur := m.laneSwitchPopup.optionRowYs[i]
		gomega.Expect(cur <= prev).To(gomega.BeFalse(), fmt.Sprintf("option row Ys not ascending at i=%d: %d -> %d", i, prev, cur))
		gomega.Expect(cur-prev).To(gomega.Equal(2), fmt.Sprintf("expected 2-line spacing between option rows, got %d at i=%d", cur-prev, i))
	}
})

var _ = ginkgo.It("render lane switch popup empty when closed", func() {
	m := &Model{}

	{
		out := m.renderLaneSwitchPopup(80, 24)
		gomega.Expect(out).To(gomega.Equal(""), fmt.Sprintf("expected empty render when closed, got %q", out))
	}
})

var _ = ginkgo.It("handle mouse consumes click inside popup box but outside option rows", func() {
	// Modal invariant: any left-click while the popup is open is consumed so
	// the underlying viewport does not scroll behind the overlay, even when
	// the click lands on the title/hint area rather than an option row.
	m := &Model{}
	m.openLaneSwitchPopup("obj", laneSwitchImplement)

	_ = m.renderLaneSwitchPopup(120, 40)

	// Click on the title row (boxTop, not any optionRowY).
	titleY := m.laneSwitchPopup.boxTop
	titleX := m.laneSwitchPopup.boxLeft + 2

	_, handled := m.handleMouse(tea.MouseMsg{
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
		X:      titleX,
		Y:      titleY,
	})

	gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("expected click inside popup footprint (title row) to be consumed"))

	gomega.Expect(m.laneSwitchPopup.open).To(gomega.BeTrue(), fmt.Sprint("popup should remain open after a non-option-row click inside the footprint"))
})

var _ = ginkgo.It("handle mouse dispatches to lane switch popup when open", func() {
	m := &Model{}
	m.openLaneSwitchPopup("obj", laneSwitchImplement)

	// Force a render so optionRowYs is populated with concrete coordinates.
	_ = m.renderLaneSwitchPopup(120, 40)

	clickY := m.laneSwitchPopup.optionRowYs[2]
	clickX := m.laneSwitchPopup.boxLeft + 2

	_, handled := m.handleMouse(tea.MouseMsg{
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
		X:      clickX,
		Y:      clickY,
	})

	gomega.Expect(handled).To(gomega.BeTrue(), fmt.Sprint("expected mouse click on option row to be handled"))

	gomega.Expect(m.laneSwitchPopup.open).To(gomega.BeFalse(), fmt.Sprint("expected popup closed after clicking an option row"))

	gomega.Expect(m.lastLaneSwitchSelection).To(gomega.Equal(laneSwitchFeaturePlanner), fmt.Sprintf("expected clicked option idx 2 to record feature-planner, got %q", m.lastLaneSwitchSelection))
})
