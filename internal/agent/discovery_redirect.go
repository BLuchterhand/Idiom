package agent

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"idiom/internal/workspace"
)

// DiscoveryRedirect inspects an about-to-run tool request and, if a more
// deterministic language-specific tool exists for the same question,
// returns a synthesized redirect ToolResult that points the model at the
// better tool. A second identical call (same fingerprint) is allowed
// through as the fallback path — the model gets one nudge per
// (fingerprint, turn), never a lockout.
//
// The redirect layer reasons in capability space (workspace.Capability)
// rather than tool-name space, so adding a new language only requires a
// LanguageSupport entry in the workspace registry — no changes here.
type DiscoveryRedirect struct {
	// firedRedirects keys on the redirect fingerprint (tool+Path+Pattern).
	// The first time a fingerprint fires, we synthesize a redirect. The
	// second time, we execute normally — the model has committed to the
	// generic path and we respect that rather than looping forever.
	firedRedirects map[string]bool
	// decisionTablesShown keys on language name. Ensures the full
	// decision guide is injected once per (language, turn), not on every
	// redirect.
	decisionTablesShown map[string]bool
	// kindFireCount keys on "lang|capability" and counts how many times a
	// given redirect class has fired this turn. Once a class has fired
	// twice, the fingerprint-fallback no longer applies: every call in
	// that class gets redirected, and the message escalates. This
	// catches the "different .go file each time" pattern observed in
	// logs, where each fresh fingerprint got its own redirect without
	// the model internalizing the guidance.
	kindFireCount map[string]int
}

// smallFileRedirectThreshold is the line count below which read_file on
// a Go/C++ file skips the outline redirect and executes directly. Based
// on log analysis: files under this size almost always get read
// whole-file in the follow-up step anyway, so the outline is pure
// overhead. Larger files benefit from outline because the model picks
// a specific symbol to go_read_symbol next.
//
// Threshold chosen from SWE suite empirical: 29% of tasks hit
// redirect_storm at the 200-line threshold. Most .go files in typical
// repos are under 400 lines — raising here cuts the outline detour
// for the majority while still protecting the large-file case.
const smallFileRedirectThreshold = 400

// turnWideReadFileHardBlockAt is the number of read_file→outline
// redirects (any .go file) in a single turn after which we stop
// redirecting and start hard-rejecting raw read_file on .go paths
// with no range. The model has been told 3 times; at that point,
// failing gracefully by forcing a structured call is better than
// continuing to burn an extra step on each one.
const turnWideReadFileHardBlockAt = 3

// NewDiscoveryRedirect returns a per-turn redirect state.
func NewDiscoveryRedirect() *DiscoveryRedirect {
	return &DiscoveryRedirect{
		firedRedirects:      make(map[string]bool),
		decisionTablesShown: make(map[string]bool),
		kindFireCount:       make(map[string]int),
	}
}

// RedirectDecision is the outcome of a Consider call. When ShouldRedirect
// is false the runner proceeds with the original request untouched.
type RedirectDecision struct {
	ShouldRedirect bool
	// Result is the synthesized ToolResult delivered back to the model
	// in place of executing the original request.
	Result workspace.ToolResult
	// DecisionGuide, if non-empty, is a one-shot system message the
	// runner should append to the turn's message list. This carries the
	// language's full capability list and fires at most once per
	// (language, turn).
	DecisionGuide string
	// Language is the language matched by the redirect (for telemetry).
	Language string
	// Capability is the capability the redirect recommends (for telemetry).
	Capability workspace.Capability
	// HardReject signals that the caller should treat this as a
	// rejection (populate its rejectedFingerprints, bump counters)
	// rather than as a soft "redirected" result. Fires once the
	// per-language redirect count for this class passes
	// turnWideReadFileHardBlockAt — the soft guidance has been ignored
	// enough that failing the call outright is the smaller evil than
	// another step of churn.
	HardReject bool
}

