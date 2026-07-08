package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"idiom/internal/platform/gitstate"
	"idiom/internal/platform/textutil"
	"idiom/internal/repository/comprehension"
	semanticlanguage "idiom/internal/repository/semantic/language"
)

const (
	discoveryQuerySchemaVersion = 1
	discoveryQueryOutputLines   = 80
	discoveryQueryOutputChars   = 12000
)

const (
	DiscoveryQueryPurposeImpactAnalysis       = "impact_analysis"
	DiscoveryQueryPurposeFunctionTracing      = "function_tracing"
	DiscoveryQueryPurposeArchitectureBoundary = "architecture_boundary"
	DiscoveryQueryPurposeSecurityReview       = "security_review"
	DiscoveryQueryPurposeHistoryContext       = "history_context"
)

func DiscoveryQueryPurposeValues() []string {
	return []string{
		DiscoveryQueryPurposeImpactAnalysis,
		DiscoveryQueryPurposeFunctionTracing,
		DiscoveryQueryPurposeArchitectureBoundary,
		DiscoveryQueryPurposeSecurityReview,
		DiscoveryQueryPurposeHistoryContext,
	}
}

type discoveryQueryPacket struct {
	SchemaVersion        int                                  `json:"schema_version"`
	ID                   string                               `json:"id"`
	Purpose              string                               `json:"purpose"`
	Query                string                               `json:"query,omitempty"`
	Target               string                               `json:"target,omitempty"`
	Pattern              string                               `json:"pattern,omitempty"`
	Language             string                               `json:"language,omitempty"`
	Depth                int                                  `json:"depth"`
	GeneratedAt          string                               `json:"generated_at"`
	ArtifactPath         string                               `json:"artifact_path"`
	ContextIndexID       string                               `json:"context_index_id,omitempty"`
	ContextViews         []string                             `json:"context_views,omitempty"`
	ContextFreshness     *comprehension.FreshnessDecision     `json:"context_freshness,omitempty"`
	ContextStaleEvidence *comprehension.StaleEvidenceSafety   `json:"context_stale_evidence,omitempty"`
	ContextConfidence    *comprehension.GraphResultConfidence `json:"context_confidence,omitempty"`
	ContextProvenance    *comprehension.GraphResultProvenance `json:"context_provenance,omitempty"`
	Layers               []discoveryQueryLayer                `json:"layers"`
	Gaps                 []string                             `json:"gaps,omitempty"`
	Status               string                               `json:"status,omitempty"`
	TimeoutOwner         string                               `json:"timeout_owner,omitempty"`
	SkippedLayers        []string                             `json:"skipped_layers,omitempty"`
}

type discoveryQueryLayer struct {
	Name          string         `json:"name"`
	Tool          string         `json:"tool"`
	Request       map[string]any `json:"request,omitempty"`
	Summary       string         `json:"summary"`
	Error         string         `json:"error,omitempty"`
	OutputExcerpt string         `json:"output_excerpt,omitempty"`
	OutputLines   int            `json:"output_lines,omitempty"`
	OutputChars   int            `json:"output_chars,omitempty"`
	PrecisionTier string         `json:"precision_tier,omitempty"`
}

type discoveryQuerySummary struct {
	ID                   string                               `json:"id"`
	Purpose              string                               `json:"purpose"`
	ArtifactPath         string                               `json:"artifact_path"`
	ContextIndexID       string                               `json:"context_index_id,omitempty"`
	ContextViews         []string                             `json:"context_views,omitempty"`
	ContextFreshness     *comprehension.FreshnessDecision     `json:"context_freshness,omitempty"`
	ContextStaleEvidence *comprehension.StaleEvidenceSafety   `json:"context_stale_evidence,omitempty"`
	ContextConfidence    *comprehension.GraphResultConfidence `json:"context_confidence,omitempty"`
	ContextProvenance    *comprehension.GraphResultProvenance `json:"context_provenance,omitempty"`
	LayersRun            []string                             `json:"layers_run"`
	Evidence             []struct {
		Name          string `json:"name"`
		Tool          string `json:"tool"`
		Summary       string `json:"summary"`
		Error         string `json:"error,omitempty"`
		OutputExcerpt string `json:"output_excerpt,omitempty"`
	} `json:"evidence,omitempty"`
	Errors        []string `json:"errors,omitempty"`
	Gaps          []string `json:"gaps,omitempty"`
	Status        string   `json:"status,omitempty"`
	SkippedLayers []string `json:"skipped_layers,omitempty"`
	TimeoutOwner  string   `json:"timeout_owner,omitempty"`
}

