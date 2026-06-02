package contractcompile

import (
	"fmt"
	"strings"

	"idiom/internal/autonomy"
)

func CompilePlanExecutePlanner(input PlanExecutePlannerInput) (CompileResult, error) {
	normalized := normalizePlanExecutePlannerInput(input)
	if err := validatePlanExecutePlannerInput(normalized); err != nil {
		return CompileResult{}, err
	}
	runtime := compileRuntimeEnvelope(normalized)
	contract, err := compileAutonomyContract(normalized, runtime)
	if err != nil {
		return CompileResult{}, err
	}
	protocol, err := compileProtocolProjection(normalized, runtime, contract)
	if err != nil {
		return CompileResult{}, err
	}
	return CompileResult{
		SchemaVersion: CompilerSchemaVersion,
		Contract:      contract,
		Runtime:       runtime,
		Protocol:      protocol,
	}, nil
}

func compileAutonomyContract(input PlanExecutePlannerInput, runtime RuntimeEnvelope) (autonomy.AutonomyContract, error) {
	obligations := compileObligations(input)
	contract, err := autonomy.NewContract(autonomy.ContractInput{
		ParentID: input.ParentID,
		Scope: autonomy.ContractScope{
			InvocationKind:    autonomy.InvocationPhase,
			Lane:              input.Lane,
			LaneRunID:         input.LaneRunID,
			Phase:             input.Phase,
			PhaseRunID:        input.PhaseRunID,
			TaskFamily:        input.TaskFamily,
			RuntimeContract:   runtime.RuntimeContractID,
			PermissionProfile: input.PermissionProfile,
		},
		Authority:       compileAuthority(input, runtime),
		PathRoles:       compilePathRoles(input),
		Capabilities:    compileCapabilityPolicy(input),
		ToolPolicy:      compileToolPolicy(input),
		Obligations:     obligations,
		Budget:          compileBudget(input),
		Verification:    compileVerification(input),
		Completion:      compileCompletion(input),
		Readiness:       compileReadiness(input, obligations),
		PolicyDecisions: compilePolicyDecisions(input, runtime, obligations),
		Evidence:        compileEvidence(input, runtime),
		Metadata:        autonomy.DefaultContractMetadata(append([]string{"contract_compile", "plan_execute_planner", "phase1_passive"}, input.MetadataTags...)...),
	})
	if err != nil {
		return autonomy.AutonomyContract{}, fmt.Errorf("compile plan-execute planner contract: %w", err)
	}
	return contract, nil
}

func compileRuntimeEnvelope(input PlanExecutePlannerInput) RuntimeEnvelope {
	envelope := RuntimeEnvelope{
		BackendKind:          input.Runtime.BackendKind,
		ModelAlias:           input.Runtime.ModelAlias,
		ModelRole:            input.Runtime.ModelRole,
		ParserTransportMode:  input.Runtime.ParserTransportMode,
		ToolTransportMode:    input.Runtime.ToolTransportMode,
		ParserContract:       input.Runtime.ParserContract,
		NativeToolPolicy:     input.Runtime.NativeToolPolicy,
		SupportsNativeTools:  input.Runtime.SupportsNativeTools,
		ForceNativeTools:     input.Runtime.ForceNativeTools,
		ToolChoice:           input.Runtime.ToolChoice,
		ResponseFormat:       input.Runtime.ResponseFormat,
		ThinkingPolicy:       input.Runtime.ThinkingPolicy,
		ThinkingBudget:       input.Runtime.ThinkingBudget,
		EffectiveMaxTokens:   input.Runtime.EffectiveMaxTokens,
		ContextBudget:        input.Runtime.ContextBudget,
		QueueState:           input.Runtime.QueueState,
		ReadinessProfile:     input.Runtime.ReadinessProfile,
		EnforcementMode:      input.Runtime.EnforcementMode,
		DegradationDecisions: append([]DegradationDecision(nil), input.Runtime.DegradationDecisions...),
		ActiveInvariantID:    InvariantActiveAuthority,
	}
	normalizeRuntimeEnvelope(&envelope)
	envelope.RuntimeContractID = stableHashID("runtime", envelope)
	return envelope
}

