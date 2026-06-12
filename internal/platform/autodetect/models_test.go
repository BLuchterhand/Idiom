package autodetect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScanHuggingFaceCacheExtractsAlias verifies the HF cache scanner
// extracts the model name (post-second-`--`) as the alias and reports
// the full directory path. The scanner must skip non-model directories
// so a stray "datasets--*" entry doesn't pollute the model list.
func TestScanHuggingFaceCacheExtractsAlias(t *testing.T) {
	dir := t.TempDir()
	model := filepath.Join(dir, "models--Qwen--Qwen2.5-Coder-7B")

	if err := os.MkdirAll(filepath.Join(model, "snapshots", "deadbeef"), 0o755); err != nil {
		t.Fatalf("mkdir model: %v", err)
	}

	if err := os.WriteFile(filepath.Join(model, "snapshots", "deadbeef", "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("stage config: %v", err)
	}

	// Decoy: a dataset directory that must NOT be picked up.
	if err := os.MkdirAll(filepath.Join(dir, "datasets--squad"), 0o755); err != nil {
		t.Fatalf("mkdir dataset: %v", err)
	}

	got := scanHuggingFaceCache(dir)
	if len(got) != 1 {
		t.Fatalf("expected 1 model, got %d (%+v)", len(got), got)
	}

	m := got[0]
	if m.Alias != "qwen2.5-coder-7b" {
		t.Fatalf("alias normalization off: got %q, want %q", m.Alias, "qwen2.5-coder-7b")
	}

	if m.Source != "huggingface" {
		t.Fatalf("expected Source=huggingface, got %q", m.Source)
	}

	if !strings.HasSuffix(m.Path, "models--Qwen--Qwen2.5-Coder-7B") {
		t.Fatalf("unexpected Path: %q", m.Path)
	}

	if m.SizeBytes <= 0 {
		t.Fatalf("expected SizeBytes > 0 (config.json is 2 bytes), got %d", m.SizeBytes)
	}
}

// TestScanModelsDirHandlesGGUFAndDirs verifies the ~/models scanner
// treats both directory snapshots and single .gguf files as model
// candidates, with the alias derived from the basename (.gguf stripped).
func TestScanModelsDirHandlesGGUFAndDirs(t *testing.T) {
	dir := t.TempDir()
	// Directory model
	if err := os.MkdirAll(filepath.Join(dir, "qwen3-coder-30b"), 0o755); err != nil {
		t.Fatalf("mkdir snapshot model: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "qwen3-coder-30b", "weights.bin"), []byte("xxxx"), 0o644); err != nil {
		t.Fatalf("stage snapshot weights: %v", err)
	}

	// .gguf file model
	gguf := filepath.Join(dir, "Llama-3-8B-Instruct.Q5_K_M.gguf")
	if err := os.WriteFile(gguf, []byte("0123456789"), 0o644); err != nil {
		t.Fatalf("stage gguf: %v", err)
	}

	// Decoy: non-gguf file should be ignored.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("ignore me"), 0o644); err != nil {
		t.Fatalf("stage decoy: %v", err)
	}

	got := scanModelsDir(dir)
	aliases := map[string]Model{}

	for _, m := range got {
		aliases[m.Alias] = m
	}

	if _, ok := aliases["qwen3-coder-30b"]; !ok {
		t.Fatalf("expected directory snapshot to surface; got %+v", got)
	}

	if _, ok := aliases["llama-3-8b-instruct.q5_k_m"]; !ok {
		t.Fatalf("expected gguf to surface (alias-stripped); got %+v", got)
	}

	if _, ok := aliases["readme.md"]; ok {
		t.Fatalf("non-gguf decoy must NOT be surfaced as a model")
	}
}

// TestScanModelsDirSkipsDotEntries verifies dot-prefixed directories
// (.mlx-env Python venvs, .cache, .DS_Store-adjacent dirs) are
// skipped. Without this filter, the smoke test on a real machine
// surfaced .mlx-env as the alphabetically-first "model" in the
// doctor output.
func TestScanModelsDirSkipsDotEntries(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".mlx-env", "lib"), 0o755); err != nil {
		t.Fatalf("stage dotfile dir: %v", err)
	}

	// Dotfile dir even with model markers must NOT be surfaced — the
	// dot prefix is the signal that the user marked it as hidden /
	// non-content.
	if err := os.WriteFile(filepath.Join(dir, ".mlx-env", "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("stage dotfile config: %v", err)
	}

	// Real model alongside it must still surface.
	real := filepath.Join(dir, "qwen3-coder-30b")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatalf("mkdir real: %v", err)
	}

	if err := os.WriteFile(filepath.Join(real, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("stage real config: %v", err)
	}

	got := scanModelsDir(dir)
	for _, m := range got {
		if strings.HasPrefix(m.Alias, ".") {
			t.Fatalf("dotfile dir leaked into results: %+v", m)
		}
	}

	found := false

	for _, m := range got {
		if m.Alias == "qwen3-coder-30b" {
			found = true
		}
	}

	if !found {
		t.Fatalf("real model alongside dotfile must still surface; got %+v", got)
	}
}

