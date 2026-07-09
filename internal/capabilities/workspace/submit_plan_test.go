package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/platform/config"
)

func newSubmitPlanTestManager(t *testing.T) (*Manager, string) {
	t.Helper()

	root := t.TempDir()
	mgr := NewManager(config.Workspace{
		Root:         root,
		CWD:          root,
		EnableWrites: true,
	})

	return mgr, root
}

func writeSubmitPlanFixture(t *testing.T, root string, path string, content string) {
	t.Helper()

	fullPath := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("create fixture parent: %v", err)
	}

	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", path, err)
	}
}

func writeSubmitPlanContextView(t *testing.T, root string, packID string, paths ...string) {
	t.Helper()

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
	if err != nil {
		t.Fatalf("marshal context view: %v", err)
	}

	writeSubmitPlanFixture(t, root, filepath.Join(".idiom", "comprehension", "views", packID+".json"), string(body))
}

func writeSubmitPlanBodyRef(t *testing.T, root string, ref string, content string) {
	t.Helper()
	writeSubmitPlanFixture(t, root, filepath.Join(".idiom", "plans", filepath.FromSlash(ref)), content)
}

func submitPlanTestContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}

	return false
}

func TestSubmitPlanToolSchemaRequiresConcreteEditSource(t *testing.T) {
	defs := BuildToolDefinitions([]string{"submit_plan"})
	if len(defs) != 1 {
		t.Fatalf("expected submit_plan schema, got %d definitions", len(defs))
	}

	for _, want := range []string{"executable final content", "no placeholders", "not a quoted JSON string", "complete file body", "insert_after_line", "small inline", "large additions"} {
		if !strings.Contains(defs[0].Description, want) {
			t.Fatalf("submit_plan description missing %q:\n%s", want, defs[0].Description)
		}
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

	if err := json.Unmarshal([]byte(defs[0].ParametersJSON), &schema); err != nil {
		t.Fatalf("decode submit_plan schema: %v\n%s", err, defs[0].ParametersJSON)
	}

	if len(schema.AnyOf) != 2 {
		t.Fatalf("submit_plan should require either top-level edits or plan_artifact, got %#v", schema.AnyOf)
	}

	if schema.Properties.Edits.MinItems != 1 || !submitPlanTestContains(schema.Properties.Edits.Items.Required, "path") {
		t.Fatalf("top-level edits should require at least one path-scoped edit, got %#v", schema.Properties.Edits)
	}

	if len(schema.Properties.Edits.Items.AnyOf) == 0 {
		t.Fatalf("edit items should require a concrete edit shape")
	}

	if !submitPlanTestAnyOfRequires(schema.Properties.Edits.Items.AnyOf, "old_text", "new_text") {
		t.Fatalf("substring edit schema should require old_text and new_text together, got %#v", schema.Properties.Edits.Items.AnyOf)
	}

	if !submitPlanTestAnyOfRequires(schema.Properties.Edits.Items.AnyOf, "insert_after_line", "content") {
		t.Fatalf("insert edit schema should require insert_after_line and content together, got %#v", schema.Properties.Edits.Items.AnyOf)
	}

	for _, field := range []string{"task", "contract", "verification_obligations", "edits"} {
		if !submitPlanTestContains(schema.Properties.PlanArtifact.Required, field) {
			t.Fatalf("plan_artifact should require %q so structured callers do not emit an edits-only wrapper, got %#v", field, schema.Properties.PlanArtifact.Required)
		}
	}

	if schema.Properties.PlanArtifact.Properties.Edits.MinItems != 1 {
		t.Fatalf("plan_artifact should require nonempty edits, got %#v", schema.Properties.PlanArtifact)
	}
}

func TestSubmitPlanEmptyEditsReturnsTypedCode(t *testing.T) {
	mgr, _ := newSubmitPlanTestManager(t)
	result := mgr.submitPlan(ToolRequest{
		Tool:      "submit_plan",
		Rationale: "empty plan should be rejected",
	})

	if result.Code != SubmitPlanEmptyEditsCode {
		t.Fatalf("empty submit_plan Code = %q, want %q; result=%#v", result.Code, SubmitPlanEmptyEditsCode, result)
	}

	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("empty submit_plan should return an actionable error: %#v", result)
	}
}

func TestSubmitPlanEmptySourceSatisfiedUsesTerminalRequiredCode(t *testing.T) {
	mgr, _ := newSubmitPlanTestManager(t)
	result := mgr.submitPlan(ToolRequest{
		Tool:     "submit_plan",
		Decision: "source-satisfied",
	})

	if result.Code != SubmitPlanSourceSatisfiedTerminalRequiredCode {
		t.Fatalf("source-satisfied empty submit_plan Code = %q, want %q; result=%#v", result.Code, SubmitPlanSourceSatisfiedTerminalRequiredCode, result)
	}

	if !strings.Contains(result.Error, "Decision: source-satisfied") {
		t.Fatalf("source-satisfied empty submit_plan should point to terminal answer shape: %#v", result)
	}
}

func TestSubmitPlanEmptySourceSatisfiedRationaleUsesTerminalRequiredCode(t *testing.T) {
	mgr, _ := newSubmitPlanTestManager(t)
	result := mgr.submitPlan(ToolRequest{
		Tool:      "submit_plan",
		Rationale: "The current source already contains the requested final state, so no changes are needed.",
	})

	if result.Code != SubmitPlanSourceSatisfiedTerminalRequiredCode {
		t.Fatalf("source-satisfied rationale Code = %q, want %q; result=%#v", result.Code, SubmitPlanSourceSatisfiedTerminalRequiredCode, result)
	}
}

func TestSubmitPlanRejectsUnsupportedProofClaimsAtIntake(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "tests/panel_contract.gd", "func test_contract():\n\tpass\n")

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
	if !strings.Contains(result.Error, "unsupported proof claim") ||
		!strings.Contains(result.Error, "self-disqualifying") ||
		!strings.Contains(result.Error, "tests/panel_contract.gd") ||
		!strings.Contains(result.Error, "simplified") {
		t.Fatalf("submit_plan should reject unsupported proof claims before executor handoff, got %#v", result)
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
		t.Fatalf("rejected submit_plan should not persist last.plan.json; stat err=%v", err)
	}
}

func TestSubmitPlanRejectsSetupOnlyTestEditsAtIntake(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "native/tests/test_fluid_brewery_cascade.cpp", "TEST_CASE(\"FET-3\") {\n    CHECK(true);\n}\n")

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
	if !strings.Contains(result.Error, "unsupported proof claim") || !strings.Contains(result.Error, "verify precondition") {
		t.Fatalf("submit_plan should reject setup-only/precondition-only test edits, got %#v", result)
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
		t.Fatalf("rejected submit_plan should not persist last.plan.json; stat err=%v", err)
	}
}

func TestSubmitPlanUnsupportedProofClaimsIgnoreProseFiles(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "docs/review.md", "before\n")

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
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("submit_plan should not apply source proof heuristics to prose files: %s", result.Error)
	}
}

func TestPlanEditUnsupportedProofClaimReasonPrefersContentOverNewText(t *testing.T) {
	reason := PlanEditUnsupportedProofClaimReason(planartifact.PlanEdit{
		Path:    "internal/sample_test.go",
		Content: "package sample\n\nfunc TestRealBehavior(t *testing.T) {\n\tif got := behavior(); got != 42 {\n\t\tt.Fatalf(\"got %d\", got)\n\t}\n}\n",
		NewText: "// for now this test is only a placeholder\n",
	})

	if reason != "" {
		t.Fatalf("PlanEditUnsupportedProofClaimReason should inspect Content before NewText, got %q", reason)
	}
}