type discoveryQueryRunner struct {
	manager *Manager
	ctx     context.Context
	packet  discoveryQueryPacket
	seen    map[string]struct{}
	stopped bool
}

func (m *Manager) discoveryQuery(ctx context.Context, req ToolRequest) ToolResult {
	purpose := NormalizeDiscoveryQueryPurpose(textutil.FirstNonEmpty(req.Purpose, req.Query, DiscoveryQueryPurposeImpactAnalysis))
	if purpose == "" {
		return ToolResult{Tool: ToolNameDiscoveryQuery, Summary: "Discovery query rejected", Error: "purpose must be one of " + strings.Join(DiscoveryQueryPurposeValues(), ", ")}
	}

	targetAbs, err := m.pathAccess.ResolvePath(defaultString(req.Path, "."), req.Workdir)
	if err != nil {
		return ToolResult{Tool: ToolNameDiscoveryQuery, Summary: "Failed to resolve discovery query target", Error: err.Error()}
	}

	targetRel := m.pathAccess.DisplayPath(targetAbs)
	depth := req.MaxDepth

	if depth <= 0 {
		depth = 2
	}

	if depth > 4 {
		depth = 4
	}

	language := normalizeDiscoveryQueryLanguage(req.Language)
	if language == "" {
		language = normalizeDiscoveryQueryLanguage(m.inferDependencyLanguage(targetAbs))
	}

	artifactRel := filepath.ToSlash(filepath.Join(".idiom", "discovery", "queries", discoveryQueryID(m.cfg.Root, purpose, req, targetRel, language, depth)+".json"))
	runner := &discoveryQueryRunner{
		manager: m,
		ctx:     ctx,
		seen:    make(map[string]struct{}),
		packet: discoveryQueryPacket{
			SchemaVersion: discoveryQuerySchemaVersion,
			ID:            strings.TrimSuffix(filepath.Base(artifactRel), ".json"),
			Purpose:       purpose,
			Query:         strings.TrimSpace(req.Query),
			Target:        targetRel,
			Pattern:       strings.TrimSpace(req.Pattern),
			Language:      language,
			Depth:         depth,
			GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
			ArtifactPath:  artifactRel,
		},
	}

	runner.run(req, targetAbs)

	if err := runner.writeArtifact(); err != nil {
		return ToolResult{Tool: ToolNameDiscoveryQuery, Summary: "Discovery query artifact write failed", Error: err.Error(), Output: runner.summaryJSON()}
	}

	return ToolResult{
		Tool:    ToolNameDiscoveryQuery,
		Summary: fmt.Sprintf("Ran %s discovery query with %d layer(s); artifact: %s", purpose, len(runner.packet.Layers), artifactRel),
		Output:  runner.summaryJSON(),
	}
}

