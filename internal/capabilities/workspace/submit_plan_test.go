package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/config"
)

func newSubmitPlanTestManager() (*Manager, string) {
	ginkgo.GinkgoHelper()

	root := ginkgo.GinkgoT().TempDir()
	mgr := NewManager(config.Workspace{
		Root:         root,
		CWD:          root,
		EnableWrites: true,
	})

	return mgr, root
}

func writeSubmitPlanFixture(root string, path string, content string) {
	ginkgo.GinkgoHelper()

	fullPath := filepath.Join(root, path)
	gomega.Expect(os.MkdirAll(filepath.Dir(fullPath), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(fullPath, []byte(content), 0o644)).To(gomega.Succeed())

}

func writeSubmitPlanContextView(root string, packID string, paths ...string) {
	ginkgo.GinkgoHelper()

	var files []map[string]string

	for _, path := range paths {
		files = append(files, map[string]string{"path": path, "role": "editable"})
	}

	body, err := json.Marshal(map[string]any{
		"pack_id":         packID,
		"index_id":        "idx-" + packID[:6],
		"fresh":           true,
		"candidate_files": files,
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal context view: %v", err))

	writeSubmitPlanFixture(root, filepath.Join(".idiom", "comprehension", "views", packID+".json"), string(body))
}

func writeSubmitPlanBodyRef(root string, ref string, content string) {
	ginkgo.GinkgoHelper()

	writeSubmitPlanFixture(root, filepath.Join(".idiom", "plans", filepath.FromSlash(ref)), content)
}

func submitPlanTestContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}

	return false
}

var _ = ginkgo.Describe("submit plan", func() {
	ginkgo.It("submit plan tool schema requires concrete edit source", func() {
		defs := BuildToolDefinitions([]string{"submit_plan"})
		gomega.Expect(defs).To(gomega.HaveLen(1), fmt.Sprintf("expected submit_plan schema, got %d definitions", len(defs)))

		for _, want := range []string{"executable final content", "no placeholders", "not a quoted JSON string", "complete file body", "insert_after_line", "small inline", "large additions"} {
			gomega.Expect(defs[0].Description).To(gomega.ContainSubstring(want), fmt.Sprintf("submit_plan description missing %q:\n%s", want, defs[0].Description))

		}

		var schema struct {
			AnyOf      []map[string][]string `json:"anyOf"`
			Properties struct {
				Edits struct {
					MinItems int `json:"minItems"`
					Items    struct {
						Required []string              `json:"required"`
						AnyOf    []map[string][]string `json:"anyOf"`
					} `json:"items"`
				} `json:"edits"`
				PlanArtifact struct {
					Required   []string `json:"required"`
					Properties struct {
						Edits struct {
							MinItems int `json:"minItems"`
						} `json:"edits"`
					} `json:"properties"`
				} `json:"plan_artifact"`
			} `json:"properties"`
		}
		gomega.Expect(json.Unmarshal([]byte(defs[0].ParametersJSON), &schema)).To(gomega.Succeed())
		gomega.Expect(schema.AnyOf).To(gomega.HaveLen(2), fmt.Sprintf("submit_plan should require either top-level edits or plan_artifact, got %#v", schema.AnyOf))
		gomega.Expect(schema.Properties.Edits.MinItems).To(gomega.BeNumerically("==", 1), fmt.Sprintf("top-level edits should require at least one path-scoped edit, got %#v", schema.Properties.Edits))
		gomega.Expect(submitPlanTestContains(schema.Properties.Edits.Items.Required, "path")).To(gomega.BeTrue(), fmt.Sprintf("top-level edits should require at least one path-scoped edit, got %#v", schema.Properties.Edits))
		gomega.Expect(schema.Properties.Edits.Items.AnyOf).NotTo(gomega.BeEmpty(), fmt.Sprintf("edit items should require a concrete edit shape"))
		gomega.Expect(submitPlanTestAnyOfRequires(schema.Properties.Edits.Items.AnyOf, "old_text", "new_text")).To(gomega.BeTrue(), fmt.Sprintf("substring edit schema should require old_text and new_text together, got %#v", schema.Properties.Edits.Items.AnyOf))
		gomega.Expect(submitPlanTestAnyOfRequires(schema.Properties.Edits.Items.AnyOf, "insert_after_line", "content")).To(gomega.BeTrue(), fmt.Sprintf("insert edit schema should require insert_after_line and content together, got %#v", schema.Properties.Edits.Items.AnyOf))

		for _, field := range []string{"task", "contract", "verification_obligations", "edits"} {
			gomega.Expect(submitPlanTestContains(schema.Properties.PlanArtifact.Required, field)).To(gomega.BeTrue(), fmt.Sprintf("plan_artifact should require %q so structured callers do not emit an edits-only wrapper, got %#v", field, schema.Properties.PlanArtifact.Required))

		}
		gomega.Expect(schema.Properties.PlanArtifact.Properties.Edits.MinItems).To(gomega.BeNumerically("==", 1), fmt.Sprintf("plan_artifact should require nonempty edits, got %#v", schema.Properties.PlanArtifact))

	})
	ginkgo.It("submit plan empty edits returns typed code", func() {
		mgr, _ := newSubmitPlanTestManager()
		result := mgr.submitPlan(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "empty plan should be rejected",
		})
		gomega.Expect(result.Code).To(gomega.Equal(SubmitPlanEmptyEditsCode), fmt.Sprintf("empty submit_plan Code = %q, want %q; result=%#v", result.Code, SubmitPlanEmptyEditsCode, result))
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("empty submit_plan should return an actionable error: %#v", result))

	})
	ginkgo.It("submit plan empty source satisfied uses terminal required code", func() {
		mgr, _ := newSubmitPlanTestManager()
		result := mgr.submitPlan(context.Background(), ToolRequest{
			Tool:     "submit_plan",
			Decision: "source-satisfied",
		})
		gomega.Expect(result.Code).To(gomega.Equal(SubmitPlanSourceSatisfiedTerminalRequiredCode), fmt.Sprintf("source-satisfied empty submit_plan Code = %q, want %q; result=%#v", result.Code, SubmitPlanSourceSatisfiedTerminalRequiredCode, result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("Decision: source-satisfied"), fmt.Sprintf("source-satisfied empty submit_plan should point to terminal answer shape: %#v", result))

	})
	ginkgo.It("submit plan empty source satisfied rationale uses terminal required code", func() {
		mgr, _ := newSubmitPlanTestManager()
		result := mgr.submitPlan(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "The current source already contains the requested final state, so no changes are needed.",
		})
		gomega.Expect(result.Code).To(gomega.Equal(SubmitPlanSourceSatisfiedTerminalRequiredCode), fmt.Sprintf("source-satisfied rationale Code = %q, want %q; result=%#v", result.Code, SubmitPlanSourceSatisfiedTerminalRequiredCode, result))

	})
	ginkgo.It("submit plan rejects unsupported proof claims at intake", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "tests/panel_contract.gd", "func test_contract():\n\tpass\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "add a real behavior test",
			Edits: []planartifact.PlanEdit{{
				Path:    "tests/panel_contract.gd",
				OldText: "func test_contract():\n\tpass\n",
				NewText: "func test_contract():\n\t# simplified check: for now we just verify that it exists and can be called\n\tpass\n",
				Purpose: "add coverage",
			}},
		})
		gomega.Expect(result.Error).To(gomega.ContainSubstring("unsupported proof claim"), fmt.Sprintf("submit_plan should reject unsupported proof claims before executor handoff, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("self-disqualifying"), fmt.Sprintf("submit_plan should reject unsupported proof claims before executor handoff, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("tests/panel_contract.gd"), fmt.Sprintf("submit_plan should reject unsupported proof claims before executor handoff, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("simplified"), fmt.Sprintf("submit_plan should reject unsupported proof claims before executor handoff, got %#v", result))

		if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("rejected submit_plan should not persist last.plan.json; stat err=%v", err))
		}
	})
	ginkgo.It("submit plan rejects setup only test edits at intake", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "native/tests/test_fluid_brewery_cascade.cpp", "TEST_CASE(\"FET-3\") {\n    CHECK(true);\n}\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "add a real behavior test",
			Edits: []planartifact.PlanEdit{{
				Path:    "native/tests/test_fluid_brewery_cascade.cpp",
				OldText: "TEST_CASE(\"FET-3\") {\n    CHECK(true);\n}\n",
				NewText: "TEST_CASE(\"FET-3\") {\n    // Setup-only: verify preconditions before behavior is implemented.\n    CHECK(cistern_exists);\n}\n",
				Purpose: "add coverage",
			}},
		})
		gomega.Expect(result.Error).To(gomega.ContainSubstring("unsupported proof claim"), fmt.Sprintf("submit_plan should reject setup-only/precondition-only test edits, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("verify precondition"), fmt.Sprintf("submit_plan should reject setup-only/precondition-only test edits, got %#v", result))

		if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("rejected submit_plan should not persist last.plan.json; stat err=%v", err))
		}
	})
	ginkgo.It("submit plan unsupported proof claims ignore prose files", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "docs/review.md", "before\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "document review limitation",
			Edits: []planartifact.PlanEdit{{
				Path:    "docs/review.md",
				OldText: "before",
				NewText: "This retrospective says we cannot easily test the old behavior.\n",
				Purpose: "update review doc",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("submit_plan should not apply source proof heuristics to prose files: %s", result.Error))

	})
	ginkgo.It("plan edit unsupported proof claim reason prefers content over new text", func() {
		reason := PlanEditUnsupportedProofClaimReason(planartifact.PlanEdit{
			Path:    "internal/sample_test.go",
			Content: "package sample\n\nfunc TestRealBehavior(t *testing.T) {\n\tif got := behavior(); got != 42 {\n\t\tt.Fatalf(\"got %d\", got)\n\t}\n}\n",
			NewText: "// for now this test is only a placeholder\n",
		})
		gomega.Expect(reason).To(gomega.Equal(""), fmt.Sprintf("PlanEditUnsupportedProofClaimReason should inspect Content before NewText, got %q", reason))

	})
	ginkgo.It("submit plan rejects source line range expansion without selected anchor", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "native/src/core/colony_sim.h", strings.Join([]string{
			"struct ColonySim {",
			"};",
			"",
			"inline godot::Array ColonySim::gd_get_build_catalog() const {",
			"    godot::Array result;",
			"}",
			"",
		}, "\n"))

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "insert inventory quality fields",
			Edits: []planartifact.PlanEdit{{
				Path:      "native/src/core/colony_sim.h",
				StartLine: 4,
				EndLine:   4,
				Content: strings.Join([]string{
					"    d[\"quality_skill_contribution\"] = quality_skill_contribution;",
					"    d[\"quality_material_contribution\"] = quality_material_contribution;",
					"    d[\"quality_workspace_contribution\"] = quality_workspace_contribution;",
					"",
				}, "\n"),
				Purpose: "insert fields after quality_name",
			}},
		})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected unanchored source line-range expansion to be rejected"))

		for _, want := range []string{"expands the selected range", "does not preserve any selected anchor line", "old_text/new_text"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("expected submit_plan rejection to mention %q, got: %s", want, result.Error))

		}

		if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("rejected submit_plan should not persist last.plan.json; stat err=%v", err))
		}
	})
	ginkgo.It("submit plan rejects go line range expansion without selected anchor after syntax preflight", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "pkg/feature.go", strings.Join([]string{
			"package pkg",
			"",
			"func existing() int { return 1 }",
			"",
		}, "\n"))

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "add replacement helpers",
			Edits: []planartifact.PlanEdit{{
				Path:      "pkg/feature.go",
				StartLine: 3,
				EndLine:   3,
				Content: strings.Join([]string{
					"func added() int { return 2 }",
					"func more() int { return 3 }",
					"",
				}, "\n"),
				Purpose: "replace helper with two helpers",
			}},
		})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected syntactically valid unanchored Go expansion to be rejected"))

		for _, want := range []string{"expands the selected range", "does not preserve any selected anchor line", "old_text/new_text"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("expected submit_plan rejection to mention %q, got: %s", want, result.Error))

		}
		gomega.Expect(result.Error).NotTo(gomega.ContainSubstring("preflight syntax validation failed"), fmt.Sprintf("test fixture should pass syntax preflight before anchor guard rejects it: %s", result.Error))

	})
	ginkgo.It("submit plan rejects content only duplicate existing source block", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "native/src/core/constants/environment.h", strings.Join([]string{
			"inline constexpr float QUALITY_NORMAL_THRESHOLD = 0.50f;",
			"",
			"// Quality Score",
			"inline constexpr float QUALITY_SCORE_COMPONENT_WEIGHT = 0.50f;",
			"struct QualityScoreBreakdown {",
			"    float raw_score = 0.0f;",
			"};",
			"",
		}, "\n"))

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "add quality score helpers",
			Edits: []planartifact.PlanEdit{{
				Path:      "native/src/core/constants/environment.h",
				StartLine: 2,
				EndLine:   2,
				Content: strings.Join([]string{
					"// Quality Score",
					"inline constexpr float QUALITY_SCORE_COMPONENT_WEIGHT = 0.50f;",
					"struct QualityScoreBreakdown {",
					"    float raw_score = 0.0f;",
					"};",
					"",
				}, "\n"),
				Purpose: "add quality score structures",
			}},
		})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected duplicate existing source block to be rejected"))

		for _, want := range []string{"duplicate an existing source block", "source_satisfied_paths", "old_text/new_text"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("expected submit_plan rejection to mention %q, got: %s", want, result.Error))

		}
	})
	ginkgo.It("submit plan allows source line range replacement without growth", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "native/src/sim.cpp", "int value() {\n    return 1;\n}\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "replace return value",
			Edits: []planartifact.PlanEdit{{
				Path:      "native/src/sim.cpp",
				StartLine: 2,
				EndLine:   2,
				Content:   "    return 2;",
				Purpose:   "replace return value",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("ordinary same-size source line-range replacement should remain valid: %s", result.Error))

	})
	ginkgo.It("submit plan rejects line range whole existing source rewrite", func() {
		mgr, root := newSubmitPlanTestManager()
		path := "native/src/simulation/world/collapse_event.h"
		writeSubmitPlanFixture(root, path, strings.Join([]string{
			"#pragma once",
			"",
			"namespace colony {",
			"struct CollapseEvent {",
			"    int trigger = 0;",
			"};",
			"}",
			"",
		}, "\n"))

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "Rewrite collapse event header.",
			Edits: []planartifact.PlanEdit{{
				Path:      path,
				StartLine: 1,
				EndLine:   7,
				Content:   "#ifndef NEW_HEADER\n#define NEW_HEADER\nclass CollapseEvent {};\n#endif\n",
				Purpose:   "replace existing source header",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), "line-range rewrite of an entire existing source file should be rejected")

		for _, want := range []string{"line-range replacement", "entire existing source file", "bounded old_text/new_text"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("expected submit_plan rejection to mention %q, got: %s", want, result.Error))

		}
	})
	ginkgo.It("submit plan rejects large source line range collapse", func() {
		mgr, root := newSubmitPlanTestManager()
		lines := make([]string, 0, 90)
		lines = append(lines, "#include \"core/colony_sim.h\"")
		for i := 0; i < 80; i++ {
			lines = append(lines, fmt.Sprintf("static int helper_%02d() { return %d; }", i, i))
		}
		lines = append(lines, "int keep() { return helper_00(); }", "")
		writeSubmitPlanFixture(root, "native/src/core/colony_sim_tick.cpp", strings.Join(lines, "\n"))

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "Add collapse helper integration.",
			Edits: []planartifact.PlanEdit{{
				Path:      "native/src/core/colony_sim_tick.cpp",
				StartLine: 1,
				EndLine:   80,
				Content:   "#include \"core/colony_sim_tick.h\"\n#include \"simulation/world/collapse_event.h\"\n\nstatic CollapseEvent g_collapse_event;\n",
				Purpose:   "add collapse event include and static helper",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), "large source line-range collapse should be rejected")

		for _, want := range []string{"would collapse", "non-empty source lines", "smaller bounded anchors"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("expected submit_plan rejection to mention %q, got: %s", want, result.Error))

		}
	})
	ginkgo.It("submit plan rejects mid file preprocessor line range drift", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "native/src/core/colony_sim_tick.cpp", strings.Join([]string{
			"#include \"core/colony_sim.h\"",
			"",
			"static std::string append_stage_decline_cause() {",
			"    return \"ok\";",
			"}",
			"",
			"struct SurfaceFloodProbe {",
			"    bool has_bank_focus = false;",
			"};",
			"",
		}, "\n"))

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "Add collapse event include.",
			Edits: []planartifact.PlanEdit{{
				Path:      "native/src/core/colony_sim_tick.cpp",
				StartLine: 3,
				EndLine:   5,
				Content:   "#include \"../simulation/world/collapse_event.h\"\n\nclass CollapsedSupport {\npublic:\n    CollapseEvent collapse_event;\n};\n",
				Purpose:   "add collapse event include and helper",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), "mid-file preprocessor drift should be rejected")

		for _, want := range []string{"file-scope preprocessor directive", "existing preprocessor context", "<<QUESTION>>"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("expected submit_plan rejection to mention %q, got: %s", want, result.Error))

		}
	})
	ginkgo.It("submit plan rejects anchor preserving mid file preprocessor line range drift", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "native/src/core/colony_sim_tick.cpp", strings.Join([]string{
			"#include \"core/colony_sim.h\"",
			"",
			"static std::string append_stage_decline_cause() {",
			"    return \"ok\";",
			"}",
			"",
		}, "\n"))

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "Add collapse event include.",
			Edits: []planartifact.PlanEdit{{
				Path:      "native/src/core/colony_sim_tick.cpp",
				StartLine: 3,
				EndLine:   5,
				Content: strings.Join([]string{
					"static std::string append_stage_decline_cause() {",
					"#include \"../simulation/world/collapse_event.h\"",
					"    return \"ok\";",
					"}",
				}, "\n"),
				Purpose: "add collapse event include near helper",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), "anchor-preserving mid-file preprocessor drift should be rejected")
		gomega.Expect(result.Error).To(gomega.ContainSubstring("file-scope preprocessor directive"), fmt.Sprintf("expected file-scope preprocessor rejection, got: %s", result.Error))

	})
	ginkgo.It("submit plan allows replacing existing conditional preprocessor context", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "native/src/core/feature.h", strings.Join([]string{
			"#pragma once",
			"",
			"#ifdef OLD_FEATURE",
			"int enabled();",
			"#endif",
			"",
		}, "\n"))

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "Update feature guard.",
			Edits: []planartifact.PlanEdit{{
				Path:      "native/src/core/feature.h",
				StartLine: 3,
				EndLine:   3,
				Content:   "#ifndef DISABLED_FEATURE",
				Purpose:   "replace conditional preprocessor guard",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("existing conditional preprocessor context should be allowed: %s", result.Error))

	})
	ginkgo.It("submit plan allows introducing in body conditional compilation", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "native/src/core/feature.cpp", strings.Join([]string{
			"void tick() {",
			"    run();",
			"}",
			"",
			"void keep_existing() {",
			"    run_other();",
			"}",
			"",
		}, "\n"))

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "Add debug-only logging.",
			Edits: []planartifact.PlanEdit{{
				Path:      "native/src/core/feature.cpp",
				StartLine: 2,
				EndLine:   2,
				Content: strings.Join([]string{
					"    run();",
					"#ifdef DEBUG",
					"    log_debug();",
					"#endif",
				}, "\n"),
				Purpose: "add debug-only logging inside function",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("in-body conditional compilation should be allowed: %s", result.Error))

	})
	ginkgo.It("submit plan allows start line one in body conditional compilation", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "native/src/core/feature.cpp", strings.Join([]string{
			"void tick() {",
			"    run();",
			"}",
			"",
			"void keep_existing() {",
			"    run_other();",
			"}",
			"",
		}, "\n"))

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "Add debug-only logging.",
			Edits: []planartifact.PlanEdit{{
				Path:      "native/src/core/feature.cpp",
				StartLine: 1,
				EndLine:   3,
				Content: strings.Join([]string{
					"void tick() {",
					"    run();",
					"#ifdef DEBUG",
					"    log_debug();",
					"#endif",
					"}",
				}, "\n"),
				Purpose: "add debug-only logging inside function",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("start-line-one in-body conditional compilation should be allowed: %s", result.Error))

	})
	ginkgo.It("submit plan allows introducing in body pragma", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "native/src/core/feature.cpp", strings.Join([]string{
			"void tick_all() {",
			"    for (int i = 0; i < count; ++i) {",
			"        tick(i);",
			"    }",
			"}",
			"",
		}, "\n"))

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "Parallelize the local loop.",
			Edits: []planartifact.PlanEdit{{
				Path:      "native/src/core/feature.cpp",
				StartLine: 2,
				EndLine:   4,
				Content: strings.Join([]string{
					"#pragma omp parallel for",
					"    for (int i = 0; i < count; ++i) {",
					"        tick(i);",
					"    }",
				}, "\n"),
				Purpose: "add in-body compiler pragma",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("in-body pragma should be allowed: %s", result.Error))

	})
	ginkgo.It("submit plan rejects header guard without selecting existing prologue", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "native/src/simulation/world/collapse_event.h", strings.Join([]string{
			"// Shared CollapseEvent struct + Trigger taxonomy.",
			"//",
			"// Migration plan text.",
			"",
			"#pragma once",
			"",
			"namespace colony {",
			"struct CollapseEvent {};",
			"}",
			"",
		}, "\n"))

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "Rewrite collapse event header prologue.",
			Edits: []planartifact.PlanEdit{{
				Path:      "native/src/simulation/world/collapse_event.h",
				StartLine: 1,
				EndLine:   3,
				Content:   "#ifndef COLLAPSE_EVENT_H\n#define COLLAPSE_EVENT_H\n\nclass CollapseEvent {};\n#endif\n",
				Purpose:   "add header guard",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), "header guard replacement without selecting existing prologue should be rejected")

		for _, want := range []string{"header guard", "existing file prologue", "current guard/include line"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("expected submit_plan rejection to mention %q, got: %s", want, result.Error))

		}
	})
	ginkgo.It("submit plan rejects header guard after leading block comment", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "native/src/simulation/world/collapse_event.h", strings.Join([]string{
			"// Shared CollapseEvent struct + Trigger taxonomy.",
			"//",
			"// Migration plan text.",
			"",
			"#pragma once",
			"",
			"namespace colony {",
			"struct CollapseEvent {};",
			"}",
			"",
		}, "\n"))

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "Rewrite collapse event header prologue.",
			Edits: []planartifact.PlanEdit{{
				Path:      "native/src/simulation/world/collapse_event.h",
				StartLine: 1,
				EndLine:   3,
				Content: strings.Join([]string{
					"/*",
					" * Collapse event helper.",
					" */",
					"#ifndef COLLAPSE_EVENT_H",
					"#define COLLAPSE_EVENT_H",
					"",
					"class CollapseEvent {};",
					"#endif",
				}, "\n"),
				Purpose: "add header guard",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), "header guard after leading block comment should be rejected")
		gomega.Expect(result.Error).To(gomega.ContainSubstring("header guard"), fmt.Sprintf("expected header guard rejection, got: %s", result.Error))

	})
	ginkgo.It("submit plan allows top of file include edit selecting include context", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "native/src/core/colony_sim_tick.cpp", strings.Join([]string{
			"#include \"core/colony_sim.h\"",
			"",
			"int value() { return 1; }",
			"",
		}, "\n"))

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "Add collapse event include.",
			Edits: []planartifact.PlanEdit{{
				Path:      "native/src/core/colony_sim_tick.cpp",
				StartLine: 1,
				EndLine:   1,
				Content:   "#include \"core/colony_sim.h\"\n#include \"simulation/world/collapse_event.h\"",
				Purpose:   "add top-of-file include",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("top-of-file include edit selecting include context should be allowed: %s", result.Error))

	})
})

