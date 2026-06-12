package agent

import (
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/workerruntime"
)

func TestVerificationInterceptRedirectsReadOnAlreadyEditedRequiredPath(t *testing.T) {
	// Replays the Colony donkey-run shape: 4 of 8 required paths are
	// written, then the model issues a code_navigate file_outline against
	// one of the ✓ paths to "verify the edit landed". The intercept must
	// rewrite that into a redirect naming the next ✗ path so the model
	// pivots to writing instead of looping verification.
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{
				"native/src/core/animal_types.h",
				"native/src/core/constants/animals.h",
				"native/src/simulation/creatures/animal_defs.cpp",
				"native/src/simulation/economy/energy.cpp",
			},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited native/src/core/animal_types.h"},
		{Tool: "edit_file", Summary: "Edited native/src/simulation/economy/energy.cpp"},
		// Prior read-class result — without this the intercept would
		// treat the new request as the first read after a write and
		// let it through (legitimate prep). The donkey-run pattern is
		// reads stacked on reads, which this fixture replicates.
		{Tool: "code_navigate", Summary: "Code navigation routed file_outline via cpp_file_outline"},
	}
	got := runner.preflightVerificationOnAlreadyEditedPath(
		workspace.ToolRequest{
			Tool:       "code_navigate",
			Capability: "file_outline",
			Path:       "native/src/core/animal_types.h",
		},
		results,
	)

	if !got.Intercepted {
		t.Fatalf("expected intercept on read of already-edited required path, got %#v", got)
	}

	if got.Result.Tool != "code_navigate" {
		t.Fatalf("synthesized result should preserve tool name, got %q", got.Result.Tool)
	}

	if !strings.Contains(got.Result.Error, "native/src/core/constants/animals.h") {
		t.Fatalf("redirect should name the first pending path, got: %s", got.Result.Error)
	}

	if !strings.Contains(got.Result.Error, "edit_file") || !strings.Contains(got.Result.Error, "<<QUESTION>>") {
		t.Fatalf("redirect should name the actionable next step and the escape hatch, got: %s", got.Result.Error)
	}

	if !strings.Contains(got.Reason, "next:") {
		t.Fatalf("telemetry reason should name the next path, got: %q", got.Reason)
	}
}

func TestVerificationInterceptHonorsAllPathsCovered(t *testing.T) {
	// Once every required path is on the ✓ list, the intercept must
	// stand down — the run is at the final-answer phase and any
	// follow-up reads are the model's normal closeout behavior, not the
	// stall pattern this guard fights.
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited a.txt"},
		{Tool: "edit_file", Summary: "Edited b.txt"},
	}
	got := runner.preflightVerificationOnAlreadyEditedPath(
		workspace.ToolRequest{Tool: "read_file", Path: "a.txt"},
		results,
	)

	if got.Intercepted {
		t.Fatalf("intercept should stand down when no ✗ paths remain, got %#v", got)
	}
}

func TestVerificationInterceptLetsFirstReadAfterWriteThroughForPrep(t *testing.T) {
	// Legitimate flow: model writes a.txt, then reads a.txt with a
	// named_gap so it can mirror that structure into b.txt's edit. The
	// intercept must NOT block this single read — only stacked reads
	// (read-after-read) are the verification-spree pattern.
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited a.txt"},
	}
	got := runner.preflightVerificationOnAlreadyEditedPath(
		workspace.ToolRequest{
			Tool: "read_file",
			Path: "a.txt",
		},
		results,
	)

	if got.Intercepted {
		t.Fatalf("first read after a write must pass through (prep for next ✗ path), got %#v", got)
	}
}

func TestVerificationInterceptIgnoresUnrelatedReads(t *testing.T) {
	// Reads of paths NOT in required_edit_paths are legitimate context
	// gathering (cross-file includes, supporting headers, etc.). The
	// intercept must not block them.
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited a.txt"},
	}
	got := runner.preflightVerificationOnAlreadyEditedPath(
		workspace.ToolRequest{Tool: "read_file", Path: "supporting/header.h"},
		results,
	)

	if got.Intercepted {
		t.Fatalf("intercept must not fire on reads of paths outside required_edit_paths, got %#v", got)
	}
}

