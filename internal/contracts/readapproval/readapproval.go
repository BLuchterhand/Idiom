package readapproval

import (
	"context"
	"errors"
	"time"
)

type Outcome int

const (
	OutcomeApproved Outcome = iota
	OutcomePromptRequired
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
	SourceCache                = "cache"
	SourceOperator             = "operator"
	SourceFlag                 = "flag"
	SourceHeadlessDefault      = "headless_default"
	SourcePromptBudgetExhausted = "prompt_budget_exhausted"
	SourceNoDecider            = "no_decider"
)

const (
	ReasonNotSubjectToGate        = "not_subject_to_gate"
	ReasonOperatorApprovedCache   = "operator_approved_cache"
	ReasonOperatorDeniedCache     = "operator_denied_cache"
	ReasonAllowedEditPath         = "allowed_edit_path"
	ReasonRequiredEditPath        = "required_edit_path"
	ReasonContextDocUnderCap      = "context_doc_under_cap"
	ReasonPriorReadPath           = "prior_read_path"
	ReasonPromptRequired          = "prompt_required"
	ReasonOperatorApprovedLive    = "operator_approved_live"
	ReasonOperatorDeniedLive      = "operator_denied_live"
	ReasonOperatorDeniedFlag      = "operator_denied_flag"
	ReasonHeadlessDefault         = "headless_default"
	ReasonPromptBudgetExhausted   = "prompt_budget_exhausted"
	ReasonNoDeciderWired          = "no_decider_wired"
	ReasonContextDocCapExhausted  = "context_doc_cap_exhausted"
	ReasonClassifierDenied        = "classifier_denied"
)

type Classification struct {
	Outcome Outcome
	Reason  string
	Detail  string
}

type Decision struct {
	Outcome Outcome
	Reason  string
	Source  string
	At      time.Time
	Path    string
}

type Request struct {
	SessionID string
	Path      string
	Tool      string
	Lane      string
	Brief     map[string]any
}

type Decider interface {
	DecideRead(ctx context.Context, req Request) (Decision, error)
}

type Cache interface {
	Lookup(sessionID, path string) (Decision, bool)
	Record(sessionID string, decision Decision) error
	ApprovedPaths(sessionID string) []string
	DeniedPaths(sessionID string) []string
}

var ErrNoDecider = errors.New("readapproval: no decider wired")

type NoOpDecider struct{}

func (NoOpDecider) DecideRead(ctx context.Context, req Request) (Decision, error) {
	return Decision{
		Outcome: OutcomeDenied,
		Reason:  ReasonNoDeciderWired,
		Source:  SourceNoDecider,
		At:      time.Now(),
		Path:    req.Path,
	}, ErrNoDecider
}

type ClassificationOptions struct {
	ContextDocCap        int
	IsContextDoc         func(path string) bool
	CountContextDocReads func(results []ToolResultRef) int
	IsReadTool           func(tool string) bool
	NormalizePath        func(path string) string
	ExtractTargetPath    func(req map[string]any) string
}

type ToolResultRef struct {
	Tool string
	Path string
}

type SessionDecisions struct {
	Approved map[string]Decision
	Denied   map[string]Decision
}

func NewSessionDecisions() SessionDecisions {
	return SessionDecisions{
		Approved: make(map[string]Decision),
		Denied:   make(map[string]Decision),
	}
}

func (s SessionDecisions) Lookup(path string) (Decision, bool) {
	if d, ok := s.Approved[path]; ok {
		return d, true
	}
	if d, ok := s.Denied[path]; ok {
		return d, true
	}
	return Decision{}, false
}

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

func (s *SessionDecisions) ApprovedPathSet() map[string]struct{} {
	out := make(map[string]struct{}, len(s.Approved))
	for p := range s.Approved {
		out[p] = struct{}{}
	}
	return out
}

func (s *SessionDecisions) DeniedPathSet() map[string]struct{} {
	out := make(map[string]struct{}, len(s.Denied))
	for p := range s.Denied {
		out[p] = struct{}{}
	}
	return out
}
