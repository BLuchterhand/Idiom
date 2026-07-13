package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/capabilities/workspace/patching"
	"idiom/internal/contracts/autonomy"
	contractcompile "idiom/internal/contracts/compile"
	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/textutil"
	semanticlanguage "idiom/internal/repository/semantic/language"
	"idiom/internal/repository/semantic/languagetools"
	"idiom/internal/runtime/workerruntime"
)

const runnerAutonomyEventRole = "autonomy_event"

const runnerAutonomyDisableModelExchangeEnv = "IDIOM_DISABLE_RUNNER_MODEL_EXCHANGE_EVENTS"

const malformedToolPolicyInvariantToolRequest = "parser.tool_request"

type runnerAutonomyRecorder struct {
	runner                 *runExecution
	brief                  workerruntime.WorkerRuntimeBrief
	contract               autonomy.AutonomyContract
	activeAuthority        contractcompile.ActiveAuthorityBinding
	activeTurnContract     TurnContract
	plannerCompile         contractcompile.CompileResult
	hasPlannerCompile      bool
	sequence               int64
	occurredAt             time.Time
	obligations            []autonomy.Obligation
	openObligations        map[string]autonomy.Obligation
	closedObligations      map[string]struct{}
	lastTurnPhase          TurnPhaseContract
	lastTurnPhaseEvent     autonomy.TurnPhaseContractEvent
	lastModelProtocol      contractcompile.ModelProtocolProjection
	lastModelPresented     bool
	lastStoredProtocolHash string
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
	Target           string
	PackID           autonomy.ContractPackID
	InvariantID      string
	ReasonCode       string
	Transport        autonomy.ToolTransport
	ParserFailure    autonomy.ParserFailureClass
	RepairSource     autonomy.RepairSource
	ApprovalRequired bool
	Result           string
	ErrorClass       string
	ChangedPaths     []string
	EvidencePaths    []string
	Source           string
}

type runnerAutonomyGateEventFields struct {
	PackID           autonomy.ContractPackID
	EnforcementMode  autonomy.EnforcementMode
	ObservedFacts    []autonomy.ContractObservedFact
	SafeAlternatives []string
}

