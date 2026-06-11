package agent

import (
	"slices"
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/workerruntime"
)

func readOnlyConversationBriefForTest(objective string) workerruntime.WorkerRuntimeBrief {
	return workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         objective,
	}
}

func typedReadOnlyTaskResult(packet ReadOnlyEvidencePacket) workspace.ToolResult {
	result := workspace.ToolResult{
		Tool:    "task",
		Summary: "Child task completed",
		Output:  "Grounded answer:\nThe child reported a concise answer without legacy packet section headings.",
	}
	return readOnlyEvidencePacketToolResult(result, packet)
}

func TestReadOnlyAnswerabilityContextPacketOnlyLocalizes(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Explain runner answer readiness.")
	results := []workspace.ToolResult{{
		Tool:    "context_pack",
		Summary: "Context pack wrote .idiom/comprehension/views/pack.json",
		Output:  `{"candidate_files":[{"path":"internal/runtime/agent/runner.go"}]}`,
	}}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() {
		t.Fatalf("context_pack alone must not make direct answer ready: %#v", ledger)
	}
	if !ledger.HasKind(readOnlyEvidenceContextPacket) {
		t.Fatalf("expected context packet item, got %#v", ledger.Items)
	}
	if !slices.Contains(ledger.CandidatePaths, "internal/runtime/agent/runner.go") {
		t.Fatalf("expected context candidate path, got %#v", ledger.CandidatePaths)
	}
	if conversationEvidenceSatisfied(brief, results) {
		t.Fatal("direct-answer readiness must not be satisfied by context packet localization only")
	}
}

func TestReadOnlyAnswerabilityHighCardinalitySearchRequiresReturnedCandidateRead(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Explain completion gate behavior.")
	results := []workspace.ToolResult{
		{
			Tool:    "search_content",
			Summary: "Found 178 matching line(s) (showing first 50).",
			Output:  "internal/runtime/agent/runner.go:420: completion gate\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/surfaces/tui/app/model_state.go lines 1-80",
			Output:  "package app\nfunc unrelated() {}\n",
		},
	}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if !ledger.HighCardinalitySearch {
		t.Fatalf("expected high-cardinality search to be recorded: %#v", ledger)
	}
	if ledger.Answerable() {
		t.Fatalf("unrelated source body must not satisfy high-cardinality search evidence: %#v", ledger)
	}
	if conversationEvidenceSatisfied(brief, results) {
		t.Fatal("direct-answer readiness must remain false until a returned candidate is read")
	}

	results[1] = workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read internal/runtime/agent/runner.go lines 400-460",
		Output:  "package agent\nfunc completionGateBehavior() {}\n",
	}
	ledger = readOnlyAnswerabilityForResults(brief, results)
	if !ledger.Answerable() {
		t.Fatalf("source body from returned candidate should make answer ready: %#v", ledger)
	}
	if !slices.Contains(ledger.RelevantSourceBodyPaths, "internal/runtime/agent/runner.go") {
		t.Fatalf("expected candidate source body to be relevant, got %#v", ledger.RelevantSourceBodyPaths)
	}
	if !conversationEvidenceSatisfied(brief, results) {
		t.Fatal("direct-answer readiness should be satisfied by source body from returned candidate")
	}
}

func TestReadOnlyAnswerabilityHighCardinalitySearchRequiresObjectiveRelevantCandidateRead(t *testing.T) {
	brief := readOnlyConversationBriefForTest("What does debug log redacted mode do?")
	results := []workspace.ToolResult{
		{
			Tool:    "search_content",
			Summary: "Found 400 matching line(s) (showing first 50).",
			Output:  "internal/platform/config/config.go:390: redacted\ninternal/evidence/transcript/debuglog.go:18: DebugLogModeRedacted\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/platform/config/config.go lines 390-400",
			Output:  "package config\nconst RedactedConfigValue = true\n",
		},
	}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() {
		t.Fatalf("one weakly related candidate from a high-cardinality search must not satisfy readiness: %#v", ledger)
	}
	closure := readOnlyClosureDecisionForResults(brief, results)
	if blocker := closure.BlockerText(); !slices.Contains(closure.PrioritizedCandidatePaths(), "internal/evidence/transcript/debuglog.go") ||
		!strings.Contains(blocker, "debug log redacted mode") ||
		!strings.Contains(blocker, "internal/evidence/transcript/debuglog.go") {
		t.Fatalf("expected blocker to prioritize objective-relevant candidate, blocker=%q candidates=%#v", blocker, closure.PrioritizedCandidatePaths())
	}

	results[1] = workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read internal/surfaces/tui/app/slash_commands.go lines 120-140",
		Output:  "m.status = \"Debug log mode set to redacted\"\nm.debugLogger.SetMode(transcript.DebugLogMode(fields[2]))\n",
	}
	ledger = readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() {
		t.Fatalf("scattered objective words in a UI/control surface must not satisfy noisy-search readiness: %#v", ledger)
	}

	results[1] = workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read internal/platform/config/config.go lines 390-400",
		Output:  "DebugLogDir: \"logs/conversations\"\nDebugLogMode: \"redacted\"\n",
	}
	ledger = readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() {
		t.Fatalf("noisy-search source body must also be on an objective-relevant path before readiness: %#v", ledger)
	}

	results[1] = workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read internal/evidence/transcript/debuglog.go lines 1-30",
		Output:  "package transcript\nconst DebugLogModeRedacted = \"redacted\"\n",
	}
	ledger = readOnlyAnswerabilityForResults(brief, results)
	if !ledger.Answerable() {
		t.Fatalf("candidate body with objective overlap should satisfy readiness: %#v", ledger)
	}
	if !slices.Contains(ledger.RelevantSourceBodyPaths, "internal/evidence/transcript/debuglog.go") {
		t.Fatalf("expected debuglog source body to be relevant, got %#v", ledger.RelevantSourceBodyPaths)
	}
}

func TestReadOnlyAnswerabilityOutlinesAndGeneratedArtifactsDoNotAnswer(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Explain runner answer readiness.")
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Outlined 12 top-level declaration(s) in internal/runtime/agent/runner.go",
			Output:  "file_outline: internal/runtime/agent/runner.go\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read .idiom/comprehension/views/pack.json lines 1-20",
			Output:  `{"candidate_files":[{"path":"internal/runtime/agent/runner.go"}]}`,
		},
	}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() {
		t.Fatalf("outline plus generated artifact should not be answerable: %#v", ledger)
	}
	if !ledger.HasKind(readOnlyEvidenceOutline) {
		t.Fatalf("expected outline evidence item, got %#v", ledger.Items)
	}
	if !ledger.HasKind(readOnlyEvidenceGeneratedArtifact) {
		t.Fatalf("expected generated artifact evidence item, got %#v", ledger.Items)
	}
}

func TestReadOnlyAnswerabilityContextPackCandidateRequiresObjectiveRelevantSourceBody(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Research the TUI chat input newline rendering bug.")
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/pack.json",
			Output:  `{"candidate_files":[{"path":"internal/surfaces/tui/app/rendering.go"}]}`,
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output: strings.Join([]string{
				"capability: read_symbol",
				"language: go",
				"routed_tool: go_read_symbol",
				"route_summary: Read Model.View in internal/surfaces/tui/app/rendering.go lines 32-70",
				"",
				"32:func (m Model) View() string {",
				"33:    return \"Loading UI...\"",
				"34:}",
			}, "\n"),
		},
	}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() {
		t.Fatalf("context_pack candidate membership alone must not make unrelated source body answerable: %#v", ledger)
	}
	if conversationEvidenceSatisfied(brief, results) {
		t.Fatal("direct-answer readiness must remain false for source body that lacks objective relevance")
	}
	if slices.Contains(ledger.RelevantSourceBodyPaths, "internal/surfaces/tui/app/rendering.go") {
		t.Fatalf("unrelated context candidate source body should not be relevant, got %#v", ledger.RelevantSourceBodyPaths)
	}

	results[1] = workspace.ToolResult{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output: strings.Join([]string{
			"capability: read_symbol",
			"language: go",
			"routed_tool: go_read_symbol",
			"route_summary: Read Model.renderChatInput in internal/surfaces/tui/app/rendering.go lines 120-150",
			"",
			"120:func (m Model) renderChatInput() string {",
			"121:    // chat input newline rendering",
			"122:    return m.textarea.View()",
			"123:}",
		}, "\n"),
	}
	ledger = readOnlyAnswerabilityForResults(brief, results)
	if !ledger.Answerable() {
		t.Fatalf("objective-relevant source body from context candidate should be answerable: %#v", ledger)
	}
}

