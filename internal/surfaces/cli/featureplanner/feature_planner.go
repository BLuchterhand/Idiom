package featureplanner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	featureplannercontract "idiom/internal/contracts/featureplanner"
	lanecontract "idiom/internal/contracts/lane"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/platform/jsonevents"
	"idiom/internal/platform/sessionid"
	"idiom/internal/platform/textutil"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
	"idiom/internal/workflows/featureplanner"
	planexecuteadapter "idiom/internal/workflows/featureplanner/adapters/planexecute"
	"idiom/internal/workflows/featureplannerproduct"
	"idiom/internal/workflows/featureplannerruntime"
	coreplanexecute "idiom/internal/workflows/planexecute"
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
	Config                       config.Config
	ConfigPath                   string
	Prompt                       string
	PromptFile                   string
	Resume                       string
	SessionID                    string
	JSONEvents                   bool
	MaxPlanningIterations        int
	MaxAtoms                     int
	EffortRoot                   string
	EffortID                     string
	ProductWriteEnvelope         []string
	ProductWriteEnvelopeExplicit bool
	Deps                         Dependencies
}

type featurePlannerResult struct {
	Answer string
	Fields map[string]any
}

type Dependencies struct {
	NewSessionID            func() string
	LocalOpsTelemetryFields func(config.Config, string) map[string]any
	RunPlanExecute          PlanExecuteRunner
}

type PlanExecuteRunner func(config.Config, coreplanexecute.Options, io.Writer, io.Writer) coreplanexecute.RunResult

type featurePlannerRunner func(context.Context, featurePlannerRequest, io.Writer) (featurePlannerResult, error)

var runHeadlessFeaturePlanner featurePlannerRunner = defaultHeadlessFeaturePlanner
var runFeaturePlannerModelTurn featurePlannerModelTurnRunner = defaultFeaturePlannerModelTurn
var runFeaturePlannerPlanExecute = defaultFeaturePlannerPlanExecute

