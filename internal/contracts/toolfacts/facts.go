package toolfacts

import (
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"

	"idiom/internal/contracts/toolpolicy"
)

// DiscoveryStrength describes how strongly a discovery tool localized a source
// owner before any source-body read.
type DiscoveryStrength string

const (
	// DiscoveryStrengthCandidateOnly means the result returned candidate paths
	// but did not establish a source owner.
	DiscoveryStrengthCandidateOnly DiscoveryStrength = "candidate_only"
	// DiscoveryStrengthSourceOwner means the result established a source owner
	// that should be consumed with source-body evidence before broad rediscovery.
	DiscoveryStrengthSourceOwner DiscoveryStrength = "source_owner"
)

// GitHistoryStatus describes producer-owned git-history evidence for a tool
// result. Consumers should not infer this from human-readable summaries.
type GitHistoryStatus string

const (
	// GitHistoryStatusCommitDescriptions means a history tool returned commit
	// descriptions for the scoped path.
	GitHistoryStatusCommitDescriptions GitHistoryStatus = "commit_descriptions"
	// GitHistoryStatusNotApplicable means the scoped path has no applicable
	// committed history, and the reason field explains why.
	GitHistoryStatusNotApplicable GitHistoryStatus = "not_applicable"
)

// SourceWindowAuthorityEvidence preserves the privacy-safe policy authority
// under which a source window was produced. PolicySubject is the normalized
// workspace-relative presentation path, which may differ from CanonicalID for
// a symlink alias. Absolute and traversal-bearing subjects are never durable.
type SourceWindowAuthorityEvidence struct {
	PolicySubject  string              `json:"policy_subject"`
	PolicyDecision toolpolicy.Decision `json:"policy_decision"`
}

// SourceWindowEvidence records durable metadata for one producer-owned,
// revision-bound workspace source window. Exact body content remains on the
// workspace result's non-serialized ephemeral attachment. Authority is
// required so a later freshness read cannot gain authority by reopening only
// CanonicalID and forgetting a stricter presentation alias.
type SourceWindowEvidence struct {
	CanonicalID    string                        `json:"canonical_id"`
	Authority      SourceWindowAuthorityEvidence `json:"authority"`
	StartLine      int                           `json:"start_line"`
	EndLine        int                           `json:"end_line"`
	TotalLines     int                           `json:"total_lines"`
	Truncated      bool                          `json:"truncated,omitempty"`
	RevisionSHA256 string                        `json:"revision_sha256"`
	WindowSHA256   string                        `json:"window_sha256"`
	MutationEpoch  uint64                        `json:"mutation_epoch"`
}

// DiagnosticLogReadEvidence records one successful bounded read of a genuine
// workspace diagnostic log. It is intentionally source-ineligible: no body,
// revision digest, window digest, or mutation epoch crosses this boundary.
type DiagnosticLogReadEvidence struct {
	CanonicalID    string              `json:"canonical_id"`
	PolicySubject  string              `json:"policy_subject"`
	PolicyDecision toolpolicy.Decision `json:"policy_decision"`
	StartLine      int                 `json:"start_line"`
	EndLine        int                 `json:"end_line"`
	TotalLines     int                 `json:"total_lines"`
	Truncated      bool                `json:"truncated,omitempty"`
}

// SubmitPlanDiagnosticKind identifies a producer-owned submit_plan rejection
// reason. Runtime consumers use these finite values instead of interpreting
// model-facing Summary, Error, or Output prose.
type SubmitPlanDiagnosticKind string

