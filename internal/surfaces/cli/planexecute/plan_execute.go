package planexecute

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"sort"
	"strings"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	"idiom/internal/surfaces/cli/runtime"
	"idiom/internal/workflows/planexecute"
)

// planExecuteOptions configures `idiom plan-execute`.
type planExecuteOptions = planexecute.Options

// RunMain is the CLI entry point for `idiom plan-execute`.
//
// Plan-then-execute is the third lane shape (alongside `idiom implement`
// and the harness-intercept lane): the planner runs FIRST with no
// per-edit write tools, emits a structured plan via submit_plan, and
// the orchestrator's executor phase iterates that plan and dispatches
// each edit deterministically against the workspace. The planner owns
// WHAT to change; the harness owns the plan contract and deterministic
// handoff to the executor.
func RunMain(args []string, stdout io.Writer, stderr io.Writer, promptRun cmdruntime.PromptRunner) int {
	return RunMainWithTelemetry(args, stdout, stderr, promptRun, nil)
}

func RunMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, promptRun cmdruntime.PromptRunner, rec *telemetry.Recorder) int {
	opts, err := parsePlanExecuteOptions(args, stderr)
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
	result := planexecute.Run(cfg, opts, adaptPromptRunner(promptRun), rec, stdout, stderr)
	return result.ExitCode
}

func adaptPromptRunner(promptRun cmdruntime.PromptRunner) planexecute.PromptRunner {
	return func(cfg config.Config, prompt string, opts planexecute.RunOptions, stdout io.Writer) (string, error) {
		return promptRun(cfg, prompt, cmdruntime.RunOptions{
			JSONEvents:                  opts.JSONEvents,
			Resume:                      opts.Resume,
			TaskFamily:                  opts.TaskFamily,
			PlannerOwnedVerification:    opts.PlannerOwnedVerification,
			MaxTokensOverride:           opts.MaxTokensOverride,
			DisableThinking:             opts.DisableThinking,
			ReviewRepairOpenObligations: opts.ReviewRepairOpenObligations,
			Telemetry:                   opts.Telemetry,
			ScopeContract:               opts.ScopeContract,
		}, stdout)
	}
}

func parsePlanExecuteOptions(args []string, stderr io.Writer) (planExecuteOptions, error) {
	return planexecute.ParseOptions(args, stderr)
}

func planExecutePrompt(opts planExecuteOptions) (string, error) {
	return planexecute.PlanExecutePrompt(opts)
}

func planExecuteRequiredEditPaths(opts planExecuteOptions, prompt string) []string {
	return planexecute.PlanExecuteRequiredEditPaths(opts, prompt)
}

func planExecuteAllowedEditPaths(opts planExecuteOptions, prompt string) []string {
	return planexecute.PlanExecuteAllowedEditPaths(opts, prompt)
}

func planExecuteScopeContract(opts planExecuteOptions, prompt string) planexecute.PlanScopeContract {
	return planexecute.PlanExecuteScopeContract(opts, prompt)
}

func inheritPlanExecutePromptPackID(plan *workspace.PlanArtifact, prompt string) string {
	return planexecute.InheritPlanExecutePromptPackID(plan, prompt)
}

func extractPlanExecutePromptPackID(prompt string) string {
	return planexecute.ExtractPlanExecutePromptPackID(prompt)
}

func isPlanExecutePackID(value string) bool {
	return planexecute.IsPlanExecutePackID(value)
}

func patchPlanArtifactPackID(path string, packID string) error {
	return planexecute.PatchPlanArtifactPackID(path, packID)
}

// planExecutePlannerSystemAddendum is prepended to the brief in the
// planner phase. Kept tight by design: local text-tool backends need
// enough visible output budget to emit a parser-readable submit_plan.
// Every rule earned its place from a real failure mode; if you add
// another, compress an existing rule to make room.
func planExecutePlannerSystemAddendum(requiredEditPaths []string, allowedEditPaths []string) string {
	return planexecute.PlanExecutePlannerSystemAddendumWithObligations(requiredEditPaths, allowedEditPaths, nil)
}

