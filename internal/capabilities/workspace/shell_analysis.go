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
	Verification     ShellVerificationFamily
	FullSuite        bool
	SideEffect       autonomy.SideEffectClass
	ExploreAllowed   bool
	ExploreRejection string
	ActionClass      autonomy.ActionClass
}

type ShellVerificationFamily string

const (
	ShellVerificationNone         ShellVerificationFamily = ""
	ShellVerificationScriptSyntax ShellVerificationFamily = "script_syntax"
	ShellVerificationFullSuite    ShellVerificationFamily = "full_suite"
	ShellVerificationTest         ShellVerificationFamily = "test"
	ShellVerificationBuild        ShellVerificationFamily = "build"
	ShellVerificationCheck        ShellVerificationFamily = "check"
	ShellVerificationGeneric      ShellVerificationFamily = "generic"
)

var shellCommandPolicy = struct {
	broadInspectionHeads    []string
	narrowingHeads          []string
	exploreHeads            []string
	readOnlyChildPrefixes   []string
	workbranchChildPrefixes []string
	childDeniedFragments    []string
}{
	broadInspectionHeads: []string{"ls", "find", "tree"},
	narrowingHeads:       []string{"rg", "grep", "awk", "sed", "cat", "head", "tail"},
	exploreHeads:         []string{"ls", "find", "rg", "grep", "awk", "sed", "cat", "head", "tail", "wc", "file", "pwd", "tree"},
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
		analysis.ExploreRejection = "command is required"
		analysis.ActionClass = autonomy.ActionClassGenericProbe
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
	analysis.Verification = shellCommandVerification(command)
	analysis.FullSuite = analysis.Verification == ShellVerificationFullSuite

	if !analysis.HasControlSyntax && readOnlyShellCommand(analysis.Head, analysis.Args, analysis.Verification) {
		analysis.SideEffect = autonomy.SideEffectReadOnly
	} else {
		analysis.SideEffect = knownWorkspaceShellMutationCommand(command)
	}

	analysis.ExploreRejection = shellExploreRejection(analysis)
	analysis.ExploreAllowed = analysis.ExploreRejection == ""
	analysis.ActionClass = shellCommandActionClass(analysis)
	return analysis
}

func knownWorkspaceShellMutationCommand(command string) autonomy.SideEffectClass {
	classified := autonomy.SideEffectUnknown
	for _, segment := range splitShellSegments(command) {
		fields := shellCommandFields(segment)
		if len(fields) == 0 {
			continue
		}
		sideEffect := knownWorkspaceShellMutation(normalizeShellHead(fields[0]), lowerShellArgs(fields[1:]))
		if sideEffect == autonomy.SideEffectDestructive {
			return sideEffect
		}
		if sideEffect == autonomy.SideEffectAdditive {
			classified = sideEffect
		}
	}
	return classified
}

