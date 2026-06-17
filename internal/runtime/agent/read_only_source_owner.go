package agent

import (
	"idiom/internal/contracts/pathparse"
	"idiom/internal/runtime/agent/agentutil"
	"idiom/internal/platform/util"
)

import "strings"

type readOnlySourceOwnerRole string

const (
	readOnlySourceOwnerUnknown               readOnlySourceOwnerRole = "unknown"
	readOnlySourceOwnerMechanism             readOnlySourceOwnerRole = "mechanism_owner"
	readOnlySourceOwnerStateHolder           readOnlySourceOwnerRole = "state_holder"
	readOnlySourceOwnerDisplaySupport        readOnlySourceOwnerRole = "display_support"
	readOnlySourceOwnerTestSupport           readOnlySourceOwnerRole = "test_support"
	readOnlySourceOwnerConflictingNavigation readOnlySourceOwnerRole = "conflicting_navigation_owner"
)

type readOnlySourceOwnerEvidence struct {
	Path          string
	Symbol        string
	Role          readOnlySourceOwnerRole
	Mechanism     bool
	SpecificTerms []string
	WeakTerms     []string
	SupportTerms  []string
	ConflictTerms []string
	Score         int
}

func (e readOnlySourceOwnerEvidence) OwnerEligible() bool {
	return e.Role == readOnlySourceOwnerMechanism
}

func readOnlySourceOwnerEvidenceForBody(path string, symbol string, objective string, body string) readOnlySourceOwnerEvidence {
	evidence := readOnlySourceOwnerEvidence{
		Path:   agentutil.NormalizeRunnerPath(path),
		Symbol: strings.TrimSpace(symbol),
		Role:   readOnlySourceOwnerUnknown,
	}
	if !agentutil.ObjectiveAsksImplementationShape(objective) {
		return evidence
	}

	if implementationShapeTestOwnerDisallowed(evidence.Path, objective) {
		evidence.Role = readOnlySourceOwnerTestSupport
		return evidence
	}

	rawText := body
	text := strings.ToLower(body)

	if strings.TrimSpace(text) == "" {
		return evidence
	}

	evidence.Mechanism = implementationShapeMechanismEvidence(text)
	evidence.SpecificTerms = sourceOwnerMatchedTerms(text, sourceOwnerSpecificObjectiveTerms(agentutil.RecoveryObjectiveSearchWords(objective, 32)))
	evidence.WeakTerms = sourceOwnerMatchedWeakTerms(text, agentutil.RecoveryObjectiveSearchWords(objective, 32))
	evidence.SupportTerms = sourceOwnerSupportTerms(text)
	evidence.ConflictTerms = sourceOwnerConflictTerms(rawText, evidence.Path, evidence.Symbol, objective)
	evidence.Score = sourceOwnerEvidenceScore(evidence)

	if sourceOwnerLooksLikeDisplaySupport(text) && !sourceOwnerHasDirectMechanismSignal(text) {
		evidence.Role = readOnlySourceOwnerDisplaySupport
		return evidence
	}

	if sourceOwnerLooksLikeStateHolder(text) && !sourceOwnerHasDirectMechanismSignal(text) {
		evidence.Role = readOnlySourceOwnerStateHolder
		return evidence
	}

	if evidence.Mechanism {
		if len(evidence.ConflictTerms) >= 2 && len(evidence.SpecificTerms) == 0 {
			evidence.Role = readOnlySourceOwnerConflictingNavigation
			return evidence
		}

		if len(evidence.SpecificTerms) > 0 || len(evidence.WeakTerms) >= 2 {
			evidence.Role = readOnlySourceOwnerMechanism
			return evidence
		}
	}

	if sourceOwnerLooksLikeDisplaySupport(text) {
		evidence.Role = readOnlySourceOwnerDisplaySupport
		return evidence
	}

	if sourceOwnerLooksLikeStateHolder(text) {
		evidence.Role = readOnlySourceOwnerStateHolder
		return evidence
	}

	return evidence
}

func sourceOwnerHasDirectMechanismSignal(text string) bool {
	return agentutil.ContainsAny(text,
		"case tea.",
		"case \"",
		" handles ",
		" handle",
		" calls ",
		" append",
		" switch ",
		" route",
		" dispatch",
		" invoke",
	)
}