func planExecutePlannerSystemAddendumWithObligations(requiredEditPaths []string, allowedEditPaths []string, obligations planexecute.EditSetObligations) string {
	return planexecute.PlanExecutePlannerSystemAddendumWithObligations(requiredEditPaths, allowedEditPaths, obligations)
}

func planExecutePlannerMaxTokens(root string, prompt string, requiredEditPaths []string, allowedEditPaths []string) int {
	return 16384
}

func planExecutePathLooksLikeTest(path string) bool {
	return planexecute.PlanExecutePathLooksLikeTest(path)
}

func planExecuteScopeToBriefBody(prompt string, requiredEditPaths []string, allowedEditPaths []string) string {
	return planexecute.AppendPlanExecuteScopeToBriefBody(prompt, requiredEditPaths, allowedEditPaths)
}

type planExecuteAutoExactPlanResult struct {
	Submitted       bool
	SourceSatisfied bool
	Result          workspace.ToolResult
	Reason          string
	EditCount       int
	AppliedCount    int
	PendingCount    int
	UnknownCount    int
	AppliedPaths    []string
	PendingPaths    []string
	UnknownPaths    []string
}

func planExecuteTryAutoExactPlan(cfg config.Config, prompt string, requiredEditPaths []string) planExecuteAutoExactPlanResult {
	edits, reason := planExecuteExactPlanEditsForWorkspace(prompt, requiredEditPaths, cfg.Workspace.Root)
	if len(edits) == 0 {
		return planExecuteAutoExactPlanResult{Reason: reason}
	}
	classified := planExecuteClassifyAutoExactPlanEdits(cfg.Workspace.Root, edits)
	if classified.UnknownCount > 0 {
		classified.Reason = "exact-anchor progress unknown for required path(s): " + strings.Join(classified.UnknownPaths, ", ")
		classified.EditCount = len(edits)
		return classified
	}
	if classified.PendingCount == 0 {
		classified.SourceSatisfied = true
		classified.Reason = "all exact-anchor edits already match the current workspace"
		classified.EditCount = len(edits)
		return classified
	}
	mgr := workspace.NewManager(cfg.Workspace)
	result := mgr.Execute(context.Background(), workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "Deterministic exact-anchor plan synthesized from the Required Edits packet.",
		Edits:     planExecutePendingAutoExactEdits(cfg.Workspace.Root, edits),
	})
	classified.Result = result
	if strings.TrimSpace(result.Error) != "" {
		classified.Reason = "exact-anchor auto-plan rejected by submit_plan validation"
		classified.EditCount = classified.PendingCount
		return classified
	}
	classified.Submitted = true
	classified.EditCount = classified.PendingCount
	if classified.AppliedCount > 0 {
		classified.Reason = "already-applied exact edits skipped; pending exact edits submitted"
	} else {
		classified.Reason = "all required paths covered by parseable exact old_text/new_text pairs"
	}
	return classified
}

func planExecuteClassifyAutoExactPlanEdits(root string, edits []workspace.PlanEdit) planExecuteAutoExactPlanResult {
	var result planExecuteAutoExactPlanResult
	var appliedPaths []string
	var pendingPaths []string
	var unknownPaths []string
	for _, edit := range edits {
		status := planExecuteAutoExactEditStatus(root, edit)
		path := normalizePlanExecutePath(edit.Path)
		switch status {
		case "applied":
			result.AppliedCount++
			appliedPaths = append(appliedPaths, path)
		case "pending":
			result.PendingCount++
			pendingPaths = append(pendingPaths, path)
		default:
			result.UnknownCount++
			unknownPaths = append(unknownPaths, path)
		}
	}
	result.AppliedPaths = mergePlanExecutePaths(appliedPaths)
	result.PendingPaths = mergePlanExecutePaths(pendingPaths)
	result.UnknownPaths = mergePlanExecutePaths(unknownPaths)
	return result
}

