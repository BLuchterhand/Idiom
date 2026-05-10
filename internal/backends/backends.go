// Package backends owns the schema and loader for
// `.idiom/backends.json`, the project-side description of which local
// LLM backends a project's idiom sessions talk to. Phase 4 of the
// reusability migration introduced this so callers like
// `idiom lane plan-execute` can pull a `default_coder` alias from
// configuration rather than hardcoding the name; later phases will
// add `idiom backend start|stop|proxy` that wrap the existing
// Colony launcher scripts using the same schema.
//
// The file lives at <project-root>/.idiom/backends.json and is
// optional — callers that can't load it (missing, malformed) should
// fall back to their previous defaults.
package backends

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// File is the on-disk representation of `.idiom/backends.json`. A
// project can declare multiple Profiles (e.g. "qwen-pair" with both
// reasoning and coder backends, "single-llama-cpp" for a single-model
// dev box) and pick one with `--profile` at lane invocation time.
type File struct {
	Profiles map[string]Profile `json:"profiles"`
}

// Profile describes one named backend stack: a list of Model entries
// the launcher will start, plus an optional Proxy block that routes
// a single client port to multiple model backends by alias.
type Profile struct {
	Models []Model `json:"models"`
	Proxy  Proxy   `json:"proxy"`
}

// Model captures the launcher arguments for one backend. The fields
// mirror the args Colony's `start_local_qwen.sh` passes to
// `mlx_lm.server`; reusing the schema keeps Phase 4b's launcher port
// straightforward.
type Model struct {
	Alias      string   `json:"alias"`
	Binary     string   `json:"binary"`
	ModelPath  string   `json:"model_path"`
	Port       int      `json:"port"`
	CacheSize  int      `json:"cache_size,omitempty"`
	CacheBytes int64    `json:"cache_bytes,omitempty"`
	MaxTokens  int      `json:"max_tokens,omitempty"`
	ExtraArgs  []string `json:"extra_args,omitempty"`
}

// Proxy describes the alias-routing layer that fronts the per-model
// backends. `Listen` is the single port a client (idiom) connects to;
// `DefaultAlias` is the alias chosen when a request doesn't name one;
// `DefaultCoder` lets `idiom lane plan-execute` (and the `--coder`
// shortcut) pick the right alias without hardcoding.
type Proxy struct {
	Listen       int    `json:"listen,omitempty"`
	DefaultAlias string `json:"default_alias,omitempty"`
	DefaultCoder string `json:"default_coder,omitempty"`
}

// Load reads and parses a backends.json file. Returns the parsed
// File or an error describing the failure. Callers who can tolerate
// "no backends file" should call LoadOptional instead.
func Load(path string) (File, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("backends: read %q: %w", path, err)
	}
	var f File
	if err := json.Unmarshal(body, &f); err != nil {
		return File{}, fmt.Errorf("backends: parse %q: %w", path, err)
	}
	return f, nil
}

// LoadOptional is the caller-friendly version: when the file is
// missing it returns a zero-value File and ok=false with no error,
// so callers can fall back to defaults silently. Parse errors still
// return as errors so a malformed file isn't masked.
func LoadOptional(path string) (File, bool, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return File{}, false, nil
		}
		return File{}, false, fmt.Errorf("backends: read %q: %w", path, err)
	}
	var f File
	if err := json.Unmarshal(body, &f); err != nil {
		return File{}, false, fmt.Errorf("backends: parse %q: %w", path, err)
	}
	return f, true, nil
}

// LoadFromProjectRoot looks for `.idiom/backends.json` under
// projectRoot. Returns (file, true, nil) on success, (zero, false,
// nil) if the file is absent, and (zero, false, err) on parse error.
func LoadFromProjectRoot(projectRoot string) (File, bool, error) {
	if strings.TrimSpace(projectRoot) == "" {
		return File{}, false, nil
	}
	return LoadOptional(filepath.Join(projectRoot, ".idiom", "backends.json"))
}

// Profile retrieves a named profile or returns ok=false. The empty
// name selects the lone profile when the file has exactly one;
// projects with multiple profiles must name one explicitly.
func (f File) Profile(name string) (Profile, bool) {
	if len(f.Profiles) == 0 {
		return Profile{}, false
	}
	if name == "" && len(f.Profiles) == 1 {
		for _, p := range f.Profiles {
			return p, true
		}
	}
	p, ok := f.Profiles[name]
	return p, ok
}

// CoderAlias returns the alias the lane wrapper's plan-execute auto-
// route and `--coder` shortcut should use. Lookup order:
//  1. Profile.Proxy.DefaultCoder (explicit project choice)
//  2. The first model whose alias contains "coder" (heuristic)
//  3. "" — caller falls back to its hardcoded default
//
// Keeping this resolver inside the package means `idiom lane` doesn't
// need to know the heuristic, and adding new fields (e.g. a tagged
// `role: "coder"` field in Model) later won't ripple through callers.
func (p Profile) CoderAlias() string {
	if alias := strings.TrimSpace(p.Proxy.DefaultCoder); alias != "" {
		return alias
	}
	for _, m := range p.Models {
		if strings.Contains(strings.ToLower(m.Alias), "coder") {
			return m.Alias
		}
	}
	return ""
}
