package comprehension

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	jsonGraphSchemaVersion   = SchemaVersion
	sqliteGraphSchemaVersion = 1
	sqliteStoreKind          = "sqlite-graph-v1"
)

type StoreBackendReport struct {
	Kind                 string `json:"kind"`
	Path                 string `json:"path"`
	MetadataPath         string `json:"metadata_path,omitempty"`
	Status               string `json:"status"`
	Active               bool   `json:"active,omitempty"`
	SchemaVersion        int    `json:"schema_version,omitempty"`
	CurrentSchemaVersion int    `json:"current_schema_version,omitempty"`
	Compatible           bool   `json:"compatible"`
	Reason               string `json:"reason,omitempty"`
}

type SQLiteMetadata struct {
	StoreKind     string `json:"store_kind"`
	SchemaVersion int    `json:"schema_version"`
	SourceIndexID string `json:"source_index_id,omitempty"`
	CreatedAt     string `json:"created_at,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

func storageBackends(root string, activeKind string) []StoreBackendReport {
	return []StoreBackendReport{
		jsonStorageBackend(root, activeKind),
		sqliteStorageBackend(root, activeKind),
	}
}

func jsonStorageBackend(root string, activeKind string) StoreBackendReport {
	active := activeKind == storeKind || activeKind == ""
	report := StoreBackendReport{
		Kind:                 storeKind,
		Path:                 filepath.ToSlash(filepath.Join(".idiom", "comprehension", "graph.json")),
		Status:               "absent",
		Active:               active,
		CurrentSchemaVersion: jsonGraphSchemaVersion,
		Compatible:           true,
		Reason:               "JSON graph export is not present",
	}
	data, err := os.ReadFile(graphPath(root))
	if err != nil {
		if os.IsNotExist(err) {
			return report
		}
		report.Status = "unknown"
		report.Compatible = false
		report.Reason = err.Error()
		return report
	}
	var graph struct {
		SchemaVersion int    `json:"schema_version"`
		StoreKind     string `json:"store_kind"`
	}
	if err := json.Unmarshal(data, &graph); err != nil {
		report.Status = "invalid"
		report.Compatible = false
		report.Reason = fmt.Sprintf("decode graph.json: %v", err)
		return report
	}
	report.SchemaVersion = graph.SchemaVersion
	if graph.StoreKind != "" && graph.StoreKind != storeKind {
		report.Status = "incompatible"
		report.Compatible = false
		report.Reason = fmt.Sprintf("metadata store kind %q does not match %q", graph.StoreKind, storeKind)
		return report
	}
	switch {
	case graph.SchemaVersion <= 0:
		report.Status = "incompatible"
		report.Compatible = false
		report.Reason = "graph.json schema_version must be positive"
	case graph.SchemaVersion > jsonGraphSchemaVersion:
		report.Status = "incompatible"
		report.Compatible = false
		report.Reason = fmt.Sprintf("JSON schema version %d is newer than supported version %d", graph.SchemaVersion, jsonGraphSchemaVersion)
	default:
		if active {
			report.Status = "active"
			report.Reason = "JSON graph is active"
		} else {
			report.Status = "export"
			report.Reason = "JSON graph export is compatible"
		}
		report.Compatible = true
	}
	return report
}

func sqliteStorageBackend(root string, activeKind string) StoreBackendReport {
	report := StoreBackendReport{
		Kind:                 sqliteStoreKind,
		Path:                 filepath.ToSlash(filepath.Join(".idiom", "comprehension", "graph.sqlite")),
		MetadataPath:         filepath.ToSlash(filepath.Join(".idiom", "comprehension", "graph.sqlite.meta.json")),
		Status:               "absent",
		Active:               activeKind == sqliteStoreKind,
		CurrentSchemaVersion: sqliteGraphSchemaVersion,
		Compatible:           true,
		Reason:               "SQLite graph store is not present",
	}
	sqlitePath := sqliteGraphPath(root)
	if _, err := os.Stat(sqlitePath); err != nil {
		if os.IsNotExist(err) {
			return report
		}
		report.Status = "unknown"
		report.Compatible = false
		report.Reason = err.Error()
		return report
	}
	report.Status = "present"
	if !looksLikeSQLite(sqlitePath) {
		report.Status = "invalid"
		report.Compatible = false
		report.Reason = "graph.sqlite does not have a SQLite file header"
		return report
	}
	meta, err := readSQLiteMetadata(root)
	if err != nil {
		report.Status = "unversioned"
		report.Compatible = false
		report.Reason = err.Error()
		return report
	}
	report.SchemaVersion = meta.SchemaVersion
	if meta.StoreKind != "" && meta.StoreKind != sqliteStoreKind {
		report.Status = "incompatible"
		report.Compatible = false
		report.Reason = fmt.Sprintf("metadata store kind %q does not match %q", meta.StoreKind, sqliteStoreKind)
		return report
	}
	switch {
	case meta.SchemaVersion <= 0:
		report.Status = "incompatible"
		report.Compatible = false
		report.Reason = "metadata schema_version must be positive"
	case meta.SchemaVersion > sqliteGraphSchemaVersion:
		report.Status = "incompatible"
		report.Compatible = false
		report.Reason = fmt.Sprintf("sqlite schema version %d is newer than supported version %d", meta.SchemaVersion, sqliteGraphSchemaVersion)
	case meta.SchemaVersion < sqliteGraphSchemaVersion:
		report.Status = "migration_available"
		report.Compatible = true
		report.Reason = fmt.Sprintf("sqlite schema version %d can migrate to %d", meta.SchemaVersion, sqliteGraphSchemaVersion)
	default:
		if err := validateSQLiteGraphFile(sqlitePath); err != nil {
			report.Status = "invalid"
			report.Compatible = false
			report.Reason = err.Error()
			return report
		}
		if report.Active {
			report.Status = "active"
			report.Reason = "SQLite graph is active"
		} else {
			report.Status = "compatible"
			report.Reason = "SQLite graph is compatible"
		}
		report.Compatible = true
	}
	return report
}

func validateSQLiteGraphFile(path string) error {
	db, err := openSQLite(path)
	if err != nil {
		return err
	}
	defer db.Close()
	requiredTables := []string{"graph_metadata", "graph_nodes", "graph_edges", "graph_views"}
	for _, table := range requiredTables {
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count)
		if err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("graph.sqlite is missing required table %s", table)
		}
	}
	values, err := readSQLiteGraphMetadata(db)
	if err != nil {
		return err
	}
	if _, err := graphFromSQLiteMetadata(values); err != nil {
		return err
	}
	return nil
}

func saveSQLiteGraph(root string, graph Graph) error {
	if err := os.MkdirAll(filepath.Dir(sqliteGraphPath(root)), 0o755); err != nil {
		return err
	}
	if err := prepareSQLiteGraphForWrite(root); err != nil {
		return err
	}
	db, err := openSQLite(sqliteGraphPath(root))
	if err != nil {
		return err
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := createSQLiteSchema(tx); err != nil {
		return err
	}
	if err := replaceSQLiteGraph(tx, graph); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true

	metadata := SQLiteMetadata{
		StoreKind:     sqliteStoreKind,
		SchemaVersion: sqliteGraphSchemaVersion,
		SourceIndexID: graph.ID,
	}
	if existing, err := readSQLiteMetadata(root); err == nil {
		metadata.CreatedAt = existing.CreatedAt
	}
	return writeSQLiteMetadata(root, metadata)
}

func prepareSQLiteGraphForWrite(root string) error {
	report := sqliteStorageBackend(root, sqliteStoreKind)
	if report.Status == "absent" || report.Status == "active" || report.Status == "compatible" || report.Status == "migration_available" {
		return nil
	}
	for _, path := range []string{
		sqliteGraphPath(root),
		sqliteGraphPath(root) + "-wal",
		sqliteGraphPath(root) + "-shm",
		sqliteMetadataPath(root),
	} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("replace %s backend artifact %s: %w", report.Status, filepath.ToSlash(path), err)
		}
	}
	return nil
}

func loadSQLiteGraph(root string) (Graph, error) {
	report := sqliteStorageBackend(root, sqliteStoreKind)
	if report.Status == "absent" {
		return Graph{}, os.ErrNotExist
	}
	if !report.Compatible || report.SchemaVersion != sqliteGraphSchemaVersion {
		return Graph{}, fmt.Errorf("SQLite graph backend is %s: %s", report.Status, report.Reason)
	}
	db, err := openSQLite(sqliteGraphPath(root))
	if err != nil {
		return Graph{}, err
	}
	defer db.Close()

	values, err := readSQLiteGraphMetadata(db)
	if err != nil {
		return Graph{}, err
	}
	graph, err := graphFromSQLiteMetadata(values)
	if err != nil {
		return Graph{}, err
	}
	nodes, err := readSQLiteNodes(db)
	if err != nil {
		return Graph{}, err
	}
	edges, err := readSQLiteEdges(db)
	if err != nil {
		return Graph{}, err
	}
	graph.Nodes = nodes
	graph.Edges = edges
	return graph, nil
}

func openSQLite(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func createSQLiteSchema(tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS graph_metadata (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS graph_nodes (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			path TEXT,
			language TEXT,
			qualified_name TEXT,
			start_line INTEGER,
			end_line INTEGER,
			fingerprint TEXT,
			git_head TEXT,
			collector TEXT NOT NULL,
			confidence TEXT NOT NULL,
			properties_json TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS graph_edges (
			id TEXT PRIMARY KEY,
			source_id TEXT NOT NULL,
			target_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			collector TEXT NOT NULL,
			confidence TEXT NOT NULL,
			evidence_json TEXT,
			properties_json TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS graph_views (
			id TEXT PRIMARY KEY,
			root_node_id TEXT,
			resolution TEXT NOT NULL,
			query_json TEXT NOT NULL,
			output_json TEXT NOT NULL,
			fingerprint TEXT NOT NULL,
			stale INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS graph_nodes_path_idx ON graph_nodes(path)`,
		`CREATE INDEX IF NOT EXISTS graph_nodes_kind_path_idx ON graph_nodes(kind, path)`,
		`CREATE INDEX IF NOT EXISTS graph_nodes_qualified_name_idx ON graph_nodes(qualified_name)`,
		`CREATE INDEX IF NOT EXISTS graph_edges_source_kind_idx ON graph_edges(source_id, kind)`,
		`CREATE INDEX IF NOT EXISTS graph_edges_target_kind_idx ON graph_edges(target_id, kind)`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func replaceSQLiteGraph(tx *sql.Tx, graph Graph) error {
	// graph_views invalidation is the caller's responsibility — full
	// rebuilds use MarkAllViewsStale, scoped refresh uses MarkViewsStale
	// with the changed-path set so the cache survives partial updates.
	for _, statement := range []string{
		`DELETE FROM graph_edges`,
		`DELETE FROM graph_nodes`,
		`DELETE FROM graph_metadata`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	metadata := map[string]string{
		"schema_version":    strconv.Itoa(graph.SchemaVersion),
		"index_id":          graph.ID,
		"root":              graph.Root,
		"generated_at":      graph.GeneratedAt,
		"git_head":          graph.GitHead,
		"git_dirty":         strconv.FormatBool(graph.GitDirty),
		"store_kind":        sqliteStoreKind,
		"collector_version": graph.CollectorVersion,
	}
	if len(graph.CollectorVersions) > 0 {
		data, err := json.Marshal(graph.CollectorVersions)
		if err != nil {
			return err
		}
		metadata["collector_versions_json"] = string(data)
	}
	if len(graph.Languages) > 0 {
		data, err := json.Marshal(graph.Languages)
		if err != nil {
			return err
		}
		metadata["languages_json"] = string(data)
	}
	if len(graph.CollectorReports) > 0 {
		data, err := json.Marshal(graph.CollectorReports)
		if err != nil {
			return err
		}
		metadata["collector_reports_json"] = string(data)
	}
	for key, value := range metadata {
		if _, err := tx.Exec(`INSERT INTO graph_metadata(key, value) VALUES(?, ?)`, key, value); err != nil {
			return err
		}
	}

	nodeStmt, err := tx.Prepare(`INSERT INTO graph_nodes(
		id, kind, path, language, qualified_name, start_line, end_line, fingerprint,
		git_head, collector, confidence, properties_json, created_at, updated_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer nodeStmt.Close()
	for _, node := range graph.Nodes {
		propertiesJSON, err := optionalJSON(node.Properties)
		if err != nil {
			return fmt.Errorf("encode node %s properties: %w", node.ID, err)
		}
		if _, err := nodeStmt.Exec(
			node.ID,
			node.Kind,
			emptyStringAsNil(node.Path),
			emptyStringAsNil(node.Language),
			emptyStringAsNil(node.QualifiedName),
			zeroAsNil(node.StartLine),
			zeroAsNil(node.EndLine),
			emptyStringAsNil(node.Fingerprint),
			emptyStringAsNil(node.GitHead),
			node.Collector,
			node.Confidence,
			propertiesJSON,
			node.CreatedAt,
			node.UpdatedAt,
		); err != nil {
			return fmt.Errorf("insert node %s: %w", node.ID, err)
		}
	}

	edgeStmt, err := tx.Prepare(`INSERT INTO graph_edges(
		id, source_id, target_id, kind, collector, confidence, evidence_json,
		properties_json, created_at, updated_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer edgeStmt.Close()
	for _, edge := range graph.Edges {
		evidenceJSON, err := optionalJSON(edge.Evidence)
		if err != nil {
			return fmt.Errorf("encode edge %s evidence: %w", edge.ID, err)
		}
		propertiesJSON, err := optionalJSON(edge.Properties)
		if err != nil {
			return fmt.Errorf("encode edge %s properties: %w", edge.ID, err)
		}
		if _, err := edgeStmt.Exec(
			edge.ID,
			edge.SourceID,
			edge.TargetID,
			edge.Kind,
			edge.Collector,
			edge.Confidence,
			evidenceJSON,
			propertiesJSON,
			edge.CreatedAt,
			edge.UpdatedAt,
		); err != nil {
			return fmt.Errorf("insert edge %s: %w", edge.ID, err)
		}
	}
	return nil
}

func readSQLiteGraphMetadata(db *sql.DB) (map[string]string, error) {
	rows, err := db.Query(`SELECT key, value FROM graph_metadata`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("SQLite graph metadata is empty")
	}
	return values, nil
}

func graphFromSQLiteMetadata(values map[string]string) (Graph, error) {
	schemaVersion, err := strconv.Atoi(values["schema_version"])
	if err != nil {
		return Graph{}, fmt.Errorf("decode SQLite graph schema_version: %w", err)
	}
	if schemaVersion <= 0 {
		return Graph{}, fmt.Errorf("SQLite graph schema_version must be positive")
	}
	if schemaVersion > SchemaVersion {
		return Graph{}, fmt.Errorf("SQLite graph schema version %d is newer than supported version %d", schemaVersion, SchemaVersion)
	}
	if storeKind := strings.TrimSpace(values["store_kind"]); storeKind != "" && storeKind != sqliteStoreKind {
		return Graph{}, fmt.Errorf("SQLite graph store kind %q does not match %q", storeKind, sqliteStoreKind)
	}
	gitDirty, err := strconv.ParseBool(firstNonEmpty(values["git_dirty"], "false"))
	if err != nil {
		return Graph{}, fmt.Errorf("decode SQLite graph git_dirty: %w", err)
	}
	var languages []string
	if raw := strings.TrimSpace(values["languages_json"]); raw != "" {
		if err := json.Unmarshal([]byte(raw), &languages); err != nil {
			return Graph{}, fmt.Errorf("decode SQLite graph languages: %w", err)
		}
	}
	var collectorReports []CollectorReport
	if raw := strings.TrimSpace(values["collector_reports_json"]); raw != "" {
		if err := json.Unmarshal([]byte(raw), &collectorReports); err != nil {
			return Graph{}, fmt.Errorf("decode SQLite graph collector reports: %w", err)
		}
	}
	var collectorVersions map[string]string
	if raw := strings.TrimSpace(values["collector_versions_json"]); raw != "" {
		if err := json.Unmarshal([]byte(raw), &collectorVersions); err != nil {
			return Graph{}, fmt.Errorf("decode SQLite graph collector versions: %w", err)
		}
	}
	return Graph{
		SchemaVersion:     schemaVersion,
		ID:                values["index_id"],
		Root:              values["root"],
		GeneratedAt:       values["generated_at"],
		GitHead:           values["git_head"],
		GitDirty:          gitDirty,
		StoreKind:         sqliteStoreKind,
		CollectorVersion:  values["collector_version"],
		CollectorVersions: collectorVersions,
		Languages:         languages,
		CollectorReports:  collectorReports,
	}, nil
}

func readSQLiteNodes(db *sql.DB) ([]Node, error) {
	rows, err := db.Query(`SELECT
		id, kind, COALESCE(path, ''), COALESCE(language, ''),
		COALESCE(qualified_name, ''), COALESCE(start_line, 0), COALESCE(end_line, 0),
		COALESCE(fingerprint, ''), COALESCE(git_head, ''), collector, confidence,
		COALESCE(properties_json, ''), created_at, updated_at
		FROM graph_nodes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var nodes []Node
	for rows.Next() {
		var propertiesJSON string
		var node Node
		if err := rows.Scan(
			&node.ID,
			&node.Kind,
			&node.Path,
			&node.Language,
			&node.QualifiedName,
			&node.StartLine,
			&node.EndLine,
			&node.Fingerprint,
			&node.GitHead,
			&node.Collector,
			&node.Confidence,
			&propertiesJSON,
			&node.CreatedAt,
			&node.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if err := decodeOptionalJSONMap(propertiesJSON, &node.Properties); err != nil {
			return nil, fmt.Errorf("decode node %s properties: %w", node.ID, err)
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

func readSQLiteEdges(db *sql.DB) ([]Edge, error) {
	rows, err := db.Query(`SELECT
		id, source_id, target_id, kind, collector, confidence,
		COALESCE(evidence_json, ''), COALESCE(properties_json, ''), created_at, updated_at
		FROM graph_edges ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var edges []Edge
	for rows.Next() {
		var evidenceJSON, propertiesJSON string
		var edge Edge
		if err := rows.Scan(
			&edge.ID,
			&edge.SourceID,
			&edge.TargetID,
			&edge.Kind,
			&edge.Collector,
			&edge.Confidence,
			&evidenceJSON,
			&propertiesJSON,
			&edge.CreatedAt,
			&edge.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if err := decodeOptionalJSONMap(evidenceJSON, &edge.Evidence); err != nil {
			return nil, fmt.Errorf("decode edge %s evidence: %w", edge.ID, err)
		}
		if err := decodeOptionalJSONMap(propertiesJSON, &edge.Properties); err != nil {
			return nil, fmt.Errorf("decode edge %s properties: %w", edge.ID, err)
		}
		edges = append(edges, edge)
	}
	return edges, rows.Err()
}

func optionalJSON(value map[string]any) (any, error) {
	if len(value) == 0 {
		return nil, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return string(data), nil
}

func decodeOptionalJSONMap(raw string, target *map[string]any) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return err
	}
	*target = value
	return nil
}

func emptyStringAsNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func zeroAsNil(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

func writeSQLiteMetadata(root string, metadata SQLiteMetadata) error {
	if metadata.StoreKind == "" {
		metadata.StoreKind = sqliteStoreKind
	}
	if metadata.SchemaVersion == 0 {
		metadata.SchemaVersion = sqliteGraphSchemaVersion
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if metadata.CreatedAt == "" {
		metadata.CreatedAt = now
	}
	metadata.UpdatedAt = now
	path := sqliteMetadataPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func readSQLiteMetadata(root string) (SQLiteMetadata, error) {
	data, err := os.ReadFile(sqliteMetadataPath(root))
	if err != nil {
		if os.IsNotExist(err) {
			return SQLiteMetadata{}, fmt.Errorf("graph.sqlite is present but %s is missing", filepath.ToSlash(filepath.Join(".idiom", "comprehension", "graph.sqlite.meta.json")))
		}
		return SQLiteMetadata{}, err
	}
	var metadata SQLiteMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return SQLiteMetadata{}, fmt.Errorf("decode graph.sqlite metadata: %w", err)
	}
	return metadata, nil
}

func sqliteMetadataPath(root string) string {
	return filepath.Join(root, ".idiom", "comprehension", "graph.sqlite.meta.json")
}

func sqliteGraphPath(root string) string {
	return filepath.Join(root, ".idiom", "comprehension", "graph.sqlite")
}

func looksLikeSQLite(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil || len(data) < len("SQLite format 3\x00") {
		return false
	}
	return strings.HasPrefix(string(data), "SQLite format 3\x00")
}