func TestReadOnlyAnswerabilityPromotesCollectiveSourceBodyObjectiveSupport(t *testing.T) {
	brief := readOnlyConversationBriefForTest("How do up and down arrow keys navigate conversation history?")
	contextOnly := []workspace.ToolResult{{
		Tool:    "context_pack",
		Summary: "Context pack returned weak candidates",
		Output:  `{"candidate_files":[{"path":"internal/surfaces/tui/app/update_handlers.go"},{"path":"internal/surfaces/tui/app/rendering.go"}]}`,
	}}

	ledger := readOnlyAnswerabilityForResults(brief, contextOnly)
	if ledger.Answerable() {
		t.Fatalf("context packet candidates alone must not satisfy answerability: %#v", ledger)
	}

	partialResults := append([]workspace.ToolResult{}, contextOnly...)
	partialResults = append(partialResults,
		workspace.ToolResult{
			Tool:    "read_file",
			Summary: "Read internal/surfaces/tui/app/update_handlers.go lines 80-120",
			Output: strings.Join([]string{
				"80:func (m Model) handleKeyUpdate(msg tea.KeyMsg) (Model, tea.Cmd) {",
				"81:\tswitch msg.String() {",
				"82:\tcase \"up\":",
				"83:\t\tm.navigateHistory(-1)",
				"84:\tcase \"down\":",
				"85:\t\tm.navigateHistory(1)",
				"86:\t}",
				"87:\treturn m, nil",
				"88:}",
			}, "\n"),
		},
		workspace.ToolResult{
			Tool:    "read_file",
			Summary: "Read internal/surfaces/tui/app/rendering.go lines 300-330",
			Output: strings.Join([]string{
				"300:func (m Model) renderViewport() string {",
				"301:\treturn m.viewport.View()",
				"302:}",
			}, "\n"),
		},
	)
	ledger = readOnlyAnswerabilityForResults(brief, partialResults)
	if ledger.Answerable() {
		t.Fatalf("source bodies missing one objective term must not satisfy collective relevance: %#v", ledger)
	}
	if conversationEvidenceSatisfied(brief, partialResults) {
		t.Fatal("conversation readiness must remain false until source bodies collectively cover the objective")
	}

	completeResults := append([]workspace.ToolResult{}, contextOnly...)
	completeResults = append(completeResults, partialResults[1])
	completeResults[1].Output = strings.ReplaceAll(completeResults[1].Output, "m.navigateHistory(-1)", "m.navigateHistory(-1) // arrow-key history")
	completeResults = append(completeResults, workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read internal/surfaces/tui/app/rendering.go lines 300-330",
		Output: strings.Join([]string{
			"300:func (m Model) renderConversationViewport() string {",
			"301:\treturn m.messages.View()",
			"302:}",
		}, "\n"),
	})

	ledger = readOnlyAnswerabilityForResults(brief, completeResults)
	if !ledger.Answerable() {
		t.Fatalf("collective source-body objective coverage should be answerable: %#v", ledger)
	}
	if !slices.Contains(ledger.SourceBodyObjectivePaths, "internal/surfaces/tui/app/update_handlers.go") {
		t.Fatalf("expected substantive collective objective path, got %#v", ledger.SourceBodyObjectivePaths)
	}
	if slices.Contains(ledger.SourceBodyObjectivePaths, "internal/surfaces/tui/app/rendering.go") {
		t.Fatalf("one-term companion source body must not be promoted as objective support, got %#v", ledger.SourceBodyObjectivePaths)
	}
	if !slices.Contains(ledger.RelevantSourceBodyPaths, "internal/surfaces/tui/app/update_handlers.go") {
		t.Fatalf("expected relevant source body path, got %#v", ledger.RelevantSourceBodyPaths)
	}
	if !conversationEvidenceSatisfied(brief, completeResults) {
		t.Fatal("conversation readiness should be satisfied by collective source-body objective coverage")
	}
}

