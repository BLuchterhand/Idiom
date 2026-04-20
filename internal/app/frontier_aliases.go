package app

import "qwen-bubbles/internal/frontier"

type analysisLane = frontier.Lane
type promptRouteReason = frontier.RouteReason
type answerContract = frontier.AnswerContract
type laneContractPolicy = frontier.LaneContractPolicy
type promptIntentDecision = frontier.IntentDecision
type promptIntentVerifierResult = frontier.IntentVerifierResult
type promptIntentReconciliationSnapshot = frontier.IntentReconciliationSnapshot
type routeAmbiguitySnapshot = frontier.RouteAmbiguitySnapshot
type deterministicControlRailsSnapshot = frontier.ControlRailsSnapshot
type controlRailsEnforcementSnapshot = frontier.ControlRailsEnforcementSnapshot
type dynamicToolPolicySnapshot = frontier.DynamicToolPolicySnapshot
type implementationTarget = frontier.Target
type implementationTargetPacket = frontier.TargetPacket
type effortPolicySnapshot = frontier.EffortPolicySnapshot
type verificationPlan = frontier.VerificationPlan
type locateCandidate = frontier.LocateCandidate

const (
	analysisLaneMain         analysisLane = frontier.LaneMain
	analysisLaneLocate       analysisLane = frontier.LaneLocate
	analysisLaneExplore      analysisLane = frontier.LaneExplore
	analysisLaneReview       analysisLane = frontier.LaneReview
	analysisLaneArchitecture analysisLane = frontier.LaneArchitecture
	analysisLaneImplement    analysisLane = frontier.LaneImplement
)

const (
	promptRouteReasonDirectConversation   promptRouteReason = frontier.RouteReasonDirectConversation
	promptRouteReasonDirectImplementation promptRouteReason = frontier.RouteReasonDirectImplementation
	promptRouteReasonLocateRequest        promptRouteReason = frontier.RouteReasonLocateRequest
	promptRouteReasonExploreRequest       promptRouteReason = frontier.RouteReasonExploreRequest
	promptRouteReasonReviewRequest        promptRouteReason = frontier.RouteReasonReviewRequest
	promptRouteReasonArchitectureRequest  promptRouteReason = frontier.RouteReasonArchitectureRequest
)

const (
	answerContractDirectConversation  answerContract = frontier.AnswerContractDirectConversation
	answerContractLocateTargets       answerContract = frontier.AnswerContractLocateTargets
	answerContractGroundedExplanation answerContract = frontier.AnswerContractGroundedExplanation
	answerContractReviewFindings      answerContract = frontier.AnswerContractReviewFindings
	answerContractArchitecture        answerContract = frontier.AnswerContractArchitecture
	answerContractImplementation      answerContract = frontier.AnswerContractImplementation
)

func answerContractForLane(lane analysisLane) answerContract {
	return frontier.AnswerContractForLane(lane)
}

func routeReasonForLane(lane analysisLane) promptRouteReason {
	return frontier.RouteReasonForLane(lane)
}

func laneContractPolicyForLane(lane analysisLane) laneContractPolicy {
	return frontier.LaneContractPolicyForLane(lane)
}

func renderLaneContractPolicy(lane analysisLane) string {
	return frontier.RenderLaneContractPolicy(lane)
}
