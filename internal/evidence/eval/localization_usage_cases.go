package eval

import (
	"bufio"
	"crypto/sha1" //nolint:gosec // an opaque case id, not a security boundary
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// LocalizationUsageConfig drives cases built from real agent sessions
// (Claude Code transcripts): the query an agent sent to Idiom, labelled with
// the repository files that agent opened next. Every repository-specific rule
// is data here, so the builder stays generic.
type LocalizationUsageConfig struct {
	// QueryRegex matches a shell command that sends a localization query;
	// group 1 is the rest of the command (the arguments).
	QueryRegex string `json:"query_regex"`
	// PathRootRegex maps an absolute, forward-slashed path to a
	// repository-relative one (group 1); paths it does not match are outside
	// the repository and dropped.
	PathRootRegex string `json:"path_root_regex"`
	// LabelTools are the tool names whose file_path input counts as opening a file.
	LabelTools []string `json:"label_tools"`
	// WindowMinutes bounds the window after a query; the next query in the
	// same session also ends it.
	WindowMinutes int `json:"window_minutes"`
	// MainRef is the first-parent line a query's base commit is taken from:
	// the newest commit on it at or before the query time. Empty means
	// UsageDefaultMainRef.
	MainRef string `json:"main_ref,omitempty"`
	// DedupeJaccard merges queries whose word sets overlap at least this much.
	DedupeJaccard float64 `json:"dedupe_jaccard"`
	// HoldoutFraction is the newest share of cases that becomes the hold-out.
	HoldoutFraction float64 `json:"holdout_fraction"`
	// DropQueryRegex drops a query that would put personal data (an email
	// address, an absolute home path) into the case file, or that is a
	// copied template placeholder rather than a real task.
	DropQueryRegex string `json:"drop_query_regex"`
	// DropBriefNamed also drops a label whose file name appears in user text
	// (the brief) before the query; the query naming it always drops it.
	DropBriefNamed bool `json:"drop_brief_named,omitempty"`
	// WorktreeRegex finds a linked worktree in a forward-slashed path or
	// command; group 1 is its name. Used by the branch base mode.
	WorktreeRegex string `json:"worktree_regex,omitempty"`
	// BranchRefPrefix turns a branch name into the ref that holds it in repo
	// (for a clone of the agents' checkout, "refs/remotes/origin/").
	BranchRefPrefix string `json:"branch_ref_prefix,omitempty"`
}

// UsageDefaultMainRef is the main line of an ordinary clone.
const UsageDefaultMainRef = "refs/remotes/origin/main"

// Usage base modes: where a case's base commit comes from.
const (
	UsageBaseMain   = "main"   // the newest MainRef commit at the query time
	UsageBaseBranch = "branch" // the newest commit of the agent's own branch at the query time
)

// UsageBuildOptions are the per-build inputs that are not repository rules.
type UsageBuildOptions struct {
	Since time.Time
	// SplitCut fixes the dev/hold-out cut; zero derives it from holdout_fraction.
	SplitCut time.Time
	BaseMode string
	// WorktreeBranches maps a linked worktree name to its branch (from
	// `git worktree list --porcelain` of the agents' checkout).
	WorktreeBranches map[string]string
}

// usageEvent is one tool call or one piece of user-written text in a session.
type usageEvent struct {
	at      time.Time
	tool    string // "" for user text
	command string // Bash command
	path    string // file_path input
	text    string // user text
	cwd     string // session working directory the transcript recorded
	branch  string // branch of cwd the transcript recorded
}

// usageQuery is one localization query with its labels, before dedupe.
type usageQuery struct {
	at      time.Time
	session string
	query   string
	opened  []string // repository-relative, in first-open order
	where   usageWhere
}

// usageWhere is the checkout a query ran in, as far as the transcript shows.
type usageWhere struct {
	worktree    string // linked worktree name, "" for the main checkout
	cwdWorktree string // worktree of the session cwd
	cwdBranch   string // branch the transcript recorded for the session cwd
}

// UsageFunnel counts what each build rule removed, for the audit.
type UsageFunnel struct {
	Queries        int // localization query calls found
	DroppedQueries int // drop_query_regex or no query text
	BeforeSince    int
	Merged         int // folded into an earlier near-identical query
	NoLabels       int // nothing opened in the window after the rules
	NoBase         int // labels, but no base commit could be resolved
	NoGold         int // labels, but no source file that exists at base (or too many)
	NoGoldOnlyNew  int // of NoGold: source labels exist, none at base
	Cases          int
	LabelsOpened   int // repository files opened in windows
	LabelsKnown    int // dropped: opened in the session before the query
	LabelsNamed    int // dropped: file name in user text or the query
}

type usageRules struct {
	funnel    *UsageFunnel
	cfg       LocalizationUsageConfig
	query     *regexp.Regexp
	root      *regexp.Regexp
	private   *regexp.Regexp
	worktree  *regexp.Regexp // nil when the config has none
	labelTool map[string]bool
	window    time.Duration
}

var (
	usageArgJSON   = regexp.MustCompile(`'(\{.*?\})'`)
	usageArgQuoted = regexp.MustCompile(`\b(?:task|query)=(?:"([^"]*)"|'([^']*)'|(\S+))`)
	usageWord      = regexp.MustCompile(`[a-z0-9]+`)
)

func compileUsageRules(cfg LocalizationUsageConfig) (usageRules, error) {
	r := usageRules{funnel: &UsageFunnel{}, cfg: cfg, labelTool: map[string]bool{}, window: time.Duration(cfg.WindowMinutes) * time.Minute}

	var err error
	if r.query, err = regexp.Compile(cfg.QueryRegex); err != nil {
		return r, fmt.Errorf("query_regex: %w", err)
	}

	if r.root, err = regexp.Compile(cfg.PathRootRegex); err != nil {
		return r, fmt.Errorf("path_root_regex: %w", err)
	}

	if r.private, err = regexp.Compile(cfg.DropQueryRegex); err != nil {
		return r, fmt.Errorf("drop_query_regex: %w", err)
	}

	if cfg.WorktreeRegex != "" {
		if r.worktree, err = regexp.Compile(cfg.WorktreeRegex); err != nil {
			return r, fmt.Errorf("worktree_regex: %w", err)
		}
	}

	for _, t := range cfg.LabelTools {
		r.labelTool[t] = true
	}

	if r.window <= 0 || cfg.HoldoutFraction <= 0 || cfg.HoldoutFraction >= 1 {
		return r, errors.New("usage config needs window_minutes > 0 and 0 < holdout_fraction < 1")
	}

	return r, nil
}

// usageQueryText pulls the task or query text out of the command arguments:
// the JSON form '{"task": ...}' or the key=value form task="...".
func usageQueryText(args string) string {
	if m := usageArgJSON.FindStringSubmatch(args); m != nil {
		var o struct {
			Task  string `json:"task"`
			Query string `json:"query"`
		}

		if json.Unmarshal([]byte(m[1]), &o) == nil {
			return strings.TrimSpace(o.Task + o.Query)
		}
	}

	if m := usageArgQuoted.FindStringSubmatch(args); m != nil {
		return strings.TrimSpace(m[1] + m[2] + m[3])
	}

	return ""
}

// repoRelative maps a tool's file path to a repository-relative path, or "".
func (r usageRules) repoRelative(p string) string {
	p = filepath.ToSlash(p)
	if m := r.root.FindStringSubmatch(p); m != nil {
		return m[1]
	}

	return ""
}

// readUsageSession streams one transcript. Lines are parsed only when they can
// carry a tool call or user text; tool results (often megabytes) are skipped,
// since the label rules never read them.
func readUsageSession(path string) ([]usageEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var events []usageEvent

	br := bufio.NewReaderSize(f, 1<<20)

	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			events = append(events, parseUsageLine(line)...)
		}

		if errors.Is(err, io.EOF) {
			return events, nil
		}

		if err != nil {
			return events, err
		}
	}
}

