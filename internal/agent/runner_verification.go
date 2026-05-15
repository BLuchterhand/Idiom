package agent

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

func resultCountsAsSuccessfulVerification(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Error) != "" {
		return false
	}
	if strings.TrimSpace(result.Tool) == "source_sanity" {
		return true
	}
	if isDiagnosticsVerificationTool(result.Tool) {
		return true
	}
	return strings.TrimSpace(result.Tool) == "shell" &&
		isVerificationCommandText(result.Output)
}

func sourceEditVerificationRejection(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (summary string, errorText string, reject bool) {
	if !sourceVerificationGateApplies(brief, results) {
		return "", "", false
	}
	latest, ok := latestVerificationAfterLatestSourceWrite(results)
	if !ok || !verificationResultIsPatchHygieneOnly(latest) {
		return "", "", false
	}
	if isVerificationRequest(req) || workspace.IsWorkspaceWriteTool(req.Tool) || sourceVerificationBoundaryAllowsTool(req.Tool) {
		return "", "", false
	}
	if !workspace.IsBroadInspectionRequest(req) && !workspace.IsNarrowingRequest(req) {
		return "", "", false
	}
	paths := changedSourcePaths(results)
	if len(paths) == 0 {
		return "", "", false
	}
	if plannerOwnsVerification(brief) {
		return "Source edit verification required",
			fmt.Sprintf("source edits are present in %s, but the latest successful check is only patch hygiene (`git diff --check`) or no source-aware check at all. Required evidence is source-sanity on the changed source path; the harness routes it to language diagnostics when available and falls back to generic source syntax sanity. If source sanity is unavailable, answer with the concrete lane-sanity blocker before doing more reads.", summarizeRunnerPaths(paths, 3)),
			true
	}
	return "Source edit verification required",
		fmt.Sprintf("source edits are present in %s, but the latest successful check is only patch hygiene (`git diff --check`) or no source-aware check at all. Required evidence is source-sanity on the changed source path; the harness routes it to language diagnostics when available and falls back to generic source syntax sanity. A focused source-aware build/test or concrete verification blocker also satisfies this contract before more reads.", summarizeRunnerPaths(paths, 3)),
		true
}

func sourceEditCloseoutPrompt(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, bool) {
	if !sourceEditsAreSemanticallyVerified(brief, results) {
		return "", false
	}
	if postReadinessGap(req) != "" {
		return "", false
	}
	if isVerificationRequest(req) || workspace.IsWorkspaceWriteTool(req.Tool) || sourceVerificationBoundaryAllowsTool(req.Tool) {
		return "", false
	}
	if !workspace.IsBroadInspectionRequest(req) && !workspace.IsNarrowingRequest(req) {
		return "", false
	}
	paths := changedSourcePaths(results)
	if len(paths) == 0 {
		return "", false
	}
	return fmt.Sprintf("Successful source edits are already followed by source-aware verification for %s. The attempted read/search did not name an exact missing claim with `named_gap`, so tools are now disabled. Write the implementation result now: changed paths, verification status, and one concrete blocker only if the verification evidence shows one.", summarizeRunnerPaths(paths, 3)), true
}

func sourceEditsNeedSemanticVerification(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !sourceVerificationGateApplies(brief, results) {
		return false
	}
	latest, ok := latestVerificationAfterLatestSourceWrite(results)
	if !ok {
		return true
	}
	return verificationResultIsPatchHygieneOnly(latest)
}

func sourceVerificationBoundaryAllowsTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "task_ledger", "harness_feedback", "idiom_feedback":
		return true
	default:
		return false
	}
}

func sourceEditsAreSemanticallyVerified(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !sourceVerificationGateApplies(brief, results) {
		return false
	}
	latest, ok := latestVerificationAfterLatestSourceWrite(results)
	return ok && !verificationResultIsPatchHygieneOnly(latest)
}