func knownWorkspaceShellMutation(head string, args []string) autonomy.SideEffectClass {
	switch head {
	case "touch", "mkdir", "cp", "install":
		return autonomy.SideEffectAdditive
	case "rm", "rmdir", "mv", "truncate":
		return autonomy.SideEffectDestructive
	case "gofmt":
		for _, arg := range args {
			if arg == "-w" {
				return autonomy.SideEffectDestructive
			}
		}
	case "sed":
		for _, arg := range args {
			if arg == "-i" || strings.HasPrefix(arg, "-i") {
				return autonomy.SideEffectDestructive
			}
		}
	}
	return autonomy.SideEffectUnknown
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

func shellExploreRejection(analysis ShellCommandAnalysis) string {
	if len(analysis.Fields) == 0 {
		return "command is required"
	}

	if analysis.HasControlSyntax {
		return "redirection, command substitution, control operators, and multiline shell are not allowed"
	}

	if analysis.Head == "git" {
		if len(analysis.Args) == 0 {
			return "git command requires a read-only subcommand"
		}
		if readOnlyGitArgs(analysis.Args) {
			return ""
		}

		if analysis.Args[0] == "diff" && gitDiffArgsWriteOutput(analysis.Args[1:]) {
			return "git diff output files are not allowed in explore mode"
		}

		return fmt.Sprintf("git %s is not allowed in explore mode", analysis.Args[0])
	}

	if !shellHeadInSet(analysis.Head, shellCommandPolicy.exploreHeads...) {
		return fmt.Sprintf("%s is not an allowed explore-mode shell command", analysis.Head)
	}

	if !exploreInspectionArgsAreReadOnly(analysis.Head, analysis.Args) {
		return fmt.Sprintf("%s arguments are not allowed in explore mode", analysis.Head)
	}

	return ""
}

func shellCommandActionClass(analysis ShellCommandAnalysis) autonomy.ActionClass {
	if analysis.Verification != ShellVerificationNone {
		return autonomy.ActionClassVerify
	}
	if analysis.SideEffect == autonomy.SideEffectReadOnly || analysis.ExploreAllowed {
		return autonomy.ActionClassRead
	}

	return autonomy.ActionClassGenericProbe
}

func shellCommandBuildOrTest(command string) bool {
	for _, part := range splitShellSegments(command) {
		fields := shellCommandFields(part)
		if len(fields) == 0 {
			continue
		}

		head := normalizeShellHead(fields[0])
		if head == "python" || head == "python3" {
			if pythonShellVerification(fields[1:]) != ShellVerificationNone {
				return true
			}
			continue
		}
		if shellHeadIsBuildOrTest(head, lowerShellArgs(fields[1:])) {
			return true
		}
	}

	return false
}

func shellCommandVerification(command string) ShellVerificationFamily {
	command = strings.TrimSpace(command)
	if command == "" || shellCommandHasControlSyntax(command) {
		return ShellVerificationNone
	}

	fields := shellCommandFields(command)
	if len(fields) == 0 {
		return ShellVerificationNone
	}

	head := normalizeShellHead(fields[0])
	args := fields[1:]
	lowerArgs := lowerShellArgs(args)

	switch head {
	case "go":
		return goShellVerification(args)
	case "git":
		if len(lowerArgs) > 0 && lowerArgs[0] == "diff" && shellArgListContains(lowerArgs[1:], "--check") {
			return ShellVerificationCheck
		}
	case "cargo":
		if len(lowerArgs) == 0 {
			return ShellVerificationNone
		}
		switch lowerArgs[0] {
		case "test":
			return ShellVerificationTest
		case "build":
			return ShellVerificationBuild
		case "check", "clippy":
			return ShellVerificationCheck
		}
	case "make":
		if len(lowerArgs) == 0 {
			return ShellVerificationNone
		}
		switch lowerArgs[0] {
		case "test":
			return ShellVerificationTest
		case "build":
			return ShellVerificationBuild
		case "check", "lint":
			return ShellVerificationCheck
		}
	case "npm", "pnpm", "yarn":
		return packageManagerShellVerification(lowerArgs)
	case "python", "python3":
		return pythonShellVerification(args)
	case "pytest", "tox", "ctest":
		return ShellVerificationTest
	case "scons", "ninja", "meson", "cmake", "godot":
		return ShellVerificationBuild
	case "mvn", "gradle", "gradlew":
		return buildToolShellVerification(lowerArgs)
	case "bash", "sh", "zsh":
		if shellArgListContains(lowerArgs, "-n") {
			return ShellVerificationScriptSyntax
		}
	case "shellcheck":
		return ShellVerificationScriptSyntax
	case "ruff":
		if len(lowerArgs) > 0 && lowerArgs[0] == "check" {
			return ShellVerificationCheck
		}
	case "tsc", "eslint":
		return ShellVerificationGeneric
	}

	return ShellVerificationNone
}

func goShellVerification(args []string) ShellVerificationFamily {
	subcommand, targets, ok := goVerificationInvocation(args)
	if !ok {
		return ShellVerificationNone
	}

	switch subcommand {
	case "test":
		for _, target := range targets {
			if strings.TrimSpace(target) == "./..." && !goVerificationPreventsFullSuite(args) {
				return ShellVerificationFullSuite
			}
		}
		return ShellVerificationTest
	case "build":
		return ShellVerificationBuild
	case "vet":
		return ShellVerificationGeneric
	default:
		return ShellVerificationNone
	}
}

func goVerificationInvocation(args []string) (string, []string, bool) {
	if len(args) == 0 {
		return "", nil, false
	}

	subcommand := strings.ToLower(strings.TrimSpace(args[0]))
	if subcommand != "test" && subcommand != "build" && subcommand != "vet" {
		return "", nil, false
	}

	targets := make([]string, 0, len(args)-1)
	for i := 1; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "-args" {
			break
		}
		if strings.HasPrefix(arg, "-") {
			consumesValue, known := goVerificationFlag(arg)
			if !known {
				return "", nil, false
			}
			if !strings.Contains(arg, "=") && consumesValue {
				if i+1 >= len(args) {
					return "", nil, false
				}
				i++
			}
			continue
		}
		targets = append(targets, arg)
	}
	return subcommand, targets, true
}

