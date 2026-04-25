package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"idiom/internal/workspace"
)

type runnerRequestBudgetState struct {
	lastRequestFingerprint           string
	identicalCallRun                 int
	fingerprintCounts                map[string]int
	fingerprintNudgeFired            map[string]bool
	broadEvidenceDelegationRequired  bool
	broadEvidenceDelegationSatisfied bool
	readOnlyChildTaskSatisfied       bool
	synthesisNudgeFired              bool
	readBudgetNudgeFired             bool
	readCallCount                    int
	readCallsSinceWrite              int
	postChildReadCalls               int
	postChildReadBudget              int
	postChildEvidencePaths           []string
	postChildReadFamilies            map[string]int
	postChildReadFamilyNudgeFired    map[string]bool
	zeroHitFingerprints              map[string]string
	rejectedFingerprints             map[string]string
	// postReadinessNoGapRejections counts the number of times this turn
	// the model has been rejected for issuing a post-readiness read/search
	// without `named_gap`. After the first such rejection the contract is
	// in the model's context; if the model still emits another no-gap
	// read, more rejection prose isn't going to teach it. Triggers a
	// fast-track force-answer at threshold (≥2 — the second offense).
	postReadinessNoGapRejections int
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
	Nudge             string
	EventRole         string
	EventContent      string
}

func newRunnerRequestBudgetState(postChildReadBudget int) runnerRequestBudgetState {
	return runnerRequestBudgetState{
		fingerprintCounts:             make(map[string]int),
		fingerprintNudgeFired:         make(map[string]bool),
		postChildReadBudget:           postChildReadBudget,
		postChildReadFamilies:         make(map[string]int),
		postChildReadFamilyNudgeFired: make(map[string]bool),
		zeroHitFingerprints:           make(map[string]string),
		rejectedFingerprints:          make(map[string]string),
	}
}

func (s *runnerRequestBudgetState) noteFingerprint(currentFingerprint string) int {
	if currentFingerprint != "" && currentFingerprint == s.lastRequestFingerprint {
		s.identicalCallRun++
	} else {
		s.identicalCallRun = 1
		s.lastRequestFingerprint = currentFingerprint
	}
	if currentFingerprint != "" {
		s.fingerprintCounts[currentFingerprint]++
	}
	return s.identicalCallRun
}

func (s *runnerRequestBudgetState) fingerprintDecision(policy RunPolicy, req workspace.ToolRequest, currentFingerprint string) runnerRequestBudgetDecision {
	if currentFingerprint == "" {
		return runnerRequestBudgetDecision{}
	}
	if policy.FingerprintNudgeAt > 0 {
		count := s.fingerprintCounts[currentFingerprint]
		nudgeFired := s.fingerprintNudgeFired[currentFingerprint]
		if count >= policy.FingerprintNudgeAt && !nudgeFired {
			s.fingerprintNudgeFired[currentFingerprint] = true
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
				ForceAnswerPrompt: fmt.Sprintf("You called %s with identical arguments %d times after being warned. Tools are now disabled. Write the normal final answer from the evidence already gathered. If the repeated request reflects one unsupported claim, state that uncertainty after the supported answer with the smallest useful next action.", req.Tool, count),
			}
		}
	}
	if priorSummary, seen := s.zeroHitFingerprints[currentFingerprint]; seen {
		return runnerRequestBudgetDecision{
			Handled:          true,
			RejectSummary:    "Zero-hit retry blocked",
			RejectError:      fmt.Sprintf("this exact %s call already returned no results earlier this turn (\"%s\"). Repeating it will not find new matches. Broaden or change the pattern, search a different scope, or answer with the evidence you have.", req.Tool, priorSummary),
			RejectStopReason: "zero_hit_retry_budget_exhausted",
		}
	}
	if priorSummary, seen := s.rejectedFingerprints[currentFingerprint]; seen {
		if isRecoverablePostReadinessGapRejection(priorSummary) && postReadinessGap(req) != "" {
			delete(s.rejectedFingerprints, currentFingerprint)
			return runnerRequestBudgetDecision{}
		}
		return runnerRequestBudgetDecision{
			Handled:           true,
			EventRole:         "rejection_retry_blocked",
			EventContent:      fmt.Sprintf("model retried a previously rejected %s call (%s) — forcing answer with the evidence already gathered", req.Tool, priorSummary),
			ForceAnswerReason: "rejection_retry_blocked",
			ForceAnswerPrompt: fmt.Sprintf("You retried a %s request that was already rejected this turn (\"%s\"). Tools are now disabled. Treat the rejected request as adding no new evidence, and write the normal final answer from the evidence already gathered. Mention the rejected probe only if it leaves one user-relevant claim unsupported.", req.Tool, priorSummary),
		}
	}
	return runnerRequestBudgetDecision{}
}

func isRecoverablePostReadinessGapRejection(summary string) bool {
	summary = strings.TrimSpace(summary)
	summary = strings.TrimSuffix(summary, " (replayed same blocker)")
	switch summary {
	case "Post-readiness follow-up missing named gap", "Post-child follow-up missing named gap":
		return true
	default:
		return false
	}
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
	if s.postChildActive() && workspace.IsBroadInspectionRequest(req) {
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
		if decision := s.postChildGuidanceDecision(req); decision.Handled {
			return decision
		}
	}
	return decision
}

