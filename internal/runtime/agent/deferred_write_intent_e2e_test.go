package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.It("records multi-path prerequisite intent and executes only a fresh validated request", ginkgo.Label("e2e", "integration", "completion_recovery_development"), func() {
	const (
		busyPath      = "internal/surfaces/tui/app/busy_status.go"
		renderingPath = "internal/surfaces/tui/app/rendering.go"
		runnerPath    = "internal/runtime/agent/runner_core.go"
		busyTestPath  = "internal/surfaces/tui/app/busy_status_test.go"
	)
	incidentPathOrder := []string{busyPath, renderingPath, runnerPath, busyTestPath}
	canonicalPaths := []string{runnerPath, busyPath, busyTestPath, renderingPath}

	root := ginkgo.GinkgoT().TempDir()
	initial := map[string]string{
		"go.mod":      "module example.com/deferred-intent\n\ngo 1.22\n",
		busyPath:      "package app\n\nconst busyLabel = \"Busy\"\nconst busyDetail = \"pending\"\n",
		renderingPath: "package app\n\nconst renderLabel = \"Render\"\n",
		runnerPath:    "package agent\n\nconst runnerLabel = \"Runner\"\n",
		busyTestPath:  "package app\n\nimport \"testing\"\n\nconst busyTestLabel = \"Busy test\"\n\nfunc TestBusyLabel(t *testing.T) {\n\tif busyTestLabel == \"\" {\n\t\tt.Fatal(\"busy label must not be empty\")\n\t}\n}\n",
	}
	for path, content := range initial {
		gomega.Expect(writeTestFile(root, path, content)).To(gomega.Succeed())
	}
	initRunnerTestGitRepo(ginkgo.GinkgoT(), root)
	manager := workspace.NewManager(config.Workspace{
		Root:         root,
		CWD:          root,
		EnableWrites: true,
		EnableShell:  true,
		Shell:        "/bin/sh",
	})

	originalPatch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: " + busyPath,
		"@@",
		"-const busyLabel = \"Busy\"",
		"+const busyLabel = \"Working\"",
		"*** Update File: " + renderingPath,
		"@@",
		"-const renderLabel = \"Render\"",
		"+const renderLabel = \"Rendering\"",
		"*** Update File: " + runnerPath,
		"@@",
		"-const runnerLabel = \"Runner\"",
		"+const runnerLabel = \"Agent runner\"",
		"*** Update File: " + busyTestPath,
		"@@",
		"-const busyTestLabel = \"Busy test\"",
		"+const busyTestLabel = \"Working test\"",
		"*** End Patch",
	}, "\n")
	previousDirective := -1
	for _, path := range incidentPathOrder {
		directive := strings.Index(originalPatch, "*** Update File: "+path)
		gomega.Expect(directive).To(gomega.BeNumerically(">", previousDirective), "the fixture must retain the incident's original four-path request order")
		previousDirective = directive
	}
	analysis, err := manager.AnalyzeMutationRequest(workspace.ToolRequest{
		Tool:    workspace.ToolNameApplyPatch,
		Content: originalPatch,
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(mutationRequestCanonicalTargetIDs(analysis)).To(gomega.Equal(canonicalPaths), "the workspace analysis owner must derive stable canonical identity ordering")
	laterBusyPatch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: " + busyPath,
		"@@",
		"-const busyDetail = \"pending\"",
		"+const busyDetail = \"active\"",
		"*** End Patch",
	}, "\n")
	refinedPatch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: " + busyPath,
		"@@",
		"-const busyLabel = \"Busy\"",
		"-const busyDetail = \"pending\"",
		"+const busyLabel = \"Working\"",
		"+const busyDetail = \"active\"",
		"*** Update File: " + renderingPath,
		"@@",
		"-const renderLabel = \"Render\"",
		"+const renderLabel = \"Rendering\"",
		"*** Update File: " + runnerPath,
		"@@",
		"-const runnerLabel = \"Runner\"",
		"+const runnerLabel = \"Agent runner\"",
		"*** Update File: " + busyTestPath,
		"@@",
		"-const busyTestLabel = \"Busy test\"",
		"+const busyTestLabel = \"Working test\"",
		"*** End Patch",
	}, "\n")
	finalAnswer := strings.Join([]string{
		"Updated the runner and TUI busy-state lifecycle.",
		"",
		"Changed paths:",
		"- " + busyPath,
		"- " + renderingPath,
		"- " + runnerPath,
		"- " + busyTestPath,
		"",
		"Verification: go test ./... passed.",
	}, "\n")
	patchCall := func(content, description string) string {
		return fmt.Sprintf(`{"tool":"apply_patch","content":%q,"description":%q}`, content, description)
	}
	replies := []string{
		patchCall(originalPatch, "update the runner and TUI busy-state lifecycle"),
		fmt.Sprintf(`{"tool":"read_file","path":%q,"start_line":1,"end_line":10,"named_gap":"source-body evidence for the original multi-path change"}`, runnerPath),
		fmt.Sprintf(`{"tool":"read_file","path":%q,"start_line":1,"end_line":10,"named_gap":"source-body evidence for the original multi-path change"}`, busyPath),
		fmt.Sprintf(`{"tool":"read_file","path":%q,"start_line":1,"end_line":10,"named_gap":"source-body evidence for the original multi-path change"}`, busyTestPath),
		fmt.Sprintf(`{"tool":"read_file","path":%q,"start_line":1,"end_line":10,"named_gap":"source-body evidence for the original multi-path change"}`, renderingPath),
		patchCall(originalPatch, "update the runner and TUI busy-state lifecycle"),
		fmt.Sprintf(`{"tool":"history_range","path":%q,"start_line":1,"end_line":10,"named_gap":"git history rationale for the original runner change"}`, runnerPath),
		patchCall(laterBusyPatch, "refine the busy status detail"),
		fmt.Sprintf(`{"tool":"history_range","path":%q,"start_line":1,"end_line":10,"named_gap":"git history rationale for the later busy-status intent"}`, busyPath),
		patchCall(refinedPatch, "apply the complete fresh multi-path change"),
		fmt.Sprintf(`{"tool":"history_range","path":%q,"start_line":1,"end_line":10,"named_gap":"git history rationale for the busy-status test"}`, busyTestPath),
		fmt.Sprintf(`{"tool":"history_range","path":%q,"start_line":1,"end_line":10,"named_gap":"git history rationale for rendering"}`, renderingPath),
		patchCall(refinedPatch, "apply the complete fresh multi-path change"),
		`{"tool":"shell","command":"go test ./...","workdir":"."}`,
		finalAnswer,
	}
	server, requestBodies := newScriptedChatServer(replies)

	type observedResult struct {
		Request workspace.ToolRequest
		Result  workspace.ToolResult
	}
	var (
		requests []workspace.ToolRequest
		results  []observedResult
		events   []RunEvent
	)
	runner := &Runner{
		Client:    llama.NewClient(server.URL(), 5*time.Second),
		Workspace: manager,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:                 "implement",
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			Objective:            "Update the runner and TUI busy-state lifecycle across the selected source and test files.",
			PermissionProfile:    "guarded_workspace_write",
			AllowedTools:         []string{workspace.ToolNameReadFile, workspace.ToolNameApplyPatch, workspace.ToolNameHistoryRange, workspace.ToolNameHistorySymbol, workspace.ToolNameShell},
			AllowedEditPaths:     []string{busyPath, renderingPath, runnerPath, busyTestPath},
			RequiredEvidence:     []string{string(workerruntime.RequiredEvidenceGitHistoryRationale), "changed_paths_or_noop_reason", "verification_status"},
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		},
		Policy: RunPolicy{MaxToolSteps: 18, MaxNoProgressTurns: 8},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	answer, err := runner.Run(
		workspace.WithExecutionSessionID(context.Background(), "deferred-multi-path"),
		[]llama.ChatMessage{{Role: "user", Content: runner.RuntimeBrief.Objective}},
		llama.ChatOptions{},
		nil,
		func(event ToolRequestEvent) {
			requests = append(requests, event.Request)
		},
		func(event ToolResultEvent) {
			results = append(results, observedResult{Request: event.Request, Result: event.Result})
		},
	)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	lifecycle := make([]string, 0, len(results))
	for _, observed := range results {
		facts := observed.Result.Facts
		var sourcePending, historyPending []string
		if facts != nil {
			sourcePending = facts.PendingSourceBodyPaths
			historyPending = facts.PendingHistoryPaths
		}
		lifecycle = append(lifecycle, fmt.Sprintf(
			"%s code=%s error=%q source=%v history=%v changed=%v",
			observed.Result.Tool,
			observed.Result.Code,
			strings.TrimSpace(observed.Result.Error),
			sourcePending,
			historyPending,
			observed.Result.ChangedPaths,
		))
	}
	gomega.Expect(answer).To(gomega.Equal(finalAnswer), "lifecycle:\n%s", strings.Join(lifecycle, "\n"))
	gomega.Expect(requestBodies()).To(gomega.HaveLen(len(replies)))

	var (
		historyPaths        []string
		executedPatchBodies []string
		deferredPathSets    [][]string
		pendingSourceSets   [][]string
		pendingHistorySets  [][]string
	)
	for _, request := range requests {
		switch request.Tool {
		case workspace.ToolNameHistoryRange:
			historyPaths = append(historyPaths, NormalizeRunnerPath(request.Path))
		}
	}
	for _, observed := range results {
		if observed.Request.Tool == workspace.ToolNameApplyPatch && workspaceWriteResultCommitted(observed.Request, observed.Result) {
			executedPatchBodies = append(executedPatchBodies, observed.Request.Content)
		}
		facts := observed.Result.Facts
		if observed.Result.Tool == workspace.ToolNameApplyPatch && observed.Result.Error != "" && facts != nil && facts.RejectionDisposition == toolfacts.ToolRejectionDeferredByPrerequisite {
			deferredPathSets = append(deferredPathSets, append([]string(nil), facts.RejectedWritePaths...))
			if len(facts.PendingSourceBodyPaths) > 0 {
				pendingSourceSets = append(pendingSourceSets, append([]string(nil), facts.PendingSourceBodyPaths...))
			}
			if len(facts.PendingHistoryPaths) > 0 {
				pendingHistorySets = append(pendingHistorySets, append([]string(nil), facts.PendingHistoryPaths...))
			}
		}
	}

	gomega.Expect(historyPaths).To(gomega.Equal([]string{runnerPath, busyPath, busyTestPath, renderingPath}))
	gomega.Expect(executedPatchBodies).To(gomega.Equal([]string{refinedPatch}), "rejected requests must never execute from stored request state")
	gomega.Expect(deferredPathSets).NotTo(gomega.BeEmpty())
	gomega.Expect(deferredPathSets[0]).To(gomega.Equal(canonicalPaths), "the original request must retain all four typed paths under canonical identity ordering")
	gomega.Expect(deferredPathSets).To(gomega.ContainElement([]string{busyPath}), "later same-path intent must append without overwriting the original")
	gomega.Expect(pendingSourceSets).To(gomega.Equal([][]string{canonicalPaths}), "the first rejection must expose every exact source-body prerequisite")
	gomega.Expect(pendingHistorySets).To(gomega.Equal([][]string{
		canonicalPaths,
		{busyPath},
		{busyTestPath, renderingPath},
	}), "history prerequisites must contract only after exact per-path evidence")

	resultLedger := make([]workspace.ToolResult, 0, len(results))
	for _, observed := range results {
		resultLedger = append(resultLedger, observed.Result)
	}
	gomega.Expect(unresolvedRejectedMutationPaths(resultLedger, nil)).To(gomega.BeEmpty(), "fresh changed-path success must resolve the typed intent ledger")

	wants := map[string][]string{
		busyPath:      {`const busyLabel = "Working"`, `const busyDetail = "active"`},
		renderingPath: {`const renderLabel = "Rendering"`},
		runnerPath:    {`const runnerLabel = "Agent runner"`},
		busyTestPath:  {`const busyTestLabel = "Working test"`},
	}
	for path, snippets := range wants {
		body, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		gomega.Expect(readErr).NotTo(gomega.HaveOccurred())
		for _, snippet := range snippets {
			gomega.Expect(string(body)).To(gomega.ContainSubstring(snippet), path)
		}
	}

	gomega.Expect(slices.Contains(executedPatchBodies, originalPatch)).To(gomega.BeFalse())
	gomega.Expect(slices.Contains(executedPatchBodies, laterBusyPatch)).To(gomega.BeFalse())
	for _, event := range events {
		if event.Role != "tool_request_rewrite" {
			continue
		}
		gomega.Expect(event.Content).NotTo(gomega.ContainSubstring(`"original_tool":"apply_patch"`), "write prerequisites must never rewrite a native mutation call into evidence gathering")
	}
})

