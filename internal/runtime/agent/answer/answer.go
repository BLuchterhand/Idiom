// Package answer provides the terminal answer pipeline: acceptance checking,
// decoration, and terminal classification. The Processor interface
// consolidates what was previously scattered across Runner methods, so
// grouped answer logic has a single contract to implement.
package answer

import (
	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/workerruntime"
)

// ---------------------------------------------------------------------------
// Context
// ---------------------------------------------------------------------------

// Context carries the shared inputs for answer pipeline operations.
type Context struct {
	Brief workerruntime.WorkerRuntimeBrief
}

// ---------------------------------------------------------------------------
// Acceptance
// ---------------------------------------------------------------------------

// Decision indicates whether an answer is accepted, rejected, or blocked.
type Decision string

const (
	DecisionAccepted Decision = "accept"
	DecisionRejected Decision = "reject"
)

// Acceptance is the result of evaluating whether an answer is acceptable.
type Acceptance struct {
	Decision    Decision
	BlockerKind string
	Reason      string
}

// ---------------------------------------------------------------------------
// DecorateInput
// ---------------------------------------------------------------------------

// DecorateInput is the input to the decoration step.
type DecorateInput struct {
	Answer     string
	StopReason string
	Results    []workspace.ToolResult
	UserFacing bool
}

// ---------------------------------------------------------------------------
// TerminalResult
// ---------------------------------------------------------------------------

// TerminalOutcome is the classification of the terminal answer.
type TerminalOutcome string

const (
	TerminalAccepted         TerminalOutcome = "accepted"
	TerminalBlocked          TerminalOutcome = "blocked"
	TerminalTransportBlocked TerminalOutcome = "transport_protocol_blocked"
)

// TerminalResult is the final output of the answer pipeline.
type TerminalResult struct {
	Outcome         TerminalOutcome
	RawAnswer       string
	PresentedAnswer string
	StopReason      string
	Acceptance      Acceptance
}

// ---------------------------------------------------------------------------
// Processor interface
// ---------------------------------------------------------------------------

// Processor evaluates, decorates, and classifies the final answer.
//
// Each method corresponds to one stage in the terminal answer pipeline:
//  1. Accept  — check whether the answer is acceptable (or blocked)
//  2. Decorate — transform the raw answer into user-facing form
//  3. Terminate — classify the terminal outcome
//
// New answer pipeline logic should implement this interface rather than
// adding standalone functions in the agent package.
type Processor interface {
	// Accept evaluates whether the model's answer is acceptable.
	Accept(ctx Context, answer string, results []workspace.ToolResult) Acceptance

	// Decorate transforms the raw answer into its final user-facing form.
	Decorate(ctx Context, input DecorateInput) string

	// Terminate classifies the final answer and produces the terminal result.
	Terminate(ctx Context, input DecorateInput) TerminalResult
}