func (s *runnerRequestBudgetState) synthesisDecision(policy RunPolicy, toolCalls int) runnerRequestBudgetDecision {
	if policy.SynthesisNudgeAt > 0 && toolCalls >= policy.SynthesisNudgeAt && !s.synthesisNudgeFired {
		s.synthesisNudgeFired = true
		return runnerRequestBudgetDecision{
			Handled:   true,
			Nudge:     fmt.Sprintf("You have used %d tool calls this turn. Before more tool calls, pause: summarize what you've learned so far, state the specific question that remains, and commit to the minimal next step. If the remaining gap is small, answer now with what you have.", toolCalls),
			EventRole: "synthesis_nudge",
		}
	}
	return runnerRequestBudgetDecision{}
}

func (s *runnerRequestBudgetState) noteReadClassRequest(req workspace.ToolRequest) {
	s.readCallCount++
	s.readCallsSinceWrite++
	if s.postChildActive() {
		s.postChildReadCalls++
		if family := postChildRequestFamilyKey(req); family != "" {
			s.postChildReadFamilies[family]++
		}
	}
}

func (s *runnerRequestBudgetState) readBudgetDecision(policy RunPolicy, req workspace.ToolRequest) runnerRequestBudgetDecision {
	if s.postChildActive() && s.postChildReadCalls > s.postChildReadBudget {
		return runnerRequestBudgetDecision{
			Handled:           true,
			EventRole:         "post_child_read_budget_block",
			EventContent:      fmt.Sprintf("post-child read budget exhausted after %d targeted reads", s.postChildReadBudget),
			RejectSummary:     "Post-child read budget exhausted",
			RejectError:       fmt.Sprintf("the child evidence packet has already been gathered and the parent used %d targeted follow-up reads. Synthesize from the child packet and targeted follow-up evidence; mention only one unsupported claim if the answer still needs it.", s.postChildReadBudget),
			RejectStopReason:  "post_child_read_budget_exhausted",
			ForceAnswerReason: "post_child_read_budget_exhausted",
			ForceAnswerPrompt: "Post-child read budget is exhausted. Tools are now disabled. Use the child packet and targeted follow-up evidence to write the normal final answer. If one exact claim remains unsupported, state that uncertainty after the answer.",
		}
	}
	if policy.ReadBudgetSoft > 0 && s.readCallCount >= policy.ReadBudgetSoft && !s.readBudgetNudgeFired {
		s.readBudgetNudgeFired = true
		return runnerRequestBudgetDecision{
			Handled:   true,
			Nudge:     fmt.Sprintf("You have made %d read-class tool calls this turn. Before more reads, update your plan: summarize what you've learned, state the remaining unknown, and commit to the minimal next step. In plan mode, the output is the plan — not another round of reads.", s.readCallCount),
			EventRole: "read_budget_nudge",
		}
	}
	if policy.ReadBudgetHard > 0 && s.readCallsSinceWrite > policy.ReadBudgetHard {
		return runnerRequestBudgetDecision{
			Handled:           true,
			EventRole:         "read_budget_hard_block",
			EventContent:      fmt.Sprintf("read-class budget exhausted after %d read/search calls without a write", s.readCallsSinceWrite-1),
			RejectSummary:     "Read budget exhausted",
			RejectError:       fmt.Sprintf("you already used %d read/search tool calls without making a write. Use the gathered evidence to answer or proceed; mention only one unsupported claim if the answer cannot be completed.", s.readCallsSinceWrite-1),
			RejectStopReason:  "read_budget_exhausted",
			ForceAnswerReason: "read_budget_exhausted",
			ForceAnswerPrompt: "Read/search budget is exhausted. Tools are now disabled. Write the normal final answer from the evidence already gathered. If the evidence is insufficient for one exact claim, state that uncertainty after the supported answer with the smallest useful next action.",
		}
	}
	return runnerRequestBudgetDecision{}
}

func (s *runnerRequestBudgetState) noteVerification() {
	s.readCallsSinceWrite = 0
}

func (s *runnerRequestBudgetState) noteWriteProgress() {
	s.readCallsSinceWrite = 0
}

func (s *runnerRequestBudgetState) noteReadOnlyChildPacket(paths []string) {
	s.postChildEvidencePaths = normalizePostChildEvidencePaths(paths)
	s.postChildReadFamilies = make(map[string]int)
	s.postChildReadFamilyNudgeFired = make(map[string]bool)
}

func (s *runnerRequestBudgetState) postChildGuidanceDecision(req workspace.ToolRequest) runnerRequestBudgetDecision {
	if postChildUnrelatedDiscovery(req, s.postChildEvidencePaths) {
		return runnerRequestBudgetDecision{
			Handled:          true,
			RejectSummary:    "Post-child unrelated discovery blocked",
			RejectError:      fmt.Sprintf("the delegated read_only child already named the relevant evidence paths (%s). Answer now, or inspect one of those paths directly for a named gap.", summarizePostChildEvidencePaths(s.postChildEvidencePaths, 3)),
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
	if count := s.postChildReadFamilies[family]; count >= 2 && s.postChildReadFamilyNudgeFired[family] {
		return runnerRequestBudgetDecision{
			Handled:          true,
			RejectSummary:    "Post-child reread blocked",
			RejectError:      fmt.Sprintf("you already revisited %s multiple times after the child packet. Answer now, or take one narrow probe on a different evidence path that names the exact missing claim.", label),
			RejectStopReason: "post_child_reread_blocked",
		}
	}
	if count := s.postChildReadFamilies[family]; count >= 1 && !s.postChildReadFamilyNudgeFired[family] {
		s.postChildReadFamilyNudgeFired[family] = true
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
