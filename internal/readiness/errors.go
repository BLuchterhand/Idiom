package readiness

import "idiom/internal/failures"

const reasonReadinessProfileInvalid failures.Reason = 9001

func (e ProfileSelectionError) Code() failures.Code {
	return failures.InvalidArgument
}

func (e ProfileSelectionError) Reason() failures.Reason {
	return reasonReadinessProfileInvalid
}
