package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func TestDelegatedReadOnlyFindingLinesIgnoreSearchContextHandles(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool: "read_file",
		Output: strings.Join([]string{
			"package workspace",
			"",
			"// task tool manifest entries wire subagents for delegated child work.",
			"context:",
			"f1 internal/capabilities/workspace/tool_manifest.go:3:// task tool manifest entries wire subagents for delegated child work.",
			"f1 internal/capabilities/workspace/tool_manifest.go:4:\treturn \"subagents are wired through task tool manifest entries for delegated child work\"",
		}, "\n"),
	}}

	lines := delegatedReadOnlyFindingLines(results, []string{"internal/capabilities/workspace/tool_manifest.go"}, 4)
	if len(lines) != 0 {
		t.Fatalf("expected comment-only context handles to be ignored, got %#v", lines)
	}
}

func TestDelegatedTaskAnswerDoesNotSynthesizeFromStrippedControlPacketOnly(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "task",
		Summary: "Child task completed",
		Output: strings.Join([]string{
			"Question: What does the debug log redacted mode do?",
			"Source: delegated read-only evidence packet.",
			"",
			"Inspected paths:",
			"- `internal/evidence/transcript/debuglog.go`",
			"- `internal/platform/config/config.go`",
			"",
			"<<QUESTION>>: Completion gate rejected the final answer repeatedly before the required completion evidence was produced.",
			"Missing completion evidence: the answer does not satisfy the required evidence contract. Missing: inspected_paths.",
			"",
			"Additional inspected paths:",
			"- `internal/runtime/agent/runner.go`",
		}, "\n"),
	}}

	if answer, ok := synthesizeDelegatedReadOnlyAnswer("What does the debug log redacted mode do?", results); ok {
		t.Fatalf("expected stripped control-only packet to be insufficient for synthesis, got:\n%s", answer)
	}
}

func TestDelegatedTaskAnswerStrippedSectionsDoNotContributeEvidencePaths(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "task",
		Summary: "Child task completed",
		Output: strings.Join([]string{
			"Question: What does ShortLabel return?",
			"Source: delegated read-only evidence packet.",
			"",
			"Source-body evidence paths:",
			"- `internal/labels/labels.go`",
			"",
			"Inspected paths:",
			"- `internal/runtime/agent/runner.go`",
			"",
			"Key findings:",
			"- Found ShortLabel returns \"Save changes\" in internal/labels/labels.go.",
			"",
			"<<QUESTION>>: Completion gate rejected the final answer repeatedly before the required completion evidence was produced.",
			"Missing completion evidence: the answer does not satisfy the required evidence contract. Missing: inspected_paths.",
			"",
			"Additional inspected paths:",
			"- `internal/evidence/transcript/debuglog.go`",
		}, "\n"),
	}}

	answer, ok := synthesizeDelegatedReadOnlyAnswer("What does ShortLabel return?", results)
	if !ok {
		t.Fatal("expected synthesized answer from sanitized key finding")
	}

	for _, want := range []string{
		"The answer is grounded in",
		"`internal/labels/labels.go`",
		"Save changes",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected synthesized answer to contain %q, got:\n%s", want, answer)
		}
	}

	for _, forbidden := range []string{
		"Question:",
		"Source: delegated",
		"Inspected paths:",
		"<<QUESTION>>",
		"Completion gate rejected",
		"Missing completion evidence",
		"Additional inspected paths:",
		"`internal/runtime/agent/runner.go`",
		"`internal/evidence/transcript/debuglog.go`",
	} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("synthesized answer leaked internal child packet text %q:\n%s", forbidden, answer)
		}
	}

	runner := &Runner{}
	if blocker, reason := runner.finalAnswerBlockerKind(answer, results); blocker != "" || reason != "" {
		t.Fatalf("synthesized answer should pass final-answer blocker, got (%q, %q):\n%s", blocker, reason, answer)
	}
}

func TestReadOnlyCompletionGateRepeatedBlockerPreservesReasonWithoutStrategy(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Contract:          string(workerruntime.ContractDirectAnswer),
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
			Objective:         "Review normalizeDelegatedDiscoveryEvidencePath for Unicode panics.",
		},
	}

	answer := runner.completionGateRepeatedBlockerAnswer(workspace.ToolResult{
		Tool:  "completion_gate",
		Error: "Architecture completion gate rejected unsupported detail(s): indexFold, utf8.ValidString.",
	}, []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/runtime/agent/runner_delegated_synthesis.go lines 1-120",
		Output:  "1:package agent\n2:func normalizeDelegatedDiscoveryEvidencePath(path string) string { return path }\n",
	}})

	for _, want := range []string{
		"Architecture completion gate rejected unsupported detail(s): indexFold, utf8.ValidString.",
		"Evidence paths observed before rejection:",
		"internal/runtime/agent/runner_delegated_synthesis.go",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected repeated blocker to contain %q, got:\n%s", want, answer)
		}
	}

	for _, forbidden := range []string{
		"Required next evidence",
		"state the supported concrete finding",
		"one bounded probe",
		"not enough supported evidence",
	} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("read-only repeated blocker should not prescribe strategy %q:\n%s", forbidden, answer)
		}
	}
}

func TestSubmitCorrectionRepeatedBlockerDoesNotAdvertiseInvalidEscapes(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go", "b.go"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read a.go lines 1-10 (total lines: 10)", Output: "1:package a\n"},
		{Tool: "read_file", Summary: "Read b.go lines 1-10 (total lines: 10)", Output: "1:package b\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "missing required edit path(s): `b.go`.",
		},
	}

	answer := runner.completionGateRepeatedBlockerAnswer(workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Completion gate rejected non-submit_plan correction",
		Error:   "Submit-correction mode has exactly one valid outcome: parser-readable submit_plan.",
	}, results)

	for _, want := range []string{
		"Submit-correction mode has exactly one valid outcome: parser-readable submit_plan.",
		"a.go",
		"b.go",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("submit-correction repeated blocker missing %q:\n%s", want, answer)
		}
	}

	for _, forbidden := range []string{
		"source-satisfied evidence or a path-specific blocker reason",
		"provide a path-specific blocker reason",
	} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("submit-correction repeated blocker advertised invalid escape %q:\n%s", forbidden, answer)
		}
	}
}

