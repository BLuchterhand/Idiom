package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func TestReadOnlyChildTaskEmitsValidatedLifecycleRecords(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "lifecycle.go"), []byte("package pkg\n\nfunc LifecycleEvidence() string { return \"present\" }\n"), 0o644); err != nil {
		t.Fatalf("seed lifecycle source: %v", err)
	}

	replies := []string{
		"```tool\n{\"tool\":\"read_file\",\"path\":\"pkg/lifecycle.go\",\"start_line\":1,\"end_line\":20}\n```",
		"Source-body evidence paths:\n- pkg/lifecycle.go\n\nKey findings:\n- pkg/lifecycle.go defines LifecycleEvidence and returns present.\n\nConfidence: high",
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
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "architecture",
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain lifecycle records from pkg/lifecycle.go",
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	result := runner.runChildTask(context.Background(), workspace.ToolRequest{
		Tool:   "task",
		Agent:  "explorer",
		Mode:   "read_only",
		Prompt: "Explain lifecycle records from pkg/lifecycle.go",
	}, llama.ChatOptions{Model: "test-model"})

	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("child task returned error: %s", result.Error)
	}

	invocation := mustFindLifecycleRecord[autonomy.ParentChildInvocationRecord](t, events, childLifecycleInvocationEventRole)
	if invocation.Role != "child_read_only:explorer" || invocation.WorkspaceMode != autonomy.ChildWorkspaceNoWrite {
		t.Fatalf("unexpected invocation record: %#v", invocation)
	}

	terminal := mustFindLifecycleRecord[autonomy.ChildTerminalRecord](t, events, childLifecycleTerminalEventRole)
	if terminal.InvocationID != invocation.InvocationID {
		t.Fatalf("terminal invocation_id = %q, want %q", terminal.InvocationID, invocation.InvocationID)
	}
	if terminal.ResultKind != autonomy.ChildResultEvidencePacket || terminal.Status != autonomy.ChildTerminalCompleted {
		t.Fatalf("unexpected terminal record: %#v", terminal)
	}
	if len(terminal.EvidenceRefs) == 0 {
		t.Fatalf("terminal must carry evidence refs: %#v", terminal)
	}

	acceptance := mustFindLifecycleRecord[autonomy.ChildAcceptanceRecord](t, events, childLifecycleAcceptanceEventRole)
	if acceptance.TerminalID != terminal.TerminalID || acceptance.Decision != autonomy.ChildAcceptanceAccepted {
		t.Fatalf("unexpected read-only child acceptance: %#v", acceptance)
	}
	if len(acceptance.AcceptedEvidenceRefs) == 0 || acceptance.AlignmentBasis.DelegatedObjectiveRef == "" {
		t.Fatalf("read-only child acceptance must carry accepted evidence and alignment basis: %#v", acceptance)
	}

	childToolRequest := mustFindLifecycleRecord[ToolRequestEvent](t, events, "child_tool_request")
	if childToolRequest.ChildInvocationID != invocation.InvocationID || childToolRequest.ChildContractID != invocation.ChildContractID {
		t.Fatalf("child tool request must carry lifecycle correlation IDs: %#v", childToolRequest)
	}
	childToolResult := mustFindLifecycleRecord[ToolResultEvent](t, events, "child_tool_result")
	if childToolResult.ChildInvocationID != invocation.InvocationID || childToolResult.ChildContractID != invocation.ChildContractID {
		t.Fatalf("child tool result must carry lifecycle correlation IDs: %#v", childToolResult)
	}

	if invalid := lifecycleEventRoles(events, "child_lifecycle_record_invalid"); len(invalid) > 0 {
		t.Fatalf("lifecycle records should validate before emission, invalid events: %#v", invalid)
	}
}

func TestReadOnlyChildTerminalDoesNotAcceptWeakEvidencePacket(t *testing.T) {
	var events []RunEvent
	runner := &Runner{
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}
	ctx := runner.beginReadOnlyChildLifecycle("explorer", llama.ChatOptions{Model: "test-model"}, RunPolicy{})
	result := workspace.ToolResult{
		Tool:    "task",
		Summary: "Child task completed",
		Output:  "ungrounded answer",
	}
	runner.emitReadOnlyChildTerminal(ctx, result, nil, autonomy.ChildTerminalCompleted)

	terminal := mustFindLifecycleRecord[autonomy.ChildTerminalRecord](t, events, childLifecycleTerminalEventRole)
	if terminal.ObjectiveAlignment != autonomy.ChildObjectiveWeak {
		t.Fatalf("read-only terminal without typed packet should be weak: %#v", terminal)
	}
	acceptance := mustFindLifecycleRecord[autonomy.ChildAcceptanceRecord](t, events, childLifecycleAcceptanceEventRole)
	if acceptance.Decision != autonomy.ChildAcceptanceRepairRequired {
		t.Fatalf("weak read-only packet should require repair, got %#v", acceptance)
	}
	if len(acceptance.AcceptedEvidenceRefs) != 0 {
		t.Fatalf("weak read-only packet must not carry accepted evidence refs: %#v", acceptance)
	}

	if invalid := lifecycleEventRoles(events, "child_lifecycle_record_invalid"); len(invalid) > 0 {
		t.Fatalf("lifecycle records should validate before emission, invalid events: %#v", invalid)
	}
}

