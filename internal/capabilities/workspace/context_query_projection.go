package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"idiom/internal/repository/comprehension"
)

const (
	contextQueryProjectionSchemaVersion = "context_query_projection.v2"
	maxContextQueryProjectionBytes      = 64 * 1024
	contextQueryClaimLimit              = 12
	contextQueryNodeLimit               = 12
	contextQueryEdgeLimit               = 24
	contextQueryGapLimit                = 8
	contextQueryMetadataEntryLimit      = 6
	contextQueryMetadataSampleLimit     = 4

	compactQueryInventorySampleLimit        = 8
	compactQueryFollowUpRefreshLimit        = 4
	compactQueryCoreTextEncodedLimit        = 384
	compactQueryMetadataKeyEncodedLimit     = 128
	compactQueryMetadataPreviewEncodedLimit = 256
	compactQueryMetadataDereferenceLimit    = 16
)

// contextQueryProjection is the canonical model-facing projection of a
// comprehension.QueryResult. comprehension.QueryResult remains the internal evidence record; this type
// preserves semantic query evidence while enforcing explicit item and byte
// bounds.
type contextQueryProjection struct {
	SchemaVersion         string                              `json:"schema_version"`
	IndexID               string                              `json:"index_id,omitempty"`
	StoreKind             string                              `json:"store_kind,omitempty"`
	Fresh                 bool                                `json:"fresh"`
	Resolution            string                              `json:"resolution,omitempty"`
	GeneratedAt           string                              `json:"generated_at,omitempty"`
	Confidence            comprehension.GraphResultConfidence `json:"confidence"`
	Provenance            comprehension.GraphResultProvenance `json:"provenance"`
	AutoRefresh           *contextQueryRefresh                `json:"auto_refresh,omitempty"`
	Freshness             *contextQueryFreshness              `json:"freshness,omitempty"`
	StaleEvidence         comprehension.StaleEvidenceSafety   `json:"stale_evidence"`
	ClaimCount            int                                 `json:"claim_count"`
	Claims                []string                            `json:"claims,omitempty"`
	ClaimsOmitted         int                                 `json:"claims_omitted,omitempty"`
	NodeCount             int                                 `json:"node_count"`
	Nodes                 []contextQueryNode                  `json:"nodes,omitempty"`
	NodesOmitted          int                                 `json:"nodes_omitted,omitempty"`
	EdgeCount             int                                 `json:"edge_count"`
	Edges                 []contextQueryEdge                  `json:"edges,omitempty"`
	EdgesOmitted          int                                 `json:"edges_omitted,omitempty"`
	UnresolvedGapCount    int                                 `json:"unresolved_gap_count"`
	UnresolvedGaps        []string                            `json:"unresolved_gaps,omitempty"`
	UnresolvedGapsOmitted int                                 `json:"unresolved_gaps_omitted,omitempty"`
}

// contextQueryNode preserves the stable graph handle and source location. Its
// properties retain small scalar values and summarize collections or nested
// structures with explicit counts.
type contextQueryNode struct {
	ID                string         `json:"id"`
	Kind              string         `json:"kind"`
	Path              string         `json:"path,omitempty"`
	Language          string         `json:"language,omitempty"`
	QualifiedName     string         `json:"qualified_name,omitempty"`
	StartLine         int            `json:"start_line,omitempty"`
	EndLine           int            `json:"end_line,omitempty"`
	Confidence        string         `json:"confidence"`
	Stale             bool           `json:"stale,omitempty"`
	PropertyCount     int            `json:"property_count"`
	Properties        map[string]any `json:"properties,omitempty"`
	PropertiesOmitted int            `json:"properties_omitted,omitempty"`
}

// contextQueryEdge preserves relationship handles and endpoints. Evidence is
// compacted with the same bounded metadata contract as node properties.
type contextQueryEdge struct {
	ID              string         `json:"id"`
	Kind            string         `json:"kind"`
	SourceID        string         `json:"source_id"`
	TargetID        string         `json:"target_id"`
	Confidence      string         `json:"confidence"`
	EvidenceCount   int            `json:"evidence_count"`
	Evidence        map[string]any `json:"evidence,omitempty"`
	EvidenceOmitted int            `json:"evidence_omitted,omitempty"`
}

// contextQueryMetadataSummary replaces a value whose raw representation can
// grow without bound. It records useful type, size, sample, and omission data
// without recursively copying the original value.
type contextQueryMetadataSummary struct {
	Type         string   `json:"type"`
	Preview      string   `json:"preview,omitempty"`
	ByteCount    int      `json:"byte_count,omitempty"`
	OmittedBytes int      `json:"omitted_bytes,omitempty"`
	ItemCount    int      `json:"item_count,omitempty"`
	Samples      []string `json:"samples,omitempty"`
	ItemsOmitted int      `json:"items_omitted,omitempty"`
}

// contextQueryRefresh preserves refresh outcomes and counts while replacing
// potentially large path and follow-up inventories with bounded samples.
type contextQueryRefresh struct {
	Attempted                bool                  `json:"attempted"`
	Scope                    string                `json:"scope,omitempty"`
	Language                 string                `json:"language,omitempty"`
	Reason                   string                `json:"reason,omitempty"`
	SkippedReason            string                `json:"skipped_reason,omitempty"`
	Error                    string                `json:"error,omitempty"`
	IndexID                  string                `json:"index_id,omitempty"`
	FilesIndexed             int                   `json:"files_indexed,omitempty"`
	SourceFileCount          int                   `json:"source_file_count,omitempty"`
	EstimatedMinimum         bool                  `json:"estimated_minimum,omitempty"`
	StalePathCount           int                   `json:"stale_path_count,omitempty"`
	MaxFiles                 int                   `json:"max_files,omitempty"`
	SymbolsIndexed           int                   `json:"symbols_indexed,omitempty"`
	EdgesIndexed             int                   `json:"edges_indexed,omitempty"`
	PartialRefreshReason     string                `json:"partial_refresh_reason,omitempty"`
	FullRebuildReason        string                `json:"full_rebuild_reason,omitempty"`
	FreshAfter               bool                  `json:"fresh_after,omitempty"`
	RefreshedFileCount       int                   `json:"refreshed_file_count"`
	RefreshedFiles           []string              `json:"refreshed_files,omitempty"`
	RefreshedFilesOmitted    int                   `json:"refreshed_files_omitted,omitempty"`
	RefreshScopeCount        int                   `json:"refresh_scope_count"`
	RefreshScopes            []string              `json:"refresh_scopes,omitempty"`
	RefreshScopesOmitted     int                   `json:"refresh_scopes_omitted,omitempty"`
	FollowUpRefreshCount     int                   `json:"follow_up_refresh_count"`
	FollowUpRefreshes        []contextQueryRefresh `json:"follow_up_refreshes,omitempty"`
	FollowUpRefreshesOmitted int                   `json:"follow_up_refreshes_omitted,omitempty"`
}

