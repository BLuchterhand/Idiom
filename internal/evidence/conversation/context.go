package conversation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const SchemaVersion = "idiom.conversation_context_evidence.v1"

type Status string

const (
	StatusComplete    Status = "complete"
	StatusPartial     Status = "partial"
	StatusUnavailable Status = "unavailable"
)

const (
	ScopeModelInput          = "model_input"
	ProducerRunnerModelInput = "runner:model_input"
)

// Evidence describes only the conversation context the runner supplied to the
// model. It cannot establish that a prior event happened or did not happen.
// In particular, partial context evidence is never answerable workspace
// evidence and never resolves workflow obligations.
type Evidence struct {
	SchemaVersion         string `json:"schema_version"`
	EvidenceID            string `json:"evidence_id"`
	Status                Status `json:"status"`
	Scope                 string `json:"scope"`
	Producer              string `json:"producer"`
	Reason                string `json:"reason"`
	MessageCount          int    `json:"message_count"`
	UserMessageCount      int    `json:"user_message_count"`
	AssistantMessageCount int    `json:"assistant_message_count"`
}

// NewModelInputEvidence records a conservative producer fact for a model
// request. Without a durable conversation ledger, the runner treats the
// supplied model input as partial context. A prior assistant turn closes the
// uncertainty handoff because the model may have evidence it failed to use.
func NewModelInputEvidence(userMessageCount, assistantMessageCount int) Evidence {
	status := StatusUnavailable
	reason := "no_user_turn_in_model_input"
	if userMessageCount > 0 {
		status = StatusPartial
		reason = "durable_prior_conversation_not_proven_in_model_input"
	}

	evidence := Evidence{
		SchemaVersion:         SchemaVersion,
		Status:                status,
		Scope:                 ScopeModelInput,
		Producer:              ProducerRunnerModelInput,
		Reason:                reason,
		MessageCount:          maxNonNegative(userMessageCount) + maxNonNegative(assistantMessageCount),
		UserMessageCount:      maxNonNegative(userMessageCount),
		AssistantMessageCount: maxNonNegative(assistantMessageCount),
	}
	evidence.EvidenceID = evidenceID(evidence)
	return evidence
}

func (e Evidence) Valid() bool {
	return e.SchemaVersion == SchemaVersion &&
		e.EvidenceID == evidenceID(e) &&
		(e.Status == StatusComplete || e.Status == StatusPartial || e.Status == StatusUnavailable) &&
		e.Scope == ScopeModelInput &&
		e.Producer != "" &&
		strings.TrimSpace(e.Reason) != "" &&
		e.MessageCount >= 0 &&
		e.UserMessageCount >= 0 &&
		e.AssistantMessageCount >= 0 &&
		e.MessageCount == e.UserMessageCount+e.AssistantMessageCount
}

// ValidForUserFacingUncertainty is intentionally stricter than Valid. Only a
// partial model-input record with a user turn and no prior assistant turn can
// authorize a user-input handoff. Complete, unavailable, malformed, or
// assistant-bearing records fail closed.
func (e Evidence) ValidForUserFacingUncertainty() bool {
	return e.Valid() &&
		e.Status == StatusPartial &&
		e.UserMessageCount > 0 &&
		e.AssistantMessageCount == 0 &&
		e.Producer == ProducerRunnerModelInput
}

func maxNonNegative(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func evidenceID(e Evidence) string {
	material := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%d\x00%d", e.SchemaVersion, e.Status, e.Scope, e.Reason, e.UserMessageCount, e.AssistantMessageCount)
	sum := sha256.Sum256([]byte(material))
	return "conversation_context_" + hex.EncodeToString(sum[:])[:16]
}
