package app

import (
	"time"

	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/runtime/sessionlog"

	"github.com/charmbracelet/bubbles/viewport"

	"idiom/internal/capabilities/workspace"
)

// planRuntimeState is cloned with a detached prompt turn and projected back
// as one execution-owned value when that turn completes.
type planRuntimeState struct {
	sessionHeadingWritten bool
	ledgerItems           map[string]workspace.TaskLedgerItem
	ledgerRenderedIDs     map[string]bool
	ledgerHits            map[string]map[string]bool
	submittedEdits        map[string]planartifact.PlanEdit
}

// planUIState remains exclusively owned by Bubble Tea's Update loop.
type planUIState struct {
	cursor            int
	overlayID         string
	overlayBody       string
	autoShown         bool
	collapsedHeadings map[string]bool
	renderedRows      []planRenderedRow
}

type docViewerState struct {
	viewport      viewport.Model
	cursor        int
	expanded      map[int]bool
	artifactPath  string
	artifactMTime time.Time
	artifact      planartifact.PlanArtifact
	loadError     string
	loaded        bool
}

type sessionsState struct {
	viewport      viewport.Model
	list          []sessionlog.SessionInfo
	rawCache      []sessionlog.SessionInfo
	rawCacheDir   string
	rawCacheMTime time.Time
	rawCacheValid bool
	summaryCache  map[string]sessionSummaryCacheEntry
	cursor        int
}

type historyState struct {
	entries            []historyEntry
	revision           uint64
	selected           int
	expanded           bool
	panelOpen          bool
	focused            bool
	viewport           viewport.Model
	renderPending      bool
	renderLast         time.Time
	renderScrollBottom bool
}
