package agent

import (
	"strings"
	"testing"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

func TestTextualWriteGateDirectImplementationRejectsUnsafeWriteWithJSONGuidance(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	reply := "tool: edit_file\npath: internal/labels/labels.go\nold_text: return \"Save changes\"\nnew_text: return \"Save\"\ndescription: update label"
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/labels/labels.go lines 1-5 (total lines: 5)",
		Output:  "package labels\n\nfunc ShortLabel() string {\n\treturn \"Save changes\"\n}\n",
	}}

	got, rejected := textualToolCallCompletionRejection(runner, reply, results)

	if !rejected {
		t.Fatal("expected unsafe textual write syntax to be rejected")
	}
	for _, want := range []string{
		"key-value and function-call write syntax are not executable",
		"one visible JSON object",
		"`path`, `old_text`, `new_text`, and `description`",
		"Do not use key-value or function-call write syntax",
	} {
		if !strings.Contains(got.Error, want) {
			t.Fatalf("expected direct implementation gate message to contain %q, got %q", want, got.Error)
		}
	}
	if strings.Contains(got.Error, "submit_plan") {
		t.Fatalf("direct implementation gate should not point at planner submit_plan: %q", got.Error)
	}
}

func TestTextualWriteGatePlanExecutePointsAtSubmitPlan(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"internal/labels/labels.go"},
		},
	}
	reply := "tool: edit_file\npath: internal/labels/labels.go\nold_text: return \"Save changes\"\nnew_text: return \"Save\"\ndescription: update label"
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/labels/labels.go lines 1-5 (total lines: 5)",
		Output:  "package labels\n\nfunc ShortLabel() string {\n\treturn \"Save changes\"\n}\n",
	}}

	got, rejected := textualToolCallCompletionRejection(runner, reply, results)

	if !rejected {
		t.Fatal("expected unsafe textual write syntax to be rejected")
	}
	for _, want := range []string{
		"plan-execute planner phase does not execute per-edit writes",
		"one visible JSON object for `submit_plan`",
		"`rationale`, and `edits`",
		"Key-value and function-call write syntax are not executable",
	} {
		if !strings.Contains(got.Error, want) {
			t.Fatalf("expected plan-execute gate message to contain %q, got %q", want, got.Error)
		}
	}
	if strings.Contains(got.Error, "`path`, `old_text`, `new_text`") {
		t.Fatalf("plan-execute gate must not point at per-edit write JSON: %q", got.Error)
	}
}

func TestTextualWriteGateReadOnlyKeepsGenericToolMarkupRejection(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: "read_only_workspace",
		},
	}
	reply := "tool: edit_file\npath: internal/labels/labels.go\nold_text: old\nnew_text: new\ndescription: update label"

	got, rejected := textualToolCallCompletionRejection(runner, reply, nil)

	if !rejected {
		t.Fatal("expected textual write markup to be rejected in read-only lane")
	}
	for _, forbidden := range []string{"one visible JSON object", "`path`, `old_text`, `new_text`", "submit_plan"} {
		if strings.Contains(got.Error, forbidden) {
			t.Fatalf("read-only rejection must not advertise write recovery %q: %q", forbidden, got.Error)
		}
	}
	if !strings.Contains(got.Error, "textual tool-call markup") {
		t.Fatalf("read-only lane should keep generic textual-tool rejection, got %q", got.Error)
	}
}

func TestTextualWriteGatePreSourceImplementationKeepsGenericToolMarkupRejection(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	reply := "tool: edit_file\npath: internal/labels/labels.go\nold_text: old\nnew_text: new\ndescription: update label"

	got, rejected := textualToolCallCompletionRejection(runner, reply, nil)

	if !rejected {
		t.Fatal("expected textual write markup to be rejected before source evidence")
	}
	if strings.Contains(got.Error, "one visible JSON object") ||
		strings.Contains(got.Error, "`path`, `old_text`, `new_text`") {
		t.Fatalf("pre-source implementation rejection must not advertise write-ready recovery: %q", got.Error)
	}
}

func TestTextualWriteGatePostWriteVerificationKeepsGenericToolMarkupRejection(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	reply := "tool: edit_file\npath: internal/labels/labels.go\nold_text: old\nnew_text: new\ndescription: update label"
	results := []workspace.ToolResult{{
		Tool:    "edit_file",
		Summary: "Edited internal/labels/labels.go",
	}}

	got, rejected := textualToolCallCompletionRejection(runner, reply, results)

	if !rejected {
		t.Fatal("expected textual write markup to be rejected after a write")
	}
	if strings.Contains(got.Error, "one visible JSON object") ||
		strings.Contains(got.Error, "`path`, `old_text`, `new_text`") {
		t.Fatalf("post-write verification rejection must not advertise another write recovery: %q", got.Error)
	}
}
