package writerrouting

import (
	"idiom/internal/contracts/writerroute"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/backends"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Writer route decisions", func() {
	It("prefers a directly configured model while retaining role aliases as context", func() {
		cfg := config.Default()
		cfg.LlamaServer.Model = "deepseek-v4-flash"

		decision := BuildDecision(cfg, DecisionOptions{
			Mode:        writerroute.ModeReadOnly,
			Lane:        "main",
			RoleAliases: map[string]string{backends.RoleClassifier: "deepseek-v4-pro"},
		})

		Expect(decision.EffectiveModel).To(Equal("deepseek-v4-flash"))
		Expect(decision.RoleAliases).To(HaveKeyWithValue(backends.RoleClassifier, "deepseek-v4-pro"))
	})

	It("falls back to the role alias when no direct model is configured", func() {
		cfg := config.Default()
		cfg.LlamaServer.Model = ""

		decision := BuildDecision(cfg, DecisionOptions{
			Mode:        writerroute.ModeReadOnly,
			Lane:        "main",
			RoleAliases: map[string]string{backends.RoleClassifier: "deepseek-v4-pro"},
		})

		Expect(decision.EffectiveModel).To(Equal("deepseek-v4-pro"))
	})

	It("uses the display model for every role when role-specific models are absent", func() {
		cfg := config.Default()
		cfg.LlamaServer.Model = "/models/qwen/qwen3.gguf"
		cfg.LlamaServer.DisplayName = "qwen-display"

		decision := BuildDecision(cfg, DecisionOptions{Mode: writerroute.ModeReadOnly, Lane: "main"})

		Expect(decision.PlannerModel).To(Equal("qwen-display"))
		Expect(decision.ExecutorModel).To(Equal("qwen-display"))
		Expect(decision.ClassifierModel).To(Equal("qwen-display"))
		Expect(decision.EffectiveModel).To(Equal("qwen-display"))
	})

	It("keeps explicit CLI model overrides authoritative", func() {
		cfg := config.Default()
		cfg.LlamaServer.Model = "base-model"
		cfg.Implementer = &config.LlamaServer{Model: "implementer-model"}
		cfg.RouteClassifier.Model = "classifier-config"

		decision := BuildDecision(cfg, DecisionOptions{
			Mode:            writerroute.ModeAgenticGuarded,
			Lane:            "implement",
			PlannerModel:    "planner-override",
			ExecutorModel:   "executor-override",
			ClassifierModel: "classifier-override",
		})

		Expect(decision.PlannerModel).To(Equal("planner-override"))
		Expect(decision.ExecutorModel).To(Equal("executor-override"))
		Expect(decision.ClassifierModel).To(Equal("classifier-override"))
		Expect(decision.EffectiveModel).To(Equal("executor-override"))
	})
})
