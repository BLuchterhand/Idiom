package agent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"idiom/internal/contracts/pathparse"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// --- path evidence registry ---
const (
	toolNameCodeNavigate       = "code_navigate"
	toolNamePrefetch           = "prefetch"
	toolNameReadFile           = "read_file"
	toolNameReadOnlyCapability = "read_only_capability"
	toolNameRg                 = "rg"
	toolNameSearchContent      = "search_content"
	toolNameSearchFiles        = "search_files"
)

// ---------------------------------------------------------------------------
// PathEvidenceRegistry — shared single source of truth for path existence,
// body-read status, candidate tracking, and basename resolution.
//
// Consolidates the following previously independent concerns:
//   - sourceCitationExists / sourceCitationCanonicalPath (runner_source_grounding.go)
//   - sourceEvidenceBodyReadForPath / sourceEvidenceBodyPaths (source_evidence_state.go)
//   - pendingDiscoveryCandidates / discoveryConsumptionCandidatesFromResult
//     (runner_execution_state.go)
//   - delegatedReadOnlyDiscoveryPathMissing (runner_lane_policy.go)
//   - Per-tool freshness checks (context tools)
//
// Populated once per turn by RegisterToolResults; all downstream consumers
// query the registry instead of calling os.Stat or scanning raw tool results.
// ---------------------------------------------------------------------------

// EvidenceLevel mirrors PlanThenExecuteSourceEvidenceLevel for use within
// the registry without importing the plan-execute guard package cycle.
type EvidenceLevel int

const (
	EvidenceNone              EvidenceLevel = 0
	EvidencePathSeen          EvidenceLevel = 1
	EvidenceSourceBodyPending EvidenceLevel = 1
	EvidenceBodyWindow        EvidenceLevel = 2
	EvidenceAnchorReady       EvidenceLevel = 3
	EvidenceExactAnchor       EvidenceLevel = 3
	EvidenceNewFile           EvidenceLevel = 3
)

