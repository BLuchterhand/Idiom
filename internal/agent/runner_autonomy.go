package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"idiom/internal/autonomy"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/util"
	"idiom/internal/workspace"
)

const runnerAutonomyEventRole = "autonomy_event"

type runnerAutonomyRecorder struct {
	runner            *Runner
	contract          autonomy.AutonomyContract
	sequence          int64
	occurredAt        time.Time
	obligations       []autonomy.Obligation
	openObligations   map[string]autonomy.Obligation
	closedObligations map[string]struct{}
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
	ApprovalRequired bool
	Result           string
	ErrorClass       string
	ChangedPaths     []string
	Source           string
}

func newRunnerAutonomyRecorder(r *Runner, policy RunPolicy, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) *runnerAutonomyRecorder {
	if r == nil || r.OnEvent == nil {
		return nil
	}
	contract, obligations, ok := runnerAutonomyContract(r, policy, brief, results)
	if !ok {
		return nil
	}
	return &runnerAutonomyRecorder{
		runner:            r,
		contract:          contract,
		occurredAt:        time.Now().UTC(),
		obligations:       obligations,
		openObligations:   make(map[string]autonomy.Obligation),
		closedObligations: make(map[string]struct{}),
	}
}

func runnerAutonomyContract(r *Runner, policy RunPolicy, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (autonomy.AutonomyContract, []autonomy.Obligation, bool) {
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
		Readiness: runnerAutonomyReadiness(brief),
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
		for _, capability := range runnerAutonomyCapabilitiesForTool(tool) {
			allowedSet[capability] = struct{}{}
		}
	}
	for _, tool := range brief.ForbiddenTools {
		for _, capability := range runnerAutonomyCapabilitiesForTool(tool) {
			forbiddenSet[capability] = struct{}{}
		}
	}
	if strings.EqualFold(strings.TrimSpace(brief.PermissionProfile), "read_only_workspace") {
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

func runnerAutonomyCapabilitiesForTool(tool string) []autonomy.Capability {
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
			FinalAnswerRequirement: util.FirstNonEmpty(strings.TrimSpace(brief.Contract), "direct_answer"),
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

func runnerAutonomyReadiness(brief workerruntime.WorkerRuntimeBrief) autonomy.ReadinessRequirement {
	if brief.Readiness == nil {
		return autonomy.ReadinessRequirement{}
	}
	return autonomy.ReadinessRequirement{
		ReadyToAnswer:       brief.Readiness.ReadyToAnswer,
		ReadyToWrite:        brief.Readiness.ReadyToWrite,
		ReadyToVerify:       brief.Readiness.ReadyToVerify,
		ClosedClaims:        append([]string(nil), brief.Readiness.ClosedClaims...),
		OpenObligationRefs:  append([]string(nil), brief.Readiness.OpenObligations...),
		EvidenceRefs:        append([]string(nil), brief.Readiness.EvidenceSources...),
		FurtherReadsRequire: brief.Readiness.FurtherReadsRequire,
	}
}

func (a *runnerAutonomyRecorder) emitContractStarted(brief workerruntime.WorkerRuntimeBrief) {
	if a == nil {
		return
	}
	a.ensureObligationState()
	a.emitNewEvent(autonomy.NewContractCompiledEvent(a.contract, a.nextTime(), a.nextSequence()))
	for _, obligation := range a.obligations {
		if obligation.Status == autonomy.ObligationOpen {
			a.openObligations[obligation.ObligationID] = obligation
			a.emitNewEvent(autonomy.NewObligationStateEvent(autonomy.EventObligationOpened, a.contract, obligation, a.nextTime(), a.nextSequence()))
		} else {
			a.closedObligations[obligation.ObligationID] = struct{}{}
		}
	}
	if brief.Readiness != nil {
		a.emitNewEvent(autonomy.NewReadinessStateEvent(a.contract, autonomy.ContractReadinessEvent{
			ReadyToAnswer:       brief.Readiness.ReadyToAnswer,
			ReadyToWrite:        brief.Readiness.ReadyToWrite,
			ReadyToVerify:       brief.Readiness.ReadyToVerify,
			ClosedClaims:        append([]string(nil), brief.Readiness.ClosedClaims...),
			OpenObligations:     append([]string(nil), brief.Readiness.OpenObligations...),
			EvidenceRefs:        append([]string(nil), brief.Readiness.EvidenceSources...),
			FurtherReadsRequire: brief.Readiness.FurtherReadsRequire,
		}, a.nextTime(), a.nextSequence()))
	}
}

func (a *runnerAutonomyRecorder) emitGateDecision(input runnerAutonomyGateDecision) {
	if a == nil {
		return
	}
	gate := autonomy.ContractGateEvent{
		Gate:                  input.Gate,
		Decision:              input.Decision,
		InvariantID:           input.InvariantID,
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
		gate.OpenObligations = append([]string(nil), gate.ObligationRefs...)
	}
	gateEvent, err := autonomy.NewGateDecisionEvent(a.contract, gate, a.nextTime(), a.nextSequence())
	a.emitNewEvent(gateEvent, err)
	if err == nil {
		a.emitResolvedObligations(gate.ResolvedObligations, gateEvent.EventID)
	}
	if input.Gate == autonomy.GateCompletion {
		a.emitNewEvent(autonomy.NewCompletionGateDecisionEvent(a.contract, autonomy.ContractCompletionEvent{
			Decision:             input.Decision,
			InvariantID:          input.InvariantID,
			RemainingObligations: gate.OpenObligations,
			VerificationStatus:   input.VerificationStatus,
			ChangedPaths:         input.ChangedPaths,
			RequiredPathStatus:   input.TargetRefs,
		}, a.nextTime(), a.nextSequence()))
	}
}

func (a *runnerAutonomyRecorder) resolvedAndOpenObligationsForGate(input runnerAutonomyGateDecision, obligationRefs []string) (resolved []string, open []string) {
	if a == nil || input.Gate != autonomy.GateWrite {
		return append([]string(nil), obligationRefs...), nil
	}
	return a.writeGateResolvedAndOpenObligations(input.ChangedPaths)
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
		VerificationStatus: runnerAutonomyVerificationStatus(results),
		TargetRefs:         runnerAutonomyRequiredPathStatus(a.contract, results),
		Source:             "runner_completion_gate",
	})
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
}

func (a *runnerAutonomyRecorder) policyEventForRequest(req workspace.ToolRequest, input runnerAutonomyPolicyEventInput) autonomy.ContractPolicyEvent {
	policy := autonomy.ContractPolicyEvent{
		ToolName:         strings.TrimSpace(req.Tool),
		Capability:       runnerAutonomyPrimaryCapability(req.Tool),
		SideEffect:       runnerAutonomySideEffectForRequest(req),
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

func runnerAutonomyVerificationStatus(results []workspace.ToolResult) string {
	if hasSuccessfulVerificationResult(results) {
		return "passed"
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

func runnerAutonomyPrimaryCapability(tool string) autonomy.Capability {
	capabilities := runnerAutonomyCapabilitiesForTool(strings.TrimSpace(tool))
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

func runnerAutonomySideEffectForRequest(req workspace.ToolRequest) autonomy.SideEffectClass {
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
