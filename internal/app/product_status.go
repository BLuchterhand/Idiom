package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/llama"
	"idiom/internal/productworkflow"
)

func (m *Model) slashProductCreate(_ string, fields []string) bool {
	m.logProductWorkflowCommand(fields)
	effortRoot, err := m.productCreateEffortRoot(fields)
	if err != nil {
		m.status = "/product-create: " + err.Error()
		return false
	}
	opts := productworkflow.CreateEffortOptions{EffortRoot: effortRoot}
	result, createErr := productworkflow.InitializeEffort(opts)
	if createErr != nil {
		m.status = fmt.Sprintf("/product-create: %v", createErr)
		return false
	}
	m.logProductWorkflowEvent("idiom.product_workflow.create", map[string]any{
		"status":            result.Status,
		"effort_root":       result.EffortRoot,
		"current_phase":     result.CurrentPhase,
		"next_valid_action": result.NextValidAction,
	})
	displayRoot := m.displayPath(result.EffortRoot)
	m.insertBeforePendingAssistant(llama.ChatMessage{
		Role:    "operator",
		Content: fmt.Sprintf("product create: %s\n  status: %s\n  phase: %s\n  next: %s", displayRoot, result.Status, result.CurrentPhase, result.NextValidAction),
	})
	switch result.Status {
	case "ok":
		m.status = fmt.Sprintf("Product effort created at %s: %s", displayRoot, result.CurrentPhase)
	case "resumed":
		m.status = fmt.Sprintf("Product effort resumed at %s: %s", displayRoot, result.CurrentPhase)
	case "blocked":
		m.status = fmt.Sprintf("Product create blocked at %s: %s", displayRoot, result.CurrentPhase)
	default:
		m.status = fmt.Sprintf("Product create %s: %s", result.Status, displayRoot)
	}
	return false
}

func (m *Model) productCreateEffortRoot(fields []string) (string, error) {
	switch len(fields) {
	case 1:
		return m.productStatusAbsPath(".")
	case 2:
		if fields[1] == "--effort-root" {
			return "", errors.New("Usage: /product-create [--effort-root <path>|<path>]")
		}
		return m.productStatusAbsPath(fields[1])
	case 3:
		if fields[1] != "--effort-root" {
			return "", errors.New("Usage: /product-create [--effort-root <path>|<path>]")
		}
		return m.productStatusAbsPath(fields[2])
	default:
		return "", errors.New("Usage: /product-create [--effort-root <path>|<path>]")
	}
}

func (m *Model) slashProductStatus(_ string, fields []string) bool {
	absRoot, err := m.productStatusEffortRoot(fields)
	if err != nil {
		m.status = err.Error()
		return false
	}

	statusView, workflowState, statusErr := productworkflow.LoadCheckedProductStatus(absRoot)
	operatorStatus := productworkflow.BuildProductOperatorStatusView(statusView, workflowState)
	body := productworkflow.FormatProductOperatorStatusText(filepath.Base(absRoot), operatorStatus)
	if hint := m.productStatusTUIRecoveryHint(absRoot, operatorStatus); hint != "" {
		body = strings.TrimRight(body, "\n") + "\n  tui_command_hint=" + hint + "\n"
	}
	m.insertBeforePendingAssistant(llama.ChatMessage{
		Role:    "operator",
		Content: strings.TrimRight(body, "\n"),
	})

	displayRoot := m.displayPath(absRoot)
	if statusErr != nil {
		m.status = "Product status failed for " + displayRoot + ": " + statusErr.Error()
		return false
	}
	if len(operatorStatus.Blockers) > 0 {
		m.status = fmt.Sprintf("Product status loaded for %s: %d blocker(s).", displayRoot, len(operatorStatus.Blockers))
		return false
	}
	m.status = "Product status loaded for " + displayRoot + "."
	return false
}

