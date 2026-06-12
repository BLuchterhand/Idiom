package projectartifacts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsureGeneratesExpectedArtifacts(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, filepath.Join(root, "go.mod"), "module example.com/demo\n\ngo 1.23.0\n")
	writeProjectFile(t, filepath.Join(root, "README.md"), "# Demo\n\n## API\n\nAlways verify focused packages.\n")
	writeProjectFile(t, filepath.Join(root, "AGENTS.md"), "Prefer semantic tools.\nNever guess paths.\n")
	writeProjectFile(t, filepath.Join(root, "internal", "app", "app.go"), "package app\n\nfunc Run() {}\n")
	writeProjectFile(t, filepath.Join(root, "cmd", "demo", "main.go"), "package main\n\nfunc main() {}\n")

	contextPath, err := Ensure(root)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	if got, want := contextPath, filepath.Join(root, filepath.FromSlash(contextRel)); got != want {
		t.Fatalf("expected context path %q, got %q", want, got)
	}

	for _, rel := range []string{
		discoveryDir + "/manifest.json",
		discoveryDir + "/modules.jsonl",
		discoveryDir + "/glossary.json",
		discoveryDir + "/workflows.json",
		discoveryDir + "/decisions.jsonl",
		discoveryDir + "/impact/root.json",
		contextRel,
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected artifact %s: %v", rel, err)
		}
	}

	data, err := os.ReadFile(contextPath)
	if err != nil {
		t.Fatalf("ReadFile context: %v", err)
	}

	text := string(data)
	for _, want := range []string{
		"Generated Project Context",
		"Prompt Files",
		"Active Languages",
		"Module Map",
		"Workflow Recipes",
		"Decision Memory",
		"`cmd`",
		"`internal`",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected context to include %q, got:\n%s", want, text)
		}
	}
}

func TestEnsureIgnoresGeneratedArtifactsInFingerprint(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, filepath.Join(root, "README.md"), "# Demo\n\nAlways verify.\n")

	contextPath, err := Ensure(root)
	if err != nil {
		t.Fatalf("Ensure first: %v", err)
	}

	info1, err := os.Stat(contextPath)
	if err != nil {
		t.Fatalf("Stat first context: %v", err)
	}

	time.Sleep(20 * time.Millisecond)

	contextPath, err = Ensure(root)
	if err != nil {
		t.Fatalf("Ensure second: %v", err)
	}

	info2, err := os.Stat(contextPath)
	if err != nil {
		t.Fatalf("Stat second context: %v", err)
	}

	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Fatalf("expected context mtime to stay stable when only .idiom exists; got %v then %v", info1.ModTime(), info2.ModTime())
	}
}

func TestEnsureRegeneratesWhenProjectSourceChanges(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "README.md")
	writeProjectFile(t, sourcePath, "# Demo\n\nAlways verify.\n")

	contextPath, err := Ensure(root)
	if err != nil {
		t.Fatalf("Ensure first: %v", err)
	}

	data1, err := os.ReadFile(contextPath)
	if err != nil {
		t.Fatalf("ReadFile first context: %v", err)
	}

	manifest1 := readManifest(t, filepath.Join(root, filepath.FromSlash(discoveryDir), "manifest.json"))

	time.Sleep(20 * time.Millisecond)
	writeProjectFile(t, sourcePath, "# Demo\n\nAlways verify.\n\n## ADR\n\nNever skip focused verification.\n")

	contextPath, err = Ensure(root)
	if err != nil {
		t.Fatalf("Ensure second: %v", err)
	}

	data2, err := os.ReadFile(contextPath)
	if err != nil {
		t.Fatalf("ReadFile second context: %v", err)
	}

	manifest2 := readManifest(t, filepath.Join(root, filepath.FromSlash(discoveryDir), "manifest.json"))

	if string(data1) == string(data2) {
		t.Fatalf("expected generated context to change after source edit, got identical output:\n%s", string(data2))
	}

	if manifest1.Fingerprint == manifest2.Fingerprint {
		t.Fatalf("expected fingerprint to change after source edit, got %q", manifest2.Fingerprint)
	}

	if !strings.Contains(string(data2), "Never skip focused verification.") {
		t.Fatalf("expected regenerated context to include new decision text, got:\n%s", string(data2))
	}
}

func writeProjectFile(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", path, err)
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func readManifest(t *testing.T, path string) Manifest {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}

	var manifest Manifest

	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("Unmarshal manifest %s: %v", path, err)
	}

	return manifest
}
