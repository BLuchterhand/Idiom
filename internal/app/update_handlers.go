package app

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"idiom/internal/llama"
	"idiom/internal/util"
)

func (m *Model) handleWindowSize(msg tea.WindowSizeMsg) (tea.Cmd, bool) {
	m.width = msg.Width
	m.height = msg.Height
	m.resize()
	return nil, false
}

func (m *Model) handleKeyPress(msg tea.KeyMsg) (tea.Cmd, bool) {

	// Slash-command suggestion navigation takes priority when the popup is visible.
	if m.slashSuggestions.visible {
		switch msg.Type {
		case tea.KeyEsc:
			m.slashSuggestions = slashSuggestionsState{}
			return m.waitForEventCmd(), true
		case tea.KeyTab, tea.KeyEnter:
			if m.slashSuggestions.selected >= 0 && m.slashSuggestions.selected < len(m.slashSuggestions.matches) {
				sel := m.slashSuggestions.matches[m.slashSuggestions.selected].command
				// Preserve any text on prior lines (multi-line input).
				oldVal := m.input.Value()
				if idx := strings.LastIndex(oldVal, "\n"); idx >= 0 {
					m.input.SetValue(oldVal[:idx+1] + sel + " ")
				} else {
					m.input.SetValue(sel + " ")
				}
				m.input.CursorEnd()
			}
			m.slashSuggestions = slashSuggestionsState{}
			return m.waitForEventCmd(), true
		case tea.KeyUp:
			if m.slashSuggestions.selected > 0 {
				m.slashSuggestions.selected--
			}
			return m.waitForEventCmd(), true
		case tea.KeyDown:
			if m.slashSuggestions.selected < len(m.slashSuggestions.matches)-1 {
				m.slashSuggestions.selected++
			}
			return m.waitForEventCmd(), true
		}
	}

	if m.history.panelOpen && m.history.focused && m.panelTab == panelTabPlan {
		if handled, cmd := m.handlePlanPanelKey(msg); handled {
			return cmd, true
		}
	}
	if m.history.panelOpen && m.history.focused && m.panelTab == panelTabSessions {
		if handled, cmd := m.handleSessionsPanelKey(msg); handled {
			return cmd, true
		}
	}
	if m.history.panelOpen && m.history.focused {
		switch msg.Type {
		case tea.KeyUp:
			if m.history.selected > 0 {
				m.history.selected--
				m.history.expanded = false
				m.renderHistoryPanelContent()
			}
			return m.waitForEventCmd(), true
		case tea.KeyDown:
			if m.history.selected < len(m.history.entries)-1 {
				m.history.selected++
				m.history.expanded = false
				m.renderHistoryPanelContent()
			}
			return m.waitForEventCmd(), true
		case tea.KeyEnter:
			m.history.expanded = !m.history.expanded
			m.renderHistoryPanelContent()
			return m.waitForEventCmd(), true
		case tea.KeyEsc:
			m.history.focused = false
			m.renderHistoryPanelContent()
			return m.waitForEventCmd(), true
		case tea.KeyPgUp, tea.KeyPgDown:
			m.history.viewport, _ = m.history.viewport.Update(msg)
			return m.waitForEventCmd(), true
		case tea.KeyRunes:
			switch string(msg.Runes) {
			case "j":
				m.history.viewport.ScrollDown(1)
				return m.waitForEventCmd(), true
			case "k":
				m.history.viewport.ScrollUp(1)
				return m.waitForEventCmd(), true
			}
		}
	}
	switch msg.Type {
	case tea.KeyCtrlC:
		return tea.Quit, true
	case tea.KeyCtrlS:
		m.saveTranscriptAsync()
	case tea.KeyCtrlL:
		m.messages = nil
		m.status = "Cleared chat."
		m.renderMessages(true)
	case tea.KeyCtrlR:
		m.restartServerAsync()
	case tea.KeyCtrlB:
		if !m.history.panelOpen {
			m.panelTab = panelTabHistory
		}
		m.setHistoryPanelOpen(!m.history.panelOpen)
		if m.history.panelOpen {
			m.status = "History panel open. Tab focuses it. Ctrl+T cycles tabs."
		} else {
			m.status = "Panel closed."
		}
		return m.waitForEventCmd(), true
	case tea.KeyCtrlT:
		if !m.history.panelOpen {
			m.setHistoryPanelOpen(true)
		}
		m.panelTab = (m.panelTab + 1) % panelTabCount
		if m.panelTab == panelTabSessions {
			m.refreshSessionsList()
		}
		m.rerenderActivePanel()
		return m.waitForEventCmd(), true
	case tea.KeyTab:
		if m.history.panelOpen {
			m.history.focused = !m.history.focused
			m.rerenderActivePanel()
		}
		return m.waitForEventCmd(), true
	case tea.KeyEnter:
		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			break
		}
		m.input.SetValue("")
		if strings.HasPrefix(text, "/") {
			if !m.handleFeaturePlannerCommand(text) && !m.handlePlanExecuteCommand(text) && !m.handleImplementCommand(text) {
				if quit := m.handleSlashCommand(text); quit {
					return tea.Quit, true
				}
			}
			if m.pendingExec != nil {
				cmd := m.pendingExec
				m.pendingExec = nil
				m.renderMessages(true)
				return cmd, true
			}
		} else if !m.streaming {
			m.appendHistoryMessage("user", text, nil)
			m.sendPromptAsync(text)
		}
		m.renderMessages(true)
	}
	return nil, false
}