func submitPlanTestAnyOfRequires(anyOf []map[string][]string, fields ...string) bool {
	for _, clause := range anyOf {
		required := clause["required"]
		if len(required) != len(fields) {
			continue
		}

		matched := true

		for _, field := range fields {
			if !submitPlanTestContains(required, field) {
				matched = false
				break
			}
		}

		if matched {
			return true
		}
	}

	return false
}

func assertSubmitPlanNoSensitiveMarkers(root string, result ToolResult, forbidden []string) planartifact.PlanArtifact {
	ginkgo.GinkgoHelper()

	body, err := os.ReadFile(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read last.plan.json: %v", err))

	combined := strings.ToLower(result.Output + "\n" + result.Error + "\n" + string(body))
	for _, marker := range forbidden {
		gomega.Expect(combined).NotTo(gomega.ContainSubstring(strings.ToLower(marker)), fmt.Sprintf("submit_plan leaked sensitive marker %q in output/error/artifact:\noutput=%s\nerror=%s\nartifact=%s", marker, result.Output, result.Error, string(body)))

	}

	var artifact planartifact.PlanArtifact
	gomega.Expect(json.Unmarshal(body, &artifact)).To(gomega.Succeed())

	return artifact
}

func assertSubmitPlanPlansTreeDoesNotContain(root string, forbidden []string) {
	ginkgo.GinkgoHelper()

	planDir := filepath.Join(root, ".idiom", "plans")
	err := filepath.WalkDir(planDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if entry.IsDir() {
			return nil
		}

		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		lower := strings.ToLower(string(body))
		for _, marker := range forbidden {
			gomega.Expect(lower).NotTo(gomega.ContainSubstring(strings.ToLower(marker)), fmt.Sprintf("plan artifact tree leaked sensitive marker %q in %s", marker, path))

		}

		return nil
	})

	if os.IsNotExist(err) {
		return
	}
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("scan plan artifact tree: %v", err))

}

