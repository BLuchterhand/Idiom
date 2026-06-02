package comprehension

import (
	"fmt"
	"idiom/internal/util"
	"strings"
)

type GraphResultConfidence struct {
	Level  string `json:"level"`
	Source string `json:"source"`
	Reason string `json:"reason,omitempty"`
}

type GraphResultProvenance struct {
	Source                  string `json:"source"`
	Operation               string `json:"operation,omitempty"`
	StoreKind               string `json:"store_kind,omitempty"`
	IndexID                 string `json:"index_id,omitempty"`
	GeneratedAt             string `json:"generated_at,omitempty"`
	CollectorVersion        string `json:"collector_version,omitempty"`
	CurrentCollectorVersion string `json:"current_collector_version,omitempty"`
	ArtifactPath            string `json:"artifact_path,omitempty"`
}

const GraphViewResultContractVersion = "graph_view_result.v1"

type GraphViewResultContract struct {
	SchemaVersion  string                `json:"schema_version"`
	Operation      string                `json:"operation,omitempty"`
	Confidence     GraphResultConfidence `json:"confidence"`
	Provenance     GraphResultProvenance `json:"provenance"`
	Freshness      *FreshnessDecision    `json:"freshness,omitempty"`
	StaleEvidence  StaleEvidenceSafety   `json:"stale_evidence"`
	Cache          GraphViewCacheState   `json:"cache"`
	UnresolvedGaps []GraphUnresolvedGap  `json:"unresolved_gaps,omitempty"`
}

type GraphViewCacheState struct {
	State                     string                         `json:"state"`
	Source                    string                         `json:"source"`
	Reason                    string                         `json:"reason,omitempty"`
	CacheKey                  string                         `json:"cache_key,omitempty"`
	PackID                    string                         `json:"pack_id,omitempty"`
	StoreKind                 string                         `json:"store_kind,omitempty"`
	WorkspaceStateFingerprint string                         `json:"workspace_state_fingerprint,omitempty"`
	RankerWeightsFingerprint  string                         `json:"ranker_weights_fingerprint,omitempty"`
	RuntimeTracesFingerprint  string                         `json:"runtime_traces_fingerprint,omitempty"`
	FreshnessPolicy           FreshnessPolicy                `json:"freshness_policy,omitempty"`
	ReadThroughRefresh        bool                           `json:"read_through_refresh,omitempty"`
	Fallback                  bool                           `json:"fallback,omitempty"`
	Invalidation              GraphViewInvalidationSemantics `json:"invalidation"`
}

type GraphViewInvalidationSemantics struct {
	Policy          string                        `json:"policy"`
	State           string                        `json:"state"`
	KeyParts        []string                      `json:"key_parts,omitempty"`
	ValidatesOnLoad []string                      `json:"validates_on_load,omitempty"`
	StaleResult     string                        `json:"stale_result,omitempty"`
	Sources         []GraphViewInvalidationSource `json:"sources,omitempty"`
	Experimental    bool                          `json:"experimental,omitempty"`
}

type GraphViewInvalidationSource struct {
	Source      string `json:"source"`
	State       string `json:"state"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Path        string `json:"path,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type GraphUnresolvedGap struct {
	Reason             string   `json:"reason"`
	State              string   `json:"state"`
	Scope              string   `json:"scope,omitempty"`
	IndexID            string   `json:"index_id,omitempty"`
	TraceID            string   `json:"trace_id,omitempty"`
	CandidateFileCount int      `json:"candidate_file_count,omitempty"`
	StalePathCount     int      `json:"stale_path_count,omitempty"`
	MissingPathCount   int      `json:"missing_path_count,omitempty"`
	StalePaths         []string `json:"stale_paths,omitempty"`
	MissingPaths       []string `json:"missing_paths,omitempty"`
}

type RankerReason struct {
	Reason     string  `json:"reason"`
	State      string  `json:"state,omitempty"`
	ScoreDelta float64 `json:"score_delta,omitempty"`
	Multiplier float64 `json:"multiplier,omitempty"`
}

