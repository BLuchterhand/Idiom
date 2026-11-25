package eval

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
)

const (
	// LocalizationEvalBootstrapResamples is the bootstrap size of every gate.
	LocalizationEvalBootstrapResamples = 10000
	// LocalizationEvalMinSubset is the smallest subset a bootstrap gate may judge.
	LocalizationEvalMinSubset = 15
	// LocalizationEvalMaxWorstDrop is the largest allowed single-case drop.
	LocalizationEvalMaxWorstDrop = 0.25
	// LocalizationEvalDevConfidence and LocalizationEvalHoldoutConfidence are the
	// one-sided bootstrap confidence levels (hold-out Bonferroni 0.05/6).
	LocalizationEvalDevConfidence     = 0.95
	LocalizationEvalHoldoutConfidence = 0.992
	// LocalizationEvalSignTestAlpha is the exact sign-test level for small subsets.
	LocalizationEvalSignTestAlpha = 0.05

	localizationEvalBootstrapSeed = 0x1d10a
	localizationEvalTieEpsilon    = 1e-9
	localizationEvalHoldoutModulo = 3
)

// LocalizationEvalSplit assigns a merge (or commit) hash to "dev" or
// "holdout" by a hash of the id alone, so a case never changes side as the
// case set grows. About one third is hold-out.
func LocalizationEvalSplit(mergeID string) string {
	sum := sha256.Sum256([]byte("localization-eval-split-v1\x00" + mergeID))
	if binary.BigEndian.Uint64(sum[:8])%localizationEvalHoldoutModulo == 0 {
		return "holdout"
	}

	return "dev"
}

// LocalizationEvalDelta is one case's paired difference (after minus before).
type LocalizationEvalDelta struct {
	ID    string
	Delta float64
}

// LocalizationEvalGateSpec states one gate. The mean must reach MinMean and the
// bootstrap lower bound at Confidence must exceed LowerBoundFloor (0 for a
// "gain" phase, -0.01 for a "no regression" phase). Losses may not exceed
// wins, and no case may drop by more than MaxWorstDrop.
type LocalizationEvalGateSpec struct {
	Metric          string
	MinMean         float64
	Confidence      float64
	LowerBoundFloor float64
	// NoRegression marks a gate that only guards against loss: small subsets
	// use the "significantly worse" sign test and an unchanged subset passes.
	NoRegression bool
	MaxWorstDrop float64
}

// LocalizationEvalGateNoRegression marks corpora that may only guard against
// loss (the Idiom commit-message queries).
const LocalizationEvalGateNoRegression = "no_regression"

// LocalizationEvalNoRegressionFloor is the lower-bound floor of a
// no-regression gate.
const LocalizationEvalNoRegressionFloor = -0.01

// EffectiveLocalizationEvalGateSpec applies a corpus's gate kind: a
// no-regression corpus never gates a gain, whatever was asked for.
func EffectiveLocalizationEvalGateSpec(gate string, spec LocalizationEvalGateSpec) LocalizationEvalGateSpec {
	if gate == LocalizationEvalGateNoRegression {
		spec.NoRegression = true
	}

	if spec.NoRegression {
		spec.MinMean = LocalizationEvalNoRegressionFloor
		spec.LowerBoundFloor = LocalizationEvalNoRegressionFloor
	}

	return spec
}

// LocalizationEvalGateVerdict is the gate outcome with its evidence.
type LocalizationEvalGateVerdict struct {
	Metric     string   `json:"metric"`
	N          int      `json:"n"`
	Mean       float64  `json:"mean_delta"`
	LowerBound float64  `json:"lower_bound"`
	Confidence float64  `json:"confidence"`
	Wins       int      `json:"wins"`
	Losses     int      `json:"losses"`
	Ties       int      `json:"ties"`
	WorstDrop  float64  `json:"worst_drop"`
	WorstCase  string   `json:"worst_case,omitempty"`
	Method     string   `json:"method"`
	SignP      float64  `json:"sign_test_p,omitempty"`
	Pass       bool     `json:"pass"`
	Reasons    []string `json:"reasons,omitempty"`
}