func TestSubmitPlanRejectsSourceLineRangeExpansionWithoutSelectedAnchor(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "native/src/core/colony_sim.h", strings.Join([]string{
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
	if result.Error == "" {
		t.Fatalf("expected unanchored source line-range expansion to be rejected")
	}

	for _, want := range []string{"expands the selected range", "does not preserve any selected anchor line", "old_text/new_text"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("expected submit_plan rejection to mention %q, got: %s", want, result.Error)
		}
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
		t.Fatalf("rejected submit_plan should not persist last.plan.json; stat err=%v", err)
	}
}

func TestSubmitPlanRejectsGoLineRangeExpansionWithoutSelectedAnchorAfterSyntaxPreflight(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "pkg/feature.go", strings.Join([]string{
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
	if result.Error == "" {
		t.Fatalf("expected syntactically valid unanchored Go expansion to be rejected")
	}

	for _, want := range []string{"expands the selected range", "does not preserve any selected anchor line", "old_text/new_text"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("expected submit_plan rejection to mention %q, got: %s", want, result.Error)
		}
	}

	if strings.Contains(result.Error, "preflight syntax validation failed") {
		t.Fatalf("test fixture should pass syntax preflight before anchor guard rejects it: %s", result.Error)
	}
}

func TestSubmitPlanRejectsContentOnlyDuplicateExistingSourceBlock(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "native/src/core/constants/environment.h", strings.Join([]string{
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
	if result.Error == "" {
		t.Fatalf("expected duplicate existing source block to be rejected")
	}

	for _, want := range []string{"duplicate an existing source block", "source_satisfied_paths", "old_text/new_text"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("expected submit_plan rejection to mention %q, got: %s", want, result.Error)
		}
	}
}

func TestSubmitPlanAllowsSourceLineRangeReplacementWithoutGrowth(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "native/src/sim.cpp", "int value() {\n    return 1;\n}\n")

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
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("ordinary same-size source line-range replacement should remain valid: %s", result.Error)
	}
}

func TestSubmitPlanRejectsLineRangeWholeExistingSourceRewrite(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	path := "native/src/simulation/world/collapse_event.h"
	writeSubmitPlanFixture(t, root, path, strings.Join([]string{
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
	if strings.TrimSpace(result.Error) == "" {
		t.Fatal("line-range rewrite of an entire existing source file should be rejected")
	}

	for _, want := range []string{"line-range replacement", "entire existing source file", "bounded old_text/new_text"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("expected submit_plan rejection to mention %q, got: %s", want, result.Error)
		}
	}
}

func TestSubmitPlanRejectsLargeSourceLineRangeCollapse(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	lines := make([]string, 0, 90)
	lines = append(lines, "#include \"core/colony_sim.h\"")
	for i := 0; i < 80; i++ {
		lines = append(lines, fmt.Sprintf("static int helper_%02d() { return %d; }", i, i))
	}
	lines = append(lines, "int keep() { return helper_00(); }", "")
	writeSubmitPlanFixture(t, root, "native/src/core/colony_sim_tick.cpp", strings.Join(lines, "\n"))

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
	if strings.TrimSpace(result.Error) == "" {
		t.Fatal("large source line-range collapse should be rejected")
	}

	for _, want := range []string{"would collapse", "non-empty source lines", "smaller bounded anchors"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("expected submit_plan rejection to mention %q, got: %s", want, result.Error)
		}
	}
}

func TestSubmitPlanRejectsMidFilePreprocessorLineRangeDrift(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "native/src/core/colony_sim_tick.cpp", strings.Join([]string{
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
	if strings.TrimSpace(result.Error) == "" {
		t.Fatal("mid-file preprocessor drift should be rejected")
	}

	for _, want := range []string{"file-scope preprocessor directive", "existing preprocessor context", "<<QUESTION>>"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("expected submit_plan rejection to mention %q, got: %s", want, result.Error)
		}
	}
}

func TestSubmitPlanRejectsAnchorPreservingMidFilePreprocessorLineRangeDrift(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "native/src/core/colony_sim_tick.cpp", strings.Join([]string{
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
	if strings.TrimSpace(result.Error) == "" {
		t.Fatal("anchor-preserving mid-file preprocessor drift should be rejected")
	}

	if !strings.Contains(result.Error, "file-scope preprocessor directive") {
		t.Fatalf("expected file-scope preprocessor rejection, got: %s", result.Error)
	}
}

func TestSubmitPlanAllowsReplacingExistingConditionalPreprocessorContext(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "native/src/core/feature.h", strings.Join([]string{
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
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("existing conditional preprocessor context should be allowed: %s", result.Error)
	}
}

func TestSubmitPlanAllowsIntroducingInBodyConditionalCompilation(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "native/src/core/feature.cpp", strings.Join([]string{
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
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("in-body conditional compilation should be allowed: %s", result.Error)
	}
}

func TestSubmitPlanAllowsStartLineOneInBodyConditionalCompilation(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "native/src/core/feature.cpp", strings.Join([]string{
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
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("start-line-one in-body conditional compilation should be allowed: %s", result.Error)
	}
}

func TestSubmitPlanAllowsIntroducingInBodyPragma(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "native/src/core/feature.cpp", strings.Join([]string{
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
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("in-body pragma should be allowed: %s", result.Error)
	}
}

func TestSubmitPlanRejectsHeaderGuardWithoutSelectingExistingPrologue(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "native/src/simulation/world/collapse_event.h", strings.Join([]string{
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
	if strings.TrimSpace(result.Error) == "" {
		t.Fatal("header guard replacement without selecting existing prologue should be rejected")
	}

	for _, want := range []string{"header guard", "existing file prologue", "current guard/include line"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("expected submit_plan rejection to mention %q, got: %s", want, result.Error)
		}
	}
}

func TestSubmitPlanRejectsHeaderGuardAfterLeadingBlockComment(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "native/src/simulation/world/collapse_event.h", strings.Join([]string{
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
	if strings.TrimSpace(result.Error) == "" {
		t.Fatal("header guard after leading block comment should be rejected")
	}
	if !strings.Contains(result.Error, "header guard") {
		t.Fatalf("expected header guard rejection, got: %s", result.Error)
	}
}

func TestSubmitPlanAllowsTopOfFileIncludeEditSelectingIncludeContext(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "native/src/core/colony_sim_tick.cpp", strings.Join([]string{
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
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("top-of-file include edit selecting include context should be allowed: %s", result.Error)
	}
}

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

func assertSubmitPlanNoSensitiveMarkers(t *testing.T, root string, result ToolResult, forbidden []string) planartifact.PlanArtifact {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
	if err != nil {
		t.Fatalf("read last.plan.json: %v", err)
	}

	combined := strings.ToLower(result.Output + "\n" + result.Error + "\n" + string(body))
	for _, marker := range forbidden {
		if strings.Contains(combined, strings.ToLower(marker)) {
			t.Fatalf("submit_plan leaked sensitive marker %q in output/error/artifact:\noutput=%s\nerror=%s\nartifact=%s", marker, result.Output, result.Error, string(body))
		}
	}

	var artifact planartifact.PlanArtifact

	if err := json.Unmarshal(body, &artifact); err != nil {
		t.Fatalf("last.plan.json is not valid plan JSON: %v", err)
	}

	return artifact
}

func assertSubmitPlanPlansTreeDoesNotContain(t *testing.T, root string, forbidden []string) {
	t.Helper()

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
			if strings.Contains(lower, strings.ToLower(marker)) {
				t.Fatalf("plan artifact tree leaked sensitive marker %q in %s", marker, path)
			}
		}

		return nil
	})

	if os.IsNotExist(err) {
		return
	}

	if err != nil {
		t.Fatalf("scan plan artifact tree: %v", err)
	}
}

func TestSubmitPlanWritesArtifactWithSchemaVersion(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "a.txt", "before\n")
	writeSubmitPlanFixture(t, root, "b.txt", "original\n")

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:      "submit_plan",
		Rationale: "add feature across the codebase",
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "before", NewText: "after", Purpose: "first edit"},
			{Path: "b.txt", StartLine: 1, EndLine: 1, Content: "rewritten\n", Purpose: "second edit"},
		},
	})
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("submit_plan rejected a valid plan: %s", result.Error)
	}

	if !strings.Contains(result.Output, "plan_path:") {
		t.Fatalf("submit_plan output should name the plan path, got %q", result.Output)
	}

	planDir := filepath.Join(root, ".idiom", "plans")

	entries, err := os.ReadDir(planDir)
	if err != nil {
		t.Fatalf("plan dir missing: %v", err)
	}

	var stamped string

	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".plan.json") && entry.Name() != "last.plan.json" {
			stamped = filepath.Join(planDir, entry.Name())
		}
	}

	if stamped == "" {
		t.Fatalf("no timestamped plan artifact written under %s", planDir)
	}

	body, err := os.ReadFile(stamped)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}

	var artifact planartifact.PlanArtifact

	if err := json.Unmarshal(body, &artifact); err != nil {
		t.Fatalf("artifact JSON invalid: %v", err)
	}

	if artifact.SchemaVersion != planartifact.SchemaVersion {
		t.Fatalf("schema_version mismatch: got %q, want %q", artifact.SchemaVersion, planartifact.SchemaVersion)
	}

	if len(artifact.Edits) != 2 {
		t.Fatalf("expected 2 edits, got %d", len(artifact.Edits))
	}

	provenance := artifact.Edits[0].AnchorProvenance
	if provenance == nil {
		t.Fatalf("substring edit should persist harness-owned anchor provenance")
	}

	if provenance.SchemaVersion != PlanEditAnchorProvenanceSchemaVersion || provenance.Source != "submit_plan" {
		t.Fatalf("unexpected anchor provenance identity: %#v", provenance)
	}

	if provenance.Path != "a.txt" || provenance.MatchCount != 1 || provenance.StartLine != 1 || provenance.EndLine != 1 {
		t.Fatalf("unexpected anchor provenance location: %#v", provenance)
	}

	if !strings.HasPrefix(provenance.FileSHA256, "sha256:") || !strings.HasPrefix(provenance.AnchorSHA256, "sha256:") {
		t.Fatalf("anchor provenance should include file and old_text digests: %#v", provenance)
	}

	if artifact.Edits[1].AnchorProvenance == nil {
		t.Fatalf("line-range edit should persist harness-owned anchor provenance")
	}

	if artifact.Rationale == "" {
		t.Fatalf("rationale should be persisted")
	}

	if artifact.Task.Summary == "" {
		t.Fatalf("durable artifact should include task summary")
	}

	if artifact.Contract.Kind != "plan_then_execute" || artifact.Contract.ContractOwner != "harness" || artifact.Contract.StrategyOwner != "model" {
		t.Fatalf("durable artifact contract not populated: %#v", artifact.Contract)
	}

	if artifact.Packet.Status == "" {
		t.Fatalf("durable artifact packet status should be populated")
	}

	if !submitPlanTestContains(artifact.Paths.Editable, "a.txt") || !submitPlanTestContains(artifact.Paths.Changed, "b.txt") {
		t.Fatalf("durable artifact paths should include planned edit paths: %#v", artifact.Paths)
	}

	if len(artifact.Evidence) == 0 {
		t.Fatalf("durable artifact should include bounded evidence")
	}

	if len(artifact.VerificationObligations) == 0 || artifact.VerificationObligations[0].Status == "" {
		t.Fatalf("durable artifact should include verification obligations: %#v", artifact.VerificationObligations)
	}

	if artifact.Approval.Status == "" {
		t.Fatalf("durable artifact should include approval metadata")
	}

	if artifact.Approval.ScopeID == "" || artifact.Approval.ExecutionPermission == "" {
		t.Fatalf("durable artifact should include approval scope and execution metadata: %#v", artifact.Approval)
	}

	if artifact.Handoff.ID == "" || artifact.Handoff.Status == "" || !artifact.Handoff.Ready {
		t.Fatalf("durable artifact should include ready handoff fields: %#v", artifact.Handoff)
	}

	pointer, err := os.ReadFile(filepath.Join(planDir, "last.plan.json"))
	if err != nil {
		t.Fatalf("last.plan.json missing: %v", err)
	}

	if !json.Valid(pointer) {
		t.Fatalf("last.plan.json is not valid JSON")
	}
}

func TestSubmitPlanRejectsSymlinkedPlanArtifactDir(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "a.txt", "before\n")

	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir .idiom: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".idiom", "plans")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
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

	if strings.TrimSpace(result.Error) == "" || !strings.Contains(result.Error, "symlink") {
		t.Fatalf("submit_plan should reject symlinked plan dir, got result=%#v", result)
	}
}

