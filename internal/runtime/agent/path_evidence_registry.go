package agent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
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

// ---------------------------------------------------------------------------
// Population
// ---------------------------------------------------------------------------

// RegisterToolResults ingests a slice of ToolResults and updates the registry
// with body-read evidence, candidate paths, and existence status.
func (r *PathEvidenceRegistry) RegisterToolResults(results []workspace.ToolResult) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		tool := strings.TrimSpace(result.Tool)

		switch {
		case tool == workspace.ToolNameReadFile || tool == workspace.ToolNamePrefetch:
			r.registerBodyRead(result)
		case tool == workspace.ToolNameCodeNavigate || tool == workspace.ToolNameReadOnlyCapability:
			r.registerBodyRead(result)
		case strings.HasSuffix(tool, "_read_symbol"), strings.HasSuffix(tool, "_extract_body"):
			r.registerBodyRead(result)
		case isCandidateProducingTool(tool):
			r.registerCandidatePaths(result)
		}
	}
}

func (r *PathEvidenceRegistry) registerBodyRead(result workspace.ToolResult) {
	// Extract path from the tool result's Summary (which begins with "Read" or "Routed")
	paths := pathparse.MentionedWorkspaceFiles(result.Summary)
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
		// Bump the evidence level: code_navigate outlines get body_window level
		if toolIsOutlineCodeNavigate(result) {
			if pe.BodyReadLevel < EvidenceBodyWindow {
				pe.BodyReadLevel = EvidenceBodyWindow
			}
		} else {
			// Full reads or symbol reads get max level
			pe.BodyReadLevel = EvidenceAnchorReady
		}
	}
}

func (r *PathEvidenceRegistry) registerCandidatePaths(result workspace.ToolResult) {
	candidates := sourceEvidenceCandidatePaths([]workspace.ToolResult{result})
	for _, p := range candidates {
		p = normalizeRegistryPath(p)
		if p == "" {
			continue
		}
		pe := r.getOrCreate(p)
		pe.IsCandidate = true
		pe.CandidatesFromPacket = result.Tool
	}
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

	pe, ok := r.paths[p]
	if ok {
		return pe.ExistsOnDisk
	}

	// First contact: check the filesystem
	root := "."
	if r.workspaceRoot != "" {
		root = r.workspaceRoot
	}
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(p)))
	exists := err == nil && !info.IsDir()

	r.paths[p] = &PathEvidence{
		Path:         p,
		ExistsOnDisk: exists,
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
				Path:         p,
				IsCandidate:  true,
				ExistsOnDisk: r.existsOnDiskUnsafe(p),
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

// isCandidateProducingTool returns true if the tool may return candidate paths
// that the model should explore.
func isCandidateProducingTool(tool string) bool {
	switch tool {
	case "context_pack", "context_query", "context_zoom",
		"discovery_query", "structural_search",
		"search_content", "search_files",
		"rust_overview", "go_overview", "python_overview",
		"cpp_overview", "ts_overview",
		"code_navigate",
		"task":
		return true
	default:
		return false
	}
}

func toolIsOutlineCodeNavigate(result workspace.ToolResult) bool {
	tool := strings.TrimSpace(result.Tool)
	return (tool == workspace.ToolNameCodeNavigate || tool == workspace.ToolNameReadOnlyCapability) &&
		strings.Contains(result.Summary, "outline")
}

func extractPathFromResult(result workspace.ToolResult) string {
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