// PathEvidence is the per-path record maintained by the registry.
type PathEvidence struct {
	Path                 string        // canonical workspace-relative path
	ExistsOnDisk         bool          // populated at registry construction
	ExistenceChecked     bool          // true once ExistsOnDisk has been resolved
	BodyReadLevel        EvidenceLevel // populated from tool results
	IsCandidate          bool          // returned by context_pack / context_query / discovery
	IsMentionedInPrompt  bool          // mentioned in the user's prompt or brief objective
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
// to populate it from tool results, and SetWorkspaceRoot so PathExists can
// fall back to the filesystem.
func NewPathEvidenceRegistry(workspaceRoot string) *PathEvidenceRegistry {
	return &PathEvidenceRegistry{
		paths:         make(map[string]*PathEvidence),
		workspaceRoot: workspaceRoot,
	}
}

func (r *PathEvidenceRegistry) Clone() *PathEvidenceRegistry {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	clone := NewPathEvidenceRegistry(r.workspaceRoot)
	for path, evidence := range r.paths {
		if evidence == nil {
			continue
		}
		copied := *evidence
		clone.paths[path] = &copied
	}
	return clone
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
		case tool == toolNameReadFile || tool == toolNamePrefetch:
			r.registerBodyRead(result)
		case tool == toolNameCodeNavigate || tool == toolNameReadOnlyCapability:
			r.registerBodyRead(result)
		case strings.HasSuffix(tool, "_read_symbol"), strings.HasSuffix(tool, "_extract_body"):
			r.registerBodyRead(result)
		case tool == toolNameSearchContent || tool == toolNameSearchFiles || tool == toolNameRg:
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
	if tool == toolNameReadFile || tool == toolNamePrefetch {
		if len(result.Paths) > 0 || (strings.Contains(text, "read ") && strings.Contains(text, " lines ")) {
			return EvidenceAnchorReady
		}
		return EvidenceNone
	}
	if tool == toolNameCodeNavigate || tool == toolNameReadOnlyCapability {
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

// registerCandidatePaths extracts workspace file paths from a discovery tool
// result (search_content, search_files, rg) and marks them as candidates.
// This seeds the registry with paths that the model can later read as source-body
// evidence, and ensures the completion gate's source-grounding check does not
// reject them as "discovered but never registered." When context_pack fails
// (e.g., readonly DB), search_content candidates provide the fallback path set.
func (r *PathEvidenceRegistry) registerCandidatePaths(result ToolResult) {
	paths := append([]string{}, result.Paths...)
	paths = append(paths, pathparse.MentionedWorkspaceFiles(result.Summary+"\n"+result.Output)...)
	if len(paths) == 0 {
		return
	}
	for _, p := range paths {
		p = normalizeRegistryPath(p)
		if p == "" {
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

// RegisterCandidatePath marks a path as a candidate in the registry.
func (r *PathEvidenceRegistry) RegisterCandidatePath(path, tool string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	p := normalizeRegistryPath(path)
	if p == "" {
		return
	}
	pe := r.getOrCreate(p)
	pe.IsCandidate = true
	pe.CandidatesFromPacket = tool
}

// SetCandidatePathsFromBrief seeds candidate paths from the brief's objective
// (paths explicitly mentioned in the user prompt).
func (r *PathEvidenceRegistry) SetCandidatePathsFromBrief(objective string) {
	if objective == "" {
		return
	}
	mentioned := pathparse.MentionedWorkspacePaths(objective)
	for _, p := range mentioned {
		p = normalizeRegistryPath(p)
		if p == "" {
			continue
		}
		pe := r.getOrCreate(p)
		pe.IsMentionedInPrompt = true
		pe.IsCandidate = true
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

// PathExists returns true if the path exists on disk (checked lazily via
// EnsurePathExists if not already cached).
func (r *PathEvidenceRegistry) PathExists(path string) bool {
	return r.EnsurePathExists(path)
}

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

// BodyReadPaths returns all paths that have body-level evidence.
func (r *PathEvidenceRegistry) BodyReadPaths() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []string
	for _, pe := range r.paths {
		if pe.BodyReadLevel >= EvidenceBodyWindow {
			result = append(result, pe.Path)
		}
	}
	slices.Sort(result)
	return result
}

// CandidatePaths returns all paths that are marked as candidates (from
// discovery tools or prompt mentions) but do NOT yet have body-level
// evidence. These are paths the model should consider reading.
func (r *PathEvidenceRegistry) CandidatePaths() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []string
	for _, pe := range r.paths {
		if pe.IsCandidate && pe.BodyReadLevel < EvidenceBodyWindow {
			result = append(result, pe.Path)
		}
	}
	slices.Sort(result)
	return result
}

// UnreadCandidatePaths returns candidate paths that have NOT yet been
// body-read. Used by the readiness gate to determine if the model should
// defer answering until candidate paths are explored.
func (r *PathEvidenceRegistry) UnreadCandidatePaths() []string {
	return r.CandidatePaths()
}

// AllPaths returns every path the registry knows about.
func (r *PathEvidenceRegistry) AllPaths() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []string
	for _, pe := range r.paths {
		result = append(result, pe.Path)
	}
	slices.Sort(result)
	return result
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
	path = strings.TrimPrefix(path, "./")
	return path
}

func toolIsOutlineCodeNavigate(result ToolResult) bool {
	tool := strings.TrimSpace(result.ToolName)
	return (tool == toolNameCodeNavigate || tool == toolNameReadOnlyCapability) &&
		strings.Contains(result.Summary, "outline")
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
		PromptHash: sha256Hex(prompt),
		ReadPaths:  sorted,
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

// --- evidence snapshot ---
type EvidenceForPath struct {
	Path          string
	Exists        bool
	BodyRead      bool
	Candidate     bool
	EvidenceLevel EvidenceLevel
}

type CandidatePath struct {
	Path string
}

type NamedPath struct {
	Name string
	Path string
}

type ObligationRef struct {
	ID   string
	Path string
}

type SourceOwnerEvidence struct {
	Path       string
	Symbol     string
	Role       string
	Confidence string
}

type Answerability struct {
	Answerable bool
	Reason     string
	Paths      []string
}

type HistoryEvidence struct {
	Path      string
	Satisfied bool
	Source    string
}

var evidenceSnapshotCounter atomic.Uint64

type EvidenceSnapshot struct {
	id                       string
	version                  int
	frozen                   bool
	registry                 *PathEvidenceRegistry
	namedPaths               []NamedPath
	pendingSourceObligations []ObligationRef
	sourceOwners             []SourceOwnerEvidence
	answerability            Answerability
	history                  []HistoryEvidence
}

func NewSnapshot(registry *PathEvidenceRegistry) EvidenceSnapshot {
	id := fmt.Sprintf("evidence_snapshot_%d", evidenceSnapshotCounter.Add(1))
	return EvidenceSnapshot{id: id, registry: registry}
}

func (s EvidenceSnapshot) ID() string {
	return s.id
}

func (s EvidenceSnapshot) Version() int {
	return s.version
}

func (s EvidenceSnapshot) Frozen() bool {
	return s.frozen
}

func (s EvidenceSnapshot) Freeze() EvidenceSnapshot {
	frozen := EvidenceSnapshot{
		id:                       s.id,
		version:                  s.version,
		frozen:                   true,
		registry:                 s.registry.Clone(),
		namedPaths:               append([]NamedPath(nil), s.namedPaths...),
		pendingSourceObligations: append([]ObligationRef(nil), s.pendingSourceObligations...),
		sourceOwners:             append([]SourceOwnerEvidence(nil), s.sourceOwners...),
		answerability:            s.answerability,
		history:                  append([]HistoryEvidence(nil), s.history...),
	}
	frozen.answerability.Paths = append([]string(nil), s.answerability.Paths...)
	return frozen
}

func (s EvidenceSnapshot) BodyRead(path string) EvidenceForPath {
	if s.registry == nil {
		return EvidenceForPath{Path: normalizeRegistryPath(path)}
	}
	normalized := normalizeRegistryPath(path)
	s.registry.mu.RLock()
	pe, ok := s.registry.paths[normalized]
	s.registry.mu.RUnlock()
	if !ok {
		return EvidenceForPath{Path: normalized, Exists: s.registry.PathExists(normalized)}
	}
	return EvidenceForPath{
		Path:          pe.Path,
		Exists:        pe.ExistsOnDisk,
		BodyRead:      pe.BodyReadLevel >= EvidenceBodyWindow,
		Candidate:     pe.IsCandidate,
		EvidenceLevel: pe.BodyReadLevel,
	}
}

func (s EvidenceSnapshot) CandidatePaths() []CandidatePath {
	if s.registry == nil {
		return nil
	}
	paths := s.registry.CandidatePaths()
	candidates := make([]CandidatePath, 0, len(paths))
	for _, path := range paths {
		candidates = append(candidates, CandidatePath{Path: path})
	}
	return candidates
}

func (s EvidenceSnapshot) BodyReadPaths() []string {
	if s.registry == nil {
		return nil
	}
	return s.registry.BodyReadPaths()
}

func (s EvidenceSnapshot) SourceOwner(path string) SourceOwnerEvidence {
	normalized := normalizeRegistryPath(path)
	for _, evidence := range s.sourceOwners {
		if normalizeRegistryPath(evidence.Path) == normalized {
			return evidence
		}
	}
	return SourceOwnerEvidence{Path: normalized}
}

func (s EvidenceSnapshot) NamedPaths() []NamedPath {
	return append([]NamedPath(nil), s.namedPaths...)
}

func (s EvidenceSnapshot) PendingSourceObligations() []ObligationRef {
	return append([]ObligationRef(nil), s.pendingSourceObligations...)
}

func (s EvidenceSnapshot) ReadOnlyAnswerability() Answerability {
	answerability := s.answerability
	answerability.Paths = append([]string(nil), answerability.Paths...)
	return answerability
}

func (s EvidenceSnapshot) GitHistory(path string) HistoryEvidence {
	normalized := normalizeRegistryPath(path)
	for _, evidence := range s.history {
		if normalizeRegistryPath(evidence.Path) == normalized {
			return evidence
		}
	}
	return HistoryEvidence{Path: normalized}
}

func (s *EvidenceSnapshot) Promote(result ToolResult) Delta {
	if s.registry == nil || s.frozen {
		return Delta{}
	}
	beforeBody := evidenceStringSet(s.registry.BodyReadPaths())
	beforeCandidates := evidenceStringSet(s.registry.CandidatePaths())
	s.registry.RegisterToolResults([]ToolResult{result})
	delta := evidenceDeltaFromFacts(result.SidecarFacts)
	for _, path := range delta.CandidatePaths {
		s.registry.RegisterCandidatePath(path, "evidence_fact")
	}
	s.namedPaths = mergeNamedPaths(s.namedPaths, delta.NamedPaths)
	s.pendingSourceObligations = mergeObligationRefs(s.pendingSourceObligations, delta.PendingSourceObligations)
	s.sourceOwners = mergeSourceOwnerEvidence(s.sourceOwners, delta.SourceOwner)
	s.history = mergeHistoryEvidence(s.history, delta.GitHistory)
	beforeAnswerability := s.answerability
	if answerability, ok := answerabilityFromFacts(result.SidecarFacts); ok {
		s.answerability = answerability
	}

	afterBody := s.registry.BodyReadPaths()
	afterCandidates := s.registry.CandidatePaths()
	delta.BodyReadPaths = newStrings(afterBody, beforeBody)
	delta.CandidatePaths = newStrings(afterCandidates, beforeCandidates)
	delta.Facts = append([]EvidenceFact(nil), result.SidecarFacts...)
	if deltaHasStateChange(delta) || !sameAnswerability(beforeAnswerability, s.answerability) {
		s.version++
	}
	return delta
}

func deltaHasStateChange(delta Delta) bool {
	return len(delta.BodyReadPaths) > 0 ||
		len(delta.CandidatePaths) > 0 ||
		len(delta.NamedPaths) > 0 ||
		len(delta.PendingSourceObligations) > 0 ||
		len(delta.SourceOwner) > 0 ||
		len(delta.GitHistory) > 0
}

func sameAnswerability(a, b Answerability) bool {
	return a.Answerable == b.Answerable &&
		a.Reason == b.Reason &&
		slices.Equal(a.Paths, b.Paths)
}

func (r *PathEvidenceRegistry) Snapshot() EvidenceSnapshot {
	return NewSnapshot(r)
}

func evidenceStringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func newStrings(values []string, prior map[string]struct{}) []string {
	var delta []string
	for _, value := range values {
		if _, ok := prior[value]; ok {
			continue
		}
		delta = append(delta, value)
	}
	return delta
}

func evidenceDeltaFromFacts(facts []EvidenceFact) Delta {
	var delta Delta
	for _, fact := range facts {
		key := strings.TrimSpace(fact.Key)
		value := strings.TrimSpace(fact.Value)
		if key == "" || value == "" {
			continue
		}
		switch key {
		case "candidate_path":
			if path := normalizeRegistryPath(value); path != "" {
				delta.CandidatePaths = append(delta.CandidatePaths, path)
			}
		case "named_path":
			if named, ok := parseNamedPathFact(value); ok {
				delta.NamedPaths = append(delta.NamedPaths, named)
			}
		case "pending_source_obligation":
			if obligation, ok := parseObligationFact(value); ok {
				delta.PendingSourceObligations = append(delta.PendingSourceObligations, obligation)
			}
		case "source_owner":
			if owner, ok := parseSourceOwnerFact(value); ok {
				delta.SourceOwner = append(delta.SourceOwner, owner)
			}
		case "git_history":
			if history, ok := parseHistoryFact(value); ok {
				delta.GitHistory = append(delta.GitHistory, history)
			}
		}
	}
	return delta
}

func parseNamedPathFact(value string) (NamedPath, bool) {
	parts := strings.SplitN(value, "=", 2)
	if len(parts) != 2 {
		return NamedPath{}, false
	}
	path := normalizeRegistryPath(parts[1])
	if path == "" {
		return NamedPath{}, false
	}
	return NamedPath{Name: strings.TrimSpace(parts[0]), Path: path}, true
}

func parseObligationFact(value string) (ObligationRef, bool) {
	id, path := splitFactIDPath(value)
	if id == "" {
		return ObligationRef{}, false
	}
	return ObligationRef{ID: id, Path: path}, true
}

func parseSourceOwnerFact(value string) (SourceOwnerEvidence, bool) {
	fields := splitFactFields(value)
	path := normalizeRegistryPath(fields["path"])
	if path == "" {
		return SourceOwnerEvidence{}, false
	}
	return SourceOwnerEvidence{
		Path:       path,
		Symbol:     fields["symbol"],
		Role:       fields["role"],
		Confidence: fields["confidence"],
	}, true
}

func parseHistoryFact(value string) (HistoryEvidence, bool) {
	fields := splitFactFields(value)
	path := normalizeRegistryPath(fields["path"])
	if path == "" {
		return HistoryEvidence{}, false
	}
	return HistoryEvidence{
		Path:      path,
		Satisfied: truthyFactValue(fields["satisfied"]),
		Source:    fields["source"],
	}, true
}

func answerabilityFromFacts(facts []EvidenceFact) (Answerability, bool) {
	answerability := Answerability{}
	ok := false
	for _, fact := range facts {
		key := strings.TrimSpace(fact.Key)
		value := strings.TrimSpace(fact.Value)
		switch key {
		case "answerability.answerable":
			answerability.Answerable = truthyFactValue(value)
			ok = true
		case "answerability.reason":
			answerability.Reason = value
			ok = true
		case "answerability.path":
			if path := normalizeRegistryPath(value); path != "" && !slices.Contains(answerability.Paths, path) {
				answerability.Paths = append(answerability.Paths, path)
				ok = true
			}
		}
	}
	slices.Sort(answerability.Paths)
	return answerability, ok
}

func splitFactIDPath(value string) (string, string) {
	fields := splitFactFields(value)
	id := strings.TrimSpace(fields["id"])
	path := normalizeRegistryPath(fields["path"])
	if id == "" && !strings.Contains(value, "=") {
		id = strings.TrimSpace(value)
	}
	return id, path
}

func splitFactFields(value string) map[string]string {
	fields := make(map[string]string)
	for _, rawField := range strings.Split(value, ";") {
		key, val, ok := strings.Cut(rawField, "=")
		if !ok {
			continue
		}
		fields[strings.TrimSpace(key)] = strings.TrimSpace(val)
	}
	return fields
}

func truthyFactValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "satisfied", "done":
		return true
	default:
		return false
	}
}

func mergeNamedPaths(existing []NamedPath, delta []NamedPath) []NamedPath {
	out := append([]NamedPath(nil), existing...)
	for _, next := range delta {
		replace := false
		for i, current := range out {
			if current.Name == next.Name {
				out[i] = next
				replace = true
				break
			}
		}
		if !replace {
			out = append(out, next)
		}
	}
	slices.SortFunc(out, func(a, b NamedPath) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func mergeObligationRefs(existing []ObligationRef, delta []ObligationRef) []ObligationRef {
	out := append([]ObligationRef(nil), existing...)
	for _, next := range delta {
		if !slices.ContainsFunc(out, func(current ObligationRef) bool { return current.ID == next.ID && current.Path == next.Path }) {
			out = append(out, next)
		}
	}
	slices.SortFunc(out, func(a, b ObligationRef) int { return strings.Compare(a.ID+a.Path, b.ID+b.Path) })
	return out
}

func mergeSourceOwnerEvidence(existing []SourceOwnerEvidence, delta []SourceOwnerEvidence) []SourceOwnerEvidence {
	out := append([]SourceOwnerEvidence(nil), existing...)
	for _, next := range delta {
		replace := false
		for i, current := range out {
			if normalizeRegistryPath(current.Path) == normalizeRegistryPath(next.Path) {
				out[i] = next
				replace = true
				break
			}
		}
		if !replace {
			out = append(out, next)
		}
	}
	return out
}

func mergeHistoryEvidence(existing []HistoryEvidence, delta []HistoryEvidence) []HistoryEvidence {
	out := append([]HistoryEvidence(nil), existing...)
	for _, next := range delta {
		replace := false
		for i, current := range out {
			if normalizeRegistryPath(current.Path) == normalizeRegistryPath(next.Path) {
				out[i] = next
				replace = true
				break
			}
		}
		if !replace {
			out = append(out, next)
		}
	}
	return out
}

type EvidenceFact struct {
	Key   string
	Value string
}

// ToolResult is the evidence-owned DTO for ingesting workspace results.
// Workspace adapters map concrete workspace.ToolResult and sidecar envelopes to
// this type before evidence code interprets paths, source bodies, or ownership.
type ToolResult struct {
	ToolName     string
	Summary      string
	Output       string
	Error        string
	Paths        []string
	SidecarFacts []EvidenceFact
	Metadata     map[string]string
}

type Delta struct {
	BodyReadPaths            []string
	CandidatePaths           []string
	NamedPaths               []NamedPath
	PendingSourceObligations []ObligationRef
	SourceOwner              []SourceOwnerEvidence
	GitHistory               []HistoryEvidence
	Facts                    []EvidenceFact
}