func TestFinalAnswerAcceptanceAllowsCollectiveSourceBodyObjectiveAnswer(t *testing.T) {
	brief := readOnlyConversationBriefForTest("How do up and down arrow keys navigate conversation history?")
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack returned weak candidates",
			Output:  `{"candidate_files":[{"path":"internal/surfaces/tui/app/update_handlers.go"},{"path":"internal/surfaces/tui/app/rendering.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/surfaces/tui/app/update_handlers.go lines 80-120",
			Output: strings.Join([]string{
				"80:func (m Model) handleKeyUpdate(msg tea.KeyMsg) (Model, tea.Cmd) {",
				"81:\tswitch msg.String() {",
				"82:\tcase \"up\":",
				"83:\t\tm.navigateHistory(-1) // arrow-key history",
				"84:\tcase \"down\":",
				"85:\t\tm.navigateHistory(1)",
				"86:\t}",
				"87:\treturn m, nil",
				"88:}",
			}, "\n"),
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/surfaces/tui/app/rendering.go lines 300-330",
			Output: strings.Join([]string{
				"300:func (m Model) renderConversationViewport() string {",
				"301:\treturn m.messages.View()",
				"302:}",
			}, "\n"),
		},
	}
	answer := "Up and down arrow keys navigate conversation history through `internal/surfaces/tui/app/update_handlers.go`, where the key handler calls `navigateHistory`, and `internal/surfaces/tui/app/rendering.go` renders the conversation viewport that displays the resulting message state."
	runner := &Runner{RuntimeBrief: brief}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "" || reason != "" {
		t.Fatalf("collective source-body answer blocked: kind=%q reason=%q", kind, reason)
	}
}

func TestFinalAnswerAcceptanceAllowsWhatWouldItTakeAnswerFromDiscoveredTUISources(t *testing.T) {
	brief := readOnlyConversationBriefForTest("what would it take to make it so that the up and down arrow keys jump between messages from conversation history?")
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote weak packet",
			Output:  `{"candidate_files":[{"path":"internal/runtime/agent/runner.go"},{"path":"internal/surfaces/tui/app/app.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/surfaces/tui/app/update_handlers.go lines 20-90",
			Output: strings.Join([]string{
				"20:func (m *Model) handleKeyPress(msg tea.KeyMsg) (tea.Cmd, bool) {",
				"21:\tswitch msg.Type {",
				"22:\tcase tea.KeyUp:",
				"23:\t\tm.history.selected--",
				"24:\tcase tea.KeyDown:",
				"25:\t\tm.history.selected++",
				"26:\t\tmessage := m.conversationHistory[m.history.selected]",
				"27:\t\tm.input.SetValue(message.Text)",
				"28:\t}",
				"29:}",
			}, "\n"),
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/surfaces/tui/app/rendering.go lines 1-100",
			Output: strings.Join([]string{
				"1:package app",
				"2:func (m *Model) renderMessages(force bool) {",
				"3:\tm.viewport.SetContent(strings.Join(m.messages, \"\\n\"))",
				"4:}",
			}, "\n"),
		},
	}
	answer := "It would take wiring the up/down key cases in `internal/surfaces/tui/app/update_handlers.go` to select conversation-history messages and fill the input with the selected message text instead of treating those keys only as the existing panel navigation behavior."
	runner := &Runner{RuntimeBrief: brief}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "" || reason != "" {
		t.Fatalf("what-would-it-take answer from discovered TUI source bodies blocked: kind=%q reason=%q", kind, reason)
	}
	paths := acceptedAnswerEvidencePaths(answer, results, brief)
	if !slices.Contains(paths, "internal/surfaces/tui/app/update_handlers.go") {
		t.Fatalf("expected accepted evidence path from the strongest source-body support, got %#v", paths)
	}
	if slices.Contains(paths, "internal/surfaces/tui/app/rendering.go") {
		t.Fatalf("accepted evidence paths should not promote one-term companion citation, got %#v", paths)
	}
	if slices.Contains(paths, "internal/runtime/agent/runner.go") {
		t.Fatalf("accepted evidence paths must not retain weak context-pack candidate, got %#v", paths)
	}
}

func TestFinalAnswerAcceptanceRejectsEchoedObjectivePathInventoryFromLiveTUIQuestion(t *testing.T) {
	objective := "What would it take to enable the up and down arrow keys to iterate through old user messages as a shortcut to retry previous messages?"
	brief := readOnlyConversationBriefForTest(objective)
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Output:  `{"candidate_files":[{"path":"internal/surfaces/tui/app/update_handlers.go"},{"path":"internal/surfaces/tui/app/sessions_panel.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/surfaces/tui/app/sessions_panel.go lines 208-248",
			Output: strings.Join([]string{
				"208:func (p *SessionsPanel) HandleKey(msg tea.KeyMsg) {",
				"209:\tswitch msg.Type {",
				"210:\tcase tea.KeyUp:",
				"211:\t\tp.MoveUp()",
				"212:\tcase tea.KeyDown:",
				"213:\t\tp.MoveDown()",
				"214:\t}",
				"215:}",
			}, "\n"),
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/surfaces/tui/app/update_handlers.go lines 22-182",
			Output: strings.Join([]string{
				"22:func (m Model) handleKeyPress(msg tea.KeyMsg) (Model, tea.Cmd) {",
				"23:\tswitch msg.Type {",
				"24:\tcase tea.KeyUp:",
				"25:\t\tm.sessionsPanel.MoveUp()",
				"26:\tcase tea.KeyDown:",
				"27:\t\tm.sessionsPanel.MoveDown()",
				"28:\tcase tea.KeyEnter:",
				"29:\t\tm.retrySelectedConversationMessage()",
				"30:\t}",
				"31:\treturn m, nil",
				"32:}",
			}, "\n"),
		},
	}
	answer := "For `" + objective + "`, `internal/surfaces/tui/app/update_handlers.go` shows defines `Model.handleKeyPress`."
	runner := &Runner{RuntimeBrief: brief}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "answer_objective_unmet" {
		t.Fatalf("echoed-objective path inventory got (%q, %q), want answer_objective_unmet", kind, reason)
	}
	acceptance := runner.evaluateFinalAnswerAcceptance(answer, results)
	if len(acceptance.AnswerObjectiveTerms) > 0 {
		t.Fatalf("echoed objective must not satisfy answer objective terms, got %#v", acceptance.AnswerObjectiveTerms)
	}
}

func TestFinalAnswerAcceptanceAllowsExplainerAnswerWithInspectedSymbol(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Explain the harness control flow.")
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/runtime/agent/runner.go lines 1-20",
		Output: strings.Join([]string{
			"1:package agent",
			"2:func (r *Runner) Run() {}",
		}, "\n"),
	}}
	answer := "`Runner.Run` is visible in `internal/runtime/agent/runner.go`; the inspected source anchors the harness control flow in that file."
	runner := &Runner{RuntimeBrief: brief}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "" || reason != "" {
		t.Fatalf("explainer answer with inspected symbol blocked: kind=%q reason=%q", kind, reason)
	}

	answer = "For `Explain the harness control flow.`, `internal/runtime/agent/runner.go` shows defines `Runner.Run`."
	kind, reason = runner.finalAnswerBlockerKind(answer, results)
	if kind != "" || reason != "" {
		t.Fatalf("deterministic explainer symbol answer blocked: kind=%q reason=%q", kind, reason)
	}
}

func TestReadOnlyAnswerabilityDoesNotPromoteUnrelatedCollectiveSourceBodies(t *testing.T) {
	brief := readOnlyConversationBriefForTest("How do up and down arrow keys navigate conversation history?")
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack returned a real candidate unrelated to later reads",
			Output:  `{"candidate_files":[{"path":"internal/surfaces/tui/app/update_handlers.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/platform/config/config.go lines 1-8",
			Output: strings.Join([]string{
				"1:package config",
				"2:// down key history words appear in an unrelated config comment",
				"3:const ConfigValue = true",
			}, "\n"),
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/capabilities/workspace/manager.go lines 1-8",
			Output: strings.Join([]string{
				"1:package workspace",
				"2:// conversation message words appear in unrelated workspace plumbing",
				"3:type Manager struct{}",
			}, "\n"),
		},
	}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() {
		t.Fatalf("unrelated source bodies must not become answerable through scattered objective words: %#v", ledger)
	}
	if len(ledger.SourceBodyObjectivePaths) > 0 {
		t.Fatalf("unrelated source bodies must not be promoted to objective paths: %#v", ledger.SourceBodyObjectivePaths)
	}
}

func TestObjectiveAsksImplementationShapeDoesNotCatchPlainHowToUse(t *testing.T) {
	if objectiveAsksImplementationShape("How to run the unit tests in this repository?") {
		t.Fatal("plain how-to usage question must not require implementation-owner readiness")
	}
	if !objectiveAsksImplementationShape("What would it take to change the runner readiness behavior?") {
		t.Fatal("change-shape question should require implementation-owner readiness")
	}
}

func TestReadOnlyAnswerabilityDoesNotTreatSessionsPanelAsChatRecallOwner(t *testing.T) {
	objective := "what would it take to enable the up and down arrow keys to iterate through the user's chat history? the intent is to make it easy to find old messages and run them again without typing them out."
	brief := readOnlyConversationBriefForTest(objective)

	sessionsResults := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack returned candidate internal/surfaces/tui/app/sessions_panel.go",
			Output:  `{"candidate_files":[{"path":"internal/surfaces/tui/app/sessions_panel.go"}]}`,
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output: strings.Join([]string{
				"route_summary: Read Model.handleSessionsPanelKey in internal/surfaces/tui/app/sessions_panel.go lines 205-245",
				"205:// handleSessionsPanelKey consumes a key press while the Sessions tab",
				"206:// has focus. Returns (true, cmd) if handled. j/k navigate, Enter",
				"207:// resumes the selected session.",
				"208:func (m *Model) handleSessionsPanelKey(msg tea.KeyMsg) (bool, tea.Cmd) {",
				"211:\tcase tea.KeyUp:",
				"215:\tcase tea.KeyDown:",
				"230:\t\tif err := m.ResumeSession(id); err != nil {",
				"236:\t\t\t// Bounce the panel back to History so the resumed chat",
			}, "\n"),
		},
	}
	if ledger := readOnlyAnswerabilityForResults(brief, sessionsResults); ledger.Answerable() {
		t.Fatalf("sessions-panel source owner must not close chat-message recall readiness: %#v", ledger)
	} else if role := sourceOwnerRoleForTest(ledger, "internal/surfaces/tui/app/sessions_panel.go"); role != readOnlySourceOwnerConflictingNavigation {
		t.Fatalf("sessions-panel source should be conflicting navigation owner, got %q in %#v", role, ledger.SourceOwnerEvidence)
	}

	overlayResults := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack returned candidate internal/surfaces/tui/app/help_overlay.go",
			Output:  `{"candidate_files":[{"path":"internal/surfaces/tui/app/help_overlay.go"}]}`,
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output: strings.Join([]string{
				"route_summary: Read Model.handleHelpOverlayKey in internal/surfaces/tui/app/help_overlay.go lines 42-88",
				"42:// handleHelpOverlayKey consumes a key press while the Help overlay has focus.",
				"43:func (m *Model) handleHelpOverlayKey(msg tea.KeyMsg) (bool, tea.Cmd) {",
				"44:\tswitch msg.Type {",
				"45:\tcase tea.KeyUp:",
				"46:\t\tm.helpOverlay.MoveUp()",
				"47:\tcase tea.KeyDown:",
				"48:\t\tm.helpOverlay.MoveDown()",
				"49:\t}",
				"50:\treturn true, nil",
			}, "\n"),
		},
	}
	if ledger := readOnlyAnswerabilityForResults(brief, overlayResults); ledger.Answerable() {
		t.Fatalf("unrelated overlay navigation owner must not close chat-message recall readiness: %#v", ledger)
	} else if role := sourceOwnerRoleForTest(ledger, "internal/surfaces/tui/app/help_overlay.go"); role != readOnlySourceOwnerConflictingNavigation {
		t.Fatalf("overlay source should be conflicting navigation owner, got %q in %#v", role, ledger.SourceOwnerEvidence)
	}

	stateOnlyResults := []workspace.ToolResult{
		sessionsResults[0],
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output: strings.Join([]string{
				"route_summary: Read Model in internal/surfaces/tui/app/model_state.go lines 100-150",
				"100:type Model struct {",
				"122:\tmessages []llama.ChatMessage",
				"130:\thistory historyState",
				"131:\tinputState inputState",
				"150:}",
			}, "\n"),
		},
	}
	if ledger := readOnlyAnswerabilityForResults(brief, stateOnlyResults); ledger.Answerable() {
		t.Fatalf("state-only source body must not close implementation-shape readiness: %#v", ledger)
	} else if role := sourceOwnerRoleForTest(ledger, "internal/surfaces/tui/app/model_state.go"); role != readOnlySourceOwnerStateHolder {
		t.Fatalf("model state source should be state support, got %q in %#v", role, ledger.SourceOwnerEvidence)
	}

	liveShapedStateResults := []workspace.ToolResult{
		sessionsResults[0],
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output: strings.Join([]string{
				"route_summary: Read Model in internal/surfaces/tui/app/model_state.go lines 133-263",
				"133:type Model struct {",
				"162:\tinput    textarea.Model",
				"169:\tmessages []llama.ChatMessage",
				"249:\tcurrentTurnHistoryIndex  int",
				"250:\tcurrentTurnHistoryActive bool",
				"256:\tplan     planState",
				"257:\tsessions sessionsState",
				"258:\thistory  historyState",
				"259:}",
			}, "\n"),
		},
	}
	if ledger := readOnlyAnswerabilityForResults(brief, liveShapedStateResults); ledger.Answerable() {
		t.Fatalf("live-shaped Model state body must not close implementation-shape readiness: %#v", ledger)
	} else if role := sourceOwnerRoleForTest(ledger, "internal/surfaces/tui/app/model_state.go"); role != readOnlySourceOwnerStateHolder {
		t.Fatalf("live-shaped Model body should be state support, got %q in %#v", role, ledger.SourceOwnerEvidence)
	}

	sessionsPlusStateResults := append([]workspace.ToolResult{}, sessionsResults...)
	sessionsPlusStateResults = append(sessionsPlusStateResults, stateOnlyResults[1])
	if ledger := readOnlyAnswerabilityForResults(brief, sessionsPlusStateResults); ledger.Answerable() {
		t.Fatalf("sessions navigation plus state support must not close without a mechanism owner: %#v", ledger)
	}

	handlerResults := []workspace.ToolResult{
		sessionsResults[0],
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output: strings.Join([]string{
				"route_summary: Read Model.handleKeyPress in internal/surfaces/tui/app/update_handlers.go lines 22-180",
				"22:func (m *Model) handleKeyPress(msg tea.KeyMsg) (tea.Cmd, bool) {",
				"43:\t\tcase tea.KeyUp:",
				"49:\t\tcase tea.KeyDown:",
				"70:\t\t\tif m.history.selected > 0 {",
				"77:\t\t\tif m.history.selected < len(m.history.entries)-1 {",
				"172:\t\tm.appendHistoryMessage(\"user\", text, nil)",
			}, "\n"),
		},
	}
	if ledger := readOnlyAnswerabilityForResults(brief, handlerResults); !ledger.Answerable() {
		t.Fatalf("chat/history key-handler source body should close readiness: %#v", ledger)
	} else if role := sourceOwnerRoleForTest(ledger, "internal/surfaces/tui/app/update_handlers.go"); role != readOnlySourceOwnerMechanism {
		t.Fatalf("chat/history key handler should be mechanism owner, got %q in %#v", role, ledger.SourceOwnerEvidence)
	}
}

