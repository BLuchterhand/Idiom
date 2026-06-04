package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"idiom/internal/autonomy"
	"idiom/internal/contractcompile"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/util"
	"idiom/internal/workspace"
)

const runnerAutonomyEventRole = "autonomy_event"
const runnerAutonomyDisableModelExchangeEnv = "IDIOM_DISABLE_RUNNER_MODEL_EXCHANGE_EVENTS"

type runnerAutonomyRecorder struct {
	runner             *Runner
	brief              workerruntime.WorkerRuntimeBrief
	contract           autonomy.AutonomyContract
	activeAuthority    contractcompile.ActiveAuthorityBinding
	plannerCompile     contractcompile.CompileResult
	hasPlannerCompile  bool
	sequence           int64
	occurredAt         time.Time
	obligations        []autonomy.Obligation
	openObligations    map[string]autonomy.Obligation
	closedObligations  map[string]struct{}
	lastTurnPhase      TurnPhaseContract
	lastTurnPhaseEvent autonomy.TurnPhaseContractEvent
	lastModelProtocol  contractcompile.ModelProtocolProjection
	lastModelPresented bool
}

type runnerAutonomyGateDecision struct {
	Gate                  autonomy.GateKind
	Decision              autonomy.ToolPolicyDecision
	InvariantID           string
	ReasonCode            string
	ToolCallID            string
	ToolName              string
	TargetRefs            []string
	ChangedPaths          []string
	VerificationStatus    string
	Budget                *autonomy.BudgetState
	LoopFingerprintDigest string
	Attempts              int
	Source                string
}

type runnerAutonomyPolicyEventInput struct {
	EventType        autonomy.EventType
	Decision         autonomy.ToolPolicyDecision
	InvariantID      string
	ReasonCode       string
	Transport        autonomy.ToolTransport
	ParserFailure    autonomy.ParserFailureClass
	RepairSource     autonomy.RepairSource
	ApprovalRequired bool
	Result           string
	ErrorClass       string
	ChangedPaths     []string
	Source           string
}

type runnerAutonomyGateEventFields struct {
	PackID           autonomy.ContractPackID
	EnforcementMode  autonomy.EnforcementMode
	ObservedFacts    []autonomy.ContractObservedFact
	SafeAlternatives []string
}

func newRunnerAutonomyRecorder(r *Runner, policy RunPolicy, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) *runnerAutonomyRecorder {
	if r == nil || r.OnEvent == nil {
		return nil
	}
	compiled, ok := buildRunnerAutonomyContract(r, policy, brief, results)
	if !ok {
		return nil
	}
	return &runnerAutonomyRecorder{
		runner:            r,
		brief:             workerruntime.NormalizeRuntimeBrief(brief),
		contract:          compiled.contract,
		activeAuthority:   compiled.activeAuthority,
		plannerCompile:    compiled.plannerCompile,
		hasPlannerCompile: compiled.hasPlannerCompile,
		occurredAt:        time.Now().UTC(),
		obligations:       compiled.obligations,
		openObligations:   make(map[string]autonomy.Obligation),
		closedObligations: make(map[string]struct{}),
	}
}

type runnerAutonomyContractBuild struct {
	contract          autonomy.AutonomyContract
	obligations       []autonomy.Obligation
	activeAuthority   contractcompile.ActiveAuthorityBinding
	plannerCompile    contractcompile.CompileResult
	hasPlannerCompile bool
}

