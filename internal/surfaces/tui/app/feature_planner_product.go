package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	featureplannercontract "idiom/internal/contracts/featureplanner"
	"idiom/internal/platform/textutil"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
	"idiom/internal/workflows/featureplanner"
	"idiom/internal/workflows/featureplannerproduct"
	"idiom/internal/workflows/productworkflow"
)

// sendProductFeaturePlannerTurnAsync dispatches a non-slash message during
// PRD or ADR drafting phases.
func (m *Model) sendProductFeaturePlannerTurnAsync(message string) {
	m.streaming = true
	m.workingFrame = 0
	m.currentTurnBusyStatus = busyStatusForPrompt(message)
	m.beginAgentHistoryTurn()

	m.startPromptExecution(func(ctx context.Context, worker *promptExecutionSession) (string, error) {
		answer, err := worker.runProductFeaturePlannerTurn(ctx, message)
		if err != nil {
			return "", err
		}

		worker.appendPromptTranscriptMessage(llama.ChatMessage{Role: "assistant", Content: answer})
		worker.logMessage("assistant", answer)

		return answer, nil
	})
}

// runProductFeaturePlannerTurn routes a message to the appropriate phase handler.
func (m *promptExecutionSession) runProductFeaturePlannerTurn(ctx context.Context, message string) (string, error) {
	if err := m.ensureServerReady(ctx); err != nil {
		return "", fmt.Errorf("server not ready: %w", err)
	}

	switch m.featurePlannerPhase {
	case productworkflow.PhasePRDDrafting:
		return m.runPRDDraftingTurn(ctx, message)
	case productworkflow.PhaseADRDrafting:
		return m.runADRDraftingTurn(ctx, message)
	default:
		return "", fmt.Errorf("unknown product creation phase: %s", m.featurePlannerPhase)
	}
}

// runProductApproveGate handles /product-approve during product creation phases.
func (m *promptExecutionSession) runProductApproveGate(ctx context.Context) (string, error) {
	effortRoot := m.featurePlannerEffortRoot
	switch m.featurePlannerPhase {
	case productworkflow.PhasePRDDrafting:
		return m.approvePRDAndAdvance(ctx, effortRoot)
	case productworkflow.PhaseADRDrafting:
		return m.approveADRAndPlan(ctx, effortRoot)
	case productworkflow.PhaseTDDSynthesis:
		return m.completeProductTDDSynthesis(ctx, effortRoot)
	default:
		return "", fmt.Errorf("no active product creation phase to approve")
	}
}

// runPRDDraftingTurn generates or refines a PRD based on the operator's message.
func (m *promptExecutionSession) runPRDDraftingTurn(ctx context.Context, message string) (string, error) {
	effortRoot := m.featurePlannerEffortRoot
	draftContext, err := productworkflow.LoadPRDDraftContext(effortRoot)
	if err != nil {
		return "", fmt.Errorf("load PRD draft context: %w", err)
	}
	hasExisting := draftContext.OriginalMarkdown != ""
	previousPRD := draftContext.Document
	var prompt string

	if hasExisting {
		if draftContext.ParseError != nil {
			m.logFeaturePlannerInternalEvent("feature_planner_prd_draft_parse_repair", draftContext.ParseError.Error())
		}
		prompt = buildPRDRefinementPrompt(draftContext.OriginalMarkdown, message, draftContext.Identity)
	} else {
		prompt = buildPRDGenerationPrompt(message, draftContext.Identity)
	}

	m.logFeaturePlannerInternalEvent("feature_planner_prd_turn", message)

	opts := chatOptions(m.cfg.Chat, m.modelID, m.modelThinkingFloor, m.disableThinkingOverride)

	opts.MaxTokens = 8192

	// PRD generation with parse-repair loop.
	answer, prd, directPlan, err := m.generatePRDWithRepair(ctx, prompt, opts, effortRoot, draftContext.Identity)
	if err != nil {
		return "", fmt.Errorf("PRD generation: %w", err)
	}

	if directPlan {
		return m.runDirectFeaturePlannerPlanFromDraft(ctx, answer, effortRoot)
	}

	if len(prd.Requirements) == 0 {
		// Parse-repair exhausted; answer is the user-visible message.
		return answer, nil
	}

	// Parse succeeded — persist and validate through the product workflow owner.
	intake, writeErr := productworkflow.PersistPRDDraft(productworkflow.PRDDraftPersistenceRequest{
		EffortRoot:         effortRoot,
		Document:           prd,
		ExpectedGeneration: draftContext.Generation,
	})
	if writeErr != nil {
		return "", fmt.Errorf("write prd.md: %w", writeErr)
	}
	if !intake.Passed && hasExisting {
		return "PRD updated but has intake issues:\n" + formatPRDIntakeFailures(intake), nil
	}

	return formatPRDStatus(prd, intake, hasExisting, previousPRD), nil
}

// generatePRDWithRepair calls the model and attempts to parse the PRD output.
// If parsing fails, it saves the raw output, asks the model to fix formatting,
// and retries up to maxParseRepairRetries times. Returns the parsed PRD and
// the raw answer text on success, or an error after exhausting retries.
func (m *promptExecutionSession) generatePRDWithRepair(
	ctx context.Context,
	prompt string,
	opts llama.ChatOptions,
	effortRoot string,
	identity productworkflow.PRDDraftIdentity,
) (string, productworkflow.PRDDocument, bool, error) {
	answer, err := m.client.CompleteChat(ctx, []llama.ChatMessage{{Role: "user", Content: prompt}}, opts)
	if err != nil {
		return "", productworkflow.PRDDocument{}, false, fmt.Errorf("PRD model call: %w", err)
	}

	prd, repaired, parseErr := productworkflow.TryParsePRDMarkdown(answer)
	if parseErr == nil {
		return repaired, prd, false, nil
	}

	if featurePlannerAnswerIsDirectPlan(answer) {
		return answer, productworkflow.PRDDocument{}, true, nil
	}

	// Parse failed — enter repair loop.
	for attempt := 0; attempt < maxParseRepairRetries; attempt++ {
		// Save raw output for operator inspection with attempt-specific filename.
		if persistErr := productworkflow.PersistRawParseFailure(
			effortRoot,
			fmt.Sprintf("prd_parse_failure_raw_%03d.md", attempt+1),
			answer,
		); persistErr != nil {
			return answer, productworkflow.PRDDocument{}, false, fmt.Errorf(
				"persist PRD parse failure attempt %d: %w",
				attempt+1,
				persistErr,
			)
		}

		// Build repair prompt with the error and ask model to fix formatting.
		repairPrompt := buildPRDFormatRepairPrompt(answer, parseErr, identity)
		opts := chatOptions(m.cfg.Chat, m.modelID, m.modelThinkingFloor, m.disableThinkingOverride)

		opts.MaxTokens = 8192

		repairAnswer, repErr := m.client.CompleteChat(ctx, []llama.ChatMessage{{Role: "user", Content: repairPrompt}}, opts)
		if repErr != nil {
			// Model call itself failed — not a format issue.
			repairErr := fmt.Errorf("PRD repair call failed (attempt %d/%d): %w", attempt+1, maxParseRepairRetries, repErr)
			if persistErr := productworkflow.PersistRawParseFailure(effortRoot, "prd_parse_failure_final.md", answer); persistErr != nil {
				return answer, productworkflow.PRDDocument{}, false, errors.Join(
					repairErr,
					fmt.Errorf("persist final PRD parse failure: %w", persistErr),
				)
			}

			return answer, productworkflow.PRDDocument{}, false, repairErr
		}

		answer = repairAnswer

		prd, repaired, parseErr = productworkflow.TryParsePRDMarkdown(answer)
		if parseErr == nil {
			return repaired, prd, false, nil
		}

		if featurePlannerAnswerIsDirectPlan(answer) {
			return answer, productworkflow.PRDDocument{}, true, nil
		}
	}

	// All retries exhausted — save final raw output and return a message,
	// not a fatal error, so the session stays alive and the operator can
	// inspect the raw output.
	if persistErr := productworkflow.PersistRawParseFailure(effortRoot, "prd_parse_failure_final.md", answer); persistErr != nil {
		return answer, productworkflow.PRDDocument{}, false, fmt.Errorf("persist final PRD parse failure: %w", persistErr)
	}

	msg := fmt.Sprintf(
		"PRD parsing failed after %d total attempts. The raw model output has been saved to %s.\nParse error: %v\n\nContinue chatting to refine the PRD, or type /product-approve to attempt auto-repair.",
		maxParseRepairRetries+1,
		filepath.Join(effortRoot, "prd_parse_failure_final.md"),
		parseErr,
	)

	return msg, productworkflow.PRDDocument{}, false, nil
}

