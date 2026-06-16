// Package evidence provides the common evaluation interface and result types
// for evidence evaluation in the agent runner. The Evaluator interface
// consolidates what was previously scattered across multiple free-standing
// functions and types in the agent package, giving grouped evidence logic a
// single contract to implement.
package evidence

import (
	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/agent/agentutil"
	"idiom/internal/runtime/workerruntime"
)

// ---------------------------------------------------------------------------
// EvalContext
// ---------------------------------------------------------------------------

// EvalContext carries the shared inputs for all evidence evaluations.
type EvalContext struct {
	Brief    workerruntime.WorkerRuntimeBrief
	Results  []workspace.ToolResult
	Registry *PathEvidenceRegistry
}

// Objective returns the evaluated brief's objective, normalized.
func (c EvalContext) Objective() string {
	return c.Brief.Objective
}

// ReadPathsFromResults is a convenience wrapper around agentutil.ReadPathsFromToolResult.
func (c EvalContext) ReadPathsFromResults(result workspace.ToolResult) []string {
	return agentutil.ReadPathsFromToolResult(result)
}

// NormalizePath normalizes a workspace path using the shared helper.
func (c EvalContext) NormalizePath(path string) string {
	return agentutil.NormalizeRunnerPath(path)
}

// ---------------------------------------------------------------------------
// Evaluator interface
// ---------------------------------------------------------------------------

// Evaluator evaluates tool result evidence against a brief and returns a
// structured result. Each concrete Evaluator returns its own result type,
// but all satisfy the EvalResult interface for uniform consumption.
//
// New evidence evaluations should implement this interface rather than
// creating standalone functions in the agent package. This ensures shared
// helpers (path extraction, normalization, string utilities) are used
// consistently rather than duplicated.
type Evaluator interface {
	// Eval processes tool results and returns a structured evidence judgment.
	Eval(ctx EvalContext) EvalResult
}

// ---------------------------------------------------------------------------
// EvalResult interface
// ---------------------------------------------------------------------------

// EvalResult is the minimal interface satisfied by all evidence evaluation
// results. Concrete result types (AnswerabilityLedger, Closeout, etc.)
// embed or implement this interface alongside their specific fields.
type EvalResult interface {
	// Ready reports whether evidence is sufficient for the evaluator's purpose.
	Ready() bool

	// SourceBodyPaths returns paths that received source-body reads during
	// evaluation.
	SourceBodyPaths() []string
}
