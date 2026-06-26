package plancontract

import (
	"strings"
)

func VerificationCommandPrefixes() []string {
	return []string{"python -m pytest", "pytest", "go test", "go vet"}
}

func VerificationCommandAllowedPrefixes() [][]string {
	return [][]string{{"go", "test"}, {"go", "vet"}, {"pytest"}, {"python", "-m", "pytest"}}
}

func VerificationCommandArgs(command string) ([]string, bool) {
	args, ok := ParseVerificationCommand(command)
	if !ok || !VerificationArgvAllowed(args) {
		return nil, false
	}

	return args, true
}

func VerificationArgvAllowed(args []string) bool {
	args = NormalizeVerificationArgv(args)
	if len(args) == 0 {
		return false
	}

	for _, prefix := range VerificationCommandAllowedPrefixes() {
		if len(args) < len(prefix) {
			continue
		}

		matches := true

		for i := range prefix {
			if args[i] != prefix[i] {
				matches = false
				break
			}
		}

		if matches {
			return true
		}
	}

	return false
}

func NormalizeVerificationArgv(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" || strings.ContainsAny(arg, "\x00\n\r") {
			return nil
		}

		if strings.Contains(arg, ";") || strings.Contains(arg, "&&") || strings.Contains(arg, "||") || strings.ContainsAny(arg, "`><") {
			return nil
		}

		out = append(out, arg)
	}

	return out
}

func ParseVerificationCommand(command string) ([]string, bool) {
	command = strings.TrimSpace(command)
	if command == "" || strings.ContainsAny(command, "\n\r") {
		return nil, false
	}

	var (
		args    []string
		builder strings.Builder
	)

	quote := rune(0)
	escaped := false

	for _, r := range command {
		if escaped {
			builder.WriteRune(r)

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
				if builder.Len() > 0 {
					args = append(args, builder.String())
					builder.Reset()
				}
			case ';', '&', '|', '`', '$', '(', ')', '>', '<':
				return nil, false
			default:
				builder.WriteRune(r)
			}

			continue
		}

		if r == quote {
			quote = 0
			continue
		}

		builder.WriteRune(r)
	}

	if escaped || quote != 0 {
		return nil, false
	}

	if builder.Len() > 0 {
		args = append(args, builder.String())
	}

	return args, len(args) > 0
}

func CanonicalVerificationCommand(command string, argv []string) string {
	command = strings.TrimSpace(command)
	if len(argv) == 0 && command != "" {
		if parsed, ok := VerificationCommandArgs(command); ok {
			argv = parsed
		}
	}
	argv = NormalizeVerificationArgv(argv)
	if len(argv) > 0 && VerificationArgvAllowed(argv) {
		return strings.Join(argv, " ")
	}
	return command
}

func VerificationCommandEvidenceKey(command string, argv []string, workingDir string) string {
	canonical := CanonicalVerificationCommand(command, argv)
	if canonical == "" {
		return ""
	}

	workingDir = NormalizeScopePath(workingDir)
	if workingDir == "." {
		workingDir = ""
	}

	return workingDir + "\x00" + canonical
}
