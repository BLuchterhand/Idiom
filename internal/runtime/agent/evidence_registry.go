package agent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
)

// ---------------------------------------------------------------------------
// PathEvidenceRegistry owns path-state facts for source-like workspace paths:
// filesystem existence, body-read level, candidate status, and basename
// resolution. It does not own diagnostic relevance, delegated task packets,
// plan artifacts, or read-only answerability; those remain raw-result read-model
// concerns because they depend on objective fit and provenance.
// ---------------------------------------------------------------------------

// EvidenceLevel mirrors PlanThenExecuteSourceEvidenceLevel for use within
// the registry without importing the plan-execute guard package cycle.
type EvidenceLevel int

const (
	EvidenceNone        EvidenceLevel = 0
	EvidenceBodyWindow  EvidenceLevel = 2
	EvidenceAnchorReady EvidenceLevel = 3
)

// PathEvidence is the per-path record maintained by the registry.
type PathEvidence struct {
	Path                 string        // canonical workspace-relative path
	ExistsOnDisk         bool          // populated at registry construction
	ExistenceChecked     bool          // true once ExistsOnDisk has been resolved
	BodyReadLevel        EvidenceLevel // populated from tool results
	IsCandidate          bool          // returned by context_pack / context_query / discovery
	CandidatesFromPacket string        // name of the packet/tool that produced this candidate
}

// PathEvidenceRegistry is the central registry for all path-related evidence
// state during a single runner turn.
type PathEvidenceRegistry struct {
	mu    sync.RWMutex
	paths map[string]*PathEvidence

	workspaceRoot string
}

// NewPathEvidenceRegistry creates an empty registry. Call RegisterToolResults
// to populate it from tool results; workspaceRoot is used for lazy filesystem
// existence checks.
func NewPathEvidenceRegistry(workspaceRoot string) *PathEvidenceRegistry {
	return &PathEvidenceRegistry{
		paths:         make(map[string]*PathEvidence),
		workspaceRoot: workspaceRoot,
	}
}

// ---------------------------------------------------------------------------
// Population
// ---------------------------------------------------------------------------

// RegisterToolResults ingests a slice of ToolResults and updates the registry
// with body-read evidence, candidate paths, and existence status.
func (r *PathEvidenceRegistry) RegisterToolResults(results []ToolResult) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		tool := strings.TrimSpace(result.ToolName)

		switch {
		case tool == workspace.ToolNameReadFile || tool == workspace.ToolNamePrefetch:
			r.registerBodyRead(result)
		case tool == workspace.ToolNameCodeNavigate || tool == workspace.ToolNameReadOnlyCapability:
			r.registerBodyRead(result)
		case strings.HasSuffix(tool, "_read_symbol"), strings.HasSuffix(tool, "_extract_body"):
			r.registerBodyRead(result)
		case tool == workspace.ToolNameContextPack || sourceEvidenceDiscoveryCandidateTool(tool) || sourceEvidenceSearchCandidateTool(tool):
			r.registerCandidatePaths(result)
		}
	}
}

func (r *PathEvidenceRegistry) registerBodyRead(result ToolResult) {
	level := bodyReadLevelForResult(result)
	if level == EvidenceNone {
		return
	}
	// Extract path from the tool result's Summary (which begins with "Read" or "Routed")
	paths := append([]string{}, result.Paths...)
	paths = append(paths, pathparse.MentionedWorkspaceFiles(result.Summary)...)
	if len(paths) == 0 {
		// Fall back to the request path embedded in the result
		if p := extractPathFromResult(result); p != "" {
			paths = []string{p}
		}
	}
	for _, p := range paths {
		p = normalizeRegistryPath(p)
		if p == "" {
			continue
		}
		if IsNonSourceEvidenceArtifactPath(p) || !pathparse.HasWorkspaceFileExtension(p) {
			continue
		}
		pe := r.getOrCreate(p)
		// The path was successfully read by a tool — mark it as existing on disk.
		// This avoids the EnsurePathExists cache returning stale false for entries
		// created here with getOrCreate's zero-value ExistsOnDisk.
		pe.ExistsOnDisk = true
		pe.ExistenceChecked = true
		if pe.BodyReadLevel < level {
			pe.BodyReadLevel = level
		}
	}
}

