package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	recoverycontract "idiom/internal/contracts/recovery"

	"idiom/internal/capabilities/workspace"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/config"
	"idiom/internal/platform/textutil"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
	"idiom/internal/workflows/featureplanner"
	"idiom/internal/workflows/implementationdispatch"
	"idiom/internal/workflows/planexecute"
	planexecuteadapters "idiom/internal/workflows/planexecute/adapters"
	"idiom/internal/workflows/recovery"
)

func (m *Model) newPlanExecutePromptRunner(ctx context.Context) planexecute.PromptRunner {
	if m == nil {
		return func(config.Config, string, planexecute.RunOptions, io.Writer) (string, error) {
			return "", fmt.Errorf("plan-execute prompt runner unavailable")
		}
	}

	return func(cfg config.Config, prompt string, opts planexecute.RunOptions, stdout io.Writer) (string, error) {
		restoreRoute := m.applyPlanExecutePromptRunnerRoute(ctx, opts)
		restore := m.applyPlanExecutePromptRunnerState(cfg, opts, stdout)

		priorReasoning := m.lastAssistantReasoningContent
		priorPinnedFiles := m.pinnedFiles
		priorSuppressPromptTranscript := m.suppressPromptTranscript
		priorRequestHistoryOverride := m.requestHistoryOverride
		priorSuppressGreenfield := m.suppressGreenfieldConfirmation
		priorDirectFeaturePlannerSessionID := m.directFeaturePlannerSessionID

		emptyHistory := []llama.ChatMessage(nil)
		m.requestHistoryOverride = &emptyHistory
		m.lastAssistantReasoningContent = ""
		m.pinnedFiles = nil
		m.suppressPromptTranscript = true
		m.suppressGreenfieldConfirmation = true
		m.directFeaturePlannerSessionID = ""

		defer func() {
			m.lastAssistantReasoningContent = priorReasoning
			m.pinnedFiles = priorPinnedFiles
			m.suppressPromptTranscript = priorSuppressPromptTranscript
			m.requestHistoryOverride = priorRequestHistoryOverride
			m.suppressGreenfieldConfirmation = priorSuppressGreenfield
			m.directFeaturePlannerSessionID = priorDirectFeaturePlannerSessionID

			restore()
			restoreRoute()
		}()

		return m.RunPromptOnce(ctx, prompt)
	}
}

func (m *Model) applyPlanExecutePromptRunnerRoute(ctx context.Context, opts planexecute.RunOptions) func() {
	if workerruntime.TaskFamilyIsPlanThenExecutePlanner(opts.TaskFamily) {
		return func() {}
	}

	return m.applyExecutorPromptRoute(ctx, "plan-execute in-process runner", opts.UseActiveModel)
}

func (m *Model) applyExecutorPromptRoute(ctx context.Context, routeLabel string, useActiveModel bool) func() {
	if m == nil || m.implementerClient == nil {
		return func() {}
	}

	routeLabel = strings.TrimSpace(routeLabel)
	if routeLabel == "" {
		routeLabel = "executor route"
	}

	if useActiveModel {
		m.logMessage("dispatch_route", fmt.Sprintf("%s using active model %s for fallback", routeLabel, strings.TrimSpace(m.activeModelDisplayID())))

		return func() {}
	}

	if sameLlamaTarget(m.executorSpec, m.plannerSpec) {
		return func() {}
	}

	if !m.executorPromptRouteReachable(ctx) {
		m.logMessage("dispatch_route", fmt.Sprintf("%s falling back to active model %s because executor model %s is not reachable", routeLabel, strings.TrimSpace(m.activeModelDisplayID()), strings.TrimSpace(resolveModelDisplayID(m.executorSpec, m.executorSpec.Model))))

		return func() {}
	}

	priorClient := m.client
	priorPlannerSpec := m.plannerSpec
	priorModelID := m.modelID
	priorModelDisplayID := m.modelDisplayID
	priorModelThinkingFloor := m.modelThinkingFloor

	m.client = m.implementerClient
	m.plannerSpec = m.executorSpec
	m.modelID = resolveModelID(m.executorSpec, m.executorSpec.Model)
	m.modelDisplayID = resolveModelDisplayID(m.executorSpec, m.executorSpec.Model)
	m.modelThinkingFloor = m.executorSpec.IsHosted() || m.executorSpec.IsMLX()
	m.invalidateComposedPrompt()
	m.logMessage("dispatch_route", fmt.Sprintf("%s using executor model %s", routeLabel, strings.TrimSpace(m.activeModelDisplayID())))

	return func() {
		m.client = priorClient
		m.plannerSpec = priorPlannerSpec
		m.modelID = priorModelID
		m.modelDisplayID = priorModelDisplayID
		m.modelThinkingFloor = priorModelThinkingFloor
		m.invalidateComposedPrompt()
	}
}

func (m *Model) executorPromptRouteReachable(ctx context.Context) bool {
	if m == nil || m.implementerClient == nil {
		return false
	}

	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)

	defer cancel()

	models, err := m.implementerClient.ListModels(probeCtx)

	return err == nil && len(models) > 0
}

func (m *Model) applyPlanExecutePromptRunnerState(cfg config.Config, opts planexecute.RunOptions, stdout io.Writer) func() {
	if m == nil {
		return func() {}
	}

	priorSessionID := m.cfg.Workspace.SessionID
	priorWorkspaceSessionID := ""

	if m.workspace != nil {
		priorWorkspaceSessionID = m.workspace.SessionID()
	}

	priorImplementationDispatch := m.cfg.Chat.ImplementationDispatch
	priorForcedTaskFamily := m.forcedTaskFamily
	priorPlannerOwnedVerification := m.plannerOwnedVerification
	priorMaxTokensOverride := m.maxTokensOverride
	priorDisableThinkingOverride := m.disableThinkingOverride
	priorForcedReadinessOpenObligations := append([]string(nil), m.forcedReadinessOpenObligations...)
	priorEventStream := m.eventStream
	priorPlanExecuteActive := m.planExecuteActive
	priorPlanExecuteScopeContract := m.planExecuteScopeContract
	priorPlanExecuteAuthorizedNewFiles := append([]string(nil), m.planExecuteAuthorizedNewFiles...)
	priorPlanExecuteWriteEnvelope := append([]string(nil), m.planExecuteWriteEnvelope...)
	priorPlanExecuteWriteEnvelopeExplicit := m.planExecuteWriteEnvelopeExplicit
	disableThinking := priorDisableThinkingOverride || opts.DisableThinking ||
		(workerruntime.TaskFamilyIsPlanThenExecutePlanner(opts.TaskFamily) && m.shouldDisableThinkingForPlanExecutePlanner())

	m.planExecuteActive = true
	m.planExecuteScopeContract = opts.ScopeContract.Normalized()
	m.planExecuteAuthorizedNewFiles = appendUniqueBriefStrings(nil, opts.AuthorizedNewFiles...)
	m.cfg.Workspace.SessionID = cfg.Workspace.SessionID

	if m.workspace != nil {
		m.workspace.SetSessionID(cfg.Workspace.SessionID)
	}

	m.cfg.Chat.ImplementationDispatch = ""
	m.SetForcedTaskFamily(opts.TaskFamily)
	m.SetPlannerOwnedVerification(opts.PlannerOwnedVerification)
	m.SetMaxTokensOverride(opts.MaxTokensOverride)
	m.SetDisableThinkingOverride(disableThinking)
	m.SetForcedReadinessOpenObligations(opts.ReviewRepairOpenObligations)

	if opts.JSONEvents && stdout != nil {
		m.SetJSONEventStream(stdout, cfg.Workspace.SessionID)
	}

	return func() {
		m.planExecuteActive = priorPlanExecuteActive
		m.cfg.Workspace.SessionID = priorSessionID

		if m.workspace != nil {
			m.workspace.SetSessionID(priorWorkspaceSessionID)
		}

		m.cfg.Chat.ImplementationDispatch = priorImplementationDispatch
		m.SetForcedTaskFamily(priorForcedTaskFamily)
		m.SetPlannerOwnedVerification(priorPlannerOwnedVerification)
		m.SetMaxTokensOverride(priorMaxTokensOverride)
		m.SetDisableThinkingOverride(priorDisableThinkingOverride)
		m.SetForcedReadinessOpenObligations(priorForcedReadinessOpenObligations)

		m.planExecuteScopeContract = priorPlanExecuteScopeContract
		m.planExecuteAuthorizedNewFiles = priorPlanExecuteAuthorizedNewFiles
		m.planExecuteWriteEnvelope = priorPlanExecuteWriteEnvelope
		m.planExecuteWriteEnvelopeExplicit = priorPlanExecuteWriteEnvelopeExplicit
		m.eventStream = priorEventStream
	}
}

func (m *Model) runImplementationViaPlanExecute(ctx context.Context, prompt string, plan promptExecutionPlan) (string, error) {
	_, _, planDocuments, resolutionBlockers := implementationDispatchEditScopePlanDocumentsAndResolution(prompt, plan, m.cfg)
	if len(resolutionBlockers) > 0 {
		var refs []string

		for _, blocker := range resolutionBlockers {
			refs = append(refs, strings.TrimSpace(blocker.Ref))
		}

		m.logMessage("dispatch_preflight", fmt.Sprintf("blocked plan-execute dispatch for unresolved referenced design document(s): %s", strings.Join(refs, ", ")))

		return implementationdispatch.HumanPlanDocumentResolutionBlockedMessage(resolutionBlockers), nil
	}

	if implementationdispatch.PlanDocumentsShouldPreAtomize(planDocuments) {
		m.logMessage("dispatch_atomize", "referenced design document exceeds single-purpose dispatch limits; materializing atom briefs before plan-execute")

		m.queueUIEvent(infoMsg{text: "Materializing design document atoms before plan-execute..."})

		atomDocuments, err := implementationdispatch.MaterializePlanDocumentAtoms(implementationdispatch.WorkspaceRoot(m.cfg.Workspace.Root, m.cfg.Workspace.CWD), planDocuments)
		if err != nil {
			return "", err
		}

		return m.runAtomizedImplementationPlanDocumentsViaPlanExecute(ctx, prompt, plan, atomDocuments)
	}

	return m.runImplementationPlanDocumentsViaPlanExecute(ctx, prompt, plan, planDocuments)
}