func (r *discoveryQueryRunner) run(req ToolRequest, targetAbs string) {
	purpose := r.packet.Purpose
	path := defaultString(req.Path, ".")
	pattern := strings.TrimSpace(req.Pattern)
	language := r.packet.Language

	contextBacked := r.addContextGraphLayer(req, path, pattern, language)
	if r.stopped {
		return
	}
	r.addContextLayer()
	if r.stopped {
		return
	}

	if isRegularFile(targetAbs) {
		r.addFileOrientation(path, language)
	} else if contextBacked && purpose != DiscoveryQueryPurposeSecurityReview {
		r.packet.Gaps = append(r.packet.Gaps, "fresh context graph supplied target orientation; skipped live target listing")
	} else {
		r.addLayer(DiscoveryLayerTargetListing, ToolRequest{Tool: ToolNameListFiles, Path: path, Recursive: true, MaxDepth: 2})
	}
	if r.stopped {
		return
	}

	if pattern != "" {
		r.addLayer(DiscoveryLayerTextAnchorSearch, ToolRequest{Tool: ToolNameSearchContent, Path: path, Pattern: pattern, Glob: globForDiscoveryLanguage(language)})
	}
	if r.stopped {
		return
	}

	switch purpose {
	case DiscoveryQueryPurposeImpactAnalysis:
		r.addDependency(path, language)
		r.addLanguageRelationshipLayers(path, pattern, language, true)
		r.addRelatedFileLayer(path, pattern, language)
		r.addHistoryLayers(path, pattern, req)
	case DiscoveryQueryPurposeFunctionTracing:
		r.addLanguageRelationshipLayers(path, pattern, language, true)
		r.addRelatedFileLayer(path, pattern, language)
		r.addCtagsFallback(path, pattern)
	case DiscoveryQueryPurposeArchitectureBoundary:
		r.addDependency(path, language)
		r.addRelatedFileLayer(path, pattern, language)
		r.addLanguageRelationshipLayers(path, pattern, language, false)
		r.addCtagsFallback(path, pattern)
	case DiscoveryQueryPurposeSecurityReview:
		r.addDependency(path, language)
		r.addLayer(DiscoveryLayerSecurityTextSweep, ToolRequest{Tool: ToolNameSearchContent, Path: path, Pattern: `eval\(|exec\(|password|secret|token|unsafe|panic\(`})
		r.addStructuralLayer(path, req)
		r.addLayer(DiscoveryLayerSemgrepScan, ToolRequest{Tool: ToolNameSemgrepScan, Path: path, Pattern: defaultString(pattern, "auto")})
		r.addLayer(DiscoveryLayerJoernScan, ToolRequest{Tool: ToolNameJoernScan, Path: path})
	case DiscoveryQueryPurposeHistoryContext:
		r.addHistoryLayers(path, pattern, req)
		r.addRelatedFileLayer(path, pattern, language)
	default:
		r.packet.Gaps = append(r.packet.Gaps, "unknown purpose; ran only common orientation layers")
	}
}

type discoveryContextPackSummary struct {
	PackID         string                              `json:"pack_id"`
	IndexID        string                              `json:"index_id"`
	ArtifactPath   string                              `json:"artifact_path"`
	Fresh          bool                                `json:"fresh"`
	Freshness      *comprehension.FreshnessDecision    `json:"freshness,omitempty"`
	Confidence     comprehension.GraphResultConfidence `json:"confidence"`
	Provenance     comprehension.GraphResultProvenance `json:"provenance"`
	UnresolvedGaps []string                            `json:"unresolved_gaps"`
}

