package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// --- /var/folders/h5/0qmlhm750_5c0srn1523bvgw0000gn/T/tmpv0l6gng0.go ---
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
	// given redirect class has fired this turn. Repeated redirects make
	// the guidance more explicit, but remain soft so the redirect layer
	// does not become a read-budget stop.
	kindFireCount map[string]int
	// fingerprintFireCount keys on the redirect fingerprint. Tracks how
	// many times this exact (tool+path+pattern) fingerprint has fired a
	// redirect this turn. The fallthrough gating uses this per-fingerprint
	// counter, not the kind-level counter, so that different patterns that
	// share the same (language, capability) class don't jointly exhaust
	// the fallthrough budget for each other.
	fingerprintFireCount map[string]int
	// allowedTools is the active lane/runtime surface. When a
	// language-specific direct tool is present in the workspace manager
	// but hidden by the lane contract, redirects must point at an
	// advertised wrapper such as code_navigate instead of leaking the
	// hidden direct tool name as the next candidate request.
	allowedTools []string
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

// NewDiscoveryRedirect returns a per-turn redirect state.
func NewDiscoveryRedirect() *DiscoveryRedirect {
	return &DiscoveryRedirect{
		firedRedirects:       make(map[string]bool),
		decisionTablesShown:  make(map[string]bool),
		kindFireCount:        make(map[string]int),
		fingerprintFireCount: make(map[string]int),
	}
}

func (d *DiscoveryRedirect) SetAllowedTools(tools []string) {
	if d == nil {
		return
	}

	d.allowedTools = append(d.allowedTools[:0], tools...)
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
	// rather than as a soft "redirected" result. Generic code-read
	// redirects intentionally leave this false: they are guidance, not
	// permission invariants.
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
	if util.EqualTrimFold(req.Tool, "shell") {
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

	adapter := d.redirectAdapter(replacement)
	fp := redirectFingerprint(req)
	kind := lang.Name + "|" + string(cap)
	priorKindFires := d.kindFireCount[kind]

	// Increment the per-fingerprint counter first so the fallthrough
	// check uses this call's count. Call 1 (fpFires=1): redirect.
	// Call 2 (fpFires=2): fall through — honor the model's retry.
	// Calls 3+ (fpFires>=3): redirect again with escalated messaging.
	fpFires := d.fingerprintFireCount[fp] + 1

	d.fingerprintFireCount[fp] = fpFires

	if fpFires == 2 {
		// Model retried the same call after one redirect — honor it.
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
	summary := redirectSummary(req, adapter.Tool)
	msg := redirectMessage(req, lang, cap, replacement, adapter)

	if priorKindFires >= 1 {
		msg = escalateRedirectMessage(msg, lang, cap, adapter.Label(), priorKindFires+1)
	}

	result := workspace.ToolResult{
		Tool:    req.Tool,
		Summary: summary,
		Output:  msg,
	}

	return RedirectDecision{
		ShouldRedirect: true,
		Result:         result,
		DecisionGuide:  guide,
		Language:       lang.Name,
		Capability:     cap,
	}
}

type redirectAdapter struct {
	Tool       string
	Underlying string
}

func (a redirectAdapter) Label() string {
	if strings.TrimSpace(a.Tool) == "code_navigate" {
		return "code_navigate"
	}

	if strings.TrimSpace(a.Underlying) != "" && strings.TrimSpace(a.Underlying) != strings.TrimSpace(a.Tool) {
		return fmt.Sprintf("%s (underlying %s)", strings.TrimSpace(a.Tool), strings.TrimSpace(a.Underlying))
	}

	return strings.TrimSpace(a.Tool)
}

func (d *DiscoveryRedirect) redirectAdapter(replacement string) redirectAdapter {
	replacement = strings.TrimSpace(replacement)

	adapter := redirectAdapter{Tool: replacement}

	if replacement == "" || len(d.allowedTools) == 0 || redirectToolAllowed(d.allowedTools, replacement) {
		return adapter
	}

	if redirectToolAllowed(d.allowedTools, "code_navigate") {
		return redirectAdapter{Tool: "code_navigate", Underlying: replacement}
	}

	return adapter
}

func redirectToolAllowed(tools []string, tool string) bool {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return false
	}

	for _, candidate := range tools {
		if strings.TrimSpace(candidate) == tool {
			return true
		}
	}

	return false
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
		"This exact call has been redirected %d times this turn. The structured tool route via %s for %s code queries remains available. If the structured tool already returned results for this symbol, those results are your answer.\n\n",
		fireNumber, replacement, lang.Name,
	)

	return preface + base
}

func redirectMessage(req workspace.ToolRequest, lang workspace.LanguageSupport, cap workspace.Capability, replacement string, adapter redirectAdapter) string {
	var b strings.Builder

	fmt.Fprintf(&b, "This %s code query is answered through the %s capability via %s.\n",
		lang.Name, cap, adapter.Label())

	switch cap {
	case workspace.CapFileOutline:
		writeRedirectCandidateRequest(&b, adapter, cap, lang.Name, req.Path, "")

		readSym := lang.Tool(workspace.CapReadSymbol)
		fmt.Fprintf(&b, "related_capability: read_symbol via %s returns one declaration body by name.\n", relatedRedirectAdapterLabel(adapter, readSym))
		b.WriteString("raw_range_fallback: read_file with start_line/end_line remains appropriate for ranges a single declaration cannot cover.\n")
	case workspace.CapWorkspaceSymbol, workspace.CapFindSymbol:
		writeRedirectCandidateRequest(&b, adapter, cap, lang.Name, req.Path, req.Pattern)
		b.WriteString("The structured result includes file, line, kind, and qualified name.\n")

		readSym := lang.Tool(workspace.CapReadSymbol)
		if readSym != "" {
			fmt.Fprintf(&b, "related_capability: read_symbol via %s returns a hit's body by pattern and path; raw range fallback remains for context beyond a single declaration.\n", relatedRedirectAdapterLabel(adapter, readSym))
		}
	case workspace.CapReferences:
		path := req.Path
		if strings.TrimSpace(path) == "" {
			path = "."
		}

		writeRedirectCandidateRequest(&b, adapter, cap, lang.Name, path, req.Pattern)

		readSym := lang.Tool(workspace.CapReadSymbol)
		if readSym != "" {
			fmt.Fprintf(&b, "related_capability: read_symbol via %s can inspect each hit's enclosing declaration; raw range fallback remains for surrounding context.\n", relatedRedirectAdapterLabel(adapter, readSym))
		}
	default:
		// Generic hint for capabilities that don't have a bespoke
		// template above. Adding a new Capability should not silently
		// degrade this message — the hint always names the tool so the
		// model can construct the call from its schema.
		fmt.Fprintf(&b, "candidate_adapter: %s with equivalent arguments from the rejected request.\n", adapter.Label())
	}

	b.WriteString("The structured route remains available. This redirect has fired for this exact fingerprint this turn.")

	return strings.TrimSpace(b.String())
}

func relatedRedirectAdapterLabel(primary redirectAdapter, directRelated string) string {
	if strings.TrimSpace(primary.Tool) == "code_navigate" {
		return "code_navigate"
	}

	return strings.TrimSpace(directRelated)
}

func writeRedirectCandidateRequest(b *strings.Builder, adapter redirectAdapter, cap workspace.Capability, language, path, pattern string) {
	if b == nil {
		return
	}

	if strings.TrimSpace(adapter.Tool) == "code_navigate" {
		fmt.Fprintf(b, "candidate_request: {\"tool\":\"code_navigate\",\"capability\":%q", string(cap))

		if strings.TrimSpace(path) != "" {
			fmt.Fprintf(b, ",\"path\":%q", path)
		}

		if strings.TrimSpace(pattern) != "" {
			fmt.Fprintf(b, ",\"pattern\":%q", pattern)
		}

		if strings.TrimSpace(language) != "" {
			fmt.Fprintf(b, ",\"language\":%q", language)
		}

		b.WriteString("}\n")

		return
	}

	fmt.Fprintf(b, "candidate_request: {\"tool\":%q", adapter.Tool)

	if strings.TrimSpace(pattern) != "" {
		fmt.Fprintf(b, ",\"pattern\":%q", pattern)
	}

	if strings.TrimSpace(path) != "" {
		fmt.Fprintf(b, ",\"path\":%q", path)
	}

	b.WriteString("}\n")
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

		lineCount, ok := ws.FileLineCount(path)
		if !ok {
			return workspace.LanguageSupport{}, "", false
		}

		// Small files short-circuit the outline detour. A typical
		// outline+read_symbol loop costs two tool calls plus a chunk
		// of prompt context for the outline table; below this
		// threshold, reading the whole file directly is cheaper AND
		// more informative (the model needs the bodies anyway). This
		// applies to tests too: small tests are cheap to read, but a
		// multi-thousand-line test file needs an outline before a raw
		// dump can blow the planner context budget.
		// The threshold is deliberately generous — outline is still the
		// right answer for 500-line files with many decls.
		if lineCount > 0 && lineCount <= smallFileRedirectThreshold {
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

// considerConversationReadFileRedirect redirects repeated source-file
// read_file calls on the same file to the shared code-navigation source-body
// capability. Diagnostic logs are deliberately excluded: their useful evidence
// is temporal windows, not symbols.
func (d *DiscoveryRedirect) considerConversationReadFileRedirect(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief) RedirectDecision {
	if d == nil {
		return RedirectDecision{}
	}

	if !conversationDiscoveryRedirectApplies(brief) {
		return RedirectDecision{}
	}

	if !util.EqualTrimFold(req.Tool, "read_file") {
		return RedirectDecision{}
	}

	path := agentutil.NormalizeRunnerPath(req.Path)
	if path == "" {
		return RedirectDecision{}
	}

	if agentutil.IsDiagnosticLogArtifactPath(path) {
		return RedirectDecision{}
	}

	// Don't redirect to code_navigate if it's been hidden by turn-level
	// tool hiding (e.g., same-path fixation). Redirecting to a hidden tool
	// creates a dead end — the model follows the redirect only to get a
	// hidden-tool rejection, wasting a step and confusing the investigation.
	codeNavigateVisible := len(d.allowedTools) == 0 || redirectToolAllowed(d.allowedTools, workspace.CodeNavigateToolName)
	if !codeNavigateVisible {
		return RedirectDecision{}
	}

	// Only redirect after the model has already read this file at
	// least twice this turn. First read is legitimate exploration.
	fp := "read_file|" + path + "|conversation"
	fpFires := d.fingerprintFireCount[fp] + 1

	d.fingerprintFireCount[fp] = fpFires

	if fpFires < 3 {
		return RedirectDecision{}
	}

	d.firedRedirects[fp] = true

	summary := "Redirected repeated read_file to " + workspace.CodeNavigateToolName
	msg := fmt.Sprintf(
		"You have read %s %d times this turn. Instead of reading small chunks, use %s with the symbol name to get the full body in one call. Try:\n\n%s\n\n"+
			"If you don't know the symbol name, use %s first to list available symbols.",
		path, fpFires,
		workspace.CodeNavigateCapabilityInstruction(workspace.CapReadSymbol),
		workspace.CodeNavigateRequestExample(workspace.CapReadSymbol, path, "<symbol name>"),
		workspace.CodeNavigateCapabilityInstruction(workspace.CapFileOutline),
	)

	return RedirectDecision{
		ShouldRedirect: true,
		Result: workspace.ToolResult{
			Tool:    req.Tool,
			Summary: summary,
			Output:  msg,
		},
	}
}

// conversationDiscoveryRedirectApplies returns true when conversation-lane
// discovery redirects should fire for this brief. Matches conversation
// TaskFamily plus direct_answer and grounded_evidence_summary contracts.
func conversationDiscoveryRedirectApplies(brief workerruntime.WorkerRuntimeBrief) bool {
	if workerruntime.TaskFamilyIsConversation(brief.TaskFamily) {
		return true
	}

	return workerruntime.RuntimeContractIsConversationDiscovery(brief.Contract)
}

func searchContentTargetsConcreteFile(req workspace.ToolRequest) bool {
	path := strings.TrimSpace(req.Path)
	if path == "" || strings.ContainsAny(path, "*?[]{}") {
		return false
	}

	return filepath.Ext(filepath.Base(path)) != ""
}

// languageForSearchRequest infers a language for a generic search_content
// request from the strongest available signal. It deliberately avoids
// guessing: ambiguous syntax in a polyglot workspace falls through to the
// original text search instead of redirecting to the wrong language tool.
func languageForSearchRequest(req workspace.ToolRequest, ws *workspace.Manager) (workspace.LanguageSupport, bool) {
	if lang, ok := languageFromConcretePath(req.Path); ok && patternCompatibleWithLanguage(req.Pattern, lang) {
		return lang, true
	}

	if lang, ok := languageFromGlob(req.Glob); ok && patternCompatibleWithLanguage(req.Pattern, lang) {
		return lang, true
	}

	return languageFromSyntaxPattern(req.Pattern, ws)
}

func patternCompatibleWithLanguage(pattern string, lang workspace.LanguageSupport) bool {
	return looksLikeSymbolIdentifier(pattern) || languageSyntaxMatches(lang.Name, pattern)
}

func languageFromConcretePath(path string) (workspace.LanguageSupport, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return workspace.LanguageSupport{}, false
	}

	ext := filepath.Ext(path)
	if ext == "" {
		return workspace.LanguageSupport{}, false
	}

	return workspace.LanguageForExtension(ext)
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
// the model is almost certainly trying to locate -- the redirect should
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

func looksLikeSymbolIdentifier(pattern string) bool {
	if !looksLikeIdentifier(pattern) {
		return false
	}

	hasSymbolShape := false

	for _, r := range pattern {
		switch {
		case r == '_' || r == ':':
			hasSymbolShape = true
		case r >= '0' && r <= '9':
			hasSymbolShape = true
		case r >= 'A' && r <= 'Z':
			hasSymbolShape = true
		}
	}

	return hasSymbolShape
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

func languageFromSyntaxPattern(pattern string, ws *workspace.Manager) (workspace.LanguageSupport, bool) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || ws == nil {
		return workspace.LanguageSupport{}, false
	}

	var matches []workspace.LanguageSupport

	for _, lang := range ws.ActiveLanguages() {
		if languageSyntaxMatches(lang.Name, pattern) {
			matches = append(matches, lang)
		}
	}

	if len(matches) != 1 {
		return workspace.LanguageSupport{}, false
	}

	return matches[0], true
}

func languageSyntaxMatches(language string, pattern string) bool {
	_, ok := extractSyntaxIdentifierForLanguage(language, pattern)
	return ok
}

func extractSyntaxIdentifierForLanguage(language string, pattern string) (string, bool) {
	for _, re := range syntaxExtractors[strings.ToLower(strings.TrimSpace(language))] {
		if m := re.FindStringSubmatch(pattern); len(m) >= 2 && m[1] != "" {
			return m[1], true
		}
	}

	return "", false
}

var syntaxExtractors = map[string][]*regexp.Regexp{
	"go": {
		regexp.MustCompile(`^func\s*\([^)]*\)\s*([A-Za-z_]\w*)`),
		regexp.MustCompile(`^func\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^func[.*\\\s]+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^type\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^const\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^var\s+([A-Za-z_]\w*)`),
	},
	"cpp": {
		regexp.MustCompile(`^class\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^class[.*\\\s]+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^struct\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^struct[.*\\\s]+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^namespace\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^enum(?:\s+class)?\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^union\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^union[.*\\\s]+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^template\s*<[^>]*>\s*(?:class|struct|typename)?\s*([A-Za-z_]\w*)`),
	},
	"python": {
		regexp.MustCompile(`^(?:async\s+)?def\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^class\s+([A-Za-z_]\w*)`),
	},
	"typescript": {
		regexp.MustCompile(`^(?:export\s+)?(?:declare\s+)?(?:default\s+)?(?:abstract\s+)?class\s+([A-Za-z_$][\w$]*)`),
		regexp.MustCompile(`^(?:export\s+)?(?:declare\s+)?(?:default\s+)?(?:async\s+)?function(?:\s+\*?|\s*\*)\s*([A-Za-z_$][\w$]*)`),
		regexp.MustCompile(`^(?:export\s+)?(?:declare\s+)?(?:const\s+)?enum\s+([A-Za-z_$][\w$]*)`),
		regexp.MustCompile(`^(?:export\s+)?(?:declare\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)`),
		regexp.MustCompile(`^(?:export\s+)?(?:declare\s+)?interface\s+([A-Za-z_$][\w$]*)`),
		regexp.MustCompile(`^(?:export\s+)?(?:declare\s+)?type\s+([A-Za-z_$][\w$]*)`),
		regexp.MustCompile(`^(?:export\s+)?(?:declare\s+)?(?:namespace|module)\s+([A-Za-z_$][\w$]*)`),
	},
	"rust": {
		regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?(?:async\s+|const\s+|unsafe\s+|extern\s+(?:"[^"]+"\s+)?)*fn\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?(?:struct|enum)\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?(?:unsafe\s+)?trait\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?type\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?const\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?static\s+(?:mut\s+)?([A-Za-z_]\w*)`),
		regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?macro_rules!\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^impl(?:<[^>]*>)?\s+([A-Za-z_]\w*)`),
	},
	"java": {
		regexp.MustCompile(`^(?:public|private|protected|abstract|final|static|sealed|non-sealed|strictfp|\s)*class\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^(?:public|private|protected|abstract|final|static|sealed|non-sealed|strictfp|\s)*interface\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^(?:public|private|protected|abstract|final|static|sealed|non-sealed|strictfp|\s)*enum\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^(?:public|private|protected|abstract|final|static|sealed|non-sealed|strictfp|\s)*record\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^(?:public|private|protected|abstract|final|static|sealed|non-sealed|strictfp|\s)*@interface\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^(?:public|private|protected|abstract|final|static|synchronized|native|default|strictfp|\s)*(?:<[^>]+>\s+)?(?:[A-Za-z_][\w<>\[\].?,\s]*\s+)?([A-Za-z_]\w*)\s*\(`),
	},
}

// isStructuredTool reports whether a tool name is already a
// language-specific structured tool; we never redirect one structured
// tool to another.
func isStructuredTool(tool string) bool {
	t := strings.ToLower(strings.TrimSpace(tool))
	if t == "" {
		return false
	}

	for _, prefix := range structuredToolPrefixes() {
		if strings.HasPrefix(t, prefix) {
			return true
		}
	}

	return false
}

func structuredToolPrefixes() []string {
	seen := make(map[string]bool)

	var prefixes []string

	for _, lang := range workspace.LanguageRegistry() {
		for _, tool := range lang.Tools {
			prefix, _, ok := strings.Cut(strings.ToLower(strings.TrimSpace(tool)), "_")
			if !ok || prefix == "" || seen[prefix+"_"] {
				continue
			}

			seen[prefix+"_"] = true
			prefixes = append(prefixes, prefix+"_")
		}
	}

	return prefixes
}

// considerShell handles shell-command redirects. Two shapes are covered:
//
//   - `sed -n 'A,Bp' FILE` -> read_file(path=FILE, start_line=A, end_line=B).
//     Strictly generic: no language-specific replacement is needed.
//   - `wc [-l|-c|-w] FILE` -> file_stats(path=FILE). Strictly generic:
//     the model is asking for bounded counts, not arbitrary shell.
//   - `grep [-n|-rn|-r] "IDENT" [FILE]` with an identifier-shaped pattern
//     -> language symbol tool (references if scoped to a file, workspace
//     symbol otherwise).
//
// Anything else -- free-form regex grep, pipelines, scripts -- falls through
// to normal execution.
func (d *DiscoveryRedirect) considerShell(req workspace.ToolRequest, ws *workspace.Manager) RedirectDecision {
	cmd := strings.TrimSpace(req.Command)
	if cmd == "" {
		return RedirectDecision{}
	}

	if d.firedRedirects[redirectFingerprint(req)] {
		return RedirectDecision{}
	}

	if match := matchShellSedRange(cmd); match != nil {
		msg := fmt.Sprintf(
			"The request reads a file range via shell. Bounded-read capability is available through read_file; it returns structured output and skips the shell permission prompt.\n"+
				"candidate_request: {\"tool\":\"read_file\",\"path\":%q,\"start_line\":%d,\"end_line\":%d}\n"+
				"direct_tool_fallback: if the bounded read returns nothing, the original command remains available.",
			match.path, match.start, match.end)

		return d.shellRedirectDecision(req, shellRedirect{
			kind:              "shell|sed_read_file",
			capability:        workspace.Capability("read_file"),
			summary:           "Redirected to read_file",
			hardRejectSummary: "Shell range-read blocked by bounded-read capability",
			message:           msg,
		})
	}

	if match := matchShellHeadTailFile(cmd); match != nil {
		msg := fmt.Sprintf(
			"The request reads a file prefix/suffix via shell. Bounded-read capability is available through read_file; structured output, no permission prompt.\n"+
				"candidate_request: {\"tool\":\"read_file\",\"path\":%q,\"start_line\":%d,\"end_line\":%d}\n"+
				"direct_tool_fallback: if the bounded read returns nothing, the original command remains available.",
			match.path, match.start, match.end)

		return d.shellRedirectDecision(req, shellRedirect{
			kind:              "shell|headtail_read_file",
			capability:        workspace.Capability("read_file"),
			summary:           "Redirected to read_file",
			hardRejectSummary: "Shell prefix/suffix read blocked by bounded-read capability",
			message:           msg,
		})
	}

	if match := matchShellCatWholeFile(cmd); match != "" {
		msg := fmt.Sprintf(
			"The request dumps an entire file via shell. Bounded-read capability is available through read_file so the result is structured and observable by the harness.\n"+
				"candidate_request: {\"tool\":\"read_file\",\"path\":%q}\n"+
				"direct_tool_fallback: if the bounded read returns nothing, the original command remains available.",
			match)

		return d.shellRedirectDecision(req, shellRedirect{
			kind:              "shell|cat_read_file",
			capability:        workspace.Capability("read_file"),
			summary:           "Redirected to read_file",
			hardRejectSummary: "Shell cat blocked by bounded-read capability",
			message:           msg,
		})
	}

	if match := matchShellFindName(cmd); match != "" {
		msg := fmt.Sprintf(
			"The request searches for files by name via shell. File-name search capability is available through search_files; it returns stable handles.\n"+
				"candidate_request: {\"tool\":\"search_files\",\"pattern\":%q}\n"+
				"direct_tool_fallback: if file-name search returns nothing, the original command remains available.",
			match)

		return d.shellRedirectDecision(req, shellRedirect{
			kind:              "shell|find_search_files",
			capability:        workspace.Capability("search_files"),
			summary:           "Redirected to search_files",
			hardRejectSummary: "Shell find blocked by file-name search capability",
			message:           msg,
		})
	}

	if match := matchShellWCStats(cmd); match != "" {
		msg := fmt.Sprintf(
			"The request counts file size/lines via shell. File-stat capability is available through file_stats; it returns structured file, byte, and line counts without a shell prompt.\n"+
				"candidate_request: {\"tool\":\"file_stats\",\"path\":%q}\n"+
				"direct_tool_fallback: if file stats do not answer the question, the original command remains available.",
			match)

		return d.shellRedirectDecision(req, shellRedirect{
			kind:              "shell|file_stats",
			capability:        workspace.Capability("file_stats"),
			summary:           "Redirected to file_stats",
			hardRejectSummary: "Shell wc blocked by file-stat capability",
			message:           msg,
		})
	}

	if match := matchShellGrepIdentifier(cmd); match != nil {
		lang, ok := languageForShellGrep(match, ws)
		if !ok {
			return RedirectDecision{}
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

		kind := "shell|" + lang.Name + "|" + string(primary)

		guide := ""

		if !d.decisionTablesShown[lang.Name] {
			d.decisionTablesShown[lang.Name] = true
			guide = lang.DecisionGuide
		}

		var b strings.Builder

		fmt.Fprintf(&b, "The request greps for an identifier via shell on %s code. Capability route: %s via adapter %s; it returns file, line, and qualified-name for each hit.\n",
			lang.Name, primary, replacement)

		if match.path != "" {
			fmt.Fprintf(&b, "candidate_request: {\"tool\":%q,\"pattern\":%q,\"path\":%q}\n", replacement, match.pattern, match.path)
		} else {
			fmt.Fprintf(&b, "candidate_request: {\"tool\":%q,\"pattern\":%q}\n", replacement, match.pattern)
		}

		b.WriteString("direct_tool_fallback: if the capability route returns nothing, the original command remains available.")

		return d.shellRedirectDecision(req, shellRedirect{
			kind:              kind,
			capability:        primary,
			summary:           "Redirected to " + replacement,
			hardRejectSummary: fmt.Sprintf("Shell grep blocked by %s capability", primary),
			message:           b.String(),
			language:          lang.Name,
			decisionGuide:     guide,
		})
	}

	return RedirectDecision{}
}

type shellRedirect struct {
	kind              string
	capability        workspace.Capability
	summary           string
	hardRejectSummary string
	message           string
	language          string
	decisionGuide     string
}

// Shell redirects protect a permission boundary: shell grep/find/cat can
// bypass the structured discovery contract. Unlike generic code-read
// redirects, repeated shell redirects may become hard rejections.
const turnWideShellRedirectHardBlockAt = 3

func (d *DiscoveryRedirect) shellRedirectDecision(req workspace.ToolRequest, redirect shellRedirect) RedirectDecision {
	d.firedRedirects[redirectFingerprint(req)] = true
	d.kindFireCount[redirect.kind]++

	hardReject := d.kindFireCount[redirect.kind] >= turnWideShellRedirectHardBlockAt

	result := workspace.ToolResult{Tool: req.Tool, Summary: redirect.summary, Output: redirect.message}
	if hardReject {
		result.Summary = redirect.hardRejectSummary
		result.Error = redirect.message
		result.Output = ""
	}

	return RedirectDecision{
		ShouldRedirect: true,
		HardReject:     hardReject,
		Result:         result,
		DecisionGuide:  redirect.decisionGuide,
		Language:       redirect.language,
		Capability:     redirect.capability,
	}
}

type shellSedMatch struct {
	path       string
	start, end int
}

// reShellSedRange matches the common `sed -n 'A,Bp' FILE` shape. The
// quoted address form `'1,20p'` is the only flag the logs observed -- other
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
	glob    string
}

func matchShellWCStats(cmd string) string {
	if strings.ContainsAny(cmd, "$`") || shellCommandHasControlOperator(cmd) {
		return ""
	}

	fields := shellCommandFields(cmd)
	if len(fields) < 2 || fields[0] != "wc" {
		return ""
	}

	pathArg := ""

	for i := 1; i < len(fields); i++ {
		field := fields[i]
		if field == "--" {
			if i+1 >= len(fields) || pathArg != "" {
				return ""
			}

			pathArg = fields[i+1]
			i++

			continue
		}

		if strings.HasPrefix(field, "-") {
			if !isSupportedWCFlag(field) {
				return ""
			}

			continue
		}

		if pathArg != "" {
			return ""
		}

		pathArg = field
	}

	return stripShellQuotes(pathArg)
}

func isSupportedWCFlag(flag string) bool {
	if flag == "" || flag[0] != '-' {
		return false
	}

	switch flag {
	case "-l", "-c", "-m", "-w":
		return true
	}

	if strings.HasPrefix(flag, "--") {
		switch flag {
		case "--lines", "--bytes", "--chars", "--words":
			return true
		default:
			return false
		}
	}

	for _, r := range strings.TrimPrefix(flag, "-") {
		switch r {
		case 'l', 'c', 'm', 'w':
		default:
			return false
		}
	}

	return true
}

var reShellGrepIdentToken = regexp.MustCompile(`^[A-Za-z_][\w:]*$`)

// matchShellGrepIdentifier accepts `grep [-flags] PATTERN [FILE]` (or `rg`
// in place of `grep`) where PATTERN, stripped of optional single/double
// quotes, is a bare identifier. Pipes, redirections, and regex metachars
// anywhere in the line disqualify the whole match -- we only rewrite the
// unambiguous symbol-lookup shape.
func matchShellGrepIdentifier(cmd string) *shellGrepMatch {
	if strings.ContainsAny(cmd, "$`") || shellCommandHasControlOperator(cmd) {
		return nil
	}

	fields := shellCommandFields(cmd)
	if len(fields) < 2 {
		return nil
	}

	if fields[0] != "grep" && fields[0] != "rg" {
		return nil
	}

	i := 1
	// Skip flag tokens. Accept single-letter combos (-rn) and long
	// flags (--include=foo). Capture include/glob flags because they
	// are the only language signal in common unscoped grep/rg searches.
	glob := ""

	for i < len(fields) && strings.HasPrefix(fields[i], "-") {
		// -E, -P, -F change pattern semantics -- bail. The caller's
		// pattern might be valid identifier in basic grep but a regex
		// in -E grep, so we don't rewrite those.
		if fields[i] == "-E" || fields[i] == "-P" || fields[i] == "-F" ||
			fields[i] == "--extended-regexp" || fields[i] == "--perl-regexp" {

			return nil
		}

		if strings.HasPrefix(fields[i], "--include=") {
			glob = stripShellQuotes(strings.TrimPrefix(fields[i], "--include="))
		} else if fields[i] == "-g" && i+1 < len(fields) {
			i++

			glob = stripShellQuotes(fields[i])
		}

		i++
	}

	if i >= len(fields) {
		return nil
	}

	pattern, ok := shellGrepSymbolPattern(stripShellQuotes(fields[i]))
	if !ok {
		return nil
	}

	i++

	path := ""

	for i < len(fields) {
		token := fields[i]
		if strings.HasPrefix(token, "--include=") {
			glob = stripShellQuotes(strings.TrimPrefix(token, "--include="))
			i++

			continue
		}

		if token == "-g" && i+1 < len(fields) {
			i++

			glob = stripShellQuotes(fields[i])
			i++

			continue
		}

		if strings.HasPrefix(token, "-") {
			i++
			continue
		}

		if path != "" {
			return nil
		}

		candidate := stripShellQuotes(token)
		if strings.ContainsAny(candidate, "*?[]{}()") {
			return nil
		}

		path = candidate
		i++
	}

	return &shellGrepMatch{pattern: pattern, path: path, glob: glob}
}

func shellCommandHasControlOperator(cmd string) bool {
	var quote rune

	escaped := false

	for _, r := range cmd {
		if escaped {
			escaped = false
			continue
		}

		if r == '\\' {
			if quote != '\'' {
				escaped = true
			}

			continue
		}

		if quote != 0 {
			if r == quote {
				quote = 0
			}

			continue
		}

		if r == '\'' || r == '"' {
			quote = r
			continue
		}

		if strings.ContainsRune("|><;&", r) {
			return true
		}
	}

	return false
}

func shellCommandFields(cmd string) []string {
	var (
		fields []string
		b      strings.Builder
		quote  rune
	)

	escaped := false

	for _, r := range cmd {
		if escaped {
			b.WriteRune(r)

			escaped = false

			continue
		}

		if r == '\\' {
			if quote == '\'' {
				b.WriteRune(r)
			} else {
				escaped = true
			}

			continue
		}

		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}

			continue
		}

		if r == '\'' || r == '"' {
			quote = r
			continue
		}

		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if b.Len() > 0 {
				fields = append(fields, b.String())
				b.Reset()
			}

			continue
		}

		b.WriteRune(r)
	}

	if escaped {
		b.WriteRune('\\')
	}

	if b.Len() > 0 {
		fields = append(fields, b.String())
	}

	return fields
}

func shellGrepSymbolPattern(pattern string) (string, bool) {
	pattern = strings.TrimSpace(pattern)
	if reShellGrepIdentToken.MatchString(pattern) {
		return pattern, true
	}

	if strings.ContainsAny(pattern, "*+?[]{}()^$") {
		return "", false
	}

	if first, ok := firstIdentifierAlternative(pattern); ok {
		return first, true
	}

	if ident, ok := extractUnambiguousSyntaxIdentifier(pattern); ok {
		return ident, true
	}

	return "", false
}

func firstIdentifierAlternative(pattern string) (string, bool) {
	normalized := strings.ReplaceAll(pattern, `\|`, "|")
	if !strings.Contains(normalized, "|") {
		return "", false
	}

	parts := strings.Split(normalized, "|")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if !looksLikeIdentifier(part) {
			return "", false
		}
	}

	first := strings.TrimSpace(parts[0])

	return first, first != ""
}

func extractUnambiguousSyntaxIdentifier(pattern string) (string, bool) {
	var found string

	for _, extractors := range syntaxExtractors {
		for _, re := range extractors {
			m := re.FindStringSubmatch(pattern)
			if len(m) < 2 || m[1] == "" {
				continue
			}

			if found != "" && found != m[1] {
				return "", false
			}

			found = m[1]
		}
	}

	return found, found != ""
}

func languageForShellGrep(match *shellGrepMatch, ws *workspace.Manager) (workspace.LanguageSupport, bool) {
	if match == nil || ws == nil {
		return workspace.LanguageSupport{}, false
	}

	if lang, ok := languageFromConcretePath(match.path); ok {
		return lang, true
	}

	if lang, ok := languageFromGlob(match.glob); ok {
		return lang, true
	}

	active := ws.ActiveLanguages()
	if len(active) == 1 {
		return active[0], true
	}

	return workspace.LanguageSupport{}, false
}

// reShellHeadTail matches `head -N FILE` / `tail -N FILE` / `head -n N FILE`.
// Only the simple single-file form redirects; multi-file or piped variants
// fall through to normal execution.
var reShellHeadTail = regexp.MustCompile(`^(head|tail)\s+(?:-n\s+)?-?(\d+)\s+(\S+)\s*$`)

type shellHeadTailMatch struct {
	path       string
	start, end int
}

// matchShellHeadTailFile rewrites `head -N FILE` -> read_file lines 1..N
// and `tail -N FILE` -> read_file requesting a wide-enough trailing
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
		// whole-file read_file so the workspace layer can choose the
		// right log/source behavior.
		return &shellHeadTailMatch{path: path, start: 1, end: 0}
	}

	return nil
}

// matchShellCatWholeFile rewrites `cat FILE` (single file, no flags,
// no pipe) -> read_file on that path. Returns the file path when matched,
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

// matchShellFindName rewrites `find PATH -name PATTERN` -> search_files
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

type debugLogSearchHit struct {
	Handle string
	Path   string
	Line   int
}

