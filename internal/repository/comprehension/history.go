package comprehension

import (
	"bytes"
	"context"
	"math"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Commit-message history channel. Past commits whose messages match the task
// point at the files they touched (the similar-past-change idea of Locus and
// of bug-report localization): a task rarely repeats a file's own words, but
// it often repeats the words of an earlier change to that file. History is
// read from the checked-out commit's ancestors only, so a case evaluated at a
// base never sees its own change.

const (
	historyMaxCommits    = 20000 // newest non-merge commits read
	historyTopCommits    = 30    // matching commits that contribute files
	historyHalfLifeDays  = 365.0 // recency decay of a commit's match score
	historyMaxFiles      = 50    // commits touching more files are noise
	secondsPerDay        = 86400.0
	historyGitTimeout    = 20 * time.Second
	historyRecordSep     = "\x02"
	historyFieldSep      = "\x01"
	historyCommitSep     = "\x00"
	historyGitLogFormat  = "--format=%x00%ct%x01%B%x02"
	historyMinMessageLen = 1

	// historyChannelWeight is the history list's RRF weight, chosen on the dev
	// split: 0.1 / 0.25 / 0.5 / 1 / 2 gave dev recall .405 / .412 / .410 / .433 /
	// .391 on BenTech and .478 / .484 / .484 / .505 / .451 on Idiom. 1 is equal
	// weight, the unfitted RRF default.
	historyChannelWeight = 1.0
)

// HistoryCommit is one past commit: its time, message terms and files.
type HistoryCommit struct {
	Time   int64
	Terms  map[string]int
	Length int
	Files  []string
}

// LoadHistory reads the non-merge ancestors of HEAD with their files, and the
// time of HEAD (the reference for recency). It returns nothing outside a git
// work tree.
func LoadHistory(ctx context.Context, root string) ([]HistoryCommit, int64) {
	ctx, cancel := context.WithTimeout(ctx, historyGitTimeout)
	defer cancel()

	headOut, err := exec.CommandContext(ctx, "git", "-C", root, "log", "-1", "--format=%ct").Output()
	if err != nil {
		return nil, 0
	}

	headTime, _ := strconv.ParseInt(strings.TrimSpace(string(headOut)), 10, 64)

	out, err := exec.CommandContext(ctx, "git", "-C", root, "log", "--no-merges", "--name-only",
		"--max-count="+strconv.Itoa(historyMaxCommits), historyGitLogFormat).Output()
	if err != nil {
		return nil, 0
	}

	return parseHistory(out), headTime
}

// parseHistory parses `git log --name-only` output in historyGitLogFormat.
func parseHistory(out []byte) []HistoryCommit {
	var commits []HistoryCommit

	for _, block := range bytes.Split(out, []byte(historyCommitSep))[1:] {
		head, rest, ok := strings.Cut(string(block), historyFieldSep)
		if !ok {
			continue
		}

		message, files, _ := strings.Cut(rest, historyRecordSep)

		ts, err := strconv.ParseInt(strings.TrimSpace(head), 10, 64)
		if err != nil || len(strings.TrimSpace(message)) < historyMinMessageLen {
			continue
		}

		tokens := LexicalTokens(message)
		commit := HistoryCommit{Time: ts, Terms: make(map[string]int, len(tokens)), Length: len(tokens)}

		for _, t := range tokens {
			commit.Terms[t]++
		}

		for _, f := range strings.Split(files, "\n") {
			if f = strings.TrimSpace(f); f != "" {
				commit.Files = append(commit.Files, f)
			}
		}

		commits = append(commits, commit)
	}

	return commits
}

// RankHistory scores past commits against the task terms (BM25 over commit
// messages, decayed by age), keeps the best historyTopCommits, and gives each
// file the score of the commits that touched it, a commit's share divided by
// log2(1+files) so a sweeping commit says little about any one file. include
// admits only indexed, in-scope files.
func RankHistory(commits []HistoryCommit, headTime int64, terms []string, include func(path string) bool) []LexicalHit {
	var docs []HistoryCommit

	total := 0

	for _, c := range commits {
		if len(c.Files) > 0 && len(c.Files) <= historyMaxFiles {
			docs = append(docs, c)
			total += c.Length
		}
	}

	if len(docs) == 0 {
		return nil
	}

	terms = UniqueLexicalTerms(terms)
	avg := BM25AvgLength(total, len(docs))
	df := make(map[string]int, len(terms))

	for _, c := range docs {
		for _, t := range terms {
			if c.Terms[t] > 0 {
				df[t]++
			}
		}
	}

	type scored struct {
		score float64
		files []string
	}

	var matched []scored

	for _, c := range docs {
		s := 0.0

		for _, t := range terms {
			if tf := c.Terms[t]; tf > 0 {
				s += BM25IDF(len(docs), df[t]) * BM25TermScore(tf, c.Length, avg)
			}
		}

		if s > 0 {
			age := math.Max(0, float64(headTime-c.Time)) / secondsPerDay
			matched = append(matched, scored{s * math.Pow(0.5, age/historyHalfLifeDays), c.Files})
		}
	}

	slices.SortStableFunc(matched, func(a, b scored) int {
		switch {
		case a.score > b.score:
			return -1
		case a.score < b.score:
			return 1
		default:
			return 0
		}
	})

	byFile := map[string]float64{}

	for _, m := range matched[:min(historyTopCommits, len(matched))] {
		share := m.score / math.Log2(1+float64(len(m.files)))

		for _, f := range m.files {
			if include(f) {
				byFile[f] += share
			}
		}
	}

	hits := make([]LexicalHit, 0, len(byFile))
	for f, s := range byFile {
		hits = append(hits, LexicalHit{Path: f, Score: s, History: true})
	}

	slices.SortFunc(hits, func(a, b LexicalHit) int {
		switch {
		case a.Score > b.Score:
			return -1
		case a.Score < b.Score:
			return 1
		default:
			return strings.Compare(a.Path, b.Path)
		}
	})

	return hits
}
