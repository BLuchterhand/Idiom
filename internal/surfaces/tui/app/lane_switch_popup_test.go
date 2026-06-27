package app

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestOpenLaneSwitchPopupBuildsFourOptionsAndPreSelectsRecommended(t *testing.T) {
	m := &Model{}

	m.openLaneSwitchPopup("fix the bug", laneSwitchPlanExecute)

	if !m.laneSwitchPopup.open {
		t.Fatal("expected popup open")
	}

	if got := len(m.laneSwitchPopup.options); got != 4 {
		t.Fatalf("expected 4 options, got %d", got)
	}

	wantKeys := []string{laneSwitchImplement, laneSwitchPlanExecute, laneSwitchFeaturePlanner, laneSwitchNone}
	for i, want := range wantKeys {
		if m.laneSwitchPopup.options[i].key != want {
			t.Errorf("option[%d].key = %q, want %q", i, m.laneSwitchPopup.options[i].key, want)
		}
	}

	if m.laneSwitchPopup.cursor != 1 {
		t.Fatalf("expected cursor on plan-execute (idx 1), got %d", m.laneSwitchPopup.cursor)
	}

	if !m.laneSwitchPopup.options[1].recommended {
		t.Error("expected plan-execute option flagged recommended")
	}
}

func TestOpenLaneSwitchPopupClampsUnknownRecommended(t *testing.T) {
	m := &Model{}

	m.openLaneSwitchPopup("obj", "bogus")

	if m.laneSwitchPopup.options[0].key != laneSwitchImplement || !m.laneSwitchPopup.options[0].recommended {
		t.Fatalf("expected unknown recommended to clamp to implement, got cursor=%d", m.laneSwitchPopup.cursor)
	}
}

func TestLaneSwitchPopupKeyDownMovesCursor(t *testing.T) {
	m := &Model{}
	m.openLaneSwitchPopup("obj", laneSwitchImplement)

	_, handled := m.handleLaneSwitchPopupKey(tea.KeyMsg{Type: tea.KeyDown})
	if !handled {
		t.Fatal("expected KeyDown handled")
	}

	if m.laneSwitchPopup.cursor != 1 {
		t.Fatalf("expected cursor=1 after KeyDown, got %d", m.laneSwitchPopup.cursor)
	}
}

func TestLaneSwitchPopupKeyUpClampsAtZero(t *testing.T) {
	m := &Model{}
	m.openLaneSwitchPopup("obj", laneSwitchImplement)

	_, _ = m.handleLaneSwitchPopupKey(tea.KeyMsg{Type: tea.KeyUp})
	if m.laneSwitchPopup.cursor != 0 {
		t.Fatalf("expected cursor clamped at 0, got %d", m.laneSwitchPopup.cursor)
	}
}

func TestLaneSwitchPopupEscCloses(t *testing.T) {
	m := &Model{}
	m.openLaneSwitchPopup("obj", laneSwitchImplement)

	_, handled := m.handleLaneSwitchPopupKey(tea.KeyMsg{Type: tea.KeyEsc})
	if !handled {
		t.Fatal("expected Esc handled")
	}

	if m.laneSwitchPopup.open {
		t.Fatal("expected popup closed after Esc")
	}
}

func TestLaneSwitchPopupSelectionStreamingBlocksDispatch(t *testing.T) {
	m := &Model{streaming: true}
	m.openLaneSwitchPopup("obj", laneSwitchImplement)

	// Pre-render so optionRowYs is populated for the mouse path too.
	_ = m.renderLaneSwitchPopup(120, 40)

	m.applyLaneSwitchSelection(0)

	if m.laneSwitchPopup.open {
		t.Fatal("expected popup closed even when streaming blocks dispatch")
	}

	if m.lastLaneSwitchSelection != laneSwitchImplement {
		t.Errorf("expected selection recorded even when dispatch blocked, got %q", m.lastLaneSwitchSelection)
	}

	// streaming must remain true: no async dispatch path was entered.
	if !m.streaming {
		t.Fatal("streaming flag must not be cleared when dispatch is blocked")
	}
}

func TestLaneSwitchPopupNumberKeyDispatchesAndCloses(t *testing.T) {
	m := &Model{}
	m.openLaneSwitchPopup("obj", laneSwitchImplement)

	// "4" maps to the "no" option (stay read-only): popup should close with
	// no dispatch and no streaming state change.
	_, handled := m.handleLaneSwitchPopupKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4")})
	if !handled {
		t.Fatal("expected number key handled")
	}

	if m.laneSwitchPopup.open {
		t.Fatal("expected popup closed after number-key confirm")
	}

	if m.streaming {
		t.Fatal("expected no dispatch for the 'no' option")
	}

	if m.lastLaneSwitchSelection != laneSwitchNone {
		t.Fatalf("expected lastLaneSwitchSelection=%q, got %q", laneSwitchNone, m.lastLaneSwitchSelection)
	}
}

