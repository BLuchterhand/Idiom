package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"idiom/internal/llama"
	"idiom/internal/util"
	"idiom/internal/workspace"
)

type fingerprintTrackingState struct {
	lastFingerprint string
	identicalRun    int
	counts          map[string]int
	samePathCounts  map[string]int
	samePathNudged  map[string]bool
	nudgeFired      map[string]bool
	zeroHit         map[string]string
	zeroHitFamily   map[string]zeroHitFamilyState
	rejected        map[string]string
	// rejectedRetries counts how many times a fingerprint has been
	// retried after a prior rejection. Nudge on first retry, force-answer
	// on the second — matching the nudge-then-block pattern.
	rejectedRetries map[string]int
}

type zeroHitFamilyState struct {
	Count      int
	Summary    string
	NudgeFired bool
}

type postChildBudgetState struct {
	readCalls        int
	readBudget       int
	evidencePaths    []string
	readFamilies     map[string]int
	familyNudgeFired map[string]bool
}

type runnerRequestBudgetState struct {
	fingerprint fingerprintTrackingState
	postChild   postChildBudgetState

	// writeReadyNudgeActive is set by the write-ready nudge path in
	// runner.go when the model is in a write-ready-but-not-writing
	// state. When true, the rejection retry budget defers to the
	// write-ready escalation contract (12-nudge countdown) instead
	// of force-answering on the first rejection retry.
	writeReadyNudgeActive bool

	broadEvidenceDelegationRequired  bool
	broadEvidenceDelegationSatisfied bool
	readOnlyChildTaskSatisfied       bool
	synthesisNudgeFired              bool
	readCallCount                    int
	readCallsSinceWrite              int
	scopedReadRejections             map[string]int
}

func (s runnerRequestBudgetState) postChildActive() bool {
	return s.readOnlyChildTaskSatisfied || s.broadEvidenceDelegationSatisfied
}

type runnerRequestBudgetDecision struct {
	Handled           bool
	RejectSummary     string
	RejectError       string
	RejectStopReason  string
	ForceAnswerPrompt string
	ForceAnswerReason string
	HideTool          string
	Nudge             string
	EventRole         string
	EventContent      string
}

type budgetDecisionContext struct {
	controller     *runnerTurnController
	stepNumber     int
	activeToolCall *llama.ToolCall
	req            workspace.ToolRequest
}

type budgetDecisionResult struct {
	shouldFinish   bool
	shouldContinue bool
	answer         string
	err            error
	reason         string
}

func (d runnerRequestBudgetDecision) apply(ctx budgetDecisionContext) budgetDecisionResult {
	if !d.Handled {
		return budgetDecisionResult{}
	}
	if d.Nudge != "" {
		*ctx.controller.turnMessages = append(*ctx.controller.turnMessages, llama.ChatMessage{Role: "system", Content: d.Nudge})
	}
	if d.EventRole != "" {
		ctx.controller.runner.emitEvent(RunEvent{Role: d.EventRole, Content: util.FirstNonEmpty(d.EventContent, d.Nudge)})
	}
	if d.ForceAnswerPrompt != "" {
		answer, err := ctx.controller.completeWithoutMoreToolsWithReason(d.ForceAnswerPrompt)
		return budgetDecisionResult{
			shouldFinish: true,
			answer:       answer,
			err:          err,
			reason:       d.ForceAnswerReason,
		}
	}
	if d.RejectSummary != "" {
		answer, err, done := ctx.controller.rejectTool(ctx.stepNumber, ctx.activeToolCall, ctx.req, d.RejectSummary, d.RejectError)
		if done {
			return budgetDecisionResult{
				shouldFinish: true,
				answer:       answer,
				err:          err,
				reason:       d.RejectStopReason,
			}
		}
		return budgetDecisionResult{shouldContinue: true}
	}
	return budgetDecisionResult{}
}