var _ = ginkgo.Describe("submit plan", func() {
	ginkgo.It("submit plan writes artifact with schema version", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "a.txt", "before\n")
		writeSubmitPlanFixture(root, "b.txt", "original\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "add feature across the codebase",
			Edits: []planartifact.PlanEdit{
				{Path: "a.txt", OldText: "before", NewText: "after", Purpose: "first edit"},
				{Path: "b.txt", StartLine: 1, EndLine: 1, Content: "rewritten\n", Purpose: "second edit"},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("submit_plan rejected a valid plan: %s", result.Error))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("plan_path:"), fmt.Sprintf("submit_plan output should name the plan path, got %q", result.Output))

		planDir := filepath.Join(root, ".idiom", "plans")

		entries, err := os.ReadDir(planDir)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("plan dir missing: %v", err))

		var stamped string

		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".plan.json") && entry.Name() != "last.plan.json" {
				stamped = filepath.Join(planDir, entry.Name())
			}
		}
		gomega.Expect(stamped).NotTo(gomega.Equal(""), fmt.Sprintf("no timestamped plan artifact written under %s", planDir))

		body, err := os.ReadFile(stamped)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read artifact: %v", err))

		var artifact planartifact.PlanArtifact
		gomega.Expect(json.Unmarshal(body, &artifact)).To(gomega.Succeed())
		gomega.Expect(artifact.SchemaVersion).To(gomega.Equal(planartifact.SchemaVersion), fmt.Sprintf("schema_version mismatch: got %q, want %q", artifact.SchemaVersion, planartifact.SchemaVersion))
		gomega.Expect(artifact.Edits).To(gomega.HaveLen(2), fmt.Sprintf("expected 2 edits, got %d", len(artifact.Edits)))

		provenance := artifact.Edits[0].AnchorProvenance
		gomega.Expect(provenance).NotTo(gomega.BeNil(), fmt.Sprintf("substring edit should persist harness-owned anchor provenance"))
		gomega.Expect(provenance.SchemaVersion).To(gomega.Equal(PlanEditAnchorProvenanceSchemaVersion), fmt.Sprintf("unexpected anchor provenance identity: %#v", provenance))
		gomega.Expect(provenance.Source).To(gomega.Equal("submit_plan"), fmt.Sprintf("unexpected anchor provenance identity: %#v", provenance))
		gomega.Expect(provenance.Path).To(gomega.Equal("a.txt"), fmt.Sprintf("unexpected anchor provenance location: %#v", provenance))
		gomega.Expect(provenance.MatchCount).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected anchor provenance location: %#v", provenance))
		gomega.Expect(provenance.StartLine).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected anchor provenance location: %#v", provenance))
		gomega.Expect(provenance.EndLine).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected anchor provenance location: %#v", provenance))
		gomega.Expect(provenance.FileSHA256).To(gomega.HavePrefix("sha256:"), fmt.Sprintf("anchor provenance should include file and old_text digests: %#v", provenance))
		gomega.Expect(provenance.AnchorSHA256).To(gomega.HavePrefix("sha256:"), fmt.Sprintf("anchor provenance should include file and old_text digests: %#v", provenance))
		gomega.Expect(artifact.Edits[1].AnchorProvenance).NotTo(gomega.BeNil(), fmt.Sprintf("line-range edit should persist harness-owned anchor provenance"))
		gomega.Expect(artifact.Rationale).NotTo(gomega.Equal(""), fmt.Sprintf("rationale should be persisted"))
		gomega.Expect(artifact.Task.Summary).NotTo(gomega.Equal(""), fmt.Sprintf("durable artifact should include task summary"))
		gomega.Expect(artifact.Contract.Kind).To(gomega.Equal(planartifact.ContractKindPlanThenExecute), fmt.Sprintf("durable artifact contract not populated: %#v", artifact.Contract))
		gomega.Expect(artifact.Contract.ContractOwner).To(gomega.Equal(planartifact.OwnerHarness), fmt.Sprintf("durable artifact contract not populated: %#v", artifact.Contract))
		gomega.Expect(artifact.Contract.StrategyOwner).To(gomega.Equal(planartifact.OwnerModel), fmt.Sprintf("durable artifact contract not populated: %#v", artifact.Contract))
		gomega.Expect(artifact.Packet.Status).NotTo(gomega.BeEmpty(), fmt.Sprintf("durable artifact packet status should be populated"))
		gomega.Expect(submitPlanTestContains(artifact.Paths.Editable, "a.txt")).To(gomega.BeTrue(), fmt.Sprintf("durable artifact paths should include planned edit paths: %#v", artifact.Paths))
		gomega.Expect(submitPlanTestContains(artifact.Paths.Changed, "b.txt")).To(gomega.BeTrue(), fmt.Sprintf("durable artifact paths should include planned edit paths: %#v", artifact.Paths))
		gomega.Expect(artifact.Evidence).NotTo(gomega.BeEmpty(), fmt.Sprintf("durable artifact should include bounded evidence"))
		gomega.Expect(artifact.VerificationObligations).NotTo(gomega.BeEmpty(), fmt.Sprintf("durable artifact should include verification obligations: %#v", artifact.VerificationObligations))
		gomega.Expect(artifact.VerificationObligations[0].Status).NotTo(gomega.BeEmpty(), fmt.Sprintf("durable artifact should include verification obligations: %#v", artifact.VerificationObligations))
		gomega.Expect(artifact.Approval.Status).NotTo(gomega.BeEmpty(), fmt.Sprintf("durable artifact should include approval metadata"))
		gomega.Expect(artifact.Approval.ScopeID).NotTo(gomega.BeEmpty(), fmt.Sprintf("durable artifact should include approval scope and execution metadata: %#v", artifact.Approval))
		gomega.Expect(artifact.Approval.ExecutionPermission).NotTo(gomega.BeEmpty(), fmt.Sprintf("durable artifact should include approval scope and execution metadata: %#v", artifact.Approval))
		gomega.Expect(artifact.Handoff.ID).NotTo(gomega.BeEmpty(), fmt.Sprintf("durable artifact should include ready handoff fields: %#v", artifact.Handoff))
		gomega.Expect(artifact.Handoff.Status).NotTo(gomega.BeEmpty(), fmt.Sprintf("durable artifact should include ready handoff fields: %#v", artifact.Handoff))
		gomega.Expect(artifact.Handoff.Ready).To(gomega.BeTrue(), fmt.Sprintf("durable artifact should include ready handoff fields: %#v", artifact.Handoff))

		pointer, err := os.ReadFile(filepath.Join(planDir, "last.plan.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("last.plan.json missing: %v", err))
		gomega.Expect(json.Valid(pointer)).To(gomega.BeTrue(), fmt.Sprintf("last.plan.json is not valid JSON"))

	})
	ginkgo.It("submit plan rejects symlinked plan artifact dir", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "a.txt", "before\n")

		outside := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)).To(gomega.Succeed())

		if err := os.Symlink(outside, filepath.Join(root, ".idiom", "plans")); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlink unavailable: %v", err))
		}

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "edit safely",
			Edits: []planartifact.PlanEdit{{
				Path:    "a.txt",
				OldText: "before",
				NewText: "after",
				Purpose: "safe write",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.BeEmpty(), fmt.Sprintf("submit_plan should reject symlinked plan dir, got result=%#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("symlink"), fmt.Sprintf("submit_plan should reject symlinked plan dir, got result=%#v", result))

	})
	ginkgo.It("validate plan edit anchor provenance rejects stale file state", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeSubmitPlanFixture(root, "a.txt", "before\n")

		edits, err := AttachPlanEditAnchorProvenance(root, []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "before", NewText: "after"},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("attach anchor provenance: %v", err))

		writeSubmitPlanFixture(root, "a.txt", "prefix\nbefore\n")

		err = ValidatePlanEditAnchorProvenance(root, edits[0])
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected stale anchor provenance rejection"))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("anchor provenance stale"), fmt.Sprintf("expected stale file fingerprint error, got %v", err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("file fingerprint changed"), fmt.Sprintf("expected stale file fingerprint error, got %v", err))

	})
	ginkgo.It("validate plan edit anchor provenance covers range and insert anchors", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeSubmitPlanFixture(root, "a.txt", "one\ntwo\nthree\n")

		edits, err := AttachPlanEditAnchorProvenance(root, []planartifact.PlanEdit{
			{Path: "a.txt", StartLine: 2, EndLine: 2, Content: "TWO\n"},
			{Path: "a.txt", InsertAfterLine: 3, Content: "four\n"},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("attach anchor provenance: %v", err))
		gomega.Expect(edits[0].AnchorProvenance).NotTo(gomega.BeNil(), fmt.Sprintf("range and insert edits should carry provenance: %#v", edits))
		gomega.Expect(edits[1].AnchorProvenance).NotTo(gomega.BeNil(), fmt.Sprintf("range and insert edits should carry provenance: %#v", edits))

		writeSubmitPlanFixture(root, "a.txt", "one\nTWO\nthree\n")

		if err := ValidatePlanEditAnchorProvenance(root, edits[0]); err == nil {
			ginkgo.Fail(fmt.Sprintf("expected stale range provenance rejection"))
		}

		writeSubmitPlanFixture(root, "a.txt", "one\ntwo\nTHREE\n")

		if err := ValidatePlanEditAnchorProvenance(root, edits[1]); err == nil {
			ginkgo.Fail(fmt.Sprintf("expected stale insert provenance rejection"))
		}
	})
	ginkgo.It("submit plan overwrites model supplied anchor provenance", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "a.txt", "before\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "update a",
			Edits: []planartifact.PlanEdit{{
				Path:    "a.txt",
				OldText: "before",
				NewText: "after",
				AnchorProvenance: &planartifact.PlanEditAnchorProvenance{
					SchemaVersion: "model.supplied",
					Source:        "model",
					Path:          "../escape.txt",
				},
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("submit_plan should ignore model-supplied anchor provenance and attach its own: %s", result.Error))

		plan, err := LoadPlanArtifact(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load plan: %v", err))

		provenance := plan.Edits[0].AnchorProvenance
		gomega.Expect(provenance).NotTo(gomega.BeNil(), fmt.Sprintf("expected harness-owned provenance"))
		gomega.Expect(provenance.SchemaVersion).To(gomega.Equal(PlanEditAnchorProvenanceSchemaVersion), fmt.Sprintf("expected harness-owned provenance to replace model input, got %#v", provenance))
		gomega.Expect(provenance.Source).To(gomega.Equal("submit_plan"), fmt.Sprintf("expected harness-owned provenance to replace model input, got %#v", provenance))
		gomega.Expect(provenance.Path).To(gomega.Equal("a.txt"), fmt.Sprintf("expected harness-owned provenance to replace model input, got %#v", provenance))
		gomega.Expect(ValidatePlanEditAnchorProvenance(root, plan.Edits[0])).To(gomega.Succeed())

	})
	ginkgo.It("plan edit anchor provenance shape rejects malformed digest", func() {
		err := validatePlanEditAnchorProvenanceShape(&planartifact.PlanEditAnchorProvenance{
			SchemaVersion: PlanEditAnchorProvenanceSchemaVersion,
			Source:        "submit_plan",
			Path:          "a.txt",
			FileSHA256:    "sha256:" + strings.Repeat("z", 64),
			AnchorSHA256:  "sha256:" + strings.Repeat("0", 64),
			StartByte:     1,
			EndByte:       2,
			StartLine:     1,
			EndLine:       1,
			MatchCount:    1,
		})
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected malformed file digest rejection, got %v", err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("file_sha256"), fmt.Sprintf("expected malformed file digest rejection, got %v", err))

	})
	ginkgo.It("submit plan repairs unique leading whitespace anchor", func() {
		mgr, root := newSubmitPlanTestManager()
		current := strings.Join([]string{
			"package main",
			"",
			"func run() {",
			"\tif strings.TrimSpace(effortRoot) == \"\" {",
			"\t\tfmt.Fprintln(stderr, \"product status: --effort-root is required\")",
			"\t\treturn 2",
			"\t}",
			"}",
		}, "\n")
		writeSubmitPlanFixture(root, "status.go", current)

		anchorWithoutOuterIndent := strings.Join([]string{
			"if strings.TrimSpace(effortRoot) == \"\" {",
			"\tfmt.Fprintln(stderr, \"product status: --effort-root is required\")",
			"\treturn 2",
			"}",
		}, "\n")
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "add JSON status usage error",
			Edits: []planartifact.PlanEdit{{
				Path:    "status.go",
				OldText: anchorWithoutOuterIndent,
				NewText: strings.Join([]string{
					"\tif strings.TrimSpace(effortRoot) == \"\" {",
					"\t\treturn writeStatusUsageError()",
					"\t}",
				}, "\n"),
				Purpose: "Update status usage error handling.",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("submit_plan should repair unique indentation-only anchor mismatch, got: %s", result.Error))

		plan, err := LoadPlanArtifact(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load plan: %v", err))
		gomega.Expect(plan.Edits).To(gomega.HaveLen(1), fmt.Sprintf("expected one edit, got %d", len(plan.Edits)))
		gomega.Expect(strings.HasPrefix(plan.Edits[0].OldText, "\tif strings.TrimSpace")).To(gomega.BeTrue(), fmt.Sprintf("expected persisted old_text to preserve source indentation, got %q", plan.Edits[0].OldText))

	})
	ginkgo.It("submit plan rejects line range edit that breaks go syntax", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "pkg/feature_test.go", strings.Join([]string{
			"package pkg",
			"",
			"import \"testing\"",
			"",
			"func TestExisting(t *testing.T) {",
			"\tt.Helper()",
			"}",
			"",
		}, "\n"))

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "add regression coverage",
			Edits: []planartifact.PlanEdit{{
				Path:      "pkg/feature_test.go",
				StartLine: 7,
				EndLine:   7,
				Content: "\nfunc TestAdded(t *testing.T) {\n" +
					"\tt.Helper()\n" +
					"}",
				Purpose: "add a second test",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("expected submit_plan to reject syntax-breaking line range edit"))

		for _, want := range []string{"preflight syntax validation failed", "pkg/feature_test.go", "declared start_line=7 and end_line=7", "extend end_line", "insert_after_line"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("submit_plan error missing %q:\n%s", want, result.Error))

		}
		gomega.Expect(result.Code).To(gomega.Equal(SubmitPlanSyntaxValidationCode))
		gomega.Expect(result.Facts).NotTo(gomega.BeNil())
		gomega.Expect(result.Facts.SubmitPlanDiagnostics).To(gomega.HaveLen(1))
		diagnostic := result.Facts.SubmitPlanDiagnostics[0]
		gomega.Expect(diagnostic.Kind).To(gomega.Equal(toolfacts.SubmitPlanDiagnosticSyntaxValidation))
		gomega.Expect(diagnostic.CanonicalID).To(gomega.Equal("pkg/feature_test.go"))
		gomega.Expect(diagnostic.StartLine).To(gomega.BeNumerically(">", 0))
		gomega.Expect(diagnostic.EndLine).To(gomega.Equal(diagnostic.StartLine))

		if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("syntax-rejected submit_plan should not write last.plan.json, stat err=%v", err))
		}
	})
	ginkgo.It("submit plan rejects sequential line range edits that break go syntax", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "pkg/feature.go", strings.Join([]string{
			"package pkg",
			"",
			"func existing() {",
			"}",
			"",
		}, "\n"))

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "add helper functions",
			Edits: []planartifact.PlanEdit{
				{
					Path:            "pkg/feature.go",
					InsertAfterLine: 2,
					Content:         "func inserted() {}\n",
					Purpose:         "add inserted helper",
				},
				{
					Path:      "pkg/feature.go",
					StartLine: 4,
					EndLine:   4,
					Content:   "}\n\nfunc added() {}\n",
					Purpose:   "add second helper",
				},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("expected submit_plan to reject sequential edits whose projected final file is invalid"))

		for _, want := range []string{"edits[1]", "after applying prior plan edits", "preflight syntax validation failed", "pkg/feature.go"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("submit_plan error missing %q:\n%s", want, result.Error))

		}

		if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("sequence-rejected submit_plan should not write last.plan.json, stat err=%v", err))
		}
	})
	ginkgo.It("submit plan accepts structured durable plan artifact", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "a.txt", "before\n")
		writeSubmitPlanContextView(root, "feedfacecafebeef", "a.txt")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "submit_plan",
			PlanArtifact: &planartifact.PlanArtifact{
				SchemaVersion: planartifact.SchemaVersion,
				PlanID:        "plan-structured",
				Task: planartifact.PlanArtifactTask{
					ID:                    "task-structured",
					Summary:               "Add auditable handoff metadata",
					ClientClassifications: []string{"finance"},
				},
				Contract: planartifact.PlanArtifactContract{
					Kind:              "plan_then_execute",
					StrategyOwner:     "model",
					ContractOwner:     "harness",
					PermissionProfile: "plan_then_execute",
					VerificationOwner: "executor",
				},
				Packet: planartifact.PlanArtifactPacket{PackID: "feedfacecafebeef", Status: "available", Freshness: "fresh"},
				Paths:  planartifact.PlanArtifactPaths{Editable: []string{"a.txt"}, Required: []string{"a.txt"}},
				Evidence: []planartifact.PlanArtifactEvidence{{
					ID:        "evidence-1",
					Kind:      "context_packet",
					Handle:    "context_pack://feedfacecafebeef",
					Freshness: "fresh",
				}},
				UnresolvedQuestions: []planartifact.PlanArtifactQuestion{{
					ID:     "question-1",
					Claim:  "No blocker remains before handoff",
					Status: "closed",
				}},
				VerificationObligations: []planartifact.PlanArtifactVerificationObligation{{
					ID:       "verification-1",
					Kind:     "focused_tests",
					Command:  "go test ./internal/capabilities/workspace",
					Owner:    "executor",
					Status:   "open",
					Required: true,
				}},
				Approval: planartifact.PlanArtifactApproval{Required: true, Status: "approved", ScopeID: "scope-1", Mode: "local"},
				Handoff:  planartifact.PlanArtifactHandoff{ID: "handoff-1", From: "planner", To: "executor", Status: "ready", Ready: true},
				Edits: []planartifact.PlanEdit{
					{Path: "a.txt", OldText: "before", NewText: "after", Purpose: "Update the fixture."},
				},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("submit_plan rejected structured durable artifact: %s", result.Error))

		plan, err := LoadPlanArtifact(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load structured plan: %v", err))
		gomega.Expect(plan.PlanID).To(gomega.Equal("plan-structured"), fmt.Sprintf("structured identity fields not persisted: %#v", plan))
		gomega.Expect(plan.Task.ID).To(gomega.Equal("task-structured"), fmt.Sprintf("structured identity fields not persisted: %#v", plan))
		gomega.Expect(plan.PackID).To(gomega.Equal("feedfacecafebeef"), fmt.Sprintf("pack_id mirror not preserved: top=%q packet=%q", plan.PackID, plan.Packet.PackID))
		gomega.Expect(plan.Packet.PackID).To(gomega.Equal("feedfacecafebeef"), fmt.Sprintf("pack_id mirror not preserved: top=%q packet=%q", plan.PackID, plan.Packet.PackID))
		gomega.Expect(plan.UnresolvedQuestions).To(gomega.HaveLen(1), fmt.Sprintf("structured question/verification fields not preserved: %#v %#v", plan.UnresolvedQuestions, plan.VerificationObligations))
		gomega.Expect(plan.VerificationObligations).To(gomega.HaveLen(1), fmt.Sprintf("structured question/verification fields not preserved: %#v %#v", plan.UnresolvedQuestions, plan.VerificationObligations))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("audit_ok=true"), fmt.Sprintf("accepted structured artifact should report passing audit, got %q", result.Output))

	})
	ginkgo.It("submit plan rejects source satisfied outside original required scope", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "a.txt", "before\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:                 "submit_plan",
			SourceSatisfiedPaths: []string{"b.txt"},
			PlanArtifact: &planartifact.PlanArtifact{
				SchemaVersion: planartifact.SchemaVersion,
				Task:          planartifact.PlanArtifactTask{Summary: "mixed structured coverage"},
				Contract: planartifact.PlanArtifactContract{
					Kind:              "plan_then_execute",
					RequiredEditPaths: []string{"a.txt"},
					StrategyOwner:     "model",
					ContractOwner:     "harness",
					PermissionProfile: "plan_then_execute",
					VerificationOwner: "planner",
				},
				Paths: planartifact.PlanArtifactPaths{
					Editable: []string{"a.txt"},
					Changed:  []string{"a.txt"},
				},
				Evidence: []planartifact.PlanArtifactEvidence{{
					Kind: "harness_observed_source",
					Path: "b.txt",
				}},
				VerificationObligations: []planartifact.PlanArtifactVerificationObligation{{
					ID:       "verification-1",
					Kind:     "focused_tests",
					Command:  "go test ./...",
					Owner:    "executor",
					Status:   "open",
					Required: true,
				}},
				Approval: planartifact.PlanArtifactApproval{Required: true, Status: "approved", ScopeID: "scope-1", Mode: "local"},
				Handoff:  planartifact.PlanArtifactHandoff{ID: "handoff-1", From: "planner", To: "executor", Status: "ready", Ready: true},
				Edits: []planartifact.PlanEdit{
					{Path: "a.txt", OldText: "before", NewText: "after", Purpose: "Update the fixture."},
				},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("source_satisfied_paths outside original required scope should be rejected"))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("scope_path_mismatch"), fmt.Sprintf("expected source_satisfied_paths scope rejection, got %q", result.Error))

		if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("rejected submit_plan should not persist last.plan.json; stat err=%v", err))
		}
	})
	ginkgo.It("submit plan blocks ready handoff when required approval pending", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "a.txt", "before\n")
		writeSubmitPlanContextView(root, "feedfacecafebeef", "a.txt")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "submit_plan",
			PlanArtifact: &planartifact.PlanArtifact{
				SchemaVersion: planartifact.SchemaVersion,
				PlanID:        "plan-pending-approval",
				Task: planartifact.PlanArtifactTask{
					ID:                    "task-pending-approval",
					Summary:               "Block dispatch until approval is satisfied",
					ClientClassifications: []string{"finance", "healthcare_life_sciences", "public_sector", "enterprise_saas", "tooling_vendor", "local_model_private_code", "open_source"},
				},
				Contract: planartifact.PlanArtifactContract{
					Kind:              "plan_then_execute",
					StrategyOwner:     "model",
					ContractOwner:     "harness",
					PermissionProfile: "plan_then_execute",
					VerificationOwner: "executor",
				},
				Packet: planartifact.PlanArtifactPacket{PackID: "feedfacecafebeef", Status: "available", Freshness: "fresh"},
				Paths:  planartifact.PlanArtifactPaths{Editable: []string{"a.txt"}, Required: []string{"a.txt"}},
				Evidence: []planartifact.PlanArtifactEvidence{{
					ID:        "evidence-1",
					Kind:      "context_packet",
					Handle:    "context_pack://feedfacecafebeef",
					Freshness: "fresh",
				}},
				VerificationObligations: []planartifact.PlanArtifactVerificationObligation{{
					ID:       "verification-1",
					Kind:     "focused_tests",
					Command:  "go test ./internal/capabilities/workspace",
					Owner:    "executor",
					Status:   "open",
					Required: true,
				}},
				Approval: planartifact.PlanArtifactApproval{Required: true, Status: "pending", ScopeID: "scope-1", Mode: "local"},
				Handoff:  planartifact.PlanArtifactHandoff{ID: "handoff-1", From: "planner", To: "executor", Status: "ready", Ready: true},
				Edits: []planartifact.PlanEdit{{
					Path:    "a.txt",
					OldText: "before",
					NewText: "after",
					Purpose: "Update the fixture.",
				}},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("submit_plan should block handoff-ready artifact with pending required approval"))

		for _, want := range []string{"plan_artifact.schema_errors", "handoff.status:not_executable"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("approval handoff rejection should include %q, got %q", want, result.Error))

		}
		gomega.Expect(result.Output).To(gomega.Equal(""), fmt.Sprintf("schema rejection should happen before audit output, got %q", result.Output))
		gomega.Expect(result.Summary+result.Output+result.Error).NotTo(gomega.ContainSubstring("executor phase will dispatch"), fmt.Sprintf("blocked artifact should not claim executor dispatch will proceed: %#v", result))

		if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("blocked artifact should not persist last.plan.json, stat err=%v", err))
		}
	})
	ginkgo.It("submit plan blocks pending approval before spilling large edit bodies", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanContextView(root, "feedfacecafebeef", "large.txt")

		sensitiveBody := strings.Repeat("P5_8_SECRET_BODY raw_prompt transcript token customer acme\n", 120)
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "submit_plan",
			PlanArtifact: &planartifact.PlanArtifact{
				SchemaVersion: planartifact.SchemaVersion,
				PlanID:        "plan-pending-approval-large-edit",
				Task: planartifact.PlanArtifactTask{
					ID:                    "task-pending-approval-large-edit",
					Summary:               "Block private large edit body until approval is satisfied",
					ClientClassifications: []string{"finance", "healthcare_life_sciences", "public_sector", "enterprise_saas", "tooling_vendor", "local_model_private_code", "open_source"},
				},
				Contract: planartifact.PlanArtifactContract{
					Kind:              "plan_then_execute",
					StrategyOwner:     "model",
					ContractOwner:     "harness",
					PermissionProfile: "plan_then_execute",
					VerificationOwner: "executor",
				},
				Packet: planartifact.PlanArtifactPacket{PackID: "feedfacecafebeef", Status: "available", Freshness: "fresh"},
				Paths:  planartifact.PlanArtifactPaths{Editable: []string{"large.txt"}, Required: []string{"large.txt"}},
				Evidence: []planartifact.PlanArtifactEvidence{{
					ID:        "evidence-1",
					Kind:      "context_packet",
					Handle:    "context_pack://feedfacecafebeef",
					Freshness: "fresh",
				}},
				VerificationObligations: []planartifact.PlanArtifactVerificationObligation{{
					ID:       "verification-1",
					Kind:     "focused_tests",
					Command:  "go test ./internal/capabilities/workspace",
					Owner:    "executor",
					Status:   "open",
					Required: true,
				}},
				Approval: planartifact.PlanArtifactApproval{Required: true, Status: "pending", ScopeID: "scope-1", Mode: "local"},
				Handoff:  planartifact.PlanArtifactHandoff{ID: "handoff-1", From: "planner", To: "executor", Status: "ready", Ready: true},
				Edits: []planartifact.PlanEdit{{
					Path:       "large.txt",
					NewText:    sensitiveBody,
					ReplaceAll: true,
					Purpose:    "Create large private plan body after approval.",
				}},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("submit_plan should block handoff-ready large edit artifact with pending required approval"))
		gomega.Expect(result.Output).To(gomega.BeEmpty(), fmt.Sprintf("approval handoff rejection should expose bounded audit status, got output=%q error=%q", result.Output, result.Error))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("handoff.status:not_executable"), fmt.Sprintf("approval handoff rejection should expose bounded audit status, got output=%q error=%q", result.Output, result.Error))

		combined := strings.ToLower(result.Output + "\n" + result.Error)
		for _, leaked := range []string{"p5_8_secret_body", "raw_prompt", "transcript", "customer acme"} {
			gomega.Expect(combined).NotTo(gomega.ContainSubstring(leaked), fmt.Sprintf("approval rejection leaked sensitive body marker %q: output=%q error=%q", leaked, result.Output, result.Error))

		}

		if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("blocked large edit artifact should not persist last.plan.json, stat err=%v", err))
		}

		matches, err := filepath.Glob(filepath.Join(root, ".idiom", "plans", "assets", "*new_text.txt"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("glob plan assets: %v", err))
		gomega.Expect(matches).To(gomega.BeEmpty(), fmt.Sprintf("blocked large edit should not spill new_text assets, got %v", matches))

		assertSubmitPlanPlansTreeDoesNotContain(root, []string{
			"P5_8_SECRET_BODY",
			"raw_prompt",
			"transcript",
			"customer acme",
		})
	})
	ginkgo.It("submit plan rejects ready handoff with non ready status", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "a.txt", "before\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "submit_plan",
			PlanArtifact: &planartifact.PlanArtifact{
				SchemaVersion: planartifact.SchemaVersion,
				Task:          planartifact.PlanArtifactTask{Summary: "Reject contradictory handoff readiness"},
				Contract: planartifact.PlanArtifactContract{
					Kind:              "plan_then_execute",
					StrategyOwner:     "model",
					ContractOwner:     "harness",
					PermissionProfile: "plan_then_execute",
					VerificationOwner: "executor",
				},
				Packet: planartifact.PlanArtifactPacket{Status: "not_provided"},
				Paths:  planartifact.PlanArtifactPaths{Editable: []string{"a.txt"}, Required: []string{"a.txt"}},
				Evidence: []planartifact.PlanArtifactEvidence{{
					ID:        "evidence-1",
					Kind:      "context_packet",
					Path:      "a.txt",
					Freshness: "current",
				}},
				VerificationObligations: []planartifact.PlanArtifactVerificationObligation{{
					ID:       "verification-1",
					Kind:     "focused_tests",
					Command:  "go test ./internal/capabilities/workspace",
					Owner:    "executor",
					Status:   "open",
					Required: true,
				}},
				Approval: planartifact.PlanArtifactApproval{Required: true, Status: "approved", ScopeID: "scope-1", Mode: "local", ExecutionPermission: "execute_all"},
				Handoff:  planartifact.PlanArtifactHandoff{ID: "handoff-1", From: "planner", To: "executor", Status: "blocked", Ready: true},
				Edits: []planartifact.PlanEdit{{
					Path:    "a.txt",
					OldText: "before",
					NewText: "after",
					Purpose: "Update the fixture.",
				}},
			},
		})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("submit_plan should reject ready handoff with non-ready status"))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("handoff.ready:invalid_status"), fmt.Sprintf("expected handoff ready/status schema error, got %q", result.Error))

		if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("rejected artifact should not persist last.plan.json, stat err=%v", err))
		}
	})
	ginkgo.It("plan artifact audit keeps pending approval non blocking when handoff not ready", func() {
		root := ginkgo.GinkgoT().TempDir()
		result := AuditPlanArtifact(root, planartifact.PlanArtifact{
			SchemaVersion: planartifact.SchemaVersion,
			PlanID:        "plan-non-ready-handoff",
			Created:       "2026-05-13T00:00:00Z",
			Task: planartifact.PlanArtifactTask{
				Summary:               "Keep non-ready approval handoff visible",
				ClientClassifications: []string{"enterprise_saas"},
			},
			Contract: planartifact.PlanArtifactContract{
				Kind:              "plan_then_execute",
				StrategyOwner:     "model",
				ContractOwner:     "harness",
				PermissionProfile: "plan_then_execute",
				VerificationOwner: "executor",
			},
			Paths: planartifact.PlanArtifactPaths{Editable: []string{"a.txt"}, Required: []string{"a.txt"}},
			VerificationObligations: []planartifact.PlanArtifactVerificationObligation{{
				ID:       "verification-1",
				Kind:     "focused_tests",
				Status:   "open",
				Required: true,
			}},
			Approval: planartifact.PlanArtifactApproval{Required: true, Status: "pending", ScopeID: "scope-1", Mode: "local"},
			Handoff:  planartifact.PlanArtifactHandoff{ID: "handoff-1", From: "planner", To: "executor", Status: "blocked", Ready: false},
			Edits: []planartifact.PlanEdit{{
				Path:    "a.txt",
				OldText: "before",
				NewText: "after",
				Purpose: "Update the fixture.",
			}},
		}, PlanArtifactAuditOptions{EnforcePacketEvidence: true})
		gomega.Expect(result.OK).To(gomega.BeTrue(), fmt.Sprintf("pending approval should remain report-only while handoff is not ready: %#v", result))
		gomega.Expect(result.Metrics.ApprovalHandoffMismatches).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected visible approval mismatch metric, got %#v", result.Metrics))

		gomega.Expect(result.Findings).NotTo(gomega.ContainElement(gomega.And(
			gomega.HaveField("Kind", "approval_handoff_mismatch"),
			gomega.HaveField("Severity", "error"),
		)), fmt.Sprintf("non-ready handoff should not promote approval mismatch to error: %#v", result.Findings))
	})
	ginkgo.It("plan artifact synthesizes stable approval scope and handoff i ds", func() {
		base := planartifact.PlanArtifact{
			SchemaVersion: planartifact.SchemaVersion,
			PlanID:        "plan-stable-ids",
			Task:          planartifact.PlanArtifactTask{Summary: "derive stable governance ids"},
			Contract:      planartifact.PlanArtifactContract{Kind: "plan_then_execute"},
			Paths:         planartifact.PlanArtifactPaths{Editable: []string{"b.txt", "a.txt"}},
			Evidence:      []planartifact.PlanArtifactEvidence{{Kind: "schema_example", Summary: "bounded evidence"}},
			VerificationObligations: []planartifact.PlanArtifactVerificationObligation{{
				Kind:     "focused_tests",
				Status:   "open",
				Required: true,
			}},
			Approval: planartifact.PlanArtifactApproval{Required: true, Status: "approved", Mode: "local"},
			Handoff:  planartifact.PlanArtifactHandoff{Status: "ready", Ready: true, To: "executor"},
			Edits:    []planartifact.PlanEdit{{Path: "a.txt", OldText: "a", NewText: "b"}},
		}
		first := planartifact.NormalizeForSubmit(base, planartifact.NormalizeForSubmitOptions{
			SessionID: "session-1",
			Created:   "2026-05-13T00:00:00Z",
			PlanID:    "plan-stable-ids",
		})
		second := planartifact.NormalizeForSubmit(base, planartifact.NormalizeForSubmitOptions{
			SessionID: "session-2",
			Created:   "2026-05-13T00:00:01Z",
			PlanID:    "plan-stable-ids",
		})
		gomega.Expect(first.Approval.ScopeID).NotTo(gomega.BeEmpty(), fmt.Sprintf("approval scope id should be deterministic, first=%q second=%q", first.Approval.ScopeID, second.Approval.ScopeID))
		gomega.Expect(second.Approval.ScopeID).NotTo(gomega.BeEmpty(), fmt.Sprintf("approval scope id should be deterministic, first=%q second=%q", first.Approval.ScopeID, second.Approval.ScopeID))
		gomega.Expect(first.Approval.ScopeID).To(gomega.Equal(second.Approval.ScopeID), fmt.Sprintf("approval scope id should be deterministic, first=%q second=%q", first.Approval.ScopeID, second.Approval.ScopeID))
		gomega.Expect(first.Handoff.ID).NotTo(gomega.BeEmpty(), fmt.Sprintf("handoff id should be deterministic, first=%q second=%q", first.Handoff.ID, second.Handoff.ID))
		gomega.Expect(second.Handoff.ID).NotTo(gomega.BeEmpty(), fmt.Sprintf("handoff id should be deterministic, first=%q second=%q", first.Handoff.ID, second.Handoff.ID))
		gomega.Expect(first.Handoff.ID).To(gomega.Equal(second.Handoff.ID), fmt.Sprintf("handoff id should be deterministic, first=%q second=%q", first.Handoff.ID, second.Handoff.ID))
		gomega.Expect(first.Approval.ExecutionPermission).To(gomega.Equal(planartifact.ExecutionPermissionExecuteAll), fmt.Sprintf("approved scope should default to execute_all, got %#v", first.Approval))
		gomega.Expect(first.Approval.ApprovedPaths).To(gomega.HaveLen(1), fmt.Sprintf("approved required scope should be explicit, got %#v", first.Approval.ApprovedPaths))
		gomega.Expect(first.Approval.ApprovedPaths[0]).To(gomega.Equal("a.txt"), fmt.Sprintf("approved required scope should be explicit, got %#v", first.Approval.ApprovedPaths))

	})
	ginkgo.It("plan artifact partial approval semantics are client class agnostic", func() {
		for _, class := range []string{
			string(planartifact.ClientClassificationFinance),
			string(planartifact.ClientClassificationHealthcareLifeSciences),
			string(planartifact.ClientClassificationPublicSector),
			string(planartifact.ClientClassificationEnterpriseSaaS),
			string(planartifact.ClientClassificationToolingVendor),
			string(planartifact.ClientClassificationLocalModelPrivateCode),
			string(planartifact.ClientClassificationOpenSource),
		} {
			result := AuditPlanArtifact(ginkgo.GinkgoT().TempDir(), planartifact.PlanArtifact{
				SchemaVersion: planartifact.SchemaVersion,
				PlanID:        "plan-partial-" + class,
				Task: planartifact.PlanArtifactTask{
					Summary:               "Execute only approved partial scope",
					ClientClassifications: []string{class},
				},
				Contract: planartifact.PlanArtifactContract{Kind: "plan_then_execute"},
				Paths:    planartifact.PlanArtifactPaths{Editable: []string{"a.txt"}, Changed: []string{"a.txt"}},
				Evidence: []planartifact.PlanArtifactEvidence{{Kind: "schema_example", Summary: "bounded partial approval evidence"}},
				VerificationObligations: []planartifact.PlanArtifactVerificationObligation{{
					Kind:     "focused_tests",
					Status:   "open",
					Required: true,
				}},
				Approval: planartifact.PlanArtifactApproval{
					Required:            true,
					Status:              "partial",
					ScopeID:             "scope-" + class,
					Mode:                "local",
					ApprovedPaths:       []string{"a.txt"},
					PendingPaths:        []string{"b.txt"},
					ExecutionPermission: "execute_approved_scope",
				},
				Handoff: planartifact.PlanArtifactHandoff{ID: "handoff-" + class, Status: "ready", Ready: true},
				Edits:   []planartifact.PlanEdit{{Path: "a.txt", OldText: "a", NewText: "b"}},
			}, PlanArtifactAuditOptions{})
			gomega.Expect(result.OK).To(gomega.BeTrue(), fmt.Sprintf("partial approval should allow approved scope for class %q: %#v", class, result))
			gomega.Expect(result.Metrics.ApprovalScopeMismatches).To(gomega.BeNumerically("==", 0), fmt.Sprintf("partial approval should not report scope drift for class %q: %#v", class, result.Metrics))

		}
	})
	ginkgo.It("plan artifact partial approval blocks pending scope privately", func() {
		result := AuditPlanArtifact(ginkgo.GinkgoT().TempDir(), planartifact.PlanArtifact{
			SchemaVersion: planartifact.SchemaVersion,
			PlanID:        "plan-partial-private",
			Task:          planartifact.PlanArtifactTask{Summary: "Block pending partial approval scope"},
			Contract:      planartifact.PlanArtifactContract{Kind: "plan_then_execute"},
			Paths:         planartifact.PlanArtifactPaths{Editable: []string{"a.txt", "b.txt"}, Changed: []string{"b.txt"}},
			Evidence:      []planartifact.PlanArtifactEvidence{{Kind: "schema_example", Summary: "bounded partial approval evidence"}},
			VerificationObligations: []planartifact.PlanArtifactVerificationObligation{{
				Kind:     "focused_tests",
				Status:   "open",
				Required: true,
			}},
			Approval: planartifact.PlanArtifactApproval{
				Required:            true,
				Status:              "partial",
				ScopeID:             "scope raw_prompt customer-acme token=/home/example/private/client",
				ApprovedPaths:       []string{"a.txt"},
				PendingPaths:        []string{"b.txt"},
				ExecutionPermission: "execute_approved_scope",
			},
			Handoff: planartifact.PlanArtifactHandoff{ID: "handoff raw_prompt customer-acme", Status: "ready", Ready: true},
			Edits:   []planartifact.PlanEdit{{Path: "b.txt", OldText: "a", NewText: "b"}},
		}, PlanArtifactAuditOptions{})
		gomega.Expect(result.OK).To(gomega.BeFalse(), fmt.Sprintf("partial approval should block pending edit scope"))
		gomega.Expect(result.Metrics.ApprovalScopeMismatches).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("expected approval scope mismatch, got %#v", result.Metrics))

		encoded, err := json.Marshal(result)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal audit result: %v", err))

		for _, leaked := range []string{"raw_prompt", "customer-acme", "token=", "/home/example/private"} {
			gomega.Expect(string(encoded)).NotTo(gomega.ContainSubstring(leaked), fmt.Sprintf("partial approval audit leaked sensitive marker %q: %s", leaked, encoded))

		}
	})
	ginkgo.It("plan artifact audit rejects pending partial approval source satisfied path", func() {
		result := AuditPlanArtifact(ginkgo.GinkgoT().TempDir(), planartifact.PlanArtifact{
			SchemaVersion: planartifact.SchemaVersion,
			PlanID:        "plan-partial-source-satisfied",
			Task:          planartifact.PlanArtifactTask{Summary: "Block pending partial approval source-satisfied scope"},
			Contract:      planartifact.PlanArtifactContract{Kind: "plan_then_execute"},
			Paths: planartifact.PlanArtifactPaths{
				Editable:        []string{"b.txt"},
				Changed:         []string{"b.txt"},
				Required:        []string{"a.txt", "b.txt"},
				SourceSatisfied: []string{"a.txt"},
			},
			Evidence: []planartifact.PlanArtifactEvidence{{Kind: "harness_observed_source", Path: "a.txt"}},
			VerificationObligations: []planartifact.PlanArtifactVerificationObligation{{
				Kind:     "focused_tests",
				Status:   "open",
				Required: true,
			}},
			Approval: planartifact.PlanArtifactApproval{
				Required:            true,
				Status:              "partial",
				ScopeID:             "scope-1",
				ApprovedPaths:       []string{"b.txt"},
				PendingPaths:        []string{"a.txt"},
				ExecutionPermission: "execute_approved_scope",
			},
			Handoff: planartifact.PlanArtifactHandoff{ID: "handoff-1", Status: "ready", Ready: true},
			Edits:   []planartifact.PlanEdit{{Path: "b.txt", OldText: "before", NewText: "after"}},
		}, PlanArtifactAuditOptions{})
		gomega.Expect(result.OK).To(gomega.BeFalse(), fmt.Sprintf("pending partial approval source_satisfied_paths should fail audit"))
		gomega.Expect(result.Metrics.ApprovalScopeMismatches).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("expected approval scope mismatch, got %#v", result.Metrics))
		gomega.Expect(result.ErrorSummary()).To(gomega.ContainSubstring("approval_scope_mismatch"), fmt.Sprintf("expected approval scope error summary, got %#v", result))

	})
	ginkgo.It("plan artifact audit reports packet states", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeSubmitPlanFixture(root, "a.txt", "alpha\n")
		writeSubmitPlanContextView(root, "feedfacecafebeef", "a.txt")
		writeSubmitPlanFixture(root, filepath.Join(".idiom", "comprehension", "views", "deadc0decafef00d.json"), `{
  "pack_id": "deadc0decafef00d",
  "index_id": "idx-stale",
  "fresh": false,
  "candidate_files": [{"path":"a.txt","role":"editable"}]
}`)

		base := planartifact.PlanArtifact{
			SchemaVersion: planartifact.SchemaVersion,
			PlanID:        "plan-packet-state",
			Task:          planartifact.PlanArtifactTask{Summary: "audit packet state"},
			Contract:      planartifact.PlanArtifactContract{Kind: "plan_then_execute"},
			Paths:         planartifact.PlanArtifactPaths{Editable: []string{"a.txt"}, Changed: []string{"a.txt"}},
			Evidence:      []planartifact.PlanArtifactEvidence{{Kind: "planner_submission", Summary: "bounded evidence"}},
			VerificationObligations: []planartifact.PlanArtifactVerificationObligation{{
				Kind:     "focused_tests",
				Status:   "open",
				Required: true,
			}},
			Approval: planartifact.PlanArtifactApproval{Status: "implicit"},
			Handoff:  planartifact.PlanArtifactHandoff{ID: "handoff-1", Status: "ready", Ready: true},
			Edits:    []planartifact.PlanEdit{{Path: "a.txt", OldText: "alpha", NewText: "ALPHA"}},
		}
		inherited := base

		inherited.PackID = "feedfacecafebeef"
		inherited.Packet = planartifact.PlanArtifactPacket{PackID: "feedfacecafebeef", Status: "available", Freshness: "fresh"}

		if result := AuditPlanArtifact(root, inherited, PlanArtifactAuditOptions{}); result.Metrics.PacketState != "inherited" || !result.Metrics.PacketFresh {
			ginkgo.Fail(fmt.Sprintf("expected inherited packet state, got %#v", result.Metrics))
		}

		stale := base

		stale.PackID = "deadc0decafef00d"
		stale.Packet = planartifact.PlanArtifactPacket{PackID: "deadc0decafef00d", Status: "available", Freshness: "fresh"}

		if result := AuditPlanArtifact(root, stale, PlanArtifactAuditOptions{}); result.Metrics.PacketState != "stale" || result.Metrics.PacketFresh {
			ginkgo.Fail(fmt.Sprintf("expected stale packet state, got %#v", result.Metrics))
		}

		missing := base

		missing.Packet = planartifact.PlanArtifactPacket{Status: "not_provided"}

		if result := AuditPlanArtifact(root, missing, PlanArtifactAuditOptions{}); result.Metrics.PacketState != "missing" || result.Metrics.PacketReason != "not_provided" {
			ginkgo.Fail(fmt.Sprintf("expected missing packet state, got %#v", result.Metrics))
		}

		overridden := inherited

		overridden.Packet.PackID = "deadc0decafef00d"

		if result := AuditPlanArtifact(root, overridden, PlanArtifactAuditOptions{}); result.Metrics.PacketState != "overridden" || result.Metrics.PacketReason != "packet_id_mirror" {
			ginkgo.Fail(fmt.Sprintf("expected overridden packet state, got %#v", result.Metrics))
		}
	})
	ginkgo.It("submit plan sanitizes structured edit purpose metadata", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "a.txt", "before\n")
		writeSubmitPlanContextView(root, "feedfacecafebeef", "a.txt")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "submit_plan",
			PlanArtifact: &planartifact.PlanArtifact{
				SchemaVersion: planartifact.SchemaVersion,
				PlanID:        "plan-structured-purpose-privacy",
				Task: planartifact.PlanArtifactTask{
					ID:                    "task-structured-purpose-privacy",
					Summary:               "Bound edit-purpose metadata before durable persistence",
					ClientClassifications: []string{"finance", "local_model_private_code"},
				},
				Contract: planartifact.PlanArtifactContract{
					Kind:              "plan_then_execute",
					StrategyOwner:     "model",
					ContractOwner:     "harness",
					PermissionProfile: "plan_then_execute",
					VerificationOwner: "executor",
				},
				Packet: planartifact.PlanArtifactPacket{PackID: "feedfacecafebeef", Status: "available", Freshness: "fresh"},
				Paths:  planartifact.PlanArtifactPaths{Editable: []string{"a.txt"}, Required: []string{"a.txt"}},
				Evidence: []planartifact.PlanArtifactEvidence{{
					ID:        "evidence-1",
					Kind:      "context_packet",
					Handle:    "context_pack://feedfacecafebeef",
					Freshness: "fresh",
				}},
				VerificationObligations: []planartifact.PlanArtifactVerificationObligation{{
					ID:       "verification-1",
					Kind:     "focused_tests",
					Command:  "go test ./internal/capabilities/workspace",
					Owner:    "executor",
					Status:   "open",
					Required: true,
				}},
				Approval: planartifact.PlanArtifactApproval{Required: true, Status: "approved", ScopeID: "scope-1", Mode: "local"},
				Handoff:  planartifact.PlanArtifactHandoff{ID: "handoff-1", From: "planner", To: "executor", Status: "ready", Ready: true},
				Edits: []planartifact.PlanEdit{{
					Path:    "a.txt",
					OldText: "before",
					NewText: "after",
					Purpose: "SECRET raw_prompt customer-acme transcript token=SECRET logs/telemetry/secrets/customer-acme/file_body rotate session token for customer acme at /home/example/private/client/repo",
				}},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("submit_plan rejected structured artifact with sensitive metadata: %s", result.Error))

		artifact := assertSubmitPlanNoSensitiveMarkers(root, result, []string{
			"SECRET",
			"raw_prompt",
			"customer-acme",
			"customer_acme",
			"transcript",
			"token=",
			"rotate session token",
			"customer acme",
			"logs/telemetry/secrets",
			"file_body",
			"/home/example/private/client/repo",
			"/users/ben/private/client/repo",
			"private/client/repo",
		})
		gomega.Expect(artifact.Edits).To(gomega.HaveLen(1), fmt.Sprintf("expected one edit, got %d", len(artifact.Edits)))

		edit := artifact.Edits[0]
		gomega.Expect(strings.HasPrefix(edit.Purpose, "redacted:")).To(gomega.BeTrue(), fmt.Sprintf("sensitive purpose should persist as bounded redaction, got %q", edit.Purpose))
		gomega.Expect(edit.Path).To(gomega.Equal("a.txt"), fmt.Sprintf("executor-required edit contract changed: %#v", edit))
		gomega.Expect(edit.OldText).To(gomega.Equal("before"), fmt.Sprintf("executor-required edit contract changed: %#v", edit))
		gomega.Expect(edit.NewText).To(gomega.Equal("after"), fmt.Sprintf("executor-required edit contract changed: %#v", edit))

	})
	ginkgo.It("submit plan sanitizes legacy edit purpose metadata", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "a.txt", "before\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "legacy path purpose privacy",
			Edits: []planartifact.PlanEdit{{
				Path:    "a.txt",
				OldText: "before",
				NewText: "after",
				Purpose: "SECRET raw_prompt customer-acme transcript token=SECRET prompt_body rotate session token for customer acme at /home/example/private/client/repo",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("submit_plan rejected legacy artifact with sensitive metadata: %s", result.Error))

		artifact := assertSubmitPlanNoSensitiveMarkers(root, result, []string{
			"SECRET",
			"raw_prompt",
			"customer-acme",
			"customer_acme",
			"transcript",
			"token=",
			"rotate session token",
			"customer acme",
			"prompt_body",
			"/home/example/private/client/repo",
			"/users/ben/private/client/repo",
			"private/client/repo",
		})
		gomega.Expect(artifact.Edits).To(gomega.HaveLen(1), fmt.Sprintf("expected one edit, got %d", len(artifact.Edits)))

		edit := artifact.Edits[0]
		gomega.Expect(strings.HasPrefix(edit.Purpose, "redacted:")).To(gomega.BeTrue(), fmt.Sprintf("sensitive legacy purpose should persist as bounded redaction, got %q", edit.Purpose))
		gomega.Expect(edit.Path).To(gomega.Equal("a.txt"), fmt.Sprintf("executor-required edit contract changed: %#v", edit))
		gomega.Expect(edit.OldText).To(gomega.Equal("before"), fmt.Sprintf("executor-required edit contract changed: %#v", edit))
		gomega.Expect(edit.NewText).To(gomega.Equal("after"), fmt.Sprintf("executor-required edit contract changed: %#v", edit))

	})
	ginkgo.It("submit plan structured audit rejects packet and scope mismatch privately", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "a.txt", "before\n")
		writeSubmitPlanFixture(root, "b.txt", "before\n")
		writeSubmitPlanContextView(root, "feedfacecafebeef", "a.txt")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "submit_plan",
			PlanArtifact: &planartifact.PlanArtifact{
				SchemaVersion: planartifact.SchemaVersion,
				PlanID:        "plan-audit-scope",
				Task: planartifact.PlanArtifactTask{
					ID:                    "task-audit-scope",
					Summary:               "Check submitted scope against packet evidence",
					ClientClassifications: []string{"enterprise_saas"},
				},
				Contract: planartifact.PlanArtifactContract{
					Kind:              "plan_then_execute",
					StrategyOwner:     "model",
					ContractOwner:     "harness",
					PermissionProfile: "plan_then_execute",
					VerificationOwner: "executor",
					RequiredEditPaths: []string{"a.txt"},
				},
				Packet: planartifact.PlanArtifactPacket{PackID: "feedfacecafebeef", Status: "available", Freshness: "fresh"},
				Paths:  planartifact.PlanArtifactPaths{Editable: []string{"a.txt"}, Required: []string{"a.txt"}},
				Evidence: []planartifact.PlanArtifactEvidence{{
					ID:        "evidence-1",
					Kind:      "context_packet",
					Handle:    "context_pack://feedfacecafebeef",
					Path:      "logs/telemetry/secrets/customer-acme/feedback.jsonl",
					Freshness: "fresh",
				}},
				UnresolvedQuestions: []planartifact.PlanArtifactQuestion{{
					ID:                    "question-1",
					Claim:                 "raw_prompt customer transcript body",
					Status:                "open",
					RequiredBeforeHandoff: true,
				}},
				VerificationObligations: []planartifact.PlanArtifactVerificationObligation{{
					ID:       "verification-1",
					Kind:     "focused_tests",
					Command:  "go test ./internal/capabilities/workspace",
					Owner:    "executor",
					Status:   "open",
					Required: true,
				}},
				Approval: planartifact.PlanArtifactApproval{Required: false, Status: "implicit", ScopeID: "scope-1", Mode: "local", ExecutionPermission: "execute_all"},
				Handoff:  planartifact.PlanArtifactHandoff{ID: "handoff-1", From: "planner", To: "executor", Status: "ready", Ready: true},
				Edits: []planartifact.PlanEdit{{
					Path:    "b.txt",
					OldText: "before",
					NewText: "after",
					Purpose: "Update fixture outside packet scope.",
				}},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("structured plan with packet/scope mismatch should be rejected"))

		for _, want := range []string{"plan_artifact.audit_errors", "scope_path_mismatch", "unresolved_question_mismatch"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("audit rejection should include %q, got %q", want, result.Error))

		}

		for _, leaked := range []string{"customer-acme", "customer_acme", "raw_prompt", "transcript", "logs/telemetry/secrets"} {
			gomega.Expect(result.Error).NotTo(gomega.ContainSubstring(leaked), fmt.Sprintf("audit rejection leaked sensitive marker %q: %s", leaked, result.Error))

		}
	})
	ginkgo.It("submit plan legacy packet mismatch is report only", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "a.txt", "before\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "legacy packet id without graph view remains executable",
			PackID:    "feedfacecafebeef",
			Edits: []planartifact.PlanEdit{{
				Path:    "a.txt",
				OldText: "before",
				NewText: "after",
				Purpose: "Update fixture.",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("legacy submit_plan should keep packet audit report-only: %s", result.Error))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("packet_identity_mismatches=1"), fmt.Sprintf("legacy audit mismatch should be visible but non-blocking, got %q", result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("audit_ok=true"), fmt.Sprintf("legacy audit mismatch should be visible but non-blocking, got %q", result.Output))

	})
	ginkgo.It("submit plan stale old text reports source satisfied hint when replacement present", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "a.txt", "after\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "stale old text",
			Edits: []planartifact.PlanEdit{{
				Path:    "a.txt",
				OldText: "before",
				NewText: "after",
				Purpose: "replace before with after",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("stale old_text should be rejected"))

		for _, want := range []string{"replacement text already appears", "Decision: source-satisfied"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("stale old_text error should include %q, got %q", want, result.Error))

		}
	})
	ginkgo.It("submit plan structured schema errors are bounded and private", func() {
		mgr, _ := newSubmitPlanTestManager()
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "submit_plan",
			PlanArtifact: &planartifact.PlanArtifact{
				SchemaVersion: planartifact.SchemaVersion,
				PlanID:        "token=SECRET",
				Task: planartifact.PlanArtifactTask{
					Summary:               "raw_prompt customer transcript token=SECRET",
					ClientClassifications: []string{"customer-acme-session"},
				},
				Edits: []planartifact.PlanEdit{{Path: "a.txt"}},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("malformed structured artifact should be rejected"))

		for _, want := range []string{"plan_artifact.schema_errors", "contract.kind", "verification_obligations", "unsupported_client_classification"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("schema error should include %q, got %q", want, result.Error))

		}

		for _, leaked := range []string{"SECRET", "raw_prompt", "customer-acme", "customer_acme", "transcript"} {
			gomega.Expect(result.Error).NotTo(gomega.ContainSubstring(leaked), fmt.Sprintf("schema error leaked sensitive text %q: %s", leaked, result.Error))

		}
	})
	ginkgo.It("submit plan rejects unsupported verification owner and status", func() {
		mgr, _ := newSubmitPlanTestManager()
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "submit_plan",
			PlanArtifact: &planartifact.PlanArtifact{
				SchemaVersion: planartifact.SchemaVersion,
				PlanID:        "plan-verification-vocabulary",
				Task:          planartifact.PlanArtifactTask{Summary: "verification vocabulary"},
				Contract:      planartifact.PlanArtifactContract{Kind: "plan_then_execute"},
				Packet:        planartifact.PlanArtifactPacket{Status: "not_provided"},
				Paths:         planartifact.PlanArtifactPaths{Editable: []string{"a.txt"}},
				Evidence:      []planartifact.PlanArtifactEvidence{{Kind: "schema_example", Summary: "bounded evidence"}},
				VerificationObligations: []planartifact.PlanArtifactVerificationObligation{{
					Kind:     "focused_tests",
					Command:  "go test ./...",
					Owner:    "future_self",
					Status:   "trust_me",
					Required: true,
				}},
				Approval: planartifact.PlanArtifactApproval{Status: "implicit", ScopeID: "scope-1", ExecutionPermission: "execute_all"},
				Handoff:  planartifact.PlanArtifactHandoff{ID: "handoff-1", Status: "ready", Ready: true},
				Edits:    []planartifact.PlanEdit{{Path: "a.txt", OldText: "old", NewText: "new"}},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("unsupported verification vocabulary should be rejected"))

		for _, want := range []string{"verification_obligations[0].owner", "verification_obligations[0].status"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("schema error should include %q, got %q", want, result.Error))

		}
	})
	ginkgo.It("plan artifact invalid edit shape error keeps full guidance", func() {
		artifact := planartifact.NormalizeForSubmit(planartifact.PlanArtifact{
			SchemaVersion: planartifact.SchemaVersion,
			PlanID:        "plan-invalid-shape",
			Created:       "2026-05-17T00:00:00Z",
			Task:          planartifact.PlanArtifactTask{Summary: "invalid shape"},
			Contract:      planartifact.PlanArtifactContract{Kind: "plan_then_execute"},
			Packet:        planartifact.PlanArtifactPacket{Status: "not_provided"},
			Paths:         planartifact.PlanArtifactPaths{Editable: []string{"a.txt"}, Changed: []string{"a.txt"}},
			Evidence:      []planartifact.PlanArtifactEvidence{{Kind: "schema_example", Summary: "bounded evidence"}},
			VerificationObligations: []planartifact.PlanArtifactVerificationObligation{{
				Kind:     "test",
				Command:  "go test ./...",
				Owner:    "planner",
				Required: true,
			}},
			Approval: planartifact.PlanArtifactApproval{Status: "implicit"},
			Handoff:  planartifact.PlanArtifactHandoff{ID: "handoff-invalid-shape", Status: "ready", Ready: true},
			Edits:    []planartifact.PlanEdit{{Path: "a.txt"}},
		}, planartifact.NormalizeForSubmitOptions{
			SessionID: "session-1",
			Created:   "2026-05-17T00:00:00Z",
			PlanID:    "plan-invalid-shape",
		})

		err := planartifact.ValidatePlanArtifact(artifact)
		gomega.Expect(err).To(gomega.HaveOccurred(), "expected invalid edit shape error")

		message := err.Error()
		gomega.Expect(message).NotTo(gomega.ContainSubstring("[truncated]"), fmt.Sprintf("invalid edit shape guidance should not be truncated, got %q", message))
		gomega.Expect(message).To(gomega.ContainSubstring("replace_all=true with new_text and empty old_text"), fmt.Sprintf("invalid edit shape guidance missing whole-file shape, got %q", message))

		for _, want := range []string{"source_satisfied_paths", "remove it from edits[]"} {
			gomega.Expect(message).To(gomega.ContainSubstring(want), fmt.Sprintf("invalid edit shape guidance missing source-satisfied coverage hint %q, got %q", want, message))

		}
	})
	ginkgo.It("submit plan invalid edit shape points source satisfied path to top level coverage", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "x.txt", "already final\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "x.txt already contains the requested final state; y.txt still needs an edit.",
			Edits: []planartifact.PlanEdit{
				{Path: "x.txt", Purpose: "already source satisfied"},
				{Path: "y.txt", OldText: "before", NewText: "after", Purpose: "real edit"},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("invalid source-satisfied edit object should be rejected"))

		for _, want := range []string{"source_satisfied_paths", "remove it from edits[]"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("submit_plan invalid shape should steer source-satisfied paths to top-level coverage %q, got %q", want, result.Error))

		}

		if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("submit_plan should reject before writing an artifact; stat err=%v", err))
		}
	})
	ginkgo.It("load plan artifact accepts legacy v1 and synthesizes durable fields", func() {
		root := ginkgo.GinkgoT().TempDir()
		path := filepath.Join(root, "legacy.plan.json")
		body := []byte(`{"schema_version":"idiom.plan.v1","rationale":"legacy prose rationale","edits":[{"path":"a.txt","old_text":"a","new_text":"b"}]}`)
		gomega.Expect(os.WriteFile(path, body, 0o644)).To(gomega.Succeed())

		plan, err := LoadPlanArtifact(path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("legacy v1 artifact should load: %v", err))
		gomega.Expect(plan.SchemaVersion).To(gomega.Equal(planartifact.LegacySchemaVersion), fmt.Sprintf("legacy schema version should be preserved for attribution, got %q", plan.SchemaVersion))
		gomega.Expect(plan.Contract.Kind).NotTo(gomega.BeEmpty(), fmt.Sprintf("legacy artifact should synthesize durable fields: %#v", plan))
		gomega.Expect(plan.Evidence).NotTo(gomega.BeEmpty(), fmt.Sprintf("legacy artifact should synthesize durable fields: %#v", plan))
		gomega.Expect(plan.VerificationObligations).NotTo(gomega.BeEmpty(), fmt.Sprintf("legacy artifact should synthesize durable fields: %#v", plan))
		gomega.Expect(plan.Handoff.Status).NotTo(gomega.BeEmpty(), fmt.Sprintf("legacy artifact should synthesize durable fields: %#v", plan))

	})
	ginkgo.It("load plan artifact rejects no op legacy edit", func() {
		root := ginkgo.GinkgoT().TempDir()
		path := filepath.Join(root, "noop.plan.json")
		body := []byte(`{"schema_version":"idiom.plan.v1","rationale":"legacy prose rationale","edits":[{"path":"a.txt","old_text":"a","new_text":"a"}]}`)
		gomega.Expect(os.WriteFile(path, body, 0o644)).To(gomega.Succeed())

		_, err := LoadPlanArtifact(path)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected no-op legacy artifact to be rejected"))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("old_text and new_text are identical"), fmt.Sprintf("expected no-op validation error, got %v", err))

	})
	ginkgo.It("validate plan artifact rejects backslash traversal path", func() {
		artifact := planartifact.PlanArtifact{
			SchemaVersion: planartifact.SchemaVersion,
			PlanID:        "plan-backslash-traversal",
			Created:       "2026-05-19T00:00:00Z",
			Task:          planartifact.PlanArtifactTask{ID: "task-backslash-traversal"},
			Contract:      planartifact.PlanArtifactContract{Kind: "plan_then_execute"},
			Packet:        planartifact.PlanArtifactPacket{Status: "not_provided"},
			Paths:         planartifact.PlanArtifactPaths{Editable: []string{"safe.txt"}},
			Evidence:      []planartifact.PlanArtifactEvidence{{Kind: "planner_submission", Summary: "test"}},
			VerificationObligations: []planartifact.PlanArtifactVerificationObligation{{
				Kind:     "test",
				Command:  "go test ./internal/capabilities/workspace",
				Owner:    "planner",
				Required: true,
			}},
			Approval: planartifact.PlanArtifactApproval{Status: "implicit", ScopeID: "scope-test", ExecutionPermission: "execute_all"},
			Handoff:  planartifact.PlanArtifactHandoff{ID: "handoff-test", Status: "ready"},
			Edits:    []planartifact.PlanEdit{{Path: `..\secret.txt`, OldText: "old", NewText: "new"}},
		}

		err := planartifact.ValidatePlanArtifact(artifact)
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected invalid edit path error, got %v", err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("edits[0].path"), fmt.Sprintf("expected invalid edit path error, got %v", err))

	})
	ginkgo.It("plan artifact client classification examples cover approved classes", func() {
		for _, class := range []string{
			string(planartifact.ClientClassificationFinance),
			string(planartifact.ClientClassificationHealthcareLifeSciences),
			string(planartifact.ClientClassificationPublicSector),
			string(planartifact.ClientClassificationEnterpriseSaaS),
			string(planartifact.ClientClassificationToolingVendor),
			string(planartifact.ClientClassificationLocalModelPrivateCode),
			string(planartifact.ClientClassificationOpenSource),
		} {
			artifact := planartifact.NormalizeForSubmit(planartifact.PlanArtifact{
				SchemaVersion: planartifact.SchemaVersion,
				PlanID:        "plan-" + class,
				Task: planartifact.PlanArtifactTask{
					ID:                    "task-" + class,
					Summary:               "bounded client classification coverage",
					ClientClassifications: []string{class},
				},
				Contract: planartifact.PlanArtifactContract{Kind: "plan_then_execute"},
				Packet:   planartifact.PlanArtifactPacket{Status: "not_provided"},
				Paths:    planartifact.PlanArtifactPaths{Editable: []string{"a.txt"}},
				Evidence: []planartifact.PlanArtifactEvidence{{Kind: "schema_example", Summary: "bounded client classification coverage"}},
				VerificationObligations: []planartifact.PlanArtifactVerificationObligation{{
					Kind:     "example_verification",
					Status:   "open",
					Required: true,
				}},
				Approval: planartifact.PlanArtifactApproval{Status: "awaiting_approval"},
				Handoff:  planartifact.PlanArtifactHandoff{Status: "ready", Ready: true},
				Edits:    []planartifact.PlanEdit{{Path: "a.txt", OldText: "a", NewText: "b"}},
			}, planartifact.NormalizeForSubmitOptions{
				Created: "2026-05-13T00:00:00Z",
				PlanID:  "plan-" + class,
			})
			gomega.Expect(planartifact.ValidatePlanArtifact(artifact)).To(gomega.Succeed())

		}
	})
	ginkgo.It("submit plan rejects empty edit list", func() {
		mgr, _ := newSubmitPlanTestManager()
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "empty plan should be rejected",
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("empty edits should be rejected"))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("at least one edit"), fmt.Sprintf("rejection should call out missing edits, got %q", result.Error))

	})
	ginkgo.It("submit plan rejects source satisfied placeholder rationale", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "a.txt", "before\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "submit_plan",
			Rationale: "The source files are already in the expected state. " +
				"I will make minimal placeholder edits to satisfy the requirement that edits be submitted.",
			Edits: []planartifact.PlanEdit{{
				Path:    "a.txt",
				OldText: "before",
				NewText: "after",
				Purpose: "Replace text.",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("source-satisfied placeholder rationale should be rejected"))
		gomega.Expect(result.Code).To(gomega.Equal(SubmitPlanSourceSatisfiedTerminalRequiredCode), fmt.Sprintf("source-satisfied placeholder rationale code = %q, want %q", result.Code, SubmitPlanSourceSatisfiedTerminalRequiredCode))

		for _, want := range []string{"source-satisfied", "placeholder"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("rejection should point to %q, got %q", want, result.Error))

		}
	})
	ginkgo.It("submit plan allows negated source satisfied rationale", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "a.txt", "before\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "The current source is not source-satisfied because it still contains the old value.",
			Edits: []planartifact.PlanEdit{{
				Path:    "a.txt",
				OldText: "before",
				NewText: "after",
				Purpose: "Replace the old value with the requested value.",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("negated source-satisfied rationale should be allowed, got %q", result.Error))

	})
	ginkgo.It("submit plan rejects mixed source satisfied coverage without evidence", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "a.txt", "before\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:                 "submit_plan",
			Rationale:            "Update a.txt; b.txt already contains the requested final state from current source evidence.",
			SourceSatisfiedPaths: []string{"b.txt"},
			Edits: []planartifact.PlanEdit{{
				Path:    "a.txt",
				OldText: "before",
				NewText: "after",
				Purpose: "Replace the old value with the requested value.",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("mixed source_satisfied_paths without current-state evidence should be rejected"))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("source_satisfied_evidence_mismatch"), fmt.Sprintf("expected evidence mismatch rejection, got %q", result.Error))

		if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("rejected submit_plan should not persist last.plan.json; stat err=%v", err))
		}
	})
	ginkgo.It("submit plan allows mixed source satisfied coverage with current state evidence", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "a.txt", "before\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:                 "submit_plan",
			Rationale:            "Update a.txt; b.txt already contains the requested final state from current source evidence.",
			SourceSatisfiedPaths: []string{"b.txt"},
			PlanArtifact: &planartifact.PlanArtifact{
				SchemaVersion: planartifact.SchemaVersion,
				Task:          planartifact.PlanArtifactTask{Summary: "mixed source-satisfied coverage"},
				Contract: planartifact.PlanArtifactContract{
					Kind:              "plan_then_execute",
					StrategyOwner:     "model",
					ContractOwner:     "harness",
					PermissionProfile: "plan_then_execute",
					VerificationOwner: "planner",
				},
				Paths: planartifact.PlanArtifactPaths{
					Editable: []string{"a.txt"},
					Changed:  []string{"a.txt"},
					Required: []string{"a.txt", "b.txt"},
				},
				Evidence: []planartifact.PlanArtifactEvidence{{
					Kind: "harness_observed_source",
					Path: "b.txt",
				}},
				VerificationObligations: []planartifact.PlanArtifactVerificationObligation{{
					Kind:     "focused_tests",
					Status:   "open",
					Required: true,
				}},
				Approval: planartifact.PlanArtifactApproval{Required: true, Status: "approved", ScopeID: "scope-1", Mode: "local"},
				Handoff:  planartifact.PlanArtifactHandoff{ID: "handoff-1", From: "planner", To: "executor", Status: "ready", Ready: true},
				Edits: []planartifact.PlanEdit{{
					Path:    "a.txt",
					OldText: "before",
					NewText: "after",
					Purpose: "Replace the old value with the requested value.",
				}},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("mixed source_satisfied_paths with current-state evidence should be allowed, got %q", result.Error))

		plan, err := LoadPlanArtifact(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load submitted plan: %v", err))
		gomega.Expect(submitPlanTestContains(plan.Paths.SourceSatisfied, "b.txt")).To(gomega.BeTrue(), fmt.Sprintf("source_satisfied_paths should persist without synthesizing required scope: %#v", plan.Paths))
		gomega.Expect(submitPlanTestContains(plan.Paths.Required, "b.txt")).To(gomega.BeTrue(), fmt.Sprintf("source_satisfied_paths should persist without synthesizing required scope: %#v", plan.Paths))

	})
	ginkgo.It("submit plan allows scoped no changes rationale", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "a.txt", "before\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "No changes are needed outside the approved scope; update a.txt.",
			Edits: []planartifact.PlanEdit{{
				Path:    "a.txt",
				OldText: "before",
				NewText: "after",
				Purpose: "Replace the old value with the requested value.",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("scoped no-changes rationale should be allowed, got %q", result.Error))

	})
	ginkgo.It("submit plan rejects verification only edit purposes", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "a.txt", "before\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "Check current source evidence.",
			Edits: []planartifact.PlanEdit{{
				Path:    "a.txt",
				OldText: "before",
				NewText: "after",
				Purpose: "Verify that the expected text is present.",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("verification-only edit purpose should be rejected"))

		for _, want := range []string{"verification", "source-satisfied"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("rejection should point to %q, got %q", want, result.Error))

		}
	})
	ginkgo.Describe("submit plan rejects placeholder edit bodies", func() {
		for _, tc := range []struct {
			name string
			edit planartifact.PlanEdit
			want string
		}{
			{
				name: "substring replacement",
				edit: planartifact.PlanEdit{
					Path:    "a.txt",
					OldText: "func run() {\n}\n",
					NewText: "func run() {\n\t// ... existing code ...\n}\n",
					Purpose: "Update run.",
				},
				want: "existing code",
			},
			{
				name: "line range content",
				edit: planartifact.PlanEdit{
					Path:      "a.txt",
					StartLine: 3,
					EndLine:   4,
					Content:   "func run() {\n\t<TODO: fill implementation>\n}\n",
					Purpose:   "Update run.",
				},
				want: "<todo",
			},
			{
				name: "generic todo marker",
				edit: planartifact.PlanEdit{
					Path:    "a.cpp",
					OldText: "func run() {\n}\n",
					NewText: "func run() {\n\t// TODO: Implement actual dispatch logic.\n}\n",
					Purpose: "Update run.",
				},
				want: "todo:",
			},
		} {
			ginkgo.It(tc.name, func() {
				mgr, root := newSubmitPlanTestManager()
				writeSubmitPlanFixture(root, "a.txt", "package p\n\nfunc run() {\n}\n")

				result := mgr.Execute(context.Background(), ToolRequest{
					Tool:      "submit_plan",
					Rationale: "Update executable source.",
					Edits:     []planartifact.PlanEdit{tc.edit},
				})
				gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("placeholder edit body should be rejected"))

				for _, want := range []string{"placeholder edit body", tc.want, "<<QUESTION>>"} {
					gomega.Expect(strings.ToLower(result.Error)).To(gomega.ContainSubstring(strings.ToLower(want)), fmt.Sprintf("rejection should include %q, got %q", want, result.Error))

				}
			})
		}
	})
	ginkgo.It("submit plan allows todo list in non source generated body", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "docs/notes.md", "# Notes\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "Add planning notes.",
			Edits: []planartifact.PlanEdit{{
				Path:    "docs/notes.md",
				OldText: "# Notes\n",
				NewText: "# Notes\n\nTODO:\n- Follow up with product owner.\n",
				Purpose: "add non-source TODO notes",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("non-source TODO list should be allowed, got %q", result.Error))

	})
	ginkgo.It("submit plan rejects for now placeholder proof text", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "a.cpp", "int dispatch() {\n    return 0;\n}\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "Update executable source.",
			Edits: []planartifact.PlanEdit{{
				Path:    "a.cpp",
				OldText: "int dispatch() {\n    return 0;\n}\n",
				NewText: "int dispatch() {\n    // For now, return the severity as a placeholder result code.\n    return severity;\n}\n",
				Purpose: "Update dispatch.",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), "for-now placeholder proof text should be rejected")

		for _, want := range []string{"unsupported proof claim", "self-disqualifying", "for now"} {
			gomega.Expect(strings.ToLower(result.Error)).To(gomega.ContainSubstring(strings.ToLower(want)), fmt.Sprintf("rejection should include %q, got %q", want, result.Error))

		}
	})
	ginkgo.It("submit plan allows replacing existing todo anchor with final code", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "a.cpp", "int dispatch() {\n    // TODO: Implement actual dispatch logic.\n    return 0;\n}\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "Replace existing TODO with final dispatch.",
			Edits: []planartifact.PlanEdit{{
				Path:    "a.cpp",
				OldText: "int dispatch() {\n    // TODO: Implement actual dispatch logic.\n    return 0;\n}\n",
				NewText: "int dispatch() {\n    return compute_dispatch_result();\n}\n",
				Purpose: "replace existing TODO with final code",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("existing TODO anchor with final replacement should be allowed, got %q", result.Error))

	})
	ginkgo.It("submit plan allows replacing existing bracket todo anchor with final code", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "a.cpp", "int dispatch() {\n    <TODO: fill implementation>\n}\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "Replace existing bracket TODO with final dispatch.",
			Edits: []planartifact.PlanEdit{{
				Path:    "a.cpp",
				OldText: "int dispatch() {\n    <TODO: fill implementation>\n}\n",
				NewText: "int dispatch() {\n    return compute_dispatch_result();\n}\n",
				Purpose: "replace existing bracket TODO with final code",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("existing bracket TODO anchor with final replacement should be allowed, got %q", result.Error))

	})
	ginkgo.It("submit plan rejects whole file replace all for existing source file", func() {
		mgr, root := newSubmitPlanTestManager()
		path := "native/src/simulation/world/collapse_event.h"
		writeSubmitPlanFixture(root, path, "#pragma once\n\nnamespace colony {\nstruct CollapseEvent {};\n}\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "Rewrite collapse event helper.",
			Edits: []planartifact.PlanEdit{{
				Path:       path,
				NewText:    "#ifndef SUROCCUS_SIMULATION_WORLD_COLLAPSE_EVENT_H\n#define SUROCCUS_SIMULATION_WORLD_COLLAPSE_EVENT_H\nnamespace suroccus { class CollapseEvent {}; }\n#endif\n",
				ReplaceAll: true,
				Purpose:    "replace existing collapse event header",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), "whole-file replace_all on existing source file should be rejected")

		for _, want := range []string{"whole-file", "existing source file", "bounded anchored edit"} {
			gomega.Expect(strings.ToLower(result.Error)).To(gomega.ContainSubstring(strings.ToLower(want)), fmt.Sprintf("rejection should include %q, got %q", want, result.Error))

		}

		if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("rejected submit_plan should not persist last.plan.json; stat err=%v", err))
		}
	})
	ginkgo.It("submit plan rejects edit without shape", func() {
		mgr, _ := newSubmitPlanTestManager()
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "submit_plan",
			Edits: []planartifact.PlanEdit{
				{Path: "a.txt", Purpose: "missing both shapes"},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("malformed edit shape should be rejected"))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("edits[0]"), fmt.Sprintf("rejection should index the bad edit, got %q", result.Error))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("replace_all=true"), fmt.Sprintf("rejection should name the missing/new-file shape, got %q", result.Error))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("missing/new-file"), fmt.Sprintf("rejection should name the missing/new-file shape, got %q", result.Error))

	})
	ginkgo.It("submit plan rejects json old text without replacement field", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "x.txt", "keep\n")

		req, err := DecodeToolRequestJSON([]byte(`{"tool":"submit_plan","edits":[{"path":"x.txt","old_text":"keep"}]}`))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("DecodeToolRequestJSON: %v", err))

		result := mgr.Execute(context.Background(), req)
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("old_text-only JSON edit should be rejected"))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("new_text"), fmt.Sprintf("rejection should name the missing replacement field, got %q", result.Error))

		if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); err == nil || !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("submit_plan should reject before writing an artifact; stat err=%v", err))
		}
	})
	ginkgo.It("load plan artifact allows legacy deletion without new text field", func() {
		root := ginkgo.GinkgoT().TempDir()
		path := filepath.Join(root, "legacy.plan.json")
		body := []byte(`{"schema_version":"idiom.plan.v2","created":"2026-05-20T00:00:00Z","plan_id":"legacy-delete","task":{"summary":"delete obsolete line"},"contract":{"kind":"implementation"},"packet":{"status":"not_used"},"paths":{"editable":["x.txt"]},"evidence":[{"kind":"source","path":"x.txt","summary":"current source"}],"verification_obligations":[{"command":"go test ./...","reason":"default"}],"approval":{"status":"approved","scope_id":"all","execution_permission":"execute_all"},"handoff":{"id":"handoff-1","status":"ready","ready":true},"edits":[{"path":"x.txt","old_text":"obsolete\n","purpose":"delete obsolete line"}]}`)
		gomega.Expect(os.WriteFile(path, body, 0o644)).To(gomega.Succeed())

		artifact, err := LoadPlanArtifact(path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("legacy deletion artifact should load: %v", err))
		gomega.Expect(artifact.SchemaVersion).To(gomega.Equal(planartifact.PreviousSchemaVersion), fmt.Sprintf("loaded schema version = %q, want previous schema", artifact.SchemaVersion))
		gomega.Expect(artifact.Edits).To(gomega.HaveLen(1), fmt.Sprintf("legacy deletion edit should be normalized as explicit replacement: %#v", artifact.Edits))
		gomega.Expect(planartifact.PlanEditHasExplicitReplacementText(artifact.Edits[0])).To(gomega.BeTrue(), fmt.Sprintf("legacy deletion edit should be normalized as explicit replacement: %#v", artifact.Edits))

	})
	ginkgo.It("load plan artifact attaches missing current anchor provenance", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeSubmitPlanFixture(root, "x.txt", "old\n")

		artifact := planartifact.PlanArtifact{
			SchemaVersion: planartifact.SchemaVersion,
			PlanID:        "current-no-provenance",
			Created:       "2026-05-20T00:00:00Z",
			Task:          planartifact.PlanArtifactTask{Summary: "update x"},
			Contract:      planartifact.PlanArtifactContract{Kind: "implementation"},
			Packet:        planartifact.PlanArtifactPacket{Status: "not_used"},
			Paths:         planartifact.PlanArtifactPaths{Editable: []string{"x.txt"}},
			Evidence:      []planartifact.PlanArtifactEvidence{{Kind: "harness_observed_source", Path: "x.txt", Summary: "current source"}},
			VerificationObligations: []planartifact.PlanArtifactVerificationObligation{{
				Command: "go test ./...",
				Kind:    "command",
			}},
			Approval: planartifact.PlanArtifactApproval{Status: "approved", ScopeID: "all", ExecutionPermission: "execute_all"},
			Handoff:  planartifact.PlanArtifactHandoff{ID: "handoff-1", Status: "ready", Ready: true},
			Edits:    []planartifact.PlanEdit{{Path: "x.txt", OldText: "old", NewText: "new", Purpose: "update x"}},
		}

		body, err := json.Marshal(artifact)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal artifact: %v", err))

		planDir := filepath.Join(root, ".idiom", "plans")
		gomega.Expect(os.MkdirAll(planDir, 0o755)).To(gomega.Succeed())

		path := filepath.Join(planDir, "current.plan.json")
		gomega.Expect(os.WriteFile(path, body, 0o644)).To(gomega.Succeed())

		loaded, err := LoadPlanArtifact(path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("current-schema artifact should load with repaired provenance: %v", err))
		gomega.Expect(loaded.Edits).To(gomega.HaveLen(1), fmt.Sprintf("loaded current artifact should have anchor provenance: %#v", loaded.Edits))
		gomega.Expect(loaded.Edits[0].AnchorProvenance).NotTo(gomega.BeNil(), fmt.Sprintf("loaded current artifact should have anchor provenance: %#v", loaded.Edits))

	})
	ginkgo.It("submit plan accepts substring deletion", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "x.txt", "keep\nremove me\nkeep\n")

		req, err := DecodeToolRequestJSON([]byte(`{"tool":"submit_plan","edits":[{"path":"x.txt","old_text":"remove me\n","new_text":"","purpose":"delete obsolete line"}]}`))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("DecodeToolRequestJSON: %v", err))

		result := mgr.Execute(context.Background(), req)
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("substring deletion should be a valid plan edit: %s", result.Error))

		if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); err != nil {
			ginkgo.Fail(fmt.Sprintf("submit_plan should write an artifact for valid deletion edit: %v", err))
		}
	})
	ginkgo.It("submit plan rejects no op substring edit before writing artifact", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "x.txt", "keep\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "submit_plan",
			Edits: []planartifact.PlanEdit{
				{Path: "x.txt", OldText: "keep\n", NewText: "keep\n", Purpose: "no-op repair"},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("no-op substring edit should be rejected"))

		for _, want := range []string{"old_text and new_text are identical", "source_satisfied_paths", "Decision: source-satisfied"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("no-op rejection should include %q, got %q", want, result.Error))

		}

		for _, want := range []string{"remove no-op edit", "`source_satisfied_paths`: [\"x.txt\"]"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("no-op rejection should include path-specific repair %q, got %q", want, result.Error))

		}
		gomega.Expect(result.Error).NotTo(gomega.ContainSubstring("Decision: pass"), fmt.Sprintf("planner no-op guidance must not leak review-repair pass wording, got %q", result.Error))
		gomega.Expect(result.Code).To(gomega.Equal(SubmitPlanNoOpEditCode))
		gomega.Expect(result.Facts).NotTo(gomega.BeNil())
		gomega.Expect(result.Facts.SubmitPlanDiagnostics).To(gomega.Equal([]toolfacts.SubmitPlanDiagnostic{{
			Code: SubmitPlanNoOpEditCode, Kind: toolfacts.SubmitPlanDiagnosticNoOpEdit, CanonicalID: "x.txt",
		}}))

		if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("submit_plan should reject before writing an artifact; stat err=%v", err))
		}
	})
	ginkgo.It("submit plan repairs stringified multiline edits before validation", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "x.txt", "keep\nsecond\n")

		inner := `[{"path":"x.txt","old_text":"keep
second
","new_text":"keep
second
","purpose":"no edit needed"}]`

		payload, err := json.Marshal(map[string]any{
			"tool":      "submit_plan",
			"rationale": "plan",
			"edits":     inner,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal payload: %v", err))

		req, err := DecodeToolRequestJSON(payload)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("DecodeToolRequestJSON should repair stringified edits: %v", err))
		gomega.Expect(req.Edits).To(gomega.HaveLen(1), fmt.Sprintf("unexpected repaired edits: %#v", req.Edits))
		gomega.Expect(req.Edits[0].Path).To(gomega.Equal("x.txt"), fmt.Sprintf("unexpected repaired edits: %#v", req.Edits))

		result := mgr.Execute(context.Background(), req)
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("submit_plan should still reject the repaired no-op edit"))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("old_text and new_text are identical"), fmt.Sprintf("no-op validation should run after repair, got %q", result.Error))

		if _, statErr := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(statErr) {
			ginkgo.Fail(fmt.Sprintf("submit_plan should reject before writing an artifact; stat err=%v", statErr))
		}
	})
	ginkgo.It("submit plan rejects no op range edit before writing artifact", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "x.txt", "keep\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "submit_plan",
			Edits: []planartifact.PlanEdit{
				{Path: "x.txt", StartLine: 1, EndLine: 1, Content: "keep\n", Purpose: "no-op repair"},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("no-op range edit should be rejected"))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("edit would not change x.txt"), fmt.Sprintf("no-op range rejection should name unchanged edit, got %q", result.Error))

		for _, want := range []string{"source_satisfied_paths", "Decision: source-satisfied"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("no-op range rejection should include %q, got %q", want, result.Error))

		}

		for _, want := range []string{"remove no-op edit", "`source_satisfied_paths`: [\"x.txt\"]"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("no-op range rejection should include path-specific repair %q, got %q", want, result.Error))

		}
		gomega.Expect(result.Error).NotTo(gomega.ContainSubstring("Decision: pass"), fmt.Sprintf("planner no-op guidance must not leak review-repair pass wording, got %q", result.Error))

		if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("submit_plan should reject before writing an artifact; stat err=%v", err))
		}
	})
})
var _ = ginkgo.Describe("submit plan accepts insert after line middle and end", func() {
	ginkgo.It("middle", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "x.txt", "one\ntwo\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "Insert a middle line.",
			Edits: []planartifact.PlanEdit{{
				Path:            "x.txt",
				InsertAfterLine: 1,
				Content:         "inserted",
				Purpose:         "Insert the requested middle line.",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("submit_plan rejected middle insertion: %s", result.Error))

		plan, err := LoadPlanArtifact(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load middle insertion plan: %v", err))

		updated, changed, err := PreviewPlanEditContent("one\ntwo\n", plan.Edits[0])
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("preview middle insertion: %v", err))
		gomega.Expect(changed).To(gomega.BeTrue(), fmt.Sprintf("middle insertion preview mismatch: changed=%v updated=%q", changed, updated))
		gomega.Expect(updated).To(gomega.Equal("one\ninserted\ntwo\n"), fmt.Sprintf("middle insertion preview mismatch: changed=%v updated=%q", changed, updated))

	})
	ginkgo.It("end preserves boundary", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "x.txt", "one\ntwo")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "Append after the final line.",
			Edits: []planartifact.PlanEdit{{
				Path:            "x.txt",
				InsertAfterLine: 2,
				Content:         "three\n",
				Purpose:         "Append the requested final line.",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("submit_plan rejected end insertion: %s", result.Error))

		plan, err := LoadPlanArtifact(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load end insertion plan: %v", err))

		updated, changed, err := PreviewPlanEditContent("one\ntwo", plan.Edits[0])
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("preview end insertion: %v", err))
		gomega.Expect(changed).To(gomega.BeTrue(), fmt.Sprintf("end insertion preview mismatch: changed=%v updated=%q", changed, updated))
		gomega.Expect(updated).To(gomega.Equal("one\ntwo\nthree\n"), fmt.Sprintf("end insertion preview mismatch: changed=%v updated=%q", changed, updated))

	})
	ginkgo.It("preserves crlf", func() {
		updated, changed, err := PreviewPlanEditContent("one\r\ntwo\r\n", planartifact.PlanEdit{
			Path:            "x.txt",
			InsertAfterLine: 1,
			Content:         "inserted",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("preview crlf insertion: %v", err))
		gomega.Expect(changed).To(gomega.BeTrue(), fmt.Sprintf("crlf insertion preview mismatch: changed=%v updated=%q", changed, updated))
		gomega.Expect(updated).To(gomega.Equal("one\r\ninserted\r\ntwo\r\n"), fmt.Sprintf("crlf insertion preview mismatch: changed=%v updated=%q", changed, updated))

	})
	ginkgo.It("newline terminated end appends newline", func() {
		updated, changed, err := PreviewPlanEditContent("one\ntwo\n", planartifact.PlanEdit{
			Path:            "x.txt",
			InsertAfterLine: 2,
			Content:         "three",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("preview newline-terminated insertion: %v", err))
		gomega.Expect(changed).To(gomega.BeTrue(), fmt.Sprintf("newline-terminated insertion preview mismatch: changed=%v updated=%q", changed, updated))
		gomega.Expect(updated).To(gomega.Equal("one\ntwo\nthree\n"), fmt.Sprintf("newline-terminated insertion preview mismatch: changed=%v updated=%q", changed, updated))

	})
})
var _ = ginkgo.Describe("submit plan rejects invalid insert after line shapes", func() {

	for _, tc := range []struct {
		name  string
		seed  string
		edit  planartifact.PlanEdit
		wants []string
	}{
		{
			name: "missing file",
			edit: planartifact.PlanEdit{Path: "missing.txt", InsertAfterLine: 1, Content: "new\n"},
			wants: []string{
				"insert_after_line preflight could not read existing",
			},
		},
		{
			name:  "out of range",
			seed:  "one\n",
			edit:  planartifact.PlanEdit{Path: "x.txt", InsertAfterLine: 2, Content: "new\n"},
			wants: []string{"insert_after_line 2 is outside"},
		},
		{
			name:  "combined substring",
			seed:  "one\n",
			edit:  planartifact.PlanEdit{Path: "x.txt", InsertAfterLine: 1, Content: "new\n", OldText: "one\n", NewText: "ONE\n"},
			wants: []string{"mutually exclusive"},
		},
		{
			name:  "combined range",
			seed:  "one\n",
			edit:  planartifact.PlanEdit{Path: "x.txt", InsertAfterLine: 1, StartLine: 1, EndLine: 1, Content: "new\n"},
			wants: []string{"mutually exclusive"},
		},
		{
			name:  "content ref",
			seed:  "one\n",
			edit:  planartifact.PlanEdit{Path: "x.txt", InsertAfterLine: 1, ContentRef: "assets/body.txt"},
			wants: []string{"do not support content_ref"},
		},
	} {
		ginkgo.It(tc.name, func() {
			mgr, root := newSubmitPlanTestManager()
			if tc.seed != "" {
				writeSubmitPlanFixture(root, "x.txt", tc.seed)
			}

			result := mgr.Execute(context.Background(), ToolRequest{
				Tool:      "submit_plan",
				Rationale: "Try insertion.",
				Edits:     []planartifact.PlanEdit{tc.edit},
			})
			gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("invalid insertion should be rejected"))

			for _, want := range tc.wants {
				gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("rejection missing %q: %q", want, result.Error))

			}

			if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
				ginkgo.Fail(fmt.Sprintf("submit_plan should reject before writing an artifact; stat err=%v", err))
			}
		})
	}
})
var _ = ginkgo.Describe("submit plan", func() {
	ginkgo.It("submit plan rejects no op whole file edit before writing artifact or assets", func() {
		mgr, root := newSubmitPlanTestManager()
		large := strings.Repeat("same line\n", 600)
		writeSubmitPlanFixture(root, "large.txt", large)

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "submit_plan",
			Edits: []planartifact.PlanEdit{
				{Path: "large.txt", NewText: large, ReplaceAll: true, Purpose: "no-op whole-file repair"},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("no-op whole-file edit should be rejected"))

		for _, want := range []string{"edit would not change large.txt", "Decision: source-satisfied", "`source_satisfied_paths`: [\"large.txt\"]"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("no-op whole-file rejection should include %q, got %q", want, result.Error))

		}
		gomega.Expect(result.Error).NotTo(gomega.ContainSubstring("Decision: pass"), fmt.Sprintf("planner no-op guidance must not leak review-repair pass wording, got %q", result.Error))

		if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("submit_plan should reject before writing an artifact; stat err=%v", err))
		}

		matches, err := filepath.Glob(filepath.Join(root, ".idiom", "plans", "assets", "*"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("glob spill assets: %v", err))
		gomega.Expect(matches).To(gomega.BeEmpty(), fmt.Sprintf("submit_plan should reject before spilling edit assets, got %v", matches))

	})
	ginkgo.It("submit plan accepts ref backed edits", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "whole.txt", "before\n")
		writeSubmitPlanFixture(root, "range.txt", "before\n")
		writeSubmitPlanBodyRef(root, "assets/whole-body.txt", "after whole\n")
		writeSubmitPlanBodyRef(root, "assets/range-body.txt", "after range\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "submit_plan",
			Edits: []planartifact.PlanEdit{
				{Path: "whole.txt", NewTextRef: "assets/whole-body.txt", ReplaceAll: true, Purpose: "Replace whole file from a plan body ref."},
				{Path: "range.txt", StartLine: 1, EndLine: 1, ContentRef: "assets/range-body.txt", Purpose: "Replace line from a plan body ref."},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("ref-backed edits should be valid plan edits: %s", result.Error))

		planPath := filepath.Join(root, ".idiom", "plans", "last.plan.json")

		body, err := os.ReadFile(planPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read raw ref-backed plan: %v", err))

		var raw planartifact.PlanArtifact
		gomega.Expect(json.Unmarshal(body, &raw)).To(gomega.Succeed())
		gomega.Expect(raw.Edits[0].NewText).To(gomega.BeEmpty(), fmt.Sprintf("expected raw artifact to preserve refs without inline hydrated bodies, got %#v", raw.Edits))
		gomega.Expect(raw.Edits[0].NewTextRef).To(gomega.Equal("assets/whole-body.txt"), fmt.Sprintf("expected raw artifact to preserve refs without inline hydrated bodies, got %#v", raw.Edits))
		gomega.Expect(raw.Edits[1].Content).To(gomega.BeEmpty(), fmt.Sprintf("expected raw artifact to preserve refs without inline hydrated bodies, got %#v", raw.Edits))
		gomega.Expect(raw.Edits[1].ContentRef).To(gomega.Equal("assets/range-body.txt"), fmt.Sprintf("expected raw artifact to preserve refs without inline hydrated bodies, got %#v", raw.Edits))

		plan, err := LoadPlanArtifact(planPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load ref-backed plan: %v", err))
		gomega.Expect(plan.Edits).To(gomega.HaveLen(2), fmt.Sprintf("expected two ref-backed edits, got %d", len(plan.Edits)))
		gomega.Expect(plan.Edits[0].NewText).To(gomega.Equal("after whole\n"), fmt.Sprintf("expected LoadPlanArtifact to hydrate refs, got %#v", plan.Edits))
		gomega.Expect(plan.Edits[1].Content).To(gomega.Equal("after range\n"), fmt.Sprintf("expected LoadPlanArtifact to hydrate refs, got %#v", plan.Edits))

	})
	ginkgo.It("submit plan rejects no op ref backed edit before writing artifact", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "range.txt", "same\n")
		writeSubmitPlanBodyRef(root, "assets/range-body.txt", "same\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "submit_plan",
			Edits: []planartifact.PlanEdit{
				{Path: "range.txt", StartLine: 1, EndLine: 1, ContentRef: "assets/range-body.txt", Purpose: "no-op ref-backed repair"},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("no-op ref-backed edit should be rejected"))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("edit would not change range.txt"), fmt.Sprintf("no-op ref-backed rejection should name unchanged edit, got %q", result.Error))

		if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("submit_plan should reject before writing an artifact; stat err=%v", err))
		}
	})
	ginkgo.It("submit plan rejects line range deletion with exact text guidance", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "x.txt", "one\ntwo\nthree\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "submit_plan",
			Edits: []planartifact.PlanEdit{
				{Path: "x.txt", StartLine: 2, EndLine: 2, Content: "", Purpose: "delete obsolete line"},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("empty line-range content should be rejected"))

		for _, want := range []string{"line-range edits require non-empty content", "old_text", "empty new_text"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("rejection should guide deletion shape with %q, got %q", want, result.Error))

		}

		if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("submit_plan should reject before writing an artifact; stat err=%v", err))
		}
	})
	ginkgo.It("submit plan rejects ambiguous old text before writing artifact", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "matrix.cpp", "set_floor(30.0f);\nother();\nset_floor(30.0f);\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "submit_plan",
			Edits: []planartifact.PlanEdit{
				{Path: "matrix.cpp", OldText: "set_floor(30.0f);", NewText: "floor = 30.0f;"},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("ambiguous old_text should be rejected"))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("matched 2 locations"), fmt.Sprintf("rejection should report duplicate match count, got %q", result.Error))

		for _, want := range []string{"matching start line(s): 1, 3", "read_file", "replace_all=true"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("rejection should include occurrence guidance %q, got %q", want, result.Error))

		}

		if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("submit_plan should reject before writing an artifact; stat err=%v", err))
		}
	})
	ginkgo.It("submit plan rejects missing old text before writing artifact", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "x.txt", "actual\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "submit_plan",
			Edits: []planartifact.PlanEdit{
				{Path: "x.txt", OldText: "expected", NewText: "replacement"},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("missing old_text should be rejected"))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("did not appear"), fmt.Sprintf("rejection should report missing old_text, got %q", result.Error))
		gomega.Expect(result.Code).To(gomega.Equal(SubmitPlanInvalidAnchorCode))
		gomega.Expect(result.Facts).NotTo(gomega.BeNil())
		gomega.Expect(result.Facts.SubmitPlanDiagnostics).To(gomega.Equal([]toolfacts.SubmitPlanDiagnostic{{
			Code: SubmitPlanInvalidAnchorCode, Kind: toolfacts.SubmitPlanDiagnosticInvalidAnchor, CanonicalID: "x.txt",
		}}))

		if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("submit_plan should reject before writing an artifact; stat err=%v", err))
		}
	})
	ginkgo.It("submit plan rejects truncated search anchor before writing artifact", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "catalog.go", "package main\n\nvar catalog = \"needle "+strings.Repeat("x", 800)+"\"\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "submit_plan",
			Edits: []planartifact.PlanEdit{
				{
					Path:    "catalog.go",
					OldText: "var catalog = \"needle " + strings.Repeat("x", 120) + lineTruncationMarker,
					NewText: "var catalog = \"replacement\"\n",
				},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("truncated search anchor should be rejected"))

		for _, want := range []string{"truncation marker", "search_content", "read_file", "start_line/end_line"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("rejection should include truncated-anchor guidance %q, got %q", want, result.Error))

		}

		if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("submit_plan should reject before writing an artifact; stat err=%v", err))
		}
	})
	ginkgo.It("submit plan rejects adjacent duplicate code line", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "sim.cpp", "void tick() {\n    auto& c = entities_->colonists.data()[i];\n    c.cultural_work_mult = 1.0f;\n}\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "submit_plan",
			Edits: []planartifact.PlanEdit{
				{
					Path:    "sim.cpp",
					OldText: "    c.cultural_work_mult = 1.0f;\n",
					NewText: "    auto& c = entities_->colonists.data()[i];\n    c.cultural_work_mult = 1.0f;\n",
				},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("duplicate adjacent code line should be rejected"))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("adjacent duplicate code line"), fmt.Sprintf("rejection should name adjacent duplicate code, got %q", result.Error))

		if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("submit_plan should reject before writing an artifact; stat err=%v", err))
		}
	})
	ginkgo.It("submit plan rejects completed planning artifact overwrite", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, ".planning/atoms/done.md", "# Done\n\nStatus: accepted\n\nKeep this spec.\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "submit_plan",
			Edits: []planartifact.PlanEdit{
				{
					Path:       ".planning/atoms/done.md",
					NewText:    "# Done\n\nNew draft.\n",
					ReplaceAll: true,
				},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("completed artifact overwrite should be rejected"))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("completed planning artifact"), fmt.Sprintf("rejection should name completed planning artifact, got %q", result.Error))

		if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("submit_plan should reject before writing an artifact; stat err=%v", err))
		}
	})
	ginkgo.It("submit plan allows replace all for repeated old text", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "matrix.cpp", "set_floor(30.0f);\nset_floor(30.0f);\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "submit_plan",
			Edits: []planartifact.PlanEdit{
				{Path: "matrix.cpp", OldText: "set_floor(30.0f);", NewText: "floor = 30.0f;", ReplaceAll: true},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("replace_all should allow repeated old_text: %s", result.Error))

	})
	ginkgo.It("submit plan accepts whole file new file shape", func() {
		mgr, root := newSubmitPlanTestManager()
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "create a new config file",
			Edits: []planartifact.PlanEdit{
				{
					Path:       "config/new.json",
					NewText:    "{\n  \"ok\": true\n}\n",
					ReplaceAll: true,
					Purpose:    "Create the new config file body.",
				},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("submit_plan rejected missing/new-file shape: %s", result.Error))

		plan, err := LoadPlanArtifact(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load: %v", err))
		gomega.Expect(plan.Edits).To(gomega.HaveLen(1), fmt.Sprintf("expected one edit, got %d", len(plan.Edits)))

		edit := plan.Edits[0]
		gomega.Expect(edit.ReplaceAll).To(gomega.BeTrue(), fmt.Sprintf("missing/new-file shape not persisted correctly: %#v", edit))
		gomega.Expect(edit.OldText).To(gomega.BeEmpty(), fmt.Sprintf("missing/new-file shape not persisted correctly: %#v", edit))
		gomega.Expect(edit.NewText).To(gomega.ContainSubstring(`"ok": true`), fmt.Sprintf("missing/new-file shape not persisted correctly: %#v", edit))

	})
	ginkgo.It("submit plan spills large edit bodies to refs", func() {
		mgr, root := newSubmitPlanTestManager()
		large := strings.Repeat("line\n", 1200)
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "submit_plan",
			Edits: []planartifact.PlanEdit{
				{Path: "large.txt", NewText: large, ReplaceAll: true, Purpose: "Create the large file body."},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("submit_plan rejected large whole-file body: %s", result.Error))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("content_refs: 1"), fmt.Sprintf("expected submit_plan to report one content ref, got %q", result.Output))

		body, err := os.ReadFile(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read plan: %v", err))

		var raw planartifact.PlanArtifact
		gomega.Expect(json.Unmarshal(body, &raw)).To(gomega.Succeed())
		gomega.Expect(raw.Edits).To(gomega.HaveLen(1), fmt.Sprintf("expected raw artifact to store new_text_ref instead of inline body: %#v", raw.Edits))
		gomega.Expect(raw.Edits[0].NewText).To(gomega.BeEmpty(), fmt.Sprintf("expected raw artifact to store new_text_ref instead of inline body: %#v", raw.Edits))
		gomega.Expect(raw.Edits[0].NewTextRef).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected raw artifact to store new_text_ref instead of inline body: %#v", raw.Edits))

		refPath := filepath.Join(root, ".idiom", "plans", filepath.FromSlash(raw.Edits[0].NewTextRef))

		refBody, err := os.ReadFile(refPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read spilled edit body %s: %v", refPath, err))
		gomega.Expect(string(refBody)).To(gomega.Equal(large), fmt.Sprintf("spilled large body mismatch: got %d bytes, want %d", len(refBody), len(large)))

		plan, err := LoadPlanArtifact(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load plan: %v", err))
		gomega.Expect(plan.Edits[0].NewText).To(gomega.Equal(large), fmt.Sprintf("hydrated large body mismatch: got %d bytes, want %d", len(plan.Edits[0].NewText), len(large)))

	})
	ginkgo.It("submit plan rejects large insert after line content before spill", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "x.txt", "one\ntwo\n")

		large := strings.Repeat("inserted line\n", planInlineTextSpillThreshold/len("inserted line\n")+2)
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "submit_plan",
			Edits: []planartifact.PlanEdit{{
				Path:            "x.txt",
				InsertAfterLine: 1,
				Content:         large,
				Purpose:         "Insert a large generated block.",
			}},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).NotTo(gomega.Equal(""), fmt.Sprintf("large insert_after_line content should be rejected before spill"))

		for _, want := range []string{"insert_after_line content exceeds inline submit_plan limit", "content_ref is not supported", "line-range replacement", "unchanged anchor line"} {
			gomega.Expect(result.Error).To(gomega.ContainSubstring(want), fmt.Sprintf("large insertion rejection missing %q: %q", want, result.Error))

		}
		for _, forbidden := range []string{"replace_all", "whole file", "whole-file"} {
			gomega.Expect(result.Error).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("large insertion rejection retained stale whole-file guidance %q: %q", forbidden, result.Error))

		}

		if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("large insertion should reject before writing artifact; stat err=%v", err))
		}

		matches, err := filepath.Glob(filepath.Join(root, ".idiom", "plans", "assets", "*"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("glob spill assets: %v", err))
		gomega.Expect(matches).To(gomega.BeEmpty(), fmt.Sprintf("large insertion should reject before writing spill assets, got %v", matches))

	})
	ginkgo.It("load plan artifact round trip", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "x.txt", "old\n")
		mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "test plan",
			Edits: []planartifact.PlanEdit{
				{Path: "x.txt", OldText: "old", NewText: "new"},
			},
		})

		planPath := filepath.Join(root, ".idiom", "plans", "last.plan.json")

		plan, err := LoadPlanArtifact(planPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load failed: %v", err))
		gomega.Expect(plan.Edits).To(gomega.HaveLen(1), fmt.Sprintf("round-tripped plan mismatch: %#v", plan))
		gomega.Expect(plan.Edits[0].Path).To(gomega.Equal("x.txt"), fmt.Sprintf("round-tripped plan mismatch: %#v", plan))

	})
	ginkgo.It("load plan artifact rejects wrong schema version", func() {
		root := ginkgo.GinkgoT().TempDir()
		path := filepath.Join(root, "bogus.plan.json")
		body := []byte(`{"schema_version":"idiom.plan.v0","edits":[{"path":"a.txt","old_text":"a","new_text":"b"}]}`)
		gomega.Expect(os.WriteFile(path, body, 0o644)).To(gomega.Succeed())

		_, err := LoadPlanArtifact(path)
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected schema_version mismatch error, got %v", err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("schema_version"), fmt.Sprintf("expected schema_version mismatch error, got %v", err))

	})
	ginkgo.It("submit plan persists pack id for executor rehydration", func() {
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "a.txt", "old\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "exercise pack_id plumbing",
			PackID:    "feedfacecafebeef",
			Edits: []planartifact.PlanEdit{
				{Path: "a.txt", OldText: "old", NewText: "new"},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("submit_plan rejected a valid plan: %s", result.Error))

		plan, err := LoadPlanArtifact(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load: %v", err))
		gomega.Expect(plan.PackID).To(gomega.Equal("feedfacecafebeef"), fmt.Sprintf("pack_id not persisted: got %q", plan.PackID))

	})
	ginkgo.It("submit plan stamps session id from config", func() {
		// SessionID stamped on every plan artifact lets findLatestPlanArtifact
		// disambiguate concurrent plan-execute runs in the same workspace.

		root := ginkgo.GinkgoT().TempDir()
		writeSubmitPlanFixture(root, "a.txt", "x\n")

		mgr := NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
			SessionID:    "session-deadbeef",
		})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "submit_plan",
			Edits: []planartifact.PlanEdit{
				{Path: "a.txt", OldText: "x", NewText: "y"},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("submit_plan rejected: %s", result.Error))

		plan, err := LoadPlanArtifact(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load: %v", err))
		gomega.Expect(plan.SessionID).To(gomega.Equal("session-deadbeef"), fmt.Sprintf("session_id not stamped: got %q want session-deadbeef", plan.SessionID))

	})
	ginkgo.It("submit plan omits empty session id", func() {
		// Backward compatibility: legacy callers that don't set
		// cfg.Workspace.SessionID must still produce valid v1 artifacts
		// with the field omitted.
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "a.txt", "x\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "submit_plan",
			Edits: []planartifact.PlanEdit{
				{Path: "a.txt", OldText: "x", NewText: "y"},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("submit_plan rejected: %s", result.Error))

		body, err := os.ReadFile(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read: %v", err))
		gomega.Expect(string(body)).NotTo(gomega.ContainSubstring(`"session_id"`), fmt.Sprintf("empty SessionID should be omitted from artifact, got: %s", body))

	})
	ginkgo.It("submit plan omits empty pack id", func() {
		// pack_id is optional and additive — older planners that don't
		// provide it must still produce valid v1 artifacts.
		mgr, root := newSubmitPlanTestManager()
		writeSubmitPlanFixture(root, "a.txt", "x\n")

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:      "submit_plan",
			Rationale: "no pack id provided",
			Edits: []planartifact.PlanEdit{
				{Path: "a.txt", OldText: "x", NewText: "y"},
			},
		})
		gomega.Expect(strings.TrimSpace(result.Error)).To(gomega.Equal(""), fmt.Sprintf("submit_plan rejected: %s", result.Error))

		body, err := os.ReadFile(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read: %v", err))
		gomega.Expect(string(body)).NotTo(gomega.ContainSubstring(`"pack_id"`), fmt.Sprintf("empty pack_id should be omitted from artifact, got %s", body))

	})
})
