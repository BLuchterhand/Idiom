package backends

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleBackendsJSON = `{
  "profiles": {
    "qwen-pair": {
      "models": [
        {
          "alias": "qwen3.6-35b",
          "binary": "mlx_lm.server",
          "model_path": "~/models/qwen3.6-35b",
          "port": 47780,
          "cache_size": 4,
          "cache_bytes": 21474836480,
          "max_tokens": 8192,
          "extra_args": ["--temp", "1.0"]
        },
        {
          "alias": "qwen3-coder-30b",
          "binary": "mlx_lm.server",
          "model_path": "~/models/qwen3-coder-30b",
          "port": 47781,
          "max_tokens": 8192
        }
      ],
      "proxy": {
        "listen": 47680,
        "default_alias": "qwen3.6-35b",
        "default_coder": "qwen3-coder-30b"
      }
    }
  }
}`

// TestLoadParsesSampleBackends verifies the on-disk schema round-trips
// through Load: every documented field shows up in the parsed File so
// downstream callers can rely on the names. This is the canonical
// "happy path" smoke test for the schema.
func TestLoadParsesSampleBackends(t *testing.T) {
	path := writeTempJSON(t, sampleBackendsJSON)
	f, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	prof, ok := f.Profile("qwen-pair")
	if !ok {
		t.Fatalf("expected profile qwen-pair, got profiles=%v", f.Profiles)
	}
	if len(prof.Models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(prof.Models))
	}
	m35 := prof.Models[0]
	if m35.Alias != "qwen3.6-35b" || m35.Port != 47780 || m35.CacheSize != 4 || m35.CacheBytes != 21474836480 || m35.MaxTokens != 8192 {
		t.Fatalf("model[0] decoded incorrectly: %+v", m35)
	}
	if len(m35.ExtraArgs) != 2 || m35.ExtraArgs[0] != "--temp" || m35.ExtraArgs[1] != "1.0" {
		t.Fatalf("extra_args round-trip lost: %v", m35.ExtraArgs)
	}
	if prof.Proxy.Listen != 47680 || prof.Proxy.DefaultAlias != "qwen3.6-35b" || prof.Proxy.DefaultCoder != "qwen3-coder-30b" {
		t.Fatalf("proxy decoded incorrectly: %+v", prof.Proxy)
	}
}

// TestLoadOptionalMissingFileReturnsNoErr verifies the silent-fallback
// path: callers that can tolerate a missing backends.json (the lane
// wrapper, today) should see ok=false with no error so they fall back
// to their hardcoded defaults without spamming stderr.
func TestLoadOptionalMissingFileReturnsNoErr(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.json")
	f, ok, err := LoadOptional(missing)
	if err != nil {
		t.Fatalf("LoadOptional should not error on missing file: %v", err)
	}
	if ok {
		t.Fatalf("LoadOptional should report ok=false for missing file")
	}
	if len(f.Profiles) != 0 {
		t.Fatalf("zero-value file expected for missing path, got %+v", f)
	}
}

// TestLoadOptionalMalformedFileSurfacesError verifies that a file
// that exists but doesn't parse returns an error rather than the
// silent fallback. A malformed config is a project misconfiguration
// the user needs to fix; pretending the file isn't there would mask
// the bug behind a hardcoded default.
func TestLoadOptionalMalformedFileSurfacesError(t *testing.T) {
	path := writeTempJSON(t, `{"profiles": {`) // truncated JSON
	_, ok, err := LoadOptional(path)
	if err == nil {
		t.Fatalf("malformed JSON must surface an error, got nil")
	}
	if ok {
		t.Fatalf("ok must be false on parse error")
	}
	if !strings.Contains(err.Error(), "backends:") {
		t.Fatalf("error should be tagged with package name: %v", err)
	}
}

// TestLoadFromProjectRootResolvesCanonicalPath verifies the
// `<root>/.idiom/backends.json` lookup; callers that have a project
// root in hand should use this rather than constructing the path
// themselves so the canonical layout has one source of truth.
func TestLoadFromProjectRootResolvesCanonicalPath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir .idiom: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(sampleBackendsJSON), 0o644); err != nil {
		t.Fatalf("write backends.json: %v", err)
	}
	f, ok, err := LoadFromProjectRoot(root)
	if err != nil || !ok {
		t.Fatalf("LoadFromProjectRoot ok=%v err=%v", ok, err)
	}
	if _, found := f.Profile("qwen-pair"); !found {
		t.Fatalf("expected qwen-pair profile loaded from canonical path")
	}
}

