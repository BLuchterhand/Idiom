package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"idiom/internal/capabilities/workspace"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	rollbackUnavailableEvidence = "workspace did not provide exact rollback evidence"
	rollbackUnattributed        = "committed mutation has no attributed execution session"
)

// rollbackEntry retains only workspace-owned reverse-mutation authority and
// its immutable execution identity. Preimage bodies, roots, inode evidence,
// and digests remain private to workspace.
type rollbackEntry struct {
	id                uint64
	rollback          *workspace.MutationRollback
	sessionID         string
	paths             []string
	unavailableReason string
}

// rollbackRegistry is the cancellation-safe owner of undo capabilities shared
// by the live Model and each detached prompt session. A worker records a
// committed mutation here before its completion projection, so canceling the
// turn cannot discard rollback authority while the worker drains.
type rollbackRegistry struct {
	mu       sync.Mutex
	nextID   uint64
	activeID uint64
	entries  []rollbackEntry
}

func newRollbackRegistry(entries []rollbackEntry) *rollbackRegistry {
	registry := &rollbackRegistry{}
	for _, entry := range entries {
		registry.appendLocked(entry)
	}

	return registry
}

func (r *rollbackRegistry) append(entry rollbackEntry) rollbackEntry {
	if r == nil {
		return rollbackEntry{}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	return r.appendLocked(entry)
}

func (r *rollbackRegistry) appendLocked(entry rollbackEntry) rollbackEntry {
	r.nextID++
	entry.id = r.nextID
	entry.paths = append([]string(nil), entry.paths...)
	r.entries = append(r.entries, entry)

	return entry
}

func (r *rollbackRegistry) snapshot() []rollbackEntry {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	return cloneRollbackHistory(r.entries)
}

func (r *rollbackRegistry) beginLatest() (rollbackEntry, error) {
	if r == nil {
		return rollbackEntry{}, fmt.Errorf("no rollback history — nothing to undo")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.activeID != 0 {
		return rollbackEntry{}, fmt.Errorf("an undo operation is already in progress")
	}
	if len(r.entries) == 0 {
		return rollbackEntry{}, fmt.Errorf("no rollback history — nothing to undo")
	}

	entry := r.entries[len(r.entries)-1]
	if entry.rollback == nil || strings.TrimSpace(entry.unavailableReason) != "" {
		reason := strings.TrimSpace(entry.unavailableReason)
		if reason == "" {
			reason = rollbackUnavailableEvidence
		}

		return rollbackEntry{}, fmt.Errorf("latest committed mutation cannot be undone: %s", reason)
	}

	r.activeID = entry.id

	return entry, nil
}

func (r *rollbackRegistry) finish(entryID uint64, consume bool) {
	if r == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.activeID != entryID {
		return
	}
	r.activeID = 0
	if !consume {
		return
	}

	for index := range r.entries {
		if r.entries[index].id != entryID {
			continue
		}
		r.entries = append(r.entries[:index], r.entries[index+1:]...)

		return
	}
}

func (m *Model) ensureRollbackRegistry() *rollbackRegistry {
	if m == nil {
		return nil
	}
	if m.rollbackRegistry == nil {
		m.rollbackRegistry = newRollbackRegistry(m.rollbackHistory)
	}

	return m.rollbackRegistry
}

func (m *promptExecutionSession) ensureRollbackRegistry() *rollbackRegistry {
	if m == nil {
		return nil
	}
	if m.rollbackRegistry == nil {
		m.rollbackRegistry = newRollbackRegistry(m.rollbackHistory)
	}

	return m.rollbackRegistry
}

// recordMutationRollback projects one successful or partially committed tool
// result into exactly one undo entry. It binds the actual execution context,
// including nested plan-execute identity, rather than the outer session
// snapshot. Planned and failed-before-commit targets never acquire authority.
func (m *promptExecutionSession) recordMutationRollback(executionCtx context.Context, result workspace.ToolResult) {
	if m == nil || m.workspace == nil {
		return
	}
	receipt := result.CommitReceipt()
	if receipt == nil || len(receipt.ChangedPaths()) == 0 {
		return
	}

	paths := receipt.ChangedPaths()
	sessionID, bound := workspace.ExecutionSessionIDFromContext(executionCtx)
	sessionID = strings.TrimSpace(sessionID)
	if !bound || sessionID == "" {
		m.recordUnavailableMutationRollback(paths, rollbackUnattributed)

		return
	}

	rollback, err := receipt.Rollback()
	if err != nil || rollback == nil {
		m.recordUnavailableMutationRollback(paths, rollbackUnavailableEvidence)

		return
	}

	registry := m.ensureRollbackRegistry()
	registry.append(rollbackEntry{
		rollback:  rollback,
		sessionID: sessionID,
		paths:     rollback.Paths(),
	})
	m.rollbackHistory = registry.snapshot()
}

func (m *promptExecutionSession) recordUnavailableMutationRollback(paths []string, reason string) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = rollbackUnavailableEvidence
	}

	registry := m.ensureRollbackRegistry()
	registry.append(rollbackEntry{
		paths:             append([]string(nil), paths...),
		unavailableReason: reason,
	})
	m.rollbackHistory = registry.snapshot()
	m.logMessage("mutation_rollback_unavailable", reason)
	m.queueUIEvent(infoMsg{text: "Undo unavailable for committed mutation: " + reason})
}