func TestReadOnlyAnswerabilityDoesNotTreatRegressionFixtureAsImplementationOwner(t *testing.T) {
	objective := "what would it take to enable the up and down arrows to iterate between and fill the chat input with a user's chat history entries?"
	brief := readOnlyConversationBriefForTest(objective)
	results := []workspace.ToolResult{
		{
			Tool:    "search_content",
			Summary: "Found 38 matching line(s).",
			Output: strings.Join([]string{
				"internal/runtime/agent/final_answer_decorator_test.go:1237:func TestSynthesizeBriefConversationFallbackPrefersObjectiveRelevantExactProbe(t *testing.T) {",
				"internal/runtime/agent/final_answer_decorator_test.go:1242:objective := \"what would it take to enable the up and down arrow keys to iterate through the user's chat history?\"",
			}, "\n"),
		},
		{
			Tool:    "read_file",
			Summary: "Read f2 internal/runtime/agent/final_answer_decorator_test.go lines 1230-1270 (total lines: 1544)",
			Output: strings.Join([]string{
				"1237:func TestSynthesizeBriefConversationFallbackPrefersObjectiveRelevantExactProbe(t *testing.T) {",
				"1242:\tobjective := \"what would it take to enable the up and down arrow keys to iterate through the user's chat history?\"",
				"1249:\t\t\"route_summary: Read Model.handleKeyPress in internal/surfaces/tui/app/update_handlers.go lines 22-180\",",
				"1250:\t\t\"22:func (m *Model) handleKeyPress(msg tea.KeyMsg) (tea.Cmd, bool) {\",",
				"1251:\t\t\"43:\\t\\tcase tea.KeyUp:\",",
				"1252:\t\t\"49:\\t\\tcase tea.KeyDown:\",",
				"1253:\t\t\"172:\\t\\tm.appendHistoryMessage(\\\"user\\\", text, nil)\",",
			}, "\n"),
		},
	}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() {
		t.Fatalf("test fixture body must not satisfy implementation-owner readiness: %#v", ledger)
	}
	if got := ledger.ImplementationOwnerPaths(); len(got) > 0 {
		t.Fatalf("test fixture must not become implementation owner path, got %#v", got)
	}
	if role := sourceOwnerRoleForTest(ledger, "internal/runtime/agent/final_answer_decorator_test.go"); role != readOnlySourceOwnerTestSupport {
		t.Fatalf("test fixture should be classified as test support, got %q in %#v", role, ledger.SourceOwnerEvidence)
	}
}

func sourceOwnerRoleForTest(ledger readOnlyAnswerabilityLedger, path string) readOnlySourceOwnerRole {
	path = normalizeRunnerPath(path)
	for _, evidence := range ledger.SourceOwnerEvidence {
		if evidence.Path == path {
			return evidence.Role
		}
	}
	return readOnlySourceOwnerUnknown
}

func TestFinalAnswerAcceptanceRejectsSessionsPanelAsChatRecallOwner(t *testing.T) {
	objective := "what would it take to enable the up and down arrow keys to iterate through the user's chat history? the intent is to make it easy to find old messages and run them again without typing them out."
	brief := readOnlyConversationBriefForTest(objective)
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack returned candidate internal/surfaces/tui/app/sessions_panel.go",
			Output:  `{"candidate_files":[{"path":"internal/surfaces/tui/app/sessions_panel.go"},{"path":"internal/surfaces/tui/app/model_state.go"}]}`,
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output: strings.Join([]string{
				"route_summary: Read Model.handleSessionsPanelKey in internal/surfaces/tui/app/sessions_panel.go lines 205-245",
				"205:// handleSessionsPanelKey consumes a key press while the Sessions tab",
				"206:// has focus. Returns (true, cmd) if handled. j/k navigate, Enter",
				"207:// resumes the selected session.",
				"208:func (m *Model) handleSessionsPanelKey(msg tea.KeyMsg) (bool, tea.Cmd) {",
				"211:\tcase tea.KeyUp:",
				"215:\tcase tea.KeyDown:",
				"230:\t\tif err := m.ResumeSession(id); err != nil {",
				"236:\t\t\t// Bounce the panel back to History so the resumed chat",
			}, "\n"),
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output: strings.Join([]string{
				"route_summary: Read Model in internal/surfaces/tui/app/model_state.go lines 100-150",
				"100:type Model struct {",
				"122:\tmessages []llama.ChatMessage",
				"130:\thistory historyState",
				"131:\tinputState inputState",
				"150:}",
			}, "\n"),
		},
	}
	answer := "It would take changing `Model.handleSessionsPanelKey` in `internal/surfaces/tui/app/sessions_panel.go`. The inspected source shows it already handles `tea.KeyUp` and `tea.KeyDown`, so the work is to route those cases through chat-history state."
	runner := &Runner{RuntimeBrief: brief}

	decision := readOnlyClosureDecisionForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results).
		FinalAnswerDecision(answer)
	if decision.BlockerKind != "answer_objective_unmet" {
		t.Fatalf("shared final-answer decision got blocker kind=%q", decision.BlockerKind)
	}
	if slices.Contains(decision.AcceptedEvidencePaths, "internal/surfaces/tui/app/sessions_panel.go") {
		t.Fatalf("shared final-answer decision promoted conflicting navigation owner, got %#v", decision.AcceptedEvidencePaths)
	}
	aliasBrief := brief
	aliasBrief.TaskFamily = string(workerruntime.TaskFamilyDirectAnswer)
	aliasDecision := readOnlyClosureDecisionForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(aliasBrief), results).
		FinalAnswerDecision(answer)
	if aliasDecision.BlockerKind != "answer_objective_unmet" {
		t.Fatalf("direct_answer alias shared decision got blocker kind=%q", aliasDecision.BlockerKind)
	}
	if slices.Contains(aliasDecision.AcceptedEvidencePaths, "internal/surfaces/tui/app/sessions_panel.go") {
		t.Fatalf("direct_answer alias promoted conflicting navigation owner, got %#v", aliasDecision.AcceptedEvidencePaths)
	}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "answer_objective_unmet" {
		t.Fatalf("sessions-panel-owned final answer got blocker kind=%q reason=%q", kind, reason)
	}
	if paths := acceptedAnswerEvidencePaths(answer, results, brief); slices.Contains(paths, "internal/surfaces/tui/app/sessions_panel.go") {
		t.Fatalf("accepted evidence paths must not promote conflicting navigation owner, got %#v", paths)
	}
	aliasRunner := &Runner{RuntimeBrief: aliasBrief}
	if kind, reason := aliasRunner.finalAnswerBlockerKind(answer, results); kind != "answer_objective_unmet" {
		t.Fatalf("direct_answer alias final answer got blocker kind=%q reason=%q", kind, reason)
	}
	if paths := acceptedAnswerEvidencePaths(answer, results, aliasBrief); slices.Contains(paths, "internal/surfaces/tui/app/sessions_panel.go") {
		t.Fatalf("direct_answer alias accepted evidence paths promoted conflicting navigation owner, got %#v", paths)
	}
}