func TestValidatePlanEditAnchorProvenanceRejectsStaleFileState(t *testing.T) {
	root := t.TempDir()
	writeSubmitPlanFixture(t, root, "a.txt", "before\n")

	edits, err := AttachPlanEditAnchorProvenance(root, []planartifact.PlanEdit{
		{Path: "a.txt", OldText: "before", NewText: "after"},
	})
	if err != nil {
		t.Fatalf("attach anchor provenance: %v", err)
	}

	writeSubmitPlanFixture(t, root, "a.txt", "prefix\nbefore\n")

	err = ValidatePlanEditAnchorProvenance(root, edits[0])
	if err == nil {
		t.Fatalf("expected stale anchor provenance rejection")
	}

	if !strings.Contains(err.Error(), "anchor provenance stale") || !strings.Contains(err.Error(), "file fingerprint changed") {
		t.Fatalf("expected stale file fingerprint error, got %v", err)
	}
}

func TestValidatePlanEditAnchorProvenanceCoversRangeAndInsertAnchors(t *testing.T) {
	root := t.TempDir()
	writeSubmitPlanFixture(t, root, "a.txt", "one\ntwo\nthree\n")

	edits, err := AttachPlanEditAnchorProvenance(root, []planartifact.PlanEdit{
		{Path: "a.txt", StartLine: 2, EndLine: 2, Content: "TWO\n"},
		{Path: "a.txt", InsertAfterLine: 3, Content: "four\n"},
	})
	if err != nil {
		t.Fatalf("attach anchor provenance: %v", err)
	}

	if edits[0].AnchorProvenance == nil || edits[1].AnchorProvenance == nil {
		t.Fatalf("range and insert edits should carry provenance: %#v", edits)
	}

	writeSubmitPlanFixture(t, root, "a.txt", "one\nTWO\nthree\n")

	if err := ValidatePlanEditAnchorProvenance(root, edits[0]); err == nil {
		t.Fatalf("expected stale range provenance rejection")
	}

	writeSubmitPlanFixture(t, root, "a.txt", "one\ntwo\nTHREE\n")

	if err := ValidatePlanEditAnchorProvenance(root, edits[1]); err == nil {
		t.Fatalf("expected stale insert provenance rejection")
	}
}

func TestSubmitPlanOverwritesModelSuppliedAnchorProvenance(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "a.txt", "before\n")

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
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("submit_plan should ignore model-supplied anchor provenance and attach its own: %s", result.Error)
	}

	plan, err := LoadPlanArtifact(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
	if err != nil {
		t.Fatalf("load plan: %v", err)
	}

	provenance := plan.Edits[0].AnchorProvenance
	if provenance == nil {
		t.Fatalf("expected harness-owned provenance")
	}

	if provenance.SchemaVersion != PlanEditAnchorProvenanceSchemaVersion || provenance.Source != "submit_plan" || provenance.Path != "a.txt" {
		t.Fatalf("expected harness-owned provenance to replace model input, got %#v", provenance)
	}

	if err := ValidatePlanEditAnchorProvenance(root, plan.Edits[0]); err != nil {
		t.Fatalf("harness-owned provenance should validate: %v", err)
	}
}

func TestPlanEditAnchorProvenanceShapeRejectsMalformedDigest(t *testing.T) {
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
	if err == nil || !strings.Contains(err.Error(), "file_sha256") {
		t.Fatalf("expected malformed file digest rejection, got %v", err)
	}
}

func TestSubmitPlanRepairsUniqueLeadingWhitespaceAnchor(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
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
	writeSubmitPlanFixture(t, root, "status.go", current)

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

	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("submit_plan should repair unique indentation-only anchor mismatch, got: %s", result.Error)
	}

	plan, err := LoadPlanArtifact(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
	if err != nil {
		t.Fatalf("load plan: %v", err)
	}

	if len(plan.Edits) != 1 {
		t.Fatalf("expected one edit, got %d", len(plan.Edits))
	}

	if !strings.HasPrefix(plan.Edits[0].OldText, "\tif strings.TrimSpace") {
		t.Fatalf("expected persisted old_text to preserve source indentation, got %q", plan.Edits[0].OldText)
	}
}

func TestSubmitPlanRejectsLineRangeEditThatBreaksGoSyntax(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "pkg/feature_test.go", strings.Join([]string{
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
	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("expected submit_plan to reject syntax-breaking line range edit")
	}

	for _, want := range []string{"preflight syntax validation failed", "pkg/feature_test.go", "declared start_line=7 and end_line=7", "extend end_line", "insert_after_line"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("submit_plan error missing %q:\n%s", want, result.Error)
		}
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
		t.Fatalf("syntax-rejected submit_plan should not write last.plan.json, stat err=%v", err)
	}
}

func TestSubmitPlanRejectsSequentialLineRangeEditsThatBreakGoSyntax(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "pkg/feature.go", strings.Join([]string{
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
	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("expected submit_plan to reject sequential edits whose projected final file is invalid")
	}

	for _, want := range []string{"edits[1]", "after applying prior plan edits", "preflight syntax validation failed", "pkg/feature.go"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("submit_plan error missing %q:\n%s", want, result.Error)
		}
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
		t.Fatalf("sequence-rejected submit_plan should not write last.plan.json, stat err=%v", err)
	}
}

func TestSubmitPlanAcceptsStructuredDurablePlanArtifact(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "a.txt", "before\n")
	writeSubmitPlanContextView(t, root, "feedfacecafebeef", "a.txt")

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
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("submit_plan rejected structured durable artifact: %s", result.Error)
	}

	plan, err := LoadPlanArtifact(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
	if err != nil {
		t.Fatalf("load structured plan: %v", err)
	}

	if plan.PlanID != "plan-structured" || plan.Task.ID != "task-structured" {
		t.Fatalf("structured identity fields not persisted: %#v", plan)
	}

	if plan.PackID != "feedfacecafebeef" || plan.Packet.PackID != "feedfacecafebeef" {
		t.Fatalf("pack_id mirror not preserved: top=%q packet=%q", plan.PackID, plan.Packet.PackID)
	}

	if len(plan.UnresolvedQuestions) != 1 || len(plan.VerificationObligations) != 1 {
		t.Fatalf("structured question/verification fields not preserved: %#v %#v", plan.UnresolvedQuestions, plan.VerificationObligations)
	}

	if !strings.Contains(result.Output, "audit_ok=true") {
		t.Fatalf("accepted structured artifact should report passing audit, got %q", result.Output)
	}
}

func TestSubmitPlanRejectsSourceSatisfiedOutsideOriginalRequiredScope(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "a.txt", "before\n")

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
	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("source_satisfied_paths outside original required scope should be rejected")
	}

	if !strings.Contains(result.Error, "scope_path_mismatch") {
		t.Fatalf("expected source_satisfied_paths scope rejection, got %q", result.Error)
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
		t.Fatalf("rejected submit_plan should not persist last.plan.json; stat err=%v", err)
	}
}

func TestSubmitPlanBlocksReadyHandoffWhenRequiredApprovalPending(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "a.txt", "before\n")
	writeSubmitPlanContextView(t, root, "feedfacecafebeef", "a.txt")

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
	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("submit_plan should block handoff-ready artifact with pending required approval")
	}

	for _, want := range []string{"plan_artifact.schema_errors", "handoff.status:not_executable"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("approval handoff rejection should include %q, got %q", want, result.Error)
		}
	}

	if result.Output != "" {
		t.Fatalf("schema rejection should happen before audit output, got %q", result.Output)
	}

	if strings.Contains(result.Summary+result.Output+result.Error, "executor phase will dispatch") {
		t.Fatalf("blocked artifact should not claim executor dispatch will proceed: %#v", result)
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
		t.Fatalf("blocked artifact should not persist last.plan.json, stat err=%v", err)
	}
}

