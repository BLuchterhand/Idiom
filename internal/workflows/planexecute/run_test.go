package planexecute

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/platform/config"
	"idiom/internal/repository/comprehension"
	"idiom/internal/runtime/workerruntime"
	"idiom/internal/workflows/recovery"
)

func TestRunEmitsQueueAdmissionBeforePlanner(t *testing.T) {
	root := t.TempDir()
	t.Setenv("IDIOM_QUEUE_POOL", "qwen-35b")
	t.Setenv("IDIOM_QUEUE_SOCKET", filepath.Join(root, "missing-runner.sock"))

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	var stdout, stderr bytes.Buffer

	plannerCalled := false
	result := Run(cfg, Options{
		Prompt:     "plan this change",
		JSONEvents: true,
	}, func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
		plannerCalled = true

		if !strings.Contains(stdout.String(), `"type":"idiom.plan_execute.queue_unavailable"`) {
			t.Fatalf("queue admission event should be emitted before planner call, stdout=%s", stdout.String())
		}

		return "no plan", nil
	}, nil, &stdout, &stderr)

	if !plannerCalled {
		t.Fatal("expected planner prompt runner to be called")
	}

	if result.ExitCode != 1 {
		t.Fatalf("Run exit=%d, want missing-artifact failure; stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String())
	}

	if strings.Contains(stdout.String(), "idiom.plan_execute.queue_acquired") {
		t.Fatalf("missing daemon should not be reported as acquired, stdout=%s", stdout.String())
	}
}

func TestPlanExecutePromptTreatsReplaceAllAsNewFileOnly(t *testing.T) {
	prompt := planExecutePlannerSystemAddendum([]string{"pkg/existing.go", "pkg/new_helper.go"}, nil)

	for _, want := range []string{
		"replace_all:true`+`new_text`+empty `old_text` for missing/new files only",
		"Existing source/test files must use a bounded anchored edit shape",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}

	for _, forbidden := range []string{
		"replace_all:true`+`new_text`+empty `old_text` for new-file/whole-file write",
		"whole/new-file `replace_all:true`",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("prompt still advertises stale replace_all contract %q:\n%s", forbidden, prompt)
		}
	}
}

func TestRunModelOverrideReachesPlannerAndReviewRepair(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.LlamaServer.Model = "qwen-local-default"

	var stdout, stderr bytes.Buffer
	var calls []struct {
		model          string
		taskFamily     string
		useActiveModel bool
	}

	result := Run(cfg, Options{
		Prompt:            "Update a.txt from before to after.",
		Model:             "deepseek-v4-flash",
		RequiredEditPaths: MultiFlag{"a.txt"},
		JSONEvents:        true,
	}, func(callCfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		calls = append(calls, struct {
			model          string
			taskFamily     string
			useActiveModel bool
		}{
			model:          callCfg.LlamaServer.Model,
			taskFamily:     opts.TaskFamily,
			useActiveModel: opts.UseActiveModel,
		})

		switch opts.TaskFamily {
		case string(workerruntime.TaskFamilyPlanThenExecutePlanner):
			mgr := workspace.NewManager(callCfg.Workspace)
			res := mgr.Execute(context.Background(), workspace.ToolRequest{
				Tool:      workspace.ToolNameSubmitPlan,
				Rationale: "Update a.txt.",
				Edits: []workspace.PlanEdit{{
					Path:    "a.txt",
					OldText: "before\n",
					NewText: "after\n",
					Purpose: "update fixture text",
				}},
			})
			if strings.TrimSpace(res.Error) != "" {
				return "", fmt.Errorf("submit_plan: %s", res.Error)
			}

			return "Plan submitted.", nil
		case string(workerruntime.TaskFamilyPlanThenExecuteReview):
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: a.txt now contains after.\n- Scope safety: changed path is limited to a.txt.", nil
		default:
			return "", fmt.Errorf("unexpected task family %q", opts.TaskFamily)
		}
	}, nil, &stdout, &stderr)

	if len(calls) < 2 {
		t.Fatalf("expected planner and review-repair calls, got %#v result=%#v stdout=%s stderr=%s", calls, result, stdout.String(), stderr.String())
	}

	for _, call := range calls {
		if call.model != "deepseek-v4-flash" {
			t.Fatalf("task family %s saw model %q, want explicit override; calls=%#v", call.taskFamily, call.model, calls)
		}

		if !call.useActiveModel {
			t.Fatalf("task family %s did not preserve active-model authority; calls=%#v", call.taskFamily, calls)
		}
	}
}

func TestRunTerminalCompletionCoversMixedSourceSatisfiedPlan(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("already final\n"), 0o644); err != nil {
		t.Fatalf("seed b.txt: %v", err)
	}

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer

	result := Run(cfg, Options{
		Prompt:            "Update a.txt while b.txt is already final.",
		RequiredEditPaths: MultiFlag{"a.txt", "b.txt"},
		JSONEvents:        true,
	}, func(callCfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		switch opts.TaskFamily {
		case string(workerruntime.TaskFamilyPlanThenExecutePlanner):
			mgr := workspace.NewManager(callCfg.Workspace)
			plan := workspace.PlanArtifact{
				SchemaVersion: workspace.PlanArtifactSchemaVersion,
				Task:          workspace.PlanArtifactTask{Summary: "mixed source-satisfied coverage"},
				Contract: workspace.PlanArtifactContract{
					Kind:              "plan_then_execute",
					StrategyOwner:     "model",
					ContractOwner:     "harness",
					PermissionProfile: "plan_then_execute",
					VerificationOwner: "planner",
				},
				Paths: workspace.PlanArtifactPaths{
					Editable: []string{"a.txt"},
					Changed:  []string{"a.txt"},
					Required: []string{"a.txt", "b.txt"},
				},
				Evidence: []workspace.PlanArtifactEvidence{{
					Kind: "harness_observed_source",
					Path: "b.txt",
				}},
				VerificationObligations: []workspace.PlanArtifactVerificationObligation{{
					Kind:     "focused_tests",
					Status:   "open",
					Required: true,
				}},
				Approval: workspace.PlanArtifactApproval{Required: true, Status: "approved", ScopeID: "scope-1", Mode: "local"},
				Handoff:  workspace.PlanArtifactHandoff{ID: "handoff-1", From: "planner", To: "executor", Status: "ready", Ready: true},
				Edits: []workspace.PlanEdit{{
					Path:    "a.txt",
					OldText: "before\n",
					NewText: "after\n",
					Purpose: "update fixture text",
				}},
			}
			res := mgr.Execute(context.Background(), workspace.ToolRequest{
				Tool:                 "submit_plan",
				Rationale:            "a.txt needs an edit; b.txt is already final from current source evidence.",
				SourceSatisfiedPaths: []string{"b.txt"},
				PlanArtifact:         &plan,
			})
			if strings.TrimSpace(res.Error) != "" {
				return "", fmt.Errorf("submit_plan: %s", res.Error)
			}

			return "Plan submitted.", nil
		case string(workerruntime.TaskFamilyPlanThenExecuteReview):
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: a.txt now contains after and b.txt was already final.\n- Scope safety: changed path is limited to a.txt.", nil
		default:
			return "", fmt.Errorf("unexpected task family %q", opts.TaskFamily)
		}
	}, nil, &stdout, &stderr)

	if result.ExitCode != 0 {
		t.Fatalf("Run exit=%d result=%#v stdout=%s stderr=%s", result.ExitCode, result, stdout.String(), stderr.String())
	}
	if result.Completion == nil {
		t.Fatalf("expected terminal completion record, result=%#v stdout=%s", result, stdout.String())
	}
	if !slices.Contains(result.Completion.SourceSatisfiedEvidencePaths, "b.txt") {
		t.Fatalf("terminal completion lost source_satisfied path: %#v", result.Completion)
	}
	coverage := map[string]string{}
	for _, item := range result.Completion.RequiredPathCoverage {
		coverage[item.Path] = item.Status
	}
	if coverage["a.txt"] != "changed" || coverage["b.txt"] != "source_satisfied" {
		t.Fatalf("required coverage = %#v, want changed/source_satisfied", result.Completion.RequiredPathCoverage)
	}
}

func TestClassifyExecutorHandoffTreatsScopedFreshPacketAsFresh(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/planexec\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package pkg\n\nfunc A() string { return \"alpha\" }\n"), 0o644); err != nil {
		t.Fatalf("seed a.go: %v", err)
	}

	testPath := filepath.Join(root, "a_test.go")
	if err := os.WriteFile(testPath, []byte("package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatalf("seed a_test.go: %v", err)
	}

	build, err := comprehension.Build(t.Context(), comprehension.BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	key := comprehension.ViewKey{IndexID: build.IndexID, Task: "scoped fresh shared handoff", Mode: "plan"}
	if err := comprehension.SaveView(root, key, comprehension.PackResult{
		PackID:  key.ID(),
		IndexID: build.IndexID,
		Root:    root,
		Task:    "scoped fresh shared handoff",
		Mode:    "plan",
		Fresh:   false,
		Freshness: &comprehension.FreshnessDecision{
			Policy:        comprehension.FreshnessReadThrough,
			Operation:     "context_pack",
			IndexID:       build.IndexID,
			FreshForScope: true,
			GlobalFresh:   false,
		},
		CandidateFiles: []comprehension.PackFile{{Path: "a.go", Role: "editable"}},
		Tests:          []comprehension.PackTest{{Command: "go test ./...", Path: "a_test.go"}},
	}); err != nil {
		t.Fatalf("SaveView: %v", err)
	}

	if err := os.WriteFile(testPath, []byte("package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) { t.Helper() }\n"), 0o644); err != nil {
		t.Fatalf("make advisory test stale: %v", err)
	}

	handoff := classifyExecutorHandoffPacket(root, workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		PackID:        key.ID(),
	})
	if handoff.State != ExecutorHandoffStateInherited || handoff.PacketFreshnessState != ExecutorPacketFreshnessFresh || !handoff.Fresh {
		t.Fatalf("expected scoped-fresh packet to classify as fresh inherited handoff, got %#v", handoff)
	}
}

func TestApplyExecutorHandoffViewPopulatesQuality(t *testing.T) {
	view := comprehension.PackResult{
		PackID: "abc123def456abc1",
		Fresh:  true,
		Quality: comprehension.PackQuality{
			LocalizationConfidence: "high",
			SourceOwnerFound:       true,
			EvidencePaths:          []string{"pkg/target.go"},
			Reasons:                []string{"matched task terms", "strong signal"},
		},
		CandidateFiles:   []comprehension.PackFile{{Path: "pkg/target.go", Role: "editable"}},
		CandidateSymbols: []comprehension.PackSymbol{{QualifiedName: "Target", Path: "pkg/target.go", Kind: "func"}},
		Tests:            []comprehension.PackTest{{Command: "go test ./...", Path: "pkg/target_test.go"}},
	}
	handoff := applyExecutorHandoffView(ExecutorHandoffPacket{}, view)

	if handoff.Quality.LocalizationConfidence != "high" {
		t.Fatalf("Quality.LocalizationConfidence = %q, want high", handoff.Quality.LocalizationConfidence)
	}

	if !handoff.Quality.SourceOwnerFound {
		t.Fatal("Quality.SourceOwnerFound should be true")
	}

	if len(handoff.Quality.EvidencePaths) != 1 || handoff.Quality.EvidencePaths[0] != "pkg/target.go" {
		t.Fatalf("Quality.EvidencePaths = %#v, want [pkg/target.go]", handoff.Quality.EvidencePaths)
	}

	if len(handoff.Quality.Reasons) != 2 {
		t.Fatalf("Quality.Reasons = %#v, want 2 reasons", handoff.Quality.Reasons)
	}

	if handoff.CandidateFiles != 1 {
		t.Fatalf("CandidateFiles = %d, want 1", handoff.CandidateFiles)
	}
}

func TestPhaseEvidencePacketFromViewPopulatesQuality(t *testing.T) {
	view := comprehension.PackResult{
		PackID: "abc123def456abc1",
		Fresh:  true,
		Quality: comprehension.PackQuality{
			LocalizationConfidence: "medium",
			SourceOwnerFound:       false,
			EvidencePaths:          []string{"pkg/target.go", "pkg/helper.go"},
			Reasons:                []string{"partial match"},
		},
		CandidateFiles:   []comprehension.PackFile{{Path: "pkg/target.go", Role: "editable", Reason: "matched terms"}},
		CandidateSymbols: []comprehension.PackSymbol{{QualifiedName: "Helper", Path: "pkg/helper.go", Kind: "func", Reason: "called by target"}},
		Tests:            []comprehension.PackTest{{Command: "go test ./pkg/...", Path: "pkg/target_test.go", Reason: "covers target"}},
	}
	packet := PhaseEvidencePacketFromView(view)

	if packet.Quality.LocalizationConfidence != "medium" {
		t.Fatalf("Quality.LocalizationConfidence = %q, want medium", packet.Quality.LocalizationConfidence)
	}

	if packet.Quality.SourceOwnerFound {
		t.Fatal("Quality.SourceOwnerFound should be false")
	}

	if len(packet.Quality.EvidencePaths) != 2 {
		t.Fatalf("Quality.EvidencePaths = %#v, want 2 paths", packet.Quality.EvidencePaths)
	}

	if len(packet.Quality.Reasons) != 1 || packet.Quality.Reasons[0] != "partial match" {
		t.Fatalf("Quality.Reasons = %#v, want [partial match]", packet.Quality.Reasons)
	}

	// Verify existing fields unchanged.
	if packet.PackID != "abc123def456abc1" {
		t.Fatalf("PackID = %q", packet.PackID)
	}

	if packet.Status != "available" {
		t.Fatalf("Status = %q", packet.Status)
	}
}

func TestExecutorHandoffPacketEventFieldsIncludesQuality(t *testing.T) {
	handoff := ExecutorHandoffPacket{
		State:                ExecutorHandoffStateInherited,
		Reason:               "fresh_graph_view",
		PackID:               "abc123def456abc1",
		Fresh:                true,
		PacketFreshnessState: ExecutorPacketFreshnessFresh,
		CandidateFiles:       2,
		CandidateSymbols:     3,
		Tests:                1,
		Quality: comprehension.CompactPackQuality{
			LocalizationConfidence: "high",
			SourceOwnerFound:       true,
			EvidencePaths:          []string{"pkg/feature.go"},
			Reasons:                []string{"strong signal"},
		},
	}
	fields := executorHandoffPacketEventFields(handoff)

	quality, ok := fields["quality"].(map[string]any)
	if !ok {
		t.Fatalf("fields[\"quality\"] missing or wrong type, fields=%#v", fields)
	}

	if quality["localization_confidence"] != "high" {
		t.Fatalf("quality[localization_confidence] = %v", quality["localization_confidence"])
	}

	if quality["source_owner_found"] != true {
		t.Fatalf("quality[source_owner_found] = %v", quality["source_owner_found"])
	}

	paths, _ := quality["evidence_paths"].([]string)
	if len(paths) != 1 || paths[0] != "pkg/feature.go" {
		t.Fatalf("quality[evidence_paths] = %#v", paths)
	}

	reasons, _ := quality["reasons"].([]string)
	if len(reasons) != 1 || reasons[0] != "strong signal" {
		t.Fatalf("quality[reasons] = %#v", reasons)
	}
}