type featurePlannerModelTurnRunner func(context.Context, featurePlannerRequest, string, string, io.Writer) (string, error)

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
	telemetry.LogPhase(rec, "feature_planner_start", startFields)
	if err := emitFeaturePlannerJSONEvent(stdout, opts.JSONEvents, featureplannercontract.EventStart, startFields); err != nil {
		fmt.Fprintf(stderr, "feature-planner: write json event: %v\n", err)
		return 1
	}
	if err := emitFeaturePlannerMessage(stdout, opts.JSONEvents, "user", prompt, cfg.Workspace.SessionID); err != nil {
		fmt.Fprintf(stderr, "feature-planner: write json message: %v\n", err)
		return 1
	}

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
		telemetry.LogPhase(rec, "feature_planner_error", fields)
		if emitErr := emitFeaturePlannerJSONEvent(stdout, opts.JSONEvents, featureplannercontract.EventError, fields); emitErr != nil {
			fmt.Fprintf(stderr, "feature-planner: write json event: %v\n", emitErr)
		}
		fmt.Fprintln(stderr, err)

		return 1
	}

	completeFields := map[string]any{
		"session_id": cfg.Workspace.SessionID,
	}
	for key, value := range result.Fields {
		completeFields[key] = value
	}

	telemetry.LogPhase(rec, "feature_planner_complete", completeFields)
	if err := emitFeaturePlannerJSONEvent(stdout, opts.JSONEvents, featureplannercontract.EventComplete, completeFields); err != nil {
		fmt.Fprintf(stderr, "feature-planner: write json event: %v\n", err)
		return 1
	}
	if err := emitFeaturePlannerMessage(stdout, opts.JSONEvents, "assistant", result.Answer, cfg.Workspace.SessionID); err != nil {
		fmt.Fprintf(stderr, "feature-planner: write json message: %v\n", err)
		return 1
	}

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

	brief, err := featureplannerproduct.LoadBriefFromEffort(filepath.Clean(effortRoot))
	if err != nil {
		return nil, err
	}

	return brief, nil
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

	effortRoot := featurePlannerEffortRoot(req, state)

	pwBrief, briefErr := loadProductWorkflowBrief(effortRoot)
	if briefErr != nil {
		return featurePlannerResult{}, fmt.Errorf("feature-planner product workflow context unavailable: %w", briefErr)
	}

	if pwBrief != nil {
		if err := validateFeaturePlannerProductContext(req, state, *pwBrief); err != nil {
			return featurePlannerResult{}, err
		}
		if err := featureplanner.BindProductTaskIdentity(&state, pwBrief.TaskID); err != nil {
			return featurePlannerResult{}, fmt.Errorf("feature-planner product workflow context mismatch: %w", err)
		}

		state.EffortRoot = strings.TrimSpace(effortRoot)
		state.EffortID = textutil.FirstNonEmpty(strings.TrimSpace(req.EffortID), strings.TrimSpace(pwBrief.EffortID), strings.TrimSpace(state.EffortID))
		req.ProductWriteEnvelope = featureplanner.ProductWriteEnvelopePaths(pwBrief)
		req.ProductWriteEnvelopeExplicit = true

		if pwBrief.IsClosedSourceSatisfied() {
			return closeFeaturePlannerSourceSatisfied(req, state, *pwBrief, stdout)
		}
	}

	result, err := featureplanner.Run(state, featureplanner.RunOptions{
		Prompt:                req.Prompt,
		PromptFile:            req.PromptFile,
		MaxPlanningIterations: req.MaxPlanningIterations,
		MaxAtoms:              req.MaxAtoms,
		ResumeAnswer:          resumeAnswer,
		Now:                   time.Now().UTC(),
		ProductWorkflowBrief:  pwBrief,
		WorkspaceRoot:         req.Config.Workspace.Root,
		ReconcileActiveRuns: func(state *featureplanner.FeatureState) []featureplanner.Event {
			return planexecuteadapter.ReconcileActiveRuns(state, req.Config.Workspace.Root)
		},
	}, func(prompt string, taskFamily string) (string, error) {
		if workerruntime.TaskFamilyIsFeaturePlannerImplementor(taskFamily) {
			return runFeaturePlannerPlanExecute(ctx, req, prompt, stdout)
		}

		return runFeaturePlannerModelTurn(ctx, req, prompt, taskFamily, stdout)
	})
	if err != nil {
		return featurePlannerResult{}, err
	}
	if pwBrief != nil {
		if _, err := featureplannerproduct.FinalizeSourceSatisfiedDeliveryFromFeatureState(pwBrief, result.State); err != nil {
			return featurePlannerResult{}, fmt.Errorf("record product source-satisfied delivery: %w", err)
		}
	}

	if req.JSONEvents {
		featureplanner.WriteEvents(stdout, result.Events)

		if len(result.State.Blockers) > 0 {
			if err := emitFeaturePlannerJSONEvent(stdout, true, featureplannercontract.EventBlocked, map[string]any{
				"session_id": result.State.SessionID,
				"status":     result.State.Status,
				"blockers":   result.State.Blockers,
			}); err != nil {
				return featurePlannerResult{}, fmt.Errorf("write feature-planner blocked json event: %w", err)
			}
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
		"open_questions":     featureplanner.CountOpenPlannerQuestions(result.State),
		"next_valid_actions": result.State.DerivedNextValidActions,
	}
	answer := featureplanner.FormatStateSummary(result.State, statePath)

	return featurePlannerResult{Answer: answer, Fields: fields}, nil
}

func closeFeaturePlannerSourceSatisfied(req featurePlannerRequest, state featureplanner.FeatureState, brief featureplanner.ProductWorkflowBrief, stdout io.Writer) (featurePlannerResult, error) {
	state, event := featureplanner.ProductSourceSatisfiedCloseout(state, brief, req.EffortID)
	if req.JSONEvents {
		featureplanner.WriteEvents(stdout, []featureplanner.Event{event})
	}

	eventsPath, err := featureplanner.AppendEvents(req.Config.Workspace.Root, state.SessionID, []featureplanner.Event{event})
	if err != nil {
		return featurePlannerResult{}, err
	}

	state.EventsPath = eventsPath

	statePath, err := featureplanner.SaveState(req.Config.Workspace.Root, &state)
	if err != nil {
		return featurePlannerResult{}, err
	}

	fields := map[string]any{
		"session_id":         state.SessionID,
		"status":             state.Status,
		"state_path":         statePath,
		"atom_count":         len(state.Atoms),
		"open_questions":     featureplanner.CountOpenPlannerQuestions(state),
		"next_valid_actions": state.DerivedNextValidActions,
		"source_satisfied":   true,
		"effort_id":          strings.TrimSpace(brief.EffortID),
		"current_phase":      strings.TrimSpace(brief.CurrentPhase),
		"current_state":      strings.TrimSpace(brief.CurrentState),
	}

	return featurePlannerResult{Answer: featureplanner.FormatStateSummary(state, statePath), Fields: fields}, nil
}