// EvaluateLocalizationEvalGate applies spec to the paired deltas. Subsets under
// LocalizationEvalMinSubset use the exact one-sided sign test instead of the
// bootstrap, so a handful of cases never pass on a noisy bound.
func EvaluateLocalizationEvalGate(spec LocalizationEvalGateSpec, deltas []LocalizationEvalDelta) LocalizationEvalGateVerdict {
	maxDrop := spec.MaxWorstDrop
	if maxDrop == 0 {
		maxDrop = LocalizationEvalMaxWorstDrop
	}

	v := LocalizationEvalGateVerdict{Metric: spec.Metric, N: len(deltas), Confidence: spec.Confidence}
	if len(deltas) == 0 {
		v.Method = "none"
		v.Reasons = append(v.Reasons, "no cases")

		return v
	}

	values := make([]float64, len(deltas))
	for i, d := range deltas {
		values[i] = d.Delta

		switch {
		case d.Delta > localizationEvalTieEpsilon:
			v.Wins++
		case d.Delta < -localizationEvalTieEpsilon:
			v.Losses++
		default:
			v.Ties++
		}

		if -d.Delta > v.WorstDrop {
			v.WorstDrop = -d.Delta
			v.WorstCase = d.ID
		}
	}

	v.Mean = mean(values)
	v.Pass = true

	fail := func(format string, args ...any) {
		v.Pass = false

		v.Reasons = append(v.Reasons, fmt.Sprintf(format, args...))
	}

	if v.Mean < spec.MinMean {
		fail("mean delta %.4f below target %.4f", v.Mean, spec.MinMean)
	}

	// A small no-regression subset is judged by the sign test below; a bare
	// loss count would fail it on one unlucky case.
	smallNoRegression := spec.NoRegression && len(deltas) < LocalizationEvalMinSubset
	if v.Losses > v.Wins && !smallNoRegression {
		fail("losses %d exceed wins %d", v.Losses, v.Wins)
	}

	if v.WorstDrop > maxDrop {
		fail("case %s dropped %.3f, limit %.2f", v.WorstCase, v.WorstDrop, maxDrop)
	}

	if len(deltas) < LocalizationEvalMinSubset {
		v.Method = "sign_test"

		if spec.NoRegression {
			// No-regression: pass unless the subset is significantly worse.
			// An unchanged subset (no wins, no losses) is a pass.
			v.SignP = LocalizationEvalSignTestP(v.Losses, v.Wins)
			if v.Losses > 0 && v.SignP < LocalizationEvalSignTestAlpha {
				fail("sign test p=%.4f: significantly worse (%d losses, %d wins)", v.SignP, v.Losses, v.Wins)
			}

			return v
		}

		v.SignP = LocalizationEvalSignTestP(v.Wins, v.Losses)
		if v.SignP >= LocalizationEvalSignTestAlpha {
			fail("sign test p=%.4f at n=%d (<%d cases) not below %.2f", v.SignP, len(deltas), LocalizationEvalMinSubset, LocalizationEvalSignTestAlpha)
		}

		return v
	}

	v.Method = "bootstrap"
	v.LowerBound = LocalizationEvalBootstrapLowerBound(values, spec.Confidence)

	if v.LowerBound <= spec.LowerBoundFloor {
		fail("lower bound %.4f at %.1f%% not above %.4f", v.LowerBound, spec.Confidence*100, spec.LowerBoundFloor)
	}

	return v
}

// LocalizationEvalBootstrapLowerBound is the one-sided percentile bootstrap
// lower bound of the mean at the given confidence, with a fixed seed. Equal
// inputs give equal outputs.
func LocalizationEvalBootstrapLowerBound(values []float64, confidence float64) float64 {
	if len(values) == 0 {
		return math.NaN()
	}

	rng := rand.New(rand.NewPCG(localizationEvalBootstrapSeed, uint64(len(values))))
	means := make([]float64, LocalizationEvalBootstrapResamples)

	for i := range means {
		sum := 0.0
		for range values {
			sum += values[rng.IntN(len(values))]
		}

		means[i] = sum / float64(len(values))
	}

	slices.Sort(means)

	idx := int(math.Floor((1 - confidence) * float64(len(means))))

	return means[min(max(idx, 0), len(means)-1)]
}

// LocalizationEvalSignTestP is the exact one-sided sign test p-value
// P(X >= wins) for X ~ Binomial(wins+losses, 0.5); ties are dropped by the
// caller. With no decided cases it returns 1.
func LocalizationEvalSignTestP(wins, losses int) float64 {
	n := wins + losses
	if n == 0 {
		return 1
	}

	p := 0.0
	for x := wins; x <= n; x++ {
		p += math.Exp(logBinomial(n, x) - float64(n)*math.Ln2)
	}

	return math.Min(p, 1)
}

func logBinomial(n, k int) float64 {
	a, _ := math.Lgamma(float64(n + 1))
	b, _ := math.Lgamma(float64(k + 1))
	c, _ := math.Lgamma(float64(n - k + 1))

	return a - b - c
}

func mean(values []float64) float64 {
	sum := 0.0
	for _, v := range values {
		sum += v
	}

	return sum / float64(len(values))
}

// LocalizationEvalTreeKey is a stable short key for cache directories.
func LocalizationEvalTreeKey(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}

	return hex.EncodeToString(h.Sum(nil)[:8])
}
