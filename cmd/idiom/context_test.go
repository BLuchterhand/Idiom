package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite"
)

func TestRunMainRoutesContextCommands(t *testing.T) {
	root := t.TempDir()
	t.Setenv("IDIOM_TELEMETRY_DIR", filepath.Join(root, "telemetry"))
	writeContextCLIFile(t, filepath.Join(root, "go.mod"), "module example.com/contextcli\n\ngo 1.22\n")
	writeContextCLIFile(t, filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return helper() }\n\nfunc helper() string { return \"ok\" }\n")
	configPath := filepath.Join(root, "config.json")
	writeContextCLIFile(t, configPath, `{"workspace":{"root":".","cwd":"."}}`+"\n")

	var stdout, stderr bytes.Buffer
	code := runMain([]string{"context", "build", "--config", configPath, "--language", "go"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("context build failed code=%d stderr=%s", code, stderr.String())
	}
	var build struct {
		FilesIndexed int      `json:"files_indexed"`
		Languages    []string `json:"languages"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &build); err != nil {
		t.Fatalf("decode build output: %v\n%s", err, stdout.String())
	}
	if build.FilesIndexed != 1 || len(build.Languages) == 0 {
		t.Fatalf("expected build output to include indexed file count and languages, got %#v", build)
	}

	stdout.Reset()
	stderr.Reset()
	code = runMain([]string{"context", "status", "--config", configPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("context status failed code=%d stderr=%s", code, stderr.String())
	}
	var status struct {
		Indexed bool `json:"indexed"`
		Fresh   bool `json:"fresh"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("decode status output: %v\n%s", err, stdout.String())
	}
	if !status.Indexed || !status.Fresh {
		t.Fatalf("expected fresh status, got %#v", status)
	}

	writeContextCLIFile(t, filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return helper() }\n\nfunc helper() string { return \"changed\" }\n")
	stdout.Reset()
	stderr.Reset()
	code = runMain([]string{"context", "status", "--config", configPath, "--refresh"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("context status --refresh failed code=%d stderr=%s", code, stderr.String())
	}
	var refreshedStatus struct {
		Fresh       bool `json:"fresh"`
		AutoRefresh struct {
			Attempted  bool   `json:"attempted"`
			Reason     string `json:"reason"`
			FreshAfter bool   `json:"fresh_after"`
		} `json:"auto_refresh"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &refreshedStatus); err != nil {
		t.Fatalf("decode refreshed status output: %v\n%s", err, stdout.String())
	}
	if !refreshedStatus.Fresh || !refreshedStatus.AutoRefresh.Attempted || refreshedStatus.AutoRefresh.Reason != "small_stale_file_set" || !refreshedStatus.AutoRefresh.FreshAfter {
		t.Fatalf("expected context status --refresh to repair small stale set, got %#v", refreshedStatus)
	}

	stdout.Reset()
	stderr.Reset()
	code = runMain([]string{"context", "query", "--config", configPath, "--pattern", "Run", "--resolution", "symbol", "--include-evidence"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("context query failed code=%d stderr=%s", code, stderr.String())
	}
	var query struct {
		Nodes []struct {
			ID string `json:"id"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &query); err != nil {
		t.Fatalf("decode query output: %v\n%s", err, stdout.String())
	}
	if len(query.Nodes) == 0 || query.Nodes[0].ID == "" {
		t.Fatalf("expected query node handle, got %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = runMain([]string{"context", "zoom", "--config", configPath, "--id", query.Nodes[0].ID}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("context zoom failed code=%d stderr=%s", code, stderr.String())
	}
	var zoom struct {
		Node *struct {
			ID string `json:"id"`
		} `json:"node"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &zoom); err != nil {
		t.Fatalf("decode zoom output: %v\n%s", err, stdout.String())
	}
	if zoom.Node == nil || zoom.Node.ID != query.Nodes[0].ID {
		t.Fatalf("expected zoom output for handle %q, got %#v", query.Nodes[0].ID, zoom)
	}

	stdout.Reset()
	stderr.Reset()
	code = runMain([]string{"context", "pack", "--config", configPath, "--task", "Plan a change to Run", "--required-test", "go test ./..."}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("context pack failed code=%d stderr=%s", code, stderr.String())
	}
	var pack struct {
		ArtifactPath   string `json:"artifact_path"`
		CandidateFiles []struct {
			Path string `json:"path"`
		} `json:"candidate_files"`
		Tests []struct {
			Command string `json:"command"`
		} `json:"tests"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &pack); err != nil {
		t.Fatalf("decode pack output: %v\n%s", err, stdout.String())
	}
	if pack.ArtifactPath == "" || len(pack.CandidateFiles) == 0 || len(pack.Tests) == 0 {
		t.Fatalf("expected context pack artifact, candidates, and tests, got %#v", pack)
	}

	stdout.Reset()
	stderr.Reset()
	code = runMain([]string{"context", "impact", "--config", configPath, "--task", "Plan a change to Run", "--required-test", "go test ./..."}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("context impact failed code=%d stderr=%s", code, stderr.String())
	}
	var impact struct {
		ContextViewPath string `json:"context_view_path"`
		ImpactScope     []struct {
			Kind string `json:"kind"`
			Path string `json:"path"`
		} `json:"impact_scope"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &impact); err != nil {
		t.Fatalf("decode impact output: %v\n%s", err, stdout.String())
	}
	if impact.ContextViewPath == "" || len(impact.ImpactScope) == 0 {
		t.Fatalf("expected context impact view and ranked scope, got %#v", impact)
	}

	stdout.Reset()
	stderr.Reset()
	command := "printf '%s\\n' 'Traceback (most recent call last):' '  File \"" + filepath.ToSlash(filepath.Join(root, "pkg", "pkg.go")) + "\", line 3, in Run' 'AssertionError: bad' >&2; exit 1"
	code = runMain([]string{"context", "trace", "--config", configPath, "--command", command, "--language", "auto"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("context trace failed code=%d stderr=%s", code, stderr.String())
	}
	var trace struct {
		Success        bool   `json:"success"`
		ArtifactPath   string `json:"artifact_path"`
		FailingAnchors []struct {
			Path string `json:"path"`
			Line int    `json:"line"`
		} `json:"failing_anchors"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &trace); err != nil {
		t.Fatalf("decode trace output: %v\n%s", err, stdout.String())
	}
	if trace.Success || trace.ArtifactPath == "" || len(trace.FailingAnchors) == 0 {
		t.Fatalf("expected failing trace artifact and anchors, got %#v", trace)
	}
}

func TestContextStatusRefreshBuildsMissingIndexFromCLI(t *testing.T) {
	root := t.TempDir()
	t.Setenv("IDIOM_TELEMETRY_DIR", filepath.Join(root, "telemetry"))
	writeContextCLIFile(t, filepath.Join(root, "go.mod"), "module example.com/contextmissing\n\ngo 1.22\n")
	writeContextCLIFile(t, filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")
	configPath := filepath.Join(root, "config.json")
	writeContextCLIFile(t, configPath, `{"workspace":{"root":".","cwd":"."}}`+"\n")

	var stdout, stderr bytes.Buffer
	code := runMain([]string{"context", "status", "--config", configPath, "--refresh", "--language", "go"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("context status --refresh failed code=%d stderr=%s", code, stderr.String())
	}
	var status struct {
		Indexed     bool `json:"indexed"`
		Fresh       bool `json:"fresh"`
		AutoRefresh struct {
			Attempted    bool   `json:"attempted"`
			Scope        string `json:"scope"`
			Reason       string `json:"reason"`
			FilesIndexed int    `json:"files_indexed"`
			FreshAfter   bool   `json:"fresh_after"`
		} `json:"auto_refresh"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("decode status output: %v\n%s", err, stdout.String())
	}
	if !status.Indexed || !status.Fresh || !status.AutoRefresh.Attempted || status.AutoRefresh.Scope != "." || status.AutoRefresh.Reason != "missing_index" || status.AutoRefresh.FilesIndexed != 1 || !status.AutoRefresh.FreshAfter {
		t.Fatalf("expected missing-index status refresh to build root graph, got %#v", status)
	}
}

func TestContextStatusRefreshRebuildsCollectorMismatchFromCLI(t *testing.T) {
	root := t.TempDir()
	t.Setenv("IDIOM_TELEMETRY_DIR", filepath.Join(root, "telemetry"))
	writeContextCLIFile(t, filepath.Join(root, "go.mod"), "module example.com/contextcollectors\n\ngo 1.22\n")
	writeContextCLIFile(t, filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")
	configPath := filepath.Join(root, "config.json")
	writeContextCLIFile(t, configPath, `{"workspace":{"root":".","cwd":"."}}`+"\n")

	var stdout, stderr bytes.Buffer
	code := runMain([]string{"context", "build", "--config", configPath, "--language", "go"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("context build failed code=%d stderr=%s", code, stderr.String())
	}
	forceContextCLICollectorVersion(t, root, "stale-test-version")

	stdout.Reset()
	stderr.Reset()
	code = runMain([]string{"context", "status", "--config", configPath, "--refresh", "--language", "go"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("context status --refresh failed code=%d stderr=%s", code, stderr.String())
	}
	var status struct {
		Fresh                    bool `json:"fresh"`
		CollectorVersionMismatch bool `json:"collector_version_mismatch"`
		AutoRefresh              struct {
			Attempted  bool   `json:"attempted"`
			Scope      string `json:"scope"`
			Reason     string `json:"reason"`
			FreshAfter bool   `json:"fresh_after"`
		} `json:"auto_refresh"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("decode status output: %v\n%s", err, stdout.String())
	}
	if !status.Fresh || status.CollectorVersionMismatch || !status.AutoRefresh.Attempted || status.AutoRefresh.Scope != "." || status.AutoRefresh.Reason != "collector_version_mismatch" || !status.AutoRefresh.FreshAfter {
		t.Fatalf("expected collector mismatch status refresh to rebuild root graph, got %#v", status)
	}
}

func TestConcurrentContextBuildCommandsPreserveScopedRefreshes(t *testing.T) {
	root := t.TempDir()
	t.Setenv("IDIOM_TELEMETRY_DIR", filepath.Join(root, "telemetry"))
	writeContextCLIFile(t, filepath.Join(root, "go.mod"), "module example.com/contextconcurrent\n\ngo 1.22\n")
	writeContextCLIFile(t, filepath.Join(root, "pkg", "a.go"), "package pkg\n\nfunc OldA() string { return \"a\" }\n")
	writeContextCLIFile(t, filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc OldB() string { return \"b\" }\n")
	configPath := filepath.Join(root, "config.json")
	writeContextCLIFile(t, configPath, `{"workspace":{"root":".","cwd":"."}}`+"\n")

	var stdout, stderr bytes.Buffer
	code := runMain([]string{"context", "build", "--config", configPath, "--language", "go"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("context build failed code=%d stderr=%s", code, stderr.String())
	}
	writeContextCLIFile(t, filepath.Join(root, "pkg", "a.go"), "package pkg\n\nfunc NewA() string { return \"a2\" }\n")
	writeContextCLIFile(t, filepath.Join(root, "pkg", "b.go"), "package pkg\n\nfunc NewB() string { return \"b2\" }\n")

	var wg sync.WaitGroup
	errs := make(chan string, 2)
	for _, path := range []string{"pkg/a.go", "pkg/b.go"} {
		path := path
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out, errOut bytes.Buffer
			if code := runMain([]string{"context", "build", "--config", configPath, "--path", path, "--language", "go"}, &out, &errOut); code != 0 {
				errs <- errOut.String()
			}
		}()
	}
	wg.Wait()
	close(errs)
	for errText := range errs {
		if errText != "" {
			t.Fatalf("concurrent context build failed: %s", errText)
		}
	}

	stdout.Reset()
	stderr.Reset()
	code = runMain([]string{"context", "status", "--config", configPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("context status failed code=%d stderr=%s", code, stderr.String())
	}
	var status struct {
		Fresh      bool     `json:"fresh"`
		StaleFiles []string `json:"stale_files"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("decode status output: %v\n%s", err, stdout.String())
	}
	if !status.Fresh || len(status.StaleFiles) != 0 {
		t.Fatalf("expected concurrent scoped CLI builds to leave graph fresh, got %#v", status)
	}

	stdout.Reset()
	stderr.Reset()
	code = runMain([]string{"context", "query", "--config", configPath, "--query", "NewA NewB", "--resolution", "symbol", "--limit", "10"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("context query failed code=%d stderr=%s", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "NewA") || !strings.Contains(output, "NewB") {
		t.Fatalf("expected query output to include both refreshed symbols, got:\n%s", output)
	}
}

func TestContextCommandsWriteStructuredTelemetry(t *testing.T) {
	root := t.TempDir()
	telemetryDir := filepath.Join(root, "telemetry")
	t.Setenv("IDIOM_TELEMETRY_DIR", telemetryDir)
	writeContextCLIFile(t, filepath.Join(root, "go.mod"), "module example.com/contexttelemetry\n\ngo 1.22\n")
	writeContextCLIFile(t, filepath.Join(root, "pkg", "pkg.go"), "package pkg\n\nfunc Run() string { return \"ok\" }\n")
	configPath := filepath.Join(root, "config.json")
	writeContextCLIFile(t, configPath, `{"workspace":{"root":".","cwd":"."}}`+"\n")

	var stdout, stderr bytes.Buffer
	code := runMain([]string{"context", "build", "--config", configPath, "--language", "go"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("context build failed code=%d stderr=%s", code, stderr.String())
	}

	entries, err := os.ReadDir(telemetryDir)
	if err != nil {
		t.Fatalf("read telemetry dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one telemetry file, got %d", len(entries))
	}
	records := readMainTelemetryRecords(t, filepath.Join(telemetryDir, entries[0].Name()))
	var result map[string]any
	for _, record := range records {
		if record["type"] == "tool_result" && record["tool"] == "context_index" {
			result = record
			break
		}
	}
	if result == nil {
		t.Fatalf("missing context_index telemetry result: %#v", records)
	}
	if result["context_store_kind"] != "sqlite-graph-v1" || result["context_active_backend"] != "sqlite-graph-v1" || result["context_active_backend_status"] != "active" {
		t.Fatalf("context telemetry missing SQLite backend fields: %#v", result)
	}
	if result["context_files_indexed"].(float64) != 1 || result["context_symbols_indexed"].(float64) == 0 {
		t.Fatalf("context telemetry missing graph counts: %#v", result)
	}
}

func forceContextCLICollectorVersion(t *testing.T, root string, version string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(root, ".idiom", "comprehension", "graph.sqlite"))
	if err != nil {
		t.Fatalf("open sqlite graph: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE graph_metadata SET value = ? WHERE key = 'collector_version'`, version); err != nil {
		t.Fatalf("force collector version: %v", err)
	}
}

func writeContextCLIFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
