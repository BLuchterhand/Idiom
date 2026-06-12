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
	if input.rebuildTools != nil {
		input.rebuildTools()
	}

	return true
}
