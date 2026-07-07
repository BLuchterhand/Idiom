package workspace

import (
	"slices"
	"testing"
)

func TestToolSurfaceProfilesExposeContractInvariants(t *testing.T) {
	if got := ManifestToolsForSurfaceProfile(ToolSurfaceProfileArtifactDistiller); len(got) != 0 {
		t.Fatalf("artifact distiller profile must be tool-free, got %#v", got)
	}

	conversation := ManifestToolsForSurfaceProfile(ToolSurfaceProfileConversationReadOnly)
	for _, want := range []string{
		ToolNameTask,
		ToolNameReadFile,
		ToolNameSearchContent,
		ToolNameCodeNavigate,
		ToolNameReadOnlyCapability,
		ToolNameHarnessAudit,
		ToolNameContextStatus,
		ToolNameContextQuery,
		ToolNameContextZoom,
		ToolNameHistoryRange,
		ToolNameHistorySymbol,
		ToolNameBlameRange,
	} {
		if !slices.Contains(conversation, want) {
			t.Fatalf("conversation read-only profile missing %q in %#v", want, conversation)
		}
	}

	for _, forbidden := range []string{
		ToolNameEditFile,
		ToolNameApplyPatch,
		ToolNameShell,
		ToolNameContextPack,
		ToolNameContextImpact,
		ToolNameContextTracePack,
		ToolNameDiscoveryQuery,
	} {
		if slices.Contains(conversation, forbidden) {
			t.Fatalf("conversation read-only profile must not expose %q in %#v", forbidden, conversation)
		}
	}

	delegated := ManifestToolsForSurfaceProfile(ToolSurfaceProfileDelegatedReadOnly)
	for _, forbidden := range []string{ToolNameTask, ToolNameListFiles, ToolNameEditFile, ToolNameApplyPatch} {
		if slices.Contains(delegated, forbidden) {
			t.Fatalf("delegated read-only profile must not expose %q in %#v", forbidden, delegated)
		}
	}
	for _, want := range []string{ToolNameReadFile, ToolNameSearchContent, ToolNameCodeNavigate, ToolNameDiscoveryQuery} {
		if !slices.Contains(delegated, want) {
			t.Fatalf("delegated read-only profile missing %q in %#v", want, delegated)
		}
	}

	verification := ManifestToolsForSurfaceProfile(ToolSurfaceProfileVerificationOnly)
	for _, forbidden := range []string{ToolNameReadOnlyCapability, ToolNameEditFile, ToolNameApplyPatch, ToolNameSubmitPlan} {
		if slices.Contains(verification, forbidden) {
			t.Fatalf("verification-only profile must not expose %q in %#v", forbidden, verification)
		}
	}
	for _, want := range []string{ToolNameSourceSanity, ToolNameShell, ToolNameReadFile, ToolNameSearchContent} {
		if !slices.Contains(verification, want) {
			t.Fatalf("verification-only profile missing %q in %#v", want, verification)
		}
	}

	guardedWrite := ManifestToolsForSurfaceProfile(ToolSurfaceProfileGuardedWrite)
	for _, want := range []string{ToolNameTask, ToolNameTaskLedger, ToolNameEditFile, ToolNameApplyPatch, ToolNameSourceSanity} {
		if !slices.Contains(guardedWrite, want) {
			t.Fatalf("guarded write profile missing %q in %#v", want, guardedWrite)
		}
	}
}

func TestSurfaceProfilesOwnExternalToolPolicy(t *testing.T) {
	available := []string{
		ToolNameReadFile,
		ToolNameSubmitPlan,
		ToolNameEditFile,
		"mcp:question/ask",
		"project_trace",
	}

	planner := AllowedToolsForSurfaceProfile(available, ToolSurfaceProfilePlanSubmissionPlanner)
	for _, want := range []string{ToolNameReadFile, ToolNameSubmitPlan, "mcp:question/ask", "project_trace"} {
		if !slices.Contains(planner, want) {
			t.Fatalf("planner profile should preserve allowed external/read tool %q in %#v", want, planner)
		}
	}
	if slices.Contains(planner, ToolNameEditFile) {
		t.Fatalf("planner profile must not expose per-edit writes: %#v", planner)
	}

	review := AllowedToolsForSurfaceProfile(available, ToolSurfaceProfilePlanSubmissionReview)
	for _, forbidden := range []string{ToolNameEditFile, "mcp:question/ask", "project_trace"} {
		if slices.Contains(review, forbidden) {
			t.Fatalf("review profile must not expose %q in %#v", forbidden, review)
		}
	}
	if !slices.Contains(review, ToolNameSubmitPlan) {
		t.Fatalf("review profile must keep submit_plan for corrective plans: %#v", review)
	}
}

