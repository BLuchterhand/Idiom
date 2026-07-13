package workspace

import (
	"slices"
	"strings"

	"idiom/internal/contracts/autonomy"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/textutil"
)

const (
	PlanExecuteTerminalSubmitPlan      = ToolNameSubmitPlan
	PlanExecuteTerminalSourceSatisfied = plancontract.SourceSatisfiedDecision
	PlanExecuteTerminalQuestion        = "<<QUESTION>>:"
)

type ToolSurfacePhase string

const (
	ToolSurfacePhaseImplementationPreWrite           ToolSurfacePhase = ToolSurfacePhase(toolSurfaceProjectionImplementationPreWrite)
	ToolSurfacePhaseImplementationTargetSource       ToolSurfacePhase = ToolSurfacePhase(toolSurfaceProjectionImplementationTargetSource)
	ToolSurfacePhaseImplementationTargetWriteReady   ToolSurfacePhase = ToolSurfacePhase(toolSurfaceProjectionImplementationTargetWriteReady)
	ToolSurfacePhaseImplementationVerificationReady  ToolSurfacePhase = ToolSurfacePhase(toolSurfaceProjectionImplementationVerificationReady)
	ToolSurfacePhaseImplementationVerificationRepair ToolSurfacePhase = ToolSurfacePhase(toolSurfaceProjectionImplementationVerificationRepair)
	ToolSurfacePhasePlanExecuteReviewRepairInitial   ToolSurfacePhase = ToolSurfacePhase(toolSurfaceProjectionPlanExecuteReviewRepairInitial)
	ToolSurfacePhasePlanExecuteReviewRepairTerminal  ToolSurfacePhase = ToolSurfacePhase(toolSurfaceProjectionPlanExecuteReviewRepairTerminal)
	ToolSurfacePhasePlanExecuteSubmitPlanOnly        ToolSurfacePhase = ToolSurfacePhase(toolSurfaceProjectionPlanExecuteSubmitPlanOnly)
	ToolSurfacePhasePlanExecuteRequiredSource        ToolSurfacePhase = ToolSurfacePhase(toolSurfaceProjectionPlanExecuteRequiredSource)
	ToolSurfacePhasePlanExecuteHistoryPending        ToolSurfacePhase = ToolSurfacePhase(toolSurfaceProjectionPlanExecuteHistoryPending)
	ToolSurfacePhasePlanExecuteCandidateSource       ToolSurfacePhase = ToolSurfacePhase(toolSurfaceProjectionPlanExecuteCandidateSource)
	ToolSurfacePhasePlanExecuteAnchorRefresh         ToolSurfacePhase = ToolSurfacePhase(toolSurfaceProjectionPlanExecuteAnchorRefresh)
)

func ToolSurfacePhaseToolNames(phase ToolSurfacePhase) []string {
	return collectToolSurfaceProjectionToolNames(toolSurfaceProjectionID(phase), nil)
}

func ToolSurfacePhaseAllowedTools(tools []string, phase ToolSurfacePhase) []string {
	return FilterToolNames(tools, ToolSurfacePhaseToolNames(phase)...)
}

func ToolSurfacePhaseDisallowedTools(tools []string, phase ToolSurfacePhase) []string {
	phaseTools := ToolSurfacePhaseToolNames(phase)
	if len(tools) == 0 || len(phaseTools) == 0 {
		return nil
	}

	allowed := toolNameSet(phaseTools)
	disallowed := make([]string, 0, len(tools))
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			continue
		}
		if _, ok := allowed[tool]; ok {
			continue
		}
		disallowed = append(disallowed, tool)
	}

	return textutil.Unique(disallowed)
}

func ToolSurfacePhaseAllowedToolsOrDefault(tools []string, phase ToolSurfacePhase) []string {
	allowed := ToolSurfacePhaseAllowedTools(tools, phase)
	if len(allowed) > 0 {
		return allowed
	}

	return ToolSurfacePhaseToolNames(phase)
}

func ToolSurfacePhaseAllowsTool(tool string, phase ToolSurfacePhase) bool {
	return len(FilterToolNames([]string{tool}, ToolSurfacePhaseToolNames(phase)...)) > 0
}

type PlanExecuteToolSurfaceProjection struct {
	Phase                  ToolSurfacePhase
	ParserVisibleTools     []string
	CallableTools          []string
	AllowedActionClasses   []string
	InvalidActionClasses   []string
	TerminalResponseShapes []string
}

