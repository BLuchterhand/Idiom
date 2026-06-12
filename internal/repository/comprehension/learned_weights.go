package comprehension

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	LearnedWeightBaseline   = 1.0
	LearnedWeightDecay      = 0.95
	LearnedWeightMax        = 3.0
	LearnedWeightMin        = 0.30
	LearnedWeightBoost      = 0.15
	LearnedWeightPenalty    = 0.10
	learnedWeightsFileName  = "weights.json"
	learnedWeightsDirSuffix = "ranker"
)

func learnedWeightsPath(root string) string {
	return filepath.Join(root, ".idiom", learnedWeightsDirSuffix, learnedWeightsFileName)
}

// LoadLearnedWeights reads the persisted per-file relevance multipliers from
// .idiom/ranker/weights.json. Returns an empty map if the file is missing or
// malformed; the caller treats every absent entry as the baseline 1.0.
func LoadLearnedWeights(root string) (map[string]float64, error) {
	path := learnedWeightsPath(root)

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]float64{}, nil
		}

		return nil, err
	}

	var parsed map[string]float64

	if err := json.Unmarshal(raw, &parsed); err != nil {
		return map[string]float64{}, nil
	}

	out := make(map[string]float64, len(parsed))
	for k, v := range parsed {
		key := normalizeWeightKey(k)
		if key == "" {
			continue
		}

		out[key] = clampLearnedWeight(v)
	}

	return out, nil
}

// SaveLearnedWeights writes the map back to disk, dropping no-op entries
// (those that have decayed back to within 1e-9 of the baseline). The write
// is atomic via a temp file + rename so concurrent readers never see a
// partially-written file.
func SaveLearnedWeights(root string, weights map[string]float64) error {
	cleaned := make(map[string]float64, len(weights))
	for k, v := range weights {
		key := normalizeWeightKey(k)
		if key == "" {
			continue
		}

		clamped := clampLearnedWeight(v)
		if absDiff(clamped, LearnedWeightBaseline) < 1e-9 {
			continue
		}

		cleaned[key] = clamped
	}

	path := learnedWeightsPath(root)
	if len(cleaned) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}

		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	keys := make([]string, 0, len(cleaned))
	for k := range cleaned {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	sorted := make(map[string]float64, len(cleaned))
	for _, k := range keys {
		sorted[k] = cleaned[k]
	}

	encoded, err := json.MarshalIndent(sorted, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".weights-*.json.tmp")
	if err != nil {
		return err
	}

	tmpName := tmp.Name()
	if _, err := tmp.Write(append(encoded, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)

		return err
	}

	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}

	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}

	return nil
}

// UpdateLearnedWeightsOptions is the input shape for one tick of the
// learning loop. Boosted paths nudge upward, penalised paths nudge downward,
// and every existing entry decays toward baseline so old signals fade.
type UpdateLearnedWeightsOptions struct {
	Boost   []string
	Penalty []string
	// BoostAmount and PenaltyAmount are optional; zero values use the package
	// constants.
	BoostAmount   float64
	PenaltyAmount float64
}

// UpdateLearnedWeights applies one decay-then-nudge cycle and persists the
// result. Returns the new weights map.
func UpdateLearnedWeights(root string, opts UpdateLearnedWeightsOptions) (map[string]float64, error) {
	weights, err := LoadLearnedWeights(root)
	if err != nil {
		return nil, err
	}

	for k, v := range weights {
		weights[k] = clampLearnedWeight(LearnedWeightBaseline + (v-LearnedWeightBaseline)*LearnedWeightDecay)
	}

	boostAmount := opts.BoostAmount
	if boostAmount <= 0 {
		boostAmount = LearnedWeightBoost
	}

	penaltyAmount := opts.PenaltyAmount
	if penaltyAmount <= 0 {
		penaltyAmount = LearnedWeightPenalty
	}

	for _, p := range opts.Boost {
		key := normalizeWeightKey(p)
		if key == "" {
			continue
		}

		current := weights[key]
		if current == 0 {
			current = LearnedWeightBaseline
		}

		weights[key] = clampLearnedWeight(current + boostAmount)
	}

	for _, p := range opts.Penalty {
		key := normalizeWeightKey(p)
		if key == "" {
			continue
		}

		current := weights[key]
		if current == 0 {
			current = LearnedWeightBaseline
		}

		weights[key] = clampLearnedWeight(current - penaltyAmount)
	}

	if err := SaveLearnedWeights(root, weights); err != nil {
		return nil, err
	}

	return LoadLearnedWeights(root)
}

func clampLearnedWeight(v float64) float64 {
	if v != v { // NaN guard
		return LearnedWeightBaseline
	}

	if v > LearnedWeightMax {
		return LearnedWeightMax
	}

	if v < LearnedWeightMin {
		return LearnedWeightMin
	}

	return v
}

func normalizeWeightKey(p string) string {
	cleaned := strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	for strings.HasPrefix(cleaned, "./") {
		cleaned = strings.TrimPrefix(cleaned, "./")
	}

	if cleaned == "" || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return ""
	}

	for _, part := range strings.Split(cleaned, "/") {
		if part == "" || part == "." || part == ".." {
			return ""
		}
	}

	return cleaned
}

func absDiff(a, b float64) float64 {
	if a > b {
		return a - b
	}

	return b - a
}
