package workspace

import (
	pathpkg "path"
	"strings"

	"idiom/internal/contracts/pathparse"
	"idiom/internal/contracts/toolfacts"
	semanticlanguage "idiom/internal/repository/semantic/language"
)

type VerificationCoverage string

const (
	VerificationCoverageNone                        VerificationCoverage = ""
	VerificationCoverageFullSuite                   VerificationCoverage = "full_suite"
	VerificationCoveragePathScope                   VerificationCoverage = "path_scope"
	VerificationCoverageUnresolved                  VerificationCoverage = "unresolved"
	VerificationCoverageDependencyClosureUnresolved VerificationCoverage = "dependency_closure_unresolved"
	VerificationCoverageExactPaths                  VerificationCoverage = "exact_paths"
	VerificationCoverageExactHTMLPaths              VerificationCoverage = "exact_html_paths"
)

// VerificationEcosystem limits path scopes to artifacts a verifier can inspect.
type VerificationEcosystem string

const (
	VerificationEcosystemCrossArtifact VerificationEcosystem = "cross_artifact"
	VerificationEcosystemGo            VerificationEcosystem = "go"
	VerificationEcosystemRust          VerificationEcosystem = "rust"
	VerificationEcosystemJavaScript    VerificationEcosystem = "javascript"
	VerificationEcosystemPython        VerificationEcosystem = "python"
	VerificationEcosystemJVM           VerificationEcosystem = "jvm"
	VerificationEcosystemNative        VerificationEcosystem = "native"
	VerificationEcosystemShell         VerificationEcosystem = "shell"
)

// VerificationObservation is the workspace-owned classification of whether a
// tool request/result represents verification evidence. Runtime layers consume
// these facts; lane policy still decides when verification is required.
//
// AnalyzeVerificationResult reports whether the check itself succeeded.
// AnalyzePostWriteVerificationResult additionally requires relevant coverage:
// a successful full suite or an authoritative scope that contains at least one
// pending path. CoveredPendingPaths is always the exact subset established by
// that evidence, so callers must not clear unrelated pending writes.
// FullSuite means full only within Ecosystem and ScopePaths; it never means all
// pending artifacts across a polyglot workspace.
type VerificationObservation struct {
	Attempted                     bool
	Successful                    bool
	Command                       string
	Verification                  ShellVerificationFamily
	Coverage                      VerificationCoverage
	Ecosystem                     VerificationEcosystem
	Workdir                       string
	ScopePaths                    []string
	CoveredPendingPaths           []string
	RelevantToPendingWrites       bool
	FullSuite                     bool
	BuildOrTest                   bool
	PatchHygieneOnly              bool
	HTMLSmokeForPendingHTMLWrites bool
}

func AnalyzeVerificationRequest(req ToolRequest) VerificationObservation {
	tool := strings.TrimSpace(req.Tool)
	if tool == ToolNameSourceSanity || semanticlanguage.IsDiagnosticToolName(tool) {
		observed := VerificationObservation{Attempted: true}
		observed.ScopePaths = normalizeVerificationScopes([]string{req.Path})
		observed.Coverage = verificationPathCoverage(observed.ScopePaths)
		return observed
	}
	if tool != ToolNameShell {
		return VerificationObservation{}
	}
	if observed := analyzeCanonicalHTMLVerificationRequest(req); observed.Attempted {
		return observed
	}
	return analyzeShellVerificationCommand(strings.TrimSpace(req.Command), strings.TrimSpace(req.Workdir))
}

func analyzeCanonicalHTMLVerificationRequest(req ToolRequest) VerificationObservation {
	command := strings.TrimSpace(req.Command)
	path, ok := canonicalHTMLParserPath(command)
	if !ok {
		return VerificationObservation{}
	}

	workdir := normalizeVerificationScope(req.Workdir)
	var scopes []string
	if workdir != "" {
		scopes = verificationScopesInWorkdir([]string{path}, workdir)
	}
	return VerificationObservation{
		Attempted:    true,
		Command:      command,
		Verification: ShellVerificationCheck,
		Coverage:     VerificationCoverageExactHTMLPaths,
		Workdir:      workdir,
		ScopePaths:   scopes,
	}
}