// TestLoadFromProjectRootEmptyProjectRoot verifies the resolver
// short-circuits when handed an empty path, since lane.go calls
// this before knowing whether project_root is even set.
func TestLoadFromProjectRootEmptyProjectRoot(t *testing.T) {
	_, ok, err := LoadFromProjectRoot("")
	if err != nil {
		t.Fatalf("empty root must not error: %v", err)
	}
	if ok {
		t.Fatalf("empty root must report ok=false")
	}
}

// TestProfileSelectsLoneProfileWhenUnnamed verifies the convenience:
// projects with a single profile shouldn't have to name it. This is
// the common case (one project, one stack); naming becomes mandatory
// only when the file declares two or more profiles.
func TestProfileSelectsLoneProfileWhenUnnamed(t *testing.T) {
	f, err := Load(writeTempJSON(t, sampleBackendsJSON))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	prof, ok := f.Profile("")
	if !ok {
		t.Fatalf("expected lone profile to be returned with empty name")
	}
	if len(prof.Models) != 2 {
		t.Fatalf("expected qwen-pair models, got %d", len(prof.Models))
	}
}

// TestProfileEmptyNameAmbiguousWithMultipleProfiles verifies the
// disambiguation contract: when a project declares more than one
// profile, an empty name is ambiguous and Profile must return false.
// This pushes the caller to surface a clear "which profile?" error
// rather than silently picking one.
func TestProfileEmptyNameAmbiguousWithMultipleProfiles(t *testing.T) {
	body := `{"profiles": {
		"a": {"models": []},
		"b": {"models": []}
	}}`
	f, err := Load(writeTempJSON(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := f.Profile(""); ok {
		t.Fatalf("empty name with multiple profiles must be ambiguous (ok=false)")
	}
	if _, ok := f.Profile("a"); !ok {
		t.Fatalf("named lookup must still work")
	}
}

// TestCoderAliasPrefersExplicitDefaultCoder is the load-bearing
// resolver test for the lane wrapper's plan-execute auto-route. A
// project that explicitly sets proxy.default_coder gets exactly
// that alias regardless of how its model list looks.
func TestCoderAliasPrefersExplicitDefaultCoder(t *testing.T) {
	prof := Profile{
		Models: []Model{{Alias: "model-a"}, {Alias: "model-b-coder"}},
		Proxy:  Proxy{DefaultCoder: "model-a"},
	}
	if got := prof.CoderAlias(); got != "model-a" {
		t.Fatalf("explicit default_coder must win, got %q", got)
	}
}

// TestCoderAliasFallsBackToCoderHeuristic verifies that absent an
// explicit default_coder, the resolver picks the first model whose
// alias contains "coder". This covers projects that haven't set the
// new field but still have a coder backend in their model list —
// they should keep working without a config change.
func TestCoderAliasFallsBackToCoderHeuristic(t *testing.T) {
	prof := Profile{Models: []Model{
		{Alias: "qwen3.6-35b"},
		{Alias: "qwen3-coder-30b"},
	}}
	if got := prof.CoderAlias(); got != "qwen3-coder-30b" {
		t.Fatalf("heuristic must pick the coder alias, got %q", got)
	}
}

// TestCoderAliasReturnsEmptyWhenNoCoderPresent verifies the "no
// answer" path: a profile with no coder backend declared returns ""
// so the lane wrapper falls back to its hardcoded default rather
// than guessing.
func TestCoderAliasReturnsEmptyWhenNoCoderPresent(t *testing.T) {
	prof := Profile{Models: []Model{{Alias: "qwen3.6-35b"}, {Alias: "llama-7b"}}}
	if got := prof.CoderAlias(); got != "" {
		t.Fatalf("empty result expected when no coder exists, got %q", got)
	}
}

func writeTempJSON(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "backends.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}
