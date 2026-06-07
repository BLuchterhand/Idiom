package agent

import (
	"context"
	"encoding/json"

	"idiom/internal/capabilities/workspace"
	agentsafety "idiom/internal/runtime/agent/safety"
)

type PermissionDecision = agentsafety.PermissionDecision

const (
	PermissionAllow = agentsafety.PermissionAllow
	PermissionAsk   = agentsafety.PermissionAsk
	PermissionDeny  = agentsafety.PermissionDeny
)

type ToolPermissionPolicy = agentsafety.ToolPermissionPolicy
type ToolPermissionRule = agentsafety.ToolPermissionRule
type PermissionDecisionSnapshot = agentsafety.PermissionDecisionSnapshot
type CheckpointPolicy = agentsafety.CheckpointPolicy
type GitCheckpoint = agentsafety.GitCheckpoint

func CaptureGitCheckpoint(ctx context.Context, root string) (GitCheckpoint, error) {
	return agentsafety.CaptureGitCheckpoint(ctx, root)
}

func permissionTarget(req workspace.ToolRequest) string {
	return agentsafety.PermissionTarget(req)
}

func gitCombinedOutput(ctx context.Context, root string, args ...string) (string, error) {
	return agentsafety.GitCombinedOutput(ctx, root, args...)
}

func marshalRunEvent(role string, value any) RunEvent {
	data, err := json.Marshal(value)
	if err != nil {
		return RunEvent{}
	}
	return RunEvent{Role: role, Content: string(data)}
}