func TestLaneSwitchPopupNumberKeyRecordsImplementSelectionWithoutDispatch(t *testing.T) {
	// Stub context (nil client/runner): selection is recorded, dispatch is
	// skipped so the test does not need a live model server.
	m := &Model{}
	m.openLaneSwitchPopup("fix the bug", laneSwitchImplement)

	_, handled := m.handleLaneSwitchPopupKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	if !handled {
		t.Fatal("expected number key handled")
	}

	if m.laneSwitchPopup.open {
		t.Fatal("expected popup closed after implement selection")
	}

	if m.lastLaneSwitchSelection != laneSwitchImplement {
		t.Fatalf("expected lastLaneSwitchSelection=%q, got %q", laneSwitchImplement, m.lastLaneSwitchSelection)
	}

	if m.streaming {
		t.Fatal("expected no async dispatch in stub context")
	}
}

func TestLaneSwitchPopupIgnoresUnhandledKeys(t *testing.T) {
	m := &Model{}
	m.openLaneSwitchPopup("obj", laneSwitchImplement)

	_, handled := m.handleLaneSwitchPopupKey(tea.KeyMsg{Type: tea.KeySpace})
	if handled {
		t.Fatal("expected unhandled key to fall through")
	}

	if !m.laneSwitchPopup.open {
		t.Fatal("expected popup still open after unhandled key")
	}
}

func TestHandleKeyPressDispatchesToLaneSwitchPopupWhenOpen(t *testing.T) {
	m := &Model{}
	m.openLaneSwitchPopup("obj", laneSwitchImplement)

	_, handled := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyEsc})
	if !handled {
		t.Fatal("expected handleKeyPress to dispatch to lane-switch popup when open")
	}

	if m.laneSwitchPopup.open {
		t.Fatal("expected popup closed via handleKeyPress dispatch")
	}
}

func TestRenderLaneSwitchPopupRecordsOptionRowGeometry(t *testing.T) {
	m := &Model{}
	m.openLaneSwitchPopup("fix the bug in pkg/auth", laneSwitchImplement)

	out := m.renderLaneSwitchPopup(120, 40)
	if out == "" {
		t.Fatal("expected non-empty popup render")
	}

	if len(m.laneSwitchPopup.optionRowYs) != 4 {
		t.Fatalf("expected 4 option row Ys recorded, got %d", len(m.laneSwitchPopup.optionRowYs))
	}

	// Rows must be strictly ascending and spaced two lines apart (label +
	// description per option).
	for i := 1; i < len(m.laneSwitchPopup.optionRowYs); i++ {
		prev := m.laneSwitchPopup.optionRowYs[i-1]
		cur := m.laneSwitchPopup.optionRowYs[i]
		if cur <= prev {
			t.Errorf("option row Ys not ascending at i=%d: %d -> %d", i, prev, cur)
		}
		if cur-prev != 2 {
			t.Errorf("expected 2-line spacing between option rows, got %d at i=%d", cur-prev, i)
		}
	}
}

func TestRenderLaneSwitchPopupEmptyWhenClosed(t *testing.T) {
	m := &Model{}

	if out := m.renderLaneSwitchPopup(80, 24); out != "" {
		t.Fatalf("expected empty render when closed, got %q", out)
	}
}

func TestHandleMouseConsumesClickInsidePopupBoxButOutsideOptionRows(t *testing.T) {
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

	if !handled {
		t.Fatal("expected click inside popup footprint (title row) to be consumed")
	}

	if !m.laneSwitchPopup.open {
		t.Fatal("popup should remain open after a non-option-row click inside the footprint")
	}
}

func TestHandleMouseDispatchesToLaneSwitchPopupWhenOpen(t *testing.T) {
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

	if !handled {
		t.Fatal("expected mouse click on option row to be handled")
	}

	if m.laneSwitchPopup.open {
		t.Fatal("expected popup closed after clicking an option row")
	}

	if m.lastLaneSwitchSelection != laneSwitchFeaturePlanner {
		t.Fatalf("expected clicked option idx 2 to record feature-planner, got %q", m.lastLaneSwitchSelection)
	}
}