func featurePlannerAnswerIsDirectPlan(answer string) bool {
	parsed, err := featureplanner.ParsePlannerOutput(answer)
	return err == nil && parsed.Kind == featureplannercontract.ToolSubmitFeaturePlan
}

func (m *promptExecutionSession) runDirectFeaturePlannerPlanFromDraft(ctx context.Context, rawPlan string, effortRoot string) (string, error) {
	parsed, err := featureplanner.ParsePlannerOutput(rawPlan)
	if err != nil {
		return "", err
	}

	sessionID := m.featurePlannerSessionID()
	state := featureplanner.NewState(sessionID, "Implement direct feature plan emitted during PRD drafting", "", 3, time.Now().UTC())

	var events []featureplanner.Event

	brief, briefErr := featureplannerproduct.LoadBriefFromEffort(effortRoot)
	if briefErr != nil {
		state.Status = featureplanner.FeatureStatePlanningRepair
		state.PlanningLoop.State = featureplanner.FeatureStatePlanningRepair
		state.Blockers = append(state.Blockers, briefErr.Error())
		events = append(events, featureplanner.Event{Type: featureplannercontract.EventName(featureplannercontract.EventProductContextBlocked), SessionID: state.SessionID, Message: briefErr.Error()})
	} else if parsed.Kind == featureplannercontract.ToolSubmitFeaturePlan && brief != nil {
		workspaceRoot := resolveProjectRoot(m.cfg.Chat.ProjectRoot, m.workspace)
		if err := featureplanner.ValidatePlanProductWorkflowContext(parsed.Plan, *brief, workspaceRoot); err != nil {
			state.Status = featureplanner.FeatureStatePlanningRepair
			state.PlanningLoop.State = featureplanner.FeatureStatePlanningRepair
			state.Blockers = append(state.Blockers, err.Error())
			events = append(events, featureplanner.Event{Type: featureplannercontract.EventName(featureplannercontract.EventProductContextBlocked), SessionID: state.SessionID, Message: err.Error()})
		} else {
			parsed.Plan = featureplanner.NormalizePlanProductWorkflowContext(parsed.Plan, *brief, workspaceRoot)
			events = featureplanner.ApplyPlannerOutput(&state, parsed, time.Now().UTC())
		}
	} else {
		state.Status = featureplanner.FeatureStatePlanningRepair
		state.PlanningLoop.State = featureplanner.FeatureStatePlanningRepair
		state.Blockers = append(state.Blockers, "direct product feature plan is missing product workflow context")
		events = append(events, featureplanner.Event{Type: featureplannercontract.EventName(featureplannercontract.EventProductContextBlocked), SessionID: state.SessionID, Message: "direct product feature plan is missing product workflow context"})
	}

	writeEnvelope := featureplanner.ProductWriteEnvelopePaths(brief)
	implementorBlocked := false

	if state.Status == featureplanner.FeatureStateDispatching {
		events = append(events, featureplanner.DispatchReadyAtoms(&state, 0, func(prompt string, taskFamily string) (string, error) {
			if workerruntime.TaskFamilyIsFeaturePlannerImplementor(taskFamily) {
				if implementorBlocked {
					return encodeFeaturePlannerSiblingDispatchPaused(), nil
				}

				answer, runErr := m.runFeaturePlannerAtomPlanExecuteWithWriteEnvelope(ctx, prompt, writeEnvelope, brief != nil)
				if runErr != nil || featurePlannerPlanExecuteOutputBlocks(answer) {
					implementorBlocked = true
				}

				return answer, runErr
			}

			return m.runFeaturePlannerTUITurn(ctx, prompt, taskFamily)
		})...)
	}

	for _, event := range events {
		m.logFeaturePlannerEvent(event)
	}

	eventsPath, err := featureplanner.AppendEvents(resolveProjectRoot(m.cfg.Chat.ProjectRoot, m.workspace), state.SessionID, events)
	if err != nil {
		return "", err
	}

	state.EventsPath = eventsPath

	statePath, err := featureplanner.SaveState(resolveProjectRoot(m.cfg.Chat.ProjectRoot, m.workspace), &state)
	if err != nil {
		return "", err
	}

	if state.Status == featureplanner.FeatureStateComplete {
		m.featurePlannerPhase = ""
	}

	answer := formatFeaturePlannerTUIAnswerForRoot(state, statePath, resolveProjectRoot(m.cfg.Chat.ProjectRoot, m.workspace))
	m.logFeaturePlannerInternalEvent(featurePlannerTUITerminalRole(state.Status), answer)

	return answer, nil
}

// buildPRDFormatRepairPrompt creates a prompt asking the model to fix
// formatting issues in its PRD output.
func buildPRDFormatRepairPrompt(badOutput string, parseErr error, identity productworkflow.PRDDraftIdentity) string {
	var b strings.Builder

	b.WriteString("Your PRD output could not be parsed because of formatting issues.\n\n")
	b.WriteString("Parse error:\n")
	b.WriteString(parseErr.Error())
	b.WriteString("\n\n")
	b.WriteString("Your raw output (may be incomplete or malformed):\n")
	b.WriteString(badOutput)
	b.WriteString("\n\n")
	b.WriteString("Please fix the formatting and output the complete, corrected PRD.\n")
	b.WriteString("Requirements:\n")
	b.WriteString("- The document MUST begin with --- on its own line and MUST end the front matter block with a second --- on its own line immediately after the last field\n")
	b.WriteString(fmt.Sprintf("- Required front matter between the --- delimiters:\n  schema_version: idiom.prd.v1\n  prd_id: %s\n  status: draft\n  task_id: %s\n  created_at: %s\n", identity.PRDID, identity.TaskID, identity.CreatedAt))
	b.WriteString("- Do NOT wrap the output in ``` code fences\n")
	b.WriteString("- Do NOT add any preamble text before the --- delimiter\n")
	b.WriteString("- Output only the PRD markdown, nothing else\n")
	b.WriteString("- Example: the front matter block must look like this (both --- lines are required):\n")
	b.WriteString("  ---\n")
	b.WriteString(fmt.Sprintf("  schema_version: idiom.prd.v1\n  prd_id: %s\n  status: draft\n  task_id: %s\n  created_at: %s\n", identity.PRDID, identity.TaskID, identity.CreatedAt))
	b.WriteString("  ---\n")

	return b.String()
}

// formatPRDIntakeFailures formats PRD intake validation failures for display.
func formatPRDIntakeFailures(intake productworkflow.IntakeResult) string {
	var b strings.Builder

	for _, f := range intake.Failures {
		fmt.Fprintf(&b, "  - [%s] %s\n", f.Code, f.Message)
	}

	b.WriteString("\nContinue chatting to refine, or type /product-approve to attempt auto-repair.")

	return b.String()
}