// contextQueryFreshness preserves freshness authority and aggregate counts.
// Path-shaped diagnostics remain available as deterministic samples.
type contextQueryFreshness struct {
	Policy                       comprehension.FreshnessPolicy `json:"policy"`
	Operation                    string                        `json:"operation,omitempty"`
	IndexID                      string                        `json:"index_id,omitempty"`
	StoreKind                    string                        `json:"store_kind,omitempty"`
	GeneratedAt                  string                        `json:"generated_at,omitempty"`
	FreshForScope                bool                          `json:"fresh_for_scope"`
	GlobalFresh                  bool                          `json:"global_fresh"`
	Scope                        string                        `json:"scope,omitempty"`
	Reason                       string                        `json:"reason,omitempty"`
	Blocker                      string                        `json:"blocker,omitempty"`
	BudgetExceeded               bool                          `json:"budget_exceeded,omitempty"`
	Refresh                      *contextQueryRefresh          `json:"refresh,omitempty"`
	CheckedPathCount             int                           `json:"checked_path_count"`
	CheckedPaths                 []string                      `json:"checked_paths,omitempty"`
	CheckedPathsOmitted          int                           `json:"checked_paths_omitted,omitempty"`
	StalePathCount               int                           `json:"stale_path_count"`
	StalePaths                   []string                      `json:"stale_paths,omitempty"`
	StalePathsOmitted            int                           `json:"stale_paths_omitted,omitempty"`
	MissingPathCount             int                           `json:"missing_path_count"`
	MissingPaths                 []string                      `json:"missing_paths,omitempty"`
	MissingPathsOmitted          int                           `json:"missing_paths_omitted,omitempty"`
	ScopeWarning                 string                        `json:"scope_warning,omitempty"`
	ScopeWarningOutOfCount       int                           `json:"scope_warning_out_of_count"`
	ScopeWarningOutOf            []string                      `json:"scope_warning_out_of,omitempty"`
	ScopeWarningOutOfOmitted     int                           `json:"scope_warning_out_of_omitted,omitempty"`
	StalenessWarning             string                        `json:"staleness_warning,omitempty"`
	StalenessWarningFileCount    int                           `json:"staleness_warning_file_count"`
	StalenessWarningFiles        []string                      `json:"staleness_warning_files,omitempty"`
	StalenessWarningFilesOmitted int                           `json:"staleness_warning_files_omitted,omitempty"`
}

// newContextQueryProjection derives the model-facing query payload from the
// full internal result. Counts refer to the complete comprehension.QueryResult; retained
// slices are independently capped even when a caller constructs an oversized
// result directly.
func newContextQueryProjection(result comprehension.QueryResult) contextQueryProjection {
	claims := compactQueryTextSlice(result.Claims, contextQueryClaimLimit)
	nodes := make([]contextQueryNode, 0, min(len(result.Nodes), contextQueryNodeLimit))
	for _, node := range result.Nodes[:min(len(result.Nodes), contextQueryNodeLimit)] {
		nodes = append(nodes, newContextQueryNode(node))
	}

	edges := make([]contextQueryEdge, 0, min(len(result.Edges), contextQueryEdgeLimit))
	for _, edge := range result.Edges[:min(len(result.Edges), contextQueryEdgeLimit)] {
		edges = append(edges, newContextQueryEdge(edge))
	}

	gaps := compactQueryTextSlice(result.UnresolvedGaps, contextQueryGapLimit)

	return contextQueryProjection{
		SchemaVersion:         contextQueryProjectionSchemaVersion,
		IndexID:               compactQueryText(result.IndexID, compactQueryCoreTextEncodedLimit),
		StoreKind:             compactQueryText(result.StoreKind, compactQueryCoreTextEncodedLimit),
		Fresh:                 result.Fresh,
		Resolution:            compactQueryText(result.Resolution, compactQueryCoreTextEncodedLimit),
		GeneratedAt:           compactQueryText(result.GeneratedAt, compactQueryCoreTextEncodedLimit),
		Confidence:            compactQueryConfidence(result.Confidence),
		Provenance:            compactQueryProvenance(result.Provenance),
		AutoRefresh:           newContextQueryRefresh(result.AutoRefresh, true),
		Freshness:             newContextQueryFreshness(result.Freshness),
		StaleEvidence:         compactQueryStaleEvidence(result.StaleEvidence),
		ClaimCount:            len(result.Claims),
		Claims:                claims,
		ClaimsOmitted:         len(result.Claims) - len(claims),
		NodeCount:             len(result.Nodes),
		Nodes:                 nodes,
		NodesOmitted:          len(result.Nodes) - len(nodes),
		EdgeCount:             len(result.Edges),
		Edges:                 edges,
		EdgesOmitted:          len(result.Edges) - len(edges),
		UnresolvedGapCount:    len(result.UnresolvedGaps),
		UnresolvedGaps:        gaps,
		UnresolvedGapsOmitted: len(result.UnresolvedGaps) - len(gaps),
	}
}