// usageMaxLineBytes skips huge lines unparsed: they are tool results (file
// dumps, logs), which carry neither a tool call nor user-written text.
const usageMaxLineBytes = 4 << 20

func parseUsageLine(line []byte) []usageEvent {
	if len(line) > usageMaxLineBytes {
		return nil
	}

	var rec struct {
		Type      string    `json:"type"`
		Timestamp time.Time `json:"timestamp"`
		Cwd       string    `json:"cwd"`
		GitBranch string    `json:"gitBranch"`
		Message   struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}

	if json.Unmarshal(line, &rec) != nil || rec.Timestamp.IsZero() {
		return nil
	}

	var text string
	if json.Unmarshal(rec.Message.Content, &text) == nil {
		if rec.Type == "user" {
			return []usageEvent{{at: rec.Timestamp, text: text}}
		}

		return nil
	}

	var items []struct {
		Type  string `json:"type"`
		Text  string `json:"text"`
		Name  string `json:"name"`
		Input struct {
			Command  string `json:"command"`
			FilePath string `json:"file_path"`
		} `json:"input"`
	}

	if json.Unmarshal(rec.Message.Content, &items) != nil {
		return nil
	}

	var out []usageEvent

	for _, it := range items {
		switch {
		case rec.Type == "user" && it.Type == "text":
			out = append(out, usageEvent{at: rec.Timestamp, text: it.Text})
		case rec.Type == "assistant" && it.Type == "tool_use":
			out = append(out, usageEvent{at: rec.Timestamp, tool: it.Name, command: it.Input.Command, path: it.Input.FilePath, cwd: rec.Cwd, branch: rec.GitBranch})
		}
	}

	return out
}