func featurePlannerEffortRoot(req featurePlannerRequest, state featureplanner.FeatureState) string {
	if effortRoot := strings.TrimSpace(req.EffortRoot); effortRoot != "" {
		return effortRoot
	}

	return strings.TrimSpace(state.EffortRoot)
}

func validateFeaturePlannerProductContext(req featurePlannerRequest, state featureplanner.FeatureState, brief featureplanner.ProductWorkflowBrief) error {
	briefEffortID := strings.TrimSpace(brief.EffortID)
	if requestedEffortID := strings.TrimSpace(req.EffortID); requestedEffortID != "" && briefEffortID != "" && requestedEffortID != briefEffortID {
		return fmt.Errorf("feature-planner product workflow context mismatch: requested effort_id %q but effort root loaded %q", requestedEffortID, briefEffortID)
	}

	if strings.TrimSpace(req.Resume) == "" {
		return nil
	}

	stateEffortID := strings.TrimSpace(state.EffortID)
	if stateEffortID != "" && briefEffortID != "" && stateEffortID != briefEffortID {
		return fmt.Errorf("feature-planner product workflow context mismatch: resumed state effort_id %q but effort root loaded %q", stateEffortID, briefEffortID)
	}
	return nil
}

func defaultFeaturePlannerModelTurn(ctx context.Context, req featurePlannerRequest, prompt string, taskFamily string, stdout io.Writer) (string, error) {
	timeout := time.Duration(req.Config.Chat.RequestTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}

	client := llama.NewClientForServer(req.Config.LlamaServer, timeout)
	opts := llama.ChatOptions{
		Model:             req.Config.LlamaServer.Model,
		Temperature:       req.Config.Chat.Temperature,
		TopP:              req.Config.Chat.TopP,
		TopK:              req.Config.Chat.TopK,
		MinP:              req.Config.Chat.MinP,
		PresencePenalty:   req.Config.Chat.PresencePenalty,
		RepetitionPenalty: req.Config.Chat.RepetitionPenalty,
	}

	var emitErr error
	answer, err := featureplannerruntime.CompleteModelTurn(ctx, client, prompt, opts, taskFamily, func(role string, content string) {
		if emitErr != nil {
			return
		}
		if err := emitFeaturePlannerMessage(stdout, req.JSONEvents, role, content, req.SessionID); err != nil {
			emitErr = err
		}
	})
	if err != nil {
		return answer, err
	}
	if emitErr != nil {
		return answer, emitErr
	}
	return answer, nil
}

