package frontier

import (
	"sort"
	"strconv"
	"strings"

	"idiom/internal/frontier/pathparse"
	"idiom/internal/util"
)

type LocateCandidate struct {
	Path    string
	Line    int
	Match   string
	Snippet string
	Region  string
	Score   int
}

func ContainsAny(text string, needles ...string) bool {
	return util.ContainsAny(text, needles...)
}

func LimitStrings(values []string, limit int) []string {
	return util.LimitSlice(values, limit)
}

func UniqueNonEmptyStrings(values []string) []string {
	return util.UniqueNonEmpty(values)
}

func LocateSearchScopes(prompt string) []string {
	normalized := strings.ToLower(prompt)
	scopes := make([]string, 0, 5)
	seen := make(map[string]struct{})
	add := func(scope string) {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			return
		}
		if _, ok := seen[scope]; ok {
			return
		}
		seen[scope] = struct{}{}
		scopes = append(scopes, scope)
	}

	if ContainsAny(normalized, "bubbletea", "window", "header", "footer", "badge", "chat", "menu", "status", "banner") {
		add("internal/app")
	}
	for _, path := range pathparse.MentionedWorkspacePaths(prompt) {
		add(path)
	}
	add("internal")
	add(".")
	return scopes
}

func ParseLocateCandidates(output string) []LocateCandidate {
	candidates := make([]LocateCandidate, 0, 8)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "context:") || strings.HasPrefix(line, "targeted edit template:") {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 || !strings.HasPrefix(parts[0], "f") {
			continue
		}
		location := strings.SplitN(parts[1], ":", 3)
		if len(location) < 3 {
			continue
		}
		path := strings.TrimSpace(location[0])
		if LooksLikeLocateMetaPath(path) {
			continue
		}
		match := strings.TrimSpace(location[2])
		if LooksLikeLocateMetaMatch(match) {
			continue
		}
		lineNumber, err := strconv.Atoi(strings.TrimSpace(location[1]))
		if err != nil {
			continue
		}
		candidates = append(candidates, LocateCandidate{
			Path:  path,
			Line:  lineNumber,
			Match: match,
		})
	}
	return candidates
}

func LooksLikeLocateMetaPath(path string) bool {
	path = strings.ToLower(strings.TrimSpace(path))
	return strings.HasSuffix(path, "_test.go") ||
		strings.Contains(path, "/logs/") ||
		strings.Contains(path, "/transcripts/") ||
		strings.Contains(path, "/.git/")
}

func LooksLikeLocateMetaMatch(match string) bool {
	text := strings.ToLower(strings.TrimSpace(match))
	if ContainsAny(text, "workingframe", "func (m *model) workingdots", "strings.repeat") {
		return true
	}
	if strings.Contains(text, "workingstyle :=") && !strings.Contains(text, "qwen is working") {
		return true
	}
	return ContainsAny(
		text,
		"containsany(text",
		"strings.contains(text",
		"classifylocateregion",
		"scorelocatecandidate",
		"renderlocatesummary",
		"locatecandidate",
		"score +=",
		"separate similar match",
		"the top-of-window",
	)
}

func ClassifyLocateRegion(candidate LocateCandidate) string {
	text := strings.ToLower(candidate.Path + "\n" + candidate.Match + "\n" + candidate.Snippet)
	switch {
	case ContainsAny(text, "header +=", "header :=", "workingstyle.render", "titlestyle.render", "serverstate :="):
		return "header"
	case ContainsAny(text, "footer :=", "footer +=", "helpstyle.render", "context:", "workspace:"):
		return "footer"
	case ContainsAny(text, "case \"assistant\"", "assistantstyle.render", "msg.role", "label = assistantstyle.render(\"qwen\")", "content = m.spinner.view()"):
		return "chat"
	case ContainsAny(text, "m.status =", "response complete.", "saving transcript.", "server ready on"):
		return "status"
	default:
		return ""
	}
}

func ScoreLocateCandidate(prompt string, candidate LocateCandidate) int {
	normalized := strings.ToLower(prompt)
	text := strings.ToLower(candidate.Path + "\n" + candidate.Match + "\n" + candidate.Snippet)
	region := ClassifyLocateRegion(candidate)
	score := 0

	if strings.Contains(text, "qwen is working") {
		score += 2
	}
	if ContainsAny(text, "workingframe", "func (m *model) workingdots", "strings.repeat") {
		score -= 8
	}
	if strings.Contains(text, "workingstyle :=") {
		score -= 3
	}

	switch region {
	case "header":
		score += 12
	case "chat":
		score += 4
	case "footer":
		score += 2
	case "status":
		score += 1
	}

	if ContainsAny(normalized, "top", "top of the window", "header", "banner", "window") {
		switch region {
		case "header":
			score += 12
		case "footer", "status", "chat":
			score -= 6
		}
	}
	if ContainsAny(normalized, "footer", "bottom") {
		if region == "footer" {
			score += 10
		} else if region == "header" {
			score -= 4
		}
	}
	if ContainsAny(normalized, "badge", "chat", "menu") {
		if region == "chat" {
			score -= 10
		}
		if region == "header" {
			score += 4
		}
	}
	if strings.Contains(normalized, "not the one") && region == "chat" {
		score -= 8
	}

	return score
}

func SortLocateCandidates(candidates []LocateCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		a := candidates[i]
		b := candidates[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Line < b.Line
	})
}

func SelectLocateAlternates(candidates []LocateCandidate) []LocateCandidate {
	if len(candidates) <= 1 {
		return nil
	}
	alternates := make([]LocateCandidate, 0, 2)
	seenRegions := make(map[string]struct{})
	bestRegion := candidates[0].Region
	for _, candidate := range candidates[1:] {
		if len(alternates) >= 2 {
			break
		}
		if candidate.Region == "" || candidate.Region == bestRegion {
			continue
		}
		if _, ok := seenRegions[candidate.Region]; ok {
			continue
		}
		seenRegions[candidate.Region] = struct{}{}
		alternates = append(alternates, candidate)
	}
	return alternates
}