func TestDelegateEditDirectDispatchEmitsValidatedLifecycleRecords(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	var events []RunEvent
	runner := &Runner{
		Workspace: newDelegateEditTestWorkspace(t, root),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:             "mutation",
			TaskFamily:       "plan_then_execute_executor",
			Contract:         "implementation_result",
			RequiredEvidence: []string{"changed_paths", "verification_status"},
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	result := runner.runDelegateEdit(context.Background(), workspace.ToolRequest{
		Tool:    "delegate_edit",
		Path:    "a.txt",
		OldText: "before",
		NewText: "after",
		Purpose: "replace marker",
	})
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("delegate_edit returned error: %s", result.Error)
	}

	invocation := mustFindLifecycleRecord[autonomy.ParentChildInvocationRecord](t, events, childLifecycleInvocationEventRole)
	if invocation.WorkspaceMode != autonomy.ChildWorkspaceMain {
		t.Fatalf("delegate_edit invocation must use main workspace mode: %#v", invocation)
	}
	if invocation.RuntimeEnvelope.ToolTransportMode != autonomy.ChildTransportNone {
		t.Fatalf("direct delegate_edit invocation transport = %q, want none", invocation.RuntimeEnvelope.ToolTransportMode)
	}

	terminal := mustFindLifecycleRecord[autonomy.ChildTerminalRecord](t, events, childLifecycleTerminalEventRole)
	if terminal.ResultKind != autonomy.ChildResultAppliedParentEdit || terminal.Status != autonomy.ChildTerminalCompleted {
		t.Fatalf("unexpected terminal record: %#v", terminal)
	}
	if terminal.ExecutionMetadata.Route != autonomy.ChildRouteDeterministicDirect {
		t.Fatalf("terminal route = %q, want deterministic direct", terminal.ExecutionMetadata.Route)
	}
	if terminal.ParseStatus.TransportObserved != autonomy.ChildObservedTransportDeterministic {
		t.Fatalf("terminal parse transport = %q, want deterministic", terminal.ParseStatus.TransportObserved)
	}

	acceptance := mustFindLifecycleRecord[autonomy.ChildAcceptanceRecord](t, events, childLifecycleAcceptanceEventRole)
	if acceptance.TerminalID != terminal.TerminalID || acceptance.Decision != autonomy.ChildAcceptanceRecordedOnly {
		t.Fatalf("unexpected acceptance record: %#v", acceptance)
	}
	if acceptance.PathGateDecision != autonomy.ChildPathGateAllow || len(acceptance.VerificationObligationRefs) == 0 {
		t.Fatalf("acceptance must carry path gate and verification refs: %#v", acceptance)
	}

	if invalid := lifecycleEventRoles(events, "child_lifecycle_record_invalid"); len(invalid) > 0 {
		t.Fatalf("lifecycle records should validate before emission, invalid events: %#v", invalid)
	}
}

func TestDelegateEditExecutorLifecycleReportsTextualTransport(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	executor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{
					"content": `{"tool":"edit_file","path":"a.txt","old_text":"before","new_text":"after"}`,
				},
			}},
		})
	}))
	defer executor.Close()

	var events []RunEvent
	runner := &Runner{
		ImplementerClient: llama.NewClient(executor.URL, time.Second),
		Workspace:         newDelegateEditTestWorkspace(t, root),
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	result := runner.runDelegateEdit(context.Background(), workspace.ToolRequest{
		Tool:    "delegate_edit",
		Path:    "a.txt",
		OldText: "before",
		NewText: "after",
	})
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("delegate_edit returned error: %s", result.Error)
	}

	invocation := mustFindLifecycleRecord[autonomy.ParentChildInvocationRecord](t, events, childLifecycleInvocationEventRole)
	if invocation.RuntimeEnvelope.ToolTransportMode != autonomy.ChildTransportTextual {
		t.Fatalf("executor delegate_edit transport = %q, want textual", invocation.RuntimeEnvelope.ToolTransportMode)
	}
	if !invocation.RuntimeEnvelope.SupportsNativeTools {
		t.Fatalf("executor backend capability should still record native support: %#v", invocation.RuntimeEnvelope)
	}
}