func PlanExecuteTerminalSourceSatisfiedOrQuestion() []string {
	return []string{PlanExecuteTerminalSourceSatisfied, PlanExecuteTerminalQuestion}
}

func PlanExecuteTerminalSubmitPlanSourceSatisfiedQuestion() []string {
	return []string{PlanExecuteTerminalSubmitPlan, PlanExecuteTerminalSourceSatisfied, PlanExecuteTerminalQuestion}
}

func PlanExecuteTerminalSubmitPlanQuestion() []string {
	return []string{PlanExecuteTerminalSubmitPlan, PlanExecuteTerminalQuestion}
}

func PlanExecuteTerminalSubmitPlanOnly() []string {
	return []string{PlanExecuteTerminalSubmitPlan}
}

type planExecuteToolSurfaceProjectionPolicy struct {
	AllowedActionClasses   []string
	InvalidActionClasses   []string
	TerminalResponseShapes []string
}

func PlanExecuteToolSurfaceProjectionForPhase(phase ToolSurfacePhase) PlanExecuteToolSurfaceProjection {
	policy, ok := planExecuteToolSurfaceProjectionPolicies[phase]
	if !ok {
		return PlanExecuteToolSurfaceProjection{Phase: phase}
	}

	return planExecuteToolSurfaceProjection(
		phase,
		policy.AllowedActionClasses,
		policy.InvalidActionClasses,
		policy.TerminalResponseShapes,
	)
}

func PlanExecuteSubmitCorrectionProjection(blockerAllowed bool) PlanExecuteToolSurfaceProjection {
	projection := PlanExecuteToolSurfaceProjectionForPhase(ToolSurfacePhasePlanExecuteSubmitPlanOnly)
	if blockerAllowed {
		projection.AllowedActionClasses = []string{planExecuteActionSubmitPlan, planExecuteActionBlocker}
		projection.InvalidActionClasses = []string{planExecuteActionRead, planExecuteActionSearch, planExecuteActionSourceSatisfied, planExecuteActionVerify, planExecuteActionPerEditWrite, planExecuteActionGenericProbe, planExecuteActionProseFinal}
		projection.TerminalResponseShapes = PlanExecuteTerminalSubmitPlanQuestion()
		return projection
	}

	projection.AllowedActionClasses = []string{planExecuteActionSubmitPlan}
	projection.InvalidActionClasses = []string{planExecuteActionRead, planExecuteActionSearch, planExecuteActionSourceSatisfied, planExecuteActionBlocker, planExecuteActionVerify, planExecuteActionPerEditWrite, planExecuteActionGenericProbe, planExecuteActionProseFinal}
	projection.TerminalResponseShapes = PlanExecuteTerminalSubmitPlanOnly()
	return projection
}

func (projection PlanExecuteToolSurfaceProjection) WithSubmitPlanTerminal() PlanExecuteToolSurfaceProjection {
	projection = projection.Clone()
	projection.ParserVisibleTools = textutil.Unique(append(projection.ParserVisibleTools, ToolNameSubmitPlan))
	projection.CallableTools = textutil.Unique(append(projection.CallableTools, ToolNameSubmitPlan))
	projection.AllowedActionClasses = textutil.Unique(append(projection.AllowedActionClasses, planExecuteActionSubmitPlan))
	projection.InvalidActionClasses = removePlanExecuteActionClass(projection.InvalidActionClasses, planExecuteActionSubmitPlan)
	projection.TerminalResponseShapes = textutil.Unique(append(projection.TerminalResponseShapes, PlanExecuteTerminalSubmitPlan))
	return projection
}

// WithPermissionAuthorizedInspection keeps already-authorized, non-mutating
// inspection additive to a planner phase. Phase-specific terminal obligations
// remain intact; only the callable inspection tools and their owned action
// classes are removed from the invalid set.
func (projection PlanExecuteToolSurfaceProjection) WithPermissionAuthorizedInspection(inspection PermissionAuthorizedInspectionProjection) PlanExecuteToolSurfaceProjection {
	projection = projection.Clone()
	tools := inspection.ToolNames()
	classes := inspection.ActionClasses()
	projection.ParserVisibleTools = textutil.Merge(projection.ParserVisibleTools, tools)
	projection.CallableTools = textutil.Merge(projection.CallableTools, tools)
	projection.AllowedActionClasses = textutil.Merge(projection.AllowedActionClasses, classes)
	for _, class := range classes {
		projection.InvalidActionClasses = removePlanExecuteActionClass(projection.InvalidActionClasses, class)
	}
	return projection
}