func TestExecutorHandoffPacketEventFieldsOmitsQualityWhenUnset(t *testing.T) {
	handoff := ExecutorHandoffPacket{
		State: ExecutorHandoffStateMissing,
	}
	fields := executorHandoffPacketEventFields(handoff)

	if _, ok := fields["quality"]; ok {
		t.Fatal("fields[\"quality\"] should be absent for unset quality")
	}
}

func TestPhaseEvidenceHandoffFromExecutorCopiesQuality(t *testing.T) {
	handoff := ExecutorHandoffPacket{
		State:                ExecutorHandoffStateInherited,
		Reason:               "fresh_graph_view",
		PackID:               "abc123def456abc1",
		Fresh:                true,
		PacketFreshnessState: ExecutorPacketFreshnessFresh,
		CandidateFiles:       2,
		Quality: comprehension.CompactPackQuality{
			LocalizationConfidence: "high",
			SourceOwnerFound:       true,
			EvidencePaths:          []string{"pkg/feature.go"},
			Reasons:                []string{"strong signal"},
		},
	}
	peh := phaseEvidenceHandoffFromExecutor(handoff)

	if peh.Quality.LocalizationConfidence != "high" {
		t.Fatalf("Quality.LocalizationConfidence = %q, want high", peh.Quality.LocalizationConfidence)
	}

	if !peh.Quality.SourceOwnerFound {
		t.Fatal("Quality.SourceOwnerFound should be true")
	}

	if len(peh.Quality.EvidencePaths) != 1 || peh.Quality.EvidencePaths[0] != "pkg/feature.go" {
		t.Fatalf("Quality.EvidencePaths = %#v", peh.Quality.EvidencePaths)
	}
}

func TestPreflightPlanEditsRejectsEscapingPathBeforeRead(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "outside.txt")

	if err := os.WriteFile(outside, []byte("secret\n"), 0o644); err != nil {
		t.Fatalf("seed outside file: %v", err)
	}

	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "../outside.txt", OldText: "secret", NewText: "redacted"},
		},
	}

	got := preflightPlanEdits(root, plan)
	if got == nil || got.Reason != "edit has invalid path" {
		t.Fatalf("expected invalid path preflight mismatch, got %#v", got)
	}
}

func TestPreflightPlanEditsRejectsStaleAnchorProvenance(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}

	edits, err := workspace.AttachPlanEditAnchorProvenance(root, []workspace.PlanEdit{
		{Path: "a.txt", OldText: "alpha", NewText: "ALPHA"},
	})
	if err != nil {
		t.Fatalf("attach anchor provenance: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("prefix\nalpha\n"), 0o644); err != nil {
		t.Fatalf("mutate a.txt: %v", err)
	}

	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits:         edits,
	}
	got := preflightPlanEdits(root, plan)

	if got == nil {
		t.Fatalf("preflight must reject stale anchor provenance before writes")
	}

	if got.Index != 0 || got.Path != "a.txt" {
		t.Fatalf("mismatch should point at edit 0 in a.txt, got %#v", got)
	}

	if !strings.Contains(got.Reason, "anchor provenance stale") || !strings.Contains(got.Reason, "file fingerprint changed") {
		t.Fatalf("mismatch should report stale provenance, got %q", got.Reason)
	}
}

func TestPreflightPlanEditsRejectsCurrentSchemaMissingAnchorProvenance(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}

	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Created:       "2026-06-08T20:00:00Z",
		Handoff: workspace.PlanArtifactHandoff{
			From: "planner",
			To:   "plan_execute_executor",
		},
		Edits: []workspace.PlanEdit{{
			Path:    "a.txt",
			OldText: "alpha",
			NewText: "ALPHA",
		}},
	}

	got := preflightPlanEdits(root, plan)
	if got == nil || !strings.Contains(got.Reason, "missing submit_plan anchor provenance") {
		t.Fatalf("expected missing anchor provenance mismatch, got %#v", got)
	}
}

func TestPreflightPlanRequiredEditPathsAcceptsSourceSatisfiedCoverage(t *testing.T) {
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Paths: workspace.PlanArtifactPaths{
			SourceSatisfied: []string{"a.go"},
		},
		Evidence: []workspace.PlanArtifactEvidence{{Kind: "harness_observed_source", Path: "a.go"}},
		Edits: []workspace.PlanEdit{
			{Path: "b.go", OldText: "before", NewText: "after", Purpose: "update b"},
		},
	}

	if got := preflightPlanRequiredEditPaths([]string{"a.go", "b.go"}, plan); got != nil {
		t.Fatalf("source_satisfied_paths should cover already-satisfied required paths, got %#v", got)
	}
}

func TestPreflightPlanDeclaredScopeAcceptsSourceSatisfiedRequiredPath(t *testing.T) {
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Paths: workspace.PlanArtifactPaths{
			Required:        []string{"a.go", "b.go"},
			Editable:        []string{"b.go"},
			Changed:         []string{"b.go"},
			SourceSatisfied: []string{"a.go"},
		},
		Evidence: []workspace.PlanArtifactEvidence{{Kind: "harness_observed_source", Path: "a.go"}},
		Edits: []workspace.PlanEdit{
			{Path: "b.go", OldText: "before", NewText: "after", Purpose: "update b"},
		},
	}

	if got := preflightPlanDeclaredScope(plan); got != nil {
		t.Fatalf("source_satisfied_paths should satisfy declared required scope coverage, got %#v", got)
	}
}

func TestPreflightPlanRequiredEditPathsRejectsOutOfScopeSourceSatisfiedPath(t *testing.T) {
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Paths: workspace.PlanArtifactPaths{
			SourceSatisfied: []string{"outside.go"},
		},
		Edits: []workspace.PlanEdit{
			{Path: "b.go", OldText: "before", NewText: "after", Purpose: "update b"},
		},
	}

	got := preflightPlanRequiredEditPaths([]string{"a.go", "b.go"}, plan)
	if got == nil || got.Path != "outside.go" || got.Reason != "source-satisfied path is outside required_edit_paths allowlist" {
		t.Fatalf("expected out-of-scope source_satisfied_paths rejection, got %#v", got)
	}
}

func TestPreflightPlanDeclaredScopeRejectsOutOfScopeSourceSatisfiedPath(t *testing.T) {
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Paths: workspace.PlanArtifactPaths{
			Required:        []string{"a.go", "b.go"},
			Editable:        []string{"b.go"},
			Changed:         []string{"b.go"},
			SourceSatisfied: []string{"outside.go"},
		},
		Edits: []workspace.PlanEdit{
			{Path: "b.go", OldText: "before", NewText: "after", Purpose: "update b"},
		},
	}

	got := preflightPlanDeclaredScope(plan)
	if got == nil || got.Path != "outside.go" || got.Reason != "source-satisfied path is outside required plan scope" {
		t.Fatalf("expected declared-scope source_satisfied_paths rejection, got %#v", got)
	}
}

func TestPreflightPlanDeclaredScopeRejectsPendingPartialApprovalSourceSatisfiedPath(t *testing.T) {
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Paths: workspace.PlanArtifactPaths{
			Required:        []string{"a.go", "b.go"},
			Editable:        []string{"b.go"},
			Changed:         []string{"b.go"},
			SourceSatisfied: []string{"a.go"},
		},
		Evidence: []workspace.PlanArtifactEvidence{{Kind: "harness_observed_source", Path: "a.go"}},
		Approval: workspace.PlanArtifactApproval{
			Required:            true,
			Status:              "partial",
			ScopeID:             "scope-1",
			ApprovedPaths:       []string{"b.go"},
			PendingPaths:        []string{"a.go"},
			ExecutionPermission: "execute_approved_scope",
		},
		Handoff: workspace.PlanArtifactHandoff{ID: "handoff-1", Status: "ready", Ready: true},
		Edits: []workspace.PlanEdit{
			{Path: "b.go", OldText: "before", NewText: "after", Purpose: "update b"},
		},
	}

	got := preflightPlanDeclaredScope(plan)
	if got == nil || got.Path != "a.go" || got.Reason != "source-satisfied path is still pending approval" {
		t.Fatalf("expected pending-approval source_satisfied_paths rejection, got %#v", got)
	}
}

func TestRunExecutorPhaseAppliesInsertAfterLinePlanEdit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "feature_test.go"), []byte("package demo\n\nfunc TestExisting(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatalf("seed feature_test.go: %v", err)
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	writeRunTestPlanViaSubmitPlan(t, cfg, []workspace.PlanEdit{{
		Path:            "feature_test.go",
		InsertAfterLine: 3,
		Content:         "\nfunc TestInserted(t *testing.T) {}\n",
		Purpose:         "insert a regression test after the existing test",
	}}, "insert regression test")

	plan, err := workspace.LoadPlanArtifact(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
	if err != nil {
		t.Fatalf("load submitted plan: %v", err)
	}

	var stdout bytes.Buffer

	report, err := RunExecutorPhase(cfg, plan, []string{"feature_test.go"}, true, &stdout)
	if err != nil {
		t.Fatalf("executor should apply insert_after_line plan edit, report=%#v stdout=%s err=%v", report, stdout.String(), err)
	}

	if report.Succeeded != 1 || report.Failed != 0 {
		t.Fatalf("unexpected executor report: %#v", report)
	}

	got := readFileStringForRunTest(t, filepath.Join(root, "feature_test.go"))
	if !strings.Contains(got, "func TestExisting") || !strings.Contains(got, "func TestInserted") {
		t.Fatalf("insert_after_line edit not applied:\n%s", got)
	}
}

func TestRunAutoExactPlanSourceSatisfiedSkipsPlanner(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("new a\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	prompt := "## Files To Modify\n\n" +
		"| File | Change |\n" +
		"|---|---|\n" +
		"| `a.txt` | Update a. |\n\n" +
		"## Required Edits\n\n" +
		"In `a.txt`, replace the text.\n\n" +
		"old_text:\n\n```text\nold a\n```\n\n" +
		"new_text:\n\n```text\nnew a\n```\n"

	var stdout, stderr bytes.Buffer

	plannerCalled := false

	result := Run(cfg, Options{
		Prompt:     prompt,
		JSONEvents: true,
	}, func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
		plannerCalled = true
		return "", nil
	}, nil, &stdout, &stderr)

	if result.ExitCode != 0 {
		t.Fatalf("source-satisfied run should pass, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String())
	}

	if plannerCalled {
		t.Fatal("planner should not run when exact edits are already applied")
	}

	if !strings.Contains(stdout.String(), `"type":"idiom.plan_execute.auto_exact_plan_source_satisfied"`) ||
		!strings.Contains(stdout.String(), `"type":"idiom.plan_execute.source_satisfied"`) {

		t.Fatalf("source-satisfied events missing from stdout=%s", stdout.String())
	}
}

func TestRunRejectsSourceSatisfiedAnswerWithOnlyProseEvidencePaths(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer

	result := Run(cfg, Options{
		Prompt: "Reject temp symlinks and add regression tests proving no partial mutation.",
		RequiredEditPaths: MultiFlag{
			"internal/workflows/productworkflow/migration_apply.go",
			"internal/workflows/productworkflow/migration_apply_test.go",
		},
		JSONEvents: true,
	}, func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
		return "Decision: source-satisfied\n- internal/workflows/productworkflow/migration_apply.go: no changes needed.\n- internal/workflows/productworkflow/migration_apply_test.go: no changes needed.", nil
	}, nil, &stdout, &stderr)

	if result.ExitCode != 1 || result.FailureKind != RunFailureTerminalCompletionContractFailed {
		t.Fatalf("source-satisfied answer with only prose evidence should fail terminal contract, got code=%d kind=%s stdout=%s stderr=%s", result.ExitCode, result.FailureKind, stdout.String(), stderr.String())
	}

	if !strings.Contains(stdout.String(), `"type":"idiom.plan_execute.source_satisfied"`) {
		t.Fatalf("source-satisfied event missing from stdout=%s", stdout.String())
	}

	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	event, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.source_satisfied")

	if !ok {
		t.Fatalf("expected source_satisfied event, got %s", stdout.String())
	}

	if got, _ := event["source_satisfied_evidence_paths"].([]any); len(got) != 2 {
		t.Fatalf("expected source-satisfied evidence paths in telemetry, got %#v", event)
	}

	terminal, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.terminal_completion")
	if !ok || terminal["failure_kind"] != RunFailureTerminalCompletionContractFailed {
		t.Fatalf("expected terminal completion contract failure, got %#v", terminal)
	}
}

func TestRunRejectsSourceSatisfiedAnswerWithBenignUnsupportedWordButOnlyProseEvidence(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer

	result := Run(cfg, Options{
		Prompt: "Reject temp symlinks and add regression tests proving no partial mutation.",
		RequiredEditPaths: MultiFlag{
			"internal/workflows/productworkflow/migration_apply.go",
			"internal/workflows/productworkflow/migration_apply_test.go",
		},
		JSONEvents: true,
	}, func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
		return strings.Join([]string{
			"Decision: source-satisfied",
			"Context: this code uses a LikelyPrefix helper name elsewhere, but it is not source-satisfied evidence.",
			"- internal/workflows/productworkflow/migration_apply.go: already present; current source rejects temp symlinks before mutation through LikelyPrefix handling.",
			"- internal/workflows/productworkflow/migration_apply_test.go: already present; current tests cover no partial mutation on rejection.",
		}, "\n"), nil
	}, nil, &stdout, &stderr)

	if result.ExitCode != 1 || result.FailureKind != RunFailureTerminalCompletionContractFailed {
		t.Fatalf("source-satisfied answer with only prose evidence should fail terminal contract, got code=%d kind=%s stdout=%s stderr=%s", result.ExitCode, result.FailureKind, stdout.String(), stderr.String())
	}

	if strings.Contains(stdout.String(), `"type":"idiom.plan_execute.source_satisfied_rejected"`) {
		t.Fatalf("benign non-evidence marker should not reject source-satisfied answer, stdout=%s", stdout.String())
	}
}