func StatusReportWithMetadata(report StatusReport, operation string, freshness *FreshnessDecision) StatusReport {
	report.StaleFileCount = len(report.StaleFiles)
	report.MissingFileCount = len(report.MissingFiles)
	report.StalePathCount = report.StaleFileCount + report.MissingFileCount
	if freshness != nil {
		report.Freshness = freshness
	}
	report.Confidence = graphResultConfidenceForStatus(report, report.Freshness)
	report.Provenance = graphResultProvenanceFromStatus(operation, report, "")
	report.StaleEvidence = StaleEvidenceSafetyForStatus(report, report.Freshness)
	return report
}

func AttachPackResultContract(result *PackResult) {
	if result == nil {
		return
	}
	if result.Cache.State == "" {
		result.Cache = GraphViewCacheState{
			State:  "uncached",
			Source: "recompute",
			Reason: "cache_metadata_absent",
			Invalidation: GraphViewInvalidationSemantics{
				Policy:      "content_addressed_read_through",
				State:       "unknown",
				StaleResult: "cache_miss_recompute",
			},
		}
	}
	if result.Cache.PackID == "" {
		result.Cache.PackID = result.PackID
	}
	if result.Cache.StoreKind == "" {
		result.Cache.StoreKind = result.StoreKind
	}
	result.Cache.ReadThroughRefresh = result.Cache.ReadThroughRefresh || (result.AutoRefresh != nil && result.AutoRefresh.Attempted)
	if result.Freshness != nil {
		result.Cache.FreshnessPolicy = result.Freshness.Policy
	}
	result.UnresolvedGapDetails = normalizedGapDetails(result.UnresolvedGapDetails, result.UnresolvedGaps)
	result.Contract = GraphViewResultContract{
		SchemaVersion:  GraphViewResultContractVersion,
		Operation:      util.FirstNonEmpty(strings.TrimSpace(result.Provenance.Operation), "context_pack"),
		Confidence:     result.Confidence,
		Provenance:     result.Provenance,
		Freshness:      result.Freshness,
		StaleEvidence:  result.StaleEvidence,
		Cache:          result.Cache,
		UnresolvedGaps: append([]GraphUnresolvedGap(nil), result.UnresolvedGapDetails...),
	}
}

func GraphViewCacheStateForKey(key ViewKey, storeKind string, state string, reason string) GraphViewCacheState {
	state = strings.TrimSpace(state)
	if state == "" {
		state = "miss_recompute"
	}
	source := "recompute"
	switch state {
	case "hit":
		source = "graph_views"
	case "fallback_recompute":
		source = "fallback"
	}
	cache := GraphViewCacheState{
		State:                     state,
		Source:                    source,
		Reason:                    strings.TrimSpace(reason),
		CacheKey:                  key.ID(),
		PackID:                    key.ID(),
		StoreKind:                 strings.TrimSpace(storeKind),
		WorkspaceStateFingerprint: strings.TrimSpace(key.Query.WorkspaceStateFingerprint),
		RankerWeightsFingerprint:  strings.TrimSpace(key.Query.RankerWeightsFingerprint),
		RuntimeTracesFingerprint:  strings.TrimSpace(key.Query.RuntimeTracesFingerprint),
		Invalidation:              graphViewInvalidationSemantics(key, state),
	}
	if state == "fallback_recompute" {
		cache.Fallback = true
	}
	return cache
}

