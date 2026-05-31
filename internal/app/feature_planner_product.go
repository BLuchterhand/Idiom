package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"idiom/internal/featureplanner"
	"idiom/internal/llama"
	"idiom/internal/productworkflow"
)

// sendFeaturePlannerCommandAsync scaffolds an effort folder and enters the
// product creation flow: PRD drafting → ADR drafting → TDD synthesis → planning.
func (m *Model) sendFeaturePlannerCommandAsync(objective string) {
	objective = strings.TrimSpace(objective)
	if objective == "" {
		m.status = "feature-planner objective is required"
		return
	}
	effortRoot, err := m.scaffoldFeaturePlannerEffort(objective)
	if err != nil {
		m.status = fmt.Sprintf("feature-planner: %v", err)
		return
	}
	m.featurePlannerEffortRoot = effortRoot
	// Only set phase to drafting if not already set by a resume.
	if m.featurePlannerPhase == "" {
		m.featurePlannerPhase = productworkflow.PhasePRDDrafting
	}
	m.logMessage("idiom.feature_planner.start", objective)
	m.status = fmt.Sprintf("PRD drafting phase started (%s)", m.displayPath(effortRoot))

	m.streaming = true
	m.workingFrame = 0
	m.currentTurnBusyStatus = "Generating initial PRD draft..."
	m.beginAgentHistoryTurn()
	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		answer, err := m.runProductFeaturePlannerTurn(ctx, objective)
		if !m.streaming {
			return
		}
		if err != nil {
			m.events <- streamDoneMsg{err: err}
			return
		}
		m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: answer})
		m.logMessage("assistant", answer)
		m.events <- streamDoneMsg{}
	}()
}

// scaffoldFeaturePlannerEffort creates the effort folder and returns the path.
func (m *Model) scaffoldFeaturePlannerEffort(objective string) (string, error) {
	root := m.projectRoot()
	slug := sanitizeEffortSlug(objective)
	effortRoot := filepath.Join(root, "docs", "product-workflows", "efforts", "started", slug)
	result, err := productworkflow.InitializeEffort(productworkflow.CreateEffortOptions{
		EffortRoot: effortRoot,
		Title:      objective,
	})
	if err != nil {
		return "", fmt.Errorf("scaffold effort: %w", err)
	}
	if result.Status == "resumed" {
		m.featurePlannerPhase = result.CurrentPhase
		m.status = fmt.Sprintf("Resumed effort at phase %s: %s", result.CurrentPhase, m.displayPath(effortRoot))
	}
	return filepath.Clean(effortRoot), nil
}

// sendProductFeaturePlannerTurnAsync dispatches a non-slash message during
// PRD or ADR drafting phases.
func (m *Model) sendProductFeaturePlannerTurnAsync(message string) {
	m.streaming = true
	m.workingFrame = 0
	m.currentTurnBusyStatus = busyStatusForPrompt(message)
	m.beginAgentHistoryTurn()
	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		answer, err := m.runProductFeaturePlannerTurn(ctx, message)
		if err != nil {
			m.events <- streamDoneMsg{err: err}
			return
		}
		m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: answer})
		m.logMessage("assistant", answer)
		m.events <- streamDoneMsg{}
	}()
}