func TestRunRejectsSourceSatisfiedAnswerWithUnsupportedInference(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer

	result := Run(cfg, Options{
		Prompt:     "Implement item tooltip display and native binding coverage.",
		JSONEvents: true,
	}, func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
		return strings.Join([]string{
			"Decision: source-satisfied",
			"- native/src/core/colony_sim.h: binding fields are already present.",
			"- native/tests/test_quality_inventory_bindings.cpp: coverage is not shown in full but implied by passing tests.",
			"- scripts/ui/ui_helpers.gd: tooltip surface likely already uses the same dictionary fields.",
		}, "\n"), nil
	}, nil, &stdout, &stderr)

	if result.ExitCode != 1 {
		t.Fatalf("expected unsupported source-satisfied rejection, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String())
	}

	if result.FailureKind != RunFailureSourceSatisfiedUnsupportedEvidence {
		t.Fatalf("expected %s, result=%#v stderr=%s", RunFailureSourceSatisfiedUnsupportedEvidence, result, stderr.String())
	}

	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	event, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.source_satisfied_rejected")

	if !ok {
		t.Fatalf("expected source_satisfied_rejected event, got %s", stdout.String())
	}

	if event["reason"] != RunFailureSourceSatisfiedUnsupportedEvidence {
		t.Fatalf("expected unsupported-evidence reason, got %#v", event)
	}

	if event["unsupported_evidence_marker"] != "not shown in full" {
		t.Fatalf("expected rejected marker in telemetry, got %#v", event)
	}

	if got, _ := event["source_satisfied_evidence_paths"].([]any); len(got) != 3 {
		t.Fatalf("expected rejected source-satisfied evidence paths, got %#v", event)
	}
}

func TestRunRejectsSourceSatisfiedAnswerWhenRequestedTestEditMissing(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer

	result := Run(cfg, Options{
		Prompt:             "Fix parser behavior and add regression tests.",
		JSONEvents:         true,
		EditSetObligations: EditSetObligations{}.With(EditSetObligationKindTestEdit, "test"),
	}, func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
		return "Decision: source-satisfied\n- pkg/parser.go: parser behavior is already present; no changes needed.", nil
	}, nil, &stdout, &stderr)

	if result.ExitCode != 1 {
		t.Fatalf("expected missing test edit rejection, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String())
	}

	if result.FailureKind != EditSetFailureMissingRequestedTestEdit || !strings.Contains(stderr.String(), EditSetFailureMissingRequestedTestEdit) {
		t.Fatalf("expected %s, result=%#v stderr=%s", EditSetFailureMissingRequestedTestEdit, result, stderr.String())
	}

	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	event, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.source_satisfied_rejected")

	if !ok {
		t.Fatalf("expected source_satisfied_rejected event, got %s", stdout.String())
	}

	if event["reason"] != EditSetFailureMissingRequestedTestEdit {
		t.Fatalf("expected %s reason, got %#v", EditSetFailureMissingRequestedTestEdit, event)
	}

	if got, _ := event["edit_set_obligations"].([]any); len(got) == 0 {
		t.Fatalf("expected edit_set_obligations metadata, got %#v", event)
	}

	if got, _ := event["source_satisfied_evidence_paths"].([]any); len(got) != 1 || got[0] != "pkg/parser.go" {
		t.Fatalf("expected rejected source-satisfied evidence path, got %#v", event)
	}

	if got, _ := event["missing_edit_set_obligations"].([]any); len(got) == 0 {
		t.Fatalf("expected missing edit-set obligations in rejection telemetry, got %#v", event)
	}
}

func TestRunRejectsSourceSatisfiedAnswerWhenRequestedImplementationEditMissing(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer

	result := Run(cfg, Options{
		Prompt:             "Implement the feature and add tests.",
		JSONEvents:         true,
		EditSetObligations: EditSetObligations{}.With(EditSetObligationKindImplementationEdit, "test"),
	}, func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
		return "Decision: source-satisfied\n- pkg/feature_test.go: feature tests are already present.", nil
	}, nil, &stdout, &stderr)

	if result.ExitCode != 1 {
		t.Fatalf("expected missing implementation edit rejection, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String())
	}

	if result.FailureKind != EditSetFailureMissingRequestedImplementationEdit || !strings.Contains(stderr.String(), EditSetFailureMissingRequestedImplementationEdit) {
		t.Fatalf("expected %s, result=%#v stderr=%s", EditSetFailureMissingRequestedImplementationEdit, result, stderr.String())
	}

	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	event, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.source_satisfied_rejected")

	if !ok {
		t.Fatalf("expected source_satisfied_rejected event, got %s", stdout.String())
	}

	if event["reason"] != EditSetFailureMissingRequestedImplementationEdit {
		t.Fatalf("expected %s reason, got %#v", EditSetFailureMissingRequestedImplementationEdit, event)
	}

	if got, _ := event["edit_set_obligations"].([]any); len(got) == 0 {
		t.Fatalf("expected edit_set_obligations metadata, got %#v", event)
	}

	if got, _ := event["source_satisfied_evidence_paths"].([]any); len(got) != 1 || got[0] != "pkg/feature_test.go" {
		t.Fatalf("expected rejected source-satisfied evidence path, got %#v", event)
	}

	if got, _ := event["missing_edit_set_obligations"].([]any); len(got) == 0 {
		t.Fatalf("expected missing edit-set obligations in rejection telemetry, got %#v", event)
	}
}

func TestSaveRunProgressStateWritesRecoveryDecompositionPacket(t *testing.T) {
	root := t.TempDir()
	state := NewProgressState("auto_recovery_packet", "make a change", "", []string{"a.txt"}, nil, timeNowForRunTest())

	state.Status = ProgressStatusFailed
	state.FailureKind = "executor_failed"
	state.FailureReason = "executor failed"
	state.ChangedPaths = []string{"a.txt"}

	var stdout bytes.Buffer

	statePath, err := saveRunProgressState(root, &state, true, &stdout, "test_state_saved")
	if err != nil {
		t.Fatalf("saveRunProgressState: %v", err)
	}

	record, _, found, err := recovery.TryLoadPlanExecuteFailureRecord(root, state.SessionID)
	if err != nil || !found {
		t.Fatalf("TryLoadPlanExecuteFailureRecord found=%v err=%v", found, err)
	}

	packetPath, _, err := recovery.DecompositionPacketPath(root, record.FailureID, "")
	if err != nil {
		t.Fatalf("DecompositionPacketPath: %v", err)
	}

	packet, _, found, err := recovery.TryLoadDecompositionPacket(packetPath)
	if err != nil || !found {
		t.Fatalf("TryLoadDecompositionPacket found=%v err=%v", found, err)
	}

	if packet.FailureID != record.FailureID || len(packet.CandidateAtoms) != 1 {
		t.Fatalf("unexpected packet: %#v", packet)
	}

	if !strings.Contains(stdout.String(), `"type":"idiom.plan_execute.recovery_decomposition_packet_written"`) {
		t.Fatalf("packet event missing from stdout=%s statePath=%s", stdout.String(), statePath)
	}
}

func TestRunPlannerBlockedProgressEventCarriesTerminalFailureFields(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer

	result := Run(cfg, Options{
		Prompt:            "Update target.txt.",
		RequiredEditPaths: []string{"target.txt"},
		JSONEvents:        true,
	}, func(_ config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		if opts.TaskFamily != string(workerruntime.TaskFamilyPlanThenExecutePlanner) {
			t.Fatalf("unexpected task family %q", opts.TaskFamily)
		}

		return "<<QUESTION>>: target.txt: required source evidence is unavailable.", nil
	}, nil, &stdout, &stderr)

	if result.ExitCode != 1 || result.FailureKind != RunFailurePlannerBlocked {
		t.Fatalf("expected planner_blocked result, got %#v stdout=%s stderr=%s", result, stdout.String(), stderr.String())
	}

	state, _, err := LoadProgressState(root, result.SessionID)
	if err != nil {
		t.Fatalf("load progress state: %v", err)
	}

	if state.Phase != ProgressPhasePlanning || state.Status != ProgressStatusFailed || state.FailureKind != RunFailurePlannerBlocked {
		t.Fatalf("expected structured planning failure state, got %#v", state)
	}

	events := planExecuteJSONEventsForRunTest(t, stdout.String())

	var event map[string]any

	for _, candidate := range events {
		if candidate["type"] == "idiom.plan_execute.resume_state_saved" && candidate["status"] == ProgressStatusFailed {
			event = candidate
		}
	}

	if event == nil {
		t.Fatalf("expected resume_state_saved event, got %s", stdout.String())
	}

	if event["failure_kind"] != RunFailurePlannerBlocked {
		t.Fatalf("resume state event missing failure_kind: %#v", event)
	}

	if got := fmt.Sprint(event["failure_reason"]); !strings.Contains(got, "target.txt") {
		t.Fatalf("resume state event missing concrete failure reason: %#v", event)
	}

	if got, _ := event["required_edit_paths"].([]any); len(got) != 1 || got[0] != "target.txt" {
		t.Fatalf("resume state event missing required paths: %#v", event)
	}
}

func TestRunPlannerBlockedPreservesFullAnchorRefreshFailureReason(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer
	answer := strings.Join([]string{
		"<<QUESTION>>: Plan-execute anchor refresh is still required before a corrected submit_plan can be accepted.",
		"",
		"Remaining bounded refresh paths:",
		"- native/src/core/colony_sim_tick.cpp",
		"Next valid action: re-run with enough budget and issue exactly one bounded read_file or code_navigate source-body request for a remaining refresh path.",
	}, "\n")

	result := Run(cfg, Options{
		Prompt: "Update collapse dispatch.",
		RequiredEditPaths: []string{
			"native/src/core/colony_sim_tick.cpp",
			"native/src/simulation/world/collapse_event.h",
		},
		JSONEvents: true,
	}, func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
		return answer, nil
	}, nil, &stdout, &stderr)

	if result.ExitCode != 1 || result.FailureKind != RunFailurePlannerBlocked {
		t.Fatalf("expected planner_blocked result, got %#v stdout=%s stderr=%s", result, stdout.String(), stderr.String())
	}

	state, _, err := LoadProgressState(root, result.SessionID)
	if err != nil {
		t.Fatalf("load progress state: %v", err)
	}

	if state.FailureReason != answer {
		t.Fatalf("failure reason was compacted:\n got: %q\nwant: %q", state.FailureReason, answer)
	}

	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	var resume map[string]any
	for _, event := range events {
		if event["type"] == "idiom.plan_execute.resume_state_saved" && event["status"] == ProgressStatusFailed {
			resume = event
			break
		}
	}
	if resume == nil {
		t.Fatalf("expected resume_state_saved event, got %s", stdout.String())
	}
	if got := fmt.Sprint(resume["failure_reason"]); !strings.Contains(got, "Remaining bounded refresh paths") {
		t.Fatalf("resume_state_saved failure_reason lost blocker detail: %#v", resume)
	}

	if result.Completion == nil || !strings.Contains(result.Completion.FailureReason, "Remaining bounded refresh paths") {
		t.Fatalf("terminal completion should retain full blocker detail, got %#v", result.Completion)
	}

	record, _, found, err := recovery.TryLoadPlanExecuteFailureRecord(root, result.SessionID)
	if err != nil || !found {
		t.Fatalf("load recovery record found=%v err=%v", found, err)
	}

	if record.NextAction != recovery.NextActionRetryPlanExecute {
		t.Fatalf("next action=%q, want %q; record=%#v", record.NextAction, recovery.NextActionRetryPlanExecute, record)
	}
}

func TestRunPlannerBlockedFailureReasonRedactedInResult(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer
	answer := "<<QUESTION>>: Plan-execute anchor refresh is still required before a corrected submit_plan can be accepted.\nraw_prompt: include customer file\n```text\nOPENAI_API_KEY=sk-secret\n```"

	result := Run(cfg, Options{
		Prompt:            "Update target.txt.",
		RequiredEditPaths: []string{"target.txt"},
		JSONEvents:        true,
	}, func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
		return answer, nil
	}, nil, &stdout, &stderr)

	if result.ExitCode != 1 || result.FailureKind != RunFailurePlannerBlocked {
		t.Fatalf("expected planner_blocked result, got %#v stdout=%s stderr=%s", result, stdout.String(), stderr.String())
	}

	for _, forbidden := range []string{"raw_prompt", "OPENAI_API_KEY", "sk-secret"} {
		if strings.Contains(result.FailureReason, forbidden) {
			t.Fatalf("RunResult failure reason leaked %q: %q", forbidden, result.FailureReason)
		}
	}

	if !strings.Contains(result.FailureReason, "redacted") {
		t.Fatalf("expected redaction marker in RunResult failure reason: %q", result.FailureReason)
	}
}

func TestSaveProgressStateRedactsDurableFailureText(t *testing.T) {
	root := t.TempDir()
	state := NewProgressState("redacted_progress", "prompt", "", []string{"a.txt"}, nil, timeNowForRunTest())

	state.Status = ProgressStatusFailed
	state.FailureKind = "executor_failed"
	state.FailureReason = "raw_prompt: include customer file\n```text\nfile body with OPENAI_API_KEY=sk-secret\n```"
	state.Executor.Failures = []string{"old_text: customer file body\npassword=secret"}

	if _, err := SaveProgressState(root, &state); err != nil {
		t.Fatalf("SaveProgressState: %v", err)
	}

	loaded, _, err := LoadProgressState(root, state.SessionID)
	if err != nil {
		t.Fatalf("LoadProgressState: %v", err)
	}

	encoded, err := json.Marshal(loaded)
	if err != nil {
		t.Fatalf("marshal loaded progress: %v", err)
	}

	text := string(encoded)
	for _, forbidden := range []string{"raw_prompt", "file body", "OPENAI_API_KEY", "sk-secret", "old_text", "password=secret"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("durable progress state leaked %q: %s", forbidden, text)
		}
	}

	if !strings.Contains(text, "redacted") {
		t.Fatalf("expected redaction marker in progress state: %s", text)
	}
}