func TestAnswerComposerDoesNotLeakTelemetrySections(t *testing.T) {
	answer, ok := AnswerComposer{}.Compose(AnswerComposerInput{
		Question:      "Can you tell me how this harness uses subagents?",
		EvidencePaths: []string{"`internal/runtime/agent/runner.go`,", "internal/runtime/agent/runner_child_tasks.go"},
		Summaries: []delegatedDiscoverySummary{{
			Purpose:      "architecture_boundary",
			ArtifactPath: ".idiom/discovery/queries/query.json",
			Evidence: []struct {
				Name          string `json:"name"`
				Tool          string `json:"tool"`
				Summary       string `json:"summary"`
				Error         string `json:"error,omitempty"`
				OutputExcerpt string `json:"output_excerpt,omitempty"`
			}{
				{Name: "text_anchor_search", Summary: "Found subagent handoff implementation paths."},
			},
		}},
	})
	if !ok {
		t.Fatal("expected answer composer to produce generic evidence answer")
	}

	for _, forbidden := range []string{"Discovery packet:", "Relevant source hits:", "Evidence used:", "Confidence:", ".idiom/discovery"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("answer composer leaked telemetry section %q:\n%s", forbidden, answer)
		}
	}

	for _, want := range []string{"The strongest implementation evidence is in", "Use those findings as the source of truth"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("answer composer missing polished section %q:\n%s", want, answer)
		}
	}

	if !strings.Contains(answer, "`internal/runtime/agent/runner.go`, `internal/runtime/agent/runner_child_tasks.go`") {
		t.Fatalf("answer composer should format inline evidence paths cleanly:\n%s", answer)
	}

	for _, forbidden := range []string{"## Grounded Answer", "### Decision Guidance"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("answer composer leaked internal heading %q:\n%s", forbidden, answer)
		}
	}
}

func TestAnswerComposerDoesNotTemplateQuestionClassesWithoutEvidenceSummary(t *testing.T) {
	answer, ok := AnswerComposer{}.Compose(AnswerComposerInput{
		Question: "How does this harness make use of sub-agents across read-only and workbranch modes?",
		EvidencePaths: []string{
			"internal/runtime/agent/runner.go",
			"internal/runtime/agent/runner_child_tasks.go",
			"internal/runtime/agent/answer_composer.go",
		},
	})
	if ok || strings.TrimSpace(answer) != "" {
		t.Fatalf("expected no topic-template answer without evidence summaries, ok=%v answer:\n%s", ok, answer)
	}
}

func TestAnswerComposerDoesNotSpecialCaseQuestionClassWithPathsOnly(t *testing.T) {
	answer, ok := AnswerComposer{}.Compose(AnswerComposerInput{
		Question: "How does this harness make use of sub-agents?",
		EvidencePaths: []string{
			"internal/runtime/agent/runner.go",
			"internal/runtime/agent/runner_child_tasks.go",
		},
	})
	if ok || strings.TrimSpace(answer) != "" {
		t.Fatalf("expected no question-class-specific answer with paths only, ok=%v answer:\n%s", ok, answer)
	}
}

func TestAnswerComposerDecoratesDiscoveryToolEvidenceGenerically(t *testing.T) {
	answer, ok := AnswerComposer{}.Compose(AnswerComposerInput{
		Question: "What can we add to our discovery tools in an effort at increasing their overall usefulness?",
		EvidencePaths: []string{
			"internal/runtime/agent/runner.go",
			"internal/runtime/agent/answer_composer.go",
			"internal/runtime/agent/runner_lane_policy.go",
			"internal/runtime/agent/discovery_redirect.go",
			"internal/runtime/agent/runner_test.go",
		},
		Summaries: []delegatedDiscoverySummary{{
			Purpose:      "architecture_boundary",
			ArtifactPath: ".idiom/discovery/queries/query.json",
			Evidence: []struct {
				Name          string `json:"name"`
				Tool          string `json:"tool"`
				Summary       string `json:"summary"`
				Error         string `json:"error,omitempty"`
				OutputExcerpt string `json:"output_excerpt,omitempty"`
			}{
				{Name: "text_anchor_search", Summary: "Found discovery implementation paths."},
				{Name: "go_read_symbol", Summary: "Read answer composer and runner synthesis boundaries."},
			},
		}},
	})
	if !ok {
		t.Fatal("expected generic discovery-tool evidence answer")
	}

	for _, want := range []string{
		"The strongest implementation evidence is in",
		"What this establishes:",
		"Text anchors:",
		"Symbol read:",
		"`internal/runtime/agent/answer_composer.go`",
		"`internal/runtime/agent/runner.go`",
		"`internal/runtime/agent/runner_lane_policy.go`",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected generic discovery evidence answer to contain %q, got:\n%s", want, answer)
		}
	}

	for _, forbidden := range []string{"## Grounded Answer", "### Decision Guidance", "What I found:", "text_anchor_search:", "Open a returned handle", "Discovery packet:", "discovery packet", "Evidence used:", "Confidence:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("generic discovery evidence answer leaked template/raw section %q:\n%s", forbidden, answer)
		}
	}
}

func TestAnswerComposerGenericFallbackDecoratesDiscoveryLayers(t *testing.T) {
	answer, ok := AnswerComposer{}.Compose(AnswerComposerInput{
		Question:      "How is routing decided?",
		EvidencePaths: []string{"internal/surfaces/tui/app/turn_planner.go", "internal/runtime/agent/modes.go"},
		Summaries: []delegatedDiscoverySummary{{
			Purpose:      "architecture_boundary",
			ArtifactPath: ".idiom/discovery/queries/query.json",
			Evidence: []struct {
				Name          string `json:"name"`
				Tool          string `json:"tool"`
				Summary       string `json:"summary"`
				Error         string `json:"error,omitempty"`
				OutputExcerpt string `json:"output_excerpt,omitempty"`
			}{
				{Name: "text_anchor_search", Summary: "Found routing anchors in internal/surfaces/tui/app/turn_planner.go. Bounded-read capability is available on returned handles, or narrow the pattern to see more."},
				{Name: "go_read_symbol", Summary: "Read classifyPromptRoute in internal/surfaces/tui/app/turn_planner.go lines 42-96"},
			},
		}},
	})
	if !ok {
		t.Fatal("expected generic composed answer")
	}

	for _, want := range []string{"The strongest implementation evidence is in", "What this establishes:", "Text anchors:", "Symbol read:"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected decorated generic answer to contain %q, got:\n%s", want, answer)
		}
	}

	for _, forbidden := range []string{"## Grounded Answer", "### Decision Guidance", "What I found:", "text_anchor_search:", "go_read_symbol:", "Bounded-read capability is available", "Discovery packet:", "discovery packet", "Confidence:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("generic answer leaked raw discovery wording %q:\n%s", forbidden, answer)
		}
	}
}