func compileProtocolProjection(input PlanExecutePlannerInput, runtime RuntimeEnvelope, contract autonomy.AutonomyContract) (ModelProtocolProjection, error) {
	activeContractID := firstNonEmpty(input.ParentID, contract.ContractID)
	protocol := ModelProtocolProjection{
		ContractID:              activeContractID,
		ContractVersion:         contract.SchemaVersion,
		PhaseID:                 input.Protocol.PhaseID,
		ParserMode:              firstNonEmpty(input.Protocol.ParserMode, runtime.ParserContract, runtime.ParserTransportMode),
		ToolTransportMode:       runtime.ToolTransportMode,
		ParserTransportMode:     runtime.ParserTransportMode,
		ParserContract:          runtime.ParserContract,
		NativeToolPolicy:        runtime.NativeToolPolicy,
		SupportsNativeTools:     runtime.SupportsNativeTools,
		ForceNativeTools:        runtime.ForceNativeTools,
		ToolChoice:              runtime.ToolChoice,
		ResponseFormat:          runtime.ResponseFormat,
		ExpectedVisibleOutput:   input.Protocol.ExpectedVisibleOutput,
		VisibleTools:            append([]string(nil), input.Protocol.VisibleTools...),
		CallableTools:           append([]string(nil), input.Protocol.CallableTools...),
		CallableActionClasses:   append([]string(nil), input.Protocol.CallableActionClasses...),
		InvalidActionClasses:    append([]string(nil), input.Protocol.InvalidActionClasses...),
		TerminalResponseShapes:  append([]string(nil), input.Protocol.TerminalResponseShapes...),
		StructuredSchemaNames:   append([]string(nil), input.Protocol.StructuredSchemaNames...),
		MalformedOutputPolicy:   input.Protocol.MalformedOutputPolicy,
		StaleAnchorPolicy:       input.Protocol.StaleAnchorPolicy,
		SourceBodyObligations:   append([]string(nil), input.Protocol.SourceBodyObligations...),
		RequiredEditCoverage:    append([]string(nil), input.RequiredEditPaths...),
		PathRoleSummary:         compileProtocolPathRoleSummary(contract.PathRoles),
		VerificationObligations: compileProtocolVerificationObligations(input),
		RequiredTools:           append([]string(nil), input.RequiredTools...),
		ReadinessGate:           append([]string(nil), input.ReadinessGate...),
		Constraints:             append([]string(nil), input.Constraints...),
		InvalidActions:          append([]string(nil), input.InvalidActions...),
		RecoveryBudget:          input.Protocol.RecoveryBudget,
		ActiveInvariantID:       InvariantActiveAuthority,
	}
	if len(protocol.VisibleTools) == 0 {
		protocol.VisibleTools = append([]string(nil), input.AllowedTools...)
	}
	if !input.Protocol.CallableToolsSpecified && len(protocol.CallableTools) == 0 {
		protocol.CallableTools = append([]string(nil), protocol.VisibleTools...)
	}
	if protocol.PhaseID == "" {
		protocol.PhaseID = "plan_execute.required_source"
	}
	if protocol.ExpectedVisibleOutput == "" {
		protocol.ExpectedVisibleOutput = "parser_readable_tool_or_terminal_outcome"
	}
	if len(protocol.CallableActionClasses) == 0 {
		protocol.CallableActionClasses = []string{
			string(autonomy.ActionClassRead),
			string(autonomy.ActionClassSearch),
			string(autonomy.ActionClassSubmitPlan),
			string(autonomy.ActionClassSourceSatisfied),
			string(autonomy.ActionClassBlocker),
		}
	}
	if len(protocol.InvalidActionClasses) == 0 {
		protocol.InvalidActionClasses = []string{
			string(autonomy.ActionClassPerEditWrite),
			string(autonomy.ActionClassVerify),
			string(autonomy.ActionClassProseFinal),
		}
	}
	if len(protocol.TerminalResponseShapes) == 0 {
		protocol.TerminalResponseShapes = []string{"submit_plan", "Decision: source-satisfied", "<<QUESTION>>:"}
	}
	protocol.SafeAlternatives = compileProtocolSafeAlternatives(protocol.TerminalResponseShapes, protocol.CallableActionClasses)
	if len(protocol.StructuredSchemaNames) == 0 && protocolAllowsSubmitPlan(protocol) {
		protocol.StructuredSchemaNames = []string{"submit_plan"}
	}
	if protocol.MalformedOutputPolicy == "" {
		protocol.MalformedOutputPolicy = "recover_with_same_protocol_projection"
	}
	if protocol.StaleAnchorPolicy == "" {
		protocol.StaleAnchorPolicy = "bounded_source_refresh_before_submit_plan"
	}
	if protocol.RecoveryBudget == 0 {
		protocol.RecoveryBudget = 2
	}
	normalizeProtocolProjection(&protocol)
	if err := validateProtocolProjection(protocol); err != nil {
		return ModelProtocolProjection{}, err
	}
	protocol.ProtocolHash = stableHashID("protocol", protocol)
	return protocol, nil
}