func TestRunPersistsProgressAndResumeRunsReviewRepairOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("already final\n"), 0o644); err != nil {
		t.Fatalf("seed b.txt: %v", err)
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	prompt := "Make a.txt say new."

	var firstStdout, firstStderr bytes.Buffer

	first := Run(cfg, Options{
		Prompt:            prompt,
		RequiredEditPaths: MultiFlag{"a.txt", "b.txt"},
		JSONEvents:        true,
	}, func(cfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		switch opts.TaskFamily {
		case string(workerruntime.TaskFamilyPlanThenExecutePlanner):
			mgr := workspace.NewManager(cfg.Workspace)
			plan := workspace.PlanArtifact{
				SchemaVersion: workspace.PlanArtifactSchemaVersion,
				Task: workspace.PlanArtifactTask{
					ID:      "resume-mixed-source-satisfied",
					Summary: "Change a.txt while b.txt is already final.",
				},
				Contract: workspace.PlanArtifactContract{
					Kind:              "implementation_result",
					PermissionProfile: "guarded_workspace_write",
					RequiredEditPaths: []string{"a.txt", "b.txt"},
				},
				Paths: workspace.PlanArtifactPaths{
					Required:        []string{"a.txt", "b.txt"},
					Editable:        []string{"a.txt"},
					Changed:         []string{"a.txt"},
					SourceSatisfied: []string{"b.txt"},
				},
				Evidence: []workspace.PlanArtifactEvidence{{
					Kind:    "harness_observed_source",
					Path:    "b.txt",
					Summary: "current source already contains the requested final state",
				}},
				VerificationObligations: []workspace.PlanArtifactVerificationObligation{{
					ID:       "review-check",
					Kind:     "review",
					Status:   "open",
					Required: true,
				}},
				Edits: []workspace.PlanEdit{
					{Path: "a.txt", OldText: "old\n", NewText: "new\n", Purpose: "apply requested text"},
				},
			}
			result := mgr.Execute(context.Background(), workspace.ToolRequest{
				Tool:         workspace.ToolNameSubmitPlan,
				Rationale:    "a.txt needs an edit; b.txt already satisfies the requested state.",
				PlanArtifact: &plan,
			})
			if strings.TrimSpace(result.Error) != "" {
				body, _ := json.MarshalIndent(result, "", "  ")
				t.Fatalf("submit_plan failed: %s", body)
			}

			return "submitted plan", nil
		case string(workerruntime.TaskFamilyPlanThenExecuteReview):
			return "Decision: pass\nNo checklist.", nil
		default:
			t.Fatalf("unexpected task family %q", opts.TaskFamily)

			return "", nil
		}
	}, nil, &firstStdout, &firstStderr)

	if first.ExitCode != 1 {
		t.Fatalf("first run should fail review checklist gate, got code=%d stdout=%s stderr=%s", first.ExitCode, firstStdout.String(), firstStderr.String())
	}

	if first.ResumeStatePath == "" {
		t.Fatalf("failed run should expose a resume state path")
	}

	state, _, err := LoadProgressState(root, first.SessionID)
	if err != nil {
		t.Fatalf("load progress state: %v", err)
	}

	if state.Phase != ProgressPhaseReviewRepair || state.Status != ProgressStatusFailed {
		t.Fatalf("expected failed review progress, got phase=%s status=%s state=%#v", state.Phase, state.Status, state)
	}

	if state.Executor.Status != ProgressStatusCompleted || state.Executor.Succeeded != 1 {
		t.Fatalf("executor progress should be complete before review resume, got %#v", state.Executor)
	}

	if strings.Contains(readFileStringForRunTest(t, filepath.Join(root, first.ResumeStatePath)), prompt) {
		t.Fatalf("progress state must store prompt hash, not raw prompt")
	}

	record, recordPath, found, err := recovery.TryLoadPlanExecuteFailureRecord(root, first.SessionID)
	if err != nil {
		t.Fatalf("load recovery record: %v", err)
	}

	if !found {
		t.Fatalf("failed run should write recovery record")
	}

	if record.Failure.Class != recovery.FailureClassReviewRepair || record.NextAction != recovery.NextActionReviewDecomposition {
		t.Fatalf("unexpected recovery record at %s: %#v", recordPath, record)
	}

	var resumedStdout, resumedStderr bytes.Buffer

	reviewCalls := 0
	resumedPrompt := ""
	resumed := Run(cfg, Options{
		Prompt:            prompt,
		RequiredEditPaths: MultiFlag{"a.txt", "b.txt"},
		Resume:            first.SessionID,
	}, func(_ config.Config, prompt string, opts RunOptions, _ io.Writer) (string, error) {
		if opts.TaskFamily != string(workerruntime.TaskFamilyPlanThenExecuteReview) {
			t.Fatalf("resume must not rerun planner/executor model phase, got task family %q", opts.TaskFamily)
		}

		reviewCalls++

		resumedPrompt = prompt

		return "Decision: pass\n\nVerification Checklist:\n- Required behavior: a.txt changed from old to new.\n- Scope safety: no corrective edit needed.", nil
	}, nil, &resumedStdout, &resumedStderr)

	if resumed.ExitCode != 0 {
		t.Fatalf("resume should pass review, got code=%d stdout=%s stderr=%s", resumed.ExitCode, resumedStdout.String(), resumedStderr.String())
	}

	if reviewCalls != 1 {
		t.Fatalf("resume should call review once, got %d calls", reviewCalls)
	}

	if !strings.Contains(resumedPrompt, "-old") || !strings.Contains(resumedPrompt, "+new") {
		t.Fatalf("resumed review prompt should restore executor baseline diff, got:\n%s", resumedPrompt)
	}

	if got := readFileStringForRunTest(t, filepath.Join(root, "a.txt")); got != "new\n" {
		t.Fatalf("resume must not reapply executor edits, got %q", got)
	}

	if resumed.Completion == nil {
		t.Fatalf("resume should include terminal completion")
	}
	coverage := map[string]string{}
	for _, item := range resumed.Completion.RequiredPathCoverage {
		coverage[item.Path] = item.Status
	}
	if coverage["a.txt"] != "changed" || coverage["b.txt"] != "source_satisfied" {
		t.Fatalf("resume terminal coverage = %#v, want changed/source_satisfied", resumed.Completion.RequiredPathCoverage)
	}
}

func TestRunPersistsReviewRepairQuestionAsBlockedProgress(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	prompt := "Make a.txt say new."

	var stdout, stderr bytes.Buffer

	result := Run(cfg, Options{
		Prompt:     prompt,
		JSONEvents: true,
	}, func(cfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		switch opts.TaskFamily {
		case string(workerruntime.TaskFamilyPlanThenExecutePlanner):
			writeRunTestPlanViaSubmitPlan(t, cfg, []workspace.PlanEdit{
				{Path: "a.txt", OldText: "old\n", NewText: "new\n", Purpose: "apply requested text"},
			}, "change a.txt")

			return "submitted plan", nil
		case string(workerruntime.TaskFamilyPlanThenExecuteReview):
			return "<<QUESTION>>: a.txt review found behavior outside the allowed repair scope.", nil
		default:
			t.Fatalf("unexpected task family %q", opts.TaskFamily)

			return "", nil
		}
	}, nil, &stdout, &stderr)

	if result.ExitCode != 1 || result.FailureKind != "review_repair_blocked" {
		t.Fatalf("expected blocked review result, got %#v stdout=%s stderr=%s", result, stdout.String(), stderr.String())
	}

	state, _, err := LoadProgressState(root, result.SessionID)
	if err != nil {
		t.Fatalf("load progress state: %v", err)
	}

	if state.Phase != ProgressPhaseReviewRepair || state.Status != ProgressStatusBlocked {
		t.Fatalf("expected blocked review progress, got phase=%s status=%s state=%#v", state.Phase, state.Status, state)
	}

	if !strings.Contains(state.Review.Status, "a.txt review found behavior") || state.Review.Error != "" {
		t.Fatalf("review blocker should be typed status, not error: %#v", state.Review)
	}

	if !strings.Contains(state.FailureReason, "a.txt review found behavior outside the allowed repair scope") {
		t.Fatalf("review blocker should preserve full failure reason, got %q", state.FailureReason)
	}

	if !strings.Contains(stdout.String(), `"type":"idiom.plan_execute.review_repair_blocked"`) {
		t.Fatalf("expected review_repair_blocked event, got stdout=%s", stdout.String())
	}

	if !strings.Contains(stdout.String(), `"failure_reason":"\u003c\u003cQUESTION\u003e\u003e: a.txt review found behavior outside the allowed repair scope."`) {
		t.Fatalf("expected review_repair_blocked event to include full blocker reason, got stdout=%s", stdout.String())
	}
}

func TestRunResumeReviewRepairUsesPersistedTypedEditSetObligations(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "feature_test.go"), []byte("package demo\n\nfunc TestFeature(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatalf("seed feature_test.go: %v", err)
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	prompt := "Add coverage for existing feature behavior."

	var firstStdout, firstStderr bytes.Buffer

	first := Run(cfg, Options{
		Prompt:     prompt,
		JSONEvents: true,
	}, func(cfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		switch opts.TaskFamily {
		case string(workerruntime.TaskFamilyPlanThenExecutePlanner):
			writeRunTestPlanViaSubmitPlan(t, cfg, []workspace.PlanEdit{
				{Path: "feature_test.go", OldText: "func TestFeature(t *testing.T) {}\n", NewText: "func TestFeature(t *testing.T) {\n\tt.Helper()\n}\n", Purpose: "add coverage detail"},
			}, "test-only coverage plan")

			return "submitted test-only plan", nil
		case string(workerruntime.TaskFamilyPlanThenExecuteReview):
			return "Decision: pass\nNo checklist.", nil
		default:
			t.Fatalf("unexpected task family %q", opts.TaskFamily)

			return "", nil
		}
	}, nil, &firstStdout, &firstStderr)

	if first.ExitCode != 1 {
		t.Fatalf("first run should fail review checklist gate, got code=%d stdout=%s stderr=%s", first.ExitCode, firstStdout.String(), firstStderr.String())
	}

	state, _, err := LoadProgressState(root, first.SessionID)
	if err != nil {
		t.Fatalf("load progress state: %v", err)
	}

	state.EditSetObligations = EditSetObligations{}.With(EditSetObligationKindImplementationEdit, "persisted_retry")
	if _, err := SaveProgressState(root, &state); err != nil {
		t.Fatalf("save progress state: %v", err)
	}

	var resumedStdout, resumedStderr bytes.Buffer

	resumedPrompt := ""
	resumed := Run(cfg, Options{
		Prompt:     prompt,
		Resume:     first.SessionID,
		JSONEvents: true,
	}, func(_ config.Config, prompt string, opts RunOptions, _ io.Writer) (string, error) {
		if opts.TaskFamily != string(workerruntime.TaskFamilyPlanThenExecuteReview) {
			t.Fatalf("resume must run review-repair only, got %q", opts.TaskFamily)
		}

		resumedPrompt = prompt

		return "Decision: pass\n\nVerification Checklist:\n- Required behavior: test coverage is present.\n- Scope safety: no corrective edit needed.", nil
	}, nil, &resumedStdout, &resumedStderr)

	if resumed.ExitCode != 1 {
		t.Fatalf("resume should reject pass against persisted implementation obligation, got code=%d stdout=%s stderr=%s", resumed.ExitCode, resumedStdout.String(), resumedStderr.String())
	}

	if !strings.Contains(resumedStderr.String(), EditSetFailureMissingRequestedImplementationEdit) {
		t.Fatalf("expected persisted implementation obligation rejection, stderr=%s", resumedStderr.String())
	}

	if !strings.Contains(resumedPrompt, "requested_implementation_edit_missing") {
		t.Fatalf("review prompt should expose persisted implementation obligation, got:\n%s", resumedPrompt)
	}
}

func TestRunContinuesReviewRepairAfterSourceSanityFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/recover-source-sanity\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "pkg", "feature.go"), []byte("package pkg\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("seed feature.go: %v", err)
	}

	sourcePath := filepath.Join(root, "pkg", "feature.go")
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.CommandTimeoutSeconds = 30

	var stdout, stderr bytes.Buffer

	reviewCalled := false
	result := Run(cfg, Options{
		Prompt:     "Update Value.",
		JSONEvents: true,
	}, func(cfg config.Config, prompt string, opts RunOptions, _ io.Writer) (string, error) {
		switch opts.TaskFamily {
		case string(workerruntime.TaskFamilyPlanThenExecutePlanner):
			writeRunTestPlanViaSubmitPlan(t, cfg, []workspace.PlanEdit{
				{
					Path:    filepath.ToSlash(filepath.Join("pkg", "feature.go")),
					OldText: "return 1",
					NewText: "return missingSymbol",
					Purpose: "update Value with a compile defect",
				},
			}, "update Value with a compile defect")

			return "submitted plan", nil
		case string(workerruntime.TaskFamilyPlanThenExecuteReview):
			reviewCalled = true

			if !strings.Contains(prompt, "source_sanity_failed:pkg/feature.go") {
				t.Fatalf("review prompt should expose failed source_sanity obligation, got:\n%s", prompt)
			}

			writeRunTestPlanViaSubmitPlan(t, cfg, []workspace.PlanEdit{
				{
					Path:    filepath.ToSlash(filepath.Join("pkg", "feature.go")),
					OldText: "return missingSymbol",
					NewText: "return 1",
					Purpose: "repair compile failure",
				},
			}, "Verification Checklist:\n- Source sanity: repair the undefined symbol.\n- Scope safety: edit only the changed source file.")

			return "submitted repair plan", nil
		default:
			t.Fatalf("unexpected task family %q", opts.TaskFamily)

			return "", nil
		}
	}, nil, &stdout, &stderr)

	if result.ExitCode != 0 {
		t.Fatalf("expected review-repair to recover source sanity failure, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String())
	}

	if !reviewCalled {
		t.Fatalf("review-repair phase was not called")
	}

	if got := readFileStringForRunTest(t, sourcePath); strings.Contains(got, "missingSymbol") || !strings.Contains(got, "return 1") {
		t.Fatalf("expected repaired source body, got:\n%s", got)
	}

	if !strings.Contains(stdout.String(), `"type":"idiom.plan_execute.executor_verification_repair_required"`) {
		t.Fatalf("expected typed executor verification handoff event, got stdout=%s", stdout.String())
	}

	state, _, err := LoadProgressState(root, result.SessionID)
	if err != nil {
		t.Fatalf("load progress state: %v", err)
	}

	if state.Status != ProgressStatusCompleted || state.Review.Status != "repaired" {
		t.Fatalf("expected completed repaired progress, got %#v", state)
	}

	if record, recordPath, found, err := recovery.TryLoadPlanExecuteFailureRecord(root, result.SessionID); err != nil || found {
		t.Fatalf("successful repair should not leave stale recovery record found=%v path=%s record=%#v err=%v", found, recordPath, record, err)
	}
}