func (m *Model) handlePlanExecuteCommand(raw string) bool {
	fields := strings.Fields(raw)
	if len(fields) == 0 || fields[0] != "/plan-execute" {
		return false
	}
	objective := strings.TrimSpace(strings.TrimPrefix(raw, "/plan-execute"))
	if objective == "" {
		m.status = "Usage: /plan-execute <objective>"
		return true
	}
	if m.streaming {
		m.status = "A response is already in progress."
		return true
	}
	m.appendHistoryMessage("user", objective, nil)
	m.sendPlanExecuteCommandAsync(objective)
	m.status = "Dispatching to plan-execute..."
	return true
}

func (m *Model) handleFeaturePlannerCommand(raw string) bool {
	fields := strings.Fields(raw)
	if len(fields) == 0 || fields[0] != "/feature-planner" {
		return false
	}
	objective := strings.TrimSpace(strings.TrimPrefix(raw, "/feature-planner"))
	if objective == "" {
		m.status = "Usage: /feature-planner <objective>"
		return true
	}
	if m.streaming {
		m.status = "A response is already in progress."
		return true
	}
	m.appendHistoryMessage("user", objective, nil)
	m.sendFeaturePlannerCommandAsync(objective)
	m.status = "Dispatching to feature-planner..."
	return true
}

func (m *Model) handleImplementCommand(raw string) bool {
	fields := strings.Fields(raw)
	if len(fields) == 0 || fields[0] != "/implement" {
		return false
	}
	objective := strings.TrimSpace(strings.TrimPrefix(raw, "/implement"))
	if objective == "" {
		m.status = "Usage: /implement <objective>"
		return true
	}
	if m.streaming {
		m.status = "A response is already in progress."
		return true
	}
	m.appendHistoryMessage("user", objective, nil)
	m.sendImplementCommandAsync(objective)
	m.status = "Dispatching to implement..."
	return true
}

func (m *Model) handleMouse(msg tea.MouseMsg) (tea.Cmd, bool) {
	if m.history.panelOpen && msg.X >= m.viewport.Width+1 {
		m.history.viewport, _ = m.history.viewport.Update(msg)
	} else {
		m.updateViewport(msg)
	}
	return m.waitForEventCmd(), true
}

func (m *Model) handleSpinnerTick(msg spinner.TickMsg) (tea.Cmd, bool) {
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	if m.streaming {
		m.workingFrame = (m.workingFrame + 1) % 4
		m.renderMessages(false)
	}
	return cmd, false
}

func (m *Model) handleServerStatus(msg serverStatusMsg) (tea.Cmd, bool) {
	m.bootingServer = false
	m.logServerEvent(msg)
	if msg.err != nil {
		m.serverUp = false
		m.setRuntimeError("Server error: " + msg.err.Error())
	} else {
		m.serverUp = msg.running
		if !msg.running {
			m.modelID = ""
			m.modelDisplayID = ""
		}
		m.modelID = msg.modelID
		m.modelDisplayID = util.Fallback(strings.TrimSpace(msg.displayID), strings.TrimSpace(msg.modelID))
		m.managedServer = m.managedServer || msg.owned
		m.clearRuntimeErrorIfServerRelated()
		if msg.running {
			m.status = fmt.Sprintf("Server ready on %s. Model: %s", m.plannerSpec.EndpointBaseURL(), m.activeModelDisplayID())
		} else {
			m.status = "Server stopped."
		}
	}
	return nil, false
}