func defaultFeaturePlannerPlanExecute(ctx context.Context, req featurePlannerRequest, prompt string, stdout io.Writer) (string, error) {
	configPath := strings.TrimSpace(req.ConfigPath)
	if configPath == "" {
		configPath = "config.json"
	}

	var (
		planStdout bytes.Buffer
		planStderr bytes.Buffer
	)

	if req.Deps.RunPlanExecute == nil {
		return "", fmt.Errorf("plan-execute atom dispatch is unavailable")
	}

	binding, err := featureplanner.ParsePlanExecuteInvocationBinding(prompt)
	if err != nil {
		return "", err
	}
	planExecuteSessionID := binding.SessionID
	opts := coreplanexecute.Options{
		Context:                   ctx,
		ConfigPath:                configPath,
		Prompt:                    prompt,
		JSONEvents:                true,
		Model:                     strings.TrimSpace(req.Config.LlamaServer.Model),
		SessionID:                 planExecuteSessionID,
		WriteAllowedPaths:         coreplanexecute.MultiFlag(appendUniqueFeaturePlannerStrings(nil, req.ProductWriteEnvelope...)),
		WriteAllowedPathsExplicit: req.ProductWriteEnvelopeExplicit,
		AuthorizedNewFiles:        plancontract.AuthorizedNewFilePathsFromPrompt(prompt),
	}

	runPlanExecute := func() (int, featureplanner.PlanExecuteCompletionSummary) {
		planStdout.Reset()
		planStderr.Reset()
		startedAt := time.Now().UTC()
		result := req.Deps.RunPlanExecute(req.Config, opts, &planStdout, &planStderr)
		if req.JSONEvents && stdout != nil {
			_, _ = io.Copy(stdout, bytes.NewReader(planStdout.Bytes()))
		}

		completion := featureplanner.PlanExecuteCompletionSummaryFromOutput(planStdout.String())
		completion = mergeFeaturePlannerPlanExecuteRunCompletion(completion, result)
		completion = overlayFeaturePlannerPlanExecuteTerminalCompletion(req.Config.Workspace.Root, planExecuteSessionID, completion, startedAt)
		completion = binding.CorrelateCompletion(completion)

		return result.ExitCode, completion
	}

	code, completion := runPlanExecute()
	if code != 0 {
		if retryPrompt, ok := featureplanner.PlanExecuteRecoveryRetryPromptForSummary(prompt, completion); ok {
			opts.Prompt = retryPrompt
			code, completion = runPlanExecute()
		}
	}

	completion.Tool = string(featureplannercontract.ToolPlanExecuteComplete)
	completion.LaneFamily = string(lanecontract.LanePlanExecute)
	completion.Summary = strings.TrimSpace(planStdout.String())

	if strings.TrimSpace(completion.CompletionSource) == "" {
		completion.CompletionSource = string(featureplannercontract.PlanExecuteCompletionSourceHeadlessReturned)
	}

	if code != 0 {
		if completion.ExitCode == 0 {
			completion.ExitCode = code
		}
		completion.Status = textutil.FirstNonEmpty(completion.Status, "failed")
		completion.FailureReason = textutil.FirstNonEmpty(
			completion.FailureReason,
			strings.TrimSpace(fmt.Sprintf("plan-execute atom dispatch failed with exit %d: %s", code, strings.TrimSpace(planStderr.String()))),
		)
		completion = featureplanner.TrustObservedPlanExecuteCompletionSummary(completion)

		return encodeHeadlessPlanExecuteCompletion(req, stdout, completion)
	}

	if strings.TrimSpace(completion.Status) == "" {
		completion.Status = featureplanner.PlanExecuteCompletionStatusAmbiguous
		completion.FailureKind = textutil.FirstNonEmpty(completion.FailureKind, featureplanner.PlanExecuteCompletionMissingStatusFailureKind)
		completion.FailureReason = textutil.FirstNonEmpty(completion.FailureReason, featureplanner.PlanExecuteCompletionMissingStatusFailureReason)
	}

	completion = featureplanner.TrustObservedPlanExecuteCompletionSummary(completion)

	return encodeHeadlessPlanExecuteCompletion(req, stdout, completion)
}

func mergeFeaturePlannerPlanExecuteRunCompletion(summary featureplanner.PlanExecuteCompletionSummary, result coreplanexecute.RunResult) featureplanner.PlanExecuteCompletionSummary {
	if result.Completion == nil {
		return summary
	}

	return featureplanner.MergePlanExecuteCompletionSummaries(
		summary,
		featureplanner.PlanExecuteCompletionSummaryFromFields(plancontract.PlanExecuteTerminalCompletionEventFields(*result.Completion, result.CompletionPath)),
	)
}

func encodeHeadlessPlanExecuteCompletion(req featurePlannerRequest, stdout io.Writer, completion featureplanner.PlanExecuteCompletionSummary) (string, error) {
	fields := completion.FeaturePlannerPayloadFields()
	if req.JSONEvents && stdout != nil {
		eventFields := map[string]any{"session_id": req.SessionID}
		maps.Copy(eventFields, fields)
		if err := emitFeaturePlannerInternalJSONEvent(stdout, featureplanner.PlanExecuteCompletionEventTypeForSummary(completion), eventFields); err != nil {
			return "", fmt.Errorf("write feature-planner internal json event: %w", err)
		}
	}

	body, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}

	return string(body), nil
}

