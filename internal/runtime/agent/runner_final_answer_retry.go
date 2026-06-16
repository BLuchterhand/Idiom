package agent

import (
	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/llama"
)

type runnerFinalAnswerRetryInput struct {
	stepNumber               int
	activeToolCall           *llama.ToolCall
	assistantMessage         llama.ChatMessage
	acceptance               finalAnswerAcceptance
	result                   workspace.ToolResult
	collectedResults         []workspace.ToolResult
	currentAllowedTools      []string
	completionGateRejections *int
	turnMessages             *[]llama.ChatMessage
	turnHiddenTools          map[string]bool
	controller               *runnerTurnController
	autonomyRecorder         *runnerAutonomyRecorder
	rebuildTools             func()
}

func (r *Runner) retryRejectedFinalAnswerForRun(input runnerFinalAnswerRetryInput) bool {
	rejections := 0
	if input.completionGateRejections != nil {
		rejections = *input.completionGateRejections
	}

	state := &runnerRunState{
		collectedResults:         input.collectedResults,
		currentAllowedTools:      input.currentAllowedTools,
		completionGateRejections: rejections,
		autonomyRecorder:         input.autonomyRecorder,
	}
	if input.controller != nil {
		state.controller = *input.controller
	}

	retry, prompt := r.retryRejectedFinalAnswer(state, input.stepNumber, input.activeToolCall, input.assistantMessage, input.acceptance, input.result)
	if !retry {
		return false
	}
	if input.completionGateRejections != nil {
		*input.completionGateRejections = state.completionGateRejections
	}
	if input.turnMessages != nil && prompt != "" {
		*input.turnMessages = append(*input.turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
	}

	delete(input.turnHiddenTools, workspace.ToolNameCodeNavigate)
	delete(input.turnHiddenTools, workspace.ToolNameReadFile)

	// When the retry is for a source-grounding rejection (model cited paths
	// without reading them), hide all non-read tools so the model's only
	// available action is to call read_file or code_navigate for the missing
	// path. This prevents the model from trying to fix the answer text in
	// prose instead of reading the cited file.
	if input.result.Error != "" && isSourceGroundingError(input.result.Error) {
		hideNonSourceBodyReadTools(input.turnHiddenTools, input.currentAllowedTools)
		r.sourceGroundingProseRetriesLeft = 1
	}
	if input.rebuildTools != nil {
		input.rebuildTools()
	}

	return true
}
