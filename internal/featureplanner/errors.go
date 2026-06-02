package featureplanner

import "idiom/internal/failures"

const ReplayInvariantViolation failures.Reason = 4001

type ReplayError struct {
	Message string
}

func (e ReplayError) Error() string {
	return e.Message
}

func (e ReplayError) Code() failures.Code {
	return failures.FailedPrecondition
}

func (e ReplayError) Reason() failures.Reason {
	return ReplayInvariantViolation
}
