package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"idiom/internal/platform/textutil"
	"idiom/internal/workflows/planexecute"
)

type planApprovalAction string

const (
	planApprovalActionCurrent planApprovalAction = "current"
	planApprovalActionAll     planApprovalAction = "all"
)

type pendingPlanApproval struct {
	request  planexecute.PlanApprovalRequest
	response chan planApprovalResponse
}

type planApprovalResponse struct {
	allow   bool
	blanket bool
}

func (p *pendingPlanApproval) summary() string {
	if p == nil {
		return ""
	}

	paths := p.request.Paths
	label := string(p.request.Phase)
	if label == "" {
		label = "plan"
	}

	if len(paths) == 0 {
		return label + ": no write paths"
	}

	var names []string
	for i, p := range paths {
		if i >= 3 {
			names = append(names, fmt.Sprintf("+%d more", len(paths)-i))
			break
		}

		names = append(names, filepath.Base(p))
	}

	return fmt.Sprintf("%s: %d path(s): %s", label, len(paths), strings.Join(names, ", "))
}

func (m *Model) requestPlanExecuteApproval(ctx context.Context, req planexecute.PlanApprovalRequest) planexecute.PlanApprovalDecision {
	if m == nil {
		return planexecute.PlanApprovalDecision{Reason: "TUI model unavailable for plan approval"}
	}

	m.planApprovalMu.Lock()
	blanketApproved := m.planExecuteApproveAllEdits
	m.planApprovalMu.Unlock()
	if blanketApproved {
		m.markPlanApprovalRows(true, true)
		m.queueUIEvent(planRefreshMsg{})

		return planexecute.PlanApprovalDecision{
			Approved:      true,
			Blanket:       true,
			ApprovedPaths: append([]string(nil), req.Paths...),
			ApprovedBy:    "operator",
		}
	}

	if m.eventStream != nil || m.suppressPromptTranscript || m.nonInteractive {
		return planexecute.PlanApprovalDecision{Reason: "no interactive TUI available to approve planned edits"}
	}

	m.planApprovalSerial.Lock()
	defer m.planApprovalSerial.Unlock()

	resp := make(chan planApprovalResponse, 1)
	pending := &pendingPlanApproval{request: req, response: resp}

	m.planApprovalMu.Lock()
	m.planApprovalPending = pending
	m.planApprovalMu.Unlock()

	m.queueUIEvent(planApprovalRequiredMsg{})
	m.queueUIEvent(infoMsg{text: "Plan edit approval required. Open the Plan tab and select Approve or Approve all edits."})

	select {
	case <-ctx.Done():
		m.clearPendingPlanApproval(pending)
		return planexecute.PlanApprovalDecision{Reason: ctx.Err().Error()}
	case decision := <-resp:
		if !decision.allow {
			return planexecute.PlanApprovalDecision{Reason: "plan edit approval denied"}
		}

		if decision.blanket {
			m.planApprovalMu.Lock()
			m.planExecuteApproveAllEdits = true
			m.planApprovalMu.Unlock()
		}

		return planexecute.PlanApprovalDecision{
			Approved:      true,
			Blanket:       decision.blanket,
			ApprovedPaths: append([]string(nil), req.Paths...),
			ApprovedBy:    "operator",
		}
	}
}

func (m *Model) resolvePlanApproval(allow bool, blanket bool) (string, bool) {
	if m == nil {
		return "", false
	}

	m.planApprovalMu.Lock()
	pending := m.planApprovalPending
	m.planApprovalPending = nil
	if allow && blanket {
		m.planExecuteApproveAllEdits = true
	}
	m.planApprovalMu.Unlock()

	if pending == nil {
		return "", false
	}

	pending.response <- planApprovalResponse{allow: allow, blanket: blanket}
	m.markPlanApprovalRows(allow, blanket)

	return pending.summary(), true
}

func (m *Model) directPlanExecuteApprovalShouldConsumeOperatorInput(input string) bool {
	if m == nil || m.currentPlanApproval() == nil {
		return false
	}

	_, _, ok := planExecuteApprovalDecisionFromOperatorInput(input)
	return ok
}

func (m *Model) runDirectPlanExecuteApprovalAnswerOnce(input string) (string, error) {
	allow, blanket, ok := planExecuteApprovalDecisionFromOperatorInput(input)
	if !ok {
		return "Plan edit approval is pending. Use `/approve`, `/approve all`, `/deny`, or select an approval action in the Plan tab.", nil
	}

	summary, resolved := m.resolvePlanApproval(allow, blanket)
	if !resolved {
		return "No pending plan approval.", nil
	}

	if !allow {
		return "Denied planned edits: " + summary, nil
	}
	if blanket {
		return "Approved all plan-execute edits: " + summary, nil
	}

	return "Approved planned edits: " + summary, nil
}

func planExecuteApprovalDecisionFromOperatorInput(input string) (allow bool, blanket bool, ok bool) {
	lower := strings.ToLower(strings.TrimSpace(input))
	if lower == "" {
		return false, false, false
	}

	if planExecuteApprovalInputDenies(lower) {
		return false, false, true
	}

	if planExecuteApprovalInputApprovesAll(lower) {
		return true, true, true
	}
	if planExecuteApprovalInputApprovesCurrent(lower) {
		return true, false, true
	}

	return false, false, false
}