func TestRunnerFinalBoundaryDecoratesModelFinalDiscoveryAnswer(t *testing.T) {
	root := t.TempDir()
	replies := []string{
		"What I found:\n- text_anchor_search: Found 178 matching line(s) (showing first 50). Bounded-read capability is available on returned handles, or narrow the pattern to see more.\n\nDiscovery packet: `.idiom/discovery/queries/query.json`.\nConfidence: high",
		"`internal/runtime/agent/runner.go` contains the runner `Run` method. `internal/runtime/agent/runner_lane_policy.go` contains `conversationEvidenceSatisfied`, the readiness check present in the inspected source. Together, those inspected anchors show the useful integration point: discovery output has to connect into runner closeout and readiness checks before it can support final synthesis.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := "The scout reported that the delegated probe found a Go source file declaring the widget package."
		if requests < len(replies) {
			reply = replies[requests]
		}

		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))

	defer server.Close()

	var roles []string

	runner := &Runner{
		Client: llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 2, MaxNoProgressTurns: 2},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			Objective:         "What can we add to our discovery tools in an effort at increasing their overall usefulness?",
			PermissionProfile: "read_only_workspace",
		},
		SeedResults: []workspace.ToolResult{{
			Tool:    "discovery_query",
			Summary: "Ran architecture_boundary discovery query with 10 layer(s); artifact: .idiom/discovery/queries/query.json",
			Output:  `{"purpose":"architecture_boundary","artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"text_anchor_search","summary":"Found discovery tool implementation paths.","output_excerpt":"internal/runtime/agent/answer_composer.go:18: func (AnswerComposer) Compose\ninternal/runtime/agent/runner.go:128: finish := func\ninternal/runtime/agent/runner_lane_policy.go:637: delegatedReadOnlyAnswerReadyAfterResult"},{"name":"dependency_graph","summary":"Collected Go dependency graph with go list -deps"}]}`,
		}, {
			Tool:    "read_file",
			Summary: "Read f1 internal/runtime/agent/runner.go lines 1-80",
			Output:  "package agent\n\nfunc (r *Runner) Run() {}\n",
		}, {
			Tool:    "read_file",
			Summary: "Read f1 internal/runtime/agent/runner_lane_policy.go lines 1-80",
			Output:  "package agent\n\nfunc conversationEvidenceSatisfied() bool { return true }\n",
		}},
	}

	runner.OnEvent = func(event RunEvent) {
		roles = append(roles, event.Role)
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "What can we add to our discovery tools in an effort at increasing their overall usefulness?"}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests != 2 {
		t.Fatalf("expected raw discovery packet rejection followed by grounded answer, got %d requests", requests)
	}

	for _, want := range []string{"inspected anchors", "`internal/runtime/agent/runner_lane_policy.go`", "`internal/runtime/agent/runner.go`"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected decorated final answer to contain %q, got:\n%s", want, answer)
		}
	}

	for _, forbidden := range []string{"## Grounded Answer", "### Decision Guidance", "What I found:", "text_anchor_search:", "Discovery packet", "Confidence:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("final boundary leaked raw answer section %q:\n%s", forbidden, answer)
		}
	}
}

func TestFinalAnswerDecoratorSkipsChildInternalOutput(t *testing.T) {
	raw := "What I found:\n- text_anchor_search: Found paths.\n\nDiscovery packet: `.idiom/discovery/queries/query.json`."
	runner := &Runner{AgentName: "child:explorer"}
	answer := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     raw,
		StopReason: "model_final",
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "delegated_read_only",
			Objective:  "What can we add to discovery tools?",
		},
		Results: []workspace.ToolResult{{
			Tool:    "discovery_query",
			Summary: "Ran discovery query",
			Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"text_anchor_search","summary":"Found paths.","output_excerpt":"internal/runtime/agent/runner.go:128: finish"}]}`,
		}},
		UserFacing: runner.userFacingFinalAnswer(),
	})

	if answer != raw {
		t.Fatalf("expected child/internal answer to remain compact and undecorated, got:\n%s", answer)
	}
}

func TestFinalAnswerDecoratorDecoratesBudgetExhaustionScaffold(t *testing.T) {
	raw := "What I found:\n- text_anchor_search: Found paths.\n\nDiscovery packet: `internal/runtime/agent/runner.go`."
	runner := &Runner{}
	answer := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     raw,
		StopReason: "tool_step_budget_exhausted",
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "delegated_read_only",
			Objective:  "Explain how discovery tools are routed.",
		},
		Results: []workspace.ToolResult{{
			Tool:    "discovery_query",
			Summary: "Ran discovery query",
			Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"text_anchor_search","summary":"Found paths.","output_excerpt":"internal/runtime/agent/runner.go:128: finish"}]}`,
		}},
		UserFacing: true,
	})

	for _, want := range []string{"not have enough supported evidence", "internal/runtime/agent/runner.go"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected budget-exhausted scaffold to become actionable blocker containing %q, got:\n%s", want, answer)
		}
	}

	for _, forbidden := range []string{"<<QUESTION>>", "Discovery packet:", "What I found:", "source-body"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("budget-exhausted scaffold leaked internal phrase %q:\n%s", forbidden, answer)
		}
	}
}

