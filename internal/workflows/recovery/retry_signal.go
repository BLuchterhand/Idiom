package recovery

import (
	"strings"

	plancontract "idiom/internal/contracts/plan"
)

type PlanExecuteRetrySignal string

const (
	PlanExecuteRetrySignalRepeatedSubmitPlanGate     PlanExecuteRetrySignal = "repeated_submit_plan_gate"
	PlanExecuteRetrySignalSubmitPlanCoverageMismatch PlanExecuteRetrySignal = "submit_plan_coverage_mismatch"
	PlanExecuteRetrySignalRequiredSourcesSatisfied   PlanExecuteRetrySignal = "required_sources_satisfied"
	PlanExecuteRetrySignalRequiredPathsUnchanged     PlanExecuteRetrySignal = "required_paths_unchanged"
	PlanExecuteRetrySignalMissingCompletionEvidence  PlanExecuteRetrySignal = "missing_completion_evidence"
)

type PlanExecuteRetrySignalInput struct {
	FailureKind   plancontract.PlanExecuteFailureKind
	FailureReason string
	PlannerAnswer string
	Stdout        string
	Stderr        string
}

func ClassifyPlanExecuteRetrySignals(input PlanExecuteRetrySignalInput) []PlanExecuteRetrySignal {
	text := strings.ToLower(strings.Join([]string{
		string(input.FailureKind),
		input.FailureReason,
		input.PlannerAnswer,
		input.Stdout,
		input.Stderr,
	}, "\n"))
	if !strings.Contains(text, "submit_plan") {
		return nil
	}

	var signals []PlanExecuteRetrySignal
	if strings.Contains(text, "completion_gate_required_paths_remaining_repeated") {
		signals = append(signals, PlanExecuteRetrySignalRepeatedSubmitPlanGate)
	}
	if strings.Contains(text, "submit_plan rejected") &&
		(strings.Contains(text, "required edit path coverage mismatch") ||
			strings.Contains(text, "missing required edit path") ||
			strings.Contains(text, "invalid edit anchor")) {
		signals = append(signals, PlanExecuteRetrySignalSubmitPlanCoverageMismatch)
	}
	if PlanExecuteRetrySignalsShowRequiredSourcesSatisfied(input) {
		signals = append(signals, PlanExecuteRetrySignalRequiredSourcesSatisfied)
	}
	if strings.Contains(text, "required edit path(s) remain unchanged") ||
		strings.Contains(text, "required edit paths not changed") ||
		strings.Contains(text, "required_edit_paths_remaining") {
		signals = append(signals, PlanExecuteRetrySignalRequiredPathsUnchanged)
	}
	if strings.Contains(text, "missing completion evidence") &&
		(strings.Contains(text, "gate retry 3/3") ||
			strings.Contains(text, "repeated") ||
			strings.Contains(text, "last attempt before tools disable")) {
		signals = append(signals, PlanExecuteRetrySignalMissingCompletionEvidence)
	}

	return signals
}

func PlanExecuteRetrySignalsShowRequiredSourcesSatisfied(input PlanExecuteRetrySignalInput) bool {
	text := strings.ToLower(strings.Join([]string{
		input.FailureReason,
		input.PlannerAnswer,
		input.Stdout,
		input.Stderr,
	}, "\n"))

	return strings.Contains(text, "every required_edit_path has satisfied its source-body obligation")
}