func TestToolSurfaceSnapshotAdvertisedAllowedToolsOwnsRuntimeFiltering(t *testing.T) {
	surface := ToolSurfaceSnapshot{}
	got := surface.AdvertisedAllowedTools(AdvertisedToolSurfaceInput{
		BaseTools:    []string{ToolNameTask, ToolNameListFiles, ToolNameStructuralSearch, ToolNameDependencyGraph, ToolNameReadFile},
		RuntimeTools: []string{ToolNameStructuralSearch, ToolNameDependencyGraph, ToolNameTask, ToolNameListFiles},
	})
	want := []string{ToolNameStructuralSearch, ToolNameDependencyGraph, ToolNameTask, ToolNameListFiles}
	if !slices.Equal(got, want) {
		t.Fatalf("advertised runtime tools = %#v, want %#v", got, want)
	}

	got = surface.AdvertisedAllowedTools(AdvertisedToolSurfaceInput{
		BaseTools:       []string{ToolNameTask, ToolNameReadFile, ToolNameSearchContent},
		DisableTaskTool: true,
	})
	want = []string{ToolNameReadFile, ToolNameSearchContent}
	if !slices.Equal(got, want) {
		t.Fatalf("task-disabled advertised tools = %#v, want %#v", got, want)
	}
}

func TestToolSurfaceSnapshotAdvertisedAllowedToolsOwnsConversationExternalPolicy(t *testing.T) {
	surface := ToolSurfaceSnapshot{
		externalToolNames: []string{"project_lookup", "trace_code"},
	}

	got := surface.AdvertisedAllowedTools(AdvertisedToolSurfaceInput{
		BaseTools:        []string{ToolNameReadFile, "trace_code", "project_lookup"},
		ConversationOnly: true,
	})
	want := []string{"trace_code", "project_lookup"}
	if !slices.Equal(got, want) {
		t.Fatalf("conversation-only advertised externals = %#v, want %#v", got, want)
	}

	got = surface.AdvertisedAllowedTools(AdvertisedToolSurfaceInput{
		ConversationOnly: true,
	})
	want = []string{"project_lookup", "trace_code"}
	if !slices.Equal(got, want) {
		t.Fatalf("conversation-only fallback externals = %#v, want %#v", got, want)
	}

	got = surface.AdvertisedAllowedTools(AdvertisedToolSurfaceInput{
		BaseTools:        []string{"project_lookup"},
		ConversationOnly: true,
		SuppressAll:      true,
	})
	if len(got) != 0 {
		t.Fatalf("suppressed advertised tools = %#v, want none", got)
	}
}

func TestPlanSubmissionProfilesOwnRequiredToolCompletion(t *testing.T) {
	for _, profile := range []ToolSurfaceProfile{
		ToolSurfaceProfilePlanSubmissionPlanner,
		ToolSurfaceProfilePlanSubmissionReview,
	} {
		got := AllowedToolsForSurfaceProfile([]string{ToolNameReadFile, ToolNameSearchContent}, profile)
		if !slices.Contains(got, ToolNameSubmitPlan) {
			t.Fatalf("%s profile must complete missing submit_plan: %#v", profile, got)
		}
	}
}

func TestDirectAnswerSurfaceToolNamesOwnReadOnlyInventory(t *testing.T) {
	tools := DirectAnswerSurfaceToolNames()
	seen := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		if tool == "" {
			t.Fatalf("direct-answer surface contains blank tool: %#v", tools)
		}
		if _, ok := seen[tool]; ok {
			t.Fatalf("direct-answer surface contains duplicate tool %q: %#v", tool, tools)
		}
		seen[tool] = struct{}{}
	}

	for _, tool := range []string{
		ToolNameReadFile,
		ToolNameSearchContent,
		ToolNameContextQuery,
		ToolNameContextZoom,
		ToolNameCodeNavigate,
		ToolNameTask,
		ToolNameSystemSearchAccess,
	} {
		if !slices.Contains(tools, tool) {
			t.Fatalf("direct-answer surface missing read-only investigation tool %q: %#v", tool, tools)
		}
	}

	for _, tool := range WorkspaceWriteToolNames() {
		if slices.Contains(tools, tool) {
			t.Fatalf("direct-answer surface must not expose write tool %q: %#v", tool, tools)
		}
	}
	for _, tool := range []string{ToolNameSubmitPlan, ToolNameTaskLedger, ToolNameNote, ToolNameShell} {
		if slices.Contains(tools, tool) {
			t.Fatalf("direct-answer surface must not expose protocol/mutation tool %q: %#v", tool, tools)
		}
	}
}

func TestBuiltInToolNamesDoNotLookExternal(t *testing.T) {
	for _, tool := range []string{
		ToolNameCodeNavigate,
		ToolNameReadOnlyCapability,
		ToolNameSourceSanity,
		ToolNameHarnessFeedback,
		ToolNameContextImpact,
		ToolNameSubmitPlan,
		ToolNameDelegateEdit,
		ToolNameRg,
		ToolNameNote,
	} {
		if IsExternalToolName(tool) {
			t.Fatalf("built-in tool %q must not be classified as a project external", tool)
		}
	}
}
