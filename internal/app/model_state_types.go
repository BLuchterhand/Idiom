package app

import (
	"time"

	"github.com/charmbracelet/bubbles/viewport"

	"idiom/internal/workspace"
)

type planState struct {
	writeSnapshots        map[int][]planWriteSnapshot
	sessionHeadingWritten bool
	ledgerItems           map[string]workspace.TaskLedgerItem
	ledgerRenderedIDs     map[string]bool
	ledgerHits            map[string]map[string]bool
	cursor                int
	overlayID             string
	overlayBody           string
	autoShown             bool
	submittedEdits        map[string]workspace.PlanEdit
	cachedItemsContent    string
	cachedItems           []planItemMeta
}

type sessionsState struct {
	viewport      viewport.Model
	list          []SessionInfo
	rawCache      []SessionInfo
	rawCacheDir   string
	rawCacheMTime time.Time
	rawCacheValid bool
	summaryCache  map[string]sessionSummaryCacheEntry
	cursor        int
}

type historyState struct {
	entries            []historyEntry
	selected           int
	expanded           bool
	panelOpen          bool
	focused            bool
	viewport           viewport.Model
	renderPending      bool
	renderLast         time.Time
	renderScrollBottom bool
}
