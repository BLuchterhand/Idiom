package backends

import (
	"os"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const sampleBackendsJSON = `{
  "profiles": {
    "qwen-pair": {
      "models": [
        {
          "alias": "qwen3.6-35b",
          "binary": "mlx_lm.server",
          "model_path": "~/models/qwen3.6-35b",
          "port": 47780,
          "cache_size": 4,
          "cache_bytes": 21474836480,
          "max_tokens": 8192,
          "extra_args": ["--temp", "1.0"]
        },
        {
          "alias": "qwen3-coder-30b",
          "binary": "mlx_lm.server",
          "model_path": "~/models/qwen3-coder-30b",
          "port": 47781,
          "max_tokens": 8192
        }
      ],
      "proxy": {
        "listen": 47680,
        "default_alias": "qwen3.6-35b",
        "default_executor": "qwen3-coder-30b"
      }
    }
  }
}`

var _ = ginkgo.Describe("Backend configuration", func() {
	ginkgo.Describe("loading", func() {
		ginkgo.It("parses every field in the canonical schema", func() {
			file, err := Load(writeTempJSON(sampleBackendsJSON))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			profile, ok := file.Profile("qwen-pair")
			gomega.Expect(ok).To(gomega.BeTrue())
			gomega.Expect(profile.Models).To(gomega.HaveLen(2))
			gomega.Expect(profile.Models[0]).To(gomega.Equal(Model{
				Alias:      "qwen3.6-35b",
				Binary:     "mlx_lm.server",
				ModelPath:  "~/models/qwen3.6-35b",
				Port:       47780,
				CacheSize:  4,
				CacheBytes: 21474836480,
				MaxTokens:  8192,
				ExtraArgs:  []string{"--temp", "1.0"},
			}))
			gomega.Expect(profile.Proxy).To(gomega.Equal(Proxy{
				Listen:          47680,
				DefaultAlias:    "qwen3.6-35b",
				DefaultExecutor: "qwen3-coder-30b",
			}))
		})

		ginkgo.It("treats a missing optional file as absent", func() {
			missing := filepath.Join(ginkgo.GinkgoT().TempDir(), "does-not-exist.json")

			file, ok, err := LoadOptional(missing)

			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(ok).To(gomega.BeFalse())
			gomega.Expect(file.Profiles).To(gomega.BeEmpty())
		})

		ginkgo.It("surfaces malformed optional configuration", func() {
			_, ok, err := LoadOptional(writeTempJSON(`{"profiles": {`))

			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("backends:")))
			gomega.Expect(ok).To(gomega.BeFalse())
		})

		ginkgo.It("loads the canonical path below a project root", func() {
			root := ginkgo.GinkgoT().TempDir()
			configDir := filepath.Join(root, ".idiom")
			gomega.Expect(os.MkdirAll(configDir, 0o755)).To(gomega.Succeed())
			gomega.Expect(os.WriteFile(filepath.Join(configDir, "backends.json"), []byte(sampleBackendsJSON), 0o644)).To(gomega.Succeed())

			file, ok, err := LoadFromProjectRoot(root)

			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(ok).To(gomega.BeTrue())
			_, found := file.Profile("qwen-pair")
			gomega.Expect(found).To(gomega.BeTrue())
		})

		ginkgo.It("treats an empty project root as absent", func() {
			_, ok, err := LoadFromProjectRoot("")

			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(ok).To(gomega.BeFalse())
		})
	})

	ginkgo.Describe("profile selection", func() {
		ginkgo.It("selects a lone profile when no name is supplied", func() {
			file, err := Load(writeTempJSON(sampleBackendsJSON))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			profile, ok := file.Profile("")

			gomega.Expect(ok).To(gomega.BeTrue())
			gomega.Expect(profile.Models).To(gomega.HaveLen(2))
		})

		ginkgo.It("rejects an ambiguous empty profile name", func() {
			file, err := Load(writeTempJSON(`{"profiles":{"a":{"models":[]},"b":{"models":[]}}}`))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			_, emptyOK := file.Profile("")
			_, namedOK := file.Profile("a")
			gomega.Expect(emptyOK).To(gomega.BeFalse())
			gomega.Expect(namedOK).To(gomega.BeTrue())
		})

		ginkgo.It("uses the declared default among multiple profiles", func() {
			file, err := Load(writeTempJSON(`{
				"default_profile":"b",
				"profiles":{
					"a":{"models":[{"alias":"alpha"}]},
					"b":{"models":[{"alias":"bravo"}]}
				}
			}`))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			profile, ok := file.Profile("")

			gomega.Expect(ok).To(gomega.BeTrue())
			gomega.Expect(profile.Models).To(gomega.ConsistOf(Model{Alias: "bravo"}))
		})

		ginkgo.It("finds aliases outside the default profile", func() {
			file, err := Load(writeTempJSON(`{"profiles":{
				"qwen-pair":{"models":[{"alias":"qwen3.6-35b","port":47780}]},
				"qwen-27b-pair":{"models":[{"alias":"qwen3.6-27b","port":47783}]}
			}}`))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			model, ok := file.ModelByAlias("qwen3.6-27b")

			gomega.Expect(ok).To(gomega.BeTrue())
			gomega.Expect(model.Port).To(gomega.Equal(47783))
		})
	})

	ginkgo.Describe("role resolution", func() {
		ginkgo.DescribeTable("uses only hyphen-bounded coder aliases",
			func(aliases []string, expected string) {
				models := []Model{{Alias: "base"}}
				for _, alias := range aliases {
					models = append(models, Model{Alias: alias})
				}

				actual, ok := (Profile{Models: models}).AliasForRole(RoleExecutor)
				if expected == "" {
					gomega.Expect(ok).To(gomega.BeFalse())
					gomega.Expect(actual).To(gomega.BeEmpty())
					return
				}

				gomega.Expect(ok).To(gomega.BeTrue())
				gomega.Expect(actual).To(gomega.Equal(expected))
			},
			ginkgo.Entry("rejects encoder", []string{"qwen-encoder-7b"}, ""),
			ginkgo.Entry("rejects decoder", []string{"some-decoder-base"}, ""),
			ginkgo.Entry("accepts a prefixed token", []string{"qwen3-coder-30b"}, "qwen3-coder-30b"),
			ginkgo.Entry("accepts a suffixed token", []string{"deepseek-coder"}, "deepseek-coder"),
			ginkgo.Entry("accepts a standalone token", []string{"coder"}, "coder"),
			ginkgo.Entry("rejects coderbase", []string{"coderbase-1b"}, ""),
			ginkgo.Entry("selects the first matching alias", []string{"qwen-35b", "deepseek-coder-7b", "code-only"}, "deepseek-coder-7b"),
		)

		ginkgo.DescribeTable("recognizes token boundaries",
			func(value, token string, expected bool) {
				gomega.Expect(isHyphenBoundedToken(value, token)).To(gomega.Equal(expected))
			},
			ginkgo.Entry("the whole value", "coder", "coder", true),
			ginkgo.Entry("both punctuation boundaries", "-coder-", "coder", true),
			ginkgo.Entry("interior punctuation boundaries", "x-coder-y", "coder", true),
			ginkgo.Entry("a right edge", "x-coder", "coder", true),
			ginkgo.Entry("a left edge", "coder-y", "coder", true),
			ginkgo.Entry("not encoder", "encoder", "coder", false),
			ginkgo.Entry("not decoder", "decoder", "coder", false),
			ginkgo.Entry("not coderbase", "coderbase", "coder", false),
			ginkgo.Entry("not basecoder", "basecoder", "coder", false),
			ginkgo.Entry("not an empty value", "", "coder", false),
			ginkgo.Entry("not a shorter value", "co", "coder", false),
		)

		ginkgo.It("prefers explicit defaults", func() {
			profile := Profile{
				Models: []Model{
					{Alias: "tagged-planner", Roles: []string{RolePlanner}},
					{Alias: "explicit-planner"},
				},
				Proxy: Proxy{
					DefaultPlanner:    "explicit-planner",
					DefaultExecutor:   "explicit-executor",
					DefaultClassifier: "explicit-classifier",
				},
			}

			for role, expected := range map[string]string{
				RolePlanner:    "explicit-planner",
				RoleExecutor:   "explicit-executor",
				RoleClassifier: "explicit-classifier",
			} {
				actual, ok := profile.AliasForRole(role)
				gomega.Expect(ok).To(gomega.BeTrue())
				gomega.Expect(actual).To(gomega.Equal(expected))
			}
		})

		ginkgo.DescribeTable("reports how it selected an alias",
			func(profile Profile, role string, expected RoleAliasResolution) {
				gomega.Expect(profile.ResolveRoleAlias(role)).To(gomega.Equal(expected))
			},
			ginkgo.Entry("an explicit proxy default",
				Profile{
					Models: []Model{{Alias: "tagged-executor", Roles: []string{RoleExecutor}}},
					Proxy:  Proxy{DefaultExecutor: "explicit-executor"},
				},
				RoleExecutor,
				RoleAliasResolution{Role: RoleExecutor, Alias: "explicit-executor", Source: RoleAliasSourceProxyDefault, OK: true},
			),
			ginkgo.Entry("a role tag",
				Profile{Models: []Model{{Alias: "planner", Roles: []string{RolePlanner}}}},
				RolePlanner,
				RoleAliasResolution{Role: RolePlanner, Alias: "planner", Source: RoleAliasSourceRoleTag, OK: true},
			),
			ginkgo.Entry("a lone model",
				Profile{Models: []Model{{Alias: "solo"}}},
				RoleClassifier,
				RoleAliasResolution{Role: RoleClassifier, Alias: "solo", Source: RoleAliasSourceLoneModel, OK: true},
			),
			ginkgo.Entry("the executor heuristic",
				Profile{Models: []Model{{Alias: "base"}, {Alias: "qwen3-coder-30b"}}},
				RoleExecutor,
				RoleAliasResolution{Role: RoleExecutor, Alias: "qwen3-coder-30b", Source: RoleAliasSourceExecutorCoderHeuristic, OK: true},
			),
			ginkgo.Entry("a miss",
				Profile{Models: []Model{{Alias: "base"}, {Alias: "helper"}}},
				RoleExecutor,
				RoleAliasResolution{Role: RoleExecutor},
			),
		)

		ginkgo.It("selects the first matching role tag", func() {
			profile := Profile{Models: []Model{
				{Alias: "alpha", Roles: []string{RoleClassifier}},
				{Alias: "beta", Roles: []string{RolePlanner, RoleExecutor}},
				{Alias: "gamma"},
			}}

			for role, expected := range map[string]string{
				RolePlanner: "beta", RoleExecutor: "beta", RoleClassifier: "alpha",
			} {
				actual, ok := profile.AliasForRole(role)
				gomega.Expect(ok).To(gomega.BeTrue())
				gomega.Expect(actual).To(gomega.Equal(expected))
			}
		})

		ginkgo.It("assigns every role to a lone model", func() {
			profile := Profile{Models: []Model{{Alias: "only-model"}}}

			for _, role := range []string{RolePlanner, RoleExecutor, RoleClassifier} {
				actual, ok := profile.AliasForRole(role)
				gomega.Expect(ok).To(gomega.BeTrue())
				gomega.Expect(actual).To(gomega.Equal("only-model"))
			}
		})

		ginkgo.It("limits the multi-model alias heuristic to executors", func() {
			profile := Profile{Models: []Model{{Alias: "qwen3.6-35b"}, {Alias: "qwen3-coder-30b"}}}

			alias, executorOK := profile.AliasForRole(RoleExecutor)
			_, plannerOK := profile.AliasForRole(RolePlanner)
			_, classifierOK := profile.AliasForRole(RoleClassifier)

			gomega.Expect(executorOK).To(gomega.BeTrue())
			gomega.Expect(alias).To(gomega.Equal("qwen3-coder-30b"))
			gomega.Expect(plannerOK).To(gomega.BeFalse())
			gomega.Expect(classifierOK).To(gomega.BeFalse())
		})

		ginkgo.It("reports no alias when every resolution rule misses", func() {
			profile := Profile{Models: []Model{{Alias: "qwen3.6-35b"}, {Alias: "llama-7b"}}}

			alias, executorOK := profile.AliasForRole(RoleExecutor)
			blankAlias, blankOK := profile.AliasForRole("")

			gomega.Expect(executorOK).To(gomega.BeFalse())
			gomega.Expect(alias).To(gomega.BeEmpty())
			gomega.Expect(blankOK).To(gomega.BeFalse())
			gomega.Expect(blankAlias).To(gomega.BeEmpty())
		})

		ginkgo.It("finds model metadata by exact nonblank alias", func() {
			profile := Profile{Models: []Model{{Alias: "alpha", Port: 100}, {Alias: "beta", Port: 200}}}

			model, ok := profile.ModelByAlias("beta")
			_, missingOK := profile.ModelByAlias("missing")
			_, blankOK := profile.ModelByAlias("  ")

			gomega.Expect(ok).To(gomega.BeTrue())
			gomega.Expect(model.Port).To(gomega.Equal(200))
			gomega.Expect(missingOK).To(gomega.BeFalse())
			gomega.Expect(blankOK).To(gomega.BeFalse())
		})
	})
})

func writeTempJSON(body string) string {
	ginkgo.GinkgoHelper()

	path := filepath.Join(ginkgo.GinkgoT().TempDir(), "backends.json")
	gomega.Expect(os.WriteFile(path, []byte(body), 0o644)).To(gomega.Succeed())

	return path
}
