package autodetect

import (
	"os"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Model autodetection", func() {
	ginkgo.It("extracts normalized aliases from Hugging Face cache directories", func() {
		dir := ginkgo.GinkgoT().TempDir()
		model := filepath.Join(dir, "models--Qwen--Qwen2.5-Coder-7B")
		gomega.Expect(os.MkdirAll(filepath.Join(model, "snapshots", "deadbeef"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(model, "snapshots", "deadbeef", "config.json"), []byte("{}"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.MkdirAll(filepath.Join(dir, "datasets--squad"), 0o755)).To(gomega.Succeed())

		models := scanHuggingFaceCache(dir)
		gomega.Expect(models).To(gomega.HaveLen(1))
		gomega.Expect(models[0].Alias).To(gomega.Equal("qwen2.5-coder-7b"))
		gomega.Expect(models[0].Source).To(gomega.Equal("huggingface"))
		gomega.Expect(models[0].Path).To(gomega.HaveSuffix("models--Qwen--Qwen2.5-Coder-7B"))
		gomega.Expect(models[0].SizeBytes).To(gomega.BeNumerically(">", 0))
	})

	ginkgo.It("discovers directory snapshots and GGUF files while skipping ordinary files", func() {
		dir := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(dir, "qwen3-coder-30b"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(dir, "qwen3-coder-30b", "weights.bin"), []byte("xxxx"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(dir, "Llama-3-8B-Instruct.Q5_K_M.gguf"), []byte("0123456789"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(dir, "README.md"), []byte("ignore me"), 0o644)).To(gomega.Succeed())

		aliases := map[string]Model{}
		for _, model := range scanModelsDir(dir) {
			aliases[model.Alias] = model
		}
		gomega.Expect(aliases).To(gomega.HaveKey("qwen3-coder-30b"))
		gomega.Expect(aliases).To(gomega.HaveKey("llama-3-8b-instruct.q5_k_m"))
		gomega.Expect(aliases).NotTo(gomega.HaveKey("readme.md"))
	})

	ginkgo.It("skips hidden directories even when they contain model markers", func() {
		dir := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(dir, ".mlx-env", "lib"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(dir, ".mlx-env", "config.json"), []byte("{}"), 0o644)).To(gomega.Succeed())
		realModel := filepath.Join(dir, "qwen3-coder-30b")
		gomega.Expect(os.MkdirAll(realModel, 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(realModel, "config.json"), []byte("{}"), 0o644)).To(gomega.Succeed())

		models := scanModelsDir(dir)
		gomega.Expect(models).To(gomega.HaveLen(1))
		gomega.Expect(models[0].Alias).To(gomega.Equal("qwen3-coder-30b"))
	})

	ginkgo.It("skips directories without model markers", func() {
		dir := ginkgo.GinkgoT().TempDir()
		for _, name := range []string{"__pycache__", "logs", "system-prompts"} {
			path := filepath.Join(dir, name)
			gomega.Expect(os.MkdirAll(path, 0o755)).To(gomega.Succeed())
			gomega.Expect(os.WriteFile(filepath.Join(path, "README.md"), []byte("not a model"), 0o644)).To(gomega.Succeed())
		}
		realModel := filepath.Join(dir, "qwen2.5-coder")
		gomega.Expect(os.MkdirAll(realModel, 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(realModel, "model.safetensors"), []byte("xx"), 0o644)).To(gomega.Succeed())

		models := scanModelsDir(dir)
		gomega.Expect(models).To(gomega.HaveLen(1))
		gomega.Expect(models[0].Alias).To(gomega.Equal("qwen2.5-coder"))
	})

	ginkgo.DescribeTable("recognizes individual model marker files",
		func(filename string) {
			dir := ginkgo.GinkgoT().TempDir()
			gomega.Expect(os.WriteFile(filepath.Join(dir, filename), []byte("x"), 0o644)).To(gomega.Succeed())
			gomega.Expect(looksLikeModelDir(dir)).To(gomega.BeTrue())
		},
		ginkgo.Entry("Hugging Face config", "config.json"),
		ginkgo.Entry("tokenizer JSON", "tokenizer.json"),
		ginkgo.Entry("SentencePiece tokenizer", "tokenizer.model"),
		ginkgo.Entry("MLX metadata", "mlx_metadata.json"),
		ginkgo.Entry("GGUF weights", "weights.gguf"),
		ginkgo.Entry("safetensors weights", "model.safetensors"),
		ginkgo.Entry("PyTorch weights", "pytorch_model.bin"),
		ginkgo.Entry("NumPy weights", "weights.npz"),
	)

	ginkgo.It("rejects a directory containing only adjacent tooling files", func() {
		dir := ginkgo.GinkgoT().TempDir()
		for _, filename := range []string{"README.md", "run.sh", "model.log"} {
			gomega.Expect(os.WriteFile(filepath.Join(dir, filename), []byte("x"), 0o644)).To(gomega.Succeed())
		}
		gomega.Expect(looksLikeModelDir(dir)).To(gomega.BeFalse())
	})

	ginkgo.It("lists every Ollama model tag", func() {
		root := ginkgo.GinkgoT().TempDir()
		tagDir := filepath.Join(root, "qwen2.5-coder")
		gomega.Expect(os.MkdirAll(tagDir, 0o755)).To(gomega.Succeed())
		for _, tag := range []string{"7b", "14b"} {
			gomega.Expect(os.WriteFile(filepath.Join(tagDir, tag), []byte(`{"manifest":true}`), 0o644)).To(gomega.Succeed())
		}

		models := scanOllamaModels(root)
		aliases := make([]string, 0, len(models))
		for _, model := range models {
			gomega.Expect(model.Source).To(gomega.Equal("ollama"))
			aliases = append(aliases, model.Alias)
		}
		gomega.Expect(aliases).To(gomega.ConsistOf("qwen2.5-coder:7b", "qwen2.5-coder:14b"))
	})

	ginkgo.DescribeTable("normalizes aliases without losing Ollama tags",
		func(input, want string) {
			gomega.Expect(normalizeAlias(input)).To(gomega.Equal(want))
		},
		ginkgo.Entry("Ollama tag", "Qwen2.5-Coder:7B", "qwen2.5-coder:7b"),
		ginkgo.Entry("slash and space", "My/Model Name", "my-model-name"),
		ginkgo.Entry("surrounding whitespace", "  Trim This  ", "trim-this"),
		ginkgo.Entry("already normalized", "already-clean-7b", "already-clean-7b"),
	)

	ginkgo.It("returns a non-nil empty model list for a fresh home directory", func() {
		ginkgo.GinkgoT().Setenv("HOME", ginkgo.GinkgoT().TempDir())
		models := DetectModels()
		gomega.Expect(models).NotTo(gomega.BeNil())
		gomega.Expect(models).To(gomega.BeEmpty())
	})
})
