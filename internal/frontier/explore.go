package frontier

import (
	"strings"

	"qwen-bubbles/internal/workspace"
)

func IssueSearchPattern(prompt string) string {
	normalized := strings.ToLower(prompt)
	switch {
	case ContainsAny(normalized, "performance", "optimize", "optimization"):
		return "TODO|FIXME|panic\\(|slow|performance|alloc"
	case ContainsAny(normalized, "cleanup", "clean up", "code smell", "refactor"):
		return "TODO|FIXME|HACK|deprecated"
	case ContainsAny(normalized, "error", "bug", "issue", "problem", "regression"):
		return "TODO|FIXME|BUG|panic\\(|error|regression"
	default:
		return "TODO|FIXME|BUG|XXX|panic\\("
	}
}

func ExploreBootstrapReadBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 2
	case workspace.ThoroughnessThorough:
		return 4
	default:
		return 3
	}
}

func ExploreKeywordSearchPathBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 1
	case workspace.ThoroughnessThorough:
		return 3
	default:
		return 2
	}
}

func ExploreKeywordReadBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessThorough:
		return 2
	default:
		return 1
	}
}

func ExploreIssueSearchPathBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 1
	case workspace.ThoroughnessThorough:
		return 4
	default:
		return 2
	}
}

func ExploreIssueReadBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessThorough:
		return 2
	default:
		return 1
	}
}

func ExploreSemanticDefaultFileBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 1
	case workspace.ThoroughnessThorough:
		return 3
	default:
		return 2
	}
}

func ExploreSemanticRelevantFileBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 1
	case workspace.ThoroughnessThorough:
		return 4
	default:
		return 2
	}
}

func ExploreSemanticIssueFileBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 1
	case workspace.ThoroughnessThorough:
		return 4
	default:
		return 2
	}
}

func ExploreSemanticTotalReadBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 2
	case workspace.ThoroughnessThorough:
		return 5
	default:
		return 3
	}
}

func PrioritizedExploreSearchPaths(dirs map[string]struct{}, level workspace.ExploreThoroughness) []string {
	candidates := []string{"internal", "cmd", "pkg", "src", "scripts", "prompts", "tests", "test"}
	paths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if hasDirectory(dirs, candidate) {
			paths = append(paths, candidate)
		}
	}
	return LimitStrings(paths, ExploreSearchPathLimit(level))
}

func ExploreSearchGlob(path string) string {
	switch path {
	case "internal", "cmd", "pkg", "src", "test", "tests":
		return "*.go"
	case "prompts":
		return "*.md"
	default:
		return ""
	}
}

func ExploreSearchPathLimit(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 2
	case workspace.ThoroughnessThorough:
		return 6
	default:
		return 4
	}
}

func ArchitectureHotspotBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 3
	case workspace.ThoroughnessThorough:
		return 6
	default:
		return 4
	}
}

func ArchitectureReadBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 3
	case workspace.ThoroughnessThorough:
		return 6
	default:
		return 4
	}
}

func ArchitectureRelevantFileBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 2
	case workspace.ThoroughnessThorough:
		return 5
	default:
		return 3
	}
}

func ArchitectureSearchPathBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 2
	case workspace.ThoroughnessThorough:
		return 5
	default:
		return 3
	}
}

func ArchitectureSearchReadBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 1
	case workspace.ThoroughnessThorough:
		return 3
	default:
		return 2
	}
}

func ArchitectureSearchPattern(prompt string) string {
	normalized := strings.ToLower(prompt)
	switch {
	case ContainsAny(normalized, "language", "languages", "golang", "go", "extensible", "support", "supported"):
		return "GoIndex|semantic|clangd|workspace|language|languages|cpp|C\\+\\+"
	case ContainsAny(normalized, "structure", "architecture", "architectural", "architecturally", "maintainability", "optimize", "optimization", "modular", "modularity", "boundary", "boundaries", "split", "decompose", "organize", "organization"):
		return "router|lane|workspace|manager|analysis|review|explore|implement|semantic"
	default:
		return ""
	}
}

func ArchitectureSourceDirPriority(dir string) int {
	switch dir {
	case "internal":
		return 35
	case "cmd":
		return 26
	case "pkg":
		return 24
	case "src":
		return 20
	case ".":
		return 10
	default:
		return 14
	}
}

func ExploreQueryTerms(prompt string) []string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		case r >= '0' && r <= '9':
			return r
		default:
			return ' '
		}
	}, prompt)

	stopwords := map[string]struct{}{
		"a": {}, "an": {}, "and": {}, "any": {}, "before": {}, "brief": {}, "can": {}, "cleanup": {},
		"code": {}, "codebase": {}, "deep": {}, "diagnose": {}, "explore": {}, "fast": {}, "file": {},
		"files": {}, "find": {}, "fix": {}, "for": {}, "from": {}, "harness": {}, "identify": {},
		"implement": {}, "inspect": {}, "investigate": {}, "issue": {}, "look": {}, "make": {},
		"optimization": {}, "optimize": {}, "performance": {}, "please": {}, "problem": {}, "project": {},
		"quick": {}, "recommend": {}, "recommendation": {}, "recommendations": {}, "repo": {}, "repository": {},
		"review": {}, "scan": {}, "search": {}, "single": {}, "some": {}, "part": {}, "parts": {},
		"test": {}, "the": {}, "this": {}, "that": {}, "through": {}, "thorough": {}, "topic": {}, "what": {}, "wrong": {}, "you": {},
		"bug": {}, "error": {}, "cleanups": {}, "smell": {}, "should": {}, "fixed": {}, "long": {}, "term": {}, "maintainability": {},
	}

	terms := make([]string, 0, 4)
	seen := make(map[string]struct{})
	for _, field := range strings.Fields(cleaned) {
		if len(field) < 3 {
			continue
		}
		if _, skip := stopwords[field]; skip {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		terms = append(terms, field)
		if len(terms) >= 4 {
			break
		}
	}
	return terms
}

func ArchitecturePromptWantsFocusedTargetPlan(prompt string) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	if normalized == "" {
		return false
	}
	return LooksLikePlanOnlyPrompt(normalized) ||
		ContainsAny(normalized, "drill down", "deeper", "go deeper", "recommendation", "this one", "this file", "focused plan", "targeted plan")
}

func ArchitecturePromptWantsBoundedOneFilePlan(prompt string) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	return LooksLikePlanOnlyPrompt(normalized) &&
		ContainsAny(normalized, "one existing", "one-file", "single file", "editing only one", "only one existing")
}

func AnalysisBudgetNextActions(stopReason string) []string {
	stopReason = strings.ToLower(strings.TrimSpace(stopReason))
	switch {
	case strings.Contains(stopReason, "ready"):
		return []string{"answer"}
	case strings.Contains(stopReason, "fallback"), strings.Contains(stopReason, "exhausted"):
		return []string{"answer_with_explicit_uncertainty", "persist_task_memory_if_needed"}
	default:
		return []string{"retry_with_more_evidence", "fallback_if_budget_exhausted"}
	}
}

func ReviewSearchPathBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 2
	case workspace.ThoroughnessThorough:
		return 6
	default:
		return 4
	}
}

func ReviewCandidateBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 2
	case workspace.ThoroughnessThorough:
		return 5
	default:
		return 3
	}
}

func hasDirectory(dirs map[string]struct{}, candidate string) bool {
	if _, ok := dirs[candidate]; ok {
		return true
	}
	for dir := range dirs {
		if dir == candidate || strings.HasPrefix(dir, candidate+"/") {
			return true
		}
	}
	return false
}
