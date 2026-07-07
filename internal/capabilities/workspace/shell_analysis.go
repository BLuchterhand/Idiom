package workspace

import (
	"path/filepath"
	"strings"

	"idiom/internal/contracts/autonomy"
)

// ShellCommandAnalysis contains parsed facts and conservative policy
// classification for a shell command. It intentionally does not try to be a
// full shell parser; the harness treats unsupported shell syntax as unknown.
type ShellCommandAnalysis struct {
	Fields           []string
	Head             string
	Args             []string
	HasControlSyntax bool
	BroadInspection  bool
	Narrowing        bool
	BuildOrTest      bool
	SideEffect       autonomy.SideEffectClass
}

// AnalyzeShellCommand is the single workspace authority for shell command facts
// used by discovery profiling, child-task policy, and runtime autonomy events.
func AnalyzeShellCommand(command string) ShellCommandAnalysis {
	command = strings.TrimSpace(command)
	analysis := ShellCommandAnalysis{
		SideEffect: autonomy.SideEffectUnknown,
	}
	if command == "" {
		return analysis
	}

	analysis.HasControlSyntax = shellCommandHasControlSyntax(command)
	analysis.Fields = strings.Fields(command)
	if len(analysis.Fields) == 0 {
		return analysis
	}

	rawHead := analysis.Fields[0]
	analysis.Head = normalizeShellHead(rawHead)
	analysis.Args = lowerShellArgs(analysis.Fields[1:])
	analysis.BroadInspection = shellHeadInSet(rawHead, "ls", "find", "tree")
	analysis.Narrowing = shellHeadInSet(rawHead, "rg", "grep", "awk", "sed", "cat", "head", "tail")
	analysis.BuildOrTest = shellCommandBuildOrTest(command)

	if !analysis.HasControlSyntax && readOnlyShellCommand(analysis.Head, analysis.Args) {
		analysis.SideEffect = autonomy.SideEffectReadOnly
	}

	return analysis
}

func shellCommandBuildOrTest(command string) bool {
	for _, part := range splitShellSegments(command) {
		fields := strings.Fields(part)
		if len(fields) == 0 {
			continue
		}

		if shellHeadIsBuildOrTest(normalizeShellHead(fields[0]), lowerShellArgs(fields[1:])) {
			return true
		}
	}

	return false
}

func shellCommandHasControlSyntax(command string) bool {
	inSingleQuote := false
	inDoubleQuote := false
	escaped := false

	for i := 0; i < len(command); i++ {
		ch := command[i]

		if escaped {
			escaped = false
			continue
		}

		if ch == '\\' {
			escaped = true
			continue
		}

		if ch == '\'' && !inDoubleQuote {
			inSingleQuote = !inSingleQuote
			continue
		}

		if ch == '"' && !inSingleQuote {
			inDoubleQuote = !inDoubleQuote
			continue
		}

		if inSingleQuote {
			continue
		}

		if ch == '$' && i+1 < len(command) && command[i+1] == '(' {
			return true
		}

		if inDoubleQuote {
			if ch == '`' {
				return true
			}
			continue
		}

		switch ch {
		case '\n', '\r', ';', '&', '|', '<', '>', '`':
			return true
		}
	}

	return false
}

func normalizeShellHead(value string) string {
	value = strings.Trim(value, `"'`)
	value = filepath.Base(value)
	value = strings.TrimPrefix(value, "./")
	return strings.ToLower(value)
}

func lowerShellArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		arg = strings.ToLower(strings.TrimSpace(strings.Trim(arg, `"'`)))
		if arg != "" {
			out = append(out, arg)
		}
	}

	return out
}

func readOnlyShellCommand(head string, args []string) bool {
	switch head {
	case "git":
		return readOnlyGitArgs(args)
	case "go":
		return len(args) > 0 && shellHeadInSet(args[0], "test", "build")
	case "make":
		return len(args) > 0 && shellHeadInSet(args[0], "test", "build")
	case "npm":
		return len(args) > 0 && args[0] == "test"
	case "cargo":
		return len(args) > 0 && args[0] == "test"
	default:
		return false
	}
}

func shellHeadIsBuildOrTest(head string, args []string) bool {
	switch head {
	case "scons", "ctest", "ninja", "meson", "pytest", "tox", "godot":
		return true
	case "make", "cmake", "mvn", "gradle", "gradlew":
		return true
	case "go":
		return len(args) > 0 && shellHeadInSet(args[0], "test", "build", "vet")
	case "cargo":
		return len(args) > 0 && shellHeadInSet(args[0], "test", "build", "check", "clippy")
	case "npm", "pnpm", "yarn":
		return packageManagerArgsAreBuildOrTest(args)
	case "python", "python3":
		return pythonArgsAreTest(args)
	default:
		return strings.Contains(head, "test") && !strings.Contains(head, "diff")
	}
}

func packageManagerArgsAreBuildOrTest(args []string) bool {
	if len(args) == 0 {
		return false
	}

	first := args[0]
	if shellHeadInSet(first, "test", "build") {
		return true
	}

	return first == "run" && len(args) > 1 && shellHeadInSet(args[1], "test", "build", "check", "lint")
}

func pythonArgsAreTest(args []string) bool {
	if len(args) == 0 {
		return false
	}

	for i, arg := range args {
		arg = strings.Trim(strings.ToLower(strings.TrimSpace(arg)), `"'`)
		if arg == "" {
			continue
		}

		if i == 0 && strings.HasPrefix(arg, "pytest") {
			return true
		}

		if arg == "-m" && i+1 < len(args) {
			module := strings.Trim(strings.ToLower(strings.TrimSpace(args[i+1])), `"'`)
			if strings.HasPrefix(module, "pytest") || shellHeadInSet(module, "unittest", "build") {
				return true
			}
		}

		if strings.HasPrefix(arg, "-") {
			continue
		}

		base := filepath.Base(arg)
		if strings.HasSuffix(base, ".py") &&
			(strings.Contains(base, "test") || strings.Contains(base, "build")) {

			return true
		}
	}

	return false
}

func readOnlyGitArgs(args []string) bool {
	if len(args) == 0 {
		return false
	}

	switch args[0] {
	case "status":
		return true
	case "diff":
		for _, arg := range args[1:] {
			if arg == "--output" || strings.HasPrefix(arg, "--output=") {
				return false
			}
		}

		return true
	default:
		return false
	}
}

func shellHeadInSet(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}

	return false
}
