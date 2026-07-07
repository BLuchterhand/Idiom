package workspace

import (
	"fmt"
	"path/filepath"
	"strings"

	"idiom/internal/contracts/autonomy"
	"idiom/internal/platform/textutil"
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
	Replacement      *ShellReplacementCandidate
}

var shellCommandPolicy = struct {
	broadInspectionHeads    []string
	narrowingHeads          []string
	exploreHeads            []string
	exploreGitSubcommands   []string
	readOnlyChildPrefixes   []string
	workbranchChildPrefixes []string
	childDeniedFragments    []string
}{
	broadInspectionHeads:  []string{"ls", "find", "tree"},
	narrowingHeads:        []string{"rg", "grep", "awk", "sed", "cat", "head", "tail"},
	exploreHeads:          []string{"ls", "find", "rg", "grep", "awk", "sed", "cat", "head", "tail", "wc", "file", "pwd", "tree"},
	exploreGitSubcommands: []string{"status", "log", "diff", "show", "grep", "branch", "rev-parse"},
	readOnlyChildPrefixes: []string{
		"rg ", "grep ", "awk ", "sed -n", "cat ", "head ", "tail ", "wc ",
		"pwd", "ls ", "find ",
		"git status", "git diff", "git log", "git show", "git grep",
		"go test", "go list", "go env",
	},
	workbranchChildPrefixes: []string{
		"go test", "go list",
		"git status", "git diff", "git log", "git show", "git grep",
		"pwd", "ls ", "rg ", "grep ", "cat ", "head ", "tail ", "wc ",
	},
	childDeniedFragments: []string{"\n", "\r", "&&", "||", ";", "|", ">", "<", "$(", "`", "go env -w", "go env -u", "--output"},
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
	analysis.Fields = shellCommandFields(command)
	if len(analysis.Fields) == 0 {
		return analysis
	}

	rawHead := analysis.Fields[0]
	analysis.Head = normalizeShellHead(rawHead)
	analysis.Args = lowerShellArgs(analysis.Fields[1:])
	analysis.BroadInspection = shellHeadInSet(rawHead, shellCommandPolicy.broadInspectionHeads...)
	analysis.Narrowing = shellHeadInSet(rawHead, shellCommandPolicy.narrowingHeads...)
	analysis.BuildOrTest = shellCommandBuildOrTest(command)
	analysis.Replacement = shellReplacementCandidate(command)

	if !analysis.HasControlSyntax && readOnlyShellCommand(analysis.Head, analysis.Args) {
		analysis.SideEffect = autonomy.SideEffectReadOnly
	}

	return analysis
}

func validateExploreShellCommand(command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return fmt.Errorf("command is required")
	}

	if strings.ContainsAny(command, "\n\r><`") || strings.Contains(command, "$(") {
		return fmt.Errorf("redirection, command substitution, and multiline shell are not allowed")
	}

	for _, segment := range splitShellSegments(command) {
		fields := shellCommandFields(segment)
		if len(fields) == 0 {
			continue
		}

		if shellHeadInSet(fields[0], shellCommandPolicy.exploreHeads...) {
			continue
		}

		if fields[0] == "git" {
			if len(fields) < 2 {
				return fmt.Errorf("git command requires a read-only subcommand")
			}

			if shellHeadInSet(fields[1], shellCommandPolicy.exploreGitSubcommands...) {
				continue
			}

			return fmt.Errorf("git %s is not allowed in explore mode", fields[1])
		}

		return fmt.Errorf("%s is not an allowed explore-mode shell command", fields[0])
	}

	return nil
}

type ChildShellPolicy struct {
	AllowedPrefixes []string
	DeniedPrefixes  []string
	DeniedFragments []string
}

func ReadOnlyChildShellPolicy() ChildShellPolicy {
	return ChildShellPolicy{
		AllowedPrefixes: append([]string(nil), shellCommandPolicy.readOnlyChildPrefixes...),
		DeniedFragments: append([]string(nil), shellCommandPolicy.childDeniedFragments...),
	}
}

func WorkbranchChildShellPolicy(forbiddenTools []string) ChildShellPolicy {
	policy := ChildShellPolicy{
		AllowedPrefixes: append([]string(nil), shellCommandPolicy.workbranchChildPrefixes...),
		DeniedPrefixes: []string{
			"gh pr create",
			"git -C",
		},
		DeniedFragments: append([]string(nil), shellCommandPolicy.childDeniedFragments...),
	}

	for _, forbidden := range forbiddenTools {
		switch strings.TrimSpace(forbidden) {
		case "git_push":
			policy.DeniedPrefixes = append(policy.DeniedPrefixes, "git push", "gh pr merge")
		case "git_history_mutation":
			policy.DeniedPrefixes = append(policy.DeniedPrefixes, "git commit", "git reset", "git checkout", "git switch", "git worktree", "git branch", "git clean")
		case "git_index_mutation":
			policy.DeniedPrefixes = append(policy.DeniedPrefixes, "git add", "git restore")
		case "publish":
			policy.DeniedPrefixes = append(policy.DeniedPrefixes, "npm publish", "pnpm publish", "yarn publish", "docker push")
		case "deploy":
			policy.DeniedPrefixes = append(policy.DeniedPrefixes, "kubectl ", "deploy", "release", "gh release")
		}
	}

	policy.DeniedPrefixes = textutil.Merge(policy.DeniedPrefixes)
	return policy
}

// ShellCommandFromToolResult extracts the command prefix emitted by the
// workspace shell tool. The shell result output starts with "$ <command>";
// if any other non-empty line appears first, the result is not treated as a
// structured shell command result.
func ShellCommandFromToolResult(result ToolResult) string {
	for _, line := range strings.Split(strings.TrimSpace(result.Output), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "$ ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "$ "))
		}

		if line != "" {
			return ""
		}
	}

	return ""
}

func shellCommandBuildOrTest(command string) bool {
	for _, part := range splitShellSegments(command) {
		fields := shellCommandFields(part)
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