func sourceVerificationGateApplies(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !runnerBriefRequiresMutation(brief) || verificationContractSkipsGate(brief.VerificationContract) {
		return false
	}
	return len(changedSourcePaths(results)) > 0
}

func latestVerificationAfterLatestSourceWrite(results []workspace.ToolResult) (workspace.ToolResult, bool) {
	var latest workspace.ToolResult
	found := false
	seenSourceWrite := false
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		if toolResultChangedSourcePath(result) {
			seenSourceWrite = true
			found = false
			latest = workspace.ToolResult{}
			continue
		}
		if seenSourceWrite && resultCountsAsSuccessfulVerification(result) {
			latest = result
			found = true
		}
	}
	return latest, found
}

func verificationResultIsPatchHygieneOnly(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Tool) != "shell" {
		return false
	}
	return shellOutputIsPatchHygieneOnly(result.Output)
}

func shellOutputIsPatchHygieneOnly(output string) bool {
	hasPatchHygiene := false
	hasSourceAware := false
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "$"))
		lower := strings.ToLower(line)
		if strings.Contains(lower, "git diff --check") {
			hasPatchHygiene = true
			continue
		}
		if line != "" && isBuildOrTestShellCommand(line) {
			hasSourceAware = true
		}
	}
	return hasPatchHygiene && !hasSourceAware
}

func changedSourcePaths(results []workspace.ToolResult) []string {
	paths := make([]string, 0)
	seen := make(map[string]struct{})
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		for _, path := range changedPathsFromToolResult(result) {
			normalized := normalizeRunnerPath(path)
			if normalized == "" || !sourcePathNeedsSemanticVerification(normalized) {
				continue
			}
			if _, ok := seen[normalized]; ok {
				continue
			}
			seen[normalized] = struct{}{}
			paths = append(paths, normalized)
		}
	}
	slices.Sort(paths)
	return paths
}

func toolResultChangedSourcePath(result workspace.ToolResult) bool {
	for _, path := range changedPathsFromToolResult(result) {
		if sourcePathNeedsSemanticVerification(path) {
			return true
		}
	}
	return false
}

func sourcePathNeedsSemanticVerification(path string) bool {
	if _, ok := workspace.LanguageForFile(path); ok {
		return true
	}
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".gd":
		return true
	default:
		return false
	}
}

func summarizeRunnerPaths(paths []string, limit int) string {
	if len(paths) == 0 {
		return "edited source files"
	}
	if limit <= 0 || limit > len(paths) {
		limit = len(paths)
	}
	quoted := make([]string, 0, limit+1)
	for _, path := range paths[:limit] {
		quoted = append(quoted, "`"+path+"`")
	}
	if len(paths) > limit {
		quoted = append(quoted, fmt.Sprintf("%d more", len(paths)-limit))
	}
	return strings.Join(quoted, ", ")
}

func isVerificationCommandText(text string) bool {
	command := strings.ToLower(strings.TrimSpace(text))
	if command == "" {
		return false
	}
	verificationSnippets := []string{
		"go test", "go vet", "go build",
		"git diff --check",
		"npm test", "npm run test", "npm run build",
		"pnpm test", "pnpm run test", "pnpm build",
		"yarn test", "yarn build",
		"pytest", "python -m pytest", "python3 -m pytest",
		"cargo test", "cargo check", "cargo build",
		"make test", "make check",
		"ctest", "mvn test", "gradle test",
		"tsc", "ruff check", "eslint",
	}
	for _, snippet := range verificationSnippets {
		if strings.Contains(command, snippet) {
			return true
		}
	}
	for _, line := range strings.Split(command, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "$"))
		if line != "" && isBuildOrTestShellCommand(line) {
			return true
		}
	}
	return false
}

