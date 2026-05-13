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

// TestCoderAliasHeuristicSkipsEncoderAndDecoder is the regression
// guard for the Phase 4/5 review tightening: a loose substring
// match would pick "encoder" or "decoder" as a coder, which is
// silently wrong. The hyphen-bounded variant must reject those.
func TestCoderAliasHeuristicSkipsEncoderAndDecoder(t *testing.T) {
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
			models := make([]Model, len(tc.aliases))
			for i, a := range tc.aliases {
				models[i] = Model{Alias: a}
			}
			prof := Profile{Models: models}
			if got := prof.CoderAlias(); got != tc.want {
				t.Fatalf("CoderAlias for %v = %q, want %q", tc.aliases, got, tc.want)
			}
		})
	}
}

// TestIsHyphenBoundedTokenMatchesEdgeCases is the unit-level guard
// for the boundary detector. Catches regressions in the boundary
// logic before they manifest as wrong CoderAlias resolutions.
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

// TestAliasForRoleLegacyDefaultCoderAliasesExecutor verifies the
// back-compat shim: a project that set proxy.default_coder before the
// roles refactor still resolves the executor role to that alias.
func TestAliasForRoleLegacyDefaultCoderAliasesExecutor(t *testing.T) {
	prof := Profile{
		Models: []Model{{Alias: "model-a"}, {Alias: "model-b"}},
		Proxy:  Proxy{DefaultCoder: "model-a"},
	}
	got, ok := prof.AliasForRole(RoleExecutor)
	if !ok || got != "model-a" {
		t.Fatalf("legacy DefaultCoder must alias executor; got (%q,%v)", got, ok)
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

// TestResolveRoleFromProjectRootHappyPath drives the full chain that
// callers use: load <root>/.idiom/backends.json, pick the lone profile,
// resolve the executor role, and surface the Model entry.
func TestResolveRoleFromProjectRootHappyPath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{"profiles": {"only": {"models": [
		{"alias": "exec-model", "binary": "mlx_lm.server", "port": 9001, "roles": ["executor"]},
		{"alias": "plan-model", "binary": "mlx_lm.server", "port": 9000, "roles": ["planner"]}
	]}}}`
	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	alias, model, ok, err := ResolveRoleFromProjectRoot(root, "", RoleExecutor)
	if err != nil || !ok {
		t.Fatalf("resolve: ok=%v err=%v", ok, err)
	}
	if alias != "exec-model" || model.Port != 9001 {
		t.Fatalf("resolved (%q, port=%d), want (exec-model, 9001)", alias, model.Port)
	}
}

// TestResolveRoleFromProjectRootAliasOnlyPropagatesAlias guards the
// proxy-only case: when proxy.default_executor names an alias that
// has no matching Model entry (the proxy routes externally), the
// returned Model still carries the alias on its Alias field so
// callers using it as the chat-completion model identifier route to
// the right backend instead of falling back to the inherited path.
func TestResolveRoleFromProjectRootAliasOnlyPropagatesAlias(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{"profiles": {"only": {
		"models": [{"alias": "planner-model", "port": 1000}],
		"proxy": {"default_executor": "remote-coder"}
	}}}`
	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	alias, model, ok, err := ResolveRoleFromProjectRoot(root, "", RoleExecutor)
	if err != nil || !ok {
		t.Fatalf("resolve: ok=%v err=%v", ok, err)
	}
	if alias != "remote-coder" {
		t.Fatalf("alias = %q, want remote-coder", alias)
	}
	if model.Alias != "remote-coder" {
		t.Fatalf("model.Alias must echo the resolved alias for proxy-only setups; got %q", model.Alias)
	}
}

// TestResolveRoleFromProjectRootMissingFile is the silent-fallback
// guarantee: callers that have no backends.json should see ok=false
// with no error so they keep using their config.json fields.
func TestResolveRoleFromProjectRootMissingFile(t *testing.T) {
	root := t.TempDir()
	alias, _, ok, err := ResolveRoleFromProjectRoot(root, "", RoleExecutor)
	if err != nil || ok || alias != "" {
		t.Fatalf("missing file: alias=%q ok=%v err=%v", alias, ok, err)
	}
}

// TestResolveRoleFromProjectRootProfileNotFound verifies that asking
// for a profile that doesn't exist returns ok=false (caller falls
// back) without surfacing it as a hard error.
func TestResolveRoleFromProjectRootProfileNotFound(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(sampleBackendsJSON), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	alias, _, ok, err := ResolveRoleFromProjectRoot(root, "missing-profile", RoleExecutor)
	if err != nil {
		t.Fatalf("missing profile must not error: %v", err)
	}
	if ok || alias != "" {
		t.Fatalf("missing profile must report ok=false; got alias=%q ok=%v", alias, ok)
	}
}

// TestResolveRoleFromProjectRootMalformedFileSurfacesError verifies
// that malformed JSON surfaces as a real error rather than the silent
// fallback — same contract as LoadOptional.
func TestResolveRoleFromProjectRootMalformedFileSurfacesError(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(`{"profiles": {`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, _, err := ResolveRoleFromProjectRoot(root, "", RoleExecutor); err == nil {
		t.Fatalf("malformed backends.json must surface an error")
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
