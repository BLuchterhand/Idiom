package planintent

import "idiom/internal/failures"

const (
	reasonPlanIntentInvalidSchema failures.Reason = 7001 + iota
	reasonPlanIntentInvalidSource
	reasonPlanIntentInvalidScope
	reasonPlanIntentScopeRejected
	reasonPlanIntentInvalidOperation
	reasonPlanIntentInvalidShape
	reasonPlanIntentInvalidEvidence
	reasonPlanIntentInvalidBlocker
	reasonPlanIntentMissingSourceEvidence
	reasonPlanIntentAmbiguousAnchor
	reasonPlanIntentNonUniqueAnchor
	reasonPlanIntentUnsupportedOperation
	reasonPlanIntentBlocker
	reasonPlanIntentCompilerBug
)

func (err *ValidationError) FailureCode() failures.Code {
	if err == nil {
		return failures.Unknown
	}
	switch err.Code {
	case CodeRequiredScopeMismatch, CodeCandidateScopeRejected, CodeIntentScopeRejected:
		return failures.FailedPrecondition
	default:
		return failures.InvalidArgument
	}
}

func (err *ValidationError) Reason() failures.Reason {
	if err == nil {
		return failures.NoReason
	}
	switch err.Code {
	case CodeInvalidSchemaVersion:
		return reasonPlanIntentInvalidSchema
	case CodeInvalidSource:
		return reasonPlanIntentInvalidSource
	case CodeInvalidScope:
		return reasonPlanIntentInvalidScope
	case CodeRequiredScopeMismatch, CodeCandidateScopeRejected, CodeIntentScopeRejected:
		return reasonPlanIntentScopeRejected
	case CodeInvalidOperation:
		return reasonPlanIntentInvalidOperation
	case CodeInvalidIntentShape:
		return reasonPlanIntentInvalidShape
	case CodeInvalidEvidenceRef:
		return reasonPlanIntentInvalidEvidence
	case CodeInvalidBlocker:
		return reasonPlanIntentInvalidBlocker
	default:
		return reasonPlanIntentInvalidShape
	}
}

func (err *CompileError) FailureCode() failures.Code {
	if err == nil {
		return failures.Unknown
	}
	switch err.Code {
	case CodeCompilerBug:
		return failures.Internal
	case CodeUnsupportedOperation:
		return failures.Unimplemented
	case CodeScopeRejected:
		return failures.InvalidArgument
	default:
		return failures.FailedPrecondition
	}
}

func (err *CompileError) Reason() failures.Reason {
	if err == nil {
		return failures.NoReason
	}
	switch err.Code {
	case CodeMissingSourceEvidence:
		return reasonPlanIntentMissingSourceEvidence
	case CodeAmbiguousAnchor:
		return reasonPlanIntentAmbiguousAnchor
	case CodeNonUniqueAnchor:
		return reasonPlanIntentNonUniqueAnchor
	case CodeScopeRejected:
		return reasonPlanIntentScopeRejected
	case CodeUnsupportedOperation:
		return reasonPlanIntentUnsupportedOperation
	case CodeIntentBlocker:
		return reasonPlanIntentBlocker
	case CodeCompilerBug:
		return reasonPlanIntentCompilerBug
	default:
		return reasonPlanIntentCompilerBug
	}
}