type debugLogFileSelection struct {
	Handle string
	Path   string
}

type debugLogReadWindow struct {
	Handle string
	Path   string
	Start  int
	End    int
}

var singleSearchContentSummaryRE = regexp.MustCompile(`^Found 1 matching line at (.+):([0-9]+)\.`)

func debugLogEvidenceSatisfied(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !debugLogContractApplies(brief, results) {
		return false
	}

	hit, ok := latestDebugLogSearchHit(results)
	if !ok {
		return false
	}

	return hasDebugLogReadWindowForHit(results, hit)
}

func debugLogPhaseScopeRejection(req workspace.ToolRequest, results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief, sessionID string) (string, string, bool) {
	if !debugLogContractApplies(brief, results) || debugLogEvidenceSatisfied(brief, results) {
		return "", "", false
	}

	selection, ok := latestDebugLogSelection(results)
	if !ok || !hasDebugLogReadForSelection(results, selection) {
		return "", "", false
	}

	if path, ok := concreteDebugLogArtifactPathForRequest(req, results); ok &&
		!debugLogSelectionMatchesPath(selection, path) &&
		!currentSessionDebugLogPath(path, sessionID) {

		return "", "", false
	}

	if workspace.IsReadTool(strings.TrimSpace(req.Tool)) && !debugLogRequestTargetsSelection(req, selection) {
		return "Selected-log scope enforced",
			"you already selected and read the newest relevant log. Stay on that log until you can cite the weird decision or declare the missing datum.",
			true
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile || !debugLogRequestTargetsSelection(req, selection) {
		return "", "", false
	}

	if _, ok := latestDebugLogSearchHit(results); ok {
		return "", "", false
	}

	if debugLogReadRequestAddsWindow(req, results, selection) {
		return "", "", false
	}

	return "Blind log reread rejected",
		"you already have the newest tail of the selected log. A final answer is available if that tail supports it; otherwise search within the selected log for one concrete marker.",
		true
}

func debugLogFollowupRequest(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if !debugLogContractApplies(brief, results) {
		return workspace.ToolRequest{}, false
	}

	hit, ok := latestDebugLogSearchHit(results)
	if !ok || hasDebugLogReadWindowForHit(results, hit) {
		return workspace.ToolRequest{}, false
	}

	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameSearchContent:
		return debugLogReadWindowRequest(hit), true
	case workspace.ToolNameReadFile:
		if debugLogRequestTargetsHit(req, hit) && debugLogRequestCoversHit(req, hit) {
			return workspace.ToolRequest{}, false
		}

		return debugLogReadWindowRequest(hit), true
	default:
		return workspace.ToolRequest{}, false
	}
}

func latestDebugLogSelection(results []workspace.ToolResult) (debugLogFileSelection, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		switch strings.TrimSpace(result.Tool) {
		case workspace.ToolNameReadFile:
			window, ok := parseDebugLogReadWindow(result)
			if !ok || !isStrictDebugLogArtifactPath(window.Path) {
				continue
			}

			return debugLogFileSelection{Handle: window.Handle, Path: window.Path}, true
		case workspace.ToolNameSearchContent:
			hit, ok := parseDebugLogSearchHit(result)
			if !ok || !isStrictDebugLogArtifactPath(hit.Path) {
				continue
			}

			return debugLogFileSelection{Handle: hit.Handle, Path: hit.Path}, true
		case workspace.ToolNameSearchFiles:
			selection, ok := parseDebugLogFileSelection(result)
			if ok {
				return selection, true
			}
		}
	}

	return debugLogFileSelection{}, false
}

func parseDebugLogFileSelection(result workspace.ToolResult) (debugLogFileSelection, bool) {
	selections := parseDebugLogFileSelections(result)
	if len(selections) == 0 {
		return debugLogFileSelection{}, false
	}

	return selections[0], true
}

func parseDebugLogFileSelections(result workspace.ToolResult) []debugLogFileSelection {
	if strings.TrimSpace(result.Tool) != workspace.ToolNameSearchFiles || strings.TrimSpace(result.Error) != "" {
		return nil
	}

	var selections []debugLogFileSelection

	for _, line := range strings.Split(result.Output, "\n") {
		handle, path := parseSearchFilesSelectionLine(line)
		if path == "" || !isStrictDebugLogArtifactPath(path) {
			continue
		}

		selections = append(selections, debugLogFileSelection{Handle: handle, Path: path})
	}

	return selections
}

func latestStableDebugLogSelection(results []workspace.ToolResult, sessionID string) (debugLogFileSelection, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		switch strings.TrimSpace(result.Tool) {
		case workspace.ToolNameReadFile:
			window, ok := parseDebugLogReadWindow(result)
			if !ok || !isStrictDebugLogArtifactPath(window.Path) || currentSessionDebugLogPath(window.Path, sessionID) {
				continue
			}

			return debugLogFileSelection{Handle: window.Handle, Path: window.Path}, true
		case workspace.ToolNameSearchContent:
			hit, ok := parseDebugLogSearchHit(result)
			if !ok || !isStrictDebugLogArtifactPath(hit.Path) || currentSessionDebugLogPath(hit.Path, sessionID) {
				continue
			}

			return debugLogFileSelection{Handle: hit.Handle, Path: hit.Path}, true
		case workspace.ToolNameSearchFiles:
			for _, selection := range parseDebugLogFileSelections(result) {
				if currentSessionDebugLogPath(selection.Path, sessionID) {
					continue
				}

				return selection, true
			}
		}
	}

	return debugLogFileSelection{}, false
}

func parseSearchFilesSelectionLine(line string) (string, string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", ""
	}

	handle := ""
	if fields := strings.Fields(line); len(fields) > 0 && strings.HasPrefix(fields[0], "f") {
		handle = strings.TrimSpace(fields[0])
		line = strings.TrimSpace(strings.TrimPrefix(line, handle))
	}

	if idx := strings.Index(line, " (modified "); idx >= 0 {
		line = line[:idx]
	}

	return handle, strings.TrimSpace(line)
}

func hasDebugLogReadForSelection(results []workspace.ToolResult, selection debugLogFileSelection) bool {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
			continue
		}

		window, ok := parseDebugLogReadWindow(result)
		if !ok {
			continue
		}

		if selection.Handle != "" && window.Handle == selection.Handle {
			return true
		}

		if selection.Path != "" && window.Path == selection.Path {
			return true
		}
	}

	return false
}

func nextDebugLogSelection(results []workspace.ToolResult, current debugLogFileSelection) (debugLogFileSelection, bool) {
	return nextStableDebugLogSelection(results, current, "")
}

func nextStableDebugLogSelection(results []workspace.ToolResult, current debugLogFileSelection, sessionID string) (debugLogFileSelection, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != workspace.ToolNameSearchFiles || strings.TrimSpace(result.Error) != "" {
			continue
		}

		selections := parseDebugLogFileSelections(result)
		for idx := range selections {
			selection := selections[idx]
			if !sameDebugLogSelection(selection, current) {
				continue
			}

			for nextIdx := idx + 1; nextIdx < len(selections); nextIdx++ {
				if currentSessionDebugLogPath(selections[nextIdx].Path, sessionID) {
					continue
				}

				if !hasDebugLogReadForSelection(results, selections[nextIdx]) {
					return selections[nextIdx], true
				}
			}
		}
	}

	return debugLogFileSelection{}, false
}

func debugLogContractApplies(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if strings.TrimSpace(brief.TaskFamily) == "debug_logs" {
		return true
	}

	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return false
	}

	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) &&
		!workerruntime.RuntimeContractIsDirectAnswer(brief.Contract) &&
		!workerruntime.RuntimeContractIsArchitecture(brief.Contract) &&
		!workerruntime.RuntimeContractIsReviewFindings(brief.Contract) {

		return false
	}

	if !objectiveRequestsDiagnosticLogInspection(brief.Objective) {
		return false
	}

	if objectiveTreatsDiagnosticLogsAsOptionalEvidence(brief.Objective) {
		return false
	}

	return true
}

func objectiveRequestsDiagnosticLogInspection(objective string) bool {
	words := " " + strings.ToLower(strings.TrimSpace(objective)) + " "
	if strings.TrimSpace(words) == "" {
		return false
	}

	if strings.Contains(words, " read these files") || strings.Contains(words, " read the files") {
		return false
	}

	if objectiveRequestsSourceFeatureInspection(words) && !objectiveNamesConcreteDiagnosticLogArtifact(words) {
		return false
	}

	hasDiagnosticArtifact := false

	for _, artifact := range []string{
		" log", "logs", "telemetry log", "telemetry/", "conversation log", "conversations/",
		"conversation/log", "session log", "debug log", "debuglog", ".jsonl", ".log",
	} {
		if strings.Contains(words, artifact) {
			hasDiagnosticArtifact = true
			break
		}
	}

	if !hasDiagnosticArtifact {
		return false
	}

	for _, intent := range []string{
		" inspect ", " investigate ", " review ", " analyze ", " analyse ", " check ",
		" identify ", " diagnose ", " look at ", " open ", " read ", " tail ", " scan ", " audit ",
		" actual conversation logs", " newest log", " recent log", " latest log",
		" previous log", " prior log", " conversation logs", " telemetry logs",
	} {
		if strings.Contains(words, intent) {
			return true
		}
	}

	hasIncidentQuestion := strings.Contains(words, " why did ") ||
		strings.Contains(words, " why was ") ||
		strings.Contains(words, " what happened") ||
		strings.Contains(words, " did it actually") ||
		strings.Contains(words, " hit objective")
	if !hasIncidentQuestion {
		return false
	}

	for _, failureMarker := range []string{" failure", " failed", " error", " reject", " rejected", " blocker", " unmet", " overreach", " bad answer"} {
		if strings.Contains(words, failureMarker) {
			return true
		}
	}

	return false
}

func objectiveTreatsDiagnosticLogsAsOptionalEvidence(objective string) bool {
	words := " " + strings.ToLower(strings.Join(strings.Fields(objective), " ")) + " "
	if strings.TrimSpace(words) == "" {
		return false
	}

	if !agentutil.ContainsAny(words, " if available", " if present", " when available", " where available") {
		return false
	}

	return agentutil.ContainsAny(words,
		" source ", " source files ", " code ", " bug ", " behavior bug ", " rca ",
		" tests ", " fix shape ", " owning boundary ", " implementation ",
	)
}

func objectiveRequestsSourceFeatureInspection(words string) bool {
	if strings.TrimSpace(words) == "" {
		return false
	}

	return agentutil.ContainsAny(words,
		" codebase",
		" source ",
		" source-code",
		" go file",
		" go files",
		" config",
		" configuration",
		" flag ",
		" flag controls",
		" defined",
		" definition",
		" implementation",
		" logic",
		" mode ",
		" when enabled",
		" field",
		" fields",
		" value",
		" values",
		" type",
		" types",
		" struct",
		" structs",
	)
}

func objectiveNamesConcreteDiagnosticLogArtifact(words string) bool {
	if strings.TrimSpace(words) == "" {
		return false
	}

	return agentutil.ContainsAny(words,
		" actual conversation logs",
		" newest log",
		" recent log",
		" latest log",
		" previous log",
		" prior log",
		" conversation logs",
		" telemetry logs",
		" log file",
		" log artifact",
		" logs/",
		" telemetry/",
		" conversations/",
		" .jsonl",
		" .log",
	)
}

func currentDebugLogSessionID(mgr *workspace.Manager) string {
	if mgr == nil {
		return ""
	}

	return strings.TrimSpace(mgr.SessionID())
}

func currentSessionDebugLogPath(path string, sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}

	clean := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(path)), "./")
	if !isStrictDebugLogArtifactPath(clean) {
		return false
	}

	base := strings.TrimSuffix(filepath.Base(clean), filepath.Ext(clean))

	return base == sessionID || strings.HasPrefix(base, sessionID+"-")
}

func sameDebugLogSelection(a debugLogFileSelection, b debugLogFileSelection) bool {
	if a.Handle != "" && b.Handle != "" && a.Handle == b.Handle {
		return true
	}

	return a.Path != "" && b.Path != "" && a.Path == b.Path
}

func shouldDeepenDebugLogSearch(results []workspace.ToolResult, selection debugLogFileSelection) bool {
	_ = selection

	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != workspace.ToolNameSearchContent {
			continue
		}

		if strings.TrimSpace(result.Error) != "" {
			return false
		}

		return workerruntime.IsZeroHitSearchResult(result)
	}

	return false
}

func debugLogDiscoveryRequest(mgr *workspace.Manager) (workspace.ToolRequest, bool) {
	pattern := "*.log"

	if mgr != nil {
		root := strings.TrimSpace(mgr.Root())
		for _, rel := range []string{"logs", "log", "traces", "trace"} {
			if root == "" {
				break
			}

			if info, err := os.Stat(filepath.Join(root, rel)); err == nil && info.IsDir() {
				pattern = filepath.ToSlash(rel) + "/**/*"
				break
			}
		}
	}

	return workspace.ToolRequest{Tool: workspace.ToolNameSearchFiles, Pattern: pattern, Path: "."}, true
}

func debugLogTailRequest(selection debugLogFileSelection) workspace.ToolRequest {
	req := workspace.ToolRequest{Tool: workspace.ToolNameReadFile}
	if selection.Handle != "" {
		req.ID = selection.Handle
		return req
	}

	req.Path = selection.Path

	return req
}

func debugLogRequestTargetsSelection(req workspace.ToolRequest, selection debugLogFileSelection) bool {
	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameReadFile:
		if selection.Handle != "" && util.FirstNonEmpty(strings.TrimSpace(req.ID), strings.TrimSpace(req.Handle)) == selection.Handle {
			return true
		}

		return selection.Path != "" && strings.TrimSpace(req.Path) == selection.Path
	case workspace.ToolNameSearchContent:
		if selection.Path == "" {
			return false
		}

		return strings.TrimSpace(req.Path) == selection.Path || strings.TrimSpace(req.Glob) == selection.Path
	default:
		return false
	}
}

func debugLogSelectionMatchesPath(selection debugLogFileSelection, path string) bool {
	return agentutil.NormalizeRunnerPath(selection.Path) == agentutil.NormalizeRunnerPath(path)
}

func concreteDebugLogArtifactPathForRequest(req workspace.ToolRequest, results []workspace.ToolResult) (string, bool) {
	for _, value := range []string{req.Path, req.Glob} {
		if isStrictDebugLogArtifactPath(value) {
			return strings.TrimSpace(value), true
		}
	}

	handle := util.FirstNonEmpty(strings.TrimSpace(req.ID), strings.TrimSpace(req.Handle))
	if handle == "" {
		return "", false
	}

	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]

		if window, ok := parseDebugLogReadWindow(result); ok && window.Handle == handle && isStrictDebugLogArtifactPath(window.Path) {
			return window.Path, true
		}

		if hit, ok := parseDebugLogSearchHit(result); ok && hit.Handle == handle && isStrictDebugLogArtifactPath(hit.Path) {
			return hit.Path, true
		}

		for _, selection := range parseDebugLogFileSelections(result) {
			if selection.Handle == handle && isStrictDebugLogArtifactPath(selection.Path) {
				return selection.Path, true
			}
		}
	}

	return "", false
}

func debugLogReadRequestAddsWindow(req workspace.ToolRequest, results []workspace.ToolResult, selection debugLogFileSelection) bool {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile || !debugLogRequestTargetsSelection(req, selection) {
		return false
	}

	if req.StartLine <= 0 && req.EndLine <= 0 {
		return false
	}

	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]

		if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
			continue
		}

		window, ok := parseDebugLogReadWindow(result)
		if !ok {
			continue
		}

		if selection.Handle != "" && window.Handle != selection.Handle {
			continue
		}

		if selection.Path != "" && agentutil.NormalizeRunnerPath(window.Path) != agentutil.NormalizeRunnerPath(selection.Path) {
			continue
		}

		return !debugLogRequestWindowCovered(req, window)
	}

	return true
}

func debugLogRequestWindowCovered(req workspace.ToolRequest, window debugLogReadWindow) bool {
	start := req.StartLine
	end := req.EndLine

	if start <= 0 {
		start = 1
	}

	if end <= 0 {
		return false
	}

	return window.Start <= start && window.End >= end
}

func debugLogRequestTargetsCurrentSession(req workspace.ToolRequest, sessionID string) bool {
	for _, value := range []string{req.Path, req.Glob} {
		if currentSessionDebugLogPath(value, sessionID) {
			return true
		}
	}

	return false
}

func debugLogRequestMatchesDiscovery(req workspace.ToolRequest, discovery workspace.ToolRequest) bool {
	if strings.TrimSpace(req.Tool) != strings.TrimSpace(discovery.Tool) {
		return false
	}

	return strings.TrimSpace(req.Pattern) == strings.TrimSpace(discovery.Pattern) &&
		strings.TrimSpace(req.Path) == strings.TrimSpace(discovery.Path)
}

func isConcreteDebugLogArtifactRequest(req workspace.ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameReadFile:
		return isStrictDebugLogArtifactPath(strings.TrimSpace(req.Path))
	case workspace.ToolNameSearchContent:
		return isStrictDebugLogArtifactPath(strings.TrimSpace(req.Path)) || isStrictDebugLogArtifactPath(strings.TrimSpace(req.Glob))
	default:
		return false
	}
}

func debugLogScopedSearchRedirect(req workspace.ToolRequest, selection debugLogFileSelection) (workspace.ToolRequest, bool) {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameSearchContent || debugLogRequestTargetsSelection(req, selection) {
		return workspace.ToolRequest{}, false
	}

	if exactDebugLogFilePath(req.Path) || exactDebugLogFilePath(req.Glob) {
		return workspace.ToolRequest{}, false
	}

	if !mentionsConcreteDebugLogSelector(req.Path) && !mentionsConcreteDebugLogSelector(req.Glob) {
		return workspace.ToolRequest{}, false
	}

	redirected := req

	redirected.Path = selection.Path
	redirected.Glob = ""

	return redirected, true
}

func mentionsConcreteDebugLogSelector(value string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(value)))
	if lower == "" {
		return false
	}

	return strings.Contains(lower, "/logs/") ||
		strings.HasPrefix(lower, "logs/") ||
		strings.Contains(lower, "/log/") ||
		strings.HasPrefix(lower, "log/") ||
		strings.Contains(lower, "/traces/") ||
		strings.HasPrefix(lower, "traces/") ||
		strings.HasSuffix(lower, ".log") ||
		strings.HasSuffix(lower, ".jsonl") ||
		strings.HasSuffix(lower, ".ndjson") ||
		strings.HasSuffix(lower, ".trace")
}

func isStrictDebugLogArtifactPath(path string) bool {
	return mentionsConcreteDebugLogSelector(path)
}

func exactDebugLogFilePath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	if lower == "" || strings.ContainsAny(lower, "*?[") {
		return false
	}

	return strings.HasSuffix(lower, ".log") ||
		strings.HasSuffix(lower, ".jsonl") ||
		strings.HasSuffix(lower, ".ndjson") ||
		strings.HasSuffix(lower, ".trace")
}

func generatedEvidenceArtifactReadRedirectResult(req workspace.ToolRequest, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile {
		return workspace.ToolResult{}, false
	}

	path := agentutil.NormalizeRunnerPath(req.Path)
	if !isGeneratedDiscoveryContextArtifactPath(path) {
		return workspace.ToolResult{}, false
	}

	paths := delegatedDiscoveryQueryEvidencePaths(results, 6)
	if len(paths) == 0 {
		paths = promptEvidencePathsFromToolResults(results, 6)
	}

	paths = nonArtifactEvidencePaths(paths)
	if len(paths) == 0 {
		return workspace.ToolResult{}, false
	}

	var b strings.Builder

	fmt.Fprintf(&b, "Generated evidence artifact %s was not read because generated discovery/context JSON is not source-body evidence.\n", path)
	b.WriteString("Use the source paths already named by the current discovery packet instead:\n")

	for _, sourcePath := range util.Prefix(paths, 6) {
		fmt.Fprintf(&b, "- %s\n", sourcePath)
	}

	fmt.Fprintf(&b, "Next exact source probe example: {\"tool\":\"read_file\",\"path\":%q}\n", paths[0])
	b.WriteString("After one source-body read resolves the claim, answer from the gathered evidence instead of reading artifact JSON.")

	return workspace.ToolResult{
		Tool:    req.Tool,
		Summary: "Redirected generated evidence artifact read to source paths",
		Output:  strings.TrimSpace(b.String()),
	}, true
}

func generatedEvidenceArtifactReadRedirectRequest(req workspace.ToolRequest, results []workspace.ToolResult) (workspace.ToolRequest, string, string, bool) {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile {
		return workspace.ToolRequest{}, "", "", false
	}

	path := agentutil.NormalizeRunnerPath(req.Path)
	if !isGeneratedDiscoveryContextArtifactPath(path) {
		return workspace.ToolRequest{}, "", "", false
	}

	paths := delegatedDiscoveryQueryEvidencePaths(results, 6)
	if len(paths) == 0 {
		paths = promptEvidencePathsFromToolResults(results, 6)
	}

	paths = nonArtifactEvidencePaths(paths)
	if len(paths) == 0 {
		return workspace.ToolRequest{}, "", "", false
	}

	redirected := req

	redirected.Path = paths[0]
	redirected.StartLine = 0
	redirected.EndLine = 0
	redirected.NamedGap = util.FirstNonEmpty(strings.TrimSpace(req.NamedGap), "source-body evidence named by generated discovery artifact")
	redirected.WhyNotAnswering = util.FirstNonEmpty(strings.TrimSpace(req.WhyNotAnswering), "generated discovery/context JSON is provenance, not source-body evidence")

	return redirected, marshalToolRequestRaw(redirected), "generated discovery/context artifact redirected to source-body path", true
}

func structuredSourceReadRewriteRequest(req workspace.ToolRequest, ws *workspace.Manager, allowedTools []string, brief workerruntime.WorkerRuntimeBrief) (workspace.ToolRequest, string, string, bool) {
	if ws == nil || runnerBriefRequiresMutation(brief) || !redirectToolAllowed(allowedTools, workspace.ToolNameCodeNavigate) {
		return workspace.ToolRequest{}, "", "", false
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile || req.StartLine > 0 || req.EndLine > 0 {
		return workspace.ToolRequest{}, "", "", false
	}

	lang, cap, ok := matchRedirectRule(req, ws)
	if !ok || cap != workspace.CapFileOutline {
		return workspace.ToolRequest{}, "", "", false
	}

	path, ok := redirectReadFilePath(req, ws)
	if !ok || strings.TrimSpace(path) == "" {
		return workspace.ToolRequest{}, "", "", false
	}

	redirected := workspace.ToolRequest{
		Tool:            workspace.ToolNameCodeNavigate,
		Capability:      string(cap),
		Path:            agentutil.NormalizeRunnerPath(path),
		Language:        lang.Name,
		NamedGap:        req.NamedGap,
		WhyNotAnswering: req.WhyNotAnswering,
	}
	raw := marshalToolRequestRaw(redirected)

	if raw == "" || normalizedToolRequestRaw(redirected) == normalizedToolRequestRaw(req) {
		return workspace.ToolRequest{}, "", "", false
	}

	return redirected, raw, "source read redirected to structured navigation", true
}

func pathlessReadSymbolAfterOutlineRewriteRequest(req workspace.ToolRequest, results []workspace.ToolResult) (workspace.ToolRequest, string, string, bool) {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameCodeNavigate || strings.TrimSpace(req.Path) != "" {
		return workspace.ToolRequest{}, "", "", false
	}

	capability := strings.TrimSpace(util.FirstNonEmpty(req.Capability, req.Operation, req.Action))
	if capability != "read_symbol" && capability != "symbol_body" && capability != "inspect_symbol" {
		return workspace.ToolRequest{}, "", "", false
	}

	symbol := strings.TrimSpace(util.FirstNonEmpty(req.Pattern, req.Symbol, req.Query))
	if symbol == "" {
		return workspace.ToolRequest{}, "", "", false
	}

	path := latestCodeNavigateOutlinePath(results)
	if path == "" {
		return workspace.ToolRequest{}, "", "", false
	}

	redirected := req

	redirected.Path = path

	if strings.TrimSpace(redirected.Pattern) == "" {
		redirected.Pattern = symbol
	}

	if strings.TrimSpace(redirected.Symbol) == "" {
		redirected.Symbol = symbol
	}

	raw := marshalToolRequestRaw(redirected)
	if raw == "" || normalizedToolRequestRaw(redirected) == normalizedToolRequestRaw(req) {
		return workspace.ToolRequest{}, "", "", false
	}

	return redirected, raw, "pathless read_symbol anchored to latest file_outline path", true
}

func latestCodeNavigateOutlinePath(results []workspace.ToolResult) string {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != workspace.ToolNameCodeNavigate || strings.TrimSpace(result.Error) != "" {
			continue
		}

		text := strings.ToLower(strings.Join([]string{result.Summary, result.Output}, "\n"))
		if !strings.Contains(text, "file_outline") {
			continue
		}

		for _, path := range agentutil.ReadPathsFromToolResult(result) {
			path = agentutil.NormalizeRunnerPath(path)
			if path != "" && !agentutil.IsNonSourceEvidenceArtifactPath(path) {
				return path
			}
		}

		for _, path := range pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(strings.Join([]string{result.Summary, result.Output}, "\n"))) {
			path = agentutil.NormalizeRunnerPath(path)
			if path != "" && !agentutil.IsNonSourceEvidenceArtifactPath(path) {
				return path
			}
		}
	}

	return ""
}

func repeatedFileOutlineToReadSymbolRewriteRequest(req workspace.ToolRequest, results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) (workspace.ToolRequest, string, string, bool) {
	if runnerBriefRequiresMutation(brief) || strings.TrimSpace(req.Tool) != workspace.ToolNameCodeNavigate {
		return workspace.ToolRequest{}, "", "", false
	}

	capability := strings.TrimSpace(util.FirstNonEmpty(req.Capability, req.Operation, req.Action))
	if capability != "file_outline" && capability != "outline" {
		return workspace.ToolRequest{}, "", "", false
	}

	path := agentutil.NormalizeRunnerPath(req.Path)
	if path == "" || concreteSourceEvidenceReadForPath(path, results) || !priorFileOutlineForPath(path, results) {
		return workspace.ToolRequest{}, "", "", false
	}

	symbol := planThenExecuteBestOutlineSymbol(path, brief.Objective, results)
	if symbol == "" {
		return workspace.ToolRequest{}, "", "", false
	}

	redirected := req

	redirected.Capability = "read_symbol"
	redirected.Operation = ""
	redirected.Action = ""
	redirected.Pattern = symbol
	redirected.Symbol = symbol
	redirected.NamedGap = util.FirstNonEmpty(strings.TrimSpace(req.NamedGap), "source-body evidence for repeated outline path")
	redirected.WhyNotAnswering = util.FirstNonEmpty(strings.TrimSpace(req.WhyNotAnswering), "need a declaration body from the outlined file rather than another outline")

	raw := marshalToolRequestRaw(redirected)

	if raw == "" || normalizedToolRequestRaw(redirected) == normalizedToolRequestRaw(req) {
		return workspace.ToolRequest{}, "", "", false
	}

	return redirected, raw, "repeated file_outline upgraded to source-body read_symbol", true
}

func priorFileOutlineForPath(path string, results []workspace.ToolResult) bool {
	path = agentutil.NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameCodeNavigate || strings.TrimSpace(result.Error) != "" {
			continue
		}

		text := strings.ToLower(strings.Join([]string{result.Summary, result.Output}, "\n"))
		if !strings.Contains(text, "file_outline") {
			continue
		}

		for _, candidate := range pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(strings.Join([]string{result.Summary, result.Output}, "\n"))) {
			if agentutil.NormalizeRunnerPath(candidate) == path {
				return true
			}
		}
	}

	return false
}

func readOnlyDefinitionToReadSymbolRewriteRequest(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief) (workspace.ToolRequest, string, string, bool) {
	if runnerBriefRequiresMutation(brief) || strings.TrimSpace(req.Tool) != workspace.ToolNameCodeNavigate {
		return workspace.ToolRequest{}, "", "", false
	}

	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) &&
		!workerruntime.RuntimeContractIsArchitecture(brief.Contract) &&
		!workerruntime.RuntimeContractIsDirectAnswer(brief.Contract) {

		return workspace.ToolRequest{}, "", "", false
	}

	capability := strings.TrimSpace(util.FirstNonEmpty(req.Capability, req.Operation, req.Action))
	if capability != "find_definition" && capability != "definition" {
		return workspace.ToolRequest{}, "", "", false
	}

	path := agentutil.NormalizeRunnerPath(req.Path)
	symbol := strings.TrimSpace(util.FirstNonEmpty(req.Pattern, req.Symbol, req.Query))

	if path == "" || symbol == "" {
		return workspace.ToolRequest{}, "", "", false
	}

	redirected := req

	redirected.Path = path
	redirected.Capability = "read_symbol"
	redirected.Operation = ""
	redirected.Action = ""
	redirected.Pattern = symbol
	redirected.Symbol = symbol
	redirected.NamedGap = util.FirstNonEmpty(strings.TrimSpace(req.NamedGap), "source-body evidence for definition target")
	redirected.WhyNotAnswering = util.FirstNonEmpty(strings.TrimSpace(req.WhyNotAnswering), "definition location is anchor evidence; the answer needs the declaration body")

	raw := marshalToolRequestRaw(redirected)

	if raw == "" || normalizedToolRequestRaw(redirected) == normalizedToolRequestRaw(req) {
		return workspace.ToolRequest{}, "", "", false
	}

	return redirected, raw, "read-only definition probe upgraded to source-body read_symbol", true
}

func isGeneratedDiscoveryContextArtifactPath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))

	lower = strings.TrimPrefix(lower, "./")

	return strings.HasPrefix(lower, ".idiom/discovery/queries/") ||
		strings.HasPrefix(lower, ".idiom/comprehension/views/") ||
		strings.HasPrefix(lower, ".codex/discovery/")
}

func latestDebugLogSearchHit(results []workspace.ToolResult) (debugLogSearchHit, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != workspace.ToolNameSearchContent || strings.TrimSpace(result.Error) != "" {
			continue
		}

		if hit, ok := parseDebugLogSearchHit(result); ok {
			return hit, true
		}
	}

	return debugLogSearchHit{}, false
}

func parseDebugLogSearchHit(result workspace.ToolResult) (debugLogSearchHit, bool) {
	match := singleSearchContentSummaryRE.FindStringSubmatch(strings.TrimSpace(result.Summary))
	if len(match) != 3 {
		return debugLogSearchHit{}, false
	}

	line := parseRunnerInt(match[2])
	if line <= 0 {
		return debugLogSearchHit{}, false
	}

	hit := debugLogSearchHit{Path: strings.TrimSpace(match[1]), Line: line}
	firstLine := firstNonEmptyLine(result.Output)

	if firstLine == "" {
		return hit, true
	}

	parts := strings.SplitN(firstLine, " ", 2)
	if len(parts) == 2 && strings.HasPrefix(parts[0], "f") {
		hit.Handle = strings.TrimSpace(parts[0])
	}

	return hit, true
}

func hasDebugLogReadWindowForHit(results []workspace.ToolResult, hit debugLogSearchHit) bool {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
			continue
		}

		window, ok := parseDebugLogReadWindow(result)
		if !ok {
			continue
		}

		if hit.Handle != "" && window.Handle != "" && hit.Handle == window.Handle && hit.Line >= window.Start && hit.Line <= window.End {
			return true
		}

		if hit.Path != "" && window.Path == hit.Path && hit.Line >= window.Start && hit.Line <= window.End {
			return true
		}
	}

	return false
}

func debugLogReadWindowRequest(hit debugLogSearchHit) workspace.ToolRequest {
	start := hit.Line - 20
	if start < 1 {
		start = 1
	}

	end := hit.Line + 20
	req := workspace.ToolRequest{
		Tool:      workspace.ToolNameReadFile,
		StartLine: start,
		EndLine:   end,
	}

	if hit.Handle != "" {
		req.ID = hit.Handle
	} else {
		req.Path = hit.Path
	}

	return req
}

func debugLogRequestTargetsHit(req workspace.ToolRequest, hit debugLogSearchHit) bool {
	if hit.Handle != "" && strings.TrimSpace(req.ID) == hit.Handle {
		return true
	}

	return hit.Path != "" && strings.TrimSpace(req.Path) == hit.Path
}

func debugLogRequestCoversHit(req workspace.ToolRequest, hit debugLogSearchHit) bool {
	if hit.Line <= 0 {
		return false
	}

	start := req.StartLine
	end := req.EndLine

	if start <= 0 {
		start = hit.Line
	}

	if end <= 0 {
		end = start + 80
	}

	return hit.Line >= start && hit.Line <= end
}

func parseDebugLogReadWindow(result workspace.ToolResult) (debugLogReadWindow, bool) {
	summary := strings.TrimSpace(result.Summary)
	if !strings.HasPrefix(summary, "Read ") {
		return debugLogReadWindow{}, false
	}

	body := strings.TrimPrefix(summary, "Read ")
	linesIdx := strings.Index(body, " lines ")

	if linesIdx <= 0 {
		return debugLogReadWindow{}, false
	}

	left := strings.TrimSpace(body[:linesIdx])
	right := body[linesIdx+len(" lines "):]
	rangeField := right

	if space := strings.IndexByte(rangeField, ' '); space >= 0 {
		rangeField = rangeField[:space]
	}

	rangeParts := strings.SplitN(rangeField, "-", 2)
	if len(rangeParts) != 2 {
		return debugLogReadWindow{}, false
	}

	start := parseRunnerInt(rangeParts[0])
	end := parseRunnerInt(rangeParts[1])

	if start <= 0 || end < start {
		return debugLogReadWindow{}, false
	}

	window := debugLogReadWindow{Start: start, End: end}
	leftParts := strings.SplitN(left, " ", 2)

	if len(leftParts) == 2 && strings.HasPrefix(leftParts[0], "f") {
		window.Handle = strings.TrimSpace(leftParts[0])
		window.Path = strings.TrimSpace(leftParts[1])

		return window, window.Path != ""
	}

	window.Path = left

	return window, window.Path != ""
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}

	return ""
}

func parseRunnerInt(s string) int {
	value := 0

	for _, ch := range strings.TrimSpace(s) {
		if ch < '0' || ch > '9' {
			return 0
		}

		value = value*10 + int(ch-'0')
	}

	return value
}