func TestReadOnlyAnswerabilityAcceptsDelegatedInspectedPathPacket(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Can you tell me how this harness uses subagents?")
	results := []workspace.ToolResult{{
		Tool:    "task",
		Summary: "Child task completed",
		Output: strings.Join([]string{
			"Question: Can you tell me how this harness uses subagents?",
			"Source: delegated read-only evidence packet.",
			"",
			"Inspected paths:",
			"- `internal/runtime/agent/runner.go`",
			"- `internal/runtime/agent/runner_child_tasks.go`",
			"",
			"Key findings:",
			"- The harness uses the `task` tool in internal/runtime/agent/runner.go to launch a child runner implemented by internal/runtime/agent/runner_child_tasks.go.",
		}, "\n"),
	}}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if !ledger.Answerable() {
		t.Fatalf("grounded delegated inspected-path packet should be answerable: %#v", ledger)
	}
	for _, want := range []string{"internal/runtime/agent/runner.go", "internal/runtime/agent/runner_child_tasks.go"} {
		if !slices.Contains(ledger.RelevantSourceBodyPaths, want) {
			t.Fatalf("expected delegated packet path %q to be relevant, got %#v", want, ledger.RelevantSourceBodyPaths)
		}
	}
	answer, ok := synthesizeDelegatedReadOnlyAnswer(brief.Objective, results)
	if !ok {
		t.Fatal("expected deterministic synthesis from grounded delegated packet")
	}
	for _, want := range []string{"task", "child runner", "`internal/runtime/agent/runner.go`", "`internal/runtime/agent/runner_child_tasks.go`"} {
		if !strings.Contains(strings.ToLower(answer), strings.ToLower(want)) {
			t.Fatalf("expected synthesized answer to contain %q, got:\n%s", want, answer)
		}
	}
}

func TestPostChildSourceBodyPacketEvidenceAvailableAcceptsDelegatedNegativeFinding(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Review performance risks in this workspace.")
	results := []workspace.ToolResult{{
		Tool:    "task",
		Summary: "Child task completed",
		Output: strings.Join([]string{
			"Source-body evidence paths:",
			"- `internal/runtime/agent/runner.go`",
			"",
			"Inspected paths: internal/runtime/agent/runner.go. No performance issue found.",
		}, "\n"),
	}}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() {
		t.Fatalf("source-body packet availability alone should not satisfy answerability: %#v", ledger)
	}
	if !postChildSourceBodyPacketEvidenceAvailable(results) {
		t.Fatal("delegated source-body packet with a bounded negative finding should trigger parent model synthesis")
	}
}

func TestReadOnlyAnswerabilityAcceptsTypedDelegatedEvidencePacket(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Can you tell me how this harness uses subagents?")
	results := []workspace.ToolResult{typedReadOnlyTaskResult(ReadOnlyEvidencePacket{
		Objective:       brief.Objective,
		Question:        brief.Objective,
		SourceBodyPaths: []string{"internal/runtime/agent/runner.go", "internal/runtime/agent/runner_child_tasks.go"},
		InspectedPaths:  []string{"internal/runtime/agent/runner.go", "internal/runtime/agent/runner_child_tasks.go", ".idiom/discovery/queries/query.json"},
		CandidatePaths:  []string{".idiom/discovery/queries/query.json"},
		Confidence:      "high",
		Findings: []ReadOnlyEvidenceFinding{
			{Path: "internal/runtime/agent/runner.go", Text: "The harness uses the `task` tool as the subagent entrypoint for read-only exploration."},
			{Path: "internal/runtime/agent/runner_child_tasks.go", Text: "`runChildTask` builds the bounded subagent child runner and returns its packet to the parent."},
		},
	})}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if !ledger.Answerable() {
		t.Fatalf("typed delegated source-body packet should be answerable: %#v", ledger)
	}
	for _, want := range []string{"internal/runtime/agent/runner.go", "internal/runtime/agent/runner_child_tasks.go"} {
		if !slices.Contains(ledger.RelevantSourceBodyPaths, want) {
			t.Fatalf("expected typed packet path %q to be relevant, got %#v", want, ledger.RelevantSourceBodyPaths)
		}
	}
	if slices.Contains(ledger.CandidatePaths, ".idiom/discovery/queries/query.json") ||
		slices.Contains(ledger.SourceBodyPaths, ".idiom/discovery/queries/query.json") {
		t.Fatalf("generated artifacts must not survive packet normalization: %#v", ledger)
	}
	answer, ok := synthesizeDelegatedReadOnlyAnswer(brief.Objective, results)
	if !ok {
		t.Fatal("expected deterministic synthesis from typed delegated packet")
	}
	for _, want := range []string{"task", "runChildTask", "`internal/runtime/agent/runner.go`", "`internal/runtime/agent/runner_child_tasks.go`"} {
		if !strings.Contains(strings.ToLower(answer), strings.ToLower(want)) {
			t.Fatalf("expected synthesized answer to contain %q, got:\n%s", want, answer)
		}
	}
	for _, forbidden := range []string{"Inspected paths:", "Source-body evidence paths:", "Key findings:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("typed synthesis should not depend on legacy heading %q:\n%s", forbidden, answer)
		}
	}
}

func TestReadOnlyAnswerabilityRejectsOffObjectiveTypedDelegatedEvidencePacket(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Can you tell me how this harness uses subagents?")
	results := []workspace.ToolResult{typedReadOnlyTaskResult(ReadOnlyEvidencePacket{
		Objective:       brief.Objective,
		Question:        brief.Objective,
		SourceBodyPaths: []string{"internal/runtime/agent/runner.go", "internal/repository/comprehension/result_metadata.go"},
		InspectedPaths:  []string{"internal/runtime/agent/runner.go", "internal/repository/comprehension/result_metadata.go"},
		Confidence:      "high",
		Findings: []ReadOnlyEvidenceFinding{
			{Path: "internal/runtime/agent/runner.go", Text: "The runner reads ChatMessage values and records model-call metadata."},
			{Path: "internal/repository/comprehension/result_metadata.go", Text: "Result metadata stores provenance fields for context packets."},
		},
	})}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() {
		t.Fatalf("off-objective typed delegated packet must not satisfy answerability: %#v", ledger)
	}
	if len(ledger.SourceBodyPaths) > 0 || len(ledger.SourceBodyTaskPaths) > 0 || len(ledger.RelevantSourceBodyPaths) > 0 {
		t.Fatalf("off-objective typed packet paths must not be promoted to source-body readiness: %#v", ledger)
	}
	if answer, ok := synthesizeDelegatedReadOnlyAnswer(brief.Objective, results); ok {
		t.Fatalf("off-objective typed packet should not synthesize an answer, got:\n%s", answer)
	}
}

func TestReadOnlyAnswerabilityAcceptsTypedImplementationOwnerPacketWithoutAgentPath(t *testing.T) {
	objective := "what would it take to enable the up and down arrow keys to iterate through the user's chat history? the intent is to make it easy to find old messages and run them again without typing them out."
	brief := readOnlyConversationBriefForTest(objective)
	results := []workspace.ToolResult{typedReadOnlyTaskResult(ReadOnlyEvidencePacket{
		Objective:       brief.Objective,
		Question:        brief.Objective,
		SourceBodyPaths: []string{"internal/surfaces/tui/app/update_handlers.go"},
		InspectedPaths:  []string{"internal/surfaces/tui/app/update_handlers.go"},
		Confidence:      "high",
		Findings: []ReadOnlyEvidenceFinding{
			{
				Path: "internal/surfaces/tui/app/update_handlers.go",
				Text: "Model.handleKeyPress handles tea.KeyUp and tea.KeyDown, reads chat history entries, and appends the selected user message into the input flow.",
			},
		},
	})}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if !ledger.Answerable() {
		t.Fatalf("typed implementation-owner packet should satisfy direct-answer readiness without an internal/runtime/agent path: %#v", ledger)
	}
	if got := ledger.ImplementationOwnerPaths(); !slices.Contains(got, "internal/surfaces/tui/app/update_handlers.go") {
		t.Fatalf("expected typed packet source owner path, got %#v", got)
	}
	if role := sourceOwnerRoleForTest(ledger, "internal/surfaces/tui/app/update_handlers.go"); role != readOnlySourceOwnerMechanism {
		t.Fatalf("typed packet source should be mechanism owner, got %q in %#v", role, ledger.SourceOwnerEvidence)
	}
}

