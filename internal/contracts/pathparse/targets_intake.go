package pathparse

import (
	"regexp"
	"strings"
)

var (
	deliverableVerbRE = regexp.MustCompile(`(?i)\b(?:produce|create|write|generate|output\s+to|add)\b\s+` + "`" + `([^` + "`" + `]+)` + "`")
	headingParenRE    = regexp.MustCompile(`\s*\([^)]*\)\s*`)
	backtickPathRE    = regexp.MustCompile("`([^`]+)`")
)

// ExtractDeliverableTargets returns the harness-owned path allowlist
// implied by a spec. Explicit file-scope sections win over prose
// extraction so planner-owned edit contracts stay intact.
func ExtractDeliverableTargets(specBody, mode string) []string {
	if targets := FilterFilePaths(ExtractFilesToModify(specBody)); len(targets) > 0 {
		return targets
	}

	if targets := extractPlanFileScopeTargets(specBody); len(targets) > 0 {
		return targets
	}

	if targets := extractScopeFileTargets(specBody); len(targets) > 0 {
		return targets
	}

	var targets []string

	for _, match := range deliverableVerbRE.FindAllStringSubmatch(specBody, -1) {
		path := strings.TrimSpace(match[1])
		if !PathLooksLikeFile(path) {
			continue
		}

		if mode == ModeMarkdown && PathLooksLikeSource(path) {
			continue
		}

		targets = append(targets, path)
	}

	return NormalizeTargets(targets)
}

// ExtractFilesToModify pulls file paths out of a brief's "Files To
// Modify" table in document order, deduplicated.
func ExtractFilesToModify(text string) []string {
	var files []string

	seen := map[string]bool{}
	inSection := false

	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)

		if strings.HasPrefix(lower, "## files to modify") || strings.HasPrefix(lower, "## files you may edit") {
			inSection = true
			continue
		}

		if inSection && strings.HasPrefix(trimmed, "## ") {
			inSection = false
		}

		if !inSection {
			continue
		}

		candidate, change, ok := parseFilesToModifyRow(trimmed)
		if !ok || candidate == "" || seen[candidate] {
			continue
		}

		if changeLooksNoop(change) {
			continue
		}

		if strings.Contains(candidate, "/.idiom/refine/") || strings.HasPrefix(NormalizePath(candidate), ".idiom/refine/") {
			continue
		}

		seen[candidate] = true
		files = append(files, candidate)
	}

	return files
}

func parseFilesToModifyRow(row string) (path, change string, ok bool) {
	start := strings.Index(row, "`")
	if start < 0 {
		return "", "", false
	}

	end := strings.Index(row[start+1:], "`")
	if end < 0 {
		return "", "", false
	}

	path = strings.TrimSpace(row[start+1 : start+1+end])

	parts := strings.Split(row, "|")

	if len(parts) >= 3 {
		change = strings.TrimSpace(parts[2])
	}

	return path, change, path != ""
}

func changeLooksNoop(change string) bool {
	normalized := strings.ToLower(strings.TrimSpace(change))

	normalized = strings.Trim(normalized, "`*_ \t.")
	normalized = strings.Join(strings.Fields(normalized), " ")

	switch normalized {
	case "no change", "no changes", "none", "n/a", "na", "read only", "read-only", "reference only", "reference-only":
		return true
	}

	return strings.HasPrefix(normalized, "no change required") ||
		strings.HasPrefix(normalized, "no changes required") ||
		strings.HasPrefix(normalized, "do not modify") ||
		strings.HasPrefix(normalized, "do not edit") ||
		strings.HasPrefix(normalized, "do not touch")
}

var planFileScopeHeadings = []string{
	"expected file scope",
	"implementation file scope",
	"file scope",
	"files to modify",
	"files to edit",
	"write scope",
	"edit scope",
}

// PlanFileScopeHeadings returns the markdown headings that define explicit
// implementation file scope in plan documents.
func PlanFileScopeHeadings() []string {
	return append([]string(nil), planFileScopeHeadings...)
}

func extractPlanFileScopeTargets(specBody string) []string {
	return FilterFilePaths(MentionedWorkspacePathsUnderHeadings(specBody, PlanFileScopeHeadings()...))
}

func extractScopeFileTargets(specBody string) []string {
	var targets []string

	inScope := false
	sawScopeBullet := false

	for _, line := range strings.Split(specBody, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			level := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
			normalized := normalizeHeading(trimmed)

			if level == 2 && normalized == "scope" {
				inScope = true
				sawScopeBullet = false

				continue
			}

			if inScope && level <= 2 {
				break
			}
		}

		if !inScope {
			continue
		}

		if trimmed == "" {
			continue
		}

		if scopeContextLabel(trimmed) {
			break
		}

		if !isMarkdownBullet(trimmed) {
			if sawScopeBullet {
				break
			}

			continue
		}

		sawScopeBullet = true

		if path := firstFilePathMention(trimmed); path != "" {
			targets = append(targets, path)
		}
	}

	return NormalizeTargets(targets)
}

func normalizeHeading(line string) string {
	value := strings.TrimSpace(line)

	value = strings.TrimLeft(value, "#")
	value = strings.Trim(value, " *:`\"'")
	value = strings.TrimSuffix(value, ":")
	value = headingParenRE.ReplaceAllString(value, " ")
	value = strings.NewReplacer("-", " ", "_", " ", "/", " ").Replace(strings.ToLower(value))

	return strings.Join(strings.Fields(value), " ")
}

func scopeContextLabel(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))

	lower = strings.TrimSuffix(lower, ":")

	return strings.Contains(lower, "acceptance") && strings.Contains(lower, "context")
}

func isMarkdownBullet(line string) bool {
	return strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "+ ")
}

func firstFilePathMention(line string) string {
	for _, match := range backtickPathRE.FindAllStringSubmatch(line, -1) {
		candidate := strings.TrimSpace(match[1])
		if PathLooksLikeFile(candidate) {
			return candidate
		}
	}

	for _, field := range strings.Fields(line) {
		candidate := strings.Trim(field, "`'\".,;:()[]{}")
		if PathLooksLikeFile(candidate) {
			return candidate
		}
	}

	return ""
}

func FirstDeliverableTarget(targets []string) string {
	targets = NormalizeTargets(targets)
	if len(targets) == 0 {
		return ""
	}

	return targets[0]
}

func NormalizeTargets(targets []string) []string {
	var out []string

	seen := map[string]bool{}

	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}

		key := NormalizePath(target)
		if seen[key] {
			continue
		}

		seen[key] = true

		out = append(out, target)
	}

	return out
}

func NormalizePath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.ReplaceAll(path, "\\", "/")
	path = strings.TrimPrefix(path, "./")

	return path
}

func FilterFilePaths(candidates []string) []string {
	var targets []string

	for _, candidate := range candidates {
		if PathLooksLikeFile(candidate) {
			targets = append(targets, candidate)
		}
	}

	return NormalizeTargets(targets)
}

func PathsEquivalent(a, b string) bool {
	return NormalizePath(a) == NormalizePath(b)
}