func TestFinalAnswerDecoratorDecoratesPostReadinessBudgetClosure(t *testing.T) {
	raw := "Partial findings from this turn (the budget was reached before a full synthesis):\n\nVerified context:\n- Ran discovery query\n"
	runner := &Runner{}
	answer := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     raw,
		StopReason: "post_readiness_probe_budget_exhausted",
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "delegated_read_only",
			Objective:  "Explain how discovery tools are routed.",
		},
		Results: []workspace.ToolResult{{
			Tool:    "discovery_query",
			Summary: "Ran discovery query",
			Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"text_anchor_search","summary":"Found routed implementation paths.","output_excerpt":"internal/runtime/agent/runner.go:128: finish"}]}`,
		}, {
			Tool:    "read_file",
			Summary: "Read f1 internal/runtime/agent/runner.go lines 1-80",
			Output:  "package agent\n\nfunc (r *Runner) Run() {}\n",
		}},
		UserFacing: true,
	})

	if strings.Contains(answer, "Partial findings from this turn") {
		t.Fatalf("expected post-readiness closure to use composed answer, got:\n%s", answer)
	}

	if strings.Contains(answer, "## Grounded Answer") || strings.Contains(answer, "### Decision Guidance") {
		t.Fatalf("expected composed answer without internal composer headings, got:\n%s", answer)
	}

	for _, want := range []string{"internal/runtime/agent/runner.go", "Runner.Run"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected presentable post-readiness answer to contain %q, got:\n%s", want, answer)
		}
	}

	if strings.Contains(answer, "func (r *Runner) Run()") {
		t.Fatalf("expected fact synthesis instead of raw source echo, got:\n%s", answer)
	}
}

func TestFinalAnswerDecoratorDoesNotLeakDelegatedPacketWhenComposerLacksDiscoverySummary(t *testing.T) {
	raw := "Question: how does this harness utilize sub-agents?\n" +
		"Source: delegated read-only evidence packet.\n\n" +
		"Source-body evidence paths:\n" +
		"- `internal/runtime/agent/runner.go`\n" +
		"- `internal/runtime/agent/runner_child_tasks.go`\n\n" +
		"Inspected paths:\n" +
		"- `internal/runtime/agent/runner.go`\n" +
		"- `internal/runtime/agent/runner_child_tasks.go`\n" +
		"- `internal/runtime/agent/answer_composer.go`\n\n" +
		"Key findings:\n" +
		"- Child task completed\n" +
		"  The strongest implementation evidence is in `internal/runtime/agent/runner.go`, `internal/runtime/agent/runner_child_tasks.go`.\n" +
		"- Outlined 273 top-level declaration(s) in internal/runtime/agent/runner.go.\n\n" +
		"Confidence: high"
	runner := &Runner{}
	answer := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     raw,
		StopReason: "post_readiness_probe_budget_exhausted",
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "architecture",
			Objective:  "how does this harness utilize sub-agents?",
		},
		Results: []workspace.ToolResult{{
			Tool:    "task",
			Summary: "Child task completed",
			Output:  raw,
		}},
		UserFacing: true,
	})

	for _, forbidden := range []string{"Question:", "Source: delegated read-only evidence packet", "Inspected paths:", "Key findings:", "Confidence:", "Outlined 273"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("final boundary leaked internal packet section %q:\n%s", forbidden, answer)
		}
	}

	for _, want := range []string{"not have enough supported evidence", "`internal/runtime/agent/runner.go`", "`internal/runtime/agent/runner_child_tasks.go`"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected evidence-retained blocker to contain %q, got:\n%s", want, answer)
		}
	}

	if strings.Contains(answer, "<<QUESTION>>") || strings.Contains(answer, "exact missing claim") {
		t.Fatalf("final boundary leaked internal blocker protocol:\n%s", answer)
	}
}

func TestFinalAnswerDecoratorUsesExactProbeEvidenceOverRetainedScopeSummary(t *testing.T) {
	raw := "The strongest implementation evidence is in `internal/runtime/agent/runner_child_tasks.go`. Use those findings as the source of truth for `how does this harness utilize sub-agents?`.\n\nWhat this establishes:\n- Text anchors: Found child-runner implementation paths."
	runner := &Runner{}
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "architecture",
		Contract:   "architecture_assessment",
		Objective:  "how does this harness utilize sub-agents?",
	}
	results := []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran architecture_boundary discovery query",
			Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"text_anchor_search","summary":"Found child-runner implementation paths.","output_excerpt":"internal/runtime/agent/runner_child_tasks.go:16: func (r *Runner) runChildTask(ctx context.Context, req workspace.ToolRequest, opts llama.ChatOptions) workspace.ToolResult"}]}`,
		},
		{
			Tool:    "go_read_symbol",
			Summary: "Read Runner.runChildTask in internal/runtime/agent/runner_child_tasks.go lines 16-132",
			Output:  "func (r *Runner) runChildTask(ctx context.Context, req workspace.ToolRequest, opts llama.ChatOptions) workspace.ToolResult {\n\tprompt = normalizeReadOnlyChildPrompt(prompt)\n\tprompt = r.augmentReadOnlyChildPrompt(prompt)\n\tchild := &Runner{\n\t\tClient: r.Client,\n\t\tWorkspace: r.Workspace,\n\t\tPolicy: childPolicy,\n\t\tPermissionPolicy: readOnlyChildTaskPermissionPolicy(),\n\t\tDisableTaskTool: true,\n\t}\n\tanswer, err := child.Run(ctx, []llama.ChatMessage{{Role: \"user\", Content: prompt}}, opts, nil, nil, nil)\n\tanswer = augmentDelegatedReadOnlyPacket(answer, prompt, childResults)\n\treturn workspace.ToolResult{Tool: \"task\", Summary: \"Child task completed\", Output: strings.TrimSpace(answer)}\n}\n",
		},
	}
	answer := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     raw,
		StopReason: "post_readiness_probe_budget_exhausted",
		Brief:      brief,
		Results:    results,
		UserFacing: true,
	})

	for _, want := range []string{
		"`Runner.runChildTask`",
		"`internal/runtime/agent/runner_child_tasks.go`",
		"sets `prompt` to `normalizeReadOnlyChildPrompt(prompt)`",
		"assigns `child` from `&Runner{`",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected exact-probe fallback answer to contain %q, got:\n%s", want, answer)
		}
	}

	for _, forbidden := range []string{
		"retained source context",
		"bounded answer",
		"The strongest implementation evidence is in",
		"What this establishes:",
	} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("expected exact-probe fallback answer to avoid %q, got:\n%s", forbidden, answer)
		}
	}

	runner.RuntimeBrief = brief
	if kind, _ := runner.finalAnswerBlockerKind(answer, results); kind != "non_answer_prose" {
		t.Fatalf("architecture exact-probe source inventory should be rejected as non-answer prose, got %q:\n%s", kind, answer)
	}
}

