package planexecute

import (
	"strings"

	"idiom/internal/contracts/plan"
)

type EditSetObligationKind = plancontract.EditSetObligationKind

const (
	EditSetObligationKindTestEdit           = plancontract.EditSetObligationKindTestEdit
	EditSetObligationKindImplementationEdit = plancontract.EditSetObligationKindImplementationEdit

	EditSetFailureMissingRequestedTestEdit           = plancontract.EditSetFailureMissingRequestedTestEdit
	EditSetFailureMissingRequestedImplementationEdit = plancontract.EditSetFailureMissingRequestedImplementationEdit
)

type EditSetObligation = plancontract.EditSetObligation

type EditSetObligations = plancontract.EditSetObligations

func EditSetObligationsFromPrompt(prompt string) EditSetObligations {
	return plancontract.EditSetObligationsFromPrompt(prompt)
}

func planExecuteEditSetObligations(opts Options, prompt string) EditSetObligations {
	return planExecuteScopeContract(opts, prompt).EditSetObligations
}

func MergeEditSetObligations(groups ...EditSetObligations) EditSetObligations {
	return plancontract.MergeEditSetObligations(groups...)
}

func EditSetObligationFailureMessage(reason string) string {
	return plancontract.EditSetObligationFailureMessage(reason)
}

func ExtractRequestedVerificationCommands(prompt string) []string {
	return PlanVerification(VerificationPlannerInput{Prompt: prompt}).CommandStrings()
}

func planExecuteRequestedVerificationCommands(prompt string) []string {
	var commands []string
	seen := map[string]struct{}{}
	for _, line := range strings.Split(prompt, "\n") {
		if !planExecuteLineRequestsVerificationCommand(line) {
			continue
		}
		for _, command := range planExecuteGoTestCommandsFromLine(line) {
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
	if lower == "" || !strings.Contains(lower, "go test") {
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

func containsAnyVerificationRequestPhrase(text string, phrases ...string) bool {
	for _, phrase := range phrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

func planExecuteGoTestCommandsFromLine(line string) []string {
	var commands []string
	text := strings.TrimSpace(line)
	for _, span := range planExecuteInlineCodeSpans(text) {
		commands = append(commands, planExecuteGoTestCommandsFromPlainText(span)...)
	}
	text = planExecuteStripInlineCodeSpans(text)
	commands = append(commands, planExecuteGoTestCommandsFromPlainText(text)...)
	return commands
}

func planExecuteGoTestCommandsFromPlainText(text string) []string {
	var commands []string
	for {
		idx := strings.Index(text, "go test")
		if idx < 0 {
			return commands
		}
		rest := strings.TrimSpace(text[idx:])
		command := planExecuteTrimVerificationCommand(rest)
		if planExecuteVerificationCommandAllowed(command) {
			commands = append(commands, command)
		}
		if idx+len("go test") >= len(text) {
			return commands
		}
		text = text[idx+len("go test"):]
	}
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
	text = planExecuteTrimVerificationSentence(text)
	text = strings.TrimSpace(text)
	for (strings.HasSuffix(text, ".") && !strings.HasSuffix(text, "...")) || strings.HasSuffix(text, ",") {
		text = strings.TrimSpace(text[:len(text)-1])
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

func planExecuteVerificationCommandAllowed(command string) bool {
	_, ok := VerificationCommandArgs(command)
	return ok
}

func VerificationCommandArgs(command string) ([]string, bool) {
	args, ok := planExecuteVerificationCommandArgs(command)
	if !ok || len(args) < 2 {
		return nil, false
	}
	if args[0] != "go" || args[1] != "test" {
		return nil, false
	}
	return args, true
}

func planExecuteVerificationCommandArgs(command string) ([]string, bool) {
	command = strings.TrimSpace(command)
	if command == "" || strings.ContainsAny(command, "\n\r") {
		return nil, false
	}
	var args []string
	var b strings.Builder
	quote := rune(0)
	escaped := false
	for _, r := range command {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if quote == 0 {
			switch r {
			case '\\':
				escaped = true
			case '\'', '"':
				quote = r
			case ' ', '\t':
				if b.Len() > 0 {
					args = append(args, b.String())
					b.Reset()
				}
			case ';', '&', '|', '`', '$', '(', ')', '>', '<':
				return nil, false
			default:
				b.WriteRune(r)
			}
			continue
		}
		if r == quote {
			quote = 0
			continue
		}
		b.WriteRune(r)
	}
	if escaped || quote != 0 {
		return nil, false
	}
	if b.Len() > 0 {
		args = append(args, b.String())
	}
	return args, len(args) > 0
}