// renderContextQueryProjection normalizes caller-constructed projections,
// renders valid JSON, and structurally sheds retained details until the hard
// byte ceiling is satisfied. Total and omitted counts remain exact.
func renderContextQueryProjection(projection contextQueryProjection) (string, error) {
	projection = normalizecontextQueryProjection(projection)

	for {
		output, err := json.Marshal(projection)
		if err != nil {
			return "", err
		}

		if len(output) <= maxContextQueryProjectionBytes {
			return string(output), nil
		}

		if !reducecontextQueryProjection(&projection) {
			return "", fmt.Errorf("compact query projection mandatory metadata exceeds %d bytes", maxContextQueryProjectionBytes)
		}
	}
}

func newContextQueryNode(node comprehension.NodeSummary) contextQueryNode {
	properties, propertyCount, propertiesOmitted := compactQueryMetadata(node.Properties, 0)

	return contextQueryNode{
		ID:                compactQueryText(node.ID, compactQueryCoreTextEncodedLimit),
		Kind:              compactQueryText(node.Kind, compactQueryCoreTextEncodedLimit),
		Path:              compactQueryText(node.Path, compactQueryCoreTextEncodedLimit),
		Language:          compactQueryText(node.Language, compactQueryCoreTextEncodedLimit),
		QualifiedName:     compactQueryText(node.QualifiedName, compactQueryCoreTextEncodedLimit),
		StartLine:         node.StartLine,
		EndLine:           node.EndLine,
		Confidence:        compactQueryText(node.Confidence, compactQueryCoreTextEncodedLimit),
		Stale:             node.Stale,
		PropertyCount:     propertyCount,
		Properties:        properties,
		PropertiesOmitted: propertiesOmitted,
	}
}

func newContextQueryEdge(edge comprehension.EdgeSummary) contextQueryEdge {
	evidence, evidenceCount, evidenceOmitted := compactQueryMetadata(edge.Evidence, 0)

	return contextQueryEdge{
		ID:              compactQueryText(edge.ID, compactQueryCoreTextEncodedLimit),
		Kind:            compactQueryText(edge.Kind, compactQueryCoreTextEncodedLimit),
		SourceID:        compactQueryText(edge.SourceID, compactQueryCoreTextEncodedLimit),
		TargetID:        compactQueryText(edge.TargetID, compactQueryCoreTextEncodedLimit),
		Confidence:      compactQueryText(edge.Confidence, compactQueryCoreTextEncodedLimit),
		EvidenceCount:   evidenceCount,
		Evidence:        evidence,
		EvidenceOmitted: evidenceOmitted,
	}
}

func normalizecontextQueryProjection(projection contextQueryProjection) contextQueryProjection {
	projection.SchemaVersion = contextQueryProjectionSchemaVersion
	projection.IndexID = compactQueryText(projection.IndexID, compactQueryCoreTextEncodedLimit)
	projection.StoreKind = compactQueryText(projection.StoreKind, compactQueryCoreTextEncodedLimit)
	projection.Resolution = compactQueryText(projection.Resolution, compactQueryCoreTextEncodedLimit)
	projection.GeneratedAt = compactQueryText(projection.GeneratedAt, compactQueryCoreTextEncodedLimit)
	projection.Confidence = compactQueryConfidence(projection.Confidence)
	projection.Provenance = compactQueryProvenance(projection.Provenance)
	projection.StaleEvidence = compactQueryStaleEvidence(projection.StaleEvidence)
	projection.AutoRefresh = normalizecontextQueryRefresh(projection.AutoRefresh, true)
	projection.Freshness = normalizecontextQueryFreshness(projection.Freshness)

	projection.ClaimCount = max(projection.ClaimCount, len(projection.Claims)+projection.ClaimsOmitted)
	projection.Claims = compactQueryTextSlice(projection.Claims, contextQueryClaimLimit)
	projection.ClaimsOmitted = projection.ClaimCount - len(projection.Claims)

	projection.NodeCount = max(projection.NodeCount, len(projection.Nodes)+projection.NodesOmitted)
	projection.Nodes = append([]contextQueryNode(nil), projection.Nodes[:min(len(projection.Nodes), contextQueryNodeLimit)]...)
	for i := range projection.Nodes {
		properties, count, omitted := compactQueryMetadata(projection.Nodes[i].Properties, projection.Nodes[i].PropertyCount)
		projection.Nodes[i].ID = compactQueryText(projection.Nodes[i].ID, compactQueryCoreTextEncodedLimit)
		projection.Nodes[i].Kind = compactQueryText(projection.Nodes[i].Kind, compactQueryCoreTextEncodedLimit)
		projection.Nodes[i].Path = compactQueryText(projection.Nodes[i].Path, compactQueryCoreTextEncodedLimit)
		projection.Nodes[i].Language = compactQueryText(projection.Nodes[i].Language, compactQueryCoreTextEncodedLimit)
		projection.Nodes[i].QualifiedName = compactQueryText(projection.Nodes[i].QualifiedName, compactQueryCoreTextEncodedLimit)
		projection.Nodes[i].Confidence = compactQueryText(projection.Nodes[i].Confidence, compactQueryCoreTextEncodedLimit)
		projection.Nodes[i].Properties = properties
		projection.Nodes[i].PropertyCount = count
		projection.Nodes[i].PropertiesOmitted = omitted
	}
	projection.NodesOmitted = projection.NodeCount - len(projection.Nodes)

	projection.EdgeCount = max(projection.EdgeCount, len(projection.Edges)+projection.EdgesOmitted)
	projection.Edges = append([]contextQueryEdge(nil), projection.Edges[:min(len(projection.Edges), contextQueryEdgeLimit)]...)
	for i := range projection.Edges {
		evidence, count, omitted := compactQueryMetadata(projection.Edges[i].Evidence, projection.Edges[i].EvidenceCount)
		projection.Edges[i].ID = compactQueryText(projection.Edges[i].ID, compactQueryCoreTextEncodedLimit)
		projection.Edges[i].Kind = compactQueryText(projection.Edges[i].Kind, compactQueryCoreTextEncodedLimit)
		projection.Edges[i].SourceID = compactQueryText(projection.Edges[i].SourceID, compactQueryCoreTextEncodedLimit)
		projection.Edges[i].TargetID = compactQueryText(projection.Edges[i].TargetID, compactQueryCoreTextEncodedLimit)
		projection.Edges[i].Confidence = compactQueryText(projection.Edges[i].Confidence, compactQueryCoreTextEncodedLimit)
		projection.Edges[i].Evidence = evidence
		projection.Edges[i].EvidenceCount = count
		projection.Edges[i].EvidenceOmitted = omitted
	}
	projection.EdgesOmitted = projection.EdgeCount - len(projection.Edges)

	projection.UnresolvedGapCount = max(projection.UnresolvedGapCount, len(projection.UnresolvedGaps)+projection.UnresolvedGapsOmitted)
	projection.UnresolvedGaps = compactQueryTextSlice(projection.UnresolvedGaps, contextQueryGapLimit)
	projection.UnresolvedGapsOmitted = projection.UnresolvedGapCount - len(projection.UnresolvedGaps)

	return projection
}