func implementationShapeMechanismEvidence(text string) bool {
	return sourceOwnerHasDirectMechanismSignal(text) || strings.Contains(text, " func ")
}

func sourceOwnerMatchedTerms(text string, terms []string) []string {
	var matches []string

	for _, term := range terms {
		term = agentutil.ReadOnlyObjectiveSupportTerm(term)
		if len(term) < 3 {
			continue
		}

		if strings.Contains(text, term) {
			matches = append(matches, term)
		}
	}

	return util.Sorted(matches)
}

func sourceOwnerMatchedWeakTerms(text string, terms []string) []string {
	var matches []string

	for _, term := range terms {
		term = agentutil.ReadOnlyObjectiveSupportTerm(term)
		if len(term) < 3 || !weakSourceOwnerObjectiveTerm(term) {
			continue
		}

		if strings.Contains(text, term) {
			matches = append(matches, term)
		}
	}

	return util.Sorted(matches)
}

func sourceOwnerSupportTerms(text string) []string {
	var matches []string

	for _, term := range []string{
		"render", "view", "viewport", "content", "state", "struct", "field", "type",
	} {
		if strings.Contains(text, term) {
			matches = append(matches, term)
		}
	}

	return util.Sorted(matches)
}

func sourceOwnerConflictTerms(text string, path string, symbol string, objective string) []string {
	objectiveTerms := sourceOwnerObjectiveTermSet(objective)

	var matches []string

	for _, term := range sourceOwnerIdentityTerms(path, symbol, text) {
		if _, ok := objectiveTerms[term]; ok {
			continue
		}

		matches = append(matches, term)
	}

	return util.Sorted(matches)
}

func sourceOwnerLooksLikeDisplaySupport(text string) bool {
	return agentutil.ContainsAny(text, "render", "viewport", "view()", "setcontent", "content")
}

func sourceOwnerLooksLikeStateHolder(text string) bool {
	return agentutil.ContainsAny(text, "type ", " struct ", "state", " field")
}

func implementationShapeTestOwnerDisallowed(path string, objective string) bool {
	path = strings.ToLower(strings.TrimSpace(agentutil.NormalizeRunnerPath(path)))
	if path == "" || !strings.HasSuffix(path, "_test.go") {
		return false
	}

	return !objectiveAllowsTestImplementationOwner(objective)
}

func objectiveAllowsTestImplementationOwner(objective string) bool {
	lower := " " + strings.ToLower(strings.Join(strings.Fields(objective), " ")) + " "

	return agentutil.ContainsAny(lower,
		" test ",
		" tests ",
		" testing ",
		" coverage ",
		" regression ",
		" _test.go ",
		" test file ",
		" test files ",
	)
}

func sourceOwnerEvidenceScore(evidence readOnlySourceOwnerEvidence) int {
	score := 0
	if evidence.Mechanism {
		score += 10
	}

	score += len(evidence.SpecificTerms) * 5
	score += len(evidence.WeakTerms) * 2
	score += len(evidence.SupportTerms)

	score -= len(evidence.ConflictTerms) * 8

	return score
}

func sourceOwnerObjectiveTermSet(objective string) map[string]struct{} {
	set := map[string]struct{}{}

	for _, term := range agentutil.RecoveryObjectiveSearchWords(objective, 64) {
		term = agentutil.ReadOnlyObjectiveSupportTerm(term)
		if len(term) < 3 {
			continue
		}

		set[term] = struct{}{}
	}

	return set
}

func sourceOwnerIdentityTerms(path string, symbol string, text string) []string {
	var terms []string

	for _, term := range implementationPathTerms(path) {
		if sourceOwnerGenericIdentityTerm(term) {
			continue
		}

		terms = append(terms, term)
	}

	for _, raw := range sourceOwnerIdentityFragments(symbol + "\n" + sourceOwnerBodyIdentityText(text)) {
		for _, term := range splitImplementationPathToken(raw) {
			term = agentutil.ReadOnlyObjectiveSupportTerm(term)
			if len(term) < 4 || sourceOwnerGenericIdentityTerm(term) {
				continue
			}

			terms = append(terms, term)
		}
	}

	return util.Sorted(terms)
}