// buildPRDGenerationPrompt creates the initial PRD drafting prompt.
func buildPRDGenerationPrompt(featureDescription string, identity productworkflow.PRDDraftIdentity) string {
	var b strings.Builder

	b.WriteString("You are drafting a Product Requirements Document (PRD). ")
	b.WriteString("Given the feature description below, produce a complete PRD in markdown format.\n\n")
	b.WriteString("PRD format requirements:\n")
	b.WriteString("- Include YAML front matter with an opening --- on its own line, the fields below, and a closing --- on its own line:\n")
	b.WriteString("  schema_version: idiom.prd.v1\n")
	b.WriteString(fmt.Sprintf("  prd_id: %s\n", identity.PRDID))
	b.WriteString("  status: draft\n")
	b.WriteString(fmt.Sprintf("  task_id: %s\n", identity.TaskID))
	b.WriteString(fmt.Sprintf("  created_at: %s\n", identity.CreatedAt))
	b.WriteString("- After the front matter, list each requirement using these exact bullet keys:\n")
	b.WriteString("  - **id**: REQ-001\n")
	b.WriteString("  - **priority**: P1 (must be one of: P0, P1, P2, P3)\n")
	b.WriteString("  - **statement**: <one sentence describing the requirement>\n")
	b.WriteString("  - **evidence_refs**: [inference]\n")
	b.WriteString("  - **tdd_obligations**: [TDD-REQ-001]\n")
	b.WriteString("  - **owner_alignment**: <who needs this and why>\n")
	b.WriteString("  - **testability**: <how to verify this requirement is met>\n")
	b.WriteString("  - **metric**: <measurable outcome>\n")
	b.WriteString("  - **open_questions**: []\n")
	b.WriteString("- Inline array fields must use bracket syntax, e.g. [EV-001] or []\n")
	b.WriteString("- Produce at least 2 requirements\n")
	b.WriteString("- Do not use markdown tables for requirements\n")
	b.WriteString("- Do not quote YAML scalar values (use status: draft, not status: \"draft\")\n")
	b.WriteString("- Output only the PRD markdown, nothing else\n\n")
	b.WriteString("Feature description:\n")
	b.WriteString(featureDescription)
	b.WriteString("\n")

	return b.String()
}

// buildPRDRefinementPrompt creates a prompt to refine an existing PRD.
func buildPRDRefinementPrompt(currentPRD, feedback string, identity productworkflow.PRDDraftIdentity) string {
	var b strings.Builder

	b.WriteString("You are refining an existing Product Requirements Document (PRD). ")
	b.WriteString("Incorporate the operator's feedback below into the PRD.\n\n")
	b.WriteString("Current PRD:\n")
	b.WriteString(currentPRD)
	b.WriteString("\n\nOperator feedback:\n")
	b.WriteString(feedback)
	b.WriteString("\n\n")
	b.WriteString("Preserve the existing PRD format, including the opening and closing --- front matter delimiters. The front matter fields must remain:\n")
	b.WriteString(fmt.Sprintf("  schema_version: idiom.prd.v1\n  prd_id: %s\n  status: draft\n  task_id: %s\n  created_at: %s\n", identity.PRDID, identity.TaskID, identity.CreatedAt))
	b.WriteString("\nKeep or update the requirements list based on the feedback. Output only the complete PRD markdown, nothing else.\n")

	return b.String()
}

// buildPRDRepairPrompt creates a prompt to repair a PRD that failed intake validation.
func buildPRDRepairPrompt(currentPRD string, intake productworkflow.IntakeResult) string {
	var failures strings.Builder

	for _, f := range intake.Failures {
		fmt.Fprintf(&failures, "- [%s] %s (field: %s)\n", f.Code, f.Message, strings.Join(f.Fields, ", "))
	}

	var b strings.Builder

	b.WriteString("The PRD below failed intake validation. Fix every listed failure, then output the corrected PRD.\n\n")
	b.WriteString("Validation failures:\n")
	b.WriteString(failures.String())
	b.WriteString("\nCurrent PRD:\n")
	b.WriteString(currentPRD)
	b.WriteString("\n\nOutput only the corrected PRD markdown, nothing else.\n")

	return b.String()
}

// runADRDraftingTurn generates or refines ADRs from the current PRD.
func (m *promptExecutionSession) runADRDraftingTurn(ctx context.Context, message string) (string, error) {
	effortRoot := m.featurePlannerEffortRoot
	draftContext, err := productworkflow.LoadADRDraftContext(effortRoot)
	if errors.Is(err, productworkflow.ErrADRDraftIndexNotFound) {
		// Initial ADR generation: use shared helper.
		m.logFeaturePlannerInternalEvent("feature_planner_adr_turn", message)

		adrDocs, indexEntries, genErr := m.generateADRs(ctx, effortRoot)
		if genErr != nil {
			return "", genErr
		}

		return formatADRStatus(indexEntries, adrDocs, false, nil), nil
	}
	if err != nil {
		return "", fmt.Errorf("load ADR draft context: %w", err)
	}

	// Refinement path: model call with existing ADRs as context.
	var (
		existing     strings.Builder
		previousADRs = make([]productworkflow.ADRDocument, 0, len(draftContext.Documents))
	)

	existing.WriteString("Current ADR Index:\n")
	existing.Write(draftContext.OriginalIndexJSON)
	existing.WriteString("\n\nCurrent ADR Documents:\n")

	for _, document := range draftContext.Documents {
		fmt.Fprintf(&existing, "=== %s ===\n", document.IndexPath)
		existing.WriteString(document.OriginalMarkdown)
		existing.WriteString("\n")
		previousADRs = append(previousADRs, document.Document)
	}

	m.logFeaturePlannerInternalEvent("feature_planner_adr_turn", message)

	// ADR refinement with parse-repair loop.
	adrDocs, indexEntries, err := m.generateADRsWithRepair(
		ctx, effortRoot, []byte(draftContext.OriginalPRDMarkdown),
		draftContext.PRD,
		func(prdContent string) string {
			return buildADRRefinementPrompt(existing.String(), message, prdContent)
		},
	)
	if err != nil {
		return "", fmt.Errorf("ADR refinement: %w", err)
	}

	persisted, persistErr := productworkflow.PersistADRDrafts(productworkflow.ADRDraftPersistenceRequest{
		EffortRoot:         effortRoot,
		Documents:          adrDocs,
		IndexEntries:       indexEntries,
		ExpectedGeneration: draftContext.Generation,
	})
	if persistErr != nil {
		return "", persistErr
	}
	adrDocs = persisted.Documents
	indexEntries = persisted.IndexEntries

	return formatADRStatus(indexEntries, adrDocs, true, previousADRs), nil
}

// generateADRs makes a model call to produce ADRs from the PRD, parses them,
// writes the files and index, and returns the documents and index entries.
func (m *promptExecutionSession) generateADRs(ctx context.Context, effortRoot string) ([]productworkflow.ADRDocument, []productworkflow.ADRIndexEntry, error) {
	adrInput, err := productworkflow.LoadADRGenerationContext(effortRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("load ADR generation context: %w", err)
	}

	adrDocs, indexEntries, genErr := m.generateADRsWithRepair(
		ctx,
		effortRoot,
		[]byte(adrInput.OriginalPRDMarkdown),
		adrInput.PRD,
		buildADRGenerationPrompt,
	)
	if genErr != nil {
		return nil, nil, genErr
	}

	persisted, err := productworkflow.PersistADRDrafts(productworkflow.ADRDraftPersistenceRequest{
		EffortRoot:         effortRoot,
		Documents:          adrDocs,
		IndexEntries:       indexEntries,
		ExpectedGeneration: adrInput.Generation,
	})
	if err != nil {
		return nil, nil, err
	}

	return persisted.Documents, persisted.IndexEntries, nil
}

