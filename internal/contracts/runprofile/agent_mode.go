package runprofile

import "strings"

// AgentMode names a user-selectable runtime permission and prompt profile.
type AgentMode string

const (
	AgentModeBuild   AgentMode = "build"
	AgentModePlan    AgentMode = "plan"
	AgentModeGeneral AgentMode = "general"
)

// ParseAgentMode normalizes the case and surrounding whitespace accepted by
// the existing configuration and command surfaces.
func ParseAgentMode(value string) (AgentMode, bool) {
	mode := AgentMode(strings.ToLower(strings.TrimSpace(value)))
	switch mode {
	case AgentModeBuild, AgentModePlan, AgentModeGeneral:
		return mode, true
	default:
		return "", false
	}
}