func newRunnerRequestBudgetState(postChildReadBudget int) runnerRequestBudgetState {
	return runnerRequestBudgetState{
		fingerprint: fingerprintTrackingState{
			counts:          make(map[string]int),
			samePathCounts:  make(map[string]int),
			samePathNudged:  make(map[string]bool),
			nudgeFired:      make(map[string]bool),
			zeroHit:         make(map[string]string),
			zeroHitFamily:   make(map[string]zeroHitFamilyState),
			rejected:        make(map[string]string),
			rejectedRetries: make(map[string]int),
		},
		scopedReadRejections: make(map[string]int),
		postChild: postChildBudgetState{
			readBudget:       postChildReadBudget,
			readFamilies:     make(map[string]int),
			familyNudgeFired: make(map[string]bool),
		},
	}
}

const samePathFixationNudgeAt = 5

// samePathFingerprint collapses different query parameters into a
// same-tool + same-path identifier, catching file fixation even when
// the exact request arguments vary.
func samePathFingerprint(req workspace.ToolRequest) string {
	path := strings.TrimSpace(req.Path)
	if path == "" {
		return ""
	}
	path = filepath.ToSlash(filepath.Clean(path))
	return req.Tool + "|path:" + path
}

func (s *runnerRequestBudgetState) noteFingerprint(currentFingerprint string) int {
	if currentFingerprint != "" && currentFingerprint == s.fingerprint.lastFingerprint {
		s.fingerprint.identicalRun++
	} else {
		s.fingerprint.identicalRun = 1
		s.fingerprint.lastFingerprint = currentFingerprint
	}
	if currentFingerprint != "" {
		s.fingerprint.counts[currentFingerprint]++
	}
	return s.fingerprint.identicalRun
}

func (s *runnerRequestBudgetState) samePathFixationDecision(req workspace.ToolRequest, evidenceAvailable bool) runnerRequestBudgetDecision {
	fp := samePathFingerprint(req)
	if fp == "" || isDiagnosticLogArtifactPath(req.Path) {
		return runnerRequestBudgetDecision{}
	}
	s.fingerprint.samePathCounts[fp]++
	count := s.fingerprint.samePathCounts[fp]
	if count < samePathFixationNudgeAt {
		return runnerRequestBudgetDecision{}
	}

	path := cleanToolRequestPathDisplay(req.Path)
	if s.fingerprint.samePathNudged[fp] {
		return runnerRequestBudgetDecision{
			Handled:  true,
			HideTool: strings.TrimSpace(req.Tool),
			Nudge: fmt.Sprintf(
				"You asked to keep reading %s after the same-path warning. `%s` is hidden for the rest of this turn. Use structured navigation for a named symbol/body, switch to a different evidence source through the remaining tools, or answer from the evidence already gathered.",
				path,
				req.Tool,
			),
			EventRole: "same_path_fixation_tool_hidden",
		}
	}
	s.fingerprint.samePathNudged[fp] = true

	action := "switch to a materially different evidence path, use one structured source-body probe for a named missing claim, or answer with the evidence already gathered"
	if evidenceAvailable {
		action = "answer from the evidence already gathered, or use one structured source-body probe only if you can name the exact missing claim it will resolve"
	}
	return runnerRequestBudgetDecision{
		Handled: true,
		Nudge: fmt.Sprintf(
			"You have called %s on %s %d times this turn with varied arguments. Stop walking the same file by adjacent chunks; %s. Do not request another adjacent range from the same file.",
			req.Tool,
			path,
			count,
			action,
		),
		EventRole: "same_path_fixation_nudge",
	}
}