func sourceOwnerBodyIdentityText(text string) string {
	var lines []string

	for _, line := range strings.Split(text, "\n") {
		lower := strings.ToLower(strings.TrimSpace(line))
		if strings.Contains(lower, "route_summary:") ||
			strings.Contains(lower, "func ") ||
			strings.Contains(lower, " handles ") ||
			strings.Contains(lower, " handle") ||
			strings.Contains(lower, " has focus") ||
			strings.Contains(lower, "returns ") {

			lines = append(lines, line)
		}

		if len(lines) >= 8 {
			break
		}
	}

	return strings.Join(lines, "\n")
}

func sourceOwnerIdentityFragments(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9')
	})
}

func sourceOwnerGenericIdentityTerm(term string) bool {
	switch agentutil.ReadOnlyObjectiveSupportTerm(term) {
	case "internal", "cmd", "pkg", "app", "apps", "test", "tests", "model", "func", "return", "returns",
		"handle", "handler", "handlers", "update", "press", "key", "keys", "message",
		"state", "true", "false", "bool", "line", "lines", "read", "body", "source", "summary",
		"route", "routed", "tool", "code", "navigate", "msg":
		return true
	default:
		return false
	}
}

func stripSourceEvidenceHandleLinePrefix(line string) (string, bool) {
	space := strings.IndexByte(line, ' ')
	if space <= 1 || line[0] != 'f' {
		return "", false
	}

	for _, ch := range line[1:space] {
		if ch < '0' || ch > '9' {
			return "", false
		}
	}

	rest := strings.TrimSpace(line[space+1:])
	pathEnd := strings.IndexByte(rest, ':')

	if pathEnd <= 0 {
		return "", false
	}

	path := strings.TrimSpace(rest[:pathEnd])
	if path == "" || (!strings.Contains(path, "/") && !strings.Contains(path, ".")) {
		return "", false
	}

	afterPath := rest[pathEnd+1:]
	lineEnd := 0

	for lineEnd < len(afterPath) && afterPath[lineEnd] >= '0' && afterPath[lineEnd] <= '9' {
		lineEnd++
	}

	if lineEnd == 0 || lineEnd >= len(afterPath) || afterPath[lineEnd] != ':' {
		return "", false
	}

	return strings.TrimSpace(afterPath[lineEnd+1:]), true
}


func stripSourceEvidenceLinePrefix(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}

	if stripped, ok := stripSourceEvidenceHandleLinePrefix(line); ok {
		return stripped
	}

	idx := 0
	for idx < len(line) && line[idx] >= '0' && line[idx] <= '9' {
		idx++
	}

	if idx == 0 || idx >= len(line) || line[idx] != ':' {
		return line
	}

	return strings.TrimSpace(line[idx+1:])
}


func sourceBodyEvidencePathsFromDelegatedPacket(output string) []string {
	var paths []string

	inSection := false

	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			if inSection {
				break
			}

			continue
		}

		lower := strings.ToLower(strings.TrimRight(line, ":"))
		switch lower {
		case "source-body evidence", "source-body evidence paths", "source body evidence", "source body evidence paths":
			inSection = true
			continue
		}

		if !inSection {
			continue
		}

		if strings.HasSuffix(line, ":") && !strings.HasPrefix(line, "- ") {
			break
		}

		for _, path := range pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(line)) {
			if agentutil.IsNonSourceEvidenceArtifactPath(path) {
				continue
			}

			paths = append(paths, path)
		}
	}

	return uniqueDelegatedDiscoveryPaths(paths)
}


func looksLikeDelegatedGroundedFallbackAnswer(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" {
		return false
	}

	if genericEvidenceAnswerIncludesLineOrientedSourceBody(trimmed) || looksLikeLineOrientedSourceSnippetAnswer(trimmed) {
		return false
	}

	lower := strings.ToLower(trimmed)

	return strings.HasPrefix(lower, "the answer is grounded in ") &&
		strings.Contains(lower, "\n\nwhat this establishes:\n- ")
}