const (
	SubmitPlanDiagnosticMissingRequiredPath    SubmitPlanDiagnosticKind = "missing_required_path"
	SubmitPlanDiagnosticMissingEditPath        SubmitPlanDiagnosticKind = "missing_edit_path"
	SubmitPlanDiagnosticOutsideScopePath       SubmitPlanDiagnosticKind = "outside_scope_path"
	SubmitPlanDiagnosticMissingRequiredSnippet SubmitPlanDiagnosticKind = "missing_required_snippet"
	SubmitPlanDiagnosticPlaceholderBody        SubmitPlanDiagnosticKind = "placeholder_body"
	SubmitPlanDiagnosticMissingSourceGrounding SubmitPlanDiagnosticKind = "missing_source_grounding"
	SubmitPlanDiagnosticInvalidAnchor          SubmitPlanDiagnosticKind = "invalid_anchor"
	SubmitPlanDiagnosticSyntaxValidation       SubmitPlanDiagnosticKind = "syntax_validation"
	SubmitPlanDiagnosticWholeFileRewrite       SubmitPlanDiagnosticKind = "whole_file_rewrite"
	SubmitPlanDiagnosticInvalidEditShape       SubmitPlanDiagnosticKind = "invalid_edit_shape"
	SubmitPlanDiagnosticOversizedInsertion     SubmitPlanDiagnosticKind = "oversized_insertion"
	SubmitPlanDiagnosticNoOpEdit               SubmitPlanDiagnosticKind = "no_op_edit"
	SubmitPlanDiagnosticSourceSatisfiedShape   SubmitPlanDiagnosticKind = "source_satisfied_terminal_shape"
)

// SubmitPlanDiagnostic records one exact machine-readable reason a submit_plan
// request was rejected. Code is the stable rejection code emitted by the
// producer. CanonicalID is a physical workspace-relative identity when the
// reason belongs to a path. StartLine/EndLine identify an exact inclusive range
// when the rejection belongs to one. ObligationID is reserved for non-path
// contract obligations such as a required snippet.
type SubmitPlanDiagnostic struct {
	Code         string                   `json:"code"`
	Kind         SubmitPlanDiagnosticKind `json:"kind"`
	CanonicalID  string                   `json:"canonical_id,omitempty"`
	StartLine    int                      `json:"start_line,omitempty"`
	EndLine      int                      `json:"end_line,omitempty"`
	ObligationID string                   `json:"obligation_id,omitempty"`
}

// DiscoveryCandidateEvidence records producer-owned discovery evidence for a
// candidate path. The slice order is the producer's rank.
type DiscoveryCandidateEvidence struct {
	Path         string `json:"path,omitempty"`
	LayerName    string `json:"layer_name,omitempty"`
	Tool         string `json:"tool,omitempty"`
	Summary      string `json:"summary,omitempty"`
	EvidenceLine string `json:"evidence_line,omitempty"`
}

// VerificationEvidenceKind identifies a producer-owned verification method.
type VerificationEvidenceKind string

const (
	VerificationEvidenceHTMLParse    VerificationEvidenceKind = "html_parse"
	VerificationEvidenceGitDiffCheck VerificationEvidenceKind = "git_diff_check"
)

// VerificationEvidence records exact paths covered by a successful verifier.
// Consumers must not infer equivalent evidence from rendered tool output.
type VerificationEvidence struct {
	Kind  VerificationEvidenceKind `json:"kind"`
	Paths []string                 `json:"paths,omitempty"`
}

// ToolRejectionDisposition describes what a rejected request means for the
// model's original action intent. Human-readable summaries remain presentation
// only; lifecycle consumers use this typed disposition.
type ToolRejectionDisposition string

const (
	// ToolRejectionNotRetained records a rejection whose owner did not classify
	// it as recoverable mutation intent. Conservative consumers must not turn
	// an unclassified failure into a pending write obligation.
	ToolRejectionNotRetained ToolRejectionDisposition = "not_retained"
	// ToolRejectionDeferredByPhase preserves a valid request that was not
	// callable in the active phase. Its fingerprint may be released only when
	// that same tool becomes callable again.
	ToolRejectionDeferredByPhase ToolRejectionDisposition = "deferred_by_phase"
	// ToolRejectionDeferredByPrerequisite preserves a valid request while
	// source, history, verification, or another typed prerequisite is pending.
	ToolRejectionDeferredByPrerequisite ToolRejectionDisposition = "deferred_by_prerequisite"
	// ToolRejectionInvalidRequest records malformed or otherwise invalid input.
	// It never creates a pending write obligation.
	ToolRejectionInvalidRequest ToolRejectionDisposition = "invalid_request"
	// ToolRejectionPolicyDenied records scope, permission, protected-path, or
	// other terminal policy denial. It never creates a pending write obligation.
	ToolRejectionPolicyDenied ToolRejectionDisposition = "policy_denied"
)