func graphViewInvalidationSemantics(key ViewKey, cacheState string) GraphViewInvalidationSemantics {
	state := "valid"
	switch cacheState {
	case "hit":
		state = "valid"
	case "fallback_recompute":
		state = "fallback"
	case "stale_recompute":
		state = "stale"
	default:
		state = "recomputed"
	}
	workspaceFingerprint := strings.TrimSpace(key.Query.WorkspaceStateFingerprint)
	rankerFingerprint := strings.TrimSpace(key.Query.RankerWeightsFingerprint)
	runtimeFingerprint := strings.TrimSpace(key.Query.RuntimeTracesFingerprint)
	componentsAttributed := workspaceFingerprint == "" || rankerFingerprint != "" || runtimeFingerprint != ""
	return GraphViewInvalidationSemantics{
		Policy: "content_addressed_read_through",
		State:  state,
		KeyParts: []string{
			"index_id",
			"task",
			"mode",
			"path",
			"scope",
			"limit",
			"budget_tokens",
			"required_tests",
			"required_edit_paths",
			"forbidden_paths",
			"intent",
			"include_stale_runtime",
			"algorithm_version",
			"workspace_state_fingerprint",
			"ranker_weights_fingerprint",
			"runtime_traces_fingerprint",
		},
		ValidatesOnLoad: []string{
			"current_index_id",
			"referenced_source_fingerprints",
			"scoped_freshness",
		},
		StaleResult: "cache_miss_recompute",
		Sources: []GraphViewInvalidationSource{
			{Source: "index_id", State: "keyed", Fingerprint: strings.TrimSpace(key.IndexID), Reason: "graph_content"},
			{Source: "ranker_weights", State: componentWorkspaceStateSourceState(rankerFingerprint, workspaceFingerprint, componentsAttributed), Fingerprint: rankerFingerprint, Path: ".idiom/ranker/weights.json", Reason: componentWorkspaceStateReason(rankerFingerprint, workspaceFingerprint, componentsAttributed, "ranker_weights_fingerprint")},
			{Source: "runtime_traces", State: componentWorkspaceStateSourceState(runtimeFingerprint, workspaceFingerprint, componentsAttributed), Fingerprint: runtimeFingerprint, Path: ".idiom/comprehension/traces", Reason: componentWorkspaceStateReason(runtimeFingerprint, workspaceFingerprint, componentsAttributed, "runtime_traces_fingerprint")},
			{Source: "referenced_source_fingerprints", State: "validated_on_load", Reason: "cached_output_paths"},
		},
	}
}

func componentWorkspaceStateSourceState(componentFingerprint string, workspaceFingerprint string, componentsAttributed bool) string {
	if strings.TrimSpace(componentFingerprint) != "" {
		return "keyed"
	}
	if componentsAttributed {
		return "baseline_keyed"
	}
	if strings.TrimSpace(workspaceFingerprint) != "" {
		return "legacy_composite"
	}
	return "baseline_keyed"
}

func componentWorkspaceStateReason(componentFingerprint string, workspaceFingerprint string, componentsAttributed bool, reason string) string {
	if strings.TrimSpace(componentFingerprint) != "" {
		return reason
	}
	if componentsAttributed {
		return reason
	}
	if strings.TrimSpace(workspaceFingerprint) != "" {
		return "workspace_state_fingerprint_legacy"
	}
	if strings.TrimSpace(reason) == "" {
		return "baseline_keyed"
	}
	return reason
}

func graphResultProvenanceFromStatus(operation string, report StatusReport, artifactPath string) GraphResultProvenance {
	source := "context_graph"
	if !report.Indexed {
		source = "context_status"
	}
	return GraphResultProvenance{
		Source:                  source,
		Operation:               strings.TrimSpace(operation),
		StoreKind:               report.StoreKind,
		IndexID:                 report.IndexID,
		GeneratedAt:             report.GeneratedAt,
		CollectorVersion:        report.CollectorVersion,
		CurrentCollectorVersion: report.CurrentCollectorVersion,
		ArtifactPath:            artifactPath,
	}
}

func graphResultProvenanceFromGraph(operation string, graph Graph, artifactPath string) GraphResultProvenance {
	return GraphResultProvenance{
		Source:                  "context_graph",
		Operation:               strings.TrimSpace(operation),
		StoreKind:               graph.StoreKind,
		IndexID:                 graph.ID,
		GeneratedAt:             graph.GeneratedAt,
		CollectorVersion:        graph.CollectorVersion,
		CurrentCollectorVersion: currentCollectorVersion(),
		ArtifactPath:            artifactPath,
	}
}

func graphResultProvenanceFromFreshness(operation string, freshness FreshnessDecision, artifactPath string) GraphResultProvenance {
	return GraphResultProvenance{
		Source:       "context_graph",
		Operation:    strings.TrimSpace(operation),
		StoreKind:    freshness.StoreKind,
		IndexID:      freshness.IndexID,
		GeneratedAt:  freshness.GeneratedAt,
		ArtifactPath: artifactPath,
	}
}

func graphResultConfidenceForStatus(report StatusReport, freshness *FreshnessDecision) GraphResultConfidence {
	if !report.Indexed {
		return GraphResultConfidence{Level: "none", Source: "status_report", Reason: "missing_index"}
	}
	if report.CollectorVersionMismatch {
		return GraphResultConfidence{Level: "low", Source: "status_report", Reason: "collector_version_mismatch"}
	}
	return GraphResultConfidenceForFreshness(report.Fresh, freshness)
}