func (m *Model) runImplementationPlanDocumentsViaPlanExecute(ctx context.Context, prompt string, plan promptExecutionPlan, planDocuments []implementationdispatch.PlanDocument) (string, error) {
	planDocuments = implementationdispatch.PlanDocumentsWithDirtyAllowance(prompt, planDocuments)
	root := implementationdispatch.WorkspaceRoot(m.cfg.Workspace.Root, m.cfg.Workspace.CWD)

	requiredPaths, allowedPaths := implementationDispatchEditScopeFromPlanDocuments(prompt, plan, m.cfg, planDocuments)

	if closed := implementationdispatch.PlanDocumentsTerminalAtoms(planDocuments); len(closed) == len(planDocuments) && len(closed) > 0 {
		m.logMessage("dispatch_source_satisfied", fmt.Sprintf("skipping plan-execute for %d closed design-doc atom(s)", len(closed)))

		return implementationdispatch.HumanTerminalAtomMessage(closed), nil
	}

	if satisfied := implementationdispatch.PlanDocumentsSourceSatisfied(root, planDocuments); len(satisfied) == len(planDocuments) && len(satisfied) > 0 {
		m.logMessage("dispatch_source_satisfied", fmt.Sprintf("skipping plan-execute for %d already-satisfied design-doc atom(s)", len(satisfied)))
		implementationdispatch.MarkSourceSatisfiedAtomDocuments(root, satisfied)

		return implementationdispatch.HumanSourceSatisfiedMessage(satisfied), nil
	}

	if implementationdispatch.PlanDocumentsNeedRefineBeforeDispatch(planDocuments) {
		m.logMessage("dispatch_refine", "materialized design-doc atom requires refine before plan-execute")

		m.queueUIEvent(infoMsg{text: "Refining materialized design-doc atom before plan-execute..."})

		var refineResults []implementationdispatch.PlanDocumentRefineResult

		refinedPlanDocuments := append([]implementationdispatch.PlanDocument(nil), planDocuments...)

		for _, doc := range planDocuments {
			if !implementationdispatch.IsAtomizedPlanDocumentPath(doc.Path) {
				continue
			}

			refined := planDocumentRefineRunner(ctx, m.cfg, doc)

			refineResults = append(refineResults, refined)

			if !refined.OK {
				if len(refined.PostPreflightBlockers) > 0 {
					sourceSatisfiedRecords := implementationdispatch.PreflightSourceSatisfiedRecords(refined.PostPreflightBlockers)
					if implementationdispatch.PreflightRecordsAllSourceSatisfied(sourceSatisfiedRecords) {
						m.logMessage("dispatch_source_satisfied", fmt.Sprintf("refined atom candidate classified %d atom(s) as source-satisfied", len(refined.PostPreflightBlockers)))
						implementationdispatch.MarkPreflightSourceSatisfiedAtomDocuments(root, sourceSatisfiedRecords)

						return implementationdispatch.HumanPreflightSourceSatisfiedMessage(sourceSatisfiedRecords), nil
					}

					m.logMessage("dispatch_refine", fmt.Sprintf("refined atom candidate still blocked by preflight for %s", strings.TrimSpace(refined.OutputPath)))

					return implementationdispatch.HumanPlanDocumentRefinePostPreflightBlockedMessage(refineResults, refined.PostPreflightBlockers), nil
				}

				if implementationDispatchRefinedDocumentIsDispatchable(ctx, m.cfg, refined.Document) {
					m.logMessage("dispatch_refine", fmt.Sprintf("using structurally dispatchable refined atom %s despite nonzero refine exit", strings.TrimSpace(refined.OutputPath)))

					refinedPlanDocuments = implementationdispatch.ReplacePlanDocument(refinedPlanDocuments, doc.Path, refined.Document)

					continue
				}

				m.logMessage("dispatch_refine", fmt.Sprintf("refine failed for %s (code=%d output=%s)", doc.Path, refined.ExitCode, strings.TrimSpace(refined.OutputPath)))

				return implementationdispatch.HumanPlanDocumentRefineFailedMessage(nil, refineResults), nil
			}

			m.logMessage("dispatch_refine", fmt.Sprintf("refined %s to %s", doc.Path, refined.OutputPath))

			refinedPlanDocuments = implementationdispatch.ReplacePlanDocument(refinedPlanDocuments, doc.Path, refined.Document)
		}

		planDocuments = refinedPlanDocuments
		requiredPaths, allowedPaths = implementationDispatchEditScopeFromPlanDocuments(prompt, plan, m.cfg, planDocuments)

		if refinedBlocked := implementationdispatch.PreflightPlanDocuments(ctx, implementationdispatch.PlanDocumentsPreflightInput{
			WorkspaceRoot: m.cfg.Workspace.Root,
			WorkspaceCWD:  m.cfg.Workspace.CWD,
			PlanDocuments: planDocuments,
			Runner:        planDocumentPreflightRunner,
		}); len(refinedBlocked) > 0 {
			sourceSatisfiedRecords := implementationdispatch.PreflightSourceSatisfiedRecords(refinedBlocked)
			if implementationdispatch.PreflightRecordsAllSourceSatisfied(sourceSatisfiedRecords) {
				m.logMessage("dispatch_source_satisfied", fmt.Sprintf("refined atom preflight classified %d atom(s) as source-satisfied", len(refinedBlocked)))
				implementationdispatch.MarkPreflightSourceSatisfiedAtomDocuments(root, sourceSatisfiedRecords)

				return implementationdispatch.HumanPreflightSourceSatisfiedMessage(sourceSatisfiedRecords), nil
			}

			m.logMessage("dispatch_refine", "refined atom brief still blocked by preflight")

			return implementationdispatch.HumanPlanDocumentRefinePostPreflightBlockedMessage(refineResults, refinedBlocked), nil
		}
	}

	if blocked := implementationdispatch.PreflightPlanDocuments(ctx, implementationdispatch.PlanDocumentsPreflightInput{
		WorkspaceRoot: m.cfg.Workspace.Root,
		WorkspaceCWD:  m.cfg.Workspace.CWD,
		PlanDocuments: planDocuments,
		Runner:        planDocumentPreflightRunner,
	}); len(blocked) > 0 {
		sourceSatisfiedRecords := implementationdispatch.PreflightSourceSatisfiedRecords(blocked)
		if implementationdispatch.PreflightRecordsAllSourceSatisfied(sourceSatisfiedRecords) {
			m.logMessage("dispatch_source_satisfied", fmt.Sprintf("preflight classified %d atom(s) as source-satisfied", len(blocked)))
			implementationdispatch.MarkPreflightSourceSatisfiedAtomDocuments(root, sourceSatisfiedRecords)

			return implementationdispatch.HumanPreflightSourceSatisfiedMessage(sourceSatisfiedRecords), nil
		}

		if implementationdispatch.PreflightNeedsAtomization(blocked) && implementationdispatch.CanAtomizePlanDocuments(planDocuments) {
			m.logMessage("dispatch_atomize", "preflight requested sub-atoms; materializing atom briefs before plan-execute")

			m.queueUIEvent(infoMsg{text: "Materializing design document atoms before plan-execute..."})

			atomDocuments, err := implementationdispatch.MaterializePlanDocumentAtoms(implementationdispatch.WorkspaceRoot(m.cfg.Workspace.Root, m.cfg.Workspace.CWD), planDocuments)
			if err != nil {
				return "", err
			}

			return m.runAtomizedImplementationPlanDocumentsViaPlanExecute(ctx, prompt, plan, atomDocuments)
		}

		if implementationdispatch.PreflightCanRefine(blocked) {
			m.logMessage("dispatch_refine", "preflight found non-dispatchable plan document shape; running refine before plan-execute")

			m.queueUIEvent(infoMsg{text: "Refining referenced plan document before plan-execute..."})

			var refineResults []implementationdispatch.PlanDocumentRefineResult

			refinedPlanDocuments := append([]implementationdispatch.PlanDocument(nil), planDocuments...)

			for _, result := range blocked {
				docPath := strings.TrimSpace(result.Path)

				var original implementationdispatch.PlanDocument

				found := false

				for _, doc := range planDocuments {
					if doc.Path == docPath {
						original = doc
						found = true

						break
					}
				}

				if !found {
					continue
				}

				refined := planDocumentRefineRunner(ctx, m.cfg, original)

				refineResults = append(refineResults, refined)

				if !refined.OK {
					if len(refined.PostPreflightBlockers) > 0 {
						sourceSatisfiedRecords := implementationdispatch.PreflightSourceSatisfiedRecords(refined.PostPreflightBlockers)
						if implementationdispatch.PreflightRecordsAllSourceSatisfied(sourceSatisfiedRecords) {
							m.logMessage("dispatch_source_satisfied", fmt.Sprintf("refined candidate classified %d atom(s) as source-satisfied", len(refined.PostPreflightBlockers)))
							implementationdispatch.MarkPreflightSourceSatisfiedAtomDocuments(root, sourceSatisfiedRecords)

							return implementationdispatch.HumanPreflightSourceSatisfiedMessage(sourceSatisfiedRecords), nil
						}

						m.logMessage("dispatch_refine", fmt.Sprintf("refined candidate still blocked by preflight for %s", strings.TrimSpace(refined.OutputPath)))

						return implementationdispatch.HumanPlanDocumentRefinePostPreflightBlockedMessage(refineResults, refined.PostPreflightBlockers), nil
					}

					if implementationDispatchRefinedDocumentIsDispatchable(ctx, m.cfg, refined.Document) {
						m.logMessage("dispatch_refine", fmt.Sprintf("using structurally dispatchable refined brief %s despite nonzero refine exit", strings.TrimSpace(refined.OutputPath)))

						refinedPlanDocuments = implementationdispatch.ReplacePlanDocument(refinedPlanDocuments, docPath, refined.Document)

						continue
					}

					m.logMessage("dispatch_refine", fmt.Sprintf("refine failed for %s (code=%d output=%s)", docPath, refined.ExitCode, strings.TrimSpace(refined.OutputPath)))

					return implementationdispatch.HumanPlanDocumentRefineFailedMessage(blocked, refineResults), nil
				}

				m.logMessage("dispatch_refine", fmt.Sprintf("refined %s to %s", docPath, refined.OutputPath))

				refinedPlanDocuments = implementationdispatch.ReplacePlanDocument(refinedPlanDocuments, docPath, refined.Document)
			}

			planDocuments = refinedPlanDocuments
			requiredPaths, allowedPaths = implementationDispatchEditScopeFromPlanDocuments(prompt, plan, m.cfg, planDocuments)

			if refinedBlocked := implementationdispatch.PreflightPlanDocuments(ctx, implementationdispatch.PlanDocumentsPreflightInput{
				WorkspaceRoot: m.cfg.Workspace.Root,
				WorkspaceCWD:  m.cfg.Workspace.CWD,
				PlanDocuments: planDocuments,
				Runner:        planDocumentPreflightRunner,
			}); len(refinedBlocked) > 0 {
				sourceSatisfiedRecords := implementationdispatch.PreflightSourceSatisfiedRecords(refinedBlocked)
				if implementationdispatch.PreflightRecordsAllSourceSatisfied(sourceSatisfiedRecords) {
					m.logMessage("dispatch_source_satisfied", fmt.Sprintf("refined preflight classified %d atom(s) as source-satisfied", len(refinedBlocked)))
					implementationdispatch.MarkPreflightSourceSatisfiedAtomDocuments(root, sourceSatisfiedRecords)

					return implementationdispatch.HumanPreflightSourceSatisfiedMessage(sourceSatisfiedRecords), nil
				}

				if implementationdispatch.PreflightNeedsAtomization(refinedBlocked) && implementationdispatch.CanAtomizePlanDocuments(planDocuments) {
					m.logMessage("dispatch_atomize", "refined brief still exceeds single-purpose limits; materializing atom briefs before plan-execute")

					m.queueUIEvent(infoMsg{text: "Materializing refined design document atoms before plan-execute..."})

					atomDocuments, err := implementationdispatch.MaterializePlanDocumentAtoms(implementationdispatch.WorkspaceRoot(m.cfg.Workspace.Root, m.cfg.Workspace.CWD), planDocuments)
					if err != nil {
						return "", err
					}

					return m.runAtomizedImplementationPlanDocumentsViaPlanExecute(ctx, prompt, plan, atomDocuments)
				}

				m.logMessage("dispatch_refine", "refined brief still blocked by preflight")

				return implementationdispatch.HumanPlanDocumentRefinePostPreflightBlockedMessage(refineResults, refinedBlocked), nil
			}
		} else {
			var paths []string

			for _, result := range blocked {
				paths = append(paths, strings.TrimSpace(result.Path))
			}

			m.logMessage("dispatch_preflight", fmt.Sprintf("blocked plan-execute dispatch for referenced plan document(s): %s", strings.Join(paths, ", ")))

			return implementationdispatch.HumanPlanDocumentPreflightBlockedMessage(blocked), nil
		}
	}

	if len(requiredPaths) == 0 && len(allowedPaths) == 0 && len(planDocuments) == 0 {
		if persistedScope, ok := m.persistedPlanExecuteScopeForContinuation(prompt); ok {
			m.logMessage("dispatch_scope", fmt.Sprintf("executing recovered plan-execute continuation from progress state %s", persistedScope.StatePath))

			return m.runPersistedPlanExecuteContinuation(ctx, prompt, plan, persistedScope)
		}
	}

	return m.runSingleImplementationPlanExecute(ctx, prompt, plan, requiredPaths, allowedPaths, planDocuments)
}

func implementationDispatchRefinedDocumentIsDispatchable(ctx context.Context, cfg config.Config, doc implementationdispatch.PlanDocument) bool {
	if strings.TrimSpace(doc.Path) == "" || strings.TrimSpace(doc.Content) == "" {
		return false
	}

	return len(implementationdispatch.PreflightPlanDocuments(ctx, implementationdispatch.PlanDocumentsPreflightInput{
		WorkspaceRoot: cfg.Workspace.Root,
		WorkspaceCWD:  cfg.Workspace.CWD,
		PlanDocuments: []implementationdispatch.PlanDocument{doc},
		Runner:        planDocumentPreflightRunner,
	})) == 0
}

func (m *Model) runAtomizedImplementationPlanDocumentsViaPlanExecute(ctx context.Context, prompt string, plan promptExecutionPlan, atomDocuments []implementationdispatch.PlanDocument) (string, error) {
	if len(atomDocuments) == 0 {
		return "Plan-execute dispatch blocked before the planner ran.\n\nThe referenced design document needed sub-atoms, but no dispatchable atom briefs could be materialized.", nil
	}

	atomDocuments = implementationdispatch.PlanDocumentsWithDirtyAllowance(prompt, atomDocuments)
	m.logMessage("dispatch_atomize", fmt.Sprintf("dispatching %d materialized atom brief(s) through plan-execute", len(atomDocuments)))

	var (
		summaries []string
		skipped   []implementationdispatch.TerminalAtomRecord
	)
	root := implementationdispatch.WorkspaceRoot(m.cfg.Workspace.Root, m.cfg.Workspace.CWD)

	for i, atomDoc := range atomDocuments {
		if status, ok := implementationdispatch.PlanDocumentTerminalAtomStatus(atomDoc); ok {
			skipped = append(skipped, implementationdispatch.TerminalAtomRecord{DocumentPath: atomDoc.Path, Status: status})
			summaries = append(summaries, fmt.Sprintf("Atom %d/%d `%s` skipped: %s", i+1, len(atomDocuments), atomDoc.Path, status))

			continue
		}

		atomPrompt := prompt
		if len(atomDocuments) > 1 {
			atomPrompt = fmt.Sprintf("%s\n\nDispatch atom %d/%d: %s", strings.TrimSpace(prompt), i+1, len(atomDocuments), atomDoc.Path)
		}

		atomPlan := plan

		atomPlan.runtimeBrief.RequiredEditPaths = nil

		answer, err := m.runImplementationPlanDocumentsViaPlanExecute(ctx, atomPrompt, atomPlan, []implementationdispatch.PlanDocument{atomDoc})
		if err != nil {
			return "", err
		}

		result := m.lastPlanExecuteRunResult
		if implementationdispatch.PlanExecuteResultBlocksAtomCompletion(result) ||
			(result == nil && implementationdispatch.AnswerLooksBlocked(answer)) {
			return answer, nil
		}

		if pendingCommands := implementationdispatch.UnprovenVerificationCommands(atomDoc, answer); len(pendingCommands) > 0 {
			return implementationdispatch.HumanPendingVerificationCommandMessage(atomDoc, pendingCommands), nil
		}

		implementationdispatch.MarkCompletedAtomDocumentForResult(root, atomDoc, answer, result)

		summaries = append(summaries, fmt.Sprintf("Atom %d/%d `%s`:\n%s", i+1, len(atomDocuments), atomDoc.Path, strings.TrimSpace(answer)))
	}

	var b strings.Builder

	if len(skipped) == len(atomDocuments) {
		b.WriteString(fmt.Sprintf("Design document implementation already closed across %d atom(s).", len(atomDocuments)))
	} else {
		b.WriteString(fmt.Sprintf("Design document implementation completed through %d atom(s).", len(atomDocuments)-len(skipped)))

		if len(skipped) > 0 {
			b.WriteString(fmt.Sprintf(" Skipped %d already-closed atom(s).", len(skipped)))
		}
	}

	if obligations := implementationdispatch.TestObligationsFromDocuments(atomDocuments); len(obligations) > 0 {
		b.WriteString("\n\nDesign-doc test obligations tracked:")

		for _, obligation := range obligations {
			b.WriteString("\n- ")
			b.WriteString(obligation)
		}
	}

	for _, summary := range summaries {
		b.WriteString("\n\n")
		b.WriteString(summary)
	}

	return strings.TrimSpace(b.String()), nil
}

