package planexecute

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/contracts/autonomy"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/textutil"
	"idiom/internal/workflows/commandcheck"
)

func runPlanExecuteVerificationPlanWithParentContract(ctx context.Context, root string, plan VerificationPlan, parentContractID string, jsonEvents bool, stdout io.Writer) (int, []plancontract.PhaseEvidenceCheck, error) {
	ctx = planExecuteContext(ctx)
	evidence := make([]plancontract.PhaseEvidenceCheck, 0, len(plan.Commands)+len(plan.Rejects))
	if len(plan.Rejects) > 0 {
		reject := plan.Rejects[0]
		check := plancontract.PhaseEvidenceCheck{
			Kind:    plancontract.PhaseEvidenceKindVerification,
			Command: strings.TrimSpace(reject.Command),
			Status:  plancontract.PhaseEvidenceStatusFailed,
			Error:   strings.TrimSpace(reject.Reason),
			Summary: "requested verification command was rejected before execution",
		}

		evidence = append(evidence, check)

		event := map[string]any{
			plancontract.PlanExecuteEventFieldCommand:   check.Command,
			plancontract.PlanExecuteTerminalFieldStatus: string(check.Status),
			plancontract.PlanExecuteEventFieldSummary:   check.Summary,
			plancontract.PlanExecuteEventFieldError:     check.Error,
		}

		if strings.TrimSpace(reject.Source) != "" {
			event[plancontract.PlanExecuteEventFieldSource] = strings.TrimSpace(reject.Source)
		}

		if err := emitPlanExecuteJSONEvent(stdout, jsonEvents, plancontract.PlanExecuteEventVerification, event); err != nil {
			return len(evidence), evidence, planExecuteJSONEventWriteError(err)
		}
		if err := emitPlanExecuteVerificationAcceptance(stdout, jsonEvents, planExecuteVerificationAcceptanceInput{
			Root:             root,
			ParentContractID: parentContractID,
			Check:            check,
			Decision:         autonomy.VerificationAcceptanceRejected,
			ReasonCode:       "command_rejected",
			Source:           reject.Source,
		}); err != nil {
			return len(evidence), evidence, err
		}

		if !jsonEvents {
			fmt.Fprintf(stdout, "  verification %s ... failed\n", check.Command)
		}

		return len(evidence), evidence, fmt.Errorf("verification rejected for %q: %s", check.Command, firstNonEmptyString(check.Error, check.Summary))
	}

	for _, command := range plan.Commands {
		check := RunVerificationCommand(ctx, root, command, 0, false).Check

		evidence = append(evidence, check)

		event := map[string]any{
			plancontract.PlanExecuteEventFieldCommand:   command.Command,
			plancontract.PlanExecuteTerminalFieldStatus: string(check.Status),
			plancontract.PlanExecuteEventFieldSummary:   check.Summary,
			plancontract.PlanExecuteEventFieldError:     check.Error,
		}

		if check.WorkingDir != "" {
			event[plancontract.PlanExecuteEventFieldWorkingDir] = check.WorkingDir
		}

		if command.Source != "" {
			event[plancontract.PlanExecuteEventFieldSource] = command.Source
		}

		if err := emitPlanExecuteJSONEvent(stdout, jsonEvents, plancontract.PlanExecuteEventVerification, event); err != nil {
			return len(evidence), evidence, planExecuteJSONEventWriteError(err)
		}
		if err := emitPlanExecuteVerificationAcceptance(stdout, jsonEvents, planExecuteVerificationAcceptanceInput{
			Root:             root,
			ParentContractID: parentContractID,
			Check:            check,
			Command:          command,
			Decision:         planExecuteVerificationAcceptanceDecision(check),
			ReasonCode:       planExecuteVerificationAcceptanceReason(check),
			Source:           command.Source,
		}); err != nil {
			return len(evidence), evidence, err
		}

		if !jsonEvents {
			location := ""
			if check.WorkingDir != "" {
				location = " (dir " + check.WorkingDir + ")"
			}

			fmt.Fprintf(stdout, "  verification %s%s ... %s\n", command.Command, location, check.Status)
		}

		if !plancontract.PhaseEvidenceStatusIsPassed(check.Status) {
			return len(evidence), evidence, fmt.Errorf("verification failed for %q: %s", command.Command, firstNonEmptyString(check.Error, check.Summary))
		}
	}

	return len(evidence), evidence, nil
}

type planExecuteVerificationAcceptanceInput struct {
	Root             string
	ParentContractID string
	Check            plancontract.PhaseEvidenceCheck
	Command          plancontract.VerificationCommand
	Decision         autonomy.VerificationAcceptanceDecision
	ReasonCode       string
	Source           string
}

