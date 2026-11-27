package comprehension

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	pastMaxCommits = 20000 // newest commits read
	// pastMaxCommitFiles: a commit touching more files is a sweep (format,
	// rename, import) and says little about any one file.
	pastMaxCommitFiles = 50
	// pastMaxMergeFiles bounds a merge that names a ledger row: its diff
	// against the first parent is the row's whole contribution.
	pastMaxMergeFiles = 150
	pastGitTimeout    = 20 * time.Second

	pastCommitSep = "\x00"
	pastFieldSep  = "\x01"
	pastRecordSep = "\x02"
	// %ct time, %H hash, %P parents; \x01 then the message; \x02 then files.
	pastGitFormat = "--format=%x00%ct %H %P%x01%B%x02"

	// defaultLedgerRowRegex captures the first cell of a Markdown table row
	// whose id looks like T335, G823 or T335b.
	defaultLedgerRowRegex = `^\|\s*([A-Za-z]{1,4}\d{1,5}[a-z]{0,2})\s*\|`
)

// rowMention finds row-id-shaped words in a commit message; only those that
// are ledger rows count.
var rowMention = regexp.MustCompile(`\b[A-Za-z]{1,4}\d{1,5}[a-z]{0,2}\b`)

type rawCommit struct {
	time    int64
	hash    string
	merge   bool
	message string
	files   []string
}

func loadChangeHistory(ctx context.Context, root string, cfg HistoryConfig) ChangeHistory {
	if !cfg.enabled() {
		return ChangeHistory{}
	}

	raw, headTime := cachedRawCommits(ctx, root)
	if len(raw) == 0 {
		return ChangeHistory{}
	}

	asOf := headTime
	if cut, ok := cfg.asOfUnix(); ok && cut < asOf {
		asOf = cut
	}

	raw = commitsAsOf(raw, asOf)
	rows := loadLedgerRows(root, cfg)

	return ChangeHistory{AsOf: asOf, Commits: plainCommits(raw), Items: buildWorkItems(raw, rows)}
}

// commitsAsOf drops commits newer than the cut. The cut is applied here, not
// trusted to ancestry or to git's --before, because commit dates are not
// monotone along a branch.
func commitsAsOf(raw []rawCommit, asOf int64) []rawCommit {
	return slices.DeleteFunc(raw, func(c rawCommit) bool { return c.time > asOf })
}

func plainCommits(raw []rawCommit) []PastCommit {
	var out []PastCommit

	for _, c := range raw {
		if !c.merge && len(c.files) > 0 && len(c.files) <= pastMaxCommitFiles {
			out = append(out, PastCommit{Time: c.time, Hash: c.hash, Files: c.files})
		}
	}

	return out
}

func gitHeadHash(root string) string {
	ctx, cancel := context.WithTimeout(context.Background(), pastGitTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(out))
}

// readRawCommits runs one `git log`: merges are shown against their first
// parent so a merge's files are what it brought into the mainline.
func readRawCommits(ctx context.Context, root string) ([]rawCommit, int64) {
	ctx, cancel := context.WithTimeout(ctx, pastGitTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "git", "-C", root, "log", "--no-color", "--name-only",
		"--diff-merges=first-parent", "--max-count="+strconv.Itoa(pastMaxCommits), pastGitFormat).Output()
	if err != nil {
		return nil, 0
	}

	commits := parseRawCommits(out)

	var head int64
	if headOut, err := exec.CommandContext(ctx, "git", "-C", root, "log", "-1", "--format=%ct").Output(); err == nil {
		head, _ = strconv.ParseInt(strings.TrimSpace(string(headOut)), 10, 64)
	}

	return commits, head
}

