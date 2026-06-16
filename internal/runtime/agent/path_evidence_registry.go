// Package agent — re-exports for evidence path registry types moved
// to internal/runtime/agent/evidence.
package agent

import "idiom/internal/runtime/agent/evidence"

type EvidenceLevel = evidence.EvidenceLevel
type PathEvidence = evidence.PathEvidence
type PathEvidenceRegistry = evidence.PathEvidenceRegistry

const (
	EvidenceNone              = evidence.EvidenceNone
	EvidencePathSeen          = evidence.EvidencePathSeen
	EvidenceSourceBodyPending = evidence.EvidenceSourceBodyPending
	EvidenceBodyWindow        = evidence.EvidenceBodyWindow
	EvidenceAnchorReady       = evidence.EvidenceAnchorReady
	EvidenceExactAnchor       = evidence.EvidenceExactAnchor
	EvidenceNewFile           = evidence.EvidenceNewFile
)

func NewPathEvidenceRegistry(workspaceRoot string) *PathEvidenceRegistry {
	return evidence.NewPathEvidenceRegistry(workspaceRoot)
}
