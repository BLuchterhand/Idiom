package agent

import (
	"fmt"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/workerruntime"
	"slices"
	"strings"
)

type recoveryNudgeInputs struct {
	req                          workspace.ToolRequest
	results                      []workspace.ToolResult
	mgr                          *workspace.Manager
	brief                        workerruntime.WorkerRuntimeBrief
	agent                        string
	delegatedReadOnlyAnswerReady bool
	postChildPacketActive        bool
}

// recoveryNudgeRule is a priority-ordered rejection-recovery nudge.
// Returns (nudge, true) when fired or ("", false) when the rule does
// not apply. The dispatcher tries rules in order; first match wins.
type recoveryNudgeRule func(in recoveryNudgeInputs) (string, bool)

// recoveryNudgeRules lists each rule in priority order. Order is
// load-bearing: a rule that fires returns before later rules get a
// chance, so insertion position matters. When adding a new rule,
// place it at the priority that matches the scenario it handles.
//
//  1. verification-only shell (read-only lanes need to run the check)
//  2. exact-anchor pre-write (write-ready packet, no more discovery)
//  3. missing required-edit paths (allowlist not yet covered)
//  4. pending verification after writes (writes need sanity)
//  5. debug-log investigation (deepen or pivot inside the same log)
//  6. closure ready (delegated/child packet justifies answer)
//  7. premature evidence-first target read (need non-target evidence)
//  8. evidence-first target inspection (keep follow-up reads target-scoped)
//
// When every priority rule declines, lastChanceRecoveryNudge produces
// the tool-shape-specific default.
var recoveryNudgeRules = []recoveryNudgeRule{
	verificationOnlyShellRecoveryNudge,
	exactAnchorPreWriteRecoveryNudge,
	missingRequiredEditPathsRecoveryNudge,
	pendingVerificationAfterWritesRecoveryNudge,
	debugLogInvestigationRecoveryNudge,
	prematureEvidenceFirstTargetRecoveryNudge,
	evidenceFirstTargetInspectionRecoveryNudge,
}

// buildRejectionRecoveryNudge gives the model one last closure-biased repair
// after repeated rejected tool calls. It states the contract and avoids
// prescribing a copy-paste tool sequence unless a hard invariant requires it.
// Behavior is decomposed into priority-ordered rules (recoveryNudgeRules);
// the first rule that fires returns its nudge. If none fire, the
// tool-shape-specific lastChanceRecoveryNudge always produces a default.
func buildRejectionRecoveryNudge(req workspace.ToolRequest, results []workspace.ToolResult, mgr *workspace.Manager, brief workerruntime.WorkerRuntimeBrief, agent string, delegatedReadOnlyAnswerReady bool, postChildPacketActive bool) string {
	// Conversation lanes: the model owns readiness. Do not inject
	// nudges that declare evidence is sufficient or that the harness
	// will end the turn. The force-completion path is also gated in
	// rejectTool; this keeps the nudge text consistent.
	if LanePolicyForBrief(brief).ShouldSkipRecoveryNudge() {
		return ""
	}

	in := recoveryNudgeInputs{
		req:                          req,
		results:                      results,
		mgr:                          mgr,
		brief:                        brief,
		agent:                        agent,
		delegatedReadOnlyAnswerReady: delegatedReadOnlyAnswerReady,
		postChildPacketActive:        postChildPacketActive,
	}
	for _, rule := range recoveryNudgeRules {
		if nudge, ok := rule(in); ok {
			return nudge
		}
	}

	return lastChanceRecoveryNudge(in)
}

func verificationOnlyShellRecoveryNudge(in recoveryNudgeInputs) (string, bool) {
	if !workerruntime.PermissionProfileIsReadOnlyVerificationShell(in.brief.PermissionProfile) {
		return "", false
	}

	cmd := "go test ./..."
	if len(in.brief.VerificationPlan) > 0 && strings.TrimSpace(in.brief.VerificationPlan[0]) != "" {
		cmd = strings.TrimSpace(in.brief.VerificationPlan[0])
	}

	var b strings.Builder

	b.WriteString("This is a verification-only turn; the requested check has not yet run. Run it (the configured plan starts with `")
	b.WriteString(cmd)
	b.WriteString("`), then answer with the observed pass/fail. If the check has already run, answer with that result.\n")

	return b.String(), true
}

