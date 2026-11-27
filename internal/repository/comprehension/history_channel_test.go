package comprehension

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Synthetic fixture only: no real ledger, query or commit text.

var (
	epoch   = time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	allowed = func(string) bool { return true }
)

type fixtureRepo struct {
	t    *testing.T
	root string
}

func newFixtureRepo(t *testing.T) *fixtureRepo {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	r := &fixtureRepo{t: t, root: t.TempDir()}
	r.git(nil, "init", "-q", "-b", "main")
	r.git(nil, "config", "user.email", "t@example.invalid")
	r.git(nil, "config", "user.name", "t")
	r.git(nil, "config", "commit.gpgsign", "false")

	return r
}

func (r *fixtureRepo) git(when *time.Time, args ...string) {
	r.t.Helper()

	cmd := exec.Command("git", append([]string{"-C", r.root}, args...)...)
	cmd.Env = os.Environ()

	if when != nil {
		stamp := when.Format(time.RFC3339)
		cmd.Env = append(cmd.Env, "GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
	}

	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// commit appends to the files and commits them at epoch+day days.
func (r *fixtureRepo) commit(day int, message string, files ...string) {
	r.t.Helper()

	for _, f := range files {
		path := filepath.Join(r.root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			r.t.Fatal(err)
		}

		prev, _ := os.ReadFile(path)
		if err := os.WriteFile(path, append(prev, []byte(message+"\n")...), 0o644); err != nil {
			r.t.Fatal(err)
		}
	}

	when := epoch.AddDate(0, 0, day)
	r.git(&when, "add", "-A")
	r.git(&when, "commit", "-q", "-m", message)
}

func writeLedger(t *testing.T, root, text string) {
	t.Helper()

	path := filepath.Join(root, "docs", "ledger.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func paths(hits []LexicalHit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Path
	}

	return out
}

func indexOf(list []string, want string) int {
	for i, p := range list {
		if p == want {
			return i
		}
	}

	return -1
}

func workItem(id string, ts int64, text string, files ...string) WorkItem {
	tokens := LexicalTokens(text)
	it := WorkItem{ID: id, Time: ts, Terms: map[string]int{}, Length: len(tokens), Files: files}

	for _, tok := range tokens {
		it.Terms[tok]++
	}

	return it
}

func TestParseLedgerRows(t *testing.T) {
	rows := parseLedgerRows("| Row | Title |\n|---|---|\n| X12 | kiln draught tuning | queued |\n| X12b | follow up | done |\ntext\n", "")
	if len(rows) != 2 || !strings.Contains(rows["X12"], "kiln") || rows["X12b"] == "" {
		t.Fatalf("rows = %v", rows)
	}

	if got := parseLedgerRows("# H\n- [Q7] thing\n", `^- \[([A-Z]\d+)\]`); got["Q7"] == "" {
		t.Fatalf("custom regex rows = %v", got)
	}

	if got := parseLedgerRows("| X1 |", "(("); got != nil {
		t.Fatalf("bad regex must give no rows, got %v", got)
	}
}

func TestRankSimilarWorkRanksByTextAndRecency(t *testing.T) {
	h := ChangeHistory{
		AsOf: epoch.Unix() + 100*86400,
		Items: []WorkItem{
			workItem("old", epoch.Unix(), "bellows draught", "old/bellows.go"),
			workItem("new", epoch.Unix()+99*86400, "bellows draught", "new/bellows.go"),
			workItem("other", epoch.Unix()+99*86400, "quarry hoist", "other/hoist.go"),
		},
	}

	got := paths(h.RankSimilarWork(LexicalTokens("bellows draught"), allowed))
	if len(got) != 2 || got[0] != "new/bellows.go" || got[1] != "old/bellows.go" {
		t.Fatalf("want newer item first and the unmatched item absent, got %v", got)
	}

	hits := h.RankSimilarWork(LexicalTokens("bellows"), func(p string) bool { return p != "new/bellows.go" })
	if len(hits) != 1 || hits[0].Path != "old/bellows.go" {
		t.Fatalf("include filter ignored: %v", paths(hits))
	}
}

func TestSimilarWorkSweepingItemsSayLessPerFile(t *testing.T) {
	many := make([]string, 40)
	for i := range many {
		many[i] = fmt.Sprintf("wide/f%02d.go", i)
	}

	h := ChangeHistory{AsOf: epoch.Unix(), Items: []WorkItem{
		workItem("wide", epoch.Unix(), "gasket seal", many...),
		workItem("narrow", epoch.Unix(), "gasket seal", "narrow/seal.go"),
	}}

	got := h.RankSimilarWork(LexicalTokens("gasket seal"), allowed)
	if got[0].Path != "narrow/seal.go" {
		t.Fatalf("narrow item must lead, got %v", paths(got[:3]))
	}
}

func TestCoChangeBoundsAndDiscounts(t *testing.T) {
	huge := make([]string, pastMaxCommitFiles+1)
	for i := range huge {
		huge[i] = fmt.Sprintf("sweep/s%02d.go", i)
	}

	huge[0] = "seed.go"

	day := int64(86400)
	raw := []rawCommit{
		{time: epoch.Unix(), hash: "a", files: huge}, // dropped as a sweep
		{time: epoch.Unix() + day, hash: "b", files: []string{"seed.go", "mate.go", "hub.go"}},
		{time: epoch.Unix() + 2*day, hash: "c", files: []string{"seed.go", "mate.go", "hub.go"}},
		{time: epoch.Unix() + 3*day, hash: "d", files: []string{"seed.go", "once.go"}},
		{time: epoch.Unix() + 4*day, hash: "e", files: []string{"hub.go", "z1.go"}},
		{time: epoch.Unix() + 5*day, hash: "f", files: []string{"hub.go", "z2.go"}},
		{time: epoch.Unix() + 6*day, hash: "g", merge: true, files: []string{"seed.go", "merge_only.go"}},
	}
	h := ChangeHistory{AsOf: epoch.Unix() + 7*day, Commits: plainCommits(raw)}

	got := paths(h.RankCoChange([]LexicalHit{{Path: "seed.go"}}, allowed))

	for _, bad := range []string{"sweep/s01.go", "once.go", "merge_only.go", "seed.go"} {
		if indexOf(got, bad) >= 0 {
			t.Errorf("%s must not be returned (sweep, below support, merge diff, or the seed itself): %v", bad, got)
		}
	}

	if len(got) != 2 || got[0] != "mate.go" || got[1] != "hub.go" {
		t.Fatalf("want mate.go before the always-changing hub.go, got %v", got)
	}
}

func TestMergeCountsOnlyWhenItNamesARow(t *testing.T) {
	rows := map[string]string{"R9": "| R9 | ember vent |"}
	raw := []rawCommit{
		{time: 10, hash: "c1", message: "work on the vent", files: []string{"vent.go"}},
		{time: 11, hash: "m1", merge: true, message: "merge: R9 ember vent", files: []string{"vent.go", "vent_test.go"}},
		{time: 12, hash: "m2", merge: true, message: "merge: unrelated", files: []string{"stray.go"}},
		{time: 13, hash: "c2", message: "lone change", files: []string{"lone.go"}},
	}

	byID := map[string]WorkItem{}
	for _, it := range buildWorkItems(raw, rows) {
		byID[it.ID] = it
	}

	if _, ok := byID["m2"]; ok {
		t.Error("a merge that names no row would double-count its branch commits")
	}

	if got := byID["R9"].Files; len(got) != 2 || got[0] != "vent.go" || got[1] != "vent_test.go" {
		t.Errorf("R9 files = %v, want the merge diff joined to the row", got)
	}

	if _, ok := byID["c2"]; !ok {
		t.Error("a commit naming no row is its own work item")
	}
}

// TestHistoryNeverReadsPastTheCut is the leakage guard: the commit that did
// the asked-for work (its message even repeats the task) is newer than the cut
// and must contribute neither files nor row links.
func TestHistoryNeverReadsPastTheCut(t *testing.T) {
	r := newFixtureRepo(t)
	ledger := "| Row | Title |\n|---|---|\n| Z1 | brine pump valve | queued |\n| Z2 | brine pump rewrite | queued |\n"

	r.commit(1, "seed brine pump", "pump/old.go", "docs/ledger.md")
	writeLedger(t, r.root, ledger)
	r.commit(2, "Z1 brine pump valve", "pump/valve.go", "docs/ledger.md")
	r.commit(10, "Z2 brine pump rewrite", "pump/gold.go") // the asked-for change
	cut := epoch.AddDate(0, 0, 5)

	cfg := HistoryConfig{Mode: historyModeFuse, LedgerPath: "docs/ledger.md", AsOf: cut.Format(time.RFC3339)}
	h := LoadChangeHistory(context.Background(), r.root, cfg)
	got := paths(h.RankSimilarWork(LexicalTokens("brine pump rewrite"), allowed))

	if indexOf(got, "pump/gold.go") >= 0 {
		t.Fatalf("the change after the cut leaked into similar work: %v", got)
	}

	if indexOf(got, "pump/valve.go") < 0 {
		t.Fatalf("history before the cut must still be read: %v", got)
	}

	if h.AsOf != cut.Unix() {
		t.Errorf("AsOf = %d, want %d", h.AsOf, cut.Unix())
	}

	for _, c := range h.Commits {
		if c.Time > cut.Unix() {
			t.Errorf("commit %s at %d is after the cut", c.Hash, c.Time)
		}
	}

	co := h.RankCoChange([]LexicalHit{{Path: "pump/valve.go"}}, allowed)
	if indexOf(paths(co), "pump/gold.go") >= 0 {
		t.Errorf("co-change read past the cut: %v", paths(co))
	}

	// Without a cut the same repository does show the newer commit, so the
	// checks above do not pass merely because nothing was there to leak.
	all := LoadChangeHistory(context.Background(), r.root, HistoryConfig{Mode: historyModeFuse, LedgerPath: "docs/ledger.md"})
	if indexOf(paths(all.RankSimilarWork(LexicalTokens("brine pump rewrite"), allowed)), "pump/gold.go") < 0 {
		t.Fatal("control failed: uncut history should contain the newer commit")
	}
}

func TestHistoryOffByDefaultAndOutsideGit(t *testing.T) {
	r := newFixtureRepo(t)
	r.commit(1, "anything", "a.go")

	if h := LoadChangeHistory(context.Background(), r.root, HistoryConfig{}); len(h.Commits) != 0 || len(h.Items) != 0 {
		t.Fatalf("disabled channel read history: %+v", h)
	}

	if h := LoadChangeHistory(context.Background(), t.TempDir(), HistoryConfig{}); len(h.Commits) != 0 {
		t.Fatalf("a non-git directory must give an empty history, got %+v", h)
	}
}

func TestFuseRankedListsWeights(t *testing.T) {
	a := rankedList{Source: "a", Hits: []LexicalHit{{Path: "x"}, {Path: "y"}}}
	b := rankedList{Source: "b", Hits: []LexicalHit{{Path: "y"}}, Weight: 3}

	if got := fuseRankedLists(a, b); got[0].Path != "y" {
		t.Fatalf("y is in both lists and must lead, got %v", paths(got))
	}

	// A near-zero weight must let the first list's own order win.
	b.Weight = 0.001
	if got := fuseRankedLists(a, b); got[0].Path != "x" {
		t.Fatalf("a tiny weight must not outvote rank order, got %v", paths(got))
	}
}

func TestHistoryFingerprintChangesWithCommitAndLedger(t *testing.T) {
	r := newFixtureRepo(t)
	r.commit(1, "first", "a.go")

	cfgPath := filepath.Join(r.root, ".idiom", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(cfgPath, []byte(`{"history":{"mode":"fuse","ledger_path":"docs/ledger.md"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	writeLedger(t, r.root, "| A1 | one |\n")
	f1 := historyFingerprint(r.root)

	writeLedger(t, r.root, "| A1 | one edited |\n")

	if f2 := historyFingerprint(r.root); f2 == f1 {
		t.Error("a ledger edit must change the pack cache key")
	}

	r.commit(2, "second", "b.go")

	if f3 := historyFingerprint(r.root); f3 == f1 || f3 == "" {
		t.Error("a new commit must change the pack cache key")
	}
}