func hideBroadDiscoveryTools(hidden map[string]bool) {
	for _, tool := range broadDiscoveryToolNames() {
		hidden[tool] = true
	}
}

func broadDiscoveryToolNames() []string {
	return []string{
		workspace.ToolNameListFiles,
		workspace.ToolNameDiscoveryQuery, workspace.ToolNameDependencyGraph, workspace.ToolNameCtagsSymbols, workspace.ToolNameSemgrepScan, workspace.ToolNameJoernScan,
		"go_overview", "go_entrypoints",
		"cpp_overview", "cpp_entrypoints",
		"python_overview", "python_entrypoints",
		"ts_overview", "ts_entrypoints",
		"rust_overview", "rust_entrypoints",
		"java_overview", "java_entrypoints",
	}
}

func (r *Runner) augmentReadOnlyChildPrompt(prompt string) string {
	guidance := []string{
		"A final answer is available once the evidence is sufficient. After readiness, take another read only for an exact material missing claim; do not continue reading for completeness — but when the delegated question asks about a mechanism's thresholds, derived values, or configurable parameters, reading the symbol body that defines them is part of understanding the mechanism, not optional completeness reading.",
		"Treat inherited instructions to explore directories, entrypoints, or patterns as background only unless the delegated question explicitly requires them.",
		"Use repository-relative workspace paths in the answer. Do not rely on transient file handles such as f23 or tool-local aliases.",
		"Prefer targeted symbol tools and bounded file reads over workspace-wide overviews.",
	}
	if namedPaths := pathparse.MentionedWorkspacePaths(prompt); len(namedPaths) > 0 {
		guidance = append(guidance, fmt.Sprintf("Start from these named workspace paths: %s.", strings.Join(namedPaths, ", ")))
	}

	return strings.TrimSpace(prompt) + "\n\nScope guidance:\n- " + strings.Join(guidance, "\n- ")
}

func (r *Runner) completeWithoutMoreTools(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	opts llama.ChatOptions,
	collectedResults []workspace.ToolResult,
	metrics *runnerMetrics,
) (string, error) {

	return r.completeWithoutMoreToolsWithReason(ctx, turnMessages, opts, collectedResults, "Tool budget is exhausted. Tools are now disabled.", metrics)
}

func answerFirstNoToolsPrompt(reason string) string {
	reason = strings.TrimSpace(reason)

	contract := "Final answer contract: tools are unavailable now. Write the normal final answer from the evidence already gathered. Do not emit XML tool-call tags, tool JSON, or tool-request syntax of any kind — write only plain prose. If the evidence supports a concrete answer, answer directly and do not lead with budget, blocker, partial-findings, or recommended-next-step framing. If one exact claim remains unsupported, state that uncertainty after the supported answer with the smallest useful next action.\n\nA good answer explains the mechanism and names the evidence: state what happens, which code paths are involved, and cite the files or functions you inspected. For example: 'Post-write validation runs through source_sanity (runner_verification.go:120-200) which checks changed files with go vet. The implement lane calls this directly after each write, while plan-execute defers verification to the review-repair phase.' Do not dump raw function signatures — synthesize what you learned."

	if reason == "" {
		return contract
	}

	if strings.Contains(reason, "Final answer contract:") {
		return reason
	}

	return strings.TrimSpace(reason + "\n\n" + contract)
}

func (r *Runner) completeWithoutMoreToolsWithReason(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	opts llama.ChatOptions,
	collectedResults []workspace.ToolResult,
	reason string,
	metrics *runnerMetrics,
) (string, error) {

	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "Tools are now disabled."
	}

	reason = answerFirstNoToolsPrompt(reason)
	_ = r.evidenceEngine().ClosureDecision(r.RuntimeBrief, collectedResults)

	preferModelSynthesis := shouldPreferModelBudgetSynthesis(r.agentName(), r.RuntimeBrief, collectedResults)

	if !preferModelSynthesis && postReadinessForcedCompletionReason(reason) && shouldPreferDeterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults) {
		if answer, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			if !strings.HasPrefix(strings.TrimSpace(answer), "<<QUESTION>>") {
				return answer, nil
			}
		}
	}

	if !preferModelSynthesis {
		if answer, ok := immediateBudgetFallbackAnswer(r.agentName(), r.RuntimeBrief, reason, collectedResults); ok {
			return answer, nil
		}
	}

	if runnerBriefRequiresMutation(r.RuntimeBrief) && shouldPreferDeterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults) {
		if answer, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return answer, nil
		}
	}

	if preferModelSynthesis {
		reason = strings.TrimSpace(reason + "\nUse the evidence already gathered to answer the delegated question directly. Do not return a partial-findings template if a concise grounded answer is possible.")
		if paths := delegatedClosureEvidencePaths(r.RuntimeBrief.Objective, collectedResults, 4); len(paths) > 0 {
			reason = strings.TrimSpace(reason + "\nCarry forward the inspected implementation paths explicitly in the answer: " + summarizePostChildEvidencePaths(paths, 4) + ". Do not collapse the answer to a single file when the evidence already spans multiple relevant implementation paths.")
		}
	}

	// If any task_ledger items are still in_progress, prepend a closure
	// hint so the forced-completion reply actually resolves them instead
	// of leaving dangling in_progress todos in the transcript.
	if hint := taskLedgerClosureHint(collectedResults); hint != "" {
		reason = hint + "\n\n" + reason
	}

	turnMessages = append(turnMessages, llama.ChatMessage{
		Role:    "system",
		Content: reason,
	})

	if metrics != nil {
		metrics.ModelCalls++
	}

	// Strip orphaned tool_calls before the final API call so that
	// strict backends (DeepSeek) never see an assistant tool_calls
	// message without a matching role:"tool" response.
	turnMessages = stripOrphanedToolCalls(turnMessages)

	modelStart := time.Now()
	textOnlyOpts := textOnlyChatOptions(opts, r.RuntimeBrief)
	requestMessages := chatMessagesForOptions(turnMessages, textOnlyOpts)

	assistantMessage, err := r.Client.CompleteChatMessage(ctx, requestMessages, textOnlyOpts)
	if err != nil {
		return "", err
	}

	if metrics != nil {
		metrics.addUsage(assistantMessage.Usage)
	}

	r.emitModelCallTraceWithOptions(0, "forced_completion", requestMessages, assistantMessage, time.Since(modelStart).Milliseconds(), textOnlyOpts, r.supportsNativeTools(), collectedResults)

	reply := assistantMessage.Content
	r.emitThinkingTrace(0, "forced_completion_reply", assistantMessage, textOnlyOpts, false)

	preferDeterministicFallback := shouldPreferDeterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults)

	if len(assistantMessage.ToolCalls) > 0 {
		if preferDeterministicFallback {
			if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
				return fallback, nil
			}
		}

		if retry, ok, err := r.retryInvalidFinalAnswer(ctx, turnMessages, assistantMessage, textOnlyOpts, collectedResults, metrics, "native tool call"); err != nil {
			return "", err
		} else if ok {
			return retry, nil
		}

		if fallback, ok, err := r.repairOrFallbackFinalAnswer(ctx, turnMessages, textOnlyOpts, collectedResults, metrics); err != nil {
			return "", err
		} else if ok {
			return fallback, nil
		}

		return "", fmt.Errorf("tool loop exhausted and model still requested another tool instead of answering")
	}

	if looksLikeTextualToolCall(reply) {
		if preferDeterministicFallback {
			if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
				return fallback, nil
			}
		}

		if retry, ok, err := r.retryInvalidFinalAnswer(ctx, turnMessages, assistantMessage, textOnlyOpts, collectedResults, metrics, "textual tool call"); err != nil {
			return "", err
		} else if ok {
			return retry, nil
		}

		if fallback, ok, err := r.repairOrFallbackFinalAnswer(ctx, turnMessages, textOnlyOpts, collectedResults, metrics); err != nil {
			return "", err
		} else if ok {
			return fallback, nil
		}

		return "", fmt.Errorf("tool loop exhausted and model still emitted textual tool call markup instead of answering")
	}

	if _, _, ok, parseErr := workspace.ParseToolRequest(reply); parseErr != nil {
		if preferDeterministicFallback {
			if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
				return fallback, nil
			}
		}

		if retry, retryOK, err := r.retryInvalidFinalAnswer(ctx, turnMessages, assistantMessage, textOnlyOpts, collectedResults, metrics, "malformed tool request"); err != nil {
			return "", err
		} else if retryOK {
			return retry, nil
		}

		if fallback, ok, err := r.repairOrFallbackFinalAnswer(ctx, turnMessages, textOnlyOpts, collectedResults, metrics); err != nil {
			return "", err
		} else if ok {
			return fallback, nil
		}

		return "", parseErr
	} else if ok {
		if preferDeterministicFallback {
			if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
				return fallback, nil
			}
		}

		if retry, retryOK, err := r.retryInvalidFinalAnswer(ctx, turnMessages, assistantMessage, textOnlyOpts, collectedResults, metrics, "tool request"); err != nil {
			return "", err
		} else if retryOK {
			return retry, nil
		}

		if fallback, ok, err := r.repairOrFallbackFinalAnswer(ctx, turnMessages, textOnlyOpts, collectedResults, metrics); err != nil {
			return "", err
		} else if ok {
			return fallback, nil
		}

		return "", fmt.Errorf("tool loop exhausted and model still requested another tool instead of answering")
	}

	answer, err := r.ensureNonEmptyAnswer(ctx, turnMessages, assistantMessage, textOnlyOpts, collectedResults, metrics)
	if err != nil {
		return "", err
	}

	if _, rejected := r.completionGateRejection(answer, collectedResults, 0, nil, false); rejected {
		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return fallback, nil
		}

		// No deterministic fallback available — return a clean user-facing
		// message instead of an error so the user sees something useful.
		return "I wasn't able to produce a clean answer from the evidence. Could you rephrase or narrow the question?", nil
	}

	if _, rejected := r.readOnlyNonSubstantiveCompletionRejection(answer, collectedResults); rejected {
		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return fallback, nil
		}

		return "I wasn't able to produce a clean answer from the evidence. Could you rephrase or narrow the question?", nil
	}

	if finalAnswerNeedsDecoration(answer) {
		if repaired, ok, err := r.repairInternalFinalAnswer(ctx, turnMessages, textOnlyOpts, answer, collectedResults, metrics); err != nil {
			return "", err
		} else if ok {
			if presentable, ok := r.presentableFallbackCandidate(collectedResults, repaired); ok {
				return presentable, nil
			}
		}

		if presentable, ok := r.presentableFallbackCandidate(collectedResults, answer); ok {
			return presentable, nil
		}
	}

	return answer, nil
}

func postReadinessForcedCompletionReason(reason string) bool {
	lower := strings.ToLower(strings.TrimSpace(reason))

	return strings.Contains(lower, "post-readiness") &&
		strings.Contains(lower, "tools are now disabled")
}

func (r *Runner) repairOrFallbackFinalAnswer(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	opts llama.ChatOptions,
	collectedResults []workspace.ToolResult,
	metrics *runnerMetrics,
) (string, bool, error) {

	rawFallback, ok := synthesizeNoToolsFallbackAnswer(r.agentName(), r.RuntimeBrief, collectedResults)
	if !ok {
		return "", false, nil
	}

	if finalAnswerNeedsDecoration(rawFallback) && r.userFacingFinalAnswer() {
		if repaired, ok, err := r.repairInternalFinalAnswer(ctx, turnMessages, opts, rawFallback, collectedResults, metrics); err != nil {
			return "", false, err
		} else if ok {
			if presentable, ok := r.presentableFallbackCandidate(collectedResults, repaired); ok {
				return presentable, true, nil
			}
		}
	}

	if presentable, ok := r.presentableFallbackCandidate(collectedResults, rawFallback); ok {
		return presentable, true, nil
	}

	return "", false, nil
}

func (r *Runner) repairInternalFinalAnswer(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	opts llama.ChatOptions,
	internalAnswer string,
	collectedResults []workspace.ToolResult,
	metrics *runnerMetrics,
) (string, bool, error) {

	internalAnswer = strings.TrimSpace(internalAnswer)
	if internalAnswer == "" {
		return "", false, nil
	}

	repairMessages := append([]llama.ChatMessage(nil), turnMessages...)

	repairMessages = append(repairMessages, llama.ChatMessage{
		Role: "system",
		Content: "The previous final synthesis still did not produce a usable user-facing answer. Tools remain disabled. " +
			"The text below is an internal evidence summary, not a final-answer template. Rewrite it into the normal final answer to the user's request using only claims supported by that evidence and the prior tool results. " +
			"Do not mention evidence packets, probes, tool calls, budgets, fallback behavior, or internal sections. If one exact claim is unsupported, state that uncertainty at the end.\n\nInternal evidence summary:\n" + internalAnswer,
	})

	if metrics != nil {
		metrics.ModelCalls++
	}

	// Strip orphaned tool_calls before the final API call so that
	// strict backends (DeepSeek) never see an assistant tool_calls
	// message without a matching role:"tool" response.
	repairMessages = stripOrphanedToolCalls(repairMessages)

	modelStart := time.Now()
	requestMessages := chatMessagesForOptions(repairMessages, opts)

	repairMessage, err := r.Client.CompleteChatMessage(ctx, requestMessages, opts)
	if err != nil {
		return "", false, err
	}

	if metrics != nil {
		metrics.addUsage(repairMessage.Usage)
	}

	r.emitModelCallTraceWithOptions(0, "fallback_final_answer_retry", requestMessages, repairMessage, time.Since(modelStart).Milliseconds(), opts, r.supportsNativeTools(), collectedResults)
	r.emitThinkingTrace(0, "fallback_final_answer_retry_reply", repairMessage, opts, false)

	if len(repairMessage.ToolCalls) > 0 {
		return "", false, nil
	}

	content := strings.TrimSpace(repairMessage.Content)
	if content == "" || looksLikeTextualToolCall(content) {
		return "", false, nil
	}

	if _, _, ok, parseErr := workspace.ParseToolRequest(content); ok || parseErr != nil {
		return "", false, nil
	}

	if cleaned, ok := cleanedSubstantiveFinalAnswer(content); ok {
		content = cleaned
	}

	if finalAnswerNeedsDecoration(content) {
		return "", false, nil
	}

	// Run the completion gate on the repaired answer. The retry path
	// (retryInvalidFinalAnswer) already runs this gate, but this
	// fallback-repair path was skipping it, allowing garbage answers
	// like raw tool result transcripts to reach the user.
	if _, rejected := r.completionGateRejection(content, collectedResults, 0, nil, false); rejected {
		return "", false, nil
	}

	r.LastAssistantMessage = repairMessage

	return content, true, nil
}

func (r *Runner) retryInvalidFinalAnswer(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	reply llama.ChatMessage,
	opts llama.ChatOptions,
	collectedResults []workspace.ToolResult,
	metrics *runnerMetrics,
	reason string,
) (string, bool, error) {

	retryMessages := append([]llama.ChatMessage(nil), turnMessages...)
	if strings.TrimSpace(reply.Content) != "" || strings.TrimSpace(reply.ReasoningContent) != "" {
		retryMessages = append(retryMessages, assistantHistoryMessageForOptions(llama.ChatMessage{
			Role:             util.FirstNonEmpty(strings.TrimSpace(reply.Role), "assistant"),
			Content:          reply.Content,
			ReasoningContent: reply.ReasoningContent,
		}, opts))
	}

	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "tool request"
	}

	retryMessages = append(retryMessages, llama.ChatMessage{
		Role: "system",
		Content: "Your previous final response was a " + reason + ", but tools are disabled for final synthesis. " +
			"Do not emit tool JSON, XML, tool blocks, or another probe. Reply in normal prose from the evidence already gathered. " +
			"If the evidence is sufficient, answer directly; if one exact claim remains unsupported, add that uncertainty after the supported answer in one sentence. " +
			"Do not list internal tool traces, exact probes, or inspected-path inventories unless they are necessary to explain the answer.",
	})

	if metrics != nil {
		metrics.ModelCalls++
	}

	// Strip orphaned tool_calls before the final API call so that
	// strict backends (DeepSeek) never see an assistant tool_calls
	// message without a matching role:"tool" response.
	retryMessages = stripOrphanedToolCalls(retryMessages)

	modelStart := time.Now()
	requestMessages := chatMessagesForOptions(retryMessages, opts)

	retryMessage, err := r.Client.CompleteChatMessage(ctx, requestMessages, opts)
	if err != nil {
		return "", false, err
	}

	if metrics != nil {
		metrics.addUsage(retryMessage.Usage)
	}

	r.emitModelCallTraceWithOptions(0, "invalid_final_answer_retry", requestMessages, retryMessage, time.Since(modelStart).Milliseconds(), opts, r.supportsNativeTools(), collectedResults)
	r.emitThinkingTrace(0, "invalid_final_answer_retry_reply", retryMessage, opts, false)

	if len(retryMessage.ToolCalls) > 0 {
		return "", false, nil
	}

	content := strings.TrimSpace(retryMessage.Content)
	if content == "" || looksLikeTextualToolCall(content) {
		return "", false, nil
	}

	if _, _, ok, parseErr := workspace.ParseToolRequest(content); ok || parseErr != nil {
		return "", false, nil
	}

	if cleaned, ok := cleanedSubstantiveFinalAnswer(content); ok {
		content = cleaned
	}

	if _, rejected := r.completionGateRejection(content, collectedResults, 0, nil, false); rejected {
		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return fallback, true, nil
		}

		return "", false, nil
	}

	if finalAnswerNeedsDecoration(content) {
		if repaired, ok, err := r.repairInternalFinalAnswer(ctx, turnMessages, opts, content, collectedResults, metrics); err != nil {
			return "", false, err
		} else if ok {
			if presentable, ok := r.presentableFallbackCandidate(collectedResults, repaired); ok {
				return presentable, true, nil
			}
		}

		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return fallback, true, nil
		}

		return "", false, nil
	}

	r.LastAssistantMessage = retryMessage

	return content, true, nil
}

func shouldPreferDeterministicNoToolsAnswer(agentName string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if runnerBriefRequiresMutation(brief) {
		return len(changedPathsFromToolResults(results)) > 0 ||
			len(brief.RequiredEditPaths) > 0 ||
			len(brief.RequiredEditSnippets) > 0 ||
			targetPacketSupportsWrite(brief)
	}

	if runnerVerificationRequired(brief) {
		return false
	}

	return false
}

func deterministicNoToolsAnswer(agentName string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, bool) {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if !runnerBriefRequiresMutation(brief) && workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return "", false
	}

	answer, ok := synthesizeNoToolsFallbackAnswer(agentName, brief, results)
	if !ok {
		return "", false
	}

	return presentableFallbackCandidate(agentName, brief, results, answer)
}

func presentableFallbackCandidate(agentName string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, answer string) (string, bool) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", false
	}

	if runnerBriefRequiresMutation(brief) {
		return answer, true
	}

	return readOnlyClosureDecisionForResults(brief, results).PresentableFallbackCandidate(agentName, answer)
}

func presentableOrReadOnlyNonAnswerReplacement(agentName string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, answer string) (string, bool) {
	return readOnlyClosureDecisionForResults(brief, results).PresentableOrReadOnlyNonAnswerReplacement(agentName, answer)
}

func presentableBriefConversationFallbackCandidate(agentName string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, bool) {
	return readOnlyClosureDecisionForResults(brief, results).PresentableBriefConversationFallbackCandidate(agentName)
}

func fallbackCandidateUsesReadOnlyNonAnswerReplacement(agentName string, brief workerruntime.WorkerRuntimeBrief) bool {
	if strings.HasPrefix(agentName, "child:") || strings.HasPrefix(agentName, "child_write:") {
		return false
	}

	return readOnlyDirectAnswerLane(brief) && !runnerBriefRequiresMutation(brief)
}

func shouldPreferModelBudgetSynthesis(agentName string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return false
	}

	if strings.HasPrefix(agentName, "child:") {
		return true
	}

	return postChildPacketClosureReadyWithBrief(brief, results) ||
		postChildSourceBodyPacketEvidenceAvailable(results)
}

func postChildSourceBodyPacketEvidenceAvailable(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameTask || strings.TrimSpace(result.Error) != "" {
			continue
		}

		if packet, ok := readOnlyEvidencePacketFromResult(result); ok {
			if len(packet.SourceBodyPaths) > 0 {
				return true
			}

			continue
		}

		if delegatedSourceBodyPacketHasGroundedFinding(result.Output) {
			return true
		}
	}

	return false
}

func runnerBriefAllowsExternalToolAppend(brief workerruntime.WorkerRuntimeBrief) bool {
	if strings.TrimSpace(brief.TaskFamily) == workerruntime.ArtifactFinalAnswerTaskFamily ||
		strings.TrimSpace(brief.Contract) == "artifact_final_answer" {

		return false
	}

	switch strings.TrimSpace(brief.PermissionProfile) {
	case string(workerruntime.PermissionPlanSubmissionReview), string(workerruntime.PermissionReadOnlyVerificationShell):
		return false
	}

	switch strings.TrimSpace(brief.TaskFamily) {
	case PlanThenExecuteReviewRepairTaskFamily, "debug_logs":
		return false
	default:
		return true
	}
}

func (r *Runner) advertisedAllowedTools(base []string) []string {
	if strings.TrimSpace(r.RuntimeBrief.TaskFamily) == workerruntime.ArtifactFinalAnswerTaskFamily ||
		strings.TrimSpace(r.RuntimeBrief.Contract) == "artifact_final_answer" {

		return nil
	}

	if workerruntime.PermissionProfileIsConversationOnly(r.RuntimeBrief.PermissionProfile) {
		// Even in conversation_only mode, expose project-defined external
		// tools so the model can call host-registered discovery commands
		// (e.g. project_search, api_access). Built-in mutating tools
		// stay hidden — that's what conversation_only is for.
		if r.Workspace == nil {
			return nil
		}

		if r.Workspace.ExternalToolsRegistry() == nil {
			return nil
		}

		names := r.Workspace.AvailableExternalToolNames()
		// Preserve order by filtering base to only externals; this also
		// drops any non-external entry that snuck into base via prior
		// pipeline mutations.
		known := make(map[string]struct{}, len(names))
		for _, n := range names {
			known[n] = struct{}{}
		}

		var out []string

		for _, name := range base {
			if _, ok := known[strings.TrimSpace(name)]; ok {
				out = append(out, name)
			}
		}

		// If base was empty (e.g. brief AllowedTools = nil), append
		// directly from the registry so the externals still surface.
		if len(out) == 0 {
			out = append(out, names...)
		}

		return out
	}

	allowed := append([]string(nil), base...)
	if len(r.RuntimeBrief.AllowedTools) > 0 {
		available := make(map[string]string, len(base))
		for _, tool := range base {
			trimmed := strings.TrimSpace(tool)
			if trimmed != "" {
				available[trimmed] = tool
			}
		}

		filtered := make([]string, 0, len(r.RuntimeBrief.AllowedTools))
		seen := make(map[string]struct{}, len(r.RuntimeBrief.AllowedTools))

		for _, tool := range r.RuntimeBrief.AllowedTools {
			tool = strings.TrimSpace(tool)
			if tool == "" {
				continue
			}

			if _, ok := seen[tool]; ok {
				continue
			}

			if original, ok := available[tool]; ok {
				filtered = append(filtered, original)
				seen[tool] = struct{}{}
			}
		}

		allowed = filtered
	}

	if r.DisableTaskTool {
		filtered := allowed[:0]
		for _, tool := range allowed {
			if tool != workspace.ToolNameTask {
				filtered = append(filtered, tool)
			}
		}

		allowed = filtered
	}

	return allowed
}

func textOnlyChatOptions(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief) llama.ChatOptions {
	opts.Tools = nil
	opts.ToolChoice = ""

	thinkingOff := false

	opts.EnableThinking = &thinkingOff
	opts.ChatTemplateKwargs = true
	opts.PreserveThinking = false

	limit := textOnlyMaxTokensForBrief(brief)

	if opts.MaxTokens <= 0 || opts.MaxTokens > limit {
		opts.MaxTokens = limit
	}

	return opts
}

func readOnlyEvidenceAvailableTerminalChatOptions(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) llama.ChatOptions {
	if !readOnlyEvidenceAvailableTerminalVisibleOutput(brief, results) {
		return opts
	}

	thinkingOff := false

	opts.EnableThinking = &thinkingOff
	opts.ChatTemplateKwargs = true
	opts.PreserveThinking = false

	return opts
}

func readOnlyEvidenceAvailableTerminalVisibleOutput(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) || artifactFinalAnswerBrief(brief) {
		return false
	}

	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return false
	}

	if !readOnlyDirectAnswerLane(brief) {
		return false
	}

	return genericReadOnlyEvidenceReady(brief, results)
}

func textOnlyMaxTokensForBrief(brief workerruntime.WorkerRuntimeBrief) int {
	switch strings.TrimSpace(brief.Contract) {
	case string(workerruntime.ContractArtifactFinalAnswer):
		return 8192
	case string(workerruntime.ContractDirectAnswer):
		return 1024
	case string(workerruntime.ContractGroundedExplanation), string(workerruntime.ContractGroundedEvidence), string(workerruntime.ContractLocateTargets):
		return 1536
	case string(workerruntime.ContractArchitecture), string(workerruntime.ContractReviewFindings):
		return 2048
	default:
		return 768
	}
}

// IsTextualToolCall returns true when text looks like tool-call markup
// that should never reach the user-facing presentation layer.
func IsTextualToolCall(text string) bool {
	return looksLikeTextualToolCall(text)
}

// looksLikeXMLToolCall returns true when the text contains XML/HTML
// tool-call markup tags (llama.cpp DSML, janus, n_tool, etc.).
// Does not match line-oriented key-value tool requests.
func looksLikeXMLToolCall(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}

	return strings.Contains(lower, "<tool_call") ||
		strings.Contains(lower, "</tool_call>") ||
		strings.Contains(lower, "<function=") ||
		strings.Contains(lower, "</function>") ||
		strings.Contains(lower, "<invoke") ||
		strings.Contains(lower, "</invoke>") ||
		strings.Contains(lower, "<janus_tool") ||
		strings.Contains(lower, "</janus_tool") ||
		strings.Contains(lower, "<janus_invoke") ||
		strings.Contains(lower, "</janus_invoke") ||
		strings.Contains(lower, "<n_tool") ||
		strings.Contains(lower, "</n_tool") ||
		strings.Contains(lower, "<n_invoke") ||
		strings.Contains(lower, "</n_invoke") ||
		strings.Contains(lower, "<n_parameter") ||
		strings.Contains(lower, "</n_parameter") ||
		strings.Contains(lower, "dsml")
}

func looksLikeTextualToolCall(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}

	if strings.Contains(lower, "<tool_call") ||
		strings.Contains(lower, "</tool_call>") ||
		strings.Contains(lower, "<function=") ||
		strings.Contains(lower, "</function>") ||
		strings.Contains(lower, "<invoke") ||
		strings.Contains(lower, "</invoke>") ||
		strings.Contains(lower, "<janus_tool") ||
		strings.Contains(lower, "</janus_tool") ||
		strings.Contains(lower, "<janus_invoke") ||
		strings.Contains(lower, "</janus_invoke") ||
		strings.Contains(lower, "<n_tool") ||
		strings.Contains(lower, "</n_tool") ||
		strings.Contains(lower, "<n_invoke") ||
		strings.Contains(lower, "</n_invoke") ||
		strings.Contains(lower, "<n_parameter") ||
		strings.Contains(lower, "</n_parameter") ||
		strings.Contains(lower, "dsml") ||
		strings.Contains(lower, "[calling tool:") {

		return true
	}

	return looksLikeLineOrientedToolRequest(lower)
}

func looksLikeLineOrientedToolRequest(lower string) bool {
	lines := strings.Split(lower, "\n")
	if len(lines) < 2 || !strings.HasPrefix(strings.TrimSpace(lines[0]), "tool:") {
		return false
	}

	toolFields := 0

	for _, line := range lines[1:] {
		field := strings.TrimSpace(line)
		if strings.Contains(field, `"tool"`) {
			return true
		}

		if strings.HasPrefix(field, "{") && strings.Contains(field, `"tool"`) {
			return true
		}

		switch {
		case strings.HasPrefix(field, "capability:"),
			strings.HasPrefix(field, "path:"),
			strings.HasPrefix(field, "pattern:"),
			strings.HasPrefix(field, "query:"),
			strings.HasPrefix(field, "task:"),
			strings.HasPrefix(field, "mode:"),
			strings.HasPrefix(field, "limit:"),
			strings.HasPrefix(field, "command:"),
			strings.HasPrefix(field, "args:"):
			toolFields++
		}
	}

	return toolFields > 0
}

func toolResultMadeProgress(req workspace.ToolRequest, result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Error) != "" {
		return false
	}

	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameTaskLedger:
		return strings.TrimSpace(result.Output) != ""
	case workspace.ToolNameEditFile, workspace.ToolNameWriteFile, workspace.ToolNameAppendFile, workspace.ToolNameMkdir, workspace.ToolNameGoMoveDeclarations, workspace.ToolNameApplyPatch:
		summary := strings.ToLower(strings.TrimSpace(result.Summary))
		return summary != "" && !strings.Contains(summary, "no-op")
	case workspace.ToolNameReadFile, workspace.ToolNameSearchFiles, workspace.ToolNameSearchContent, workspace.ToolNameRg,
		"go_file_outline", "go_find_symbol", "go_find_type", "go_find_const", "go_read_symbol",
		"go_find_package", "go_related_files", "go_workspace_symbol",
		"go_definition", "go_references", "go_implementations",
		"go_calls_from", "go_calls_into", "go_extract_body", "go_importers", "go_diagnostics":
		return strings.TrimSpace(result.Output) != "" || strings.TrimSpace(result.Summary) != ""
	case workspace.ToolNameShell:
		return strings.TrimSpace(result.Output) != "" || strings.TrimSpace(result.Summary) != ""
	default:
		return strings.TrimSpace(result.Output) != "" || strings.TrimSpace(result.Summary) != ""
	}
}

func isMissingPathListFailure(req workspace.ToolRequest, result workspace.ToolResult) bool {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameListFiles {
		return false
	}

	errText := strings.ToLower(strings.TrimSpace(result.Error))

	return strings.Contains(errText, "no such file or directory") || strings.Contains(errText, "cannot find the path")
}

// rewriteMissingRequiredNewFileProbe converts a "file not found" error
// on a probe targeting a required_edit_path, or a missing parent dir of
// required_edit_paths, into an actionable nudge: the planner can create
// those files via submit_plan's whole-file shape (replace_all=true with
// empty old_text + new_text=full body). Without this rewrite the planner
// sees the bare "no such file" error, treats it as a blocker, and gives
// up. That is the failure mode local Qwen hit on plan-execute dispatch.
//
// The rewrite preserves the result as an Error (so the planner can't
// use it as evidence of an actual file body) but replaces the message
// with one that names the right next step. We intentionally do NOT
// convert to a successful no-op: a successful probe of a non-existent
// path would leak into downstream "did the planner read this path"
// heuristics in misleading ways.
func rewriteMissingRequiredNewFileProbe(req workspace.ToolRequest, result workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) workspace.ToolResult {
	if !missingRequiredNewFileProbeTool(req.Tool) {
		return result
	}

	if strings.TrimSpace(result.Error) == "" {
		return result
	}

	lower := strings.ToLower(result.Error)
	if !strings.Contains(lower, "no such file") && !strings.Contains(lower, "does not exist") {
		return result
	}

	target := agentutil.NormalizeRunnerPath(strings.TrimSpace(req.Path))
	if target == "" {
		return result
	}

	matches := requiredNewFileProbeMatches(target, brief.RequiredEditPaths)
	if len(matches) == 0 {
		return result
	}

	if len(matches) == 1 && agentutil.NormalizeRunnerPath(matches[0]) == target {
		result.Summary = "Required new-file path does not exist yet"
		result.Error = fmt.Sprintf("path %q is in your required_edit_paths but does not exist on disk yet — this is a NEW FILE you must create. Do NOT keep trying to inspect it. In submit_plan, include a replace_all edit for %q with empty old_text, a complete new_text body from the brief or gathered evidence, and a specific purpose. Do not copy placeholder body text or schema examples. If the brief does not provide enough body evidence for a required source/test file, answer with <<QUESTION>> naming the missing body evidence. Large bodies are moved to artifact refs after submission.", target, target)

		return result
	}

	result.Summary = "Required new-file directory does not exist yet"
	result.Error = fmt.Sprintf("path %q is a missing parent for required_edit_paths: %s. These are NEW FILES you must create. Do NOT keep trying to inspect the missing directory. In submit_plan, include one replace_all edit per required new-file path with empty old_text and a full new_text body. If the brief does not provide enough body evidence for a required source/test file, answer with <<QUESTION>> naming that missing body evidence.", target, quotedRunnerStrings(matches))

	return result
}

func unavailableRequiredNewFileProbeNudge(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, manager *workspace.Manager) (string, string, bool) {
	if !missingRequiredNewFileProbeTool(req.Tool) {
		return "", "", false
	}

	target := agentutil.NormalizeRunnerPath(strings.TrimSpace(req.Path))
	if target == "" {
		return "", "", false
	}

	matches := requiredNewFileProbeMatches(target, brief.RequiredEditPaths)
	if len(matches) == 0 {
		return "", "", false
	}

	if !requiredNewFileProbeTargetMissing(target, manager) {
		return "", "", false
	}

	if len(matches) == 1 && agentutil.NormalizeRunnerPath(matches[0]) == target {
		return "Required new-file path does not exist yet",
			fmt.Sprintf("path %q is in your required_edit_paths but does not exist on disk yet — this is a NEW FILE you must create. The requested probe tool %q is not available in the current allowed tool surface, and probing is unnecessary. In submit_plan, include a replace_all edit for %q with empty old_text, a complete new_text body from the brief or gathered evidence, and a specific purpose. Do not copy placeholder body text or schema examples. If the brief does not provide enough body evidence for a required source/test file, answer with <<QUESTION>> naming the missing body evidence.", target, strings.TrimSpace(req.Tool), target),
			true
	}

	return "Required new-file directory does not exist yet",
		fmt.Sprintf("path %q is a missing parent for required_edit_paths: %s. These are NEW FILES you must create. The requested probe tool %q is not available in the current allowed tool surface, and probing is unnecessary. In submit_plan, include one replace_all edit per required new-file path with empty old_text and a full new_text body. If the brief does not provide enough body evidence for a required source/test file, answer with <<QUESTION>> naming that missing body evidence.", target, quotedRunnerStrings(matches), strings.TrimSpace(req.Tool)),
		true
}