func TestReadOnlyAnswerabilityRejectsGenericToolSchemaPacketForUsefulnessObjective(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "architecture",
		Contract:          "architecture_assessment",
		Objective:         "What can we add to our discovery tools in an effort at increasing their overall usefulness?",
		PermissionProfile: "read_only_workspace",
	}
	results := []workspace.ToolResult{typedReadOnlyTaskResult(ReadOnlyEvidencePacket{
		Objective:       brief.Objective,
		Question:        brief.Objective,
		SourceBodyPaths: []string{"internal/capabilities/workspace/tool_schema_export.go", "internal/capabilities/workspace/tool_schemas.go"},
		InspectedPaths:  []string{"internal/capabilities/workspace/tool_schema_export.go", "internal/capabilities/workspace/tool_schemas.go"},
		Confidence:      "high",
		Findings: []ReadOnlyEvidenceFinding{
			{Path: "internal/capabilities/workspace/tool_schema_export.go", Text: "Tool schema export serializes available discovery tool schemas as JSON."},
			{Path: "internal/capabilities/workspace/tool_schemas.go", Text: "The workspace tool schema table returns argument metadata for registered tools."},
		},
	})}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() {
		t.Fatalf("generic tool/schema packet must not satisfy usefulness answerability: %#v", ledger)
	}
	if postChildReadOnlyPacketAvailable(brief, results) {
		t.Fatal("generic tool/schema packet must not activate child packet closure")
	}
	if paths := postChildClosureEvidencePaths(brief.Objective, results, 4); len(paths) > 0 {
		t.Fatalf("generic tool/schema packet must not populate post-child evidence paths, got %#v", paths)
	}
	if paths := acceptedAnswerEvidencePaths("Discovery tool schema details are available.", results, brief); len(paths) > 0 {
		t.Fatalf("generic tool/schema packet must not leak accepted evidence paths, got %#v", paths)
	}
}

func TestReadOnlyAnswerabilityAcceptsObjectiveRelevantLegacyChildPacket(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "architecture",
		Contract:          "architecture_assessment",
		Objective:         "Explain how delegated child runners are created in this harness.",
		PermissionProfile: "read_only_workspace",
	}
	results := []workspace.ToolResult{{
		Tool:    "task",
		Summary: "Child task completed",
		Output: strings.Join([]string{
			"Partial findings from this turn (the budget was reached before a full synthesis):",
			"",
			"Verified context:",
			"- Read f23 internal/runtime/agent/runner_child_tasks.go lines 1-20",
			"- Found task child runner creation in internal/runtime/agent/runner_child_tasks.go",
		}, "\n"),
	}}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if !ledger.Answerable() {
		t.Fatalf("objective-relevant legacy child packet should satisfy read-only answerability: %#v", ledger)
	}
}

func TestReadOnlyAnswerabilityRejectsDelegatedArtifactOnlyPacket(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Explain runner answer readiness.")
	results := []workspace.ToolResult{{
		Tool:    "task",
		Summary: "Child task completed",
		Output: strings.Join([]string{
			"Inspected paths:",
			"- `.idiom/discovery/queries/query.json`",
			"",
			"Key findings:",
			"- The context cache was inspected.",
		}, "\n"),
	}}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() {
		t.Fatalf("artifact-only delegated packet must not satisfy answerability: %#v", ledger)
	}
}

func TestReadOnlyAnswerabilityRejectsTypedDelegatedArtifactOnlyPacket(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Explain runner answer readiness.")
	result := typedReadOnlyTaskResult(ReadOnlyEvidencePacket{
		Objective:      brief.Objective,
		InspectedPaths: []string{".idiom/discovery/queries/query.json"},
		CandidatePaths: []string{".idiom/discovery/queries/query.json"},
		Confidence:     "medium",
		Findings:       []ReadOnlyEvidenceFinding{{Text: "The context cache was inspected."}},
	})
	results := []workspace.ToolResult{result}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() {
		t.Fatalf("artifact-only typed packet must not satisfy answerability: %#v", ledger)
	}
	if postChildReadOnlyPacketAvailable(brief, results) {
		t.Fatalf("artifact-only typed packet must not activate child packet closure")
	}
	if answer, ok := synthesizeDelegatedReadOnlyAnswer(brief.Objective, results); ok {
		t.Fatalf("artifact-only typed packet should not synthesize an answer, got:\n%s", answer)
	}
}

func TestReadOnlyAnswerabilityAcceptsExplicitDiagnosticEvidence(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Explain why diagnostics fail.")
	results := []workspace.ToolResult{{
		Tool:    "source_sanity",
		Summary: "source_sanity failed for internal/runtime/agent/runner.go",
		Output:  "internal/runtime/agent/runner.go:10: undefined: Foo\n",
	}}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if !ledger.Answerable() {
		t.Fatalf("explicit diagnostic evidence should make read-only explanation answerable: %#v", ledger)
	}
	if !ledger.DiagnosticEvidence {
		t.Fatalf("expected diagnostic evidence flag: %#v", ledger)
	}
	if !slices.Contains(ledger.DiagnosticPaths, "internal/runtime/agent/runner.go") {
		t.Fatalf("expected diagnostic source path, got %#v", ledger.DiagnosticPaths)
	}
	answer := "Diagnostics fail in `internal/runtime/agent/runner.go` because source_sanity reports `undefined: Foo`."
	decision := readOnlyClosureDecisionForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results).
		FinalAnswerDecision(answer)
	if decision.BlockerKind != "" {
		t.Fatalf("diagnostic final-answer decision rejected relevant evidence: %#v", decision)
	}
	if !slices.Contains(decision.AcceptedEvidencePaths, "internal/runtime/agent/runner.go") {
		t.Fatalf("diagnostic final-answer decision missed accepted diagnostic path: %#v", decision.AcceptedEvidencePaths)
	}
	if paths := acceptedAnswerEvidencePaths(answer, results, brief); !slices.Contains(paths, "internal/runtime/agent/runner.go") {
		t.Fatalf("accepted evidence paths missed relevant diagnostic path: %#v", paths)
	}
}

func TestReadOnlyAnswerabilityRejectsUnrelatedDiagnosticEvidence(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Why did the runner build fail?")
	results := []workspace.ToolResult{{
		Tool:    "source_sanity",
		Summary: "source_sanity failed for internal/platform/config/config.go",
		Output:  "internal/platform/config/config.go:10: undefined: ConfigValue\n",
	}}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() || ledger.DiagnosticRelevant {
		t.Fatalf("unrelated diagnostic evidence must not satisfy broad failure objectives: %#v", ledger)
	}
	if !ledger.DiagnosticEvidence {
		t.Fatalf("diagnostic evidence should still be recorded as weak evidence: %#v", ledger)
	}
	if !slices.Contains(ledger.DiagnosticPaths, "internal/platform/config/config.go") {
		t.Fatalf("expected diagnostic source path to be recorded, got %#v", ledger.DiagnosticPaths)
	}
	answer := "The runner build failed because `internal/platform/config/config.go` has `undefined: ConfigValue`."
	decision := readOnlyClosureDecisionForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results).
		FinalAnswerDecision(answer)
	if len(decision.AcceptedEvidencePaths) > 0 {
		t.Fatalf("unrelated diagnostic evidence must not become accepted evidence: %#v", decision.AcceptedEvidencePaths)
	}
	if paths := acceptedAnswerEvidencePaths(answer, results, brief); len(paths) > 0 {
		t.Fatalf("accepted evidence paths promoted unrelated diagnostic evidence: %#v", paths)
	}
}

func TestReadOnlyAnswerabilityAcceptsPathMatchedDiagnosticEvidence(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Why did diagnostics fail for internal/runtime/agent/runner.go?")
	results := []workspace.ToolResult{{
		Tool:    "source_sanity",
		Summary: "source_sanity failed for internal/runtime/agent/runner.go",
		Output:  "internal/runtime/agent/runner.go:10: undefined: RunnerState\n",
	}}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if !ledger.Answerable() || !ledger.DiagnosticRelevant {
		t.Fatalf("path-matched diagnostic evidence should satisfy diagnostic objective: %#v", ledger)
	}
}

