package agent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SessionEvidenceRecord stores the paths that were read in a completed session
// so subsequent sessions can be seeded with candidate paths.
type SessionEvidenceRecord struct {
	SessionID  string   `json:"session_id"`
	Prompt     string   `json:"prompt"`
	PromptHash string   `json:"prompt_hash"`  // sha256 of the original prompt
	ReadPaths  []string `json:"read_paths"`   // paths that were body-read
	Objective  string   `json:"objective"`    // the brief objective
	WasBlocked bool     `json:"was_blocked"`  // true if session ended with blocked
	BlockedBy  string   `json:"blocked_by,omitempty"` // blocker kind if blocked
	Timestamp  string   `json:"timestamp"`
}

// sessionEvidenceDir returns the directory under workspaceRoot where session
// evidence records are stored: .idiom/sessions/evidence/
func sessionEvidenceDir(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, ".idiom", "sessions", "evidence")
}

// sha256Hex returns the lowercase hex-encoded SHA-256 hash of the input string.
func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h)
}

// WriteSessionEvidenceRecord writes a SessionEvidenceRecord to disk as a JSON
// file named <sessionID>.json under .idiom/sessions/evidence/.
//
// readPaths are the paths that were body-read during the session (obtained from
// PathEvidenceRegistry.BodyReadPaths()). The record is used by subsequent
// sessions to seed candidate paths via MergeFromPriorSession.
func WriteSessionEvidenceRecord(workspaceRoot, sessionID, prompt, objective string, readPaths []string, wasBlocked bool, blockedBy string) {
	if workspaceRoot == "" || sessionID == "" {
		return
	}

	dir := sessionEvidenceDir(workspaceRoot)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}

	// Deduplicate and sort read paths for deterministic output.
	unique := make(map[string]bool)
	for _, p := range readPaths {
		p = strings.TrimSpace(p)
		if p != "" {
			unique[p] = true
		}
	}
	sorted := make([]string, 0, len(unique))
	for p := range unique {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)

	record := SessionEvidenceRecord{
		SessionID:  sessionID,
		Prompt:     prompt,
		PromptHash: sha256Hex(prompt),
		ReadPaths:  sorted,
		Objective:  objective,
		WasBlocked: wasBlocked,
		BlockedBy:  strings.TrimSpace(blockedBy),
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}

	data, err := json.Marshal(record)
	if err != nil {
		return
	}

	// Write atomically: write to a temp file then rename.
	tmpPath := filepath.Join(dir, "."+sessionID+".tmp")
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmpPath, filepath.Join(dir, sessionID+".json"))
}

// levenshteinDistance computes the Levenshtein edit distance between two strings.
// It uses a single-row O(n) space variant of the Wagner-Fischer algorithm.
func levenshteinDistance(a, b string) int {
	if len(a) < len(b) {
		a, b = b, a
	}
	if len(b) == 0 {
		return len(a)
	}

	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)

	for j := range prev {
		prev[j] = j
	}

	for i, ca := range a {
		curr[0] = i + 1
		for j, cb := range b {
			cost := 0
			if ca != cb {
				cost = 1
			}
			curr[j+1] = min(curr[j]+1, prev[j+1]+1, prev[j]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

// FindMatchingPriorSession walks the .idiom/sessions/evidence/ directory and
// returns the most recent SessionEvidenceRecord whose prompt sha256 hash
// matches, or whose Levenshtein edit distance is < 20% of the prompt length.
//
// "Most recent" is determined by the Timestamp field; records with empty
// timestamps sort last.
func FindMatchingPriorSession(workspaceRoot, prompt string) *SessionEvidenceRecord {
	if workspaceRoot == "" || prompt == "" {
		return nil
	}

	dir := sessionEvidenceDir(workspaceRoot)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	promptHash := sha256Hex(prompt)
	promptLen := float64(len(prompt))
	threshold := promptLen * 0.20 // 20% of prompt length

	var candidates []*SessionEvidenceRecord

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}

		var record SessionEvidenceRecord
		if err := json.Unmarshal(data, &record); err != nil {
			continue
		}

		// Skip if this record is the same session — can't match itself.
		// Identify by session ID extracted from the filename.
		sessionIDFromName := strings.TrimSuffix(entry.Name(), ".json")
		if sessionIDFromName == record.SessionID {
			// This is the same session's own record; skip.
			continue
		}

		// Check hash match first (fast path).
		if record.PromptHash != "" && record.PromptHash == promptHash {
			candidates = append(candidates, &record)
			continue
		}

		// Fall back to Levenshtein distance check.
		if threshold <= 0 {
			continue
		}
		dist := levenshteinDistance(prompt, record.Prompt)
		if float64(dist) < threshold {
			candidates = append(candidates, &record)
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	// Sort by timestamp descending (most recent first).
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Timestamp > candidates[j].Timestamp
	})

	return candidates[0]
}

// PriorSessionCandidatePaths is a convenience wrapper that calls
// FindMatchingPriorSession and returns the matching record's ReadPaths.
// Returns nil if no match is found.
func PriorSessionCandidatePaths(workspaceRoot, prompt string) []string {
	record := FindMatchingPriorSession(workspaceRoot, prompt)
	if record == nil {
		return nil
	}
	return record.ReadPaths
}