// CarriesRejectedWriteIntent reports whether the disposition preserves a
// model-requested mutation until a later successful write changes its paths or
// an exact path-specific blocker terminates the run.
func (d ToolRejectionDisposition) CarriesRejectedWriteIntent() bool {
	switch d {
	case ToolRejectionDeferredByPhase,
		ToolRejectionDeferredByPrerequisite:
		return true
	default:
		return false
	}
}

// PhaseLocal reports whether a denial is owned only by the active tool phase.
func (d ToolRejectionDisposition) PhaseLocal() bool {
	return d == ToolRejectionDeferredByPhase
}

// ToolResultFacts carries producer-owned evidence facts that consumers should
// not re-derive from model-facing result text.
type ToolResultFacts struct {
	DiagnosticEvidencePaths   []string                     `json:"diagnostic_evidence_paths,omitempty"`
	RejectedWritePaths        []string                     `json:"rejected_write_paths,omitempty"`
	DeferredPlanPaths         []string                     `json:"deferred_plan_paths,omitempty"`
	PendingSourceBodyPaths    []string                     `json:"pending_source_body_paths,omitempty"`
	PendingHistoryPaths       []string                     `json:"pending_history_paths,omitempty"`
	RejectionDisposition      ToolRejectionDisposition     `json:"rejection_disposition,omitempty"`
	VerificationEvidence      []VerificationEvidence       `json:"verification_evidence,omitempty"`
	SourceWindows             []SourceWindowEvidence       `json:"source_windows,omitempty"`
	DiagnosticLogReads        []DiagnosticLogReadEvidence  `json:"diagnostic_log_reads,omitempty"`
	SubmitPlanDiagnostics     []SubmitPlanDiagnostic       `json:"submit_plan_diagnostics,omitempty"`
	ArtifactIDs               []string                     `json:"artifact_ids,omitempty"`
	CandidatePaths            []string                     `json:"candidate_paths,omitempty"`
	CandidateSymbols          []string                     `json:"candidate_symbols,omitempty"`
	RankedDiscoveryCandidates []DiscoveryCandidateEvidence `json:"ranked_discovery_candidates,omitempty"`
	HasDiscoveryTargets       bool                         `json:"has_discovery_targets,omitempty"`
	DiscoveryStrength         DiscoveryStrength            `json:"discovery_strength,omitempty"`
	SourceOwnerFound          bool                         `json:"source_owner_found,omitempty"`
	LocalizationConfidence    string                       `json:"localization_confidence,omitempty"`
	SearchHitCount            int                          `json:"search_hit_count,omitempty"`
	SearchHighCardinality     bool                         `json:"search_high_cardinality,omitempty"`
	GitHistoryPath            string                       `json:"git_history_path,omitempty"`
	GitHistoryStatus          GitHistoryStatus             `json:"git_history_status,omitempty"`
	ShellCommand              string                       `json:"shell_command,omitempty"`
	ShellWorkdir              string                       `json:"shell_workdir,omitempty"`
}

// NewSourceWindowFacts returns normalized, revision-bound source-window facts,
// or nil when no valid evidence remains after normalization.
func NewSourceWindowFacts(evidence ...SourceWindowEvidence) *ToolResultFacts {
	normalized := NormalizeSourceWindows(evidence)
	if len(normalized) == 0 {
		return nil
	}

	return &ToolResultFacts{SourceWindows: normalized}
}

// NewDiagnosticLogReadFacts returns normalized metadata-only diagnostic-log
// facts, or nil when no safe workspace-relative evidence remains.
func NewDiagnosticLogReadFacts(evidence ...DiagnosticLogReadEvidence) *ToolResultFacts {
	normalized := NormalizeDiagnosticLogReads(evidence)
	if len(normalized) == 0 {
		return nil
	}

	return &ToolResultFacts{DiagnosticLogReads: normalized}
}