// Consider inspects a request and returns a redirect decision. When the
// request is already a language-specific tool, is not covered by any
// rule, or has already fired a redirect this turn, the decision has
// ShouldRedirect=false.
//
// ws is used to confirm the language is actually active in the current
// workspace — we never redirect to a tool that does not apply.
func (d *DiscoveryRedirect) Consider(req workspace.ToolRequest, ws *workspace.Manager) RedirectDecision {
	if d == nil || ws == nil {
		return RedirectDecision{}
	}
	if isStructuredTool(req.Tool) {
		return RedirectDecision{}
	}
	// Shell has its own handler because some replacements (sed -n → read_file)
	// aren't language-specific — they don't fit the LanguageSupport pipeline.
	if strings.EqualFold(strings.TrimSpace(req.Tool), "shell") {
		return d.considerShell(req, ws)
	}
	lang, cap, ok := matchRedirectRule(req, ws)
	if !ok {
		return RedirectDecision{}
	}
	replacement := lang.Tool(cap)
	if replacement == "" {
		return RedirectDecision{}
	}
	fp := redirectFingerprint(req)
	kind := lang.Name + "|" + string(cap)
	priorKindFires := d.kindFireCount[kind]
	if d.firedRedirects[fp] && priorKindFires < 2 {
		// Fallback: model retried the same generic call after a
		// redirect. Honor it — as long as this redirect class is still
		// in its grace period (first two fires). Past that, we stop
		// letting anything through because the model has been warned
		// twice and kept ignoring the structured tool.
		return RedirectDecision{}
	}
	d.firedRedirects[fp] = true
	d.kindFireCount[kind] = priorKindFires + 1

	guide := ""
	if !d.decisionTablesShown[lang.Name] {
		d.decisionTablesShown[lang.Name] = true
		guide = lang.DecisionGuide
	}

	// After turnWideReadFileHardBlockAt fires of the same (lang, cap),
	// the model has had its nudge three times. Flip to a hard-reject
	// path so the runner populates rejectedFingerprints and a retry
	// actually terminates the turn instead of giving the model yet
	// another soft bounce. Gated by cap==CapFileOutline because the
	// other capabilities use the soft-nudge semantics to good effect.
	//
	// Using `>=` means the third fire (not the fourth) hard-rejects.
	// Empirical: the suite run at the looser `>` threshold still saw
	// redirect_storm in 26% of tasks with 41 total events — the
	// kindFireCount was trending to 4+ before biting. Tightening to
	// the third fire cuts the tail.
	hardReject := cap == workspace.CapFileOutline && d.kindFireCount[kind] >= turnWideReadFileHardBlockAt

	summary := fmt.Sprintf("Redirected to %s", replacement)
	if hardReject {
		summary = fmt.Sprintf("Raw %s on %s code blocked — use %s instead", req.Tool, lang.Name, replacement)
	}
	msg := redirectMessage(req, lang, cap, replacement)
	if priorKindFires >= 1 {
		msg = escalateRedirectMessage(msg, lang, cap, replacement, priorKindFires+1)
	}
	result := workspace.ToolResult{
		Tool:    req.Tool,
		Summary: summary,
		Output:  msg,
	}
	if hardReject {
		// When hard-rejecting, the model needs to see this as an error
		// path so its next attempt isn't another raw read_file. Put the
		// guidance in Error so failure_feedback escalates and the
		// runner's existing rejected/failure tracking kicks in.
		result.Error = msg
		result.Output = ""
	}
	return RedirectDecision{
		ShouldRedirect: true,
		HardReject:     hardReject,
		Result:         result,
		DecisionGuide:  guide,
		Language:       lang.Name,
		Capability:     cap,
	}
}

// escalateRedirectMessage prepends a hardened preface to the standard
// redirect body when the same (language, capability) class has already
// fired this turn. The number of prior fires is included so the model
// sees the streak, not just an anonymous scolding.
func escalateRedirectMessage(base string, lang workspace.LanguageSupport, cap workspace.Capability, replacement string, fireNumber int) string {
	preface := fmt.Sprintf(
		"Redirect #%d for %s %s this turn. Further raw-inspection calls on %s code will be blocked instead of executed — even identical retries. Use %s (or the related structured tool listed below) now.\n\n",
		fireNumber, lang.Name, cap, lang.Name, replacement,
	)
	return preface + base
}