func emitPlanExecuteVerificationAcceptance(stdout io.Writer, jsonEvents bool, input planExecuteVerificationAcceptanceInput) error {
	if !jsonEvents {
		return nil
	}

	record := planExecuteVerificationAcceptanceRecord(input)
	return emitPlanExecuteAutonomyRecord(stdout, jsonEvents, plancontract.PlanExecuteEventVerificationAcceptance, &record)
}

type planExecuteAutonomyRecord interface {
	Normalize()
	Validate() error
}

func emitPlanExecuteAutonomyRecord(stdout io.Writer, jsonEvents bool, eventType plancontract.PlanExecuteEventType, record planExecuteAutonomyRecord) error {
	if !jsonEvents || record == nil {
		return nil
	}

	record.Normalize()
	if err := record.Validate(); err != nil {
		if emitErr := emitPlanExecuteInvalidJSONEvent(stdout, jsonEvents, eventType, map[string]any{
			plancontract.PlanExecuteEventFieldError: err.Error(),
		}); emitErr != nil {
			return planExecuteJSONEventWriteError(emitErr)
		}
		return nil
	}

	fields := map[string]any{}
	body, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, &fields); err != nil {
		return err
	}

	if err := emitPlanExecuteJSONEvent(stdout, jsonEvents, eventType, fields); err != nil {
		return planExecuteJSONEventWriteError(err)
	}
	return nil
}

func planExecuteVerificationAcceptanceRecord(input planExecuteVerificationAcceptanceInput) autonomy.VerificationAcceptanceRecord {
	command := strings.TrimSpace(firstNonEmptyString(input.Check.Command, input.Command.Command, "verification"))
	resultText := strings.Join([]string{
		string(input.Check.Kind),
		input.Check.Command,
		input.Check.WorkingDir,
		string(input.Check.Status),
		input.Check.Summary,
		input.Check.Error,
	}, "\x00")
	commandRef := "command:" + planExecuteShortHash(command)
	resultRef := "verification_result:" + planExecuteShortHash(resultText)
	headSHA := planExecuteGitSHAOrUnknown(input.Root)
	parentContractID := strings.TrimSpace(input.ParentContractID)
	if parentContractID == "" {
		parentContractID = "plan_execute_verification:" + planExecuteShortHash(input.Root)
	}

	return autonomy.VerificationAcceptanceRecord{
		VerificationAcceptanceID: "verification_acceptance_" + planExecuteShortHash(parentContractID, commandRef, resultRef, string(input.Decision)),
		ParentContractID:         parentContractID,
		ObligationRefs:           planExecuteVerificationObligationRefs(input),
		CommandRef:               commandRef,
		CommandFingerprint:       "sha256:" + planExecuteFullHash(command),
		WorkspaceRef:             "workspace:" + planExecuteShortHash(input.Root),
		BaseSHA:                  headSHA,
		HeadSHA:                  headSHA,
		ExitStatus:               planExecuteVerificationExitStatus(input.Check),
		ResultRef:                resultRef,
		ScopeRefs:                planExecuteVerificationScopeRefs(input),
		Decision:                 input.Decision,
		ReasonCode:               strings.TrimSpace(firstNonEmptyString(input.ReasonCode, "verification_recorded")),
	}
}

func planExecuteVerificationAcceptanceDecision(check plancontract.PhaseEvidenceCheck) autonomy.VerificationAcceptanceDecision {
	if plancontract.PhaseEvidenceStatusIsPassed(check.Status) {
		return autonomy.VerificationAcceptanceAccepted
	}
	return autonomy.VerificationAcceptanceRepairRequired
}

func planExecuteVerificationAcceptanceReason(check plancontract.PhaseEvidenceCheck) string {
	if plancontract.PhaseEvidenceStatusIsPassed(check.Status) {
		return "verification_passed"
	}
	return "verification_failed"
}

func planExecuteVerificationExitStatus(check plancontract.PhaseEvidenceCheck) int {
	if plancontract.PhaseEvidenceStatusIsPassed(check.Status) {
		return 0
	}
	return 1
}

func planExecuteVerificationObligationRefs(input planExecuteVerificationAcceptanceInput) []string {
	source := strings.TrimSpace(firstNonEmptyString(input.Source, input.Command.Source, "planner_owned_verification"))
	return []string{"obligation:" + normalizePlanExecuteRefToken(source)}
}