func planExecutePendingAutoExactEdits(root string, edits []workspace.PlanEdit) []workspace.PlanEdit {
	return planexecute.PlanExecutePendingAutoExactEdits(root, edits)
}

func planExecuteAutoExactEditStatus(root string, edit workspace.PlanEdit) string {
	return planexecute.PlanExecuteAutoExactEditStatus(root, edit)
}

func planExecuteAutoSourceSatisfiedAnswer(requiredEditPaths []string) string {
	return planexecute.PlanExecuteAutoSourceSatisfiedAnswer(requiredEditPaths)
}

func planExecuteSourceSatisfiedEvidencePaths(answer string, requiredEditPaths []string, allowedEditPaths []string) []string {
	return planexecute.PlanExecuteSourceSatisfiedEvidencePaths(answer, requiredEditPaths, allowedEditPaths)
}

func planExecuteMissingSourceSatisfiedCoveragePaths(answer string, requiredEditPaths []string) []string {
	return planexecute.PlanExecuteMissingSourceSatisfiedCoveragePaths(answer, requiredEditPaths)
}

func planExecuteExactPlanEdits(prompt string, requiredEditPaths []string) ([]workspace.PlanEdit, string) {
	return planexecute.PlanExecuteExactPlanEdits(prompt, requiredEditPaths)
}

func planExecuteExactPlanEditsForWorkspace(prompt string, requiredEditPaths []string, workspaceRoot string) ([]workspace.PlanEdit, string) {
	return planexecute.PlanExecuteExactPlanEditsForWorkspace(prompt, requiredEditPaths, workspaceRoot)
}

func planExecuteUniqueRequiredPaths(requiredEditPaths []string) []string {
	return planexecute.PlanExecuteUniqueRequiredPaths(requiredEditPaths)
}

// newPlanExecuteSessionID returns a fresh, unguessable session
// identifier for this plan-execute run. Used by the executor phase
// to filter `.idiom/plans/<stamp>.plan.json` artifacts to only those
// produced by THIS session — see findLatestPlanArtifact for the
// load-side guard. 16 hex bytes from crypto/rand: 64 bits of entropy
// is overkill for collision avoidance but trivial to generate. Falls
// back to a timestamp-derived hex on the (essentially impossible)
// rand.Read failure path so the rest of the lane still runs.
func newPlanExecuteSessionID() string {
	return planexecute.NewPlanExecuteSessionID()
}

func NewSessionID() string {
	return newPlanExecuteSessionID()
}

// findLatestPlanArtifact picks the most recent .idiom/plans/<stamp>.plan.json
// produced since notBefore. When sessionID is non-empty, the search is
// narrowed to plans whose persisted SessionID matches — otherwise two
// concurrent plan-execute runs in the same workspace race on the shared
// plans directory and the executor phase silently loads the wrong plan.
// Empty sessionID falls back to the legacy time-only filter for
// backward compatibility with callers that don't yet stamp a session.
func findLatestPlanArtifact(root string, notBefore time.Time, sessionID string) (string, error) {
	return planexecute.FindLatestPlanArtifact(root, notBefore, sessionID)
}

type executorPhaseReport = planexecute.ExecutorPhaseReport

const (
	executorHandoffStateInherited  = planexecute.ExecutorHandoffStateInherited
	executorHandoffStateMissing    = planexecute.ExecutorHandoffStateMissing
	executorHandoffStateStale      = planexecute.ExecutorHandoffStateStale
	executorHandoffStateOverridden = planexecute.ExecutorHandoffStateOverridden
)

type executorHandoffPacket = planexecute.ExecutorHandoffPacket

func classifyExecutorHandoffPacket(root string, plan workspace.PlanArtifact) executorHandoffPacket {
	return planexecute.ClassifyExecutorHandoffPacket(root, plan)
}

type reviewRepairPhaseReport = planexecute.ReviewRepairPhaseReport

type patchHygieneAutoRepairReport struct {
	Repaired bool
	Paths    []string
	Lines    int
}