func redirectMessage(req workspace.ToolRequest, lang workspace.LanguageSupport, cap workspace.Capability, replacement string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The request targets %s code. Use the deterministic tool %s (%s) instead of %s.\n",
		lang.Name, replacement, cap, req.Tool)
	switch cap {
	case workspace.CapFileOutline:
		fmt.Fprintf(&b, "Try: {\"tool\":%q,\"path\":%q}\n", replacement, req.Path)
		readSym := lang.Tool(workspace.CapReadSymbol)
		fmt.Fprintf(&b, "Next step: to read one declaration by name, use %s — that is the default follow-up and returns the body directly.\n", readSym)
		b.WriteString("Use read_file with start_line/end_line only when you need a range a single declaration cannot cover (imports block, package/file header, or a span across multiple decls).\n")
	case workspace.CapWorkspaceSymbol, workspace.CapFindSymbol:
		fmt.Fprintf(&b, "Try: {\"tool\":%q,\"pattern\":%q}\n", replacement, req.Pattern)
		b.WriteString("The structured result includes file, line, kind, and qualified name.\n")
		readSym := lang.Tool(workspace.CapReadSymbol)
		if readSym != "" {
			fmt.Fprintf(&b, "Next step: to read a hit's body, use %s (pattern, path) — prefer it over read_file. Fall back to read_file with start_line/end_line only when you need context beyond a single declaration.\n", readSym)
		}
	case workspace.CapReferences:
		path := req.Path
		if strings.TrimSpace(path) == "" {
			path = "."
		}
		fmt.Fprintf(&b, "Try: {\"tool\":%q,\"pattern\":%q,\"path\":%q}\n", replacement, req.Pattern, path)
		readSym := lang.Tool(workspace.CapReadSymbol)
		if readSym != "" {
			fmt.Fprintf(&b, "Next step: inspect each hit's enclosing declaration with %s (pattern, path). Use read_file with start_line/end_line only when the surrounding context matters more than the symbol itself.\n", readSym)
		}
	default:
		// Generic hint for capabilities that don't have a bespoke
		// template above. Adding a new Capability should not silently
		// degrade this message — the hint always names the tool so the
		// model can construct the call from its schema.
		fmt.Fprintf(&b, "Try calling %s with the equivalent arguments from your request.\n", replacement)
	}
	b.WriteString("If the deterministic tool returns nothing, retry your original request and it will execute.")
	return strings.TrimSpace(b.String())
}

func redirectFingerprint(req workspace.ToolRequest) string {
	return req.Tool + "|" + req.Path + "|" + req.Pattern + "|" + req.Glob + "|" + req.Command
}