func planExecuteVerificationScopeRefs(input planExecuteVerificationAcceptanceInput) []string {
	refs := make([]string, 0, 3)
	for _, value := range []string{input.Command.Path, input.Check.Path, input.Check.WorkingDir} {
		value = plancontract.NormalizeScopePath(value)
		if value != "" && value != "." {
			refs = append(refs, "scope:"+value)
		}
	}
	return textutil.Sorted(refs)
}

func planExecuteGitSHAOrUnknown(root string) string {
	out := strings.TrimSpace(runGitEvidence(root, "rev-parse", "HEAD"))
	if len(out) >= 7 && !strings.ContainsAny(out, " \t\r\n:") {
		return out
	}
	return "unknown"
}

func normalizePlanExecuteRefToken(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "unknown"
	}

	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}

	return strings.Trim(strings.TrimSpace(b.String()), "_")
}

func planExecuteShortHash(values ...string) string {
	return planExecuteFullHash(values...)[:16]
}

func planExecuteFullHash(values ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return hex.EncodeToString(sum[:])
}

type VerificationCommandRun struct {
	Check       plancontract.PhaseEvidenceCheck
	StartedAt   time.Time
	CompletedAt time.Time
	ExitCode    int
	Failure     commandcheck.FailureKind
}

func RunVerificationCommand(parent context.Context, root string, command plancontract.VerificationCommand, timeout time.Duration, requireOperandAfterPrefix bool) VerificationCommandRun {
	if timeout <= 0 {
		timeout = defaultPlanExecuteVerificationTimeout
	}
	command.Command = strings.TrimSpace(command.Command)
	args := plancontract.NormalizeVerificationArgv(command.Argv)

	if len(args) == 0 {
		if parsed, ok := plancontract.VerificationCommandArgs(command.Command); ok {
			args = parsed
		}
	}

	startedAt := time.Now().UTC()
	run := VerificationCommandRun{StartedAt: startedAt, ExitCode: -1}
	check := plancontract.PhaseEvidenceCheck{
		Kind:       plancontract.PhaseEvidenceKindVerification,
		Command:    command.Command,
		WorkingDir: command.WorkingDir,
		Status:     plancontract.PhaseEvidenceStatusFailed,
	}
	finish := func() VerificationCommandRun {
		run.Check = check
		if run.CompletedAt.IsZero() {
			run.CompletedAt = time.Now().UTC()
		}
		return run
	}
	workdir := strings.TrimSpace(root)

	if workdir == "" {
		workdir = "."
	}

	if command.WorkingDir != "" {
		workingDir, ok := cleanVerificationWorkingDir(command.WorkingDir)
		if !ok {
			check.Error = "unsafe verification working_dir"
			return finish()
		}

		workdir = filepath.Join(workdir, filepath.FromSlash(workingDir))
	}

	result := commandcheck.Run(planExecuteContext(parent), commandcheck.Request{
		Argv:                      args,
		Dir:                       workdir,
		Timeout:                   timeout,
		AllowedArgv:               plancontract.VerificationArgvAllowed,
		PrefixArgv:                plancontract.VerificationCommandPrefix,
		RequireOperandAfterPrefix: requireOperandAfterPrefix,
		CaptureBytes:              commandcheck.DiagnosticCaptureBytes,
		DisplayBytes:              12000,
		Truncation:                commandcheck.TruncatePlanExecute,
		MergeOutput:               true,
	})
	run.StartedAt = result.StartedAt
	run.CompletedAt = result.CompletedAt
	run.ExitCode = result.ExitCode
	run.Failure = result.Failure

	switch result.Failure {
	case commandcheck.FailureNone:
		check.Status = plancontract.PhaseEvidenceStatusOK
		check.Summary = result.DisplayOutput
		run.ExitCode = 0

	case commandcheck.FailureUnsupported:
		check.Error = "unsupported verification command"

	case commandcheck.FailureMissingOperand:
		check.Error = "verification command must name a package pattern"

	case commandcheck.FailureTimeout:
		check.Error = fmt.Sprintf("verification command timed out after %s", timeout)
		check.Summary = check.Error

	default:
		if result.Err != nil {
			check.Error = firstNonEmptyString(result.DisplayOutput, result.Err.Error())
			check.Summary = result.Err.Error()
		}
	}

	return finish()
}

func VerificationStatus(evidence []plancontract.PhaseEvidenceCheck) string {
	if len(evidence) == 0 {
		return string(plancontract.PhaseEvidenceSummaryStatusNotApplicable)
	}

	for _, check := range evidence {
		if !plancontract.PhaseEvidenceStatusIsPassed(check.Status) {
			return string(plancontract.PhaseEvidenceSummaryStatusFailed)
		}
	}

	return string(plancontract.PhaseEvidenceSummaryStatusPassed)
}
