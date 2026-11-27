package eval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"idiom/internal/repository/comprehension"
)

// LocalizationEvalCaseFile is the checked-in case set of one corpus.
type LocalizationEvalCaseFile struct {
	Schema int    `json:"schema"`
	Corpus string `json:"corpus"`
	// Gate is "" (gain gates allowed) or "no_regression": a corpus whose
	// queries are post-hoc commit messages may only guard against loss.
	Gate         string                           `json:"gate,omitempty"`
	Config       LocalizationEvalBuilderConfig    `json:"builder"`
	Localization comprehension.LocalizationConfig `json:"localization,omitempty"`
	// SplitCut (RFC 3339) splits a usage corpus by time: cases before it are
	// dev, the rest hold-out. Empty means the hash split.
	SplitCut string                 `json:"split_cut,omitempty"`
	Cases    []LocalizationEvalCase `json:"cases"`
}

// LocalizationEvalCase is one task. Query comes from the task text that existed
// at Base; Gold is the scoreable files (they exist at Base); GoldNew are files
// the change created, which no index at Base can rank, so they are listed and
// never scored.
type LocalizationEvalCase struct {
	ID          string   `json:"id"`
	Merge       string   `json:"merge"`
	Base        string   `json:"base"`
	Query       string   `json:"query"`
	QuerySource string   `json:"query_source"`
	Gold        []string `json:"gold"`
	GoldNew     []string `json:"gold_new,omitempty"`
	// GoldRenames lists renamed source files. The old path is scored in Gold
	// (it is the file an index at Base contains); the new path is listed here.
	GoldRenames []LocalizationEvalRename `json:"gold_renames,omitempty"`
	GoldTests   []string                 `json:"gold_tests,omitempty"`
	GoldDocs    []string                 `json:"gold_docs,omitempty"`
	Split       string                   `json:"split"`
	// Time (RFC 3339, UTC) is when a usage case's query was sent.
	Time string `json:"time,omitempty"`
}

// LocalizationEvalRename is one renamed gold file.
type LocalizationEvalRename struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// LocalizationEvalBuilderConfig carries every repository-specific rule of the
// case builder, so the builder itself stays generic.
type LocalizationEvalBuilderConfig struct {
	// Commits selects "merges" (first-parent merge commits) or "commits"
	// (every first-parent commit).
	Commits string `json:"commits"`
	// Since is a git revision range start (exclusive) or empty.
	Since string `json:"since,omitempty"`
	// MaxCommits bounds how many candidate commits are scanned.
	MaxCommits int `json:"max_commits,omitempty"`
	// QueryFrom is "ledger" (row text at base) or "message" (commit message).
	QueryFrom string `json:"query_from"`
	// LedgerPaths, SubjectIDRegex and LedgerRowRegex drive QueryFrom=ledger.
	// SubjectIDRegex has one group (the row id). LedgerRowRegex has a %s for
	// the quoted id and one group (the query text).
	LedgerPaths    []string `json:"ledger_paths,omitempty"`
	SubjectIDRegex string   `json:"subject_id_regex,omitempty"`
	LedgerRowRegex string   `json:"ledger_row_regex,omitempty"`
	// SourceRegex marks scoreable source files; the Exclude lists remove files.
	SourceRegex  string   `json:"source_regex"`
	ExcludeGold  []string `json:"exclude_gold,omitempty"`
	TestRegex    string   `json:"test_regex,omitempty"`
	DocRegex     string   `json:"doc_regex,omitempty"`
	MaxGoldFiles int      `json:"max_gold_files,omitempty"`
	MinQueryLen  int      `json:"min_query_len,omitempty"`
	MaxCases     int      `json:"max_cases,omitempty"`
	// Usage drives cases built from agent sessions instead of commits.
	Usage *LocalizationUsageConfig `json:"usage,omitempty"`
}

func gitOut(repo string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)

	var stdout, stderr bytes.Buffer

	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}

	return stdout.String(), nil
}