func bodyReadLevelForResult(result ToolResult) EvidenceLevel {
	tool := strings.TrimSpace(result.ToolName)
	text := strings.ToLower(strings.Join([]string{result.Summary, result.Output}, "\n"))
	if strings.TrimSpace(result.Error) != "" {
		return EvidenceNone
	}
	if strings.Contains(text, "redirected ") ||
		strings.Contains(text, "use code_navigate for source-body evidence") ||
		strings.Contains(text, "path-filter miss") ||
		strings.Contains(text, "candidate_anchor") ||
		strings.Contains(text, "candidate anchor") ||
		strings.Contains(text, "no source-body match") {
		return EvidenceNone
	}
	if strings.Contains(text, ".idiom/comprehension/views/") {
		return EvidenceNone
	}
	if tool == workspace.ToolNameReadFile || tool == workspace.ToolNamePrefetch {
		if len(result.Paths) > 0 || (strings.Contains(text, "read ") && strings.Contains(text, " lines ")) {
			return EvidenceAnchorReady
		}
		return EvidenceNone
	}
	if tool == workspace.ToolNameCodeNavigate || tool == workspace.ToolNameReadOnlyCapability {
		if strings.Contains(text, "file_outline") ||
			strings.Contains(text, "outlined ") ||
			strings.Contains(text, "top-level declaration") {
			return EvidenceNone
		}
		if strings.Contains(text, "read_symbol") ||
			strings.Contains(text, "extract_body") ||
			strings.Contains(text, "source body") ||
			strings.Contains(text, "source-body") ||
			(strings.Contains(text, "read ") && strings.Contains(text, " lines ")) {
			return EvidenceAnchorReady
		}
		return EvidenceNone
	}
	if strings.HasSuffix(tool, "_file_outline") {
		return EvidenceNone
	}
	if strings.HasSuffix(tool, "_read_symbol") || strings.HasSuffix(tool, "_extract_body") {
		if strings.Contains(text, "read ") && strings.Contains(text, " lines ") ||
			strings.Contains(text, "func ") ||
			strings.Contains(text, "type ") ||
			strings.Contains(text, "package ") {
			return EvidenceAnchorReady
		}
	}
	return EvidenceNone
}

// registerCandidatePaths extracts workspace file paths from a discovery or
// search tool result and marks them as candidates.
// This seeds the registry with paths that the model can later read as source-body
// evidence, and ensures the completion gate's source-grounding check does not
// reject them as "discovered but never registered." When context_pack fails
// (e.g., readonly DB), search_content candidates provide the fallback path set.
func (r *PathEvidenceRegistry) registerCandidatePaths(result ToolResult) {
	tool := strings.TrimSpace(result.ToolName)
	workspaceResult := workspace.ToolResult{
		Tool:    tool,
		Summary: result.Summary,
		Output:  result.Output,
		Error:   result.Error,
	}
	paths := append([]string{}, result.Paths...)
	paths = append(paths, sourceEvidenceCandidatePathsFromResult(workspaceResult, 128)...)

	if len(paths) == 0 {
		return
	}
	for _, p := range paths {
		p = normalizeRegistryPath(p)
		if p == "" {
			continue
		}
		if IsNonSourceEvidenceArtifactPath(p) || !pathparse.HasWorkspaceFileExtension(p) {
			continue
		}
		pe := r.getOrCreate(p)
		pe.IsCandidate = true
		if pe.CandidatesFromPacket == "" {
			pe.CandidatesFromPacket = result.ToolName
		}
		// Don't set ExistsOnDisk here — we know the path was mentioned in
		// search results but don't know if it still exists. It will be
		// resolved lazily by EnsurePathExists when the model cites it.
	}
}

// EnsurePathExists checks the filesystem once and caches the result.
func (r *PathEvidenceRegistry) EnsurePathExists(path string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	p := normalizeRegistryPath(path)
	if p == "" {
		return false
	}

	root := "."
	if r.workspaceRoot != "" {
		root = r.workspaceRoot
	}

	pe, ok := r.paths[p]
	if ok {
		// Candidate paths are discovered before body reads. Resolve their
		// filesystem existence lazily instead of treating the zero-value as a
		// cached negative.
		if pe.ExistsOnDisk {
			return true
		}
		if !pe.ExistenceChecked {
			info, err := os.Stat(filepath.Join(root, filepath.FromSlash(p)))
			exists := err == nil && !info.IsDir()
			pe.ExistsOnDisk = exists
			pe.ExistenceChecked = true
			return exists
		}
		return false
	}

	// First contact: check the filesystem
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(p)))
	exists := err == nil && !info.IsDir()

	r.paths[p] = &PathEvidence{
		Path:             p,
		ExistsOnDisk:     exists,
		ExistenceChecked: true,
	}
	return exists
}

// ---------------------------------------------------------------------------
// Queries
// ---------------------------------------------------------------------------