func TestSubmitPlanBlocksPendingApprovalBeforeSpillingLargeEditBodies(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanContextView(t, root, "feedfacecafebeef", "large.txt")

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

	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("submit_plan should block handoff-ready large edit artifact with pending required approval")
	}

	if result.Output != "" || !strings.Contains(result.Error, "handoff.status:not_executable") {
		t.Fatalf("approval handoff rejection should expose bounded audit status, got output=%q error=%q", result.Output, result.Error)
	}

	combined := strings.ToLower(result.Output + "\n" + result.Error)
	for _, leaked := range []string{"p5_8_secret_body", "raw_prompt", "transcript", "customer acme"} {
		if strings.Contains(combined, leaked) {
			t.Fatalf("approval rejection leaked sensitive body marker %q: output=%q error=%q", leaked, result.Output, result.Error)
		}
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
		t.Fatalf("blocked large edit artifact should not persist last.plan.json, stat err=%v", err)
	}

	matches, err := filepath.Glob(filepath.Join(root, ".idiom", "plans", "assets", "*new_text.txt"))
	if err != nil {
		t.Fatalf("glob plan assets: %v", err)
	}

	if len(matches) != 0 {
		t.Fatalf("blocked large edit should not spill new_text assets, got %v", matches)
	}

	assertSubmitPlanPlansTreeDoesNotContain(t, root, []string{
		"P5_8_SECRET_BODY",
		"raw_prompt",
		"transcript",
		"customer acme",
	})
}

func TestSubmitPlanRejectsReadyHandoffWithNonReadyStatus(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "a.txt", "before\n")

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
	if result.Error == "" {
		t.Fatalf("submit_plan should reject ready handoff with non-ready status")
	}
	if !strings.Contains(result.Error, "handoff.ready:invalid_status") {
		t.Fatalf("expected handoff ready/status schema error, got %q", result.Error)
	}
	if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
		t.Fatalf("rejected artifact should not persist last.plan.json, stat err=%v", err)
	}
}

func TestPlanArtifactAuditKeepsPendingApprovalNonBlockingWhenHandoffNotReady(t *testing.T) {
	root := t.TempDir()
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

	if !result.OK {
		t.Fatalf("pending approval should remain report-only while handoff is not ready: %#v", result)
	}

	if result.Metrics.ApprovalHandoffMismatches != 1 {
		t.Fatalf("expected visible approval mismatch metric, got %#v", result.Metrics)
	}

	for _, finding := range result.Findings {
		if finding.Kind == "approval_handoff_mismatch" && finding.Severity == "error" {
			t.Fatalf("non-ready handoff should not promote approval mismatch to error: %#v", result.Findings)
		}
	}
}

func TestPlanArtifactSynthesizesStableApprovalScopeAndHandoffIDs(t *testing.T) {
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

	if first.Approval.ScopeID == "" || second.Approval.ScopeID == "" || first.Approval.ScopeID != second.Approval.ScopeID {
		t.Fatalf("approval scope id should be deterministic, first=%q second=%q", first.Approval.ScopeID, second.Approval.ScopeID)
	}

	if first.Handoff.ID == "" || second.Handoff.ID == "" || first.Handoff.ID != second.Handoff.ID {
		t.Fatalf("handoff id should be deterministic, first=%q second=%q", first.Handoff.ID, second.Handoff.ID)
	}

	if first.Approval.ExecutionPermission != "execute_all" {
		t.Fatalf("approved scope should default to execute_all, got %#v", first.Approval)
	}

	if len(first.Approval.ApprovedPaths) != 1 || first.Approval.ApprovedPaths[0] != "a.txt" {
		t.Fatalf("approved required scope should be explicit, got %#v", first.Approval.ApprovedPaths)
	}
}

