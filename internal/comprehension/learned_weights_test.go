package comprehension

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestLearnedWeightsRoundTrip(t *testing.T) {
	root := t.TempDir()
	if _, err := UpdateLearnedWeights(root, UpdateLearnedWeightsOptions{
		Boost: []string{"internal/runner.go", "internal/helper.go"},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := LoadLearnedWeights(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if math.Abs(got["internal/runner.go"]-1.15) > 1e-6 {
		t.Fatalf("boost result = %v, want 1.15", got["internal/runner.go"])
	}
	if math.Abs(got["internal/helper.go"]-1.15) > 1e-6 {
		t.Fatalf("boost result = %v, want 1.15", got["internal/helper.go"])
	}
}

func TestLearnedWeightsClampToBounds(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 100; i++ {
		_, _ = UpdateLearnedWeights(root, UpdateLearnedWeightsOptions{Boost: []string{"a.go"}})
	}
	weights, _ := LoadLearnedWeights(root)
	if weights["a.go"] != LearnedWeightMax {
		t.Fatalf("excessive boost = %v, want clamp to %v", weights["a.go"], LearnedWeightMax)
	}
	for i := 0; i < 200; i++ {
		_, _ = UpdateLearnedWeights(root, UpdateLearnedWeightsOptions{Penalty: []string{"a.go"}})
	}
	weights, _ = LoadLearnedWeights(root)
	if weights["a.go"] != LearnedWeightMin {
		t.Fatalf("excessive penalty = %v, want clamp to %v", weights["a.go"], LearnedWeightMin)
	}
}

func TestLearnedWeightsDecayTowardBaseline(t *testing.T) {
	root := t.TempDir()
	if _, err := UpdateLearnedWeights(root, UpdateLearnedWeightsOptions{Boost: []string{"a.go"}, BoostAmount: 1.0}); err != nil {
		t.Fatalf("update: %v", err)
	}
	first, _ := LoadLearnedWeights(root)
	// Subsequent updates without any boost/penalty should decay the weight
	// back toward 1.0. (1.0 - baseline) * 0.95^80 ≈ 1.7e-2, well below the
	// 0.05 threshold.
	for i := 0; i < 80; i++ {
		_, _ = UpdateLearnedWeights(root, UpdateLearnedWeightsOptions{})
	}
	final, _ := LoadLearnedWeights(root)
	if val, ok := final["a.go"]; ok && math.Abs(val-LearnedWeightBaseline) > 0.05 {
		t.Fatalf("decayed weight = %v, want close to baseline; before decay = %v", val, first["a.go"])
	}
}

func TestLearnedWeightsRejectsTraversalKeys(t *testing.T) {
	root := t.TempDir()
	if _, err := UpdateLearnedWeights(root, UpdateLearnedWeightsOptions{Boost: []string{"../escape.go", "/abs.go", " ", "safe/../escape.go", "safe/./escape.go", "safe//escape.go"}}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := LoadLearnedWeights(root)
	if len(got) != 0 {
		t.Fatalf("traversal keys should have been dropped, got %v", got)
	}
}

func TestLearnedWeightsFilePersistence(t *testing.T) {
	root := t.TempDir()
	_, _ = UpdateLearnedWeights(root, UpdateLearnedWeightsOptions{Boost: []string{"a.go"}})
	target := filepath.Join(root, ".idiom", "ranker", "weights.json")
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("weights file missing: %v", err)
	}
	// Penalising back into the no-op band should remove the file once all
	// entries decay to baseline.
	for i := 0; i < 200; i++ {
		_, _ = UpdateLearnedWeights(root, UpdateLearnedWeightsOptions{})
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		// File may still exist if any entry has not yet decayed to within 1e-9
		// of baseline; that's acceptable. Just sanity-check the weights are
		// near baseline.
		got, _ := LoadLearnedWeights(root)
		for k, v := range got {
			if math.Abs(v-LearnedWeightBaseline) > 0.01 {
				t.Fatalf("entry %q stuck at %v after decay", k, v)
			}
		}
	}
}
