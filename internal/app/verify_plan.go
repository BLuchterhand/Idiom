package app

import (
	"os"
	"path/filepath"
	"strings"

	"qwen-bubbles/internal/semantic"
	"qwen-bubbles/internal/workspace"
)

type verificationPlan struct {
	Commands         []string
	Reasons          []string
	UsesFullSuite    bool
	Coverage         string
	Confidence       string
	RiskLevel        string
	ExpectedFailures []string
	RepairPolicy     string
}

func implementationVerificationPlan(mgr *workspace.Manager, changedPaths []string) verificationPlan {
	plan := verificationPlan{
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

	paths := uniqueNormalizedPaths(changedPaths)
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
	topDirs := uniqueTopLevelDirs(paths)
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
		addCommand(goTestCommandForDir(dir), reason)
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
				for _, importer := range goImporterFiles(idx, dir, 4) {
					importerPath := strings.TrimSpace(strings.Split(importer, " imports ")[0])
					importerDir := filepath.ToSlash(filepath.Dir(importerPath))
					if importerDir == "" {
						importerDir = "."
					}
					addGoTestDir(importerDir, "cover importer packages that depend on the changed Go package")
				}
				if isCoreGoImpactPath(path) || isEntrypointGoPath(path, file) {
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

	plan.Coverage, plan.Confidence = summarizeVerificationConfidence(plan)
	plan.RiskLevel = verificationRiskLevel(paths, plan)
	plan.ExpectedFailures = verificationExpectedFailures(paths, plan)
	plan.RepairPolicy = verificationRepairPolicy(plan)

	return plan
}

func verificationRiskLevel(paths []string, plan verificationPlan) string {
	topDirs := uniqueTopLevelDirs(paths)
	switch {
	case plan.UsesFullSuite || len(topDirs) > 1 || len(paths) > 3:
		return "high"
	case pathsContainRuntimePolicySurface(paths):
		return "high"
	case containsPathSuffix(paths, ".go"):
		return "medium"
	case containsPathSuffix(paths, ".sh"):
		return "medium"
	case onlyPathSuffix(paths, ".md"):
		return "low"
	default:
		return "medium"
	}
}

func verificationExpectedFailures(paths []string, plan verificationPlan) []string {
	failures := make([]string, 0, 4)
	if containsPathSuffix(paths, ".go") || plan.UsesFullSuite {
		failures = append(failures, "compile errors", "package test regressions")
	}
	if pathsContainRuntimePolicySurface(paths) {
		failures = append(failures, "routing or prompt assembly regressions")
	}
	if containsPathSuffix(paths, ".sh") {
		failures = append(failures, "shell syntax errors")
	}
	if len(failures) == 0 {
		failures = append(failures, "unexpected integration regressions")
	}
	return uniqueNonEmptyStrings(failures)
}

func verificationRepairPolicy(plan verificationPlan) string {
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

func pathsContainRuntimePolicySurface(paths []string) bool {
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

func containsPathSuffix(paths []string, suffix string) bool {
	for _, path := range paths {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

func onlyPathSuffix(paths []string, suffix string) bool {
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

func summarizeVerificationConfidence(plan verificationPlan) (string, string) {
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

func uniqueNormalizedPaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = normalizeGuardedPath(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}