func (r *discoveryQueryRunner) addContextGraphLayer(req ToolRequest, path string, pattern string, language string) bool {
	if r.stopBeforeLayer(DiscoveryLayerContextPack) {
		return false
	}

	status, err := comprehension.Status(r.manager.cfg.Root)
	if err != nil {
		return false
	}

	if status.IndexID != "" {
		r.packet.ContextIndexID = status.IndexID
	}

	task := discoveryContextPackTask(r.packet.Purpose, path, pattern, language, req.Query)

	freshness, err := comprehension.EnsureFresh(r.ctx, comprehension.FreshnessRequest{
		Root:      r.manager.cfg.Root,
		Operation: ToolNameDiscoveryQuery,
		Policy:    contextFreshnessPolicy(req, comprehension.FreshnessReadThrough),
		Path:      path,
		Query:     task,
		Language:  language,
		Mode:      "plan",
		MaxFiles:  req.FreshnessMaxFiles,
		Enrichers: r.manager.contextGraphEnrichers("plan"),
		Status:    &status,
	})
	if err != nil {
		if r.stopAfterLayer(DiscoveryLayerContextPack) {
			return false
		}
		r.packet.Gaps = append(r.packet.Gaps, "context freshness check failed: "+err.Error())
		return false
	}

	r.packet.ContextFreshness = &freshness

	staleEvidence := comprehension.StaleEvidenceSafetyForFreshness(freshness)

	r.packet.ContextStaleEvidence = &staleEvidence
	status = comprehension.StatusReportWithMetadata(status, ToolNameDiscoveryQuery, &freshness)

	contextConfidence := status.Confidence
	contextProvenance := status.Provenance

	r.packet.ContextConfidence = &contextConfidence
	r.packet.ContextProvenance = &contextProvenance

	if refreshed, err := comprehension.Status(r.manager.cfg.Root); err == nil && refreshed.IndexID != "" {
		r.packet.ContextIndexID = refreshed.IndexID
	}

	if !freshness.FreshForScope && freshness.Policy != comprehension.FreshnessAllowStale {
		r.packet.Gaps = append(r.packet.Gaps, comprehension.FreshnessGapForDecision(freshness))
		return false
	}

	result, ok := r.addLayerResult(DiscoveryLayerContextPack, ToolRequest{
		Tool:              ToolNameContextPack,
		Task:              task,
		Path:              path,
		Mode:              "plan",
		Limit:             8,
		FreshnessPolicy:   string(freshness.Policy),
		FreshnessMaxFiles: req.FreshnessMaxFiles,
	})
	if !ok || strings.TrimSpace(result.Error) != "" {
		return false
	}

	var pack discoveryContextPackSummary

	if err := json.Unmarshal([]byte(result.Output), &pack); err != nil {
		r.packet.Gaps = append(r.packet.Gaps, "context_pack output could not be decoded; used live discovery layers")
		return false
	}

	if pack.IndexID != "" {
		r.packet.ContextIndexID = pack.IndexID
	}

	if r.packet.ContextFreshness == nil && pack.Freshness != nil {
		r.packet.ContextFreshness = pack.Freshness
	}

	if r.packet.ContextStaleEvidence == nil {
		packStaleEvidence := comprehension.StaleEvidenceSafetyForFreshness(freshness)

		r.packet.ContextStaleEvidence = &packStaleEvidence
	}

	if pack.Confidence.Level != "" {
		contextConfidence := pack.Confidence

		r.packet.ContextConfidence = &contextConfidence
	}

	if pack.Provenance.Source != "" {
		contextProvenance := pack.Provenance

		r.packet.ContextProvenance = &contextProvenance
	}

	if pack.ArtifactPath != "" && !slices.Contains(r.packet.ContextViews, pack.ArtifactPath) {
		r.packet.ContextViews = append(r.packet.ContextViews, pack.ArtifactPath)
	}

	if !pack.Fresh {
		r.packet.Gaps = append(r.packet.Gaps, "context_pack reported stale context; live discovery layers remain authoritative")
		return false
	}

	for _, gap := range pack.UnresolvedGaps {
		gap = strings.TrimSpace(gap)
		if gap != "" {
			r.packet.Gaps = append(r.packet.Gaps, "context_pack: "+gap)
		}
	}

	return true
}

func discoveryContextPackTask(purpose string, path string, pattern string, language string, query string) string {
	parts := []string{"Discovery query", purpose}
	if strings.TrimSpace(query) != "" {
		parts = append(parts, strings.TrimSpace(query))
	}

	if strings.TrimSpace(pattern) != "" {
		parts = append(parts, "pattern "+strings.TrimSpace(pattern))
	}

	if strings.TrimSpace(path) != "" {
		parts = append(parts, "path "+strings.TrimSpace(path))
	}

	if strings.TrimSpace(language) != "" {
		parts = append(parts, "language "+strings.TrimSpace(language))
	}

	return strings.Join(parts, " ")
}

func (r *discoveryQueryRunner) addContextLayer() {
	if r.stopBeforeLayer(DiscoveryLayerProjectContextDigest) {
		return
	}

	path := filepath.Join(r.manager.cfg.Root, ".idiom", "discovery", "context.md")

	data, err := os.ReadFile(path)
	if err != nil {
		r.packet.Gaps = append(r.packet.Gaps, ".idiom/discovery/context.md was not present")
		return
	}

	r.packet.Layers = append(r.packet.Layers, discoveryQueryLayer{
		Name:          DiscoveryLayerProjectContextDigest,
		Tool:          "artifact_read",
		Request:       map[string]any{"path": ".idiom/discovery/context.md"},
		Summary:       "Read generated project context digest",
		OutputExcerpt: compactDiscoveryQueryOutput(string(data)),
		OutputLines:   countNonEmptyOutputLines(string(data)),
		OutputChars:   len(data),
	})
}

