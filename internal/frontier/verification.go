package frontier

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"qwen-bubbles/internal/semantic"
	"qwen-bubbles/internal/workspace"
)

type VerificationPlan struct {
	Commands         []string
	Reasons          []string
	UsesFullSuite    bool
	Coverage         string
	Confidence       string
	RiskLevel        string
	ExpectedFailures []string
	RepairPolicy     string
}

func BuildVerificationPlan(mgr *workspace.Manager, changedPaths []string) VerificationPlan {
	plan := VerificationPlan{
		Commands: make([]string, 0, 8),
		Reasons:  make([]string, 0, 8),
	}
	if mgr == nil {
		return plan
	}
	root := strings.TrimSpace(mgr.Root())
	if root == "" {
		return plan
	}

	hasGoModule := false
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
		hasGoModule = true
	}

	paths := UniqueNormalizedPaths(changedPaths)
	if len(paths) == 0 {
		if hasGoModule {
			plan.Commands = append(plan.Commands, "go build ./...", "go test -vet=off ./...")
			plan.Reasons = append(plan.Reasons, "no changed-path diff was available, so verification fell back to the module-wide baseline")
			plan.UsesFullSuite = true
			plan.Coverage = "module-wide baseline"
			plan.Confidence = "medium"
			plan.RiskLevel = "unknown"
			plan.ExpectedFailures = []string{"compile errors", "test regressions"}
			plan.RepairPolicy = "repair failing build or test output before reporting success"
		}
		return plan
	}

	var idx *semantic.GoIndex
	if hasGoModule {
		if built, err := semantic.BuildGoIndex(root); err == nil {
			idx = built
		}
		plan.Commands = append(plan.Commands, "go build ./...")
		plan.Reasons = append(plan.Reasons, "always compile the full Go workspace after a bounded edit")
	}

	seenCommands := map[string]struct{}{"go build ./...": {}}
	seenDirs := make(map[string]struct{})
	topDirs := UniqueTopLevelDirs(paths)
	needsFullSuite := len(topDirs) > 1 || len(paths) > 3
	if needsFullSuite {
		plan.Reasons = append(plan.Reasons, "changed scope crosses multiple areas or files, so verification escalates to the full suite")
	}

	addCommand := func(command string, reason string) {
		command = strings.TrimSpace(command)
		if command == "" {
			return
		}
		if _, ok := seenCommands[command]; ok {
			return
		}
		seenCommands[command] = struct{}{}
		plan.Commands = append(plan.Commands, command)
		if strings.TrimSpace(reason) != "" {
			plan.Reasons = append(plan.Reasons, reason)
		}
	}
	addGoTestDir := func(dir string, reason string) {
		dir = filepath.ToSlash(strings.TrimSpace(dir))
		if dir == "" {
			dir = "."
		}
		if _, ok := seenDirs[dir]; ok {
			return
		}
		seenDirs[dir] = struct{}{}
		addCommand(GoTestCommandForDir(dir), reason)
	}

	for _, path := range paths {
		switch {
		case strings.HasSuffix(path, ".go"):
			dir := filepath.ToSlash(filepath.Dir(path))
			file := (*semantic.GoFile)(nil)
			if idx != nil {
				file = idx.FileByPath(path)
				if file != nil {
					dir = file.PathDir()
				}
			}
			if dir == "" {
				dir = "."
			}
			addGoTestDir(dir, "target the changed Go package first")
			if idx != nil && file != nil && !file.IsTest {
				for _, importer := range GoImporterFiles(idx, dir, 4) {
					importerPath := strings.TrimSpace(strings.Split(importer, " imports ")[0])
					importerDir := filepath.ToSlash(filepath.Dir(importerPath))
					if importerDir == "" {
						importerDir = "."
					}
					addGoTestDir(importerDir, "cover importer packages that depend on the changed Go package")
				}
				if IsCoreGoImpactPath(path) || IsEntrypointGoPath(path, file) {
					needsFullSuite = true
				}
			}
		case path == "config.json" || path == "config.example.json" || strings.HasPrefix(path, "prompts/"):
			if hasGoModule {
				addGoTestDir("internal/app", "config and prompt changes affect runtime routing and prompt assembly")
				needsFullSuite = true
			}
		case strings.HasSuffix(path, ".sh"):
			addCommand("bash -n "+path, "shell changes should at least pass syntax verification")
			if strings.HasPrefix(path, "scripts/") {
				needsFullSuite = true
			}
		case strings.HasSuffix(path, ".md"):
			// docs-only change: no targeted command beyond any baseline build/test already selected
		default:
			if hasGoModule {
				needsFullSuite = true
			}
		}
	}

	if hasGoModule && needsFullSuite {
		addCommand("go test -vet=off ./...", "blast radius is broad enough that the full Go suite should run")
		plan.UsesFullSuite = true
	}

	plan.Coverage, plan.Confidence = SummarizeVerificationConfidence(plan)
	plan.RiskLevel = VerificationRiskLevel(paths, plan)
	plan.ExpectedFailures = VerificationExpectedFailures(paths, plan)
	plan.RepairPolicy = VerificationRepairPolicy(plan)

	return plan
}