type reviewRepairEvidence struct {
	Diff          string
	DiffCheck     string
	DiffCheckExit int
	PhaseEvidence phaseEvidenceBundle
}

type reviewRepairBaseline = planexecute.ReviewRepairBaseline

type reviewRepairFileSnapshot = planexecute.ReviewRepairFileSnapshot

func runReviewRepairPhase(
	cfg config.Config,
	originalPrompt string,
	plan workspace.PlanArtifact,
	requiredEditPaths []string,
	baseline *reviewRepairBaseline,
	executorReport executorPhaseReport,
	opts planExecuteOptions,
	stdout io.Writer,
	promptRun cmdruntime.PromptRunner,
	rec *telemetry.Recorder,
) (reviewRepairPhaseReport, error) {
	report, err := planexecute.RunReviewRepairPhase(
		cfg,
		originalPrompt,
		plan,
		requiredEditPaths,
		baseline,
		executorReport,
		opts,
		stdout,
		adaptPromptRunner(promptRun),
		rec,
	)
	return report, err
}

func planExecuteReviewRepairPrompt(originalPrompt string, plan workspace.PlanArtifact, changedPaths []string, allowedRepairPaths []string, evidence reviewRepairEvidence) string {
	return planexecute.PlanExecuteReviewRepairPrompt(originalPrompt, plan, changedPaths, allowedRepairPaths, toSharedReviewRepairEvidence(evidence))
}

func toSharedReviewRepairEvidence(evidence reviewRepairEvidence) planexecute.ReviewRepairEvidence {
	shared := planexecute.ReviewRepairEvidence{
		Diff:          evidence.Diff,
		DiffCheck:     evidence.DiffCheck,
		DiffCheckExit: evidence.DiffCheckExit,
	}
	data, err := json.Marshal(evidence.PhaseEvidence)
	if err == nil {
		_ = json.Unmarshal(data, &shared.PhaseEvidence)
	}
	return shared
}

func fromSharedReviewRepairEvidence(evidence planexecute.ReviewRepairEvidence) reviewRepairEvidence {
	out := reviewRepairEvidence{
		Diff:          evidence.Diff,
		DiffCheck:     evidence.DiffCheck,
		DiffCheckExit: evidence.DiffCheckExit,
	}
	data, err := json.Marshal(evidence.PhaseEvidence)
	if err == nil {
		_ = json.Unmarshal(data, &out.PhaseEvidence)
	}
	return out
}

func planExecuteReviewRepairRejectedPassRetryPrompt(basePrompt string, reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "phase evidence contradicted the pass decision"
	}
	return strings.Join([]string{
		basePrompt,
		"",
		"## Rejected Previous Review Outcome",
		"",
		"The previous review-repair answer used `Decision: pass`, but the phase evidence rejected that pass decision: " + truncatePlanExecuteText(reason, 2000) + ".",
		"",
		"For this retry, `Decision: pass` is not an allowed terminal outcome. Choose exactly one visible outcome:",
		"- Corrective plan: call `submit_plan` with only the executable corrective edits needed to resolve the rejected checklist item. The rationale must start with `Verification Checklist:`.",
		"- Blocker: return `<<QUESTION>>:` with the exact failed checklist item if it cannot be repaired inside Allowed Repair Paths.",
		"",
		"Do not repeat `Decision: pass`. Do not add prose before or after the corrective plan or blocker.",
	}, "\n")
}

func collectReviewRepairEvidence(root string, paths []string, baseline *reviewRepairBaseline) reviewRepairEvidence {
	return fromSharedReviewRepairEvidence(planexecute.CollectReviewRepairEvidence(root, paths, baseline))
}

func autoRepairReviewPatchHygiene(root string, changedPaths []string, evidence reviewRepairEvidence) (patchHygieneAutoRepairReport, error) {
	report, err := planexecute.AutoRepairReviewPatchHygiene(root, changedPaths, toSharedReviewRepairEvidence(evidence))
	return patchHygieneAutoRepairReport{
		Repaired: report.Repaired,
		Paths:    append([]string(nil), report.Paths...),
		Lines:    report.Lines,
	}, err
}

