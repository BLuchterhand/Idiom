package comprehension

import (
	"context"
	"strings"
)

// Past-work channel: two RRF lists drawn from the repository's own past.
//
//   - similar work: past work items (a ledger row with the commits that name
//     its id, or a lone commit) whose text matches the task, each pointing at
//     the files its commits changed;
//   - co-change: files that changed in the same commits as the files the
//     other channels already rank highest (logical coupling).
//
// Every input is cut at ChangeHistory.AsOf, so a task never reads the change
// it asks for. Design: docs/design-history-channel.md.

// Channel names reported in LexicalHit.PastWork and rankedList.Source.
const (
	pastWorkSimilar  = "similar_work"
	pastWorkCoChange = "co_change"
)

// ChangeHistory is the repository's past as of one instant: non-merge commits
// with the files they changed, and the planning ledger's rows. It is built
// once per query by LoadChangeHistory and read by the two rankers.
type ChangeHistory struct {
	// AsOf is the cut, in Unix seconds: nothing newer is in Commits or Items.
	AsOf    int64
	Commits []PastCommit
	Items   []WorkItem
}

// PastCommit is one commit before the cut.
type PastCommit struct {
	Time  int64
	Hash  string
	Files []string
}

// WorkItem is one unit of past work: a ledger row joined with the commits
// whose messages name its id, or a commit that names no row.
type WorkItem struct {
	ID     string
	Time   int64 // newest commit of the item
	Terms  map[string]int
	Length int
	Files  []string // distinct, sorted
}

// LoadChangeHistory reads root's git history and ledger as of the cut. It
// returns an empty history outside a git work tree, with the channel disabled
// or on any git failure: the channel adds evidence and never blocks a pack.
func LoadChangeHistory(ctx context.Context, root string, cfg HistoryConfig) ChangeHistory {
	return loadChangeHistory(ctx, root, cfg)
}

// RankSimilarWork scores past work items against the task terms (BM25 over
// item text, decayed by age) and returns the files of the best items, best
// first. include admits only indexed, in-scope files.
func (h ChangeHistory) RankSimilarWork(terms []string, include func(path string) bool) []LexicalHit {
	return rankSimilarWork(h, terms, include)
}

// RankCoChange scores files by how often they changed together with the seed
// files (the head of the other channels' fused list). Files that are seeds
// are not returned.
func (h ChangeHistory) RankCoChange(seeds []LexicalHit, include func(path string) bool) []LexicalHit {
	return rankCoChange(h, seeds, include)
}

// pastWorkLists builds the channel's RRF lists. base is the fused list of the
// other channels: its head seeds the co-change graph, and the similar-work
// list joins the seeds so a past change can pull in its own neighbours.
func pastWorkLists(ctx *packContext, include func(string) bool, base []LexicalHit) []rankedList {
	cfg, err := LoadHistoryConfig(ctx.root)
	if err != nil || cfg.Disabled {
		return nil
	}

	indexed := func(path string) bool {
		_, ok := ctx.nodesByID[fileNodeID(path)]

		return ok && include(path)
	}

	history := LoadChangeHistory(ctx.requestContext, ctx.root, cfg)
	similar := history.RankSimilarWork(LexicalTokens(ctx.task), indexed)

	seedBase := fuseRankedLists(rankedList{Source: "base", Hits: base}, rankedList{Source: pastWorkSimilar, Hits: similar})
	co := history.RankCoChange(seedBase, indexed)

	var lists []rankedList

	// A weight of 0 drops the list (rankedList treats 0 as 1).
	if w := cfg.similarWeight(); w > 0 {
		lists = append(lists, rankedList{Source: pastWorkSimilar, Hits: similar, Weight: w})
	}

	if w := cfg.coChangeWeight(); w > 0 {
		lists = append(lists, rankedList{Source: pastWorkCoChange, Hits: co, Weight: w})
	}

	return lists
}

func pastWorkReason(source string) string {
	return strings.ReplaceAll(source, "_", " ")
}
