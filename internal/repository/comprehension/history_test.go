package comprehension

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func histCommit(ts int64, msg string, files ...string) HistoryCommit {
	tokens := LexicalTokens(msg)
	c := HistoryCommit{Time: ts, Terms: map[string]int{}, Length: len(tokens), Files: files}

	for _, t := range tokens {
		c.Terms[t]++
	}

	return c
}

func always2(string) bool { return true }

func TestRankHistoryFindsFilesOfMatchingPastCommits(t *testing.T) {
	const now = 1_800_000_000

	commits := []HistoryCommit{
		histCommit(now-100, "Fireflies: dusk habitat gates", "core/habitat.h", "client/flash.cpp"),
		histCommit(now-100, "Fix tree cache eviction", "client/tree_cache.cpp"),
		histCommit(now-100, "Rename things", "a.cpp"),
	}

	hits := RankHistory(commits, now, LexicalTokens("fireflies at dusk"), always2)

	var got []string
	for _, h := range hits {
		got = append(got, h.Path)
		if !h.History {
			t.Fatalf("%s must be marked as a history hit", h.Path)
		}
	}

	if want := []string{"client/flash.cpp", "core/habitat.h"}; !slices.Equal(got, want) {
		t.Fatalf("history hits = %v, want %v (only the matching commit's files, ties by path)", got, want)
	}
}

func TestRankHistoryDownweightsLargeCommitsAndDropsHugeOnes(t *testing.T) {
	const now = 1_800_000_000

	var many []string
	for i := 0; i < historyMaxFiles+1; i++ {
		many = append(many, "bulk/"+string(rune('a'+i%26))+string(rune('a'+i/26))+".cpp")
	}

	commits := []HistoryCommit{
		histCommit(now, "habitat gates", "z_small/a.cpp"),
		histCommit(now, "habitat gates", "a_wide/a.cpp", "a_wide/b.cpp", "a_wide/c.cpp", "a_wide/d.cpp", "a_wide/e.cpp", "a_wide/f.cpp", "a_wide/g.cpp"),
		histCommit(now, "habitat gates sweep", many...),
	}

	hits := RankHistory(commits, now, LexicalTokens("habitat gates"), always2)
	if len(hits) == 0 || hits[0].Path != "z_small/a.cpp" {
		t.Fatalf("the one-file commit must lead the seven-file one, got %+v", hits)
	}

	for _, h := range hits {
		if strings.HasPrefix(h.Path, "bulk/") {
			t.Fatalf("a commit over %d files must be ignored, got %s", historyMaxFiles, h.Path)
		}
	}
}

func TestRankHistoryDecaysAndScopes(t *testing.T) {
	const now = 1_800_000_000

	year := int64(historyHalfLifeDays * secondsPerDay)
	commits := []HistoryCommit{
		histCommit(now-2*year, "habitat gates", "old/a.cpp"),
		histCommit(now, "habitat gates", "new/a.cpp"),
	}

	hits := RankHistory(commits, now, LexicalTokens("habitat gates"), always2)
	if hits[0].Path != "new/a.cpp" || hits[1].Score >= hits[0].Score/3 {
		t.Fatalf("a commit two half-lives old must score about a quarter, got %+v", hits)
	}

	scoped := RankHistory(commits, now, LexicalTokens("habitat gates"), func(p string) bool { return strings.HasPrefix(p, "old/") })
	if len(scoped) != 1 || scoped[0].Path != "old/a.cpp" {
		t.Fatalf("scope filter ignored: %+v", scoped)
	}
}

func TestParseHistory(t *testing.T) {
	raw := "\x00100\x01First change\n\nbody text\x02\n\na.go\nb/c.go\n\x00200\x01Second\x02\n\nd.go\n"

	got := parseHistory([]byte(raw))
	if len(got) != 2 || got[0].Time != 100 || !slices.Equal(got[0].Files, []string{"a.go", "b/c.go"}) || got[0].Terms["body"] != 1 || got[1].Files[0] != "d.go" {
		t.Fatalf("parsed = %+v", got)
	}
}

// End to end in a real repository: a task that shares no word with the file
// itself finds it through the message of the commit that touched it, and a
// commit made after the checked-out base is invisible.
func TestPackFindsFilesThroughCommitHistory(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)

		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}

		return strings.TrimSpace(string(out))
	}
	write := func(p, c string) {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	run("init", "-q")
	write("go.mod", "module example.com/hist\n\ngo 1.22\n")
	write("quiet/zeta.go", "package quiet\n\nfunc Zeta() int { return 1 }\n")
	write("quiet/other.go", "package quiet\n\nfunc Other() int { return 2 }\n")
	run("add", "-A")
	run("commit", "-q", "-m", "Pelican migration schedule for harbour boats", "--", "quiet/zeta.go", "go.mod", "quiet/other.go")

	base := run("rev-parse", "HEAD")

	write("quiet/late.go", "package quiet\n\nfunc Late() int { return 3 }\n")
	run("add", "-A")
	run("commit", "-q", "-m", "Pelican migration follow up", "--", "quiet/late.go")
	run("checkout", "-q", "--detach", base)

	if _, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"}); err != nil {
		t.Fatal(err)
	}

	pack, err := Pack(context.Background(), PackOptions{Root: root, Task: "pelican migration schedule", Now: testTime()})
	if err != nil {
		t.Fatal(err)
	}

	var paths []string
	for _, f := range pack.CandidateFiles {
		paths = append(paths, f.Path)
	}

	if !slices.Contains(paths, "quiet/zeta.go") || !slices.Contains(paths, "quiet/other.go") {
		t.Fatalf("files of the matching commit missing from the pack: %v", paths)
	}

	if slices.Contains(paths, "quiet/late.go") {
		t.Fatalf("a commit after the checked-out base leaked into the pack: %v", paths)
	}
}

func TestRankHistoryDiscountsHubFiles(t *testing.T) {
	const now = 1_800_000_000

	// hub.cpp is touched by every commit, rare.cpp by the one that matches.
	commits := []HistoryCommit{histCommit(now, "habitat gates", "hub.cpp", "rare.cpp")}
	for i := 0; i < 30; i++ {
		commits = append(commits, histCommit(now, "unrelated change", "hub.cpp"))
	}

	hits := RankHistory(commits, now, LexicalTokens("habitat gates"), always2)
	if len(hits) != 2 || hits[0].Path != "rare.cpp" {
		t.Fatalf("a file touched by every commit must rank below the file only the match touched, got %+v", hits)
	}
}
