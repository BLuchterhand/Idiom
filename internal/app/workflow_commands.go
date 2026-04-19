package app

import "strings"

func workflowCommandPrompt(name string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "latest-log", "logs":
		return "Check the most recent debug log. Identify the exact failure, explain why it happened, implement the smallest safe fix, then run targeted verification and report the command/output status.", true
	case "implement-plan", "build-plan":
		return "Resume the latest durable implementation plan. If explicit approval exists, switch to build mode, restate objective, constraints, target files, and verification checks, then implement the smallest bounded change and verify it.", true
	case "verify":
		return "Run focused verification for the current implementation state. Do not edit files. Report the exact command, pass/fail/skipped status, and failure output if any.", true
	case "frontier-audit":
		return "Audit the harness for frontier-agent capability gaps across routing, planning, control rails, memory, tool use, verification, and eval coverage. Ground claims in files, update the implementation tracker, implement the highest-leverage safe improvements, and test them.", true
	case "opencode-audit":
		return "Compare this harness's decisioning and routing with OpenCode-style strengths: explicit modes, plan/build handoff, tool gates, subtask delegation, and command templates. Identify concrete bugs or inefficiencies in our code, implement safe improvements, and add tests.", true
	default:
		return "", false
	}
}