func (s *runnerRequestBudgetState) fingerprintDecision(policy RunPolicy, req workspace.ToolRequest, currentFingerprint string) runnerRequestBudgetDecision {
	if currentFingerprint == "" {
		return runnerRequestBudgetDecision{}
	}
	if decision := s.terminalArtifactRejectedRetryDecision(req, currentFingerprint); decision.Handled {
		return decision
	}
	if policy.FingerprintNudgeAt > 0 {
		count := s.fingerprint.counts[currentFingerprint]
		nudgeFired := s.fingerprint.nudgeFired[currentFingerprint]
		if count >= policy.FingerprintNudgeAt && !nudgeFired {
			s.fingerprint.nudgeFired[currentFingerprint] = true
			return runnerRequestBudgetDecision{
				Handled:   true,
				Nudge:     fmt.Sprintf("You have called %s with the same arguments %d times this turn (not consecutively). Summarize what you learned from those calls and pick a materially different next step, or answer with the evidence you already have. Any further identical call will be blocked.", req.Tool, count),
				EventRole: "fingerprint_nudge",
			}
		}
		if count > policy.FingerprintNudgeAt && nudgeFired {
			return runnerRequestBudgetDecision{
				Handled:           true,
				EventRole:         "fingerprint_hard_block",
				EventContent:      fmt.Sprintf("%s repeated %d times; hard-blocking", req.Tool, count),
				ForceAnswerReason: "fingerprint_hard_block_exhausted",
				ForceAnswerPrompt: fmt.Sprintf("You called %s with identical arguments %d times after being warned. Further tools are not available. Provide your final answer from the evidence already gathered. If the repeated request reflects one unsupported claim, state that uncertainty after the supported answer with the smallest useful next action.", req.Tool, count),
			}
		}
	}
	if priorSummary, seen := s.fingerprint.zeroHit[currentFingerprint]; seen {
		return runnerRequestBudgetDecision{
			Handled:          true,
			RejectSummary:    "Zero-hit retry blocked",
			RejectError:      fmt.Sprintf("this exact %s call already returned no results earlier this turn (\"%s\"). Repeating it will not find new matches. Broaden or change the pattern, search a different scope, or answer with the evidence you have.", req.Tool, priorSummary),
			RejectStopReason: "zero_hit_retry_budget_exhausted",
		}
	}
	if decision := s.zeroHitFamilyDecision(req); decision.Handled {
		return decision
	}
	if decision := s.rejectedFingerprintDecision(req, currentFingerprint); decision.Handled {
		return decision
	}
	return runnerRequestBudgetDecision{}
}

func (s *runnerRequestBudgetState) noteZeroHitSearch(req workspace.ToolRequest, currentFingerprint string, result workspace.ToolResult) {
	summary := truncForRetryReason(result.Summary)
	if strings.TrimSpace(currentFingerprint) != "" {
		s.fingerprint.zeroHit[currentFingerprint] = summary
	}
	family := zeroHitSearchFamilyKey(req)
	if family == "" {
		return
	}
	state := s.fingerprint.zeroHitFamily[family]
	state.Count++
	state.Summary = summary
	s.fingerprint.zeroHitFamily[family] = state
}

func (s *runnerRequestBudgetState) zeroHitFamilyDecision(req workspace.ToolRequest) runnerRequestBudgetDecision {
	family := zeroHitSearchFamilyKey(req)
	if family == "" {
		return runnerRequestBudgetDecision{}
	}
	state, seen := s.fingerprint.zeroHitFamily[family]
	if !seen {
		return runnerRequestBudgetDecision{}
	}
	if state.Count >= 2 {
		return runnerRequestBudgetDecision{
			Handled:          true,
			RejectSummary:    "Zero-hit search family blocked",
			RejectError:      fmt.Sprintf("closely related %s searches already returned no results this turn (latest: %q). Repeating symbol or spelling variants in the same search family is not progress. Switch to a source-body read from an existing candidate, use a broader semantic/context query, or answer with the evidence already gathered.", req.Tool, state.Summary),
			RejectStopReason: "zero_hit_family_budget_exhausted",
		}
	}
	if !state.NudgeFired {
		state.NudgeFired = true
		s.fingerprint.zeroHitFamily[family] = state
		return runnerRequestBudgetDecision{
			Handled:      true,
			EventRole:    "zero_hit_family_nudge",
			EventContent: fmt.Sprintf("zero-hit search family %q seen once; nudging before another related miss", family),
			Nudge: fmt.Sprintf(
				"A closely related %s search already returned no results this turn (%q). Before trying another spelling or symbol variant, choose a materially different probe: read a localized candidate, use semantic/context navigation, or answer with the evidence you have.",
				req.Tool, state.Summary),
		}
	}
	return runnerRequestBudgetDecision{}
}

func zeroHitSearchFamilyKey(req workspace.ToolRequest) string {
	switch strings.TrimSpace(req.Tool) {
	case "search_content", "search_files", "rg", "grep":
	default:
		return ""
	}
	terms := zeroHitSearchFamilyTerms(strings.Join([]string{req.Pattern, req.Query, req.Command}, " "))
	if len(terms) < 2 {
		return ""
	}
	path := normalizePostChildPath(req.Path)
	return strings.TrimSpace(req.Tool) + ":" + path + ":" + strings.Join(terms, ":")
}