// sessionQueries applies the label rules to one session (see the AUDIT for the
// rule list): a file counts for a query when the session opened it inside the
// query's window, had not opened it before the query, and no user text or the
// query itself named its file name before.
func (r usageRules) sessionQueries(session string, events []usageEvent) []usageQuery {
	var out []usageQuery

	openedBefore := map[string]bool{}

	var userText strings.Builder

	for i, e := range events {
		if e.tool == "" {
			userText.WriteString(strings.ToLower(e.text))
			userText.WriteByte('\n')

			continue
		}

		if r.labelTool[e.tool] {
			if rel := r.repoRelative(e.path); rel != "" {
				openedBefore[rel] = true
			}

			continue
		}

		m := r.query.FindStringSubmatch(e.command)
		if e.tool != "Bash" || m == nil {
			continue
		}

		r.funnel.Queries++

		q := usageQueryText(m[1])
		if q == "" || r.private.MatchString(q) {
			r.funnel.DroppedQueries++

			continue
		}

		named := strings.ToLower(q)
		if r.cfg.DropBriefNamed {
			named = userText.String() + "\n" + named
		}

		out = append(out, usageQuery{at: e.at, session: session, query: q, opened: r.windowLabels(events[i+1:], e.at, openedBefore, named), where: r.whereOf(e)})
	}

	return out
}

// whereOf names the checkout a query ran in: a worktree the command cds into
// wins over the session cwd, since orchestrator sessions run from the main
// checkout and cd into a maker's worktree per command.
func (r usageRules) whereOf(e usageEvent) usageWhere {
	w := usageWhere{cwdWorktree: r.worktreeIn(e.cwd), cwdBranch: e.branch}

	w.worktree = r.worktreeIn(e.command)
	if w.worktree == "" && !strings.Contains(e.command, "cd ") {
		w.worktree = w.cwdWorktree
	}

	return w
}