func (r *discoveryQueryRunner) addFileOrientation(path string, language string) {
	if lang, ok := semanticlanguage.LanguageForName(language); ok {
		if tool := lang.Tool(semanticlanguage.CapFileOutline); tool != "" {
			r.addLayer(DiscoveryLayerFileOutline, ToolRequest{Tool: tool, Path: path})

			return
		}
	}

	r.addLayer(DiscoveryLayerFileRead, ToolRequest{Tool: ToolNameReadFile, Path: path, StartLine: 1, EndLine: 160})
}

func (r *discoveryQueryRunner) addDependency(path string, language string) {
	r.addLayer(DiscoveryLayerDependencyGraph, ToolRequest{Tool: ToolNameDependencyGraph, Path: path, Language: language})
}

func (r *discoveryQueryRunner) addLanguageRelationshipLayers(path string, pattern string, language string, includeCalls bool) {
	lang, ok := semanticlanguage.LanguageForName(language)
	if !ok {
		if pattern != "" {
			r.addCtagsFallback(path, pattern)
		}

		return
	}

	if pattern == "" {
		r.packet.Gaps = append(r.packet.Gaps, "pattern was empty; skipped symbol relationship layers")
		return
	}

	for _, capability := range []semanticlanguage.Capability{
		semanticlanguage.CapWorkspaceSymbol,
		semanticlanguage.CapDefinition,
		semanticlanguage.CapReferences,
		semanticlanguage.CapReadSymbol,
	} {
		if tool := lang.Tool(capability); tool != "" {
			r.addLayer(tool, ToolRequest{Tool: tool, Path: path, Pattern: pattern, MaxDepth: r.packet.Depth})
		}
	}

	if includeCalls {
		for _, capability := range []semanticlanguage.Capability{semanticlanguage.CapCallsFrom, semanticlanguage.CapCallsInto} {
			if tool := lang.Tool(capability); tool != "" {
				r.addLayer(tool, ToolRequest{Tool: tool, Path: path, Pattern: pattern, MaxDepth: r.packet.Depth})
			}
		}
	}
}

func (r *discoveryQueryRunner) addRelatedFileLayer(path string, pattern string, language string) {
	lang, ok := semanticlanguage.LanguageForName(language)
	if !ok {
		return
	}

	if tool := lang.Tool(semanticlanguage.CapRelatedFiles); tool != "" {
		r.addLayer(DiscoveryLayerRelatedFiles, ToolRequest{Tool: tool, Path: path, Pattern: pattern, MaxDepth: r.packet.Depth})
	}
}

func (r *discoveryQueryRunner) addHistoryLayers(path string, pattern string, req ToolRequest) {
	if pattern != "" {
		r.addLayer(DiscoveryLayerHistorySymbol, ToolRequest{Tool: ToolNameHistorySymbol, Path: path, Pattern: pattern})
	}

	if req.StartLine > 0 && req.EndLine >= req.StartLine {
		r.addLayer(DiscoveryLayerHistoryRange, ToolRequest{Tool: ToolNameHistoryRange, Path: path, StartLine: req.StartLine, EndLine: req.EndLine})
		r.addLayer(DiscoveryLayerBlameRange, ToolRequest{Tool: ToolNameBlameRange, Path: path, StartLine: req.StartLine, EndLine: req.EndLine})

		return
	}

	if path != "." {
		r.addLayer(DiscoveryLayerBlameRange, ToolRequest{Tool: ToolNameBlameRange, Path: path, StartLine: 1, EndLine: 80})
	}
}

func (r *discoveryQueryRunner) addStructuralLayer(path string, req ToolRequest) {
	pattern := strings.TrimSpace(req.Pattern)
	if pattern == "" || !strings.Contains(pattern, "$") {
		r.packet.Gaps = append(r.packet.Gaps, ToolNameStructuralSearch+" skipped because pattern was not an ast-grep shape")
		return
	}

	r.addLayer(DiscoveryLayerStructuralSearch, ToolRequest{Tool: ToolNameStructuralSearch, Path: path, Pattern: pattern, Language: r.packet.Language})
}