func reducecontextQueryProjection(projection *contextQueryProjection) bool {
	if stripCompactQueryOperationalSamples(projection) {
		return true
	}

	if dropCompactQueryMetadata(projection) {
		return true
	}

	if len(projection.Edges) > 1 {
		projection.Edges = projection.Edges[:len(projection.Edges)-1]
		projection.EdgesOmitted = projection.EdgeCount - len(projection.Edges)
		return true
	}

	if len(projection.Nodes) > 1 {
		projection.Nodes = projection.Nodes[:len(projection.Nodes)-1]
		projection.NodesOmitted = projection.NodeCount - len(projection.Nodes)
		return true
	}

	if len(projection.Claims) > 1 {
		projection.Claims = projection.Claims[:len(projection.Claims)-1]
		projection.ClaimsOmitted = projection.ClaimCount - len(projection.Claims)
		return true
	}

	if len(projection.UnresolvedGaps) > 0 {
		projection.UnresolvedGaps = projection.UnresolvedGaps[:len(projection.UnresolvedGaps)-1]
		projection.UnresolvedGapsOmitted = projection.UnresolvedGapCount - len(projection.UnresolvedGaps)
		return true
	}

	return false
}

func dropCompactQueryMetadata(projection *contextQueryProjection) bool {
	for i := len(projection.Edges) - 1; i >= 0; i-- {
		if dropLastCompactQueryMetadataEntry(projection.Edges[i].Evidence) {
			projection.Edges[i].EvidenceOmitted = projection.Edges[i].EvidenceCount - len(projection.Edges[i].Evidence)
			return true
		}
	}

	for i := len(projection.Nodes) - 1; i >= 0; i-- {
		if dropLastCompactQueryMetadataEntry(projection.Nodes[i].Properties) {
			projection.Nodes[i].PropertiesOmitted = projection.Nodes[i].PropertyCount - len(projection.Nodes[i].Properties)
			return true
		}
	}

	return false
}

func dropLastCompactQueryMetadataEntry(metadata map[string]any) bool {
	if len(metadata) == 0 {
		return false
	}

	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	delete(metadata, keys[len(keys)-1])

	return true
}

func stripCompactQueryOperationalSamples(projection *contextQueryProjection) bool {
	changed := stripcontextQueryRefreshSamples(projection.AutoRefresh)
	if projection.Freshness != nil {
		changed = stripcontextQueryRefreshSamples(projection.Freshness.Refresh) || changed
		changed = stripCompactQueryInventory(&projection.Freshness.CheckedPaths, projection.Freshness.CheckedPathCount, &projection.Freshness.CheckedPathsOmitted) || changed
		changed = stripCompactQueryInventory(&projection.Freshness.StalePaths, projection.Freshness.StalePathCount, &projection.Freshness.StalePathsOmitted) || changed
		changed = stripCompactQueryInventory(&projection.Freshness.MissingPaths, projection.Freshness.MissingPathCount, &projection.Freshness.MissingPathsOmitted) || changed
		changed = stripCompactQueryInventory(&projection.Freshness.ScopeWarningOutOf, projection.Freshness.ScopeWarningOutOfCount, &projection.Freshness.ScopeWarningOutOfOmitted) || changed
		changed = stripCompactQueryInventory(&projection.Freshness.StalenessWarningFiles, projection.Freshness.StalenessWarningFileCount, &projection.Freshness.StalenessWarningFilesOmitted) || changed
	}

	return changed
}

func stripcontextQueryRefreshSamples(refresh *contextQueryRefresh) bool {
	if refresh == nil {
		return false
	}

	changed := stripCompactQueryInventory(&refresh.RefreshedFiles, refresh.RefreshedFileCount, &refresh.RefreshedFilesOmitted)
	changed = stripCompactQueryInventory(&refresh.RefreshScopes, refresh.RefreshScopeCount, &refresh.RefreshScopesOmitted) || changed
	if len(refresh.FollowUpRefreshes) > 0 {
		refresh.FollowUpRefreshes = nil
		refresh.FollowUpRefreshesOmitted = refresh.FollowUpRefreshCount
		changed = true
	}

	return changed
}

func stripCompactQueryInventory(values *[]string, total int, omitted *int) bool {
	if len(*values) == 0 {
		return false
	}

	*values = nil
	*omitted = total

	return true
}

