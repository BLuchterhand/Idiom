package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/textutil"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

const loopPolicyInvariantRepeatedCall = "loop.repeated_call"

type fingerprintTrackingState struct {
	lastFingerprint string
	identicalRun    int
	counts          map[string]int
	nudgeFired      map[string]bool
	zeroHit         map[string]string
	zeroHitFamily   map[string]zeroHitFamilyState
	rejected        map[string]rejectedFingerprint
	// rejectedRetries counts how many times a fingerprint has been
	// retried after a prior rejection. Nudge on first retry, force-answer
	// on the second — matching the nudge-then-block pattern.
	rejectedRetries map[string]int
}

type rejectedFingerprint struct {
	Tool        string
	Summary     string
	Disposition toolfacts.ToolRejectionDisposition
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
	// write-ready escalation contract instead of force-answering on the
	// first rejection retry.
	writeReadyNudgeActive bool

	broadEvidenceDelegationRequired  bool
	broadEvidenceDelegationSatisfied bool
	readOnlyChildTaskSatisfied       bool
	synthesisNudgeFired              bool
	readCallCount                    int
	readCallsSinceWrite              int
	scopedReadRejections             map[string]int
	recoveryBrief                    workerruntime.WorkerRuntimeBrief
	recoveryResults                  []workspace.ToolResult
}

func (s runnerRequestBudgetState) postChildActive() bool {
	return s.readOnlyChildTaskSatisfied || s.broadEvidenceDelegationSatisfied
}

func (s *runnerRequestBudgetState) setRecoveryContext(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) {
	if s == nil {
		return
	}
	s.recoveryBrief = brief
	s.recoveryResults = append([]workspace.ToolResult(nil), results...)
}

func (s *runnerRequestBudgetState) recordRejectedFingerprint(currentFingerprint string, req workspace.ToolRequest, result workspace.ToolResult) {
	if s == nil || strings.TrimSpace(currentFingerprint) == "" {
		return
	}

	disposition := toolfacts.ToolRejectionDisposition("")
	if result.Facts != nil {
		disposition = result.Facts.RejectionDisposition
	}
	if disposition == "" {
		disposition = toolfacts.ToolRejectionNotRetained
	}
	s.fingerprint.rejected[currentFingerprint] = rejectedFingerprint{
		Tool:        strings.TrimSpace(req.Tool),
		Summary:     truncForRetryReason(result.Summary),
		Disposition: disposition,
	}
}

func (s *runnerRequestBudgetState) clearRejectedFingerprint(currentFingerprint string, disposition toolfacts.ToolRejectionDisposition) bool {
	if s == nil || strings.TrimSpace(currentFingerprint) == "" || disposition == "" {
		return false
	}

	prior, seen := s.fingerprint.rejected[currentFingerprint]
	if !seen || prior.Disposition != disposition {
		return false
	}
	delete(s.fingerprint.rejected, currentFingerprint)
	delete(s.fingerprint.rejectedRetries, currentFingerprint)
	return true
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
	Policy            runnerLoopPolicyDecision
}

