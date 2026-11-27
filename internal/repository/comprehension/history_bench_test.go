package comprehension

import (
	"context"
	"os"
	"testing"
)

// BenchmarkPastWorkChannel measures the per-query cost of the channel on a
// real repository: IDIOM_HISTORY_BENCH_REPO names a git checkout and
// IDIOM_HISTORY_BENCH_LEDGER a ledger path inside it (optional).
//
//	IDIOM_HISTORY_BENCH_REPO=... go test ./internal/repository/comprehension -run xxx -bench PastWork -benchtime 5x
func BenchmarkPastWorkChannel(b *testing.B) {
	root := os.Getenv("IDIOM_HISTORY_BENCH_REPO")
	if root == "" {
		b.Skip("IDIOM_HISTORY_BENCH_REPO not set")
	}

	cfg := HistoryConfig{LedgerPath: os.Getenv("IDIOM_HISTORY_BENCH_LEDGER")}
	terms := LexicalTokens("rewrite the structure system save path and the net codec")
	seeds := []LexicalHit{{Path: "core/structures/structure_system.cpp"}, {Path: "net/codec.cpp"}}

	b.Run("load", func(b *testing.B) {
		for range b.N {
			LoadChangeHistory(context.Background(), root, cfg)
		}
	})

	h := LoadChangeHistory(context.Background(), root, cfg)

	b.Run("rank", func(b *testing.B) {
		for range b.N {
			h.RankSimilarWork(terms, allowed)
			h.RankCoChange(seeds, allowed)
		}
	})
}