func compactQueryMetadata(metadata map[string]any, declaredCount int) (map[string]any, int, int) {
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	count := max(declaredCount, len(keys))
	retained := min(len(keys), contextQueryMetadataEntryLimit)
	if retained == 0 {
		return nil, count, count
	}

	compact := make(map[string]any, retained)
	for _, key := range keys[:retained] {
		compact[compactQueryMetadataKey(key)] = compactQueryMetadataValue(metadata[key])
	}

	return compact, count, count - len(compact)
}

func compactQueryMetadataKey(key string) string {
	key = strings.ToValidUTF8(strings.TrimSpace(key), "�")
	if encodedJSONStringLength(key) <= compactQueryMetadataKeyEncodedLimit {
		return key
	}

	sum := sha256.Sum256([]byte(key))
	suffix := "~" + hex.EncodeToString(sum[:4])
	prefix := strings.TrimSuffix(compactQueryText(key, compactQueryMetadataKeyEncodedLimit-32), "…")

	return prefix + suffix
}

func compactQueryMetadataValue(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case contextQueryMetadataSummary:
		return normalizeContextQueryMetadataSummary(typed)
	case *contextQueryMetadataSummary:
		if typed == nil {
			return nil
		}

		return normalizeContextQueryMetadataSummary(*typed)
	case string:
		preview, omitted := compactQueryTextWithOmission(typed, compactQueryMetadataPreviewEncodedLimit)
		if omitted == 0 {
			return preview
		}

		return contextQueryMetadataSummary{Type: "string", Preview: preview, ByteCount: len(typed), OmittedBytes: omitted}
	case bool:
		return typed
	case json.Number:
		return compactQueryText(typed.String(), compactQueryMetadataPreviewEncodedLimit)
	case float32:
		if math.IsNaN(float64(typed)) || math.IsInf(float64(typed), 0) {
			return contextQueryMetadataSummary{Type: "number", Preview: strconv.FormatFloat(float64(typed), 'g', -1, 32)}
		}

		return typed
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return contextQueryMetadataSummary{Type: "number", Preview: strconv.FormatFloat(typed, 'g', -1, 64)}
		}

		return typed
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return typed
	}

	original := reflect.ValueOf(value)
	reflected, dereferenceState := dereferenceCompactQueryMetadataValue(original)
	if dereferenceState != "" {
		return contextQueryMetadataSummary{
			Type:    compactQueryMetadataTypeName(original),
			Preview: dereferenceState,
		}
	}

	if !reflected.IsValid() {
		return nil
	}

	switch reflected.Kind() {
	case reflect.Map:
		return compactQueryMapSummary(reflected)
	case reflect.Array, reflect.Slice:
		return compactQueryListSummary(reflected)
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128:
		return contextQueryMetadataSummary{
			Type:    compactQueryMetadataTypeName(reflected),
			Preview: compactQueryStableValueSample(reflected, "type:"),
		}
	default:
		return contextQueryMetadataSummary{
			Type: compactQueryMetadataTypeName(reflected),
		}
	}
}

type compactQueryMetadataReference struct {
	valueType reflect.Type
	pointer   uintptr
}

func dereferenceCompactQueryMetadataValue(value reflect.Value) (reflect.Value, string) {
	visited := make(map[compactQueryMetadataReference]struct{})
	for range compactQueryMetadataDereferenceLimit {
		if !value.IsValid() {
			return reflect.Value{}, ""
		}

		switch value.Kind() {
		case reflect.Interface:
			if value.IsNil() {
				return reflect.Value{}, ""
			}
			value = value.Elem()
		case reflect.Pointer:
			if value.IsNil() {
				return reflect.Value{}, ""
			}

			reference := compactQueryMetadataReference{valueType: value.Type(), pointer: value.Pointer()}
			if _, ok := visited[reference]; ok {
				return value, "cyclic_reference"
			}
			visited[reference] = struct{}{}
			value = value.Elem()
		default:
			return value, ""
		}
	}

	return value, "dereference_limit"
}

func compactQueryMetadataTypeName(value reflect.Value) string {
	if !value.IsValid() {
		return "null"
	}

	return compactQueryText(value.Type().String(), compactQueryMetadataPreviewEncodedLimit)
}

func compactQueryMapSummary(value reflect.Value) contextQueryMetadataSummary {
	itemCount := value.Len()
	samples := make([]string, 0, min(itemCount, contextQueryMetadataSampleLimit)+1)
	iterator := value.MapRange()
	for iterator.Next() {
		sample := compactQueryMapKeySample(iterator.Key())
		index, _ := slices.BinarySearch(samples, sample)
		if len(samples) == contextQueryMetadataSampleLimit && index >= contextQueryMetadataSampleLimit {
			continue
		}

		samples = append(samples, "")
		copy(samples[index+1:], samples[index:])
		samples[index] = sample
		if len(samples) > contextQueryMetadataSampleLimit {
			samples = samples[:contextQueryMetadataSampleLimit]
		}
	}

	return contextQueryMetadataSummary{
		Type:         "object",
		ItemCount:    itemCount,
		Samples:      samples,
		ItemsOmitted: itemCount - len(samples),
	}
}

