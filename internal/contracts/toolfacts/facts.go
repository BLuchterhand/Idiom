package toolfacts

import (
	"path/filepath"
	"strconv"
	"strings"
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

// SourceBodyEvidence records producer-owned source-body evidence returned by a
// read-like tool. Consumers should not recover this from human-readable
// summaries.
type SourceBodyEvidence struct {
	Path       string `json:"path,omitempty"`
	StartLine  int    `json:"start_line,omitempty"`
	EndLine    int    `json:"end_line,omitempty"`
	TotalLines int    `json:"total_lines,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
}

// ToolResultFacts carries producer-owned evidence facts that consumers should
// not re-derive from model-facing result text.
type ToolResultFacts struct {
	DiagnosticEvidencePaths []string             `json:"diagnostic_evidence_paths,omitempty"`
	SourceBodies            []SourceBodyEvidence `json:"source_bodies,omitempty"`
	CandidatePaths          []string             `json:"candidate_paths,omitempty"`
	CandidateSymbols        []string             `json:"candidate_symbols,omitempty"`
	HasDiscoveryTargets     bool                 `json:"has_discovery_targets,omitempty"`
	DiscoveryStrength       DiscoveryStrength    `json:"discovery_strength,omitempty"`
	SourceOwnerFound        bool                 `json:"source_owner_found,omitempty"`
	LocalizationConfidence  string               `json:"localization_confidence,omitempty"`
	GitHistoryPath          string               `json:"git_history_path,omitempty"`
	GitHistoryStatus        GitHistoryStatus     `json:"git_history_status,omitempty"`
	ShellCommand            string               `json:"shell_command,omitempty"`
}

// NewSourceBodyFacts returns normalized source-body evidence facts, or nil when
// no valid source-body evidence remains after normalization.
func NewSourceBodyFacts(evidence ...SourceBodyEvidence) *ToolResultFacts {
	normalized := NormalizeSourceBodies(evidence)
	if len(normalized) == 0 {
		return nil
	}

	return &ToolResultFacts{SourceBodies: normalized}
}

// Clone returns a deep copy of producer-owned facts so adapters can preserve the
// authority fields without sharing slice backing arrays.
func (facts *ToolResultFacts) Clone() *ToolResultFacts {
	if facts == nil {
		return nil
	}

	return &ToolResultFacts{
		DiagnosticEvidencePaths: append([]string(nil), facts.DiagnosticEvidencePaths...),
		SourceBodies:            append([]SourceBodyEvidence(nil), facts.SourceBodies...),
		CandidatePaths:          append([]string(nil), facts.CandidatePaths...),
		CandidateSymbols:        append([]string(nil), facts.CandidateSymbols...),
		HasDiscoveryTargets:     facts.HasDiscoveryTargets,
		DiscoveryStrength:       facts.DiscoveryStrength,
		SourceOwnerFound:        facts.SourceOwnerFound,
		LocalizationConfidence:  facts.LocalizationConfidence,
		GitHistoryPath:          facts.GitHistoryPath,
		GitHistoryStatus:        facts.GitHistoryStatus,
		ShellCommand:            facts.ShellCommand,
	}
}

// NormalizeSourceBodies normalizes and deduplicates producer source-body facts.
func NormalizeSourceBodies(evidence []SourceBodyEvidence) []SourceBodyEvidence {
	seen := make(map[string]int, len(evidence))
	out := make([]SourceBodyEvidence, 0, len(evidence))

	for _, fact := range evidence {
		fact.Path = normalizeFactPath(fact.Path)
		if fact.Path == "" {
			continue
		}

		if fact.StartLine < 0 {
			fact.StartLine = 0
		}
		if fact.EndLine < 0 {
			fact.EndLine = 0
		}
		if fact.TotalLines < 0 {
			fact.TotalLines = 0
		}

		key := strings.Join([]string{
			fact.Path,
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

func normalizeFactPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, "`'\" ")
	path = filepath.ToSlash(path)
	path = strings.TrimPrefix(path, "./")
	return path
}
