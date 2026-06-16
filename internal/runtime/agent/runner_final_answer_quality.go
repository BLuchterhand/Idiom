package agent

import (
	"fmt"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/runtime/workerruntime"
	"strings"
)

func conversationLaneWithoutSemanticEval(brief workerruntime.WorkerRuntimeBrief) bool {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if !readOnlyDirectAnswerTaskFamily(brief.TaskFamily) ||
		!workerruntime.RuntimeContractIsDirectAnswer(brief.Contract) {

		return false
	}

	return !readOnlyDirectAnswerLane(brief) || strings.TrimSpace(brief.Objective) == ""
}

// emptyAnswerRetryPrompt builds the retry system message when the model
// produces an empty answer. The prompt re-anchors to the user's original
// objective so the model reorients rather than repeating its mistake.
func emptyAnswerRetryPrompt(brief workerruntime.WorkerRuntimeBrief) string {
	base := "Your previous response was empty. Tools are disabled now — do NOT emit a tool block. "
	if obj := strings.TrimSpace(brief.Objective); obj != "" {
		base += fmt.Sprintf("The user asked: %q. ", obj)
	}

	base += "Reply with a direct, non-empty answer based on the evidence already gathered. Keep it short " +
		"(a few sentences or a brief list). No preamble like \"Based on...\" or \"Here is...\" — just the answer. " +
		"If one exact claim remains unsupported, state that uncertainty after the supported answer in one sentence."

	return base
}

func finishShouldOverrideSourceEcho(answer string) bool {
	if !looksLikeSourceEchoOnlyAnswer(answer) {
		return false
	}

	lower := strings.ToLower(answer)
	if !strings.Contains(lower, "no files were changed") {
		return false
	}

	for _, path := range pathparse.MentionedWorkspacePaths(answer) {
		path = normalizeRunnerPath(path)
		if strings.HasPrefix(path, "internal/") || strings.HasPrefix(path, "logs/") {
			return true
		}
	}

	return false
}

// cleanToolRequestPathDisplay returns a short display value for a tool
// request path suitable for progress nudges and event logs.
func cleanToolRequestPathDisplay(path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return "."
	}

	return p
}

// looksLikeNonAnswerProse checks whether the answer text is clearly a
// non-answer (tool-call markup, echoed user question, tool-failure
// language without substantive findings) rather than an actual answer.
// These patterns indicate the model emitted transport-level noise
// instead of addressing the user's request.





func lineStartsWithAny(line string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}

	return false
}


// requiredEvidenceCheck maps structurally-verifiable evidence labels to
// a check that returns true when the obligation is met. Labels that
// require semantic evaluation (e.g. answer_addresses_user_request) are
// excluded — the harness cannot evaluate answer quality.
var requiredEvidenceCheck = map[workerruntime.EvidenceRequirementID]func(answer string, results []workspace.ToolResult) bool{
	workerruntime.RequiredEvidenceTargetLocation: func(answer string, results []workspace.ToolResult) bool {
		if len(pathparse.MentionedWorkspacePaths(answer)) > 0 {
			return true
		}

		return len(changedPathsFromToolResults(results)) > 0
	},
	workerruntime.RequiredEvidenceChangedPaths: func(answer string, results []workspace.ToolResult) bool {
		return len(changedPathsFromToolResults(results)) > 0
	},
	workerruntime.RequiredEvidenceChangedPathsOrNoopReason: func(answer string, results []workspace.ToolResult) bool {
		if len(changedPathsFromToolResults(results)) > 0 {
			return true
		}

		lower := strings.ToLower(answer)

		return strings.Contains(lower, "no files were changed") ||
			strings.Contains(lower, "changed paths: none") ||
			strings.Contains(lower, "no changes") ||
			strings.Contains(lower, "noop")
	},
	workerruntime.RequiredEvidenceVerificationStatus: func(answer string, results []workspace.ToolResult) bool {
		lower := strings.ToLower(answer)
		if strings.Contains(lower, "verification:") ||
			strings.Contains(lower, "tests passed") ||
			strings.Contains(lower, "tests failed") ||
			strings.Contains(lower, "not run") ||
			strings.Contains(lower, "not detected") {
			return true
		}

		for _, r := range results {
			if isVerificationToolResult(r) {
				return true
			}
		}

		return false
	},
	workerruntime.RequiredEvidenceVerificationCommand: func(answer string, results []workspace.ToolResult) bool {
		for _, r := range results {
			if isVerificationToolResult(r) {
				return true
			}
		}

		return false
	},
	workerruntime.RequiredEvidencePassFailStatus: func(answer string, results []workspace.ToolResult) bool {
		lower := strings.ToLower(answer)

		return strings.Contains(lower, "pass") || strings.Contains(lower, "fail") ||
			strings.Contains(lower, "error") || strings.Contains(lower, "exit code")
	},
	workerruntime.RequiredEvidencePlanSubmittedOrSourceSatisfied: func(answer string, results []workspace.ToolResult) bool {
		return successfulSubmitPlanResultCount(results) > 0 ||
			strings.Contains(strings.ToLower(answer), "source-satisfied") ||
			strings.HasPrefix(strings.TrimSpace(answer), "<<QUESTION>>")
	},
	workerruntime.RequiredEvidenceGitHistoryRationale: func(answer string, results []workspace.ToolResult) bool {
		return successfulGitHistoryEvidenceSeen(results) || answerHasGitHistoryNoopOrBlocker(answer)
	},
	workerruntime.RequiredEvidenceInspectedPaths: func(answer string, results []workspace.ToolResult) bool {
		if len(pathparse.MentionedWorkspacePaths(answer)) > 0 {
			return true
		}

		for _, r := range results {
			for _, rel := range r.Relationships {
				if strings.TrimSpace(rel.TargetPath) != "" || strings.TrimSpace(rel.SourcePath) != "" {
					return true
				}
			}
		}

		return false
	},
	workerruntime.RequiredEvidenceExactWorkspacePath: func(answer string, results []workspace.ToolResult) bool {
		return len(pathparse.MentionedWorkspacePaths(answer)) > 0
	},
	workerruntime.RequiredEvidenceArtifactBodyOrTypedBlocker: func(answer string, results []workspace.ToolResult) bool {
		trimmed := strings.TrimSpace(answer)
		return len(trimmed) > 40 || strings.HasPrefix(trimmed, "<<QUESTION>>")
	},
}