func TestReadOnlyAnswerabilityRejectsWrongPathDiagnosticEvidence(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Why did diagnostics fail for internal/runtime/agent/runner.go?")
	results := []workspace.ToolResult{{
		Tool:    "source_sanity",
		Summary: "source_sanity failed for internal/platform/config/config.go",
		Output:  "internal/platform/config/config.go:10: undefined: ConfigValue\n",
	}}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() || ledger.DiagnosticRelevant {
		t.Fatalf("diagnostic evidence for the wrong named path must not satisfy answerability: %#v", ledger)
	}
}

func TestReadOnlyAnswerabilityRejectsWrongPathDiagnosticEvidenceEvenWhenTextEchoesObjective(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Why did diagnostics fail for internal/runtime/agent/runner.go?")
	results := []workspace.ToolResult{{
		Tool:    "source_sanity",
		Summary: "source_sanity failed for internal/platform/config/config.go while investigating diagnostics for internal/runtime/agent/runner.go",
		Output:  "internal/platform/config/config.go:10: undefined: ConfigValue\ninternal/runtime/agent/runner.go remains the requested diagnostic target.\n",
	}}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() || ledger.DiagnosticRelevant {
		t.Fatalf("wrong-path diagnostic text that echoes the named target must not satisfy answerability: %#v", ledger)
	}
}

func TestReadOnlyAnswerabilityDoesNotTreatCompleteDiagnosticLogAsSourceEvidence(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Why did the runner build fail?")
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f7 logs/conversations/20260603-173738.903.jsonl lines 1-72 (file has 72 lines total; defaulted to recent log tail; pass start_line/end_line for a different window)",
		Output:  "1:{\"type\":\"session_start\"}\n72:{\"type\":\"final\"}\n",
	}}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() || ledger.DiagnosticRelevant {
		t.Fatalf("complete diagnostic logs must not satisfy source-code failure questions without a typed diagnostic evidence contract: %#v", ledger)
	}
	if len(ledger.SourceBodyPaths) > 0 {
		t.Fatalf("diagnostic logs must not become source-body paths: %#v", ledger.SourceBodyPaths)
	}
}

func TestReadOnlyAnswerabilityRejectsRetainedContextFinalWithoutSourceBody(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Explain runner answer readiness.")
	results := []workspace.ToolResult{{
		Tool:    "context_pack",
		Summary: "Context pack wrote .idiom/comprehension/views/pack.json",
		Output:  `{"candidate_files":[{"path":"internal/runtime/agent/runner.go"}]}`,
	}}
	answer := "The retained source context identifies the relevant implementation surface as `internal/runtime/agent/runner.go`."
	runner := &Runner{RuntimeBrief: brief}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "source_grounding_unmet" {
		t.Fatalf("retained-context final should be rejected, got (%q, %q)", kind, reason)
	}
	if reason == "" {
		t.Fatal("expected rejection reason")
	}
}

func TestReadOnlyAnswerabilityRejectsScaffoldFinalEvenWithRelevantSourceBody(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Explain runner answer readiness.")
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/runtime/agent/runner.go lines 4128-4285",
		Output:  "package agent\nfunc runnerReadinessState() {}\n",
	}}
	answer := "The strongest implementation evidence is in `internal/runtime/agent/runner.go`. Use those findings as the source of truth for `Explain runner answer readiness`.\n\nWhat this establishes:\n- The source body shows `runnerReadinessState` computes answer readiness."
	runner := &Runner{RuntimeBrief: brief}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "source_grounding_unmet" {
		t.Fatalf("source-backed scaffold final should still be rejected, got (%q, %q)", kind, reason)
	}
	if reason == "" {
		t.Fatal("expected scaffold rejection reason")
	}
}

func TestFinalAnswerAcceptanceRejectsOffObjectiveSourceBackedAnswer(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.")
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Output:  `{"candidate_files":[{"path":"internal/surfaces/tui/app/rendering.go"},{"path":"internal/surfaces/tui/app/model.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/surfaces/tui/app/rendering.go lines 1-9",
			Output:  "1:package app\n2:\n3:func evidenceDisplayName(rawTool string) string {\n4:\treturn strings.TrimSpace(rawTool)\n5:}\n",
		},
	}
	answer := "The relevant code is `internal/surfaces/tui/app/rendering.go`: `evidenceDisplayName` trims the raw tool name before display."
	runner := &Runner{RuntimeBrief: brief}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "answer_objective_unmet" {
		t.Fatalf("off-objective source-backed answer got (%q, %q), want answer_objective_unmet", kind, reason)
	}
	if !strings.Contains(reason, "does not establish the user's objective") {
		t.Fatalf("blocker reason should describe objective mismatch, got %q", reason)
	}
}

func TestFinalAnswerAcceptanceRejectsObjectiveParrotWithWrongSource(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.")
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Output:  `{"candidate_files":[{"path":"internal/surfaces/tui/app/rendering.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/surfaces/tui/app/rendering.go lines 1-9",
			Output:  "1:package app\n2:\n3:func evidenceDisplayName(rawTool string) string {\n4:\treturn strings.TrimSpace(rawTool)\n5:}\n",
		},
	}
	answer := "The TUI chat input newline wrap and upward shift issue is in `internal/surfaces/tui/app/rendering.go`: `evidenceDisplayName` trims the raw tool name before display."
	runner := &Runner{RuntimeBrief: brief}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "answer_objective_unmet" {
		t.Fatalf("objective-parroting wrong-source answer got (%q, %q), want answer_objective_unmet", kind, reason)
	}
}

func TestFinalAnswerAcceptanceRejectsCitationlessObjectiveParrotWithUnrelatedRead(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.")
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/surfaces/tui/app/rendering.go lines 1-9",
		Output:  "1:package app\n2:\n3:func evidenceDisplayName(rawTool string) string {\n4:\treturn strings.TrimSpace(rawTool)\n5:}\n",
	}}
	answer := "The TUI chat input newline wrap and upward shift issue is caused by trimming the raw tool name before display."
	runner := &Runner{RuntimeBrief: brief}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "answer_objective_unmet" {
		t.Fatalf("citationless objective-parroting unrelated read got (%q, %q), want answer_objective_unmet", kind, reason)
	}
}