// Clone returns a deep copy of producer-owned facts so adapters can preserve the
// authority fields without sharing slice backing arrays.
func (facts *ToolResultFacts) Clone() *ToolResultFacts {
	if facts == nil {
		return nil
	}

	return &ToolResultFacts{
		DiagnosticEvidencePaths:   append([]string(nil), facts.DiagnosticEvidencePaths...),
		RejectedWritePaths:        append([]string(nil), facts.RejectedWritePaths...),
		DeferredPlanPaths:         append([]string(nil), facts.DeferredPlanPaths...),
		PendingSourceBodyPaths:    append([]string(nil), facts.PendingSourceBodyPaths...),
		PendingHistoryPaths:       append([]string(nil), facts.PendingHistoryPaths...),
		RejectionDisposition:      facts.RejectionDisposition,
		VerificationEvidence:      cloneVerificationEvidence(facts.VerificationEvidence),
		SourceWindows:             append([]SourceWindowEvidence(nil), facts.SourceWindows...),
		DiagnosticLogReads:        append([]DiagnosticLogReadEvidence(nil), facts.DiagnosticLogReads...),
		SubmitPlanDiagnostics:     append([]SubmitPlanDiagnostic(nil), facts.SubmitPlanDiagnostics...),
		ArtifactIDs:               append([]string(nil), facts.ArtifactIDs...),
		CandidatePaths:            append([]string(nil), facts.CandidatePaths...),
		CandidateSymbols:          append([]string(nil), facts.CandidateSymbols...),
		RankedDiscoveryCandidates: append([]DiscoveryCandidateEvidence(nil), facts.RankedDiscoveryCandidates...),
		HasDiscoveryTargets:       facts.HasDiscoveryTargets,
		DiscoveryStrength:         facts.DiscoveryStrength,
		SourceOwnerFound:          facts.SourceOwnerFound,
		LocalizationConfidence:    facts.LocalizationConfidence,
		SearchHitCount:            facts.SearchHitCount,
		SearchHighCardinality:     facts.SearchHighCardinality,
		GitHistoryPath:            facts.GitHistoryPath,
		GitHistoryStatus:          facts.GitHistoryStatus,
		ShellCommand:              facts.ShellCommand,
		ShellWorkdir:              facts.ShellWorkdir,
	}
}

// NewSubmitPlanDiagnosticFacts returns normalized producer-owned submit_plan
// rejection diagnostics, or nil when no valid diagnostic remains.
func NewSubmitPlanDiagnosticFacts(diagnostics ...SubmitPlanDiagnostic) *ToolResultFacts {
	normalized := NormalizeSubmitPlanDiagnostics(diagnostics)
	if len(normalized) == 0 {
		return nil
	}

	return &ToolResultFacts{SubmitPlanDiagnostics: normalized}
}

func cloneVerificationEvidence(evidence []VerificationEvidence) []VerificationEvidence {
	if len(evidence) == 0 {
		return nil
	}

	clone := make([]VerificationEvidence, len(evidence))
	for i, item := range evidence {
		clone[i] = VerificationEvidence{
			Kind:  item.Kind,
			Paths: append([]string(nil), item.Paths...),
		}
	}
	return clone
}