func requiredNewFileProbeTargetMissing(target string, manager *workspace.Manager) bool {
	root := ""
	if manager != nil {
		root = strings.TrimSpace(manager.Root())
	}

	if root == "" {
		return true
	}

	path := target
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, filepath.FromSlash(path))
	}

	_, err := os.Stat(path)

	return os.IsNotExist(err)
}

func missingRequiredNewFileProbeTool(tool string) bool {
	tool = strings.TrimSpace(tool)
	if tool == workspace.ToolNameReadFile || tool == workspace.ToolNameListFiles {
		return true
	}

	return strings.HasSuffix(tool, "_file_outline")
}

func requiredNewFileProbeMatches(target string, requiredPaths []string) []string {
	target = strings.Trim(strings.TrimSpace(agentutil.NormalizeRunnerPath(target)), "/")
	if target == "" {
		return nil
	}

	var matches []string

	for _, p := range requiredPaths {
		path := strings.Trim(strings.TrimSpace(agentutil.NormalizeRunnerPath(p)), "/")
		if path == "" {
			continue
		}

		if path == target || strings.HasPrefix(path, target+"/") || filepath.Dir(path) == target {
			matches = util.AppendUniqueMulti(matches, path)
		}
	}

	return matches
}

func briefIsDocDeliverableLane(brief workerruntime.WorkerRuntimeBrief) bool {
	if len(brief.RequiredEditPaths) == 0 {
		return false
	}

	for _, p := range brief.RequiredEditPaths {
		if !writePathIsNonSource(p) {
			return false
		}
	}

	return true
}

func briefIsScratchpadDeliverableLane(brief workerruntime.WorkerRuntimeBrief) bool {
	if !briefIsDocDeliverableLane(brief) {
		return false
	}

	for _, p := range brief.RequiredEditPaths {
		normalized := agentutil.NormalizeRunnerPath(p)
		if !strings.HasPrefix(normalized, ".idiom/refine/") {
			return false
		}
	}

	return true
}

func scopedImplementationUsesBoundedSourceEdits(brief workerruntime.WorkerRuntimeBrief, paths []string) bool {
	if !implementationScopedEditSurfaceActive(brief, nil) || briefIsDocDeliverableLane(brief) {
		return false
	}

	for _, path := range paths {
		normalized := agentutil.NormalizeRunnerPath(path)
		if normalized == "" || writePathIsNonSource(normalized) {
			continue
		}

		if recoverySearchPathLooksEditableSource(normalized) {
			return true
		}
	}

	return false
}

// writeTouchesOnlyNonSourcePaths returns true when every path in a
// write result is a non-source file (markdown, plain text). Used to
// exempt doc-deliverable lanes from the post-write verification gate.
func writeTouchesOnlyNonSourcePaths(result workspace.ToolResult) bool {
	paths := changedPathsFromToolResult(result)
	if len(paths) == 0 {
		return false
	}

	for _, p := range paths {
		if !writePathIsNonSource(p) {
			return false
		}
	}

	return true
}

func mkdirTouchesOnlyRequiredPathParents(result workspace.ToolResult, requiredPaths []string) bool {
	if strings.TrimSpace(result.Tool) != workspace.ToolNameMkdir || len(requiredPaths) == 0 {
		return false
	}

	paths := changedPathsFromToolResult(result)
	if len(paths) == 0 {
		return false
	}

	for _, path := range paths {
		if !mkdirPathIsRequiredParent(path, requiredPaths) {
			return false
		}
	}

	return true
}

func mkdirPathIsRequiredParent(path string, requiredPaths []string) bool {
	path = strings.Trim(agentutil.NormalizeRunnerPath(path), "/")
	if path == "" {
		return false
	}

	for _, required := range requiredPaths {
		required = strings.Trim(agentutil.NormalizeRunnerPath(required), "/")
		if required == "" || !writePathIsNonSource(required) {
			continue
		}

		parent := strings.Trim(filepath.ToSlash(filepath.Dir(required)), "/")
		if path == parent || strings.HasPrefix(parent, path+"/") {
			return true
		}
	}

	return false
}

// writePathIsNonSource returns true for paths whose verification has
// no source-aware analog. Conservative list — adding extensions risks
// silently bypassing real verification gates.
func writePathIsNonSource(path string) bool {
	p := strings.ToLower(strings.TrimSpace(path))
	if p == "" {
		return false
	}

	return strings.HasSuffix(p, ".md") ||
		strings.HasSuffix(p, ".markdown") ||
		strings.HasSuffix(p, ".txt") ||
		strings.HasSuffix(p, ".rst")
}

func (r *Runner) ensureNonEmptyAnswer(
	ctx context.Context,
	turnMessages []llama.ChatMessage,
	reply llama.ChatMessage,
	opts llama.ChatOptions,
	collectedResults []workspace.ToolResult,
	metrics *runnerMetrics,
) (string, error) {

	trimmed := strings.TrimSpace(reply.Content)
	if trimmed != "" {
		if looksLikeTextualToolCall(trimmed) {
			if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
				return fallback, nil
			}

			return "", fmt.Errorf("%s agent emitted textual tool call markup instead of a non-empty answer", r.agentName())
		}

		r.LastAssistantMessage = reply

		return trimmed, nil
	}

	retryMessages := append([]llama.ChatMessage(nil), turnMessages...)
	if strings.TrimSpace(reply.Content) != "" || strings.TrimSpace(reply.ReasoningContent) != "" {
		retryMessages = append(retryMessages, assistantHistoryMessageForOptions(llama.ChatMessage{
			Role:             util.FirstNonEmpty(strings.TrimSpace(reply.Role), "assistant"),
			Content:          reply.Content,
			ReasoningContent: reply.ReasoningContent,
		}, opts))
	}

	retryMessages = append(retryMessages, llama.ChatMessage{
		Role:    "system",
		Content: emptyAnswerRetryPrompt(r.RuntimeBrief),
	})

	if metrics != nil {
		metrics.ModelCalls++
	}

	// Strip orphaned tool_calls before the final API call so that
	// strict backends (DeepSeek) never see an assistant tool_calls
	// message without a matching role:"tool" response.
	retryMessages = stripOrphanedToolCalls(retryMessages)

	modelStart := time.Now()
	textOnlyOpts := textOnlyChatOptions(opts, r.RuntimeBrief)
	requestMessages := chatMessagesForOptions(retryMessages, textOnlyOpts)

	retryMessage, err := r.Client.CompleteChatMessage(ctx, requestMessages, textOnlyOpts)
	if err != nil {
		return "", err
	}

	if metrics != nil {
		metrics.addUsage(retryMessage.Usage)
	}

	r.emitModelCallTraceWithOptions(0, "empty_answer_retry", requestMessages, retryMessage, time.Since(modelStart).Milliseconds(), textOnlyOpts, r.supportsNativeTools(), collectedResults)

	retryReply := retryMessage.Content
	r.emitThinkingTrace(0, "empty_answer_retry_reply", retryMessage, textOnlyOpts, false)

	if len(retryMessage.ToolCalls) > 0 {
		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return fallback, nil
		}

		return "", fmt.Errorf("%s agent emitted a tool request instead of a non-empty answer", r.agentName())
	}

	if looksLikeTextualToolCall(retryReply) {
		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return fallback, nil
		}

		return "", fmt.Errorf("%s agent emitted textual tool call markup instead of a non-empty answer", r.agentName())
	}

	if _, _, ok, parseErr := workspace.ParseToolRequest(retryReply); parseErr != nil {
		return "", parseErr
	} else if ok {
		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return fallback, nil
		}

		return "", fmt.Errorf("%s agent emitted a tool request instead of a non-empty answer", r.agentName())
	}

	retryTrimmed := strings.TrimSpace(retryReply)
	if retryTrimmed == "" {
		if fallback, ok := deterministicNoToolsAnswer(r.agentName(), r.RuntimeBrief, collectedResults); ok {
			return fallback, nil
		}

		return "", fmt.Errorf("%s agent returned an empty response", r.agentName())
	}

	r.LastAssistantMessage = retryMessage

	return retryTrimmed, nil
}

func extractRunnerFilePath(summary string) string {
	path := extractReadFilePath(summary)
	if path == "" {
		return ""
	}

	fields := strings.Fields(path)
	if len(fields) >= 2 && looksLikeRunnerFileHandle(fields[0]) {
		return strings.TrimSpace(strings.Join(fields[1:], " "))
	}

	return path
}

func extractSemanticInspectionPath(summary string) string {
	trimmed := strings.TrimSpace(summary)
	if trimmed == "" {
		return ""
	}

	for _, marker := range []string{" lines ", ". Use "} {
		idx := strings.Index(trimmed, marker)
		if idx > 0 {
			trimmed = strings.TrimSpace(trimmed[:idx])
			break
		}
	}

	for _, prefix := range []string{"Read ", "Extracted ", "Outlined "} {
		if !strings.HasPrefix(trimmed, prefix) {
			continue
		}

		for _, separator := range []string{" in ", " from "} {
			idx := strings.LastIndex(trimmed, separator)
			if idx <= len(prefix) {
				continue
			}

			return strings.TrimSpace(trimmed[idx+len(separator):])
		}
	}

	return ""
}

func looksLikeRunnerFileHandle(token string) bool {
	if len(token) < 2 || token[0] != 'f' {
		return false
	}

	for _, r := range token[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}

// rewriteVisibilityNote returns a short system-message body explaining that
// the requested tool was redirected to a different tool by harness routing
// rules. Generic across rewrite kinds (delegated bootstrap, verification
// bootstrap, debug-log preflight, etc.) — it states the fact without
// hard-picking the next tool, leaving the model free to act on the result.
func rewriteVisibilityNote(originalTool, rewrittenTool string) string {
	original := strings.TrimSpace(originalTool)
	rewritten := strings.TrimSpace(rewrittenTool)

	switch {
	case original != "" && rewritten != "" && original != rewritten:
		return fmt.Sprintf("Note: your `%s` call was redirected to `%s` by lane routing. The rewritten call will execute and return its result; use that result, not another retry of `%s`.", original, rewritten, original)
	case rewritten != "":
		return fmt.Sprintf("Note: your tool call was redirected to `%s` by lane routing. The rewritten call will execute and return its result.", rewritten)
	default:
		return "Note: your tool call was rewritten by lane routing. The rewritten call will execute and return its result."
	}
}

// taskLedgerClosureHint inspects the collected tool-results set and, if the
// most recent task_ledger state still has any items flagged in_progress,
// returns a short reminder asking the model to close them with a final
// status in the upcoming reply. Returns the empty string when there is
// nothing open.
func taskLedgerClosureHint(results []workspace.ToolResult) string {
	var latest *workspace.ToolResult

	for i := len(results) - 1; i >= 0; i-- {
		if results[i].Tool == workspace.ToolNameTaskLedger && strings.TrimSpace(results[i].Error) == "" {
			copyResult := results[i]

			latest = &copyResult

			break
		}
	}

	if latest == nil {
		return ""
	}

	if !strings.Contains(strings.ToLower(latest.Output), "in_progress") {
		return ""
	}

	var openItems []string

	for _, line := range strings.Split(latest.Output, "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "in_progress") {
			openItems = append(openItems, strings.TrimSpace(line))
		}
	}

	if len(openItems) == 0 {
		return ""
	}

	var b strings.Builder

	b.WriteString("Task ledger has open items you never closed. Resolve them in your reply (state each as completed / blocked / cancelled with one short evidence sentence):\n")

	for _, item := range openItems {
		b.WriteString("- ")
		b.WriteString(item)
		b.WriteString("\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

// toolFailurePathStem returns the first two path segments of the request's
// target path (e.g. "internal/llm" for "internal/llm/llm.go"). Used by the
// same-stem failure counter so that five different hallucinated filenames
// inside the same made-up directory count as repeated failures, not fresh
// attempts.
func toolFailurePathStem(req workspace.ToolRequest) string {
	raw := strings.ReplaceAll(strings.TrimSpace(req.Path), "\\", "/")

	raw = strings.Trim(raw, "/")

	if raw == "" {
		return ""
	}

	parts := strings.Split(raw, "/")
	if len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}

	return parts[0]
}

// recoveryNudgeInputs bundles the rejection-recovery-nudge state so
// each rule can be unit-tested in isolation without a 7-arg signature.

const (
	reasoningAnswerSalvageMinChars = 40
	reasoningAnswerSalvageMaxChars = 4000
)

// salvageAnswerFromReasoning extracts answer prose from the thinking block
// when the model produced empty visible content but substantial reasoning.
// This happens with Qwen in thinking-enabled mode when the model puts its
// entire response into the reasoning block and leaves Content empty.
func (r *Runner) salvageAnswerFromReasoning(reply llama.ChatMessage, results []workspace.ToolResult) (string, bool) {
	if r == nil || strings.TrimSpace(reply.Content) != "" {
		return "", false
	}

	reasoning := strings.TrimSpace(reply.ReasoningContent)
	if reasoning == "" {
		return "", false
	}

	// Only apply to lanes that can produce a direct answer (conversation,
	// read-only). Mutation lanes need tool-call-shaped recovery, not prose.
	if runnerBriefRequiresMutation(r.RuntimeBrief) {
		return "", false
	}

	// Reasoning must be substantial enough to plausibly contain an answer.
	if len(reasoning) < 200 {
		return "", false
	}

	answer := extractAnswerProseFromReasoning(reasoning)
	if len(strings.TrimSpace(answer)) < reasoningAnswerSalvageMinChars {
		return "", false
	}

	if !reasoningAnswerCandidateIsClean(answer) {
		return "", false
	}

	if blockerKind, _ := r.finalAnswerBlockerKind(answer, results); blockerKind != "" {
		return "", false
	}

	return answer, true
}

// extractAnswerProseFromReasoning extracts the most likely answer prose
// from a model thinking block. Qwen thinking output typically contains
// internal monologue, code inspection, tool-call syntax, and a synthesis
// paragraph where the model works out what to tell the user. This function
// strips the scaffolding and returns the synthesis.
func extractAnswerProseFromReasoning(reasoning string) string {
	reasoning = strings.TrimSpace(reasoning)
	if reasoning == "" {
		return ""
	}

	if sections, hasMarkers := markedReasoningAnswerSections(reasoning); hasMarkers {
		return selectMarkedReasoningAnswerSection(sections)
	}

	// Phase 1: strip fenced code blocks, Qwen XML tool calls,
	// tool-call wrappers, and internal-monologue lines.
	var cleaned strings.Builder

	inFence := false

	for _, line := range strings.Split(reasoning, "\n") {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}

		if inFence {
			continue
		}

		if looksLikeQwenThinkingToolCall(trimmed) {
			continue
		}

		if trimmed == "" {
			cleaned.WriteString("\n")

			continue
		}

		if looksLikeToolCallWrapper(trimmed) {
			continue
		}

		if looksLikeInternalMonologue(trimmed) {
			continue
		}

		cleaned.WriteString(line)
		cleaned.WriteString("\n")
	}

	text := cleaned.String()

	// Phase 2: score each paragraph by how answer-like it is and
	// select the best candidate.
	paragraphs := splitReasoningParagraphs(text)
	if len(paragraphs) == 0 {
		return ""
	}

	best := ""
	bestScore := 0

	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if len(p) < 40 {
			continue
		}

		if looksLikeMetadataBlock(p) {
			continue
		}

		if looksLikeUnfinishedReasoningPlan(p) {
			continue
		}

		score := answerParagraphScore(p)
		if score > bestScore {
			bestScore = score
			best = p
		}
	}

	if !reasoningAnswerCandidateIsClean(best) {
		return ""
	}

	return best
}

func markedReasoningAnswerSections(reasoning string) ([]string, bool) {
	var (
		sections []string
		current  strings.Builder
	)

	active := false
	seenMarker := false

	for _, line := range strings.Split(reasoning, "\n") {
		if rest, ok := finalAnswerSectionMarkerRest(line); ok {
			seenMarker = true

			if active {
				sections = append(sections, current.String())
				current.Reset()
			}

			active = true

			if rest != "" {
				current.WriteString(rest)
				current.WriteString("\n")
			}

			continue
		}

		if active {
			current.WriteString(line)
			current.WriteString("\n")
		}
	}

	if active {
		sections = append(sections, current.String())
	}

	return sections, seenMarker
}

func finalAnswerSectionMarkerRest(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)

	for _, marker := range []string{"[final answer]", "[answer]"} {
		if strings.HasPrefix(lower, marker) {
			rest := strings.TrimSpace(trimmed[len(marker):])

			rest = strings.TrimLeft(rest, ":.- ")

			return strings.TrimSpace(rest), true
		}
	}

	for _, marker := range []string{"final answer:", "answer:"} {
		if strings.HasPrefix(lower, marker) {
			return strings.TrimSpace(trimmed[len(marker):]), true
		}
	}

	return "", false
}

func selectMarkedReasoningAnswerSection(sections []string) string {
	var (
		selected    string
		selectedKey string
	)

	for _, section := range sections {
		candidate := strings.TrimSpace(section)
		if !reasoningAnswerCandidateIsClean(candidate) {
			return ""
		}

		key := normalizeReasoningAnswerForDedupe(candidate)
		if key == "" {
			return ""
		}

		if selected == "" {
			selected = candidate
			selectedKey = key

			continue
		}

		if key != selectedKey {
			return ""
		}
	}

	return selected
}

func normalizeReasoningAnswerForDedupe(answer string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(answer)), " "))
}

func reasoningAnswerCandidateIsClean(answer string) bool {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return false
	}

	if len(answer) > reasoningAnswerSalvageMaxChars {
		return false
	}

	if finalAnswerSectionMarkerCount(answer) > 0 {
		return false
	}

	if reasoningAnswerLooksTruncated(answer) {
		return false
	}

	if looksLikeTextualToolCall(answer) ||
		looksLikeRawToolResultTranscript(answer) ||
		looksLikeUnexecutedToolRequestAnswer(answer) ||
		looksLikeNonAnswerProse(answer) {

		return false
	}

	return true
}

func reasoningAnswerLooksTruncated(answer string) bool {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return true
	}

	last := answer[len(answer)-1]
	switch last {
	case '.', '!', '?', ')', ']', '`', '"', '\'':
		return false
	default:
		return true
	}
}

func splitReasoningParagraphs(text string) []string {
	raw := strings.Split(text, "\n\n")

	var out []string

	for _, block := range raw {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}

		lines := strings.Split(block, "\n")

		var joined []string

		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			joined = append(joined, line)
		}

		if len(joined) > 0 {
			out = append(out, strings.Join(joined, " "))
		}
	}

	return out
}

func answerParagraphScore(p string) int {
	score := 0
	lower := strings.ToLower(p)

	for _, word := range []string{
		"because", "therefore", "should", "would", "could",
		"recommend", "suggest", "however", "although",
		"benefit", "tradeoff", "alternative", "approach",
		"important", "useful", "valuable", "better",
		"evidence", "system", "struct", "field",
	} {
		if strings.Contains(lower, word) {
			score++
		}
	}

	if strings.Contains(lower, "you") || strings.Contains(lower, "your") {
		score += 3
	}

	if strings.HasSuffix(strings.TrimSpace(p), ".") ||
		strings.HasSuffix(strings.TrimSpace(p), "?") ||
		strings.HasSuffix(strings.TrimSpace(p), "!") {

		score += 2
	}

	for _, prefix := range []string{
		"now i", "let me", "i should", "i need to", "i can see",
		"i think", "i'll", "i will", "looking at", "checking",
		"first,", "next,", "then,", "finally,",
	} {
		if strings.HasPrefix(lower, prefix) {
			score -= 5
		}
	}

	if len(p) > 100 {
		score += 2
	}

	if len(p) > 200 {
		score += 1
	}

	return score
}

func looksLikeQwenThinkingToolCall(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "<") {
		return false
	}

	rest := trimmed[1:]
	if idx := strings.IndexAny(rest, " />"); idx > 0 {
		name := strings.TrimSpace(rest[:idx])
		if knownToolName(name) {
			return true
		}
	}

	if strings.HasPrefix(trimmed, "</") {
		return true
	}

	return false
}

func knownToolName(name string) bool {
	switch strings.TrimSpace(name) {
	case "read_file", "search_content", "search_files",
		"context_pack", "context_query", "context_zoom", "context_status",
		"code_navigate", "discovery_query", "structural_search",
		"edit_file", "write_file", "append_file", "apply_patch",
		"shell", "source_sanity", "submit_plan", "task",
		"task_ledger", "mkdir", "note":
		return true
	default:
		return false
	}
}

func looksLikeToolCallWrapper(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true
	}

	if strings.Contains(trimmed, "<tool_call>") || strings.Contains(trimmed, "</tool_call>") {
		return true
	}

	if strings.Contains(trimmed, "<function=") || strings.Contains(trimmed, "</function>") {
		return true
	}

	if strings.HasPrefix(trimmed, "tool:") || strings.HasPrefix(trimmed, "TOOL:") {
		return true
	}

	if strings.HasPrefix(trimmed, `{"tool":`) {
		return true
	}

	return false
}

func looksLikeInternalMonologue(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true
	}

	lower := strings.ToLower(trimmed)
	for _, prefix := range []string{
		"now i", "let me", "i should", "i need to", "i can see",
		"i think", "i'll", "i will", "i'm going to", "i would",
		"i could", "i might", "i have", "i've", "i had", "i'd",
		"looking at", "checking", "i notice", "i see", "i found",
		"first,", "next,", "then,", "finally,", "also,",
		"this means", "so the", "so i", "so we",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}

	return false
}

func looksLikeMetadataBlock(p string) bool {
	p = strings.TrimSpace(p)
	if p == "" {
		return true
	}

	if looksLikeStructuredInternalState(p) {
		return true
	}

	if !strings.Contains(p, " ") && len(p) < 20 {
		return true
	}

	if len(p) > 10 && strings.ToUpper(p) == p {
		return true
	}

	alpha := 0

	for _, r := range p {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			alpha++
		}
	}

	if alpha < 3 && len(p) > 2 {
		return true
	}

	return false
}

func looksLikeUnfinishedReasoningPlan(p string) bool {
	lower := strings.ToLower(strings.TrimSpace(p))
	if lower == "" {
		return true
	}

	taskFramingPrefixes := []string{
		"the user wants me",
		"the user asked me",
		"user wants me",
		"user asked me",
	}
	for _, prefix := range taskFramingPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return agentutil.ContainsAny(lower,
				" i need to ",
				" i should ",
				" i have to ",
				" need to ",
				" should ",
				" find ",
				" inspect ",
				" check ",
				" debug ",
				" understand ",
				" gather ",
			)
		}
	}

	if strings.Contains(lower, " i need to ") || strings.Contains(lower, " i should ") {
		return !looksLikeGroundedAnswer(lower)
	}

	return false
}

func looksLikeGroundedAnswer(lower string) bool {
	return strings.Contains(lower, "root cause") ||
		strings.Contains(lower, "because") ||
		strings.Contains(lower, "evidence") ||
		strings.Contains(lower, ".go:") ||
		strings.Contains(lower, ".py:") ||
		strings.Contains(lower, ".ts:") ||
		strings.Contains(lower, ".rs:") ||
		strings.Contains(lower, ".java:") ||
		strings.Contains(lower, ".cpp:") ||
		strings.Contains(lower, ".h:")
}

func looksLikeStructuredInternalState(p string) bool {
	trimmed := strings.TrimSpace(p)
	if trimmed == "" {
		return false
	}

	lower := strings.ToLower(trimmed)
	for _, prefix := range []string{"current_state:", "tool_manifest:", "allowed_tools:", "forbidden_tools:"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}

	first := trimmed[0]
	if first != '{' && first != '[' {
		return false
	}

	hasInternalStateMarker := strings.Contains(lower, `"current_state"`) ||
		strings.Contains(lower, `"tool_manifest"`) ||
		strings.Contains(lower, `"allowed_tools"`) ||
		strings.Contains(lower, `"forbidden_tools"`)

	return hasInternalStateMarker
}

// rewriteToolRequest records a tool-request rewrite and returns the redirected
// request/raw pair. Call sites still assign req/raw explicitly so loop-state
// mutation stays visible in Runner.Run.
func (r *Runner) rewriteToolRequest(
	messages *[]llama.ChatMessage,
	original workspace.ToolRequest,
	originalRaw string,
	redirected workspace.ToolRequest,
	redirectedRaw string,
	reason string,
) (workspace.ToolRequest, string) {
	r.emitEvent(marshalRunEvent("tool_request_rewrite", map[string]any{
		"agent":          r.agentName(),
		"original_tool":  strings.TrimSpace(original.Tool),
		"rewritten_tool": strings.TrimSpace(redirected.Tool),
		"original_raw":   strings.TrimSpace(originalRaw),
		"rewritten_raw":  strings.TrimSpace(redirectedRaw),
		"reason":         reason,
	}))
	*messages = append(*messages, llama.ChatMessage{
		Role:    "system",
		Content: rewriteVisibilityNote(original.Tool, redirected.Tool),
	})
	return redirected, redirectedRaw
}

func (r *Runner) visibleBriefForToolSurface(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) workerruntime.WorkerRuntimeBrief {
	visible := runnerVisibleBriefForToolSurface(brief, results)
	if r == nil || r.Workspace == nil ||
		(!implementationPreWriteToolSurfaceActive(brief, results) &&
			!implementationTargetSourcePending(brief, results) &&
			!implementationTargetWriteReadySurfaceActive(brief, results) &&
			!implementationScopedEditSurfaceActive(brief, results)) {

		return visible
	}

	allowed := implementationPreWriteAllowedTools(r.advertisedAllowedTools(r.Workspace.AllowedTools()))
	if implementationTargetSourcePending(brief, results) {
		allowed = implementationTargetSourceAllowedTools(r.advertisedAllowedTools(r.Workspace.AllowedTools()))
	} else if implementationTargetWriteReadySurfaceActive(brief, results) {
		allowed = implementationTargetWriteReadyAllowedToolsForBrief(r.advertisedAllowedTools(r.Workspace.AllowedTools()), brief, results)
	} else if implementationScopedEditSurfaceActive(brief, results) {
		allowed = implementationScopedEditAllowedToolsWithRegistry(r.advertisedAllowedTools(r.Workspace.AllowedTools()), r.Workspace.ExternalToolsRegistry())
	}

	visible.AllowedTools = allowed
	visible.ToolManifest = nil

	if len(allowed) > 0 {
		visible.ToolManifest = runnerSanitizedToolManifest(allowed)
	}

	return workerruntime.NormalizeRuntimeBrief(visible)
}

func (r *Runner) textualToolProtocolPrompt(brief workerruntime.WorkerRuntimeBrief) string {
	if r == nil || r.Client == nil || r.Client.SupportsNativeTools() {
		return ""
	}

	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if len(brief.ToolManifest) == 0 {
		return ""
	}

	entries := textualToolProtocolEntries(brief)

	var b strings.Builder

	b.WriteString("# Textual Tool Call Protocol\n\n")
	b.WriteString("Native tool calls are unavailable for this backend. Idiom will execute tools only when your entire reply is one textual JSON tool request.\n\n")
	b.WriteString("When taking a tool action, reply with exactly one JSON object for a real allowed tool call and no prose, markdown commentary, XML tags, or second JSON object. Do not copy generic field names or schema examples as values.\n\n")
	b.WriteString("Keep structured fields structured: arrays and objects must be JSON arrays/objects, not quoted JSON strings. For `submit_plan`, `edits` must be an array and every edit object must include `path`.\n")
	b.WriteString("Use the runtime brief's `tool_manifest` for allowed tools and fields. If the runtime brief requires a tool, do not answer in prose while that tool contract is still open.\n")

	if planThenExecutePlannerBrief(brief) && toolManifestHasTool(brief.ToolManifest, workspace.ToolNameSubmitPlan) {
		b.WriteString("\nFor plan-execute `submit_plan`, emit one JSON object with top-level `tool`, `rationale`, and `edits`. Use only real paths and complete current/final source text from gathered evidence; never copy placeholders, ellipses, `TODO`, schema examples, or `existing code` comments.\n")
	}

	if len(entries) > 0 {
		b.WriteString("\nCurrent required/allowed examples:\n")

		for _, entry := range entries {
			b.WriteString("- ")

			tool := strings.TrimSpace(entry.Tool)
			b.WriteString(tool)

			if req := strings.TrimSpace(entry.Request); req != "" {
				b.WriteByte(' ')
				b.WriteString(req)
			}

			if purpose := strings.TrimSpace(entry.Purpose); purpose != "" {
				b.WriteString(" (")
				b.WriteString(purpose)
				b.WriteString(")")
			}

			b.WriteByte('\n')
		}
	}

	return strings.TrimSpace(b.String())
}

func toolManifestHasTool(manifest []workerruntime.ToolManifestEntry, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}

	for _, entry := range manifest {
		if strings.TrimSpace(entry.Tool) == name {
			return true
		}
	}

	return false
}

func runnerSanitizedToolManifest(tools []string) []workerruntime.ToolManifestEntry {
	return workerruntime.BuildToolManifest(tools)
}

func textualToolProtocolEntries(brief workerruntime.WorkerRuntimeBrief) []workerruntime.ToolManifestEntry {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if len(brief.ToolManifest) == 0 {
		return nil
	}

	suppressWholeFileWrites := scopedImplementationUsesBoundedSourceEdits(brief, brief.RequiredEditPaths)
	required := make(map[string]struct{}, len(brief.RequiredTools))

	for _, tool := range brief.RequiredTools {
		for _, concrete := range textualToolProtocolConcreteRequiredTools(tool) {
			if suppressWholeFileWrites && textualToolProtocolWholeFileWriteTool(concrete) {
				continue
			}

			if concrete != "" {
				required[concrete] = struct{}{}
			}
		}
	}

	selected := make([]workerruntime.ToolManifestEntry, 0, len(brief.ToolManifest))
	seenEntries := make(map[string]struct{}, len(brief.ToolManifest))
	seenTools := make(map[string]struct{}, len(brief.ToolManifest))

	if len(required) > 0 {
		for _, entry := range brief.ToolManifest {
			tool := strings.TrimSpace(entry.Tool)
			if suppressWholeFileWrites && textualToolProtocolWholeFileWriteTool(tool) {
				continue
			}

			if _, ok := required[tool]; ok {
				key := textualToolProtocolEntryKey(entry)
				if _, seen := seenEntries[key]; seen {
					continue
				}

				selected = append(selected, entry)
				seenEntries[key] = struct{}{}
				seenTools[tool] = struct{}{}
			}
		}
	}

	const maxExamples = 8

	for _, entry := range brief.ToolManifest {
		if len(selected) >= maxExamples {
			break
		}

		tool := strings.TrimSpace(entry.Tool)
		if tool == "" {
			continue
		}

		if suppressWholeFileWrites && textualToolProtocolWholeFileWriteTool(tool) {
			continue
		}

		if _, ok := seenTools[tool]; ok {
			continue
		}

		key := textualToolProtocolEntryKey(entry)
		if _, seen := seenEntries[key]; seen {
			continue
		}

		selected = append(selected, entry)
		seenEntries[key] = struct{}{}
		seenTools[tool] = struct{}{}
	}

	return selected
}

func textualToolProtocolWholeFileWriteTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case workspace.ToolNameWriteFile, workspace.ToolNameAppendFile:
		return true
	default:
		return false
	}
}

func textualToolProtocolConcreteRequiredTools(tool string) []string {
	tool = strings.TrimSpace(tool)
	switch tool {
	case "edit_file_or_apply_patch":
		return []string{workspace.ToolNameEditFile, workspace.ToolNameWriteFile, workspace.ToolNameAppendFile, workspace.ToolNameApplyPatch}
	case "source_sanity_or_verification_shell", "verification_shell":
		return []string{workspace.ToolNameSourceSanity, workspace.ToolNameShell}
	case "bounded_workspace_evidence_tool":
		return []string{workspace.ToolNameReadFile, workspace.ToolNameCodeNavigate, workspace.ToolNameSearchContent, workspace.ToolNameContextPack, workspace.ToolNameDiscoveryQuery}
	default:
		if tool == "" {
			return nil
		}

		return []string{tool}
	}
}

func textualToolProtocolEntryKey(entry workerruntime.ToolManifestEntry) string {
	return strings.TrimSpace(entry.Tool) + "\x00" + strings.TrimSpace(entry.Request)
}

func hideImplementationWriteOnlyToolsForTurn(hidden map[string]bool, owned map[string]bool, manager *workspace.Manager) {
	if hidden == nil || owned == nil || manager == nil {
		return
	}

	for _, tool := range manager.AllowedTools() {
		if implementationPreWriteToolAllowed(tool) {
			continue
		}

		hidden[tool] = true
		owned[tool] = true
	}
}

func hideReviewRepairTerminalOnlyToolsForTurn(hidden map[string]bool, owned map[string]bool, manager *workspace.Manager) {
	if hidden == nil || owned == nil || manager == nil {
		return
	}

	for _, tool := range manager.AllowedTools() {
		if strings.TrimSpace(tool) == workspace.ToolNameSubmitPlan {
			continue
		}

		hidden[tool] = true
		owned[tool] = true
	}
}

func hideImplementationScopedToolsForTurn(hidden map[string]bool, owned map[string]bool, manager *workspace.Manager) {
	if hidden == nil || owned == nil || manager == nil {
		return
	}

	extReg := manager.ExternalToolsRegistry()
	for _, tool := range manager.AllowedTools() {
		if implementationScopedEditToolAllowedWithRegistry(tool, extReg) {
			continue
		}

		hidden[tool] = true
		owned[tool] = true
	}
}

func releaseHiddenTools(hidden map[string]bool, owned map[string]bool) {
	if hidden == nil || owned == nil {
		return
	}

	for tool := range owned {
		delete(hidden, tool)
		delete(owned, tool)
	}
}