// matchRedirectRule maps a generic tool request to a (language,
// capability) pair when a deterministic tool would answer the same
// question better. Returns ok=false when no rule matches.
func matchRedirectRule(req workspace.ToolRequest, ws *workspace.Manager) (workspace.LanguageSupport, workspace.Capability, bool) {
	switch strings.ToLower(strings.TrimSpace(req.Tool)) {
	case "read_file":
		// A read_file on a code file with no line range is almost
		// always "what's in this file?" — file_outline answers that
		// with line ranges the model can use for a follow-up bounded
		// read_file. A read with start_line/end_line set is a
		// targeted read and is not redirected.
		if req.StartLine > 0 || req.EndLine > 0 {
			return workspace.LanguageSupport{}, "", false
		}
		path := strings.TrimSpace(req.Path)
		if path == "" {
			return workspace.LanguageSupport{}, "", false
		}
		lang, ok := workspace.LanguageForFile(path)
		if !ok {
			return workspace.LanguageSupport{}, "", false
		}
		if !languageActive(ws, lang) {
			return workspace.LanguageSupport{}, "", false
		}
		if lang.Tool(workspace.CapFileOutline) == "" {
			return workspace.LanguageSupport{}, "", false
		}
		// Small files short-circuit the outline detour. A typical
		// outline+read_symbol loop costs two tool calls plus a chunk
		// of prompt context for the outline table; below this
		// threshold, reading the whole file directly is cheaper AND
		// more informative (the model needs the bodies anyway). The
		// threshold is deliberately generous — outline is still the
		// right answer for 500-line files with many decls.
		if n, ok := ws.FileLineCount(path); ok && n > 0 && n <= smallFileRedirectThreshold {
			return workspace.LanguageSupport{}, "", false
		}
		return lang, workspace.CapFileOutline, true
	case "search_content":
		pattern := strings.TrimSpace(req.Pattern)
		if pattern == "" {
			return workspace.LanguageSupport{}, "", false
		}
		// Three ways to qualify: (a) unambiguous Go syntax (func/type/
		// func.*X) → lang=go regardless of glob; (b) unambiguous C++
		// syntax (class/namespace/enum/template) → lang=cpp regardless
		// of glob; (c) bare identifier + a language-specific glob;
		// (d) none of the above → skip.
		var lang workspace.LanguageSupport
		var ok bool
		if _, isGo := extractGoSymbolFromPattern(pattern); isGo {
			lang, ok = workspace.LanguageForExtension(".go")
		} else if _, isCpp := extractCppSymbolFromPattern(pattern); isCpp {
			lang, ok = workspace.LanguageForExtension(".cpp")
		} else if looksLikeIdentifier(pattern) {
			lang, ok = languageFromGlob(req.Glob)
		}
		if !ok {
			return workspace.LanguageSupport{}, "", false
		}
		if !languageActive(ws, lang) {
			return workspace.LanguageSupport{}, "", false
		}
		// Choose between workspace-wide and path-scoped symbol search
		// based on request intent, not purely on which tool the language
		// exposes. When the model already scoped the request to a path,
		// the index-backed find_symbol respects that scope directly;
		// workspace_symbol (gopls-backed) would ignore it and return
		// unfiltered hits. When no path is set, workspace_symbol is the
		// right default — it has the broader reach. Either tool falls
		// back to the other when the preferred one is missing.
		primary, secondary := workspace.CapWorkspaceSymbol, workspace.CapFindSymbol
		if strings.TrimSpace(req.Path) != "" {
			primary, secondary = workspace.CapFindSymbol, workspace.CapWorkspaceSymbol
		}
		cap := primary
		if lang.Tool(cap) == "" {
			cap = secondary
			if lang.Tool(cap) == "" {
				return workspace.LanguageSupport{}, "", false
			}
		}
		return lang, cap, true
	}
	return workspace.LanguageSupport{}, "", false
}

func languageActive(ws *workspace.Manager, lang workspace.LanguageSupport) bool {
	for _, active := range ws.ActiveLanguages() {
		if active.Name == lang.Name {
			return true
		}
	}
	return false
}

// looksLikeIdentifier returns true when the pattern is a bare identifier
// the model is almost certainly trying to locate — the redirect should
// only fire for symbol-shaped queries, not for free-form regex.
func looksLikeIdentifier(pattern string) bool {
	if len(pattern) < 2 {
		return false
	}
	for _, r := range pattern {
		switch {
		case r == '_' || r == ':':
		case r >= '0' && r <= '9':
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		default:
			return false
		}
	}
	return true
}

// languageFromGlob derives a language from a filename glob like
// "*.go", "*.{cpp,h}", "**/*.cc". Only language-specific globs
// redirect; a generic "*" matches nothing here.
func languageFromGlob(glob string) (workspace.LanguageSupport, bool) {
	glob = strings.ToLower(strings.TrimSpace(glob))
	if glob == "" {
		return workspace.LanguageSupport{}, false
	}
	base := filepath.Base(glob)
	// Handle brace-expansion patterns like "*.{cpp,h}".
	if strings.Contains(base, "{") && strings.Contains(base, "}") {
		open := strings.Index(base, "{")
		close := strings.Index(base, "}")
		if open >= 0 && close > open {
			inner := base[open+1 : close]
			for _, ext := range strings.Split(inner, ",") {
				ext = strings.TrimSpace(ext)
				if ext == "" {
					continue
				}
				if !strings.HasPrefix(ext, ".") {
					ext = "." + ext
				}
				if lang, ok := workspace.LanguageForExtension(ext); ok {
					return lang, true
				}
			}
			return workspace.LanguageSupport{}, false
		}
	}
	ext := filepath.Ext(base)
	if ext == "" {
		return workspace.LanguageSupport{}, false
	}
	return workspace.LanguageForExtension(ext)
}