func exactAnchorPreWriteRecoveryNudge(in recoveryNudgeInputs) (string, bool) {
	if !exactAnchorPreWriteSurfaceActive(in.brief, in.results) {
		return "", false
	}

	target := "the supplied exact-anchor target"
	if paths := nonEmptyRunnerStrings(in.brief.RequiredEditPaths, 3); len(paths) > 0 {
		target = strings.Join(paths, ", ")
	} else if refs := nonEmptyRunnerStrings(in.brief.TargetRefs, 3); len(refs) > 0 {
		target = strings.Join(refs, ", ")
	}

	var b strings.Builder

	b.WriteString("This is an exact-anchor implementation pre-write turn. The target packet is the current-state path and source evidence for the first write; do not inspect, search, shell, or verify before that write.\n")
	b.WriteString("Exact-anchor write capability is required for ")
	b.WriteString(target)
	b.WriteString(". If the supplied anchor is missing or stale, answer with <<QUESTION>> naming that exact anchor gap; no additional discovery is allowed in this lane.\n")

	return b.String(), true
}

func missingRequiredEditPathsRecoveryNudge(in recoveryNudgeInputs) (string, bool) {
	if !runnerBriefRequiresMutation(in.brief) {
		return "", false
	}

	missing := missingRequiredEditPaths(in.brief.RequiredEditPaths, changedPathsFromToolResults(in.results))
	if len(missing) == 0 {
		return "", false
	}

	var b strings.Builder

	if strings.TrimSpace(in.brief.TaskFamily) == PlanThenExecutePlannerTaskFamily {
		b.WriteString("Recovery nudge — this plan-execute planner turn still has required edit path(s) without successful `submit_plan` coverage: ")
		b.WriteString(strings.Join(missing, ", "))
		b.WriteString(".\n")

		if planThenExecuteSubmitCorrectionActive(in.brief, in.results) {
			if planThenExecuteSubmitCorrectionBlockerAllowed(in.brief, in.results) {
				b.WriteString("Do not answer with partial findings. Submit-correction mode accepts a parser-readable `submit_plan`, or `<<QUESTION>>` only with the exact path-specific missing body/anchor blocker after repeated rejected submit_plan attempts. ")
				b.WriteString(planThenExecuteSubmitPlanCoverageSentence())
				b.WriteString(" Do not answer `Decision: source-satisfied` and do not repeat the rejected tool call.\n")
			} else {
				b.WriteString("Do not answer with partial findings. Submit-correction mode has exactly one valid outcome: parser-readable `submit_plan`. ")
				b.WriteString(planThenExecuteSubmitPlanCoverageSentence())
				b.WriteString(" Do not answer `Decision: source-satisfied`, do not answer <<QUESTION>>, and do not repeat the rejected tool call.\n")
			}

			return b.String(), true
		}

		b.WriteString("Do not answer with partial findings. Required outcomes are `submit_plan` (")
		b.WriteString(planThenExecuteSubmitPlanCoverageSentence())
		b.WriteString("), `Decision: source-satisfied` with current-state evidence for every remaining path, or <<QUESTION>> naming the exact blocker for each remaining path. Do not repeat the rejected tool call.\n")

		return b.String(), true
	}

	b.WriteString("Recovery nudge — this implementation turn still has required edit path(s) without a successful write: ")
	b.WriteString(strings.Join(missing, ", "))
	b.WriteString(".\n")

	if scopedImplementationUsesBoundedSourceEdits(in.brief, missing) {
		b.WriteString("Do not answer with partial findings. For existing source/test required paths, the allowed write outcome is a bounded `edit_file` call using old_text/new_text or start_line/end_line/content, or an `apply_patch` envelope. Do not use `write_file` or `append_file` for existing source/test paths; reserve whole-file writes for new files, small files, and non-source deliverables. Otherwise answer with <<QUESTION>> naming the exact blocker for each remaining path. Do not repeat the rejected tool call.\n")

		return b.String(), true
	}

	b.WriteString("Do not answer with partial findings. Required outcomes are an allowed write or <<QUESTION>> naming the exact blocker for each remaining path. Do not repeat the rejected tool call.\n")

	return b.String(), true
}