func (projection PlanExecuteToolSurfaceProjection) WithNativeSubmitPlanOnly() PlanExecuteToolSurfaceProjection {
	projection = projection.Clone()
	projection.AllowedActionClasses = []string{planExecuteActionSubmitPlan}
	projection.TerminalResponseShapes = PlanExecuteTerminalSubmitPlanOnly()
	return projection
}

func (projection PlanExecuteToolSurfaceProjection) Clone() PlanExecuteToolSurfaceProjection {
	return PlanExecuteToolSurfaceProjection{
		Phase:                  projection.Phase,
		ParserVisibleTools:     append([]string(nil), projection.ParserVisibleTools...),
		CallableTools:          append([]string(nil), projection.CallableTools...),
		AllowedActionClasses:   append([]string(nil), projection.AllowedActionClasses...),
		InvalidActionClasses:   append([]string(nil), projection.InvalidActionClasses...),
		TerminalResponseShapes: append([]string(nil), projection.TerminalResponseShapes...),
	}
}

const (
	planExecuteActionRead            = string(autonomy.ActionClassRead)
	planExecuteActionSearch          = string(autonomy.ActionClassSearch)
	planExecuteActionSubmitPlan      = string(autonomy.ActionClassSubmitPlan)
	planExecuteActionPerEditWrite    = string(autonomy.ActionClassPerEditWrite)
	planExecuteActionSourceSatisfied = string(autonomy.ActionClassSourceSatisfied)
	planExecuteActionBlocker         = string(autonomy.ActionClassBlocker)
	planExecuteActionVerify          = string(autonomy.ActionClassVerify)
	planExecuteActionGenericProbe    = string(autonomy.ActionClassGenericProbe)
	planExecuteActionProseFinal      = string(autonomy.ActionClassProseFinal)
)

var planExecuteToolSurfaceProjectionPolicies = map[ToolSurfacePhase]planExecuteToolSurfaceProjectionPolicy{
	ToolSurfacePhasePlanExecuteRequiredSource: {
		AllowedActionClasses:   []string{planExecuteActionRead, planExecuteActionSourceSatisfied, planExecuteActionBlocker},
		InvalidActionClasses:   []string{planExecuteActionSubmitPlan, planExecuteActionPerEditWrite, planExecuteActionSearch},
		TerminalResponseShapes: PlanExecuteTerminalSourceSatisfiedOrQuestion(),
	},
	ToolSurfacePhasePlanExecuteCandidateSource: {
		AllowedActionClasses:   []string{planExecuteActionRead, planExecuteActionSearch, planExecuteActionBlocker, planExecuteActionSourceSatisfied},
		InvalidActionClasses:   []string{planExecuteActionSubmitPlan, planExecuteActionPerEditWrite, planExecuteActionVerify, planExecuteActionGenericProbe},
		TerminalResponseShapes: PlanExecuteTerminalSourceSatisfiedOrQuestion(),
	},
	ToolSurfacePhasePlanExecuteHistoryPending: {
		AllowedActionClasses:   []string{planExecuteActionRead, planExecuteActionSourceSatisfied, planExecuteActionBlocker},
		InvalidActionClasses:   []string{planExecuteActionSearch, planExecuteActionVerify, planExecuteActionPerEditWrite, planExecuteActionGenericProbe},
		TerminalResponseShapes: PlanExecuteTerminalSourceSatisfiedOrQuestion(),
	},
	ToolSurfacePhasePlanExecuteAnchorRefresh: {
		AllowedActionClasses: []string{planExecuteActionRead},
		InvalidActionClasses: []string{planExecuteActionSubmitPlan, planExecuteActionSourceSatisfied, planExecuteActionBlocker, planExecuteActionSearch, planExecuteActionPerEditWrite, planExecuteActionProseFinal},
	},
	ToolSurfacePhasePlanExecuteSubmitPlanOnly: {
		AllowedActionClasses:   []string{planExecuteActionSubmitPlan, planExecuteActionSourceSatisfied, planExecuteActionBlocker},
		InvalidActionClasses:   []string{planExecuteActionRead, planExecuteActionSearch, planExecuteActionVerify, planExecuteActionPerEditWrite, planExecuteActionGenericProbe, planExecuteActionProseFinal},
		TerminalResponseShapes: PlanExecuteTerminalSubmitPlanSourceSatisfiedQuestion(),
	},
}

