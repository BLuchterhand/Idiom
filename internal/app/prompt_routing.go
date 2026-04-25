package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/agent"
	"idiom/internal/frontier/pathparse"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
)

// promptExecutionPlan is the minimal OpenCode-aligned carrier for a turn.
// All the classification fields from the prior lane-based shape are gone;
// the Mode the user picked is the only routing signal the harness cares
// about.
type promptExecutionPlan struct {
	mode         agent.Mode
	route        turnRoutePlan
	runtimeBrief workerruntime.WorkerRuntimeBrief
}

// RunPromptOnce is the single entry point every caller uses to send one
// prompt and get one reply. There are no resume variants, no lane
// dispatch, no task-memory gates — OpenCode's shape is one loop.
func (m *Model) RunPromptOnce(ctx context.Context, prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}
	m.emitJSONStart(prompt)
	if err := m.ensureServerReady(ctx); err != nil {
		m.emitJSONError("server_not_ready", err.Error())
		return "", err
	}
	return m.runStandardPromptOnce(ctx, prompt)
}

func (m *Model) runStandardPromptOnce(ctx context.Context, prompt string) (string, error) {
	m.lastAssistantReasoningContent = ""
	plan := m.buildPromptExecutionPlan(ctx, prompt)
	chatOpts := tuneChatOptionsForPlan(m.chatOptions(), plan)
	if m.workspace != nil {
		m.workspace.PrimeDiscoveredPaths(promptMentionedWorkspacePaths(prompt))
	}
	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: prompt})
	m.logMessage("user", prompt)
	m.logMessage("thinking_config", samplingSummaryForOptions(chatOpts))
	if summary := strings.TrimSpace(renderTurnRoutePlan(plan.route)); summary != "" {
		m.logMessage("route_plan", summary)
	}
	reply, err := m.runAgentTurn(ctx, prompt, m.requestMessages(), chatOpts, plan)
	if err != nil {
		m.emitJSONError("run_failed", err.Error())
		return "", err
	}
	return m.finishPromptOnceReply(reply), nil
}

func (m *Model) finishPromptOnceReply(reply string) string {
	m.messages = append(m.messages, llama.ChatMessage{Role: "assistant", Content: reply, ReasoningContent: m.takeLastAssistantReasoningContent()})
	m.logMessage("assistant", reply)
	m.emitJSONFinal(reply)
	return reply
}

// currentMode resolves the Mode selected for this session. Defaults to
// BuildMode if the user hasn't picked one.
func (m *Model) currentMode() agent.Mode {
	if strings.TrimSpace(m.agentModeName) != "" {
		return agent.ModeOrDefault(m.agentModeName)
	}
	return agent.ModeOrDefault(m.cfg.Chat.AgentMode)
}

func (m *Model) buildPromptExecutionPlan(ctx context.Context, prompt string) promptExecutionPlan {
	mode := m.currentMode()
	route := m.classifyTurnRoute(ctx, prompt, mode)
	brief := turnRoutePlanRuntimeBrief(route, mode, m.workspaceAllowedTools())
	brief.TargetRefs = appendUniqueBriefStrings(brief.TargetRefs, pathparse.MentionedWorkspacePaths(prompt)...)
	if plan := defaultVerificationPlan(m.projectRoot()); len(brief.VerificationPlan) == 0 {
		brief.VerificationPlan = plan
	}
	if route.Family == "verification_only" {
		brief.RequiredNext = appendUniqueBriefStrings(brief.RequiredNext,
			"first tool call must run one verification command with shell",
		)
		brief.InvalidActions = appendUniqueBriefStrings(brief.InvalidActions,
			"pwd before verification",
			"ls before verification",
			"find before verification",
		)
		if len(brief.VerificationPlan) > 0 {
			brief.RequiredNext = appendUniqueBriefStrings(brief.RequiredNext,
				"prefer a command from verification_plan before any inspection",
			)
		}
	}
	if isEvidenceFirstMutationFamily(route.Family) && len(brief.TargetRefs) > 0 {
		brief.RequiredNext = appendUniqueBriefStrings(brief.RequiredNext,
			"gather source evidence before large reads of named target refs",
		)
		brief.InvalidActions = appendUniqueBriefStrings(brief.InvalidActions,
			"large read_file on named target refs before source evidence",
		)
	}
	return promptExecutionPlan{
		mode:         mode,
		route:        route,
		runtimeBrief: brief,
	}
}

func defaultVerificationPlan(root string) []string {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}
	type manifestPlan struct {
		path string
		cmd  string
	}
	candidates := []manifestPlan{
		{path: "go.mod", cmd: "go test ./..."},
		{path: "go.work", cmd: "go test ./..."},
		{path: "Cargo.toml", cmd: "cargo test"},
		{path: "package.json", cmd: "npm test"},
		{path: "pyproject.toml", cmd: "pytest"},
		{path: "pytest.ini", cmd: "pytest"},
		{path: "pom.xml", cmd: "mvn test"},
		{path: "build.gradle", cmd: "gradle test"},
		{path: "build.gradle.kts", cmd: "gradle test"},
	}
	var plan []string
	for _, candidate := range candidates {
		if _, err := os.Stat(filepath.Join(root, candidate.path)); err == nil {
			plan = append(plan, candidate.cmd)
		}
	}
	return appendUniqueBriefStrings(nil, plan...)
}

func appendUniqueBriefStrings(dst []string, values ...string) []string {
	seen := make(map[string]struct{}, len(dst)+len(values))
	for _, value := range dst {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		seen[trimmed] = struct{}{}
	}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		dst = append(dst, trimmed)
	}
	return dst
}

func isEvidenceFirstMutationFamily(family string) bool {
	switch strings.TrimSpace(family) {
	case "explore_then_implement", "investigate_then_implement", "review_then_implement", "architecture_then_implement":
		return true
	default:
		return false
	}
}

func tuneChatOptionsForPlan(opts llama.ChatOptions, plan promptExecutionPlan) llama.ChatOptions {
	profile := strings.TrimSpace(plan.runtimeBrief.PermissionProfile)
	contract := strings.TrimSpace(plan.runtimeBrief.Contract)
	readOnly := profile == "conversation_only" || profile == "read_only_workspace" || profile == "read_only_verification_shell"
	if readOnly {
		thinkingOff := false
		opts.EnableThinking = &thinkingOff
		opts.PreserveThinking = false
	}
	switch contract {
	case "direct_answer":
		opts.MaxTokens = clampPositiveMin(opts.MaxTokens, 1024)
	case "grounded_explanation", "locate_targets":
		opts.MaxTokens = clampPositiveMin(opts.MaxTokens, 1536)
	case "review_findings", "architecture_assessment":
		opts.MaxTokens = clampPositiveMin(opts.MaxTokens, 2048)
	}
	return opts
}

func clampPositiveMin(current, limit int) int {
	if limit <= 0 {
		return current
	}
	if current <= 0 || current > limit {
		return limit
	}
	return current
}
