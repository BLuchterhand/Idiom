package workspace

import (
	"context"
	"os"

	"idiom/internal/capabilities/workspace/llmrerank"
	"idiom/internal/repository/comprehension"
)

const defaultImpactLimit = 8 // comprehension's default pack limit

// llmRerankPlan says how wide the pack must be so the reranker sees its pool,
// and how many files the caller still gets back afterwards.
type llmRerankPlan struct {
	cfg       llmrerank.Config
	enabled   bool
	packLimit int // limit handed to Pack
	keep      int // limit the caller asked for (zero: unlimited, as before)
}

// planLLMRerank reads the environment. With the reranker off it returns the
// caller's limit unchanged, so default behaviour is identical to before.
func planLLMRerank(limit int) llmRerankPlan {
	cfg := llmrerank.FromEnv(os.Getenv)
	plan := llmRerankPlan{cfg: cfg, packLimit: limit, keep: limit}

	if !cfg.Enabled() {
		return plan
	}

	plan.enabled = true
	plan.keep = limit

	if plan.keep <= 0 {
		plan.keep = defaultImpactLimit
	}

	plan.packLimit = max(plan.keep, cfg.PoolSize)

	return plan
}

// apply reorders the pack's candidate files in place by the reranker and
// returns the status to show ("" when the reranker is off). Only the first
// PoolSize files are reranked; the rest keep their order behind them.
func (p llmRerankPlan) apply(ctx context.Context, root, task string, pack *comprehension.PackResult) string {
	if !p.enabled {
		return ""
	}

	n := min(len(pack.CandidateFiles), p.cfg.PoolSize)
	paths := make([]string, n)

	for i := range paths {
		paths[i] = pack.CandidateFiles[i].Path
	}

	docs := llmrerank.LoadDocs(root, p.cfg.CodeMapPath, paths)
	outcome := llmrerank.Run(ctx, p.cfg, task, docs)

	if outcome.Reranked() {
		head := make([]comprehension.PackFile, n)
		for i, idx := range outcome.Order {
			head[i] = pack.CandidateFiles[idx]
		}

		copy(pack.CandidateFiles, head)
	}

	return "rerank: " + outcome.Status
}