type undoCompletedMsg struct {
	generation uint64
	result     workspace.MutationRollbackResult
	summary    string
	err        error
}

// startUndo begins an undo on the Model-owned operation group. The UI thread
// reserves the latest capability synchronously, then returns without waiting
// for the workspace transaction gate. Closing the program cancels an undo that
// has not committed a reverse effect; partial progress remains resumable.
func (m *Model) startUndo() error {
	if m == nil {
		return fmt.Errorf("no active session")
	}
	if m.workspace == nil {
		return fmt.Errorf("workspace is unavailable")
	}
	if m.events == nil {
		return fmt.Errorf("TUI event stream is unavailable")
	}

	m.undoPresentationGeneration++
	generation := m.undoPresentationGeneration
	registry := m.ensureRollbackRegistry()
	entry, err := registry.beginLatest()
	if err != nil {
		m.rollbackHistory = registry.snapshot()

		return err
	}

	operationGroup := m.ensureBackgroundOperations()
	workspaceManager := m.workspace
	events := m.events
	started := operationGroup.start(func(ctx context.Context) {
		result, summary, operationErr := executeUndo(ctx, workspaceManager, entry)
		registry.finish(entry.id, result.Status == workspace.MutationRollbackComplete)

		select {
		case events <- undoCompletedMsg{generation: generation, result: result, summary: summary, err: operationErr}:
		case <-ctx.Done():
		}
	})
	if !started {
		registry.finish(entry.id, false)

		return fmt.Errorf("TUI is shutting down")
	}

	return nil
}

func executeUndo(ctx context.Context, manager *workspace.Manager, entry rollbackEntry) (workspace.MutationRollbackResult, string, error) {
	if ctx == nil {
		return workspace.MutationRollbackResult{}, "", fmt.Errorf("undo context is required")
	}

	executionCtx := workspace.WithExecutionSessionID(ctx, entry.sessionID)
	result, err := manager.RollbackMutation(executionCtx, entry.rollback)
	if err != nil {
		return result, "", fmt.Errorf("undo workspace mutation: %w", err)
	}
	if result.Status != workspace.MutationRollbackComplete {
		return result, "", fmt.Errorf("undo workspace mutation remained %s", result.Status)
	}

	paths := append([]string(nil), entry.paths...)
	if len(paths) == 0 {
		for _, effect := range result.Effects {
			paths = append(paths, effect.CanonicalID)
		}
	}

	return result, fmt.Sprintf("Undone: restored %s", strings.Join(paths, ", ")), nil
}

func (m *Model) handleUndoCompleted(msg undoCompletedMsg) (tea.Cmd, bool) {
	if m == nil {
		return nil, false
	}

	m.rollbackHistory = m.ensureRollbackRegistry().snapshot()
	if encoded, err := json.Marshal(msg.result); err == nil {
		m.logMessage("mutation_rollback", string(encoded))
	}
	var refreshErr error
	var refreshCmd tea.Cmd
	if len(msg.result.Effects) > 0 {
		refreshErr = m.reloadPromptStackWithProfiles(m.activeProfiles)
		if refreshErr != nil {
			m.setContextError("Prompt context failed after undo: " + refreshErr.Error())
		}
		refreshCmd, _ = m.handlePlanRefresh(planRefreshMsg{})
	}
	if msg.generation != m.undoPresentationGeneration {
		return refreshCmd, false
	}
	if msg.err != nil {
		m.status = "Undo failed: " + msg.err.Error()
	} else if refreshErr != nil {
		m.status = "Undo completed, but prompt context refresh failed: " + refreshErr.Error()
	} else {
		m.status = msg.summary
	}

	return refreshCmd, false
}
