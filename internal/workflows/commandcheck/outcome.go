package commandcheck

import "context"

type OutcomePolicy struct {
	// Context overrides any non-success result when the parent context was canceled.
	Context         context.Context
	ContextExitCode int
	// TimeoutErr and TimeoutExitCode override command timeout failures.
	TimeoutErr      error
	TimeoutExitCode int
	// InfrastructureExitCode applies to failures where the command did not produce
	// its own process exit status. Zero preserves Result.ExitCode.
	InfrastructureExitCode int
}

type Outcome struct {
	Result
	Diagnostic string
}

func ProjectOutcome(result Result, policy OutcomePolicy) Outcome {
	outcome := Outcome{
		Result:     result,
		Diagnostic: commandDiagnostic(result.DisplayOutput, result.Err),
	}

	if outcome.Failure == FailureNone {
		outcome.ExitCode = 0
		return outcome
	}

	if policy.Context != nil && policy.Context.Err() != nil {
		outcome.ExitCode = policyExitCode(policy.ContextExitCode, outcome.ExitCode)
		outcome.Err = policy.Context.Err()
		outcome.Diagnostic = commandDiagnostic(outcome.DisplayOutput, outcome.Err)
		return outcome
	}

	switch outcome.Failure {
	case FailureTimeout:
		timeoutExitCode := policy.TimeoutExitCode
		if timeoutExitCode == 0 {
			timeoutExitCode = policy.InfrastructureExitCode
		}
		outcome.ExitCode = policyExitCode(timeoutExitCode, outcome.ExitCode)
		if policy.TimeoutErr != nil {
			outcome.Err = policy.TimeoutErr
			outcome.Diagnostic = commandDiagnostic(outcome.DisplayOutput, outcome.Err)
		}
	case FailureSpawn, FailureUnsupported, FailureMissingOperand:
		outcome.ExitCode = policyExitCode(policy.InfrastructureExitCode, outcome.ExitCode)
	}

	return outcome
}

func policyExitCode(policyCode, resultCode int) int {
	if policyCode != 0 {
		return policyCode
	}

	return resultCode
}

func commandDiagnostic(displayOutput string, err error) string {
	if displayOutput != "" {
		return displayOutput
	}
	if err != nil {
		return err.Error()
	}

	return ""
}