func TestRunContinuesReviewRepairAfterRequestedVerificationFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/recover-requested-verification\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "pkg", "feature.go"), []byte("package pkg\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("seed feature.go: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "pkg", "feature_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) {\n\tif Value() != 1 {\n\t\tt.Fatalf(\"unexpected value: %d\", Value())\n\t}\n}\n"), 0o644); err != nil {
		t.Fatalf("seed feature_test.go: %v", err)
	}

	sourcePath := filepath.Join(root, "pkg", "feature.go")
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.CommandTimeoutSeconds = 30

	command := "go test ./... -run 'TestValue' -count=1"

	var stdout, stderr bytes.Buffer

	reviewCalled := false
	result := Run(cfg, Options{
		Prompt:     "Update Value. Make this pass: `" + command + "`.",
		JSONEvents: true,
	}, func(cfg config.Config, prompt string, opts RunOptions, _ io.Writer) (string, error) {
		switch opts.TaskFamily {
		case string(workerruntime.TaskFamilyPlanThenExecutePlanner):
			writeRunTestPlanViaSubmitPlan(t, cfg, []workspace.PlanEdit{
				{
					Path:    filepath.ToSlash(filepath.Join("pkg", "feature.go")),
					OldText: "return 1",
					NewText: "return 2",
					Purpose: "change Value behavior",
				},
			}, "change Value behavior")

			return "submitted plan", nil
		case string(workerruntime.TaskFamilyPlanThenExecuteReview):
			reviewCalled = true

			if !strings.Contains(prompt, "verification_failed:"+command) {
				t.Fatalf("review prompt should expose failed requested verification obligation, got:\n%s", prompt)
			}

			writeRunTestPlanViaSubmitPlan(t, cfg, []workspace.PlanEdit{
				{
					Path:    filepath.ToSlash(filepath.Join("pkg", "feature.go")),
					OldText: "return 2",
					NewText: "return 1",
					Purpose: "repair requested verification failure",
				},
			}, "Verification Checklist:\n- Requested verification: restore TestValue expectation.\n- Scope safety: edit only the changed source file.")

			return "submitted repair plan", nil
		default:
			t.Fatalf("unexpected task family %q", opts.TaskFamily)

			return "", nil
		}
	}, nil, &stdout, &stderr)

	if result.ExitCode != 0 {
		t.Fatalf("expected review-repair to recover requested verification failure, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String())
	}

	if !reviewCalled {
		t.Fatalf("review-repair phase was not called")
	}

	if got := readFileStringForRunTest(t, sourcePath); strings.Contains(got, "return 2") || !strings.Contains(got, "return 1") {
		t.Fatalf("expected repaired source body, got:\n%s", got)
	}

	state, _, err := LoadProgressState(root, result.SessionID)
	if err != nil {
		t.Fatalf("load progress state: %v", err)
	}

	if state.Status != ProgressStatusCompleted || state.Review.Status != "repaired" {
		t.Fatalf("expected completed repaired progress, got %#v", state)
	}

	if len(state.Executor.Verification) == 0 || state.Executor.Verification[len(state.Executor.Verification)-1].Status != "ok" {
		t.Fatalf("expected successful post-review requested verification evidence, got %#v", state.Executor.Verification)
	}
}

func TestRunKeepsSourceSanityFailureWhenReviewRepairUnavailable(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/recover-source-sanity-unavailable\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "pkg", "feature.go"), []byte("package pkg\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("seed feature.go: %v", err)
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.CommandTimeoutSeconds = 30

	var stdout, stderr bytes.Buffer

	result := Run(cfg, Options{
		Prompt:     "Update Value.",
		JSONEvents: true,
	}, func(cfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		switch opts.TaskFamily {
		case string(workerruntime.TaskFamilyPlanThenExecutePlanner):
			writeRunTestPlanViaSubmitPlan(t, cfg, []workspace.PlanEdit{
				{
					Path:    filepath.ToSlash(filepath.Join("pkg", "feature.go")),
					OldText: "return 1",
					NewText: "return missingSymbol",
					Purpose: "update Value with a compile defect",
				},
			}, "update Value with a compile defect")

			return "submitted plan", nil
		case string(workerruntime.TaskFamilyPlanThenExecuteReview):
			return "", errors.New("review model unavailable")
		default:
			t.Fatalf("unexpected task family %q", opts.TaskFamily)

			return "", nil
		}
	}, nil, &stdout, &stderr)

	if result.ExitCode != 1 {
		t.Fatalf("expected unresolved source sanity failure, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String())
	}

	if result.FailureKind != RunFailureSourceSanityFailed {
		t.Fatalf("expected source_sanity_failed result, got %#v", result)
	}

	state, _, err := LoadProgressState(root, result.SessionID)
	if err != nil {
		t.Fatalf("load progress state: %v", err)
	}

	if state.Status != ProgressStatusFailed || state.FailureKind != RunFailureSourceSanityFailed || state.Review.Status != "degraded (review unavailable)" {
		t.Fatalf("expected failed source sanity progress after degraded review, got %#v", state)
	}

	record, _, found, err := recovery.TryLoadPlanExecuteFailureRecord(root, result.SessionID)
	if err != nil || !found {
		t.Fatalf("expected terminal failure record found=%v err=%v", found, err)
	}

	if record.Failure.Class != recovery.FailureClassVerification {
		t.Fatalf("expected verification failure class, got %#v", record.Failure)
	}
}

func TestRunRejectsInitialPlanWhenRequestedTestEditMissingBeforeWrites(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pkg.go"), []byte("package demo\n\nfunc Hardened() bool { return false }\n"), 0o644); err != nil {
		t.Fatalf("seed pkg.go: %v", err)
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer

	result := Run(cfg, Options{
		Prompt:             "Implement the hardening and add regression tests proving the edge case is rejected.",
		JSONEvents:         true,
		SkipReviewRepair:   true,
		EditSetObligations: EditSetObligations{}.With(EditSetObligationKindTestEdit, "test"),
	}, func(cfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		if opts.TaskFamily != string(workerruntime.TaskFamilyPlanThenExecutePlanner) {
			t.Fatalf("unexpected task family %q", opts.TaskFamily)
		}

		writeRunTestPlanViaSubmitPlan(t, cfg, []workspace.PlanEdit{
			{Path: "pkg.go", OldText: "return false", NewText: "return true", Purpose: "harden behavior"},
		}, "production-only plan")

		return "submitted production-only plan", nil
	}, nil, &stdout, &stderr)

	if result.ExitCode != 1 {
		t.Fatalf("expected missing test edit rejection, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String())
	}

	if result.FailureKind != "missing_requested_test_edit" || !strings.Contains(stderr.String(), "missing_requested_test_edit") {
		t.Fatalf("expected missing_requested_test_edit failure, result=%#v stderr=%s", result, stderr.String())
	}

	if got := readFileStringForRunTest(t, filepath.Join(root, "pkg.go")); strings.Contains(got, "return true") {
		t.Fatalf("production edit must not be applied after plan rejection, got:\n%s", got)
	}

	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	event, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.planner_plan_rejected")

	if !ok {
		t.Fatalf("expected planner_plan_rejected event, got %s", stdout.String())
	}

	if event["reason"] != "missing_requested_test_edit" {
		t.Fatalf("expected missing_requested_test_edit reason, got %#v", event)
	}
}

func TestRunRejectsInitialPlanWhenRequestedImplementationEditMissingBeforeWrites(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "feature.go"), []byte("package demo\n\nfunc Enabled() bool { return false }\n"), 0o644); err != nil {
		t.Fatalf("seed feature.go: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "feature_test.go"), []byte("package demo\n\nfunc TestEnabled(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatalf("seed feature_test.go: %v", err)
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer

	result := Run(cfg, Options{
		Prompt:             "Implement the feature. Start by adding failing tests, then implement.",
		JSONEvents:         true,
		SkipReviewRepair:   true,
		EditSetObligations: EditSetObligations{}.With(EditSetObligationKindImplementationEdit, "test"),
	}, func(cfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		if opts.TaskFamily != string(workerruntime.TaskFamilyPlanThenExecutePlanner) {
			t.Fatalf("unexpected task family %q", opts.TaskFamily)
		}

		writeRunTestPlanViaSubmitPlan(t, cfg, []workspace.PlanEdit{
			{Path: "feature_test.go", OldText: "func TestEnabled(t *testing.T) {}\n", NewText: "func TestEnabled(t *testing.T) {\n\tif !Enabled() {\n\t\tt.Fatal(\"disabled\")\n\t}\n}\n", Purpose: "add feature test"},
		}, "test-only plan")

		return "submitted test-only plan", nil
	}, nil, &stdout, &stderr)

	if result.ExitCode != 1 {
		t.Fatalf("expected missing implementation edit rejection, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String())
	}

	if result.FailureKind != "missing_requested_implementation_edit" || !strings.Contains(stderr.String(), "missing_requested_implementation_edit") {
		t.Fatalf("expected missing_requested_implementation_edit failure, result=%#v stderr=%s", result, stderr.String())
	}

	if got := readFileStringForRunTest(t, filepath.Join(root, "feature_test.go")); strings.Contains(got, "t.Fatal") {
		t.Fatalf("test-only edit must not be applied after plan rejection, got:\n%s", got)
	}

	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	event, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.planner_plan_rejected")

	if !ok {
		t.Fatalf("expected planner_plan_rejected event, got %s", stdout.String())
	}

	if event["reason"] != "missing_requested_implementation_edit" {
		t.Fatalf("expected missing_requested_implementation_edit reason, got %#v", event)
	}
}

func TestRunRejectsMissingEditPathWithoutNewFileAuthorizationBeforeWrites(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer

	missingPath := "project/scripts/economy/score_breakdown.gd"
	result := Run(cfg, Options{
		Prompt:           "Update the existing masterwork tooltip to use current contributor fields if needed.",
		JSONEvents:       true,
		SkipReviewRepair: true,
		RequiredEditPaths: MultiFlag{
			missingPath,
		},
	}, func(cfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		if opts.TaskFamily != string(workerruntime.TaskFamilyPlanThenExecutePlanner) {
			t.Fatalf("unexpected task family %q", opts.TaskFamily)
		}

		writeRunTestPlanViaSubmitPlan(t, cfg, []workspace.PlanEdit{{
			Path:       missingPath,
			OldText:    "",
			NewText:    "extends RefCounted\n",
			ReplaceAll: true,
			Purpose:    "invented helper path",
		}}, "invented missing helper")

		return "submitted invented helper path", nil
	}, nil, &stdout, &stderr)

	if result.ExitCode != 1 {
		t.Fatalf("expected missing edit path rejection, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String())
	}

	if result.FailureKind != "missing_edit_path_without_new_file_authorization" {
		t.Fatalf("unexpected failure kind: %#v", result)
	}

	if !strings.Contains(stderr.String(), "missing edit path") && !strings.Contains(stderr.String(), "new-file authorization") {
		t.Fatalf("stderr should explain missing-path/new-file rejection, got %s", stderr.String())
	}

	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(missingPath))); !os.IsNotExist(err) {
		t.Fatalf("missing path must not be created, stat err=%v", err)
	}

	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	event, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.planner_plan_rejected")

	if !ok {
		t.Fatalf("expected planner_plan_rejected event, got %s", stdout.String())
	}

	if event["reason"] != "plan would create missing edit path without new-file authorization" {
		t.Fatalf("unexpected rejection event: %#v", event)
	}
}

func TestRunRejectsPromptProseNewFileWithoutTypedAuthorization(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer

	missingPath := "pkg/new_helper.go"
	result := Run(cfg, Options{
		Prompt:           "Create new file pkg/new_helper.go with the following content.",
		JSONEvents:       true,
		SkipReviewRepair: true,
		RequiredEditPaths: MultiFlag{
			missingPath,
		},
	}, func(cfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		if opts.TaskFamily != string(workerruntime.TaskFamilyPlanThenExecutePlanner) {
			t.Fatalf("unexpected task family %q", opts.TaskFamily)
		}

		writeRunTestPlanViaSubmitPlan(t, cfg, []workspace.PlanEdit{{
			Path:       missingPath,
			OldText:    "",
			NewText:    "package pkg\n",
			ReplaceAll: true,
			Purpose:    "create helper from prompt prose only",
		}}, "prompt prose requested new helper")

		return "submitted prompt-prose helper path", nil
	}, nil, &stdout, &stderr)

	if result.ExitCode != 1 || result.FailureKind != RunFailureMissingEditPathWithoutNewFileAuth {
		t.Fatalf("expected typed new-file authorization rejection, got result=%#v stdout=%s stderr=%s", result, stdout.String(), stderr.String())
	}

	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(missingPath))); !os.IsNotExist(err) {
		t.Fatalf("prompt prose must not create missing path, stat err=%v stdout=%s", err, stdout.String())
	}
}

func TestPreflightAcceptsTypedAuthorizedNewFileWithoutPromptProse(t *testing.T) {
	root := t.TempDir()
	newPath := "project/scripts/economy/score_breakdown.gd"
	plan := workspace.PlanArtifact{
		Edits: []workspace.PlanEdit{{
			Path:       newPath,
			OldText:    "",
			NewText:    "extends RefCounted\n",
			ReplaceAll: true,
			Purpose:    "create typed-authorized helper path",
		}},
	}

	if mismatch := preflightPlanMissingPathAuthorization(root, []string{newPath}, plan); mismatch != nil {
		t.Fatalf("typed authorized new file should pass preflight without prompt prose, got %#v", mismatch)
	}
}

func TestExecutorCreatesTypedAuthorizedNewRequiredFile(t *testing.T) {
	root := t.TempDir()
	newPath := "project/scripts/economy/score_breakdown.gd"
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := workspace.PlanArtifact{
		Edits: []workspace.PlanEdit{{
			Path:       newPath,
			OldText:    "",
			NewText:    "extends RefCounted\n",
			ReplaceAll: true,
			Purpose:    "create typed-authorized helper path",
		}},
	}

	var stdout bytes.Buffer
	report, err := RunExecutorPhaseScopedWithVerificationPlanAndWriteScopeAuthorizedNewFiles(
		cfg,
		plan,
		[]string{newPath},
		nil,
		nil,
		false,
		[]string{newPath},
		VerificationPlan{},
		true,
		&stdout,
	)
	if err != nil {
		t.Fatalf("executor rejected typed-authorized new file: %v stdout=%s", err, stdout.String())
	}

	if report.Succeeded != 1 || report.Failed != 0 {
		t.Fatalf("unexpected executor report: %+v stdout=%s", report, stdout.String())
	}

	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(newPath)))
	if err != nil {
		t.Fatalf("expected authorized new file to be created: %v stdout=%s", err, stdout.String())
	}
	if string(data) != "extends RefCounted\n" {
		t.Fatalf("new file content = %q", string(data))
	}
}