type runnerLoopPolicyDecision struct {
	ContractID  autonomy.ContractPackID
	InvariantID string
	Decision    autonomy.ToolPolicyDecision
	ReasonCode  string
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

	if ctx.controller != nil && ctx.controller.runner != nil && ctx.controller.runner.state.autonomyRecorder != nil && d.Policy.InvariantID != "" {
		ctx.controller.runner.state.autonomyRecorder.emitPolicyResult(ctx.req, runnerAutonomyPolicyEventInput{
			Decision:    d.Policy.Decision,
			PackID:      d.Policy.ContractID,
			InvariantID: d.Policy.InvariantID,
			ReasonCode:  d.Policy.ReasonCode,
			Result:      runnerLoopPolicyResultLabel(d.Policy.Decision),
			ErrorClass:  "loop_policy",
			Source:      "runner_loop_policy",
		})
	}

	if d.Nudge != "" && ctx.controller != nil && ctx.controller.runner != nil {
		ctx.controller.runner.state.turnMessages = append(ctx.controller.runner.state.turnMessages, llama.ChatMessage{Role: "system", Content: d.Nudge})
	}

	if d.EventRole != "" {
		ctx.controller.runner.emitEvent(RunEvent{Role: d.EventRole, Content: textutil.FirstNonEmpty(d.EventContent, d.Nudge)})
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
		answer, err, done := ctx.controller.rejectTool(ctx.stepNumber, ctx.activeToolCall, ctx.req, runnerToolRejection{
			Summary: d.RejectSummary,
			Error:   d.RejectError,
		})
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

func runnerLoopPolicyResultLabel(decision autonomy.ToolPolicyDecision) string {
	switch decision {
	case autonomy.PolicyObserve:
		return "observed"
	case autonomy.PolicyReject:
		return "rejected"
	case autonomy.PolicyStop:
		return "stopped"
	default:
		return strings.TrimSpace(string(decision))
	}
}

func newRunnerRequestBudgetState(postChildReadBudget int) runnerRequestBudgetState {
	return runnerRequestBudgetState{
		fingerprint: fingerprintTrackingState{
			counts:          make(map[string]int),
			nudgeFired:      make(map[string]bool),
			zeroHit:         make(map[string]string),
			zeroHitFamily:   make(map[string]zeroHitFamilyState),
			rejected:        make(map[string]rejectedFingerprint),
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

func attachLoopPolicyDecision(decision runnerRequestBudgetDecision) runnerRequestBudgetDecision {
	if !decision.Handled {
		return decision
	}
	policyDecision := autonomy.PolicyObserve
	if decision.ForceAnswerReason != "" || decision.RejectSummary != "" {
		policyDecision = autonomy.PolicyStop
		if decision.RejectSummary != "" {
			policyDecision = autonomy.PolicyReject
		}
	}
	reason := strings.TrimSpace(decision.EventRole)
	if reason == "" {
		reason = strings.TrimSpace(decision.ForceAnswerReason)
	}
	if reason == "" {
		reason = strings.TrimSpace(decision.RejectStopReason)
	}
	decision.Policy = runnerLoopPolicyDecision{
		ContractID:  autonomy.ContractPackLoopRepeatedCall,
		InvariantID: loopPolicyInvariantRepeatedCall,
		Decision:    policyDecision,
		ReasonCode:  reason,
	}
	return decision
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

func (s *runnerRequestBudgetState) fingerprintDecision(policy RunPolicy, req workspace.ToolRequest, currentFingerprint string) runnerRequestBudgetDecision {
	if currentFingerprint == "" {
		return runnerRequestBudgetDecision{}
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
	if !workspace.IsSearchToolName(req.Tool) {
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

		terms = textutil.AppendUnique(terms, word)
		if len(terms) >= 3 {
			break
		}
	}

	return terms
}

func splitZeroHitSearchWords(text string) []string {
	var (
		words []string
		b     strings.Builder
	)

	flush := func() {
		if b.Len() == 0 {
			return
		}

		word := strings.ToLower(b.String())
		b.Reset()

		if len(word) < 3 {
			return
		}

		words = textutil.AppendUnique(words, word)
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

	if prior, ok := s.fingerprint.rejected[currentFingerprint]; ok {
		priorSummary := prior.Summary
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

func (s *runnerRequestBudgetState) earlyRejectedFingerprintDecision(req workspace.ToolRequest, currentFingerprint string) runnerRequestBudgetDecision {
	if s == nil || strings.TrimSpace(currentFingerprint) == "" {
		return runnerRequestBudgetDecision{}
	}

	prior, seen := s.fingerprint.rejected[currentFingerprint]
	if !seen || !prior.Disposition.PhaseLocal() {
		return runnerRequestBudgetDecision{}
	}

	return s.rejectedFingerprintDecision(req, currentFingerprint)
}

// clearStaleToolSurfaceRejection releases only a phase-local surface denial
// after the same tool becomes callable again. The rejected ToolResult remains
// in evidence for completion accounting and audit; only the stale retry guard
// is removed.
func (s *runnerRequestBudgetState) clearStaleToolSurfaceRejection(req workspace.ToolRequest, currentFingerprint string, allowedTools []string) bool {
	if s == nil || strings.TrimSpace(currentFingerprint) == "" {
		return false
	}

	prior, seen := s.fingerprint.rejected[currentFingerprint]
	if !seen || !prior.Disposition.PhaseLocal() {
		return false
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" || strings.TrimSpace(prior.Tool) != tool {
		return false
	}
	toolAvailable := false
	for _, allowed := range allowedTools {
		if strings.TrimSpace(allowed) == tool {
			toolAvailable = true
			break
		}
	}
	if !toolAvailable {
		return false
	}

	return s.clearRejectedFingerprint(currentFingerprint, toolfacts.ToolRejectionDeferredByPhase)
}

func (s *runnerRequestBudgetState) synthesisDecision(policy RunPolicy, toolCalls int, mutation bool) runnerRequestBudgetDecision {
	if policy.SynthesisNudgeAt > 0 && toolCalls >= policy.SynthesisNudgeAt && !s.synthesisNudgeFired {
		s.synthesisNudgeFired = true

		nudgeText := fmt.Sprintf("You have used %d tool calls this turn. Before more tool calls, pause: summarize what you've learned so far, state the specific question that remains, and commit to the minimal next step. If the remaining gap is small, answer now with what you have.", toolCalls)

		if mutation {
			nudgeText = fmt.Sprintf("You have used %d tool calls this turn. Pause: name the exact edit target you've identified (file and line range), and commit to the smallest necessary edit. If you know the target and the change, write it now. If one edit-safety fact is still missing, take a bounded target-scoped probe for that fact; if the harness cannot provide it, state the exact blocker.", toolCalls)
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
	clear(s.fingerprint.zeroHit)
	clear(s.fingerprint.zeroHitFamily)
}

func (s *runnerRequestBudgetState) noteReadOnlyChildPacket(paths []string) {
	s.postChild.evidencePaths = normalizePostChildEvidencePaths(paths)
	s.postChild.readFamilies = make(map[string]int)
	s.postChild.familyNudgeFired = make(map[string]bool)
}

func postChildRequestFamilyKey(req workspace.ToolRequest) string {
	if workspace.IsSearchToolName(req.Tool) {
		if path := normalizePostChildPath(req.Path); path != "" && path != "." {
			return strings.TrimSpace(req.Tool) + ":" + path
		}
	}

	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameReadFile, "go_read_symbol", "go_file_outline", "python_read_symbol", "python_file_outline",
		"ts_read_symbol", "ts_file_outline", "rust_read_symbol", "rust_file_outline",
		"java_read_symbol", "java_file_outline", "cpp_read_symbol":
		if path := normalizePostChildPath(req.Path); path != "" {
			return strings.TrimSpace(req.Tool) + ":" + path
		}
	}

	return ""
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
