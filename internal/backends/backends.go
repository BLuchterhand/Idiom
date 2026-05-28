// Package backends owns the schema and loader for
// `.idiom/backends.json`, the project-side description of which local
// LLM backends a project's idiom sessions talk to. Phase 4 of the
// reusability migration introduced this so callers like
// `idiom lane plan-execute` can pull a `default_coder` alias from
// configuration rather than hardcoding the name. Phase 4b shipped
// the Go-native launcher (Launch / Stop / Status — see launcher.go)
// that drives `idiom backend start|stop|status` against the same
// schema; Phase 4c shipped the alias-routing proxy (BuildProxyConfig
// + NewProxyHandler + ServeProxy — see proxy.go) that drives
// `idiom backend proxy`, the Go port of Colony's mlx_alias_proxy.py.
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
	"slices"
	"strings"
)

// File is the on-disk representation of `.idiom/backends.json`. A
// project can declare multiple Profiles (e.g. "qwen-pair" with both
// reasoning and coder backends, "single-llama-cpp" for a single-model
// dev box) and pick one with `--profile` at lane invocation time.
type File struct {
	DefaultProfile string             `json:"default_profile,omitempty"`
	Profiles       map[string]Profile `json:"profiles"`
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
//
// Roles tags the model with the harness role(s) it should serve.
// Recognized values are RolePlanner, RoleExecutor, RoleClassifier.
// A single-model project can leave Roles empty and the lone-model
// fallback in AliasForRole will route every role to it; a multi-model
// project tags each Model with the roles it owns.
type Model struct {
	Alias           string   `json:"alias"`
	Binary          string   `json:"binary"`
	ModelPath       string   `json:"model_path"`
	Model           string   `json:"model,omitempty"`
	Port            int      `json:"port"`
	Host            string   `json:"host,omitempty"`
	Kind            string   `json:"kind,omitempty"`
	BaseURL         string   `json:"base_url,omitempty"`
	APIKeyEnv       string   `json:"api_key_env,omitempty"`
	ReasoningEffort string   `json:"reasoning_effort,omitempty"`
	Verbosity       string   `json:"verbosity,omitempty"`
	CacheSize       int      `json:"cache_size,omitempty"`
	CacheBytes      int64    `json:"cache_bytes,omitempty"`
	MaxTokens       int      `json:"max_tokens,omitempty"`
	ExtraArgs       []string `json:"extra_args,omitempty"`
	Roles           []string `json:"roles,omitempty"`
}

// Proxy describes the alias-routing layer that fronts the per-model
// backends. `Listen` is the single port a client (idiom) connects to;
// `DefaultAlias` is the alias chosen when a request doesn't name one.
// The Default<Role> fields let callers pin a specific alias to a
// harness role without relying on the role-tag heuristic; DefaultCoder
// is the legacy alias for DefaultExecutor and remains supported.
type Proxy struct {
	Listen            int    `json:"listen,omitempty"`
	DefaultAlias      string `json:"default_alias,omitempty"`
	DefaultCoder      string `json:"default_coder,omitempty"`
	DefaultPlanner    string `json:"default_planner,omitempty"`
	DefaultExecutor   string `json:"default_executor,omitempty"`
	DefaultClassifier string `json:"default_classifier,omitempty"`
}

// Recognized role names for Model.Roles and AliasForRole. The harness
// resolves planner / executor / classifier through this single set of
// constants so callers don't have to spell the strings themselves.
const (
	RolePlanner    = "planner"
	RoleExecutor   = "executor"
	RoleClassifier = "classifier"
)

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
	if name == "" {
		name = strings.TrimSpace(f.DefaultProfile)
	}
	if name == "" && len(f.Profiles) == 1 {
		for _, p := range f.Profiles {
			return p, true
		}
	}
	p, ok := f.Profiles[name]
	return p, ok
}

// ModelByAlias searches every declared profile for a model alias. It supports
// explicit CLI model overrides that intentionally target a non-default profile
// without changing the project's default_profile.
func (f File) ModelByAlias(alias string) (Model, bool) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return Model{}, false
	}
	if name := strings.TrimSpace(f.DefaultProfile); name != "" {
		if p, ok := f.Profiles[name]; ok {
			if m, ok := p.ModelByAlias(alias); ok {
				return m, true
			}
		}
	}
	names := make([]string, 0, len(f.Profiles))
	for name := range f.Profiles {
		if name != strings.TrimSpace(f.DefaultProfile) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		p := f.Profiles[name]
		if m, ok := p.ModelByAlias(alias); ok {
			return m, true
		}
	}
	return Model{}, false
}

// CoderAlias is the legacy "does this profile have a coder?" resolver.
// It deliberately does NOT use the lone-model fallback: a profile
// with a single encoder/llama model returns "" because the project
// hasn't declared a coder. Callers that want the more permissive
// "what alias should the executor role use?" semantic (including the
// lone-model fallback for single-model setups) should call
// AliasForRole(RoleExecutor) instead.
//
// Lookup order:
//  1. Profile.Proxy.DefaultCoder / DefaultExecutor (explicit)
//  2. The first Model whose Roles contains RoleExecutor
//  3. The first Model whose alias matches the coder heuristic
//  4. ""
func (p Profile) CoderAlias() string {
	if alias := strings.TrimSpace(p.proxyDefaultForRole(RoleExecutor)); alias != "" {
		return alias
	}
	for _, m := range p.Models {
		for _, r := range m.Roles {
			if strings.ToLower(strings.TrimSpace(r)) == RoleExecutor {
				if alias := strings.TrimSpace(m.Alias); alias != "" {
					return alias
				}
			}
		}
	}
	for _, m := range p.Models {
		if isHyphenBoundedToken(strings.ToLower(m.Alias), "coder") {
			return m.Alias
		}
	}
	return ""
}