func (m *Model) runPersistedPlanExecuteContinuation(ctx context.Context, prompt string, plan promptExecutionPlan, scope planexecute.ContinuationScope) (string, error) {
	allow, blanket, ok := planExecuteApprovalDecisionFromOperatorInput(prompt)
	if !ok || !allow {
		return "", fmt.Errorf("plan-execute continuation requires operator approval input")
	}

	if blanket {
		m.planExecuteApproveAllEdits = true
	}

	requiredPaths := appendUniqueBriefStrings(nil, scope.RequiredEditPaths...)
	authorizedNewFiles := appendUniqueBriefStrings(plan.runtimeBrief.AuthorizedNewFiles, scope.AuthorizedNewFiles...)
	planExecuteDeps := planexecuteadapters.Dependencies()

	if len(requiredPaths) > 0 {
		m.recordRollbackSnapshotsForPaths(requiredPaths)
	}

	opts := planexecute.Options{
		Context:               ctx,
		Prompt:                prompt,
		Model:                 m.implementationDispatchPlanExecuteModelOverride(),
		Resume:                scope.SessionID,
		SessionID:             scope.SessionID,
		RequiredEditPaths:     requiredPaths,
		ScopeContract:         plancontract.NewPlanScopeContract(requiredPaths, nil, nil),
		ScopeExtractionPolicy: planexecute.ScopeExtractionFrozen,
		AuthorizedNewFiles:    authorizedNewFiles,
		MaxTokens:             implementationdispatch.PlanExecuteMaxTokens(prompt, requiredPaths, nil, nil),
		OnPhase: func(phase, detail string) {
			m.queueUIEvent(planPhaseMsg{phase: phase, detail: detail})
		},
		OnEditProgress: func(step, total int, path string, succeeded bool) {
			if succeeded {
				m.attachWriteToSubmittedEdit(path, fmt.Sprintf("pe-%d", step-1))
			}

			m.queueUIEvent(planRefreshMsg{})

			label := "succeeded"
			if !succeeded {
				label = "failed"
			}

			m.queueUIEvent(planPhaseMsg{phase: "executing", detail: fmt.Sprintf("Edit %d/%d %s (%s)", step, total, filepath.Base(path), label)})
		},
		OnPlanApproval: func(approvalCtx context.Context, request planexecute.PlanApprovalRequest) planexecute.PlanApprovalDecision {
			if request.Phase == planexecute.PlanApprovalPhaseInitial {
				return planexecute.PlanApprovalDecision{
					Approved:      true,
					Blanket:       blanket,
					ApprovedPaths: append([]string(nil), request.Paths...),
					ApprovedBy:    "operator",
				}
			}

			return m.requestPlanExecuteApproval(approvalCtx, request)
		},
	}

	promptRun := m.newPlanExecutePromptRunner(ctx)

	m.logMessage("dispatch", "invoking plan-execute continuation in-process")
	m.queueUIEvent(infoMsg{text: "Executing approved plan-execute continuation..."})

	var outBuf, errBuf bytes.Buffer

	m.beginPlanExecuteApprovalSession()
	defer m.endPlanExecuteApprovalSession()

	result := planexecute.RunExecutorContinuationWithDependencies(m.cfg, opts, promptRun, nil, &outBuf, &errBuf, planExecuteDeps)

	m.lastPlanExecuteRunResult = &result

	m.logMessage("dispatch_result", fmt.Sprintf("plan-execute continuation finished (code=%d session=%s state=%s stdout=%d stderr=%d)",
		result.ExitCode, result.SessionID, result.ResumeStatePath, outBuf.Len(), errBuf.Len()))

	if result.ExitCode != 0 {
		if msg, ok := m.humanPlanExecuteMissingNewFileAuthorizationMessage(result, authorizedNewFiles); ok {
			return msg, nil
		}

		if msg, ok := humanPlanExecuteExecutionRecoveryOpenMessage(m.cfg.Workspace.Root, result, requiredPaths); ok {
			return msg, nil
		}

		return "", fmt.Errorf("plan-execute continuation exited %d (session=%s state=%s): %s", result.ExitCode, result.SessionID, result.ResumeStatePath, strings.TrimSpace(errBuf.String()))
	}

	answer := strings.TrimSpace(outBuf.String())
	if strings.TrimSpace(result.ResumeStatePath) != "" && !strings.Contains(strings.ToLower(answer), "progress_state:") {
		answer = strings.TrimSpace(answer + "\nprogress_state: " + strings.TrimSpace(result.ResumeStatePath))
	}

	return answer, nil
}

func (m *Model) runSingleImplementationPlanExecute(ctx context.Context, prompt string, plan promptExecutionPlan, requiredPaths []string, allowedPaths []string, planDocuments []implementationdispatch.PlanDocument) (string, error) {
	brief := composeImplementationBriefWithEditScopeAndPlanDocuments(prompt, plan, m.cfg, requiredPaths, allowedPaths, planDocuments)
	root := implementationdispatch.WorkspaceRoot(m.cfg.Workspace.Root, m.cfg.Workspace.CWD)
	intake := implementationDispatchCompilePlanExecuteScopeIntake(prompt, plan, m.cfg)
	authorizedNewFiles := appendUniqueBriefStrings(
		implementationdispatch.AuthorizedNewFiles(prompt, planDocuments),
		intake.AuthorizedNewFiles...,
	)

	var contextPackLog bytes.Buffer

	planExecuteDeps := planexecuteadapters.Dependencies()
	contextPackSection := planexecute.BuildPromptContextPackSectionWithRunner(m.cfg, planexecute.PromptContextPackOptions{
		Context:           ctx,
		Mode:              "plan-execute",
		Title:             prompt,
		Brief:             brief,
		ProjectRoot:       root,
		RequiredEditPaths: requiredPaths,
	}, &contextPackLog, planExecuteDeps.RunPromptContextTool)

	if diag := strings.TrimSpace(contextPackLog.String()); diag != "" {
		m.logMessage("dispatch_context", textutil.TruncateWithByteCount(diag, 500))
	}

	if strings.TrimSpace(contextPackSection) != "" {
		m.logMessage("dispatch_context", "preloaded context_pack for in-process plan-execute dispatch")
	}

	if len(requiredPaths) == 0 && len(allowedPaths) == 0 && strings.TrimSpace(contextPackSection) != "" {
		discoveredPaths := m.implementationDispatchAllowedPathsFromPreloadedContextPack(prompt, contextPackSection)
		if len(discoveredPaths) > 0 && intake.ClosedScope {
			m.logMessage("scope_expansion", implementationDispatchFormatScopeExpansionEvent(plancontract.ScopeExpansionEvent{
				Kind:   plancontract.ScopeExpansionEventExpansionBlocked,
				Policy: plancontract.ScopeExpansionPolicyClosed,
				Reason: "closed scope blocks context_pack candidate expansion",
				Paths:  implementationDispatchCandidateExpansionRecords(discoveredPaths, "context_pack"),
			}))
		} else if len(discoveredPaths) > 0 {
			m.logMessage("scope_expansion", implementationDispatchFormatScopeExpansionEvent(plancontract.ScopeExpansionEvent{
				Kind:   plancontract.ScopeExpansionEventProposalCreated,
				Policy: plancontract.ScopeExpansionPolicyBoundedDiscovery,
				Reason: "context_pack proposed candidate scope for unscoped plan-execute request",
				Paths:  implementationDispatchCandidateExpansionRecords(discoveredPaths, "context_pack"),
			}))
			m.logMessage("scope_expansion", implementationDispatchFormatScopeExpansionEvent(plancontract.ScopeExpansionEvent{
				Kind:   plancontract.ScopeExpansionEventProposalApproved,
				Policy: plancontract.ScopeExpansionPolicyBoundedDiscovery,
				Source: "harness_bounded_discovery",
				Reason: "no explicit or closed scope was present",
				Paths:  implementationDispatchCandidateExpansionRecords(discoveredPaths, "context_pack"),
			}))
			allowedPaths = discoveredPaths
			m.logMessage("dispatch_scope", fmt.Sprintf("derived %d candidate edit path(s) from preloaded context_pack", len(allowedPaths)))

			brief = composeImplementationBriefWithEditScopeAndPlanDocuments(prompt, plan, m.cfg, requiredPaths, allowedPaths, planDocuments)
		} else if !intake.ClosedScope {
			allowedPaths = m.resolveUnscopedImplementationAllowedPaths(ctx, prompt)
			allowedPaths = uniqueImplementationDispatchPaths(allowedPaths)

			if len(allowedPaths) > 0 {
				m.logMessage("scope_expansion", implementationDispatchFormatScopeExpansionEvent(plancontract.ScopeExpansionEvent{
					Kind:   plancontract.ScopeExpansionEventProposalCreated,
					Policy: plancontract.ScopeExpansionPolicyBoundedDiscovery,
					Reason: "fallback discovery proposed candidate scope for unscoped plan-execute request",
					Paths:  implementationDispatchCandidateExpansionRecords(allowedPaths, "fallback_discovery"),
				}))
				m.logMessage("scope_expansion", implementationDispatchFormatScopeExpansionEvent(plancontract.ScopeExpansionEvent{
					Kind:   plancontract.ScopeExpansionEventProposalApproved,
					Policy: plancontract.ScopeExpansionPolicyBoundedDiscovery,
					Source: "harness_bounded_discovery",
					Reason: "no explicit or closed scope was present",
					Paths:  implementationDispatchCandidateExpansionRecords(allowedPaths, "fallback_discovery"),
				}))
				m.logMessage("dispatch_scope", fmt.Sprintf("derived %d candidate edit path(s) from fallback discovery after context_pack scope was not relevant", len(allowedPaths)))

				brief = composeImplementationBriefWithEditScopeAndPlanDocuments(prompt, plan, m.cfg, requiredPaths, allowedPaths, planDocuments)
			}
		} else {
			m.logMessage("scope_expansion", implementationDispatchFormatScopeExpansionEvent(plancontract.ScopeExpansionEvent{
				Kind:   plancontract.ScopeExpansionEventExpansionBlocked,
				Policy: plancontract.ScopeExpansionPolicyClosed,
				Reason: "closed scope blocks fallback candidate discovery",
			}))
		}
	}

	brief = planexecute.ComposePlannerPromptWithContextPack(brief, contextPackSection)

	if len(requiredPaths) > 0 {
		m.recordRollbackSnapshotsForPaths(requiredPaths)
		m.logMessage("dispatch_scope", fmt.Sprintf("derived %d required edit path(s) for plan-execute dispatch", len(requiredPaths)))
	} else if len(allowedPaths) > 0 {
		m.recordRollbackSnapshotsForPaths(allowedPaths)
		m.logMessage("dispatch_scope", fmt.Sprintf("derived %d candidate edit path(s) from referenced plan document", len(allowedPaths)))
	}

	var editSetObligations plancontract.EditSetObligations

	scopeContract := plancontract.NewPlanScopeContract(requiredPaths, allowedPaths, editSetObligations)

	if m.planExecuteWriteEnvelopeExplicit {
		scopeContract = scopeContract.WithWriteAllowedPaths(m.planExecuteWriteEnvelope)
	} else if len(requiredPaths) > 0 || len(allowedPaths) > 0 {
		scopeContract = scopeContract.WithWriteAllowedPaths(append(append([]string{}, requiredPaths...), allowedPaths...))
	}
	scopeExtractionPolicy := planexecute.ScopeExtractionFromPrompt
	if scopeContract.HasContent() {
		scopeExtractionPolicy = planexecute.ScopeExtractionFrozen
	}

	opts := planexecute.Options{
		Context:               ctx,
		Prompt:                brief,
		Model:                 m.implementationDispatchPlanExecuteModelOverride(),
		SessionID:             featureplanner.PlanExecuteSessionIDFromImplementorPrompt(prompt),
		RequiredEditPaths:     requiredPaths,
		AllowedEditPaths:      allowedPaths,
		EditSetObligations:    editSetObligations,
		ScopeContract:         scopeContract,
		ScopeExtractionPolicy: scopeExtractionPolicy,
		AuthorizedNewFiles:    authorizedNewFiles,
		MaxTokens:             implementationdispatch.PlanExecuteMaxTokens(prompt, requiredPaths, allowedPaths, planDocuments),
		OnPhase: func(phase, detail string) {
			m.queueUIEvent(planPhaseMsg{phase: phase, detail: detail})
		},
		OnEditProgress: func(step, total int, path string, succeeded bool) {
			if succeeded {
				m.attachWriteToSubmittedEdit(path, fmt.Sprintf("pe-%d", step-1))
			}

			m.queueUIEvent(planRefreshMsg{})

			label := "succeeded"
			if !succeeded {
				label = "failed"
			}

			m.queueUIEvent(planPhaseMsg{phase: "executing", detail: fmt.Sprintf("Edit %d/%d %s (%s)", step, total, filepath.Base(path), label)})
		},
		OnPlanApproval: m.requestPlanExecuteApproval,
	}
	promptRun := m.newPlanExecutePromptRunner(ctx)

	m.logMessage("dispatch", "invoking plan-execute in-process")

	m.queueUIEvent(infoMsg{text: "Dispatching to plan-execute..."})

	var outBuf, errBuf bytes.Buffer

	m.beginPlanExecuteApprovalSession()
	defer m.endPlanExecuteApprovalSession()

	result := planexecute.RunWithDependencies(m.cfg, opts, promptRun, nil, &outBuf, &errBuf, planExecuteDeps)

	m.lastPlanExecuteRunResult = &result

	m.logMessage("dispatch_result", fmt.Sprintf("plan-execute finished (code=%d session=%s state=%s stdout=%d stderr=%d)",
		result.ExitCode, result.SessionID, result.ResumeStatePath, outBuf.Len(), errBuf.Len()))

	if result.ExitCode != 0 {
		if msg, ok := m.humanPlanExecuteMissingNewFileAuthorizationMessage(result, authorizedNewFiles); ok {
			return msg, nil
		}

		if msg, ok := humanPlanExecuteExecutionRecoveryOpenMessage(m.cfg.Workspace.Root, result, requiredPaths); ok {
			return msg, nil
		}

		if recovery.PlanExecuteFailureCanUseRecovery(implementationDispatchPlanExecuteRunFailureKind(result)) {
			answer, handled, err := m.handlePlanExecutePlannerBlocked(ctx, implementationDispatchPlanExecuteFailure{
				Prompt:        prompt,
				Plan:          plan,
				Brief:         brief,
				Options:       opts,
				RequiredPaths: requiredPaths,
				AllowedPaths:  allowedPaths,
				Result:        result,
				Stdout:        outBuf.String(),
				Stderr:        errBuf.String(),
			})
			if handled || err != nil {
				return answer, err
			}
		}

		return "", fmt.Errorf("plan-execute exited %d (session=%s state=%s): %s", result.ExitCode, result.SessionID, result.ResumeStatePath, strings.TrimSpace(errBuf.String()))
	}

	answer := strings.TrimSpace(outBuf.String())
	if strings.TrimSpace(result.ResumeStatePath) != "" && !strings.Contains(strings.ToLower(answer), "progress_state:") {
		answer = strings.TrimSpace(answer + "\nprogress_state: " + strings.TrimSpace(result.ResumeStatePath))
	}

	return answer, nil
}

