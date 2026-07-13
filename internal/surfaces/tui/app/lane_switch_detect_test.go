package app

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("lane switch referral from reply matches conservative patterns", func() {
	cases := []struct {
		name       string
		reply      string
		wantMatch  bool
		wantRecKey string
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
			name:      "plain status answer no refusal",
			reply:     "The auth middleware checks tokens in read-only fashion before delegating.",
			wantMatch: false,
		},
		{
			name:      "architecture explanation not refusal-shaped",
			reply:     "The conversation lane is read-only by design; it never writes.",
			wantMatch: false, // "read-only by design" is descriptive, not a lane-refusal
		},
		{
			name:      "empty reply",
			reply:     "",
			wantMatch: false,
		},
		{
			name:      "bare single word implement not a signal",
			reply:     "You can implement this by calling foo() in your code.",
			wantMatch: false,
		},
	}

	for _, tc := range cases {
		ginkgo.By(fmt.Sprint(tc.name), func() {
			rec, ok := laneSwitchReferralFromReply(tc.reply)
			gomega.Expect(ok).To(gomega.Equal(tc.wantMatch), fmt.Sprintf("laneSwitchReferralFromReply match = %v, want %v (reply=%q)", ok, tc.wantMatch, tc.reply))

			if ok {
				gomega.Expect(rec).To(gomega.Equal(tc.wantRecKey), fmt.Sprintf("recommended key = %q, want %q (reply=%q)", rec, tc.wantRecKey, tc.reply))
			}
		})
	}
})

var _ = ginkgo.It("lane switch headless reply contains all options", func() {
	reply := laneSwitchHeadlessReply("fix the bug", laneSwitchImplement)

	required := []string{
		"/implement fix the bug",
		"/plan-execute fix the bug",
		"/feature-planner fix the bug",
		"No, keep the read-only answer",
		"Recommended: /implement",
	}

	for _, want := range required {
		gomega.Expect(strings.Contains(reply, want)).To(gomega.BeTrue(), fmt.Sprintf("headless reply missing %q\n----\n%s", want, reply))
	}
})

var _ = ginkgo.It("lane switch headless reply clamps unknown recommended", func() {
	reply := laneSwitchHeadlessReply("obj", "bogus")
	gomega.Expect(strings.Contains(reply, "Recommended: /implement")).To(gomega.BeTrue(), fmt.Sprintf("expected unknown recommended to clamp to /implement, got:\n%s", reply))
})

var _ = ginkgo.It("apply lane switch referral opens popup in tui", func() {
	m := &Model{events: make(chan tea.Msg, 1)} // no event stream, no suppress flags → TUI (interactive)
	session := newPromptExecutionSession(context.Background(), m, m.events, false)

	reply := session.applyLaneSwitchReferralIfAny(
		"update the README",
		"I'm in read-only mode and cannot edit files. Use /implement to switch.",
	)
	var popup laneSwitchPopupMsg
	gomega.Expect(m.events).To(gomega.Receive(&popup))
	updated, _ := m.Update(popup)
	m = updated.(*Model)

	gomega.Expect(m.laneSwitchPopup.open).To(gomega.BeTrue(), fmt.Sprint("expected popup to open for lane-refusal reply in TUI mode"))

	// Reply is preserved unchanged in TUI mode; the popup overlays.
	gomega.Expect(strings.Contains(reply, "read-only mode")).To(gomega.BeTrue(), fmt.Sprintf("expected original reply preserved in TUI mode, got %q", reply))

	gomega.Expect(m.laneSwitchPopup.objective).To(gomega.Equal("update the README"), fmt.Sprintf("expected popup to carry original objective, got %q", m.laneSwitchPopup.objective))
})

var _ = ginkgo.It("apply lane switch referral appends options in headless", func() {
	m := &Model{events: make(chan tea.Msg, 1), promptRuntimeState: promptRuntimeState{
		promptRuntimeSnapshotState: promptRuntimeSnapshotState{suppressPromptTranscript: true},
	}} // headless
	session := newPromptExecutionSession(context.Background(), m, m.events, false)

	reply := session.applyLaneSwitchReferralIfAny(
		"fix the bug in pkg/auth",
		"I'm in read-only mode; please switch to the plan-execute lane.",
	)

	gomega.Expect(m.laneSwitchPopup.open).To(gomega.BeFalse(), fmt.Sprint("expected popup NOT to open in headless mode"))

	gomega.Expect(strings.Contains(reply, "I'm in read-only mode")).To(gomega.BeTrue(), fmt.Sprintf("expected original refusal preserved in headless reply, got %q", reply))

	required := []string{
		"/implement fix the bug in pkg/auth",
		"/plan-execute fix the bug in pkg/auth",
		"/feature-planner fix the bug in pkg/auth",
		"Recommended: /plan-execute",
	}
	for _, want := range required {
		gomega.Expect(strings.Contains(reply, want)).To(gomega.BeTrue(), fmt.Sprintf("headless reply missing %q\n----\n%s", want, reply))
	}
})

var _ = ginkgo.It("apply lane switch referral no op on non refusal", func() {
	m := &Model{events: make(chan tea.Msg, 1)}
	session := newPromptExecutionSession(context.Background(), m, m.events, false)

	original := "The auth middleware validates tokens before delegating to the session store."
	reply := session.applyLaneSwitchReferralIfAny("explain auth", original)

	gomega.Expect(reply).To(gomega.Equal(original), fmt.Sprintf("expected reply unchanged for non-refusal, got %q", reply))

	gomega.Expect(m.laneSwitchPopup.open).To(gomega.BeFalse(), fmt.Sprint("expected popup NOT to open for non-refusal reply"))
})