func buildRunnerAutonomyContract(r *Runner, policy RunPolicy, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (runnerAutonomyContractBuild, bool) {
	if planThenExecutePlannerBrief(brief) {
		compiled, ok := compileRunnerPlanExecutePlannerContract(r, policy, brief, results)
		if !ok {
			return runnerAutonomyContractBuild{}, false
		}
		return runnerAutonomyContractBuild{
			contract:          compiled.Contract,
			obligations:       append([]autonomy.Obligation(nil), compiled.Contract.Obligations...),
			activeAuthority:   compiled.ActiveAuthority,
			plannerCompile:    compiled,
			hasPlannerCompile: true,
		}, true
	}
	contract, obligations, ok := legacyRunnerAutonomyContract(r, policy, brief, results)
	if !ok {
		return runnerAutonomyContractBuild{}, false
	}
	return runnerAutonomyContractBuild{
		contract:        contract,
		obligations:     obligations,
		activeAuthority: runnerLegacyActiveAuthorityBinding(contract),
	}, true
}

func compileRunnerPlanExecutePlannerContract(r *Runner, policy RunPolicy, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (contractcompile.CompileResult, bool) {
	input, ok := runnerPlanExecutePlannerInput(brief, results, TurnPhaseContract{}, policy, false, "")
	if !ok {
		return contractcompile.CompileResult{}, false
	}
	compiled, err := contractcompile.CompilePlanExecutePlanner(input)
	if err != nil {
		if r != nil {
			r.emitEvent(marshalRunEvent("runner_autonomy_contract_gap", map[string]any{
				"task_family": strings.TrimSpace(brief.TaskFamily),
				"reason":      "compile_failed",
				"error":       err.Error(),
			}))
		}
		return contractcompile.CompileResult{}, false
	}
	return compiled, true
}

func runnerLegacyActiveAuthorityBinding(contract autonomy.AutonomyContract) contractcompile.ActiveAuthorityBinding {
	return contractcompile.ActiveAuthorityBinding{
		ContractID:        contract.ContractID,
		RuntimeContractID: strings.TrimSpace(contract.Scope.RuntimeContract),
		ActiveInvariantID: contractcompile.InvariantActiveAuthority,
	}
}

func (a *runnerAutonomyRecorder) activeAuthorityContractID() string {
	if a == nil {
		return ""
	}
	if a.hasPlannerCompile && strings.TrimSpace(a.activeAuthority.ContractID) != "" {
		return a.activeAuthority.ContractID
	}
	return a.contract.ContractID
}

func (a *runnerAutonomyRecorder) contractIDForTurnPhase() string {
	return a.activeAuthorityContractID()
}

func (a *runnerAutonomyRecorder) activeAuthorityInvariantID() string {
	if a == nil {
		return contractcompile.InvariantActiveAuthority
	}
	if strings.TrimSpace(a.activeAuthority.ActiveInvariantID) != "" {
		return a.activeAuthority.ActiveInvariantID
	}
	return contractcompile.InvariantActiveAuthority
}

func (a *runnerAutonomyRecorder) activeAuthorityForProtocol(protocol contractcompile.ModelProtocolProjection) contractcompile.ActiveAuthorityBinding {
	if a == nil {
		return contractcompile.ActiveAuthorityBinding{}
	}
	binding := a.activeAuthority
	if strings.TrimSpace(binding.ContractID) == "" {
		binding.ContractID = a.contract.ContractID
	}
	if strings.TrimSpace(binding.RuntimeContractID) == "" {
		binding.RuntimeContractID = strings.TrimSpace(a.contract.Scope.RuntimeContract)
	}
	if a.hasPlannerCompile {
		if strings.TrimSpace(protocol.ProtocolHash) != "" {
			binding.ProtocolHash = protocol.ProtocolHash
		} else if strings.TrimSpace(binding.ProtocolHash) == "" {
			binding.ProtocolHash = protocol.ProtocolHash
		}
		if strings.TrimSpace(binding.ActiveInvariantID) == "" {
			binding.ActiveInvariantID = util.FirstNonEmpty(strings.TrimSpace(protocol.ActiveInvariantID), contractcompile.InvariantActiveAuthority)
		}
		return binding
	}
	if strings.TrimSpace(protocol.ContractID) != "" {
		binding.ContractID = protocol.ContractID
	}
	if strings.TrimSpace(protocol.ProtocolHash) != "" {
		binding.ProtocolHash = protocol.ProtocolHash
	}
	if strings.TrimSpace(protocol.ActiveInvariantID) != "" {
		binding.ActiveInvariantID = protocol.ActiveInvariantID
	}
	if strings.TrimSpace(binding.ActiveInvariantID) == "" {
		binding.ActiveInvariantID = contractcompile.InvariantActiveAuthority
	}
	return binding
}

func (a *runnerAutonomyRecorder) bindModelProtocolProjection(protocol contractcompile.ModelProtocolProjection) contractcompile.ModelProtocolProjection {
	if a == nil {
		return protocol
	}
	if a.hasPlannerCompile {
		binding := a.activeAuthority
		if strings.TrimSpace(binding.ContractID) == "" {
			binding.ContractID = a.contract.ContractID
		}
		if strings.TrimSpace(binding.ActiveInvariantID) == "" {
			binding.ActiveInvariantID = contractcompile.InvariantActiveAuthority
		}
		return protocolWithActiveAuthority(protocol, binding)
	}
	return protocolWithActiveAuthority(protocol, a.activeAuthorityForProtocol(protocol))
}

func protocolWithActiveAuthority(protocol contractcompile.ModelProtocolProjection, binding contractcompile.ActiveAuthorityBinding) contractcompile.ModelProtocolProjection {
	if strings.TrimSpace(binding.ContractID) != "" {
		protocol.ContractID = binding.ContractID
	}
	if strings.TrimSpace(binding.ProtocolHash) != "" {
		protocol.ProtocolHash = binding.ProtocolHash
	}
	if strings.TrimSpace(binding.ActiveInvariantID) != "" {
		protocol.ActiveInvariantID = binding.ActiveInvariantID
	}
	return protocol
}

func runnerAutonomyContract(r *Runner, policy RunPolicy, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (autonomy.AutonomyContract, []autonomy.Obligation, bool) {
	compiled, ok := buildRunnerAutonomyContract(r, policy, brief, results)
	if !ok {
		return autonomy.AutonomyContract{}, nil, false
	}
	return compiled.contract, compiled.obligations, true
}

func legacyRunnerAutonomyContract(r *Runner, policy RunPolicy, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (autonomy.AutonomyContract, []autonomy.Obligation, bool) {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	lane := util.FirstNonEmpty(strings.TrimSpace(brief.Lane), r.agentName())
	if lane == "" {
		lane = "main"
	}
	scope := autonomy.ContractScope{
		InvocationKind:    autonomy.InvocationLane,
		Lane:              lane,
		TaskFamily:        strings.TrimSpace(brief.TaskFamily),
		RuntimeContract:   strings.TrimSpace(brief.Contract),
		PermissionProfile: strings.TrimSpace(brief.PermissionProfile),
	}
	pathRoles := runnerAutonomyPathRoles(brief, results)
	obligations := runnerAutonomyObligations(brief, policy, results)
	contract, err := autonomy.NewContract(autonomy.ContractInput{
		Scope:        scope,
		Authority:    runnerAutonomyAuthority(brief),
		PathRoles:    pathRoles,
		Capabilities: runnerAutonomyCapabilities(brief),
		Obligations:  obligations,
		Budget: autonomy.BudgetState{
			MaxToolSteps:       policy.MaxToolSteps,
			MaxModelCalls:      policy.MaxModelCalls,
			MaxContextMessages: policy.MaxContextMessages,
			MaxContextChars:    policy.MaxContextChars,
		},
		Verification: autonomy.VerificationRequirement{
			Required:               runnerVerificationRequired(brief),
			Contract:               strings.TrimSpace(brief.VerificationContract),
			RequiredAfterWrite:     runnerBriefRequiresMutation(brief) && !verificationContractSkipsGate(brief.VerificationContract),
			SourceAwareRequired:    runnerBriefRequiresMutation(brief),
			AcceptedCommands:       append([]string(nil), brief.VerificationPlan...),
			SkippedRequiresBlocker: runnerBriefRequiresMutation(brief),
		},
		Completion: autonomy.CompletionRequirement{
			Required:               true,
			Criteria:               runnerAutonomyCompletionCriteria(brief),
			BlockOnOpenObligations: true,
		},
		Readiness: runnerAutonomyReadiness(brief, obligations),
		Metadata:  autonomy.DefaultContractMetadata("p1.5", "runner_gate_contracts"),
	})
	if err != nil {
		return autonomy.AutonomyContract{}, nil, false
	}
	return contract, obligations, true
}

func runnerAutonomyAuthority(brief workerruntime.WorkerRuntimeBrief) []autonomy.AuthoritySource {
	authority := []autonomy.AuthoritySource{
		{Kind: "runtime_brief", Ref: "runtime_brief", Freshness: autonomy.FreshnessFresh},
	}
	for _, marker := range brief.TargetPacket {
		marker = strings.TrimSpace(marker)
		if marker == "" {
			continue
		}
		authority = append(authority, autonomy.AuthoritySource{
			Kind:      "target_packet",
			Ref:       marker,
			Freshness: autonomy.FreshnessFresh,
		})
	}
	return authority
}

func runnerAutonomyPathRoles(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []autonomy.PathRole {
	var roles []autonomy.PathRole
	for _, path := range brief.RequiredEditPaths {
		if normalized := normalizeRunnerPath(path); normalized != "" {
			roles = append(roles, autonomy.PathRole{Role: autonomy.PathRoleRequiredEdit, PathPattern: normalized, Freshness: autonomy.FreshnessFresh, Reason: "runtime brief required_edit_paths"})
		}
	}
	for _, path := range brief.EvidencePaths {
		if normalized := normalizeRunnerPath(path); normalized != "" {
			roles = append(roles, autonomy.PathRole{Role: autonomy.PathRoleEvidenceReadonly, PathPattern: normalized, Freshness: autonomy.FreshnessFresh, Reason: "runtime brief evidence_paths"})
		}
	}
	for _, path := range mapKeys(changedPathsFromToolResults(results)) {
		if normalized := normalizeRunnerPath(path); normalized != "" {
			roles = append(roles, autonomy.PathRole{Role: autonomy.PathRoleGeneratedArtifact, PathPattern: normalized, Freshness: autonomy.FreshnessFresh, Reason: "recorded changed_paths"})
		}
	}
	roles = append(roles, autonomy.PathRole{Role: autonomy.PathRoleProtected, PathPattern: ".git/**", Freshness: autonomy.FreshnessFresh, Reason: "repository metadata boundary"})
	return roles
}

func runnerAutonomyCapabilities(brief workerruntime.WorkerRuntimeBrief) autonomy.CapabilityPolicy {
	allowedSet := map[autonomy.Capability]struct{}{}
	forbiddenSet := map[autonomy.Capability]struct{}{}
	for _, tool := range brief.AllowedTools {
		for _, capability := range runnerAutonomyCapabilitiesForTool(brief, tool) {
			allowedSet[capability] = struct{}{}
		}
	}
	for _, tool := range brief.ForbiddenTools {
		for _, capability := range runnerAutonomyCapabilitiesForTool(brief, tool) {
			forbiddenSet[capability] = struct{}{}
		}
	}
	if workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		forbiddenSet[autonomy.CapabilitySourceEdit] = struct{}{}
		forbiddenSet[autonomy.CapabilityApplyPatch] = struct{}{}
		forbiddenSet[autonomy.CapabilityMutatingShell] = struct{}{}
	}
	return autonomy.CapabilityPolicy{
		Allowed:          mapAutonomyCapabilityKeys(allowedSet),
		Forbidden:        mapAutonomyCapabilityKeys(forbiddenSet),
		AllowedEffects:   runnerAutonomyAllowedEffects(brief),
		ForbiddenEffects: runnerAutonomyForbiddenEffects(brief),
		HiddenTools:      append([]string(nil), brief.ForbiddenTools...),
	}
}

func runnerAutonomyCapabilitiesForTool(brief workerruntime.WorkerRuntimeBrief, tool string) []autonomy.Capability {
	if metadata, ok := runnerAutonomyPlannerToolMetadata(brief, tool); ok {
		return append([]autonomy.Capability(nil), metadata.Capabilities...)
	}
	return runnerAutonomyFallbackCapabilitiesForTool(tool)
}

func runnerAutonomyPlannerToolMetadata(brief workerruntime.WorkerRuntimeBrief, tool string) (autonomy.PlanExecutePlannerToolMetadata, bool) {
	if !planThenExecutePlannerBrief(brief) {
		return autonomy.PlanExecutePlannerToolMetadata{}, false
	}
	return autonomy.LookupPlanExecutePlannerToolMetadata(tool)
}

func runnerAutonomyFallbackCapabilitiesForTool(tool string) []autonomy.Capability {
	switch strings.TrimSpace(tool) {
	case "read_file":
		return []autonomy.Capability{autonomy.CapabilityBoundedRead}
	case "search_files", "search_content", "rg", "grep":
		return []autonomy.Capability{autonomy.CapabilityLiteralSearch}
	case "context_pack", "context_query", "context_zoom", "discovery_query", "go_read_symbol", "go_find_symbol", "cpp_read_symbol", "cpp_find_symbol":
		return []autonomy.Capability{autonomy.CapabilitySemanticDiscovery}
	case "edit_file", "write_file", "append_file", "mkdir":
		return []autonomy.Capability{autonomy.CapabilitySourceEdit}
	case "apply_patch":
		return []autonomy.Capability{autonomy.CapabilityApplyPatch}
	case "shell", "source_sanity", "go_diagnostics", "cpp_diagnostics":
		return []autonomy.Capability{autonomy.CapabilityShellVerification}
	case "task":
		return []autonomy.Capability{autonomy.CapabilityDelegation}
	case "submit_plan":
		return []autonomy.Capability{autonomy.CapabilityPlanSubmission}
	default:
		return nil
	}
}

func runnerAutonomyAllowedEffects(brief workerruntime.WorkerRuntimeBrief) []autonomy.SideEffectClass {
	if strings.Contains(strings.ToLower(strings.TrimSpace(brief.PermissionProfile)), "read_only") {
		return []autonomy.SideEffectClass{autonomy.SideEffectReadOnly}
	}
	if runnerBriefRequiresMutation(brief) {
		return []autonomy.SideEffectClass{autonomy.SideEffectReadOnly, autonomy.SideEffectAdditive}
	}
	return []autonomy.SideEffectClass{autonomy.SideEffectReadOnly}
}

func runnerAutonomyForbiddenEffects(brief workerruntime.WorkerRuntimeBrief) []autonomy.SideEffectClass {
	if strings.Contains(strings.ToLower(strings.TrimSpace(brief.PermissionProfile)), "read_only") {
		return []autonomy.SideEffectClass{autonomy.SideEffectAdditive, autonomy.SideEffectDestructive, autonomy.SideEffectOpenWorld}
	}
	return []autonomy.SideEffectClass{autonomy.SideEffectDestructive, autonomy.SideEffectOpenWorld}
}

func runnerAutonomyObligations(brief workerruntime.WorkerRuntimeBrief, policy RunPolicy, results []workspace.ToolResult) []autonomy.Obligation {
	changed := changedPathsFromToolResults(results)
	var obligations []autonomy.Obligation
	if runnerBriefRequiresMutation(brief) {
		for _, path := range brief.RequiredEditPaths {
			path = normalizeRunnerPath(path)
			if path == "" {
				continue
			}
			status := autonomy.ObligationOpen
			if _, ok := changed[path]; ok {
				status = autonomy.ObligationResolved
			}
			obligations = append(obligations, autonomy.Obligation{
				ObligationID: runnerAutonomyObligationID(autonomy.ObligationWrite, path),
				Kind:         autonomy.ObligationWrite,
				Status:       status,
				Owner:        autonomy.OwnerLane,
				TargetRef:    path,
				RequiredBy:   "required_edit_paths",
			})
		}
		if len(brief.RequiredEditPaths) == 0 {
			status := autonomy.ObligationOpen
			if len(changed) > 0 {
				status = autonomy.ObligationResolved
			}
			obligations = append(obligations, autonomy.Obligation{
				ObligationID: "obl-write-workspace-change",
				Kind:         autonomy.ObligationWrite,
				Status:       status,
				Owner:        autonomy.OwnerLane,
				RequiredBy:   "mutation_contract",
			})
		}
	}
	if !runnerBriefRequiresMutation(brief) {
		status := autonomy.ObligationOpen
		if readOnlyAnalysisReadyFromEvidence(results) {
			status = autonomy.ObligationResolved
		}
		obligations = append(obligations, autonomy.Obligation{
			ObligationID: "obl-evidence-read-only",
			Kind:         autonomy.ObligationEvidence,
			Status:       status,
			Owner:        autonomy.OwnerLane,
			RequiredBy:   "read_only_evidence_contract",
		})
	}
	if runnerBriefRequiresMutation(brief) && !verificationContractSkipsGate(brief.VerificationContract) {
		owner := autonomy.OwnerHost
		if plannerOwnsVerification(brief) {
			owner = autonomy.OwnerPlanner
		}
		status := autonomy.ObligationOpen
		if !hasPendingVerificationAfterWrites(results, brief) && hasSuccessfulVerificationResult(results) {
			status = autonomy.ObligationResolved
		}
		obligations = append(obligations, autonomy.Obligation{
			ObligationID: "obl-verify-post-write",
			Kind:         autonomy.ObligationVerify,
			Status:       status,
			Owner:        owner,
			RequiredBy:   "post_write_verification",
		})
	}
	obligations = append(obligations,
		autonomy.Obligation{
			ObligationID:           "obl-final-answer",
			Kind:                   autonomy.ObligationFinalAnswer,
			Status:                 autonomy.ObligationOpen,
			Owner:                  autonomy.OwnerHost,
			RequiredBy:             "completion_gate",
			FinalAnswerRequirement: util.FirstNonEmpty(strings.TrimSpace(brief.Contract), string(workerruntime.ContractDirectAnswer)),
		},
		autonomy.Obligation{
			ObligationID: "obl-loop-fuse",
			Kind:         autonomy.ObligationLoop,
			Status:       autonomy.ObligationOpen,
			Owner:        autonomy.OwnerHost,
			RequiredBy:   "repeated_call_and_no_progress_fuses",
		},
		autonomy.Obligation{
			ObligationID: "obl-budget",
			Kind:         autonomy.ObligationBudget,
			Status:       autonomy.ObligationOpen,
			Owner:        autonomy.OwnerHost,
			RequiredBy:   "run_policy",
			TargetRef:    runnerAutonomyBudgetTarget(policy),
		},
	)
	return obligations
}

func runnerAutonomyCompletionCriteria(brief workerruntime.WorkerRuntimeBrief) []string {
	criteria := append([]string(nil), brief.RequiredEvidence...)
	criteria = append(criteria, brief.ReadinessGate...)
	if runnerBriefRequiresMutation(brief) {
		criteria = append(criteria, "changed_paths_or_noop_reason", "verification_status_or_blocker")
	}
	if len(criteria) == 0 {
		criteria = []string{"answer_satisfies_runtime_contract"}
	}
	return criteria
}

func runnerAutonomyReadiness(brief workerruntime.WorkerRuntimeBrief, obligations []autonomy.Obligation) autonomy.ReadinessRequirement {
	if brief.Readiness == nil {
		return autonomy.ReadinessRequirement{}
	}
	return autonomy.ReadinessRequirement{
		ReadyToAnswer:       brief.Readiness.ReadyToAnswer,
		ReadyToWrite:        brief.Readiness.ReadyToWrite,
		ReadyToVerify:       brief.Readiness.ReadyToVerify,
		ClosedClaims:        append([]string(nil), brief.Readiness.ClosedClaims...),
		OpenObligationRefs:  runnerAutonomyReadinessOpenObligationRefs(brief.Readiness.OpenObligations, obligations),
		EvidenceRefs:        append([]string(nil), brief.Readiness.EvidenceSources...),
		FurtherReadsRequire: brief.Readiness.FurtherReadsRequire,
	}
}

func runnerAutonomyReadinessOpenObligationRefs(refs []string, obligations []autonomy.Obligation) []string {
	if len(refs) == 0 || len(obligations) == 0 {
		return nil
	}
	obligationsByID := make(map[string]autonomy.Obligation, len(obligations))
	for _, obligation := range obligations {
		obligationID := strings.TrimSpace(obligation.ObligationID)
		if obligationID != "" {
			obligationsByID[obligationID] = obligation
		}
	}
	seen := make(map[string]struct{})
	var out []string
	addID := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if obligation, ok := obligationsByID[id]; !ok || obligation.Status != autonomy.ObligationOpen {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	addKind := func(kind autonomy.ObligationKind) {
		for _, obligation := range obligations {
			if obligation.Kind == kind {
				addID(obligation.ObligationID)
			}
		}
	}
	for _, ref := range refs {
		trimmed := strings.TrimSpace(ref)
		normalized := strings.ToLower(trimmed)
		if trimmed == "" {
			continue
		}
		addID(trimmed)
		switch {
		case strings.Contains(normalized, "required_edit") || strings.Contains(normalized, "write"):
			addKind(autonomy.ObligationWrite)
		case strings.Contains(normalized, "verify") || strings.Contains(normalized, "verification"):
			addKind(autonomy.ObligationVerify)
		case strings.Contains(normalized, "evidence") || strings.Contains(normalized, "discovery"):
			addKind(autonomy.ObligationEvidence)
		case strings.Contains(normalized, "final") || strings.Contains(normalized, "answer") || strings.Contains(normalized, "completion"):
			addKind(autonomy.ObligationFinalAnswer)
		case strings.Contains(normalized, "budget"):
			addKind(autonomy.ObligationBudget)
		case strings.Contains(normalized, "loop"):
			addKind(autonomy.ObligationLoop)
		}
	}
	return out
}

func (a *runnerAutonomyRecorder) emitContractStarted(brief workerruntime.WorkerRuntimeBrief) {
	if a == nil {
		return
	}
	a.ensureObligationState()
	compiledEvent, compiledErr := autonomy.NewContractCompiledEvent(a.contract, a.nextTime(), a.nextSequence())
	if a.hasPlannerCompile && compiledEvent.Lifecycle != nil {
		compiledEvent.Lifecycle.InvariantID = a.activeAuthorityInvariantID()
		compiledEvent.Lifecycle.ProtocolHash = strings.TrimSpace(a.activeAuthority.ProtocolHash)
		compiledEvent.Lifecycle.StateDelta = append(compiledEvent.Lifecycle.StateDelta,
			"runtime_contract_id:"+strings.TrimSpace(a.activeAuthority.RuntimeContractID),
			"active_invariant:"+a.activeAuthorityInvariantID(),
		)
	}
	a.emitNewEvent(compiledEvent, compiledErr)
	for _, obligation := range a.obligations {
		opened := obligation
		opened.Status = autonomy.ObligationOpen
		opened.SourceEvent = ""
		opened.EvidenceRef = ""
		a.emitNewEvent(autonomy.NewObligationStateEvent(autonomy.EventObligationOpened, a.contract, opened, a.nextTime(), a.nextSequence()))
		switch obligation.Status {
		case autonomy.ObligationOpen:
			a.openObligations[obligation.ObligationID] = opened
		case autonomy.ObligationResolved:
			obligation.SourceEvent = util.FirstNonEmpty(strings.TrimSpace(obligation.SourceEvent), compiledEvent.EventID)
			obligation.EvidenceRef = util.FirstNonEmpty(strings.TrimSpace(obligation.EvidenceRef), compiledEvent.EventID)
			a.emitNewEvent(autonomy.NewObligationStateEvent(autonomy.EventObligationResolved, a.contract, obligation, a.nextTime(), a.nextSequence()))
			a.closedObligations[obligation.ObligationID] = struct{}{}
		case autonomy.ObligationWaived:
			obligation.SourceEvent = util.FirstNonEmpty(strings.TrimSpace(obligation.SourceEvent), compiledEvent.EventID)
			a.emitNewEvent(autonomy.NewObligationStateEvent(autonomy.EventObligationWaived, a.contract, obligation, a.nextTime(), a.nextSequence()))
			a.closedObligations[obligation.ObligationID] = struct{}{}
		}
	}
	if brief.Readiness != nil {
		a.emitNewEvent(autonomy.NewReadinessStateEvent(a.contract, autonomy.ContractReadinessEvent{
			ReadyToAnswer:       brief.Readiness.ReadyToAnswer,
			ReadyToWrite:        brief.Readiness.ReadyToWrite,
			ReadyToVerify:       brief.Readiness.ReadyToVerify,
			ClosedClaims:        append([]string(nil), brief.Readiness.ClosedClaims...),
			OpenObligations:     a.openObligationIDsForRefs(a.contract.Readiness.OpenObligationRefs),
			EvidenceRefs:        append([]string(nil), brief.Readiness.EvidenceSources...),
			FurtherReadsRequire: brief.Readiness.FurtherReadsRequire,
		}, a.nextTime(), a.nextSequence()))
	}
}

func (a *runnerAutonomyRecorder) emitTurnPhaseContract(contract TurnPhaseContract, protocol contractcompile.ModelProtocolProjection) {
	if a == nil {
		return
	}
	a.lastTurnPhase = contract
	binding := a.activeAuthorityForProtocol(protocol)
	protocol = protocolWithActiveAuthority(protocol, binding)
	a.lastModelProtocol = protocol
	a.lastModelPresented = false
	payload := contract.autonomyEventPayload()
	protocolPayload := modelProtocolEventPayload(protocol)
	if strings.TrimSpace(binding.ContractID) != "" {
		payload.ContractID = binding.ContractID
	}
	if strings.TrimSpace(protocolPayload.ProtocolHash) != "" {
		payload.ProtocolHash = protocolPayload.ProtocolHash
	}
	if strings.TrimSpace(payload.ProtocolHash) == "" {
		payload.ProtocolHash = runnerTurnPhaseProtocolHash(payload)
	}
	a.lastTurnPhaseEvent = payload
	lifecycle := autonomy.ContractLifecycleEvent{
		Reason:               "turn_phase_contract",
		StateDelta:           []string{payload.PhaseID, payload.DerivationReason},
		ProtocolHash:         payload.ProtocolHash,
		AllowedTools:         append([]string(nil), contract.AllowedTools...),
		HiddenTools:          append([]string(nil), contract.HiddenTools...),
		RequiredEvidence:     append([]string(nil), contract.EvidenceState.AcceptanceRefs...),
		ReadinessGate:        contract.Phase,
		VerificationContract: contract.EvidenceState.VerificationStatus,
		InvariantID:          binding.ActiveInvariantID,
		RemainingObligations: a.openObligationIDs(),
		TurnPhaseContract:    &payload,
		SurfaceSummaries: []autonomy.TurnPhaseSurfaceSummaryEvent{
			autonomy.SurfaceSummaryFromTurnPhaseContract(payload),
		},
	}
	if strings.TrimSpace(protocolPayload.ProtocolHash) != "" {
		lifecycle.ModelProtocol = &protocolPayload
	}
	a.emitNewEvent(autonomy.NewContractPresentedEvent(a.contract, lifecycle, a.nextTime(), a.nextSequence()))
	a.emitRollbackControlIfDisabled(payload.ProtocolHash)
	a.emitContextPacket(contract, protocol, payload.ProtocolHash)
}

func (a *runnerAutonomyRecorder) markModelProtocolPresented(protocol contractcompile.ModelProtocolProjection) {
	if a == nil || strings.TrimSpace(protocol.ProtocolHash) == "" {
		return
	}
	binding := a.activeAuthorityForProtocol(protocol)
	protocol = protocolWithActiveAuthority(protocol, binding)
	if !a.hasPlannerCompile {
		a.activeAuthority = binding
	}
	a.lastModelProtocol = protocol
	a.lastModelPresented = true
}

func runnerTurnPhaseProtocolHash(payload autonomy.TurnPhaseContractEvent) string {
	payload.ProtocolHash = ""
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return "sha256:" + digestRunnerAutonomyValue(string(data))
}

func (a *runnerAutonomyRecorder) emitTurnPhaseSurfaceSummary(surface autonomy.TurnPhaseSurfaceSummaryEvent, phase TurnPhaseContract) {
	if a == nil || strings.TrimSpace(surface.SurfaceID) == "" {
		return
	}
	if strings.TrimSpace(phase.ContractID) == "" {
		phase.ContractID = a.activeAuthorityContractID()
	}
	if strings.TrimSpace(phase.ModelCallID) == "" {
		phase.ModelCallID = a.lastTurnPhaseEvent.ModelCallID
	}
	if len(phase.RuntimeCall.ExpressibleActionClasses) == 0 {
		phase.RuntimeCall = a.lastTurnPhase.RuntimeCall
	}
	phasePayload := phase.autonomyEventPayload()
	if hash := a.currentModelProtocolHash(); hash != "" {
		phasePayload.ProtocolHash = hash
	} else if strings.TrimSpace(phasePayload.ProtocolHash) == "" {
		phasePayload.ProtocolHash = a.lastTurnPhaseEvent.ProtocolHash
	}
	if strings.TrimSpace(surface.ContractID) == "" {
		surface.ContractID = a.activeAuthorityContractID()
	}
	if strings.TrimSpace(surface.ModelCallID) == "" {
		surface.ModelCallID = phasePayload.ModelCallID
	}
	if hash := a.currentModelProtocolHash(); hash != "" {
		surface.ProtocolHash = hash
	} else if strings.TrimSpace(surface.ProtocolHash) == "" {
		surface.ProtocolHash = phasePayload.ProtocolHash
	}
	if strings.TrimSpace(surface.PhaseID) == "" {
		surface.PhaseID = phasePayload.PhaseID
	}
	lifecycle := autonomy.ContractLifecycleEvent{
		Reason:            "turn_phase_surface_summary",
		StateDelta:        []string{surface.SurfaceID, surface.PhaseID},
		ProtocolHash:      surface.ProtocolHash,
		InvariantID:       a.activeAuthorityInvariantID(),
		TurnPhaseContract: &phasePayload,
		SurfaceSummaries: []autonomy.TurnPhaseSurfaceSummaryEvent{
			surface,
		},
	}
	a.emitNewEvent(autonomy.NewContractPresentedEvent(a.contract, lifecycle, a.nextTime(), a.nextSequence()))
}

func (a *runnerAutonomyRecorder) emitGateDecision(input runnerAutonomyGateDecision) {
	if a == nil {
		return
	}
	invariantID := strings.TrimSpace(input.InvariantID)
	if invariantID == "" && a.hasPlannerCompile {
		invariantID = a.activeAuthorityInvariantID()
	}
	gate := autonomy.ContractGateEvent{
		Gate:                  input.Gate,
		Decision:              input.Decision,
		InvariantID:           invariantID,
		ProtocolHash:          a.currentModelProtocolHash(),
		ReasonCode:            input.ReasonCode,
		ToolCallID:            input.ToolCallID,
		ToolName:              input.ToolName,
		TargetRefs:            input.TargetRefs,
		ChangedPaths:          input.ChangedPaths,
		VerificationStatus:    input.VerificationStatus,
		Budget:                input.Budget,
		LoopFingerprintDigest: input.LoopFingerprintDigest,
		Attempts:              input.Attempts,
		Source:                input.Source,
	}
	gate.ObligationRefs = a.obligationIDsForGate(input.Gate)
	if runnerAutonomyGateClosesObligations(input.Gate, input.Decision) {
		gate.ResolvedObligations, gate.OpenObligations = a.resolvedAndOpenObligationsForGate(input, gate.ObligationRefs)
	} else {
		gate.OpenObligations = a.openObligationIDsForRefs(gate.ObligationRefs)
	}
	gateFields := runnerAutonomyGateFields(input, gate, a)
	gate.PackID = gateFields.PackID
	gate.EnforcementMode = gateFields.EnforcementMode
	gate.ObservedFacts = gateFields.ObservedFacts
	gate.SafeAlternatives = gateFields.SafeAlternatives
	gateEvent, err := autonomy.NewGateDecisionEvent(a.contract, gate, a.nextTime(), a.nextSequence())
	a.emitNewEvent(gateEvent, err)
	if err == nil {
		a.emitResolvedObligations(gate.ResolvedObligations, gateEvent.EventID)
		a.emitGateFriction(input, gate, gateEvent.EventID)
		a.emitFailureDeltaIntentFromGate(input, gate, gateEvent.EventID)
		a.emitModelResponseObservationFromGate(input, gate, gateEvent.EventID)
	}
	if input.Gate == autonomy.GateCompletion {
		a.emitNewEvent(autonomy.NewCompletionGateDecisionEvent(a.contract, autonomy.ContractCompletionEvent{
			Decision:             input.Decision,
			InvariantID:          input.InvariantID,
			ProtocolHash:         gate.ProtocolHash,
			RemainingObligations: gate.OpenObligations,
			VerificationStatus:   input.VerificationStatus,
			ChangedPaths:         input.ChangedPaths,
			RequiredPathStatus:   input.TargetRefs,
		}, a.nextTime(), a.nextSequence()))
	}
}

func runnerAutonomyGateFields(input runnerAutonomyGateDecision, gate autonomy.ContractGateEvent, recorder *runnerAutonomyRecorder) runnerAutonomyGateEventFields {
	return runnerAutonomyGateEventFields{
		PackID:           runnerAutonomyGatePackID(input),
		EnforcementMode:  runnerAutonomyGateEnforcementMode(input.Decision),
		ObservedFacts:    runnerAutonomyGateObservedFacts(input, gate),
		SafeAlternatives: runnerAutonomyGateSafeAlternatives(input, recorder),
	}
}

func runnerAutonomyGatePackID(input runnerAutonomyGateDecision) autonomy.ContractPackID {
	switch strings.TrimSpace(input.InvariantID) {
	case "completion.readiness":
		return autonomy.ContractPackCompletionReadiness
	case "write.required_edit_paths":
		return autonomy.ContractPackWriteRequiredPaths
	case "verification.post_write":
		return autonomy.ContractPackVerificationPostWrite
	case "loop.repeated_call":
		return autonomy.ContractPackLoopRepeatedCall
	case "budget.run_policy":
		return autonomy.ContractPackBudgetRunPolicy
	case "read_only.readiness":
		return autonomy.ContractPackCompletionReadiness
	}
	switch input.Gate {
	case autonomy.GateCompletion:
		return autonomy.ContractPackCompletionReadiness
	case autonomy.GateWrite:
		return autonomy.ContractPackWriteRequiredPaths
	case autonomy.GateVerification:
		return autonomy.ContractPackVerificationPostWrite
	case autonomy.GateLoop:
		return autonomy.ContractPackLoopRepeatedCall
	case autonomy.GateBudget:
		return autonomy.ContractPackBudgetRunPolicy
	case autonomy.GateReadOnly:
		return autonomy.ContractPackCompletionReadiness
	default:
		return ""
	}
}

func runnerAutonomyGateEnforcementMode(decision autonomy.ToolPolicyDecision) autonomy.EnforcementMode {
	if decision == autonomy.PolicyObserve {
		return autonomy.EnforcementModeObserveOnly
	}
	return autonomy.EnforcementModeEnforce
}

func runnerAutonomyGateObservedFacts(input runnerAutonomyGateDecision, gate autonomy.ContractGateEvent) []autonomy.ContractObservedFact {
	var facts []autonomy.ContractObservedFact
	addGateFactValue(&facts, autonomy.ObservedFactStatus, "gate", string(input.Gate))
	addGateFactValue(&facts, autonomy.ObservedFactStatus, "decision", string(input.Decision))
	addGateFactCount(&facts, "target_ref_count", len(gate.TargetRefs))
	addGateFactCount(&facts, "obligation_ref_count", len(gate.ObligationRefs))
	addGateFactCount(&facts, "open_obligation_count", len(gate.OpenObligations))
	addGateFactCount(&facts, "resolved_obligation_count", len(gate.ResolvedObligations))
	addGateFactCount(&facts, "changed_path_count", len(gate.ChangedPaths))
	addGateFactValue(&facts, autonomy.ObservedFactReasonCode, "reason_code", runnerAutonomyReasonCode(input.ReasonCode))
	addGateFactValue(&facts, autonomy.ObservedFactReasonCode, "source", runnerAutonomyReasonCode(input.Source))
	addGateFactValue(&facts, autonomy.ObservedFactToolName, "tool", input.ToolName)
	addGateFactValue(&facts, autonomy.ObservedFactStatus, "tool_call_id", input.ToolCallID)
	addGateFactValue(&facts, autonomy.ObservedFactStatus, "verification_status", input.VerificationStatus)
	addGateFactValue(&facts, autonomy.ObservedFactProtocolHash, "protocol_hash", gate.ProtocolHash)
	addGateFactValue(&facts, autonomy.ObservedFactStatus, "loop_fingerprint_digest", gate.LoopFingerprintDigest)
	addGateFactValues(&facts, autonomy.ObservedFactPathRef, "target_refs", gate.TargetRefs, 8)
	addGateFactValues(&facts, autonomy.ObservedFactObligationRef, "obligation_refs", gate.ObligationRefs, 8)
	addGateFactValues(&facts, autonomy.ObservedFactObligationRef, "open_obligations", gate.OpenObligations, 8)
	addGateFactValues(&facts, autonomy.ObservedFactObligationRef, "resolved_obligations", gate.ResolvedObligations, 8)
	if input.Attempts > 0 {
		addGateFactCount(&facts, "attempts", input.Attempts)
	}
	if gate.Budget != nil {
		addGateFactValue(&facts, autonomy.ObservedFactStatus, "budget_exhausted", runnerAutonomyBoolStatus(gate.Budget.Exhausted))
		addGateFactCount(&facts, "budget_used_tool_steps", gate.Budget.UsedToolSteps)
		addGateFactCount(&facts, "budget_max_tool_steps", gate.Budget.MaxToolSteps)
		addGateFactCount(&facts, "budget_used_model_calls", gate.Budget.UsedModelCalls)
		addGateFactCount(&facts, "budget_max_model_calls", gate.Budget.MaxModelCalls)
		addGateFactValue(&facts, autonomy.ObservedFactReasonCode, "budget_exhaustion_reason", runnerAutonomyReasonCode(gate.Budget.ExhaustionReason))
	}
	return facts
}

func addGateFactValue(facts *[]autonomy.ContractObservedFact, kind autonomy.ObservedFactKind, name string, value string) {
	name = strings.TrimSpace(name)
	value = strings.TrimSpace(value)
	if name == "" || value == "" {
		return
	}
	if len(value) > 160 {
		value = value[:160]
	}
	*facts = append(*facts, autonomy.ContractObservedFact(string(kind)+":"+name+"="+value))
}

func addGateFactCount(facts *[]autonomy.ContractObservedFact, name string, count int) {
	name = strings.TrimSpace(name)
	if count < 0 {
		return
	}
	*facts = append(*facts, autonomy.ContractObservedFact(string(autonomy.ObservedFactCount)+":"+name+"="+runnerAutonomyIntString(count)))
}

func addGateFactValues(facts *[]autonomy.ContractObservedFact, kind autonomy.ObservedFactKind, name string, values []string, limit int) {
	values = runnerAutonomyBoundedFactStrings(values, limit)
	for _, value := range values {
		addGateFactValue(facts, kind, name, value)
	}
}

func runnerAutonomyBoolStatus(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func runnerAutonomyIntString(value int) string {
	return strconv.Itoa(value)
}

func runnerAutonomyBoundedFactStrings(values []string, limit int) []string {
	if limit <= 0 {
		limit = 8
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, min(len(values), limit))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if len(value) > 160 {
			value = value[:160]
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func runnerAutonomyGateSafeAlternatives(input runnerAutonomyGateDecision, recorder *runnerAutonomyRecorder) []string {
	if !runnerAutonomyGateNeedsSafeAlternatives(input.Decision) {
		return nil
	}
	var alternatives []string
	if recorder != nil {
		protocolVisible := recorder.currentModelProtocolHash() != ""
		alternatives = runnerAutonomyFrictionSafeAlternatives(recorder.visibleModelProtocol(), recorder.lastTurnPhase, protocolVisible)
	}
	if len(alternatives) == 0 {
		alternatives = runnerAutonomyDefaultGateSafeAlternatives(input.Gate)
	}
	return runnerAutonomyBoundedFactStrings(alternatives, 8)
}

func runnerAutonomyGateNeedsSafeAlternatives(decision autonomy.ToolPolicyDecision) bool {
	switch decision {
	case autonomy.PolicyAsk, autonomy.PolicyDeny, autonomy.PolicyNudge, autonomy.PolicyReject, autonomy.PolicyStop:
		return true
	default:
		return false
	}
}

func runnerAutonomyDefaultGateSafeAlternatives(gate autonomy.GateKind) []string {
	switch gate {
	case autonomy.GateCompletion:
		return []string{"Decision: source-satisfied", "<<QUESTION>>:"}
	case autonomy.GateWrite:
		return []string{"edit required_edit_paths", "<<QUESTION>>:"}
	case autonomy.GateVerification:
		return []string{"run verification", "report verification blocker"}
	case autonomy.GateLoop:
		return []string{"change tool target", "Decision: source-satisfied"}
	case autonomy.GateBudget:
		return []string{"Decision: blocker", "Decision: source-satisfied"}
	case autonomy.GateReadOnly:
		return []string{"read evidence paths", "Decision: source-satisfied"}
	default:
		return []string{"choose allowed action", "<<QUESTION>>:"}
	}
}

func (a *runnerAutonomyRecorder) resolvedAndOpenObligationsForGate(input runnerAutonomyGateDecision, obligationRefs []string) (resolved []string, open []string) {
	if a == nil {
		return nil, nil
	}
	switch input.Gate {
	case autonomy.GateWrite:
		return a.writeGateResolvedAndOpenObligations(input.ChangedPaths)
	case autonomy.GateCompletion:
		return a.completionGateResolvedAndOpenObligations(input, obligationRefs)
	case autonomy.GateVerification:
		if strings.EqualFold(strings.TrimSpace(input.VerificationStatus), "passed") ||
			strings.EqualFold(strings.TrimSpace(input.VerificationStatus), "observed") {
			return a.openObligationIDsForRefs(obligationRefs), nil
		}
		return nil, a.openObligationIDsForRefs(obligationRefs)
	default:
		return a.openObligationIDsForRefs(obligationRefs), nil
	}
}

func (a *runnerAutonomyRecorder) completionGateResolvedAndOpenObligations(input runnerAutonomyGateDecision, refs []string) (resolved []string, open []string) {
	a.ensureObligationState()
	changed := make(map[string]struct{}, len(input.ChangedPaths))
	for _, path := range input.ChangedPaths {
		if normalized := normalizeRunnerPath(path); normalized != "" {
			changed[normalized] = struct{}{}
		}
	}
	sourceSatisfiedPaths := map[string]struct{}{}
	sourceSatisfiedReason := strings.TrimSpace(input.ReasonCode) == "plan_source_satisfied_short_circuit" ||
		strings.Contains(strings.ToLower(strings.TrimSpace(input.ReasonCode)), "source_satisfied")
	if sourceSatisfiedReason {
		for _, path := range input.TargetRefs {
			path = strings.TrimSpace(path)
			if idx := strings.LastIndex(path, ":"); idx > 0 {
				path = path[:idx]
			}
			if normalized := normalizeRunnerPath(path); normalized != "" {
				sourceSatisfiedPaths[normalized] = struct{}{}
			}
		}
	}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		obligation, ok := a.openObligations[ref]
		if !ok {
			continue
		}
		if obligation.Kind == autonomy.ObligationWrite {
			target := normalizeRunnerPath(obligation.TargetRef)
			if target == "" {
				if sourceSatisfiedReason {
					resolved = append(resolved, ref)
					continue
				}
				open = append(open, ref)
				continue
			}
			if _, ok := changed[target]; ok {
				resolved = append(resolved, ref)
				continue
			}
			if _, ok := sourceSatisfiedPaths[target]; ok {
				resolved = append(resolved, ref)
				continue
			}
			if len(changed) == 0 {
				open = append(open, ref)
				continue
			}
			open = append(open, ref)
			continue
		}
		if ref == "obl-verify-post-write" && !strings.EqualFold(strings.TrimSpace(input.VerificationStatus), "passed") {
			open = append(open, ref)
			continue
		}
		resolved = append(resolved, ref)
	}
	return resolved, open
}

func (a *runnerAutonomyRecorder) openObligationIDsForRefs(refs []string) []string {
	if a == nil || len(refs) == 0 {
		return nil
	}
	a.ensureObligationState()
	seen := make(map[string]struct{}, len(refs))
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if _, ok := a.openObligations[ref]; !ok {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	return out
}

func (a *runnerAutonomyRecorder) openObligationIDs() []string {
	if a == nil {
		return nil
	}
	a.ensureObligationState()
	ids := make([]string, 0, len(a.openObligations))
	for id := range a.openObligations {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			ids = append(ids, trimmed)
		}
	}
	slices.Sort(ids)
	return ids
}

func (a *runnerAutonomyRecorder) writeGateResolvedAndOpenObligations(changedPaths []string) (resolved []string, open []string) {
	if a == nil {
		return nil, nil
	}
	a.ensureObligationState()
	obligations := a.obligationsForGate(autonomy.GateWrite)
	hasRequiredPathTargets := false
	for _, obligation := range obligations {
		if strings.TrimSpace(obligation.TargetRef) != "" {
			hasRequiredPathTargets = true
			break
		}
	}
	if !hasRequiredPathTargets {
		for _, obligation := range obligations {
			if _, ok := a.openObligations[obligation.ObligationID]; ok {
				resolved = append(resolved, obligation.ObligationID)
			}
		}
		return resolved, nil
	}
	changed := a.runnerAutonomyChangedPathSet(changedPaths)
	for _, obligation := range obligations {
		if _, ok := a.openObligations[obligation.ObligationID]; !ok {
			continue
		}
		if a.runnerAutonomyPathMatchesChangedSet(obligation.TargetRef, changed) {
			resolved = append(resolved, obligation.ObligationID)
			continue
		}
		open = append(open, obligation.ObligationID)
	}
	return resolved, open
}

func (a *runnerAutonomyRecorder) runnerAutonomyChangedPathSet(paths []string) map[string]struct{} {
	out := make(map[string]struct{})
	root := a.runnerAutonomyWorkspaceRoot()
	for _, path := range paths {
		for _, variant := range runnerAutonomyPathVariants(path, root) {
			out[variant] = struct{}{}
		}
	}
	return out
}

func (a *runnerAutonomyRecorder) runnerAutonomyPathMatchesChangedSet(path string, changed map[string]struct{}) bool {
	if len(changed) == 0 {
		return false
	}
	for _, variant := range runnerAutonomyPathVariants(path, a.runnerAutonomyWorkspaceRoot()) {
		if _, ok := changed[variant]; ok {
			return true
		}
	}
	return false
}

func (a *runnerAutonomyRecorder) runnerAutonomyWorkspaceRoot() string {
	if a == nil || a.runner == nil || a.runner.Workspace == nil {
		return ""
	}
	return strings.TrimSpace(a.runner.Workspace.Root())
}

func runnerAutonomyPathVariants(path string, root string) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	seen := make(map[string]struct{})
	var variants []string
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		value = filepath.ToSlash(filepath.Clean(value))
		value = normalizeRunnerPath(value)
		if value == "" || value == "." {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		variants = append(variants, value)
	}
	add(path)
	canonicalPath := ""
	if filepath.IsAbs(path) {
		if canonical, ok := runnerAutonomyCanonicalPath(path); ok {
			canonicalPath = canonical
			add(canonicalPath)
		}
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return variants
	}
	if filepath.IsAbs(path) {
		if rel, err := filepath.Rel(root, path); err == nil {
			rel = filepath.ToSlash(rel)
			if rel != "." && rel != ".." && !strings.HasPrefix(rel, "../") {
				add(rel)
			}
		}
		if canonicalPath != "" {
			if rel, err := filepath.Rel(root, canonicalPath); err == nil {
				rel = filepath.ToSlash(rel)
				if rel != "." && rel != ".." && !strings.HasPrefix(rel, "../") {
					add(rel)
				}
			}
		}
		return variants
	}
	add(filepath.Join(root, path))
	return variants
}

func runnerAutonomyCanonicalPath(path string) (string, bool) {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved, true
	}
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	if resolvedDir, err := filepath.EvalSymlinks(dir); err == nil {
		return filepath.Join(resolvedDir, base), true
	}
	return "", false
}

func runnerAutonomyGateClosesObligations(gate autonomy.GateKind, decision autonomy.ToolPolicyDecision) bool {
	switch decision {
	case autonomy.PolicyAllow, autonomy.PolicyObserve:
		return true
	case autonomy.PolicyStop:
		return gate == autonomy.GateBudget || gate == autonomy.GateLoop
	default:
		return false
	}
}

func (a *runnerAutonomyRecorder) emitResolvedObligations(ids []string, sourceEvent string) {
	if a == nil {
		return
	}
	a.ensureObligationState()
	for _, id := range ids {
		obligation, ok := a.openObligations[strings.TrimSpace(id)]
		if !ok {
			continue
		}
		obligation.Status = autonomy.ObligationResolved
		obligation.SourceEvent = sourceEvent
		obligation.EvidenceRef = sourceEvent
		a.emitNewEvent(autonomy.NewObligationStateEvent(autonomy.EventObligationResolved, a.contract, obligation, a.nextTime(), a.nextSequence()))
		delete(a.openObligations, obligation.ObligationID)
		a.closedObligations[obligation.ObligationID] = struct{}{}
	}
}

func (a *runnerAutonomyRecorder) ensureObligationState() {
	if a.openObligations == nil {
		a.openObligations = make(map[string]autonomy.Obligation)
	}
	if a.closedObligations == nil {
		a.closedObligations = make(map[string]struct{})
	}
}

func (a *runnerAutonomyRecorder) emitWriteGate(req workspace.ToolRequest, decision autonomy.ToolPolicyDecision, reasonCode string, changedPaths []string) {
	if a == nil {
		return
	}
	a.emitGateDecision(runnerAutonomyGateDecision{
		Gate:         autonomy.GateWrite,
		Decision:     decision,
		InvariantID:  "write.required_edit_paths",
		ReasonCode:   reasonCode,
		ToolName:     strings.TrimSpace(req.Tool),
		TargetRefs:   writeRequestPaths(req),
		ChangedPaths: changedPaths,
		Source:       "runner_write_gate",
	})
}

func (a *runnerAutonomyRecorder) emitIntentClassified(req workspace.ToolRequest) {
	a.emitPolicyEvent(req, runnerAutonomyPolicyEventInput{
		EventType: autonomy.EventToolIntentClassified,
		Source:    "runner_tool_intent",
	})
}

func (a *runnerAutonomyRecorder) emitPathRolesResolved(req workspace.ToolRequest) {
	if a == nil || len(runnerAutonomyPolicyPaths(req)) == 0 {
		return
	}
	a.emitPolicyEvent(req, runnerAutonomyPolicyEventInput{
		EventType: autonomy.EventPathRolesResolved,
		Source:    "runner_path_roles",
	})
}

func (a *runnerAutonomyRecorder) emitPolicyResult(req workspace.ToolRequest, input runnerAutonomyPolicyEventInput) {
	if input.EventType == "" {
		input.EventType = autonomy.EventToolPolicyResult
	}
	a.emitPolicyEvent(req, input)
}

func (a *runnerAutonomyRecorder) emitVerificationGate(req workspace.ToolRequest, decision autonomy.ToolPolicyDecision, reasonCode string, status string) {
	if a == nil {
		return
	}
	a.emitGateDecision(runnerAutonomyGateDecision{
		Gate:               autonomy.GateVerification,
		Decision:           decision,
		InvariantID:        "verification.post_write",
		ReasonCode:         reasonCode,
		ToolName:           strings.TrimSpace(req.Tool),
		VerificationStatus: status,
		Source:             "runner_verification_gate",
	})
}

func (a *runnerAutonomyRecorder) emitCompletionGate(decision autonomy.ToolPolicyDecision, reasonCode string, results []workspace.ToolResult) {
	a.emitCompletionGateAttempt(decision, reasonCode, results, 0)
}

func (a *runnerAutonomyRecorder) emitCompletionGateAttempt(decision autonomy.ToolPolicyDecision, reasonCode string, results []workspace.ToolResult, attempts int) {
	if a == nil {
		return
	}
	changed := mapKeys(changedPathsFromToolResults(results))
	a.emitGateDecision(runnerAutonomyGateDecision{
		Gate:               autonomy.GateCompletion,
		Decision:           decision,
		InvariantID:        "completion.readiness",
		ReasonCode:         reasonCode,
		ChangedPaths:       changed,
		VerificationStatus: runnerAutonomyVerificationStatus(results, a.brief),
		TargetRefs:         runnerAutonomyRequiredPathStatus(a.contract, results),
		Attempts:           attempts,
		Source:             "runner_completion_gate",
	})
}

func (a *runnerAutonomyRecorder) emitGateFriction(input runnerAutonomyGateDecision, gate autonomy.ContractGateEvent, sourceEventID string) {
	if a == nil {
		return
	}
	decision, ok := runnerAutonomyGateFrictionDecision(input)
	if !ok {
		return
	}
	phase := a.lastTurnPhase
	protocol := a.visibleModelProtocol()
	protocolHash := a.currentModelProtocolHash()
	friction := autonomy.ContractGuardFrictionEvent{
		Step:                 phase.Step,
		Lane:                 a.contract.Scope.Lane,
		Phase:                phase.Phase,
		PhaseID:              phase.PhaseID,
		ModelCallID:          phase.ModelCallID,
		SourceEventID:        sourceEventID,
		ContractVersion:      a.contract.SchemaVersion,
		ProtocolHash:         protocolHash,
		InvariantID:          input.InvariantID,
		ObligationID:         firstRunnerAutonomyString(gate.OpenObligations),
		Subject:              runnerAutonomyFrictionSubject(input),
		Decision:             decision,
		GuardSurface:         runnerAutonomyFrictionSurface(input.Gate),
		FirstInvalidAction:   runnerAutonomyFrictionInvalidAction(input),
		VisibleActionClasses: runnerAutonomyFrictionVisibleActionClasses(protocol, phase, protocolHash != ""),
		VisibleTools:         runnerAutonomyFrictionVisibleTools(protocol, phase, protocolHash != ""),
		SafeAlternatives:     runnerAutonomyFrictionSafeAlternatives(protocol, phase, protocolHash != ""),
		ModelContractVisible: protocolHash != "",
		RetryNumber:          input.Attempts,
		RetryOutcome:         runnerAutonomyFrictionRetryOutcome(input),
		Avoidability:         autonomy.GuardFrictionAvoidabilityUnknown,
		EvidenceRefs:         runnerAutonomyFrictionEvidenceRefs(input),
	}
	a.emitNewEvent(autonomy.NewGuardFrictionEvent(a.contract, friction, a.nextTime(), a.nextSequence()))
}

func (a *runnerAutonomyRecorder) currentModelProtocolHash() string {
	if a == nil || !a.lastModelPresented {
		return ""
	}
	return strings.TrimSpace(a.lastModelProtocol.ProtocolHash)
}

func (a *runnerAutonomyRecorder) visibleModelProtocol() contractcompile.ModelProtocolProjection {
	if a == nil || !a.lastModelPresented {
		return contractcompile.ModelProtocolProjection{}
	}
	return a.lastModelProtocol
}

func runnerAutonomyFrictionDecision(decision autonomy.ToolPolicyDecision) (autonomy.GuardFrictionDecision, bool) {
	switch decision {
	case autonomy.PolicyReject, autonomy.PolicyDeny:
		return autonomy.GuardFrictionDecisionReject, true
	case autonomy.PolicyNudge, autonomy.PolicyAsk:
		return autonomy.GuardFrictionDecisionNudge, true
	case autonomy.PolicyStop:
		return autonomy.GuardFrictionDecisionStop, true
	default:
		return "", false
	}
}

func runnerAutonomyGateFrictionDecision(input runnerAutonomyGateDecision) (autonomy.GuardFrictionDecision, bool) {
	if input.Gate == autonomy.GateCompletion && input.Decision == autonomy.PolicyAllow && input.Attempts > 0 {
		return autonomy.GuardFrictionDecisionRecover, true
	}
	return runnerAutonomyFrictionDecision(input.Decision)
}

func runnerAutonomyFrictionSurface(gate autonomy.GateKind) autonomy.GuardFrictionSurface {
	switch gate {
	case autonomy.GateCompletion:
		return autonomy.GuardFrictionSurfaceCompletionGate
	case autonomy.GateWrite:
		return autonomy.GuardFrictionSurfaceWriteGate
	case autonomy.GateVerification:
		return autonomy.GuardFrictionSurfaceVerification
	case autonomy.GateLoop:
		return autonomy.GuardFrictionSurfaceRunnerLoop
	case autonomy.GateBudget:
		return autonomy.GuardFrictionSurfaceBudget
	case autonomy.GateReadOnly:
		return autonomy.GuardFrictionSurfaceReadiness
	default:
		return autonomy.GuardFrictionSurfaceToolPolicy
	}
}

func runnerAutonomyFrictionSubject(input runnerAutonomyGateDecision) autonomy.GuardFrictionSubject {
	if input.Gate == autonomy.GateCompletion {
		return autonomy.GuardFrictionSubjectFinalAnswer
	}
	return autonomy.GuardFrictionSubjectToolCall
}

func runnerAutonomyFrictionInvalidAction(input runnerAutonomyGateDecision) string {
	if tool := strings.TrimSpace(input.ToolName); tool != "" {
		return tool
	}
	if input.Gate == autonomy.GateCompletion {
		return actionFinalAnswer
	}
	return strings.TrimSpace(string(input.Gate))
}

func runnerAutonomyFrictionRetryOutcome(input runnerAutonomyGateDecision) autonomy.GuardFrictionRetryOutcome {
	if input.Gate == autonomy.GateCompletion && input.Decision == autonomy.PolicyAllow && input.Attempts > 0 {
		return autonomy.GuardFrictionRetrySuccess
	}
	if input.Attempts > 0 && (input.Decision == autonomy.PolicyReject || input.Decision == autonomy.PolicyStop) {
		return autonomy.GuardFrictionRetrySameFailure
	}
	return autonomy.GuardFrictionRetryNotApplicable
}

func runnerAutonomyFrictionEvidenceRefs(input runnerAutonomyGateDecision) []string {
	refs := []string{"reason:" + runnerAutonomyReasonCode(input.ReasonCode)}
	if input.Source != "" {
		refs = append(refs, "source:"+runnerAutonomyReasonCode(input.Source))
	}
	return refs
}

func runnerAutonomyFrictionVisibleActionClasses(protocol contractcompile.ModelProtocolProjection, phase TurnPhaseContract, modelProtocolVisible bool) []string {
	if modelProtocolVisible {
		return append([]string(nil), protocol.CallableActionClasses...)
	}
	return append([]string(nil), phase.ActionPolicy.AllowedActionClasses...)
}

func runnerAutonomyFrictionVisibleTools(protocol contractcompile.ModelProtocolProjection, phase TurnPhaseContract, modelProtocolVisible bool) []string {
	if modelProtocolVisible {
		return append([]string(nil), protocol.VisibleTools...)
	}
	return append([]string(nil), phase.AllowedTools...)
}

func runnerAutonomyFrictionSafeAlternatives(protocol contractcompile.ModelProtocolProjection, phase TurnPhaseContract, modelProtocolVisible bool) []string {
	if modelProtocolVisible {
		return append([]string(nil), protocol.SafeAlternatives...)
	}
	if len(phase.TerminalOutcomes) > 0 {
		return append([]string(nil), phase.TerminalOutcomes...)
	}
	return append([]string(nil), protocol.SafeAlternatives...)
}

func firstRunnerAutonomyString(values []string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (a *runnerAutonomyRecorder) emitLoopGate(req workspace.ToolRequest, decision autonomy.ToolPolicyDecision, reasonCode string, fingerprint string, attempts int) {
	if a == nil {
		return
	}
	a.emitGateDecision(runnerAutonomyGateDecision{
		Gate:                  autonomy.GateLoop,
		Decision:              decision,
		InvariantID:           "loop.repeated_call",
		ReasonCode:            reasonCode,
		ToolName:              strings.TrimSpace(req.Tool),
		LoopFingerprintDigest: digestRunnerAutonomyValue(fingerprint),
		Attempts:              attempts,
		Source:                "runner_loop_gate",
	})
}

func (a *runnerAutonomyRecorder) emitLoopCloseout(stopReason string) {
	if a == nil || !a.hasOpenObligationsForGate(autonomy.GateLoop) {
		return
	}
	reason := strings.TrimSpace(stopReason)
	if reason == "" || reason == "completed" {
		reason = "completed_without_loop_fuse"
	}
	a.emitGateDecision(runnerAutonomyGateDecision{
		Gate:        autonomy.GateLoop,
		Decision:    autonomy.PolicyObserve,
		InvariantID: "loop.repeated_call",
		ReasonCode:  reason,
		Source:      "runner_loop_closeout",
	})
}

func (a *runnerAutonomyRecorder) emitBudgetGate(policy RunPolicy, metrics runnerMetrics, stopReason string, exhausted bool) {
	if a == nil {
		return
	}
	decision := autonomy.PolicyObserve
	if exhausted {
		decision = autonomy.PolicyStop
	}
	reason := strings.TrimSpace(stopReason)
	if reason == "" {
		reason = "completed"
	}
	a.emitGateDecision(runnerAutonomyGateDecision{
		Gate:        autonomy.GateBudget,
		Decision:    decision,
		InvariantID: "budget.run_policy",
		ReasonCode:  reason,
		Budget: &autonomy.BudgetState{
			MaxToolSteps:       policy.MaxToolSteps,
			UsedToolSteps:      metrics.ToolCalls,
			MaxModelCalls:      policy.MaxModelCalls,
			UsedModelCalls:     metrics.ModelCalls,
			MaxContextMessages: policy.MaxContextMessages,
			MaxContextChars:    policy.MaxContextChars,
			Exhausted:          exhausted,
			ExhaustionReason:   reason,
		},
		Source: "runner_budget_gate",
	})
}

func (a *runnerAutonomyRecorder) hasOpenObligationsForGate(gate autonomy.GateKind) bool {
	if a == nil {
		return false
	}
	for _, obligation := range a.obligationsForGate(gate) {
		if _, ok := a.openObligations[obligation.ObligationID]; ok {
			return true
		}
	}
	return false
}

func (a *runnerAutonomyRecorder) emitContextPacket(contract TurnPhaseContract, protocol contractcompile.ModelProtocolProjection, protocolHash string) {
	if a == nil {
		return
	}
	protocolHash = util.FirstNonEmpty(strings.TrimSpace(protocol.ProtocolHash), strings.TrimSpace(protocolHash))
	packet := protocol.CompactPacket
	if packet.SchemaVersion == "" {
		packet.SchemaVersion = autonomy.ContextPacketSchemaVersion
	}
	packet.ContractID = a.contract.ContractID
	packet.ProtocolHash = protocolHash
	if packet.PacketID == "" {
		packet.PacketID = "packet:" + digestRunnerAutonomyValue(strings.Join([]string{
			packet.ContractID,
			packet.ProtocolHash,
			strings.TrimSpace(contract.PhaseID),
			strings.TrimSpace(contract.ModelCallID),
		}, ":"))
	}
	if len(packet.SchemaNames) == 0 {
		packet.SchemaNames = compileModelExchangeSchemaNames()
	}
	if len(packet.RedactionMarkers) == 0 {
		packet.RedactionMarkers = []string{"metadata_only", "prompt_omitted", "transcript_omitted", "file_content_omitted", "secrets_omitted"}
	}
	a.emitModelExchangeEvent(autonomy.ContractModelExchangeEvent{
		Kind:          "context_packet",
		ContractID:    a.contract.ContractID,
		ProtocolHash:  protocolHash,
		PhaseID:       util.FirstNonEmpty(strings.TrimSpace(contract.PhaseID), strings.TrimSpace(protocol.PhaseID)),
		ModelCallID:   strings.TrimSpace(contract.ModelCallID),
		InvariantID:   firstRunnerAutonomyString(contract.InvariantIDs),
		ContextPacket: &packet,
	})
	a.emitModelExchangeEvent(autonomy.ContractModelExchangeEvent{
		Kind:         "trace_store_policy",
		ContractID:   a.contract.ContractID,
		ProtocolHash: protocolHash,
		PhaseID:      util.FirstNonEmpty(strings.TrimSpace(contract.PhaseID), strings.TrimSpace(protocol.PhaseID)),
		ModelCallID:  strings.TrimSpace(contract.ModelCallID),
		TraceStorePolicy: &autonomy.TraceStorePolicy{
			ContractID:      a.contract.ContractID,
			ProtocolHash:    protocolHash,
			RetentionRef:    "retention:local_default_30d_raw_180d_redacted",
			RedactionRef:    "redaction:metadata_only",
			RedactionStatus: "pass",
			AllowedFields:   []string{"schema_version", "event_id", "type", "contract_id", "protocol_hash", "phase_id", "model_call_id", "invariant_id", "source_event_id", "source_event_type", "refs", "counts", "redaction_markers"},
			ForbiddenFields: []string{"raw_prompt", "raw_transcript", "prompt_body", "transcript_body", "file_body", "old_text", "new_text", "long_excerpt", "customer_id", "customer_identifier"},
			StoreRefs:       []string{"role:autonomy_event", "event_type:model_exchange"},
			CapturedBy:      "harness",
		},
	})
}

func (a *runnerAutonomyRecorder) emitPermissionSnapshot(req workspace.ToolRequest, policy autonomy.ContractPolicyEvent, sourceEventID string) {
	if a == nil {
		return
	}
	protocolHash := a.activeProtocolHash()
	if strings.TrimSpace(protocolHash) == "" {
		return
	}
	_ = req
	a.emitModelExchangeEvent(autonomy.ContractModelExchangeEvent{
		Kind:            "permission_snapshot",
		ContractID:      a.contract.ContractID,
		ProtocolHash:    protocolHash,
		PhaseID:         a.lastTurnPhaseEvent.PhaseID,
		ModelCallID:     a.lastTurnPhaseEvent.ModelCallID,
		InvariantID:     strings.TrimSpace(policy.InvariantID),
		SourceEventID:   sourceEventID,
		SourceEventType: string(autonomy.EventToolPolicyResult),
		PermissionSnapshot: &autonomy.PermissionSnapshot{
			ContractID:        a.contract.ContractID,
			ProtocolHash:      protocolHash,
			PermissionProfile: a.contract.Scope.PermissionProfile,
			AllowedTools:      append([]string(nil), a.brief.AllowedTools...),
			HiddenTools:       append([]string(nil), a.contract.Capabilities.HiddenTools...),
			CapabilityRefs:    runnerAutonomyPermissionCapabilityRefs(policy),
			PathRoleRefs:      runnerAutonomyPermissionPathRoleRefs(policy),
			CapturedBy:        "harness",
		},
	})
}

func (a *runnerAutonomyRecorder) emitFailureDeltaIntentFromPolicy(policy autonomy.ContractPolicyEvent, sourceEventID string) {
	if a == nil || !runnerAutonomyPolicyDecisionIsFailure(policy.Decision) {
		return
	}
	protocolHash := a.activeProtocolHash()
	if strings.TrimSpace(protocolHash) == "" {
		return
	}
	a.emitModelExchangeEvent(autonomy.ContractModelExchangeEvent{
		Kind:            "failure_delta",
		ContractID:      a.contract.ContractID,
		ProtocolHash:    protocolHash,
		PhaseID:         a.lastTurnPhaseEvent.PhaseID,
		ModelCallID:     a.lastTurnPhaseEvent.ModelCallID,
		InvariantID:     strings.TrimSpace(policy.InvariantID),
		SourceEventID:   sourceEventID,
		SourceEventType: string(autonomy.EventToolPolicyResult),
		FailureDelta: &autonomy.FailureDelta{
			ContractID:   a.contract.ContractID,
			ProtocolHash: protocolHash,
			FailureClass: util.FirstNonEmpty(runnerAutonomyReasonCode(policy.ErrorClass), runnerAutonomyReasonCode(policy.ReasonCode), strings.TrimSpace(string(policy.Decision))),
			BeforeRef:    "policy:before",
			AfterRef:     "policy:" + strings.TrimSpace(string(policy.Decision)),
			EvidenceRefs: []string{"event:" + sourceEventID, "surface:tool_policy"},
			GeneratedBy:  "harness",
		},
	})
}

func (a *runnerAutonomyRecorder) emitFailureDeltaIntentFromGate(input runnerAutonomyGateDecision, gate autonomy.ContractGateEvent, sourceEventID string) {
	if a == nil || !runnerAutonomyPolicyDecisionIsFailure(input.Decision) {
		return
	}
	protocolHash := util.FirstNonEmpty(strings.TrimSpace(gate.ProtocolHash), a.activeProtocolHash())
	if strings.TrimSpace(protocolHash) == "" {
		return
	}
	a.emitModelExchangeEvent(autonomy.ContractModelExchangeEvent{
		Kind:            "failure_delta",
		ContractID:      a.contract.ContractID,
		ProtocolHash:    protocolHash,
		PhaseID:         a.lastTurnPhaseEvent.PhaseID,
		ModelCallID:     a.lastTurnPhaseEvent.ModelCallID,
		InvariantID:     strings.TrimSpace(input.InvariantID),
		SourceEventID:   sourceEventID,
		SourceEventType: string(autonomy.EventGateDecision),
		FailureDelta: &autonomy.FailureDelta{
			ContractID:   a.contract.ContractID,
			ProtocolHash: protocolHash,
			FailureClass: util.FirstNonEmpty(string(input.Gate), runnerAutonomyReasonCode(input.ReasonCode), strings.TrimSpace(string(input.Decision))),
			BeforeRef:    "gate:before",
			AfterRef:     "gate:" + strings.TrimSpace(string(input.Decision)),
			EvidenceRefs: []string{"event:" + sourceEventID, "surface:" + string(input.Gate)},
			GeneratedBy:  "harness",
		},
	})
}

func (a *runnerAutonomyRecorder) emitModelResponseObservationFromPolicy(policy autonomy.ContractPolicyEvent, sourceEventID string) {
	if a == nil || policy.ParserFailure == "" || policy.ParserFailure == autonomy.ParserFailureNone {
		return
	}
	protocolHash := a.activeProtocolHash()
	if strings.TrimSpace(protocolHash) == "" {
		return
	}
	a.emitModelExchangeEvent(autonomy.ContractModelExchangeEvent{
		Kind:            "validation_signal",
		ContractID:      a.contract.ContractID,
		ProtocolHash:    protocolHash,
		PhaseID:         a.lastTurnPhaseEvent.PhaseID,
		ModelCallID:     a.lastTurnPhaseEvent.ModelCallID,
		InvariantID:     strings.TrimSpace(policy.InvariantID),
		SourceEventID:   sourceEventID,
		SourceEventType: string(autonomy.EventToolPolicyResult),
		ValidationSignal: &autonomy.ValidationSignal{
			ContractID:    a.contract.ContractID,
			ProtocolHash:  protocolHash,
			ValidationRef: "parser:" + strings.TrimSpace(string(policy.ParserFailure)),
			Status:        "model_response_observed_non_authoritative",
			EvidenceRefs:  []string{"event:" + sourceEventID, "basis:runner_parser_policy_result"},
			CapturedBy:    "harness",
		},
	})
}

func (a *runnerAutonomyRecorder) emitModelResponseObservationFromGate(input runnerAutonomyGateDecision, gate autonomy.ContractGateEvent, sourceEventID string) {
	if a == nil || input.Gate != autonomy.GateCompletion || !runnerAutonomyPolicyDecisionIsFailure(input.Decision) {
		return
	}
	protocolHash := util.FirstNonEmpty(strings.TrimSpace(gate.ProtocolHash), a.activeProtocolHash())
	if strings.TrimSpace(protocolHash) == "" {
		return
	}
	a.emitModelExchangeEvent(autonomy.ContractModelExchangeEvent{
		Kind:            "validation_signal",
		ContractID:      a.contract.ContractID,
		ProtocolHash:    protocolHash,
		PhaseID:         a.lastTurnPhaseEvent.PhaseID,
		ModelCallID:     a.lastTurnPhaseEvent.ModelCallID,
		InvariantID:     strings.TrimSpace(input.InvariantID),
		SourceEventID:   sourceEventID,
		SourceEventType: string(autonomy.EventGateDecision),
		ValidationSignal: &autonomy.ValidationSignal{
			ContractID:    a.contract.ContractID,
			ProtocolHash:  protocolHash,
			ValidationRef: "completion_gate:" + strings.TrimSpace(string(input.Decision)),
			Status:        "model_response_observed_non_authoritative",
			EvidenceRefs:  []string{"event:" + sourceEventID, "basis:runner_completion_gate_decision"},
			CapturedBy:    "harness",
		},
	})
}

func (a *runnerAutonomyRecorder) emitRollbackControlIfDisabled(protocolHash string) {
	if a == nil || !runnerAutonomyModelExchangeDisabled() {
		return
	}
	protocolHash = util.FirstNonEmpty(strings.TrimSpace(protocolHash), a.activeProtocolHash())
	if protocolHash == "" {
		return
	}
	a.emitModelExchangeEventIgnoringRollback(autonomy.ContractModelExchangeEvent{
		Kind:         "rollback_control",
		ContractID:   a.contract.ContractID,
		ProtocolHash: protocolHash,
		PhaseID:      a.lastTurnPhaseEvent.PhaseID,
		ModelCallID:  a.lastTurnPhaseEvent.ModelCallID,
		RollbackControl: &autonomy.RollbackControl{
			ContractID:   a.contract.ContractID,
			ProtocolHash: protocolHash,
			RollbackRef:  "env:" + runnerAutonomyDisableModelExchangeEnv,
			Decision:     "model_exchange_disabled",
			OperatorRef:  "operator:env",
			Source:       "runner_autonomy",
			PreviousMode: "observe_only_model_exchange",
			NewMode:      "disabled",
			Reason:       "rollback switch active",
			GuardRefs:    []string{"guard:typed_autonomy_events_remain_enabled"},
			AffectedPackIDs: []string{
				string(autonomy.ContractPackCompletionReadiness),
				string(autonomy.ContractPackVerificationPostWrite),
				string(autonomy.ContractPackWriteRequiredPaths),
			},
			AuthorizedBy: "harness",
		},
	})
}

func (a *runnerAutonomyRecorder) emitModelExchangeEvent(event autonomy.ContractModelExchangeEvent) {
	if a == nil || runnerAutonomyModelExchangeDisabled() {
		return
	}
	a.emitModelExchangeEventIgnoringRollback(event)
}

func (a *runnerAutonomyRecorder) emitModelExchangeEventIgnoringRollback(event autonomy.ContractModelExchangeEvent) {
	if a == nil {
		return
	}
	event.ContractID = util.FirstNonEmpty(strings.TrimSpace(event.ContractID), a.contract.ContractID)
	if strings.TrimSpace(event.ProtocolHash) == "" || strings.TrimSpace(event.Kind) == "" {
		return
	}
	autonomyEvent, err := autonomy.NewModelExchangeEvent(a.contract, event, a.nextTime(), a.nextSequence())
	if err != nil {
		return
	}
	a.emitNewEvent(autonomyEvent, err)
}

func runnerAutonomyModelExchangeDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(runnerAutonomyDisableModelExchangeEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func compileModelExchangeSchemaNames() []string {
	return []string{
		autonomy.ContractEnvelopeSchemaVersion,
		autonomy.ContextPacketSchemaVersion,
		autonomy.ModelResponseSchemaVersion,
		autonomy.PermissionSnapshotSchemaVersion,
		autonomy.ValidationSignalSchemaVersion,
		autonomy.FailureDeltaSchemaVersion,
		autonomy.TraceStorePolicySchemaVersion,
		autonomy.RollbackControlSchemaVersion,
	}
}

func runnerAutonomyPermissionCapabilityRefs(policy autonomy.ContractPolicyEvent) []string {
	refs := []string{}
	if policy.Capability != "" {
		refs = append(refs, "capability:"+string(policy.Capability))
	}
	if policy.SideEffect != "" {
		refs = append(refs, "side_effect:"+string(policy.SideEffect))
	}
	if policy.ActionClass != "" {
		refs = append(refs, "action_class:"+string(policy.ActionClass))
	}
	if policy.Transport != "" {
		refs = append(refs, "transport:"+string(policy.Transport))
	}
	if policy.Decision != "" {
		refs = append(refs, "decision:"+string(policy.Decision))
	}
	if policy.ArgumentsDigest != "" {
		refs = append(refs, "arguments_digest:"+policy.ArgumentsDigest)
	}
	return runnerAutonomyBoundedFactStrings(refs, 12)
}

func runnerAutonomyPermissionPathRoleRefs(policy autonomy.ContractPolicyEvent) []string {
	refs := []string{}
	for _, match := range policy.PathRoles {
		path := util.FirstNonEmpty(match.Path, match.MatchedPattern, match.PacketID)
		if path != "" && match.Role != "" {
			refs = append(refs, string(match.Role)+":"+path)
		}
	}
	for _, path := range policy.ProtectedMatches {
		refs = append(refs, "protected:"+path)
	}
	for _, path := range policy.RequiredEditMatches {
		refs = append(refs, "required_edit:"+path)
	}
	for _, path := range policy.OutsideWorkspace {
		refs = append(refs, "outside_workspace:"+path)
	}
	return runnerAutonomyBoundedFactStrings(refs, 24)
}

func (a *runnerAutonomyRecorder) activeProtocolHash() string {
	if a == nil {
		return ""
	}
	return util.FirstNonEmpty(a.currentModelProtocolHash(), strings.TrimSpace(a.lastTurnPhaseEvent.ProtocolHash), strings.TrimSpace(a.lastModelProtocol.ProtocolHash))
}

func runnerAutonomyPolicyDecisionIsFailure(decision autonomy.ToolPolicyDecision) bool {
	switch decision {
	case autonomy.PolicyDeny, autonomy.PolicyReject, autonomy.PolicyStop:
		return true
	default:
		return false
	}
}

func (a *runnerAutonomyRecorder) emitNewEvent(event autonomy.Event, err error) {
	if a == nil || err != nil {
		return
	}
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	a.runner.emitEvent(RunEvent{Role: runnerAutonomyEventRole, Content: string(data)})
}

func (a *runnerAutonomyRecorder) emitPolicyEvent(req workspace.ToolRequest, input runnerAutonomyPolicyEventInput) {
	if a == nil {
		return
	}
	policy := a.policyEventForRequest(req, input)
	event, err := autonomy.NewPolicyEvent(input.EventType, a.contract, policy, a.nextTime(), a.nextSequence())
	a.emitNewEvent(event, err)
	if err == nil && input.EventType == autonomy.EventToolPolicyResult {
		a.emitPermissionSnapshot(req, policy, event.EventID)
		a.emitPolicyFriction(req, input, policy, event.EventID)
		a.emitFailureDeltaIntentFromPolicy(policy, event.EventID)
		a.emitModelResponseObservationFromPolicy(policy, event.EventID)
	}
}

func (a *runnerAutonomyRecorder) emitPolicyFriction(req workspace.ToolRequest, input runnerAutonomyPolicyEventInput, policy autonomy.ContractPolicyEvent, sourceEventID string) {
	if a == nil || runnerAutonomyPolicyFrictionSuppressed(input) {
		return
	}
	decision, ok := runnerAutonomyFrictionDecision(policy.Decision)
	if !ok {
		return
	}
	phase := a.lastTurnPhase
	protocol := a.visibleModelProtocol()
	protocolVisible := a.currentModelProtocolHash() != ""
	friction := autonomy.ContractGuardFrictionEvent{
		Step:                 phase.Step,
		Lane:                 a.contract.Scope.Lane,
		Phase:                phase.Phase,
		PhaseID:              phase.PhaseID,
		ModelCallID:          phase.ModelCallID,
		SourceEventID:        sourceEventID,
		ContractVersion:      a.contract.SchemaVersion,
		ProtocolHash:         a.currentModelProtocolHash(),
		InvariantID:          strings.TrimSpace(policy.InvariantID),
		ObligationID:         firstRunnerAutonomyString(a.openObligationIDs()),
		Subject:              autonomy.GuardFrictionSubjectToolCall,
		Decision:             decision,
		GuardSurface:         runnerAutonomyPolicyFrictionSurface(policy),
		FirstInvalidAction:   runnerAutonomyPolicyFrictionInvalidAction(req, policy),
		VisibleActionClasses: runnerAutonomyFrictionVisibleActionClasses(protocol, phase, protocolVisible),
		VisibleTools:         runnerAutonomyFrictionVisibleTools(protocol, phase, protocolVisible),
		SafeAlternatives:     runnerAutonomyFrictionSafeAlternatives(protocol, phase, protocolVisible),
		ModelContractVisible: protocolVisible,
		RetryOutcome:         autonomy.GuardFrictionRetryNotApplicable,
		Avoidability:         autonomy.GuardFrictionAvoidabilityUnknown,
		EvidenceRefs:         runnerAutonomyPolicyFrictionEvidenceRefs(input, policy),
	}
	a.emitNewEvent(autonomy.NewGuardFrictionEvent(a.contract, friction, a.nextTime(), a.nextSequence()))
}

func runnerAutonomyPolicyFrictionSuppressed(input runnerAutonomyPolicyEventInput) bool {
	return strings.TrimSpace(input.Source) == "runner_required_edit_scope"
}

func runnerAutonomyPolicyFrictionSurface(policy autonomy.ContractPolicyEvent) autonomy.GuardFrictionSurface {
	if policy.ParserFailure != "" && policy.ParserFailure != autonomy.ParserFailureNone {
		return autonomy.GuardFrictionSurfaceParser
	}
	return autonomy.GuardFrictionSurfaceToolPolicy
}

func runnerAutonomyPolicyFrictionInvalidAction(req workspace.ToolRequest, policy autonomy.ContractPolicyEvent) string {
	if tool := strings.TrimSpace(policy.ToolName); tool != "" {
		return tool
	}
	if tool := strings.TrimSpace(req.Tool); tool != "" {
		return tool
	}
	return "tool_call"
}

func runnerAutonomyPolicyFrictionEvidenceRefs(input runnerAutonomyPolicyEventInput, policy autonomy.ContractPolicyEvent) []string {
	refs := []string{"reason:" + runnerAutonomyReasonCode(policy.ReasonCode)}
	if policy.Source != "" {
		refs = append(refs, "source:"+runnerAutonomyReasonCode(policy.Source))
	}
	if policy.ParserFailure != "" && policy.ParserFailure != autonomy.ParserFailureNone {
		refs = append(refs, "parser_failure:"+runnerAutonomyReasonCode(string(policy.ParserFailure)))
	}
	if input.ErrorClass != "" {
		refs = append(refs, "error_class:"+runnerAutonomyReasonCode(input.ErrorClass))
	}
	return refs
}

func (a *runnerAutonomyRecorder) policyEventForRequest(req workspace.ToolRequest, input runnerAutonomyPolicyEventInput) autonomy.ContractPolicyEvent {
	intent := runnerAutonomyIntentForRequest(a.brief, req, input)
	action := autonomy.ActionFromIntent(intent)
	policy := autonomy.ContractPolicyEvent{
		ToolName:         strings.TrimSpace(req.Tool),
		ActionClass:      action.ActionClass,
		ActionKey:        action.ActionKey,
		Transport:        intent.Transport,
		ParserFailure:    intent.ParserFailure,
		RepairSource:     intent.RepairSource,
		Capability:       intent.Capability,
		SideEffect:       intent.SideEffect,
		ArgumentsDigest:  runnerAutonomyArgumentsDigest(req),
		Decision:         input.Decision,
		InvariantID:      strings.TrimSpace(input.InvariantID),
		ReasonCode:       strings.TrimSpace(input.ReasonCode),
		ApprovalRequired: input.ApprovalRequired,
		Result:           strings.TrimSpace(input.Result),
		ErrorClass:       strings.TrimSpace(input.ErrorClass),
		ChangedPaths:     compactRunnerAutonomyPaths(input.ChangedPaths),
		Source:           strings.TrimSpace(input.Source),
	}
	resolution, ok := a.resolvePolicyPaths(req)
	if ok {
		policy.PathRoles = resolution.PolicyPathMatches()
		for _, item := range resolution.Resolutions {
			switch item.EffectiveRole {
			case autonomy.PathRoleProtected:
				policy.ProtectedMatches = append(policy.ProtectedMatches, item.Path)
			case autonomy.PathRoleRequiredEdit:
				policy.RequiredEditMatches = append(policy.RequiredEditMatches, item.Path)
			}
			if item.OutsideWorkspace {
				policy.OutsideWorkspace = append(policy.OutsideWorkspace, item.Path)
			}
			if policy.InvariantID == "" && item.EffectiveInvariantID != "" {
				policy.InvariantID = string(item.EffectiveInvariantID)
			}
			if policy.ReasonCode == "" && item.ReasonCode != "" {
				policy.ReasonCode = item.ReasonCode
			}
			if policy.SideEffect == autonomy.SideEffectUnknown {
				for _, role := range item.Roles {
					if role.SideEffect != "" {
						policy.SideEffect = role.SideEffect
						break
					}
				}
			}
		}
		normalizeRequiredEditScopePolicyEvent(&policy, req, resolution)
	}
	policy.ProtectedMatches = compactRunnerAutonomyPaths(policy.ProtectedMatches)
	policy.RequiredEditMatches = compactRunnerAutonomyPaths(policy.RequiredEditMatches)
	policy.OutsideWorkspace = compactRunnerAutonomyPaths(policy.OutsideWorkspace)
	return policy
}

func runnerAutonomyIntentForRequest(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest, input runnerAutonomyPolicyEventInput) autonomy.ToolIntent {
	transport := input.Transport
	if transport == "" {
		transport = autonomy.ToolTransportTextualCall
	}
	intent := autonomy.ToolIntent{
		ToolName:        strings.TrimSpace(req.Tool),
		Capability:      runnerAutonomyPrimaryCapability(brief, req.Tool),
		SideEffect:      runnerAutonomySideEffectForBrief(brief, req),
		ActionClass:     runnerAutonomyActionClassForBrief(brief, req),
		Transport:       transport,
		ParserFailure:   input.ParserFailure,
		RepairSource:    input.RepairSource,
		TargetRefs:      runnerAutonomyPolicyPaths(req),
		ArgumentsDigest: runnerAutonomyArgumentsDigest(req),
		Source:          input.Source,
	}
	return autonomy.CanonicalizeToolIntent(intent)
}

func runnerAutonomyActionClassForRequest(req workspace.ToolRequest) autonomy.ActionClass {
	return runnerAutonomyActionClassForBrief(workerruntime.WorkerRuntimeBrief{}, req)
}

func runnerAutonomyActionClassForBrief(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest) autonomy.ActionClass {
	tool := strings.TrimSpace(req.Tool)
	switch {
	case tool == "submit_plan":
		return autonomy.ActionClassSubmitPlan
	case tool == "task":
		return autonomy.ActionClassDelegate
	case workspace.IsWorkspaceWriteTool(tool):
		if tool == "write_file" && strings.TrimSpace(req.Path) != "" && !runnerAutonomyLooksLikeSourcePath(req.Path) {
			return autonomy.ActionClassDeliverableWrite
		}
		return autonomy.ActionClassPerEditWrite
	case runnerAutonomyVerificationTool(brief, req):
		return autonomy.ActionClassVerify
	case workspace.IsReadTool(tool):
		if strings.Contains(toolActionClass(req), "broad_inspection") ||
			tool == "search_files" || tool == "search_content" || tool == "rg" || tool == "grep" {
			return autonomy.ActionClassSearch
		}
		return autonomy.ActionClassRead
	default:
		return autonomy.ActionClassGenericProbe
	}
}

func runnerAutonomyVerificationTool(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case "source_sanity", "go_diagnostics", "cpp_diagnostics":
		return true
	case "shell":
		return runnerAutonomySideEffectForBrief(brief, req) == autonomy.SideEffectReadOnly
	default:
		return false
	}
}

func runnerAutonomyLooksLikeSourcePath(path string) bool {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".c", ".cc", ".cpp", ".cs", ".go", ".h", ".hpp", ".java", ".js", ".jsx", ".kt", ".kts", ".php", ".py", ".rb", ".rs", ".swift", ".ts", ".tsx":
		return true
	default:
		return false
	}
}

func normalizeRequiredEditScopePolicyEvent(policy *autonomy.ContractPolicyEvent, req workspace.ToolRequest, resolution autonomy.PathRoleResolutionSet) {
	if policy == nil || !workspace.IsWorkspaceWriteTool(req.Tool) {
		return
	}
	if policy.Decision != autonomy.PolicyDeny && policy.Decision != autonomy.PolicyReject {
		return
	}
	if strings.TrimSpace(policy.ReasonCode) != "required_edit_scope_rejected" &&
		strings.TrimSpace(policy.ErrorClass) != "required_edit_scope" {
		return
	}
	hasOutOfScopeTarget := false
	for _, item := range resolution.Resolutions {
		if item.EffectiveRole == autonomy.PathRoleProtected {
			return
		}
		if item.EffectiveRole != autonomy.PathRoleRequiredEdit {
			hasOutOfScopeTarget = true
		}
	}
	if !hasOutOfScopeTarget {
		return
	}
	policy.InvariantID = string(autonomy.InvariantForbiddenPath)
	policy.ReasonCode = "forbidden_path"
	policy.Result = "denied"
}

func (a *runnerAutonomyRecorder) resolvePolicyPaths(req workspace.ToolRequest) (autonomy.PathRoleResolutionSet, bool) {
	if a == nil {
		return autonomy.PathRoleResolutionSet{}, false
	}
	paths := runnerAutonomyPolicyPaths(req)
	if len(paths) == 0 {
		return autonomy.PathRoleResolutionSet{}, false
	}
	workspaceRoot := ""
	if a.runner != nil && a.runner.Workspace != nil {
		workspaceRoot = a.runner.Workspace.Root()
	}
	resolution, err := autonomy.ResolveContractPathRoles(a.contract, workspaceRoot, paths...)
	if err != nil {
		return autonomy.PathRoleResolutionSet{}, false
	}
	return resolution, true
}

func (a *runnerAutonomyRecorder) nextSequence() int64 {
	a.sequence++
	return a.sequence
}

func (a *runnerAutonomyRecorder) nextTime() time.Time {
	if a.occurredAt.IsZero() {
		a.occurredAt = time.Now().UTC()
	}
	return a.occurredAt.Add(time.Duration(a.sequence+1) * time.Millisecond)
}

func (a *runnerAutonomyRecorder) obligationIDsForGate(gate autonomy.GateKind) []string {
	if a == nil {
		return nil
	}
	obligations := a.obligationsForGate(gate)
	ids := make([]string, 0, len(obligations))
	for _, obligation := range obligations {
		ids = append(ids, obligation.ObligationID)
	}
	return ids
}

func (a *runnerAutonomyRecorder) obligationsForGate(gate autonomy.GateKind) []autonomy.Obligation {
	if a == nil {
		return nil
	}
	var kinds []autonomy.ObligationKind
	switch gate {
	case autonomy.GateWrite:
		kinds = []autonomy.ObligationKind{autonomy.ObligationWrite}
	case autonomy.GateVerification:
		kinds = []autonomy.ObligationKind{autonomy.ObligationVerify}
	case autonomy.GateCompletion:
		kinds = []autonomy.ObligationKind{autonomy.ObligationFinalAnswer, autonomy.ObligationWrite, autonomy.ObligationVerify, autonomy.ObligationEvidence}
	case autonomy.GateLoop:
		kinds = []autonomy.ObligationKind{autonomy.ObligationLoop}
	case autonomy.GateBudget:
		kinds = []autonomy.ObligationKind{autonomy.ObligationBudget}
	case autonomy.GateReadOnly:
		kinds = []autonomy.ObligationKind{autonomy.ObligationEvidence, autonomy.ObligationFinalAnswer}
	}
	kindSet := make(map[autonomy.ObligationKind]struct{}, len(kinds))
	for _, kind := range kinds {
		kindSet[kind] = struct{}{}
	}
	var obligations []autonomy.Obligation
	for _, obligation := range a.obligations {
		if _, ok := kindSet[obligation.Kind]; ok {
			obligations = append(obligations, obligation)
		}
	}
	return obligations
}

func runnerAutonomyVerificationStatus(results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) string {
	if runnerBriefRequiresMutation(brief) && !verificationContractSkipsGate(brief.VerificationContract) {
		if _, failed := failedVerificationAfterLatestWrite(results, brief); failed {
			return "failed"
		}
		if hasPendingVerificationAfterWrites(results, brief) || sourceEditsNeedSemanticVerification(brief, results) {
			return "pending"
		}
		if hasSuccessfulVerificationResult(results) {
			return "passed"
		}
	}
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if runnerAutonomyIsVerificationResult(result) {
			if strings.TrimSpace(result.Error) != "" {
				return "failed"
			}
			return "observed"
		}
	}
	return "pending"
}

func runnerAutonomyIsVerificationResult(result workspace.ToolResult) bool {
	return isVerificationRequest(workspace.ToolRequest{Tool: result.Tool})
}

func runnerAutonomyRequiredPathStatus(contract autonomy.AutonomyContract, results []workspace.ToolResult) []string {
	changed := changedPathsFromToolResults(results)
	var status []string
	for _, role := range contract.PathRoles {
		if role.Role != autonomy.PathRoleRequiredEdit {
			continue
		}
		path := normalizeRunnerPath(role.PathPattern)
		if path == "" {
			continue
		}
		state := "pending"
		if _, ok := changed[path]; ok {
			state = "changed"
		}
		status = append(status, path+":"+state)
	}
	return status
}

func runnerAutonomyBudgetTarget(policy RunPolicy) string {
	var parts []string
	if policy.MaxToolSteps > 0 {
		parts = append(parts, "max_tool_steps")
	}
	if policy.MaxModelCalls > 0 {
		parts = append(parts, "max_model_calls")
	}
	if policy.MaxContextMessages > 0 {
		parts = append(parts, "max_context_messages")
	}
	if policy.MaxContextChars > 0 {
		parts = append(parts, "max_context_chars")
	}
	if len(parts) == 0 {
		return "unbounded"
	}
	return strings.Join(parts, ",")
}

func runnerAutonomyReasonCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "unspecified"
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
		if b.Len() >= 80 {
			break
		}
	}
	out := strings.Trim(strings.TrimSpace(b.String()), "_")
	if out == "" {
		return "unspecified"
	}
	return out
}

func runnerAutonomyObligationID(kind autonomy.ObligationKind, target string) string {
	target = strings.TrimSpace(filepath.ToSlash(target))
	if target == "" {
		return "obl-" + string(kind)
	}
	return "obl-" + string(kind) + "-" + digestRunnerAutonomyValue(target)[:12]
}

func runnerAutonomyArgumentsDigest(req workspace.ToolRequest) string {
	normalized := workspace.ToolRequest{
		Tool:            strings.TrimSpace(req.Tool),
		Path:            strings.TrimSpace(req.Path),
		Pattern:         strings.TrimSpace(req.Pattern),
		Query:           strings.TrimSpace(req.Query),
		Symbol:          strings.TrimSpace(req.Symbol),
		Task:            strings.TrimSpace(req.Task),
		Purpose:         strings.TrimSpace(req.Purpose),
		Workdir:         strings.TrimSpace(req.Workdir),
		Command:         strings.TrimSpace(req.Command),
		Mode:            strings.TrimSpace(req.Mode),
		RequiredTests:   append([]string(nil), req.RequiredTests...),
		ForbiddenPaths:  append([]string(nil), req.ForbiddenPaths...),
		Destination:     strings.TrimSpace(req.Destination),
		Declarations:    append([]string(nil), req.Declarations...),
		Moves:           append([]workspace.GoDeclarationMove(nil), req.Moves...),
		NamedGap:        strings.TrimSpace(req.NamedGap),
		WhyNotAnswering: strings.TrimSpace(req.WhyNotAnswering),
		Description:     strings.TrimSpace(req.Description),
		TaskID:          strings.TrimSpace(req.TaskID),
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return ""
	}
	return "sha256:" + digestRunnerAutonomyValue(string(data))
}

func runnerAutonomyPrimaryCapability(brief workerruntime.WorkerRuntimeBrief, tool string) autonomy.Capability {
	capabilities := runnerAutonomyCapabilitiesForTool(brief, strings.TrimSpace(tool))
	if len(capabilities) == 0 {
		return ""
	}
	return capabilities[0]
}

func runnerAutonomyPolicyPaths(req workspace.ToolRequest) []string {
	switch strings.TrimSpace(req.Tool) {
	case "read_file", "search_content", "search_files", "list_files", "file_stats", "git_diff",
		"go_read_symbol", "go_extract_body", "go_find_symbol", "cpp_read_symbol", "cpp_find_symbol",
		"context_pack", "context_query", "context_zoom", "discovery_query", "structural_search",
		"dependency_graph", "history_range", "history_symbol", "blame_range", "ctags_symbols",
		"semgrep_scan", "joern_scan":
		for _, path := range []string{req.Path, req.Workdir} {
			if trimmed := strings.TrimSpace(path); trimmed != "" {
				return []string{trimmed}
			}
		}
		return nil
	default:
		return writeRequestPaths(req)
	}
}

func runnerAutonomySideEffectForBrief(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest) autonomy.SideEffectClass {
	if metadata, ok := runnerAutonomyPlannerToolMetadata(brief, req.Tool); ok {
		return metadata.SideEffect
	}
	return runnerAutonomyFallbackSideEffectForRequest(req)
}

func runnerAutonomyFallbackSideEffectForRequest(req workspace.ToolRequest) autonomy.SideEffectClass {
	switch strings.TrimSpace(req.Tool) {
	case "read_file", "search_files", "search_content", "list_files", "context_pack", "context_query", "context_zoom",
		"discovery_query", "go_read_symbol", "go_extract_body", "go_find_symbol", "cpp_read_symbol", "cpp_find_symbol",
		"structural_search", "dependency_graph", "history_range", "history_symbol", "blame_range", "ctags_symbols",
		"semgrep_scan", "joern_scan", "file_stats", "git_diff":
		return autonomy.SideEffectReadOnly
	case "edit_file", "write_file", "append_file", "mkdir", "delegate_edit", "go_move_declarations":
		return autonomy.SideEffectAdditive
	case "apply_patch":
		if effect, ok := runnerAutonomyPatchSideEffect(req.Content); ok {
			return effect
		}
		return autonomy.SideEffectUnknown
	case "shell", "source_sanity", "go_diagnostics", "cpp_diagnostics":
		return runnerAutonomyShellSideEffect(req.Command)
	default:
		return autonomy.SideEffectUnknown
	}
}

func runnerAutonomyPatchSideEffect(patch string) (autonomy.SideEffectClass, bool) {
	ops, err := workspace.ParsePatch(patch)
	if err != nil {
		return "", false
	}
	effect := autonomy.SideEffectReadOnly
	for _, op := range ops {
		switch op.Kind {
		case "delete":
			return autonomy.SideEffectDestructive, true
		case "add", "update":
			if op.MoveTo != "" {
				return autonomy.SideEffectDestructive, true
			}
			effect = autonomy.SideEffectAdditive
		}
	}
	if effect == autonomy.SideEffectReadOnly {
		return "", false
	}
	return effect, true
}

func runnerAutonomyShellSideEffect(command string) autonomy.SideEffectClass {
	command = strings.TrimSpace(command)
	if command == "" {
		return autonomy.SideEffectUnknown
	}
	if shellCommandHasControlSyntax(command) {
		return autonomy.SideEffectUnknown
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return autonomy.SideEffectUnknown
	}
	head := strings.TrimPrefix(strings.ToLower(filepath.Base(strings.Trim(fields[0], `"'`))), "./")
	args := lowerRunnerAutonomyShellArgs(fields[1:])
	if runnerAutonomyReadOnlyShellCommand(head, args) {
		return autonomy.SideEffectReadOnly
	}
	return autonomy.SideEffectUnknown
}

func shellCommandHasControlSyntax(command string) bool {
	inSingleQuote := false
	inDoubleQuote := false
	escaped := false
	for i := 0; i < len(command); i++ {
		ch := command[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '\'' && !inDoubleQuote {
			inSingleQuote = !inSingleQuote
			continue
		}
		if ch == '"' && !inSingleQuote {
			inDoubleQuote = !inDoubleQuote
			continue
		}
		if inSingleQuote {
			continue
		}
		if ch == '$' && i+1 < len(command) && command[i+1] == '(' {
			return true
		}
		if inDoubleQuote {
			if ch == '`' {
				return true
			}
			continue
		}
		switch ch {
		case '\n', '\r', ';', '&', '|', '<', '>', '`':
			return true
		}
	}
	return false
}

func lowerRunnerAutonomyShellArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		arg = strings.ToLower(strings.TrimSpace(strings.Trim(arg, `"'`)))
		if arg != "" {
			out = append(out, arg)
		}
	}
	return out
}

func runnerAutonomyReadOnlyShellCommand(head string, args []string) bool {
	switch head {
	case "git":
		return runnerAutonomyGitReadOnlyArgs(args)
	case "go":
		return len(args) > 0 && runnerStringInSet(args[0], "test", "build")
	case "make":
		return len(args) > 0 && runnerStringInSet(args[0], "test", "build")
	case "npm":
		return len(args) > 0 && args[0] == "test"
	case "cargo":
		return len(args) > 0 && args[0] == "test"
	default:
		return false
	}
}

func runnerAutonomyGitReadOnlyArgs(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "status":
		return true
	case "diff":
		for _, arg := range args[1:] {
			if arg == "--output" || strings.HasPrefix(arg, "--output=") {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func compactRunnerAutonomyPaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = normalizeRunnerPath(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	slices.Sort(out)
	return out
}

func digestRunnerAutonomyValue(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}

func mapAutonomyCapabilityKeys(values map[autonomy.Capability]struct{}) []autonomy.Capability {
	out := make([]autonomy.Capability, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	return out
}