// considerShell handles shell-command redirects. Two shapes are covered:
//
//   - `sed -n 'A,Bp' FILE` → read_file(path=FILE, start_line=A, end_line=B).
//     Strictly generic: no language-specific replacement is needed.
//   - `grep [-n|-rn|-r] "IDENT" [FILE]` with an identifier-shaped pattern
//     → language symbol tool (references if scoped to a file, workspace
//     symbol otherwise).
//
// Anything else — free-form regex grep, pipelines, scripts — falls through
// to normal execution.
func (d *DiscoveryRedirect) considerShell(req workspace.ToolRequest, ws *workspace.Manager) RedirectDecision {
	cmd := strings.TrimSpace(req.Command)
	if cmd == "" {
		return RedirectDecision{}
	}
	fp := redirectFingerprint(req)
	if d.firedRedirects[fp] {
		return RedirectDecision{}
	}

	if match := matchShellSedRange(cmd); match != nil {
		d.firedRedirects[fp] = true
		kind := "shell|sed_read_file"
		d.kindFireCount[kind]++
		hardReject := d.kindFireCount[kind] >= turnWideReadFileHardBlockAt
		msg := fmt.Sprintf(
			"The request reads a file range via shell. Use read_file instead — it returns structured output and skips the shell permission prompt.\n"+
				"Try: {\"tool\":\"read_file\",\"path\":%q,\"start_line\":%d,\"end_line\":%d}\n"+
				"If the read returns nothing, retry your original command and it will execute.",
			match.path, match.start, match.end)
		summary := "Redirected to read_file"
		result := workspace.ToolResult{Tool: req.Tool, Summary: summary, Output: msg}
		if hardReject {
			result.Summary = "Shell range-read blocked — use read_file instead"
			result.Error = msg
			result.Output = ""
		}
		return RedirectDecision{
			ShouldRedirect: true,
			HardReject:     hardReject,
			Result:         result,
			Language:       "",
			Capability:     "read_file",
		}
	}

	if match := matchShellHeadTailFile(cmd); match != nil {
		d.firedRedirects[fp] = true
		kind := "shell|headtail_read_file"
		d.kindFireCount[kind]++
		hardReject := d.kindFireCount[kind] >= turnWideReadFileHardBlockAt
		msg := fmt.Sprintf(
			"The request reads a file prefix/suffix via shell. Use read_file instead — structured output, no permission prompt.\n"+
				"Try: {\"tool\":\"read_file\",\"path\":%q,\"start_line\":%d,\"end_line\":%d}\n"+
				"If the read returns nothing, retry your original command and it will execute.",
			match.path, match.start, match.end)
		result := workspace.ToolResult{Tool: req.Tool, Summary: "Redirected to read_file", Output: msg}
		if hardReject {
			result.Summary = "Shell prefix/suffix read blocked — use read_file instead"
			result.Error = msg
			result.Output = ""
		}
		return RedirectDecision{
			ShouldRedirect: true,
			HardReject:     hardReject,
			Result:         result,
			Language:       "",
			Capability:     "read_file",
		}
	}

	if match := matchShellCatWholeFile(cmd); match != "" {
		d.firedRedirects[fp] = true
		kind := "shell|cat_read_file"
		d.kindFireCount[kind]++
		hardReject := d.kindFireCount[kind] >= turnWideReadFileHardBlockAt
		msg := fmt.Sprintf(
			"The request dumps an entire file via shell. Use read_file instead — it returns structured output and bounds large files automatically.\n"+
				"Try: {\"tool\":\"read_file\",\"path\":%q}\n"+
				"If the read returns nothing, retry your original command and it will execute.",
			match)
		result := workspace.ToolResult{Tool: req.Tool, Summary: "Redirected to read_file", Output: msg}
		if hardReject {
			result.Summary = "Shell cat blocked — use read_file instead"
			result.Error = msg
			result.Output = ""
		}
		return RedirectDecision{
			ShouldRedirect: true,
			HardReject:     hardReject,
			Result:         result,
			Language:       "",
			Capability:     "read_file",
		}
	}

	if match := matchShellFindName(cmd); match != "" {
		d.firedRedirects[fp] = true
		kind := "shell|find_search_files"
		d.kindFireCount[kind]++
		hardReject := d.kindFireCount[kind] >= turnWideReadFileHardBlockAt
		msg := fmt.Sprintf(
			"The request searches for files by name via shell. Use search_files instead — it returns stable handles the model can open with read_file.\n"+
				"Try: {\"tool\":\"search_files\",\"pattern\":%q}\n"+
				"If the search returns nothing, retry your original command and it will execute.",
			match)
		result := workspace.ToolResult{Tool: req.Tool, Summary: "Redirected to search_files", Output: msg}
		if hardReject {
			result.Summary = "Shell find blocked — use search_files instead"
			result.Error = msg
			result.Output = ""
		}
		return RedirectDecision{
			ShouldRedirect: true,
			HardReject:     hardReject,
			Result:         result,
			Language:       "",
			Capability:     "search_files",
		}
	}

	if match := matchShellGrepIdentifier(cmd); match != nil {
		// Infer language: from the target file's extension if present,
		// otherwise from the first active language in the workspace.
		var lang workspace.LanguageSupport
		var ok bool
		if match.path != "" {
			lang, ok = workspace.LanguageForFile(match.path)
		}
		if !ok {
			active := ws.ActiveLanguages()
			if len(active) == 0 {
				return RedirectDecision{}
			}
			lang = active[0]
			ok = true
		}
		if !languageActive(ws, lang) {
			return RedirectDecision{}
		}
		// Prefer references when the model already named a file (that's
		// the "where is X used in this file" question); workspace_symbol
		// otherwise.
		primary := workspace.CapWorkspaceSymbol
		if match.path != "" {
			primary = workspace.CapReferences
		}
		replacement := lang.Tool(primary)
		if replacement == "" {
			// Fall back to the other side of the symbol pair.
			alt := workspace.CapFindSymbol
			if primary == workspace.CapReferences {
				alt = workspace.CapFindSymbol
			}
			replacement = lang.Tool(alt)
			if replacement == "" {
				return RedirectDecision{}
			}
			primary = alt
		}
		d.firedRedirects[fp] = true
		kind := "shell|" + lang.Name + "|" + string(primary)
		d.kindFireCount[kind]++
		hardReject := d.kindFireCount[kind] >= turnWideReadFileHardBlockAt

		guide := ""
		if !d.decisionTablesShown[lang.Name] {
			d.decisionTablesShown[lang.Name] = true
			guide = lang.DecisionGuide
		}

		var b strings.Builder
		fmt.Fprintf(&b, "The request greps for an identifier via shell on %s code. Use %s (%s) instead — it returns file, line, and qualified-name for each hit.\n",
			lang.Name, replacement, primary)
		if match.path != "" {
			fmt.Fprintf(&b, "Try: {\"tool\":%q,\"pattern\":%q,\"path\":%q}\n", replacement, match.pattern, match.path)
		} else {
			fmt.Fprintf(&b, "Try: {\"tool\":%q,\"pattern\":%q}\n", replacement, match.pattern)
		}
		b.WriteString("If the deterministic tool returns nothing, retry your original command and it will execute.")
		summary := "Redirected to " + replacement
		result := workspace.ToolResult{Tool: req.Tool, Summary: summary, Output: b.String()}
		if hardReject {
			result.Summary = fmt.Sprintf("Shell grep blocked — use %s instead", replacement)
			result.Error = b.String()
			result.Output = ""
		}
		return RedirectDecision{
			ShouldRedirect: true,
			HardReject:     hardReject,
			Result:         result,
			DecisionGuide:  guide,
			Language:       lang.Name,
			Capability:     primary,
		}
	}

	return RedirectDecision{}
}