func TestFinalAnswerAcceptanceAppliesToDirectAnswerTaskFamilyAlias(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.")
	brief.TaskFamily = string(workerruntime.TaskFamilyDirectAnswer)
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Output:  `{"candidate_files":[{"path":"internal/surfaces/tui/app/rendering.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/surfaces/tui/app/rendering.go lines 1-9",
			Output:  "1:package app\n2:\n3:func evidenceDisplayName(rawTool string) string {\n4:\treturn strings.TrimSpace(rawTool)\n5:}\n",
		},
	}
	answer := "The relevant code is `internal/surfaces/tui/app/rendering.go`: `evidenceDisplayName` trims the raw tool name before display."
	runner := &Runner{RuntimeBrief: brief}

	kind, _ := runner.finalAnswerBlockerKind(answer, results)
	if kind != "answer_objective_unmet" {
		t.Fatalf("direct_answer task-family alias was not checked: got %q, want answer_objective_unmet", kind)
	}
}

func TestFinalAnswerAcceptanceRejectsAliasInsufficientEvidenceWithoutSourceBody(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.")
	brief.TaskFamily = string(workerruntime.TaskFamilyDirectAnswer)
	results := []workspace.ToolResult{{
		Tool:    "context_pack",
		Summary: "Context pack wrote packet",
		Output:  `{"candidate_files":[{"path":"internal/surfaces/tui/app/rendering.go"}]}`,
	}}
	answer := "I cannot determine from the available evidence why the TUI chat input first wraps to a newline."
	runner := &Runner{RuntimeBrief: brief}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "answer_objective_unmet" {
		t.Fatalf("premature insufficient-evidence alias answer got (%q, %q), want answer_objective_unmet", kind, reason)
	}
	if !strings.Contains(reason, "before inspecting source-body or diagnostic evidence") {
		t.Fatalf("expected source-body insufficiency reason, got %q", reason)
	}
}

func TestFinalAnswerAcceptanceAllowsAliasInsufficientEvidenceAfterSourceBody(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.")
	brief.TaskFamily = string(workerruntime.TaskFamilyDirectAnswer)
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/surfaces/tui/app/rendering.go lines 1-9",
		Output:  "1:package app\n2:\n3:func evidenceDisplayName(rawTool string) string {\n4:\treturn strings.TrimSpace(rawTool)\n5:}\n",
	}}
	answer := "I cannot determine from the available evidence why the TUI chat input first wraps to a newline."
	runner := &Runner{RuntimeBrief: brief}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "" || reason != "" {
		t.Fatalf("source-backed insufficient-evidence alias answer blocked: kind=%q reason=%q", kind, reason)
	}
}

func TestFinalAnswerAcceptanceRejectsAliasObjectiveParrotWithWrongSource(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.")
	brief.TaskFamily = string(workerruntime.TaskFamilyDirectAnswer)
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Output:  `{"candidate_files":[{"path":"internal/surfaces/tui/app/rendering.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/surfaces/tui/app/rendering.go lines 1-9",
			Output:  "1:package app\n2:\n3:func evidenceDisplayName(rawTool string) string {\n4:\treturn strings.TrimSpace(rawTool)\n5:}\n",
		},
	}
	answer := "The TUI chat input newline wrap and upward shift issue is in `internal/surfaces/tui/app/rendering.go`: `evidenceDisplayName` trims the raw tool name before display."
	runner := &Runner{RuntimeBrief: brief}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "answer_objective_unmet" {
		t.Fatalf("alias objective-parroting wrong-source answer got (%q, %q), want answer_objective_unmet", kind, reason)
	}
}

func TestGenericReadOnlyEvidenceReadyRejectsAliasWrongSource(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.")
	brief.TaskFamily = string(workerruntime.TaskFamilyDirectAnswer)
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Output:  `{"candidate_files":[{"path":"internal/surfaces/tui/app/rendering.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/surfaces/tui/app/rendering.go lines 1-9",
			Output:  "1:package app\n2:\n3:func evidenceDisplayName(rawTool string) string {\n4:\treturn strings.TrimSpace(rawTool)\n5:}\n",
		},
	}

	if genericReadOnlyAnswerEvidenceReady(brief, results) {
		t.Fatal("direct_answer alias readiness should not close from an unrelated candidate source body")
	}
}

func TestConversationEvidenceSatisfiedNormalizesDirectAnswerAlias(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.")
	brief.TaskFamily = string(workerruntime.TaskFamilyDirectAnswer)
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Output:  `{"candidate_files":[{"path":"internal/surfaces/tui/app/rendering.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/surfaces/tui/app/rendering.go lines 1-9",
			Output:  "1:package app\n2:\n3:func evidenceDisplayName(rawTool string) string {\n4:\treturn strings.TrimSpace(rawTool)\n5:}\n",
		},
	}
	if conversationEvidenceSatisfied(brief, results) {
		t.Fatal("direct_answer alias should use objective-relevant source-body evidence, not any candidate body")
	}

	results[1] = workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read internal/surfaces/tui/app/chat_input.go lines 1-9",
		Output:  "1:package app\n2:\n3:func syncChatInputHeight() {\n4:\t// chat input newline wrap keeps the first existing line visible instead of shifting upward\n5:}\n",
	}
	if !conversationEvidenceSatisfied(brief, results) {
		t.Fatal("direct_answer alias should be satisfied by objective-relevant source-body evidence")
	}
}

func TestRunnerReadinessStateNormalizesDirectAnswerAlias(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.")
	brief.TaskFamily = string(workerruntime.TaskFamilyDirectAnswer)
	results := []workspace.ToolResult{{
		Tool:    "context_pack",
		Summary: "Context pack wrote packet",
		Output:  `{"candidate_files":[{"path":"internal/surfaces/tui/app/rendering.go"}]}`,
	}}

	readiness := runnerReadinessState("main", brief, results)
	if readiness.ReadyToAnswer {
		t.Fatalf("direct_answer alias should not be answer-ready from localization only: %#v", readiness)
	}
	if !slices.Contains(readiness.OpenObligations, "read_only_source_body_evidence_pending") {
		t.Fatalf("expected source-body obligation for direct_answer alias, got %#v", readiness.OpenObligations)
	}
}

func TestFinalAnswerAcceptanceAllowsObjectiveRelevantSourceBackedAnswer(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.")
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/surfaces/tui/app/chat_input.go lines 1-9",
		Output:  "1:package app\n2:\n3:func syncChatInputHeight() {\n4:\t// chat input newline wrap keeps the first existing line visible instead of shifting upward\n5:}\n",
	}}
	answer := "The chat input newline wrap issue is handled in `internal/surfaces/tui/app/chat_input.go`: `syncChatInputHeight` keeps the first existing line visible instead of letting it shift upward."
	runner := &Runner{RuntimeBrief: brief}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "" || reason != "" {
		t.Fatalf("objective-relevant source-backed answer blocked: kind=%q reason=%q", kind, reason)
	}
}

func TestFinalAnswerAcceptanceAllowsNamedFileContentAnswer(t *testing.T) {
	brief := readOnlyConversationBriefForTest("What is in internal/runtime/agent/runner.go?")
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/runtime/agent/runner.go lines 1-20",
		Output:  "1:package agent\n2:\n3:func RunExample() {}\n",
	}}
	answer := "runner.go contains package agent."
	runner := &Runner{RuntimeBrief: brief}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "" || reason != "" {
		t.Fatalf("named-file source-backed answer blocked: kind=%q reason=%q", kind, reason)
	}
}

func TestFinalAnswerAcceptanceAllowsFixturePurposeSummary(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Summarize what this fixture repository is for. Use repository evidence and do not edit files.")
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 README.md lines 1-5 (total lines: 5)",
		Output: strings.Join([]string{
			"1:# Frozen label repo",
			"2:",
			"3:This fixture repository is used for deterministic Idiom scripted-contract tests.",
			"4:It gives the harness a tiny real Git workspace for mutation, verification, and rollback.",
			"5:Do not edit files for read-only summary requests.",
		}, "\n"),
	}}
	answer := "This fixture repository exists for deterministic Idiom scripted-contract tests. It gives the harness a tiny real Git workspace where a fake model can drive the real binary through workspace mutation, verification, and rollback. I did not edit files or run shell commands."
	runner := &Runner{RuntimeBrief: brief}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "" || reason != "" {
		t.Fatalf("fixture purpose summary blocked: kind=%q reason=%q", kind, reason)
	}
}

func TestFinalAnswerAcceptanceRejectsNamedFileWhyAnswerWithUnrelatedBody(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Why does the TUI chat input first wrap to a newline in internal/surfaces/tui/app/rendering.go?")
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/surfaces/tui/app/rendering.go lines 1-9",
		Output:  "1:package app\n2:\n3:func evidenceDisplayName(rawTool string) string {\n4:\treturn strings.TrimSpace(rawTool)\n5:}\n",
	}}
	answer := "`internal/surfaces/tui/app/rendering.go` contains `evidenceDisplayName`, which trims the raw tool name before display."
	runner := &Runner{RuntimeBrief: brief}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "answer_objective_unmet" {
		t.Fatalf("named-file why answer with unrelated body got (%q, %q), want answer_objective_unmet", kind, reason)
	}
}

func TestAcceptedAnswerEvidencePathsPreferObjectiveRelevantSourceBody(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.")
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Output:  `{"candidate_files":[{"path":"internal/runtime/agent/runner.go"},{"path":"internal/surfaces/tui/app/rendering.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/surfaces/tui/app/chat_input.go lines 1-9",
			Output:  "1:package app\n2:\n3:func syncChatInputHeight() {\n4:\t// chat input newline wrap keeps the first existing line visible instead of shifting upward\n5:}\n",
		},
	}
	answer := "The chat input newline wrap issue is in `internal/surfaces/tui/app/chat_input.go`, where the input height sync keeps the first existing line visible."

	paths := acceptedAnswerEvidencePaths(answer, results, brief)
	if len(paths) == 0 {
		t.Fatal("expected evidence paths")
	}
	if paths[0] != "internal/surfaces/tui/app/chat_input.go" {
		t.Fatalf("expected objective-relevant source body first, got %v", paths)
	}
}

func TestAcceptedAnswerEvidencePathsAliasIgnoresUnrelatedCandidateBody(t *testing.T) {
	brief := readOnlyConversationBriefForTest("Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.")
	brief.TaskFamily = string(workerruntime.TaskFamilyDirectAnswer)
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Output:  `{"candidate_files":[{"path":"internal/surfaces/tui/app/rendering.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/surfaces/tui/app/rendering.go lines 1-9",
			Output:  "1:package app\n2:\n3:func evidenceDisplayName(rawTool string) string {\n4:\treturn strings.TrimSpace(rawTool)\n5:}\n",
		},
	}
	answer := "The TUI chat input newline wrap and upward shift issue is not established by the inspected source."

	paths := acceptedAnswerEvidencePaths(answer, results, brief)
	if len(paths) != 0 {
		t.Fatalf("alias evidence paths should not report unrelated candidate source body, got %v", paths)
	}
}