func TestVerificationInterceptIgnoresWritesAndSearches(t *testing.T) {
	// edit_file on a ✓ path is not verification — the model could be
	// fixing up the prior edit. search_content / search_files are aimed
	// at literal text inside files and are the right tool to verify
	// expression presence, so they must remain available even on ✓
	// paths. Only the read-class verification tools get intercepted.
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited a.txt"},
	}
	cases := []workspace.ToolRequest{
		{Tool: "edit_file", Path: "a.txt"},
		{Tool: "apply_patch", Content: "*** Begin Patch\n*** End Patch"},
		{Tool: "search_content", Path: "a.txt", Pattern: "FOO"},
		{Tool: "search_files", Pattern: "FOO"},
		{Tool: "shell", Command: "ls"},
	}

	for _, req := range cases {
		got := runner.preflightVerificationOnAlreadyEditedPath(req, results)
		if got.Intercepted {
			t.Fatalf("intercept must not fire on %q, got %#v", req.Tool, got)
		}
	}
}

func TestVerificationInterceptOnlyFiresOnVerificationCapabilitiesOfCodeNavigate(t *testing.T) {
	// code_navigate has many capabilities. file_outline and
	// read_symbol are the verification habits we target. find_symbol /
	// references / definition / calls / implementations / related_files
	// are real lookups that have value on already-edited paths
	// (e.g., "where is X used now that I added it") and must NOT be
	// intercepted.
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited a.txt"},
		{Tool: "code_navigate", Summary: "prior read"},
	}

	for _, capability := range []string{
		"find_symbol", "workspace_symbol", "definition", "references",
		"calls_from", "calls_into", "implementations", "related_files",
	} {
		got := runner.preflightVerificationOnAlreadyEditedPath(
			workspace.ToolRequest{
				Tool:       "code_navigate",
				Capability: capability,
				Path:       "a.txt",
				Pattern:    "Foo",
			},
			results,
		)
		if got.Intercepted {
			t.Fatalf("code_navigate %s must pass through (it's a lookup, not verification); got %#v", capability, got)
		}
	}

	// Sanity check: file_outline and read_symbol DO trip the guard
	// under the same conditions.
	for _, capability := range []string{"file_outline", "outline", "read_symbol", "inspect_symbol", "symbol_body"} {
		got := runner.preflightVerificationOnAlreadyEditedPath(
			workspace.ToolRequest{
				Tool:       "code_navigate",
				Capability: capability,
				Path:       "a.txt",
				Pattern:    "Foo",
			},
			results,
		)
		if !got.Intercepted {
			t.Fatalf("code_navigate %s must trip the verification guard; got %#v", capability, got)
		}
	}
}

func TestVerificationInterceptIgnoresBriefsWithoutRequiredPaths(t *testing.T) {
	// Briefs that don't enumerate required_edit_paths (e.g. open-ended
	// debug or research lanes) must not get this directive — the model
	// is not in "implement N files" mode, so the verification urge is
	// not the failure pattern we're targeting.
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "research",
			Contract:          "research_result",
			PermissionProfile: "guarded_workspace_read",
		},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited a.txt"},
	}
	got := runner.preflightVerificationOnAlreadyEditedPath(
		workspace.ToolRequest{Tool: "read_file", Path: "a.txt"},
		results,
	)

	if got.Intercepted {
		t.Fatalf("intercept must stand down without required_edit_paths, got %#v", got)
	}
}

func TestVerificationInterceptAppliesAcrossLanguageReadSymbolTools(t *testing.T) {
	// The intercept must catch direct *_read_symbol / *_file_outline
	// dispatches, not just code_navigate, because the model can route
	// either way depending on the brief and the tool surface.
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"x.cpp", "y.cpp"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited x.cpp"},
		// Stacked-read precondition: the model has already done one
		// read since the write, so the next read on a ✓ path is the
		// verification-spree pattern this guard targets.
		{Tool: "code_navigate", Summary: "prior read"},
	}

	for _, tool := range []string{
		"cpp_file_outline", "cpp_read_symbol",
		"go_file_outline", "go_read_symbol",
		"python_file_outline", "python_read_symbol",
		"ts_file_outline", "ts_read_symbol",
		"rust_file_outline", "rust_read_symbol",
		"java_file_outline", "java_read_symbol",
		"read_file",
	} {
		got := runner.preflightVerificationOnAlreadyEditedPath(
			workspace.ToolRequest{Tool: tool, Path: "x.cpp", Pattern: "Foo"},
			results,
		)
		if !got.Intercepted {
			t.Fatalf("intercept should fire on %q against ✓ required path, got %#v", tool, got)
		}

		if !strings.Contains(got.Result.Error, "y.cpp") {
			t.Fatalf("redirect for %q should name the pending path y.cpp, got: %s", tool, got.Result.Error)
		}
	}
}