func TestFinalAnswerDecoratorPreservesSubstantiveModelAnswerOverEvidencePacket(t *testing.T) {
	raw := "The harness uses sub-agents through the `task` tool. The parent runner delegates broad read-only questions to a bounded child runner, and `Runner.runChildTask` creates that child with its own prompt, policy, tool budget, and read-only permissions before returning the child result as the parent's evidence packet.\n\nAdditional inspected paths:\n- `internal/runtime/agent/answer_composer.go`\n\nExact probes already inspected:\n- `runChildTask`"
	runner := &Runner{}
	answer := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     raw,
		StopReason: "post_readiness_probe_budget_exhausted",
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "architecture",
			Objective:  "how does this harness utilize sub-agents?",
		},
		Results: []workspace.ToolResult{{
			Tool:   "task",
			Output: "For `how does this harness utilize sub-agents?`, the gathered evidence points to these implementation surfaces and findings.\n\nEvidence:\n- Text anchors: Found 25 matching line(s).\n\nPrimary files: `internal/runtime/agent/runner.go`, `internal/runtime/agent/runner_child_tasks.go`.",
		}},
		UserFacing: true,
	})

	if !strings.Contains(answer, "The harness uses sub-agents through the `task` tool") {
		t.Fatalf("expected substantive model answer to be preserved, got:\n%s", answer)
	}

	for _, forbidden := range []string{"Additional inspected paths:", "Exact probes already inspected:", "the gathered evidence points to these implementation surfaces", "Evidence:\n- Text anchors:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("expected internal evidence packet/appendix %q to be removed, got:\n%s", forbidden, answer)
		}
	}
}

func TestChildPacketBoundaryNudgeNamesEvidencePaths(t *testing.T) {
	nudge := buildReadinessNudge(ReadinessNudge{
		Headline:          "The delegated read_only child result is now available as an evidence packet.",
		EvidencePathLabel: "Packet-named paths: ",
		EvidencePaths: []string{
			"internal/runtime/agent/discovery_redirect.go",
			"internal/capabilities/workspace/discovery_query.go",
		},
		Objective:         "Explain how this harness uses subagents.",
		Results:           nil,
		ProbeBudget:       2,
		ContinuationScope: "child_packet_available",
		FooterLines: []string{
			"Ignore packet artifacts (the `.idiom/discovery/queries/` JSON files) — they are not source paths.",
			"If you answer now, answer from the packet and inspected source evidence.",
		},
		IncludeProbeSuggestions: true,
	})
	for _, want := range []string{
		"Why a final answer is now valid:",
		"Packet-named paths:",
		"`internal/runtime/agent/discovery_redirect.go`",
		"normal tool policy",
		"named path, symbol, discovered handle, branch, body, or narrow line-range",
		"at most 2 targeted probes",
		"If you answer now, answer from the packet",
	} {
		if !strings.Contains(nudge, want) {
			t.Fatalf("expected child packet nudge to contain %q, got:\n%s", want, nudge)
		}
	}

	if strings.Contains(nudge, "`named_gap`") || strings.Contains(nudge, "`why_not_answering`") {
		t.Fatalf("child packet nudge must not expose read-only named-gap schema requirements, got:\n%s", nudge)
	}
}

func TestChildPacketBoundaryNudgeDisclosesActualProbeBudget(t *testing.T) {
	for _, tc := range []struct {
		budget int
		want   string
	}{
		{budget: 2, want: "at most 2 targeted probes"},
		{budget: 1, want: "one targeted probe"},
		{budget: 0, want: "answer unless the missing claim is exact and material"},
	} {
		nudge := buildReadinessNudge(ReadinessNudge{
			Headline:          "The delegated read_only child result is now available as an evidence packet.",
			EvidencePathLabel: "Packet-named paths: ",
			EvidencePaths:     []string{"internal/runtime/agent/runner.go"},
			Objective:         "q",
			Results:           nil,
			ProbeBudget:       tc.budget,
			ContinuationScope: "child_packet_available",
			FooterLines: []string{
				"Ignore packet artifacts (the `.idiom/discovery/queries/` JSON files) — they are not source paths.",
				"If you answer now, answer from the packet and inspected source evidence.",
			},
			IncludeProbeSuggestions: true,
		})
		if !strings.Contains(nudge, tc.want) {
			t.Fatalf("budget=%d: expected nudge to contain %q, got:\n%s", tc.budget, tc.want, nudge)
		}
	}
}

func TestChildPacketBoundaryNudgeFiltersNonFileArtifacts(t *testing.T) {
	nudge := buildReadinessNudge(ReadinessNudge{
		Headline:          "The delegated read_only child result is now available as an evidence packet.",
		EvidencePathLabel: "Packet-named paths: ",
		EvidencePaths: []string{
			".idiom/discovery/queries/query.json",
			"start_line/end_line",
			"internal/runtime/agent/runner_child_tasks.go",
			"idiom/internal/platform/config",
		},
		Objective:         "Explain how this harness uses subagents.",
		Results:           nil,
		ProbeBudget:       2,
		ContinuationScope: "child_packet_available",
		FooterLines: []string{
			"Ignore packet artifacts (the `.idiom/discovery/queries/` JSON files) — they are not source paths.",
			"If you answer now, answer from the packet and inspected source evidence.",
		},
		IncludeProbeSuggestions: true,
	})
	// The static `.idiom/discovery/queries/` string in the contract is a
	// reference to where artifacts live, not a path emitted from the
	// evidence list. The test should fail only if the *evidence* list
	// leaks artifacts — check the "Packet-named paths:" line directly.
	for _, line := range strings.Split(nudge, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "Packet-named paths:") {
			continue
		}

		if strings.Contains(line, "start_line/end_line") || strings.Contains(line, "idiom/internal/platform/config") || strings.Contains(line, ".idiom/discovery/queries/query.json") {
			t.Fatalf("expected packet-named paths line to drop non-file artifacts, got:\n%s", line)
		}
	}

	if !strings.Contains(nudge, "`internal/runtime/agent/runner_child_tasks.go`") {
		t.Fatalf("expected child packet nudge to keep real repository paths, got:\n%s", nudge)
	}
}

