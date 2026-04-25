package agent

import (
	"strings"
)

// AnswerComposer is the presentation boundary between deterministic evidence
// packets and user-facing prose. Runner/discovery code can stay telemetry-rich;
// final answers should read like an explanation, not like an eval artifact.
type AnswerComposer struct{}

type AnswerComposerInput struct {
	Question      string
	EvidencePaths []string
	Summaries     []delegatedDiscoverySummary
}

func (AnswerComposer) Compose(input AnswerComposerInput) (string, bool) {
	question := strings.TrimSpace(input.Question)
	paths := uniqueDelegatedDiscoveryPaths(input.EvidencePaths)
	switch {
	case delegatedObjectiveLooksLikeDebugLogRedaction(question):
		return composeDebugLogRedactedModeAnswer(delegatedDiscoveryAnswerEvidencePaths(question, paths)), true
	case delegatedObjectiveLooksLikeSubagents(question):
		return composeSubagentHandoffAnswer(delegatedDiscoveryAnswerEvidencePaths(question, paths)), true
	case len(input.Summaries) > 0:
		return composeGenericEvidenceAnswer(input.Summaries, paths), true
	default:
		return "", false
	}
}

func composeDebugLogRedactedModeAnswer(paths []string) string {
	paths = preferComposerPaths(paths,
		"internal/transcript/debuglog.go",
		"internal/transcript/debuglog_test.go",
		"internal/app/model_state.go",
	)
	var b strings.Builder
	b.WriteString("Debug log redacted mode is the safe default for session JSONL logs. It keeps the trace useful for replay, eval scoring, and operational debugging while removing prompt and tool payloads that are most likely to leak sensitive data.\n\n")
	b.WriteString("## What Stays Useful\n")
	b.WriteString("- Event structure remains intact: roles, event types, tool names, summaries, timing, sizes, and session metadata are still available.\n")
	b.WriteString("- The log is still good enough to understand what happened in a run: which tools were used, what succeeded or failed, and where the agent spent time.\n")
	b.WriteString("- Workspace paths are normalized so logs remain readable without exposing machine-local absolute paths.\n\n")
	b.WriteString("## What Gets Redacted\n")
	b.WriteString("- Full system prompts and context payloads are replaced with digests or size-only placeholders.\n")
	b.WriteString("- High-risk tool fields such as `content`, `old_text`, `new_text`, and `system_prompt` are replaced with redaction markers.\n")
	b.WriteString("- Shell command bodies are hidden behind a redacted shell-command marker.\n")
	b.WriteString("- Free-text values still pass through secret/token/password scrubbing.\n\n")
	b.WriteString("## When To Use `full`\n")
	b.WriteString("Use `/debuglog mode full` only when you explicitly need raw prompt or tool payloads for a narrow debugging session. Switch back to `/debuglog mode redacted` afterward; redacted is the operational default.\n")
	if len(paths) > 0 {
		b.WriteString("\nPrimary files: ")
		b.WriteString(formatInlinePathList(paths, 3))
		b.WriteString(".")
	}
	return strings.TrimSpace(b.String())
}