func (r *discoveryQueryRunner) addCtagsFallback(path string, pattern string) {
	if strings.TrimSpace(pattern) == "" {
		return
	}

	r.addLayer(DiscoveryLayerCtagsSymbols, ToolRequest{Tool: ToolNameCtagsSymbols, Path: path, Pattern: pattern})
}

func (r *discoveryQueryRunner) addLayer(name string, req ToolRequest) {
	_, _ = r.addLayerResult(name, req)
}

func (r *discoveryQueryRunner) addLayerResult(name string, req ToolRequest) (ToolResult, bool) {
	if r.stopBeforeLayer(name) {
		return ToolResult{}, false
	}

	if strings.TrimSpace(req.Tool) == "" || req.Tool == ToolNameDiscoveryQuery {
		return ToolResult{}, false
	}

	keyData, _ := json.Marshal(req)
	key := name + ":" + string(keyData)

	if _, ok := r.seen[key]; ok {
		return ToolResult{}, false
	}

	r.seen[key] = struct{}{}

	result := r.manager.Execute(r.ctx, req)
	layer := discoveryQueryLayer{
		Name:          name,
		Tool:          result.Tool,
		Request:       requestMap(req),
		Summary:       result.Summary,
		Error:         result.Error,
		OutputExcerpt: compactDiscoveryQueryOutput(result.Output),
		OutputLines:   countNonEmptyOutputLines(result.Output),
		OutputChars:   len(result.Output),
		PrecisionTier: result.PrecisionTier,
	}

	r.packet.Layers = append(r.packet.Layers, layer)
	r.checkpointArtifact()
	r.stopAfterLayer(name)

	return result, true
}

func (r *discoveryQueryRunner) stopBeforeLayer(name string) bool {
	if r.stopped {
		r.addSkippedLayer(name)
		return true
	}

	if err := r.ctx.Err(); err != nil {
		r.markStopped(err, "before "+name)
		r.addSkippedLayer(name)
		return true
	}

	return false
}

func (r *discoveryQueryRunner) stopAfterLayer(name string) bool {
	if err := r.ctx.Err(); err != nil {
		r.markStopped(err, "after "+name)
		return true
	}

	return false
}

func (r *discoveryQueryRunner) markStopped(err error, location string) {
	if r.stopped {
		return
	}

	r.stopped = true
	if errors.Is(err, context.DeadlineExceeded) {
		r.packet.Status = "partial_interrupted"
		r.packet.TimeoutOwner = "parent_context"
		r.packet.Gaps = append(r.packet.Gaps, "discovery_query parent context deadline reached "+location+"; partial artifact records completed layers and skipped layers")
		return
	}

	r.packet.Status = "partial_interrupted"
	r.packet.TimeoutOwner = "parent_context"
	r.packet.Gaps = append(r.packet.Gaps, "discovery_query was canceled "+location+"; partial artifact records completed layers and skipped layers")
}

func (r *discoveryQueryRunner) addSkippedLayer(name string) {
	name = strings.TrimSpace(name)
	if name == "" || slices.Contains(r.packet.SkippedLayers, name) {
		return
	}

	r.packet.SkippedLayers = append(r.packet.SkippedLayers, name)
}

func (r *discoveryQueryRunner) checkpointArtifact() {
	if err := r.writeArtifact(); err != nil {
		r.packet.Gaps = append(r.packet.Gaps, "discovery_query checkpoint write failed: "+err.Error())
	}
}

func (r *discoveryQueryRunner) writeArtifact() error {
	path := filepath.Join(r.manager.cfg.Root, filepath.FromSlash(r.packet.ArtifactPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir query artifact dir: %w", err)
	}

	_ = ensureDiscoveryArtifactsIgnored(r.manager.cfg.Root)

	data, err := json.MarshalIndent(r.packet, "", "  ")
	if err != nil {
		return fmt.Errorf("encode query packet: %w", err)
	}

	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write query artifact: %w", err)
	}

	return nil
}

