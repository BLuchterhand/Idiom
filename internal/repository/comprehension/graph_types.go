package comprehension

import (
	"time"
)

const (
	SchemaVersion = 1
	storeKind     = "json-graph-v1"
)

type Node struct {
	ID            string         `json:"id"`
	Kind          string         `json:"kind"`
	Path          string         `json:"path,omitempty"`
	Language      string         `json:"language,omitempty"`
	QualifiedName string         `json:"qualified_name,omitempty"`
	StartLine     int            `json:"start_line,omitempty"`
	EndLine       int            `json:"end_line,omitempty"`
	Fingerprint   string         `json:"fingerprint,omitempty"`
	GitHead       string         `json:"git_head,omitempty"`
	Collector     string         `json:"collector"`
	Confidence    string         `json:"confidence"`
	Properties    map[string]any `json:"properties,omitempty"`
	CreatedAt     string         `json:"created_at"`
	UpdatedAt     string         `json:"updated_at"`
}

type Edge struct {
	ID         string         `json:"id"`
	SourceID   string         `json:"source_id"`
	TargetID   string         `json:"target_id"`
	Kind       string         `json:"kind"`
	Collector  string         `json:"collector"`
	Confidence string         `json:"confidence"`
	Evidence   map[string]any `json:"evidence,omitempty"`
	Properties map[string]any `json:"properties,omitempty"`
	CreatedAt  string         `json:"created_at"`
	UpdatedAt  string         `json:"updated_at"`
}

type Graph struct {
	SchemaVersion     int               `json:"schema_version"`
	ID                string            `json:"id"`
	Root              string            `json:"root"`
	GeneratedAt       string            `json:"generated_at"`
	GitHead           string            `json:"git_head,omitempty"`
	GitDirty          bool              `json:"git_dirty"`
	StoreKind         string            `json:"store_kind"`
	CollectorVersion  string            `json:"collector_version,omitempty"`
	CollectorVersions map[string]string `json:"collector_versions,omitempty"`
	Languages         []string          `json:"languages,omitempty"`
	CollectorReports  []CollectorReport `json:"collector_reports,omitempty"`
	Nodes             []Node            `json:"nodes"`
	Edges             []Edge            `json:"edges"`
}

type Manifest struct {
	SchemaVersion     int                  `json:"schema_version"`
	Root              string               `json:"root"`
	IndexID           string               `json:"index_id,omitempty"`
	StoreKind         string               `json:"store_kind"`
	GraphPath         string               `json:"graph_path"`
	GitHead           string               `json:"git_head,omitempty"`
	GitDirty          bool                 `json:"git_dirty"`
	GeneratedAt       string               `json:"generated_at"`
	CollectorVersion  string               `json:"collector_version,omitempty"`
	CollectorVersions map[string]string    `json:"collector_versions,omitempty"`
	Languages         []string             `json:"languages,omitempty"`
	FilesIndexed      int                  `json:"files_indexed"`
	SymbolsIndexed    int                  `json:"symbols_indexed"`
	EdgesIndexed      int                  `json:"edges_indexed"`
	StorageBackends   []StoreBackendReport `json:"storage_backends,omitempty"`
	CollectorReports  []CollectorReport    `json:"collector_reports,omitempty"`
}

type BuildOptions struct {
	Root             string
	Path             string
	Language         string
	Mode             string
	MaxFiles         int
	Now              time.Time
	ExactScope       bool
	PatchActiveStore bool
	Enrichers        []GraphEnricher
}