func TestDelegatedReadOnlyAnswerReadyNudgeIncludesClosureJustification(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"go_read_symbol","summary":"Read subagent flow","output_excerpt":"internal/runtime/agent/runner.go:648 runChildTask\ninternal/runtime/agent/runner_child_tasks.go:16 runChildTask\ninternal/runtime/agent/answer_composer.go:18 Compose"}]}`,
	}}
	nudge := buildReadinessNudge(ReadinessNudge{
		Headline:          "The delegated read-only evidence is sufficient for a grounded answer to this question.",
		EvidencePathLabel: "Core evidence paths: ",
		EvidencePaths:     closurePromptEvidencePaths(delegatedClosureEvidencePaths("Explain how this harness uses subagents.", results, 4), 4),
		Objective:         "Explain how this harness uses subagents.",
		Results:           results,
		ProbeBudget:       2,
		ContinuationScope: "delegated_read_only_answer_ready",
		FooterLines: []string{
			"Carry forward the inspected paths above and keep the answer minimal; avoid side-path comparison or mode-by-mode breakdown unless the question requires that contrast.",
		},
	})

	for _, want := range []string{
		"The delegated read-only evidence is sufficient for a grounded answer to this question.",
		"Why a final answer is now valid:",
		"Core evidence paths:",
		"Carry forward the inspected paths above",
		"normal tool policy",
		"one exact unresolved claim",
		"file-level raw reads",
		"`internal/runtime/agent/runner.go`",
	} {
		if !strings.Contains(nudge, want) {
			t.Fatalf("expected delegated readiness nudge to contain %q, got:\n%s", want, nudge)
		}
	}

	if strings.Contains(nudge, "`named_gap`") || strings.Contains(nudge, "`why_not_answering`") {
		t.Fatalf("delegated readiness nudge must not expose read-only named-gap schema requirements, got:\n%s", nudge)
	}
}

func TestClosureProbeCandidatesExtractCallLikeSignals(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"text_anchor_search","summary":"Found call sites","output_excerpt":"internal/runtime/agent/runner.go:574: result := r.runChildTask(ctx, req, opts)\ninternal/runtime/agent/runner_child_tasks.go:16: func (r *Runner) runChildTask(ctx context.Context, req workspace.ToolRequest, opts llama.ChatOptions) string"}]}`,
	}}
	candidates := closureProbeCandidates(results, 3)

	if !containsExactProbeCandidate(candidates, "runChildTask") {
		t.Fatalf("expected call-like discovery line to surface runChildTask, got %#v", candidates)
	}
}

func TestDelegatedClosureEvidencePathsPreserveDiscoveryPacketOrderAmongEqualPriorityFiles(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"text_anchor_search","summary":"Found handoff files","output_excerpt":"internal/runtime/agent/runner.go:574: result := r.runChildTask(ctx, req, opts)\ninternal/runtime/agent/runner_child_tasks.go:16: func (r *Runner) runChildTask(ctx context.Context, req workspace.ToolRequest, opts llama.ChatOptions) string\ninternal/runtime/agent/answer_composer.go:18: func (AnswerComposer) Compose(input AnswerComposerInput) (string, bool)"}]}`,
	}}
	paths := delegatedClosureEvidencePaths("Explain how this harness uses subagents.", results, 4)

	if len(paths) < 3 {
		t.Fatalf("expected multiple closure evidence paths, got %#v", paths)
	}

	if paths[0] != "internal/runtime/agent/runner.go" || paths[1] != "internal/runtime/agent/runner_child_tasks.go" {
		t.Fatalf("expected discovery-ranked runner paths to stay ahead of ancillary files, got %#v", paths)
	}
}

func TestDelegatedReadOnlyDiminishingReturnsNudgeEscalates(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"go_read_symbol","summary":"Read subagent flow","output_excerpt":"internal/runtime/agent/runner.go:648 runChildTask\ninternal/runtime/agent/runner_child_tasks.go:16 runChildTask"}]}`,
	}}
	first := delegatedReadOnlyDiminishingReturnsNudge("Explain how this harness uses subagents.", results, 1)
	second := delegatedReadOnlyDiminishingReturnsNudge("Explain how this harness uses subagents.", results, 2)

	if !strings.Contains(first, "Post-readiness probes used: 1") {
		t.Fatalf("expected first diminishing-returns nudge to count one used probe, got:\n%s", first)
	}

	if !strings.Contains(first, "`internal/runtime/agent/runner.go`") {
		t.Fatalf("expected first diminishing-returns nudge to surface inspected paths, got:\n%s", first)
	}

	if !strings.Contains(second, "Post-readiness probes used: 2") {
		t.Fatalf("expected second diminishing-returns nudge to count two used probes, got:\n%s", second)
	}

	if strings.Contains(strings.ToLower(first), "disables tools") || strings.Contains(strings.ToLower(second), "disables tools") {
		t.Fatalf("diminishing-returns nudge should stay advisory, got:\n%s\n---\n%s", first, second)
	}
}

func TestDelegatedReadOnlyReadRedirectUsesPriorSearchHitWindow(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "delegated_read_only",
			Objective:  "Explain how delegated child runners are created.\nKey terms: subagent, delegate, task",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:   "search_content",
			Output: "internal/runtime/agent/runner.go:574:\t\tresult := r.runChildTask(ctx, req, opts)\n",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "read_file", Path: "internal/runtime/agent/runner.go", StartLine: 9, EndLine: 109},
		Results: results,
	})

	if decision.Request.Tool != "read_file" || decision.Request.Path != "internal/runtime/agent/runner.go" {
		t.Fatalf("expected redirected hit-window read on runner.go, got %#v", decision.Request)
	}

	if decision.Request.StartLine != 554 || decision.Request.EndLine != 654 {
		t.Fatalf("expected redirected hit-window range, got %#v", decision.Request)
	}
}

func TestNormalizeReadOnlyChildPromptPreservesNamedScope(t *testing.T) {
	prompt := normalizeReadOnlyChildPrompt("How does this harness use sub-agents?\nNamed workspace scope: internal, cmd\nReturn inspected paths and key findings.")
	if !strings.Contains(prompt, "Named workspace scope: internal, cmd") {
		t.Fatalf("expected normalized child prompt to preserve named scope, got %q", prompt)
	}
}

func TestNormalizeReadOnlyChildPromptStripsBroadExplorationClauses(t *testing.T) {
	prompt := normalizeReadOnlyChildPrompt(strings.Join([]string{
		"How does this harness use sub-agents? Explore the cmd directory, main entry points, and look for patterns in startup flow.",
		"Focus on Go code in cmd/ and any agent-related packages.",
		"Named workspace scope: internal, cmd",
		"Return inspected paths and key findings.",
	}, "\n"))
	if !strings.HasPrefix(prompt, "How does this harness use sub-agents?") {
		t.Fatalf("expected normalized child prompt to keep the delegated question first, got %q", prompt)
	}

	for _, unwanted := range []string{"Explore the cmd directory", "main entry points", "Focus on Go code in cmd/"} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("expected normalized child prompt to strip broad exploration clause %q, got %q", unwanted, prompt)
		}
	}

	if !strings.Contains(prompt, "Named workspace scope: internal, cmd") {
		t.Fatalf("expected normalized child prompt to preserve named scope, got %q", prompt)
	}
}

