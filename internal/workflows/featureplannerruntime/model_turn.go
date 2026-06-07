package featureplannerruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

type Logger func(role string, content string)

func CompleteModelTurn(ctx context.Context, client llama.LLMClient, prompt string, opts llama.ChatOptions, taskFamily string, log Logger) (string, error) {
	logFeaturePlannerMessage(log, "feature_planner_prompt", prompt)
	thinking := false
	opts.EnableThinking = &thinking
	opts.ResponseFormatJSON = workerruntime.TaskFamilyIsFeaturePlannerPlanner(taskFamily)
	opts.MaxTokens = 16384

	reply, err := client.CompleteChatMessage(ctx, []llama.ChatMessage{{Role: "user", Content: prompt}}, opts)
	if err != nil {
		return "", err
	}
	answer := strings.TrimSpace(reply.Content)
	if answer == "" && workerruntime.TaskFamilyIsFeaturePlannerPlanner(taskFamily) {
		logFeaturePlannerMessage(log, "feature_planner_model_empty", emptyModelMessage(reply.FinishReason, true))
		retryOpts := opts
		retryOpts.ResponseFormatJSON = false
		retryPrompt := buildEmptyRetryPrompt(prompt, reply.FinishReason)
		reply, err = client.CompleteChatMessage(ctx, []llama.ChatMessage{{Role: "user", Content: retryPrompt}}, retryOpts)
		if err != nil {
			return "", err
		}
		answer = strings.TrimSpace(reply.Content)
	}
	if answer == "" {
		return "", fmt.Errorf("feature-planner model returned empty content after retry (finish_reason=%q)", strings.TrimSpace(reply.FinishReason))
	}
	logFeaturePlannerMessage(log, "feature_planner_model", answer)
	return answer, nil
}

func logFeaturePlannerMessage(log Logger, role string, content string) {
	if log != nil {
		log(role, content)
	}
}

func emptyModelMessage(finishReason string, responseFormatJSON bool) string {
	fields := map[string]any{
		"finish_reason":        strings.TrimSpace(finishReason),
		"response_format_json": responseFormatJSON,
		"retry":                "same planner contract with response_format_json disabled",
	}
	data, _ := json.Marshal(fields)
	return string(data)
}

func buildEmptyRetryPrompt(originalPrompt string, finishReason string) string {
	var b strings.Builder
	b.WriteString("The prior feature-planner response had empty visible content")
	if reason := strings.TrimSpace(finishReason); reason != "" {
		b.WriteString(" with finish_reason=")
		b.WriteString(reason)
	}
	b.WriteString(". Retry once. Emit exactly one visible JSON object that satisfies the feature-planner contract. Do not put the JSON only in hidden reasoning.\n\n")
	b.WriteString(originalPrompt)
	return b.String()
}
