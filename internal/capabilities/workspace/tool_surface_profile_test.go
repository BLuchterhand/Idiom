package workspace

import (
	"fmt"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"slices"
)

var _ = ginkgo.Describe("tool surface profile", func() {
	ginkgo.It("tool surface profiles expose contract invariants", func() {
		if got := ManifestToolsForSurfaceProfile(ToolSurfaceProfileArtifactDistiller); len(got) != 0 {
			ginkgo.Fail(fmt.Sprintf("artifact distiller profile must be tool-free, got %#v", got))
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
			gomega.Expect(slices.Contains(conversation, want)).To(gomega.BeTrue(), fmt.Sprintf("conversation read-only profile missing %q in %#v", want, conversation))

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
			gomega.Expect(slices.Contains(conversation, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("conversation read-only profile must not expose %q in %#v", forbidden, conversation))

		}

		delegated := ManifestToolsForSurfaceProfile(ToolSurfaceProfileDelegatedReadOnly)
		for _, forbidden := range []string{ToolNameTask, ToolNameListFiles, ToolNameEditFile, ToolNameApplyPatch} {
			gomega.Expect(slices.Contains(delegated, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("delegated read-only profile must not expose %q in %#v", forbidden, delegated))

		}
		for _, want := range []string{ToolNameReadFile, ToolNameSearchContent, ToolNameCodeNavigate, ToolNameDiscoveryQuery} {
			gomega.Expect(slices.Contains(delegated, want)).To(gomega.BeTrue(), fmt.Sprintf("delegated read-only profile missing %q in %#v", want, delegated))

		}

		verification := ManifestToolsForSurfaceProfile(ToolSurfaceProfileVerificationOnly)
		for _, forbidden := range []string{ToolNameReadOnlyCapability, ToolNameEditFile, ToolNameApplyPatch, ToolNameSubmitPlan} {
			gomega.Expect(slices.Contains(verification, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("verification-only profile must not expose %q in %#v", forbidden, verification))

		}
		for _, want := range []string{ToolNameSourceSanity, ToolNameShell, ToolNameReadFile, ToolNameSearchContent} {
			gomega.Expect(slices.Contains(verification, want)).To(gomega.BeTrue(), fmt.Sprintf("verification-only profile missing %q in %#v", want, verification))

		}

		guardedWrite := ManifestToolsForSurfaceProfile(ToolSurfaceProfileGuardedWrite)
		for _, want := range []string{ToolNameTask, ToolNameTaskLedger, ToolNameEditFile, ToolNameApplyPatch, ToolNameSourceSanity} {
			gomega.Expect(slices.Contains(guardedWrite, want)).To(gomega.BeTrue(), fmt.Sprintf("guarded write profile missing %q in %#v", want, guardedWrite))

		}
	})
	ginkgo.It("surface profiles own external tool policy", func() {
		available := []string{
			ToolNameReadFile,
			ToolNameSubmitPlan,
			ToolNameEditFile,
			"mcp:question/ask",
			"project_trace",
		}

		planner := AllowedToolsForSurfaceProfile(available, ToolSurfaceProfilePlanSubmissionPlanner)
		for _, want := range []string{ToolNameReadFile, ToolNameSubmitPlan, "mcp:question/ask", "project_trace"} {
			gomega.Expect(slices.Contains(planner, want)).To(gomega.BeTrue(), fmt.Sprintf("planner profile should preserve allowed external/read tool %q in %#v", want, planner))

		}
		gomega.Expect(slices.Contains(planner, ToolNameEditFile)).To(gomega.BeFalse(), fmt.Sprintf("planner profile must not expose per-edit writes: %#v", planner))

		review := AllowedToolsForSurfaceProfile(available, ToolSurfaceProfilePlanSubmissionReview)
		for _, forbidden := range []string{ToolNameEditFile, "mcp:question/ask", "project_trace"} {
			gomega.Expect(slices.Contains(review, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("review profile must not expose %q in %#v", forbidden, review))

		}
		gomega.Expect(slices.Contains(review, ToolNameSubmitPlan)).To(gomega.BeTrue(), fmt.Sprintf("review profile must keep submit_plan for corrective plans: %#v", review))

	})
	ginkgo.It("tool surface snapshot advertised allowed tools owns runtime filtering", func() {
		surface := ToolSurfaceSnapshot{}
		got := surface.AdvertisedAllowedTools(AdvertisedToolSurfaceInput{
			BaseTools:    []string{ToolNameTask, ToolNameListFiles, ToolNameStructuralSearch, ToolNameDependencyGraph, ToolNameReadFile},
			RuntimeTools: []string{ToolNameStructuralSearch, ToolNameDependencyGraph, ToolNameTask, ToolNameListFiles},
		})
		want := []string{ToolNameStructuralSearch, ToolNameDependencyGraph, ToolNameTask, ToolNameListFiles}
		gomega.Expect(slices.Equal(got, want)).To(gomega.BeTrue(), fmt.Sprintf("advertised runtime tools = %#v, want %#v", got, want))

		got = surface.AdvertisedAllowedTools(AdvertisedToolSurfaceInput{
			BaseTools:       []string{ToolNameTask, ToolNameReadFile, ToolNameSearchContent},
			DisableTaskTool: true,
		})
		want = []string{ToolNameReadFile, ToolNameSearchContent}
		gomega.Expect(slices.Equal(got, want)).To(gomega.BeTrue(), fmt.Sprintf("task-disabled advertised tools = %#v, want %#v", got, want))

	})
	ginkgo.It("tool surface snapshot advertised allowed tools owns conversation external policy", func() {
		surface := ToolSurfaceSnapshot{
			externalToolNames: []string{"project_lookup", "trace_code"},
		}

		got := surface.AdvertisedAllowedTools(AdvertisedToolSurfaceInput{
			BaseTools:        []string{ToolNameReadFile, "trace_code", "project_lookup"},
			ConversationOnly: true,
		})
		want := []string{"trace_code", "project_lookup"}
		gomega.Expect(slices.Equal(got, want)).To(gomega.BeTrue(), fmt.Sprintf("conversation-only advertised externals = %#v, want %#v", got, want))

		got = surface.AdvertisedAllowedTools(AdvertisedToolSurfaceInput{
			ConversationOnly: true,
		})
		want = []string{"project_lookup", "trace_code"}
		gomega.Expect(slices.Equal(got, want)).To(gomega.BeTrue(), fmt.Sprintf("conversation-only fallback externals = %#v, want %#v", got, want))

		got = surface.AdvertisedAllowedTools(AdvertisedToolSurfaceInput{
			BaseTools:        []string{"project_lookup"},
			ConversationOnly: true,
			SuppressAll:      true,
		})
		gomega.Expect(got).To(gomega.BeEmpty(), fmt.Sprintf("suppressed advertised tools = %#v, want none", got))

	})
	ginkgo.It("plan submission profiles own required tool completion", func() {
		for _, profile := range []ToolSurfaceProfile{
			ToolSurfaceProfilePlanSubmissionPlanner,
			ToolSurfaceProfilePlanSubmissionReview,
		} {
			got := AllowedToolsForSurfaceProfile([]string{ToolNameReadFile, ToolNameSearchContent}, profile)
			gomega.Expect(slices.Contains(got, ToolNameSubmitPlan)).To(gomega.BeTrue(), fmt.Sprintf("%s profile must complete missing submit_plan: %#v", profile, got))

		}
	})
	ginkgo.It("direct answer surface tool names own read only inventory", func() {
		tools := DirectAnswerSurfaceToolNames()
		seen := make(map[string]struct{}, len(tools))
		for _, tool := range tools {
			gomega.Expect(tool).NotTo(gomega.Equal(""), fmt.Sprintf("direct-answer surface contains blank tool: %#v", tools))

			if _, ok := seen[tool]; ok {
				ginkgo.Fail(fmt.Sprintf("direct-answer surface contains duplicate tool %q: %#v", tool, tools))
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
			gomega.Expect(slices.Contains(tools, tool)).To(gomega.BeTrue(), fmt.Sprintf("direct-answer surface missing read-only investigation tool %q: %#v", tool, tools))

		}

		for _, tool := range WorkspaceWriteToolNames() {
			gomega.Expect(slices.Contains(tools, tool)).To(gomega.BeFalse(), fmt.Sprintf("direct-answer surface must not expose write tool %q: %#v", tool, tools))

		}
		for _, tool := range []string{ToolNameSubmitPlan, ToolNameTaskLedger, ToolNameNote, ToolNameShell} {
			gomega.Expect(slices.Contains(tools, tool)).To(gomega.BeFalse(), fmt.Sprintf("direct-answer surface must not expose protocol/mutation tool %q: %#v", tool, tools))

		}
	})
	ginkgo.It("built in tool names do not look external", func() {
		for _, tool := range []string{
			ToolNameCodeNavigate,
			ToolNameReadOnlyCapability,
			ToolNameSourceSanity,
			ToolNameHarnessFeedback,
			ToolNameContextImpact,
			ToolNameSubmitPlan,
			ToolNameRg,
			ToolNameNote,
		} {
			gomega.Expect(IsExternalToolName(tool)).To(gomega.BeFalse(), fmt.Sprintf("built-in tool %q must not be classified as a project external", tool))

		}
	})
})