func GraphResultConfidenceForFreshness(globalFresh bool, freshness *FreshnessDecision) GraphResultConfidence {
	if freshness == nil {
		if globalFresh {
			return GraphResultConfidence{Level: "high", Source: "status_report"}
		}
		return GraphResultConfidence{Level: "low", Source: "status_report", Reason: FreshnessReasonStaleGraph}
	}
	source := "freshness_decision"
	if !freshness.FreshForScope {
		reason := util.FirstNonEmpty(strings.TrimSpace(freshness.Reason), strings.TrimSpace(freshness.Blocker), "stale_scope")
		return GraphResultConfidence{Level: "low", Source: source, Reason: reason}
	}
	if !freshness.GlobalFresh {
		return GraphResultConfidence{Level: "medium", Source: source, Reason: "scope_fresh_global_stale"}
	}
	return GraphResultConfidence{Level: "high", Source: source}
}

func GraphGapForStatus(report StatusReport) string {
	if !report.Indexed {
		return "reason=missing_index"
	}
	if report.CollectorVersionMismatch {
		return fmt.Sprintf("reason=%s index_id=%s collector_version=%s current_collector_version=%s", FreshnessReasonCollectorVersionMismatch, strings.TrimSpace(report.IndexID), strings.TrimSpace(report.CollectorVersion), strings.TrimSpace(report.CurrentCollectorVersion))
	}
	if report.Fresh {
		return ""
	}
	return fmt.Sprintf("reason=%s index_id=%s stale_path_count=%d missing_path_count=%d stale_paths=%s missing_paths=%s", FreshnessReasonStaleGraph, strings.TrimSpace(report.IndexID), len(report.StaleFiles), len(report.MissingFiles), limitedCSV(report.StaleFiles, 5), limitedCSV(report.MissingFiles, 5))
}

func GraphGapDetailForStatus(report StatusReport) (GraphUnresolvedGap, bool) {
	if !report.Indexed {
		return GraphUnresolvedGap{Reason: FreshnessReasonMissingIndex, State: "unavailable"}, true
	}
	if report.CollectorVersionMismatch {
		return GraphUnresolvedGap{
			Reason:  FreshnessReasonCollectorVersionMismatch,
			State:   "stale",
			IndexID: strings.TrimSpace(report.IndexID),
		}, true
	}
	if report.Fresh {
		return GraphUnresolvedGap{}, false
	}
	return GraphUnresolvedGap{
		Reason:           FreshnessReasonStaleGraph,
		State:            "stale",
		IndexID:          strings.TrimSpace(report.IndexID),
		StalePathCount:   len(report.StaleFiles),
		MissingPathCount: len(report.MissingFiles),
		StalePaths:       limitedStrings(report.StaleFiles, 5),
		MissingPaths:     limitedStrings(report.MissingFiles, 5),
	}, true
}

func FreshnessGapForDecision(decision FreshnessDecision) string {
	reason := util.FirstNonEmpty(strings.TrimSpace(decision.Reason), strings.TrimSpace(decision.Blocker), "freshness_blocker")
	parts := []string{
		"reason=" + reason,
		fmt.Sprintf("fresh_for_scope=%t", decision.FreshForScope),
		fmt.Sprintf("global_fresh=%t", decision.GlobalFresh),
		fmt.Sprintf("stale_path_count=%d", decision.StalePathCount),
		fmt.Sprintf("missing_path_count=%d", decision.MissingPathCount),
	}
	if decision.Scope != "" {
		parts = append(parts, "scope="+decision.Scope)
	}
	if decision.IndexID != "" {
		parts = append(parts, "index_id="+decision.IndexID)
	}
	if decision.Blocker != "" && decision.Blocker != reason {
		parts = append(parts, "blocker="+decision.Blocker)
	}
	if decision.BudgetExceeded {
		parts = append(parts, "budget_exceeded=true")
	}
	if len(decision.StalePaths) > 0 {
		parts = append(parts, "stale_paths="+limitedCSV(decision.StalePaths, 5))
	}
	if len(decision.MissingPaths) > 0 {
		parts = append(parts, "missing_paths="+limitedCSV(decision.MissingPaths, 5))
	}
	return strings.Join(parts, " ")
}