func TestReadOnlyChildKeyTermsIncludeLaterSymbolHints(t *testing.T) {
	terms := readOnlyChildKeyTerms(strings.Join([]string{
		"Explain how delegated child runners are created.",
		"Focus on runChildTask and runWorkbranchChildTask.",
	}, "\n"))

	for _, want := range []string{"runchildtask", "runworkbranchchildtask"} {
		found := false

		for _, term := range terms {
			if term == want {
				found = true
				break
			}
		}

		if !found {
			t.Fatalf("expected key terms to preserve later symbol hint %q, got %#v", want, terms)
		}
	}
}

func TestBuildRunnerNextActionStatePrefersClosureAfterDelegatedReadiness(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "delegated_read_only",
		Objective:  "Explain how this harness uses subagents.",
	}
	results := []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran architecture_boundary discovery query",
			Output:  `{"artifact_path":".idiom/discovery/queries/query.json","evidence":[{"path":"internal/runtime/agent/runner.go","excerpt":"completion gate"},{"path":"internal/runtime/agent/runner_child_tasks.go","excerpt":"task child delegated_read_only"}]}`,
		},
		{
			Tool:    "search_content",
			Summary: "Found 10 matching line(s)",
			Output:  "internal/runtime/agent/runner.go:2228: completion gate\ninternal/runtime/agent/runner.go:2247: final answer rejected\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/runtime/agent/runner.go lines 2200-2260",
			Output:  "package agent\nfunc finish() {}\n",
		},
	}
	state := buildRunnerNextActionState(
		"child:explorer",
		3,
		"tool_result",
		workspace.ToolRequest{Tool: "search_content", Path: "internal/runtime/agent/runner.go"},
		results[2],
		results,
		false,
		brief,
	)

	if !slices.Contains(state.Constraints, "answer_readiness_reached") {
		t.Fatalf("expected delegated readiness closure bias, got %#v", state)
	}

	if slices.Contains(state.Constraints, "completion_gate_active") {
		t.Fatalf("search output should not fabricate completion gate state, got %#v", state)
	}

	if slices.Contains(state.InvalidNextActions, "final_answer_without_satisfying_completion_gate") {
		t.Fatalf("closure-biased state should not block answering on a fabricated gate, got %#v", state)
	}

	joinedNext := strings.Join(state.NextValidActions, "\n")
	if !strings.Contains(joinedNext, "final answer from the evidence already gathered is available") {
		t.Fatalf("expected answer availability guidance, got %#v", state.NextValidActions)
	}

	if !strings.Contains(joinedNext, "one exact missing claim") {
		t.Fatalf("expected exact-probe option to remain visible, got %#v", state.NextValidActions)
	}

	for _, want := range []string{
		"internal/runtime/agent/runner.go",
		"internal/runtime/agent/runner_child_tasks.go",
	} {
		if !slices.Contains(state.EvidencePaths, want) {
			t.Fatalf("expected delegated closure state to preserve evidence path %q, got %#v", want, state.EvidencePaths)
		}
	}
}

func TestBuildRunnerNextActionStatePrefersClosureAfterChildPacket(t *testing.T) {
	state := buildRunnerNextActionState(
		"main",
		1,
		"child_task_result",
		workspace.ToolRequest{Tool: "task", Mode: "read_only", Agent: "explorer"},
		workspace.ToolResult{Tool: "task", Summary: "Child task completed", Output: "Source-body evidence paths:\n- internal/runtime/agent/runner.go\n\nKey findings:\n- The inspected runner source explains how this harness uses subagents through delegated child tasks.\n\nInspected paths: internal/runtime/agent/runner.go"},
		[]workspace.ToolResult{{Tool: "task", Summary: "Child task completed", Output: "Source-body evidence paths:\n- internal/runtime/agent/runner.go\n\nKey findings:\n- The inspected runner source explains how this harness uses subagents through delegated child tasks.\n\nInspected paths: internal/runtime/agent/runner.go"}},
		false,
		workerruntime.WorkerRuntimeBrief{TaskFamily: "architecture", Objective: "Explain how subagents work."},
	)
	if !slices.Contains(state.Constraints, "child_packet_available") {
		t.Fatalf("expected child-packet closure bias, got %#v", state)
	}

	joinedNext := strings.Join(state.NextValidActions, "\n")
	if !strings.Contains(joinedNext, "final answer from the child packet evidence is available") {
		t.Fatalf("expected child packet answer availability guidance, got %#v", state.NextValidActions)
	}

	if !strings.Contains(joinedNext, "one exact missing claim") {
		t.Fatalf("expected child packet exact-probe guidance, got %#v", state.NextValidActions)
	}

	for _, forbidden := range []string{
		"broad rediscovery outside packet-named paths or symbols after the child packet",
		"README/docs or repo-wide search outside packet-named paths or symbols after the child packet",
	} {
		if slices.Contains(state.InvalidNextActions, forbidden) {
			t.Fatalf("child packet must not become an invalid-action allowlist %q: %#v", forbidden, state.InvalidNextActions)
		}
	}
}

