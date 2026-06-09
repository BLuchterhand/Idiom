package safety

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/util"
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
	Tool            string
	PathPattern     string
	CommandPrefix   string
	CommandContains string
	Decision        PermissionDecision
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
		Target:   PermissionTarget(req),
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
	if r.CommandContains != "" {
		parts = append(parts, "command_contains="+r.CommandContains)
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
	if r.CommandPrefix != "" && !commandMatchesPrefix(strings.TrimSpace(req.Command), r.CommandPrefix) {
		return false
	}
	if r.CommandContains != "" && !strings.Contains(strings.TrimSpace(req.Command), r.CommandContains) {
		return false
	}
	if r.PathPattern != "" {
		target := PermissionTarget(req)
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

func commandMatchesPrefix(command string, prefix string) bool {
	if prefix == "" {
		return true
	}
	if strings.HasSuffix(prefix, " ") || strings.HasSuffix(prefix, "\t") {
		return strings.HasPrefix(command, prefix)
	}
	if command == prefix {
		return true
	}
	if !strings.HasPrefix(command, prefix) {
		return false
	}
	if len(command) <= len(prefix) {
		return true
	}
	return command[len(prefix)] == ' ' || command[len(prefix)] == '\t'
}

func PermissionTarget(req workspace.ToolRequest) string {
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
			Error:   fmt.Sprintf("permission policy denied %s on %s", s.Tool, util.FirstNonEmpty(s.Target, "this target")),
		}
	case PermissionAsk:
		return workspace.ToolResult{
			Tool:    s.Tool,
			Summary: "Tool approval required",
			Error:   fmt.Sprintf("permission policy requires approval for %s on %s", s.Tool, util.FirstNonEmpty(s.Target, "this target")),
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
	head, err := GitCombinedOutput(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return GitCheckpoint{}, err
	}
	status, err := GitCombinedOutput(ctx, root, "status", "--porcelain", "--untracked-files=normal", "--ignore-submodules=all")
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

func GitCombinedOutput(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}
