package comprehension

import (
	"context"
	"os"
	"path/filepath"
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

	cfg := HistoryConfig{Mode: historyModeFuse, LedgerPath: os.Getenv("IDIOM_HISTORY_BENCH_LEDGER")}
	terms := LexicalTokens("rewrite the structure system save path and the net codec")
	seeds := []LexicalHit{{Path: "core/structures/structure_system.cpp"}, {Path: "net/codec.cpp"}}

	cache := filepath.Join(root, filepath.FromSlash(historyCacheRelPath))

	b.Run("load_cold", func(b *testing.B) {
		for range b.N {
			b.StopTimer()
			_ = os.Remove(cache)
			b.StartTimer()
			LoadChangeHistory(context.Background(), root, cfg)
		}
	})

	b.Run("load_warm", func(b *testing.B) {
		LoadChangeHistory(context.Background(), root, cfg)
		b.ResetTimer()

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
