// idiom-analyze scans session JSONL logs, clusters generic tool-call shapes,
// and proposes (a) new discovery-redirect rules that would route them to an
// existing deterministic tool and (b) candidate new deterministic tools for
// shapes no capability covers. Output is a human-readable markdown report.
//
// An optional single LLM review pass (via the local llama-server) reads the
// deterministic findings plus compressed per-session traces and names
// patterns the rule-miner missed. One call per run, off any hot path.
package main

import (
	"cmp"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"idiom/internal/config"
	"idiom/internal/llama"
	"idiom/internal/util"
)

// clusterThreshold is the fixed low bar: a shape must occur at least this
// many times across the corpus (or in a single session) before it earns a
// proposal line.
const clusterThreshold = 3

type logEntry struct {
	TS      string          `json:"ts"`
	Kind    string          `json:"kind"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type toolRequestPayload struct {
	Turn int    `json:"turn"`
	Step int    `json:"step"`
	Tool string `json:"tool"`
	Raw  string `json:"raw"`
}

type toolResultPayload struct {
	Turn    int    `json:"turn"`
	Step    int    `json:"step"`
	Tool    string `json:"tool"`
	Summary string `json:"summary"`
	Error   string `json:"error"`
}

type toolCall struct {
	Session string
	Turn    int
	Step    int
	Tool    string
	Raw     string
	// Parsed fields from Raw (best-effort — only fields we care about).
	Pattern   string
	Path      string
	Glob      string
	Command   string
	StartLine int
	EndLine   int
	// Shape is the classification bucket, e.g. "go_func_def" or "grep -r".
	Shape string
	// Capability is the deterministic capability (if any) that would
	// satisfy this shape. Empty means "no existing tool fits — candidate
	// for a new one."
	Capability string
	// Language is the detected language hint for the call, if any.
	Language string
}

type shapeCluster struct {
	Shape      string
	Capability string
	Language   string
	Count      int
	Sessions   map[string]int
	Examples   []string
}

func main() {
	var (
		logsGlob    = flag.String("logs", "logs/e2e/*.jsonl", "glob of session JSONL files to analyze")
		outPath     = flag.String("out", "", "markdown output path (default: docs/rule-proposals-<ts>.md)")
		useLLM      = flag.Bool("llm", false, "additionally run a single local LLM review pass for novel-pattern detection")
		cfgPath     = flag.String("config", "config.json", "path to config.json (only used with -llm)")
		maxExamples = flag.Int("max-examples", 4, "max raw examples per cluster in the report")
	)
	flag.Parse()

	files, err := filepath.Glob(*logsGlob)
	if err != nil {
		fatalf("glob %s: %v", *logsGlob, err)
	}
	if len(files) == 0 {
		fatalf("no log files match %q", *logsGlob)
	}
	slices.Sort(files)

	var calls []toolCall
	sessionSummaries := make(map[string]*sessionSummary)
	for _, f := range files {
		session := filepath.Base(f)
		summary := &sessionSummary{Name: session}
		sessionSummaries[session] = summary

		fileCalls, err := parseSession(f, session, summary)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warn: %s: %v\n", f, err)
			continue
		}
		calls = append(calls, fileCalls...)
	}

	if len(calls) == 0 {
		fatalf("no tool_request entries found in %d log files", len(files))
	}

	for i := range calls {
		classify(&calls[i])
	}

	clusters := clusterByShape(calls)
	existing, novel := splitClusters(clusters)

	ts := time.Now().Format("20060102-150405")
	if strings.TrimSpace(*outPath) == "" {
		_ = os.MkdirAll("docs", 0o755)
		*outPath = filepath.Join("docs", "rule-proposals-"+ts+".md")
	}

	report := renderReport(reportInputs{
		LogsGlob:    *logsGlob,
		Files:       files,
		Calls:       calls,
		Sessions:    sessionSummaries,
		Existing:    existing,
		Novel:       novel,
		MaxExamples: *maxExamples,
	})

	if *useLLM {
		note, err := llmReview(*cfgPath, calls, sessionSummaries, existing, novel)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warn: llm review failed: %v\n", err)
			report += "\n## LLM review\n\n_LLM review requested but failed: " + err.Error() + "_\n"
		} else {
			report += "\n## LLM review (single pass)\n\n" + note + "\n"
		}
	}

	if err := os.WriteFile(*outPath, []byte(report), 0o644); err != nil {
		fatalf("write %s: %v", *outPath, err)
	}
	fmt.Printf("wrote %s (%d calls, %d existing-capability clusters, %d novel clusters)\n",
		*outPath, len(calls), len(existing), len(novel))
}

type sessionSummary struct {
	Name        string
	TotalCalls  int
	Generic     int // count of generic tool calls (search_content, read_file, shell)
	Structured  int // count of go_*/cpp_* calls
	Redirects   int // count of run_event Role=discovery_redirect
	FirstPrompt string
}

func parseSession(path, session string, summary *sessionSummary) ([]toolCall, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var calls []toolCall
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry logEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		kind := strings.TrimSpace(entry.Kind)
		if kind == "" {
			kind = strings.TrimSpace(entry.Type)
		}
		payload := entry.Payload
		if len(payload) == 0 {
			payload = []byte(line)
		}
		switch kind {
		case "message":
			if summary.FirstPrompt == "" {
				var p struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				}
				if json.Unmarshal(payload, &p) == nil && p.Role == "user" {
					summary.FirstPrompt = p.Content
				}
			}
		case "user_turn":
			if summary.FirstPrompt == "" {
				var p struct {
					Prompt string `json:"prompt"`
				}
				_ = json.Unmarshal(payload, &p)
				summary.FirstPrompt = p.Prompt
			}
		case "tool_request":
			var p toolRequestPayload
			if err := json.Unmarshal(payload, &p); err != nil {
				continue
			}
			summary.TotalCalls++
			tc := toolCall{
				Session: session,
				Turn:    p.Turn,
				Step:    p.Step,
				Tool:    p.Tool,
				Raw:     p.Raw,
			}
			parseRaw(&tc)
			if isStructuredTool(tc.Tool) {
				summary.Structured++
			} else {
				summary.Generic++
			}
			calls = append(calls, tc)
		case "run_event":
			var p struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			}
			_ = json.Unmarshal(payload, &p)
			if p.Role == "discovery_redirect" {
				summary.Redirects++
			}
		}
	}
	return calls, nil
}

func parseRaw(tc *toolCall) {
	if strings.TrimSpace(tc.Raw) == "" {
		return
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(tc.Raw), &m); err != nil {
		return
	}
	if v, ok := m["pattern"].(string); ok {
		tc.Pattern = v
	}
	if v, ok := m["path"].(string); ok {
		tc.Path = v
	}
	if v, ok := m["glob"].(string); ok {
		tc.Glob = v
	}
	if v, ok := m["command"].(string); ok {
		tc.Command = v
	}
	if v, ok := m["start_line"].(float64); ok {
		tc.StartLine = int(v)
	}
	if v, ok := m["end_line"].(float64); ok {
		tc.EndLine = int(v)
	}
}

// classify attaches a Shape, Capability, and Language to the call based on
// deterministic regex / lexical heuristics. Capability is set only when
// an existing deterministic tool cleanly covers the shape.
func classify(tc *toolCall) {
	tool := strings.ToLower(strings.TrimSpace(tc.Tool))
	switch tool {
	case "search_content":
		classifySearch(tc)
	case "read_file":
		classifyRead(tc)
	case "shell":
		classifyShell(tc)
	case "search_files", "list_files", "pwd", "task_ledger", "task", "git_status", "git_diff":
		// Baseline tools — already structured, nothing to propose.
		tc.Shape = "baseline:" + tool
	default:
		if isStructuredTool(tool) {
			tc.Shape = "structured:" + tool
		} else {
			tc.Shape = "other:" + tool
		}
	}
}

var (
	reGoFuncDef       = regexp.MustCompile(`^func\s+[A-Za-z_]\w*`)
	reGoFuncMethodDef = regexp.MustCompile(`^func\s*\([^)]*\)\s*[A-Za-z_]\w*`)
	reGoTypeDef       = regexp.MustCompile(`^type\s+[A-Za-z_]\w*`)
	// reGoFuncWildcard catches `func.*Handler` / `func.*\s*Foo` — the
	// shape an LLM writes when it's trying to locate a definition but
	// doesn't know the exact signature. These slip past looksLikeIdentifier
	// because of the wildcard, so the current redirect misses them.
	reGoFuncWildcard = regexp.MustCompile(`^func[.\\\s]*[*+]?\s*[A-Za-z_]\w*`)
	reCppClassDef    = regexp.MustCompile(`^(class|struct)\s+[A-Za-z_]\w*`)
	reCppFuncDef     = regexp.MustCompile(`^[A-Za-z_][\w:]*\s+[A-Za-z_]\w*\s*\(`)
	reBareIdent      = regexp.MustCompile(`^[A-Za-z_][\w:]*$`)
	reRegexMeta      = regexp.MustCompile(`\\[bBdDsSwW]|\.[*+?]|\([^)]*[|*+?]|\[[^\]]+\]`)
)

func classifySearch(tc *toolCall) {
	p := strings.TrimSpace(tc.Pattern)
	lang := langFromGlob(tc.Glob)
	tc.Language = lang
	switch {
	case reGoFuncMethodDef.MatchString(p):
		tc.Shape = "search:func_method_def"
		if tc.Language == "" {
			tc.Language = "go"
		}
		tc.Capability = capForLangSymbol(tc.Language)
	case reGoFuncDef.MatchString(p):
		tc.Shape = "search:func_def"
		if tc.Language == "" {
			tc.Language = "go"
		}
		tc.Capability = capForLangSymbol(tc.Language)
	case reGoFuncWildcard.MatchString(p):
		tc.Shape = "search:func_wildcard"
		// `func.*X` is Go syntax regardless of whether a glob was set —
		// no other registered language uses `func ` as a keyword prefix.
		if tc.Language == "" {
			tc.Language = "go"
		}
		tc.Capability = capForLangSymbol(tc.Language)
	case reGoTypeDef.MatchString(p):
		tc.Shape = "search:type_def"
		if tc.Language == "" {
			tc.Language = "go"
		}
		tc.Capability = capForLangSymbol(tc.Language)
	case reCppClassDef.MatchString(p):
		tc.Shape = "search:cpp_class_def"
		tc.Capability = capForLangSymbol("cpp")
		if tc.Language == "" {
			tc.Language = "cpp"
		}
	case strings.Contains(p, `\b`):
		tc.Shape = "search:regex_word_boundary"
		tc.Capability = capForLangSymbol(lang)
	case reBareIdent.MatchString(p):
		tc.Shape = "search:bare_identifier"
		tc.Capability = capForLangSymbol(lang)
	case strings.ContainsAny(p, " \t"):
		tc.Shape = "search:whitespace_phrase"
	case reRegexMeta.MatchString(p):
		tc.Shape = "search:complex_regex"
	default:
		tc.Shape = "search:other"
	}
}

func classifyRead(tc *toolCall) {
	lang := langFromPath(tc.Path)
	tc.Language = lang
	span := tc.EndLine - tc.StartLine
	switch {
	case tc.StartLine == 0 && tc.EndLine == 0:
		tc.Shape = "read:no_range"
		if lang != "" {
			tc.Capability = "file_outline"
		}
	case tc.StartLine == 0 && tc.EndLine > 0:
		tc.Shape = "read:one_sided_head"
	case tc.StartLine > 0 && tc.EndLine == 0:
		tc.Shape = "read:one_sided_tail"
	case span > 200:
		tc.Shape = "read:wide_range"
	default:
		tc.Shape = "read:targeted"
	}
}

func classifyShell(tc *toolCall) {
	cmd := strings.TrimSpace(tc.Command)
	if cmd == "" {
		tc.Shape = "shell:empty"
		return
	}
	fields := strings.Fields(cmd)
	head := fields[0]
	sub := ""
	if len(fields) > 1 {
		sub = fields[1]
	}
	// Normalize well-known two-token prefixes; single-token otherwise.
	switch head {
	case "grep", "rg":
		if strings.HasPrefix(sub, "-") {
			tc.Shape = "shell:" + head + " " + sub
		} else {
			tc.Shape = "shell:" + head
		}
		tc.Capability = "workspace_symbol_or_references"
	case "git":
		tc.Shape = "shell:git " + sub
	case "find":
		tc.Shape = "shell:find"
		tc.Capability = "related_files_or_search_files"
	case "wc":
		tc.Shape = "shell:wc"
	case "head", "tail":
		tc.Shape = "shell:" + head
		tc.Capability = "file_outline_or_read_symbol"
	case "cat":
		tc.Shape = "shell:cat"
		tc.Capability = "file_outline_or_read_file"
	case "go":
		tc.Shape = "shell:go " + sub
		if sub == "test" || sub == "vet" || sub == "build" {
			tc.Capability = "diagnostics"
		}
	case "ls":
		tc.Shape = "shell:ls"
		tc.Capability = "list_files"
	case "awk", "sed", "cut", "sort", "uniq":
		tc.Shape = "shell:" + head
	default:
		tc.Shape = "shell:" + head
	}
}

func langFromGlob(glob string) string {
	glob = strings.ToLower(strings.TrimSpace(glob))
	if glob == "" {
		return ""
	}
	if strings.Contains(glob, ".go") {
		return "go"
	}
	for _, ext := range []string{".cpp", ".cc", ".cxx", ".c", ".h", ".hpp", ".hh", ".hxx"} {
		if strings.Contains(glob, ext) {
			return "cpp"
		}
	}
	return ""
}

func langFromPath(path string) string {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(path)))
	switch ext {
	case ".go":
		return "go"
	case ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".ipp", ".inl", ".tpp":
		return "cpp"
	}
	return ""
}

func capForLangSymbol(lang string) string {
	if lang == "" {
		return ""
	}
	return "workspace_symbol_or_find_symbol"
}

func isStructuredTool(tool string) bool {
	t := strings.ToLower(strings.TrimSpace(tool))
	return strings.HasPrefix(t, "go_") || strings.HasPrefix(t, "cpp_")
}

func clusterByShape(calls []toolCall) []shapeCluster {
	byShape := make(map[string]*shapeCluster)
	for _, c := range calls {
		if c.Shape == "" {
			continue
		}
		// Skip already-structured and baseline calls — those aren't
		// candidates for new rules or new tools.
		if strings.HasPrefix(c.Shape, "structured:") || strings.HasPrefix(c.Shape, "baseline:") {
			continue
		}
		// read:targeted is the desired path (bounded read after an
		// outline) — nothing to propose, so drop it from clustering.
		if c.Shape == "read:targeted" {
			continue
		}
		key := c.Shape + "|" + c.Language
		cl, ok := byShape[key]
		if !ok {
			cl = &shapeCluster{
				Shape:      c.Shape,
				Capability: c.Capability,
				Language:   c.Language,
				Sessions:   make(map[string]int),
			}
			byShape[key] = cl
		}
		cl.Count++
		cl.Sessions[c.Session]++
		if len(cl.Examples) < 12 {
			cl.Examples = append(cl.Examples, truncate(c.Raw, 180))
		}
	}
	var out []shapeCluster
	for _, cl := range byShape {
		if !clusterMeetsThreshold(cl) {
			continue
		}
		out = append(out, *cl)
	}
	slices.SortFunc(out, func(a, b shapeCluster) int {
		return cmp.Or(
			-cmp.Compare(a.Count, b.Count),
			cmp.Compare(a.Shape, b.Shape),
		)
	})
	return out
}

func clusterMeetsThreshold(cl *shapeCluster) bool {
	if cl.Count >= clusterThreshold {
		return true
	}
	for _, n := range cl.Sessions {
		if n >= clusterThreshold {
			return true
		}
	}
	return false
}

func splitClusters(all []shapeCluster) (existing, novel []shapeCluster) {
	// If a shape has a capability-bearing variant (e.g. with a language),
	// treat its langless twin as "same finding, missing hint" and keep
	// it in the existing bucket with a note — not a separate novelty.
	capByShape := make(map[string]string)
	for _, cl := range all {
		if cl.Capability != "" && capByShape[cl.Shape] == "" {
			capByShape[cl.Shape] = cl.Capability
		}
	}
	for _, cl := range all {
		if cl.Capability == "" {
			if cap, ok := capByShape[cl.Shape]; ok {
				cl.Capability = cap + " (langless — needs inference)"
				existing = append(existing, cl)
				continue
			}
		}
		if strings.TrimSpace(cl.Capability) != "" {
			existing = append(existing, cl)
		} else {
			novel = append(novel, cl)
		}
	}
	return
}

type reportInputs struct {
	LogsGlob    string
	Files       []string
	Calls       []toolCall
	Sessions    map[string]*sessionSummary
	Existing    []shapeCluster
	Novel       []shapeCluster
	MaxExamples int
}

func renderReport(in reportInputs) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Discovery redirect rule proposals\n\n")
	fmt.Fprintf(&b, "_Generated %s from `%s` (%d sessions, %d tool calls)._\n\n",
		time.Now().Format(time.RFC3339), in.LogsGlob, len(in.Files), len(in.Calls))

	// Session roster
	b.WriteString("## Sessions analyzed\n\n")
	b.WriteString("| Session | Total calls | Structured | Generic | Redirects fired | Deterministic % |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|\n")
	var sessionNames []string
	for name := range in.Sessions {
		sessionNames = append(sessionNames, name)
	}
	slices.Sort(sessionNames)
	for _, name := range sessionNames {
		s := in.Sessions[name]
		pct := 0.0
		if s.TotalCalls > 0 {
			pct = 100 * float64(s.Structured) / float64(s.TotalCalls)
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %.0f%% |\n",
			name, s.TotalCalls, s.Structured, s.Generic, s.Redirects, pct)
	}
	b.WriteString("\n")

	// Overall shape breakdown (all clusters, not just ≥threshold)
	fmt.Fprintf(&b, "## Clusters meeting threshold (≥%d across corpus or in one session)\n\n", clusterThreshold)

	b.WriteString("### 1. Existing-capability clusters — candidates for new redirect rules\n\n")
	if len(in.Existing) == 0 {
		b.WriteString("_No existing-capability clusters above threshold._\n\n")
	} else {
		for _, cl := range in.Existing {
			renderCluster(&b, cl, in.MaxExamples, true)
		}
	}

	b.WriteString("### 2. Novel clusters — candidates for new deterministic tools\n\n")
	if len(in.Novel) == 0 {
		b.WriteString("_No novel clusters above threshold._\n\n")
	} else {
		for _, cl := range in.Novel {
			renderCluster(&b, cl, in.MaxExamples, false)
		}
	}

	// Proposal summary (one-liners)
	b.WriteString("## Proposal summary\n\n")
	for _, cl := range in.Existing {
		fmt.Fprintf(&b, "- **Add redirect rule** for `%s` (lang=%s, %d hits) → capability `%s`. %s\n",
			cl.Shape, orDash(cl.Language), cl.Count, cl.Capability, proposalRationale(cl))
	}
	for _, cl := range in.Novel {
		fmt.Fprintf(&b, "- **Consider new discovery tool** covering `%s` (%d hits). %s\n",
			cl.Shape, cl.Count, noveltyRationale(cl))
	}
	if len(in.Existing)+len(in.Novel) == 0 {
		b.WriteString("_Nothing above threshold — corpus is either small or routing is already clean._\n")
	}
	b.WriteString("\n")

	return b.String()
}

func renderCluster(b *strings.Builder, cl shapeCluster, maxExamples int, existing bool) {
	fmt.Fprintf(b, "#### `%s`", cl.Shape)
	if cl.Language != "" {
		fmt.Fprintf(b, " (lang=%s)", cl.Language)
	}
	fmt.Fprintf(b, " — %d hits across %d session(s)\n\n", cl.Count, len(cl.Sessions))
	if existing {
		fmt.Fprintf(b, "- Suggested capability: `%s`\n", cl.Capability)
		fmt.Fprintf(b, "- Rationale: %s\n", proposalRationale(cl))
	} else {
		fmt.Fprintf(b, "- No existing capability clearly fits.\n")
		fmt.Fprintf(b, "- Rationale: %s\n", noveltyRationale(cl))
	}
	if len(cl.Sessions) > 0 {
		var pairs []string
		for name, n := range cl.Sessions {
			pairs = append(pairs, fmt.Sprintf("%s×%d", name, n))
		}
		slices.Sort(pairs)
		fmt.Fprintf(b, "- By session: %s\n", strings.Join(pairs, ", "))
	}
	if len(cl.Examples) > 0 {
		b.WriteString("- Examples:\n")
		n := maxExamples
		if n > len(cl.Examples) {
			n = len(cl.Examples)
		}
		for _, ex := range cl.Examples[:n] {
			fmt.Fprintf(b, "  - `%s`\n", ex)
		}
	}
	b.WriteString("\n")
}

func proposalRationale(cl shapeCluster) string {
	switch cl.Shape {
	case "search:func_def", "search:func_method_def", "search:type_def", "search:cpp_class_def":
		return "declaration-shape regex is a symbol-name query in disguise — extract the identifier and route to the symbol capability."
	case "search:func_wildcard":
		return "`func.*X` is the LLM's way of asking 'where is X defined?' — strip the wildcard, extract the trailing identifier, and redirect to workspace_symbol/find_symbol."
	case "search:regex_word_boundary":
		return "`\\b` regexes are almost always `find_symbol` queries; strip the boundary and redirect."
	case "search:bare_identifier":
		return "bare identifier with a language-specific glob is the canonical workspace_symbol query."
	case "read:no_range":
		return "unbounded reads on a code file should route to file_outline first; model can follow up with a bounded read."
	case "shell:grep", "shell:grep -r", "shell:grep -rn", "shell:rg":
		return "raw grep is the exact query workspace_symbol / references were built to replace."
	case "shell:find":
		return "find-by-name is list_files/search_files territory; redirect eliminates shell permission friction."
	case "shell:cat":
		return "cat ≈ read_file or file_outline; no reason to shell out."
	case "shell:head", "shell:tail":
		return "head/tail are narrow range reads; redirect to file_outline or bounded read_file."
	case "shell:go test", "shell:go vet", "shell:go build":
		return "go_diagnostics already answers this and is structured."
	}
	return "shape is deterministic enough to match a rule; promoting it would cut generic-tool noise."
}

func noveltyRationale(cl shapeCluster) string {
	switch {
	case strings.HasPrefix(cl.Shape, "shell:git "):
		return "no git-aware tool exists; a structured git_log/git_diff/git_blame capability would pay off."
	case cl.Shape == "shell:wc":
		return "line-count / size queries could be answered by a stat-style structured tool."
	case cl.Shape == "read:wide_range":
		return "wide range reads suggest the model wants an outline it doesn't have; consider a capability per file-shape."
	case cl.Shape == "read:one_sided_head", cl.Shape == "read:one_sided_tail":
		return "one-sided range reads often stand in for 'give me the top/bottom'; a bounded read_file alias or outline may suffice."
	case cl.Shape == "search:whitespace_phrase":
		return "free-text phrase search — consider a full-text docs/comments tool."
	case cl.Shape == "search:complex_regex":
		return "structured regex search could be exposed explicitly rather than via shell."
	}
	return "shape appears consistently but no clean capability match — worth investigating what underlying question it represents."
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func truncate(s string, n int) string {
	return util.TruncateBytes(strings.ReplaceAll(s, "\n", " "), n, "…")
}

// llmReview sends one request to the local llama-server with a compressed
// view of the corpus — deterministic findings + per-session tool traces —
// and asks for novel-pattern observations the rule miner would miss.
func llmReview(cfgPath string, calls []toolCall, sessions map[string]*sessionSummary, existing, novel []shapeCluster) (string, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return "", fmt.Errorf("load config: %w", err)
	}
	client := llama.NewClient(cfg.BaseURL(), 0).WithBackend(cfg.LlamaServer.Kind)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	sysMsg := "You are reviewing session logs for idiom, a local coding agent. " +
		"The deterministic rule miner has already identified the clusters listed. " +
		"Your job is to name patterns it MISSED — sequences of calls, recurring workarounds, " +
		"or cross-call shapes (not single-call shapes — those are already counted). " +
		"Be terse. Bullet points only. If nothing stands out, say so. Never repeat the deterministic findings."

	var userBuf strings.Builder
	fmt.Fprintf(&userBuf, "Corpus: %d sessions, %d tool calls total.\n\n", len(sessions), len(calls))
	userBuf.WriteString("Deterministic findings (already covered — do not repeat):\n")
	for _, cl := range existing {
		fmt.Fprintf(&userBuf, "- %s (%d) → %s\n", cl.Shape, cl.Count, cl.Capability)
	}
	for _, cl := range novel {
		fmt.Fprintf(&userBuf, "- %s (%d) novel\n", cl.Shape, cl.Count)
	}
	userBuf.WriteString("\nPer-session traces (tool sequence only):\n")
	var names []string
	for n := range sessions {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, name := range names {
		traces := traceFor(calls, name, 60)
		fmt.Fprintf(&userBuf, "\n[%s]\n%s\n", name, strings.Join(traces, " → "))
	}
	userBuf.WriteString("\nWhat multi-call patterns or non-obvious workarounds do you see that the single-call rule miner missed?")

	reply, err := client.CompleteChat(ctx, []llama.ChatMessage{
		{Role: "system", Content: sysMsg},
		{Role: "user", Content: userBuf.String()},
	}, llama.ChatOptions{
		Temperature: 0.2,
		MaxTokens:   900,
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(reply), nil
}

func traceFor(calls []toolCall, session string, limit int) []string {
	var out []string
	for _, c := range calls {
		if c.Session != session {
			continue
		}
		label := c.Tool
		if c.Shape != "" && !strings.HasPrefix(c.Shape, "structured:") {
			label = c.Shape
		}
		out = append(out, label)
		if len(out) >= limit {
			out = append(out, "…")
			break
		}
	}
	return out
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "idiom-analyze: "+format+"\n", args...)
	os.Exit(1)
}