type BuildReport struct {
	IndexID                string               `json:"index_id"`
	ArtifactPath           string               `json:"artifact_path"`
	ManifestPath           string               `json:"manifest_path"`
	StoreKind              string               `json:"store_kind"`
	Root                   string               `json:"root"`
	GitHead                string               `json:"git_head,omitempty"`
	GitDirty               bool                 `json:"git_dirty"`
	CollectorVersion       string               `json:"collector_version,omitempty"`
	CollectorVersions      map[string]string    `json:"collector_versions,omitempty"`
	Incremental            bool                 `json:"incremental,omitempty"`
	ReusedIndexedFiles     int                  `json:"reused_indexed_files,omitempty"`
	ExpandedScope          string               `json:"expanded_scope,omitempty"`
	DependencyRefreshFiles []string             `json:"dependency_refresh_files,omitempty"`
	FullRebuildReason      string               `json:"full_rebuild_reason,omitempty"`
	PartialRefreshReason   string               `json:"partial_refresh_reason,omitempty"`
	Languages              []string             `json:"languages"`
	FilesIndexed           int                  `json:"files_indexed"`
	SymbolsIndexed         int                  `json:"symbols_indexed"`
	EdgesIndexed           int                  `json:"edges_indexed"`
	StorageBackends        []StoreBackendReport `json:"storage_backends,omitempty"`
	RefreshedStaleFiles    []string             `json:"refreshed_stale_files,omitempty"`
	RefreshedStaleSymbols  int                  `json:"refreshed_stale_symbols,omitempty"`
	CollectorCoverage      []string             `json:"collector_coverage"`
	CollectorReports       []CollectorReport    `json:"collector_reports,omitempty"`
	UnsupportedLanguages   []string             `json:"unsupported_languages,omitempty"`
}

type StatusReport struct {
	Indexed                  bool                  `json:"indexed"`
	IndexID                  string                `json:"index_id,omitempty"`
	StoreKind                string                `json:"store_kind,omitempty"`
	Root                     string                `json:"root"`
	GitHead                  string                `json:"git_head,omitempty"`
	GitDirty                 bool                  `json:"git_dirty"`
	GeneratedAt              string                `json:"generated_at,omitempty"`
	CollectorVersion         string                `json:"collector_version,omitempty"`
	CurrentCollectorVersion  string                `json:"current_collector_version,omitempty"`
	CollectorVersionMismatch bool                  `json:"collector_version_mismatch,omitempty"`
	Languages                []string              `json:"languages,omitempty"`
	FilesIndexed             int                   `json:"files_indexed,omitempty"`
	SymbolsIndexed           int                   `json:"symbols_indexed,omitempty"`
	EdgesIndexed             int                   `json:"edges_indexed,omitempty"`
	StorageBackends          []StoreBackendReport  `json:"storage_backends,omitempty"`
	CollectorReports         []CollectorReport     `json:"collector_reports,omitempty"`
	StaleFiles               []string              `json:"stale_files,omitempty"`
	StaleFileCount           int                   `json:"stale_file_count"`
	StaleSymbols             int                   `json:"stale_symbols,omitempty"`
	StaleSymbolSamples       []NodeSummary         `json:"stale_symbol_samples,omitempty"`
	MissingFiles             []string              `json:"missing_files,omitempty"`
	MissingFileCount         int                   `json:"missing_file_count"`
	StalePathCount           int                   `json:"stale_path_count"`
	Fresh                    bool                  `json:"fresh"`
	Confidence               GraphResultConfidence `json:"confidence"`
	Provenance               GraphResultProvenance `json:"provenance"`
	AutoRefresh              *RefreshReport        `json:"auto_refresh,omitempty"`
	Freshness                *FreshnessDecision    `json:"freshness,omitempty"`
	StaleEvidence            StaleEvidenceSafety   `json:"stale_evidence"`
}

type QueryOptions struct {
	Root            string
	Query           string
	Path            string
	Symbol          string
	Scope           string
	Resolution      string
	Limit           int
	IncludeEvidence bool
}