var _ = ginkgo.It("executes a valid history-phase source read unchanged and accepts a fresh plan after explicit history", ginkgo.Label("e2e", "integration", "completion_recovery_development"), func() {
	const target = "a.go"
	root := ginkgo.GinkgoT().TempDir()
	gomega.Expect(writeTestFile(root, target, "package a\n\nfunc Label() string { return \"Long label\" }\n")).To(gomega.Succeed())
	initRunnerTestGitRepo(ginkgo.GinkgoT(), root)

	planRequest := `{"tool":"submit_plan","rationale":"Shorten the established label after reviewing its history.","edits":[{"path":"a.go","old_text":"return \"Long label\"","new_text":"return \"Short label\"","purpose":"shorten the label"}]}`
	finalAnswer := "Submitted the executable edit plan for a.go after reviewing the current source and its history."
	replies := []string{
		`{"tool":"read_file","path":"a.go","start_line":1,"end_line":3,"named_gap":"read the current label implementation"}`,
		`{"tool":"read_file","path":"a.go","start_line":1,"end_line":3,"named_gap":"repeat source read while history is pending"}`,
		`{"tool":"history_range","path":"a.go","start_line":1,"end_line":3,"named_gap":"git history rationale for the established label"}`,
		planRequest,
		finalAnswer,
	}
	server, requestBodies := newScriptedChatServer(replies)

	var (
		results                 []workspace.ToolResult
		executedTools           []string
		sawSourceHistoryRewrite bool
	)
	runner := &Runner{
		Client: llama.NewClient(server.URL(), 5*time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			Objective:         "Shorten the established label in a.go.",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools: []string{
				workspace.ToolNameReadFile,
				workspace.ToolNameHistoryRange,
				workspace.ToolNameHistorySymbol,
				workspace.ToolNameSubmitPlan,
			},
			RequiredEditPaths: []string{target},
			RequiredEvidence:  []string{string(workerruntime.RequiredEvidenceGitHistoryRationale)},
		},
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 6},
		OnEvent: func(event RunEvent) {
			if event.Role == "tool_request_rewrite" &&
				strings.Contains(event.Content, `"original_tool":"read_file"`) &&
				strings.Contains(event.Content, `"rewritten_tool":"history_range"`) {

				sawSourceHistoryRewrite = true
			}
		},
	}

	answer, err := runner.Run(
		workspace.WithExecutionSessionID(context.Background(), "deferred-plan-history"),
		[]llama.ChatMessage{{Role: "user", Content: runner.RuntimeBrief.Objective}},
		llama.ChatOptions{},
		nil,
		func(event ToolRequestEvent) {
			executedTools = append(executedTools, event.Request.Tool)
		},
		func(event ToolResultEvent) {
			results = append(results, event.Result)
		},
	)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(answer).To(gomega.Equal(finalAnswer), "results: %#v", results)
	gomega.Expect(requestBodies()).To(gomega.HaveLen(len(replies)))
	gomega.Expect(sawSourceHistoryRewrite).To(gomega.BeFalse())
	gomega.Expect(executedTools).To(gomega.Equal([]string{
		workspace.ToolNameReadFile,
		workspace.ToolNameReadFile,
		workspace.ToolNameHistoryRange,
		workspace.ToolNameSubmitPlan,
	}), "every valid read must execute unchanged before the model explicitly requests history")
})