func zeroHitSearchFamilyTerms(text string) []string {
	words := splitZeroHitSearchWords(text)
	if len(words) == 0 {
		return nil
	}
	hasKey := requestBudgetContainsString(words, "key")
	hasArrow := requestBudgetContainsString(words, "arrow")
	if hasKey && hasArrow {
		return []string{"key", "arrow"}
	}
	var terms []string
	for _, word := range words {
		switch word {
		case "up", "down", "left", "right", "true", "false":
			continue
		}
		terms = util.AppendUnique(terms, word)
		if len(terms) >= 3 {
			break
		}
	}
	return terms
}

func splitZeroHitSearchWords(text string) []string {
	var words []string
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		word := strings.ToLower(b.String())
		b.Reset()
		if len(word) < 3 {
			return
		}
		words = util.AppendUnique(words, word)
	}
	prevLower := false
	for _, r := range text {
		if r >= 'A' && r <= 'Z' {
			if prevLower {
				flush()
			}
			b.WriteRune(r + ('a' - 'A'))
			prevLower = false
			continue
		}
		if r >= 'a' && r <= 'z' {
			b.WriteRune(r)
			prevLower = true
			continue
		}
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
			prevLower = false
			continue
		}
		flush()
		prevLower = false
	}
	flush()
	return words
}

func requestBudgetContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func (s *runnerRequestBudgetState) rejectedFingerprintDecision(req workspace.ToolRequest, currentFingerprint string) runnerRequestBudgetDecision {
	if s == nil || strings.TrimSpace(currentFingerprint) == "" {
		return runnerRequestBudgetDecision{}
	}
	if decision := s.terminalArtifactRejectedRetryDecision(req, currentFingerprint); decision.Handled {
		return decision
	}
	if priorSummary, ok := s.fingerprint.rejected[currentFingerprint]; ok {
		if isRecoverablePostReadinessGapRejection(priorSummary) {
			delete(s.fingerprint.rejected, currentFingerprint)
			delete(s.fingerprint.rejectedRetries, currentFingerprint)
			return runnerRequestBudgetDecision{}
		}
		// When write-ready nudge escalation is active, defer to that
		// contract rather than force-answering on rejection retry.
		if s.writeReadyNudgeActive {
			s.fingerprint.rejectedRetries[currentFingerprint]++
			return runnerRequestBudgetDecision{
				Handled:   true,
				EventRole: "rejection_retry_deferred_to_write_ready",
				Nudge: fmt.Sprintf(
					"You retried a rejected %s call. The write-ready contract is active — either write to a sourced target path or name the exact missing claim and probe. The rejection retry budget is deferred until the write-ready escalation completes.",
					req.Tool),
			}
		}
		retries := s.fingerprint.rejectedRetries[currentFingerprint] + 1
		s.fingerprint.rejectedRetries[currentFingerprint] = retries
		if retries == 1 {
			return runnerRequestBudgetDecision{
				Handled:      true,
				EventRole:    "rejection_retry_nudge",
				EventContent: fmt.Sprintf("model retried a previously rejected %s call (%s) — nudging", req.Tool, priorSummary),
				Nudge: fmt.Sprintf(
					"You retried a %s request that was already rejected this turn (%q). The rejection reason still applies — repeating the same call will not change the outcome. Either take a materially different approach, or answer with the evidence you already have.",
					req.Tool, priorSummary),
			}
		}
		return runnerRequestBudgetDecision{
			Handled:           true,
			EventRole:         "rejection_retry_blocked",
			EventContent:      fmt.Sprintf("model retried a previously rejected %s call %d times (%s) — forcing answer with the evidence already gathered", req.Tool, retries, priorSummary),
			ForceAnswerReason: "rejection_retry_blocked",
			ForceAnswerPrompt: fmt.Sprintf("You retried a %s request that was already rejected %d times this turn and were warned. Further tools are not available. Provide your final answer from the evidence already gathered.", req.Tool, retries),
		}
	}
	return runnerRequestBudgetDecision{}
}