func AnalyzeVerificationResult(result ToolResult) VerificationObservation {
	tool := strings.TrimSpace(result.Tool)
	if tool == ToolNameSourceSanity {
		observed := VerificationObservation{
			Attempted:  true,
			Successful: strings.TrimSpace(result.Error) == "" && sourceSanityResultUsesLanguageDiagnostics(result),
		}
		return applyResultVerificationScopes(observed, result)
	}
	if semanticlanguage.IsDiagnosticToolName(tool) {
		observed := VerificationObservation{
			Attempted:  true,
			Successful: strings.TrimSpace(result.Error) == "",
		}
		return applyResultVerificationScopes(observed, result)
	}
	if tool != ToolNameShell {
		return VerificationObservation{}
	}
	if observed := analyzeStructuredShellVerificationEvidence(result); observed.Attempted {
		return observed
	}

	observed := analyzeShellVerificationFacts(result)
	if observed.Attempted {
		observed = applyShellResultVerificationScopes(observed, result)
		observed.Successful = strings.TrimSpace(result.Error) == ""
	}
	return observed
}

func AnalyzePostWriteVerificationResult(result ToolResult, pendingPaths []string) VerificationObservation {
	observed := AnalyzeVerificationResult(result)
	pending := normalizeVerificationScopes(pendingPaths)
	checkSucceeded := observed.Successful

	if observed.Attempted {
		observed.CoveredPendingPaths = verificationCoveredPendingPaths(observed, pending)
		observed.RelevantToPendingWrites = len(observed.CoveredPendingPaths) > 0
		if observed.Coverage == VerificationCoverageExactHTMLPaths {
			observed.HTMLSmokeForPendingHTMLWrites = len(observed.CoveredPendingPaths) > 0
		}
		observed.Successful = checkSucceeded && observed.RelevantToPendingWrites
		return observed
	}
	return observed
}

func analyzeStructuredShellVerificationEvidence(result ToolResult) VerificationObservation {
	if result.Facts == nil {
		return VerificationObservation{}
	}

	var htmlPaths []string
	var gitDiffPaths []string
	for _, evidence := range result.Facts.VerificationEvidence {
		switch evidence.Kind {
		case toolfacts.VerificationEvidenceHTMLParse:
			for _, evidencePath := range normalizeVerificationScopes(evidence.Paths) {
				ext := strings.ToLower(pathpkg.Ext(evidencePath))
				if ext == ".html" || ext == ".htm" {
					htmlPaths = append(htmlPaths, evidencePath)
				}
			}
		case toolfacts.VerificationEvidenceGitDiffCheck:
			gitDiffPaths = append(gitDiffPaths, normalizeVerificationScopes(evidence.Paths)...)
		}
	}
	if paths := normalizeVerificationScopes(htmlPaths); len(paths) > 0 {
		return VerificationObservation{
			Attempted:  true,
			Successful: strings.TrimSpace(result.Error) == "",
			Command:    strings.TrimSpace(result.Facts.ShellCommand),
			Coverage:   VerificationCoverageExactHTMLPaths,
			Workdir:    normalizeVerificationScope(result.Facts.ShellWorkdir),
			ScopePaths: paths,
		}
	}
	if paths := normalizeVerificationScopes(gitDiffPaths); len(paths) > 0 {
		return VerificationObservation{
			Attempted:        true,
			Successful:       strings.TrimSpace(result.Error) == "",
			Command:          strings.TrimSpace(result.Facts.ShellCommand),
			Verification:     ShellVerificationCheck,
			Coverage:         VerificationCoverageExactPaths,
			Ecosystem:        VerificationEcosystemCrossArtifact,
			Workdir:          normalizeVerificationScope(result.Facts.ShellWorkdir),
			ScopePaths:       paths,
			PatchHygieneOnly: true,
		}
	}
	return VerificationObservation{}
}