// hideNonSourceBodyReadTools hides every tool in allowedTools except
// read_file and code_navigate. Used on retry after a source-grounding
// gate rejection so the model's only available action is to read the
// cited unread path instead of fixing the answer text in prose.
func hideNonSourceBodyReadTools(hidden map[string]bool, allowedTools []string) {
	if hidden == nil {
		return
	}
	for _, tool := range allowedTools {
		tool = strings.TrimSpace(tool)
		if tool == workspace.ToolNameReadFile || tool == workspace.ToolNameCodeNavigate {
			continue
		}
		hidden[tool] = true
	}
}

func hideExactAnchorReadTools(hidden map[string]bool, exactHidden map[string]bool, manager *workspace.Manager) {
	if hidden == nil || exactHidden == nil || manager == nil {
		return
	}

	for _, tool := range manager.AllowedTools() {
		if !implementationPreWriteToolAllowed(tool) {
			hidden[tool] = true
			exactHidden[tool] = true
		}
	}
}

func implementationPreWriteToolAllowed(tool string) bool {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return false
	}

	if workspace.IsWorkspaceWriteTool(tool) {
		return true
	}

	// submit_plan is the planner-phase write for plan-then-execute
	// mode; without this exception the exact-anchor pre-write filter
	// hides it and the planner has nothing to call.
	if tool == workspace.ToolNameSubmitPlan {
		return true
	}

	return false
}

func implementationPreWriteAllowedTools(tools []string) []string {
	if len(tools) == 0 {
		return nil
	}

	filtered := make([]string, 0, len(tools))
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if implementationPreWriteToolAllowed(tool) && !slices.Contains(filtered, tool) {
			filtered = append(filtered, tool)
		}
	}

	return filtered
}

func implementationScopedEditToolAllowed(tool string) bool {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return false
	}

	if workspace.IsWorkspaceWriteTool(tool) {
		return true
	}

	switch tool {
	case workspace.ToolNameReadFile, workspace.ToolNameCodeNavigate:
		return true
	case workspace.ToolNameSubmitPlan:
		// Plan-then-execute planner phase: submit_plan IS the planner's
		// write — it captures the structured plan the executor phase
		// will dispatch. Without this case the scoped-edit surface
		// filter strips it before the model ever sees it, leaving the
		// planner with only read tools and no way to finish.
		return true
	default:
		return implementationScopedSemanticDiscoveryToolAllowed(tool)
	}
}

func implementationScopedEditToolAllowedWithRegistry(tool string, extReg *workspace.ExternalToolRegistry) bool {
	tool = strings.TrimSpace(tool)
	if implementationScopedEditToolAllowed(tool) {
		return true
	}

	return extReg != nil && extReg.IsReadOnlyDiscovery(tool)
}

func implementationScopedEditAllowedToolsWithRegistry(tools []string, extReg *workspace.ExternalToolRegistry) []string {
	if len(tools) == 0 {
		return nil
	}

	filtered := make([]string, 0, len(tools))
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if implementationScopedEditToolAllowedWithRegistry(tool, extReg) && !slices.Contains(filtered, tool) {
			filtered = append(filtered, tool)
		}
	}

	return filtered
}

func implementationScopedSemanticDiscoveryToolAllowed(tool string) bool {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return false
	}

	// Delegate explicit tool-name classification to the catalog profile.
	if len(workerruntime.AllowedToolsForCapabilityProfile([]string{tool}, workerruntime.CapabilityImplementationScopedEdit)) > 0 {
		return true
	}

	// Language-prefixed tools (go_find_symbol, cpp_definition, etc.) are
	// not in the explicit name list; classify via their discovery profile.
	profile := workspace.DiscoveryProfileForRequest(workspace.ToolRequest{Tool: tool})
	switch profile.Class {
	case "language_symbol", "language_relationship", "diagnostics":
		return true
	default:
		return false
	}
}

func releaseExactAnchorReadTools(hidden map[string]bool, exactHidden map[string]bool) {
	if hidden == nil || exactHidden == nil {
		return
	}

	for tool := range exactHidden {
		delete(hidden, tool)
		delete(exactHidden, tool)
	}
}

func workspaceWriteResultCompleted(req workspace.ToolRequest, result workspace.ToolResult) bool {
	if !workspace.IsWorkspaceWriteTool(req.Tool) {
		return false
	}

	return strings.TrimSpace(result.Error) == ""
}

func exactAnchorImplementationActive(brief workerruntime.WorkerRuntimeBrief) bool {
	if !runnerBriefRequiresMutation(brief) {
		return false
	}

	return slices.Contains(brief.Constraints, "exact_anchor_implementation")
}

func requiredEditPathScopeRejection(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief) (summary string, errorText string, reject bool) {
	required := normalizedRequiredEditPathSet(brief.RequiredEditPaths)
	if len(required) == 0 || !workspace.IsWorkspaceWriteTool(req.Tool) {
		return "", "", false
	}

	paths := writeRequestPaths(req)
	if len(paths) == 0 {
		return "", "", false
	}

	var outside []string

	for _, path := range paths {
		normalized := agentutil.NormalizeRunnerPath(path)
		if normalized == "" {
			continue
		}

		if strings.TrimSpace(req.Tool) == workspace.ToolNameMkdir && requiredEditPathAllowsMkdir(normalized, required) {
			continue
		}

		if _, ok := required[normalized]; !ok {
			outside = append(outside, normalized)
		}
	}

	if len(outside) == 0 {
		return "", "", false
	}

	return "Write outside required edit paths rejected",
		fmt.Sprintf("this implementation contract restricts writes to required_edit_paths %s; attempted write path(s) outside that scope: %s. Use an allowed path or report a concrete scope blocker.",
			quotedRunnerStrings(mapKeys(required)), quotedRunnerStrings(outside)),
		true
}

func writeRequestPaths(req workspace.ToolRequest) []string {
	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameEditFile, workspace.ToolNameWriteFile, workspace.ToolNameAppendFile, workspace.ToolNameMkdir:
		if strings.TrimSpace(req.Path) != "" {
			return []string{req.Path}
		}
	case workspace.ToolNameApplyPatch:
		ops, err := workspace.ParsePatch(req.Content)
		if err != nil {
			return nil
		}

		return workspace.PatchOpPaths(ops)
	case workspace.ToolNameGoMoveDeclarations:
		var paths []string

		if strings.TrimSpace(req.Path) != "" {
			paths = append(paths, req.Path)
		}

		if strings.TrimSpace(req.Destination) != "" {
			paths = append(paths, req.Destination)
		}

		for _, move := range req.Moves {
			if strings.TrimSpace(move.Destination) != "" {
				paths = append(paths, move.Destination)
			}
		}

		return paths
	}

	return nil
}

func normalizedRequiredEditPathSet(paths []string) map[string]struct{} {
	out := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		normalized := agentutil.NormalizeRunnerPath(path)
		if normalized != "" {
			out[normalized] = struct{}{}
		}
	}

	return out
}

func readRequestTargetsRequiredEditPath(req workspace.ToolRequest, requiredPaths []string) bool {
	required := normalizedRequiredEditPathSet(requiredPaths)
	if len(required) == 0 {
		return false
	}

	path := agentutil.NormalizeRunnerPath(req.Path)
	if path == "" {
		return false
	}

	if _, ok := required[path]; ok {
		return true
	}

	return false
}

func scopedReadRecoveryRejectionKey(req workspace.ToolRequest) string {
	tool := strings.TrimSpace(req.Tool)
	target := agentutil.NormalizeRunnerPath(util.FirstNonEmpty(req.Path, req.ID, req.Handle, req.Query, req.Pattern, req.Task, req.Command))

	if target == "" {
		target = "<empty-target>"
	}

	return tool + ":" + normalizeActionToken(target)
}

func requiredEditPathAllowsMkdir(dir string, required map[string]struct{}) bool {
	dir = strings.Trim(strings.TrimSpace(dir), "/")
	if dir == "" {
		return false
	}

	for path := range required {
		parent := strings.Trim(strings.TrimSpace(filepath.ToSlash(filepath.Dir(path))), "/")
		if parent == dir || strings.HasPrefix(parent, dir+"/") {
			return true
		}
	}

	return false
}

func markRuntimeBriefPathsObserved(manager *workspace.Manager, brief workerruntime.WorkerRuntimeBrief) {
	if manager == nil {
		return
	}

	candidates := append([]string{}, brief.TargetRefs...)

	candidates = append(candidates, brief.EvidencePaths...)
	candidates = append(candidates, brief.ChangedPaths...)
	candidates = append(candidates, pathparse.MentionedWorkspacePaths(strings.Join(brief.TargetPacket, "\n"))...)
	candidates = append(candidates, pathparse.MentionedWorkspacePaths(strings.Join(brief.UserBoundaries, "\n"))...)

	for _, candidate := range candidates {
		candidate = strings.Trim(candidate, "`'\" ,.;:")
		if candidate == "" || !strings.Contains(candidate, ".") {
			continue
		}

		manager.MarkPathObserved(candidate)
	}
}

func nonEmptyRunnerStrings(values []string, max int) []string {
	if max <= 0 || len(values) == 0 {
		return nil
	}

	out := make([]string, 0, max)

	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}

		out = append(out, trimmed)
		if len(out) >= max {
			return out
		}
	}

	return out
}

func hasSuccessfulVerificationResult(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		if resultCountsAsSuccessfulVerification(result) {
			return true
		}
	}

	return false
}

func shellCommandFromToolResult(result workspace.ToolResult) string {
	for _, line := range strings.Split(strings.TrimSpace(result.Output), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "$ ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "$ "))
		}

		if line != "" {
			return ""
		}
	}

	return ""
}

func hasSuccessfulTaskResult(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameTask {
			continue
		}

		if strings.TrimSpace(result.Error) == "" {
			return true
		}
	}

	return false
}

func postChildReadBudgetForBrief(brief workerruntime.WorkerRuntimeBrief) int {
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return 3
	}

	if workerruntime.RuntimeContractIsDirectAnswer(brief.Contract) {
		return 0
	}

	if runnerBriefThoroughness(brief) == "standard" {
		return 1
	}

	return 2
}

func runnerBriefThoroughness(brief workerruntime.WorkerRuntimeBrief) string {
	for _, constraint := range brief.Constraints {
		key, value, ok := strings.Cut(strings.TrimSpace(constraint), "=")
		if !ok || strings.TrimSpace(key) != "thoroughness" {
			continue
		}

		return strings.ToLower(strings.TrimSpace(value))
	}

	return ""
}

func successfulReadClassResultCount(results []workspace.ToolResult) int {
	count := 0

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		req := workspace.ToolRequest{Tool: strings.TrimSpace(result.Tool)}
		if workspace.IsReadTool(strings.TrimSpace(req.Tool)) {
			count++
		}
	}

	return count
}

func toolRequestKey(req workspace.ToolRequest) string {
	data, err := json.Marshal(normalizeRunnerToolRequest(req))
	if err != nil {
		return ""
	}

	// Args is `json:"-"` on ToolRequest (it intentionally stays out of
	// log/event serialization), so it would NOT contribute to this key
	// otherwise. For MCP tools and external tools the typed fields are
	// empty and Args carries the entire request shape — without this
	// suffix two materially-different calls (e.g. `lookup` with
	// {"enum":"JobType"} vs {"enum":"ColonistState"}) would collapse to
	// the same key and trip the "replayed prior evidence" guard.
	if len(req.Args) > 0 {
		return string(data) + "|args=" + string(req.Args)
	}

	return string(data)
}

func normalizedToolRequestRaw(req workspace.ToolRequest) string {
	data, err := json.Marshal(normalizeRunnerToolRequest(req))
	if err != nil {
		return ""
	}

	// Mirror the toolRequestKey suffix: Args is `json:"-"` on
	// ToolRequest so the marshal above misses it. The doom_loop_guard
	// and other fingerprint-based heuristics need to distinguish
	// external/MCP calls that carry their entire request shape in
	// Args; without this, three different lookup({enum:X}),
	// lookup({enum:Y}), lookup({enum:Z}) calls all collapse to one
	// fingerprint and trip the doom_loop_guard at threshold 3.
	if len(req.Args) > 0 {
		return string(data) + "|args=" + string(req.Args)
	}

	return string(data)
}

func doomLoopForcedCloseoutPrompt(req workspace.ToolRequest, identicalCallRun int) string {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "the same tool"
	}

	return fmt.Sprintf("The exact same %s request was stopped after %d identical attempts. Tools are now disabled. Complete the active contract from the evidence already gathered. If the evidence is sufficient, answer with the required final decision or summary. If one concrete claim remains unsupported, answer with <<QUESTION>> and the exact unresolved blocker; do not request more tools.", tool, identicalCallRun)
}

func shouldRejectExactReplayBeforeLoopGate(req workspace.ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameSearchFiles, workspace.ToolNameSearchContent, workspace.ToolNameRg:
		return true
	default:
		return false
	}
}

func normalizeRunnerToolRequest(req workspace.ToolRequest) workspace.ToolRequest {
	req.Path = agentutil.NormalizeRunnerPath(req.Path)
	req.Destination = agentutil.NormalizeRunnerPath(req.Destination)
	req.Workdir = agentutil.NormalizeRunnerPath(req.Workdir)
	req.NamedGap = ""
	req.WhyNotAnswering = ""

	for i := range req.Moves {
		req.Moves[i].Destination = agentutil.NormalizeRunnerPath(req.Moves[i].Destination)
	}

	return req
}

// truncForRetryReason trims a summary so the stored "why this was
// blocked" string stays small — model-facing text, not log-sized.
func truncForRetryReason(s string) string {
	s = strings.TrimSpace(s)

	const max = 160

	if len(s) <= max {
		return s
	}

	return s[:max-3] + "..."
}

// isContentInspectionTool reports whether a tool actually inspects
// content (as opposed to listing directory names). Used to reset the
// consecutive-list_files streak — running search_content, read_file, a
// go_* symbol tool, or a cpp_* symbol tool is exactly the narrowing
// behavior we want to reward.
func isContentInspectionTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case workspace.ToolNameSearchContent, workspace.ToolNameSearchFiles, workspace.ToolNameRg, workspace.ToolNameReadFile,
		workspace.ToolNameCodeNavigate, workspace.ToolNameReadOnlyCapability,
		"go_file_outline", "go_read_symbol", "go_find_symbol", "go_find_type",
		"go_find_const", "go_workspace_symbol", "go_definition", "go_references",
		"go_implementations", "go_calls_from", "go_calls_into", "go_extract_body",
		"go_related_files", "go_find_package", "go_importers", "go_diagnostics",
		"cpp_file_outline", "cpp_find_symbol", "cpp_read_symbol", "cpp_workspace_symbol",
		"cpp_definition", "cpp_declaration", "cpp_implementations",
		"cpp_references", "cpp_calls_from", "cpp_calls_into", "cpp_extract_body",
		"cpp_related_files", "cpp_includes", "cpp_who_includes",
		"cpp_blast_radius", "cpp_dead_symbol", "cpp_diagnostics":
		return true
	}

	return false
}

// isReadClassTool reports whether a tool request is read-only / inspection.
// Used to drive the plan-mode read-budget nudge. Kept explicit (allow-list)
// so mutating tools can never be miscounted. Shell is excluded because it
// can mutate even when the command looks benign.

const narrowToolFamilyNudgeAt = 5

// narrowToolFamily returns a stable family name for narrow/specific tools
// that the model might over-use in a loop. Returns "" for broad tools
// (discovery_query, context_query, harness_audit, etc.) so the repetition
// nudge resets when the model tries a broader approach.
func narrowToolFamily(tool string) string {
	tool = strings.TrimSpace(tool)
	switch tool {
	case workspace.ToolNameCodeNavigate,
		"go_find_symbol", "go_find_type", "go_find_const", "go_file_outline", "go_read_symbol",
		"go_find_package", "go_related_files", "go_workspace_symbol",
		"go_definition", "go_references", "go_implementations",
		"go_calls_from", "go_calls_into", "go_extract_body", "go_importers", "go_diagnostics":
		return "code_navigate"
	case workspace.ToolNameReadFile:
		return "read_file"
	case workspace.ToolNameSearchFiles, workspace.ToolNameSearchContent, workspace.ToolNameRg:
		return "search"
	case workspace.ToolNameShell:
		return "shell"
	default:
		return ""
	}
}

// narrowToolFamilyAutoRedirect reports whether a narrow tool family
// should be auto-redirected to a broader discovery tool after the
// repetition nudge fires. Only code_navigate enumeration benefits
// from this redirect; read_file and search are legitimate evidence
// tools that should never be redirected away.
func narrowToolFamilyAutoRedirect(family string) bool {
	return family == "code_navigate"
}

func toolActionClass(req workspace.ToolRequest) string {
	tool := strings.TrimSpace(req.Tool)
	switch tool {
	case workspace.ToolNameListFiles:
		if workspace.IsScopedInspectionRequest(req) {
			return "list_files:" + normalizeActionToken(req.Path)
		}

		return "broad_inspection"
	case workspace.ToolNameShell:
		return shellActionClass(req.Command)
	case workspace.ToolNameReadFile:
		if target := util.FirstNonEmpty(strings.TrimSpace(req.ID), strings.TrimSpace(req.Path)); target != "" {
			action := "read_file:" + normalizeActionToken(target)
			if strings.TrimSpace(req.ID) == "" && (req.StartLine > 0 || req.EndLine > 0) {
				action += fmt.Sprintf(":%d-%d", req.StartLine, req.EndLine)
			}

			return action
		}

		return workspace.ToolNameReadFile
	case workspace.ToolNameSourceSanity:
		return tool + ":" + normalizeActionToken(req.Path)
	case workspace.ToolNameCodeNavigate:
		target := util.FirstNonEmpty(strings.TrimSpace(req.Pattern), strings.TrimSpace(req.Query), strings.TrimSpace(req.Path), strings.TrimSpace(req.Language))
		return tool + ":" + normalizeActionToken(req.Capability) + ":" + normalizeActionToken(target)
	case workspace.ToolNameReadOnlyCapability:
		target := util.FirstNonEmpty(strings.TrimSpace(req.Pattern), strings.TrimSpace(req.Query), strings.TrimSpace(req.Path), strings.TrimSpace(req.Language))
		return tool + ":" + normalizeActionToken(req.Capability) + ":" + normalizeActionToken(target)
	case workspace.ToolNameEditFile:
		shape := strings.TrimSpace(req.OldText) + "->" + strings.TrimSpace(req.NewText)
		if req.StartLine > 0 || req.EndLine > 0 || strings.TrimSpace(req.Content) != "" {
			shape = fmt.Sprintf("%d-%d:%s", req.StartLine, req.EndLine, strings.TrimSpace(req.Content))
		}

		return "edit_file:" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(shape)
	case workspace.ToolNameSearchFiles, workspace.ToolNameSearchContent, workspace.ToolNameRg:
		return tool + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(req.Pattern) + ":" + normalizeActionToken(req.Glob)
	case workspace.ToolNameDiscoveryQuery:
		purpose := util.FirstNonEmpty(strings.TrimSpace(req.Purpose), strings.TrimSpace(req.Query))
		return tool + ":" + normalizeActionToken(purpose) + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(req.Pattern) + ":" + normalizeActionToken(req.Language)
	case workspace.ToolNameStructuralSearch:
		return tool + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(req.Pattern) + ":" + normalizeActionToken(req.Language)
	case workspace.ToolNameDependencyGraph:
		return tool + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(req.Language)
	case workspace.ToolNameHistoryRange, workspace.ToolNameBlameRange:
		return tool + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(fmt.Sprintf("%d-%d", req.StartLine, req.EndLine))
	case workspace.ToolNameHistorySymbol, workspace.ToolNameCtagsSymbols, workspace.ToolNameSemgrepScan:
		return tool + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(req.Pattern)
	case workspace.ToolNameJoernScan:
		return tool + ":" + normalizeActionToken(req.Path)
	case "go_find_symbol", "go_find_type", "go_find_const", "go_file_outline", "go_read_symbol",
		"go_find_package", "go_related_files", "go_workspace_symbol",
		"go_definition", "go_references", "go_implementations",
		"go_calls_from", "go_calls_into", "go_extract_body", "go_importers", "go_diagnostics":
		target := util.FirstNonEmpty(strings.TrimSpace(req.Pattern), strings.TrimSpace(req.Path), strings.TrimSpace(req.ID))
		return tool + ":" + normalizeActionToken(target)
	case workspace.ToolNameGoMoveDeclarations:
		target := util.FirstNonEmpty(strings.TrimSpace(req.Path)+"->"+strings.TrimSpace(req.Destination), strings.TrimSpace(req.Pattern))
		if len(req.Moves) > 0 {
			target = strings.TrimSpace(req.Path) + "->batch"
		}

		return tool + ":" + normalizeActionToken(target)
	case workspace.ToolNameTaskLedger:
		return workspace.ToolNameTaskLedger
	default:
		return tool
	}
}

func isBootstrappedTargetReadReminder(req workspace.ToolRequest, result workspace.ToolResult) bool {
	return strings.TrimSpace(req.Tool) == workspace.ToolNameReadFile &&
		strings.TrimSpace(result.Summary) == "Implementation bootstrap already inspected this target" &&
		strings.TrimSpace(result.Error) != ""
}

func declarationMovesForbiddenByMessages(messages []llama.ChatMessage) bool {
	if len(messages) == 0 {
		return false
	}

	lastUser := -1

	for i := len(messages) - 1; i >= 0; i-- {
		if strings.ToLower(strings.TrimSpace(messages[i].Role)) == "user" {
			lastUser = i
			break
		}
	}

	if lastUser < 0 {
		return false
	}

	for _, message := range messages[lastUser:] {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role != "user" && role != "system" {
			continue
		}

		if textForbidsDeclarationMoves(message.Content) {
			return true
		}
	}

	return false
}

func textForbidsDeclarationMoves(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))

	return agentutil.ContainsAny(normalized,
		"do not move declarations",
		"do not move declaration",
		"don't move declarations",
		"no declaration moves",
		"without moving declarations",
		"do not create files or move declarations",
	)
}

func declarationMoveConstraintRejection(req workspace.ToolRequest, declarationMovesForbidden bool) (string, string, bool) {
	if !declarationMovesForbidden {
		return "", "", false
	}

	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameGoMoveDeclarations:
		return "Declaration move rejected by task constraint",
			"the current task explicitly forbids moving declarations. Do not call go_move_declarations; allowed contract outcomes are narrow same-file read, approved same-file edit, verification evidence, or a concrete blocker.",
			true
	case "go_file_outline":
		return "Go outline rejected by task constraint",
			"the current task explicitly forbids declaration moves, and go_file_outline primarily suggests declaration-move batches. Do not call go_file_outline again; allowed contract outcomes are exact-line read, approved same-file edit, verification evidence, or a concrete blocker.",
			true
	default:
		return "", "", false
	}
}

func marshalToolRequestRaw(req workspace.ToolRequest) string {
	data, err := json.Marshal(req)
	if err != nil {
		return ""
	}

	return string(data)
}

func shellActionClass(command string) string {
	if isVerificationCommandText(command) {
		return "shell_verification:" + normalizeActionToken(command)
	}

	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return workspace.ToolNameShell
	}

	switch fields[0] {
	case "ls", "find", "tree":
		return "broad_inspection"
	case workspace.ToolNameRg, "grep", "awk", "sed", "cat", "head", "tail":
		return "shell_narrow:" + normalizeActionToken(command)
	default:
		return "shell:" + normalizeActionToken(fields[0])
	}
}

func normalizeActionToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "_"
	}

	if len(value) > 96 {
		return value[:96]
	}

	return value
}

func runnerPlanExecuteTextualAllowedTools(currentAllowedTools []string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []string {
	taskFamily := strings.TrimSpace(brief.TaskFamily)
	if taskFamily == PlanThenExecuteReviewRepairTaskFamily {
		if !completionGateResultSeen(results) && len(planThenExecuteReviewRepairFailedPhaseObligations(brief)) == 0 {
			allowed := filterPhaseTools(brief.AllowedTools, workspace.ToolNameReadFile, workspace.ToolNameCodeNavigate, workspace.ToolNameContextStatus, workspace.ToolNameContextQuery, workspace.ToolNameContextZoom, workspace.ToolNameSubmitPlan)
			if len(allowed) > 0 {
				return allowed
			}

			return []string{workspace.ToolNameReadFile, workspace.ToolNameCodeNavigate, workspace.ToolNameContextStatus, workspace.ToolNameContextQuery, workspace.ToolNameContextZoom, workspace.ToolNameSubmitPlan}
		}

		contract := deriveTurnPhaseContract(TurnPhaseContractInput{
			PhaseHint:        turnPhaseReviewRepairTerminal,
			Brief:            brief,
			Results:          results,
			BaseAllowedTools: brief.AllowedTools,
		})
		if len(contract.AllowedTools) > 0 {
			return contract.AllowedTools
		}

		return currentAllowedTools
	}

	if taskFamily != PlanThenExecutePlannerTaskFamily {
		if len(util.Unique(currentAllowedTools)) > 0 {
			return currentAllowedTools
		}

		return currentAllowedTools
	}

	state, contract, baseTools := runnerPlanExecuteTextualPlanningProjection(currentAllowedTools, brief, results)
	if runnerPlanExecuteTextualContractDisablesTools(contract) {
		return nil
	}
	if allowed := runnerPlanExecuteTextualAllowedToolsFromProjection(state, contract, baseTools); len(allowed) > 0 {
		return allowed
	}

	if len(util.Unique(currentAllowedTools)) > 0 {
		return currentAllowedTools
	}

	if slices.Contains(state.TerminalOptions, workspace.ToolNameSubmitPlan) || planThenExecuteTerminalSubmitPlanAllowed(brief, results) {
		return []string{workspace.ToolNameSubmitPlan}
	}

	return currentAllowedTools
}

func runnerPlanExecuteTextualAllowedToolsForRequest(currentAllowedTools []string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, req workspace.ToolRequest) []string {
	allowed := runnerPlanExecuteTextualAllowedTools(currentAllowedTools, brief, results)
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return allowed
	}

	if len(util.Unique(allowed)) == 1 && slices.Contains(allowed, workspace.ToolNameSubmitPlan) {
		return allowed
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" || slices.Contains(allowed, tool) {
		return allowed
	}

	state, contract, baseTools := runnerPlanExecuteTextualPlanningProjection(currentAllowedTools, brief, results)
	if runnerPlanExecuteTextualContractDisablesTools(contract) {
		return allowed
	}
	if !slices.Contains(state.ParserVisibleTools, tool) {
		return allowed
	}

	if len(baseTools) > 0 && !slices.Contains(baseTools, tool) {
		return allowed
	}

	return util.Unique(append(append([]string(nil), allowed...), tool))
}

func runnerPlanExecuteTextualContractDisablesTools(contract TurnPhaseContract) bool {
	return contract.PhaseID == turnPhaseSourceSatisfiedTerminal
}

func runnerPlanExecuteTextualPlanningProjection(currentAllowedTools []string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (PlanExecutePlanningState, TurnPhaseContract, []string) {
	baseTools := util.Unique(append(append([]string(nil), brief.AllowedTools...), currentAllowedTools...))
	if strings.TrimSpace(brief.TaskFamily) == PlanThenExecutePlannerTaskFamily {
		baseTools = util.Unique(append(baseTools, workspace.ToolNameSubmitPlan))
	}
	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode == PlanExecutePlanningModeAnchorRefresh && len(state.RefreshPaths) > 0 {
		baseTools = util.Unique(append(baseTools, planThenExecuteAnchorRefreshToolNames()...))
	}
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:             brief,
		Results:           results,
		BaseAllowedTools:  baseTools,
		TextualToolParser: true,
		ToolTransportMode: "textual",
	})

	return state, contract, baseTools
}

func runnerPlanExecuteTextualAllowedToolsFromProjection(state PlanExecutePlanningState, contract TurnPhaseContract, baseTools []string) []string {
	for _, tools := range [][]string{
		state.CallableTools,
		state.ParserVisibleTools,
		contract.ActionPolicy.CallableToolNames,
		contract.AllowedTools,
	} {
		allowed := runnerFilterPlanExecuteProjectionTools(baseTools, tools)
		if len(allowed) > 0 {
			return allowed
		}
	}

	return nil
}

func runnerFilterPlanExecuteProjectionTools(baseTools []string, projectionTools []string) []string {
	projectionTools = util.Unique(projectionTools)
	if len(projectionTools) == 0 {
		return nil
	}

	if len(baseTools) == 0 {
		return projectionTools
	}

	return filterPhaseTools(baseTools, projectionTools...)
}

func (r *Runner) salvageToolRequestFromReasoning(reply llama.ChatMessage, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || strings.TrimSpace(reply.Content) != "" {
		return workspace.ToolRequest{}, "", false
	}

	reasoning := strings.TrimSpace(reply.ReasoningContent)
	if reasoning == "" {
		return workspace.ToolRequest{}, "", false
	}

	for _, candidate := range reasoningToolRequestCandidates(reasoning) {
		req, raw, ok, err := workspace.ParseToolRequest(candidate)
		if !ok || err != nil {
			continue
		}

		if r.safeReasoningToolRequest(req, results) {
			return req, raw, true
		}
	}

	return workspace.ToolRequest{}, "", false
}

func (r *Runner) salvageVisibleToolRequestFromProse(reply string, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil {
		return workspace.ToolRequest{}, "", false
	}

	trimmed := strings.TrimSpace(reply)
	if req, raw, ok := parseVisibleLineToolRequest(trimmed); ok && r.safeVisibleToolRequest(req, results) {
		return req, raw, true
	}

	if req, raw, ok := parseMalformedVisibleReadOnlyToolJSONPrefix(trimmed); ok && r.safeVisibleToolRequest(req, results) {
		return req, raw, true
	}

	start, end, ok := firstToolRequestShapedJSONObjectSpan(trimmed)
	if !ok {
		return workspace.ToolRequest{}, "", false
	}

	raw := trimmed[start:end]

	var fields map[string]json.RawMessage

	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return workspace.ToolRequest{}, "", false
	}

	req, parsedRaw, ok := parseVisibleToolRequestJSONObject(raw, fields)
	if !ok {
		return workspace.ToolRequest{}, "", false
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameSubmitPlan &&
		!looksLikeUnexecutedToolRequestAnswer(trimmed) &&
		!r.explicitVisibleToolJSONRecoveryAllowed(trimmed, fields, results) {

		return workspace.ToolRequest{}, "", false
	}

	if r.safeVisibleToolRequest(req, results) {
		return req, parsedRaw, true
	}

	if repaired, repairedRaw, repairedOK := r.delegatedReadOnlyVisibleDiscoveryRepair(results, req); repairedOK && r.safeVisibleToolRequest(repaired, results) {
		return repaired, repairedRaw, true
	}

	return workspace.ToolRequest{}, "", false
}

func (r *Runner) delegatedReadOnlyVisibleDiscoveryRepair(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, string, bool) {
	if r == nil || len(results) > 0 || strings.TrimSpace(req.Tool) != workspace.ToolNameDiscoveryQuery {
		return workspace.ToolRequest{}, "", false
	}

	repaired, ok := r.delegatedReadOnlyDiscoveryQueryRepairRequest(results, req)
	if !ok {
		return workspace.ToolRequest{}, "", false
	}

	raw := marshalToolRequestRaw(repaired)
	if raw == "" {
		return workspace.ToolRequest{}, "", false
	}

	return repaired, raw, true
}

func parseMalformedVisibleReadOnlyToolJSONPrefix(text string) (workspace.ToolRequest, string, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return workspace.ToolRequest{}, "", false
	}

	if _, _, ok := firstToolRequestShapedJSONObjectSpan(text); ok {
		return workspace.ToolRequest{}, "", false
	}

	start := strings.Index(text, "{")
	if start < 0 || markdownFenceOpenBefore(text, start) {
		return workspace.ToolRequest{}, "", false
	}

	candidate := text[start:]
	if !strings.Contains(candidate, `"tool"`) {
		return workspace.ToolRequest{}, "", false
	}

	req := workspace.ToolRequest{
		Tool:            firstJSONLikeStringField(candidate, "tool"),
		Path:            firstJSONLikeStringField(candidate, "path"),
		Pattern:         firstJSONLikeStringField(candidate, "pattern"),
		Query:           firstJSONLikeStringField(candidate, "query"),
		Glob:            firstJSONLikeStringField(candidate, "glob"),
		Capability:      firstJSONLikeStringField(candidate, "capability"),
		NamedGap:        firstJSONLikeStringField(candidate, "named_gap"),
		WhyNotAnswering: firstJSONLikeStringField(candidate, "why_not_answering"),
		Symbol:          firstJSONLikeStringField(candidate, "symbol"),
	}
	if v, ok := firstJSONLikeIntField(candidate, "start_line"); ok {
		req.StartLine = v
	}

	if v, ok := firstJSONLikeIntField(candidate, "end_line"); ok {
		req.EndLine = v
	}

	if v, ok := firstJSONLikeIntField(candidate, "limit"); ok {
		req.Limit = v
	}

	if v, ok := firstJSONLikeIntField(candidate, "budget_tokens"); ok {
		req.BudgetTokens = v
	}

	if strings.TrimSpace(req.Tool) == "" {
		return workspace.ToolRequest{}, "", false
	}

	if !malformedVisibleReadOnlyToolCanSalvage(req.Tool) {
		return workspace.ToolRequest{}, "", false
	}

	raw := marshalToolRequestRaw(req)

	return req, raw, true
}

func malformedVisibleReadOnlyToolCanSalvage(tool string) bool {
	switch strings.TrimSpace(tool) {
	case workspace.ToolNameReadFile:
		return true
	case workspace.ToolNameSearchContent, workspace.ToolNameSearchFiles, workspace.ToolNameContextPack, workspace.ToolNameContextQuery, workspace.ToolNameContextZoom, workspace.ToolNameDiscoveryQuery, workspace.ToolNameStructuralSearch, workspace.ToolNameFileStats, workspace.ToolNameCodeNavigate:
		return true
	default:
		return false
	}
}

func firstJSONLikeStringField(text string, key string) string {
	pattern := regexp.MustCompile(`(?s)"` + regexp.QuoteMeta(key) + `"\s*:\s*"((?:\\.|[^"\\])*)"`)
	match := pattern.FindStringSubmatch(text)

	if len(match) != 2 {
		return ""
	}

	decoded, err := strconv.Unquote(`"` + match[1] + `"`)
	if err != nil {
		return strings.TrimSpace(match[1])
	}

	return strings.TrimSpace(decoded)
}