func (s *runnerRequestBudgetState) terminalArtifactRejectedRetryDecision(req workspace.ToolRequest, currentFingerprint string) runnerRequestBudgetDecision {
	if s == nil || strings.TrimSpace(currentFingerprint) == "" {
		return runnerRequestBudgetDecision{}
	}
	priorSummary, seen := s.fingerprint.rejected[currentFingerprint]
	if !seen || !terminalArtifactRequiredRejectionSummary(priorSummary) {
		return runnerRequestBudgetDecision{}
	}
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "the requested tool"
	}
	return runnerRequestBudgetDecision{
		Handled:      true,
		EventRole:    "terminal_artifact_retry_recovery",
		EventContent: fmt.Sprintf("model retried %s after terminal artifact recovery rejected it", tool),
		Nudge:        terminalArtifactRejectedRetryNudge(tool, priorSummary),
	}
}

func terminalArtifactRejectedRetryNudge(tool string, priorSummary string) string {
	if strings.TrimSpace(priorSummary) == "Corrected submit_plan required" {
		return fmt.Sprintf(
			"The repeated %s request was already rejected because submit-correction mode requires a corrected terminal plan artifact. Emit the currently allowed terminal artifact (`submit_plan`) now. Do not answer `Decision: source-satisfied`, do not answer `<<QUESTION>>:`, and do not report the rejected request as a blocker.",
			tool,
		)
	}
	return fmt.Sprintf(
		"The repeated %s request was already rejected because the current planner phase requires a terminal plan artifact. Do not report that rejected request as a blocker. Emit the currently allowed terminal artifact (`submit_plan`) now, or use `Decision: source-satisfied` / `<<QUESTION>>:` only for a true terminal exception grounded in the evidence already gathered.",
		tool,
	)
}

func terminalArtifactRequiredRejectionSummary(summary string) bool {
	summary = strings.TrimSpace(strings.TrimSuffix(summary, " (replayed same blocker)"))
	switch summary {
	case "Plan-execute submit_plan required",
		"Corrected submit_plan required",
		"Plan submission required":
		return true
	default:
		return false
	}
}

func (s *runnerRequestBudgetState) earlyRejectedFingerprintDecision(req workspace.ToolRequest, currentFingerprint string) runnerRequestBudgetDecision {
	if s == nil || strings.TrimSpace(currentFingerprint) == "" {
		return runnerRequestBudgetDecision{}
	}
	priorSummary, seen := s.fingerprint.rejected[currentFingerprint]
	if !seen || !earlyRejectedFingerprintSummary(priorSummary) {
		return runnerRequestBudgetDecision{}
	}
	return s.rejectedFingerprintDecision(req, currentFingerprint)
}

func earlyRejectedFingerprintSummary(summary string) bool {
	summary = strings.TrimSpace(summary)
	switch summary {
	case "Plan-execute submit_plan required",
		"Tool not available in current runtime contract",
		"Tool hidden by active contract",
		"Write-only recovery blocked non-write tool",
		"Repeated range-less read_file redirected to bounded source request",
		"Repeated range-less read_file rejected":
		return true
	default:
		return false
	}
}

func isRecoverablePostReadinessGapRejection(summary string) bool {
	return postReadinessContinuationRejectionSummary(summary)
}

func normalizedHarnessRejectionSummary(summary string) string {
	summary = strings.TrimSpace(summary)
	return strings.TrimSpace(strings.TrimSuffix(summary, " (replayed same blocker)"))
}

func postReadinessContinuationRejectionSummary(summary string) bool {
	summary = normalizedHarnessRejectionSummary(summary)
	return strings.HasPrefix(summary, "Post-readiness follow-up ") ||
		strings.HasPrefix(summary, "Post-readiness named_gap ") ||
		strings.HasPrefix(summary, "Post-readiness search ") ||
		strings.HasPrefix(summary, "Post-readiness exact probe ") ||
		strings.HasPrefix(summary, "Post-child follow-up ")
}