// generateADRsWithRepair calls the model and attempts to split/parse ADR output.
// If all ADR chunks fail to parse, it enters a repair loop with retry budget.
// The repair loop parses the repair model output directly via TrySplitAndParseADRs
// rather than routing through tryGenerateADRs (which would rebuild the prompt
// and call the model a second time).
func (m *promptExecutionSession) generateADRsWithRepair(
	ctx context.Context,
	effortRoot string,
	prdData []byte,
	prd productworkflow.PRDDocument,
	buildPrompt func(string) string,
) ([]productworkflow.ADRDocument, []productworkflow.ADRIndexEntry, error) {

	rawAnswer, adrDocs, indexEntries, failures, err := m.tryGenerateADRs(ctx, effortRoot, string(prdData), prd, buildPrompt)
	if err != nil {
		return nil, nil, err
	}

	if len(adrDocs) > 0 {
		return adrDocs, indexEntries, nil
	}

	// All chunks failed — enter repair loop.
	var splitErr error

	for attempt := 0; attempt < maxParseRepairRetries; attempt++ {
		if persistErr := productworkflow.PersistRawParseFailure(
			effortRoot,
			fmt.Sprintf("adrs_parse_failure_raw_%03d.txt", attempt+1),
			rawAnswer,
		); persistErr != nil {
			return nil, nil, fmt.Errorf("persist ADR parse failure attempt %d: %w", attempt+1, persistErr)
		}

		repairPrompt := buildADRFormatRepairPrompt(rawAnswer, failures)
		opts := chatOptions(m.cfg.Chat, m.modelID, m.modelThinkingFloor, m.disableThinkingOverride)

		opts.MaxTokens = 16384

		repairAnswer, repErr := m.client.CompleteChat(ctx, []llama.ChatMessage{{Role: "user", Content: repairPrompt}}, opts)
		if repErr != nil {
			repairErr := fmt.Errorf("ADR repair call failed (attempt %d/%d): %w", attempt+1, maxParseRepairRetries, repErr)
			if persistErr := productworkflow.PersistRawParseFailure(effortRoot, "adrs_parse_failure_final.txt", rawAnswer); persistErr != nil {
				return nil, nil, errors.Join(
					repairErr,
					fmt.Errorf("persist final ADR parse failure: %w", persistErr),
				)
			}

			return nil, nil, repairErr
		}

		rawAnswer = repairAnswer
		// Parse the repair output directly — the closure would ignore the content
		// parameter, and routing through tryGenerateADRs would make a redundant
		// model call via buildPrompt.
		adrDocs, indexEntries, failures, splitErr = productworkflow.TrySplitAndParseADRs(rawAnswer, prd)
		if splitErr != nil && len(adrDocs) == 0 {
			continue
		}

		if len(adrDocs) > 0 {
			return adrDocs, indexEntries, nil
		}
	}

	if persistErr := productworkflow.PersistRawParseFailure(effortRoot, "adrs_parse_failure_final.txt", rawAnswer); persistErr != nil {
		return nil, nil, fmt.Errorf("persist final ADR parse failure: %w", persistErr)
	}

	return nil, nil, fmt.Errorf("ADR parsing failed after %d repair attempt(s); raw output saved to %s", maxParseRepairRetries, filepath.Join(effortRoot, "adrs_parse_failure_final.txt"))
}

// tryGenerateADRs makes one model call and attempts to parse ADR output.
func (m *promptExecutionSession) tryGenerateADRs(
	ctx context.Context,
	effortRoot string,
	promptContent string,
	prd productworkflow.PRDDocument,
	buildPrompt func(string) string,
) (string, []productworkflow.ADRDocument, []productworkflow.ADRIndexEntry, []productworkflow.ADRParseFailure, error) {

	prompt := buildPrompt(promptContent)
	opts := chatOptions(m.cfg.Chat, m.modelID, m.modelThinkingFloor, m.disableThinkingOverride)

	opts.MaxTokens = 16384

	answer, err := m.client.CompleteChat(ctx, []llama.ChatMessage{{Role: "user", Content: prompt}}, opts)
	if err != nil {
		return "", nil, nil, nil, fmt.Errorf("ADR model call: %w", err)
	}

	adrDocs, indexEntries, failures, splitErr := productworkflow.TrySplitAndParseADRs(answer, prd)
	if splitErr != nil && len(adrDocs) == 0 {
		// All chunks failed — return for repair.
		return answer, nil, nil, failures, nil
	}

	return answer, adrDocs, indexEntries, failures, nil
}

// buildADRFormatRepairPrompt creates a prompt asking the model to fix
// formatting issues in its ADR output.
func buildADRFormatRepairPrompt(badOutput string, failures []productworkflow.ADRParseFailure) string {
	var b strings.Builder

	b.WriteString("Your ADR output could not be parsed because of formatting issues.\n\n")
	b.WriteString("Parse errors:\n")

	for _, f := range failures {
		fmt.Fprintf(&b, "  Chunk %d: %s\n", f.ChunkIndex, f.Error)
	}

	b.WriteString("\nYour raw output (may be incomplete or malformed):\n")
	b.WriteString(badOutput)
	b.WriteString("\n\n")
	b.WriteString("Please fix the formatting and output the complete, corrected set of ADRs.\n")
	b.WriteString("Requirements:\n")
	b.WriteString("- Each ADR MUST begin with --- on its own line and MUST end the front matter block with a second --- on its own line immediately after the last field\n")
	b.WriteString("- Separate ADRs with a line containing exactly three equals signs: ===\n")
	b.WriteString("- Do NOT wrap the output in ``` code fences\n")
	b.WriteString("- Do NOT add any preamble text before the first --- delimiter\n")
	b.WriteString("- Output only the ADRs separated by === lines. Do not add any preamble or postamble.\n")

	return b.String()
}

// buildADRGenerationPrompt creates the initial ADR drafting prompt.
func buildADRGenerationPrompt(prdContent string) string {
	var b strings.Builder

	b.WriteString("Draft Architecture Decision Records (ADRs) for the PRD below. ")
	b.WriteString("Produce one ADR per PRD requirement.\n\n")
	b.WriteString("PRD:\n")
	b.WriteString(prdContent)
	b.WriteString("\n\n")
	b.WriteString("Output format: write each ADR as a standalone markdown document with YAML front matter. ")
	b.WriteString("Separate ADRs with a line containing exactly three equals signs: ===\n\n")
	b.WriteString("Each ADR must have YAML front matter: opening --- on its own line, the fields below, and a closing --- on its own line:\n")
	b.WriteString("- schema_version: idiom.adr.v1\n")
	b.WriteString("- adr_id: ADR-001 (increment for each ADR)\n")
	b.WriteString("- status: draft\n")
	b.WriteString("- prd_refs: [REQ-001] (reference the PRD requirement IDs)\n")
	b.WriteString("- evidence_refs: [inference]\n")
	b.WriteString("- tdd_obligations: [TDD-ADR-001]\n")
	b.WriteString("- coverage_tier: unit\n\n")
	b.WriteString("Each ADR body must include all of these sections with substantive content:\n")
	b.WriteString("## Context\n## Decision\n## Alternatives Considered\n## Evidence\n")
	b.WriteString("## Consequences\n## Compatibility and Migration\n## TDD Obligations\n")
	b.WriteString("## Rollback\n## Open Questions\n\n")
	b.WriteString("Output only the ADRs separated by === lines. Do not add any preamble or postamble.\n")

	return b.String()
}

// buildADRRefinementPrompt creates a prompt to refine existing ADRs.
func buildADRRefinementPrompt(currentADRs, feedback, prdContent string) string {
	var b strings.Builder

	b.WriteString("You are refining existing Architecture Decision Records (ADRs). ")
	b.WriteString("Incorporate the operator's feedback below into the ADRs.\n\n")
	b.WriteString("Current ADRs:\n")
	b.WriteString(currentADRs)
	b.WriteString("\n\nCurrent PRD:\n")
	b.WriteString(prdContent)
	b.WriteString("\n\nOperator feedback:\n")
	b.WriteString(feedback)
	b.WriteString("\n\n")
	b.WriteString("Maintain the same ADR format, including opening and closing --- front matter delimiters, YAML front matter fields, and all required sections. ")
	b.WriteString("Output the complete set of ADRs separated by === lines. Do not add any preamble or postamble.\n")

	return b.String()
}