func compactQueryMapKeySample(value reflect.Value) string {
	for range compactQueryMetadataDereferenceLimit {
		if !value.IsValid() || value.Kind() != reflect.Interface {
			break
		}
		if value.IsNil() {
			return "null"
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return "null"
	}

	return compactQueryStableValueSample(value, "key_type:")
}

func compactQueryStableValueSample(value reflect.Value, unsupportedPrefix string) string {
	var sample string
	switch value.Kind() {
	case reflect.String:
		sample = value.String()
	case reflect.Bool:
		sample = strconv.FormatBool(value.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		sample = strconv.FormatInt(value.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		sample = strconv.FormatUint(value.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		sample = strconv.FormatFloat(value.Float(), 'g', -1, value.Type().Bits())
	case reflect.Complex64, reflect.Complex128:
		sample = strconv.FormatComplex(value.Complex(), 'g', -1, value.Type().Bits())
	default:
		sample = unsupportedPrefix + value.Type().String()
	}

	return compactQueryText(sample, compactQueryMetadataPreviewEncodedLimit)
}

func compactQueryListSummary(value reflect.Value) contextQueryMetadataSummary {
	count := value.Len()
	sampleCount := min(count, contextQueryMetadataSampleLimit)
	samples := make([]string, 0, sampleCount)
	for i := 0; i < sampleCount; i++ {
		samples = append(samples, compactQueryMetadataSample(value.Index(i)))
	}

	return contextQueryMetadataSummary{
		Type:         "array",
		ItemCount:    count,
		Samples:      samples,
		ItemsOmitted: count - len(samples),
	}
}

func compactQueryMetadataSample(value reflect.Value) string {
	original := value
	value, dereferenceState := dereferenceCompactQueryMetadataValue(value)
	if dereferenceState != "" {
		return compactQueryText(dereferenceState+":"+compactQueryMetadataTypeName(original), compactQueryMetadataPreviewEncodedLimit)
	}

	if !value.IsValid() {
		return "null"
	}

	switch value.Kind() {
	case reflect.Map:
		return fmt.Sprintf("object(%d entries)", value.Len())
	case reflect.Array, reflect.Slice:
		return fmt.Sprintf("array(%d items)", value.Len())
	default:
		return compactQueryStableValueSample(value, "type:")
	}
}

func normalizeContextQueryMetadataSummary(summary contextQueryMetadataSummary) contextQueryMetadataSummary {
	summary.Type = compactQueryText(summary.Type, compactQueryMetadataPreviewEncodedLimit)
	summary.Preview = compactQueryText(summary.Preview, compactQueryMetadataPreviewEncodedLimit)
	summary.Samples = compactQueryTextSlice(summary.Samples, contextQueryMetadataSampleLimit)
	summary.ItemCount = max(summary.ItemCount, len(summary.Samples)+summary.ItemsOmitted)
	summary.ItemsOmitted = summary.ItemCount - len(summary.Samples)

	return summary
}

func newContextQueryFreshness(freshness *comprehension.FreshnessDecision) *contextQueryFreshness {
	if freshness == nil {
		return nil
	}

	checkedCount, checkedPaths, checkedOmitted := compactQueryStringInventory(freshness.CheckedPaths, freshness.CheckedPathCount)
	staleCount, stalePaths, staleOmitted := compactQueryStringInventory(freshness.StalePaths, freshness.StalePathCount)
	missingCount, missingPaths, missingOmitted := compactQueryStringInventory(freshness.MissingPaths, freshness.MissingPathCount)
	scopeWarningCount, scopeWarningPaths, scopeWarningOmitted := compactQueryStringInventory(freshness.ScopeWarningOutOf, 0)
	stalenessWarningCount, stalenessWarningPaths, stalenessWarningOmitted := compactQueryStringInventory(freshness.StalenessWarningFiles, 0)

	return &contextQueryFreshness{
		Policy:                       freshness.Policy,
		Operation:                    compactQueryText(freshness.Operation, compactQueryCoreTextEncodedLimit),
		IndexID:                      compactQueryText(freshness.IndexID, compactQueryCoreTextEncodedLimit),
		StoreKind:                    compactQueryText(freshness.StoreKind, compactQueryCoreTextEncodedLimit),
		GeneratedAt:                  compactQueryText(freshness.GeneratedAt, compactQueryCoreTextEncodedLimit),
		FreshForScope:                freshness.FreshForScope,
		GlobalFresh:                  freshness.GlobalFresh,
		Scope:                        compactQueryText(freshness.Scope, compactQueryCoreTextEncodedLimit),
		Reason:                       compactQueryText(freshness.Reason, compactQueryCoreTextEncodedLimit),
		Blocker:                      compactQueryText(freshness.Blocker, compactQueryCoreTextEncodedLimit),
		BudgetExceeded:               freshness.BudgetExceeded,
		Refresh:                      newContextQueryRefresh(freshness.Refresh, true),
		CheckedPathCount:             checkedCount,
		CheckedPaths:                 checkedPaths,
		CheckedPathsOmitted:          checkedOmitted,
		StalePathCount:               staleCount,
		StalePaths:                   stalePaths,
		StalePathsOmitted:            staleOmitted,
		MissingPathCount:             missingCount,
		MissingPaths:                 missingPaths,
		MissingPathsOmitted:          missingOmitted,
		ScopeWarning:                 compactQueryText(freshness.ScopeWarning, compactQueryCoreTextEncodedLimit),
		ScopeWarningOutOfCount:       scopeWarningCount,
		ScopeWarningOutOf:            scopeWarningPaths,
		ScopeWarningOutOfOmitted:     scopeWarningOmitted,
		StalenessWarning:             compactQueryText(freshness.StalenessWarning, compactQueryCoreTextEncodedLimit),
		StalenessWarningFileCount:    stalenessWarningCount,
		StalenessWarningFiles:        stalenessWarningPaths,
		StalenessWarningFilesOmitted: stalenessWarningOmitted,
	}
}

func normalizecontextQueryFreshness(freshness *contextQueryFreshness) *contextQueryFreshness {
	if freshness == nil {
		return nil
	}

	checkedCount, checkedPaths, checkedOmitted := compactQueryStringInventory(freshness.CheckedPaths, freshness.CheckedPathCount)
	staleCount, stalePaths, staleOmitted := compactQueryStringInventory(freshness.StalePaths, freshness.StalePathCount)
	missingCount, missingPaths, missingOmitted := compactQueryStringInventory(freshness.MissingPaths, freshness.MissingPathCount)
	scopeWarningCount, scopeWarningPaths, scopeWarningOmitted := compactQueryStringInventory(freshness.ScopeWarningOutOf, freshness.ScopeWarningOutOfCount)
	stalenessWarningCount, stalenessWarningPaths, stalenessWarningOmitted := compactQueryStringInventory(freshness.StalenessWarningFiles, freshness.StalenessWarningFileCount)

	normalized := *freshness
	normalized.Operation = compactQueryText(freshness.Operation, compactQueryCoreTextEncodedLimit)
	normalized.Policy = comprehension.FreshnessPolicy(compactQueryText(string(freshness.Policy), compactQueryCoreTextEncodedLimit))
	normalized.IndexID = compactQueryText(freshness.IndexID, compactQueryCoreTextEncodedLimit)
	normalized.StoreKind = compactQueryText(freshness.StoreKind, compactQueryCoreTextEncodedLimit)
	normalized.GeneratedAt = compactQueryText(freshness.GeneratedAt, compactQueryCoreTextEncodedLimit)
	normalized.Scope = compactQueryText(freshness.Scope, compactQueryCoreTextEncodedLimit)
	normalized.Reason = compactQueryText(freshness.Reason, compactQueryCoreTextEncodedLimit)
	normalized.Blocker = compactQueryText(freshness.Blocker, compactQueryCoreTextEncodedLimit)
	normalized.Refresh = normalizecontextQueryRefresh(freshness.Refresh, true)
	normalized.CheckedPathCount = checkedCount
	normalized.CheckedPaths = checkedPaths
	normalized.CheckedPathsOmitted = checkedOmitted
	normalized.StalePathCount = staleCount
	normalized.StalePaths = stalePaths
	normalized.StalePathsOmitted = staleOmitted
	normalized.MissingPathCount = missingCount
	normalized.MissingPaths = missingPaths
	normalized.MissingPathsOmitted = missingOmitted
	normalized.ScopeWarning = compactQueryText(freshness.ScopeWarning, compactQueryCoreTextEncodedLimit)
	normalized.ScopeWarningOutOfCount = scopeWarningCount
	normalized.ScopeWarningOutOf = scopeWarningPaths
	normalized.ScopeWarningOutOfOmitted = scopeWarningOmitted
	normalized.StalenessWarning = compactQueryText(freshness.StalenessWarning, compactQueryCoreTextEncodedLimit)
	normalized.StalenessWarningFileCount = stalenessWarningCount
	normalized.StalenessWarningFiles = stalenessWarningPaths
	normalized.StalenessWarningFilesOmitted = stalenessWarningOmitted

	return &normalized
}

func newContextQueryRefresh(refresh *comprehension.RefreshReport, includeFollowUps bool) *contextQueryRefresh {
	if refresh == nil {
		return nil
	}

	refreshedCount, refreshedFiles, refreshedOmitted := compactQueryStringInventory(refresh.RefreshedFiles, 0)
	refreshScopeCount, refreshScopes, refreshScopesOmitted := compactQueryStringInventory(refresh.RefreshScopes, 0)
	followUpCount := len(refresh.FollowUpRefreshes)
	followUpLimit := 0
	if includeFollowUps {
		followUpLimit = min(followUpCount, compactQueryFollowUpRefreshLimit)
	}

	followUps := make([]contextQueryRefresh, 0, followUpLimit)
	for i := 0; i < followUpLimit; i++ {
		compact := newContextQueryRefresh(&refresh.FollowUpRefreshes[i], false)
		if compact != nil {
			followUps = append(followUps, *compact)
		}
	}

	return &contextQueryRefresh{
		Attempted:                refresh.Attempted,
		Scope:                    compactQueryText(refresh.Scope, compactQueryCoreTextEncodedLimit),
		Language:                 compactQueryText(refresh.Language, compactQueryCoreTextEncodedLimit),
		Reason:                   compactQueryText(refresh.Reason, compactQueryCoreTextEncodedLimit),
		SkippedReason:            compactQueryText(refresh.SkippedReason, compactQueryCoreTextEncodedLimit),
		Error:                    compactQueryText(refresh.Error, compactQueryCoreTextEncodedLimit),
		IndexID:                  compactQueryText(refresh.IndexID, compactQueryCoreTextEncodedLimit),
		FilesIndexed:             refresh.FilesIndexed,
		SourceFileCount:          refresh.SourceFileCount,
		EstimatedMinimum:         refresh.EstimatedMinimum,
		StalePathCount:           refresh.StalePathCount,
		MaxFiles:                 refresh.MaxFiles,
		SymbolsIndexed:           refresh.SymbolsIndexed,
		EdgesIndexed:             refresh.EdgesIndexed,
		PartialRefreshReason:     compactQueryText(refresh.PartialRefreshReason, compactQueryCoreTextEncodedLimit),
		FullRebuildReason:        compactQueryText(refresh.FullRebuildReason, compactQueryCoreTextEncodedLimit),
		FreshAfter:               refresh.FreshAfter,
		RefreshedFileCount:       refreshedCount,
		RefreshedFiles:           refreshedFiles,
		RefreshedFilesOmitted:    refreshedOmitted,
		RefreshScopeCount:        refreshScopeCount,
		RefreshScopes:            refreshScopes,
		RefreshScopesOmitted:     refreshScopesOmitted,
		FollowUpRefreshCount:     followUpCount,
		FollowUpRefreshes:        followUps,
		FollowUpRefreshesOmitted: followUpCount - len(followUps),
	}
}

func normalizecontextQueryRefresh(refresh *contextQueryRefresh, includeFollowUps bool) *contextQueryRefresh {
	if refresh == nil {
		return nil
	}

	refreshedCount, refreshedFiles, refreshedOmitted := compactQueryStringInventory(refresh.RefreshedFiles, refresh.RefreshedFileCount)
	refreshScopeCount, refreshScopes, refreshScopesOmitted := compactQueryStringInventory(refresh.RefreshScopes, refresh.RefreshScopeCount)
	followUpCount := max(refresh.FollowUpRefreshCount, len(refresh.FollowUpRefreshes)+refresh.FollowUpRefreshesOmitted)
	followUpLimit := 0
	if includeFollowUps {
		followUpLimit = min(len(refresh.FollowUpRefreshes), compactQueryFollowUpRefreshLimit)
	}

	followUps := make([]contextQueryRefresh, 0, followUpLimit)
	for i := 0; i < followUpLimit; i++ {
		normalized := normalizecontextQueryRefresh(&refresh.FollowUpRefreshes[i], false)
		followUps = append(followUps, *normalized)
	}

	normalized := *refresh
	normalized.Scope = compactQueryText(refresh.Scope, compactQueryCoreTextEncodedLimit)
	normalized.Language = compactQueryText(refresh.Language, compactQueryCoreTextEncodedLimit)
	normalized.Reason = compactQueryText(refresh.Reason, compactQueryCoreTextEncodedLimit)
	normalized.SkippedReason = compactQueryText(refresh.SkippedReason, compactQueryCoreTextEncodedLimit)
	normalized.Error = compactQueryText(refresh.Error, compactQueryCoreTextEncodedLimit)
	normalized.IndexID = compactQueryText(refresh.IndexID, compactQueryCoreTextEncodedLimit)
	normalized.PartialRefreshReason = compactQueryText(refresh.PartialRefreshReason, compactQueryCoreTextEncodedLimit)
	normalized.FullRebuildReason = compactQueryText(refresh.FullRebuildReason, compactQueryCoreTextEncodedLimit)
	normalized.RefreshedFileCount = refreshedCount
	normalized.RefreshedFiles = refreshedFiles
	normalized.RefreshedFilesOmitted = refreshedOmitted
	normalized.RefreshScopeCount = refreshScopeCount
	normalized.RefreshScopes = refreshScopes
	normalized.RefreshScopesOmitted = refreshScopesOmitted
	normalized.FollowUpRefreshCount = followUpCount
	normalized.FollowUpRefreshes = followUps
	normalized.FollowUpRefreshesOmitted = followUpCount - len(followUps)

	return &normalized
}

func compactQueryConfidence(confidence comprehension.GraphResultConfidence) comprehension.GraphResultConfidence {
	confidence.Level = compactQueryText(confidence.Level, compactQueryCoreTextEncodedLimit)
	confidence.Source = compactQueryText(confidence.Source, compactQueryCoreTextEncodedLimit)
	confidence.Reason = compactQueryText(confidence.Reason, compactQueryCoreTextEncodedLimit)

	return confidence
}

func compactQueryProvenance(provenance comprehension.GraphResultProvenance) comprehension.GraphResultProvenance {
	provenance.Source = compactQueryText(provenance.Source, compactQueryCoreTextEncodedLimit)
	provenance.Operation = compactQueryText(provenance.Operation, compactQueryCoreTextEncodedLimit)
	provenance.StoreKind = compactQueryText(provenance.StoreKind, compactQueryCoreTextEncodedLimit)
	provenance.IndexID = compactQueryText(provenance.IndexID, compactQueryCoreTextEncodedLimit)
	provenance.GeneratedAt = compactQueryText(provenance.GeneratedAt, compactQueryCoreTextEncodedLimit)
	provenance.CollectorVersion = compactQueryText(provenance.CollectorVersion, compactQueryCoreTextEncodedLimit)
	provenance.CurrentCollectorVersion = compactQueryText(provenance.CurrentCollectorVersion, compactQueryCoreTextEncodedLimit)
	provenance.ArtifactPath = compactQueryText(provenance.ArtifactPath, compactQueryCoreTextEncodedLimit)

	return provenance
}

func compactQueryStaleEvidence(stale comprehension.StaleEvidenceSafety) comprehension.StaleEvidenceSafety {
	stale.State = compactQueryText(stale.State, compactQueryCoreTextEncodedLimit)
	stale.Policy = comprehension.FreshnessPolicy(compactQueryText(string(stale.Policy), compactQueryCoreTextEncodedLimit))
	stale.Reason = compactQueryText(stale.Reason, compactQueryCoreTextEncodedLimit)

	return stale
}

func compactQueryTextSlice(values []string, limit int) []string {
	retained := min(len(values), max(limit, 0))
	compact := make([]string, retained)
	for i := 0; i < retained; i++ {
		compact[i] = compactQueryText(values[i], compactQueryCoreTextEncodedLimit)
	}

	return compact
}

func compactQueryStringInventory(values []string, declaredCount int) (int, []string, int) {
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToValidUTF8(strings.TrimSpace(value), "�")
		if value == "" {
			continue
		}

		if _, ok := seen[value]; ok {
			continue
		}

		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}

	slices.Sort(normalized)
	total := max(declaredCount, len(values))
	sampleCount := min(len(normalized), compactQueryInventorySampleLimit)
	samples := make([]string, sampleCount)
	for i := 0; i < sampleCount; i++ {
		samples[i] = compactQueryText(normalized[i], compactQueryCoreTextEncodedLimit)
	}

	return total, samples, total - len(samples)
}

func compactQueryText(value string, maxEncodedBytes int) string {
	compact, _ := compactQueryTextWithOmission(value, maxEncodedBytes)

	return compact
}

func compactQueryTextWithOmission(value string, maxEncodedBytes int) (string, int) {
	value = strings.ToValidUTF8(strings.TrimSpace(value), "�")
	if encodedJSONStringLength(value) <= maxEncodedBytes {
		return value, 0
	}

	runes := []rune(value)
	low, high := 0, len(runes)
	best := ""
	bestRunes := 0
	for low <= high {
		middle := low + (high-low)/2
		candidate := string(runes[:middle]) + "…"
		if encodedJSONStringLength(candidate) <= maxEncodedBytes {
			best = candidate
			bestRunes = middle
			low = middle + 1
		} else {
			high = middle - 1
		}
	}

	if best == "" {
		return "", len(value)
	}

	retainedBytes := len(string(runes[:bestRunes]))

	return best, len(value) - retainedBytes
}

func encodedJSONStringLength(value string) int {
	encoded, _ := json.Marshal(value)

	return len(encoded)
}