func (s *runnerRequestBudgetState) explorationDecision(r *Runner, req workspace.ToolRequest, results []workspace.ToolResult) runnerRequestBudgetDecision {
	var decision runnerRequestBudgetDecision
	if !s.broadEvidenceDelegationRequired {
		if nudge := r.explorationDelegationNudge(s.readCallsSinceWrite, results); nudge != "" {
			s.broadEvidenceDelegationRequired = true
			decision = runnerRequestBudgetDecision{
				Handled:   true,
				Nudge:     nudge,
				EventRole: "exploration_task_delegation_nudge",
			}
		}
	}
	if s.broadEvidenceDelegationRequired && !s.broadEvidenceDelegationSatisfied && workspace.IsBroadInspectionRequest(req) {
		return runnerRequestBudgetDecision{
			Handled:          true,
			RejectSummary:    "Broad exploration after delegation nudge blocked",
			RejectError:      "broad inspection has already been nudged on this turn. Answer from the evidence gathered, or take one narrower next step naming the exact missing claim it closes.",
			RejectStopReason: "exploration_broad_inspection_after_nudge_blocked",
		}
	}
	mutationParent := runnerExplorationParentIsMutation(r)
	if s.postChildActive() && workspace.IsBroadInspectionRequest(req) && !mutationParent {
		return runnerRequestBudgetDecision{
			Handled:          true,
			RejectSummary:    "Post-child broad inspection blocked",
			RejectError:      "the read_only child task returned the broad evidence packet. Continue from that packet with one targeted read or search for a named gap, make the required change, or answer with the current evidence.",
			RejectStopReason: "post_child_broad_inspection_blocked",
		}
	}
	if s.readOnlyChildTaskSatisfied && strings.TrimSpace(req.Tool) == "task" && !isWriteChildMode(req.Mode) {
		return runnerRequestBudgetDecision{
			Handled:          true,
			RejectSummary:    "Additional read_only child task blocked",
			RejectError:      "this turn already has a successful read_only child evidence packet. Continue from that packet with at most a couple of targeted follow-ups, or answer with the current evidence.",
			RejectStopReason: "duplicate_read_only_child_task_blocked",
		}
	}
	if s.postChildActive() {
		if decision := s.postChildGuidanceDecision(req, mutationParent); decision.Handled {
			return decision
		}
	}
	return decision
}

// runnerExplorationParentIsMutation reports whether the parent lane
// observing this explorationDecision call has a mutation brief AND is
// itself the parent (not a child:* delegated agent). Mutation parents
// consume child packets to commit a WRITE — the child's evidence is a
// hint, not a hard contract. Suppressing the post-child blocks for
// these parents lets them broaden/probe outside the packet's evidence
// paths when their own RequiredEditPaths need it. The completion gate
// owns mutation closure; doom_loop_guard + MaxToolSteps own the
// runaway-exploration ceiling. Same logic as
// postReadinessToolContractForBrief — keep source-refresh gating centralized.
func runnerExplorationParentIsMutation(r *Runner) bool {
	if r == nil {
		return false
	}
	if strings.HasPrefix(strings.TrimSpace(r.agentName()), "child:") {
		return false
	}
	return runnerBriefRequiresMutation(r.RuntimeBrief)
}

func (s *runnerRequestBudgetState) synthesisDecision(policy RunPolicy, toolCalls int, mutation bool) runnerRequestBudgetDecision {
	if policy.SynthesisNudgeAt > 0 && toolCalls >= policy.SynthesisNudgeAt && !s.synthesisNudgeFired {
		s.synthesisNudgeFired = true
		nudgeText := fmt.Sprintf("You have used %d tool calls this turn. Before more tool calls, pause: summarize what you've learned so far, state the specific question that remains, and commit to the minimal next step. If the remaining gap is small, answer now with what you have.", toolCalls)
		if mutation {
			nudgeText = fmt.Sprintf("You have used %d tool calls this turn. Pause: name the exact edit target you've identified (file and line range), and commit to the smallest necessary edit. If you know the target and the change, write it now — do not keep reading. If you're uncertain, state the exact blocker.", toolCalls)
		}
		return runnerRequestBudgetDecision{
			Handled:   true,
			Nudge:     nudgeText,
			EventRole: "synthesis_nudge",
		}
	}
	return runnerRequestBudgetDecision{}
}