func pendingVerificationAfterWritesRecoveryNudge(in recoveryNudgeInputs) (string, bool) {
	if failed, ok := failedVerificationAfterLatestWrite(in.results, in.brief); ok {
		return postWriteVerificationFailedRecoveryPrompt(failed, plannerOwnsVerification(in.brief)) + "\n", true
	}

	if !hasPendingVerificationAfterWrites(in.results, in.brief) {
		return "", false
	}

	cmd := "git diff --check"
	if len(in.brief.VerificationPlan) > 0 && strings.TrimSpace(in.brief.VerificationPlan[0]) != "" {
		cmd = strings.TrimSpace(in.brief.VerificationPlan[0])
	}

	var b strings.Builder

	if plannerOwnsVerification(in.brief) {
		b.WriteString("This implementation turn has successful writes that are still pending lane sanity. Do not inspect or edit again yet. Build/test verification is planner-owned for this route, so required evidence is compiler-backed source-sanity or language diagnostics for changed source files; for non-source edits one cheap non-build sanity command such as `")
		b.WriteString(cmd)
		b.WriteString("`. For source edits, `git diff --check` and generic source-sanity fallback are only patch hygiene. If source sanity is forbidden or impossible, answer with the changed paths and the concrete lane-sanity blocker.\n")

		return b.String(), true
	}

	b.WriteString("This implementation turn has successful writes that are still pending verification. Do not inspect or edit again yet. Required evidence is one focused language/project verification command or compiler-backed diagnostics. If a verification plan exists, start with `")
	b.WriteString(cmd)
	b.WriteString("`. If verification is forbidden or impossible, answer with the changed paths and the concrete verification blocker.\n")

	return b.String(), true
}

func debugLogInvestigationRecoveryNudge(in recoveryNudgeInputs) (string, bool) {
	if strings.TrimSpace(in.brief.TaskFamily) != "debug_logs" {
		return "", false
	}

	selection, ok := latestDebugLogSelection(in.results)
	if !ok {
		return "", false
	}

	var b strings.Builder

	b.WriteString("This is a debug-log investigation. Stay on real log artifacts.\n")

	if shouldDeepenDebugLogSearch(in.results, selection) {
		if next, nextOK := nextDebugLogSelection(in.results, selection); nextOK {
			b.WriteString("The current selected log did not yield a concrete hit. Deepen to the next newest log now:\n")
			b.WriteString("  ")
			b.WriteString(marshalToolRequestRaw(debugLogTailRequest(next)))
			b.WriteString("\n")
			b.WriteString("If that next log is still not useful, answer with the current uncertainty and name the missing symptom.\n")

			return b.String(), true
		}
	}

	b.WriteString("Answer from the selected log if it is already clear.\n")

	if redirected, redirectedOK := debugLogScopedSearchRedirect(in.req, selection); redirectedOK {
		b.WriteString("Or search inside the same selected log with your current pattern:\n")
		b.WriteString("  ")
		b.WriteString(marshalToolRequestRaw(redirected))
		b.WriteString("\n")
	}

	return b.String(), true
}

func prematureEvidenceFirstTargetRecoveryNudge(in recoveryNudgeInputs) (string, bool) {
	if !isPrematureEvidenceFirstTargetRead(in.req, in.results, in.brief) {
		return "", false
	}

	var b strings.Builder

	b.WriteString("This turn still needs source evidence before the named target file.\n")

	candidates := nonTargetRecoveryFileCandidates(in.results, in.brief.TargetRefs)
	if len(candidates) > 0 {
		b.WriteString("Candidate source paths:\n")

		for _, path := range candidates {
			b.WriteString("  - ")
			b.WriteString(path)
			b.WriteString("\n")
		}
	} else {
		b.WriteString("Use a non-target implementation file or search result that can explain the change before returning to the target.\n")
	}

	b.WriteString("\nIf the source evidence is already sufficient in prior results, answer the user directly instead of calling another tool.\n")

	return b.String(), true
}

