package app

import (
	"testing"
)

func TestClassifyTUIRequestDoesNotUseKeywordMutationPreflight(t *testing.T) {
	got := classifyTUIRequest("Implement persistent input history.")
	if got.Class != tuiRequestClassConversation {
		t.Fatalf("class = %q, want conversation", got.Class)
	}
}

func TestClassifyTUIRequestHonorsReadOnlyConstraint(t *testing.T) {
	got := classifyTUIRequest("Just explain how we would implement persistent input history; do not edit files.")
	if got.Class != tuiRequestClassConversation {
		t.Fatalf("class = %q, want conversation", got.Class)
	}
}

func TestClassifyTUIRequestDoesNotUsePlanExecuteKeywordPreflight(t *testing.T) {
	got := classifyTUIRequest("Send this through plan-execute and implement the atom.")
	if got.Class != tuiRequestClassConversation {
		t.Fatalf("class = %q, want conversation", got.Class)
	}
}

func TestUpdateTUIRequestStatusDoesNotConfirmKeywordMutation(t *testing.T) {
	m := &Model{}
	m.updateTUIRequestStatus("Implement persistent input history.")
	if len(m.messages) != 0 {
		t.Fatalf("messages = %d, want no local confirmation messages", len(m.messages))
	}
	if m.status != "Request classified: conversation." {
		t.Fatalf("status = %q", m.status)
	}
}