func TestPlanArtifactPartialApprovalSemanticsAreClientClassAgnostic(t *testing.T) {
	for _, class := range []string{
		string(planartifact.ClientClassificationFinance),
		string(planartifact.ClientClassificationHealthcareLifeSciences),
		string(planartifact.ClientClassificationPublicSector),
		string(planartifact.ClientClassificationEnterpriseSaaS),
		string(planartifact.ClientClassificationToolingVendor),
		string(planartifact.ClientClassificationLocalModelPrivateCode),
		string(planartifact.ClientClassificationOpenSource),
	} {
		result := AuditPlanArtifact(t.TempDir(), planartifact.PlanArtifact{
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
		if !result.OK {
			t.Fatalf("partial approval should allow approved scope for class %q: %#v", class, result)
		}

		if result.Metrics.ApprovalScopeMismatches != 0 {
			t.Fatalf("partial approval should not report scope drift for class %q: %#v", class, result.Metrics)
		}
	}
}

func TestPlanArtifactPartialApprovalBlocksPendingScopePrivately(t *testing.T) {
	result := AuditPlanArtifact(t.TempDir(), planartifact.PlanArtifact{
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
	if result.OK {
		t.Fatalf("partial approval should block pending edit scope")
	}

	if result.Metrics.ApprovalScopeMismatches == 0 {
		t.Fatalf("expected approval scope mismatch, got %#v", result.Metrics)
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal audit result: %v", err)
	}

	for _, leaked := range []string{"raw_prompt", "customer-acme", "token=", "/home/example/private"} {
		if strings.Contains(string(encoded), leaked) {
			t.Fatalf("partial approval audit leaked sensitive marker %q: %s", leaked, encoded)
		}
	}
}

func TestPlanArtifactAuditRejectsPendingPartialApprovalSourceSatisfiedPath(t *testing.T) {
	result := AuditPlanArtifact(t.TempDir(), planartifact.PlanArtifact{
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
	if result.OK {
		t.Fatalf("pending partial approval source_satisfied_paths should fail audit")
	}

	if result.Metrics.ApprovalScopeMismatches == 0 {
		t.Fatalf("expected approval scope mismatch, got %#v", result.Metrics)
	}

	if !strings.Contains(result.ErrorSummary(), "approval_scope_mismatch") {
		t.Fatalf("expected approval scope error summary, got %#v", result)
	}
}

func TestPlanArtifactAuditReportsPacketStates(t *testing.T) {
	root := t.TempDir()
	writeSubmitPlanFixture(t, root, "a.txt", "alpha\n")
	writeSubmitPlanContextView(t, root, "feedfacecafebeef", "a.txt")
	writeSubmitPlanFixture(t, root, filepath.Join(".idiom", "comprehension", "views", "deadc0decafef00d.json"), `{
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
		t.Fatalf("expected inherited packet state, got %#v", result.Metrics)
	}

	stale := base

	stale.PackID = "deadc0decafef00d"
	stale.Packet = planartifact.PlanArtifactPacket{PackID: "deadc0decafef00d", Status: "available", Freshness: "fresh"}

	if result := AuditPlanArtifact(root, stale, PlanArtifactAuditOptions{}); result.Metrics.PacketState != "stale" || result.Metrics.PacketFresh {
		t.Fatalf("expected stale packet state, got %#v", result.Metrics)
	}

	missing := base

	missing.Packet = planartifact.PlanArtifactPacket{Status: "not_provided"}

	if result := AuditPlanArtifact(root, missing, PlanArtifactAuditOptions{}); result.Metrics.PacketState != "missing" || result.Metrics.PacketReason != "not_provided" {
		t.Fatalf("expected missing packet state, got %#v", result.Metrics)
	}

	overridden := inherited

	overridden.Packet.PackID = "deadc0decafef00d"

	if result := AuditPlanArtifact(root, overridden, PlanArtifactAuditOptions{}); result.Metrics.PacketState != "overridden" || result.Metrics.PacketReason != "packet_id_mirror" {
		t.Fatalf("expected overridden packet state, got %#v", result.Metrics)
	}
}

func TestSubmitPlanSanitizesStructuredEditPurposeMetadata(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "a.txt", "before\n")
	writeSubmitPlanContextView(t, root, "feedfacecafebeef", "a.txt")

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
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("submit_plan rejected structured artifact with sensitive metadata: %s", result.Error)
	}

	artifact := assertSubmitPlanNoSensitiveMarkers(t, root, result, []string{
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
	if len(artifact.Edits) != 1 {
		t.Fatalf("expected one edit, got %d", len(artifact.Edits))
	}

	edit := artifact.Edits[0]
	if !strings.HasPrefix(edit.Purpose, "redacted:") {
		t.Fatalf("sensitive purpose should persist as bounded redaction, got %q", edit.Purpose)
	}

	if edit.Path != "a.txt" || edit.OldText != "before" || edit.NewText != "after" {
		t.Fatalf("executor-required edit contract changed: %#v", edit)
	}
}

func TestSubmitPlanSanitizesLegacyEditPurposeMetadata(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "a.txt", "before\n")

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
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("submit_plan rejected legacy artifact with sensitive metadata: %s", result.Error)
	}

	artifact := assertSubmitPlanNoSensitiveMarkers(t, root, result, []string{
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
	if len(artifact.Edits) != 1 {
		t.Fatalf("expected one edit, got %d", len(artifact.Edits))
	}

	edit := artifact.Edits[0]
	if !strings.HasPrefix(edit.Purpose, "redacted:") {
		t.Fatalf("sensitive legacy purpose should persist as bounded redaction, got %q", edit.Purpose)
	}

	if edit.Path != "a.txt" || edit.OldText != "before" || edit.NewText != "after" {
		t.Fatalf("executor-required edit contract changed: %#v", edit)
	}
}

func TestSubmitPlanStructuredAuditRejectsPacketAndScopeMismatchPrivately(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "a.txt", "before\n")
	writeSubmitPlanFixture(t, root, "b.txt", "before\n")
	writeSubmitPlanContextView(t, root, "feedfacecafebeef", "a.txt")

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
	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("structured plan with packet/scope mismatch should be rejected")
	}

	for _, want := range []string{"plan_artifact.audit_errors", "scope_path_mismatch", "unresolved_question_mismatch"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("audit rejection should include %q, got %q", want, result.Error)
		}
	}

	for _, leaked := range []string{"customer-acme", "customer_acme", "raw_prompt", "transcript", "logs/telemetry/secrets"} {
		if strings.Contains(result.Error, leaked) {
			t.Fatalf("audit rejection leaked sensitive marker %q: %s", leaked, result.Error)
		}
	}
}

func TestSubmitPlanLegacyPacketMismatchIsReportOnly(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "a.txt", "before\n")

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
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("legacy submit_plan should keep packet audit report-only: %s", result.Error)
	}

	if !strings.Contains(result.Output, "packet_identity_mismatches=1") || !strings.Contains(result.Output, "audit_ok=true") {
		t.Fatalf("legacy audit mismatch should be visible but non-blocking, got %q", result.Output)
	}
}

func TestSubmitPlanStaleOldTextReportsSourceSatisfiedHintWhenReplacementPresent(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "a.txt", "after\n")

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
	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("stale old_text should be rejected")
	}

	for _, want := range []string{"replacement text already appears", "Decision: source-satisfied"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("stale old_text error should include %q, got %q", want, result.Error)
		}
	}
}

func TestSubmitPlanStructuredSchemaErrorsAreBoundedAndPrivate(t *testing.T) {
	mgr, _ := newSubmitPlanTestManager(t)
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

	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("malformed structured artifact should be rejected")
	}

	for _, want := range []string{"plan_artifact.schema_errors", "contract.kind", "verification_obligations", "unsupported_client_classification"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("schema error should include %q, got %q", want, result.Error)
		}
	}

	for _, leaked := range []string{"SECRET", "raw_prompt", "customer-acme", "customer_acme", "transcript"} {
		if strings.Contains(result.Error, leaked) {
			t.Fatalf("schema error leaked sensitive text %q: %s", leaked, result.Error)
		}
	}
}

func TestSubmitPlanRejectsUnsupportedVerificationOwnerAndStatus(t *testing.T) {
	mgr, _ := newSubmitPlanTestManager(t)
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

	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("unsupported verification vocabulary should be rejected")
	}
	for _, want := range []string{"verification_obligations[0].owner", "verification_obligations[0].status"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("schema error should include %q, got %q", want, result.Error)
		}
	}
}

func TestPlanArtifactInvalidEditShapeErrorKeepsFullGuidance(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected invalid edit shape error")
	}

	message := err.Error()
	if strings.Contains(message, "[truncated]") {
		t.Fatalf("invalid edit shape guidance should not be truncated, got %q", message)
	}

	if !strings.Contains(message, "replace_all=true with new_text and empty old_text") {
		t.Fatalf("invalid edit shape guidance missing whole-file shape, got %q", message)
	}

	for _, want := range []string{"source_satisfied_paths", "remove it from edits[]"} {
		if !strings.Contains(message, want) {
			t.Fatalf("invalid edit shape guidance missing source-satisfied coverage hint %q, got %q", want, message)
		}
	}
}

func TestSubmitPlanInvalidEditShapePointsSourceSatisfiedPathToTopLevelCoverage(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "x.txt", "already final\n")

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:      "submit_plan",
		Rationale: "x.txt already contains the requested final state; y.txt still needs an edit.",
		Edits: []planartifact.PlanEdit{
			{Path: "x.txt", Purpose: "already source satisfied"},
			{Path: "y.txt", OldText: "before", NewText: "after", Purpose: "real edit"},
		},
	})
	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("invalid source-satisfied edit object should be rejected")
	}

	for _, want := range []string{"source_satisfied_paths", "remove it from edits[]"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("submit_plan invalid shape should steer source-satisfied paths to top-level coverage %q, got %q", want, result.Error)
		}
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
		t.Fatalf("submit_plan should reject before writing an artifact; stat err=%v", err)
	}
}

func TestLoadPlanArtifactAcceptsLegacyV1AndSynthesizesDurableFields(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "legacy.plan.json")
	body := []byte(`{"schema_version":"idiom.plan.v1","rationale":"legacy prose rationale","edits":[{"path":"a.txt","old_text":"a","new_text":"b"}]}`)

	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("seed legacy artifact: %v", err)
	}

	plan, err := LoadPlanArtifact(path)
	if err != nil {
		t.Fatalf("legacy v1 artifact should load: %v", err)
	}

	if plan.SchemaVersion != planartifact.LegacySchemaVersion {
		t.Fatalf("legacy schema version should be preserved for attribution, got %q", plan.SchemaVersion)
	}

	if plan.Contract.Kind == "" || len(plan.Evidence) == 0 || len(plan.VerificationObligations) == 0 || plan.Handoff.Status == "" {
		t.Fatalf("legacy artifact should synthesize durable fields: %#v", plan)
	}
}

func TestLoadPlanArtifactRejectsNoOpLegacyEdit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "noop.plan.json")
	body := []byte(`{"schema_version":"idiom.plan.v1","rationale":"legacy prose rationale","edits":[{"path":"a.txt","old_text":"a","new_text":"a"}]}`)

	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("seed legacy artifact: %v", err)
	}

	_, err := LoadPlanArtifact(path)
	if err == nil {
		t.Fatalf("expected no-op legacy artifact to be rejected")
	}

	if !strings.Contains(err.Error(), "old_text and new_text are identical") {
		t.Fatalf("expected no-op validation error, got %v", err)
	}
}

func TestValidatePlanArtifactRejectsBackslashTraversalPath(t *testing.T) {
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
	if err == nil || !strings.Contains(err.Error(), "edits[0].path") {
		t.Fatalf("expected invalid edit path error, got %v", err)
	}
}

func TestPlanArtifactClientClassificationExamplesCoverApprovedClasses(t *testing.T) {
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
		if err := planartifact.ValidatePlanArtifact(artifact); err != nil {
			t.Fatalf("approved client classification %q should validate: %v", class, err)
		}
	}
}

func TestSubmitPlanRejectsEmptyEditList(t *testing.T) {
	mgr, _ := newSubmitPlanTestManager(t)
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:      "submit_plan",
		Rationale: "empty plan should be rejected",
	})

	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("empty edits should be rejected")
	}

	if !strings.Contains(result.Error, "at least one edit") {
		t.Fatalf("rejection should call out missing edits, got %q", result.Error)
	}
}

func TestSubmitPlanRejectsSourceSatisfiedPlaceholderRationale(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "a.txt", "before\n")

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
	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("source-satisfied placeholder rationale should be rejected")
	}

	if result.Code != SubmitPlanSourceSatisfiedTerminalRequiredCode {
		t.Fatalf("source-satisfied placeholder rationale code = %q, want %q", result.Code, SubmitPlanSourceSatisfiedTerminalRequiredCode)
	}

	for _, want := range []string{"source-satisfied", "placeholder"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("rejection should point to %q, got %q", want, result.Error)
		}
	}
}

func TestSubmitPlanAllowsNegatedSourceSatisfiedRationale(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "a.txt", "before\n")

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
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("negated source-satisfied rationale should be allowed, got %q", result.Error)
	}
}

func TestSubmitPlanRejectsMixedSourceSatisfiedCoverageWithoutEvidence(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "a.txt", "before\n")

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
	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("mixed source_satisfied_paths without current-state evidence should be rejected")
	}

	if !strings.Contains(result.Error, "source_satisfied_evidence_mismatch") {
		t.Fatalf("expected evidence mismatch rejection, got %q", result.Error)
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
		t.Fatalf("rejected submit_plan should not persist last.plan.json; stat err=%v", err)
	}
}

func TestSubmitPlanAllowsMixedSourceSatisfiedCoverageWithCurrentStateEvidence(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "a.txt", "before\n")

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
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("mixed source_satisfied_paths with current-state evidence should be allowed, got %q", result.Error)
	}

	plan, err := LoadPlanArtifact(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
	if err != nil {
		t.Fatalf("load submitted plan: %v", err)
	}

	if !submitPlanTestContains(plan.Paths.SourceSatisfied, "b.txt") || !submitPlanTestContains(plan.Paths.Required, "b.txt") {
		t.Fatalf("source_satisfied_paths should persist without synthesizing required scope: %#v", plan.Paths)
	}
}

func TestSubmitPlanAllowsScopedNoChangesRationale(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "a.txt", "before\n")

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
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("scoped no-changes rationale should be allowed, got %q", result.Error)
	}
}

func TestSubmitPlanRejectsVerificationOnlyEditPurposes(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "a.txt", "before\n")

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
	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("verification-only edit purpose should be rejected")
	}

	for _, want := range []string{"verification", "source-satisfied"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("rejection should point to %q, got %q", want, result.Error)
		}
	}
}

func TestSubmitPlanRejectsPlaceholderEditBodies(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "a.txt", "package p\n\nfunc run() {\n}\n")

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
		t.Run(tc.name, func(t *testing.T) {
			result := mgr.Execute(context.Background(), ToolRequest{
				Tool:      "submit_plan",
				Rationale: "Update executable source.",
				Edits:     []planartifact.PlanEdit{tc.edit},
			})
			if strings.TrimSpace(result.Error) == "" {
				t.Fatalf("placeholder edit body should be rejected")
			}

			for _, want := range []string{"placeholder edit body", tc.want, "<<QUESTION>>"} {
				if !strings.Contains(strings.ToLower(result.Error), strings.ToLower(want)) {
					t.Fatalf("rejection should include %q, got %q", want, result.Error)
				}
			}
		})
	}
}

func TestSubmitPlanAllowsTodoListInNonSourceGeneratedBody(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "docs/notes.md", "# Notes\n")

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
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("non-source TODO list should be allowed, got %q", result.Error)
	}
}

func TestSubmitPlanRejectsForNowPlaceholderProofText(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "a.cpp", "int dispatch() {\n    return 0;\n}\n")

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
	if strings.TrimSpace(result.Error) == "" {
		t.Fatal("for-now placeholder proof text should be rejected")
	}

	for _, want := range []string{"unsupported proof claim", "self-disqualifying", "for now"} {
		if !strings.Contains(strings.ToLower(result.Error), strings.ToLower(want)) {
			t.Fatalf("rejection should include %q, got %q", want, result.Error)
		}
	}
}

func TestSubmitPlanAllowsReplacingExistingTodoAnchorWithFinalCode(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "a.cpp", "int dispatch() {\n    // TODO: Implement actual dispatch logic.\n    return 0;\n}\n")

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
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("existing TODO anchor with final replacement should be allowed, got %q", result.Error)
	}
}

func TestSubmitPlanAllowsReplacingExistingBracketTodoAnchorWithFinalCode(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "a.cpp", "int dispatch() {\n    <TODO: fill implementation>\n}\n")

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
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("existing bracket TODO anchor with final replacement should be allowed, got %q", result.Error)
	}
}

func TestSubmitPlanRejectsWholeFileReplaceAllForExistingSourceFile(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	path := "native/src/simulation/world/collapse_event.h"
	writeSubmitPlanFixture(t, root, path, "#pragma once\n\nnamespace colony {\nstruct CollapseEvent {};\n}\n")

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
	if strings.TrimSpace(result.Error) == "" {
		t.Fatal("whole-file replace_all on existing source file should be rejected")
	}

	for _, want := range []string{"whole-file", "existing source file", "bounded anchored edit"} {
		if !strings.Contains(strings.ToLower(result.Error), strings.ToLower(want)) {
			t.Fatalf("rejection should include %q, got %q", want, result.Error)
		}
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
		t.Fatalf("rejected submit_plan should not persist last.plan.json; stat err=%v", err)
	}
}

func TestSubmitPlanRejectsEditWithoutShape(t *testing.T) {
	mgr, _ := newSubmitPlanTestManager(t)
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", Purpose: "missing both shapes"},
		},
	})

	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("malformed edit shape should be rejected")
	}

	if !strings.Contains(result.Error, "edits[0]") {
		t.Fatalf("rejection should index the bad edit, got %q", result.Error)
	}

	if !strings.Contains(result.Error, "replace_all=true") || !strings.Contains(result.Error, "missing/new-file") {
		t.Fatalf("rejection should name the missing/new-file shape, got %q", result.Error)
	}
}

func TestSubmitPlanRejectsJSONOldTextWithoutReplacementField(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "x.txt", "keep\n")

	req, err := DecodeToolRequestJSON([]byte(`{"tool":"submit_plan","edits":[{"path":"x.txt","old_text":"keep"}]}`))
	if err != nil {
		t.Fatalf("DecodeToolRequestJSON: %v", err)
	}

	result := mgr.Execute(context.Background(), req)
	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("old_text-only JSON edit should be rejected")
	}

	if !strings.Contains(result.Error, "new_text") {
		t.Fatalf("rejection should name the missing replacement field, got %q", result.Error)
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); err == nil || !os.IsNotExist(err) {
		t.Fatalf("submit_plan should reject before writing an artifact; stat err=%v", err)
	}
}

func TestLoadPlanArtifactAllowsLegacyDeletionWithoutNewTextField(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "legacy.plan.json")
	body := []byte(`{"schema_version":"idiom.plan.v2","created":"2026-05-20T00:00:00Z","plan_id":"legacy-delete","task":{"summary":"delete obsolete line"},"contract":{"kind":"implementation"},"packet":{"status":"not_used"},"paths":{"editable":["x.txt"]},"evidence":[{"kind":"source","path":"x.txt","summary":"current source"}],"verification_obligations":[{"command":"go test ./...","reason":"default"}],"approval":{"status":"approved","scope_id":"all","execution_permission":"execute_all"},"handoff":{"id":"handoff-1","status":"ready","ready":true},"edits":[{"path":"x.txt","old_text":"obsolete\n","purpose":"delete obsolete line"}]}`)

	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write legacy artifact: %v", err)
	}

	artifact, err := LoadPlanArtifact(path)
	if err != nil {
		t.Fatalf("legacy deletion artifact should load: %v", err)
	}

	if artifact.SchemaVersion != planartifact.PreviousSchemaVersion {
		t.Fatalf("loaded schema version = %q, want previous schema", artifact.SchemaVersion)
	}

	if len(artifact.Edits) != 1 || !planartifact.PlanEditHasExplicitReplacementText(artifact.Edits[0]) {
		t.Fatalf("legacy deletion edit should be normalized as explicit replacement: %#v", artifact.Edits)
	}
}

func TestLoadPlanArtifactAttachesMissingCurrentAnchorProvenance(t *testing.T) {
	root := t.TempDir()
	writeSubmitPlanFixture(t, root, "x.txt", "old\n")

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
	if err != nil {
		t.Fatalf("marshal artifact: %v", err)
	}

	planDir := filepath.Join(root, ".idiom", "plans")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatalf("mkdir plans: %v", err)
	}

	path := filepath.Join(planDir, "current.plan.json")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	loaded, err := LoadPlanArtifact(path)
	if err != nil {
		t.Fatalf("current-schema artifact should load with repaired provenance: %v", err)
	}

	if len(loaded.Edits) != 1 || loaded.Edits[0].AnchorProvenance == nil {
		t.Fatalf("loaded current artifact should have anchor provenance: %#v", loaded.Edits)
	}
}

func TestSubmitPlanAcceptsSubstringDeletion(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "x.txt", "keep\nremove me\nkeep\n")

	req, err := DecodeToolRequestJSON([]byte(`{"tool":"submit_plan","edits":[{"path":"x.txt","old_text":"remove me\n","new_text":"","purpose":"delete obsolete line"}]}`))
	if err != nil {
		t.Fatalf("DecodeToolRequestJSON: %v", err)
	}

	result := mgr.Execute(context.Background(), req)
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("substring deletion should be a valid plan edit: %s", result.Error)
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); err != nil {
		t.Fatalf("submit_plan should write an artifact for valid deletion edit: %v", err)
	}
}

func TestSubmitPlanRejectsNoOpSubstringEditBeforeWritingArtifact(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "x.txt", "keep\n")

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "x.txt", OldText: "keep\n", NewText: "keep\n", Purpose: "no-op repair"},
		},
	})
	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("no-op substring edit should be rejected")
	}

	for _, want := range []string{"old_text and new_text are identical", "source_satisfied_paths", "Decision: source-satisfied"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("no-op rejection should include %q, got %q", want, result.Error)
		}
	}

	for _, want := range []string{"remove no-op edit", "`source_satisfied_paths`: [\"x.txt\"]"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("no-op rejection should include path-specific repair %q, got %q", want, result.Error)
		}
	}

	if strings.Contains(result.Error, "Decision: pass") {
		t.Fatalf("planner no-op guidance must not leak review-repair pass wording, got %q", result.Error)
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
		t.Fatalf("submit_plan should reject before writing an artifact; stat err=%v", err)
	}
}

func TestSubmitPlanRepairsStringifiedMultilineEditsBeforeValidation(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "x.txt", "keep\nsecond\n")

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
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	req, err := DecodeToolRequestJSON(payload)
	if err != nil {
		t.Fatalf("DecodeToolRequestJSON should repair stringified edits: %v", err)
	}

	if len(req.Edits) != 1 || req.Edits[0].Path != "x.txt" {
		t.Fatalf("unexpected repaired edits: %#v", req.Edits)
	}

	result := mgr.Execute(context.Background(), req)
	if result.Error == "" {
		t.Fatalf("submit_plan should still reject the repaired no-op edit")
	}

	if !strings.Contains(result.Error, "old_text and new_text are identical") {
		t.Fatalf("no-op validation should run after repair, got %q", result.Error)
	}

	if _, statErr := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(statErr) {
		t.Fatalf("submit_plan should reject before writing an artifact; stat err=%v", statErr)
	}
}

func TestSubmitPlanRejectsNoOpRangeEditBeforeWritingArtifact(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "x.txt", "keep\n")

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "x.txt", StartLine: 1, EndLine: 1, Content: "keep\n", Purpose: "no-op repair"},
		},
	})
	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("no-op range edit should be rejected")
	}

	if !strings.Contains(result.Error, "edit would not change x.txt") {
		t.Fatalf("no-op range rejection should name unchanged edit, got %q", result.Error)
	}

	for _, want := range []string{"source_satisfied_paths", "Decision: source-satisfied"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("no-op range rejection should include %q, got %q", want, result.Error)
		}
	}

	for _, want := range []string{"remove no-op edit", "`source_satisfied_paths`: [\"x.txt\"]"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("no-op range rejection should include path-specific repair %q, got %q", want, result.Error)
		}
	}

	if strings.Contains(result.Error, "Decision: pass") {
		t.Fatalf("planner no-op guidance must not leak review-repair pass wording, got %q", result.Error)
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
		t.Fatalf("submit_plan should reject before writing an artifact; stat err=%v", err)
	}
}

func TestSubmitPlanAcceptsInsertAfterLineMiddleAndEnd(t *testing.T) {
	t.Run("middle", func(t *testing.T) {
		mgr, root := newSubmitPlanTestManager(t)
		writeSubmitPlanFixture(t, root, "x.txt", "one\ntwo\n")

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
		if strings.TrimSpace(result.Error) != "" {
			t.Fatalf("submit_plan rejected middle insertion: %s", result.Error)
		}

		plan, err := LoadPlanArtifact(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
		if err != nil {
			t.Fatalf("load middle insertion plan: %v", err)
		}

		updated, changed, err := PreviewPlanEditContent("one\ntwo\n", plan.Edits[0])
		if err != nil {
			t.Fatalf("preview middle insertion: %v", err)
		}

		if !changed || updated != "one\ninserted\ntwo\n" {
			t.Fatalf("middle insertion preview mismatch: changed=%v updated=%q", changed, updated)
		}
	})

	t.Run("end preserves boundary", func(t *testing.T) {
		mgr, root := newSubmitPlanTestManager(t)
		writeSubmitPlanFixture(t, root, "x.txt", "one\ntwo")

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
		if strings.TrimSpace(result.Error) != "" {
			t.Fatalf("submit_plan rejected end insertion: %s", result.Error)
		}

		plan, err := LoadPlanArtifact(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
		if err != nil {
			t.Fatalf("load end insertion plan: %v", err)
		}

		updated, changed, err := PreviewPlanEditContent("one\ntwo", plan.Edits[0])
		if err != nil {
			t.Fatalf("preview end insertion: %v", err)
		}

		if !changed || updated != "one\ntwo\nthree\n" {
			t.Fatalf("end insertion preview mismatch: changed=%v updated=%q", changed, updated)
		}
	})

	t.Run("preserves crlf", func(t *testing.T) {
		updated, changed, err := PreviewPlanEditContent("one\r\ntwo\r\n", planartifact.PlanEdit{
			Path:            "x.txt",
			InsertAfterLine: 1,
			Content:         "inserted",
		})
		if err != nil {
			t.Fatalf("preview crlf insertion: %v", err)
		}

		if !changed || updated != "one\r\ninserted\r\ntwo\r\n" {
			t.Fatalf("crlf insertion preview mismatch: changed=%v updated=%q", changed, updated)
		}
	})

	t.Run("newline terminated end appends newline", func(t *testing.T) {
		updated, changed, err := PreviewPlanEditContent("one\ntwo\n", planartifact.PlanEdit{
			Path:            "x.txt",
			InsertAfterLine: 2,
			Content:         "three",
		})
		if err != nil {
			t.Fatalf("preview newline-terminated insertion: %v", err)
		}

		if !changed || updated != "one\ntwo\nthree\n" {
			t.Fatalf("newline-terminated insertion preview mismatch: changed=%v updated=%q", changed, updated)
		}
	})
}

func TestSubmitPlanRejectsInvalidInsertAfterLineShapes(t *testing.T) {
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
		t.Run(tc.name, func(t *testing.T) {
			mgr, root := newSubmitPlanTestManager(t)
			if tc.seed != "" {
				writeSubmitPlanFixture(t, root, "x.txt", tc.seed)
			}

			result := mgr.Execute(context.Background(), ToolRequest{
				Tool:      "submit_plan",
				Rationale: "Try insertion.",
				Edits:     []planartifact.PlanEdit{tc.edit},
			})
			if strings.TrimSpace(result.Error) == "" {
				t.Fatalf("invalid insertion should be rejected")
			}

			for _, want := range tc.wants {
				if !strings.Contains(result.Error, want) {
					t.Fatalf("rejection missing %q: %q", want, result.Error)
				}
			}

			if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
				t.Fatalf("submit_plan should reject before writing an artifact; stat err=%v", err)
			}
		})
	}
}

func TestSubmitPlanRejectsNoOpWholeFileEditBeforeWritingArtifactOrAssets(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	large := strings.Repeat("same line\n", 600)
	writeSubmitPlanFixture(t, root, "large.txt", large)

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "large.txt", NewText: large, ReplaceAll: true, Purpose: "no-op whole-file repair"},
		},
	})
	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("no-op whole-file edit should be rejected")
	}

	for _, want := range []string{"edit would not change large.txt", "Decision: source-satisfied", "`source_satisfied_paths`: [\"large.txt\"]"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("no-op whole-file rejection should include %q, got %q", want, result.Error)
		}
	}

	if strings.Contains(result.Error, "Decision: pass") {
		t.Fatalf("planner no-op guidance must not leak review-repair pass wording, got %q", result.Error)
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
		t.Fatalf("submit_plan should reject before writing an artifact; stat err=%v", err)
	}

	matches, err := filepath.Glob(filepath.Join(root, ".idiom", "plans", "assets", "*"))
	if err != nil {
		t.Fatalf("glob spill assets: %v", err)
	}

	if len(matches) != 0 {
		t.Fatalf("submit_plan should reject before spilling edit assets, got %v", matches)
	}
}

func TestSubmitPlanAcceptsRefBackedEdits(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "whole.txt", "before\n")
	writeSubmitPlanFixture(t, root, "range.txt", "before\n")
	writeSubmitPlanBodyRef(t, root, "assets/whole-body.txt", "after whole\n")
	writeSubmitPlanBodyRef(t, root, "assets/range-body.txt", "after range\n")

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "whole.txt", NewTextRef: "assets/whole-body.txt", ReplaceAll: true, Purpose: "Replace whole file from a plan body ref."},
			{Path: "range.txt", StartLine: 1, EndLine: 1, ContentRef: "assets/range-body.txt", Purpose: "Replace line from a plan body ref."},
		},
	})
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("ref-backed edits should be valid plan edits: %s", result.Error)
	}

	planPath := filepath.Join(root, ".idiom", "plans", "last.plan.json")

	body, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatalf("read raw ref-backed plan: %v", err)
	}

	var raw planartifact.PlanArtifact

	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decode raw ref-backed plan: %v", err)
	}

	if raw.Edits[0].NewText != "" || raw.Edits[0].NewTextRef != "assets/whole-body.txt" || raw.Edits[1].Content != "" || raw.Edits[1].ContentRef != "assets/range-body.txt" {
		t.Fatalf("expected raw artifact to preserve refs without inline hydrated bodies, got %#v", raw.Edits)
	}

	plan, err := LoadPlanArtifact(planPath)
	if err != nil {
		t.Fatalf("load ref-backed plan: %v", err)
	}

	if len(plan.Edits) != 2 {
		t.Fatalf("expected two ref-backed edits, got %d", len(plan.Edits))
	}

	if plan.Edits[0].NewText != "after whole\n" || plan.Edits[1].Content != "after range\n" {
		t.Fatalf("expected LoadPlanArtifact to hydrate refs, got %#v", plan.Edits)
	}
}

func TestSubmitPlanRejectsNoOpRefBackedEditBeforeWritingArtifact(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "range.txt", "same\n")
	writeSubmitPlanBodyRef(t, root, "assets/range-body.txt", "same\n")

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "range.txt", StartLine: 1, EndLine: 1, ContentRef: "assets/range-body.txt", Purpose: "no-op ref-backed repair"},
		},
	})
	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("no-op ref-backed edit should be rejected")
	}

	if !strings.Contains(result.Error, "edit would not change range.txt") {
		t.Fatalf("no-op ref-backed rejection should name unchanged edit, got %q", result.Error)
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
		t.Fatalf("submit_plan should reject before writing an artifact; stat err=%v", err)
	}
}

func TestSubmitPlanRejectsLineRangeDeletionWithExactTextGuidance(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "x.txt", "one\ntwo\nthree\n")

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "x.txt", StartLine: 2, EndLine: 2, Content: "", Purpose: "delete obsolete line"},
		},
	})
	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("empty line-range content should be rejected")
	}

	for _, want := range []string{"line-range edits require non-empty content", "old_text", "empty new_text"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("rejection should guide deletion shape with %q, got %q", want, result.Error)
		}
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
		t.Fatalf("submit_plan should reject before writing an artifact; stat err=%v", err)
	}
}

func TestSubmitPlanRejectsAmbiguousOldTextBeforeWritingArtifact(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "matrix.cpp", "set_floor(30.0f);\nother();\nset_floor(30.0f);\n")

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "matrix.cpp", OldText: "set_floor(30.0f);", NewText: "floor = 30.0f;"},
		},
	})
	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("ambiguous old_text should be rejected")
	}

	if !strings.Contains(result.Error, "matched 2 locations") {
		t.Fatalf("rejection should report duplicate match count, got %q", result.Error)
	}

	for _, want := range []string{"matching start line(s): 1, 3", "read_file", "replace_all=true"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("rejection should include occurrence guidance %q, got %q", want, result.Error)
		}
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
		t.Fatalf("submit_plan should reject before writing an artifact; stat err=%v", err)
	}
}

func TestSubmitPlanRejectsMissingOldTextBeforeWritingArtifact(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "x.txt", "actual\n")

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "x.txt", OldText: "expected", NewText: "replacement"},
		},
	})
	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("missing old_text should be rejected")
	}

	if !strings.Contains(result.Error, "did not appear") {
		t.Fatalf("rejection should report missing old_text, got %q", result.Error)
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
		t.Fatalf("submit_plan should reject before writing an artifact; stat err=%v", err)
	}
}

func TestSubmitPlanRejectsTruncatedSearchAnchorBeforeWritingArtifact(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "catalog.go", "package main\n\nvar catalog = \"needle "+strings.Repeat("x", 800)+"\"\n")

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
	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("truncated search anchor should be rejected")
	}

	for _, want := range []string{"truncation marker", "search_content", "read_file", "start_line/end_line"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("rejection should include truncated-anchor guidance %q, got %q", want, result.Error)
		}
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
		t.Fatalf("submit_plan should reject before writing an artifact; stat err=%v", err)
	}
}

func TestSubmitPlanRejectsAdjacentDuplicateCodeLine(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "sim.cpp", "void tick() {\n    auto& c = entities_->colonists.data()[i];\n    c.cultural_work_mult = 1.0f;\n}\n")

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
	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("duplicate adjacent code line should be rejected")
	}

	if !strings.Contains(result.Error, "adjacent duplicate code line") {
		t.Fatalf("rejection should name adjacent duplicate code, got %q", result.Error)
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
		t.Fatalf("submit_plan should reject before writing an artifact; stat err=%v", err)
	}
}

func TestSubmitPlanRejectsCompletedPlanningArtifactOverwrite(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, ".planning/atoms/done.md", "# Done\n\nStatus: accepted\n\nKeep this spec.\n")

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
	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("completed artifact overwrite should be rejected")
	}

	if !strings.Contains(result.Error, "completed planning artifact") {
		t.Fatalf("rejection should name completed planning artifact, got %q", result.Error)
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
		t.Fatalf("submit_plan should reject before writing an artifact; stat err=%v", err)
	}
}

func TestSubmitPlanAllowsReplaceAllForRepeatedOldText(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "matrix.cpp", "set_floor(30.0f);\nset_floor(30.0f);\n")

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "matrix.cpp", OldText: "set_floor(30.0f);", NewText: "floor = 30.0f;", ReplaceAll: true},
		},
	})
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("replace_all should allow repeated old_text: %s", result.Error)
	}
}

func TestSubmitPlanAcceptsWholeFileNewFileShape(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
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

	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("submit_plan rejected missing/new-file shape: %s", result.Error)
	}

	plan, err := LoadPlanArtifact(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if len(plan.Edits) != 1 {
		t.Fatalf("expected one edit, got %d", len(plan.Edits))
	}

	edit := plan.Edits[0]
	if !edit.ReplaceAll || edit.OldText != "" || !strings.Contains(edit.NewText, `"ok": true`) {
		t.Fatalf("missing/new-file shape not persisted correctly: %#v", edit)
	}
}

func TestSubmitPlanSpillsLargeEditBodiesToRefs(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	large := strings.Repeat("line\n", 1200)
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "large.txt", NewText: large, ReplaceAll: true, Purpose: "Create the large file body."},
		},
	})

	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("submit_plan rejected large whole-file body: %s", result.Error)
	}

	if !strings.Contains(result.Output, "content_refs: 1") {
		t.Fatalf("expected submit_plan to report one content ref, got %q", result.Output)
	}

	body, err := os.ReadFile(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}

	var raw planartifact.PlanArtifact

	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decode raw plan: %v", err)
	}

	if len(raw.Edits) != 1 || raw.Edits[0].NewText != "" || raw.Edits[0].NewTextRef == "" {
		t.Fatalf("expected raw artifact to store new_text_ref instead of inline body: %#v", raw.Edits)
	}

	refPath := filepath.Join(root, ".idiom", "plans", filepath.FromSlash(raw.Edits[0].NewTextRef))

	refBody, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatalf("read spilled edit body %s: %v", refPath, err)
	}

	if string(refBody) != large {
		t.Fatalf("spilled large body mismatch: got %d bytes, want %d", len(refBody), len(large))
	}

	plan, err := LoadPlanArtifact(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
	if err != nil {
		t.Fatalf("load plan: %v", err)
	}

	if plan.Edits[0].NewText != large {
		t.Fatalf("hydrated large body mismatch: got %d bytes, want %d", len(plan.Edits[0].NewText), len(large))
	}
}

func TestSubmitPlanRejectsLargeInsertAfterLineContentBeforeSpill(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "x.txt", "one\ntwo\n")

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

	if strings.TrimSpace(result.Error) == "" {
		t.Fatalf("large insert_after_line content should be rejected before spill")
	}

	for _, want := range []string{"insert_after_line content exceeds inline submit_plan limit", "content_ref is not supported", "line-range replacement", "unchanged anchor line"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("large insertion rejection missing %q: %q", want, result.Error)
		}
	}
	for _, forbidden := range []string{"replace_all", "whole file", "whole-file"} {
		if strings.Contains(result.Error, forbidden) {
			t.Fatalf("large insertion rejection retained stale whole-file guidance %q: %q", forbidden, result.Error)
		}
	}

	if _, err := os.Stat(filepath.Join(root, ".idiom", "plans", "last.plan.json")); !os.IsNotExist(err) {
		t.Fatalf("large insertion should reject before writing artifact; stat err=%v", err)
	}

	matches, err := filepath.Glob(filepath.Join(root, ".idiom", "plans", "assets", "*"))
	if err != nil {
		t.Fatalf("glob spill assets: %v", err)
	}

	if len(matches) != 0 {
		t.Fatalf("large insertion should reject before writing spill assets, got %v", matches)
	}
}

func TestLoadPlanArtifactRoundTrip(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "x.txt", "old\n")
	mgr.Execute(context.Background(), ToolRequest{
		Tool:      "submit_plan",
		Rationale: "test plan",
		Edits: []planartifact.PlanEdit{
			{Path: "x.txt", OldText: "old", NewText: "new"},
		},
	})

	planPath := filepath.Join(root, ".idiom", "plans", "last.plan.json")

	plan, err := LoadPlanArtifact(planPath)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	if len(plan.Edits) != 1 || plan.Edits[0].Path != "x.txt" {
		t.Fatalf("round-tripped plan mismatch: %#v", plan)
	}
}

func TestLoadPlanArtifactRejectsWrongSchemaVersion(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "bogus.plan.json")
	body := []byte(`{"schema_version":"idiom.plan.v0","edits":[{"path":"a.txt","old_text":"a","new_text":"b"}]}`)

	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	_, err := LoadPlanArtifact(path)
	if err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("expected schema_version mismatch error, got %v", err)
	}
}

func TestSubmitPlanPersistsPackIDForExecutorRehydration(t *testing.T) {
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "a.txt", "old\n")

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:      "submit_plan",
		Rationale: "exercise pack_id plumbing",
		PackID:    "feedfacecafebeef",
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "old", NewText: "new"},
		},
	})
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("submit_plan rejected a valid plan: %s", result.Error)
	}

	plan, err := LoadPlanArtifact(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if plan.PackID != "feedfacecafebeef" {
		t.Fatalf("pack_id not persisted: got %q", plan.PackID)
	}
}

func TestSubmitPlanStampsSessionIDFromConfig(t *testing.T) {
	// SessionID stamped on every plan artifact lets findLatestPlanArtifact
	// disambiguate concurrent plan-execute runs in the same workspace.
	t.Helper()

	root := t.TempDir()
	writeSubmitPlanFixture(t, root, "a.txt", "x\n")

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

	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("submit_plan rejected: %s", result.Error)
	}

	plan, err := LoadPlanArtifact(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if plan.SessionID != "session-deadbeef" {
		t.Fatalf("session_id not stamped: got %q want session-deadbeef", plan.SessionID)
	}
}

func TestSubmitPlanOmitsEmptySessionID(t *testing.T) {
	// Backward compatibility: legacy callers that don't set
	// cfg.Workspace.SessionID must still produce valid v1 artifacts
	// with the field omitted.
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "a.txt", "x\n")

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool: "submit_plan",
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "x", NewText: "y"},
		},
	})
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("submit_plan rejected: %s", result.Error)
	}

	body, err := os.ReadFile(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if strings.Contains(string(body), `"session_id"`) {
		t.Fatalf("empty SessionID should be omitted from artifact, got: %s", body)
	}
}

func TestSubmitPlanOmitsEmptyPackID(t *testing.T) {
	// pack_id is optional and additive — older planners that don't
	// provide it must still produce valid v1 artifacts.
	mgr, root := newSubmitPlanTestManager(t)
	writeSubmitPlanFixture(t, root, "a.txt", "x\n")

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:      "submit_plan",
		Rationale: "no pack id provided",
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "x", NewText: "y"},
		},
	})
	if strings.TrimSpace(result.Error) != "" {
		t.Fatalf("submit_plan rejected: %s", result.Error)
	}

	body, err := os.ReadFile(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if strings.Contains(string(body), `"pack_id"`) {
		t.Fatalf("empty pack_id should be omitted from artifact, got %s", body)
	}
}
