package plan

import "strings"

type ScopePathRole string

const (
	ScopePathRoleMutationTarget     ScopePathRole = "mutation_target"
	ScopePathRoleReferenceDocument  ScopePathRole = "reference_document"
	ScopePathRoleReadOnlyEvidence   ScopePathRole = "read_only_evidence"
	ScopePathRoleCandidateExpansion ScopePathRole = "candidate_expansion"
	ScopePathRoleForbidden          ScopePathRole = "forbidden"
)

type ScopeExpansionPolicy string

const (
	ScopeExpansionPolicyClosed               ScopeExpansionPolicy = "closed"
	ScopeExpansionPolicyBoundedDiscovery     ScopeExpansionPolicy = "bounded_discovery"
	ScopeExpansionPolicyRequiresUserApproval ScopeExpansionPolicy = "requires_user_approval"
)

type ScopeExpansionEventKind string

const (
	ScopeExpansionEventProposalCreated  ScopeExpansionEventKind = "proposal_created"
	ScopeExpansionEventProposalApproved ScopeExpansionEventKind = "proposal_approved"
	ScopeExpansionEventProposalRejected ScopeExpansionEventKind = "proposal_rejected"
	ScopeExpansionEventExpansionBlocked ScopeExpansionEventKind = "scope_expansion_blocked"
)

type ScopeExpansionPath struct {
	Path   string        `json:"path"`
	Role   ScopePathRole `json:"role"`
	Reason string        `json:"reason,omitempty"`
	Source string        `json:"source,omitempty"`
}

type ScopeExpansionProposal struct {
	ID     string                `json:"id,omitempty"`
	Policy ScopeExpansionPolicy  `json:"policy"`
	Paths  []ScopeExpansionPath  `json:"paths,omitempty"`
	Reason string                `json:"reason,omitempty"`
	Source string                `json:"source,omitempty"`
	Events []ScopeExpansionEvent `json:"events,omitempty"`
}

type ScopeExpansionEvent struct {
	Kind       ScopeExpansionEventKind `json:"kind"`
	ProposalID string                  `json:"proposal_id,omitempty"`
	Policy     ScopeExpansionPolicy    `json:"policy,omitempty"`
	Paths      []ScopeExpansionPath    `json:"paths,omitempty"`
	Reason     string                  `json:"reason,omitempty"`
	Source     string                  `json:"source,omitempty"`
}

func (path ScopeExpansionPath) Normalized() ScopeExpansionPath {
	path.Path = NormalizeScopePath(path.Path)
	path.Role = NormalizeScopePathRole(path.Role)
	path.Reason = strings.TrimSpace(path.Reason)
	path.Source = strings.TrimSpace(path.Source)

	if path.Path == "" || path.Role == "" {
		return ScopeExpansionPath{}
	}

	return path
}

func (proposal ScopeExpansionProposal) Normalized() ScopeExpansionProposal {
	proposal.ID = strings.TrimSpace(proposal.ID)
	proposal.Policy = NormalizeScopeExpansionPolicy(proposal.Policy)
	proposal.Paths = NormalizeScopeExpansionPaths(proposal.Paths)
	proposal.Reason = strings.TrimSpace(proposal.Reason)
	proposal.Source = strings.TrimSpace(proposal.Source)

	events := make([]ScopeExpansionEvent, 0, len(proposal.Events))
	for _, event := range proposal.Events {
		event = event.Normalized()
		if event.Kind == "" {
			continue
		}

		events = append(events, event)
	}
	proposal.Events = events

	return proposal
}

func (event ScopeExpansionEvent) Normalized() ScopeExpansionEvent {
	event.Kind = NormalizeScopeExpansionEventKind(event.Kind)
	event.ProposalID = strings.TrimSpace(event.ProposalID)
	event.Policy = NormalizeScopeExpansionPolicy(event.Policy)
	event.Paths = NormalizeScopeExpansionPaths(event.Paths)
	event.Reason = strings.TrimSpace(event.Reason)
	event.Source = strings.TrimSpace(event.Source)

	return event
}

func NormalizeScopeExpansionPaths(paths []ScopeExpansionPath) []ScopeExpansionPath {
	out := make([]ScopeExpansionPath, 0, len(paths))
	seen := make(map[scopeExpansionPathKey]struct{}, len(paths))

	for _, path := range paths {
		path = path.Normalized()
		if path.Path == "" {
			continue
		}

		key := scopeExpansionPathKey{path: path.Path, role: path.Role}
		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}
		out = append(out, path)
	}

	return out
}

func NormalizeScopePathRole(role ScopePathRole) ScopePathRole {
	switch ScopePathRole(normalizeScopeExpansionToken(string(role))) {
	case ScopePathRoleMutationTarget:
		return ScopePathRoleMutationTarget
	case ScopePathRoleReferenceDocument:
		return ScopePathRoleReferenceDocument
	case ScopePathRoleReadOnlyEvidence:
		return ScopePathRoleReadOnlyEvidence
	case ScopePathRoleCandidateExpansion:
		return ScopePathRoleCandidateExpansion
	case ScopePathRoleForbidden:
		return ScopePathRoleForbidden
	default:
		return ""
	}
}

func NormalizeScopeExpansionPolicy(policy ScopeExpansionPolicy) ScopeExpansionPolicy {
	switch ScopeExpansionPolicy(normalizeScopeExpansionToken(string(policy))) {
	case ScopeExpansionPolicyClosed:
		return ScopeExpansionPolicyClosed
	case ScopeExpansionPolicyBoundedDiscovery:
		return ScopeExpansionPolicyBoundedDiscovery
	case ScopeExpansionPolicyRequiresUserApproval:
		return ScopeExpansionPolicyRequiresUserApproval
	default:
		return ""
	}
}

func NormalizeScopeExpansionEventKind(kind ScopeExpansionEventKind) ScopeExpansionEventKind {
	switch ScopeExpansionEventKind(normalizeScopeExpansionToken(string(kind))) {
	case ScopeExpansionEventProposalCreated, "proposal":
		return ScopeExpansionEventProposalCreated
	case ScopeExpansionEventProposalApproved, "approval":
		return ScopeExpansionEventProposalApproved
	case ScopeExpansionEventProposalRejected, "rejection":
		return ScopeExpansionEventProposalRejected
	case ScopeExpansionEventExpansionBlocked, "block":
		return ScopeExpansionEventExpansionBlocked
	default:
		return ""
	}
}

type scopeExpansionPathKey struct {
	path string
	role ScopePathRole
}

func normalizeScopeExpansionToken(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