func captureReviewRepairBaseline(root string, paths []string) reviewRepairBaseline {
	return planexecute.CaptureReviewRepairBaseline(root, paths)
}

func planChangedPaths(plan workspace.PlanArtifact) []string {
	return planexecute.PlanChangedPaths(plan)
}

func mergePlanExecutePaths(groups ...[]string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, group := range groups {
		for _, path := range group {
			normalized := normalizePlanExecutePath(path)
			if normalized == "" {
				continue
			}
			if _, ok := seen[normalized]; ok {
				continue
			}
			seen[normalized] = struct{}{}
			out = append(out, normalized)
		}
	}
	sort.Strings(out)
	return out
}

func reviewAnswerDeclaresBlocker(answer string) bool {
	lower := strings.ToLower(answer)
	return strings.Contains(answer, "<<QUESTION>>") || strings.Contains(lower, "decision: block") || strings.Contains(lower, "blocker")
}

func planExecuteAnswerDeclaresBlocker(answer string) bool {
	answer = strings.TrimSpace(answer)
	lower := strings.ToLower(answer)
	return strings.Contains(answer, "<<QUESTION>>") ||
		strings.Contains(lower, "decision: block") ||
		strings.HasPrefix(lower, "blocker:") ||
		strings.Contains(lower, "\nblocker:")
}

func planExecuteBlockerSummary(answer string) string {
	for _, raw := range strings.Split(answer, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		const max = 240
		if len(line) > max {
			return line[:max] + "..."
		}
		return line
	}
	return "planner stopped with a blocker"
}

func reviewAnswerPassContradictionReason(answer string) string {
	return planexecute.ReviewAnswerPassContradictionReason(answer)
}

func truncatePlanExecuteText(text string, maxBytes int) string {
	return planexecute.TruncatePlanExecuteText(text, maxBytes)
}

// runExecutorPhase iterates the plan and dispatches each edit
// against a fresh workspace.Manager rooted at the configured repo.
// It does NOT use a separate model — the plan IS the source of
// truth. Failure of one edit aborts the phase and surfaces the
// specific failure so the caller (planner re-run, human, etc.) can
// react.
func runExecutorPhase(cfg config.Config, plan workspace.PlanArtifact, requiredEditPaths []string, jsonEvents bool, stdout io.Writer, progress ...planexecute.EditProgressFunc) (executorPhaseReport, error) {
	return runExecutorPhaseScoped(cfg, plan, requiredEditPaths, nil, jsonEvents, stdout, progress...)
}

func runExecutorPhaseScoped(cfg config.Config, plan workspace.PlanArtifact, requiredEditPaths []string, allowedEditPaths []string, jsonEvents bool, stdout io.Writer, progress ...planexecute.EditProgressFunc) (executorPhaseReport, error) {
	return runExecutorPhaseScopedWithVerification(cfg, plan, requiredEditPaths, allowedEditPaths, nil, jsonEvents, stdout, progress...)
}

func runExecutorPhaseScopedWithVerification(cfg config.Config, plan workspace.PlanArtifact, requiredEditPaths []string, allowedEditPaths []string, verificationCommands []string, jsonEvents bool, stdout io.Writer, progress ...planexecute.EditProgressFunc) (executorPhaseReport, error) {
	shared, err := planexecute.RunExecutorPhaseScopedWithVerification(cfg, plan, requiredEditPaths, allowedEditPaths, verificationCommands, jsonEvents, stdout, progress...)
	return shared, err
}

func runExecutorPhaseScopedWithVerificationPlan(cfg config.Config, plan workspace.PlanArtifact, requiredEditPaths []string, allowedEditPaths []string, verificationPlan planexecute.VerificationPlan, jsonEvents bool, stdout io.Writer, progress ...planexecute.EditProgressFunc) (executorPhaseReport, error) {
	shared, err := planexecute.RunExecutorPhaseScopedWithVerificationPlan(cfg, plan, requiredEditPaths, allowedEditPaths, verificationPlan, jsonEvents, stdout, progress...)
	return shared, err
}