func (m *Model) implementationDispatchPlanExecuteModelOverride() string {
	if m == nil || !m.plannerSpec.IsHosted() {
		return ""
	}

	return strings.TrimSpace(textutil.FirstNonEmpty(
		m.modelID,
		resolveModelID(m.plannerSpec, m.plannerSpec.Model),
		m.plannerSpec.Model,
		m.cfg.LlamaServer.Model,
	))
}

func humanPlanExecuteExecutionRecoveryOpenMessage(root string, result planexecute.RunResult, fallbackRequiredPaths []string) (string, bool) {
	switch strings.TrimSpace(result.FailureKind) {
	case string(plancontract.PlanExecuteFailureSourceSanityFailed), string(plancontract.PlanExecuteFailureVerificationFailed):
	default:
		return "", false
	}

	state, _, found, _ := planexecute.TryLoadProgressState(root, result.SessionID)
	requiredPaths := appendUniqueBriefStrings(nil, fallbackRequiredPaths...)

	if found && len(state.RequiredEditPaths) > 0 {
		requiredPaths = appendUniqueBriefStrings(nil, state.RequiredEditPaths...)
	}

	var b strings.Builder

	b.WriteString("plan-execute: recovery_open\n")
	fmt.Fprintf(&b, "session_id: %s\n", strings.TrimSpace(result.SessionID))

	if strings.TrimSpace(result.ResumeStatePath) != "" {
		fmt.Fprintf(&b, "progress_state: %s\n", strings.TrimSpace(result.ResumeStatePath))
	}

	fmt.Fprintf(&b, "failure_kind: %s\n", strings.TrimSpace(result.FailureKind))

	if reason := strings.TrimSpace(result.FailureReason); reason != "" {
		fmt.Fprintf(&b, "reason: %s\n", reason)
	}

	if len(requiredPaths) > 0 {
		b.WriteString("required_edit_paths:\n")

		for _, path := range requiredPaths {
			fmt.Fprintf(&b, "- %s\n", path)
		}
	}

	if found {
		if len(state.Executor.SourceSanity) > 0 {
			b.WriteString("source_sanity:\n")

			for _, check := range state.Executor.SourceSanity {
				fmt.Fprintf(&b, "- %s %s %s\n", strings.TrimSpace(check.Path), strings.TrimSpace(string(check.Status)), strings.TrimSpace(firstNonEmpty(check.Error, check.Summary)))
			}
		}

		if len(state.Executor.Verification) > 0 {
			b.WriteString("verification:\n")

			for _, check := range state.Executor.Verification {
				fmt.Fprintf(&b, "- %s %s %s\n", strings.TrimSpace(check.Command), strings.TrimSpace(string(check.Status)), strings.TrimSpace(firstNonEmpty(check.Error, check.Summary)))
			}
		}
	}

	b.WriteString("next: inspect the progress state and recover the generated source before dispatching sibling atoms\n")

	return strings.TrimSpace(b.String()), true
}

func implementationDispatchPlanExecuteRunFailureKind(result planexecute.RunResult) plancontract.PlanExecuteFailureKind {
	failureKind, _ := plancontract.ParsePlanExecuteFailureKind(result.FailureKind)
	return failureKind
}

func humanPlanExecuteNewFileAuthorizationRequiredMessage(paths []string) string {
	paths = uniqueImplementationDispatchPaths(paths)
	if len(paths) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("Plan-execute needs approval before creating new files.\n\n")
	b.WriteString("Proposed new files:\n")
	for _, path := range paths {
		fmt.Fprintf(&b, "- %s\n", path)
	}

	b.WriteString("\nReply with approval using this typed authority shape, revise the request to use existing files, or reject it:\n\n")
	b.WriteString("```json\n")
	b.WriteString("{\"authorized_new_files\":[")
	for i, path := range paths {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "{\"path\":%q}", path)
	}
	b.WriteString("]}\n")
	b.WriteString("```")

	return strings.TrimSpace(b.String())
}

func (m *Model) humanPlanExecuteMissingNewFileAuthorizationMessage(result planexecute.RunResult, authorizedNewFiles []string) (string, bool) {
	if implementationDispatchPlanExecuteRunFailureKind(result) != plancontract.PlanExecuteFailureMissingEditPathWithoutNewFileAuth {
		return "", false
	}

	root := implementationdispatch.WorkspaceRoot(m.cfg.Workspace.Root, m.cfg.Workspace.CWD)
	state, _, found, _ := planexecute.TryLoadProgressState(root, result.SessionID)
	if !found {
		return "", false
	}

	missing, err := planexecute.MissingNewFileAuthorizationPaths(m.cfg, state, authorizedNewFiles, planexecuteadapters.Dependencies())
	if err != nil {
		m.logMessage("dispatch_scope", fmt.Sprintf("plan-execute missing new-file authorization detail unavailable: %v", err))
		return "", false
	}
	if len(missing) == 0 {
		return "", false
	}

	m.logMessage("dispatch_scope", fmt.Sprintf("plan-execute result requires new-file authorization for %s", strings.Join(missing, ", ")))

	return humanPlanExecuteNewFileAuthorizationRequiredMessage(missing), true
}

type implementationDispatchPlanExecuteFailure struct {
	Prompt                   string
	Plan                     promptExecutionPlan
	Brief                    string
	Options                  planexecute.Options
	RequiredPaths            []string
	AllowedPaths             []string
	Result                   planexecute.RunResult
	Stdout                   string
	Stderr                   string
	PlannerBlockedChainDepth int
}

func implementationDispatchOptionsAlreadyCarriedEditSetObligation(opts planexecute.Options, kind plancontract.EditSetObligationKind) bool {
	return implementationDispatchEditSetObligationHasNonPromptSource(opts.EditSetObligations, kind) ||
		implementationDispatchEditSetObligationHasNonPromptSource(opts.ScopeContract.EditSetObligations, kind)
}

func implementationDispatchEditSetObligationHasNonPromptSource(obligations plancontract.EditSetObligations, kind plancontract.EditSetObligationKind) bool {
	for _, obligation := range obligations.Normalized() {
		if obligation.Kind != kind {
			continue
		}

		source := strings.TrimSpace(obligation.Source)

		return source == "" || source != "prompt"
	}

	return false
}

func implementationDispatchShouldChainMissingEditRecovery(result planexecute.RunResult) bool {
	_, ok := plancontract.PlanExecuteFailureKindEditSetObligation(implementationDispatchPlanExecuteRunFailureKind(result))
	return ok
}

type implementationDispatchMissingEditRetrySpec struct {
	failureKind          plancontract.PlanExecuteFailureKind
	obligationKind       plancontract.EditSetObligationKind
	allowedPaths         func(*Model, implementationDispatchPlanExecuteFailure) []string
	appendAddendum       func(string, []string) string
	alreadyCarriedLog    string
	alreadyCarriedReason string
	noScopeLog           string
	noScopeReason        string
	retryLog             string
	retryInfo            string
	resultLogLabel       string
	chainLogPrefix       string
	executorFailedReason string
	finalReason          string
}

