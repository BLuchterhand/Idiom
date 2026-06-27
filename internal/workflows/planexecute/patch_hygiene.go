package planexecute

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/atomicfile"
)

func AutoRepairReviewPatchHygiene(root string, changedPaths []string, evidence ReviewRepairEvidence) (PatchHygieneAutoRepairReport, error) {
	if evidence.DiffCheckExit != 1 {
		return PatchHygieneAutoRepairReport{}, nil
	}

	trailingWhitespace := reviewRepairTrailingWhitespaceLines(evidence.Diff)
	if len(trailingWhitespace) == 0 {
		return PatchHygieneAutoRepairReport{}, nil
	}

	changedSet := planExecutePathSet(changedPaths)
	paths := make([]string, 0, len(trailingWhitespace))

	for path := range trailingWhitespace {
		normalized := plancontract.NormalizeScopePath(path)
		if _, ok := changedSet[normalized]; !ok {
			return PatchHygieneAutoRepairReport{}, nil
		}

		paths = append(paths, normalized)
	}

	slices.Sort(paths)

	report := PatchHygieneAutoRepairReport{Repaired: true, Paths: paths}
	for _, path := range paths {
		fullPath, err := planExecuteWorkspaceFile(root, path)
		if err != nil {
			return PatchHygieneAutoRepairReport{}, err
		}

		body, err := os.ReadFile(fullPath)
		if err != nil {
			return PatchHygieneAutoRepairReport{}, fmt.Errorf("read %s for patch hygiene repair: %w", path, err)
		}

		repaired, lines := trimTrailingWhitespaceOnLines(string(body), trailingWhitespace[path])
		if lines == 0 {
			continue
		}

		if err := atomicfile.WriteFile(fullPath, []byte(repaired), 0o644); err != nil {
			return PatchHygieneAutoRepairReport{}, fmt.Errorf("write %s for patch hygiene repair: %w", path, err)
		}

		report.Lines += lines
	}

	if report.Lines == 0 {
		return PatchHygieneAutoRepairReport{}, nil
	}

	return report, nil
}

func reviewRepairWhitespaceCheck(diff string) (string, int) {
	findings := reviewRepairTrailingWhitespaceFindings(diff)
	if len(findings) == 0 {
		return "", 0
	}

	return strings.Join(findings, "\n"), 1
}

func reviewRepairTrailingWhitespaceFindings(diff string) []string {
	var findings []string

	path := ""
	newLine := 0
	inHunk := false

	for _, raw := range strings.Split(diff, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			path = strings.TrimSpace(strings.TrimPrefix(line, "+++ b/"))
			inHunk = false
			newLine = 0

			continue
		case strings.HasPrefix(line, "@@ "):
			newLine = parseReviewRepairHunkNewStart(line)
			inHunk = true

			continue
		case strings.HasPrefix(line, "diff --git ") || strings.HasPrefix(line, "--- "):
			inHunk = false
			newLine = 0

			continue
		}

		if !inHunk || line == "" || strings.HasPrefix(line, "\\ ") {
			continue
		}

		switch line[0] {
		case '+':
			if strings.HasPrefix(line, "+++") {
				continue
			}

			content := line[1:]
			if strings.TrimRight(content, " \t") != content {
				loc := path
				if newLine > 0 {
					loc = fmt.Sprintf("%s:%d", firstNonEmptyString(path, "(unknown)"), newLine)
				}

				findings = append(findings, fmt.Sprintf("%s: trailing whitespace.\n%s", loc, line))
			}

			if newLine > 0 {
				newLine++
			}
		case ' ':
			if newLine > 0 {
				newLine++
			}
		case '-':
		}
	}

	return findings
}

func reviewRepairTrailingWhitespaceLines(diff string) map[string]map[int]struct{} {
	out := make(map[string]map[int]struct{})
	path := ""
	newLine := 0
	inHunk := false

	for _, raw := range strings.Split(diff, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			path = plancontract.NormalizeScopePath(strings.TrimSpace(strings.TrimPrefix(line, "+++ b/")))
			inHunk = false
			newLine = 0

			continue
		case strings.HasPrefix(line, "@@ "):
			newLine = parseReviewRepairHunkNewStart(line)
			inHunk = true

			continue
		case strings.HasPrefix(line, "diff --git ") || strings.HasPrefix(line, "--- "):
			inHunk = false
			newLine = 0

			continue
		}

		if !inHunk || line == "" || strings.HasPrefix(line, "\\ ") {
			continue
		}

		switch line[0] {
		case '+':
			if strings.HasPrefix(line, "+++") {
				continue
			}

			content := line[1:]
			if path != "" && newLine > 0 && strings.TrimRight(content, " \t") != content {
				if _, ok := out[path]; !ok {
					out[path] = make(map[int]struct{})
				}

				out[path][newLine] = struct{}{}
			}

			if newLine > 0 {
				newLine++
			}
		case ' ':
			if newLine > 0 {
				newLine++
			}
		case '-':
		}
	}

	return out
}

func trimTrailingWhitespaceOnLines(text string, targetLines map[int]struct{}) (string, int) {
	if len(targetLines) == 0 {
		return text, 0
	}

	var b strings.Builder

	b.Grow(len(text))

	changed := 0
	lineNo := 1

	for start := 0; start < len(text); lineNo++ {
		end := start
		for end < len(text) && text[end] != '\n' {
			end++
		}

		line := text[start:end]
		newline := ""

		if end < len(text) && text[end] == '\n' {
			newline = "\n"
			end++
		}

		if _, ok := targetLines[lineNo]; ok {
			repaired := trimTrailingWhitespaceBeforeLineEnding(line)
			if repaired != line {
				changed++

				line = repaired
			}
		}

		b.WriteString(line)
		b.WriteString(newline)

		start = end
	}

	return b.String(), changed
}

func trimTrailingWhitespaceEveryLine(text string) string {
	var b strings.Builder

	b.Grow(len(text))

	for start := 0; start < len(text); {
		end := start
		for end < len(text) && text[end] != '\n' {
			end++
		}

		line := text[start:end]
		newline := ""

		if end < len(text) && text[end] == '\n' {
			newline = "\n"
			end++
		}

		b.WriteString(trimTrailingWhitespaceBeforeLineEnding(line))
		b.WriteString(newline)

		start = end
	}

	return b.String()
}

func trimTrailingWhitespaceBeforeLineEnding(line string) string {
	if strings.HasSuffix(line, "\r") {
		return strings.TrimRight(line[:len(line)-1], " \t") + "\r"
	}

	return strings.TrimRight(line, " \t")
}

func planExecuteWorkspaceFile(root string, path string) (string, error) {
	normalized := plancontract.NormalizeScopePath(path)
	if normalized == "" {
		return "", fmt.Errorf("empty workspace path")
	}

	if filepath.IsAbs(normalized) {
		return "", fmt.Errorf("workspace path must be relative: %s", normalized)
	}

	cleanRoot := filepath.Clean(strings.TrimSpace(root))
	fullPath := filepath.Clean(filepath.Join(cleanRoot, filepath.FromSlash(normalized)))
	rel, err := filepath.Rel(cleanRoot, fullPath)

	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") || strings.HasPrefix(rel, `..\`) {
		return "", fmt.Errorf("workspace path escapes root: %s", normalized)
	}

	return fullPath, nil
}

func parseReviewRepairHunkNewStart(line string) int {
	plus := strings.Index(line, " +")
	if plus < 0 {
		return 0
	}

	rest := line[plus+2:]
	end := 0

	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}

	if end == 0 {
		return 0
	}

	value, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0
	}

	return value
}