type shellSedMatch struct {
	path       string
	start, end int
}

// reShellSedRange matches the common `sed -n 'A,Bp' FILE` shape. The
// quoted address form `'1,20p'` is the only flag the logs observed — other
// sed invocations (in-place edits, script form) fall through and execute.
var reShellSedRange = regexp.MustCompile(`^sed\s+-n\s+['"]?(\d+),(\d+)p['"]?\s+(\S+)\s*$`)

func matchShellSedRange(cmd string) *shellSedMatch {
	m := reShellSedRange.FindStringSubmatch(cmd)
	if m == nil {
		return nil
	}
	start, err := strconv.Atoi(m[1])
	if err != nil {
		return nil
	}
	end, err := strconv.Atoi(m[2])
	if err != nil || end < start {
		return nil
	}
	return &shellSedMatch{path: m[3], start: start, end: end}
}

type shellGrepMatch struct {
	pattern string
	path    string
}

var reShellGrepIdentToken = regexp.MustCompile(`^[A-Za-z_][\w:]*$`)

// matchShellGrepIdentifier accepts `grep [-flags] PATTERN [FILE]` (or `rg`
// in place of `grep`) where PATTERN, stripped of optional single/double
// quotes, is a bare identifier. Pipes, redirections, and regex metachars
// anywhere in the line disqualify the whole match — we only rewrite the
// unambiguous symbol-lookup shape.
func matchShellGrepIdentifier(cmd string) *shellGrepMatch {
	if strings.ContainsAny(cmd, "|><$`;&") {
		return nil
	}
	fields := strings.Fields(cmd)
	if len(fields) < 2 {
		return nil
	}
	if fields[0] != "grep" && fields[0] != "rg" {
		return nil
	}
	i := 1
	// Skip flag tokens. Accept single-letter combos (-rn) and long
	// flags (--include=foo); reject anything with `=` on its own as
	// too close to shell-var territory.
	for i < len(fields) && strings.HasPrefix(fields[i], "-") {
		// -E, -P, -F change pattern semantics — bail. The caller's
		// pattern might be valid identifier in basic grep but a regex
		// in -E grep, so we don't rewrite those.
		if fields[i] == "-E" || fields[i] == "-P" || fields[i] == "-F" ||
			fields[i] == "--extended-regexp" || fields[i] == "--perl-regexp" {
			return nil
		}
		i++
	}
	if i >= len(fields) {
		return nil
	}
	pattern := stripShellQuotes(fields[i])
	if len(pattern) < 2 || !reShellGrepIdentToken.MatchString(pattern) {
		return nil
	}
	i++
	path := ""
	if i < len(fields) {
		// Everything after the pattern must be a single unambiguous
		// file token. Multiple files, globs, or anything regex-shaped
		// disqualifies.
		if len(fields)-i > 1 {
			return nil
		}
		candidate := fields[i]
		if strings.ContainsAny(candidate, "*?[]{}()") {
			return nil
		}
		path = candidate
	}
	return &shellGrepMatch{pattern: pattern, path: path}
}