func (r *discoveryQueryRunner) summaryJSON() string {
	summary := discoveryQuerySummary{
		ID:                   r.packet.ID,
		Purpose:              r.packet.Purpose,
		ArtifactPath:         r.packet.ArtifactPath,
		ContextIndexID:       r.packet.ContextIndexID,
		ContextViews:         slices.Clone(r.packet.ContextViews),
		ContextFreshness:     r.packet.ContextFreshness,
		ContextStaleEvidence: r.packet.ContextStaleEvidence,
		ContextConfidence:    r.packet.ContextConfidence,
		ContextProvenance:    r.packet.ContextProvenance,
		Gaps:                 slices.Clone(r.packet.Gaps),
		Status:               r.packet.Status,
		SkippedLayers:        slices.Clone(r.packet.SkippedLayers),
		TimeoutOwner:         r.packet.TimeoutOwner,
	}
	for _, layer := range r.packet.Layers {
		summary.LayersRun = append(summary.LayersRun, layer.Name+":"+layer.Tool)
		summary.Evidence = append(summary.Evidence, struct {
			Name          string `json:"name"`
			Tool          string `json:"tool"`
			Summary       string `json:"summary"`
			Error         string `json:"error,omitempty"`
			OutputExcerpt string `json:"output_excerpt,omitempty"`
		}{
			Name:          layer.Name,
			Tool:          layer.Tool,
			Summary:       layer.Summary,
			Error:         layer.Error,
			OutputExcerpt: compactDiscoveryQuerySummaryLayerOutput(layer),
		})

		if strings.TrimSpace(layer.Error) != "" {
			summary.Errors = append(summary.Errors, layer.Name+":"+layer.Tool+": "+layer.Error)
		}
	}

	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return ""
	}

	return string(data)
}

func NormalizeDiscoveryQueryPurpose(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")

	switch value {
	case "impact", DiscoveryQueryPurposeImpactAnalysis:
		return DiscoveryQueryPurposeImpactAnalysis
	case "trace", "function_trace", DiscoveryQueryPurposeFunctionTracing:
		return DiscoveryQueryPurposeFunctionTracing
	case "architecture", DiscoveryQueryPurposeArchitectureBoundary, "boundary", "tool_usefulness", "tooling_usefulness", "discovery_tooling", "discovery_tools", "usefulness":
		return DiscoveryQueryPurposeArchitectureBoundary
	case "security", DiscoveryQueryPurposeSecurityReview:
		return DiscoveryQueryPurposeSecurityReview
	case "history", DiscoveryQueryPurposeHistoryContext:
		return DiscoveryQueryPurposeHistoryContext
	default:
		if strings.Contains(value, "security") || strings.Contains(value, "vulnerability") {
			return DiscoveryQueryPurposeSecurityReview
		}

		if strings.Contains(value, "history") || strings.Contains(value, "evolution") {
			return DiscoveryQueryPurposeHistoryContext
		}

		if strings.Contains(value, "impact") || strings.Contains(value, "blast_radius") {
			return DiscoveryQueryPurposeImpactAnalysis
		}

		if strings.Contains(value, "trace") || strings.Contains(value, "tracing") || strings.Contains(value, "call_graph") {
			return DiscoveryQueryPurposeFunctionTracing
		}

		if strings.Contains(value, "architecture") ||
			strings.Contains(value, "boundary") ||
			strings.Contains(value, "usefulness") ||
			(strings.Contains(value, "discovery") && (strings.Contains(value, "implementation") || strings.Contains(value, "definition") || strings.Contains(value, "orchestration") || strings.Contains(value, "pattern"))) {

			return DiscoveryQueryPurposeArchitectureBoundary
		}

		return ""
	}
}

func normalizeDiscoveryQueryLanguage(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, ".")

	switch value {
	case "go", "golang":
		return "go"
	case "ts", "tsx", "mts", "cts", "d.ts", "d.mts", "d.cts", "typescript", "js", "jsx", "mjs", "cjs", "javascript":
		return "typescript"
	case "py", "python":
		return "python"
	case "cpp", "c++", "cc", "cxx", "c":
		return "cpp"
	case "rs", "rust":
		return "rust"
	case "java":
		return "java"
	default:
		return ""
	}
}