// approvePRDAndAdvance runs the PRD intake validator, advances to ADR drafting,
// and generates initial ADRs automatically.
func (m *promptExecutionSession) approvePRDAndAdvance(ctx context.Context, effortRoot string) (string, error) {
	transition, err := productworkflow.SubmitPRDForReview(effortRoot)
	if err != nil {
		return "", fmt.Errorf("advance to PRD review: %w", err)
	}
	if transition.Status != productworkflow.ProductWorkflowStatusOK {
		return "", fmt.Errorf("advance to PRD review: %s", strings.Join(transition.Blockers, "; "))
	}

	result, intake, err := productworkflow.ApprovePRDGate(effortRoot)
	if err != nil {
		return "", fmt.Errorf("PRD approval: %w", err)
	}

	if result.Status == "blocked" {
		recovery, recoveryErr := productworkflow.RequestCreateChanges(effortRoot)
		if recoveryErr != nil {
			return "", fmt.Errorf("return blocked PRD to drafting: %w", recoveryErr)
		}
		if recovery.Status != productworkflow.ProductWorkflowStatusOK {
			return "", fmt.Errorf("return blocked PRD to drafting: %s", strings.Join(recovery.Blockers, "; "))
		}

		draftContext, loadErr := productworkflow.LoadPRDDraftContext(effortRoot)
		if loadErr != nil {
			m.logFeaturePlannerInternalEvent("feature_planner_prd_auto_repair_failed", "load draft: "+loadErr.Error())

			return formatPRDApprovalBlocked(intake, false), nil
		}
		if serr := m.ensureServerReady(ctx); serr != nil {
			m.logFeaturePlannerInternalEvent("feature_planner_prd_auto_repair_failed", "server readiness: "+serr.Error())

			return formatPRDApprovalBlocked(intake, false), nil
		}

		prompt := buildPRDRepairPrompt(draftContext.OriginalMarkdown, intake)
		opts := chatOptions(m.cfg.Chat, m.modelID, m.modelThinkingFloor, m.disableThinkingOverride)
		opts.MaxTokens = 8192

		repair, repErr := m.client.CompleteChat(ctx, []llama.ChatMessage{{Role: "user", Content: prompt}}, opts)
		if repErr != nil {
			m.logFeaturePlannerInternalEvent("feature_planner_prd_auto_repair_failed", "model call: "+repErr.Error())

			return formatPRDApprovalBlocked(intake, false), nil
		}

		repairedPRD, _, parseErr := productworkflow.TryParsePRDMarkdown(repair)
		if parseErr != nil {
			m.logFeaturePlannerInternalEvent("feature_planner_prd_auto_repair_failed", "parse repair: "+parseErr.Error())

			return formatPRDApprovalBlocked(intake, false), nil
		}

		newIntake, persistErr := productworkflow.PersistPRDDraft(productworkflow.PRDDraftPersistenceRequest{
			EffortRoot:         effortRoot,
			Document:           repairedPRD,
			ExpectedGeneration: draftContext.Generation,
		})
		if persistErr != nil {
			return "", fmt.Errorf("write repaired prd.md: %w", persistErr)
		}
		if newIntake.Passed {
			return "PRD was rejected but has been auto-repaired. Review the updated requirements and type /product-approve to try again.", nil
		}

		return formatPRDApprovalBlocked(newIntake, true), nil
	}

	// PRD approved — advance to ADR drafting phase.
	m.featurePlannerPhase = productworkflow.PhaseADRDrafting

	if serr := m.ensureServerReady(ctx); serr != nil {
		return "", fmt.Errorf("server not ready for ADR generation: %w", serr)
	}

	adrDocs, indexEntries, genErr := m.generateADRs(ctx, effortRoot)
	if genErr != nil {
		return "", fmt.Errorf("PRD approved but ADR generation failed: %w", genErr)
	}

	var output strings.Builder

	output.WriteString(formatPRDApproved())
	output.WriteString("\n\n")
	output.WriteString(formatADRStatus(indexEntries, adrDocs, false, nil))
	output.WriteString("\n\nReview the ADRs and type /product-approve to proceed, or continue chatting to refine.")

	return output.String(), nil
}

// approveADRAndPlan runs ADR intake validation, TDD synthesis, and feature planning.
func (m *promptExecutionSession) approveADRAndPlan(ctx context.Context, effortRoot string) (string, error) {
	transition, err := productworkflow.SubmitADRsForReview(effortRoot)
	if err != nil {
		return "", fmt.Errorf("advance to ADR review: %w", err)
	}
	if transition.Status != productworkflow.ProductWorkflowStatusOK {
		return "", fmt.Errorf("advance to ADR review: %s", strings.Join(transition.Blockers, "; "))
	}

	result, intakes, err := productworkflow.ApproveADRGate(effortRoot)
	if err != nil {
		return "", fmt.Errorf("ADR approval: %w", err)
	}

	if result.Status == "blocked" {
		recovery, recoveryErr := productworkflow.RequestCreateChanges(effortRoot)
		if recoveryErr != nil {
			return "", fmt.Errorf("return blocked ADRs to drafting: %w", recoveryErr)
		}
		if recovery.Status != productworkflow.ProductWorkflowStatusOK {
			return "", fmt.Errorf("return blocked ADRs to drafting: %s", strings.Join(recovery.Blockers, "; "))
		}
		return formatADRApprovalBlocked(intakes), nil
	}
	m.featurePlannerPhase = productworkflow.PhaseTDDSynthesis

	return m.completeProductTDDSynthesis(ctx, effortRoot)
}

func (m *promptExecutionSession) completeProductTDDSynthesis(ctx context.Context, effortRoot string) (string, error) {
	planResult, plannerState, statePath, planErr := m.runPlannerAfterTDDSynthesis(ctx, effortRoot)
	if planErr != nil {
		if recoveryErr := m.recoverProductTDDSynthesis(effortRoot); recoveryErr != nil {
			return "", fmt.Errorf("feature planning failed: %v; TDD synthesis recovery failed: %w", planErr, recoveryErr)
		}
		return "", fmt.Errorf("feature planning: %w", planErr)
	}

	if recoveryMessage, blocked := formatProductWorkflowFeaturePlannerRecovery(plannerState, statePath); blocked {
		if err := m.recoverProductTDDSynthesis(effortRoot); err != nil {
			return "", fmt.Errorf("recover blocked TDD synthesis: %w", err)
		}
		m.logFeaturePlannerInternalEvent(featureplannercontract.EventName(featureplannercontract.EventRecoveryOpen), recoveryMessage)

		return strings.TrimSpace(planResult + "\n\n" + recoveryMessage), nil
	}

	completion, err := productworkflow.CompleteEffort(effortRoot)
	if err != nil {
		return "", fmt.Errorf("complete product effort: %w", err)
	}
	if completion.Status != productworkflow.ProductWorkflowStatusOK {
		return "", fmt.Errorf("complete product effort: %s", strings.Join(completion.Blockers, "; "))
	}
	if plannerState.Status == featureplanner.FeatureStateComplete {
		completedBrief, err := featureplannerproduct.LoadBriefFromEffort(effortRoot)
		if err != nil {
			return "", fmt.Errorf("reload source-satisfied product effort: %w", err)
		}
		if _, err := featureplannerproduct.FinalizeSourceSatisfiedDeliveryFromFeatureState(completedBrief, plannerState); err != nil {
			return "", fmt.Errorf("record product source-satisfied delivery: %w", err)
		}
	}

	m.featurePlannerPhase = ""
	m.featurePlannerEffortRoot = ""

	return planResult, nil
}

