package agent

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
)

var _ = ginkgo.It("ReadOnlyInvestigationStrategyPromptOnlyMentionsVisibleDiscoveryTools", func() {
	locateTools := []string{
		workspace.ToolNamePwd,
		workspace.ToolNameSearchFiles,
		workspace.ToolNameSearchContent,
		workspace.ToolNameReadFile,
		workspace.ToolNameCodeNavigate,
		workspace.ToolNameContextQuery,
		workspace.ToolNameContextZoom,
	}

	prompt := readOnlyInvestigationStrategyPrompt(locateTools)
	for _, forbidden := range []string{workspace.ToolNameContextPack, workspace.ToolNameDiscoveryQuery} {
		gomega.Expect(prompt).NotTo(gomega.ContainSubstring(forbidden), "locate/read-only prompt mentioned hidden tool %q", forbidden)
	}

	for _, want := range []string{workspace.ToolNameContextQuery, workspace.ToolNameSearchContent} {
		gomega.Expect(prompt).To(gomega.ContainSubstring(want), "locate/read-only prompt should mention visible strategy tool %q", want)
	}

	broadTools := append(append([]string(nil), locateTools...), workspace.ToolNameContextPack, workspace.ToolNameDiscoveryQuery)
	broadPrompt := readOnlyInvestigationStrategyPrompt(broadTools)
	for _, want := range []string{workspace.ToolNameContextPack, workspace.ToolNameDiscoveryQuery} {
		gomega.Expect(broadPrompt).To(gomega.ContainSubstring(want), "broad read-only prompt should keep visible discovery tool %q", want)
	}
})

var _ = ginkgo.It("NarrowToolFamilyRepetitionNudgeOnlyMentionsVisibleDiscoveryTools", func() {
	generalTools := []string{
		workspace.ToolNameSearchFiles,
		workspace.ToolNameSearchContent,
		workspace.ToolNameReadFile,
		workspace.ToolNameCodeNavigate,
		workspace.ToolNameReadOnlyCapability,
		workspace.ToolNameHarnessAudit,
		workspace.ToolNameContextStatus,
		workspace.ToolNameContextQuery,
		workspace.ToolNameContextZoom,
	}

	prompt := narrowToolFamilyRepetitionNudgePrompt("code_navigate", narrowToolFamilyNudgeAt, generalTools)
	for _, forbidden := range []string{workspace.ToolNameContextPack, workspace.ToolNameDiscoveryQuery} {
		gomega.Expect(prompt).NotTo(gomega.ContainSubstring(forbidden), "general read-only nudge mentioned hidden tool %q", forbidden)
	}

	for _, want := range []string{workspace.ToolNameContextQuery, workspace.ToolNameHarnessAudit, workspace.ToolNameSearchContent} {
		gomega.Expect(prompt).To(gomega.ContainSubstring(want), "general read-only nudge should mention visible strategy tool %q", want)
	}

	broadTools := append(append([]string(nil), generalTools...), workspace.ToolNameContextPack, workspace.ToolNameDiscoveryQuery)
	broadPrompt := narrowToolFamilyRepetitionNudgePrompt("code_navigate", narrowToolFamilyNudgeAt, broadTools)
	for _, want := range []string{workspace.ToolNameContextPack, workspace.ToolNameDiscoveryQuery} {
		gomega.Expect(broadPrompt).To(gomega.ContainSubstring(want), "broad read-only nudge should keep visible discovery tool %q", want)
	}
})

var _ = ginkgo.It("AnswerReadinessDoesNotInvalidateAuthorizedInspection", func() {
	gomega.Expect(closurePromptInvalidActions("answer_readiness_reached")).To(gomega.BeEmpty())
	gomega.Expect(closurePromptInvalidActions("child_packet_available")).To(gomega.BeEmpty())
})
