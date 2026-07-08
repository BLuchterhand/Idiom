package agent

import (
	"os"
	"path/filepath"
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

// PathEvidence is the per-path record maintained by the registry.
type PathEvidence struct {
	Path                 string // canonical workspace-relative path
	ExistsOnDisk         bool   // populated at registry construction
	ExistenceChecked     bool   // true once ExistsOnDisk has been resolved
	BodyReadLevel        PlanThenExecuteSourceEvidenceLevel
	IsCandidate          bool   // returned by context_pack / context_query / discovery
	CandidatesFromPacket string // name of the packet/tool that produced this candidate
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
		workspaceResult := workspace.ToolResult{
			Tool:         tool,
			Summary:      result.Summary,
			Output:       result.Output,
			Error:        result.Error,
			Paths:        append([]string(nil), result.Paths...),
			ChangedPaths: append([]string(nil), result.ChangedPaths...),
		}

		switch {
		case tool == workspace.ToolNameReadFile || tool == workspace.ToolNamePrefetch,
			tool == workspace.ToolNameCodeNavigate || tool == workspace.ToolNameReadOnlyCapability,
			strings.HasSuffix(tool, "_read_symbol"), strings.HasSuffix(tool, "_extract_body"),
			tool == workspace.ToolNameContextZoom && contextZoomResultHasConcreteSourceBody(workspaceResult):

			r.registerBodyRead(workspaceResult)
		}

		if tool == workspace.ToolNameContextPack || (toolResultEvidenceProjection{}).DiscoveryCandidateTool(tool) || workspace.IsSearchToolName(tool) {
			r.registerCandidatePaths(workspaceResult)
		}
	}
}

func (r *PathEvidenceRegistry) registerBodyRead(result workspace.ToolResult) {
	for _, p := range ReadPathsFromToolResult(result) {
		p = normalizeRegistryPath(p)
		if p == "" {
			continue
		}
		if IsNonSourceEvidenceArtifactPath(p) || !pathparse.HasWorkspaceFileExtension(p) {
			continue
		}
		evidence := planThenExecuteToolResultSourceEvidence(result, p)
		if !sourceEvidenceHasBody(evidence) {
			continue
		}
		pe := r.getOrCreate(p)
		// The path was successfully read by a tool — mark it as existing on disk.
		// This avoids the EnsurePathExists cache returning stale false for entries
		// created here with getOrCreate's zero-value ExistsOnDisk.
		pe.ExistsOnDisk = true
		pe.ExistenceChecked = true
		if planThenExecuteEvidenceRank(pe.BodyReadLevel) < planThenExecuteEvidenceRank(evidence.Level) {
			pe.BodyReadLevel = evidence.Level
		}
	}
}

// registerCandidatePaths extracts workspace file paths from a discovery or
// search tool result and marks them as candidates.
// This seeds the registry with paths that the model can later read as source-body
// evidence, and ensures the completion gate's source-grounding check does not
// reject them as "discovered but never registered." When context_pack fails
// (e.g., readonly DB), search_content candidates provide the fallback path set.
func (r *PathEvidenceRegistry) registerCandidatePaths(result workspace.ToolResult) {
	paths := append([]string{}, result.Paths...)
	paths = append(paths, (toolResultEvidenceProjection{}).CandidatePathsFromResult(result, 128)...)

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
			pe.CandidatesFromPacket = result.Tool
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
	return planThenExecuteEvidenceRank(pe.BodyReadLevel) >= planThenExecuteEvidenceRank(planThenExecuteEvidenceBodyWindow)
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

// ToolResult is the evidence-owned DTO for ingesting workspace results.
// Workspace adapters map concrete workspace.ToolResult and sidecar envelopes to
// this type before evidence code interprets paths, source bodies, or ownership.
type ToolResult struct {
	ToolName     string
	Summary      string
	Output       string
	Error        string
	Paths        []string
	ChangedPaths []string
	Metadata     map[string]string
}