func plannerOwnedVerificationShellRejection(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief) (summary string, errorText string, reject bool) {
	if strings.TrimSpace(req.Tool) != "shell" || !plannerOwnsVerification(brief) {
		return "", "", false
	}
	command := strings.TrimSpace(req.Command)
	if command == "" || !isBuildOrTestShellCommand(command) {
		return "", "", false
	}
	return "Planner-owned verification shell rejected",
		"this implementation lane delegates build/test verification to the caller. Source edits require source-sanity evidence; git diff --check is only patch-hygiene evidence. The other contract outcome is changed paths plus the concrete planner-owned verification status.",
		true
}

func verificationRequiredAfterWritesMessage(brief workerruntime.WorkerRuntimeBrief, writesSinceVerification int) (summary string, errorText string) {
	summary = "Verification required after workspace edits"
	if plannerOwnsVerification(brief) {
		return summary, fmt.Sprintf(
			"the workspace has %d successful write(s) since the last lane sanity check. This route delegates build/test verification to the planner; do not run build or test commands. Source edits require source-sanity evidence routed by the harness to language diagnostics when available and generic syntax sanity otherwise; git diff --check is only patch-hygiene evidence. The other contract outcome is changed paths plus the concrete planner-owned verification status.",
			writesSinceVerification,
		)
	}
	return summary, fmt.Sprintf("the workspace has %d successful write(s) since the last verification. Required evidence is a focused verification command (for example package tests, diagnostics, or the relevant build/test command) before more inspection or edits, or the concrete blocker.", writesSinceVerification)
}

func plannerOwnsVerification(brief workerruntime.WorkerRuntimeBrief) bool {
	if !runnerBriefRequiresMutation(brief) {
		return false
	}
	for _, constraint := range brief.Constraints {
		switch strings.ToLower(strings.TrimSpace(constraint)) {
		case "planner_owned_verification", "planner-owned-verification", "planner_owned_acceptance", "planner-owned-acceptance":
			return true
		}
	}
	return false
}

func isBuildOrTestShellCommand(command string) bool {
	for _, segment := range shellCommandClassificationSegments(command) {
		fields := strings.Fields(segment)
		if len(fields) == 0 {
			continue
		}
		head := strings.TrimPrefix(strings.ToLower(filepath.Base(fields[0])), "./")
		if shellHeadIsBuildOrTest(head, fields[1:]) {
			return true
		}
	}
	return false
}

func shellHeadIsBuildOrTest(head string, args []string) bool {
	switch head {
	case "scons", "ctest", "ninja", "meson", "pytest", "tox", "godot":
		return true
	case "make", "cmake", "mvn", "gradle", "gradlew":
		return true
	case "go":
		return len(args) > 0 && runnerStringInSet(args[0], "test", "build", "vet")
	case "cargo":
		return len(args) > 0 && runnerStringInSet(args[0], "test", "build", "check", "clippy")
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
	first := strings.ToLower(strings.TrimSpace(args[0]))
	if runnerStringInSet(first, "test", "build") {
		return true
	}
	return first == "run" && len(args) > 1 && runnerStringInSet(args[1], "test", "build", "check", "lint")
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
			if strings.HasPrefix(module, "pytest") || runnerStringInSet(module, "unittest", "build") {
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

func shellCommandClassificationSegments(command string) []string {
	replacer := strings.NewReplacer("&&", "|", "||", "|", ";", "|")
	normalized := replacer.Replace(strings.TrimSpace(command))
	parts := strings.Split(normalized, "|")
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			segments = append(segments, part)
		}
	}
	return segments
}

func runnerStringInSet(value string, candidates ...string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}

func isDiagnosticsVerificationTool(tool string) bool {
	return workspace.IsDiagnosticToolName(tool)
}

func clearVerificationGateFingerprints(fingerprints map[string]string) {
	for fingerprint, reason := range fingerprints {
		if strings.Contains(reason, "Verification required after workspace edits") {
			delete(fingerprints, fingerprint)
		}
	}
}

func clearRunnerStringIntMap(values map[string]int) {
	for key := range values {
		delete(values, key)
	}
}