func (s *runnerRequestBudgetState) noteReadClassRequest(req workspace.ToolRequest) {
	s.readCallCount++
	s.readCallsSinceWrite++
	if s.postChildActive() {
		s.postChild.readCalls++
		if family := postChildRequestFamilyKey(req); family != "" {
			s.postChild.readFamilies[family]++
		}
	}
}

// readBudgetDecision used to force-finish lanes after a fixed post-
// child read budget. Per the project's "no bandaid budgets" memory:
// budget-style caps are bandaids; the structural enforcers
// (MaxToolSteps, doom_loop_guard, completion_gate) are the real
// closure contract. The hard cap fired prematurely on mutation
// parents that legitimately needed several anchor-verification reads
// after a child:explorer evidence packet (see #101 wedges). The
// completion gate enforces RequiredEditPaths on those parents, and
// doom_loop_guard catches genuine read-loops. Returning the empty
// decision here keeps the post-child counters tracking for telemetry
// + diminishing-returns nudges without force-finishing the lane.
func (s *runnerRequestBudgetState) readBudgetDecision(policy RunPolicy, req workspace.ToolRequest) runnerRequestBudgetDecision {
	return runnerRequestBudgetDecision{}
}

func (s *runnerRequestBudgetState) noteVerification() {
	s.readCallsSinceWrite = 0
}

func (s *runnerRequestBudgetState) noteWriteProgress() {
	s.readCallsSinceWrite = 0
	s.writeReadyNudgeActive = false
	// A successful write moves the lane from target/source acquisition
	// into post-write verification. Rejections from the pre-write phase
	// must not poison verification tools with stale retry fingerprints.
	clear(s.fingerprint.rejected)
	clear(s.fingerprint.rejectedRetries)
}

func (s *runnerRequestBudgetState) noteReadOnlyChildPacket(paths []string) {
	s.postChild.evidencePaths = normalizePostChildEvidencePaths(paths)
	s.postChild.readFamilies = make(map[string]int)
	s.postChild.familyNudgeFired = make(map[string]bool)
}

func (s *runnerRequestBudgetState) postChildGuidanceDecision(req workspace.ToolRequest, mutationParent bool) runnerRequestBudgetDecision {
	if !mutationParent && postChildUnrelatedDiscovery(req, s.postChild.evidencePaths) {
		return runnerRequestBudgetDecision{
			Handled:          true,
			RejectSummary:    "Post-child unrelated discovery blocked",
			RejectError:      fmt.Sprintf("the delegated read_only child already named the relevant evidence paths (%s). Answer now, or inspect one of those paths directly for a named gap.", summarizePostChildEvidencePaths(s.postChild.evidencePaths, 3)),
			RejectStopReason: "post_child_unrelated_discovery_blocked",
		}
	}
	family := postChildRequestFamilyKey(req)
	if family == "" {
		return runnerRequestBudgetDecision{}
	}
	label := postChildRequestFamilyLabel(req)
	if label == "" {
		label = "the same evidence path"
	}
	// The post-child reread BLOCK was a bandaid budget that fired after
	// the model re-read the same file family 2× post-packet. Per the
	// no-bandaid memory, the structural enforcers (doom_loop_guard for
	// exact-duplicate reads, MaxToolSteps for total budget, completion
	// gate for mutation closure) own the closure contract. The nudge
	// below (educational, fires once) is kept; the hard block is gone.
	if count := s.postChild.readFamilies[family]; count >= 1 && !s.postChild.familyNudgeFired[family] {
		s.postChild.familyNudgeFired[family] = true
		return runnerRequestBudgetDecision{
			Handled:   true,
			Nudge:     fmt.Sprintf("you already inspected %s after the child packet. Either answer now, or take one narrow probe that names the exact missing claim; the same path is fine if you can name what new line you expect.", label),
			EventRole: "post_child_reread_nudge",
		}
	}
	return runnerRequestBudgetDecision{}
}

func postChildUnrelatedDiscovery(req workspace.ToolRequest, evidencePaths []string) bool {
	switch strings.TrimSpace(req.Tool) {
	case "search_files", "search_content", "rg", "grep":
		return len(evidencePaths) > 0 && !requestAnchoredToEvidencePaths(req, evidencePaths)
	default:
		return false
	}
}