func planExecuteToolSurfaceProjection(phase ToolSurfacePhase, allowedActions, invalidActions, terminalShapes []string) PlanExecuteToolSurfaceProjection {
	tools := ToolSurfacePhaseToolNames(phase)
	return PlanExecuteToolSurfaceProjection{
		Phase:                  phase,
		ParserVisibleTools:     append([]string(nil), tools...),
		CallableTools:          append([]string(nil), tools...),
		AllowedActionClasses:   textutil.Unique(allowedActions),
		InvalidActionClasses:   textutil.Unique(invalidActions),
		TerminalResponseShapes: textutil.Unique(terminalShapes),
	}
}

func removePlanExecuteActionClass(classes []string, removed string) []string {
	if len(classes) == 0 || strings.TrimSpace(removed) == "" {
		return nil
	}

	out := make([]string, 0, len(classes))
	for _, class := range classes {
		if strings.TrimSpace(class) == removed {
			continue
		}
		out = append(out, class)
	}

	return textutil.Unique(out)
}

func FilterToolNames(tools []string, allowed ...string) []string {
	if len(tools) == 0 || len(allowed) == 0 {
		return nil
	}

	allowedSet := make(map[string]struct{}, len(allowed))
	for _, tool := range allowed {
		tool = strings.TrimSpace(tool)
		if tool != "" {
			allowedSet[tool] = struct{}{}
		}
	}

	var out []string
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if _, ok := allowedSet[tool]; ok {
			out = append(out, tool)
		}
	}

	return textutil.Unique(out)
}

func ImplementationTargetWriteReadySourceRefreshTools(tools []string) []string {
	return FilterToolNames(tools, ToolNameReadFile, ToolNameSearchContent, ToolNameFileStats)
}

func ImplementationVerificationReadyHasWrite(tools []string) bool {
	for _, tool := range tools {
		if ClassifyTool(strings.TrimSpace(tool)) == CategoryWrite {
			return true
		}
	}

	return false
}

func ImplementationScopedEditToolAllowed(tool string) bool {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return false
	}

	if IsWorkspaceWriteTool(tool) {
		return true
	}

	switch tool {
	case ToolNameReadFile, ToolNameCodeNavigate, ToolNameSubmitPlan:
		return true
	default:
		return ImplementationScopedSemanticDiscoveryToolAllowed(tool)
	}
}

func ImplementationScopedEditToolAllowedWithExternalReadOnlyDiscovery(tool string, externalReadOnlyDiscovery func(string) bool) bool {
	tool = strings.TrimSpace(tool)
	if ImplementationScopedEditToolAllowed(tool) {
		return true
	}

	return externalReadOnlyDiscovery != nil && externalReadOnlyDiscovery(tool)
}

func ImplementationScopedEditAllowedToolsWithExternalReadOnlyDiscovery(tools []string, externalReadOnlyDiscovery func(string) bool) []string {
	if len(tools) == 0 {
		return nil
	}

	filtered := make([]string, 0, len(tools))
	for _, tool := range tools {
		if ImplementationScopedEditToolAllowedWithExternalReadOnlyDiscovery(tool, externalReadOnlyDiscovery) {
			filtered = append(filtered, tool)
		}
	}

	return textutil.Unique(filtered)
}

func ImplementationScopedSemanticDiscoveryToolAllowed(tool string) bool {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return false
	}

	if len(AllowedToolsForSurfaceProfile([]string{tool}, ToolSurfaceProfileImplementationScopedEdit)) > 0 {
		return true
	}

	profile := DiscoveryProfileForRequest(ToolRequest{Tool: tool})
	switch profile.Class {
	case "language_symbol", "language_relationship", "diagnostics":
		return true
	default:
		return false
	}
}

func PlanExecuteHistoryPendingToolListForPrompt() string {
	tools := ToolSurfacePhaseToolNames(ToolSurfacePhasePlanExecuteHistoryPending)
	for i, tool := range tools {
		tools[i] = "`" + tool + "`"
	}

	return strings.Join(tools, " or ")
}