func goVerificationFlag(flag string) (bool, bool) {
	flag = strings.ToLower(strings.TrimSpace(flag))
	if i := strings.IndexByte(flag, '='); i >= 0 {
		flag = flag[:i]
	}
	switch flag {
	case "-asmflags", "-bench", "-benchtime", "-blockprofile", "-blockprofilerate", "-buildmode", "-compiler", "-count", "-covermode", "-coverpkg", "-coverprofile", "-cpu", "-cpuprofile", "-exec", "-fuzz", "-fuzzminimizetime", "-fuzztime", "-gccgoflags", "-gcflags", "-installsuffix", "-ldflags", "-list", "-memprofile", "-memprofilerate", "-mod", "-modfile", "-mutexprofile", "-mutexprofilefraction", "-o", "-outputdir", "-overlay", "-p", "-parallel", "-pkgdir", "-run", "-shuffle", "-skip", "-tags", "-timeout", "-toolexec", "-trace", "-vet":
		return true, true
	case "-a", "-asan", "-buildvcs", "-c", "-cover", "-failfast", "-fullpath", "-i", "-json", "-msan", "-n", "-race", "-short", "-trimpath", "-v", "-work", "-x":
		return false, true
	default:
		return false, false
	}
}

func goVerificationPreventsFullSuite(args []string) bool {
	for i, arg := range args[1:] {
		flag := strings.ToLower(strings.TrimSpace(arg))
		value := ""
		if i := strings.IndexByte(flag, '='); i >= 0 {
			value = flag[i+1:]
			flag = flag[:i]
		}
		switch flag {
		case "-bench", "-c", "-exec", "-fuzz", "-list", "-n", "-run", "-short", "-skip":
			return true
		case "-count":
			if value == "0" || (value == "" && i+2 < len(args) && strings.TrimSpace(args[i+2]) == "0") {
				return true
			}
		}
	}
	return false
}

func goVerificationPreventsPathCoverage(args []string) bool {
	if len(args) == 0 {
		return false
	}
	subcommand := strings.ToLower(strings.TrimSpace(args[0]))
	for i, arg := range args[1:] {
		flag := strings.ToLower(strings.TrimSpace(arg))
		value := ""
		if split := strings.IndexByte(flag, '='); split >= 0 {
			value = flag[split+1:]
			flag = flag[:split]
		}
		switch flag {
		case "-n":
			return true
		case "-c", "-exec", "-list":
			return subcommand == "test"
		case "-count":
			if subcommand != "test" {
				continue
			}
			if value == "0" || (value == "" && i+2 < len(args) && strings.TrimSpace(args[i+2]) == "0") {
				return true
			}
		}
	}
	return false
}

func goVerificationNonExecutionReason(args []string) string {
	if len(args) == 0 {
		return ""
	}
	subcommand := strings.ToLower(strings.TrimSpace(args[0]))
	for i, arg := range args[1:] {
		flag := strings.ToLower(strings.TrimSpace(arg))
		value := ""
		if split := strings.IndexByte(flag, '='); split >= 0 {
			value = flag[split+1:]
			flag = flag[:split]
		}
		switch flag {
		case "-n":
			return "go " + subcommand + " -n is a dry run"
		case "-c":
			if subcommand == "test" {
				return "go test -c compiles a test binary without running tests"
			}
		case "-exec":
			if subcommand == "test" {
				return "go test -exec delegates test-binary execution without producer evidence"
			}
		case "-list":
			if subcommand == "test" {
				return "go test -list lists tests without running them"
			}
		case "-count":
			if subcommand == "test" && (value == "0" || (value == "" && i+2 < len(args) && strings.TrimSpace(args[i+2]) == "0")) {
				return "go test -count=0 runs zero tests"
			}
		}
	}
	return ""
}