// reShellHeadTail matches `head -N FILE` / `tail -N FILE` / `head -n N FILE`.
// Only the simple single-file form redirects; multi-file or piped variants
// fall through to normal execution.
var reShellHeadTail = regexp.MustCompile(`^(head|tail)\s+(?:-n\s+)?-?(\d+)\s+(\S+)\s*$`)

type shellHeadTailMatch struct {
	path       string
	start, end int
}

// matchShellHeadTailFile rewrites `head -N FILE` → read_file lines 1..N
// and `tail -N FILE` → read_file requesting a wide-enough trailing
// window. For tail we use start_line=1 end_line=0 (open-ended) because
// without reading the file we don't know the line count; read_file's
// own clamping logic will handle it.
func matchShellHeadTailFile(cmd string) *shellHeadTailMatch {
	if strings.ContainsAny(cmd, "|><$`;&") {
		return nil
	}
	m := reShellHeadTail.FindStringSubmatch(cmd)
	if m == nil {
		return nil
	}
	n, err := strconv.Atoi(m[2])
	if err != nil || n <= 0 {
		return nil
	}
	path := m[3]
	if strings.ContainsAny(path, "*?[]{}()") {
		return nil
	}
	switch m[1] {
	case "head":
		return &shellHeadTailMatch{path: path, start: 1, end: n}
	case "tail":
		// For tail we don't know total lines; point the model at a
		// whole-file read_file — it'll get the default cap.
		return &shellHeadTailMatch{path: path, start: 1, end: 0}
	}
	return nil
}