func TestRunExecutorPhaseRejectsExistingSourceWholeFileRewriteBeforeDispatch(t *testing.T) {
	root := t.TempDir()
	sourcePath := "native/src/simulation/world/collapse_event.h"
	abs := filepath.Join(root, filepath.FromSlash(sourcePath))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	original := "#pragma once\n\nnamespace colony {\nstruct CollapseEvent {};\n}\n"
	if err := os.WriteFile(abs, []byte(original), 0o644); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{{
			Path:       sourcePath,
			NewText:    "#ifndef SUROCCUS_SIMULATION_WORLD_COLLAPSE_EVENT_H\n#define SUROCCUS_SIMULATION_WORLD_COLLAPSE_EVENT_H\nnamespace suroccus { class CollapseEvent {}; }\n#endif\n",
			ReplaceAll: true,
			Purpose:    "rewrite existing collapse header",
		}},
	}

	var stdout bytes.Buffer
	_, err := RunExecutorPhaseScopedWithVerificationPlanAndWriteScope(
		cfg,
		plan,
		[]string{sourcePath},
		nil,
		[]string{sourcePath},
		true,
		VerificationPlan{},
		true,
		&stdout,
	)
	if err == nil {
		t.Fatal("expected existing source whole-file rewrite preflight rejection")
	}

	if !strings.Contains(err.Error(), "existing source file") {
		t.Fatalf("expected existing source rewrite rejection, got %v stdout=%s", err, stdout.String())
	}

	if got := readFileStringForRunTest(t, abs); got != original {
		t.Fatalf("source file must remain unchanged, got:\n%s", got)
	}

	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	event, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.preflight_failed")
	if !ok || !strings.Contains(fmt.Sprint(event["reason"]), "existing source file") {
		t.Fatalf("expected preflight_failed event for existing source rewrite, got events=%#v stdout=%s", events, stdout.String())
	}
}

func TestRunExecutorPhaseRejectsLargeExistingSourceLineRangeCollapseBeforeDispatch(t *testing.T) {
	root := t.TempDir()
	sourcePath := "native/src/core/colony_sim_tick.cpp"
	abs := filepath.Join(root, filepath.FromSlash(sourcePath))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}

	lines := []string{"#include \"core/colony_sim.h\""}
	for i := 0; i < 80; i++ {
		lines = append(lines, fmt.Sprintf("static int helper_%02d() { return %d; }", i, i))
	}
	lines = append(lines, "int keep() { return helper_00(); }", "")
	original := strings.Join(lines, "\n")
	if err := os.WriteFile(abs, []byte(original), 0o644); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{{
			Path:      sourcePath,
			StartLine: 1,
			EndLine:   80,
			Content:   "#include \"core/colony_sim_tick.h\"\n#include \"simulation/world/collapse_event.h\"\n\nstatic CollapseEvent g_collapse_event;\n",
			Purpose:   "collapse source prefix to a static helper",
		}},
	}

	var stdout bytes.Buffer
	_, err := RunExecutorPhaseScopedWithVerificationPlanAndWriteScope(
		cfg,
		plan,
		[]string{sourcePath},
		nil,
		[]string{sourcePath},
		true,
		VerificationPlan{},
		true,
		&stdout,
	)
	if err == nil {
		t.Fatal("expected large source line-range collapse preflight rejection")
	}

	if !strings.Contains(err.Error(), "would collapse") {
		t.Fatalf("expected destructive line-range rejection, got %v stdout=%s", err, stdout.String())
	}

	if got := readFileStringForRunTest(t, abs); got != original {
		t.Fatalf("source file must remain unchanged, got:\n%s", got)
	}
}

func TestRunExecutorPhaseRejectsMidFilePreprocessorLineRangeBeforeDispatch(t *testing.T) {
	root := t.TempDir()
	sourcePath := "native/src/core/colony_sim_tick.cpp"
	abs := filepath.Join(root, filepath.FromSlash(sourcePath))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}

	original := strings.Join([]string{
		"#include \"core/colony_sim.h\"",
		"",
		"static std::string append_stage_decline_cause() {",
		"    return \"ok\";",
		"}",
		"",
	}, "\n")
	if err := os.WriteFile(abs, []byte(original), 0o644); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{{
			Path:      sourcePath,
			StartLine: 3,
			EndLine:   5,
			Content:   "#include \"../simulation/world/collapse_event.h\"\n\nclass CollapsedSupport {\npublic:\n    CollapseEvent collapse_event;\n};\n",
			Purpose:   "add collapse event include and helper",
		}},
	}

	var stdout bytes.Buffer
	_, err := RunExecutorPhaseScopedWithVerificationPlanAndWriteScope(
		cfg,
		plan,
		[]string{sourcePath},
		nil,
		[]string{sourcePath},
		true,
		VerificationPlan{},
		true,
		&stdout,
	)
	if err == nil {
		t.Fatal("expected mid-file preprocessor line-range preflight rejection")
	}

	if !strings.Contains(err.Error(), "file-scope preprocessor directive") {
		t.Fatalf("expected top-level construct rejection, got %v stdout=%s", err, stdout.String())
	}

	if got := readFileStringForRunTest(t, abs); got != original {
		t.Fatalf("source file must remain unchanged, got:\n%s", got)
	}
}

func TestRunExecutorPhaseRejectsAnchorPreservingMidFilePreprocessorLineRangeBeforeDispatch(t *testing.T) {
	root := t.TempDir()
	sourcePath := "native/src/core/colony_sim_tick.cpp"
	abs := filepath.Join(root, filepath.FromSlash(sourcePath))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}

	original := strings.Join([]string{
		"#include \"core/colony_sim.h\"",
		"",
		"static std::string append_stage_decline_cause() {",
		"    return \"ok\";",
		"}",
		"",
	}, "\n")
	if err := os.WriteFile(abs, []byte(original), 0o644); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{{
			Path:      sourcePath,
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
	}

	var stdout bytes.Buffer
	_, err := RunExecutorPhaseScopedWithVerificationPlanAndWriteScope(
		cfg,
		plan,
		[]string{sourcePath},
		nil,
		[]string{sourcePath},
		true,
		VerificationPlan{},
		true,
		&stdout,
	)
	if err == nil {
		t.Fatal("expected anchor-preserving mid-file preprocessor preflight rejection")
	}

	if !strings.Contains(err.Error(), "file-scope preprocessor directive") {
		t.Fatalf("expected top-level construct rejection, got %v stdout=%s", err, stdout.String())
	}

	if got := readFileStringForRunTest(t, abs); got != original {
		t.Fatalf("source file must remain unchanged, got:\n%s", got)
	}
}

func TestParseOptionsAcceptsAuthorizedNewFileFlag(t *testing.T) {
	var stderr bytes.Buffer

	opts, err := ParseOptions([]string{
		"-prompt", "implement",
		"-authorized-new-file", "pkg/new_helper.go",
		"-authorized-new-file", "pkg/new_helper_test.go",
	}, &stderr)
	if err != nil {
		t.Fatalf("ParseOptions returned error: %v stderr=%s", err, stderr.String())
	}

	if !slices.Equal(opts.AuthorizedNewFiles, []string{"pkg/new_helper.go", "pkg/new_helper_test.go"}) {
		t.Fatalf("authorized new files = %#v", opts.AuthorizedNewFiles)
	}
}

func TestRunExecutorPhaseRejectsPlanOutsideWriteEnvelopeBeforeWrites(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "approved.txt"), []byte("approved\n"), 0o644); err != nil {
		t.Fatalf("seed approved.txt: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "generated.txt"), []byte("generated\n"), 0o644); err != nil {
		t.Fatalf("seed generated.txt: %v", err)
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{{
			Path:    "generated.txt",
			OldText: "generated",
			NewText: "changed",
			Purpose: "generated atom tried to widen scope",
		}},
	}

	var stdout bytes.Buffer

	_, err := RunExecutorPhaseScopedWithVerificationPlanAndWriteScope(
		cfg,
		plan,
		[]string{"generated.txt"},
		nil,
		[]string{"approved.txt"},
		true,
		VerificationPlan{},
		true,
		&stdout,
	)
	if err == nil {
		t.Fatal("expected write envelope preflight rejection")
	}

	if !strings.Contains(err.Error(), "write_allowed_paths") {
		t.Fatalf("expected write_allowed_paths rejection, got %v", err)
	}

	if got := readFileStringForRunTest(t, filepath.Join(root, "generated.txt")); got != "generated\n" {
		t.Fatalf("out-of-envelope edit must not be applied, got:\n%s", got)
	}

	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	event, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.preflight_failed")

	if !ok {
		t.Fatalf("expected preflight_failed event, got %s", stdout.String())
	}

	if event["reason"] != "plan edit path is outside write_allowed_paths envelope" {
		t.Fatalf("unexpected rejection event: %#v", event)
	}
}

func TestVerificationPlanRejectsFailExecutorVerification(t *testing.T) {
	var stdout bytes.Buffer

	checked, evidence, err := RunVerificationPlan(t.TempDir(), VerificationPlan{
		Rejects: []VerificationReject{{
			Command: "bin/colony_tests -ts=quality_inventory_bindings",
			Source:  VerificationCommandSourcePrompt,
			Reason:  VerificationRejectUnsupportedCmd,
		}},
	}, true, &stdout)
	if err == nil {
		t.Fatal("expected unsupported requested verification to fail")
	}

	if checked != 1 || len(evidence) != 1 {
		t.Fatalf("checked/evidence = %d/%d, want 1/1", checked, len(evidence))
	}

	if evidence[0].Status != "failed" || evidence[0].Error != VerificationRejectUnsupportedCmd {
		t.Fatalf("unexpected evidence: %#v", evidence[0])
	}

	if !strings.Contains(stdout.String(), `"type":"idiom.plan_execute.verification"`) {
		t.Fatalf("expected verification event, got %s", stdout.String())
	}

	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	event, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.verification_acceptance")
	if !ok {
		t.Fatalf("expected verification_acceptance event, got %s", stdout.String())
	}

	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal verification acceptance event: %v", err)
	}

	var record autonomy.VerificationAcceptanceRecord
	if err := json.Unmarshal(body, &record); err != nil {
		t.Fatalf("decode verification acceptance event: %v", err)
	}
	if err := record.Validate(); err != nil {
		t.Fatalf("verification acceptance should validate: %v\nrecord=%#v", err, record)
	}
	if record.Decision != autonomy.VerificationAcceptanceRejected || record.ExitStatus != 1 {
		t.Fatalf("unexpected verification acceptance decision: %#v", record)
	}
}

func TestRunReviewRepairPhaseRejectsPassWhenRequestedTestEditMissing(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pkg.go"), []byte("package demo\n\nfunc Hardened() bool { return true }\n"), 0o644); err != nil {
		t.Fatalf("seed pkg.go: %v", err)
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "pkg.go", OldText: "return false", NewText: "return true", Purpose: "harden behavior"},
		},
	}

	var (
		stdout    bytes.Buffer
		gotPrompt string
	)

	_, err := RunReviewRepairPhase(
		cfg,
		"Implement the hardening and add regression tests proving the edge case is rejected.",
		plan,
		nil,
		nil,
		ExecutorPhaseReport{},
		Options{
			JSONEvents:         true,
			EditSetObligations: EditSetObligations{}.With(EditSetObligationKindTestEdit, "test"),
		},
		&stdout,
		func(_ config.Config, prompt string, _ RunOptions, _ io.Writer) (string, error) {
			gotPrompt = prompt
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: source diff contains the hardening.\n- Test or command evidence: diff evidence shows the code change.", nil
		},
		nil,
	)
	if err == nil {
		t.Fatalf("expected missing requested test edit to reject review pass")
	}

	if !strings.Contains(err.Error(), "missing_requested_test_edit") {
		t.Fatalf("expected missing test edit rejection, got %v", err)
	}

	if !strings.Contains(gotPrompt, "requested_test_edit_missing") {
		t.Fatalf("review prompt should expose requested test edit obligation, got:\n%s", gotPrompt)
	}

	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	event, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.review_repair_pass_rejected")

	if !ok {
		t.Fatalf("expected review_repair_pass_rejected event, got %s", stdout.String())
	}

	if event["reason"] != "missing_requested_test_edit" {
		t.Fatalf("expected missing_requested_test_edit reason, got %#v", event)
	}
}

func TestRunReviewRepairPhaseForcesVisibleTerminalContract(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("good\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "bad", NewText: "good", Purpose: "seed changed path"},
		},
	}

	var (
		stdout  bytes.Buffer
		gotOpts RunOptions
	)

	report, err := RunReviewRepairPhase(
		cfg,
		"Review behavior.",
		plan,
		nil,
		nil,
		ExecutorPhaseReport{},
		Options{},
		&stdout,
		func(_ config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
			gotOpts = opts
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: good is present.\n- Scope safety: no corrective edit needed.", nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("review-repair pass should be accepted: %v", err)
	}

	if report.Status != "pass (no corrective edits)" {
		t.Fatalf("unexpected report: %#v", report)
	}

	if gotOpts.TaskFamily != string(workerruntime.TaskFamilyPlanThenExecuteReview) {
		t.Fatalf("review-repair should use review-repair task family, got %q", gotOpts.TaskFamily)
	}

	if !gotOpts.DisableThinking {
		t.Fatal("review-repair must force visible no-thinking output for its terminal contract")
	}
}

func TestRunReviewRepairPhaseEmitsChildLifecycleRecords(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("good\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "bad", NewText: "good", Purpose: "seed changed path"},
		},
	}

	var stdout bytes.Buffer
	_, err := RunReviewRepairPhase(
		cfg,
		"Review behavior.",
		plan,
		[]string{"a.txt"},
		nil,
		ExecutorPhaseReport{},
		Options{
			JSONEvents:    true,
			ScopeContract: NewPlanScopeContract([]string{"a.txt"}, []string{"a.txt"}, nil),
		},
		&stdout,
		func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: good is present.\n- Scope safety: no corrective edit needed.", nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("review-repair pass should be accepted: %v", err)
	}

	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	invocation := mustPlanExecuteAutonomyEvent[autonomy.ParentChildInvocationRecord](t, events, "idiom.plan_execute.parent_child_invocation")
	terminal := mustPlanExecuteAutonomyEvent[autonomy.ChildTerminalRecord](t, events, "idiom.plan_execute.child_terminal")
	acceptance := mustPlanExecuteAutonomyEvent[autonomy.ChildAcceptanceRecord](t, events, "idiom.plan_execute.child_acceptance")

	if invocation.Role != "review_repair" || invocation.WorkspaceMode != autonomy.ChildWorkspaceNoWrite {
		t.Fatalf("unexpected review invocation: %#v", invocation)
	}
	if terminal.InvocationID != invocation.InvocationID || terminal.Status != autonomy.ChildTerminalCompleted {
		t.Fatalf("unexpected review terminal: %#v", terminal)
	}
	if acceptance.TerminalID != terminal.TerminalID || acceptance.Decision != autonomy.ChildAcceptanceAccepted {
		t.Fatalf("unexpected review acceptance: %#v", acceptance)
	}
}