// runProductFeaturePlannerTurn routes a message to the appropriate phase handler.
func (m *Model) runProductFeaturePlannerTurn(ctx context.Context, message string) (string, error) {
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
func (m *Model) runProductApproveGate() (string, error) {
	effortRoot := m.featurePlannerEffortRoot
	switch m.featurePlannerPhase {
	case productworkflow.PhasePRDDrafting:
		return m.approvePRDAndAdvance(effortRoot)
	case productworkflow.PhaseADRDrafting:
		return m.approveADRAndPlan(effortRoot)
	default:
		return "", fmt.Errorf("no active product creation phase to approve")
	}
}

// runPRDDraftingTurn generates or refines a PRD based on the operator's message.
func (m *Model) runPRDDraftingTurn(ctx context.Context, message string) (string, error) {
	effortRoot := m.featurePlannerEffortRoot
	prdPath := filepath.Join(effortRoot, "prd.md")

	hasExisting := false
	if _, err := os.Stat(prdPath); err == nil {
		hasExisting = true
	}

	var prompt string
	var previousPRD *productworkflow.PRDDocument
	if hasExisting {
		current, err := os.ReadFile(prdPath)
		if err != nil {
			return "", fmt.Errorf("read prd.md: %w", err)
		}
		if parsed, _, parseErr := productworkflow.TryParsePRDMarkdown(string(current)); parseErr == nil {
			previousPRD = &parsed
		}
		prompt = buildPRDRefinementPrompt(string(current), message, effortRoot)
	} else {
		prompt = buildPRDGenerationPrompt(message, effortRoot)
	}

	m.logMessage("feature_planner_prd_turn", message)
	opts := m.chatOptions()
	opts.MaxTokens = 8192

	// PRD generation with parse-repair loop.
	answer, prd, directPlan, err := m.generatePRDWithRepair(ctx, prompt, opts, effortRoot, hasExisting)
	if err != nil {
		return "", fmt.Errorf("PRD generation: %w", err)
	}
	if directPlan {
		return m.runDirectFeaturePlannerPlanFromDraft(ctx, answer, effortRoot, hasExisting)
	}
	if len(prd.Requirements) == 0 {
		// Parse-repair exhausted; answer is the user-visible message.
		return answer, nil
	}

	// Parse succeeded — write and validate.
	if writeErr := productworkflow.WritePRDMarkdown(effortRoot, prd); writeErr != nil {
		return "", fmt.Errorf("write prd.md: %w", writeErr)
	}

	intake := productworkflow.ValidatePRDIntake(prd)
	if !intake.Passed && hasExisting {
		return "PRD updated but has intake issues:\n" + formatPRDIntakeFailures(intake), nil
	}
	return formatPRDStatus(prd, intake, hasExisting, previousPRD), nil
}

// generatePRDWithRepair calls the model and attempts to parse the PRD output.
// If parsing fails, it saves the raw output, asks the model to fix formatting,
// and retries up to maxParseRepairRetries times. Returns the parsed PRD and
// the raw answer text on success, or an error after exhausting retries.
func (m *Model) generatePRDWithRepair(ctx context.Context, prompt string, opts llama.ChatOptions, effortRoot string, hasExisting bool) (string, productworkflow.PRDDocument, bool, error) {
	answer, err := m.client.CompleteChat(ctx, []llama.ChatMessage{{Role: "user", Content: prompt}}, opts)
	if err != nil {
		return "", productworkflow.PRDDocument{}, false, fmt.Errorf("PRD model call: %w", err)
	}

	prd, repaired, parseErr := productworkflow.TryParsePRDMarkdown(answer)
	if parseErr == nil {
		return answer, prd, false, nil
	}
	if featurePlannerAnswerIsDirectPlan(answer) {
		return answer, productworkflow.PRDDocument{}, true, nil
	}

	// Parse failed — enter repair loop.
	for attempt := 0; attempt < maxParseRepairRetries; attempt++ {
		// Save raw output for operator inspection with attempt-specific filename.
		saveRawParseFailure(effortRoot, fmt.Sprintf("prd_parse_failure_raw_%03d.md", attempt+1), answer)

		// Build repair prompt with the error and ask model to fix formatting.
		repairPrompt := buildPRDFormatRepairPrompt(answer, parseErr, effortRoot)
		opts := m.chatOptions()
		opts.MaxTokens = 8192
		repairAnswer, repErr := m.client.CompleteChat(ctx, []llama.ChatMessage{{Role: "user", Content: repairPrompt}}, opts)
		if repErr != nil {
			// Model call itself failed — not a format issue.
			saveRawParseFailure(effortRoot, "prd_parse_failure_final.md", answer)
			return answer, productworkflow.PRDDocument{}, false, fmt.Errorf("PRD repair call failed (attempt %d/%d): %w", attempt+1, maxParseRepairRetries, repErr)
		}

		answer = repairAnswer
		prd, repaired, parseErr = productworkflow.TryParsePRDMarkdown(answer)
		if parseErr == nil {
			_ = repaired // successfully repaired text is written below
			return answer, prd, false, nil
		}
		if featurePlannerAnswerIsDirectPlan(answer) {
			return answer, productworkflow.PRDDocument{}, true, nil
		}
	}

	// All retries exhausted — save final raw output and return a message,
	// not a fatal error, so the session stays alive and the operator can
	// inspect the raw output.
	saveRawParseFailure(effortRoot, "prd_parse_failure_final.md", answer)
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
	return err == nil && parsed.Kind == featureplanner.ToolSubmitFeaturePlan
}

func (m *Model) runDirectFeaturePlannerPlanFromDraft(ctx context.Context, rawPlan string, effortRoot string, hasExisting bool) (string, error) {
	parsed, err := featureplanner.ParsePlannerOutput(rawPlan)
	if err != nil {
		return "", err
	}
	sessionID := m.featurePlannerSessionID()
	state := featureplanner.NewState(sessionID, "Implement direct feature plan emitted during PRD drafting", "", 3, time.Now().UTC())
	events := featureplanner.ApplyPlannerOutput(&state, parsed, time.Now().UTC())
	implementorBlocked := false
	if state.Status == featureplanner.FeatureStateDispatching {
		events = append(events, featureplanner.DispatchReadyAtoms(&state, 0, func(prompt string, taskFamily string) (string, error) {
			if taskFamily == "feature_planner_implementor" {
				if implementorBlocked {
					return encodeFeaturePlannerSiblingDispatchPaused(), nil
				}
				answer, runErr := m.runFeaturePlannerAtomPlanExecute(ctx, prompt)
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
	eventsPath, err := featureplanner.AppendEvents(m.projectRoot(), state.SessionID, events)
	if err != nil {
		return "", err
	}
	state.EventsPath = eventsPath
	statePath, err := featureplanner.SaveState(m.projectRoot(), &state)
	if err != nil {
		return "", err
	}
	if state.Status == featureplanner.FeatureStateComplete {
		m.featurePlannerPhase = ""
	}
	if !hasExisting && strings.TrimSpace(effortRoot) != "" {
		_ = os.RemoveAll(effortRoot)
	}
	answer := formatFeaturePlannerTUIAnswer(state, statePath)
	m.logMessage(featurePlannerTUITerminalRole(state.Status), answer)
	return answer, nil
}

// buildPRDFormatRepairPrompt creates a prompt asking the model to fix
// formatting issues in its PRD output.
func buildPRDFormatRepairPrompt(badOutput string, parseErr error, effortRoot string) string {
	effortID := filepath.Base(effortRoot)
	now := time.Now().UTC().Format(time.RFC3339)
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
	b.WriteString(fmt.Sprintf("- Required front matter between the --- delimiters:\n  schema_version: idiom.prd.v1\n  prd_id: %s\n  status: draft\n  task_id: %s\n  created_at: %s\n", effortID, effortID, now))
	b.WriteString("- Do NOT wrap the output in ``` code fences\n")
	b.WriteString("- Do NOT add any preamble text before the --- delimiter\n")
	b.WriteString("- Output only the PRD markdown, nothing else\n")
	b.WriteString("- Example: the front matter block must look like this (both --- lines are required):\n")
	b.WriteString("  ---\n")
	b.WriteString(fmt.Sprintf("  schema_version: idiom.prd.v1\n  prd_id: %s\n  status: draft\n  task_id: %s\n  created_at: %s\n", effortID, effortID, now))
	b.WriteString("  ---\n")
	return b.String()
}

// saveRawParseFailure writes raw model output to disk when parsing fails,
// so the operator can inspect what went wrong without losing the data.
func saveRawParseFailure(effortRoot, filename, content string) {
	path := filepath.Join(effortRoot, filename)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(content), 0o644)
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
func buildPRDGenerationPrompt(featureDescription, effortRoot string) string {
	effortID := filepath.Base(effortRoot)
	now := time.Now().UTC().Format(time.RFC3339)
	var b strings.Builder
	b.WriteString("You are drafting a Product Requirements Document (PRD). ")
	b.WriteString("Given the feature description below, produce a complete PRD in markdown format.\n\n")
	b.WriteString("PRD format requirements:\n")
	b.WriteString("- Include YAML front matter with an opening --- on its own line, the fields below, and a closing --- on its own line:\n")
	b.WriteString("  schema_version: idiom.prd.v1\n")
	b.WriteString(fmt.Sprintf("  prd_id: %s\n", effortID))
	b.WriteString("  status: draft\n")
	b.WriteString(fmt.Sprintf("  task_id: %s\n", effortID))
	b.WriteString(fmt.Sprintf("  created_at: %s\n", now))
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
func buildPRDRefinementPrompt(currentPRD, feedback, effortRoot string) string {
	effortID := filepath.Base(effortRoot)
	now := time.Now().UTC().Format(time.RFC3339)
	var b strings.Builder
	b.WriteString("You are refining an existing Product Requirements Document (PRD). ")
	b.WriteString("Incorporate the operator's feedback below into the PRD.\n\n")
	b.WriteString("Current PRD:\n")
	b.WriteString(currentPRD)
	b.WriteString("\n\nOperator feedback:\n")
	b.WriteString(feedback)
	b.WriteString("\n\n")
	b.WriteString("Preserve the existing PRD format, including the opening and closing --- front matter delimiters. The front matter fields must remain:\n")
	b.WriteString(fmt.Sprintf("  schema_version: idiom.prd.v1\n  prd_id: %s\n  status: draft\n  task_id: %s\n  created_at: %s\n", effortID, effortID, now))
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
func (m *Model) runADRDraftingTurn(ctx context.Context, message string) (string, error) {
	effortRoot := m.featurePlannerEffortRoot
	adrsDir := filepath.Join(effortRoot, "adrs")
	idxPath := filepath.Join(adrsDir, "index.json")

	prdData, err := os.ReadFile(filepath.Join(effortRoot, "prd.md"))
	if err != nil {
		return "", fmt.Errorf("read prd.md: %w", err)
	}
	prd, _, err := productworkflow.TryParsePRDMarkdown(string(prdData))
	if err != nil {
		return "", fmt.Errorf("parse prd.md: %w", err)
	}

	hasExisting := false
	if _, err := os.Stat(idxPath); err == nil {
		hasExisting = true
	}

	if !hasExisting {
		// Initial ADR generation: use shared helper.
		m.logMessage("feature_planner_adr_turn", message)
		adrDocs, indexEntries, genErr := m.generateADRs(ctx, effortRoot)
		if genErr != nil {
			return "", genErr
		}
		return formatADRStatus(indexEntries, adrDocs, false, nil), nil
	}

	// Refinement path: model call with existing ADRs as context.
	var existing strings.Builder
	var previousADRs []productworkflow.ADRDocument
	idxData, _ := os.ReadFile(idxPath)
	existing.WriteString("Current ADR Index:\n")
	existing.Write(idxData)
	existing.WriteString("\n\nCurrent ADR Documents:\n")
	idx, _ := productworkflow.ParseADRIndexJSON(idxData)
	for _, entry := range idx.ADRs {
		adrData, readErr := os.ReadFile(filepath.Join(adrsDir, entry.Path))
		if readErr != nil {
			continue
		}
		fmt.Fprintf(&existing, "=== %s ===\n", entry.Path)
		existing.Write(adrData)
		existing.WriteString("\n")
		if adrDoc, _, parseErr := productworkflow.TryParseADRMarkdown(string(adrData)); parseErr == nil {
			previousADRs = append(previousADRs, adrDoc)
		}
	}
	m.logMessage("feature_planner_adr_turn", message)

	// ADR refinement with parse-repair loop.
	adrDocs, indexEntries, err := m.generateADRsWithRepair(
		ctx, effortRoot, mustReadFile(filepath.Join(effortRoot, "prd.md")),
		prd,
		func(content, root string) string {
			return buildADRRefinementPrompt(existing.String(), message, effortRoot)
		},
	)
	if err != nil {
		return "", fmt.Errorf("ADR refinement: %w", err)
	}

	existingIdx, _ := productworkflow.ParseADRIndexJSON(mustReadFile(idxPath))
	adrDocs, indexEntries = mergeADRIndex(existingIdx, adrDocs, indexEntries)
	adrDocs = preserveExistingADRDocuments(adrDocs, previousADRs)

	if len(adrDocs) == 0 {
		// Parse-repair exhausted — raw output on disk for operator inspection.
		return "ADR parsing failed after all repair attempts. The raw model output has been saved to adrs_parse_failure_final.txt in the effort directory. Continue chatting to refine the ADRs.", nil
	}

	for i, doc := range adrDocs {
		path := indexEntries[i].Path
		if writeErr := productworkflow.WriteADRMarkdown(effortRoot, path, doc); writeErr != nil {
			return "", fmt.Errorf("write %s: %w", path, writeErr)
		}
	}

	// Write index.
	ws, wsErr := productworkflow.LoadWorkflowState(effortRoot)
	effortID := filepath.Base(effortRoot)
	if wsErr == nil {
		effortID = ws.EffortID
	}
	adrIndex := productworkflow.ADRIndex{
		SchemaVersion: "idiom.adr_index.v1",
		EffortID:      effortID,
		ADRs:          indexEntries,
	}
	if writeErr := productworkflow.WriteADRIndexJSON(effortRoot, adrIndex); writeErr != nil {
		return "", fmt.Errorf("write adrs/index.json: %w", writeErr)
	}

	return formatADRStatus(indexEntries, adrDocs, true, previousADRs), nil
}

// generateADRs makes a model call to produce ADRs from the PRD, parses them,
// writes the files and index, and returns the documents and index entries.
func (m *Model) generateADRs(ctx context.Context, effortRoot string) ([]productworkflow.ADRDocument, []productworkflow.ADRIndexEntry, error) {
	prdData, err := os.ReadFile(filepath.Join(effortRoot, "prd.md"))
	if err != nil {
		return nil, nil, fmt.Errorf("read prd.md: %w", err)
	}
	prd, _, err := productworkflow.TryParsePRDMarkdown(string(prdData))
	if err != nil {
		return nil, nil, fmt.Errorf("parse prd.md: %w", err)
	}

	adrDocs, indexEntries, genErr := m.generateADRsWithRepair(ctx, effortRoot, prdData, prd, buildADRGenerationPrompt)
	if genErr != nil {
		return nil, nil, genErr
	}

	// Write successfully parsed ADRs (only for initial generation path —
	// the refinement path in runADRDraftingTurn handles its own writes
	// after mergeADRIndex).
	for i, doc := range adrDocs {
		path := indexEntries[i].Path
		if writeErr := productworkflow.WriteADRMarkdown(effortRoot, path, doc); writeErr != nil {
			return nil, nil, fmt.Errorf("write %s: %w", path, writeErr)
		}
	}

	// Write index.
	ws, wsErr := productworkflow.LoadWorkflowState(effortRoot)
	effortID := filepath.Base(effortRoot)
	if wsErr == nil {
		effortID = ws.EffortID
	}
	idx := productworkflow.ADRIndex{
		SchemaVersion: "idiom.adr_index.v1",
		EffortID:      effortID,
		ADRs:          indexEntries,
	}
	if writeErr := productworkflow.WriteADRIndexJSON(effortRoot, idx); writeErr != nil {
		return nil, nil, fmt.Errorf("write adrs/index.json: %w", writeErr)
	}

	return adrDocs, indexEntries, nil
}

// generateADRsWithRepair calls the model and attempts to split/parse ADR output.
// If all ADR chunks fail to parse, it enters a repair loop with retry budget.
// The repair loop parses the repair model output directly via TrySplitAndParseADRs
// rather than routing through tryGenerateADRs (which would rebuild the prompt
// and call the model a second time).
func (m *Model) generateADRsWithRepair(
	ctx context.Context,
	effortRoot string,
	prdData []byte,
	prd productworkflow.PRDDocument,
	buildPrompt func(string, string) string,
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
		saveRawParseFailure(effortRoot, fmt.Sprintf("adrs_parse_failure_raw_%03d.txt", attempt+1), rawAnswer)

		repairPrompt := buildADRFormatRepairPrompt(rawAnswer, failures, effortRoot)
		opts := m.chatOptions()
		opts.MaxTokens = 16384
		repairAnswer, repErr := m.client.CompleteChat(ctx, []llama.ChatMessage{{Role: "user", Content: repairPrompt}}, opts)
		if repErr != nil {
			saveRawParseFailure(effortRoot, "adrs_parse_failure_final.txt", rawAnswer)
			return nil, nil, fmt.Errorf("ADR repair call failed (attempt %d/%d): %w", attempt+1, maxParseRepairRetries, repErr)
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

	saveRawParseFailure(effortRoot, "adrs_parse_failure_final.txt", rawAnswer)
	return nil, nil, fmt.Errorf("ADR parsing failed after %d repair attempt(s); raw output saved to %s", maxParseRepairRetries, filepath.Join(effortRoot, "adrs_parse_failure_final.txt"))
}

// tryGenerateADRs makes one model call and attempts to parse ADR output.
func (m *Model) tryGenerateADRs(
	ctx context.Context,
	effortRoot string,
	promptContent string,
	prd productworkflow.PRDDocument,
	buildPrompt func(string, string) string,
) (string, []productworkflow.ADRDocument, []productworkflow.ADRIndexEntry, []productworkflow.ADRParseFailure, error) {
	prompt := buildPrompt(promptContent, effortRoot)
	opts := m.chatOptions()
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
func buildADRFormatRepairPrompt(badOutput string, failures []productworkflow.ADRParseFailure, effortRoot string) string {
	effortID := filepath.Base(effortRoot)
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
	b.WriteString(fmt.Sprintf("- Effort ID: %s\n", effortID))
	b.WriteString("- Do NOT wrap the output in ``` code fences\n")
	b.WriteString("- Do NOT add any preamble text before the first --- delimiter\n")
	b.WriteString("- Output only the ADRs separated by === lines. Do not add any preamble or postamble.\n")
	return b.String()
}

// buildADRGenerationPrompt creates the initial ADR drafting prompt.
func buildADRGenerationPrompt(prdContent string, effortRoot string) string {
	effortID := filepath.Base(effortRoot)
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
	b.WriteString(fmt.Sprintf("Effort ID: %s\n", effortID))
	b.WriteString("Output only the ADRs separated by === lines. Do not add any preamble or postamble.\n")
	return b.String()
}

// buildADRRefinementPrompt creates a prompt to refine existing ADRs.
func buildADRRefinementPrompt(currentADRs, feedback, effortRoot string) string {
	effortID := filepath.Base(effortRoot)
	var b strings.Builder
	b.WriteString("You are refining existing Architecture Decision Records (ADRs). ")
	b.WriteString("Incorporate the operator's feedback below into the ADRs.\n\n")
	b.WriteString("Current ADRs:\n")
	b.WriteString(currentADRs)
	b.WriteString("\n\nOperator feedback:\n")
	b.WriteString(feedback)
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("Effort ID: %s. ", effortID))
	b.WriteString("Maintain the same ADR format, including opening and closing --- front matter delimiters, YAML front matter fields, and all required sections. ")
	b.WriteString("Output the complete set of ADRs separated by === lines. Do not add any preamble or postamble.\n")
	return b.String()
}

// splitAndParseADRs is a backward-compatible shim that delegates to
// productworkflow.TrySplitAndParseADRs and discards per-chunk failure details.
func splitAndParseADRs(output string, prd productworkflow.PRDDocument) ([]productworkflow.ADRDocument, []productworkflow.ADRIndexEntry, error) {
	adrDocs, indexEntries, _, err := productworkflow.TrySplitAndParseADRs(output, prd)
	return adrDocs, indexEntries, err
}

// mergeADRIndex merges new ADRs into an existing index, replacing matching IDs.
func mergeADRIndex(existing productworkflow.ADRIndex, newDocs []productworkflow.ADRDocument, newEntries []productworkflow.ADRIndexEntry) ([]productworkflow.ADRDocument, []productworkflow.ADRIndexEntry) {
	existingByID := map[string]int{}
	for i, entry := range existing.ADRs {
		existingByID[entry.ADRID] = i
	}
	for _, entry := range newEntries {
		if idx, ok := existingByID[entry.ADRID]; ok {
			existing.ADRs[idx] = entry
		} else {
			existing.ADRs = append(existing.ADRs, entry)
		}
	}
	orderedDocs := make([]productworkflow.ADRDocument, 0, len(existing.ADRs))
	for _, entry := range existing.ADRs {
		found := false
		for _, doc := range newDocs {
			if doc.FrontMatter.ADRID == entry.ADRID {
				orderedDocs = append(orderedDocs, doc)
				found = true
				break
			}
		}
		if !found {
			orderedDocs = append(orderedDocs, productworkflow.ADRDocument{
				FrontMatter: productworkflow.ADRFrontMatter{ADRID: entry.ADRID, Status: entry.Status},
			})
		}
	}
	return orderedDocs, existing.ADRs
}

func preserveExistingADRDocuments(docs []productworkflow.ADRDocument, previous []productworkflow.ADRDocument) []productworkflow.ADRDocument {
	if len(docs) == 0 || len(previous) == 0 {
		return docs
	}
	previousByID := map[string]productworkflow.ADRDocument{}
	for _, doc := range previous {
		adrID := strings.TrimSpace(doc.FrontMatter.ADRID)
		if adrID != "" {
			previousByID[adrID] = doc
		}
	}
	for i, doc := range docs {
		adrID := strings.TrimSpace(doc.FrontMatter.ADRID)
		if adrID == "" || strings.TrimSpace(doc.BodyMarkdown) != "" {
			continue
		}
		if oldDoc, ok := previousByID[adrID]; ok && strings.TrimSpace(oldDoc.BodyMarkdown) != "" {
			docs[i] = oldDoc
		}
	}
	return docs
}

// approvePRDAndAdvance runs the PRD intake validator, advances to ADR drafting,
// and generates initial ADRs automatically.
func (m *Model) approvePRDAndAdvance(effortRoot string) (string, error) {
	if _, err := productworkflow.AdvanceCreatePhase(effortRoot, productworkflow.PhasePRDReview); err != nil {
		return "", fmt.Errorf("advance to PRD review: %w", err)
	}

	result, intake, err := productworkflow.ApprovePRDGate(effortRoot)
	if err != nil {
		return "", fmt.Errorf("PRD approval: %w", err)
	}
	if result.Status == "blocked" {
		_ = resetCreatePhase(effortRoot, productworkflow.PhasePRDDrafting)
		prdData, readErr := os.ReadFile(filepath.Join(effortRoot, "prd.md"))
		if readErr == nil {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if serr := m.ensureServerReady(ctx); serr == nil {
				prompt := buildPRDRepairPrompt(string(prdData), intake)
				opts := m.chatOptions()
				opts.MaxTokens = 8192
				repair, repErr := m.client.CompleteChat(ctx, []llama.ChatMessage{{Role: "user", Content: prompt}}, opts)
				if repErr == nil {
					repairedPRD, _, parseErr := productworkflow.TryParsePRDMarkdown(repair)
					if parseErr == nil {
						_ = productworkflow.WritePRDMarkdown(effortRoot, repairedPRD)
						newIntake := productworkflow.ValidatePRDIntake(repairedPRD)
						if newIntake.Passed {
							return "PRD was rejected but has been auto-repaired. Review the updated requirements and type /product-approve to try again.", nil
						}
						return formatPRDApprovalBlocked(intake, true), nil
					}
				}
			}
		}
		return formatPRDApprovalBlocked(intake, false), nil
	}

	// PRD approved — advance to ADR drafting phase.
	m.featurePlannerPhase = productworkflow.PhaseADRDrafting
	m.status = "ADR drafting phase started."

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if serr := m.ensureServerReady(ctx); serr != nil {
		return "", fmt.Errorf("server not ready for ADR generation: %w", serr)
	}

	adrDocs, indexEntries, genErr := m.generateADRs(ctx, effortRoot)
	if genErr != nil {
		return "", fmt.Errorf("PRD approved but ADR generation failed: %w", genErr)
	}

	// Advance to ADR drafting in workflow state.
	_, _ = productworkflow.AdvanceCreatePhase(effortRoot, productworkflow.PhaseADRDrafting)

	var output strings.Builder
	output.WriteString(formatPRDApproved())
	output.WriteString("\n\n")
	output.WriteString(formatADRStatus(indexEntries, adrDocs, false, nil))
	output.WriteString("\n\nReview the ADRs and type /product-approve to proceed, or continue chatting to refine.")
	return output.String(), nil
}

// approveADRAndPlan runs ADR intake validation, TDD synthesis, and feature planning.
func (m *Model) approveADRAndPlan(effortRoot string) (string, error) {
	if _, err := productworkflow.AdvanceCreatePhase(effortRoot, productworkflow.PhaseADRReview); err != nil {
		return "", fmt.Errorf("advance to ADR review: %w", err)
	}

	result, intakes, err := productworkflow.ApproveADRGate(effortRoot)
	if err != nil {
		return "", fmt.Errorf("ADR approval: %w", err)
	}
	if result.Status == "blocked" {
		_ = resetCreatePhase(effortRoot, productworkflow.PhaseADRDrafting)
		return formatADRApprovalBlocked(intakes), nil
	}

	// ADRs approved.
	_, _ = productworkflow.AdvanceCreatePhase(effortRoot, productworkflow.PhaseTDDSynthesis)

	planResult, plannerState, statePath, planErr := m.synthesizeTDDAndRunPlanner(effortRoot)
	if planErr != nil {
		return "", fmt.Errorf("feature planning: %w", planErr)
	}
	if recoveryMessage, blocked := formatProductWorkflowFeaturePlannerRecovery(plannerState, statePath); blocked {
		m.logMessage("idiom.feature_planner.recovery_open", recoveryMessage)
		return strings.TrimSpace(planResult + "\n\n" + recoveryMessage), nil
	}

	// Only mark complete and clear phase on success.
	_, _ = productworkflow.CompleteEffort(effortRoot)
	m.featurePlannerPhase = ""
	m.featurePlannerEffortRoot = ""

	return planResult, nil
}

// synthesizeTDDAndRunPlanner builds TDD obligations from PRD+ADRs, loads
// product workflow context, and runs the feature planner with it.
func (m *Model) synthesizeTDDAndRunPlanner(effortRoot string) (string, featureplanner.FeatureState, string, error) {
	prdData, err := os.ReadFile(filepath.Join(effortRoot, "prd.md"))
	if err != nil {
		return "", featureplanner.FeatureState{}, "", fmt.Errorf("read prd.md: %w", err)
	}
	prd, err := productworkflow.ParsePRDMarkdown(prdData)
	if err != nil {
		return "", featureplanner.FeatureState{}, "", fmt.Errorf("parse prd.md: %w", err)
	}

	idxData, err := os.ReadFile(filepath.Join(effortRoot, "adrs", "index.json"))
	if err != nil {
		return "", featureplanner.FeatureState{}, "", fmt.Errorf("read adrs/index.json: %w", err)
	}
	adrIdx, err := productworkflow.ParseADRIndexJSON(idxData)
	if err != nil {
		return "", featureplanner.FeatureState{}, "", fmt.Errorf("parse adrs/index.json: %w", err)
	}

	var parsedADRs []productworkflow.ParsedADR
	adrPathByID := map[string]string{}
	for _, entry := range adrIdx.ADRs {
		if entry.Status == "" || entry.Path == "" {
			continue
		}
		adrData, readErr := os.ReadFile(filepath.Join(effortRoot, "adrs", entry.Path))
		if readErr != nil {
			continue
		}
		adrDoc, _, parseErr := productworkflow.TryParseADRMarkdown(string(adrData))
		if parseErr != nil {
			continue
		}
		parsedADRs = append(parsedADRs, productworkflow.ParsedADR{
			Doc:         adrDoc,
			RelPath:     filepath.Join("adrs", entry.Path),
			RawMarkdown: string(adrData),
		})
		adrPathByID[adrDoc.FrontMatter.ADRID] = filepath.Join("adrs", entry.Path)
	}

	ws, wsErr := productworkflow.LoadWorkflowState(effortRoot)
	effortID := filepath.Base(effortRoot)
	if wsErr == nil {
		effortID = ws.EffortID
	}

	ledger := productworkflow.TDDLedger{
		SchemaVersion: "idiom.tdd_ledger.v1",
		EffortID:      effortID,
		TaskID:        effortID,
		CoverageTier:  "unit",
		Obligations:   []productworkflow.TDDObligation{},
	}

	synthPlan := productworkflow.PlanTDDObligations(prd, parsedADRs, ledger, adrPathByID)
	obligationID := 1
	for _, item := range synthPlan.Obligations {
		if item.BlockerReason != "" {
			continue
		}
		ledger.Obligations = append(ledger.Obligations, productworkflow.TDDObligation{
			ID:             fmt.Sprintf("TDD-%03d", obligationID),
			Type:           item.SuggestedType,
			Behavior:       fmt.Sprintf("Verify %s behavior for PRD requirement %s", item.ADRRef, item.PRDRequirementRef),
			Owner:          item.OwnerPath,
			Command:        firstNonEmpty(item.Command, "go test ./..."),
			RequiredBefore: "implementation",
			Status:         "planned",
		})
		obligationID++
	}

	tddJSON, _ := json.MarshalIndent(ledger, "", "  ")
	if writeErr := os.WriteFile(filepath.Join(effortRoot, "tdd.json"), append(tddJSON, '\n'), 0o644); writeErr != nil {
		return "", featureplanner.FeatureState{}, "", fmt.Errorf("write tdd.json: %w", writeErr)
	}

	traceability := loadProductWorkflowTraceability(effortRoot)
	brief := buildProductWorkflowBrief(effortRoot, ws, prd, adrIdx, parsedADRs, ledger, traceability)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := m.ensureServerReady(ctx); err != nil {
		return "", featureplanner.FeatureState{}, "", fmt.Errorf("server not ready: %w", err)
	}

	sessionID := m.featurePlannerSessionID()
	state := featureplanner.NewState(sessionID, "Implement feature from PRD/ADR artifacts", "", 3, time.Now().UTC())

	m.logMessage("idiom.feature_planner.start", "Planning from product workflow artifacts")

	implementorBlocked := false
	result, err := featureplanner.Run(state, featureplanner.RunOptions{
		Prompt:                "Implement the feature described by the product workflow context.",
		MaxPlanningIterations: 3,
		Now:                   time.Now().UTC(),
		ProductWorkflowBrief:  brief,
	}, func(prompt string, taskFamily string) (string, error) {
		if taskFamily == "feature_planner_implementor" {
			if implementorBlocked {
				return encodeFeaturePlannerSiblingDispatchPaused(), nil
			}
			answer, runErr := m.runFeaturePlannerAtomPlanExecute(ctx, prompt)
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

	eventsPath, _ := featureplanner.AppendEvents(m.projectRoot(), result.State.SessionID, result.Events)
	result.State.EventsPath = eventsPath
	statePath, _ := featureplanner.SaveState(m.projectRoot(), &result.State)

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
	output.WriteString(formatFeaturePlannerTUIAnswer(result.State, statePath))
	answer := output.String()
	m.logMessage(featurePlannerTUITerminalRole(result.State.Status), answer)
	return answer, result.State, statePath, nil
}

// buildProductWorkflowBrief constructs a ProductWorkflowBrief from effort artifacts.
func buildProductWorkflowBrief(effortRoot string, workflowState productworkflow.WorkflowState, prd productworkflow.PRDDocument, adrIdx productworkflow.ADRIndex, parsedADRs []productworkflow.ParsedADR, ledger productworkflow.TDDLedger, traceability productworkflow.TraceabilityMatrix) *featureplanner.ProductWorkflowBrief {
	brief := &featureplanner.ProductWorkflowBrief{
		EffortID:           ledger.EffortID,
		TaskID:             ledger.TaskID,
		EffortRoot:         effortRoot,
		CoverageTier:       ledger.CoverageTier,
		CoverageGatePassed: true,
		ArtifactRootPath:   effortRoot,
	}
	brief.ContextEvidencePaths = appendUniqueBriefStrings(brief.ContextEvidencePaths, workflowState.Context.ReferencedPaths...)
	for _, finding := range productworkflow.ImplementationAffectingClaimFindings(prd, parsedADRs) {
		brief.Blockers = appendUniqueBriefStrings(brief.Blockers, finding.Message)
	}
	for _, finding := range productworkflow.ImplementationAffectingClaimEvidenceFindings(prd, parsedADRs, productworkflow.TraceabilityEvidenceIDs(traceability)) {
		brief.Blockers = appendUniqueBriefStrings(brief.Blockers, finding.Message)
	}

	for _, req := range prd.Requirements {
		brief.PRDRequirements = append(brief.PRDRequirements, featureplanner.PRDRequirementBrief{
			ID:          req.ID,
			Priority:    req.Priority,
			Statement:   req.Statement,
			Testability: req.Testability,
			Metric:      req.Metric,
			Obligations: append([]string{}, req.TDDObligations...),
		})
		if strings.TrimSpace(req.Statement) != "" {
			brief.RequiredBehavior = appendUniqueBriefStrings(brief.RequiredBehavior, fmt.Sprintf("%s: %s", req.ID, strings.TrimSpace(req.Statement)))
		}
		if strings.TrimSpace(req.Testability) != "" {
			brief.Acceptance = appendUniqueBriefStrings(brief.Acceptance, fmt.Sprintf("%s testability: %s", req.ID, strings.TrimSpace(req.Testability)))
		}
		if strings.TrimSpace(req.Metric) != "" {
			brief.Acceptance = appendUniqueBriefStrings(brief.Acceptance, fmt.Sprintf("%s metric: %s", req.ID, strings.TrimSpace(req.Metric)))
		}
	}

	for _, entry := range adrIdx.ADRs {
		brief.AcceptedADRIDs = append(brief.AcceptedADRIDs, entry.ADRID)
		brief.AcceptedADRPaths = append(brief.AcceptedADRPaths, filepath.Join("adrs", entry.Path))
	}

	for _, obl := range ledger.Obligations {
		brief.TDDObligations = append(brief.TDDObligations, featureplanner.TDDObligationBrief{
			ID:             obl.ID,
			Behavior:       obl.Behavior,
			Command:        obl.Command,
			RequiredBefore: obl.RequiredBefore,
			Status:         obl.Status,
		})
		if strings.TrimSpace(obl.Behavior) != "" {
			brief.RequiredBehavior = appendUniqueBriefStrings(brief.RequiredBehavior, fmt.Sprintf("%s: %s", obl.ID, strings.TrimSpace(obl.Behavior)))
		}
		if strings.TrimSpace(obl.Command) != "" {
			brief.Tests = appendUniqueBriefStrings(brief.Tests, strings.TrimSpace(obl.Command))
			brief.Acceptance = appendUniqueBriefStrings(brief.Acceptance, fmt.Sprintf("%s passes before implementation dispatch", strings.TrimSpace(obl.Command)))
		}
	}
	for _, adr := range parsedADRs {
		adrID := strings.TrimSpace(adr.Doc.FrontMatter.ADRID)
		if adrID == "" {
			continue
		}
		if decision := adrSectionExcerpt(adr.Doc.BodyMarkdown, "Decision"); decision != "" {
			brief.Decisions = appendUniqueBriefStrings(brief.Decisions, fmt.Sprintf("%s (%s): %s", adrID, adr.RelPath, decision))
		}
		brief.FilesToModify = appendUniqueBriefStrings(brief.FilesToModify, inferProductWorkflowFilesToModify(adr.Doc.BodyMarkdown)...)
	}
	brief.Constraints = appendUniqueBriefStrings(brief.Constraints,
		"Preserve accepted PRD requirements and ADR decisions unless a new accepted ADR supersedes them.",
		"Do not mutate product workflow artifacts while implementing the feature atom.",
		"Report a planner question if PRD, ADR, or TDD obligations contradict the implementation scope.",
	)

	return brief
}

func loadProductWorkflowTraceability(effortRoot string) productworkflow.TraceabilityMatrix {
	data, err := os.ReadFile(filepath.Join(effortRoot, "evidence", "traceability_matrix.json"))
	if err != nil {
		return productworkflow.TraceabilityMatrix{}
	}
	matrix, err := productworkflow.ParseTraceabilityMatrixJSON(data)
	if err != nil {
		return productworkflow.TraceabilityMatrix{}
	}
	return matrix
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
			line += " blockers=" + strings.Join(atom.Blockers, "; ")
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
	b.WriteString("\nThe PRD has been updated. Review and type /product-approve to try again.")
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
			inSection = strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(trimmed, "## ")), section)
			continue
		}
		if inSection && trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return truncate(strings.Join(parts, " "), 220)
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

// resetCreatePhase directly writes the given phase to workflow_state.json
// without going through the creation state machine. Used to return to
// drafting after a blocked approval gate.
func resetCreatePhase(absRoot string, phase string) error {
	ws, err := productworkflow.LoadWorkflowState(absRoot)
	if err != nil {
		return err
	}
	ws.CurrentPhase = phase
	ws.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	ws.StateRevision++
	data, err := json.MarshalIndent(ws, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(absRoot, "workflow_state.json"), data, 0o644)
}

const (
	// maxParseRepairRetries is the maximum number of model retry calls
	// when PRD or ADR parsing fails. The initial attempt does not count
	// against this budget.
	maxParseRepairRetries = 2
)

// sanitizeEffortSlug produces a filesystem-safe directory name from a description.
func sanitizeEffortSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	result := strings.Builder{}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			result.WriteRune(r)
		} else if r == ' ' || r == '-' || r == '_' || r == '.' {
			if result.Len() > 0 && result.String()[result.Len()-1] != '-' {
				result.WriteByte('-')
			}
		}
	}
	slug := strings.Trim(result.String(), "-")
	if len(slug) > 60 {
		slug = slug[:60]
		slug = strings.TrimRight(slug, "-")
		// Truncate back to last word boundary to avoid partial words.
		if lastDash := strings.LastIndex(slug, "-"); lastDash > 0 {
			slug = slug[:lastDash]
		}
	}
	if slug == "" {
		slug = fmt.Sprintf("effort-%d", time.Now().Unix())
	}
	return slug
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// truncate truncates a string to maxLen characters, adding "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// mustReadFile reads a file and returns its content, or nil on error.
func mustReadFile(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return data
}