func (m *promptExecutionSession) recoverProductTDDSynthesis(effortRoot string) error {
	recovery, err := productworkflow.RecoverTDDSynthesis(effortRoot)
	if err != nil {
		return err
	}
	if recovery.Status != productworkflow.ProductWorkflowStatusOK {
		return fmt.Errorf("TDD synthesis recovery blocked: %s", strings.Join(recovery.Blockers, "; "))
	}

	m.featurePlannerPhase = recovery.CurrentPhase

	return nil
}

// runPlannerAfterTDDSynthesis delegates durable TDD synthesis to the product
// workflow owner, then runs the interactive feature-planner delivery loop.
func (m *promptExecutionSession) runPlannerAfterTDDSynthesis(ctx context.Context, effortRoot string) (string, featureplanner.FeatureState, string, error) {
	synthesis, err := productworkflow.SynthesizeTDDLedgerFromEffort(effortRoot)
	if err != nil {
		return "", featureplanner.FeatureState{}, "", err
	}
	ledger := synthesis.Ledger
	synthPlan := synthesis.Plan

	brief, briefErr := featureplannerproduct.LoadBriefFromEffort(effortRoot)
	if briefErr != nil {
		return "", featureplanner.FeatureState{}, "", briefErr
	}
	brief.Constraints = appendUniqueBriefStrings(brief.Constraints,
		"Preserve accepted PRD requirements and ADR decisions unless a new accepted ADR supersedes them.",
		"Do not mutate product workflow artifacts while implementing the feature atom.",
		"Report a planner question if PRD, ADR, or TDD obligations contradict the implementation scope.",
	)

	if err := m.ensureServerReady(ctx); err != nil {
		return "", featureplanner.FeatureState{}, "", fmt.Errorf("server not ready: %w", err)
	}

	sessionID := m.featurePlannerSessionID()
	state := featureplanner.NewState(sessionID, "Implement feature from PRD/ADR artifacts", "", 3, time.Now().UTC())
	state.EffortID = brief.EffortID
	state.EffortRoot = effortRoot

	m.logFeaturePlannerInternalEvent(featureplannercontract.EventName(featureplannercontract.EventStart), "Planning from product workflow artifacts")

	writeEnvelope := featureplanner.ProductWriteEnvelopePaths(brief)
	implementorBlocked := false

	result, err := featureplanner.Run(state, featureplanner.RunOptions{
		Prompt:                "Implement the feature described by the product workflow context.",
		MaxPlanningIterations: 3,
		Now:                   time.Now().UTC(),
		ProductWorkflowBrief:  brief,
		ReconcileActiveRuns:   m.reconcileFeaturePlannerActivePlanExecuteRuns,
		WorkspaceRoot:         resolveProjectRoot(m.cfg.Chat.ProjectRoot, m.workspace),
	}, func(prompt string, taskFamily string) (string, error) {
		if workerruntime.TaskFamilyIsFeaturePlannerImplementor(taskFamily) {
			if implementorBlocked {
				return encodeFeaturePlannerSiblingDispatchPaused(), nil
			}

			answer, runErr := m.runFeaturePlannerAtomPlanExecuteWithWriteEnvelope(ctx, prompt, writeEnvelope, brief != nil)
			if runErr != nil || featurePlannerPlanExecuteOutputBlocks(answer) {
				implementorBlocked = true
			}

			return answer, runErr
		}

		return m.runFeaturePlannerTUITurn(ctx, prompt, taskFamily)
	})
	if err != nil {
		return "", result.State, "", fmt.Errorf("feature planner run: %w", err)
	}

	for _, event := range result.Events {
		m.logFeaturePlannerEvent(event)
	}

	eventsPath, err := featureplanner.AppendEvents(resolveProjectRoot(m.cfg.Chat.ProjectRoot, m.workspace), result.State.SessionID, result.Events)
	if err != nil {
		return "", result.State, "", fmt.Errorf("append feature planner events: %w", err)
	}

	result.State.EventsPath = eventsPath

	statePath, err := featureplanner.SaveState(resolveProjectRoot(m.cfg.Chat.ProjectRoot, m.workspace), &result.State)
	if err != nil {
		return "", result.State, "", fmt.Errorf("save feature planner state: %w", err)
	}

	var output strings.Builder

	output.WriteString("ADR approval passed.\n")
	fmt.Fprintf(&output, "TDD obligations synthesized: %d\n", len(ledger.Obligations))

	if len(synthPlan.Findings) > 0 {
		output.WriteString("TDD findings:\n")

		for _, f := range synthPlan.Findings {
			fmt.Fprintf(&output, "  [%s] %s\n", f.Severity, f.Message)
		}
	}

	output.WriteString("\n")
	output.WriteString(formatFeaturePlannerTUIAnswerForRoot(result.State, statePath, resolveProjectRoot(m.cfg.Chat.ProjectRoot, m.workspace)))

	answer := output.String()
	m.logFeaturePlannerInternalEvent(featurePlannerTUITerminalRole(result.State.Status), answer)

	return answer, result.State, statePath, nil
}

func formatProductWorkflowFeaturePlannerRecovery(state featureplanner.FeatureState, statePath string) (string, bool) {
	var blockers []string

	for _, run := range state.ActiveRuns {
		sessionID := firstNonEmpty(strings.TrimSpace(run.LaneSessionID), strings.TrimSpace(run.RunID))

		blockers = append(blockers, fmt.Sprintf("active_run atom=%s session=%s state=%s", strings.TrimSpace(run.AtomID), sessionID, strings.TrimSpace(run.State)))
	}

	for _, atom := range state.Atoms {
		if !productWorkflowAtomBlocksCompletion(atom) {
			continue
		}

		sessionID := strings.TrimSpace(atom.LastRunID)
		requiredPaths := appendUniqueBriefStrings(nil, atom.RequiredEditPaths...)

		if len(requiredPaths) == 0 {
			requiredPaths = appendUniqueBriefStrings(nil, atom.AllowedEditPaths...)
		}

		line := fmt.Sprintf("atom=%s state=%s session=%s", strings.TrimSpace(atom.ID), strings.TrimSpace(atom.State), sessionID)
		if len(requiredPaths) > 0 {
			line += " required_paths=" + strings.Join(requiredPaths, ",")
		}

		if len(atom.Blockers) > 0 {
			line += fmt.Sprintf(" diagnostics=%d internal planner diagnostic(s) saved in feature-planner state", len(atom.Blockers))
		}

		if question := productWorkflowAtomQuestionDetail(state, atom); question != "" {
			line += " question=" + question
		}

		blockers = append(blockers, line)
	}

	if state.Status == featureplanner.FeatureStateComplete && len(blockers) == 0 {
		return "", false
	}

	var b strings.Builder

	b.WriteString("feature-planner: recovery_open\n")
	fmt.Fprintf(&b, "session_id: %s\n", strings.TrimSpace(state.SessionID))
	fmt.Fprintf(&b, "state: %s\n", strings.TrimSpace(state.Status))

	if strings.TrimSpace(statePath) != "" {
		fmt.Fprintf(&b, "state_path: %s\n", strings.TrimSpace(statePath))
	}

	if len(blockers) > 0 {
		b.WriteString("blocked_children:\n")

		for _, blocker := range blockers {
			fmt.Fprintf(&b, "  - %s\n", blocker)
		}
	} else {
		b.WriteString("blocked_children:\n")
		fmt.Fprintf(&b, "  - feature_state=%s is not complete\n", strings.TrimSpace(state.Status))
	}

	if len(state.DerivedNextValidActions) > 0 {
		fmt.Fprintf(&b, "next: %s\n", strings.Join(state.DerivedNextValidActions, ", "))
	} else {
		b.WriteString("next: inspect feature-planner state and resolve the blocked child session before product completion\n")
	}

	return strings.TrimSpace(b.String()), true
}