// AliasForRole resolves the alias the harness should use for a given
// role. Lookup order, top to bottom:
//
//  1. The explicit Default<Role> field on Profile.Proxy (DefaultPlanner,
//     DefaultExecutor / DefaultCoder, DefaultClassifier).
//  2. The first Model whose Roles list contains the requested role.
//  3. The lone Model in the profile, when only one is declared. A
//     single-model project gets every role routed to that model
//     without having to spell out the Roles field.
//  4. (RoleExecutor only) The first Model whose alias contains "coder"
//     as a hyphen-bounded token. Preserves the legacy heuristic so
//     existing projects that haven't migrated to Roles keep working.
//  5. "" / ok=false — caller falls back to whichever cfg.json field
//     mirrors the role (cfg.LlamaServer, cfg.Implementer, or
//     cfg.RouteClassifier).
//
// The heuristic only fires for RoleExecutor because that's the only
// role with a recognizable name token; planner/classifier rely on
// explicit Default<Role> or role tags.
func (p Profile) AliasForRole(role string) (string, bool) {
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" {
		return "", false
	}
	if alias := strings.TrimSpace(p.proxyDefaultForRole(role)); alias != "" {
		return alias, true
	}
	for _, m := range p.Models {
		for _, r := range m.Roles {
			if strings.ToLower(strings.TrimSpace(r)) == role {
				if alias := strings.TrimSpace(m.Alias); alias != "" {
					return alias, true
				}
			}
		}
	}
	if len(p.Models) == 1 {
		if alias := strings.TrimSpace(p.Models[0].Alias); alias != "" {
			return alias, true
		}
	}
	if role == RoleExecutor {
		for _, m := range p.Models {
			if isHyphenBoundedToken(strings.ToLower(m.Alias), "coder") {
				return m.Alias, true
			}
		}
	}
	return "", false
}

// proxyDefaultForRole returns the Proxy.Default<Role> field value for a
// given role, including the legacy DefaultCoder alias for executor.
// Empty when the role has no explicit default.
func (p Profile) proxyDefaultForRole(role string) string {
	switch role {
	case RolePlanner:
		return p.Proxy.DefaultPlanner
	case RoleExecutor:
		if alias := strings.TrimSpace(p.Proxy.DefaultExecutor); alias != "" {
			return alias
		}
		return p.Proxy.DefaultCoder
	case RoleClassifier:
		return p.Proxy.DefaultClassifier
	}
	return ""
}

// ModelByAlias returns the Model entry with the given alias. Used by
// callers that resolve a role to an alias and then need the launcher
// metadata (host, port, binary) to wire up a llama.Client. Match is
// case-sensitive on the alias string, mirroring proxy routing.
func (p Profile) ModelByAlias(alias string) (Model, bool) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return Model{}, false
	}
	for _, m := range p.Models {
		if m.Alias == alias {
			return m, true
		}
	}
	return Model{}, false
}

// ResolveRoleFromProjectRoot loads <projectRoot>/.idiom/backends.json,
// selects the named profile (or the lone profile when name is empty),
// and resolves the alias + Model entry for the requested role. The
// returned ok is false when the file is missing, the profile is not
// found, or the role isn't declared. Errors only surface for malformed
// JSON — callers can treat (ok=false, err=nil) as "fall back to your
// own config".
//
// When the resolved alias points to an alias that has no matching
// Model entry (e.g., proxy.default_coder names an alias the proxy
// routes externally), the returned Model still carries the alias on
// its Alias field so callers can use it as the model identifier in
// chat-completion requests.
func ResolveRoleFromProjectRoot(projectRoot, profileName, role string) (alias string, model Model, ok bool, err error) {
	file, found, err := LoadFromProjectRoot(projectRoot)
	if err != nil {
		return "", Model{}, false, err
	}
	if !found {
		return "", Model{}, false, nil
	}
	prof, profOK := file.Profile(profileName)
	if !profOK {
		return "", Model{}, false, nil
	}
	alias, ok = prof.AliasForRole(role)
	if !ok {
		return "", Model{}, false, nil
	}
	model, _ = prof.ModelByAlias(alias)
	if strings.TrimSpace(model.Alias) == "" {
		model.Alias = alias
	}
	return alias, model, true, nil
}

// isHyphenBoundedToken reports whether `token` appears in `s` with
// non-alphanumeric (or string-end) boundaries on both sides. So
// "qwen3-coder-30b" matches "coder" but "encoder" does not. We
// don't pull in regexp for one match — manual scan is enough.
func isHyphenBoundedToken(s, token string) bool {
	for i := 0; i+len(token) <= len(s); i++ {
		if s[i:i+len(token)] != token {
			continue
		}
		// Left boundary check: start-of-string OR non-alnum.
		if i > 0 && isAlnum(s[i-1]) {
			continue
		}
		// Right boundary check: end-of-string OR non-alnum.
		if i+len(token) < len(s) && isAlnum(s[i+len(token)]) {
			continue
		}
		return true
	}
	return false
}

func isAlnum(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z':
		return true
	case b >= 'A' && b <= 'Z':
		return true
	case b >= '0' && b <= '9':
		return true
	}
	return false
}

// Save writes the File back to the given path as indented JSON.
func (f File) Save(path string) error {
	body, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("backends: marshal: %w", err)
	}
	body = append(body, '\n')
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fmt.Errorf("backends: write %q: %w", path, err)
	}
	return nil
}