func firstJSONLikeIntField(text string, key string) (int, bool) {
	pattern := regexp.MustCompile(`(?s)"` + regexp.QuoteMeta(key) + `"\s*:\s*"?([0-9]+)"?`)
	match := pattern.FindStringSubmatch(text)

	if len(match) != 2 {
		return 0, false
	}

	value, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false
	}

	return value, true
}

func (r *Runner) explicitVisibleToolJSONRecoveryAllowed(text string, fields map[string]json.RawMessage, results []workspace.ToolResult) bool {
	if r == nil || !completionGateResultSeen(results) {
		return false
	}

	rawTool, ok := fields["tool"]
	if !ok {
		return false
	}

	var tool string

	if err := json.Unmarshal(rawTool, &tool); err != nil || strings.TrimSpace(tool) == "" {
		return false
	}

	return countToolRequestShapedJSONObjects(text) == 1
}

func countToolRequestShapedJSONObjects(text string) int {
	count := 0

	for searchFrom := 0; searchFrom < len(text); {
		start, end, ok := firstToolRequestShapedJSONObjectSpan(text[searchFrom:])
		if !ok {
			break
		}

		count++

		searchFrom += end

		if start == end {
			searchFrom++
		}
	}

	return count
}

func parseVisibleLineToolRequest(text string) (workspace.ToolRequest, string, bool) {
	text = strings.TrimSpace(text)
	if text == "" || strings.Contains(text, "{") || strings.Contains(text, "```") {
		return workspace.ToolRequest{}, "", false
	}

	// Scan for the first tool: line, skipping leading prose.
	// Models sometimes emit conversational text before a tool call.
	lines := strings.Split(text, "\n")
	toolLineIdx := -1
	proseLines := 0

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		key, _, ok := strings.Cut(line, ":")
		if ok && util.EqualTrimFold(key, "tool") {
			toolLineIdx = i
			break
		}

		proseLines++
		if proseLines > 10 {
			return workspace.ToolRequest{}, "", false
		}
	}

	if toolLineIdx < 0 {
		return workspace.ToolRequest{}, "", false
	}

	// Parse from the tool: line onward.
	values := map[string]string{}

	for _, line := range lines[toolLineIdx:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return workspace.ToolRequest{}, "", false
		}

		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.Trim(strings.TrimSpace(value), `"'`)

		if key == "" || value == "" {
			return workspace.ToolRequest{}, "", false
		}

		switch key {
		case "tool", "path", "pattern", "query", "capability", "mode", "task", "named_gap", "why_not_answering",
			"start_line", "end_line", "limit", "budget_tokens":
			values[key] = value
		default:
			return workspace.ToolRequest{}, "", false
		}
	}

	tool := strings.TrimSpace(values["tool"])
	if tool == "" {
		return workspace.ToolRequest{}, "", false
	}

	req := workspace.ToolRequest{
		Tool:            tool,
		Path:            values["path"],
		Pattern:         values["pattern"],
		Query:           values["query"],
		Capability:      values["capability"],
		Mode:            values["mode"],
		Task:            values["task"],
		NamedGap:        values["named_gap"],
		WhyNotAnswering: values["why_not_answering"],
	}
	for key, assign := range map[string]func(int){
		"start_line":    func(v int) { req.StartLine = v },
		"end_line":      func(v int) { req.EndLine = v },
		"limit":         func(v int) { req.Limit = v },
		"budget_tokens": func(v int) { req.BudgetTokens = v },
	} {
		if raw := strings.TrimSpace(values[key]); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil {
				return workspace.ToolRequest{}, "", false
			}

			assign(v)
		}
	}

	return req, marshalToolRequestRaw(req), true
}

func parseVisibleToolRequestJSONObject(raw string, fields map[string]json.RawMessage) (workspace.ToolRequest, string, bool) {
	if _, hasExplicitTool := fields["tool"]; hasExplicitTool {
		req, parsedRaw, ok, err := workspace.ParseToolRequest(raw)
		if !ok || err != nil {
			return workspace.ToolRequest{}, "", false
		}

		return req, parsedRaw, true
	}

	if _, hasEdits := fields["edits"]; hasEdits {
		if _, hasRationale := fields["rationale"]; hasRationale {
			req, err := workspace.DecodeToolRequestJSON([]byte(raw))
			if err != nil {
				return workspace.ToolRequest{}, "", false
			}

			req.Tool = workspace.ToolNameSubmitPlan

			return req, marshalToolRequestRaw(req), true
		}
	}

	if _, hasPlanArtifact := fields["plan_artifact"]; hasPlanArtifact {
		req, err := workspace.DecodeToolRequestJSON([]byte(raw))
		if err != nil {
			return workspace.ToolRequest{}, "", false
		}

		req.Tool = workspace.ToolNameSubmitPlan

		return req, marshalToolRequestRaw(req), true
	}

	if _, hasCapability := fields["capability"]; !hasCapability {
		return workspace.ToolRequest{}, "", false
	}

	req, err := workspace.DecodeToolRequestJSON([]byte(raw))
	if err != nil {
		return workspace.ToolRequest{}, "", false
	}

	if strings.TrimSpace(req.Tool) == "" && strings.TrimSpace(req.Capability) != "" {
		if workspace.CodeNavigateCapabilityAlias(req.Capability) == "" {
			return workspace.ToolRequest{}, "", false
		}

		req.Tool = workspace.ToolNameCodeNavigate
	}

	if strings.TrimSpace(req.Tool) == "" {
		return workspace.ToolRequest{}, "", false
	}

	return req, marshalToolRequestRaw(req), true
}

func (r *Runner) safeVisibleToolRequest(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	if strings.TrimSpace(req.Tool) == workspace.ToolNameSubmitPlan {
		return r.safeVisibleSubmitPlanRequest(req, results)
	}

	if r.safeVisiblePlanExecuteMissingTargetReadRequest(req) {
		return true
	}

	if r.safeVisibleWorkspaceWriteRequest(req, results) {
		return true
	}

	return r.safeReasoningToolRequest(req, results)
}

func (r *Runner) safeVisibleWorkspaceWriteRequest(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	if r == nil || !runnerBriefRequiresMutation(r.RuntimeBrief) || !workspace.IsWorkspaceWriteTool(req.Tool) {
		return false
	}

	if reasoningToolRequestHasPlaceholder(req) || !safeWorkspaceWriteRequestShape(req) {
		return false
	}

	if _, _, reject := requiredEditPathScopeRejection(req, r.RuntimeBrief); reject {
		return false
	}

	if len(r.RuntimeBrief.RequiredEditPaths) > 0 {
		return r.safeReasoningWorkspaceWriteRequest(req)
	}

	if !completionGateResultSeen(results) {
		return false
	}

	paths := writeRequestPaths(req)
	if len(paths) == 0 {
		return false
	}

	for _, path := range paths {
		if !concreteSourceEvidenceReadForPath(path, results) {
			return false
		}
	}

	return true
}

func (r *Runner) safeVisiblePlanExecuteMissingTargetReadRequest(req workspace.ToolRequest) bool {
	if r == nil || !r.planThenExecutePlannerPhase() {
		return false
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile {
		return false
	}

	return planThenExecuteReadRequestMissingConcreteTarget(req)
}

func (r *Runner) safeVisibleSubmitPlanRequest(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	if r == nil || !r.planThenExecutePlannerPhase() || strings.TrimSpace(req.Tool) != workspace.ToolNameSubmitPlan {
		return false
	}

	if reasoningToolRequestHasPlaceholder(req) {
		return false
	}

	if !r.planThenExecuteSubmitPlanOnlyVisibleJSONSalvageAllowed(results) {
		return false
	}

	edits := planThenExecuteEffectiveSubmitPlanEdits(req)
	if len(edits) == 0 {
		return false
	}

	covered := make(map[string]struct{}, len(edits))
	for _, edit := range edits {
		path := agentutil.NormalizeRunnerPath(edit.Path)
		if path != "" {
			covered[path] = struct{}{}
		}
	}

	required := r.requiredEditCoveragePaths()
	if len(required) == 0 {
		return len(covered) > 0
	}

	return len(missingRequiredEditPaths(required, covered)) == 0
}

func (r *Runner) planThenExecuteSubmitPlanOnlyVisibleJSONSalvageAllowed(results []workspace.ToolResult) bool {
	if r == nil || !r.planThenExecutePlannerPhase() {
		return false
	}

	return planThenExecuteTerminalSubmitPlanAllowed(r.RuntimeBrief, results) ||
		planThenExecuteOpenCandidateSourceCanSubmit(r.RuntimeBrief, results) ||
		planThenExecuteRejectedPlanCorrectionOnly(r.RuntimeBrief, results)
}

func reasoningToolRequestCandidates(reasoning string) []string {
	reasoning = strings.TrimSpace(reasoning)
	if reasoning == "" {
		return nil
	}

	candidates := make([]string, 0, 3)

	for offset := 0; offset < len(reasoning); {
		idx := strings.Index(reasoning[offset:], "```tool")
		if idx < 0 {
			break
		}

		start := offset + idx
		rest := reasoning[start:]
		end := strings.Index(rest, "\n```")

		if end < 0 {
			break
		}

		candidates = append(candidates, rest[:end+4])
		offset = start + end + 4
	}

	if strings.Contains(reasoning, "<tool_call>") || strings.Contains(reasoning, "<function=") {
		candidates = append(candidates, reasoning)
	}

	if strings.Contains(reasoning, `"tool"`) {
		candidates = append(candidates, reasoning)
	}

	return candidates
}

func (r *Runner) safeReasoningToolRequest(req workspace.ToolRequest, results []workspace.ToolResult) bool {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" || reasoningToolRequestHasPlaceholder(req) {
		return false
	}

	switch tool {
	case workspace.ToolNameReadFile:
		return strings.TrimSpace(req.Path) != ""
	case workspace.ToolNameSearchContent:
		return strings.TrimSpace(req.Pattern) != ""
	case workspace.ToolNameSearchFiles:
		return strings.TrimSpace(req.Pattern) != "" || strings.TrimSpace(req.Glob) != ""
	case workspace.ToolNameContextPack:
		return strings.TrimSpace(req.Task) != "" && !contextPackUnavailableForRecovery(results)
	case workspace.ToolNameContextQuery:
		return strings.TrimSpace(req.Query) != ""
	case workspace.ToolNameContextZoom:
		return strings.TrimSpace(req.Handle) != ""
	case workspace.ToolNameDiscoveryQuery:
		return safeDiscoveryQueryRequest(req)
	case workspace.ToolNameStructuralSearch:
		return strings.TrimSpace(req.Pattern) != ""
	case workspace.ToolNameCodeNavigate:
		return workspace.CodeNavigateCapabilityAlias(req.Capability) != "" &&
			(strings.TrimSpace(req.Path) != "" || strings.TrimSpace(req.Pattern) != "" || strings.TrimSpace(req.Query) != "")
	case workspace.ToolNameSubmitPlan:
		return false
	default:
		return r.safeReasoningWorkspaceWriteRequest(req)
	}
}

func safeDiscoveryQueryRequest(req workspace.ToolRequest) bool {
	if strings.TrimSpace(util.FirstNonEmpty(req.Purpose, req.Query, req.Pattern, req.Path)) == "" {
		return false
	}

	if purpose := strings.TrimSpace(req.Purpose); purpose != "" {
		return normalizeRunnerDiscoveryQueryPurpose(purpose) != ""
	}

	if query := strings.TrimSpace(req.Query); query != "" {
		return normalizeRunnerDiscoveryQueryPurpose(query) != ""
	}

	return true
}

func normalizeRunnerDiscoveryQueryPurpose(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")

	switch value {
	case "impact", "impact_analysis":
		return "impact_analysis"
	case "trace", "function_trace", "function_tracing":
		return "function_tracing"
	case "architecture", "architecture_boundary", "boundary", "tool_usefulness", "tooling_usefulness", "discovery_tooling", "discovery_tools", "usefulness":
		return "architecture_boundary"
	case "security", "security_review":
		return "security_review"
	case "history", "history_context":
		return "history_context"
	default:
		if strings.Contains(value, "security") || strings.Contains(value, "vulnerability") {
			return "security_review"
		}

		if strings.Contains(value, "history") || strings.Contains(value, "evolution") {
			return "history_context"
		}

		if strings.Contains(value, "impact") || strings.Contains(value, "blast_radius") {
			return "impact_analysis"
		}

		if strings.Contains(value, "trace") || strings.Contains(value, "tracing") || strings.Contains(value, "call_graph") {
			return "function_tracing"
		}

		if strings.Contains(value, "architecture") ||
			strings.Contains(value, "boundary") ||
			(strings.Contains(value, "discovery") && (strings.Contains(value, "implementation") || strings.Contains(value, "definition") || strings.Contains(value, "orchestration") || strings.Contains(value, "pattern"))) {

			return "architecture_boundary"
		}

		return ""
	}
}

func (r *Runner) safeReasoningWorkspaceWriteRequest(req workspace.ToolRequest) bool {
	if r == nil || !runnerBriefRequiresMutation(r.RuntimeBrief) || len(r.RuntimeBrief.RequiredEditPaths) == 0 {
		return false
	}

	if !workspace.IsWorkspaceWriteTool(req.Tool) {
		return false
	}

	paths := writeRequestPaths(req)
	if len(paths) == 0 {
		return false
	}

	if _, _, reject := requiredEditPathScopeRejection(req, r.RuntimeBrief); reject {
		return false
	}

	return safeWorkspaceWriteRequestShape(req)
}

func safeWorkspaceWriteRequestShape(req workspace.ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameEditFile:
		hasOldNew := strings.TrimSpace(req.OldText) != "" && (strings.TrimSpace(req.NewText) != "" || req.NewText == "")
		hasRange := req.StartLine > 0 && req.EndLine >= req.StartLine && strings.TrimSpace(req.Content) != ""

		return hasOldNew || hasRange
	case workspace.ToolNameWriteFile, workspace.ToolNameAppendFile:
		return strings.TrimSpace(req.Content) != ""
	case workspace.ToolNameApplyPatch:
		if strings.TrimSpace(req.Content) == "" {
			return false
		}

		_, err := workspace.ParsePatch(req.Content)

		return err == nil
	default:
		return false
	}
}

func reasoningToolRequestHasPlaceholder(req workspace.ToolRequest) bool {
	fields := []string{
		req.Path,
		req.Task,
		req.Pattern,
		req.Query,
		req.Content,
		req.OldText,
		req.NewText,
		req.NamedGap,
		req.WhyNotAnswering,
		req.Rationale,
	}
	for _, edit := range reasoningSubmitPlanEdits(req) {
		fields = append(fields,
			edit.Path,
			edit.OldText,
			edit.NewText,
			edit.NewTextRef,
			edit.Content,
			edit.ContentRef,
			edit.Purpose,
		)
	}

	for _, field := range fields {
		lower := strings.ToLower(strings.TrimSpace(field))
		if lower == "" {
			continue
		}

		if agentutil.ContainsAny(lower,
			"candidate/path.go",
			"relative/file",
			"exact current text",
			"exact current substring",
			"replacement text",
			"complete replacement text",
			"why these candidate path edits satisfy the objective",
			"one paragraph on the overall plan",
			"why this edit",
			"one sentence",
			"<full file body>",
			"<user objective>",
			"<path>",
			"<pattern>",
		) {

			return true
		}
	}

	return false
}

func reasoningSubmitPlanHasConcreteEdit(req workspace.ToolRequest) bool {
	edits := reasoningSubmitPlanEdits(req)
	if len(edits) == 0 {
		return false
	}

	for _, edit := range edits {
		if strings.TrimSpace(edit.Path) == "" {
			continue
		}

		hasOldText := strings.TrimSpace(edit.OldText) != ""
		hasContent := strings.TrimSpace(edit.Content) != "" || strings.TrimSpace(edit.ContentRef) != ""
		hasRange := edit.StartLine > 0 && edit.EndLine >= edit.StartLine && hasContent
		hasReplace := edit.ReplaceAll && (strings.TrimSpace(edit.NewText) != "" || strings.TrimSpace(edit.NewTextRef) != "")

		if (hasOldText && workspace.PlanEditHasExplicitReplacementText(edit)) || hasRange || hasReplace {
			return true
		}
	}

	return false
}

func reasoningSubmitPlanEdits(req workspace.ToolRequest) []workspace.PlanEdit {
	if req.PlanArtifact != nil && len(req.PlanArtifact.Edits) > 0 {
		return append([]workspace.PlanEdit(nil), req.PlanArtifact.Edits...)
	}

	return append([]workspace.PlanEdit(nil), req.Edits...)
}

const (
	toolRequestNormalizerOutcomeNormalized          = "normalized"
	toolRequestNormalizerOutcomeMissingToolInferred = "missing_tool_inferred"
	toolRequestNormalizerOutcomeUnsupportedShape    = "unsupported_shape"
	toolRequestNormalizerOutcomeUnsafeToInfer       = "unsafe_to_infer"
	toolRequestNormalizerOutcomeRequiresTerminal    = "requires_terminal_action"
	toolRequestNormalizerOutcomeTransportMismatch   = "transport_mismatch"

	toolRequestNormalizerTransportNative                  = "native"
	toolRequestNormalizerTransportTextual                 = "textual"
	toolRequestNormalizerTransportFencedJSON              = "fenced_json"
	toolRequestNormalizerTransportKeyValue                = "key_value"
	toolRequestNormalizerTransportKeyValueArgs            = "key_value_args"
	toolRequestNormalizerTransportFunctionCall            = "function_call"
	toolRequestNormalizerTransportNestedArguments         = "nested_arguments"
	toolRequestNormalizerTransportIdiomCLI                = "idiom_cli"
	toolRequestNormalizerTransportXMLToolMarkup           = "xml_tool_markup"
	toolRequestNormalizerTransportStringifiedSubmitRepair = "stringified_submit_plan_repair"
)

type toolRequestSyntaxNormalization struct {
	Request            workspace.ToolRequest
	Raw                string
	OK                 bool
	ParseErr           error
	EventOutcome       string
	EventTransport     string
	ParserFailureClass string
	ActiveToolCall     *llama.ToolCall
	Repaired           bool
}

type toolRequestIntentProjection struct {
	ToolName    string
	ActionClass string
	Transport   string
	ParserState string
	Confidence  string
}

func (n toolRequestSyntaxNormalization) Intent() toolRequestIntentProjection {
	return toolRequestIntentProjection{
		ToolName:    strings.TrimSpace(n.Request.Tool),
		ActionClass: toolRequestIntentActionClass(n.Request),
		Transport:   strings.TrimSpace(n.EventTransport),
		ParserState: strings.TrimSpace(n.EventOutcome),
		Confidence:  toolRequestIntentConfidence(n),
	}
}

func toolRequestIntentActionClass(req workspace.ToolRequest) string {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		return ""
	}
	if tool == workspace.ToolNameSearchContent {
		return actionRead
	}
	return string(runnerAutonomyActionClassForRequest(req))
}

func toolRequestIntentConfidence(n toolRequestSyntaxNormalization) string {
	switch strings.TrimSpace(n.EventOutcome) {
	case toolRequestNormalizerOutcomeNormalized:
		if n.Repaired {
			return "repaired"
		}
		return "normalized"
	case toolRequestNormalizerOutcomeMissingToolInferred:
		return "inferred"
	case toolRequestNormalizerOutcomeUnsafeToInfer:
		return "unsafe"
	case toolRequestNormalizerOutcomeTransportMismatch:
		return "transport_mismatch"
	default:
		if n.OK {
			return "normalized"
		}
		return "unsupported"
	}
}

func runnerAutonomyPolicyInputFromNormalization(n toolRequestSyntaxNormalization) runnerAutonomyPolicyEventInput {
	input := runnerAutonomyPolicyEventInput{
		EventType:     autonomy.EventToolIntentClassified,
		Transport:     toolRequestAutonomyTransport(n.EventTransport),
		ParserFailure: runnerAutonomyParserFailureClass(n.Raw, n.ParseErr),
		RepairSource:  autonomy.RepairSourceNone,
		Result:        strings.TrimSpace(n.EventOutcome),
		Source:        "tooling.intent",
	}
	if strings.TrimSpace(n.ParserFailureClass) != "" {
		input.ParserFailure = toolRequestParserFailureToAutonomy(n.ParserFailureClass)
	}
	if n.Repaired {
		input.RepairSource = autonomy.RepairSourceHostNormalizer
	}
	return input
}

func toolRequestAutonomyTransport(transport string) autonomy.ToolTransport {
	switch strings.TrimSpace(transport) {
	case toolRequestNormalizerTransportNative:
		return autonomy.ToolTransportNativeCall
	case toolRequestNormalizerTransportTextual,
		toolRequestNormalizerTransportFencedJSON,
		toolRequestNormalizerTransportKeyValue,
		toolRequestNormalizerTransportKeyValueArgs,
		toolRequestNormalizerTransportFunctionCall,
		toolRequestNormalizerTransportNestedArguments,
		toolRequestNormalizerTransportIdiomCLI,
		toolRequestNormalizerTransportXMLToolMarkup,
		toolRequestNormalizerTransportStringifiedSubmitRepair:
		return autonomy.ToolTransportTextualCall
	default:
		return autonomy.ToolTransportUnknown
	}
}

func toolRequestParserFailureToAutonomy(class string) autonomy.ParserFailureClass {
	switch strings.TrimSpace(class) {
	case "malformed_json":
		return autonomy.ParserFailureMalformedJSON
	case "submit_plan_edits_string", "schema_type_mismatch", "nested_tool_mismatch":
		return autonomy.ParserFailureSchemaMismatch
	case "missing_tool":
		return autonomy.ParserFailureMissingToolName
	case "transport_mismatch":
		return autonomy.ParserFailureTransportMismatch
	case "":
		return autonomy.ParserFailureNone
	default:
		return autonomy.ParserFailureAmbiguousToolCall
	}
}

func normalizeToolRequestSyntax(message llama.ChatMessage) toolRequestSyntaxNormalization {
	if len(message.ToolCalls) > 0 {
		call := message.ToolCalls[0]
		req, ok, parseErr := parseToolCall(call)

		result := toolRequestSyntaxNormalization{
			Request:        req,
			Raw:            call.Function.Arguments,
			OK:             ok,
			ParseErr:       parseErr,
			ActiveToolCall: &call,
		}
		if parseErr != nil {
			repaired := repairToolRequestSyntax(result)
			if repaired.Repaired {
				return repaired
			}

			result.EventOutcome = toolRequestNormalizerOutcomeUnsupportedShape
			if nativeToolCallTransportMismatch(parseErr) {
				result.EventOutcome = toolRequestNormalizerOutcomeTransportMismatch
			}
			result.EventTransport = toolRequestNormalizerTransportNative
			result.ParserFailureClass = toolRequestParserFailureClass(result.Raw, parseErr)

			return result
		}

		if repaired := repairToolRequestSyntax(result); repaired.Repaired {
			return repaired
		}

		if ok {
			normalizeParsedToolRequestSyntax(&result)

			result.EventOutcome = toolRequestNormalizerOutcomeNormalized
			result.EventTransport = toolRequestNormalizerTransportNative

			return result
		}

		result.EventOutcome = toolRequestNormalizerOutcomeUnsupportedShape
		result.EventTransport = toolRequestNormalizerTransportNative

		return result
	}

	req, raw, ok, parseErr := workspace.ParseToolRequest(message.Content)

	result := toolRequestSyntaxNormalization{
		Request:  req,
		Raw:      raw,
		OK:       ok,
		ParseErr: parseErr,
	}
	if parseErr != nil {
		repaired := repairToolRequestSyntax(result)
		if repaired.Repaired {
			return repaired
		}

		result.EventOutcome = toolRequestNormalizerOutcomeUnsupportedShape
		result.EventTransport = toolRequestNormalizerTransportTextual

		if looksLikeXMLToolCall(message.Content) {
			result.EventOutcome = toolRequestNormalizerOutcomeUnsafeToInfer
			result.EventTransport = toolRequestNormalizerTransportXMLToolMarkup
		}

		result.ParserFailureClass = toolRequestParserFailureClass(result.Raw, parseErr)

		return result
	}

	if repaired := repairToolRequestSyntax(result); repaired.Repaired {
		return repaired
	}

	if ok {
		normalizeParsedToolRequestSyntax(&result)

		result.EventOutcome = toolRequestNormalizerOutcomeNormalized

		if toolRequestPayloadMissingExplicitTool(raw) && strings.TrimSpace(req.Tool) != "" {
			if missingToolInferenceNeedsTypedAuthority(req) {
				result.Request = workspace.ToolRequest{}
				result.OK = false
				result.EventOutcome = toolRequestNormalizerOutcomeUnsafeToInfer
				result.EventTransport = toolRequestNormalizerTransportTextual

				return result
			}

			result.EventOutcome = toolRequestNormalizerOutcomeMissingToolInferred
		}

		result.EventTransport = toolRequestNormalizerTransportTextual
		if looksLikeXMLToolCall(message.Content) {
			result.EventTransport = toolRequestNormalizerTransportXMLToolMarkup
		}

		return result
	}

	// Try the unified tokenizer dispatch for non-standard textual formats.
	// This replaces the per-format cascade of normalize* functions.
	if tokenResult := ParseWithAllTokenizers(message.Content); tokenResult.Unsafe {
		result.EventOutcome = toolRequestNormalizerOutcomeUnsafeToInfer
		result.EventTransport = textToNormalizerTransport(tokenResult.Transport)

		return result
	} else if tokenResult.OK {
		// Reject write tools inferred from non-native formats to prevent
		// accidental mutation from function-call, CLI, or args-block syntax.
		if tokenizerWriteRequestUnsafe(tokenResult.Transport, tokenResult.Request.Tool) {
			result.EventOutcome = toolRequestNormalizerOutcomeUnsafeToInfer
			result.EventTransport = textToNormalizerTransport(tokenResult.Transport)

			return result
		}

		result.Request = tokenResult.Request
		result.Raw = tokenResult.Raw
		result.OK = true
		result.EventOutcome = toolRequestNormalizerOutcomeNormalized
		result.EventTransport = textToNormalizerTransport(tokenResult.Transport)
		normalizeParsedToolRequestSyntax(&result)

		return result
	}

	if outcome := classifyUnsupportedTextualToolShape(message.Content); outcome != "" {
		result.EventOutcome = outcome
		result.EventTransport = toolRequestNormalizerTransportTextual
	}

	return result
}

func nativeToolCallTransportMismatch(err error) bool {
	return err != nil && strings.Contains(err.Error(), "arguments declared tool")
}

func tokenizerWriteRequestUnsafe(transport string, tool string) bool {
	if !workspace.IsWorkspaceWriteTool(tool) {
		return false
	}

	return !(transport == "key_value" && tool == "edit_file")
}

func repairToolRequestSyntax(result toolRequestSyntaxNormalization) toolRequestSyntaxNormalization {
	if repairedReq, repairedRaw, repaired := repairNestedArgumentsToolRequest(result.Raw); repaired {
		result.Request = repairedReq
		result.Raw = repairedRaw
		result.OK = true
		result.ParseErr = nil
		result.EventOutcome = toolRequestNormalizerOutcomeNormalized
		result.EventTransport = toolRequestNormalizerTransportNestedArguments
		result.ParserFailureClass = ""
		result.Repaired = true
		normalizeParsedToolRequestSyntax(&result)

		return result
	}

	repairedReq, repairedRaw, repaired := repairStringifiedSubmitPlanEditsToolRequest(result.Raw, result.ActiveToolCall)
	if !repaired {
		return result
	}

	result.Request = repairedReq
	result.Raw = repairedRaw
	result.OK = true
	result.ParseErr = nil
	result.EventOutcome = toolRequestNormalizerOutcomeNormalized
	result.EventTransport = toolRequestNormalizerTransportStringifiedSubmitRepair
	result.ParserFailureClass = ""
	result.Repaired = true
	normalizeParsedToolRequestSyntax(&result)

	return result
}

func repairNestedArgumentsToolRequest(raw string) (workspace.ToolRequest, string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return workspace.ToolRequest{}, "", false
	}

	var obj map[string]json.RawMessage

	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return workspace.ToolRequest{}, "", false
	}

	if len(obj) != 1 {
		return workspace.ToolRequest{}, "", false
	}

	args, ok := obj["arguments"]
	if !ok || !json.Valid(args) {
		return workspace.ToolRequest{}, "", false
	}

	req, err := workspace.DecodeToolRequestJSON(args)
	if err != nil || strings.TrimSpace(req.Tool) == "" {
		return workspace.ToolRequest{}, "", false
	}

	return req, string(args), true
}

func toolRequestParserFailureClass(raw string, parseErr error) string {
	if parseErr == nil {
		return ""
	}

	lower := strings.ToLower(parseErr.Error())
	combined := lower + "\n" + strings.ToLower(raw)

	switch {
	case nativeToolCallTransportMismatch(parseErr):
		return "transport_mismatch"
	case strings.Contains(combined, "toolrequest.edits") && strings.Contains(combined, "string"):
		return "submit_plan_edits_string"
	case strings.Contains(lower, "unexpected end of json") ||
		strings.Contains(lower, "invalid character") ||
		strings.Contains(lower, "syntax error"):
		return "malformed_json"
	case strings.Contains(lower, "cannot unmarshal"):
		return "schema_type_mismatch"
	case strings.Contains(lower, "missing tool"):
		return "missing_tool"
	case strings.Contains(lower, "nested request tool") && strings.Contains(lower, "does not match"):
		return "nested_tool_mismatch"
	default:
		return "unknown"
	}
}

func repairStringifiedSubmitPlanEditsToolRequest(raw string, activeToolCall *llama.ToolCall) (workspace.ToolRequest, string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return workspace.ToolRequest{}, "", false
	}

	var obj map[string]json.RawMessage

	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return workspace.ToolRequest{}, "", false
	}

	toolName := ""
	if activeToolCall != nil {
		toolName = strings.TrimSpace(activeToolCall.Function.Name)
	}

	if toolName == "" {
		_ = json.Unmarshal(obj["tool"], &toolName)
		toolName = strings.TrimSpace(toolName)
	}

	if toolName != "submit_plan" {
		return workspace.ToolRequest{}, "", false
	}

	var encodedEdits string

	if err := json.Unmarshal(obj["edits"], &encodedEdits); err != nil {
		return workspace.ToolRequest{}, "", false
	}

	decodedEdits := strings.TrimSpace(encodedEdits)
	if decodedEdits == "" || !json.Valid([]byte(decodedEdits)) {
		return workspace.ToolRequest{}, "", false
	}

	var edits []workspace.PlanEdit

	if err := json.Unmarshal([]byte(decodedEdits), &edits); err != nil || len(edits) == 0 {
		return workspace.ToolRequest{}, "", false
	}

	obj["edits"] = json.RawMessage(decodedEdits)
	if _, ok := obj["tool"]; !ok {
		obj["tool"] = json.RawMessage(`"submit_plan"`)
	}

	repaired, err := json.Marshal(obj)
	if err != nil {
		return workspace.ToolRequest{}, "", false
	}

	req, err := workspace.DecodeToolRequestJSON(repaired)
	if err != nil {
		return workspace.ToolRequest{}, "", false
	}

	req.Tool = "submit_plan"

	return req, string(repaired), true
}

func parseIdiomCLIToolRequestSyntax(text string) (workspace.ToolRequest, string, bool, bool) {
	trimmed := strings.TrimSpace(text)
	lower := strings.ToLower(trimmed)

	if !strings.HasPrefix(lower, "idiom tool ") {
		return workspace.ToolRequest{}, "", false, false
	}

	objectStart := strings.Index(trimmed, "{")
	if objectStart < 0 {
		return workspace.ToolRequest{}, "", false, true
	}

	prefix := strings.TrimSpace(trimmed[:objectStart])
	parts := strings.Fields(prefix)

	if len(parts) < 3 || !strings.EqualFold(parts[0], "idiom") || !strings.EqualFold(parts[1], "tool") || !toolRequestSyntaxIdentifier(parts[2]) {
		return workspace.ToolRequest{}, "", false, true
	}

	object, _, ok := extractJSONObjectAt(trimmed, objectStart)
	if !ok {
		return workspace.ToolRequest{}, "", false, true
	}

	req, err := workspace.DecodeToolRequestJSON([]byte(object))
	if err != nil {
		return workspace.ToolRequest{}, "", false, true
	}

	if strings.TrimSpace(req.Tool) == "" {
		req.Tool = parts[2]
	}

	if !util.EqualTrimFold(req.Tool, parts[2]) {
		return workspace.ToolRequest{}, "", false, true
	}

	return req, object, true, false
}

func exactFencedJSONPayload(text string) (payload string, ok bool, unsafe bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false, false
	}

	if !strings.HasPrefix(trimmed, "```") {
		if strings.Contains(strings.ToLower(trimmed), "```json") {
			return "", false, true
		}

		return "", false, false
	}

	firstLineEnd := strings.Index(trimmed, "\n")
	if firstLineEnd < 0 {
		return "", false, true
	}

	language := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed[:firstLineEnd], "```")))
	if language != "json" {
		return "", false, false
	}

	rest := trimmed[firstLineEnd+1:]
	if !strings.HasSuffix(rest, "\n```") {
		return "", false, true
	}

	payload = strings.TrimSpace(strings.TrimSuffix(rest, "\n```"))
	if !strings.HasPrefix(payload, "{") {
		return "", false, true
	}

	return payload, true, false
}

func parseKeyValueArgsToolRequestSyntax(text string) (workspace.ToolRequest, string, bool, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || !strings.Contains(trimmed, "{") || !strings.Contains(strings.ToLower(trimmed), "args:") {
		return workspace.ToolRequest{}, "", false, false
	}

	tool, argsStart, ok := keyValueArgsToolAndBodyStart(trimmed)
	if !ok {
		return workspace.ToolRequest{}, "", false, true
	}

	objectOffset := strings.Index(trimmed[argsStart:], "{")
	if objectOffset < 0 {
		return workspace.ToolRequest{}, "", false, true
	}

	objectStart := argsStart + objectOffset
	object, _, ok := extractJSONObjectAt(trimmed, objectStart)

	if !ok {
		return workspace.ToolRequest{}, "", false, true
	}

	payload, ok := normalizeKeyValueArgsObject(tool, object)
	if !ok {
		return workspace.ToolRequest{}, "", false, true
	}

	req, err := workspace.DecodeToolRequestJSON([]byte(payload))
	if err != nil || strings.TrimSpace(req.Tool) == "" {
		return workspace.ToolRequest{}, "", false, true
	}

	return req, payload, true, false
}