// missingRequiredEvidence returns required_evidence labels that are
// structurally verifiable and not satisfied by the answer or results.
// Semantic labels (answer_addresses_user_request, confidence, etc.) are
// skipped — the harness cannot evaluate answer quality deterministically.
func missingRequiredEvidence(evidence []string, answer string, results []workspace.ToolResult) []string {
	var missing []string

	for _, label := range evidence {
		trimmed := strings.TrimSpace(label)
		check, ok := requiredEvidenceCheck[workerruntime.EvidenceRequirementID(trimmed)]

		if !ok {
			// Non-deterministic label — skip (model guidance only)
			continue
		}

		if !check(answer, results) {
			missing = append(missing, trimmed)
		}
	}

	return missing
}

// isVerificationToolResult reports whether a tool result came from a
// verification-oriented tool (source_sanity, shell with test invocation).
func isVerificationToolResult(result workspace.ToolResult) bool {
	tool := strings.TrimSpace(strings.ToLower(result.Tool))
	if tool == workspace.ToolNameSourceSanity {
		return true
	}

	if tool == workspace.ToolNameShell && strings.TrimSpace(result.Error) == "" {
		return true
	}

	return false
}

func stripFallbackJSONArtifacts(output string) string {
	if !strings.Contains(output, "{") {
		return output
	}

	lines := strings.Split(output, "\n")
	out := make([]string, 0, len(lines))
	skipUntilBaseline := false
	skipDepth := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if skipUntilBaseline {
			// Track nested braces. We exit the skip when depth returns
			// to zero, when we see a blank line, or when a new bullet
			// starts (lines beginning with "- " mark the next section).
			skipDepth += strings.Count(trimmed, "{") - strings.Count(trimmed, "}")
			if skipDepth <= 0 {
				skipUntilBaseline = false
				skipDepth = 0
			}

			if trimmed == "" || strings.HasPrefix(trimmed, "- ") {
				skipUntilBaseline = false
				skipDepth = 0

				if strings.HasPrefix(trimmed, "- ") {
					out = append(out, line)
				}
			}

			continue
		}

		// A bare "{" line, or a "{...}" JSON one-liner whose first
		// inner content is a quoted key, marks the start of a metadata
		// dump. Drop the block.
		if trimmed == "{" || (strings.HasPrefix(trimmed, "{") && strings.Contains(trimmed, "\":")) {
			skipUntilBaseline = true
			skipDepth = strings.Count(trimmed, "{") - strings.Count(trimmed, "}")

			if skipDepth <= 0 {
				// One-liner JSON; consume just this line.
				skipUntilBaseline = false
				skipDepth = 0
			}

			continue
		}

		out = append(out, line)
	}

	return strings.Join(out, "\n")
}