func evidenceFirstTargetInspectionActive(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	return isEvidenceFirstMutationTaskFamily(brief.TaskFamily) &&
		len(brief.TargetRefs) > 0 &&
		len(changedPathsFromToolResults(results)) == 0 &&
		hasFocusedNonTargetEvidence(results, brief.TargetRefs) &&
		hasSuccessfulTargetRead(results, brief.TargetRefs)
}

func evidenceFirstTargetInspectionRecoveryNudge(in recoveryNudgeInputs) (string, bool) {
	if !evidenceFirstTargetInspectionActive(in.brief, in.results) {
		return "", false
	}

	target := util.FirstNonEmpty(in.brief.TargetRefs...)
	if target == "" {
		target = strings.TrimSpace(in.req.Path)
	}

	var b strings.Builder

	b.WriteString("This turn already has source evidence and target-file evidence.\n")
	b.WriteString("Either edit the target now, or if one exact detail is still missing, stay inside the named target with a named, narrow read or search.\n")

	if target != "" {
		start, end := 1, 40
		if window, ok := latestSuccessfulTargetReadWindow(in.results, in.brief.TargetRefs); ok {
			start = window.End - 19
			if start < 1 {
				start = 1
			}

			end = window.End
		}

		b.WriteString("Relevant target window: ")
		b.WriteString(target)
		b.WriteString(fmt.Sprintf(" lines %d-%d\n", start, end))
	}

	b.WriteString("\nIf you already have enough evidence, answer the user now with what you observed.\n")

	return b.String(), true
}

// lastChanceRecoveryNudge is the always-fires terminator. It produces
// a tool-shape-specific default when no priority rule fired.
func lastChanceRecoveryNudge(in recoveryNudgeInputs) string {
	tool := strings.TrimSpace(in.req.Tool)

	var b strings.Builder

	b.WriteString("Recovery nudge — last chance before the harness ends the turn. Answer with the evidence already gathered. Only take one narrow next step if you can name the exact missing claim it closes. Do not repeat the rejected tool call.\n\n")

	switch {
	case tool == workspace.ToolNameListFiles || tool == "list_dir":
		candidates := rejectionRecoveryFileCandidates(in.results)
		if len(candidates) > 0 {
			b.WriteString("You already have a workspace overview. Choose one discovered path that can close a named gap:\n")

			for _, path := range candidates {
				b.WriteString("  - ")
				b.WriteString(path)
				b.WriteString("\n")
			}
		} else {
			b.WriteString("You already have a workspace overview. Either answer now or take one focused search/read step tied to a named gap.\n")
		}
	case tool == workspace.ToolNameReadFile:
		path := strings.TrimSpace(in.req.Path)
		if path != "" {
			b.WriteString("The repeated read should become a smaller window or a structured probe tied to a named gap on ")
			b.WriteString(path)
			b.WriteString(".\n")
		} else {
			b.WriteString("The repeated read needs a concrete discovered path and a named gap.\n")
		}
	case tool == workspace.ToolNameSearchContent:
		b.WriteString("A follow-up search should change the claim being tested or narrow to a specific discovered path.\n")

		if strings.TrimSpace(in.req.Path) != "" {
			b.WriteString("Current scope: ")
			b.WriteString(in.req.Path)
			b.WriteString("\n")
		}
	default:
		b.WriteString("Pick a materially different move that names the exact missing claim it closes.\n")
	}

	b.WriteString("\nIf you already have enough evidence, answer the user now with what you observed.\n")

	return b.String()
}

func isPrematureEvidenceFirstTargetRead(req workspace.ToolRequest, results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) bool {
	return isEvidenceFirstMutationTaskFamily(brief.TaskFamily) &&
		len(brief.TargetRefs) > 0 &&
		strings.TrimSpace(req.Tool) == workspace.ToolNameReadFile &&
		slices.Contains(brief.TargetRefs, strings.TrimSpace(req.Path)) &&
		!hasFocusedNonTargetEvidence(results, brief.TargetRefs)
}

func nonTargetRecoveryFileCandidates(results []workspace.ToolResult, targetRefs []string) []string {
	raw := rejectionRecoveryFileCandidates(results)
	filtered := make([]string, 0, len(raw))

	for _, path := range raw {
		if slices.Contains(targetRefs, path) {
			continue
		}

		filtered = append(filtered, path)
	}

	return filtered
}