func (m *Model) handlePlanExecutePlannerBlocked(ctx context.Context, failure implementationDispatchPlanExecuteFailure) (string, bool, error) {
	const maxPlannerBlockedChainDepth = 3

	if failure.PlannerBlockedChainDepth >= maxPlannerBlockedChainDepth {
		m.logMessage("dispatch_recovery", fmt.Sprintf("planner_blocked chain depth %d reached limit %d; surfacing to user", failure.PlannerBlockedChainDepth, maxPlannerBlockedChainDepth))

		return humanPlanExecutePlannerBlockedMessage(failure, recovery.PlanExecuteRecoveryDecision{
			Decision: "surface",
			Reason:   fmt.Sprintf("plan-execute retry chain depth %d exceeded; surfacing blocker to user", failure.PlannerBlockedChainDepth),
		}), true, nil
	}

	if answer, ok := humanPlanExecuteSourceSatisfiedCoverageBlockerMessage(failure); ok {
		m.logMessage("dispatch_recovery", "surfacing source-satisfied coverage mismatch without retry")

		return answer, true, nil
	}

	for _, spec := range []implementationDispatchMissingEditRetrySpec{
		{
			failureKind:    plancontract.PlanExecuteFailureMissingRequestedTestEdit,
			obligationKind: plancontract.EditSetObligationKindTestEdit,
			allowedPaths: func(m *Model, failure implementationDispatchPlanExecuteFailure) []string {
				return m.implementationDispatchAllowedPathsForMissingRequestedTestEdit(failure)
			},
			appendAddendum:       recovery.AppendMissingRequestedTestEditRecoveryAddendum,
			alreadyCarriedLog:    "surfacing missing test-edit rejection after typed test obligation was already carried",
			alreadyCarriedReason: "test-edit obligation was already explicit, but the retry still did not produce a valid test edit",
			noScopeLog:           "surfacing missing test-edit rejection without retry because no concrete test scope was available",
			noScopeReason:        "typed test-edit obligation is unsatisfied, but no concrete test path was available for retry",
			retryLog:             "retrying plan-execute after missing requested test edit with candidate scope: %s",
			retryInfo:            "Retrying plan-execute with required test edit obligation...",
			resultLogLabel:       "missing-test",
			chainLogPrefix:       "missing-test",
			executorFailedReason: "automatic missing-test retry submitted a plan, but the executor rejected an edit",
			finalReason:          "automatic missing-test retry did not produce a valid test edit plan",
		},
		{
			failureKind:    plancontract.PlanExecuteFailureMissingRequestedImplementationEdit,
			obligationKind: plancontract.EditSetObligationKindImplementationEdit,
			allowedPaths: func(m *Model, failure implementationDispatchPlanExecuteFailure) []string {
				return m.implementationDispatchAllowedPathsForMissingRequestedImplementationEdit(failure)
			},
			appendAddendum:       recovery.AppendMissingRequestedImplementationEditRecoveryAddendum,
			alreadyCarriedLog:    "surfacing missing implementation-edit rejection after typed implementation obligation was already carried",
			alreadyCarriedReason: "implementation-edit obligation was already explicit, but the retry still did not produce a valid implementation edit",
			noScopeLog:           "surfacing missing implementation-edit rejection without retry because no concrete source scope was available",
			noScopeReason:        "typed implementation-edit obligation is unsatisfied, but no concrete source path was available for retry",
			retryLog:             "retrying plan-execute after missing requested implementation edit with candidate scope: %s",
			retryInfo:            "Retrying plan-execute with required implementation edit obligation...",
			resultLogLabel:       "missing-implementation",
			chainLogPrefix:       "missing-implementation",
			executorFailedReason: "automatic missing-implementation retry submitted a plan, but the executor rejected an edit",
			finalReason:          "automatic missing-implementation retry did not produce a valid implementation edit plan",
		},
	} {
		if implementationDispatchPlanExecuteRunFailureKind(failure.Result) == spec.failureKind {
			return m.handlePlanExecuteMissingEditRetry(ctx, failure, spec)
		}
	}

	if !m.planExecuteFailureHasPlanExecuteHandoff(failure) {
		allowedPaths := implementationDispatchAllowedPathsFromContextPack(failure.Options.Prompt, 8)
		if len(allowedPaths) > 0 {
			allowedPaths = uniqueImplementationDispatchPaths(allowedPaths)
			m.logMessage("dispatch_recovery", fmt.Sprintf("using context-pack candidate scope for planner-blocked retry: %s", strings.Join(allowedPaths, ", ")))
		} else {
			allowedPaths = m.resolveUnscopedImplementationAllowedPaths(ctx, failure.Prompt)
		}

		if len(allowedPaths) == 0 {
			m.logMessage("dispatch_recovery", "surfacing planner blocker without retry because plan-execute dispatch has no concrete edit scope")

			return humanPlanExecuteDispatchWithoutScopeMessage(failure), true, nil
		}

		retryOptions := implementationDispatchRetryOptionsWithCandidateScope(failure.Options, allowedPaths)
		retryOptions.Prompt = recovery.AppendDiscoveredCandidateScopeAddendum(failure.Options.Prompt, allowedPaths)
		m.logMessage("dispatch_recovery", fmt.Sprintf("retrying plan-execute with discovered candidate scope: %s", strings.Join(allowedPaths, ", ")))

		m.queueUIEvent(infoMsg{text: "Retrying plan-execute with discovered candidate scope..."})

		var retryOut, retryErr bytes.Buffer

		retryOptions.Context = ctx

		retryResult := planexecute.RunWithDependencies(m.cfg, retryOptions, m.newPlanExecutePromptRunner(ctx), nil, &retryOut, &retryErr, planexecuteadapters.Dependencies())
		m.lastPlanExecuteRunResult = &retryResult
		m.logMessage("dispatch_result", fmt.Sprintf("plan-execute discovered-scope retry finished (code=%d session=%s state=%s stdout=%d stderr=%d)",
			retryResult.ExitCode, retryResult.SessionID, retryResult.ResumeStatePath, retryOut.Len(), retryErr.Len()))

		if retryResult.ExitCode == 0 {
			return strings.TrimSpace(retryOut.String()), true, nil
		}

		retryFailure := failure

		retryFailure.AllowedPaths = allowedPaths
		retryFailure.Options = retryOptions
		retryFailure.Result = retryResult
		retryFailure.Stdout = retryOut.String()
		retryFailure.Stderr = retryErr.String()

		if implementationDispatchShouldChainMissingEditRecovery(retryResult) {
			m.logMessage("dispatch_recovery", fmt.Sprintf("discovered-scope retry produced %s; chaining missing-edit recovery", strings.TrimSpace(retryResult.FailureKind)))

			retryFailure.PlannerBlockedChainDepth = failure.PlannerBlockedChainDepth + 1

			return m.handlePlanExecutePlannerBlocked(ctx, retryFailure)
		}

		return humanPlanExecutePlannerBlockedMessage(retryFailure, recovery.PlanExecuteRecoveryDecision{
			Decision: "surface",
			Reason:   "automatic discovered-scope retry did not produce a plan",
		}), true, nil
	}

	if path, ok := planExecuteRequiredSourceReadBlockerPath(failure); ok {
		decision := recovery.PlanExecuteRecoveryDecision{
			Decision:      "retry",
			Reason:        "planner blocker names an unread required source path that a retry can satisfy with a source-body read",
			RetryAddendum: fmt.Sprintf("First read source for `%s` using a source-body route, then submit the plan when every required path has source evidence.", path),
		}
		retryPrompt := appendPlanExecutePlannerRecoveryAddendum(failure.Brief, failure, decision, "")
		retryOptions := failure.Options

		retryOptions.Prompt = retryPrompt

		m.logMessage("dispatch_recovery", fmt.Sprintf("planner_blocked decision=retry reason=required source read blocker for %s", path))

		m.queueUIEvent(infoMsg{text: "Retrying plan-execute after required source-read blocker..."})

		var retryOut, retryErr bytes.Buffer

		retryOptions.Context = ctx

		retryResult := planexecute.RunWithDependencies(m.cfg, retryOptions, m.newPlanExecutePromptRunner(ctx), nil, &retryOut, &retryErr, planexecuteadapters.Dependencies())
		m.lastPlanExecuteRunResult = &retryResult
		m.logMessage("dispatch_result", fmt.Sprintf("plan-execute source-read retry finished (code=%d session=%s state=%s stdout=%d stderr=%d)",
			retryResult.ExitCode, retryResult.SessionID, retryResult.ResumeStatePath, retryOut.Len(), retryErr.Len()))

		if retryResult.ExitCode == 0 {
			return strings.TrimSpace(retryOut.String()), true, nil
		}

		retryFailure := failure

		retryFailure.Result = retryResult
		retryFailure.Options = retryOptions
		retryFailure.Stdout = retryOut.String()
		retryFailure.Stderr = retryErr.String()

		if implementationDispatchShouldChainMissingEditRecovery(retryResult) {
			m.logMessage("dispatch_recovery", fmt.Sprintf("source-read retry produced %s; chaining missing-edit recovery", strings.TrimSpace(retryResult.FailureKind)))

			retryFailure.PlannerBlockedChainDepth = failure.PlannerBlockedChainDepth + 1

			return m.handlePlanExecutePlannerBlocked(ctx, retryFailure)
		}

		if recovery.PlanExecutePlannerBlockedCanRetryOnActiveModel(implementationDispatchPlanExecuteRetryInput(retryFailure)) {
			m.logMessage("dispatch_recovery", "source-read retry hit repeated submit_plan gate; chaining to active-model fallback")

			retryFailure.PlannerBlockedChainDepth = failure.PlannerBlockedChainDepth + 1

			return m.handlePlanExecutePlannerBlocked(ctx, retryFailure)
		}

		return humanPlanExecutePlannerBlockedMessage(retryFailure, recovery.PlanExecuteRecoveryDecision{
			Decision: "surface",
			Reason:   "automatic source-read retry did not produce a plan",
		}), true, nil
	}

	if recovery.PlanExecutePlannerBlockedCanRetryOnActiveModel(implementationDispatchPlanExecuteRetryInput(failure)) {
		retryOptions := failure.Options

		retryOptions.UseActiveModel = true
		retryOptions.Prompt = recovery.AppendPlanExecuteActiveModelFallbackAddendum(failure.Options.Prompt)

		m.logMessage("dispatch_recovery", "retrying plan-execute on active model after previous planner attempt failed to produce a valid submit_plan")

		m.queueUIEvent(infoMsg{text: "Retrying plan-execute on active model..."})

		var retryOut, retryErr bytes.Buffer

		retryOptions.Context = ctx

		retryResult := planexecute.RunWithDependencies(m.cfg, retryOptions, m.newPlanExecutePromptRunner(ctx), nil, &retryOut, &retryErr, planexecuteadapters.Dependencies())
		m.lastPlanExecuteRunResult = &retryResult
		m.logMessage("dispatch_result", fmt.Sprintf("plan-execute active-model retry finished (code=%d session=%s state=%s stdout=%d stderr=%d)",
			retryResult.ExitCode, retryResult.SessionID, retryResult.ResumeStatePath, retryOut.Len(), retryErr.Len()))

		if retryResult.ExitCode == 0 {
			return strings.TrimSpace(retryOut.String()), true, nil
		}

		retryFailure := failure

		retryFailure.Options = retryOptions
		retryFailure.Result = retryResult
		retryFailure.Stdout = retryOut.String()
		retryFailure.Stderr = retryErr.String()

		if implementationDispatchShouldChainMissingEditRecovery(retryResult) {
			m.logMessage("dispatch_recovery", fmt.Sprintf("active-model retry produced %s; chaining missing-edit recovery", strings.TrimSpace(retryResult.FailureKind)))

			retryFailure.PlannerBlockedChainDepth = failure.PlannerBlockedChainDepth + 1

			return m.handlePlanExecutePlannerBlocked(ctx, retryFailure)
		}

		return humanPlanExecutePlannerBlockedMessage(retryFailure, recovery.PlanExecuteRecoveryDecision{
			Decision: "surface",
			Reason:   "active-model fallback did not produce a plan",
		}), true, nil
	}

	decision, raw, err := m.decidePlanExecutePlannerBlocked(ctx, failure)
	if err != nil {
		m.logMessage("dispatch_recovery", "recovery decision failed; surfacing planner blocker: "+err.Error())

		return humanPlanExecutePlannerBlockedMessage(failure, recovery.PlanExecuteRecoveryDecision{
			Decision: "surface",
			Reason:   "recovery decision failed: " + err.Error(),
		}), true, nil
	}

	m.logMessage("dispatch_recovery", fmt.Sprintf("planner_blocked decision=%s reason=%s", strings.TrimSpace(decision.Decision), textutil.TruncateWithByteCount(strings.TrimSpace(decision.Reason), 240)))

	if textutil.EqualTrimFold(decision.Decision, "retry") {
		retryPrompt := appendPlanExecutePlannerRecoveryAddendum(failure.Brief, failure, decision, raw)
		retryOptions := failure.Options

		retryOptions.Prompt = retryPrompt

		m.logMessage("dispatch_recovery", "retrying plan-execute after planner_blocked decision")

		m.queueUIEvent(infoMsg{text: "Retrying plan-execute after planner recovery decision..."})

		var retryOut, retryErr bytes.Buffer

		retryOptions.Context = ctx

		retryResult := planexecute.RunWithDependencies(m.cfg, retryOptions, m.newPlanExecutePromptRunner(ctx), nil, &retryOut, &retryErr, planexecuteadapters.Dependencies())
		m.lastPlanExecuteRunResult = &retryResult
		m.logMessage("dispatch_result", fmt.Sprintf("plan-execute retry finished (code=%d session=%s state=%s stdout=%d stderr=%d)",
			retryResult.ExitCode, retryResult.SessionID, retryResult.ResumeStatePath, retryOut.Len(), retryErr.Len()))

		if retryResult.ExitCode == 0 {
			return strings.TrimSpace(retryOut.String()), true, nil
		}

		retryFailure := failure

		retryFailure.Result = retryResult
		retryFailure.Options = retryOptions
		retryFailure.Stdout = retryOut.String()
		retryFailure.Stderr = retryErr.String()

		if implementationDispatchShouldChainMissingEditRecovery(retryResult) {
			m.logMessage("dispatch_recovery", fmt.Sprintf("planner-blocked retry produced %s; chaining missing-edit recovery", strings.TrimSpace(retryResult.FailureKind)))

			retryFailure.PlannerBlockedChainDepth = failure.PlannerBlockedChainDepth + 1

			return m.handlePlanExecutePlannerBlocked(ctx, retryFailure)
		}

		if recovery.PlanExecutePlannerBlockedCanRetryOnActiveModel(implementationDispatchPlanExecuteRetryInput(retryFailure)) {
			m.logMessage("dispatch_recovery", "planner-blocked retry hit repeated submit_plan gate; chaining to active-model fallback")

			retryFailure.PlannerBlockedChainDepth = failure.PlannerBlockedChainDepth + 1

			return m.handlePlanExecutePlannerBlocked(ctx, retryFailure)
		}

		return humanPlanExecutePlannerBlockedMessage(retryFailure, recovery.PlanExecuteRecoveryDecision{
			Decision: "surface",
			Reason:   "automatic retry did not produce a plan",
			Message:  strings.TrimSpace(decision.Message),
		}), true, nil
	}

	return humanPlanExecutePlannerBlockedMessage(failure, decision), true, nil
}

func (m *Model) handlePlanExecuteMissingEditRetry(ctx context.Context, failure implementationDispatchPlanExecuteFailure, spec implementationDispatchMissingEditRetrySpec) (string, bool, error) {
	if implementationDispatchOptionsAlreadyCarriedEditSetObligation(failure.Options, spec.obligationKind) {
		m.logMessage("dispatch_recovery", spec.alreadyCarriedLog)

		return humanPlanExecutePlannerBlockedMessage(failure, recovery.PlanExecuteRecoveryDecision{
			Decision: "surface",
			Reason:   spec.alreadyCarriedReason,
		}), true, nil
	}

	allowedPaths := spec.allowedPaths(m, failure)
	if len(allowedPaths) == 0 {
		m.logMessage("dispatch_recovery", spec.noScopeLog)

		return humanPlanExecutePlannerBlockedMessage(failure, recovery.PlanExecuteRecoveryDecision{
			Decision: "surface",
			Reason:   spec.noScopeReason,
		}), true, nil
	}

	retryOptions := failure.Options

	retryOptions = implementationDispatchRetryOptionsWithCandidateScope(retryOptions, allowedPaths)
	retryOptions = implementationDispatchRetryOptionsWithEditSetObligation(retryOptions, spec.obligationKind)
	retryOptions.Prompt = spec.appendAddendum(failure.Options.Prompt, allowedPaths)
	m.logMessage("dispatch_recovery", fmt.Sprintf(spec.retryLog, strings.Join(allowedPaths, ", ")))

	m.queueUIEvent(infoMsg{text: spec.retryInfo})

	var retryOut, retryErr bytes.Buffer

	retryOptions.Context = ctx

	retryResult := planexecute.RunWithDependencies(m.cfg, retryOptions, m.newPlanExecutePromptRunner(ctx), nil, &retryOut, &retryErr, planexecuteadapters.Dependencies())
	m.lastPlanExecuteRunResult = &retryResult
	m.logMessage("dispatch_result", fmt.Sprintf("plan-execute %s retry finished (code=%d session=%s state=%s stdout=%d stderr=%d)",
		spec.resultLogLabel, retryResult.ExitCode, retryResult.SessionID, retryResult.ResumeStatePath, retryOut.Len(), retryErr.Len()))

	if retryResult.ExitCode == 0 {
		return strings.TrimSpace(retryOut.String()), true, nil
	}

	retryFailure := failure

	retryFailure.AllowedPaths = allowedPaths
	retryFailure.Options = retryOptions
	retryFailure.Result = retryResult
	retryFailure.Stdout = retryOut.String()
	retryFailure.Stderr = retryErr.String()

	if implementationDispatchShouldChainMissingEditRecovery(retryResult) {
		m.logMessage("dispatch_recovery", fmt.Sprintf("%s retry produced %s; chaining missing-edit recovery", spec.chainLogPrefix, strings.TrimSpace(retryResult.FailureKind)))

		retryFailure.PlannerBlockedChainDepth = failure.PlannerBlockedChainDepth + 1

		return m.handlePlanExecutePlannerBlocked(ctx, retryFailure)
	}

	if implementationDispatchPlanExecuteRunFailureKind(retryResult) == plancontract.PlanExecuteFailureExecutorFailed {
		return humanPlanExecutePlannerBlockedMessage(retryFailure, recovery.PlanExecuteRecoveryDecision{
			Decision: "surface",
			Reason:   spec.executorFailedReason,
		}), true, nil
	}

	return humanPlanExecutePlannerBlockedMessage(retryFailure, recovery.PlanExecuteRecoveryDecision{
		Decision: "surface",
		Reason:   spec.finalReason,
	}), true, nil
}

func implementationDispatchPlanExecuteRetryInput(failure implementationDispatchPlanExecuteFailure) recovery.PlanExecutePlannerBlockedRetryInput {
	retrySignalInput := recovery.PlanExecuteRetrySignalInput{
		FailureKind:   implementationDispatchPlanExecuteRunFailureKind(failure.Result),
		FailureReason: failure.Result.FailureReason,
		PlannerAnswer: failure.Result.PlannerAnswer,
		Stdout:        failure.Stdout,
		Stderr:        failure.Stderr,
	}
	return recovery.PlanExecutePlannerBlockedRetryInput{
		FailureKind:    retrySignalInput.FailureKind,
		FailureReason:  failure.Result.FailureReason,
		PlannerAnswer:  failure.Result.PlannerAnswer,
		Stdout:         failure.Stdout,
		Stderr:         failure.Stderr,
		RequiredPaths:  append([]string(nil), failure.RequiredPaths...),
		UseActiveModel: failure.Options.UseActiveModel,
		RetrySignals:   recovery.ClassifyPlanExecuteRetrySignals(retrySignalInput),
	}
}

func implementationDispatchRetryOptionsWithCandidateScope(opts planexecute.Options, candidatePaths []string) planexecute.Options {
	writeEnvelope, writeEnvelopeExplicit := implementationDispatchRetryWriteEnvelope(opts)
	opts.RequiredEditPaths = uniqueImplementationDispatchPaths(opts.RequiredEditPaths)
	opts.AllowedEditPaths = uniqueImplementationDispatchPaths(append(append([]string{}, opts.AllowedEditPaths...), candidatePaths...))
	opts.ScopeContract = plancontract.NewPlanScopeContract([]string(opts.RequiredEditPaths), []string(opts.AllowedEditPaths), opts.EditSetObligations)
	if writeEnvelopeExplicit {
		opts.ScopeContract = opts.ScopeContract.WithWriteAllowedPaths(writeEnvelope)
	}
	if opts.ScopeContract.HasContent() {
		opts.ScopeExtractionPolicy = planexecute.ScopeExtractionFrozen
	}

	return opts
}