func (m *Model) handleStreamDone(msg streamDoneMsg) (tea.Cmd, bool) {
	m.streaming = false
	m.workingFrame = 0
	m.planPhaseText = ""
	m.currentTurnBusyStatus = ""
	if msg.err != nil {
		m.failAgentHistoryTurn(msg.err)
		m.setRuntimeError("Chat failed: " + msg.err.Error())
	} else {
		m.clearRuntimeError()
		m.status = "Response complete."
		m.recordAgentTurn()
	}
	m.renderMessages(false)
	return nil, false
}

func (m *Model) handleTranscriptSaved(msg transcriptSavedMsg) (tea.Cmd, bool) {
	if msg.err != nil {
		m.setRuntimeError("Save failed: " + msg.err.Error())
	} else {
		m.clearRuntimeError()
		m.status = "Transcript saved to " + m.displayPath(msg.path)
		m.logArtifact("transcript", msg.path)
	}
	return nil, false
}

func (m *Model) handleInfoMessage(msg infoMsg) (tea.Cmd, bool) {
	m.status = msg.text
	return nil, false
}

func (m *Model) handleToolTrace(msg toolTraceMsg) (tea.Cmd, bool) {
	if strings.TrimSpace(msg.busyStatus) != "" {
		m.currentTurnBusyStatus = msg.busyStatus
	}
	if m.traceMode == traceDisplayDetail && !msg.running {
		m.insertBeforePendingAssistant(llama.ChatMessage{Role: "tool", Content: msg.text})
		m.logMessage("tool", msg.text)
	}
	m.recordToolTraceInHistory(msg.step, msg.text, msg.intent, msg.running, msg.auditKind, msg.outcome, msg.auditEvents)
	m.renderMessages(false)
	return nil, false
}

func (m *Model) handleHistoryAudit(msg historyAuditMsg) (tea.Cmd, bool) {
	m.recordHistoryAuditEvent(msg.event)
	return nil, false
}

func (m *Model) handleHistorySummary(msg historySummaryMsg) (tea.Cmd, bool) {
	if msg.index >= 0 && msg.index < len(m.history.entries) {
		m.history.entries[msg.index].summary = msg.summary
		m.requestHistoryPanelRender(false, false)
	}
	return nil, false
}

func (m *Model) handleHistoryPanelRefresh(msg historyPanelRefreshMsg) (tea.Cmd, bool) {
	if m.history.renderPending {
		m.flushHistoryPanelRender()
	}
	return nil, false
}

func (m *Model) handlePlanEditorDone(msg planEditorDoneMsg) (tea.Cmd, bool) {
	if msg.err != nil {
		m.status = "Editor exited with error: " + msg.err.Error()
	} else {
		m.status = "Plan updated: " + m.displayPath(m.planPath)
	}
	m.renderPlanPanelContent()
	return nil, false
}

func (m *Model) handlePlanRefresh(msg planRefreshMsg) (tea.Cmd, bool) {
	if !m.history.panelOpen && !m.plan.autoShown {
		if m.planPanelAutoShowNeeded() {
			m.panelTab = panelTabPlan
			m.setHistoryPanelOpen(true)
			m.plan.autoShown = true
		}
	}
	m.renderPlanPanelContent()
	return nil, false
}

func (m *Model) handlePlanPhase(msg planPhaseMsg) (tea.Cmd, bool) {
	if msg.phase == "complete" {
		m.planPhaseText = ""
	} else {
		m.planPhaseText = formatPlanPhaseText(msg.phase, msg.detail)
	}
	m.recordHistoryAuditEvent(historyAuditEvent{kind: "lane", label: "plan-execute " + strings.TrimSpace(msg.phase), detail: msg.detail, status: msg.phase})
	m.renderMessages(false)
	return nil, false
}