func TestBuildRunnerNextActionStatePrefersClosureAfterTypedChildPacket(t *testing.T) {
	result := typedReadOnlyTaskResult(ReadOnlyEvidencePacket{
		Objective:       "Explain how subagents work.",
		Question:        "Explain how subagents work.",
		SourceBodyPaths: []string{"internal/runtime/agent/runner.go", "internal/runtime/agent/runner_child_tasks.go"},
		InspectedPaths:  []string{"internal/runtime/agent/runner.go", "internal/runtime/agent/runner_child_tasks.go", ".idiom/discovery/queries/query.json"},
		Confidence:      "high",
		Findings: []ReadOnlyEvidenceFinding{
			{Path: "internal/runtime/agent/runner.go", Text: "The harness uses the `task` tool for delegated subagent read-only work."},
			{Path: "internal/runtime/agent/runner_child_tasks.go", Text: "`runChildTask` constructs the subagent child runner and returns the child packet."},
		},
	})
	state := buildRunnerNextActionState(
		"main",
		1,
		"child_task_result",
		workspace.ToolRequest{Tool: "task", Mode: "read_only", Agent: "explorer"},
		result,
		[]workspace.ToolResult{result},
		false,
		workerruntime.WorkerRuntimeBrief{TaskFamily: "architecture", Objective: "Explain how subagents work.", PermissionProfile: "read_only_workspace"},
	)

	if !slices.Contains(state.Constraints, "child_packet_available") {
		t.Fatalf("expected typed child-packet closure bias, got %#v", state)
	}

	joinedNext := strings.Join(state.NextValidActions, "\n")
	if !strings.Contains(joinedNext, "final answer from the child packet evidence is available") {
		t.Fatalf("expected typed packet answer availability guidance, got %#v", state.NextValidActions)
	}

	if !strings.Contains(joinedNext, "one exact missing claim") {
		t.Fatalf("expected typed packet exact-probe guidance, got %#v", state.NextValidActions)
	}

	for _, want := range []string{"internal/runtime/agent/runner.go", "internal/runtime/agent/runner_child_tasks.go"} {
		if !slices.Contains(state.EvidencePaths, want) {
			t.Fatalf("expected typed packet evidence path %q, got %#v", want, state.EvidencePaths)
		}
	}

	if slices.Contains(state.EvidencePaths, ".idiom/discovery/queries/query.json") {
		t.Fatalf("typed packet should not expose generated artifacts as evidence paths: %#v", state.EvidencePaths)
	}
}

func TestFinalAnswerDecoratorSynthesizesTypedChildPacketFindings(t *testing.T) {
	result := typedReadOnlyTaskResult(ReadOnlyEvidencePacket{
		Objective:       "Explain how subagents work.",
		Question:        "Explain how subagents work.",
		SourceBodyPaths: []string{"internal/runtime/agent/runner.go", "internal/runtime/agent/runner_child_tasks.go"},
		InspectedPaths:  []string{"internal/runtime/agent/runner.go", "internal/runtime/agent/runner_child_tasks.go"},
		Confidence:      "high",
		Findings: []ReadOnlyEvidenceFinding{
			{Path: "internal/runtime/agent/runner.go", Text: "The parent runner delegates subagent read-only exploration through the `task` tool"},
			{Path: "internal/runtime/agent/runner_child_tasks.go", Text: "`runChildTask` constructs the subagent child runner and returns the typed evidence packet"},
		},
	})
	runner := &Runner{}
	answer := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     "Question: Explain how subagents work.\nSource: delegated read-only evidence packet.\n\nInspected paths:\n- internal/runtime/agent/runner.go\n\nKey findings:\n- packet headings should not leak",
		StopReason: "post_child_read_budget_exhausted",
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain how subagents work.",
		},
		Results:    []workspace.ToolResult{result},
		UserFacing: true,
	})

	for _, want := range []string{"delegates subagent read-only exploration", "returns the typed evidence packet", "`internal/runtime/agent/runner.go`", "`internal/runtime/agent/runner_child_tasks.go`"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected typed finding or path %q in final answer, got:\n%s", want, answer)
		}
	}

	for _, forbidden := range []string{"Source: delegated read-only evidence packet", "Inspected paths:", "Key findings:", "What this establishes:", "Use those findings as the source of truth"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("final synthesis leaked packet/scaffold heading %q:\n%s", forbidden, answer)
		}
	}
}

func TestBuildRunnerNextActionStateDoesNotCloseOnUnrelatedChildPacket(t *testing.T) {
	result := workspace.ToolResult{
		Tool:    "task",
		Summary: "Child task completed",
		Output:  "Question: explain labels\nSource-body evidence paths:\n- internal/labels/labels.go\n\nKey findings:\n- ShortLabel returns Save changes.",
	}
	state := buildRunnerNextActionState(
		"main",
		1,
		"child_task_result",
		workspace.ToolRequest{Tool: "task", Mode: "read_only", Agent: "explorer"},
		result,
		[]workspace.ToolResult{result},
		false,
		workerruntime.WorkerRuntimeBrief{TaskFamily: "architecture", Objective: "Explain how subagents work."},
	)

	if slices.Contains(state.Constraints, "child_packet_available") {
		t.Fatalf("unrelated child source-body packet must not satisfy closure, got %#v", state)
	}
}

func TestBuildRunnerNextActionStateDoesNotInventChildPacketForDirectReadOnlyEvidence(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/evidence/transcript/debuglog.go lines 1-80 (total lines: 720)",
		Output:  "package transcript\nconst DebugLogModeRedacted DebugLogMode = \"redacted\"\nfunc redactSecrets() {}\n",
	}}
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "What does the debug log redacted mode do?",
	}
	state := buildRunnerNextActionState(
		"main",
		1,
		"tool_result",
		workspace.ToolRequest{Tool: "read_file", Path: "internal/evidence/transcript/debuglog.go", StartLine: 1, EndLine: 80},
		results[0],
		results,
		false,
		brief,
	)

	if slices.Contains(state.Constraints, "child_packet_available") {
		t.Fatalf("ordinary direct reads must not fabricate child-packet closure, got %#v", state)
	}

	for _, action := range state.NextValidActions {
		if strings.Contains(action, "child packet") {
			t.Fatalf("ordinary direct reads must not produce child-packet guidance, got %#v", state.NextValidActions)
		}
	}

	readiness := runnerReadinessState("main", brief, results)
	if readiness == nil || !readiness.ReadyToAnswer {
		t.Fatalf("expected normal readiness, got %#v", readiness)
	}

	if slices.Contains(readiness.ClosedClaims, "child_packet_available") {
		t.Fatalf("readiness must not close child_packet_available without a task packet, got %#v", readiness)
	}
}

func TestReadOnlyAnalysisExactProbeCountIncludesCodeNavigateSourceBody(t *testing.T) {
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed file_outline via go_file_outline",
			Output:  "capability: file_outline\nroute_summary: Outlined internal/evidence/transcript/debuglog.go",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "capability: read_symbol\nroute_summary: Read DebugLogger.SetMode in internal/evidence/transcript/debuglog.go lines 52-61\n\n52:func (d *DebugLogger) SetMode(mode DebugLogMode) {}",
		},
	}
	if got := readOnlyAnalysisExactProbeCount(results); got != 1 {
		t.Fatalf("exact probe count = %d, want 1", got)
	}
}
