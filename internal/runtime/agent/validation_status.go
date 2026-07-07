package agent

import (
	"fmt"
	"strings"

	"idiom/internal/capabilities/workspace"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/textutil"
	semanticlanguage "idiom/internal/repository/semantic/language"
	"idiom/internal/runtime/workerruntime"
)

type validationStatusQuestionShape struct {
	AsksVerification bool
	AsksBuild        bool
	AsksTestAdded    bool
}

func validationStatusQuestionForBrief(brief workerruntime.WorkerRuntimeBrief) validationStatusQuestionShape {
	text := strings.ToLower(strings.Join(strings.Fields(brief.Objective), " "))
	if text == "" {
		return validationStatusQuestionShape{}
	}

	shape := validationStatusQuestionShape{}
	shape.AsksVerification = ContainsAny(text,
		"did you verify", "did you validate", "did you test",
		"have you verified", "have you validated", "have you tested",
		"was this verified", "was this validated", "verification status",
	)
	shape.AsksBuild = ContainsAny(text,
		"still builds", "it builds", "build still", "verify that it builds",
		"validated that it builds", "build passes", "build passed",
		"go build", "compile", "compiles",
	)
	shape.AsksTestAdded = ContainsAny(text,
		"added a test", "add a test", "added tests", "add tests",
		"test to cover", "tests to cover", "test coverage", "covered by a test",
	)

	return shape
}

func (s validationStatusQuestionShape) Active() bool {
	return s.AsksVerification || s.AsksBuild || s.AsksTestAdded
}

func (r *Runner) validationStatusAnswerRejectionReason(answer string, results []workspace.ToolResult) string {
	if r == nil {
		return ""
	}

	shape := validationStatusQuestionForBrief(r.RuntimeBrief)
	if !shape.Active() {
		return ""
	}

	lower := strings.ToLower(strings.Join(strings.Fields(answer), " "))
	if strings.TrimSpace(lower) == "" {
		return ""
	}

	if shape.AsksBuild {
		if !validationAnswerMentionsBuildStatus(lower) {
			return "the user asked whether the change still builds, but the answer did not state whether a build/test command was run."
		}
		if validationAnswerClaimsBuildPassed(lower) && !hasSuccessfulBuildOrTestVerification(results) {
			return "the answer claims build/test verification passed, but the tool ledger has no successful build or test command."
		}
	}

	if shape.AsksTestAdded {
		if !validationAnswerMentionsTestStatus(lower) {
			return "the user asked whether a test was added, but the answer did not state whether any test path changed."
		}
		if validationAnswerClaimsTestAdded(lower) && !hasChangedTestPath(results) {
			return "the answer claims a test was added, but the tool ledger has no successful write to a test path."
		}
	}

	if validationAnswerClaimsBroadVerificationPassed(lower) && !hasSuccessfulVerificationResult(results) {
		return "the answer claims verification passed, but the tool ledger has no successful verification result."
	}
	if validationAnswerClaimsSourceSanityPassed(lower) && !hasObservedSourceSanityResult(results) {
		return "the answer claims source_sanity passed, but the tool ledger has no source_sanity result."
	}

	return ""
}

func (r *Runner) validationStatusAnswerCanUseLedgerEvidence(answer string, results []workspace.ToolResult) bool {
	if r == nil || !validationStatusQuestionForBrief(r.RuntimeBrief).Active() {
		return false
	}

	return r.validationStatusAnswerRejectionReason(answer, results) == "" &&
		(validationAnswerMentionsValidationLedger(strings.ToLower(answer)) || hasObservedVerificationResult(results) || len(changedPathsFromToolResults(results)) > 0)
}

func validationStatusPathBackedByLedger(rawPath string, canonicalPath string, results []workspace.ToolResult) bool {
	candidates := map[string]struct{}{}
	for _, path := range []string{rawPath, canonicalPath} {
		if normalized := NormalizeRunnerPath(path); normalized != "" {
			candidates[normalized] = struct{}{}
		}
	}
	if len(candidates) == 0 {
		return false
	}

	for _, path := range validationStatusEvidenceLedgerPaths(results) {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if _, ok := candidates[path]; ok {
			return true
		}
	}

	return false
}

func (r *Runner) validationStatusTerminalRecoveryAnswer(results []workspace.ToolResult) (string, bool) {
	if r == nil {
		return "", false
	}

	shape := validationStatusQuestionForBrief(r.RuntimeBrief)
	if !shape.Active() {
		return "", false
	}

	var lines []string

	if summary, ok := latestObservedVerificationSummary(results); ok {
		lines = append(lines, fmt.Sprintf("I only have this verification evidence: %s.", summary))
	} else {
		lines = append(lines, "I do not have a successful verification result in the tool ledger.")
	}

	if shape.AsksBuild {
		if summary, ok := latestSuccessfulBuildOrTestVerificationSummary(results); ok {
			lines = append(lines, fmt.Sprintf("A build/test command did run: %s.", summary))
		} else {
			lines = append(lines, "I did not run a build or test command, so I did not verify that the project still builds.")
		}
	}

	if shape.AsksTestAdded {
		if paths := changedTestPaths(results); len(paths) > 0 {
			lines = append(lines, "A test path changed: "+quotedRunnerStrings(paths)+".")
		} else {
			lines = append(lines, "I did not add or change a test path.")
		}
	}

	if paths := validationStatusEvidencePaths(results); len(paths) > 0 {
		lines = append(lines, "Ledger evidence: "+quotedRunnerStrings(paths)+".")
	}

	return strings.Join(lines, " "), true
}

func validationAnswerMentionsBuildStatus(lower string) bool {
	return ContainsAny(lower,
		"build", "builds", "built", "compile", "compiles", "compiled",
		"go test", "go build", "test command", "build command",
	)
}