func VerificationRiskLevel(paths []string, plan VerificationPlan) string {
	topDirs := UniqueTopLevelDirs(paths)
	switch {
	case plan.UsesFullSuite || len(topDirs) > 1 || len(paths) > 3:
		return "high"
	case PathsContainRuntimePolicySurface(paths):
		return "high"
	case ContainsPathSuffix(paths, ".go"):
		return "medium"
	case ContainsPathSuffix(paths, ".sh"):
		return "medium"
	case OnlyPathSuffix(paths, ".md"):
		return "low"
	default:
		return "medium"
	}
}

func VerificationExpectedFailures(paths []string, plan VerificationPlan) []string {
	failures := make([]string, 0, 4)
	if ContainsPathSuffix(paths, ".go") || plan.UsesFullSuite {
		failures = append(failures, "compile errors", "package test regressions")
	}
	if PathsContainRuntimePolicySurface(paths) {
		failures = append(failures, "routing or prompt assembly regressions")
	}
	if ContainsPathSuffix(paths, ".sh") {
		failures = append(failures, "shell syntax errors")
	}
	if len(failures) == 0 {
		failures = append(failures, "unexpected integration regressions")
	}
	return UniqueNonEmptyStrings(failures)
}

func VerificationRepairPolicy(plan VerificationPlan) string {
	switch {
	case plan.UsesFullSuite:
		return "repair all failing full-suite checks before reporting success"
	case plan.Confidence == "high":
		return "repair targeted or importer failures before widening scope"
	case plan.Confidence == "medium":
		return "repair targeted failures, then consider full-suite escalation if behavior changed"
	case plan.Confidence == "low":
		return "treat passing checks as smoke coverage only and report residual risk"
	default:
		return "report that verification coverage is unavailable"
	}
}

func ClassifyVerificationFailure(command string, result workspace.ToolResult) []string {
	combined := strings.ToLower(strings.Join([]string{command, result.Summary, result.Output, result.Error}, "\n"))
	commandLower := strings.ToLower(strings.TrimSpace(command))
	var classes []string
	add := func(value string) {
		if strings.TrimSpace(value) != "" {
			classes = append(classes, value)
		}
	}
	switch {
	case strings.Contains(combined, "declared and not used"):
		add("go_unused_variable")
	case strings.Contains(combined, "undefined:"):
		add("go_undefined_symbol")
	case strings.Contains(combined, "import cycle not allowed"):
		add("go_import_cycle")
	case strings.Contains(combined, "no required module provides package") || strings.Contains(combined, "cannot find package"):
		add("go_missing_package")
	case strings.Contains(commandLower, "bash -n"):
		add("shell_syntax_error")
	case strings.Contains(combined, "expected 'package'") || strings.Contains(combined, "syntax error"):
		if strings.Contains(combined, ".go") || strings.Contains(commandLower, "go ") {
			add("go_syntax_error")
		} else {
			add("syntax_error")
		}
	case strings.Contains(combined, "--- fail:") || strings.Contains(combined, "fail\t") || strings.Contains(combined, "failed"):
		add("test_failure")
	case strings.TrimSpace(result.Error) != "":
		add("verification_command_failure")
	}
	if len(classes) == 0 && strings.TrimSpace(result.Error) != "" {
		add("unknown_verification_failure")
	}
	return UniqueNonEmptyStrings(classes)
}

func ClassifyVerificationFailures(commands []string, results []workspace.ToolResult) []string {
	var classes []string
	for i, result := range results {
		if strings.TrimSpace(result.Error) == "" {
			continue
		}
		command := result.Tool
		if i < len(commands) && strings.TrimSpace(commands[i]) != "" {
			command = commands[i]
		}
		classes = append(classes, ClassifyVerificationFailure(command, result)...)
	}
	return UniqueNonEmptyStrings(classes)
}