func implementationDispatchRetryOptionsWithEditSetObligation(opts planexecute.Options, kind plancontract.EditSetObligationKind) planexecute.Options {
	writeEnvelope, writeEnvelopeExplicit := implementationDispatchRetryWriteEnvelope(opts)
	opts.EditSetObligations = implementationDispatchEditSetObligationsWithSource(opts.EditSetObligations, kind, "tui_recovery")
	opts.ScopeContract = plancontract.NewPlanScopeContract([]string(opts.RequiredEditPaths), []string(opts.AllowedEditPaths), opts.EditSetObligations)
	if writeEnvelopeExplicit {
		opts.ScopeContract = opts.ScopeContract.WithWriteAllowedPaths(writeEnvelope)
	}
	if opts.ScopeContract.HasContent() {
		opts.ScopeExtractionPolicy = planexecute.ScopeExtractionFrozen
	}

	return opts
}

func implementationDispatchRetryWriteEnvelope(opts planexecute.Options) ([]string, bool) {
	scope := opts.ScopeContract.Normalized()
	if scope.WriteAllowedExplicit {
		return append([]string(nil), scope.WriteAllowedPaths...), true
	}

	if opts.WriteAllowedPathsExplicit || len(opts.WriteAllowedPaths) > 0 {
		return uniqueImplementationDispatchPaths([]string(opts.WriteAllowedPaths)), true
	}

	if len(scope.RequiredCoveragePaths) > 0 || len(scope.AllowedCandidatePaths) > 0 {
		paths := append(append([]string{}, scope.RequiredCoveragePaths...), scope.AllowedCandidatePaths...)
		return uniqueImplementationDispatchPaths(paths), true
	}

	return nil, false
}

func implementationDispatchEditSetObligationsWithSource(obligations plancontract.EditSetObligations, kind plancontract.EditSetObligationKind, source string) plancontract.EditSetObligations {
	obligations = obligations.Normalized()
	for i := range obligations {
		if obligations[i].Kind == kind {
			obligations[i].Source = strings.TrimSpace(source)
			return obligations.Normalized()
		}
	}

	return obligations.With(kind, source)
}

func uniqueImplementationDispatchPaths(paths []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))

	for _, path := range paths {
		path = filepath.ToSlash(strings.TrimSpace(path))
		if path == "" {
			continue
		}

		if _, ok := seen[path]; ok {
			continue
		}

		seen[path] = struct{}{}
		out = append(out, path)
	}

	return out
}

func uniqueImplementationDispatchStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))

	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		if _, ok := seen[value]; ok {
			continue
		}

		seen[value] = struct{}{}
		out = append(out, value)
	}

	return out
}

func (m *Model) resolveUnscopedImplementationAllowedPaths(ctx context.Context, prompt string) []string {
	if m == nil {
		return nil
	}

	wsCfg := m.cfg.Workspace
	if strings.TrimSpace(wsCfg.Root) == "" {
		wsCfg.Root = m.projectRoot()
	}

	if strings.TrimSpace(wsCfg.CWD) == "" {
		wsCfg.CWD = wsCfg.Root
	}

	wsCfg.EnableWrites = false

	manager := workspace.NewManager(wsCfg)

	for _, filePattern := range implementationDispatchFallbackFilePatterns(prompt) {
		result := manager.Execute(ctx, workspace.ToolRequest{
			Tool:            "search_files",
			Path:            ".",
			Pattern:         filePattern,
			NamedGap:        "locate candidate source path for unscoped TUI implementation dispatch",
			WhyNotAnswering: "need candidate edit scope before plan-execute retry",
			BudgetTokens:    1200,
		})
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		paths := implementationDispatchPathsFromSearchFilesOutput(result.Output, 4)
		if len(paths) > 0 {
			m.logMessage("dispatch_recovery", fmt.Sprintf("fallback file search %q found %s", filePattern, strings.Join(paths, ", ")))

			return paths
		}
	}

	pattern := implementationDispatchFallbackSearchPattern(prompt)
	if pattern == "" {
		return nil
	}

	result := manager.Execute(ctx, workspace.ToolRequest{
		Tool:              "search_content",
		Path:              ".",
		Pattern:           pattern,
		NamedGap:          "locate candidate source path for unscoped TUI implementation dispatch",
		WhyNotAnswering:   "need candidate edit scope before plan-execute retry",
		BudgetTokens:      1600,
		FreshnessPolicy:   "scoped",
		FreshnessMaxFiles: 64,
	})
	if strings.TrimSpace(result.Error) != "" {
		m.logMessage("dispatch_recovery", "fallback scope search failed: "+textutil.TruncateWithByteCount(result.Error, 240))

		return nil
	}

	paths := implementationDispatchRankSearchPaths(prompt, result.Output, 4)
	if len(paths) > 0 {
		m.logMessage("dispatch_recovery", fmt.Sprintf("fallback scope search %q found %s", pattern, strings.Join(paths, ", ")))
	}

	return paths
}

func implementationDispatchFallbackFilePatterns(prompt string) []string {
	raw := strings.Split(implementationDispatchFallbackSearchPattern(prompt), "|")

	var terms []string

	for _, term := range raw {
		term = strings.TrimSpace(term)
		if term != "" {
			terms = append(terms, term)
		}
	}

	var patterns []string

	for width := 3; width >= 2; width-- {
		for i := 0; i+width <= len(terms); i++ {
			patterns = append(patterns, strings.Join(terms[i:i+width], "_"))
		}
	}

	return uniqueImplementationDispatchStrings(patterns)
}

func implementationDispatchPathsFromSearchFilesOutput(output string, max int) []string {
	if max <= 0 {
		max = 4
	}

	var paths []string

	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}

		path := filepath.ToSlash(fields[len(fields)-1])
		if !implementationDispatchPathLooksEditableSource(path) || !strings.Contains(path, "/") {
			continue
		}

		paths = append(paths, path)
		if len(paths) >= max {
			break
		}
	}

	return uniqueImplementationDispatchPaths(paths)
}

func implementationDispatchFallbackSearchPattern(prompt string) string {
	lower := strings.ToLower(strings.TrimSpace(prompt))
	if lower == "" {
		return ""
	}

	replacer := strings.NewReplacer("'", " ", "\"", " ", "`", " ", "’", " ", "-", " ", "_", " ", "/", " ")

	lower = replacer.Replace(lower)

	words := strings.FieldsFunc(lower, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	stop := map[string]struct{}{
		"actual": {}, "against": {}, "bounded": {}, "brief": {}, "completion": {}, "continue": {}, "continuing": {}, "design": {}, "docs": {}, "durable": {}, "existing": {}, "focus": {}, "from": {}, "implement": {}, "implementation": {}, "left": {}, "minimal": {}, "model": {}, "must": {}, "only": {}, "partial": {}, "path": {}, "please": {}, "prd": {}, "proving": {}, "replace": {}, "research": {}, "right": {}, "that": {}, "this": {}, "through": {}, "tdd": {}, "tests": {}, "topic": {}, "with": {}, "work": {}, "workflow": {}, "you": {}, "can": {}, "top": {}, "alias": {},
	}

	var terms []string

	seen := map[string]struct{}{}

	for _, word := range words {
		word = strings.TrimSpace(word)
		if len(word) < 4 {
			continue
		}

		if _, skip := stop[word]; skip {
			continue
		}

		if _, ok := seen[word]; ok {
			continue
		}

		seen[word] = struct{}{}
		terms = append(terms, word)

		if len(terms) >= 4 {
			break
		}
	}

	return strings.Join(terms, "|")
}

func implementationDispatchAllowedPathsFromContextPack(prompt string, max int) []string {
	if max <= 0 {
		max = 8
	}

	inCandidateFiles := false

	var paths []string

	for _, line := range strings.Split(prompt, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "### Candidate Files") || trimmed == "candidate_files:":
			inCandidateFiles = true
			continue
		case inCandidateFiles && isImplementationDispatchContextPackSectionBoundary(trimmed):
			inCandidateFiles = false
		case inCandidateFiles && strings.HasPrefix(trimmed, "### "):
			inCandidateFiles = false
		case inCandidateFiles && strings.HasPrefix(trimmed, "## "):
			inCandidateFiles = false
		}

		if !inCandidateFiles || !strings.HasPrefix(trimmed, "- ") {
			continue
		}

		path, role, ok := implementationDispatchContextPackCandidateLine(trimmed)
		if !ok {
			continue
		}

		role = strings.ToLower(role)
		if !strings.Contains(role, "editable") && !strings.Contains(role, "test") {
			continue
		}

		if !implementationDispatchPathLooksEditableSource(path) {
			continue
		}

		paths = append(paths, path)
		if len(paths) >= max {
			break
		}
	}

	return uniqueImplementationDispatchPaths(paths)
}

func implementationDispatchTestPathsFromContextPack(prompt string, max int) []string {
	if max <= 0 {
		max = 8
	}

	inCandidateTests := false

	var paths []string

	for _, line := range strings.Split(prompt, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "### Candidate Tests") || trimmed == "tests:":
			inCandidateTests = true
			continue
		case inCandidateTests && isImplementationDispatchContextPackSectionBoundary(trimmed):
			inCandidateTests = false
		case inCandidateTests && strings.HasPrefix(trimmed, "### "):
			inCandidateTests = false
		case inCandidateTests && strings.HasPrefix(trimmed, "## "):
			inCandidateTests = false
		}

		if !inCandidateTests || !strings.HasPrefix(trimmed, "- ") {
			continue
		}

		path, ok := implementationDispatchContextPackTestLinePath(trimmed)
		if !ok || !implementationDispatchPathLooksEditableSource(path) {
			continue
		}

		paths = append(paths, path)
		if len(paths) >= max {
			break
		}
	}

	return uniqueImplementationDispatchPaths(paths)
}

func implementationDispatchContextPackTestLinePath(line string) (string, bool) {
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
	if line == "" {
		return "", false
	}

	// Support old backtick-quoted format: `command` - path
	if idx := strings.Index(line, "`"); idx >= 0 {
		if idx2 := strings.LastIndex(line, " - "); idx2 >= 0 {
			line = strings.TrimSpace(line[idx2+3:])
		}

		if fields := strings.Fields(line); len(fields) > 0 {
			line = fields[len(fields)-1]
		}

		path := filepath.ToSlash(strings.Trim(strings.TrimSpace(line), "`"))
		if path == "" || strings.Contains(path, " ") {
			return "", false
		}

		return path, true
	}

	// New compact format: "path: detail" or "path"
	parts := strings.SplitN(line, ": ", 2)
	path := filepath.ToSlash(strings.TrimSpace(parts[0]))

	if path == "" || strings.Contains(path, " ") {
		return "", false
	}

	return path, true
}

func implementationDispatchContextPackCandidateLine(line string) (string, string, bool) {
	line = strings.TrimPrefix(strings.TrimSpace(line), "- ")
	if line == "" {
		return "", "", false
	}

	// Support old backtick-quoted format: `path` (role, reason)
	if firstTick := strings.Index(line, "`"); firstTick >= 0 {
		rest := line[firstTick+1:]
		secondTick := strings.Index(rest, "`")

		if secondTick < 0 {
			return "", "", false
		}

		path := filepath.ToSlash(strings.TrimSpace(rest[:secondTick]))
		if path == "" {
			return "", "", false
		}

		afterPath := rest[secondTick+1:]
		open := strings.Index(afterPath, "(")
		close := strings.Index(afterPath, ")")

		if open < 0 || close < 0 || close <= open {
			return path, "", true
		}

		role := strings.TrimSpace(afterPath[open+1 : close])
		if comma := strings.Index(role, ","); comma >= 0 {
			role = strings.TrimSpace(role[:comma])
		}

		return path, role, true
	}

	// New compact format: "path: role" or "path: role (reason)" or "path"
	parts := strings.SplitN(line, ": ", 2)
	path := filepath.ToSlash(strings.TrimSpace(parts[0]))

	if path == "" {
		return "", "", false
	}

	if len(parts) < 2 {
		return path, "", true
	}

	detail := strings.TrimSpace(parts[1])
	// Extract role from "role" or "role (reason)"
	role := detail
	if open := strings.Index(detail, "("); open >= 0 {
		if close := strings.Index(detail[open:], ")"); close >= 0 {
			role = strings.TrimSpace(detail[:open])
		}
	}

	if role == "" {
		role = detail
	}

	// Take first comma-separated token as role (like old behavior)
	if comma := strings.Index(role, ","); comma >= 0 {
		role = strings.TrimSpace(role[:comma])
	}

	return path, role, true
}

// isImplementationDispatchContextPackSectionBoundary returns true when line is a
// compact-projection section header (like "candidate_symbols:", "tests:",
// "context_gaps:", "omitted:", or "evidence_note:"). These mark the end of the
// current candidate section. Also matches old-format "### " and "## " headings.
// Line must not be prefixed with "- ".
func isImplementationDispatchContextPackSectionBoundary(line string) bool {
	if strings.HasPrefix(line, "- ") {
		return false
	}

	if strings.HasPrefix(line, "### ") || strings.HasPrefix(line, "## ") {
		return true
	}

	// Known section headers in the compact projection format.
	switch line {
	case "candidate_symbols:", "tests:", "context_gaps:", "omitted:":
		return true
	}

	// evidence_note: may have trailing content.
	if strings.HasPrefix(line, "evidence_note:") {
		return true
	}

	return false
}

func (m *Model) implementationDispatchAllowedPathsFromPreloadedContextPack(prompt string, contextPackSection string) []string {
	if strings.TrimSpace(contextPackSection) == "" {
		return nil
	}

	paths := implementationDispatchAllowedPathsFromContextPack(contextPackSection, 8)

	paths = append(paths, implementationDispatchTestPathsFromContextPack(contextPackSection, 8)...)
	paths = implementationDispatchFilterContextPackPathsForPrompt(prompt, paths)

	return uniqueImplementationDispatchPaths(paths)
}