func planExecuteApprovalInputDenies(lower string) bool {
	return textContainsAny(lower, []string{
		"deny",
		"reject",
		"disapprove",
		"do not approve",
		"don't approve",
		"dont approve",
		"not approve",
		"not approved",
		"do not begin",
		"don't begin",
		"dont begin",
		"do not start",
		"don't start",
		"dont start",
		"do not proceed",
		"don't proceed",
		"dont proceed",
		"do not continue",
		"don't continue",
		"dont continue",
		"not yet",
		"hold off",
		"wait",
		"stop",
	})
}

func planExecuteApprovalInputApprovesAll(lower string) bool {
	return strings.Contains(lower, "approve all") ||
		strings.Contains(lower, "all edits") ||
		strings.Contains(lower, "blanket")
}

func planExecuteApprovalInputApprovesCurrent(lower string) bool {
	return strings.Contains(lower, "approve") ||
		strings.Contains(lower, "approved") ||
		strings.Contains(lower, "go ahead") ||
		strings.Contains(lower, "please begin") ||
		strings.Contains(lower, "begin implementation") ||
		strings.Contains(lower, "begin on your implementation") ||
		strings.Contains(lower, "please start") ||
		strings.Contains(lower, "start implementation") ||
		strings.Contains(lower, "please proceed") ||
		lower == "proceed" ||
		strings.Contains(lower, "proceed with implementation") ||
		strings.Contains(lower, "continue with implementation")
}

func textContainsAny(value string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}

	return false
}

func (m *Model) clearPendingPlanApproval(pending *pendingPlanApproval) {
	if m == nil || pending == nil {
		return
	}

	m.planApprovalMu.Lock()
	if m.planApprovalPending == pending {
		m.planApprovalPending = nil
	}
	m.planApprovalMu.Unlock()
}

func (m *Model) currentPlanApproval() *pendingPlanApproval {
	if m == nil {
		return nil
	}

	m.planApprovalMu.Lock()
	defer m.planApprovalMu.Unlock()

	return m.planApprovalPending
}

func parsePlanApprovalAction(value string) (planApprovalAction, bool) {
	switch action := planApprovalAction(strings.TrimSpace(value)); action {
	case planApprovalActionCurrent, planApprovalActionAll:
		return action, true
	default:
		return "", false
	}
}

func formatPlanApprovalAnchor(action planApprovalAction) string {
	return fmt.Sprintf("<!-- plan-approval:%s -->", action)
}

func planApprovalRows(pathCount int) []string {
	return []string{
		fmt.Sprintf("- [ ] Approve planned edits (%d file(s)) %s", pathCount, formatPlanApprovalAnchor(planApprovalActionCurrent)),
		fmt.Sprintf("- [ ] Approve all edits in this extended session %s", formatPlanApprovalAnchor(planApprovalActionAll)),
	}
}

func (m *Model) activatePlanApprovalAction(action planApprovalAction) bool {
	switch action {
	case planApprovalActionCurrent:
		if summary, ok := m.resolvePlanApproval(true, false); ok {
			m.status = "Approved planned edits: " + summary
		} else {
			m.status = "No pending plan approval."
		}
	case planApprovalActionAll:
		if summary, ok := m.resolvePlanApproval(true, true); ok {
			m.status = "Approved all plan-execute edits: " + summary
		} else {
			m.status = "No pending plan approval."
		}
	default:
		return false
	}

	m.renderPlanPanelContent()

	return true
}

func (m *Model) markPlanApprovalRows(allow bool, blanket bool) {
	path := m.ensurePlanPath()
	if path == "" || !allow {
		return
	}

	if err := m.mutatePlanLines(path, func(lines []string) []string {
		targets := map[planApprovalAction]bool{planApprovalActionCurrent: true}
		if blanket {
			targets[planApprovalActionAll] = true
		}

		for i, line := range lines {
			item, ok := parsePlanItemLine(i, line)
			if !ok || !targets[item.PlanApprovalAction] {
				continue
			}

			lines[i] = replacePlanCheckbox(line, "[x]")
		}

		return lines
	}); err != nil {
		return
	}
}

func replacePlanCheckbox(line string, targetBox string) string {
	for _, oldBox := range []string{"[ ]", "[~]", "[x]", "[X]"} {
		if strings.Contains(line, oldBox) {
			return strings.Replace(line, oldBox, targetBox, 1)
		}
	}

	return line
}

func (m *Model) planApprovalStatusText() string {
	if pending := m.currentPlanApproval(); pending != nil {
		return "Approval pending: " + textutil.Truncate(pending.summary(), 120)
	}

	m.planApprovalMu.Lock()
	defer m.planApprovalMu.Unlock()
	if m.planExecuteApproveAllEdits {
		return "All plan-execute edits are approved for this session."
	}

	return ""
}

func (m *Model) beginPlanExecuteApprovalSession() {
	if m == nil {
		return
	}

	m.planApprovalMu.Lock()
	m.planApprovalPending = nil
	m.planApprovalMu.Unlock()
}

func (m *Model) endPlanExecuteApprovalSession() {
	if m == nil {
		return
	}

	m.planApprovalMu.Lock()
	m.planApprovalPending = nil
	m.planApprovalMu.Unlock()
}
