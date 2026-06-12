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
func looksLikeNonAnswerProse(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	lower := strings.ToLower(trimmed)

	if trimmed == "" {
		return false
	}

	// Textual tool-call markup like [Calling tool: ...] in answer prose.
	if looksLikeTextualToolCall(answer) {
		return true
	}

	// Echoed user question: the answer starts with "Question:" followed
	// by the user's own words rather than an actual investigation result.
	if strings.HasPrefix(lower, "question:") {
		return true
	}

	if looksLikeMetaIntentRestatement(trimmed) {
		return true
	}

	if looksLikeScratchpadLeakedAnswer(trimmed) {
		return true
	}

	if looksLikeRawReadOnlyEvidencePacketAnswer(trimmed) {
		return true
	}

	if looksLikeLowValueSourceInventoryNonAnswer(trimmed) {
		return true
	}

	if finalAnswerSectionMarkerCount(answer) > 1 {
		return true
	}

	// Raw tool-result transcript with no user-facing prose.
	if looksLikeRawToolResultTranscript(answer) {
		return true
	}

	if looksLikeSourceEchoOnlyAnswer(trimmed) {
		return true
	}

	// Leaked implementation-result templates are protocol artifacts, not
	// direct answers. Do not reject ordinary diagnostic prose just because it
	// discusses a blocker, a failed command, or an exit status.
	if looksLikeLeakedImplementationBlockerTemplate(lower) {
		return true
	}

	return false
}

func looksLikeRawReadOnlyEvidencePacketAnswer(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))
	if lower == "" || strings.Contains(lower, "grounded answer:") {
		return false
	}

	packetMarkers := 0

	for _, marker := range append(delegatedReadOnlyPacketLineLabels(), "source: delegated read-only evidence packet") {
		if strings.Contains(lower, marker) {
			packetMarkers++
		}
	}

	return packetMarkers >= 1
}

func looksLikeLeakedImplementationBlockerTemplate(lowerAnswer string) bool {
	if !strings.HasPrefix(lowerAnswer, "changed paths: none") {
		return false
	}

	return strings.Contains(lowerAnswer, "\nverification: not run") &&
		strings.Contains(lowerAnswer, "\nblocker:")
}

func looksLikeMetaIntentRestatement(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" || len(trimmed) > 700 || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return false
	}

	lower := " " + strings.ToLower(strings.Join(strings.Fields(trimmed), " ")) + " "
	if !containsAny(lower,
		" the user is asking me to ",
		" the user asked me to ",
		" the user wants me to ",
		" the user requested me to ",
	) {

		return false
	}

	if containsAny(lower,
		" the answer is ",
		" the likely cause is ",
		" the root cause is ",
		" because ",
		" this means ",
	) {

		return false
	}

	return containsAny(lower,
		" i need to find ",
		" i need to inspect ",
		" i need to search ",
		" i need to read ",
		" i need to check ",
		" i should find ",
		" i should inspect ",
		" i should search ",
		" i should read ",
		" i should check ",
	)
}

func looksLikeScratchpadLeakedAnswer(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return false
	}

	normalized := " " + strings.ToLower(strings.Join(strings.Fields(trimmed), " ")) + " "
	for i, line := range strings.Split(trimmed, "\n") {
		lowerLine := strings.ToLower(strings.TrimSpace(line))
		if lowerLine == "" {
			continue
		}

		if lineStartsWithAny(lowerLine,
			"actually, i should",
			"actually, i need",
			"actually, let me",
			"actually, wait",
			"let me analyze",
			"let me inspect",
			"let me check",
			"let me read",
			"let me search",
			"let me verify",
			"let me gather",
			"let me prepare",
			"let me write",
			"let me provide",
			"let me produce",
			"let me think",
			"i should analyze",
			"i should inspect",
			"i should check",
			"i should read",
			"i should search",
			"i should verify",
			"i should gather",
			"i should prepare",
			"i should write",
			"i should provide",
			"i should produce",
			"i need to analyze",
			"i need to inspect",
			"i need to check",
			"i need to read",
			"i need to search",
			"i need to verify",
			"i need to gather",
			"i need to prepare",
			"i need to write",
			"i need to provide",
			"i need to produce",
			"ok, i'm providing",
			"ok, i’m providing",
			"ok, final answer",
			"ok, i'm ready",
			"ok, i’m ready",
			"wait, i",
		) {

			return true
		}

		if i == 0 && lineStartsWithAny(lowerLine, "the user wants", "the user is asking") {
			if containsAny(normalized,
				" actually, ",
				" actually, i ",
				" actually, let me ",
				" let me ",
				" i should ",
				" i need to ",
				" ok, i'm providing ",
				" ok, i’m providing ",
				" ok, final answer ",
				" wait, ",
			) {

				return true
			}

			return !containsAny(normalized,
				" the answer is ",
				" the likely cause is ",
				" the root cause is ",
				" because ",
				" this means ",
			)
		}
	}

	return false
}

func lineStartsWithAny(line string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}

	return false
}

func looksLikeSourceEchoOnlyAnswer(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" || len(trimmed) > 700 || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return false
	}

	lower := " " + strings.ToLower(strings.Join(strings.Fields(trimmed), " ")) + " "
	if !looksLikeToolResultTranscript(lower) {
		return false
	}

	claim := normalizeSourceInventoryClaim(stripPresentableSourceObjectiveEcho(trimmed))
	if sourceInventoryClaimLooksSynthesized(claim) ||
		containsAny(lower,
			" should reject ",
			" should change ",
		) {

		return false
	}

	return true
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