func implementationDispatchFilterContextPackPathsForPrompt(prompt string, paths []string) []string {
	if len(paths) == 0 {
		return nil
	}

	promptTokens := implementationDispatchScopeTokens(prompt, true)
	if len(promptTokens) == 0 {
		return uniqueImplementationDispatchPaths(paths)
	}

	var filtered []string

	for _, path := range uniqueImplementationDispatchPaths(paths) {
		pathTokens := implementationDispatchScopeTokens(path, false)
		if implementationDispatchPathTokenOverlap(promptTokens, pathTokens) > 0 {
			filtered = append(filtered, path)
		}
	}

	return uniqueImplementationDispatchPaths(filtered)
}

func implementationDispatchPathTokenOverlap(promptTokens map[string]struct{}, pathTokens map[string]struct{}) int {
	overlap := 0

	for token := range pathTokens {
		if _, ok := promptTokens[token]; ok {
			overlap++
		}
	}

	return overlap
}

func implementationDispatchScopeTokens(text string, fromPrompt bool) map[string]struct{} {
	tokens := map[string]struct{}{}

	for _, token := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		token = strings.Trim(token, "_-")
		if len(token) < 4 {
			continue
		}

		if implementationDispatchScopeTokenIsWeak(token, fromPrompt) {
			continue
		}

		tokens[token] = struct{}{}
	}

	return tokens
}

func implementationDispatchScopeTokenIsWeak(token string, fromPrompt bool) bool {
	switch token {
	case "candidate", "change", "changes", "code", "docs", "edit", "edits", "existing", "file", "files", "focus", "from", "implementation", "implement", "internal", "make", "minimal", "only", "path", "paths", "please", "product", "requested", "source", "test", "tests", "that", "this", "work", "workflow":
		return true
	}

	if fromPrompt {
		switch token {
		case "adr", "brief", "completion", "continuing", "design", "prd", "research", "tdd", "through":
			return true
		}
	}

	return false
}

func humanPlanExecuteSourceSatisfiedCoverageBlockerMessage(failure implementationDispatchPlanExecuteFailure) (string, bool) {
	answer := strings.TrimSpace(failure.Result.PlannerAnswer)
	requiredCoveragePaths := implementationDispatchPlanExecuteFailureRequiredCoveragePaths(failure)

	if len(requiredCoveragePaths) == 0 {
		return "", false
	}

	if plancontract.SourceSatisfiedAnswerCoversPaths(answer, requiredCoveragePaths) {
		return "", false
	}

	missing := implementationDispatchPathsMissingFromAnswer(requiredCoveragePaths, answer)
	if len(missing) == 0 {
		return "", false
	}

	evidencePaths := implementationDispatchCoveredSourceSatisfiedPaths(requiredCoveragePaths, missing)
	if len(evidencePaths) == 0 {
		return "", false
	}

	var b strings.Builder

	b.WriteString("<<QUESTION>>: Plan-execute reported `Decision: source-satisfied`, but the answer did not cover every required edit path.")
	b.WriteString("\n\nThis is a terminal evidence mismatch, not a retryable planner failure. The TUI will not stack another recovery prompt.")
	b.WriteString("\n\nCovered source-satisfied evidence paths:")

	for _, path := range evidencePaths {
		b.WriteString("\n- ")
		b.WriteString(path)
	}

	b.WriteString("\n\nMissing required edit paths:")

	for _, path := range missing {
		b.WriteString("\n- ")
		b.WriteString(path)
	}

	if summary := strings.TrimSpace(failure.Result.FailureReason); summary != "" {
		b.WriteString("\n\nPlanner/orchestrator reason:\n")
		b.WriteString(summary)
	}

	return strings.TrimSpace(b.String()), true
}

func implementationDispatchPlanExecuteFailureRequiredCoveragePaths(failure implementationDispatchPlanExecuteFailure) []string {
	var paths []string

	paths = append(paths, failure.RequiredPaths...)
	paths = append(paths, failure.Options.RequiredEditPaths...)

	return uniqueImplementationDispatchPaths(paths)
}

func implementationDispatchPathsMissingFromAnswer(paths []string, answer string) []string {
	return plancontract.SourceSatisfiedMissingCoveragePaths(answer, uniqueImplementationDispatchPaths(paths))
}

func implementationDispatchCoveredSourceSatisfiedPaths(requiredPaths []string, missingPaths []string) []string {
	missing := map[string]struct{}{}
	for _, path := range uniqueImplementationDispatchPaths(missingPaths) {
		missing[path] = struct{}{}
	}

	var covered []string

	for _, path := range uniqueImplementationDispatchPaths(requiredPaths) {
		if _, ok := missing[path]; !ok {
			covered = append(covered, path)
		}
	}

	return uniqueImplementationDispatchPaths(covered)
}

func implementationDispatchRankSearchPaths(prompt string, output string, max int) []string {
	if max <= 0 {
		max = 4
	}

	terms := strings.Split(implementationDispatchFallbackSearchPattern(prompt), "|")
	uiObjective := strings.Contains(strings.ToLower(prompt), "tui") ||
		strings.Contains(strings.ToLower(prompt), "ui") ||
		strings.Contains(strings.ToLower(prompt), "banner") ||
		strings.Contains(strings.ToLower(prompt), "header") ||
		strings.Contains(strings.ToLower(prompt), "top-right") ||
		strings.Contains(strings.ToLower(prompt), "top right") ||
		strings.Contains(strings.ToLower(prompt), "view") ||
		strings.Contains(strings.ToLower(prompt), "render")

	type scored struct {
		path  string
		score int
		order int
	}

	scores := map[string]*scored{}
	order := 0

	for _, line := range strings.Split(output, "\n") {
		path, text, ok := implementationDispatchSearchLinePathText(line)
		if !ok {
			continue
		}

		entry := scores[path]
		if entry == nil {
			entry = &scored{path: path, order: order}
			scores[path] = entry
			order++
		}

		entry.score += implementationDispatchSearchPathScore(path, text, terms, uiObjective)
	}

	ranked := make([]scored, 0, len(scores))
	for _, entry := range scores {
		ranked = append(ranked, *entry)
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}

		return ranked[i].order < ranked[j].order
	})

	var out []string

	for _, entry := range ranked {
		if entry.score <= -50 {
			continue
		}

		out = append(out, entry.path)
		if len(out) >= max {
			break
		}
	}

	return out
}

func implementationDispatchSearchLinePathText(line string) (string, string, bool) {
	line = strings.TrimSpace(line)

	firstColon := strings.Index(line, ":")

	if firstColon < 0 {
		return "", "", false
	}

	fields := strings.Fields(strings.TrimSpace(line[:firstColon]))
	if len(fields) == 0 {
		return "", "", false
	}

	path := filepath.ToSlash(fields[len(fields)-1])
	if !implementationDispatchPathLooksEditableSource(path) || !strings.Contains(path, "/") {
		return "", "", false
	}

	rest := line[firstColon+1:]
	text := ""

	if secondColon := strings.Index(rest, ":"); secondColon >= 0 {
		text = strings.TrimSpace(rest[secondColon+1:])
	}

	return path, text, true
}

func implementationDispatchPathLooksEditableSource(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	if lower == "" {
		return false
	}

	for _, suffix := range []string{
		".go", ".rs", ".py", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs",
		".java", ".kt", ".swift", ".c", ".cc", ".cpp", ".h", ".hpp", ".cs",
		".rb", ".php", ".sh", ".bash", ".zsh", ".fish", ".lua", ".sql",
		".html", ".css", ".scss", ".sass", ".vue", ".svelte", ".templ",
		".json", ".yaml", ".yml", ".toml", ".xml", ".md", ".mdx",
	} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}

	return false
}

func implementationDispatchSearchPathScore(path string, text string, terms []string, uiObjective bool) int {
	lowerPath := strings.ToLower(filepath.ToSlash(path))
	lowerText := strings.ToLower(text)
	score := 1

	for _, term := range terms {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}

		if strings.Contains(lowerPath, term) {
			score += 10
		}

		if strings.Contains(lowerText, term) {
			score += 12
		}
	}

	if strings.HasSuffix(lowerPath, "_test.go") {
		score -= 100
	}

	if strings.HasPrefix(strings.TrimSpace(lowerText), "//") {
		score -= 8
	}

	if strings.Contains(lowerPath, "/agent/") || strings.Contains(lowerPath, "/workspace/") || strings.Contains(lowerPath, "/eval/") {
		score -= 30
	}

	if uiObjective {
		if strings.HasPrefix(lowerPath, "internal/surfaces/tui/app/") {
			score += 45
		}

		if strings.Contains(lowerPath, "render") || strings.Contains(lowerPath, "view") {
			score += 70
		}

		if strings.Contains(lowerPath, "model_state") {
			score += 5
		}

		if strings.Contains(lowerPath, "frontier/") || strings.Contains(lowerPath, "comprehension/") {
			score -= 25
		}
	}

	return score
}

func (m *Model) planExecuteFailureHasPlanExecuteHandoff(failure implementationDispatchPlanExecuteFailure) bool {
	if len(failure.RequiredPaths) > 0 || len(failure.AllowedPaths) > 0 {
		return true
	}

	if m == nil {
		return false
	}

	return implementationDispatchHasPlanExecuteHandoff(failure.Prompt, failure.Plan, m.cfg)
}

func (m *Model) decidePlanExecutePlannerBlocked(ctx context.Context, failure implementationDispatchPlanExecuteFailure) (recovery.PlanExecuteRecoveryDecision, string, error) {
	if m == nil || m.client == nil {
		return recovery.PlanExecuteRecoveryDecision{}, "", fmt.Errorf("model client unavailable")
	}

	opts := m.chatOptions()

	opts.Tools = nil
	opts.ToolChoice = ""
	opts.MaxTokens = 1024

	thinkingOff := false

	opts.EnableThinking = &thinkingOff
	opts.PreserveThinking = false
	opts.ChatTemplateKwargs = true

	prompt := planExecutePlannerBlockedDecisionPrompt(failure)

	raw, err := m.client.CompleteChat(ctx, []llama.ChatMessage{
		{Role: "system", Content: "You are a recovery controller for a local coding-agent harness. Return only the requested JSON object."},
		{Role: "user", Content: prompt},
	}, opts)
	if err != nil {
		return recovery.PlanExecuteRecoveryDecision{}, raw, err
	}

	decision, ok := recovery.ParsePlanExecuteRecoveryDecision(raw)
	if !ok {
		return recovery.PlanExecuteRecoveryDecision{}, raw, fmt.Errorf("recovery decision was not valid JSON")
	}

	decision.Decision = strings.ToLower(strings.TrimSpace(decision.Decision))
	if decision.Decision != "retry" && decision.Decision != "surface" {
		return recovery.PlanExecuteRecoveryDecision{}, raw, fmt.Errorf("unsupported recovery decision %q", decision.Decision)
	}

	return decision, raw, nil
}

func planExecutePlannerBlockedDecisionPrompt(failure implementationDispatchPlanExecuteFailure) string {
	var b strings.Builder

	b.WriteString("# Plan-Execute Planner Blocked Recovery\n\n")
	b.WriteString("The in-process plan-execute planner stopped before a successful `submit_plan`. Decide whether one automatic retry is worthwhile or whether this should be surfaced to the human.\n\n")
	b.WriteString("Return JSON only with this schema:\n")
	b.WriteString(`{"decision":"retry|surface","reason":"short reason","message":"human-facing message when surfacing","retry_addendum":"optional short observation; protocol instructions are ignored"}`)
	b.WriteString("\n\nRetry only when the next run can materially change conditions. Surface when the failure is missing human information, missing body evidence, repeated no-tool/no-progress behavior, or a contract failure that a same-model retry is likely to repeat. The harness owns the retry contract; do not invent submit_plan schemas or tool-call instructions in retry_addendum.\n\n")
	b.WriteString("## Objective\n\n")
	b.WriteString(strings.TrimSpace(failure.Prompt))
	b.WriteString("\n\n## Required Edit Paths\n\n")

	for _, path := range failure.RequiredPaths {
		b.WriteString("- ")
		b.WriteString(path)
		b.WriteString("\n")
	}

	if len(failure.AllowedPaths) > 0 {
		b.WriteString("\n## Allowed Edit Paths\n\n")

		for _, path := range failure.AllowedPaths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}
	}

	if reason := strings.TrimSpace(failure.Result.FailureReason); reason != "" {
		b.WriteString("\n## Planner Blocker Summary\n\n")
		b.WriteString(reason)
		b.WriteString("\n")
	}

	if answer := strings.TrimSpace(failure.Result.PlannerAnswer); answer != "" {
		b.WriteString("\n## Planner Blocker Answer\n\n")
		b.WriteString(textutil.TruncateWithByteCount(answer, 6000))
		b.WriteString("\n")
	}

	if stderr := strings.TrimSpace(failure.Stderr); stderr != "" {
		b.WriteString("\n## Orchestrator Error\n\n")
		b.WriteString(textutil.TruncateWithByteCount(stderr, 2000))
		b.WriteString("\n")
	}

	if stdout := strings.TrimSpace(failure.Stdout); stdout != "" && !strings.Contains(stdout, failure.Result.PlannerAnswer) {
		b.WriteString("\n## Orchestrator Output\n\n")
		b.WriteString(textutil.TruncateWithByteCount(stdout, 3000))
		b.WriteString("\n")
	}

	return b.String()
}

func appendPlanExecutePlannerRecoveryAddendum(brief string, failure implementationDispatchPlanExecuteFailure, decision recovery.PlanExecuteRecoveryDecision, rawDecision string) string {
	var b strings.Builder

	b.WriteString(strings.TrimSpace(brief))
	b.WriteString("\n\n## Previous Planner Blocker Recovery\n\n")
	b.WriteString("The previous planner attempt stopped before `submit_plan`. This retry is allowed exactly once. Do not repeat a prose final answer.\n\n")

	if reason := strings.TrimSpace(decision.Reason); reason != "" {
		b.WriteString("- Recovery decision reason: ")
		b.WriteString(reason)
		b.WriteString("\n")
	}

	b.WriteString("- Retry instruction: ")
	b.WriteString(planExecutePlannerRecoveryRetryInstruction(failure))
	b.WriteString("\n")

	if addendum := planExecutePlannerRecoveryObservation(decision.RetryAddendum); addendum != "" {
		b.WriteString("- Recovery controller observation: ")
		b.WriteString(addendum)
		b.WriteString("\n")
	}

	if summary := strings.TrimSpace(failure.Result.FailureReason); summary != "" {
		b.WriteString("- Previous blocker: ")
		b.WriteString(summary)
		b.WriteString("\n")
	}

	if strings.TrimSpace(rawDecision) != "" && strings.TrimSpace(decision.RetryAddendum) == "" {
		b.WriteString("- Recovery controller returned a retry decision without an observation.\n")
	}

	return b.String()
}

