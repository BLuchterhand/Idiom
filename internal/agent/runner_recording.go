package agent

import (
	"encoding/json"
	"strings"

	"idiom/internal/llama"
	"idiom/internal/workspace"
)

type runnerTurnRecorder struct {
	runner                    *Runner
	activeToolCall            *llama.ToolCall
	onToolResult              func(ToolResultEvent)
	collectedResults          *[]workspace.ToolResult
	turnMessages              *[]llama.ChatMessage
	lastToolKey               *string
	lastToolResult            **workspace.ToolResult
	consecutiveRejectedAction *int
	consecutiveNoProgress     *int
	metrics                   *runnerMetrics
	declarationMovesForbidden bool
}

type runnerRecordOptions struct {
	TrackProgress           bool
	ResetRejected           bool
	CountRejected           bool
	IncrementNoProgress     bool
	IncrementRejectedStreak bool
	SetLastToolKey          bool
}

func (r runnerTurnRecorder) record(step int, req workspace.ToolRequest, result workspace.ToolResult, reason string, trackProgress bool, resetRejected bool) {
	r.recordWithOptions(step, req, result, reason, runnerRecordOptions{
		TrackProgress:  trackProgress,
		ResetRejected:  resetRejected,
		SetLastToolKey: true,
	})
}

func (r runnerTurnRecorder) recordWithOptions(step int, req workspace.ToolRequest, result workspace.ToolResult, reason string, opts runnerRecordOptions) {
	if r.collectedResults != nil {
		*r.collectedResults = append(*r.collectedResults, result)
	}
	if opts.SetLastToolKey && r.lastToolKey != nil {
		*r.lastToolKey = toolRequestKey(req)
	}
	if r.lastToolResult != nil {
		resultCopy := result
		*r.lastToolResult = &resultCopy
	}
	if opts.ResetRejected && r.consecutiveRejectedAction != nil {
		*r.consecutiveRejectedAction = 0
	}
	if opts.IncrementRejectedStreak && r.consecutiveRejectedAction != nil {
		*r.consecutiveRejectedAction++
	}
	if opts.CountRejected && r.metrics != nil {
		r.metrics.RejectedActions++
	}
	if opts.TrackProgress && r.consecutiveNoProgress != nil {
		if toolResultMadeProgress(req, result) {
			*r.consecutiveNoProgress = 0
			if r.metrics != nil {
				r.metrics.ProgressTurns++
			}
		} else {
			*r.consecutiveNoProgress++
		}
	} else if opts.IncrementNoProgress && r.consecutiveNoProgress != nil {
		*r.consecutiveNoProgress++
	}
	if r.onToolResult != nil && r.runner != nil {
		r.onToolResult(ToolResultEvent{Step: step, Agent: r.runner.agentName(), Request: req, Result: result})
	}
	if r.turnMessages != nil {
		*r.turnMessages = append(*r.turnMessages, toolResultMessage(r.activeToolCall, result))
	}
	if r.runner != nil {
		r.runner.emitNextActionState(step, reason, req, result, r.snapshotResults(), r.declarationMovesForbidden)
		if strings.TrimSpace(result.Error) != "" {
			packet := buildRunnerFailureFeedbackPacketWithAllowedTools(r.runner.agentName(), step, reason, req, result, r.snapshotResults(), r.declarationMovesForbidden, r.runner.RuntimeBrief, r.runner.currentAllowedTools)
			if len(packet.DoNotRepeat) > 0 || len(packet.RequiredNext) > 0 || strings.TrimSpace(packet.ResultError) != "" {
				if data, err := json.Marshal(packet); err == nil {
					r.runner.emitEvent(RunEvent{Role: "failure_feedback", Content: string(data)})
				}
			}
		}
	}
}

func (r runnerTurnRecorder) snapshotResults() []workspace.ToolResult {
	if r.collectedResults == nil {
		return nil
	}
	return *r.collectedResults
}