func analyzeShellVerificationCommand(command string, workdir string) VerificationObservation {
	analysis := AnalyzeShellCommand(command)
	if analysis.Verification == ShellVerificationNone {
		return VerificationObservation{}
	}
	observed := VerificationObservation{
		Attempted:        true,
		Command:          command,
		Verification:     analysis.Verification,
		Ecosystem:        shellVerificationEcosystem(command),
		FullSuite:        analysis.FullSuite,
		BuildOrTest:      analysis.BuildOrTest,
		PatchHygieneOnly: shellVerificationCommandIsPatchHygieneOnly(command, analysis),
	}
	workdir = normalizeVerificationScope(workdir)
	observed.Workdir = workdir
	if workdir != "" {
		observed.ScopePaths = verificationScopesInWorkdir(shellVerificationScopePaths(command, analysis), workdir)
	}
	observed.Coverage = shellVerificationCoverage(analysis, observed.ScopePaths)
	return observed
}

func analyzeShellVerificationFacts(result ToolResult) VerificationObservation {
	if result.Facts == nil {
		return VerificationObservation{}
	}

	command := strings.TrimSpace(result.Facts.ShellCommand)
	if command == "" {
		return VerificationObservation{}
	}

	return analyzeShellVerificationCommand(command, strings.TrimSpace(result.Facts.ShellWorkdir))
}

func shellVerificationCommandIsPatchHygieneOnly(command string, analysis ShellCommandAnalysis) bool {
	return standaloneGitDiffCheckCommand(command) && !analysis.BuildOrTest
}

func applyResultVerificationScopes(observed VerificationObservation, result ToolResult) VerificationObservation {
	scopes := append([]string(nil), observed.ScopePaths...)
	scopes = append(scopes, result.Paths...)
	if result.Facts != nil {
		scopes = append(scopes, result.Facts.DiagnosticEvidencePaths...)
	}
	observed.ScopePaths = normalizeVerificationScopes(scopes)
	observed.Coverage = verificationPathCoverage(observed.ScopePaths)
	return observed
}

func applyShellResultVerificationScopes(observed VerificationObservation, result ToolResult) VerificationObservation {
	if observed.PatchHygieneOnly {
		// Only the shell producer knows which tracked working-tree paths Git
		// actually inspected. In particular, argv pathspecs may name untracked
		// files that `git diff --check` silently ignores. Without producer-owned
		// evidence, keep coverage unresolved even if a caller attached generic
		// result paths.
		observed.ScopePaths = nil
		observed.Coverage = VerificationCoverageUnresolved
		return observed
	}

	scopes := append([]string(nil), observed.ScopePaths...)
	scopes = append(scopes, result.Paths...)
	if result.Facts != nil {
		scopes = append(scopes, result.Facts.DiagnosticEvidencePaths...)
	}
	observed.ScopePaths = normalizeVerificationScopes(scopes)
	observed.Coverage = shellVerificationCoverage(AnalyzeShellCommand(observed.Command), observed.ScopePaths)
	return observed
}

func verificationPathCoverage(scopes []string) VerificationCoverage {
	if len(scopes) == 0 {
		return VerificationCoverageUnresolved
	}
	return VerificationCoveragePathScope
}

func shellVerificationCoverage(analysis ShellCommandAnalysis, scopes []string) VerificationCoverage {
	if analysis.FullSuite {
		return VerificationCoverageFullSuite
	}
	if analysis.Verification == ShellVerificationBuild {
		// A build target can compile transitive dependencies outside its direct
		// path scope, but the shell result does not currently carry that closure.
		// Keep the uncertainty explicit instead of claiming repository-wide
		// coverage. Producer-owned result paths can extend ScopePaths later.
		return VerificationCoverageDependencyClosureUnresolved
	}
	if len(scopes) > 0 {
		return VerificationCoveragePathScope
	}
	return VerificationCoverageUnresolved
}