// matchShellCatWholeFile rewrites `cat FILE` (single file, no flags,
// no pipe) → read_file on that path. Returns the file path when matched,
// empty string otherwise.
func matchShellCatWholeFile(cmd string) string {
	if strings.ContainsAny(cmd, "|><$`;&") {
		return ""
	}
	fields := strings.Fields(cmd)
	if len(fields) != 2 || fields[0] != "cat" {
		return ""
	}
	path := fields[1]
	if strings.HasPrefix(path, "-") {
		return ""
	}
	if strings.ContainsAny(path, "*?[]{}()") {
		return ""
	}
	return path
}

// reShellFindName matches the common `find PATH -name PATTERN` shape.
// Go's regexp (RE2) doesn't support backreferences, so we match with
// liberal quote handling and strip quotes from the captured pattern.
// Any additional predicates (-type, -path, -exec, -mtime, etc.) or
// pipe/redirection disqualify the match so we don't silently rewrite
// complex finds.
var reShellFindName = regexp.MustCompile(`^find\s+\S+\s+-name\s+(\S+)\s*$`)

// matchShellFindName rewrites `find PATH -name PATTERN` → search_files
// with the same pattern. Returns the pattern when matched, empty string
// otherwise.
func matchShellFindName(cmd string) string {
	if strings.ContainsAny(cmd, "|><$`;&") {
		return ""
	}
	m := reShellFindName.FindStringSubmatch(cmd)
	if m == nil {
		return ""
	}
	pattern := stripShellQuotes(m[1])
	if pattern == "" {
		return ""
	}
	return pattern
}

func stripShellQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// extractGoSymbolFromPattern recognizes unambiguous Go declaration shapes
// in a search_content pattern and returns the target identifier. `ok` is
// true only when the pattern is syntactically Go — lets the redirect fire
// on `func.*Foo` / `func Foo(` / `type Foo` even without an explicit glob.
func extractGoSymbolFromPattern(pattern string) (string, bool) {
	p := strings.TrimSpace(pattern)
	if p == "" {
		return "", false
	}
	for _, re := range goSyntaxExtractors {
		if m := re.FindStringSubmatch(p); len(m) >= 2 && m[1] != "" {
			return m[1], true
		}
	}
	return "", false
}

var goSyntaxExtractors = []*regexp.Regexp{
	regexp.MustCompile(`^func\s*\([^)]*\)\s*([A-Za-z_]\w*)`),
	regexp.MustCompile(`^func\s+([A-Za-z_]\w*)`),
	regexp.MustCompile(`^func[.*\\\s]+([A-Za-z_]\w*)`),
	regexp.MustCompile(`^type\s+([A-Za-z_]\w*)`),
}

// extractCppSymbolFromPattern recognizes unambiguous C++ declaration
// shapes and returns the target identifier. Only keywords absent from
// Go qualify — `struct` is shared and stays out of this list to avoid
// false positives. `template<...>` matches the keyword form even
// without a following identifier (the query intent is still "find the
// template"); in that case we return the raw "template" sentinel and
// let the caller fall back to workspace_symbol.
func extractCppSymbolFromPattern(pattern string) (string, bool) {
	p := strings.TrimSpace(pattern)
	if p == "" {
		return "", false
	}
	for _, re := range cppSyntaxExtractors {
		if m := re.FindStringSubmatch(p); len(m) >= 2 && m[1] != "" {
			return m[1], true
		}
	}
	return "", false
}

var cppSyntaxExtractors = []*regexp.Regexp{
	regexp.MustCompile(`^class\s+([A-Za-z_]\w*)`),
	regexp.MustCompile(`^class[.*\\\s]+([A-Za-z_]\w*)`),
	regexp.MustCompile(`^namespace\s+([A-Za-z_]\w*)`),
	regexp.MustCompile(`^enum(?:\s+class)?\s+([A-Za-z_]\w*)`),
	regexp.MustCompile(`^template\s*<[^>]*>\s*(?:class|struct|typename)?\s*([A-Za-z_]\w*)`),
}

// isStructuredTool reports whether a tool name is already a
// language-specific structured tool — we never redirect one structured
// tool to another.
func isStructuredTool(tool string) bool {
	t := strings.ToLower(strings.TrimSpace(tool))
	if t == "" {
		return false
	}
	for _, prefix := range []string{"go_", "cpp_"} {
		if strings.HasPrefix(t, prefix) {
			return true
		}
	}
	return false
}
