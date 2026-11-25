package comprehension

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// localizationConfigRelPath is the project config that carries the optional
// `localization` section. Other sections of the file belong to the app config
// and are ignored here.
const localizationConfigRelPath = ".idiom/config.json"

// LocalizationConfig holds every repository-specific input to localization
// (what to index beyond source code, which doc roots enrich query terms). All
// fields default to empty, and an empty config changes nothing: no collector
// version entry, no cache-key component, no extra nodes.
type LocalizationConfig struct {
	ContentGlobs    []string `json:"content_globs,omitempty"`
	ShaderGlobs     []string `json:"shader_globs,omitempty"`
	ExtraExtensions []string `json:"extra_extensions,omitempty"`
	KeyPatterns     []string `json:"key_patterns,omitempty"`
	KeyMinLen       int      `json:"key_min_len,omitempty"`
	KeyEdges        bool     `json:"key_edges,omitempty"`
	DocRoots        []string `json:"doc_roots,omitempty"`
}

type localizationConfigFile struct {
	Localization *LocalizationConfig `json:"localization"`
}

// LoadLocalizationConfig reads the `localization` section of
// <root>/.idiom/config.json. A missing file or section yields the zero
// config. A malformed file yields the zero config plus the error, and
// LocalizationConfigFingerprint still changes with the file bytes so an edit
// cannot leave a stale index looking fresh.
func LoadLocalizationConfig(root string) (LocalizationConfig, error) {
	cfg, _, err := readLocalizationConfig(root)

	return cfg, err
}

// LocalizationConfigFingerprint is the short hash of the canonicalized
// localization section: empty for the zero config, so repositories without a
// section keep their existing collector version and cache keys. It is folded
// into the collector fingerprint, the freshness verdict (through
// StatusReport.CollectorVersionMismatch) and the pack cache key.
func LocalizationConfigFingerprint(root string) string {
	cfg, raw, err := readLocalizationConfig(root)
	if err != nil {
		return "invalid-" + shortSHA256Hex(raw)
	}

	return cfg.Fingerprint()
}

// Fingerprint hashes the canonical form; empty for the zero config.
func (c LocalizationConfig) Fingerprint() string {
	c = c.canonical()
	if c.isZero() {
		return ""
	}

	data, _ := json.Marshal(c)

	return shortSHA256Hex(append([]byte("localization_config\x00"), data...))
}

func (c LocalizationConfig) isZero() bool {
	return len(c.ContentGlobs) == 0 && len(c.ShaderGlobs) == 0 && len(c.ExtraExtensions) == 0 &&
		len(c.KeyPatterns) == 0 && c.KeyMinLen == 0 && !c.KeyEdges && len(c.DocRoots) == 0
}

// canonical sorts and deduplicates every list so ordering and whitespace
// edits that do not change meaning do not invalidate the index.
func (c LocalizationConfig) canonical() LocalizationConfig {
	c.ContentGlobs = canonicalStringSlice(c.ContentGlobs)
	c.ShaderGlobs = canonicalStringSlice(c.ShaderGlobs)
	c.ExtraExtensions = canonicalStringSlice(c.ExtraExtensions)
	c.KeyPatterns = canonicalStringSlice(c.KeyPatterns)
	c.DocRoots = canonicalStringSlice(c.DocRoots)

	return c
}

func readLocalizationConfig(root string) (LocalizationConfig, []byte, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(localizationConfigRelPath)))
	if errors.Is(err, os.ErrNotExist) {
		return LocalizationConfig{}, nil, nil
	}

	if err != nil {
		return LocalizationConfig{}, []byte(err.Error()), err
	}

	var file localizationConfigFile
	if err := json.Unmarshal(data, &file); err != nil {
		return LocalizationConfig{}, data, err
	}

	if file.Localization == nil {
		return LocalizationConfig{}, nil, nil
	}

	return file.Localization.canonical(), nil, nil
}