func (m *Model) slashProductApprove(_ string, fields []string) bool {
	m.logProductWorkflowCommand(fields)
	// If in a product creation phase, run the creation-specific approval gate.
	if m.featurePlannerPhase == productworkflow.PhasePRDDrafting || m.featurePlannerPhase == productworkflow.PhaseADRDrafting {
		if m.streaming {
			m.status = "A product creation turn is already in progress."
			return false
		}
		phase := m.featurePlannerPhase
		m.streaming = true
		m.workingFrame = 0
		m.currentTurnBusyStatus = "Running " + phase + " approval..."
		m.beginAgentHistoryTurn()
		go func() {
			answer, err := m.runProductApproveGate()
			if err != nil {
				m.events <- streamDoneMsg{err: err}
				return
			}
			m.insertBeforePendingAssistant(llama.ChatMessage{
				Role:    "operator",
				Content: fmt.Sprintf("product approve: %s phase", phase),
			})
			m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: answer})
			m.logMessage("assistant", answer)
			m.events <- streamDoneMsg{}
		}()
		m.status = "Product approval in progress..."
		return false
	}

	absRoot, err := m.productStatusEffortRoot(fields)
	if err != nil {
		m.status = strings.Replace(err.Error(), "/product-status", "/product-approve", 1)
		return false
	}
	return m.applyProductWorkflowTransition(absRoot, "approve_for_implementation")
}

func (m *Model) slashProductContinue(_ string, fields []string) bool {
	action, rootFields, err := productContinueArgs(fields)
	if err != nil {
		m.status = err.Error()
		return false
	}
	absRoot, err := m.productStatusEffortRoot(rootFields)
	if err != nil {
		m.status = strings.Replace(err.Error(), "/product-status", "/product-continue", 1)
		return false
	}
	return m.applyProductWorkflowTransition(absRoot, action)
}

func (m *Model) logProductWorkflowCommand(fields []string) {
	command := strings.TrimSpace(strings.Join(fields, " "))
	if command != "" {
		m.logMessage("user", command)
	}
}

func productContinueArgs(fields []string) (string, []string, error) {
	if len(fields) < 2 {
		return "", nil, errors.New("Usage: /product-continue <action> [--effort-root <path>|<path>]")
	}
	action := ""
	rootFields := []string{fields[0]}
	for i := 1; i < len(fields); i++ {
		field := strings.TrimSpace(fields[i])
		if field == "" {
			continue
		}
		if field == "--effort-root" {
			if i+1 >= len(fields) {
				return "", nil, errors.New("Usage: /product-continue <action> [--effort-root <path>|<path>]")
			}
			rootFields = append(rootFields, "--effort-root", fields[i+1])
			i++
			continue
		}
		if action == "" {
			action = field
			continue
		}
		if len(rootFields) == 1 {
			rootFields = append(rootFields, field)
			continue
		}
		return "", nil, errors.New("Usage: /product-continue <action> [--effort-root <path>|<path>]")
	}
	if action == "" {
		return "", nil, errors.New("Usage: /product-continue <action> [--effort-root <path>|<path>]")
	}
	return action, rootFields, nil
}

func (m *Model) applyProductWorkflowTransition(absRoot string, action string) bool {
	result := productworkflow.ApplyProductWorkflowTransition(productworkflow.ProductWorkflowTransitionApplyOptions{
		EffortRoot: absRoot,
		Action:     action,
		Actor:      "operator",
	})
	m.logProductWorkflowEvent("idiom.product_workflow.transition", map[string]any{
		"status":            result.Status,
		"effort_root":       absRoot,
		"action":            result.Action,
		"from_state":        result.FromState,
		"to_state":          result.ToState,
		"next_valid_action": result.NextValidAction,
		"blocker_count":     len(result.Blockers),
	})
	body := productWorkflowTransitionTUIMessage(result)
	if result.Status == "applied" {
		m.logProductWorkflowEvent("idiom.product_workflow.transition_applied", map[string]any{
			"status":      result.Status,
			"effort_root": absRoot,
			"action":      result.Action,
			"from_state":  result.FromState,
			"to_state":    result.ToState,
		})
		statusView, workflowState, statusErr := productworkflow.LoadCheckedProductStatus(absRoot)
		operatorStatus := productworkflow.BuildProductOperatorStatusView(statusView, workflowState)
		body = strings.TrimRight(body, "\n") + "\n\n" + strings.TrimRight(productworkflow.FormatProductOperatorStatusText(filepath.Base(absRoot), operatorStatus), "\n")
		if statusErr != nil {
			body = strings.TrimRight(body, "\n") + "\nstatus_reload_error: " + statusErr.Error()
		}
	}
	m.insertBeforePendingAssistant(llama.ChatMessage{
		Role:    "operator",
		Content: strings.TrimRight(body, "\n"),
	})
	displayRoot := m.displayPath(absRoot)
	if result.Status != "applied" {
		m.status = fmt.Sprintf("Product workflow transition blocked for %s: %d blocker(s).", displayRoot, len(result.Blockers))
		return false
	}
	m.status = fmt.Sprintf("Product workflow transition applied for %s: %s -> %s.", displayRoot, result.FromState, result.ToState)
	return false
}

