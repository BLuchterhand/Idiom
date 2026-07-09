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
        "default_executor": "qwen3-coder-30b"
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

	if prof.Proxy.Listen != 47680 || prof.Proxy.DefaultAlias != "qwen3.6-35b" || prof.Proxy.DefaultExecutor != "qwen3-coder-30b" {
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

func TestProfileUsesDefaultProfileWithMultipleProfiles(t *testing.T) {
	body := `{
		"default_profile": "b",
		"profiles": {
			"a": {"models": [{"alias": "alpha"}]},
			"b": {"models": [{"alias": "bravo"}]}
		}
	}`

	f, err := Load(writeTempJSON(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	prof, ok := f.Profile("")
	if !ok {
		t.Fatalf("expected empty profile lookup to use default_profile")
	}

	if len(prof.Models) != 1 || prof.Models[0].Alias != "bravo" {
		t.Fatalf("default profile resolved wrong model: %+v", prof.Models)
	}
}

func TestFileModelByAliasSearchesAllProfiles(t *testing.T) {
	body := `{"profiles": {
		"qwen-pair": {"models": [{"alias": "qwen3.6-35b", "port": 47780}]},
		"qwen-27b-pair": {"models": [{"alias": "qwen3.6-27b", "port": 47783}]}
	}}`

	f, err := Load(writeTempJSON(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	model, ok := f.ModelByAlias("qwen3.6-27b")
	if !ok {
		t.Fatalf("expected alias in non-default profile to resolve")
	}

	if model.Port != 47783 {
		t.Fatalf("port = %d, want 47783", model.Port)
	}
}

// TestAliasForRoleExecutorHeuristicSkipsEncoderAndDecoder is the regression
// guard for the Phase 4/5 review tightening: a loose substring
// match would pick "encoder" or "decoder" as a coder, which is
// silently wrong. The hyphen-bounded variant must reject those.
func TestAliasForRoleExecutorHeuristicSkipsEncoderAndDecoder(t *testing.T) {
	cases := []struct {
		name    string
		aliases []string
		want    string
	}{
		{"encoder must not match", []string{"qwen-encoder-7b"}, ""},
		{"decoder must not match", []string{"some-decoder-base"}, ""},
		{"prefix-coder matches", []string{"qwen3-coder-30b"}, "qwen3-coder-30b"},
		{"suffix-coder matches", []string{"deepseek-coder"}, "deepseek-coder"},
		{"standalone coder matches", []string{"coder"}, "coder"},
		{"coderbase does not match", []string{"coderbase-1b"}, ""},
		{"first match wins", []string{"qwen-35b", "deepseek-coder-7b", "code-only"}, "deepseek-coder-7b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			models := make([]Model, 0, len(tc.aliases)+1)
			models = append(models, Model{Alias: "base"})
			for _, a := range tc.aliases {
				models = append(models, Model{Alias: a})
			}

			prof := Profile{Models: models}
			got, ok := prof.AliasForRole(RoleExecutor)
			if tc.want == "" {
				if ok {
					t.Fatalf("AliasForRole executor for %v = (%q,true), want miss", tc.aliases, got)
				}
				return
			}

			if !ok || got != tc.want {
				t.Fatalf("AliasForRole executor for %v = (%q,%v), want (%q,true)", tc.aliases, got, ok, tc.want)
			}
		})
	}
}

// TestIsHyphenBoundedTokenMatchesEdgeCases is the unit-level guard
// for the boundary detector. Catches regressions in the boundary
// logic before they manifest as wrong executor-role resolutions.
func TestIsHyphenBoundedTokenMatchesEdgeCases(t *testing.T) {
	cases := []struct {
		s, token string
		want     bool
	}{
		{"coder", "coder", true},
		{"-coder-", "coder", true},
		{"x-coder-y", "coder", true},
		{"x-coder", "coder", true},
		{"coder-y", "coder", true},
		{"encoder", "coder", false},
		{"decoder", "coder", false},
		{"coderbase", "coder", false},
		{"basecoder", "coder", false},
		{"", "coder", false},
		{"co", "coder", false},
	}
	for _, c := range cases {
		got := isHyphenBoundedToken(c.s, c.token)
		if got != c.want {
			t.Fatalf("isHyphenBoundedToken(%q,%q) = %v, want %v", c.s, c.token, got, c.want)
		}
	}
}

// TestAliasForRolePrefersExplicitDefaults verifies the highest-priority
// resolution path: when Profile.Proxy declares Default<Role>, that
// alias wins over role tags and heuristics.
func TestAliasForRolePrefersExplicitDefaults(t *testing.T) {
	prof := Profile{
		Models: []Model{
			{Alias: "tagged-planner", Roles: []string{RolePlanner}},
			{Alias: "explicit-planner"},
		},
		Proxy: Proxy{
			DefaultPlanner:    "explicit-planner",
			DefaultExecutor:   "explicit-executor",
			DefaultClassifier: "explicit-classifier",
		},
	}
	cases := map[string]string{
		RolePlanner:    "explicit-planner",
		RoleExecutor:   "explicit-executor",
		RoleClassifier: "explicit-classifier",
	}

	for role, want := range cases {
		got, ok := prof.AliasForRole(role)
		if !ok || got != want {
			t.Fatalf("AliasForRole(%q) = (%q,%v), want (%q,true)", role, got, ok, want)
		}
	}
}

func TestResolveRoleAliasReportsSelectionSource(t *testing.T) {
	cases := []struct {
		name string
		prof Profile
		role string
		want RoleAliasResolution
	}{
		{
			name: "proxy default",
			prof: Profile{
				Models: []Model{{Alias: "tagged-executor", Roles: []string{RoleExecutor}}},
				Proxy:  Proxy{DefaultExecutor: "explicit-executor"},
			},
			role: RoleExecutor,
			want: RoleAliasResolution{Role: RoleExecutor, Alias: "explicit-executor", Source: RoleAliasSourceProxyDefault, OK: true},
		},
		{
			name: "role tag",
			prof: Profile{Models: []Model{{Alias: "planner", Roles: []string{RolePlanner}}}},
			role: RolePlanner,
			want: RoleAliasResolution{Role: RolePlanner, Alias: "planner", Source: RoleAliasSourceRoleTag, OK: true},
		},
		{
			name: "lone model",
			prof: Profile{Models: []Model{{Alias: "solo"}}},
			role: RoleClassifier,
			want: RoleAliasResolution{Role: RoleClassifier, Alias: "solo", Source: RoleAliasSourceLoneModel, OK: true},
		},
		{
			name: "executor coder heuristic",
			prof: Profile{Models: []Model{{Alias: "base"}, {Alias: "qwen3-coder-30b"}}},
			role: RoleExecutor,
			want: RoleAliasResolution{Role: RoleExecutor, Alias: "qwen3-coder-30b", Source: RoleAliasSourceExecutorCoderHeuristic, OK: true},
		},
		{
			name: "miss",
			prof: Profile{Models: []Model{{Alias: "base"}, {Alias: "helper"}}},
			role: RoleExecutor,
			want: RoleAliasResolution{Role: RoleExecutor},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.prof.ResolveRoleAlias(tc.role)
			if got != tc.want {
				t.Fatalf("ResolveRoleAlias(%q) = %#v, want %#v", tc.role, got, tc.want)
			}
		})
	}
}

// TestAliasForRoleFindsRoleTaggedModel verifies the second-priority
// path: when no explicit default is set, the resolver picks the first
// Model whose Roles list contains the requested role.
func TestAliasForRoleFindsRoleTaggedModel(t *testing.T) {
	prof := Profile{
		Models: []Model{
			{Alias: "alpha", Roles: []string{RoleClassifier}},
			{Alias: "beta", Roles: []string{RolePlanner, RoleExecutor}},
			{Alias: "gamma"},
		},
	}
	got, ok := prof.AliasForRole(RolePlanner)

	if !ok || got != "beta" {
		t.Fatalf("planner: got (%q,%v), want (beta,true)", got, ok)
	}

	got, ok = prof.AliasForRole(RoleExecutor)
	if !ok || got != "beta" {
		t.Fatalf("executor: got (%q,%v), want (beta,true)", got, ok)
	}

	got, ok = prof.AliasForRole(RoleClassifier)
	if !ok || got != "alpha" {
		t.Fatalf("classifier: got (%q,%v), want (alpha,true)", got, ok)
	}
}

// TestAliasForRoleLoneModelWearsAllRoles is the single-model story: a
// project with one model in its profile gets that model for every
// role without having to spell out Roles. This is the path that lets
// `idiom lane plan-execute` work on a one-model setup with a
// minimally-declared backends.json.
func TestAliasForRoleLoneModelWearsAllRoles(t *testing.T) {
	prof := Profile{Models: []Model{{Alias: "only-model"}}}
	for _, role := range []string{RolePlanner, RoleExecutor, RoleClassifier} {
		got, ok := prof.AliasForRole(role)
		if !ok || got != "only-model" {
			t.Fatalf("role %q on lone-model profile: got (%q,%v), want (only-model,true)", role, got, ok)
		}
	}
}

// TestAliasForRoleExecutorHeuristicMultiModel guards the legacy
// behavior: a multi-model profile with no role tags still picks the
// "coder"-named model for the executor role. This keeps existing
// projects (which haven't migrated to Roles tags) working unchanged.
func TestAliasForRoleExecutorHeuristicMultiModel(t *testing.T) {
	prof := Profile{Models: []Model{
		{Alias: "qwen3.6-35b"},
		{Alias: "qwen3-coder-30b"},
	}}
	got, ok := prof.AliasForRole(RoleExecutor)

	if !ok || got != "qwen3-coder-30b" {
		t.Fatalf("executor heuristic: got (%q,%v), want (qwen3-coder-30b,true)", got, ok)
	}

	// Planner and classifier do NOT have an alias-token heuristic, so
	// in a multi-model profile with no tags they return ok=false.
	if alias, ok := prof.AliasForRole(RolePlanner); ok {
		t.Fatalf("planner heuristic should not exist; got (%q,%v)", alias, ok)
	}

	if alias, ok := prof.AliasForRole(RoleClassifier); ok {
		t.Fatalf("classifier heuristic should not exist; got (%q,%v)", alias, ok)
	}
}

// TestAliasForRoleEmptyOnMiss verifies the no-match contract: when no
// explicit default, no role tag, multiple models (so no lone-model
// fallback), and no heuristic match — return ok=false so the caller
// falls back to its config.json field.
func TestAliasForRoleEmptyOnMiss(t *testing.T) {
	prof := Profile{Models: []Model{
		{Alias: "qwen3.6-35b"},
		{Alias: "llama-7b"},
	}}
	if alias, ok := prof.AliasForRole(RoleExecutor); ok {
		t.Fatalf("executor on no-coder multi-model profile should miss; got (%q,%v)", alias, ok)
	}

	if alias, ok := prof.AliasForRole(""); ok {
		t.Fatalf("empty role must report ok=false; got (%q,%v)", alias, ok)
	}
}

// TestModelByAliasFindsAndMissesCleanly verifies the metadata lookup
// helper that callers use after AliasForRole to fetch host/port/binary.
func TestModelByAliasFindsAndMissesCleanly(t *testing.T) {
	prof := Profile{Models: []Model{
		{Alias: "alpha", Port: 100},
		{Alias: "beta", Port: 200},
	}}
	got, ok := prof.ModelByAlias("beta")

	if !ok || got.Port != 200 {
		t.Fatalf("ModelByAlias(beta): got (%+v,%v)", got, ok)
	}

	if _, ok := prof.ModelByAlias("missing"); ok {
		t.Fatalf("ModelByAlias should miss for unknown alias")
	}

	if _, ok := prof.ModelByAlias("  "); ok {
		t.Fatalf("ModelByAlias should miss for blank alias")
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