func keyValueArgsToolAndBodyStart(text string) (string, int, bool) {
	tool := ""
	argsStart := -1
	offset := 0

	for _, line := range strings.SplitAfter(text, "\n") {
		rawLine := strings.TrimRight(line, "\r\n")
		trimmed := strings.TrimSpace(rawLine)
		lower := strings.ToLower(trimmed)

		if tool == "" && trimmed != "" && !strings.HasPrefix(lower, "tool:") {
			return "", 0, false
		}

		if value, ok := strings.CutPrefix(lower, "tool:"); ok {
			original := strings.TrimSpace(rawLine[strings.Index(strings.ToLower(rawLine), "tool:")+len("tool:"):])
			if original == "" || !toolRequestSyntaxIdentifier(original) {
				return "", 0, false
			}

			tool = original
			_ = value
		}

		if strings.HasPrefix(lower, "args:") {
			argsIndex := strings.Index(strings.ToLower(rawLine), "args:")

			argsStart = offset + argsIndex + len("args:")

			break
		}

		offset += len(line)
	}

	if tool == "" || argsStart < 0 {
		return "", 0, false
	}

	return tool, argsStart, true
}

func normalizeKeyValueArgsObject(tool string, object string) (string, bool) {
	var fields map[string]json.RawMessage

	if err := json.Unmarshal([]byte(object), &fields); err != nil {
		return "", false
	}

	if rawTool, exists := fields["tool"]; exists {
		var explicit string

		if err := json.Unmarshal(rawTool, &explicit); err != nil {
			return "", false
		}

		if explicit = strings.TrimSpace(explicit); explicit != "" && explicit != tool {
			return "", false
		}
	}

	if _, ok := fields["capability"]; !ok && syntaxToolRequestUsesCapabilityAlias(tool) {
		if rawKind, hasKind := fields["kind"]; hasKind {
			var kind string

			if err := json.Unmarshal(rawKind, &kind); err == nil && strings.TrimSpace(kind) != "" {
				capabilityRaw, _ := json.Marshal(strings.TrimSpace(kind))

				fields["capability"] = capabilityRaw
			}
		}

		if _, hasCapability := fields["capability"]; !hasCapability {
			if rawResolution, hasResolution := fields["resolution"]; hasResolution {
				var resolution string

				if err := json.Unmarshal(rawResolution, &resolution); err == nil && strings.TrimSpace(resolution) != "" {
					if capability := workspace.CodeNavigateResolutionCapabilityAlias(resolution); capability != "" {
						capabilityRaw, _ := json.Marshal(capability)

						fields["capability"] = capabilityRaw
					}
				}
			}
		}
	}

	toolRaw, _ := json.Marshal(tool)

	fields["tool"] = toolRaw
	delete(fields, "kind")

	payload, err := json.Marshal(fields)
	if err != nil {
		return "", false
	}

	return string(payload), true
}

func normalizeParsedToolRequestSyntax(result *toolRequestSyntaxNormalization) {
	if result == nil || !result.OK {
		return
	}

	if normalizeReadToolAlias(&result.Request) {
		result.Raw = marshalToolRequestRaw(result.Request)
		result.Repaired = true
	}

	if normalizeReadFileLinesAlias(result) {
		result.Raw = marshalToolRequestRaw(result.Request)
		result.Repaired = true
	}

	if normalizeCodeNavigateMissingCapability(&result.Request) {
		result.Raw = marshalToolRequestRaw(result.Request)
		result.Repaired = true
	}
}

func normalizeReadToolAlias(req *workspace.ToolRequest) bool {
	if req == nil || strings.TrimSpace(req.Tool) != "code_read" || strings.TrimSpace(req.Path) == "" {
		return false
	}

	req.Tool = "read_file"

	return true
}

func normalizeReadFileLinesAlias(result *toolRequestSyntaxNormalization) bool {
	if result == nil || strings.TrimSpace(result.Request.Tool) != "read_file" || result.Request.StartLine > 0 || result.Request.EndLine > 0 {
		return false
	}

	start, end, ok := readFileLinesAliasFromRaw(result.Raw)
	if !ok {
		return false
	}

	result.Request.StartLine = start
	result.Request.EndLine = end

	return true
}

func readFileLinesAliasFromRaw(raw string) (int, int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return 0, 0, false
	}

	var fields map[string]json.RawMessage

	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return 0, 0, false
	}

	value, ok := fields["lines"]
	if !ok {
		return 0, 0, false
	}

	var text string

	if err := json.Unmarshal(value, &text); err == nil {
		return parseReadFileLinesAlias(text)
	}

	var number int

	if err := json.Unmarshal(value, &number); err == nil {
		if number <= 0 {
			return 0, 0, false
		}

		return number, number, true
	}

	return 0, 0, false
}

func parseReadFileLinesAlias(raw string) (int, int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, 0, false
	}

	for _, sep := range []string{"-", ":"} {
		if left, right, ok := strings.Cut(raw, sep); ok {
			start, errStart := strconv.Atoi(strings.TrimSpace(left))
			end, errEnd := strconv.Atoi(strings.TrimSpace(right))

			if errStart != nil || errEnd != nil || start <= 0 || end < start {
				return 0, 0, false
			}

			return start, end, true
		}
	}

	line, err := strconv.Atoi(raw)
	if err != nil || line <= 0 {
		return 0, 0, false
	}

	return line, line, true
}

func normalizeCodeNavigateMissingCapability(req *workspace.ToolRequest) bool {
	if req == nil || strings.TrimSpace(req.Tool) != "code_navigate" || strings.TrimSpace(req.Capability) != "" {
		return false
	}

	if workspace.ContextGraphResolutionOnly(req.Resolution) {
		return false
	}

	mode := strings.TrimSpace(req.Mode)
	if mode != "" && workspace.CodeNavigateCapabilityAlias(mode) != "" {
		req.Capability = mode
		return true
	}

	pattern := strings.TrimSpace(req.Pattern)
	query := strings.TrimSpace(req.Query)
	symbol := strings.TrimSpace(req.Symbol)
	target := util.FirstNonEmpty(pattern, query, symbol)

	if target != "" && workspace.CodeNavigateCapabilityAlias(target) == "" {
		req.Capability = "read_symbol"
		if strings.TrimSpace(req.Pattern) == "" {
			req.Pattern = target
		}

		return true
	}

	if strings.TrimSpace(req.Path) == "" {
		return false
	}

	req.Capability = "file_outline"
	if target != "" && workspace.CodeNavigateCapabilityAlias(target) != "" {
		req.Pattern = ""
		req.Query = ""
		req.Symbol = ""
	}

	return true
}

func parseFunctionCallToolRequestSyntax(text string) (workspace.ToolRequest, bool, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		// Empty content is not a function call attempt -- it is a model producing no
		// visible output (often because everything went into the thinking block).
		// Classifying empty strings as "unsafe" causes the harness to skip
		// reasoning-content salvage paths that could recover the answer.
		return workspace.ToolRequest{}, false, false
	}

	if strings.Contains(trimmed, "\n") {
		if strings.Contains(trimmed, "(") && keyValueLikeText(trimmed) {
			return workspace.ToolRequest{}, false, false
		}

		return workspace.ToolRequest{}, false, false
	}

	if strings.Contains(trimmed, "{") || strings.Contains(trimmed, "```") {
		return workspace.ToolRequest{}, false, true
	}

	key, body, ok := strings.Cut(trimmed, ":")
	if !ok || strings.ToLower(strings.TrimSpace(key)) != "tool" {
		if ok && keyValueMissingToolActionField(strings.ToLower(strings.TrimSpace(key))) {
			return workspace.ToolRequest{}, false, true
		}

		return workspace.ToolRequest{}, false, false
	}

	body = strings.TrimSpace(body)

	open := strings.Index(body, "(")

	if open < 0 {
		return workspace.ToolRequest{}, false, true
	}

	if !strings.HasSuffix(body, ")") {
		return workspace.ToolRequest{}, false, true
	}

	tool := strings.TrimSpace(body[:open])
	if !toolRequestSyntaxIdentifier(tool) {
		return workspace.ToolRequest{}, false, true
	}

	args := strings.TrimSpace(body[open+1 : len(body)-1])
	values, ok := parseFunctionCallToolArguments(args)

	if !ok {
		return workspace.ToolRequest{}, false, true
	}

	req, ok := toolRequestFromSyntaxValues(tool, values)
	if !ok {
		return workspace.ToolRequest{}, false, true
	}

	return req, true, false
}

func parseFunctionCallToolArguments(args string) (map[string]string, bool) {
	values := map[string]string{}
	if strings.TrimSpace(args) == "" {
		return values, true
	}

	for _, part := range splitFunctionCallArguments(args) {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, false
		}

		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return nil, false
		}

		key = strings.ToLower(strings.TrimSpace(key))
		if !keyValueToolRequestFieldAllowed(key) {
			return nil, false
		}

		decoded, ok := decodeFunctionCallArgumentValue(value)
		if !ok || decoded == "" {
			return nil, false
		}

		values[key] = decoded
	}

	return values, true
}

func splitFunctionCallArguments(args string) []string {
	var (
		parts []string
		b     strings.Builder
	)

	quote := rune(0)
	escaped := false

	for _, r := range args {
		if escaped {
			b.WriteRune(r)

			escaped = false

			continue
		}

		if r == '\\' && quote != 0 {
			b.WriteRune(r)

			escaped = true

			continue
		}

		if quote != 0 {
			b.WriteRune(r)

			if r == quote {
				quote = 0
			}

			continue
		}

		if r == '"' || r == '\'' {
			quote = r
			b.WriteRune(r)

			continue
		}

		if r == ',' {
			parts = append(parts, b.String())
			b.Reset()

			continue
		}

		b.WriteRune(r)
	}

	parts = append(parts, b.String())

	return parts
}

func decodeFunctionCallArgumentValue(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}

	if strings.HasPrefix(value, `"`) {
		decoded, err := strconv.Unquote(value)
		if err != nil {
			return "", false
		}

		return strings.TrimSpace(decoded), true
	}

	if strings.HasPrefix(value, `'`) {
		if !strings.HasSuffix(value, `'`) || len(value) < 2 {
			return "", false
		}

		return strings.TrimSpace(strings.Trim(value, `'`)), true
	}

	if strings.ContainsAny(value, " \t") {
		return "", false
	}

	return strings.Trim(value, `"'`), true
}

func toolRequestSyntaxIdentifier(value string) bool {
	if value == "" {
		return false
	}

	for i, r := range value {
		if r == '_' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || (i > 0 && '0' <= r && r <= '9') {
			continue
		}

		return false
	}

	return true
}

func parseKeyValueToolRequestSyntax(text string) (workspace.ToolRequest, bool, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return workspace.ToolRequest{}, false, false
	}

	parsed := ParseToolRequestFromTokenizer(keyValueToolRequestTokenizer{}, trimmed)
	if parsed.Unsafe {
		return workspace.ToolRequest{}, false, true
	}

	if !parsed.OK {
		return workspace.ToolRequest{}, false, false
	}

	return parsed.Request, true, false
}

func toolRequestFromSyntaxValues(tool string, values map[string]string) (workspace.ToolRequest, bool) {
	req := workspace.ToolRequest{
		Tool:            tool,
		ID:              values["id"],
		Key:             values["key"],
		Path:            values["path"],
		Pattern:         values["pattern"],
		Query:           values["query"],
		Symbol:          values["symbol"],
		Capability:      values["capability"],
		Operation:       values["operation"],
		Action:          values["action"],
		Mode:            values["mode"],
		Task:            values["task"],
		Glob:            values["glob"],
		Language:        values["language"],
		Resolution:      values["resolution"],
		NamedGap:        values["named_gap"],
		WhyNotAnswering: values["why_not_answering"],
		Purpose:         values["purpose"],
		Command:         values["command"],
		Description:     values["description"],
		Content:         values["content"],
		Prompt:          values["prompt"],
		Agent:           values["agent"],
		Workdir:         values["workdir"],
		Handle:          values["handle"],
		Root:            values["root"],
		Destination:     values["destination"],
		Status:          values["status"],
		PacketPath:      values["packet_path"],
		OutputPath:      values["output_path"],
		Component:       values["component"],
		Category:        values["category"],
		SessionID:       values["session_id"],
		Confidence:      values["confidence"],
		Severity:        values["severity"],
		SuggestedFix:    values["suggested_fix"],
		Expected:        values["expected"],
		Observed:        values["observed"],
		Workaround:      values["workaround"],
		Impact:          values["impact"],
		TaskID:          values["task_id"],
		OldText:         values["old_text"],
		NewText:         values["new_text"],
	}
	if strings.TrimSpace(req.Capability) == "" && strings.TrimSpace(req.Operation) != "" {
		req.Capability = req.Operation
	}

	if strings.TrimSpace(req.Capability) == "" && strings.TrimSpace(req.Action) != "" && syntaxToolRequestUsesCapabilityAlias(req.Tool) {
		req.Capability = req.Action
	}

	if strings.TrimSpace(req.Capability) == "" && strings.TrimSpace(values["kind"]) != "" && syntaxToolRequestUsesCapabilityAlias(req.Tool) {
		req.Capability = values["kind"]
	}

	if strings.TrimSpace(req.Capability) == "" && strings.TrimSpace(req.Resolution) != "" && syntaxToolRequestUsesCapabilityAlias(req.Tool) {
		req.Capability = workspace.CodeNavigateResolutionCapabilityAlias(req.Resolution)
	}

	if strings.TrimSpace(req.Path) == "" && strings.TrimSpace(values["scope"]) != "" {
		req.Path = values["scope"]
	}

	if strings.TrimSpace(req.Pattern) == "" && strings.TrimSpace(req.Symbol) != "" {
		req.Pattern = req.Symbol
	}

	if strings.TrimSpace(req.Pattern) == "" && strings.TrimSpace(req.Query) != "" && syntaxToolRequestUsesPatternAlias(req.Tool) {
		req.Pattern = req.Query
	}

	// Parse read_file lines alias: "lines: N-M" becomes start_line=N,
	// end_line=M; "lines: N" becomes start_line=N, end_line=N.
	if raw := strings.TrimSpace(values["lines"]); strings.TrimSpace(req.Tool) == "read_file" &&
		strings.TrimSpace(values["start_line"]) == "" && strings.TrimSpace(values["end_line"]) == "" && raw != "" {

		start, end, ok := parseReadFileLinesAlias(raw)
		if !ok {
			return workspace.ToolRequest{}, false
		}

		req.StartLine = start
		req.EndLine = end
	}

	for key, assign := range map[string]func(int){
		"start_line":    func(v int) { req.StartLine = v },
		"end_line":      func(v int) { req.EndLine = v },
		"limit":         func(v int) { req.Limit = v },
		"budget_tokens": func(v int) { req.BudgetTokens = v },
		"max_depth":     func(v int) { req.MaxDepth = v },
	} {
		if raw := strings.TrimSpace(values[key]); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil {
				return workspace.ToolRequest{}, false
			}

			assign(v)
		}
	}

	for key, assign := range map[string]func(bool){
		"include_evidence": func(v bool) { req.IncludeEvidence = v },
		"include_source":   func(v bool) { req.IncludeSource = v },
		"include_files":    func(v bool) { req.IncludeFiles = v },
		"refresh":          func(v bool) { req.Refresh = v },
		"replace_all":      func(v bool) { req.ReplaceAll = v },
	} {
		if raw := strings.TrimSpace(values[key]); raw != "" {
			v, err := strconv.ParseBool(raw)
			if err != nil {
				return workspace.ToolRequest{}, false
			}

			assign(v)
		}
	}

	return req, true
}

func syntaxToolRequestUsesPatternAlias(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "search_content", "rg", "grep", "search_files":
		return true
	default:
		return false
	}
}

func syntaxToolRequestUsesCapabilityAlias(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "code_navigate", "read_only_capability":
		return true
	default:
		return false
	}
}

func keyValueToolRequestFieldAllowed(key string) bool {
	switch key {
	case "tool", "id", "key", "path", "file", "scope", "pattern", "query", "capability", "operation", "action", "kind", "mode", "task", "glob", "language", "resolution",
		"symbol", "named_gap", "why_not_answering", "start_line", "end_line", "limit", "budget_tokens", "max_depth",
		"include_evidence", "include_source", "include_files", "refresh", "lines", "replace_all",
		"purpose", "command", "description", "content", "prompt", "agent", "workdir", "handle", "root", "destination", "status",
		"packet_path", "output_path", "component", "category", "session_id", "confidence", "severity", "suggested_fix",
		"expected", "observed", "workaround", "impact", "task_id", "recursive", "old_text", "new_text", "new_text_ref", "content_ref":
		return true
	default:
		return false
	}
}

func classifyUnsupportedTextualToolShape(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}

	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "```json") || strings.Contains(trimmed, "{") {
		return toolRequestNormalizerOutcomeUnsafeToInfer
	}

	if strings.Contains(trimmed, ":") && keyValueLikeText(trimmed) {
		return toolRequestNormalizerOutcomeUnsafeToInfer
	}

	return ""
}

func keyValueLikeText(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		key, _, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}

		if keyValueToolRequestFieldAllowed(strings.ToLower(strings.TrimSpace(key))) {
			return true
		}
	}

	return false
}

func toolRequestPayloadMissingExplicitTool(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return false
	}

	var fields map[string]json.RawMessage

	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return false
	}

	_, hasTool := fields["tool"]

	return !hasTool
}

func missingToolInferenceNeedsTypedAuthority(req workspace.ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case "append_file",
		"apply_patch",
		"delegate_edit",
		"edit_file",
		"go_move_declarations",
		"mkdir",
		"shell",
		"submit_plan",
		"task",
		"task_ledger",
		"write_file":
		return true
	case workspace.ToolNameReadFile:
		return strings.TrimSpace(req.Path) == "" && strings.TrimSpace(req.Handle) == ""
	case workspace.ToolNameSearchContent, workspace.ToolNameRg:
		return strings.TrimSpace(req.Query) == "" && strings.TrimSpace(req.Pattern) == ""
	case workspace.ToolNameSearchFiles:
		return strings.TrimSpace(req.Query) == "" && strings.TrimSpace(req.Glob) == ""
	case workspace.ToolNameCodeNavigate:
		return strings.TrimSpace(req.Path) == "" && strings.TrimSpace(req.Handle) == "" && strings.TrimSpace(req.Symbol) == ""
	case workspace.ToolNameContextPack:
		return strings.TrimSpace(req.Task) == "" && strings.TrimSpace(req.Query) == ""
	default:
		return false
	}
}

func unsafeTextualWriteAttempt(text string) (tool string, transport string, ok bool) {
	normalization := normalizeToolRequestSyntax(llama.ChatMessage{Content: text})
	if normalization.OK ||
		normalization.ParseErr != nil ||
		normalization.EventOutcome != toolRequestNormalizerOutcomeUnsafeToInfer {

		return "", "", false
	}

	tool = unsafeTextualToolName(text)
	if !workspace.IsWorkspaceWriteTool(tool) {
		return "", "", false
	}

	return tool, strings.TrimSpace(normalization.EventTransport), true
}

func unsafeTextualToolName(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		key, body, ok := strings.Cut(line, ":")
		if !ok || strings.ToLower(strings.TrimSpace(key)) != "tool" {
			return ""
		}

		body = strings.TrimSpace(body)
		if left, _, ok := strings.Cut(body, "("); ok {
			body = strings.TrimSpace(left)
		}

		if left, _, ok := strings.Cut(body, " "); ok {
			body = strings.TrimSpace(left)
		}

		return strings.ToLower(strings.TrimSpace(body))
	}

	return ""
}

// ToolRequestTokenizer converts raw model output into a tool name and
// key-value field map.
type ToolRequestTokenizer interface {
	Tokenize(raw string) ToolRequestTokenization
}

type ToolRequestTokenization struct {
	Tool   string
	Fields map[string]string
	OK     bool
	Unsafe bool
}

// ParseResult is the unified output of all tokenizers after normalization.
type ParseResult struct {
	Request workspace.ToolRequest
	Raw     string
	OK      bool
	Unsafe  bool
	// Transport identifies the text format that was parsed, for telemetry.
	Transport string
}

// ParseToolRequestFromTokenizer runs a tokenizer and converts its output
// through the shared toolRequestFromSyntaxValues function. This is the
// single convergence point for all parsing strategies.
func ParseToolRequestFromTokenizer(t ToolRequestTokenizer, raw string) ParseResult {
	tokens := t.Tokenize(raw)
	if tokens.Unsafe {
		return ParseResult{Unsafe: true}
	}

	if !tokens.OK {
		return ParseResult{}
	}

	req, ok := toolRequestFromSyntaxValues(tokens.Tool, tokens.Fields)
	if !ok {
		return ParseResult{Unsafe: true}
	}

	return ParseResult{
		Request: req,
		Raw:     marshalToolRequestRaw(req),
		OK:      true,
	}
}

// ParseWithAllTokenizers tries each registered tokenizer in priority order
// and returns the first successful parse. Priority order matches the legacy
// normalizeToolRequestSyntax cascade.
func ParseWithAllTokenizers(raw string) ParseResult {
	type namedTokenizer struct {
		name      string
		tokenizer ToolRequestTokenizer
	}

	tokenizers := []namedTokenizer{
		{"nested_arguments", nestedArgumentsToolRequestTokenizer{}},
		{"fenced_json", fencedJSONToolRequestTokenizer{}},
		{"idiom_cli", idiomCLIToolRequestTokenizer{}},
		{"key_value_args", keyValueArgsToolRequestTokenizer{}},
		{"function_call", functionCallToolRequestTokenizer{}},
		{"key_value", keyValueToolRequestTokenizer{}},
	}

	for _, nt := range tokenizers {
		result := ParseToolRequestFromTokenizer(nt.tokenizer, raw)
		if result.Unsafe {
			return ParseResult{Unsafe: true, Transport: nt.name}
		}

		if result.OK {
			result.Transport = nt.name

			return result
		}
	}

	return ParseResult{}
}

type keyValueToolRequestTokenizer struct{}

func (keyValueToolRequestTokenizer) Tokenize(text string) ToolRequestTokenization {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ToolRequestTokenization{}
	}

	lines := strings.Split(trimmed, "\n")
	toolLineIdx := -1
	proseLines := 0

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		key, _, ok := strings.Cut(line, ":")
		if ok && util.EqualTrimFold(key, "tool") {
			toolLineIdx = i
			break
		}

		proseLines++
		if proseLines > 5 {
			return ToolRequestTokenization{Unsafe: true}
		}
	}

	if toolLineIdx < 0 {
		if keyValueNoToolUnsafe(lines) {
			return ToolRequestTokenization{Unsafe: true}
		}

		return ToolRequestTokenization{}
	}

	values := map[string]string{}
	sawKeyValue := false
	lastKey := ""
	continuationLines := 0
	appendContinuation := func(line string) bool {
		if !keyValueContinuationField(lastKey) {
			return true
		}

		continuationLines++
		if continuationLines > 8 || len(values[lastKey])+len(line)+1 > 8192 {
			return false
		}

		values[lastKey] += "\n" + line

		return true
	}

	for _, rawLine := range lines[toolLineIdx:] {
		line := strings.TrimRight(rawLine, "\r")
		trimmedLine := strings.TrimSpace(line)

		if trimmedLine == "" {
			if sawKeyValue && keyValueBlankContinuationField(lastKey) {
				if !appendContinuation(line) {
					return ToolRequestTokenization{Unsafe: true}
				}

				continue
			}

			if sawKeyValue && strings.TrimSpace(values["tool"]) != "" {
				break
			}

			continue
		}

		key, value, ok := strings.Cut(line, ":")
		if !ok {
			if sawKeyValue {
				if !appendContinuation(line) {
					return ToolRequestTokenization{Unsafe: true}
				}

				continue
			}

			return ToolRequestTokenization{Unsafe: true}
		}

		key = strings.ToLower(strings.TrimSpace(key))
		if sawKeyValue && keyValueRawValueField(lastKey) && !keyValueRawFieldStructuralTransition(lastKey, key) {
			if !appendContinuation(line) {
				return ToolRequestTokenization{Unsafe: true}
			}

			continue
		}

		if keyValueRawValueField(key) {
			value = normalizeRawKeyValueField(value)
		} else {
			value = trimMatchingSyntaxQuotes(strings.TrimSpace(value))
		}

		if key == "" || (value == "" && !keyValueAllowsEmptyValue(key, values)) {
			return ToolRequestTokenization{Unsafe: true}
		}

		if key == "tool" && strings.TrimSpace(values["tool"]) != "" {
			break
		}

		if !keyValueToolRequestFieldAllowed(key) {
			if sawKeyValue && keyValueContinuationField(lastKey) {
				if !appendContinuation(line) {
					return ToolRequestTokenization{Unsafe: true}
				}

				continue
			}

			return ToolRequestTokenization{Unsafe: true}
		}

		sawKeyValue = true
		continuationLines = 0
		values[key] = value
		lastKey = key
	}

	if !sawKeyValue {
		return ToolRequestTokenization{Unsafe: true}
	}

	tool := strings.TrimSpace(values["tool"])
	if tool == "" {
		return ToolRequestTokenization{Unsafe: true}
	}

	return ToolRequestTokenization{Tool: tool, Fields: values, OK: true}
}

func keyValueNoToolUnsafe(lines []string) bool {
	for _, line := range lines {
		key, _, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}

		if keyValueMissingToolActionField(strings.ToLower(strings.TrimSpace(key))) {
			return true
		}
	}

	return false
}

func keyValueMissingToolActionField(key string) bool {
	switch key {
	case "path", "file", "scope", "pattern", "query", "symbol", "capability", "operation", "action", "kind",
		"mode", "task", "glob", "language", "resolution", "named_gap", "why_not_answering", "start_line",
		"end_line", "limit", "budget_tokens", "max_depth", "include_evidence", "include_source", "include_files",
		"refresh", "lines", "replace_all", "purpose", "command", "content", "prompt", "agent", "workdir",
		"handle", "root", "destination", "packet_path", "output_path", "component", "category", "session_id",
		"task_id", "recursive", "old_text", "new_text", "new_text_ref", "content_ref":
		return true
	default:
		return false
	}
}

func normalizeRawKeyValueField(value string) string {
	if strings.HasPrefix(value, " ") || strings.HasPrefix(value, "\t") {
		value = value[1:]
	}

	return trimMatchingSyntaxQuotes(value)
}

func trimMatchingSyntaxQuotes(value string) string {
	if len(value) < 2 {
		return value
	}

	first := value[0]
	last := value[len(value)-1]

	if (first == '"' || first == '\'') && first == last {
		return value[1 : len(value)-1]
	}

	return value
}

func keyValueRawValueField(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "old_text", "new_text", "content", "prompt":
		return true
	default:
		return false
	}
}

func keyValueBlankContinuationField(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "old_text", "new_text", "content", "prompt":
		return true
	default:
		return false
	}
}

func keyValueRawFieldStructuralTransition(lastKey, key string) bool {
	switch strings.ToLower(strings.TrimSpace(lastKey)) {
	case "old_text":
		return key == "new_text"
	case "new_text":
		switch key {
		case "description", "task_id", "replace_all":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func keyValueAllowsEmptyValue(key string, values map[string]string) bool {
	return key == "new_text" && strings.TrimSpace(values["tool"]) == "edit_file"
}

func keyValueContinuationField(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "prompt", "content", "old_text", "new_text", "description", "purpose", "task", "query", "named_gap", "why_not_answering", "expected", "observed", "workaround", "impact", "suggested_fix":
		return true
	default:
		return false
	}
}

// fencedJSONToolRequestTokenizer handles model output wrapped in a fenced
// JSON code block:
//
//	```json
//	{"tool": "read_file", "path": "some/path", "limit": 50}
//	```
type fencedJSONToolRequestTokenizer struct{}

func (fencedJSONToolRequestTokenizer) Tokenize(text string) ToolRequestTokenization {
	payload, ok, unsafe := exactFencedJSONPayload(text)
	if !ok {
		if unsafe {
			return ToolRequestTokenization{Unsafe: true}
		}

		return ToolRequestTokenization{}
	}

	var obj map[string]json.RawMessage

	if err := json.Unmarshal([]byte(payload), &obj); err != nil {
		return ToolRequestTokenization{Unsafe: true}
	}

	toolRaw, ok := obj["tool"]
	if !ok {
		return ToolRequestTokenization{Unsafe: true}
	}

	var tool string

	if err := json.Unmarshal(toolRaw, &tool); err != nil || strings.TrimSpace(tool) == "" {
		return ToolRequestTokenization{Unsafe: true}
	}

	fields := make(map[string]string, len(obj))

	for key, raw := range obj {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "tool" {
			continue
		}

		if !keyValueToolRequestFieldAllowed(key) {
			continue
		}

		var value string

		if err := json.Unmarshal(raw, &value); err != nil {
			// If it's not a string, try marshaling back to JSON string.
			compact, err := json.Marshal(raw)
			if err != nil {
				continue
			}

			value = string(compact)
		}

		value = strings.TrimSpace(value)

		if value == "" && !keyValueAllowsEmptyValue(key, fields) {
			continue
		}

		fields[key] = value
	}

	return ToolRequestTokenization{Tool: tool, Fields: fields, OK: true}
}

// functionCallToolRequestTokenizer handles single-line function call syntax:
//
//	tool: read_file(path="some/path", limit=50)
type functionCallToolRequestTokenizer struct{}

func (functionCallToolRequestTokenizer) Tokenize(text string) ToolRequestTokenization {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ToolRequestTokenization{}
	}

	// Must be single-line to be a function call.
	if strings.Contains(trimmed, "\n") {
		return ToolRequestTokenization{}
	}

	if strings.Contains(trimmed, "{") || strings.Contains(trimmed, "```") {
		return ToolRequestTokenization{Unsafe: true}
	}

	key, body, ok := strings.Cut(trimmed, ":")
	if !ok || strings.ToLower(strings.TrimSpace(key)) != "tool" {
		return ToolRequestTokenization{}
	}

	body = strings.TrimSpace(body)

	open := strings.Index(body, "(")

	if open < 0 {
		return ToolRequestTokenization{Unsafe: true}
	}

	if !strings.HasSuffix(body, ")") {
		return ToolRequestTokenization{Unsafe: true}
	}

	tool := strings.TrimSpace(body[:open])
	if !toolRequestSyntaxIdentifier(tool) {
		return ToolRequestTokenization{Unsafe: true}
	}

	args := strings.TrimSpace(body[open+1 : len(body)-1])
	values, ok := parseFunctionCallToolArguments(args)
	if !ok {
		return ToolRequestTokenization{Unsafe: true}
	}

	return ToolRequestTokenization{Tool: tool, Fields: values, OK: true}
}

// idiomCLIToolRequestTokenizer handles CLI-style invocation:
//
//	idiom tool read_file {"path": "some/path"}
type idiomCLIToolRequestTokenizer struct{}

func (idiomCLIToolRequestTokenizer) Tokenize(text string) ToolRequestTokenization {
	trimmed := strings.TrimSpace(text)
	lower := strings.ToLower(trimmed)

	if !strings.HasPrefix(lower, "idiom tool ") {
		return ToolRequestTokenization{}
	}

	objectStart := strings.Index(trimmed, "{")
	if objectStart < 0 {
		return ToolRequestTokenization{Unsafe: true}
	}

	prefix := strings.TrimSpace(trimmed[:objectStart])
	parts := strings.Fields(prefix)

	if len(parts) < 3 || !strings.EqualFold(parts[0], "idiom") || !strings.EqualFold(parts[1], "tool") || !toolRequestSyntaxIdentifier(parts[2]) {
		return ToolRequestTokenization{Unsafe: true}
	}

	object, _, ok := extractJSONObjectAt(trimmed, objectStart)
	if !ok {
		return ToolRequestTokenization{Unsafe: true}
	}

	var obj map[string]json.RawMessage

	if err := json.Unmarshal([]byte(object), &obj); err != nil {
		return ToolRequestTokenization{Unsafe: true}
	}

	tool := parts[2]

	// If JSON specifies a different tool, it must match.
	if toolRaw, ok := obj["tool"]; ok {
		var jsonTool string

		if err := json.Unmarshal(toolRaw, &jsonTool); err == nil && strings.TrimSpace(jsonTool) != "" {
			if !strings.EqualFold(tool, jsonTool) {
				return ToolRequestTokenization{Unsafe: true}
			}
		}
	}

	fields := make(map[string]string, len(obj))

	for key, raw := range obj {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "tool" {
			continue
		}

		if !keyValueToolRequestFieldAllowed(key) {
			continue
		}

		var value string

		if err := json.Unmarshal(raw, &value); err != nil {
			compact, err := json.Marshal(raw)
			if err != nil {
				continue
			}

			value = string(compact)
		}

		value = strings.TrimSpace(value)

		if value == "" && !keyValueAllowsEmptyValue(key, fields) {
			continue
		}

		fields[key] = value
	}

	return ToolRequestTokenization{Tool: tool, Fields: fields, OK: true}
}

// keyValueArgsToolRequestTokenizer handles args-block invocation:
//
//	tool: name
//	args: {"key": "value", ...}
type keyValueArgsToolRequestTokenizer struct{}

func (keyValueArgsToolRequestTokenizer) Tokenize(text string) ToolRequestTokenization {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || !strings.Contains(trimmed, "{") || !strings.Contains(strings.ToLower(trimmed), "args:") {
		return ToolRequestTokenization{}
	}

	tool, argsStart, ok := keyValueArgsToolAndBodyStart(trimmed)
	if !ok {
		return ToolRequestTokenization{Unsafe: true}
	}

	objectOffset := strings.Index(trimmed[argsStart:], "{")
	if objectOffset < 0 {
		return ToolRequestTokenization{Unsafe: true}
	}

	objectStart := argsStart + objectOffset
	object, _, ok := extractJSONObjectAt(trimmed, objectStart)
	if !ok {
		return ToolRequestTokenization{Unsafe: true}
	}

	payload, ok := normalizeKeyValueArgsObject(tool, object)
	if !ok {
		return ToolRequestTokenization{Unsafe: true}
	}

	var obj map[string]json.RawMessage

	if err := json.Unmarshal([]byte(payload), &obj); err != nil {
		return ToolRequestTokenization{Unsafe: true}
	}

	toolRaw, ok := obj["tool"]
	if !ok {
		return ToolRequestTokenization{Unsafe: true}
	}

	var jsonTool string

	if err := json.Unmarshal(toolRaw, &jsonTool); err != nil || strings.TrimSpace(jsonTool) == "" {
		return ToolRequestTokenization{Unsafe: true}
	}

	fields := make(map[string]string, len(obj))

	for key, raw := range obj {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "tool" {
			continue
		}

		if !keyValueToolRequestFieldAllowed(key) {
			continue
		}

		var value string

		if err := json.Unmarshal(raw, &value); err != nil {
			compact, err := json.Marshal(raw)
			if err != nil {
				continue
			}

			value = string(compact)
		}

		value = strings.TrimSpace(value)

		if value == "" && !keyValueAllowsEmptyValue(key, fields) {
			continue
		}

		fields[key] = value
	}

	return ToolRequestTokenization{Tool: jsonTool, Fields: fields, OK: true}
}

// nestedArgumentsToolRequestTokenizer handles a JSON wrapper with the payload
// inside an "arguments" field:
//
//	{"arguments": {"tool": "read_file", "path": "some/path", "limit": 50}}
//
// The outer object must have exactly one key ("arguments") whose value is a
// valid JSON ToolRequest.
type nestedArgumentsToolRequestTokenizer struct{}

func (nestedArgumentsToolRequestTokenizer) Tokenize(text string) ToolRequestTokenization {
	raw := strings.TrimSpace(text)
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return ToolRequestTokenization{}
	}

	var obj map[string]json.RawMessage

	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return ToolRequestTokenization{}
	}

	if len(obj) != 1 {
		return ToolRequestTokenization{}
	}

	args, ok := obj["arguments"]
	if !ok || !json.Valid(args) {
		return ToolRequestTokenization{}
	}

	var inner map[string]json.RawMessage

	if err := json.Unmarshal(args, &inner); err != nil {
		return ToolRequestTokenization{Unsafe: true}
	}

	toolRaw, ok := inner["tool"]
	if !ok {
		return ToolRequestTokenization{Unsafe: true}
	}

	var tool string

	if err := json.Unmarshal(toolRaw, &tool); err != nil || strings.TrimSpace(tool) == "" {
		return ToolRequestTokenization{Unsafe: true}
	}

	fields := make(map[string]string, len(inner))

	for key, raw := range inner {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "tool" {
			continue
		}

		if !keyValueToolRequestFieldAllowed(key) {
			continue
		}

		var value string

		if err := json.Unmarshal(raw, &value); err != nil {
			compact, err := json.Marshal(raw)
			if err != nil {
				continue
			}

			value = string(compact)
		}

		value = strings.TrimSpace(value)

		if value == "" && !keyValueAllowsEmptyValue(key, fields) {
			continue
		}

		fields[key] = value
	}

	return ToolRequestTokenization{Tool: tool, Fields: fields, OK: true}
}