func (r usageRules) worktreeIn(s string) string {
	if r.worktree == nil {
		return ""
	}

	if m := r.worktree.FindStringSubmatch(filepath.ToSlash(s)); m != nil {
		return m[1]
	}

	return ""
}

// UsageWorktreeBranches reads `git worktree list --porcelain` output into a
// worktree name to branch map, naming each worktree by worktree_regex.
func UsageWorktreeBranches(porcelain string, usage LocalizationUsageConfig) (map[string]string, error) {
	re, err := regexp.Compile(usage.WorktreeRegex)
	if err != nil || usage.WorktreeRegex == "" {
		return nil, fmt.Errorf("worktree_regex %q: %w", usage.WorktreeRegex, err)
	}

	out := map[string]string{}
	name := ""

	for _, line := range strings.Split(strings.ReplaceAll(porcelain, "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			name = ""
			if m := re.FindStringSubmatch(filepath.ToSlash(strings.TrimPrefix(line, "worktree ")) + "/"); m != nil {
				name = m[1]
			}
		case strings.HasPrefix(line, "branch ") && name != "":
			out[name] = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}

	return out, nil
}

// usageBranch is the branch a query's checkout had, or "" when the
// transcript cannot tell: a linked worktree's branch comes from the worktree
// list (or the recorded branch when the session ran inside that worktree);
// the main checkout's from the recorded branch.
func usageBranch(w usageWhere, worktreeBranches map[string]string) string {
	if w.worktree == "" {
		if w.cwdWorktree == "" {
			return w.cwdBranch
		}

		return ""
	}

	if b := worktreeBranches[w.worktree]; b != "" {
		return b
	}

	if w.cwdWorktree == w.worktree {
		return w.cwdBranch
	}

	return ""
}

func (r usageRules) windowLabels(after []usageEvent, at time.Time, openedBefore map[string]bool, named string) []string {
	var opened []string

	seen := map[string]bool{}

	for _, n := range after {
		if n.at.Sub(at) > r.window || (n.tool == "Bash" && r.query.MatchString(n.command)) {
			break
		}

		if !r.labelTool[n.tool] {
			continue
		}

		rel := r.repoRelative(n.path)
		if rel == "" || seen[rel] {
			continue
		}

		seen[rel] = true
		r.funnel.LabelsOpened++

		switch {
		case openedBefore[rel]:
			r.funnel.LabelsKnown++

			continue
		case strings.Contains(named, strings.ToLower(filepath.Base(rel))):
			r.funnel.LabelsNamed++

			continue
		}

		opened = append(opened, rel)
	}

	return opened
}

func usageWords(q string) map[string]bool {
	w := map[string]bool{}
	for _, t := range usageWord.FindAllString(strings.ToLower(q), -1) {
		w[t] = true
	}

	return w
}

func jaccard(a, b map[string]bool) float64 {
	inter := 0

	for t := range a {
		if b[t] {
			inter++
		}
	}

	union := len(a) + len(b) - inter
	if union == 0 {
		return 1
	}

	return float64(inter) / float64(union)
}

// dedupeUsageQueries merges near-identical queries (word-set Jaccard at least
// the threshold) into the earliest one, which keeps its time and gains the
// others' labels: repeated runs of one query (a re-run with a different pipe,
// a retried brief) are one case, not several.
func dedupeUsageQueries(qs []usageQuery, threshold float64) []usageQuery {
	sort.SliceStable(qs, func(i, j int) bool { return qs[i].at.Before(qs[j].at) })

	var out []usageQuery

	var words []map[string]bool

	for _, q := range qs {
		w := usageWords(q.query)
		merged := false

		for k := range out {
			if jaccard(words[k], w) >= threshold {
				for _, p := range q.opened {
					if !slices.Contains(out[k].opened, p) {
						out[k].opened = append(out[k].opened, p)
					}
				}

				merged = true

				break
			}
		}

		if !merged {
			out = append(out, q)
			words = append(words, w)
		}
	}

	return out
}

// BuildLocalizationUsageCases turns transcripts under dirs (sessions modified
// at or after opts.Since) into cases. A case's base is the newest commit at the
// query time on MainRef, or in branch mode on the agent's own branch (cases
// whose branch the transcript cannot tell, or whose branch ref is gone, are
// dropped). Labels that exist at base are Gold, source files written later are
// GoldNew. Cases are split by time at opts.SplitCut, or with the newest
// HoldoutFraction as hold-out. It returns the cases and the split cut.
func BuildLocalizationUsageCases(repo string, dirs []string, cfg LocalizationEvalBuilderConfig, usage LocalizationUsageConfig, opts UsageBuildOptions) ([]LocalizationEvalCase, time.Time, UsageFunnel, error) {
	if usage.MainRef == "" {
		usage.MainRef = UsageDefaultMainRef
	}

	rules, err := compileUsageRules(usage)
	if err != nil {
		return nil, time.Time{}, UsageFunnel{}, err
	}

	since := opts.Since
	if opts.BaseMode == UsageBaseBranch && (rules.worktree == nil || usage.BranchRefPrefix == "") {
		return nil, time.Time{}, UsageFunnel{}, errors.New("branch base mode needs worktree_regex and branch_ref_prefix")
	}

	funnel := rules.funnel

	var qs []usageQuery

	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
				return err
			}

			if info, err := d.Info(); err != nil || info.ModTime().Before(since) {
				return err
			}

			events, err := readUsageSession(p)
			if err != nil {
				return fmt.Errorf("%s: %w", p, err)
			}

			sum := sha1.Sum([]byte(p)) //nolint:gosec // opaque session id
			qs = append(qs, rules.sessionQueries(hex.EncodeToString(sum[:4]), events)...)

			return nil
		})
		if err != nil {
			return nil, time.Time{}, *funnel, err
		}
	}

	n := len(qs)
	qs = slices.DeleteFunc(qs, func(q usageQuery) bool { return q.at.Before(since) })
	funnel.BeforeSince = n - len(qs)
	n = len(qs)
	qs = dedupeUsageQueries(qs, usage.DedupeJaccard)
	funnel.Merged = n - len(qs)

	for _, q := range qs {
		if len(q.opened) == 0 {
			funnel.NoLabels++
		}
	}

	resolve := func(q usageQuery) (string, error) {
		ref := usage.MainRef
		if opts.BaseMode == UsageBaseBranch {
			branch := usageBranch(q.where, opts.WorktreeBranches)
			if branch == "" {
				return "", nil
			}

			ref = usage.BranchRefPrefix + branch
			if _, err := gitOut(repo, "rev-parse", "--verify", "--quiet", ref); err != nil {
				return "", nil //nolint:nilerr // the branch is gone: the case cannot be resolved
			}
		}

		base, err := gitOut(repo, "rev-list", "-1", "--first-parent", "--before="+strconv.FormatInt(q.at.Unix(), 10), ref)

		return strings.TrimSpace(base), err
	}

	cases, err := usageCasesAtBase(repo, qs, cfg, resolve, funnel)
	if err != nil {
		return nil, time.Time{}, *funnel, err
	}

	funnel.Cases = len(cases)
	funnel.NoGold = len(qs) - funnel.NoLabels - funnel.NoBase - len(cases)

	cut := opts.SplitCut
	if cut.IsZero() {
		cut = usageHoldoutCut(cases, usage.HoldoutFraction)
	}

	splitUsageCasesAt(cases, cut)

	return cases, cut, *funnel, nil
}

