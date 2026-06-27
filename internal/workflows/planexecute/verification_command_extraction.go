package planexecute

import (
	"strings"

	plancontract "idiom/internal/contracts/plan"
)

func planExecuteRequestedVerificationCommands(prompt string) []string {
	var commands []string

	seen := map[string]struct{}{}

	for _, line := range strings.Split(prompt, "\n") {
		if !planExecuteLineRequestsVerificationCommand(line) {
			continue
		}

		for _, command := range planExecuteVerificationCommandsFromLine(line) {
			if _, ok := seen[command]; ok {
				continue
			}

			seen[command] = struct{}{}
			commands = append(commands, command)
		}
	}

	return commands
}

func planExecuteLineRequestsVerificationCommand(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" || !planExecuteLineContainsSupportedVerificationCommand(lower) {
		return false
	}

	return containsAnyVerificationRequestPhrase(lower,
		"after editing, run",
		"after edits, run",
		"after implementation, run",
		"after the changes, run",
		"make this pass",
		"make it pass",
		"must pass",
		"must now pass",
		"required verification",
		"required test command",
		"requested verification",
		"requested test command",
		"run this verification",
		"run this test",
		"run:",
		"run `go test",
		"run go test",
		"verify with",
		"verification command",
	)
}

func planExecuteLineContainsSupportedVerificationCommand(line string) bool {
	for _, prefix := range plancontract.VerificationCommandPrefixes() {
		if strings.Contains(line, prefix) {
			return true
		}
	}

	return false
}

func containsAnyVerificationRequestPhrase(text string, phrases ...string) bool {
	for _, phrase := range phrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}

	return false
}

func planExecuteVerificationCommandsFromLine(line string) []string {
	var commands []string

	text := strings.TrimSpace(line)

	for _, span := range planExecuteInlineCodeSpans(text) {
		commands = append(commands, planExecuteVerificationCommandsFromPlainText(span)...)
	}

	text = planExecuteStripInlineCodeSpans(text)
	commands = append(commands, planExecuteVerificationCommandsFromPlainText(text)...)

	return commands
}

func planExecuteVerificationCommandsFromPlainText(text string) []string {
	var commands []string

	for {
		idx, prefix := planExecuteFirstVerificationCommandPrefix(text)
		if idx < 0 || prefix == "" {
			return commands
		}

		candidate, remaining := planExecuteSplitVerificationCommandCandidate(text[idx:], prefix)
		command := planExecuteTrimVerificationCommand(candidate)

		if strings.TrimSpace(command) != "" {
			commands = append(commands, command)
		}

		if strings.TrimSpace(remaining) == "" {
			return commands
		}

		text = remaining
	}
}

func planExecuteSplitVerificationCommandCandidate(text string, prefix string) (string, string) {
	lower := strings.ToLower(text)
	searchStart := len(prefix)
	if searchStart > len(lower) {
		searchStart = len(lower)
	}

	bestIndex := -1
	bestPrefixIndex := -1
	for _, nextPrefix := range plancontract.VerificationCommandPrefixes() {
		for _, marker := range []string{", and " + nextPrefix, ", " + nextPrefix, " and " + nextPrefix} {
			idx := strings.Index(lower[searchStart:], marker)
			if idx < 0 {
				continue
			}
			idx += searchStart
			prefixIndex := idx + len(marker) - len(nextPrefix)
			if bestIndex < 0 || idx < bestIndex {
				bestIndex = idx
				bestPrefixIndex = prefixIndex
			}
		}
	}

	if bestIndex < 0 {
		return text, ""
	}

	return text[:bestIndex], text[bestPrefixIndex:]
}

func planExecuteFirstVerificationCommandPrefix(text string) (int, string) {
	text = strings.ToLower(text)

	bestIndex := -1
	bestPrefix := ""
	for _, prefix := range plancontract.VerificationCommandPrefixes() {
		idx := strings.Index(text, prefix)
		if idx < 0 {
			continue
		}
		if bestIndex < 0 || idx < bestIndex || (idx == bestIndex && len(prefix) > len(bestPrefix)) {
			bestIndex = idx
			bestPrefix = prefix
		}
	}

	return bestIndex, bestPrefix
}

func planExecuteInlineCodeSpans(text string) []string {
	var spans []string

	for {
		start := strings.Index(text, "`")
		if start < 0 {
			return spans
		}

		rest := text[start+1:]
		end := strings.Index(rest, "`")

		if end < 0 {
			return spans
		}

		spans = append(spans, rest[:end])
		text = rest[end+1:]
	}
}

func planExecuteStripInlineCodeSpans(text string) string {
	var b strings.Builder

	for {
		start := strings.Index(text, "`")
		if start < 0 {
			b.WriteString(text)

			return b.String()
		}

		b.WriteString(text[:start])

		rest := text[start+1:]
		end := strings.Index(rest, "`")

		if end < 0 {
			return b.String()
		}

		text = rest[end+1:]
	}
}

func planExecuteTrimVerificationCommand(text string) string {
	text = strings.TrimSpace(text)
	if idx := strings.Index(text, "```"); idx >= 0 {
		text = text[:idx]
	}

	text = planExecuteTrimVerificationTrailingPhrases(text)
	text = planExecuteTrimVerificationSentence(text)
	text = planExecuteTrimVerificationTrailingPhrases(text)
	text = strings.TrimSpace(text)

	for (strings.HasSuffix(text, ".") && !strings.HasSuffix(text, "...")) || strings.HasSuffix(text, ",") {
		text = strings.TrimSpace(text[:len(text)-1])
	}

	return text
}

func planExecuteTrimVerificationTrailingPhrases(text string) string {
	lower := strings.ToLower(text)
	for _, marker := range []string{
		" as the verification commands",
		" as the verification command",
		" as verification commands",
		" as verification command",
		" as the test commands",
		" as the test command",
		" after edits",
		" after editing",
		" after implementation",
		" after the changes",
	} {
		if idx := strings.Index(lower, marker); idx >= 0 {
			return strings.TrimSpace(text[:idx])
		}
	}

	return text
}

func planExecuteTrimVerificationSentence(text string) string {
	quote := rune(0)
	for i, r := range text {
		if quote == 0 {
			if r == '\'' || r == '"' {
				quote = r
				continue
			}

			if r == '.' && i+1 < len(text) {
				next := rune(text[i+1])
				previous := rune(0)

				if i > 0 {
					previous = rune(text[i-1])
				}

				if (next == ' ' || next == '\t') && previous != '.' {
					return text[:i]
				}
			}

			continue
		}

		if r == quote {
			quote = 0
		}
	}

	return text
}