func protocolAllowsSubmitPlan(protocol ModelProtocolProjection) bool {
	return containsProtocolString(protocol.TerminalResponseShapes, string(autonomy.ActionClassSubmitPlan)) ||
		containsProtocolString(protocol.CallableActionClasses, string(autonomy.ActionClassSubmitPlan)) ||
		containsProtocolString(protocol.CallableTools, "submit_plan")
}

func containsProtocolString(values []string, want string) bool {
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

func compileProtocolPathRoleSummary(roles []autonomy.PathRole) []string {
	out := make([]string, 0, len(roles))
	for _, role := range roles {
		if role.Role == "" || strings.TrimSpace(role.PathPattern) == "" {
			continue
		}
		out = append(out, string(role.Role)+":"+strings.TrimSpace(role.PathPattern))
	}
	return uniqueStrings(out)
}

func compileProtocolVerificationObligations(input PlanExecutePlannerInput) []string {
	var out []string
	if strings.TrimSpace(input.VerificationContract) != "" {
		out = append(out, "contract:"+strings.TrimSpace(input.VerificationContract))
	}
	for _, command := range input.VerificationPlan {
		out = append(out, "command:"+strings.TrimSpace(command))
	}
	return uniqueStrings(out)
}

func compileProtocolSafeAlternatives(terminalShapes []string, actionClasses []string) []string {
	alternatives := append([]string(nil), terminalShapes...)
	if len(alternatives) == 0 {
		for _, actionClass := range actionClasses {
			switch strings.TrimSpace(actionClass) {
			case string(autonomy.ActionClassSubmitPlan):
				alternatives = append(alternatives, "submit_plan")
			case string(autonomy.ActionClassSourceSatisfied):
				alternatives = append(alternatives, "Decision: source-satisfied")
			case string(autonomy.ActionClassBlocker):
				alternatives = append(alternatives, "<<QUESTION>>:")
			case string(autonomy.ActionClassReviewPass):
				alternatives = append(alternatives, "Decision: pass")
			}
		}
	}
	return uniqueStrings(alternatives)
}

func compileAuthority(input PlanExecutePlannerInput, runtime RuntimeEnvelope) []autonomy.AuthoritySource {
	authority := []autonomy.AuthoritySource{
		{Kind: "compiled_contract", Ref: InvocationPlanExecutePlanner, Freshness: autonomy.FreshnessFresh, Description: "passive plan-execute planner compiler"},
		{Kind: "runtime_envelope", Ref: runtime.RuntimeContractID, Freshness: autonomy.FreshnessFresh, Description: "model runtime envelope bound before dispatch"},
	}
	if input.RuntimeContract != "" {
		authority = append(authority, autonomy.AuthoritySource{Kind: "legacy_runtime_contract", Ref: input.RuntimeContract, Freshness: autonomy.FreshnessFresh})
	}
	for _, ref := range input.TargetRefs {
		authority = append(authority, autonomy.AuthoritySource{Kind: "target_ref", Ref: ref, Freshness: autonomy.FreshnessFresh})
	}
	for _, ref := range input.TargetPacket {
		authority = append(authority, autonomy.AuthoritySource{Kind: "target_packet", Ref: ref, Freshness: autonomy.FreshnessFresh})
	}
	for _, boundary := range input.UserBoundaries {
		authority = append(authority, autonomy.AuthoritySource{Kind: "user_boundary", Ref: boundary, Freshness: autonomy.FreshnessFresh})
	}
	return authority
}

func compilePathRoles(input PlanExecutePlannerInput) []autonomy.PathRole {
	var roles []autonomy.PathRole
	for _, path := range input.RequiredEditPaths {
		roles = appendPathRole(roles, autonomy.PathRoleRequiredEdit, path, "required_edit_paths")
	}
	for _, path := range input.AllowedEditPaths {
		roles = appendPathRole(roles, autonomy.PathRoleAllowedEdit, path, "allowed_edit_paths")
	}
	for _, path := range input.EvidencePaths {
		roles = appendPathRole(roles, autonomy.PathRoleEvidenceReadonly, path, "evidence_paths")
	}
	for _, path := range input.ChangedPaths {
		roles = appendPathRole(roles, autonomy.PathRoleGeneratedArtifact, path, "changed_paths")
	}
	roles = appendPathRole(roles, autonomy.PathRoleProtected, ".git/**", "repository metadata boundary")
	return roles
}

func appendPathRole(roles []autonomy.PathRole, role autonomy.PathRoleKind, path string, reason string) []autonomy.PathRole {
	path = normalizePath(path)
	if path == "" {
		return roles
	}
	return append(roles, autonomy.PathRole{
		Role:        role,
		PathPattern: path,
		Freshness:   autonomy.FreshnessFresh,
		Reason:      reason,
	})
}

func compileCapabilityPolicy(input PlanExecutePlannerInput) autonomy.CapabilityPolicy {
	allowed := map[autonomy.Capability]struct{}{}
	forbidden := map[autonomy.Capability]struct{}{}
	for _, tool := range input.AllowedTools {
		for _, capability := range capabilitiesForTool(tool) {
			allowed[capability] = struct{}{}
		}
	}
	for _, tool := range input.ForbiddenTools {
		for _, capability := range capabilitiesForTool(tool) {
			forbidden[capability] = struct{}{}
		}
	}
	if strings.Contains(strings.ToLower(input.PermissionProfile), "read_only") {
		forbidden[autonomy.CapabilitySourceEdit] = struct{}{}
		forbidden[autonomy.CapabilityApplyPatch] = struct{}{}
		forbidden[autonomy.CapabilityMutatingShell] = struct{}{}
	}
	return autonomy.CapabilityPolicy{
		Allowed:          capabilityKeys(allowed),
		Forbidden:        capabilityKeys(forbidden),
		HiddenTools:      append([]string(nil), input.ForbiddenTools...),
		AllowedEffects:   allowedEffects(input),
		ForbiddenEffects: forbiddenEffects(input),
	}
}

func compileToolPolicy(input PlanExecutePlannerInput) []autonomy.ToolPolicyRule {
	var rules []autonomy.ToolPolicyRule
	for _, tool := range input.AllowedTools {
		for _, capability := range capabilitiesForTool(tool) {
			rules = append(rules, autonomy.ToolPolicyRule{
				ToolPattern: tool,
				Decision:    autonomy.PolicyAllow,
				Capability:  capability,
				SideEffect:  sideEffectForTool(tool),
				Source:      "contractcompile.plan_execute_planner.report_only",
				ReasonCode:  "declared_visible_tool_allowed_report_only",
			})
		}
	}
	for _, tool := range input.ForbiddenTools {
		for _, capability := range capabilitiesForTool(tool) {
			rules = append(rules, autonomy.ToolPolicyRule{
				ToolPattern: tool,
				Decision:    autonomy.PolicyReject,
				Capability:  capability,
				SideEffect:  sideEffectForTool(tool),
				Source:      "contractcompile.plan_execute_planner.report_only",
				ReasonCode:  "declared_hidden_tool_forbidden_report_only",
			})
		}
	}
	return rules
}

func compileObligations(input PlanExecutePlannerInput) []autonomy.Obligation {
	var obligations []autonomy.Obligation
	for _, path := range input.RequiredEditPaths {
		path = normalizePath(path)
		if path == "" {
			continue
		}
		obligations = append(obligations, autonomy.Obligation{
			ObligationID: stableHashID("obl_plan_coverage", path),
			Kind:         autonomy.ObligationEvidence,
			Status:       autonomy.ObligationOpen,
			Owner:        autonomy.OwnerPlanner,
			TargetRef:    path,
			RequiredBy:   "submit_plan_required_edit_coverage",
		})
	}
	for _, evidence := range uniqueStrings(append(append([]string(nil), input.RequiredEvidence...), input.Protocol.SourceBodyObligations...)) {
		evidence = strings.TrimSpace(evidence)
		if evidence == "" {
			continue
		}
		obligations = append(obligations, autonomy.Obligation{
			ObligationID: stableHashID("obl_evidence", evidence),
			Kind:         autonomy.ObligationEvidence,
			Status:       autonomy.ObligationOpen,
			Owner:        autonomy.OwnerPlanner,
			TargetRef:    evidence,
			RequiredBy:   "source_body_obligations",
		})
	}
	if verificationRequired(input) {
		obligations = append(obligations, autonomy.Obligation{
			ObligationID: stableHashID("obl_verify", input.VerificationContract, input.VerificationPlan),
			Kind:         autonomy.ObligationVerify,
			Status:       autonomy.ObligationOpen,
			Owner:        autonomy.OwnerLane,
			TargetRef:    input.VerificationContract,
			RequiredBy:   "verification_contract",
			Command:      strings.Join(input.VerificationPlan, " && "),
		})
	}
	obligations = append(obligations, autonomy.Obligation{
		ObligationID:           stableHashID("obl_final_answer", input.Lane, input.Phase, input.TaskFamily),
		Kind:                   autonomy.ObligationFinalAnswer,
		Status:                 autonomy.ObligationOpen,
		Owner:                  autonomy.OwnerPlanner,
		RequiredBy:             "completion_gate",
		FinalAnswerRequirement: "terminal planner outcome must match protocol projection",
	})
	return obligations
}

func compileBudget(input PlanExecutePlannerInput) autonomy.BudgetState {
	return autonomy.BudgetState{
		MaxToolSteps:       input.Budget.MaxToolSteps,
		MaxModelCalls:      input.Budget.MaxModelCalls,
		MaxContextMessages: firstPositive(input.Runtime.ContextBudget.MaxContextMessages, input.Budget.MaxContextMessages),
		MaxContextChars:    firstPositive(input.Runtime.ContextBudget.MaxContextChars, input.Budget.MaxContextChars),
	}
}

func compileVerification(input PlanExecutePlannerInput) autonomy.VerificationRequirement {
	return autonomy.VerificationRequirement{
		Required:               verificationRequired(input),
		Contract:               input.VerificationContract,
		RequiredAfterWrite:     len(input.RequiredEditPaths) > 0,
		SourceAwareRequired:    len(input.RequiredEditPaths) > 0,
		AcceptedCommands:       append([]string(nil), input.VerificationPlan...),
		SkippedRequiresBlocker: len(input.RequiredEditPaths) > 0,
	}
}

func compileCompletion(input PlanExecutePlannerInput) autonomy.CompletionRequirement {
	criteria := append([]string{
		"terminal outcome conforms to model protocol projection",
		"open obligations are resolved or explicitly blocked",
	}, input.FinalCritic...)
	criteria = append(criteria, input.StopConditions...)
	return autonomy.CompletionRequirement{
		Required:               true,
		Criteria:               criteria,
		BlockOnOpenObligations: true,
		AllowedNoopReasons:     []string{"source_satisfied", "blocked_by_exact_question"},
	}
}

func compileReadiness(input PlanExecutePlannerInput, obligations []autonomy.Obligation) autonomy.ReadinessRequirement {
	openRefs := append([]string(nil), input.Readiness.OpenObligations...)
	for _, obligation := range obligations {
		if obligation.Status == autonomy.ObligationOpen {
			openRefs = append(openRefs, obligation.ObligationID)
		}
	}
	return autonomy.ReadinessRequirement{
		ReadyToAnswer:       input.Readiness.ReadyToAnswer,
		ReadyToWrite:        input.Readiness.ReadyToWrite,
		ReadyToVerify:       input.Readiness.ReadyToVerify,
		ClosedClaims:        append([]string(nil), input.Readiness.ClosedClaims...),
		OpenObligationRefs:  openRefs,
		EvidenceRefs:        append([]string(nil), input.Readiness.EvidenceSources...),
		FurtherReadsRequire: input.Readiness.FurtherReadsRequire,
	}
}

func compilePolicyDecisions(input PlanExecutePlannerInput, runtime RuntimeEnvelope, obligations []autonomy.Obligation) []autonomy.PolicyDecision {
	return []autonomy.PolicyDecision{{
		DecisionID:     stableHashID("decision", InvariantActiveAuthority, input.Lane, input.Phase, runtime.RuntimeContractID),
		Decision:       autonomy.PolicyObserve,
		InvariantID:    InvariantActiveAuthority,
		ReasonCode:     "passive_contract_compiled_before_dispatch",
		ObligationRefs: obligationIDs(obligations),
		EvidenceRefs:   []string{runtime.RuntimeContractID},
		Source:         "internal/contractcompile",
	}}
}

func compileEvidence(input PlanExecutePlannerInput, runtime RuntimeEnvelope) []autonomy.EvidenceRef {
	evidence := []autonomy.EvidenceRef{{
		EvidenceID: runtime.RuntimeContractID,
		Kind:       "runtime_envelope",
		Freshness:  autonomy.FreshnessFresh,
		Confidence: "compiled",
		Source:     "internal/contractcompile",
		Summary:    "passive model runtime envelope",
	}}
	for _, path := range input.EvidencePaths {
		path = normalizePath(path)
		if path == "" {
			continue
		}
		evidence = append(evidence, autonomy.EvidenceRef{
			EvidenceID: stableHashID("evidence_path", path),
			Kind:       "source_path",
			Path:       path,
			Freshness:  autonomy.FreshnessFresh,
			Confidence: "declared",
			Source:     "planner_input",
		})
	}
	return evidence
}

func verificationRequired(input PlanExecutePlannerInput) bool {
	return input.VerificationContract != "" || len(input.VerificationPlan) > 0 || len(input.RequiredEditPaths) > 0
}
