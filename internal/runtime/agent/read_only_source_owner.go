package agent

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
		Path:   normalizeRunnerPath(path),
		Symbol: strings.TrimSpace(symbol),
		Role:   readOnlySourceOwnerUnknown,
	}
	if !objectiveAsksImplementationShape(objective) {
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
	evidence.SpecificTerms = sourceOwnerMatchedTerms(text, sourceOwnerSpecificObjectiveTerms(recoveryObjectiveSearchWords(objective, 32)))
	evidence.WeakTerms = sourceOwnerMatchedWeakTerms(text, recoveryObjectiveSearchWords(objective, 32))
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
	return containsAny(text,
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
		term = readOnlyObjectiveSupportTerm(term)
		if len(term) < 3 {
			continue
		}
		if strings.Contains(text, term) {
			matches = append(matches, term)
		}
	}
	return uniqueSortedStrings(matches)
}

func sourceOwnerMatchedWeakTerms(text string, terms []string) []string {
	var matches []string
	for _, term := range terms {
		term = readOnlyObjectiveSupportTerm(term)
		if len(term) < 3 || !weakSourceOwnerObjectiveTerm(term) {
			continue
		}
		if strings.Contains(text, term) {
			matches = append(matches, term)
		}
	}
	return uniqueSortedStrings(matches)
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
	return uniqueSortedStrings(matches)
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
	return uniqueSortedStrings(matches)
}

func sourceOwnerLooksLikeDisplaySupport(text string) bool {
	return containsAny(text, "render", "viewport", "view()", "setcontent", "content")
}

func sourceOwnerLooksLikeStateHolder(text string) bool {
	return containsAny(text, "type ", " struct ", "state", " field")
}

func implementationShapeTestOwnerDisallowed(path string, objective string) bool {
	path = strings.ToLower(strings.TrimSpace(normalizeRunnerPath(path)))
	if path == "" || !strings.HasSuffix(path, "_test.go") {
		return false
	}
	return !objectiveAllowsTestImplementationOwner(objective)
}

func objectiveAllowsTestImplementationOwner(objective string) bool {
	lower := " " + strings.ToLower(strings.Join(strings.Fields(objective), " ")) + " "
	return containsAny(lower,
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
	for _, term := range recoveryObjectiveSearchWords(objective, 64) {
		term = readOnlyObjectiveSupportTerm(term)
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
			term = readOnlyObjectiveSupportTerm(term)
			if len(term) < 4 || sourceOwnerGenericIdentityTerm(term) {
				continue
			}
			terms = append(terms, term)
		}
	}
	return uniqueSortedStrings(terms)
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
	switch readOnlyObjectiveSupportTerm(term) {
	case "internal", "cmd", "pkg", "app", "apps", "test", "tests", "model", "func", "return", "returns",
		"handle", "handler", "handlers", "update", "press", "key", "keys", "message",
		"state", "true", "false", "bool", "line", "lines", "read", "body", "source", "summary",
		"route", "routed", "tool", "code", "navigate", "msg":
		return true
	default:
		return false
	}
}
