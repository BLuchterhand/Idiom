package agent

import (
	"encoding/json"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/llama"
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

func (r runnerTurnRecorder) record(step int, req workspace.ToolRequest, result workspace.ToolResult, reason string, opts runnerRecordOptions) {
	opts.SetLastToolKey = true
	r.recordWithOptions(step, req, result, reason, opts)
}

func (r runnerTurnRecorder) recordWithOptions(step int, req workspace.ToolRequest, result workspace.ToolResult, reason string, opts runnerRecordOptions) {
	result = resultWithRejectedRequestTarget(req, result)
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
		modelResult := r.modelFacingToolResult(step, req, result, reason)

		*r.turnMessages = append(*r.turnMessages, toolResultMessage(r.activeToolCall, modelResult))
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

func resultWithRejectedRequestTarget(req workspace.ToolRequest, result workspace.ToolResult) workspace.ToolResult {
	if strings.TrimSpace(result.Error) == "" || !workspace.IsWorkspaceWriteTool(req.Tool) {
		return result
	}

	path := normalizeRunnerPath(req.Path)
	if path == "" || strings.Contains(result.Summary, path) || strings.Contains(result.Output, path) {
		return result
	}

	if strings.TrimSpace(result.Output) == "" {
		result.Output = "Rejected write target: `" + path + "`"
	} else {
		result.Output = strings.TrimRight(result.Output, "\n") + "\nRejected write target: `" + path + "`"
	}

	return result
}

func (r runnerTurnRecorder) modelFacingToolResult(step int, req workspace.ToolRequest, result workspace.ToolResult, reason string) workspace.ToolResult {
	if r.runner == nil || strings.TrimSpace(result.Error) == "" || strings.TrimSpace(reason) != "tool_rejected" {
		return result
	}

	packet := buildRunnerFailureFeedbackPacketWithAllowedTools(
		r.runner.agentName(),
		step,
		reason,
		req,
		result,
		r.snapshotResults(),
		r.declarationMovesForbidden,
		r.runner.RuntimeBrief,
		r.runner.currentAllowedTools,
	)
	feedback := renderModelFacingFailureFeedback(packet)

	if feedback == "" {
		return result
	}

	modelResult := result

	modelResult.Error = appendRecoveryCardToMessage(modelResult.Error, feedback)

	return modelResult
}

func renderModelFacingFailureFeedback(packet runnerFailureFeedbackPacket) string {
	var b strings.Builder

	b.WriteString("Recovery feedback")
	writeModelFacingFeedbackList(&b, "do_not_repeat", packet.DoNotRepeat, 5)
	writeModelFacingFeedbackList(&b, "required_next", packet.RequiredNext, 5)
	writeModelFacingFeedbackList(&b, "valid_next", packet.ValidNext, 5)
	writeModelFacingFeedbackList(&b, "evidence_status", packet.EvidenceStatus, 3)

	if card := strings.TrimSpace(packet.RecoveryCard); card != "" {
		b.WriteString("\n")
		b.WriteString(card)
	}

	feedback := strings.TrimSpace(b.String())
	if feedback == "Recovery feedback" {
		return ""
	}

	return feedback
}

func writeModelFacingFeedbackList(b *strings.Builder, label string, values []string, limit int) {
	values = nonEmptyRunnerStrings(values, limit)
	if len(values) == 0 {
		return
	}

	b.WriteString("\n")
	b.WriteString(label)
	b.WriteString(":")

	for _, value := range values {
		b.WriteString("\n- ")
		b.WriteString(value)
	}
}

func (r runnerTurnRecorder) snapshotResults() []workspace.ToolResult {
	if r.collectedResults == nil {
		return nil
	}

	return *r.collectedResults
}