func applyFreshnessStatusMetadata(decision *FreshnessDecision, status StatusReport) {
	if decision == nil {
		return
	}
	decision.IndexID = status.IndexID
	decision.StoreKind = status.StoreKind
	decision.GeneratedAt = status.GeneratedAt
	decision.CheckedPathCount = len(decision.CheckedPaths)
	decision.StalePathCount = len(decision.StalePaths)
	decision.MissingPathCount = len(decision.MissingPaths)
}

func normalizedGapDetails(details []GraphUnresolvedGap, legacy []string) []GraphUnresolvedGap {
	out := make([]GraphUnresolvedGap, 0, len(details)+len(legacy))
	seen := make(map[string]struct{}, len(details)+len(legacy))
	add := func(gap GraphUnresolvedGap) {
		gap.Reason = strings.TrimSpace(gap.Reason)
		gap.State = strings.TrimSpace(gap.State)
		if gap.Reason == "" {
			return
		}
		if gap.State == "" {
			gap.State = "unknown"
		}
		key := gap.Reason + "\x00" + gap.State + "\x00" + gap.IndexID + "\x00" + gap.TraceID + "\x00" + gap.Scope
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, gap)
	}
	for _, gap := range details {
		add(gap)
	}
	for _, legacyGap := range legacy {
		add(GraphGapDetailFromString(legacyGap))
	}
	return out
}

func GraphGapDetailFromString(gap string) GraphUnresolvedGap {
	fields := parseGapFields(gap)
	reason := util.FirstNonEmpty(fields["reason"], neutralGapReason(gap))
	detail := GraphUnresolvedGap{
		Reason:             reason,
		State:              gapStateForReason(reason),
		Scope:              fields["scope"],
		IndexID:            fields["index_id"],
		TraceID:            fields["trace_id"],
		CandidateFileCount: atoiGapField(fields["candidate_file_count"]),
		StalePathCount:     atoiGapField(fields["stale_path_count"]),
		MissingPathCount:   atoiGapField(fields["missing_path_count"]),
		StalePaths:         limitedStrings(splitGapList(fields["stale_paths"]), 5),
		MissingPaths:       limitedStrings(splitGapList(fields["missing_paths"]), 5),
	}
	return detail
}

func parseGapFields(gap string) map[string]string {
	out := make(map[string]string)
	for _, field := range strings.Fields(strings.TrimSpace(gap)) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key != "" && value != "" {
			out[key] = value
		}
	}
	return out
}

func gapStateForReason(reason string) string {
	switch reason {
	case FreshnessReasonMissingIndex:
		return "unavailable"
	case FreshnessReasonStaleGraph, FreshnessReasonStaleScopeFiles, FreshnessReasonStaleUnboundedGraph, FreshnessReasonCollectorVersionMismatch, "stale_runtime_trace":
		return "stale"
	case "no_graph_matches":
		return "empty"
	case FreshnessReasonBudgetExceeded, FreshnessReasonRefreshFailed, FreshnessReasonRefreshEstimateFailed:
		return "blocked"
	default:
		return "unknown"
	}
}

func neutralGapReason(gap string) string {
	gap = strings.ToLower(strings.TrimSpace(gap))
	if gap == "" {
		return ""
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range gap {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore && b.Len() > 0 {
			b.WriteByte('_')
			lastUnderscore = true
		}
		if b.Len() >= 48 {
			break
		}
	}
	return strings.Trim(b.String(), "_")
}

func atoiGapField(value string) int {
	var n int
	_, _ = fmt.Sscanf(strings.TrimSpace(value), "%d", &n)
	return n
}

func splitGapList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.Split(value, ",")
}

func limitedStrings(values []string, limit int) []string {
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		cleaned = append(cleaned, value)
		if limit > 0 && len(cleaned) >= limit {
			break
		}
	}
	return cleaned
}

func limitedCSV(values []string, limit int) string {
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		cleaned = append(cleaned, value)
		if limit > 0 && len(cleaned) >= limit {
			break
		}
	}
	if len(cleaned) == 0 {
		return ""
	}
	return strings.Join(cleaned, ",")
}
