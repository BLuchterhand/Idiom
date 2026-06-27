// Package readapproval defines stable decision vocabulary for harness
// read-approval gating.
package readapproval

import (
	"errors"
	"time"
)

// Outcome is the normalized decision class for a read request.
type Outcome int

const (
	// OutcomeApproved allows the read without further operator interaction.
	OutcomeApproved Outcome = iota
	// OutcomePromptRequired means the caller must ask the operator before reading.
	OutcomePromptRequired
	// OutcomeDenied rejects the read.
	OutcomeDenied
)

func (o Outcome) String() string {
	switch o {
	case OutcomeApproved:
		return "approved"
	case OutcomePromptRequired:
		return "prompt_required"
	case OutcomeDenied:
		return "denied"
	default:
		return "unknown"
	}
}

const (
	SourceCache                 = "cache"
	SourceOperator              = "operator"
	SourceFlag                  = "flag"
	SourceHeadlessDefault       = "headless_default"
	SourcePromptBudgetExhausted = "prompt_budget_exhausted"
	SourceNoDecider             = "no_decider"
)

const (
	ReasonNotSubjectToGate       = "not_subject_to_gate"
	ReasonOperatorApprovedCache  = "operator_approved_cache"
	ReasonOperatorDeniedCache    = "operator_denied_cache"
	ReasonAllowedEditPath        = "allowed_edit_path"
	ReasonRequiredEditPath       = "required_edit_path"
	ReasonContextDocUnderCap     = "context_doc_under_cap"
	ReasonPriorReadPath          = "prior_read_path"
	ReasonPromptRequired         = "prompt_required"
	ReasonOperatorApprovedLive   = "operator_approved_live"
	ReasonOperatorDeniedLive     = "operator_denied_live"
	ReasonOperatorDeniedFlag     = "operator_denied_flag"
	ReasonHeadlessDefault        = "headless_default"
	ReasonPromptBudgetExhausted  = "prompt_budget_exhausted"
	ReasonNoDeciderWired         = "no_decider_wired"
	ReasonCacheUnavailable       = "cache_unavailable"
	ReasonContextDocCapExhausted = "context_doc_cap_exhausted"
	ReasonClassifierDenied       = "classifier_denied"
)

const (
	// EventReadApprovalRequested is emitted when the harness asks an operator for read approval.
	EventReadApprovalRequested = "read_approval_requested"
	// EventReadApprovalDecision is emitted when a read approval decision is recorded.
	EventReadApprovalDecision = "read_approval_decision"
	// EventOriginCacheLookup marks decisions emitted from a cache lookup.
	EventOriginCacheLookup = "cache_lookup"
	// EventOriginLiveDecision marks decisions emitted from a live decider result.
	EventOriginLiveDecision = "live_decision"
	// EventOriginCacheRecord marks a live decision rejected because it could not be durably recorded.
	EventOriginCacheRecord = "cache_record"
)

// Classification describes a policy-level read decision before it is persisted.
type Classification struct {
	Outcome Outcome
	Reason  string
	Detail  string
}

// Decision is the persisted result of resolving a read-approval request.
type Decision struct {
	Outcome Outcome
	Reason  string
	Source  string
	At      time.Time
	Path    string
}

// Request contains the context needed to classify or prompt for a read.
type Request struct {
	SessionID string
	Path      string
	Tool      string
	Lane      string
}

// ErrNoDecider reports that no interactive approval path is available.
var ErrNoDecider = errors.New("readapproval: no decider wired")

// ClassificationOptions supplies host-specific policy hooks for read gating.
type ClassificationOptions struct {
	ContextDocCap        int
	IsContextDoc         func(path string) bool
	CountContextDocReads func(results []ToolResultRef) int
	IsReadTool           func(tool string) bool
	NormalizePath        func(path string) string
}

// ToolResultRef summarizes a previous tool result relevant to read policy.
type ToolResultRef struct {
	Tool string
	Path string
}

// SessionDecisions contains approved and denied decisions for one session.
type SessionDecisions struct {
	Approved map[string]Decision
	Denied   map[string]Decision
}

// NewSessionDecisions returns an initialized decision set.
func NewSessionDecisions() SessionDecisions {
	return SessionDecisions{
		Approved: make(map[string]Decision),
		Denied:   make(map[string]Decision),
	}
}

// Lookup returns a prior approved or denied decision for path.
func (s SessionDecisions) Lookup(path string) (Decision, bool) {
	if d, ok := s.Approved[path]; ok {
		return d, true
	}
	if d, ok := s.Denied[path]; ok {
		return d, true
	}
	return Decision{}, false
}

// Record stores d under its path and replaces any opposite outcome.
func (s *SessionDecisions) Record(d Decision) {
	if d.Path == "" {
		return
	}
	switch d.Outcome {
	case OutcomeApproved:
		s.Approved[d.Path] = d
		delete(s.Denied, d.Path)
	case OutcomeDenied:
		s.Denied[d.Path] = d
		delete(s.Approved, d.Path)
	}
}

// ApprovedPathSet returns a set of approved paths.
func (s *SessionDecisions) ApprovedPathSet() map[string]struct{} {
	out := make(map[string]struct{}, len(s.Approved))
	for p := range s.Approved {
		out[p] = struct{}{}
	}
	return out
}

// DeniedPathSet returns a set of denied paths.
func (s *SessionDecisions) DeniedPathSet() map[string]struct{} {
	out := make(map[string]struct{}, len(s.Denied))
	for p := range s.Denied {
		out[p] = struct{}{}
	}
	return out
}