func validationAnswerMentionsTestStatus(lower string) bool {
	return ContainsAny(lower, "test", "tests", "coverage")
}

func validationAnswerMentionsValidationLedger(lower string) bool {
	return ContainsAny(lower,
		"verification", "verified", "validate", "validated", "source_sanity",
		"source sanity", "build", "test", "coverage", "changed path",
	)
}

func validationAnswerClaimsBuildPassed(lower string) bool {
	if validationAnswerNegatesNear(lower, "build") ||
		validationAnswerNegatesNear(lower, "go build") ||
		validationAnswerNegatesNear(lower, "test command") ||
		validationAnswerNegatesNear(lower, "build command") {
		return false
	}

	return ContainsAny(lower,
		"still builds", "build passed", "build passes", "build succeeded",
		"build succeeds", "go build passed", "compiled successfully",
		"tests passed", "test passed", "go test passed",
	)
}

func validationAnswerClaimsTestAdded(lower string) bool {
	if validationAnswerNegatesNear(lower, "test") || validationAnswerNegatesNear(lower, "coverage") {
		return false
	}

	return ContainsAny(lower,
		"added a test", "added tests", "test added", "tests added",
		"created a test", "new test", "test coverage added",
	)
}

func validationAnswerClaimsBroadVerificationPassed(lower string) bool {
	if validationAnswerNegatesNear(lower, "verified") ||
		validationAnswerNegatesNear(lower, "validated") ||
		validationAnswerNegatesNear(lower, "verification") {
		return false
	}

	return ContainsAny(lower,
		"verified", "validated", "verification passed",
	)
}

func validationAnswerClaimsSourceSanityPassed(lower string) bool {
	if validationAnswerNegatesNear(lower, "source_sanity") ||
		validationAnswerNegatesNear(lower, "source sanity") {
		return false
	}

	return ContainsAny(lower,
		"source_sanity passed", "source sanity passed", "source sanity succeeded",
	)
}

func validationAnswerNegatesNear(lower string, term string) bool {
	term = strings.TrimSpace(strings.ToLower(term))
	if term == "" {
		return false
	}

	idx := strings.Index(lower, term)
	if idx < 0 {
		return false
	}

	start := idx - 40
	if start < 0 {
		start = 0
	}
	window := lower[start : idx+len(term)]

	return ContainsAny(window, "no ", "not ", "did not ", "didn't ", "was not ", "wasn't ", "without ")
}

func hasSuccessfulBuildOrTestVerification(results []workspace.ToolResult) bool {
	_, ok := latestSuccessfulBuildOrTestVerificationSummary(results)
	return ok
}

func latestSuccessfulBuildOrTestVerificationSummary(results []workspace.ToolResult) (string, bool) {
	var latest string
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Tool) != workspace.ToolNameShell {
			if strings.TrimSpace(result.Error) == "" && workspace.IsWorkspaceWriteTool(result.Tool) {
				latest = ""
			}
			continue
		}

		command := workspace.ShellCommandFromToolResult(result)
		if command == "" || !workspace.AnalyzeShellCommand(command).BuildOrTest {
			continue
		}

		latest = command + " passed"
	}

	if latest == "" {
		return "", false
	}
	return latest, true
}

func hasChangedTestPath(results []workspace.ToolResult) bool {
	return len(changedTestPaths(results)) > 0
}

func changedTestPaths(results []workspace.ToolResult) []string {
	var paths []string
	for _, path := range mapKeys(changedPathsFromToolResults(results)) {
		if plancontract.PathLooksLikeTest(path) {
			paths = textutil.AppendUnique(paths, path)
		}
	}
	return paths
}

func hasObservedVerificationResult(results []workspace.ToolResult) bool {
	_, ok := latestObservedVerificationSummary(results)
	return ok
}

func hasObservedSourceSanityResult(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Error) == "" && strings.TrimSpace(result.Tool) == workspace.ToolNameSourceSanity {
			return true
		}
	}

	return false
}

func latestObservedVerificationSummary(results []workspace.ToolResult) (string, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		if resultCountsAsSuccessfulVerification(result) {
			if command := workspace.ShellCommandFromToolResult(result); command != "" {
				return command + " passed", true
			}
			if summary := strings.TrimSpace(result.Summary); summary != "" {
				return summary, true
			}
			return "verification passed", true
		}

		if strings.TrimSpace(result.Tool) == workspace.ToolNameSourceSanity || semanticlanguage.IsDiagnosticToolName(result.Tool) {
			if summary := strings.TrimSpace(result.Summary); summary != "" {
				return summary, true
			}
			return strings.TrimSpace(result.Tool) + " completed", true
		}
	}

	return "", false
}

func validationStatusEvidencePaths(results []workspace.ToolResult) []string {
	var paths []string
	for _, path := range validationStatusEvidenceLedgerPaths(results) {
		paths = textutil.AppendUnique(paths, path)
	}
	return closurePromptEvidencePathsPreservingTests(paths, 8)
}

func validationStatusEvidenceLedgerPaths(results []workspace.ToolResult) []string {
	var paths []string
	add := func(path string) {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" {
			return
		}
		paths = textutil.AppendUnique(paths, normalized)
	}

	for _, path := range changedPathListFromToolResults(results) {
		add(path)
	}

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		if strings.TrimSpace(result.Tool) != workspace.ToolNameSourceSanity && !semanticlanguage.IsDiagnosticToolName(result.Tool) {
			continue
		}
		for _, path := range ReadPathsFromToolResult(result) {
			add(path)
		}
		for _, path := range sourceGroundingMentionedPaths(result.Summary + "\n" + result.Output) {
			add(path)
		}
	}

	return paths
}