func verificationCoveredPendingPaths(observed VerificationObservation, pending []string) []string {
	covered := make([]string, 0, len(pending))
	for _, pendingPath := range pending {
		if !verificationEcosystemCoversPath(observed.Ecosystem, pendingPath) {
			continue
		}
		for _, scope := range observed.ScopePaths {
			matches := verificationScopeContainsPath(scope, pendingPath)
			if observed.Coverage == VerificationCoverageExactHTMLPaths || observed.Coverage == VerificationCoverageExactPaths {
				matches = normalizeVerificationScope(scope) == normalizeVerificationScope(pendingPath)
			}
			if matches {
				covered = append(covered, pendingPath)
				break
			}
		}
	}
	return covered
}

func verificationScopeContainsPath(scope string, pendingPath string) bool {
	scope = normalizeVerificationScope(scope)
	pendingPath = normalizeVerificationScope(pendingPath)
	if scope == "" || pendingPath == "" {
		return false
	}
	if scope == "." || scope == pendingPath {
		return true
	}
	return strings.HasPrefix(pendingPath, scope+"/")
}

func normalizeVerificationScopes(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	normalized := make([]string, 0, len(paths))
	for _, candidate := range paths {
		candidate = normalizeVerificationScope(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		normalized = append(normalized, candidate)
	}
	return normalized
}

func normalizeVerificationScope(value string) string {
	value = strings.Trim(strings.TrimSpace(value), "`'\"")
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "\\", "/")
	value = strings.TrimSuffix(value, "/...")
	if value == "." || value == "./" || value == "./..." || value == "..." {
		return "."
	}

	value = strings.TrimPrefix(value, "./")
	value = pathpkg.Clean(value)
	if value == "." {
		return "."
	}
	return pathparse.NormalizeWorkspaceRelativePath(value)
}

func verificationScopesInWorkdir(scopes []string, workdir string) []string {
	workdir = normalizeVerificationScope(workdir)
	if workdir == "" {
		return nil
	}

	resolved := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		scope = normalizeVerificationScope(scope)
		if scope == "" {
			continue
		}
		if workdir == "." {
			resolved = append(resolved, scope)
		} else if scope == "." {
			resolved = append(resolved, workdir)
		} else {
			resolved = append(resolved, pathpkg.Join(workdir, scope))
		}
	}
	return normalizeVerificationScopes(resolved)
}

func shellVerificationEcosystem(command string) VerificationEcosystem {
	fields := shellCommandFields(command)
	if len(fields) == 0 {
		return ""
	}
	switch normalizeShellHead(fields[0]) {
	case "git":
		return VerificationEcosystemCrossArtifact
	case "go":
		return VerificationEcosystemGo
	case "cargo":
		return VerificationEcosystemRust
	case "npm", "pnpm", "yarn", "tsc", "eslint":
		return VerificationEcosystemJavaScript
	case "python", "python3", "pytest", "tox", "ruff":
		return VerificationEcosystemPython
	case "mvn", "gradle", "gradlew":
		return VerificationEcosystemJVM
	case "scons", "ninja", "meson", "cmake", "ctest", "godot":
		return VerificationEcosystemNative
	case "bash", "sh", "zsh", "shellcheck":
		return VerificationEcosystemShell
	default:
		return ""
	}
}

