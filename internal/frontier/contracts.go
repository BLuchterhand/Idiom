package frontier

import "strings"

type Lane string

const (
	LaneMain         Lane = "main"
	LaneLocate       Lane = "locate"
	LaneExplore      Lane = "explore"
	LaneReview       Lane = "review"
	LaneArchitecture Lane = "architecture"
	LaneImplement    Lane = "implement"
)

type RouteReason string

const (
	RouteReasonDirectConversation   RouteReason = "direct_conversation"
	RouteReasonDirectImplementation RouteReason = "direct_implementation"
	RouteReasonLocateRequest        RouteReason = "locate_request"
	RouteReasonExploreRequest       RouteReason = "explore_request"
	RouteReasonReviewRequest        RouteReason = "review_request"
	RouteReasonArchitectureRequest  RouteReason = "architecture_request"
)

type AnswerContract string

const (
	AnswerContractDirectConversation  AnswerContract = "direct_answer"
	AnswerContractLocateTargets       AnswerContract = "locate_targets"
	AnswerContractGroundedExplanation AnswerContract = "grounded_explanation"
	AnswerContractReviewFindings      AnswerContract = "review_findings"
	AnswerContractArchitecture        AnswerContract = "architecture_assessment"
	AnswerContractImplementation      AnswerContract = "implementation_result"
)

type LaneContractPolicy struct {
	Contract  AnswerContract
	Expected  []string
	Forbidden []string
	Fallback  string
}

func RouteReasonForLane(lane Lane) RouteReason {
	switch lane {
	case LaneLocate:
		return RouteReasonLocateRequest
	case LaneExplore:
		return RouteReasonExploreRequest
	case LaneReview:
		return RouteReasonReviewRequest
	case LaneArchitecture:
		return RouteReasonArchitectureRequest
	case LaneImplement:
		return RouteReasonDirectImplementation
	default:
		return RouteReasonDirectConversation
	}
}

func AnswerContractForLane(lane Lane) AnswerContract {
	switch lane {
	case LaneLocate:
		return AnswerContractLocateTargets
	case LaneExplore:
		return AnswerContractGroundedExplanation
	case LaneReview:
		return AnswerContractReviewFindings
	case LaneArchitecture:
		return AnswerContractArchitecture
	case LaneImplement:
		return AnswerContractImplementation
	case LaneMain:
		return AnswerContractDirectConversation
	default:
		return ""
	}
}

func LaneContractPolicyForLane(lane Lane) LaneContractPolicy {
	policy := LaneContractPolicy{Contract: AnswerContractForLane(lane)}
	switch lane {
	case LaneLocate:
		policy.Expected = []string{"exact file path", "line number or focused snippet", "why this is the best target"}
		policy.Forbidden = []string{"repo-wide speculation", "edits"}
		policy.Fallback = "If no exact target is found, say what was searched and name the next focused search."
	case LaneExplore:
		policy.Expected = []string{"major files or packages involved", "what was verified", "what is inferred", "confidence", "next validation step"}
		policy.Forbidden = []string{"edits", "ungrounded architecture advice", "claims without cited paths"}
		policy.Fallback = "If evidence is weak, return the concrete paths inspected and the narrowest next read/search."
	case LaneReview:
		policy.Expected = []string{"one strongest finding", "exact file path", "why it is a real issue or cleanup", "evidence", "confidence"}
		policy.Forbidden = []string{"structure-only advice", "multiple weak findings", "edits"}
		policy.Fallback = "If no bounded finding is supported, say the evidence is inconclusive and stop before editing."
	case LaneArchitecture:
		policy.Expected = []string{"current structural shape", "top structural opportunities", "exact hotspot paths", "boundary or ownership direction", "confidence"}
		policy.Forbidden = []string{"bug-triage framing", "line-specific defect requirement", "edits inside the analysis lane"}
		policy.Fallback = "If model synthesis is weak, use deterministic hotspot evidence for a grounded structural summary."
	case LaneImplement:
		policy.Expected = []string{"changed files", "outcome", "verification status"}
		policy.Forbidden = []string{"broad discovery after target packet", "unbounded refactor", "rewriting unrelated files"}
		policy.Fallback = "If the target is unclear, inspect the bounded target packet before widening."
	case LaneMain:
		policy.Expected = []string{"direct answer"}
		policy.Forbidden = []string{"workspace writes unless routed to implement"}
	}
	return policy
}

func RenderLaneContractPolicy(lane Lane) string {
	policy := LaneContractPolicyForLane(lane)
	if policy.Contract == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("Answer contract: ")
	b.WriteString(string(policy.Contract))
	if len(policy.Expected) > 0 {
		b.WriteString("\nExpected output:")
		for _, item := range policy.Expected {
			b.WriteString("\n- ")
			b.WriteString(item)
		}
	}
	if len(policy.Forbidden) > 0 {
		b.WriteString("\nForbidden:")
		for _, item := range policy.Forbidden {
			b.WriteString("\n- ")
			b.WriteString(item)
		}
	}
	if strings.TrimSpace(policy.Fallback) != "" {
		b.WriteString("\nFallback policy:\n- ")
		b.WriteString(strings.TrimSpace(policy.Fallback))
	}
	return b.String()
}