func productWorkflowAtomQuestionDetail(state featureplanner.FeatureState, atom featureplanner.FeatureAtomState) string {
	atomID := strings.TrimSpace(atom.ID)
	for _, question := range state.OpenQuestions {
		if strings.TrimSpace(question.AtomID) != "" && strings.TrimSpace(question.AtomID) != atomID {
			continue
		}

		parts := []string{}
		if summary := strings.TrimSpace(question.QuestionSummary); summary != "" {
			parts = append(parts, summary)
		}

		if claim := strings.TrimSpace(question.BlockingClaim); claim != "" && claim != strings.TrimSpace(question.QuestionSummary) {
			parts = append(parts, claim)
		}

		if refs := appendUniqueBriefStrings(nil, question.EvidenceRefs...); len(refs) > 0 {
			parts = append(parts, "evidence_refs="+strings.Join(refs, ","))
		}

		if len(parts) > 0 {
			return strings.Join(parts, "; ")
		}
	}

	return strings.TrimSpace(atom.LastQuestion)
}

func productWorkflowAtomBlocksCompletion(atom featureplanner.FeatureAtomState) bool {
	switch strings.TrimSpace(atom.State) {
	case featureplanner.AtomStateComplete, featureplanner.AtomStateSourceSatisfied, featureplanner.AtomStateSkipped:
		return false
	default:
		return true
	}
}

var productWorkflowPathPattern = regexp.MustCompile(`\b(?:cmd|internal|pkg|docs|test|tests)/[A-Za-z0-9_./-]+(?:\.[A-Za-z0-9_]+)?\b`)
var productWorkflowGoFilePattern = regexp.MustCompile(`\b[A-Za-z0-9_-]+(?:_test)?\.go\b`)
var productWorkflowPackagePattern = regexp.MustCompile(`\binternal/[A-Za-z0-9_/-]+\b`)

func inferProductWorkflowFilesToModify(markdown string) []string {
	markdown = strings.TrimSpace(markdown)
	if markdown == "" {
		return nil
	}

	var paths []string

	for _, match := range productWorkflowPathPattern.FindAllString(markdown, -1) {
		paths = appendUniqueBriefStrings(paths, strings.Trim(match, ".,;:)"))
	}

	packagePath := ""

	for _, match := range productWorkflowPackagePattern.FindAllString(markdown, -1) {
		match = strings.Trim(match, ".,;:)")
		if strings.Contains(match, ".") {
			continue
		}

		packagePath = match

		break
	}

	if packagePath != "" {
		for _, match := range productWorkflowGoFilePattern.FindAllString(markdown, -1) {
			if strings.Contains(match, "/") {
				paths = appendUniqueBriefStrings(paths, strings.Trim(match, ".,;:)"))
				continue
			}

			paths = appendUniqueBriefStrings(paths, filepath.ToSlash(filepath.Join(packagePath, match)))
		}
	}

	return paths
}

// formatPRDStatus returns a conversational PRD status summary without echoing
// the raw persisted PRD markdown.
func formatPRDStatus(prd productworkflow.PRDDocument, intake productworkflow.IntakeResult, wasRefinement bool, previous *productworkflow.PRDDocument) string {
	var b strings.Builder

	if wasRefinement {
		b.WriteString("PRD updated from your feedback.\n")
	} else {
		b.WriteString("PRD draft generated.\n")
	}

	b.WriteString("Saved: prd.md (artifact path: prd.md)\n\n")

	if wasRefinement && previous != nil {
		writePRDChangeSummary(&b, *previous, prd)
		b.WriteString("\n")
	}

	b.WriteString("What I captured:\n")
	fmt.Fprintf(&b, "Requirements: %d\n", len(prd.Requirements))

	for _, req := range prd.Requirements {
		fmt.Fprintf(&b, "  - %s [%s]: %s\n", req.ID, req.Priority, req.Statement)

		if strings.TrimSpace(req.Testability) != "" {
			fmt.Fprintf(&b, "    Testability: %s\n", req.Testability)
		}

		if strings.TrimSpace(req.Metric) != "" {
			fmt.Fprintf(&b, "    Metric: %s\n", req.Metric)
		}

		if len(req.OpenQuestions) > 0 {
			fmt.Fprintf(&b, "    Open questions: %s\n", strings.Join(req.OpenQuestions, "; "))
		}
	}

	if intake.Passed {
		b.WriteString("\nAll intake checks passed. Type /product-approve to accept this PRD and proceed to ADR drafting.")
	} else {
		fmt.Fprintf(&b, "\n%d intake check(s) failed. Continue chatting to refine, or type /product-approve to attempt auto-repair.\n", len(intake.Failures))

		for _, f := range intake.Failures {
			fmt.Fprintf(&b, "  - [%s] %s\n", f.Code, f.Message)
		}
	}

	return b.String()
}

// formatPRDApproved returns approval confirmation for PRD.
func formatPRDApproved() string {
	return "PRD approved. Intake validator passed."
}

// formatPRDApprovalBlocked returns a blocked PRD approval message.
func formatPRDApprovalBlocked(intake productworkflow.IntakeResult, repaired bool) string {
	var b strings.Builder

	if repaired {
		b.WriteString("PRD approval blocked but auto-repair was attempted. Review the updated PRD and try again.\n\n")
	} else {
		b.WriteString("PRD approval blocked.\n\n")
	}

	fmt.Fprintf(&b, "%d intake check(s) failed:\n", len(intake.Failures))

	for _, f := range intake.Failures {
		fmt.Fprintf(&b, "  - [%s] %s\n", f.Code, f.Message)
	}

	if repaired {
		b.WriteString("\nThe repaired PRD was persisted. Review and type /product-approve to try again.")
	} else {
		b.WriteString("\nNo repair was persisted. Continue refining the PRD, then type /product-approve to try again.")
	}

	return b.String()
}

func writePRDChangeSummary(b *strings.Builder, previous, current productworkflow.PRDDocument) {
	previousByID := mapPRDRequirementsByID(previous.Requirements)
	currentByID := mapPRDRequirementsByID(current.Requirements)

	var added, updated, removed []string

	for id, req := range currentByID {
		old, ok := previousByID[id]
		if !ok {
			added = append(added, id)
			continue
		}

		if prdRequirementFingerprint(old) != prdRequirementFingerprint(req) {
			updated = append(updated, id)
		}
	}

	for id := range previousByID {
		if _, ok := currentByID[id]; !ok {
			removed = append(removed, id)
		}
	}

	sortStrings(added)
	sortStrings(updated)
	sortStrings(removed)

	if len(added) == 0 && len(updated) == 0 && len(removed) == 0 {
		b.WriteString("Change summary: no requirement IDs changed; the PRD was normalized and re-saved.\n")

		return
	}

	b.WriteString("Change summary:\n")

	if len(added) > 0 {
		fmt.Fprintf(b, "  - Added requirements: %s\n", strings.Join(added, ", "))
	}

	if len(updated) > 0 {
		fmt.Fprintf(b, "  - Updated requirements: %s\n", strings.Join(updated, ", "))
	}

	if len(removed) > 0 {
		fmt.Fprintf(b, "  - Removed requirements: %s\n", strings.Join(removed, ", "))
	}
}

func mapPRDRequirementsByID(requirements []productworkflow.PRDRequirement) map[string]productworkflow.PRDRequirement {
	result := map[string]productworkflow.PRDRequirement{}

	for _, req := range requirements {
		id := strings.TrimSpace(req.ID)
		if id != "" {
			result[id] = req
		}
	}

	return result
}

