package featureplanner

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/config"
	"idiom/internal/featureplanner"
	"idiom/internal/llama"
	"idiom/internal/productworkflow"
	"idiom/internal/telemetry"
)

type featurePlannerOptions struct {
	ConfigPath            string
	Prompt                string
	PromptFile            string
	Model                 string
	Resume                string
	JSONEvents            bool
	MaxPlanningIterations int
	MaxAtoms              int
	EffortRoot            string
	EffortID              string
}

type featurePlannerRequest struct {
	Config                config.Config
	ConfigPath            string
	Prompt                string
	PromptFile            string
	Resume                string
	SessionID             string
	JSONEvents            bool
	MaxPlanningIterations int
	MaxAtoms              int
	EffortRoot            string
	EffortID              string
	Deps                  Dependencies
}

type featurePlannerResult struct {
	Answer string
	Fields map[string]any
}

type Dependencies struct {
	NewSessionID            func() string
	LocalOpsTelemetryFields func(config.Config, string) map[string]any
	RunPlanExecute          func(args []string, stdout io.Writer, stderr io.Writer) int
}

type featurePlannerRunner func(context.Context, featurePlannerRequest, io.Writer) (featurePlannerResult, error)

var runHeadlessFeaturePlanner featurePlannerRunner = defaultHeadlessFeaturePlanner
var runFeaturePlannerModelTurn = defaultFeaturePlannerModelTurn
var runFeaturePlannerPlanExecute = defaultFeaturePlannerPlanExecute

func RunMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return RunMainWithTelemetry(args, stdout, stderr, nil)
}

func RunMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder, depOverrides ...Dependencies) int {
	deps := resolveDependencies(depOverrides...)
	opts, err := parseFeaturePlannerOptions(args, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if strings.TrimSpace(opts.Model) != "" {
		cfg.LlamaServer.Model = opts.Model
	}
	if strings.TrimSpace(opts.Resume) != "" {
		cfg.Workspace.SessionID = strings.TrimSpace(opts.Resume)
	} else {
		cfg.Workspace.SessionID = deps.NewSessionID()
	}

	prompt, err := featurePlannerPrompt(opts)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	startFields := map[string]any{
		"json_events": opts.JSONEvents,
		"resume":      strings.TrimSpace(opts.Resume) != "",
		"session_id":  cfg.Workspace.SessionID,
		"local_ops":   deps.LocalOpsTelemetryFields(cfg, "feature-planner"),
	}
	logFeaturePlannerPhase(rec, "feature_planner_start", startFields)
	emitFeaturePlannerJSONEvent(stdout, opts.JSONEvents, "start", startFields)
	emitFeaturePlannerMessage(stdout, opts.JSONEvents, "user", prompt, cfg.Workspace.SessionID)

	result, err := runHeadlessFeaturePlanner(context.Background(), featurePlannerRequest{
		Config:                cfg,
		ConfigPath:            opts.ConfigPath,
		Prompt:                prompt,
		PromptFile:            opts.PromptFile,
		Resume:                opts.Resume,
		SessionID:             cfg.Workspace.SessionID,
		JSONEvents:            opts.JSONEvents,
		MaxPlanningIterations: opts.MaxPlanningIterations,
		MaxAtoms:              opts.MaxAtoms,
		EffortRoot:            opts.EffortRoot,
		EffortID:              opts.EffortID,
		Deps:                  deps,
	}, stdout)
	if err != nil {
		fields := map[string]any{
			"session_id": cfg.Workspace.SessionID,
			"error":      err.Error(),
		}
		logFeaturePlannerPhase(rec, "feature_planner_error", fields)
		emitFeaturePlannerJSONEvent(stdout, opts.JSONEvents, "error", fields)
		fmt.Fprintln(stderr, err)
		return 1
	}

	completeFields := map[string]any{
		"session_id": cfg.Workspace.SessionID,
	}
	for key, value := range result.Fields {
		completeFields[key] = value
	}
	logFeaturePlannerPhase(rec, "feature_planner_complete", completeFields)
	emitFeaturePlannerJSONEvent(stdout, opts.JSONEvents, "complete", completeFields)
	emitFeaturePlannerMessage(stdout, opts.JSONEvents, "assistant", result.Answer, cfg.Workspace.SessionID)
	if !opts.JSONEvents && strings.TrimSpace(result.Answer) != "" {
		fmt.Fprintln(stdout, result.Answer)
	}
	return 0
}

// loadProductWorkflowBrief loads product workflow artifacts from an effort
// folder and populates a ProductWorkflowBrief for the feature-planner.
// Returns nil if effortRoot is empty (standalone mode).
func loadProductWorkflowBrief(effortRoot string) (*featureplanner.ProductWorkflowBrief, error) {
	if strings.TrimSpace(effortRoot) == "" {
		return nil, nil
	}
	root := filepath.Clean(effortRoot)

	// Load workflow_state.json
	stateData, err := os.ReadFile(filepath.Join(root, "workflow_state.json"))
	if err != nil {
		return nil, fmt.Errorf("load workflow state: %w", err)
	}
	workflowState, err := productworkflow.ParseWorkflowStateJSON(stateData)
	if err != nil {
		return nil, fmt.Errorf("parse workflow state: %w", err)
	}

	// Load PRD
	prdData, err := os.ReadFile(filepath.Join(root, "prd.md"))
	if err != nil {
		return nil, fmt.Errorf("load prd.md: %w", err)
	}
	prd, err := productworkflow.ParsePRDMarkdown(prdData)
	if err != nil {
		return nil, fmt.Errorf("parse prd.md: %w", err)
	}

	// Load TDD ledger
	tddData, err := os.ReadFile(filepath.Join(root, "tdd.json"))
	if err != nil {
		return nil, fmt.Errorf("load tdd.json: %w", err)
	}
	tdd, err := productworkflow.ParseTDDLedgerJSON(tddData)
	if err != nil {
		return nil, fmt.Errorf("parse tdd.json: %w", err)
	}

	// Load ADR index
	adrIndexData, err := os.ReadFile(filepath.Join(root, "adrs", "index.json"))
	var adrIndex productworkflow.ADRIndex
	var parsedADRs []productworkflow.ParsedADR
	if err == nil {
		adrIndex, err = productworkflow.ParseADRIndexJSON(adrIndexData)
		if err != nil {
			return nil, fmt.Errorf("parse adrs/index.json: %w", err)
		}
		for _, entry := range adrIndex.ADRs {
			if entry.Status != "accepted" || strings.TrimSpace(entry.Path) == "" {
				continue
			}
			adrData, adrErr := os.ReadFile(filepath.Join(root, "adrs", entry.Path))
			if adrErr != nil {
				continue
			}
			adrDoc, adrErr := productworkflow.ParseADRMarkdown(adrData)
			if adrErr != nil {
				continue
			}
			parsedADRs = append(parsedADRs, productworkflow.ParsedADR{
				Doc:         adrDoc,
				RelPath:     filepath.Join("adrs", entry.Path),
				RawMarkdown: string(adrData),
			})
		}
	}

	// Load traceability matrix
	var traceability productworkflow.TraceabilityMatrix
	matrixData, matrixErr := os.ReadFile(filepath.Join(root, "evidence", "traceability_matrix.json"))
	if matrixErr == nil {
		traceability, _ = productworkflow.ParseTraceabilityMatrixJSON(matrixData)
	}
	var implementationRecords []productworkflow.ImplementationLedgerRecord
	if data, ledgerErr := os.ReadFile(filepath.Join(root, "implementation_ledger.jsonl")); ledgerErr == nil {
		implementationRecords, _ = productworkflow.ParseImplementationLedgerJSONL(data)
	}

	// Evaluate coverage gate
	knownTDDIDs := productworkflow.BuildKnownTDDObligationIDs(tdd, implementationRecords, prd.FrontMatter.TaskID)
	coverage := productworkflow.EvaluateCoverageGate(prd, adrIndex, parsedADRs, tdd, traceability, knownTDDIDs)

	// Build the brief
	brief := &featureplanner.ProductWorkflowBrief{
		EffortID:           workflowState.EffortID,
		TaskID:             workflowState.TaskID,
		EffortRoot:         effortRoot,
		CoverageTier:       tdd.CoverageTier,
		CoverageGatePassed: coverage.Status == "pass",
		ArtifactRootPath:   effortRoot,
	}

	for _, req := range prd.Requirements {
		id := strings.TrimSpace(req.ID)
		if id == "" {
			continue
		}
		brief.PRDRequirements = append(brief.PRDRequirements, featureplanner.PRDRequirementBrief{
			ID:          id,
			Priority:    strings.TrimSpace(req.Priority),
			Statement:   strings.TrimSpace(req.Statement),
			Testability: strings.TrimSpace(req.Testability),
			Metric:      strings.TrimSpace(req.Metric),
			Obligations: append([]string{}, req.TDDObligations...),
		})
		if strings.TrimSpace(req.Statement) != "" {
			brief.RequiredBehavior = appendUniqueFeaturePlannerStrings(brief.RequiredBehavior, fmt.Sprintf("%s: %s", id, strings.TrimSpace(req.Statement)))
		}
		if strings.TrimSpace(req.Testability) != "" {
			brief.Acceptance = appendUniqueFeaturePlannerStrings(brief.Acceptance, fmt.Sprintf("%s testability: %s", id, strings.TrimSpace(req.Testability)))
		}
		if strings.TrimSpace(req.Metric) != "" {
			brief.Acceptance = appendUniqueFeaturePlannerStrings(brief.Acceptance, fmt.Sprintf("%s metric: %s", id, strings.TrimSpace(req.Metric)))
		}
	}

	for _, entry := range adrIndex.ADRs {
		if entry.Status != "accepted" {
			continue
		}
		brief.AcceptedADRIDs = append(brief.AcceptedADRIDs, entry.ADRID)
		brief.AcceptedADRPaths = append(brief.AcceptedADRPaths, filepath.Join("adrs", entry.Path))
	}

	for _, obl := range tdd.Obligations {
		id := strings.TrimSpace(obl.ID)
		if id == "" {
			continue
		}
		brief.TDDObligations = append(brief.TDDObligations, featureplanner.TDDObligationBrief{
			ID:             id,
			Behavior:       strings.TrimSpace(obl.Behavior),
			Command:        strings.TrimSpace(obl.Command),
			RequiredBefore: strings.TrimSpace(obl.RequiredBefore),
			Status:         strings.TrimSpace(obl.Status),
		})
		if strings.TrimSpace(obl.Behavior) != "" {
			brief.RequiredBehavior = appendUniqueFeaturePlannerStrings(brief.RequiredBehavior, fmt.Sprintf("%s: %s", id, strings.TrimSpace(obl.Behavior)))
		}
		if strings.TrimSpace(obl.Command) != "" {
			brief.Tests = appendUniqueFeaturePlannerStrings(brief.Tests, strings.TrimSpace(obl.Command))
			brief.Acceptance = appendUniqueFeaturePlannerStrings(brief.Acceptance, fmt.Sprintf("%s passes before implementation dispatch", strings.TrimSpace(obl.Command)))
		}
	}

	for _, b := range coverage.Blockers {
		brief.CoverageBlockers = append(brief.CoverageBlockers, b.Message)
	}
	for _, adr := range parsedADRs {
		adrID := strings.TrimSpace(adr.Doc.FrontMatter.ADRID)
		if adrID == "" {
			continue
		}
		if decision := productWorkflowSectionExcerpt(adr.Doc.BodyMarkdown, "Decision"); decision != "" {
			brief.Decisions = appendUniqueFeaturePlannerStrings(brief.Decisions, fmt.Sprintf("%s (%s): %s", adrID, adr.RelPath, decision))
		}
	}
	brief.Constraints = appendUniqueFeaturePlannerStrings(brief.Constraints,
		"Preserve accepted PRD requirements and ADR decisions unless a new accepted ADR supersedes them.",
		"Do not mutate product workflow artifacts while implementing the feature atom.",
	)
	if len(implementationRecords) > 0 {
		gen := productworkflow.GenerateImplementationBrief(productworkflow.BriefGenerationInput{
			EffortRoot:            root,
			ArtifactRootPath:      root,
			WorkflowState:         workflowState,
			PRD:                   prd,
			ADRIndex:              adrIndex,
			ADRs:                  parsedADRs,
			TDD:                   tdd,
			Traceability:          traceability,
			ImplementationRecords: implementationRecords,
			CoverageEvaluation:    coverage,
		})
		brief.FilesToModify = appendUniqueFeaturePlannerStrings(brief.FilesToModify, gen.FilesToModify...)
		brief.RequiredBehavior = appendUniqueFeaturePlannerStrings(brief.RequiredBehavior, gen.RequiredBehavior...)
		brief.Tests = appendUniqueFeaturePlannerStrings(brief.Tests, gen.Tests...)
		brief.Acceptance = appendUniqueFeaturePlannerStrings(brief.Acceptance, gen.Acceptance...)
		brief.Constraints = appendUniqueFeaturePlannerStrings(brief.Constraints, gen.Constraints...)
		brief.Decisions = appendUniqueFeaturePlannerStrings(brief.Decisions, gen.Decisions...)
		brief.Blockers = appendUniqueFeaturePlannerStrings(brief.Blockers, gen.Blockers...)
	}

	return brief, nil
}

func appendUniqueFeaturePlannerStrings(dst []string, values ...string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(dst)+len(values))
	for _, value := range append(dst, values...) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func productWorkflowSectionExcerpt(markdown string, section string) string {
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
	joined := strings.Join(parts, " ")
	if len(joined) <= 220 {
		return joined
	}
	return strings.TrimSpace(joined[:220]) + "..."
}

func defaultHeadlessFeaturePlanner(ctx context.Context, req featurePlannerRequest, stdout io.Writer) (featurePlannerResult, error) {
	state, err := loadOrCreateFeaturePlannerState(req)
	if err != nil {
		return featurePlannerResult{}, err
	}
	resumeAnswer := ""
	if strings.TrimSpace(req.Resume) != "" && hasOpenFeaturePlannerQuestion(state) {
		resumeAnswer = req.Prompt
	}
	effortRoot := ""
	if strings.TrimSpace(req.EffortRoot) != "" {
		effortRoot = req.EffortRoot
	}
	pwBrief, briefErr := loadProductWorkflowBrief(effortRoot)
	if briefErr != nil {
		fmt.Fprintf(stdout, "feature-planner: product workflow context unavailable: %v\n", briefErr)
	}

	result, err := featureplanner.Run(state, featureplanner.RunOptions{
		Prompt:                req.Prompt,
		PromptFile:            req.PromptFile,
		MaxPlanningIterations: req.MaxPlanningIterations,
		MaxAtoms:              req.MaxAtoms,
		ResumeAnswer:          resumeAnswer,
		Now:                   time.Now().UTC(),
		ProductWorkflowBrief:  pwBrief,
	}, func(prompt string, taskFamily string) (string, error) {
		if taskFamily == "feature_planner_implementor" {
			emitPlanExecuteGitHistoryRequirement(req, prompt, stdout)
			return runFeaturePlannerPlanExecute(ctx, req, prompt, stdout)
		}
		return runFeaturePlannerModelTurn(req, prompt, taskFamily, stdout)
	})
	if err != nil {
		return featurePlannerResult{}, err
	}
	if req.JSONEvents {
		featureplanner.WriteEvents(stdout, result.Events)
		if len(result.State.Blockers) > 0 {
			emitFeaturePlannerJSONEvent(stdout, true, "blocked", map[string]any{
				"session_id": result.State.SessionID,
				"status":     result.State.Status,
				"blockers":   result.State.Blockers,
			})
		}
	}
	eventsPath, err := featureplanner.AppendEvents(req.Config.Workspace.Root, result.State.SessionID, result.Events)
	if err != nil {
		return featurePlannerResult{}, err
	}
	result.State.EventsPath = eventsPath
	statePath, err := featureplanner.SaveState(req.Config.Workspace.Root, &result.State)
	if err != nil {
		return featurePlannerResult{}, err
	}
	fields := map[string]any{
		"session_id":         result.State.SessionID,
		"status":             result.State.Status,
		"state_path":         statePath,
		"atom_count":         len(result.State.Atoms),
		"open_questions":     countOpenFeaturePlannerQuestions(result.State),
		"next_valid_actions": result.State.DerivedNextValidActions,
	}
	answer := featurePlannerAnswer(result.State, statePath)
	return featurePlannerResult{Answer: answer, Fields: fields}, nil
}

func defaultFeaturePlannerModelTurn(req featurePlannerRequest, prompt string, taskFamily string, stdout io.Writer) (string, error) {
	emitFeaturePlannerMessage(stdout, req.JSONEvents, "feature_planner_prompt", prompt, req.SessionID)
	timeout := time.Duration(req.Config.Chat.RequestTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	client := llama.NewClientForServer(req.Config.LlamaServer, timeout)
	thinking := false
	answer, err := client.CompleteChat(context.Background(), []llama.ChatMessage{{Role: "user", Content: prompt}}, llama.ChatOptions{
		Model:              req.Config.LlamaServer.Model,
		Temperature:        req.Config.Chat.Temperature,
		TopP:               req.Config.Chat.TopP,
		TopK:               req.Config.Chat.TopK,
		MinP:               req.Config.Chat.MinP,
		PresencePenalty:    req.Config.Chat.PresencePenalty,
		RepetitionPenalty:  req.Config.Chat.RepetitionPenalty,
		EnableThinking:     &thinking,
		MaxTokens:          16384,
		ResponseFormatJSON: taskFamily == "feature_planner_planner",
	})
	if err != nil {
		return "", err
	}
	emitFeaturePlannerMessage(stdout, req.JSONEvents, "feature_planner_model", answer, req.SessionID)
	return answer, nil
}

func defaultFeaturePlannerPlanExecute(_ context.Context, req featurePlannerRequest, prompt string, stdout io.Writer) (string, error) {
	promptFile, err := os.CreateTemp("", "idiom-feature-planner-atom-*.md")
	if err != nil {
		return "", fmt.Errorf("create plan-execute prompt file: %w", err)
	}
	promptPath := promptFile.Name()
	defer os.Remove(promptPath)
	if _, err := promptFile.WriteString(prompt); err != nil {
		_ = promptFile.Close()
		return "", fmt.Errorf("write plan-execute prompt file: %w", err)
	}
	if err := promptFile.Close(); err != nil {
		return "", fmt.Errorf("close plan-execute prompt file: %w", err)
	}

	configPath := strings.TrimSpace(req.ConfigPath)
	if configPath == "" {
		configPath = "config.json"
	}
	var planStdout bytes.Buffer
	var planStderr bytes.Buffer
	if req.Deps.RunPlanExecute == nil {
		return "", fmt.Errorf("plan-execute atom dispatch is unavailable")
	}
	code := req.Deps.RunPlanExecute([]string{
		"-config", configPath,
		"-prompt-file", promptPath,
		"-json",
		"-skip-review-repair",
	}, &planStdout, &planStderr)
	if req.JSONEvents && stdout != nil {
		_, _ = io.Copy(stdout, bytes.NewReader(planStdout.Bytes()))
	}
	if code != 0 {
		return "", fmt.Errorf("plan-execute atom dispatch failed with exit %d: %s", code, strings.TrimSpace(planStderr.String()))
	}
	if req.JSONEvents && stdout != nil {
		emitFeaturePlannerInternalJSONEvent(stdout, "internal_idiom.plan_execute.completed", map[string]any{
			"session_id":    req.SessionID,
			"tool":          featureplanner.ToolPlanExecuteComplete,
			"lane_family":   "plan-execute",
			"summary":       strings.TrimSpace(planStdout.String()),
			"progress_path": featurePlannerPlanExecuteProgressPath(planStdout.String()),
		})
	}
	payload := map[string]any{
		"tool":        featureplanner.ToolPlanExecuteComplete,
		"lane_family": "plan-execute",
		"summary":     strings.TrimSpace(planStdout.String()),
	}
	if progressPath := featurePlannerPlanExecuteProgressPath(planStdout.String()); progressPath != "" {
		payload["progress_path"] = progressPath
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func emitPlanExecuteGitHistoryRequirement(req featurePlannerRequest, prompt string, stdout io.Writer) {
	if !req.JSONEvents || stdout == nil || !featureplanner.PlanExecuteGitHistoryRequirementPresent(prompt) {
		return
	}
	fields := featureplanner.PlanExecuteGitHistoryRequirementFields()
	fields["session_id"] = req.SessionID
	emitFeaturePlannerInternalJSONEvent(stdout, featureplanner.EventPlanExecuteGitHistoryRequirement, fields)
}

func featurePlannerPlanExecuteProgressPath(output string) string {
	var progressPath string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var record struct {
			Type      string `json:"type"`
			StatePath string `json:"state_path"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		if strings.HasPrefix(record.Type, "idiom.plan_execute.") && strings.TrimSpace(record.StatePath) != "" {
			progressPath = strings.TrimSpace(record.StatePath)
		}
	}
	return progressPath
}

func emitFeaturePlannerMessage(stdout io.Writer, jsonEvents bool, role string, content string, sessionID string) {
	if !jsonEvents {
		return
	}
	payload := map[string]any{
		"type":       "message",
		"role":       role,
		"content":    content,
		"session_id": sessionID,
		"ts":         time.Now().UTC().Format(time.RFC3339Nano),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fmt.Fprintln(stdout, string(body))
}

func loadOrCreateFeaturePlannerState(req featurePlannerRequest) (featureplanner.FeatureState, error) {
	if strings.TrimSpace(req.Resume) != "" {
		state, _, err := featureplanner.LoadState(req.Config.Workspace.Root, req.Resume)
		return state, err
	}
	return featureplanner.NewState(req.SessionID, req.Prompt, req.PromptFile, req.MaxPlanningIterations, time.Now().UTC()), nil
}

func hasOpenFeaturePlannerQuestion(state featureplanner.FeatureState) bool {
	for _, question := range state.OpenQuestions {
		if question.Status == "open" {
			return true
		}
	}
	return false
}

func featurePlannerAnswer(state featureplanner.FeatureState, statePath string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "feature-planner: %s\n", state.Status)
	if statePath != "" {
		fmt.Fprintf(&b, "state: %s\n", statePath)
	}
	if len(state.Atoms) > 0 {
		fmt.Fprintf(&b, "atoms: %d\n", len(state.Atoms))
		var atomIDs []string
		for _, atom := range state.Atoms {
			atomIDs = append(atomIDs, atom.ID)
		}
		fmt.Fprintf(&b, "atom_ids: %s\n", strings.Join(atomIDs, ", "))
	}
	if len(state.Plan.SDDContract.Refs) > 0 {
		fmt.Fprintf(&b, "sdd_refs: %s\n", strings.Join(state.Plan.SDDContract.Refs, ", "))
	}
	if len(state.Plan.TDDContract.Obligations) > 0 {
		var tddIDs []string
		for _, obligation := range state.Plan.TDDContract.Obligations {
			tddIDs = append(tddIDs, obligation.ID)
		}
		fmt.Fprintf(&b, "tdd_obligations: %s\n", strings.Join(tddIDs, ", "))
	}
	if len(state.OpenQuestions) > 0 {
		openCount := countOpenFeaturePlannerQuestions(state)
		if openCount > 0 {
			fmt.Fprintf(&b, "open questions: %d\n", openCount)
		} else {
			fmt.Fprintf(&b, "answered questions: %d\n", len(state.OpenQuestions))
		}
		for _, question := range state.OpenQuestions {
			if strings.TrimSpace(question.QuestionSummary) != "" {
				fmt.Fprintf(&b, "question: %s\n", question.QuestionSummary)
			}
			if strings.TrimSpace(question.Answer) != "" {
				fmt.Fprintf(&b, "answer: %s\n", question.Answer)
			}
		}
	}
	if len(state.Blockers) > 0 {
		fmt.Fprintf(&b, "blockers: %s\n", strings.Join(state.Blockers, "; "))
	}
	if len(state.DerivedNextValidActions) > 0 {
		fmt.Fprintf(&b, "next: %s\n", strings.Join(state.DerivedNextValidActions, ", "))
	}
	return strings.TrimSpace(b.String())
}

func countOpenFeaturePlannerQuestions(state featureplanner.FeatureState) int {
	open := 0
	for _, question := range state.OpenQuestions {
		if question.Status == "open" {
			open++
		}
	}
	return open
}

func parseFeaturePlannerOptions(args []string, stderr io.Writer) (featurePlannerOptions, error) {
	var opts featurePlannerOptions
	fs := flag.NewFlagSet("idiom feature-planner", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.ConfigPath, "config", "config.json", "path to config file")
	fs.StringVar(&opts.Prompt, "prompt", "", "feature planning prompt")
	fs.StringVar(&opts.PromptFile, "prompt-file", "", "read feature planning prompt from a file")
	fs.BoolVar(&opts.JSONEvents, "json", false, "emit structured JSONL events to stdout")
	fs.StringVar(&opts.Resume, "resume", "", "resume a prior feature-planner session by id before running")
	fs.StringVar(&opts.Model, "model", "", "override llama_server.model from config")
	fs.IntVar(&opts.MaxPlanningIterations, "max-planning-iterations", 3, "maximum planner repair/question iterations before marking planning stuck")
	fs.IntVar(&opts.MaxAtoms, "max-atoms", 0, "maximum queued atoms to dispatch in this invocation; 0 dispatches all ready atoms")
	fs.StringVar(&opts.EffortRoot, "effort-root", "", "path to product workflow effort folder")
	fs.StringVar(&opts.EffortID, "effort-id", "", "product workflow effort ID")
	if err := fs.Parse(args); err != nil {
		return featurePlannerOptions{}, err
	}
	if fs.NArg() > 0 {
		if strings.TrimSpace(opts.Prompt) != "" || strings.TrimSpace(opts.PromptFile) != "" {
			return featurePlannerOptions{}, fmt.Errorf("positional prompt cannot be combined with --prompt or --prompt-file")
		}
		opts.Prompt = strings.Join(fs.Args(), " ")
	}
	if strings.TrimSpace(opts.Prompt) == "" && strings.TrimSpace(opts.PromptFile) == "" {
		return featurePlannerOptions{}, fmt.Errorf("provide --prompt, --prompt-file, or a positional prompt")
	}
	if strings.TrimSpace(opts.Prompt) != "" && strings.TrimSpace(opts.PromptFile) != "" {
		return featurePlannerOptions{}, fmt.Errorf("--prompt and --prompt-file are mutually exclusive")
	}
	return opts, nil
}

func featurePlannerPrompt(opts featurePlannerOptions) (string, error) {
	hasPrompt := strings.TrimSpace(opts.Prompt) != ""
	hasFile := strings.TrimSpace(opts.PromptFile) != ""
	if hasPrompt && hasFile {
		return "", fmt.Errorf("--prompt and --prompt-file are mutually exclusive")
	}
	if !hasPrompt && !hasFile {
		return "", fmt.Errorf("provide --prompt, --prompt-file, or a positional prompt")
	}
	if hasPrompt {
		return opts.Prompt, nil
	}
	data, err := os.ReadFile(opts.PromptFile)
	if err != nil {
		return "", fmt.Errorf("read prompt file: %w", err)
	}
	return strings.TrimRight(string(data), "\r\n"), nil
}

func emitFeaturePlannerJSONEvent(stdout io.Writer, jsonEvents bool, eventType string, fields map[string]any) {
	if !jsonEvents {
		return
	}
	payload := map[string]any{
		"type": "idiom.feature_planner." + eventType,
		"ts":   time.Now().UTC().Format(time.RFC3339Nano),
	}
	for key, value := range fields {
		payload[key] = value
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fmt.Fprintln(stdout, string(body))
}

func emitFeaturePlannerInternalJSONEvent(stdout io.Writer, eventType string, fields map[string]any) {
	payload := map[string]any{
		"type": strings.TrimSpace(eventType),
		"ts":   time.Now().UTC().Format(time.RFC3339Nano),
	}
	maps.Copy(payload, fields)
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fmt.Fprintln(stdout, string(body))
}

func resolveDependencies(overrides ...Dependencies) Dependencies {
	deps := Dependencies{
		NewSessionID:            defaultFeaturePlannerSessionID,
		LocalOpsTelemetryFields: func(config.Config, string) map[string]any { return nil },
	}
	if len(overrides) > 0 {
		merged := overrides[0]
		if merged.NewSessionID != nil {
			deps.NewSessionID = merged.NewSessionID
		}
		if merged.LocalOpsTelemetryFields != nil {
			deps.LocalOpsTelemetryFields = merged.LocalOpsTelemetryFields
		}
		if merged.RunPlanExecute != nil {
			deps.RunPlanExecute = merged.RunPlanExecute
		}
	}
	return deps
}

func defaultFeaturePlannerSessionID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("ts%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}

func logFeaturePlannerPhase(rec *telemetry.Recorder, eventType string, fields map[string]any) {
	if rec == nil {
		return
	}
	if fields == nil {
		fields = map[string]any{}
	}
	_ = rec.Log(eventType, fields)
}
