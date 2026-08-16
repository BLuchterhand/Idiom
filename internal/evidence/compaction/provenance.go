package compaction

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

const SchemaVersion = "idiom.context_compaction_provenance.v1"

type ReconstructionStatus string

const (
	ReconstructionVerified ReconstructionStatus = "verified"
	ReconstructionDegraded ReconstructionStatus = "degraded"
	ReconstructionRejected ReconstructionStatus = "rejected"
)

type Provenance struct {
	SchemaVersion        string               `json:"schema_version"`
	SourceMessages       int                  `json:"source_messages"`
	RetainedMessages     int                  `json:"retained_messages"`
	EvictedMessages      int                  `json:"evicted_messages"`
	SourceHash           string               `json:"source_hash"`
	RetainedHash         string               `json:"retained_hash"`
	SummaryHash          string               `json:"summary_hash"`
	RetainedPaths        []string             `json:"retained_paths,omitempty"`
	RequiredPaths        []string             `json:"required_paths,omitempty"`
	Strategy             string               `json:"strategy"`
	Reason               string               `json:"reason"`
	ReconstructionStatus ReconstructionStatus `json:"reconstruction_status"`
}

func New(source, retained, summary string, sourceMessages, retainedMessages int, retainedPaths, requiredPaths []string, strategy, reason string) Provenance {
	retainedPaths = sortedUnique(retainedPaths)
	requiredPaths = sortedUnique(requiredPaths)
	status := ReconstructionVerified
	for _, required := range requiredPaths {
		if !contains(retainedPaths, required) {
			status = ReconstructionRejected
			break
		}
	}
	if retainedMessages <= 0 || sourceMessages <= retainedMessages {
		if status == ReconstructionVerified && sourceMessages > 0 && retainedMessages <= 0 {
			status = ReconstructionRejected
		}
	}
	return Provenance{
		SchemaVersion:        SchemaVersion,
		SourceMessages:       sourceMessages,
		RetainedMessages:     retainedMessages,
		EvictedMessages:      sourceMessages - retainedMessages,
		SourceHash:           fingerprint(source),
		RetainedHash:         fingerprint(retained),
		SummaryHash:          fingerprint(summary),
		RetainedPaths:        retainedPaths,
		RequiredPaths:        requiredPaths,
		Strategy:             strings.TrimSpace(strategy),
		Reason:               strings.TrimSpace(reason),
		ReconstructionStatus: status,
	}
}

func (p Provenance) Acceptable() bool {
	return p.SchemaVersion == SchemaVersion && p.ReconstructionStatus == ReconstructionVerified && p.SourceMessages > p.RetainedMessages && p.EvictedMessages > 0
}

func sortedUnique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func fingerprint(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}
