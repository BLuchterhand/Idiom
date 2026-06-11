package agent

import "idiom/internal/capabilities/workspace"

// delegatedReadOnlyProbeBudget is the soft cap disclosed to post-readiness
// read-only continuations. It is not a hard tool counter; the runner uses the
// advisory closeout nudge, loop fuses, and MaxToolSteps for runaway protection.
const delegatedReadOnlyProbeBudget = 2

const scopedImplementationLargeReadLineThreshold = 800
const scopedImplementationLargeReadByteThreshold = 96 * 1024
const scopedImplementationBoundedReadWindowLines = 180
const scopedImplementationBoundedReadLineCharCap = 400

const forceAnswerTailGatheredEvidence = "Write the normal final answer from the gathered evidence. If one exact claim remains unsupported, state that uncertainty after the supported answer. Do not emit XML tool-call tags, tool JSON, or tool-request syntax — write only the plain user-visible final answer."

type terminalAnswerOutcome string

const (
	terminalAnswerAccepted         terminalAnswerOutcome = "accepted"
	terminalAnswerBlocked          terminalAnswerOutcome = "blocked"
	terminalAnswerTransportBlocked terminalAnswerOutcome = "transport_protocol_blocked"
)

type terminalAnswerDecision struct {
	Outcome         terminalAnswerOutcome
	RawAnswer       string
	PresentedAnswer string
	StopReason      string
	Acceptance      finalAnswerAcceptance
	BlockerResult   workspace.ToolResult
	OverrideSource  string
}