func appendUniqueFeaturePlannerStrings(base []string, values ...string) []string {
	seen := make(map[string]bool, len(base)+len(values))
	for _, value := range base {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || seen[trimmed] {
			continue
		}

		seen[trimmed] = true
	}

	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || seen[trimmed] {
			continue
		}

		base = append(base, trimmed)
		seen[trimmed] = true
	}

	return base
}

func overlayFeaturePlannerPlanExecuteTerminalCompletion(root string, sessionID string, summary featureplanner.PlanExecuteCompletionSummary, invocationStartedAt time.Time) featureplanner.PlanExecuteCompletionSummary {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return summary
	}

	record, path, found, err := coreplanexecute.TryLoadPlanExecuteTerminalCompletionRecord(root, sessionID)
	if err != nil {
		summary.Status = "failed"
		summary.FailureKind = string(plancontract.PlanExecuteFailureTerminalCompletionContractFailed)
		summary.FailureReason = "load plan-execute terminal completion: " + err.Error()

		return summary
	}
	if !found {
		return summary
	}
	if !featurePlannerPlanExecuteTerminalCompletionFresh(root, record, path, invocationStartedAt) {
		return summary
	}

	return featureplanner.MergePlanExecuteCompletionSummaries(
		summary,
		featureplanner.PlanExecuteCompletionSummaryFromFields(plancontract.PlanExecuteTerminalCompletionEventFields(record, path)),
	)
}

func featurePlannerPlanExecuteTerminalCompletionFresh(root string, record plancontract.PlanExecuteTerminalCompletionRecord, path string, invocationStartedAt time.Time) bool {
	if invocationStartedAt.IsZero() {
		return true
	}

	if ts := strings.TrimSpace(record.TS); ts != "" {
		parsed, err := time.Parse(time.RFC3339Nano, ts)
		if err == nil {
			return !parsed.Before(invocationStartedAt)
		}
	}

	statPath := path
	if statPath != "" && !filepath.IsAbs(statPath) {
		statPath = filepath.Join(root, statPath)
	}

	info, err := os.Stat(statPath)
	if err != nil {
		return false
	}

	return !info.ModTime().Before(invocationStartedAt)
}

func emitFeaturePlannerMessage(stdout io.Writer, jsonEvents bool, role string, content string, sessionID string) error {
	if jsonEvents {
		return jsonevents.Emit(stdout, true, "message", map[string]any{
			"role":           role,
			"content_hash":   featurePlannerContentHash(content),
			"content_length": len(content),
			"session_id":     sessionID,
		})
	}

	return jsonevents.Emit(stdout, jsonEvents, "message", map[string]any{
		"role":       role,
		"content":    content,
		"session_id": sessionID,
	})
}

func featurePlannerContentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return fmt.Sprintf("sha256:%x", sum)
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

func emitFeaturePlannerJSONEvent(stdout io.Writer, jsonEvents bool, eventType featureplannercontract.FeaturePlannerEventType, fields map[string]any) error {
	if !featureplannercontract.KnownEventType(eventType) {
		return fmt.Errorf("unknown feature planner event type %q", eventType)
	}

	return jsonevents.Emit(stdout, jsonEvents, featureplannercontract.EventName(eventType), fields)
}

func emitFeaturePlannerInternalJSONEvent(stdout io.Writer, eventType featureplannercontract.NestedPlanExecuteCompletionEventName, fields map[string]any) error {
	if !featureplannercontract.KnownNestedPlanExecuteCompletionEventName(string(eventType)) {
		return fmt.Errorf("unknown nested plan-execute completion event type %q", eventType)
	}

	return jsonevents.Emit(stdout, true, string(eventType), fields)
}

func resolveDependencies(overrides ...Dependencies) Dependencies {
	deps := Dependencies{
		NewSessionID:            sessionid.New,
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
