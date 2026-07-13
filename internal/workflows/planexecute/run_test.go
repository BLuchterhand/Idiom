package planexecute

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	planartifact "idiom/internal/contracts/planartifact"
	recoverycontract "idiom/internal/contracts/recovery"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/contracts/pathparse"
	plancontract "idiom/internal/contracts/plan"
	queuecontract "idiom/internal/contracts/queue"
	"idiom/internal/platform/config"
	"idiom/internal/platform/jsonevents/jsoneventstest"
	"idiom/internal/repository/comprehension"
	"idiom/internal/workflows/recovery"
)

type planExecuteFailingWriter struct {
	err error
}

func (w planExecuteFailingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

var _ = ginkgo.It("run emits queue admission before planner", func() {
	root := ginkgo.GinkgoT().TempDir()
	ginkgo.GinkgoT().
		Setenv("IDIOM_QUEUE_POOL", "qwen-35b")
	ginkgo.GinkgoT().
		Setenv("IDIOM_QUEUE_SOCKET", filepath.Join(root, "missing-runner.sock"))

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	var stdout, stderr bytes.Buffer

	plannerCalled := false
	result := runForTest(cfg, Options{
		Prompt:     "plan this change",
		JSONEvents: true,
	}, func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
		plannerCalled = true

		return "no plan", nil
	}, nil, &stdout, &stderr)
	gomega.Expect(plannerCalled).To(gomega.BeFalse(), fmt.Sprint("planner prompt runner should not be called when required queue admission fails"))
	gomega.Expect(result.ExitCode != 1).To(gomega.BeFalse(), fmt.Sprintf("Run exit=%d, want queue admission failure; stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String()))
	gomega.Expect(strings.Contains(stdout.String(), `"type":"`+queuecontract.CommandAdmissionEventName(queuecontract.CommandEventPrefixPlanExecute, queuecontract.AdmissionEventQueueUnavailable)+`"`)).To(gomega.BeTrue(), fmt.Sprintf("queue admission event should be emitted before dispatch stops, stdout=%s", stdout.String()))
	gomega.Expect(strings.Contains(stdout.String(), queuecontract.CommandAdmissionEventName(queuecontract.CommandEventPrefixPlanExecute, queuecontract.AdmissionEventQueueAcquired))).To(gomega.BeFalse(), fmt.Sprintf("missing daemon should not be reported as acquired, stdout=%s", stdout.String()))
})

var _ = ginkgo.It("run stops before planner on json event write failure", func() {
	root := ginkgo.GinkgoT().TempDir()
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	wantErr := errors.New("stdout closed")
	var stderr bytes.Buffer
	plannerCalled := false

	result := runForTest(cfg, Options{
		Prompt:     "plan this change",
		JSONEvents: true,
	}, func(config.Config, string, RunOptions, io.Writer) (string, error) {
		plannerCalled = true
		return "no plan", nil
	}, nil, planExecuteFailingWriter{err: wantErr}, &stderr)
	gomega.Expect(plannerCalled).To(gomega.BeFalse(), fmt.Sprint("planner should not run after a command-owned JSON event write failure"))
	gomega.Expect(result.ExitCode).To(gomega.Equal(1), fmt.Sprintf("result = exit %d kind %q reason %q", result.ExitCode, result.FailureKind, result.FailureReason))
	gomega.Expect(result.FailureKind).To(gomega.Equal(string(plancontract.PlanExecuteFailureJSONEventWriteFailed)), fmt.Sprintf("result = exit %d kind %q reason %q", result.ExitCode, result.FailureKind, result.FailureReason))
	gomega.Expect(strings.Contains(result.FailureReason, wantErr.Error())).To(gomega.BeTrue(), fmt.Sprintf("FailureReason = %q, want writer error", result.FailureReason))
})

var _ = ginkgo.It("run executor phase returns json event write failure", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed a.txt: %v", err))
	}

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	_, err := runExecutorPhaseForTest(cfg, planartifact.PlanArtifact{
		Edits: []planartifact.PlanEdit{{
			Path:    "a.txt",
			OldText: "before\n",
			NewText: "after\n",
			Purpose: "update fixture text",
		}},
	}, ExecutorPhaseOptions{
		JSONEvents: true,
		Stdout:     planExecuteFailingWriter{err: errors.New("stdout closed")},
	})
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("RunExecutorPhase error = %v, want json event write failure", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring(string(plancontract.PlanExecuteFailureJSONEventWriteFailed)), fmt.Sprintf("RunExecutorPhase error = %v, want json event write failure", err))

	body, err := os.ReadFile(filepath.Join(root, "a.txt"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read a.txt: %v", err))
	gomega.Expect(string(body) != "before\n").To(gomega.BeFalse(), fmt.Sprintf("executor should stop before mutation on event failure, got %q", string(body)))
})

var _ = ginkgo.It("save run progress state returns json event write failure", func() {
	root := ginkgo.GinkgoT().TempDir()
	state := NewProgressState("progress-json-failure", "prompt", "", nil, nil, time.Now().UTC())

	_, err := saveRunProgressState(root, &state, true, planExecuteFailingWriter{err: errors.New("stdout closed")}, plancontract.PlanExecuteEventResumeStateSaved)
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("saveRunProgressState error = %v, want json event write failure", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring(string(plancontract.PlanExecuteFailureJSONEventWriteFailed)), fmt.Sprintf("saveRunProgressState error = %v, want json event write failure", err))
})

var _ = ginkgo.It("finalize plan execute run result returns json event write failure", func() {
	root := ginkgo.GinkgoT().TempDir()
	state := NewProgressState("terminal-json-failure", "prompt", "", nil, nil, time.Now().UTC())
	state.Phase = plancontract.PlanExecuteProgressPhaseComplete
	state.Status = plancontract.PlanExecuteProgressStatusCompleted

	result := finalizePlanExecuteRunResult(root, state, ".idiom/plan-execute/sessions/terminal-json-failure.json", true, planExecuteFailingWriter{err: errors.New("stdout closed")}, planExecuteRunResultInput{ExitCode: 0})
	gomega.Expect(result.ExitCode).To(gomega.Equal(1), fmt.Sprintf("result = exit %d kind %q reason %q", result.ExitCode, result.FailureKind, result.FailureReason))
	gomega.Expect(result.FailureKind).To(gomega.Equal(string(plancontract.PlanExecuteFailureJSONEventWriteFailed)), fmt.Sprintf("result = exit %d kind %q reason %q", result.ExitCode, result.FailureKind, result.FailureReason))
	gomega.Expect(strings.Contains(result.FailureReason, "stdout closed")).To(gomega.BeTrue(), fmt.Sprintf("FailureReason = %q, want writer error", result.FailureReason))
})

var _ = ginkgo.It("progress state preserves json event write failure kind", func() {
	state := NewProgressState("json-failure-progress", "prompt", "", nil, nil, time.Now().UTC())
	err := planExecuteJSONEventWriteError(errors.New("stdout closed"))

	state.MarkExecutorComplete(ExecutorPhaseReport{Total: 1}, err)
	gomega.Expect(state.FailureKind != plancontract.PlanExecuteFailureJSONEventWriteFailed).To(gomega.BeFalse(), fmt.Sprintf("executor FailureKind = %q, want json event write failure", state.FailureKind))

	state.MarkReviewComplete(ReviewRepairPhaseReport{Status: plancontract.PlanExecuteReviewStatusFailed}, err)
	gomega.Expect(state.FailureKind != plancontract.PlanExecuteFailureJSONEventWriteFailed).To(gomega.BeFalse(), fmt.Sprintf("review FailureKind = %q, want json event write failure", state.FailureKind))
})

var _ = ginkgo.It("plan execute prompt treats replace all as new file only", func() {
	prompt := PlanExecutePlannerSystemAddendumWithObligations([]string{"pkg/existing.go", "pkg/new_helper.go"}, nil, nil)

	for _, want := range []string{
		"replace_all:true`+`new_text`+empty `old_text` for missing/new files only",
		"Existing source/test files must use a bounded anchored edit shape",
	} {
		gomega.Expect(strings.Contains(prompt, want)).To(gomega.BeTrue(), fmt.Sprintf("prompt missing %q:\n%s", want, prompt))

	}

	for _, forbidden := range []string{
		"replace_all:true`+`new_text`+empty `old_text` for new-file/whole-file write",
		"whole/new-file `replace_all:true`",
	} {
		gomega.Expect(strings.Contains(prompt, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("prompt still advertises stale replace_all contract %q:\n%s", forbidden, prompt))

	}
})

var _ = ginkgo.It("run model override reaches planner and review repair", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed a.txt: %v", err))
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

	result := runForTest(cfg, Options{
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
		case string(autonomy.TaskFamilyPlanThenExecutePlanner):
			mgr := workspace.NewManager(callCfg.Workspace)
			res := mgr.Execute(context.Background(), workspace.ToolRequest{
				Tool:      workspace.ToolNameSubmitPlan,
				Rationale: "Update a.txt.",
				Edits: []planartifact.PlanEdit{{
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
		case string(autonomy.TaskFamilyPlanThenExecuteReview):
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: a.txt now contains after.\n- Scope safety: changed path is limited to a.txt.", nil
		default:
			return "", fmt.Errorf("unexpected task family %q", opts.TaskFamily)
		}
	}, nil, &stdout, &stderr)
	gomega.Expect(len(calls) < 2).To(gomega.BeFalse(), fmt.Sprintf("expected planner and review-repair calls, got %#v result=%#v stdout=%s stderr=%s", calls, result, stdout.String(), stderr.String()))

	for _, call := range calls {
		gomega.Expect(call.model != "deepseek-v4-flash").To(gomega.BeFalse(), fmt.Sprintf("task family %s saw model %q, want explicit override; calls=%#v", call.taskFamily, call.model, calls))
		gomega.Expect(call.useActiveModel).To(gomega.BeTrue(), fmt.Sprintf("task family %s did not preserve active-model authority; calls=%#v", call.taskFamily, calls))

	}
})

var _ = ginkgo.It("run terminal completion covers mixed source satisfied plan", ginkgo.Label("development-scenario", "integration", "contract"), func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed a.txt: %v", err))
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("already final\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed b.txt: %v", err))
	}

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer

	result := runForTest(cfg, Options{
		Prompt:            "Update a.txt while b.txt is already final.",
		RequiredEditPaths: MultiFlag{"a.txt", "b.txt"},
		JSONEvents:        true,
	}, func(callCfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		switch opts.TaskFamily {
		case string(autonomy.TaskFamilyPlanThenExecutePlanner):
			mgr := workspace.NewManager(callCfg.Workspace)
			plan := planartifact.PlanArtifact{
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
		case string(autonomy.TaskFamilyPlanThenExecuteReview):
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: a.txt now contains after and b.txt was already final.\n- Scope safety: changed path is limited to a.txt.", nil
		default:
			return "", fmt.Errorf("unexpected task family %q", opts.TaskFamily)
		}
	}, nil, &stdout, &stderr)
	gomega.Expect(result.ExitCode != 0).To(gomega.BeFalse(), fmt.Sprintf("Run exit=%d result=%#v stdout=%s stderr=%s", result.ExitCode, result, stdout.String(), stderr.String()))
	gomega.Expect(result.Completion).NotTo(gomega.BeNil(), fmt.Sprintf("expected terminal completion record, result=%#v stdout=%s", result, stdout.String()))
	gomega.Expect(slices.Contains(result.Completion.SourceSatisfiedEvidencePaths, "b.txt")).To(gomega.BeTrue(), fmt.Sprintf("terminal completion lost source_satisfied path: %#v", result.Completion))

	coverage := map[string]plancontract.PlanExecuteRequiredPathCoverageStatus{}
	for _, item := range result.Completion.RequiredPathCoverage {
		coverage[item.Path] = item.Status
	}
	gomega.Expect(coverage["a.txt"]).To(gomega.Equal(plancontract.PlanExecuteRequiredPathCoverageChanged), fmt.Sprintf("required coverage = %#v, want changed/source_satisfied", result.Completion.RequiredPathCoverage))
	gomega.Expect(coverage["b.txt"]).To(gomega.Equal(plancontract.PlanExecuteRequiredPathCoverageSourceSatisfied), fmt.Sprintf("required coverage = %#v, want changed/source_satisfied", result.Completion.RequiredPathCoverage))
})

var _ = ginkgo.It("classify executor handoff treats scoped fresh packet as fresh", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/planexec\n\ngo 1.22\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed go.mod: %v", err))
	}

	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package pkg\n\nfunc A() string { return \"alpha\" }\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed a.go: %v", err))
	}

	testPath := filepath.Join(root, "a_test.go")
	if err := os.WriteFile(testPath, []byte("package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed a_test.go: %v", err))
	}

	build, err := comprehension.Build(ginkgo.GinkgoT().Context(), comprehension.BuildOptions{Root: root, Language: "go"})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Build: %v", err))

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
		ginkgo.Fail(fmt.Sprintf("SaveView: %v", err))
	}

	if err := os.WriteFile(testPath, []byte("package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) { t.Helper() }\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("make advisory test stale: %v", err))
	}

	handoff := ClassifyExecutorHandoffPacket(root, planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		PackID:        key.ID(),
	})
	gomega.Expect(handoff.State).To(gomega.Equal(plancontract.ExecutorHandoffStateInherited), fmt.Sprintf("expected scoped-fresh packet to classify as fresh inherited handoff, got %#v", handoff))
	gomega.Expect(handoff.PacketFreshnessState).To(gomega.Equal(plancontract.ExecutorPacketFreshnessFresh), fmt.Sprintf("expected scoped-fresh packet to classify as fresh inherited handoff, got %#v", handoff))
	gomega.Expect(handoff.Fresh).To(gomega.BeTrue(), fmt.Sprintf("expected scoped-fresh packet to classify as fresh inherited handoff, got %#v", handoff))
})

var _ = ginkgo.It("apply executor handoff view populates quality", func() {
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
	gomega.Expect(handoff.Quality.LocalizationConfidence != "high").To(gomega.BeFalse(), fmt.Sprintf("Quality.LocalizationConfidence = %q, want high", handoff.Quality.LocalizationConfidence))
	gomega.Expect(handoff.Quality.SourceOwnerFound).To(gomega.BeTrue(), fmt.Sprint("Quality.SourceOwnerFound should be true"))
	gomega.Expect(handoff.Quality.EvidencePaths).To(gomega.HaveLen(1), fmt.Sprintf("Quality.EvidencePaths = %#v, want [pkg/target.go]", handoff.Quality.EvidencePaths))
	gomega.Expect(handoff.Quality.EvidencePaths[0]).To(gomega.Equal("pkg/target.go"), fmt.Sprintf("Quality.EvidencePaths = %#v, want [pkg/target.go]", handoff.Quality.EvidencePaths))
	gomega.Expect(len(handoff.Quality.Reasons) != 2).To(gomega.BeFalse(), fmt.Sprintf("Quality.Reasons = %#v, want 2 reasons", handoff.Quality.Reasons))
	gomega.Expect(handoff.CandidateFiles != 1).To(gomega.BeFalse(), fmt.Sprintf("CandidateFiles = %d, want 1", handoff.CandidateFiles))
})

var _ = ginkgo.It("phase evidence packet from view populates quality", func() {
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
	gomega.Expect(packet.Quality.LocalizationConfidence != "medium").To(gomega.BeFalse(), fmt.Sprintf("Quality.LocalizationConfidence = %q, want medium", packet.Quality.LocalizationConfidence))
	gomega.Expect(packet.Quality.SourceOwnerFound).To(gomega.BeFalse(), fmt.Sprint("Quality.SourceOwnerFound should be false"))
	gomega.Expect(len(packet.Quality.EvidencePaths) != 2).To(gomega.BeFalse(), fmt.Sprintf("Quality.EvidencePaths = %#v, want 2 paths", packet.Quality.EvidencePaths))
	gomega.Expect(packet.Quality.Reasons).To(gomega.HaveLen(1), fmt.Sprintf("Quality.Reasons = %#v, want [partial match]", packet.Quality.Reasons))
	gomega.Expect(packet.Quality.Reasons[0]).To(gomega.Equal("partial match"), fmt.Sprintf("Quality.Reasons = %#v, want [partial match]", packet.Quality.Reasons))
	// Verify existing fields unchanged.
	gomega.Expect(packet.PackID != "abc123def456abc1").To(gomega.BeFalse(), fmt.Sprintf("PackID = %q", packet.PackID))
	gomega.Expect(packet.Status != PhaseEvidencePacketStatusAvailable).To(gomega.BeFalse(), fmt.Sprintf("Status = %q", packet.Status))
})

var _ = ginkgo.It("executor handoff packet event fields includes quality", func() {
	handoff := ExecutorHandoffPacket{
		State:                plancontract.ExecutorHandoffStateInherited,
		Reason:               "fresh_graph_view",
		PackID:               "abc123def456abc1",
		Fresh:                true,
		PacketFreshnessState: plancontract.ExecutorPacketFreshnessFresh,
		CandidateFiles:       2,
		CandidateSymbols:     3,
		Tests:                1,
		Quality: PackQuality{
			LocalizationConfidence: "high",
			SourceOwnerFound:       true,
			EvidencePaths:          []string{"pkg/feature.go"},
			Reasons:                []string{"strong signal"},
		},
	}
	fields := executorHandoffPacketEventFields(handoff)

	quality, ok := fields["quality"].(map[string]any)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("fields[\"quality\"] missing or wrong type, fields=%#v", fields))
	gomega.Expect(quality["localization_confidence"] != "high").To(gomega.BeFalse(), fmt.Sprintf("quality[localization_confidence] = %v", quality["localization_confidence"]))
	gomega.Expect(quality["source_owner_found"]).To(gomega.BeTrue(), fmt.Sprintf("quality[source_owner_found] = %v", quality["source_owner_found"]))

	paths, _ := quality["evidence_paths"].([]string)
	gomega.Expect(paths).To(gomega.HaveLen(1), fmt.Sprintf("quality[evidence_paths] = %#v", paths))
	gomega.Expect(paths[0]).To(gomega.Equal("pkg/feature.go"), fmt.Sprintf("quality[evidence_paths] = %#v", paths))

	reasons, _ := quality["reasons"].([]string)
	gomega.Expect(reasons).To(gomega.HaveLen(1), fmt.Sprintf("quality[reasons] = %#v", reasons))
	gomega.Expect(reasons[0]).To(gomega.Equal("strong signal"), fmt.Sprintf("quality[reasons] = %#v", reasons))
})

var _ = ginkgo.It("executor handoff packet event fields omits quality when unset", func() {
	handoff := ExecutorHandoffPacket{
		State: plancontract.ExecutorHandoffStateMissing,
	}
	fields := executorHandoffPacketEventFields(handoff)

	if _, ok := fields["quality"]; ok {
		ginkgo.Fail(fmt.Sprint("fields[\"quality\"] should be absent for unset quality"))
	}
})

var _ = ginkgo.It("phase evidence handoff from executor copies quality", func() {
	handoff := ExecutorHandoffPacket{
		State:                plancontract.ExecutorHandoffStateInherited,
		Reason:               "fresh_graph_view",
		PackID:               "abc123def456abc1",
		Fresh:                true,
		PacketFreshnessState: plancontract.ExecutorPacketFreshnessFresh,
		CandidateFiles:       2,
		Quality: PackQuality{
			LocalizationConfidence: "high",
			SourceOwnerFound:       true,
			EvidencePaths:          []string{"pkg/feature.go"},
			Reasons:                []string{"strong signal"},
		},
	}
	peh := phaseEvidenceHandoffFromExecutor(handoff)
	gomega.Expect(peh.Quality.LocalizationConfidence != "high").To(gomega.BeFalse(), fmt.Sprintf("Quality.LocalizationConfidence = %q, want high", peh.Quality.LocalizationConfidence))
	gomega.Expect(peh.Quality.SourceOwnerFound).To(gomega.BeTrue(), fmt.Sprint("Quality.SourceOwnerFound should be true"))
	gomega.Expect(peh.Quality.EvidencePaths).To(gomega.HaveLen(1), fmt.Sprintf("Quality.EvidencePaths = %#v", peh.Quality.EvidencePaths))
	gomega.Expect(peh.Quality.EvidencePaths[0]).To(gomega.Equal("pkg/feature.go"), fmt.Sprintf("Quality.EvidencePaths = %#v", peh.Quality.EvidencePaths))
})

