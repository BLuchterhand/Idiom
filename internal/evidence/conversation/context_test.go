package conversation

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("conversation context evidence", func() {
	It("is conservative for model input", func() {
		evidence := NewModelInputEvidence(1, 0)
		if !evidence.Valid() || !evidence.ValidForUserFacingUncertainty() {
			Fail("partial model-input evidence should authorize only the scoped handoff")
		}
		Expect(evidence.EvidenceID).NotTo(BeEmpty())
	})

	It("closes the handoff when prior assistant evidence is present", func() {
		evidence := NewModelInputEvidence(1, 1)
		if !evidence.Valid() {
			Fail("assistant-bearing evidence should remain structurally valid")
		}
		if evidence.ValidForUserFacingUncertainty() {
			Fail("prior assistant evidence must not authorize uncertainty handoff")
		}
	})

	It("fails closed for unavailable and complete evidence", func() {
		complete := Evidence{
			SchemaVersion:         SchemaVersion,
			Status:                StatusComplete,
			Scope:                 ScopeModelInput,
			Producer:              ProducerRunnerModelInput,
			Reason:                "complete_context",
			MessageCount:          1,
			UserMessageCount:      1,
			AssistantMessageCount: 0,
		}
		complete.EvidenceID = evidenceID(complete)
		Expect(complete.Valid()).To(BeTrue())
		for _, evidence := range []Evidence{
			NewModelInputEvidence(0, 0),
			complete,
		} {
			if evidence.ValidForUserFacingUncertainty() {
				Fail("evidence must fail closed")
			}
		}
	})

	It("fails closed for malformed evidence", func() {
		evidence := NewModelInputEvidence(1, 0)
		evidence.EvidenceID = "model-authored"
		Expect(evidence.Valid()).To(BeFalse())
		Expect(evidence.ValidForUserFacingUncertainty()).To(BeFalse())

		evidence = NewModelInputEvidence(1, 0)
		evidence.Producer = "model"
		Expect(evidence.ValidForUserFacingUncertainty()).To(BeFalse())
	})
})