// NormalizeSourceWindows validates and deduplicates producer source windows.
// Incomplete, non-canonical, or digest-inconsistent facts are discarded.
func NormalizeSourceWindows(evidence []SourceWindowEvidence) []SourceWindowEvidence {
	seen := make(map[string]int, len(evidence))
	out := make([]SourceWindowEvidence, 0, len(evidence))

	for _, fact := range evidence {
		fact.CanonicalID = normalizeWorkspaceRelativeEvidencePath(fact.CanonicalID)
		fact.Authority.PolicySubject = normalizeWorkspaceRelativeEvidencePath(fact.Authority.PolicySubject)
		if fact.CanonicalID == "" || fact.Authority.PolicySubject == "" ||
			!validReadPolicyDecision(fact.Authority.PolicyDecision) || !validReadRange(fact.StartLine, fact.EndLine, fact.TotalLines) {
			continue
		}
		if !validSourceWindowSHA256(fact.RevisionSHA256) || !validSourceWindowSHA256(fact.WindowSHA256) || fact.MutationEpoch == 0 {
			continue
		}

		key := strings.Join([]string{
			fact.CanonicalID,
			fact.Authority.PolicySubject,
			string(fact.Authority.PolicyDecision),
			strconv.Itoa(fact.StartLine),
			strconv.Itoa(fact.EndLine),
			strconv.Itoa(fact.TotalLines),
			fact.RevisionSHA256,
			fact.WindowSHA256,
			strconv.FormatUint(fact.MutationEpoch, 10),
		}, "\x00")
		if idx, ok := seen[key]; ok {
			out[idx].Truncated = out[idx].Truncated || fact.Truncated
			continue
		}

		seen[key] = len(out)
		out = append(out, fact)
	}

	return out
}

// NormalizeDiagnosticLogReads validates and deduplicates metadata-only log
// read facts. Absolute, traversal-bearing, or malformed identities are dropped.
func NormalizeDiagnosticLogReads(evidence []DiagnosticLogReadEvidence) []DiagnosticLogReadEvidence {
	seen := make(map[string]int, len(evidence))
	out := make([]DiagnosticLogReadEvidence, 0, len(evidence))

	for _, fact := range evidence {
		fact.CanonicalID = normalizeWorkspaceRelativeEvidencePath(fact.CanonicalID)
		fact.PolicySubject = normalizeWorkspaceRelativeEvidencePath(fact.PolicySubject)
		if fact.CanonicalID == "" || fact.PolicySubject == "" ||
			!validReadPolicyDecision(fact.PolicyDecision) || !validReadRange(fact.StartLine, fact.EndLine, fact.TotalLines) {
			continue
		}

		key := strings.Join([]string{
			fact.CanonicalID,
			fact.PolicySubject,
			string(fact.PolicyDecision),
			strconv.Itoa(fact.StartLine),
			strconv.Itoa(fact.EndLine),
			strconv.Itoa(fact.TotalLines),
		}, "\x00")
		if idx, ok := seen[key]; ok {
			out[idx].Truncated = out[idx].Truncated || fact.Truncated
			continue
		}

		seen[key] = len(out)
		out = append(out, fact)
	}

	return out
}

// NormalizeSubmitPlanDiagnostics validates and deduplicates submit_plan
// rejection diagnostics. It deliberately does not repair paths or ranges:
// producers must supply an exact canonical identity and an exact range.
func NormalizeSubmitPlanDiagnostics(diagnostics []SubmitPlanDiagnostic) []SubmitPlanDiagnostic {
	seen := make(map[string]struct{}, len(diagnostics))
	out := make([]SubmitPlanDiagnostic, 0, len(diagnostics))

	for _, diagnostic := range diagnostics {
		rawCanonicalID := strings.TrimSpace(diagnostic.CanonicalID)
		diagnostic.Code = strings.TrimSpace(diagnostic.Code)
		diagnostic.CanonicalID = normalizeWorkspaceRelativeEvidencePath(diagnostic.CanonicalID)
		diagnostic.ObligationID = strings.TrimSpace(diagnostic.ObligationID)

		if diagnostic.Code == "" || strings.ContainsAny(diagnostic.Code, " \t\r\n\x00") ||
			!validSubmitPlanDiagnosticKind(diagnostic.Kind) {
			continue
		}
		if rawCanonicalID != "" && diagnostic.CanonicalID == "" {
			continue
		}
		if !validSubmitPlanDiagnosticSubject(diagnostic) || !validSubmitPlanDiagnosticRange(diagnostic) {
			continue
		}

		key := strings.Join([]string{
			diagnostic.Code,
			string(diagnostic.Kind),
			diagnostic.CanonicalID,
			strconv.Itoa(diagnostic.StartLine),
			strconv.Itoa(diagnostic.EndLine),
			diagnostic.ObligationID,
		}, "\x00")
		if _, duplicate := seen[key]; duplicate {
			continue
		}

		seen[key] = struct{}{}
		out = append(out, diagnostic)
	}

	return out
}