func PathsContainRuntimePolicySurface(paths []string) bool {
	for _, path := range paths {
		switch {
		case path == "config.json", path == "config.example.json":
			return true
		case strings.HasPrefix(path, "prompts/"):
			return true
		case strings.HasPrefix(path, "internal/app/"):
			return true
		}
	}
	return false
}

func ContainsPathSuffix(paths []string, suffix string) bool {
	for _, path := range paths {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

func OnlyPathSuffix(paths []string, suffix string) bool {
	if len(paths) == 0 {
		return false
	}
	for _, path := range paths {
		if !strings.HasSuffix(path, suffix) {
			return false
		}
	}
	return true
}

func SummarizeVerificationConfidence(plan VerificationPlan) (string, string) {
	hasBuild := false
	goTestCount := 0
	syntaxChecks := 0
	for _, command := range plan.Commands {
		switch {
		case command == "go build ./...":
			hasBuild = true
		case strings.HasPrefix(command, "go test "):
			goTestCount++
		case strings.HasPrefix(command, "bash -n "):
			syntaxChecks++
		}
	}
	switch {
	case plan.UsesFullSuite:
		return "full-suite escalation", "high"
	case goTestCount > 1 && hasBuild:
		return "targeted package and importer verification", "high"
	case goTestCount >= 1 && hasBuild:
		return "targeted package verification", "medium"
	case goTestCount >= 1:
		return "targeted test verification", "medium"
	case syntaxChecks > 0 && !hasBuild:
		return "syntax-only verification", "low"
	case hasBuild:
		return "compile-only verification", "low"
	default:
		return "", ""
	}
}

func IsCoreGoImpactPath(path string) bool {
	return strings.HasPrefix(path, "internal/app/") ||
		strings.HasPrefix(path, "internal/agent/") ||
		strings.HasPrefix(path, "internal/workspace/") ||
		strings.HasPrefix(path, "internal/llama/")
}

func IsEntrypointGoPath(path string, file *semantic.GoFile) bool {
	if strings.HasPrefix(path, "cmd/") {
		return true
	}
	if file == nil {
		return false
	}
	return file.HasMain || strings.HasSuffix(path, "/main.go") || path == "main.go"
}

func GoImportPath(pkgPath string, module string) string {
	pkgPath = filepath.ToSlash(strings.TrimSpace(pkgPath))
	module = strings.TrimSpace(module)
	if module == "" {
		return pkgPath
	}
	if pkgPath == "." {
		return module
	}
	return module + "/" + pkgPath
}

func GoImporterFiles(idx *semantic.GoIndex, pkgDir string, limit int) []string {
	if idx == nil || limit <= 0 {
		return nil
	}
	target := GoImportPath(pkgDir, idx.Module)
	lines := make([]string, 0, limit)
	for _, file := range idx.Files {
		for _, imp := range file.Imports {
			if imp != target {
				continue
			}
			lines = append(lines, fmt.Sprintf("%s imports %s", file.Path, pkgDir))
			break
		}
		if len(lines) >= limit {
			break
		}
	}
	sort.Strings(lines)
	return lines
}

func GoTestFilesForDir(idx *semantic.GoIndex, dir string, limit int) []string {
	if idx == nil || limit <= 0 {
		return nil
	}
	tests := make([]string, 0, limit)
	for _, file := range idx.Files {
		if !file.IsTest || file.PathDir() != dir {
			continue
		}
		tests = append(tests, file.Path)
		if len(tests) >= limit {
			break
		}
	}
	sort.Strings(tests)
	return tests
}

func GoTestCommandForDir(dir string) string {
	dir = filepath.ToSlash(strings.TrimSpace(dir))
	if dir == "" || dir == "." {
		return "go test -vet=off ./..."
	}
	return "go test -vet=off ./" + dir
}

func UniqueTopLevelDirs(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = NormalizePath(path)
		if path == "" {
			continue
		}
		top := strings.Split(path, "/")[0]
		if top == "" {
			top = "."
		}
		if _, ok := seen[top]; ok {
			continue
		}
		seen[top] = struct{}{}
		out = append(out, top)
	}
	sort.Strings(out)
	return out
}