// TestScanModelsDirSkipsNonModelDirs verifies directories without any
// recognized model marker (config.json, tokenizer.*, *.gguf,
// *.safetensors, *.bin, *.npz) are NOT treated as models. On a real
// machine ~/models often holds adjacent tooling — __pycache__, logs,
// system-prompts — that look like model snapshots from the outside.
func TestScanModelsDirSkipsNonModelDirs(t *testing.T) {
	dir := t.TempDir()
	// Three decoys: nothing model-shaped inside any of them.
	for _, name := range []string{"__pycache__", "logs", "system-prompts"} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("mkdir decoy %s: %v", name, err)
		}

		// Stage a non-marker file so the dir isn't empty.
		if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("not a model"), 0o644); err != nil {
			t.Fatalf("stage decoy file: %v", err)
		}
	}

	// One real model with safetensors weights.
	real := filepath.Join(dir, "qwen2.5-coder")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatalf("mkdir real: %v", err)
	}

	if err := os.WriteFile(filepath.Join(real, "model.safetensors"), []byte("xx"), 0o644); err != nil {
		t.Fatalf("stage real weights: %v", err)
	}

	got := scanModelsDir(dir)
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 model (the real one), got %d: %+v", len(got), got)
	}

	if got[0].Alias != "qwen2.5-coder" {
		t.Fatalf("expected qwen2.5-coder, got %q", got[0].Alias)
	}
}

// TestLooksLikeModelDirRecognizesMarkers exercises the marker
// recognition directly so a contributor adding a new format only has
// to update the table, not the scanner. Each marker file or suffix
// must qualify the directory on its own.
func TestLooksLikeModelDirRecognizesMarkers(t *testing.T) {
	cases := map[string]string{
		"config.json":       "hf-style",
		"tokenizer.json":    "hf-tokenizer",
		"tokenizer.model":   "sentencepiece",
		"mlx_metadata.json": "mlx-metadata",
		"weights.gguf":      "gguf-suffix",
		"model.safetensors": "safetensors-suffix",
		"pytorch_model.bin": "bin-suffix",
		"weights.npz":       "npz-suffix",
	}
	for filename, label := range cases {
		t.Run(label, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, filename), []byte("x"), 0o644); err != nil {
				t.Fatalf("stage %s: %v", filename, err)
			}

			if !looksLikeModelDir(dir) {
				t.Fatalf("expected %s to qualify dir as a model, but it didn't", filename)
			}
		})
	}

	// Negative case: directory with only adjacent tooling files
	// (script, README, log) must not qualify.
	t.Run("non-model-dir", func(t *testing.T) {
		dir := t.TempDir()
		for _, f := range []string{"README.md", "run.sh", "model.log"} {
			if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
				t.Fatalf("stage %s: %v", f, err)
			}
		}

		if looksLikeModelDir(dir) {
			t.Fatalf("expected non-model dir to NOT qualify, but it did")
		}
	})
}

// TestScanOllamaModelsListsTags verifies the Ollama scanner walks
// model/tag pairs and emits one Model per tag. The scanner must NOT
// crash when blobs/ alongside manifests/ contains binary data.
func TestScanOllamaModelsListsTags(t *testing.T) {
	root := t.TempDir()
	tagDir := filepath.Join(root, "qwen2.5-coder")

	if err := os.MkdirAll(tagDir, 0o755); err != nil {
		t.Fatalf("mkdir tagDir: %v", err)
	}

	for _, tag := range []string{"7b", "14b"} {
		if err := os.WriteFile(filepath.Join(tagDir, tag), []byte("{\"manifest\":true}"), 0o644); err != nil {
			t.Fatalf("stage tag %s: %v", tag, err)
		}
	}

	got := scanOllamaModels(root)
	aliases := map[string]bool{}

	for _, m := range got {
		aliases[m.Alias] = true
		if m.Source != "ollama" {
			t.Fatalf("expected Source=ollama, got %q", m.Source)
		}
	}

	if !aliases["qwen2.5-coder:7b"] || !aliases["qwen2.5-coder:14b"] {
		t.Fatalf("expected qwen2.5-coder:{7b,14b} in detected aliases; got %+v", aliases)
	}
}

// TestNormalizeAliasPreservesColon guards against aliases losing the
// Ollama-style "name:tag" form. The model_path passed to mlx_lm.server
// or llama-server uses this alias verbatim; stripping the colon would
// turn two distinct tags into the same alias.
func TestNormalizeAliasPreservesColon(t *testing.T) {
	cases := map[string]string{
		"Qwen2.5-Coder:7B": "qwen2.5-coder:7b",
		"My/Model Name":    "my-model-name",
		"  Trim This  ":    "trim-this",
		"already-clean-7b": "already-clean-7b",
	}
	for in, want := range cases {
		if got := normalizeAlias(in); got != want {
			t.Fatalf("normalizeAlias(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestDetectModelsHandlesMissingHomeGracefully verifies the top-level
// DetectModels function returns an empty (non-nil) slice when none of
// the cache dirs exist. Doctor must be able to call it on a fresh host
// without crashing.
func TestDetectModelsHandlesMissingHomeGracefully(t *testing.T) {
	// Point HOME at a tempdir where no cache structure exists.
	t.Setenv("HOME", t.TempDir())

	got := DetectModels()
	if got == nil {
		t.Fatalf("expected non-nil slice from DetectModels, got nil")
	}

	if len(got) != 0 {
		t.Fatalf("expected zero models in empty home, got %+v", got)
	}
}