// BuildLocalizationEvalCases scans history newest first and returns the cases
// that satisfy the builder config, one per commit, de-duplicated by commit and
// by identical (query, gold) pairs, split by hash.
func BuildLocalizationEvalCases(repo string, cfg LocalizationEvalBuilderConfig) ([]LocalizationEvalCase, error) {
	args := []string{"log", "--first-parent", "--format=%H"}
	if cfg.Commits == "merges" {
		args = append(args, "--merges")
	}

	if cfg.MaxCommits > 0 {
		args = append(args, fmt.Sprintf("--max-count=%d", cfg.MaxCommits))
	}

	if cfg.Since != "" {
		args = append(args, cfg.Since+"..HEAD")
	}

	out, err := gitOut(repo, args...)
	if err != nil {
		return nil, err
	}

	source, err := regexp.Compile(cfg.SourceRegex)
	if err != nil {
		return nil, fmt.Errorf("source_regex: %w", err)
	}

	excludes, err := compileAll(cfg.ExcludeGold)
	if err != nil {
		return nil, err
	}

	var cases []LocalizationEvalCase

	seen := map[string]struct{}{}

	for _, merge := range strings.Fields(out) {
		c, ok, err := buildLocalizationEvalCase(repo, merge, cfg, source, excludes)
		if err != nil {
			return nil, err
		}

		if !ok {
			continue
		}

		key := c.Query + "\x00" + strings.Join(c.Gold, "\x00")
		if _, dup := seen[key]; dup {
			continue
		}

		seen[key] = struct{}{}

		cases = append(cases, c)
		if cfg.MaxCases > 0 && len(cases) >= cfg.MaxCases {
			break
		}
	}

	return cases, nil
}

func compileAll(patterns []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(patterns))

	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("pattern %q: %w", p, err)
		}

		out = append(out, re)
	}

	return out, nil
}

func buildLocalizationEvalCase(repo, merge string, cfg LocalizationEvalBuilderConfig, source *regexp.Regexp, excludes []*regexp.Regexp) (LocalizationEvalCase, bool, error) {
	base, err := gitOut(repo, "rev-parse", merge+"^1")
	if err != nil {
		return LocalizationEvalCase{}, false, nil //nolint:nilerr // a root commit has no base and is not a case
	}

	base = strings.TrimSpace(base)

	query, querySource, ok, err := localizationEvalQuery(repo, merge, base, cfg)
	if err != nil || !ok || len(query) < max(cfg.MinQueryLen, 1) {
		return LocalizationEvalCase{}, false, err
	}

	names, err := gitOut(repo, "diff", "--name-status", "-M", base, merge)
	if err != nil {
		return LocalizationEvalCase{}, false, err
	}

	c := LocalizationEvalCase{ID: shortRef(merge), Merge: merge, Base: base, Query: query, QuerySource: querySource, Split: LocalizationEvalSplit(merge)}
	testRe := optionalRegexp(cfg.TestRegex)
	docRe := optionalRegexp(cfg.DocRegex)

	for _, line := range strings.Split(strings.TrimSpace(names), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			continue
		}

		status, path := fields[0], fields[len(fields)-1]
		if strings.HasPrefix(status, "R") && len(fields) == 3 {
			c = recordRename(c, fields[1], fields[2], source, excludes, testRe, docRe)
			continue
		}

		switch {
		case testRe != nil && testRe.MatchString(path):
			c.GoldTests = append(c.GoldTests, path)
		case docRe != nil && docRe.MatchString(path):
			c.GoldDocs = append(c.GoldDocs, path)
		case !source.MatchString(path) || matchesAny(excludes, path):
		case status == "A":
			c.GoldNew = append(c.GoldNew, path)
		case status == "D" || status == "M":
			c.Gold = append(c.Gold, path)
		}
	}

	slices.Sort(c.Gold)

	if len(c.Gold) == 0 || (cfg.MaxGoldFiles > 0 && len(c.Gold) > cfg.MaxGoldFiles) {
		return LocalizationEvalCase{}, false, nil
	}

	return c, true, nil
}

func optionalRegexp(pattern string) *regexp.Regexp {
	if pattern == "" {
		return nil
	}

	return regexp.MustCompile(pattern)
}

func matchesAny(res []*regexp.Regexp, path string) bool {
	for _, re := range res {
		if re.MatchString(path) {
			return true
		}
	}

	return false
}

func shortRef(hash string) string {
	if len(hash) > 9 {
		return hash[:9]
	}

	return hash
}

