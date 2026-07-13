package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	contractcompile "idiom/internal/contracts/compile"
	plancontract "idiom/internal/contracts/plan"
	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/config"
	"idiom/internal/platform/failures"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
	"idiom/internal/workflows/planintent"
)

// TestQwenKeyValueSimpleFormat verifies the exact format Qwen emits
// (from live curl test) parses through the normalizer.
var _ = ginkgo.It("QwenKeyValueSimpleFormat", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: "tool: read_file\npath: internal/repository/comprehension/pack.go\n",
	})
	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("parse error: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected key-value tool request to normalize", 1)
	}

	if result.Request.Tool != "read_file" || result.Request.Path != "internal/repository/comprehension/pack.go" {
		ginkgo.Fail(fmt.Sprintf("unexpected request: tool=%q path=%q", result.Request.Tool, result.Request.Path), 1)
	}

	if result.EventTransport != toolRequestNormalizerTransportKeyValue {
		ginkgo.Fail(fmt.Sprintf("expected key_value transport, got %q", result.EventTransport), 1)
	}
})

var _ = ginkgo.It("LanePreflightPreservesFreshAuthorizedReadIdentity", func() {
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: string(workerruntime.PermissionGuardedWorkspaceWrite),
	}})
	req := workspace.ToolRequest{
		Tool:      workspace.ToolNameReadFile,
		Path:      "internal/runtime/agent/runner_core.go",
		StartLine: 120,
		EndLine:   180,
		NamedGap:  "inspect terminal acceptance without changing the requested target",
	}
	raw := `{"tool":"read_file","path":"internal/runtime/agent/runner_core.go","start_line":120,"end_line":180,"named_gap":"inspect terminal acceptance without changing the requested target"}`

	decision := runner.applyLanePreflight(runnerLanePreflightInput{Request: req, Raw: raw})
	gomega.Expect(decision.Request).To(gomega.Equal(req))
	gomega.Expect(decision.Raw).To(gomega.Equal(raw))
	gomega.Expect(decision.RejectSummary).To(gomega.BeEmpty())
	gomega.Expect(decision.RejectError).To(gomega.BeEmpty())
	gomega.Expect(decision.ForceAnswer).To(gomega.BeNil())
})

var _ = ginkgo.It("CompiledPlannerKeepsPermissionAuthorizedExternalInspection", func() {
	root := ginkgo.GinkgoT().TempDir()
	script := filepath.Join(root, "lookup.sh")
	gomega.Expect(os.WriteFile(script, []byte("#!/bin/sh\necho ok\n"), 0o755)).To(gomega.Succeed())

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.ExternalTools = []config.ExternalToolDef{
		{Name: "lookup_tool", Command: script, ReadOnly: true, Discovery: true},
		{Name: "mutating_tool", Command: script, ReadOnly: false, Discovery: true},
	}
	setup := workspace.ConfigureToolSurface(context.Background(), cfg, workspace.ConfiguredToolSurfaceOptions{Runtime: true})
	defer setup.Close()

	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: string(workerruntime.PermissionGuardedWorkspaceWrite),
		AllowedTools:      []string{workspace.ToolNameReadFile, workspace.ToolNameSubmitPlan, "lookup_tool", "mutating_tool"},
	}
	runner := newRunnerRunFixture(&Runner{Workspace: setup.Manager, RuntimeBrief: brief})
	protocol, ok := runner.compileModelProtocolProjection(brief, nil, TurnPhaseContract{}, RunPolicy{}, false)
	gomega.Expect(ok).To(gomega.BeTrue())
	gomega.Expect(protocol.VisibleTools).To(gomega.ContainElements(workspace.ToolNameReadFile, workspace.ToolNameSubmitPlan, "lookup_tool"))
	gomega.Expect(protocol.CallableTools).To(gomega.ContainElements(workspace.ToolNameReadFile, workspace.ToolNameSubmitPlan, "lookup_tool"))
	gomega.Expect(protocol.VisibleTools).NotTo(gomega.ContainElement("mutating_tool"))
	gomega.Expect(protocol.CallableTools).NotTo(gomega.ContainElement("mutating_tool"))
})

var _ = ginkgo.It("SourcePrerequisiteRejectionCarriesExactPendingPathsWithoutRequest", func() {
	rejection := sourcePrerequisiteRejection(
		"Required source evidence pending",
		"issue a fresh model-selected request",
		[]string{"internal/runtime/agent/runner_core.go", "./internal/runtime/agent/plan_execute.go"},
	)

	gomega.Expect(rejection.Disposition).To(gomega.Equal(toolfacts.ToolRejectionDeferredByPrerequisite))
	gomega.Expect(rejection.Facts).NotTo(gomega.BeNil())
	gomega.Expect(rejection.Facts.PendingSourceBodyPaths).To(gomega.Equal([]string{
		"internal/runtime/agent/plan_execute.go",
		"internal/runtime/agent/runner_core.go",
	}))
	gomega.Expect(rejection.Facts.RejectionDisposition).To(gomega.Equal(toolfacts.ToolRejectionDeferredByPrerequisite))
})

var _ = ginkgo.It("QwenKeyValueEditFileFormat", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: strings.Join([]string{
			"tool: edit_file",
			"path: internal/labels/labels.go",
			`old_text: return "Save changes"`,
			`new_text: return "Save"`,
			"description: update label",
		}, "\n"),
	})
	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("parse error: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected key-value edit_file request to normalize", 1)
	}

	if result.Request.Tool != "edit_file" || result.Request.Path != "internal/labels/labels.go" {
		ginkgo.Fail(fmt.Sprintf("unexpected request: tool=%q path=%q", result.Request.Tool, result.Request.Path), 1)
	}

	if result.Request.OldText != `return "Save changes"` || result.Request.NewText != `return "Save"` {
		ginkgo.Fail(fmt.Sprintf("edit anchors not parsed: old=%q new=%q", result.Request.OldText, result.Request.NewText), 1)
	}

	if result.EventTransport != toolRequestNormalizerTransportKeyValue {
		ginkgo.Fail(fmt.Sprintf("expected key_value transport, got %q", result.EventTransport), 1)
	}
})

var _ = ginkgo.It("QwenKeyValueEditFilePreservesBracesAndIndentation", func() {
	oldText := strings.Join([]string{
		"func label() {",
		"    return \"Save changes\"",
		"}",
	}, "\n")
	newText := strings.Join([]string{
		"func label() {",
		"    return \"Save\"",
		"}",
	}, "\n")
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: strings.Join([]string{
			"tool: edit_file",
			"path: internal/labels/labels.go",
			"old_text: func label() {",
			"    return \"Save changes\"",
			"}",
			"new_text: func label() {",
			"    return \"Save\"",
			"}",
			"description: update label",
		}, "\n"),
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("parse error: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected key-value edit_file request with braces to normalize", 1)
	}

	if result.Request.OldText != oldText {
		ginkgo.Fail(fmt.Sprintf("old_text not preserved:\nwant: %q\ngot:  %q", oldText, result.Request.OldText), 1)
	}

	if result.Request.NewText != newText {
		ginkgo.Fail(fmt.Sprintf("new_text not preserved:\nwant: %q\ngot:  %q", newText, result.Request.NewText), 1)
	}
})

var _ = ginkgo.It("QwenKeyValueEditFilePreservesAllowedKeyLookingAnchorLines", func() {
	oldText := strings.Join([]string{
		"metadata := []string{",
		"path: internal/foo",
		"content: old value",
		"}",
	}, "\n")
	newText := strings.Join([]string{
		"metadata := []string{",
		"path: internal/foo",
		"content: new value",
		"}",
	}, "\n")
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: strings.Join([]string{
			"tool: edit_file",
			"path: internal/labels/labels.go",
			"old_text: metadata := []string{",
			"path: internal/foo",
			"content: old value",
			"}",
			"new_text: metadata := []string{",
			"path: internal/foo",
			"content: new value",
			"}",
			"description: update metadata",
		}, "\n"),
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("parse error: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected key-value edit_file request with key-looking anchor lines to normalize", 1)
	}

	if result.Request.OldText != oldText {
		ginkgo.Fail(fmt.Sprintf("old_text not preserved:\nwant: %q\ngot:  %q", oldText, result.Request.OldText), 1)
	}

	if result.Request.NewText != newText {
		ginkgo.Fail(fmt.Sprintf("new_text not preserved:\nwant: %q\ngot:  %q", newText, result.Request.NewText), 1)
	}
})

var _ = ginkgo.It("QwenKeyValueEditFileAllowsEmptyNewTextDeletion", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: strings.Join([]string{
			"tool: edit_file",
			"path: internal/labels/labels.go",
			`old_text: return "Save changes"`,
			"new_text:",
			"description: delete label",
		}, "\n"),
	})
	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("parse error: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected key-value edit_file deletion request to normalize", 1)
	}

	if result.Request.OldText != `return "Save changes"` {
		ginkgo.Fail(fmt.Sprintf("old_text not parsed: %q", result.Request.OldText), 1)
	}

	if result.Request.NewText != "" {
		ginkgo.Fail(fmt.Sprintf("empty new_text should be preserved, got %q", result.Request.NewText), 1)
	}

	if result.EventTransport != toolRequestNormalizerTransportKeyValue {
		ginkgo.Fail(fmt.Sprintf("expected key_value transport, got %q", result.EventTransport), 1)
	}
})

// TestQwenKeyValueMultiCallParsesFirst verifies that when Qwen emits
// two tool: blocks in one reply, only the first is parsed.
var _ = ginkgo.It("QwenKeyValueMultiCallParsesFirst", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: strings.Join([]string{
			"tool: read_file",
			"path: internal/repository/comprehension/pack.go",
			"lines: 1-30",
			"",
			"tool: search_content",
			"pattern: Packet",
		}, "\n"),
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("parse error: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected key-value tool request to normalize (first tool only)", 1)
	}

	if result.Request.Tool != "read_file" {
		ginkgo.Fail(fmt.Sprintf("expected first tool (read_file), got %q", result.Request.Tool), 1)
	}

	if result.Request.Path != "internal/repository/comprehension/pack.go" {
		ginkgo.Fail(fmt.Sprintf("expected path from first tool, got %q", result.Request.Path), 1)
	}

	if result.Request.StartLine != 1 || result.Request.EndLine != 30 {
		ginkgo.Fail(fmt.Sprintf("expected lines 1-30, got start=%d end=%d", result.Request.StartLine, result.Request.EndLine), 1)
	}
})

// TestQwenKeyValuePurposeField verifies the purpose field (which
// Qwen naturally emits) is accepted and mapped.
var _ = ginkgo.It("QwenKeyValuePurposeField", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: strings.Join([]string{
			"tool: context_pack",
			"task: find handlers",
			"mode: plan",
			"limit: 10",
			"purpose: architecture",
		}, "\n"),
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("parse error: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected key-value tool request to normalize", 1)
	}

	if result.Request.Tool != "context_pack" || result.Request.Purpose != "architecture" {
		ginkgo.Fail(fmt.Sprintf("unexpected request: tool=%q purpose=%q", result.Request.Tool, result.Request.Purpose), 1)
	}

	if result.Request.Task != "find handlers" || result.Request.Mode != "plan" || result.Request.Limit != 10 {
		ginkgo.Fail(fmt.Sprintf("unexpected fields: task=%q mode=%q limit=%d", result.Request.Task, result.Request.Mode, result.Request.Limit), 1)
	}
})

// TestQwenKeyValuePromptWithColon verifies that a prompt value
// containing a colon (e.g. "Evidence": ...) preserves bounded
// continuation lines for known long-text fields.
var _ = ginkgo.It("QwenKeyValuePromptWithColon", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: strings.Join([]string{
			"tool: task",
			"agent: explorer",
			"mode: read_only",
			`prompt: Inspect the idiom harness to define "Evidence": where it is declared`,
			"and how it is consumed",
		}, "\n"),
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("parse error: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected key-value tool request to normalize despite continuation lines", 1)
	}

	if result.Request.Tool != "task" || result.Request.Agent != "explorer" || result.Request.Mode != "read_only" {
		ginkgo.Fail(fmt.Sprintf("unexpected request: tool=%q agent=%q mode=%q", result.Request.Tool, result.Request.Agent, result.Request.Mode), 1)
	}

	if !strings.Contains(result.Request.Prompt, `Inspect the idiom harness to define "Evidence"`) {
		ginkgo.Fail(fmt.Sprintf("prompt not captured correctly: %q", result.Request.Prompt), 1)
	}

	if !strings.Contains(result.Request.Prompt, "and how it is consumed") {
		ginkgo.Fail(fmt.Sprintf("prompt continuation not captured correctly: %q", result.Request.Prompt), 1)
	}
})

var _ = ginkgo.It("QwenKeyValueScalarContinuationIsIgnored", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: strings.Join([]string{
			"tool: read_file",
			"path: internal/repository/comprehension/pack.go",
			"this line must not be appended to the path",
			"start_line: 1",
			"end_line: 10",
		}, "\n"),
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("parse error: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected key-value tool request to normalize", 1)
	}

	if result.Request.Path != "internal/repository/comprehension/pack.go" {
		ginkgo.Fail(fmt.Sprintf("scalar continuation changed path: %q", result.Request.Path), 1)
	}

	if result.Request.StartLine != 1 || result.Request.EndLine != 10 {
		ginkgo.Fail(fmt.Sprintf("line range not parsed: start=%d end=%d", result.Request.StartLine, result.Request.EndLine), 1)
	}
})

var _ = ginkgo.It("QwenKeyValueNotePreservesKeyAndMultilineContent", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: strings.Join([]string{
			"tool: note",
			"key: source-finding",
			"content: First source fact.",
			"Second source fact.",
			"internal/runtime/agent/runner_core.go:2039 shows note handling.",
			"Evidence: note content can contain colon-bearing labels.",
		}, "\n"),
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("parse error: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected note key-value request to normalize", 1)
	}

	if result.Request.Tool != "note" || result.Request.Key != "source-finding" {
		ginkgo.Fail(fmt.Sprintf("unexpected note request: %#v", result.Request), 1)
	}

	if !strings.Contains(result.Raw, `"key":"source-finding"`) {
		ginkgo.Fail(fmt.Sprintf("raw request dropped note key: %s", result.Raw), 1)
	}

	if !strings.Contains(result.Request.Content, "Second source fact.") {
		ginkgo.Fail(fmt.Sprintf("note content continuation missing: %q", result.Request.Content), 1)
	}

	if !strings.Contains(result.Request.Content, "internal/runtime/agent/runner_core.go:2039") {
		ginkgo.Fail(fmt.Sprintf("file:line continuation missing: %q", result.Request.Content), 1)
	}

	if !strings.Contains(result.Request.Content, "Evidence: note content") {
		ginkgo.Fail(fmt.Sprintf("colon label continuation missing: %q", result.Request.Content), 1)
	}
})

var _ = ginkgo.It("QwenKeyValuePromptPreservesUnknownColonContinuation", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: strings.Join([]string{
			"tool: task",
			"agent: explorer",
			"mode: read_only",
			"prompt: Inspect the completion gate.",
			"Evidence: include the current rejection order.",
			"internal/runtime/agent/runner_core.go:6658 is the call site.",
		}, "\n"),
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("parse error: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected task key-value request to normalize", 1)
	}

	if !strings.Contains(result.Request.Prompt, "Evidence: include") {
		ginkgo.Fail(fmt.Sprintf("prompt dropped evidence label continuation: %q", result.Request.Prompt), 1)
	}

	if !strings.Contains(result.Request.Prompt, "internal/runtime/agent/runner_core.go:6658") {
		ginkgo.Fail(fmt.Sprintf("prompt dropped file:line continuation: %q", result.Request.Prompt), 1)
	}
})

var _ = ginkgo.It("QwenKeyValueFinalAnswerConfidenceIsNotUnsafeToolRequest", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: "No performance risk stands out in internal/runtime/agent/runner_core.go from the delegated evidence. Confidence: medium.",
	})
	if result.OK {
		ginkgo.Fail(fmt.Sprintf("final-answer prose must not parse as a tool request: %#v", result.Request), 1)
	}

	if result.EventOutcome != "" || result.EventTransport != "" || result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("final-answer confidence label must not classify as unsafe tool syntax: %#v", result), 1)
	}
})

var _ = ginkgo.It("QwenKeyValueMissingToolWithActionFieldRemainsUnsafe", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: "path: internal/runtime/agent/runner_core.go\nstart_line: 1\nend_line: 20",
	})
	if result.OK {
		ginkgo.Fail(fmt.Sprintf("missing-tool key-value action fields must not parse as a valid request: %#v", result.Request), 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeUnsafeToInfer || result.EventTransport != toolRequestNormalizerTransportKeyValue {
		ginkgo.Fail(fmt.Sprintf("missing-tool action fields should remain unsafe key-value syntax, got %#v", result), 1)
	}
})

var _ = ginkgo.It("ReadOnlyChildTerminalDoesNotAcceptWeakEvidencePacket", func() {
	t := ginkgo.GinkgoT()
	var events []RunEvent
	runner := newRunnerRunFixture(&Runner{
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	})
	ctx := runner.beginReadOnlyChildLifecycle("explorer", llama.ChatOptions{Model: "test-model"}, RunPolicy{})
	result := workspace.ToolResult{
		Tool:    "task",
		Summary: "Child task completed",
		Output:  "ungrounded answer",
	}
	runner.emitReadOnlyChildTerminal(ctx, result, nil, autonomy.ChildTerminalCompleted)

	terminal := mustFindLifecycleRecord[autonomy.ChildTerminalRecord](t, events, childLifecycleTerminalEventRole)
	if terminal.ObjectiveAlignment != autonomy.ChildObjectiveWeak {
		ginkgo.Fail(fmt.Sprintf("read-only terminal without typed packet should be weak: %#v", terminal), 1)
	}
	acceptance := mustFindLifecycleRecord[autonomy.ChildAcceptanceRecord](t, events, childLifecycleAcceptanceEventRole)
	if acceptance.Decision != autonomy.ChildAcceptanceRepairRequired {
		ginkgo.Fail(fmt.Sprintf("weak read-only packet should require repair, got %#v", acceptance), 1)
	}
	if len(acceptance.AcceptedEvidenceRefs) != 0 {
		ginkgo.Fail(fmt.Sprintf("weak read-only packet must not carry accepted evidence refs: %#v", acceptance), 1)
	}

	if invalid := lifecycleEventRoles(events, "child_lifecycle_record_invalid"); len(invalid) > 0 {
		ginkgo.Fail(fmt.Sprintf("lifecycle records should validate before emission, invalid events: %#v", invalid), 1)
	}
})

var _ = ginkgo.It("WorkbranchChildTerminalEmitsCandidatePatchArtifactMetadata", func() {
	t := ginkgo.GinkgoT()
	var events []RunEvent
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:       "mutation",
			TaskFamily: "implementation",
			Contract:   "implementation_result",
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	})

	ctx := runner.beginWorkbranchChildLifecycle("worker", llama.ChatOptions{Model: "coder"}, RunPolicy{}, runner.runner.Client)
	diff := Diff{
		ChangedPaths: []string{"pkg/feature.go"},
		Stat:         "1 file changed, 3 insertions, 1 deletions",
		Patch:        "diff --git a/pkg/feature.go b/pkg/feature.go\n",
	}
	checkout := Checkout{Branch: "idiom-child-test", Path: "/tmp/idiom-child-test", BaseSHA: "abc123"}
	runner.emitWorkbranchChildTerminal(ctx, checkout, workspace.ToolResult{Tool: "task", Summary: "done"}, diff, autonomy.ChildTerminalCompleted)

	artifact := mustFindLifecycleRecord[autonomy.CandidatePatchArtifact](t, events, childLifecyclePatchEventRole)
	if artifact.BaseSHA != checkout.BaseSHA || len(artifact.ChangedPaths) != 1 || artifact.ChangedPaths[0] != "pkg/feature.go" {
		ginkgo.Fail(fmt.Sprintf("unexpected candidate patch artifact: %#v", artifact), 1)
	}
	if artifact.PatchDigest == "" || artifact.PatchPath == "" {
		ginkgo.Fail(fmt.Sprintf("candidate patch artifact must carry digest and patch path refs: %#v", artifact), 1)
	}

	terminal := mustFindLifecycleRecord[autonomy.ChildTerminalRecord](t, events, childLifecycleTerminalEventRole)
	if terminal.ResultKind != autonomy.ChildResultCandidatePatch || len(terminal.ArtifactRefs) == 0 {
		ginkgo.Fail(fmt.Sprintf("workbranch terminal must reference candidate patch artifact: %#v", terminal), 1)
	}

	acceptance := mustFindLifecycleRecord[autonomy.ChildAcceptanceRecord](t, events, childLifecycleAcceptanceEventRole)
	if acceptance.TerminalID != terminal.TerminalID || acceptance.Decision != autonomy.ChildAcceptanceAccepted {
		ginkgo.Fail(fmt.Sprintf("unexpected workbranch acceptance record: %#v", acceptance), 1)
	}
	if len(acceptance.AcceptedPatchRefs) == 0 {
		ginkgo.Fail(fmt.Sprintf("workbranch acceptance must carry accepted patch refs: %#v", acceptance), 1)
	}

	if invalid := lifecycleEventRoles(events, "child_lifecycle_record_invalid"); len(invalid) > 0 {
		ginkgo.Fail(fmt.Sprintf("lifecycle records should validate before emission, invalid events: %#v", invalid), 1)
	}
})

var _ = ginkgo.It("WorkbranchChildToolEventsCarryLifecycleIDs", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	runLifecycleTestGit(t, root, "init")
	runLifecycleTestGit(t, root, "config", "user.email", "test@example.com")
	runLifecycleTestGit(t, root, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(root, "feature.txt"), []byte("before\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed feature: %v", err), 1)
	}
	runLifecycleTestGit(t, root, "add", "feature.txt")
	runLifecycleTestGit(t, root, "commit", "-m", "seed")

	replies := []string{
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"feature.txt\",\"old_text\":\"before\",\"new_text\":\"after\"}\n```",
		"Changed files: feature.txt\nVerification: not run\nConfidence: high",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := replies[len(replies)-1]
		if requests < len(replies) {
			reply = replies[requests]
		}
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))
	defer server.Close()

	var events []RunEvent
	runner := newRunnerRunFixture(&Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	})

	result := runner.runChildTask(context.Background(), workspace.ToolRequest{
		Tool:   "task",
		Agent:  "worker",
		Mode:   "workbranch",
		Prompt: "Update feature.txt from before to after.",
	}, llama.ChatOptions{Model: "test-model"})
	if strings.TrimSpace(result.Error) != "" {
		ginkgo.Fail(fmt.Sprintf("workbranch child returned error: %s\n%s", result.Error, result.Output), 1)
	}

	invocation := mustFindLifecycleRecord[autonomy.ParentChildInvocationRecord](t, events, childLifecycleInvocationEventRole)
	requestEvent := mustFindLifecycleRecord[ToolRequestEvent](t, events, "child_tool_request")
	if requestEvent.ChildInvocationID != invocation.InvocationID || requestEvent.ChildContractID != invocation.ChildContractID {
		ginkgo.Fail(fmt.Sprintf("workbranch child tool request missing lifecycle IDs: %#v", requestEvent), 1)
	}
	resultEvent := mustFindLifecycleRecord[ToolResultEvent](t, events, "child_tool_result")
	if resultEvent.ChildInvocationID != invocation.InvocationID || resultEvent.ChildContractID != invocation.ChildContractID {
		ginkgo.Fail(fmt.Sprintf("workbranch child tool result missing lifecycle IDs: %#v", resultEvent), 1)
	}
})

func runLifecycleTestGit(t ginkgo.FullGinkgoTInterface, root string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		ginkgo.Fail(fmt.Sprintf("git %v: %v\n%s", args, err, out), 1)
	}
}

func mustFindLifecycleRecord[T any](t ginkgo.FullGinkgoTInterface, events []RunEvent, role string) T {
	t.Helper()

	var zero T
	for _, event := range events {
		if event.Role != role {
			continue
		}
		var record T
		if err := json.Unmarshal([]byte(event.Content), &record); err != nil {
			ginkgo.Fail(fmt.Sprintf("unmarshal %s: %v\n%s", role, err, event.Content), 1)
		}
		if validatable, ok := any(&record).(interface{ Validate() error }); ok {
			if err := validatable.Validate(); err != nil {
				ginkgo.Fail(fmt.Sprintf("validate %s: %v\n%#v", role, err, record), 1)
			}
		}
		return record
	}

	ginkgo.Fail(fmt.Sprintf("missing lifecycle event role %q; roles=%v", role, lifecycleEventRoles(events, "")), 1)
	return zero
}

func lifecycleEventRoles(events []RunEvent, filter string) []string {
	var roles []string
	for _, event := range events {
		if filter == "" || event.Role == filter {
			roles = append(roles, event.Role)
		}
	}
	return roles
}

var _ = ginkgo.It("AgentGuidanceDocumentsHarnessBoundaryChecklist", func() {
	t := ginkgo.GinkgoT()
	root := agentTestRepoRoot(t)
	required := map[string][]string{
		filepath.Join(root, "AGENTS.md"): {
			"model owns strategy",
			"harness owns contracts",
			"Do not create pass-through wrappers",
			"typed contracts",
			"Evidence tools should report findings",
			"grounded in code, tests, logs, tool output",
		},
		filepath.Join(root, "docs", "architecture.md"): {
			"model strategy",
			"harness authority",
			"tool schemas",
			"permissions",
			"evidence freshness",
			"typed contracts",
		},
	}

	for path, markers := range required {
		body, err := os.ReadFile(path)
		if err != nil {
			ginkgo.Fail(fmt.Sprintf("read %s: %v", path, err), 1)
		}

		text := string(body)
		for _, marker := range markers {
			if !strings.Contains(text, marker) {
				ginkgo.Fail(fmt.Sprintf("%s missing de-drift marker %q", path, marker), 1)
			}
		}
	}
})

func agentTestRepoRoot(t ginkgo.FullGinkgoTInterface) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("get cwd: %v", err), 1)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			ginkgo.Fail("repo root not found from cwd", 1)
		}

		dir = parent
	}
}

var _ = ginkgo.It("InternalRuntimeAgentProductionFilesStayFlat", func() {
	t := ginkgo.GinkgoT()
	root := filepath.Clean("../../..")
	agentDir := filepath.Join(root, "internal", "runtime", "agent")

	productionFiles := productionAgentFiles(t, agentDir)

	for _, file := range productionFiles {
		if strings.Contains(file, "/") {
			ginkgo.Fail(fmt.Sprintf("internal/runtime/agent production file moved into nested package without updating architecture guidance: %s", file), 1)
		}
	}
})

func productionAgentFiles(t ginkgo.FullGinkgoTInterface, agentDir string) []string {
	t.Helper()

	var files []string
	err := filepath.WalkDir(agentDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(agentDir, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("walk production agent files: %v", err), 1)
	}
	sort.Strings(files)
	return files
}

var _ = ginkgo.It("WriteBeforeGitHistoryEvidenceRejectsExistingLogic", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	markGitHistoryRequirementWorkspace(t, root)
	writeGitHistoryRequirementTestFile(t, root, "pkg/feature.go", "package pkg\n\nfunc Apply() string { return \"old\" }\n")
	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true, MaxOutputBytes: 4 << 20})
	runner := gitHistoryRequirementRunner(manager, "implementation")
	results := []workspace.ToolResult{gitHistoryRequirementSourceResult(t, runner, "pkg/feature.go")}
	req := workspace.ToolRequest{
		Tool:        "edit_file",
		Path:        "pkg/feature.go",
		OldText:     "return \"old\"",
		NewText:     "return \"new\"",
		Description: "change existing logic",
	}
	if !newToolResultEvidenceProjection(results).HasSourceWindow("pkg/feature.go") {
		ginkgo.Fail(fmt.Sprintf("source fixture did not produce typed body evidence: %#v", results), 1)
	}
	if assessment, ok := runner.mutationAdmissionForRequest(req); !ok || !assessment.Analysis.Mutation {
		ginkgo.Fail("workspace did not analyze the admitted edit request", 1)
	}
	result, deferred := runner.implementationWritePrerequisiteDeferralResult(req, results)
	if !deferred {
		ginkgo.Fail("expected existing-file write to require git history evidence", 1)
	}
	if result.Facts == nil || result.Facts.RejectionDisposition != toolfacts.ToolRejectionDeferredByPrerequisite ||
		!slices.Equal(result.Facts.PendingHistoryPaths, []string{"pkg/feature.go"}) {
		ginkgo.Fail(fmt.Sprintf("typed prerequisite should retain the exact pending history path, got %#v", result), 1)
	}
})

var _ = ginkgo.It("WriteBeforeGitHistoryEvidenceAcceptsHistoryRange", func() {
	t := ginkgo.GinkgoT()
	manager, sourceResults := sourceWindowManagerResultsForTest("pkg/feature.go")
	markGitHistoryRequirementWorkspace(t, manager.Root())
	runner := gitHistoryRequirementRunner(manager, "implementation")
	req := workspace.ToolRequest{
		Tool:        "edit_file",
		Path:        "pkg/feature.go",
		OldText:     "return \"old\"",
		NewText:     "return \"new\"",
		Description: "change existing logic",
	}
	results := append(sourceResults, gitHistoryRangeResult("pkg/feature.go"))

	if result, deferred := runner.implementationWritePrerequisiteDeferralResult(req, results); deferred {
		ginkgo.Fail(fmt.Sprintf("did not expect typed prerequisite after successful history_range evidence: %#v", result), 1)
	} else if result.Tool != "" {
		ginkgo.Fail("did not expect write rejection after successful history_range evidence", 1)
	}
})

var _ = ginkgo.It("WriteBeforeGitHistoryEvidenceAcceptsPathScopedNotApplicable", func() {
	t := ginkgo.GinkgoT()
	manager, sourceResults := sourceWindowManagerResultsForTest("pkg/feature.go")
	markGitHistoryRequirementWorkspace(t, manager.Root())
	runner := gitHistoryRequirementRunner(manager, "implementation")
	req := workspace.ToolRequest{
		Tool:        "edit_file",
		Path:        "pkg/feature.go",
		OldText:     "return \"old\"",
		NewText:     "return \"new\"",
		Description: "change existing logic",
	}
	results := append(sourceResults, gitHistoryNotApplicableResult("pkg/feature.go"))

	if result, deferred := runner.implementationWritePrerequisiteDeferralResult(req, results); deferred {
		ginkgo.Fail(fmt.Sprintf("did not expect typed prerequisite after path-scoped history_not_applicable evidence: %#v", result), 1)
	} else if result.Tool != "" {
		ginkgo.Fail("did not expect write rejection after path-scoped history_not_applicable evidence", 1)
	}
})

var _ = ginkgo.It("WriteBeforeGitHistoryEvidenceDoesNotUseNotApplicableAcrossPaths", func() {
	t := ginkgo.GinkgoT()
	manager, sourceResults := sourceWindowManagerResultsForTest("pkg/feature.go", "pkg/other.go")
	markGitHistoryRequirementWorkspace(t, manager.Root())
	runner := gitHistoryRequirementRunner(manager, "implementation")
	req := workspace.ToolRequest{
		Tool:        "edit_file",
		Path:        "pkg/feature.go",
		OldText:     "return \"old\"",
		NewText:     "return \"new\"",
		Description: "change existing logic",
	}
	results := append(sourceResults[:1:1], gitHistoryNotApplicableResult("pkg/other.go"))

	result, deferred := runner.implementationWritePrerequisiteDeferralResult(req, results)
	if !deferred {
		ginkgo.Fail("expected path-scoped history_not_applicable for another path not to satisfy pkg/feature.go", 1)
	}
	if result.Facts == nil || !slices.Equal(result.Facts.PendingHistoryPaths, []string{"pkg/feature.go"}) {
		ginkgo.Fail(fmt.Sprintf("typed prerequisite should remain scoped to pkg/feature.go, got %#v", result), 1)
	}
})

var _ = ginkgo.It("WriteBeforeGitHistoryEvidenceSkipsNewFile", func() {
	t := ginkgo.GinkgoT()
	manager, _ := sourceWindowManagerResultsForTest()
	if err := os.MkdirAll(filepath.Join(manager.Root(), "pkg"), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("create new-file parent: %v", err), 1)
	}
	markGitHistoryRequirementWorkspace(t, manager.Root())
	runner := gitHistoryRequirementRunner(manager, "implementation")
	req := workspace.ToolRequest{
		Tool:        "write_file",
		Path:        "pkg/new_feature.go",
		Content:     "package pkg\n",
		Description: "add new logic",
	}

	if result, deferred := runner.implementationWritePrerequisiteDeferralResult(req, nil); deferred || result.Tool != "" {
		ginkgo.Fail(fmt.Sprintf("new-file writes should not require prior git history: %#v", result), 1)
	}
})

var _ = ginkgo.It("WriteBeforeGitHistoryEvidenceSkipsWorkspaceWithoutGitMetadata", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	writeGitHistoryRequirementTestFile(t, root, "pkg/feature.go", "package pkg\n\nfunc Apply() string { return \"old\" }\n")
	runner := gitHistoryRequirementRunner(workspace.NewManager(config.Workspace{Root: root, CWD: root}), "implementation")
	req := workspace.ToolRequest{
		Tool:        "edit_file",
		Path:        "pkg/feature.go",
		OldText:     "return \"old\"",
		NewText:     "return \"new\"",
		Description: "change existing logic in a fixture workspace",
	}

	if result, deferred := runner.implementationWritePrerequisiteDeferralResult(req, nil); deferred || result.Tool != "" {
		ginkgo.Fail("workspace without git metadata should not be blocked on unavailable history evidence", 1)
	}
})

var _ = ginkgo.It("SubmitPlanHistoryGuardRejectsExistingEditWithoutHistory", func() {
	t := ginkgo.GinkgoT()
	manager, results := sourceWindowManagerResultsForTest("pkg/feature.go")
	markGitHistoryRequirementWorkspace(t, manager.Root())
	runner := gitHistoryRequirementRunner(manager, PlanThenExecutePlannerTaskFamily)
	req := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{{
			Path:    "pkg/feature.go",
			OldText: "package fixture",
			NewText: "package updated",
		}},
	}
	guard := runner.preflightPlanThenExecuteSubmitPlanHistoryGuard(req, results)

	if !guard.Intercepted {
		ginkgo.Fail("expected submit_plan to be rejected without git history evidence", 1)
	}

	if !strings.Contains(guard.Result.Error, "history_range") || !strings.Contains(guard.Result.Error, "commit-description") {
		ginkgo.Fail(fmt.Sprintf("guard should require commit-description history evidence, got %q", guard.Result.Error), 1)
	}
})

var _ = ginkgo.It("SubmitPlanHistoryGuardAcceptsHistorySymbol", func() {
	t := ginkgo.GinkgoT()
	manager, sourceResults := sourceWindowManagerResultsForTest("pkg/feature.go")
	markGitHistoryRequirementWorkspace(t, manager.Root())
	runner := gitHistoryRequirementRunner(manager, PlanThenExecutePlannerTaskFamily)
	req := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{{
			Path:    "pkg/feature.go",
			OldText: "package fixture",
			NewText: "package updated",
		}},
	}
	results := append(sourceResults, workspace.ToolResult{
		Tool:    "history_symbol",
		Summary: "Read git history for Apply in pkg/feature.go",
		Output:  "commit 1234567\nAuthor: Example\n\n    Preserve Apply behavior for compatibility\n",
		Facts: &toolfacts.ToolResultFacts{
			GitHistoryPath:   "pkg/feature.go",
			GitHistoryStatus: toolfacts.GitHistoryStatusCommitDescriptions,
		},
	})

	if guard := runner.preflightPlanThenExecuteSubmitPlanHistoryGuard(req, results); guard.Intercepted {
		ginkgo.Fail(fmt.Sprintf("did not expect submit_plan rejection after history_symbol evidence: %#v", guard.Result), 1)
	}
})

func gitHistoryRequirementRunner(manager *workspace.Manager, family string) *runExecution {
	manager.SetPermissionRules(map[string][]toolpolicy.Rule{
		workspace.ToolNameReadFile: {{Pattern: "**", Decision: toolpolicy.Allow}},
	})
	brief := workerruntime.RuntimeBriefForTaskFamily(family, "main", []string{"read_file", "history_range", "history_symbol", "blame_range", "edit_file", "write_file", "apply_patch", "submit_plan"})

	brief.RequiredEvidence = append([]string(nil), brief.RequiredEvidence...)

	if !runnerContainsString(brief.RequiredEvidence, string(workerruntime.RequiredEvidenceGitHistoryRationale)) {
		brief.RequiredEvidence = append(brief.RequiredEvidence, string(workerruntime.RequiredEvidenceGitHistoryRationale))
	}

	return newRunnerRunFixture(&Runner{
		Workspace:    manager,
		RuntimeBrief: brief,
	})
}

func gitHistoryRequirementSourceResult(t ginkgo.FullGinkgoTInterface, runner *runExecution, path string) workspace.ToolResult {
	t.Helper()
	result := runner.runner.Workspace.Execute(context.Background(), workspace.ToolRequest{
		Tool:      workspace.ToolNameReadFile,
		Path:      path,
		StartLine: 1,
		EndLine:   100_000,
	})
	if result.Error != "" || len(result.SourceWindows()) == 0 {
		ginkgo.Fail(fmt.Sprintf("read git-history source fixture %s: %#v", path, result), 1)
	}
	return result
}

func gitHistoryRangeResult(path string) workspace.ToolResult {
	return workspace.ToolResult{
		Tool:    "history_range",
		Summary: "Read git history for " + path + " lines 1-20",
		Output:  "commit 1234567\nAuthor: Example\n\n    Preserve established behavior for compatibility\n",
		Facts: &toolfacts.ToolResultFacts{
			GitHistoryPath:   path,
			GitHistoryStatus: toolfacts.GitHistoryStatusCommitDescriptions,
		},
	}
}

func gitHistoryNotApplicableResult(path string) workspace.ToolResult {
	return workspace.ToolResult{
		Tool:    "history_range",
		Code:    workspace.HistoryNotApplicableCode,
		Summary: "Git history not applicable for " + path,
		Output:  `{"status":"not_applicable","code":"` + workspace.HistoryNotApplicableCode + `","reason":"untracked_path","path":"` + path + `"}`,
		Facts: &toolfacts.ToolResultFacts{
			GitHistoryPath:   path,
			GitHistoryStatus: toolfacts.GitHistoryStatusNotApplicable,
		},
	}
}

var _ = ginkgo.It("PlanExecuteUnavailableHistoryToolNudgeWhenHistoryAlreadySatisfied", func() {
	brief := workerruntime.RuntimeBriefForTaskFamily(PlanThenExecutePlannerTaskFamily, "main", []string{"read_file", "code_navigate", "history_range", "history_symbol", "submit_plan"})
	brief.RequiredEditPaths = []string{"a.go"}
	brief.RequiredEvidence = []string{string(workerruntime.RequiredEvidenceGitHistoryRationale)}
	results := []workspace.ToolResult{
		gitHistoryRangeResult("a.go"),
	}

	summary, errorText, ok := planThenExecuteUnavailableHistoryToolNudge(
		workspace.ToolRequest{Tool: "history_range", Path: "a.go", StartLine: 1, EndLine: 10},
		brief,
		results,
		[]string{"read_file", "code_navigate"}, planThenExecutePathAuthority{})

	if !ok {
		ginkgo.Fail("expected repeated history_range to get typed phase nudge", 1)
	}

	if !strings.Contains(summary, "Git history tool not available") {
		ginkgo.Fail(fmt.Sprintf("summary = %q", summary), 1)
	}

	for _, want := range []string{"already satisfied", "a.go"} {
		if !strings.Contains(errorText, want) {
			ginkgo.Fail(fmt.Sprintf("error text missing %q: %s", want, errorText), 1)
		}
	}
})

var _ = ginkgo.It("PlanThenExecuteHistoryPendingPathsMixedEvidenceStates", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go", "b.go", "c.go", "d.go"},
		RequiredEvidence:  []string{string(workerruntime.RequiredEvidenceGitHistoryRationale)},
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
		gitHistoryRangeResult("a.go"),
		readFileSourceWindowResult("b.go", 1, 10, 10, "1:package b\n"),
		gitHistoryNotApplicableResult("b.go"),
		readFileSourceWindowResult("c.go", 1, 10, 10, "1:package c\n"),
	}

	pending := planThenExecuteHistoryPendingPaths(brief, results, nil, planThenExecutePathAuthority{})
	if len(pending) != 1 || pending[0] != "c.go" {
		ginkgo.Fail(fmt.Sprintf("pending paths = %#v, want only c.go", pending), 1)
	}

	completed := modelProtocolCompletedGitHistoryPaths(brief, results)
	if len(completed) != 2 || completed[0] != "a.go" || completed[1] != "b.go" {
		ginkgo.Fail(fmt.Sprintf("completed paths = %#v, want a.go and b.go", completed), 1)
	}
})

func writeGitHistoryRequirementTestFile(t ginkgo.FullGinkgoTInterface, root string, rel string, body string) {
	t.Helper()

	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir %s: %v", filepath.Dir(full), err), 1)
	}

	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write %s: %v", full, err), 1)
	}
}

func markGitHistoryRequirementWorkspace(t ginkgo.FullGinkgoTInterface, root string) {
	t.Helper()

	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("create .git marker: %v", err), 1)
	}
}

func runnerContainsString(values []string, want string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}

	return false
}

// TestLanePolicyForBrief_ReadOnlyInvestigationLanes verifies that read-only
// workspace lanes that do not require mutation keep loop detection and advisory
// nudges while suppressing force-answer-after-rejection. Conversation and
// direct_answer lanes still let the model own readiness more fully.
var _ = ginkgo.It("LanePolicyForBrief_ReadOnlyInvestigationLanes", func() {
	conversationFirst, conversationInterval := conversationMilestones(32768)

	tests := []struct {
		name             string
		brief            workerruntime.WorkerRuntimeBrief
		wantName         LanePolicyID
		wantForceAnswer  bool
		wantSkipRecovery bool
		wantAutoCapture  bool
		wantNoteTool     bool
		wantConfidence   string
		wantMilestone    int
		wantInterval     int
	}{
		{
			name: "conversation gets conversation policy",
			brief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "conversation",
				Contract:          "direct_answer",
				PermissionProfile: "read_only_workspace",
			},
			wantName:         lanePolicyConversation,
			wantSkipRecovery: true,
			wantAutoCapture:  true,
			wantNoteTool:     true,
			wantConfidence:   "medium",
			wantMilestone:    conversationFirst,
			wantInterval:     conversationInterval,
		},
		{
			name: "direct_answer + read_only gets conversation policy",
			brief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "direct_answer",
				Contract:          "direct_answer",
				PermissionProfile: "read_only_workspace",
			},
			wantName:         lanePolicyConversation,
			wantSkipRecovery: true,
			wantAutoCapture:  true,
			wantNoteTool:     true,
			wantConfidence:   "medium",
			wantMilestone:    conversationFirst,
			wantInterval:     conversationInterval,
		},
		{
			name: "architecture + read_only gets read-only investigation policy",
			brief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "architecture",
				Contract:          "architecture_assessment",
				PermissionProfile: "read_only_workspace",
			},
			wantName:        lanePolicyReadOnlyInvestigation,
			wantAutoCapture: true,
			wantNoteTool:    true,
			wantConfidence:  "medium",
			wantMilestone:   10,
			wantInterval:    10,
		},
		{
			name: "review + read_only gets read-only investigation policy",
			brief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "security_review",
				Contract:          "review_findings",
				PermissionProfile: "read_only_workspace",
			},
			wantName:        lanePolicyReadOnlyInvestigation,
			wantAutoCapture: true,
			wantNoteTool:    true,
			wantConfidence:  "medium",
			wantMilestone:   10,
			wantInterval:    10,
		},
		{
			name: "locate + read_only gets read-only investigation policy",
			brief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "locate",
				Contract:          "locate_targets",
				PermissionProfile: "read_only_workspace",
			},
			wantName:        lanePolicyReadOnlyInvestigation,
			wantAutoCapture: true,
			wantNoteTool:    true,
			wantConfidence:  "medium",
			wantMilestone:   10,
			wantInterval:    10,
		},
		{
			name: "implementation gets default policy",
			brief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "implementation",
				Contract:          "implementation_result",
				PermissionProfile: "guarded_workspace_write",
			},
			wantName:        lanePolicyDefault,
			wantForceAnswer: true,
		},
		{
			name: "plan-execute planner gets default policy",
			brief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "plan_execute_planner",
				Contract:          "implementation_result",
				PermissionProfile: "guarded_workspace_write",
			},
			wantName:        lanePolicyDefault,
			wantForceAnswer: true,
		},
		{
			name: "delegated read-only gets delegated policy",
			brief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "delegated_read_only",
				Contract:          "grounded_evidence_summary",
				PermissionProfile: "read_only_workspace",
			},
			wantName:        lanePolicyDelegatedReadOnly,
			wantForceAnswer: true,
			wantNoteTool:    true,
		},
	}

	for _, tt := range tests {
		ginkgo.By(tt.name, func() {
			got := LanePolicyForBrief(tt.brief)
			if !lanePolicyHasName(got, tt.wantName) {
				ginkgo.Fail(fmt.Sprintf("LanePolicyForBrief(%+v).Name() = %q, want %q", tt.brief, lanePolicyName(got), tt.wantName), 1)
			}
			if got.forceAnswerAfterRejection != tt.wantForceAnswer {
				ginkgo.Fail(fmt.Sprintf("%s force-answer-after-rejection = %v, want %v", lanePolicyName(got), got.forceAnswerAfterRejection, tt.wantForceAnswer), 1)
			}
			if got.skipRecoveryNudge != tt.wantSkipRecovery {
				ginkgo.Fail(fmt.Sprintf("%s skip-recovery-nudge = %v, want %v", lanePolicyName(got), got.skipRecoveryNudge, tt.wantSkipRecovery), 1)
			}
			if got.autoCaptureFindings != tt.wantAutoCapture {
				ginkgo.Fail(fmt.Sprintf("%s auto-capture-findings = %v, want %v", lanePolicyName(got), got.autoCaptureFindings, tt.wantAutoCapture), 1)
			}
			if lanePolicyShouldHaveNoteTool(got, tt.brief.Contract) != tt.wantNoteTool {
				ginkgo.Fail(fmt.Sprintf("%s note-tool(%q) = %v, want %v", lanePolicyName(got), tt.brief.Contract, lanePolicyShouldHaveNoteTool(got, tt.brief.Contract), tt.wantNoteTool), 1)
			}
			if got.confidenceCap != tt.wantConfidence {
				ginkgo.Fail(fmt.Sprintf("%s confidence cap = %q, want %q", lanePolicyName(got), got.confidenceCap, tt.wantConfidence), 1)
			}
			first, interval := lanePolicyCallCountMilestones(got, 32768)
			if first != tt.wantMilestone || interval != tt.wantInterval {
				ginkgo.Fail(fmt.Sprintf("%s milestones = (%d, %d), want (%d, %d)", lanePolicyName(got), first, interval, tt.wantMilestone, tt.wantInterval), 1)
			}
		})
	}
})

var _ = ginkgo.It("CompilePlanIntentToolRequestRoutesThroughSubmitPlanShape", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	writePlanIntentFixture(t, root, "internal/a.go", "package internal\n\nfunc label() string {\n\treturn \"old\"\n}\n")

	artifact := planintent.Artifact{
		SchemaVersion: planintent.SchemaVersion,
		Source:        planintent.SourcePlanner,
		TaskSummary:   "replace label",
		Scope:         planintent.Scope{RequiredEditPaths: []string{"internal/a.go"}},
		Intents: []planintent.Intent{{
			Path:      "internal/a.go",
			Operation: planintent.OperationReplaceExactText,
			OldText:   "return \"old\"",
			NewText:   "return \"next\"",
			Purpose:   "replace label",
		}},
	}
	body := marshalPlanIntentArtifact(t, artifact)

	var events []RunEvent

	runner := newRunnerRunFixture(&Runner{
		Workspace:    workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: planIntentPlannerBrief("internal/a.go"),
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	})

	req, raw, ok := runner.compilePlanIntentToolRequest(body)

	if !ok {
		ginkgo.Fail("expected edit_intent.v1 artifact to compile", 1)
	}

	if req.Tool != "submit_plan" || len(req.Edits) != 1 || req.Edits[0].Path != "internal/a.go" {
		ginkgo.Fail(fmt.Sprintf("unexpected compiled request: %#v", req), 1)
	}

	if !strings.Contains(raw, `"tool":"submit_plan"`) {
		ginkgo.Fail(fmt.Sprintf("raw compiled request should be parser-readable submit_plan JSON: %s", raw), 1)
	}

	if !runEventsContainRole(events, "edit_intent_validated") || !runEventsContainRole(events, "patch_compile_started") || !runEventsContainRole(events, "patch_compile_result") {
		ginkgo.Fail(fmt.Sprintf("expected bounded plan-intent stage events, got %#v", events), 1)
	}

	if !runEventsContainContent(events, `"compile_outcome":"accepted"`) {
		ginkgo.Fail(fmt.Sprintf("expected compile_outcome event, got %#v", events), 1)
	}
})

var _ = ginkgo.It("CompilePlanIntentToolRequestAcceptsExactFencedArtifact", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	writePlanIntentFixture(t, root, "internal/a.go", "one\ntwo\n")

	artifact := planintent.Artifact{
		SchemaVersion: planintent.SchemaVersion,
		Source:        planintent.SourcePlanner,
		Scope:         planintent.Scope{RequiredEditPaths: []string{"internal/a.go"}},
		Intents: []planintent.Intent{{
			Path:       "internal/a.go",
			Operation:  planintent.OperationInsertAfterLineAnchor,
			LineAnchor: "one",
			Content:    "middle\n",
			Purpose:    "insert middle",
		}},
	}
	runner := newRunnerRunFixture(&Runner{
		Workspace:    workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: planIntentPlannerBrief("internal/a.go"),
	})

	req, _, ok := runner.compilePlanIntentToolRequest("```json\n" + marshalPlanIntentArtifact(t, artifact) + "\n```")

	if !ok || req.Tool != "submit_plan" || len(req.Edits) != 1 {
		ginkgo.Fail(fmt.Sprintf("expected exact fenced edit intent artifact to compile, ok=%v req=%#v", ok, req), 1)
	}
})

var _ = ginkgo.It("CompilePlanIntentToolRequestRejectsScopeWidening", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	writePlanIntentFixture(t, root, "internal/a.go", "package internal\n")

	artifact := planintent.Artifact{
		SchemaVersion: planintent.SchemaVersion,
		Source:        planintent.SourcePlanner,
		Scope:         planintent.Scope{RequiredEditPaths: []string{"internal/other.go"}},
		Intents: []planintent.Intent{{
			Path:      "internal/other.go",
			Operation: planintent.OperationReplaceExactText,
			OldText:   "old",
			NewText:   "new",
		}},
	}

	var events []RunEvent

	runner := newRunnerRunFixture(&Runner{
		Workspace:    workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: planIntentPlannerBrief("internal/a.go"),
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	})

	req, raw, ok := runner.compilePlanIntentToolRequest(marshalPlanIntentArtifact(t, artifact))

	if ok || req.Tool != "" || raw != "" {
		ginkgo.Fail(fmt.Sprintf("scope-widening edit intent must not compile: ok=%v req=%#v raw=%q", ok, req, raw), 1)
	}

	if !runEventsContainContent(events, "required_scope_mismatch") {
		ginkgo.Fail(fmt.Sprintf("expected scope mismatch event, got %#v", events), 1)
	}
})

var _ = ginkgo.It("CompilePlanIntentToolRequestReportsMalformedIntentJSON", func() {
	var events []RunEvent

	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: planIntentPlannerBrief("internal/a.go"),
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	})

	req, raw, ok := runner.compilePlanIntentToolRequest(`{"schema_version":"idiom.edit_intent.v1",`)

	if ok || req.Tool != "" || raw != "" {
		ginkgo.Fail(fmt.Sprintf("malformed edit intent JSON must not compile: ok=%v req=%#v raw=%q", ok, req, raw), 1)
	}

	if !runEventsContainContent(events, "malformed_json") {
		ginkgo.Fail(fmt.Sprintf("expected malformed_json event, got %#v", events), 1)
	}
})

var _ = ginkgo.It("CompilePlanIntentToolRequestDoesNotRouteTypedBlockerToSubmitPlan", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	artifact := planintent.Artifact{
		SchemaVersion: planintent.SchemaVersion,
		Source:        planintent.SourcePlanner,
		Scope:         planintent.Scope{RequiredEditPaths: []string{"internal/a.go"}},
		Blockers: []planintent.Blocker{{
			Code:         planintent.CodeIntentBlocker,
			Message:      "required source evidence is unavailable",
			Paths:        []string{"internal/a.go"},
			EvidenceRefs: []string{"conversation:parser_failure"},
		}},
	}

	var events []RunEvent

	runner := newRunnerRunFixture(&Runner{
		Workspace:    workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: planIntentPlannerBrief("internal/a.go"),
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	})

	req, raw, ok := runner.compilePlanIntentToolRequest(marshalPlanIntentArtifact(t, artifact))

	if ok || req.Tool != "" || raw != "" {
		ginkgo.Fail(fmt.Sprintf("typed blocker must not compile into submit_plan: ok=%v req=%#v raw=%q", ok, req, raw), 1)
	}

	if !runEventsContainContent(events, planintent.CodeIntentBlocker) {
		ginkgo.Fail(fmt.Sprintf("expected typed blocker compile event, got %#v", events), 1)
	}
})

var _ = ginkgo.It("CompilePlanIntentToolRequestDoesNotCompileOutsidePlannerSubmitPlanSurface", func() {
	t := ginkgo.GinkgoT()
	artifact := planintent.Artifact{
		SchemaVersion: planintent.SchemaVersion,
		Source:        planintent.SourceArtifactDistiller,
		Scope:         planintent.Scope{RequiredEditPaths: []string{"internal/a.go"}},
		Intents: []planintent.Intent{{
			Path:      "internal/a.go",
			Operation: planintent.OperationReplaceExactText,
			OldText:   "old",
			NewText:   "new",
		}},
	}

	var events []RunEvent

	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.RuntimeBriefForTaskFamily(workerruntime.ArtifactDistillerTaskFamily, "main", []string{"submit_plan"}),
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	})

	req, raw, ok := runner.compilePlanIntentToolRequest(marshalPlanIntentArtifact(t, artifact))

	if ok || req.Tool != "" || raw != "" {
		ginkgo.Fail(fmt.Sprintf("artifact distiller output must remain a non-executable artifact: ok=%v req=%#v raw=%q", ok, req, raw), 1)
	}

	if len(events) != 0 {
		ginkgo.Fail(fmt.Sprintf("non-planner bridge should not emit compile events, got %#v", events), 1)
	}
})

func planIntentPlannerBrief(paths ...string) workerruntime.WorkerRuntimeBrief {
	return workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: paths,
		AllowedTools:      []string{"submit_plan"},
		RequiredTools:     []string{"submit_plan"},
	}
}

func marshalPlanIntentArtifact(t ginkgo.FullGinkgoTInterface, artifact planintent.Artifact) string {
	t.Helper()

	body, err := json.Marshal(artifact)
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("marshal artifact: %v", err), 1)
	}

	return string(body)
}

func writePlanIntentFixture(t ginkgo.FullGinkgoTInterface, root string, path string, content string) {
	t.Helper()

	fullPath := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir fixture parent: %v", err), 1)
	}

	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write fixture: %v", err), 1)
	}
}

func runEventsContainRole(events []RunEvent, role string) bool {
	for _, event := range events {
		if event.Role == role {
			return true
		}
	}

	return false
}

func runEventsContainContent(events []RunEvent, text string) bool {
	for _, event := range events {
		if strings.Contains(event.Content, text) {
			return true
		}
	}

	return false
}

type runtimeAgentPackage struct {
	ImportPath string
	Imports    []string
	Deps       []string
}

var _ = ginkgo.It("RuntimeAgentTargetPackagesKeepAllowedDirectImports", func() {
	t := ginkgo.GinkgoT()
	packages := loadRuntimeAgentPackages(t)
	for importPath, allowed := range runtimeAgentTargetPackageDirectImports() {
		pkg, ok := packages[importPath]
		if !ok {
			ginkgo.Fail(fmt.Sprintf("target package %s does not exist", importPath), 1)
		}
		allowedSet := stringSet(allowed)
		for _, imported := range pkg.Imports {
			if !strings.HasPrefix(imported, "idiom/internal/") {
				continue
			}
			if _, ok := allowedSet[imported]; !ok {
				ginkgo.Fail(fmt.Sprintf("%s imports %s; allowed internal imports: %v", importPath, imported, allowed), 1)
			}
		}
	}
})

var _ = ginkgo.It("RuntimeAgentTargetPackagesDoNotDependOnOuterLayers", func() {
	t := ginkgo.GinkgoT()
	packages := loadRuntimeAgentPackages(t)
	for _, importPath := range runtimeAgentOuterLayerProtectedPackages() {
		assertRuntimePackageNoDependency(t, packages, importPath, []string{
			"idiom/cmd/",
			"idiom/internal/surfaces/",
			"idiom/internal/runtime/backends",
			"idiom/internal/runtime/llama",
			"idiom/internal/capabilities/mcp",
		})
	}
})

var _ = ginkgo.It("RuntimeAgentHasNoNestedGoPackages", func() {
	t := ginkgo.GinkgoT()
	packages := loadRuntimeAgentPackages(t)
	for importPath := range packages {
		if strings.HasPrefix(importPath, "idiom/internal/runtime/agent/") {
			ginkgo.Fail(fmt.Sprintf("internal/runtime/agent should remain a flat package; found nested package %s", importPath), 1)
		}
	}
})

func runtimeAgentTargetPackageDirectImports() map[string][]string {
	return map[string][]string{
		"idiom/internal/runtime/instructions": {},
		"idiom/internal/runtime/modepolicy": {
			"idiom/internal/contracts/runprofile",
			"idiom/internal/contracts/toolpolicy",
		},
	}
}

func runtimeAgentOuterLayerProtectedPackages() []string {
	return []string{
		"idiom/internal/runtime/instructions",
		"idiom/internal/runtime/modepolicy",
	}
}

func loadRuntimeAgentPackages(t ginkgo.FullGinkgoTInterface) map[string]runtimeAgentPackage {
	t.Helper()

	cmd := exec.Command("go", "list", "-json", "./internal/runtime/agent/...", "./internal/runtime/instructions", "./internal/runtime/modepolicy")
	cmd.Dir = repoRootForRuntimeAgentBoundaryTest()
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			ginkgo.Fail(fmt.Sprintf("go list failed: %v\n%s", err, exitErr.Stderr), 1)
		}
		ginkgo.Fail(fmt.Sprintf("go list failed: %v", err), 1)
	}

	decoder := json.NewDecoder(strings.NewReader(string(out)))
	packages := make(map[string]runtimeAgentPackage)
	for {
		var pkg runtimeAgentPackage
		if err := decoder.Decode(&pkg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			ginkgo.Fail(fmt.Sprintf("decode go list output: %v", err), 1)
		}
		sort.Strings(pkg.Imports)
		sort.Strings(pkg.Deps)
		packages[pkg.ImportPath] = pkg
	}
	return packages
}

func repoRootForRuntimeAgentBoundaryTest() string {
	return filepath.Clean("../../..")
}

func assertRuntimePackageNoDependency(t ginkgo.FullGinkgoTInterface, packages map[string]runtimeAgentPackage, importPath string, forbidden []string) {
	t.Helper()

	pkg, ok := packages[importPath]
	if !ok {
		ginkgo.Fail(fmt.Sprintf("target package %s does not exist", importPath), 1)
	}
	for _, dep := range pkg.Deps {
		for _, prefix := range forbidden {
			if strings.HasPrefix(dep, prefix) {
				ginkgo.Fail(fmt.Sprintf("%s depends on forbidden package %s via prefix %s", importPath, dep, prefix), 1)
			}
		}
	}
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

var _ = ginkgo.It("ParseToolCall_BuiltinTypedFields", func() {
	rawArgs := `{"pattern":"foo","path":"src","glob":"*.go"}`
	call := llama.ToolCall{
		Function: llama.ToolCallFunc{
			Name:      "search_content",
			Arguments: rawArgs,
		},
	}

	req, ok, err := parseToolCall(call)
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("parseToolCall: %v", err), 1)
	}

	if !ok {
		ginkgo.Fail("expected ok=true", 1)
	}

	if req.Tool != "search_content" {
		ginkgo.Fail(fmt.Sprintf("Tool = %q", req.Tool), 1)
	}

	if req.Pattern != "foo" || req.Path != "src" || req.Glob != "*.go" {
		ginkgo.Fail(fmt.Sprintf("typed fields = pattern=%q path=%q glob=%q", req.Pattern, req.Path, req.Glob), 1)
	}

	// Args is also preserved verbatim for non-MCP tools so it can feed the
	// dedup key (toolRequestKey) — external tools have empty typed fields
	// and need Args to distinguish materially-different calls. Built-ins
	// don't need it for dedup (their typed fields differ), but populating
	// uniformly keeps the contract simple.
	if string(req.Args) != rawArgs {
		ginkgo.Fail(fmt.Sprintf("expected Args to be raw arguments verbatim, got %q", string(req.Args)), 1)
	}
})

var _ = ginkgo.It("ParseToolCall_BuiltinRepairsNumericStringFields", func() {
	rawArgs := `{"path":"README.md","start_line":"10","end_line":"12","content":"updated\n","description":"update range"}`
	call := llama.ToolCall{
		Function: llama.ToolCallFunc{
			Name:      "edit_file",
			Arguments: rawArgs,
		},
	}

	req, ok, err := parseToolCall(call)
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("parseToolCall: %v", err), 1)
	}

	if !ok {
		ginkgo.Fail("expected ok=true", 1)
	}

	if req.Tool != "edit_file" || req.StartLine != 10 || req.EndLine != 12 {
		ginkgo.Fail(fmt.Sprintf("unexpected request: %#v", req), 1)
	}

	if string(req.Args) != rawArgs {
		ginkgo.Fail(fmt.Sprintf("Args should preserve raw model arguments, got %q", string(req.Args)), 1)
	}
})

var _ = ginkgo.It("ParseToolCall_BuiltinRepairsStringifiedMultilineEdits", func() {
	inner := `[{"path":"a.go","old_text":"line one
line two","new_text":"line one
line three","purpose":"update a"}]`

	payload, err := json.Marshal(map[string]any{
		"rationale": "plan",
		"edits":     inner,
	})
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("marshal payload: %v", err), 1)
	}

	rawArgs := string(payload)
	call := llama.ToolCall{
		Function: llama.ToolCallFunc{
			Name:      "submit_plan",
			Arguments: rawArgs,
		},
	}

	req, ok, err := parseToolCall(call)
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("parseToolCall: %v", err), 1)
	}

	if !ok {
		ginkgo.Fail("expected parsed tool call", 1)
	}

	if len(req.Edits) != 1 || req.Edits[0].Path != "a.go" {
		ginkgo.Fail(fmt.Sprintf("expected stringified submit_plan edits to repair into typed edits, got %#v", req.Edits), 1)
	}
})

var _ = ginkgo.It("ParseToolCall_MCPRoutesArgsRaw", func() {
	rawArgs := `{"query":"go 1.24 release notes","numResults":5,"livecrawl":"preferred","customField":42}`
	call := llama.ToolCall{
		Function: llama.ToolCallFunc{
			Name:      "mcp:exa/web_search_exa",
			Arguments: rawArgs,
		},
	}

	req, ok, err := parseToolCall(call)
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("parseToolCall: %v", err), 1)
	}

	if !ok {
		ginkgo.Fail("expected ok=true", 1)
	}

	if req.Tool != "mcp:exa/web_search_exa" {
		ginkgo.Fail(fmt.Sprintf("Tool = %q", req.Tool), 1)
	}

	if string(req.Args) != rawArgs {
		ginkgo.Fail(fmt.Sprintf("Args = %q, want byte-for-byte %q", string(req.Args), rawArgs), 1)
	}

	// The typed fields should be untouched even though `query` exists on
	// both ToolRequest and the model's MCP arg JSON. We never tried to
	// unmarshal into them.
	if req.Query != "" {
		ginkgo.Fail(fmt.Sprintf("Query field unexpectedly populated: %q (should be empty for mcp:* calls)", req.Query), 1)
	}

	// And Args is still valid JSON.
	var probe map[string]any

	if err := json.Unmarshal(req.Args, &probe); err != nil {
		ginkgo.Fail(fmt.Sprintf("Args is not valid JSON: %v", err), 1)
	}

	if probe["customField"] != float64(42) {
		ginkgo.Fail(fmt.Sprintf("expected customField preserved, got %v", probe["customField"]), 1)
	}
})

var _ = ginkgo.It("ParseToolCall_MCPEmptyArgsBecomesEmptyObject", func() {
	call := llama.ToolCall{
		Function: llama.ToolCallFunc{
			Name:      "mcp:fetch/fetch",
			Arguments: "",
		},
	}

	req, ok, err := parseToolCall(call)
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("parseToolCall: %v", err), 1)
	}

	if !ok {
		ginkgo.Fail("expected ok=true", 1)
	}

	if string(req.Args) != "{}" {
		ginkgo.Fail(fmt.Sprintf("Args = %q, want empty-object {}", string(req.Args)), 1)
	}
})

var _ = ginkgo.It("ParseToolCall_EmptyNameReturnsNotOk", func() {
	_, ok, err := parseToolCall(llama.ToolCall{})
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("parseToolCall: %v", err), 1)
	}

	if ok {
		ginkgo.Fail("expected ok=false for empty name", 1)
	}
})

var _ = ginkgo.It("ParseToolCall_BuiltinBadJSONReturnsError", func() {
	call := llama.ToolCall{
		Function: llama.ToolCallFunc{
			Name:      "search_content",
			Arguments: `{not json}`,
		},
	}

	_, _, err := parseToolCall(call)
	if err == nil {
		ginkgo.Fail("expected JSON error", 1)
	}
})

// TestLanePolicyClassifiersIgnoreMCP pins the read-class lane policy
// against `mcp:*` calls. The classifier is an explicit built-in tool policy,
// so MCP tools never:
//
//   - count as verification (so the verification-only lane keeps
//     redirecting to a real verification command instead of letting
//     web_search short-circuit it), nor
//   - count as post-readiness "gap" tools (so we never ask the model
//     to attach `named_gap` / `why_not_answering` to an MCP call —
//     external retrieval is not workspace narrowing and the contract
//     would be nonsensical).
//
// A future refactor that switches either function to a prefix check
// would silently regress this; this test catches that.
var _ = ginkgo.It("LanePolicyClassifiersIgnoreMCP", func() {
	if workspace.IsReadClassToolName("mcp:exa/web_search_exa") {
		ginkgo.Fail("workspace.IsReadClassToolName(mcp:*) = true, want false", 1)
	}

	if workspace.IsReadClassToolName("mcp:fetch/fetch") {
		ginkgo.Fail("workspace.IsReadClassToolName(mcp:fetch/fetch) = true, want false", 1)
	}
})

// TestToolResultMessageSyntheticIDForEmptyCallID verifies that
// toolResultMessage generates a synthetic tool_call_id when the
// call's ID is empty, so strict-protocol backends (DeepSeek) see a
// proper role:"tool" message with tool_call_id.
var _ = ginkgo.It("ToolResultMessageSyntheticIDForEmptyCallID", func() {
	t := ginkgo.GinkgoT()
	call := &llama.ToolCall{
		ID:   "", // empty — simulates textual-tool fallback
		Type: "function",
		Function: llama.ToolCallFunc{
			Name:      "search_content",
			Arguments: `{"pattern":"test"}`,
		},
	}
	result := workspace.ToolResult{
		Tool:    "search_content",
		Summary: "Rejected by retry budget",
		Error:   "test rejection",
	}

	msg := toolResultMessage(call, result)

	if msg.Role != "tool" {
		ginkgo.Fail(fmt.Sprintf("expected role=tool for non-nil call even with empty ID, got role=%q", msg.Role), 1)
	}

	if msg.ToolCallID == "" {
		ginkgo.Fail("expected non-empty ToolCallID (synthetic), got empty", 1)
	}

	if !strings.Contains(msg.ToolCallID, "rejected_search_content_") {
		ginkgo.Fail(fmt.Sprintf("expected synthetic ID to contain 'rejected_search_content_', got %q", msg.ToolCallID), 1)
	}

	if msg.Name != "search_content" {
		ginkgo.Fail(fmt.Sprintf("expected Name=search_content, got %q", msg.Name), 1)
	}

	t.Logf("synthetic ID: %s", msg.ToolCallID)
})

// TestToolResultMessageHonorsRealCallID verifies that when a real
// call ID is present, it is used directly (no synthetic generation).
var _ = ginkgo.It("ToolResultMessageHonorsRealCallID", func() {
	call := &llama.ToolCall{
		ID:   "call_abc123",
		Type: "function",
		Function: llama.ToolCallFunc{
			Name:      "read_file",
			Arguments: `{"path":"main.go"}`,
		},
	}
	result := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read main.go",
	}

	msg := toolResultMessage(call, result)

	if msg.Role != "tool" {
		ginkgo.Fail(fmt.Sprintf("expected role=tool, got %q", msg.Role), 1)
	}

	if msg.ToolCallID != "call_abc123" {
		ginkgo.Fail(fmt.Sprintf("expected ToolCallID=call_abc123, got %q", msg.ToolCallID), 1)
	}
})

// TestToolResultMessageNilCallReturnsSystem verifies that when call
// is nil, the legacy system-role fallback is preserved.
var _ = ginkgo.It("ToolResultMessageNilCallReturnsSystem", func() {
	result := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read main.go",
	}

	msg := toolResultMessage(nil, result)

	if msg.Role != "system" {
		ginkgo.Fail(fmt.Sprintf("expected role=system for nil call, got %q", msg.Role), 1)
	}
})

var _ = ginkgo.It("SentinelFailuresPreserveErrorsIsAndCanonicalCodes", func() {
	tests := []struct {
		name string
		err  error
		code failures.Code
	}{
		{
			name: "completion gate rejected",
			err:  ErrCompletionGateRejected,
			code: failures.FailedPrecondition,
		},
		{
			name: "textual tool call",
			err:  ErrAgentTextualToolCall,
			code: failures.FailedPrecondition,
		},
		{
			name: "tool request",
			err:  ErrAgentToolRequest,
			code: failures.FailedPrecondition,
		},
		{
			name: "empty response",
			err:  ErrAgentEmptyResponse,
			code: failures.FailedPrecondition,
		},
	}

	for _, tt := range tests {
		ginkgo.By(tt.name, func() {
			wrapped := fmt.Errorf("wrapped: %w", tt.err)

			if !errors.Is(wrapped, tt.err) {
				ginkgo.Fail("errors.Is(wrapped, sentinel) = false", 1)
			}

			if got := failures.CodeOf(wrapped); got != tt.code {
				ginkgo.Fail(fmt.Sprintf("failures.CodeOf(wrapped) = %v, want %v", got, tt.code), 1)
			}
		})
	}
})

var _ = ginkgo.It("RunNoteToolWritesHarnessMetadataAtomically", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	result := runNoteTool(root, `{"key":"finding-1","content":"Important source fact."}`)

	if result.Error != "" {
		ginkgo.Fail(fmt.Sprintf("runNoteTool error: %s", result.Error), 1)
	}

	path := filepath.Join(root, ".idiom", "notes", "finding-1.md")

	data, err := os.ReadFile(path)
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("read note: %v", err), 1)
	}

	if got := string(data); got != "Important source fact.\n" {
		ginkgo.Fail(fmt.Sprintf("note content = %q", got), 1)
	}
})

var _ = ginkgo.It("RunNoteToolRejectsUnsafeKeys", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	keys := []string{"", "../x", "/abs", "a/b", `a\b`, ".hidden", strings.Repeat("a", noteToolMaxKeyLength+1), "snowman-☃"}

	for _, key := range keys {
		payload, _ := json.Marshal(map[string]string{"key": key, "content": "fact"})
		result := runNoteTool(root, string(payload))

		if result.Error == "" {
			ginkgo.Fail(fmt.Sprintf("expected key %q to be rejected, got %#v", key, result), 1)
		}
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "x.md")); !os.IsNotExist(err) {
		ginkgo.Fail(fmt.Sprintf("unsafe traversal created file or returned unexpected error: %v", err), 1)
	}
})

var _ = ginkgo.It("RunNoteToolRejectsSymlinkedNotesDirectoryAndTarget", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".idiom"), 0o700); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir .idiom: %v", err), 1)
	}

	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir outside: %v", err), 1)
	}

	if err := os.Symlink(outside, filepath.Join(root, ".idiom", "notes")); err != nil {
		ginkgo.Fail(fmt.Sprintf("symlink notes: %v", err), 1)
	}

	if result := runNoteTool(root, `{"key":"safe","content":"fact"}`); result.Error == "" {
		ginkgo.Fail(fmt.Sprintf("expected symlinked notes dir rejection, got %#v", result), 1)
	}

	root = t.TempDir()

	noteDir := filepath.Join(root, ".idiom", "notes")

	if err := os.MkdirAll(noteDir, 0o700); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir notes: %v", err), 1)
	}

	target := filepath.Join(root, "target.md")
	if err := os.WriteFile(target, []byte("outside\n"), 0o600); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed target: %v", err), 1)
	}

	if err := os.Symlink(target, filepath.Join(noteDir, "safe.md")); err != nil {
		ginkgo.Fail(fmt.Sprintf("symlink target: %v", err), 1)
	}

	if result := runNoteTool(root, `{"key":"safe","content":"fact"}`); result.Error == "" {
		ginkgo.Fail(fmt.Sprintf("expected symlink target rejection, got %#v", result), 1)
	}
})

var _ = ginkgo.It("WriteAutoFindingsNoteUsesHardenedWriter", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".idiom"), 0o700); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir .idiom: %v", err), 1)
	}

	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir outside: %v", err), 1)
	}

	if err := os.Symlink(outside, filepath.Join(root, ".idiom", "notes")); err != nil {
		ginkgo.Fail(fmt.Sprintf("symlink notes: %v", err), 1)
	}

	_, err := writeAutoFindingsNoteFromResults(root, "review note safety", []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read internal/runtime/agent/runner_core.go"},
	})
	if err == nil {
		ginkgo.Fail("expected auto-findings note to reject symlinked notes dir", 1)
	}

	if _, err := os.Stat(filepath.Join(outside, "auto-findings.md")); !os.IsNotExist(err) {
		ginkgo.Fail(fmt.Sprintf("auto-findings followed symlink or stat failed unexpectedly: %v", err), 1)
	}
})

var _ = ginkgo.It("RunNoteToolRejectsEmptyOrOversizedContent", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	if result := runNoteTool(root, `{"key":"safe","content":"   "}`); result.Error == "" {
		ginkgo.Fail("expected empty content rejection", 1)
	}

	payload, _ := json.Marshal(map[string]string{
		"key":     "safe",
		"content": strings.Repeat("x", noteToolMaxContentBytes+1),
	})
	if result := runNoteTool(root, string(payload)); result.Error == "" {
		ginkgo.Fail("expected oversized content rejection", 1)
	}
})

var _ = ginkgo.It("RunnerExecutesAllowedNoteTool", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	replies := []string{
		`{"tool":"note","key":"finding-1","content":"Observed Runner.Run note handling."}`,
		"Captured the finding.",
	}
	answer, events := runNoteToolScriptedConversation(t, root, replies, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "grounded_evidence_summary",
		PermissionProfile: "read_only_workspace",
		AllowedTools:      []string{"read_file"},
	})

	if answer != "Captured the finding." {
		ginkgo.Fail(fmt.Sprintf("answer = %q", answer), 1)
	}

	if !slicesContainsString(events, "note_tool") {
		ginkgo.Fail(fmt.Sprintf("expected note_tool event, got %v", events), 1)
	}

	data, err := os.ReadFile(filepath.Join(root, ".idiom", "notes", "finding-1.md"))
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("read note: %v", err), 1)
	}

	if !strings.Contains(string(data), "Runner.Run note handling") {
		ginkgo.Fail(fmt.Sprintf("note content not written: %q", string(data)), 1)
	}
})

var _ = ginkgo.It("RunnerDoesNotExposeNoteToolForDirectAnswer", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	replies := []string{
		`{"tool":"note","key":"finding-1","content":"should not write"}`,
		"Final answer after rejection.",
	}
	answer, events := runNoteToolScriptedConversation(t, root, replies, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "conversation_only",
	})

	if answer == "" {
		ginkgo.Fail("expected final answer", 1)
	}

	if slicesContainsString(events, "note_tool") {
		ginkgo.Fail(fmt.Sprintf("direct_answer must not execute note tool, events=%v", events), 1)
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "notes", "finding-1.md")); !os.IsNotExist(err) {
		ginkgo.Fail(fmt.Sprintf("direct_answer note file exists or stat failed unexpectedly: %v", err), 1)
	}
})

func runNoteToolScriptedConversation(t ginkgo.FullGinkgoTInterface, root string, replies []string, brief workerruntime.WorkerRuntimeBrief) (string, []string) {
	t.Helper()

	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()
		if r.URL.Path != "/v1/chat/completions" {
			ginkgo.Fail(fmt.Sprintf("unexpected path %s", r.URL.Path), 1)
		}

		if requests >= len(replies) {
			ginkgo.Fail("unexpected extra chat request", 1)
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))

	defer server.Close()

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	if err := mgr.SetMode(workspace.ModeExplore); err != nil {
		ginkgo.Fail(fmt.Sprintf("set explore mode: %v", err), 1)
	}

	events := make([]string, 0)
	baseRunner := &Runner{
		Client:       llama.NewClient(server.URL, time.Second),
		Workspace:    mgr,
		RuntimeBrief: brief,
		Policy: RunPolicy{
			MaxToolSteps:       4,
			MaxNoProgressTurns: 4,
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event.Role)
		},
	}

	answer, err := baseRunner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "capture a finding"}}, llama.ChatOptions{}, nil, nil, nil)
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("Run: %v", err), 1)
	}

	return answer, events
}

func slicesContainsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}

	return false
}

var _ = ginkgo.It("IntentCarriesTransportNeutralToolRequest", func() {
	intent := Intent{
		ToolName:    "read_file",
		ActionClass: "read",
		Path:        "README.md",
		Transport:   "native",
		ParserState: "normalized",
	}

	if intent.ToolName != "read_file" || intent.ActionClass != "read" || intent.Transport != "native" {
		ginkgo.Fail(fmt.Sprintf("unexpected intent: %#v", intent), 1)
	}
})

var _ = ginkgo.It("ManagerCreatesDiffsAndRemovesWorktree", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test User")

	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write seed file: %v", err), 1)
	}

	runGit(t, root, "add", "README.md")
	runGit(t, root, "commit", "-m", "init")

	manager := Manager{
		Root:    root,
		Scratch: t.TempDir(),
		Prefix:  "codex/test",
	}

	checkout, err := manager.Create(context.Background(), "Refactor App.go!")
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("Create: %v", err), 1)
	}

	if checkout.Name != "refactor-app-go" || !strings.HasPrefix(checkout.Branch, "codex/test/refactor-app-go-") {
		ginkgo.Fail(fmt.Sprintf("unexpected checkout identity: %#v", checkout), 1)
	}

	if _, err := os.Stat(filepath.Join(checkout.Path, "README.md")); err != nil {
		ginkgo.Fail(fmt.Sprintf("expected worktree checkout: %v", err), 1)
	}

	if err := os.WriteFile(filepath.Join(checkout.Path, "README.md"), []byte("hello\nworld\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write worktree file: %v", err), 1)
	}

	diff, err := manager.Diff(context.Background(), checkout)
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("Diff: %v", err), 1)
	}

	if len(diff.ChangedPaths) != 1 || diff.ChangedPaths[0] != "README.md" {
		ginkgo.Fail(fmt.Sprintf("unexpected changed paths: %#v", diff), 1)
	}

	if diff.Stat == "" {
		ginkgo.Fail("expected diff stat", 1)
	}

	if !strings.Contains(diff.Patch, "+world") {
		ginkgo.Fail(fmt.Sprintf("expected binary patch to include changed file contents, got:\n%s", diff.Patch), 1)
	}

	if err := manager.Remove(context.Background(), checkout); err != nil {
		ginkgo.Fail(fmt.Sprintf("Remove: %v", err), 1)
	}

	if _, err := os.Stat(checkout.Path); !os.IsNotExist(err) {
		ginkgo.Fail(fmt.Sprintf("expected worktree path removed, stat err=%v", err), 1)
	}

	if branches := runGitOutput(t, root, "branch", "--list", checkout.Branch); strings.TrimSpace(branches) != "" {
		ginkgo.Fail(fmt.Sprintf("expected temporary branch removed, got %q", branches), 1)
	}

	second, err := manager.Create(context.Background(), "Refactor App.go!")
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("Create with same logical name after cleanup: %v", err), 1)
	}

	if second.Branch == checkout.Branch || second.Path == checkout.Path {
		ginkgo.Fail(fmt.Sprintf("expected unique branch and path after repeated create, first=%#v second=%#v", checkout, second), 1)
	}

	if err := manager.Remove(context.Background(), second); err != nil {
		ginkgo.Fail(fmt.Sprintf("Remove second checkout: %v", err), 1)
	}
})

var _ = ginkgo.It("DiffIncludesUntrackedFiles", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test User")

	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write seed file: %v", err), 1)
	}

	runGit(t, root, "add", "README.md")
	runGit(t, root, "commit", "-m", "init")

	manager := Manager{
		Root:    root,
		Scratch: t.TempDir(),
		Prefix:  "codex/test",
	}

	checkout, err := manager.Create(context.Background(), "new-file")
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("Create: %v", err), 1)
	}

	defer func() {
		if err := manager.Remove(context.Background(), checkout); err != nil {
			ginkgo.Fail(fmt.Sprintf("Remove: %v", err), 1)
		}
	}()

	if err := os.WriteFile(filepath.Join(checkout.Path, "NEW.md"), []byte("new file\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write untracked file: %v", err), 1)
	}

	diff, err := manager.Diff(context.Background(), checkout)
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("Diff: %v", err), 1)
	}

	if len(diff.ChangedPaths) != 1 || diff.ChangedPaths[0] != "NEW.md" {
		ginkgo.Fail(fmt.Sprintf("expected untracked file in changed paths, got %#v", diff.ChangedPaths), 1)
	}

	if !strings.Contains(diff.Patch, "new file mode") || !strings.Contains(diff.Patch, "+new file") {
		ginkgo.Fail(fmt.Sprintf("expected untracked file patch, got:\n%s", diff.Patch), 1)
	}
})

func runGit(t ginkgo.FullGinkgoTInterface, root string, args ...string) {
	t.Helper()

	_ = runGitOutput(t, root, args...)
}

func runGitOutput(t ginkgo.FullGinkgoTInterface, root string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("git %v: %v\n%s", args, err, output), 1)
	}

	return string(output)
}

var _ = ginkgo.It("ToolResultEvidenceProjectionUsesTypedReadPathsNotMentionRelationships", func() {
	result := readFileSourceWindowResult(
		"internal/runtime/agent/runner_core.go", 1, 20, 20,
		"package agent\nfunc Run() {}\n",
	)
	result.Code = "ok"
	result.PrecisionTier = "source_body"
	result.Relationships = []workspace.ToolRelationship{
		{Kind: "reads", SourcePath: "./internal/runtime/agent/runner_core.go"},
		{Kind: "mentions", TargetPath: "internal\\runtime\\agent\\evidence_registry.go"},
		{Kind: "reads", SourcePath: "internal/runtime/agent/runner_core.go"},
	}

	projection := newToolResultEvidenceProjection([]workspace.ToolResult{result})
	if !projection.HasSourceWindow("internal/runtime/agent/runner_core.go") {
		ginkgo.Fail("expected typed read path to count as source-body evidence", 1)
	}
	if projection.HasSourceWindow("internal/runtime/agent/evidence_registry.go") {
		ginkgo.Fail("mention-only relationship must not count as source-body evidence", 1)
	}
})

var _ = ginkgo.It("FailedReadFileMissingPathIsNotAcceptedEvidence", func() {
	const (
		missingPath   = "internal/runtime/agent/missing.go"
		inspectedPath = "internal/runtime/agent/runner_core.go"
	)

	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read internal/runtime/agent/missing.go lines 1-80",
			Error:   "internal/runtime/agent/missing.go was not found",
		},
		{
			Tool:    "search_content",
			Summary: "No matches in internal/runtime/agent/zero_hit.go",
			Output:  "0 matching line(s)",
		},
		readFileSourceWindowResult(
			inspectedPath,
			1,
			8,
			0,
			"package agent\n\nfunc evidenceTelemetryRegressionFixture() {}\n",
		),
	}

	for name, paths := range map[string][]string{
		"direct probes":           newToolResultEvidenceProjection(results).directProbePaths(8),
		"mentioned evidence":      newToolResultEvidenceProjection(results).MentionedEvidencePathsForObjective("", 8),
		"implementation evidence": newToolResultEvidenceProjection(results).acceptedImplementationEvidencePaths(),
	} {
		if slices.Contains(paths, missingPath) {
			ginkgo.Fail(fmt.Sprintf("%s promoted missing read_file path as accepted evidence: %#v", name, paths), 1)
		}

		if slices.Contains(paths, "internal/runtime/agent/zero_hit.go") {
			ginkgo.Fail(fmt.Sprintf("%s promoted zero-hit search path as accepted evidence: %#v", name, paths), 1)
		}

		if !slices.Contains(paths, inspectedPath) {
			ginkgo.Fail(fmt.Sprintf("%s should preserve successful read_file evidence path %q, got %#v", name, inspectedPath, paths), 1)
		}
	}
})

var _ = ginkgo.It("ModelCallTelemetryDistinguishesParsedTextualTool", func() {
	var event RunEvent

	runner := newRunnerRunFixture(&Runner{
		OnEvent: func(got RunEvent) {
			event = got
		},
	})

	runner.emitModelCallTraceWithOptions(
		1,
		"regression",
		[]llama.ChatMessage{{Role: "user", Content: "inspect runner"}},
		llama.ChatMessage{
			Role:    "assistant",
			Content: `{"tool":"read_file","path":"internal/runtime/agent/runner_core.go"}`,
		},
		7,
		llama.ChatOptions{},
		false,
		nil,
	)

	if event.Role != "model_call" {
		ginkgo.Fail(fmt.Sprintf("expected model_call event, got %#v", event), 1)
	}

	var payload map[string]any

	if err := json.Unmarshal([]byte(event.Content), &payload); err != nil {
		ginkgo.Fail(fmt.Sprintf("decode model_call payload: %v", err), 1)
	}

	if payload["chosen_tool"] != "<answer>" {
		ginkgo.Fail(fmt.Sprintf("textual tool request must not masquerade as native chosen_tool, got %#v", payload), 1)
	}

	if payload["parsed_textual_tool"] != "read_file" {
		ginkgo.Fail(fmt.Sprintf("model_call telemetry missing parsed textual tool identity: %#v", payload), 1)
	}

	if payload["parsed_tool_transport"] != "textual" {
		ginkgo.Fail(fmt.Sprintf("model_call telemetry missing textual transport: %#v", payload), 1)
	}

	if payload["parsed_tool_request_valid"] != true {
		ginkgo.Fail(fmt.Sprintf("model_call telemetry should mark parsed textual request valid: %#v", payload), 1)
	}
})

var _ = ginkgo.It("ModelCallTelemetryReportsParsedKeyValueWriteWithoutRawEditText", func() {
	var event RunEvent

	runner := newRunnerRunFixture(&Runner{
		OnEvent: func(got RunEvent) {
			event = got
		},
	})

	runner.emitModelCallTraceWithOptions(
		1,
		"regression",
		[]llama.ChatMessage{{Role: "user", Content: "edit the label"}},
		llama.ChatMessage{
			Role:    "assistant",
			Content: "tool: edit_file\npath: internal/labels/labels.go\nold_text: return \"Save changes\"\nnew_text: return \"Save\"\ndescription: update label",
		},
		7,
		llama.ChatOptions{},
		false,
		nil,
	)

	if event.Role != "model_call" {
		ginkgo.Fail(fmt.Sprintf("expected model_call event, got %#v", event), 1)
	}

	var payload map[string]any

	if err := json.Unmarshal([]byte(event.Content), &payload); err != nil {
		ginkgo.Fail(fmt.Sprintf("decode model_call payload: %v", err), 1)
	}

	if payload["chosen_tool"] != "<answer>" {
		ginkgo.Fail(fmt.Sprintf("textual write request must not masquerade as native chosen_tool, got %#v", payload), 1)
	}

	if payload["parsed_textual_tool"] != "edit_file" || payload["parsed_tool_transport"] != toolRequestNormalizerTransportKeyValue || payload["parsed_tool_request_valid"] != true {
		ginkgo.Fail(fmt.Sprintf("model_call telemetry should report parsed key-value write shape without treating it as native: %#v", payload), 1)
	}

	for _, forbidden := range []string{"executed_textual_tool", "textual_tool_transport", "old_text", "new_text"} {
		if _, ok := payload[forbidden]; ok {
			ginkgo.Fail(fmt.Sprintf("textual write telemetry must not emit raw edit field %q: %#v", forbidden, payload), 1)
		}
	}
})

var _ = ginkgo.It("ToolRequestNormalizerTelemetryExposesParsedTextualTool", func() {
	var event RunEvent

	runner := newRunnerRunFixture(&Runner{
		OnEvent: func(got RunEvent) {
			event = got
		},
	})

	runner.emitToolRequestNormalizerEvent(
		toolRequestNormalizerOutcomeNormalized,
		toolRequestNormalizerTransportTextual,
		workspace.ToolRequest{Tool: "read_file", Path: "internal/runtime/agent/runner_core.go"},
		nil,
		autonomy.ParserFailureNone,
	)

	if event.Role != "tool_request_normalizer" {
		ginkgo.Fail(fmt.Sprintf("expected tool_request_normalizer event, got %#v", event), 1)
	}

	var payload map[string]any

	if err := json.Unmarshal([]byte(event.Content), &payload); err != nil {
		ginkgo.Fail(fmt.Sprintf("decode tool_request_normalizer payload: %v", err), 1)
	}

	if payload["tool"] != "read_file" {
		ginkgo.Fail(fmt.Sprintf("existing tool field should remain compatible, got %#v", payload), 1)
	}

	if payload["parsed_textual_tool"] != "read_file" {
		ginkgo.Fail(fmt.Sprintf("normalizer telemetry missing parsed textual tool identity: %#v", payload), 1)
	}

	if _, ok := payload["executed_textual_tool"]; ok {
		ginkgo.Fail(fmt.Sprintf("normalizer telemetry is pre-dispatch and must not claim execution: %#v", payload), 1)
	}

	if payload["textual_tool_transport"] != "textual" {
		ginkgo.Fail(fmt.Sprintf("normalizer telemetry missing textual transport: %#v", payload), 1)
	}
})

var _ = ginkgo.It("ToolRequestNormalizerTelemetryDoesNotTreatDeterministicRecoveryAsTextualTool", func() {
	var event RunEvent

	runner := newRunnerRunFixture(&Runner{
		OnEvent: func(got RunEvent) {
			event = got
		},
	})

	runner.emitToolRequestNormalizerEvent(
		toolRequestNormalizerOutcomeRequiresTerminal,
		"deterministic_required_source_recovery",
		workspace.ToolRequest{Tool: "read_file", Path: "internal/runtime/agent/runner_core.go"},
		nil,
		autonomy.ParserFailureNone,
	)

	if event.Role != "tool_request_normalizer" {
		ginkgo.Fail(fmt.Sprintf("expected tool_request_normalizer event, got %#v", event), 1)
	}

	var payload map[string]any

	if err := json.Unmarshal([]byte(event.Content), &payload); err != nil {
		ginkgo.Fail(fmt.Sprintf("decode tool_request_normalizer payload: %v", err), 1)
	}

	if payload["tool"] != "read_file" {
		ginkgo.Fail(fmt.Sprintf("existing tool field should remain compatible, got %#v", payload), 1)
	}

	for _, forbidden := range []string{"parsed_textual_tool", "executed_textual_tool", "textual_tool_transport"} {
		if _, ok := payload[forbidden]; ok {
			ginkgo.Fail(fmt.Sprintf("deterministic recovery must not be labeled as model textual tool telemetry via %q: %#v", forbidden, payload), 1)
		}
	}
})

var _ = ginkgo.It("BlockedReadOnlyDirectAnswerTelemetryKeepsAcceptedEvidenceEmpty", func() {
	var events []RunEvent

	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain telemetry accounting.",
		},
		OnEvent: func(got RunEvent) {
			events = append(events, got)
		},
	})
	results := []workspace.ToolResult{
		readFileSourceWindowResult(
			"internal/runtime/agent/runner_core.go",
			1,
			20,
			0,
			"package agent\n\nfunc telemetryAccounting() {}\n",
		),
	}
	acceptance := finalAnswerAcceptance{
		SchemaVersion: finalAnswerAcceptanceSchema,
		Decision:      finalAnswerAcceptanceRejected,
		BlockerKind:   "answer_objective_unmet",
		Reason:        "answer did not address the objective",
		EvidencePaths: nil,
	}

	runner.emitFinalAnswerTelemetry("This answer is blocked.", results, acceptance)

	if len(events) != 3 {
		ginkgo.Fail(fmt.Sprintf("expected three blocked telemetry events, got %#v", events), 1)
	}

	for _, event := range events {
		var payload map[string]any

		if err := json.Unmarshal([]byte(event.Content), &payload); err != nil {
			ginkgo.Fail(fmt.Sprintf("decode %s payload: %v", event.Role, err), 1)
		}

		if pathsLen(payload["evidence_paths"]) != 0 {
			ginkgo.Fail(fmt.Sprintf("%s must keep rejected accepted evidence empty, got %#v", event.Role, payload), 1)
		}

		if got := stringSliceFromJSON(payload["presentation_evidence_paths"]); !slices.Contains(got, "internal/runtime/agent/runner_core.go") {
			ginkgo.Fail(fmt.Sprintf("%s should expose presentation evidence separately, got %#v", event.Role, payload), 1)
		}
	}
})

var _ = ginkgo.It("AcceptedReadOnlyDirectAnswerTelemetryKeepsAcceptedEvidenceSeparateFromPresentation", func() {
	var events []RunEvent

	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain telemetry accounting.",
		},
		OnEvent: func(got RunEvent) {
			events = append(events, got)
		},
	})
	results := []workspace.ToolResult{
		readFileSourceWindowResult(
			"internal/runtime/agent/runner_core.go",
			1,
			20,
			0,
			"package agent\n\nfunc telemetryAccounting() {}\n",
		),
	}
	acceptance := finalAnswerAcceptance{
		SchemaVersion: finalAnswerAcceptanceSchema,
		Decision:      finalAnswerAcceptanceAccepted,
		EvidencePaths: nil,
	}

	runner.emitFinalAnswerTelemetry("Telemetry accounting is implemented in internal/runtime/agent/runner_core.go.", results, acceptance)

	if len(events) != 3 {
		ginkgo.Fail(fmt.Sprintf("expected three accepted telemetry events, got %#v", events), 1)
	}

	for _, event := range events {
		var payload map[string]any

		if err := json.Unmarshal([]byte(event.Content), &payload); err != nil {
			ginkgo.Fail(fmt.Sprintf("decode %s payload: %v", event.Role, err), 1)
		}

		if pathsLen(payload["evidence_paths"]) != 0 {
			ginkgo.Fail(fmt.Sprintf("%s must keep accepted evidence empty when acceptance has none, got %#v", event.Role, payload), 1)
		}

		if got := stringSliceFromJSON(payload["presentation_evidence_paths"]); !slices.Contains(got, "internal/runtime/agent/runner_core.go") {
			ginkgo.Fail(fmt.Sprintf("%s should expose presentation evidence separately, got %#v", event.Role, payload), 1)
		}
	}
})

var _ = ginkgo.It("AcceptedFinalAnswerTelemetryPreservesAcceptedEvidencePaths", func() {
	var events []RunEvent

	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain accepted evidence accounting.",
		},
		OnEvent: func(got RunEvent) {
			events = append(events, got)
		},
	})
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/runtime/agent/presentation_only.go lines 1-20",
		Output:  "package agent\n\nfunc presentationOnly() {}\n",
	}}
	acceptance := finalAnswerAcceptance{
		SchemaVersion: finalAnswerAcceptanceSchema,
		Decision:      finalAnswerAcceptanceAccepted,
		EvidencePaths: []string{"internal/runtime/agent/accepted.go"},
	}

	runner.emitFinalAnswerTelemetry("Accepted evidence was already computed by the gate.", results, acceptance)

	if len(events) != 3 {
		ginkgo.Fail(fmt.Sprintf("expected three accepted telemetry events, got %#v", events), 1)
	}

	for _, event := range events {
		var payload map[string]any

		if err := json.Unmarshal([]byte(event.Content), &payload); err != nil {
			ginkgo.Fail(fmt.Sprintf("decode %s payload: %v", event.Role, err), 1)
		}

		got := stringSliceFromJSON(payload["evidence_paths"])
		if !slices.Contains(got, "internal/runtime/agent/accepted.go") || slices.Contains(got, "internal/runtime/agent/presentation_only.go") {
			ginkgo.Fail(fmt.Sprintf("%s should preserve accepted evidence paths without presentation replacement, got %#v", event.Role, payload), 1)
		}
	}
})

func pathsLen(value any) int {
	switch typed := value.(type) {
	case nil:
		return 0
	case []any:
		return len(typed)
	default:
		return 1
	}
}

func stringSliceFromJSON(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}

	out := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			out = append(out, text)
		}
	}

	return out
}

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

var _ = ginkgo.It("ReadOnlyAnswerabilityContextPacketOnlyLocalizes", func() {
	brief := readOnlyConversationBriefForTest("Explain runner answer readiness.")
	results := []workspace.ToolResult{{
		Tool:    "context_pack",
		Summary: "Context pack wrote .idiom/comprehension/views/pack.json",
		Facts:   contextPackCandidateFactsForTest("internal/runtime/agent/runner_core.go"),
	}}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() {
		ginkgo.Fail(fmt.Sprintf("context_pack alone must not make direct answer ready: %#v", ledger), 1)
	}

	if !ledger.HasKind(readOnlyEvidenceContextPacket) {
		ginkgo.Fail(fmt.Sprintf("expected context packet item, got %#v", ledger.Items), 1)
	}

	if !slices.Contains(ledger.CandidatePaths, "internal/runtime/agent/runner_core.go") {
		ginkgo.Fail(fmt.Sprintf("expected context candidate path, got %#v", ledger.CandidatePaths), 1)
	}

	if conversationEvidenceSatisfied(brief, results) {
		ginkgo.Fail("direct-answer readiness must not be satisfied by context packet localization only", 1)
	}
})

var _ = ginkgo.It("ReadOnlyAnswerabilityHighCardinalitySearchRequiresReturnedCandidateRead", func() {
	brief := readOnlyConversationBriefForTest("Explain completion gate behavior.")
	results := []workspace.ToolResult{
		{
			Tool:    "search_content",
			Summary: "Found 178 matching line(s) (showing first 50).",
			Output:  "internal/runtime/agent/runner_core.go:420: completion gate\n",
			Facts: &toolfacts.ToolResultFacts{
				CandidatePaths:        []string{"internal/runtime/agent/runner_core.go"},
				HasDiscoveryTargets:   true,
				DiscoveryStrength:     toolfacts.DiscoveryStrengthCandidateOnly,
				SearchHitCount:        178,
				SearchHighCardinality: true,
			},
		},
		readFileSourceWindowResult(
			"internal/surfaces/tui/app/model_state.go",
			1,
			80,
			0,
			"package app\nfunc unrelated() {}\n",
		),
	}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if !ledger.HighCardinalitySearch {
		ginkgo.Fail(fmt.Sprintf("expected high-cardinality search to be recorded: %#v", ledger), 1)
	}

	if ledger.Answerable() {
		ginkgo.Fail(fmt.Sprintf("unrelated source body must not satisfy high-cardinality search evidence: %#v", ledger), 1)
	}

	if conversationEvidenceSatisfied(brief, results) {
		ginkgo.Fail("direct-answer readiness must remain false until a returned candidate is read", 1)
	}

	results[1] = readFileSourceWindowResult(
		"internal/runtime/agent/runner_core.go",
		400,
		460,
		0,
		"package agent\nfunc completionGateBehavior() {}\n",
	)
	ledger = readOnlyAnswerabilityForResults(brief, results)

	if !ledger.Answerable() {
		ginkgo.Fail(fmt.Sprintf("source body from returned candidate should make answer ready: %#v", ledger), 1)
	}

	if !slices.Contains(ledger.RelevantSourceBodyPaths, "internal/runtime/agent/runner_core.go") {
		ginkgo.Fail(fmt.Sprintf("expected candidate source body to be relevant, got %#v", ledger.RelevantSourceBodyPaths), 1)
	}

	if !conversationEvidenceSatisfied(brief, results) {
		ginkgo.Fail("direct-answer readiness should be satisfied by source body from returned candidate", 1)
	}
})

var _ = ginkgo.It("ReadOnlyAnswerabilityOutlinesAndGeneratedArtifactsDoNotAnswer", func() {
	brief := readOnlyConversationBriefForTest("Explain runner answer readiness.")
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Outlined 12 top-level declaration(s) in internal/runtime/agent/runner_core.go",
			Output:  "file_outline: internal/runtime/agent/runner_core.go\n",
		},
		readFileSourceWindowResult(
			".idiom/comprehension/views/pack.json",
			1,
			20,
			0,
			`{"candidate_files":[{"path":"internal/runtime/agent/runner_core.go"}]}`,
		),
	}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() {
		ginkgo.Fail(fmt.Sprintf("outline plus generated artifact should not be answerable: %#v", ledger), 1)
	}

	if !ledger.HasKind(readOnlyEvidenceOutline) {
		ginkgo.Fail(fmt.Sprintf("expected outline evidence item, got %#v", ledger.Items), 1)
	}

	if !ledger.HasKind(readOnlyEvidenceGeneratedArtifact) {
		ginkgo.Fail(fmt.Sprintf("expected generated artifact evidence item, got %#v", ledger.Items), 1)
	}
})

var _ = ginkgo.It("ReadOnlyAnswerabilityContextPackCandidateRequiresObjectiveRelevantSourceBody", func() {
	brief := readOnlyConversationBriefForTest("Research the TUI chat input newline rendering bug.")
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/pack.json",
			Facts:   contextPackCandidateFactsForTest("internal/surfaces/tui/app/rendering.go"),
		},
		codeNavigateSourceWindowResult(
			"internal/surfaces/tui/app/rendering.go",
			32,
			70,
			0,
			strings.Join([]string{
				"capability: read_symbol",
				"language: go",
				"routed_tool: go_read_symbol",
				"route_summary: Read Model.View in internal/surfaces/tui/app/rendering.go lines 32-70",
				"",
				"32:func (m Model) View() string {",
				"33:    return \"Loading UI...\"",
				"34:}",
			}, "\n"),
		),
	}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() {
		ginkgo.Fail(fmt.Sprintf("context_pack candidate membership alone must not make unrelated source body answerable: %#v", ledger), 1)
	}

	if conversationEvidenceSatisfied(brief, results) {
		ginkgo.Fail("direct-answer readiness must remain false for source body that lacks objective relevance", 1)
	}

	if slices.Contains(ledger.RelevantSourceBodyPaths, "internal/surfaces/tui/app/rendering.go") {
		ginkgo.Fail(fmt.Sprintf("unrelated context candidate source body should not be relevant, got %#v", ledger.RelevantSourceBodyPaths), 1)
	}

	results[1] = codeNavigateSourceWindowResult(
		"internal/surfaces/tui/app/rendering.go",
		120,
		150,
		0,
		strings.Join([]string{
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
	)
	ledger = readOnlyAnswerabilityForResults(brief, results)

	if !ledger.Answerable() {
		ginkgo.Fail(fmt.Sprintf("objective-relevant source body from context candidate should be answerable: %#v", ledger), 1)
	}
})

var _ = ginkgo.It("ReadOnlyAnswerabilityPromotesCollectiveSourceBodyObjectiveSupport", func() {
	brief := readOnlyConversationBriefForTest("How do up and down arrow keys navigate conversation history?")
	contextOnly := []workspace.ToolResult{{
		Tool:    "context_pack",
		Summary: "Context pack returned weak candidates",
		Facts: contextPackCandidateFactsForTest(
			"internal/surfaces/tui/app/update_handlers.go",
			"internal/surfaces/tui/app/rendering.go",
		),
	}}

	ledger := readOnlyAnswerabilityForResults(brief, contextOnly)
	if ledger.Answerable() {
		ginkgo.Fail(fmt.Sprintf("context packet candidates alone must not satisfy answerability: %#v", ledger), 1)
	}

	partialResults := append([]workspace.ToolResult{}, contextOnly...)

	partialResults = append(partialResults,
		readFileSourceWindowResult(
			"internal/surfaces/tui/app/update_handlers.go",
			80,
			120,
			0,
			strings.Join([]string{
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
		),
		readFileSourceWindowResult(
			"internal/surfaces/tui/app/rendering.go",
			300,
			330,
			0,
			strings.Join([]string{
				"300:func (m Model) renderViewport() string {",
				"301:\treturn m.viewport.View()",
				"302:}",
			}, "\n"),
		),
	)
	ledger = readOnlyAnswerabilityForResults(brief, partialResults)

	if ledger.Answerable() {
		ginkgo.Fail(fmt.Sprintf("source bodies missing one objective term must not satisfy collective relevance: %#v", ledger), 1)
	}

	if conversationEvidenceSatisfied(brief, partialResults) {
		ginkgo.Fail("conversation readiness must remain false until source bodies collectively cover the objective", 1)
	}

	completeResults := append([]workspace.ToolResult{}, contextOnly...)

	completeResults = append(completeResults, partialResults[1])
	completeResults[1].Output = strings.ReplaceAll(completeResults[1].Output, "m.navigateHistory(-1)", "m.navigateHistory(-1) // arrow-key history")
	completeResults = append(completeResults, readFileSourceWindowResult(
		"internal/surfaces/tui/app/rendering.go",
		300,
		330,
		0,
		strings.Join([]string{
			"300:func (m Model) renderConversationViewport() string {",
			"301:\treturn m.messages.View()",
			"302:}",
		}, "\n"),
	))

	ledger = readOnlyAnswerabilityForResults(brief, completeResults)
	if !ledger.Answerable() {
		ginkgo.Fail(fmt.Sprintf("collective source-body objective coverage should be answerable: %#v", ledger), 1)
	}

	if !slices.Contains(ledger.SourceBodyObjectivePaths, "internal/surfaces/tui/app/update_handlers.go") {
		ginkgo.Fail(fmt.Sprintf("expected substantive collective objective path, got %#v", ledger.SourceBodyObjectivePaths), 1)
	}

	if slices.Contains(ledger.SourceBodyObjectivePaths, "internal/surfaces/tui/app/rendering.go") {
		ginkgo.Fail(fmt.Sprintf("one-term companion source body must not be promoted as objective support, got %#v", ledger.SourceBodyObjectivePaths), 1)
	}

	if !slices.Contains(ledger.RelevantSourceBodyPaths, "internal/surfaces/tui/app/update_handlers.go") {
		ginkgo.Fail(fmt.Sprintf("expected relevant source body path, got %#v", ledger.RelevantSourceBodyPaths), 1)
	}

	if !conversationEvidenceSatisfied(brief, completeResults) {
		ginkgo.Fail("conversation readiness should be satisfied by collective source-body objective coverage", 1)
	}
})

var _ = ginkgo.It("FinalAnswerAcceptanceAllowsCollectiveSourceBodyObjectiveAnswer", func() {
	brief := readOnlyConversationBriefForTest("How do up and down arrow keys navigate conversation history?")
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack returned weak candidates",
			Facts: contextPackCandidateFactsForTest(
				"internal/surfaces/tui/app/update_handlers.go",
				"internal/surfaces/tui/app/rendering.go",
			),
		},
		readFileSourceWindowResult(
			"internal/surfaces/tui/app/update_handlers.go",
			80,
			120,
			0,
			strings.Join([]string{
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
		),
		readFileSourceWindowResult(
			"internal/surfaces/tui/app/rendering.go",
			300,
			330,
			0,
			strings.Join([]string{
				"300:func (m Model) renderConversationViewport() string {",
				"301:\treturn m.messages.View()",
				"302:}",
			}, "\n"),
		),
	}
	answer := "Up and down arrow keys navigate conversation history through `internal/surfaces/tui/app/update_handlers.go`, where the key handler calls `navigateHistory`, and `internal/surfaces/tui/app/rendering.go` renders the conversation viewport that displays the resulting message state."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
	results = bindCurrentSourceWindowsForContractTest(ginkgo.GinkgoT(), runner, results)

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "" || reason != "" {
		ginkgo.Fail(fmt.Sprintf("collective source-body answer blocked: kind=%q reason=%q", kind, reason), 1)
	}
})

var _ = ginkgo.It("FinalAnswerAcceptanceAllowsWhatWouldItTakeAnswerFromDiscoveredTUISources", func() {
	brief := readOnlyConversationBriefForTest("what would it take to make it so that the up and down arrow keys jump between messages from conversation history?")
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote weak packet",
			Facts: contextPackCandidateFactsForTest(
				"internal/runtime/agent/runner_core.go",
				"internal/surfaces/tui/app/app.go",
			),
		},
		readFileSourceWindowResult(
			"internal/surfaces/tui/app/update_handlers.go",
			20,
			90,
			0,
			strings.Join([]string{
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
		),
		readFileSourceWindowResult(
			"internal/surfaces/tui/app/rendering.go",
			1,
			100,
			0,
			strings.Join([]string{
				"1:package app",
				"2:func (m *Model) renderMessages(force bool) {",
				"3:\tm.viewport.SetContent(strings.Join(m.messages, \"\\n\"))",
				"4:}",
			}, "\n"),
		),
	}
	answer := "It would take wiring the up/down key cases in `internal/surfaces/tui/app/update_handlers.go` to select conversation-history messages and fill the input with the selected message text instead of treating those keys only as the existing panel navigation behavior."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
	results = bindCurrentSourceWindowsForContractTest(ginkgo.GinkgoT(), runner, results)

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "" || reason != "" {
		ginkgo.Fail(fmt.Sprintf("what-would-it-take answer from discovered TUI source bodies blocked: kind=%q reason=%q", kind, reason), 1)
	}

	paths := acceptedFinalAnswerEvidencePaths(answer, brief, results)
	if !slices.Contains(paths, "internal/surfaces/tui/app/update_handlers.go") {
		ginkgo.Fail(fmt.Sprintf("expected accepted evidence path from the strongest source-body support, got %#v", paths), 1)
	}

	if slices.Contains(paths, "internal/surfaces/tui/app/rendering.go") {
		ginkgo.Fail(fmt.Sprintf("accepted evidence paths should not promote one-term companion citation, got %#v", paths), 1)
	}

	if slices.Contains(paths, "internal/runtime/agent/runner_core.go") {
		ginkgo.Fail(fmt.Sprintf("accepted evidence paths must not retain weak context-pack candidate, got %#v", paths), 1)
	}
})

func assertObjectiveRelevanceAdvisoryForTest(t ginkgo.FullGinkgoTInterface, runner *runExecution, answer string, results []workspace.ToolResult, wantKind string) readOnlyDirectAnswerRelevance {
	t.Helper()
	results = bindCurrentSourceWindowsForContractTest(t, runner, results)

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "" || reason != "" {
		ginkgo.Fail(fmt.Sprintf("objective relevance advisory must not reject final answer: kind=%q reason=%q", kind, reason), 1)
	}

	acceptance := runner.evaluateFinalAnswerAcceptance(answer, results)
	if acceptance.Decision != finalAnswerAcceptanceAccepted || acceptance.PolicyDecision != autonomy.PolicyAllow {
		ginkgo.Fail(fmt.Sprintf("objective relevance advisory must leave acceptance allowed, got %#v", acceptance), 1)
	}
	if !acceptance.ContractAssessment.HasDiagnostic(turnDiagnosticObjectiveRelevance) ||
		len(acceptance.ContractAssessment.Violations) > 0 {
		ginkgo.Fail(fmt.Sprintf("expected objective diagnostic without enforceable violations, got %#v", acceptance.ContractAssessment), 1)
	}

	projection := newToolResultEvidenceProjection(results)
	relevance := readOnlyClosureDecisionForProjection(
		readOnlyDirectAnswerAcceptanceLedgerBrief(runner.state.runtimeBrief),
		projection,
	).DirectAnswerRelevance(answer)
	if relevance.BlockerKind != wantKind {
		ginkgo.Fail(fmt.Sprintf("objective relevance advisory got kind=%q reason=%q, want %q", relevance.BlockerKind, relevance.Reason, wantKind), 1)
	}
	if !finalAnswerObjectiveRelevanceAdvisoryKind(relevance.BlockerKind) {
		ginkgo.Fail(fmt.Sprintf("objective relevance kind should be advisory: %#v", relevance), 1)
	}

	return relevance
}

func bindCurrentSourceWindowsForContractTest(t ginkgo.FullGinkgoTInterface, runner *runExecution, results []workspace.ToolResult) []workspace.ToolResult {
	t.Helper()

	type sourceFile struct {
		lines   []string
		present []bool
	}

	files := make(map[string]*sourceFile)
	for _, result := range results {
		for _, window := range result.SourceWindows() {
			evidence := window.Evidence
			if evidence.StartLine <= 0 || evidence.EndLine < evidence.StartLine || evidence.TotalLines < evidence.EndLine {
				ginkgo.Fail(fmt.Sprintf("invalid source-window fixture metadata: %#v", evidence), 1)
			}
			cleanPath := filepath.Clean(filepath.FromSlash(evidence.CanonicalID))
			if cleanPath == "." || cleanPath == ".." || filepath.IsAbs(cleanPath) || strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) {
				ginkgo.Fail(fmt.Sprintf("source-window fixture path is not workspace relative: %q", evidence.CanonicalID), 1)
			}

			file := files[evidence.CanonicalID]
			if file == nil {
				file = &sourceFile{
					lines:   make([]string, evidence.TotalLines),
					present: make([]bool, evidence.TotalLines),
				}
				files[evidence.CanonicalID] = file
			} else if len(file.lines) < evidence.TotalLines {
				file.lines = append(file.lines, make([]string, evidence.TotalLines-len(file.lines))...)
				file.present = append(file.present, make([]bool, evidence.TotalLines-len(file.present))...)
			}

			windowLines := strings.Split(window.Content, "\n")
			if got, want := len(windowLines), evidence.EndLine-evidence.StartLine+1; got != want {
				ginkgo.Fail(fmt.Sprintf("source-window fixture body has %d lines, want %d for %#v", got, want, evidence), 1)
			}
			for index, line := range windowLines {
				lineIndex := evidence.StartLine - 1 + index
				if file.present[lineIndex] && file.lines[lineIndex] != line {
					ginkgo.Fail(fmt.Sprintf("conflicting source-window fixture bodies for %s line %d", evidence.CanonicalID, lineIndex+1), 1)
				}
				file.lines[lineIndex] = line
				file.present[lineIndex] = true
			}
		}
	}

	if len(files) == 0 {
		return results
	}

	root := t.TempDir()
	for path, file := range files {
		for index := range file.lines {
			if !file.present[index] {
				file.lines[index] = "fixture padding"
			}
		}
		absolutePath := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
			ginkgo.Fail(fmt.Sprintf("create current source-window fixture directory: %v", err), 1)
		}
		if err := os.WriteFile(absolutePath, []byte(strings.Join(file.lines, "\n")), 0o600); err != nil {
			ginkgo.Fail(fmt.Sprintf("write current source-window fixture: %v", err), 1)
		}
	}

	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true, MaxOutputBytes: 4 << 20})
	manager.SetPermissionRules(map[string][]toolpolicy.Rule{
		workspace.ToolNameReadFile: {{Pattern: "**", Decision: toolpolicy.Allow}},
	})
	runner.runner.Workspace = manager

	rebound := append([]workspace.ToolResult(nil), results...)
	for index, result := range results {
		windows := result.SourceWindows()
		if len(windows) == 0 {
			continue
		}
		if len(windows) != 1 {
			ginkgo.Fail(fmt.Sprintf("contract source-window fixture must expose one window per result, got %d", len(windows)), 1)
		}

		evidence := windows[0].Evidence
		presentedPath := evidence.Authority.PolicySubject
		if presentedPath == "" {
			presentedPath = evidence.CanonicalID
		}
		current := manager.Execute(context.Background(), workspace.ToolRequest{
			Tool:      workspace.ToolNameReadFile,
			Path:      presentedPath,
			StartLine: evidence.StartLine,
			EndLine:   evidence.EndLine,
		})
		if current.Error != "" || len(current.SourceWindows()) != 1 {
			ginkgo.Fail(fmt.Sprintf("produce current source-window fixture for %s: %#v", evidence.CanonicalID, current), 1)
		}

		current.Tool = result.Tool
		current.Code = result.Code
		current.Summary = result.Summary
		current.Output = result.Output
		current.Error = result.Error
		current.Failure = result.Failure
		current.Paths = append([]string(nil), result.Paths...)
		current.ChangedPaths = append([]string(nil), result.ChangedPaths...)
		current.PrecisionTier = result.PrecisionTier
		current.Relationships = append([]workspace.ToolRelationship(nil), result.Relationships...)
		current.PayloadType = result.PayloadType
		current.Payload = append([]byte(nil), result.Payload...)
		facts := result.Facts.Clone()
		if facts == nil {
			facts = &toolfacts.ToolResultFacts{}
		}
		facts.SourceWindows = append([]toolfacts.SourceWindowEvidence(nil), current.Facts.SourceWindows...)
		current.Facts = facts
		rebound[index] = current
	}

	return rebound
}

var _ = ginkgo.It("FinalAnswerAcceptanceTreatsEchoedObjectivePathInventoryAsAdvisory", func() {
	t := ginkgo.GinkgoT()
	objective := "What would it take to enable the up and down arrow keys to iterate through old user messages as a shortcut to retry previous messages?"
	brief := readOnlyConversationBriefForTest(objective)
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Facts: contextPackCandidateFactsForTest(
				"internal/surfaces/tui/app/update_handlers.go",
				"internal/surfaces/tui/app/sessions_panel.go",
			),
		},
		readFileSourceWindowResult(
			"internal/surfaces/tui/app/sessions_panel.go",
			208,
			248,
			0,
			strings.Join([]string{
				"208:func (p *SessionsPanel) HandleKey(msg tea.KeyMsg) {",
				"209:\tswitch msg.Type {",
				"210:\tcase tea.KeyUp:",
				"211:\t\tp.MoveUp()",
				"212:\tcase tea.KeyDown:",
				"213:\t\tp.MoveDown()",
				"214:\t}",
				"215:}",
			}, "\n"),
		),
		readFileSourceWindowResult(
			"internal/surfaces/tui/app/update_handlers.go",
			22,
			182,
			0,
			strings.Join([]string{
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
		),
	}
	answer := "For `" + objective + "`, `internal/surfaces/tui/app/update_handlers.go` shows defines `Model.handleKeyPress`."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})

	assertObjectiveRelevanceAdvisoryForTest(t, runner, answer, results, "answer_objective_unmet:term_mismatch")

	acceptance := runner.evaluateFinalAnswerAcceptance(answer, results)
	if len(acceptance.AnswerObjectiveTerms) > 0 {
		ginkgo.Fail(fmt.Sprintf("echoed objective must not satisfy answer objective terms, got %#v", acceptance.AnswerObjectiveTerms), 1)
	}
})

var _ = ginkgo.It("FinalAnswerAcceptanceAllowsExplainerAnswerWithInspectedSymbol", func() {
	brief := readOnlyConversationBriefForTest("Explain the harness control flow.")
	results := []workspace.ToolResult{
		readFileSourceWindowResult(
			"internal/runtime/agent/runner_core.go",
			1,
			20,
			0,
			strings.Join([]string{
				"1:package agent",
				"2:func (r *Runner) Run() {}",
			}, "\n"),
		),
	}
	answer := "`Runner.Run` is visible in `internal/runtime/agent/runner_core.go`; the inspected source anchors the harness control flow in that file."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
	results = bindCurrentSourceWindowsForContractTest(ginkgo.GinkgoT(), runner, results)

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "" || reason != "" {
		ginkgo.Fail(fmt.Sprintf("explainer answer with inspected symbol blocked: kind=%q reason=%q", kind, reason), 1)
	}

	answer = "For `Explain the harness control flow.`, `internal/runtime/agent/runner_core.go` shows defines `Runner.Run`."
	kind, reason = runner.finalAnswerBlockerKind(answer, results)

	if kind != "" || reason != "" {
		ginkgo.Fail(fmt.Sprintf("deterministic explainer symbol answer blocked: kind=%q reason=%q", kind, reason), 1)
	}
})

var _ = ginkgo.It("ReadOnlyAnswerabilityDoesNotPromoteUnrelatedCollectiveSourceBodies", func() {
	brief := readOnlyConversationBriefForTest("How do up and down arrow keys navigate conversation history?")
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack returned a real candidate unrelated to later reads",
			Facts:   contextPackCandidateFactsForTest("internal/surfaces/tui/app/update_handlers.go"),
		},
		readFileSourceWindowResult(
			"internal/platform/config/config.go",
			1,
			8,
			0,
			strings.Join([]string{
				"1:package config",
				"2:// down key history words appear in an unrelated config comment",
				"3:const ConfigValue = true",
			}, "\n"),
		),
		readFileSourceWindowResult(
			"internal/capabilities/workspace/manager.go",
			1,
			8,
			0,
			strings.Join([]string{
				"1:package workspace",
				"2:// conversation message words appear in unrelated workspace plumbing",
				"3:type Manager struct{}",
			}, "\n"),
		),
	}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() {
		ginkgo.Fail(fmt.Sprintf("unrelated source bodies must not become answerable through scattered objective words: %#v", ledger), 1)
	}

	if len(ledger.SourceBodyObjectivePaths) > 0 {
		ginkgo.Fail(fmt.Sprintf("unrelated source bodies must not be promoted to objective paths: %#v", ledger.SourceBodyObjectivePaths), 1)
	}
})

var _ = ginkgo.It("ObjectiveAsksImplementationShapeDoesNotCatchPlainHowToUse", func() {
	if ObjectiveAsksImplementationShape("How to run the unit tests in this repository?") {
		ginkgo.Fail("plain how-to usage question must not require implementation-owner readiness", 1)
	}

	if !ObjectiveAsksImplementationShape("What would it take to change the runner readiness behavior?") {
		ginkgo.Fail("change-shape question should require implementation-owner readiness", 1)
	}
})

var _ = ginkgo.It("ReadOnlyAnswerabilityDoesNotTreatSessionsPanelAsChatRecallOwner", func() {
	objective := "what would it take to enable the up and down arrow keys to iterate through the user's chat history? the intent is to make it easy to find old messages and run them again without typing them out."
	brief := readOnlyConversationBriefForTest(objective)

	sessionsResults := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack returned candidate internal/surfaces/tui/app/sessions_panel.go",
			Facts:   contextPackCandidateFactsForTest("internal/surfaces/tui/app/sessions_panel.go"),
		},
		codeNavigateSourceWindowResult(
			"internal/surfaces/tui/app/sessions_panel.go",
			205,
			245,
			0,
			strings.Join([]string{
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
		),
	}
	if ledger := readOnlyAnswerabilityForResults(brief, sessionsResults); ledger.Answerable() {
		ginkgo.Fail(fmt.Sprintf("sessions-panel source owner must not close chat-message recall readiness: %#v", ledger), 1)
	} else if role := sourceOwnerRoleForTest(ledger, "internal/surfaces/tui/app/sessions_panel.go"); role != readOnlySourceOwnerConflictingNavigation {
		ginkgo.Fail(fmt.Sprintf("sessions-panel source should be conflicting navigation owner, got %q in %#v", role, ledger.SourceOwnerEvidence), 1)
	}

	overlayResults := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack returned candidate internal/surfaces/tui/app/help_overlay.go",
			Facts:   contextPackCandidateFactsForTest("internal/surfaces/tui/app/help_overlay.go"),
		},
		codeNavigateSourceWindowResult(
			"internal/surfaces/tui/app/help_overlay.go",
			42,
			88,
			0,
			strings.Join([]string{
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
		),
	}
	if ledger := readOnlyAnswerabilityForResults(brief, overlayResults); ledger.Answerable() {
		ginkgo.Fail(fmt.Sprintf("unrelated overlay navigation owner must not close chat-message recall readiness: %#v", ledger), 1)
	} else if role := sourceOwnerRoleForTest(ledger, "internal/surfaces/tui/app/help_overlay.go"); role != readOnlySourceOwnerConflictingNavigation {
		ginkgo.Fail(fmt.Sprintf("overlay source should be conflicting navigation owner, got %q in %#v", role, ledger.SourceOwnerEvidence), 1)
	}

	stateOnlyResults := []workspace.ToolResult{
		sessionsResults[0],
		codeNavigateSourceWindowResult(
			"internal/surfaces/tui/app/model_state.go",
			100,
			150,
			0,
			strings.Join([]string{
				"route_summary: Read Model in internal/surfaces/tui/app/model_state.go lines 100-150",
				"100:type Model struct {",
				"122:\tmessages []llama.ChatMessage",
				"130:\thistory historyState",
				"131:\tinputState inputState",
				"150:}",
			}, "\n"),
		),
	}
	if ledger := readOnlyAnswerabilityForResults(brief, stateOnlyResults); ledger.Answerable() {
		ginkgo.Fail(fmt.Sprintf("state-only source body must not close implementation-shape readiness: %#v", ledger), 1)
	} else if role := sourceOwnerRoleForTest(ledger, "internal/surfaces/tui/app/model_state.go"); role != readOnlySourceOwnerStateHolder {
		ginkgo.Fail(fmt.Sprintf("model state source should be state support, got %q in %#v", role, ledger.SourceOwnerEvidence), 1)
	}

	liveShapedStateResults := []workspace.ToolResult{
		sessionsResults[0],
		codeNavigateSourceWindowResult(
			"internal/surfaces/tui/app/model_state.go",
			133,
			263,
			0,
			strings.Join([]string{
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
		),
	}
	if ledger := readOnlyAnswerabilityForResults(brief, liveShapedStateResults); ledger.Answerable() {
		ginkgo.Fail(fmt.Sprintf("live-shaped Model state body must not close implementation-shape readiness: %#v", ledger), 1)
	} else if role := sourceOwnerRoleForTest(ledger, "internal/surfaces/tui/app/model_state.go"); role != readOnlySourceOwnerStateHolder {
		ginkgo.Fail(fmt.Sprintf("live-shaped Model body should be state support, got %q in %#v", role, ledger.SourceOwnerEvidence), 1)
	}

	sessionsPlusStateResults := append([]workspace.ToolResult{}, sessionsResults...)

	sessionsPlusStateResults = append(sessionsPlusStateResults, stateOnlyResults[1])

	if ledger := readOnlyAnswerabilityForResults(brief, sessionsPlusStateResults); ledger.Answerable() {
		ginkgo.Fail(fmt.Sprintf("sessions navigation plus state support must not close without a mechanism owner: %#v", ledger), 1)
	}

	handlerResults := []workspace.ToolResult{
		sessionsResults[0],
		codeNavigateSourceWindowResult(
			"internal/surfaces/tui/app/update_handlers.go",
			22,
			180,
			0,
			strings.Join([]string{
				"route_summary: Read Model.handleKeyPress in internal/surfaces/tui/app/update_handlers.go lines 22-180",
				"22:func (m *Model) handleKeyPress(msg tea.KeyMsg) (tea.Cmd, bool) {",
				"43:\t\tcase tea.KeyUp:",
				"49:\t\tcase tea.KeyDown:",
				"70:\t\t\tif m.history.selected > 0 {",
				"77:\t\t\tif m.history.selected < len(m.history.entries)-1 {",
				"172:\t\tm.appendHistoryMessage(\"user\", text, nil)",
			}, "\n"),
		),
	}
	if ledger := readOnlyAnswerabilityForResults(brief, handlerResults); !ledger.Answerable() {
		ginkgo.Fail(fmt.Sprintf("chat/history key-handler source body should close readiness: %#v", ledger), 1)
	} else if role := sourceOwnerRoleForTest(ledger, "internal/surfaces/tui/app/update_handlers.go"); role != readOnlySourceOwnerMechanism {
		ginkgo.Fail(fmt.Sprintf("chat/history key handler should be mechanism owner, got %q in %#v", role, ledger.SourceOwnerEvidence), 1)
	}
})

var _ = ginkgo.It("ReadOnlyAnswerabilityDoesNotTreatRegressionFixtureAsImplementationOwner", func() {
	objective := "what would it take to enable the up and down arrows to iterate between and fill the chat input with a user's chat history entries?"
	brief := readOnlyConversationBriefForTest(objective)
	results := []workspace.ToolResult{
		{
			Tool:    "search_content",
			Summary: "Found 38 matching line(s).",
			Output: strings.Join([]string{
				"internal/runtime/agent/contract_surface_tests_test.go:1237:func TestSynthesizeBriefConversationFallbackPrefersObjectiveRelevantExactProbe(t *testing.T) {",
				"internal/runtime/agent/contract_surface_tests_test.go:1242:objective := \"what would it take to enable the up and down arrow keys to iterate through the user's chat history?\"",
			}, "\n"),
		},
		readFileSourceWindowResult(
			"internal/runtime/agent/contract_surface_tests_test.go",
			1230,
			1270,
			1544,
			strings.Join([]string{
				"1237:func TestSynthesizeBriefConversationFallbackPrefersObjectiveRelevantExactProbe(t *testing.T) {",
				"1242:\tobjective := \"what would it take to enable the up and down arrow keys to iterate through the user's chat history?\"",
				"1249:\t\t\"route_summary: Read Model.handleKeyPress in internal/surfaces/tui/app/update_handlers.go lines 22-180\",",
				"1250:\t\t\"22:func (m *Model) handleKeyPress(msg tea.KeyMsg) (tea.Cmd, bool) {\",",
				"1251:\t\t\"43:\\t\\tcase tea.KeyUp:\",",
				"1252:\t\t\"49:\\t\\tcase tea.KeyDown:\",",
				"1253:\t\t\"172:\\t\\tm.appendHistoryMessage(\\\"user\\\", text, nil)\",",
			}, "\n"),
		),
	}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() {
		ginkgo.Fail(fmt.Sprintf("test fixture body must not satisfy implementation-owner readiness: %#v", ledger), 1)
	}

	if got := ledger.ImplementationOwnerPaths(); len(got) > 0 {
		ginkgo.Fail(fmt.Sprintf("test fixture must not become implementation owner path, got %#v", got), 1)
	}

	if role := sourceOwnerRoleForTest(ledger, "internal/runtime/agent/contract_surface_tests_test.go"); role != readOnlySourceOwnerTestSupport {
		ginkgo.Fail(fmt.Sprintf("test fixture should be classified as test support, got %q in %#v", role, ledger.SourceOwnerEvidence), 1)
	}
})

func sourceOwnerRoleForTest(ledger readOnlyAnswerabilityLedger, path string) readOnlySourceOwnerRole {
	path = NormalizeRunnerPath(path)
	for _, evidence := range ledger.SourceOwnerEvidence {
		if evidence.Path == path {
			return evidence.Role
		}
	}

	return readOnlySourceOwnerUnknown
}

var _ = ginkgo.It("FinalAnswerAcceptanceRejectsSessionsPanelAsChatRecallOwner", func() {
	t := ginkgo.GinkgoT()
	objective := "what would it take to enable the up and down arrow keys to iterate through the user's chat history? the intent is to make it easy to find old messages and run them again without typing them out."
	brief := readOnlyConversationBriefForTest(objective)
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack returned candidate internal/surfaces/tui/app/sessions_panel.go",
			Facts: contextPackCandidateFactsForTest(
				"internal/surfaces/tui/app/sessions_panel.go",
				"internal/surfaces/tui/app/model_state.go",
			),
		},
		codeNavigateSourceWindowResult(
			"internal/surfaces/tui/app/sessions_panel.go",
			205,
			245,
			0,
			strings.Join([]string{
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
		),
		codeNavigateSourceWindowResult(
			"internal/surfaces/tui/app/model_state.go",
			100,
			150,
			0,
			strings.Join([]string{
				"route_summary: Read Model in internal/surfaces/tui/app/model_state.go lines 100-150",
				"100:type Model struct {",
				"122:\tmessages []llama.ChatMessage",
				"130:\thistory historyState",
				"131:\tinputState inputState",
				"150:}",
			}, "\n"),
		),
	}
	answer := "It would take changing `Model.handleSessionsPanelKey` in `internal/surfaces/tui/app/sessions_panel.go`. The inspected source shows it already handles `tea.KeyUp` and `tea.KeyDown`, so the work is to route those cases through chat-history state."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})

	decision := readOnlyClosureDecisionForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results).
		FinalAnswerDecision(answer)
	if decision.BlockerKind != "answer_objective_unmet:citation_mismatch" {
		ginkgo.Fail(fmt.Sprintf("shared final-answer decision got blocker kind=%q", decision.BlockerKind), 1)
	}

	if slices.Contains(decision.AcceptedEvidencePaths, "internal/surfaces/tui/app/sessions_panel.go") {
		ginkgo.Fail(fmt.Sprintf("shared final-answer decision promoted conflicting navigation owner, got %#v", decision.AcceptedEvidencePaths), 1)
	}

	aliasBrief := brief

	aliasBrief.TaskFamily = string(autonomy.TaskFamilyDirectAnswer)

	aliasDecision := readOnlyClosureDecisionForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(aliasBrief), results).
		FinalAnswerDecision(answer)

	if aliasDecision.BlockerKind != "answer_objective_unmet:citation_mismatch" {
		ginkgo.Fail(fmt.Sprintf("direct_answer alias shared decision got blocker kind=%q", aliasDecision.BlockerKind), 1)
	}

	if slices.Contains(aliasDecision.AcceptedEvidencePaths, "internal/surfaces/tui/app/sessions_panel.go") {
		ginkgo.Fail(fmt.Sprintf("direct_answer alias promoted conflicting navigation owner, got %#v", aliasDecision.AcceptedEvidencePaths), 1)
	}

	assertObjectiveRelevanceAdvisoryForTest(t, runner, answer, results, "answer_objective_unmet:citation_mismatch")

	if paths := acceptedFinalAnswerEvidencePaths(answer, brief, results); slices.Contains(paths, "internal/surfaces/tui/app/sessions_panel.go") {
		ginkgo.Fail(fmt.Sprintf("accepted evidence paths must not promote conflicting navigation owner, got %#v", paths), 1)
	}

	aliasRunner := newRunnerRunFixture(&Runner{RuntimeBrief: aliasBrief})
	assertObjectiveRelevanceAdvisoryForTest(t, aliasRunner, answer, results, "answer_objective_unmet:citation_mismatch")

	if paths := acceptedFinalAnswerEvidencePaths(answer, aliasBrief, results); slices.Contains(paths, "internal/surfaces/tui/app/sessions_panel.go") {
		ginkgo.Fail(fmt.Sprintf("direct_answer alias accepted evidence paths promoted conflicting navigation owner, got %#v", paths), 1)
	}
})

var _ = ginkgo.It("PostChildSourceBodyPacketEvidenceAvailableAcceptsDelegatedNegativeFinding", func() {
	brief := readOnlyConversationBriefForTest("Review performance risks in this workspace.")
	results := []workspace.ToolResult{typedReadOnlyTaskResult(ReadOnlyEvidencePacket{
		Objective:       "Review performance risks in this workspace.",
		Question:        "Review performance risks in this workspace.",
		SourceBodyPaths: []string{"internal/runtime/agent/runner_core.go"},
		InspectedPaths:  []string{"internal/runtime/agent/runner_core.go"},
		Confidence:      "medium",
		Findings: []ReadOnlyEvidenceFinding{
			{Path: "internal/runtime/agent/runner_core.go", Text: "No performance issue found in the inspected runner path."},
		},
	})}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() {
		ginkgo.Fail(fmt.Sprintf("source-body packet availability alone should not satisfy answerability: %#v", ledger), 1)
	}

	if !newToolResultEvidenceProjection(results).sourceBodyPacketAvailable() {
		ginkgo.Fail("delegated source-body packet with a bounded negative finding should trigger parent model synthesis", 1)
	}
})

var _ = ginkgo.It("ReadOnlyAnswerabilityAcceptsTypedImplementationOwnerPacketWithoutAgentPath", func() {
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
		ginkgo.Fail(fmt.Sprintf("typed implementation-owner packet should satisfy direct-answer readiness without an internal/runtime/agent path: %#v", ledger), 1)
	}

	if got := ledger.ImplementationOwnerPaths(); !slices.Contains(got, "internal/surfaces/tui/app/update_handlers.go") {
		ginkgo.Fail(fmt.Sprintf("expected typed packet source owner path, got %#v", got), 1)
	}

	if role := sourceOwnerRoleForTest(ledger, "internal/surfaces/tui/app/update_handlers.go"); role != readOnlySourceOwnerMechanism {
		ginkgo.Fail(fmt.Sprintf("typed packet source should be mechanism owner, got %q in %#v", role, ledger.SourceOwnerEvidence), 1)
	}
})

var _ = ginkgo.It("ReadOnlyAnswerabilityRejectsGenericToolSchemaPacketForUsefulnessObjective", func() {
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
		ginkgo.Fail(fmt.Sprintf("generic tool/schema packet must not satisfy usefulness answerability: %#v", ledger), 1)
	}

	if newToolResultEvidenceProjection(results).hasAnswerableDelegatedPacket(brief) {
		ginkgo.Fail("generic tool/schema packet must not activate child packet closure", 1)
	}

	if paths := newToolResultEvidenceProjection(results).postChildClosureEvidencePaths(brief.Objective, 4); len(paths) > 0 {
		ginkgo.Fail(fmt.Sprintf("generic tool/schema packet must not populate post-child evidence paths, got %#v", paths), 1)
	}

	if paths := acceptedFinalAnswerEvidencePaths("Discovery tool schema details are available.", brief, results); len(paths) > 0 {
		ginkgo.Fail(fmt.Sprintf("generic tool/schema packet must not leak accepted evidence paths, got %#v", paths), 1)
	}
})

var _ = ginkgo.It("ReadOnlyAnswerabilityRejectsObjectiveRelevantLegacyChildPacket", func() {
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
			"- Read f23 internal/runtime/agent/implementation_flow.go lines 1-20",
			"- Found task child runner creation in internal/runtime/agent/implementation_flow.go",
		}, "\n"),
	}}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() {
		ginkgo.Fail(fmt.Sprintf("objective-relevant legacy child packet must not satisfy read-only answerability: %#v", ledger), 1)
	}
})

var _ = ginkgo.It("ReadOnlyAnswerabilityRejectsDelegatedArtifactOnlyPacket", func() {
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
		ginkgo.Fail(fmt.Sprintf("artifact-only delegated packet must not satisfy answerability: %#v", ledger), 1)
	}
})

var _ = ginkgo.It("ReadOnlyAnswerabilityAcceptsExplicitDiagnosticEvidence", func() {
	brief := readOnlyConversationBriefForTest("Explain why diagnostics fail.")
	results := []workspace.ToolResult{{
		Tool:    "source_sanity",
		Summary: "source_sanity failed for internal/runtime/agent/runner_core.go",
		Output:  "internal/runtime/agent/runner_core.go:10: undefined: Foo\n",
		Facts:   &toolfacts.ToolResultFacts{DiagnosticEvidencePaths: []string{"internal/runtime/agent/runner_core.go"}},
	}}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if !ledger.Answerable() {
		ginkgo.Fail(fmt.Sprintf("explicit diagnostic evidence should make read-only explanation answerable: %#v", ledger), 1)
	}

	if !ledger.DiagnosticEvidence {
		ginkgo.Fail(fmt.Sprintf("expected diagnostic evidence flag: %#v", ledger), 1)
	}

	if !slices.Contains(ledger.DiagnosticPaths, "internal/runtime/agent/runner_core.go") {
		ginkgo.Fail(fmt.Sprintf("expected diagnostic source path, got %#v", ledger.DiagnosticPaths), 1)
	}

	answer := "Diagnostics fail in `internal/runtime/agent/runner_core.go` because source_sanity reports `undefined: Foo`."
	decision := readOnlyClosureDecisionForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results).
		FinalAnswerDecision(answer)

	if decision.BlockerKind != "" {
		ginkgo.Fail(fmt.Sprintf("diagnostic final-answer decision rejected relevant evidence: %#v", decision), 1)
	}

	if !slices.Contains(decision.AcceptedEvidencePaths, "internal/runtime/agent/runner_core.go") {
		ginkgo.Fail(fmt.Sprintf("diagnostic final-answer decision missed accepted diagnostic path: %#v", decision.AcceptedEvidencePaths), 1)
	}

	if paths := acceptedFinalAnswerEvidencePaths(answer, brief, results); !slices.Contains(paths, "internal/runtime/agent/runner_core.go") {
		ginkgo.Fail(fmt.Sprintf("accepted evidence paths missed relevant diagnostic path: %#v", paths), 1)
	}
})

var _ = ginkgo.It("ReadOnlyAcceptedEvidencePathsRejectOutputOnlyDiagnosticSpoof", func() {
	brief := readOnlyConversationBriefForTest("Explain why diagnostics fail.")
	results := []workspace.ToolResult{{
		Tool:   workspace.ToolNameSourceSanity,
		Output: "internal/runtime/agent/runner_core.go:10: undefined: Foo\n",
	}}
	answer := "Diagnostics fail in `internal/runtime/agent/runner_core.go` because source_sanity reports `undefined: Foo`."

	if paths := acceptedFinalAnswerEvidencePaths(answer, brief, results); len(paths) != 0 {
		ginkgo.Fail(fmt.Sprintf("rendered diagnostic output minted accepted evidence paths: %#v", paths), 1)
	}
})

var _ = ginkgo.It("ReadOnlyAcceptedEvidencePathsKeepDiagnosticLogRangeAuthoritySeparate", func() {
	const logPath = "logs/runtime/app.log"
	brief := readOnlyConversationBriefForTest("Explain the failure in logs/runtime/app.log.")
	logRead := diagnosticLogReadResultForTest(logPath, logPath, 10, 20, 20, false, "failure")
	logRead.Facts.DiagnosticEvidencePaths = []string{logPath}
	answer := "The failure appears at `logs/runtime/app.log:1-2`."

	if paths := acceptedFinalAnswerEvidencePaths(answer, brief, []workspace.ToolResult{logRead}); len(paths) != 0 {
		ginkgo.Fail(fmt.Sprintf("co-carried diagnostic paths bypassed diagnostic-log range coverage: %#v", paths), 1)
	}
})

var _ = ginkgo.It("ReadOnlyAnswerabilityRejectsUnrelatedDiagnosticEvidence", func() {
	brief := readOnlyConversationBriefForTest("Why did the runner build fail?")
	results := []workspace.ToolResult{{
		Tool:    "source_sanity",
		Summary: "source_sanity failed for internal/platform/config/config.go",
		Output:  "internal/platform/config/config.go:10: undefined: ConfigValue\n",
		Facts:   &toolfacts.ToolResultFacts{DiagnosticEvidencePaths: []string{"internal/platform/config/config.go"}},
	}}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() || ledger.DiagnosticRelevant {
		ginkgo.Fail(fmt.Sprintf("unrelated diagnostic evidence must not satisfy broad failure objectives: %#v", ledger), 1)
	}

	if !ledger.DiagnosticEvidence {
		ginkgo.Fail(fmt.Sprintf("diagnostic evidence should still be recorded as weak evidence: %#v", ledger), 1)
	}

	if !slices.Contains(ledger.DiagnosticPaths, "internal/platform/config/config.go") {
		ginkgo.Fail(fmt.Sprintf("expected diagnostic source path to be recorded, got %#v", ledger.DiagnosticPaths), 1)
	}

	answer := "The runner build failed because `internal/platform/config/config.go` has `undefined: ConfigValue`."
	decision := readOnlyClosureDecisionForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results).
		FinalAnswerDecision(answer)

	if len(decision.AcceptedEvidencePaths) > 0 {
		ginkgo.Fail(fmt.Sprintf("unrelated diagnostic evidence must not become accepted evidence: %#v", decision.AcceptedEvidencePaths), 1)
	}

	if paths := acceptedFinalAnswerEvidencePaths(answer, brief, results); len(paths) > 0 {
		ginkgo.Fail(fmt.Sprintf("accepted evidence paths promoted unrelated diagnostic evidence: %#v", paths), 1)
	}
})

var _ = ginkgo.It("ReadOnlyAnswerabilityAcceptsPathMatchedDiagnosticEvidence", func() {
	brief := readOnlyConversationBriefForTest("Why did diagnostics fail for internal/runtime/agent/runner_core.go?")
	results := []workspace.ToolResult{{
		Tool:    "source_sanity",
		Summary: "source_sanity failed for internal/runtime/agent/runner_core.go",
		Output:  "internal/runtime/agent/runner_core.go:10: undefined: RunnerState\n",
		Facts:   &toolfacts.ToolResultFacts{DiagnosticEvidencePaths: []string{"internal/runtime/agent/runner_core.go"}},
	}}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if !ledger.Answerable() || !ledger.DiagnosticRelevant {
		ginkgo.Fail(fmt.Sprintf("path-matched diagnostic evidence should satisfy diagnostic objective: %#v", ledger), 1)
	}
})

var _ = ginkgo.It("ReadOnlyAnswerabilityRejectsWrongPathDiagnosticEvidence", func() {
	brief := readOnlyConversationBriefForTest("Why did diagnostics fail for internal/runtime/agent/runner_core.go?")
	results := []workspace.ToolResult{{
		Tool:    "source_sanity",
		Summary: "source_sanity failed for internal/platform/config/config.go",
		Output:  "internal/platform/config/config.go:10: undefined: ConfigValue\n",
		Facts:   &toolfacts.ToolResultFacts{DiagnosticEvidencePaths: []string{"internal/platform/config/config.go"}},
	}}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() || ledger.DiagnosticRelevant {
		ginkgo.Fail(fmt.Sprintf("diagnostic evidence for the wrong named path must not satisfy answerability: %#v", ledger), 1)
	}
})

var _ = ginkgo.It("ReadOnlyAnswerabilityRejectsWrongPathDiagnosticEvidenceEvenWhenTextEchoesObjective", func() {
	brief := readOnlyConversationBriefForTest("Why did diagnostics fail for internal/runtime/agent/runner_core.go?")
	results := []workspace.ToolResult{{
		Tool:    "source_sanity",
		Summary: "source_sanity failed for internal/platform/config/config.go while investigating diagnostics for internal/runtime/agent/runner_core.go",
		Output:  "internal/platform/config/config.go:10: undefined: ConfigValue\ninternal/runtime/agent/runner_core.go remains the requested diagnostic target.\n",
		Facts:   &toolfacts.ToolResultFacts{DiagnosticEvidencePaths: []string{"internal/platform/config/config.go"}},
	}}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() || ledger.DiagnosticRelevant {
		ginkgo.Fail(fmt.Sprintf("wrong-path diagnostic text that echoes the named target must not satisfy answerability: %#v", ledger), 1)
	}
})

var _ = ginkgo.It("ReadOnlyAnswerabilityDoesNotTreatCompleteDiagnosticLogAsSourceEvidence", func() {
	brief := readOnlyConversationBriefForTest("Why did the runner build fail?")
	results := []workspace.ToolResult{
		readFileSourceWindowResult("logs/conversations/20260603-173738.903.jsonl", 1, 72, 72, "1:{\"type\":\"session_start\"}\n72:{\"type\":\"final\"}\n"),
	}

	ledger := readOnlyAnswerabilityForResults(brief, results)
	if ledger.Answerable() || ledger.DiagnosticRelevant {
		ginkgo.Fail(fmt.Sprintf("complete diagnostic logs must not satisfy source-code failure questions without a typed diagnostic evidence contract: %#v", ledger), 1)
	}

	if len(ledger.SourceBodyPaths) > 0 {
		ginkgo.Fail(fmt.Sprintf("diagnostic logs must not become source-body paths: %#v", ledger.SourceBodyPaths), 1)
	}
})

var _ = ginkgo.It("ReadOnlyAnswerabilityRejectsRetainedContextFinalWithoutSourceBody", func() {
	brief := readOnlyConversationBriefForTest("Explain runner answer readiness.")
	results := []workspace.ToolResult{{
		Tool:    "context_pack",
		Summary: "Context pack wrote .idiom/comprehension/views/pack.json",
		Facts:   contextPackCandidateFactsForTest("internal/runtime/agent/runner_core.go"),
	}}
	answer := "The retained source context identifies the relevant implementation surface as `internal/runtime/agent/runner_core.go`."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "source_grounding_unmet" {
		ginkgo.Fail(fmt.Sprintf("retained-context final should be rejected, got (%q, %q)", kind, reason), 1)
	}

	if reason == "" {
		ginkgo.Fail("expected rejection reason", 1)
	}
})

var _ = ginkgo.It("FinalAnswerAcceptanceRejectsGroundingAndKeepsObjectiveDiagnostic", func() {
	objective := "Would it fit our north star to replace this with model prose? Or do we risk leaking unverified information to the user?"
	brief := readOnlyConversationBriefForTest(objective)
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Facts: contextPackCandidateFactsForTest(
				"docs/decisioning-north-star.md",
				"internal/runtime/agent/tools_surface.go",
			),
		},
		readFileSourceWindowResult(
			"docs/decisioning-north-star.md",
			1,
			6,
			0,
			strings.Join([]string{
				"1:# Decisioning North Star",
				"2:The model owns strategy; the harness owns contracts.",
				"3:Final answers must be grounded in verified source evidence rather than unverified model prose.",
			}, "\n"),
		),
	}
	answer := "No. Free-form prose would move deterministic closeout language away from the contract boundary in `internal/runtime/agent/tools_surface.go`."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})

	acceptance := runner.evaluateFinalAnswerAcceptance(answer, results)
	if acceptance.Decision != finalAnswerAcceptanceRejected ||
		acceptance.BlockerKind != "source_grounding_unmet" ||
		acceptance.Reason == "" {
		ginkgo.Fail(fmt.Sprintf("candidate-only citation should be rejected by source grounding, got %#v", acceptance), 1)
	}
	if !acceptance.ContractAssessment.HasViolation(turnViolationSourceGroundingUnmet) ||
		!acceptance.ContractAssessment.HasDiagnostic(turnDiagnosticObjectiveRelevance) {
		ginkgo.Fail(fmt.Sprintf("expected source-grounding violation and objective diagnostic, got %#v", acceptance.ContractAssessment), 1)
	}
})

var _ = ginkgo.It("FinalAnswerAcceptanceRejectsFalseBuildAndTestAddedClaim", func() {
	brief := readOnlyConversationBriefForTest("Did you verify that it still builds? And added a test to cover this new behavior?")
	results := []workspace.ToolResult{{
		Tool:    "source_sanity",
		Summary: "Source sanity passed for 1 file(s)",
		Output:  "internal/surfaces/tui/app/model_state.go: no warnings or errors via go_diagnostics",
		Facts:   &toolfacts.ToolResultFacts{DiagnosticEvidencePaths: []string{"internal/surfaces/tui/app/model_state.go"}},
	}}
	answer := "Yes, it still builds, and I added a new test in `internal/surfaces/tui/app/perf_test.go`."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})

	acceptance := runner.evaluateFinalAnswerAcceptance(answer, results)
	if acceptance.Decision != finalAnswerAcceptanceRejected ||
		acceptance.BlockerKind != string(turnViolationValidationStatusContradictsEvidence) ||
		!acceptance.ContractAssessment.HasViolation(turnViolationValidationStatusContradictsEvidence) {
		ginkgo.Fail(fmt.Sprintf("false validation status should be rejected by typed validation contract, got %#v", acceptance), 1)
	}
	if !strings.Contains(acceptance.Reason, "build/test verification") {
		ginkgo.Fail(fmt.Sprintf("rejection should name missing build/test evidence, got %q", acceptance.Reason), 1)
	}
})

var _ = ginkgo.It("FinalAnswerAcceptanceAllowsHonestSourceSanityButNoBuildOrTestAnswer", func() {
	brief := readOnlyConversationBriefForTest("Did you verify that it still builds? And added a test to cover this new behavior?")
	results := []workspace.ToolResult{{
		Tool:    "source_sanity",
		Summary: "Source sanity passed for 1 file(s)",
		Output:  "internal/surfaces/tui/app/model_state.go: no warnings or errors via go_diagnostics",
		Facts:   &toolfacts.ToolResultFacts{DiagnosticEvidencePaths: []string{"internal/surfaces/tui/app/model_state.go"}},
	}}
	answer := "I ran source_sanity on `internal/surfaces/tui/app/model_state.go`, and it passed. I did not run a build or test command, so I did not verify that the project still builds. I also did not add or change a test path."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})

	acceptance := runner.evaluateFinalAnswerAcceptance(answer, results)
	if acceptance.Decision != finalAnswerAcceptanceAccepted ||
		acceptance.BlockerKind != "" ||
		len(acceptance.ContractAssessment.Violations) != 0 {
		ginkgo.Fail(fmt.Sprintf("honest validation status answer should be accepted, got %#v", acceptance), 1)
	}
})

var _ = ginkgo.It("ValidationStatusEvidencePathsUseDiagnosticFacts", func() {
	results := []workspace.ToolResult{{
		Tool:    "source_sanity",
		Summary: "Source sanity passed for 1 file(s)",
		Output:  "internal/surfaces/tui/app/model_state.go: no warnings or errors via go_diagnostics",
		Facts:   &toolfacts.ToolResultFacts{DiagnosticEvidencePaths: []string{"internal/surfaces/tui/app/model_state.go"}},
	}}

	paths := validationStatusEvidencePaths(results)
	if !slices.Contains(paths, "internal/surfaces/tui/app/model_state.go") {
		ginkgo.Fail(fmt.Sprintf("source_sanity diagnostic facts should be ledger-backed validation evidence, got %#v", paths), 1)
	}
	if slices.Contains(paths, "internal/surfaces/tui/app/perf_test.go") {
		ginkgo.Fail(fmt.Sprintf("unobserved test path must not become validation ledger evidence, got %#v", paths), 1)
	}

	transcriptOnly := []workspace.ToolResult{{
		Tool:    "source_sanity",
		Summary: "Source sanity passed for 1 file(s)",
		Output:  "internal/surfaces/tui/app/output_only.go: no warnings or errors via go_diagnostics",
	}}
	if paths := validationStatusEvidencePaths(transcriptOnly); len(paths) != 0 {
		ginkgo.Fail(fmt.Sprintf("diagnostic transcript paths without facts must not become validation evidence, got %#v", paths), 1)
	}
})

var _ = ginkgo.It("FinalAnswerAcceptanceRejectsUnsupportedCitationInValidationStatusAnswer", func() {
	brief := readOnlyConversationBriefForTest("Did you verify that it still builds? And added a test to cover this new behavior?")
	results := []workspace.ToolResult{{
		Tool:    "source_sanity",
		Summary: "Source sanity passed for 1 file(s)",
		Output:  "internal/surfaces/tui/app/model_state.go: no warnings or errors via go_diagnostics",
		Facts:   &toolfacts.ToolResultFacts{DiagnosticEvidencePaths: []string{"internal/surfaces/tui/app/model_state.go"}},
	}}
	answer := "I ran source_sanity on `internal/surfaces/tui/app/model_state.go`, and it passed. I did not run a build or test command, so I did not verify that the project still builds. I also did not add or change a test path. Separately, `internal/surfaces/tui/app/perf_test.go` covers this behavior."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})

	acceptance := runner.evaluateFinalAnswerAcceptance(answer, results)
	if acceptance.Decision != finalAnswerAcceptanceRejected ||
		acceptance.BlockerKind != string(turnViolationSourceGroundingUnmet) ||
		!acceptance.ContractAssessment.HasViolation(turnViolationSourceGroundingUnmet) {

		ginkgo.Fail(fmt.Sprintf("unsupported non-ledger citation should still be rejected, got %#v", acceptance), 1)
	}
	if !strings.Contains(acceptance.Reason, "perf_test.go") {
		ginkgo.Fail(fmt.Sprintf("source-grounding rejection should name unsupported citation, got %q", acceptance.Reason), 1)
	}
})

var _ = ginkgo.It("FinalAnswerAcceptanceRejectsBuildClaimWhenBuildPredatesLaterWrite", func() {
	brief := readOnlyConversationBriefForTest("Did you verify that it still builds? And added a test to cover this new behavior?")
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
	_, write := committedMutationResultForTest("internal/surfaces/tui/app/model_state.go")
	installMutationReceiptForTest(runner, write)
	results := []workspace.ToolResult{
		shellCommandResultForTest("go test ./internal/surfaces/tui/app", "ok", ""),
		write,
		{
			Tool:    "source_sanity",
			Summary: "Source sanity passed for 1 file(s)",
			Output:  "internal/surfaces/tui/app/model_state.go: no warnings or errors via go_diagnostics",
		},
	}
	answer := "The build passed after the change. I did not add or change a test path."

	acceptance := runner.evaluateFinalAnswerAcceptance(answer, results)
	if acceptance.Decision != finalAnswerAcceptanceRejected ||
		acceptance.BlockerKind != string(turnViolationValidationStatusContradictsEvidence) {
		ginkgo.Fail(fmt.Sprintf("stale build command should not satisfy final build claim, got %#v", acceptance), 1)
	}
})

var _ = ginkgo.It("FinalAnswerAcceptanceAcceptsBuildClaimWhenBuildFollowsWrite", func() {
	brief := readOnlyConversationBriefForTest("Did you verify that it still builds? And added a test to cover this new behavior?")
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
	_, write := committedMutationResultForTest("internal/surfaces/tui/app/model_state.go")
	installMutationReceiptForTest(runner, write)
	results := []workspace.ToolResult{
		write,
		shellCommandResultForTest("go test ./internal/surfaces/tui/app", "ok", ""),
	}
	answer := "The build/test command passed after the change. I did not add or change a test path."

	acceptance := runner.evaluateFinalAnswerAcceptance(answer, results)
	if acceptance.Decision != finalAnswerAcceptanceAccepted ||
		acceptance.BlockerKind != "" ||
		len(acceptance.ContractAssessment.Violations) != 0 {
		ginkgo.Fail(fmt.Sprintf("post-write build command should satisfy validation status answer, got %#v", acceptance), 1)
	}
})

var _ = ginkgo.It("ReadOnlyAnswerabilityRejectsScaffoldFinalEvenWithRelevantSourceBody", func() {
	brief := readOnlyConversationBriefForTest("Explain runner answer readiness.")
	results := []workspace.ToolResult{
		readFileSourceWindowResult(
			"internal/runtime/agent/runner_core.go",
			4128,
			4285,
			0,
			"package agent\nfunc runnerReadinessState() {}\n",
		),
	}
	answer := "The strongest implementation evidence is in `internal/runtime/agent/runner_core.go`. Use those findings as the source of truth for `Explain runner answer readiness`.\n\nWhat this establishes:\n- The source body shows `runnerReadinessState` computes answer readiness."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "source_grounding_unmet" {
		ginkgo.Fail(fmt.Sprintf("source-backed scaffold final should still be rejected, got (%q, %q)", kind, reason), 1)
	}

	if reason == "" {
		ginkgo.Fail("expected scaffold rejection reason", 1)
	}
})

var _ = ginkgo.It("ReadOnlyAnswerabilityRejectsAnswerAboveSourceFooter", func() {
	brief := readOnlyConversationBriefForTest("how is evidence used in this harness?")
	results := []workspace.ToolResult{
		readFileSourceWindowResult(
			"internal/runtime/agent/evidence_readonly.go",
			1,
			65,
			0,
			"package agent\ntype ReadOnlyEvidencePacket struct{}\nfunc buildReadOnlyEvidencePacket() {}\n",
		),
		readFileSourceWindowResult(
			"internal/runtime/agent/runner_core.go",
			128,
			270,
			0,
			"package agent\nfunc conversationEvidenceSatisfied() bool { return true }\nfunc (r *Runner) conversationEvidenceAvailableForObjective() bool { return true }\n",
		),
	}
	answer := "The answer above is grounded in the following inspected source files:\n\n" +
		"- `internal/runtime/agent/evidence_readonly.go` - `ReadOnlyEvidencePacket` struct, `buildReadOnlyEvidencePacket`\n" +
		"- `internal/runtime/agent/runner_core.go` - `conversationEvidenceSatisfied`, `conversationEvidenceAvailableForObjective`\n"
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "protocol_meta_final_answer" || reason != "non_self_contained_final_answer" {
		ginkgo.Fail(fmt.Sprintf("footer-only context-dependent answer got (%q, %q), want protocol_meta_final_answer/non_self_contained_final_answer", kind, reason), 1)
	}
})

var _ = ginkgo.It("ReadOnlyAnswerabilityAllowsStandaloneGroundedEvidenceAnswer", func() {
	brief := readOnlyConversationBriefForTest("how is evidence used in this harness?")
	results := []workspace.ToolResult{
		readFileSourceWindowResult(
			"internal/runtime/agent/evidence_readonly.go",
			1,
			65,
			0,
			"package agent\ntype ReadOnlyEvidencePacket struct{}\nfunc buildReadOnlyEvidencePacket() {}\n",
		),
		readFileSourceWindowResult(
			"internal/runtime/agent/runner_core.go",
			128,
			270,
			0,
			"package agent\nfunc conversationEvidenceSatisfied() bool { return true }\nfunc (r *Runner) conversationEvidenceAvailableForObjective() bool { return true }\n",
		),
	}
	answer := "Evidence is used as the harness contract input: `ReadOnlyEvidencePacket` records source-body paths and findings in `internal/runtime/agent/evidence_readonly.go`, and `conversationEvidenceSatisfied` in `internal/runtime/agent/runner_core.go` evaluates the source-evidence threshold before a grounded answer can close."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
	results = bindCurrentSourceWindowsForContractTest(ginkgo.GinkgoT(), runner, results)

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "" || reason != "" {
		ginkgo.Fail(fmt.Sprintf("standalone grounded answer should pass final-answer acceptance, got (%q, %q)", kind, reason), 1)
	}
})

var _ = ginkgo.It("FinalAnswerAcceptanceTreatsOffObjectiveSourceBackedAnswerAsAdvisory", func() {
	t := ginkgo.GinkgoT()
	brief := readOnlyConversationBriefForTest("Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.")
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Facts: contextPackCandidateFactsForTest(
				"internal/surfaces/tui/app/rendering.go",
				"internal/surfaces/tui/app/model.go",
			),
		},
		readFileSourceWindowResult(
			"internal/surfaces/tui/app/rendering.go",
			1,
			9,
			0,
			"1:package app\n2:\n3:func evidenceDisplayName(rawTool string) string {\n4:\treturn strings.TrimSpace(rawTool)\n5:}\n",
		),
	}
	answer := "The relevant code is `internal/surfaces/tui/app/rendering.go`: `evidenceDisplayName` trims the raw tool name before display."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})

	relevance := assertObjectiveRelevanceAdvisoryForTest(t, runner, answer, results, "answer_objective_unmet:citation_mismatch")
	if !strings.Contains(relevance.Reason, "does not establish the user's objective") {
		ginkgo.Fail(fmt.Sprintf("advisory reason should describe objective mismatch, got %q", relevance.Reason), 1)
	}
})

var _ = ginkgo.It("FinalAnswerAcceptanceTreatsObjectiveParrotWithWrongSourceAsAdvisory", func() {
	t := ginkgo.GinkgoT()
	brief := readOnlyConversationBriefForTest("Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.")
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Facts:   contextPackCandidateFactsForTest("internal/surfaces/tui/app/rendering.go"),
		},
		readFileSourceWindowResult(
			"internal/surfaces/tui/app/rendering.go",
			1,
			9,
			0,
			"1:package app\n2:\n3:func evidenceDisplayName(rawTool string) string {\n4:\treturn strings.TrimSpace(rawTool)\n5:}\n",
		),
	}
	answer := "The TUI chat input newline wrap and upward shift issue is in `internal/surfaces/tui/app/rendering.go`: `evidenceDisplayName` trims the raw tool name before display."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})

	assertObjectiveRelevanceAdvisoryForTest(t, runner, answer, results, "answer_objective_unmet:citation_mismatch")
})

var _ = ginkgo.It("FinalAnswerAcceptanceTreatsCitationlessObjectiveParrotWithUnrelatedReadAsAdvisory", func() {
	t := ginkgo.GinkgoT()
	brief := readOnlyConversationBriefForTest("Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.")
	results := []workspace.ToolResult{
		readFileSourceWindowResult(
			"internal/surfaces/tui/app/rendering.go",
			1,
			9,
			0,
			"1:package app\n2:\n3:func evidenceDisplayName(rawTool string) string {\n4:\treturn strings.TrimSpace(rawTool)\n5:}\n",
		),
	}
	answer := "The TUI chat input newline wrap and upward shift issue is caused by trimming the raw tool name before display."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})

	assertObjectiveRelevanceAdvisoryForTest(t, runner, answer, results, "answer_objective_unmet:citation_mismatch")
})

var _ = ginkgo.It("FinalAnswerAcceptanceAppliesToDirectAnswerTaskFamilyAlias", func() {
	t := ginkgo.GinkgoT()
	brief := readOnlyConversationBriefForTest("Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.")

	brief.TaskFamily = string(autonomy.TaskFamilyDirectAnswer)

	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Facts:   contextPackCandidateFactsForTest("internal/surfaces/tui/app/rendering.go"),
		},
		readFileSourceWindowResult(
			"internal/surfaces/tui/app/rendering.go",
			1,
			9,
			0,
			"1:package app\n2:\n3:func evidenceDisplayName(rawTool string) string {\n4:\treturn strings.TrimSpace(rawTool)\n5:}\n",
		),
	}
	answer := "The relevant code is `internal/surfaces/tui/app/rendering.go`: `evidenceDisplayName` trims the raw tool name before display."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})

	assertObjectiveRelevanceAdvisoryForTest(t, runner, answer, results, "answer_objective_unmet:citation_mismatch")
})

var _ = ginkgo.It("FinalAnswerAcceptanceTreatsAliasInsufficientEvidenceWithoutSourceBodyAsAdvisory", func() {
	t := ginkgo.GinkgoT()
	brief := readOnlyConversationBriefForTest("Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.")

	brief.TaskFamily = string(autonomy.TaskFamilyDirectAnswer)

	results := []workspace.ToolResult{{
		Tool:    "context_pack",
		Summary: "Context pack wrote packet",
		Facts:   contextPackCandidateFactsForTest("internal/surfaces/tui/app/rendering.go"),
	}}
	answer := "I cannot determine from the available evidence why the TUI chat input first wraps to a newline."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})

	relevance := assertObjectiveRelevanceAdvisoryForTest(t, runner, answer, results, "answer_objective_unmet:no_source_body_evidence")
	if !strings.Contains(relevance.Reason, "before source-body or diagnostic evidence") ||
		!strings.Contains(relevance.Reason, "diagnostic is advisory") {
		ginkgo.Fail(fmt.Sprintf("expected source-body insufficiency advisory reason, got %q", relevance.Reason), 1)
	}
})

var _ = ginkgo.It("FinalAnswerAcceptanceRejectsAliasInsufficientEvidenceWhenSourceBodyIsAnswerable", func() {
	brief := readOnlyConversationBriefForTest("What is the role of evidence in the harness?")
	brief.TaskFamily = string(autonomy.TaskFamilyDirectAnswer)

	results := []workspace.ToolResult{
		readFileSourceWindowResult(
			"internal/runtime/agent/evidence_readonly.go",
			1,
			9,
			0,
			"1:package agent\n2:\n3:// Evidence records inspected paths and source-body support for harness readiness.\n4:type SourceEvidenceState struct { SourceBodyPaths []string }\n5:func evidenceReadiness() {}\n",
		),
	}
	answer := "I cannot determine from the available evidence what role evidence has in the harness."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})

	projection := newToolResultEvidenceProjection(results)
	relevance := readOnlyClosureDecisionForProjection(
		readOnlyDirectAnswerAcceptanceLedgerBrief(runner.state.runtimeBrief),
		projection,
	).DirectAnswerRelevance(answer)
	if relevance.BlockerKind != answerObjectiveUnmetInsufficientEvidenceClaimed {
		ginkgo.Fail(fmt.Sprintf("direct answer relevance blocker = %q, want %q", relevance.BlockerKind, answerObjectiveUnmetInsufficientEvidenceClaimed), 1)
	}
	if !strings.Contains(relevance.Reason, "evidence appears answerable") ||
		!strings.Contains(relevance.Reason, "diagnostic is advisory") {
		ginkgo.Fail(fmt.Sprintf("expected answerable evidence advisory reason, got %q", relevance.Reason), 1)
	}

	acceptance := runner.evaluateFinalAnswerAcceptance(answer, results)
	if acceptance.Decision != finalAnswerAcceptanceRejected ||
		acceptance.PolicyDecision != autonomy.PolicyReject ||
		acceptance.BlockerKind != string(turnViolationNonAnswerProse) ||
		acceptance.Reason != string(turnReasonAnswerableEvidenceInsufficient) {

		ginkgo.Fail(fmt.Sprintf("answerable insufficient-evidence closeout must be rejected, got %#v", acceptance), 1)
	}
	if !acceptance.ContractAssessment.HasDiagnostic(turnDiagnosticObjectiveRelevance) ||
		len(acceptance.ContractAssessment.Violations) == 0 {

		ginkgo.Fail(fmt.Sprintf("expected objective diagnostic plus enforceable violation, got %#v", acceptance.ContractAssessment), 1)
	}
})

var _ = ginkgo.It("FinalAnswerAcceptanceRejectsReadOnlyReadinessNudgeEcho", func() {
	brief := readOnlyConversationBriefForTest("What is the role of evidence in this harness? Where does it come from and what is it used for?")
	results := []workspace.ToolResult{
		readFileSourceWindowResult(
			"docs/architecture.md",
			1,
			80,
			0,
			strings.Join([]string{
				"1:# Idiom Architecture",
				"2:Idiom is a local coding-agent harness.",
				"3:The model owns strategy; the harness owns contracts, evidence, permissions, audit, and verification.",
				"4:Repository evidence feeds runtime briefs, answer grounding, audit trails, and verification decisions.",
			}, "\n"),
		),
	}
	answer := strings.Join([]string{
		"The read-only evidence is sufficient for a grounded answer to this question.",
		"",
		"Why a final answer is now valid: the current evidence already names inspected source paths `docs/architecture.md`.",
		"",
		"Core evidence paths: `docs/architecture.md`",
		"",
		"A final answer is available unless one exact unresolved claim remains.",
		"",
		"If one more source read would materially change the answer, take a bounded follow-up through the normal tool policy.",
	}, "\n")
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "non_answer_prose" {
		ginkgo.Fail(fmt.Sprintf("readiness nudge echo got (%q, %q), want non_answer_prose", kind, reason), 1)
	}

	if !looksLikeReadOnlyReadinessNudgeEcho(answer) {
		ginkgo.Fail("test fixture should exercise readiness-nudge echo detector", 1)
	}
})

var _ = ginkgo.It("FinalAnswerAcceptanceTreatsAliasObjectiveParrotWithWrongSourceAsAdvisory", func() {
	t := ginkgo.GinkgoT()
	brief := readOnlyConversationBriefForTest("Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.")

	brief.TaskFamily = string(autonomy.TaskFamilyDirectAnswer)

	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Facts:   contextPackCandidateFactsForTest("internal/surfaces/tui/app/rendering.go"),
		},
		readFileSourceWindowResult(
			"internal/surfaces/tui/app/rendering.go",
			1,
			9,
			0,
			"1:package app\n2:\n3:func evidenceDisplayName(rawTool string) string {\n4:\treturn strings.TrimSpace(rawTool)\n5:}\n",
		),
	}
	answer := "The TUI chat input newline wrap and upward shift issue is in `internal/surfaces/tui/app/rendering.go`: `evidenceDisplayName` trims the raw tool name before display."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})

	assertObjectiveRelevanceAdvisoryForTest(t, runner, answer, results, "answer_objective_unmet:citation_mismatch")
})

var _ = ginkgo.It("GenericReadOnlyEvidenceReadyRejectsAliasWrongSource", func() {
	brief := readOnlyConversationBriefForTest("Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.")

	brief.TaskFamily = string(autonomy.TaskFamilyDirectAnswer)

	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Facts:   contextPackCandidateFactsForTest("internal/surfaces/tui/app/rendering.go"),
		},
		readFileSourceWindowResult(
			"internal/surfaces/tui/app/rendering.go",
			1,
			9,
			0,
			"1:package app\n2:\n3:func evidenceDisplayName(rawTool string) string {\n4:\treturn strings.TrimSpace(rawTool)\n5:}\n",
		),
	}

	if genericReadOnlyAnswerEvidenceReady(brief, results) {
		ginkgo.Fail("direct_answer alias readiness should not close from an unrelated candidate source body", 1)
	}
})

var _ = ginkgo.It("ConversationEvidenceSatisfiedNormalizesDirectAnswerAlias", func() {
	brief := readOnlyConversationBriefForTest("Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.")

	brief.TaskFamily = string(autonomy.TaskFamilyDirectAnswer)

	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Facts:   contextPackCandidateFactsForTest("internal/surfaces/tui/app/rendering.go"),
		},
		readFileSourceWindowResult(
			"internal/surfaces/tui/app/rendering.go",
			1,
			9,
			0,
			"1:package app\n2:\n3:func evidenceDisplayName(rawTool string) string {\n4:\treturn strings.TrimSpace(rawTool)\n5:}\n",
		),
	}

	if conversationEvidenceSatisfied(brief, results) {
		ginkgo.Fail("direct_answer alias should use objective-relevant source-body evidence, not any candidate body", 1)
	}

	results[1] = readFileSourceWindowResult(
		"internal/surfaces/tui/app/chat_input.go",
		1,
		9,
		0,
		"1:package app\n2:\n3:func syncChatInputHeight() {\n4:\t// chat input newline wrap keeps the first existing line visible instead of shifting upward\n5:}\n",
	)
	if !conversationEvidenceSatisfied(brief, results) {
		ginkgo.Fail("direct_answer alias should be satisfied by objective-relevant source-body evidence", 1)
	}
})

var _ = ginkgo.It("RunnerReadinessStateNormalizesDirectAnswerAlias", func() {
	brief := readOnlyConversationBriefForTest("Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.")

	brief.TaskFamily = string(autonomy.TaskFamilyDirectAnswer)

	results := []workspace.ToolResult{{
		Tool:    "context_pack",
		Summary: "Context pack wrote packet",
		Facts:   contextPackCandidateFactsForTest("internal/surfaces/tui/app/rendering.go"),
	}}

	readiness := runnerReadinessState("main", brief, results, nil, planThenExecutePathAuthority{})
	if readiness.ReadyToAnswer {
		ginkgo.Fail(fmt.Sprintf("direct_answer alias should not be answer-ready from localization only: %#v", readiness), 1)
	}

	if !slices.Contains(readiness.OpenObligations, "read_only_source_body_evidence_pending") {
		ginkgo.Fail(fmt.Sprintf("expected source-body obligation for direct_answer alias, got %#v", readiness.OpenObligations), 1)
	}
})

var _ = ginkgo.It("FinalAnswerAcceptanceAllowsObjectiveRelevantSourceBackedAnswer", func() {
	brief := readOnlyConversationBriefForTest("Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.")
	results := []workspace.ToolResult{
		readFileSourceWindowResult(
			"internal/surfaces/tui/app/chat_input.go",
			1,
			9,
			0,
			"1:package app\n2:\n3:func syncChatInputHeight() {\n4:\t// chat input newline wrap keeps the first existing line visible instead of shifting upward\n5:}\n",
		),
	}
	answer := "The chat input newline wrap issue is handled in `internal/surfaces/tui/app/chat_input.go`: `syncChatInputHeight` keeps the first existing line visible instead of letting it shift upward."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
	results = bindCurrentSourceWindowsForContractTest(ginkgo.GinkgoT(), runner, results)

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "" || reason != "" {
		ginkgo.Fail(fmt.Sprintf("objective-relevant source-backed answer blocked: kind=%q reason=%q", kind, reason), 1)
	}
})

var _ = ginkgo.It("FinalAnswerAcceptanceAllowsSourceBackedPlanningReadinessWhyAnswer", func() {
	cases := []struct {
		name       string
		statusPath string
		trackPath  string
	}{
		{
			name:       "planning-paths",
			statusPath: ".planning/campaign-status.md",
			trackPath:  ".planning/campaigns/rec-025-tick-path-audit-tracker.md",
		},
		{
			name:       "neutral-paths",
			statusPath: "docs/work/status.md",
			trackPath:  "trackers/rec-025.md",
		},
	}

	for _, tc := range cases {
		ginkgo.By(tc.name, func() {
			brief := readOnlyConversationBriefForTest("Why are those not dispatchable?")
			brief.EvidencePaths = []string{tc.statusPath, tc.trackPath}
			results := []workspace.ToolResult{
				readFileSourceWindowResult(
					tc.statusPath,
					1,
					8,
					8,
					strings.Join([]string{
						"1:# Campaign status",
						"2:Approved campaigns exist for REC-025, REC-026, REC-027, and REC-028.",
						"3:These campaign trackers are planning-level approvals, not dispatchable implementation atoms.",
						"4:They remain blocked until Atom 0.1 specs are written and intake passes.",
					}, "\n"),
				),
				readFileSourceWindowResult(
					tc.trackPath,
					1,
					7,
					7,
					strings.Join([]string{
						"1:# REC-025 tick-path audit tracker",
						"2:Status: approved planning.",
						"3:Dispatchable: no.",
						"4:Next gate: write the Atom 0.1 spec and pass atom intake.",
					}, "\n"),
				),
			}
			answer := strings.Join([]string{
				"They are not dispatchable because the approved campaign docs are still planning-level approvals, not implementation atoms.",
				fmt.Sprintf("`%s` says the trackers remain blocked until Atom 0.1 specs are written and intake passes.", tc.statusPath),
				fmt.Sprintf("`%s` records the same gate: approved planning, dispatchable no, next gate Atom 0.1 plus intake.", tc.trackPath),
			}, "\n")
			runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
			results = bindCurrentSourceWindowsForContractTest(ginkgo.GinkgoT(), runner, results)

			decision := readOnlyClosureDecisionForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results).
				FinalAnswerDecision(answer)
			if decision.BlockerKind != "" {
				ginkgo.Fail(fmt.Sprintf("planning readiness answer blocked by shared decision: %#v", decision), 1)
			}

			if !slices.Contains(decision.RelevantSourceBodyPaths, tc.statusPath) {
				ginkgo.Fail(fmt.Sprintf("expected status source body to be relevant, got %#v", decision.RelevantSourceBodyPaths), 1)
			}

			if kind, reason := runner.finalAnswerBlockerKind(answer, results); kind != "" || reason != "" {
				ginkgo.Fail(fmt.Sprintf("planning readiness answer blocked by runner: kind=%q reason=%q", kind, reason), 1)
			}
		})
	}
})

var _ = ginkgo.It("FinalAnswerAcceptanceRejectsPlanningPathWithoutBodyOwnedSupport", func() {
	t := ginkgo.GinkgoT()
	brief := readOnlyConversationBriefForTest("Why are those not dispatchable?")
	brief.EvidencePaths = []string{".planning/campaign-status.md"}
	results := []workspace.ToolResult{
		readFileSourceWindowResult(
			".planning/campaign-status.md",
			1,
			6,
			6,
			strings.Join([]string{
				"1:# Campaign status",
				"2:Approved campaigns exist for REC-025 and REC-026.",
				"3:Owners should review the schedule before the next checkpoint.",
				"4:No atom intake or implementation-readiness decision is recorded here.",
			}, "\n"),
		),
	}
	answer := "`.planning/campaign-status.md` shows they are not dispatchable because Atom 0.1 intake has not passed."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})

	assertObjectiveRelevanceAdvisoryForTest(t, runner, answer, results, "answer_objective_unmet:citation_mismatch")
})

var _ = ginkgo.It("FinalAnswerAcceptanceRejectsContradictedDispatchabilityAnswer", func() {
	t := ginkgo.GinkgoT()
	brief := readOnlyConversationBriefForTest("Why are those not dispatchable?")
	brief.EvidencePaths = []string{"docs/work/status.md"}
	results := []workspace.ToolResult{
		readFileSourceWindowResult(
			"docs/work/status.md",
			1,
			6,
			6,
			strings.Join([]string{
				"1:# Campaign status",
				"2:Dispatchable: yes.",
				"3:Atom 0.1 spec is complete and intake passed.",
				"4:The implementation atom is ready to start.",
			}, "\n"),
		),
	}
	answer := "`docs/work/status.md` shows they are not dispatchable because Atom 0.1 intake has not passed."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})

	assertObjectiveRelevanceAdvisoryForTest(t, runner, answer, results, "answer_objective_unmet:citation_mismatch")
})

var _ = ginkgo.It("FinalAnswerAcceptanceAllowsLowConfidenceStrategicSourceBackedAnswer", func() {
	objective := "Why would replacing this with model prose fit our north star or risk leaking unverified information to the user?"
	brief := readOnlyConversationBriefForTest(objective)
	results := []workspace.ToolResult{
		readFileSourceWindowResult(
			"docs/decisioning-north-star.md",
			1,
			6,
			0,
			strings.Join([]string{
				"1:# Decisioning North Star",
				"2:The model owns strategy; the harness owns contracts.",
				"3:Final answers must be grounded in verified source evidence rather than unverified model prose.",
				"4:Deterministic closeouts prevent raw unverified information from leaking to users.",
			}, "\n"),
		),
	}
	answer := "No. Free-form prose moves the closeout away from deterministic contract enforcement."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})

	decision := readOnlyClosureDecisionForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results).
		FinalAnswerDecision(answer)
	if decision.BlockerKind != "" {
		ginkgo.Fail(fmt.Sprintf("low-confidence strategic answer blocked by shared decision: %#v", decision), 1)
	}

	if decision.Status != "checked" && decision.Status != "checked_low_confidence_term_match" {
		ginkgo.Fail(fmt.Sprintf("expected checked objective status, got %q with terms %#v", decision.Status, decision.AnswerObjectiveTerms), 1)
	}

	if !slices.Contains(decision.AcceptedEvidencePaths, "docs/decisioning-north-star.md") {
		ginkgo.Fail(fmt.Sprintf("expected accepted source-body evidence path, got %#v", decision.AcceptedEvidencePaths), 1)
	}

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "" || reason != "" {
		ginkgo.Fail(fmt.Sprintf("low-confidence strategic answer blocked by runner: kind=%q reason=%q", kind, reason), 1)
	}

	acceptance := runner.evaluateFinalAnswerAcceptance(answer, results)
	if acceptance.ObjectiveRelevanceStatus != "checked_low_confidence_term_match" {
		ginkgo.Fail(fmt.Sprintf("expected accepted telemetry status checked_low_confidence_term_match, got %q", acceptance.ObjectiveRelevanceStatus), 1)
	}
})

var _ = ginkgo.It("FinalAnswerAcceptanceAllowsReplayStyleNorthStarRiskAnswer", func() {
	t := ginkgo.GinkgoT()
	objective := "Would it fit our north star to replace this with model prose? Or do we ris leaking unverified information to the user?"
	brief := readOnlyConversationBriefForTest(objective)
	root := t.TempDir()
	gomega.Expect(writeTestFile(root, "docs/decisioning-north-star.md", strings.Join([]string{
		"Read-only conversation, direct-answer, architecture, and review lanes are strategy lanes.",
		"The model owns strategy; the harness owns contracts.",
		"In read-only strategy lanes, do not force answer synthesis merely because the harness thinks evidence is sufficient.",
	}, "\n")+"\n")).To(gomega.Succeed())
	gomega.Expect(writeTestFile(root, "internal/runtime/agent/tools_surface.go", "package agent\nfunc completeWithoutMoreToolsWithReason() {}\n")).To(gomega.Succeed())
	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	results := []workspace.ToolResult{
		manager.Execute(context.Background(), workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: "docs/decisioning-north-star.md", StartLine: 1, EndLine: 3}),
		manager.Execute(context.Background(), workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: "internal/runtime/agent/tools_surface.go", StartLine: 1, EndLine: 2}),
	}
	answer := strings.Join([]string{
		"I now have sufficient evidence to answer your north-star question.",
		"",
		"**No, it would not fit our north star to replace the deterministic template answer with free-form model prose in read-only strategy lanes. And yes, doing so would risk leaking unverified information to the user.**",
		"",
		"From `docs/decisioning-north-star.md`:",
		"",
		"> Read-only conversation, direct-answer, architecture, and review lanes are strategy lanes.",
		"",
		"`internal/runtime/agent/tools_surface.go` shows the harness-owned deterministic closeout path, so the safer fix is to keep verified contract prose and improve the template surface only when source-backed evidence exists.",
	}, "\n")
	runner := newRunnerRunFixture(&Runner{Workspace: manager, RuntimeBrief: brief})

	decision := readOnlyClosureDecisionForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results).
		FinalAnswerDecision(answer)
	if decision.BlockerKind != "" {
		ginkgo.Fail(fmt.Sprintf("replay-style north-star answer blocked by shared decision: %#v", decision), 1)
	}

	if decision.Status != "checked" && decision.Status != "checked_low_confidence_term_match" {
		ginkgo.Fail(fmt.Sprintf("expected checked objective status, got %q with terms %#v", decision.Status, decision.AnswerObjectiveTerms), 1)
	}

	if kind, reason := runner.finalAnswerBlockerKind(answer, results); kind != "" || reason != "" {
		ginkgo.Fail(fmt.Sprintf("replay-style north-star answer blocked by runner: kind=%q reason=%q", kind, reason), 1)
	}
})

var _ = ginkgo.It("FinalAnswerAcceptanceRejectsPriorTopicAnswerForNamingQuestion", func() {
	t := ginkgo.GinkgoT()
	objective := "we need a better and more generic name than qwen for the agent in this project... what is a good thematic yet cute option"
	brief := readOnlyConversationBriefForTest(objective)
	results := []workspace.ToolResult{
		readFileSourceWindowResult(
			"docs/idiom-strategic-thesis.md",
			1,
			6,
			0,
			strings.Join([]string{
				"1:# Idiom strategic thesis",
				"2:Idiom is a local coding-agent harness originally optimized around Qwen models.",
				"3:The project focuses on repo memory, freshness, contracts, audit, and evals.",
			}, "\n"),
		),
		readFileSourceWindowResult(
			"internal/runtime/agent/implementation_flow.go",
			2219,
			2290,
			0,
			strings.Join([]string{
				"2219:func canonicalMutationBlockerFinalAnswer() string {",
				"2220:\treturn \"<<QUESTION>>: required edit paths remain unchanged\"",
				"2221:}",
			}, "\n"),
		),
	}
	answer := strings.Join([]string{
		"I have enough evidence to answer this question directly.",
		"",
		"## Answer: It would not fit our north star to replace `canonicalMutationBlockerFinalAnswer` with model prose.",
		"",
		"The north star says the model owns strategy and the harness owns contracts. The `canonicalMutationBlockerFinalAnswer` is a harness-owned contract artifact, not a model strategy output.",
		"",
		"Do not replace `canonicalMutationBlockerFinalAnswer` with model prose.",
	}, "\n")
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})

	assertObjectiveRelevanceAdvisoryForTest(t, runner, answer, results, "answer_objective_unmet:term_mismatch")
})

var _ = ginkgo.It("FinalAnswerAcceptanceAllowsGenericLowConfidenceSourceBackedAnswer", func() {
	objective := "Why would replacing verified summaries with generated narrative affect customer release reports?"
	brief := readOnlyConversationBriefForTest(objective)
	results := []workspace.ToolResult{
		readFileSourceWindowResult(
			"internal/runtime/agent/runner_core.go",
			1,
			8,
			0,
			strings.Join([]string{
				"1:package agent",
				"2:// Verified summaries are deterministic release-report closeouts.",
				"3:// Generated narrative is separate from the customer-facing contract.",
				"4:func finalReportCloseout() {}",
			}, "\n"),
		),
	}
	answer := "No. The release closeout remains under a deterministic contract boundary."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})

	decision := readOnlyClosureDecisionForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results).
		FinalAnswerDecision(answer)
	if decision.BlockerKind != "" {
		ginkgo.Fail(fmt.Sprintf("generic low-confidence answer blocked by shared decision: %#v", decision), 1)
	}

	if decision.Status != "checked_low_confidence_term_match" {
		ginkgo.Fail(fmt.Sprintf("expected low-confidence objective status, got %q with terms %#v", decision.Status, decision.AnswerObjectiveTerms), 1)
	}

	if kind, reason := runner.finalAnswerBlockerKind(answer, results); kind != "" || reason != "" {
		ginkgo.Fail(fmt.Sprintf("generic low-confidence answer blocked by runner: kind=%q reason=%q", kind, reason), 1)
	}
})

var _ = ginkgo.It("FinalAnswerAcceptanceKeepsCandidateOnlyCitationHardRejectedAfterLowConfidenceMatch", func() {
	objective := "Would it fit our north star to replace this with model prose? Or do we risk leaking unverified information to the user?"
	brief := readOnlyConversationBriefForTest(objective)
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Facts:   contextPackCandidateFactsForTest("internal/runtime/agent/runner_tests_test.go"),
		},
		readFileSourceWindowResult(
			"docs/decisioning-north-star.md",
			1,
			6,
			0,
			strings.Join([]string{
				"1:# Decisioning North Star",
				"2:The model owns strategy; the harness owns contracts.",
				"3:Final answers must be grounded in verified source evidence rather than unverified model prose.",
			}, "\n"),
		),
	}
	answer := "No. Free-form prose would move deterministic closeout language away from the contract boundary; `internal/runtime/agent/runner_tests_test.go` confirms the tested closeout behavior."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "source_grounding_unmet" {
		ginkgo.Fail(fmt.Sprintf("candidate-only citation got (%q, %q), want source_grounding_unmet", kind, reason), 1)
	}
})

var _ = ginkgo.It("FinalAnswerAcceptanceTreatsLowConfidenceWrongSourceCitationAsAdvisory", func() {
	t := ginkgo.GinkgoT()
	objective := "Would it fit our north star to replace this with model prose? Or do we risk leaking unverified information to the user?"
	brief := readOnlyConversationBriefForTest(objective)
	manager, sourceResults := sourceWindowManagerResultsForTest(
		"docs/decisioning-north-star.md",
		"internal/runtime/agent/tools_surface.go",
	)
	results := append([]workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Facts:   contextPackCandidateFactsForTest("docs/decisioning-north-star.md"),
		},
	}, sourceResults...)
	answer := "No. Free-form prose would move deterministic closeout language away from the contract boundary in `internal/runtime/agent/tools_surface.go`."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief, Workspace: manager})

	assertObjectiveRelevanceAdvisoryForTest(t, runner, answer, results, "answer_objective_unmet:term_mismatch")
})

var _ = ginkgo.It("FinalAnswerAcceptanceRejectsLowConfidenceSourceInventoryWithOneSupportTerm", func() {
	objective := "Would it fit our north star to replace this with model prose? Or do we risk leaking unverified information to the user?"
	brief := readOnlyConversationBriefForTest(objective)
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read docs/decisioning-north-star.md lines 1-6",
		Output: strings.Join([]string{
			"1:# Decisioning North Star",
			"2:The model owns strategy; the harness owns contracts.",
			"3:Final answers must be grounded in verified source evidence rather than unverified model prose.",
		}, "\n"),
	}}
	answer := "The strongest implementation evidence is in `docs/decisioning-north-star.md`; it mentions prose."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "source_grounding_unmet" {
		ginkgo.Fail(fmt.Sprintf("source-inventory answer should remain rejected by grounding contract, got kind=%q reason=%q", kind, reason), 1)
	}
})

var _ = ginkgo.It("LowConfidenceObjectiveSupportTermsIgnoreBroadTerms", func() {
	objective := "Does the answer source file risk misleading the user?"
	answer := "The answer cites a source file and reduces user risk."

	if terms := readOnlySharedObjectiveSupportTerms(answer, objective); len(terms) > 0 {
		ginkgo.Fail(fmt.Sprintf("broad terms must not satisfy low-confidence support, got %#v", terms), 1)
	}
})

var _ = ginkgo.It("LowConfidenceNonFinalProseDistinguishesSoftScaffoldFromHardLeak", func() {
	answer := strings.Join([]string{
		"**No, this would risk leaking unverified information to the user.**",
		"",
		"What this establishes:",
		"The north-star boundary keeps strategy with the model and contracts with the harness.",
		"",
		"Verification boundary:",
		"The deterministic template keeps model prose behind source-backed verification.",
	}, "\n")

	if readOnlyAnswerHasHardNonFinalProse(answer) {
		ginkgo.Fail("soft scaffold wording with a direct strategic answer should not be treated as a hard non-answer leak", 1)
	}

	if !readOnlyScaffoldFinalAnswer(answer) {
		ginkgo.Fail("test fixture should exercise the soft scaffold marker path", 1)
	}
})

var _ = ginkgo.It("FinalAnswerAcceptanceAllowsNamedFileContentAnswer", func() {
	brief := readOnlyConversationBriefForTest("What is in internal/runtime/agent/runner_core.go?")
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/runtime/agent/runner_core.go lines 1-20",
		Output:  "1:package agent\n2:\n3:func RunExample() {}\n",
	}}
	answer := "runner_core.go contains package agent."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "" || reason != "" {
		ginkgo.Fail(fmt.Sprintf("named-file source-backed answer blocked: kind=%q reason=%q", kind, reason), 1)
	}
})

var _ = ginkgo.It("FinalAnswerAcceptanceAllowsFixturePurposeSummary", func() {
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
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})

	kind, reason := runner.finalAnswerBlockerKind(answer, results)
	if kind != "" || reason != "" {
		ginkgo.Fail(fmt.Sprintf("fixture purpose summary blocked: kind=%q reason=%q", kind, reason), 1)
	}
})

var _ = ginkgo.It("FinalAnswerAcceptanceRejectsNamedFileWhyAnswerWithUnrelatedBody", func() {
	t := ginkgo.GinkgoT()
	brief := readOnlyConversationBriefForTest("Why does the TUI chat input first wrap to a newline in internal/surfaces/tui/app/rendering.go?")
	results := []workspace.ToolResult{
		readFileSourceWindowResult(
			"internal/surfaces/tui/app/rendering.go",
			1,
			9,
			0,
			"1:package app\n2:\n3:func evidenceDisplayName(rawTool string) string {\n4:\treturn strings.TrimSpace(rawTool)\n5:}\n",
		),
	}
	answer := "`internal/surfaces/tui/app/rendering.go` contains `evidenceDisplayName`, which trims the raw tool name before display."
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})

	assertObjectiveRelevanceAdvisoryForTest(t, runner, answer, results, "answer_objective_unmet:citation_mismatch")
})

var _ = ginkgo.It("AcceptedAnswerEvidencePathsPreferObjectiveRelevantSourceBody", func() {
	brief := readOnlyConversationBriefForTest("Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.")
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Facts: contextPackCandidateFactsForTest(
				"internal/runtime/agent/runner_core.go",
				"internal/surfaces/tui/app/rendering.go",
			),
		},
		readFileSourceWindowResult(
			"internal/surfaces/tui/app/chat_input.go",
			1,
			9,
			0,
			"1:package app\n2:\n3:func syncChatInputHeight() {\n4:\t// chat input newline wrap keeps the first existing line visible instead of shifting upward\n5:}\n",
		),
	}
	answer := "The chat input newline wrap issue is in `internal/surfaces/tui/app/chat_input.go`, where the input height sync keeps the first existing line visible."

	paths := acceptedFinalAnswerEvidencePaths(answer, brief, results)
	if len(paths) == 0 {
		ginkgo.Fail("expected evidence paths", 1)
	}

	if paths[0] != "internal/surfaces/tui/app/chat_input.go" {
		ginkgo.Fail(fmt.Sprintf("expected objective-relevant source body first, got %v", paths), 1)
	}
})

var _ = ginkgo.It("AcceptedAnswerEvidencePathsAliasIgnoresUnrelatedCandidateBody", func() {
	brief := readOnlyConversationBriefForTest("Find why the TUI chat input first wraps to a newline and the existing line shifts upward out of view.")

	brief.TaskFamily = string(autonomy.TaskFamilyDirectAnswer)

	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote packet",
			Facts:   contextPackCandidateFactsForTest("internal/surfaces/tui/app/rendering.go"),
		},
		readFileSourceWindowResult(
			"internal/surfaces/tui/app/rendering.go",
			1,
			9,
			0,
			"1:package app\n2:\n3:func evidenceDisplayName(rawTool string) string {\n4:\treturn strings.TrimSpace(rawTool)\n5:}\n",
		),
	}
	answer := "The TUI chat input newline wrap and upward shift issue is not established by the inspected source."

	paths := acceptedFinalAnswerEvidencePaths(answer, brief, results)
	if len(paths) != 0 {
		ginkgo.Fail(fmt.Sprintf("alias evidence paths should not report unrelated candidate source body, got %v", paths), 1)
	}
})

var _ = ginkgo.It("ReadOnlySourceBodyEvidenceSummary", func() {
	bodyPath := "internal/runtime/agent/runner_core.go"
	candidatePath := "internal/surfaces/tui/app/rendering.go"

	results := []workspace.ToolResult{
		readFileSourceWindowResult(bodyPath, 1, 20, 0, "package agent\nfunc Run() {}\n"),
		{
			Tool:    "search_content",
			Summary: "Found 5 matching line(s).",
			Output:  candidatePath + ":42: func Render() {}",
			Facts: &toolfacts.ToolResultFacts{
				CandidatePaths:      []string{candidatePath},
				HasDiscoveryTargets: true,
				DiscoveryStrength:   toolfacts.DiscoveryStrengthSourceOwner,
				SourceOwnerFound:    true,
				SearchHitCount:      5,
			},
		},
		{
			Tool:    "search_content",
			Summary: "Found 3 more candidates",
			Output:  "internal/surfaces/tui/app/model_state.go:10: type Model struct{}",
			Facts: &toolfacts.ToolResultFacts{
				CandidatePaths:      []string{"internal/surfaces/tui/app/model_state.go"},
				HasDiscoveryTargets: true,
				DiscoveryStrength:   toolfacts.DiscoveryStrengthSourceOwner,
				SourceOwnerFound:    true,
				SearchHitCount:      3,
			},
		},
	}

	ginkgo.By("body-and-candidate", func() {
		summary := readOnlySourceBodyEvidenceSummary(results)
		if summary == "" {
			ginkgo.Fail("expected non-empty summary", 1)
		}

		if !strings.Contains(summary, bodyPath) {
			ginkgo.Fail(fmt.Sprintf("expected body path %q in summary, got: %s", bodyPath, summary), 1)
		}

		if !strings.Contains(summary, "Source-body evidence available") {
			ginkgo.Fail(fmt.Sprintf("expected body evidence header in summary, got: %s", summary), 1)
		}

		if !strings.Contains(summary, candidatePath) {
			ginkgo.Fail(fmt.Sprintf("expected candidate path %q in summary, got: %s", candidatePath, summary), 1)
		}

		if !strings.Contains(summary, "Candidate paths") {
			ginkgo.Fail(fmt.Sprintf("expected candidate paths header in summary, got: %s", summary), 1)
		}

		if !strings.Contains(summary, "cite only the body-backed paths") {
			ginkgo.Fail(fmt.Sprintf("expected guidance in summary, got: %s", summary), 1)
		}
	})

	ginkgo.By("no-candidates", func() {
		bodyOnly := []workspace.ToolResult{
			readFileSourceWindowResult(bodyPath, 1, 20, 0, "package agent\nfunc Run() {}\n"),
		}
		summary := readOnlySourceBodyEvidenceSummary(bodyOnly)
		if summary == "" {
			ginkgo.Fail("expected non-empty summary for body-only", 1)
		}

		if !strings.Contains(summary, bodyPath) {
			ginkgo.Fail(fmt.Sprintf("expected body path in summary, got: %s", summary), 1)
		}

		if strings.Contains(summary, "Candidate paths") {
			ginkgo.Fail(fmt.Sprintf("expected no candidate section for body-only, got: %s", summary), 1)
		}
	})

	ginkgo.By("no-body", func() {
		candidateOnly := []workspace.ToolResult{
			{
				Tool:    "search_content",
				Summary: "Found 2 matching line(s).",
				Output:  "internal/surfaces/tui/app/panel.go:5: type Panel struct{}",
				Facts: &toolfacts.ToolResultFacts{
					CandidatePaths:      []string{"internal/surfaces/tui/app/panel.go"},
					HasDiscoveryTargets: true,
					DiscoveryStrength:   toolfacts.DiscoveryStrengthSourceOwner,
					SourceOwnerFound:    true,
					SearchHitCount:      2,
				},
			},
		}
		summary := readOnlySourceBodyEvidenceSummary(candidateOnly)
		if summary == "" {
			ginkgo.Fail("expected non-empty summary for candidate-only", 1)
		}

		if !strings.Contains(summary, "Candidate paths") {
			ginkgo.Fail(fmt.Sprintf("expected candidate section, got: %s", summary), 1)
		}

		if strings.Contains(summary, "Source-body evidence available") {
			ginkgo.Fail(fmt.Sprintf("expected no body section for candidate-only, got: %s", summary), 1)
		}
	})

	ginkgo.By("empty-results", func() {
		summary := readOnlySourceBodyEvidenceSummary(nil)
		if summary != "" {
			ginkgo.Fail(fmt.Sprintf("expected empty summary for nil results, got: %s", summary), 1)
		}
	})

	ginkgo.By("body-path-not-in-candidate", func() {
		summary := readOnlySourceBodyEvidenceSummary(results)
		// The body path must not appear in the candidate section
		if strings.Contains(summary, "Candidate paths") {
			parts := strings.Split(summary, "Candidate paths")
			if len(parts) > 1 && strings.Contains(parts[1], bodyPath) {
				ginkgo.Fail(fmt.Sprintf("body path %q must not appear in candidate section, summary: %s", bodyPath, summary), 1)
			}
		}
	})
})

var _ = ginkgo.It("ReadOnlyEvidencePacketParsesTypedPayloadWithoutProseHeadings", func() {
	payload, err := json.Marshal(ReadOnlyEvidencePacket{
		SchemaVersion:   readOnlyEvidencePacketPayloadType,
		Objective:       "Explain runner readiness.",
		Question:        "How does readiness work?",
		SourceBodyPaths: []string{"internal/runtime/agent/runner_core.go"},
		InspectedPaths:  []string{"internal/runtime/agent/runner_core.go"},
		CandidatePaths:  []string{"internal/runtime/agent/evidence_readonly.go"},
		Findings:        []ReadOnlyEvidenceFinding{{Path: "internal/runtime/agent/runner_core.go", Text: "runner.go computes read-only readiness from grounded source evidence.", Source: "read_file"}},
		Confidence:      "medium",
	})
	if err != nil {
		ginkgo.Fail(fmt.Sprint(err), 1)
	}

	result := workspace.ToolResult{
		Tool:        "task",
		Summary:     "Child task completed",
		Output:      "Plain child output with no inspected-paths or key-findings headings.",
		PayloadType: readOnlyEvidencePacketPayloadType,
		Payload:     payload,
	}

	packet, ok := readOnlyEvidencePacketFromResult(result)
	if !ok {
		ginkgo.Fail("expected typed payload to parse without relying on prose headings", 1)
	}

	if !readOnlyEvidencePacketAnswerable(packet) {
		ginkgo.Fail(fmt.Sprintf("expected source-backed typed packet to be answerable: %#v", packet), 1)
	}

	ledger := readOnlyAnswerabilityForResults(workerruntime.WorkerRuntimeBrief{
		Objective:         "Explain runner readiness.",
		PermissionProfile: "read_only_workspace",
	}, []workspace.ToolResult{result})
	if !ledger.Answerable() {
		ginkgo.Fail(fmt.Sprintf("typed task packet should satisfy read-only answerability: answerable=%t terms=%#v ledger=%#v",
			readOnlyEvidencePacketAnswerableForObjective(packet, "Explain runner readiness."),
			readOnlyObjectivePromotionTerms("Explain runner readiness."),
			ledger), 1)
	}

	rendered := renderReadOnlyEvidencePacketForToolOutput(packet, result.Output)
	if !strings.Contains(rendered, "`internal/runtime/agent/runner_core.go`") || !strings.Contains(rendered, "runner.go computes") {
		ginkgo.Fail(fmt.Sprintf("rendered typed packet should expose paths and findings, got:\n%s", rendered), 1)
	}
})

var _ = ginkgo.It("ReadOnlyEvidencePacketFiltersAndNormalizesSourceBodyPaths", func() {
	packet := normalizeReadOnlyEvidencePacket(ReadOnlyEvidencePacket{
		SchemaVersion: readOnlyEvidencePacketPayloadType,
		Objective:     "Explain runner readiness.",
		SourceBodyPaths: []string{
			"./internal/runtime/agent/runner_core.go",
			"internal/runtime/agent/runner_core.go",
			"/tmp/outside/runner_core.go",
			"../outside/runner_core.go",
			".idiom/logs/conversations/session.jsonl",
			"logs/conversations/session.jsonl",
			".idiom/comprehension/views/pack.json",
		},
		InspectedPaths: []string{
			"./internal/runtime/agent/evidence_readonly.go",
			"/tmp/outside/evidence_readonly.go",
			"transcripts/session.md",
			"internal/runtime/agent/evidence_readonly.go",
		},
		CandidatePaths: []string{
			"internal/capabilities/workspace/manager.go",
			"../outside/manager.go",
			".codex/discovery/query.json",
		},
		Findings: []ReadOnlyEvidenceFinding{{
			Path: ".idiom/comprehension/views/pack.json",
			Text: "Generated context was inspected but not promoted as source-body evidence.",
		}},
	})

	if got, want := packet.SourceBodyPaths, []string{"internal/runtime/agent/runner_core.go"}; !slices.Equal(got, want) {
		ginkgo.Fail(fmt.Sprintf("SourceBodyPaths = %#v, want %#v", got, want), 1)
	}

	if got, want := packet.InspectedPaths, []string{"internal/runtime/agent/evidence_readonly.go"}; !slices.Equal(got, want) {
		ginkgo.Fail(fmt.Sprintf("InspectedPaths = %#v, want %#v", got, want), 1)
	}

	if got, want := packet.CandidatePaths, []string{"internal/capabilities/workspace/manager.go"}; !slices.Equal(got, want) {
		ginkgo.Fail(fmt.Sprintf("CandidatePaths = %#v, want %#v", got, want), 1)
	}

	if packet.Findings[0].Path != "" {
		ginkgo.Fail(fmt.Sprintf("artifact finding path should be cleared during normalization: %#v", packet.Findings[0]), 1)
	}
})

var _ = ginkgo.It("ReadOnlyAnswerabilityDoesNotAcceptTypedPacketWithOnlyOutsideRepoEvidence", func() {
	payload, err := json.Marshal(ReadOnlyEvidencePacket{
		SchemaVersion:   readOnlyEvidencePacketPayloadType,
		Objective:       "Explain runner readiness.",
		SourceBodyPaths: []string{"/tmp/outside/runner_core.go", "../outside/evidence_readonly.go"},
		InspectedPaths:  []string{"/tmp/outside/runner_core.go"},
		Findings: []ReadOnlyEvidenceFinding{{
			Path: "/tmp/outside/runner_core.go",
			Text: "outside packet claims runner readiness was inspected.",
		}},
		Confidence: "high",
	})
	if err != nil {
		ginkgo.Fail(fmt.Sprint(err), 1)
	}

	result := workspace.ToolResult{
		Tool:        "task",
		Summary:     "Child task completed",
		PayloadType: readOnlyEvidencePacketPayloadType,
		Payload:     payload,
	}

	packet, ok := readOnlyEvidencePacketFromResult(result)
	if ok && (len(packet.SourceBodyPaths) != 0 || len(packet.InspectedPaths) != 0 || packet.Findings[0].Path != "") {
		ginkgo.Fail(fmt.Sprintf("outside-only typed packet paths should be cleared, got %#v", packet), 1)
	}

	ledger := readOnlyAnswerabilityForResults(workerruntime.WorkerRuntimeBrief{
		Objective:         "Explain runner readiness.",
		PermissionProfile: "read_only_workspace",
	}, []workspace.ToolResult{result})
	if ledger.Answerable() || len(ledger.SourceBodyPaths) != 0 || len(ledger.RelevantSourceBodyPaths) != 0 {
		ginkgo.Fail(fmt.Sprintf("outside-only typed packet must not satisfy answerability: %#v", ledger), 1)
	}
})

var _ = ginkgo.It("ReadOnlyEvidencePacketArtifactOnlyIsNotAnswerableAtPacketLevel", func() {
	packet := normalizeReadOnlyEvidencePacket(ReadOnlyEvidencePacket{
		SchemaVersion:   readOnlyEvidencePacketPayloadType,
		SourceBodyPaths: []string{".idiom/comprehension/views/pack.json", "logs/conversations/session.jsonl"},
		InspectedPaths:  []string{".idiom/discovery/queries/query.json"},
		Findings:        []ReadOnlyEvidenceFinding{{Text: "Only generated context artifacts were inspected."}},
		Confidence:      "medium",
	})

	if readOnlyEvidencePacketAnswerable(packet) {
		ginkgo.Fail(fmt.Sprintf("artifact-only packet must not be answerable: %#v", packet), 1)
	}

	if len(packet.SourceBodyPaths) != 0 || len(packet.InspectedPaths) != 0 {
		ginkgo.Fail(fmt.Sprintf("artifact paths should be filtered out at packet level: %#v", packet), 1)
	}
})

var _ = ginkgo.It("ReadOnlyEvidencePacketDoesNotRenderGeneratedArtifactRedirectAsAnswer", func() {
	answer := strings.Join([]string{
		"Generated evidence artifact .idiom/discovery/queries/query.json was not read because generated discovery/context JSON is not source-body evidence.",
		"Use the source paths already named by the current discovery packet instead:",
		"- internal/runtime/agent/runner_core.go",
		"Next exact source probe example: {\"tool\":\"read_file\",\"path\":\"internal/runtime/agent/runner_core.go\"}",
	}, "\n")
	childResults := []workspace.ToolResult{
		readFileSourceWindowResult(
			"internal/runtime/agent/runner_core.go",
			1,
			20,
			0,
			"package agent\nfunc (r *Runner) Run() {}\n",
		),
	}

	packet, ok := buildReadOnlyEvidencePacket("Explain the runner.", answer, childResults)
	if !ok {
		ginkgo.Fail("expected packet to retain source paths even when redirect answer text is stripped", 1)
	}

	rendered := renderReadOnlyEvidencePacketForToolOutput(packet, answer)
	for _, forbidden := range []string{"Grounded answer:", "Generated evidence artifact", "Next exact source probe example"} {
		if strings.Contains(rendered, forbidden) {
			ginkgo.Fail(fmt.Sprintf("redirect guidance should not render as grounded answer %q:\n%s", forbidden, rendered), 1)
		}
	}

	if readOnlyEvidencePacketAnswerable(packet) {
		ginkgo.Fail(fmt.Sprintf("redirect-only packet must not be answerable: %#v", packet), 1)
	}
})

var _ = ginkgo.It("BuildReadOnlyEvidencePacketFromChildResultsAndAnswerText", func() {
	childResults := []workspace.ToolResult{
		readFileSourceWindowResult(
			"./internal/runtime/agent/runner_core.go",
			4310,
			4370,
			0,
			"package agent\nfunc readOnlyAnalysisReadyFromEvidence() bool { return true }\n",
		),
		readFileSourceWindowResult(
			".idiom/comprehension/views/pack.json",
			1,
			20,
			0,
			`{"candidate_files":[{"path":"internal/runtime/agent/runner_core.go"}]}`,
		),
		{
			Tool:    "search_content",
			Summary: "Found 1 matching line(s).",
			Output:  "internal/runtime/agent/evidence_readonly.go:55: func readOnlyAnswerabilityForResults\n",
			Facts: &toolfacts.ToolResultFacts{
				CandidatePaths:      []string{"internal/runtime/agent/evidence_readonly.go"},
				HasDiscoveryTargets: true,
				DiscoveryStrength:   toolfacts.DiscoveryStrengthSourceOwner,
				SourceOwnerFound:    true,
				SearchHitCount:      1,
			},
		},
	}
	answer := "- `internal/runtime/agent/runner_core.go` checks whether read-only analysis has grounded source evidence.\n"

	packet, ok := buildReadOnlyEvidencePacket("Explain runner readiness.", answer, childResults)
	if !ok {
		ginkgo.Fail("expected child result packet to be built", 1)
	}

	if packet.SchemaVersion != readOnlyEvidencePacketPayloadType {
		ginkgo.Fail(fmt.Sprintf("schema version = %q", packet.SchemaVersion), 1)
	}

	if got, want := packet.SourceBodyPaths, []string{"internal/runtime/agent/runner_core.go"}; !slices.Equal(got, want) {
		ginkgo.Fail(fmt.Sprintf("SourceBodyPaths = %#v, want %#v", got, want), 1)
	}

	if slices.Contains(packet.SourceBodyPaths, ".idiom/comprehension/views/pack.json") {
		ginkgo.Fail(fmt.Sprintf("artifact path leaked into source-body paths: %#v", packet.SourceBodyPaths), 1)
	}

	if !slices.Contains(packet.CandidatePaths, "internal/runtime/agent/evidence_readonly.go") {
		ginkgo.Fail(fmt.Sprintf("expected search candidate path, got %#v", packet.CandidatePaths), 1)
	}

	if !readOnlyEvidencePacketAnswerable(packet) {
		ginkgo.Fail(fmt.Sprintf("source-backed built packet should be answerable: %#v", packet), 1)
	}
})

var _ = ginkgo.It("BuildReadOnlyEvidencePacketKeepsFoundFindingBullets", func() {
	answer := strings.Join([]string{
		"Partial findings from this turn (the budget was reached before a full synthesis):",
		"",
		"Verified context:",
		"- Read internal/runtime/agent/implementation_flow.go lines 1-20",
		"- Found task child runner creation in internal/runtime/agent/implementation_flow.go",
	}, "\n")
	childResults := []workspace.ToolResult{
		readFileSourceWindowResult("internal/runtime/agent/implementation_flow.go", 1, 20, 0, "package agent\n"),
	}

	packet, ok := buildReadOnlyEvidencePacket("Explain how delegated child runners are created.", answer, childResults)
	if !ok {
		ginkgo.Fail("expected packet from child answer", 1)
	}

	if !readOnlyEvidencePacketAnswerableForObjective(packet, "Explain how delegated child runners are created in this harness.") {
		ginkgo.Fail(fmt.Sprintf("expected found finding bullet to support objective answerability: %#v", packet), 1)
	}
})

var _ = ginkgo.It("BuildReadOnlyEvidencePacketDropsLineOrientedSourceFindings", func() {
	answer := strings.Join([]string{
		"Grounded findings:",
		"- 1:package agent",
		"- 2:",
		"- 3:import (",
	}, "\n")
	childResults := []workspace.ToolResult{
		readFileSourceWindowResult("internal/runtime/agent/runner_core.go", 1, 20, 0, "1:package agent\n2:\n3:import (\n"),
	}

	packet, ok := buildReadOnlyEvidencePacket("Explain runner behavior.", answer, childResults)
	if !ok {
		ginkgo.Fail("expected packet to retain source-body path context", 1)
	}

	if readOnlyEvidencePacketAnswerable(packet) {
		ginkgo.Fail(fmt.Sprintf("raw line-oriented source snippets must not become answerable findings: %#v", packet), 1)
	}

	if len(packet.Findings) != 0 {
		ginkgo.Fail(fmt.Sprintf("line-oriented source snippets leaked into packet findings: %#v", packet.Findings), 1)
	}

	if got, want := packet.SourceBodyPaths, []string{"internal/runtime/agent/runner_core.go"}; !slices.Equal(got, want) {
		ginkgo.Fail(fmt.Sprintf("SourceBodyPaths = %#v, want %#v", got, want), 1)
	}
})

var _ = ginkgo.It("BuildReadOnlyEvidencePacketDropsCompactedLineSourceFindings", func() {
	answer := "Grounded findings:\n- ... 57:\tObjective string. 58:\tFindings []ReadOnlyEvidenceFinding. f3 internal/runtime/agent/evidence_readonly.go."
	childResults := []workspace.ToolResult{
		readFileSourceWindowResult(
			"internal/runtime/agent/evidence_readonly.go",
			50,
			65,
			0,
			"50:package agent\n57:\tObjective string\n58:\tFindings []ReadOnlyEvidenceFinding\n",
		),
	}

	packet, ok := buildReadOnlyEvidencePacket("What is the role of evidence in this harness?", answer, childResults)
	if !ok {
		ginkgo.Fail("expected packet to retain source-body path context", 1)
	}

	if readOnlyEvidencePacketAnswerable(packet) {
		ginkgo.Fail(fmt.Sprintf("compacted source snippets must not become answerable findings: %#v", packet), 1)
	}

	if len(packet.Findings) != 0 {
		ginkgo.Fail(fmt.Sprintf("compacted source snippets leaked into packet findings: %#v", packet.Findings), 1)
	}
})

var _ = ginkgo.It("ReadOnlyObjectiveAnswerEvidenceSupportedAcceptsRunnerReadiness", func() {
	text := "runner.go computes read-only readiness from grounded source evidence."
	objective := "Explain runner readiness."

	if readOnlyObjectiveAnswerEvidenceSupported(text, objective) {
		ginkgo.Fail("path-like source name alone should not satisfy generic objective support", 1)
	}

	if !readOnlyObjectiveAnswerEvidenceSupportedByNamedSource(text, objective) {
		ginkgo.Fail(fmt.Sprintf("expected named-source runner readiness text to support objective: terms=%#v text=%q",
			readOnlyObjectivePromotionTerms(objective), text), 1)
	}
})

var _ = ginkgo.It("ReadOnlyEvidencePacketRejectsPathPrefixWithoutObjectiveSupport", func() {
	packet := ReadOnlyEvidencePacket{
		Objective:       "Explain runner readiness.",
		Question:        "Explain runner readiness.",
		SourceBodyPaths: []string{"internal/runtime/agent/runner_core.go"},
		Findings: []ReadOnlyEvidenceFinding{
			{Path: "internal/runtime/agent/runner_core.go", Text: "The source body was inspected."},
		},
		Confidence: "medium",
	}

	if readOnlyEvidencePacketAnswerableForObjective(packet, "Explain runner readiness.") {
		ginkgo.Fail(fmt.Sprintf("path prefix and source-body presence alone must not satisfy objective answerability: %#v", packet), 1)
	}
})

var _ = ginkgo.It("ReadOnlyEvidencePacketRejectsOffObjectiveSubagentImplementationPacket", func() {
	packet := ReadOnlyEvidencePacket{
		Objective:       "Can you tell me how this harness uses subagents?",
		Question:        "Can you tell me how this harness uses subagents?",
		SourceBodyPaths: []string{"cmd/idiom/main.go"},
		Findings: []ReadOnlyEvidenceFinding{{
			Path: "cmd/idiom/main.go",
			Text: "The entrypoint imports rootcmd, calls RunMain, and defines version metadata.",
		}},
		Confidence: "medium",
	}

	if readOnlyEvidencePacketAnswerableForObjective(packet, "Can you tell me how this harness uses subagents?") {
		ginkgo.Fail(fmt.Sprintf("entrypoint-only packet must not satisfy subagent implementation-shape objective: %#v", packet), 1)
	}
})

var _ = ginkgo.It("ToolResultEvidenceProjectionUsesTypedPacketAsAnswerabilityAuthority", func() {
	result := workspace.ToolResult{
		Tool:    "task",
		Summary: "Child task completed",
		Output: strings.Join([]string{
			"Source-body evidence paths:",
			"- `internal/runtime/agent/runner_core.go`",
			"",
			"Key findings:",
			"- The harness uses subagents through delegated child runners.",
		}, "\n"),
	}

	result = readOnlyEvidencePacketToolResult(result, ReadOnlyEvidencePacket{
		SchemaVersion:   readOnlyEvidencePacketPayloadType,
		Objective:       "Explain how subagents work.",
		Question:        "Explain how subagents work.",
		SourceBodyPaths: []string{"internal/runtime/agent/runner_core.go"},
		Findings: []ReadOnlyEvidenceFinding{
			{Path: "internal/runtime/agent/runner_core.go", Text: "The runner records model-call metadata."},
		},
		Confidence: "medium",
	})

	projection := newToolResultEvidenceProjection([]workspace.ToolResult{result})
	if projection.hasAnswerableDelegatedPacket(workerruntime.WorkerRuntimeBrief{Objective: "Explain how subagents work."}) {
		ginkgo.Fail("typed packet rejection must not fall back to legacy rendered text promotion", 1)
	}

	if paths := projection.postChildClosureEvidencePaths("Explain how subagents work.", 4); len(paths) != 0 {
		ginkgo.Fail(fmt.Sprintf("non-answerable typed packet must not promote closure evidence paths: %#v", paths), 1)
	}
})

var _ = ginkgo.DescribeTable("Read-only named-path prefetch guards",
	func(
		mutate func(*runExecution),
		collectedResults []workspace.ToolResult,
		envDisabled bool,
		wantEligible bool,
		wantMissReason string,
	) {
		t := ginkgo.GinkgoT()
		root := t.TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "README.md"), []byte("prefetch fixture\n"), 0o644)).To(gomega.Succeed())

		runner := newRunnerRunFixture(&Runner{
			Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        string(autonomy.TaskFamilyArchitecture),
				Contract:          string(workerruntime.ContractArchitecture),
				Objective:         "Read README.md and summarize it.",
				PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
			},
		})
		if mutate != nil {
			mutate(runner)
		}
		if envDisabled {
			t.Setenv("IDIOM_DISABLE_READONLY_PREFETCH", "1")
		}

		event := runner.readOnlyNamedPathPrefetchEvent(collectedResults)
		gomega.Expect(event.Eligible).To(gomega.Equal(wantEligible))
		gomega.Expect(event.MissReason).To(gomega.Equal(wantMissReason))
	},
	ginkgo.Entry("allows an eligible top-level read-only run", nil, nil, false, true, ""),
	ginkgo.Entry("rejects a delegated child", func(r *runExecution) {
		r.runner.AgentName = "child:explorer"
	}, nil, false, false, "delegated_child_agent"),
	ginkgo.Entry("honors the environment opt-out", nil, nil, true, false, "disabled_by_env"),
	ginkgo.Entry("does not prefetch after evidence exists", nil, []workspace.ToolResult{
		{Tool: workspace.ToolNameReadFile, Summary: "Read README.md"},
	}, false, false, "existing_evidence_present"),
	ginkgo.Entry("requires read-only permission", func(r *runExecution) {
		r.state.runtimeBrief.PermissionProfile = string(workerruntime.PermissionGuardedWorkspaceWrite)
	}, nil, false, false, "not_read_only_workspace"),
)

var _ = ginkgo.It("RunnerPrefetchSendsEvidenceToModelBeforeFollowup", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "labels"), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir fixture: %v", err), 1)
	}

	source := "package labels\n\nfunc ShortLabel() string {\n\treturn \"Save changes\"\n}\n"
	if err := os.WriteFile(filepath.Join(root, "internal", "labels", "labels.go"), []byte(source), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write fixture: %v", err), 1)
	}

	requests := 0
	sawPrefetchEvidence := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()
		requests++

		body, err := io.ReadAll(r.Body)
		if err != nil {
			ginkgo.Fail(fmt.Sprintf("read request body: %v", err), 1)
		}

		if strings.Contains(string(body), "internal/labels/labels.go") &&
			strings.Contains(string(body), "Save changes") {

			sawPrefetchEvidence = true
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{
					"content": "Yes. `internal/labels/labels.go` defines `ShortLabel`, and its current label is \"Save changes\". Source: internal/labels/labels.go.",
				},
			}},
		})
	}))

	defer server.Close()

	baseRunner := &Runner{
		Client:    llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(autonomy.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			Objective:         "Can we shorten the save label in internal/labels/labels.go? Answer the question from the codebase. Do not edit files.",
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
			AllowedTools:      []string{"read_file", "code_navigate", "search_content"},
		},
	}

	var events []RunEvent

	baseRunner.OnEvent = func(event RunEvent) {
		events = append(events, event)
	}

	answer, err := baseRunner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: baseRunner.RuntimeBrief.Objective}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("Run: %v", err), 1)
	}

	if requests == 0 {
		ginkgo.Fail("expected model call with prefetched evidence", 1)
	}

	if !sawPrefetchEvidence {
		ginkgo.Fail("expected at least one model request to include prefetched source evidence", 1)
	}

	if !strings.Contains(answer, "Save changes") || !strings.Contains(answer, "internal/labels/labels.go") {
		ginkgo.Fail(fmt.Sprintf("expected grounded prefetch-backed answer, got:\n%s", answer), 1)
	}

	for _, event := range events {
		if event.Role == "tool_result" && strings.Contains(event.Content, "code_navigate") {
			ginkgo.Fail(fmt.Sprintf("prefetch-backed answer should not execute model retry, saw event %#v", event), 1)
		}
	}
})

var _ = ginkgo.It("ReadOnlyNamedPathPrefetchNoPathMissIsReported", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	var events []RunEvent
	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	manager.SetPermissionRules(map[string][]toolpolicy.Rule{
		workspace.ToolNameReadFile: {{Pattern: "**", Decision: toolpolicy.Allow}},
	})
	runner := newRunnerRunFixture(&Runner{
		Workspace: manager,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(autonomy.TaskFamilyArchitecture),
			Contract:          string(workerruntime.ContractArchitecture),
			Objective:         "Summarize the relevant behavior.",
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
		},
		OnEvent: func(event RunEvent) {
			if event.Role == readOnlyPrefetchEventRole {
				events = append(events, event)
			}
		},
	})
	recorder := runnerTurnRecorder{
		runner: runner,
	}

	got := runner.maybeApplyReadOnlyNamedPathPrefetch(recorder, runner.state.collectedResults)
	if len(got) != 0 {
		ginkgo.Fail(fmt.Sprintf("expected no prefetched results, got %#v", got), 1)
	}

	if len(events) != 1 {
		ginkgo.Fail(fmt.Sprintf("expected one prefetch event, got %#v", events), 1)
	}

	var payload readOnlyPrefetchEvent

	if err := json.Unmarshal([]byte(events[0].Content), &payload); err != nil {
		ginkgo.Fail(fmt.Sprintf("unmarshal event: %v", err), 1)
	}

	if payload.Eligible || payload.MissReason != "no_explicit_named_paths" {
		ginkgo.Fail(fmt.Sprintf("unexpected no-path event: %#v", payload), 1)
	}
})

var _ = ginkgo.It("MissingRequiredEvidenceUsesTypedRegistryAndSkipsSemanticLabels", func() {
	missing := missingRequiredEvidence([]string{
		" " + string(workerruntime.RequiredEvidenceVerificationStatus) + " ",
		string(workerruntime.RequiredEvidenceConfidence),
	}, newFinalEvidenceProjection("", nil, implementationCompletionState{}))
	if !slices.Equal(missing, []string{string(workerruntime.RequiredEvidenceVerificationStatus)}) {
		ginkgo.Fail(fmt.Sprintf("missing evidence = %#v, want only deterministic verification status", missing), 1)
	}

	missing = missingRequiredEvidence([]string{
		string(workerruntime.RequiredEvidenceVerificationStatus),
	}, newFinalEvidenceProjection("Verification: passed", []workspace.ToolResult{shellCommandResultForTest("go test ./...", "ok", "")}, implementationCompletionState{}))
	if len(missing) != 0 {
		ginkgo.Fail(fmt.Sprintf("verification tool result should satisfy verification evidence, got %#v", missing), 1)
	}
})

var _ = ginkgo.It("MissingRequiredEvidenceDoesNotRequireHistoryWithoutExistingSourceEvidence", func() {
	for _, path := range []string{"internal/labels/generated.go", "temp.md"} {
		_, result := committedMutationResultForTest(path)
		missing := missingRequiredEvidence(
			[]string{string(workerruntime.RequiredEvidenceGitHistoryRationale)},
			newFinalEvidenceProjection("Implemented the requested file.\n\nVerification: not run.", []workspace.ToolResult{result}, implementationCompletionState{}),
		)
		if len(missing) != 0 {
			ginkgo.Fail(fmt.Sprintf("new source or non-source deliverable without prior source-body evidence should not require git history, result=%#v missing=%#v", result, missing), 1)
		}
	}
})

var _ = ginkgo.It("MissingRequiredEvidenceRequiresHistoryForExistingSourceWrites", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	path := "internal/labels/labels.go"
	if err := writeTestFile(root, path, "package labels\n\nconst Label = \"old\"\n"); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed existing source: %v", err), 1)
	}
	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true, MaxOutputBytes: 4 << 20})
	source := manager.Execute(context.Background(), workspace.ToolRequest{
		Tool: workspace.ToolNameReadFile, Path: path, StartLine: 1, EndLine: 3,
	})
	write := manager.Execute(context.Background(), workspace.ToolRequest{
		Tool: workspace.ToolNameEditFile, Path: path,
		OldText: "const Label = \"old\"", NewText: "const Label = \"new\"",
		Description: "update the existing label fixture",
	})
	if source.Error != "" || len(source.SourceWindows()) != 1 || write.Error != "" || write.CommitReceipt() == nil {
		ginkgo.Fail(fmt.Sprintf("produce existing-source receipt fixture: source=%#v write=%#v", source, write), 1)
	}
	missing := missingRequiredEvidence(
		[]string{string(workerruntime.RequiredEvidenceGitHistoryRationale)},
		newFinalEvidenceProjection("Implemented the source change.\n\nChanged paths:\n- internal/labels/labels.go\n\nVerification: not run.", []workspace.ToolResult{source, write}, implementationCompletionState{}),
	)
	if !slices.Equal(missing, []string{string(workerruntime.RequiredEvidenceGitHistoryRationale)}) {
		ginkgo.Fail(fmt.Sprintf("existing source write should still require history evidence, got %#v", missing), 1)
	}

	wrongPathHistory := missingRequiredEvidence(
		[]string{string(workerruntime.RequiredEvidenceGitHistoryRationale)},
		newFinalEvidenceProjection("Implemented the source change.\n\nChanged paths:\n- internal/labels/labels.go\n\nVerification: not run.", []workspace.ToolResult{source, write, gitHistoryRangeResult("internal/labels/other.go")}, implementationCompletionState{}),
	)
	if !slices.Equal(wrongPathHistory, []string{string(workerruntime.RequiredEvidenceGitHistoryRationale)}) {
		ginkgo.Fail(fmt.Sprintf("history for another path must not satisfy changed source history evidence, got %#v", wrongPathHistory), 1)
	}

	rightPathHistory := missingRequiredEvidence(
		[]string{string(workerruntime.RequiredEvidenceGitHistoryRationale)},
		newFinalEvidenceProjection("Implemented the source change.\n\nChanged paths:\n- internal/labels/labels.go\n\nVerification: not run.", []workspace.ToolResult{source, write, gitHistoryRangeResult("internal/labels/labels.go")}, implementationCompletionState{}),
	)
	if len(rightPathHistory) != 0 {
		ginkgo.Fail(fmt.Sprintf("path-scoped history should satisfy changed source history evidence, got %#v", rightPathHistory), 1)
	}
})

var _ = ginkgo.It("SourceEvidenceIgnoresGeneratedArtifacts", func() {
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 .idiom/comprehension/views/context.json lines 1-20 (total lines: 20)",
		Output:  "1:{\"candidate_files\":[{\"path\":\"internal/runtime/agent/runner_core.go\"}]}\n",
	}}
	if newToolResultEvidenceProjection(results).HasSourceWindow(".idiom/comprehension/views/context.json") {
		ginkgo.Fail("generated context artifacts must not satisfy source-body evidence", 1)
	}

	if paths := newToolResultEvidenceProjection(results).SourceWindowPaths(); len(paths) != 0 {
		ginkgo.Fail(fmt.Sprintf("generated context artifacts must not appear in source-body path list: %#v", paths), 1)
	}
})

var _ = ginkgo.It("SourceEvidenceIgnoresCandidateAnchorMiss", func() {
	results := []workspace.ToolResult{{
		Tool:    "go_read_symbol",
		Summary: `Go symbol read path-filter miss; found 1 candidate anchor(s) for "Model.Update"`,
		Output: strings.Join([]string{
			"requested_symbol: Model.Update",
			"requested_path: internal/surfaces/tui/app/model_state.go",
			"path_filter_status: no source-body match in requested path",
			"candidate_anchor_status: anchors only; read one candidate path to inspect source body",
			"candidate_anchors:",
			"- internal/surfaces/tui/app/lifecycle.go:409 method Model.Update",
		}, "\n"),
	}}

	if newToolResultEvidenceProjection(results).HasSourceWindow("internal/surfaces/tui/app/lifecycle.go") {
		ginkgo.Fail("candidate anchor must not count as source-body evidence for the candidate path", 1)
	}

	if paths := newToolResultEvidenceProjection(results).SourceWindowPaths(); len(paths) != 0 {
		ginkgo.Fail(fmt.Sprintf("candidate anchor produced source-body paths: %#v", paths), 1)
	}
})

var _ = ginkgo.It("SourceEvidenceNonBodySignalsDoNotCloseSourceBodyObligations", func() {
	cases := []struct {
		name   string
		result workspace.ToolResult
	}{
		{
			name: "search hit",
			result: workspace.ToolResult{
				Tool:    "search_content",
				Summary: "Found 1 matching line(s) in internal/runtime/agent/runner_core.go.",
				Output:  "internal/runtime/agent/runner_core.go:88: func (r *Runner) Run(...)",
			},
		},
		{
			name: "context packet",
			result: workspace.ToolResult{
				Tool:    "context_pack",
				Summary: "Context pack includes internal/runtime/agent/runner_core.go.",
				Facts:   contextPackCandidateFactsForTest("internal/runtime/agent/runner_core.go"),
			},
		},
		{
			name: "diagnostic",
			result: workspace.ToolResult{
				Tool:    "source_sanity",
				Summary: "source_sanity failed for internal/runtime/agent/runner_core.go",
				Output:  "internal/runtime/agent/runner_core.go:88: undefined: runState",
			},
		},
		{
			name:   "log summary",
			result: readFileSourceWindowResult("logs/conversations/session.jsonl", 1, 20, 20, `{"message":"model mentioned internal/runtime/agent/runner_core.go"}`),
		},
		{
			name: "plain mentioned path",
			result: workspace.ToolResult{
				Tool:    "task",
				Summary: "Child task completed",
				Output:  "Mentioned paths: internal/runtime/agent/runner_core.go",
			},
		},
	}

	for _, tc := range cases {
		ginkgo.By(tc.name, func() {
			results := []workspace.ToolResult{tc.result}
			if newToolResultEvidenceProjection(results).HasSourceWindow("internal/runtime/agent/runner_core.go") {
				ginkgo.Fail(fmt.Sprintf("%s must not satisfy source-body evidence", tc.name), 1)
			}

			if paths := newToolResultEvidenceProjection(results).SourceWindowPaths(); len(paths) != 0 {
				ginkgo.Fail(fmt.Sprintf("%s produced source-body paths: %#v", tc.name, paths), 1)
			}
		})
	}
})

var _ = ginkgo.It("SourceEvidenceCandidatePathsUseContextPackCandidates", func() {
	results := []workspace.ToolResult{{
		Tool:    workspace.ToolNameContextPack,
		Summary: "Context pack includes evidence_readonly.go.",
		Facts:   contextPackCandidateFactsForTest("evidence_readonly.go"),
	}}

	paths := newToolResultEvidenceProjection(results).CandidatePaths()
	if len(paths) != 1 || paths[0] != "evidence_readonly.go" {
		ginkgo.Fail(fmt.Sprintf("candidate paths = %#v, want context_pack candidate", paths), 1)
	}
})

var _ = ginkgo.It("TurnPhaseContractRuntimeProjectionCarriesContractAndObligations", func() {
	phase := TurnPhaseContract{
		ContractID:   " contract-1 ",
		ModelCallID:  " call-1 ",
		PhaseID:      "phase.required_source",
		InvariantIDs: []string{"completion.readiness", "completion.readiness", "tool.surface"},
		TypedRoute: TypedRouteContract{
			EditSetObligations: []string{"obl-edit", "obl-edit"},
		},
		EvidenceState: PhaseEvidenceState{
			SourceBodyObligations:    []string{"obl-source"},
			TDDObligationRefs:        []string{"obl-tdd"},
			VerificationEvidenceRefs: []string{"obl-verify"},
		},
	}

	projection := phase.RuntimeProjection()

	if projection.ContractID != "contract-1" || projection.ProtocolHash != "call-1" || projection.PhaseID != "phase.required_source" {
		ginkgo.Fail(fmt.Sprintf("unexpected projection identity: %#v", projection), 1)
	}
	if got, want := projection.InvariantIDs, []string{"completion.readiness", "tool.surface"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		ginkgo.Fail(fmt.Sprintf("invariant ids = %#v, want %#v", got, want), 1)
	}
	if got, want := projection.ObligationIDs, []string{"obl-edit", "obl-source", "obl-tdd", "obl-verify"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		ginkgo.Fail(fmt.Sprintf("obligation ids = %#v, want %#v", got, want), 1)
	}
})

var _ = ginkgo.It("PhaseActionVocabularyUsesAutonomyConstants", func() {
	want := map[string]autonomy.ActionClass{
		actionRead:             autonomy.ActionClassRead,
		actionSearch:           autonomy.ActionClassSearch,
		actionSubmitPlan:       autonomy.ActionClassSubmitPlan,
		actionPerEditWrite:     autonomy.ActionClassPerEditWrite,
		actionDeliverableWrite: autonomy.ActionClassDeliverableWrite,
		actionSourceSatisfied:  autonomy.ActionClassSourceSatisfied,
		actionBlocker:          autonomy.ActionClassBlocker,
		actionVerify:           autonomy.ActionClassVerify,
		actionFinalAnswer:      autonomy.ActionClassFinalAnswer,
		actionReviewPass:       autonomy.ActionClassReviewPass,
		actionGenericProbe:     autonomy.ActionClassGenericProbe,
		actionProseFinal:       autonomy.ActionClassProseFinal,
	}
	for got, class := range want {
		if got != autonomy.ActionClassToken(class) {
			ginkgo.Fail(fmt.Sprintf("phase action alias %q does not match autonomy class %q", got, class), 1)
		}
	}
})

var _ = ginkgo.It("GenericAllowedActionClassesUsesWorkspaceToolProjection", func() {
	t := ginkgo.GinkgoT()
	classes := genericAllowedActionClasses([]string{"history_range", "history_symbol", "context_status", "apply_patch"})

	assertContains(t, classes, actionRead)
	assertContains(t, classes, actionSearch)
	assertContains(t, classes, actionPerEditWrite)
	assertContains(t, classes, actionFinalAnswer)
	assertContains(t, classes, actionBlocker)
	assertNotContains(t, classes, "history")
})

var _ = ginkgo.It("DeriveTurnPhaseContractPrunesToolsForInvalidActionClasses", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(autonomy.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file", "source_sanity"},
			Constraints:       []string{"answer_readiness_reached"},
		},
	})

	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionVerify)
	assertNotContains(t, contract.AllowedTools, "source_sanity")
	assertNotContains(t, contract.ActionPolicy.CallableToolNames, "source_sanity")
	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.ActionPolicy.CallableToolNames, "read_file")
})

var _ = ginkgo.It("DeriveTurnPhaseContractKeepsDiagnosticToolsInGeneralReadOnlyLane", func() {
	t := ginkgo.GinkgoT()
	diagnosticTools := generalReadOnlyDiagnosticToolRegressionSet()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:         string(autonomy.TaskFamilyDirectAnswer),
			Contract:           string(workerruntime.ContractDirectAnswer),
			PermissionProfile:  "read_only_workspace",
			AllowedTools:       append([]string{"read_file", "search_content"}, diagnosticTools...),
			Objective:          "Investigate the failure from the previous turn.",
			ReadOnlyConstraint: true,
		},
	})

	if contract.Phase != "read_only_evidence_pending" {
		ginkgo.Fail(fmt.Sprintf("Phase = %q, want read_only_evidence_pending", contract.Phase), 1)
	}

	for _, tool := range diagnosticTools {
		assertContains(t, contract.AllowedTools, tool)
		assertContains(t, contract.ActionPolicy.CallableToolNames, tool)
		assertNotContains(t, contract.HiddenTools, tool)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
})

var _ = ginkgo.It("DirectAnswerSurfaceDiagnosticToolsSurviveTurnPhaseProjection", func() {
	t := ginkgo.GinkgoT()
	brief := testDirectAnswerSurfaceBrief()
	surface, ok := DirectAnswerSurfaceForBrief(brief)
	if !ok {
		ginkgo.Fail("expected direct-answer surface", 1)
	}

	brief.AllowedTools = surface.ToolNames
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{Brief: brief})

	for _, tool := range generalReadOnlyDiagnosticToolRegressionSet() {
		assertContains(t, surface.ToolNames, tool)
		assertContains(t, contract.AllowedTools, tool)
		assertContains(t, contract.ActionPolicy.CallableToolNames, tool)
		assertNotContains(t, contract.HiddenTools, tool)
	}

	for _, tool := range []string{"submit_plan", "task_ledger", "note", "shell", "edit_file", "write_file", "apply_patch", "context_pack", "context_impact", "discovery_query"} {
		assertNotContains(t, surface.ToolNames, tool)
		assertNotContains(t, contract.AllowedTools, tool)
		assertNotContains(t, contract.ActionPolicy.CallableToolNames, tool)
	}
})

var _ = ginkgo.It("DirectAnswerModelProtocolProjectionCarriesActiveReadOnlyAuthority", func() {
	t := ginkgo.GinkgoT()
	brief := testDirectAnswerSurfaceBrief()
	brief.Objective = "Explain why source grounding rejected the final answer."
	brief.ReadOnlyConstraint = true
	brief.AllowedTools = []string{"read_file", "code_navigate", "context_query"}
	externalDocument := filepath.Join(t.TempDir(), "reference.md")

	results := []workspace.ToolResult{
		readFileSourceWindowResult("README.md", 1, 8, 80, "1:# Idiom\n2:Local coding-agent harness.\n"),
		readFileSourceWindowResult("docs/architecture.md", 1, 8, 80, "1:# Architecture\n2:Source evidence is typed.\n"),
		{
			Tool:    "context_query",
			Summary: "Found candidate source owner",
			Output:  "internal/repository/comprehension/search.go",
			Facts:   contextPackCandidateFactsForTest("internal/repository/comprehension/search.go"),
		},
	}
	phase := deriveTurnPhaseContract(TurnPhaseContractInput{
		ContractID:       "contract-direct",
		ModelCallID:      "contract-direct:model_call_1",
		Brief:            brief,
		Results:          results,
		BaseAllowedTools: brief.AllowedTools,
	})
	phase.EvidenceState.SourceBodyObligations = []string{"README.md", externalDocument, "internal/runtime/agent/evidence_readonly.go"}

	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
	protocol, ok := runner.compileModelProtocolProjection(brief, results, phase, RunPolicy{MaxToolSteps: 3}, true)
	if !ok {
		ginkgo.Fail("expected direct-answer model protocol projection", 1)
	}

	if protocol.ProtocolHash == "" || protocol.CompactPacket.ProtocolHash != protocol.ProtocolHash {
		ginkgo.Fail(fmt.Sprintf("direct-answer protocol identity is incomplete: %#v", protocol), 1)
	}
	if protocol.ContractID != phase.ContractID || protocol.PhaseID != string(phase.PhaseID) {
		ginkgo.Fail(fmt.Sprintf("direct-answer protocol identity = %#v, phase = %#v", protocol, phase), 1)
	}
	assertEqualSet(t, protocol.VisibleTools, phase.AllowedTools)
	assertEqualSet(t, protocol.CallableTools, phase.ActionPolicy.CallableToolNames)
	assertEqualSet(t, protocol.CallableActionClasses, actionClassesAllowedByPolicy(phase.ActionPolicy.AllowedActionClasses, phase.ActionPolicy.InvalidActionClasses))
	assertEqualSet(t, protocol.InvalidActionClasses, phase.ActionPolicy.InvalidActionClasses)
	assertEqualSet(t, protocol.TerminalResponseShapes, phase.ActionPolicy.TerminalResponseShapes)
	assertContains(t, protocol.GroundedSourceBodyPaths, "README.md")
	assertContains(t, protocol.GroundedSourceBodyPaths, "docs/architecture.md")
	assertNotContains(t, protocol.GroundedSourceBodyPaths, externalDocument)
	assertContains(t, protocol.CandidateOnlyPaths, "internal/repository/comprehension/search.go")
	assertContains(t, protocol.SourceBodyObligations, "internal/runtime/agent/evidence_readonly.go")
	assertContains(t, protocol.SourceBodyObligations, externalDocument)
	assertNotContains(t, protocol.SourceBodyObligations, "README.md")
	if protocol.GroundedSourceBodyPathTotal != 2 || protocol.GroundedSourceBodyPathOmitted != 0 ||
		protocol.CandidateOnlyPathTotal != 1 || protocol.CandidateOnlyPathOmitted != 0 ||
		protocol.SourceBodyObligationTotal != 2 || protocol.SourceBodyObligationOmitted != 0 {

		ginkgo.Fail(fmt.Sprintf("unexpected evidence sample counts: %#v", protocol), 1)
	}
	eventPayload := modelProtocolEventPayload(protocol)
	assertContains(t, eventPayload.GroundedSourceBodyPaths, "README.md")
	assertNotContains(t, eventPayload.GroundedSourceBodyPaths, externalDocument)
	assertContains(t, eventPayload.CandidateOnlyPaths, "internal/repository/comprehension/search.go")
	if eventPayload.GroundedSourceBodyPathTotal != protocol.GroundedSourceBodyPathTotal ||
		eventPayload.SourceBodyObligationTotal != protocol.SourceBodyObligationTotal {
		ginkgo.Fail(fmt.Sprintf("model protocol event dropped evidence counts: payload=%#v protocol=%#v", eventPayload, protocol), 1)
	}
	assertNotContains(t, protocol.SchemaNames, "submit_plan")
	assertNotContains(t, protocol.CallableTools, "submit_plan")
	assertNotContains(t, protocol.CallableActionClasses, actionSubmitPlan)
	assertNotContains(t, protocol.TerminalResponseShapes, "submit_plan")
})

var _ = ginkgo.It("ModelProtocolEvidencePathSamplingPreservesRootAndExternalDocuments", func() {
	t := ginkgo.GinkgoT()
	externalDocument := filepath.Join(t.TempDir(), "external.md")
	paths := []string{"README.md", externalDocument}
	for index := 0; index < 12; index++ {
		paths = append(paths, fmt.Sprintf("internal/source_%02d.go", index))
	}

	sample := sampleModelProtocolEvidencePaths(paths, modelProtocolEvidencePathLimit)
	if sample.Total != 14 || sample.Omitted != 2 || len(sample.Paths) != modelProtocolEvidencePathLimit {
		ginkgo.Fail(fmt.Sprintf("unexpected bounded evidence sample: %#v", sample), 1)
	}
	assertContains(t, sample.Paths, "README.md")
	assertContains(t, sample.Paths, externalDocument)
})

var _ = ginkgo.It("DeriveTurnPhaseContractPlanExecuteRequiredSource", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "submit_plan", "edit_file"},
			RequiredEditPaths: []string{"a.go", "b.go"},
		},
		Results: []workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read f1 a.go lines 1-10 (total lines: 10)",
		}},
	})

	if contract.PhaseID != contractcompile.TurnPhaseRequiredSource {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, contractcompile.TurnPhaseRequiredSource), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSourceSatisfied)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionSubmitPlan)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionPerEditWrite)
	assertEqualSet(t, contract.AllowedTools, []string{"read_file", "code_navigate"})
	assertNotContains(t, contract.ActionPolicy.CallableToolNames, "submit_plan")
})

var _ = ginkgo.It("PlanExecutePlanningStateRequiredSourceProjectionSplitsParserVisibleSubmit", func() {
	t := ginkgo.GinkgoT()
	state := derivePlanExecutePlanningState(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go", "b.go"},
	}, []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
	}, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})

	if state.Mode != PlanExecutePlanningModeRequiredSource {
		ginkgo.Fail(fmt.Sprintf("Mode = %q, want %q", state.Mode, PlanExecutePlanningModeRequiredSource), 1)
	}

	assertNotContains(t, state.ParserVisibleTools, "submit_plan")
	assertNotContains(t, state.CallableTools, "submit_plan")
	assertNotContains(t, state.AllowedActionClasses, actionSubmitPlan)
	assertContains(t, state.InvalidActionClasses, actionSubmitPlan)
})

var _ = ginkgo.It("DeriveTurnPhaseContractPlanExecuteCandidateSourcePending", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools: []string{
				"context_pack", "read_file", "code_navigate", "product_efforts", "product_status",
				"submit_plan", "edit_file", "shell",
			},
		},
	})

	if contract.PhaseID != contractcompile.TurnPhaseCandidateSource {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, contractcompile.TurnPhaseCandidateSource), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSourceSatisfied)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionSubmitPlan)
	assertEqualSet(t, contract.AllowedTools, []string{"context_pack", "read_file", "code_navigate", "product_efforts", "product_status"})
	assertNotContains(t, contract.ActionPolicy.CallableToolNames, "submit_plan")
})

var _ = ginkgo.It("PlanExecutePlanningStateCandidateProjectionRequiresSelectedPathForSubmit", func() {
	t := ginkgo.GinkgoT()
	state := derivePlanExecutePlanningState(workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}, nil, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})

	if state.Mode != PlanExecutePlanningModeAllowedCandidate {
		ginkgo.Fail(fmt.Sprintf("Mode = %q, want %q", state.Mode, PlanExecutePlanningModeAllowedCandidate), 1)
	}

	assertNotContains(t, state.ParserVisibleTools, "submit_plan")
	assertNotContains(t, state.CallableTools, "submit_plan")
	assertNotContains(t, state.AllowedActionClasses, actionSubmitPlan)
	assertContains(t, state.InvalidActionClasses, actionSubmitPlan)
})

var _ = ginkgo.It("PlanExecutePlanningStateCandidateProjectionAllowsSubmitWithSelectedPath", func() {
	t := ginkgo.GinkgoT()
	state := derivePlanExecutePlanningState(workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"a.go", "b.go"},
	}, []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
	}, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})

	if state.Mode != PlanExecutePlanningModeSubmitReady {
		ginkgo.Fail(fmt.Sprintf("Mode = %q, want %q", state.Mode, PlanExecutePlanningModeSubmitReady), 1)
	}

	assertContains(t, state.SelectedEditPaths, "a.go")
	assertContains(t, state.ParserVisibleTools, "submit_plan")
	assertContains(t, state.CallableTools, "submit_plan")
	assertContains(t, state.AllowedActionClasses, actionSubmitPlan)
	assertNotContains(t, state.InvalidActionClasses, actionSubmitPlan)
	assertContains(t, state.TerminalResponseShapes, "submit_plan")
})

var _ = ginkgo.It("DeriveTurnPhaseContractPlanExecuteOpenCandidateSourceRequiresConcreteSelectedPathBeforeSubmit", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"context_pack", "read_file", "search_content", "submit_plan"},
		},
		Results: []workspace.ToolResult{
			readFileSourceWindowResult(
				"internal/capabilities/workspace/product_tools.go",
				1,
				114,
				114,
				"func (m *Manager) productEfforts(req ToolRequest) ToolResult { return ToolResult{} }",
			),
		},
	})

	if contract.PhaseID != contractcompile.TurnPhaseCandidateSource {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, contractcompile.TurnPhaseCandidateSource), 1)
	}

	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "search_content")
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionSubmitPlan)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
})

var _ = ginkgo.It("DeriveTurnPhaseContractPlanExecuteTerminalRecoveryHidesReadActions", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"submit_plan"},
		},
		Results: []workspace.ToolResult{
			readFileSourceWindowResult(
				"internal/workflows/productworkflow/coverage_gate.go",
				1,
				125,
				391,
				"func EvaluateCoverageGate() {}",
			),
		},
	})

	if contract.PhaseID != contractcompile.TurnPhaseCandidateSource {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, contractcompile.TurnPhaseCandidateSource), 1)
	}

	if len(contract.AllowedTools) != 0 || len(contract.ActionPolicy.CallableToolNames) != 0 {
		ginkgo.Fail(fmt.Sprintf("candidate source without selected edit paths must not advertise submit_plan as callable, got allowed=%#v callable=%#v", contract.AllowedTools, contract.ActionPolicy.CallableToolNames), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionSubmitPlan)
})

var _ = ginkgo.It("DeriveTurnPhaseContractSubmitReadyTextual", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "submit_plan"},
			RequiredEditPaths: []string{"a.go"},
		},
		Results: []workspace.ToolResult{
			readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
		},
		TextualToolParser: true,
	})

	if contract.PhaseID != contractcompile.TurnPhaseSubmitReadyTextual {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, contractcompile.TurnPhaseSubmitReadyTextual), 1)
	}

	if len(contract.AllowedTools) != 0 {
		ginkgo.Fail(fmt.Sprintf("textual submit-ready must not advertise manifest-visible tools, got %#v", contract.AllowedTools), 1)
	}

	if len(contract.ActionPolicy.CallableToolNames) != 0 {
		ginkgo.Fail(fmt.Sprintf("textual submit-ready must not advertise callable tool names, got %#v", contract.ActionPolicy.CallableToolNames), 1)
	}

	assertEqualSet(t, contract.RuntimeCall.ExpressibleActionClasses, []string{actionSubmitPlan, actionSourceSatisfied, actionBlocker})
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSourceSatisfied)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionBlocker)

	if contract.RuntimeCall.ResponseFormat != "json" {
		ginkgo.Fail(fmt.Sprintf("ResponseFormat = %q, want json", contract.RuntimeCall.ResponseFormat), 1)
	}
})

var _ = ginkgo.It("PlanExecutePlanningStateSubmitReadyProjectionMatchesTransport", func() {
	t := ginkgo.GinkgoT()
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go"},
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "old\n"),
	}

	textual := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true, ResponseFormatJSON: true}, planThenExecutePathAuthority{})
	if textual.Mode != PlanExecutePlanningModeSubmitReady {
		ginkgo.Fail(fmt.Sprintf("textual Mode = %q, want %q", textual.Mode, PlanExecutePlanningModeSubmitReady), 1)
	}

	assertEqualSet(t, textual.ParserVisibleTools, []string{"submit_plan"})
	assertEqualSet(t, textual.CallableTools, []string{"submit_plan"})
	assertEqualSet(t, textual.AllowedActionClasses, []string{actionSubmitPlan, actionSourceSatisfied, actionBlocker})
	assertContains(t, textual.InvalidActionClasses, actionRead)
	assertEqualSet(t, textual.TerminalResponseShapes, []string{"submit_plan", "Decision: source-satisfied", "<<QUESTION>>:"})

	native := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{SupportsNativeTools: true}, planThenExecutePathAuthority{})
	if native.Mode != PlanExecutePlanningModeSubmitReady {
		ginkgo.Fail(fmt.Sprintf("native Mode = %q, want %q", native.Mode, PlanExecutePlanningModeSubmitReady), 1)
	}

	assertEqualSet(t, native.ParserVisibleTools, []string{"submit_plan"})
	assertEqualSet(t, native.CallableTools, []string{"submit_plan"})
	assertEqualSet(t, native.AllowedActionClasses, []string{actionSubmitPlan})
	assertContains(t, native.InvalidActionClasses, actionRead)
	assertEqualSet(t, native.TerminalResponseShapes, []string{"submit_plan"})
})

var _ = ginkgo.It("PlanExecutePlanningStateCanonicalAuthorizedNewFileIsSubmitReadyWithoutRead", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Objective:         "Implement docs/plan.md.",
		RequiredEditPaths: []string{"internal/newpkg/decision.go"},
	}
	scope := workspace.CanonicalMutationScopeProjection{
		RequiredCoverageIDs: []string{"internal/newpkg/decision.go"},
		Rules: []workspace.CanonicalMutationScopeRuleProjection{{
			Kind: workspace.MutationScopeExactFile, Source: workspace.MutationScopeAuthorizedNewFile, CanonicalID: "internal/newpkg/decision.go",
		}},
		Presentations: []workspace.CanonicalMutationScopePresentationProjection{{
			PresentedPath: "internal/newpkg/decision.go", CanonicalID: "internal/newpkg/decision.go", Source: workspace.MutationScopeAuthorizedNewFile,
		}},
	}
	authority := planThenExecutePathAuthority{scope: &scope}

	state := derivePlanExecutePlanningState(brief, nil, PlanExecutePlanningTransport{TextualToolParser: true}, authority)
	if state.Mode != PlanExecutePlanningModeSubmitReady {
		ginkgo.Fail(fmt.Sprintf("Mode = %q, want %q; state=%#v", state.Mode, PlanExecutePlanningModeSubmitReady, state), 1)
	}

	if unread := planThenExecuteUnreadRequiredPaths(brief, nil, authority); len(unread) != 0 {
		ginkgo.Fail(fmt.Sprintf("typed authorized new file must not require source-body read, got %#v", unread), 1)
	}
})

var _ = ginkgo.It("PlanExecutePlanningStateRejectsProseAuthorizedNewFileAuthority", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Objective: strings.Join([]string{
			"Implement docs/plan.md.",
			"",
			"Typed plan execution contract:",
			`{"authorized_new_files":[{"path":"internal/newpkg/decision.go"}]}`,
			"Typed plan authority projection:",
			`{"atom_id":"atom-1"}`,
		}, "\n"),
		RequiredEditPaths: []string{"internal/newpkg/decision.go"},
	}

	if unread := planThenExecuteUnreadRequiredPaths(brief, nil, planThenExecutePathAuthority{}); len(unread) == 0 {
		ginkgo.Fail("prompt text authorized_new_files must not grant typed new-file authority", 1)
	}
})

var _ = ginkgo.It("PlanExecuteTerminalTransportCompatibilityMatrix", func() {
	t := ginkgo.GinkgoT()
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "code_navigate", "submit_plan"},
		RequiredEditPaths: []string{"a.go"},
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
	}

	tests := []struct {
		name                  string
		input                 TurnPhaseContractInput
		message               llama.ChatMessage
		wantPhaseID           contractcompile.TurnPhaseID
		wantTransport         string
		wantParserOutcome     string
		wantTerminalShape     string
		wantSubmitExpressible bool
	}{
		{
			name: "deepseek native submit",
			input: TurnPhaseContractInput{
				Brief:               brief,
				Results:             results,
				SupportsNativeTools: true,
				ToolTransportMode:   "native",
				ToolChoice:          "required",
			},
			message: llama.ChatMessage{ToolCalls: []llama.ToolCall{{
				Function: llama.ToolCallFunc{Name: workspace.ToolNameSubmitPlan, Arguments: `{"tool":"submit_plan","edits":[{"path":"a.go","old_text":"package a","new_text":"package a\n"}]}`},
			}}},
			wantPhaseID:           contractcompile.TurnPhaseSubmitReadyNativeRequired,
			wantTransport:         "native",
			wantParserOutcome:     toolRequestNormalizerOutcomeNormalized,
			wantTerminalShape:     "submit_plan",
			wantSubmitExpressible: true,
		},
		{
			name: "textual qwen submit",
			input: TurnPhaseContractInput{
				Brief:               brief,
				Results:             results,
				TextualToolParser:   true,
				ToolTransportMode:   "textual",
				ResponseFormat:      "json",
				ParserContract:      "submit_plan_json",
				SupportsNativeTools: false,
			},
			message:               llama.ChatMessage{Content: `{"tool":"submit_plan","edits":[{"path":"a.go","old_text":"package a","new_text":"package a\n"}]}`},
			wantPhaseID:           contractcompile.TurnPhaseSubmitReadyTextual,
			wantTransport:         "textual",
			wantParserOutcome:     toolRequestNormalizerOutcomeNormalized,
			wantTerminalShape:     "submit_plan",
			wantSubmitExpressible: true,
		},
		{
			name: "malformed textual submit",
			input: TurnPhaseContractInput{
				Brief:             brief,
				Results:           append(results, workspace.ToolResult{Tool: "tool_parse", Summary: "Malformed tool request rejected", Error: "decode tool request"}),
				TextualToolParser: true,
				ToolTransportMode: "textual",
			},
			message:               llama.ChatMessage{Content: `{"tool":"submit_plan","edits":`},
			wantPhaseID:           contractcompile.TurnPhaseMalformedSubmitPlanRecovery,
			wantTransport:         "textual",
			wantParserOutcome:     toolRequestNormalizerOutcomeUnsupportedShape,
			wantTerminalShape:     "submit_plan",
			wantSubmitExpressible: true,
		},
		{
			name: "native transport mismatch",
			input: TurnPhaseContractInput{
				Brief:               brief,
				Results:             results,
				SupportsNativeTools: true,
				ToolTransportMode:   "native",
			},
			message: llama.ChatMessage{ToolCalls: []llama.ToolCall{{
				Function: llama.ToolCallFunc{Name: workspace.ToolNameCodeNavigate, Arguments: `{"tool":"context_query","query":"callers of Run"}`},
			}}},
			wantPhaseID:           contractcompile.TurnPhaseSubmitReadyNativeRequired,
			wantTransport:         "native",
			wantParserOutcome:     toolRequestNormalizerOutcomeTransportMismatch,
			wantTerminalShape:     "submit_plan",
			wantSubmitExpressible: true,
		},
	}

	for _, tt := range tests {
		ginkgo.By(tt.name, func() {
			if tt.name == "verify ready" {
				run := newRunnerRunFixture(&Runner{RuntimeBrief: tt.input.Brief})
				_, write := committedMutationResultForTest("pkg/feature.go")
				installMutationReceiptForTest(run, write)
				tt.input.Results = []workspace.ToolResult{write}
				state := run.implementationCompletionState(tt.input.Results)
				tt.input.ImplementationCompletion = &state
			}
			contract := deriveTurnPhaseContract(tt.input)
			if contract.PhaseID != tt.wantPhaseID {
				ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, tt.wantPhaseID), 1)
			}
			if contract.RuntimeCall.ToolTransportMode != tt.wantTransport {
				ginkgo.Fail(fmt.Sprintf("ToolTransportMode = %q, want %q", contract.RuntimeCall.ToolTransportMode, tt.wantTransport), 1)
			}
			if tt.wantSubmitExpressible {
				assertContains(t, contract.RuntimeCall.ExpressibleActionClasses, actionSubmitPlan)
			}
			assertContains(t, contract.ActionPolicy.TerminalResponseShapes, tt.wantTerminalShape)

			normalization := normalizeToolRequestSyntax(tt.message)
			if normalization.EventOutcome != tt.wantParserOutcome {
				ginkgo.Fail(fmt.Sprintf("parser outcome = %q, want %q; normalization=%#v", normalization.EventOutcome, tt.wantParserOutcome, normalization), 1)
			}
		})
	}
})

var _ = ginkgo.It("DeriveTurnPhaseContractStaleAnchorRefresh", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "submit_plan"},
			RequiredEditPaths: []string{"a.go"},
			StopConditions:    []string{"submit_plan_returned", "source_satisfied_reported", "path_specific_blocker_reported"},
		},
		Results: []workspace.ToolResult{
			{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)"},
			{
				Tool:    "submit_plan",
				Summary: "submit_plan rejected: required edit path coverage mismatch",
				Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; invalid edit anchor(s): `a.go: old_text not found in current source`.",
				Code:    workspace.SubmitPlanInvalidAnchorCode,
				Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
					Code:        workspace.SubmitPlanInvalidAnchorCode,
					Kind:        toolfacts.SubmitPlanDiagnosticInvalidAnchor,
					CanonicalID: "a.go",
				}),
			},
		},
		InspectionAuthority: permissionAuthorizedInspectionForTest("read_file", "code_navigate", "search_content"),
	})

	if contract.PhaseID != contractcompile.TurnPhaseStaleAnchorRefresh {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, contractcompile.TurnPhaseStaleAnchorRefresh), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionSourceSatisfied)
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionBlocker)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionSubmitPlan)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionSourceSatisfied)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionBlocker)
	assertEqualSet(t, contract.ActionPolicy.TerminalResponseShapes, nil)
	assertEqualSet(t, contract.StopConditions, []string{"bounded_source_refresh_returned"})
	assertEqualSet(t, contract.AllowedTools, []string{"read_file", "code_navigate", "search_content"})
})

var _ = ginkgo.It("DeriveTurnPhaseContractStaleAnchorRefreshWinsOverUnreadRequiredSource", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "submit_plan"},
			RequiredEditPaths: []string{"a.go", "b.go"},
		},
		Results: []workspace.ToolResult{
			{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)"},
			{
				Tool:    "submit_plan",
				Summary: "submit_plan rejected: required edit path coverage mismatch",
				Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; invalid edit anchor(s): `a.go: old_text not found in current source`.",
				Code:    workspace.SubmitPlanInvalidAnchorCode,
				Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
					Code:        workspace.SubmitPlanInvalidAnchorCode,
					Kind:        toolfacts.SubmitPlanDiagnosticInvalidAnchor,
					CanonicalID: "a.go",
				}),
			},
		},
		InspectionAuthority: permissionAuthorizedInspectionForTest("read_file", "code_navigate", "search_content"),
	})

	if contract.PhaseID != contractcompile.TurnPhaseStaleAnchorRefresh {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, contractcompile.TurnPhaseStaleAnchorRefresh), 1)
	}

	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionSubmitPlan)
})

var _ = ginkgo.It("PlanExecutePlanningStateAnchorRefreshWinsOverHistory", func() {
	t := ginkgo.GinkgoT()
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "code_navigate", "history_range", "history_symbol", "submit_plan"},
		RequiredEditPaths: []string{"a.go"},
		RequiredEvidence:  []string{string(workerruntime.RequiredEvidenceGitHistoryRationale)},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)", Output: "1:package a\n"},
		gitHistoryRangeResult("a.go"),
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; invalid edit anchor(s): `a.go: old_text not found in current source`.",
			Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
				Code:        workspace.SubmitPlanInvalidAnchorCode,
				Kind:        toolfacts.SubmitPlanDiagnosticInvalidAnchor,
				CanonicalID: "a.go",
			}),
		},
	}

	state := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})
	if state.Mode != PlanExecutePlanningModeAnchorRefresh {
		ginkgo.Fail(fmt.Sprintf("mode = %q, want anchor_refresh", state.Mode), 1)
	}

	assertEqualSet(t, state.ParserVisibleTools, []string{"read_file", "code_navigate"})

	if slices.Contains(state.ParserVisibleTools, "submit_plan") {
		ginkgo.Fail(fmt.Sprintf("anchor-refresh state must not expose submit_plan: %#v", state.ParserVisibleTools), 1)
	}

	withoutHistory := []workspace.ToolResult{results[0], results[2]}

	state = derivePlanExecutePlanningState(brief, withoutHistory, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})

	if state.Mode != PlanExecutePlanningModeAnchorRefresh {
		ginkgo.Fail(fmt.Sprintf("mode with pending history = %q, want anchor_refresh", state.Mode), 1)
	}

	assertEqualSet(t, state.ParserVisibleTools, []string{"read_file", "code_navigate"})

	withRefresh := append(append([]workspace.ToolResult(nil), withoutHistory...),
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
	)

	state = derivePlanExecutePlanningState(brief, withRefresh, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})

	if state.Mode != PlanExecutePlanningModeHistoryPending {
		ginkgo.Fail(fmt.Sprintf("mode after anchor refresh with pending history = %q, want history_pending", state.Mode), 1)
	}

})

var _ = ginkgo.It("PlanExecuteRecoverySurfaceAnchorRefreshWinsOverSourceSatisfiedTerminal", func() {
	t := ginkgo.GinkgoT()
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "code_navigate", "submit_plan"},
		RequiredEditPaths: []string{"a.go"},
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `a.go: old_text not found in current source`.",
			Code:    workspace.SubmitPlanInvalidAnchorCode,
			Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
				Code:        workspace.SubmitPlanInvalidAnchorCode,
				Kind:        toolfacts.SubmitPlanDiagnosticInvalidAnchor,
				CanonicalID: "a.go",
			}),
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: source-satisfied terminal shape required",
			Error:   "submit_plan cannot carry a source-satisfied/no-op closeout. Answer `Decision: source-satisfied` with per-path current-state evidence, submit real edits, or answer <<QUESTION>> with the exact blocker.",
			Code:    workspace.SubmitPlanSourceSatisfiedTerminalRequiredCode,
		},
	}

	surface := derivePlanExecuteRecoverySurface(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})
	if surface.Kind != PlanExecuteRecoverySurfaceAnchorRefresh {
		ginkgo.Fail(fmt.Sprintf("surface kind = %q, want anchor_refresh; state=%#v", surface.Kind, surface.State), 1)
	}

	assertEqualSet(t, surface.State.CallableTools, []string{"read_file", "code_navigate"})
	assertEqualSet(t, surface.State.TerminalResponseShapes, nil)

	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:             brief,
		Results:           results,
		BaseAllowedTools:  brief.AllowedTools,
		TextualToolParser: true,
		InspectionAuthority: permissionAuthorizedInspectionForTest(
			"read_file", "code_navigate",
		),
	})
	if contract.PhaseID != contractcompile.TurnPhaseStaleAnchorRefresh {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, contractcompile.TurnPhaseStaleAnchorRefresh), 1)
	}

	assertEqualSet(t, contract.ActionPolicy.TerminalResponseShapes, nil)
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionSourceSatisfied)
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionBlocker)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionSubmitPlan)
})

var _ = ginkgo.It("DeriveTurnPhaseContractGroundingRejectedPlanUsesSourceRefresh", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "submit_plan"},
		},
		Results: []workspace.ToolResult{
			{Tool: "read_file", Summary: "Read f1 cmd/idiom/product_workflow.go lines 1-120 (total lines: 2894)"},
			{
				Tool:    "submit_plan",
				Summary: "submit_plan rejected: edit paths lack source grounding",
				Error:   "submit_plan edits for plan-execute planner atoms must be grounded before executor handoff; missing source-body evidence for `internal/workflows/productworkflow/tdd.go`. Read each existing edit path with `read_file` or `code_navigate` `read_symbol`/`extract_body`, then submit a corrected plan.",
				Code:    planThenExecuteResultSubmitPlanMissingSourceBody,
				Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
					Code:        planThenExecuteResultSubmitPlanMissingSourceBody,
					Kind:        toolfacts.SubmitPlanDiagnosticMissingSourceGrounding,
					CanonicalID: "internal/workflows/productworkflow/tdd.go",
				}),
			},
		},
		TextualToolParser:   true,
		InspectionAuthority: permissionAuthorizedInspectionForTest("read_file", "code_navigate", "search_content"),
	})

	if contract.PhaseID != contractcompile.TurnPhaseStaleAnchorRefresh {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, contractcompile.TurnPhaseStaleAnchorRefresh), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionSubmitPlan)

	if len(contract.AllowedTools) != 0 || len(contract.ActionPolicy.CallableToolNames) != 0 {
		ginkgo.Fail(fmt.Sprintf("textual source refresh must not advertise manifest tools, got allowed=%#v callable=%#v", contract.AllowedTools, contract.ActionPolicy.CallableToolNames), 1)
	}

	if contract.RuntimeCall.ParserContract != "source_refresh" {
		ginkgo.Fail(fmt.Sprintf("ParserContract = %q, want source_refresh", contract.RuntimeCall.ParserContract), 1)
	}
})

var _ = ginkgo.It("DeriveTurnPhaseContractSubmitCorrectionAfterAnchorRefresh", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "submit_plan"},
			RequiredEditPaths: []string{"a.go"},
		},
		Results: []workspace.ToolResult{
			readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
			{
				Tool:    "submit_plan",
				Summary: "submit_plan rejected: required edit path coverage mismatch",
				Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; invalid edit anchor(s): `a.go: old_text not found in current source`.",
			},
			codeNavigateSourceWindowResult("a.go", 1, 10, 10, "route_summary: Read Target in a.go lines 1-10\n1:package a"),
		},
		TextualToolParser: true,
	})

	if contract.PhaseID != contractcompile.TurnPhaseSubmitReadyTextual {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, contractcompile.TurnPhaseSubmitReadyTextual), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSubmitPlan)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionRead)

	if contract.DerivationReason != "plan_execute_submit_plan_correction" {
		ginkgo.Fail(fmt.Sprintf("DerivationReason = %q, want plan_execute_submit_plan_correction", contract.DerivationReason), 1)
	}
})

var _ = ginkgo.It("DeriveTurnPhaseContractSubmitCorrectionIsSubmitPlanOnly", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "submit_plan"},
			RequiredEditPaths: []string{"a.go", "b.go"},
		},
		Results: []workspace.ToolResult{
			readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
			readFileSourceWindowResult("b.go", 1, 10, 10, "1:package b\n"),
			{
				Tool:    "submit_plan",
				Summary: "submit_plan rejected: required edit path coverage mismatch",
				Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; missing required edit path(s): `b.go`.",
			},
		},
		TextualToolParser: true,
	})

	if contract.PhaseID != contractcompile.TurnPhaseSubmitReadyTextual {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, contractcompile.TurnPhaseSubmitReadyTextual), 1)
	}

	if contract.DerivationReason != "plan_execute_submit_plan_correction" {
		ginkgo.Fail(fmt.Sprintf("DerivationReason = %q, want plan_execute_submit_plan_correction", contract.DerivationReason), 1)
	}

	assertEqualSet(t, contract.TerminalOutcomes, []string{"submit_plan"})
	assertEqualSet(t, contract.ActionPolicy.AllowedActionClasses, []string{actionSubmitPlan})
	assertEqualSet(t, contract.ActionPolicy.TerminalResponseShapes, []string{"submit_plan"})
	assertEqualSet(t, contract.RuntimeCall.ExpressibleActionClasses, []string{actionSubmitPlan})
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionSourceSatisfied)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionBlocker)
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionSourceSatisfied)
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionBlocker)
})

var _ = ginkgo.It("TurnPhaseContractInvalidActionClassesWinDuringNormalization", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"submit_plan"},
			RequiredEditPaths: []string{"a.go"},
		},
		Results: []workspace.ToolResult{
			readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
			{
				Tool:    "submit_plan",
				Summary: "submit_plan rejected: required edit path coverage mismatch",
				Error:   "missing required edit path(s): `a.go`.",
			},
		},
		TextualToolParser: true,
	})

	contract.ActionPolicy.AllowedActionClasses = []string{actionSubmitPlan, actionBlocker}
	contract.ActionPolicy.InvalidActionClasses = []string{actionBlocker}
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionSubmitPlan, actionBlocker}

	contract.ActionPolicy.AllowedActionClasses = actionClassesAllowedByPolicy(contract.ActionPolicy.AllowedActionClasses, contract.ActionPolicy.InvalidActionClasses)
	contract.RuntimeCall.ExpressibleActionClasses = actionClassesAllowedByPolicy(contract.RuntimeCall.ExpressibleActionClasses, contract.ActionPolicy.InvalidActionClasses)

	assertEqualSet(t, contract.ActionPolicy.AllowedActionClasses, []string{actionSubmitPlan})
	assertEqualSet(t, contract.RuntimeCall.ExpressibleActionClasses, []string{actionSubmitPlan})
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionBlocker)
})

var _ = ginkgo.It("DeriveTurnPhaseContractMalformedSubmitPlanRecoveryUsesTextualOutput", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "submit_plan"},
			RequiredEditPaths: []string{"a.go"},
		},
		Results: []workspace.ToolResult{
			readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
			{Tool: "tool_parse", Summary: "Malformed tool request rejected", Error: "decode tool request"},
		},
		SupportsNativeTools: true,
	})

	if contract.PhaseID != contractcompile.TurnPhaseMalformedSubmitPlanRecovery {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, contractcompile.TurnPhaseMalformedSubmitPlanRecovery), 1)
	}

	if contract.RuntimeCall.ExpectedVisibleOutput != "visible_text_tool_request" {
		ginkgo.Fail(fmt.Sprintf("ExpectedVisibleOutput = %q, want visible_text_tool_request", contract.RuntimeCall.ExpectedVisibleOutput), 1)
	}

	assertEqualSet(t, contract.RuntimeCall.ExpressibleActionClasses, []string{actionSubmitPlan, actionSourceSatisfied, actionBlocker})
})

var _ = ginkgo.It("DeriveTurnPhaseContractReviewRepairTerminal", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			PermissionProfile: "plan_submission_review",
			AllowedTools:      []string{"read_file", "search_content", "submit_plan"},
		},
		PhaseHint:           contractcompile.TurnPhaseReviewRepairTerminal,
		InspectionAuthority: permissionAuthorizedInspectionForTest("read_file", "search_content"),
	})

	if contract.PhaseID != contractcompile.TurnPhaseReviewRepairTerminal {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, contractcompile.TurnPhaseReviewRepairTerminal), 1)
	}

	assertEqualSet(t, contract.AllowedTools, []string{"read_file", "search_content", "submit_plan"})
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionReviewPass)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
	assertNotContains(t, contract.ActionPolicy.InvalidActionClasses, actionSearch)
	assertContains(t, contract.TerminalOutcomes, "Decision: pass")
})

var _ = ginkgo.It("DeriveTurnPhaseContractReviewRepairFailedEvidenceStartsTerminal", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			PermissionProfile: "plan_submission_review",
			AllowedTools:      []string{"read_file", "search_content", "submit_plan"},
			Readiness: &workerruntime.ReadinessState{
				OpenObligations: []string{"source_sanity_failed:internal/contracts/writerroute/decision_test.go"},
			},
		},
		InspectionAuthority: permissionAuthorizedInspectionForTest("read_file", "search_content"),
	})

	if contract.PhaseID != contractcompile.TurnPhaseReviewRepairTerminal {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, contractcompile.TurnPhaseReviewRepairTerminal), 1)
	}

	assertEqualSet(t, contract.AllowedTools, []string{"read_file", "search_content", "submit_plan"})
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSubmitPlan)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionBlocker)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionReviewPass)
	assertNotContains(t, contract.TerminalOutcomes, "Decision: pass")
	assertNotContains(t, contract.ActionPolicy.InvalidActionClasses, actionRead)
	assertNotContains(t, contract.ActionPolicy.InvalidActionClasses, actionSearch)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionReviewPass)
})

var _ = ginkgo.It("DeriveTurnPhaseContractReviewRepairCompletionGateStartsTerminal", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			PermissionProfile: "plan_submission_review",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "submit_plan"},
		},
		Results: []workspace.ToolResult{completionGateRejectionWithRecovery(
			"Review-repair completion gate rejected final answer",
			"review-repair must close with exactly one terminal outcome",
			newCompletionGateRecoveryEnvelope(completionGateInvariantPlanSubmission, "review_repair_closeout", completionGateViolationPlanSubmission, nil),
		)},
		InspectionAuthority: permissionAuthorizedInspectionForTest("read_file", "code_navigate", "search_content"),
	})

	if contract.PhaseID != contractcompile.TurnPhaseReviewRepairTerminal {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, contractcompile.TurnPhaseReviewRepairTerminal), 1)
	}

	assertEqualSet(t, contract.AllowedTools, []string{"read_file", "code_navigate", "search_content", "submit_plan"})
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionReviewPass)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSubmitPlan)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionBlocker)
	assertContains(t, contract.TerminalOutcomes, "Decision: pass")
	assertContains(t, contract.TerminalOutcomes, "submit_plan")
	assertNotContains(t, contract.ActionPolicy.InvalidActionClasses, actionRead)
	assertNotContains(t, contract.ActionPolicy.InvalidActionClasses, actionSearch)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
})

var _ = ginkgo.It("DeriveTurnPhaseContractImplementationWriteReady", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "edit_file", "apply_patch", "shell"},
			RequiredEditPaths: []string{"a.go"},
		},
		InspectionAuthority: permissionAuthorizedInspectionForTest("read_file", "code_navigate", "search_content", "shell"),
	})

	if contract.PhaseID != contractcompile.TurnPhaseImplementationWriteReady {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, contractcompile.TurnPhaseImplementationWriteReady), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionPerEditWrite)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
	assertNotContains(t, contract.ActionPolicy.InvalidActionClasses, actionGenericProbe)
	assertContains(t, contract.AllowedTools, "edit_file")
	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "shell")
	assertNotContains(t, contract.HiddenTools, "shell")
})

var _ = ginkgo.It("DeriveTurnPhaseContractImplementationTargetSourcePending", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools: []string{
				"context_pack", "search_content", "read_file", "code_navigate",
				"edit_file", "write_file", "apply_patch", "source_sanity", "shell",
			},
		},
	})

	if contract.PhaseID != contractcompile.TurnPhaseImplementationTargetSource {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, contractcompile.TurnPhaseImplementationTargetSource), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionPerEditWrite)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionBlocker)
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionFinalAnswer)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionVerify)
	assertContains(t, contract.AllowedTools, "context_pack")
	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "edit_file")
	assertContains(t, contract.HiddenTools, "source_sanity")

	joinedNext := strings.Join(contract.RequiredNext, "\n")
	for _, want := range []string{"source-body evidence", "parser-readable write", "<<QUESTION>>"} {
		if !strings.Contains(joinedNext, want) {
			ginkgo.Fail(fmt.Sprintf("RequiredNext missing %q: %#v", want, contract.RequiredNext), 1)
		}
	}
})

var _ = ginkgo.It("DeriveTurnPhaseContractImplementationTargetSourceDoesNotOverrideWriteReady", func() {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "edit_file", "source_sanity"},
			Readiness: &workerruntime.ReadinessState{
				ReadyToWrite: true,
			},
		},
	})

	if contract.PhaseID == contractcompile.TurnPhaseImplementationTargetSource {
		ginkgo.Fail(fmt.Sprintf("write-ready packet should not use target-source phase: %#v", contract), 1)
	}
})

var _ = ginkgo.It("DeriveTurnPhaseContractWriteReadyDropsSupersededPendingRedirect", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "fix the session pane cursor scrolling",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "edit_file", "apply_patch"},
			TargetPacket: []string{
				"candidate file: internal/surfaces/tui/app/implementation_dispatch.go",
				"candidate file: internal/surfaces/tui/app/sessions_panel.go",
			},
		},
		Results: []workspace.ToolResult{
			{
				Tool:    "read_file",
				Summary: "Redirected internal/surfaces/tui/app/implementation_dispatch.go to code_navigate",
				Output:  "Use code_navigate for source-body evidence in internal/surfaces/tui/app/implementation_dispatch.go.",
			},
			readFileSourceWindowResult(
				"internal/surfaces/tui/app/sessions_panel.go",
				1,
				219,
				219,
				"func renderSessionsPanelContent() {}\nfunc handleSessionsPanelKey() {}\n",
			),
		},
		BaseAllowedTools: []string{"read_file", "code_navigate", "search_content", "edit_file", "apply_patch"},
	})

	if contract.PhaseID != contractcompile.TurnPhaseImplementationWriteReady {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, contractcompile.TurnPhaseImplementationWriteReady), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionPerEditWrite)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "search_content")
	assertNotContains(t, contract.AllowedTools, "code_navigate")

	joined := strings.Join(append(append([]string{}, contract.RequiredNext...), contract.InvalidNext...), "\n")
	if strings.Contains(joined, "pending redirected source-body obligation") ||
		strings.Contains(joined, "implementation_dispatch.go via code_navigate") {

		ginkgo.Fail(fmt.Sprintf("stale redirected candidate should not remain a hard write-ready obligation: %#v / %#v", contract.RequiredNext, contract.InvalidNext), 1)
	}
})

var _ = ginkgo.It("DeriveTurnPhaseContractPlanExecuteAllowedScopeDoesNotUseImplementationTargetSource", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"context_pack", "read_file", "code_navigate", "search_content", "submit_plan", "edit_file", "apply_patch"},
			AllowedEditPaths:  []string{"pkg/feature.go", "pkg/feature_test.go"},
		},
	})

	if contract.PhaseID != contractcompile.TurnPhaseCandidateSource {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, contractcompile.TurnPhaseCandidateSource), 1)
	}

	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionPerEditWrite)
	assertNotContains(t, contract.AllowedTools, "edit_file")
	assertNotContains(t, contract.AllowedTools, "apply_patch")
})

var _ = ginkgo.It("DeriveTurnPhaseContractImplementationVerifyReady", func() {
	t := ginkgo.GinkgoT()
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"internal/surfaces/tui/app/model_swap.go"},
		AllowedTools:      []string{"read_file", "code_navigate", "search_content", "edit_file", "source_sanity", "shell", "git_diff", "git_status"},
	}
	run := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
	_, write := committedMutationResultForTest("internal/surfaces/tui/app/model_swap.go")
	installMutationReceiptForTest(run, write)
	state := run.implementationCompletionState([]workspace.ToolResult{write})
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:                    brief,
		Results:                  []workspace.ToolResult{write},
		BaseAllowedTools:         brief.AllowedTools,
		ImplementationCompletion: &state,
		InspectionAuthority:      permissionAuthorizedInspectionForTest(brief.AllowedTools...),
	})

	if contract.PhaseID != contractcompile.TurnPhaseImplementationVerifyReady {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, contractcompile.TurnPhaseImplementationVerifyReady), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionVerify)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionPerEditWrite)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionBlocker)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.AllowedTools, "source_sanity")
	assertContains(t, contract.AllowedTools, "shell")
	assertContains(t, contract.AllowedTools, "edit_file")
	assertContains(t, contract.AllowedTools, "read_file")
})

var _ = ginkgo.It("DeriveTurnPhaseContractPostWriteAllowsAdditiveNewFileBeforeVerification", func() {
	t := ginkgo.GinkgoT()
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Objective:         "Create the smallest useful browser version.",
		AllowedTools: []string{
			"read_file", "search_content", "edit_file", "write_file", "apply_patch",
			"mkdir", "source_sanity", "shell", "git_diff", "git_status",
		},
	}
	run := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
	_, write := committedMutationResultForTest("go.mod")
	installMutationReceiptForTest(run, write)
	state := run.implementationCompletionState([]workspace.ToolResult{write})
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:                    brief,
		Results:                  []workspace.ToolResult{write},
		BaseAllowedTools:         brief.AllowedTools,
		ImplementationCompletion: &state,
		InspectionAuthority:      permissionAuthorizedInspectionForTest(brief.AllowedTools...),
	})

	if contract.PhaseID != contractcompile.TurnPhaseImplementationVerifyReady {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, contractcompile.TurnPhaseImplementationVerifyReady), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionVerify)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionPerEditWrite)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionBlocker)
	assertContains(t, contract.AllowedTools, "write_file")
	assertContains(t, contract.AllowedTools, "apply_patch")
	assertContains(t, contract.AllowedTools, "mkdir")
	assertContains(t, contract.AllowedTools, "source_sanity")
	assertContains(t, contract.AllowedTools, "edit_file")
	assertContains(t, contract.AllowedTools, "read_file")
	assertNotContains(t, contract.HiddenTools, "edit_file")
})

var _ = ginkgo.It("DeriveTurnPhaseContractImplementationTextualDoesNotAdvertiseManifestTools", func() {
	t := ginkgo.GinkgoT()
	verifyBrief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "edit_file", "source_sanity", "shell", "git_diff"},
	}
	verifyRun := newRunnerRunFixture(&Runner{RuntimeBrief: verifyBrief})
	_, verifyWrite := committedMutationResultForTest("pkg/feature.go")
	installMutationReceiptForTest(verifyRun, verifyWrite)
	verifyState := verifyRun.implementationCompletionState([]workspace.ToolResult{verifyWrite})
	tests := []struct {
		name           string
		input          TurnPhaseContractInput
		wantPhaseID    contractcompile.TurnPhaseID
		wantAction     string
		wantHiddenTool string
	}{
		{
			name: "target source",
			input: TurnPhaseContractInput{
				Brief: workerruntime.WorkerRuntimeBrief{
					TaskFamily:        "implementation",
					Contract:          "implementation_result",
					PermissionProfile: "guarded_workspace_write",
					AllowedTools:      []string{"context_pack", "search_content", "read_file", "code_navigate", "edit_file", "apply_patch"},
				},
				InspectionAuthority: permissionAuthorizedInspectionForTest("context_pack", "search_content", "read_file", "code_navigate", "edit_file", "apply_patch"),
				TextualToolParser:   true,
				ToolTransportMode:   "textual",
			},
			wantPhaseID:    contractcompile.TurnPhaseImplementationTargetSource,
			wantAction:     actionRead,
			wantHiddenTool: "read_file",
		},
		{
			name: "write ready",
			input: TurnPhaseContractInput{
				Brief: workerruntime.WorkerRuntimeBrief{
					TaskFamily:        "implementation",
					Contract:          "implementation_result",
					PermissionProfile: "guarded_workspace_write",
					AllowedTools:      []string{"read_file", "edit_file", "apply_patch", "shell"},
					RequiredEditPaths: []string{"pkg/feature.go"},
				},
				InspectionAuthority: permissionAuthorizedInspectionForTest("read_file", "edit_file", "apply_patch", "shell"),
				TextualToolParser:   true,
				ToolTransportMode:   "textual",
			},
			wantPhaseID:    contractcompile.TurnPhaseImplementationWriteReady,
			wantAction:     actionPerEditWrite,
			wantHiddenTool: "edit_file",
		},
		{
			name: "verify ready",
			input: TurnPhaseContractInput{
				Brief:                    verifyBrief,
				Results:                  []workspace.ToolResult{verifyWrite},
				BaseAllowedTools:         verifyBrief.AllowedTools,
				ImplementationCompletion: &verifyState,
				InspectionAuthority:      permissionAuthorizedInspectionForTest(verifyBrief.AllowedTools...),
				TextualToolParser:        true,
				ToolTransportMode:        "textual",
			},
			wantPhaseID:    contractcompile.TurnPhaseImplementationVerifyReady,
			wantAction:     actionVerify,
			wantHiddenTool: "source_sanity",
		},
	}

	for _, tt := range tests {
		ginkgo.By(tt.name, func() {
			contract := deriveTurnPhaseContract(tt.input)
			if contract.PhaseID != tt.wantPhaseID {
				ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, tt.wantPhaseID), 1)
			}

			if len(contract.AllowedTools) != 0 || len(contract.ActionPolicy.CallableToolNames) != 0 {
				ginkgo.Fail(fmt.Sprintf("textual implementation phase must not advertise manifest tools, got allowed=%#v callable=%#v", contract.AllowedTools, contract.ActionPolicy.CallableToolNames), 1)
			}

			assertContains(t, contract.HiddenTools, tt.wantHiddenTool)
			assertContains(t, contract.ActionPolicy.AllowedActionClasses, tt.wantAction)

			if issues := autonomy.ReconcileTurnPhaseSurface(
				contract.autonomyEventPayload(),
				autonomy.SurfaceSummaryFromTurnPhaseContract(contract.autonomyEventPayload()),
			); len(issues) != 0 {
				ginkgo.Fail(fmt.Sprintf("textual implementation self-summary should reconcile cleanly, got %#v", issues), 1)
			}
		})
	}
})

var _ = ginkgo.It("DeriveTurnPhaseContractGenericTextualDoesNotAdvertiseManifestTools", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "debug_logs",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file", "search_content"},
			RequiredNext:      []string{"inspect the concrete log artifact or answer from existing evidence"},
		},
		TextualToolParser: true,
		ToolTransportMode: "textual",
	})

	if contract.PhaseID != contractcompile.TurnPhaseGeneric {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, contractcompile.TurnPhaseGeneric), 1)
	}

	if len(contract.AllowedTools) != 0 || len(contract.ActionPolicy.CallableToolNames) != 0 {
		ginkgo.Fail(fmt.Sprintf("textual generic phase must not advertise manifest tools, got allowed=%#v callable=%#v", contract.AllowedTools, contract.ActionPolicy.CallableToolNames), 1)
	}

	assertContains(t, contract.HiddenTools, "read_file")
	assertContains(t, contract.RuntimeCall.ExpressibleActionClasses, actionFinalAnswer)

	if issues := autonomy.ReconcileTurnPhaseSurface(
		contract.autonomyEventPayload(),
		autonomy.SurfaceSummaryFromTurnPhaseContract(contract.autonomyEventPayload()),
	); len(issues) != 0 {
		ginkgo.Fail(fmt.Sprintf("textual generic self-summary should reconcile cleanly, got %#v", issues), 1)
	}
})

var _ = ginkgo.It("DeriveTurnPhaseContractVerificationOnlyShellAllowsVerify", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "verification_only",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_verification_shell",
			AllowedTools:      []string{"shell"},
			RequiredNext:      []string{"first tool call must run one verification command with shell"},
		},
	})

	if contract.PhaseID != contractcompile.TurnPhaseGeneric {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, contractcompile.TurnPhaseGeneric), 1)
	}

	assertEqualSet(t, contract.AllowedTools, []string{"shell"})
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionVerify)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionBlocker)
	assertContains(t, contract.RuntimeCall.ExpressibleActionClasses, actionVerify)

	if issues := autonomy.ReconcileTurnPhaseSurface(
		contract.autonomyEventPayload(),
		autonomy.TurnPhaseSurfaceSummaryEvent{
			SchemaVersion:           autonomy.TurnPhaseSurfaceSummarySchemaVersion,
			SurfaceID:               "next_action_state",
			ContractID:              contract.ContractID,
			PhaseID:                 string(contract.PhaseID),
			AdvertisedActionClasses: []string{actionVerify, actionBlocker},
		},
	); len(issues) != 0 {
		ginkgo.Fail(fmt.Sprintf("verification-only shell surface should reconcile cleanly, got %#v", issues), 1)
	}
})

var _ = ginkgo.It("DeriveTurnPhaseContractUnscopedImplementationVerifyReadyKeepsWritesCallable", func() {
	t := ginkgo.GinkgoT()
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "code_navigate", "search_content", "edit_file", "source_sanity", "shell", "git_diff", "git_status"},
	}
	run := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
	_, write := committedMutationResultForTest("pkg/feature.go")
	installMutationReceiptForTest(run, write)
	state := run.implementationCompletionState([]workspace.ToolResult{write})
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:                    brief,
		Results:                  []workspace.ToolResult{write},
		BaseAllowedTools:         brief.AllowedTools,
		ImplementationCompletion: &state,
		InspectionAuthority:      permissionAuthorizedInspectionForTest(brief.AllowedTools...),
	})

	if contract.PhaseID != contractcompile.TurnPhaseImplementationVerifyReady {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, contractcompile.TurnPhaseImplementationVerifyReady), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionVerify)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionPerEditWrite)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.AllowedTools, "source_sanity")
	assertContains(t, contract.AllowedTools, "edit_file")
	assertContains(t, contract.AllowedTools, "read_file")
	assertNotContains(t, contract.HiddenTools, "edit_file")
})

var _ = ginkgo.It("DeriveTurnPhaseContractFailedVerificationOpensRepairLane", func() {
	t := ginkgo.GinkgoT()
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "code_navigate", "search_content", "edit_file", "apply_patch", "source_sanity", "shell", "git_diff", "git_status"},
	}
	run := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
	_, write := committedMutationResultForTest("pkg/feature.go")
	installMutationReceiptForTest(run, write)
	failedBuild := shellCommandResultForTest("go test ./...", "", "build failed")
	failedBuild.Facts.DiagnosticEvidencePaths = []string{"pkg/feature.go"}
	results := []workspace.ToolResult{write, failedBuild}
	state := run.implementationCompletionState(results)
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: brief, Results: results, BaseAllowedTools: brief.AllowedTools, ImplementationCompletion: &state,
	})

	if contract.Phase != "verification_repair" {
		ginkgo.Fail(fmt.Sprintf("Phase = %q, want verification_repair", contract.Phase), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionPerEditWrite)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionVerify)
	assertContains(t, contract.AllowedTools, "edit_file")
	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "shell")
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
})

var _ = ginkgo.It("DeriveTurnPhaseContractUnscopedImplementationWriteReadyAfterSourceBody", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "search_content", "file_stats", "edit_file", "apply_patch", "source_sanity"},
		},
		Results: []workspace.ToolResult{
			readFileSourceWindowResult(
				"internal/labels/labels.go",
				1,
				5,
				5,
				"func ShortLabel() string {\n\treturn \"Save changes\"\n}\n",
			),
		},
	})

	if contract.Phase != "target_write_ready" {
		ginkgo.Fail(fmt.Sprintf("Phase = %q, want target_write_ready", contract.Phase), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionPerEditWrite)
	assertContains(t, contract.AllowedTools, "edit_file")
	assertContains(t, contract.AllowedTools, "apply_patch")
	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "search_content")
	assertContains(t, contract.AllowedTools, "file_stats")
	assertContains(t, contract.ActionPolicy.CallableToolNames, "file_stats")

	joinedConstraints := strings.Join(contract.Constraints, "\n")
	if !strings.Contains(joinedConstraints, "bounded_source_refresh_allowance") ||
		!strings.Contains(joinedConstraints, implementationTargetWriteReadySourceProbeToolsPhrase) {

		ginkgo.Fail(fmt.Sprintf("target-write-ready contract should project bounded source refresh allowance: %#v", contract.Constraints), 1)
	}
})

var _ = ginkgo.It("DeriveTurnPhaseContractUnscopedImplementationDesignDocReadStaysTargetSource", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "context_pack", "search_content", "edit_file", "apply_patch"},
		},
		Results: []workspace.ToolResult{
			readFileSourceWindowResult(
				"docs/design-tdd-coverage.md",
				1,
				200,
				815,
				"Phase 1: EARS And BDD Coverage Checks\n",
			),
		},
	})

	if contract.PhaseID != contractcompile.TurnPhaseImplementationTargetSource {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want target source after design-doc-only read", contract.PhaseID), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionPerEditWrite)
	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "context_pack")
	assertContains(t, contract.AllowedTools, "edit_file")
})

var _ = ginkgo.It("DeriveTurnPhaseContractUnscopedImplementationWrongTargetReadStaysTargetSource", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "the session panel cursor should scroll down when navigating with arrow keys",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "context_pack", "code_navigate", "search_content", "edit_file", "apply_patch"},
			TargetPacket: []string{
				"### Candidate Files",
				"- `internal/surfaces/tui/app/app.go` (read_only_evidence): weak packet candidate",
				"- `internal/runtime/agent/contract.go` (editable): matched task terms",
				"- `internal/runtime/agent/runner_core.go` (editable): matched task terms",
			},
		},
		Results: []workspace.ToolResult{
			readFileSourceWindowResult(
				"internal/surfaces/tui/app/app.go",
				1,
				55,
				55,
				"package app\n\ntype Model struct{}\n",
			),
		},
	})

	if contract.PhaseID != contractcompile.TurnPhaseImplementationTargetSource {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want target_source after non-plausible source read; contract=%#v", contract.PhaseID, contract), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
	assertContains(t, contract.AllowedTools, "context_pack")
	assertContains(t, contract.AllowedTools, "read_file")
})

var _ = ginkgo.It("DeriveTurnPhaseContractSingleObjectiveTokenReadStaysTargetSource", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "The history tab's shortened text carries newlines that do not make sense for wrapping.",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "context_pack", "code_navigate", "search_content", "edit_file", "apply_patch"},
			TargetPacket: []string{
				"### Candidate Files",
				"- `internal/runtime/agent/contract.go` (editable): matched task terms",
				"- `cmd/idiom/lane_history.go` (read_only_evidence): related by calls relationship",
			},
		},
		Results: []workspace.ToolResult{
			readFileSourceWindowResult(
				"cmd/idiom/lane_history.go",
				1,
				331,
				331,
				"package main\n\ntype laneHistoryEvent struct{}\n",
			),
		},
	})

	if contract.PhaseID != contractcompile.TurnPhaseImplementationTargetSource {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want target_source after weak single-token evidence read; contract=%#v", contract.PhaseID, contract), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
	assertContains(t, contract.AllowedTools, "context_pack")
	assertContains(t, contract.AllowedTools, "search_content")
})

var _ = ginkgo.It("DeriveTurnPhaseContractNonEditableTargetPacketReadStaysTargetSource", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "the session panel cursor should scroll down when navigating with arrow keys",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "context_pack", "code_navigate", "search_content", "edit_file", "apply_patch"},
			TargetPacket: []string{
				"### Candidate Files",
				"- `internal/surfaces/tui/app/model_state_test.go` (test): related coverage only",
			},
		},
		Results: []workspace.ToolResult{
			readFileSourceWindowResult(
				"internal/surfaces/tui/app/model_state_test.go",
				1,
				80,
				80,
				"package app\n\nfunc TestModelState(t *testing.T) {}\n",
			),
		},
	})

	if contract.PhaseID != contractcompile.TurnPhaseImplementationTargetSource {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want target_source after non-editable target-packet read; contract=%#v", contract.PhaseID, contract), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.AllowedTools, "context_pack")
})

var _ = ginkgo.It("TargetPacketWritableMentionedPathsSkipsReadOnlyPriorRecommendation", func() {
	t := ginkgo.GinkgoT()
	got := targetPacketWritableMentionedPaths([]string{
		"read_only_evidence previous assistant recommendation: Editable candidate file: internal/runtime/workerruntime/vocabulary.go",
		"Editable candidate file from previous assistant recommendation: pkg/a_test.go",
	})

	assertNotContains(t, got, "internal/runtime/workerruntime/vocabulary.go")
	assertContains(t, got, "pkg/a_test.go")
})

var _ = ginkgo.It("DeriveTurnPhaseContractUnscopedImplementationPlausibleTargetReadBecomesWriteReady", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "the session panel cursor should scroll down when navigating with arrow keys",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "context_pack", "code_navigate", "search_content", "edit_file", "apply_patch"},
			TargetPacket: []string{
				"### Candidate Files",
				"- `internal/runtime/agent/contract.go` (editable): matched task terms",
				"- `internal/runtime/agent/runner_core.go` (editable): matched task terms",
			},
		},
		Results: []workspace.ToolResult{
			readFileSourceWindowResult(
				"internal/surfaces/tui/app/sessions_panel.go",
				1,
				80,
				320,
				"package app\n\ntype sessionsPanelModel struct{}\n",
			),
		},
	})

	if contract.Phase != "target_write_ready" {
		ginkgo.Fail(fmt.Sprintf("Phase = %q, want target_write_ready for objective-plausible source read; contract=%#v", contract.Phase, contract), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionPerEditWrite)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.AllowedTools, "edit_file")
	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "search_content")
	assertNotContains(t, contract.AllowedTools, "context_pack")
})

var _ = ginkgo.It("DeriveTurnPhaseContractUnscopedImplementationStaysWriteReadyAfterMultipleSourceReads", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "edit_file", "apply_patch", "source_sanity"},
		},
		Results: []workspace.ToolResult{
			readFileSourceWindowResult(
				"docs/design-tdd-coverage.md",
				1,
				20,
				100,
				"Phase 1: EARS And BDD Coverage Checks\n",
			),
			readFileSourceWindowResult(
				"internal/workflows/productworkflow/coverage_gate_test.go",
				1,
				20,
				100,
				"func TestCoverageGate(t *testing.T) {}\n",
			),
		},
	})

	if contract.Phase != "target_write_ready" {
		ginkgo.Fail(fmt.Sprintf("Phase = %q, want target_write_ready", contract.Phase), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionPerEditWrite)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.AllowedTools, "edit_file")
	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "search_content")
	assertNotContains(t, contract.AllowedTools, "code_navigate")
})

var _ = ginkgo.It("DeriveTurnPhaseContractUnscopedImplementationStaysTargetSourceAfterSymbolMiss", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "edit_file", "apply_patch"},
		},
		Results: []workspace.ToolResult{{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "capability: read_symbol\nlanguage: go\nrouted_tool: go_read_symbol\nroute_summary: No Go symbols matched \"save\"",
		}},
	})

	if contract.PhaseID != contractcompile.TurnPhaseImplementationTargetSource {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want target_source after symbol miss; contract=%#v", contract.PhaseID, contract), 1)
	}

	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "code_navigate")
})

var _ = ginkgo.It("DeriveTurnPhaseContractUnscopedImplementationStaysTargetSourceAfterCandidateAnchorMiss", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "edit_file", "apply_patch"},
		},
		Results: []workspace.ToolResult{{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output: strings.Join([]string{
				"capability: read_symbol",
				"language: go",
				"routed_tool: go_read_symbol",
				`route_summary: Go symbol read path-filter miss; found 1 candidate anchor(s) for "Model.Update"`,
				"",
				"requested_symbol: Model.Update",
				"requested_path: internal/surfaces/tui/app/model_state.go",
				"path_filter_status: no source-body match in requested path",
				"candidate_anchor_status: anchors only; read one candidate path to inspect source body",
				"candidate_anchors:",
				"- internal/surfaces/tui/app/lifecycle.go:409 method Model.Update",
			}, "\n"),
		}},
	})

	if contract.PhaseID != contractcompile.TurnPhaseImplementationTargetSource {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want target_source after candidate-anchor miss; contract=%#v", contract.PhaseID, contract), 1)
	}

	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "code_navigate")
})

var _ = ginkgo.It("DeriveTurnPhaseContractUnscopedImplementationWriteReadyAfterSymbolBody", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "edit_file", "apply_patch"},
		},
		Results: []workspace.ToolResult{
			codeNavigateSourceWindowResult(
				"internal/workflows/productworkflow/coverage_gate.go",
				1,
				8,
				0,
				strings.Join([]string{
					"capability: read_symbol",
					"language: go",
					"routed_tool: go_read_symbol",
					"route_summary: Read EvaluateCoverageGate in internal/workflows/productworkflow/coverage_gate.go lines 1-8",
					"1:func EvaluateCoverageGate() bool {",
					"2:\treturn true",
					"3:}",
				}, "\n"),
			),
		},
	})

	if contract.Phase != "target_write_ready" {
		ginkgo.Fail(fmt.Sprintf("Phase = %q, want target_write_ready after symbol body; contract=%#v", contract.Phase, contract), 1)
	}

	assertContains(t, contract.AllowedTools, "edit_file")
	assertContains(t, contract.AllowedTools, "apply_patch")
	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "search_content")
	assertNotContains(t, contract.AllowedTools, "code_navigate")
})

var _ = ginkgo.It("DeriveTurnPhaseContractReadOnlyWorkspaceNamesEvidenceBeforeAnswerWhilePending", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "direct_answer",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"search_files", "search_content", "read_file", "edit_file", "shell"},
			Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: false, OpenObligations: []string{"evidence"}},
		},
	})

	if contract.Phase != "read_only_evidence_pending" {
		ginkgo.Fail(fmt.Sprintf("Phase = %q, want read_only_evidence_pending", contract.Phase), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionFinalAnswer)
	assertNotContains(t, contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
	assertContains(t, contract.TerminalOutcomes, "final answer")
	assertContains(t, contract.RequiredNext, "one bounded read/search for an exact unsupported claim, or <<QUESTION>> with the exact blocker; answer only if the gathered evidence supports every material claim or explicitly marks unsupported claims as unverified")
	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "search_content")
	assertNotContains(t, contract.AllowedTools, "edit_file")
	assertNotContains(t, contract.AllowedTools, "shell")
})

var _ = ginkgo.It("DeriveTurnPhaseContractReadOnlyConversationWithoutReadinessRequiresEvidence", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"search_content", "read_file", "edit_file"},
		},
	})

	if contract.Phase != "read_only_evidence_pending" {
		ginkgo.Fail(fmt.Sprintf("Phase = %q, want read_only_evidence_pending", contract.Phase), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionFinalAnswer)
	assertNotContains(t, contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "search_content")
	assertNotContains(t, contract.AllowedTools, "edit_file")
})

var _ = ginkgo.It("DeriveTurnPhaseContractReadOnlyDirectAnswerWithoutReadinessRequiresEvidence", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "direct_answer",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"search_content", "read_file", "edit_file"},
		},
	})

	if contract.Phase != "read_only_evidence_pending" {
		ginkgo.Fail(fmt.Sprintf("Phase = %q, want read_only_evidence_pending", contract.Phase), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionFinalAnswer)
	assertNotContains(t, contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "search_content")
	assertNotContains(t, contract.AllowedTools, "edit_file")
})

var _ = ginkgo.It("DeriveTurnPhaseContractReadOnlyOpenEvidenceBlocksFinalAnswer", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "direct_answer",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"search_content", "read_file", "edit_file"},
		},
		ResidualBlockers:   []string{"context_pack blocked by context freshness"},
		VerificationStatus: "failed",
	})

	if contract.Phase != "read_only_evidence_pending" {
		ginkgo.Fail(fmt.Sprintf("Phase = %q, want read_only_evidence_pending", contract.Phase), 1)
	}

	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionFinalAnswer)
	assertNotContains(t, contract.RuntimeCall.ExpressibleActionClasses, actionFinalAnswer)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
	assertNotContains(t, contract.ActionPolicy.TerminalResponseShapes, "final answer")
	assertNotContains(t, contract.TerminalOutcomes, "final answer")
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionBlocker)
})

var _ = ginkgo.It("DeriveTurnPhaseContractReadOnlyWorkspaceAllowsAnswerAfterEvidence", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "direct_answer",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain ShortLabel behavior in internal/labels/labels.go.",
			AllowedTools:      []string{"search_content", "read_file"},
			Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: false, OpenObligations: []string{"evidence"}},
		},
		Results: []workspace.ToolResult{
			readFileSourceWindowResult(
				"internal/labels/labels.go",
				1,
				5,
				5,
				"1:package labels\n2:\n3:func ShortLabel() string {\n4:\treturn \"Save changes\"\n5:}\n",
			),
		},
	})

	if contract.Phase != "read_only_evidence_available" {
		ginkgo.Fail(fmt.Sprintf("Phase = %q, want read_only_evidence_available after evidence", contract.Phase), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionFinalAnswer)
	assertNotContains(t, contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionGenericProbe)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
	assertNotContains(t, contract.ActionPolicy.InvalidActionClasses, actionRead)
	assertNotContains(t, contract.ActionPolicy.InvalidActionClasses, actionSearch)
})

var _ = ginkgo.It("DeriveCurrentTurnPhaseContractReadOnlyCompletionGateRejectionKeepsCorrectedFinalAnswerOpen", func() {
	t := ginkgo.GinkgoT()
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "What does internal/labels/labels.go return?",
		AllowedTools:      []string{"search_content", "read_file", "code_navigate"},
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult(
			"internal/labels/labels.go",
			1,
			5,
			0,
			"package labels\nfunc ShortLabel() string { return \"Save changes\" }\n",
		),
		{
			Tool:    "completion_gate",
			Summary: "Final answer rejected by blocker check",
			Error:   "the answer cites path(s) that were discovered as index candidates but not read as source-body evidence: `src/pages/book.astro`. Read the first unread path with one bounded `read_file` or `code_navigate` call and cite the actual evidence from the file body. If the path was from search or graph results and does not need a full read, qualify the claim as `unverified` next to the citation.",
		},
	}
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
	contract := runner.deriveCurrentTurnPhaseContract(runnerTurnPhaseContractInput{
		Brief:               brief,
		Results:             results,
		AllowedTools:        brief.AllowedTools,
		Options:             llama.ChatOptions{Model: "test-model"},
		SupportsNativeTools: true,
		ModelCallNumber:     22,
	})

	if contract.Phase != "read_only_evidence_available" {
		ginkgo.Fail(fmt.Sprintf("Phase = %q, want read_only_evidence_available", contract.Phase), 1)
	}

	if len(contract.EvidenceState.ResidualBlockers) > 0 {
		ginkgo.Fail(fmt.Sprintf("completion_gate feedback must not become residual evidence blockers: %#v", contract.EvidenceState.ResidualBlockers), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionFinalAnswer)
	assertContains(t, contract.RuntimeCall.ExpressibleActionClasses, actionFinalAnswer)
	assertNotContains(t, contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
	assertContains(t, contract.TerminalOutcomes, "final answer")
	assertContains(t, contract.ActionPolicy.TerminalResponseShapes, "final answer")
})

var _ = ginkgo.It("DeriveTurnPhaseContractReadOnlyConversationEvidenceAvailableKeepsProofReadSurface", func() {
	t := ginkgo.GinkgoT()
	input := TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			Objective:         "What does internal/labels/labels.go return?",
			AllowedTools:      []string{"search_content", "read_file", "code_navigate"},
		},
		Results: []workspace.ToolResult{
			readFileSourceWindowResult(
				"internal/labels/labels.go",
				1,
				5,
				0,
				"package labels\nfunc ShortLabel() string { return \"Save changes\" }\n",
			),
			{Tool: "code_navigate", Summary: "Code navigation routed references via go_references"},
		},
	}
	contract := deriveTurnPhaseContract(input)

	if contract.Phase != "read_only_evidence_available" {
		ginkgo.Fail(fmt.Sprintf("Phase = %q, want read_only_evidence_available", contract.Phase), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionFinalAnswer)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionBlocker)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionGenericProbe)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
	assertNotContains(t, contract.ActionPolicy.InvalidActionClasses, actionRead)
	assertNotContains(t, contract.ActionPolicy.InvalidActionClasses, actionSearch)
	assertEqualSet(t, contract.AllowedTools, []string{"search_content", "read_file", "code_navigate", "task"})
	assertEqualSet(t, contract.ActionPolicy.CallableToolNames, []string{"search_content", "read_file", "code_navigate", "task"})

	input.TextualToolParser = true
	input.ToolTransportMode = "textual"

	textual := deriveTurnPhaseContract(input)

	if textual.Phase != "read_only_evidence_available" {
		ginkgo.Fail(fmt.Sprintf("textual Phase = %q, want read_only_evidence_available", textual.Phase), 1)
	}

	assertContains(t, textual.ActionPolicy.AllowedActionClasses, actionGenericProbe)
	assertContains(t, textual.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, textual.ActionPolicy.AllowedActionClasses, actionSearch)
	assertNotContains(t, textual.ActionPolicy.InvalidActionClasses, actionRead)
	assertNotContains(t, textual.ActionPolicy.InvalidActionClasses, actionSearch)
	assertEqualSet(t, textual.AllowedTools, []string{"search_content", "read_file", "code_navigate", "task"})
	assertEqualSet(t, textual.ActionPolicy.CallableToolNames, []string{"search_content", "read_file", "code_navigate", "task"})
})

var _ = ginkgo.It("DeriveTurnPhaseContractRuntimeFieldsRoundTripToAutonomyEvent", func() {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "submit_plan",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"submit_plan"},
		},
		ToolTransportMode:      "textual",
		ParserContract:         "submit_plan_json",
		ResponseFormat:         "json",
		MaxTokens:              4096,
		TimeoutMillis:          300000,
		EffectiveFallbackState: "textual_submit_plan_fallback",
		TextualToolParser:      true,
	})

	if contract.RuntimeCall.TimeoutMillis != 300000 {
		ginkgo.Fail(fmt.Sprintf("TimeoutMillis = %d, want 300000", contract.RuntimeCall.TimeoutMillis), 1)
	}
	if contract.RuntimeCall.EffectiveFallbackState != "textual_submit_plan_fallback" {
		ginkgo.Fail(fmt.Sprintf("EffectiveFallbackState = %q", contract.RuntimeCall.EffectiveFallbackState), 1)
	}

	payload := contract.autonomyEventPayload()
	if payload.RuntimeCall.TimeoutMillis != 300000 ||
		payload.RuntimeCall.EffectiveFallbackState != "textual_submit_plan_fallback" ||
		payload.RuntimeCall.ParserContract != "submit_plan_json" {
		ginkgo.Fail(fmt.Sprintf("runtime call event did not round-trip fields: %#v", payload.RuntimeCall), 1)
	}

	if payload.LifecycleState.TypedPhase == "" ||
		payload.LifecycleState.CloseoutOwner == "" ||
		payload.LifecycleState.TimeoutState != "armed" ||
		len(payload.LifecycleState.EventOrdering) == 0 {
		ginkgo.Fail(fmt.Sprintf("lifecycle state event did not round-trip minimal fields: %#v", payload.LifecycleState), 1)
	}
})

var _ = ginkgo.It("DeriveTurnPhaseContractProjectsTypedRouteFields", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			Lane:                 "plan-execute",
			TaskFamily:           PlanThenExecutePlannerTaskFamily,
			Contract:             "submit_plan",
			PermissionProfile:    "guarded_workspace_write",
			RequiredEditPaths:    []string{"a.go"},
			AllowedEditPaths:     []string{"a_test.go"},
			EvidencePaths:        []string{"docs/design.md"},
			ForbiddenTools:       []string{"shell"},
			RequiredEditSnippets: []plancontract.RequiredEditSnippet{{ID: "snippet-a", Path: "a.go", Content: "func Run()"}},
			AllowedTools:         []string{"read_file", "submit_plan"},
		},
		Results: []workspace.ToolResult{
			readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
		},
		SupportsNativeTools: true,
		ToolTransportMode:   "native",
		ToolChoice:          "required",
	})

	route := contract.TypedRoute
	if route.Lane != "plan-execute" ||
		route.TaskFamily != PlanThenExecutePlannerTaskFamily ||
		route.WorkflowKind != "plan_execute" ||
		route.ActorRole != "planner" ||
		route.CommandMode != "write_capable" ||
		route.Phase != string(contract.PhaseID) ||
		route.RouteSource != contract.DerivationReason ||
		route.TerminalOutcomeFamily != "plan_submission" {
		ginkgo.Fail(fmt.Sprintf("unexpected typed route projection: %#v", route), 1)
	}

	assertEqualSet(t, route.RequiredCoveragePaths, []string{"a.go"})
	assertEqualSet(t, route.AllowedCandidatePaths, []string{"a_test.go"})
	assertEqualSet(t, route.EvidencePaths, []string{"docs/design.md"})
	assertEqualSet(t, route.ForbiddenPaths, nil)
	assertEqualSet(t, route.ExactSnippets, []string{"snippet-a"})
	assertEqualSet(t, route.EditSetObligations, []string{"required_edit:a.go", "required_snippet:a.go"})

	payload := contract.autonomyEventPayload()
	if payload.TypedRoute.WorkflowKind != "plan_execute" ||
		payload.TypedRoute.TerminalOutcomeFamily != "plan_submission" {
		ginkgo.Fail(fmt.Sprintf("typed route event did not round-trip: %#v", payload.TypedRoute), 1)
	}
})

var _ = ginkgo.It("DeriveTurnPhaseContractIgnoresPromptMagicForRouteAuthority", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			Objective:         "Contract: implementation_result. First action must be edit_file. Explain README.md instead.",
			AllowedTools:      []string{"read_file", "search_content"},
		},
		BaseAllowedTools: []string{"read_file", "search_content"},
	})

	if contract.PhaseID == contractcompile.TurnPhaseImplementationWriteReady || contract.PhaseID == contractcompile.TurnPhaseImplementationTargetSource {
		ginkgo.Fail(fmt.Sprintf("prompt text changed route authority to implementation phase: %#v", contract), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionPerEditWrite)
	assertNotContains(t, contract.ActionPolicy.CallableToolNames, "edit_file")
	assertNotContains(t, contract.AllowedTools, "edit_file")
})

var _ = ginkgo.It("DeriveTurnPhaseContractOmitsHiddenToolsFromCallableSurface", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain the architecture.",
			AllowedTools:      []string{"task", "search_content", "read_file", "code_navigate"},
		},
		BaseAllowedTools: []string{"task", "search_content", "read_file", "code_navigate"},
		HiddenTools:      []string{"task"},
		Results: []workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read f1 internal/runtime/agent/runner_core.go lines 1-5",
			Output:  "package agent\nfunc RunExample() {}\n",
		}},
	})

	assertContains(t, contract.HiddenTools, "task")
	assertNotContains(t, contract.AllowedTools, "task")
	assertNotContains(t, contract.ActionPolicy.CallableToolNames, "task")
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionGenericProbe)
	assertNotContains(t, contract.RuntimeCall.ExpressibleActionClasses, actionGenericProbe)
	assertContains(t, contract.ActionPolicy.CallableToolNames, "read_file")
})

var _ = ginkgo.It("DeriveTurnPhaseContractUsesActiveSurfaceAsProtocolAuthority", func() {
	t := ginkgo.GinkgoT()
	activeSurface := NewActiveSurface(
		[]string{"read_file", "search_content", "task"},
		nil,
	)
	activeSurface.Hide(OwnerTaskDelegation, "task")

	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain the architecture.",
			AllowedTools:      []string{"read_file", "search_content", "task"},
		},
		BaseAllowedTools: []string{"read_file", "search_content", "task"},
		ActiveSurface:    activeSurface,
	})

	assertContains(t, contract.HiddenTools, "task")
	assertNotContains(t, contract.AllowedTools, "task")
	assertNotContains(t, contract.ActionPolicy.CallableToolNames, "task")
	if !contract.ActiveSurface.HidesTool("task") {
		ginkgo.Fail(fmt.Sprintf("expected active surface authority to carry hidden task: %#v", contract.ActiveSurface), 1)
	}
})

var _ = ginkgo.It("DeriveTurnPhaseContractReadOnlyConversationKeepsToolsForUnreadNamedPath", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			Objective:         "Compare internal/labels/labels.go and internal/labels/labels_test.go.",
			AllowedTools:      []string{"search_content", "read_file", "code_navigate"},
			Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: false, OpenObligations: []string{"evidence"}},
		},
		Results: []workspace.ToolResult{
			{Tool: "read_file", Summary: "Read f1 internal/labels/labels.go lines 1-5"},
		},
	})

	if contract.Phase != "read_only_evidence_pending" {
		ginkgo.Fail(fmt.Sprintf("Phase = %q, want read_only_evidence_pending until every named path is read", contract.Phase), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionFinalAnswer)
	assertNotContains(t, contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
	assertContains(t, contract.AllowedTools, "read_file")
})

var _ = ginkgo.It("DeriveTurnPhaseContractReadOnlyWorkspaceStaysPendingAfterShallowDiscovery", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"search_files", "context_pack", "read_file"},
			Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: false, OpenObligations: []string{"evidence"}},
		},
		Results: []workspace.ToolResult{
			{Tool: "search_files", Summary: "Matched 1 file", Output: "internal/labels/labels.go"},
		},
	})

	if contract.Phase != "read_only_evidence_pending" {
		ginkgo.Fail(fmt.Sprintf("Phase = %q, want read_only_evidence_pending after shallow discovery; contract=%#v", contract.Phase, contract), 1)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionFinalAnswer)
	assertNotContains(t, contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
	assertContains(t, contract.AllowedTools, "read_file")
})

var _ = ginkgo.It("DeriveTurnPhaseContractProductAcceptanceBlocksFinalWithOpenEvidence", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		PhaseHint:          contractcompile.TurnPhaseProductAcceptanceCloseout,
		BaseAllowedTools:   []string{"read_file", "search_content"},
		TDDObligationRefs:  []string{"tdd:missing"},
		ResidualBlockers:   []string{"repair packet open"},
		VerificationStatus: "pre_write",
	})

	if contract.PhaseID != contractcompile.TurnPhaseProductAcceptanceCloseout {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, contractcompile.TurnPhaseProductAcceptanceCloseout), 1)
	}

	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionFinalAnswer)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
	assertEqualSet(t, contract.RuntimeCall.ExpressibleActionClasses, []string{actionBlocker})

	if len(contract.AllowedTools) != 0 {
		ginkgo.Fail(fmt.Sprintf("product closeout must not advertise tools, got %#v", contract.AllowedTools), 1)
	}

	assertEqualSet(t, contract.HiddenTools, []string{"read_file", "search_content"})
	assertContains(t, contract.EvidenceState.TDDObligationRefs, "tdd:missing")
})

var _ = ginkgo.It("DeriveTurnPhaseContractProductAcceptanceBlocksSkippedWorkflow", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		PhaseHint:            contractcompile.TurnPhaseProductAcceptanceCloseout,
		ProductWorkflowState: "skipped",
	})

	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionFinalAnswer)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
})

var _ = ginkgo.It("DeriveTurnPhaseContractArtifactFinalAnswerNoTools", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        workerruntime.ArtifactFinalAnswerTaskFamily,
			Contract:          "artifact_final_answer",
			PermissionProfile: "conversation_only",
		},
		BaseAllowedTools:    []string{"read_file", "search_content", "write_file", "project_lookup"},
		ToolTransportMode:   "textual",
		TextualToolParser:   true,
		SupportsNativeTools: false,
	})

	if contract.PhaseID != contractcompile.TurnPhaseArtifactFinalAnswer {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, contractcompile.TurnPhaseArtifactFinalAnswer), 1)
	}

	if len(contract.AllowedTools) != 0 || len(contract.ActionPolicy.CallableToolNames) != 0 {
		ginkgo.Fail(fmt.Sprintf("artifact final-answer must not advertise callable tools, got allowed=%#v callable=%#v", contract.AllowedTools, contract.ActionPolicy.CallableToolNames), 1)
	}

	assertContains(t, contract.HiddenTools, "project_lookup")
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionFinalAnswer)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionSubmitPlan)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionPerEditWrite)

	if contract.RuntimeCall.ExpectedVisibleOutput != "final_answer_artifact" {
		ginkgo.Fail(fmt.Sprintf("ExpectedVisibleOutput = %q, want final_answer_artifact", contract.RuntimeCall.ExpectedVisibleOutput), 1)
	}

	assertEqualSet(t, contract.RuntimeCall.ExpressibleActionClasses, []string{actionFinalAnswer, actionBlocker})
})

var _ = ginkgo.It("NextActionStateSurfaceSummaryReconcilesWithPhaseContract", func() {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "submit_plan"},
			RequiredEditPaths: []string{"a.go"},
		},
		Results: []workspace.ToolResult{
			readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
		},
		TextualToolParser: true,
	})
	state := applyTurnPhaseContractToNextActionState(runnerNextActionState{
		Phase: "generic",
		Step:  4,
	}, contract)

	state.NextValidActions = append([]string(nil), contract.TerminalOutcomes...)

	summary := turnPhaseSurfaceSummaryFromNextActionState(state, contract)
	issues := autonomy.ReconcileTurnPhaseSurface(contract.autonomyEventPayload(), summary)

	if len(issues) != 0 {
		ginkgo.Fail(fmt.Sprintf("next-action summary should reconcile cleanly, got %#v", issues), 1)
	}

	invalidActionClass := actionPerEditWrite
	if !slices.Contains(contract.ActionPolicy.InvalidActionClasses, invalidActionClass) {
		ginkgo.Fail(fmt.Sprintf("expected %s to be invalid in submit-ready contract: %#v", invalidActionClass, contract.ActionPolicy), 1)
	}

	if slices.Contains(summary.AdvertisedActionClasses, invalidActionClass) {
		ginkgo.Fail(fmt.Sprintf("summary advertised invalid action %s: %#v", invalidActionClass, summary), 1)
	}

	if !slices.Contains(summary.InvalidActionClasses, invalidActionClass) {
		ginkgo.Fail(fmt.Sprintf("summary did not project contract-invalid action %s as invalid: %#v", invalidActionClass, summary), 1)
	}

	issues = autonomy.ReconcileTurnPhaseSurface(contract.autonomyEventPayload(), summary)
	if len(issues) != 0 {
		ginkgo.Fail(fmt.Sprintf("contract-invalid guidance should not be advertised as callable, got %#v", issues), 1)
	}
})

var _ = ginkgo.It("WriteReadyNextActionStateDoesNotMarkAllowedWriteInvalid", func() {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "edit_file", "apply_patch"},
		},
		Results: []workspace.ToolResult{
			readFileSourceWindowResult("pkg/feature.go", 1, 20, 20, "1:package pkg\n"),
		},
		TextualToolParser: true,
		ToolTransportMode: "textual",
	})
	if contract.PhaseID != contractcompile.TurnPhaseImplementationWriteReady {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, contractcompile.TurnPhaseImplementationWriteReady), 1)
	}

	state := applyTurnPhaseContractToNextActionState(runnerNextActionState{
		Phase: "generic",
		Step:  4,
	}, contract)
	summary := turnPhaseSurfaceSummaryFromNextActionState(state, contract)

	if slices.Contains(summary.InvalidActionClasses, actionPerEditWrite) {
		ginkgo.Fail(fmt.Sprintf("next-action summary must not mark allowed write invalid, got %#v from invalid text %#v", summary.InvalidActionClasses, state.InvalidNextActions), 1)
	}

	if issues := autonomy.ReconcileTurnPhaseSurface(contract.autonomyEventPayload(), summary); len(issues) != 0 {
		ginkgo.Fail(fmt.Sprintf("write-ready next-action summary should reconcile cleanly, got %#v", issues), 1)
	}
})

var _ = ginkgo.It("RequiredSourceSelfSurfaceSummaryReconcilesWithPhaseContract", func() {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "submit_plan"},
			RequiredEditPaths: []string{"a.go"},
		},
	})

	issues := autonomy.ReconcileTurnPhaseSurface(
		contract.autonomyEventPayload(),
		autonomy.SurfaceSummaryFromTurnPhaseContract(contract.autonomyEventPayload()),
	)
	if len(issues) != 0 {
		ginkgo.Fail(fmt.Sprintf("required-source self-summary should reconcile cleanly, got %#v", issues), 1)
	}
})

var _ = ginkgo.It("RecoveryCardSurfaceSummaryReconcilesWithPhaseContract", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			PermissionProfile: "plan_submission_review",
			AllowedTools:      []string{"read_file", "search_content", "submit_plan"},
			Readiness: &workerruntime.ReadinessState{
				OpenObligations: []string{"source_sanity_failed:internal/example.go"},
			},
		},
	})
	card := BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:       contract,
		CurrentAllowedTools: []string{"submit_plan"},
		RejectedRequest:     workspace.ToolRequest{Tool: "completion_gate"},
		RejectedResult: workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Review-repair pass rejected",
			Error:   "failed obligations remain",
		},
	})

	summary := card.TurnPhaseSurfaceSummary(contract)
	issues := autonomy.ReconcileTurnPhaseSurface(contract.autonomyEventPayload(), summary)

	if len(issues) != 0 {
		ginkgo.Fail(fmt.Sprintf("recovery-card summary should reconcile cleanly, got %#v", issues), 1)
	}

	assertNotContains(t, card.CallableActionClasses, actionReviewPass)
	assertNotContains(t, card.TerminalOutcomes, "Decision: pass")
})

func assertContains(t ginkgo.FullGinkgoTInterface, values []string, want string) {
	t.Helper()

	if !slices.Contains(values, want) {
		ginkgo.Fail(fmt.Sprintf("expected %q in %#v", want, values), 1)
	}
}

func assertNotContains(t ginkgo.FullGinkgoTInterface, values []string, want string) {
	t.Helper()

	if slices.Contains(values, want) {
		ginkgo.Fail(fmt.Sprintf("did not expect %q in %#v", want, values), 1)
	}
}

func assertEqualSet(t ginkgo.FullGinkgoTInterface, got []string, want []string) {
	t.Helper()

	if len(got) != len(want) {
		ginkgo.Fail(fmt.Sprintf("got %#v, want set %#v", got, want), 1)
	}

	for _, value := range want {
		if !slices.Contains(got, value) {
			ginkgo.Fail(fmt.Sprintf("got %#v, want set containing %q", got, value), 1)
		}
	}
}

var _ = ginkgo.It("PlanExecuteGroundingRequiredRecoveryPlanFieldsDoNotDependOnPromptProse", func() {
	plan := planThenExecuteGroundingRequiredRecoveryPlan()

	if plan.Kind != PlanExecuteRecoveryKindGroundingRequired {
		ginkgo.Fail(fmt.Sprintf("Kind = %q", plan.Kind), 1)
	}

	if !slices.Equal(plan.TerminalOptions, []string{"Decision: source-satisfied", "<<QUESTION>>:"}) {
		ginkgo.Fail(fmt.Sprintf("TerminalOptions = %#v", plan.TerminalOptions), 1)
	}
	gomega.Expect(RenderPlanExecuteRecoveryPlan(plan)).NotTo(gomega.ContainSubstring("Allowed tools now:"))
})

var _ = ginkgo.It("PlanExecuteEmptySubmitPlanRecoveryReopensCandidateTools", func() {
	plan := planThenExecuteUnscopedEmptySubmitPlanRecoveryPlan()
	if plan.Kind != PlanExecuteRecoveryKindEmptySubmitPlan {
		ginkgo.Fail(fmt.Sprintf("Kind = %q", plan.Kind), 1)
	}

	if !slices.Contains(plan.TerminalOptions, "submit_plan") ||
		!slices.Contains(plan.TerminalOptions, "<<QUESTION>>:") {

		ginkgo.Fail(fmt.Sprintf("TerminalOptions = %#v", plan.TerminalOptions), 1)
	}
})

var _ = ginkgo.It("PlanExecuteSubmitPlanParseRecoveryPlanFieldsDoNotDependOnPromptProse", func() {
	plan := planThenExecuteSubmitPlanParseRecoveryPlan(true, []string{"a.go", "b.go"})

	if plan.Kind != PlanExecuteRecoveryKindSubmitPlanParse {
		ginkgo.Fail(fmt.Sprintf("Kind = %q", plan.Kind), 1)
	}

	if !slices.Equal(plan.TerminalOptions, []string{"submit_plan", "Decision: source-satisfied", "<<QUESTION>>:"}) {
		ginkgo.Fail(fmt.Sprintf("TerminalOptions = %#v", plan.TerminalOptions), 1)
	}
})

var _ = ginkgo.It("PlanExecuteSubmitPlanParseRecoveryKeepsInspectionCallable", func() {
	plan := planThenExecuteSubmitPlanParseRecoveryPlan(false, []string{"required.go"})
	rendered := RenderPlanExecuteRecoveryPlan(plan)
	gomega.Expect(rendered).To(gomega.ContainSubstring("permission-authorized inspection remains callable"))
	gomega.Expect(rendered).NotTo(gomega.ContainSubstring("Do not call read/search/discovery"))
})

var _ = ginkgo.It("PlanExecuteRepeatedCorrectionRecoveryDoesNotOfferTerminalEscape", func() {
	plan := planThenExecuteRepeatedSubmitCorrectionRecoveryPlan(workspace.ToolResult{
		Tool:  "submit_plan",
		Error: "invalid anchor for pkg/feature.go",
	})

	if !slices.Equal(plan.TerminalOptions, []string{"submit_plan"}) {
		ginkgo.Fail(fmt.Sprintf("TerminalOptions = %#v", plan.TerminalOptions), 1)
	}
})

var _ = ginkgo.It("PlanExecuteNativeNoCallFallbackSwitchesToTextualSubmitOnly", func() {
	plan := planThenExecuteNativeNoCallFallbackRecoveryPlan([]string{"pkg/feature.go"})

	if !slices.Equal(plan.TerminalOptions, []string{"submit_plan"}) {
		ginkgo.Fail(fmt.Sprintf("TerminalOptions = %#v", plan.TerminalOptions), 1)
	}
})

var _ = ginkgo.It("PlanExecuteRecoverySurfaceMetadataCharacterizesKnownSurfaces", func() {
	cases := []struct {
		name          string
		kind          PlanExecuteRecoverySurfaceKind
		wantMode      PlanExecutePlanningMode
		wantPhaseID   contractcompile.TurnPhaseID
		wantInvariant string
	}{
		{
			name:          "anchor refresh",
			kind:          PlanExecuteRecoverySurfaceAnchorRefresh,
			wantMode:      PlanExecutePlanningModeAnchorRefresh,
			wantPhaseID:   contractcompile.TurnPhaseStaleAnchorRefresh,
			wantInvariant: "planexecute.anchor_refresh",
		},
		{
			name:          "history pending",
			kind:          PlanExecuteRecoverySurfaceHistoryPending,
			wantMode:      PlanExecutePlanningModeHistoryPending,
			wantPhaseID:   contractcompile.TurnPhaseHistoryPending,
			wantInvariant: "planexecute.history_required",
		},
		{
			name:          "submit correction",
			kind:          PlanExecuteRecoverySurfaceSubmitCorrection,
			wantMode:      PlanExecutePlanningModeSubmitCorrection,
			wantPhaseID:   contractcompile.TurnPhaseSubmitReadyTextual,
			wantInvariant: "planexecute.submit_plan",
		},
		{
			name:          "submit ready",
			kind:          PlanExecuteRecoverySurfaceSubmitReady,
			wantMode:      PlanExecutePlanningModeSubmitReady,
			wantPhaseID:   contractcompile.TurnPhaseSubmitReadyTextual,
			wantInvariant: "planexecute.submit_ready",
		},
		{
			name:          "source satisfied terminal",
			kind:          PlanExecuteRecoverySurfaceSourceSatisfiedTerminal,
			wantMode:      PlanExecutePlanningModeSubmitReady,
			wantPhaseID:   contractcompile.TurnPhaseSourceSatisfiedTerminal,
			wantInvariant: "planexecute.source_satisfied_terminal",
		},
	}

	for _, tc := range cases {
		ginkgo.By(tc.name, func() {
			if got := recoveryModeFromSurface(tc.kind); got != tc.wantMode {
				ginkgo.Fail(fmt.Sprintf("mode = %q, want %q", got, tc.wantMode), 1)
			}
			if got := recoveryPhaseIDFromSurface(tc.kind); got != tc.wantPhaseID {
				ginkgo.Fail(fmt.Sprintf("phase = %q, want %q", got, tc.wantPhaseID), 1)
			}
			if got := recoveryStateFailedInvariantID(tc.kind, TurnPhaseContract{}); got != tc.wantInvariant {
				ginkgo.Fail(fmt.Sprintf("invariant = %q, want %q", got, tc.wantInvariant), 1)
			}
		})
	}
})

var _ = ginkgo.It("PlanExecuteRecoverySurfaceKindFromContractCharacterizesPhaseOverrides", func() {
	cases := []struct {
		name     string
		phaseID  contractcompile.TurnPhaseID
		fallback PlanExecuteRecoverySurfaceKind
		want     PlanExecuteRecoverySurfaceKind
	}{
		{
			name:     "stale anchor phase",
			phaseID:  contractcompile.TurnPhaseStaleAnchorRefresh,
			fallback: PlanExecuteRecoverySurfaceSubmitReady,
			want:     PlanExecuteRecoverySurfaceAnchorRefresh,
		},
		{
			name:     "history phase",
			phaseID:  contractcompile.TurnPhaseHistoryPending,
			fallback: PlanExecuteRecoverySurfaceSubmitReady,
			want:     PlanExecuteRecoverySurfaceHistoryPending,
		},
		{
			name:     "malformed submit-plan recovery",
			phaseID:  contractcompile.TurnPhaseMalformedSubmitPlanRecovery,
			fallback: PlanExecuteRecoverySurfaceSubmitCorrection,
			want:     PlanExecuteRecoverySurfaceSubmitReady,
		},
		{
			name:     "native submit ready",
			phaseID:  contractcompile.TurnPhaseSubmitReadyNativeRequired,
			fallback: PlanExecuteRecoverySurfaceNone,
			want:     PlanExecuteRecoverySurfaceSubmitReady,
		},
		{
			name:     "native submit ready preserves correction fallback",
			phaseID:  contractcompile.TurnPhaseSubmitReadyNativeRequired,
			fallback: PlanExecuteRecoverySurfaceSubmitCorrection,
			want:     PlanExecuteRecoverySurfaceSubmitCorrection,
		},
		{
			name:     "textual submit ready preserves correction fallback",
			phaseID:  contractcompile.TurnPhaseSubmitReadyTextual,
			fallback: PlanExecuteRecoverySurfaceSubmitCorrection,
			want:     PlanExecuteRecoverySurfaceSubmitCorrection,
		},
		{
			name:     "textual submit ready without correction fallback",
			phaseID:  contractcompile.TurnPhaseSubmitReadyTextual,
			fallback: PlanExecuteRecoverySurfaceNone,
			want:     PlanExecuteRecoverySurfaceSubmitReady,
		},
		{
			name:     "unknown phase",
			phaseID:  "future_phase",
			fallback: PlanExecuteRecoverySurfaceSourceSatisfiedTerminal,
			want:     PlanExecuteRecoverySurfaceSourceSatisfiedTerminal,
		},
	}

	for _, tc := range cases {
		ginkgo.By(tc.name, func() {
			got := recoverySurfaceKindFromContract(TurnPhaseContract{PhaseID: tc.phaseID}, tc.fallback)
			if got != tc.want {
				ginkgo.Fail(fmt.Sprintf("surface = %q, want %q", got, tc.want), 1)
			}
		})
	}
})

var _ = ginkgo.It("PlanExecuteRecoveryStateProjectsStaleAnchorFields", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go"},
	}
	rejected := workspace.ToolResult{
		Tool:    workspace.ToolNameSubmitPlan,
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "invalid edit anchor(s): `a.go: old_text not found in current source`.",
		Code:    workspace.SubmitPlanInvalidAnchorCode,
		Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
			Code:        workspace.SubmitPlanInvalidAnchorCode,
			Kind:        toolfacts.SubmitPlanDiagnosticInvalidAnchor,
			CanonicalID: "a.go",
		}),
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-40 (total lines: 40)", Output: "1:package a\n"},
		rejected,
	}
	req := workspace.ToolRequest{Tool: "read_file", Path: "a.go", StartLine: 1, EndLine: 40}
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		ContractID:       "contract-1",
		Brief:            brief,
		Results:          results,
		LastRequest:      req,
		LastResult:       rejected,
		BaseAllowedTools: []string{"read_file", workspace.ToolNameCodeNavigate, workspace.ToolNameSubmitPlan},
	})

	state, ok := derivePlanExecuteRecoveryState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, contract, req, rejected, planThenExecutePathAuthority{})
	if !ok {
		ginkgo.Fail("expected report-only recovery state", 1)
	}

	if state.SchemaVersion != planExecuteRecoveryStateSchemaVersion {
		ginkgo.Fail(fmt.Sprintf("schema = %q", state.SchemaVersion), 1)
	}
	if state.Surface != PlanExecuteRecoverySurfaceAnchorRefresh || state.Mode != PlanExecutePlanningModeAnchorRefresh {
		ginkgo.Fail(fmt.Sprintf("surface/mode = %q/%q, want anchor refresh", state.Surface, state.Mode), 1)
	}
	if state.ContractID != "contract-1" || state.PhaseID != contractcompile.TurnPhaseStaleAnchorRefresh {
		ginkgo.Fail(fmt.Sprintf("contract/phase = %q/%q", state.ContractID, state.PhaseID), 1)
	}
	if !strings.Contains(state.RecoveryCardProtocolProjectionID, state.ContractID) ||
		!strings.Contains(state.RecoveryCardProtocolProjectionID, string(state.PhaseID)) {
		ginkgo.Fail(fmt.Sprintf("protocol projection id should include contract and phase: %#v", state), 1)
	}
	if state.FailedInvariantID == "" || state.ActionClass != actionSubmitPlan || state.RejectedSubject != "a.go" {
		ginkgo.Fail(fmt.Sprintf("unexpected invariant/action/subject: %#v", state), 1)
	}
	if !strings.Contains(state.RecoveryID, string(contractcompile.TurnPhaseStaleAnchorRefresh)) || !strings.Contains(state.RecoveryID, "a.go") {
		ginkgo.Fail(fmt.Sprintf("recovery id missing phase/subject: %q", state.RecoveryID), 1)
	}
	if len(state.VisibleSafeAlternatives) == 0 {
		ginkgo.Fail(fmt.Sprintf("expected visible safe alternatives: %#v", state), 1)
	}
})

var _ = ginkgo.It("PlanExecuteRecoveryStateAbsentWithoutRecoverySurfaceOrRejectedResult", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implement",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go"},
	}
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		ContractID:       "contract-1",
		Brief:            brief,
		BaseAllowedTools: []string{"read_file", workspace.ToolNameCodeNavigate},
	})

	if state, ok := derivePlanExecuteRecoveryState(brief, nil, PlanExecutePlanningTransport{TextualToolParser: true}, contract, workspace.ToolRequest{}, workspace.ToolResult{}, planThenExecutePathAuthority{}); ok {
		ginkgo.Fail(fmt.Sprintf("unexpected recovery state: %#v", state), 1)
	}

	rejected := workspace.ToolResult{Tool: workspace.ToolNameSubmitPlan, Summary: "submit_plan rejected"}
	if state, ok := derivePlanExecuteRecoveryState(brief, []workspace.ToolResult{rejected}, PlanExecutePlanningTransport{TextualToolParser: true}, contract, workspace.ToolRequest{Tool: workspace.ToolNameSubmitPlan}, rejected, planThenExecutePathAuthority{}); ok {
		ginkgo.Fail(fmt.Sprintf("unexpected recovery state for non-plan-execute rejected result: %#v", state), 1)
	}
})

var _ = ginkgo.It("PlanExecuteRecoveryStateUsesActiveContractForSubmitCorrectionBlocker", func() {
	t := ginkgo.GinkgoT()
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", workspace.ToolNameSubmitPlan},
		RequiredEditPaths: []string{"a.go", "b.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)", Output: "1:package a\n"},
		{Tool: "read_file", Summary: "Read f2 b.go lines 1-10 (total lines: 10)", Output: "1:package b\n"},
		{
			Tool:    workspace.ToolNameSubmitPlan,
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; missing required edit path(s): `b.go`.",
		},
	}
	rejected := results[len(results)-1]
	req := workspace.ToolRequest{Tool: workspace.ToolNameSubmitPlan}
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		ContractID:        "contract-submit-correction",
		Brief:             brief,
		Results:           results,
		LastRequest:       req,
		LastResult:        rejected,
		BaseAllowedTools:  brief.AllowedTools,
		TextualToolParser: true,
	})

	state, ok := derivePlanExecuteRecoveryState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, contract, req, rejected, planThenExecutePathAuthority{})
	if !ok {
		ginkgo.Fail("expected submit-correction recovery state", 1)
	}
	if state.Surface != PlanExecuteRecoverySurfaceSubmitCorrection {
		ginkgo.Fail(fmt.Sprintf("surface = %q, want submit_correction; state=%#v", state.Surface, state), 1)
	}
	if state.TerminalBlockerAllowed {
		ginkgo.Fail(fmt.Sprintf("terminal blocker should follow active contract and stay disallowed: %#v", state), 1)
	}
	assertContains(t, state.VisibleSafeAlternatives, actionSubmitPlan)
	assertNotContains(t, state.VisibleSafeAlternatives, actionBlocker)
})

var _ = ginkgo.It("PlanExecuteRecoveryStateReportsSubmitCorrectionBlockerAllowed", func() {
	t := ginkgo.GinkgoT()
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", workspace.ToolNameSubmitPlan},
		RequiredEditPaths: []string{"native/src/simulation/world/collapse_event.h"},
	}
	rejection := workspace.ToolResult{
		Tool:    workspace.ToolNameSubmitPlan,
		Code:    planThenExecuteResultSubmitPlanInvalidAnchor,
		Summary: "submit_plan rejected",
		Error:   "edits[0]: whole-file replace_all edit would rewrite existing source file /workspace/Colony/native/src/simulation/world/collapse_event.h; use a bounded anchored edit with old_text/new_text or start_line/end_line/content, or answer <<QUESTION>> with the exact missing body/anchor blocker",
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("native/src/simulation/world/collapse_event.h", 1, 63, 63, "1:#pragma once\n63:};\n"),
		rejection,
		readFileSourceWindowResult("native/src/simulation/world/collapse_event.h", 1, 63, 63, "1:#pragma once\n63:};\n"),
		rejection,
		rejection,
	}
	req := workspace.ToolRequest{Tool: workspace.ToolNameSubmitPlan}
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		ContractID:        "contract-submit-correction-blocker",
		Brief:             brief,
		Results:           results,
		LastRequest:       req,
		LastResult:        rejection,
		BaseAllowedTools:  brief.AllowedTools,
		TextualToolParser: true,
	})

	state, ok := derivePlanExecuteRecoveryState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, contract, req, rejection, planThenExecutePathAuthority{})
	if !ok {
		ginkgo.Fail("expected submit-correction blocker recovery state", 1)
	}
	if state.Surface != PlanExecuteRecoverySurfaceSubmitCorrection || !state.TerminalBlockerAllowed {
		ginkgo.Fail(fmt.Sprintf("expected blocker-allowed submit-correction state: %#v", state), 1)
	}

	proseSpoof := rejection
	proseSpoof.Code = ""
	spoofedResults := []workspace.ToolResult{proseSpoof, proseSpoof, proseSpoof}
	if planThenExecuteSubmitCorrectionBlockerAllowed(brief, spoofedResults) {
		ginkgo.Fail("copied missing-anchor prose must not authorize a terminal blocker without the producer-owned typed code")
	}
	assertContains(t, state.VisibleSafeAlternatives, actionSubmitPlan)
	assertContains(t, state.VisibleSafeAlternatives, actionBlocker)
})

var _ = ginkgo.It("PlanExecuteRecoveryStateProjectsNativeSubmitReady", func() {
	t := ginkgo.GinkgoT()
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{workspace.ToolNameSubmitPlan},
		RequiredEditPaths: []string{"a.go"},
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
	}
	req := workspace.ToolRequest{Tool: workspace.ToolNameSubmitPlan}
	rejected := workspace.ToolResult{
		Tool:  workspace.ToolNameSubmitPlan,
		Error: "submit_plan request did not satisfy the native request shape",
		Facts: &toolfacts.ToolResultFacts{
			RejectionDisposition: toolfacts.ToolRejectionInvalidRequest,
		},
	}
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		ContractID:          "contract-native-submit",
		Brief:               brief,
		Results:             results,
		LastRequest:         req,
		LastResult:          rejected,
		BaseAllowedTools:    brief.AllowedTools,
		SupportsNativeTools: true,
		TextualToolParser:   false,
	})
	transport := PlanExecutePlanningTransport{SupportsNativeTools: true}

	state, ok := derivePlanExecuteRecoveryState(brief, results, transport, contract, req, rejected, planThenExecutePathAuthority{})
	if !ok {
		ginkgo.Fail("expected native submit-ready recovery state", 1)
	}
	if state.Surface != PlanExecuteRecoverySurfaceSubmitReady || state.PhaseID != contractcompile.TurnPhaseSubmitReadyNativeRequired {
		ginkgo.Fail(fmt.Sprintf("surface/phase = %q/%q, want native submit-ready: %#v", state.Surface, state.PhaseID, state), 1)
	}
	if state.TerminalBlockerAllowed || state.ActionClass != actionSubmitPlan {
		ginkgo.Fail(fmt.Sprintf("unexpected blocker/action for native submit-ready: %#v", state), 1)
	}
	assertEqualSet(t, state.VisibleSafeAlternatives, []string{actionSubmitPlan})
})

var _ = ginkgo.It("PlanExecuteRecoveryStateProjectsSourceSatisfiedTerminal", func() {
	t := ginkgo.GinkgoT()
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", workspace.ToolNameSubmitPlan},
		RequiredEditPaths: []string{"a.go"},
	}
	rejected := workspace.ToolResult{
		Tool:    workspace.ToolNameSubmitPlan,
		Summary: "submit_plan rejected: source-satisfied terminal shape required",
		Error:   "submit_plan cannot carry a source-satisfied/no-op closeout. Answer `Decision: source-satisfied` with per-path current-state evidence, submit real edits, or answer <<QUESTION>> with the exact blocker.",
		Code:    workspace.SubmitPlanSourceSatisfiedTerminalRequiredCode,
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
		rejected,
	}
	req := workspace.ToolRequest{Tool: workspace.ToolNameSubmitPlan}
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		ContractID:        "contract-source-satisfied",
		Brief:             brief,
		Results:           results,
		LastRequest:       req,
		LastResult:        rejected,
		BaseAllowedTools:  brief.AllowedTools,
		TextualToolParser: true,
	})

	state, ok := derivePlanExecuteRecoveryState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, contract, req, rejected, planThenExecutePathAuthority{})
	if !ok {
		ginkgo.Fail("expected source-satisfied terminal recovery state", 1)
	}
	if state.Surface != PlanExecuteRecoverySurfaceSourceSatisfiedTerminal || !state.TerminalBlockerAllowed {
		ginkgo.Fail(fmt.Sprintf("expected source-satisfied terminal with blocker allowed: %#v", state), 1)
	}
	assertEqualSet(t, state.VisibleSafeAlternatives, []string{actionSourceSatisfied, actionBlocker, actionRead, actionSearch})
})

var _ = ginkgo.It("PlanExecuteRecoveryStateProjectsMalformedSubmitPlanRecoveryPhase", func() {
	t := ginkgo.GinkgoT()
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", workspace.ToolNameSubmitPlan},
		RequiredEditPaths: []string{"a.go"},
	}
	rejected := workspace.ToolResult{
		Tool:    "tool_parse",
		Summary: "Malformed tool request rejected",
		Error:   "decode submit_plan tool request",
		Output:  `{"tool":"submit_plan","edits":"[]"}`,
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
		rejected,
	}
	req := workspace.ToolRequest{Tool: workspace.ToolNameSubmitPlan}
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		ContractID:        "contract-malformed-submit",
		Brief:             brief,
		Results:           results,
		LastRequest:       req,
		LastResult:        rejected,
		BaseAllowedTools:  brief.AllowedTools,
		TextualToolParser: true,
	})
	if contract.PhaseID != contractcompile.TurnPhaseMalformedSubmitPlanRecovery {
		ginkgo.Fail(fmt.Sprintf("phase = %q, want malformed recovery", contract.PhaseID), 1)
	}

	state, ok := derivePlanExecuteRecoveryState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, contract, req, rejected, planThenExecutePathAuthority{})
	if !ok {
		ginkgo.Fail("expected malformed submit-plan recovery state", 1)
	}
	if state.Surface != PlanExecuteRecoverySurfaceSubmitReady || state.PhaseID != contractcompile.TurnPhaseMalformedSubmitPlanRecovery {
		ginkgo.Fail(fmt.Sprintf("surface/phase = %q/%q, want submit-ready malformed projection: %#v", state.Surface, state.PhaseID, state), 1)
	}
	if state.ActionClass != actionSubmitPlan {
		ginkgo.Fail(fmt.Sprintf("malformed textual submit_plan should keep rejected action class submit_plan, got %#v", state), 1)
	}
	if !strings.Contains(state.RecoveryCardProtocolProjectionID, string(contractcompile.TurnPhaseMalformedSubmitPlanRecovery)) {
		ginkgo.Fail(fmt.Sprintf("projection should preserve malformed phase: %#v", state), 1)
	}
	if successfulSubmitPlanResultCount(results) != 0 {
		ginkgo.Fail("malformed submit_plan parse must not count as a plan artifact", 1)
	}
	assertContains(t, state.VisibleSafeAlternatives, actionSubmitPlan)
})

var _ = ginkgo.It("PlanExecuteRecoveryStateClassifiesSyntheticToolParseAsSubmitPlan", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", workspace.ToolNameSubmitPlan},
		RequiredEditPaths: []string{"a.go"},
	}
	rejected := workspace.ToolResult{
		Tool:    "tool_parse",
		Summary: "Malformed tool request rejected",
		Error:   "decode submit_plan tool request",
		Output:  `{"tool":"submit_plan","edits":"[]"}`,
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
		rejected,
	}
	req := workspace.ToolRequest{Tool: "tool_parse"}
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		ContractID:        "contract-synthetic-tool-parse",
		Brief:             brief,
		Results:           results,
		LastRequest:       req,
		LastResult:        rejected,
		BaseAllowedTools:  brief.AllowedTools,
		TextualToolParser: true,
	})

	state, ok := derivePlanExecuteRecoveryState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, contract, req, rejected, planThenExecutePathAuthority{})
	if !ok {
		ginkgo.Fail("expected malformed submit-plan recovery state", 1)
	}
	if state.PhaseID != contractcompile.TurnPhaseMalformedSubmitPlanRecovery || state.ActionClass != actionSubmitPlan {
		ginkgo.Fail(fmt.Sprintf("synthetic parser rejection should be classified as failed submit_plan, got %#v", state), 1)
	}
})

var _ = ginkgo.It("PlanExecuteRecoveryStateRepeatedCorrectionStaysSubmitPlanOnly", func() {
	t := ginkgo.GinkgoT()
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", workspace.ToolNameSubmitPlan},
		RequiredEditPaths: []string{"a.go"},
	}
	rejected := workspace.ToolResult{
		Tool:    workspace.ToolNameSubmitPlan,
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "submit_plan must use edits[] for changed required paths; missing required edit path(s): `a.go`.",
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)", Output: "1:package a\n"},
		rejected,
		rejected,
	}
	req := workspace.ToolRequest{Tool: workspace.ToolNameSubmitPlan}
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		ContractID:        "contract-repeated-submit-correction",
		Brief:             brief,
		Results:           results,
		LastRequest:       req,
		LastResult:        rejected,
		BaseAllowedTools:  brief.AllowedTools,
		TextualToolParser: true,
	})

	state, ok := derivePlanExecuteRecoveryState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, contract, req, rejected, planThenExecutePathAuthority{})
	if !ok {
		ginkgo.Fail("expected repeated submit-correction recovery state", 1)
	}
	if state.Surface != PlanExecuteRecoverySurfaceSubmitCorrection || state.TerminalBlockerAllowed {
		ginkgo.Fail(fmt.Sprintf("expected submit-correction without terminal escape: %#v", state), 1)
	}
	assertEqualSet(t, state.VisibleSafeAlternatives, []string{actionSubmitPlan})
	if successfulSubmitPlanResultCount(results) != 0 {
		ginkgo.Fail("rejected submit_plan attempts must not count as plan artifacts", 1)
	}
})

var _ = ginkgo.It("PlanExecuteRecoveryStateHiddenOnlyCardUsesSameSubmitCorrectionState", func() {
	t := ginkgo.GinkgoT()
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{workspace.ToolNameSubmitPlan},
		RequiredEditPaths: []string{"a.go"},
	}
	rejected := workspace.ToolResult{
		Tool:    workspace.ToolNameSubmitPlan,
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "submit_plan must use edits[] for changed required paths; missing required edit path(s): `a.go`.",
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)", Output: "1:package a\n"},
		rejected,
	}
	req := workspace.ToolRequest{Tool: workspace.ToolNameSubmitPlan}
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		ContractID:        "contract-hidden-only-submit-correction",
		Brief:             brief,
		Results:           results,
		LastRequest:       req,
		LastResult:        rejected,
		BaseAllowedTools:  brief.AllowedTools,
		TextualToolParser: true,
	})
	state, ok := derivePlanExecuteRecoveryState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, contract, req, rejected, planThenExecutePathAuthority{})
	if !ok {
		ginkgo.Fail("expected submit-correction recovery state", 1)
	}

	card := BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:       contract,
		CurrentAllowedTools: []string{workspace.ToolNameSubmitPlan},
		RejectedRequest:     req,
		RejectedResult:      rejected,
		RecoveryState:       &state,
	})
	prompt := planThenExecuteSubmitCorrectionHiddenOnlyVisiblePromptForState(card.Render(), brief, results)
	if !strings.Contains(prompt, state.RecoveryID) || !strings.Contains(prompt, "hidden-only output") {
		ginkgo.Fail(fmt.Sprintf("hidden-only prompt should wrap the same typed recovery card, got:\n%s", prompt), 1)
	}
	if len(card.CallableTools) > 0 {
		ginkgo.Fail(fmt.Sprintf("textual hidden-only submit correction should not expose native callable tools: %#v", card.CallableTools), 1)
	}
	assertEqualSet(t, card.VisibleSafeAlternatives, []string{actionSubmitPlan})
})

var _ = ginkgo.It("PlanExecuteRuntimeCallPolicyNativeSubmitReady", func() {
	t := ginkgo.GinkgoT()
	opts := llama.ChatOptions{
		Tools: []llama.Tool{{
			Function: llama.ToolFunction{Name: "submit_plan"},
		}},
		ToolChoice: "required",
		MaxTokens:  4096,
	}

	policy := PlanExecuteRuntimeCallPolicyFromChatOptions(opts, true, true)
	if policy.TransportMode != ToolCallTransportModeNative {
		ginkgo.Fail(fmt.Sprintf("TransportMode = %q, want %q", policy.TransportMode, ToolCallTransportModeNative), 1)
	}

	if policy.TextualToolParser {
		ginkgo.Fail("native submit-ready policy should not use textual parser", 1)
	}

	if policy.ResponseFormatJSON {
		ginkgo.Fail("native submit-ready policy should not request JSON response format", 1)
	}

	if !policy.ToolChoiceRequired || policy.ToolChoice != "required" {
		ginkgo.Fail(fmt.Sprintf("tool choice not preserved: %#v", policy), 1)
	}

	if policy.MaxTokens != 4096 {
		ginkgo.Fail(fmt.Sprintf("MaxTokens = %d, want 4096", policy.MaxTokens), 1)
	}

	assertEqualSet(t, policy.ExpressibleActionClasses, []string{actionSubmitPlan})
})

var _ = ginkgo.It("PlanExecuteRuntimeCallPolicyTextualJSONSubmitReady", func() {
	t := ginkgo.GinkgoT()
	opts := llama.ChatOptions{
		ResponseFormatJSON: true,
		MaxTokens:          4096,
	}

	policy := PlanExecuteRuntimeCallPolicyFromChatOptions(opts, false, true)
	if policy.TransportMode != ToolCallTransportModeTextualJSON {
		ginkgo.Fail(fmt.Sprintf("TransportMode = %q, want %q", policy.TransportMode, ToolCallTransportModeTextualJSON), 1)
	}

	if !policy.TextualToolParser {
		ginkgo.Fail("textual JSON submit-ready policy should use textual parser", 1)
	}

	if !policy.ResponseFormatJSON {
		ginkgo.Fail("textual JSON submit-ready policy should preserve response format", 1)
	}

	if policy.MaxTokens != 4096 {
		ginkgo.Fail(fmt.Sprintf("MaxTokens = %d, want 4096", policy.MaxTokens), 1)
	}

	assertEqualSet(t, policy.ExpressibleActionClasses, []string{actionSourceSatisfied, actionBlocker, actionFinalAnswer})
})

var _ = ginkgo.It("PlanExecuteRuntimeCallPolicyForceNativeOverridesBackendDefault", func() {
	opts := llama.ChatOptions{
		Tools: []llama.Tool{{
			Function: llama.ToolFunction{Name: "submit_plan"},
		}},
		ToolChoice:       "required",
		ForceNativeTools: true,
		MaxTokens:        4096,
	}

	policy := PlanExecuteRuntimeCallPolicyFromChatOptions(opts, false, true)
	if policy.TransportMode != ToolCallTransportModeNative {
		ginkgo.Fail(fmt.Sprintf("TransportMode = %q, want %q", policy.TransportMode, ToolCallTransportModeNative), 1)
	}

	if policy.TextualToolParser {
		ginkgo.Fail("forced native submit-ready policy should not use textual parser", 1)
	}

	if policy.ToolTransportMode() != "native" {
		ginkgo.Fail(fmt.Sprintf("ToolTransportMode = %q, want native", policy.ToolTransportMode()), 1)
	}
})

var _ = ginkgo.It("PlanExecuteRuntimeCallPolicyMapsToGenericPolicy", func() {
	t := ginkgo.GinkgoT()
	thinkingOff := false
	policy := DerivePlanExecuteRuntimeCallPolicy(RuntimeCallPolicyInput{
		SupportsNativeTools:      true,
		ParserContract:           " submit_plan_json ",
		ResponseFormat:           " json ",
		Temperature:              0.2,
		TopP:                     1,
		TopK:                     20,
		MinP:                     0.05,
		PresencePenalty:          0.1,
		RepetitionPenalty:        1.1,
		ToolChoice:               " required ",
		ThinkingPolicy:           " disabled_visible ",
		EnableThinking:           &thinkingOff,
		PreserveThinking:         false,
		ThinkingBudget:           64,
		ChatTemplateKwargs:       true,
		MaxTokens:                1024,
		TimeoutMillis:            1500,
		ExpressibleActionClasses: []string{actionSubmitPlan, actionSourceSatisfied, actionBlocker},
	})

	if policy.TransportMode != ToolCallTransportModeTextualJSON {
		ginkgo.Fail(fmt.Sprintf("TransportMode = %q, want %q", policy.TransportMode, ToolCallTransportModeTextualJSON), 1)
	}

	if policy.ToolTransportMode() != "textual" {
		ginkgo.Fail(fmt.Sprintf("ToolTransportMode = %q, want textual", policy.ToolTransportMode()), 1)
	}

	if policy.ParserContract != "submit_plan_json" {
		ginkgo.Fail(fmt.Sprintf("ParserContract = %q, want submit_plan_json", policy.ParserContract), 1)
	}

	if policy.ResponseFormatMode() != "json" {
		ginkgo.Fail(fmt.Sprintf("ResponseFormatMode = %q, want json", policy.ResponseFormatMode()), 1)
	}

	if policy.ToolChoice != "required" || !policy.ToolChoiceRequired {
		ginkgo.Fail(fmt.Sprintf("ToolChoice mismatch: %q/%v", policy.ToolChoice, policy.ToolChoiceRequired), 1)
	}

	if policy.ThinkingPolicy != "disabled_visible" || policy.MaxTokens != 1024 || policy.TimeoutMillis != 1500 {
		ginkgo.Fail(fmt.Sprintf("runtime limits mismatch: %#v", policy), 1)
	}

	if policy.Temperature != 0.2 || policy.TopP != 1 || policy.TopK != 20 || policy.MinP != 0.05 || policy.PresencePenalty != 0.1 || policy.RepetitionPenalty != 1.1 {
		ginkgo.Fail(fmt.Sprintf("runtime sampling mismatch: %#v", policy), 1)
	}

	assertEqualSet(t, policy.ExpressibleActionClasses, []string{actionSubmitPlan, actionSourceSatisfied, actionBlocker})
})

var _ = ginkgo.It("PlanExecuteRuntimeCallPolicySourceRefreshKeepsSmallVisibleToolBudget", func() {
	thinkingOn := true
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go", "b.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "route_summary: Read A in a.go lines 1-10\n\n1:func A() {}\n"},
		{Tool: "code_navigate", Summary: "Code navigation routed file_outline via go_file_outline", Output: "route_summary: Outlined 2 top-level declaration(s) in b.go.\n"},
	}
	opts := planThenExecuteUnreadRequiredSourceChatOptions(llama.ChatOptions{
		Tools: []llama.Tool{
			{Function: llama.ToolFunction{Name: "read_file"}},
			{Function: llama.ToolFunction{Name: "code_navigate"}},
			{Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        16384,
	}, brief, results, planThenExecutePathAuthority{})

	policy := PlanExecuteRuntimeCallPolicyFromChatOptions(opts, true, false)
	if policy.TransportMode != ToolCallTransportModeNative {
		ginkgo.Fail(fmt.Sprintf("source-refresh transport = %q, want %q", policy.TransportMode, ToolCallTransportModeNative), 1)
	}

	if policy.ResponseFormatJSON {
		ginkgo.Fail("source-refresh policy should not request JSON response format", 1)
	}

	if policy.MaxTokens != 768 {
		ginkgo.Fail(fmt.Sprintf("source-refresh MaxTokens = %d, want 768", policy.MaxTokens), 1)
	}

	if opts.EnableThinking == nil || *opts.EnableThinking {
		ginkgo.Fail("source-refresh policy should force visible no-thinking tool output", 1)
	}
})

var _ = ginkgo.It("PlanExecuteRuntimeModelCallTelemetryIsBounded", func() {
	t := ginkgo.GinkgoT()
	thinkingOff := false

	var event RunEvent

	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
		OnEvent: func(emitted RunEvent) {
			event = emitted
		},
	})

	runner.emitModelCallTraceWithOptions(
		3,
		"main_loop",
		[]llama.ChatMessage{{Role: "user", Content: "plan edits"}},
		llama.ChatMessage{
			Content:      "{}",
			FinishReason: "length",
			Usage: llama.Usage{
				PromptTokens:               12,
				CompletionTokens:           7,
				TotalTokens:                19,
				PromptCacheHitTokens:       8,
				PromptCacheMissTokens:      4,
				PromptCacheAccountingKnown: true,
				ReasoningTokens:            3,
			},
		},
		250,
		llama.ChatOptions{
			Model:              "/private/models/qwen-local.gguf",
			ResponseFormatJSON: true,
			EnableThinking:     &thinkingOff,
			ThinkingBudget:     512,
			ChatTemplateKwargs: true,
			Temperature:        0.2,
			TopP:               1,
			TopK:               20,
			MinP:               0.05,
			PresencePenalty:    0.1,
			RepetitionPenalty:  1.1,
			MaxTokens:          4096,
			Tools: []llama.Tool{{
				Function: llama.ToolFunction{Name: workspace.ToolNameSubmitPlan},
			}},
		},
		false,
		nil,
	)

	if event.Role != "model_call" {
		ginkgo.Fail(fmt.Sprintf("event role = %q, want model_call", event.Role), 1)
	}

	var payload map[string]any

	if err := json.Unmarshal([]byte(event.Content), &payload); err != nil {
		ginkgo.Fail(fmt.Sprintf("decode model_call event: %v", err), 1)
	}

	assertStringField(t, payload, "model_alias", "qwen-local.gguf")
	assertStringField(t, payload, "runtime_transport_mode", string(ToolCallTransportModeTextualJSON))
	assertStringField(t, payload, "response_format", "json")
	assertStringField(t, payload, "response_format_mode", "json")
	assertStringField(t, payload, "finish_reason", "length")
	assertNumberField(t, payload, "prompt_tokens", 12)
	assertNumberField(t, payload, "completion_tokens", 7)
	assertNumberField(t, payload, "total_tokens", 19)
	assertNumberField(t, payload, "prompt_cache_hit_tokens", 8)
	assertNumberField(t, payload, "prompt_cache_miss_tokens", 4)
	assertNumberField(t, payload, "reasoning_tokens", 3)

	if got, ok := payload["prompt_cache_accounting_known"].(bool); !ok || !got {
		ginkgo.Fail(fmt.Sprintf("prompt_cache_accounting_known = %#v, want true", payload["prompt_cache_accounting_known"]), 1)
	}

	assertNumberField(t, payload, "effective_max_tokens", 4096)
	assertNumberField(t, payload, "temperature", 0.2)
	assertNumberField(t, payload, "top_p", 1)
	assertNumberField(t, payload, "top_k", 20)
	assertNumberField(t, payload, "min_p", 0.05)
	assertNumberField(t, payload, "presence_penalty", 0.1)
	assertNumberField(t, payload, "repetition_penalty", 1.1)
	assertNumberField(t, payload, "thinking_budget", 512)

	gotClasses, ok := payload["expressible_action_classes"].([]any)
	if !ok {
		ginkgo.Fail(fmt.Sprintf("model_call telemetry missing expressible_action_classes in %#v", payload), 1)
	}
	if len(gotClasses) == 0 {
		ginkgo.Fail(fmt.Sprintf("expressible_action_classes should not be empty in %#v", payload), 1)
	}

	if got, ok := payload["thinking_enabled"].(bool); !ok || got {
		ginkgo.Fail(fmt.Sprintf("thinking_enabled = %#v, want false", payload["thinking_enabled"]), 1)
	}

	if got, ok := payload["chat_template_kwargs"].(bool); !ok || !got {
		ginkgo.Fail(fmt.Sprintf("chat_template_kwargs = %#v, want true", payload["chat_template_kwargs"]), 1)
	}

	for _, key := range []string{
		"backend_capability_native_tools",
		"backend_capability_json_object",
		"backend_capability_strict_json_schema",
		"backend_capability_grammar",
		"backend_capability_thinking_controls",
		"backend_capability_request_timeout_telemetry",
	} {
		if _, ok := payload[key].(bool); !ok {
			ginkgo.Fail(fmt.Sprintf("model_call telemetry missing boolean backend capability %q in %#v", key, payload), 1)
		}
	}

	if strings.Contains(event.Content, "/private/models") {
		ginkgo.Fail(fmt.Sprintf("model_call telemetry leaked raw model path: %s", event.Content), 1)
	}
})

var _ = ginkgo.It("ToolRequestNormalizerClassifiesParserFailure", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{ToolCalls: []llama.ToolCall{{
		Function: llama.ToolCallFunc{
			Name:      "submit_plan",
			Arguments: `{"edits":`,
		},
	}}})
	if result.OK {
		ginkgo.Fail("malformed native tool arguments should not parse", 1)
	}

	if result.ParserFailureClass != autonomy.ParserFailureMalformedJSON {
		ginkgo.Fail(fmt.Sprintf("ParserFailureClass = %q, want %q", result.ParserFailureClass, autonomy.ParserFailureMalformedJSON), 1)
	}

	if result.EventTransport != toolRequestNormalizerTransportNative {
		ginkgo.Fail(fmt.Sprintf("EventTransport = %q, want native", result.EventTransport), 1)
	}
})

var _ = ginkgo.It("ToolRequestNormalizerClassifiesNativeTransportMismatch", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{ToolCalls: []llama.ToolCall{{
		Function: llama.ToolCallFunc{
			Name:      workspace.ToolNameCodeNavigate,
			Arguments: `{"tool":"context_query","query":"callers of Run"}`,
		},
	}}})
	if result.OK {
		ginkgo.Fail("mismatched native tool arguments should not parse", 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeTransportMismatch {
		ginkgo.Fail(fmt.Sprintf("EventOutcome = %q, want %q", result.EventOutcome, toolRequestNormalizerOutcomeTransportMismatch), 1)
	}

	if result.ParserFailureClass != autonomy.ParserFailureTransportMismatch {
		ginkgo.Fail(fmt.Sprintf("ParserFailureClass = %q, want %q", result.ParserFailureClass, autonomy.ParserFailureTransportMismatch), 1)
	}

	if result.EventTransport != toolRequestNormalizerTransportNative {
		ginkgo.Fail(fmt.Sprintf("EventTransport = %q, want native", result.EventTransport), 1)
	}

	input := runnerAutonomyPolicyInputFromNormalization(result)
	if input.Transport != autonomy.ToolTransportNativeCall {
		ginkgo.Fail(fmt.Sprintf("policy transport = %q, want native tool call", input.Transport), 1)
	}
	if input.ParserFailure != autonomy.ParserFailureTransportMismatch {
		ginkgo.Fail(fmt.Sprintf("policy parser failure = %q, want transport mismatch", input.ParserFailure), 1)
	}
})

var _ = ginkgo.It("ToolRequestNormalizerRejectsMissingToolInToolTransport", func() {
	tests := []struct {
		name          string
		content       string
		wantOK        bool
		wantTool      string
		wantOutcome   string
		wantTransport string
	}{
		{
			name:          "read shape missing tool is unsupported",
			content:       "```tool\n{\"path\":\"internal/runtime/agent/plan_execute.go\"}\n```",
			wantOK:        false,
			wantOutcome:   toolRequestNormalizerOutcomeUnsupportedShape,
			wantTransport: toolRequestNormalizerTransportTextual,
		},
		{
			name:          "write shape missing tool is unsupported",
			content:       "```tool\n{\"path\":\"README.md\",\"old_text\":\"old\",\"new_text\":\"new\"}\n```",
			wantOK:        false,
			wantOutcome:   toolRequestNormalizerOutcomeUnsupportedShape,
			wantTransport: toolRequestNormalizerTransportTextual,
		},
		{
			name:          "submit plan shape missing tool is unsupported",
			content:       "```tool\n{\"edits\":[{\"path\":\"README.md\",\"old_text\":\"old\",\"new_text\":\"new\"}]}\n```",
			wantOK:        false,
			wantOutcome:   toolRequestNormalizerOutcomeUnsupportedShape,
			wantTransport: toolRequestNormalizerTransportTextual,
		},
		{
			name:          "prompt-like prose is unsafe",
			content:       "Please make the change.\npath: README.md\nold_text: old\nnew_text: new",
			wantOK:        false,
			wantOutcome:   toolRequestNormalizerOutcomeUnsafeToInfer,
			wantTransport: toolRequestNormalizerTransportKeyValue,
		},
	}

	for _, tt := range tests {
		ginkgo.By(tt.name, func() {
			result := normalizeToolRequestSyntax(llama.ChatMessage{Content: tt.content})
			if result.OK != tt.wantOK {
				ginkgo.Fail(fmt.Sprintf("OK = %v, want %v; result=%#v", result.OK, tt.wantOK, result), 1)
			}
			if tt.wantTool != "" && result.Request.Tool != tt.wantTool {
				ginkgo.Fail(fmt.Sprintf("Tool = %q, want %q", result.Request.Tool, tt.wantTool), 1)
			}
			if tt.wantTool == "" && result.Request.Tool != "" {
				ginkgo.Fail(fmt.Sprintf("missing-tool payload must not infer executable tool %q", result.Request.Tool), 1)
			}
			if result.EventOutcome != tt.wantOutcome {
				ginkgo.Fail(fmt.Sprintf("EventOutcome = %q, want %q; result=%#v", result.EventOutcome, tt.wantOutcome, result), 1)
			}
			if result.EventTransport != tt.wantTransport {
				ginkgo.Fail(fmt.Sprintf("EventTransport = %q, want %q", result.EventTransport, tt.wantTransport), 1)
			}
		})
	}
})

var _ = ginkgo.It("ToolRequestNormalizerDoesNotInferWhenRequiredArgumentsAreAbsent", func() {
	tests := []string{
		`{"start_line":1,"end_line":20}`,
		`{"path":""}`,
		`{"query":""}`,
		`{"capability":"read_symbol"}`,
	}
	for _, content := range tests {
		ginkgo.By(content, func() {
			result := normalizeToolRequestSyntax(llama.ChatMessage{Content: content})
			if result.OK {
				ginkgo.Fail(fmt.Sprintf("missing required args must not infer a tool request, got %#v", result), 1)
			}
		})
	}
})

var _ = ginkgo.It("ToolRequestNormalizerParsesIdiomToolCLIShape", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{Content: `idiom tool task {"tool":"task","agent":"explorer","mode":"read_only","prompt":"Inspect subagent flow."}`})
	if !result.OK {
		ginkgo.Fail(fmt.Sprintf("expected idiom tool CLI shape to parse, outcome=%s raw=%s", result.EventOutcome, result.Raw), 1)
	}

	if result.EventTransport != toolRequestNormalizerTransportIdiomCLI {
		ginkgo.Fail(fmt.Sprintf("EventTransport = %q, want %q", result.EventTransport, toolRequestNormalizerTransportIdiomCLI), 1)
	}

	if result.Request.Tool != "task" || result.Request.Agent != "explorer" || result.Request.Mode != "read_only" {
		ginkgo.Fail(fmt.Sprintf("unexpected parsed request: %#v", result.Request), 1)
	}
})

func assertStringField(t ginkgo.FullGinkgoTInterface, payload map[string]any, key string, want string) {
	t.Helper()

	if got, ok := payload[key].(string); !ok || got != want {
		ginkgo.Fail(fmt.Sprintf("%s = %#v, want %q", key, payload[key], want), 1)
	}
}

func assertNumberField(t ginkgo.FullGinkgoTInterface, payload map[string]any, key string, want float64) {
	t.Helper()

	if got, ok := payload[key].(float64); !ok || got != want {
		ginkgo.Fail(fmt.Sprintf("%s = %#v, want %v", key, payload[key], want), 1)
	}
}

var _ = ginkgo.It("RecoveryCardRendersZeroHit", func() {
	t := ginkgo.GinkgoT()
	got := RenderRecoveryCard(RecoveryCard{
		ContractID:  "contract-7",
		PhaseID:     contractcompile.TurnPhaseCandidateSource,
		InvariantID: "evidence.zero_hit_not_absence",
		EvidenceRefs: []string{
			"tool_result:search_content:step_3",
			"tool_result:search_content:step_3",
		},
		RejectedAction: RecoveryRejectedAction{
			Tool:        "search_content",
			ActionClass: actionSearch,
			Target:      "MissingSymbol",
			Reason:      "zero hits",
		},
		Invariant: "A zero-hit search is not source-body evidence for absence.",
		InvalidRepeats: []string{
			"repeat the same zero-hit search",
			"broaden the query without naming one missing claim",
		},
		CallableTools:         []string{"context_pack", "read_file", "code_navigate", "search_content"},
		CallableActionClasses: []string{actionRead, actionSearch, actionBlocker},
		ExactBlockerShape:     "<<QUESTION>>: Cannot verify <claim> because the search returned zero candidate paths and no source anchor is available.",
	})

	assertRecoveryCardContains(t, got,
		"Recovery card",
		"schema: idiom.recovery_card.v1",
		"contract_id=contract-7",
		"phase_id=plan_execute.candidate_source",
		"invariant_id=evidence.zero_hit_not_absence",
		"evidence: tool_result:search_content:step_3",
		"rejected action: tool=search_content action_class=search target=MissingSymbol reason=\"zero hits\"",
		"invariant: evidence.zero_hit_not_absence: A zero-hit search is not source-body evidence for absence.",
		"invalid repeats: repeat the same zero-hit search, broaden the query without naming one missing claim",
		"currently callable tools: context_pack, read_file, code_navigate, search_content",
		"currently callable action classes: read, search, blocker",
		"exact blocker shape: <<QUESTION>>: State the exact blocker and cite the concrete evidence source, path, command, or capability that is unavailable or failed.",
	)
	assertRecoveryCardOmitsProjectWorkflowTerms(t, got)

	if strings.Contains(got, "deterministic safe next action") {
		ginkgo.Fail(fmt.Sprintf("recovery card should not synthesize strategy text:\n%s", got), 1)
	}

	if strings.Count(got, "tool_result:search_content:step_3") != 1 {
		ginkgo.Fail(fmt.Sprintf("expected duplicate evidence refs to be collapsed, got:\n%s", got), 1)
	}
})

var _ = ginkgo.It("RecoveryCardRendersRepeatedReadDeadEnd", func() {
	t := ginkgo.GinkgoT()
	got := RenderRecoveryCard(RecoveryCard{
		PhaseID:     contractcompile.TurnPhaseRequiredSource,
		InvariantID: "loop.repeated_dead_end",
		EvidenceRefs: []string{
			"tool_result:read_file:step_5",
			"tool_result:read_file:step_6",
		},
		RejectedAction: RecoveryRejectedAction{
			Tool:        "read_file",
			ActionClass: actionRead,
			Target:      "internal/example.go",
			Reason:      "same source body already read",
		},
		Invariant:             "Repeated reads of the same evidence do not close a new claim.",
		InvalidRepeats:        []string{"read the same path and range again", "ask for another generic probe without a named gap"},
		CallableTools:         []string{"read_file", "code_navigate"},
		CallableActionClasses: []string{actionRead, actionBlocker},
		ExactBlockerShape:     "<<QUESTION>>: Cannot resolve <claim> because <specific source range or capability> is unavailable after the repeated read.",
	})

	assertRecoveryCardContains(t, got,
		"phase_id=plan_execute.required_source",
		"invariant_id=loop.repeated_dead_end",
		"rejected action: tool=read_file action_class=read target=internal/example.go reason=\"same source body already read\"",
		"invalid repeats: read the same path and range again, ask for another generic probe without a named gap",
		"currently callable tools: read_file, code_navigate",
		"currently callable action classes: read, blocker",
		"exact blocker shape: <<QUESTION>>: State the exact blocker and cite the concrete evidence source, path, command, or capability that is unavailable or failed.",
	)
	assertRecoveryCardOmitsProjectWorkflowTerms(t, got)

	if strings.Contains(got, "deterministic safe next action") {
		ginkgo.Fail(fmt.Sprintf("recovery card should not synthesize strategy text:\n%s", got), 1)
	}
})

var _ = ginkgo.It("RecoveryCardRendersCompletionGateStyleCard", func() {
	t := ginkgo.GinkgoT()
	got := RenderRecoveryCard(RecoveryCard{
		ContractID:  "completion-gate",
		PhaseID:     contractcompile.TurnPhaseImplementationVerifyReady,
		InvariantID: "completion.post_write_verification",
		EvidenceRefs: []string{
			"changed_path:internal/example.go",
			"verification:none_after_write",
		},
		RejectedAction: RecoveryRejectedAction{
			ActionClass: actionFinalAnswer,
			Reason:      "final answer before verification evidence",
		},
		Invariant:             "A completion answer requires verification evidence or an exact verification blocker after writes.",
		InvalidRepeats:        []string{"final answer without verification status", "more source reads before naming the verification blocker"},
		CallableActionClasses: []string{actionVerify, actionBlocker},
		ExactBlockerShape:     "<<QUESTION>>: Verification blocked because <exact command or capability> cannot run: <specific reason>.",
	})

	assertRecoveryCardContains(t, got,
		"contract_id=completion-gate",
		"phase_id=implementation.verify_ready",
		"invariant_id=completion.post_write_verification",
		"evidence: changed_path:internal/example.go, verification:none_after_write",
		"rejected action: action_class=final_answer reason=\"final answer before verification evidence\"",
		"currently callable tools: none",
		"currently callable action classes: verify, blocker",
		"exact blocker shape: <<QUESTION>>: State the exact blocker and cite the concrete evidence source, path, command, or capability that is unavailable or failed.",
	)
	if strings.Contains(got, "deterministic safe next action") {
		ginkgo.Fail(fmt.Sprintf("completion-gate recovery card without deterministic action should not render strategy text:\n%s", got), 1)
	}
	assertRecoveryCardOmitsProjectWorkflowTerms(t, got)
})

var _ = ginkgo.It("RecoveryCardDoesNotAdvertiseCompletionGateAsCallableTool", func() {
	t := ginkgo.GinkgoT()
	got := RenderRecoveryCard(RecoveryCard{
		RejectedAction: RecoveryRejectedAction{
			Tool:   "completion_gate",
			Reason: "unsupported detail",
		},
		CallableTools:         []string{"read_file", "code_navigate"},
		CallableActionClasses: []string{actionFinalAnswer, actionRead, actionBlocker},
	})

	assertRecoveryCardContains(t, got,
		"rejected action: action_class=final_answer reason=\"unsupported detail\"",
		"currently callable tools: read_file, code_navigate",
	)

	if strings.Contains(got, "tool=completion_gate") {
		ginkgo.Fail(fmt.Sprintf("model-facing recovery card must not advertise internal completion_gate as a tool:\n%s", got), 1)
	}
})

var _ = ginkgo.It("BuildRecoveryCardDerivesCallableSurfaceFromPhaseContract", func() {
	t := ginkgo.GinkgoT()
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implement",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go"},
		AllowedTools:      []string{"read_file", "source_sanity", "shell", "apply_patch"},
	}
	run := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
	_, write := committedMutationResultForTest("a.go")
	installMutationReceiptForTest(run, write)
	state := run.implementationCompletionState([]workspace.ToolResult{write})
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:                    brief,
		Results:                  []workspace.ToolResult{write},
		BaseAllowedTools:         brief.AllowedTools,
		ImplementationCompletion: &state,
		InspectionAuthority:      permissionAuthorizedInspectionForTest(brief.AllowedTools...),
	})
	card := BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:       contract,
		CurrentAllowedTools: []string{"read_file", "source_sanity", "shell", "apply_patch"},
		RejectedRequest:     workspace.ToolRequest{Tool: "completion_gate"},
		RejectedResult: workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Completion gate rejected final answer",
			Error:   "post-write verification is unresolved",
		},
	})

	if contract.PhaseID != contractcompile.TurnPhaseImplementationVerifyReady {
		ginkgo.Fail(fmt.Sprintf("PhaseID = %q, want %q", contract.PhaseID, contractcompile.TurnPhaseImplementationVerifyReady), 1)
	}

	for _, class := range contract.RuntimeCall.ExpressibleActionClasses {
		if !slices.Contains(card.CallableActionClasses, class) {
			ginkgo.Fail(fmt.Sprintf("recovery card callable action classes %#v missing contract class %q", card.CallableActionClasses, class), 1)
		}
	}

	if !slices.Contains(card.CallableTools, "read_file") || !slices.Contains(card.CallableActionClasses, actionRead) {
		ginkgo.Fail(fmt.Sprintf("verification recovery card should retain permission-authorized reads, got tools=%#v actions=%#v", card.CallableTools, card.CallableActionClasses), 1)
	}

	for _, want := range []string{"source_sanity", "shell", "apply_patch"} {
		if !slices.Contains(card.CallableTools, want) {
			ginkgo.Fail(fmt.Sprintf("verification recovery card missing callable tool %q: %#v", want, card.CallableTools), 1)
		}
	}

	assertRecoveryCardOmitsProjectWorkflowTerms(t, RenderRecoveryCard(card))
})

var _ = ginkgo.It("BuildRecoveryCardDoesNotAdvertiseBlockerWhenSubmitCorrectionRequiresPlanOnly", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "submit_plan"},
			RequiredEditPaths: []string{"a.go"},
		},
		Results: []workspace.ToolResult{
			{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)"},
			{
				Tool:    "submit_plan",
				Summary: "submit_plan rejected: required edit path coverage mismatch",
				Error:   "missing required edit path(s): `a.go`.",
			},
		},
		BaseAllowedTools:  []string{"read_file", "code_navigate", "submit_plan"},
		TextualToolParser: true,
		ToolTransportMode: "textual",
		ResponseFormat:    "json",
	})
	card := BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:       contract,
		CurrentAllowedTools: []string{"submit_plan"},
		RejectedRequest:     workspace.ToolRequest{Tool: "submit_plan"},
		RejectedResult: workspace.ToolResult{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "missing required edit path(s): `a.go`.",
		},
		ExactBlockerShape: "<<QUESTION>>: stale blocker escape hatch",
	})
	got := RenderRecoveryCard(card)

	assertRecoveryCardContains(t, got,
		"invariant: parser.submit_plan_required: The active phase requires one parser-readable submit_plan action that satisfies the current phase contract.",
		"currently callable action classes: submit_plan",
		"visible safe alternatives: submit_plan",
		"exact blocker shape: none available",
	)

	if strings.Contains(got, "supported terminal blocker") ||
		strings.Contains(got, "stale blocker escape hatch") ||
		strings.Contains(got, "currently callable action classes: submit_plan, blocker") ||
		strings.Contains(got, "deterministic safe next action") {

		ginkgo.Fail(fmt.Sprintf("submit-correction recovery card advertised blocker despite submit_plan-only contract:\n%s", got), 1)
	}
})

var _ = ginkgo.It("RecoveryCardForTextualReadOnlyEvidencePendingKeepsReadTools", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			Objective:         "Compare internal/labels/labels.go and internal/labels/labels_test.go.",
			AllowedTools:      []string{"read_file", "search_content", "code_navigate"},
		},
		Results: []workspace.ToolResult{
			{Tool: "read_file", Summary: "Read f1 internal/labels/labels.go lines 1-5"},
			{Tool: "code_navigate", Summary: "Code navigation routed references via go_references"},
		},
		BaseAllowedTools:  []string{"read_file", "search_content", "code_navigate"},
		TextualToolParser: true,
		ToolTransportMode: "textual",
	})
	if contract.Phase != "read_only_evidence_pending" {
		ginkgo.Fail(fmt.Sprintf("Phase = %q, want read_only_evidence_pending", contract.Phase), 1)
	}

	card := BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:       contract,
		CurrentAllowedTools: []string{"read_file", "search_content", "code_navigate"},
		RejectedRequest:     workspace.ToolRequest{Tool: "code_navigate", Path: "internal/surfaces/tui/app"},
		RejectedResult: workspace.ToolResult{
			Tool:    "code_navigate",
			Summary: "Repeated dead-end action rejected",
			Error:   "this action class already failed earlier in the turn",
		},
	})
	got := RenderRecoveryCard(card)
	assertRecoveryCardContains(t, got,
		"currently callable tools: read_file, search_content, code_navigate",
		"currently callable action classes:",
		"read",
		"search",
	)

	if strings.Contains(got, "currently callable tools: none") {
		ginkgo.Fail(fmt.Sprintf("read-only pending recovery card must not close the textual tool surface:\n%s", got), 1)
	}

	assertRecoveryCardOmitsProjectWorkflowTerms(t, got)
})

var _ = ginkgo.It("RecoveryCardOmitsHiddenToolsFromCallableSurface", func() {
	contract := TurnPhaseContract{
		Phase:        "generic",
		PhaseID:      contractcompile.TurnPhaseGeneric,
		AllowedTools: []string{"task", "read_file", "code_navigate"},
		HiddenTools:  []string{"task"},
		ActionPolicy: TurnActionClassPolicy{
			AllowedActionClasses: []string{actionRead, actionGenericProbe, actionFinalAnswer, actionBlocker},
			CallableToolNames:    []string{"task", "read_file", "code_navigate"},
		},
		RuntimeCall: RuntimeCallContract{
			ExpressibleActionClasses: []string{actionRead, actionGenericProbe, actionFinalAnswer, actionBlocker},
		},
	}

	card := BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:       contract,
		CurrentAllowedTools: []string{"task", "read_file", "code_navigate"},
		RejectedRequest:     workspace.ToolRequest{Tool: "task", Agent: "explorer"},
		RejectedResult: workspace.ToolResult{
			Tool:    "task",
			Summary: "Tool hidden by active contract",
			Error:   "task is hidden for the rest of this turn by the active runtime contract.",
		},
	})

	if slices.Contains(card.CallableTools, "task") {
		ginkgo.Fail(fmt.Sprintf("hidden task must not be advertised as callable: %#v", card.CallableTools), 1)
	}

	for _, want := range []string{"read_file", "code_navigate"} {
		if !slices.Contains(card.CallableTools, want) {
			ginkgo.Fail(fmt.Sprintf("expected callable tool %q to remain: %#v", want, card.CallableTools), 1)
		}
	}
})

var _ = ginkgo.It("RecoveryCardExposesWriteReadySurfaceWithoutStrategyText", func() {
	t := ginkgo.GinkgoT()
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools: []string{
				"context_pack", "read_file", "code_navigate", "search_content",
				"mkdir", "edit_file", "write_file", "append_file", "apply_patch", "go_move_declarations",
			},
		},
	})
	results := []workspace.ToolResult{
		readFileSourceWindowResult(
			"internal/surfaces/tui/app/sessions_panel.go",
			1,
			219,
			219,
			"func renderSessionsPanelContent() {}\nfunc handleSessionsPanelKey() {}\n",
		),
	}

	got := runner.renderRecoveryCardForRejectedActionWithTransport(
		workspace.ToolRequest{Tool: "code_navigate", Path: "internal/surfaces/tui/app/model.go"},
		workspace.ToolResult{
			Tool:    "code_navigate",
			Summary: "Implementation target write-ready tool rejected",
			Error:   "code_navigate is outside the implementation target-write-ready surface",
		},
		results,
		[]string{"mkdir", "edit_file", "write_file", "append_file", "apply_patch", "go_move_declarations"},
		PlanExecutePlanningTransport{},
	)

	assertRecoveryCardContains(t, got,
		"phase_id=implementation.write_ready",
		"currently callable action classes: per_edit_write, blocker",
		"visible safe alternatives: per_edit_write, blocker",
	)

	if strings.Contains(got, "deterministic safe next action") {
		ginkgo.Fail(fmt.Sprintf("write-ready recovery card should not synthesize strategy text, got:\n%s", got), 1)
	}

	assertRecoveryCardOmitsProjectWorkflowTerms(t, got)
})

var _ = ginkgo.It("RecoveryCardForRejectedActionBuildsRecoveryCard", func() {
	t := ginkgo.GinkgoT()
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implement",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"internal/example.go"},
			AllowedTools:      []string{"read_file", "source_sanity", "shell", "apply_patch"},
		},
	})
	_, write := committedMutationResultForTest("internal/example.go")
	installMutationReceiptForTest(runner, write)

	got := runner.renderRecoveryCardForRejectedActionWithTransport(
		workspace.ToolRequest{Tool: "completion_gate"},
		workspace.ToolResult{Tool: "completion_gate", Summary: "Completion gate rejected final answer", Error: "post-write verification is unresolved"},
		[]workspace.ToolResult{write},
		[]string{"read_file", "source_sanity", "shell", "apply_patch"},
		PlanExecutePlanningTransport{},
	)

	assertRecoveryCardContains(t, got,
		"Recovery card",
		"phase_id=implementation.verify_ready",
		"currently callable tools: source_sanity, shell",
	)
})

var _ = ginkgo.It("RecoveryCardForRejectedActionUsesEffectiveTextualFallbackTransport", func() {
	t := ginkgo.GinkgoT()
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "main",
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{workspace.ToolNameSubmitPlan},
			RequiredEditPaths: []string{"a.go"},
		},
	})
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
	}

	got := runner.renderRecoveryCardForRejectedActionWithTransport(
		workspace.ToolRequest{Tool: "completion_gate"},
		workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Completion gate rejected final answer",
			Error:   "submit_plan JSON object required",
		},
		results,
		[]string{workspace.ToolNameSubmitPlan},
		PlanExecutePlanningTransport{TextualToolParser: true},
	)

	assertRecoveryCardContains(t, got,
		"phase_id=plan_execute.submit_ready.textual",
		"projection_id=plan_execute.submit_ready.textual",
	)
	if strings.Contains(got, string(contractcompile.TurnPhaseSubmitReadyNativeRequired)) {
		ginkgo.Fail(fmt.Sprintf("textual fallback recovery card leaked native-required projection:\n%s", got), 1)
	}
})

var _ = ginkgo.It("BuildRecoveryCardExposesClosedPlannerSurfaceWithoutStrategyText", func() {
	t := ginkgo.GinkgoT()
	contract := TurnPhaseContract{
		PhaseID:      contractcompile.TurnPhaseMalformedSubmitPlanRecovery,
		RequiredNext: []string{"parser-readable submit_plan using edits[] for changed paths and top-level source_satisfied_paths for already-final paths, Decision: source-satisfied with per-path current-state evidence, or <<QUESTION>> with the exact blocker"},
		ActionPolicy: TurnActionClassPolicy{
			AllowedActionClasses:   []string{actionSubmitPlan, actionSourceSatisfied, actionBlocker},
			InvalidActionClasses:   []string{actionRead, actionSearch, actionPerEditWrite},
			TerminalResponseShapes: []string{"submit_plan", "Decision: source-satisfied", "<<QUESTION>>:"},
			CallableToolNames:      []string{"submit_plan"},
		},
		RuntimeCall: RuntimeCallContract{
			ExpressibleActionClasses: []string{actionSubmitPlan, actionSourceSatisfied, actionBlocker},
		},
	}
	got := RenderRecoveryCard(BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:       contract,
		CurrentAllowedTools: []string{"submit_plan"},
		RejectedRequest:     workspace.ToolRequest{Tool: "read_file", Path: "internal/surfaces/tui/app/sessions_panel.go"},
		RejectedResult: workspace.ToolResult{
			Tool:    "read_file",
			Summary: "read/search/source-refresh after plan-execute submit_plan readiness",
		},
	}))

	assertRecoveryCardContains(t, got,
		"phase_id=plan_execute.malformed_submit_plan_recovery",
		"currently callable tools: submit_plan",
		"currently callable action classes: submit_plan, source_satisfied, blocker",
		"visible safe alternatives: submit_plan, source_satisfied, blocker",
	)

	if strings.Contains(got, "deterministic safe next action") {
		ginkgo.Fail(fmt.Sprintf("closed planner recovery card should not synthesize strategy text, got:\n%s", got), 1)
	}

	assertRecoveryCardOmitsProjectWorkflowTerms(t, got)
})

var _ = ginkgo.It("BuildRecoveryCardExposesAnchorRefreshSurfaceWithoutStrategyText", func() {
	t := ginkgo.GinkgoT()
	contract := TurnPhaseContract{
		PhaseID:      contractcompile.TurnPhaseStaleAnchorRefresh,
		Phase:        "anchor_refresh",
		RequiredNext: []string{"bounded source-body refresh for: a.go; do not submit_plan until that refresh returns"},
		InvalidNext:  []string{"submit_plan before bounded anchor refresh", "broad search"},
		ActionPolicy: TurnActionClassPolicy{
			AllowedActionClasses: []string{actionRead},
			InvalidActionClasses: []string{actionSubmitPlan, actionSearch},
			CallableToolNames:    []string{"read_file", "code_navigate"},
		},
		RuntimeCall: RuntimeCallContract{
			ExpressibleActionClasses: []string{actionRead},
		},
	}
	got := RenderRecoveryCard(BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:       contract,
		CurrentAllowedTools: []string{"read_file", "code_navigate"},
		RejectedRequest:     workspace.ToolRequest{Tool: "submit_plan"},
		RejectedResult: workspace.ToolResult{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "old_text not found in current source",
		},
	}))

	assertRecoveryCardContains(t, got,
		"phase_id=plan_execute.stale_anchor_refresh",
		"currently callable tools: read_file, code_navigate",
		"currently callable action classes: read",
		"visible safe alternatives: read",
	)

	if strings.Contains(got, "deterministic safe next action") {
		ginkgo.Fail(fmt.Sprintf("anchor refresh recovery card should not synthesize strategy text, got:\n%s", got), 1)
	}
})

var _ = ginkgo.It("RecoveryCardDoesNotAdvertiseUnavailablePendingRedirect", func() {
	t := ginkgo.GinkgoT()
	contract := TurnPhaseContract{
		PhaseID:      contractcompile.TurnPhaseImplementationWriteReady,
		RequiredNext: []string{"parser-readable write to the sourced target path, or <<QUESTION>> with the exact edit blocker/no-op evidence"},
		ActionPolicy: TurnActionClassPolicy{
			AllowedActionClasses: []string{actionPerEditWrite, actionBlocker},
			CallableToolNames:    []string{"apply_patch"},
		},
		RuntimeCall: RuntimeCallContract{
			ExpressibleActionClasses: []string{actionPerEditWrite, actionBlocker},
		},
	}
	got := RenderRecoveryCard(BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:       contract,
		CurrentAllowedTools: []string{"apply_patch"},
		RejectedRequest:     workspace.ToolRequest{Tool: "read_file", Path: "internal/surfaces/tui/app/sessions_panel.go"},
		RejectedResult:      workspace.ToolResult{Tool: "read_file", Summary: "Implementation target write-ready tool rejected"},
	}))

	assertRecoveryCardContains(t, got,
		"phase_id=implementation.write_ready",
		"currently callable tools: apply_patch",
		"currently callable action classes: per_edit_write, blocker",
		"visible safe alternatives: per_edit_write, blocker",
	)

	if strings.Contains(got, "Resolve the exact pending redirected source-body obligation") ||
		strings.Contains(got, "via code_navigate") ||
		strings.Contains(got, "deterministic safe next action") {

		ginkgo.Fail(fmt.Sprintf("recovery card must not advertise unavailable pending read follow-up, got:\n%s", got), 1)
	}
})

var _ = ginkgo.It("RecoveryCardExposesCallablePendingRedirectSurfaceWithoutStrategyText", func() {
	t := ginkgo.GinkgoT()
	contract := TurnPhaseContract{
		PhaseID:      contractcompile.TurnPhaseImplementationWriteReady,
		RequiredNext: []string{"resolve the pending redirected source-body obligation, then make one parser-readable write to a sourced target path, or <<QUESTION>> with the exact edit blocker/no-op evidence"},
		ActionPolicy: TurnActionClassPolicy{
			AllowedActionClasses: []string{actionRead, actionPerEditWrite, actionBlocker},
			CallableToolNames:    []string{"code_navigate", "apply_patch"},
		},
		RuntimeCall: RuntimeCallContract{
			ExpressibleActionClasses: []string{actionRead, actionPerEditWrite, actionBlocker},
		},
	}
	card := BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:       contract,
		CurrentAllowedTools: []string{"code_navigate", "apply_patch"},
		RejectedRequest:     workspace.ToolRequest{Tool: "search_content", Path: "internal/surfaces/tui/app"},
		RejectedResult:      workspace.ToolResult{Tool: "search_content", Summary: "Implementation target write-ready pending source-body obligation rejected"},
	})
	got := RenderRecoveryCard(card)

	if card.RejectedAction.ActionClass != actionSearch {
		ginkgo.Fail(fmt.Sprintf("rejected search_content should retain search action class, got %q", card.RejectedAction.ActionClass), 1)
	}

	assertRecoveryCardContains(t, got,
		"currently callable action classes: read, per_edit_write, blocker",
		"visible safe alternatives: read, per_edit_write, blocker",
	)

	if strings.Contains(got, "deterministic safe next action") ||
		strings.Contains(got, "Resolve the exact pending redirected source-body obligation") {

		ginkgo.Fail(fmt.Sprintf("recovery card should expose callable surface without strategy text:\n%s", got), 1)
	}
})

var _ = ginkgo.It("RecoveryCardDoesNotAdvertiseHiddenWriteAsSafeAlternative", func() {
	t := ginkgo.GinkgoT()
	contract := TurnPhaseContract{
		PhaseID:      contractcompile.TurnPhaseImplementationWriteReady,
		AllowedTools: []string{"read_file", "apply_patch"},
		HiddenTools:  []string{"apply_patch"},
		ActionPolicy: TurnActionClassPolicy{
			AllowedActionClasses: []string{actionRead, actionPerEditWrite, actionBlocker},
			CallableToolNames:    []string{"read_file", "apply_patch"},
		},
		RuntimeCall: RuntimeCallContract{
			ExpressibleActionClasses: []string{actionRead, actionPerEditWrite, actionBlocker},
		},
	}

	got := RenderRecoveryCard(BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:       contract,
		CurrentAllowedTools: []string{"read_file", "apply_patch"},
		RejectedRequest:     workspace.ToolRequest{Tool: "completion_gate"},
		RejectedResult:      workspace.ToolResult{Tool: "completion_gate", Summary: "Completion gate rejected final answer"},
	}))

	assertRecoveryCardContains(t, got,
		"currently callable tools: read_file",
		"currently callable action classes: read, blocker",
		"visible safe alternatives: read, blocker",
	)
	if strings.Contains(got, "visible safe alternatives: read, per_edit_write") ||
		strings.Contains(got, "currently callable action classes: read, per_edit_write") {

		ginkgo.Fail(fmt.Sprintf("hidden write tools must not advertise per_edit_write as safe:\n%s", got), 1)
	}
})

var _ = ginkgo.It("RecoveryCardSurfaceSummaryUsesVisibleSafeAlternatives", func() {
	t := ginkgo.GinkgoT()
	contract := TurnPhaseContract{
		ContractID: "contract-test",
		PhaseID:    contractcompile.TurnPhaseImplementationWriteReady,
		ActionPolicy: TurnActionClassPolicy{
			AllowedActionClasses: []string{actionPerEditWrite, actionBlocker},
			CallableToolNames:    []string{"apply_patch"},
		},
		RuntimeCall: RuntimeCallContract{
			ExpressibleActionClasses: []string{actionPerEditWrite, actionBlocker},
		},
	}
	card := BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:       contract,
		CurrentAllowedTools: []string{"apply_patch"},
		RejectedRequest:     workspace.ToolRequest{Tool: "completion_gate"},
		RejectedResult:      workspace.ToolResult{Tool: "completion_gate", Summary: "Completion gate rejected final answer"},
	})

	summary := card.TurnPhaseSurfaceSummary(contract)

	assertContains(t, summary.SafeAlternatives, actionPerEditWrite)
	assertContains(t, summary.SafeAlternatives, actionBlocker)
})

var _ = ginkgo.It("BuildRecoveryCardExposesVerificationReadySurfaceWithoutStrategyText", func() {
	t := ginkgo.GinkgoT()
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "source_sanity", "shell", "git_diff", "git_status", "apply_patch"},
	}
	run := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
	_, write := committedMutationResultForTest("internal/surfaces/tui/app/sessions_panel.go")
	installMutationReceiptForTest(run, write)
	state := run.implementationCompletionState([]workspace.ToolResult{write})
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:                    brief,
		Results:                  []workspace.ToolResult{write},
		BaseAllowedTools:         brief.AllowedTools,
		ImplementationCompletion: &state,
		InspectionAuthority:      permissionAuthorizedInspectionForTest(brief.AllowedTools...),
	})
	got := RenderRecoveryCard(BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:       contract,
		CurrentAllowedTools: []string{"read_file", "source_sanity", "shell", "git_diff", "git_status"},
		RejectedRequest:     workspace.ToolRequest{Tool: "completion_gate"},
		RejectedResult: workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Completion gate rejected final answer",
			Error:   "post-write verification is unresolved",
		},
	}))

	assertRecoveryCardContains(t, got,
		"phase_id=implementation.verify_ready",
		"currently callable action classes:",
		"read",
		"verify",
	)

	if strings.Contains(got, "deterministic safe next action") {
		ginkgo.Fail(fmt.Sprintf("verification-ready recovery card should not synthesize strategy text, got:\n%s", got), 1)
	}

	assertRecoveryCardOmitsProjectWorkflowTerms(t, got)
})

var _ = ginkgo.It("BuildRecoveryCardDoesNotDeriveVerifyOnlySafeNextForVerificationRepair", func() {
	t := ginkgo.GinkgoT()
	contract := TurnPhaseContract{
		PhaseID:      contractcompile.TurnPhaseImplementationVerifyReady,
		Phase:        "verification_repair",
		RequiredNext: []string{"repair the failing in-scope changed/required path, use one narrow source/language/context lookup for an exact repair fact, rerun language-appropriate verification after the repair write, or <<QUESTION>> with the exact external blocker"},
		ActionPolicy: TurnActionClassPolicy{
			AllowedActionClasses: []string{actionPerEditWrite, actionRead, actionVerify, actionBlocker},
			CallableToolNames:    []string{"apply_patch", "read_file", "source_sanity"},
		},
		RuntimeCall: RuntimeCallContract{
			ExpressibleActionClasses: []string{actionPerEditWrite, actionRead, actionVerify, actionBlocker},
		},
	}
	got := RenderRecoveryCard(BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:       contract,
		CurrentAllowedTools: []string{"apply_patch", "read_file", "source_sanity"},
		RejectedRequest:     workspace.ToolRequest{Tool: "completion_gate"},
		RejectedResult: workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Completion gate rejected final answer",
			Error:   "post-repair verification is unresolved",
		},
	}))

	assertRecoveryCardContains(t, got,
		"phase_id=implementation.verify_ready",
		"currently callable action classes: per_edit_write, read, verify, blocker",
	)

	if strings.Contains(got, "Run one focused verification action") {
		ginkgo.Fail(fmt.Sprintf("verification-repair recovery card must not imply verification before repair, got:\n%s", got), 1)
	}
	if strings.Contains(got, "deterministic safe next action") {
		ginkgo.Fail(fmt.Sprintf("verification-repair recovery card must not synthesize strategy text, got:\n%s", got), 1)
	}

	assertRecoveryCardOmitsProjectWorkflowTerms(t, got)
})

var _ = ginkgo.It("BuildRecoveryCardDoesNotAdvertiseToolsForFinalOnlyPhase", func() {
	contract := TurnPhaseContract{
		PhaseID:      contractcompile.TurnPhaseGeneric,
		AllowedTools: nil,
		ActionPolicy: TurnActionClassPolicy{
			AllowedActionClasses: []string{actionFinalAnswer, actionBlocker},
		},
		RuntimeCall: RuntimeCallContract{
			ExpressibleActionClasses: []string{actionFinalAnswer, actionBlocker},
		},
	}
	card := BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:       contract,
		CurrentAllowedTools: []string{"read_file", "source_sanity", "shell"},
		RejectedRequest:     workspace.ToolRequest{Tool: "read_file", Path: "internal/labels/labels.go"},
		RejectedResult: workspace.ToolResult{
			Tool:    "read_file",
			Summary: "Conversation contract rejected extra exploration after bounded evidence",
		},
	})

	if len(card.CallableTools) != 0 {
		ginkgo.Fail(fmt.Sprintf("final-only recovery card should not advertise stale tools: %#v", card.CallableTools), 1)
	}

	for _, forbidden := range []string{actionRead, actionVerify, actionGenericProbe} {
		if slices.Contains(card.CallableActionClasses, forbidden) {
			ginkgo.Fail(fmt.Sprintf("final-only recovery card advertised %q: %#v", forbidden, card.CallableActionClasses), 1)
		}
	}
})

var _ = ginkgo.It("RecoveryCardKeepsFinalAnswerStrategyModelOwned", func() {
	t := ginkgo.GinkgoT()
	contract := TurnPhaseContract{
		PhaseID: contractcompile.TurnPhaseGeneric,
		Phase:   "read_only_evidence_available",
		ActionPolicy: TurnActionClassPolicy{
			AllowedActionClasses: []string{actionFinalAnswer, actionBlocker},
		},
		RuntimeCall: RuntimeCallContract{
			ExpressibleActionClasses: []string{actionFinalAnswer, actionBlocker},
		},
	}
	card := BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:   contract,
		RejectedRequest: workspace.ToolRequest{Tool: "code_navigate", Path: "internal/surfaces/tui/app/model_state.go", Capability: "read_symbol"},
		RejectedResult: workspace.ToolResult{
			Tool:    "code_navigate",
			Summary: "Repeated tool request rejected",
			Error:   "this exact request already replayed prior evidence once",
		},
	})
	got := RenderRecoveryCard(card)

	assertEqualSet(t, card.CallableActionClasses, []string{actionFinalAnswer, actionBlocker})
	assertEqualSet(t, card.VisibleSafeAlternatives, []string{actionBlocker})
	if strings.Contains(got, "deterministic safe next action") {
		ginkgo.Fail(fmt.Sprintf("read-only final-answer recovery must not render strategy text:\n%s", got), 1)
	}

	assertRecoveryCardContains(t, got,
		"currently callable action classes: final_answer, blocker",
		"visible safe alternatives: blocker",
	)
})

var _ = ginkgo.It("RecoveryCardEvidencePendingKeepsReadSearchStrategyModelOwned", func() {
	t := ginkgo.GinkgoT()
	contract := TurnPhaseContract{
		PhaseID: contractcompile.TurnPhaseGeneric,
		Phase:   "read_only_evidence_pending",
		RequiredNext: []string{
			"one bounded read/search for an exact unsupported claim, or <<QUESTION>> with the exact blocker; answer only if the gathered evidence supports every material claim or explicitly marks unsupported claims as unverified",
		},
		ActionPolicy: TurnActionClassPolicy{
			AllowedActionClasses: []string{actionBlocker, actionFinalAnswer, actionRead, actionSearch},
			CallableToolNames:    []string{"code_navigate", "read_file", "search_content"},
		},
		RuntimeCall: RuntimeCallContract{
			ExpressibleActionClasses: []string{actionBlocker, actionFinalAnswer, actionRead, actionSearch},
		},
	}
	card := BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:       contract,
		CurrentAllowedTools: []string{"code_navigate", "read_file", "search_content"},
		RejectedRequest:     workspace.ToolRequest{Tool: "search_content", Path: ".", Pattern: "key.*input"},
		RejectedResult: workspace.ToolResult{
			Tool:    "search_content",
			Summary: "Discovery consumption required before more broad discovery",
			Error:   "a previous discovery result already returned concrete candidates",
		},
	})
	got := RenderRecoveryCard(card)

	assertEqualSet(t, card.CallableActionClasses, []string{actionRead, actionSearch, actionBlocker, actionFinalAnswer})
	assertEqualSet(t, card.VisibleSafeAlternatives, []string{actionRead, actionSearch, actionBlocker})
	if strings.Contains(got, "answer from gathered evidence now") {
		ginkgo.Fail(fmt.Sprintf("read-only pending recovery must not tell the model to answer now when evidence is missing:\n%s", got), 1)
	}
	if strings.Contains(got, "deterministic safe next action") {
		ginkgo.Fail(fmt.Sprintf("read-only pending recovery must not render strategy text:\n%s", got), 1)
	}

	assertRecoveryCardContains(t, got,
		"currently callable action classes: read, search, blocker, final_answer",
		"visible safe alternatives: read, search, blocker",
	)
})

var _ = ginkgo.It("BuildRecoveryCardGenericDoesNotInjectProductWorkflowTerms", func() {
	t := ginkgo.GinkgoT()
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			AllowedTools: []string{"read_file", "search_content"},
		},
		BaseAllowedTools: []string{"read_file", "search_content"},
	})
	got := RenderRecoveryCard(BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:       contract,
		CurrentAllowedTools: []string{"read_file", "search_content"},
		RejectedRequest:     workspace.ToolRequest{Tool: "search_content", Pattern: "MissingThing"},
		RejectedResult: workspace.ToolResult{
			Tool:    "search_content",
			Summary: "Zero-hit retry blocked",
			Error:   "this exact search_content call already returned no results earlier this turn",
		},
	}))
	assertRecoveryCardOmitsProjectWorkflowTerms(t, got)
})

var _ = ginkgo.It("CompletionGateRecoveryCardUsesGuardedMutationTargetSourcePhase", func() {
	t := ginkgo.GinkgoT()
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"context_pack", "read_file", "search_content", "edit_file", "apply_patch", "source_sanity"},
		},
	})
	got := runner.renderRecoveryCardForRejectedActionWithTransport(
		workspace.ToolRequest{Tool: "completion_gate"},
		workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Mutation completion gate rejected final answer",
			Error:   "no successful workspace write",
		},
		nil,
		[]string{"context_pack", "read_file", "search_content", "edit_file", "apply_patch", "source_sanity"},
		PlanExecutePlanningTransport{},
	)

	assertRecoveryCardContains(t, got,
		"phase_id=implementation.target_source",
		"currently callable action classes:",
		"read",
		"search",
		"per_edit_write",
		"exact blocker shape:",
	)

	if strings.Contains(got, "currently callable action classes: final_answer") ||
		strings.Contains(got, " final_answer") {

		ginkgo.Fail(fmt.Sprintf("guarded no-write recovery card should not advertise final_answer, got:\n%s", got), 1)
	}

	assertRecoveryCardOmitsProjectWorkflowTerms(t, got)
})

var _ = ginkgo.It("ImplementationTargetSourceContractSteersNewFileWrite", func() {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "search_content", "edit_file", "write_file", "apply_patch"},
		},
		BaseAllowedTools: []string{"read_file", "search_content", "edit_file", "write_file", "apply_patch"},
	})

	if contract.PhaseID != contractcompile.TurnPhaseImplementationTargetSource {
		ginkgo.Fail(fmt.Sprintf("expected phase_id=%s, got %q", contractcompile.TurnPhaseImplementationTargetSource, contract.PhaseID), 1)
	}

	requiredNextJoined := strings.Join(contract.RequiredNext, "\n")
	if !strings.Contains(requiredNextJoined, "genuinely new") ||
		!strings.Contains(requiredNextJoined, "write_file") {
		ginkgo.Fail(fmt.Sprintf("target_source RequiredNext must steer a direct write_file for a genuinely new file so the enforcement escape hatch is discoverable, got:\n%s", requiredNextJoined), 1)
	}

	for _, invalid := range contract.InvalidNext {
		if strings.Contains(invalid, "write before exact source-body evidence") {
			ginkgo.Fail(fmt.Sprintf("target_source InvalidNext must scope the evidence requirement to modifications of existing source, not all writes; found blanket prohibition %q", invalid), 1)
		}
	}
})

func assertRecoveryCardContains(t ginkgo.FullGinkgoTInterface, got string, wants ...string) {
	t.Helper()

	for _, want := range wants {
		if !strings.Contains(got, want) {
			ginkgo.Fail(fmt.Sprintf("expected recovery card to contain %q, got:\n%s", want, got), 1)
		}
	}
}

func assertRecoveryCardOmitsProjectWorkflowTerms(t ginkgo.FullGinkgoTInterface, got string) {
	t.Helper()

	for _, forbidden := range []string{"PRD", "ADR", "brief", "product workflow"} {
		if strings.Contains(got, forbidden) {
			ginkgo.Fail(fmt.Sprintf("recovery card should stay generic; found %q in:\n%s", forbidden, got), 1)
		}
	}
}

// ---------------------------------------------------------------------------
// P0: plan_execute_recovery_plan.go (8 functions, 0% coverage)
// ---------------------------------------------------------------------------

var _ = ginkgo.It("PlanThenExecuteSubmitCorrectionRecoveryPlanEmptyRequiredPaths", func() {
	plan := planThenExecuteSubmitCorrectionRecoveryPlan(workerruntime.WorkerRuntimeBrief{}, workspace.ToolResult{Tool: "submit_plan", Summary: "rejected"}, "refresh note", false)
	if plan.Kind != PlanExecuteRecoveryKindSubmitCorrection {
		ginkgo.Fail("expected SubmitCorrection kind", 1)
	}
	for _, inst := range plan.Instructions {
		if strings.Contains(inst, "Required edit paths") {
			ginkgo.Fail("expected no paths listing when empty", 1)
		}
	}
})

var _ = ginkgo.It("PlanThenExecuteSubmitCorrectionRecoveryPlanWithRefreshNote", func() {
	plan := planThenExecuteSubmitCorrectionRecoveryPlan(workerruntime.WorkerRuntimeBrief{}, workspace.ToolResult{Tool: "submit_plan", Summary: "rejected"}, "source-refresh: src/main.go", false)
	foundNote := false
	for _, inst := range plan.Instructions {
		if strings.Contains(inst, "source-refresh") {
			foundNote = true
			break
		}
	}
	if !foundNote {
		ginkgo.Fail("expected refresh note", 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteSubmitCorrectionRecoveryPlanAllowsExactBlockerAfterRepeatedFailures", func() {
	plan := planThenExecuteSubmitCorrectionRecoveryPlan(workerruntime.WorkerRuntimeBrief{}, workspace.ToolResult{Tool: "submit_plan", Summary: "rejected"}, "", true)
	if !slices.Equal(plan.TerminalOptions, []string{"submit_plan", "<<QUESTION>>:"}) {
		ginkgo.Fail(fmt.Sprintf("terminal options = %#v, want submit_plan plus exact blocker", plan.TerminalOptions), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteNativeSubmitPlanCorrectionRecoveryPlanEmptyPaths", func() {
	plan := planThenExecuteNativeSubmitPlanCorrectionRecoveryPlan(workerruntime.WorkerRuntimeBrief{}, workspace.ToolResult{Tool: "submit_plan", Summary: "rejected"})
	if plan.Kind != PlanExecuteRecoveryKindNativeSubmitCorrection {
		ginkgo.Fail("expected NativeSubmitCorrection kind", 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteRepeatedSubmitCorrectionRecoveryPlan", func() {
	plan := planThenExecuteRepeatedSubmitCorrectionRecoveryPlan(workspace.ToolResult{Tool: "submit_plan", Summary: "submit_plan rejected: repeated invalid", Error: "repeated invalid submit_plan"})
	if plan.Kind != PlanExecuteRecoveryKindRepeatedCorrection {
		ginkgo.Fail("expected RepeatedCorrection kind", 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteRepeatedCorrectionExhaustedRejection", func() {
	summary, reason := planThenExecuteRepeatedCorrectionExhaustedRejection(workspace.ToolResult{Tool: "submit_plan", Error: "repeated invalid submit_plan after correction prompts"})
	if summary == "" {
		ginkgo.Fail("expected non-empty summary", 1)
	}
	if !strings.Contains(reason, "submit_plan") {
		ginkgo.Fail("expected reason to reference submit_plan", 1)
	}
})

var _ = ginkgo.It("ExtractMalformedWriteBlockerReasonFound", func() {
	result := extractMalformedWriteBlockerReason("Rejected by completion gate: blocked by malformed write request: edit_file old_text not found in source")
	if !strings.Contains(result, "malformed write request") {
		ginkgo.Fail(fmt.Sprintf("expected malformed write blocker, got %q", result), 1)
	}
})

var _ = ginkgo.It("ExtractMalformedWriteBlockerReasonNotFound", func() {
	if result := extractMalformedWriteBlockerReason("Rejected by completion gate: source grounding unmet"); result != "" {
		ginkgo.Fail("expected empty", 1)
	}
})

var _ = ginkgo.It("ExtractMalformedWriteBlockerReasonEmpty", func() {
	if result := extractMalformedWriteBlockerReason(""); result != "" {
		ginkgo.Fail("expected empty for empty input", 1)
	}
})

var _ = ginkgo.It("ExtractMalformedWriteBlockerReasonTrailingPeriod", func() {
	result := extractMalformedWriteBlockerReason("blocked by malformed write request")
	if !strings.HasSuffix(result, ".") {
		ginkgo.Fail("expected trailing period", 1)
	}
})

// ---------------------------------------------------------------------------
// P1: git_history_requirement.go — successfulGitHistoryEvidenceSeen (0%)
// ---------------------------------------------------------------------------

var _ = ginkgo.It("SuccessfulGitHistoryEvidenceSeenWithValidResult", func() {
	results := []workspace.ToolResult{gitHistoryRangeResult("src/main.go")}
	if !successfulGitHistoryEvidenceSeen(results) {
		ginkgo.Fail("expected true for valid git history result", 1)
	}
})

var _ = ginkgo.It("SuccessfulGitHistoryEvidenceSeenWithHistorySymbol", func() {
	results := []workspace.ToolResult{{
		Tool:   workspace.ToolNameHistorySymbol,
		Output: "commit abc123\n\n    refactor init function\n",
		Facts: &toolfacts.ToolResultFacts{
			GitHistoryPath:   "src/main.go",
			GitHistoryStatus: toolfacts.GitHistoryStatusCommitDescriptions,
		},
	}}
	if !successfulGitHistoryEvidenceSeen(results) {
		ginkgo.Fail("expected true for history_symbol", 1)
	}
})

var _ = ginkgo.It("SuccessfulGitHistoryEvidenceSeenEmptyResults", func() {
	if successfulGitHistoryEvidenceSeen(nil) {
		ginkgo.Fail("expected false for nil", 1)
	}
})

var _ = ginkgo.It("SuccessfulGitHistoryEvidenceSeenWithError", func() {
	results := []workspace.ToolResult{{Tool: workspace.ToolNameHistoryRange, Error: "git error: fatal: bad revision"}}
	if successfulGitHistoryEvidenceSeen(results) {
		ginkgo.Fail("expected false for errored result", 1)
	}
})

var _ = ginkgo.It("SuccessfulGitHistoryEvidenceSeenWithWrongTool", func() {
	results := []workspace.ToolResult{{Tool: "read_file", Summary: "Read src/main.go lines 1-50", Output: "package main"}}
	if successfulGitHistoryEvidenceSeen(results) {
		ginkgo.Fail("expected false for non-history tool", 1)
	}
})

var _ = ginkgo.It("SuccessfulGitHistoryEvidenceSeenIgnoresLegacyTextWithoutFacts", func() {
	results := []workspace.ToolResult{{
		Tool:    workspace.ToolNameHistoryRange,
		Summary: "Read git history for src/main.go lines 10-50",
		Output:  "commit abc123\n\n    fix initialization\n",
	}}
	if successfulGitHistoryEvidenceSeen(results) {
		ginkgo.Fail("expected false without producer-owned git history facts", 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteSourceSatisfiedMissingCoveragePathsAllCovered", func() {
	_ = plancontract.SourceSatisfiedMissingCoveragePaths("Decision: source-satisfied\nThe required path src/main.go has been reviewed.", []string{"src/main.go"})
})

var _ = ginkgo.It("PlanThenExecuteSourceSatisfiedMissingCoveragePathsNoDecision", func() {
	missing := plancontract.SourceSatisfiedMissingCoveragePaths("I need to read more source code first.", []string{"src/main.go"})
	if len(missing) == 0 {
		ginkgo.Fail("expected missing paths when no source-satisfied decision", 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteSourceSatisfiedMissingCoveragePathsEmptyAnswer", func() {
	missing := plancontract.SourceSatisfiedMissingCoveragePaths("", []string{"src/main.go"})
	if len(missing) == 0 {
		ginkgo.Fail("expected missing paths for empty answer", 1)
	}
})

var _ = ginkgo.It("FinishShouldOverrideSourceEchoWithoutNoFilesChanged", func() {
	if finishShouldOverrideSourceEcho("Read internal/runtime/agent/runner_core.go. The function is Run().") {
		ginkgo.Fail("expected no override without 'no files were changed'", 1)
	}
})

var _ = ginkgo.It("FinishShouldOverrideSourceEchoWithExternalPath", func() {
	if finishShouldOverrideSourceEcho("Read README.md. No files were changed.") {
		ginkgo.Fail("expected no override for external path", 1)
	}
})

var _ = ginkgo.It("FinishShouldOverrideSourceEchoEmptyAnswer", func() {
	if finishShouldOverrideSourceEcho("") {
		ginkgo.Fail("expected no override for empty answer", 1)
	}
})

var _ = ginkgo.It("FinishShouldOverrideSourceEchoRequiresTranscript", func() {
	if finishShouldOverrideSourceEcho("Read internal/runtime/agent/runner_core.go") {
		ginkgo.Fail("expected false — bare path doesn't look like transcript", 1)
	}
})

var _ = ginkgo.It("TerminalAnswerOverridesRejectedLowValueSourceInventory", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
		},
	})
	answer := "`Runner.evaluateFinalAnswerAcceptance` in `internal/runtime/agent/implementation_flow.go` is the clearest inspected implementation anchor. The inspected body shows that it assigns `acceptance` from `newFinalAnswerAcceptance()`, sets `acceptance.EvidencePaths`, and sets `acceptance.RelevantSourceBodyPaths`."
	acceptance := finalAnswerAcceptance{
		BlockerKind: "non_answer_prose",
	}

	if !runner.terminalAnswerShouldOverrideRejectedRaw(answer, acceptance) {
		ginkgo.Fail("user-facing read-only final answer should not surface raw inspected-body source inventory", 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteWriteGuardRejectsEditFile", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	})
	got := runner.preflightPlanThenExecuteWriteGuard(workspace.ToolRequest{
		Tool: "edit_file",
		Path: "a.txt",
	})

	if !got.Intercepted {
		ginkgo.Fail(fmt.Sprintf("planner phase must reject edit_file; got %#v", got), 1)
	}

	if got.Result.Tool != "edit_file" {
		ginkgo.Fail(fmt.Sprintf("rejection should keep the tool name (so the audit trail shows what was blocked), got %q", got.Result.Tool), 1)
	}

	if !strings.Contains(got.Result.Error, "submit_plan") {
		ginkgo.Fail(fmt.Sprintf("rejection must point at submit_plan, got %q", got.Result.Error), 1)
	}

	if !strings.Contains(got.Result.Error, "executor phase") {
		ginkgo.Fail(fmt.Sprintf("rejection should explain the executor will dispatch later, got %q", got.Result.Error), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteLoopBudgetRecoveryRequiresTerminalOutcome", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	})
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack selected the coverage gate implementation",
			Facts:   contextPackCandidateFactsForTest("internal/workflows/productworkflow/coverage_gate.go"),
		},
		readFileSourceWindowResult(
			"internal/workflows/productworkflow/coverage_gate.go",
			1,
			10,
			20,
			"1:package productworkflow\n2:\n3:func EvaluateCoverageGate() {}\n",
		),
	}
	recovery, ok := runner.planThenExecuteLoopBudgetRecoveryPrompt(workspace.ToolRequest{
		Tool:       "code_navigate",
		Path:       "internal/workflows/productworkflow/coverage_gate.go",
		Capability: "definition",
		Pattern:    "EvaluateCoverageGate",
	}, results, "code_navigate repeated after warning", false)

	if !ok {
		ginkgo.Fail("expected loop-budget recovery once source evidence makes terminal planning available", 1)
	}

	for _, want := range []string{"loop budget", "permission-authorized inspection", "submit_plan", "Decision: source-satisfied", "<<QUESTION>>"} {
		if !strings.Contains(recovery.Prompt, want) {
			ginkgo.Fail(fmt.Sprintf("recovery prompt missing %q:\n%s", want, recovery.Prompt), 1)
		}
	}

	if strings.Contains(recovery.Prompt, "tools are disabled") {
		ginkgo.Fail(fmt.Sprintf("loop-budget recovery must not hide authorized inspection:\n%s", recovery.Prompt), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteWriteGuardRejectsAllPerEditWrites", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	})
	for _, tool := range []string{
		"edit_file", "write_file", "append_file", "apply_patch",
		"go_move_declarations", "mkdir",
	} {
		got := runner.preflightPlanThenExecuteWriteGuard(workspace.ToolRequest{Tool: tool})
		if !got.Intercepted {
			ginkgo.Fail(fmt.Sprintf("planner phase must reject %q, got %#v", tool, got), 1)
		}
	}
})

var _ = ginkgo.It("PlanThenExecuteWriteGuardLetsSubmitPlanThrough", func() {
	// submit_plan is the planner's ONE write — without this exemption
	// the guard would intercept it and the lane could never produce a
	// plan.
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	})
	got := runner.preflightPlanThenExecuteWriteGuard(workspace.ToolRequest{
		Tool: "submit_plan",
	})

	if got.Intercepted {
		ginkgo.Fail(fmt.Sprintf("submit_plan must pass through during planner phase, got %#v", got), 1)
	}
})

var _ = ginkgo.It("MLXPlanThenExecuteAdvertisesTextualSubmitPlanProtocol", func() {
	runner := newRunnerRunFixture(&Runner{
		Client: llama.NewClient("http://example.invalid", time.Second).WithBackend(config.BackendMLX),
	})
	brief := workerruntime.NormalizeRuntimeBrief(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"submit_plan", "read_file"},
		RequiredTools:     []string{"submit_plan"},
		RequiredEditPaths: []string{"a.txt"},
	})
	got := runner.textualToolProtocolPrompt(brief)

	for _, want := range []string{
		"# Textual Tool Call Protocol",
		"exactly one JSON object",
		"top-level `tool`, `rationale`, and `edits`",
		"`edits` must be an array",
		"never copy placeholders",
		"schema examples",
		"ellipses",
		"`existing code` comments",
		"no prose",
		"XML tags",
	} {
		if !strings.Contains(got, want) {
			ginkgo.Fail(fmt.Sprintf("expected %q in textual protocol prompt:\n%s", want, got), 1)
		}
	}

	if !strings.Contains(got, "submit_plan") || !strings.Contains(got, "read_file") {
		ginkgo.Fail(fmt.Sprintf("prompt should include required tool first and bounded allowed examples:\n%s", got), 1)
	}

	for _, forbidden := range []string{"relative/file", "exact current substring", "replacement text", "one paragraph on the overall plan"} {
		if strings.Contains(got, forbidden) {
			ginkgo.Fail(fmt.Sprintf("textual protocol prompt must not include copyable placeholder %q:\n%s", forbidden, got), 1)
		}
	}
})

var _ = ginkgo.It("NativeToolBackendSkipsTextualToolProtocolPrompt", func() {
	runner := newRunnerRunFixture(&Runner{
		Client: llama.NewClient("http://example.invalid", time.Second).WithBackend(config.BackendLlama),
	})
	brief := workerruntime.NormalizeRuntimeBrief(workerruntime.WorkerRuntimeBrief{
		AllowedTools:  []string{"submit_plan"},
		RequiredTools: []string{"submit_plan"},
	})

	if got := runner.textualToolProtocolPrompt(brief); got != "" {
		ginkgo.Fail(fmt.Sprintf("native-tool backend should not receive duplicate textual protocol prompt:\n%s", got), 1)
	}
})

var _ = ginkgo.It("PlanExecuteCompletionGateRecoveryKeepsSubmitPlanBudget", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt"},
	}
	got := completionGateRecoveryChatOptionsForBackend(llama.ChatOptions{MaxTokens: 16384}, brief, nil, true, planThenExecutePathAuthority{}, deriveImplementationProgressState(brief, nil))

	if got.MaxTokens != 768 {
		ginkgo.Fail(fmt.Sprintf("plan-execute source-read recovery should cap to one tool call, got %d", got.MaxTokens), 1)
	}

	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.txt", 1, 10, 10, "1:before\n"),
	}

	got = completionGateRecoveryChatOptionsForBackend(llama.ChatOptions{MaxTokens: 16384}, brief, results, true, planThenExecutePathAuthority{}, deriveImplementationProgressState(brief, results))

	if got.MaxTokens != 4096 {
		ginkgo.Fail(fmt.Sprintf("plan-execute existing-file recovery should cap submit_plan budget, got %d", got.MaxTokens), 1)
	}

	got = completionGateRecoveryChatOptionsForBackend(llama.ChatOptions{}, brief, results, true, planThenExecutePathAuthority{}, deriveImplementationProgressState(brief, results))
	if got.MaxTokens != 4096 {
		ginkgo.Fail(fmt.Sprintf("plan-execute existing-file recovery should default to capped submit_plan budget, got %d", got.MaxTokens), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteSubmitPlanCoverageGuardRejectsMissingRequiredPath", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	})
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "a", NewText: "A"},
		},
	})

	if !got.Intercepted {
		ginkgo.Fail("submit_plan missing a required path must be rejected", 1)
	}

	if !strings.Contains(got.Result.Error, "b.txt") {
		ginkgo.Fail(fmt.Sprintf("rejection should name the missing path, got %q", got.Result.Error), 1)
	}

	diagnostics := submitPlanDiagnostics(got.Result)
	if len(diagnostics) != 1 || diagnostics[0].Kind != toolfacts.SubmitPlanDiagnosticMissingRequiredPath || diagnostics[0].CanonicalID != "b.txt" {
		ginkgo.Fail(fmt.Sprintf("typed coverage diagnostics should name missing path, got %#v", diagnostics), 1)
	}

	if !strings.Contains(got.Result.Error, "corrected plan") {
		ginkgo.Fail(fmt.Sprintf("rejection should be recoverable, got %q", got.Result.Error), 1)
	}

	for _, forbidden := range []string{"Decision: source-satisfied", "<<QUESTION>>"} {
		if strings.Contains(got.Result.Error, forbidden) {
			ginkgo.Fail(fmt.Sprintf("coverage rejection advertised stale terminal exit %q: %q", forbidden, got.Result.Error), 1)
		}
	}
})

var _ = ginkgo.It("PlanThenExecuteSubmitPlanCoverageGuardEnforcesAllowedScopeWithoutCoverage", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:       PlanThenExecutePlannerTaskFamily,
			AllowedEditPaths: []string{"a.txt", "b.txt"},
		},
	})
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "a", NewText: "A"},
		},
	})

	if got.Intercepted {
		ginkgo.Fail(fmt.Sprintf("allowed_edit_paths are scope only, not coverage obligations: %#v", got), 1)
	}

	got = runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "c.txt", OldText: "c", NewText: "C"},
		},
	})
	if !got.Intercepted {
		ginkgo.Fail("submit_plan outside allowed_edit_paths must be rejected", 1)
	}

	if !strings.Contains(got.Result.Error, "outside allowed_edit_paths") || !strings.Contains(got.Result.Error, "c.txt") {
		ginkgo.Fail(fmt.Sprintf("allowed-scope rejection should name the outside path, got %q", got.Result.Error), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteUngroundedSubmitPlanRejectsUnscopedExistingFileEditWithoutAnchor", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	if err := writeTestFile(root, "target.txt", "old\n"); err != nil {
		ginkgo.Fail(fmt.Sprintf("write target: %v", err), 1)
	}

	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	manager.SetPermissionRules(map[string][]toolpolicy.Rule{
		workspace.ToolNameReadFile: {{Pattern: "**", Decision: toolpolicy.Allow}},
	})
	runner := newRunnerRunFixture(&Runner{
		Workspace: manager,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	})
	got := runner.preflightPlanThenExecuteSubmitPlanGroundingGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{{
			Path:       "target.txt",
			ReplaceAll: true,
			NewText:    "new\n",
			Purpose:    "replace target",
		}},
	}, nil)

	if !got.Intercepted {
		ginkgo.Fail("unscoped submit_plan must require source-body evidence for existing-file edits", 1)
	}

	if !strings.Contains(got.Result.Error, "target.txt") || !strings.Contains(got.Result.Error, "source-body evidence") {
		ginkgo.Fail(fmt.Sprintf("grounding rejection should name the path and evidence requirement, got %q", got.Result.Error), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteUngroundedSubmitPlanAllowsSourceReadEdit", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	if err := writeTestFile(root, "target.txt", "old\n"); err != nil {
		ginkgo.Fail(fmt.Sprintf("write target: %v", err), 1)
	}

	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, MaxOutputBytes: 4 << 20})
	manager.SetPermissionRules(map[string][]toolpolicy.Rule{
		workspace.ToolNameReadFile: {{Pattern: "**", Decision: toolpolicy.Allow}},
	})
	runner := newRunnerRunFixture(&Runner{
		Workspace: manager,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	})
	read := manager.Execute(runner.state.ctx, workspace.ToolRequest{
		Tool: workspace.ToolNameReadFile, Path: "target.txt", StartLine: 1, EndLine: 1,
	})
	if read.Error != "" || len(read.SourceWindows()) != 1 {
		ginkgo.Fail(fmt.Sprintf("produce current source-window evidence: %#v", read), 1)
	}
	got := runner.preflightPlanThenExecuteSubmitPlanGroundingGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{{
			Path:    "target.txt",
			OldText: "old",
			NewText: "new",
			Purpose: "update target",
		}},
	}, []workspace.ToolResult{read})

	if got.Intercepted {
		ginkgo.Fail(fmt.Sprintf("source-body evidence should ground the unscoped submit_plan edit, got %#v", got), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteSubmitPlanAllowsValidatedInsertionAnchorAfterPathEvidence", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	if err := writeTestFile(root, "target.txt", "line 1\nline 2\n"); err != nil {
		ginkgo.Fail(fmt.Sprintf("write target: %v", err), 1)
	}

	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	runner := newRunnerRunFixture(&Runner{
		Workspace: manager,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	})

	got := runner.preflightPlanThenExecuteSubmitPlanGroundingGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{{
			Path:            "target.txt",
			InsertAfterLine: 2,
			Content:         "inserted\n",
			Purpose:         "insert target line",
		}},
	}, []workspace.ToolResult{manager.Execute(context.Background(), workspace.ToolRequest{
		Tool: workspace.ToolNameReadFile, Path: "target.txt", StartLine: 2, EndLine: 2,
	})})

	if got.Intercepted {
		ginkgo.Fail(fmt.Sprintf("validated insertion anchor with path evidence should be executable, got %#v", got), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteSubmitPlanRejectsInsertionAnchorWithoutPathEvidence", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	if err := writeTestFile(root, "target.txt", "line 1\nline 2\n"); err != nil {
		ginkgo.Fail(fmt.Sprintf("write target: %v", err), 1)
	}

	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	runner := newRunnerRunFixture(&Runner{
		Workspace: manager,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	})

	got := runner.preflightPlanThenExecuteSubmitPlanGroundingGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{{
			Path:            "target.txt",
			InsertAfterLine: 2,
			Content:         "inserted\n",
			Purpose:         "insert target line",
		}},
	}, nil)

	if !got.Intercepted {
		ginkgo.Fail("validated insertion anchor must still require prior path evidence", 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteScopedSubmitPlanRequiresGroundingBeyondPathScope", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	if err := writeTestFile(root, "target.txt", "old\n"); err != nil {
		ginkgo.Fail(fmt.Sprintf("write target: %v", err), 1)
	}

	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	runner := newRunnerRunFixture(&Runner{
		Workspace: manager,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"target.txt"},
		},
	})
	got := runner.preflightPlanThenExecuteSubmitPlanGroundingGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{{
			Path:       "target.txt",
			ReplaceAll: true,
			NewText:    "new\n",
			Purpose:    "replace target",
		}},
	}, nil)

	if !got.Intercepted {
		ginkgo.Fail("required path scope alone must not ground an existing-file submit_plan edit", 1)
	}

	if !strings.Contains(got.Result.Error, "target.txt") {
		ginkgo.Fail(fmt.Sprintf("grounding rejection should name target.txt, got %q", got.Result.Error), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteScopedSubmitPlanExactAnchorGroundsEdit", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	if err := writeTestFile(root, "target.txt", "old\n"); err != nil {
		ginkgo.Fail(fmt.Sprintf("write target: %v", err), 1)
	}

	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, MaxOutputBytes: 4 << 20})
	manager.SetPermissionRules(map[string][]toolpolicy.Rule{
		workspace.ToolNameReadFile: {{Pattern: "**", Decision: toolpolicy.Allow}},
	})
	runner := newRunnerRunFixture(&Runner{
		Workspace: manager,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"target.txt"},
		},
	})
	read := manager.Execute(runner.state.ctx, workspace.ToolRequest{
		Tool: workspace.ToolNameReadFile, Path: "target.txt", StartLine: 1, EndLine: 1,
	})
	if read.Error != "" || len(read.SourceWindows()) != 1 {
		ginkgo.Fail(fmt.Sprintf("produce current source-window evidence: %#v", read), 1)
	}
	got := runner.preflightPlanThenExecuteSubmitPlanGroundingGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{{
			Path:    "target.txt",
			OldText: "old",
			NewText: "new",
			Purpose: "update target",
		}},
	}, []workspace.ToolResult{read})

	if got.Intercepted {
		ginkgo.Fail(fmt.Sprintf("exact current-source old_text anchor should ground scoped submit_plan edit, got %#v", got), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteSubmitPlanCoverageGuardRejectsPlaceholderBodiesWithTypedPath", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.go"},
		},
	})
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{{
			Path:    "a.go",
			OldText: "func run() {\n}\n",
			NewText: "func run() {\n\t// ... existing code ...\n}\n",
			Purpose: "update run",
		}},
	})

	if !got.Intercepted {
		ginkgo.Fail("submit_plan placeholder body must be rejected", 1)
	}

	if !strings.Contains(got.Result.Error, "placeholder edit body path") || !strings.Contains(got.Result.Error, "a.go") {
		ginkgo.Fail(fmt.Sprintf("rejection should name placeholder path, got %q", got.Result.Error), 1)
	}

	diagnostics := submitPlanDiagnostics(got.Result)
	if len(diagnostics) != 1 || diagnostics[0].Kind != toolfacts.SubmitPlanDiagnosticPlaceholderBody || diagnostics[0].CanonicalID != "a.go" {
		ginkgo.Fail(fmt.Sprintf("typed coverage diagnostics should name placeholder path, got %#v", diagnostics), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteSubmitPlanCoverageGuardRejectsEditWithoutPathExplicitly", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt"},
		},
	})
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{OldText: "a", NewText: "A", Purpose: "patch a required file"},
		},
	})

	if !got.Intercepted {
		ginkgo.Fail("submit_plan edit without path must be rejected", 1)
	}

	for _, want := range []string{
		"edit entry missing `path`",
		"edits[0] patch a required file",
		"`edits` as a JSON array, not a quoted string",
		"complete executable source text",
		"not placeholders, ellipses, or prose",
	} {
		if !strings.Contains(got.Result.Error, want) {
			ginkgo.Fail(fmt.Sprintf("rejection should contain %q, got %q", want, got.Result.Error), 1)
		}
	}
})

var _ = ginkgo.It("PlanThenExecuteSubmitPlanCoverageGuardRejectsOutsideRequiredPath", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt"},
		},
	})
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "a", NewText: "A"},
			{Path: "surprise.txt", OldText: "x", NewText: "X"},
		},
	})

	if !got.Intercepted {
		ginkgo.Fail("submit_plan outside required paths must be rejected", 1)
	}

	if !strings.Contains(got.Result.Error, "surprise.txt") {
		ginkgo.Fail(fmt.Sprintf("rejection should name the outside path, got %q", got.Result.Error), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteSubmitPlanCoverageGuardAcceptsFullCoverage", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	})
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "a", NewText: "A"},
			{Path: "b.txt", OldText: "b", NewText: "B"},
		},
	})

	if got.Intercepted {
		ginkgo.Fail(fmt.Sprintf("submit_plan with full required path coverage must pass, got %#v", got), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteSubmitPlanCoverageGuardAllowsCandidateWithoutRequiringIt", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"core.cpp"},
			AllowedEditPaths:  []string{"event.h"},
		},
	})

	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "core.cpp", OldText: "dispatch();", NewText: "CollapseEvent::dispatch();"},
			{Path: "event.h", OldText: "struct CollapseEvent;", NewText: "struct CollapseEvent { void dispatch(); };"},
		},
	})
	if got.Intercepted {
		ginkgo.Fail(fmt.Sprintf("allowed candidate edit should be accepted alongside required coverage, got %#v", got), 1)
	}

	missingRequired := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "event.h", OldText: "struct CollapseEvent;", NewText: "struct CollapseEvent { void dispatch(); };"},
		},
	})
	if !missingRequired.Intercepted {
		ginkgo.Fail("allowed candidate path must not satisfy required coverage by itself", 1)
	}
	if !strings.Contains(missingRequired.Result.Error, "core.cpp") {
		ginkgo.Fail(fmt.Sprintf("missing required coverage should name core.cpp, got %q", missingRequired.Result.Error), 1)
	}
	if strings.Contains(missingRequired.Result.Error, "event.h") {
		ginkgo.Fail(fmt.Sprintf("allowed candidate path should not be reported outside scope, got %q", missingRequired.Result.Error), 1)
	}

	diagnostics := submitPlanDiagnostics(missingRequired.Result)
	if len(diagnostics) != 1 || diagnostics[0].Kind != toolfacts.SubmitPlanDiagnosticMissingRequiredPath || diagnostics[0].CanonicalID != "core.cpp" {
		ginkgo.Fail(fmt.Sprintf("missing required coverage should carry typed core.cpp diagnostic, got %#v", diagnostics), 1)
	}
	coverages := runner.planThenExecuteSubmitPlanPathRoleCoverage([]string{"event.h"})
	if len(coverages) != 1 {
		ginkgo.Fail(fmt.Sprintf("expected one path-role coverage entry, got %#v", coverages), 1)
	}
	coverage := coverages[0]
	if coverage.Path != "event.h" ||
		coverage.EffectiveRole != autonomy.PathRoleAllowedEdit ||
		coverage.EffectiveInvariantID != autonomy.InvariantAllowedCandidate ||
		coverage.SatisfiesRequiredCoverage ||
		!coverage.WithinAllowedCandidatePath {

		ginkgo.Fail(fmt.Sprintf("allowed candidate coverage should be resolver-owned and scope-only, got %#v", coverage), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteSubmitPlanCoverageGuardDoesNotTreatRequiredReadsAsCoverage", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	})

	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "a", NewText: "A"},
		},
	})
	if !got.Intercepted {
		ginkgo.Fail("source-body reads are grounding evidence only; submit_plan must still cover every required edit path", 1)
	}

	diagnostics := submitPlanDiagnostics(got.Result)
	if len(diagnostics) != 1 || diagnostics[0].Kind != toolfacts.SubmitPlanDiagnosticMissingRequiredPath || diagnostics[0].CanonicalID != "b.txt" {
		ginkgo.Fail(fmt.Sprintf("required path read/grounding state must not become plan coverage, diagnostics=%#v", diagnostics), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteSubmitPlanCoverageGuardAllowsCandidateDirectoryChildren", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	generatedDir := filepath.Join(root, "pkg", "generated")
	if err := os.MkdirAll(generatedDir, 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir generated dir: %v", err), 1)
	}
	corePath := filepath.Join(root, "pkg", "core.go")
	if err := os.MkdirAll(filepath.Dir(corePath), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir core dir: %v", err), 1)
	}
	if err := os.WriteFile(corePath, []byte("package pkg\n\nfunc Run() { dispatch(); }\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write core file: %v", err), 1)
	}

	runner := newRunnerRunFixture(&Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"pkg/core.go"},
			AllowedEditPaths:  []string{"pkg/generated"},
		},
	})

	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "pkg/core.go", OldText: "dispatch();", NewText: "generated.Dispatch();"},
			{Path: "pkg/generated/helper.go", ReplaceAll: true, OldText: "", NewText: "package generated\n\nfunc Dispatch() {}\n"},
		},
	})
	if got.Intercepted {
		ginkgo.Fail(fmt.Sprintf("allowed candidate directory child edit should be accepted with required coverage, got %#v", got), 1)
	}

	missingRequired := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "pkg/generated/helper.go", ReplaceAll: true, OldText: "", NewText: "package generated\n\nfunc Dispatch() {}\n"},
		},
	})
	if !missingRequired.Intercepted {
		ginkgo.Fail("allowed candidate directory child must not satisfy required coverage by itself", 1)
	}
	if !strings.Contains(missingRequired.Result.Error, "pkg/core.go") {
		ginkgo.Fail(fmt.Sprintf("missing required coverage should name pkg/core.go, got %q", missingRequired.Result.Error), 1)
	}
	if strings.Contains(missingRequired.Result.Error, "pkg/generated/helper.go") {
		ginkgo.Fail(fmt.Sprintf("allowed candidate directory child should not be reported outside scope, got %q", missingRequired.Result.Error), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteSubmitPlanCoverageTreatsDirectoriesAsScopeNotExactCoverage", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	for _, rel := range []string{"pkg/core", "pkg/generated"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(rel)), 0o755); err != nil {
			ginkgo.Fail(fmt.Sprintf("mkdir %s: %v", rel, err), 1)
		}
	}

	runner := newRunnerRunFixture(&Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"pkg/core"},
			AllowedEditPaths:  []string{"pkg/generated"},
		},
	})

	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "pkg/generated/helper.go", ReplaceAll: true, OldText: "", NewText: "package generated\n\nfunc Dispatch() {}\n"},
		},
	})
	if got.Intercepted {
		ginkgo.Fail(fmt.Sprintf("directory scope must not become synthetic exact-file coverage, got %#v", got), 1)
	}

	withRequiredChild := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "pkg/core/dispatch.go", ReplaceAll: true, OldText: "", NewText: "package core\n\nfunc Run() {}\n"},
			{Path: "pkg/generated/helper.go", ReplaceAll: true, OldText: "", NewText: "package generated\n\nfunc Dispatch() {}\n"},
		},
	})
	if withRequiredChild.Intercepted {
		ginkgo.Fail(fmt.Sprintf("required directory child plus allowed directory child should pass, got %#v", withRequiredChild), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteSubmitPlanCoverageGuardTreatsRequiredDirectoryAsScope", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	for _, rel := range []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"} {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			ginkgo.Fail(fmt.Sprintf("mkdir %s: %v", rel, err), 1)
		}

		if err := os.WriteFile(abs, []byte("package main\n"), 0o644); err != nil {
			ginkgo.Fail(fmt.Sprintf("write %s: %v", rel, err), 1)
		}
	}

	runner := newRunnerRunFixture(&Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{
				"cmd/idiom",
				"cmd/idiom/product_workflow.go",
				"cmd/idiom/product_workflow_test.go",
			},
		},
	})
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "cmd/idiom/product_workflow.go", OldText: "package main", NewText: "package main\n// updated"},
			{Path: "cmd/idiom/product_workflow_test.go", OldText: "package main", NewText: "package main\n// covered"},
		},
	})

	if got.Intercepted {
		ginkgo.Fail(fmt.Sprintf("submit_plan should not require an edit entry for the directory scope, got %#v", got), 1)
	}

	got = runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "cmd/idiom/product_workflow.go", OldText: "package main", NewText: "package main\n// updated"},
		},
	})
	if !got.Intercepted {
		ginkgo.Fail("submit_plan missing the concrete required test file must still be rejected", 1)
	}

	diagnostics := submitPlanDiagnostics(got.Result)
	if len(diagnostics) != 1 || diagnostics[0].Kind != toolfacts.SubmitPlanDiagnosticMissingRequiredPath || diagnostics[0].CanonicalID != "cmd/idiom/product_workflow_test.go" {
		ginkgo.Fail(fmt.Sprintf("directory scope must not become missing file coverage while the concrete file remains required, got %#v", diagnostics), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteSubmitPlanCoverageGuardAllowsDirectoryScopeChildrenOnly", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "idiom"), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir cmd/idiom: %v", err), 1)
	}

	runner := newRunnerRunFixture(&Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"cmd/idiom"},
		},
	})
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "cmd/idiom/new_feature.go", Content: "package main\n"},
		},
	})

	if got.Intercepted {
		ginkgo.Fail(fmt.Sprintf("submit_plan child edit under directory scope should pass, got %#v", got), 1)
	}

	got = runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "docs/out-of-scope.md", Content: "outside\n"},
		},
	})
	if !got.Intercepted {
		ginkgo.Fail("submit_plan outside directory scope must be rejected", 1)
	}

	if !strings.Contains(got.Result.Error, "docs/out-of-scope.md") {
		ginkgo.Fail(fmt.Sprintf("outside-scope rejection should name the path, got %q", got.Result.Error), 1)
	}
})

var _ = ginkgo.It("PlanExecuteRemainingRequiredEditPathsExcludeDirectoryScope", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	for _, rel := range []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"} {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			ginkgo.Fail(fmt.Sprintf("mkdir %s: %v", rel, err), 1)
		}

		if err := os.WriteFile(abs, []byte("package main\n"), 0o644); err != nil {
			ginkgo.Fail(fmt.Sprintf("write %s: %v", rel, err), 1)
		}
	}

	runner := newRunnerRunFixture(&Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{
				"cmd/idiom",
				"cmd/idiom/product_workflow.go",
				"cmd/idiom/product_workflow_test.go",
			},
		},
	})
	missing := runner.remainingRequiredEditPaths(nil)

	if slices.Contains(missing, "cmd/idiom") {
		ginkgo.Fail(fmt.Sprintf("directory scope must not appear as a remaining required file, got %#v", missing), 1)
	}

	for _, want := range []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"} {
		if !slices.Contains(missing, want) {
			ginkgo.Fail(fmt.Sprintf("missing required file %q, got %#v", want, missing), 1)
		}
	}
})

var _ = ginkgo.It("PlanExecuteUnreadRequiredPathsExcludeParentDirectoryScope", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{
			"cmd/idiom",
			"cmd/idiom/product_workflow.go",
			"cmd/idiom/product_workflow_test.go",
		},
	}
	unread := planThenExecuteUnreadRequiredPaths(brief, nil, planThenExecutePathAuthority{})

	if _, ok := unread["cmd/idiom"]; ok {
		ginkgo.Fail(fmt.Sprintf("parent directory scope must not become a source-body obligation, got %#v", unread), 1)
	}

	for _, want := range []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"} {
		if _, ok := unread[want]; !ok {
			ginkgo.Fail(fmt.Sprintf("missing unread required file %q, got %#v", want, unread), 1)
		}
	}
})

var _ = ginkgo.It("PlanThenExecuteSubmitPlanCoverageGuardAcceptsPlanArtifactEdits", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt"},
		},
	})
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		PlanArtifact: &planartifact.PlanArtifact{
			Edits: []planartifact.PlanEdit{{Path: "a.txt", OldText: "a", NewText: "A"}},
		},
	})

	if got.Intercepted {
		ginkgo.Fail(fmt.Sprintf("submit_plan plan_artifact.edits with full required path coverage must pass, got %#v", got), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteSubmitPlanCoverageGuardValidatesPersistedPlanArtifactEditsOnly", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	})
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "a", NewText: "A"},
			{Path: "b.txt", OldText: "b", NewText: "B"},
		},
		PlanArtifact: &planartifact.PlanArtifact{
			Edits: []planartifact.PlanEdit{{Path: "a.txt", OldText: "a", NewText: "A"}},
		},
	})

	if !got.Intercepted {
		ginkgo.Fail("coverage guard must reject when persisted plan_artifact.edits misses a required path", 1)
	}

	if !strings.Contains(got.Result.Error, "b.txt") {
		ginkgo.Fail(fmt.Sprintf("rejection should name the missing persisted path, got %q", got.Result.Error), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteSubmitPlanCoverageGuardRejectsMissingRequiredSnippet", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("old behavior\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed a.txt: %v", err), 1)
	}

	runner := newRunnerRunFixture(&Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt"},
			RequiredEditSnippets: []plancontract.RequiredEditSnippet{{
				ID:      "required_edit_2",
				Path:    "a.txt",
				Content: "old behavior\nnew regression coverage",
			}},
		},
	})
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "old behavior", NewText: "old behavior\n#include \"only-one-edit\""},
		},
	})

	if !got.Intercepted {
		ginkgo.Fail("submit_plan missing a required final-state snippet must be rejected", 1)
	}

	if !strings.Contains(got.Result.Error, "required_edit_2") || !strings.Contains(got.Result.Error, "required_edit_snippets") {
		ginkgo.Fail(fmt.Sprintf("rejection should name the missing required snippet, got %q", got.Result.Error), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteSubmitPlanCoverageGuardRejectsPlanThatRemovesRequiredSnippet", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("old behavior\nnew regression coverage\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed a.txt: %v", err), 1)
	}

	runner := newRunnerRunFixture(&Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt"},
			RequiredEditSnippets: []plancontract.RequiredEditSnippet{{
				ID:      "required_edit_1",
				Path:    "a.txt",
				Content: "old behavior\nnew regression coverage",
			}},
		},
	})
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "new regression coverage\n", NewText: ""},
		},
	})

	if !got.Intercepted {
		ginkgo.Fail("plan removing a required final-state snippet must be rejected", 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteSubmitPlanCoverageGuardInactiveOutsidePlannerPhase", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	})
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "a", NewText: "A"},
		},
	})

	if got.Intercepted {
		ginkgo.Fail(fmt.Sprintf("coverage guard must stand down outside planner phase, got %#v", got), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteWriteGuardLetsReadsThrough", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	})
	for _, tool := range []string{"read_file", "list_files", "search_files", "search_content", "code_navigate"} {
		got := runner.preflightPlanThenExecuteWriteGuard(workspace.ToolRequest{Tool: tool, Path: "a.txt"})
		if got.Intercepted {
			ginkgo.Fail(fmt.Sprintf("read-class %q must pass through during planner phase, got %#v", tool, got), 1)
		}
	}
})

var _ = ginkgo.It("PlanThenExecutePostReadinessKeepsAuthorizedSearchCallable", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{
		codeNavigateSourceWindowResult(
			"cmd/idiom/product_workflow.go",
			1515,
			1640,
			0,
			"route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
		),
		codeNavigateSourceWindowResult(
			"internal/workflows/productworkflow/coverage_gate.go",
			1,
			120,
			0,
			"route_summary: Read EvaluateCoverageGate in internal/workflows/productworkflow/coverage_gate.go lines 1-120\n\n1:func EvaluateCoverageGate() {}\n",
		),
	}
	brief.AllowedTools = []string{workspace.ToolNameReadFile, workspace.ToolNameSearchContent, workspace.ToolNameSubmitPlan}
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: brief, Results: results, BaseAllowedTools: brief.AllowedTools,
		InspectionAuthority: permissionAuthorizedInspectionForTest(brief.AllowedTools...),
	})
	if !slices.Contains(contract.AllowedTools, workspace.ToolNameSearchContent) || slices.Contains(contract.ActionPolicy.InvalidActionClasses, actionSearch) {
		ginkgo.Fail(fmt.Sprintf("post-readiness authorized search must remain callable: %#v", contract))
	}
})

var _ = ginkgo.It("PlanThenExecutePostReadinessKeepsUnreadPathReadCallable", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
	}}
	brief.AllowedTools = []string{workspace.ToolNameReadFile, workspace.ToolNameSubmitPlan}
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: brief, Results: results, BaseAllowedTools: brief.AllowedTools,
		InspectionAuthority: permissionAuthorizedInspectionForTest(brief.AllowedTools...),
	})
	if !slices.Contains(contract.AllowedTools, workspace.ToolNameReadFile) || slices.Contains(contract.ActionPolicy.InvalidActionClasses, actionRead) {
		ginkgo.Fail(fmt.Sprintf("required-path read must remain callable: %#v", contract))
	}
})

var _ = ginkgo.It("PlanThenExecutePlannerToolGuardAllowsPermissionAuthorizedInspection", func() {
	allowedTools := []string{"shell", "git_diff", "git_status", "context_trace_pack", "pwd", "source_sanity", "affected_suites", "task", "task_ledger"}
	runner := newRunnerRunFixture(&Runner{
		Workspace: workspace.NewManager(config.Workspace{}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   PlanThenExecutePlannerTaskFamily,
			AllowedTools: allowedTools,
		},
	})
	for _, req := range []workspace.ToolRequest{
		{Tool: "shell", Command: "pwd"},
		{Tool: "git_diff"},
		{Tool: "git_status"},
		{Tool: "context_trace_pack", Command: "pwd"},
		{Tool: "pwd"},
	} {
		got := runner.preflightPlanThenExecutePlannerToolGuard(req)
		if got.Intercepted {
			ginkgo.Fail(fmt.Sprintf("planner phase rejected permission-authorized inspection %#v: %#v", req, got), 1)
		}
	}
	if got := runner.preflightPlanThenExecutePlannerToolGuard(workspace.ToolRequest{Tool: "shell", Command: "touch changed.txt"}); !got.Intercepted {
		ginkgo.Fail(fmt.Sprintf("planner phase must reject mutating shell request, got %#v", got), 1)
	}
	if got := runner.preflightPlanThenExecutePlannerToolGuard(workspace.ToolRequest{Tool: "context_trace_pack", Command: "touch changed.txt"}); !got.Intercepted {
		ginkgo.Fail(fmt.Sprintf("planner phase must reject mutation-capable context trace request, got %#v", got), 1)
	}

	for _, tool := range []string{"source_sanity", "affected_suites", "task", "task_ledger"} {
		got := runner.preflightPlanThenExecutePlannerToolGuard(workspace.ToolRequest{Tool: tool})
		if !got.Intercepted {
			ginkgo.Fail(fmt.Sprintf("planner phase must reject %q, got %#v", tool, got), 1)
		}

		if !strings.Contains(got.Result.Error, "current planner terminal contract") || !strings.Contains(got.Result.Error, "submit_plan") {
			ginkgo.Fail(fmt.Sprintf("rejection should point at the active planner contract, got %q", got.Result.Error), 1)
		}

		for _, forbidden := range []string{"Decision: source-satisfied", "<<QUESTION>>"} {
			if strings.Contains(got.Result.Error, forbidden) {
				ginkgo.Fail(fmt.Sprintf("planner tool rejection advertised stale terminal exit %q: %q", forbidden, got.Result.Error), 1)
			}
		}
	}
})

var _ = ginkgo.It("PlanThenExecutePlannerToolGuardDoesNotAffectReviewRepair", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecuteReviewRepairTaskFamily,
		},
	})
	if got := runner.preflightPlanThenExecutePlannerToolGuard(workspace.ToolRequest{Tool: "git_diff"}); got.Intercepted {
		ginkgo.Fail(fmt.Sprintf("review-repair may inspect diff evidence; planner-only guard must stand down, got %#v", got), 1)
	}
})

var _ = ginkgo.It("ReviewRepairCompletionGateRejectsNonDecisionFinalAnswer", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
			AllowedTools:      []string{"submit_plan"},
		},
	})
	got, rejected := runner.completionGateRejection("Let me trace the actual implementation to verify the tests against real code.", nil, 1, nil, false)

	if !rejected {
		ginkgo.Fail("review-repair must reject process narration as a final answer", 1)
	}

	if got.Tool != "completion_gate" || !strings.Contains(got.Error, "Decision: pass") || !strings.Contains(got.Error, "submit_plan") {
		ginkgo.Fail(fmt.Sprintf("review-repair rejection should name the valid closeout shapes, got %#v", got), 1)
	}
})

var _ = ginkgo.It("ReviewRepairCompletionGateAcceptsPassChecklistOrSubmittedPlan", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
			AllowedTools:      []string{"submit_plan"},
		},
	})
	passAnswer := "Decision: pass\n\nVerification Checklist:\n- Required behavior is satisfied.\n- Scope safety checked."

	if got, rejected := runner.completionGateRejection(passAnswer, nil, 1, nil, false); rejected {
		ginkgo.Fail(fmt.Sprintf("review-repair pass checklist should be accepted, got %#v", got), 1)
	}

	pathOnlyChecklist := "Decision: pass\n\nVerification Checklist:\n- `widget_config.json`\n- `widget_notes.md`\n"
	if got, rejected := runner.completionGateRejection(pathOnlyChecklist, nil, 1, nil, false); rejected {
		ginkgo.Fail(fmt.Sprintf("review-repair pass checklist must not be rejected by the generic read-only explanation gate, got %#v", got), 1)
	}

	recoveryTemplate := "The completion gate rejected the review-repair response.\n\nPass shape:\nDecision: pass\n\nVerification Checklist:\n- Required behavior: <checked fact>."
	if got, rejected := runner.completionGateRejection(recoveryTemplate, nil, 1, nil, false); !rejected {
		ginkgo.Fail(fmt.Sprintf("review-repair must not accept recovery template text as a pass, got %#v", got), 1)
	}

	results := []workspace.ToolResult{{Tool: "submit_plan", Summary: "Plan submitted with 1 edit(s); the executor phase will dispatch them."}}
	if got, rejected := runner.completionGateRejection("Plan submitted with 1 edit.", results, 2, nil, false); rejected {
		ginkgo.Fail(fmt.Sprintf("review-repair submitted corrective plan should be accepted, got %#v", got), 1)
	}
})

var _ = ginkgo.It("ReviewRepairDoesNotTriggerReadOnlyAnswerReady", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
		},
	})
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read cmd/idiom/product_workflow.go lines 280-360 (total lines: 2961)",
			Output:  "280:func runProductLint() error { return nil }",
		},
		{
			Tool:    "read_file",
			Summary: "Read cmd/idiom/product_workflow.go lines 1-50 (total lines: 2961)",
			Output:  "1:package main",
		},
	}

	if runner.readOnlyAnalysisAnswerReadyAfterResult(workspace.ToolRequest{Tool: "read_file"}, results[1], results) {
		ginkgo.Fail("review-repair is a plan-submission phase and must not receive generic read-only answer-ready nudges", 1)
	}
})

var _ = ginkgo.It("ReviewRepairCompletionGateRecoveryForcesVisibleOutput", func() {
	thinkingOn := true
	opts := completionGateRecoveryChatOptionsForBackend(llama.ChatOptions{
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        8192,
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
	}, nil, true, planThenExecutePathAuthority{}, deriveImplementationProgressState(

		workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
		}, nil))

	if opts.EnableThinking == nil || *opts.EnableThinking {
		ginkgo.Fail(fmt.Sprintf("review-repair recovery should force visible output over hidden thinking, got %#v", opts.EnableThinking), 1)
	}

	if opts.PreserveThinking {
		ginkgo.Fail("review-repair recovery must not preserve hidden reasoning in the next request", 1)
	}

	if opts.MaxTokens != 8192 {
		ginkgo.Fail(fmt.Sprintf("review-repair recovery should preserve corrective-plan budget, got %d", opts.MaxTokens), 1)
	}
})

var _ = ginkgo.It("ReviewRepairNoopSubmitPlanForcesTerminalCloseout", func() {
	t := ginkgo.GinkgoT()
	replies := []string{
		`{"tool":"submit_plan","rationale":"No corrective edits are needed.","edits":[]}`,
		"Decision: pass\n\nVerification Checklist:\n- Required behavior: checked from phase evidence.\n- Scope safety: changed paths are allowed.\n- Test or command evidence: pending outside this lane.",
	}

	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()
		if requests >= len(replies) {
			ginkgo.Fail("unexpected extra chat request", 1)
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))

	defer server.Close()

	runner := newRunnerRunFixture(&Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: t.TempDir(), CWD: "."}),
		Policy:    RunPolicy{MaxToolSteps: 5, MaxNoProgressTurns: 5},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "doom_loop_guard" {
				ginkgo.Fail(fmt.Sprintf("review-repair noop submit_plan must close before doom-loop approval: %s", event.Content), 1)
			}
		},
	})

	answer, err := runner.runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "review the applied plan"}}, llama.ChatOptions{MaxTokens: 2048}, nil, nil, nil)
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("Run: %v", err), 1)
	}

	if requests != len(replies) {
		ginkgo.Fail(fmt.Sprintf("expected one forced terminal closeout after noop submit_plan, got %d request(s)", requests), 1)
	}

	if !strings.Contains(answer, "Decision: pass") || !strings.Contains(answer, "Verification Checklist") {
		ginkgo.Fail(fmt.Sprintf("expected terminal review-repair pass, got:\n%s", answer), 1)
	}
})

var _ = ginkgo.It("PlanExecuteTextualAllowedToolsFallsBackToPhaseContract", func() {
	allowed := runnerPlanExecuteTextualAllowedTools(nil, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"context_pack", "read_file", "search_content", "code_navigate", "submit_plan"},
	}, []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/example/target.go lines 1-125 (total lines: 391)",
		Output:  "package example\n",
	}}, permissionAuthorizedInspectionForTest("context_pack", "read_file", "search_content", "code_navigate", "submit_plan"), planThenExecutePathAuthority{})

	if !slices.Contains(allowed, "read_file") {
		ginkgo.Fail(fmt.Sprintf("textual plan-execute surface should preserve phase read tools, got %#v", allowed), 1)
	}

	if slices.Contains(allowed, "submit_plan") {
		ginkgo.Fail(fmt.Sprintf("candidate-source phase must not label invalid submit_plan as allowed, got %#v", allowed), 1)
	}
})

var _ = ginkgo.It("PlanExecuteSelectedCandidateWithSourceAndHistoryExposesSubmitAdditively", func() {
	t := ginkgo.GinkgoT()
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "search_content", "history_range", "submit_plan"},
		RequiredEvidence:  []string{string(workerruntime.RequiredEvidenceGitHistoryRationale)},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack selected the implementation target",
			Facts:   contextPackCandidateFactsForTest("internal/example/target.go"),
		},
		readFileSourceWindowResult("internal/example/target.go", 1, 20, 20, "1:package example\n"),
		gitHistoryRangeResult("internal/example/target.go"),
	}

	allowed := runnerPlanExecuteTextualAllowedTools(
		[]string{workspace.ToolNameSubmitPlan},
		brief,
		results,
		permissionAuthorizedInspectionForTest(brief.AllowedTools...),
		planThenExecutePathAuthority{},
	)

	assertContains(t, allowed, workspace.ToolNameSubmitPlan)
	assertContains(t, allowed, workspace.ToolNameReadFile)
	assertContains(t, allowed, "search_content")
	assertContains(t, allowed, "history_range")
})

var _ = ginkgo.It("PlanExecuteTextualAllowedToolsExposeSubmitForTypedGroundingCorrection", func() {
	allowed := runnerPlanExecuteTextualAllowedTools(nil, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"context_pack", "read_file", "search_content", "code_navigate", "submit_plan"},
	}, nil, permissionAuthorizedInspectionForTest("context_pack", "read_file", "search_content", "code_navigate", "submit_plan"), planThenExecutePathAuthority{})

	if !slices.Contains(allowed, "read_file") {
		ginkgo.Fail(fmt.Sprintf("textual plan-execute surface should preserve candidate read tools, got %#v", allowed), 1)
	}

	if slices.Contains(allowed, "submit_plan") {
		ginkgo.Fail(fmt.Sprintf("candidate-source phase must not expose submit_plan until a selected path makes it callable, got %#v", allowed), 1)
	}
})

var _ = ginkgo.It("ReviewRepairTextualAllowedToolsKeepsInitialProbeSurface", func() {
	t := ginkgo.GinkgoT()
	allowed := runnerPlanExecuteTextualAllowedTools(nil, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
		AllowedTools:      []string{"read_file", "search_content", "submit_plan"},
	}, nil, permissionAuthorizedInspectionForTest("read_file", "search_content", "submit_plan"), planThenExecutePathAuthority{})

	assertContains(t, allowed, "read_file")
	assertContains(t, allowed, "submit_plan")
	assertContains(t, allowed, "search_content")
})

var _ = ginkgo.It("PlanThenExecuteWriteGuardInactiveOutsidePlannerPhase", func() {
	// Same edit_file call that gets rejected during the planner phase
	// must pass through cleanly under any other lane shape.
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{TaskFamily: "implementation"},
	})
	got := runner.preflightPlanThenExecuteWriteGuard(workspace.ToolRequest{Tool: "edit_file", Path: "a.txt"})

	if got.Intercepted {
		ginkgo.Fail(fmt.Sprintf("guard must stand down outside the planner phase, got %#v", got), 1)
	}
})

var _ = ginkgo.It("MutationCompletionGateAcceptsSubmitPlanInPlannerPhase", func() {
	// Plan-execute planner phase: submit_plan succeeded but no files
	// were mutated (the executor phase mutates files in a separate
	// process). The completion gate must NOT reject the answer for
	// "no successful write tool result is present" — submit_plan IS
	// the successful write here.
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	})
	results := []workspace.ToolResult{
		{Tool: "submit_plan", Summary: "Plan submitted with 2 edit(s); the executor phase will dispatch them."},
	}
	got, rejected := runner.mutationCompletionRejection("Plan submitted with 2 edits.", results)

	if rejected {
		ginkgo.Fail(fmt.Sprintf("planner phase with successful submit_plan must not be rejected by mutation gate; got %#v", got), 1)
	}
})

var _ = ginkgo.It("MutationCompletionGateAcceptsPlannerSourceSatisfiedWithReadEvidence", func() {
	manager, results := sourceWindowManagerResultsForTest("a.txt", "b.txt")
	runner := newRunnerRunFixture(&Runner{
		Workspace: manager,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	})
	answer := "Decision: source-satisfied\n- a.txt: already implemented; no changes needed.\n- b.txt: already implemented; no changes needed."

	if got, rejected := runner.mutationCompletionRejection(answer, results); rejected {
		ginkgo.Fail(fmt.Sprintf("source-satisfied planner answer with read evidence should pass, got %#v", got), 1)
	}

	if kind, reason := runner.finalAnswerBlockerKind(answer, results); kind != "" || reason != "" {
		ginkgo.Fail(fmt.Sprintf("source-satisfied planner answer should not be converted to blocker, got kind=%q reason=%q", kind, reason), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteHarnessDiagnosticDoesNotSatisfyRequiredPathBlockerEvidence", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "autonomy_event",
			Summary: `{"type":"gate_decision","gate":{"gate":"completion_gate","safe_alternatives":["<<QUESTION>>:","Decision: source-satisfied"],"observed_facts":["path_ref:target_refs=a.txt:pending","path_ref:target_refs=b.txt:pending"]}}`,
		},
	}

	unread := planThenExecuteUnreadRequiredPaths(brief, results, planThenExecutePathAuthority{})
	for _, path := range []string{"a.txt", "b.txt"} {
		if _, ok := unread[path]; !ok {
			ginkgo.Fail(fmt.Sprintf("harness diagnostic must not satisfy required source coverage for %s: %#v", path, unread), 1)
		}

		if planThenExecuteRequiredPathState(planThenExecutePathAuthority{}, results, path) != planThenExecuteRequiredPathExistingSourceBodyRequired {
			ginkgo.Fail(fmt.Sprintf("harness diagnostic must not become blocker evidence for %s", path), 1)
		}
	}
})

var _ = ginkgo.It("MutationCompletionGateAcceptsUnscopedPlannerSourceSatisfiedWithReadEvidence", func() {
	manager, results := sourceWindowManagerResultsForTest("pkg/feature.go", "pkg/feature_test.go")
	runner := newRunnerRunFixture(&Runner{
		Workspace: manager,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	})
	answer := "Decision: source-satisfied\n- pkg/feature.go: requested final state is already present.\n- pkg/feature_test.go: current test coverage already contains the regression."

	if got, rejected := runner.mutationCompletionRejection(answer, results); rejected {
		ginkgo.Fail(fmt.Sprintf("unscoped source-satisfied planner answer with read evidence should pass, got %#v", got), 1)
	}

	if kind, reason := runner.finalAnswerBlockerKind(answer, results); kind != "" || reason != "" {
		ginkgo.Fail(fmt.Sprintf("unscoped source-satisfied planner answer should not be converted to blocker, got kind=%q reason=%q", kind, reason), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteSourceSatisfiedAnswerCoversPathsRequiresExplicitDecision", func() {
	tests := []struct {
		name   string
		answer string
		want   bool
	}{
		{
			name:   "hyphenated explicit decision",
			answer: "Decision: source-satisfied\n- a.txt: already implemented; no changes needed.\n- b.txt: already implemented; no changes needed.",
			want:   true,
		},
		{
			name:   "spaced explicit decision",
			answer: "Decision: source satisfied\n- a.txt: already implemented; no changes needed.\n- b.txt: already implemented; no changes needed.",
			want:   true,
		},
		{
			name:   "already present final state",
			answer: "Decision: source-satisfied\n- a.txt: requested final state is already present.\n- b.txt: current source already contains the requested behavior.",
			want:   true,
		},
		{
			name:   "question mentioning accepted remedy",
			answer: "<<QUESTION>>: Completion gate rejected before submit_plan.\n- a.txt: no successful edit was recorded; provide submit_plan, Decision: source-satisfied evidence, or a path-specific blocker.\n- b.txt: same blocker.",
			want:   false,
		},
		{
			name:   "blocker mentioning accepted remedy",
			answer: "Blocked: a.txt and b.txt need submit_plan or Decision: source-satisfied evidence before closeout.",
			want:   false,
		},
		{
			name:   "punctuated decision is not exact",
			answer: "Decision: source-satisfied:\n- a.txt: already implemented; no changes needed.\n- b.txt: already implemented; no changes needed.",
			want:   false,
		},
		{
			name:   "legacy implicit already-present wording",
			answer: "- a.txt: already implemented; no changes needed.\n- b.txt: already implemented; no changes needed.",
			want:   false,
		},
	}
	for _, tt := range tests {
		ginkgo.By(tt.name, func() {
			got := plancontract.SourceSatisfiedAnswerCoversPaths(tt.answer, []string{"a.txt", "b.txt"})
			if got != tt.want {
				ginkgo.Fail(fmt.Sprintf("SourceSatisfiedAnswerCoversPaths() = %v, want %v", got, tt.want), 1)
			}
		})
	}
})

var _ = ginkgo.It("PlanThenExecuteSourceSatisfiedEvidencePathsRequiresExplicitDecision", func() {
	answer := "Decision: source-satisfied\n- internal/a.go: no changes needed.\n- internal/a_test.go: no changes needed."
	paths := plancontract.SourceSatisfiedEvidencePaths(answer)
	want := []string{"internal/a.go", "internal/a_test.go"}

	if !slices.Equal(paths, want) {
		ginkgo.Fail(fmt.Sprintf("evidence paths = %#v, want %#v", paths, want), 1)
	}

	blocker := "<<QUESTION>>: provide Decision: source-satisfied evidence for internal/a.go"
	if paths := plancontract.SourceSatisfiedEvidencePaths(blocker); len(paths) != 0 {
		ginkgo.Fail(fmt.Sprintf("blocker text must not produce source-satisfied evidence paths: %#v", paths), 1)
	}
})

var _ = ginkgo.It("MutationCompletionGateRejectsPlannerSourceSatisfiedWithoutPathEvidence", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	})
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.txt", 1, 10, 0, "1:current a\n"),
	}
	answer := "Decision: source-satisfied\n- a.txt: already implemented; no changes needed.\n- b.txt: already implemented; no changes needed."
	got, rejected := runner.mutationCompletionRejection(answer, results)

	if !rejected {
		ginkgo.Fail("source-satisfied answer without evidence for every required path must be rejected", 1)
	}

	if !strings.Contains(got.Error, "source-satisfied") {
		ginkgo.Fail(fmt.Sprintf("rejection should explain the accepted source-satisfied shape, got %#v", got), 1)
	}
})

var _ = ginkgo.It("RequiredPathCompletionRecoveryRejectionPreservesSubmitPlanDetails", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	})
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.txt", 1, 10, 10, "1:current a\n"),
		readFileSourceWindowResult("b.txt", 1, 10, 10, "1:current b\n"),
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "plan_artifact.schema_errors edits[0]:invalid_edit_shape:insert_after_line content exceeds inline submit_plan limit (4096 bytes); content_ref is not supported for insert_after_line in this schema",
			Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
				Code: workspace.SubmitPlanOversizedInsertionCode, Kind: toolfacts.SubmitPlanDiagnosticOversizedInsertion, CanonicalID: "a.txt",
			}),
		},
	}

	result, rejected := runner.requiredPathCompletionRecoveryRejection("Done.", results)
	if !rejected {
		ginkgo.Fail("expected non-specific completion to be rejected", 1)
	}

	for _, want := range []string{
		"Previous rejected submit_plan drafts do not count as coverage",
		"Last submit_plan rejection",
		"Next submit_plan coverage checklist",
		"Do not repeat `insert_after_line`",
		"start_line",
		"end_line",
	} {
		if !strings.Contains(result.Error, want) {
			ginkgo.Fail(fmt.Sprintf("completion rejection missing %q:\n%s", want, result.Error), 1)
		}
	}
})

var _ = ginkgo.It("CompletionGateRecoveryPromptPointsPlanExecuteAtFirstRequiredReadWhenNoEvidence", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	})

	prompt, ok := runner.completionGateRecoveryPrompt(nil)
	if !ok {
		ginkgo.Fail("expected completion gate recovery prompt for planner phase with missing required paths", 1)
	}

	if unread := sortedRunnerPathKeys(planThenExecuteUnreadRequiredPaths(runner.state.runtimeBrief, nil, runner.planThenExecutePathAuthority())); !slices.Equal(unread, []string{"a.txt", "b.txt"}) {
		ginkgo.Fail(fmt.Sprintf("typed unread required paths = %#v, want both required paths", unread), 1)
	}

	for _, want := range []string{
		"source-body evidence",
		"Do not draft `submit_plan` from outline/search hits",
		"Source-window tools are available",
		"permission-authorized inspection",
	} {
		if !strings.Contains(prompt, want) {
			ginkgo.Fail(fmt.Sprintf("recovery prompt missing %q: %q", want, prompt), 1)
		}
	}

	for _, notWant := range []string{
		"Every required path already has source-body evidence",
		"read/search/discovery tools are hidden now",
		"Your next response must be a parser-readable `submit_plan` request",
	} {
		if strings.Contains(prompt, notWant) {
			ginkgo.Fail(fmt.Sprintf("recovery prompt should not claim submit_plan readiness before source evidence (%q): %q", notWant, prompt), 1)
		}
	}
})

var _ = ginkgo.It("CompletionGateRecoveryPromptPrioritizesUnreadRequiredSource", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go", "b.go"},
		},
	})
	results := []workspace.ToolResult{
		codeNavigateSourceWindowResult("a.go", 1, 10, 0, "route_summary: Read A in a.go lines 1-10\n\n1:func A() {}\n"),
		{Tool: "code_navigate", Summary: "Code navigation routed file_outline via go_file_outline", Output: "route_summary: Outlined 2 top-level declaration(s) in b.go.\n"},
		{Tool: "search_content", Summary: "Found 2 matching line(s).", Output: "b.go:12:func TestB(t *testing.T)"},
	}

	prompt, ok := runner.completionGateRecoveryPrompt(results)
	if !ok {
		ginkgo.Fail("expected completion gate recovery prompt", 1)
	}

	if unread := sortedRunnerPathKeys(planThenExecuteUnreadRequiredPaths(runner.state.runtimeBrief, results, runner.planThenExecutePathAuthority())); !slices.Equal(unread, []string{"b.go"}) {
		ginkgo.Fail(fmt.Sprintf("typed unread required paths = %#v, want b.go", unread), 1)
	}

	for _, want := range []string{
		"source-body evidence",
		"Do not draft `submit_plan` from outline/search hits",
		"Source-window tools are available",
		"permission-authorized inspection",
	} {
		if !strings.Contains(prompt, want) {
			ginkgo.Fail(fmt.Sprintf("recovery prompt missing %q:\n%s", want, prompt), 1)
		}
	}
})

var _ = ginkgo.It("PlanExecuteRequiredSourceFollowupRedirectAllowsAdvancingRange", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"internal/workflows/productworkflow/coverage_gate.go"},
		},
	})
	results := []workspace.ToolResult{
		readFileSourceWindowResult("internal/workflows/productworkflow/coverage_gate.go", 1, 180, 338, "1:package productworkflow"),
	}
	req := workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "internal/workflows/productworkflow/coverage_gate.go",
		StartLine: 181,
		EndLine:   360,
	}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: req,
		Raw:     marshalToolRequestRaw(req),
		Results: results,
	})
	if decision.Request.Tool != req.Tool || decision.Request.Path != req.Path || decision.Request.StartLine != req.StartLine || decision.Request.EndLine != req.EndLine {
		ginkgo.Fail(fmt.Sprintf("bounded follow-up range should be allowed as advancing evidence, got %#v", decision.Request), 1)
	}
})

var _ = ginkgo.It("PlanExecuteRequiredSourceFollowupRedirectPreservesInvalidAnchorRefresh", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
		},
	})
	results := []workspace.ToolResult{
		codeNavigateSourceWindowResult(
			"cmd/idiom/product_workflow.go",
			1515,
			1640,
			0,
			"route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
		),
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `cmd/idiom/product_workflow.go: old_text not found in current source`; missing required edit path(s): `internal/workflows/productworkflow/coverage_gate.go`.",
		},
	}
	req := workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow.go",
		StartLine: 1515,
		EndLine:   1640,
	}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: req,
		Raw:     marshalToolRequestRaw(req),
		Results: results,
	})
	if decision.Request.Tool != req.Tool || decision.Request.Path != req.Path || decision.Request.StartLine != req.StartLine || decision.Request.EndLine != req.EndLine {
		ginkgo.Fail(fmt.Sprintf("bounded invalid-anchor refresh should not be redirected, got %#v", decision.Request), 1)
	}
})

var _ = ginkgo.It("CompletionGateRecoveryPromptPointsCandidateScopeAtToolAction", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	})

	prompt, ok := runner.completionGateRecoveryPrompt(nil)
	if !ok {
		ginkgo.Fail("expected completion gate recovery prompt for planner phase with candidate scope", 1)
	}

	for _, want := range []string{
		"candidate/allowed scope",
		"No required_edit_paths list is active",
		"not the same prose",
		"parser-readable action",
		"Do not include analysis before or after",
		"hidden reasoning",
		"ignored by the tool parser",
		"`submit_plan`",
		"one bounded read/search on the highest-confidence candidate path",
		"named missing edit-safety fact",
		"source-satisfied",
		"<<QUESTION>>",
	} {
		if !strings.Contains(prompt, want) {
			ginkgo.Fail(fmt.Sprintf("candidate-scope recovery prompt missing %q: %q", want, prompt), 1)
		}
	}

	if strings.Contains(prompt, "edit_file") || strings.Contains(prompt, "apply_patch") {
		ginkgo.Fail(fmt.Sprintf("planner-phase recovery must not point at per-edit tools: %q", prompt), 1)
	}

	for _, forbidden := range []string{"candidate/path.go", "exact current text", "replacement text", "why these candidate path edits satisfy the objective"} {
		if strings.Contains(prompt, forbidden) {
			ginkgo.Fail(fmt.Sprintf("candidate-scope recovery must not include copyable placeholder %q:\n%s", forbidden, prompt), 1)
		}
	}
})

var _ = ginkgo.It("CompletionGateRecoveryPromptKeepsUnreadCandidateReadVisible", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedEditPaths:  []string{"pkg/feature.go", "pkg/feature_test.go"},
		},
	})

	prompt, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		readFileSourceWindowResult("pkg/feature.go", 1, 4, 4, "1:package pkg\n2:\n3:func Hardened() bool { return true }\n"),
	})
	if !ok {
		ginkgo.Fail("expected completion gate recovery prompt for candidate scope", 1)
	}

	for _, want := range []string{
		"Read candidate path(s): pkg/feature.go",
		"source-satisfied closeout depends on another candidate path",
		"pkg/feature_test.go",
		"Do not reread a candidate path that already has source evidence",
	} {
		if !strings.Contains(prompt, want) {
			ginkgo.Fail(fmt.Sprintf("candidate recovery prompt missing %q:\n%s", want, prompt), 1)
		}
	}

	if strings.Contains(prompt, "Read/search tools are hidden") {
		ginkgo.Fail(fmt.Sprintf("candidate recovery must not hide reads before unread candidate evidence can be gathered:\n%s", prompt), 1)
	}
})

var _ = ginkgo.It("CompletionGateRecoveryPromptForCandidateScopeFinalAnswerHidesReads", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedEditPaths:  []string{"pkg/feature.go", "pkg/feature_test.go"},
		},
	})

	prompt, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		readFileSourceWindowResult("pkg/feature.go", 1, 4, 4, "1:package pkg\n2:\n3:func Hardened() bool { return true }\n"),
		{
			Tool:    "completion_gate",
			Summary: "Plan-execute completion gate rejected final answer",
			Error:   "plan-execute planner phase requires a successful submit_plan tool result before final answer",
		},
	})
	if !ok {
		ginkgo.Fail("expected completion gate recovery prompt for candidate scope", 1)
	}

	for _, want := range []string{
		"Allowed/candidate paths are not coverage obligations",
		"one concrete edit-safety fact is still missing",
		"any bounded read/search/navigation action is one visible JSON object",
		"top-level `tool`, `rationale`, and `edits` keys",
	} {
		if !strings.Contains(prompt, want) {
			ginkgo.Fail(fmt.Sprintf("candidate final-answer recovery prompt missing %q:\n%s", want, prompt), 1)
		}
	}

	if !strings.Contains(prompt, "pkg/feature_test.go") {
		ginkgo.Fail(fmt.Sprintf("terminal recovery should surface unread candidate paths as optional bounded reads after a rejected final answer:\n%s", prompt), 1)
	}
})

var _ = ginkgo.It("PlanExecuteCandidateScopeRecoveryPreservesThinking", func() {
	thinkingOn := true
	opts := completionGateRecoveryChatOptionsForBackend(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		ModelThinkingFloor: false,
		MaxTokens:          16384,
		Tools:              []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}, nil, true, planThenExecutePathAuthority{}, deriveImplementationProgressState(

		workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		}, nil))

	if opts.EnableThinking == nil || !*opts.EnableThinking {
		ginkgo.Fail(fmt.Sprintf("candidate-scope recovery should preserve thinking mode, got %#v", opts.EnableThinking), 1)
	}

	if !opts.PreserveThinking {
		ginkgo.Fail("candidate-scope recovery should preserve hidden reasoning policy", 1)
	}

	if opts.ChatTemplateKwargs {
		ginkgo.Fail("candidate-scope recovery should not force request-level chat_template_kwargs", 1)
	}

	if opts.MaxTokens != 4096 {
		ginkgo.Fail(fmt.Sprintf("candidate-scope recovery MaxTokens = %d, want 4096", opts.MaxTokens), 1)
	}

	if opts.ToolChoice != "" {
		ginkgo.Fail(fmt.Sprintf("candidate-scope recovery should not force backend required-tool mode, got %q", opts.ToolChoice), 1)
	}
})

var _ = ginkgo.It("PlanExecuteCompletionRecoveryCapsUnreadRequiredSourceCall", func() {
	thinkingOn := true
	opts := completionGateRecoveryChatOptionsForBackend(llama.ChatOptions{
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        16384,
		Tools:            []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go", "b.go"},
	}, []workspace.ToolResult{
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "route_summary: Read A in a.go lines 1-10\n\n1:func A() {}\n"},
		{Tool: "code_navigate", Summary: "Code navigation routed file_outline via go_file_outline", Output: "route_summary: Outlined 2 top-level declaration(s) in b.go.\n"},
	}, true, planThenExecutePathAuthority{}, deriveImplementationProgressState(

		workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go", "b.go"},
		}, []workspace.ToolResult{
			{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "route_summary: Read A in a.go lines 1-10\n\n1:func A() {}\n"},
			{Tool: "code_navigate", Summary: "Code navigation routed file_outline via go_file_outline", Output: "route_summary: Outlined 2 top-level declaration(s) in b.go.\n"},
		}))

	if opts.EnableThinking == nil || *opts.EnableThinking {
		ginkgo.Fail("unread required source recovery should force thinking off for the next short tool call", 1)
	}

	if opts.PreserveThinking {
		ginkgo.Fail("unread required source recovery should not preserve prior hidden reasoning", 1)
	}

	if opts.MaxTokens != 768 {
		ginkgo.Fail(fmt.Sprintf("MaxTokens = %d, want 768", opts.MaxTokens), 1)
	}
})

var _ = ginkgo.It("PlanExecuteCandidateScopeRecoveryDropsPriorThinkingAfterRead", func() {
	thinkingOn := true
	opts := completionGateRecoveryChatOptionsForBackend(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          16384,
		Tools:              []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}, []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 internal/surfaces/tui/app/rendering.go lines 1-120"}}, true, planThenExecutePathAuthority{}, deriveImplementationProgressState(

		workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		}, []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 internal/surfaces/tui/app/rendering.go lines 1-120"}}))

	if opts.EnableThinking == nil || *opts.EnableThinking {
		ginkgo.Fail(fmt.Sprintf("post-read plan-execute recovery should force visible output over hidden thinking, got %#v", opts.EnableThinking), 1)
	}

	if opts.PreserveThinking {
		ginkgo.Fail("post-read plan-execute recovery should stop preserving prior hidden reasoning", 1)
	}

	if !opts.ChatTemplateKwargs {
		ginkgo.Fail("post-read plan-execute recovery should force request-level thinking kwargs", 1)
	}

	if opts.ToolChoice == "required" {
		ginkgo.Fail(fmt.Sprintf("post-read plan-execute recovery should not force submit_plan until terminal readiness is reached, got %q", opts.ToolChoice), 1)
	}
})

var _ = ginkgo.It("PlanExecuteTextualAllowedToolsSubmitReadyDoesNotUseStaleReadSurface", func() {
	t := ginkgo.GinkgoT()
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "code_navigate", "history_range", "history_symbol"},
		RequiredEvidence:  []string{string(workerruntime.RequiredEvidenceGitHistoryRationale)},
		RequiredEditPaths: []string{"native/src/core/colony_sim_tick.cpp", "native/src/simulation/world/collapse_event.cpp", "native/tests/test_collapse_event.cpp"},
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("native/src/core/colony_sim_tick.cpp", 1, 180, 2616, "1:#include \"simulation/world/collapse_event.h\"\n"),
		{
			Tool:    "read_file",
			Summary: "Required new-file path does not exist yet",
			Error:   "path \"native/src/simulation/world/collapse_event.cpp\" is in your required_edit_paths but does not exist on disk yet - this is a NEW FILE you must create.",
		},
		{
			Tool:    "read_file",
			Summary: "Required new-file path does not exist yet",
			Error:   "path \"native/tests/test_collapse_event.cpp\" is in your required_edit_paths but does not exist on disk yet - this is a NEW FILE you must create.",
		},
		gitHistoryRangeResult("native/src/core/colony_sim_tick.cpp"),
	}

	if state := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{}); state.Mode != PlanExecutePlanningModeRequiredSource {
		t.Skipf("mode=%q — test expects required_source; submit-ready contract changed", state.Mode)
	}
})

var _ = ginkgo.It("PlanExecuteTextualAllowedToolsPreservesSourceSatisfiedTerminalNoToolContract", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "code_navigate", "submit_plan"},
		RequiredEditPaths: []string{"a.go"},
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: source-satisfied terminal shape required",
			Error:   "submit_plan cannot carry a source-satisfied/no-op closeout. Answer `Decision: source-satisfied` with per-path current-state evidence, submit real edits, or answer <<QUESTION>> with the exact blocker.",
			Code:    workspace.SubmitPlanSourceSatisfiedTerminalRequiredCode,
		},
	}

	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
	if allowed := runner.runnerPlanExecuteTextualAllowedToolsForRequest(
		[]string{"read_file", "code_navigate"},
		brief,
		results,
		workspace.ToolRequest{Tool: "submit_plan"},
		permissionAuthorizedInspectionForTest(brief.AllowedTools...), planThenExecutePathAuthority{}); !slices.Contains(allowed, workspace.ToolNameReadFile) {
		ginkgo.Fail(fmt.Sprintf("source-satisfied terminal contract must preserve permission-authorized reads, got %#v", allowed), 1)
	}
})

var _ = ginkgo.It("PlanExecuteCandidateTerminalSubmitPlanDoesNotRequireEveryAllowedCandidate", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"pkg/feature.go", "pkg/feature_test.go"},
	}
	if !planThenExecuteTerminalSubmitPlanAllowed(brief, []workspace.ToolResult{
		readFileSourceWindowResult("pkg/feature.go", 1, 4, 4, ""),
	}, planThenExecutePathAuthority{}) {

		ginkgo.Fail("one selected allowed candidate with source evidence should make submit_plan callable without making every allowed path mandatory", 1)
	}

	if !planThenExecuteTerminalSubmitPlanAllowed(brief, []workspace.ToolResult{
		readFileSourceWindowResult("pkg/feature.go", 1, 4, 4, ""),
		readFileSourceWindowResult("pkg/feature_test.go", 1, 4, 4, ""),
	}, planThenExecutePathAuthority{}) {

		ginkgo.Fail("additional allowed-candidate evidence should preserve submit_plan availability", 1)
	}
})

var _ = ginkgo.It("PlanExecuteOpenCandidateTerminalAllowsSubmitWithoutHidingReads", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "context pack",
			Facts:   contextPackCandidateFactsForTest("internal/example/target.go"),
		},
		readFileSourceWindowResult("internal/example/target.go", 1, 80, 80, ""),
	}

	if !planThenExecuteTerminalSubmitPlanAllowed(brief, results, planThenExecutePathAuthority{}) {
		ginkgo.Fail("one selected unscoped candidate with source evidence should make submit_plan callable", 1)
	}

})

var _ = ginkgo.It("PlanExecuteOpenCandidateTerminalDoesNotRequireEveryContextCandidate", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "context pack",
			Facts:   contextPackCandidateFactsForTest("pkg/feature.go", "pkg/feature_test.go"),
		},
		readFileSourceWindowResult("pkg/feature.go", 1, 80, 80, "1:package pkg\n"),
	}

	if !planThenExecuteTerminalSubmitPlanAllowed(brief, results, planThenExecutePathAuthority{}) {
		ginkgo.Fail("selected context candidate with source evidence should make submit_plan callable without making every discovered candidate mandatory", 1)
	}

})

var _ = ginkgo.It("PlanExecuteCompletionRecoveryKeepsReadsForUnreadContextCandidate", func() {
	thinkingOn := true
	opts := completionGateRecoveryChatOptionsForBackend(llama.ChatOptions{
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        16384,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "search_content"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}, []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "context pack",
			Facts:   contextPackCandidateFactsForTest("pkg/feature.go", "pkg/feature_test.go"),
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 pkg/feature.go lines 1-80 (total lines: 80)",
			Output:  "1:package pkg\n",
		},
	}, true, planThenExecutePathAuthority{}, deriveImplementationProgressState(

		workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		}, []workspace.ToolResult{
			{
				Tool:    "context_pack",
				Summary: "context pack",
				Facts:   contextPackCandidateFactsForTest("pkg/feature.go", "pkg/feature_test.go"),
			},
			{
				Tool:    "read_file",
				Summary: "Read f1 pkg/feature.go lines 1-80 (total lines: 80)",
				Output:  "1:package pkg\n",
			},
		}))

	if opts.ToolChoice == "required" {
		ginkgo.Fail("completion recovery with unread context candidates must not force terminal submit_plan mode", 1)
	}

	if !chatOptionsExposeTool(opts, "read_file") || !chatOptionsExposeTool(opts, "search_content") || !chatOptionsExposeTool(opts, "submit_plan") {
		ginkgo.Fail(fmt.Sprintf("completion recovery should keep read/search and submit_plan available, got %#v", opts.Tools), 1)
	}
})

var _ = ginkgo.It("PlanExecuteLoopBudgetRecoveryAfterSymbolMissKeepsUnreadCandidatePath", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	})
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/test.json with 2 candidate file(s)",
			Facts: contextPackCandidateFactsForTest(
				"internal/workflows/productworkflow/coverage_gate.go",
				"internal/productcmd/product_workflow.go",
			),
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "capability: read_symbol\nlanguage: go\nrouted_tool: go_read_symbol\nroute_summary: No Go symbols matched \"CoverageGate\"",
		},
	}

	recovery, ok := runner.planThenExecuteLoopBudgetRecoveryPrompt(
		workspace.ToolRequest{Tool: "code_navigate", Path: "internal/workflows/productworkflow/coverage_gate.go", Capability: "read_symbol", Pattern: "CoverageGate"},
		results,
		"the model repeated code_navigate with the same arguments 3 turns in a row",
		false,
	)
	if !ok {
		ginkgo.Fail("expected loop recovery to stay model-owned on unread candidate paths after a symbol miss", 1)
	}

	for _, want := range []string{
		"Unread candidate path(s):",
		"internal/productcmd/product_workflow.go",
		"internal/workflows/productworkflow/coverage_gate.go",
		"one bounded source-body read/search for a model-selected unread candidate path",
		"a bounded read on that same candidate path is valid",
		"Do not call the same tool with the same arguments again",
	} {
		if !strings.Contains(recovery.Prompt, want) {
			ginkgo.Fail(fmt.Sprintf("recovery prompt missing %q:\n%s", want, recovery.Prompt), 1)
		}
	}
})

var _ = ginkgo.It("PlanExecuteLoopBudgetRecoveryDoesNotUseUnreadCandidateEscapeAfterSourceBody", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	})
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack",
			Facts: contextPackCandidateFactsForTest(
				"internal/workflows/productworkflow/coverage_gate.go",
				"internal/productcmd/product_workflow.go",
			),
		},
		readFileSourceWindowResult(
			"internal/workflows/productworkflow/coverage_gate.go",
			1,
			80,
			320,
			"1:package productworkflow\n2:func EvaluateCoverageGate() {}\n",
		),
	}

	recovery, ok := runner.planThenExecuteLoopBudgetRecoveryPrompt(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/workflows/productworkflow/coverage_gate.go"},
		results,
		"the model repeated read_file with the same arguments 3 turns in a row",
		false,
	)
	if !ok {
		ginkgo.Fail("source-body evidence should still get terminal loop recovery", 1)
	}

	if !strings.Contains(recovery.Prompt, "permission-authorized inspection") {
		ginkgo.Fail(fmt.Sprintf("source-body recovery must preserve authorized inspection:\n%s", recovery.Prompt), 1)
	}
})

var _ = ginkgo.It("PlanExecuteUnscopedEmptySubmitPlanRejectionReopensCandidateSurface", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/test.json with 2 candidate file(s)",
			Facts:   contextPackCandidateFactsForTest("pkg/feature.go", "pkg/feature_test.go"),
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 pkg/feature.go lines 1-80 (total lines: 80)",
			Output:  "1:package pkg\n2:\n3:func Label() string { return \"long label\" }\n",
		},
		{
			Tool:    "submit_plan",
			Code:    workspace.SubmitPlanEmptyEditsCode,
			Summary: "submit_plan rejected",
			Error:   "submit_plan requires at least one edit in the edits array",
		},
	}

	if !planThenExecuteUnscopedEmptySubmitPlanRejected(brief, results) {
		ginkgo.Fail("empty submit_plan rejection should be detected from the typed result code", 1)
	}

	if planThenExecuteSubmitPlanOnlyReady(brief, results, planThenExecutePathAuthority{}) {
		ginkgo.Fail("unscoped empty submit_plan rejection must not keep the planner in submit-plan-only mode", 1)
	}

	if planThenExecuteTerminalSubmitPlanAllowed(brief, results, planThenExecutePathAuthority{}) {
		ginkgo.Fail("unscoped empty submit_plan rejection must reopen candidate source/search surface", 1)
	}

	if planThenExecuteRejectedPlanCorrectionOnly(brief, results) {
		ginkgo.Fail("unscoped empty submit_plan rejection is salvage, not submit-plan-only correction", 1)
	}

	allowed := runnerPlanExecuteTextualAllowedTools(
		[]string{"context_pack", "read_file", "code_navigate", "submit_plan"},
		brief,
		results,
		permissionAuthorizedInspectionForTest("context_pack", "read_file", "code_navigate", "submit_plan"), planThenExecutePathAuthority{})

	if slices.Equal(allowed, []string{"submit_plan"}) {
		ginkgo.Fail(fmt.Sprintf("textual allowed tools collapsed to submit_plan after empty rejection: %#v", allowed), 1)
	}

	if !slices.Contains(allowed, "read_file") || !slices.Contains(allowed, "submit_plan") {
		ginkgo.Fail(fmt.Sprintf("candidate recovery should keep both source evidence and non-empty submit_plan available, got %#v", allowed), 1)
	}
})

var _ = ginkgo.It("PlanExecuteUnscopedEmptySubmitPlanRejectionRequiresTypedCode", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "submit_plan requires at least one edit in the edits array",
	}}

	if planThenExecuteUnscopedEmptySubmitPlanRejected(brief, results) {
		ginkgo.Fail("empty submit_plan salvage must be driven by typed result code, not diagnostic prose", 1)
	}
})

var _ = ginkgo.It("PlanExecuteSubmitReadyFailureFeedbackDoesNotSuggestGenericProbe", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go", "a_test.go"},
	}
	req := workspace.ToolRequest{Tool: "read_file", Path: "a.go", StartLine: 1, EndLine: 20}
	rejected := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Plan-execute submit_plan required",
		Error:   "read_file is not available because the plan-execute planner is source-ready and exposes only submit_plan",
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 20, 20, ""),
		readFileSourceWindowResult("a_test.go", 1, 20, 20, ""),
		rejected,
	}

	state, _ := buildRunnerNextActionStateWithAllowedToolsAndContract("main", 3, "tool_rejected", req, rejected, results, false, brief, nil)
	joinedState := strings.Join(state.NextValidActions, "\n")

	if !strings.Contains(joinedState, "source_satisfied_paths") {
		ginkgo.Fail(fmt.Sprintf("submit-ready next_action_state should point at submit_plan, got %#v", state.NextValidActions), 1)
	}

	if strings.Contains(joinedState, "one exact tool-shaped follow-up") {
		ginkgo.Fail(fmt.Sprintf("submit-ready next_action_state must not suggest another probe: %#v", state.NextValidActions), 1)
	}

	if strings.Contains(joinedState, "answer from the current evidence") {
		ginkgo.Fail(fmt.Sprintf("submit-ready next_action_state must not include generic answer/probe fallback: %#v", state.NextValidActions), 1)
	}

	packet := buildRunnerFailureFeedbackPacketWithAllowedToolsAndTransport("main", 3, "tool_rejected", req, rejected, results, false, brief, nil, PlanExecutePlanningTransport{TextualToolParser: true}, deriveImplementationProgressState(brief, results), planThenExecutePathAuthority{})
	joinedFeedback := strings.Join(packet.ValidNext, "\n")

	if !strings.Contains(joinedFeedback, "source_satisfied_paths") {
		ginkgo.Fail(fmt.Sprintf("submit-ready failure feedback should point at submit_plan, got %#v", packet.ValidNext), 1)
	}

	if strings.Contains(joinedFeedback, "one exact tool-shaped follow-up") {
		ginkgo.Fail(fmt.Sprintf("submit-ready failure feedback must not suggest another probe: %#v", packet.ValidNext), 1)
	}

	if strings.Contains(joinedFeedback, "answer from the current evidence") {
		ginkgo.Fail(fmt.Sprintf("submit-ready failure feedback must not include generic answer/probe fallback: %#v", packet.ValidNext), 1)
	}

	if strings.Contains(packet.ResultError, "Use an available tool") {
		ginkgo.Fail(fmt.Sprintf("submit-ready rejection should not use generic allowed-tool wording: %q", packet.ResultError), 1)
	}
})

var _ = ginkgo.It("PlanExecuteReadyToSubmitRaisesBudgetAfterSourceReadCap", func() {
	thinkingOff := false
	opts := planThenExecuteReadyToSubmitChatOptionsForBackend(llama.ChatOptions{
		EnableThinking:   &thinkingOff,
		PreserveThinking: false,
		MaxTokens:        768,
		Tools:            []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice:       "required",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"},
	}, []workspace.ToolResult{
		codeNavigateSourceWindowResult("cmd/idiom/product_workflow.go", 1515, 1640, 0, "Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640"),
		readFileSourceWindowResult("cmd/idiom/product_workflow_test.go", 5257, 5270, 0, ""),
	}, true, planThenExecutePathAuthority{})

	if opts.MaxTokens != 4096 {
		ginkgo.Fail(fmt.Sprintf("ready-to-submit should raise inherited source-read budget to planner budget, got %d", opts.MaxTokens), 1)
	}

	if opts.EnableThinking == nil || *opts.EnableThinking {
		ginkgo.Fail(fmt.Sprintf("ready-to-submit should keep thinking disabled after source-read cap, got %#v", opts.EnableThinking), 1)
	}
})

var _ = ginkgo.It("PlanExecuteReadyToSubmitUsesLargeBudgetForFourExistingFiles", func() {
	thinkingOff := false
	opts := planThenExecuteReadyToSubmitChatOptionsForBackend(llama.ChatOptions{
		EnableThinking:   &thinkingOff,
		PreserveThinking: false,
		MaxTokens:        4096,
		Tools:            []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice:       "required",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go", "internal/workflows/productworkflow/coverage_gate.go", "internal/workflows/productworkflow/coverage_gate_test.go"},
	}, []workspace.ToolResult{
		codeNavigateSourceWindowResult("cmd/idiom/product_workflow.go", 1515, 1640, 0, "Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640"),
		codeNavigateSourceWindowResult("cmd/idiom/product_workflow_test.go", 5257, 5270, 0, "Read TestProductTDDCoverageGate in cmd/idiom/product_workflow_test.go lines 5257-5270"),
		codeNavigateSourceWindowResult("internal/workflows/productworkflow/coverage_gate.go", 1, 120, 0, "Read EvaluateCoverageGate in internal/workflows/productworkflow/coverage_gate.go lines 1-120"),
		codeNavigateSourceWindowResult("internal/workflows/productworkflow/coverage_gate_test.go", 1, 80, 0, "Read TestCoverageGate in internal/workflows/productworkflow/coverage_gate_test.go lines 1-80"),
	}, true, planThenExecutePathAuthority{})

	if opts.MaxTokens != 16384 {
		ginkgo.Fail(fmt.Sprintf("ready-to-submit should use large budget for four-file textual submit_plan output, got %d", opts.MaxTokens), 1)
	}

	if opts.EnableThinking == nil || *opts.EnableThinking {
		ginkgo.Fail(fmt.Sprintf("ready-to-submit should keep thinking disabled for submit_plan-only multi-file synthesis, got %#v", opts.EnableThinking), 1)
	}
})

var _ = ginkgo.It("PlanExecuteReadyToSubmitKeepsBoundedBudgetForThreeExistingFiles", func() {
	thinkingOff := false
	opts := planThenExecuteReadyToSubmitChatOptionsForBackend(llama.ChatOptions{
		EnableThinking:   &thinkingOff,
		PreserveThinking: false,
		MaxTokens:        4096,
		Tools:            []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice:       "required",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go", "internal/workflows/productworkflow/coverage_gate.go"},
	}, []workspace.ToolResult{
		codeNavigateSourceWindowResult("cmd/idiom/product_workflow.go", 1515, 1640, 0, "Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640"),
		codeNavigateSourceWindowResult("cmd/idiom/product_workflow_test.go", 5257, 5270, 0, "Read TestProductTDDCoverageGate in cmd/idiom/product_workflow_test.go lines 5257-5270"),
		codeNavigateSourceWindowResult("internal/workflows/productworkflow/coverage_gate.go", 1, 120, 0, "Read EvaluateCoverageGate in internal/workflows/productworkflow/coverage_gate.go lines 1-120"),
	}, true, planThenExecutePathAuthority{})

	if opts.MaxTokens != 8192 {
		ginkgo.Fail(fmt.Sprintf("ready-to-submit should keep bounded budget for three existing files, got %d", opts.MaxTokens), 1)
	}
})

var _ = ginkgo.It("PlanExecuteRejectedPlanCorrectionKeepsLargeBudgetForWholeFile", func() {
	thinkingOff := false
	opts := planThenExecutePostReadinessRecoveryChatOptionsForBackend(llama.ChatOptions{
		EnableThinking:   &thinkingOff,
		PreserveThinking: false,
		MaxTokens:        4096,
		Tools:            []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice:       "required",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Objective:         "Correct the rejected submit_plan with a whole-file fixture replacement.",
		RequiredEditPaths: []string{"internal/workflows/productworkflow/coverage_gate_report.go", "internal/workflows/productworkflow/coverage_gate_report_test.go", "internal/workflows/productworkflow/coverage_gate_fixture.json"},
	}, []workspace.ToolResult{
		readFileSourceWindowResult("internal/workflows/productworkflow/coverage_gate_report.go", 1, 40, 40, ""),
		readFileSourceWindowResult("internal/workflows/productworkflow/coverage_gate_report_test.go", 1, 40, 40, ""),
		readFileSourceWindowResult("internal/workflows/productworkflow/coverage_gate_fixture.json", 1, 80, 80, ""),
		{Tool: "submit_plan", Summary: "submit_plan rejected: invalid edit shape", Error: "replace_all requires a full file body for internal/workflows/productworkflow/coverage_gate_fixture.json"},
	}, true, planThenExecutePathAuthority{})

	if opts.MaxTokens != 16384 {
		ginkgo.Fail(fmt.Sprintf("whole-file correction should preserve full submit_plan budget, got %d", opts.MaxTokens), 1)
	}
})

var _ = ginkgo.It("PlanExecuteTextualFallbackUsesLargeBudgetForFourExistingFiles", func() {
	thinkingOn := true
	opts := planThenExecuteTextualSubmitPlanFallbackChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        16384,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "required",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go", "internal/workflows/productworkflow/coverage_gate.go", "internal/workflows/productworkflow/coverage_gate_test.go"},
	}, []workspace.ToolResult{
		codeNavigateSourceWindowResult("cmd/idiom/product_workflow.go", 1515, 1640, 0, "Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640"),
		codeNavigateSourceWindowResult("cmd/idiom/product_workflow_test.go", 5257, 5270, 0, "Read TestProductTDDCoverageGate in cmd/idiom/product_workflow_test.go lines 5257-5270"),
		codeNavigateSourceWindowResult("internal/workflows/productworkflow/coverage_gate.go", 1, 120, 0, "Read EvaluateCoverageGate in internal/workflows/productworkflow/coverage_gate.go lines 1-120"),
		codeNavigateSourceWindowResult("internal/workflows/productworkflow/coverage_gate_test.go", 1, 80, 0, "Read TestCoverageGate in internal/workflows/productworkflow/coverage_gate_test.go lines 1-80"),
	})

	if opts.MaxTokens != 16384 {
		ginkgo.Fail(fmt.Sprintf("textual fallback should use large four-file submit_plan budget, got %d", opts.MaxTokens), 1)
	}

	if opts.Tools != nil || opts.ToolChoice != "" || opts.ForceNativeTools {
		ginkgo.Fail(fmt.Sprintf("textual fallback should disable native tool transport, got force=%v choice=%q tools=%#v", opts.ForceNativeTools, opts.ToolChoice, opts.Tools), 1)
	}

	if opts.EnableThinking == nil || *opts.EnableThinking || opts.PreserveThinking {
		ginkgo.Fail(fmt.Sprintf("textual fallback should force visible no-thinking output, got enable=%#v preserve=%v", opts.EnableThinking, opts.PreserveThinking), 1)
	}

	if !opts.ResponseFormatJSON {
		ginkgo.Fail("textual fallback should request JSON response format", 1)
	}
})

var _ = ginkgo.It("PlanExecuteRequiredPathSymbolReadRecoveryCapsPlannerBudget", func() {
	thinkingOn := true
	opts := completionGateRecoveryChatOptionsForBackend(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          16384,
		Tools:              []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"},
	}, []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
		Paths:   []string{"cmd/idiom/product_workflow.go"},
		Facts:   sourceWindowFactsForTest("cmd/idiom/product_workflow.go", 1515, 1640, 0),
	}}, true, planThenExecutePathAuthority{}, deriveImplementationProgressState(

		workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"},
		}, []workspace.ToolResult{{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
			Paths:   []string{"cmd/idiom/product_workflow.go"},
			Facts:   sourceWindowFactsForTest("cmd/idiom/product_workflow.go", 1515, 1640, 0),
		}}))

	if opts.PreserveThinking {
		ginkgo.Fail("symbol-read required-path recovery should stop preserving prior hidden reasoning", 1)
	}

	if opts.MaxTokens != 768 {
		ginkgo.Fail(fmt.Sprintf("symbol-read required-path recovery MaxTokens = %d, want 768", opts.MaxTokens), 1)
	}
})

var _ = ginkgo.It("PlanExecuteCandidateReadyKeepsUnreadCandidateReadTools", func() {
	thinkingOn := true
	opts := planThenExecuteReadyToSubmitChatOptionsForBackend(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          8192,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "code_navigate"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedEditPaths:  []string{"pkg/feature.go", "pkg/feature_test.go"},
	}, []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 pkg/feature.go lines 1-4 (total lines: 4)",
		Output:  "1:package pkg\n2:\n3:func Hardened() bool { return true }\n",
		Paths:   []string{"pkg/feature.go"},
		Facts:   sourceWindowFactsForTest("pkg/feature.go", 1, 4, 4),
	}}, true, planThenExecutePathAuthority{})

	if !chatOptionsExposeTool(opts, "read_file") || !chatOptionsExposeTool(opts, "code_navigate") || !chatOptionsExposeTool(opts, "submit_plan") {
		ginkgo.Fail(fmt.Sprintf("candidate readiness must keep read tools visible while an allowed candidate remains unread, got %#v", opts.Tools), 1)
	}

	if opts.ToolChoice == "required" {
		ginkgo.Fail("candidate readiness must not require submit_plan before unread candidate source-satisfied evidence can be read", 1)
	}

	if opts.EnableThinking == nil || *opts.EnableThinking {
		ginkgo.Fail(fmt.Sprintf("candidate readiness should still force visible actions, got %#v", opts.EnableThinking), 1)
	}
})

var _ = ginkgo.It("PlanExecuteRejectedOldTextRequiresAnchorReadyRefresh", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow_test.go"},
	}
	rejected := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "invalid edit anchor(s): `cmd/idiom/product_workflow_test.go: old_text not found in current source`.",
		Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
			Code: plancontract.CoverageMismatchCode, Kind: toolfacts.SubmitPlanDiagnosticInvalidAnchor, CanonicalID: "cmd/idiom/product_workflow_test.go",
		}),
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("cmd/idiom/product_workflow_test.go", 1, 120, 6834, ""),
		rejected,
		readFileSourceWindowResult("cmd/idiom/product_workflow_test.go", 1, 100, 6834, ""),
	}

	if !planThenExecuteRejectedPlanNeedsSourceRefresh(brief, results) {
		ginkgo.Fail("generic first-window refresh must not close a stale old_text anchor rejection", 1)
	}

	results = append(results, readFileSourceWindowResult("cmd/idiom/product_workflow_test.go", 5300, 5360, 6834, ""))
	if !planThenExecuteRejectedPlanNeedsSourceRefresh(brief, results) {
		ginkgo.Fail("raw bounded source refresh of a very large Go file should not close stale-anchor refresh requirement", 1)
	}

	results = append(results, codeNavigateSourceWindowResult("cmd/idiom/product_workflow_test.go", 5300, 5360, 0, "route_summary: Read TestProductLintFailsOnInvalidTDDCoverageTier in cmd/idiom/product_workflow_test.go lines 5300-5360\n5300:func TestProductLintFailsOnInvalidTDDCoverageTier(t *testing.T) {}"))
	if planThenExecuteRejectedPlanNeedsSourceRefresh(brief, results) {
		ginkgo.Fail("symbol/body source refresh should close stale-anchor refresh requirement", 1)
	}
})

var _ = ginkgo.It("PlanExecuteRejectedOldTextKeepsCppRawRangeInAnchorRefresh", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"native/src/simulation/production/production_execution.cpp"},
	}
	rejected := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "invalid edit anchor(s): `native/src/simulation/production/production_execution.cpp: old_text not found in current source`.",
		Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
			Code: plancontract.CoverageMismatchCode, Kind: toolfacts.SubmitPlanDiagnosticInvalidAnchor, CanonicalID: "native/src/simulation/production/production_execution.cpp",
		}),
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("native/src/simulation/production/production_execution.cpp", 1, 180, 1593, ""),
		rejected,
		readFileSourceWindowResult("native/src/simulation/production/production_execution.cpp", 181, 360, 1593, "181:void execute_production() {}\n360:}\n"),
	}

	if planThenExecuteRejectedPlanNeedsSourceRefresh(brief, results) {
		ginkgo.Fail("bounded non-header C++ source windows should close a stale old_text anchor rejection", 1)
	}

	state := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})
	if state.Mode == PlanExecutePlanningModeAnchorRefresh {
		ginkgo.Fail(fmt.Sprintf("Mode after bounded C++ range = %q, want submit correction/ready state; state=%+v", state.Mode, state), 1)
	}
})

var _ = ginkgo.It("PlanExecuteMissingGroundingRefreshCompletesAfterBoundedReads", func() {
	t := ginkgo.GinkgoT()
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{
			"native/src/simulation/production/production_execution.cpp",
			"native/tests/test_quality_inventory_bindings.cpp",
		},
	}
	rejected := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: edit paths lack source grounding",
		Error:   "submit_plan edits for plan-execute planner atoms must be grounded before executor handoff; missing source-body evidence for `native/src/simulation/production/production_execution.cpp`; missing source-body evidence for `native/tests/test_quality_inventory_bindings.cpp`.",
		Facts: toolfacts.NewSubmitPlanDiagnosticFacts(
			toolfacts.SubmitPlanDiagnostic{Code: planThenExecuteResultSubmitPlanMissingSourceBody, Kind: toolfacts.SubmitPlanDiagnosticMissingSourceGrounding, CanonicalID: "native/src/simulation/production/production_execution.cpp"},
			toolfacts.SubmitPlanDiagnostic{Code: planThenExecuteResultSubmitPlanMissingSourceBody, Kind: toolfacts.SubmitPlanDiagnosticMissingSourceGrounding, CanonicalID: "native/tests/test_quality_inventory_bindings.cpp"},
		),
	}
	results := []workspace.ToolResult{
		rejected,
	}
	paths := planThenExecuteRejectedPlanRefreshPaths(brief, results, rejected, 0)
	assertEqualSet(t, paths, []string{
		"native/src/simulation/production/production_execution.cpp",
		"native/tests/test_quality_inventory_bindings.cpp",
	})

	results = append(results,
		readFileSourceWindowResult("native/src/simulation/production/production_execution.cpp", 181, 260, 1593, ""),
		readFileSourceWindowResult("native/tests/test_quality_inventory_bindings.cpp", 1, 160, 260, ""),
	)
	if paths := planThenExecuteRejectedPlanRefreshPaths(brief, results, rejected, 0); len(paths) != 0 {
		ginkgo.Fail(fmt.Sprintf("bounded reads should close missing-grounding refresh paths, got %#v", paths), 1)
	}

	brief.AllowedTools = []string{workspace.ToolNameReadFile, workspace.ToolNameSubmitPlan}
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: brief, Results: results, BaseAllowedTools: brief.AllowedTools,
		InspectionAuthority: permissionAuthorizedInspectionForTest(brief.AllowedTools...),
	})
	if !slices.Contains(contract.AllowedTools, workspace.ToolNameReadFile) {
		ginkgo.Fail(fmt.Sprintf("post-refresh permission-authorized reads must remain callable: %#v", contract))
	}
})

var _ = ginkgo.It("PlanExecuteRejectedMissingSourceGroundingRequiresRefreshForRejectedEditPath", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}
	rejected := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: edit paths lack source grounding",
		Error:   "submit_plan edits for plan-execute planner atoms must be grounded before executor handoff; missing source-body evidence for `internal/workflows/productworkflow/tdd.go`. Read each existing edit path with `read_file` or `code_navigate` `read_symbol`/`extract_body`, then submit a corrected plan.",
		Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
			Code: planThenExecuteResultSubmitPlanMissingSourceBody, Kind: toolfacts.SubmitPlanDiagnosticMissingSourceGrounding, CanonicalID: "internal/workflows/productworkflow/tdd.go",
		}),
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("cmd/idiom/product_workflow.go", 1, 120, 2894, ""),
		rejected,
	}

	if !planThenExecuteRejectedPlanNeedsSourceRefresh(brief, results) {
		ginkgo.Fail("missing source-body grounding rejection should enable a bounded refresh for the rejected edit path", 1)
	}

	paths := planThenExecuteRejectedPlanRefreshPaths(brief, results, rejected, 1)
	if len(paths) != 1 || paths[0] != "internal/workflows/productworkflow/tdd.go" {
		ginkgo.Fail(fmt.Sprintf("refresh paths = %#v, want rejected edit path", paths), 1)
	}

	results = append(results, readFileSourceWindowResult("internal/workflows/productworkflow/tdd.go", 1, 50, 120, ""))
	if planThenExecuteRejectedPlanNeedsSourceRefresh(brief, results) {
		ginkgo.Fail("successful source read after the rejected plan should close missing-grounding refresh", 1)
	}
})

var _ = ginkgo.It("PlanExecuteSubmitPlanDiagnosticsKeepExtensionlessPathsAndDropProtocolTokens", func() {
	coverageRejected := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "missing required edit path(s): `.gitignore`, `go.work`; invalid edit anchor(s): `Brewfile: old_text not found in current source`, `Cargo.lock: old_text not found in current source`. Use `Decision: source-satisfied` or `<<QUESTION>>:` only as generic exceptions.",
		Facts: toolfacts.NewSubmitPlanDiagnosticFacts(
			toolfacts.SubmitPlanDiagnostic{Code: plancontract.CoverageMismatchCode, Kind: toolfacts.SubmitPlanDiagnosticMissingRequiredPath, CanonicalID: ".gitignore"},
			toolfacts.SubmitPlanDiagnostic{Code: plancontract.CoverageMismatchCode, Kind: toolfacts.SubmitPlanDiagnosticMissingRequiredPath, CanonicalID: "go.work"},
			toolfacts.SubmitPlanDiagnostic{Code: plancontract.CoverageMismatchCode, Kind: toolfacts.SubmitPlanDiagnosticInvalidAnchor, CanonicalID: "Brewfile"},
			toolfacts.SubmitPlanDiagnostic{Code: plancontract.CoverageMismatchCode, Kind: toolfacts.SubmitPlanDiagnosticInvalidAnchor, CanonicalID: "Cargo.lock"},
		),
	}
	if got := sortedRunnerPathKeys(submitPlanMissingRequiredPaths(coverageRejected)); !slices.Equal(got, []string{".gitignore", "go.work"}) {
		ginkgo.Fail(fmt.Sprintf("missing required diagnostics should retain extensionless path, got %#v", got), 1)
	}

	if got := sortedRunnerPathKeys(submitPlanInvalidAnchorPaths(coverageRejected)); !slices.Equal(got, []string{"Brewfile", "Cargo.lock"}) {
		ginkgo.Fail(fmt.Sprintf("invalid anchor diagnostics should retain extensionless path, got %#v", got), 1)
	}

	existingSourceRewriteRejected := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[0]: whole-file replace_all edit would rewrite existing source file /workspace/Colony/native/src/simulation/world/collapse_event.h; use a bounded anchored edit with old_text/new_text or start_line/end_line/content, or answer <<QUESTION>> with the exact missing body/anchor blocker",
		Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
			Code: workspace.SubmitPlanWholeFileRewriteCode, Kind: toolfacts.SubmitPlanDiagnosticWholeFileRewrite, CanonicalID: "native/src/simulation/world/collapse_event.h",
		}),
	}
	if got := sortedRunnerPathKeys(submitPlanInvalidAnchorPaths(existingSourceRewriteRejected)); !slices.Equal(got, []string{"native/src/simulation/world/collapse_event.h"}) {
		ginkgo.Fail(fmt.Sprintf("whole-file existing source rewrite should become an anchor-refresh path, got %#v", got), 1)
	}

	lineRangeWholeBodyRejected := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[0]: line-range replacement for /workspace/Colony/native/src/simulation/world/collapse_event.h lines 1-63 would rewrite the entire existing source file; use bounded old_text/new_text or smaller start_line/end_line anchors",
		Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
			Code: workspace.SubmitPlanWholeFileRewriteCode, Kind: toolfacts.SubmitPlanDiagnosticWholeFileRewrite, CanonicalID: "native/src/simulation/world/collapse_event.h", StartLine: 1, EndLine: 63,
		}),
	}
	if got := sortedRunnerPathKeys(submitPlanInvalidAnchorPaths(lineRangeWholeBodyRejected)); !slices.Equal(got, []string{"native/src/simulation/world/collapse_event.h"}) {
		ginkgo.Fail(fmt.Sprintf("whole-file line-range existing source rewrite should become an anchor-refresh path, got %#v", got), 1)
	}

	lineRangeCollapseRejected := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[2]: line-range replacement for /workspace/Colony/native/src/core/colony_sim_tick.cpp lines 1-80 would collapse 73 non-empty source lines to 24; use smaller bounded anchors or answer <<QUESTION>> with the exact missing body/anchor blocker",
		Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
			Code: workspace.SubmitPlanInvalidAnchorCode, Kind: toolfacts.SubmitPlanDiagnosticInvalidAnchor, CanonicalID: "native/src/core/colony_sim_tick.cpp", StartLine: 1, EndLine: 80,
		}),
	}
	if got := sortedRunnerPathKeys(submitPlanInvalidAnchorPaths(lineRangeCollapseRejected)); !slices.Equal(got, []string{"native/src/core/colony_sim_tick.cpp"}) {
		ginkgo.Fail(fmt.Sprintf("line-range source collapse should become an anchor-refresh path, got %#v", got), 1)
	}

	headerGuardRejected := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[0]: line-range replacement for /workspace/Colony/native/src/simulation/world/collapse_event.h lines 1-1 starts a header guard before selecting the existing file prologue/include guard; use a bounded anchor that includes the current guard/include line",
		Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
			Code: workspace.SubmitPlanInvalidAnchorCode, Kind: toolfacts.SubmitPlanDiagnosticInvalidAnchor, CanonicalID: "native/src/simulation/world/collapse_event.h", StartLine: 1, EndLine: 1,
		}),
	}
	if got := sortedRunnerPathKeys(submitPlanInvalidAnchorPaths(headerGuardRejected)); !slices.Equal(got, []string{"native/src/simulation/world/collapse_event.h"}) {
		ginkgo.Fail(fmt.Sprintf("header-guard line-range rejection should become an anchor-refresh path, got %#v", got), 1)
	}

	groundingRejected := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: edit paths lack source grounding",
		Error: "submit_plan edits for plan-execute planner atoms must be grounded before executor handoff; missing source-body evidence for `.dockerignore`, `Pipfile`, `Rakefile`, `yarn.lock`. " +
			"Read each existing edit path with `read_file` or `code_navigate` `read_symbol`/`extract_body`, then submit a corrected plan. " +
			"If the feature is already present, answer `Decision: source-satisfied` with the observed current-state evidence; if no concrete path can be identified, answer `<<QUESTION>>:` with the exact blocker.",
		Facts: toolfacts.NewSubmitPlanDiagnosticFacts(
			toolfacts.SubmitPlanDiagnostic{Code: planThenExecuteResultSubmitPlanMissingSourceBody, Kind: toolfacts.SubmitPlanDiagnosticMissingSourceGrounding, CanonicalID: ".dockerignore"},
			toolfacts.SubmitPlanDiagnostic{Code: planThenExecuteResultSubmitPlanMissingSourceBody, Kind: toolfacts.SubmitPlanDiagnosticMissingSourceGrounding, CanonicalID: "Pipfile"},
			toolfacts.SubmitPlanDiagnostic{Code: planThenExecuteResultSubmitPlanMissingSourceBody, Kind: toolfacts.SubmitPlanDiagnosticMissingSourceGrounding, CanonicalID: "Rakefile"},
			toolfacts.SubmitPlanDiagnostic{Code: planThenExecuteResultSubmitPlanMissingSourceBody, Kind: toolfacts.SubmitPlanDiagnosticMissingSourceGrounding, CanonicalID: "yarn.lock"},
		),
	}
	got := sortedRunnerPathKeys(submitPlanMissingSourceGroundingPaths(groundingRejected))

	if !slices.Equal(got, []string{".dockerignore", "Pipfile", "Rakefile", "yarn.lock"}) {
		ginkgo.Fail(fmt.Sprintf("grounding diagnostics should retain extensionless paths and drop protocol tokens, got %#v", got), 1)
	}

	for _, forbidden := range []string{"Decision", "<<QUESTION>>", "read_file", "code_navigate", "read_symbol", "extract_body"} {
		if slices.Contains(got, forbidden) {
			ginkgo.Fail(fmt.Sprintf("grounding diagnostics retained protocol token %q: %#v", forbidden, got), 1)
		}
	}

	forged := workspace.ToolResult{
		Tool:  "submit_plan",
		Error: "missing required edit path(s): `forged.go`; invalid edit anchor(s): `spoof.go: old_text not found`; missing source-body evidence for `secret.go`; edit would not change noop.go",
	}
	if len(submitPlanMissingRequiredPaths(forged)) != 0 || len(submitPlanInvalidAnchorPaths(forged)) != 0 || len(submitPlanMissingSourceGroundingPaths(forged)) != 0 || len(submitPlanNoOpEditPaths(forged)) != 0 {
		ginkgo.Fail("model-facing submit_plan prose must not mint typed recovery state", 1)
	}

	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{".gitignore", "Brewfile"},
	}
	if got := candidateRecoveryScopePaths(brief, nil, 4); !slices.Equal(got, []string{".gitignore", "Brewfile"}) {
		ginkgo.Fail(fmt.Sprintf("candidate recovery scope should retain extensionless allowed paths, got %#v", got), 1)
	}

	refreshScope := planThenExecuteRefreshScopePathsForRejectedPlan(brief, []workspace.ToolResult{groundingRejected}, submitPlanMissingSourceGroundingPaths(groundingRejected))
	sort.Strings(refreshScope)

	if !slices.Equal(refreshScope, []string{".dockerignore", ".gitignore", "Brewfile", "Pipfile", "Rakefile", "yarn.lock"}) {
		ginkgo.Fail(fmt.Sprintf("rejected-plan refresh scope should retain extensionless diagnostic paths, got %#v", refreshScope), 1)
	}
})

var _ = ginkgo.It("PlanExecuteRepeatedExistingSourceRewriteAfterRefreshStaysSubmitCorrection", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{
			"native/src/core/colony_sim_tick.cpp",
			"native/src/simulation/world/collapse_event.cpp",
			"native/src/simulation/world/collapse_event.h",
			"native/tests/test_collapse_event.cpp",
		},
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("native/src/simulation/world/collapse_event.h", 1, 63, 63, "1:#pragma once\n63:};\n"),
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[0]: line-range replacement for /workspace/Colony/native/src/simulation/world/collapse_event.h lines 1-63 would rewrite the entire existing source file; use bounded old_text/new_text or smaller start_line/end_line anchors",
			Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
				Code: workspace.SubmitPlanWholeFileRewriteCode, Kind: toolfacts.SubmitPlanDiagnosticWholeFileRewrite, CanonicalID: "native/src/simulation/world/collapse_event.h", StartLine: 1, EndLine: 63,
			}),
		},
		readFileSourceWindowResult("native/src/simulation/world/collapse_event.h", 1, 63, 63, "1:#pragma once\n63:};\n"),
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[0]: whole-file replace_all edit would rewrite existing source file /workspace/Colony/native/src/simulation/world/collapse_event.h; use a bounded anchored edit with old_text/new_text or start_line/end_line/content",
			Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
				Code: workspace.SubmitPlanWholeFileRewriteCode, Kind: toolfacts.SubmitPlanDiagnosticWholeFileRewrite, CanonicalID: "native/src/simulation/world/collapse_event.h",
			}),
		},
	}

	state := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})
	if state.Mode != PlanExecutePlanningModeSubmitCorrection {
		ginkgo.Fail(fmt.Sprintf("mode = %q, want submit_correction after refreshed repeated invalid rewrite; state=%+v", state.Mode, state), 1)
	}
	if len(state.RefreshPaths) != 0 {
		ginkgo.Fail(fmt.Sprintf("repeated invalid rewrite after refresh should not reopen anchor refresh, got %#v", state.RefreshPaths), 1)
	}

})

var _ = ginkgo.It("PlanExecutePartialPriorRefreshDoesNotCloseLaterInvalidAnchor", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"native/src/core/colony_sim_tick.cpp"},
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("native/src/core/colony_sim_tick.cpp", 1, 180, 2616, "1:#include <x>\n180:void a();\n"),
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `native/src/core/colony_sim_tick.cpp: old_text not found in current source`.",
			Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
				Code: plancontract.CoverageMismatchCode, Kind: toolfacts.SubmitPlanDiagnosticInvalidAnchor, CanonicalID: "native/src/core/colony_sim_tick.cpp",
			}),
		},
		readFileSourceWindowResult("native/src/core/colony_sim_tick.cpp", 880, 1050, 2616, "880:void tick() {}\n1050:}\n"),
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `native/src/core/colony_sim_tick.cpp: old_text not found in current source`.",
			Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
				Code: plancontract.CoverageMismatchCode, Kind: toolfacts.SubmitPlanDiagnosticInvalidAnchor, CanonicalID: "native/src/core/colony_sim_tick.cpp",
			}),
		},
	}

	state := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})
	if state.Mode != PlanExecutePlanningModeAnchorRefresh {
		ginkgo.Fail(fmt.Sprintf("mode = %q, want anchor_refresh after later invalid anchor with only partial prior refresh; state=%+v", state.Mode, state), 1)
	}
	if !slices.Equal(state.RefreshPaths, []string{"native/src/core/colony_sim_tick.cpp"}) {
		ginkgo.Fail(fmt.Sprintf("refresh paths = %#v, want core source path", state.RefreshPaths), 1)
	}
})

var _ = ginkgo.It("PlanExecuteRepeatedBoundedCppReadAfterRejectionCountsAsFresh", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"native/src/core/colony_sim_tick.cpp"},
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("native/src/core/colony_sim_tick.cpp", 1, 180, 2616, "1:#include <x>\n180:void a();\n"),
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `native/src/core/colony_sim_tick.cpp: old_text not found in current source`.",
			Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
				Code: plancontract.CoverageMismatchCode, Kind: toolfacts.SubmitPlanDiagnosticInvalidAnchor, CanonicalID: "native/src/core/colony_sim_tick.cpp",
			}),
		},
		readFileSourceWindowResult("native/src/core/colony_sim_tick.cpp", 1, 180, 2616, "1:#include <x>\n180:void a();\n"),
	}

	state := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})
	if state.Mode == PlanExecutePlanningModeAnchorRefresh {
		ginkgo.Fail(fmt.Sprintf("post-rejection bounded C++ re-read should count as fresh source evidence; state=%+v", state), 1)
	}
	if len(state.RefreshPaths) != 0 {
		ginkgo.Fail(fmt.Sprintf("refresh paths = %#v, want none after post-rejection re-read", state.RefreshPaths), 1)
	}
})

var _ = ginkgo.It("PlanExecutePathlessNoopProofDoesNotSuppressUnresolvedStreakRefresh", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"native/src/core/colony_sim_tick.cpp"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 native/src/core/colony_sim_tick.cpp lines 1-120 (total lines: 2616)", Output: "1:#include <x>\n120:void a();\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[2]: line-range replacement for /workspace/Colony/native/src/core/colony_sim_tick.cpp lines 200-260 would collapse 61 non-empty source lines to 20; use smaller bounded anchors or answer <<QUESTION>> with the exact missing body/anchor blocker",
			Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
				Code: workspace.SubmitPlanInvalidAnchorCode, Kind: toolfacts.SubmitPlanDiagnosticInvalidAnchor, CanonicalID: "native/src/core/colony_sim_tick.cpp", StartLine: 200, EndLine: 260,
			}),
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "plan_artifact.schema_errors edits[2]:invalid_edit_shape:failed_precondition[3002]: plan submission rejected: unsupported proof claim: no-op assertion on line 7; submit a concrete state assertion or report a blocker",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "plan_artifact.schema_errors edits[2]:invalid_edit_shape:failed_precondition[3002]: plan submission rejected: unsupported proof claim: no-op assertion on line 6; submit a concrete state assertion or report a blocker",
		},
	}

	state := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})
	if state.Mode != PlanExecutePlanningModeAnchorRefresh {
		ginkgo.Fail(fmt.Sprintf("mode = %q, want anchor_refresh while earlier pathful collapse rejection is unresolved; state=%+v", state.Mode, state), 1)
	}
	if !slices.Equal(state.RefreshPaths, []string{"native/src/core/colony_sim_tick.cpp"}) {
		ginkgo.Fail(fmt.Sprintf("refresh paths = %#v, want unresolved core source path", state.RefreshPaths), 1)
	}
})

var _ = ginkgo.It("PlanExecuteCollapseRangeReadClosesRefresh", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"native/src/core/colony_sim_tick.cpp"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[2]: line-range replacement for /workspace/Colony/native/src/core/colony_sim_tick.cpp lines 1-80 would collapse 73 non-empty source lines to 24; use smaller bounded anchors or answer <<QUESTION>> with the exact missing body/anchor blocker",
			Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
				Code: workspace.SubmitPlanInvalidAnchorCode, Kind: toolfacts.SubmitPlanDiagnosticInvalidAnchor, CanonicalID: "native/src/core/colony_sim_tick.cpp", StartLine: 1, EndLine: 80,
			}),
		},
		readFileSourceWindowResult("native/src/core/colony_sim_tick.cpp", 1, 120, 2616, "1:#include <x>\n120:void after();\n"),
	}

	rejected := results[0]
	if paths := planThenExecuteRejectedPlanRefreshPaths(brief, results, rejected, 0); len(paths) != 0 {
		ginkgo.Fail(fmt.Sprintf("bounded read covering collapse diagnostic lines should close refresh, got %#v", paths), 1)
	}

	state := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})
	if slices.Contains(state.RefreshPaths, "native/src/core/colony_sim_tick.cpp") {
		ginkgo.Fail(fmt.Sprintf("state should not keep covered collapse refresh path: %+v", state), 1)
	}
})

var _ = ginkgo.It("PlanExecuteAnchorRefreshPhaseHintsDoNotForbidRefresh", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow_test.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "route_summary: Read TestProductLintFailsOnInvalidTDDCoverageTier in cmd/idiom/product_workflow_test.go lines 5300-5360\n5300:func TestProductLintFailsOnInvalidTDDCoverageTier(t *testing.T) {}"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `cmd/idiom/product_workflow_test.go: old_text not found in current source`.",
			Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
				Code: plancontract.CoverageMismatchCode, Kind: toolfacts.SubmitPlanDiagnosticInvalidAnchor, CanonicalID: "cmd/idiom/product_workflow_test.go",
			}),
		},
	}
	got := runnerBriefWithPhaseHints(brief, results, planThenExecutePathAuthority{}, implementationCompletionState{MissingRequiredPaths: brief.RequiredEditPaths})
	joinedNext := strings.Join(got.RequiredNext, "\n")
	joinedInvalid := strings.Join(got.InvalidActions, "\n")
	joinedPolicy := strings.Join(got.SearchPolicy, "\n")

	if !strings.Contains(joinedNext, "stale-anchor paths [cmd/idiom/product_workflow_test.go]") ||
		!strings.Contains(joinedNext, "choose any permission-authorized inspection") {
		ginkgo.Fail(fmt.Sprintf("phase hints should project every stale anchor without selecting a refresh request, got next=%#v", got.RequiredNext), 1)
	}

	if strings.Contains(joinedInvalid, "read/search/source-refresh") || strings.Contains(joinedPolicy, "no read/search/discovery action is valid") {
		ginkgo.Fail(fmt.Sprintf("anchor-refresh phase must not retain submit-ready no-read authority, invalid=%#v policy=%#v", got.InvalidActions, got.SearchPolicy), 1)
	}
})

var _ = ginkgo.It("PlanExecuteMLXTerminalSubmitPlanUsesTextualSurface", func() {
	runner := newRunnerRunFixture(&Runner{
		Client: llama.NewClient("http://127.0.0.1:1", time.Second).WithBackend(config.BackendMLX),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go"},
		},
	})
	results := []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)"}}

	if runner.supportsTerminalSubmitPlanNativeTools(results) {
		ginkgo.Fail("MLX should use textual submit_plan at terminal readiness instead of forced native tool_choice=required", 1)
	}
})

var _ = ginkgo.It("PlanExecuteCompletionGateRecoveryPreservesAnchorRefreshTools", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"native/src/core/colony_sim.h", "native/tests/test_quality_inventory_bindings.cpp"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 native/src/core/colony_sim.h lines 1-80 (total lines: 120)"},
		{Tool: "read_file", Summary: "Read f2 native/tests/test_quality_inventory_bindings.cpp lines 1-180 (total lines: 355)"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "submit_plan must cover every required_edit_paths entry; missing required edit path(s): `native/src/core/colony_sim.h`; invalid edit anchor(s): `native/tests/test_quality_inventory_bindings.cpp: old_text not found in current source`.",
			Facts: toolfacts.NewSubmitPlanDiagnosticFacts(
				toolfacts.SubmitPlanDiagnostic{Code: plancontract.CoverageMismatchCode, Kind: toolfacts.SubmitPlanDiagnosticMissingRequiredPath, CanonicalID: "native/src/core/colony_sim.h"},
				toolfacts.SubmitPlanDiagnostic{Code: plancontract.CoverageMismatchCode, Kind: toolfacts.SubmitPlanDiagnosticInvalidAnchor, CanonicalID: "native/tests/test_quality_inventory_bindings.cpp"},
			),
		},
		{
			Tool:    "completion_gate",
			Summary: "Plan-execute completion gate rejected final answer",
			Error:   "plan-execute planner phase requires a successful submit_plan tool result before final answer",
		},
	}
	opts := completionGateRecoveryChatOptionsForBackend(llama.ChatOptions{
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "code_navigate"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, brief, results, true, planThenExecutePathAuthority{}, deriveImplementationProgressState(

		brief, results))

	if opts.ForceNativeTools || opts.ResponseFormatJSON {
		ginkgo.Fail(fmt.Sprintf("anchor-refresh completion recovery should keep normal source-refresh transport, got force=%v json=%v", opts.ForceNativeTools, opts.ResponseFormatJSON), 1)
	}

	if !chatOptionsExposeTool(opts, "read_file") || !chatOptionsExposeTool(opts, "code_navigate") || !chatOptionsExposeTool(opts, "submit_plan") || opts.ToolChoice != "auto" {
		ginkgo.Fail(fmt.Sprintf("completion-gate recovery must preserve additive authorized reads plus submit/refresh, got choice=%q tools=%#v", opts.ToolChoice, opts.Tools), 1)
	}

	if opts.MaxTokens != planThenExecuteAnchorRefreshMaxTokens {
		ginkgo.Fail(fmt.Sprintf("completion-gate anchor refresh should clamp retry budget to %d tokens, got %d", planThenExecuteAnchorRefreshMaxTokens, opts.MaxTokens), 1)
	}
})

var _ = ginkgo.It("PlanExecuteCompletionGateRecoveryAnchorRefreshPrefixMatchesReadOnlyPhase", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"native/src/core/colony_sim.h", "native/tests/test_quality_inventory_bindings.cpp"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 native/src/core/colony_sim.h lines 1-80 (total lines: 120)"},
		{Tool: "read_file", Summary: "Read f2 native/tests/test_quality_inventory_bindings.cpp lines 1-180 (total lines: 355)"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "submit_plan must cover every required_edit_paths entry; missing required edit path(s): `native/src/core/colony_sim.h`; invalid edit anchor(s): `native/tests/test_quality_inventory_bindings.cpp: old_text not found in current source`.",
			Facts: toolfacts.NewSubmitPlanDiagnosticFacts(
				toolfacts.SubmitPlanDiagnostic{Code: plancontract.CoverageMismatchCode, Kind: toolfacts.SubmitPlanDiagnosticMissingRequiredPath, CanonicalID: "native/src/core/colony_sim.h"},
				toolfacts.SubmitPlanDiagnostic{Code: plancontract.CoverageMismatchCode, Kind: toolfacts.SubmitPlanDiagnosticInvalidAnchor, CanonicalID: "native/tests/test_quality_inventory_bindings.cpp"},
			),
		},
		{
			Tool:    "completion_gate",
			Summary: "Plan-execute completion gate rejected final answer",
			Error:   "plan-execute planner phase requires a successful submit_plan tool result before final answer",
		},
	}
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
	prompt, ok := runner.completionGateRecoveryPrompt(results)

	if !ok {
		ginkgo.Fail("expected anchor-refresh completion-gate recovery prompt", 1)
	}
	for _, want := range []string{
		"Recovery decision",
		"workflow_id=recovery",
		"state=inspecting",
		"terminal_state=recovery-open",
		"dispatchable=false",
		"reason=anchor_refresh",
		"callable_tools=read_file, code_navigate",
		"safe_alternatives=bounded_source_read",
	} {
		if !strings.Contains(prompt, want) {
			ginkgo.Fail(fmt.Sprintf("anchor-refresh recovery prompt missing %q:\n%s", want, prompt), 1)
		}
	}
	if strings.Contains(prompt, "terminal_outcomes=") {
		ginkgo.Fail(fmt.Sprintf("anchor refresh should not advertise terminal outcomes before source refresh returns:\n%s", prompt), 1)
	}
	if strings.Contains(prompt, "terminal_outcomes=bounded_source_read, <<QUESTION>>:") ||
		strings.Contains(prompt, "safe_alternatives=bounded_source_read, <<QUESTION>>:") {

		ginkgo.Fail(fmt.Sprintf("anchor-refresh prefix must not advertise blocker/source-satisfied closeout while refresh is open:\n%s", prompt), 1)
	}
})

var _ = ginkgo.It("PlanExecuteLaterInvalidAnchorRequiresFreshRefreshDespitePriorCppWindow", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{
			"native/src/core/colony_sim_tick.cpp",
			"native/src/simulation/world/collapse_event.h",
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 native/src/core/colony_sim_tick.cpp lines 200-260 (total lines: 2700)"},
		{Tool: "read_file", Summary: "Read f2 native/src/simulation/world/collapse_event.h lines 1-80 (total lines: 80)"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "submit_plan rejected: invalid edit anchor(s): `native/src/core/colony_sim_tick.cpp: old_text not found in current source`.",
			Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
				Code: plancontract.CoverageMismatchCode, Kind: toolfacts.SubmitPlanDiagnosticInvalidAnchor, CanonicalID: "native/src/core/colony_sim_tick.cpp",
			}),
		},
		{Tool: "read_file", Summary: "Read f3 native/src/core/colony_sim_tick.cpp lines 51-230 (total lines: 2700)"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[0] path \"native/src/core/colony_sim_tick.cpp\": source line-range edit for native/src/core/colony_sim_tick.cpp lines 1-1 expands the selected range but does not preserve any selected anchor line; for insertions, use old_text/new_text or replace the anchor line(s) with unchanged anchor line(s) plus new content",
			Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
				Code: workspace.SubmitPlanInvalidEditShapeCode, Kind: toolfacts.SubmitPlanDiagnosticInvalidEditShape, CanonicalID: "native/src/core/colony_sim_tick.cpp", StartLine: 1, EndLine: 1,
			}),
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "submit_plan rejected: invalid edit anchor(s): `native/src/core/colony_sim_tick.cpp: old_text not found in current source`, `native/src/simulation/world/collapse_event.h: old_text not found in current source`.",
			Facts: toolfacts.NewSubmitPlanDiagnosticFacts(
				toolfacts.SubmitPlanDiagnostic{Code: plancontract.CoverageMismatchCode, Kind: toolfacts.SubmitPlanDiagnosticInvalidAnchor, CanonicalID: "native/src/core/colony_sim_tick.cpp"},
				toolfacts.SubmitPlanDiagnostic{Code: plancontract.CoverageMismatchCode, Kind: toolfacts.SubmitPlanDiagnosticInvalidAnchor, CanonicalID: "native/src/simulation/world/collapse_event.h"},
			),
		},
	}

	state := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})
	if state.Mode != PlanExecutePlanningModeAnchorRefresh {
		ginkgo.Fail(fmt.Sprintf("later invalid anchor should require fresh anchor refresh, state=%#v", state), 1)
	}
	if !slices.Contains(state.RefreshPaths, "native/src/core/colony_sim_tick.cpp") {
		ginkgo.Fail(fmt.Sprintf("latest invalid core anchor should remain refreshable; refresh paths=%#v state=%#v", state.RefreshPaths, state), 1)
	}
	if !submitPlanRejectionHasInvalidEditShape(results[4]) {
		ginkgo.Fail("line-range anchor preservation rejection should be classified as invalid edit shape", 1)
	}
})

var _ = ginkgo.It("PlanExecuteRuntimeRecoverySequencePreservesNewFilePlannerBudget", func() {
	thinkingOn := true
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Objective:         "Create internal/contracts/writerroute/decision.go",
		RequiredEditPaths: []string{"internal/contracts/writerroute/decision.go"},
	}
	results := []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 internal/contracts/writerroute/decision.go", Error: "path is a NEW FILE and does not exist yet"}}
	opts := completionGateRecoveryChatOptionsForBackend(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          16384,
		Tools:              []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice:         "auto",
	}, brief, results, true, planThenExecutePathAuthority{}, deriveImplementationProgressState(

		brief, results))

	opts = planThenExecuteRequiredWriteOnlyRecoveryChatOptionsForBackend(opts, brief, results, true)

	if opts.MaxTokens != 16384 {
		ginkgo.Fail(fmt.Sprintf("real recovery option sequence should preserve new-file planner budget, got %d", opts.MaxTokens), 1)
	}
})

var _ = ginkgo.It("PlanExecuteCandidateWriteOnlyRecoveryDoesNotForceWithoutReadEvidence", func() {
	opts := planThenExecuteCandidateWriteOnlyRecoveryChatOptionsForBackend(llama.ChatOptions{
		MaxTokens:  2048,
		Tools:      []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice: "auto",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}, []workspace.ToolResult{{Tool: "context_pack", Summary: "candidate packet"}}, true)

	if opts.ToolChoice != "auto" {
		ginkgo.Fail(fmt.Sprintf("candidate recovery without a source read should not force tool choice, got %q", opts.ToolChoice), 1)
	}

	if opts.MaxTokens != 2048 {
		ginkgo.Fail(fmt.Sprintf("candidate recovery without a source read should preserve budget, got %d", opts.MaxTokens), 1)
	}
})

var _ = ginkgo.It("ReadPathsFromResultsCountsCodeNavigateRouteSummary", func() {
	results := []workspace.ToolResult{
		codeNavigateSourceWindowResult(
			"cmd/idiom/product_workflow.go",
			1382,
			1501,
			0,
			strings.Join([]string{
				"capability: read_symbol",
				"language: go",
				"routed_tool: go_read_symbol",
				"route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1382-1501",
				"",
				"1382:func runProductTDD(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {",
			}, "\n"),
		),
		readFileSourceWindowResult("cmd/idiom/product_workflow_test.go", 3209, 3230, 0, "3209:func TestProductWorkflow(t *testing.T) {}\n"),
	}

	paths := newToolResultEvidenceProjection(results).ReadPaths()
	for _, want := range []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"} {
		if _, ok := paths[want]; !ok {
			ginkgo.Fail(fmt.Sprintf("read paths missing %q: %#v", want, paths), 1)
		}
	}
})

var _ = ginkgo.It("ReadPathsFromResultsDoesNotCountRouteSummaryWithoutSourceBody", func() {
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output: strings.Join([]string{
			"capability: read_symbol",
			"language: go",
			"routed_tool: go_read_symbol",
			"route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1382-1501",
		}, "\n"),
	}}

	paths := newToolResultEvidenceProjection(results).ReadPaths()
	if _, ok := paths["cmd/idiom/product_workflow.go"]; ok {
		ginkgo.Fail(fmt.Sprintf("route_summary without source body should not satisfy source readiness: %#v", paths), 1)
	}
})

const syntheticPolicyGuardApplyObjective = "We are continuing TDD completion work from docs/design-prd-adr-brief-research-workflow.md. Implement one bounded topic only: policy guard apply must reject predictable staging temp symlinks before durable writes. Focus on the policy guard apply path, not the surrounding workflow boilerplate. Add regression tests proving staging temp symlinks are rejected without partial mutation. Use repo discovery first, make the minimal code changes, and run the focused tests."

func syntheticPolicyGuardApplySearchOutput() string {
	return strings.Join([]string{
		`internal/policy/guard.go:34: // policy guard planning metadata`,
		`cmd/idiom/policy.go:65: idiom policy guard --apply updates workflow summaries`,
		`internal/policy/guard_apply.go:88: tmpPath := stagingPath + ".tmp"`,
		`internal/policy/guard_apply.go:92: if err := rejectSymlinkTempPath(tmpPath); err != nil {`,
	}, "\n")
}

var _ = ginkgo.It("LiteralRecoverySearchPatternPrioritizesTopicOverWorkflowBoilerplate", func() {
	got := literalRecoverySearchPattern(syntheticPolicyGuardApplyObjective)
	for _, want := range []string{"policy", "guard", "apply", "temp"} {
		if !strings.Contains(got, want) {
			ginkgo.Fail(fmt.Sprintf("recovery search pattern missing %q: %q", want, got), 1)
		}
	}

	for _, notWant := range []string{"tdd", "prd", "adr", "brief", "research", "workflow"} {
		if strings.Contains(got, notWant) {
			ginkgo.Fail(fmt.Sprintf("recovery search pattern should not spend early terms on workflow boilerplate %q: %q", notWant, got), 1)
		}
	}
})

var _ = ginkgo.It("CompletionGateRecoveryPromptKeepsUnreadDiscoveredCandidateReadVisible", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         syntheticPolicyGuardApplyObjective,
		},
	})

	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/test.json with 2 candidate file(s)",
			Facts:   contextPackCandidateFactsForTest("internal/policy/guard.go", "internal/policy/guard_apply.go"),
		},
		{
			Tool:    "search_content",
			Summary: "Found matching lines.",
			Output:  syntheticPolicyGuardApplySearchOutput(),
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/policy/guard.go lines 1-180 (total lines: 180)",
			Output:  "type PolicyGuardPlan struct{}",
			Paths:   []string{"internal/policy/guard.go"},
			Facts:   sourceWindowFactsForTest("internal/policy/guard.go", 1, 180, 180),
		},
	}
	prompt, ok := runner.completionGateRecoveryPrompt(results)

	if !ok {
		ginkgo.Fail("expected candidate-scope recovery prompt", 1)
	}

	for _, want := range []string{
		"Allowed/candidate paths are not coverage obligations",
		"submit_plan",
		"Read candidate path(s): internal/policy/guard.go",
		"read one different unread candidate path",
		"internal/policy/guard_apply.go",
	} {
		if !strings.Contains(prompt, want) {
			ginkgo.Fail(fmt.Sprintf("recovery prompt missing %q:\n%s", want, prompt), 1)
		}
	}
})

var _ = ginkgo.It("PlanExecuteCandidateScopeReadinessDoesNotDeferForUnreadBetterCandidate", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Objective:         syntheticPolicyGuardApplyObjective,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/test.json with 2 candidate file(s)",
			Facts:   contextPackCandidateFactsForTest("internal/policy/guard.go", "internal/policy/guard_apply.go"),
		},
		{
			Tool:    "search_content",
			Summary: "Found matching lines.",
			Output:  syntheticPolicyGuardApplySearchOutput(),
		},
		readFileSourceWindowResult("internal/policy/guard.go", 1, 180, 180, "type PolicyGuardPlan struct{}"),
	}
	got := planThenExecuteReadyToSubmitNudge(brief, results, planThenExecutePathAuthority{})

	if got == "" {
		ginkgo.Fail("unscoped candidate evidence should create submit-ready guidance without requiring every ranked candidate", 1)
	}

	for _, want := range []string{
		"internal/policy/guard.go",
		"Allowed/candidate paths are not coverage obligations",
		"One bounded source-read/search for a different candidate path",
	} {
		if !strings.Contains(got, want) {
			ginkgo.Fail(fmt.Sprintf("readiness nudge missing %q:\n%s", want, got), 1)
		}
	}
})

var _ = ginkgo.It("PlanExecuteCandidateScopeReadinessDoesNotDeferForUnreadAllowedPath", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		Contract:         "implementation_result",
		AllowedEditPaths: []string{"pkg/feature.go", "pkg/feature_test.go"},
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("pkg/feature.go", 1, 20, 20, ""),
	}

	if got := planThenExecuteReadyToSubmitNudge(brief, results, planThenExecutePathAuthority{}); got != "" {
		state := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})
		if !slices.Contains(state.SelectedEditPaths, "pkg/feature.go") || slices.Contains(state.SelectedEditPaths, "pkg/feature_test.go") {
			ginkgo.Fail(fmt.Sprintf("selected edit paths = %#v, want only read allowed candidate", state.SelectedEditPaths), 1)
		}

		return
	}

	ginkgo.Fail("expected candidate-scope readiness after one allowed candidate source read", 1)
})

var _ = ginkgo.It("PlanThenExecutePlannerSurfaceKeepsProductWorkflowBrowsers", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})

	runner := newRunnerRunFixture(&Runner{
		Workspace: manager,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"recovery_inspect", "recovery_status", "product_efforts", "product_status", "read_file", "submit_plan"},
			RequiredTools:     []string{"submit_plan"},
		},
	})

	allowed := runner.advertisedAllowedTools(manager.ToolSurfaceSnapshotForProfile(workspace.ToolSurfaceProfilePlanSubmissionPlanner).AllowedTools())
	for _, tool := range []string{"recovery_inspect", "recovery_status", "product_efforts", "product_status"} {
		if !slices.Contains(allowed, tool) {
			ginkgo.Fail(fmt.Sprintf("planner surface lost %s; allowed=%#v", tool, allowed), 1)
		}

		if decision := toolAuthorizationSurfaceDecision(workspace.ToolRequest{Tool: tool}, allowed, false); decision.Rejected {
			ginkgo.Fail(fmt.Sprintf("%s should be callable in plan-execute planner surface, summary=%q error=%q allowed=%#v", tool, decision.Summary, decision.Error, allowed), 1)
		}
	}

	if slices.Contains(allowed, "recovery_decompose") {
		ginkgo.Fail(fmt.Sprintf("planner surface must not expose recovery_decompose because it writes recovery packets, allowed=%#v", allowed), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteControlRailsKeepSubmitPlanContract", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	runner := newRunnerRunFixture(&Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "submit_plan", "edit_file", "apply_patch"},
			RequiredTools:     []string{"submit_plan"},
			ReadinessGate:     []string{"submit_plan_called_or_blocker_reported"},
			SearchPolicy:      []string{"submit_plan_when_edit_set_is_supported"},
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	})

	rails := runner.controlRailsForBrief(runner.state.runtimeBrief)
	if !slices.Contains(rails.RequiredTools, "submit_plan") {
		ginkgo.Fail(fmt.Sprintf("control rails must require submit_plan for planner phase, got %#v", rails.RequiredTools), 1)
	}

	if slices.Contains(rails.RequiredTools, workspace.RequiredToolEditFileOrApplyPatch) {
		ginkgo.Fail(fmt.Sprintf("control rails must not advertise per-edit write requirement in planner phase: %#v", rails.RequiredTools), 1)
	}

	if !slices.Contains(rails.ReadinessGate, "submit_plan_or_source_satisfied_or_blocker_reported") {
		ginkgo.Fail(fmt.Sprintf("control rails must keep submit_plan readiness gate, got %#v", rails.ReadinessGate), 1)
	}

	if !slices.Contains(rails.SearchPolicy, "required_existing_paths_need_source_body_before_submit_plan") {
		ginkgo.Fail(fmt.Sprintf("control rails must advertise source-body-before-submit_plan policy, got %#v", rails.SearchPolicy), 1)
	}

	if slices.Contains(rails.SearchPolicy, "trust_preloaded_current_state_anchors") {
		ginkgo.Fail(fmt.Sprintf("control rails must not imply preloaded anchors satisfy source-body evidence, got %#v", rails.SearchPolicy), 1)
	}

	if !slices.Contains(rails.FinalCritic, "submit_plan_called") ||
		!slices.Contains(rails.FinalCritic, "source_satisfied_or_blocker_only_with_per_path_evidence") {

		ginkgo.Fail(fmt.Sprintf("control rails must keep submit_plan final critic, got %#v", rails.FinalCritic), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteRequiredPathHintsUseSubmitPlanLanguage", func() {
	brief := runnerBriefWithPhaseHints(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredTools:     []string{"submit_plan"},
		RequiredEditPaths: []string{"a.txt", "b.txt"},
		TargetPacket:      []string{"verified anchors"},
	}, nil, planThenExecutePathAuthority{}, implementationCompletionState{MissingRequiredPaths: []string{"a.txt", "b.txt"}})

	joined := strings.Join(append(append([]string{}, brief.RequiredNext...), brief.InvalidActions...), "\n")
	if !strings.Contains(joined, "submit_plan") {
		ginkgo.Fail(fmt.Sprintf("planner-phase hints must name submit_plan, got %#v / %#v", brief.RequiredNext, brief.InvalidActions), 1)
	}

	if !strings.Contains(joined, "source-body evidence remains pending for required_edit_paths [a.txt, b.txt]") ||
		!strings.Contains(joined, "choose any permission-authorized inspection") {
		ginkgo.Fail(fmt.Sprintf("planner-phase hints must project the complete pending source set without selecting a next path, got %#v / %#v", brief.RequiredNext, brief.InvalidActions), 1)
	}

	if !strings.Contains(joined, "submit_plan before every existing required_edit_path has satisfied its source-body obligation") {
		ginkgo.Fail(fmt.Sprintf("planner-phase hints must reject premature submit_plan, got %#v / %#v", brief.RequiredNext, brief.InvalidActions), 1)
	}

	if !strings.Contains(joined, "source-satisfied") {
		ginkgo.Fail(fmt.Sprintf("planner-phase hints must name source-satisfied closeout, got %#v / %#v", brief.RequiredNext, brief.InvalidActions), 1)
	}

	if strings.Contains(joined, "write capability") || strings.Contains(joined, "first required-path write") {
		ginkgo.Fail(fmt.Sprintf("planner-phase hints must not use generic write/edit closeout language: %s", joined), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteRequiredPathHintsSwitchToSubmitPlanAfterSourceEvidence", func() {
	brief := runnerBriefWithPhaseHints(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredTools:     []string{"submit_plan"},
		RequiredEditPaths: []string{"a.txt", "b.txt"},
		TargetPacket:      []string{"verified anchors"},
	}, []workspace.ToolResult{
		readFileSourceWindowResult("a.txt", 1, 10, 10, "current a"),
		readFileSourceWindowResult("b.txt", 1, 10, 10, "current b"),
	}, planThenExecutePathAuthority{}, implementationCompletionState{MissingRequiredPaths: []string{"a.txt", "b.txt"}})

	joined := strings.Join(append(append([]string{}, brief.RequiredNext...), brief.InvalidActions...), "\n")
	if !strings.Contains(joined, "submit_plan") || !strings.Contains(joined, "source_satisfied_paths") {
		ginkgo.Fail(fmt.Sprintf("planner-phase hints should require submit_plan once required paths are read, got %#v / %#v", brief.RequiredNext, brief.InvalidActions), 1)
	}

	if strings.Contains(joined, "source-body evidence remains pending for required_edit_paths") {
		ginkgo.Fail(fmt.Sprintf("planner-phase hints should not keep source-read-first language after every required path is read: %s", joined), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteRequiredPathHintsClearStaleSourceReadLanguageAfterReadiness", func() {
	brief := runnerBriefWithPhaseHints(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredTools:     []string{"submit_plan"},
		RequiredEditPaths: []string{"a.txt", "b.txt"},
		RequiredNext: []string{
			"source-body evidence remains pending for required_edit_paths [a.txt, b.txt]; choose any permission-authorized inspection that resolves a concrete edit-safety fact",
		},
		InvalidActions: []string{
			"submit_plan before every existing required_edit_path has satisfied its source-body obligation",
		},
		SearchPolicy: []string{
			"pending required_edit_paths are advisory evidence targets; all permission-authorized inspection remains callable",
			"keep this unrelated policy",
		},
	}, []workspace.ToolResult{
		readFileSourceWindowResult("a.txt", 1, 10, 10, "current a"),
		codeNavigateSourceWindowResult("b.txt", 1, 10, 0, "Read B in b.txt lines 1-10\n1: current b"),
	}, planThenExecutePathAuthority{}, implementationCompletionState{MissingRequiredPaths: []string{"a.txt", "b.txt"}})

	joined := strings.Join(append(append(append([]string{}, brief.RequiredNext...), brief.InvalidActions...), brief.SearchPolicy...), "\n")
	if !strings.Contains(joined, "submit_plan") || !strings.Contains(joined, "source_satisfied_paths") {
		ginkgo.Fail(fmt.Sprintf("planner-phase hints should require submit_plan once source evidence is complete, got %s", joined), 1)
	}

	for _, stale := range []string{
		"source-body evidence remains pending for required_edit_paths",
		"submit_plan before every existing required_edit_path has satisfied",
		"pending required_edit_paths are advisory evidence targets",
	} {
		if strings.Contains(joined, stale) {
			ginkgo.Fail(fmt.Sprintf("planner-phase hints retained stale source-read language %q: %s", stale, joined), 1)
		}
	}

	if !strings.Contains(joined, "keep this unrelated policy") {
		ginkgo.Fail(fmt.Sprintf("planner-phase hint cleanup should preserve unrelated policy, got %s", joined), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteCanonicalNewFileAuthorityBindsToNamedPathOnly", func() {
	t := ginkgo.GinkgoT()
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
		Objective:  `Use plan-execute to update labels and add the generated label implementation.`,
		RequiredEditPaths: []string{
			"internal/labels/labels.go",
			"internal/labels/generated_label.go",
		},
		RequiredEvidence: []string{string(workerruntime.RequiredEvidenceGitHistoryRationale)},
		AllowedTools:     []string{"read_file", "history_range", "history_symbol", "submit_plan"},
	}
	scope := workspace.CanonicalMutationScopeProjection{
		RequiredCoverageIDs: []string{"internal/labels/labels.go", "internal/labels/generated_label.go"},
		Rules: []workspace.CanonicalMutationScopeRuleProjection{
			{Kind: workspace.MutationScopeExactFile, Source: workspace.MutationScopeRequiredPath, CanonicalID: "internal/labels/labels.go"},
			{Kind: workspace.MutationScopeExactFile, Source: workspace.MutationScopeAuthorizedNewFile, CanonicalID: "internal/labels/generated_label.go"},
		},
		Presentations: []workspace.CanonicalMutationScopePresentationProjection{
			{PresentedPath: "internal/labels/labels.go", CanonicalID: "internal/labels/labels.go", Source: workspace.MutationScopeRequiredPath},
			{PresentedPath: "internal/labels/generated_label.go", CanonicalID: "internal/labels/generated_label.go", Source: workspace.MutationScopeAuthorizedNewFile},
		},
	}
	authority := planThenExecutePathAuthority{scope: &scope}

	if got := planThenExecuteRequiredPathState(authority, nil, "internal/labels/labels.go"); got != planThenExecuteRequiredPathExistingSourceBodyRequired {
		ginkgo.Fail(fmt.Sprintf("existing path state = %q, want existing_source_body_required", got), 1)
	}

	if got := planThenExecuteRequiredPathState(authority, nil, "internal/labels/generated_label.go"); got != planThenExecuteRequiredPathNewFileBodyRequired {
		ginkgo.Fail(fmt.Sprintf("generated path state = %q, want new_file_body_required", got), 1)
	}

	state := derivePlanExecutePlanningState(brief, nil, PlanExecutePlanningTransport{TextualToolParser: true}, authority)
	if state.Mode != PlanExecutePlanningModeRequiredSource {
		ginkgo.Fail(fmt.Sprintf("mode = %q, want required_source; states=%#v", state.Mode, state.RequiredPathStates), 1)
	}
	if slices.Contains(state.ParserVisibleTools, "submit_plan") {
		ginkgo.Fail(fmt.Sprintf("mixed existing/new scope must not expose submit_plan before existing source evidence: %#v", state.ParserVisibleTools), 1)
	}

	withExistingSource := []workspace.ToolResult{
		readFileSourceWindowResult("internal/labels/labels.go", 1, 8, 8, "1:package labels\n3:func ShortLabel() string {\n4:\treturn \"Save changes\"\n5:}\n"),
	}
	state = derivePlanExecutePlanningState(brief, withExistingSource, PlanExecutePlanningTransport{TextualToolParser: true}, authority)
	if state.Mode != PlanExecutePlanningModeHistoryPending {
		ginkgo.Fail(fmt.Sprintf("mode after existing source read = %q, want history_pending", state.Mode), 1)
	}
	assertEqualSet(t, state.SelectedEditPaths, []string{"internal/labels/labels.go"})

	withHistory := append(withExistingSource, gitHistoryRangeResult("internal/labels/labels.go"))
	state = derivePlanExecutePlanningState(brief, withHistory, PlanExecutePlanningTransport{TextualToolParser: true}, authority)
	if state.Mode != PlanExecutePlanningModeSubmitReady {
		ginkgo.Fail(fmt.Sprintf("mode after existing history = %q, want submit_ready", state.Mode), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteMissingSubmitPlanGateNamesSubmitPlan", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	})

	got, rejected := runner.mutationCompletionRejection("Done.", nil)
	if !rejected {
		ginkgo.Fail("planner phase without submit_plan must be rejected", 1)
	}

	if !strings.Contains(got.Error, "submit_plan") {
		ginkgo.Fail(fmt.Sprintf("planner-phase rejection must name submit_plan, got %#v", got), 1)
	}

	if !strings.Contains(got.Error, "source-satisfied") {
		ginkgo.Fail(fmt.Sprintf("planner-phase rejection must offer source-satisfied closeout, got %#v", got), 1)
	}

	if strings.Contains(got.Error, "no successful write tool result") {
		ginkgo.Fail(fmt.Sprintf("planner-phase rejection must not use generic write-tool language: %#v", got), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteMissingSubmitPlanGateWithoutRequiredPathsNamesCandidateScope", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	})

	got, rejected := runner.mutationCompletionRejection("Done.", nil)
	if !rejected {
		ginkgo.Fail("planner phase without submit_plan must be rejected", 1)
	}

	if !strings.Contains(got.Error, "candidate/allowed edit path") {
		ginkgo.Fail(fmt.Sprintf("planner-phase rejection without required paths should name candidate scope, got %#v", got), 1)
	}

	if strings.Contains(got.Error, "every required_edit_paths entry") {
		ginkgo.Fail(fmt.Sprintf("planner-phase rejection without required paths should not imply required path coverage, got %#v", got), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteCandidateCompletionRejectsGenericBlockedProse", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedEditPaths:  []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
		},
	})
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read file.", Output: `{"path":"cmd/idiom/product_workflow.go","content":"before\n"}`},
		{Tool: "submit_plan", Summary: "Plan rejected", Error: "old_text not found in cmd/idiom/product_workflow.go"},
	}

	got, rejected := runner.mutationCompletionRejection("The planner is blocked because candidate source is already ready but no plan was submitted.", results)
	if !rejected {
		ginkgo.Fail(fmt.Sprintf("candidate plan-execute must reject generic blocked prose without submit_plan, got %#v", got), 1)
	}

	if !strings.Contains(got.Error, "submit_plan") || !strings.Contains(got.Error, "<<QUESTION>>") {
		ginkgo.Fail(fmt.Sprintf("candidate rejection should name the exact planner closeout contract, got %#v", got), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteCandidateCompletionRejectsMentionedDecisionBlock", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedEditPaths:  []string{"cmd/idiom/product_workflow.go"},
		},
	})

	got, rejected := runner.mutationCompletionRejection("I know I could use Decision: block, but I will keep investigating.", nil)
	if !rejected {
		ginkgo.Fail(fmt.Sprintf("mentioning Decision: block in prose must not satisfy typed blocker closeout, got %#v", got), 1)
	}
})

var _ = ginkgo.It("MutationCompletionGateStillRejectsImplementationLaneWithoutEdits", func() {
	// Sanity check: my plan-execute exemption must not weaken the gate
	// for normal implementation lanes. A regular implementation that
	// finalizes without any write tool result (and no submit_plan)
	// should still be rejected.
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt"},
		},
	})
	got, rejected := runner.mutationCompletionRejection("Done.", nil)

	if !rejected {
		ginkgo.Fail(fmt.Sprintf("implementation lane with no edits must be rejected; got %#v", got), 1)
	}
})

var _ = ginkgo.It("CompletionGateRecoveryPromptForUnscopedImplementationNoWrite", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	})

	prompt, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		{Tool: "context_pack", Summary: "Ranked context packet produced.", Facts: contextPackCandidateFactsForTest("candidate/path.go")},
	})
	if !ok {
		ginkgo.Fail("expected completion gate recovery prompt for direct implementation with no write", 1)
	}

	for _, want := range []string{
		"before any successful workspace write",
		"No required_edit_paths list is active",
		"target-selection failure",
		"parser-readable action",
		"not prose",
		"edit_file",
		"write_file",
		"append_file",
		"apply_patch",
		"Do not call a write tool until",
		"one visible JSON object",
		"Qwen key-value `tool:` format",
		"Function-call write syntax is not executable",
		"previous `context_pack` did not establish an exact edit target",
		"`search_content`",
		"named_gap",
		"why_not_answering",
		"<<QUESTION>>",
		"Do not repeat a final answer",
	} {
		if !strings.Contains(prompt, want) {
			ginkgo.Fail(fmt.Sprintf("direct implementation recovery prompt missing %q: %q", want, prompt), 1)
		}
	}

	if strings.Contains(prompt, "submit_plan") {
		ginkgo.Fail(fmt.Sprintf("direct implementation recovery must not point at planner-phase submit_plan: %q", prompt), 1)
	}

	for _, forbidden := range []string{"Current evidence paths", `"path":"candidate/path.go"`} {
		if strings.Contains(prompt, forbidden) {
			ginkgo.Fail(fmt.Sprintf("context_pack-only recovery should not treat packet candidates as concrete targets (%q): %q", forbidden, prompt), 1)
		}
	}
})

var _ = ginkgo.It("CompletionGateRecoveryPromptForUnscopedImplementationNoWriteKeepsReadsAfterRepeatedGate", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	})
	results := []workspace.ToolResult{
		readFileSourceWindowResult("internal/surfaces/tui/app/rendering.go", 1, 120, 332, "1:package app\n2:func renderMessages() {}\n"),
		{
			Tool:    "completion_gate",
			Summary: "Mutation completion gate rejected final answer",
			Error:   "this task asks for a workspace change, but no successful write tool result is present",
		},
		readFileSourceWindowResult("internal/surfaces/tui/app/model_state.go", 1, 120, 1456, "1:package app\n2:type modelState struct{}\n"),
		{
			Tool:    "completion_gate",
			Summary: "Mutation completion gate rejected final answer",
			Error:   "this task asks for a workspace change, but no successful write tool result is present",
		},
	}

	prompt, ok := runner.completionGateRecoveryPrompt(results)
	if !ok {
		ginkgo.Fail("expected terminal no-write recovery prompt", 1)
	}

	for _, want := range []string{
		"target-write-ready",
		"parser-readable write",
		"bounded read/search",
		"The model owns whether more evidence is needed",
		"internal/surfaces/tui/app/model_state.go",
		"`<<QUESTION>>:`",
	} {
		if !strings.Contains(prompt, want) {
			ginkgo.Fail(fmt.Sprintf("terminal no-write recovery prompt missing %q:\n%s", want, prompt), 1)
		}
	}

	for _, notWant := range []string{"Read/search/discovery tools are hidden", "Do not inspect, search, navigate"} {
		if strings.Contains(prompt, notWant) {
			ginkgo.Fail(fmt.Sprintf("terminal no-write recovery prompt must not hide discovery with %q:\n%s", notWant, prompt), 1)
		}
	}
})

var _ = ginkgo.It("CompletionGateRecoveryPromptForDirectRequiredPathImplementationNoWrite", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"internal/workflows/productworkflow/coverage_gate.go", "internal/workflows/productworkflow/coverage_gate_test.go"},
		},
	})

	prompt, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		readFileSourceWindowResult("internal/workflows/productworkflow/coverage_gate.go", 1, 220, 220, "1:package productworkflow\n"),
		{Tool: "context_query", Summary: "Answered query for ValidatePRDTDDObligationCrossLinks"},
	})
	if !ok {
		ginkgo.Fail("expected completion gate recovery prompt for direct required-path implementation", 1)
	}

	completion := runner.implementationCompletionState([]workspace.ToolResult{
		readFileSourceWindowResult("internal/workflows/productworkflow/coverage_gate.go", 1, 220, 220, "1:package productworkflow\n"),
		{Tool: "context_query", Summary: "Answered query for ValidatePRDTDDObligationCrossLinks"},
	})
	if !slices.Equal(completion.MissingRequiredPaths, []string{"internal/workflows/productworkflow/coverage_gate.go", "internal/workflows/productworkflow/coverage_gate_test.go"}) {
		ginkgo.Fail(fmt.Sprintf("typed missing required paths = %#v", completion.MissingRequiredPaths), 1)
	}

	for _, want := range []string{
		"required edit path(s) remain unchanged",
		"internal/workflows/productworkflow/coverage_gate.go",
		"internal/workflows/productworkflow/coverage_gate_test.go",
		"parser-readable action",
		"not prose",
		"Hidden-only reasoning is not completion evidence",
		"edit_file",
		"write_file",
		"append_file",
		"apply_patch",
		"Every remaining existing source path already has source-body anchor evidence",
		"permission-authorized inspection remains callable",
		"<<QUESTION>>",
	} {
		if !strings.Contains(prompt, want) {
			ginkgo.Fail(fmt.Sprintf("direct required-path recovery prompt missing %q:\n%s", want, prompt), 1)
		}
	}

	if strings.Contains(prompt, "submit_plan") {
		ginkgo.Fail(fmt.Sprintf("direct implementation recovery must not point at planner-phase submit_plan:\n%s", prompt), 1)
	}
	if strings.Contains(prompt, "Source-body evidence remains pending") {
		ginkgo.Fail(fmt.Sprintf("typed recovery state should not invent a pending source prerequisite:\n%s", prompt), 1)
	}
})

var _ = ginkgo.It("CompletionGateRecoveryPromptUsesSearchContentEvidencePath", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	})

	prompt, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		{
			Tool:    "search_content",
			Summary: "Found 24 matching line(s).",
			Output:  "internal/surfaces/tui/app/rendering.go:50: if banner := m.errorBanner(); banner != \"\" {\ninternal/frontier/locate.go:12: banner",
		},
	})
	if !ok {
		ginkgo.Fail("expected completion gate recovery prompt", 1)
	}

	for _, want := range []string{
		"Current evidence paths",
		"internal/surfaces/tui/app/rendering.go",
		`"tool":"read_file"`,
		`"path":"internal/surfaces/tui/app/rendering.go"`,
		"Do not call `context_pack` again",
	} {
		if !strings.Contains(prompt, want) {
			ginkgo.Fail(fmt.Sprintf("search-content recovery prompt missing %q:\n%s", want, prompt), 1)
		}
	}

	if strings.Contains(prompt, `"tool":"context_pack"`) {
		ginkgo.Fail(fmt.Sprintf("search-content recovery should not request context_pack:\n%s", prompt), 1)
	}
})

var _ = ginkgo.It("CompletionGateRecoveryPromptFallsBackAfterContextPackFailure", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	})

	prompt, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		{Tool: "context_pack", Summary: "context_pack failed", Error: "index unavailable"},
	})
	if !ok {
		ginkgo.Fail("expected completion gate recovery prompt", 1)
	}

	for _, want := range []string{
		"do not call `context_pack` again",
		"bounded fallback discovery phase",
		"`search_files`",
		"`search_content`",
		"If a literal phrase missed, broaden",
		"`read_file`",
	} {
		if !strings.Contains(prompt, want) {
			ginkgo.Fail(fmt.Sprintf("context fallback recovery prompt missing %q:\n%s", want, prompt), 1)
		}
	}

	if strings.Contains(prompt, `"tool":"context_pack"`) {
		ginkgo.Fail(fmt.Sprintf("context_pack failure recovery should not ask for another context_pack call:\n%s", prompt), 1)
	}
})

var _ = ginkgo.It("CompletionGateRecoveryPromptFallsBackAfterEmptyContextPack", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	})

	prompt, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		{Tool: "context_pack", Summary: "0 candidates"},
	})
	if !ok {
		ginkgo.Fail("expected completion gate recovery prompt", 1)
	}

	if !strings.Contains(prompt, "bounded fallback discovery phase") {
		ginkgo.Fail(fmt.Sprintf("empty context_pack should trigger fallback discovery:\n%s", prompt), 1)
	}

	if strings.Contains(prompt, `"tool":"context_pack"`) {
		ginkgo.Fail(fmt.Sprintf("empty context_pack recovery should not ask for another context_pack call:\n%s", prompt), 1)
	}
})

var _ = ginkgo.It("CompletionGateRecoveryChatOptionsPreservesDirectMutationModelMode", func() {
	thinkingOn := true
	opts := completionGateRecoveryChatOptionsForBackend(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          16384,
		Tools:              []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "edit_file"}}},
		ToolChoice:         "auto",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}, []workspace.ToolResult{{Tool: "context_pack", Summary: "context"}}, true, planThenExecutePathAuthority{}, deriveImplementationProgressState(

		workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		}, []workspace.ToolResult{{Tool: "context_pack", Summary: "context"}}))

	if opts.EnableThinking == nil || !*opts.EnableThinking {
		ginkgo.Fail(fmt.Sprintf("direct mutation recovery should preserve thinking mode, got %#v", opts.EnableThinking), 1)
	}

	if !opts.PreserveThinking {
		ginkgo.Fail("direct mutation recovery should preserve hidden-reasoning policy", 1)
	}

	if opts.ChatTemplateKwargs {
		ginkgo.Fail("direct mutation recovery should not force request-level chat_template_kwargs", 1)
	}

	if opts.MaxTokens != 2048 {
		ginkgo.Fail(fmt.Sprintf("direct mutation recovery MaxTokens = %d, want 2048", opts.MaxTokens), 1)
	}

	if opts.ToolChoice != "auto" {
		ginkgo.Fail(fmt.Sprintf("direct mutation recovery should preserve existing tool choice, got %q", opts.ToolChoice), 1)
	}
})

var _ = ginkgo.It("CompletionGateRecoveryChatOptionsForcesVisibleToolAfterConcreteRead", func() {
	thinkingOn := true
	opts := completionGateRecoveryChatOptionsForBackend(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          16384,
		Tools:              []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "edit_file"}}},
		ToolChoice:         "auto",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}, []workspace.ToolResult{{Tool: "read_file", Summary: "Read internal/surfaces/tui/app/rendering.go", Output: "rightInfo := statusStyle.Render(...) "}}, true, planThenExecutePathAuthority{}, deriveImplementationProgressState(

		workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		}, []workspace.ToolResult{{Tool: "read_file", Summary: "Read internal/surfaces/tui/app/rendering.go", Output: "rightInfo := statusStyle.Render(...) "}}))

	if opts.EnableThinking == nil || !*opts.EnableThinking {
		ginkgo.Fail(fmt.Sprintf("post-read mutation recovery should keep current-response thinking available, got %#v", opts.EnableThinking), 1)
	}

	if opts.PreserveThinking {
		ginkgo.Fail("post-read mutation recovery should not preserve hidden reasoning", 1)
	}

	if opts.ChatTemplateKwargs {
		ginkgo.Fail("post-read mutation recovery should not force request-level chat_template_kwargs", 1)
	}

	if opts.ToolChoice != "auto" {
		ginkgo.Fail(fmt.Sprintf("post-read mutation recovery should preserve existing tool choice, got %q", opts.ToolChoice), 1)
	}

	if opts.MaxTokens != 1024 {
		ginkgo.Fail(fmt.Sprintf("post-read mutation recovery MaxTokens = %d, want 1024", opts.MaxTokens), 1)
	}
})

var _ = ginkgo.It("CompletionGateRecoveryChatOptionsPreservesCurrentThinkingForDirectRequiredPaths", func() {
	thinkingOn := true
	opts := completionGateRecoveryChatOptionsForBackend(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          16384,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "edit_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "write_file"}},
		},
		ToolChoice: "required",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"internal/workflows/productworkflow/coverage_gate.go", "internal/workflows/productworkflow/coverage_gate_test.go"},
	}, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read internal/workflows/productworkflow/coverage_gate.go lines 1-220"},
		{Tool: "context_query", Summary: "Answered query for ValidatePRDTDDObligationCrossLinks"},
	}, true, planThenExecutePathAuthority{}, deriveImplementationProgressState(

		workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"internal/workflows/productworkflow/coverage_gate.go", "internal/workflows/productworkflow/coverage_gate_test.go"},
		}, []workspace.ToolResult{
			{Tool: "read_file", Summary: "Read internal/workflows/productworkflow/coverage_gate.go lines 1-220"},
			{Tool: "context_query", Summary: "Answered query for ValidatePRDTDDObligationCrossLinks"},
		}))

	if opts.EnableThinking == nil || !*opts.EnableThinking {
		ginkgo.Fail(fmt.Sprintf("direct required-path recovery should keep current-response thinking available, got %#v", opts.EnableThinking), 1)
	}

	if opts.PreserveThinking {
		ginkgo.Fail("direct required-path recovery should drop prior hidden reasoning", 1)
	}

	if opts.ChatTemplateKwargs {
		ginkgo.Fail("direct required-path recovery should not force request-level no-thinking kwargs", 1)
	}

	if opts.ToolChoice != "auto" {
		ginkgo.Fail(fmt.Sprintf("direct required-path recovery should allow text blockers alongside tools, got %q", opts.ToolChoice), 1)
	}

	if opts.MaxTokens != 4096 {
		ginkgo.Fail(fmt.Sprintf("direct required-path recovery MaxTokens = %d, want 4096", opts.MaxTokens), 1)
	}
})

var _ = ginkgo.It("CompletionGateRecoveryChatOptionsForcesVisibleTextualActionWhenAnchorsReady", func() {
	thinkingOn := true
	opts := completionGateRecoveryChatOptionsForBackend(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          16384,
		ToolChoice:         "auto",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go", "a_test.go"},
	}, []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 20, 20, "1:package a"),
		readFileSourceWindowResult("a_test.go", 1, 40, 40, "1:package a"),
	}, false, planThenExecutePathAuthority{}, deriveImplementationProgressState(

		workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go", "a_test.go"},
		}, []workspace.ToolResult{
			readFileSourceWindowResult("a.go", 1, 20, 20, "1:package a"),
			readFileSourceWindowResult("a_test.go", 1, 40, 40, "1:package a"),
		}))

	if opts.EnableThinking == nil || *opts.EnableThinking {
		ginkgo.Fail(fmt.Sprintf("textual required-path write recovery should disable hidden thinking after anchors are ready, got %#v", opts.EnableThinking), 1)
	}

	if !opts.ChatTemplateKwargs {
		ginkgo.Fail("textual required-path write recovery should send request-level chat template kwargs", 1)
	}

	if opts.PreserveThinking {
		ginkgo.Fail("textual required-path write recovery should not preserve prior hidden reasoning", 1)
	}

	if opts.ToolChoice != "auto" {
		ginkgo.Fail(fmt.Sprintf("textual backend should keep terminal-aware auto tool choice, got %q", opts.ToolChoice), 1)
	}

	if opts.ResponseFormatJSON {
		ginkgo.Fail("implementation recovery should not force JSON response format; only textual tool terminal contracts should", 1)
	}
})

var _ = ginkgo.It("ImplementationRequiredPathHiddenOnlyRecoveryStallDisablesThinkingAfterRetry", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		RequiredEditPaths: []string{"a.go"},
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 4, 4, "1:package main\n2:\n3:func main() {}\n"),
	}
	reply := llama.ChatMessage{ReasoningContent: "I can make the edit now."}

	if implementationRequiredPathHiddenOnlyRecoveryStall(brief, results, reply, 0, false, deriveImplementationProgressState(brief, results)) {
		ginkgo.Fail("first hidden-only recovery should keep bounded thinking available", 1)
	}

	if !implementationRequiredPathHiddenOnlyRecoveryStall(brief, results, reply, 1, false, deriveImplementationProgressState(brief, results)) {
		ginkgo.Fail("second hidden-only anchor-ready textual recovery should disable hidden thinking", 1)
	}

	if implementationRequiredPathHiddenOnlyRecoveryStall(brief, results, llama.ChatMessage{Content: `{"tool":"edit_file"}`}, 1, false, deriveImplementationProgressState(brief, results)) {
		ginkgo.Fail("visible content should not be treated as hidden-only stall", 1)
	}

	if implementationRequiredPathHiddenOnlyRecoveryStall(brief, results, reply, 1, true, deriveImplementationProgressState(brief, results)) {
		ginkgo.Fail("native tool backends should not use textual hidden-only fallback", 1)
	}
})

var _ = ginkgo.It("MutationHiddenOnlyNoWriteTerminalRecoveryDoesNotOverrideTargetWriteReady", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "implementation",
		Contract:   "implementation_result",
	}
	reply := llama.ChatMessage{ReasoningContent: "I know the edit now."}
	sourceRead := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 3, 3, "1:package a\n2:\n3:func A() {}\n"),
	}

	if !implementationTargetWriteReadySurfaceActive(brief, sourceRead, false) {
		ginkgo.Fail("test setup should be target-write-ready after concrete source evidence", 1)
	}
	if mutationHiddenOnlyNoWriteTerminalRecoveryStall(brief, sourceRead, reply, 1, false, deriveImplementationProgressState(brief, sourceRead)) {
		ginkgo.Fail("hidden-only textual retry after concrete source read must not override target-write-ready recovery", 1)
	}

	outlineOnly := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed file_outline via go_file_outline",
		Paths:   []string{"a.go"},
		Output:  "capability: file_outline\nroute_summary: Outlined 1 top-level declaration(s) in a.go.",
	}}
	if mutationHiddenOnlyNoWriteTerminalRecoveryStall(brief, outlineOnly, reply, 1, false, deriveImplementationProgressState(brief, outlineOnly)) {
		ginkgo.Fail("outline-only evidence must not satisfy concrete source-body recovery precondition", 1)
	}

	if mutationHiddenOnlyNoWriteTerminalRecoveryStall(brief, sourceRead, reply, 0, false, deriveImplementationProgressState(brief, sourceRead)) {
		ginkgo.Fail("first completion-gate rejection should keep normal bounded recovery", 1)
	}

	if mutationHiddenOnlyNoWriteTerminalRecoveryStall(brief, sourceRead, reply, 1, true, deriveImplementationProgressState(brief, sourceRead)) {
		ginkgo.Fail("native tool backends should not use textual hidden-only terminal recovery", 1)
	}

	plannerBrief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
		Contract:   "implementation_result",
	}
	if mutationHiddenOnlyNoWriteTerminalRecoveryStall(plannerBrief, sourceRead, reply, 1, false, deriveImplementationProgressState(plannerBrief, sourceRead)) {
		ginkgo.Fail("plan-execute planner recovery must keep submit_plan/source-satisfied/question terminal contract", 1)
	}

	targetWriteReadyBrief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "implementation",
		Contract:   "implementation_result",
		TargetPacket: []string{
			"conversation_context:previous_assistant_recommendation",
			"Editable candidate file from previous assistant recommendation: a.go",
		},
		AllowedEditPaths: []string{"a.go"},
		TargetRefs:       []string{"a.go"},
	}
	if !implementationTargetWriteReadySurfaceActive(targetWriteReadyBrief, sourceRead, false) {
		ginkgo.Fail("test setup should be target-write-ready after recommended source evidence", 1)
	}
	if mutationHiddenOnlyNoWriteTerminalRecoveryStall(targetWriteReadyBrief, sourceRead, reply, 1, false, deriveImplementationProgressState(targetWriteReadyBrief, sourceRead)) {
		ginkgo.Fail("target-write-ready hidden-only retry must stay on strategy-preserving recovery and keep reads available", 1)
	}

	if mutationHiddenOnlyNoWriteTerminalRecoveryStall(brief, append(sourceRead, workspace.ToolResult{Tool: "edit_file", Summary: "Edited a.go", ChangedPaths: []string{"a.go"}}), reply, 1, false, deriveImplementationProgressState(brief, append(sourceRead, workspace.ToolResult{Tool: "edit_file", Summary: "Edited a.go", ChangedPaths: []string{"a.go"}}))) {
		ginkgo.Fail("successful writes should disable no-write terminal recovery", 1)
	}

	if mutationHiddenOnlyNoWriteTerminalRecoveryStall(brief, sourceRead, llama.ChatMessage{Content: `{"tool":"edit_file"}`, ReasoningContent: "edit"}, 1, false, deriveImplementationProgressState(brief, sourceRead)) {
		ginkgo.Fail("visible content should not be treated as a hidden-only stall", 1)
	}
})

var _ = ginkgo.It("HiddenOnlyNoWriteTerminalRecoveryPromptKeepsInspectionCallableAndDoesNotChoosePatchShape", func() {
	prompt := hiddenOnlyNoWriteTerminalRecoveryPrompt()
	for _, want := range []string{
		"Hidden thinking is disabled",
		"Permission-authorized read/search/discovery tools remain callable",
		"visible parser-readable action",
		"`<<QUESTION>>:`",
	} {
		if !strings.Contains(prompt, want) {
			ginkgo.Fail(fmt.Sprintf("terminal recovery prompt missing %q:\n%s", want, prompt), 1)
		}
	}

	for _, notWant := range []string{
		"Read/search/discovery tools are hidden",
		"exactly one terminal action",
		`"tool":"edit_file"`,
		`"tool":"apply_patch"`,
		"old_text",
		"new_text",
		"*** Begin Patch",
		"candidate/path.go",
	} {
		if strings.Contains(prompt, notWant) {
			ginkgo.Fail(fmt.Sprintf("terminal recovery prompt should not choose an edit path or patch shape (%q):\n%s", notWant, prompt), 1)
		}
	}
})

var _ = ginkgo.It("SalvageToolRequestFromEmptyReasoning", func() {
	runner := newRunnerRunFixture(&Runner{})
	req, _, ok := runner.salvageToolRequestFromReasoning(llama.ChatMessage{
		Content:          "",
		ReasoningContent: "I should inspect it.\n\n```tool\n{\"tool\":\"read_file\",\"path\":\"internal/surfaces/tui/app/rendering.go\",\"start_line\":1,\"end_line\":180,\"named_gap\":\"render branch\",\"why_not_answering\":\"need source\"}\n```",
	}, nil)

	if !ok {
		ginkgo.Fail("expected reasoning tool request to be salvaged", 1)
	}

	if req.Tool != "read_file" || req.Path != "internal/surfaces/tui/app/rendering.go" {
		ginkgo.Fail(fmt.Sprintf("unexpected salvaged request: %#v", req), 1)
	}
})

var _ = ginkgo.It("SalvageToolRequestFromReasoningRejectsPlanExecuteSubmitPlan", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	})
	if req, _, ok := runner.salvageToolRequestFromReasoning(llama.ChatMessage{
		Content: "",
		ReasoningContent: "I have the anchor now.\n\n```tool\n" +
			`{"tool":"submit_plan","rationale":"Replace the banner model path with the configured alias.","edits":[{"path":"internal/surfaces/tui/app/rendering.go","old_text":"rightInfo := statusStyle.Render(serverState + \"  \" + textutil.Fallback(m.modelID, \"unknown\"))","new_text":"rightInfo := statusStyle.Render(serverState + \"  \" + textutil.Fallback(m.currentAliasForRole(\"writer\"), \"unknown\"))","purpose":"Display the model alias in the TUI banner instead of the raw model path."}]}` +
			"\n```",
	}, nil); ok {
		ginkgo.Fail(fmt.Sprintf("hidden-reasoning submit_plan must not execute; require visible/native tool call, got %#v", req), 1)
	}
})

var _ = ginkgo.It("SalvageToolRequestFromReasoningRejectsSubmitPlanOutsidePlanSubmission", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "implementation",
		},
	})
	if req, _, ok := runner.salvageToolRequestFromReasoning(llama.ChatMessage{
		Content: "",
		ReasoningContent: "```tool\n" +
			`{"tool":"submit_plan","rationale":"Plan.","edits":[{"path":"a.txt","old_text":"old","new_text":"new","purpose":"replace text"}]}` +
			"\n```",
	}, nil); ok {
		ginkgo.Fail(fmt.Sprintf("submit_plan salvage must stay scoped to plan-submission phases, got %#v", req), 1)
	}
})

var _ = ginkgo.It("SalvageToolRequestFromReasoningRejectsSubmitPlanPlaceholders", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	})
	if req, _, ok := runner.salvageToolRequestFromReasoning(llama.ChatMessage{
		Content: "",
		ReasoningContent: "```tool\n" +
			`{"tool":"submit_plan","rationale":"why these candidate path edits satisfy the objective","edits":[{"path":"candidate/path.go","old_text":"exact current text","new_text":"replacement text","purpose":"one sentence"}]}` +
			"\n```",
	}, nil); ok {
		ginkgo.Fail(fmt.Sprintf("placeholder submit_plan request should not be salvaged: %#v", req), 1)
	}
})

var _ = ginkgo.It("SalvageToolRequestFromReasoningRejectsPlaceholders", func() {
	runner := newRunnerRunFixture(&Runner{})
	if req, _, ok := runner.salvageToolRequestFromReasoning(llama.ChatMessage{
		Content:          "",
		ReasoningContent: "```tool\n{\"tool\":\"edit_file\",\"path\":\"candidate/path.go\",\"old_text\":\"exact current text\",\"new_text\":\"replacement text\"}\n```",
	}, nil); ok {
		ginkgo.Fail(fmt.Sprintf("placeholder request should not be salvaged: %#v", req), 1)
	}
})

var _ = ginkgo.It("SalvageToolRequestFromReasoningRejectsMutatingToolsWithoutRequiredScope", func() {
	runner := newRunnerRunFixture(&Runner{})
	for _, reasoning := range []string{
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"a.txt\",\"old_text\":\"old\",\"new_text\":\"new\"}\n```",
		"```tool\n{\"tool\":\"write_file\",\"path\":\"a.txt\",\"content\":\"new\"}\n```",
		"```tool\n{\"tool\":\"append_file\",\"path\":\"a.txt\",\"content\":\"new\"}\n```",
		"```tool\n{\"tool\":\"apply_patch\",\"content\":\"*** Begin Patch\\n*** Update File: a.txt\\n@@\\n-old\\n+new\\n*** End Patch\\n\"}\n```",
	} {
		if req, _, ok := runner.salvageToolRequestFromReasoning(llama.ChatMessage{
			Content:          "",
			ReasoningContent: reasoning,
		}, nil); ok {
			ginkgo.Fail(fmt.Sprintf("hidden-reasoning mutating request should not be salvaged: %#v", req), 1)
		}
	}
})

var _ = ginkgo.It("CompletionGateRetryLimitAllowsTwoUnscopedNoWriteRecoveries", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	})
	results := []workspace.ToolResult{{Tool: "search_content", Summary: "No content matches"}}

	if got := runner.completionGateRetryLimit(results); got != 2 {
		ginkgo.Fail(fmt.Sprintf("unscoped no-write recovery retry limit = %d, want 2", got), 1)
	}

	if !runner.unscopedNoWriteCompletionRecoveryApplies(results) {
		ginkgo.Fail("expected unscoped no-write recovery to apply", 1)
	}
})

var _ = ginkgo.It("UnscopedNoWriteRecoveryRequiresSourceBodyBeforeWrite", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	})
	results := []workspace.ToolResult{
		{Tool: "context_pack", Summary: "packet", Facts: contextPackCandidateFactsForTest("internal/surfaces/tui/app/banner.go")},
		{Tool: "completion_gate", Summary: "Mutation completion gate rejected final answer", Error: "no successful workspace write"},
	}

	prompt, ok := runner.completionGateRecoveryPrompt(results)
	if !ok {
		ginkgo.Fail("expected unscoped no-write recovery prompt", 1)
	}

	if strings.Contains(prompt, "call one write tool now") {
		ginkgo.Fail(fmt.Sprintf("prompt should not tell the model to write before source-body evidence, got:\n%s", prompt), 1)
	}

	for _, want := range []string{"source-body evidence", "exact replacement shape", "named_gap"} {
		if !strings.Contains(prompt, want) {
			ginkgo.Fail(fmt.Sprintf("expected prompt to contain %q, got:\n%s", want, prompt), 1)
		}
	}
})

var _ = ginkgo.It("CompletionGateRetryLimitAllowsTwoTextualRequiredPathRecoveries", func() {
	runner := newRunnerRunFixture(&Runner{
		Client: llama.NewClient("http://example.invalid", time.Second).WithBackend(config.BackendMLX),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go"},
		},
	})

	if got := runner.completionGateRetryLimit(nil); got != 2 {
		ginkgo.Fail(fmt.Sprintf("textual required-path recovery retry limit = %d, want 2", got), 1)
	}

	if !runner.textualRequiredPathCompletionRecoveryApplies(nil) {
		ginkgo.Fail("expected textual required-path recovery to apply", 1)
	}
})

var _ = ginkgo.It("CompletionGateRecoveryPromptSurvivesStaleGateBeforeLatestWrite", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	})
	_, write := committedMutationResultForTest("internal/surfaces/tui/app/model_swap.go")
	installMutationReceiptForTest(runner, write)
	results := []workspace.ToolResult{
		{Tool: "completion_gate", Summary: "Mutation completion gate rejected final answer", Error: "no successful workspace write"},
		write,
	}

	if got := runner.completionGateRetryLimit(results); got != 1 {
		ginkgo.Fail(fmt.Sprintf("post-write verification recovery retry limit = %d, want 1", got), 1)
	}

	prompt, ok := runner.completionGateRecoveryPrompt(results)
	if !ok {
		ginkgo.Fail("expected post-write verification recovery prompt", 1)
	}

	for _, want := range []string{
		"post-write verification",
		"parser-readable verification action",
		"language-appropriate verification",
		"internal/surfaces/tui/app/model_swap.go",
	} {
		if !strings.Contains(prompt, want) {
			ginkgo.Fail(fmt.Sprintf("expected recovery prompt to contain %q, got:\n%s", want, prompt), 1)
		}
	}
})

var _ = ginkgo.It("CompletionGateRetryLimitIgnoresStaleGateBeforeLaterTurnEvents", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	})
	staleGate := workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Mutation completion gate rejected final answer",
		Error:   "stale completion gate rejection",
	}
	edit := workspace.ToolResult{Tool: "edit_file", Summary: "Edited internal/runtime/agent/turn_decision.go", ChangedPaths: []string{"internal/runtime/agent/turn_decision.go"}}
	tests := []struct {
		name    string
		results []workspace.ToolResult
		control []workspace.ToolResult
	}{
		{
			name: "later model answer",
		},
		{
			name:    "later tool result",
			results: []workspace.ToolResult{staleGate, {Tool: "read_file", Summary: "Read README.md lines 1-5", Output: "1:# Idiom\n"}},
			control: []workspace.ToolResult{{Tool: "read_file", Summary: "Read README.md lines 1-5", Output: "1:# Idiom\n"}},
		},
		{
			name:    "later write",
			results: []workspace.ToolResult{staleGate, edit},
			control: []workspace.ToolResult{edit},
		},
		{
			name: "later verification result",
			results: []workspace.ToolResult{
				edit,
				staleGate,
				{Tool: "source_sanity", Summary: "source_sanity passed for internal/runtime/agent/turn_decision.go"},
			},
			control: []workspace.ToolResult{
				edit,
				{Tool: "source_sanity", Summary: "source_sanity passed for internal/runtime/agent/turn_decision.go"},
			},
		},
		{
			name:    "later permission result",
			results: []workspace.ToolResult{staleGate, {Tool: "permission_decision", Summary: "Permission denied for edit_file"}},
			control: []workspace.ToolResult{{Tool: "permission_decision", Summary: "Permission denied for edit_file"}},
		},
		{
			name:    "later child result",
			results: []workspace.ToolResult{staleGate, {Tool: "task", Summary: "Child task completed", Output: "child result"}},
			control: []workspace.ToolResult{{Tool: "task", Summary: "Child task completed", Output: "child result"}},
		},
	}

	for _, tt := range tests {
		ginkgo.By(tt.name, func() {
			results := tt.results
			control := tt.control
			if results == nil && control == nil {
				results = []workspace.ToolResult{staleGate}
			}
			if got, want := runner.completionGateRetryLimit(results), runner.completionGateRetryLimit(control); got != want {
				ginkgo.Fail(fmt.Sprintf("stale completion_gate changed retry limit for %s: got %d, want %d", tt.name, got, want), 1)
			}
		})
	}
})

var _ = ginkgo.It("CompletionGateRetryLimitCutsOverPlanExecuteSubmitPlanRecovery", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go", "b.go"},
		},
	})
	results := []workspace.ToolResult{
		codeNavigateSourceWindowResult("a.go", 1, 10, 0, "route_summary: Read A in a.go lines 1-10\n\n1:func A() {}\n"),
		codeNavigateSourceWindowResult("b.go", 1, 10, 0, "route_summary: Read B in b.go lines 1-10\n\n1:func B() {}\n"),
	}

	if got := runner.completionGateRetryLimit(results); got != 1 {
		ginkgo.Fail(fmt.Sprintf("plan-execute submit_plan recovery retry limit = %d, want 1", got), 1)
	}
})

var _ = ginkgo.It("CompletionGateRetryLimitAllowsSubmitCorrectionRepeatRecovery", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go", "b.go"},
		},
	})
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
		readFileSourceWindowResult("b.go", 1, 10, 10, "1:package b\n"),
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "missing required edit path(s): `b.go`.",
		},
	}

	if got := runner.completionGateRetryLimit(results); got != 2 {
		ginkgo.Fail(fmt.Sprintf("submit-correction retry limit = %d, want 2", got), 1)
	}

	result := completionGateRejectionWithRecovery(
		"copy may change without changing authority",
		"submit correction remains required",
		newCompletionGateRecoveryEnvelope(completionGateInvariantPlanSubmission, "submit_plan_correction_pending", completionGateViolationPlanSubmission, []string{"b.go"}),
	)
	state := runnerRunState{}
	state.recordCompletionGateRejection(result)
	if !state.completionGateRecovery.ShouldAttachPrompt(1, state.completionGateRecovery.CanRetryActive(2)) {
		ginkgo.Fail("submit-correction non-submit_plan final answer rejection should receive the second recovery prompt", 1)
	}
})

var _ = ginkgo.It("PlanExecuteLoopBudgetRecoveryKeepsUnreadCandidateReadsAvailable", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	})
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote candidates",
			Facts:   contextPackCandidateFactsForTest("a.go", "b.go"),
		},
		readFileSourceWindowResult("a.go", 1, 20, 20, "1:package demo\n"),
	}

	recovery, ok := runner.planThenExecuteLoopBudgetRecoveryPrompt(
		workspace.ToolRequest{Tool: "code_navigate", Path: "a.go", Pattern: "type Foo", Resolution: "definition"},
		results,
		"the model repeated code_navigate with the same arguments 3 turns in a row",
		false,
	)
	if !ok {
		ginkgo.Fail("expected plan-execute loop-budget recovery", 1)
	}

	for _, want := range []string{
		"The planner already has source evidence",
		"permission-authorized inspection requests remain callable",
		"parser-readable `submit_plan`",
	} {
		if !strings.Contains(recovery.Prompt, want) {
			ginkgo.Fail(fmt.Sprintf("expected recovery prompt to contain %q:\n%s", want, recovery.Prompt), 1)
		}
	}
})

var _ = ginkgo.It("PlanExecuteSubmitCorrectionLoopBudgetRecoveryKeepsInspectionCallable", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go", "b.go"},
		},
	})
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
		readFileSourceWindowResult("b.go", 1, 10, 10, "1:package b\n"),
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "missing required edit path(s): `b.go`.",
			Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
				Code: "missing_required_paths", Kind: toolfacts.SubmitPlanDiagnosticMissingRequiredPath, CanonicalID: "b.go",
			}),
		},
	}

	recovery, ok := runner.planThenExecuteLoopBudgetRecoveryPrompt(
		workspace.ToolRequest{Tool: "read_file", Path: "b.go"},
		results,
		"read_file repeated after submit_plan coverage rejection",
		false,
	)
	if !ok {
		ginkgo.Fail("expected submit-correction loop-budget recovery", 1)
	}

	for _, want := range []string{
		"Submit-correction mode remains open",
		"permission-authorized inspection requests remain callable",
		"submit one parser-readable `submit_plan`",
		"required edit path: a.go",
		"required edit path: b.go",
	} {
		if !strings.Contains(recovery.Prompt, want) {
			ginkgo.Fail(fmt.Sprintf("submit-correction loop recovery missing %q:\n%s", want, recovery.Prompt), 1)
		}
	}

	for _, forbidden := range []string{"tools are disabled", "do not call more read", "do not read, search"} {
		if strings.Contains(recovery.Prompt, forbidden) {
			ginkgo.Fail(fmt.Sprintf("submit-correction loop recovery advertised stale exit %q:\n%s", forbidden, recovery.Prompt), 1)
		}
	}
})

var _ = ginkgo.It("PlanExecuteIgnoredLoopBudgetRecoveryKeepsFreshInspectionCallable", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	})
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote candidates",
			Facts:   contextPackCandidateFactsForTest("a.go", "b.go"),
		},
		readFileSourceWindowResult("a.go", 1, 20, 20, "1:package demo\n"),
	}

	recovery, ok := runner.planThenExecuteIgnoredLoopBudgetRecoveryPrompt(
		workspace.ToolRequest{Tool: "code_navigate", Path: "a.go", Pattern: "type Foo", Resolution: "definition"},
		results,
		"the model repeated code_navigate with the same arguments after a loop-budget recovery",
		false,
	)
	if !ok {
		ginkgo.Fail("expected ignored loop-budget recovery", 1)
	}

	for _, want := range []string{
		"repeated code_navigate after a loop-budget recovery",
		"Permission-authorized inspection remains callable",
		"submit_plan",
	} {
		if !strings.Contains(recovery.Prompt, want) {
			ginkgo.Fail(fmt.Sprintf("expected recovery prompt to contain %q:\n%s", want, recovery.Prompt), 1)
		}
	}
})

var _ = ginkgo.It("PlanExecuteSubmitCorrectionIgnoredLoopBudgetRecoveryKeepsInspectionCallable", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go", "b.go"},
		},
	})
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
		readFileSourceWindowResult("b.go", 1, 10, 10, "1:package b\n"),
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "missing required edit path(s): `b.go`.",
			Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
				Code: "missing_required_paths", Kind: toolfacts.SubmitPlanDiagnosticMissingRequiredPath, CanonicalID: "b.go",
			}),
		},
	}

	recovery, ok := runner.planThenExecuteIgnoredLoopBudgetRecoveryPrompt(
		workspace.ToolRequest{Tool: "read_file", Path: "b.go"},
		results,
		"read_file repeated after submit_plan coverage rejection",
		false,
	)
	if !ok {
		ginkgo.Fail("expected ignored submit-correction loop-budget recovery", 1)
	}

	for _, want := range []string{
		"Submit-correction mode is already ready",
		"Permission-authorized inspection remains callable",
		"submit one parser-readable `submit_plan`",
		"required edit path: a.go",
		"required edit path: b.go",
	} {
		if !strings.Contains(recovery.Prompt, want) {
			ginkgo.Fail(fmt.Sprintf("ignored submit-correction loop recovery missing %q:\n%s", want, recovery.Prompt), 1)
		}
	}

	for _, forbidden := range []string{"tools are disabled", "do not call more read", "do not read, search"} {
		if strings.Contains(recovery.Prompt, forbidden) {
			ginkgo.Fail(fmt.Sprintf("ignored submit-correction loop recovery advertised stale exit %q:\n%s", forbidden, recovery.Prompt), 1)
		}
	}
})

var _ = ginkgo.It("CompletionGateRejectsUnresolvedProcessIntent", func() {
	got, rejected := unresolvedProcessIntentCompletionRejection("I'll search the codebase and inspect the TUI next.")
	if !rejected {
		ginkgo.Fail("expected process narration to be rejected", 1)
	}

	if got.Tool != "completion_gate" {
		ginkgo.Fail(fmt.Sprintf("rejection should be attributed to completion gate, got %#v", got), 1)
	}

	if !strings.Contains(got.Error, "Issue the needed tool call now") {
		ginkgo.Fail(fmt.Sprintf("rejection should point at action, got %q", got.Error), 1)
	}

	if _, rejected := unresolvedProcessIntentCompletionRejection("I cannot proceed because the workspace is read-only."); rejected {
		ginkgo.Fail("concrete blocker answers should not be classified as unresolved process intent", 1)
	}
})

var _ = ginkgo.It("FinalAnswerBlockerKindExemptsPlannerPhaseWithSubmitPlan", func() {
	// Post-answer blocker check: in the plan-execute planner phase,
	// successful submit_plan IS the deliverable — the executor phase
	// (run by the orchestrator after this runner exits) produces the
	// actual file changes. Without this exemption the blocker check
	// sees zero changed_paths and overrides the model's final answer
	// with a "required_edit_paths_remaining" narrative, even after the
	// model submitted a correct plan.
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	})
	results := []workspace.ToolResult{
		{Tool: "submit_plan", Summary: "Plan submitted with 2 edit(s); the executor phase will dispatch them."},
	}
	kind, reason := runner.finalAnswerBlockerKind("Plan submitted with 2 edits.", results)

	if kind != "" || reason != "" {
		ginkgo.Fail(fmt.Sprintf("planner phase with successful submit_plan must not trigger the blocker check; got kind=%q reason=%q", kind, reason), 1)
	}
})

var _ = ginkgo.It("FinalAnswerBlockerKindStillRejectsImplementationLaneWithoutEdits", func() {
	// Sanity: the plan-execute exemption must not weaken the post-answer
	// blocker for normal implementation lanes.
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt"},
		},
	})
	kind, reason := runner.finalAnswerBlockerKind("Done.", nil)

	if kind != "required_edit_paths_remaining" || reason != "required_edit_paths_remaining" {
		ginkgo.Fail(fmt.Sprintf("implementation lane with no edits must trigger blocker; got kind=%q reason=%q", kind, reason), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteReadyToSubmitNudgeFiresWhenAllPathsRead", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.txt", 1, 10, 10, "1:a\n"),
		readFileSourceWindowResult("b.txt", 1, 10, 10, "1:b\n"),
	}
	got := planThenExecuteReadyToSubmitNudge(brief, results, planThenExecutePathAuthority{})

	if got == "" {
		ginkgo.Fail("expected nudge once every required path has been read; got empty string", 1)
	}

	state := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})
	if state.Mode != PlanExecutePlanningModeSubmitReady {
		ginkgo.Fail(fmt.Sprintf("state mode = %q, want submit-ready; state=%#v", state.Mode, state), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteSmallWholeFileReadIsSourceReady", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"small.go"},
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("small.go", 1, 24, 24, "1:package small\n2:\n3:func Value() int { return 1 }"),
	}

	if got := planThenExecuteReadyToSubmitNudge(brief, results, planThenExecutePathAuthority{}); got == "" {
		ginkgo.Fail(fmt.Sprintf("small whole-file read should reach readiness, got:\n%s", got), 1)
	}

	if unread := planThenExecuteUnreadRequiredPaths(brief, results, planThenExecutePathAuthority{}); len(unread) != 0 {
		ginkgo.Fail(fmt.Sprintf("small whole-file read should satisfy source readiness, got %#v", unread), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteSymbolBodyReadIsSourceReady", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"internal/runtime/agent/runner_core.go"},
	}
	results := []workspace.ToolResult{
		codeNavigateSourceWindowResult("internal/runtime/agent/runner_core.go", 88, 210, 0, "capability: read_symbol\nlanguage: go\nrouted_tool: go_read_symbol\nroute_summary: Read Runner.Run in internal/runtime/agent/runner_core.go lines 88-210\n\n88:func (r *Runner) Run() {}"),
	}

	if got := planThenExecuteReadyToSubmitNudge(brief, results, planThenExecutePathAuthority{}); got == "" {
		ginkgo.Fail(fmt.Sprintf("read_symbol body should reach readiness, got:\n%s", got), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteOutlineAndSearchDoNotSatisfyRequiredSource", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"pkg/feature.go"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "search_content",
			Summary: "Found matching lines.",
			Output:  "pkg/feature.go:88: func Feature() bool",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed file_outline via go_file_outline",
			Output:  "capability: file_outline\nlanguage: go\nrouted_tool: go_file_outline\nroute_summary: Outlined 2 top-level declaration(s) in pkg/feature.go.",
		},
	}

	if got := planThenExecuteReadyToSubmitNudge(brief, results, planThenExecutePathAuthority{}); got != "" {
		ginkgo.Fail(fmt.Sprintf("outline/search evidence should not reach readiness:\n%s", got), 1)
	}

	if unread := planThenExecuteUnreadRequiredPaths(brief, results, planThenExecutePathAuthority{}); len(unread) != 1 {
		ginkgo.Fail(fmt.Sprintf("required source should remain pending after outline/search, got %#v", unread), 1)
	}
})

var _ = ginkgo.It("PlanExecuteTerminalTextualPruneDropsStaleScaffoldingOnly", func() {
	messages := []llama.ChatMessage{
		{Role: "user", Content: "plan edit"},
		{Role: "system", Content: "[frontier step instruction]\n{\"current_state\":{\"tool_manifest\":[{\"tool\":\"read_file\"}]}}"},
		{Role: "system", Content: "[frontier runtime brief]\n{\"allowed_tools\":[\"read_file\"]}"},
		{Role: "system", Content: "# Textual Tool Call Protocol\n- read_file {\"tool\":\"read_file\",\"path\":\"a.go\"}"},
		{Role: "system", Content: "REQUIRED SOURCE EVIDENCE PENDING: required_edit_paths still lack current source-body evidence.\n\nPending paths and current evidence state:\n- a.go: none\n- b.go: none\n\nPermission-authorized inspection remains callable."},
		{Role: "system", Content: "Note: your `read_file` call was redirected to `code_navigate` by lane routing. The rewritten call will execute and return its result; use that result, not another retry of `read_file`."},
		{Role: "system", Content: "AUTHORITATIVE_CONSTRAINT: preserve this unrelated system guidance."},
		{Role: "system", Content: "Read f1 a.go lines 1-20 (total lines: 20)\n1:package a"},
		{Role: "assistant", Content: `{"tool":"read_file","path":"a.go"}`},
		{Role: "assistant", Content: "I should now submit the plan."},
	}

	pruned, droppedActions, droppedScaffolding := prunePlanThenExecuteTerminalTextualMessages(messages)
	if droppedActions != 2 || droppedScaffolding != 5 {
		ginkgo.Fail(fmt.Sprintf("drops = actions:%d scaffolding:%d, want 2/5; messages=%#v", droppedActions, droppedScaffolding, pruned), 1)
	}

	joined := ""
	for _, msg := range pruned {
		joined += msg.Role + "\n" + msg.Content + "\n"
	}

	if !strings.Contains(joined, "plan edit") || !strings.Contains(joined, "Read f1 a.go") {
		ginkgo.Fail(fmt.Sprintf("terminal prune must preserve user objective and source evidence, got:\n%s", joined), 1)
	}

	if !strings.Contains(joined, "AUTHORITATIVE_CONSTRAINT") {
		ginkgo.Fail(fmt.Sprintf("terminal prune must preserve unrelated system authority, got:\n%s", joined), 1)
	}

	for _, stale := range []string{"[frontier step instruction]", "[frontier runtime brief]", "# Textual Tool Call Protocol", "REQUIRED SOURCE EVIDENCE PENDING", "redirected to `code_navigate`", `"tool":"read_file"`, "I should now submit"} {
		if strings.Contains(joined, stale) {
			ginkgo.Fail(fmt.Sprintf("terminal prune retained stale scaffolding/action %q:\n%s", stale, joined), 1)
		}
	}
})

var _ = ginkgo.It("PlanExecuteTerminalTextualCompactContextPreservesEvidenceOnly", func() {
	messages := []llama.ChatMessage{
		{Role: "system", Content: "GLOBAL TOOL SURFACE\n- read_file {\"tool\":\"read_file\",\"path\":\"a.go\"}\n- code_navigate {\"tool\":\"code_navigate\"}"},
		{Role: "user", Content: "plan edit"},
		{Role: "system", Content: "AUTHORITATIVE_CONSTRAINT: preserve bounded write scope."},
		{Role: "system", Content: "Read f1 a.go lines 1-20 (total lines: 20)\n1:package a\n2:func Value() int { return 1 }"},
		{Role: "assistant", Content: `{"tool":"read_file","path":"a.go"}`},
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 20, 20, "1:package a\n2:func Value() int { return 1 }"),
		readFileSourceWindowResult("unrelated.go", 1, 5, 5, "1:package unrelated"),
	}

	pruned, _, _ := prunePlanThenExecuteTerminalTextualMessages(messages)
	compact, dropped, evidenceMessages := compactPlanThenExecuteTerminalTextualMessages(pruned, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go"},
	}, results)

	if dropped == 0 {
		ginkgo.Fail(fmt.Sprintf("expected compact terminal context to drop broad prior system context, got %#v", compact), 1)
	}

	if evidenceMessages != 1 {
		ginkgo.Fail(fmt.Sprintf("evidenceMessages = %d, want 1; compact=%#v", evidenceMessages, compact), 1)
	}

	joined := ""
	for _, msg := range compact {
		joined += msg.Role + "\n" + msg.Content + "\n"
	}

	for _, want := range []string{
		"plan edit",
		"AUTHORITATIVE_CONSTRAINT: preserve bounded write scope.",
		planThenExecuteTerminalSourceEvidencePrefix,
		"Read a.go lines 1-20",
		"2:func Value() int { return 1 }",
	} {
		if !strings.Contains(joined, want) {
			ginkgo.Fail(fmt.Sprintf("compact terminal context missing %q:\n%s", want, joined), 1)
		}
	}

	for _, stale := range []string{
		"GLOBAL TOOL SURFACE",
		`"tool":"read_file"`,
		`"tool":"code_navigate"`,
		"unrelated.go",
	} {
		if strings.Contains(joined, stale) {
			ginkgo.Fail(fmt.Sprintf("compact terminal context retained stale/unrelated content %q:\n%s", stale, joined), 1)
		}
	}
})

var _ = ginkgo.It("PlanExecuteTerminalNativePrunePreservesToolCallPairs", func() {
	messages := []llama.ChatMessage{
		{Role: "user", Content: "plan edit"},
		{Role: "system", Content: "# Terminal Plan-Execute Native Tool Surface\nstale"},
		{Role: "system", Content: "Read f1 a.go lines 1-20 (total lines: 20)\n1:package a"},
		{Role: "assistant", ToolCalls: []llama.ToolCall{{
			ID:   "call_submit",
			Type: "function",
			Function: llama.ToolCallFunc{
				Name:      "submit_plan",
				Arguments: `{"edits":[]}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call_submit", Name: "submit_plan", Content: "submit_plan rejected: missing required edit path(s): a.go"},
		{Role: "system", Content: "AUTHORITATIVE_CONSTRAINT: preserve this unrelated system guidance."},
	}

	pruned, droppedScaffolding := prunePlanThenExecuteTerminalNativeMessages(messages)
	if droppedScaffolding != 1 {
		ginkgo.Fail(fmt.Sprintf("dropped scaffolding = %d, want 1; messages=%#v", droppedScaffolding, pruned), 1)
	}

	joined := ""
	var assistantCalls, toolResults int
	for _, msg := range pruned {
		if msg.Role == "assistant" && len(msg.ToolCalls) == 1 && msg.ToolCalls[0].ID == "call_submit" {
			assistantCalls++
		}
		if msg.Role == "tool" && msg.ToolCallID == "call_submit" {
			toolResults++
		}

		joined += msg.Role + "\n" + msg.Content + "\n"
	}

	if assistantCalls != 1 || toolResults != 1 {
		ginkgo.Fail(fmt.Sprintf("native terminal prune broke assistant/tool pairing: assistant=%d tool=%d messages=%#v", assistantCalls, toolResults, pruned), 1)
	}

	if !strings.Contains(joined, "submit_plan rejected") || !strings.Contains(joined, "Read f1 a.go") || !strings.Contains(joined, "AUTHORITATIVE_CONSTRAINT") {
		ginkgo.Fail(fmt.Sprintf("native terminal prune should keep paired tool history, source evidence, and unrelated authority, got:\n%s", joined), 1)
	}
})

var _ = ginkgo.It("PlanExecuteTextualSkeletonDefaultsExistingFilesToLineRange", func() {
	prompt := planThenExecuteTextualSubmitPlanSkeleton([]planThenExecuteSubmitPlanSkeletonEntry{{
		Path:      "a.go",
		StartLine: 10,
		EndLine:   12,
	}})

	for _, want := range []string{"required edit path: a.go", "start_line 10", "end_line 12", "complete final replacement"} {
		if !strings.Contains(prompt, want) {
			ginkgo.Fail(fmt.Sprintf("textual skeleton missing %q:\n%s", want, prompt), 1)
		}
	}

	if strings.Contains(prompt, `"insert_after_line":`) {
		ginkgo.Fail(fmt.Sprintf("textual skeleton should not default existing paths to insert_after_line:\n%s", prompt), 1)
	}
})

var _ = ginkgo.It("PlanExecuteTextualTerminalSurfaceIncludesRequiredPathSkeleton", func() {
	runner := newRunnerRunFixture(&Runner{
		Client: llama.NewClient("http://example.invalid", time.Second).WithBackend(config.BackendMLX),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go", "b.go"},
		},
	})
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 20, 20, "1:package a"),
		readFileSourceWindowResult("b.go", 1, 20, 20, "1:package b"),
	}
	messages := []llama.ChatMessage{{Role: "user", Content: "plan edit"}}

	runner.refreshPlanThenExecuteTerminalToolSurface(&messages, results, "test")

	var joinedBuilder strings.Builder
	for _, message := range messages {
		joinedBuilder.WriteString(message.Role)
		joinedBuilder.WriteByte('\n')
		joinedBuilder.WriteString(message.Content)
		joinedBuilder.WriteByte('\n')
	}
	joined := joinedBuilder.String()

	if strings.Count(joined, planThenExecuteCurrentSubmitPlanSkeletonPrefix) != 1 {
		ginkgo.Fail(fmt.Sprintf("textual terminal surface should include exactly one current skeleton:\n%s", joined), 1)
	}

	for _, want := range []string{
		"# Terminal Plan-Execute Textual Tool Surface",
		"submit_plan_called",
		"source_satisfied_or_blocker_only_with_per_path_evidence",
		"Parser-readable submit_plan contract",
		"required edit path: a.go",
		"required edit path: b.go",
		"evidence: Read a.go lines 1-20",
		"insert_after_line",
		"old_text",
		"start_line",
		"start_line 1",
		"end_line 20",
		"`insert_after_line` targets an existing 1-based line in an existing file",
		"The shown source ranges are evidence handles, not proposed edits",
		"Line-range `content` replaces every current source line",
	} {
		if !strings.Contains(joined, want) {
			ginkgo.Fail(fmt.Sprintf("textual terminal surface missing %q:\n%s", want, joined), 1)
		}
	}

	for _, forbidden := range []string{"# Terminal Plan-Execute Native Tool Surface", "Native `submit_plan` tool-argument shape"} {
		if strings.Contains(joined, forbidden) {
			ginkgo.Fail(fmt.Sprintf("textual terminal surface should not ask for %q:\n%s", forbidden, joined), 1)
		}
	}

	for _, forbidden := range []string{"submit_plan_called_or_source_satisfied", "edits_or_source_satisfied_evidence_listed_in_summary"} {
		if strings.Contains(joined, forbidden) {
			ginkgo.Fail(fmt.Sprintf("textual terminal surface should not present source-satisfied as a coequal final critic %q:\n%s", forbidden, joined), 1)
		}
	}
})

var _ = ginkgo.It("PlanExecuteTerminalSurfaceRefreshAllowsSelectedContextPackCandidate", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/test.json with 1 candidate file(s)",
			Facts:   contextPackCandidateFactsForTest("pkg/feature.go"),
		},
		readFileSourceWindowResult("pkg/feature.go", 1, 4, 4, "1:package pkg\n2:\n3:func Hardened() bool { return true }\n"),
	}
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
	messages := []llama.ChatMessage{{Role: "user", Content: "plan feature edit"}}

	runner.refreshPlanThenExecuteTerminalToolSurface(&messages, results, "context_pack_candidate")

	var joinedBuilder strings.Builder
	for _, message := range messages {
		joinedBuilder.WriteString(message.Role)
		joinedBuilder.WriteByte('\n')
		joinedBuilder.WriteString(message.Content)
		joinedBuilder.WriteByte('\n')
	}
	joined := joinedBuilder.String()

	if !strings.Contains(joined, "# Terminal Plan-Execute Textual Tool Surface") &&
		!strings.Contains(joined, "# Terminal Plan-Execute Native Tool Surface") {

		ginkgo.Fail(fmt.Sprintf("selected context-pack candidate should render terminal tool surface:\n%s", joined), 1)
	}

	if !strings.Contains(joined, "required edit path: pkg/feature.go") && !strings.Contains(joined, `path: "pkg/feature.go"`) {
		ginkgo.Fail(fmt.Sprintf("selected context-pack candidate should appear in submit skeleton:\n%s", joined), 1)
	}

	if strings.Contains(joined, "No concrete selected edit path") {
		ginkgo.Fail(fmt.Sprintf("selected context-pack candidate should not render no-path blocker skeleton:\n%s", joined), 1)
	}
})

var _ = ginkgo.It("PlanExecuteTerminalSurfaceRefreshUsesSelectedReadCandidateWithoutRequiringEveryCandidate", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/test.json with 2 candidate file(s)",
			Facts:   contextPackCandidateFactsForTest("pkg/feature.go", "pkg/feature_test.go"),
		},
		readFileSourceWindowResult("pkg/feature.go", 1, 4, 4, "1:package pkg\n2:\n3:func Hardened() bool { return true }\n"),
	}
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
	messages := []llama.ChatMessage{{Role: "user", Content: "plan feature edit"}}

	runner.refreshPlanThenExecuteTerminalToolSurface(&messages, results, "context_pack_candidate")

	var joinedBuilder strings.Builder
	for _, message := range messages {
		joinedBuilder.WriteString(message.Role)
		joinedBuilder.WriteByte('\n')
		joinedBuilder.WriteString(message.Content)
		joinedBuilder.WriteByte('\n')
	}
	joined := joinedBuilder.String()

	if !strings.Contains(joined, "# Terminal Plan-Execute Textual Tool Surface") &&
		!strings.Contains(joined, "# Terminal Plan-Execute Native Tool Surface") {

		ginkgo.Fail(fmt.Sprintf("selected candidate source evidence should render the terminal tool surface:\n%s", joined), 1)
	}
	if !strings.Contains(joined, `path: "pkg/feature.go"`) && !strings.Contains(joined, "required edit path: pkg/feature.go") {
		ginkgo.Fail(fmt.Sprintf("terminal skeleton should use the selected candidate with source evidence:\n%s", joined), 1)
	}
	if strings.Contains(joined, `path: "pkg/feature_test.go"`) || strings.Contains(joined, "required edit path: pkg/feature_test.go") {
		ginkgo.Fail(fmt.Sprintf("unread candidate should not become a required submit-plan path:\n%s", joined), 1)
	}
	if !strings.Contains(joined, "Permission-authorized read/search tools remain callable") {
		ginkgo.Fail(fmt.Sprintf("submit readiness must keep authorized inspection callable:\n%s", joined), 1)
	}
})

var _ = ginkgo.It("PlanExecuteTrueNativeTerminalSurfaceIncludesRequiredPathSkeleton", func() {
	runner := newRunnerRunFixture(&Runner{
		Client: llama.NewClient("http://example.invalid", time.Second).WithBackend(config.BackendLlama),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go"},
		},
	})
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 10, 40, 80, "10:func value() int {"),
	}
	messages := []llama.ChatMessage{{Role: "user", Content: "plan edit"}}

	runner.refreshPlanThenExecuteTerminalToolSurface(&messages, results, "test")

	var joinedBuilder strings.Builder
	for _, message := range messages {
		joinedBuilder.WriteString(message.Role)
		joinedBuilder.WriteByte('\n')
		joinedBuilder.WriteString(message.Content)
		joinedBuilder.WriteByte('\n')
	}
	joined := joinedBuilder.String()

	if !strings.Contains(joined, "# Terminal Plan-Execute Native Tool Surface") ||
		!strings.Contains(joined, "Native `submit_plan` tool-argument shape") ||
		!strings.Contains(joined, `path: "a.go"`) ||
		!strings.Contains(joined, "line_range_default: start_line=10 + end_line=40 + content") ||
		!strings.Contains(joined, "insertion_small_only: insert_after_line + small inline content") ||
		!strings.Contains(joined, "large additions inside existing files") {

		ginkgo.Fail(fmt.Sprintf("true native terminal surface should include native skeleton:\n%s", joined), 1)
	}

	for _, forbidden := range []string{"visible JSON object", "parser-readable JSON", "Decision: source-satisfied", "<<QUESTION>>"} {
		if strings.Contains(joined, forbidden) {
			ginkgo.Fail(fmt.Sprintf("true native terminal surface should not advertise %q:\n%s", forbidden, joined), 1)
		}
	}
})

var _ = ginkgo.It("PlanExecuteTextualFallbackSurfaceIncludesConcreteSubmitPlanJSONSkeleton", func() {
	runner := newRunnerRunFixture(&Runner{
		Client: llama.NewClient("http://example.invalid", time.Second).WithBackend(config.BackendMLX),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go", "b.go"},
		},
	})
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 20, 20, "1:package a"),
		readFileSourceWindowResult("b.go", 1, 20, 20, "1:package b"),
	}
	messages := []llama.ChatMessage{{Role: "user", Content: "plan edit"}}

	runner.refreshPlanThenExecuteTerminalTextualFallbackSurface(&messages, results, "test")

	var joinedBuilder strings.Builder
	for _, message := range messages {
		joinedBuilder.WriteString(message.Role)
		joinedBuilder.WriteByte('\n')
		joinedBuilder.WriteString(message.Content)
		joinedBuilder.WriteByte('\n')
	}
	joined := joinedBuilder.String()

	if strings.Count(joined, planThenExecuteCurrentSubmitPlanSkeletonPrefix) != 1 {
		ginkgo.Fail(fmt.Sprintf("textual fallback should include exactly one current skeleton:\n%s", joined), 1)
	}

	for _, want := range []string{
		"# Terminal Plan-Execute Textual Tool Surface",
		"Parser-readable submit_plan contract",
		"top-level `tool:\"submit_plan\"`",
		"required edit path: a.go",
		"required edit path: b.go",
		"evidence: Read a.go lines 1-20",
		"start_line 1",
		"end_line 20",
		"complete final replacement",
		"Edit shape choices",
		"use `insert_after_line` + small inline `content` only for small insertions/appends after an existing line",
		"large additions anchored to existing lines",
		"`replace_all:true` + empty `old_text` + full `new_text`",
		"`insert_after_line` targets an existing 1-based line in an existing file",
		"The shown source ranges are evidence handles, not proposed edits",
		"Line-range `content` replaces every current source line",
	} {
		if !strings.Contains(joined, want) {
			ginkgo.Fail(fmt.Sprintf("textual fallback surface missing %q:\n%s", want, joined), 1)
		}
	}

	if strings.Contains(joined, `"insert_after_line":20`) {
		ginkgo.Fail(fmt.Sprintf("textual fallback surface should not default existing paths to insert_after_line:\n%s", joined), 1)
	}
})

var _ = ginkgo.It("PlanExecuteTerminalSkeletonCapsEvidenceWhileListingAllRequiredPaths", func() {
	paths := make([]string, 0, planThenExecuteSubmitPlanSkeletonMaxEvidenceLines+4)
	results := make([]workspace.ToolResult, 0, planThenExecuteSubmitPlanSkeletonMaxEvidenceLines+4)

	for i := 0; i < planThenExecuteSubmitPlanSkeletonMaxEvidenceLines+4; i++ {
		path := fmt.Sprintf("file%d.go", i)

		paths = append(paths, path)
		results = append(results, readFileSourceWindowResult(path, 1, 10, 10, "1:package p"))
	}

	skeleton := planThenExecuteTerminalSubmitPlanSkeleton(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: paths,
	}, results, true, planThenExecutePathAuthority{})

	for _, path := range paths {
		if !strings.Contains(skeleton, strconv.Quote(path)) {
			ginkgo.Fail(fmt.Sprintf("skeleton must still list required path %s:\n%s", path, skeleton), 1)
		}
	}

	if got := strings.Count(skeleton, "  evidence: "); got != planThenExecuteSubmitPlanSkeletonMaxEvidenceLines {
		ginkgo.Fail(fmt.Sprintf("evidence details should be globally capped, got %d:\n%s", got, skeleton), 1)
	}

	if !strings.Contains(skeleton, "Additional evidence handles were omitted after the template evidence cap") {
		ginkgo.Fail(fmt.Sprintf("capped skeleton should disclose omitted evidence handles:\n%s", skeleton), 1)
	}
})

var _ = ginkgo.It("PlanExecutePostRejectionSurfaceKeepsOneSkeletonAndRejectedDraftEvidence", func() {
	runner := newRunnerRunFixture(&Runner{
		Client: llama.NewClient("http://example.invalid", time.Second).WithBackend(config.BackendMLX),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go", "b.go"},
		},
	})
	req := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "a.go", StartLine: 1, EndLine: 3, Content: "package a\n\nfunc Value() int { return 2 }\n", Purpose: "change returned value"},
			{Path: "b.go", OldText: "return 1", NewText: "return 2", Purpose: "change returned value"},
		},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "invalid edit anchor(s): `b.go: old_text not found in current source`.",
		Code:    workspace.SubmitPlanInvalidAnchorCode,
		Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
			Code:        workspace.SubmitPlanInvalidAnchorCode,
			Kind:        toolfacts.SubmitPlanDiagnosticInvalidAnchor,
			CanonicalID: "b.go",
		}),
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 20, 20, "1:package a"),
		readFileSourceWindowResult("b.go", 1, 20, 20, "1:package b"),
		result,
		codeNavigateSourceWindowResult("b.go", 1, 20, 20, "route_summary: Read Target in b.go lines 1-20\n1:package b"),
	}
	messages := []llama.ChatMessage{
		{Role: "system", Content: "# Terminal Plan-Execute Native Tool Surface\n" + planThenExecuteNativeSubmitPlanSkeleton([]planThenExecuteSubmitPlanSkeletonEntry{{Path: "stale.go"}})},
		{Role: "system", Content: planThenExecuteRejectedSubmitPlanDraftEvidencePrefix + "\nstale draft"},
	}

	replacePlanThenExecuteRejectedSubmitPlanDraftEvidence(&messages, req, result)
	runner.refreshPlanThenExecuteTerminalToolSurface(&messages, results, "submit_plan_rejected")

	var joinedBuilder strings.Builder
	for _, message := range messages {
		joinedBuilder.WriteString(message.Role)
		joinedBuilder.WriteByte('\n')
		joinedBuilder.WriteString(message.Content)
		joinedBuilder.WriteByte('\n')
	}
	joined := joinedBuilder.String()

	if strings.Count(joined, planThenExecuteCurrentSubmitPlanSkeletonPrefix) != 1 {
		ginkgo.Fail(fmt.Sprintf("post-rejection surface should have exactly one current skeleton:\n%s", joined), 1)
	}

	if strings.Count(joined, planThenExecuteRejectedSubmitPlanDraftEvidencePrefix) != 1 {
		ginkgo.Fail(fmt.Sprintf("post-rejection surface should preserve exactly one rejected draft evidence block:\n%s", joined), 1)
	}

	for _, want := range []string{"required edit path: b.go", "path=a.go lines=1-3", "return 2", "old_text not found"} {
		if !strings.Contains(joined, want) {
			ginkgo.Fail(fmt.Sprintf("post-rejection surface missing %q:\n%s", want, joined), 1)
		}
	}

	if !strings.Contains(joined, "required edit path: a.go") {
		ginkgo.Fail(fmt.Sprintf("post-rejection correction skeleton must preserve required-path coverage:\n%s", joined), 1)
	}

	if strings.Contains(joined, "stale.go") || strings.Contains(joined, "stale draft") {
		ginkgo.Fail(fmt.Sprintf("post-rejection surface retained stale skeleton/evidence:\n%s", joined), 1)
	}
})

var _ = ginkgo.It("PlanExecuteRejectedSubmitPlanDraftEvidencePreservesFailingBody", func() {
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[0]: preflight syntax validation failed for a.go at line 4: ./a.go:4:2: expected declaration, found '}'",
		Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
			Code: workspace.SubmitPlanSyntaxValidationCode, Kind: toolfacts.SubmitPlanDiagnosticSyntaxValidation, CanonicalID: "a.go", StartLine: 4, EndLine: 4,
		}),
	}
	prompt := planThenExecuteRejectedSubmitPlanDraftEvidence(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{
				Path:      "a.go",
				StartLine: 1,
				EndLine:   5,
				Content:   "package a\n\nfunc Value() int {\n\treturn 2\n}\n}\n",
				Purpose:   "change returned value",
			},
			{
				Path:    "b.go",
				OldText: "return 1",
				NewText: "return 2",
				Purpose: "follow-up edit",
			},
		},
	}, result)

	for _, want := range []string{
		planThenExecuteRejectedSubmitPlanDraftEvidencePrefix,
		"path=a.go",
		"status=invalid_anchor_or_syntax",
		"rejected_body_excerpt_not_source_evidence",
		"not source evidence",
		"func Value() int",
		"path=b.go",
	} {
		if !strings.Contains(prompt, want) {
			ginkgo.Fail(fmt.Sprintf("rejected draft evidence missing %q:\n%s", want, prompt), 1)
		}
	}

	messages := []llama.ChatMessage{
		{Role: "system", Content: prompt},
		{Role: "system", Content: "# Terminal Plan-Execute Native Tool Surface\nstale"},
		{Role: "assistant", ToolCalls: []llama.ToolCall{{ID: "call_1", Type: "function", Function: llama.ToolCallFunc{Name: "submit_plan", Arguments: "{}"}}}},
		{Role: "tool", ToolCallID: "call_1", Name: "submit_plan", Content: "submit_plan rejected"},
	}
	pruned, droppedActions, droppedScaffolding := prunePlanThenExecuteTerminalTextualMessages(messages)

	if droppedActions != 1 || droppedScaffolding != 1 {
		ginkgo.Fail(fmt.Sprintf("drops = actions:%d scaffolding:%d, want 1/1 (tool messages are preserved)", droppedActions, droppedScaffolding), 1)
	}

	if len(pruned) < 2 || !strings.Contains(pruned[0].Content, "func Value() int") {
		ginkgo.Fail(fmt.Sprintf("terminal prune should keep tool messages and preserve rejected draft evidence, got %#v (%d messages)", pruned, len(pruned)), 1)
	}
})

var _ = ginkgo.It("PlanExecuteRejectedSubmitPlanDraftEvidenceSanitizesNoopGuidance", func() {
	prompt := planThenExecuteRejectedSubmitPlanDraftEvidence(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{{
			Path:    "b.txt",
			OldText: "before\n",
			NewText: "before\n",
			Purpose: "no-op edit",
		}},
	}, workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[0]: edit would not change b.txt; answer `Decision: source-satisfied`, use `source_satisfied_paths` for mixed coverage, or submit real edits",
	})

	if strings.Contains(prompt, "Decision: source-satisfied") {
		ginkgo.Fail(fmt.Sprintf("rejected draft evidence should not replay terminal source-satisfied in submit-correction stack:\n%s", prompt), 1)
	}

	if !strings.Contains(prompt, "source_satisfied_paths") || !strings.Contains(prompt, "submit real edits") {
		ginkgo.Fail(fmt.Sprintf("rejected draft evidence should preserve corrected submit_plan alternatives:\n%s", prompt), 1)
	}
})

var _ = ginkgo.It("PlanExecuteSubmitPlanRejectionBriefPreservesTailGuidance", func() {
	longPrefix := strings.Repeat("planned content around syntax error ", 30)
	brief := lastSubmitPlanRejectionBrief(workspace.ToolResult{
		Tool: "submit_plan",
		Error: longPrefix + "Line-range edit detail: submit_plan edit for a.go declared start_line=10 and end_line=20. " +
			"The `content` replaces exactly that inclusive current-source range; if the replacement includes source that currently lives after end_line, extend end_line to cover it.",
	})

	for _, want := range []string{"Last submit_plan rejection", "Line-range edit detail", "extend end_line"} {
		if !strings.Contains(brief, want) {
			ginkgo.Fail(fmt.Sprintf("brief should preserve actionable tail guidance %q:\n%s", want, brief), 1)
		}
	}

	if len(brief) > 820 {
		ginkgo.Fail(fmt.Sprintf("brief should remain bounded, got %d chars:\n%s", len(brief), brief), 1)
	}
})

var _ = ginkgo.It("PlanExecuteSubmitCorrectionRejectionBriefRemovesTerminalSourceSatisfied", func() {
	brief := lastSubmitPlanCorrectionRejectionBrief(workspace.ToolResult{
		Tool:  "submit_plan",
		Error: "edits[1]: edit would not change b.txt; answer `Decision: source-satisfied`, use `source_satisfied_paths` for mixed coverage, or submit real edits",
	})

	if strings.Contains(brief, "Decision: source-satisfied") {
		ginkgo.Fail(fmt.Sprintf("submit-correction brief must not advertise terminal source-satisfied:\n%s", brief), 1)
	}

	if !strings.Contains(brief, "source_satisfied_paths") || !strings.Contains(brief, "submit real edits") {
		ginkgo.Fail(fmt.Sprintf("submit-correction brief should preserve submit_plan alternatives:\n%s", brief), 1)
	}
})

var _ = ginkgo.It("PlanExecuteSubmitCorrectionInvalidShapeSeparatesSourceSatisfiedCoverage", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "plan_artifact.schema_errors edits[1]:invalid_edit_shape:requires one executable edit shape: old_text with new_text. If this path is already at the requested final state, remove it from edits[] and put the path in top-level source_satisfied_paths instead of using an edit object",
	}

	instruction := planThenExecuteSubmitPlanCorrectionInstructionForBrief(brief, result)
	for _, want := range []string{
		"put paths needing changes in `edits[]`",
		"top-level `source_satisfied_paths`",
		"never create an `edits[]` object whose only purpose is source-satisfied coverage",
		"must-have-valid-edit-or-source_satisfied_paths-entry",
	} {
		if !strings.Contains(instruction, want) {
			ginkgo.Fail(fmt.Sprintf("invalid-shape correction instruction missing %q:\n%s", want, instruction), 1)
		}
	}

	if strings.Contains(instruction, "Decision: source-satisfied") {
		ginkgo.Fail(fmt.Sprintf("submit-correction invalid-shape instruction must not advertise terminal source-satisfied:\n%s", instruction), 1)
	}
})

var _ = ginkgo.It("PlanExecuteSubmitPlanSkeletonExplainsSourceSatisfiedPathsAreTopLevel", func() {
	skeleton := planThenExecuteTextualSubmitPlanSkeleton([]planThenExecuteSubmitPlanSkeletonEntry{{
		Path:      "a.txt",
		StartLine: 1,
		EndLine:   3,
		Evidence:  []string{"Read f1 a.txt lines 1-3 (total lines: 3)"},
	}})
	for _, want := range []string{
		"top-level `source_satisfied_paths`",
		"never create an `edits[]` object whose only purpose is source-satisfied coverage",
		"top-level `tool:\"submit_plan\"`, `rationale`, and an `edits` array",
	} {
		if !strings.Contains(skeleton, want) {
			ginkgo.Fail(fmt.Sprintf("submit_plan skeleton missing %q:\n%s", want, skeleton), 1)
		}
	}
})

var _ = ginkgo.It("PlanExecuteNativeNoToolCallFallbackSwitchesToTextualSubmitPlan", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go", "b.go"},
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 20, 20, "1:package a\n"),
		readFileSourceWindowResult("b.go", 1, 20, 20, "1:package b\n"),
	}
	runner := newRunnerRunFixture(&Runner{
		Client:       llama.NewClient("http://127.0.0.1:1", time.Second).WithBackend(config.BackendMLX),
		RuntimeBrief: brief,
	})
	prompt, ok := runner.planThenExecuteNativeSubmitPlanNoCallFallbackPrompt(
		errors.New(`chat request failed: {"detail":"tool_choice='required' was set but the model did not produce any tool calls."}`),
		llama.ChatOptions{ForceNativeTools: true, ToolChoice: "required"},
		results,
		0,
	)

	if !ok {
		ginkgo.Fail("expected native required no-tool-call error to activate textual fallback", 1)
	}

	for _, want := range []string{"textual tool parser is active", "Emit exactly one visible JSON object", "a.go, b.go"} {
		if !strings.Contains(prompt, want) {
			ginkgo.Fail(fmt.Sprintf("fallback prompt missing %q:\n%s", want, prompt), 1)
		}
	}

	opts := planThenExecuteTextualSubmitPlanFallbackChatOptions(llama.ChatOptions{
		ForceNativeTools: true,
		Tools:            []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice:       "required",
	}, brief, results)
	if opts.ForceNativeTools || len(opts.Tools) != 0 || opts.ToolChoice != "" {
		ginkgo.Fail(fmt.Sprintf("textual fallback should disable native tools, got force=%v tools=%#v choice=%q", opts.ForceNativeTools, opts.Tools, opts.ToolChoice), 1)
	}

	if opts.EnableThinking == nil || *opts.EnableThinking {
		ginkgo.Fail(fmt.Sprintf("textual fallback should force visible output, got %#v", opts.EnableThinking), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteUnreadRequiredSourceChatOptionsCapsNextCall", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go", "b.go"},
	}
	results := []workspace.ToolResult{
		codeNavigateSourceWindowResult("a.go", 1, 10, 0, "route_summary: Read A in a.go lines 1-10\n\n1:func A() {}\n"),
		{Tool: "code_navigate", Summary: "Code navigation routed file_outline via go_file_outline", Paths: []string{"b.go"}, Output: "route_summary: Outlined 2 top-level declaration(s) in b.go.\n"},
	}
	thinkingOn := true
	opts := planThenExecuteUnreadRequiredSourceChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        16384,
	}, brief, results, planThenExecutePathAuthority{})

	if opts.EnableThinking == nil || *opts.EnableThinking {
		ginkgo.Fail("unread-source nudge should force thinking off for the next short tool call", 1)
	}

	if opts.PreserveThinking {
		ginkgo.Fail("unread-source nudge should not preserve prior hidden reasoning", 1)
	}

	if opts.MaxTokens != 768 {
		ginkgo.Fail(fmt.Sprintf("MaxTokens = %d, want 768", opts.MaxTokens), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteReadyToSubmitNudgeWaitsForFullCoverage", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt", "b.txt", "c.txt"},
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.txt", 1, 10, 10, "1:a\n"),
		readFileSourceWindowResult("b.txt", 1, 10, 10, "1:b\n"),
		// c.txt has not been read — the nudge must wait.
	}

	if got := planThenExecuteReadyToSubmitNudge(brief, results, planThenExecutePathAuthority{}); got != "" {
		ginkgo.Fail(fmt.Sprintf("nudge must NOT fire while a required path is unread; got %q", got), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteReadyToSubmitNudgeFiresForCandidateScopeRead", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/test.json with 1 candidate file(s)",
			Facts:   contextPackCandidateFactsForTest("internal/surfaces/tui/app/rendering.go"),
		},
		readFileSourceWindowResult("internal/surfaces/tui/app/rendering.go", 1, 120, 120, "1:package app\n"),
	}

	got := planThenExecuteReadyToSubmitNudge(brief, results, planThenExecutePathAuthority{})
	if got == "" {
		ginkgo.Fail("expected candidate-scope readiness nudge after a read", 1)
	}

	state := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})
	if !slices.Contains(state.SelectedEditPaths, "internal/surfaces/tui/app/rendering.go") {
		ginkgo.Fail(fmt.Sprintf("selected edit paths = %#v, want read candidate path", state.SelectedEditPaths), 1)
	}
	if !planThenExecuteTerminalSubmitPlanAllowed(brief, results, planThenExecutePathAuthority{}) {
		ginkgo.Fail("candidate-scope read should make submit_plan available", 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteReadyToSubmitNudgeDoesNotRequireUnreadUnscopedCandidates", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/test.json with 2 candidate file(s)",
			Facts: contextPackCandidateFactsForTest(
				"internal/workflows/productworkflow/coverage_gate.go",
				"internal/workflows/productworkflow/coverage_gate_test.go",
			),
		},
		codeNavigateSourceWindowResult("internal/workflows/productworkflow/coverage_gate.go", 20, 80, 0, "route_summary: Read EvaluateCoverage in internal/workflows/productworkflow/coverage_gate.go lines 20-80\n\n20:func EvaluateCoverage() {}\n"),
	}

	got := planThenExecuteReadyToSubmitNudge(brief, results, planThenExecutePathAuthority{})
	if got == "" {
		ginkgo.Fail("unscoped context candidates should receive submit-ready guidance after a selected source-body read", 1)
	}

	for _, want := range []string{
		"coverage_gate.go",
		"Allowed/candidate paths are not coverage obligations",
		"One bounded source-read/search for a different candidate path",
		"submit_plan",
	} {
		if !strings.Contains(got, want) {
			ginkgo.Fail(fmt.Sprintf("candidate readiness nudge missing %q:\n%s", want, got), 1)
		}
	}
})

var _ = ginkgo.It("PlanThenExecuteReadyToSubmitNudgeIgnoresUnselectedEvidenceOnlyRead", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 internal/surfaces/tui/app/rendering.go lines 1-120"},
	}

	if got := planThenExecuteReadyToSubmitNudge(brief, results, planThenExecutePathAuthority{}); got != "" {
		ginkgo.Fail(fmt.Sprintf("evidence-only unscoped reads must not receive submit-ready nudge without selected candidate path, got %q", got), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteReadyToSubmitNudgeDoesNotRequireAllAllowedCandidates", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths:  []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go", "internal/workflows/productworkflow/coverage_gate.go"},
		RequiredEditPaths: nil,
	}
	results := []workspace.ToolResult{
		codeNavigateSourceWindowResult(
			"cmd/idiom/product_workflow_test.go",
			3636,
			3678,
			0,
			"route_summary: Read validTestImplementationLedgerJSONL in cmd/idiom/product_workflow_test.go lines 3636-3678\n\n3636:func validTestImplementationLedgerJSONL() string {\n3637:\treturn `{}`\n3638:}\n",
		),
	}

	got := planThenExecuteReadyToSubmitNudge(brief, results, planThenExecutePathAuthority{})
	if got == "" {
		ginkgo.Fail("candidate-only allowed scope must become submit-ready after one anchor-ready candidate read", 1)
	}

	if !strings.Contains(got, "cmd/idiom/product_workflow_test.go") || !strings.Contains(got, "Allowed/candidate paths are not coverage obligations") {
		ginkgo.Fail(fmt.Sprintf("nudge should cite the read path and not promote allowed paths to required coverage:\n%s", got), 1)
	}

	if strings.Contains(got, "cmd/idiom/product_workflow.go") || strings.Contains(got, "internal/workflows/productworkflow/coverage_gate.go") {
		ginkgo.Fail(fmt.Sprintf("nudge should not demand unread allowed candidates as coverage obligations:\n%s", got), 1)
	}
})

var _ = ginkgo.It("PlanExecutePlanningStateKeepsCandidatesOutOfRequiredCoverage", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"pkg/feature.go", "pkg/feature_test.go"},
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("pkg/feature.go", 1, 40, 40, "1:package pkg\n"),
	}

	state := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})
	if state.Mode != PlanExecutePlanningModeSubmitReady {
		ginkgo.Fail(fmt.Sprintf("mode = %q, want submit_ready after one selected allowed candidate has source evidence", state.Mode), 1)
	}

	if len(state.RequiredCoveragePaths) != 0 {
		ginkgo.Fail(fmt.Sprintf("allowed candidates must not become required coverage: %#v", state.RequiredCoveragePaths), 1)
	}

	if !slices.Equal(state.SelectedEditPaths, []string{"pkg/feature.go"}) {
		ginkgo.Fail(fmt.Sprintf("selected edit paths should come from source evidence, got %#v", state.SelectedEditPaths), 1)
	}

	if !slices.Contains(state.TerminalOptions, "submit_plan") {
		ginkgo.Fail(fmt.Sprintf("terminal options should include submit_plan once selected source evidence exists: %#v", state.TerminalOptions), 1)
	}
})

var _ = ginkgo.It("PlanExecutePlanningStateDoesNotPromoteEvidenceOnlyPaths", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("internal/workflows/productworkflow/types.go", 1, 40, 40, "1:package productworkflow\n"),
	}

	state := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})
	if len(state.RequiredCoveragePaths) != 0 {
		ginkgo.Fail(fmt.Sprintf("unexpected required coverage paths: %#v", state.RequiredCoveragePaths), 1)
	}

	if len(state.SelectedEditPaths) != 0 {
		ginkgo.Fail(fmt.Sprintf("evidence-only paths must not become selected edit paths: %#v", state.SelectedEditPaths), 1)
	}

	if slices.Contains(state.TerminalOptions, "submit_plan") {
		ginkgo.Fail(fmt.Sprintf("submit_plan must not be advertised without concrete selected edit paths: %#v", state.TerminalOptions), 1)
	}
})

var _ = ginkgo.It("PlanExecutePlanningStateDoesNotPromoteEvidenceOnlyPathViaContextPack", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "context pack",
			Facts:   contextPackCandidateFactsForTest("pkg/feature.go"),
		},
		readFileSourceWindowResult("pkg/other.go", 1, 40, 40, "1:package pkg\n"),
	}

	state := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})
	if len(state.SelectedEditPaths) != 0 {
		ginkgo.Fail(fmt.Sprintf("context-pack candidates must not make unrelated evidence-only paths selected: %#v", state.SelectedEditPaths), 1)
	}

	if slices.Contains(state.TerminalOptions, "submit_plan") {
		ginkgo.Fail(fmt.Sprintf("submit_plan must not be advertised without a selected edit path: %#v", state.TerminalOptions), 1)
	}

	skeleton := planThenExecuteTerminalSubmitPlanSkeleton(brief, results, false, planThenExecutePathAuthority{})
	if strings.Contains(skeleton, "pkg/feature.go") || strings.Contains(skeleton, "pkg/other.go") {
		ginkgo.Fail(fmt.Sprintf("skeleton must not promote context-pack or evidence-only paths:\n%s", skeleton), 1)
	}

	if !strings.Contains(skeleton, "No concrete selected edit path") {
		ginkgo.Fail(fmt.Sprintf("skeleton should report no safe selected path:\n%s", skeleton), 1)
	}
})

var _ = ginkgo.It("PlanExecutePlanningStateSelectsContextPackCandidateWithSourceEvidence", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "context pack",
			Facts:   contextPackCandidateFactsForTest("pkg/feature.go"),
		},
		readFileSourceWindowResult("pkg/feature.go", 1, 40, 40, "1:package pkg\n"),
	}

	state := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})
	if !slices.Equal(state.SelectedEditPaths, []string{"pkg/feature.go"}) {
		ginkgo.Fail(fmt.Sprintf("selected edit paths should be context-pack candidates with source evidence, got %#v", state.SelectedEditPaths), 1)
	}

	if !slices.Contains(state.TerminalOptions, "submit_plan") {
		ginkgo.Fail(fmt.Sprintf("submit_plan should be available once a typed candidate has source evidence: %#v", state.TerminalOptions), 1)
	}

	if !planThenExecuteTerminalSubmitPlanAllowed(brief, results, planThenExecutePathAuthority{}) {
		ginkgo.Fail("typed context-pack candidate with source evidence should allow terminal submit_plan surface", 1)
	}
})

var _ = ginkgo.It("PlanExecuteSourceSatisfiedSubmitPlanRejectionDoesNotEnterSubmitCorrection", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go", "b.go"},
	}
	manager, sourceResults := sourceWindowManagerResultsForTest("a.go", "b.go")
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief, Workspace: manager})
	results := append(sourceResults, workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: source-satisfied terminal shape required",
		Error:   "submit_plan cannot carry a source-satisfied/no-op closeout. Answer `Decision: source-satisfied` with per-path current-state evidence, submit real edits, or answer <<QUESTION>> with the exact blocker.",
		Code:    workspace.SubmitPlanSourceSatisfiedTerminalRequiredCode,
	})
	answer := "Decision: source-satisfied\n- a.go: requested final state is already present.\n- b.go: requested final state is already present."

	if planThenExecuteSubmitCorrectionActive(brief, results, runner.planThenExecutePathAuthority()) {
		ginkgo.Fail("source-satisfied terminal-shape rejection should not enter submit-correction mode", 1)
	}

	if _, ok := runner.planThenExecuteAcceptedSourceSatisfiedPaths(answer, results); !ok {
		ginkgo.Fail("explicit source-satisfied answer should remain available after typed terminal-shape rejection", 1)
	}

	afterNoopRejection := append([]workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
		readFileSourceWindowResult("b.go", 1, 10, 10, "1:package b\n"),
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[0]: edit would not change a.go; submit real edits, or answer `Decision: source-satisfied` with current-state evidence when no changes are needed",
		},
	}, results[2])
	state := derivePlanExecutePlanningState(brief, afterNoopRejection, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})

	if state.Mode != PlanExecutePlanningModeSubmitReady {
		ginkgo.Fail(fmt.Sprintf("mode after source-satisfied rejection following no-op = %q, want submit_ready", state.Mode), 1)
	}

	if planThenExecuteSubmitCorrectionActive(brief, afterNoopRejection, runner.planThenExecutePathAuthority()) {
		ginkgo.Fail("source-satisfied terminal-shape rejection after no-op should exit submit-correction mode", 1)
	}

	if !slices.Contains(state.TerminalResponseShapes, "Decision: source-satisfied") {
		ginkgo.Fail(fmt.Sprintf("source-satisfied answer shape should be restored: %#v", state.TerminalResponseShapes), 1)
	}
})

var _ = ginkgo.It("PlanExecuteSourceSatisfiedRationaleRejectionDoesNotEnterSubmitCorrection", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go", "b.go"},
	}
	manager, sourceResults := sourceWindowManagerResultsForTest("a.go", "b.go")
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief, Workspace: manager})
	results := append(sourceResults, workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: source-satisfied rationale",
		Error:   "submit_plan rationale says the requested final state is already present or that edits are placeholders. Do not submit verification/placeholder edits; answer `Decision: source-satisfied` with per-path current-state evidence, or answer <<QUESTION>> with the exact blocker.",
		Code:    workspace.SubmitPlanSourceSatisfiedTerminalRequiredCode,
	})
	answer := "Decision: source-satisfied\n- a.go: requested final state is already present.\n- b.go: requested final state is already present."

	state := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})
	if state.Mode != PlanExecutePlanningModeSubmitReady {
		ginkgo.Fail(fmt.Sprintf("mode = %q, want submit_ready", state.Mode), 1)
	}

	if planThenExecuteSubmitCorrectionActive(brief, results, runner.planThenExecutePathAuthority()) {
		ginkgo.Fail("source-satisfied rationale rejection should not enter submit-correction mode", 1)
	}

	if !slices.Contains(state.TerminalResponseShapes, "Decision: source-satisfied") {
		ginkgo.Fail(fmt.Sprintf("source-satisfied answer shape should remain available: %#v", state.TerminalResponseShapes), 1)
	}

	if _, ok := runner.planThenExecuteAcceptedSourceSatisfiedPaths(answer, results); !ok {
		ginkgo.Fail("explicit source-satisfied answer should remain available after source-satisfied rationale rejection", 1)
	}
})

var _ = ginkgo.It("PlanExecuteSourceSatisfiedRejectsStaleSourceWindow", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go"},
	}
	manager, results := sourceWindowManagerResultsForTest("a.go")
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief, Workspace: manager})
	if err := os.WriteFile(filepath.Join(manager.Root(), "a.go"), []byte("package fixture\n\nconst changed = true\n"), 0o600); err != nil {
		ginkgo.Fail(fmt.Sprintf("mutate source-window fixture: %v", err), 1)
	}

	answer := "Decision: source-satisfied\n- a.go: requested final state is already present."
	if _, ok := runner.planThenExecuteAcceptedSourceSatisfiedPaths(answer, results); ok {
		ginkgo.Fail("source-satisfied closeout must reject a source window whose current revision digest changed")
	}
})

var _ = ginkgo.It("PlanExecuteSubmitCorrectionTerminalBriefIsSubmitPlanOnly", func() {
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go", "b.go"},
	}})
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
		readFileSourceWindowResult("b.go", 1, 10, 10, "1:package b\n"),
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; missing required edit path(s): `b.go`.",
		},
	}

	brief := runner.planThenExecuteTerminalToolBrief(results)
	if !slices.Contains(brief.ReadinessGate, "submit_plan_correction_required") {
		ginkgo.Fail(fmt.Sprintf("submit-correction terminal brief missing strict readiness: %#v", brief.ReadinessGate), 1)
	}

	joinedNext := strings.Join(brief.RequiredNext, "\n")
	if !strings.Contains(joinedNext, "corrected submit_plan") {
		ginkgo.Fail(fmt.Sprintf("submit-correction terminal brief missing strict next action: %#v", brief.RequiredNext), 1)
	}

	for _, forbidden := range []string{"submit_plan_or_source_satisfied_or_blocker_reported", "source_satisfied_or_blocker_only_with_per_path_evidence"} {
		if slices.Contains(brief.ReadinessGate, forbidden) || slices.Contains(brief.FinalCritic, forbidden) {
			ginkgo.Fail(fmt.Sprintf("submit-correction terminal brief advertised stale exit %q: readiness=%#v final=%#v", forbidden, brief.ReadinessGate, brief.FinalCritic), 1)
		}
	}

	for _, forbidden := range []string{"Decision: source-satisfied with per-path evidence", "submit_plan covering every required_edit_path with real edits or source_satisfied_paths entries, Decision: source-satisfied"} {
		if strings.Contains(joinedNext, forbidden) || strings.Contains(strings.Join(brief.InvalidActions, "\n"), "final answer before submit_plan succeeds unless it is a source-satisfied closeout") {
			ginkgo.Fail(fmt.Sprintf("submit-correction terminal brief retained stale next/invalid guidance %q: next=%#v invalid=%#v", forbidden, brief.RequiredNext, brief.InvalidActions), 1)
		}
	}
})

var _ = ginkgo.It("PlanExecuteSubmitCorrectionChecklistNamesNoopSourceSatisfiedPaths", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[1]: edit would not change b.txt; answer `Decision: source-satisfied`, use `source_satisfied_paths` for mixed coverage, or submit real edits",
		Code:    workspace.SubmitPlanNoOpEditCode,
		Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
			Code:        workspace.SubmitPlanNoOpEditCode,
			Kind:        toolfacts.SubmitPlanDiagnosticNoOpEdit,
			CanonicalID: "b.txt",
		}),
	}

	checklist := planThenExecuteSubmitPlanCorrectionChecklist(brief, result)
	if !strings.Contains(checklist, "a.txt=must-keep-valid-coverage") {
		ginkgo.Fail(fmt.Sprintf("checklist should preserve valid prior coverage for unaffected path:\n%s", checklist), 1)
	}

	if !strings.Contains(checklist, "b.txt=use-source_satisfied_paths-if-current-state-is-final-or-submit-real-edit") {
		ginkgo.Fail(fmt.Sprintf("checklist should steer no-op path to source_satisfied_paths or a real edit:\n%s", checklist), 1)
	}
})

var _ = ginkgo.It("PlanExecuteSubmitCorrectionChecklistParsesSequenceNoopPath", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[1]: edit would not change b.txt after applying prior plan edits; answer `Decision: source-satisfied`, use `source_satisfied_paths` for mixed coverage, or submit real edits",
		Code:    workspace.SubmitPlanNoOpEditCode,
		Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
			Code:        workspace.SubmitPlanNoOpEditCode,
			Kind:        toolfacts.SubmitPlanDiagnosticNoOpEdit,
			CanonicalID: "b.txt",
		}),
	}

	checklist := planThenExecuteSubmitPlanCorrectionChecklist(brief, result)
	if strings.Contains(checklist, "after applying prior plan edits") {
		ginkgo.Fail(fmt.Sprintf("checklist parsed extra prose as path:\n%s", checklist), 1)
	}

	if !strings.Contains(checklist, "b.txt=use-source_satisfied_paths-if-current-state-is-final-or-submit-real-edit") {
		ginkgo.Fail(fmt.Sprintf("checklist should identify no-op path from sequence preflight:\n%s", checklist), 1)
	}
})

var _ = ginkgo.It("PlanExecuteSubmitCorrectionChecklistCoversPathlessNoop", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[0]: old_text and new_text are identical; answer `Decision: source-satisfied`, use `source_satisfied_paths` for mixed coverage, or submit real edits",
		Code:    workspace.SubmitPlanNoOpEditCode,
		Facts: toolfacts.NewSubmitPlanDiagnosticFacts(toolfacts.SubmitPlanDiagnostic{
			Code: workspace.SubmitPlanNoOpEditCode,
			Kind: toolfacts.SubmitPlanDiagnosticNoOpEdit,
		}),
	}

	checklist := planThenExecuteSubmitPlanCorrectionChecklist(brief, result)
	if !strings.Contains(checklist, "a.txt=must-have-real-edit-or-source_satisfied_paths-entry-no-noop") ||
		!strings.Contains(checklist, "b.txt=must-have-real-edit-or-source_satisfied_paths-entry-no-noop") {

		ginkgo.Fail(fmt.Sprintf("pathless no-op should force every required path away from no-op edits:\n%s", checklist), 1)
	}
})

var _ = ginkgo.It("PlanExecutePlanningStateDerivesTextualJSONTerminalTransport", func() {
	state := derivePlanExecutePlanningState(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go"},
	}, []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
	}, PlanExecutePlanningTransport{
		TextualToolParser:  true,
		TerminalOnly:       true,
		ResponseFormatJSON: true,
	}, planThenExecutePathAuthority{})

	if state.Mode != PlanExecutePlanningModeSubmitReady {
		ginkgo.Fail(fmt.Sprintf("mode = %q, want submit_ready", state.Mode), 1)
	}

	if state.TransportMode != ToolCallTransportModeTextualJSON {
		ginkgo.Fail(fmt.Sprintf("transport mode = %q, want textual_json_terminal", state.TransportMode), 1)
	}

	policy := DerivePlanExecuteRuntimeCallPolicy(RuntimeCallPolicyInput{
		TextualToolParser:  true,
		TerminalOnly:       true,
		ResponseFormat:     "json",
		MaxTokens:          4096,
		TimeoutMillis:      300000,
		ResponseFormatJSON: true,
	})
	if !policy.ResponseFormatJSON || policy.MaxTokens != 4096 || policy.TimeoutMillis != 300000 {
		ginkgo.Fail(fmt.Sprintf("runtime policy not preserved: %#v", policy), 1)
	}
})

var _ = ginkgo.It("PlanExecuteDuplicateRejectedPlanStaysInSubmitCorrection", func() {
	state := derivePlanExecutePlanningState(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go", "b.go"},
	}, []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
		readFileSourceWindowResult("b.go", 1, 10, 10, "1:package b\n"),
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[1]: edit would introduce adjacent duplicate code line 7 in b.go: \"const ready = true\"",
		},
	}, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})

	if state.Mode != PlanExecutePlanningModeSubmitCorrection {
		ginkgo.Fail(fmt.Sprintf("mode = %q, want submit_correction", state.Mode), 1)
	}

	if !slices.Contains(state.InvalidActionClasses, actionSourceSatisfied) {
		ginkgo.Fail(fmt.Sprintf("source-satisfied should be invalid after an untyped duplicate/no-op rejection: %#v", state.InvalidActionClasses), 1)
	}
})

var _ = ginkgo.It("PlanExecutePlanningStateKeepsRequiredSourceBeforeHistoryUntilAllRequiredPathsRead", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go", "b.go"},
		RequiredEvidence:  []string{string(workerruntime.RequiredEvidenceGitHistoryRationale)},
		AllowedTools:      []string{"read_file", "code_navigate", "history_range", "history_symbol", "submit_plan"},
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
	}

	state := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})
	if state.Mode != PlanExecutePlanningModeRequiredSource {
		ginkgo.Fail(fmt.Sprintf("mode = %q, want required_source while b.go is unread", state.Mode), 1)
	}

	for _, want := range []string{"read_file", "code_navigate"} {
		if !slices.Contains(state.ParserVisibleTools, want) {
			ginkgo.Fail(fmt.Sprintf("required-source phase missing %s: %#v", want, state.ParserVisibleTools), 1)
		}
	}

	for _, hidden := range []string{"history_range", "history_symbol", "submit_plan"} {
		if slices.Contains(state.ParserVisibleTools, hidden) {
			ginkgo.Fail(fmt.Sprintf("required-source phase should not expose %s before all required paths are read: %#v", hidden, state.ParserVisibleTools), 1)
		}
	}

	withAllSource := append(results, readFileSourceWindowResult("b.go", 1, 10, 10, "1:package b\n"))

	state = derivePlanExecutePlanningState(brief, withAllSource, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})

	if state.Mode != PlanExecutePlanningModeHistoryPending {
		ginkgo.Fail(fmt.Sprintf("mode after all required source reads = %q, want history_pending", state.Mode), 1)
	}

	for _, want := range []string{"a.go", "b.go"} {
		if !slices.Contains(state.SelectedEditPaths, want) {
			ginkgo.Fail(fmt.Sprintf("history-pending selected paths missing %s: %#v", want, state.SelectedEditPaths), 1)
		}
	}
})

var _ = ginkgo.It("PlanExecutePlanningStateRequiresHistoryAfterSourceEvidence", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go"},
		RequiredEvidence:  []string{string(workerruntime.RequiredEvidenceGitHistoryRationale)},
		AllowedTools:      []string{"read_file", "history_range", "history_symbol", "submit_plan"},
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
	}

	state := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})
	if state.Mode != PlanExecutePlanningModeHistoryPending {
		ginkgo.Fail(fmt.Sprintf("mode = %q, want history_pending", state.Mode), 1)
	}

	if slices.Contains(state.ParserVisibleTools, "submit_plan") {
		ginkgo.Fail(fmt.Sprintf("history-pending phase must not advertise submit_plan before git history evidence: %#v", state.ParserVisibleTools), 1)
	}

	for _, want := range []string{"history_range", "history_symbol"} {
		if !slices.Contains(state.ParserVisibleTools, want) {
			ginkgo.Fail(fmt.Sprintf("history-pending phase missing %s: %#v", want, state.ParserVisibleTools), 1)
		}
	}

	if slices.Contains(state.ParserVisibleTools, "read_file") {
		ginkgo.Fail(fmt.Sprintf("history-pending phase must not advertise read_file after source evidence is satisfied: %#v", state.ParserVisibleTools), 1)
	}

	if !slices.Contains(state.AllowedActionClasses, actionRead) {
		ginkgo.Fail(fmt.Sprintf("history-pending phase should expose history tools as read actions: %#v", state.AllowedActionClasses), 1)
	}

	if slices.Contains(state.AllowedActionClasses, "history") {
		ginkgo.Fail(fmt.Sprintf("history-pending phase should not advertise non-canonical history action class: %#v", state.AllowedActionClasses), 1)
	}

	if slices.Contains(state.InvalidActionClasses, actionRead) {
		ginkgo.Fail(fmt.Sprintf("history-pending phase should not invalidate read action class for history tools: %#v", state.InvalidActionClasses), 1)
	}

	withHistory := append(results, gitHistoryRangeResult("a.go"))

	state = derivePlanExecutePlanningState(brief, withHistory, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})

	if state.Mode != PlanExecutePlanningModeSubmitReady {
		ginkgo.Fail(fmt.Sprintf("mode after history = %q, want submit_ready", state.Mode), 1)
	}

	if !slices.Contains(state.ParserVisibleTools, "submit_plan") {
		ginkgo.Fail(fmt.Sprintf("submit_ready phase should expose submit_plan after history: %#v", state.ParserVisibleTools), 1)
	}
})

var _ = ginkgo.It("PlanExecutePartialRequiredHistoryDoesNotAdvertiseSubmitPlanReadiness", func() {
	t := ginkgo.GinkgoT()
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go", "b.go"},
		RequiredEvidence:  []string{string(workerruntime.RequiredEvidenceGitHistoryRationale)},
		AllowedTools:      []string{"read_file", "history_range", "history_symbol", "submit_plan"},
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
		readFileSourceWindowResult("b.go", 1, 10, 10, "1:package b\n"),
		gitHistoryRangeResult("a.go"),
	}

	state := derivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})
	if state.Mode != PlanExecutePlanningModeHistoryPending {
		ginkgo.Fail(fmt.Sprintf("mode with one required path still missing history = %q, want history_pending", state.Mode), 1)
	}

	if slices.Contains(state.ParserVisibleTools, "submit_plan") {
		ginkgo.Fail(fmt.Sprintf("partial history must not advertise submit_plan: %#v", state.ParserVisibleTools), 1)
	}

	if got := planThenExecuteReadyToSubmitNudge(brief, results, planThenExecutePathAuthority{}); got != "" {
		ginkgo.Fail(fmt.Sprintf("partial history must not emit submit-readiness nudge:\n%s", got), 1)
	}

	if planThenExecuteSubmitPlanOnlyReady(brief, results, planThenExecutePathAuthority{}) {
		ginkgo.Fail("partial history must not be submit-plan-only ready", 1)
	}

	pending := planThenExecuteHistoryPendingPaths(brief, results, nil, planThenExecutePathAuthority{})
	assertEqualSet(t, pending, []string{"b.go"})

	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
	prompt, ok := runner.completionGateRecoveryPrompt(results)

	if !ok {
		ginkgo.Fail("history-pending completion rejection should produce recovery guidance", 1)
	}

	for _, want := range []string{"required git-history evidence", "history_range", "b.go", "Permission-authorized inspection requests remain callable"} {
		if !strings.Contains(prompt, want) {
			ginkgo.Fail(fmt.Sprintf("history recovery prompt missing %q:\n%s", want, prompt), 1)
		}
	}
})

var _ = ginkgo.It("PlanExecutePostReadinessAllowsRequiredHistoryRead", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go"},
		RequiredEvidence:  []string{string(workerruntime.RequiredEvidenceGitHistoryRationale)},
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
	}
	req := workspace.ToolRequest{
		Tool:      "history_range",
		Path:      "a.go",
		StartLine: 1,
		EndLine:   10,
	}

	brief.AllowedTools = []string{workspace.ToolNameReadFile, workspace.ToolNameHistoryRange, workspace.ToolNameHistorySymbol, workspace.ToolNameSubmitPlan}
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: brief, Results: results, LastRequest: req, BaseAllowedTools: brief.AllowedTools,
		InspectionAuthority: permissionAuthorizedInspectionForTest(brief.AllowedTools...),
	})
	if !slices.Contains(contract.AllowedTools, workspace.ToolNameHistoryRange) {
		ginkgo.Fail(fmt.Sprintf("history read after source evidence must remain callable: %#v", contract))
	}
})

var _ = ginkgo.It("PlanExecutePostReadinessKeepsSourceReadCallableBeforeHistory", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go"},
		RequiredEvidence:  []string{string(workerruntime.RequiredEvidenceGitHistoryRationale)},
	}
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
	}
	req := workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "a.go",
		StartLine: 1,
		EndLine:   10,
	}

	brief.AllowedTools = []string{workspace.ToolNameReadFile, workspace.ToolNameHistoryRange, workspace.ToolNameHistorySymbol, workspace.ToolNameSubmitPlan}
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: brief, Results: results, LastRequest: req, BaseAllowedTools: brief.AllowedTools,
		InspectionAuthority: permissionAuthorizedInspectionForTest(brief.AllowedTools...),
	})
	if !slices.Contains(contract.AllowedTools, workspace.ToolNameReadFile) || slices.Contains(contract.ActionPolicy.InvalidActionClasses, actionRead) {
		ginkgo.Fail(fmt.Sprintf("source-ready/history-pending phase must keep authorized reads callable: %#v", contract))
	}
})

var _ = ginkgo.It("PlanExecuteSubmitBeforeHistoryRecordsTypedPrerequisite", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("old\n"), 0o600); err != nil {
		ginkgo.Fail(fmt.Sprintf("write a.go: %v", err), 1)
	}
	markGitHistoryRequirementWorkspace(ginkgo.GinkgoT(), root)
	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
	runner := newRunnerRunFixture(&Runner{
		Workspace: manager,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			AllowedTools:      []string{workspace.ToolNameReadFile, workspace.ToolNameHistoryRange, workspace.ToolNameSubmitPlan},
			RequiredEditPaths: []string{"a.go"},
			RequiredEvidence:  []string{string(workerruntime.RequiredEvidenceGitHistoryRationale)},
		},
	})
	results := []workspace.ToolResult{
		manager.Execute(runner.state.ctx, workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: "a.go", StartLine: 1, EndLine: 1}),
	}
	req := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{{
			Path:    "a.go",
			OldText: "old",
			NewText: "new",
			Purpose: "update",
		}},
	}

	result, ok := runner.submitPlanHistoryDeferralResult(req, results)
	if !ok {
		ginkgo.Fail("expected submit_plan-before-history typed prerequisite", 1)
	}

	if result.Tool != workspace.ToolNameSubmitPlan || result.Code != planThenExecuteSubmitPlanHistoryDeferredCode ||
		result.Facts == nil || result.Facts.RejectionDisposition != toolfacts.ToolRejectionDeferredByPrerequisite ||
		!slices.Equal(result.Facts.DeferredPlanPaths, []string{"a.go"}) ||
		!slices.Equal(result.Facts.PendingHistoryPaths, []string{"a.go"}) {

		ginkgo.Fail(fmt.Sprintf("unexpected typed prerequisite: %#v", result), 1)
	}

	allowed := runner.runnerPlanExecuteTextualAllowedToolsForRequest(
		[]string{workspace.ToolNameReadFile, workspace.ToolNameHistoryRange},
		runner.state.runtimeBrief,
		results,
		req,
		permissionAuthorizedInspectionForTest(workspace.ToolNameReadFile, workspace.ToolNameHistoryRange, workspace.ToolNameSubmitPlan),
		runner.planThenExecutePathAuthority(),
	)
	if !slices.Contains(allowed, workspace.ToolNameSubmitPlan) {
		ginkgo.Fail(fmt.Sprintf("otherwise-valid submit_plan must reach typed history deferral, got surface %#v", allowed), 1)
	}
})

var _ = ginkgo.It("PlanExecuteOutOfScopeSubmitDoesNotBecomeHistoryPrerequisiteIntent", func() {
	root := ginkgo.GinkgoT().TempDir()
	for _, path := range []string{"a.go", "outside.go"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("old\n"), 0o600); err != nil {
			ginkgo.Fail(fmt.Sprintf("write %s: %v", path, err), 1)
		}
	}
	markGitHistoryRequirementWorkspace(ginkgo.GinkgoT(), root)
	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
	runner := newRunnerRunFixture(&Runner{
		Workspace: manager,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			AllowedTools:      []string{workspace.ToolNameReadFile, workspace.ToolNameHistoryRange, workspace.ToolNameSubmitPlan},
			AllowedEditPaths:  []string{"a.go"},
			RequiredEvidence:  []string{string(workerruntime.RequiredEvidenceGitHistoryRationale)},
			PermissionProfile: "guarded_workspace_write",
		},
	})
	results := []workspace.ToolResult{
		manager.Execute(runner.state.ctx, workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: "outside.go", StartLine: 1, EndLine: 1}),
	}
	req := workspace.ToolRequest{
		Tool: workspace.ToolNameSubmitPlan,
		Edits: []planartifact.PlanEdit{{
			Path: "outside.go", OldText: "old", NewText: "new", Purpose: "escape the allowed plan scope",
		}},
	}

	if result, deferred := runner.submitPlanHistoryDeferralResult(req, results); deferred {
		ginkgo.Fail(fmt.Sprintf("out-of-scope plan must not be retained as history-prerequisite intent: %#v", result), 1)
	}
	allowed := runner.runnerPlanExecuteTextualAllowedToolsForRequest(
		[]string{workspace.ToolNameReadFile, workspace.ToolNameHistoryRange},
		runner.state.runtimeBrief,
		results,
		req,
		permissionAuthorizedInspectionForTest(runner.state.runtimeBrief.AllowedTools...),
		runner.planThenExecutePathAuthority(),
	)
	if slices.Contains(allowed, workspace.ToolNameSubmitPlan) {
		ginkgo.Fail(fmt.Sprintf("out-of-scope plan must remain outside the request surface: %#v", allowed), 1)
	}
})

var _ = ginkgo.It("PlanExecuteSubmitBeforeHistoryDoesNotDeferWhenNotApplicable", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.go"},
			RequiredEvidence:  []string{string(workerruntime.RequiredEvidenceGitHistoryRationale)},
		},
	})
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
		gitHistoryNotApplicableResult("a.go"),
	}
	req := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{{
			Path:    "a.go",
			OldText: "old",
			NewText: "new",
			Purpose: "update",
		}},
	}

	state := derivePlanExecutePlanningState(runner.state.runtimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true}, planThenExecutePathAuthority{})
	if state.Mode != PlanExecutePlanningModeSubmitReady {
		ginkgo.Fail(fmt.Sprintf("mode after path-scoped history_not_applicable = %q, want submit_ready", state.Mode), 1)
	}

	if deferred, ok := runner.submitPlanHistoryDeferralResult(req, results); ok {
		ginkgo.Fail(fmt.Sprintf("submit_plan must not defer after path-scoped history_not_applicable: %#v", deferred), 1)
	}

	if guard := runner.preflightPlanThenExecuteSubmitPlanHistoryGuard(req, results); guard.Intercepted {
		ginkgo.Fail(fmt.Sprintf("submit_plan guard should accept path-scoped history_not_applicable: %#v", guard.Result), 1)
	}
})

var _ = ginkgo.It("PlanExecuteSubmitBeforeHistoryDoesNotBypassUnreadRequiredSource", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.go", "b.go"},
			RequiredEvidence:  []string{string(workerruntime.RequiredEvidenceGitHistoryRationale)},
		},
	})
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
	}
	req := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "a.go", OldText: "old a", NewText: "new a", Purpose: "update a"},
			{Path: "b.go", OldText: "old b", NewText: "new b", Purpose: "update b"},
		},
	}

	if deferred, ok := runner.submitPlanHistoryDeferralResult(req, results); ok {
		ginkgo.Fail(fmt.Sprintf("submit_plan must not defer to history while a required path is unread: %#v", deferred), 1)
	}
})

var _ = ginkgo.It("ImplementationWriteBeforeHistoryRecordsTypedPrerequisite", func() {
	manager, results := sourceWindowManagerResultsForTest("a.go")
	markGitHistoryRequirementWorkspace(ginkgo.GinkgoT(), manager.Root())
	runner := newRunnerRunFixture(&Runner{
		Workspace: manager,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:       "implementation",
			RequiredEvidence: []string{string(workerruntime.RequiredEvidenceGitHistoryRationale)},
		},
	})
	req := workspace.ToolRequest{
		Tool:        "edit_file",
		Path:        "a.go",
		OldText:     "package fixture",
		NewText:     "package changed",
		Description: "update value after checking history",
	}

	result, ok := runner.implementationWritePrerequisiteDeferralResult(req, results)
	if !ok {
		ginkgo.Fail("expected write-before-history typed prerequisite", 1)
	}

	if result.Tool != workspace.ToolNameEditFile || result.Facts == nil ||
		result.Facts.RejectionDisposition != toolfacts.ToolRejectionDeferredByPrerequisite ||
		!slices.Equal(result.Facts.PendingHistoryPaths, []string{"a.go"}) {

		ginkgo.Fail(fmt.Sprintf("unexpected typed prerequisite: %#v", result), 1)
	}
})

var _ = ginkgo.It("PlanExecuteSubmitPlanExactAnchorGroundsUnscopedPath", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write fixture: %v", err), 1)
	}

	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	runner := newRunnerRunFixture(&Runner{
		Workspace: manager,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	})
	req := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{{
			Path:    "a.go",
			OldText: "return 1",
			NewText: "return 2",
			Purpose: "change value",
		}},
		Rationale: "Change value.",
	}
	guard := runner.preflightPlanThenExecuteSubmitPlanGroundingGuard(req, []workspace.ToolResult{
		manager.Execute(context.Background(), workspace.ToolRequest{
			Tool: workspace.ToolNameReadFile, Path: "a.go", StartLine: 1, EndLine: 3,
		}),
	})

	if guard.Intercepted {
		ginkgo.Fail(fmt.Sprintf("exact current-source anchor should ground unscoped submit_plan edit, got %#v", guard), 1)
	}
})

var _ = ginkgo.It("PlanExecuteManySelectedCandidateReadsKeepSubmitAndInspectionAvailable", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/test.json with 2 candidate file(s)",
			Facts:   contextPackCandidateFactsForTest("pkg/feature.go", "pkg/feature_test.go"),
		},
		readFileSourceWindowResult("pkg/feature.go", 1, 80, 80, "1:package pkg\n"),
	}

	for i := 0; i < 11; i++ {
		results = append(results, codeNavigateSourceWindowResult("pkg/feature.go", 20, 40, 0, "route_summary: Read Helper in pkg/feature.go lines 20-40\n20:func Helper() {}\n"))
	}

	if !planThenExecuteTerminalSubmitPlanAllowed(brief, results, planThenExecutePathAuthority{}) {
		ginkgo.Fail("source evidence for a selected candidate should make submit_plan available without reading every candidate", 1)
	}

	if got := planThenExecuteReadyToSubmitNudge(brief, results, planThenExecutePathAuthority{}); got == "" {
		ginkgo.Fail("selected candidate evidence should emit submit-ready guidance", 1)
	} else {
		for _, want := range []string{
			"This makes `submit_plan` available",
			"Allowed/candidate paths are not coverage obligations",
			"Permission-authorized inspection remains available",
		} {
			if !strings.Contains(got, want) {
				ginkgo.Fail(fmt.Sprintf("readiness nudge missing %q:\n%s", want, got), 1)
			}
		}
	}
})

var _ = ginkgo.It("PlanExecuteRunnerRepeatedCandidateReadDoesNotRecordRunnerRejection", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write fixture: %v", err), 1)
	}

	replies := []string{
		`{"tool":"read_file","path":"a.go","start_line":1,"end_line":4}`,
		`{"tool":"read_file","path":"a.go","start_line":1,"end_line":4,"named_gap":"need more context"}`,
		"<<QUESTION>>: no concrete selected edit path is required for this low-value-read regression fixture.",
	}

	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := replies[len(replies)-1]
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

	var (
		sawLowValue     bool
		eventRoles      []string
		modelCallEvents []map[string]any
		turnSummary     struct {
			RejectedActions          int `json:"rejected_actions"`
			CompletionGateRejections int `json:"completion_gate_rejections"`
		}
	)

	runner := newRunnerRunFixture(&Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
		OnEvent: func(event RunEvent) {
			eventRoles = append(eventRoles, event.Role)
			switch event.Role {
			case "plan_then_execute_low_value_action":
				sawLowValue = true
			case "model_call":
				var payload map[string]any

				if err := json.Unmarshal([]byte(event.Content), &payload); err != nil {
					ginkgo.Fail(fmt.Sprintf("decode model_call: %v", err), 1)
				}

				modelCallEvents = append(modelCallEvents, payload)
			case "turn_summary":
				if err := json.Unmarshal([]byte(event.Content), &turnSummary); err != nil {
					ginkgo.Fail(fmt.Sprintf("decode turn_summary: %v", err), 1)
				}
			}
		},
	})

	answer, err := runner.runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan a no-op"}}, llama.ChatOptions{MaxTokens: 2048}, nil, nil, nil)
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("Run: %v", err), 1)
	}

	if !strings.Contains(answer, "<<QUESTION>>") {
		ginkgo.Fail(fmt.Sprintf("expected typed blocker answer, got %q", answer), 1)
	}

	if sawLowValue {
		ginkgo.Fail(fmt.Sprintf("repeated candidate read should remain model-owned before budget exhaustion; saw events %#v", eventRoles), 1)
	}

	if len(modelCallEvents) == 0 {
		ginkgo.Fail("expected model_call telemetry", 1)
	}

	for _, key := range []string{"backend_kind", "transport_mode", "parser_contract", "response_format", "max_tokens", "thinking_policy", "supports_native_tools"} {
		if _, ok := modelCallEvents[0][key]; !ok {
			ginkgo.Fail(fmt.Sprintf("model_call telemetry missing %q: %#v", key, modelCallEvents[0]), 1)
		}
	}

	if turnSummary.RejectedActions != 0 || turnSummary.CompletionGateRejections != 0 {
		ginkgo.Fail(fmt.Sprintf("low-value read must not count as runner rejection, got %#v", turnSummary), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteCandidatePostReadinessDoesNotPromoteAllowedPathsToSubmitCoverage", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:       PlanThenExecutePlannerTaskFamily,
			AllowedEditPaths: []string{"a.txt", "b.txt"},
		},
	})
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{{
			Path:            "a.txt",
			InsertAfterLine: 1,
			Content:         "added\n",
			Purpose:         "add needed behavior",
		}},
	})

	if got.Intercepted {
		ginkgo.Fail(fmt.Sprintf("allowed candidate scope must constrain paths without requiring coverage for every allowed path: %#v", got), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteReadyToSubmitNudgeStandsDownAfterSubmit", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10"},
		{Tool: "submit_plan", Summary: "Plan submitted with 1 edit(s); the executor phase will dispatch them."},
	}

	if got := planThenExecuteReadyToSubmitNudge(brief, results, planThenExecutePathAuthority{}); got != "" {
		ginkgo.Fail(fmt.Sprintf("nudge must stand down once submit_plan succeeded; got %q", got), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteReadyToSubmitNudgeIgnoresOtherLanes", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		RequiredEditPaths: []string{"a.txt"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10"},
	}

	if got := planThenExecuteReadyToSubmitNudge(brief, results, planThenExecutePathAuthority{}); got != "" {
		ginkgo.Fail(fmt.Sprintf("nudge must only fire under plan_then_execute_planner; got %q", got), 1)
	}
})

var _ = ginkgo.It("PlanThenExecuteReadyToSubmitNudgeRecognizesLanguageSymbolReads", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"native/src/foo.cpp"},
	}
	result := codeNavigateSourceWindowResult(
		"native/src/foo.cpp", 10, 30, 30,
		"void MyClass::method() {}\n",
	)
	result.Tool = "cpp_read_symbol"
	results := []workspace.ToolResult{result}

	if got := planThenExecuteReadyToSubmitNudge(brief, results, planThenExecutePathAuthority{}); got == "" {
		ginkgo.Fail("nudge should recognize language-symbol typed source-body facts as covering the path", 1)
	}
})

var _ = ginkgo.It("PlanExecuteRunnerRedirectsEmptySubmitPlanSourceSatisfiedToTerminalAnswer", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write a.txt: %v", err), 1)
	}

	replies := []string{
		`{"tool":"read_file","path":"a.txt","start_line":1,"end_line":1}`,
		`{"tool":"submit_plan","decision":"source-satisfied","rationale":"The current source already contains the requested final state and is source-satisfied; no changes are needed.","edits":[]}`,
		"Decision: source-satisfied\n- a.txt: current source already contains the requested final state.",
	}

	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()
		if requests >= len(replies) {
			ginkgo.Fail("unexpected extra chat request", 1)
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))

	defer server.Close()

	var (
		sawShortCircuit     bool
		sawTerminalRedirect bool
	)

	runner := newRunnerRunFixture(&Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "plan_then_execute_source_satisfied_short_circuit" {
				sawShortCircuit = true
			}

			if event.Role == "plan_then_execute_source_satisfied_terminal_required" {
				sawTerminalRedirect = true
			}
		},
	})

	answer, err := runner.runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edits"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, nil, nil, nil)
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("Run: %v", err), 1)
	}

	if requests != len(replies) {
		ginkgo.Fail(fmt.Sprintf("runner should require an explicit terminal answer after source-satisfied submit_plan, got %d request(s)", requests), 1)
	}

	if sawShortCircuit {
		ginkgo.Fail("empty submit_plan must not short-circuit as source-satisfied", 1)
	}

	if !sawTerminalRedirect {
		ginkgo.Fail("source-satisfied submit_plan rejection should trigger terminal-shape recovery", 1)
	}

	for _, want := range []string{"Decision: source-satisfied", "a.txt"} {
		if !strings.Contains(answer, want) {
			ginkgo.Fail(fmt.Sprintf("answer missing %q:\n%s", want, answer), 1)
		}
	}
})

var _ = ginkgo.It("PlanExecuteRunnerRedirectsSourceSatisfiedAfterNoopSubmitPlan", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write a.txt: %v", err), 1)
	}

	replies := []string{
		`{"tool":"read_file","path":"a.txt","start_line":1,"end_line":1}`,
		`{"tool":"submit_plan","rationale":"No-op mistake for already-satisfied path.","edits":[{"path":"a.txt","old_text":"before","new_text":"before","purpose":"leave existing behavior"}]}`,
		`{"tool":"submit_plan","decision":"source-satisfied","rationale":"The current source already contains the requested final state and is source-satisfied; no changes are needed.","edits":[]}`,
		"Decision: source-satisfied\n- a.txt: current source already contains the requested final state.",
	}

	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()
		if requests >= len(replies) {
			ginkgo.Fail("unexpected extra chat request", 1)
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))

	defer server.Close()

	var sawTerminalRedirect bool

	runner := newRunnerRunFixture(&Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "plan_then_execute_source_satisfied_terminal_required" {
				sawTerminalRedirect = true
			}
		},
	})

	answer, err := runner.runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edits"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, nil, nil, nil)
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("Run: %v", err), 1)
	}

	if requests != len(replies) {
		ginkgo.Fail(fmt.Sprintf("runner should require an explicit terminal answer after source-satisfied submit_plan, got %d request(s)", requests), 1)
	}

	if !sawTerminalRedirect {
		ginkgo.Fail("source-satisfied submit_plan rejection should trigger terminal-shape recovery after no-op submit_plan", 1)
	}

	for _, want := range []string{"Decision: source-satisfied", "a.txt"} {
		if !strings.Contains(answer, want) {
			ginkgo.Fail(fmt.Sprintf("answer missing %q:\n%s", want, answer), 1)
		}
	}
})

var _ = ginkgo.It("ReviewRepairDoesNotAppendExternalTools", func() {
	if runnerBriefAllowsExternalToolAppend(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		PermissionProfile: "plan_submission_review",
	}) {

		ginkgo.Fail("review-repair must keep the exact phase tool surface and not append project externals", 1)
	}

	if !runnerBriefAllowsExternalToolAppend(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
	}) {

		ginkgo.Fail("planner phase should still inherit project discovery externals", 1)
	}

	if runnerBriefAllowsExternalToolAppend(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "verification_only",
		PermissionProfile: "read_only_verification_shell",
	}) {

		ginkgo.Fail("verification-only lanes must not widen their shell/check surface with project externals", 1)
	}
})

var _ = ginkgo.It("ReviewRepairInitialTextualSurfaceAllowsTargetedProbeTools", func() {
	t := ginkgo.GinkgoT()
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		PermissionProfile: "plan_submission_review",
		AllowedTools:      []string{"read_file", "code_navigate", "context_status", "context_query", "context_zoom", "context_pack", "submit_plan"},
	}
	inspection := permissionAuthorizedInspectionForTest(brief.AllowedTools...)
	got := runnerPlanExecuteTextualAllowedTools([]string{"submit_plan"}, brief, nil, inspection, planThenExecutePathAuthority{})
	assertContains(t, got, "read_file")
	assertContains(t, got, "code_navigate")
	assertContains(t, got, "context_status")
	assertContains(t, got, "submit_plan")
	assertNotContains(t, got, "context_pack")

	terminal := runnerPlanExecuteTextualAllowedTools([]string{"submit_plan"}, brief, []workspace.ToolResult{{
		Tool:    "completion_gate",
		Summary: "Review-repair completion gate rejected final answer",
		Error:   "review-repair must close",
	}}, inspection, planThenExecutePathAuthority{})

	assertContains(t, terminal, "read_file")
	assertContains(t, terminal, "code_navigate")
	assertContains(t, terminal, "context_status")
	assertContains(t, terminal, "submit_plan")
})

var _ = ginkgo.It("PlanExecuteSubmitPlanSourceSatisfiedEvidenceBridgeRejectsBodyWindow", func() {
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}})
	req := workspace.ToolRequest{
		Tool:                 "submit_plan",
		Rationale:            "Update a.txt; b.txt already contains the requested final state.",
		SourceSatisfiedPaths: []string{"b.txt"},
		Edits: []planartifact.PlanEdit{{
			Path:    "a.txt",
			OldText: "before\n",
			NewText: "after\n",
			Purpose: "replace a fixture",
		}},
	}
	bridged := runner.planThenExecuteAttachSubmitPlanSourceSatisfiedEvidence(req, []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f2 b.txt lines 1-200 (total lines: 1000)",
		Output:  "b.txt\n" + strings.Repeat("body\n", 10),
	}})

	if bridged.PlanArtifact == nil {
		ginkgo.Fail("expected legacy submit_plan to be normalized into an artifact", 1)
	}

	for _, evidence := range bridged.PlanArtifact.Evidence {
		if evidence.Path == "b.txt" {
			ginkgo.Fail(fmt.Sprintf("body-window source evidence must not be durable source_satisfied proof: %#v", bridged.PlanArtifact.Evidence), 1)
		}
	}
})

var _ = ginkgo.It("PlanExecuteSubmitPlanSourceSatisfiedBridgeStripsModelSuppliedEvidence", func() {
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}})
	req := workspace.ToolRequest{
		Tool:                 "submit_plan",
		Rationale:            "Update a.txt; b.txt already contains the requested final state.",
		SourceSatisfiedPaths: []string{"b.txt"},
		PlanArtifact: &planartifact.PlanArtifact{
			Task: planartifact.PlanArtifactTask{Summary: "mixed source-satisfied coverage"},
			Contract: planartifact.PlanArtifactContract{
				Kind:              "plan_then_execute",
				StrategyOwner:     "model",
				ContractOwner:     "harness",
				PermissionProfile: "plan_then_execute",
				VerificationOwner: "planner",
			},
			Paths: planartifact.PlanArtifactPaths{Required: []string{"a.txt", "b.txt"}},
			Evidence: []planartifact.PlanArtifactEvidence{{
				Kind: "read_file",
				Path: "b.txt",
			}},
			Edits: []planartifact.PlanEdit{{
				Path:    "a.txt",
				OldText: "before\n",
				NewText: "after\n",
				Purpose: "replace a fixture",
			}},
			VerificationObligations: []planartifact.PlanArtifactVerificationObligation{{
				Kind:     "executor_lane_sanity",
				Owner:    "planner",
				Status:   "open",
				Required: true,
			}},
		},
	}
	bridged := runner.planThenExecuteAttachSubmitPlanSourceSatisfiedEvidence(req, []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f2 b.txt lines 1-200 (total lines: 1000)",
		Output:  "b.txt\n" + strings.Repeat("body\n", 10),
	}})

	for _, evidence := range bridged.PlanArtifact.Evidence {
		if evidence.Path == "b.txt" {
			ginkgo.Fail(fmt.Sprintf("model-supplied source_satisfied evidence must be replaced by harness-observed anchor-ready evidence, got %#v", bridged.PlanArtifact.Evidence), 1)
		}
	}
})

var _ = ginkgo.It("PlanExecuteSubmitPlanSourceSatisfiedBridgeRejectsSnippetOnlyEvidence", func() {
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt", "b.txt"},
		RequiredEditSnippets: []plancontract.RequiredEditSnippet{
			{Path: "b.txt", Content: "already final"},
		},
	}})
	req := workspace.ToolRequest{
		Tool:                 "submit_plan",
		Rationale:            "Update a.txt; b.txt already contains the requested final state.",
		SourceSatisfiedPaths: []string{"b.txt"},
		Edits: []planartifact.PlanEdit{{
			Path:    "a.txt",
			OldText: "before\n",
			NewText: "after\n",
			Purpose: "replace a fixture",
		}},
	}
	bridged := runner.planThenExecuteAttachSubmitPlanSourceSatisfiedEvidence(req, nil)

	if bridged.PlanArtifact == nil {
		ginkgo.Fail("expected legacy submit_plan to be normalized into an artifact", 1)
	}

	for _, evidence := range bridged.PlanArtifact.Evidence {
		if evidence.Path == "b.txt" {
			ginkgo.Fail(fmt.Sprintf("required_edit_snippet must not be durable source_satisfied proof without observed source evidence: %#v", bridged.PlanArtifact.Evidence), 1)
		}
	}
})

var _ = ginkgo.It("PlanExecuteSubmitPlanCoverageAcceptsArtifactSourceSatisfiedPaths", func() {
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}})
	req := workspace.ToolRequest{
		Tool: "submit_plan",
		PlanArtifact: &planartifact.PlanArtifact{
			Paths: planartifact.PlanArtifactPaths{SourceSatisfied: []string{"b.txt"}},
			Edits: []planartifact.PlanEdit{
				{Path: "a.txt", OldText: "before", NewText: "after", Purpose: "replace a fixture"},
			},
		},
	}

	if result := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(req); result.Intercepted {
		ginkgo.Fail(fmt.Sprintf("artifact paths.source_satisfied should satisfy required coverage, got %#v", result.Result), 1)
	}
})

var _ = ginkgo.It("PlanExecuteSubmitPlanCoverageRejectsSourceSatisfiedMissingRequiredSnippet", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write a.txt: %v", err), 1)
	}

	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("not final\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write b.txt: %v", err), 1)
	}

	runner := newRunnerRunFixture(&Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt", "b.txt"},
			RequiredEditSnippets: []plancontract.RequiredEditSnippet{
				{ID: "b_final", Path: "b.txt", Content: "already final"},
			},
		},
	})
	req := workspace.ToolRequest{
		Tool:                 "submit_plan",
		SourceSatisfiedPaths: []string{"b.txt"},
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "before\n", NewText: "after\n", Purpose: "replace a fixture"},
		},
	}

	result := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(req)
	if !result.Intercepted || !strings.Contains(result.Result.Error, "missing required edit snippet(s): `b_final`") {
		ginkgo.Fail(fmt.Sprintf("expected missing required snippet rejection, got %#v", result), 1)
	}
})

var _ = ginkgo.It("PlanExecuteSubmitPlanCoverageRejectsOutOfScopeSourceSatisfiedPaths", func() {
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt"},
	}})
	req := workspace.ToolRequest{
		Tool:                 "submit_plan",
		SourceSatisfiedPaths: []string{"outside.txt"},
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "before", NewText: "after", Purpose: "replace a fixture"},
		},
	}

	result := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(req)
	if !result.Intercepted || !strings.Contains(result.Result.Error, "path(s) outside required_edit_paths: `outside.txt`") {
		ginkgo.Fail(fmt.Sprintf("expected out-of-scope source_satisfied_paths rejection, got %#v", result), 1)
	}
})

var _ = ginkgo.It("PlanExecuteRecoveryRetriesAfterNonSpecificSubmitPlanBlocker", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("before\n"), 0o644); err != nil {
			ginkgo.Fail(fmt.Sprintf("write %s: %v", path, err), 1)
		}
	}

	replies := []string{
		`{"tool":"read_file","path":"a.txt"}`,
		`{"tool":"read_file","path":"b.txt"}`,
		"Done.",
		`{"tool":"read_file","path":"a.txt"}`,
		"Still done.",
		"<<QUESTION>>: blocked by missing safe edit body.\n- a.txt: no safe edit body is available.\n- b.txt: no safe edit body is available.",
	}

	var (
		requests                            int
		recoveryPrompts                     int
		sawRepeatedSubmitPlanMissingBlocker bool
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()
		if requests >= len(replies) {
			ginkgo.Fail("unexpected extra chat request", 1)
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))

	defer server.Close()

	runner := newRunnerRunFixture(&Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
		OnEvent: func(event RunEvent) {
			switch event.Role {
			case "completion_gate_recovery":
				recoveryPrompts++
			case "completion_status":
				if strings.Contains(event.Content, "completion_gate_submit_plan_missing_repeated") {
					sawRepeatedSubmitPlanMissingBlocker = true
				}
			}
		},
	})

	answer, err := runner.runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edits"}}, llama.ChatOptions{MaxTokens: 2048}, nil, nil, nil)
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("Run: %v", err), 1)
	}

	if requests >= len(replies) {
		ginkgo.Fail(fmt.Sprintf("expected recovery to cut over before exhausting scripted retries, got %d request(s)", requests), 1)
	}

	if recoveryPrompts < 1 {
		ginkgo.Fail(fmt.Sprintf("expected one completion recovery prompt before cutover, got %d", recoveryPrompts), 1)
	}

	if !sawRepeatedSubmitPlanMissingBlocker {
		ginkgo.Fail("expected repeated submit-plan-missing completion status", 1)
	}

	if !strings.Contains(answer, "required edit path") {
		ginkgo.Fail(fmt.Sprintf("expected deterministic required-path blocker answer after recovery cutover, got:\n%s", answer), 1)
	}
})

var _ = ginkgo.It("PlanExecuteCompletionBlockerKindDistinguishesInvalidSubmitPlan", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	})
	results := []workspace.ToolResult{
		readFileSourceWindowResult("a.txt", 1, 2, 2, "before a\n"),
		readFileSourceWindowResult("b.txt", 1, 2, 2, "before b\n"),
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths",
		},
	}

	if got := string(runner.completionGateRepeatedViolation(results).Kind); got != "completion_gate_submit_plan_invalid_repeated" {
		ginkgo.Fail(fmt.Sprintf("completion blocker kind = %q, want submit-plan invalid", got), 1)
	}
})

var _ = ginkgo.It("ImplementationScopedSurfaceActiveForRequiredPathMutation", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{".idiom/refine/example/review.md"},
	}
	if !implementationScopedEditSurfaceActive(brief, nil, brief.RequiredEditPaths, false) {
		ginkgo.Fail("required-path mutation packets should use scoped discovery/edit tools even without target-packet anchors", 1)
	}

	brief.RequiredEditPaths = nil
	if implementationScopedEditSurfaceActive(brief, nil, nil, false) {
		ginkgo.Fail("scoped implementation surface must stay inactive without required edit paths", 1)
	}
})

var _ = ginkgo.It("DocDeliverableCompletionRecoveryRecordsEvidenceGapWithoutHidingReads", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
		},
	})
	prompt, ok := runner.completionGateRecoveryPrompt(nil)

	if !ok {
		ginkgo.Fail("expected completion recovery prompt", 1)
	}

	for _, want := range []string{"non-source deliverable", "deliverable-write contract", "Source verification pending", "<<QUESTION>>"} {
		if !strings.Contains(prompt, want) {
			ginkgo.Fail(fmt.Sprintf("expected prompt to contain %q, got %q", want, prompt), 1)
		}
	}
})

var _ = ginkgo.It("ScratchpadDeliverableFailedEvidenceNudgesDraftedGap", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
			EvidencePaths:     []string{".idiom/refine/example/iter-1/draft.md"},
		},
	})
	prompt, ok := runner.scratchpadDeliverableFailedEvidenceNudge(
		workspace.ToolRequest{Tool: "code_navigate", Capability: "references", Path: "native/src/example.cpp", Pattern: "MISSING_SYMBOL"},
		workspace.ToolResult{Tool: "code_navigate", Summary: "Code navigation routed references", Error: "no C++ symbols matched"},
		[]workspace.ToolResult{{Tool: "code_navigate", Summary: "Code navigation routed references", Error: "no C++ symbols matched"}},
	)

	if !ok {
		ginkgo.Fail("expected failed optional evidence to nudge scratchpad gap recording", 1)
	}

	for _, want := range []string{"does not satisfy the source-evidence contract", "Permission-authorized inspection remains callable", "fresh model-selected request", ".idiom/refine/example/iter-2/draft.md"} {
		if !strings.Contains(prompt, want) {
			ginkgo.Fail(fmt.Sprintf("expected prompt to contain %q, got %q", want, prompt), 1)
		}
	}
	for _, forbidden := range []string{"rather than repairing the lookup", "next action should be either writing"} {
		if strings.Contains(prompt, forbidden) {
			ginkgo.Fail(fmt.Sprintf("scratchpad recovery must not prescribe strategy %q: %q", forbidden, prompt), 1)
		}
	}
})

var _ = ginkgo.It("ScratchpadDeliverableFailedEvidenceKeepsOriginalFailure", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
	}
	result, ok := scratchpadDeliverableFailedEvidenceResult(brief, []workspace.ToolResult{
		{Tool: "code_navigate", Summary: "Code navigation routed references", Error: "no C++ symbols matched"},
		{Tool: "read_file", Summary: "Scratchpad evidence follow-up rejected", Error: "A previous optional evidence lookup failed"},
	}, brief.RequiredEditPaths)

	if !ok {
		ginkgo.Fail("expected original failed evidence result", 1)
	}

	if result.Tool != "code_navigate" || strings.Contains(result.Error, "previous optional evidence") {
		ginkgo.Fail(fmt.Sprintf("expected original failure, got %#v", result), 1)
	}
})

var _ = ginkgo.It("ScratchpadDeliverableFailedEvidenceIgnoresSourceImplementation", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"native/src/example.cpp"},
		},
	})
	if _, ok := runner.scratchpadDeliverableFailedEvidenceNudge(
		workspace.ToolRequest{Tool: "code_navigate", Capability: "references", Path: "native/src/example.cpp", Pattern: "MISSING_SYMBOL"},
		workspace.ToolResult{Tool: "code_navigate", Summary: "Code navigation routed references", Error: "no C++ symbols matched"},
		nil,
	); ok {
		ginkgo.Fail("source implementation failures should keep the normal recovery path", 1)
	}
})

var _ = ginkgo.It("ToolAuthorizationPolicySurfaceRejectsUnavailableTool", func() {
	decision := toolAuthorizationSurfaceDecision(
		workspace.ToolRequest{Tool: "shell"},
		[]string{"read_file"},
		false,
	)

	if !decision.Rejected || decision.Summary != "Tool not available in current runtime contract" {
		ginkgo.Fail(fmt.Sprintf("expected surface rejection, got %#v", decision), 1)
	}

	if decision.Policy.Decision != autonomy.PolicyReject ||
		decision.Policy.PackID != autonomy.ContractPackToolAuthorization ||
		decision.Policy.InvariantID != toolAuthorizationPolicyInvariant ||
		decision.Policy.ReasonCode != "current_tool_surface" ||
		decision.Policy.ErrorClass != "tool_surface" {
		ginkgo.Fail(fmt.Sprintf("unexpected surface policy metadata: %#v", decision.Policy), 1)
	}
	if decision.Policy.ParityReport.SurfaceID != "current_tool_surface" {
		ginkgo.Fail(fmt.Sprintf("policy parity report not carried to event input: %#v", decision.Policy.ParityReport), 1)
	}
	if decision.ParityReport.SurfaceID != "current_tool_surface" ||
		decision.ParityReport.InvariantID != toolAuthorizationPolicyInvariant ||
		decision.ParityReport.OldDecision != "reject" ||
		decision.ParityReport.NewDecision != "reject" ||
		decision.ParityReport.Diverged {

		ginkgo.Fail(fmt.Sprintf("unexpected surface parity report: %#v", decision.ParityReport), 1)
	}
})

var _ = ginkgo.It("ToolAuthorizationPolicySurfaceSkipsRecoveryAllowance", func() {
	decision := toolAuthorizationSurfaceDecision(
		workspace.ToolRequest{Tool: "shell"},
		[]string{"read_file"},
		true,
	)

	if decision.Rejected {
		ginkgo.Fail(fmt.Sprintf("recovery allowance should skip surface rejection, got %#v", decision), 1)
	}
	if decision.ParityReport.SurfaceID != "current_tool_surface" || decision.ParityReport.NewDecision != "allow" {
		ginkgo.Fail(fmt.Sprintf("expected report-only allow parity for recovery allowance: %#v", decision.ParityReport), 1)
	}
	if decision.Policy.EventType != autonomy.EventToolPolicyDecision ||
		decision.Policy.Decision != autonomy.PolicyAllow ||
		decision.Policy.Result != "allowed" ||
		decision.Policy.ParityReport.SurfaceID != "current_tool_surface" {

		ginkgo.Fail(fmt.Sprintf("expected emit-ready allow parity policy: %#v", decision.Policy), 1)
	}
})

var _ = ginkgo.It("TextualWriteNormalizerAcceptsKeyValueWriteSyntax", func() {
	reply := "tool: edit_file\npath: internal/labels/labels.go\nold_text: return \"Save changes\"\nnew_text: return \"Save\"\ndescription: update label"

	result := normalizeToolRequestSyntax(llama.ChatMessage{Content: reply})
	if !result.OK {
		ginkgo.Fail(fmt.Sprintf("key-value write syntax should normalize before completion gating, outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
	}

	if result.Request.Tool != "edit_file" || result.Request.OldText != `return "Save changes"` || result.Request.NewText != `return "Save"` {
		ginkgo.Fail(fmt.Sprintf("unexpected normalized write request: %#v", result.Request), 1)
	}

	if tool, transport, unsafe := unsafeTextualWriteAttempt(reply); unsafe {
		ginkgo.Fail(fmt.Sprintf("normalized key-value write should not be unsafe, tool=%q transport=%q", tool, transport), 1)
	}
})

var _ = ginkgo.It("TextualWriteGateDirectImplementationRejectsFunctionCallWriteWithExecutableGuidance", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	})
	reply := `tool: edit_file(path="internal/labels/labels.go", old_text="return \"Save changes\"", new_text="return \"Save\"", description="update label")`
	results := []workspace.ToolResult{
		readFileSourceWindowResult("internal/labels/labels.go", 1, 5, 5, "package labels\n\nfunc ShortLabel() string {\n\treturn \"Save changes\"\n}\n"),
	}

	got, rejected := textualToolCallCompletionRejection(runner, reply, results)

	if !rejected {
		ginkgo.Fail("expected unsafe function-call write syntax to be rejected", 1)
	}

	for _, want := range []string{
		"could not be converted into a safe write request",
		"one visible JSON object",
		"Qwen key-value `tool:` syntax",
		"`path`, `old_text`, `new_text`, and `description`",
		"Function-call write syntax is not executable",
	} {
		if !strings.Contains(got.Error, want) {
			ginkgo.Fail(fmt.Sprintf("expected direct implementation gate message to contain %q, got %q", want, got.Error), 1)
		}
	}

	if strings.Contains(got.Error, "submit_plan") {
		ginkgo.Fail(fmt.Sprintf("direct implementation gate should not point at planner submit_plan: %q", got.Error), 1)
	}
})

var _ = ginkgo.It("TextualWriteGatePlanExecutePointsAtSubmitPlan", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"internal/labels/labels.go"},
		},
	})
	reply := `tool: edit_file(path="internal/labels/labels.go", old_text="return \"Save changes\"", new_text="return \"Save\"", description="update label")`
	results := []workspace.ToolResult{
		readFileSourceWindowResult("internal/labels/labels.go", 1, 5, 5, "package labels\n\nfunc ShortLabel() string {\n\treturn \"Save changes\"\n}\n"),
	}

	got, rejected := textualToolCallCompletionRejection(runner, reply, results)

	if !rejected {
		ginkgo.Fail("expected unsafe textual write syntax to be rejected", 1)
	}

	for _, want := range []string{
		"plan-execute planner phase does not execute per-edit writes",
		"one visible JSON object for `submit_plan`",
		"`rationale`, and `edits`",
		"Function-call write syntax is not executable",
	} {
		if !strings.Contains(got.Error, want) {
			ginkgo.Fail(fmt.Sprintf("expected plan-execute gate message to contain %q, got %q", want, got.Error), 1)
		}
	}

	if strings.Contains(got.Error, "`path`, `old_text`, `new_text`") {
		ginkgo.Fail(fmt.Sprintf("plan-execute gate must not point at per-edit write JSON: %q", got.Error), 1)
	}

	for _, forbidden := range []string{"Decision: source-satisfied", "<<QUESTION>>"} {
		if strings.Contains(got.Error, forbidden) {
			ginkgo.Fail(fmt.Sprintf("plan-execute gate advertised stale terminal exit %q: %q", forbidden, got.Error), 1)
		}
	}
})

var _ = ginkgo.It("TextualWriteGateReadOnlyKeepsGenericToolMarkupRejection", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
		},
	})
	reply := "tool: edit_file\npath: internal/labels/labels.go\nold_text: old\nnew_text: new\ndescription: update label"

	got, rejected := textualToolCallCompletionRejection(runner, reply, nil)

	if !rejected {
		ginkgo.Fail("expected textual write markup to be rejected in read-only lane", 1)
	}

	for _, forbidden := range []string{"one visible JSON object", "`path`, `old_text`, `new_text`", "submit_plan"} {
		if strings.Contains(got.Error, forbidden) {
			ginkgo.Fail(fmt.Sprintf("read-only rejection must not advertise write recovery %q: %q", forbidden, got.Error), 1)
		}
	}

	if !strings.Contains(got.Error, "textual tool-call markup") {
		ginkgo.Fail(fmt.Sprintf("read-only lane should keep generic textual-tool rejection, got %q", got.Error), 1)
	}
})

var _ = ginkgo.It("TextualWriteGatePreSourceImplementationKeepsGenericToolMarkupRejection", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	})
	reply := "tool: edit_file\npath: internal/labels/labels.go\nold_text: old\nnew_text: new\ndescription: update label"

	got, rejected := textualToolCallCompletionRejection(runner, reply, nil)

	if !rejected {
		ginkgo.Fail("expected textual write markup to be rejected before source evidence", 1)
	}

	if strings.Contains(got.Error, "one visible JSON object") ||
		strings.Contains(got.Error, "`path`, `old_text`, `new_text`") {

		ginkgo.Fail(fmt.Sprintf("pre-source implementation rejection must not advertise write-ready recovery: %q", got.Error), 1)
	}
})

var _ = ginkgo.It("TextualWriteGatePostWriteVerificationKeepsGenericToolMarkupRejection", func() {
	runner := newRunnerRunFixture(&Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	})
	reply := "tool: edit_file\npath: internal/labels/labels.go\nold_text: old\nnew_text: new\ndescription: update label"
	results := []workspace.ToolResult{{
		Tool:    "edit_file",
		Summary: "Edited internal/labels/labels.go",
	}}

	got, rejected := textualToolCallCompletionRejection(runner, reply, results)

	if !rejected {
		ginkgo.Fail("expected textual write markup to be rejected after a write", 1)
	}

	if strings.Contains(got.Error, "one visible JSON object") ||
		strings.Contains(got.Error, "`path`, `old_text`, `new_text`") {

		ginkgo.Fail(fmt.Sprintf("post-write verification rejection must not advertise another write recovery: %q", got.Error), 1)
	}
})

var _ = ginkgo.It("DeriveNormalizesTransportAndContractFields", func() {
	t := ginkgo.GinkgoT()
	thinkingEnabled := false

	policy := DeriveRuntimeCallPolicy(RuntimeCallPolicyInput{
		SupportsNativeTools:      true,
		ResponseFormat:           " json ",
		Temperature:              0.2,
		TopP:                     1,
		TopK:                     20,
		MinP:                     0.05,
		PresencePenalty:          0.1,
		RepetitionPenalty:        1.1,
		ToolChoice:               " required ",
		ParserContract:           " submit_plan_json ",
		ThinkingPolicy:           " disabled_visible ",
		EnableThinking:           &thinkingEnabled,
		PreserveThinking:         false,
		ThinkingBudget:           128,
		ChatTemplateKwargs:       true,
		MaxTokens:                512,
		TimeoutMillis:            2500,
		ExpressibleActionClasses: []string{"submit_plan", "blocker", "submit_plan"},
	})

	if policy.TransportMode != ToolCallTransportModeTextualJSON {
		ginkgo.Fail(fmt.Sprintf("TransportMode = %q, want %q", policy.TransportMode, ToolCallTransportModeTextualJSON), 1)
	}

	if policy.ToolTransportMode() != "textual" {
		ginkgo.Fail(fmt.Sprintf("ToolTransportMode = %q, want textual", policy.ToolTransportMode()), 1)
	}

	if !policy.ResponseFormatJSON || policy.ResponseFormatMode() != "json" {
		ginkgo.Fail(fmt.Sprintf("response format = json:%v mode:%q, want json", policy.ResponseFormatJSON, policy.ResponseFormatMode()), 1)
	}

	if !policy.ToolChoiceRequired || policy.ToolChoice != "required" {
		ginkgo.Fail(fmt.Sprintf("tool choice = required:%v value:%q, want required", policy.ToolChoiceRequired, policy.ToolChoice), 1)
	}

	if policy.ParserContract != "submit_plan_json" {
		ginkgo.Fail(fmt.Sprintf("ParserContract = %q, want submit_plan_json", policy.ParserContract), 1)
	}

	if policy.Temperature != 0.2 || policy.TopP != 1 || policy.TopK != 20 || policy.MinP != 0.05 || policy.PresencePenalty != 0.1 || policy.RepetitionPenalty != 1.1 {
		ginkgo.Fail(fmt.Sprintf("sampling fields not preserved: %#v", policy), 1)
	}

	assertEqualSet(t, policy.ExpressibleActionClasses, []string{"submit_plan", "blocker"})

	if policy.EnableThinking == nil || *policy.EnableThinking {
		ginkgo.Fail(fmt.Sprintf("EnableThinking = %#v, want false pointer", policy.EnableThinking), 1)
	}
})

var _ = ginkgo.It("DeriveUsesNativeToolsWhenParserIsNative", func() {
	policy := DeriveRuntimeCallPolicy(RuntimeCallPolicyInput{
		SupportsNativeTools: true,
		ToolChoice:          "required",
		MaxTokens:           4096,
	})

	if policy.TransportMode != ToolCallTransportModeNative {
		ginkgo.Fail(fmt.Sprintf("TransportMode = %q, want %q", policy.TransportMode, ToolCallTransportModeNative), 1)
	}

	if policy.ToolTransportMode() != "native" {
		ginkgo.Fail(fmt.Sprintf("ToolTransportMode = %q, want native", policy.ToolTransportMode()), 1)
	}

	if policy.TextualToolParser {
		ginkgo.Fail("TextualToolParser = true, want false", 1)
	}
})

var _ = ginkgo.It("DeriveForceNativeOverridesBackendDefault", func() {
	policy := DeriveRuntimeCallPolicy(RuntimeCallPolicyInput{
		SupportsNativeTools: false,
		ForceNativeTools:    true,
		ToolChoice:          "required",
		MaxTokens:           4096,
	})

	if policy.TransportMode != ToolCallTransportModeNative {
		ginkgo.Fail(fmt.Sprintf("TransportMode = %q, want %q", policy.TransportMode, ToolCallTransportModeNative), 1)
	}

	if policy.ToolTransportMode() != "native" {
		ginkgo.Fail(fmt.Sprintf("ToolTransportMode = %q, want native", policy.ToolTransportMode()), 1)
	}

	if policy.TextualToolParser {
		ginkgo.Fail("TextualToolParser = true, want false for forced native transport", 1)
	}
})

var _ = ginkgo.It("ActiveSurfaceCopiesInputSlices", func() {
	visible := []string{"read_file"}
	hidden := []string{"shell"}

	surface := NewActiveSurface(visible, hidden)
	visible[0] = "shell"
	hidden[0] = "read_file"

	if !surface.AllowsTool("read_file") {
		ginkgo.Fail(fmt.Sprintf("expected copied visible tool to remain allowed: %#v", surface.VisibleTools), 1)
	}
	if !surface.HidesTool("shell") {
		ginkgo.Fail(fmt.Sprintf("expected copied hidden tool to remain hidden: %#v", surface.HiddenTools), 1)
	}
})

var _ = ginkgo.It("ActiveSurfaceNormalizesVisibility", func() {
	surface := NewActiveSurface(
		[]string{" read_file ", "shell", "read_file", ""},
		[]string{"shell", "shell", ""},
	)

	if surface.AllowsTool("shell") {
		ginkgo.Fail(fmt.Sprintf("hidden tool leaked into visible surface: %#v", surface), 1)
	}
	if len(surface.VisibleTools) != 1 || surface.VisibleTools[0] != "read_file" {
		ginkgo.Fail(fmt.Sprintf("VisibleTools = %#v, want read_file", surface.VisibleTools), 1)
	}
	if len(surface.HiddenTools) != 1 || surface.HiddenTools[0] != "shell" {
		ginkgo.Fail(fmt.Sprintf("HiddenTools = %#v, want shell", surface.HiddenTools), 1)
	}
})

var _ = ginkgo.It("ActiveSurfaceHideAndReleaseByOwner", func() {
	surface := NewActiveSurface([]string{"read_file", "shell"}, nil)

	surface.Hide(OwnerLoopPolicy, "read_file")
	surface.Hide(OwnerChildPacket, "read_file")
	surface.Release(OwnerLoopPolicy)

	if surface.HiddenByOwner(OwnerLoopPolicy, "read_file") {
		ginkgo.Fail(fmt.Sprintf("loop owner should be released: %#v", surface), 1)
	}
	if !surface.HiddenByOwner(OwnerChildPacket, "read_file") {
		ginkgo.Fail(fmt.Sprintf("child-packet owner should still hold read_file: %#v", surface), 1)
	}
	if !surface.HidesTool("read_file") {
		ginkgo.Fail(fmt.Sprintf("read_file should remain hidden while another owner holds it: %#v", surface), 1)
	}
	if surface.AllowsTool("read_file") {
		ginkgo.Fail(fmt.Sprintf("hidden read_file leaked into visible tools: %#v", surface.VisibleTools), 1)
	}

	surface.Release(OwnerChildPacket)
	surface.SetVisibleTools([]string{"read_file", "shell"})
	if surface.HidesTool("read_file") {
		ginkgo.Fail(fmt.Sprintf("read_file should be released: %#v", surface.HiddenTools), 1)
	}
	if !surface.AllowsTool("read_file") {
		ginkgo.Fail(fmt.Sprintf("read_file should be visible after release: %#v", surface.VisibleTools), 1)
	}
})

var _ = ginkgo.It("ActiveSurfaceSetVisibleFiltersHidden", func() {
	surface := NewActiveSurface(nil, nil)
	surface.Hide(OwnerLoopPolicy, "shell")
	surface.SetVisibleTools([]string{"read_file", "shell"})

	if surface.AllowsTool("shell") {
		ginkgo.Fail(fmt.Sprintf("hidden shell leaked into visible surface: %#v", surface.VisibleTools), 1)
	}
	if !surface.AllowsTool("read_file") {
		ginkgo.Fail(fmt.Sprintf("read_file should remain visible: %#v", surface.VisibleTools), 1)
	}
})

var _ = ginkgo.It("ActiveSurfaceHideExcept", func() {
	surface := NewActiveSurface([]string{"read_file", "edit_file", "shell"}, nil)
	surface.HideExcept(Owner("test_explicit_subset"), surface.VisibleTools, func(tool string) bool {
		return tool == "edit_file"
	})

	if !surface.AllowsTool("edit_file") {
		ginkgo.Fail(fmt.Sprintf("edit_file should remain visible: %#v", surface.VisibleTools), 1)
	}
	if surface.AllowsTool("read_file") || surface.AllowsTool("shell") {
		ginkgo.Fail(fmt.Sprintf("non-write tools should be hidden: %#v", surface.VisibleTools), 1)
	}
})

var _ = ginkgo.It("ActiveSurfaceRejectionForUnavailableTool", func() {
	surface := NewActiveSurface([]string{"read_file"}, nil)

	rejection := surface.RejectionForTool("shell")

	if !rejection.Reject {
		ginkgo.Fail("expected unavailable tool rejection", 1)
	}
	if rejection.Summary != "Tool not available in current runtime contract" {
		ginkgo.Fail(fmt.Sprintf("Summary = %q", rejection.Summary), 1)
	}
	if rejection.Error != "shell is not in the current allowed tool surface. Available tools: read_file. Use an available tool, answer from gathered evidence, or report a concrete blocker." {
		ginkgo.Fail(fmt.Sprintf("Error = %q", rejection.Error), 1)
	}
})

var _ = ginkgo.It("ActiveSurfaceRejectionForHiddenToolUsesVisibleProjection", func() {
	surface := NewActiveSurface([]string{"read_file", "shell"}, nil)
	surface.Hide(OwnerLoopPolicy, "shell")

	rejection := surface.RejectionForTool("shell")

	if !rejection.Reject {
		ginkgo.Fail("expected hidden tool rejection", 1)
	}
	if rejection.Error != "shell is not in the current allowed tool surface. Available tools: read_file. Use an available tool, answer from gathered evidence, or report a concrete blocker." {
		ginkgo.Fail(fmt.Sprintf("Error = %q", rejection.Error), 1)
	}
})

var _ = ginkgo.It("ActiveSurfaceRejectionForEmptySurface", func() {
	surface := NewActiveSurface(nil, nil)

	rejection := surface.RejectionForTool("shell")

	if !rejection.Reject {
		ginkgo.Fail("expected empty surface rejection", 1)
	}
	if rejection.Error != "shell is not available because the current runtime contract exposes no tools. Answer from gathered evidence or report a concrete blocker." {
		ginkgo.Fail(fmt.Sprintf("Error = %q", rejection.Error), 1)
	}
})

var _ = ginkgo.It("DirectAnswerSurfaceForBriefFiltersProtocolAndMutationTools", func() {
	surface, ok := DirectAnswerSurfaceForBrief(testDirectAnswerSurfaceBrief())
	if !ok {
		ginkgo.Fail("expected direct-answer surface", 1)
	}
	if !slices.Equal(surface.ToolNames, workspace.DirectAnswerSurfaceToolNames()) {
		ginkgo.Fail(fmt.Sprintf("direct-answer runtime surface must project workspace-owned tools\ngot  %#v\nwant %#v", surface.ToolNames, workspace.DirectAnswerSurfaceToolNames()), 1)
	}

	tools := map[string]bool{}

	for _, tool := range surface.ToolNames {
		if strings.TrimSpace(tool) == "" {
			ginkgo.Fail(fmt.Sprintf("surface contains blank tool name: %#v", surface.ToolNames), 1)
		}

		if tools[tool] {
			ginkgo.Fail(fmt.Sprintf("surface contains duplicate tool %q: %#v", tool, surface.ToolNames), 1)
		}

		tools[tool] = true
	}

	for _, tool := range workspace.WorkspaceWriteToolNames() {
		if tools[tool] {
			ginkgo.Fail(fmt.Sprintf("direct-answer surface exposed write tool %q", tool), 1)
		}
	}

	for _, tool := range []string{"submit_plan", "task_ledger", "note", "shell"} {
		if tools[tool] {
			ginkgo.Fail(fmt.Sprintf("direct-answer surface exposed protocol tool %q", tool), 1)
		}
	}

	for _, tool := range append([]string{
		"read_file",
		"search_content",
		"context_query",
		"context_zoom",
		"code_navigate",
		"task",
	}, generalReadOnlyDiagnosticToolRegressionSet()...) {
		if !tools[tool] {
			ginkgo.Fail(fmt.Sprintf("direct-answer surface did not expose read-only investigation tool %q; got %#v", tool, surface.ToolNames), 1)
		}
	}

	if !slices.Contains(surface.Capabilities, DirectAnswerSurfaceCapReadOnlyInvestigation) {
		ginkgo.Fail("direct-answer surface should advertise read-only investigation", 1)
	}
})

var _ = ginkgo.It("DirectAnswerSurfaceNotReturnedForWriteBrief", func() {
	brief := testDirectAnswerSurfaceBrief()

	brief.PermissionProfile = string(workerruntime.PermissionGuardedWorkspaceWrite)

	if _, ok := DirectAnswerSurfaceForBrief(brief); ok {
		ginkgo.Fail("write-capable direct-answer brief must not receive lightweight direct-answer surface", 1)
	}

	brief = testDirectAnswerSurfaceBrief()
	brief.TaskFamily = string(autonomy.TaskFamilyImplementation)

	if _, ok := DirectAnswerSurfaceForBrief(brief); ok {
		ginkgo.Fail("non-direct-answer task family must not receive lightweight direct-answer surface", 1)
	}
})

var _ = ginkgo.It("EmitDirectAnswerSurfaceEvent", func() {
	surface, ok := DirectAnswerSurfaceForBrief(testDirectAnswerSurfaceBrief())
	if !ok {
		ginkgo.Fail("expected direct-answer surface", 1)
	}

	var got []RunEvent

	EmitDirectAnswerSurfaceEvent(func(event RunEvent) {
		got = append(got, event)
	}, surface)

	if len(got) != 1 {
		ginkgo.Fail(fmt.Sprintf("events = %d, want 1", len(got)), 1)
	}

	if got[0].Role != "direct_answer_surface" {
		ginkgo.Fail(fmt.Sprintf("event role = %q, want direct_answer_surface", got[0].Role), 1)
	}

	var payload struct {
		SchemaVersion  string   `json:"schema_version"`
		Version        string   `json:"version"`
		Capabilities   []string `json:"capabilities"`
		ToolCount      int      `json:"tool_count"`
		TerminalStates []string `json:"terminal_states"`
	}

	if err := json.Unmarshal([]byte(got[0].Content), &payload); err != nil {
		ginkgo.Fail(fmt.Sprintf("unmarshal surface event: %v", err), 1)
	}

	if payload.SchemaVersion != DirectAnswerSurfaceSchemaVersion {
		ginkgo.Fail(fmt.Sprintf("schema version = %q", payload.SchemaVersion), 1)
	}

	if payload.Version != DirectAnswerSurfaceVersion {
		ginkgo.Fail(fmt.Sprintf("version = %q", payload.Version), 1)
	}

	if payload.ToolCount != len(surface.ToolNames) {
		ginkgo.Fail(fmt.Sprintf("tool count = %d, want %d", payload.ToolCount, len(surface.ToolNames)), 1)
	}

	if len(payload.TerminalStates) == 0 {
		ginkgo.Fail("expected terminal states in surface event", 1)
	}
})

var _ = ginkgo.It("RunnerDirectAnswerSurfacePrefetchesNamedPath", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("Idiom is a local coding-agent harness.\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write README fixture: %v", err), 1)
	}

	replies := []string{
		"Idiom is a local coding-agent harness. Source: README.md.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()
		if requests >= len(replies) {
			ginkgo.Fail(fmt.Sprintf("unexpected extra chat request #%d", requests), 1)
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))

	defer server.Close()

	runner := newRunnerRunFixture(&Runner{
		Client:    llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 3},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(autonomy.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			Objective:         "Read README.md and answer what Idiom is.",
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
			AllowedTools:      []string{"read_file", "write_file", "submit_plan"},
		},
	})

	var (
		surfaceEvent    RunEvent
		prefetchEvents  []RunEvent
		readFileResults int
	)

	runner.runner.OnEvent = func(event RunEvent) {
		if event.Role == "direct_answer_surface" {
			surfaceEvent = event
		}

		if strings.Contains(event.Role, "prefetch") {
			prefetchEvents = append(prefetchEvents, event)
		}
	}

	answer, err := runner.runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: runner.state.runtimeBrief.Objective}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool == "read_file" {
				readFileResults++
			}
		},
	)
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("Run: %v", err), 1)
	}

	if !strings.Contains(answer, "README.md") || !strings.Contains(answer, "Idiom is a local coding-agent harness") {
		ginkgo.Fail(fmt.Sprintf("expected grounded README answer citing source and fixture content, got %q", answer), 1)
	}

	if surfaceEvent.Role == "" {
		ginkgo.Fail("runner did not emit direct_answer_surface event", 1)
	}

	var payload struct {
		Capabilities []string `json:"capabilities"`
	}

	if err := json.Unmarshal([]byte(surfaceEvent.Content), &payload); err != nil {
		ginkgo.Fail(fmt.Sprintf("unmarshal direct-answer surface event: %v", err), 1)
	}

	if !slices.Contains(payload.Capabilities, DirectAnswerSurfaceCapSourceBodyPrefetch) {
		ginkgo.Fail(fmt.Sprintf("runner surface did not advertise source_body_prefetch: %#v", payload.Capabilities), 1)
	}

	if len(prefetchEvents) != 1 {
		ginkgo.Fail(fmt.Sprintf("expected one prefetch event, got %#v", prefetchEvents), 1)
	}

	if readFileResults != 0 {
		ginkgo.Fail(fmt.Sprintf("prefetch should avoid model-initiated read_file before final answer, got %d", readFileResults), 1)
	}

	if requests != len(replies) {
		ginkgo.Fail(fmt.Sprintf("expected %d model requests, got %d", len(replies), requests), 1)
	}
})

var _ = ginkgo.It("RunnerNamedPathPrefetchDoesNotRunForWriteBrief", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("Idiom is a local coding-agent harness.\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write README fixture: %v", err), 1)
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": "Done."}}},
		})
	}))

	defer server.Close()

	runner := newRunnerRunFixture(&Runner{
		Client:    llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		Policy:    RunPolicy{MaxToolSteps: 1, MaxNoProgressTurns: 1},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(autonomy.TaskFamilyImplementation),
			Contract:          string(workerruntime.ContractImplementationResult),
			Objective:         "Read README.md and update it.",
			PermissionProfile: string(workerruntime.PermissionGuardedWorkspaceWrite),
			AllowedTools:      []string{"read_file", "edit_file", "write_file"},
		},
	})

	var (
		prefetchResults int
		prefetchEvent   *RunEvent
	)

	runner.runner.OnEvent = func(event RunEvent) {
		if event.Role == readOnlyPrefetchEventRole {
			copy := event

			prefetchEvent = &copy
		}
	}
	_, _ = runner.runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: runner.state.runtimeBrief.Objective}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool == workspace.ToolNamePrefetch {
				prefetchResults++
			}
		},
	)

	if prefetchResults != 0 {
		ginkgo.Fail(fmt.Sprintf("write brief must not receive prefetch result, got %d", prefetchResults), 1)
	}

	if prefetchEvent == nil {
		ginkgo.Fail("expected metadata prefetch event", 1)
	}

	var payload struct {
		Eligible   bool   `json:"eligible"`
		MissReason string `json:"miss_reason"`
	}

	if err := json.Unmarshal([]byte(prefetchEvent.Content), &payload); err != nil {
		ginkgo.Fail(fmt.Sprintf("unmarshal prefetch event: %v", err), 1)
	}

	if payload.Eligible || payload.MissReason != "not_read_only_workspace" {
		ginkgo.Fail(fmt.Sprintf("unexpected prefetch metadata: %#v", payload), 1)
	}

	if requests == 0 {
		ginkgo.Fail("expected the runner to reach the model", 1)
	}
})

var _ = ginkgo.It("RunnerNamedPathPrefetchRunsForReadOnlyArchitectureBrief", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("Idiom carries cache metrics through session summaries.\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write README fixture: %v", err), 1)
	}

	replies := []string{
		"README.md shows that Idiom carries cache metrics through session summaries and uses that reporting flow in this fixture. Source: README.md.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()
		if requests >= len(replies) {
			ginkgo.Fail(fmt.Sprintf("unexpected extra chat request #%d", requests), 1)
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))

	defer server.Close()

	runner := newRunnerRunFixture(&Runner{
		Client:    llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 3},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(autonomy.TaskFamilyArchitecture),
			Contract:          string(workerruntime.ContractArchitecture),
			Objective:         "Read README.md and explain how cache metrics flow.",
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
			AllowedTools:      []string{"read_file", "search_content"},
		},
	})

	var (
		prefetchEvent   *RunEvent
		prefetchResults int
		readFileResults int
	)

	runner.runner.OnEvent = func(event RunEvent) {
		if event.Role == readOnlyPrefetchEventRole {
			copy := event

			prefetchEvent = &copy
		}
	}

	answer, err := runner.runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: runner.state.runtimeBrief.Objective}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			switch event.Result.Tool {
			case workspace.ToolNamePrefetch:
				prefetchResults++
			case "read_file":
				readFileResults++
			}
		},
	)
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("Run: %v", err), 1)
	}

	if !strings.Contains(answer, "README.md") || !strings.Contains(answer, "Idiom carries cache metrics") {
		ginkgo.Fail(fmt.Sprintf("expected grounded README answer citing source and fixture content, got %q", answer), 1)
	}

	if prefetchEvent == nil {
		ginkgo.Fail("expected prefetch metadata event", 1)
	}

	var payload struct {
		Eligible      bool     `json:"eligible"`
		SelectedPaths []string `json:"selected_paths"`
	}

	if err := json.Unmarshal([]byte(prefetchEvent.Content), &payload); err != nil {
		ginkgo.Fail(fmt.Sprintf("unmarshal prefetch event: %v", err), 1)
	}

	if !payload.Eligible || !slices.Contains(payload.SelectedPaths, "README.md") {
		ginkgo.Fail(fmt.Sprintf("unexpected prefetch metadata: %#v", payload), 1)
	}

	if prefetchResults != 1 {
		ginkgo.Fail(fmt.Sprintf("expected one prefetch result, got %d", prefetchResults), 1)
	}

	if readFileResults != 0 {
		ginkgo.Fail(fmt.Sprintf("prefetch should avoid model-initiated read_file before final answer, got %d", readFileResults), 1)
	}

	if requests != len(replies) {
		ginkgo.Fail(fmt.Sprintf("expected %d model requests, got %d", len(replies), requests), 1)
	}
})

var _ = ginkgo.It("RunnerDirectAnswerSurfaceAllowsSearchThenBoundedRead", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("Idiom sells repo memory, freshness, contracts, audit, and evals.\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write README fixture: %v", err), 1)
	}

	replies := []string{
		`{"tool":"search_content","path":".","pattern":"repo memory"}`,
		`{"tool":"read_file","path":"README.md","start_line":1,"end_line":1}`,
		"Idiom sells repo memory, freshness, contracts, audit, and evals. Source: README.md.",
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()
		if requests >= len(replies) {
			ginkgo.Fail(fmt.Sprintf("unexpected extra chat request #%d", requests), 1)
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))

	defer server.Close()

	runner := newRunnerRunFixture(&Runner{
		Client:    llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 5, MaxNoProgressTurns: 3},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(autonomy.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			Objective:         "Find the repo-memory claim and answer what Idiom sells.",
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
			AllowedTools:      []string{"search_content", "read_file", "write_file", "submit_plan"},
		},
	})

	var (
		searchResults int
		readResults   int
	)

	answer, err := runner.runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: runner.state.runtimeBrief.Objective}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Error != "" {
				return
			}

			switch event.Result.Tool {
			case "search_content":
				searchResults++
			case "read_file":
				readResults++
			}
		},
	)
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("Run: %v", err), 1)
	}

	if searchResults != 1 || readResults != 1 {
		ginkgo.Fail(fmt.Sprintf("expected one search_content and one read_file result, got search=%d read=%d", searchResults, readResults), 1)
	}

	if !strings.Contains(answer, "README.md") || !strings.Contains(answer, "Idiom sells repo memory") {
		ginkgo.Fail(fmt.Sprintf("expected final answer grounded in README.md, got %q", answer), 1)
	}

	if requests != len(replies) {
		ginkgo.Fail(fmt.Sprintf("expected %d model requests, got %d", len(replies), requests), 1)
	}
})

func testDirectAnswerSurfaceBrief() workerruntime.WorkerRuntimeBrief {
	return workerruntime.WorkerRuntimeBrief{
		TaskFamily:        string(autonomy.TaskFamilyDirectAnswer),
		Contract:          string(workerruntime.ContractDirectAnswer),
		PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
	}
}

func generalReadOnlyDiagnosticToolRegressionSet() []string {
	return []string{
		"read_only_capability",
		"harness_audit",
		"context_status",
		"history_range",
		"history_symbol",
		"blame_range",
	}
}

var _ = ginkgo.It("ReadOnlyWeakDiscoveryNextActionNamesMissingSourceOwner", func() {
	brief := testDirectAnswerSurfaceBrief()

	brief.Objective = "what would it take to enable arrow-key history recall in the TUI chat box?"
	brief.AllowedTools = []string{"context_pack", "context_query", "search_content", "read_file", "code_navigate"}

	req := workspace.ToolRequest{Tool: "context_pack", Task: "implement arrow-key history recall in TUI chat input", Mode: "plan"}
	result := workspace.ToolResult{
		Tool:    "context_pack",
		Summary: "Context pack wrote view with candidate file(s)",
		Facts: contextPackCandidateFactsForTest(
			"internal/runtime/agent/runner_core.go",
			"internal/surfaces/tui/app/rendering.go",
		),
	}

	state, _ := buildRunnerNextActionStateWithAllowedToolsAndContract("main", 1, "tool_result", req, result, []workspace.ToolResult{result}, false, brief, brief.AllowedTools)
	if len(state.EvidencePaths) != 0 {
		ginkgo.Fail(fmt.Sprintf("candidate-only discovery must not be promoted to source-body evidence paths: %#v", state.EvidencePaths), 1)
	}

	joinedStatus := strings.Join(state.EvidenceStatus, "\n")
	if !strings.Contains(joinedStatus, "candidate-only") || !strings.Contains(joinedStatus, "source owner") {
		ginkgo.Fail(fmt.Sprintf("weak discovery status should name the missing source-owner evidence, got %#v", state.EvidenceStatus), 1)
	}

	joinedNext := strings.Join(state.NextValidActions, "\n")
	for _, want := range []string{
		"source owner was not established by context_pack",
		"materially different source-owner or source-body probe",
		"exact lexical/symbol/source-owner probe",
	} {
		if !strings.Contains(joinedNext, want) {
			ginkgo.Fail(fmt.Sprintf("weak discovery next-action guidance missing %q: %#v", want, state.NextValidActions), 1)
		}
	}

	joinedInvalid := strings.Join(state.InvalidNextActions, "\n")
	if strings.Contains(joinedInvalid, "exact replay of the same weak discovery request") {
		ginkgo.Fail(fmt.Sprintf("weak discovery guidance must not turn model-owned discovery strategy into a phase restriction, got %#v", state.InvalidNextActions), 1)
	}
})

var _ = ginkgo.It("ReadOnlySourceOwnerDiscoveryNextActionRequiresSourceBody", func() {
	brief := testDirectAnswerSurfaceBrief()

	brief.Objective = "explain discovery consumption state"
	brief.AllowedTools = []string{"context_pack", "context_query", "search_content", "read_file", "code_navigate"}

	req := workspace.ToolRequest{Tool: "context_pack", Task: "runner discovery consumption"}
	result := workspace.ToolResult{
		Tool:    "context_pack",
		Summary: "Context pack wrote view with 1 candidate file(s)",
		Facts:   contextPackSourceOwnerFactsForTest("internal/runtime/agent/runner_core.go"),
	}

	state, _ := buildRunnerNextActionStateWithAllowedToolsAndContract("main", 1, "tool_result", req, result, []workspace.ToolResult{result}, false, brief, brief.AllowedTools)
	if len(state.EvidencePaths) != 0 {
		ginkgo.Fail(fmt.Sprintf("source-owner discovery still must not become source-body evidence paths: %#v", state.EvidencePaths), 1)
	}

	joinedNext := strings.Join(state.NextValidActions, "\n")
	if !strings.Contains(joinedNext, "inspect one returned path or handle with source-body evidence") {
		ginkgo.Fail(fmt.Sprintf("source-owner discovery should direct source-body consumption, got %#v", state.NextValidActions), 1)
	}

	joinedInvalid := strings.Join(state.InvalidNextActions, "\n")
	if strings.Contains(joinedInvalid, "repeated broad or identical rediscovery") {
		ginkgo.Fail(fmt.Sprintf("source-owner guidance must keep permission-authorized discovery callable, got %#v", state.InvalidNextActions), 1)
	}
})

var _ = ginkgo.It("ReadOnlyCompletionGateCandidateOnlyRecoveryNamesSourceBody", func() {
	brief := testDirectAnswerSurfaceBrief()

	brief.Objective = "What does ShortLabel return?"
	brief.AllowedTools = []string{"search_content", "read_file", "code_navigate"}

	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
	result, rejected := runner.completionGateRejection("ShortLabel returns Save changes.", []workspace.ToolResult{{
		Tool:    "search_content",
		Summary: "Found 1 matching line(s).",
		Output:  "internal/labels/labels.go:4:\treturn \"Save changes\"",
		Facts: &toolfacts.ToolResultFacts{
			CandidatePaths:      []string{"internal/labels/labels.go"},
			HasDiscoveryTargets: true,
			DiscoveryStrength:   toolfacts.DiscoveryStrengthSourceOwner,
			SourceOwnerFound:    true,
			SearchHitCount:      1,
		},
	}}, 1, nil, false)

	if !rejected {
		ginkgo.Fail("expected candidate-only read-only answer to be rejected", 1)
	}

	assessment, ok := sourceGroundingAssessmentFromResult(result)
	if !ok {
		ginkgo.Fail(fmt.Sprintf("expected typed candidate-only source-grounding assessment, got %#v", result), 1)
	}
	if assessment.Kind != sourceGroundingRejectionCandidateOnly {
		ginkgo.Fail(fmt.Sprintf("source-grounding kind = %q, want %q", assessment.Kind, sourceGroundingRejectionCandidateOnly), 1)
	}
	if paths := assessment.ReadableRecoveryPaths(); !slices.Equal(paths, []string{"internal/labels/labels.go"}) {
		ginkgo.Fail(fmt.Sprintf("read-body recovery paths = %#v, want candidate source path", paths), 1)
	}

	for _, want := range []string{
		sourceGroundingCandidateOnlyMarker,
		"internal/labels/labels.go",
		"read one body-readable file/document",
	} {
		if !strings.Contains(result.Error, want) {
			ginkgo.Fail(fmt.Sprintf("candidate-only recovery missing %q:\n%s", want, result.Error), 1)
		}
	}

	if strings.Contains(result.Error, "Use one targeted read/search/discovery tool") {
		ginkgo.Fail(fmt.Sprintf("candidate-only recovery should not fall back to generic discovery advice:\n%s", result.Error), 1)
	}

	if _, rejected := runner.completionGateRejection("<<QUESTION>>: Cannot answer until source-body evidence is available.", []workspace.ToolResult{{
		Tool:    "search_content",
		Summary: "Found 1 matching line(s).",
		Output:  "internal/labels/labels.go:4:\treturn \"Save changes\"",
		Facts: &toolfacts.ToolResultFacts{
			CandidatePaths:      []string{"internal/labels/labels.go"},
			HasDiscoveryTargets: true,
			DiscoveryStrength:   toolfacts.DiscoveryStrengthSourceOwner,
			SourceOwnerFound:    true,
			SearchHitCount:      1,
		},
	}}, 1, nil, false); rejected {
		ginkgo.Fail("candidate-only recovery must not reject an explicit blocker terminal outcome", 1)
	}
})

var _ = ginkgo.It("ReadOnlyCompletionGateTypedDiagnosticLogCitationIgnoresStaleSearchCandidates", func() {
	brief := testDirectAnswerSurfaceBrief()
	brief.Objective = "RCA the most recent harness failure"
	brief.AllowedTools = []string{"search_files", "read_file", "search_content"}

	logPath := "logs/conversations/20260628-081924.685.jsonl"
	results := []workspace.ToolResult{
		{
			Tool:    "search_files",
			Summary: "No file matches for \"*.jsonl\". Closest paths in this workspace: f1 internal/platform/jsonl/jsonl.go, f2 testdata/product-workflows/prd-adr-brief-workflow/evidence/evidence.jsonl.",
			Output:  "",
		},
		diagnosticLogReadResultForTest(
			"artifacts/runtime/20260628-081924.685.jsonl",
			logPath,
			4300,
			4420,
			4641,
			true,
			"4420:{\"role\":\"completion_gate_recovery\"}\n",
		),
	}
	answer := "The latest failure was a repeated completion-gate rejection in `" + logPath + "` after stale search candidates polluted the source-grounding ledger."

	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
	if reason := runner.assessSourceGrounding(answer, results).Message(); reason != "" {
		ginkgo.Fail(fmt.Sprintf("read diagnostic log citation should satisfy source grounding for the cited log, got %q", reason), 1)
	}

	result, rejected := runner.completionGateRejection(answer, results, 1, nil, false)
	if rejected {
		ginkgo.Fail(fmt.Sprintf("stale search candidates must not reject an answer grounded in a read diagnostic log, got: %s", result.Error), 1)
	}

	externalPath := "/Users/ben/.idiom/logs/conversations/20260628-081924.685.jsonl"
	untypedExternal := []workspace.ToolResult{readFileSourceWindowResult(externalPath, 4300, 4420, 4641, "rendered log body")}
	externalAnswer := "The latest failure is recorded in `" + externalPath + "`."
	if reason := runner.assessSourceGrounding(externalAnswer, untypedExternal).Message(); reason == "" {
		ginkgo.Fail("outside-workspace log output without a producer diagnostic fact must not satisfy source grounding", 1)
	}
})

var _ = ginkgo.It("ReadOnlyCompletionGateDiagnosticLogCitationDoesNotSatisfySourceFailureQuestion", func() {
	brief := testDirectAnswerSurfaceBrief()
	brief.Objective = "Why did the runner build fail?"
	brief.AllowedTools = []string{"search_files", "read_file", "search_content"}

	logPath := "logs/conversations/20260628-081924.685.jsonl"
	results := []workspace.ToolResult{
		{
			Tool:    "search_files",
			Summary: "Found candidate path internal/runtime/agent/runner_core.go",
			Output:  "internal/runtime/agent/runner_core.go",
			Facts: &toolfacts.ToolResultFacts{
				CandidatePaths:      []string{"internal/runtime/agent/runner_core.go"},
				HasDiscoveryTargets: true,
				DiscoveryStrength:   toolfacts.DiscoveryStrengthSourceOwner,
				SourceOwnerFound:    true,
			},
		},
		{
			Tool:    "read_file",
			Summary: "Read f7 " + logPath + " lines 1-72 (file has 72 lines total)",
			Output:  "1:{\"type\":\"session_start\"}\n72:{\"type\":\"final\"}\n",
		},
	}
	answer := "The runner build failed according to `" + logPath + "`."

	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
	result, rejected := runner.completionGateRejection(answer, results, 1, nil, false)
	if !rejected {
		ginkgo.Fail("source/build failure questions must still require source-body or relevant diagnostic evidence", 1)
	}
	if !strings.Contains(result.Error, "Candidate/search/context paths are not source-body evidence") ||
		!strings.Contains(result.Error, "internal/runtime/agent/runner_core.go") {
		ginkgo.Fail(fmt.Sprintf("expected diagnostic log read to remain insufficient for source/build answer, got: %s", result.Error), 1)
	}
})

var _ = ginkgo.It("DecorateFinalAnswerKeepsBlockerWhenRequiredPathsRemaining", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Constraints:       []string{"planner_owned_verification"},
		RequiredEditPaths: []string{"a.go", "b.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited a.go", ChangedPaths: []string{"a.go"}},
		{
			Tool:    "apply_patch",
			Summary: "Patch hunk did not apply",
			Error:   "Update File a.go: hunk 1 context did not match the file",
		},
	}
	answer := "Changed paths:\n- a.go\n\nBlocker:\n- Patch hunk did not apply on a.go"

	runner := newRunnerRunFixture(&Runner{})
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     answer,
		StopReason: "tool_step_budget_exhausted",
		Brief:      brief,
		Results:    results,
		UserFacing: true,
	})
	// Required path b.go remains unchanged; the strip helper must NOT
	// fire (otherwise we'd hide a real blocker). Caller should still
	// see the model's blocker prose.
	if !strings.Contains(strings.ToLower(got), "blocker") {
		ginkgo.Fail(fmt.Sprintf("decorator must preserve Blocker when required paths remain, got:\n%s", got), 1)
	}
})

var _ = ginkgo.It("RepeatedPlanCloseoutProjectsUserSafeSubmitCorrectionStatus", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read a.txt lines 1-10 (total lines: 10)"},
		{Tool: "read_file", Summary: "Read b.txt lines 1-10 (total lines: 10)"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[1]: edit would not change b.txt; for b.txt: remove no-op edit; use `source_satisfied_paths`: [\"b.txt\"] if final; use `Decision: source-satisfied` only if all paths are final; else submit real edit",
		},
	}
	runner := newRunnerRunFixture(&Runner{RuntimeBrief: brief})
	got := runner.completionGateRepeatedBlockerAnswer(workspace.ToolResult{
		Tool: "completion_gate",
		Code: "plan_then_execute_submit_plan_coverage_mismatch",
		Facts: &toolfacts.ToolResultFacts{
			DeferredPlanPaths: []string{"b.txt"},
		},
	}, results)

	for _, want := range []string{
		"Plan-execute submit correction is still required",
		"submit_plan",
		"source_satisfied_paths",
	} {
		if !strings.Contains(got, want) {
			ginkgo.Fail(fmt.Sprintf("decorated plan-execute blocker missing %q:\n%s", want, got), 1)
		}
	}

	for _, forbidden := range []string{
		"Implementation incomplete",
		"Changed paths: none",
		"Missing completion evidence",
		"Completion gate",
		"gate retry",
		"2/3",
	} {
		if strings.Contains(got, forbidden) {
			ginkgo.Fail(fmt.Sprintf("decorated plan-execute blocker used wrong surface or leaked %q:\n%s", forbidden, got), 1)
		}
	}
})

var _ = ginkgo.It("DecorateFinalAnswerKeepsLegitimateMutationQuestionBlocker", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go"},
	}
	answer := "<<QUESTION>>: Cannot edit a.go because the permission policy denied edit_file for that path."

	runner := newRunnerRunFixture(&Runner{})
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     answer,
		StopReason: "model_final",
		Brief:      brief,
		UserFacing: true,
	})

	if got != answer {
		ginkgo.Fail(fmt.Sprintf("legitimate mutation blocker should pass through unchanged, got:\n%s", got), 1)
	}
})

var _ = ginkgo.It("RunnerSanitizesRepeatedMutationCompletionGateBlockerAfterUnsupportedSourceSanity", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	if err := writeTestFile(root, "src/pages/contact.astro", "---\n---\n<h1>Contact</h1>\n"); err != nil {
		ginkgo.Fail(fmt.Sprintf("write fixture: %v", err), 1)
	}

	internalBlocker := strings.Join([]string{
		"<<QUESTION>>: Completion gate rejected the final answer repeatedly before the required completion evidence was produced.",
		"",
		"Changed paths:",
		"- src/pages/contact.astro",
		"",
		"Missing completion evidence: [gate retry 2/3] workspace changes are present, but the latest successful write is not followed by successful verification.",
	}, "\n")
	replies := []string{
		`{"tool":"read_file","path":"src/pages/contact.astro","start_line":1,"end_line":20,"named_gap":"current contact page before edit"}`,
		`{"tool":"edit_file","path":"src/pages/contact.astro","old_text":"<h1>Contact</h1>","new_text":"<h1>Contact us</h1>","description":"update contact heading"}`,
		`{"tool":"source_sanity","path":"src/pages/contact.astro"}`,
		internalBlocker,
		internalBlocker,
		internalBlocker,
	}

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := replies[len(replies)-1]
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

	var results []workspace.ToolResult
	runner := newRunnerRunFixture(&Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			AllowedTools:         []string{"read_file", "edit_file", "source_sanity"},
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
			RequiredEditPaths:    []string{"src/pages/contact.astro"},
			EvidencePaths:        []string{"src/pages/contact.astro"},
		},
	})

	answer, err := runner.runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update the contact page heading."}},
		llama.ChatOptions{MaxTokens: 4096},
		nil,
		nil,
		func(event ToolResultEvent) {
			results = append(results, event.Result)
		},
	)
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("Run: %v", err), 1)
	}

	for _, want := range []string{
		"Changed paths:",
		"src/pages/contact.astro",
		"Remaining obligations:",
		"verification pending after recorded workspace write(s)",
		"verification failed after the latest workspace write",
	} {
		if !strings.Contains(answer, want) {
			ginkgo.Fail(fmt.Sprintf("runner answer missing %q:\n%s", want, answer), 1)
		}
	}

	for _, forbidden := range []string{
		"Completion gate",
		"Missing completion evidence",
		"required completion evidence",
		"gate retry",
		"2/3",
	} {
		if strings.Contains(answer, forbidden) {
			ginkgo.Fail(fmt.Sprintf("runner answer leaked internal phrase %q:\n%s", forbidden, answer), 1)
		}
	}

	sawUnsupportedSourceSanity := false
	for _, result := range results {
		if result.Tool == "source_sanity" && strings.Contains(result.Error, "src/pages/contact.astro is not a supported source file") {
			sawUnsupportedSourceSanity = true
		}
	}
	if !sawUnsupportedSourceSanity {
		ginkgo.Fail(fmt.Sprintf("expected unsupported source_sanity result, got %#v", results), 1)
	}
})

var _ = ginkgo.It("DecorateFinalAnswerKeepsPlanExecuteSourceSatisfiedAnswer", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go", "b.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-10"},
		{Tool: "read_file", Summary: "Read f2 b.go lines 1-10"},
	}
	answer := "Decision: source-satisfied\n- a.go: already implemented; no changes needed.\n- b.go: already implemented; no changes needed."

	runner := newRunnerRunFixture(&Runner{})
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     answer,
		StopReason: "model_stop",
		Brief:      brief,
		Results:    results,
		UserFacing: true,
	})

	if strings.Contains(got, "Required edit path") {
		ginkgo.Fail(fmt.Sprintf("decorator must not rewrite source-satisfied plan-execute answer into a blocker, got:\n%s", got), 1)
	}

	if !strings.Contains(got, "source-satisfied") {
		ginkgo.Fail(fmt.Sprintf("decorator should preserve source-satisfied answer, got:\n%s", got), 1)
	}
})

var _ = ginkgo.It("DecorateFinalAnswerKeepsBlockerWhenVerificationNotPlannerOwned", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited a.go", ChangedPaths: []string{"a.go"}},
		{
			Tool:    "apply_patch",
			Summary: "Patch hunk did not apply",
			Error:   "Update File a.go: hunk 1 context did not match the file",
		},
	}
	answer := "Changed paths:\n- a.go\n\nBlocker:\n- Patch hunk did not apply"

	runner := newRunnerRunFixture(&Runner{})
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     answer,
		StopReason: "tool_step_budget_exhausted",
		Brief:      brief,
		Results:    results,
		UserFacing: true,
	})

	if !strings.Contains(strings.ToLower(got), "blocker") {
		ginkgo.Fail(fmt.Sprintf("decorator should preserve Blocker when verification isn't planner-owned, got:\n%s", got), 1)
	}
})

var _ = ginkgo.It("DecorateFinalAnswerKeepsBlockerWhenLastFailureIsNonWriteTool", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Constraints:       []string{"planner_owned_verification"},
		RequiredEditPaths: []string{"a.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited a.go", ChangedPaths: []string{"a.go"}},
		{
			Tool:    "shell",
			Summary: "shell command failed",
			Error:   "exit status 1",
		},
	}
	answer := "Changed paths:\n- a.go\n\nBlocker:\n- shell command failed"

	runner := newRunnerRunFixture(&Runner{})
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     answer,
		StopReason: "tool_step_budget_exhausted",
		Brief:      brief,
		Results:    results,
		UserFacing: true,
	})

	if !strings.Contains(strings.ToLower(got), "blocker") {
		ginkgo.Fail(fmt.Sprintf("decorator should preserve Blocker when last failure was non-write tool, got:\n%s", got), 1)
	}
})

var _ = ginkgo.It("ExactEvidenceSymbolSetAcceptsRoutedCodeNavigateSourceWindow", func() {
	results := []workspace.ToolResult{
		codeNavigateSourceWindowResult(
			"internal/surfaces/tui/app/rendering.go",
			459,
			520,
			0,
			strings.Join([]string{
				"capability: read_symbol",
				"route_summary: Read Model.renderMessages in internal/surfaces/tui/app/rendering.go lines 459-520",
				"459:func (m *Model) renderMessages() {",
				"460:\tm.messagesViewport.SetContent(rendered)",
				"461:}",
			}, "\n"),
		),
		codeNavigateSourceWindowResult(
			"internal/runtime/agent/runner_delegated_synthesis.go", 1333, 1345, 1345,
			"func indexFold(value string, needle string) int {\n\tif strings.EqualFold(value, needle) {\n\t\treturn 0\n\t}\n\treturn -1\n}\n",
		),
	}

	symbols := ExactEvidenceSymbolSet(results)

	for _, want := range []string{"Model.renderMessages", "SetContent", "indexFold", "strings.EqualFold"} {
		key := NormalizedAnswerSymbol(want)
		if _, ok := symbols[key]; !ok {
			ginkgo.Fail(fmt.Sprintf("exact evidence symbol set missing %q (%q): %#v", want, key, symbols), 1)
		}
	}

	if key := NormalizedAnswerSymbol("0xb1"); key != "" {
		ginkgo.Fail(fmt.Sprintf("numeric literals must not be treated as exact source symbols, got %q", key), 1)
	}
})

var _ = ginkgo.It("DecorateFinalAnswerDoesNotRewriteNormalReadOnlyAnswer", func() {
	runner := newRunnerRunFixture(&Runner{})
	answer := "The inspected source shows that rendering delegates wrapping to the layout width calculation."
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer: answer,
		Brief: workerruntime.WorkerRuntimeBrief{
			Contract:          string(workerruntime.ContractDirectAnswer),
			TaskFamily:        string(autonomy.TaskFamilyDirectAnswer),
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
		},
		UserFacing: true,
	})

	if got != answer {
		ginkgo.Fail(fmt.Sprintf("normal read-only answer should pass through unchanged, got:\n%s", got), 1)
	}
})

var _ = ginkgo.It("DecorateFinalAnswerPreservesPlanExecuteReviewRepairQuestionBlocker", func() {
	runner := newRunnerRunFixture(&Runner{})
	answer := "<<QUESTION>>: Cannot repair because evidence is unavailable."
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer: answer,
		Brief: workerruntime.WorkerRuntimeBrief{
			Contract:          "review_findings",
			TaskFamily:        string(autonomy.TaskFamilyPlanThenExecuteReview),
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
		},
		UserFacing: true,
	})

	if got != answer {
		ginkgo.Fail(fmt.Sprintf("plan-execute review-repair blocker must remain machine-readable, got:\n%s", got), 1)
	}
})

var _ = ginkgo.It("PresentableUnsupportedFinalAnswerLeadMatchesTUIContract", func() {
	got := presentableUnsupportedFinalAnswerLead()
	for _, want := range []string{
		"I do not have a supported answer",
	} {
		if !strings.Contains(got, want) {
			ginkgo.Fail(fmt.Sprintf("unsupported final-answer lead missing %q:\n%s", want, got), 1)
		}
	}
	for _, forbidden := range []string{"could not produce", "final answer", "run stopped before", "not enough supported evidence"} {
		if strings.Contains(got, forbidden) {
			ginkgo.Fail(fmt.Sprintf("unsupported final-answer lead leaked %q:\n%s", forbidden, got), 1)
		}
	}
})

var _ = ginkgo.It("StripInternalEvidenceAppendixStripsSourceLine", func() {
	answer := "The implementation lives in internal/surfaces/tui/app/history.go.\n\nThe key function is RenderHistory.\n\n" +
		"Source: delegated read-only evidence packet."

	stripped := stripInternalEvidenceAppendix(answer)
	if strings.Contains(stripped, "Source:") {
		ginkgo.Fail(fmt.Sprintf("stripInternalEvidenceAppendix should strip lines starting with Source:\ngot: %s", stripped), 1)
	}

	if !strings.Contains(stripped, "internal/surfaces/tui/app/history.go") {
		ginkgo.Fail(fmt.Sprintf("stripInternalEvidenceAppendix dropped substantive content:\ngot: %s", stripped), 1)
	}
})

var _ = ginkgo.It("FinalAnswerPolicyAcceptsWithTypedMetadata", func() {
	t := ginkgo.GinkgoT()
	acceptance := newFinalAnswerAcceptance()

	if acceptance.Decision != finalAnswerAcceptanceAccepted || acceptance.PolicyDecision != autonomy.PolicyAllow {
		ginkgo.Fail(fmt.Sprintf("expected accepted policy, got %#v", acceptance), 1)
	}
	assertFinalAnswerPolicyMetadata(t, acceptance, autonomy.PolicyAllow)
	if acceptance.ParityReport.SurfaceID != "final_answer_acceptance" ||
		acceptance.ParityReport.InvariantID != finalAnswerPolicyInvariantAcceptance ||
		acceptance.ParityReport.Diverged {

		ginkgo.Fail(fmt.Sprintf("missing report-only parity metadata: %#v", acceptance.ParityReport), 1)
	}
})

var _ = ginkgo.It("FinalAnswerAcceptanceEventFieldsIncludeParityReport", func() {
	acceptance := newFinalAnswerAcceptance()
	acceptance.ParityReport.ContractID = "contract-1"
	acceptance.ParityReport.ProtocolHash = "protocol-1"

	fields := finalAnswerAcceptanceEventFields(acceptance)
	if fields["parity_contract_id"] != "contract-1" ||
		fields["parity_protocol_hash"] != "protocol-1" ||
		fields["parity_surface_id"] != "final_answer_acceptance" ||
		fields["parity_invariant_id"] != finalAnswerPolicyInvariantAcceptance ||
		fields["parity_old_decision"] != "accept" ||
		fields["parity_new_decision"] != "accept" ||
		fields["parity_diverged"] != false {

		ginkgo.Fail(fmt.Sprintf("event fields missing parity report: %#v", fields), 1)
	}
})

var _ = ginkgo.It("FinalAnswerAcceptanceEventFieldsIncludeTypedAssessment", func() {
	acceptance := finalAnswerAcceptanceWithRelevanceForTest(readOnlyDirectAnswerRelevance{
		BlockerKind:             "answer_objective_unmet",
		Reason:                  "objective evidence missing",
		RelevantSourceBodyPaths: []string{"internal/runtime/agent/runner_core.go"},
	})
	sourceGroundingAssessment := sourceGroundingCandidateOnlyAssessment([]string{"internal/runtime/agent/implementation_flow.go"})
	acceptance = rejectFinalAnswerAcceptanceWithSourceGrounding(acceptance, sourceGroundingAssessment)

	fields := finalAnswerAcceptanceEventFields(acceptance)
	diagnostics, ok := fields["contract_diagnostics"].([]map[string]any)
	if !ok || len(diagnostics) != 1 {
		ginkgo.Fail(fmt.Sprintf("expected one typed diagnostic event field, got %#v", fields["contract_diagnostics"]), 1)
	}
	if diagnostics[0]["kind"] != string(turnDiagnosticObjectiveRelevance) ||
		diagnostics[0]["reason_code"] != "answer_objective_unmet" {
		ginkgo.Fail(fmt.Sprintf("unexpected diagnostic field: %#v", diagnostics[0]), 1)
	}

	violations, ok := fields["contract_violations"].([]map[string]any)
	if !ok || len(violations) != 1 {
		ginkgo.Fail(fmt.Sprintf("expected one typed violation event field, got %#v", fields["contract_violations"]), 1)
	}
	if violations[0]["kind"] != string(turnViolationSourceGroundingUnmet) ||
		violations[0]["reason_code"] != string(turnReasonSourceGroundingUnmet) ||
		violations[0]["policy_pack_id"] != string(autonomy.ContractPackFinalAnswerAcceptance) {
		ginkgo.Fail(fmt.Sprintf("unexpected violation field: %#v", violations[0]), 1)
	}
	if violations[0]["message"] != sourceGroundingAssessment.Message() {
		ginkgo.Fail(fmt.Sprintf("violation should carry exact source-grounding message: %#v", violations[0]), 1)
	}
	references, ok := violations[0]["source_grounding_references"].([]sourceReference)
	if !ok || !slices.Contains(sourceReferencePaths(references), "internal/runtime/agent/implementation_flow.go") {
		ginkgo.Fail(fmt.Sprintf("violation should carry typed source-grounding references: %#v", violations[0]), 1)
	}
	resolutions, ok := violations[0]["source_grounding_resolutions"].([]sourceGroundingRecoveryResolution)
	if !ok || len(resolutions) == 0 {
		ginkgo.Fail(fmt.Sprintf("violation should carry typed source-grounding resolutions: %#v", violations[0]), 1)
	}
})

var _ = ginkgo.It("FinalAnswerPolicyRejectsRequiredEditPathsWithTypedMetadata", func() {
	t := ginkgo.GinkgoT()
	acceptance := rejectFinalAnswerAcceptance(newFinalAnswerAcceptance(), turnViolationRequiredEditPathsRemaining, turnReasonRequiredEditPathsRemaining)

	if acceptance.Decision != finalAnswerAcceptanceRejected ||
		acceptance.BlockerKind != "required_edit_paths_remaining" ||
		acceptance.PolicyDecision != autonomy.PolicyReject {

		ginkgo.Fail(fmt.Sprintf("expected required-path rejection, got %#v", acceptance), 1)
	}
	assertFinalAnswerPolicyMetadata(t, acceptance, autonomy.PolicyReject)
})

var _ = ginkgo.It("FinalAnswerPolicyRejectsGroundingAndKeepsObjectiveAdvisory", func() {
	t := ginkgo.GinkgoT()
	acceptance := finalAnswerAcceptanceWithRelevanceForTest(readOnlyDirectAnswerRelevance{
		BlockerKind:             "answer_objective_unmet",
		Reason:                  "objective evidence missing",
		RelevantSourceBodyPaths: []string{"internal/runtime/agent/runner_core.go"},
	})
	sourceGroundingAssessment := sourceGroundingUnsupportedDetailReferenceAssessment(sourceReferencesForPaths([]string{"internal/runtime/agent/source_grounding.go"}))
	acceptance = rejectFinalAnswerAcceptanceWithSourceGrounding(acceptance, sourceGroundingAssessment)

	if acceptance.BlockerKind != "source_grounding_unmet" ||
		acceptance.Reason != sourceGroundingAssessment.Message() ||
		acceptance.PolicyDecision != autonomy.PolicyReject {

		ginkgo.Fail(fmt.Sprintf("expected grounding rejection with advisory objective metadata, got %#v", acceptance), 1)
	}
	if len(acceptance.EvidencePaths) != 1 || acceptance.EvidencePaths[0] != "internal/runtime/agent/runner_core.go" {
		ginkgo.Fail(fmt.Sprintf("expected fallback evidence path, got %#v", acceptance.EvidencePaths), 1)
	}
	if !acceptance.ContractAssessment.HasViolation(turnViolationSourceGroundingUnmet) ||
		!acceptance.ContractAssessment.HasDiagnostic(turnDiagnosticObjectiveRelevance) {
		ginkgo.Fail(fmt.Sprintf("expected source-grounding violation and objective diagnostic, got %#v", acceptance.ContractAssessment), 1)
	}
	assertFinalAnswerPolicyMetadata(t, acceptance, autonomy.PolicyReject)
})

var _ = ginkgo.It("FinalAnswerPolicyTreatsObjectiveRelevanceAsAdvisory", func() {
	t := ginkgo.GinkgoT()
	acceptance := finalAnswerAcceptanceWithRelevanceForTest(readOnlyDirectAnswerRelevance{
		Status:                  "checked",
		BlockerKind:             "answer_objective_unmet:citation_mismatch",
		Reason:                  "objective evidence mismatch",
		RelevantSourceBodyPaths: []string{"internal/runtime/agent/runner_core.go"},
	})

	if acceptance.Decision != finalAnswerAcceptanceAccepted ||
		acceptance.PolicyDecision != autonomy.PolicyAllow ||
		acceptance.BlockerKind != "" {
		ginkgo.Fail(fmt.Sprintf("objective relevance should be advisory-only, got %#v", acceptance), 1)
	}
	if acceptance.ObjectiveRelevanceStatus != "checked" ||
		len(acceptance.RelevantSourceBodyPaths) != 1 {
		ginkgo.Fail(fmt.Sprintf("objective relevance advisory metadata should be retained, got %#v", acceptance), 1)
	}
	if !acceptance.ContractAssessment.HasDiagnostic(turnDiagnosticObjectiveRelevance) ||
		len(acceptance.ContractAssessment.Violations) > 0 {
		ginkgo.Fail(fmt.Sprintf("expected objective diagnostic without enforceable violations, got %#v", acceptance.ContractAssessment), 1)
	}
	assertFinalAnswerPolicyMetadata(t, acceptance, autonomy.PolicyAllow)
})

func finalAnswerAcceptanceWithRelevanceForTest(relevance readOnlyDirectAnswerRelevance) finalAnswerAcceptance {
	acceptance := newFinalAnswerAcceptance()
	acceptance.EvidencePaths = append([]string(nil), relevance.RelevantSourceBodyPaths...)
	acceptance = finalAnswerAcceptanceWithDirectAnswerRelevance(acceptance, relevance)
	return finalAnswerAcceptanceWithPolicy(acceptance)
}

var _ = ginkgo.It("FinalAnswerAcceptanceTerminalOverrideKeepsPolicyDecisionConsistent", func() {
	t := ginkgo.GinkgoT()
	rejected := finalAnswerAcceptanceWithTerminalOverride(
		newFinalAnswerAcceptance(),
		finalAnswerAcceptanceRejected,
		"transport_protocol_output",
		"tool markup",
	)
	if rejected.PolicyDecision != autonomy.PolicyReject || rejected.Decision != finalAnswerAcceptanceRejected {
		ginkgo.Fail(fmt.Sprintf("rejected override must carry reject policy metadata: %#v", rejected), 1)
	}
	if !rejected.ContractAssessment.HasViolation(turnViolationTransportProtocolOutput) {
		ginkgo.Fail(fmt.Sprintf("rejected override must carry typed transport violation: %#v", rejected.ContractAssessment), 1)
	}
	assertFinalAnswerPolicyMetadata(t, rejected, autonomy.PolicyReject)

	accepted := finalAnswerAcceptanceWithTerminalOverride(
		rejected,
		finalAnswerAcceptanceAccepted,
		"",
		"read_only_terminal_recovery",
	)
	if accepted.PolicyDecision != autonomy.PolicyAllow ||
		accepted.Decision != finalAnswerAcceptanceAccepted ||
		accepted.BlockerKind != "" ||
		accepted.Reason != "read_only_terminal_recovery" {

		ginkgo.Fail(fmt.Sprintf("accepted override must carry allow policy metadata: %#v", accepted), 1)
	}
	if len(accepted.ContractAssessment.Violations) > 0 {
		ginkgo.Fail(fmt.Sprintf("accepted override must clear typed violations: %#v", accepted.ContractAssessment), 1)
	}
	assertFinalAnswerPolicyMetadata(t, accepted, autonomy.PolicyAllow)
})

func assertFinalAnswerPolicyMetadata(t ginkgo.FullGinkgoTInterface, acceptance finalAnswerAcceptance, decision autonomy.ToolPolicyDecision) {
	t.Helper()

	if acceptance.PackID != autonomy.ContractPackFinalAnswerAcceptance {
		ginkgo.Fail(fmt.Sprintf("PackID = %q, want %q", acceptance.PackID, autonomy.ContractPackFinalAnswerAcceptance), 1)
	}
	if acceptance.InvariantID != finalAnswerPolicyInvariantAcceptance {
		ginkgo.Fail(fmt.Sprintf("InvariantID = %q, want %q", acceptance.InvariantID, finalAnswerPolicyInvariantAcceptance), 1)
	}
	if acceptance.PolicyDecision != decision {
		ginkgo.Fail(fmt.Sprintf("PolicyDecision = %q, want %q", acceptance.PolicyDecision, decision), 1)
	}
}

var _ = ginkgo.It("NormalizeToolRequestSyntaxNativeToolCall", func() {
	rawArgs := `{"pattern":"needle","path":"internal","glob":"*.go"}`
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		ToolCalls: []llama.ToolCall{{
			Function: llama.ToolCallFunc{
				Name:      "search_content",
				Arguments: rawArgs,
			},
		}},
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("NormalizeToolRequestSyntax: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected native tool call to normalize", 1)
	}

	if result.Request.Tool != "search_content" || result.Request.Pattern != "needle" || result.Request.Path != "internal" {
		ginkgo.Fail(fmt.Sprintf("unexpected request: %#v", result.Request), 1)
	}

	if result.Raw != rawArgs {
		ginkgo.Fail(fmt.Sprintf("Raw = %q, want %q", result.Raw, rawArgs), 1)
	}

	if result.ActiveToolCall == nil {
		ginkgo.Fail("expected active native tool call context", 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized || result.EventTransport != toolRequestNormalizerTransportNative {
		ginkgo.Fail(fmt.Sprintf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
	}

	input := runnerAutonomyPolicyInputFromNormalization(result)
	if input.Source != "tooling.intent" {
		ginkgo.Fail(fmt.Sprintf("policy input source = %q, want tooling.intent", input.Source), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxNestedCodeNavigateGraphResolutionDoesNotInferCapability", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `{"tool":"code_navigate","request":{"tool":"code_navigate","query":"task tool implementation","resolution":"symbol","include_evidence":"true"}}`,
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("NormalizeToolRequestSyntax: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected nested code_navigate request to normalize", 1)
	}

	if result.Request.Tool != "code_navigate" || result.Request.Query != "task tool implementation" || result.Request.Resolution != "symbol" || !result.Request.IncludeEvidence {
		ginkgo.Fail(fmt.Sprintf("unexpected request: %#v", result.Request), 1)
	}

	if result.Request.Capability != "" {
		ginkgo.Fail(fmt.Sprintf("graph resolution must not become code_navigate capability: %#v", result.Request), 1)
	}
})

var _ = ginkgo.It("ParseNativeToolCallRejectsMismatchedDeclaredTool", func() {
	_, ok, err := parseToolCall(llama.ToolCall{
		Function: llama.ToolCallFunc{
			Name:      "code_navigate",
			Arguments: `{"tool":"context_query","query":"task tool implementation","resolution":"symbol","include_evidence":true}`,
		},
	})

	if err == nil || ok {
		ginkgo.Fail(fmt.Sprintf("native tool mismatch should reject, ok=%t err=%v", ok, err), 1)
	}

	if !strings.Contains(err.Error(), `arguments declared tool "context_query"`) {
		ginkgo.Fail(fmt.Sprintf("unexpected mismatch error: %v", err), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxTextualJSON", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `{"tool":"read_file","path":"README.md","start_line":1,"end_line":20}`,
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("NormalizeToolRequestSyntax: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected textual JSON tool request to normalize", 1)
	}

	if result.Request.Tool != "read_file" || result.Request.Path != "README.md" || result.Request.StartLine != 1 || result.Request.EndLine != 20 {
		ginkgo.Fail(fmt.Sprintf("unexpected request: %#v", result.Request), 1)
	}

	if result.ActiveToolCall != nil {
		ginkgo.Fail("textual normalization must not create native tool-call context", 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized || result.EventTransport != toolRequestNormalizerTransportTextual {
		ginkgo.Fail(fmt.Sprintf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxDeepSeekDSMLReadFile", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `<｜｜DSML｜｜tool_calls>
<｜｜DSML｜｜invoke name="read_file">
<｜｜DSML｜｜parameter name="path" string="true">prompts/profiles/deepseek-v4-pro.md</｜｜DSML｜｜parameter>
<｜｜DSML｜｜parameter name="start_line" string="true">1</｜｜DSML｜｜parameter>
<｜｜DSML｜｜parameter name="end_line" string="true">80</｜｜DSML｜｜parameter>
</｜｜DSML｜｜invoke>
</｜｜DSML｜｜tool_calls>`,
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("NormalizeToolRequestSyntax: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected DSML read_file request to normalize", 1)
	}

	if result.Request.Tool != "read_file" ||
		result.Request.Path != "prompts/profiles/deepseek-v4-pro.md" ||
		result.Request.StartLine != 1 ||
		result.Request.EndLine != 80 {

		ginkgo.Fail(fmt.Sprintf("unexpected request: %#v", result.Request), 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized ||
		result.EventTransport != toolRequestNormalizerTransportXMLToolMarkup {

		ginkgo.Fail(fmt.Sprintf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxJanusCodeNavigate", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `<janus_tool_calls>
<janus_invoke name="code_navigate">
<janus_parameter name="capability" string="true">read_symbol</janus_parameter>
<janus_parameter name="path" string="true">internal/runtime/agent/runner_core.go</janus_parameter>
<janus_parameter name="pattern" string="true">Runner</janus_parameter>
</janus_invoke>
</janus_tool_calls>`,
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("NormalizeToolRequestSyntax: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected janus code_navigate request to normalize", 1)
	}

	if result.Request.Tool != "code_navigate" ||
		result.Request.Capability != "read_symbol" ||
		result.Request.Path != "internal/runtime/agent/runner_core.go" ||
		result.Request.Pattern != "Runner" {

		ginkgo.Fail(fmt.Sprintf("unexpected request: %#v", result.Request), 1)
	}

	if result.EventTransport != toolRequestNormalizerTransportXMLToolMarkup {
		ginkgo.Fail(fmt.Sprintf("transport=%q, want %q", result.EventTransport, toolRequestNormalizerTransportXMLToolMarkup), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxDeepSeekXMLWriteDoesNotNormalize", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `<n_tool_calls>
<n_invoke name="edit_file">
<n_parameter name="path" string="true">README.md</n_parameter>
<n_parameter name="old_text" string="true">old</n_parameter>
<n_parameter name="new_text" string="true">new</n_parameter>
</n_invoke>
</n_tool_calls>`,
	})

	if strings.TrimSpace(result.Request.Tool) != "" {
		ginkgo.Fail(fmt.Sprintf("DeepSeek XML write request must not normalize: %#v", result.Request), 1)
	}

	if result.ParseErr == nil || !strings.Contains(result.ParseErr.Error(), "not a read/search/discovery tool") {
		ginkgo.Fail(fmt.Sprintf("expected read-only XML parser error, got %v", result.ParseErr), 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeUnsafeToInfer ||
		result.EventTransport != toolRequestNormalizerTransportXMLToolMarkup {

		ginkgo.Fail(fmt.Sprintf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxReadFileLinesAlias", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `{"tool":"read_file","path":"logs/conversations/recent.jsonl","lines":"1-51"}`,
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("NormalizeToolRequestSyntax: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected textual JSON tool request to normalize", 1)
	}

	if result.Request.Tool != "read_file" || result.Request.Path != "logs/conversations/recent.jsonl" || result.Request.StartLine != 1 || result.Request.EndLine != 51 {
		ginkgo.Fail(fmt.Sprintf("unexpected request: %#v", result.Request), 1)
	}

	if !result.Repaired {
		ginkgo.Fail("expected lines alias repair to be marked repaired", 1)
	}

	for _, want := range []string{`"start_line":1`, `"end_line":51`} {
		if !strings.Contains(result.Raw, want) {
			ginkgo.Fail(fmt.Sprintf("raw repair missing %q:\n%s", want, result.Raw), 1)
		}
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxReadFileLinesAliasPreservesExplicitRange", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `{"tool":"read_file","path":"logs/conversations/recent.jsonl","lines":"1-51","start_line":52,"end_line":131}`,
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("NormalizeToolRequestSyntax: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected textual JSON tool request to normalize", 1)
	}

	if result.Request.StartLine != 52 || result.Request.EndLine != 131 {
		ginkgo.Fail(fmt.Sprintf("explicit range should win, got %d-%d", result.Request.StartLine, result.Request.EndLine), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxTextualJSONEditFile", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `{"tool":"edit_file","path":"internal/labels/labels.go","old_text":"	return \"Save changes\"","new_text":"	return \"Save\"","description":"Update ShortLabel"}`,
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("NormalizeToolRequestSyntax: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail(fmt.Sprintf("expected textual edit_file JSON tool request to normalize: %#v", result), 1)
	}

	if result.Request.Tool != "edit_file" || result.Request.Path != "internal/labels/labels.go" {
		ginkgo.Fail(fmt.Sprintf("unexpected request: %#v", result.Request), 1)
	}

	if result.Request.OldText != "\treturn \"Save changes\"" || result.Request.NewText != "\treturn \"Save\"" {
		ginkgo.Fail(fmt.Sprintf("unexpected replacement text: old=%q new=%q", result.Request.OldText, result.Request.NewText), 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized || result.EventTransport != toolRequestNormalizerTransportTextual {
		ginkgo.Fail(fmt.Sprintf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxBareToolJSONObject", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: strings.Join([]string{
			"submit_plan",
			"",
			"{",
			`  "rationale": "Verification Checklist:\n- source_sanity failed for target.txt, so submit a corrective replacement.",`,
			`  "edits": [{"path":"target.txt","old_text":"old","new_text":"new","purpose":"repair target"}]`,
			"}",
		}, "\n"),
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("NormalizeToolRequestSyntax: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected bare submit_plan JSON request to normalize", 1)
	}

	if result.Request.Tool != "submit_plan" || result.Request.Rationale == "" || len(result.Request.Edits) != 1 || result.Request.Edits[0].Path != "target.txt" {
		ginkgo.Fail(fmt.Sprintf("unexpected request: %#v", result.Request), 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized || result.EventTransport != toolRequestNormalizerTransportTextual {
		ginkgo.Fail(fmt.Sprintf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxFencedJSON", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: "```json\n{\"tool\":\"read_file\",\"path\":\"README.md\",\"start_line\":1,\"end_line\":20}\n```",
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("NormalizeToolRequestSyntax: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected exact fenced JSON tool request to normalize", 1)
	}

	if result.Request.Tool != "read_file" || result.Request.Path != "README.md" || result.Request.StartLine != 1 || result.Request.EndLine != 20 {
		ginkgo.Fail(fmt.Sprintf("unexpected request: %#v", result.Request), 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized || result.EventTransport != toolRequestNormalizerTransportFencedJSON {
		ginkgo.Fail(fmt.Sprintf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxKeyValue", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: "tool: read_file\npath: README.md\nstart_line: 1\nend_line: 20\nnamed_gap: inspect intro",
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("NormalizeToolRequestSyntax: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected key/value tool request to normalize", 1)
	}

	if result.Request.Tool != "read_file" || result.Request.Path != "README.md" || result.Request.StartLine != 1 || result.Request.EndLine != 20 || result.Request.NamedGap != "inspect intro" {
		ginkgo.Fail(fmt.Sprintf("unexpected request: %#v", result.Request), 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized || result.EventTransport != toolRequestNormalizerTransportKeyValue {
		ginkgo.Fail(fmt.Sprintf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxKeyValueSearchScopeAlias", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: "tool: search_content\npattern: implementation detection\nscope: .",
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("NormalizeToolRequestSyntax: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail(fmt.Sprintf("expected key/value search request to normalize: %#v", result), 1)
	}

	if result.Request.Tool != "search_content" || result.Request.Pattern != "implementation detection" || result.Request.Path != "." {
		ginkgo.Fail(fmt.Sprintf("unexpected request: %#v", result.Request), 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized || result.EventTransport != toolRequestNormalizerTransportKeyValue {
		ginkgo.Fail(fmt.Sprintf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxRejectsKeyValueActionAlias", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: "tool: code_navigate\naction: read_symbol\npath: internal/workflows/productworkflow/coverage_gate.go\nsymbol: EvaluateCoverageGate",
	})

	if result.OK {
		ginkgo.Fail(fmt.Sprintf("action alias must not normalize: %#v", result.Request), 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeUnsafeToInfer || result.EventTransport != toolRequestNormalizerTransportKeyValue {
		ginkgo.Fail(fmt.Sprintf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxKeyValueLinesAliasOnlyAppliesToReadFile", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: "tool: code_navigate\ncapability: read_symbol\npath: internal/workflows/productworkflow/coverage_gate.go\npattern: EvaluateCoverageGate\nlines: 1-20",
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("NormalizeToolRequestSyntax: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected key/value tool request to normalize", 1)
	}

	if result.Request.Tool != "code_navigate" {
		ginkgo.Fail(fmt.Sprintf("unexpected request: %#v", result.Request), 1)
	}

	if result.Request.StartLine != 0 || result.Request.EndLine != 0 {
		ginkgo.Fail(fmt.Sprintf("non-read_file lines alias should not set range, got %d-%d", result.Request.StartLine, result.Request.EndLine), 1)
	}

	result = normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `{"tool":"edit_file","path":"internal/labels/labels.go","old_text":"old","new_text":"new","lines":"1-20","description":"negative alias test"}`,
	})
	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("NormalizeToolRequestSyntax: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected edit request to normalize", 1)
	}

	if result.Request.StartLine != 0 || result.Request.EndLine != 0 {
		ginkgo.Fail(fmt.Sprintf("mutation request lines alias should not set range, got %d-%d", result.Request.StartLine, result.Request.EndLine), 1)
	}

	result = normalizeToolRequestSyntax(llama.ChatMessage{
		ToolCalls: []llama.ToolCall{{
			Function: llama.ToolCallFunc{
				Name:      "edit_file",
				Arguments: `{"tool":"edit_file","path":"internal/labels/labels.go","old_text":"old","new_text":"new","lines":"1-20","description":"negative native alias test"}`,
			},
		}},
	})
	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("NormalizeToolRequestSyntax: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected native edit request to normalize", 1)
	}

	if result.Request.StartLine != 0 || result.Request.EndLine != 0 {
		ginkgo.Fail(fmt.Sprintf("native mutation request lines alias should not set range, got %d-%d", result.Request.StartLine, result.Request.EndLine), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxKeyValueReadFileLinesAliasRejectsMalformedRange", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: "tool: read_file\npath: logs/conversations/recent.jsonl\nlines: 1-bad",
	})

	if result.OK {
		ginkgo.Fail(fmt.Sprintf("malformed lines alias should not normalize: %#v", result.Request), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxCodeNavigateDoesNotDefaultMissingCapabilityWithPattern", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: "tool: code_navigate\npath: cmd/idiom/product_workflow.go\npattern: Run\nstart_line: 1\nend_line: 100",
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("NormalizeToolRequestSyntax: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected key/value tool request to normalize", 1)
	}

	if result.Request.Tool != "code_navigate" ||
		result.Request.Capability != "" ||
		result.Request.Path != "cmd/idiom/product_workflow.go" ||
		result.Request.Pattern != "Run" {

		ginkgo.Fail(fmt.Sprintf("unexpected request: %#v", result.Request), 1)
	}

	if result.Repaired {
		ginkgo.Fail("missing capability must not be repaired", 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxCodeNavigateDoesNotDefaultMissingCapabilityWithPathOnly", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `{"tool":"code_navigate","path":"cmd/idiom/product_workflow.go","symbol":"read_symbol"}`,
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("NormalizeToolRequestSyntax: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected textual JSON tool request to normalize", 1)
	}

	if result.Request.Tool != "code_navigate" ||
		result.Request.Capability != "" ||
		result.Request.Path != "cmd/idiom/product_workflow.go" ||
		result.Request.Pattern != "" {

		ginkgo.Fail(fmt.Sprintf("unexpected request: %#v", result.Request), 1)
	}

	if result.Repaired {
		ginkgo.Fail("missing capability must not be repaired", 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxCodeReadAlias", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `{"tool":"code_read","path":"internal/workflows/productworkflow/coverage_gate.go","start_line":1,"end_line":125}`,
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("NormalizeToolRequestSyntax: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected textual JSON tool request to normalize", 1)
	}

	if result.Request.Tool != "read_file" ||
		result.Request.Path != "internal/workflows/productworkflow/coverage_gate.go" ||
		result.Request.StartLine != 1 ||
		result.Request.EndLine != 125 {

		ginkgo.Fail(fmt.Sprintf("unexpected request: %#v", result.Request), 1)
	}

	if !result.Repaired {
		ginkgo.Fail("expected code_read alias repair to be marked repaired", 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxResolutionDoesNotBecomeCapability", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `{"tool":"code_navigate","path":"internal/workflows/productworkflow/coverage_gate.go","pattern":"func EvaluateCoverageGate","resolution":"definition"}`,
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("NormalizeToolRequestSyntax: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected textual JSON tool request to normalize", 1)
	}

	if result.Request.Tool != "code_navigate" ||
		result.Request.Capability != "" ||
		result.Request.Path != "internal/workflows/productworkflow/coverage_gate.go" ||
		result.Request.Pattern != "func EvaluateCoverageGate" ||
		result.Request.Resolution != "definition" {

		ginkgo.Fail(fmt.Sprintf("unexpected request: %#v", result.Request), 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized || result.EventTransport != toolRequestNormalizerTransportTextual {
		ginkgo.Fail(fmt.Sprintf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxCodeNavigateFindReferencesAlias", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `{"tool":"code_navigate","capability":"find_references","path":"internal/surfaces/tui/app","pattern":"input"}`,
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("NormalizeToolRequestSyntax: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected missing-tool code_navigate request to normalize", 1)
	}

	if result.Request.Tool != "code_navigate" ||
		result.Request.Capability != "find_references" ||
		result.Request.Path != "internal/surfaces/tui/app" ||
		result.Request.Pattern != "input" {

		ginkgo.Fail(fmt.Sprintf("unexpected request: %#v", result.Request), 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized {
		ginkgo.Fail(fmt.Sprintf("unexpected event outcome: %q", result.EventOutcome), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxKeyValueArgsJSON", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: strings.Join([]string{
			"tool_call:code:navigate",
			"summary: Looking up symbol definition for RunAudit",
			"tool: code_navigate",
			"args:",
			"{",
			`  "path": "cmd/idiom/audit.go",`,
			`  "symbol": "RunAudit",`,
			`  "kind": "definition"`,
			"}",
		}, "\n"),
	})

	if result.OK {
		ginkgo.Fail(fmt.Sprintf("function-call-like multi-line text should not normalize: %#v", result.Request), 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeUnsafeToInfer || result.EventTransport != toolRequestNormalizerTransportKeyValueArgs {
		ginkgo.Fail(fmt.Sprintf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxKeyValueArgsJSONRejectsToolMismatch", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: "tool: code_navigate\nargs:\n{\"tool\":\"read_file\",\"path\":\"cmd/idiom/audit.go\"}",
	})

	if result.OK {
		ginkgo.Fail(fmt.Sprintf("mismatched key/value args request must not normalize: %#v", result.Request), 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeUnsafeToInfer || result.EventTransport != toolRequestNormalizerTransportKeyValueArgs {
		ginkgo.Fail(fmt.Sprintf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxFunctionCall", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `tool: code_navigate(capability="read_symbol", path="internal/workflows/productworkflow/coverage_gate.go", language="go", pattern="EvaluateCoverageGate")`,
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("NormalizeToolRequestSyntax: %v", result.ParseErr), 1)
	}

	if !result.OK {
		ginkgo.Fail("expected function-call-shaped tool request to normalize", 1)
	}

	if result.Request.Tool != "code_navigate" ||
		result.Request.Capability != "read_symbol" ||
		result.Request.Path != "internal/workflows/productworkflow/coverage_gate.go" ||
		result.Request.Language != "go" ||
		result.Request.Pattern != "EvaluateCoverageGate" {

		ginkgo.Fail(fmt.Sprintf("unexpected request: %#v", result.Request), 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized || result.EventTransport != toolRequestNormalizerTransportFunctionCall {
		ginkgo.Fail(fmt.Sprintf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxRejectsFunctionCallOperationAlias", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `tool: code_navigate(operation="read_symbol", path="internal/workflows/productworkflow/coverage_gate.go", symbol="EvaluateCoverageGate")`,
	})

	if result.OK {
		ginkgo.Fail(fmt.Sprintf("operation alias must not normalize: %#v", result.Request), 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeUnsafeToInfer || result.EventTransport != toolRequestNormalizerTransportFunctionCall {
		ginkgo.Fail(fmt.Sprintf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxRejectsFunctionCallActionAlias", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `tool: code_navigate(action="read_symbol", path="internal/workflows/productworkflow/coverage_gate.go", symbol="EvaluateCoverageGate")`,
	})

	if result.OK {
		ginkgo.Fail(fmt.Sprintf("action alias must not normalize: %#v", result.Request), 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeUnsafeToInfer || result.EventTransport != toolRequestNormalizerTransportFunctionCall {
		ginkgo.Fail(fmt.Sprintf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxRejectsUnsafeFunctionCallFields", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `tool: edit_file(path="README.md", old_text="old", new_text="new")`,
	})

	if result.OK {
		ginkgo.Fail(fmt.Sprintf("function-call edit body shape must not normalize: %#v", result.Request), 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeUnsafeToInfer || result.EventTransport != toolRequestNormalizerTransportFunctionCall {
		ginkgo.Fail(fmt.Sprintf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
	}

	tool, transport, ok := unsafeTextualWriteAttempt(`tool: edit_file(path="README.md", old_text="old", new_text="new")`)
	if !ok || tool != "edit_file" || transport != toolRequestNormalizerTransportFunctionCall {
		ginkgo.Fail(fmt.Sprintf("expected non-executing unsafe function-call write classification, got tool=%q transport=%q ok=%v", tool, transport, ok), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxRejectsNonJSONTextualWriteTools", func() {
	tests := []struct {
		name      string
		content   string
		tool      string
		transport string
	}{
		{
			name:      "key value write_file",
			content:   "tool: write_file\npath: README.md\ncontent: hello\ndescription: update readme",
			tool:      "write_file",
			transport: toolRequestNormalizerTransportKeyValue,
		},
		{
			name:      "key value append_file",
			content:   "tool: append_file\npath: README.md\ncontent: hello\ndescription: append readme",
			tool:      "append_file",
			transport: toolRequestNormalizerTransportKeyValue,
		},
		{
			name:      "key value args write_file",
			content:   "tool: write_file\nargs:\n{\"path\":\"README.md\",\"content\":\"hello\",\"description\":\"update readme\"}",
			tool:      "write_file",
			transport: toolRequestNormalizerTransportKeyValueArgs,
		},
		{
			name:      "function call write_file",
			content:   `tool: write_file(path="README.md", content="hello", description="update readme")`,
			tool:      "write_file",
			transport: toolRequestNormalizerTransportFunctionCall,
		},
		{
			name:      "function call append_file",
			content:   `tool: append_file(path="README.md", content="hello", description="append readme")`,
			tool:      "append_file",
			transport: toolRequestNormalizerTransportFunctionCall,
		},
	}

	for _, tt := range tests {
		ginkgo.By(tt.name, func() {
			result := normalizeToolRequestSyntax(llama.ChatMessage{Content: tt.content})
			if result.OK {
				ginkgo.Fail(fmt.Sprintf("non-JSON textual %s body must not normalize: %#v", tt.tool, result.Request), 1)
			}

			if result.EventOutcome != toolRequestNormalizerOutcomeUnsafeToInfer || result.EventTransport != tt.transport {
				ginkgo.Fail(fmt.Sprintf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
			}

			tool, transport, ok := unsafeTextualWriteAttempt(tt.content)
			if !ok || tool != tt.tool || transport != tt.transport {
				ginkgo.Fail(fmt.Sprintf("expected non-executing unsafe textual write classification, got tool=%q transport=%q ok=%v", tool, transport, ok), 1)
			}
		})
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxBareJSONMissingToolRejected", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `{"path":"README.md","start_line":1,"end_line":20}`,
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("NormalizeToolRequestSyntax: %v", result.ParseErr), 1)
	}

	if result.OK || result.Request.Tool != "" {
		ginkgo.Fail(fmt.Sprintf("bare JSON without tool must not normalize: %#v", result.Request), 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeUnsafeToInfer || result.EventTransport != toolRequestNormalizerTransportFunctionCall {
		ginkgo.Fail(fmt.Sprintf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxDoesNotInferUnsafeBareJSONEdit", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `{"path":"README.md","old_text":"old","new_text":"new"}`,
	})

	if result.OK {
		ginkgo.Fail(fmt.Sprintf("unsafe missing-tool edit shape must not normalize: %#v", result.Request), 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeUnsafeToInfer || result.EventTransport != toolRequestNormalizerTransportFunctionCall {
		ginkgo.Fail(fmt.Sprintf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxDoesNotInferUnsafeBareJSONShell", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `{"command":"go test ./..."}`,
	})

	if result.OK {
		ginkgo.Fail(fmt.Sprintf("unsafe missing-tool shell shape must not normalize: %#v", result.Request), 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeUnsafeToInfer || result.EventTransport != toolRequestNormalizerTransportFunctionCall {
		ginkgo.Fail(fmt.Sprintf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxDoesNotInferUnsafeBareJSONTask", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: `{"agent":"worker","prompt":"edit README.md"}`,
	})

	if result.OK {
		ginkgo.Fail(fmt.Sprintf("unsafe missing-tool task shape must not normalize: %#v", result.Request), 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeUnsafeToInfer || result.EventTransport != toolRequestNormalizerTransportFunctionCall {
		ginkgo.Fail(fmt.Sprintf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxMalformedOutputFixtureMatrix", func() {
	stringifiedEdits, err := json.Marshal(map[string]any{
		"tool":      "submit_plan",
		"rationale": "plan",
		"edits":     `[{"path":"a.go","old_text":"old","new_text":"new","purpose":"update a"}]`,
	})
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("marshal stringified edits fixture: %v", err), 1)
	}

	cases := []struct {
		name      string
		content   string
		wantOK    bool
		wantTool  string
		outcome   string
		transport string
		repaired  bool
	}{
		{
			name:      "stringified submit_plan edits repair",
			content:   string(stringifiedEdits),
			wantOK:    true,
			wantTool:  "submit_plan",
			outcome:   toolRequestNormalizerOutcomeNormalized,
			transport: toolRequestNormalizerTransportStringifiedSubmitRepair,
			repaired:  true,
		},
		{
			name:      "exact fenced read-only JSON",
			content:   "```json\n{\"tool\":\"read_file\",\"path\":\"README.md\"}\n```",
			wantOK:    true,
			wantTool:  "read_file",
			outcome:   toolRequestNormalizerOutcomeNormalized,
			transport: toolRequestNormalizerTransportFencedJSON,
		},
		{
			name:      "narrative fenced JSON rejected",
			content:   "Use this:\n```json\n{\"tool\":\"read_file\",\"path\":\"README.md\"}\n```",
			wantOK:    false,
			outcome:   toolRequestNormalizerOutcomeUnsafeToInfer,
			transport: toolRequestNormalizerTransportFencedJSON,
		},
		{
			name:      "bare JSON missing tool rejected",
			content:   `{"path":"README.md","start_line":1,"end_line":20}`,
			wantOK:    false,
			outcome:   toolRequestNormalizerOutcomeUnsafeToInfer,
			transport: toolRequestNormalizerTransportFunctionCall,
		},
		{
			name:      "unsafe bare edit shape rejected",
			content:   `{"path":"README.md","old_text":"old","new_text":"new"}`,
			wantOK:    false,
			outcome:   toolRequestNormalizerOutcomeUnsafeToInfer,
			transport: toolRequestNormalizerTransportFunctionCall,
		},
		{
			name:      "unsafe function-call edit shape rejected",
			content:   `tool: edit_file(path="README.md", old_text="old", new_text="new")`,
			wantOK:    false,
			outcome:   toolRequestNormalizerOutcomeUnsafeToInfer,
			transport: toolRequestNormalizerTransportFunctionCall,
		},
		{
			name:      "nested read-only arguments repair",
			content:   `{"arguments":{"tool":"read_file","path":"cmd/idiom/audit.go","start_line":1,"end_line":100}}`,
			wantOK:    true,
			wantTool:  "read_file",
			outcome:   toolRequestNormalizerOutcomeNormalized,
			transport: toolRequestNormalizerTransportNestedArguments,
			repaired:  true,
		},
		{
			name:      "unsafe missing-tool shell rejected",
			content:   `{"command":"go test ./..."}`,
			wantOK:    false,
			outcome:   toolRequestNormalizerOutcomeUnsafeToInfer,
			transport: toolRequestNormalizerTransportFunctionCall,
		},
	}

	for _, tc := range cases {
		ginkgo.By(tc.name, func() {
			result := normalizeToolRequestSyntax(llama.ChatMessage{Content: tc.content})
			if result.OK != tc.wantOK {
				ginkgo.Fail(fmt.Sprintf("OK = %v, want %v; request=%#v parseErr=%v", result.OK, tc.wantOK, result.Request, result.ParseErr), 1)
			}

			if tc.wantTool != "" && result.Request.Tool != tc.wantTool {
				ginkgo.Fail(fmt.Sprintf("Tool = %q, want %q; request=%#v", result.Request.Tool, tc.wantTool, result.Request), 1)
			}

			if result.EventOutcome != tc.outcome || result.EventTransport != tc.transport {
				ginkgo.Fail(fmt.Sprintf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
			}

			if result.Repaired != tc.repaired {
				ginkgo.Fail(fmt.Sprintf("Repaired = %v, want %v", result.Repaired, tc.repaired), 1)
			}
		})
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxDoesNotInferNarrativeFencedJSON", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: "Please run this:\n```json\n{\"tool\":\"read_file\",\"path\":\"README.md\"}\n```",
	})

	if result.OK {
		ginkgo.Fail(fmt.Sprintf("narrative-wrapped fenced JSON must stay out of the syntax normalizer: %#v", result.Request), 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeUnsafeToInfer || result.EventTransport != toolRequestNormalizerTransportFencedJSON {
		ginkgo.Fail(fmt.Sprintf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxAcceptsKeyValueEditFields", func() {
	result := normalizeToolRequestSyntax(llama.ChatMessage{
		Content: "tool: edit_file\npath: README.md\nold_text: old\nnew_text: new\ndescription: update readme",
	})

	if !result.OK {
		ginkgo.Fail(fmt.Sprintf("key/value edit body shape should normalize: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
	}

	if result.Request.Tool != "edit_file" || result.Request.OldText != "old" || result.Request.NewText != "new" {
		ginkgo.Fail(fmt.Sprintf("unexpected parsed edit request: %#v", result.Request), 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized || result.EventTransport != toolRequestNormalizerTransportKeyValue {
		ginkgo.Fail(fmt.Sprintf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
	}

	tool, transport, ok := unsafeTextualWriteAttempt("tool: edit_file\npath: README.md\nold_text: old\nnew_text: new\ndescription: update readme")
	if ok {
		ginkgo.Fail(fmt.Sprintf("normalized key-value edit should not be classified unsafe, got tool=%q transport=%q", tool, transport), 1)
	}
})

var _ = ginkgo.It("RepairToolRequestSyntaxStringifiedSubmitPlanEdits", func() {
	inner := `[{"path":"a.go","old_text":"old","new_text":"new","purpose":"update a"}]`

	payload, err := json.Marshal(map[string]any{
		"rationale": "plan",
		"edits":     inner,
	})
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("marshal payload: %v", err), 1)
	}

	call := llama.ToolCall{Function: llama.ToolCallFunc{Name: "submit_plan", Arguments: string(payload)}}
	result := repairToolRequestSyntax(toolRequestSyntaxNormalization{
		Raw:            string(payload),
		ParseErr:       errors.New("json: cannot unmarshal string into Go struct field ToolRequest.edits"),
		ActiveToolCall: &call,
	})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("repair should clear parse error: %v", result.ParseErr), 1)
	}

	if !result.OK || !result.Repaired {
		ginkgo.Fail(fmt.Sprintf("expected repaired normalization, got ok=%v repaired=%v", result.OK, result.Repaired), 1)
	}

	if result.Request.Tool != "submit_plan" || len(result.Request.Edits) != 1 || result.Request.Edits[0].Path != "a.go" {
		ginkgo.Fail(fmt.Sprintf("unexpected repaired request: %#v", result.Request), 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized || result.EventTransport != toolRequestNormalizerTransportStringifiedSubmitRepair {
		ginkgo.Fail(fmt.Sprintf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
	}
})

var _ = ginkgo.It("NormalizeToolRequestSyntaxRepairsTextualStringifiedSubmitPlanEditsBeforeReportingParse", func() {
	inner := `[{"path":"a.go","old_text":"old","new_text":"new","purpose":"update a"}]`

	payload, err := json.Marshal(map[string]any{
		"tool":      "submit_plan",
		"rationale": "plan",
		"edits":     inner,
	})
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("marshal payload: %v", err), 1)
	}

	result := normalizeToolRequestSyntax(llama.ChatMessage{Content: string(payload)})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("textual stringified edits repair should clear parse error: %v", result.ParseErr), 1)
	}

	if !result.OK || !result.Repaired {
		ginkgo.Fail(fmt.Sprintf("expected repaired textual normalization, got ok=%v repaired=%v", result.OK, result.Repaired), 1)
	}

	if result.Request.Tool != "submit_plan" || len(result.Request.Edits) != 1 || result.Request.Edits[0].Path != "a.go" {
		ginkgo.Fail(fmt.Sprintf("unexpected repaired request: %#v", result.Request), 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized || result.EventTransport != toolRequestNormalizerTransportStringifiedSubmitRepair {
		ginkgo.Fail(fmt.Sprintf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
	}

	if result.ParserFailureClass != autonomy.ParserFailureNone {
		ginkgo.Fail(fmt.Sprintf("repaired parse failure should not remain classified, got %q", result.ParserFailureClass), 1)
	}
})

var _ = ginkgo.It("RepairToolRequestSyntaxNestedArgumentsObject", func() {
	payload := `{"arguments":{"tool":"read_file","path":"cmd/idiom/audit.go","start_line":1,"end_line":100}}`
	result := normalizeToolRequestSyntax(llama.ChatMessage{Content: payload})

	if result.ParseErr != nil {
		ginkgo.Fail(fmt.Sprintf("nested arguments repair should clear parse error: %v", result.ParseErr), 1)
	}

	if !result.OK || !result.Repaired {
		ginkgo.Fail(fmt.Sprintf("expected repaired normalization, got ok=%v repaired=%v", result.OK, result.Repaired), 1)
	}

	if result.Request.Tool != "read_file" || result.Request.Path != "cmd/idiom/audit.go" || result.Request.StartLine != 1 || result.Request.EndLine != 100 {
		ginkgo.Fail(fmt.Sprintf("unexpected repaired request: %#v", result.Request), 1)
	}

	if result.EventOutcome != toolRequestNormalizerOutcomeNormalized || result.EventTransport != toolRequestNormalizerTransportNestedArguments {
		ginkgo.Fail(fmt.Sprintf("unexpected event metadata: outcome=%q transport=%q", result.EventOutcome, result.EventTransport), 1)
	}
})

var _ = ginkgo.It("RepairToolRequestSyntaxNestedArgumentsRejectsMixedWrapper", func() {
	payload := `{"tool":"read_file","arguments":{"tool":"write_file","path":"README.md","content":"unsafe"}}`
	result := normalizeToolRequestSyntax(llama.ChatMessage{Content: payload})

	if result.OK && result.EventTransport == toolRequestNormalizerTransportNestedArguments {
		ginkgo.Fail(fmt.Sprintf("mixed wrapper must not be repaired through nested arguments: %#v", result.Request), 1)
	}
})

var _ = ginkgo.It("CheckWriteDescriptionSkipsNonDescribedTools", func() {
	req := workspace.ToolRequest{Tool: "read_file", Path: "foo.go"}
	if got := CheckDescription(req); got != nil {
		ginkgo.Fail(fmt.Sprintf("read_file should be skipped, got guard result: %#v", got), 1)
	}

	req = workspace.ToolRequest{Tool: "mkdir", Path: "foo"}
	if got := CheckDescription(req); got != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir should be skipped, got guard result: %#v", got), 1)
	}
})

var _ = ginkgo.It("CheckWriteDescriptionPassesWhenDescriptionPresent", func() {
	req := workspace.ToolRequest{Tool: "edit_file", Path: "x.go", Content: "pkg", Description: "rename package"}
	if got := CheckDescription(req); got != nil {
		ginkgo.Fail(fmt.Sprintf("edit_file with description should pass, got guard result: %#v", got), 1)
	}
})

var _ = ginkgo.It("CheckWriteDescriptionTreatsWhitespaceAsMissing", func() {
	req := workspace.ToolRequest{Tool: "edit_file", Path: "x.go", Description: "   \t\n "}
	got := CheckDescription(req)

	if got == nil {
		ginkgo.Fail("whitespace-only description should be rejected", 1)
	}

	if !strings.Contains(got.Error, "requires a \"description\"") {
		ginkgo.Fail(fmt.Sprintf("guard error should explain requirement, got: %q", got.Error), 1)
	}
})

var _ = ginkgo.It("CheckWriteDescriptionRejectsEachDescribedTool", func() {
	for _, tool := range workspace.WorkspacePerEditWriteToolNames() {
		req := workspace.ToolRequest{Tool: tool, Path: "x.go"}
		got := CheckDescription(req)

		if got == nil {
			ginkgo.Fail(fmt.Sprintf("%s without description should be rejected", tool), 1)

			continue
		}

		if got.Tool != tool {
			ginkgo.Fail(fmt.Sprintf("%s: guard Tool = %q, want %q", tool, got.Tool, tool), 1)
		}

		if got.Summary != "Missing write description" {
			ginkgo.Fail(fmt.Sprintf("%s: guard Summary = %q", tool, got.Summary), 1)
		}

		if got.Code != writeDescriptionMissingCode {
			ginkgo.Fail(fmt.Sprintf("%s: guard Code = %q, want %q", tool, got.Code, writeDescriptionMissingCode), 1)
		}

		if !strings.Contains(got.Error, tool) {
			ginkgo.Fail(fmt.Sprintf("%s: guard error should mention the tool name, got: %q", tool, got.Error), 1)
		}

		if !strings.Contains(got.Error, "description") {
			ginkgo.Fail(fmt.Sprintf("%s: guard error should explain the missing description, got: %q", tool, got.Error), 1)
		}
	}
})

// TestRunnerRejectsEditWithoutDescription locks in that the guard fires
// through the runner and surfaces as a structured ToolResultEvent.
var _ = ginkgo.It("RunnerRejectsEditWithoutDescription", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed: %v", err), 1)
	}
	replies := []string{
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"README.md\",\"old_text\":\"hello\",\"new_text\":\"bye\"}\n```",
		"Stopped without description.",
	}

	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()

		if requests >= len(replies) {
			ginkgo.Fail(fmt.Sprintf("unexpected model request %d", requests+1), 1)
		}
		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))

	defer server.Close()

	var guarded *workspace.ToolResult

	ws := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
	ws.MarkPathObserved("README.md")
	runner := newRunnerRunFixture(&Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: ws,
		Policy:    RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3, MaxOperationalModelCalls: 2},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			PermissionProfile: "guarded_workspace_write",
			AllowedEditPaths:  []string{"README.md"},
		},
	})

	_, err := runner.runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "edit without description"}},
		llama.ChatOptions{},
		nil, nil,
		func(ev ToolResultEvent) {
			if strings.Contains(ev.Result.Error, "requires a \"description\"") {
				copy := ev.Result

				guarded = &copy
			}
		},
	)
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("Run: %v", err), 1)
	}

	if guarded == nil {
		ginkgo.Fail("expected guard to fire as a ToolResultEvent", 1)
	}

	if guarded.Tool != "edit_file" {
		ginkgo.Fail(fmt.Sprintf("guard ToolResult.Tool = %q", guarded.Tool), 1)
	}
	if requests != len(replies) {
		ginkgo.Fail(fmt.Sprintf("expected the guard and bounded closeout model calls, got %d", requests), 1)
	}

	// The file must NOT have been mutated — the guard fires before Execute.
	data, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("read readme: %v", err), 1)
	}

	if string(data) != "hello\n" {
		ginkgo.Fail(fmt.Sprintf("file was mutated despite rejected write: %q", data), 1)
	}
})

// TestRunnerAllowsEditWithDescription verifies the happy path — once the
// description is present the edit executes and mutates the file.
var _ = ginkgo.It("RunnerAllowsEditWithDescription", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed: %v", err), 1)
	}

	replies := []string{
		"```tool\n{\"tool\":\"read_file\",\"path\":\"README.md\",\"start_line\":1,\"end_line\":1,\"named_gap\":\"current greeting before edit\"}\n```",
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"README.md\",\"old_text\":\"hello\",\"new_text\":\"bye\",\"description\":\"change greeting\"}\n```",
		"Done.",
	}

	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))

	defer server.Close()

	var (
		lastReq     workspace.ToolRequest
		toolResults []workspace.ToolResult
	)

	ws := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})

	// Satisfy the read-before-edit invariant for edit_file.
	ws.MarkPathObserved("README.md")

	runner := newRunnerRunFixture(&Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: ws,
		Policy:    RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
	})

	_, err := runner.runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "edit"}},
		llama.ChatOptions{},
		nil, nil,
		func(ev ToolResultEvent) {
			toolResults = append(toolResults, ev.Result)
			if ev.Request.Tool == "edit_file" {
				lastReq = ev.Request
			}
		},
	)
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("Run: %v", err), 1)
	}

	if lastReq.Description != "change greeting" {
		ginkgo.Fail(fmt.Sprintf("Request.Description not propagated through event: %#v", lastReq), 1)
	}

	data, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("read: %v", err), 1)
	}

	if string(data) != "bye\n" {
		ginkgo.Fail(fmt.Sprintf("file not mutated: %q; results=%#v", data, toolResults), 1)
	}
})

// TestRunnerGuardFiresAfterPermissionDeny ensures that when a tool is
// denied by policy, the permission decision wins and the description
// guard does not redundantly nag. This is a correctness check on the
// order we chose: PreExecute (permissions) before description guard.
var _ = ginkgo.It("RunnerGuardFiresAfterPermissionDeny", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	if err := writeTestFile(root, "README.md", "hello\n"); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed: %v", err), 1)
	}

	replies := []string{
		// No description — if the guard ran first, the result would be a
		// description-missing error instead of a permission denial.
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"README.md\",\"old_text\":\"hello\",\"new_text\":\"bye\"}\n```",
		"Denied.",
	}

	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))

	defer server.Close()

	var firstResult workspace.ToolResult

	runner := newRunnerRunFixture(&Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
		PermissionPolicy: toolpolicy.Policy{
			Default: toolpolicy.Allow,
			Rules: []toolpolicy.Rule{{
				Tool:        "edit_file",
				PathPattern: "README.md",
				Decision:    toolpolicy.Deny,
			}},
		},
	})

	_, err := runner.runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "edit"}},
		llama.ChatOptions{},
		nil, nil,
		func(ev ToolResultEvent) {
			if firstResult.Tool == "" && ev.Result.Tool == "edit_file" {
				firstResult = ev.Result
			}
		},
	)
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("Run: %v", err), 1)
	}

	if firstResult.Tool != "edit_file" {
		ginkgo.Fail(fmt.Sprintf("expected edit_file permission result, got %#v", firstResult), 1)
	}
	if !strings.Contains(strings.ToLower(firstResult.Summary+"\n"+firstResult.Error), "permission") {
		ginkgo.Fail(fmt.Sprintf("expected permission denial result, got %#v", firstResult), 1)
	}
	if strings.Contains(firstResult.Error, "requires a \"description\"") {
		ginkgo.Fail(fmt.Sprintf("description guard fired before permission check: %q", firstResult.Error), 1)
	}
})

var _ = ginkgo.It("RunnerAllowsRecoveryAfterRepeatedMissingWriteDescriptions", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	if err := writeTestFile(root, "target.go", "package hello\n"); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed: %v", err), 1)
	}
	if err := writeTestFile(root, "go.mod", "module example.com/description-fixture\n\ngo 1.22\n"); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed module: %v", err), 1)
	}

	replies := []string{
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"target.go\"}\n```",
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"target.go\",\"old_text\":\"hello\",\"new_text\":\"bye\"}\n```",
		"```tool\n{\"tool\":\"edit_file\",\"path\":\"target.go\",\"old_text\":\"hello\",\"new_text\":\"bye\",\"description\":\"change package name after guard feedback\"}\n```",
		"```tool\n{\"tool\":\"shell\",\"command\":\"go test ./...\"}\n```",
		"Done.",
	}

	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer ginkgo.GinkgoRecover()
		if requests >= len(replies) {
			ginkgo.Fail("unexpected extra chat request after recovery sequence", 1)
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))

	defer server.Close()

	var (
		descriptionRejections int
		toolResults           []workspace.ToolResult
	)

	ws := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true, EnableShell: true})
	source := ws.Execute(context.Background(), workspace.ToolRequest{
		Tool: workspace.ToolNameReadFile, Path: "target.go", StartLine: 1, EndLine: 1,
	})
	if source.Error != "" || len(source.SourceWindows()) != 1 {
		ginkgo.Fail(fmt.Sprintf("produce current target source fixture: %#v", source), 1)
	}
	runner := newRunnerRunFixture(&Runner{
		Client:      llama.NewClient(server.URL, time.Second),
		Workspace:   ws,
		Policy:      RunPolicy{MaxToolSteps: 5, MaxNoProgressTurns: 5},
		SeedResults: []workspace.ToolResult{source},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			PermissionProfile: "guarded_workspace_write",
			AllowedEditPaths:  []string{"target.go"},
		},
	})

	answer, err := runner.runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "edit without description"}},
		llama.ChatOptions{},
		nil, nil,
		func(ev ToolResultEvent) {
			toolResults = append(toolResults, ev.Result)
			if ev.Result.Code == writeDescriptionMissingCode {
				descriptionRejections++
			}
		},
	)
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("Run: %v", err), 1)
	}

	if answer != "Done." {
		ginkgo.Fail(fmt.Sprintf("runner should continue through missing-description rejections and accept later final answer, got %q; requests=%d results=%#v", answer, requests, toolResults), 1)
	}

	if descriptionRejections != 2 {
		ginkgo.Fail(fmt.Sprintf("description rejections = %d, want 2", descriptionRejections), 1)
	}

	data, err := os.ReadFile(filepath.Join(root, "target.go"))
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("read readme: %v", err), 1)
	}

	if string(data) != "package bye\n" {
		ginkgo.Fail(fmt.Sprintf("described recovery write did not mutate file: %q; results=%#v", data, toolResults), 1)
	}
})