func globForDiscoveryLanguage(language string) string {
	lang, ok := semanticlanguage.LanguageForName(normalizeDiscoveryQueryLanguage(language))
	if !ok {
		return ""
	}

	return globForLanguageExtensions(lang.Extensions)
}

func globForLanguageExtensions(extensions []string) string {
	seen := make(map[string]struct{}, len(extensions))
	suffixes := make([]string, 0, len(extensions))

	for _, ext := range extensions {
		ext = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ext)), ".")
		if ext == "" {
			continue
		}

		if _, ok := seen[ext]; ok {
			continue
		}

		seen[ext] = struct{}{}
		suffixes = append(suffixes, ext)
	}

	if len(suffixes) == 0 {
		return ""
	}

	if len(suffixes) == 1 {
		return "*." + suffixes[0]
	}

	return "*.{" + strings.Join(suffixes, ",") + "}"
}

func discoveryQueryID(root string, purpose string, req ToolRequest, target string, language string, depth int) string {
	var head, status string
	if snapshot, err := gitstate.Current(context.Background(), root); err == nil {
		head = snapshot.Head
		status = gitstate.FilterPorcelainStatusByPathPrefix(snapshot.Status, ".idiom/")
	}
	payload, _ := json.Marshal(map[string]any{
		"purpose":  purpose,
		"path":     target,
		"pattern":  strings.TrimSpace(req.Pattern),
		"query":    strings.TrimSpace(req.Query),
		"language": language,
		"depth":    depth,
		"head":     strings.TrimSpace(head),
		"status":   strings.TrimSpace(status),
	})
	sum := sha256.Sum256(payload)

	return fmt.Sprintf("%x", sum[:8])
}

func ensureDiscoveryArtifactsIgnored(root string) error {
	excludePath, err := gitInfoExcludePath(root)
	if err != nil || strings.TrimSpace(excludePath) == "" {
		return err
	}

	data, err := os.ReadFile(excludePath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	for _, line := range strings.Split(string(data), "\n") {
		switch strings.TrimSpace(line) {
		case ".idiom/", "/.idiom/":
			return nil
		}
	}

	if err := os.MkdirAll(filepath.Dir(excludePath), 0o755); err != nil {
		return err
	}

	addition := "# Idiom discovery artifacts\n.idiom/\n"

	if len(data) > 0 {
		prefix := "\n"
		if strings.HasSuffix(string(data), "\n") {
			prefix = ""
		}

		addition = prefix + addition
	}

	file, err := os.OpenFile(excludePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.WriteString(addition)

	return err
}

func gitInfoExcludePath(root string) (string, error) {
	return gitstate.GitPath(context.Background(), root, "info/exclude")
}

func requestMap(req ToolRequest) map[string]any {
	data, err := json.Marshal(req)
	if err != nil {
		return nil
	}

	var out map[string]any

	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}

	for key, value := range out {
		if emptyJSONValue(value) {
			delete(out, key)
		}
	}

	return out
}

func emptyJSONValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return typed == ""
	case float64:
		return typed == 0
	case bool:
		return !typed
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	default:
		return false
	}
}

func compactDiscoveryQuerySummaryLayerOutput(layer discoveryQueryLayer) string {
	if layer.Name == DiscoveryLayerProjectBehaviorDigest || layer.Name == DiscoveryLayerProjectContextDigest {
		return ""
	}

	return compactDiscoveryQueryEvidenceOutput(layer.OutputExcerpt)
}

func compactDiscoveryQueryOutput(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	lines := strings.Split(text, "\n")
	if len(lines) > discoveryQueryOutputLines {
		lines = append(lines[:discoveryQueryOutputLines], "...")
	}

	out := strings.Join(lines, "\n")
	if len(out) > discoveryQueryOutputChars {
		out = out[:discoveryQueryOutputChars] + "\n..."
	}

	return out
}

func compactDiscoveryQueryEvidenceOutput(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	lines := strings.Split(text, "\n")
	if len(lines) > 20 {
		lines = append(lines[:20], "...")
	}

	out := strings.Join(lines, "\n")
	if len(out) > 2500 {
		out = out[:2500] + "\n..."
	}

	return out
}

func countNonEmptyOutputLines(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}

	return strings.Count(text, "\n") + 1
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