func (m *Model) logProductWorkflowEvent(role string, fields map[string]any) {
	if body, err := json.Marshal(fields); err == nil {
		m.logMessage(role, string(body))
	}
}

func productWorkflowTransitionTUIMessage(result productworkflow.ProductWorkflowTransitionApplyResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "product workflow transition: %s\n", result.Status)
	fmt.Fprintf(&b, "  action: %s\n", result.Action)
	if result.FromState != "" || result.ToState != "" {
		fmt.Fprintf(&b, "  state: %s -> %s\n", result.FromState, result.ToState)
	}
	if result.Event.EventID != "" {
		fmt.Fprintf(&b, "  event_id: %s\n", result.Event.EventID)
	}
	if result.NextValidAction != "" {
		fmt.Fprintf(&b, "  next: %s\n", result.NextValidAction)
	}
	if len(result.Blockers) > 0 {
		fmt.Fprintf(&b, "  blockers (%d):\n", len(result.Blockers))
		for _, blocker := range result.Blockers {
			if blocker.Path != "" {
				fmt.Fprintf(&b, "    [%s] %s: %s\n", blocker.Code, blocker.Path, blocker.Message)
			} else {
				fmt.Fprintf(&b, "    [%s] %s\n", blocker.Code, blocker.Message)
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *Model) productStatusEffortRoot(fields []string) (string, error) {
	switch len(fields) {
	case 1:
		absRoot, err := m.productStatusAbsPath(".")
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(filepath.Join(absRoot, "workflow_state.json")); err != nil {
			return "", errors.New("usage: /product-status --effort-root <path> (or run from an effort root)")
		}
		return absRoot, nil
	case 2:
		if fields[1] == "--effort-root" {
			return "", errors.New("Usage: /product-status [--effort-root <path>|<path>]")
		}
		return m.productStatusAbsPath(fields[1])
	case 3:
		if fields[1] != "--effort-root" {
			return "", errors.New("Usage: /product-status [--effort-root <path>|<path>]")
		}
		return m.productStatusAbsPath(fields[2])
	default:
		return "", errors.New("Usage: /product-status [--effort-root <path>|<path>]")
	}
}

func (m *Model) productStatusAbsPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "."
	}
	base := ""
	if m != nil && m.workspace != nil && strings.TrimSpace(m.workspace.CWD()) != "" {
		base = m.workspace.CWD()
	} else if cwd, err := os.Getwd(); err == nil {
		base = cwd
	}
	if !filepath.IsAbs(path) && base != "" {
		path = filepath.Join(base, path)
	}
	absRoot, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("product status: resolve effort root: %w", err)
	}
	return filepath.Clean(absRoot), nil
}

func (m *Model) productStatusTUIRecoveryHint(effortRoot string, view productworkflow.ProductOperatorStatusView) string {
	if view.Recovery == nil {
		return ""
	}
	packetPath := strings.TrimSpace(view.Recovery.PacketPath)
	if packetPath == "" || packetPath == "outside_effort_root" {
		return ""
	}
	if !filepath.IsAbs(packetPath) {
		packetPath = filepath.Join(effortRoot, filepath.FromSlash(packetPath))
	}
	if m != nil && m.workspace != nil && strings.TrimSpace(m.workspace.Root()) != "" {
		root := filepath.Clean(m.workspace.Root())
		candidate := filepath.Clean(packetPath)
		if resolvedRoot, err := filepath.EvalSymlinks(root); err == nil {
			root = resolvedRoot
		}
		if resolvedCandidate, err := filepath.EvalSymlinks(candidate); err == nil {
			candidate = resolvedCandidate
		}
		if rel, err := filepath.Rel(root, candidate); err == nil &&
			rel != "." &&
			rel != ".." &&
			!strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "/recover-status --packet " + filepath.ToSlash(rel)
		}
	}
	return "/recover-status --packet " + m.displayPath(packetPath)
}