// ResolveShortPath attempts to resolve a bare filename (e.g. "ray_intersection.rs")
// to its full workspace path. It searches:
//  1. Known registry paths (from tool results)
//  2. A filesystem glob for unique basename matches
//
// Returns the resolved path and true if exactly one candidate match is found.
func (r *PathEvidenceRegistry) ResolveShortPath(name string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	name = normalizeRegistryPath(name)
	if name == "" {
		return "", false
	}

	// If the path already contains a directory or is a root-level file, use as-is.
	if strings.Contains(name, "/") {
		if r.existsOnDiskUnsafe(name) {
			return name, true
		}
		return name, false
	}

	base := strings.ToLower(filepath.Base(name))

	// First pass: search known registry paths.
	var matches []string
	for _, pe := range r.paths {
		if !pe.ExistsOnDisk {
			continue
		}
		if strings.ToLower(filepath.Base(pe.Path)) == base {
			matches = append(matches, pe.Path)
		}
	}

	if len(matches) == 1 {
		return matches[0], true
	}
	if len(matches) > 1 {
		// Ambiguous — can't resolve to a single path.
		return name, false
	}

	// Second pass: search the filesystem via a glob.
	root := "."
	if r.workspaceRoot != "" {
		root = r.workspaceRoot
	}
	globMatches, _ := filepath.Glob(filepath.Join(root, "**", name))
	var filtered []string
	for _, m := range globMatches {
		rel, err := filepath.Rel(root, m)
		if err != nil {
			continue
		}
		info, err := os.Stat(m)
		if err != nil || info.IsDir() {
			continue
		}
		filtered = append(filtered, normalizeRegistryPath(rel))
	}
	if len(filtered) == 1 {
		return filtered[0], true
	}
	return name, false
}

// HasBodyEvidence returns true if the path has been read with body-level
// evidence (body_window or higher).
func (r *PathEvidenceRegistry) HasBodyEvidence(path string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	pe, ok := r.paths[normalizeRegistryPath(path)]
	if !ok {
		return false
	}
	return pe.BodyReadLevel >= EvidenceBodyWindow
}

// MergeFromPriorSession injects paths from a prior session's evidence as
// candidates (not body reads). These become UnreadCandidatePaths entries
// that the model can choose to read.
func (r *PathEvidenceRegistry) MergeFromPriorSession(paths []string) {
	if len(paths) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, p := range paths {
		p = normalizeRegistryPath(p)
		if p == "" {
			continue
		}
		pe, ok := r.paths[p]
		if !ok {
			r.paths[p] = &PathEvidence{
				Path:             p,
				IsCandidate:      true,
				ExistsOnDisk:     r.existsOnDiskUnsafe(p),
				ExistenceChecked: true,
			}
		} else {
			pe.IsCandidate = true
		}
	}
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func (r *PathEvidenceRegistry) getOrCreate(path string) *PathEvidence {
	pe, ok := r.paths[path]
	if !ok {
		pe = &PathEvidence{
			Path: path,
		}
		r.paths[path] = pe
	}
	return pe
}

// existsOnDiskUnsafe checks the filesystem without locking. Caller must hold
// at least a read lock (or know the registry is not yet shared).
func (r *PathEvidenceRegistry) existsOnDiskUnsafe(path string) bool {
	root := "."
	if r.workspaceRoot != "" {
		root = r.workspaceRoot
	}
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
	return err == nil && !info.IsDir()
}

func normalizeRegistryPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) || looksLikeWindowsAbsolutePath(path) {
		return ""
	}

	path = filepath.ToSlash(filepath.Clean(path))
	path = strings.TrimPrefix(path, "./")
	if path == "." || path == ".." || strings.HasPrefix(path, "../") || strings.HasPrefix(path, `..\`) {
		return ""
	}

	return path
}

func looksLikeWindowsAbsolutePath(path string) bool {
	return len(path) >= 3 &&
		((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) &&
		path[1] == ':' &&
		(path[2] == '/' || path[2] == '\\')
}

func extractPathFromResult(result ToolResult) string {
	// The tool results store the path information in the Summary field
	// (e.g. "Read src/graphics/mod.rs lines 1-14 (total lines: 14)")
	// or Output. We extract it via mentioned-workspace-files parsing.
	paths := pathparse.MentionedWorkspaceFiles(result.Summary)
	if len(paths) > 0 {
		return paths[0]
	}
	paths = pathparse.MentionedWorkspaceFiles(result.Output)
	if len(paths) > 0 {
		return paths[0]
	}
	return ""
}

// --- session evidence store ---
// SessionEvidenceRecord stores the paths that were read in a completed session
// so subsequent sessions can be seeded with candidate paths.
type SessionEvidenceRecord struct {
	SessionID  string   `json:"session_id"`
	Prompt     string   `json:"prompt,omitempty"`     // legacy read-only field; new writes persist only PromptHash
	PromptHash string   `json:"prompt_hash"`          // sha256 of the original prompt
	ReadPaths  []string `json:"read_paths"`           // paths that were body-read
	Objective  string   `json:"objective,omitempty"`  // legacy read-only field; new writes do not persist raw objective text
	WasBlocked bool     `json:"was_blocked"`          // true if session ended with blocked
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
// matches. For legacy records that still contain raw prompt text, it can also
// match when the Levenshtein edit distance is < 20% of the prompt length.
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

// ToolResult is the evidence-owned DTO for ingesting workspace results.
// Workspace adapters map concrete workspace.ToolResult and sidecar envelopes to
// this type before evidence code interprets paths, source bodies, or ownership.
type ToolResult struct {
	ToolName string
	Summary  string
	Output   string
	Error    string
	Paths    []string
	Metadata map[string]string
}