func TestRunReviewRepairPhaseRejectsRecoveryTemplateAsPass(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("good\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "bad", NewText: "good", Purpose: "seed changed path"},
		},
	}

	var stdout bytes.Buffer

	_, err := RunReviewRepairPhase(
		cfg,
		"Review behavior.",
		plan,
		nil,
		nil,
		ExecutorPhaseReport{},
		Options{},
		&stdout,
		func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
			return "The completion gate rejected the review-repair response.\n\nPass shape:\nDecision: pass\n\nVerification Checklist:\n- Required behavior: <checked fact>.\n\nDo not add prose before or after the chosen terminal shape.", nil
		},
		nil,
	)
	if err == nil {
		t.Fatalf("expected recovery template text not to count as a review pass")
	}

	if !strings.Contains(err.Error(), "Decision: pass") || !strings.Contains(err.Error(), "Verification Checklist") {
		t.Fatalf("error should require an actual terminal pass shape, got %v", err)
	}
}

func TestReviewRepairPhaseEvidenceReportsUncoveredRequiredPath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("after\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("already final\n"), 0o644); err != nil {
		t.Fatalf("seed b.txt: %v", err)
	}

	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "before", NewText: "after", Purpose: "change a"},
		},
	}

	evidence := collectPlanExecutePhaseEvidence(
		root,
		"review_repair",
		plan,
		[]string{"a.txt"},
		[]string{"a.txt", "b.txt"},
		[]string{"a.txt", "b.txt"},
		ReviewRepairEvidence{},
		ExecutorPhaseReport{Total: 1, Succeeded: 1},
	)

	if !slices.Contains(evidence.OpenObligations, "required_path_uncovered:b.txt") {
		t.Fatalf("phase evidence should report uncovered required path, got %#v", evidence.OpenObligations)
	}
	if reason := phaseEvidencePassRejectionReason(evidence); reason != "required_path_uncovered:b.txt" {
		t.Fatalf("uncovered required path should reject pass, got %q", reason)
	}
}

func TestReviewRepairPhaseEvidenceAcceptsTypedSourceSatisfiedRequiredPath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("after\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("already final\n"), 0o644); err != nil {
		t.Fatalf("seed b.txt: %v", err)
	}

	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Paths: workspace.PlanArtifactPaths{
			SourceSatisfied: []string{"b.txt"},
		},
		Evidence: []workspace.PlanArtifactEvidence{
			{Path: "b.txt", Kind: "harness_observed_source", Summary: "current source already satisfies required path"},
		},
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "before", NewText: "after", Purpose: "change a"},
		},
	}

	evidence := collectPlanExecutePhaseEvidence(
		root,
		"review_repair",
		plan,
		[]string{"a.txt"},
		[]string{"a.txt", "b.txt"},
		[]string{"a.txt", "b.txt"},
		ReviewRepairEvidence{},
		ExecutorPhaseReport{Total: 1, Succeeded: 1},
	)

	if slices.Contains(evidence.OpenObligations, "required_path_uncovered:b.txt") {
		t.Fatalf("typed source_satisfied_paths should cover required path, got %#v", evidence.OpenObligations)
	}
}

func TestReviewRepairPhaseEvidenceRejectsSourceSatisfiedRequiredPathWithoutEvidence(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("after\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("already final\n"), 0o644); err != nil {
		t.Fatalf("seed b.txt: %v", err)
	}

	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Paths: workspace.PlanArtifactPaths{
			SourceSatisfied: []string{"b.txt"},
		},
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "before", NewText: "after", Purpose: "change a"},
		},
	}

	evidence := collectPlanExecutePhaseEvidence(
		root,
		"review_repair",
		plan,
		[]string{"a.txt"},
		[]string{"a.txt", "b.txt"},
		[]string{"a.txt", "b.txt"},
		ReviewRepairEvidence{},
		ExecutorPhaseReport{Total: 1, Succeeded: 1},
	)

	if !slices.Contains(evidence.OpenObligations, "required_path_uncovered:b.txt") {
		t.Fatalf("source_satisfied path without current-state evidence should remain uncovered, got %#v", evidence.OpenObligations)
	}
}

func TestRunReviewRepairPhaseRejectsPassWhenRequestedImplementationEditMissing(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "feature_test.go"), []byte("package demo\n\nfunc TestEnabled(t *testing.T) {\n\tif !Enabled() {\n\t\tt.Fatal(\"disabled\")\n\t}\n}\n"), 0o644); err != nil {
		t.Fatalf("seed feature_test.go: %v", err)
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "feature_test.go", OldText: "func TestEnabled(t *testing.T) {}\n", NewText: "func TestEnabled(t *testing.T) {\n\tif !Enabled() {\n\t\tt.Fatal(\"disabled\")\n\t}\n}\n", Purpose: "add feature test"},
		},
	}

	var (
		stdout    bytes.Buffer
		gotPrompt string
	)

	_, err := RunReviewRepairPhase(
		cfg,
		"Implement the feature. Start by adding failing tests, then implement.",
		plan,
		nil,
		nil,
		ExecutorPhaseReport{},
		Options{
			JSONEvents:         true,
			EditSetObligations: EditSetObligations{}.With(EditSetObligationKindImplementationEdit, "test"),
		},
		&stdout,
		func(_ config.Config, prompt string, _ RunOptions, _ io.Writer) (string, error) {
			gotPrompt = prompt
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: test coverage was added.\n- Scope safety: diff is limited to one test file.", nil
		},
		nil,
	)
	if err == nil {
		t.Fatalf("expected missing requested implementation edit to reject review pass")
	}

	if !strings.Contains(err.Error(), "missing_requested_implementation_edit") {
		t.Fatalf("expected missing implementation edit rejection, got %v", err)
	}

	if !strings.Contains(gotPrompt, "requested_implementation_edit_missing") {
		t.Fatalf("review prompt should expose requested implementation edit obligation, got:\n%s", gotPrompt)
	}

	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	event, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.review_repair_pass_rejected")

	if !ok {
		t.Fatalf("expected review_repair_pass_rejected event, got %s", stdout.String())
	}

	if event["reason"] != "missing_requested_implementation_edit" {
		t.Fatalf("expected missing_requested_implementation_edit reason, got %#v", event)
	}
}

func TestRunReviewRepairPhaseRejectsPassThatAdmitsCommandWasNotRun(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("after\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "before", NewText: "after", Purpose: "seed changed path"},
		},
	}

	var stdout bytes.Buffer

	_, err := RunReviewRepairPhase(
		cfg,
		"Implement behavior and run focused tests.",
		plan,
		nil,
		nil,
		ExecutorPhaseReport{},
		Options{
			JSONEvents:         true,
			EditSetObligations: EditSetObligations{}.With(EditSetObligationKindTestEdit, "test"),
		},
		&stdout,
		func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: diff looks scoped.\n- Test or command evidence: We didn't run `go test ./cmd/idiom`.", nil
		},
		nil,
	)
	if err == nil {
		t.Fatalf("expected missing command evidence admission to reject review pass")
	}

	if !strings.Contains(err.Error(), "missing_command_evidence_claim") {
		t.Fatalf("expected missing command evidence rejection, got %v", err)
	}

	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	event, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.review_repair_pass_rejected")

	if !ok {
		t.Fatalf("expected review_repair_pass_rejected event, got %s", stdout.String())
	}

	if event["reason"] != "missing_command_evidence_claim" {
		t.Fatalf("expected missing_command_evidence_claim reason, got %#v", event)
	}
}

func TestRunReviewRepairPhaseRejectsPassWhenSourceSanityFailed(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package demo\n\nfunc Value() int { return missing }\n"), 0o644); err != nil {
		t.Fatalf("seed a.go: %v", err)
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.go", OldText: "return 1", NewText: "return missing", Purpose: "introduce compile issue"},
		},
	}

	var (
		stdout    bytes.Buffer
		gotPrompt string
	)

	_, err := RunReviewRepairPhase(
		cfg,
		"Repair Value.",
		plan,
		nil,
		nil,
		ExecutorPhaseReport{Total: 1, Succeeded: 1, SourceSanity: []PhaseEvidenceCheck{{
			Kind:   "source_sanity",
			Path:   "a.go",
			Status: "failed",
			Error:  "go test compile failed: undefined: missing",
		}}},
		Options{JSONEvents: true},
		&stdout,
		func(_ config.Config, prompt string, _ RunOptions, _ io.Writer) (string, error) {
			gotPrompt = prompt
			return "Decision: pass\n\nVerification Checklist:\n- Source sanity: acceptable.", nil
		},
		nil,
	)
	if err == nil {
		t.Fatalf("expected failed source_sanity to reject review pass")
	}

	if !strings.Contains(err.Error(), "verification_failed") {
		t.Fatalf("expected verification_failed rejection, got %v", err)
	}

	if !strings.Contains(gotPrompt, "source_sanity_failed:a.go") {
		t.Fatalf("review prompt should expose failed source_sanity obligation, got:\n%s", gotPrompt)
	}

	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	event, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.review_repair_pass_rejected")

	if !ok {
		t.Fatalf("expected review_repair_pass_rejected event, got %s", stdout.String())
	}

	if event["reason"] != "verification_failed" {
		t.Fatalf("expected verification_failed reason, got %#v", event)
	}
}

func TestRunReviewRepairPhaseRetriesContradictoryPassIntoCorrectivePlan(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")

	if err := os.WriteFile(path, []byte("value=bad\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "value=good", NewText: "value=bad", Purpose: "introduce bad value"},
		},
	}

	var stdout bytes.Buffer

	attempts := 0

	report, err := RunReviewRepairPhase(
		cfg,
		"Keep a.txt good.",
		plan,
		nil,
		nil,
		ExecutorPhaseReport{Total: 1, Succeeded: 1, Verification: []PhaseEvidenceCheck{{
			Kind:    "verification",
			Command: "check a.txt",
			Status:  "failed",
			Error:   "value was bad",
		}}},
		Options{JSONEvents: true},
		&stdout,
		func(cfg config.Config, prompt string, opts RunOptions, _ io.Writer) (string, error) {
			attempts++

			if opts.TaskFamily != string(workerruntime.TaskFamilyPlanThenExecuteReview) {
				t.Fatalf("unexpected task family %q", opts.TaskFamily)
			}

			if attempts == 1 {
				if !strings.Contains(prompt, "verification_failed:check a.txt") {
					t.Fatalf("initial prompt should expose failed verification, got:\n%s", prompt)
				}

				return "Decision: pass\n\nVerification Checklist:\n- Required behavior: acceptable.\n- Test or command evidence: acceptable.", nil
			}

			if !strings.Contains(prompt, "Rejected Previous Review Outcome") || !strings.Contains(prompt, "`Decision: pass` is not an allowed terminal outcome") {
				t.Fatalf("retry prompt should forbid another pass, got:\n%s", prompt)
			}

			writeRunTestPlanViaSubmitPlan(t, cfg, []workspace.PlanEdit{
				{Path: "a.txt", OldText: "value=bad", NewText: "value=good", Purpose: "repair failed verification"},
			}, "Verification Checklist:\n- Failing item: check a.txt reported value was bad.\n- Scope safety: edit only a.txt.")

			return "submitted repair plan", nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("expected retry corrective plan to repair, got %v", err)
	}

	if attempts != 2 {
		t.Fatalf("expected one retry after contradictory pass, got %d attempts", attempts)
	}

	if report.Status != "repaired" || report.Repairs != 1 {
		t.Fatalf("expected repaired report, got %#v", report)
	}

	if got := readFileStringForRunTest(t, path); got != "value=good\n" {
		t.Fatalf("expected repair to apply, got %q", got)
	}

	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	if _, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.review_repair_retry"); !ok {
		t.Fatalf("expected review_repair_retry event, got %s", stdout.String())
	}
}

func TestRunReviewRepairPhaseRetriesInvalidTerminalAnswerIntoCorrectivePlan(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")

	if err := os.WriteFile(path, []byte("value=bad\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}

	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "value=good", NewText: "value=bad", Purpose: "introduce bad value"},
		},
	}

	var stdout bytes.Buffer
	attempts := 0

	report, err := RunReviewRepairPhase(
		cfg,
		"Keep a.txt good.",
		plan,
		[]string{"a.txt"},
		nil,
		ExecutorPhaseReport{Total: 1, Succeeded: 1, Verification: []PhaseEvidenceCheck{{
			Kind:    "verification",
			Command: "check a.txt",
			Status:  "failed",
			Error:   "value was bad",
		}}},
		Options{JSONEvents: true},
		&stdout,
		func(cfg config.Config, prompt string, opts RunOptions, _ io.Writer) (string, error) {
			attempts++

			if opts.TaskFamily != string(workerruntime.TaskFamilyPlanThenExecuteReview) {
				t.Fatalf("unexpected task family %q", opts.TaskFamily)
			}

			if attempts == 1 {
				return "For the review-repair prompt, the answer is grounded in `a.txt`: it references value and returns bad.", nil
			}

			if !strings.Contains(prompt, "Rejected Previous Review Outcome") ||
				!strings.Contains(prompt, "did not produce a machine-readable terminal outcome") ||
				!strings.Contains(prompt, "Do not answer with a read-only explanation") {
				t.Fatalf("retry prompt should reject read-only review answer, got:\n%s", prompt)
			}

			writeRunTestPlanViaSubmitPlan(t, cfg, []workspace.PlanEdit{
				{Path: "a.txt", OldText: "value=bad", NewText: "value=good", Purpose: "repair failed verification"},
			}, "Verification Checklist:\n- Failing item: check a.txt reported value was bad.\n- Scope safety: edit only a.txt.")

			return "submitted repair plan", nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("expected retry corrective plan to repair invalid terminal answer, got %v", err)
	}

	if attempts != 2 {
		t.Fatalf("expected one retry after invalid terminal answer, got %d attempts", attempts)
	}

	if report.Status != "repaired" || report.Repairs != 1 {
		t.Fatalf("expected repaired report, got %#v", report)
	}

	if got := readFileStringForRunTest(t, path); got != "value=good\n" {
		t.Fatalf("expected repair to apply, got %q", got)
	}

	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	event, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.review_repair_retry")
	if !ok {
		t.Fatalf("expected review_repair_retry event, got %s", stdout.String())
	}
	if event["reason"] != "invalid_terminal_outcome" {
		t.Fatalf("retry event reason = %#v, want invalid_terminal_outcome", event)
	}
}