// textToNormalizerTransport maps the tokenizer's transport identifier to the
// corresponding normalizer transport constant for telemetry parity.
func textToNormalizerTransport(transport string) string {
	switch transport {
	case "nested_arguments":
		return toolRequestNormalizerTransportNestedArguments
	case "fenced_json":
		return toolRequestNormalizerTransportFencedJSON
	case "idiom_cli":
		return toolRequestNormalizerTransportIdiomCLI
	case "key_value_args":
		return toolRequestNormalizerTransportKeyValueArgs
	case "function_call":
		return toolRequestNormalizerTransportFunctionCall
	case "key_value":
		return toolRequestNormalizerTransportKeyValue
	default:
		return toolRequestNormalizerTransportTextual
	}
}

// buildLlamaTools converts workspace tool definitions into the shape the
// llama client may send to the server — i.e. OpenAI function-calling specs.
// Some backends intentionally downgrade to idiom's JSON tool protocol at the
// llama client boundary when their native parser cannot be trusted to return
// structured errors.
// Called once per turn when the runner sets up ChatOptions. Native-capable
// backends such as llama.cpp can route the model's tool_calls through the
// OpenAI channel; downgraded backends still receive the same tool manifest in
// the runtime brief for idiom-side JSON parsing.
func buildLlamaTools(defs []workspace.ToolDefinition) []llama.Tool {
	if len(defs) == 0 {
		return nil
	}

	out := make([]llama.Tool, 0, len(defs))
	for _, def := range defs {
		params := json.RawMessage(def.ParametersJSON)
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}

		out = append(out, llama.Tool{
			Type: "function",
			Function: llama.ToolFunction{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  params,
			},
		})
	}

	return out
}

// postReadinessRequiredFields lists the JSON Schema properties required on
// read-class source-refresh calls only when the consolidated runner contract
// selects an explicit mutation/source-refresh surface. Answerable read-only
// evidence contracts to a final-answer surface instead.
var postReadinessRequiredFields = []string{"named_gap", "why_not_answering"}

// applyPostReadinessSchemaRequirements rewrites the JSON Schema of every
// read-class tool in tools to mark `named_gap` and `why_not_answering` as
// required and to ensure both properties are declared. Generic across
// tools — anything that is_read_class returns true for picks up the same
// schema constraint. Non-read-class tools are returned unchanged.
//
// For `read_file`, also require a concrete path and line range. After
// readiness, an unanchored or path-wide file read is nearly always an
// inefficient substitute for either answering or choosing a symbol/range probe,
// so push that choice into the tool contract instead of spending a model turn
// on a runtime rejection.
//
// Called from the runner's rebuildTools only when
// postReadinessToolContractForBrief returns RequireSourceRefreshContract.
// Idempotent: calling twice in a row produces the same schema.
func applyPostReadinessSchemaRequirements(tools []llama.Tool) []llama.Tool {
	if len(tools) == 0 {
		return tools
	}

	out := make([]llama.Tool, len(tools))
	for i, tool := range tools {
		if !isPostReadinessGapTool(tool.Function.Name) {
			out[i] = tool
			continue
		}

		mutated, ok := mutateSchemaRequireFields(tool.Function.Parameters, postReadinessRequiredFields)
		if !ok {
			out[i] = tool
			continue
		}

		if tool.Function.Name == "read_file" {
			if narrowed, narrowOK := mutateSchemaRequireLineRange(mutated); narrowOK {
				mutated = narrowed
			}
		} else if isPostReadinessLineRangeTool(tool.Function.Name) {
			if exposed, exposeOK := mutateSchemaExposeLineRange(mutated); exposeOK {
				mutated = exposed
			}
		}

		tool.Function.Parameters = mutated
		out[i] = tool
	}

	return out
}

// stripPostReadinessBroadTools removes tools that are inappropriate under an
// explicit mutation/source-refresh contract. Broad file discovery
// (search_files, list_files) encourages rediscovery instead of resolving the
// scoped write contract. Answerable read-only evidence uses a separate
// final-answer surface instead of schema-constrained source refresh.
func stripPostReadinessBroadTools(tools []llama.Tool) []llama.Tool {
	if len(tools) == 0 {
		return tools
	}

	out := make([]llama.Tool, 0, len(tools))
	for _, t := range tools {
		name := strings.TrimSpace(t.Function.Name)
		if name == "search_files" || name == "list_files" {
			continue
		}

		out = append(out, t)
	}

	return out
}

func mutateSchemaExposeLineRange(raw json.RawMessage) (json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}

	var schema map[string]any

	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, false
	}

	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		props = make(map[string]any)
	}

	for _, name := range []string{"start_line", "end_line"} {
		entry, _ := props[name].(map[string]any)
		if entry == nil {
			entry = map[string]any{
				"type":        "integer",
				"description": "optional 1-indexed line number for an exact follow-up range after readiness",
			}
		}

		entry["minimum"] = 1
		props[name] = entry
	}

	schema["properties"] = props

	updated, err := json.Marshal(schema)
	if err != nil {
		return nil, false
	}

	return json.RawMessage(updated), true
}

func mutateSchemaRequireLineRange(raw json.RawMessage) (json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}

	var schema map[string]any

	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, false
	}

	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		props = make(map[string]any)
	}

	if _, ok := props["path"]; !ok {
		props["path"] = map[string]any{
			"type":        "string",
			"description": "repository-relative file path; required after readiness so read_file has a concrete target",
		}
	}

	for _, name := range []string{"start_line", "end_line"} {
		entry, _ := props[name].(map[string]any)
		if entry == nil {
			entry = map[string]any{
				"type":        "integer",
				"description": "1-indexed line number; required after readiness so read_file remains a narrow probe",
			}
		}

		entry["minimum"] = 1
		props[name] = entry
	}

	schema["properties"] = props

	requiredSet := make(map[string]struct{})

	if existing, ok := schema["required"].([]any); ok {
		for _, v := range existing {
			if s, ok := v.(string); ok {
				requiredSet[s] = struct{}{}
			}
		}
	}

	for _, name := range []string{"path", "start_line", "end_line"} {
		requiredSet[name] = struct{}{}
	}

	required := make([]string, 0, len(requiredSet))

	if existing, ok := schema["required"].([]any); ok {
		for _, v := range existing {
			if s, ok := v.(string); ok {
				if _, seen := requiredSet[s]; seen {
					required = append(required, s)
					delete(requiredSet, s)
				}
			}
		}
	}

	for _, name := range []string{"path", "start_line", "end_line"} {
		if _, ok := requiredSet[name]; ok {
			required = append(required, name)
			delete(requiredSet, name)
		}
	}

	schema["required"] = required

	updated, err := json.Marshal(schema)
	if err != nil {
		return nil, false
	}

	return json.RawMessage(updated), true
}

// isPostReadinessGapTool reports whether a tool name is read-class — i.e.
// the kind of call that, after readiness or a child packet, must carry a
// named_gap. Mirrors workspace.IsReadClassRequest's tool list at the name
// level so we don't need to reconstruct ToolRequest shapes here.
func isPostReadinessGapTool(name string) bool {
	switch name {
	case "read_file", "search_content", "search_files", "list_files",
		"harness_audit", "discovery_query", "structural_search", "ctags_symbols",
		"go_read_symbol", "go_file_outline", "go_extract_body",
		"go_workspace_symbol", "go_references", "go_definition",
		"go_calls_from", "go_calls_into", "go_related_files",
		"cpp_read_symbol", "cpp_file_outline", "cpp_extract_body",
		"cpp_workspace_symbol", "cpp_references", "cpp_definition",
		"cpp_calls_from", "cpp_calls_into", "cpp_related_files",
		"python_read_symbol", "python_file_outline",
		"python_workspace_symbol", "python_references", "python_definition",
		"python_calls_from", "python_calls_into", "python_related_files",
		"ts_read_symbol", "ts_file_outline",
		"ts_workspace_symbol", "ts_references", "ts_definition",
		"ts_calls_from", "ts_calls_into", "ts_related_files",
		"rust_read_symbol", "rust_file_outline",
		"rust_workspace_symbol", "rust_references", "rust_definition",
		"rust_calls_from", "rust_calls_into", "rust_related_files",
		"java_read_symbol", "java_file_outline",
		"java_workspace_symbol", "java_references", "java_definition",
		"java_calls_from", "java_calls_into", "java_related_files",
		"dependency_graph", "history_range", "history_symbol", "blame_range",
		"semgrep_scan", "joern_scan":
		return true
	default:
		return false
	}
}

func isPostReadinessLineRangeTool(name string) bool {
	name = strings.TrimSpace(name)
	return strings.HasSuffix(name, "_read_symbol") || strings.HasSuffix(name, "_extract_body")
}

// mutateSchemaRequireFields takes a raw JSON Schema (object) and adds the
// given names to its `required` array and `properties` map. Returns the
// new raw schema and ok=true. Returns ok=false when the input is not an
// object schema we can sensibly extend.
func mutateSchemaRequireFields(raw json.RawMessage, fields []string) (json.RawMessage, bool) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{"type":"object","properties":{}}`)
	}

	var schema map[string]any

	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, false
	}

	if t, _ := schema["type"].(string); t != "" && t != "object" {
		return nil, false
	}

	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		props = make(map[string]any)
	}

	for _, name := range fields {
		if _, exists := props[name]; !exists || props[name].(map[string]any)["minLength"] == nil {
			props[name] = map[string]any{
				"type":        "string",
				"minLength":   1,
				"description": postReadinessFieldDescription(name),
			}
		}
	}

	schema["properties"] = props

	requiredSet := make(map[string]struct{})

	if existing, ok := schema["required"].([]any); ok {
		for _, v := range existing {
			if s, ok := v.(string); ok {
				requiredSet[s] = struct{}{}
			}
		}
	}

	for _, name := range fields {
		requiredSet[name] = struct{}{}
	}

	required := make([]string, 0, len(requiredSet))

	if existing, ok := schema["required"].([]any); ok {
		for _, v := range existing {
			if s, ok := v.(string); ok {
				if _, seen := requiredSet[s]; seen {
					required = append(required, s)
					delete(requiredSet, s)
				}
			}
		}
	}

	for _, name := range fields {
		if _, ok := requiredSet[name]; ok {
			required = append(required, name)
			delete(requiredSet, name)
		}
	}

	schema["required"] = required

	updated, err := json.Marshal(schema)
	if err != nil {
		return nil, false
	}

	return json.RawMessage(updated), true
}

func postReadinessFieldDescription(name string) string {
	switch name {
	case "named_gap":
		return "after answer-readiness or a child packet, name one exact unresolved claim this follow-up will verify or falsify; reference a specific symbol, file path, or narrow line range"
	case "why_not_answering":
		return "after answer-readiness or a child packet, state what would be materially wrong or missing if you answered now"
	default:
		return ""
	}
}

// toolResultMessage builds the message we append to turnMessages after
// a tool executes. When the call came in via native tool_calls (call is
// non-nil), we return a role:"tool" message linked by tool_call_id:
// OpenAI's documented shape, which Qwen was trained on. The main runner
// synthesizes a tool_call for legacy inline-JSON tool requests before
// recording the result, so file/tool output stays in the tool channel
// rather than becoming system-strength prompt text. The nil-call fallback
// remains only for low-level tests or non-runner callers.
func toolResultMessage(call *llama.ToolCall, result workspace.ToolResult) llama.ChatMessage {
	if call != nil {
		id := call.ID
		if id == "" {
			// DeepSeek and other strict OpenAI-protocol backends require
			// a tool message with tool_call_id after every tool_calls
			// message. Generate a synthetic ID when the call's ID is
			// empty (e.g., when the harness injects a rejected-result
			// message for a textual-tool backend that was normalized to
			// a native-tool history shape).
			id = fmt.Sprintf("rejected_%s_%d", call.Function.Name, time.Now().UnixNano())
		}

		return llama.ChatMessage{
			Role:       "tool",
			ToolCallID: id,
			Name:       call.Function.Name,
			Content:    workspace.FormatToolResultForModel(result),
		}
	}

	return llama.ChatMessage{
		Role:    "system",
		Content: workspace.FormatToolResultForModel(result),
	}
}

// toolResultTouchedPaths returns the set of workspace paths a tool call
// affected (or tried to read), so the InstructionAttacher can walk up
// from those directories and surface nearby configured instruction files once.
// Kept intentionally shallow: we only care about tools that name a
// single file or a patch envelope.
func toolResultTouchedPaths(req workspace.ToolRequest, _ workspace.ToolResult) []string {
	switch req.Tool {
	case "read_file", "edit_file", "write_file", "append_file", "code_navigate", "read_only_capability", "source_sanity", "go_read_symbol", "go_extract_body", "go_diagnostics", "go_file_outline", "cpp_read_symbol", "cpp_extract_body", "cpp_diagnostics":
		if req.Path != "" {
			return []string{req.Path}
		}
	case "apply_patch":
		ops, err := workspace.ParsePatch(req.Content)
		if err != nil {
			return nil
		}

		return workspace.PatchOpPaths(ops)
	}

	return nil
}

// parseToolCall unmarshals the arguments JSON the model produced in a
// ToolCall into a typed ToolRequest. The function name is the tool;
// for built-in tools the arguments JSON decodes directly into
// ToolRequest's field set (they overlap by design). For
// `mcp:<server>/<tool>` calls the raw JSON is preserved verbatim in
// ToolRequest.Args — MCP tools have arbitrary JSON-Schema parameters
// that do not map onto this struct's typed fields, and forcing them
// through the typed unmarshaller would silently drop fields. Returns
// ok=false when the call has no function name.
func parseToolCall(call llama.ToolCall) (workspace.ToolRequest, bool, error) {
	if call.Function.Name == "" {
		return workspace.ToolRequest{}, false, nil
	}

	req := workspace.ToolRequest{Tool: call.Function.Name}
	if strings.HasPrefix(call.Function.Name, "mcp:") {
		if call.Function.Arguments != "" {
			req.Args = json.RawMessage(call.Function.Arguments)
		} else {
			req.Args = json.RawMessage(`{}`)
		}

		return req, true, nil
	}

	if call.Function.Arguments != "" {
		decoded, err := workspace.DecodeToolRequestJSON([]byte(call.Function.Arguments))
		if err != nil {
			return workspace.ToolRequest{}, false, err
		}

		req = decoded
		req.Args = json.RawMessage(call.Function.Arguments)
	} else {
		req.Args = json.RawMessage(`{}`)
	}

	if strings.TrimSpace(req.Tool) == "" {
		req.Tool = call.Function.Name
	} else if strings.TrimSpace(req.Tool) != call.Function.Name {
		if call.Function.Name == "code_navigate" {
			if capability := workspace.CodeNavigateCapabilityAlias(req.Tool); capability != "" {
				if strings.TrimSpace(req.Capability) == "" {
					req.Capability = capability
				}

				req.Tool = call.Function.Name

				return req, true, nil
			}
		}

		return workspace.ToolRequest{}, false, fmt.Errorf("native tool call %q arguments declared tool %q", call.Function.Name, req.Tool)
	}

	return req, true, nil
}

// --- /var/folders/h5/0qmlhm750_5c0srn1523bvgw0000gn/T/tmp0lxi6ip3.go ---
// NormalizeRunnerPath cleans a workspace-relative path for consistent comparison.
func NormalizeRunnerPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "./")
	return path
}

// ContainsAny reports whether text contains any of the given needles.
func ContainsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

// ReadPathsFromToolResult extracts workspace-relative paths from a tool result's
// summary and output fields.
func ReadPathsFromToolResult(r workspace.ToolResult) []string {
	if CandidateAnchorOnlyResult(r) {
		return nil
	}

	tool := strings.TrimSpace(r.Tool)
	if (tool == workspace.ToolNameCodeNavigate || tool == workspace.ToolNameReadOnlyCapability) && !RoutedSourceReadOutputHasBody(r.Output) {
		return nil
	}

	seen := map[string]struct{}{}
	var paths []string

	for _, candidate := range []string{
		ExtractReadPathFromSummary(r.Summary),
		ExtractReadPathFromSummary(r.Output),
		ExtractRoutedReadPathFromOutput(r.Output),
	} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		normalized := NormalizeRunnerPath(candidate)
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		paths = append(paths, candidate)
	}

	return paths
}

// CandidateAnchorOnlyResult reports whether a tool result only produced a
// candidate anchor without reading source body content.
func CandidateAnchorOnlyResult(r workspace.ToolResult) bool {
	return CandidateAnchorOnlyText(r.Summary + "\n" + r.Output)
}

// ReadOnlySourceLikePaths filters paths to only include source-code-like paths
// with a limit on count.
func ReadOnlySourceLikePaths(paths []string, max int) []string {
	if max <= 0 {
		max = 8
	}

	var filtered []string

	for _, path := range paths {
		path = NormalizeRunnerPath(path)
		if path == "" || strings.HasPrefix(path, "../") || path == ".." || strings.HasPrefix(path, "/") {
			continue
		}

		if !looksLikeWorkspaceSourcePath(strings.ToLower(path)) {
			continue
		}

		filtered = append(filtered, path)
		if len(filtered) >= max {
			break
		}
	}

	return filtered
}

func looksLikeWorkspaceSourcePath(lower string) bool {
	// Skip non-source paths that commonly appear in tool output.
	if strings.HasPrefix(lower, ".idiom/") || strings.HasPrefix(lower, ".git/") {
		return false
	}

	if strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".json") || strings.HasSuffix(lower, ".jsonl") ||
		strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".toml") ||
		strings.HasSuffix(lower, ".lock") || strings.HasSuffix(lower, ".sum") || strings.HasSuffix(lower, ".mod") ||
		strings.HasSuffix(lower, ".txt") || strings.HasSuffix(lower, ".csv") || strings.HasSuffix(lower, ".env") ||
		strings.HasSuffix(lower, ".log") || strings.HasSuffix(lower, ".tmp") ||
		strings.HasSuffix(lower, "go.sum") || strings.HasSuffix(lower, "go.mod") {
		return true // config/doc files are valid source-like paths in workspace context
	}

	// Skip paths that look like URLs or binary names.
	if strings.Contains(lower, "://") || !strings.Contains(lower, "/") {
		return false
	}

	return true
}

// ---------------------------------------------------------------------------
// Internal helpers extracted from runner_plan_then_execute_guard.go
// ---------------------------------------------------------------------------

// RoutedSourceReadOutputHasBody reports whether a routed tool result's output
// contains source body content (not just headers, anchors, or route summaries).
func RoutedSourceReadOutputHasBody(output string) bool {
	output = strings.TrimSpace(output)
	if output == "" {
		return false
	}

	if CandidateAnchorOnlyText(output) {
		return false
	}

	if !strings.Contains(output, "route_summary:") {
		return true
	}

	seenRouteSummary := false

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "route_summary:") {
			seenRouteSummary = true
			continue
		}

		if !seenRouteSummary || line == "" {
			continue
		}

		if routedSourceReadOutputHeaderLine(line) {
			continue
		}

		return true
	}

	return false
}

func routedSourceReadOutputHeaderLine(line string) bool {
	return strings.Contains(line, "capability:") ||
		strings.Contains(line, "language:") ||
		strings.Contains(line, "routed_tool:")
}

// ExtractReadPathFromSummary parses a canonical source-body read summary
// and returns the path.
func ExtractReadPathFromSummary(summary string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ""
	}

	lower := strings.ToLower(summary)
	fields := strings.Fields(summary)
	if len(fields) < 2 {
		return ""
	}

	// "Read <symbol> in <path> ..." — language-symbol shape.
	// "Extracted <symbol> from <path> lines A-B" — extract_body shape.
	if len(fields) >= 4 && (strings.EqualFold(fields[0], "Read") || strings.EqualFold(fields[0], "Extracted")) {
		// "Read <symbol> in <path>" or "Extracted <symbol> from <path>"
		preposition := fields[2]
		if preposition == "in" || preposition == "from" {
			return fields[3]
		}
	}

	if strings.EqualFold(fields[0], "Read") && len(fields) >= 3 {
		// "Read fXX path lines A-B (total lines: N)" — read_file shape.
		// fields[1] is a file handle ("f1"), fields[3] is "lines".
		if strings.HasPrefix(fields[1], "f") && len(fields) >= 4 && strings.EqualFold(fields[3], "lines") {
			return fields[2]
		}

		// "Read path lines A-B" — fields[1] is the path, fields[2] is "lines".
		if len(fields) >= 3 && strings.EqualFold(fields[2], "lines") {
			return fields[1]
		}

		// "Read <N> lines from <path>" — path is at index 4.
		if len(fields) >= 5 && strings.EqualFold(fields[2], "lines") && strings.EqualFold(fields[3], "from") {
			return fields[4]
		}

		// "Read <symbol> in <path>" or "Extracted <symbol> from <path>"
		if len(fields) >= 4 && strings.EqualFold(fields[2], "in") && strings.EqualFold(fields[0], "Read") {
			return fields[3]
		}

		// "Read file: path" or "Read path" — fields[2] is the path (original fallback).
		return fields[2]
	}

	// Pattern: "Redirected grep for X to read_file: <path>"
	if idx := strings.Index(lower, " to read_file: "); idx >= 0 {
		return strings.TrimSpace(summary[idx+len(" to read_file: "):])
	}

	// Pattern: "Redirected search to code_navigate: <path>"
	if idx := strings.Index(lower, " to code_navigate: "); idx >= 0 {
		return strings.TrimSpace(summary[idx+len(" to code_navigate: "):])
	}

	// Pattern: "Redirected search to read_only_capability: <path>"
	if idx := strings.Index(lower, " to read_only_capability: "); idx >= 0 {
		return strings.TrimSpace(summary[idx+len(" to read_only_capability: "):])
	}

	// Pattern: "<path> — <summary>" (used by file_outline)
	parts := strings.SplitN(summary, " — ", 2)
	if len(parts) == 2 && hasSourceSuffix(parts[0]) {
		return strings.TrimSpace(parts[0])
	}

	return ""
}

func hasSourceSuffix(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".go") || strings.HasSuffix(lower, ".rs") ||
		strings.HasSuffix(lower, ".ts") || strings.HasSuffix(lower, ".py") ||
		strings.HasSuffix(lower, ".java") || strings.HasSuffix(lower, ".c") ||
		strings.HasSuffix(lower, ".h") || strings.HasSuffix(lower, ".cpp") ||
		strings.HasSuffix(lower, ".hpp")
}

// ---------------------------------------------------------------------------
// Objective text helpers
// ---------------------------------------------------------------------------

// ReadOnlyObjectiveSupportTerm normalizes a term for comparison (lowercase,
// singular).
func ReadOnlyObjectiveSupportTerm(term string) string {
	term = strings.TrimSpace(strings.ToLower(term))
	if len(term) > 3 && strings.HasSuffix(term, "s") {
		term = strings.TrimSuffix(term, "s")
	}
	return term
}

// SingularObjectiveWord trims a trailing 's' from a word to produce a
// singular form, unless the word ends with "ss".
func SingularObjectiveWord(word string) string {
	word = strings.TrimSpace(strings.ToLower(word))
	if len(word) > 4 && strings.HasSuffix(word, "s") && !strings.HasSuffix(word, "ss") {
		return strings.TrimSuffix(word, "s")
	}
	return word
}

// RecoverySearchStopWords returns the set of stop words used when extracting
// search terms from an objective.
func RecoverySearchStopWords() map[string]struct{} {
	return map[string]struct{}{
		"actual": {}, "please": {}, "replace": {}, "with": {}, "from": {}, "that": {}, "this": {}, "the": {}, "and": {}, "for": {}, "you": {}, "can": {}, "top": {}, "right": {}, "left": {}, "path": {}, "alias": {},
		"continuing": {}, "continue": {}, "completion": {}, "complete": {}, "work": {}, "docs": {}, "document": {}, "design": {}, "implement": {}, "implementation": {}, "bounded": {}, "topic": {}, "only": {}, "must": {},
		"against": {}, "existing": {}, "focus": {}, "focused": {}, "add": {}, "regression": {}, "tests": {}, "test": {}, "proving": {}, "without": {}, "partial": {}, "durable": {}, "mutation": {},
		"repo": {}, "repository": {}, "discovery": {}, "first": {}, "make": {}, "minimal": {}, "code": {}, "changes": {}, "change": {}, "requested": {}, "request": {},
		"tdd": {}, "prd": {}, "adr": {}, "brief": {}, "research": {}, "workflow": {}, "workflows": {}, "markdown": {},
		// "model" intentionally kept — it carries domain signal for path→objective matching.
		"send": {}, "through": {}, "plan": {}, "execute": {}, "expose": {}, "read": {}, "external": {}, "agents": {},
	}
}

// RecoveryObjectiveSearchWords extracts content-bearing search terms from an
// objective string, filtered by stop words and minimum length.
func RecoveryObjectiveSearchWords(objective string, max int) []string {
	if max <= 0 {
		max = 8
	}

	lower := strings.ToLower(strings.TrimSpace(objective))
	if lower == "" {
		return nil
	}

	replacer := strings.NewReplacer("'", " ", "\"", " ", "`", " ", "’", " ", "-", " ", "_", " ", "/", " ")

	lower = replacer.Replace(lower)

	words := strings.FieldsFunc(lower, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	stop := RecoverySearchStopWords()
	terms := make([]string, 0, max)
	seen := make(map[string]struct{})

	for _, word := range words {
		word = strings.TrimSpace(word)
		if len(word) < 4 {
			continue
		}

		if _, skip := stop[word]; skip {
			continue
		}

		if _, ok := seen[word]; ok {
			continue
		}

		seen[word] = struct{}{}
		terms = append(terms, word)

		if len(terms) >= max {
			break
		}
	}

	return terms
}

// ExtractRoutedReadPathFromOutput extracts the path from a route_summary line.
func ExtractRoutedReadPathFromOutput(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "route_summary:") {
			return ExtractReadPathFromSummary(strings.TrimSpace(strings.TrimPrefix(line, "route_summary:")))
		}
	}
	return ""
}

// CandidateAnchorOnlyText reports whether the given text contains only anchor
// metadata with no source body content.
func CandidateAnchorOnlyText(text string) bool {
	lower := strings.ToLower(text)

	return strings.Contains(lower, "path-filter miss") &&
		strings.Contains(lower, "candidate_anchor_status: anchors only")
}

// ObjectiveAsksImplementationShape reports whether the objective asks about
// implementation shape ("what would it take", "what needs to change", etc.)
func ObjectiveAsksImplementationShape(objective string) bool {
	lower := " " + strings.ToLower(strings.Join(strings.Fields(objective), " ")) + " "

	return ContainsAny(lower,
		" what would it take ",
		" what needs to change ",
		" what would need to change ",
		" how would ",
		" how should ",
	)
}

// ReadStyleSourceBodyTool reports whether the given tool name reads source
// body content (read_file, prefetch).
func ReadStyleSourceBodyTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case workspace.ToolNameReadFile, workspace.ToolNamePrefetch:
		return true
	default:
		return false
	}
}

// IsDiagnosticLogArtifactPath reports whether the path points to a known
// diagnostic log artifact that should be excluded from source evidence.
func IsDiagnosticLogArtifactPath(path string) bool {
	lower := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(path), "\\", "/"))
	lower = strings.TrimPrefix(lower, "./")

	switch {
	case lower == "":
		return false
	case lower == "logs" || strings.HasPrefix(lower, "logs/"):
		return true
	case lower == ".idiom/logs" || strings.HasPrefix(lower, ".idiom/logs/"):
		return true
	case lower == ".idiom/comprehension/traces" || strings.HasPrefix(lower, ".idiom/comprehension/traces/"):
		return true
	case lower == "traces" || strings.HasPrefix(lower, "traces/"):
		return true
	case strings.Contains(lower, "/.idiom/logs/") || strings.Contains(lower, "/.idiom/comprehension/traces/"):
		return true
	default:
		return false
	}
}

// IsNonSourceEvidenceArtifactPath reports whether the path points to a
// non-source artifact that should be excluded from source evidence.
func IsNonSourceEvidenceArtifactPath(path string) bool {
	lower := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(path), "\\", "/"))
	lower = strings.TrimPrefix(lower, "./")

	switch {
	case lower == "":
		return false
	case IsDiagnosticLogArtifactPath(lower):
		return true
	case lower == "transcripts" || strings.HasPrefix(lower, "transcripts/"):
		return true
	case lower == ".idiom" || strings.HasPrefix(lower, ".idiom/"):
		return true
	case lower == "idiom" || strings.HasPrefix(lower, "idiom/"):
		return true
	case lower == ".codex/discovery" || strings.HasPrefix(lower, ".codex/discovery/"):
		return true
	case lower == "testdata/swe/generated-logs" || strings.HasPrefix(lower, "testdata/swe/generated-logs/"):
		return true
	default:
		return false
	}
}

var agentutil = struct {
	NormalizeRunnerPath              func(string) string
	ContainsAny                      func(string, ...string) bool
	ReadPathsFromToolResult          func(workspace.ToolResult) []string
	CandidateAnchorOnlyResult        func(workspace.ToolResult) bool
	ReadOnlySourceLikePaths          func([]string, int) []string
	RoutedSourceReadOutputHasBody    func(string) bool
	ExtractReadPathFromSummary       func(string) string
	ReadOnlyObjectiveSupportTerm     func(string) string
	SingularObjectiveWord            func(string) string
	RecoverySearchStopWords          func() map[string]struct{}
	RecoveryObjectiveSearchWords     func(string, int) []string
	ExtractRoutedReadPathFromOutput  func(string) string
	CandidateAnchorOnlyText          func(string) bool
	ObjectiveAsksImplementationShape func(string) bool
	ReadStyleSourceBodyTool          func(string) bool
	IsDiagnosticLogArtifactPath      func(string) bool
	IsNonSourceEvidenceArtifactPath  func(string) bool
}{
	NormalizeRunnerPath:              NormalizeRunnerPath,
	ContainsAny:                      ContainsAny,
	ReadPathsFromToolResult:          ReadPathsFromToolResult,
	CandidateAnchorOnlyResult:        CandidateAnchorOnlyResult,
	ReadOnlySourceLikePaths:          ReadOnlySourceLikePaths,
	RoutedSourceReadOutputHasBody:    RoutedSourceReadOutputHasBody,
	ExtractReadPathFromSummary:       ExtractReadPathFromSummary,
	ReadOnlyObjectiveSupportTerm:     ReadOnlyObjectiveSupportTerm,
	SingularObjectiveWord:            SingularObjectiveWord,
	RecoverySearchStopWords:          RecoverySearchStopWords,
	RecoveryObjectiveSearchWords:     RecoveryObjectiveSearchWords,
	ExtractRoutedReadPathFromOutput:  ExtractRoutedReadPathFromOutput,
	CandidateAnchorOnlyText:          CandidateAnchorOnlyText,
	ObjectiveAsksImplementationShape: ObjectiveAsksImplementationShape,
	ReadStyleSourceBodyTool:          ReadStyleSourceBodyTool,
	IsDiagnosticLogArtifactPath:      IsDiagnosticLogArtifactPath,
	IsNonSourceEvidenceArtifactPath:  IsNonSourceEvidenceArtifactPath,
}

// --- /var/folders/h5/0qmlhm750_5c0srn1523bvgw0000gn/T/tmp_ojc8zuu.go ---
type Intent struct {
	ToolName    string
	ActionClass string
	Capability  string
	Path        string
	Confidence  string
	Transport   string
	ParserState string
}