func PlanExecuteSubmitReadyDisallowedTools(available []string) []string {
	candidates := textutil.Merge(
		available,
		ManifestToolsForSurfaceProfile(ToolSurfaceProfilePlanSubmissionPlanner),
		ToolSurfacePhaseToolNames(ToolSurfacePhasePlanExecuteRequiredSource),
		ToolSurfacePhaseToolNames(ToolSurfacePhasePlanExecuteHistoryPending),
		ToolSurfacePhaseToolNames(ToolSurfacePhasePlanExecuteCandidateSource),
		ToolSurfacePhaseToolNames(ToolSurfacePhasePlanExecuteAnchorRefresh),
		ToolSurfacePhaseToolNames(ToolSurfacePhaseImplementationVerificationReady),
		WorkspaceWriteToolNames(),
		[]string{ToolNameListFiles},
	)

	return ToolSurfacePhaseDisallowedTools(candidates, ToolSurfacePhasePlanExecuteSubmitPlanOnly)
}

func PlanExecuteTextualBaseTools(taskFamily string, briefAllowedTools []string, currentAllowedTools []string) []string {
	baseTools := textutil.Unique(append(append([]string(nil), briefAllowedTools...), currentAllowedTools...))
	if strings.TrimSpace(taskFamily) == string(autonomy.TaskFamilyPlanThenExecutePlanner) {
		baseTools = textutil.Unique(append(baseTools, ToolNameSubmitPlan))
	}

	return baseTools
}

func PlanExecuteTextualProjectionAllowedTools(baseTools []string, projections ...[]string) []string {
	for _, projectionTools := range projections {
		projectionTools = textutil.Unique(projectionTools)
		if len(projectionTools) == 0 {
			continue
		}

		if len(baseTools) == 0 {
			return projectionTools
		}

		allowed := FilterToolNames(baseTools, projectionTools...)
		if len(allowed) > 0 {
			return allowed
		}
	}

	return nil
}

type PlanExecuteTextualAllowedToolsInput struct {
	TaskFamily                       string
	CurrentAllowedTools              []string
	BriefAllowedTools                []string
	BaseTools                        []string
	CallableTools                    []string
	ParserVisibleTools               []string
	TerminalOptions                  []string
	ReviewRepairInitialProbe         bool
	ReviewRepairTerminalAllowedTools []string
	SourceSatisfiedTerminalComplete  bool
	TerminalSubmitPlanAllowed        bool
}

func PlanExecuteTextualAllowedTools(input PlanExecuteTextualAllowedToolsInput) []string {
	switch strings.TrimSpace(input.TaskFamily) {
	case string(autonomy.TaskFamilyPlanThenExecuteReview):
		if input.ReviewRepairInitialProbe {
			return ToolSurfacePhaseAllowedToolsOrDefault(input.BriefAllowedTools, ToolSurfacePhasePlanExecuteReviewRepairInitial)
		}
		if len(input.ReviewRepairTerminalAllowedTools) > 0 {
			return append([]string(nil), input.ReviewRepairTerminalAllowedTools...)
		}
		return append([]string(nil), input.CurrentAllowedTools...)
	case string(autonomy.TaskFamilyPlanThenExecutePlanner):
		if input.SourceSatisfiedTerminalComplete {
			return nil
		}
		if allowed := PlanExecuteTextualProjectionAllowedTools(
			input.BaseTools,
			input.CallableTools,
			input.ParserVisibleTools,
		); len(allowed) > 0 {
			return allowed
		}
		if len(textutil.Unique(input.CurrentAllowedTools)) > 0 {
			return append([]string(nil), input.CurrentAllowedTools...)
		}
		if slices.Contains(input.TerminalOptions, ToolNameSubmitPlan) || input.TerminalSubmitPlanAllowed {
			return ToolSurfacePhaseToolNames(ToolSurfacePhasePlanExecuteSubmitPlanOnly)
		}
	}

	return append([]string(nil), input.CurrentAllowedTools...)
}

func PlanExecuteTextualAllowedToolsForRequest(allowed []string, parserVisibleTools []string, baseTools []string, requestedTool string) []string {
	if len(textutil.Unique(allowed)) == 1 && slices.Contains(allowed, ToolNameSubmitPlan) {
		return allowed
	}

	tool := strings.TrimSpace(requestedTool)
	if tool == "" || slices.Contains(allowed, tool) {
		return allowed
	}

	if !slices.Contains(parserVisibleTools, tool) {
		return allowed
	}

	if len(baseTools) > 0 && !slices.Contains(baseTools, tool) {
		return allowed
	}

	return textutil.Unique(append(append([]string(nil), allowed...), tool))
}
