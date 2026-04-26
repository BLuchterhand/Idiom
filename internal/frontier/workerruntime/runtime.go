package workerruntime

import (
	"encoding/json"
	"strings"

	"idiom/internal/workspace"
)

type ToolResultSummary struct {
	Tool        string `json:"tool,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Error       string `json:"error,omitempty"`
	OutputChars int    `json:"output_chars,omitempty"`
}

type ToolManifestEntry = workspace.ToolManifestEntry

type BudgetBrief struct {
	MaxToolSteps       int `json:"max_tool_steps,omitempty"`
	MaxModelCalls      int `json:"max_model_calls,omitempty"`
	MaxContextMessages int `json:"max_context_messages,omitempty"`
	MaxContextChars    int `json:"max_context_chars,omitempty"`
}

type WorkerRuntimeBrief struct {
	Lane                 string              `json:"lane,omitempty"`
	TaskFamily           string              `json:"task_family,omitempty"`
	Contract             string              `json:"contract,omitempty"`
	Objective            string              `json:"objective,omitempty"`
	PermissionProfile    string              `json:"permission_profile,omitempty"`
	TargetRefs           []string            `json:"target_refs,omitempty"`
	EvidencePaths        []string            `json:"evidence_paths,omitempty"`
	ChangedPaths         []string            `json:"changed_paths,omitempty"`
	UserBoundaries       []string            `json:"user_boundaries,omitempty"`
	AllowedTools         []string            `json:"allowed_tools,omitempty"`
	ForbiddenTools       []string            `json:"forbidden_tools,omitempty"`
	ToolManifest         []ToolManifestEntry `json:"tool_manifest,omitempty"`
	RequiredEvidence     []string            `json:"required_evidence,omitempty"`
	RequiredTools        []string            `json:"required_tools,omitempty"`
	ReadinessGate        []string            `json:"readiness_gate,omitempty"`
	SearchPolicy         []string            `json:"search_policy,omitempty"`
	DiscoveryExamples    []string            `json:"discovery_examples,omitempty"`
	VerificationContract string              `json:"verification_contract,omitempty"`
	VerificationPlan     []string            `json:"verification_plan,omitempty"`
	StopConditions       []string            `json:"stop_conditions,omitempty"`
	FinalCritic          []string            `json:"final_critic,omitempty"`
	TargetPacket         []string            `json:"target_packet,omitempty"`
	Constraints          []string            `json:"constraints,omitempty"`
	RequiredNext         []string            `json:"required_next,omitempty"`
	InvalidActions       []string            `json:"invalid_actions,omitempty"`
	Budget               BudgetBrief         `json:"budget,omitempty"`
}

type ActionGuidanceInput struct {
	Reason                     string
	Request                    workspace.ToolRequest
	Result                     workspace.ToolResult
	Constraints                []string
	DeclarationMovesForbidden  bool
	ApprovedTargetScopePresent bool
	WriteCapable               bool
	// Objective is the current user ask for this turn, pulled verbatim
	// from the latest user message. When it contains relational verbs
	// like "calls"/"uses"/"references"/"delegates", BuildActionGuidance
	// promotes the language symbol/reference tools ahead of the generic
	// read-file suggestion — those are the right answer for "how does X
	// hand off to Y" questions, which the generic nudges don't surface.
	Objective string
}

type ActionGuidance struct {
	Constraints []string `json:"constraints,omitempty"`
	InvalidNext []string `json:"invalid_next,omitempty"`
	ValidNext   []string `json:"valid_next,omitempty"`
}

type RunState struct {
	Lane                 string              `json:"lane,omitempty"`
	TaskFamily           string              `json:"task_family,omitempty"`
	Contract             string              `json:"contract,omitempty"`
	Phase                string              `json:"phase,omitempty"`
	Objective            string              `json:"objective,omitempty"`
	Step                 int                 `json:"step,omitempty"`
	Permission           string              `json:"permission_profile,omitempty"`
	TargetRefs           []string            `json:"target_refs,omitempty"`
	EvidencePaths        []string            `json:"evidence_paths,omitempty"`
	ChangedPaths         []string            `json:"changed_paths,omitempty"`
	Constraints          []string            `json:"constraints,omitempty"`
	AllowedTools         []string            `json:"allowed_tools,omitempty"`
	ForbiddenTools       []string            `json:"forbidden_tools,omitempty"`
	ToolManifest         []ToolManifestEntry `json:"tool_manifest,omitempty"`
	RequiredEvidence     []string            `json:"required_evidence,omitempty"`
	RequiredTools        []string            `json:"required_tools,omitempty"`
	ReadinessGate        []string            `json:"readiness_gate,omitempty"`
	SearchPolicy         []string            `json:"search_policy,omitempty"`
	DiscoveryExamples    []string            `json:"discovery_examples,omitempty"`
	VerificationContract string              `json:"verification_contract,omitempty"`
	VerificationPlan     []string            `json:"verification_plan,omitempty"`
	StopConditions       []string            `json:"stop_conditions,omitempty"`
	FinalCritic          []string            `json:"final_critic,omitempty"`
	TargetPacket         []string            `json:"target_packet,omitempty"`
	UserBoundaries       []string            `json:"user_boundaries,omitempty"`
	Budget               BudgetBrief         `json:"budget,omitempty"`
	LastTool             string              `json:"last_tool,omitempty"`
	LastResult           ToolResultSummary   `json:"last_result,omitempty"`
	FailureClass         string              `json:"failure_class,omitempty"`
	NextActions          []string            `json:"next_actions,omitempty"`
	InvalidActions       []string            `json:"invalid_actions,omitempty"`
}

type StepInstruction struct {
	CurrentState RunState `json:"current_state"`
	Directive    string   `json:"directive"`
}

type RecoveryInput struct {
	Lane                      string
	Step                      int
	Reason                    string
	Request                   workspace.ToolRequest
	Result                    workspace.ToolResult
	EvidencePaths             []string
	Constraints               []string
	DeclarationMovesForbidden bool
	Objective                 string
	WriteCapable              bool
}

type RecoveryPlan struct {
	FailureClass       string   `json:"failure_class,omitempty"`
	UserVisibleBlocker bool     `json:"user_visible_blocker"`
	DoNotRepeat        []string `json:"do_not_repeat,omitempty"`
	RequiredNext       []string `json:"required_next,omitempty"`
	InvalidNext        []string `json:"invalid_next,omitempty"`
	ValidNext          []string `json:"valid_next,omitempty"`
	StateSummary       string   `json:"state_summary,omitempty"`
}

func BuildActionGuidance(input ActionGuidanceInput) ActionGuidance {
	statusCombined := combinedLower(input.Reason, input.Result.Summary, input.Result.Error)
	tool := strings.TrimSpace(input.Request.Tool)
	closureScope := closureGuidanceScope(input.Constraints, statusCombined)
	closureActive := closureScope != ""
	guidance := ActionGuidance{}
	if input.DeclarationMovesForbidden {
		guidance.Constraints = appendUnique(guidance.Constraints, "declaration_moves_forbidden")
		guidance.InvalidNext = appendUnique(guidance.InvalidNext, "go_move_declarations")
	}
	if input.WriteCapable && (input.ApprovedTargetScopePresent || containsAny(statusCombined, "implementation target packet", "approved editable target")) {
		guidance.Constraints = appendUnique(guidance.Constraints, "approved_target_scope_enforced")
	}
	if strings.Contains(statusCombined, "write approval required") {
		guidance.Constraints = appendUnique(guidance.Constraints, "write_approval_required")
	}
	if strings.Contains(statusCombined, "completion gate") {
		guidance.Constraints = appendUnique(guidance.Constraints, "completion_gate_active")
	}
	if strings.Contains(statusCombined, "bootstrap already inspected") {
		guidance.Constraints = appendUnique(guidance.Constraints, "bootstrap_evidence_available")
	}
	if closureActive {
		guidance.Constraints = appendUnique(guidance.Constraints, closureScope)
	}

	if containsAny(statusCombined, "malformed tool request", "tool_parse") {
		guidance.InvalidNext = appendUnique(guidance.InvalidNext, "malformed_json_tool_request")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "emit exactly one valid JSON tool object")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "answer normally only if the task is complete")
		return guidance
	}
	if containsAny(statusCombined, "repeated", "replayed", "already failed earlier") {
		if tool != "" {
			guidance.InvalidNext = appendUnique(guidance.InvalidNext, "repeat_exact_"+tool+"_request")
		} else {
			guidance.InvalidNext = appendUnique(guidance.InvalidNext, "repeat_exact_tool_request")
		}
	}
	if strings.Contains(statusCombined, "broad inspection") {
		guidance.InvalidNext = appendUnique(guidance.InvalidNext, "additional_broad_workspace_inspection")
	}
	if strings.Contains(statusCombined, "bootstrap already inspected") && tool == "read_file" {
		guidance.InvalidNext = appendUnique(guidance.InvalidNext, "duplicate_read_file_on_bootstrapped_target")
	}
	if containsAny(statusCombined, "completion gate", "final answer rejected") {
		guidance.InvalidNext = appendUnique(guidance.InvalidNext, "final_answer_without_satisfying_completion_gate")
	}
	if closureScope != "" {
		guidance.InvalidNext = appendUnique(guidance.InvalidNext, "open-ended discovery after readiness without one exact missing claim and one exact probe")
	}

	if strings.Contains(statusCombined, "bootstrap already inspected") {
		guidance.ValidNext = appendUnique(guidance.ValidNext, "edit_file on the approved target using bootstrapped evidence")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "narrow read_file with start_line/end_line only if exact lines are missing")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "search_content scoped to the approved target")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "answer with a concrete blocker or no-op reason")
	}
	if !closureActive && containsAny(statusCombined, "repeated dead-end", "already failed earlier", "replayed same blocker", "replayed prior evidence") {
		guidance.ValidNext = appendUnique(guidance.ValidNext, "choose a materially different tool or target")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "answer with the explicit blocker and evidence")
	}
	if !closureActive && strings.Contains(statusCombined, "broad inspection") {
		guidance.ValidNext = appendUnique(guidance.ValidNext, "use one structured symbol, call/reference, or file-outline probe on a named target")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "use one discovery_query for a broad architecture, tracing, or impact question")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "use search_content for one exact phrase only when a semantic probe or discovered handle does not land")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "answer if the current evidence already resolves the question")
	}
	if containsAny(statusCombined, "post-edit", "workspace edit already landed") {
		guidance.ValidNext = appendUnique(guidance.ValidNext, "run targeted verification")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "read the changed file if exact failure lines are needed")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "update task_ledger or answer with the concrete blocker")
	}
	if containsAny(statusCombined, "approved editable target", "implementation target packet") {
		guidance.ValidNext = appendUnique(guidance.ValidNext, "edit_file inside approved target paths")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "request a target-scope update if the approved target is wrong")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "report the guard as a concrete blocker")
	}
	if strings.Contains(statusCombined, "write approval required") {
		guidance.ValidNext = appendUnique(guidance.ValidNext, "stop and report that write approval is required")
	}
	if containsAny(statusCombined, "completion gate", "final answer rejected") {
		guidance.ValidNext = appendUnique(guidance.ValidNext, "use a tool to gather or edit the missing evidence")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "update task_ledger if the gate requires ledger state")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "answer only after the gate condition is satisfied")
	}
	if closureActive {
		for _, next := range closureGuidanceNext(scopeSpecificClosureLabel(closureScope)) {
			guidance.ValidNext = appendUnique(guidance.ValidNext, next)
		}
	}
	if input.DeclarationMovesForbidden {
		guidance.ValidNext = appendUnique(guidance.ValidNext, "do not call go_file_outline or go_move_declarations")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "read_file with a narrow line range inside the approved target")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "use edit_file instead of go_move_declarations")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "run verification or report the concrete blocker")
	}
	if !closureActive {
		switch tool {
		case "go_file_outline", "cpp_overview":
			if strings.TrimSpace(input.Result.Error) == "" {
				if objectiveImpliesRefactor(input.Objective) {
					// Refactoring objective — outline's built-in
					// go_move_declarations hint is the right next step.
					guidance.ValidNext = appendUnique(guidance.ValidNext, "go_move_declarations to relocate a tagged group of declarations")
					guidance.ValidNext = appendUnique(guidance.ValidNext, "go_read_symbol if you still need to inspect one declaration before moving")
				} else {
					// Default (explanatory/relational/unknown): reading a
					// specific declaration is the high-value next step.
					// The outline summary itself may suggest
					// go_move_declarations — that advice only lands for
					// refactor objectives, so we override here.
					guidance.ValidNext = appendUnique(guidance.ValidNext, "go_read_symbol with one of the outlined declaration names to inspect its body")
					guidance.ValidNext = appendUnique(guidance.ValidNext, "go_references or go_calls_into on a named declaration to trace how it's used")
					guidance.ValidNext = appendUnique(guidance.ValidNext, "ignore the outline's go_move_declarations suggestion unless the objective is actually a refactor")
				}
			}
		case "read_file", "go_extract_body", "go_read_symbol":
			if strings.TrimSpace(input.Result.Error) == "" {
				guidance.ValidNext = appendUnique(guidance.ValidNext, "answer if the inspected body already resolves the question")
				guidance.ValidNext = appendUnique(guidance.ValidNext, "if one exact missing claim remains, use one exact symbol, reference, or narrow range probe that would resolve it")
				if input.WriteCapable && (input.ApprovedTargetScopePresent || objectiveImpliesRefactor(input.Objective)) {
					guidance.ValidNext = appendUnique(guidance.ValidNext, "edit_file using the inspected evidence")
				}
				guidance.ValidNext = appendUnique(guidance.ValidNext, "use one narrow read_file or exact text search only if a specific anchor is still missing")
			}
		case "search_files", "search_content", "rg", "go_find_symbol", "go_find_type", "go_find_const", "go_references", "go_definition":
			if strings.TrimSpace(input.Result.Error) == "" {
				if IsZeroHitSearchResult(input.Result) {
					// The default "read the highest-confidence hit" nudge is
					// meaningless when there are no hits. Point the model at
					// a different search shape instead — its common failure
					// after a zero-hit search_files is to invent more
					// filenames to search for.
					guidance.ValidNext = appendUnique(guidance.ValidNext, "use a language-specific symbol tool before guessing filenames or repeating the same search")
					guidance.ValidNext = appendUnique(guidance.ValidNext, "narrow to one exact term or scope from the user's question")
					guidance.ValidNext = appendUnique(guidance.ValidNext, "answer with the evidence you already have if the target may not exist")
				} else {
					guidance.ValidNext = appendUnique(guidance.ValidNext, "read one exact symbol body, discovered handle, or narrow range from the highest-confidence hit")
					guidance.ValidNext = appendUnique(guidance.ValidNext, "answer if the best hit already resolves the question")
					if input.WriteCapable {
						guidance.ValidNext = appendUnique(guidance.ValidNext, "edit_file if the target is already clear")
					}
				}
			}
		case "edit_file", "write_file", "append_file", "go_move_declarations":
			if strings.TrimSpace(input.Result.Error) == "" {
				guidance.ValidNext = appendUnique(guidance.ValidNext, "run targeted verification")
				guidance.ValidNext = appendUnique(guidance.ValidNext, "update task_ledger or answer with the change summary")
			}
		case "task_ledger":
			guidance.ValidNext = appendUnique(guidance.ValidNext, "continue with the next incomplete ledger item")
			guidance.ValidNext = appendUnique(guidance.ValidNext, "answer if all required ledger items are complete")
		}
	}
	if len(guidance.ValidNext) == 0 {
		guidance.ValidNext = appendUnique(guidance.ValidNext, "answer from the current evidence if it already resolves the task")
		guidance.ValidNext = appendUnique(guidance.ValidNext, "otherwise take one exact tool-shaped follow-up tied to a concrete missing claim")
	}
	if !closureActive && objectiveImpliesRelation(input.Objective) && isDiscoveryTool(tool) {
		// Promote the semantic symbol tools ahead of the generic
		// "read a hit" advice. The generic nudges are tuned for "find a
		// file and read it"; a relational question ("how does X call Y")
		// is better answered by go_workspace_symbol and go_references
		// jumping straight to definitions and call sites.
		relational := []string{
			"go_workspace_symbol to locate the symbol by name, then go_references to list call sites",
			"go_calls_into / go_calls_from to trace handoff direction from a known symbol",
			"cpp_workspace_symbol + cpp_references for the C++ equivalent when the symbol is in C++ code",
		}
		rebuilt := make([]string, 0, len(relational)+len(guidance.ValidNext))
		for _, hint := range relational {
			rebuilt = appendUnique(rebuilt, hint)
		}
		for _, existing := range guidance.ValidNext {
			rebuilt = appendUnique(rebuilt, existing)
		}
		guidance.ValidNext = rebuilt
	}
	if closureScope != "" {
		guidance.ValidNext = filterClosureGuidanceNext(guidance.ValidNext, closureScope)
	}
	return guidance
}

func closureGuidanceScope(constraints []string, statusCombined string) string {
	switch {
	case containsStringValue(constraints, "child_packet_available"):
		return "child_packet_available"
	case containsStringValue(constraints, "answer_readiness_reached"):
		return "answer_readiness_reached"
	}
	switch {
	case containsAny(statusCombined,
		"child packet already contains sufficient evidence",
		"post-child follow-up missing named gap",
		"child_packet_available",
	):
		return "child_packet_available"
	case containsAny(statusCombined,
		"answer readiness has already been reached",
		"post-readiness follow-up missing named gap",
		"answer_readiness_reached",
	):
		return "answer_readiness_reached"
	default:
		return ""
	}
}

func scopeSpecificClosureLabel(scope string) string {
	if strings.TrimSpace(scope) == "child_packet_available" {
		return "child packet"
	}
	return "current evidence"
}

func closureGuidanceNext(subject string) []string {
	if strings.TrimSpace(subject) == "child packet" {
		return []string{
			"answer now from the child packet evidence",
			"if one exact missing claim remains, choose one exact packet-named path, symbol, discovered handle, or narrow range that would resolve it",
			"state what would be materially wrong or missing if you answered now",
		}
	}
	return []string{
		"answer now from the evidence already gathered",
		"if one exact missing claim remains, choose one exact symbol, discovered handle, or narrow range that would resolve it",
		"state what would be materially wrong or missing if you answered now",
	}
}

func filterClosureGuidanceNext(values []string, scope string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		lower := strings.ToLower(strings.TrimSpace(value))
		switch {
		case strings.Contains(lower, "search_files"),
			strings.Contains(lower, "todo|fixme"),
			strings.Contains(lower, "panic\\("),
			strings.Contains(lower, "exact text search"),
			strings.Contains(lower, "narrow to one exact term or scope"),
			strings.Contains(lower, "continue from the latest evidence with a targeted tool"),
			strings.Contains(lower, "choose a materially different tool or target"),
			strings.Contains(lower, "use a targeted read-only shell search"),
			strings.Contains(lower, "use a tool to gather or edit the missing evidence"),
			strings.Contains(lower, "update task_ledger"),
			strings.Contains(lower, "edit_file"),
			strings.Contains(lower, "verification"),
			strings.Contains(lower, "changed file"),
			strings.Contains(lower, "target-scope update"),
			strings.Contains(lower, "go_move_declarations"),
			strings.Contains(lower, "choose a materially different tool or target"),
			strings.Contains(lower, "use one structured symbol, call/reference, or file-outline probe"),
			strings.Contains(lower, "use one discovery_query"),
			strings.Contains(lower, "use search_content for one exact phrase"),
			strings.Contains(lower, "go_read_symbol with one of the outlined declaration names"),
			strings.Contains(lower, "go_references or go_calls_into on a named declaration"),
			strings.Contains(lower, "ignore the outline"),
			strings.Contains(lower, "read one exact symbol body, discovered handle, or narrow range from the highest-confidence hit"):
			continue
		}
		filtered = appendUnique(filtered, value)
	}
	prefixed := make([]string, 0, len(filtered)+3)
	for _, value := range closureGuidanceNext(scopeSpecificClosureLabel(scope)) {
		prefixed = appendUnique(prefixed, value)
	}
	for _, value := range filtered {
		prefixed = appendUnique(prefixed, value)
	}
	return prefixed
}

// relationalObjectiveVerbs enumerates the words that signal "find how X
// reaches Y" queries. Deliberately small and lowercase — false
// positives here downgrade a single next_valid_actions row, not safety.
var relationalObjectiveVerbs = []string{
	"calls", "call site", "called by", "uses", "used by",
	"invokes", "invoked", "references", "referenced",
	"delegates", "delegate to", "hands off", "handoff", "hand off",
	"dispatches", "dispatched", "subclasses", "implements",
	"who calls", "who uses",
}

func objectiveImpliesRelation(objective string) bool {
	lowered := strings.ToLower(strings.TrimSpace(objective))
	if lowered == "" {
		return false
	}
	for _, needle := range relationalObjectiveVerbs {
		if strings.Contains(lowered, needle) {
			return true
		}
	}
	return false
}

// refactorObjectiveVerbs signals "move/reshape code" intent, which is
// when the outline tool's built-in go_move_declarations next-action
// hint is actually the right follow-up. Kept narrow on purpose —
// over-matching here suppresses the go_read_symbol nudge that almost
// every explanatory question actually wants.
var refactorObjectiveVerbs = []string{
	"move ", "relocate", "extract to", "split ", "split-up", "split out",
	"refactor", "rename ", "reshape", "reorganize",
	"pull out", "pull up", "push down", "hoist",
}

func objectiveImpliesRefactor(objective string) bool {
	lowered := strings.ToLower(strings.TrimSpace(objective))
	if lowered == "" {
		return false
	}
	for _, needle := range refactorObjectiveVerbs {
		if strings.Contains(lowered, needle) {
			return true
		}
	}
	return false
}

// isDiscoveryTool reports whether the just-finished tool was an
// orientation/search step — the only moment it's worth re-ranking the
// suggested next actions. We deliberately exclude edit/write tools
// (post-edit guidance has its own shape) and structured symbol tools
// (already returned what relational queries want).
func isDiscoveryTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "list_files", "search_content", "search_files", "read_file", "rg", "grep":
		return true
	}
	return false
}

func BuildRecoveryPlan(input RecoveryInput) RecoveryPlan {
	guidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:                    input.Reason,
		Request:                   input.Request,
		Result:                    input.Result,
		Constraints:               input.Constraints,
		DeclarationMovesForbidden: input.DeclarationMovesForbidden,
		Objective:                 input.Objective,
		WriteCapable:              input.WriteCapable,
	})
	tool := strings.TrimSpace(input.Request.Tool)
	if tool == "" {
		tool = strings.TrimSpace(input.Result.Tool)
	}
	target := ToolRequestTarget(input.Request)
	combined := combinedLower(input.Reason, input.Result.Summary, input.Result.Error, input.Result.Output)
	plan := RecoveryPlan{
		FailureClass:       ClassifyFailure(input.Reason, input.Request, input.Result),
		UserVisibleBlocker: UserVisibleBlocker(input.Reason, input.Result),
		InvalidNext:        append([]string(nil), guidance.InvalidNext...),
		ValidNext:          append([]string(nil), guidance.ValidNext...),
		StateSummary:       recoveryStateSummary(input.Reason, tool, target, input.Result, input.EvidencePaths),
	}
	for _, invalid := range plan.InvalidNext {
		plan.DoNotRepeat = appendUnique(plan.DoNotRepeat, invalid)
	}
	if tool != "" {
		if target != "" {
			plan.DoNotRepeat = appendUnique(plan.DoNotRepeat, "same "+tool+" request on "+target)
		} else {
			plan.DoNotRepeat = appendUnique(plan.DoNotRepeat, "same "+tool+" request")
		}
	}
	if strings.Contains(combined, "permission denied") {
		plan.DoNotRepeat = appendUnique(plan.DoNotRepeat, "tool requests outside the current permission policy")
	}
	if containsAny(combined, "already failed earlier", "repeated") {
		plan.DoNotRepeat = appendUnique(plan.DoNotRepeat, "same failed action class")
	}
	if containsAny(combined, "malformed tool", "tool_parse") {
		plan.DoNotRepeat = appendUnique(plan.DoNotRepeat, "markdown-wrapped or malformed tool JSON")
	}
	if containsAny(combined, "malformed tool", "tool_parse") {
		plan.RequiredNext = appendUnique(plan.RequiredNext, "emit exactly one valid JSON tool object")
	}
	if containsAny(combined, "approval required", "write approval required") {
		plan.RequiredNext = appendUnique(plan.RequiredNext, "stop and report that write approval is required")
	}
	if isActualVerificationRecoverySignal(combined) {
		plan.RequiredNext = appendUnique(plan.RequiredNext, "resolve verification status before claiming completion")
	}
	if containsAny(combined, "completion gate", "final answer rejected") {
		plan.RequiredNext = appendUnique(plan.RequiredNext, "satisfy the completion gate before final answer")
	}
	return plan
}

func ClassifyFailure(reason string, req workspace.ToolRequest, result workspace.ToolResult) string {
	combined := combinedLower(reason, result.Summary, result.Error, result.Output)
	tool := strings.TrimSpace(req.Tool)
	switch {
	case containsAny(combined, "tool_parse", "malformed tool request", "valid json tool object"):
		return "tool_parse_error"
	case containsAny(combined, "approval required", "write approval required", "permission denied", "permission"):
		return "permission_boundary"
	case containsAny(combined, "guard", "rejected by guard", "approved editable target", "implementation target packet", "bootstrap already inspected"):
		return "guard_rejection"
	case containsAny(combined, "completion gate", "final answer rejected"):
		return "completion_gate"
	case isActualVerificationRecoverySignal(combined):
		return "verification_unresolved"
	case containsAny(combined, "same tool request", "repeated tool request", "replayed", "already failed earlier", "repeated dead-end"):
		return "repeated_action"
	case strings.Contains(combined, "broad inspection"):
		return "broad_inspection_loop"
	case containsAny(combined, "target scope", "outside approved", "approved target"):
		return "target_scope_violation"
	case containsAny(combined, "post-edit", "workspace edit already landed"):
		return "post_edit_focus"
	case tool == "task" || strings.Contains(combined, "child task"):
		return "child_task_failure"
	case strings.Contains(combined, "checkpoint"):
		return "checkpoint_failure"
	case strings.TrimSpace(result.Error) != "":
		return "tool_failure"
	default:
		return "unknown"
	}
}

func isActualVerificationRecoverySignal(combined string) bool {
	combined = strings.ToLower(strings.TrimSpace(combined))
	if combined == "" {
		return false
	}
	return containsAny(combined,
		"verification failed",
		"verification result",
		"verification status",
		"verification-only",
		"verification only",
		"verification command",
		"verification contract",
		"missing verification",
		"missing-verification",
		"post-edit verification",
		"resolve verification",
	)
}

func ClassifyPhase(req workspace.ToolRequest, result workspace.ToolResult) string {
	if strings.TrimSpace(result.Error) != "" {
		return "repair"
	}
	tool := strings.TrimSpace(req.Tool)
	if workspace.IsWorkspaceWriteTool(tool) {
		return "verification"
	}
	switch tool {
	case "task_ledger":
		return "planning"
	default:
		return "evidence"
	}
}

func UserVisibleBlocker(reason string, result workspace.ToolResult) bool {
	combined := combinedLower(reason, result.Summary, result.Error)
	return containsAny(combined, "approval required", "permission", "guard", "completion gate", "blocked")
}

func BuildStepInstruction(state RunState) StepInstruction {
	state = MergeRuntimeBriefIntoState(WorkerRuntimeBrief{}, state)
	state.TargetRefs = uniqueNonEmpty(state.TargetRefs)
	state.EvidencePaths = uniqueNonEmpty(state.EvidencePaths)
	state.ChangedPaths = uniqueNonEmpty(state.ChangedPaths)
	state.Constraints = uniqueNonEmpty(state.Constraints)
	state.AllowedTools = uniqueNonEmpty(state.AllowedTools)
	state.ForbiddenTools = uniqueNonEmpty(state.ForbiddenTools)
	state.RequiredEvidence = uniqueNonEmpty(state.RequiredEvidence)
	state.RequiredTools = uniqueNonEmpty(state.RequiredTools)
	state.ReadinessGate = uniqueNonEmpty(state.ReadinessGate)
	state.SearchPolicy = uniqueNonEmpty(state.SearchPolicy)
	state.VerificationPlan = uniqueNonEmpty(state.VerificationPlan)
	state.StopConditions = uniqueNonEmpty(state.StopConditions)
	state.FinalCritic = uniqueNonEmpty(state.FinalCritic)
	state.TargetPacket = uniqueNonEmpty(state.TargetPacket)
	state.UserBoundaries = uniqueNonEmpty(state.UserBoundaries)
	state.NextActions = uniqueNonEmpty(state.NextActions)
	state.InvalidActions = uniqueNonEmpty(state.InvalidActions)
	directive := "Use this current-state packet for the next action. Treat next_actions as ranked options, not mandatory commands. Do not repeat invalid actions."
	if containsStringValue(state.Constraints, "answer_readiness_reached") || containsStringValue(state.Constraints, "child_packet_available") {
		directive = "Use this current-state packet for the next action. Prefer answering now; only continue if you can name one exact missing claim, one exact probe, and what would be materially incomplete if you answered now. Treat next_actions as ranked options, not mandatory commands. Do not repeat invalid actions."
	}
	return StepInstruction{
		CurrentState: state,
		Directive:    directive,
	}
}

func BuildInitialStepInstruction(brief WorkerRuntimeBrief) StepInstruction {
	state := MergeRuntimeBriefIntoState(brief, RunState{Phase: "start", Step: 1})
	return StepInstruction{
		CurrentState: state,
		Directive:    "Use this session-state packet when choosing the next action. The system prompt already lists the available tools and how to call them. Treat the search policy and examples as guidance, not a fixed script.",
	}
}

// MergeRuntimeBriefIntoStateDelta is a slimmed-down variant of
// MergeRuntimeBriefIntoState that keeps only the fields that can change
// turn-to-turn (evidence paths, changed paths, constraints, next/invalid
// actions, budget). It DROPS the static scaffolding — tool_manifest,
// allowed/forbidden tools, required tools, readiness gate, search policy,
// stop conditions, final critic, target packet. Once a model has seen
// those fields three times, repeating them burns tokens for no benefit.
// The runner uses this variant after step 3 of a turn.
func MergeRuntimeBriefIntoStateDelta(brief WorkerRuntimeBrief, state RunState) RunState {
	brief = NormalizeRuntimeBrief(brief)
	if state.Lane == "" {
		state.Lane = brief.Lane
	}
	if state.TaskFamily == "" {
		state.TaskFamily = brief.TaskFamily
	}
	if state.Contract == "" {
		state.Contract = brief.Contract
	}
	if state.Objective == "" {
		state.Objective = brief.Objective
	}
	if state.Permission == "" {
		state.Permission = brief.PermissionProfile
	}
	if len(state.TargetRefs) == 0 {
		state.TargetRefs = append([]string(nil), brief.TargetRefs...)
	}
	state.EvidencePaths = mergeStrings(state.EvidencePaths, brief.EvidencePaths)
	state.ChangedPaths = mergeStrings(state.ChangedPaths, brief.ChangedPaths)
	state.Constraints = mergeStrings(state.Constraints, brief.Constraints)
	if state.Budget == (BudgetBrief{}) {
		state.Budget = brief.Budget
	}
	state.NextActions = mergeStrings(state.NextActions, brief.RequiredNext)
	state.InvalidActions = mergeStrings(state.InvalidActions, brief.InvalidActions)
	return state
}

func MergeRuntimeBriefIntoState(brief WorkerRuntimeBrief, state RunState) RunState {
	brief = NormalizeRuntimeBrief(brief)
	if state.Lane == "" {
		state.Lane = brief.Lane
	}
	if state.TaskFamily == "" {
		state.TaskFamily = brief.TaskFamily
	}
	if state.Contract == "" {
		state.Contract = brief.Contract
	}
	if state.Objective == "" {
		state.Objective = brief.Objective
	}
	if state.Permission == "" {
		state.Permission = brief.PermissionProfile
	}
	if len(state.TargetRefs) == 0 {
		state.TargetRefs = append([]string(nil), brief.TargetRefs...)
	}
	state.EvidencePaths = mergeStrings(state.EvidencePaths, brief.EvidencePaths)
	state.ChangedPaths = mergeStrings(state.ChangedPaths, brief.ChangedPaths)
	state.Constraints = mergeStrings(state.Constraints, brief.Constraints)
	if len(state.AllowedTools) == 0 {
		state.AllowedTools = append([]string(nil), brief.AllowedTools...)
	}
	if len(state.ForbiddenTools) == 0 {
		state.ForbiddenTools = append([]string(nil), brief.ForbiddenTools...)
	}
	if len(state.ToolManifest) == 0 {
		state.ToolManifest = append([]ToolManifestEntry(nil), brief.ToolManifest...)
	}
	if len(state.RequiredEvidence) == 0 {
		state.RequiredEvidence = append([]string(nil), brief.RequiredEvidence...)
	}
	if len(state.RequiredTools) == 0 {
		state.RequiredTools = append([]string(nil), brief.RequiredTools...)
	}
	if len(state.ReadinessGate) == 0 {
		state.ReadinessGate = append([]string(nil), brief.ReadinessGate...)
	}
	if len(state.SearchPolicy) == 0 {
		state.SearchPolicy = append([]string(nil), brief.SearchPolicy...)
	}
	if len(state.DiscoveryExamples) == 0 {
		state.DiscoveryExamples = append([]string(nil), brief.DiscoveryExamples...)
	}
	if state.VerificationContract == "" {
		state.VerificationContract = brief.VerificationContract
	}
	if len(state.VerificationPlan) == 0 {
		state.VerificationPlan = append([]string(nil), brief.VerificationPlan...)
	}
	if len(state.StopConditions) == 0 {
		state.StopConditions = append([]string(nil), brief.StopConditions...)
	}
	if len(state.FinalCritic) == 0 {
		state.FinalCritic = append([]string(nil), brief.FinalCritic...)
	}
	if len(state.TargetPacket) == 0 {
		state.TargetPacket = append([]string(nil), brief.TargetPacket...)
	}
	if len(state.UserBoundaries) == 0 {
		state.UserBoundaries = append([]string(nil), brief.UserBoundaries...)
	}
	if state.Budget == (BudgetBrief{}) {
		state.Budget = brief.Budget
	}
	state.NextActions = mergeStrings(state.NextActions, brief.RequiredNext)
	state.InvalidActions = mergeStrings(state.InvalidActions, brief.InvalidActions)
	return state
}

func NormalizeRuntimeBrief(brief WorkerRuntimeBrief) WorkerRuntimeBrief {
	brief.Lane = strings.TrimSpace(brief.Lane)
	brief.TaskFamily = strings.TrimSpace(brief.TaskFamily)
	brief.Contract = strings.TrimSpace(brief.Contract)
	brief.Objective = truncate(strings.TrimSpace(brief.Objective), 500)
	brief.PermissionProfile = strings.TrimSpace(brief.PermissionProfile)
	brief.VerificationContract = strings.TrimSpace(brief.VerificationContract)
	brief.TargetRefs = uniqueNonEmpty(brief.TargetRefs)
	brief.EvidencePaths = uniqueNonEmpty(brief.EvidencePaths)
	brief.ChangedPaths = uniqueNonEmpty(brief.ChangedPaths)
	brief.UserBoundaries = uniqueNonEmpty(brief.UserBoundaries)
	brief.AllowedTools = uniqueNonEmpty(brief.AllowedTools)
	brief.ForbiddenTools = uniqueNonEmpty(brief.ForbiddenTools)
	brief.RequiredEvidence = uniqueNonEmpty(brief.RequiredEvidence)
	brief.RequiredTools = uniqueNonEmpty(brief.RequiredTools)
	brief.ReadinessGate = uniqueNonEmpty(brief.ReadinessGate)
	brief.SearchPolicy = uniqueNonEmpty(brief.SearchPolicy)
	brief.DiscoveryExamples = uniqueNonEmpty(brief.DiscoveryExamples)
	brief.VerificationPlan = uniqueNonEmpty(brief.VerificationPlan)
	brief.StopConditions = uniqueNonEmpty(brief.StopConditions)
	brief.FinalCritic = uniqueNonEmpty(brief.FinalCritic)
	brief.TargetPacket = uniqueNonEmpty(brief.TargetPacket)
	brief.Constraints = uniqueNonEmpty(brief.Constraints)
	brief.RequiredNext = uniqueNonEmpty(brief.RequiredNext)
	brief.InvalidActions = uniqueNonEmpty(brief.InvalidActions)
	if strings.EqualFold(brief.PermissionProfile, "conversation_only") {
		brief.AllowedTools = nil
		brief.RequiredTools = nil
		brief.ToolManifest = nil
		brief.DiscoveryExamples = nil
	}
	if len(brief.ToolManifest) == 0 && len(brief.AllowedTools) > 0 {
		brief.ToolManifest = BuildToolManifest(brief.AllowedTools)
	}
	return brief
}

func RenderStepInstruction(instruction StepInstruction) string {
	if strings.TrimSpace(instruction.Directive) == "" {
		instruction.Directive = "Use this current-state packet for the next action. Do not repeat invalid actions."
	}
	data, err := json.Marshal(instruction)
	if err != nil {
		return ""
	}
	return strings.Join([]string{
		"[frontier step instruction]",
		instruction.Directive,
		string(data),
	}, "\n")
}

func RenderRunState(state RunState) string {
	return RenderStepInstruction(BuildStepInstruction(state))
}

func RenderInitialRuntimeBrief(brief WorkerRuntimeBrief) string {
	brief = NormalizeRuntimeBrief(brief)
	brief = withDeterministicDiscoveryExamples(brief)
	if !RuntimeBriefHasContent(brief) {
		return ""
	}
	return RenderStepInstruction(BuildInitialStepInstruction(brief))
}

func withDeterministicDiscoveryExamples(brief WorkerRuntimeBrief) WorkerRuntimeBrief {
	if !toolAllowedInBrief(brief.AllowedTools, "discovery_query") {
		return brief
	}
	brief.DiscoveryExamples = uniqueNonEmpty(append(brief.DiscoveryExamples,
		`Use one discovery_query before ad hoc list/search/read for broad architecture, impact, tracing, security, or history questions; follow with targeted reads only for named gaps.`,
		`architecture_boundary shape: {"tool":"discovery_query","purpose":"architecture_boundary","path":"<narrow-root>","pattern":"<component-term>|<boundary-term>","max_depth":2}`,
		`impact_analysis shape: {"tool":"discovery_query","purpose":"impact_analysis","path":"<target-file-or-dir>","pattern":"<changed-symbol-or-feature-term>","max_depth":2}`,
		`function_tracing shape: {"tool":"discovery_query","purpose":"function_tracing","path":"<target-file-or-dir>","pattern":"<function-or-call-term>","max_depth":2}`,
		`security_review: {"tool":"discovery_query","purpose":"security_review","path":".","pattern":"auth|token|secret","max_depth":2}`,
	))
	return brief
}

func toolAllowedInBrief(tools []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	for _, tool := range tools {
		if strings.EqualFold(strings.TrimSpace(tool), want) {
			return true
		}
	}
	return false
}

func RenderRuntimeBriefSummary(brief WorkerRuntimeBrief) string {
	brief = NormalizeRuntimeBrief(brief)
	if !RuntimeBriefHasContent(brief) {
		return ""
	}
	data, err := json.Marshal(brief)
	if err != nil {
		return ""
	}
	return strings.Join([]string{
		"[frontier runtime brief]",
		"Preserved frontier runtime state. Continue under these same target, policy, evidence, and verification constraints.",
		string(data),
	}, "\n")
}

func RuntimeBriefHasContent(brief WorkerRuntimeBrief) bool {
	brief = NormalizeRuntimeBrief(brief)
	return brief.Lane != "" ||
		brief.TaskFamily != "" ||
		brief.Contract != "" ||
		brief.Objective != "" ||
		len(brief.TargetRefs) > 0 ||
		len(brief.AllowedTools) > 0 ||
		len(brief.ForbiddenTools) > 0 ||
		len(brief.RequiredEvidence) > 0 ||
		len(brief.ReadinessGate) > 0 ||
		len(brief.DiscoveryExamples) > 0 ||
		len(brief.TargetPacket) > 0 ||
		len(brief.VerificationPlan) > 0
}

func RenderFailureFeedbackPacket(packet any) string {
	data, err := json.Marshal(packet)
	if err != nil {
		return ""
	}
	return strings.Join([]string{
		"[failure feedback packet]",
		"Use this packet to recover. `required_next` lists hard requirements; `valid_next` lists safe options. Do not repeat invalid actions from this packet.",
		string(data),
	}, "\n")
}

func SummarizeToolResult(result workspace.ToolResult) ToolResultSummary {
	return ToolResultSummary{
		Tool:        strings.TrimSpace(result.Tool),
		Summary:     truncate(result.Summary, 220),
		Error:       truncate(result.Error, 260),
		OutputChars: len(result.Output),
	}
}

func SummarizeToolResults(results []workspace.ToolResult, limit int) []ToolResultSummary {
	if limit > 0 && len(results) > limit {
		results = results[len(results)-limit:]
	}
	out := make([]ToolResultSummary, 0, len(results))
	for _, result := range results {
		out = append(out, SummarizeToolResult(result))
	}
	return out
}

func recoveryStateSummary(reason string, tool string, target string, result workspace.ToolResult, evidencePaths []string) string {
	parts := []string{strings.TrimSpace(reason)}
	if tool != "" {
		if target != "" {
			parts = append(parts, "last tool: "+tool+" on "+target)
		} else {
			parts = append(parts, "last tool: "+tool)
		}
	}
	if summary := strings.TrimSpace(result.Summary); summary != "" {
		parts = append(parts, "summary: "+truncate(summary, 180))
	}
	if errText := strings.TrimSpace(result.Error); errText != "" {
		parts = append(parts, "error: "+truncate(errText, 220))
	}
	if len(evidencePaths) > 0 {
		parts = append(parts, "evidence: "+strings.Join(uniqueNonEmpty(evidencePaths), ", "))
	}
	return strings.Join(uniqueNonEmpty(parts), "; ")
}

func ToolRequestTarget(req workspace.ToolRequest) string {
	target := firstNonEmpty(
		strings.TrimSpace(req.Path),
		strings.TrimSpace(req.ID),
		strings.TrimSpace(req.Destination),
		strings.TrimSpace(req.Pattern),
		strings.TrimSpace(req.Glob),
		strings.TrimSpace(req.Workdir),
		strings.TrimSpace(req.Agent),
		strings.TrimSpace(req.Command),
	)
	return truncate(target, 180)
}

func combinedLower(parts ...string) string {
	return strings.ToLower(strings.Join(parts, "\n"))
}

// IsZeroHitSearchResult detects the "search found nothing" shape from a
// tool result's summary. Used to swap the next-action nudge from
// "read the highest-confidence hit" (useless when the hit list is empty)
// to "switch to a broader search or answer with what you have", and by
// the runner to block retries of the exact same zero-hit fingerprint.
// Summaries are produced by the workspace layer and are stable strings.
func IsZeroHitSearchResult(result workspace.ToolResult) bool {
	summary := strings.ToLower(strings.TrimSpace(result.Summary))
	if summary == "" {
		return false
	}
	return containsAny(summary,
		"no file matches",
		"no matches",
		"no matching",
		"no go symbols matched",
		"no c++ symbols matched",
		"no symbols matched",
		"found 0 ",
	)
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func containsStringValue(values []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}

func appendUnique(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func mergeStrings(base []string, extra []string) []string {
	out := append([]string(nil), base...)
	for _, value := range extra {
		out = appendUnique(out, value)
	}
	return out
}

func uniqueNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = appendUnique(out, value)
	}
	return out
}

func BuildToolManifest(allowedTools []string) []ToolManifestEntry {
	return workspace.BuildToolManifest(allowedTools)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func truncate(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	if max <= 1 {
		return value[:max]
	}
	return value[:max-1] + "..."
}