func verificationEcosystemCoversPath(ecosystem VerificationEcosystem, candidate string) bool {
	candidate = normalizeVerificationScope(candidate)
	if candidate == "" {
		return false
	}
	if ecosystem == "" || ecosystem == VerificationEcosystemCrossArtifact {
		return true
	}

	base := strings.ToLower(pathpkg.Base(candidate))
	ext := strings.ToLower(pathpkg.Ext(candidate))
	switch ecosystem {
	case VerificationEcosystemGo:
		return ext == ".go" || base == "go.mod" || base == "go.sum" || base == "go.work"
	case VerificationEcosystemRust:
		return ext == ".rs" || base == "cargo.toml" || base == "cargo.lock"
	case VerificationEcosystemJavaScript:
		return ext == ".js" || ext == ".jsx" || ext == ".mjs" || ext == ".cjs" ||
			ext == ".ts" || ext == ".tsx" || ext == ".mts" || ext == ".cts" ||
			base == "package.json" || base == "package-lock.json" || base == "pnpm-lock.yaml" || base == "yarn.lock"
	case VerificationEcosystemPython:
		return ext == ".py" || ext == ".pyi" || base == "pyproject.toml" || base == "poetry.lock" || strings.HasPrefix(base, "requirements")
	case VerificationEcosystemJVM:
		return ext == ".java" || ext == ".kt" || ext == ".kts" || base == "pom.xml" || strings.HasPrefix(base, "build.gradle")
	case VerificationEcosystemNative:
		return ext == ".c" || ext == ".cc" || ext == ".cpp" || ext == ".cxx" || ext == ".h" || ext == ".hh" || ext == ".hpp" || ext == ".hxx"
	case VerificationEcosystemShell:
		return ext == ".sh" || ext == ".bash" || ext == ".zsh"
	default:
		return false
	}
}

func shellVerificationScopePaths(command string, analysis ShellCommandAnalysis) []string {
	if analysis.FullSuite {
		return []string{"."}
	}

	fields := shellCommandFields(command)
	if len(fields) == 0 {
		return nil
	}

	head := normalizeShellHead(fields[0])
	args := fields[1:]
	var scopes []string

	switch head {
	case "go":
		if !goVerificationPreventsPathCoverage(args) {
			scopes = goVerificationScopePaths(args)
		}
	case "bash", "sh", "zsh":
		scopes = scriptSyntaxScopePaths(args)
	case "shellcheck":
		scopes = positionalVerificationScopePaths(args)
	case "ruff":
		if len(args) > 0 && strings.EqualFold(strings.TrimSpace(args[0]), "check") {
			scopes = positionalVerificationScopePaths(args[1:])
		}
	case "eslint":
		scopes = positionalVerificationScopePaths(args)
	case "tsc":
		scopes = []string{"."}
	}

	return normalizeVerificationScopes(scopes)
}

func goVerificationScopePaths(args []string) []string {
	_, targets, ok := goVerificationInvocation(args)
	if !ok {
		return nil
	}

	scopes := make([]string, 0, len(targets))
	for _, arg := range targets {
		if scope := normalizeVerificationScope(arg); scope != "" {
			scopes = append(scopes, scope)
		}
	}
	return scopes
}

func scriptSyntaxScopePaths(args []string) []string {
	for i, arg := range args {
		if strings.TrimSpace(arg) == "-n" {
			return positionalVerificationScopePaths(args[i+1:])
		}
	}
	return nil
}

func positionalVerificationScopePaths(args []string) []string {
	scopes := make([]string, 0, len(args))
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" || strings.HasPrefix(arg, "-") {
			continue
		}
		if scope := normalizeVerificationScope(arg); scope != "" {
			scopes = append(scopes, scope)
		}
	}
	return scopes
}

func sourceSanityResultUsesLanguageDiagnostics(result ToolResult) bool {
	text := strings.ToLower(strings.TrimSpace(result.Summary + "\n" + result.Output))
	if text == "" {
		return false
	}

	if strings.Contains(text, "generic fallback") ||
		strings.Contains(text, "generic_source_sanity") ||
		strings.Contains(text, "generic delimiter") ||
		strings.Contains(text, "generic source sanity") {

		return false
	}

	for _, tool := range semanticlanguage.DiagnosticToolNames() {
		if strings.Contains(text, strings.ToLower(strings.TrimSpace(tool))) {
			return true
		}
	}

	return false
}