func parseRawCommits(out []byte) []rawCommit {
	var commits []rawCommit

	for _, block := range bytes.Split(out, []byte(pastCommitSep))[1:] {
		head, rest, ok := strings.Cut(string(block), pastFieldSep)
		if !ok {
			continue
		}

		message, files, _ := strings.Cut(rest, pastRecordSep)
		fields := strings.Fields(head)

		if len(fields) < 2 {
			continue
		}

		ts, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}

		c := rawCommit{time: ts, hash: fields[1], merge: len(fields) > 3, message: message}

		for _, f := range strings.Split(files, "\n") {
			if f = strings.TrimSpace(f); f != "" {
				c.files = append(c.files, filepath.ToSlash(f))
			}
		}

		commits = append(commits, c)
	}

	return commits
}

// loadLedgerRows maps each row id to the row's text. Missing ledger, empty
// path or a bad regex yield no rows: commit messages alone still work.
func loadLedgerRows(root string, cfg HistoryConfig) map[string]string {
	if cfg.LedgerPath == "" {
		return nil
	}

	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(cfg.LedgerPath)))
	if err != nil {
		return nil
	}

	return parseLedgerRows(string(data), cfg.LedgerIDRegex)
}

func parseLedgerRows(text, idRegex string) map[string]string {
	if idRegex == "" {
		idRegex = defaultLedgerRowRegex
	}

	re, err := regexp.Compile(idRegex)
	if err != nil || re.NumSubexp() < 1 {
		return nil
	}

	rows := map[string]string{}

	for _, line := range strings.Split(text, "\n") {
		if m := re.FindStringSubmatch(line); m != nil {
			rows[m[1]] += " " + line
		}
	}

	return rows
}

// buildWorkItems joins commits to ledger rows by the ids their messages name.
// A merge counts only when it names a row (its branch commits are separate
// non-merge commits, so a nameless merge would count the same files twice).
func buildWorkItems(raw []rawCommit, rows map[string]string) []WorkItem {
	type draft struct {
		text  []string
		files map[string]struct{}
		time  int64
	}

	drafts := map[string]*draft{}
	add := func(id, rowText, message string, c rawCommit) {
		d := drafts[id]
		if d == nil {
			d = &draft{files: map[string]struct{}{}}
			if rowText != "" {
				d.text = append(d.text, rowText)
			}

			drafts[id] = d
		}

		d.text = append(d.text, message)
		d.time = max(d.time, c.time)

		for _, f := range c.files {
			d.files[f] = struct{}{}
		}
	}

	for _, c := range raw {
		limit := pastMaxCommitFiles
		if c.merge {
			limit = pastMaxMergeFiles
		}

		if len(c.files) == 0 || len(c.files) > limit {
			continue
		}

		ids := mentionedRows(c.message, rows)
		switch {
		case len(ids) > 0:
			for _, id := range ids {
				add(id, rows[id], c.message, c)
			}
		case !c.merge:
			add(c.hash, "", c.message, c)
		}
	}

	return finishWorkItems(drafts, func(d *draft) ([]string, []string, int64) {
		return d.text, sortedKeys(d.files), d.time
	})
}

func finishWorkItems[D any](drafts map[string]D, parts func(D) ([]string, []string, int64)) []WorkItem {
	items := make([]WorkItem, 0, len(drafts))

	for id, d := range drafts {
		text, files, ts := parts(d)
		tokens := LexicalTokens(strings.Join(text, " "))
		item := WorkItem{ID: id, Time: ts, Terms: make(map[string]int, len(tokens)), Length: len(tokens), Files: files}

		for _, t := range tokens {
			item.Terms[t]++
		}

		items = append(items, item)
	}

	slices.SortFunc(items, func(a, b WorkItem) int { return strings.Compare(a.ID, b.ID) })

	return items
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	slices.Sort(out)

	return out
}

// mentionedRows returns the distinct ledger row ids a message names.
func mentionedRows(message string, rows map[string]string) []string {
	if len(rows) == 0 {
		return nil
	}

	var ids []string

	for _, word := range rowMention.FindAllString(message, -1) {
		if _, ok := rows[word]; ok && !slices.Contains(ids, word) {
			ids = append(ids, word)
		}
	}

	return ids
}
