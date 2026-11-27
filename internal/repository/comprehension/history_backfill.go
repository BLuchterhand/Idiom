package comprehension

import "slices"

// backfillSlots is how many leading positions the metric and the reader use:
// the top 8. Backfill only ever changes positions keep..backfillSlots-1.
const backfillSlots = 8

// backfillOrder keeps the first `keep` files of base exactly where they are,
// fills positions keep..backfillSlots-1 with the best history files not
// already among the kept ones, then lists what is left of base in its own
// order, then any further history files. A case whose gold is inside the
// kept head therefore scores as before: recall, hit and MRR cannot fall.
//
// Scores are made non-increasing down the list, because the pack reads the
// order through them: a history file inherits just under the score before it.
func backfillOrder(base, history []LexicalHit, keep int) []LexicalHit {
	keep = min(max(keep, 0), len(base))
	out := slices.Clone(base[:keep])
	placed := make(map[string]struct{}, len(base)+len(history))

	for _, h := range out {
		placed[h.Path] = struct{}{}
	}

	score := 0.0
	if len(out) > 0 {
		score = out[len(out)-1].Score
	}

	add := func(h LexicalHit, inherits bool) {
		if _, dup := placed[h.Path]; dup {
			return
		}

		placed[h.Path] = struct{}{}

		if inherits || h.Score > score {
			h.Score = score * backfillScoreStep
		}

		score = h.Score
		out = append(out, h)
	}

	for _, h := range history {
		if len(out) >= backfillSlots {
			break
		}

		add(h, true)
	}

	for _, h := range base[keep:] {
		add(h, false)
	}

	for _, h := range history {
		add(h, true)
	}

	return out
}

// backfillScoreStep puts an inserted file just under its predecessor.
const backfillScoreStep = 0.999
