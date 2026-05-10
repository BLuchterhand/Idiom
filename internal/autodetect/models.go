package autodetect

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Model describes one local model file or directory autodetect
// discovered in a well-known cache location. The fields are oriented
// for `idiom init --auto` (Phase 10) — Alias slots into backends.json,
// SizeBytes informs default-pick heuristics, Source disambiguates when
// the same model name appears in multiple caches.
type Model struct {
	// Alias is the suggested alias for backends.json, normalized from
	// the on-disk name (lowercased, slashes/colons collapsed). Examples:
	// "qwen2.5-coder-7b", "llama-3-8b-instruct".
	Alias string
	// Path is the absolute filesystem path to the model directory or
	// file. Backends use it as the model_path in the generated profile.
	Path string
	// Source identifies which cache this model came from: "huggingface",
	// "ollama", "models-dir". Useful for doctor output and for picking
	// the right loader at backend-start time.
	Source string
	// SizeBytes is the on-disk size summed over the directory tree (or
	// the file size for single-file models). 0 if the size couldn't be
	// determined; callers treat 0 as "unknown" rather than "empty."
	SizeBytes int64
}

// DetectModels walks well-known cache locations and returns every
// candidate model. Sources scanned:
//   - ~/.cache/huggingface/hub (HF Transformers / mlx_lm cache layout)
//   - ~/models (the convention idiom's documentation uses)
//   - ~/.ollama/models/manifests/registry.ollama.ai/library
//
// Results are sorted by alias for determinism. An empty slice is
// returned (not nil) when no models are found — callers can range
// without nil-checks.
//
// Errors during the walk are silently skipped: the function aims to
// return whatever's discoverable, not crash on the first unreadable
// directory. Doctor surfaces missing-models as a remedy line; init
// surfaces it as "consider running `idiom models pull <alias>`."
func DetectModels() []Model {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return []Model{}
	}
	found := []Model{}
	found = append(found, scanHuggingFaceCache(filepath.Join(home, ".cache", "huggingface", "hub"))...)
	found = append(found, scanModelsDir(filepath.Join(home, "models"))...)
	found = append(found, scanOllamaModels(filepath.Join(home, ".ollama", "models", "manifests"))...)
	sort.Slice(found, func(i, j int) bool { return found[i].Alias < found[j].Alias })
	return found
}

// scanHuggingFaceCache reads ~/.cache/huggingface/hub looking for
// directories named "models--<org>--<name>". Each maps to one Model
// with the alias derived from <name> (the part after the second "--").
func scanHuggingFaceCache(dir string) []Model {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Model
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "models--") {
			continue
		}
		// HF format: models--<org>--<name> → alias is <name>.
		parts := strings.SplitN(strings.TrimPrefix(name, "models--"), "--", 2)
		if len(parts) != 2 {
			continue
		}
		alias := normalizeAlias(parts[1])
		path := filepath.Join(dir, name)
		out = append(out, Model{
			Alias:     alias,
			Path:      path,
			Source:    "huggingface",
			SizeBytes: dirSize(path),
		})
	}
	return out
}

// scanModelsDir reads ~/models for direct child entries — directories
// that look like a model snapshot or single .gguf files (llama.cpp's
// canonical format). The alias comes from the basename, with the
// .gguf suffix stripped when present.
//
// Filtering rules — keeps the doctor output noise-free on real
// machines where ~/models is shared with adjacent tooling:
//
//   - dot-prefixed entries are skipped (`.mlx-env` Python venvs,
//     `.DS_Store` files on macOS, `.cache` directories)
//   - directories must contain at least one recognized model marker
//     file (see looksLikeModelDir) — `__pycache__`, `logs`, generic
//     scratch dirs without weights are not surfaced as models
//   - non-directory files must end in `.gguf` to be considered
//     (single-file llama.cpp models); other file types are skipped
func scanModelsDir(dir string) []Model {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Model
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(dir, name)
		if e.IsDir() {
			if !looksLikeModelDir(path) {
				continue
			}
			out = append(out, Model{
				Alias:     normalizeAlias(name),
				Path:      path,
				Source:    "models-dir",
				SizeBytes: dirSize(path),
			})
			continue
		}
		if !strings.HasSuffix(name, ".gguf") {
			continue
		}
		info, err := e.Info()
		var size int64
		if err == nil {
			size = info.Size()
		}
		out = append(out, Model{
			Alias:     normalizeAlias(strings.TrimSuffix(name, ".gguf")),
			Path:      path,
			Source:    "models-dir",
			SizeBytes: size,
		})
	}
	return out
}

// modelMarkerFiles is the set of filenames whose presence in a
// directory makes us treat that directory as a model snapshot. Most
// HF-format models carry config.json + tokenizer.json + weights;
// MLX-converted models add their own metadata files; llama.cpp gguf
// files trigger via suffix match instead. This list is conservative —
// we'd rather under-report a true model than surface ~/.cache or
// ~/__pycache__ as a "model."
var modelMarkerFiles = map[string]bool{
	"config.json":       true,
	"tokenizer.json":    true,
	"tokenizer.model":   true,
	"mlx_metadata.json": true,
}

// modelMarkerSuffixes are file suffixes that indicate a directory
// holds model weights. Any one match qualifies the directory.
var modelMarkerSuffixes = []string{".gguf", ".safetensors", ".bin", ".npz"}

// looksLikeModelDir returns true when `dir` contains at least one
// model marker file or any file with a model-weight suffix. This is a
// shallow, top-level check — HF and MLX models put their markers at
// the directory root, so we don't recurse. False negatives (model
// dirs without any of these files) are accepted as the cost of
// keeping doctor output focused on real models.
func looksLikeModelDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if modelMarkerFiles[name] {
			return true
		}
		for _, suffix := range modelMarkerSuffixes {
			if strings.HasSuffix(name, suffix) {
				return true
			}
		}
	}
	return false
}

// scanOllamaModels reads ~/.ollama/models/manifests/registry.ollama.ai/library
// for installed Ollama models. The directory layout is
// <library>/<model-name>/<tag>; each <tag> file is a JSON manifest. We
// don't parse the manifest here — the existence of a tag file is enough
// to surface the model as detected. Size is best-effort: walking the
// blobs dir is expensive and not worth it for a doctor check.
func scanOllamaModels(dir string) []Model {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Model
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		modelDir := filepath.Join(dir, e.Name())
		tags, err := os.ReadDir(modelDir)
		if err != nil {
			continue
		}
		for _, tag := range tags {
			if tag.IsDir() {
				continue
			}
			out = append(out, Model{
				Alias:  normalizeAlias(e.Name() + ":" + tag.Name()),
				Path:   filepath.Join(modelDir, tag.Name()),
				Source: "ollama",
				// Ollama keeps real bytes in ../../../blobs; skipping
				// size here keeps the scan O(directory entries).
			})
		}
	}
	return out
}

// normalizeAlias lowercases and replaces characters that are awkward
// in a CLI alias (slashes, whitespace) with hyphens. Colons are
// preserved so Ollama's "name:tag" form survives — it's a meaningful
// identifier, not punctuation we want to strip.
func normalizeAlias(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, " ", "-")
	return s
}

// dirSize sums file sizes under root. Returns 0 on any error so
// callers can treat 0 as "unknown" without distinguishing "empty" or
// "permission denied" — the size is a heuristic, not load-bearing.
func dirSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}