func planExecutePlannerRecoveryRetryInstruction(failure implementationDispatchPlanExecuteFailure) string {
	var b strings.Builder

	paths := uniqueImplementationDispatchPaths(append(append([]string{}, failure.RequiredPaths...), failure.Options.RequiredEditPaths...))

	if refresh, ok := planExecuteRequiredSourceReadBlocker(failure); ok {
		b.WriteString(recovery.PlanExecuteSourceRefreshRetryInstruction(refresh, paths))
	} else {
		switch {
		case len(paths) > 0:
			fmt.Fprintf(&b, "After this retry has satisfied source-body obligations and runtime-required git history evidence for every existing required path listed above (%s), produce one valid `submit_plan` call whose edits include at least one real edit for each required path, with no outside paths. Do not treat a source read or history read as plan coverage.", strings.Join(paths, ", "))
		case len(failure.AllowedPaths) > 0 || len(failure.Options.AllowedEditPaths) > 0:
			allowed := uniqueImplementationDispatchPaths(append(append([]string{}, failure.AllowedPaths...), failure.Options.AllowedEditPaths...))
			fmt.Fprintf(&b, "Produce one valid `submit_plan` call using the allowed candidate paths listed above (%s) as the edit allowlist; do not treat every candidate path as mandatory coverage.", strings.Join(allowed, ", "))
		default:
			b.WriteString("Produce one valid `submit_plan` call grounded in the objective and target references.")
		}
	}

	b.WriteString(" Each edit entry must include `path`, exactly one edit shape (`start_line`+`end_line`+`content` for non-trivial existing-source edits, `old_text`+`new_text` for tiny exact snippets, or `replace_all:true`+`new_text` for missing/new files only), and one-sentence `purpose`. Existing source/test files must use bounded anchored edits. If the current files already contain the requested final state, answer `Decision: source-satisfied` with current-state evidence for every relevant path instead of submitting stale edits. If the plan cannot be produced from the available evidence, answer `<<QUESTION>>:` with the exact path-specific blocker.")

	return b.String()
}

func planExecuteRequiredSourceReadBlockerPath(failure implementationDispatchPlanExecuteFailure) (string, bool) {
	refresh, ok := planExecuteRequiredSourceReadBlocker(failure)
	if !ok {
		return "", false
	}

	return refresh.Path, true
}

func planExecuteRequiredSourceReadBlocker(failure implementationDispatchPlanExecuteFailure) (recovery.PlanExecutePlannerBlockedSourceRefresh, bool) {
	if len(failure.RequiredPaths) == 0 && len(failure.Options.RequiredEditPaths) == 0 {
		return recovery.PlanExecutePlannerBlockedSourceRefresh{}, false
	}

	text := strings.Join([]string{
		failure.Result.FailureReason,
		failure.Result.PlannerAnswer,
		failure.Stdout,
		failure.Stderr,
	}, "\n")
	paths := uniqueImplementationDispatchPaths(append(append([]string{}, failure.RequiredPaths...), failure.Options.RequiredEditPaths...))
	nextAction := ""
	if failure.Result.Completion != nil {
		nextAction = string(failure.Result.Completion.NextAction)
	}
	return recovery.PlanExecutePlannerBlockedRetryRefreshResult(failure.Result.FailureKind, text, paths, recoverycontract.NextAction(strings.TrimSpace(nextAction)))
}

func planExecutePlannerRecoveryObservation(addendum string) string {
	addendum = strings.TrimSpace(addendum)
	if addendum == "" || implementationDispatchRecoveryMessageIsInternal(addendum) || planExecutePlannerRecoveryAddendumContradictsContract(addendum) {
		return ""
	}

	return textutil.TruncateWithByteCount(addendum, 600)
}

func planExecutePlannerRecoveryAddendumContradictsContract(addendum string) bool {
	lower := strings.ToLower(strings.TrimSpace(addendum))
	if lower == "" {
		return false
	}

	contractTerms := []string{
		"decision: source-satisfied",
		"`decision: source-satisfied`",
		"source-satisfied",
	}
	entryTerms := []string{
		"each entry",
		"each edit",
		"every entry",
		"every edit",
		"must have",
		"must contain",
		"submit_plan",
	}
	hasContractTerm := false

	for _, term := range contractTerms {
		if strings.Contains(lower, term) {
			hasContractTerm = true
			break
		}
	}

	if !hasContractTerm {
		return false
	}

	for _, term := range entryTerms {
		if strings.Contains(lower, term) {
			return true
		}
	}

	return false
}

func (m *Model) implementationDispatchAllowedPathsForMissingRequestedTestEdit(failure implementationDispatchPlanExecuteFailure) []string {
	var paths []string

	submittedPaths := m.implementationDispatchSubmittedPlanPaths(failure)

	paths = append(paths, submittedPaths...)
	paths = append(paths, failure.RequiredPaths...)
	paths = append(paths, failure.Options.RequiredEditPaths...)
	paths = append(paths, failure.AllowedPaths...)
	paths = append(paths, failure.Options.AllowedEditPaths...)
	paths = append(paths, implementationDispatchAllowedPathsFromContextPack(failure.Options.Prompt, 8)...)
	paths = append(paths, implementationDispatchTestPathsFromContextPack(failure.Options.Prompt, 8)...)

	return uniqueImplementationDispatchPaths(paths)
}

func (m *Model) implementationDispatchAllowedPathsForMissingRequestedImplementationEdit(failure implementationDispatchPlanExecuteFailure) []string {
	submittedPaths := m.implementationDispatchSubmittedPlanPaths(failure)

	var paths []string

	paths = append(paths, submittedPaths...)

	for _, path := range submittedPaths {
		if sourcePath := m.implementationDispatchSiblingSourceForTestPath(path); sourcePath != "" {
			paths = append(paths, sourcePath)
		}
	}

	paths = append(paths, failure.RequiredPaths...)
	paths = append(paths, failure.Options.RequiredEditPaths...)
	paths = append(paths, failure.AllowedPaths...)
	paths = append(paths, failure.Options.AllowedEditPaths...)
	paths = append(paths, implementationDispatchAllowedPathsFromContextPack(failure.Options.Prompt, 8)...)

	return uniqueImplementationDispatchPaths(paths)
}

func (m *Model) implementationDispatchSiblingSourceForTestPath(path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" || !strings.HasSuffix(strings.ToLower(path), "_test.go") {
		return ""
	}

	if m == nil {
		return ""
	}

	sourcePath := strings.TrimSuffix(path, "_test.go") + ".go"
	root := strings.TrimSpace(m.projectRoot())

	if root == "" {
		return ""
	}

	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(sourcePath)))
	if err != nil || info.IsDir() {
		return ""
	}

	return sourcePath
}

func (m *Model) implementationDispatchSubmittedPlanPaths(failure implementationDispatchPlanExecuteFailure) []string {
	if m == nil {
		return nil
	}

	root := implementationdispatch.WorkspaceRoot(m.cfg.Workspace.Root, m.cfg.Workspace.CWD)
	if strings.TrimSpace(root) == "" {
		return nil
	}

	sessionID := strings.TrimSpace(failure.Result.SessionID)
	if sessionID == "" {
		base := strings.TrimSpace(filepath.Base(filepath.ToSlash(failure.Result.ResumeStatePath)))

		sessionID = strings.TrimSuffix(base, ".json")
	}

	if sessionID == "" {
		return nil
	}

	state, _, found, err := planexecute.TryLoadProgressState(root, sessionID)
	if err != nil || !found {
		return nil
	}

	var paths []string

	paths = append(paths, state.ChangedPaths...)

	for _, edit := range state.Edits {
		paths = append(paths, edit.Path)
	}

	return uniqueImplementationDispatchPaths(paths)
}

func humanPlanExecutePlannerBlockedMessage(failure implementationDispatchPlanExecuteFailure, decision recovery.PlanExecuteRecoveryDecision) string {
	if msg := strings.TrimSpace(decision.Message); msg != "" {
		if !implementationDispatchRecoveryMessageIsInternal(msg) && !planExecuteRecoveryMessageContradictsSourceSatisfied(failure, msg) {
			return msg
		}
	}

	var b strings.Builder

	b.WriteString("<<QUESTION>>: Plan-execute could not produce a plan artifact.")

	if reason := strings.TrimSpace(decision.Reason); reason != "" {
		b.WriteString("\n\nDecision: ")
		b.WriteString(reason)
	}

	blockerSummary := humanPlanExecutePlannerBlockerSummary(failure)
	if blockerSummary != "" {
		b.WriteString("\n\nPlanner blocker: ")
		b.WriteString(blockerSummary)
	}

	if len(failure.RequiredPaths) > 0 {
		b.WriteString("\n\nRequired edit paths still need a plan:")

		for _, path := range failure.RequiredPaths {
			b.WriteString("\n- ")
			b.WriteString(path)
		}
	}

	if answer := strings.TrimSpace(failure.Result.PlannerAnswer); answer != "" && !planExecutePlannerAnswerIsInternalCompletionGate(answer) {
		if detail, ok := humanPlanExecutePlannerAnswerDetail(answer); ok {
			b.WriteString("\n\nPlanner detail:\n")
			b.WriteString(textutil.TruncateWithByteCount(detail, 1200))
		} else {
			b.WriteString("\n\nPlanner answer:\n")
			b.WriteString(textutil.TruncateWithByteCount(answer, 2000))
		}
	}

	return strings.TrimSpace(b.String())
}

func humanPlanExecutePlannerAnswerDetail(answer string) (string, bool) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", false
	}

	lower := strings.ToLower(answer)
	if !strings.Contains(lower, "changed paths: none") ||
		!strings.Contains(lower, "required edit paths not changed:") ||
		!strings.Contains(lower, "blocker:") {

		return "", false
	}

	idx := strings.Index(lower, "blocker:")
	if idx < 0 {
		return "", false
	}

	detail := strings.TrimSpace(answer[idx:])

	return detail, detail != ""
}

func planExecuteRecoveryMessageContradictsSourceSatisfied(failure implementationDispatchPlanExecuteFailure, message string) bool {
	if !recovery.PlanExecuteFailureShowsRequiredSourcesSatisfied(implementationDispatchPlanExecuteRetryInput(failure)) {
		return false
	}

	lower := strings.ToLower(strings.TrimSpace(message))
	if lower == "" {
		return false
	}

	return strings.Contains(lower, "has not read") ||
		strings.Contains(lower, "lacks the current source") ||
		strings.Contains(lower, "without reading") ||
		strings.Contains(lower, "please allow the planner to read")
}

func planExecuteFailureIsRepeatedCompletionGate(failure implementationDispatchPlanExecuteFailure) bool {
	text := strings.ToLower(strings.TrimSpace(failure.Result.FailureReason + "\n" + failure.Result.PlannerAnswer))

	return strings.Contains(text, "completion gate rejected") &&
		(strings.Contains(text, "repeatedly") || strings.Contains(text, "gate retry 3/3") || strings.Contains(text, "completion_gate_repeated_rejection"))
}

func implementationDispatchRecoveryMessageIsInternal(message string) bool {
	lower := strings.ToLower(strings.TrimSpace(message))
	if lower == "" {
		return false
	}

	normalized := strings.NewReplacer("`", "", `"`, "", "'", "").Replace(lower)

	return strings.Contains(normalized, "completion gate") ||
		strings.Contains(normalized, "submit_plan tool") ||
		(strings.Contains(normalized, "submit_plan") && strings.Contains(normalized, "tool call")) ||
		strings.Contains(normalized, "manually invoke") ||
		strings.Contains(normalized, "system rejected") ||
		strings.Contains(normalized, "tool calls")
}

func planExecutePlannerAnswerIsInternalCompletionGate(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))

	return strings.Contains(lower, "completion gate rejected") ||
		strings.Contains(lower, "missing completion evidence") ||
		strings.Contains(lower, "gate retry") ||
		strings.Contains(lower, "requires a successful submit_plan tool result")
}

func humanPlanExecutePlannerBlockerSummary(failure implementationDispatchPlanExecuteFailure) string {
	summary := strings.TrimSpace(failure.Result.FailureReason)
	if summary == "" {
		return ""
	}

	lowerSummary := strings.ToLower(summary)
	answer := strings.ToLower(strings.TrimSpace(failure.Result.PlannerAnswer))

	if recovery.PlanExecuteFailureShowsRequiredSourcesSatisfied(implementationDispatchPlanExecuteRetryInput(failure)) {
		return "planner satisfied required source reads but kept choosing discovery instead of emitting a `submit_plan` tool call"
	}

	if strings.Contains(lowerSummary, "completion gate rejected") ||
		strings.Contains(answer, "completion gate rejected the final answer repeatedly") {

		if len(failure.AllowedPaths) > 0 {
			return "planner stopped before emitting a `submit_plan` tool call for the discovered candidate scope"
		}

		if len(failure.RequiredPaths) > 0 {
			return "planner stopped before emitting a `submit_plan` tool call covering the required edit paths"
		}

		return "planner stopped before emitting a `submit_plan` tool call"
	}

	return summary
}

func humanPlanExecuteDispatchWithoutScopeMessage(failure implementationDispatchPlanExecuteFailure) string {
	var b strings.Builder

	b.WriteString("<<QUESTION>>: Plan-execute dispatch blocked before retry because the TUI did not provide a concrete edit scope.")
	b.WriteString("\n\nThe TUI plan-execute path needs required edit paths, allowed edit paths, or a resolved plan document before retrying the planner.")

	if prompt := strings.TrimSpace(failure.Prompt); prompt != "" {
		b.WriteString("\n\nObjective:\n")
		b.WriteString(prompt)
	}

	if summary := strings.TrimSpace(failure.Result.FailureReason); summary != "" {
		b.WriteString("\n\nPlanner blocker:\n")
		b.WriteString(summary)
	}

	return strings.TrimSpace(b.String())
}

func (m *Model) shouldDisableThinkingForPlanExecutePlanner() bool {
	return m == nil || !m.modelThinkingFloor
}