func newRunnerAutonomyRecorder(r *runExecution, policy RunPolicy, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) *runnerAutonomyRecorder {
	if r == nil || r.runner.OnEvent == nil {
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

func buildRunnerAutonomyContract(r *runExecution, policy RunPolicy, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (runnerAutonomyContractBuild, bool) {
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

func compileRunnerPlanExecutePlannerContract(r *runExecution, policy RunPolicy, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (contractcompile.CompileResult, bool) {
	inspection := r.permissionAuthorizedInspection(textutil.Merge(
		r.workspaceAdvertisedAllowedTools(),
		brief.AllowedTools,
	))
	input, ok := runnerPlanExecutePlannerInput(brief, results, TurnPhaseContract{}, policy, false, "", inspection, r.planThenExecutePathAuthority(), r.completionChangedPaths(results))
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
			binding.ActiveInvariantID = textutil.FirstNonEmpty(strings.TrimSpace(protocol.ActiveInvariantID), contractcompile.InvariantActiveAuthority)
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

func legacyRunnerAutonomyContract(r *runExecution, policy RunPolicy, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (autonomy.AutonomyContract, []autonomy.Obligation, bool) {
	brief = workerruntime.NormalizeRuntimeBrief(brief)

	lane := textutil.FirstNonEmpty(strings.TrimSpace(brief.Lane), r.agentName())

	if lane == "" {
		lane = "main"
	}

	scope := autonomy.ContractScope{
		InvocationKind:    autonomy.InvocationLane,
		Lane:              lane,
		LaneRunID:         time.Now().UTC().Format(time.RFC3339Nano),
		TaskFamily:        strings.TrimSpace(brief.TaskFamily),
		RuntimeContract:   strings.TrimSpace(brief.Contract),
		PermissionProfile: strings.TrimSpace(brief.PermissionProfile),
	}
	completion := r.implementationCompletionState(results)
	requiredCoverage := append([]string(nil), brief.RequiredEditPaths...)
	if r.state.mutation.bound {
		requiredCoverage = append([]string(nil), r.state.mutation.canonicalScopeProjection.RequiredCoverageIDs...)
	}
	pathRoles := runnerAutonomyPathRoles(brief, completion.ChangedPaths, requiredCoverage)
	obligations := runnerAutonomyObligations(brief, policy, results, completion, requiredCoverage)

	contract, err := autonomy.NewContract(autonomy.ContractInput{
		Scope:        scope,
		Authority:    runnerAutonomyAuthority(brief),
		PathRoles:    pathRoles,
		Capabilities: runnerAutonomyCapabilities(brief),
		Obligations:  obligations,
		Budget: autonomy.BudgetState{
			MaxToolSteps:             policy.MaxToolSteps,
			MaxModelCalls:            policy.MaxModelCalls,
			MaxOperationalModelCalls: policy.MaxOperationalModelCalls,
			MaxContextMessages:       policy.MaxContextMessages,
			MaxContextChars:          policy.MaxContextChars,
		},
		Verification: autonomy.VerificationRequirement{
			Required:               runnerVerificationRequired(brief),
			Contract:               strings.TrimSpace(brief.VerificationContract),
			RequiredAfterWrite:     runnerBriefRequiresMutation(brief) && !workerruntime.VerificationContractSkipsGate(brief.VerificationContract),
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
			Freshness: autonomy.FreshnessUnknown,
		})
	}

	return authority
}

func runnerAutonomyPathRoles(brief workerruntime.WorkerRuntimeBrief, changedPaths []string, requiredCoverage []string) []autonomy.PathRole {
	var roles []autonomy.PathRole

	for _, path := range requiredCoverage {
		if normalized := NormalizeRunnerPath(path); normalized != "" {
			roles = append(roles, autonomy.PathRole{Role: autonomy.PathRoleRequiredEdit, PathPattern: normalized, Freshness: autonomy.FreshnessFresh, Reason: "runtime brief required_edit_paths"})
		}
	}

	for _, path := range brief.AllowedEditPaths {
		if normalized := NormalizeRunnerPath(path); normalized != "" {
			roles = append(roles, autonomy.PathRole{Role: autonomy.PathRoleAllowedEdit, PathPattern: normalized, Freshness: autonomy.FreshnessFresh, Reason: "runtime brief allowed_edit_paths"})
		}
	}

	for _, path := range brief.EvidencePaths {
		if normalized := NormalizeRunnerPath(path); normalized != "" {
			roles = append(roles, autonomy.PathRole{Role: autonomy.PathRoleEvidenceReadonly, PathPattern: normalized, Freshness: autonomy.FreshnessFresh, Reason: "runtime brief evidence_paths"})
		}
	}

	for _, path := range changedPaths {
		if normalized := NormalizeRunnerPath(path); normalized != "" {
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

func runnerAutonomyPlannerToolMetadata(brief workerruntime.WorkerRuntimeBrief, tool string) (workspace.PlanExecutePlannerToolMetadata, bool) {
	if !planThenExecutePlannerBrief(brief) {
		return workspace.PlanExecutePlannerToolMetadata{}, false
	}

	return workspace.LookupPlanExecutePlannerToolMetadata(tool)
}

func runnerAutonomyFallbackCapabilitiesForTool(tool string) []autonomy.Capability {
	if capabilities := workspace.ToolPolicyCapabilities(tool); len(capabilities) > 0 {
		return capabilities
	}

	return nil
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

func runnerAutonomyObligations(brief workerruntime.WorkerRuntimeBrief, policy RunPolicy, results []workspace.ToolResult, completion implementationCompletionState, requiredCoverage []string) []autonomy.Obligation {
	missing := make(map[string]struct{}, len(completion.MissingRequiredPaths))
	for _, path := range completion.MissingRequiredPaths {
		missing[path] = struct{}{}
	}

	var obligations []autonomy.Obligation

	if runnerBriefRequiresMutation(brief) {
		for _, path := range requiredCoverage {
			path = NormalizeRunnerPath(path)
			if path == "" {
				continue
			}

			status := autonomy.ObligationResolved
			if _, ok := missing[path]; ok {
				status = autonomy.ObligationOpen
			} else if !completion.HasChanges() {
				// A required path cannot be considered resolved before the
				// workspace has produced a durable commit effect.
				status = autonomy.ObligationOpen
			} else {
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

		if len(requiredCoverage) == 0 {
			status := autonomy.ObligationOpen
			if completion.HasChanges() {
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

	if runnerBriefRequiresMutation(brief) && !workerruntime.VerificationContractSkipsGate(brief.VerificationContract) {
		owner := autonomy.OwnerHost
		if plannerOwnsVerification(brief) {
			owner = autonomy.OwnerPlanner
		}

		status := autonomy.ObligationOpen
		if !completion.VerificationPending && !completion.VerificationFailed && !completion.SemanticVerificationPending && hasSuccessfulVerificationResult(results) {
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
			RequiredBy:             completionGateToolName,
			FinalAnswerRequirement: textutil.FirstNonEmpty(strings.TrimSpace(brief.Contract), string(workerruntime.ContractDirectAnswer)),
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
			obligation.SourceEvent = textutil.FirstNonEmpty(strings.TrimSpace(obligation.SourceEvent), compiledEvent.EventID)
			obligation.EvidenceRef = textutil.FirstNonEmpty(strings.TrimSpace(obligation.EvidenceRef), compiledEvent.EventID)
			a.emitNewEvent(autonomy.NewObligationStateEvent(autonomy.EventObligationResolved, a.contract, obligation, a.nextTime(), a.nextSequence()))

			a.closedObligations[obligation.ObligationID] = struct{}{}
		case autonomy.ObligationWaived:
			obligation.SourceEvent = textutil.FirstNonEmpty(strings.TrimSpace(obligation.SourceEvent), compiledEvent.EventID)
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
	a.activeAuthority = binding
	a.lastModelProtocol = protocol
	a.lastModelPresented = false
	a.lastStoredProtocolHash = strings.TrimSpace(protocol.ProtocolHash)

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
	a.activeTurnContract = a.turnContractProjection(contract, protocol, binding, payload)

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

func (a *runnerAutonomyRecorder) turnContractProjection(contract TurnPhaseContract, protocol contractcompile.ModelProtocolProjection, binding contractcompile.ActiveAuthorityBinding, payload autonomy.TurnPhaseContractEvent) TurnContract {
	if a == nil {
		return TurnContract{}
	}
	if a.hasPlannerCompile {
		compiled := a.plannerCompile
		if strings.TrimSpace(compiled.Protocol.ProtocolHash) == "" {
			compiled.Protocol.ProtocolHash = protocol.ProtocolHash
		}
		compiled.ActiveAuthority = binding
		if strings.TrimSpace(compiled.Contract.ContractID) == "" {
			compiled.Contract = a.contract
		}
		return FromCompileResult(compiled)
	}

	projection := contract.RuntimeProjection()
	if strings.TrimSpace(projection.ContractID) == "" {
		projection.ContractID = payload.ContractID
	}
	if strings.TrimSpace(projection.ProtocolHash) == "" {
		projection.ProtocolHash = payload.ProtocolHash
	}
	if strings.TrimSpace(projection.PhaseID) == "" {
		projection.PhaseID = payload.PhaseID
	}
	projection.Canonical = a.contract
	projection.Protocol = protocol
	projection.ActiveAuthority = binding
	return projection
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
	a.lastStoredProtocolHash = strings.TrimSpace(protocol.ProtocolHash)
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
		completionProtocolHash := textutil.FirstNonEmpty(gate.ProtocolHash, a.activeProtocolHash())
		a.emitNewEvent(autonomy.NewCompletionGateDecisionEvent(a.contract, autonomy.ContractCompletionEvent{
			Decision:             input.Decision,
			InvariantID:          input.InvariantID,
			ProtocolHash:         completionProtocolHash,
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
		addGateFactCount(&facts, "budget_max_operational_model_calls", gate.Budget.MaxOperationalModelCalls)
		if gate.Budget.Exhausted {
			addGateFactValue(&facts, autonomy.ObservedFactReasonCode, "budget_exhaustion_reason", runnerAutonomyReasonCode(gate.Budget.ExhaustionReason))
		}
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

	*facts = append(*facts, autonomy.ContractObservedFact(string(autonomy.ObservedFactCount)+":"+name+"="+strconv.Itoa(count)))
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
		if textutil.EqualTrimFold(input.VerificationStatus, "passed") ||
			textutil.EqualTrimFold(input.VerificationStatus, "observed") {

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
		if normalized := NormalizeRunnerPath(path); normalized != "" {
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

			if normalized := NormalizeRunnerPath(path); normalized != "" {
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
			target := NormalizeRunnerPath(obligation.TargetRef)
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

		if ref == "obl-verify-post-write" && !runnerAutonomyVerificationStatusResolvesPostWriteObligation(input.VerificationStatus) {
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
	if a == nil || a.runner == nil || a.runner.runner.Workspace == nil {
		return ""
	}

	return strings.TrimSpace(a.runner.runner.Workspace.Root())
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
		value = NormalizeRunnerPath(value)

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
		TargetRefs:   a.policyPaths(req),
		ChangedPaths: changedPaths,
		Source:       "runner_write_gate",
	})
}

func (a *runnerAutonomyRecorder) emitIntentClassified(req workspace.ToolRequest, inputs ...runnerAutonomyPolicyEventInput) {
	input := runnerAutonomyPolicyEventInput{
		EventType: autonomy.EventToolIntentClassified,
		Source:    "runner_tool_intent",
	}
	if len(inputs) > 0 {
		input = inputs[0]
		if input.EventType == "" {
			input.EventType = autonomy.EventToolIntentClassified
		}
		if strings.TrimSpace(input.Source) == "" {
			input.Source = "runner_tool_intent"
		}
	}
	a.emitPolicyEvent(req, input)
}

func (a *runnerAutonomyRecorder) emitPathRolesResolved(req workspace.ToolRequest, inputs ...runnerAutonomyPolicyEventInput) {
	if a == nil || len(a.policyPaths(req)) == 0 {
		return
	}

	input := runnerAutonomyPolicyEventInput{
		EventType: autonomy.EventPathRolesResolved,
		Source:    "runner_path_roles",
	}
	if len(inputs) > 0 {
		input = inputs[0]
		if input.EventType == "" {
			input.EventType = autonomy.EventPathRolesResolved
		}
		if strings.TrimSpace(input.Source) == "" {
			input.Source = "runner_path_roles"
		}
	}
	a.emitPolicyEvent(req, input)
}

func (a *runnerAutonomyRecorder) emitPolicyResult(req workspace.ToolRequest, input runnerAutonomyPolicyEventInput) {
	if input.EventType == "" {
		input.EventType = autonomy.EventToolPolicyResult
	}

	a.emitPolicyEvent(req, input)

	// Also emit the legacy permission_decision RunEvent so consumers that have not
	// yet migrated to the autonomy event stream continue to receive the signal.
	// The autonomy recorder is now the single source of truth for both events.
	a.emitLegacyPermissionDecision(req, input)
}

// emitLegacyPermissionDecision emits the legacy permission_decision RunEvent,
// deriving its fields from the autonomy policy event data that was already computed.
func (a *runnerAutonomyRecorder) emitLegacyPermissionDecision(req workspace.ToolRequest, input runnerAutonomyPolicyEventInput) {
	if a == nil || a.runner == nil || a.runner.runner.OnEvent == nil {
		return
	}

	decision := permissionDecisionFromAutonomy(input.Decision)
	target := strings.TrimSpace(input.Target)
	if target == "" {
		target = PermissionTarget(req)
	}
	snapshot := PermissionDecisionSnapshot{
		Tool:     strings.TrimSpace(req.Tool),
		Target:   target,
		Decision: decision,
		Rule:     strings.TrimSpace(input.Source),
	}
	a.runner.emitEvent(marshalRunEvent("permission_decision", snapshot))
}

// permissionDecisionFromAutonomy maps an autonomy ToolPolicyDecision to the
// legacy decision value used by the permission_decision RunEvent.
func permissionDecisionFromAutonomy(d autonomy.ToolPolicyDecision) toolpolicy.Decision {
	switch d {
	case autonomy.PolicyAllow, autonomy.PolicyObserve, autonomy.PolicyNudge:
		return toolpolicy.Allow
	case autonomy.PolicyAsk:
		return toolpolicy.Ask
	case autonomy.PolicyDeny, autonomy.PolicyReject:
		return toolpolicy.Deny
	default:
		return toolpolicy.Deny
	}
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

func (a *runnerAutonomyRecorder) emitCompletionGateAttempt(decision autonomy.ToolPolicyDecision, reasonCode string, results []workspace.ToolResult, attempts int) {
	if a == nil {
		return
	}

	completion := implementationCompletionState{}
	if a.runner != nil {
		completion = a.runner.implementationCompletionState(results)
	}
	a.emitGateDecision(runnerAutonomyGateDecision{
		Gate:               autonomy.GateCompletion,
		Decision:           decision,
		InvariantID:        "completion.readiness",
		ReasonCode:         reasonCode,
		ChangedPaths:       completion.ChangedPaths,
		VerificationStatus: runnerAutonomyVerificationStatus(results, a.brief, completion),
		TargetRefs:         runnerAutonomyRequiredPathStatus(a.contract, completion),
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
	modelContractVisible := protocolHash != ""
	effectiveHash := protocolHash
	if string(input.Gate) == completionGateToolName && a.lastStoredProtocolHash != "" {
		if !modelContractVisible {
			effectiveHash = a.lastStoredProtocolHash
			modelContractVisible = true
		}
	}
	friction := autonomy.ContractGuardFrictionEvent{
		Step:                 phase.Step,
		Lane:                 a.contract.Scope.Lane,
		Phase:                phase.Phase,
		PhaseID:              string(phase.PhaseID),
		ModelCallID:          phase.ModelCallID,
		SourceEventID:        sourceEventID,
		ContractVersion:      a.contract.SchemaVersion,
		ProtocolHash:         effectiveHash,
		InvariantID:          input.InvariantID,
		ObligationID:         firstRunnerAutonomyString(gate.OpenObligations),
		Subject:              runnerAutonomyFrictionSubject(input),
		Decision:             decision,
		GuardSurface:         runnerAutonomyFrictionSurface(input.Gate),
		FirstInvalidAction:   runnerAutonomyFrictionInvalidAction(input),
		VisibleActionClasses: runnerAutonomyFrictionVisibleActionClasses(protocol, phase, protocolHash != ""),
		VisibleTools:         runnerAutonomyFrictionVisibleTools(protocol, phase, protocolHash != ""),
		SafeAlternatives:     runnerAutonomyFrictionSafeAlternatives(protocol, phase, protocolHash != ""),
		ModelContractVisible: modelContractVisible,
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
		if len(protocol.SafeAlternatives) > 0 {
			return append([]string(nil), protocol.SafeAlternatives...)
		}
		return recoverySafeActionAlternatives(protocol.CallableActionClasses)
	}

	if alternatives := recoverySafeActionAlternatives(recoveryCardCallableActionClasses(phase, phase.ActionPolicy.CallableToolNames)); len(alternatives) > 0 {
		return alternatives
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

func (a *runnerAutonomyRecorder) emitLoopCloseout(input any) {
	if a == nil {
		return
	}

	if decision, ok := input.(LifecycleCloseoutPolicyDecision); ok {
		if decision.Handled {
			a.emitGateDecision(decision.Gate)
		}
		return
	}

	stopReason, _ := input.(string)
	if decision := lifecycleLoopCloseoutDecision(stopReason, a.hasOpenObligationsForGate(autonomy.GateLoop)); decision.Handled {
		a.emitGateDecision(decision.Gate)
	}
}

func (a *runnerAutonomyRecorder) emitBudgetGate(args ...any) {
	if a == nil {
		return
	}

	if len(args) == 1 {
		if decision, ok := args[0].(LifecycleCloseoutPolicyDecision); ok {
			if decision.Handled {
				a.emitGateDecision(decision.Gate)
			}
			return
		}
	}
	if len(args) != 4 {
		return
	}
	policy, _ := args[0].(RunPolicy)
	metrics, _ := args[1].(runnerMetrics)
	stopReason, _ := args[2].(string)
	exhausted, _ := args[3].(bool)
	if decision := lifecycleBudgetCloseoutDecision(policy, metrics, stopReason, exhausted); decision.Handled {
		a.emitGateDecision(decision.Gate)
	}
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

	protocolHash = textutil.FirstNonEmpty(strings.TrimSpace(protocol.ProtocolHash), strings.TrimSpace(protocolHash))

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
			strings.TrimSpace(string(contract.PhaseID)),
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
		PhaseID:       textutil.FirstNonEmpty(strings.TrimSpace(string(contract.PhaseID)), strings.TrimSpace(protocol.PhaseID)),
		ModelCallID:   strings.TrimSpace(contract.ModelCallID),
		InvariantID:   firstRunnerAutonomyString(contract.InvariantIDs),
		ContextPacket: &packet,
	})
	a.emitModelExchangeEvent(autonomy.ContractModelExchangeEvent{
		Kind:         "trace_store_policy",
		ContractID:   a.contract.ContractID,
		ProtocolHash: protocolHash,
		PhaseID:      textutil.FirstNonEmpty(strings.TrimSpace(string(contract.PhaseID)), strings.TrimSpace(protocol.PhaseID)),
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
			FailureClass: textutil.FirstNonEmpty(runnerAutonomyReasonCode(policy.ErrorClass), runnerAutonomyReasonCode(policy.ReasonCode), strings.TrimSpace(string(policy.Decision))),
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

	protocolHash := textutil.FirstNonEmpty(strings.TrimSpace(gate.ProtocolHash), a.activeProtocolHash())
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
			FailureClass: textutil.FirstNonEmpty(runnerAutonomyReasonCode(input.ReasonCode), string(input.Gate), strings.TrimSpace(string(input.Decision))),
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

	protocolHash := textutil.FirstNonEmpty(strings.TrimSpace(gate.ProtocolHash), a.activeProtocolHash())
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

	protocolHash = textutil.FirstNonEmpty(strings.TrimSpace(protocolHash), a.activeProtocolHash())
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

	event.ContractID = textutil.FirstNonEmpty(strings.TrimSpace(event.ContractID), a.contract.ContractID)
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
		path := textutil.FirstNonEmpty(match.Path, match.MatchedPattern, match.PacketID)
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

	return textutil.FirstNonEmpty(a.currentModelProtocolHash(), strings.TrimSpace(a.lastTurnPhaseEvent.ProtocolHash), a.lastStoredProtocolHash)
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
		PhaseID:              string(phase.PhaseID),
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

func runnerAutonomyParityEvidenceRefs(report ParityReport) []string {
	refs := []string{}
	if report.ContractID != "" {
		refs = append(refs, "parity_contract_id:"+report.ContractID)
	}
	if report.ProtocolHash != "" {
		refs = append(refs, "parity_protocol_hash:"+report.ProtocolHash)
	}
	if report.SurfaceID != "" {
		refs = append(refs, "parity_surface:"+report.SurfaceID)
	}
	if report.InvariantID != "" {
		refs = append(refs, "parity_invariant:"+report.InvariantID)
	}
	if report.OldDecision != "" {
		refs = append(refs, "parity_old_decision:"+report.OldDecision)
	}
	if report.NewDecision != "" {
		refs = append(refs, "parity_new_decision:"+report.NewDecision)
	}
	refs = append(refs, "parity_diverged:"+strconv.FormatBool(report.Diverged))
	if report.DivergenceReason != "" {
		refs = append(refs, "parity_reason:"+report.DivergenceReason)
	}
	return refs
}

func (a *runnerAutonomyRecorder) policyEventForRequest(req workspace.ToolRequest, input runnerAutonomyPolicyEventInput) autonomy.ContractPolicyEvent {
	intent := a.intentForRequest(req, input)
	action := autonomy.ActionFromIntent(intent)
	policy := autonomy.ContractPolicyEvent{
		ToolName:         strings.TrimSpace(req.Tool),
		PackID:           input.PackID,
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
		EvidencePaths:    compactRunnerAutonomyPaths(input.EvidencePaths),
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

func (a *runnerAutonomyRecorder) intentForRequest(req workspace.ToolRequest, input runnerAutonomyPolicyEventInput) autonomy.ToolIntent {
	transport := input.Transport
	if transport == "" {
		transport = autonomy.ToolTransportTextualCall
	}

	intent := autonomy.ToolIntent{
		ToolName:        strings.TrimSpace(req.Tool),
		Capability:      runnerAutonomyPrimaryCapability(a.brief, req.Tool),
		SideEffect:      runnerAutonomySideEffectForBrief(a.brief, req),
		ActionClass:     workspace.ToolRequestActionClass(req),
		Transport:       transport,
		ParserFailure:   input.ParserFailure,
		RepairSource:    input.RepairSource,
		TargetRefs:      a.policyPaths(req),
		ArgumentsDigest: runnerAutonomyArgumentsDigest(req),
		Source:          input.Source,
	}

	return autonomy.CanonicalizeToolIntent(intent)
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

	paths := a.policyPaths(req)
	if len(paths) == 0 {
		return autonomy.PathRoleResolutionSet{}, false
	}

	workspaceRoot := ""
	if a.runner != nil && a.runner.runner.Workspace != nil {
		workspaceRoot = a.runner.runner.Workspace.Root()
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

func runnerAutonomyVerificationStatus(results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief, completion implementationCompletionState) string {
	if runnerBriefRequiresMutation(brief) && workerruntime.VerificationContractSkipsGate(brief.VerificationContract) {
		return "not_required"
	}

	if runnerBriefRequiresMutation(brief) && !workerruntime.VerificationContractSkipsGate(brief.VerificationContract) {
		if completion.VerificationFailed {
			return "failed"
		}

		if completion.VerificationPending || completion.SemanticVerificationPending {
			return "pending"
		}

		if hasSuccessfulVerificationResult(results) {
			return "passed"
		}

		if completion.HasChanges() {
			return "not_required"
		}
	}

	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		observed := workspace.AnalyzeVerificationResult(result)
		if observed.Attempted {
			if !observed.Successful {
				return "failed"
			}

			return "observed"
		}
	}

	return "pending"
}

func runnerAutonomyVerificationStatusResolvesPostWriteObligation(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pass", "passed", "success", "succeeded", "ok", "clean", "verified", "resolved", "observed", "not_required":
		return true
	default:
		return false
	}
}

func runnerAutonomyRequiredPathStatus(contract autonomy.AutonomyContract, completion implementationCompletionState) []string {
	missing := make(map[string]struct{}, len(completion.MissingRequiredPaths))
	for _, path := range completion.MissingRequiredPaths {
		missing[path] = struct{}{}
	}

	var status []string

	for _, role := range contract.PathRoles {
		if role.Role != autonomy.PathRoleRequiredEdit {
			continue
		}

		path := NormalizeRunnerPath(role.PathPattern)
		if path == "" {
			continue
		}

		state := "changed"
		if _, ok := missing[path]; ok || !completion.HasChanges() {
			state = "pending"
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

	if policy.MaxOperationalModelCalls > 0 {
		parts = append(parts, "max_operational_model_calls")
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

func (a *runnerAutonomyRecorder) policyPaths(req workspace.ToolRequest) []string {
	tool := strings.TrimSpace(req.Tool)
	if workspace.IsRuntimeInspectionRequest(req) || languagetools.IsToolName(tool) ||
		tool == workspace.ToolNameGitDiff || tool == workspace.ToolNameGitStatus {

		for _, path := range []string{req.Path, req.Workdir} {
			if trimmed := strings.TrimSpace(path); trimmed != "" {
				return []string{trimmed}
			}
		}

		return nil
	}

	if a == nil || a.runner == nil || a.runner.runner == nil || a.runner.runner.Workspace == nil {
		return nil
	}
	assessment, ok := a.runner.mutationAdmissionForRequest(req)
	if !ok || !assessment.Analysis.Mutation {
		return nil
	}
	return mutationRequestCanonicalTargetIDs(assessment.Analysis)
}

func runnerAutonomySideEffectForBrief(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest) autonomy.SideEffectClass {
	if metadata, ok := runnerAutonomyPlannerToolMetadata(brief, req.Tool); ok {
		return metadata.SideEffect
	}

	return runnerAutonomyFallbackSideEffectForRequest(req)
}

func runnerAutonomyFallbackSideEffectForRequest(req workspace.ToolRequest) autonomy.SideEffectClass {
	tool := strings.TrimSpace(req.Tool)
	switch tool {
	case workspace.ToolNameApplyPatch:
		if effect, ok := runnerAutonomyPatchSideEffect(req.Content); ok {
			return effect
		}

		return autonomy.SideEffectUnknown
	case workspace.ToolNameShell, workspace.ToolNameSourceSanity:
		return workspace.AnalyzeShellCommand(req.Command).SideEffect
	}

	if semanticlanguage.IsDiagnosticToolName(tool) {
		return workspace.AnalyzeShellCommand(req.Command).SideEffect
	}

	if workspace.IsWorkspaceWriteTool(tool) {
		return autonomy.SideEffectAdditive
	}

	if workspace.IsRuntimeInspectionRequest(req) || languagetools.IsToolName(tool) ||
		tool == workspace.ToolNameGitDiff || tool == workspace.ToolNameGitStatus {

		return autonomy.SideEffectReadOnly
	}

	return autonomy.SideEffectUnknown
}

func runnerAutonomyPatchSideEffect(patch string) (autonomy.SideEffectClass, bool) {
	ops, err := patching.Parse(patch)
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

func compactRunnerAutonomyPaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))

	for _, path := range paths {
		path = NormalizeRunnerPath(path)
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

func runnerBriefWithPhaseHints(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, authority planThenExecutePathAuthority, completion implementationCompletionState) workerruntime.WorkerRuntimeBrief {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	brief = runnerBriefWithRequiredEditPathHints(brief, results, authority, completion)

	switch phase := debugLogPhase(brief, results); phase {
	case "latest_log_selection":
		brief.Constraints = textutil.Merge([]string{"latest_log_selection_phase"}, brief.Constraints)
		brief.SearchPolicy = textutil.Merge([]string{
			"no concrete diagnostic-log artifact has been observed yet; permission-authorized inspection remains callable",
		}, brief.SearchPolicy)
	case "latest_log_tail":
		brief.Constraints = textutil.Merge([]string{"latest_log_tail_phase"}, brief.Constraints)
		brief.SearchPolicy = textutil.Merge([]string{
			"a diagnostic-log artifact is selected without a typed diagnostic read; permission-authorized inspection remains callable",
		}, brief.SearchPolicy)
	case "selected_log_analysis":
		brief.Constraints = textutil.Merge([]string{"selected_log_analysis_phase"}, brief.Constraints)
		brief.SearchPolicy = textutil.Merge([]string{
			"typed diagnostic-log evidence is available; permission-authorized inspection remains callable",
		}, brief.SearchPolicy)
	case "failure_window":
		brief.Constraints = textutil.Merge([]string{"failure_window_phase"}, brief.Constraints)
		brief.SearchPolicy = textutil.Merge([]string{
			"a diagnostic-log search hit lacks a covering typed read window; permission-authorized inspection remains callable",
		}, brief.SearchPolicy)
	case "log_analysis":
		brief.Constraints = textutil.Merge([]string{"log_analysis_phase"}, brief.Constraints)
		brief.SearchPolicy = textutil.Merge([]string{"typed diagnostic-log evidence covers the observed failure hit"}, brief.SearchPolicy)
	}

	switch phase := evidenceFirstMutationPhase(brief, results, completion.HasChanges()); phase {
	case "source_evidence":
		brief.Constraints = textutil.Merge([]string{"source_evidence_phase"}, brief.Constraints)
		brief.SearchPolicy = textutil.Merge([]string{
			"no non-target implementation evidence has been observed; permission-authorized inspection remains callable",
		}, brief.SearchPolicy)
	case "target_entry":
		brief.Constraints = textutil.Merge([]string{"first_target_read_pending"}, brief.Constraints)
		brief.SearchPolicy = textutil.Merge([]string{
			"non-target evidence is available and no named-target read is observed; permission-authorized inspection remains callable",
		}, brief.SearchPolicy)
	case "target_inspection":
		brief.Constraints = textutil.Merge([]string{"target_inspection_phase"}, brief.Constraints)
		brief.SearchPolicy = textutil.Merge([]string{
			"named-target evidence is available; permission-authorized inspection remains callable",
		}, brief.SearchPolicy)
	}

	return workerruntime.NormalizeRuntimeBrief(brief)
}

func runnerBriefWithRequiredEditPathHints(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, authority planThenExecutePathAuthority, completion implementationCompletionState) workerruntime.WorkerRuntimeBrief {
	if !runnerBriefRequiresMutation(brief) || len(brief.RequiredEditPaths) == 0 {
		return brief
	}

	if planThenExecutePlannerBrief(brief) {
		if successfulSubmitPlanResultCount(results) > 0 {
			return brief
		}

		brief = clearPlanThenExecuteRequiredSourceHints(brief)
		brief.Constraints = textutil.Merge([]string{"plan_then_execute_submit_plan_pending"}, brief.Constraints)

		unread := planThenExecuteUnreadRequiredPathList(brief, results, authority)

		if len(unread) > 0 {
			readInstruction := fmt.Sprintf("source-body evidence remains pending for required_edit_paths [%s]; choose any permission-authorized inspection that resolves a concrete edit-safety fact, then submit_plan using edits[] for changed paths and source_satisfied_paths for already-final paths, Decision: source-satisfied with per-path evidence, or <<QUESTION>> with the exact path-specific blocker", strings.Join(unread, ", "))

			brief.RequiredNext = textutil.Merge([]string{readInstruction}, brief.RequiredNext)
			brief.InvalidActions = textutil.Merge([]string{
				"submit_plan before every existing required_edit_path has satisfied its source-body obligation",
				"final answer before submit_plan succeeds unless it is a source-satisfied closeout with current-state evidence for every required path",
				"prose final answer while required source evidence is pending",
				"per-edit write tool in plan-execute planner phase",
				"shell/git/source-sanity verification in plan-execute planner phase",
			}, brief.InvalidActions)
			brief.SearchPolicy = textutil.Merge([]string{
				"required_edit_paths plus packet evidence define the submit_plan scope",
				"pending required_edit_paths are advisory evidence targets; all permission-authorized inspection remains callable",
				"outline/search hits orient but do not satisfy required source-body evidence",
				"if current source already matches requested final state, close as source-satisfied instead of submitting stale edits",
			}, brief.SearchPolicy)

			return brief
		}

		state := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, authority)
		if state.Mode == PlanExecutePlanningModeAnchorRefresh && len(state.RefreshPaths) > 0 {
			brief.RequiredNext = textutil.Merge([]string{
				fmt.Sprintf("refresh current source-body evidence for the stale-anchor paths [%s] before corrected submit_plan; choose any permission-authorized inspection that resolves a concrete anchor fact, then submit a corrected plan using anchors copied from current source", strings.Join(state.RefreshPaths, ", ")),
			}, rejectRunnerStateStrings(brief.RequiredNext, planThenExecuteSubmitReadySourceHint))
			brief.Constraints = textutil.Merge([]string{"plan_then_execute_submit_anchor_refresh_required"}, brief.Constraints)
			brief.InvalidActions = textutil.Merge([]string{
				"corrected submit_plan before refreshing the stale or missing edit anchor",
				"final answer, Decision: source-satisfied, or <<QUESTION>> before bounded anchor refresh returns",
				"per-edit write tool in plan-execute planner phase",
				"shell/git/source-sanity verification in plan-execute planner phase",
			}, rejectRunnerStateStrings(brief.InvalidActions, planThenExecuteSubmitReadySourceHint))
			brief.SearchPolicy = textutil.Merge([]string{
				"stale submit_plan anchors require a bounded source-body refresh before corrected submit_plan",
				"refresh only the rejected required path named by the submit_plan guard",
				"after the refresh, use insert_after_line/content or old_text/new_text copied from fresh source; do not reuse stale anchors",
			}, rejectRunnerStateStrings(brief.SearchPolicy, planThenExecuteSubmitReadySourceHint))

			return brief
		}

		brief.RequiredNext = textutil.Merge([]string{
			"submit_plan using edits[] for changed paths and top-level source_satisfied_paths for already-final paths, Decision: source-satisfied with per-path evidence, or <<QUESTION>> with the exact path-specific blocker",
		}, rejectRunnerStateStrings(brief.RequiredNext, planThenExecuteSubmitReadySourceHint))
		brief.Constraints = textutil.Merge([]string{"plan_then_execute_submit_plan_ready"}, brief.Constraints)
		brief.InvalidActions = textutil.Merge([]string{
			"final answer before submit_plan succeeds unless it is a source-satisfied closeout with current-state evidence for every required path",
			"per-edit write tool in plan-execute planner phase",
			"shell/git/source-sanity verification in plan-execute planner phase",
		}, brief.InvalidActions)
		brief.SearchPolicy = textutil.Merge([]string{
			"required_edit_paths plus packet evidence define the submit_plan scope",
			"if current source already matches requested final state, close as source-satisfied instead of submitting stale edits",
			"permission-authorized read/search tools remain callable; submit_plan readiness is advisory until the model chooses a terminal action",
		}, rejectRunnerStateStrings(brief.SearchPolicy, planThenExecuteSubmitReadySourceHint))

		return brief
	}

	changed := make(map[string]struct{}, len(completion.ChangedPaths))
	for _, path := range completion.ChangedPaths {
		changed[path] = struct{}{}
	}
	missing := append([]string(nil), completion.MissingRequiredPaths...)

	if len(changed) == 0 {
		if briefHasUsableEditAnchors(brief) {
			brief.Constraints = textutil.Merge([]string{"implementation_packet_write_ready"}, brief.Constraints)
		}

		if exactAnchorPreWriteSurfaceActive(brief, results, completion.HasChanges()) {
			brief.RequiredNext = textutil.Merge([]string{
				"required-path write capability from the exact target-packet anchor",
				"answer <<QUESTION>> only if the supplied exact anchor is stale or missing",
			}, brief.RequiredNext)
			brief.SearchPolicy = textutil.Merge([]string{
				"required_edit_paths plus exact target-packet anchors establish write readiness; permission-authorized reads remain callable",
			}, brief.SearchPolicy)
		} else {
			brief.RequiredNext = textutil.Merge([]string{
				"resolve a required path with packet evidence, semantic/context/declared project discovery or a narrow required-path source read with named_gap for an exact edit-safety fact, or answer <<QUESTION>> with the exact path-specific blocker",
			}, brief.RequiredNext)
			brief.InvalidActions = textutil.Merge([]string{"task_ledger planning before the first required-path write"}, brief.InvalidActions)
			brief.SearchPolicy = textutil.Merge([]string{
				"required_edit_paths plus packet evidence establish the edit scope; prefer semantic/context/declared project discovery for named edit-safety gaps before raw source reads",
			}, brief.SearchPolicy)
		}

		return brief
	}

	if len(missing) > 0 {
		brief.Constraints = textutil.Merge([]string{"implementation_required_paths_remaining"}, brief.Constraints)
		brief.RequiredNext = textutil.Merge([]string{
			"resolve each remaining required path with an edit, semantic/context/declared project discovery or a narrow required-path source read with named_gap for an exact edit-safety fact, or <<QUESTION>> with the exact blocker for that path",
		}, brief.RequiredNext)
		brief.InvalidActions = textutil.Merge([]string{"final answer while required edit paths remain unchanged"}, brief.InvalidActions)
	}

	return brief
}

func clearPlanThenExecuteRequiredSourceHints(brief workerruntime.WorkerRuntimeBrief) workerruntime.WorkerRuntimeBrief {
	brief.RequiredNext = rejectRunnerStateStrings(brief.RequiredNext, planThenExecuteRequiredSourceHint)
	brief.InvalidActions = rejectRunnerStateStrings(brief.InvalidActions, planThenExecuteRequiredSourceHint)
	brief.SearchPolicy = rejectRunnerStateStrings(brief.SearchPolicy, planThenExecuteRequiredSourceHint)

	return brief
}

func planThenExecuteRequiredSourceHint(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}

	for _, marker := range []string{
		"source-body evidence remains pending for required_edit_paths [",
		"submit_plan before every existing required_edit_path has satisfied its source-body obligation",
		"prose final answer while required source evidence is pending",
		"pending required_edit_paths are advisory evidence targets",
		"outline/search hits orient but do not satisfy required source-body evidence",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}

	return false
}

func planThenExecuteSubmitReadySourceHint(value string) bool {
	if planThenExecuteRequiredSourceHint(value) {
		return true
	}

	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}

	for _, marker := range []string{
		"for existing required paths, satisfy source-body obligations before submit_plan",
		"packet anchors establish scope but existing paths still need source-body obligations satisfied before submit_plan",
		"source-body obligations before submit_plan",
		"use reads only for exact anchor confirmation before submit_plan",
		"raw source reads are only for exact anchor confirmation",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}

	return false
}

func rejectRunnerStateStrings(values []string, reject func(string) bool) []string {
	if len(values) == 0 {
		return nil
	}

	out := make([]string, 0, len(values))
	for _, value := range values {
		if reject != nil && reject(value) {
			continue
		}

		out = append(out, value)
	}

	return out
}

func runnerVisibleBriefForToolSurface(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, completion implementationCompletionState) workerruntime.WorkerRuntimeBrief {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	mutationCommitted := completion.HasChanges()
	if exactAnchorPreWriteSurfaceActive(brief, results, mutationCommitted) {
		return exactAnchorPreWriteRuntimeBrief(brief)
	}

	if implementationTargetSourcePending(brief, results, mutationCommitted) {
		return implementationTargetSourceRuntimeBrief(brief)
	}

	if implementationTargetWriteReadySurfaceActive(brief, results, mutationCommitted) {
		return implementationTargetWriteReadyRuntimeBrief(brief, results, mutationCommitted)
	}

	if implementationScopedEditSurfaceActive(brief, results, completion.MissingRequiredPaths, mutationCommitted) {
		return implementationScopedEditRuntimeBrief(brief)
	}

	if genericReadOnlyEvidencePending(brief, brief.AllowedTools, results) {
		return readOnlyEvidencePendingRuntimeBrief(brief)
	}

	return brief
}

func readOnlyEvidencePendingRuntimeBrief(brief workerruntime.WorkerRuntimeBrief) workerruntime.WorkerRuntimeBrief {
	allowed := genericReadOnlyEvidenceTools(brief.AllowedTools)

	brief.AllowedTools = allowed
	brief.ToolManifest = nil

	if len(allowed) > 0 {
		brief.ToolManifest = workspace.BuildToolManifest(allowed)
	}

	brief.RequiredTools = textutil.Merge([]string{workspace.RequiredToolBoundedWorkspaceEvidence}, brief.RequiredTools)
	brief.ReadinessGate = textutil.Merge([]string{"bounded_workspace_evidence_before_final_answer"}, brief.ReadinessGate)
	brief.RequiredNext = textutil.Merge([]string{
		"gather producer-owned evidence for the user's question with permission-authorized inspection, or report the exact evidence blocker",
	}, brief.RequiredNext)
	brief.InvalidActions = textutil.Merge([]string{
		"prose/final answer before a successful bounded evidence tool result",
		"workspace write",
		"shell/mutating command",
	}, brief.InvalidActions)
	brief.SearchPolicy = textutil.Merge([]string{
		"permission-authorized read, search, context, and discovery capabilities remain callable; final acceptance still requires producer-owned evidence",
	}, brief.SearchPolicy)
	brief.Constraints = textutil.Merge([]string{"read_only_evidence_pending"}, brief.Constraints)

	return workerruntime.NormalizeRuntimeBrief(brief)
}

// briefAuditEvent is the structured payload for the per-turn `brief_audit`
// event. The control_rails event already shows the brief's outward-facing
// shape (lane, allowed tools, search_policy). brief_audit fills in the
// fields control_rails omits — Constraints, RequiredEditSnippets,
// TargetPacket size, ChangedPaths, and the full Readiness block — so a
// post-hoc audit can answer "what evidence and contract did the model
// actually receive?" without re-deriving from the prompt text.
type briefAuditEvent struct {
	Lane                         string   `json:"lane,omitempty"`
	TaskFamily                   string   `json:"task_family,omitempty"`
	Contract                     string   `json:"contract,omitempty"`
	PermissionProfile            string   `json:"permission_profile,omitempty"`
	Constraints                  []string `json:"constraints,omitempty"`
	TargetRefs                   []string `json:"target_refs,omitempty"`
	RequiredEditPaths            []string `json:"required_edit_paths,omitempty"`
	AllowedEditPaths             []string `json:"allowed_edit_paths,omitempty"`
	RequiredEditSnippetsCount    int      `json:"required_edit_snippets_count"`
	RequiredEditSnippetPaths     []string `json:"required_edit_snippet_paths,omitempty"`
	EvidencePaths                []string `json:"evidence_paths,omitempty"`
	ChangedPaths                 []string `json:"changed_paths,omitempty"`
	TargetPacketLines            int      `json:"target_packet_lines"`
	TargetPacketBytes            int      `json:"target_packet_bytes"`
	ReadinessReadyToAnswer       bool     `json:"readiness_ready_to_answer"`
	ReadinessReadyToWrite        bool     `json:"readiness_ready_to_write"`
	ReadinessReadyToVerify       bool     `json:"readiness_ready_to_verify"`
	ReadinessClosedClaims        []string `json:"readiness_closed_claims,omitempty"`
	ReadinessOpenObligations     []string `json:"readiness_open_obligations,omitempty"`
	ReadinessFurtherReadsRequire string   `json:"readiness_further_reads_require,omitempty"`
	ExactAnchorImplActive        bool     `json:"exact_anchor_implementation_active"`
	TargetPacketSupportsWrite    bool     `json:"target_packet_supports_write"`
	WriteReadyReadContract       bool     `json:"write_ready_read_contract"`
	AllowedToolsCount            int      `json:"allowed_tools_count"`
}

// emitBriefAudit emits a one-shot brief_audit event at turn start so an
// audit can see the brief shape that drove the lane (constraints,
// snippets, packet size, readiness) without re-deriving from the prompt.
func (r *runExecution) emitBriefAudit(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) {
	if r == nil || r.runner.OnEvent == nil {
		return
	}

	allowedToolsCount := len(brief.AllowedTools)
	if surface, ok := DirectAnswerSurfaceForBrief(brief); ok {
		allowedToolsCount = len(surface.ToolNames)
	}

	event := briefAuditEvent{
		Lane:                      strings.TrimSpace(brief.Lane),
		TaskFamily:                strings.TrimSpace(brief.TaskFamily),
		Contract:                  strings.TrimSpace(brief.Contract),
		PermissionProfile:         strings.TrimSpace(brief.PermissionProfile),
		Constraints:               append([]string(nil), brief.Constraints...),
		TargetRefs:                append([]string(nil), brief.TargetRefs...),
		RequiredEditPaths:         append([]string(nil), brief.RequiredEditPaths...),
		AllowedEditPaths:          append([]string(nil), brief.AllowedEditPaths...),
		RequiredEditSnippetsCount: len(brief.RequiredEditSnippets),
		EvidencePaths:             append([]string(nil), brief.EvidencePaths...),
		ChangedPaths:              append([]string(nil), brief.ChangedPaths...),
		TargetPacketLines:         len(brief.TargetPacket),
		TargetPacketBytes:         targetPacketByteCount(brief.TargetPacket),
		ExactAnchorImplActive:     exactAnchorImplementationActive(brief),
		TargetPacketSupportsWrite: targetPacketSupportsWrite(brief),
		WriteReadyReadContract:    writeReadyPacketReadContractActive(brief, r.implementationCompletionState(results).HasChanges()),
		AllowedToolsCount:         allowedToolsCount,
	}

	if len(brief.RequiredEditSnippets) > 0 {
		event.RequiredEditSnippetPaths = make([]string, 0, len(brief.RequiredEditSnippets))

		seen := make(map[string]struct{}, len(brief.RequiredEditSnippets))

		for _, snippet := range brief.RequiredEditSnippets {
			path := strings.TrimSpace(snippet.Path)
			if path == "" {
				continue
			}

			if _, ok := seen[path]; ok {
				continue
			}

			seen[path] = struct{}{}
			event.RequiredEditSnippetPaths = append(event.RequiredEditSnippetPaths, path)
		}
	}

	if brief.Readiness != nil {
		event.ReadinessReadyToAnswer = brief.Readiness.ReadyToAnswer
		event.ReadinessReadyToWrite = brief.Readiness.ReadyToWrite
		event.ReadinessReadyToVerify = brief.Readiness.ReadyToVerify
		event.ReadinessClosedClaims = append([]string(nil), brief.Readiness.ClosedClaims...)
		event.ReadinessOpenObligations = append([]string(nil), brief.Readiness.OpenObligations...)
		event.ReadinessFurtherReadsRequire = strings.TrimSpace(brief.Readiness.FurtherReadsRequire)
	}

	r.emitEvent(marshalRunEvent("brief_audit", event))
}

func targetPacketByteCount(packet []string) int {
	total := 0
	for _, line := range packet {
		total += len(line)
		total++
	}

	if total > 0 {
		total--
	}

	return total
}