func runPlanExecuteVerificationPlan(root string, plan planexecute.VerificationPlan, jsonEvents bool, stdout io.Writer) (int, []phaseEvidenceCheck, error) {
	checked, evidence, err := planexecute.RunVerificationPlan(root, plan, jsonEvents, stdout)
	return checked, evidence, err
}

// planEditPreflightMismatch describes the first edit whose old_text
// does not appear in its target file. Used by preflightPlanEdits to
// abort the executor phase before any edit lands when a plan would
// otherwise partially-apply.
type planEditPreflightMismatch struct {
	Index  int
	Path   string
	Reason string
}

func planEditPreflightMismatchFromPackage(mismatch *planexecute.PlanEditPreflightMismatch) *planEditPreflightMismatch {
	if mismatch == nil {
		return nil
	}
	return &planEditPreflightMismatch{
		Index:  mismatch.Index,
		Path:   mismatch.Path,
		Reason: mismatch.Reason,
	}
}

func preflightPlanUnsupportedProofClaims(plan workspace.PlanArtifact) *planEditPreflightMismatch {
	return planEditPreflightMismatchFromPackage(planexecute.PreflightPlanUnsupportedProofClaims(plan))
}

func normalizePlanExecutePath(path string) string {
	return planexecute.NormalizePlanExecutePath(path)
}

// preflightPlanEdits validates every plan edit's old_text against the
// current file content before any edit dispatches. Returns nil when
// every substring-shaped edit matches; otherwise returns a pointer to
// the first mismatch. Range-shaped edits (start_line+end_line+content,
// no old_text) are skipped — those don't depend on substring matching.
//
// The walk is deliberately read-only: it loads each unique path once
// and reuses the buffer for every edit on that path. Edits ordered K
// and K+1 on the same path are validated against the SAME file
// content (the version on disk before any executor dispatch); this
// matches the executor's per-edit substring search semantics, which
// look at the file as edited up to that point. For a plan with two
// non-overlapping edits on the same file, both anchors must appear
// in the original content; once edit K lands, edit K+1's anchor must
// still appear in the modified content. The preflight catches the
// common case where the plan's anchor is wrong from the start.
func preflightPlanEdits(workspaceRoot string, plan workspace.PlanArtifact) *planEditPreflightMismatch {
	return planEditPreflightMismatchFromPackage(planexecute.PreflightPlanEdits(workspaceRoot, plan))
}

// emitPlanExecuteJSONEvent writes one JSONL line to stdout when the
// orchestrator is running with --json. Each event carries an
// idiom.plan_execute.* type so downstream log parsers can recognize
// orchestrator-emitted events distinct from the runner's JSON stream
// (which closes when the planner phase exits).
//
// All events include an ISO-8601 timestamp so reviewers can stitch
// them into the planner-phase events using ts ordering.
func emitPlanExecuteJSONEvent(stdout io.Writer, jsonEvents bool, eventType string, fields map[string]any) {
	if !jsonEvents {
		return
	}
	payload := map[string]any{
		"type": "idiom.plan_execute." + eventType,
		"ts":   time.Now().UTC().Format(time.RFC3339Nano),
	}
	maps.Copy(payload, fields)
	body, err := json.Marshal(payload)
	if err != nil {
		// json.Marshal on a map[string]any with primitive values
		// shouldn't fail; if it does, swallow rather than crash the
		// orchestrator — the executor phase has already done the
		// real work and a missing telemetry event isn't a blocker.
		return
	}
	fmt.Fprintln(stdout, string(body))
}

func firstNonEmptyString(values ...string) string {
	return planexecute.FirstNonEmptyString(values...)
}

func FirstNonEmptyString(values ...string) string {
	return firstNonEmptyString(values...)
}