func packageManagerShellVerification(args []string) ShellVerificationFamily {
	if len(args) == 0 {
		return ShellVerificationNone
	}
	switch args[0] {
	case "test":
		return ShellVerificationTest
	case "build":
		return ShellVerificationBuild
	case "run":
		if len(args) < 2 {
			return ShellVerificationNone
		}
		switch args[1] {
		case "test":
			return ShellVerificationTest
		case "build":
			return ShellVerificationBuild
		case "check", "lint":
			return ShellVerificationCheck
		}
	}
	return ShellVerificationNone
}

func pythonShellVerification(args []string) ShellVerificationFamily {
	if len(args) == 0 {
		return ShellVerificationNone
	}

	for index := 0; index < len(args); index++ {
		arg := strings.TrimSpace(strings.Trim(args[index], `"'`))
		if arg == "" {
			continue
		}

		switch {
		case arg == "-m":
			if index+1 >= len(args) {
				return ShellVerificationNone
			}
			return pythonModuleShellVerification(args[index+1], args[index+2:])
		case strings.HasPrefix(arg, "-m") && len(arg) > len("-m"):
			return pythonModuleShellVerification(strings.TrimPrefix(arg, "-m"), args[index+1:])
		case arg == "-c" || strings.HasPrefix(arg, "-c"):
			// The command string, rather than later argv, owns the executed
			// program. Never treat quoted or unused argv as verification.
			return ShellVerificationNone
		case arg == "--":
			if index+1 >= len(args) {
				return ShellVerificationNone
			}
			return pythonScriptShellVerification(args[index+1])
		case pythonInterpreterOptionStopsExecution(arg):
			return ShellVerificationNone
		case pythonInterpreterOptionConsumesValue(arg):
			if arg == "-W" || arg == "-X" || arg == "--check-hash-based-pycs" {
				if index+1 >= len(args) {
					return ShellVerificationNone
				}
				index++
			}
			continue
		case pythonInterpreterBooleanOption(arg):
			continue
		case strings.HasPrefix(arg, "-"):
			// Unknown interpreter options make the execution shape
			// ambiguous. Fail closed instead of scanning later argv for
			// verification-looking text.
			return ShellVerificationNone
		default:
			// The first non-option is the executed script. Its arguments do
			// not participate in verification classification.
			return pythonScriptShellVerification(arg)
		}
	}

	return ShellVerificationNone
}

func pythonModuleShellVerification(module string, args []string) ShellVerificationFamily {
	module = strings.ToLower(strings.TrimSpace(strings.Trim(module, `"'`)))
	switch module {
	case "pytest", "unittest":
		if len(args) == 0 {
			return ShellVerificationFullSuite
		}
		return ShellVerificationTest
	case "build":
		return ShellVerificationBuild
	default:
		return ShellVerificationNone
	}
}

func pythonScriptShellVerification(script string) ShellVerificationFamily {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(strings.Trim(script, `"'`))))
	if !strings.HasSuffix(base, ".py") {
		return ShellVerificationNone
	}
	switch {
	case strings.Contains(base, "build"):
		return ShellVerificationBuild
	case strings.Contains(base, "test"):
		return ShellVerificationTest
	default:
		return ShellVerificationNone
	}
}

func pythonInterpreterOptionStopsExecution(arg string) bool {
	switch arg {
	case "-h", "--help", "-V", "--version":
		return true
	default:
		return false
	}
}

func pythonInterpreterOptionConsumesValue(arg string) bool {
	if arg == "-W" || arg == "-X" || arg == "--check-hash-based-pycs" {
		return true
	}
	return (strings.HasPrefix(arg, "-W") && len(arg) > len("-W")) ||
		(strings.HasPrefix(arg, "-X") && len(arg) > len("-X"))
}

func pythonInterpreterBooleanOption(arg string) bool {
	switch arg {
	case "-b", "-bb", "-B", "-d", "-E", "-i", "-I", "-O", "-OO", "-P", "-q", "-R", "-s", "-S", "-u", "-v", "-vv", "-x":
		return true
	default:
		return false
	}
}

func buildToolShellVerification(args []string) ShellVerificationFamily {
	for _, arg := range args {
		switch arg {
		case "test":
			return ShellVerificationTest
		case "check", "lint":
			return ShellVerificationCheck
		case "build", "compile", "package", "verify":
			return ShellVerificationBuild
		}
	}
	return ShellVerificationBuild
}

func shellArgListContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func shellCommandHasControlSyntax(command string) bool {
	if shellCommandHasUnquotedComment(command) {
		return true
	}

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

func shellCommandHasUnquotedComment(command string) bool {
	var quote byte
	escaped := false
	wordStart := true

	for i := 0; i < len(command); i++ {
		ch := command[i]
		if escaped {
			escaped = false
			wordStart = false
			continue
		}
		if quote != 0 {
			if ch == quote {
				quote = 0
			} else if ch == '\\' && quote == '"' {
				escaped = true
			}
			continue
		}
		if ch == '\\' {
			escaped = true
			wordStart = false
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			wordStart = false
			continue
		}
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			wordStart = true
			continue
		}
		if ch == '#' && wordStart {
			return true
		}
		wordStart = false
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

func shellCommandFields(command string) []string {
	var (
		fields []string
		b      strings.Builder
		quote  rune
	)

	escaped := false
	for _, r := range command {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}

		if r == '\\' {
			if quote == '\'' {
				b.WriteRune(r)
			} else {
				escaped = true
			}
			continue
		}

		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
			continue
		}

		if r == '\'' || r == '"' {
			quote = r
			continue
		}

		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if b.Len() > 0 {
				fields = append(fields, b.String())
				b.Reset()
			}
			continue
		}

		b.WriteRune(r)
	}

	if escaped {
		b.WriteRune('\\')
	}

	if b.Len() > 0 {
		fields = append(fields, b.String())
	}

	return fields
}

func readOnlyShellCommand(head string, args []string, verification ShellVerificationFamily) bool {
	switch head {
	case "pwd", "ls", "find", "rg", "grep", "cat", "head", "tail", "wc", "file", "tree":
		return exploreInspectionArgsAreReadOnly(head, args)
	case "git":
		return readOnlyGitArgs(args)
	case "go":
		return readOnlyGoArgs(args)
	case "make":
		return len(args) > 0 && shellHeadInSet(args[0], "test", "build")
	case "npm":
		return len(args) > 0 && args[0] == "test"
	case "cargo":
		return len(args) > 0 && args[0] == "test"
	case "python", "python3":
		return verification == ShellVerificationTest || verification == ShellVerificationFullSuite
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

func readOnlyGoArgs(args []string) bool {
	if len(args) == 0 {
		return false
	}

	switch args[0] {
	case "test", "build", "list":
		return true
	case "env":
		for _, arg := range args[1:] {
			if arg == "-w" || arg == "-u" {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func readOnlyGitArgs(args []string) bool {
	if len(args) == 0 {
		return false
	}

	switch args[0] {
	case "status":
		return true
	case "diff":
		return !gitDiffArgsWriteOutput(args[1:])
	case "log", "show", "grep", "rev-parse":
		return true
	case "branch":
		return readOnlyGitBranchArgs(args[1:])
	default:
		return false
	}
}

func gitDiffArgsWriteOutput(args []string) bool {
	for _, arg := range args {
		if arg == "--output" || strings.HasPrefix(arg, "--output=") {
			return true
		}
	}

	return false
}

func readOnlyGitBranchArgs(args []string) bool {
	for _, arg := range args {
		if arg == "" {
			continue
		}
		if !strings.HasPrefix(arg, "-") {
			return false
		}
		switch arg {
		case "-d", "-D", "-m", "-M", "-c", "-C":
			return false
		}
		if strings.HasPrefix(arg, "--delete") ||
			strings.HasPrefix(arg, "--move") ||
			strings.HasPrefix(arg, "--copy") ||
			strings.HasPrefix(arg, "--set-upstream-to") ||
			strings.HasPrefix(arg, "--unset-upstream") ||
			strings.HasPrefix(arg, "--edit-description") {

			return false
		}
	}

	return true
}

func exploreInspectionArgsAreReadOnly(head string, args []string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, "--output") {
			return false
		}
	}

	switch head {
	case "find":
		for _, arg := range args {
			if shellHeadInSet(arg, "-delete", "-exec", "-execdir", "-ok", "-okdir") {
				return false
			}
		}
	case "sed":
		for _, arg := range args {
			if arg == "-i" || strings.HasPrefix(arg, "-i.") || strings.HasPrefix(arg, "-i'") || strings.HasPrefix(arg, `-i"`) {
				return false
			}
		}
	case "tree":
		for _, arg := range args {
			if arg == "-o" {
				return false
			}
		}
	}

	return true
}

func shellHeadInSet(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}

	return false
}