var _ = ginkgo.It("preflight plan edits rejects escaping path before read", func() {
	root := ginkgo.GinkgoT().TempDir()
	outside := filepath.Join(filepath.Dir(root), "outside.txt")

	if err := os.WriteFile(outside, []byte("secret\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed outside file: %v", err))
	}

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "../outside.txt", OldText: "secret", NewText: "redacted"},
		},
	}

	got := PreflightPlanEditsWithPolicy(root, plan, planExecuteTestDependencies().EditPreflight)
	gomega.Expect(got).NotTo(gomega.BeNil(), fmt.Sprintf("expected invalid path preflight mismatch, got %#v", got))
	gomega.Expect(got.Reason).To(gomega.Equal("edit has invalid path"), fmt.Sprintf("expected invalid path preflight mismatch, got %#v", got))
})

var _ = ginkgo.It("preflight plan edits rejects stale anchor provenance", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed a.txt: %v", err))
	}

	edits, err := workspace.AttachPlanEditAnchorProvenance(root, []planartifact.PlanEdit{
		{Path: "a.txt", OldText: "alpha", NewText: "ALPHA"},
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("attach anchor provenance: %v", err))

	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("prefix\nalpha\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("mutate a.txt: %v", err))
	}

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits:         edits,
	}
	got := PreflightPlanEditsWithPolicy(root, plan, planExecuteTestDependencies().EditPreflight)
	gomega.Expect(got).NotTo(gomega.BeNil(), fmt.Sprintf("preflight must reject stale anchor provenance before writes"))
	gomega.Expect(got.Index).To(gomega.Equal(0), fmt.Sprintf("mismatch should point at edit 0 in a.txt, got %#v", got))
	gomega.Expect(got.Path).To(gomega.Equal("a.txt"), fmt.Sprintf("mismatch should point at edit 0 in a.txt, got %#v", got))
	gomega.Expect(got.Reason).To(gomega.ContainSubstring("anchor provenance stale"), fmt.Sprintf("mismatch should report stale provenance, got %q", got.Reason))
	gomega.Expect(got.Reason).To(gomega.ContainSubstring("file fingerprint changed"), fmt.Sprintf("mismatch should report stale provenance, got %q", got.Reason))
})

var _ = ginkgo.It("preflight plan edits rejects current schema missing anchor provenance", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed a.txt: %v", err))
	}

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Created:       "2026-06-08T20:00:00Z",
		Handoff: planartifact.PlanArtifactHandoff{
			From: "planner",
			To:   "plan_execute_executor",
		},
		Edits: []planartifact.PlanEdit{{
			Path:    "a.txt",
			OldText: "alpha",
			NewText: "ALPHA",
		}},
	}

	got := PreflightPlanEditsWithPolicy(root, plan, planExecuteTestDependencies().EditPreflight)
	gomega.Expect(got).NotTo(gomega.BeNil(), fmt.Sprintf("expected missing anchor provenance mismatch, got %#v", got))
	gomega.Expect(got.Reason).To(gomega.ContainSubstring("missing submit_plan anchor provenance"), fmt.Sprintf("expected missing anchor provenance mismatch, got %#v", got))
})

var _ = ginkgo.It("preflight plan edits rejects existing edit without accepted source body evidence", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed a.txt: %v", err))
	}

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Created:       "2026-06-18T20:00:00Z",
		Handoff: planartifact.PlanArtifactHandoff{
			From: "planner",
			To:   "plan_execute_executor",
		},
		Edits: []planartifact.PlanEdit{{
			Path:    "a.txt",
			OldText: "alpha",
			NewText: "ALPHA",
			Purpose: "update existing file without accepted source-body evidence",
		}},
	}

	got := PreflightPlanEditsWithPolicy(root, plan, planExecuteTestDependencies().EditPreflight)
	gomega.Expect(got).NotTo(gomega.BeNil(), fmt.Sprint("expected missing source-body evidence to reject existing-file edit"))
	gomega.Expect(strings.Contains(got.Reason, "missing submit_plan anchor provenance")).To(gomega.BeTrue(), fmt.Sprintf("expected missing source-body evidence rejection, got %#v", got))
})

var _ = ginkgo.It("preflight plan required edit paths accepts source satisfied coverage", func() {
	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Paths: planartifact.PlanArtifactPaths{
			SourceSatisfied: []string{"a.go"},
		},
		Evidence: []planartifact.PlanArtifactEvidence{{Kind: "harness_observed_source", Path: "a.go"}},
		Edits: []planartifact.PlanEdit{
			{Path: "b.go", OldText: "before", NewText: "after", Purpose: "update b"},
		},
	}

	if got := preflightPlanRequiredEditPaths([]string{"a.go", "b.go"}, plan); got != nil {
		ginkgo.Fail(fmt.Sprintf("source_satisfied_paths should cover already-satisfied required paths, got %#v", got))
	}
})

var _ = ginkgo.It("preflight plan declared scope accepts source satisfied required path", func() {
	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Paths: planartifact.PlanArtifactPaths{
			Required:        []string{"a.go", "b.go"},
			Editable:        []string{"b.go"},
			Changed:         []string{"b.go"},
			SourceSatisfied: []string{"a.go"},
		},
		Evidence: []planartifact.PlanArtifactEvidence{{Kind: "harness_observed_source", Path: "a.go"}},
		Edits: []planartifact.PlanEdit{
			{Path: "b.go", OldText: "before", NewText: "after", Purpose: "update b"},
		},
	}

	if got := preflightPlanDeclaredScope(plan); got != nil {
		ginkgo.Fail(fmt.Sprintf("source_satisfied_paths should satisfy declared required scope coverage, got %#v", got))
	}
})

var _ = ginkgo.It("preflight plan required edit paths rejects out of scope source satisfied path", func() {
	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Paths: planartifact.PlanArtifactPaths{
			SourceSatisfied: []string{"outside.go"},
		},
		Edits: []planartifact.PlanEdit{
			{Path: "b.go", OldText: "before", NewText: "after", Purpose: "update b"},
		},
	}

	got := preflightPlanRequiredEditPaths([]string{"a.go", "b.go"}, plan)
	gomega.Expect(got).NotTo(gomega.BeNil(), fmt.Sprintf("expected out-of-scope source_satisfied_paths rejection, got %#v", got))
	gomega.Expect(got.Path).To(gomega.Equal("outside.go"), fmt.Sprintf("expected out-of-scope source_satisfied_paths rejection, got %#v", got))
	gomega.Expect(got.Reason).To(gomega.Equal("source-satisfied path is outside required_edit_paths allowlist"), fmt.Sprintf("expected out-of-scope source_satisfied_paths rejection, got %#v", got))
})

var _ = ginkgo.It("preflight plan declared scope rejects out of scope source satisfied path", func() {
	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Paths: planartifact.PlanArtifactPaths{
			Required:        []string{"a.go", "b.go"},
			Editable:        []string{"b.go"},
			Changed:         []string{"b.go"},
			SourceSatisfied: []string{"outside.go"},
		},
		Edits: []planartifact.PlanEdit{
			{Path: "b.go", OldText: "before", NewText: "after", Purpose: "update b"},
		},
	}

	got := preflightPlanDeclaredScope(plan)
	gomega.Expect(got).NotTo(gomega.BeNil(), fmt.Sprintf("expected declared-scope source_satisfied_paths rejection, got %#v", got))
	gomega.Expect(got.Path).To(gomega.Equal("outside.go"), fmt.Sprintf("expected declared-scope source_satisfied_paths rejection, got %#v", got))
	gomega.Expect(got.Reason).To(gomega.Equal("source-satisfied path is outside required plan scope"), fmt.Sprintf("expected declared-scope source_satisfied_paths rejection, got %#v", got))
})

var _ = ginkgo.It("preflight plan declared scope rejects pending partial approval source satisfied path", func() {
	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Paths: planartifact.PlanArtifactPaths{
			Required:        []string{"a.go", "b.go"},
			Editable:        []string{"b.go"},
			Changed:         []string{"b.go"},
			SourceSatisfied: []string{"a.go"},
		},
		Evidence: []planartifact.PlanArtifactEvidence{{Kind: "harness_observed_source", Path: "a.go"}},
		Approval: planartifact.PlanArtifactApproval{
			Required:            true,
			Status:              "partial",
			ScopeID:             "scope-1",
			ApprovedPaths:       []string{"b.go"},
			PendingPaths:        []string{"a.go"},
			ExecutionPermission: "execute_approved_scope",
		},
		Handoff: planartifact.PlanArtifactHandoff{ID: "handoff-1", Status: "ready", Ready: true},
		Edits: []planartifact.PlanEdit{
			{Path: "b.go", OldText: "before", NewText: "after", Purpose: "update b"},
		},
	}

	got := preflightPlanDeclaredScope(plan)
	gomega.Expect(got).NotTo(gomega.BeNil(), fmt.Sprintf("expected pending-approval source_satisfied_paths rejection, got %#v", got))
	gomega.Expect(got.Path).To(gomega.Equal("a.go"), fmt.Sprintf("expected pending-approval source_satisfied_paths rejection, got %#v", got))
	gomega.Expect(got.Reason).To(gomega.Equal("source-satisfied path is still pending approval"), fmt.Sprintf("expected pending-approval source_satisfied_paths rejection, got %#v", got))
})

var _ = ginkgo.It("run executor phase applies insert after line plan edit", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "feature_test.go"), []byte("package demo\n\nfunc TestExisting(t *testing.T) {}\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed feature_test.go: %v", err))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	writeRunTestPlanViaSubmitPlan(cfg, []planartifact.PlanEdit{{
		Path:            "feature_test.go",
		InsertAfterLine: 3,
		Content:         "\nfunc TestInserted(t *testing.T) {}\n",
		Purpose:         "insert a regression test after the existing test",
	}}, "insert regression test")

	plan, err := workspace.LoadPlanArtifact(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load submitted plan: %v", err))

	var stdout bytes.Buffer

	report, err := runExecutorPhaseForTest(cfg, plan, ExecutorPhaseOptions{
		RequiredEditPaths: []string{"feature_test.go"},
		JSONEvents:        true,
		Stdout:            &stdout,
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("executor should apply insert_after_line plan edit, report=%#v stdout=%s err=%v", report, stdout.String(), err))
	gomega.Expect(report.Succeeded).To(gomega.Equal(1), fmt.Sprintf("unexpected executor report: %#v", report))
	gomega.Expect(report.Failed).To(gomega.Equal(0), fmt.Sprintf("unexpected executor report: %#v", report))

	got := readFileStringForRunTest(filepath.Join(root, "feature_test.go"))
	gomega.Expect(got).To(gomega.ContainSubstring("func TestExisting"), fmt.Sprintf("insert_after_line edit not applied:\n%s", got))
	gomega.Expect(got).To(gomega.ContainSubstring("func TestInserted"), fmt.Sprintf("insert_after_line edit not applied:\n%s", got))
})

