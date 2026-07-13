package app

import (
	"path/filepath"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/runprofile"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/promptstack"
)

var _ = ginkgo.Describe("shared prompt execution derivations", ginkgo.Label("race-contract"), func() {
	ginkgo.It("preserves model, mode, context, project, and server precedence", func() {
		root := ginkgo.GinkgoT().TempDir()
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root})

		gomega.Expect(activeModelDisplayID(" display ", "model")).To(gomega.Equal("display"))
		gomega.Expect(activeModelDisplayID("", " model ")).To(gomega.Equal("model"))
		gomega.Expect(compactionContextSize(8192, 4096)).To(gomega.Equal(8192))
		gomega.Expect(compactionContextSize(0, 4096)).To(gomega.Equal(4096))
		gomega.Expect(compactionContextSize(0, 0)).To(gomega.Equal(65536))
		gomega.Expect(currentMode(runprofile.AgentModePlan, runprofile.AgentModeGeneral).Name).To(gomega.Equal(runprofile.AgentModePlan))
		gomega.Expect(currentMode("", runprofile.AgentModeGeneral).Name).To(gomega.Equal(runprofile.AgentModeGeneral))
		gomega.Expect(resolveProjectRoot(" configured ", manager)).To(gomega.Equal("configured"))
		gomega.Expect(resolveProjectRoot("", manager)).To(gomega.Equal(manager.Root()))
		gomega.Expect(resolveProjectRoot("", nil)).To(gomega.BeEmpty())

		spec := config.LlamaServer{Kind: "hosted", BaseURL: "https://example.invalid/v1", Model: "model-a", DisplayName: "Model A"}
		source := currentServerStatusSource(7, nil, spec)
		gomega.Expect(source.backendRevision).To(gomega.Equal(uint64(7)))
		gomega.Expect(source.controller).To(gomega.BeNil())
		gomega.Expect(source.kind).To(gomega.Equal(spec.Kind))
		gomega.Expect(source.endpoint).To(gomega.Equal(spec.EndpointBaseURL()))
		gomega.Expect(source.model).To(gomega.Equal(spec.Model))
		gomega.Expect(source.displayName).To(gomega.Equal(spec.DisplayName))
	})

	ginkgo.It("owns prompt composition caching without sharing mutable results", func() {
		derivation := derivePromptComposition(
			runprofile.AgentModePlan,
			runprofile.AgentModeGeneral,
			"qwen",
			" planner-model ",
			"configured-model",
		)
		state := promptRuntimeCompletionState{}
		stack := promptstack.Stack{
			Text:    " Base behavior ",
			Sources: []promptstack.Source{{Kind: "baseline", DisplayName: "base.md"}},
		}
		modeLoads := 0
		loadModeFragments := func(existing []promptstack.Source) systemPromptFragments {
			modeLoads++
			gomega.Expect(existing).To(gomega.ContainElement(promptstack.Source{Kind: "model_overlay", DisplayName: "model:qwen"}))

			return systemPromptFragments{
				Text:    " Plan behavior ",
				Sources: []promptstack.Source{{Kind: "mode", DisplayName: "mode:plan"}},
			}
		}

		first := state.resolveComposedPrompt(stack, derivation, loadModeFragments)
		gomega.Expect(first.text).To(gomega.ContainSubstring("Base behavior"))
		gomega.Expect(first.text).To(gomega.ContainSubstring("model compatibility: model:qwen"))
		gomega.Expect(first.text).To(gomega.ContainSubstring("Plan behavior"))
		gomega.Expect(modeLoads).To(gomega.Equal(1))

		first.sources[0].DisplayName = "mutated result"
		second := state.resolveComposedPrompt(stack, derivation, func([]promptstack.Source) systemPromptFragments {
			ginkgo.Fail("cached prompt unexpectedly reloaded dynamic mode fragments")

			return systemPromptFragments{}
		})
		gomega.Expect(second.sources[0].DisplayName).To(gomega.Equal("base.md"))
		gomega.Expect(modeLoads).To(gomega.Equal(1))

		state.invalidateComposedPrompt()
		gomega.Expect(state.composedPrompt).To(gomega.BeEmpty())
		gomega.Expect(state.composedPromptSources).To(gomega.BeNil())
		gomega.Expect(state.composedPromptCacheKey).To(gomega.BeEmpty())
		_ = state.resolveComposedPrompt(stack, derivation, loadModeFragments)
		gomega.Expect(modeLoads).To(gomega.Equal(2))
	})

	ginkgo.It("builds isolated chat options and preserves thinking overrides", func() {
		thinking := true
		chat := config.Chat{
			Temperature:       0.25,
			TopP:              0.9,
			TopK:              40,
			MinP:              0.1,
			PresencePenalty:   0.2,
			RepetitionPenalty: 1.1,
			EnableThinking:    &thinking,
			PreserveThinking:  true,
			MaxTokens:         2048,
		}

		options := chatOptions(chat, "model-a", true, false)
		gomega.Expect(options.Model).To(gomega.Equal("model-a"))
		gomega.Expect(options.EnableThinking).NotTo(gomega.BeIdenticalTo(chat.EnableThinking))
		gomega.Expect(options.EnableThinking).NotTo(gomega.BeNil())
		gomega.Expect(*options.EnableThinking).To(gomega.BeTrue())
		gomega.Expect(options.PreserveThinking).To(gomega.BeTrue())
		gomega.Expect(options.ChatTemplateKwargs).To(gomega.BeFalse())
		gomega.Expect(options.ModelThinkingFloor).To(gomega.BeTrue())
		gomega.Expect(options.MaxTokens).To(gomega.Equal(2048))

		overridden := chatOptions(chat, "model-a", false, true)
		gomega.Expect(overridden.EnableThinking).NotTo(gomega.BeNil())
		gomega.Expect(*overridden.EnableThinking).To(gomega.BeFalse())
		gomega.Expect(overridden.PreserveThinking).To(gomega.BeFalse())
		gomega.Expect(overridden.ChatTemplateKwargs).To(gomega.BeTrue())
		gomega.Expect(thinking).To(gomega.BeTrue())
	})

	ginkgo.It("derives planning artifacts and marks only the requested approval rows", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(planningArtifactPath(root, " session-a ", ".diffs.jsonl")).To(gomega.Equal(filepath.Join(root, planDirName, "session-a.diffs.jsonl")))
		gomega.Expect(planningArtifactPath(root, "", ".md")).To(gomega.BeEmpty())

		lines := []string{
			"- [ ] Approve planned edits " + formatPlanApprovalAnchor(planApprovalActionCurrent),
			"- [ ] Approve all edits " + formatPlanApprovalAnchor(planApprovalActionAll),
			"- [ ] Ordinary plan item",
		}
		currentOnly := markPlanApprovalLines(append([]string(nil), lines...), false)
		gomega.Expect(currentOnly[0]).To(gomega.ContainSubstring("[x]"))
		gomega.Expect(currentOnly[1]).To(gomega.ContainSubstring("[ ]"))
		gomega.Expect(currentOnly[2]).To(gomega.Equal(lines[2]))

		blanket := markPlanApprovalLines(append([]string(nil), lines...), true)
		gomega.Expect(blanket[0]).To(gomega.ContainSubstring("[x]"))
		gomega.Expect(blanket[1]).To(gomega.ContainSubstring("[x]"))
		gomega.Expect(strings.Count(strings.Join(blanket, "\n"), "[x]")).To(gomega.Equal(2))
	})
})
