package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"qwen-bubbles/internal/workspace"
)

type PermissionDecision string

const (
	PermissionAllow PermissionDecision = "allow"
	PermissionAsk   PermissionDecision = "ask"
	PermissionDeny  PermissionDecision = "deny"
)

type ToolPermissionPolicy struct {
	Default PermissionDecision
	Rules   []ToolPermissionRule
}

type ToolPermissionRule struct {
	Tool          string
	PathPattern   string
	CommandPrefix string
	Decision      PermissionDecision
}

type PermissionDecisionSnapshot struct {
	Tool     string             `json:"tool"`
	Target   string             `json:"target,omitempty"`
	Decision PermissionDecision `json:"decision"`
	Rule     string             `json:"rule,omitempty"`
}

type CheckpointPolicy struct {
	Enabled bool
}

type GitCheckpoint struct {
	Root   string `json:"root"`
	Head   string `json:"head,omitempty"`
	Dirty  bool   `json:"dirty"`
	Status string `json:"status,omitempty"`
}

func (p ToolPermissionPolicy) Decide(req workspace.ToolRequest) PermissionDecisionSnapshot {
	decision := p.Default
	if decision == "" {
		decision = PermissionAllow
	}
	snapshot := PermissionDecisionSnapshot{
		Tool:     strings.TrimSpace(req.Tool),
		Target:   permissionTarget(req),
		Decision: decision,
	}
	for _, rule := range p.Rules {
		if !rule.matches(req) {
			continue
		}
		if rule.Decision == "" {
			continue
		}
		snapshot.Decision = rule.Decision
		snapshot.Rule = rule.String()
	}
	return snapshot
}

func (r ToolPermissionRule) String() string {
	parts := []string{}
	if r.Tool != "" {
		parts = append(parts, "tool="+r.Tool)
	}
	if r.PathPattern != "" {
		parts = append(parts, "path="+r.PathPattern)
	}
	if r.CommandPrefix != "" {
		parts = append(parts, "command="+r.CommandPrefix)
	}
	if r.Decision != "" {
		parts = append(parts, "decision="+string(r.Decision))
	}
	return strings.Join(parts, " ")
}

func (r ToolPermissionRule) matches(req workspace.ToolRequest) bool {
	if r.Tool != "" && r.Tool != "*" && r.Tool != strings.TrimSpace(req.Tool) {
		return false
	}
	if r.CommandPrefix != "" && !strings.HasPrefix(strings.TrimSpace(req.Command), r.CommandPrefix) {
		return false
	}
	if r.PathPattern != "" {
		target := permissionTarget(req)
		if target == "" {
			return false
		}
		ok, err := filepath.Match(r.PathPattern, target)
		if err != nil || !ok {
			return false
		}
	}
	return true
}

func permissionTarget(req workspace.ToolRequest) string {
	for _, value := range []string{req.Path, req.Destination, req.Workdir} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return filepath.ToSlash(trimmed)
		}
	}
	if len(req.Moves) > 0 {
		return filepath.ToSlash(strings.TrimSpace(req.Moves[0].Destination))
	}
	return ""
}

func (s PermissionDecisionSnapshot) ToolResult() workspace.ToolResult {
	switch s.Decision {
	case PermissionDeny:
		return workspace.ToolResult{
			Tool:    s.Tool,
			Summary: "Tool permission denied",
			Error:   fmt.Sprintf("permission policy denied %s on %s", s.Tool, firstNonEmpty(s.Target, "this target")),
		}
	case PermissionAsk:
		return workspace.ToolResult{
			Tool:    s.Tool,
			Summary: "Tool approval required",
			Error:   fmt.Sprintf("permission policy requires approval for %s on %s", s.Tool, firstNonEmpty(s.Target, "this target")),
		}
	default:
		return workspace.ToolResult{}
	}
}

func CaptureGitCheckpoint(ctx context.Context, root string) (GitCheckpoint, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return GitCheckpoint{}, fmt.Errorf("checkpoint root is required")
	}
	head, err := gitCombinedOutput(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return GitCheckpoint{}, err
	}
	status, err := gitCombinedOutput(ctx, root, "status", "--porcelain", "--untracked-files=normal", "--ignore-submodules=all")
	if err != nil {
		return GitCheckpoint{}, err
	}
	trimmedStatus := strings.TrimSpace(status)
	return GitCheckpoint{
		Root:   root,
		Head:   strings.TrimSpace(head),
		Dirty:  trimmedStatus != "",
		Status: trimmedStatus,
	}, nil
}

func gitCombinedOutput(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func marshalRunEvent(role string, value any) RunEvent {
	data, err := json.Marshal(value)
	if err != nil {
		return RunEvent{}
	}
	return RunEvent{Role: role, Content: string(data)}
}