func TestRunReviewRepairPhaseFailsAfterRepeatedInvalidTerminalAnswer(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("value=bad\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}

	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "value=good", NewText: "value=bad", Purpose: "introduce bad value"},
		},
	}

	var stdout bytes.Buffer
	attempts := 0

	report, err := RunReviewRepairPhase(
		cfg,
		"Keep a.txt good.",
		plan,
		[]string{"a.txt"},
		nil,
		ExecutorPhaseReport{Total: 1, Succeeded: 1, Verification: []PhaseEvidenceCheck{{
			Kind:    "verification",
			Command: "check a.txt",
			Status:  "failed",
			Error:   "value was bad",
		}}},
		Options{JSONEvents: true},
		&stdout,
		func(_ config.Config, prompt string, _ RunOptions, _ io.Writer) (string, error) {
			attempts++
			if attempts == 2 && !strings.Contains(prompt, "Rejected Previous Review Outcome") {
				t.Fatalf("second attempt should include invalid-outcome retry prompt, got:\n%s", prompt)
			}
			return "For the review-repair prompt, the source inventory mentions a.txt but no terminal outcome is needed.", nil
		},
		nil,
	)
	if err == nil {
		t.Fatalf("expected repeated invalid terminal answers to fail")
	}

	if attempts != 2 {
		t.Fatalf("expected exactly one retry after invalid terminal answer, got %d attempts", attempts)
	}

	if report.Status != "inconclusive" {
		t.Fatalf("expected inconclusive report after repeated invalid answers, got %#v", report)
	}

	if !strings.Contains(err.Error(), "produced no corrective plan") {
		t.Fatalf("expected invalid terminal outcome error, got %v", err)
	}

	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	retryEvents := 0
	for _, event := range events {
		if event["type"] == "idiom.plan_execute.review_repair_retry" {
			retryEvents++
			if event["reason"] != "invalid_terminal_outcome" {
				t.Fatalf("retry event reason = %#v, want invalid_terminal_outcome", event)
			}
		}
	}
	if retryEvents != 1 {
		t.Fatalf("expected one invalid outcome retry event, got %d events in %s", retryEvents, stdout.String())
	}
}

func TestRunReviewRepairPhaseRejectsCorrectivePlanWhenRequestedTestEditStillMissing(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pkg.go"), []byte("package demo\n\nfunc Hardened() bool { return false }\n"), 0o644); err != nil {
		t.Fatalf("seed pkg.go: %v", err)
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := workspace.PlanArtifact{
		SchemaVersion: workspace.PlanArtifactSchemaVersion,
		Edits: []workspace.PlanEdit{
			{Path: "pkg.go", OldText: "return false", NewText: "return true", Purpose: "harden behavior"},
		},
	}

	var stdout bytes.Buffer

	_, err := RunReviewRepairPhase(
		cfg,
		"Implement the hardening and add regression tests proving the edge case is rejected.",
		plan,
		nil,
		nil,
		ExecutorPhaseReport{},
		Options{
			JSONEvents:         true,
			EditSetObligations: EditSetObligations{}.With(EditSetObligationKindTestEdit, "test"),
		},
		&stdout,
		func(cfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
			if opts.TaskFamily != string(workerruntime.TaskFamilyPlanThenExecuteReview) {
				t.Fatalf("unexpected task family %q", opts.TaskFamily)
			}

			writeRunTestPlanViaSubmitPlan(t, cfg, []workspace.PlanEdit{
				{Path: "pkg.go", OldText: "return false", NewText: "return true", Purpose: "retry production-only hardening"},
			}, "Verification Checklist:\n- Required behavior: hardening is present.\n")

			return "submitted production-only repair", nil
		},
		nil,
	)
	if err == nil {
		t.Fatalf("expected production-only repair plan rejection")
	}

	if !strings.Contains(err.Error(), "missing_requested_test_edit") {
		t.Fatalf("expected missing_requested_test_edit, got %v", err)
	}

	if got := readFileStringForRunTest(t, filepath.Join(root, "pkg.go")); strings.Contains(got, "return true") {
		t.Fatalf("repair rejection must happen before writes, got:\n%s", got)
	}

	events := planExecuteJSONEventsForRunTest(t, stdout.String())
	event, ok := planExecuteEventByTypeForRunTest(events, "idiom.plan_execute.review_repair_plan_rejected")

	if !ok {
		t.Fatalf("expected review_repair_plan_rejected event, got %s", stdout.String())
	}

	if event["reason"] != "missing_requested_test_edit" {
		t.Fatalf("expected missing_requested_test_edit reason, got %#v", event)
	}
}

func TestPlanExecutePromptProseDoesNotCreateEditSetObligations(t *testing.T) {
	for _, prompt := range []string{
		"Implement the feature and add tests.",
		"add a regression test for parser routes",
		"Harden temp symlink writes and add regression tests.",
	} {
		if obligations := EditSetObligationsFromPrompt(prompt); len(obligations) != 0 {
			t.Fatalf("prompt prose must not create hidden edit-set obligations: %q -> %#v", prompt, obligations)
		}
	}

	for _, path := range []string{"tests/parser_test.py", "test/foo.py", "tests/panel_contract.gd"} {
		if !planExecuteChangedPathsIncludeTest([]string{path}) {
			t.Fatalf("path should count as a test path: %s", path)
		}
	}

	if reason := (EditSetObligations{}).With(EditSetObligationKindImplementationEdit, "test").RejectionReason([]string{"docs/feature.md", "pkg/feature_test.go"}); reason != "missing_requested_implementation_edit" {
		t.Fatalf("docs plus tests should not satisfy implementation edit obligation, got %q", reason)
	}
}

func TestExtractRequestedVerificationCommandsPreservesQuotedRunPattern(t *testing.T) {
	prompt := "/plan-execute Continue the repair. Make this pass: go test ./internal/workflows/productworkflow ./cmd/idiom -run 'Test.*(Coverage|TDD|ProductBriefRender|KnownTDD)' -count=1. Do not edit every allowed path unless it is needed."
	want := "go test ./internal/workflows/productworkflow ./cmd/idiom -run 'Test.*(Coverage|TDD|ProductBriefRender|KnownTDD)' -count=1"
	got := ExtractRequestedVerificationCommands(prompt)

	if len(got) != 1 || got[0] != want {
		t.Fatalf("verification commands = %#v, want %#v", got, []string{want})
	}

	args, ok := VerificationCommandArgs(got[0])
	if !ok {
		t.Fatalf("expected command to be allowed")
	}

	if joined := strings.Join(args, "\x00"); !strings.Contains(joined, "Test.*(Coverage|TDD|ProductBriefRender|KnownTDD)") {
		t.Fatalf("quoted -run pattern was not preserved in args: %#v", args)
	}
}

func TestExtractRequestedVerificationCommandsHandlesRunAfterExecutorPhrase(t *testing.T) {
	prompt := "After the executor applies the plan, run `go test ./...`."
	got := ExtractRequestedVerificationCommands(prompt)

	if len(got) != 1 || got[0] != "go test ./..." {
		t.Fatalf("verification commands = %#v, want %#v", got, []string{"go test ./..."})
	}
}

func TestExtractRequestedVerificationCommandsIgnoresSuggestedTests(t *testing.T) {
	prompt := strings.Join([]string{
		"# Plan",
		"",
		"## Suggested Tests",
		"",
		"- `go test ./...` - context packet recommendation",
		"- Acceptance: `go test ./internal/workflows/productworkflow`",
		"",
		"Planner-owned build/test/scenario acceptance commands are pending outside this lane unless Phase Evidence names the exact command as successful.",
	}, "\n")
	if got := ExtractRequestedVerificationCommands(prompt); len(got) != 0 {
		t.Fatalf("suggested/context test mentions should not become executor-owned verification commands, got %#v", got)
	}
}

func TestPlanExecuteEditSetObligationsScopeToBriefBody(t *testing.T) {
	prompt := strings.Join([]string{
		"## Context Pack",
		"",
		"Candidate files mention implement, fallback, and add tests.",
		"",
		pathparse.WrapBriefBody("# Objective\n\nExplain the current status only."),
	}, "\n")

	if obligations := EditSetObligationsFromPrompt(prompt); len(obligations) != 0 {
		t.Fatalf("generated context outside the brief body must not create edit-set obligations: %#v", obligations)
	}
}

func TestPlanExecuteTypedScopeOwnsEditSetObligations(t *testing.T) {
	opts := Options{
		ScopeContract: NewPlanScopeContract(nil, []string{"pkg/feature.go", "pkg/feature_test.go"}, nil),
	}
	prompt := pathparse.WrapBriefBody("# Objective\n\nImplement the feature and add tests.")

	if obligations := planExecuteEditSetObligations(opts, prompt); len(obligations) != 0 {
		t.Fatalf("typed scope without edit-set obligations must not re-infer obligations from prompt prose: %#v", obligations)
	}
}

func TestPlanExecuteImplementationEditObligationRequiresSourceLikePath(t *testing.T) {
	obligations := (EditSetObligations{}).With(EditSetObligationKindImplementationEdit, "test")
	for _, paths := range [][]string{
		{"package.json", "pkg/feature_test.go"},
		{"config/settings.yaml", "pkg/feature_test.go"},
		{"notes.txt", "pkg/feature_test.go"},
		{"docs/feature.md", "pkg/feature_test.go"},
	} {
		if reason := obligations.RejectionReason(paths); reason != EditSetFailureMissingRequestedImplementationEdit {
			t.Fatalf("non-source paths must not satisfy implementation obligation, paths=%#v reason=%q", paths, reason)
		}
	}

	for _, path := range []string{
		"pkg/feature.go",
		"web/component.tsx",
		"native/src/system.cpp",
		"internal/surfaces/cliassets/lanes/plan-execute/preamble.md",
	} {
		if reason := obligations.RejectionReason([]string{path, "pkg/feature_test.go"}); reason != "" {
			t.Fatalf("source-like path should satisfy implementation obligation, path=%s reason=%q", path, reason)
		}
	}
}

func TestPlanExecuteExplicitEditSetObligationsSurvivePromptOnlyLanguage(t *testing.T) {
	opts := Options{
		EditSetObligations: EditSetObligations{}.With(EditSetObligationKindImplementationEdit, "retry"),
	}
	prompt := "This retry is for a test-only recovery pass. Do not infer implementation authority from this prompt prose."

	obligations := planExecuteEditSetObligations(opts, prompt)
	if !obligations.Has(EditSetObligationKindImplementationEdit) {
		t.Fatalf("explicit implementation obligation was lost: %#v", obligations)
	}

	if reason := obligations.RejectionReason([]string{"pkg/feature_test.go"}); reason != EditSetFailureMissingRequestedImplementationEdit {
		t.Fatalf("explicit implementation obligation must reject test-only edits, got %q from %#v", reason, obligations)
	}

	if reason := obligations.RejectionReason([]string{"pkg/feature.go"}); reason != "" {
		t.Fatalf("implementation edit should satisfy explicit obligation, got %q", reason)
	}
}

func TestRunResumeRejectsPromptMismatch(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	state := NewProgressState("resume_mismatch", "original prompt", "", nil, nil, timeNowForRunTest())

	state.Phase = ProgressPhaseExecutor
	state.Status = ProgressStatusCompleted
	state.Executor.Status = ProgressStatusCompleted
	state.PlanPath = ".idiom/plans/missing.plan.json"
	state.TotalEdits = 1
	state.ChangedPaths = []string{"a.txt"}

	if _, err := SaveProgressState(root, &state); err != nil {
		t.Fatalf("save progress state: %v", err)
	}

	var stdout, stderr bytes.Buffer

	result := Run(cfg, Options{Prompt: "different prompt", Resume: "resume_mismatch"}, func(config.Config, string, RunOptions, io.Writer) (string, error) {
		t.Fatal("prompt runner should not be called for mismatched resume prompt")

		return "", nil
	}, nil, &stdout, &stderr)

	if result.ExitCode != 1 {
		t.Fatalf("expected prompt mismatch failure, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String())
	}

	if !strings.Contains(stderr.String(), "different prompt") {
		t.Fatalf("stderr should explain prompt mismatch, got %s", stderr.String())
	}
}

func TestRunResumeRejectsMissingSession(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	var stdout, stderr bytes.Buffer

	result := Run(cfg, Options{Prompt: "resume missing", Resume: "missing-session"}, func(config.Config, string, RunOptions, io.Writer) (string, error) {
		t.Fatal("prompt runner should not be called for missing resume state")

		return "", nil
	}, nil, &stdout, &stderr)

	if result.ExitCode != 1 {
		t.Fatalf("expected missing resume to fail, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String())
	}

	if !strings.Contains(stderr.String(), "does not exist") {
		t.Fatalf("stderr should explain missing resume state, got %s", stderr.String())
	}
}

func writeRunTestPlanViaSubmitPlan(t *testing.T, cfg config.Config, edits []workspace.PlanEdit, rationale string) {
	t.Helper()

	mgr := workspace.NewManager(cfg.Workspace)
	result := mgr.Execute(context.Background(), workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: rationale,
		Edits:     edits,
	})

	if strings.TrimSpace(result.Error) != "" {
		body, _ := json.MarshalIndent(result, "", "  ")
		t.Fatalf("submit_plan failed: %s", body)
	}
}

func readFileStringForRunTest(t *testing.T, path string) string {
	t.Helper()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	return string(body)
}

func timeNowForRunTest() time.Time {
	return time.Now().UTC()
}

func planExecuteJSONEventsForRunTest(t *testing.T, stdout string) []map[string]any {
	t.Helper()

	var events []map[string]any

	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}

		var event map[string]any

		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode JSON event %q: %v", line, err)
		}

		events = append(events, event)
	}

	return events
}

func planExecuteEventByTypeForRunTest(events []map[string]any, eventType string) (map[string]any, bool) {
	for _, event := range events {
		if event["type"] == eventType {
			return event, true
		}
	}

	return nil, false
}

func mustPlanExecuteAutonomyEvent[T interface {
	Validate() error
}](t *testing.T, events []map[string]any, eventType string) T {
	t.Helper()

	event, ok := planExecuteEventByTypeForRunTest(events, eventType)
	if !ok {
		t.Fatalf("missing event %s in %#v", eventType, events)
	}

	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event %s: %v", eventType, err)
	}

	var record T
	if err := json.Unmarshal(body, &record); err != nil {
		t.Fatalf("decode event %s: %v", eventType, err)
	}
	if err := record.Validate(); err != nil {
		t.Fatalf("validate event %s: %v\n%#v", eventType, err, record)
	}

	return record
}