func usageCasesAtBase(repo string, qs []usageQuery, cfg LocalizationEvalBuilderConfig, resolve func(usageQuery) (string, error), funnel *UsageFunnel) ([]LocalizationEvalCase, error) {
	source, err := regexp.Compile(cfg.SourceRegex)
	if err != nil {
		return nil, fmt.Errorf("source_regex: %w", err)
	}

	excludes, err := compileAll(cfg.ExcludeGold)
	if err != nil {
		return nil, err
	}

	testRe, docRe := optionalRegexp(cfg.TestRegex), optionalRegexp(cfg.DocRegex)
	trees := map[string]map[string]bool{}

	var cases []LocalizationEvalCase

	for _, q := range qs {
		if len(q.query) < max(cfg.MinQueryLen, 1) || len(q.opened) == 0 {
			continue
		}

		base, err := resolve(q)
		if err != nil {
			return nil, err
		}

		if base == "" {
			funnel.NoBase++

			continue
		}

		if trees[base] == nil {
			if trees[base], err = gitTreeFiles(repo, base); err != nil {
				return nil, err
			}
		}

		c := usageCase(q, base)

		for _, p := range q.opened {
			switch {
			case testRe != nil && testRe.MatchString(p):
				c.GoldTests = append(c.GoldTests, p)
			case docRe != nil && docRe.MatchString(p):
				c.GoldDocs = append(c.GoldDocs, p)
			case !source.MatchString(p) || matchesAny(excludes, p):
			case trees[base][p]:
				c.Gold = append(c.Gold, p)
			default:
				c.GoldNew = append(c.GoldNew, p)
			}
		}

		slices.Sort(c.Gold)

		if len(c.Gold) == 0 || (cfg.MaxGoldFiles > 0 && len(c.Gold) > cfg.MaxGoldFiles) {
			if len(c.Gold) == 0 && len(c.GoldNew) > 0 {
				funnel.NoGoldOnlyNew++
			}

			continue
		}

		cases = append(cases, c)
	}

	return cases, nil
}