var _ = ginkgo.It("run auto exact plan source satisfied skips planner", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("new a\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed a.txt: %v", err))
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

	result := runForTest(cfg, Options{
		Prompt:     prompt,
		JSONEvents: true,
	}, func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
		plannerCalled = true
		return "", nil
	}, nil, &stdout, &stderr)
	gomega.Expect(result.ExitCode != 0).To(gomega.BeFalse(), fmt.Sprintf("source-satisfied run should pass, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String()))
	gomega.Expect(plannerCalled).To(gomega.BeFalse(), fmt.Sprint("planner should not run when exact edits are already applied"))
	gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"type":"`+plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventAutoExactPlanSourceSatisfied)+`"`), fmt.Sprintf("source-satisfied events missing from stdout=%s", stdout.String()))
	gomega.Expect(stdout.String()).To(gomega.ContainSubstring(`"type":"`+plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventSourceSatisfied)+`"`), fmt.Sprintf("source-satisfied events missing from stdout=%s", stdout.String()))
})

var _ = ginkgo.It("run rejects source satisfied answer with only prose evidence paths", func() {
	root := ginkgo.GinkgoT().TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer

	result := runForTest(cfg, Options{
		Prompt: "Reject temp symlinks and add regression tests proving no partial mutation.",
		RequiredEditPaths: MultiFlag{
			"internal/workflows/productworkflow/migration_apply.go",
			"internal/workflows/productworkflow/migration_apply_test.go",
		},
		JSONEvents: true,
	}, func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
		return sourceSatisfiedPlannerAnswerForRunTest(
			"- internal/workflows/productworkflow/migration_apply.go: no changes needed.",
			"- internal/workflows/productworkflow/migration_apply_test.go: no changes needed.",
		), nil
	}, nil, &stdout, &stderr)
	gomega.Expect(result.ExitCode).To(gomega.Equal(1), fmt.Sprintf("source-satisfied answer with only prose evidence should fail terminal contract, got code=%d kind=%s stdout=%s stderr=%s", result.ExitCode, result.FailureKind, stdout.String(), stderr.String()))
	gomega.Expect(result.FailureKind).To(gomega.Equal(string(plancontract.PlanExecuteFailureTerminalCompletionContractFailed)), fmt.Sprintf("source-satisfied answer with only prose evidence should fail terminal contract, got code=%d kind=%s stdout=%s stderr=%s", result.ExitCode, result.FailureKind, stdout.String(), stderr.String()))
	gomega.Expect(strings.Contains(stdout.String(), `"type":"`+plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventSourceSatisfied)+`"`)).To(gomega.BeTrue(), fmt.Sprintf("source-satisfied event missing from stdout=%s", stdout.String()))

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	event, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventSourceSatisfied))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected source_satisfied event, got %s", stdout.String()))

	if got, _ := event["source_satisfied_evidence_paths"].([]any); len(got) != 2 {
		ginkgo.Fail(fmt.Sprintf("expected source-satisfied evidence paths in telemetry, got %#v", event))
	}

	terminal, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventTerminalCompletion))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected terminal completion contract failure, got %#v", terminal))
	gomega.Expect(terminal["failure_kind"]).To(gomega.Equal(string(plancontract.PlanExecuteFailureTerminalCompletionContractFailed)), fmt.Sprintf("expected terminal completion contract failure, got %#v", terminal))
})

var _ = ginkgo.It("run preserves source satisfied evidence accepted by the trusted prompt runtime", func() {
	root := ginkgo.GinkgoT().TempDir()
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer
	answer := sourceSatisfiedPlannerAnswerForRunTest(
		"- pkg/feature.go: the current state already contains the requested behavior.",
		"- pkg/feature_test.go: the current state already contains the requested coverage.",
	)

	result := runForTest(cfg, Options{
		Prompt:            "Confirm the existing feature and regression coverage.",
		RequiredEditPaths: MultiFlag{"pkg/feature.go", "pkg/feature_test.go"},
		JSONEvents:        true,
	}, func(_ config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		gomega.Expect(opts.OnSourceSatisfied).NotTo(gomega.BeNil())
		opts.OnSourceSatisfied(plancontract.SourceSatisfiedEvidencePaths(answer))
		return answer, nil
	}, nil, &stdout, &stderr)

	gomega.Expect(result.ExitCode).To(gomega.Equal(0), fmt.Sprintf("trusted source-satisfied result failed: %#v\nstdout=%s\nstderr=%s", result, stdout.String(), stderr.String()))
	gomega.Expect(result.Completion).NotTo(gomega.BeNil())
	gomega.Expect(result.Completion.SourceSatisfiedEvidence).To(gomega.ConsistOf(
		plancontract.SourceSatisfiedEvidenceRef{Path: "pkg/feature.go", Kind: plancontract.SourceSatisfiedEvidenceKindHarnessObservedSource},
		plancontract.SourceSatisfiedEvidenceRef{Path: "pkg/feature_test.go", Kind: plancontract.SourceSatisfiedEvidenceKindHarnessObservedSource},
	))
})

var _ = ginkgo.It("run rejects source satisfied answer with benign unsupported word but only prose evidence", func() {
	root := ginkgo.GinkgoT().TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer

	result := runForTest(cfg, Options{
		Prompt: "Reject temp symlinks and add regression tests proving no partial mutation.",
		RequiredEditPaths: MultiFlag{
			"internal/workflows/productworkflow/migration_apply.go",
			"internal/workflows/productworkflow/migration_apply_test.go",
		},
		JSONEvents: true,
	}, func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
		return sourceSatisfiedPlannerAnswerForRunTest(
			"Context: this code uses a LikelyPrefix helper name elsewhere, but it is not source-satisfied evidence.",
			"- internal/workflows/productworkflow/migration_apply.go: already present; current source rejects temp symlinks before mutation through LikelyPrefix handling.",
			"- internal/workflows/productworkflow/migration_apply_test.go: already present; current tests cover no partial mutation on rejection.",
		), nil
	}, nil, &stdout, &stderr)
	gomega.Expect(result.ExitCode).To(gomega.Equal(1), fmt.Sprintf("source-satisfied answer with only prose evidence should fail terminal contract, got code=%d kind=%s stdout=%s stderr=%s", result.ExitCode, result.FailureKind, stdout.String(), stderr.String()))
	gomega.Expect(result.FailureKind).To(gomega.Equal(string(plancontract.PlanExecuteFailureTerminalCompletionContractFailed)), fmt.Sprintf("source-satisfied answer with only prose evidence should fail terminal contract, got code=%d kind=%s stdout=%s stderr=%s", result.ExitCode, result.FailureKind, stdout.String(), stderr.String()))
	gomega.Expect(strings.Contains(stdout.String(), `"type":"`+plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventSourceSatisfiedRejected)+`"`)).To(gomega.BeFalse(), fmt.Sprintf("benign non-evidence marker should not reject source-satisfied answer, stdout=%s", stdout.String()))
})

var _ = ginkgo.It("run rejects source satisfied answer with unsupported inference", func() {
	root := ginkgo.GinkgoT().TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer

	result := runForTest(cfg, Options{
		Prompt:     "Implement item tooltip display and native binding coverage.",
		JSONEvents: true,
	}, func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
		return sourceSatisfiedPlannerAnswerForRunTest(
			"- native/src/core/colony_sim.h: binding fields are already present.",
			"- native/tests/test_quality_inventory_bindings.cpp: coverage is not shown in full but implied by passing tests.",
			"- scripts/ui/ui_helpers.gd: tooltip surface likely already uses the same dictionary fields.",
		), nil
	}, nil, &stdout, &stderr)
	gomega.Expect(result.ExitCode != 1).To(gomega.BeFalse(), fmt.Sprintf("expected unsupported source-satisfied rejection, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String()))
	gomega.Expect(result.FailureKind != string(plancontract.PlanExecuteFailureSourceSatisfiedUnsupportedEvidence)).To(gomega.BeFalse(), fmt.Sprintf("expected %s, result=%#v stderr=%s", string(plancontract.PlanExecuteFailureSourceSatisfiedUnsupportedEvidence), result, stderr.String()))

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	event, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventSourceSatisfiedRejected))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected source_satisfied_rejected event, got %s", stdout.String()))
	gomega.Expect(event["reason"] != string(plancontract.PlanExecuteFailureSourceSatisfiedUnsupportedEvidence)).To(gomega.BeFalse(), fmt.Sprintf("expected unsupported-evidence reason, got %#v", event))
	gomega.Expect(event["unsupported_evidence_marker"] != "not shown in full").To(gomega.BeFalse(), fmt.Sprintf("expected rejected marker in telemetry, got %#v", event))

	if got, _ := event["source_satisfied_evidence_paths"].([]any); len(got) != 3 {
		ginkgo.Fail(fmt.Sprintf("expected rejected source-satisfied evidence paths, got %#v", event))
	}
})

var _ = ginkgo.It("run rejects source satisfied answer with stale anchor marker", func() {
	root := ginkgo.GinkgoT().TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer

	result := runForTest(cfg, Options{
		Prompt:            "Update parser behavior.",
		RequiredEditPaths: MultiFlag{"pkg/x.go"},
		JSONEvents:        true,
	}, func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
		return sourceSatisfiedPlannerAnswerForRunTest("- pkg/x.go: stale anchor; old_text no longer applies."), nil
	}, nil, &stdout, &stderr)
	gomega.Expect(result.ExitCode).To(gomega.Equal(1), fmt.Sprintf("expected unsupported stale-anchor rejection, got code=%d kind=%s stdout=%s stderr=%s", result.ExitCode, result.FailureKind, stdout.String(), stderr.String()))
	gomega.Expect(result.FailureKind).To(gomega.Equal(string(plancontract.PlanExecuteFailureSourceSatisfiedUnsupportedEvidence)), fmt.Sprintf("expected unsupported stale-anchor rejection, got code=%d kind=%s stdout=%s stderr=%s", result.ExitCode, result.FailureKind, stdout.String(), stderr.String()))

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	event, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventSourceSatisfiedRejected))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected source_satisfied_rejected event, got %s", stdout.String()))
	gomega.Expect(event["unsupported_evidence_marker"] != "stale anchor").To(gomega.BeFalse(), fmt.Sprintf("expected stale anchor marker, got %#v", event))
})

var _ = ginkgo.It("run rejects source satisfied answer when requested test edit missing", func() {
	root := ginkgo.GinkgoT().TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer

	result := runForTest(cfg, Options{
		Prompt:             "Fix parser behavior and add regression tests.",
		JSONEvents:         true,
		EditSetObligations: plancontract.EditSetObligations{}.With(plancontract.EditSetObligationKindTestEdit, "test"),
	}, func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
		return sourceSatisfiedPlannerAnswerForRunTest("- pkg/parser.go: parser behavior is already present; no changes needed."), nil
	}, nil, &stdout, &stderr)
	gomega.Expect(result.ExitCode != 1).To(gomega.BeFalse(), fmt.Sprintf("expected missing test edit rejection, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String()))
	gomega.Expect(result.FailureKind).To(gomega.Equal(string(plancontract.PlanExecuteFailureMissingRequestedTestEdit)), fmt.Sprintf("expected %s, result=%#v stderr=%s", plancontract.PlanExecuteFailureMissingRequestedTestEdit, result, stderr.String()))
	gomega.Expect(stderr.String()).To(gomega.ContainSubstring(string(plancontract.PlanExecuteFailureMissingRequestedTestEdit)), fmt.Sprintf("expected %s, result=%#v stderr=%s", plancontract.PlanExecuteFailureMissingRequestedTestEdit, result, stderr.String()))

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	event, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventSourceSatisfiedRejected))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected source_satisfied_rejected event, got %s", stdout.String()))
	gomega.Expect(event["reason"] != string(plancontract.PlanExecuteFailureMissingRequestedTestEdit)).To(gomega.BeFalse(), fmt.Sprintf("expected %s reason, got %#v", plancontract.PlanExecuteFailureMissingRequestedTestEdit, event))

	if got, _ := event["edit_set_obligations"].([]any); len(got) == 0 {
		ginkgo.Fail(fmt.Sprintf("expected edit_set_obligations metadata, got %#v", event))
	}

	if got, _ := event["source_satisfied_evidence_paths"].([]any); len(got) != 1 || got[0] != "pkg/parser.go" {
		ginkgo.Fail(fmt.Sprintf("expected rejected source-satisfied evidence path, got %#v", event))
	}

	if got, _ := event["missing_edit_set_obligations"].([]any); len(got) == 0 {
		ginkgo.Fail(fmt.Sprintf("expected missing edit-set obligations in rejection telemetry, got %#v", event))
	}
})

var _ = ginkgo.It("run rejects source satisfied answer when requested implementation edit missing", func() {
	root := ginkgo.GinkgoT().TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer

	result := runForTest(cfg, Options{
		Prompt:             "Implement the feature and add tests.",
		JSONEvents:         true,
		EditSetObligations: plancontract.EditSetObligations{}.With(plancontract.EditSetObligationKindImplementationEdit, "test"),
	}, func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
		return sourceSatisfiedPlannerAnswerForRunTest("- pkg/feature_test.go: feature tests are already present."), nil
	}, nil, &stdout, &stderr)
	gomega.Expect(result.ExitCode != 1).To(gomega.BeFalse(), fmt.Sprintf("expected missing implementation edit rejection, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String()))
	gomega.Expect(result.FailureKind).To(gomega.Equal(string(plancontract.PlanExecuteFailureMissingRequestedImplementationEdit)), fmt.Sprintf("expected %s, result=%#v stderr=%s", plancontract.PlanExecuteFailureMissingRequestedImplementationEdit, result, stderr.String()))
	gomega.Expect(stderr.String()).To(gomega.ContainSubstring(string(plancontract.PlanExecuteFailureMissingRequestedImplementationEdit)), fmt.Sprintf("expected %s, result=%#v stderr=%s", plancontract.PlanExecuteFailureMissingRequestedImplementationEdit, result, stderr.String()))

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	event, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventSourceSatisfiedRejected))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected source_satisfied_rejected event, got %s", stdout.String()))
	gomega.Expect(event["reason"] != string(plancontract.PlanExecuteFailureMissingRequestedImplementationEdit)).To(gomega.BeFalse(), fmt.Sprintf("expected %s reason, got %#v", plancontract.PlanExecuteFailureMissingRequestedImplementationEdit, event))

	if got, _ := event["edit_set_obligations"].([]any); len(got) == 0 {
		ginkgo.Fail(fmt.Sprintf("expected edit_set_obligations metadata, got %#v", event))
	}

	if got, _ := event["source_satisfied_evidence_paths"].([]any); len(got) != 1 || got[0] != "pkg/feature_test.go" {
		ginkgo.Fail(fmt.Sprintf("expected rejected source-satisfied evidence path, got %#v", event))
	}

	if got, _ := event["missing_edit_set_obligations"].([]any); len(got) == 0 {
		ginkgo.Fail(fmt.Sprintf("expected missing edit-set obligations in rejection telemetry, got %#v", event))
	}
})

var _ = ginkgo.It("save run progress state writes recovery decomposition packet", func() {
	root := ginkgo.GinkgoT().TempDir()
	state := NewProgressState("auto_recovery_packet", "make a change", "", []string{"a.txt"}, nil, timeNowForRunTest())

	state.Status = plancontract.PlanExecuteProgressStatusFailed
	state.FailureKind = "executor_failed"
	state.FailureReason = "executor failed"
	state.ChangedPaths = []string{"a.txt"}

	var stdout bytes.Buffer

	statePath, err := saveRunProgressState(root, &state, true, &stdout, plancontract.PlanExecuteEventResumeStateSaved)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("saveRunProgressState: %v", err))

	record, _, found, err := recovery.TryLoadFailureRecord(root, recovery.PlanExecuteFailureID(state.SessionID))
	gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("TryLoadFailureRecord found=%v err=%v", found, err))
	gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprintf("TryLoadFailureRecord found=%v err=%v", found, err))

	packetPath, _, err := recovery.DecompositionPacketPath(root, record.FailureID, "")
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("DecompositionPacketPath: %v", err))

	packet, _, found, err := recovery.TryLoadDecompositionPacket(packetPath)
	gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("TryLoadDecompositionPacket found=%v err=%v", found, err))
	gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprintf("TryLoadDecompositionPacket found=%v err=%v", found, err))
	gomega.Expect(packet.FailureID).To(gomega.Equal(record.FailureID), fmt.Sprintf("unexpected packet: %#v", packet))
	gomega.Expect(packet.CandidateAtoms).To(gomega.HaveLen(1), fmt.Sprintf("unexpected packet: %#v", packet))
	gomega.Expect(strings.Contains(stdout.String(), `"type":"`+plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventRecoveryDecompositionWritten)+`"`)).To(gomega.BeTrue(), fmt.Sprintf("packet event missing from stdout=%s statePath=%s", stdout.String(), statePath))
})

var _ = ginkgo.It("run planner blocked progress event carries terminal failure fields", func() {
	root := ginkgo.GinkgoT().TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer

	result := runForTest(cfg, Options{
		Prompt:            "Update target.txt.",
		RequiredEditPaths: []string{"target.txt"},
		JSONEvents:        true,
	}, func(_ config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		gomega.Expect(opts.TaskFamily != string(autonomy.TaskFamilyPlanThenExecutePlanner)).To(gomega.BeFalse(), fmt.Sprintf("unexpected task family %q", opts.TaskFamily))

		return "<<QUESTION>>: target.txt: required source evidence is unavailable.", nil
	}, nil, &stdout, &stderr)
	gomega.Expect(result.ExitCode).To(gomega.Equal(1), fmt.Sprintf("expected planner_blocked result, got %#v stdout=%s stderr=%s", result, stdout.String(), stderr.String()))
	gomega.Expect(result.FailureKind).To(gomega.Equal(string(plancontract.PlanExecuteFailurePlannerBlocked)), fmt.Sprintf("expected planner_blocked result, got %#v stdout=%s stderr=%s", result, stdout.String(), stderr.String()))

	state, _, found, err := TryLoadProgressState(root, result.SessionID)
	gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("load progress state: %v", err))
	gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprintf("load progress state: %v", err))
	gomega.Expect(state.Phase).To(gomega.Equal(plancontract.PlanExecuteProgressPhasePlanning), fmt.Sprintf("expected structured planning failure state, got %#v", state))
	gomega.Expect(state.Status).To(gomega.Equal(plancontract.PlanExecuteProgressStatusFailed), fmt.Sprintf("expected structured planning failure state, got %#v", state))
	gomega.Expect(state.FailureKind).To(gomega.Equal(plancontract.PlanExecuteFailurePlannerBlocked), fmt.Sprintf("expected structured planning failure state, got %#v", state))

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())

	var event map[string]any

	for _, candidate := range events {
		if candidate["type"] == plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventResumeStateSaved) && candidate["status"] == string(plancontract.PlanExecuteProgressStatusFailed) {
			event = candidate
		}
	}
	gomega.Expect(event).NotTo(gomega.BeNil(), fmt.Sprintf("expected resume_state_saved event, got %s", stdout.String()))
	gomega.Expect(event["failure_kind"] != string(plancontract.PlanExecuteFailurePlannerBlocked)).To(gomega.BeFalse(), fmt.Sprintf("resume state event missing failure_kind: %#v", event))

	if got := fmt.Sprint(event["failure_reason"]); !strings.Contains(got, "target.txt") {
		ginkgo.Fail(fmt.Sprintf("resume state event missing concrete failure reason: %#v", event))
	}

	if got, _ := event["required_edit_paths"].([]any); len(got) != 1 || got[0] != "target.txt" {
		ginkgo.Fail(fmt.Sprintf("resume state event missing required paths: %#v", event))
	}
})

var _ = ginkgo.It("run planner blocked preserves full anchor refresh failure reason", func() {
	root := ginkgo.GinkgoT().TempDir()
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

	result := runForTest(cfg, Options{
		Prompt: "Update collapse dispatch.",
		RequiredEditPaths: []string{
			"native/src/core/colony_sim_tick.cpp",
			"native/src/simulation/world/collapse_event.h",
		},
		JSONEvents: true,
	}, func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
		return answer, nil
	}, nil, &stdout, &stderr)
	gomega.Expect(result.ExitCode).To(gomega.Equal(1), fmt.Sprintf("expected planner_blocked result, got %#v stdout=%s stderr=%s", result, stdout.String(), stderr.String()))
	gomega.Expect(result.FailureKind).To(gomega.Equal(string(plancontract.PlanExecuteFailurePlannerBlocked)), fmt.Sprintf("expected planner_blocked result, got %#v stdout=%s stderr=%s", result, stdout.String(), stderr.String()))

	state, _, found, err := TryLoadProgressState(root, result.SessionID)
	gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("load progress state: %v", err))
	gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprintf("load progress state: %v", err))
	gomega.Expect(state.FailureReason != answer).To(gomega.BeFalse(), fmt.Sprintf("failure reason was compacted:\n got: %q\nwant: %q", state.FailureReason, answer))

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	var resume map[string]any
	for _, event := range events {
		if event["type"] == plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventResumeStateSaved) && event["status"] == string(plancontract.PlanExecuteProgressStatusFailed) {
			resume = event
			break
		}
	}
	gomega.Expect(resume).NotTo(gomega.BeNil(), fmt.Sprintf("expected resume_state_saved event, got %s", stdout.String()))

	if got := fmt.Sprint(resume["failure_reason"]); !strings.Contains(got, "Remaining bounded refresh paths") {
		ginkgo.Fail(fmt.Sprintf("resume_state_saved failure_reason lost blocker detail: %#v", resume))
	}
	gomega.Expect(result.Completion).NotTo(gomega.BeNil(), fmt.Sprintf("terminal completion should retain full blocker detail, got %#v", result.Completion))
	gomega.Expect(result.Completion.FailureReason).To(gomega.ContainSubstring("Remaining bounded refresh paths"), fmt.Sprintf("terminal completion should retain full blocker detail, got %#v", result.Completion))

	record, _, found, err := recovery.TryLoadFailureRecord(root, recovery.PlanExecuteFailureID(result.SessionID))
	gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("load recovery record found=%v err=%v", found, err))
	gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprintf("load recovery record found=%v err=%v", found, err))
	gomega.Expect(record.NextAction != recoverycontract.NextActionRetryPlanExecute).To(gomega.BeFalse(), fmt.Sprintf("next action=%q, want %q; record=%#v", record.NextAction, recoverycontract.NextActionRetryPlanExecute, record))
})

var _ = ginkgo.It("run planner blocked failure reason redacted in result", func() {
	root := ginkgo.GinkgoT().TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer
	answer := "<<QUESTION>>: Plan-execute anchor refresh is still required before a corrected submit_plan can be accepted.\nraw_prompt: include customer file\n```text\nOPENAI_API_KEY=sk-secret\n```"

	result := runForTest(cfg, Options{
		Prompt:            "Update target.txt.",
		RequiredEditPaths: []string{"target.txt"},
		JSONEvents:        true,
	}, func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
		return answer, nil
	}, nil, &stdout, &stderr)
	gomega.Expect(result.ExitCode).To(gomega.Equal(1), fmt.Sprintf("expected planner_blocked result, got %#v stdout=%s stderr=%s", result, stdout.String(), stderr.String()))
	gomega.Expect(result.FailureKind).To(gomega.Equal(string(plancontract.PlanExecuteFailurePlannerBlocked)), fmt.Sprintf("expected planner_blocked result, got %#v stdout=%s stderr=%s", result, stdout.String(), stderr.String()))

	for _, forbidden := range []string{"raw_prompt", "OPENAI_API_KEY", "sk-secret"} {
		gomega.Expect(strings.Contains(result.FailureReason, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("RunResult failure reason leaked %q: %q", forbidden, result.FailureReason))

	}
	gomega.Expect(strings.Contains(result.FailureReason, "redacted")).To(gomega.BeTrue(), fmt.Sprintf("expected redaction marker in RunResult failure reason: %q", result.FailureReason))
})

var _ = ginkgo.It("save progress state redacts durable failure text", func() {
	root := ginkgo.GinkgoT().TempDir()
	state := NewProgressState("redacted_progress", "prompt", "", []string{"a.txt"}, nil, timeNowForRunTest())

	state.Status = plancontract.PlanExecuteProgressStatusFailed
	state.FailureKind = "executor_failed"
	state.FailureReason = "raw_prompt: include customer file\n```text\nfile body with OPENAI_API_KEY=sk-secret\n```"
	state.Executor.Failures = []string{"old_text: customer file body\npassword=secret"}

	if _, err := SaveProgressState(root, &state); err != nil {
		ginkgo.Fail(fmt.Sprintf("SaveProgressState: %v", err))
	}

	loaded, _, found, err := TryLoadProgressState(root, state.SessionID)
	gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("LoadProgressState: %v", err))
	gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprintf("LoadProgressState: %v", err))

	encoded, err := json.Marshal(loaded)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal loaded progress: %v", err))

	text := string(encoded)
	for _, forbidden := range []string{"raw_prompt", "file body", "OPENAI_API_KEY", "sk-secret", "old_text", "password=secret"} {
		gomega.Expect(strings.Contains(text, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("durable progress state leaked %q: %s", forbidden, text))

	}
	gomega.Expect(strings.Contains(text, "redacted")).To(gomega.BeTrue(), fmt.Sprintf("expected redaction marker in progress state: %s", text))
})

var _ = ginkgo.It("save progress state does not follow predictable temp symlink", func() {
	root := ginkgo.GinkgoT().TempDir()
	state := NewProgressState("tmp_symlink_progress", "prompt", "", []string{"a.txt"}, nil, timeNowForRunTest())
	fullPath, _, err := progressStatePath(root, state.SessionID)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("progressStatePath: %v", err))

	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir progress dir: %v", err))
	}
	outside := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(outside, []byte("sentinel"), 0o600); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed outside: %v", err))
	}
	if err := os.Symlink(outside, fullPath+".tmp"); err != nil {
		ginkgo.Fail(fmt.Sprintf("symlink predictable temp: %v", err))
	}

	if _, err := SaveProgressState(root, &state); err != nil {
		ginkgo.Fail(fmt.Sprintf("SaveProgressState: %v", err))
	}
	body, err := os.ReadFile(outside)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read outside: %v", err))
	gomega.Expect(string(body) != "sentinel").To(gomega.BeFalse(), fmt.Sprintf("predictable temp symlink target was modified: %q", string(body)))

	if _, err := os.Lstat(fullPath + ".tmp"); err != nil {
		ginkgo.Fail(fmt.Sprintf("predictable temp symlink should remain untouched: %v", err))
	}
})

var _ = ginkgo.It("run persists progress and resume runs review repair only", ginkgo.Label("development-scenario", "integration", "contract"), func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("old\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed a.txt: %v", err))
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("already final\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed b.txt: %v", err))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	prompt := "Make a.txt say new."

	var firstStdout, firstStderr bytes.Buffer

	first := runForTest(cfg, Options{
		Prompt:            prompt,
		RequiredEditPaths: MultiFlag{"a.txt", "b.txt"},
		JSONEvents:        true,
	}, func(cfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		switch opts.TaskFamily {
		case string(autonomy.TaskFamilyPlanThenExecutePlanner):
			mgr := workspace.NewManager(cfg.Workspace)
			plan := planartifact.PlanArtifact{
				SchemaVersion: planartifact.SchemaVersion,
				Task: planartifact.PlanArtifactTask{
					ID:      "resume-mixed-source-satisfied",
					Summary: "Change a.txt while b.txt is already final.",
				},
				Contract: planartifact.PlanArtifactContract{
					Kind:              "implementation_result",
					PermissionProfile: "guarded_workspace_write",
					RequiredEditPaths: []string{"a.txt", "b.txt"},
				},
				Paths: planartifact.PlanArtifactPaths{
					Required:        []string{"a.txt", "b.txt"},
					Editable:        []string{"a.txt"},
					Changed:         []string{"a.txt"},
					SourceSatisfied: []string{"b.txt"},
				},
				Evidence: []planartifact.PlanArtifactEvidence{{
					Kind:    "harness_observed_source",
					Path:    "b.txt",
					Summary: "current source already contains the requested final state",
				}},
				VerificationObligations: []planartifact.PlanArtifactVerificationObligation{{
					ID:       "review-check",
					Kind:     "review",
					Status:   "open",
					Required: true,
				}},
				Edits: []planartifact.PlanEdit{
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
				ginkgo.Fail(fmt.Sprintf("submit_plan failed: %s", body))
			}

			return "submitted plan", nil
		case string(autonomy.TaskFamilyPlanThenExecuteReview):
			return "Decision: pass\nNo checklist.", nil
		default:
			ginkgo.Fail(fmt.Sprintf("unexpected task family %q", opts.TaskFamily))

			return "", nil
		}
	}, nil, &firstStdout, &firstStderr)
	gomega.Expect(first.ExitCode != 1).To(gomega.BeFalse(), fmt.Sprintf("first run should fail review checklist gate, got code=%d stdout=%s stderr=%s", first.ExitCode, firstStdout.String(), firstStderr.String()))
	gomega.Expect(first.ResumeStatePath == "").To(gomega.BeFalse(), fmt.Sprintf("failed run should expose a resume state path"))

	state, _, found, err := TryLoadProgressState(root, first.SessionID)
	gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("load progress state: %v", err))
	gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprintf("load progress state: %v", err))
	gomega.Expect(state.Phase).To(gomega.Equal(plancontract.PlanExecuteProgressPhaseReviewRepair), fmt.Sprintf("expected failed review progress, got phase=%s status=%s state=%#v", state.Phase, state.Status, state))
	gomega.Expect(state.Status).To(gomega.Equal(plancontract.PlanExecuteProgressStatusFailed), fmt.Sprintf("expected failed review progress, got phase=%s status=%s state=%#v", state.Phase, state.Status, state))
	gomega.Expect(state.Executor.Status).To(gomega.Equal(plancontract.PlanExecuteProgressStatusCompleted), fmt.Sprintf("executor progress should be complete before review resume, got %#v", state.Executor))
	gomega.Expect(state.Executor.Succeeded).To(gomega.Equal(1), fmt.Sprintf("executor progress should be complete before review resume, got %#v", state.Executor))
	gomega.Expect(strings.Contains(readFileStringForRunTest(filepath.Join(root, first.ResumeStatePath)), prompt)).To(gomega.BeFalse(), fmt.Sprintf("progress state must store prompt hash, not raw prompt"))

	record, recordPath, found, err := recovery.TryLoadFailureRecord(root, recovery.PlanExecuteFailureID(first.SessionID))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load recovery record: %v", err))
	gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprintf("failed run should write recovery record"))
	gomega.Expect(record.Failure.Class).To(gomega.Equal(recoverycontract.FailureClassReviewRepair), fmt.Sprintf("unexpected recovery record at %s: %#v", recordPath, record))
	gomega.Expect(record.NextAction).To(gomega.Equal(recoverycontract.NextActionReviewDecomposition), fmt.Sprintf("unexpected recovery record at %s: %#v", recordPath, record))

	var resumedStdout, resumedStderr bytes.Buffer

	reviewCalls := 0
	resumedPrompt := ""
	resumed := runForTest(cfg, Options{
		Prompt:            prompt,
		RequiredEditPaths: MultiFlag{"a.txt", "b.txt"},
		Resume:            first.SessionID,
	}, func(_ config.Config, prompt string, opts RunOptions, _ io.Writer) (string, error) {
		gomega.Expect(opts.TaskFamily != string(autonomy.TaskFamilyPlanThenExecuteReview)).To(gomega.BeFalse(), fmt.Sprintf("resume must not rerun planner/executor model phase, got task family %q", opts.TaskFamily))

		reviewCalls++

		resumedPrompt = prompt

		return "Decision: pass\n\nVerification Checklist:\n- Required behavior: a.txt changed from old to new.\n- Scope safety: no corrective edit needed.", nil
	}, nil, &resumedStdout, &resumedStderr)
	gomega.Expect(resumed.ExitCode != 0).To(gomega.BeFalse(), fmt.Sprintf("resume should pass review, got code=%d stdout=%s stderr=%s", resumed.ExitCode, resumedStdout.String(), resumedStderr.String()))
	gomega.Expect(reviewCalls != 1).To(gomega.BeFalse(), fmt.Sprintf("resume should call review once, got %d calls", reviewCalls))
	gomega.Expect(resumedPrompt).To(gomega.ContainSubstring("-old"), fmt.Sprintf("resumed review prompt should restore executor baseline diff, got:\n%s", resumedPrompt))
	gomega.Expect(resumedPrompt).To(gomega.ContainSubstring("+new"), fmt.Sprintf("resumed review prompt should restore executor baseline diff, got:\n%s", resumedPrompt))

	if got := readFileStringForRunTest(filepath.Join(root, "a.txt")); got != "new\n" {
		ginkgo.Fail(fmt.Sprintf("resume must not reapply executor edits, got %q", got))
	}
	gomega.Expect(resumed.Completion).NotTo(gomega.BeNil(), fmt.Sprintf("resume should include terminal completion"))

	coverage := map[string]plancontract.PlanExecuteRequiredPathCoverageStatus{}
	for _, item := range resumed.Completion.RequiredPathCoverage {
		coverage[item.Path] = item.Status
	}
	gomega.Expect(coverage["a.txt"]).To(gomega.Equal(plancontract.PlanExecuteRequiredPathCoverageChanged), fmt.Sprintf("resume terminal coverage = %#v, want changed/source_satisfied", resumed.Completion.RequiredPathCoverage))
	gomega.Expect(coverage["b.txt"]).To(gomega.Equal(plancontract.PlanExecuteRequiredPathCoverageSourceSatisfied), fmt.Sprintf("resume terminal coverage = %#v, want changed/source_satisfied", resumed.Completion.RequiredPathCoverage))
})

var _ = ginkgo.It("run persists review repair question as blocked progress", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("old\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed a.txt: %v", err))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	prompt := "Make a.txt say new."

	var stdout, stderr bytes.Buffer

	result := runForTest(cfg, Options{
		Prompt:     prompt,
		JSONEvents: true,
	}, func(cfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		switch opts.TaskFamily {
		case string(autonomy.TaskFamilyPlanThenExecutePlanner):
			writeRunTestPlanViaSubmitPlan(cfg, []planartifact.PlanEdit{
				{Path: "a.txt", OldText: "old\n", NewText: "new\n", Purpose: "apply requested text"},
			}, "change a.txt")

			return "submitted plan", nil
		case string(autonomy.TaskFamilyPlanThenExecuteReview):
			return "<<QUESTION>>: a.txt review found behavior outside the allowed repair scope.", nil
		default:
			ginkgo.Fail(fmt.Sprintf("unexpected task family %q", opts.TaskFamily))

			return "", nil
		}
	}, nil, &stdout, &stderr)
	gomega.Expect(result.ExitCode).To(gomega.Equal(1), fmt.Sprintf("expected blocked review result, got %#v stdout=%s stderr=%s", result, stdout.String(), stderr.String()))
	gomega.Expect(result.FailureKind).To(gomega.Equal("review_repair_blocked"), fmt.Sprintf("expected blocked review result, got %#v stdout=%s stderr=%s", result, stdout.String(), stderr.String()))

	state, _, found, err := TryLoadProgressState(root, result.SessionID)
	gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("load progress state: %v", err))
	gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprintf("load progress state: %v", err))
	gomega.Expect(state.Phase).To(gomega.Equal(plancontract.PlanExecuteProgressPhaseReviewRepair), fmt.Sprintf("expected blocked review progress, got phase=%s status=%s state=%#v", state.Phase, state.Status, state))
	gomega.Expect(state.Status).To(gomega.Equal(plancontract.PlanExecuteProgressStatusBlocked), fmt.Sprintf("expected blocked review progress, got phase=%s status=%s state=%#v", state.Phase, state.Status, state))
	gomega.Expect(state.Review.Status).To(gomega.Equal(plancontract.PlanExecuteReviewStatusBlocked), fmt.Sprintf("review blocker should be typed status, not error: %#v", state.Review))
	gomega.Expect(state.Review.StatusDetail).To(gomega.ContainSubstring("a.txt review found behavior"), fmt.Sprintf("review blocker should be typed status, not error: %#v", state.Review))
	gomega.Expect(state.Review.Error).To(gomega.Equal(""), fmt.Sprintf("review blocker should be typed status, not error: %#v", state.Review))
	gomega.Expect(strings.Contains(state.FailureReason, "a.txt review found behavior outside the allowed repair scope")).To(gomega.BeTrue(), fmt.Sprintf("review blocker should preserve full failure reason, got %q", state.FailureReason))
	gomega.Expect(strings.Contains(stdout.String(), `"type":"`+plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventReviewRepairBlocked)+`"`)).To(gomega.BeTrue(), fmt.Sprintf("expected review_repair_blocked event, got stdout=%s", stdout.String()))
	gomega.Expect(strings.Contains(stdout.String(), `"failure_reason":"\u003c\u003cQUESTION\u003e\u003e: a.txt review found behavior outside the allowed repair scope."`)).To(gomega.BeTrue(), fmt.Sprintf("expected review_repair_blocked event to include full blocker reason, got stdout=%s", stdout.String()))
})

var _ = ginkgo.It("run resume review repair uses persisted typed edit set obligations", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "feature_test.go"), []byte("package demo\n\nfunc TestFeature(t *testing.T) {}\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed feature_test.go: %v", err))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	prompt := "Add coverage for existing feature behavior."

	var firstStdout, firstStderr bytes.Buffer

	first := runForTest(cfg, Options{
		Prompt:     prompt,
		JSONEvents: true,
	}, func(cfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		switch opts.TaskFamily {
		case string(autonomy.TaskFamilyPlanThenExecutePlanner):
			writeRunTestPlanViaSubmitPlan(cfg, []planartifact.PlanEdit{
				{Path: "feature_test.go", OldText: "func TestFeature(t *testing.T) {}\n", NewText: "func TestFeature(t *testing.T) {\n\tt.Helper()\n}\n", Purpose: "add coverage detail"},
			}, "test-only coverage plan")

			return "submitted test-only plan", nil
		case string(autonomy.TaskFamilyPlanThenExecuteReview):
			return "Decision: pass\nNo checklist.", nil
		default:
			ginkgo.Fail(fmt.Sprintf("unexpected task family %q", opts.TaskFamily))

			return "", nil
		}
	}, nil, &firstStdout, &firstStderr)
	gomega.Expect(first.ExitCode != 1).To(gomega.BeFalse(), fmt.Sprintf("first run should fail review checklist gate, got code=%d stdout=%s stderr=%s", first.ExitCode, firstStdout.String(), firstStderr.String()))

	state, _, found, err := TryLoadProgressState(root, first.SessionID)
	gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("load progress state: %v", err))
	gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprintf("load progress state: %v", err))

	state.EditSetObligations = plancontract.EditSetObligations{}.With(plancontract.EditSetObligationKindImplementationEdit, "persisted_retry")
	if _, err := SaveProgressState(root, &state); err != nil {
		ginkgo.Fail(fmt.Sprintf("save progress state: %v", err))
	}

	var resumedStdout, resumedStderr bytes.Buffer

	resumedPrompt := ""
	resumed := runForTest(cfg, Options{
		Prompt:     prompt,
		Resume:     first.SessionID,
		JSONEvents: true,
	}, func(_ config.Config, prompt string, opts RunOptions, _ io.Writer) (string, error) {
		gomega.Expect(opts.TaskFamily != string(autonomy.TaskFamilyPlanThenExecuteReview)).To(gomega.BeFalse(), fmt.Sprintf("resume must run review-repair only, got %q", opts.TaskFamily))

		resumedPrompt = prompt

		return "Decision: pass\n\nVerification Checklist:\n- Required behavior: test coverage is present.\n- Scope safety: no corrective edit needed.", nil
	}, nil, &resumedStdout, &resumedStderr)
	gomega.Expect(resumed.ExitCode != 1).To(gomega.BeFalse(), fmt.Sprintf("resume should reject pass against persisted implementation obligation, got code=%d stdout=%s stderr=%s", resumed.ExitCode, resumedStdout.String(), resumedStderr.String()))
	gomega.Expect(strings.Contains(resumedStderr.String(), string(plancontract.PlanExecuteFailureMissingRequestedImplementationEdit))).To(gomega.BeTrue(), fmt.Sprintf("expected persisted implementation obligation rejection, stderr=%s", resumedStderr.String()))
	gomega.Expect(strings.Contains(resumedPrompt, "requested_implementation_edit_missing")).To(gomega.BeTrue(), fmt.Sprintf("review prompt should expose persisted implementation obligation, got:\n%s", resumedPrompt))
})

var _ = ginkgo.It("run continues review repair after source sanity failure", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/recover-source-sanity\n\ngo 1.22\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed go.mod: %v", err))
	}

	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir pkg: %v", err))
	}

	if err := os.WriteFile(filepath.Join(root, "pkg", "feature.go"), []byte("package pkg\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed feature.go: %v", err))
	}

	sourcePath := filepath.Join(root, "pkg", "feature.go")
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.CommandTimeoutSeconds = 30

	var stdout, stderr bytes.Buffer

	reviewCalled := false
	result := runForTest(cfg, Options{
		Prompt:     "Update Value.",
		JSONEvents: true,
	}, func(cfg config.Config, prompt string, opts RunOptions, _ io.Writer) (string, error) {
		switch opts.TaskFamily {
		case string(autonomy.TaskFamilyPlanThenExecutePlanner):
			writeRunTestPlanViaSubmitPlan(cfg, []planartifact.PlanEdit{
				{
					Path:    filepath.ToSlash(filepath.Join("pkg", "feature.go")),
					OldText: "return 1",
					NewText: "return missingSymbol",
					Purpose: "update Value with a compile defect",
				},
			}, "update Value with a compile defect")

			return "submitted plan", nil
		case string(autonomy.TaskFamilyPlanThenExecuteReview):
			reviewCalled = true

			if !strings.Contains(prompt, "source_sanity_failed:pkg/feature.go") {
				ginkgo.Fail(fmt.Sprintf("review prompt should expose failed source_sanity obligation, got:\n%s", prompt))
			}

			writeRunTestPlanViaSubmitPlan(cfg, []planartifact.PlanEdit{
				{
					Path:    filepath.ToSlash(filepath.Join("pkg", "feature.go")),
					OldText: "return missingSymbol",
					NewText: "return 1",
					Purpose: "repair compile failure",
				},
			}, "Verification Checklist:\n- Source sanity: repair the undefined symbol.\n- Scope safety: edit only the changed source file.")

			return "submitted repair plan", nil
		default:
			ginkgo.Fail(fmt.Sprintf("unexpected task family %q", opts.TaskFamily))

			return "", nil
		}
	}, nil, &stdout, &stderr)
	gomega.Expect(result.ExitCode != 0).To(gomega.BeFalse(), fmt.Sprintf("expected review-repair to recover source sanity failure, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String()))
	gomega.Expect(reviewCalled).To(gomega.BeTrue(), fmt.Sprintf("review-repair phase was not called"))

	if got := readFileStringForRunTest(sourcePath); strings.Contains(got, "missingSymbol") || !strings.Contains(got, "return 1") {
		ginkgo.Fail(fmt.Sprintf("expected repaired source body, got:\n%s", got))
	}
	gomega.Expect(strings.Contains(stdout.String(), `"type":"`+plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventExecutorVerificationRepairNeeded)+`"`)).To(gomega.BeTrue(), fmt.Sprintf("expected typed executor verification handoff event, got stdout=%s", stdout.String()))

	state, _, found, err := TryLoadProgressState(root, result.SessionID)
	gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("load progress state: %v", err))
	gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprintf("load progress state: %v", err))
	gomega.Expect(state.Status).To(gomega.Equal(plancontract.PlanExecuteProgressStatusCompleted), fmt.Sprintf("expected completed repaired progress, got %#v", state))
	gomega.Expect(state.Review.Status).To(gomega.Equal(plancontract.PlanExecuteReviewStatusRepaired), fmt.Sprintf("expected completed repaired progress, got %#v", state))

	if record, recordPath, found, err := recovery.TryLoadFailureRecord(root, recovery.PlanExecuteFailureID(result.SessionID)); err != nil || found {
		ginkgo.Fail(fmt.Sprintf("successful repair should not leave stale recovery record found=%v path=%s record=%#v err=%v", found, recordPath, record, err))
	}
})

var _ = ginkgo.It("run continues review repair after requested verification failure", ginkgo.Label("development-scenario", "integration", "contract"), func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/recover-requested-verification\n\ngo 1.22\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed go.mod: %v", err))
	}

	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir pkg: %v", err))
	}

	if err := os.WriteFile(filepath.Join(root, "pkg", "feature.go"), []byte("package pkg\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed feature.go: %v", err))
	}

	if err := os.WriteFile(filepath.Join(root, "pkg", "feature_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) {\n\tif Value() != 1 {\n\t\tt.Fatalf(\"unexpected value: %d\", Value())\n\t}\n}\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed feature_test.go: %v", err))
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
	result := runForTest(cfg, Options{
		Prompt:     "Update Value. Make this pass: `" + command + "`.",
		JSONEvents: true,
	}, func(cfg config.Config, prompt string, opts RunOptions, _ io.Writer) (string, error) {
		switch opts.TaskFamily {
		case string(autonomy.TaskFamilyPlanThenExecutePlanner):
			writeRunTestPlanViaSubmitPlan(cfg, []planartifact.PlanEdit{
				{
					Path:    filepath.ToSlash(filepath.Join("pkg", "feature.go")),
					OldText: "return 1",
					NewText: "return 2",
					Purpose: "change Value behavior",
				},
			}, "change Value behavior")

			return "submitted plan", nil
		case string(autonomy.TaskFamilyPlanThenExecuteReview):
			reviewCalled = true

			if !strings.Contains(prompt, "verification_failed:"+command) {
				ginkgo.Fail(fmt.Sprintf("review prompt should expose failed requested verification obligation, got:\n%s", prompt))
			}

			writeRunTestPlanViaSubmitPlan(cfg, []planartifact.PlanEdit{
				{
					Path:    filepath.ToSlash(filepath.Join("pkg", "feature.go")),
					OldText: "return 2",
					NewText: "return 1",
					Purpose: "repair requested verification failure",
				},
			}, "Verification Checklist:\n- Requested verification: restore TestValue expectation.\n- Scope safety: edit only the changed source file.")

			return "submitted repair plan", nil
		default:
			ginkgo.Fail(fmt.Sprintf("unexpected task family %q", opts.TaskFamily))

			return "", nil
		}
	}, nil, &stdout, &stderr)
	gomega.Expect(result.ExitCode != 0).To(gomega.BeFalse(), fmt.Sprintf("expected review-repair to recover requested verification failure, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String()))
	gomega.Expect(reviewCalled).To(gomega.BeTrue(), fmt.Sprintf("review-repair phase was not called"))

	if got := readFileStringForRunTest(sourcePath); strings.Contains(got, "return 2") || !strings.Contains(got, "return 1") {
		ginkgo.Fail(fmt.Sprintf("expected repaired source body, got:\n%s", got))
	}

	state, _, found, err := TryLoadProgressState(root, result.SessionID)
	gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("load progress state: %v", err))
	gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprintf("load progress state: %v", err))
	gomega.Expect(state.Status).To(gomega.Equal(plancontract.PlanExecuteProgressStatusCompleted), fmt.Sprintf("expected completed repaired progress, got %#v", state))
	gomega.Expect(state.Review.Status).To(gomega.Equal(plancontract.PlanExecuteReviewStatusRepaired), fmt.Sprintf("expected completed repaired progress, got %#v", state))
	gomega.Expect(state.Executor.Verification).NotTo(gomega.HaveLen(0), fmt.Sprintf("expected successful post-review requested verification evidence, got %#v", state.Executor.Verification))
	gomega.Expect(state.Executor.Verification[len(state.Executor.Verification)-1].Status).To(gomega.Equal(plancontract.PhaseEvidenceStatusOK), fmt.Sprintf("expected successful post-review requested verification evidence, got %#v", state.Executor.Verification))
})

var _ = ginkgo.It("run injects explicit verification commands into executor plan", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/explicit-verification\n\ngo 1.22\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed go.mod: %v", err))
	}

	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir pkg: %v", err))
	}

	if err := os.WriteFile(filepath.Join(root, "pkg", "feature.go"), []byte("package pkg\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed feature.go: %v", err))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.CommandTimeoutSeconds = 30

	var stdout, stderr bytes.Buffer

	result := runForTest(cfg, Options{
		Prompt:                           "Update Value without mentioning any verification command.",
		JSONEvents:                       true,
		SkipReviewRepair:                 true,
		VerificationPlanExplicitCommands: []string{"go vet ./..."},
	}, func(cfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		gomega.Expect(opts.TaskFamily != string(autonomy.TaskFamilyPlanThenExecutePlanner)).To(gomega.BeFalse(), fmt.Sprintf("unexpected task family %q", opts.TaskFamily))

		writeRunTestPlanViaSubmitPlan(cfg, []planartifact.PlanEdit{{
			Path:    filepath.ToSlash(filepath.Join("pkg", "feature.go")),
			OldText: "return 1",
			NewText: "return 2",
			Purpose: "change Value behavior",
		}}, "change Value behavior")

		return "submitted plan", nil
	}, nil, &stdout, &stderr)
	gomega.Expect(result.ExitCode != 0).To(gomega.BeFalse(), fmt.Sprintf("Run exit=%d result=%#v stdout=%s stderr=%s", result.ExitCode, result, stdout.String(), stderr.String()))
	gomega.Expect(result.Completion).NotTo(gomega.BeNil(), fmt.Sprintf("expected terminal completion record, result=%#v stdout=%s", result, stdout.String()))

	var vetCommand *plancontract.VerificationCommand
	for i := range result.Completion.VerificationCommands {
		if result.Completion.VerificationCommands[i].Command == "go vet ./..." {
			vetCommand = &result.Completion.VerificationCommands[i]
			break
		}
	}
	gomega.Expect(vetCommand).NotTo(gomega.BeNil(), fmt.Sprintf("explicit verification command was not planned, commands=%#v", result.Completion.VerificationCommands))
	gomega.Expect(vetCommand.Source != plancontract.VerificationCommandSourceExplicit).To(gomega.BeFalse(), fmt.Sprintf("explicit command source = %q, want %q", vetCommand.Source, plancontract.VerificationCommandSourceExplicit))

	for _, check := range result.Completion.Verification {
		if check.Command == "go vet ./..." && check.Status == "ok" {
			return
		}
	}
	ginkgo.Fail(fmt.Sprintf("explicit verification command did not pass, verification=%#v", result.Completion.Verification))
})

var _ = ginkgo.It("run executes requested real pytest verification for greenfield atom", ginkgo.Label("development-scenario", "e2e", "integration", "optional-toolchain"), func() {
	if _, err := exec.LookPath("pytest"); err != nil {
		ginkgo.Skip("pytest is not available on PATH")
	}

	root := ginkgo.GinkgoT().TempDir()

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.CommandTimeoutSeconds = 30

	var stdout, stderr bytes.Buffer

	result := runForTest(cfg, Options{
		Prompt: strings.Join([]string{
			"Implement the LLM Brain query handler.",
			"Required verification command: run `pytest tests/test_query_handler.py` after edits.",
		}, "\n"),
		JSONEvents:        true,
		SkipReviewRepair:  true,
		RequiredEditPaths: MultiFlag{"llm_brain/query_handler.py", "tests/test_query_handler.py"},
		AuthorizedNewFiles: []string{
			"llm_brain/query_handler.py",
			"tests/test_query_handler.py",
		},
	}, func(cfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		gomega.Expect(opts.TaskFamily != string(autonomy.TaskFamilyPlanThenExecutePlanner)).To(gomega.BeFalse(), fmt.Sprintf("unexpected task family %q", opts.TaskFamily))

		writeRunTestPlanViaSubmitPlan(cfg, []planartifact.PlanEdit{
			{
				Path:       "tests/test_query_handler.py",
				OldText:    "",
				NewText:    "from llm_brain.query_handler import QueryHandler\n\n\ndef test_query_handler_smoke():\n    assert QueryHandler().handle_query('question') == 'ok'\n",
				ReplaceAll: true,
				Purpose:    "create pytest smoke coverage for the query handler",
			},
			{
				Path:       "llm_brain/query_handler.py",
				OldText:    "",
				NewText:    "class QueryHandler:\n    def handle_query(self, query: str) -> str:\n        return 'ok'\n",
				ReplaceAll: true,
				Purpose:    "create the query handler implementation",
			},
		}, "create greenfield query handler and test")

		return "submitted plan", nil
	}, nil, &stdout, &stderr)
	gomega.Expect(result.ExitCode != 0).To(gomega.BeFalse(), fmt.Sprintf("Run exit=%d result=%#v stdout=%s stderr=%s", result.ExitCode, result, stdout.String(), stderr.String()))
	gomega.Expect(result.Completion).NotTo(gomega.BeNil(), fmt.Sprintf("expected terminal completion record, result=%#v stdout=%s", result, stdout.String()))

	command := "pytest tests/test_query_handler.py"
	gomega.Expect(planExecuteRunTestCompletionHasCommand(result.Completion.VerificationCommands, command)).To(gomega.BeTrue(), fmt.Sprintf("requested pytest command was not planned, commands=%#v", result.Completion.VerificationCommands))
	gomega.Expect(planExecuteRunTestCompletionHasPassedVerification(result.Completion.Verification, command)).To(gomega.BeTrue(), fmt.Sprintf("requested pytest command did not pass, verification=%#v", result.Completion.Verification))
})

var _ = ginkgo.It("run keeps source sanity failure when review repair unavailable", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/recover-source-sanity-unavailable\n\ngo 1.22\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed go.mod: %v", err))
	}

	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir pkg: %v", err))
	}

	if err := os.WriteFile(filepath.Join(root, "pkg", "feature.go"), []byte("package pkg\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed feature.go: %v", err))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	cfg.Workspace.CommandTimeoutSeconds = 30

	var stdout, stderr bytes.Buffer

	result := runForTest(cfg, Options{
		Prompt:     "Update Value.",
		JSONEvents: true,
	}, func(cfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		switch opts.TaskFamily {
		case string(autonomy.TaskFamilyPlanThenExecutePlanner):
			writeRunTestPlanViaSubmitPlan(cfg, []planartifact.PlanEdit{
				{
					Path:    filepath.ToSlash(filepath.Join("pkg", "feature.go")),
					OldText: "return 1",
					NewText: "return missingSymbol",
					Purpose: "update Value with a compile defect",
				},
			}, "update Value with a compile defect")

			return "submitted plan", nil
		case string(autonomy.TaskFamilyPlanThenExecuteReview):
			return "", errors.New("review model unavailable")
		default:
			ginkgo.Fail(fmt.Sprintf("unexpected task family %q", opts.TaskFamily))

			return "", nil
		}
	}, nil, &stdout, &stderr)
	gomega.Expect(result.ExitCode != 1).To(gomega.BeFalse(), fmt.Sprintf("expected unresolved source sanity failure, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String()))
	gomega.Expect(result.FailureKind != string(plancontract.PlanExecuteFailureSourceSanityFailed)).To(gomega.BeFalse(), fmt.Sprintf("expected source_sanity_failed result, got %#v", result))

	state, _, found, err := TryLoadProgressState(root, result.SessionID)
	gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("load progress state: %v", err))
	gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprintf("load progress state: %v", err))
	gomega.Expect(state.Status).To(gomega.Equal(plancontract.PlanExecuteProgressStatusFailed), fmt.Sprintf("expected failed source sanity progress after degraded review, got %#v", state))
	gomega.Expect(state.FailureKind).To(gomega.Equal(plancontract.PlanExecuteFailureSourceSanityFailed), fmt.Sprintf("expected failed source sanity progress after degraded review, got %#v", state))
	gomega.Expect(state.Review.Status).To(gomega.Equal(plancontract.PlanExecuteReviewStatusDegraded), fmt.Sprintf("expected failed source sanity progress after degraded review, got %#v", state))
	gomega.Expect(state.Review.StatusDetail).To(gomega.Equal("review unavailable"), fmt.Sprintf("expected failed source sanity progress after degraded review, got %#v", state))

	record, _, found, err := recovery.TryLoadFailureRecord(root, recovery.PlanExecuteFailureID(result.SessionID))
	gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("expected terminal failure record found=%v err=%v", found, err))
	gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprintf("expected terminal failure record found=%v err=%v", found, err))
	gomega.Expect(record.Failure.Class != recoverycontract.FailureClassVerification).To(gomega.BeFalse(), fmt.Sprintf("expected verification failure class, got %#v", record.Failure))
})

var _ = ginkgo.It("run rejects initial plan when requested test edit missing before writes", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "pkg.go"), []byte("package demo\n\nfunc Hardened() bool { return false }\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed pkg.go: %v", err))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer

	result := runForTest(cfg, Options{
		Prompt:             "Implement the hardening and add regression tests proving the edge case is rejected.",
		JSONEvents:         true,
		SkipReviewRepair:   true,
		EditSetObligations: plancontract.EditSetObligations{}.With(plancontract.EditSetObligationKindTestEdit, "test"),
	}, func(cfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		gomega.Expect(opts.TaskFamily != string(autonomy.TaskFamilyPlanThenExecutePlanner)).To(gomega.BeFalse(), fmt.Sprintf("unexpected task family %q", opts.TaskFamily))

		writeRunTestPlanViaSubmitPlan(cfg, []planartifact.PlanEdit{
			{Path: "pkg.go", OldText: "return false", NewText: "return true", Purpose: "harden behavior"},
		}, "production-only plan")

		return "submitted production-only plan", nil
	}, nil, &stdout, &stderr)
	gomega.Expect(result.ExitCode != 1).To(gomega.BeFalse(), fmt.Sprintf("expected missing test edit rejection, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String()))
	gomega.Expect(result.FailureKind).To(gomega.Equal("missing_requested_test_edit"), fmt.Sprintf("expected missing_requested_test_edit failure, result=%#v stderr=%s", result, stderr.String()))
	gomega.Expect(stderr.String()).To(gomega.ContainSubstring("missing_requested_test_edit"), fmt.Sprintf("expected missing_requested_test_edit failure, result=%#v stderr=%s", result, stderr.String()))

	if got := readFileStringForRunTest(filepath.Join(root, "pkg.go")); strings.Contains(got, "return true") {
		ginkgo.Fail(fmt.Sprintf("production edit must not be applied after plan rejection, got:\n%s", got))
	}

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	event, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventPlannerPlanRejected))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected planner_plan_rejected event, got %s", stdout.String()))
	gomega.Expect(event["reason"] != "missing_requested_test_edit").To(gomega.BeFalse(), fmt.Sprintf("expected missing_requested_test_edit reason, got %#v", event))
})

var _ = ginkgo.It("run rejects initial plan when requested implementation edit missing before writes", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "feature.go"), []byte("package demo\n\nfunc Enabled() bool { return false }\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed feature.go: %v", err))
	}

	if err := os.WriteFile(filepath.Join(root, "feature_test.go"), []byte("package demo\n\nfunc TestEnabled(t *testing.T) {}\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed feature_test.go: %v", err))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer

	result := runForTest(cfg, Options{
		Prompt:             "Implement the feature. Start by adding failing tests, then implement.",
		JSONEvents:         true,
		SkipReviewRepair:   true,
		EditSetObligations: plancontract.EditSetObligations{}.With(plancontract.EditSetObligationKindImplementationEdit, "test"),
	}, func(cfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		gomega.Expect(opts.TaskFamily != string(autonomy.TaskFamilyPlanThenExecutePlanner)).To(gomega.BeFalse(), fmt.Sprintf("unexpected task family %q", opts.TaskFamily))

		writeRunTestPlanViaSubmitPlan(cfg, []planartifact.PlanEdit{
			{Path: "feature_test.go", OldText: "func TestEnabled(t *testing.T) {}\n", NewText: "func TestEnabled(t *testing.T) {\n\tif !Enabled() {\n\t\tt.Fatal(\"disabled\")\n\t}\n}\n", Purpose: "add feature test"},
		}, "test-only plan")

		return "submitted test-only plan", nil
	}, nil, &stdout, &stderr)
	gomega.Expect(result.ExitCode != 1).To(gomega.BeFalse(), fmt.Sprintf("expected missing implementation edit rejection, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String()))
	gomega.Expect(result.FailureKind).To(gomega.Equal("missing_requested_implementation_edit"), fmt.Sprintf("expected missing_requested_implementation_edit failure, result=%#v stderr=%s", result, stderr.String()))
	gomega.Expect(stderr.String()).To(gomega.ContainSubstring("missing_requested_implementation_edit"), fmt.Sprintf("expected missing_requested_implementation_edit failure, result=%#v stderr=%s", result, stderr.String()))

	if got := readFileStringForRunTest(filepath.Join(root, "feature_test.go")); strings.Contains(got, "t.Fatal") {
		ginkgo.Fail(fmt.Sprintf("test-only edit must not be applied after plan rejection, got:\n%s", got))
	}

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	event, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventPlannerPlanRejected))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected planner_plan_rejected event, got %s", stdout.String()))
	gomega.Expect(event["reason"] != "missing_requested_implementation_edit").To(gomega.BeFalse(), fmt.Sprintf("expected missing_requested_implementation_edit reason, got %#v", event))
})

var _ = ginkgo.It("run rejects missing edit path without new file authorization before writes", func() {
	root := ginkgo.GinkgoT().TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer

	missingPath := "project/scripts/economy/score_breakdown.gd"
	result := runForTest(cfg, Options{
		Prompt:           "Update the existing masterwork tooltip to use current contributor fields if needed.",
		JSONEvents:       true,
		SkipReviewRepair: true,
		RequiredEditPaths: MultiFlag{
			missingPath,
		},
	}, func(cfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		gomega.Expect(opts.TaskFamily != string(autonomy.TaskFamilyPlanThenExecutePlanner)).To(gomega.BeFalse(), fmt.Sprintf("unexpected task family %q", opts.TaskFamily))

		writeRunTestPlanViaSubmitPlan(cfg, []planartifact.PlanEdit{{
			Path:       missingPath,
			OldText:    "",
			NewText:    "extends RefCounted\n",
			ReplaceAll: true,
			Purpose:    "invented helper path",
		}}, "invented missing helper")

		return "submitted invented helper path", nil
	}, nil, &stdout, &stderr)
	gomega.Expect(result.ExitCode != 1).To(gomega.BeFalse(), fmt.Sprintf("expected missing edit path rejection, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String()))
	gomega.Expect(result.FailureKind != "missing_edit_path_without_new_file_authorization").To(gomega.BeFalse(), fmt.Sprintf("unexpected failure kind: %#v", result))
	gomega.Expect(stderr.String()).To(gomega.Or(
		gomega.ContainSubstring("missing edit path"),
		gomega.ContainSubstring("new-file authorization"),
	), fmt.Sprintf("stderr should explain missing-path/new-file rejection, got %s", stderr.String()))

	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(missingPath))); !os.IsNotExist(err) {
		ginkgo.Fail(fmt.Sprintf("missing path must not be created, stat err=%v", err))
	}

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	event, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventPlannerPlanRejected))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected planner_plan_rejected event, got %s", stdout.String()))
	gomega.Expect(event["reason"] != "plan would create missing edit path without new-file authorization").To(gomega.BeFalse(), fmt.Sprintf("unexpected rejection event: %#v", event))
})

var _ = ginkgo.It("run rejects prompt prose new file without typed authorization", func() {
	root := ginkgo.GinkgoT().TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer

	missingPath := "pkg/new_helper.go"
	result := runForTest(cfg, Options{
		Prompt:           "Create new file pkg/new_helper.go with the following content.",
		JSONEvents:       true,
		SkipReviewRepair: true,
		RequiredEditPaths: MultiFlag{
			missingPath,
		},
	}, func(cfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		gomega.Expect(opts.TaskFamily != string(autonomy.TaskFamilyPlanThenExecutePlanner)).To(gomega.BeFalse(), fmt.Sprintf("unexpected task family %q", opts.TaskFamily))

		writeRunTestPlanViaSubmitPlan(cfg, []planartifact.PlanEdit{{
			Path:       missingPath,
			OldText:    "",
			NewText:    "package pkg\n",
			ReplaceAll: true,
			Purpose:    "create helper from prompt prose only",
		}}, "prompt prose requested new helper")

		return "submitted prompt-prose helper path", nil
	}, nil, &stdout, &stderr)
	gomega.Expect(result.ExitCode).To(gomega.Equal(1), fmt.Sprintf("expected typed new-file authorization rejection, got result=%#v stdout=%s stderr=%s", result, stdout.String(), stderr.String()))
	gomega.Expect(result.FailureKind).To(gomega.Equal(string(plancontract.PlanExecuteFailureMissingEditPathWithoutNewFileAuth)), fmt.Sprintf("expected typed new-file authorization rejection, got result=%#v stdout=%s stderr=%s", result, stdout.String(), stderr.String()))

	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(missingPath))); !os.IsNotExist(err) {
		ginkgo.Fail(fmt.Sprintf("prompt prose must not create missing path, stat err=%v stdout=%s", err, stdout.String()))
	}
})

var _ = ginkgo.It("preflight accepts typed authorized new file without prompt prose", func() {
	root := ginkgo.GinkgoT().TempDir()
	newPath := "project/scripts/economy/score_breakdown.gd"
	plan := planartifact.PlanArtifact{
		Edits: []planartifact.PlanEdit{{
			Path:       newPath,
			OldText:    "",
			NewText:    "extends RefCounted\n",
			ReplaceAll: true,
			Purpose:    "create typed-authorized helper path",
		}},
	}

	if mismatch := preflightPlanMissingPathAuthorization(root, []string{newPath}, plan); mismatch != nil {
		ginkgo.Fail(fmt.Sprintf("typed authorized new file should pass preflight without prompt prose, got %#v", mismatch))
	}
})

var _ = ginkgo.It("executor creates typed authorized new required file", func() {
	root := ginkgo.GinkgoT().TempDir()
	newPath := "project/scripts/economy/score_breakdown.gd"
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := planartifact.PlanArtifact{
		Edits: []planartifact.PlanEdit{{
			Path:       newPath,
			OldText:    "",
			NewText:    "extends RefCounted\n",
			ReplaceAll: true,
			Purpose:    "create typed-authorized helper path",
		}},
	}

	var stdout bytes.Buffer
	report, err := runExecutorPhaseForTest(cfg, plan, ExecutorPhaseOptions{
		RequiredEditPaths:  []string{newPath},
		AuthorizedNewFiles: []string{newPath},
		VerificationPlan:   &VerificationPlan{},
		JSONEvents:         true,
		Stdout:             &stdout,
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("executor rejected typed-authorized new file: %v stdout=%s", err, stdout.String()))
	gomega.Expect(report.Succeeded).To(gomega.Equal(1), fmt.Sprintf("unexpected executor report: %+v stdout=%s", report, stdout.String()))
	gomega.Expect(report.Failed).To(gomega.Equal(0), fmt.Sprintf("unexpected executor report: %+v stdout=%s", report, stdout.String()))

	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(newPath)))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected authorized new file to be created: %v stdout=%s", err, stdout.String()))
	gomega.Expect(string(data) != "extends RefCounted\n").To(gomega.BeFalse(), fmt.Sprintf("new file content = %q", string(data)))
})

var _ = ginkgo.It("run executor phase rejects existing source whole file rewrite before dispatch", func() {
	root := ginkgo.GinkgoT().TempDir()
	sourcePath := "native/src/simulation/world/collapse_event.h"
	abs := filepath.Join(root, filepath.FromSlash(sourcePath))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir source dir: %v", err))
	}
	original := "#pragma once\n\nnamespace colony {\nstruct CollapseEvent {};\n}\n"
	if err := os.WriteFile(abs, []byte(original), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed source: %v", err))
	}

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{{
			Path:       sourcePath,
			NewText:    "#ifndef SUROCCUS_SIMULATION_WORLD_COLLAPSE_EVENT_H\n#define SUROCCUS_SIMULATION_WORLD_COLLAPSE_EVENT_H\nnamespace suroccus { class CollapseEvent {}; }\n#endif\n",
			ReplaceAll: true,
			Purpose:    "rewrite existing collapse header",
		}},
	}

	var stdout bytes.Buffer
	_, err := runExecutorPhaseForTest(cfg, plan, ExecutorPhaseOptions{
		RequiredEditPaths:    []string{sourcePath},
		WriteAllowedPaths:    []string{sourcePath},
		WriteAllowedExplicit: true,
		VerificationPlan:     &VerificationPlan{},
		JSONEvents:           true,
		Stdout:               &stdout,
	})
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected existing source whole-file rewrite preflight rejection"))
	gomega.Expect(strings.Contains(err.Error(), "existing source file")).To(gomega.BeTrue(), fmt.Sprintf("expected existing source rewrite rejection, got %v stdout=%s", err, stdout.String()))

	if got := readFileStringForRunTest(abs); got != original {
		ginkgo.Fail(fmt.Sprintf("source file must remain unchanged, got:\n%s", got))
	}

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	event, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventPreflightFailed))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected preflight_failed event for existing source rewrite, got events=%#v stdout=%s", events, stdout.String()))
	gomega.Expect(fmt.Sprint(event["reason"])).To(gomega.ContainSubstring("existing source file"), fmt.Sprintf("expected preflight_failed event for existing source rewrite, got events=%#v stdout=%s", events, stdout.String()))
})

var _ = ginkgo.It("run executor phase rejects large existing source line range collapse before dispatch", func() {
	root := ginkgo.GinkgoT().TempDir()
	sourcePath := "native/src/core/colony_sim_tick.cpp"
	abs := filepath.Join(root, filepath.FromSlash(sourcePath))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir source dir: %v", err))
	}

	lines := []string{"#include \"core/colony_sim.h\""}
	for i := 0; i < 80; i++ {
		lines = append(lines, fmt.Sprintf("static int helper_%02d() { return %d; }", i, i))
	}
	lines = append(lines, "int keep() { return helper_00(); }", "")
	original := strings.Join(lines, "\n")
	if err := os.WriteFile(abs, []byte(original), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed source: %v", err))
	}

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{{
			Path:      sourcePath,
			StartLine: 1,
			EndLine:   80,
			Content:   "#include \"core/colony_sim_tick.h\"\n#include \"simulation/world/collapse_event.h\"\n\nstatic CollapseEvent g_collapse_event;\n",
			Purpose:   "collapse source prefix to a static helper",
		}},
	}

	var stdout bytes.Buffer
	_, err := runExecutorPhaseForTest(cfg, plan, ExecutorPhaseOptions{
		RequiredEditPaths:    []string{sourcePath},
		WriteAllowedPaths:    []string{sourcePath},
		WriteAllowedExplicit: true,
		VerificationPlan:     &VerificationPlan{},
		JSONEvents:           true,
		Stdout:               &stdout,
	})
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected large source line-range collapse preflight rejection"))
	gomega.Expect(strings.Contains(err.Error(), "would collapse")).To(gomega.BeTrue(), fmt.Sprintf("expected destructive line-range rejection, got %v stdout=%s", err, stdout.String()))

	if got := readFileStringForRunTest(abs); got != original {
		ginkgo.Fail(fmt.Sprintf("source file must remain unchanged, got:\n%s", got))
	}
})

var _ = ginkgo.It("run executor phase rejects mid file preprocessor line range before dispatch", func() {
	root := ginkgo.GinkgoT().TempDir()
	sourcePath := "native/src/core/colony_sim_tick.cpp"
	abs := filepath.Join(root, filepath.FromSlash(sourcePath))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir source dir: %v", err))
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
		ginkgo.Fail(fmt.Sprintf("seed source: %v", err))
	}

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{{
			Path:      sourcePath,
			StartLine: 3,
			EndLine:   5,
			Content:   "#include \"../simulation/world/collapse_event.h\"\n\nclass CollapsedSupport {\npublic:\n    CollapseEvent collapse_event;\n};\n",
			Purpose:   "add collapse event include and helper",
		}},
	}

	var stdout bytes.Buffer
	_, err := runExecutorPhaseForTest(cfg, plan, ExecutorPhaseOptions{
		RequiredEditPaths:    []string{sourcePath},
		WriteAllowedPaths:    []string{sourcePath},
		WriteAllowedExplicit: true,
		VerificationPlan:     &VerificationPlan{},
		JSONEvents:           true,
		Stdout:               &stdout,
	})
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected mid-file preprocessor line-range preflight rejection"))
	gomega.Expect(strings.Contains(err.Error(), "file-scope preprocessor directive")).To(gomega.BeTrue(), fmt.Sprintf("expected top-level construct rejection, got %v stdout=%s", err, stdout.String()))

	if got := readFileStringForRunTest(abs); got != original {
		ginkgo.Fail(fmt.Sprintf("source file must remain unchanged, got:\n%s", got))
	}
})

var _ = ginkgo.It("run executor phase rejects anchor preserving mid file preprocessor line range before dispatch", func() {
	root := ginkgo.GinkgoT().TempDir()
	sourcePath := "native/src/core/colony_sim_tick.cpp"
	abs := filepath.Join(root, filepath.FromSlash(sourcePath))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir source dir: %v", err))
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
		ginkgo.Fail(fmt.Sprintf("seed source: %v", err))
	}

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{{
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
	_, err := runExecutorPhaseForTest(cfg, plan, ExecutorPhaseOptions{
		RequiredEditPaths:    []string{sourcePath},
		WriteAllowedPaths:    []string{sourcePath},
		WriteAllowedExplicit: true,
		VerificationPlan:     &VerificationPlan{},
		JSONEvents:           true,
		Stdout:               &stdout,
	})
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected anchor-preserving mid-file preprocessor preflight rejection"))
	gomega.Expect(strings.Contains(err.Error(), "file-scope preprocessor directive")).To(gomega.BeTrue(), fmt.Sprintf("expected top-level construct rejection, got %v stdout=%s", err, stdout.String()))

	if got := readFileStringForRunTest(abs); got != original {
		ginkgo.Fail(fmt.Sprintf("source file must remain unchanged, got:\n%s", got))
	}
})

var _ = ginkgo.It("parse options accepts authorized new file flag", func() {
	var stderr bytes.Buffer

	opts, err := ParseOptions([]string{
		"-prompt", "implement",
		"-authorized-new-file", "pkg/new_helper.go",
		"-authorized-new-file", "pkg/new_helper_test.go",
	}, &stderr)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ParseOptions returned error: %v stderr=%s", err, stderr.String()))
	gomega.Expect(slices.Equal(opts.AuthorizedNewFiles, []string{"pkg/new_helper.go", "pkg/new_helper_test.go"})).To(gomega.BeTrue(), fmt.Sprintf("authorized new files = %#v", opts.AuthorizedNewFiles))
})

var _ = ginkgo.It("run executor phase rejects plan outside write envelope before writes", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "approved.txt"), []byte("approved\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed approved.txt: %v", err))
	}

	if err := os.WriteFile(filepath.Join(root, "generated.txt"), []byte("generated\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed generated.txt: %v", err))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{{
			Path:    "generated.txt",
			OldText: "generated",
			NewText: "changed",
			Purpose: "generated atom tried to widen scope",
		}},
	}

	var stdout bytes.Buffer

	_, err := runExecutorPhaseForTest(cfg, plan, ExecutorPhaseOptions{
		RequiredEditPaths:    []string{"generated.txt"},
		WriteAllowedPaths:    []string{"approved.txt"},
		WriteAllowedExplicit: true,
		VerificationPlan:     &VerificationPlan{},
		JSONEvents:           true,
		Stdout:               &stdout,
	})
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected write envelope preflight rejection"))
	gomega.Expect(strings.Contains(err.Error(), "write_allowed_paths")).To(gomega.BeTrue(), fmt.Sprintf("expected write_allowed_paths rejection, got %v", err))

	if got := readFileStringForRunTest(filepath.Join(root, "generated.txt")); got != "generated\n" {
		ginkgo.Fail(fmt.Sprintf("out-of-envelope edit must not be applied, got:\n%s", got))
	}

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	event, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventPreflightFailed))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected preflight_failed event, got %s", stdout.String()))
	gomega.Expect(event["reason"] != "plan edit path is outside write_allowed_paths envelope").To(gomega.BeFalse(), fmt.Sprintf("unexpected rejection event: %#v", event))
})

var _ = ginkgo.It("run executor phase rejects missing path without new file authorization", func() {
	root := ginkgo.GinkgoT().TempDir()
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{{
			Path:       "generated.txt",
			ReplaceAll: true,
			NewText:    "generated\n",
			Purpose:    "create generated file",
		}},
	}

	var stdout bytes.Buffer
	_, err := runExecutorPhaseForTest(cfg, plan, ExecutorPhaseOptions{
		RequiredEditPaths: []string{"generated.txt"},
		VerificationPlan:  &VerificationPlan{},
		JSONEvents:        true,
		Stdout:            &stdout,
	})
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected missing path authorization rejection"))
	gomega.Expect(strings.Contains(err.Error(), "authorized_new_files")).To(gomega.BeTrue(), fmt.Sprintf("expected authorized_new_files rejection, got %v", err))

	if _, statErr := os.Stat(filepath.Join(root, "generated.txt")); !os.IsNotExist(statErr) {
		ginkgo.Fail(fmt.Sprintf("generated file should not be written, stat err=%v", statErr))
	}
})

var _ = ginkgo.It("run plan approval authorizes approved new file path", ginkgo.Label("development-scenario", "integration", "contract"), func() {
	root := ginkgo.GinkgoT().TempDir()
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	var stdout, stderr bytes.Buffer
	approvalRequests := 0
	sessionID := "approval-new-file-test"

	result := runForTest(cfg, Options{
		Prompt:            "Create generated.txt.",
		SessionID:         sessionID,
		RequiredEditPaths: MultiFlag{"generated.txt"},
		JSONEvents:        true,
		OnPlanApproval: func(_ context.Context, req PlanApprovalRequest) PlanApprovalDecision {
			approvalRequests++
			gomega.Expect(slices.Equal(req.Paths, []string{"generated.txt"})).To(gomega.BeTrue(), fmt.Sprintf("approval paths = %#v, want generated.txt", req.Paths))

			progressPath, _, err := progressStatePath(root, sessionID)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("progress state path: %v", err))

			body, err := os.ReadFile(progressPath)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("approval exposed before durable progress state: %v", err))

			var state ProgressState
			if err := json.Unmarshal(body, &state); err != nil {
				ginkgo.Fail(fmt.Sprintf("decode progress state during approval: %v", err))
			}
			gomega.Expect(state.PlanPath == "").To(gomega.BeFalse(), fmt.Sprintf("approval exposed before progress state recorded plan path: %#v", state))

			return PlanApprovalDecision{Approved: true}
		},
	}, func(callCfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
		switch opts.TaskFamily {
		case string(autonomy.TaskFamilyPlanThenExecutePlanner):
			mgr := workspace.NewManager(callCfg.Workspace)
			res := mgr.Execute(context.Background(), workspace.ToolRequest{
				Tool:      workspace.ToolNameSubmitPlan,
				Rationale: "Create generated.txt.",
				Edits: []planartifact.PlanEdit{{
					Path:       "generated.txt",
					ReplaceAll: true,
					NewText:    "generated\n",
					Purpose:    "create generated file",
				}},
			})
			if strings.TrimSpace(res.Error) != "" {
				return "", fmt.Errorf("submit_plan: %s", res.Error)
			}
			return "Plan submitted.", nil
		case string(autonomy.TaskFamilyPlanThenExecuteReview):
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: generated.txt exists.\n- Scope safety: changed path is limited to generated.txt.", nil
		default:
			return "", fmt.Errorf("unexpected task family %q", opts.TaskFamily)
		}
	}, nil, &stdout, &stderr)
	gomega.Expect(result.ExitCode != 0).To(gomega.BeFalse(), fmt.Sprintf("Run exit=%d result=%#v stdout=%s stderr=%s", result.ExitCode, result, stdout.String(), stderr.String()))
	gomega.Expect(approvalRequests != 1).To(gomega.BeFalse(), fmt.Sprintf("approval requests = %d, want 1", approvalRequests))

	if got := readFileStringForRunTest(filepath.Join(root, "generated.txt")); got != "generated\n" {
		ginkgo.Fail(fmt.Sprintf("generated file = %q, want generated", got))
	}

	storedPlan, err := workspace.LoadPlanArtifact(filepath.Join(root, ".idiom", "plans", "last.plan.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load approved plan artifact: %v", err))
	gomega.Expect(storedPlan.Approval.Status).To(gomega.Equal(planartifact.ApprovalStatusApproved), fmt.Sprintf("stored approval = %#v, want approved generated.txt", storedPlan.Approval))
	gomega.Expect(storedPlan.Approval.ExecutionPermission).To(gomega.Equal(planartifact.ExecutionPermissionExecuteAll), fmt.Sprintf("stored approval = %#v, want approved generated.txt", storedPlan.Approval))
	gomega.Expect(storedPlan.Approval.ApprovedPaths).To(gomega.Equal([]string{"generated.txt"}), fmt.Sprintf("stored approval = %#v, want approved generated.txt", storedPlan.Approval))
})

var _ = ginkgo.It("review repair non interactive does not trust self approved repair plan for new file", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "existing.txt"), []byte("old\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed existing file: %v", err))
	}

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	originalPlan := planartifact.NormalizeForSubmit(planartifact.PlanArtifact{
		Rationale: "Original implementation missed the generated file.",
		Task:      planartifact.PlanArtifactTask{Summary: "review repair new file authorization"},
		Contract: planartifact.PlanArtifactContract{
			RequiredEditPaths: []string{"existing.txt", "generated.txt"},
		},
		Paths: planartifact.PlanArtifactPaths{
			Required: []string{"existing.txt", "generated.txt"},
			Editable: []string{"existing.txt"},
			Changed:  []string{"existing.txt"},
		},
		Edits: []planartifact.PlanEdit{{
			Path:    "existing.txt",
			OldText: "old\n",
			NewText: "old\n",
			Purpose: "leave existing implementation unchanged for fixture",
		}},
	}, planartifact.NormalizeForSubmitOptions{
		PlanID:    "original-plan",
		SessionID: "original-session",
		Created:   time.Now().UTC().Format(time.RFC3339Nano),
	})
	baseline := CaptureReviewRepairBaseline(root, []string{"existing.txt"})
	reviewSessionID := "self-approved-repair"

	var stdout bytes.Buffer
	report, err := runReviewRepairPhaseForTest(
		cfg,
		"Create existing.txt and generated.txt.",
		originalPlan,
		[]string{"existing.txt", "generated.txt"},
		&baseline,
		ExecutorPhaseReport{Succeeded: 1},
		Options{
			ReviewSessionID: reviewSessionID,
			JSONEvents:      true,
		},
		&stdout,
		func(callCfg config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
			created := time.Now().UTC().Format(time.RFC3339Nano)
			repairPlan := planartifact.NormalizeForSubmit(planartifact.PlanArtifact{
				Rationale: "Verification Checklist:\n- generated.txt is created.\n- Scope safety: only generated.txt is added.",
				Task:      planartifact.PlanArtifactTask{Summary: "create missing generated file"},
				Contract: planartifact.PlanArtifactContract{
					Kind:              planartifact.ContractKindPlanThenExecute,
					StrategyOwner:     planartifact.OwnerModel,
					ContractOwner:     planartifact.OwnerHarness,
					PermissionProfile: planartifact.PermissionProfilePlanThenExecute,
					VerificationOwner: planartifact.OwnerPlanner,
					RequiredEditPaths: []string{"generated.txt"},
				},
				Paths: planartifact.PlanArtifactPaths{
					Required: []string{"generated.txt"},
					Editable: []string{"generated.txt"},
					Changed:  []string{"generated.txt"},
				},
				Approval: planartifact.PlanArtifactApproval{
					Required:            true,
					Status:              planartifact.ApprovalStatusApproved,
					Mode:                planartifact.ApprovalModePlanThenExecute,
					ApprovedBy:          "model-authored-fixture",
					ApprovedAt:          created,
					ApprovedPaths:       []string{"generated.txt"},
					ExecutionPermission: planartifact.ExecutionPermissionExecuteAll,
				},
				Handoff: planartifact.PlanArtifactHandoff{
					From:   planartifact.OwnerPlanner,
					To:     planartifact.HandoffTargetPlanExecuteExecutor,
					Status: planartifact.HandoffStatusReady,
					Ready:  true,
				},
				VerificationObligations: []planartifact.PlanArtifactVerificationObligation{{
					ID:       "verification-1",
					Kind:     planartifact.VerificationKindExecutorLaneSanity,
					Owner:    planartifact.OwnerPlanner,
					Status:   planartifact.VerificationStatusOpen,
					Required: true,
				}},
				Edits: []planartifact.PlanEdit{{
					Path:       "generated.txt",
					ReplaceAll: true,
					NewText:    "generated\n",
					Purpose:    "create the missing required file",
				}},
			}, planartifact.NormalizeForSubmitOptions{
				PlanID:    "self-approved-repair-plan",
				SessionID: callCfg.Workspace.SessionID,
				Created:   created,
			})
			body, err := json.MarshalIndent(repairPlan, "", "  ")
			if err != nil {
				return "", err
			}
			planDir := filepath.Join(root, ".idiom", "plans")
			if err := os.MkdirAll(planDir, 0o755); err != nil {
				return "", err
			}
			planPath := filepath.Join(planDir, "20260628-214652354.plan.json")
			if err := os.WriteFile(planPath, append(body, '\n'), 0o644); err != nil {
				return "", err
			}

			return "Plan submitted.", nil
		},
		nil,
	)
	gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected noninteractive self-approved repair plan to fail new-file authorization, report=%#v err=%v stdout=%s", report, err, stdout.String()))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("without new-file authorization"), fmt.Sprintf("expected noninteractive self-approved repair plan to fail new-file authorization, report=%#v err=%v stdout=%s", report, err, stdout.String()))
	gomega.Expect(report.Status != plancontract.PlanExecuteReviewStatusFailed).To(gomega.BeFalse(), fmt.Sprintf("review repair status = %s, want failed", report.Status))

	if _, err := os.Stat(filepath.Join(root, "generated.txt")); !os.IsNotExist(err) {
		ginkgo.Fail(fmt.Sprintf("generated.txt should not be written, stat err=%v", err))
	}
})

var _ = ginkgo.It("verification plan rejects fail executor verification", func() {
	var stdout bytes.Buffer

	checked, evidence, err := runPlanExecuteVerificationPlanWithParentContract(context.Background(), ginkgo.GinkgoT().TempDir(), VerificationPlan{
		Rejects: []plancontract.VerificationReject{{
			Command: "bin/colony_tests -ts=quality_inventory_bindings",
			Source:  plancontract.VerificationCommandSourcePrompt,
			Reason:  plancontract.VerificationRejectUnsupportedCmd,
		}},
	}, "", true, &stdout)
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected unsupported requested verification to fail"))
	gomega.Expect(checked).To(gomega.Equal(1), fmt.Sprintf("checked/evidence = %d/%d, want 1/1", checked, len(evidence)))
	gomega.Expect(evidence).To(gomega.HaveLen(1), fmt.Sprintf("checked/evidence = %d/%d, want 1/1", checked, len(evidence)))
	gomega.Expect(evidence[0].Status).To(gomega.Equal(plancontract.PhaseEvidenceStatusFailed), fmt.Sprintf("unexpected evidence: %#v", evidence[0]))
	gomega.Expect(evidence[0].Error).To(gomega.Equal(plancontract.VerificationRejectUnsupportedCmd), fmt.Sprintf("unexpected evidence: %#v", evidence[0]))
	gomega.Expect(strings.Contains(stdout.String(), `"type":"`+plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventVerification)+`"`)).To(gomega.BeTrue(), fmt.Sprintf("expected verification event, got %s", stdout.String()))

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	event, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventVerificationAcceptance))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected verification_acceptance event, got %s", stdout.String()))

	body, err := json.Marshal(event)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal verification acceptance event: %v", err))

	var record autonomy.VerificationAcceptanceRecord
	if err := json.Unmarshal(body, &record); err != nil {
		ginkgo.Fail(fmt.Sprintf("decode verification acceptance event: %v", err))
	}
	if err := record.Validate(); err != nil {
		ginkgo.Fail(fmt.Sprintf("verification acceptance should validate: %v\nrecord=%#v", err, record))
	}
	gomega.Expect(record.Decision).To(gomega.Equal(autonomy.VerificationAcceptanceRejected), fmt.Sprintf("unexpected verification acceptance decision: %#v", record))
	gomega.Expect(record.ExitStatus).To(gomega.Equal(1), fmt.Sprintf("unexpected verification acceptance decision: %#v", record))
})

var _ = ginkgo.It("run review repair phase rejects pass when requested test edit missing", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "pkg.go"), []byte("package demo\n\nfunc Hardened() bool { return true }\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed pkg.go: %v", err))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "pkg.go", OldText: "return false", NewText: "return true", Purpose: "harden behavior"},
		},
	}

	var (
		stdout    bytes.Buffer
		gotPrompt string
	)

	_, err := runReviewRepairPhaseForTest(
		cfg,
		"Implement the hardening and add regression tests proving the edge case is rejected.",
		plan,
		nil,
		nil,
		ExecutorPhaseReport{},
		Options{
			JSONEvents:         true,
			EditSetObligations: plancontract.EditSetObligations{}.With(plancontract.EditSetObligationKindTestEdit, "test"),
		},
		&stdout,
		func(_ config.Config, prompt string, _ RunOptions, _ io.Writer) (string, error) {
			gotPrompt = prompt
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: source diff contains the hardening.\n- Test or command evidence: diff evidence shows the code change.", nil
		},
		nil,
	)
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected missing requested test edit to reject review pass"))
	gomega.Expect(strings.Contains(err.Error(), "missing_requested_test_edit")).To(gomega.BeTrue(), fmt.Sprintf("expected missing test edit rejection, got %v", err))
	gomega.Expect(strings.Contains(gotPrompt, "requested_test_edit_missing")).To(gomega.BeTrue(), fmt.Sprintf("review prompt should expose requested test edit obligation, got:\n%s", gotPrompt))

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	event, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventReviewRepairPassRejected))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected review_repair_pass_rejected event, got %s", stdout.String()))
	gomega.Expect(event["reason"] != "missing_requested_test_edit").To(gomega.BeFalse(), fmt.Sprintf("expected missing_requested_test_edit reason, got %#v", event))
})

var _ = ginkgo.It("run review repair phase forces visible terminal contract", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("good\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed a.txt: %v", err))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "bad", NewText: "good", Purpose: "seed changed path"},
		},
	}

	var (
		stdout  bytes.Buffer
		gotOpts RunOptions
	)

	report, err := runReviewRepairPhaseForTest(
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
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("review-repair pass should be accepted: %v", err))
	gomega.Expect(report.Status).To(gomega.Equal(plancontract.PlanExecuteReviewStatusPassed), fmt.Sprintf("unexpected report: %#v", report))
	gomega.Expect(report.StatusDetail).To(gomega.Equal("no corrective edits"), fmt.Sprintf("unexpected report: %#v", report))
	gomega.Expect(gotOpts.TaskFamily != string(autonomy.TaskFamilyPlanThenExecuteReview)).To(gomega.BeFalse(), fmt.Sprintf("review-repair should use review-repair task family, got %q", gotOpts.TaskFamily))
	gomega.Expect(gotOpts.DisableThinking).To(gomega.BeTrue(), fmt.Sprint("review-repair must force visible no-thinking output for its terminal contract"))
})

var _ = ginkgo.It("run review repair phase emits child lifecycle records", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("good\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed a.txt: %v", err))
	}

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "bad", NewText: "good", Purpose: "seed changed path"},
		},
	}

	var stdout bytes.Buffer
	_, err := runReviewRepairPhaseForTest(
		cfg,
		"Review behavior.",
		plan,
		[]string{"a.txt"},
		nil,
		ExecutorPhaseReport{},
		Options{
			JSONEvents:    true,
			ScopeContract: plancontract.NewPlanScopeContract([]string{"a.txt"}, []string{"a.txt"}, nil),
		},
		&stdout,
		func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: good is present.\n- Scope safety: no corrective edit needed.", nil
		},
		nil,
	)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("review-repair pass should be accepted: %v", err))

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	invocation := mustPlanExecuteAutonomyEvent[autonomy.ParentChildInvocationRecord](events, plancontract.PlanExecuteEventParentChildInvocation)
	terminal := mustPlanExecuteAutonomyEvent[autonomy.ChildTerminalRecord](events, plancontract.PlanExecuteEventChildTerminal)
	acceptance := mustPlanExecuteAutonomyEvent[autonomy.ChildAcceptanceRecord](events, plancontract.PlanExecuteEventChildAcceptance)
	gomega.Expect(invocation.Role).To(gomega.Equal("review_repair"), fmt.Sprintf("unexpected review invocation: %#v", invocation))
	gomega.Expect(invocation.WorkspaceMode).To(gomega.Equal(autonomy.ChildWorkspaceNoWrite), fmt.Sprintf("unexpected review invocation: %#v", invocation))
	gomega.Expect(terminal.InvocationID).To(gomega.Equal(invocation.InvocationID), fmt.Sprintf("unexpected review terminal: %#v", terminal))
	gomega.Expect(terminal.Status).To(gomega.Equal(autonomy.ChildTerminalCompleted), fmt.Sprintf("unexpected review terminal: %#v", terminal))
	gomega.Expect(acceptance.TerminalID).To(gomega.Equal(terminal.TerminalID), fmt.Sprintf("unexpected review acceptance: %#v", acceptance))
	gomega.Expect(acceptance.Decision).To(gomega.Equal(autonomy.ChildAcceptanceAccepted), fmt.Sprintf("unexpected review acceptance: %#v", acceptance))
})

var _ = ginkgo.It("run review repair phase rejects recovery template as pass", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("good\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed a.txt: %v", err))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "bad", NewText: "good", Purpose: "seed changed path"},
		},
	}

	var stdout bytes.Buffer

	_, err := runReviewRepairPhaseForTest(
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
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected recovery template text not to count as a review pass"))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("Decision: pass"), fmt.Sprintf("error should require an actual terminal pass shape, got %v", err))
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("Verification Checklist"), fmt.Sprintf("error should require an actual terminal pass shape, got %v", err))
})

var _ = ginkgo.It("review repair phase evidence reports uncovered required path", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("after\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed a.txt: %v", err))
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("already final\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed b.txt: %v", err))
	}

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
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
		planExecuteTestDependencies().SourceSanityCovers,
	)
	gomega.Expect(slices.Contains(evidence.OpenObligations, "required_path_uncovered:b.txt")).To(gomega.BeTrue(), fmt.Sprintf("phase evidence should report uncovered required path, got %#v", evidence.OpenObligations))

	if reason := phaseEvidencePassRejectionReason(evidence); reason != "required_path_uncovered:b.txt" {
		ginkgo.Fail(fmt.Sprintf("uncovered required path should reject pass, got %q", reason))
	}
})

var _ = ginkgo.It("review repair phase evidence accepts typed source satisfied required path", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("after\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed a.txt: %v", err))
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("already final\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed b.txt: %v", err))
	}

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Paths: planartifact.PlanArtifactPaths{
			SourceSatisfied: []string{"b.txt"},
		},
		Evidence: []planartifact.PlanArtifactEvidence{
			{Path: "b.txt", Kind: "harness_observed_source", Summary: "current source already satisfies required path"},
		},
		Edits: []planartifact.PlanEdit{
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
		planExecuteTestDependencies().SourceSanityCovers,
	)
	gomega.Expect(slices.Contains(evidence.OpenObligations, "required_path_uncovered:b.txt")).To(gomega.BeFalse(), fmt.Sprintf("typed source_satisfied_paths should cover required path, got %#v", evidence.OpenObligations))
})

var _ = ginkgo.It("review repair phase evidence rejects source satisfied required path without evidence", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("after\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed a.txt: %v", err))
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("already final\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed b.txt: %v", err))
	}

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Paths: planartifact.PlanArtifactPaths{
			SourceSatisfied: []string{"b.txt"},
		},
		Edits: []planartifact.PlanEdit{
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
		planExecuteTestDependencies().SourceSanityCovers,
	)
	gomega.Expect(slices.Contains(evidence.OpenObligations, "required_path_uncovered:b.txt")).To(gomega.BeTrue(), fmt.Sprintf("source_satisfied path without current-state evidence should remain uncovered, got %#v", evidence.OpenObligations))
})

var _ = ginkgo.It("run review repair phase rejects pass when requested implementation edit missing", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "feature_test.go"), []byte("package demo\n\nfunc TestEnabled(t *testing.T) {\n\tif !Enabled() {\n\t\tt.Fatal(\"disabled\")\n\t}\n}\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed feature_test.go: %v", err))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "feature_test.go", OldText: "func TestEnabled(t *testing.T) {}\n", NewText: "func TestEnabled(t *testing.T) {\n\tif !Enabled() {\n\t\tt.Fatal(\"disabled\")\n\t}\n}\n", Purpose: "add feature test"},
		},
	}

	var (
		stdout    bytes.Buffer
		gotPrompt string
	)

	_, err := runReviewRepairPhaseForTest(
		cfg,
		"Implement the feature. Start by adding failing tests, then implement.",
		plan,
		nil,
		nil,
		ExecutorPhaseReport{},
		Options{
			JSONEvents:         true,
			EditSetObligations: plancontract.EditSetObligations{}.With(plancontract.EditSetObligationKindImplementationEdit, "test"),
		},
		&stdout,
		func(_ config.Config, prompt string, _ RunOptions, _ io.Writer) (string, error) {
			gotPrompt = prompt
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: test coverage was added.\n- Scope safety: diff is limited to one test file.", nil
		},
		nil,
	)
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected missing requested implementation edit to reject review pass"))
	gomega.Expect(strings.Contains(err.Error(), "missing_requested_implementation_edit")).To(gomega.BeTrue(), fmt.Sprintf("expected missing implementation edit rejection, got %v", err))
	gomega.Expect(strings.Contains(gotPrompt, "requested_implementation_edit_missing")).To(gomega.BeTrue(), fmt.Sprintf("review prompt should expose requested implementation edit obligation, got:\n%s", gotPrompt))

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	event, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventReviewRepairPassRejected))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected review_repair_pass_rejected event, got %s", stdout.String()))
	gomega.Expect(event["reason"] != "missing_requested_implementation_edit").To(gomega.BeFalse(), fmt.Sprintf("expected missing_requested_implementation_edit reason, got %#v", event))
})

var _ = ginkgo.It("run review repair phase rejects pass that admits command was not run", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("after\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed a.txt: %v", err))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "before", NewText: "after", Purpose: "seed changed path"},
		},
	}

	var stdout bytes.Buffer

	_, err := runReviewRepairPhaseForTest(
		cfg,
		"Implement behavior and run focused tests.",
		plan,
		nil,
		nil,
		ExecutorPhaseReport{},
		Options{
			JSONEvents:         true,
			EditSetObligations: plancontract.EditSetObligations{}.With(plancontract.EditSetObligationKindTestEdit, "test"),
		},
		&stdout,
		func(_ config.Config, _ string, _ RunOptions, _ io.Writer) (string, error) {
			return "Decision: pass\n\nVerification Checklist:\n- Required behavior: diff looks scoped.\n- Test or command evidence: We didn't run `go test ./cmd/idiom`.", nil
		},
		nil,
	)
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected missing command evidence admission to reject review pass"))
	gomega.Expect(strings.Contains(err.Error(), "missing_command_evidence_claim")).To(gomega.BeTrue(), fmt.Sprintf("expected missing command evidence rejection, got %v", err))

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	event, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventReviewRepairPassRejected))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected review_repair_pass_rejected event, got %s", stdout.String()))
	gomega.Expect(event["reason"] != "missing_command_evidence_claim").To(gomega.BeFalse(), fmt.Sprintf("expected missing_command_evidence_claim reason, got %#v", event))
})

var _ = ginkgo.It("run review repair phase rejects pass when source sanity failed", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package demo\n\nfunc Value() int { return missing }\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed a.go: %v", err))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "a.go", OldText: "return 1", NewText: "return missing", Purpose: "introduce compile issue"},
		},
	}

	var (
		stdout    bytes.Buffer
		gotPrompt string
	)

	_, err := runReviewRepairPhaseForTest(
		cfg,
		"Repair Value.",
		plan,
		nil,
		nil,
		ExecutorPhaseReport{Total: 1, Succeeded: 1, SourceSanity: []plancontract.PhaseEvidenceCheck{{
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
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected failed source_sanity to reject review pass"))
	gomega.Expect(strings.Contains(err.Error(), "verification_failed")).To(gomega.BeTrue(), fmt.Sprintf("expected verification_failed rejection, got %v", err))
	gomega.Expect(strings.Contains(gotPrompt, "source_sanity_failed:a.go")).To(gomega.BeTrue(), fmt.Sprintf("review prompt should expose failed source_sanity obligation, got:\n%s", gotPrompt))

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	event, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventReviewRepairPassRejected))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected review_repair_pass_rejected event, got %s", stdout.String()))
	gomega.Expect(event["reason"] != "verification_failed").To(gomega.BeFalse(), fmt.Sprintf("expected verification_failed reason, got %#v", event))
})

var _ = ginkgo.It("run review repair phase retries contradictory pass into corrective plan", func() {
	root := ginkgo.GinkgoT().TempDir()
	path := filepath.Join(root, "a.txt")

	if err := os.WriteFile(path, []byte("value=bad\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed a.txt: %v", err))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "value=good", NewText: "value=bad", Purpose: "introduce bad value"},
		},
	}

	var stdout bytes.Buffer

	attempts := 0

	report, err := runReviewRepairPhaseForTest(
		cfg,
		"Keep a.txt good.",
		plan,
		nil,
		nil,
		ExecutorPhaseReport{Total: 1, Succeeded: 1, Verification: []plancontract.PhaseEvidenceCheck{{
			Kind:    "verification",
			Command: "check a.txt",
			Status:  "failed",
			Error:   "value was bad",
		}}},
		Options{JSONEvents: true},
		&stdout,
		func(cfg config.Config, prompt string, opts RunOptions, _ io.Writer) (string, error) {
			attempts++
			gomega.Expect(opts.TaskFamily != string(autonomy.TaskFamilyPlanThenExecuteReview)).To(gomega.BeFalse(), fmt.Sprintf("unexpected task family %q", opts.TaskFamily))

			if attempts == 1 {
				gomega.Expect(strings.Contains(prompt, "verification_failed:check a.txt")).To(gomega.BeTrue(), fmt.Sprintf("initial prompt should expose failed verification, got:\n%s", prompt))

				return "Decision: pass\n\nVerification Checklist:\n- Required behavior: acceptable.\n- Test or command evidence: acceptable.", nil
			}
			gomega.Expect(prompt).To(gomega.ContainSubstring("Rejected Previous Review Outcome"), fmt.Sprintf("retry prompt should forbid another pass, got:\n%s", prompt))
			gomega.Expect(prompt).To(gomega.ContainSubstring("`Decision: pass` is not an allowed terminal outcome"), fmt.Sprintf("retry prompt should forbid another pass, got:\n%s", prompt))

			writeRunTestPlanViaSubmitPlan(cfg, []planartifact.PlanEdit{
				{Path: "a.txt", OldText: "value=bad", NewText: "value=good", Purpose: "repair failed verification"},
			}, "Verification Checklist:\n- Failing item: check a.txt reported value was bad.\n- Scope safety: edit only a.txt.")

			return "submitted repair plan", nil
		},
		nil,
	)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected retry corrective plan to repair, got %v", err))
	gomega.Expect(attempts != 2).To(gomega.BeFalse(), fmt.Sprintf("expected one retry after contradictory pass, got %d attempts", attempts))
	gomega.Expect(report.Status).To(gomega.Equal(plancontract.PlanExecuteReviewStatusRepaired), fmt.Sprintf("expected repaired report, got %#v", report))
	gomega.Expect(report.Repairs).To(gomega.Equal(1), fmt.Sprintf("expected repaired report, got %#v", report))

	if got := readFileStringForRunTest(path); got != "value=good\n" {
		ginkgo.Fail(fmt.Sprintf("expected repair to apply, got %q", got))
	}

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	if _, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventReviewRepairRetry)); !ok {
		ginkgo.Fail(fmt.Sprintf("expected review_repair_retry event, got %s", stdout.String()))
	}
})

var _ = ginkgo.It("run review repair phase retries invalid terminal answer into corrective plan", func() {
	root := ginkgo.GinkgoT().TempDir()
	path := filepath.Join(root, "a.txt")

	if err := os.WriteFile(path, []byte("value=bad\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed a.txt: %v", err))
	}

	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "value=good", NewText: "value=bad", Purpose: "introduce bad value"},
		},
	}

	var stdout bytes.Buffer
	attempts := 0

	report, err := runReviewRepairPhaseForTest(
		cfg,
		"Keep a.txt good.",
		plan,
		[]string{"a.txt"},
		nil,
		ExecutorPhaseReport{Total: 1, Succeeded: 1, Verification: []plancontract.PhaseEvidenceCheck{{
			Kind:    "verification",
			Command: "check a.txt",
			Status:  "failed",
			Error:   "value was bad",
		}}},
		Options{JSONEvents: true},
		&stdout,
		func(cfg config.Config, prompt string, opts RunOptions, _ io.Writer) (string, error) {
			attempts++
			gomega.Expect(opts.TaskFamily != string(autonomy.TaskFamilyPlanThenExecuteReview)).To(gomega.BeFalse(), fmt.Sprintf("unexpected task family %q", opts.TaskFamily))

			if attempts == 1 {
				return "For the review-repair prompt, the answer is grounded in `a.txt`: it references value and returns bad.", nil
			}
			gomega.Expect(prompt).To(gomega.ContainSubstring("Rejected Previous Review Outcome"), fmt.Sprintf("retry prompt should reject read-only review answer, got:\n%s", prompt))
			gomega.Expect(prompt).To(gomega.ContainSubstring("did not produce a machine-readable terminal outcome"), fmt.Sprintf("retry prompt should reject read-only review answer, got:\n%s", prompt))
			gomega.Expect(prompt).To(gomega.ContainSubstring("Do not answer with a read-only explanation"), fmt.Sprintf("retry prompt should reject read-only review answer, got:\n%s", prompt))

			writeRunTestPlanViaSubmitPlan(cfg, []planartifact.PlanEdit{
				{Path: "a.txt", OldText: "value=bad", NewText: "value=good", Purpose: "repair failed verification"},
			}, "Verification Checklist:\n- Failing item: check a.txt reported value was bad.\n- Scope safety: edit only a.txt.")

			return "submitted repair plan", nil
		},
		nil,
	)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected retry corrective plan to repair invalid terminal answer, got %v", err))
	gomega.Expect(attempts != 2).To(gomega.BeFalse(), fmt.Sprintf("expected one retry after invalid terminal answer, got %d attempts", attempts))
	gomega.Expect(report.Status).To(gomega.Equal(plancontract.PlanExecuteReviewStatusRepaired), fmt.Sprintf("expected repaired report, got %#v", report))
	gomega.Expect(report.Repairs).To(gomega.Equal(1), fmt.Sprintf("expected repaired report, got %#v", report))

	if got := readFileStringForRunTest(path); got != "value=good\n" {
		ginkgo.Fail(fmt.Sprintf("expected repair to apply, got %q", got))
	}

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	event, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventReviewRepairRetry))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected review_repair_retry event, got %s", stdout.String()))
	gomega.Expect(event["reason"] != "invalid_terminal_outcome").To(gomega.BeFalse(), fmt.Sprintf("retry event reason = %#v, want invalid_terminal_outcome", event))
})

var _ = ginkgo.It("run review repair phase fails after repeated invalid terminal answer", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("value=bad\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed a.txt: %v", err))
	}

	cfg := config.Config{Workspace: config.Workspace{Root: root, CWD: root, EnableWrites: true}}
	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "a.txt", OldText: "value=good", NewText: "value=bad", Purpose: "introduce bad value"},
		},
	}

	var stdout bytes.Buffer
	attempts := 0

	report, err := runReviewRepairPhaseForTest(
		cfg,
		"Keep a.txt good.",
		plan,
		[]string{"a.txt"},
		nil,
		ExecutorPhaseReport{Total: 1, Succeeded: 1, Verification: []plancontract.PhaseEvidenceCheck{{
			Kind:    "verification",
			Command: "check a.txt",
			Status:  "failed",
			Error:   "value was bad",
		}}},
		Options{JSONEvents: true},
		&stdout,
		func(_ config.Config, prompt string, _ RunOptions, _ io.Writer) (string, error) {
			attempts++
			if attempts == 2 {
				gomega.Expect(prompt).To(gomega.ContainSubstring("Rejected Previous Review Outcome"), fmt.Sprintf("second attempt should include invalid-outcome retry prompt, got:\n%s", prompt))
			}

			return "For the review-repair prompt, the source inventory mentions a.txt but no terminal outcome is needed.", nil
		},
		nil,
	)
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected repeated invalid terminal answers to fail"))
	gomega.Expect(attempts != 2).To(gomega.BeFalse(), fmt.Sprintf("expected exactly one retry after invalid terminal answer, got %d attempts", attempts))
	gomega.Expect(report.Status).To(gomega.Equal(plancontract.PlanExecuteReviewStatusFailed), fmt.Sprintf("expected inconclusive report after repeated invalid answers, got %#v", report))
	gomega.Expect(report.StatusDetail).To(gomega.Equal("inconclusive"), fmt.Sprintf("expected inconclusive report after repeated invalid answers, got %#v", report))
	gomega.Expect(strings.Contains(err.Error(), "produced no corrective plan")).To(gomega.BeTrue(), fmt.Sprintf("expected invalid terminal outcome error, got %v", err))

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	retryEvents := 0
	for _, event := range events {
		if event["type"] == plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventReviewRepairRetry) {
			retryEvents++
			gomega.Expect(event["reason"] != "invalid_terminal_outcome").To(gomega.BeFalse(), fmt.Sprintf("retry event reason = %#v, want invalid_terminal_outcome", event))

		}
	}
	gomega.Expect(retryEvents != 1).To(gomega.BeFalse(), fmt.Sprintf("expected one invalid outcome retry event, got %d events in %s", retryEvents, stdout.String()))
})

var _ = ginkgo.It("run review repair phase rejects corrective plan when requested test edit still missing", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "pkg.go"), []byte("package demo\n\nfunc Hardened() bool { return false }\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed pkg.go: %v", err))
	}

	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		Edits: []planartifact.PlanEdit{
			{Path: "pkg.go", OldText: "return false", NewText: "return true", Purpose: "harden behavior"},
		},
	}

	var stdout bytes.Buffer

	_, err := runReviewRepairPhaseForTest(
		cfg,
		"Implement the hardening and add regression tests proving the edge case is rejected.",
		plan,
		nil,
		nil,
		ExecutorPhaseReport{},
		Options{
			JSONEvents:         true,
			EditSetObligations: plancontract.EditSetObligations{}.With(plancontract.EditSetObligationKindTestEdit, "test"),
		},
		&stdout,
		func(cfg config.Config, _ string, opts RunOptions, _ io.Writer) (string, error) {
			gomega.Expect(opts.TaskFamily != string(autonomy.TaskFamilyPlanThenExecuteReview)).To(gomega.BeFalse(), fmt.Sprintf("unexpected task family %q", opts.TaskFamily))

			writeRunTestPlanViaSubmitPlan(cfg, []planartifact.PlanEdit{
				{Path: "pkg.go", OldText: "return false", NewText: "return true", Purpose: "retry production-only hardening"},
			}, "Verification Checklist:\n- Required behavior: hardening is present.\n")

			return "submitted production-only repair", nil
		},
		nil,
	)
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected production-only repair plan rejection"))
	gomega.Expect(strings.Contains(err.Error(), "missing_requested_test_edit")).To(gomega.BeTrue(), fmt.Sprintf("expected missing_requested_test_edit, got %v", err))

	if got := readFileStringForRunTest(filepath.Join(root, "pkg.go")); strings.Contains(got, "return true") {
		ginkgo.Fail(fmt.Sprintf("repair rejection must happen before writes, got:\n%s", got))
	}

	events := jsoneventstest.DecodeLines(ginkgo.GinkgoT(), stdout.String())
	event, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(plancontract.PlanExecuteEventReviewRepairPlanRejected))
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected review_repair_plan_rejected event, got %s", stdout.String()))
	gomega.Expect(event["reason"] != "missing_requested_test_edit").To(gomega.BeFalse(), fmt.Sprintf("expected missing_requested_test_edit reason, got %#v", event))
})

var _ = ginkgo.It("plan execute prompt prose does not create edit set obligations", func() {
	for _, prompt := range []string{
		"Implement the feature and add tests.",
		"add a regression test for parser routes",
		"Harden temp symlink writes and add regression tests.",
	} {
		if obligations := planExecuteScopeContract(Options{}, prompt).EditSetObligations; len(obligations) != 0 {
			ginkgo.Fail(fmt.Sprintf("prompt prose must not create hidden edit-set obligations: %q -> %#v", prompt, obligations))
		}
	}

	for _, path := range []string{"tests/parser_test.py", "test/foo.py", "tests/panel_contract.gd"} {
		gomega.Expect(plancontract.ChangedPathsIncludeTest([]string{path})).To(gomega.BeTrue(), fmt.Sprintf("path should count as a test path: %s", path))

	}

	if reason := (plancontract.EditSetObligations{}).With(plancontract.EditSetObligationKindImplementationEdit, "test").RejectionReason([]string{"docs/feature.md", "pkg/feature_test.go"}); reason != plancontract.PlanExecuteFailureMissingRequestedImplementationEdit {
		ginkgo.Fail(fmt.Sprintf("docs plus tests should not satisfy implementation edit obligation, got %q", reason))
	}
})

var _ = ginkgo.It("plan verification preserves quoted run pattern", func() {
	prompt := "/plan-execute Continue the repair. Make this pass: go test ./internal/workflows/productworkflow ./cmd/idiom -run 'Test.*(Coverage|TDD|ProductBriefRender|KnownTDD)' -count=1. Do not edit every allowed path unless it is needed."
	want := "go test ./internal/workflows/productworkflow ./cmd/idiom -run 'Test.*(Coverage|TDD|ProductBriefRender|KnownTDD)' -count=1"
	got := NewVerificationPlanner("").Plan(VerificationPlannerInput{Prompt: prompt}).CommandStrings()
	gomega.Expect(got).To(gomega.HaveLen(1), fmt.Sprintf("verification commands = %#v, want %#v", got, []string{want}))
	gomega.Expect(got[0]).To(gomega.Equal(want), fmt.Sprintf("verification commands = %#v, want %#v", got, []string{want}))

	args, ok := plancontract.VerificationCommandArgs(got[0])
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("expected command to be allowed"))

	if joined := strings.Join(args, "\x00"); !strings.Contains(joined, "Test.*(Coverage|TDD|ProductBriefRender|KnownTDD)") {
		ginkgo.Fail(fmt.Sprintf("quoted -run pattern was not preserved in args: %#v", args))
	}
})

var _ = ginkgo.It("plan verification handles run after executor phrase", func() {
	prompt := "After the executor applies the plan, run `go test ./...`."
	got := NewVerificationPlanner("").Plan(VerificationPlannerInput{Prompt: prompt}).CommandStrings()
	gomega.Expect(got).To(gomega.HaveLen(1), fmt.Sprintf("verification commands = %#v, want %#v", got, []string{"go test ./..."}))
	gomega.Expect(got[0]).To(gomega.Equal("go test ./..."), fmt.Sprintf("verification commands = %#v, want %#v", got, []string{"go test ./..."}))
})

var _ = ginkgo.It("plan verification handles comma separated pytest approval", func() {
	prompt := `Approved. Use pytest tests/test_llm_core.py, pytest tests/test_memory.py, pytest tests/test_tools.py, and pytest tests/test_planner.py as the verification commands.`
	got := NewVerificationPlanner("").Plan(VerificationPlannerInput{Prompt: prompt}).CommandStrings()
	want := []string{
		"pytest tests/test_llm_core.py",
		"pytest tests/test_memory.py",
		"pytest tests/test_tools.py",
		"pytest tests/test_planner.py",
	}
	gomega.Expect(slices.Equal(got, want)).To(gomega.BeTrue(), fmt.Sprintf("verification commands = %#v, want %#v", got, want))
})

var _ = ginkgo.It("plan verification ignores suggested tests", func() {
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
	if got := NewVerificationPlanner("").Plan(VerificationPlannerInput{Prompt: prompt}).CommandStrings(); len(got) != 0 {
		ginkgo.Fail(fmt.Sprintf("suggested/context test mentions should not become executor-owned verification commands, got %#v", got))
	}
})

var _ = ginkgo.It("plan execute edit set obligations scope to brief body", func() {
	prompt := strings.Join([]string{
		"## Context Pack",
		"",
		"Candidate files mention implement, fallback, and add tests.",
		"",
		pathparse.WrapBriefBody("# Objective\n\nExplain the current status only."),
	}, "\n")

	if obligations := planExecuteScopeContract(Options{}, prompt).EditSetObligations; len(obligations) != 0 {
		ginkgo.Fail(fmt.Sprintf("generated context outside the brief body must not create edit-set obligations: %#v", obligations))
	}
})

var _ = ginkgo.It("plan execute typed scope owns edit set obligations", func() {
	opts := Options{
		ScopeContract: plancontract.NewPlanScopeContract(nil, []string{"pkg/feature.go", "pkg/feature_test.go"}, nil),
	}
	prompt := pathparse.WrapBriefBody("# Objective\n\nImplement the feature and add tests.")

	if obligations := planExecuteScopeContract(opts, prompt).EditSetObligations; len(obligations) != 0 {
		ginkgo.Fail(fmt.Sprintf("typed scope without edit-set obligations must not re-infer obligations from prompt prose: %#v", obligations))
	}
})

var _ = ginkgo.It("plan execute implementation edit obligation requires source like path", func() {
	obligations := (plancontract.EditSetObligations{}).With(plancontract.EditSetObligationKindImplementationEdit, "test")
	for _, paths := range [][]string{
		{"package.json", "pkg/feature_test.go"},
		{"config/settings.yaml", "pkg/feature_test.go"},
		{"notes.txt", "pkg/feature_test.go"},
		{"docs/feature.md", "pkg/feature_test.go"},
	} {
		if reason := obligations.RejectionReason(paths); reason != plancontract.PlanExecuteFailureMissingRequestedImplementationEdit {
			ginkgo.Fail(fmt.Sprintf("non-source paths must not satisfy implementation obligation, paths=%#v reason=%q", paths, reason))
		}
	}

	for _, path := range []string{
		"pkg/feature.go",
		"web/component.tsx",
		"native/src/system.cpp",
		"internal/surfaces/cliassets/lanes/plan-execute/preamble.md",
	} {
		if reason := obligations.RejectionReason([]string{path, "pkg/feature_test.go"}); reason != "" {
			ginkgo.Fail(fmt.Sprintf("source-like path should satisfy implementation obligation, path=%s reason=%q", path, reason))
		}
	}
})

var _ = ginkgo.It("plan execute explicit edit set obligations survive prompt only language", func() {
	opts := Options{
		EditSetObligations: plancontract.EditSetObligations{}.With(plancontract.EditSetObligationKindImplementationEdit, "retry"),
	}
	prompt := "This retry is for a test-only recovery pass. Do not infer implementation authority from this prompt prose."

	obligations := planExecuteScopeContract(opts, prompt).EditSetObligations
	gomega.Expect(obligations.Has(plancontract.EditSetObligationKindImplementationEdit)).To(gomega.BeTrue(), fmt.Sprintf("explicit implementation obligation was lost: %#v", obligations))

	if reason := obligations.RejectionReason([]string{"pkg/feature_test.go"}); reason != plancontract.PlanExecuteFailureMissingRequestedImplementationEdit {
		ginkgo.Fail(fmt.Sprintf("explicit implementation obligation must reject test-only edits, got %q from %#v", reason, obligations))
	}

	if reason := obligations.RejectionReason([]string{"pkg/feature.go"}); reason != "" {
		ginkgo.Fail(fmt.Sprintf("implementation edit should satisfy explicit obligation, got %q", reason))
	}
})

var _ = ginkgo.It("run resume rejects prompt mismatch", func() {
	root := ginkgo.GinkgoT().TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	state := NewProgressState("resume_mismatch", "original prompt", "", nil, nil, timeNowForRunTest())

	state.Phase = plancontract.PlanExecuteProgressPhaseExecutor
	state.Status = plancontract.PlanExecuteProgressStatusCompleted
	state.Executor.Status = plancontract.PlanExecuteProgressStatusCompleted
	state.PlanPath = ".idiom/plans/missing.plan.json"
	state.TotalEdits = 1
	state.ChangedPaths = []string{"a.txt"}

	if _, err := SaveProgressState(root, &state); err != nil {
		ginkgo.Fail(fmt.Sprintf("save progress state: %v", err))
	}

	var stdout, stderr bytes.Buffer

	result := runForTest(cfg, Options{Prompt: "different prompt", Resume: "resume_mismatch"}, func(config.Config, string, RunOptions, io.Writer) (string, error) {
		ginkgo.Fail(fmt.Sprint("prompt runner should not be called for mismatched resume prompt"))

		return "", nil
	}, nil, &stdout, &stderr)
	gomega.Expect(result.ExitCode != 1).To(gomega.BeFalse(), fmt.Sprintf("expected prompt mismatch failure, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String()))
	gomega.Expect(strings.Contains(stderr.String(), "different prompt")).To(gomega.BeTrue(), fmt.Sprintf("stderr should explain prompt mismatch, got %s", stderr.String()))
})

var _ = ginkgo.It("run resume rejects missing session", func() {
	root := ginkgo.GinkgoT().TempDir()
	cfg := config.Default()

	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	var stdout, stderr bytes.Buffer

	result := runForTest(cfg, Options{Prompt: "resume missing", Resume: "missing-session"}, func(config.Config, string, RunOptions, io.Writer) (string, error) {
		ginkgo.Fail(fmt.Sprint("prompt runner should not be called for missing resume state"))

		return "", nil
	}, nil, &stdout, &stderr)
	gomega.Expect(result.ExitCode != 1).To(gomega.BeFalse(), fmt.Sprintf("expected missing resume to fail, got code=%d stdout=%s stderr=%s", result.ExitCode, stdout.String(), stderr.String()))
	gomega.Expect(strings.Contains(stderr.String(), "does not exist")).To(gomega.BeTrue(), fmt.Sprintf("stderr should explain missing resume state, got %s", stderr.String()))
})

func writeRunTestPlanViaSubmitPlan(cfg config.Config, edits []planartifact.PlanEdit, rationale string) {
	ginkgo.GinkgoHelper()

	mgr := workspace.NewManager(cfg.Workspace)
	result := mgr.Execute(context.Background(), workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: rationale,
		Edits:     edits,
	})

	if strings.TrimSpace(result.Error) != "" {
		body, _ := json.MarshalIndent(result, "", "  ")
		ginkgo.Fail(fmt.Sprintf("submit_plan failed: %s", body))
	}
}

func readFileStringForRunTest(path string) string {
	ginkgo.GinkgoHelper()

	body, err := os.ReadFile(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read %s: %v", path, err))

	return string(body)
}

func planExecuteRunTestCompletionHasCommand(commands []plancontract.VerificationCommand, want string) bool {
	for _, command := range commands {
		if command.Command == want {
			return true
		}
	}

	return false
}

func planExecuteRunTestCompletionHasPassedVerification(checks []plancontract.PhaseEvidenceCheck, want string) bool {
	for _, check := range checks {
		if check.Command == want && check.Status == "ok" {
			return true
		}
	}

	return false
}

func timeNowForRunTest() time.Time {
	return time.Now().UTC()
}

func sourceSatisfiedPlannerAnswerForRunTest(lines ...string) string {
	return strings.Join(append([]string{plancontract.SourceSatisfiedDecision}, lines...), "\n")
}

func mustPlanExecuteAutonomyEvent[T interface {
	Validate() error
}](events []map[string]any, eventType plancontract.PlanExecuteEventType) T {
	ginkgo.GinkgoHelper()

	event, ok := jsoneventstest.FindByType(events, plancontract.PlanExecuteEventName(eventType))
	eventName := plancontract.PlanExecuteEventName(eventType)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("missing event %s in %#v", eventName, events))

	body, err := json.Marshal(event)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal event %s: %v", eventName, err))

	var record T
	if err := json.Unmarshal(body, &record); err != nil {
		ginkgo.Fail(fmt.Sprintf("decode event %s: %v", eventName, err))
	}
	if err := record.Validate(); err != nil {
		ginkgo.Fail(fmt.Sprintf("validate event %s: %v\n%#v", eventName, err, record))
	}

	return record
}
