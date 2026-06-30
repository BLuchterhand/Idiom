package productworkflow

type ImplementationLedgerRecordType string

const (
	LedgerRecordAtomPlanned                        ImplementationLedgerRecordType = "atom_planned"
	LedgerRecordAtomClosed                         ImplementationLedgerRecordType = "atom_closed"
	LedgerRecordImplementationCompleted            ImplementationLedgerRecordType = "implementation_completed"
	LedgerRecordFallbackCompletion                 ImplementationLedgerRecordType = "fallback_completion"
	LedgerRecordPartialImplementationRecorded      ImplementationLedgerRecordType = "partial_implementation_recorded"
	LedgerRecordVerificationCompleted              ImplementationLedgerRecordType = "verification_completed"
	LedgerRecordReviewCompleted                    ImplementationLedgerRecordType = "review_completed"
	LedgerRecordPostEffortInvestigationCompleted   ImplementationLedgerRecordType = "post_effort_investigation_completed"
	LedgerRecordFallbackException                  ImplementationLedgerRecordType = "fallback_exception"
	LedgerRecordBlockerRecorded                    ImplementationLedgerRecordType = "blocker_recorded"
	LedgerRecordImplementationGuidelinesUpdated    ImplementationLedgerRecordType = "implementation_guidelines_updated"
	LedgerRecordImplementorError                   ImplementationLedgerRecordType = "implementor_error"
	LedgerRecordImplementorInterruptedForGuideline ImplementationLedgerRecordType = "implementor_interrupted_for_guideline_update"
	LedgerRecordImplementorPolicyUpdated           ImplementationLedgerRecordType = "implementor_policy_updated"
	LedgerRecordImplementorTimeout                 ImplementationLedgerRecordType = "implementor_timeout"
)

var knownImplementationLedgerRecordTypes = []ImplementationLedgerRecordType{
	LedgerRecordAtomPlanned,
	LedgerRecordAtomClosed,
	LedgerRecordImplementationCompleted,
	LedgerRecordFallbackCompletion,
	LedgerRecordPartialImplementationRecorded,
	LedgerRecordVerificationCompleted,
	LedgerRecordReviewCompleted,
	LedgerRecordPostEffortInvestigationCompleted,
	LedgerRecordFallbackException,
	LedgerRecordBlockerRecorded,
	LedgerRecordImplementationGuidelinesUpdated,
	LedgerRecordImplementorError,
	LedgerRecordImplementorInterruptedForGuideline,
	LedgerRecordImplementorPolicyUpdated,
	LedgerRecordImplementorTimeout,
}

func KnownImplementationLedgerRecordType(recordType ImplementationLedgerRecordType) bool {
	for _, known := range knownImplementationLedgerRecordTypes {
		if recordType == known {
			return true
		}
	}

	return false
}

func IsImplementationCompletionRecordType(recordType ImplementationLedgerRecordType) bool {
	switch recordType {
	case LedgerRecordImplementationCompleted, LedgerRecordFallbackCompletion:
		return true
	default:
		return false
	}
}

func IsImplementationChangeRecordType(recordType ImplementationLedgerRecordType) bool {
	return IsImplementationCompletionRecordType(recordType) || recordType == LedgerRecordPartialImplementationRecorded
}

func RequiresImplementationCommitHash(recordType ImplementationLedgerRecordType) bool {
	return IsImplementationChangeRecordType(recordType)
}

func IsImplementationAtomRecordType(recordType ImplementationLedgerRecordType) bool {
	switch recordType {
	case LedgerRecordAtomPlanned, LedgerRecordAtomClosed:
		return true
	default:
		return false
	}
}

func allowedImplementationLedgerRecordTypeMap() map[string]bool {
	out := map[string]bool{}
	for _, recordType := range knownImplementationLedgerRecordTypes {
		out[string(recordType)] = true
	}
	return out
}