// rejectionRecoveryFileCandidates pulls up to 3 plausible files the model
// can read_file next, harvested from whatever listing results it already
// collected this turn. Preference: .go files in internal/, then anything
// .go, then any discovered path.
func rejectionRecoveryFileCandidates(results []workspace.ToolResult) []string {
	seen := make(map[string]struct{})
	internalGo := make([]string, 0, 3)
	anyGo := make([]string, 0, 3)
	anything := make([]string, 0, 3)

	for _, r := range results {
		for _, line := range strings.Split(r.Output, "\n") {
			path := recoveryCandidatePath(line)
			if path == "" || strings.HasPrefix(path, "/") {
				continue
			}

			if _, ok := seen[path]; ok {
				continue
			}

			seen[path] = struct{}{}
			switch {
			case strings.HasSuffix(path, ".go") && strings.HasPrefix(path, "internal/"):
				if len(internalGo) < 3 {
					internalGo = append(internalGo, path)
				}
			case strings.HasSuffix(path, ".go"):
				if len(anyGo) < 3 {
					anyGo = append(anyGo, path)
				}
			default:
				if len(anything) < 3 {
					anything = append(anything, path)
				}
			}
		}
	}

	if len(internalGo) > 0 {
		return internalGo
	}

	if len(anyGo) > 0 {
		return anyGo
	}

	return anything
}

func recoveryCandidatePath(line string) string {
	path := strings.TrimSpace(line)

	path = strings.TrimPrefix(path, "- ")
	path = strings.TrimSpace(path)

	if path == "" {
		return ""
	}

	if colon := strings.IndexByte(path, ':'); colon > 0 {
		prefix := strings.TrimSpace(path[:colon])
		if strings.Contains(prefix, "/") || strings.Contains(prefix, ".") {
			path = prefix
		}
	}

	if strings.Contains(path, " ") {
		return ""
	}

	return path
}

func compactOutput(output string, maxLines int) string {
	output = stripFallbackJSONArtifacts(output)

	lines := strings.Split(strings.TrimSpace(output), "\n")

	if len(lines) == 0 || lines[0] == "" {
		return ""
	}

	if maxLines <= 0 || len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}

	return strings.Join(lines[:maxLines], "\n") + "\n..."
}

// sanitizeFallbackResultOutput returns a tool-result Output that's safe to
// embed under "Verified context" in a forced-completion fallback. Some
// tools produce Output that is fine for the model's tool-call loop but
// reads as noise in the final user-facing answer:
//
//   - search_content matches against test fixtures often span multi-line
//     quoted Go strings containing JSON, so the per-line strip can't
//     reach the JSON cleanly. The count summary ("Found N matching
//     lines.") is sufficient context for synthesis; drop the raw output.
//   - read_file and symbol-read outputs are kept — those are the actual
//     evidence the model needs to summarize.
//
// Generic across languages: applies to *any* tool whose output is a
// long quoted-string match list.
func sanitizeFallbackResultOutput(result workspace.ToolResult) string {
	switch strings.TrimSpace(result.Tool) {
	case workspace.ToolNameSearchContent, workspace.ToolNameSearchFiles:
		return ""
	default:
		return result.Output
	}
}

// stripFallbackJSONArtifacts removes JSON-shaped object dumps from a tool
// result Output before it gets surfaced under "Verified context" in the
// forced-completion fallback. When a child lane's task result Output
// includes a discovery_query summary, the artifact metadata JSON often
// follows verbatim ({"id": "...", "purpose": "...", "layers_run": [...]
// }) and bleeds into the user-facing answer as opaque blocks. The
// fallback should show prose bullets; metadata blobs belong in the
// artifact file, not the answer. Generic across artifact JSON — any
// brace-delimited object whose first content line starts with a quoted
// key is treated as a metadata dump and dropped.

// conversationLaneWithoutSemanticEval reports true when the lane has no
// deterministic final-answer relevance contract. Read-only direct-answer
// lanes now have final_answer_acceptance.v1, so they no longer fall into
// that bucket when the objective is explicit enough to check.
