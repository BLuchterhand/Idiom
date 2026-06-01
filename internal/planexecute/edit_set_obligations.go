package planexecute

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

type EditSetObligationKind string

const (
	EditSetObligationKindTestEdit           EditSetObligationKind = "test_edit_required"
	EditSetObligationKindImplementationEdit EditSetObligationKind = "implementation_edit_required"

	EditSetFailureMissingRequestedTestEdit           = "missing_requested_test_edit"
	EditSetFailureMissingRequestedImplementationEdit = "missing_requested_implementation_edit"
)

type EditSetObligation struct {
	Kind   EditSetObligationKind `json:"kind"`
	Source string                `json:"source,omitempty"`
}

type EditSetObligations []EditSetObligation

func EditSetObligationsFromPrompt(prompt string) EditSetObligations {
	return nil
}

func planExecuteEditSetObligations(opts Options, prompt string) EditSetObligations {
	return planExecuteScopeContract(opts, prompt).EditSetObligations
}

func ExtractRequestedVerificationCommands(prompt string) []string {
	return planExecuteRequestedVerificationCommands(prompt)
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
		for _, command := range planExecuteGoTestCommandsFromPlainText(span) {
			commands = append(commands, command)
		}
	}
	text = planExecuteStripInlineCodeSpans(text)
	for _, command := range planExecuteGoTestCommandsFromPlainText(text) {
		commands = append(commands, command)
	}
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

func planExecuteVerificationCommands(commands []string) []string {
	out := make([]string, 0, len(commands))
	seen := map[string]struct{}{}
	for _, command := range commands {
		command = strings.TrimSpace(command)
		if !planExecuteVerificationCommandAllowed(command) {
			continue
		}
		if _, ok := seen[command]; ok {
			continue
		}
		seen[command] = struct{}{}
		out = append(out, command)
	}
	return out
}

func VerificationCommands(commands []string) []string {
	return planExecuteVerificationCommands(commands)
}

func VerificationCommandsForChangedPaths(commands []string, changedPaths []string) []string {
	combined := append([]string(nil), commands...)
	combined = append(combined, InferredTouchedVerificationCommands(changedPaths)...)
	return planExecuteVerificationCommands(combined)
}

func VerificationCommandsForChangedPathsInRoot(root string, commands []string, changedPaths []string) []string {
	if !goModuleRootAvailable(root) {
		return planExecuteVerificationCommands(commands)
	}
	return VerificationCommandsForChangedPaths(commands, changedPaths)
}

func InferredTouchedVerificationCommands(paths []string) []string {
	seen := map[string]struct{}{}
	var commands []string
	for _, rawPath := range paths {
		packagePath, ok := goTestPackageForChangedPath(rawPath)
		if !ok {
			continue
		}
		command := "go test " + packagePath
		if _, exists := seen[command]; exists {
			continue
		}
		seen[command] = struct{}{}
		commands = append(commands, command)
	}
	return commands
}

func goTestPackageForChangedPath(rawPath string) (string, bool) {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return "", false
	}
	cleaned := filepath.ToSlash(filepath.Clean(rawPath))
	if filepath.IsAbs(rawPath) || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return "", false
	}
	if !strings.HasSuffix(cleaned, ".go") {
		return "", false
	}
	dir := path.Dir(cleaned)
	if dir == "." || dir == "/" {
		return ".", true
	}
	return "./" + strings.TrimPrefix(dir, "./"), true
}

func goModuleRootAvailable(root string) bool {
	root = strings.TrimSpace(root)
	if root == "" {
		root = "."
	}
	info, err := os.Stat(filepath.Join(root, "go.mod"))
	return err == nil && !info.IsDir()
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

func (obligations EditSetObligations) Has(kind EditSetObligationKind) bool {
	for _, obligation := range obligations {
		if obligation.Kind == kind {
			return true
		}
	}
	return false
}

func (obligations EditSetObligations) Kinds() []string {
	out := make([]string, 0, len(obligations))
	for _, obligation := range obligations.Normalized() {
		if obligation.Kind == "" {
			continue
		}
		out = append(out, string(obligation.Kind))
	}
	return out
}

func (obligations EditSetObligations) Normalized() EditSetObligations {
	return obligations.normalizedWithDefaultSource("typed")
}

func (obligations EditSetObligations) WithDefaultSource(defaultSource string) EditSetObligations {
	return obligations.normalizedWithDefaultSource(defaultSource)
}

func (obligations EditSetObligations) normalizedWithDefaultSource(defaultSource string) EditSetObligations {
	var out EditSetObligations
	for _, obligation := range obligations {
		kind := obligation.Kind
		switch kind {
		case EditSetObligationKindTestEdit, EditSetObligationKindImplementationEdit:
		default:
			continue
		}
		source := strings.TrimSpace(obligation.Source)
		if source == "" {
			source = strings.TrimSpace(defaultSource)
			if source == "" {
				source = "prompt"
			}
		}
		out = out.with(kind, source)
	}
	return out
}

func MergeEditSetObligations(groups ...EditSetObligations) EditSetObligations {
	var out EditSetObligations
	for _, group := range groups {
		for _, obligation := range group.Normalized() {
			out = out.with(obligation.Kind, obligation.Source)
		}
	}
	return out.Normalized()
}

func (obligations EditSetObligations) With(kind EditSetObligationKind, source string) EditSetObligations {
	return obligations.with(kind, source).Normalized()
}

func (obligations EditSetObligations) RejectionReason(changedPaths []string) string {
	if obligations.Has(EditSetObligationKindTestEdit) && !planExecuteChangedPathsIncludeTest(changedPaths) {
		return EditSetFailureMissingRequestedTestEdit
	}
	if obligations.Has(EditSetObligationKindImplementationEdit) && !planExecuteChangedPathsIncludeImplementation(changedPaths) {
		return EditSetFailureMissingRequestedImplementationEdit
	}
	return ""
}

func (obligations EditSetObligations) MissingOpenObligations(changedPaths []string) []string {
	var out []string
	if obligations.Has(EditSetObligationKindTestEdit) && !planExecuteChangedPathsIncludeTest(changedPaths) {
		out = append(out, "requested_test_edit_missing")
	}
	if obligations.Has(EditSetObligationKindImplementationEdit) && !planExecuteChangedPathsIncludeImplementation(changedPaths) {
		out = append(out, "requested_implementation_edit_missing")
	}
	return out
}

func EditSetObligationFailureMessage(reason string) string {
	switch reason {
	case EditSetFailureMissingRequestedTestEdit:
		return reason + ": typed test-edit obligation is unsatisfied because submitted evidence changed no test path"
	case EditSetFailureMissingRequestedImplementationEdit:
		return reason + ": typed implementation-edit obligation is unsatisfied because submitted evidence changed only test paths"
	default:
		return reason
	}
}

func (obligations EditSetObligations) with(kind EditSetObligationKind, source string) EditSetObligations {
	for i := range obligations {
		if obligations[i].Kind == kind {
			if strings.TrimSpace(obligations[i].Source) == "" {
				obligations[i].Source = source
			}
			return obligations
		}
	}
	return append(obligations, EditSetObligation{Kind: kind, Source: source})
}