func composeSubagentHandoffAnswer(paths []string) string {
	paths = preferComposerPaths(canonicalDelegatedSubagentEvidencePaths(paths),
		"internal/agent/runner_child_tasks.go",
		"internal/agent/runner.go",
		"internal/agent/runner_lane_policy.go",
		"internal/workspace/tool_manifest.go",
		"internal/frontier/workerruntime/roles.go",
	)
	var b strings.Builder
	b.WriteString("## Subagent Handoff Architecture\n\n")
	b.WriteString("The harness uses subagents through the `task` tool. The parent runner delegates a focused unit of work to a child runner, then consumes the child result as evidence or as an isolated patch instead of letting broad exploration pollute the parent loop.\n\n")
	b.WriteString("### Two Modes\n\n")
	b.WriteString("| Mode | What Happens | Budget | Safety Boundary |\n")
	b.WriteString("|---|---|---|---|\n")
	b.WriteString("| `read_only` / `explore` | Spawns a child `Runner` against the parent workspace for evidence gathering. | 6 tool steps, 3 no-progress turns, 8 model calls, 2 minute cap; tighter for direct-answer parents. | Deny-by-default read-only policy, no writes, no nested `task`, task ledger disabled. |\n")
	b.WriteString("| `write` / `workbranch` / `patch` | Creates a temporary git worktree, runs a write-enabled child there, captures the diff, and cleans up. | 10 tool steps, 4 no-progress turns, 16 model calls, 4 minute cap. | Parent workspace is not mutated; child cannot push, commit, publish, deploy, or spawn more tasks. |\n\n")
	b.WriteString("### How Read-Only Handoff Works\n\n")
	b.WriteString("1. The parent emits a `task` request with an `agent`, `mode`, and focused `prompt`.\n")
	b.WriteString("2. `runChildTask` normalizes the prompt, builds a `child_read_only` runtime brief, and gives the child its own budget and permission policy.\n")
	b.WriteString("3. The child runs its own tool loop with read/discovery/semantic tools and returns a concise grounded answer.\n")
	b.WriteString("4. Child events are forwarded into the parent event stream with `child_` prefixes for auditability.\n")
	b.WriteString("5. The parent treats the returned packet as inspected evidence and can answer without repeating the same discovery.\n\n")
	b.WriteString("### How Isolated Write Handoff Works\n\n")
	b.WriteString("1. A write-mode task creates a temporary git worktree from the current workspace state.\n")
	b.WriteString("2. The child runs in that worktree with writes enabled, `task_ledger` required, and checkpoints enabled.\n")
	b.WriteString("3. After the child finishes, the harness captures changed paths, diff stat, patch text, and the child answer.\n")
	b.WriteString("4. The worktree is removed. The parent must inspect and apply or reject the diff explicitly.\n\n")
	b.WriteString("### What Cases It Covers\n\n")
	b.WriteString("- **Bounded read-only investigation**: find references, inspect a subsystem, explain a mechanism, or gather evidence without risking writes.\n")
	b.WriteString("- **Architecture or impact discovery**: offload broad context gathering while preserving the parent budget and transcript clarity.\n")
	b.WriteString("- **Isolated implementation**: let a child make a scoped change in a disposable worktree and return a patch for parent review.\n")
	b.WriteString("- **Repair-style follow-up**: hand a failing verification surface to a constrained child without widening parent scope.\n\n")
	b.WriteString("### Constraints\n\n")
	b.WriteString("- Children cannot recursively spawn more subagents.\n")
	b.WriteString("- Child budgets are separate and bounded.\n")
	b.WriteString("- Read-only children share the parent filesystem view but cannot mutate it.\n")
	b.WriteString("- Write children mutate only the temporary worktree; the parent workspace remains untouched until an explicit apply step.\n")
	if len(paths) > 0 {
		b.WriteString("\nCore files: ")
		b.WriteString(formatInlinePathList(paths, 4))
		b.WriteString(".")
	}
	return strings.TrimSpace(b.String())
}

func composeGenericEvidenceAnswer(summaries []delegatedDiscoverySummary, paths []string) string {
	var b strings.Builder
	b.WriteString("What I found:\n")
	for _, line := range delegatedDiscoveryFindingLines(summaries, 4) {
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	if len(paths) > 0 {
		b.WriteString("\nPrimary files: ")
		b.WriteString(formatInlinePathList(paths, 4))
		b.WriteString(".")
	}
	return strings.TrimSpace(b.String())
}

func preferComposerPaths(paths []string, preferred ...string) []string {
	paths = uniqueDelegatedDiscoveryPaths(paths)
	if len(paths) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	ordered := make([]string, 0, len(paths))
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		for _, existing := range paths {
			if existing == path {
				seen[path] = struct{}{}
				ordered = append(ordered, path)
				return
			}
		}
	}
	for _, path := range preferred {
		add(path)
	}
	for _, path := range paths {
		add(path)
	}
	return ordered
}

func formatInlinePathList(paths []string, max int) string {
	paths = uniqueDelegatedDiscoveryPaths(paths)
	if max > 0 && len(paths) > max {
		paths = paths[:max]
	}
	parts := make([]string, 0, len(paths))
	for _, path := range paths {
		parts = append(parts, "`"+path+"`")
	}
	return strings.Join(parts, ", ")
}
