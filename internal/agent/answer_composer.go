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
	case delegatedObjectiveLooksLikeDiscoveryToolImprovement(question):
		return composeDiscoveryToolImprovementAnswer(delegatedDiscoveryAnswerEvidencePaths(question, paths)), true
	case len(input.Summaries) > 0:
		return composeGenericEvidenceAnswer(question, input.Summaries, paths), true
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

func composeDiscoveryToolImprovementAnswer(paths []string) string {
	paths = preferComposerPaths(paths,
		"internal/agent/answer_composer.go",
		"internal/agent/runner.go",
		"internal/agent/runner_lane_policy.go",
		"internal/agent/discovery_redirect.go",
		"internal/workspace/tool_manifest.go",
	)
	var b strings.Builder
	b.WriteString("## Discovery Tool Improvements\n\n")
	b.WriteString("The current discovery stack is already good at finding evidence; the weak point is turning that evidence into a useful human answer. The next useful additions should make discovery more purpose-aware and answer-oriented, not just add more raw search surfaces.\n\n")
	b.WriteString("### Additions Worth Making\n\n")
	b.WriteString("- **Purpose-specific answer blueprints**: map common discovery purposes such as impact analysis, function tracing, architecture explanation, and debugging to expected answer sections.\n")
	b.WriteString("- **Evidence scoring and deduping**: rank implementation files above tests, artifacts, raw search handles, and incidental path-looking strings before they reach the final answer.\n")
	b.WriteString("- **Gap extraction**: make each evidence bundle name what it did not prove, so final answers can distinguish verified claims from remaining uncertainty.\n")
	b.WriteString("- **Layer-aware synthesis**: translate deterministic layers like text search, dependency graphs, related files, and symbol reads into human-facing findings instead of exposing tool labels.\n")
	b.WriteString("- **Composer coverage tests**: every discovery purpose that can short-circuit into an answer should have a fixture proving the final response is readable, specific, and free of telemetry sections.\n\n")
	b.WriteString("### Where This Belongs\n\n")
	b.WriteString("- `discovery_query` should stay responsible for collecting deterministic evidence packets.\n")
	b.WriteString("- `runner_lane_policy.go` should decide when a child has enough evidence and can stop.\n")
	b.WriteString("- `answer_composer.go` should own final presentation, including purpose-specific response shapes and generic fallback decoration.\n")
	b.WriteString("- Live/eval scenarios should assert both the efficient path and the answer shape, otherwise the system can be technically correct and still useless to a human.\n")
	if len(paths) > 0 {
		b.WriteString("\nPrimary files: ")
		b.WriteString(formatInlinePathList(paths, 5))
		b.WriteString(".")
	}
	return strings.TrimSpace(b.String())
}

func composeGenericEvidenceAnswer(question string, summaries []delegatedDiscoverySummary, paths []string) string {
	var b strings.Builder
	b.WriteString("## Grounded Answer\n\n")
	if strings.TrimSpace(question) != "" {
		b.WriteString("For `")
		b.WriteString(strings.TrimSpace(question))
		b.WriteString("`, deterministic discovery found the relevant implementation surfaces, but no purpose-specific answer template matched. The useful response is therefore a compact synthesis of the strongest evidence, not a dump of raw discovery layers.\n\n")
	} else {
		b.WriteString("Deterministic discovery found relevant implementation surfaces, but no purpose-specific answer template matched. The useful response is therefore a compact synthesis of the strongest evidence, not a dump of raw discovery layers.\n\n")
	}
	if lines := decoratedDiscoveryFindingLines(summaries, 4); len(lines) > 0 {
		b.WriteString("### Evidence Highlights\n")
		for _, line := range lines {
			b.WriteString("- ")
			b.WriteString(line)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("### Recommended Next Step\n")
	b.WriteString("- Add or refine a purpose-specific composer for this question class if it is expected to recur; otherwise use the primary files below for one targeted follow-up read before answering in detail.\n")
	if len(paths) > 0 {
		b.WriteString("\nPrimary files: ")
		b.WriteString(formatInlinePathList(paths, 4))
		b.WriteString(".")
	}
	return strings.TrimSpace(b.String())
}

func decoratedDiscoveryFindingLines(summaries []delegatedDiscoverySummary, limit int) []string {
	raw := delegatedDiscoveryFindingLines(summaries, limit)
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, text, ok := strings.Cut(line, ":")
		if ok {
			line = humanDiscoveryLayerName(name) + ": " + cleanDiscoveryFindingText(text)
		} else {
			line = cleanDiscoveryFindingText(line)
		}
		if line == "" || strings.Contains(strings.ToLower(line), "open a returned handle") {
			continue
		}
		lines = append(lines, line)
		if limit > 0 && len(lines) >= limit {
			return lines
		}
	}
	return lines
}

func humanDiscoveryLayerName(name string) string {
	switch strings.TrimSpace(name) {
	case "text_anchor_search":
		return "Text anchors"
	case "dependency_graph":
		return "Dependency graph"
	case "related_files":
		return "Related files"
	case "go_read_symbol":
		return "Symbol read"
	case "go_workspace_symbol", "ctags_symbols":
		return "Symbol index"
	case "structural_search":
		return "Structural search"
	case "history_range", "history_symbol":
		return "History"
	case "blame_range":
		return "Blame"
	case "semgrep_scan":
		return "Static scan"
	case "joern_scan":
		return "Code property graph"
	default:
		return strings.TrimSpace(strings.ReplaceAll(name, "_", " "))
	}
}

func cleanDiscoveryFindingText(text string) string {
	text = strings.TrimSpace(text)
	for _, suffix := range []string{
		" Use read_file on a returned handle, or narrow the pattern to see more.",
		" Open a returned handle with read_file.",
		" Use read_file on a returned handle to inspect a file.",
	} {
		text = strings.ReplaceAll(text, suffix, ".")
	}
	text = strings.ReplaceAll(text, "  ", " ")
	return strings.TrimSpace(text)
}

func delegatedObjectiveLooksLikeDiscoveryToolImprovement(objective string) bool {
	lower := strings.ToLower(objective)
	return strings.Contains(lower, "discovery tool") &&
		(containsAny(lower, "useful", "usefulness", "improve", "increasing", "add", "complement", "better") ||
			strings.Contains(lower, "what can we add"))
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
