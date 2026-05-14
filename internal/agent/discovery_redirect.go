package agent

import (
	"fmt"
	"path/filepath"
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

// turnWideReadFileHardBlockAt is the number of read_file->outline redirects
// for a single language in one turn after which we stop soft redirecting and
// start hard-rejecting the wide raw-read shape. Other semantic redirects stay
// soft: they are ranked guidance, not mandatory strategy.
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
	if cap == workspace.CapFileOutline && strings.TrimSpace(req.Path) == "" {
		if path, ok := redirectReadFilePath(req, ws); ok {
			req.Path = path
		}
	}
	req = normalizeRedirectRequest(req, lang, cap)
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

	// After turnWideReadFileHardBlockAt whole-file outline redirects of the
	// same language, flip wide raw reads to a hard-reject path so the runner
	// populates rejectedFingerprints and a retry terminates the churn. Gated
	// by CapFileOutline because the other capabilities are semantic ranking
	// hints; forcing them would choose the model's strategy instead of
	// protecting an invariant.
	//
	// Using `>=` means the third fire (not the fourth) hard-rejects.
	// Empirical: the suite run at the looser `>` threshold still saw
	// redirect_storm in 26% of tasks with 41 total events — the
	// kindFireCount was trending to 4+ before biting. Tightening to
	// the third fire cuts the tail.
	hardReject := cap == workspace.CapFileOutline && d.kindFireCount[kind] >= turnWideReadFileHardBlockAt

	summary := redirectSummary(req, replacement)
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

func redirectSummary(req workspace.ToolRequest, replacement string) string {
	replacement = strings.TrimSpace(replacement)
	path := strings.TrimSpace(req.Path)
	if path != "" {
		return fmt.Sprintf("Redirected %s to %s", path, replacement)
	}
	return fmt.Sprintf("Redirected to %s", replacement)
}

// escalateRedirectMessage prepends a hardened preface to the standard
// redirect body when the same (language, capability) class has already
// fired this turn. The number of prior fires is included so the model
// sees the streak, not just an anonymous scolding.
func escalateRedirectMessage(base string, lang workspace.LanguageSupport, cap workspace.Capability, replacement string, fireNumber int) string {
	preface := fmt.Sprintf(
		"Redirect #%d for %s %s this turn. Further raw-inspection calls on %s code will be blocked instead of executed — even identical retries. Available capability route: %s or the related structured adapter listed below.\n\n",
		fireNumber, lang.Name, cap, lang.Name, replacement,
	)
	return preface + base
}

func redirectMessage(req workspace.ToolRequest, lang workspace.LanguageSupport, cap workspace.Capability, replacement string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The request targets %s code. Capability route: %s via adapter %s instead of raw %s.\n",
		lang.Name, cap, replacement, req.Tool)
	switch cap {
	case workspace.CapFileOutline:
		fmt.Fprintf(&b, "candidate_request: {\"tool\":%q,\"path\":%q}\n", replacement, req.Path)
		readSym := lang.Tool(workspace.CapReadSymbol)
		fmt.Fprintf(&b, "related_capability: read_symbol via %s returns one declaration body by name.\n", readSym)
		b.WriteString("raw_range_fallback: read_file with start_line/end_line remains appropriate for ranges a single declaration cannot cover.\n")
	case workspace.CapWorkspaceSymbol, workspace.CapFindSymbol:
		if cap == workspace.CapFindSymbol && strings.TrimSpace(req.Path) != "" {
			fmt.Fprintf(&b, "candidate_request: {\"tool\":%q,\"pattern\":%q,\"path\":%q}\n", replacement, req.Pattern, req.Path)
		} else {
			fmt.Fprintf(&b, "candidate_request: {\"tool\":%q,\"pattern\":%q}\n", replacement, req.Pattern)
		}
		b.WriteString("The structured result includes file, line, kind, and qualified name.\n")
		readSym := lang.Tool(workspace.CapReadSymbol)
		if readSym != "" {
			fmt.Fprintf(&b, "related_capability: read_symbol via %s returns a hit's body by pattern and path; raw range fallback remains for context beyond a single declaration.\n", readSym)
		}
	case workspace.CapReferences:
		path := req.Path
		if strings.TrimSpace(path) == "" {
			path = "."
		}
		fmt.Fprintf(&b, "candidate_request: {\"tool\":%q,\"pattern\":%q,\"path\":%q}\n", replacement, req.Pattern, path)
		readSym := lang.Tool(workspace.CapReadSymbol)
		if readSym != "" {
			fmt.Fprintf(&b, "related_capability: read_symbol via %s can inspect each hit's enclosing declaration; raw range fallback remains for surrounding context.\n", readSym)
		}
	default:
		// Generic hint for capabilities that don't have a bespoke
		// template above. Adding a new Capability should not silently
		// degrade this message — the hint always names the tool so the
		// model can construct the call from its schema.
		fmt.Fprintf(&b, "candidate_adapter: %s with equivalent arguments from the rejected request.\n", replacement)
	}
	b.WriteString("direct_tool_fallback: if the capability route returns nothing, the original request remains available.")
	return strings.TrimSpace(b.String())
}

func normalizeRedirectRequest(req workspace.ToolRequest, lang workspace.LanguageSupport, cap workspace.Capability) workspace.ToolRequest {
	switch cap {
	case workspace.CapWorkspaceSymbol, workspace.CapFindSymbol, workspace.CapReferences:
		if ident, ok := extractSyntaxIdentifierForLanguage(lang.Name, req.Pattern); ok {
			req.Pattern = ident
		}
	}
	return req
}

func redirectFingerprint(req workspace.ToolRequest) string {
	return req.Tool + "|" + req.ID + "|" + req.Path + "|" + req.Pattern + "|" + req.Glob + "|" + req.Command
}

func redirectReadFilePath(req workspace.ToolRequest, ws *workspace.Manager) (string, bool) {
	path := strings.TrimSpace(req.Path)
	if path != "" {
		return path, true
	}
	id := strings.TrimSpace(req.ID)
	if id == "" || ws == nil {
		return "", false
	}
	resolved, err := ws.ResolveHandlePath(id)
	if err != nil {
		return "", false
	}
	if rel, err := filepath.Rel(ws.Root(), resolved); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel), true
	}
	return resolved, true
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
		path, ok := redirectReadFilePath(req, ws)
		if !ok {
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
		if pathLooksTestLike(path) {
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
		if searchContentTargetsConcreteFile(req) && looksLikeIdentifier(pattern) {
			return workspace.LanguageSupport{}, "", false
		}
		lang, ok := languageForSearchRequest(req, ws)
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
