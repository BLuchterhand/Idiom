// Package agent — re-exports for evidence session store types moved
// to internal/runtime/agent/evidence.
package agent

import "idiom/internal/runtime/agent/evidence"

type SessionEvidenceRecord = evidence.SessionEvidenceRecord

// WriteSessionEvidenceRecord writes a SessionEvidenceRecord to disk.
func WriteSessionEvidenceRecord(workspaceRoot, sessionID, prompt, objective string, readPaths []string, wasBlocked bool, blockedBy string) {
	evidence.WriteSessionEvidenceRecord(workspaceRoot, sessionID, prompt, objective, readPaths, wasBlocked, blockedBy)
}

// FindMatchingPriorSession returns the most recent matching session record.
func FindMatchingPriorSession(workspaceRoot, prompt string) *SessionEvidenceRecord {
	return evidence.FindMatchingPriorSession(workspaceRoot, prompt)
}

// PriorSessionCandidatePaths returns prior session read paths for a prompt.
func PriorSessionCandidatePaths(workspaceRoot, prompt string) []string {
	return evidence.PriorSessionCandidatePaths(workspaceRoot, prompt)
}