func localizationEvalQuery(repo, merge, base string, cfg LocalizationEvalBuilderConfig) (string, string, bool, error) {
	if cfg.QueryFrom == "message" {
		body, err := gitOut(repo, "show", "-s", "--format=%B", merge)
		if err != nil {
			return "", "", false, err
		}

		return cleanCommitMessage(body), "commit message", true, nil
	}

	subject, err := gitOut(repo, "show", "-s", "--format=%s", merge)
	if err != nil {
		return "", "", false, err
	}

	idRe, err := regexp.Compile(cfg.SubjectIDRegex)
	if err != nil {
		return "", "", false, fmt.Errorf("subject_id_regex: %w", err)
	}

	m := idRe.FindStringSubmatch(subject)
	if m == nil {
		return "", "", false, nil
	}

	rowRe, err := regexp.Compile(fmt.Sprintf(cfg.LedgerRowRegex, regexp.QuoteMeta(m[1])))
	if err != nil {
		return "", "", false, fmt.Errorf("ledger_row_regex: %w", err)
	}

	for _, ledger := range cfg.LedgerPaths {
		text, err := gitOut(repo, "show", base+":"+ledger)
		if err != nil {
			continue // the ledger file may not exist yet at an old base
		}

		if row := rowRe.FindStringSubmatch(text); row != nil {
			return strings.TrimSpace(row[1]), "ledger " + ledger + " at base, row " + m[1], true, nil
		}
	}

	return "", "", false, nil
}

var (
	trailerRe     = regexp.MustCompile(`(?im)^(co-authored-by|signed-off-by):.*$`)
	mergePrefixRe = regexp.MustCompile(`^(?i)merge[^:\n]*:\s*`)
)

func cleanCommitMessage(message string) string {
	message = trailerRe.ReplaceAllString(message, "")
	message = mergePrefixRe.ReplaceAllString(strings.TrimSpace(message), "")

	return strings.Join(strings.Fields(message), " ")
}

// ReadLocalizationEvalCaseFile loads and validates a case file.
func ReadLocalizationEvalCaseFile(path string) (LocalizationEvalCaseFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return LocalizationEvalCaseFile{}, err
	}

	var file LocalizationEvalCaseFile
	if err := json.Unmarshal(data, &file); err != nil {
		return LocalizationEvalCaseFile{}, fmt.Errorf("%s: %w", path, err)
	}

	seen := map[string]struct{}{}

	for _, c := range file.Cases {
		if c.ID == "" || c.Merge == "" || c.Base == "" || c.Query == "" || len(c.Gold) == 0 {
			return file, fmt.Errorf("%s: case %q is incomplete", path, c.ID)
		}

		if _, dup := seen[c.Merge]; dup {
			return file, fmt.Errorf("%s: merge %s appears twice", path, c.Merge)
		}

		seen[c.Merge] = struct{}{}

		if want := localizationEvalWantSplit(file.SplitCut, c); c.Split != want {
			return file, fmt.Errorf("%s: case %s split %q does not match hash split %q", path, c.ID, c.Split, want)
		}
	}

	return file, nil
}

// localizationEvalWantSplit is the side a case belongs on: by time when the
// file states a cut, by hash otherwise.
func localizationEvalWantSplit(splitCut string, c LocalizationEvalCase) string {
	if splitCut == "" {
		return LocalizationEvalSplit(c.Merge)
	}

	cut, err := time.Parse(time.RFC3339, splitCut)
	if err != nil {
		return "invalid split_cut " + splitCut
	}

	return localizationTimeSplit(c.Time, cut)
}

// recordRename scores a renamed source file by its old path, which an index at
// the base commit contains, and keeps the new path alongside it. Renamed tests
// and docs follow the same kind rules as any other changed file.
func recordRename(c LocalizationEvalCase, from, to string, source *regexp.Regexp, excludes []*regexp.Regexp, testRe, docRe *regexp.Regexp) LocalizationEvalCase {
	switch {
	case testRe != nil && testRe.MatchString(to):
		c.GoldTests = append(c.GoldTests, to)
	case docRe != nil && docRe.MatchString(to):
		c.GoldDocs = append(c.GoldDocs, to)
	case source.MatchString(from) && !matchesAny(excludes, from):
		c.Gold = append(c.Gold, from)
		c.GoldRenames = append(c.GoldRenames, LocalizationEvalRename{From: from, To: to})
	}

	return c
}