func usageCase(q usageQuery, base string) LocalizationEvalCase {
	at := q.at.UTC().Format(time.RFC3339)
	sum := sha1.Sum([]byte(q.query + "\x00" + base + "\x00" + at)) //nolint:gosec // opaque case id
	id := hex.EncodeToString(sum[:])

	return LocalizationEvalCase{ID: id[:9], Merge: id, Base: base, Query: q.query, QuerySource: "usage session " + q.session, Time: at}
}

func gitTreeFiles(repo, rev string) (map[string]bool, error) {
	out, err := gitOut(repo, "ls-tree", "-r", "--name-only", rev)
	if err != nil {
		return nil, err
	}

	files := map[string]bool{}
	for _, p := range strings.Split(out, "\n") {
		if p != "" {
			files[p] = true
		}
	}

	return files, nil
}

// usageHoldoutCut is the time of the first case of the newest holdout share
// (cases at or after it are hold-out); cases at one timestamp stay on one side.
func usageHoldoutCut(cases []LocalizationEvalCase, holdout float64) time.Time {
	sort.SliceStable(cases, func(i, j int) bool { return cases[i].Time < cases[j].Time })

	if len(cases) == 0 {
		return time.Time{}
	}

	first := len(cases) - int(float64(len(cases))*holdout+0.5)
	first = min(max(first, 0), len(cases)-1)

	for first > 0 && cases[first-1].Time == cases[first].Time {
		first--
	}

	cut, _ := time.Parse(time.RFC3339, cases[first].Time)

	return cut
}

// splitUsageCasesAt orders cases by time and marks those before cut "dev".
func splitUsageCasesAt(cases []LocalizationEvalCase, cut time.Time) {
	sort.SliceStable(cases, func(i, j int) bool { return cases[i].Time < cases[j].Time })

	for i := range cases {
		cases[i].Split = localizationTimeSplit(cases[i].Time, cut)
	}
}

func localizationTimeSplit(at string, cut time.Time) string {
	t, err := time.Parse(time.RFC3339, at)
	if err == nil && t.Before(cut) {
		return "dev"
	}

	return "holdout"
}