func TestWorkbranchChildTerminalEmitsCandidatePatchArtifactMetadata(t *testing.T) {
	var events []RunEvent
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:       "mutation",
			TaskFamily: "implementation",
			Contract:   "implementation_result",
		},
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	ctx := runner.beginWorkbranchChildLifecycle("worker", llama.ChatOptions{Model: "coder"}, RunPolicy{}, runner.Client)
	diff := WorkbranchDiff{
		ChangedPaths: []string{"pkg/feature.go"},
		Stat:         "1 file changed, 3 insertions, 1 deletions",
		Patch:        "diff --git a/pkg/feature.go b/pkg/feature.go\n",
	}
	checkout := WorkbranchCheckout{Branch: "idiom-child-test", Path: "/tmp/idiom-child-test", BaseSHA: "abc123"}
	runner.emitWorkbranchChildTerminal(ctx, checkout, workspace.ToolResult{Tool: "task", Summary: "done"}, diff, autonomy.ChildTerminalCompleted)

	artifact := mustFindLifecycleRecord[autonomy.CandidatePatchArtifact](t, events, childLifecyclePatchEventRole)
	if artifact.BaseSHA != checkout.BaseSHA || len(artifact.ChangedPaths) != 1 || artifact.ChangedPaths[0] != "pkg/feature.go" {
		t.Fatalf("unexpected candidate patch artifact: %#v", artifact)
	}
	if artifact.PatchDigest == "" || artifact.PatchPath == "" {
		t.Fatalf("candidate patch artifact must carry digest and patch path refs: %#v", artifact)
	}

	terminal := mustFindLifecycleRecord[autonomy.ChildTerminalRecord](t, events, childLifecycleTerminalEventRole)
	if terminal.ResultKind != autonomy.ChildResultCandidatePatch || len(terminal.ArtifactRefs) == 0 {
		t.Fatalf("workbranch terminal must reference candidate patch artifact: %#v", terminal)
	}

	acceptance := mustFindLifecycleRecord[autonomy.ChildAcceptanceRecord](t, events, childLifecycleAcceptanceEventRole)
	if acceptance.TerminalID != terminal.TerminalID || acceptance.Decision != autonomy.ChildAcceptanceAccepted {
		t.Fatalf("unexpected workbranch acceptance record: %#v", acceptance)
	}
	if len(acceptance.AcceptedPatchRefs) == 0 {
		t.Fatalf("workbranch acceptance must carry accepted patch refs: %#v", acceptance)
	}

	if invalid := lifecycleEventRoles(events, "child_lifecycle_record_invalid"); len(invalid) > 0 {
		t.Fatalf("lifecycle records should validate before emission, invalid events: %#v", invalid)
	}
}

func TestWorkbranchChildToolEventsCarryLifecycleIDs(t *testing.T) {
	root := t.TempDir()
	runLifecycleTestGit(t, root, "init")
	runLifecycleTestGit(t, root, "config", "user.email", "test@example.com")
	runLifecycleTestGit(t, root, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(root, "feature.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("seed feature: %v", err)
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
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		OnEvent: func(event RunEvent) {
			events = append(events, event)
		},
	}

	result := runner.runChildTask(context.Background(), workspace.ToolRequest{
		Tool:   "task",
		Agent:  "worker",
		Mode:   "workbranch",
		Prompt: "Update feature.txt from before to after.",
	}, llama.ChatOptions{Model: "test-model"})
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("workbranch child returned error: %s\n%s", result.Error, result.Output)
	}

	invocation := mustFindLifecycleRecord[autonomy.ParentChildInvocationRecord](t, events, childLifecycleInvocationEventRole)
	requestEvent := mustFindLifecycleRecord[ToolRequestEvent](t, events, "child_tool_request")
	if requestEvent.ChildInvocationID != invocation.InvocationID || requestEvent.ChildContractID != invocation.ChildContractID {
		t.Fatalf("workbranch child tool request missing lifecycle IDs: %#v", requestEvent)
	}
	resultEvent := mustFindLifecycleRecord[ToolResultEvent](t, events, "child_tool_result")
	if resultEvent.ChildInvocationID != invocation.InvocationID || resultEvent.ChildContractID != invocation.ChildContractID {
		t.Fatalf("workbranch child tool result missing lifecycle IDs: %#v", resultEvent)
	}
}

func runLifecycleTestGit(t *testing.T, root string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func mustFindLifecycleRecord[T any](t *testing.T, events []RunEvent, role string) T {
	t.Helper()

	var zero T
	for _, event := range events {
		if event.Role != role {
			continue
		}
		var record T
		if err := json.Unmarshal([]byte(event.Content), &record); err != nil {
			t.Fatalf("unmarshal %s: %v\n%s", role, err, event.Content)
		}
		if validatable, ok := any(&record).(interface{ Validate() error }); ok {
			if err := validatable.Validate(); err != nil {
				t.Fatalf("validate %s: %v\n%#v", role, err, record)
			}
		}
		return record
	}

	t.Fatalf("missing lifecycle event role %q; roles=%v", role, lifecycleEventRoles(events, ""))
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