type QueryResult struct {
	IndexID        string                `json:"index_id,omitempty"`
	StoreKind      string                `json:"store_kind,omitempty"`
	Fresh          bool                  `json:"fresh"`
	Resolution     string                `json:"resolution,omitempty"`
	GeneratedAt    string                `json:"generated_at,omitempty"`
	Confidence     GraphResultConfidence `json:"confidence"`
	Provenance     GraphResultProvenance `json:"provenance"`
	AutoRefresh    *RefreshReport        `json:"auto_refresh,omitempty"`
	Freshness      *FreshnessDecision    `json:"freshness,omitempty"`
	StaleEvidence  StaleEvidenceSafety   `json:"stale_evidence"`
	Claims         []string              `json:"claims,omitempty"`
	Nodes          []NodeSummary         `json:"nodes,omitempty"`
	Edges          []EdgeSummary         `json:"edges,omitempty"`
	UnresolvedGaps []string              `json:"unresolved_gaps,omitempty"`
}

type RefreshReport struct {
	Attempted            bool            `json:"attempted"`
	Scope                string          `json:"scope,omitempty"`
	Language             string          `json:"language,omitempty"`
	Reason               string          `json:"reason,omitempty"`
	SkippedReason        string          `json:"skipped_reason,omitempty"`
	Error                string          `json:"error,omitempty"`
	IndexID              string          `json:"index_id,omitempty"`
	FilesIndexed         int             `json:"files_indexed,omitempty"`
	SourceFileCount      int             `json:"source_file_count,omitempty"`
	EstimatedMinimum     bool            `json:"estimated_minimum,omitempty"`
	StalePathCount       int             `json:"stale_path_count,omitempty"`
	MaxFiles             int             `json:"max_files,omitempty"`
	SymbolsIndexed       int             `json:"symbols_indexed,omitempty"`
	EdgesIndexed         int             `json:"edges_indexed,omitempty"`
	PartialRefreshReason string          `json:"partial_refresh_reason,omitempty"`
	FullRebuildReason    string          `json:"full_rebuild_reason,omitempty"`
	FreshAfter           bool            `json:"fresh_after,omitempty"`
	RefreshedFiles       []string        `json:"refreshed_files,omitempty"`
	RefreshScopes        []string        `json:"refresh_scopes,omitempty"`
	FollowUpRefreshes    []RefreshReport `json:"follow_up_refreshes,omitempty"`
}

type ZoomOptions struct {
	Root          string
	Handle        string
	Resolution    string
	Limit         int
	IncludeSource bool
}

type ZoomResult struct {
	IndexID        string                `json:"index_id,omitempty"`
	StoreKind      string                `json:"store_kind,omitempty"`
	Fresh          bool                  `json:"fresh"`
	Freshness      *FreshnessDecision    `json:"freshness,omitempty"`
	Resolution     string                `json:"resolution,omitempty"`
	GeneratedAt    string                `json:"generated_at,omitempty"`
	Confidence     GraphResultConfidence `json:"confidence"`
	Provenance     GraphResultProvenance `json:"provenance"`
	StaleEvidence  StaleEvidenceSafety   `json:"stale_evidence"`
	Node           *NodeSummary          `json:"node,omitempty"`
	RelatedNodes   []NodeSummary         `json:"related_nodes,omitempty"`
	Edges          []EdgeSummary         `json:"edges,omitempty"`
	UnresolvedGaps []string              `json:"unresolved_gaps,omitempty"`
}

type NodeSummary struct {
	ID            string         `json:"id"`
	Kind          string         `json:"kind"`
	Path          string         `json:"path,omitempty"`
	Language      string         `json:"language,omitempty"`
	QualifiedName string         `json:"qualified_name,omitempty"`
	StartLine     int            `json:"start_line,omitempty"`
	EndLine       int            `json:"end_line,omitempty"`
	Confidence    string         `json:"confidence"`
	Stale         bool           `json:"stale,omitempty"`
	Properties    map[string]any `json:"properties,omitempty"`
}

type EdgeSummary struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	SourceID   string         `json:"source_id"`
	TargetID   string         `json:"target_id"`
	Confidence string         `json:"confidence"`
	Evidence   map[string]any `json:"evidence,omitempty"`
}
