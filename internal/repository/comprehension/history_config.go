package comprehension

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const (
	// defaultSimilarWeight and defaultCoChangeWeight are the channel's RRF
	// weights, chosen on the usage-eval v1 dev split only (AUDIT.md, G884).
	defaultSimilarWeight  = 1.0
	defaultCoChangeWeight = 1.0
)

// HistoryConfig is the optional `history` section of <root>/.idiom/config.json.
// It is separate from the localization section on purpose: it shapes ranking,
// never the index, so editing it must not invalidate an index cache.
type HistoryConfig struct {
	// Disabled turns the whole channel off (the baseline arm of an eval).
	Disabled bool `json:"disabled,omitempty"`
	// LedgerPath is a planning ledger, a Markdown file with one table row per
	// work item, relative to the root. Empty reads commit messages only.
	LedgerPath string `json:"ledger_path,omitempty"`
	// LedgerIDRegex captures a row id from a ledger line; empty uses
	// defaultLedgerRowRegex.
	LedgerIDRegex string `json:"ledger_id_regex,omitempty"`
	// AsOf (RFC 3339) cuts history at that instant when it is earlier than
	// the checked-out commit's own time. An eval sets it to a case's query
	// time so nothing written after the question is read.
	AsOf string `json:"as_of,omitempty"`
	// SimilarWeight and CoChangeWeight scale each list's RRF contribution;
	// nil means the default, 0 drops the list.
	SimilarWeight  *float64 `json:"similar_weight,omitempty"`
	CoChangeWeight *float64 `json:"cochange_weight,omitempty"`
}

type historyConfigFile struct {
	History *HistoryConfig `json:"history"`
}

func weightOrDefault(w *float64, def float64) float64 {
	if w == nil {
		return def
	}

	return *w
}

func (c HistoryConfig) similarWeight() float64 {
	return weightOrDefault(c.SimilarWeight, defaultSimilarWeight)
}
func (c HistoryConfig) coChangeWeight() float64 {
	return weightOrDefault(c.CoChangeWeight, defaultCoChangeWeight)
}

// asOfUnix is the explicit cut in Unix seconds; ok is false when unset or
// unparsable (an unparsable cut is ignored: the commit's own time still cuts).
func (c HistoryConfig) asOfUnix() (int64, bool) {
	if c.AsOf == "" {
		return 0, false
	}

	t, err := time.Parse(time.RFC3339, c.AsOf)
	if err != nil {
		return 0, false
	}

	return t.Unix(), true
}

// LoadHistoryConfig reads the `history` section; a missing file or section
// yields the zero config (channel on, commit messages only).
func LoadHistoryConfig(root string) (HistoryConfig, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(localizationConfigRelPath)))
	if errors.Is(err, os.ErrNotExist) {
		return HistoryConfig{}, nil
	}

	if err != nil {
		return HistoryConfig{}, err
	}

	var file historyConfigFile
	if err := json.Unmarshal(data, &file); err != nil || file.History == nil {
		return HistoryConfig{}, nil //nolint:nilerr // a malformed file is reported by the localization loader
	}

	return *file.History, nil
}

// historyFingerprint keys the pack cache on everything the channel reads that
// the index does not cover: the history section, the ledger's bytes and the
// checked-out commit. A new commit or ledger edit therefore misses the cache.
func historyFingerprint(root string) string {
	cfg, err := LoadHistoryConfig(root)
	if err != nil || cfg.Disabled {
		return ""
	}

	h := sha256.New()
	data, _ := json.Marshal(cfg)
	h.Write(data)

	if cfg.LedgerPath != "" {
		ledger, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(cfg.LedgerPath)))
		h.Write(ledger)
	}

	h.Write([]byte(gitHeadHash(root)))

	return hex.EncodeToString(h.Sum(nil))[:16]
}
