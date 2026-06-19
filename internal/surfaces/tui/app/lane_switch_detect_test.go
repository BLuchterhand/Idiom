package app

import (
	"strings"
	"testing"
)

func TestLaneSwitchReferralFromReplyMatchesConservativePatterns(t *testing.T) {
	cases := []struct {
		name        string
		reply       string
		wantMatch   bool
		wantRecKey  string
	}{
		{
			name:       "read-only mode refusal",
			reply:      "I'm currently in read-only mode, so I cannot edit files. Use /implement to switch.",
			wantMatch:  true,
			wantRecKey: laneSwitchImplement,
		},
		{
			name:       "read-only lane explicit",
			reply:      "This ask needs the implement lane; I'm in a read-only lane right now.",
			wantMatch:  true,
			wantRecKey: laneSwitchImplement,
		},
		{
			name:       "switch to plan-execute lane",
			reply:      "Please switch to the plan-execute lane and re-run; I cannot plan here.",
			wantMatch:  true,
			wantRecKey: laneSwitchPlanExecute,
		},
		{
			name:       "use /feature-planner suggestion",
			reply:      "Use /feature-planner to dispatch this atom; the current route cannot.",
			wantMatch:  true,
			wantRecKey: laneSwitchFeaturePlanner,
		},
		{
			name:       "not in the right lane",
			reply:      "I am not in the right lane for this task.",
			wantMatch:  true,
			wantRecKey: laneSwitchImplement,
		},
		{
			name:       "cannot edit files read-only",
			reply:      "I cannot edit files in read-only mode.",
			wantMatch:  true,
			wantRecKey: laneSwitchImplement,
		},
		{
			name:       "write-capable lane suggestion",
			reply:      "Reply with /plan-execute to enter the write-capable lane.",
			wantMatch:  true,
			wantRecKey: laneSwitchPlanExecute,
		},
		{
			name:       "plain status answer no refusal",
			reply:      "The auth middleware checks tokens in read-only fashion before delegating.",
			wantMatch:  false,
		},
		{
			name:       "architecture explanation not refusal-shaped",
			reply:      "The conversation lane is read-only by design; it never writes.",
			wantMatch:  false, // "read-only by design" is descriptive, not a lane-refusal
		},
		{
			name:       "empty reply",
			reply:      "",
			wantMatch:  false,
		},
		{
			name:       "bare single word implement not a signal",
			reply:      "You can implement this by calling foo() in your code.",
			wantMatch:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, ok := laneSwitchReferralFromReply(tc.reply)
			if ok != tc.wantMatch {
				t.Fatalf("laneSwitchReferralFromReply match = %v, want %v (reply=%q)", ok, tc.wantMatch, tc.reply)
			}

			if ok && rec != tc.wantRecKey {
				t.Errorf("recommended key = %q, want %q (reply=%q)", rec, tc.wantRecKey, tc.reply)
			}
		})
	}
}

func TestLaneSwitchHeadlessReplyContainsAllOptions(t *testing.T) {
	reply := laneSwitchHeadlessReply("fix the bug", laneSwitchImplement)

	required := []string{
		"/implement fix the bug",
		"/plan-execute fix the bug",
		"/feature-planner fix the bug",
		"No, keep the read-only answer",
		"Recommended: /implement",
	}

	for _, want := range required {
		if !strings.Contains(reply, want) {
			t.Errorf("headless reply missing %q\n----\n%s", want, reply)
		}
	}
}

func TestLaneSwitchHeadlessReplyClampsUnknownRecommended(t *testing.T) {
	reply := laneSwitchHeadlessReply("obj", "bogus")
	if !strings.Contains(reply, "Recommended: /implement") {
		t.Fatalf("expected unknown recommended to clamp to /implement, got:\n%s", reply)
	}
}

func TestApplyLaneSwitchReferralOpensPopupInTUI(t *testing.T) {
	m := &Model{} // no event stream, no suppress flags → TUI (interactive)

	reply := m.applyLaneSwitchReferralIfAny(
		"update the README",
		"I'm in read-only mode and cannot edit files. Use /implement to switch.",
	)

	if !m.laneSwitchPopup.open {
		t.Fatal("expected popup to open for lane-refusal reply in TUI mode")
	}

	// Reply is preserved unchanged in TUI mode; the popup overlays.
	if !strings.Contains(reply, "read-only mode") {
		t.Errorf("expected original reply preserved in TUI mode, got %q", reply)
	}

	if m.laneSwitchPopup.objective != "update the README" {
		t.Errorf("expected popup to carry original objective, got %q", m.laneSwitchPopup.objective)
	}
}

func TestApplyLaneSwitchReferralAppendsOptionsInHeadless(t *testing.T) {
	m := &Model{suppressPromptTranscript: true} // headless

	reply := m.applyLaneSwitchReferralIfAny(
		"fix the bug in pkg/auth",
		"I'm in read-only mode; please switch to the plan-execute lane.",
	)

	if m.laneSwitchPopup.open {
		t.Error("expected popup NOT to open in headless mode")
	}

	if !strings.Contains(reply, "I'm in read-only mode") {
		t.Errorf("expected original refusal preserved in headless reply, got %q", reply)
	}

	required := []string{
		"/implement fix the bug in pkg/auth",
		"/plan-execute fix the bug in pkg/auth",
		"/feature-planner fix the bug in pkg/auth",
		"Recommended: /plan-execute",
	}
	for _, want := range required {
		if !strings.Contains(reply, want) {
			t.Errorf("headless reply missing %q\n----\n%s", want, reply)
		}
	}
}

func TestApplyLaneSwitchReferralNoOpOnNonRefusal(t *testing.T) {
	m := &Model{}

	original := "The auth middleware validates tokens before delegating to the session store."
	reply := m.applyLaneSwitchReferralIfAny("explain auth", original)

	if reply != original {
		t.Errorf("expected reply unchanged for non-refusal, got %q", reply)
	}

	if m.laneSwitchPopup.open {
		t.Error("expected popup NOT to open for non-refusal reply")
	}
}