func prdRequirementFingerprint(req productworkflow.PRDRequirement) string {
	parts := []string{
		strings.TrimSpace(req.Priority),
		strings.TrimSpace(req.Statement),
		strings.TrimSpace(req.OwnerAlignment),
		strings.TrimSpace(req.Testability),
		strings.TrimSpace(req.Metric),
		strings.Join(req.EvidenceRefs, "\x00"),
		strings.Join(req.TDDObligations, "\x00"),
		strings.Join(req.OpenQuestions, "\x00"),
	}

	return strings.Join(parts, "\x01")
}

// formatADRStatus returns a conversational ADR status summary without echoing
// the raw persisted ADR markdown.
func formatADRStatus(entries []productworkflow.ADRIndexEntry, docs []productworkflow.ADRDocument, wasRefinement bool, previous []productworkflow.ADRDocument) string {
	var b strings.Builder

	if wasRefinement {
		b.WriteString("ADR drafts updated from your feedback.\n")
	} else {
		b.WriteString("ADR drafts generated.\n")
	}

	var artifactPaths []string

	artifactPaths = append(artifactPaths, "adrs/index.json")

	for _, entry := range entries {
		if strings.TrimSpace(entry.Path) != "" {
			artifactPaths = append(artifactPaths, filepath.ToSlash(filepath.Join("adrs", entry.Path)))
		}
	}

	b.WriteString("Saved: adrs/index.json and ADR markdown files.")

	if len(artifactPaths) > 0 {
		b.WriteString(" Artifact paths: ")
		b.WriteString(strings.Join(artifactPaths, ", "))
	}

	b.WriteString("\n\n")
	fmt.Fprintf(&b, "ADRs: %d\n", len(entries))

	if len(entries) == 0 || len(docs) == 0 {
		b.WriteString("\nADR generation did not produce any parseable ADRs. Continue chatting to regenerate ADRs before approval.")

		return b.String()
	}

	if wasRefinement && len(previous) > 0 {
		writeADRChangeSummary(&b, previous, docs)
		b.WriteString("\n")
	}

	intakeResults := make([]productworkflow.IntakeResult, len(docs))
	totalFailures := 0

	for i, doc := range docs {
		adrPath := ""
		if i < len(entries) {
			adrPath = entries[i].Path
		}

		intakeResults[i] = productworkflow.ValidateADRIntake(doc, adrPath)

		totalFailures += len(intakeResults[i].Failures)
	}

	b.WriteString("Decisions to review:\n")

	for i, entry := range entries {
		fmt.Fprintf(&b, "  - %s: %s", entry.ADRID, entry.Path)

		if i < len(intakeResults) && intakeResults[i].Passed {
			b.WriteString(" (complete)")
		} else {
			b.WriteString(" (incomplete)")
		}

		b.WriteString("\n")

		if i < len(docs) {
			if decision := adrSectionExcerpt(docs[i].BodyMarkdown, "Decision"); decision != "" {
				fmt.Fprintf(&b, "    Decision: %s\n", decision)
			}

			if paths := inferProductWorkflowFilesToModify(docs[i].BodyMarkdown); len(paths) > 0 {
				fmt.Fprintf(&b, "    Impact mentioned: %s\n", strings.Join(paths, ", "))
			}

			if openQuestions := normalizedOpenQuestions(adrSectionExcerpt(docs[i].BodyMarkdown, "Open Questions")); openQuestions != "" {
				fmt.Fprintf(&b, "    Open questions: %s\n", openQuestions)
			}
		}
	}

	if totalFailures == 0 {
		b.WriteString("\nAll intake checks passed. Type /product-approve to accept the ADRs and proceed to planning.")
	} else {
		fmt.Fprintf(&b, "\n%d intake check(s) across all ADRs. Continue chatting to refine, or type /product-approve to attempt auto-repair.\n", totalFailures)
	}

	return b.String()
}

func writeADRChangeSummary(b *strings.Builder, previous, current []productworkflow.ADRDocument) {
	previousByID := mapADRsByID(previous)
	currentByID := mapADRsByID(current)

	var added, updated, removed []string

	for id, doc := range currentByID {
		old, ok := previousByID[id]
		if !ok {
			added = append(added, id)
			continue
		}

		if adrFingerprint(old) != adrFingerprint(doc) {
			updated = append(updated, id)
		}
	}

	for id := range previousByID {
		if _, ok := currentByID[id]; !ok {
			removed = append(removed, id)
		}
	}

	sortStrings(added)
	sortStrings(updated)
	sortStrings(removed)

	if len(added) == 0 && len(updated) == 0 && len(removed) == 0 {
		b.WriteString("Change summary: no ADR IDs changed; the ADR set was normalized and re-saved.\n")

		return
	}

	b.WriteString("Change summary:\n")

	if len(added) > 0 {
		fmt.Fprintf(b, "  - Added ADRs: %s\n", strings.Join(added, ", "))
	}

	if len(updated) > 0 {
		fmt.Fprintf(b, "  - Updated ADRs: %s\n", strings.Join(updated, ", "))
	}

	if len(removed) > 0 {
		fmt.Fprintf(b, "  - Removed ADRs: %s\n", strings.Join(removed, ", "))
	}
}

func mapADRsByID(docs []productworkflow.ADRDocument) map[string]productworkflow.ADRDocument {
	result := map[string]productworkflow.ADRDocument{}

	for _, doc := range docs {
		id := strings.TrimSpace(doc.FrontMatter.ADRID)
		if id != "" {
			result[id] = doc
		}
	}

	return result
}

func adrFingerprint(doc productworkflow.ADRDocument) string {
	parts := []string{
		strings.TrimSpace(doc.FrontMatter.Status),
		strings.Join(doc.FrontMatter.PRDRefs, "\x00"),
		strings.Join(doc.FrontMatter.EvidenceRefs, "\x00"),
		strings.Join(doc.FrontMatter.TDDObligations, "\x00"),
		strings.TrimSpace(doc.FrontMatter.CoverageTier),
		strings.TrimSpace(doc.BodyMarkdown),
	}

	return strings.Join(parts, "\x01")
}

func sortStrings(values []string) {
	if len(values) < 2 {
		return
	}

	sort.Strings(values)
}

func adrSectionExcerpt(markdown string, section string) string {
	markdown = strings.TrimSpace(markdown)
	section = strings.TrimSpace(section)

	if markdown == "" || section == "" {
		return ""
	}

	lines := strings.Split(markdown, "\n")
	inSection := false

	var parts []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			if inSection {
				break
			}

			inSection = textutil.EqualTrimFold(strings.TrimPrefix(trimmed, "## "), section)

			continue
		}

		if inSection && trimmed != "" {
			parts = append(parts, trimmed)
		}
	}

	return textutil.Truncate(strings.Join(parts, " "), 220)
}

func normalizedOpenQuestions(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	normalized := strings.TrimSpace(strings.TrimPrefix(text, "-"))
	if normalized == "" || strings.EqualFold(normalized, "none") {
		return ""
	}

	return text
}

// formatADRApprovalBlocked returns a blocked ADR approval message.
func formatADRApprovalBlocked(intakes []productworkflow.IntakeResult) string {
	var b strings.Builder

	b.WriteString("ADR approval blocked.\n\n")

	totalFailures := 0

	for _, intake := range intakes {
		if intake.Passed {
			continue
		}

		for _, f := range intake.Failures {
			fmt.Fprintf(&b, "  - [%s] %s (%s)\n", f.Code, f.Message, f.Path)

			totalFailures++
		}
	}

	if totalFailures == 0 {
		b.WriteString("  (No specific failures reported — the ADR index or ADR files may be missing.)\n")
	}

	b.WriteString("\nReview and type /product-approve to try again after refining the ADRs.")

	return b.String()
}

const (
	// maxParseRepairRetries is the maximum number of model retry calls
	// when PRD or ADR parsing fails. The initial attempt does not count
	// against this budget.
	maxParseRepairRetries = 2
)

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}

	return ""
}