func validSubmitPlanDiagnosticKind(kind SubmitPlanDiagnosticKind) bool {
	switch kind {
	case SubmitPlanDiagnosticMissingRequiredPath,
		SubmitPlanDiagnosticMissingEditPath,
		SubmitPlanDiagnosticOutsideScopePath,
		SubmitPlanDiagnosticMissingRequiredSnippet,
		SubmitPlanDiagnosticPlaceholderBody,
		SubmitPlanDiagnosticMissingSourceGrounding,
		SubmitPlanDiagnosticInvalidAnchor,
		SubmitPlanDiagnosticSyntaxValidation,
		SubmitPlanDiagnosticWholeFileRewrite,
		SubmitPlanDiagnosticInvalidEditShape,
		SubmitPlanDiagnosticOversizedInsertion,
		SubmitPlanDiagnosticNoOpEdit,
		SubmitPlanDiagnosticSourceSatisfiedShape:
		return true
	default:
		return false
	}
}

func validSubmitPlanDiagnosticSubject(diagnostic SubmitPlanDiagnostic) bool {
	switch diagnostic.Kind {
	case SubmitPlanDiagnosticMissingRequiredPath,
		SubmitPlanDiagnosticOutsideScopePath,
		SubmitPlanDiagnosticPlaceholderBody,
		SubmitPlanDiagnosticMissingSourceGrounding,
		SubmitPlanDiagnosticInvalidAnchor,
		SubmitPlanDiagnosticSyntaxValidation,
		SubmitPlanDiagnosticWholeFileRewrite,
		SubmitPlanDiagnosticOversizedInsertion:
		return diagnostic.CanonicalID != "" && diagnostic.ObligationID == ""
	case SubmitPlanDiagnosticMissingRequiredSnippet:
		return diagnostic.CanonicalID == "" && diagnostic.ObligationID != "" && !strings.ContainsAny(diagnostic.ObligationID, "\r\n\x00")
	case SubmitPlanDiagnosticMissingEditPath,
		SubmitPlanDiagnosticInvalidEditShape,
		SubmitPlanDiagnosticNoOpEdit,
		SubmitPlanDiagnosticSourceSatisfiedShape:
		return diagnostic.ObligationID == ""
	default:
		return false
	}
}

func validSubmitPlanDiagnosticRange(diagnostic SubmitPlanDiagnostic) bool {
	if diagnostic.StartLine == 0 && diagnostic.EndLine == 0 {
		return true
	}
	return diagnostic.CanonicalID != "" && diagnostic.StartLine > 0 && diagnostic.EndLine >= diagnostic.StartLine
}

func validReadPolicyDecision(decision toolpolicy.Decision) bool {
	switch decision {
	case toolpolicy.None, toolpolicy.Allow, toolpolicy.Ask, toolpolicy.Deny:
		return true
	default:
		return false
	}
}

func validReadRange(startLine int, endLine int, totalLines int) bool {
	if totalLines == 0 {
		return startLine == 0 && endLine == 0
	}
	return startLine > 0 && endLine >= startLine && totalLines >= endLine
}

func validSourceWindowSHA256(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, char := range value[len("sha256:"):] {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}

func normalizeWorkspaceRelativeEvidencePath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, "`'\" ")
	if path == "" || strings.ContainsRune(path, 0) || strings.HasPrefix(path, "/") || strings.Contains(path, `\`) {
		return ""
	}
	if len(path) >= 3 && ((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) && path[1] == ':' && path[2] == '/' {
		return ""
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return ""
		}
	}
	return path
}

// SourceWindowSHA256 returns the canonical digest spelling used for source
// revisions and exact window bodies.
func SourceWindowSHA256(body []byte) string {
	digest := sha256.Sum256(body)
	return fmt.Sprintf("sha256:%x", digest)
}