func requestAnchoredToEvidencePaths(req workspace.ToolRequest, evidencePaths []string) bool {
	reqPath := normalizePostChildPath(req.Path)
	queryText := strings.ToLower(strings.Join([]string{
		req.Path,
		req.Pattern,
		req.Query,
		req.Command,
	}, "\n"))
	for _, evidencePath := range evidencePaths {
		if evidencePath == "" {
			continue
		}
		if reqPath != "" && postChildPathsOverlap(reqPath, evidencePath) {
			return true
		}
		for _, token := range evidenceAnchorTokens(evidencePath) {
			if token != "" && strings.Contains(queryText, token) {
				return true
			}
		}
	}
	return false
}

func postChildRequestFamilyKey(req workspace.ToolRequest) string {
	switch strings.TrimSpace(req.Tool) {
	case "read_file", "go_read_symbol", "go_file_outline", "python_read_symbol", "python_file_outline",
		"ts_read_symbol", "ts_file_outline", "rust_read_symbol", "rust_file_outline",
		"java_read_symbol", "java_file_outline", "cpp_read_symbol":
		if path := normalizePostChildPath(req.Path); path != "" {
			return strings.TrimSpace(req.Tool) + ":" + path
		}
	case "search_content", "search_files", "rg", "grep":
		if path := normalizePostChildPath(req.Path); path != "" && path != "." {
			return strings.TrimSpace(req.Tool) + ":" + path
		}
	}
	return ""
}

func postChildRequestFamilyLabel(req workspace.ToolRequest) string {
	if path := normalizePostChildPath(req.Path); path != "" && path != "." {
		return path
	}
	return strings.TrimSpace(req.Tool)
}

func normalizePostChildEvidencePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	normalized := make([]string, 0, len(paths))
	for _, path := range paths {
		cleaned := normalizePostChildPath(path)
		if cleaned == "" {
			continue
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		normalized = append(normalized, cleaned)
	}
	return normalized
}

func summarizePostChildEvidencePaths(paths []string, max int) string {
	if len(paths) == 0 {
		return "the child packet paths"
	}
	limit := len(paths)
	if max > 0 && limit > max {
		limit = max
	}
	quoted := make([]string, 0, limit+1)
	for _, path := range paths[:limit] {
		quoted = append(quoted, "`"+path+"`")
	}
	if len(paths) > limit {
		quoted = append(quoted, "and related nearby files")
	}
	return strings.Join(quoted, ", ")
}

func normalizePostChildPath(path string) string {
	cleaned := strings.TrimSpace(path)
	if cleaned == "" {
		return ""
	}
	cleaned = filepath.ToSlash(filepath.Clean(cleaned))
	cleaned = strings.TrimPrefix(cleaned, "./")
	if cleaned == "" {
		return ""
	}
	return cleaned
}

func postChildPathsOverlap(reqPath string, evidencePath string) bool {
	if reqPath == evidencePath {
		return true
	}
	if strings.HasPrefix(reqPath+"/", evidencePath+"/") || strings.HasPrefix(evidencePath+"/", reqPath+"/") {
		return true
	}
	return filepath.ToSlash(filepath.Dir(evidencePath)) == reqPath
}

func evidenceAnchorTokens(path string) []string {
	seen := make(map[string]struct{})
	add := func(tokens []string, token string) []string {
		token = strings.ToLower(strings.TrimSpace(token))
		if len(token) < 4 {
			return tokens
		}
		switch token {
		case "internal", "scripts", "script", "tests", "docs", "vendor":
			return tokens
		}
		if _, ok := seen[token]; ok {
			return tokens
		}
		seen[token] = struct{}{}
		return append(tokens, token)
	}

	var tokens []string
	base := filepath.Base(path)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	for _, candidate := range []string{base, stem} {
		tokens = add(tokens, candidate)
	}
	replacer := strings.NewReplacer("/", " ", "_", " ", "-", " ", ".", " ")
	for _, part := range strings.Fields(replacer.Replace(path)) {
		tokens = add(tokens, part)
	}
	return tokens
}
