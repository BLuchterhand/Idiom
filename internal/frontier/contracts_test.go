package frontier

import (
	"strings"
	"testing"
)

func TestLaneContractSourceOfTruth(t *testing.T) {
	tests := []struct {
		lane     Lane
		reason   RouteReason
		contract AnswerContract
		rendered string
	}{
		{LaneMain, RouteReasonDirectConversation, AnswerContractDirectConversation, "direct answer"},
		{LaneLocate, RouteReasonLocateRequest, AnswerContractLocateTargets, "exact file path"},
		{LaneExplore, RouteReasonExploreRequest, AnswerContractGroundedExplanation, "what was verified"},
		{LaneReview, RouteReasonReviewRequest, AnswerContractReviewFindings, "one strongest finding"},
		{LaneArchitecture, RouteReasonArchitectureRequest, AnswerContractArchitecture, "top structural opportunities"},
		{LaneImplement, RouteReasonDirectImplementation, AnswerContractImplementation, "changed files"},
	}

	for _, tt := range tests {
		if got := RouteReasonForLane(tt.lane); got != tt.reason {
			t.Fatalf("%s: expected route reason %q, got %q", tt.lane, tt.reason, got)
		}
		if got := AnswerContractForLane(tt.lane); got != tt.contract {
			t.Fatalf("%s: expected answer contract %q, got %q", tt.lane, tt.contract, got)
		}
		if rendered := RenderLaneContractPolicy(tt.lane); !strings.Contains(rendered, tt.rendered) {
			t.Fatalf("%s: expected rendered policy to contain %q, got:\n%s", tt.lane, tt.rendered, rendered)
		}
	}
}
