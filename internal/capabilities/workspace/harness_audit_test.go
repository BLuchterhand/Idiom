package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/evidence/eval"
	"idiom/internal/platform/config"
	"idiom/internal/repository/comprehension"
)

var _ = ginkgo.Describe("harness audit", func() {
	ginkgo.Describe("audit universal ctags path requires universal ctags", func() {
		tests := []struct {
			name string
			body string
			want bool
		}{
			{
				name: "accepts universal ctags",
				body: "#!/bin/sh\nprintf '%s\\n' 'Universal Ctags 6.1.0'\n",
				want: true,
			},
			{
				name: "rejects non-universal ctags",
				body: "#!/bin/sh\nprintf '%s\\n' 'Exuberant Ctags 5.8'\n",
				want: false,
			},
		}

		for _, tt := range tests {
			ginkgo.It(tt.name, func() {
				if runtime.GOOS == "windows" {
					ginkgo.Skip("executable shell fixture uses POSIX mode bits")
				}
				binDir := ginkgo.GinkgoT().TempDir()
				path := filepath.Join(binDir, "ctags")
				gomega.Expect(os.WriteFile(path, []byte(tt.body), 0o755)).To(gomega.Succeed())
				ginkgo.GinkgoT().Setenv("PATH", binDir)

				got := auditUniversalCtagsPath()
				if tt.want {
					gomega.Expect(got).To(gomega.Equal(path), fmt.Sprintf("auditUniversalCtagsPath() = %q, want %q", got, path))
				} else {
					gomega.Expect(got).To(gomega.BeEmpty(), fmt.Sprintf("auditUniversalCtagsPath() = %q, want empty", got))
				}

			})
		}
	})
	ginkgo.It("build ranker learning audit empty workspace", func() {
		root := ginkgo.GinkgoT().TempDir()
		got := buildRankerLearningAudit(ginkgo.GinkgoT().Context(), root)
		snapshot, snapshotErr := comprehension.LoadRankerStateSnapshot(ginkgo.GinkgoT().Context(), root)
		gomega.Expect(snapshotErr).NotTo(gomega.HaveOccurred())
		gomega.Expect(got.Generation).To(gomega.Equal(snapshot.Generation), "audit currentness must use the ranker owner's generation")
		gomega.Expect(got).NotTo(gomega.BeNil(), "expected report")
		gomega.Expect(got.WeightsFileExists).To(gomega.BeFalse(), fmt.Sprintf("fresh workspace should have no ranker artifacts: %#v", got))
		gomega.Expect(got.LearningLogExists).To(gomega.BeFalse(), fmt.Sprintf("fresh workspace should have no ranker artifacts: %#v", got))
		gomega.Expect(got.WiredSources).To(gomega.BeEmpty(), fmt.Sprintf("no sources should be wired yet, got %v", got.WiredSources))
		gomega.Expect(got.DormantSources).To(gomega.HaveLen(len(rankerLearningKnownTriggers)), fmt.Sprintf("all known triggers should be dormant; got %v", got.DormantSources))

		for _, t2 := range got.TriggerRecommendations {
			gomega.Expect(t2.Status).To(gomega.Equal("dormant"), fmt.Sprintf("trigger %q should be dormant, got %q", t2.Name, t2.Status))
			gomega.Expect(t2.Suggested).NotTo(gomega.Equal(""), fmt.Sprintf("trigger %q must have a suggestion", t2.Name))

		}
	})
	ginkgo.It("build ranker learning audit classifies observed sources", func() {
		root := ginkgo.GinkgoT().TempDir()
		_, _, err := comprehension.ApplyVerifiedRankerLearning(ginkgo.GinkgoT().Context(), root, comprehension.VerifiedRankerLearningRequest{
			Source:   "eval",
			Action:   "boost",
			Files:    []string{"a.go"},
			Score:    0.9,
			Verified: true,
			Reason:   comprehension.LearningReasonVerifiedFeedback,
			VerificationEvidence: &comprehension.VerificationEvidence{
				EditPaths:           []string{"a.go"},
				VerificationCommand: "go test ./...",
				VerificationPassed:  true,
			},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "apply verified learning")

		got := buildRankerLearningAudit(ginkgo.GinkgoT().Context(), root)
		gomega.Expect(got.WeightsFileExists).To(gomega.BeTrue(), fmt.Sprintf("weights.json should exist after update"))
		gomega.Expect(got.LearningLogExists).To(gomega.BeTrue(), fmt.Sprintf("learning log should exist after append"))
		gomega.Expect(got.WeightCount).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected one weight entry, got %d", got.WeightCount))
		gomega.Expect(got.LearningSummary).NotTo(gomega.BeNil(), fmt.Sprintf("learning summary missing boost: %#v", got.LearningSummary))
		gomega.Expect(got.LearningSummary.BoostCount).To(gomega.BeNumerically("==", 1), fmt.Sprintf("learning summary missing boost: %#v", got.LearningSummary))
		gomega.Expect(slices.Contains(got.WiredSources, "eval")).To(gomega.BeTrue(), fmt.Sprintf("eval should be classified wired, got %v", got.WiredSources))

		for _, t2 := range got.TriggerRecommendations {
			if t2.Name == "eval" {
				gomega.Expect(t2.Status).To(gomega.Equal("wired"), fmt.Sprintf("eval trigger should be wired, got %q", t2.Status))
			}
			if t2.Name == "verified_edit" {
				gomega.Expect(t2.Status).To(gomega.Equal("dormant"), fmt.Sprintf("verified_edit should be dormant, got %q", t2.Status))
			}

		}
	})
	ginkgo.It("build ranker learning audit classifies recovery reconciliation source", func() {
		root := ginkgo.GinkgoT().TempDir()
		if _, err := comprehension.RecordVerifiedRankerEvidence(ginkgo.GinkgoT().Context(), root, comprehension.VerifiedRankerLearningRequest{
			Source:   comprehension.LearningSourceRecoveryReconciliation,
			Files:    []string{"internal/workflows/recovery/packet.go"},
			Score:    1,
			Reason:   comprehension.LearningReasonVerifiedRecovery,
			Verified: true,
			Evidence: comprehension.LearningEvidenceRef{
				Kind:     comprehension.LearningEvidenceKindRecoveryReconciliation,
				Verified: true,
				Files:    []string{"internal/workflows/recovery/packet.go"},
				Metrics: map[string]float64{
					"packet_quality":       1,
					"completed_atom_count": 1,
				},
			},
		}); err != nil {
			ginkgo.Fail(fmt.Sprintf("record recovery learning evidence: %v", err))
		}

		got := buildRankerLearningAudit(ginkgo.GinkgoT().Context(), root)
		gomega.Expect(got.LearningSummary).NotTo(gomega.BeNil(), fmt.Sprintf("learning summary missing recovery reconciliation source: %#v", got.LearningSummary))
		gomega.Expect(got.LearningSummary.BySource[comprehension.LearningSourceRecoveryReconciliation]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("learning summary missing recovery reconciliation source: %#v", got.LearningSummary))
		gomega.Expect(slices.Contains(got.WiredSources, comprehension.LearningSourceRecoveryReconciliation)).To(gomega.BeTrue(), fmt.Sprintf("recovery reconciliation should be wired, got %v", got.WiredSources))

		for _, trigger := range got.TriggerRecommendations {
			if trigger.Name == comprehension.LearningSourceRecoveryReconciliation {
				gomega.Expect(trigger.Status).To(gomega.Equal("wired"), fmt.Sprintf("recovery reconciliation trigger should be wired, got %#v", trigger))
			}

		}
	})
	ginkgo.It("harness audit includes build identity", func() {
		root := ginkgo.GinkgoT().TempDir()
		SetHarnessAuditBuildIdentity("test-version", "abc123", "2026-06-20T00:00:00Z")
		ginkgo.DeferCleanup(func() {
			SetHarnessAuditBuildIdentity("", "", "")
		})

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "tool_surface"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_audit returned error: %v\n%s", result.Error, result.Output))

		var report harnessAuditReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())
		gomega.Expect(report.Build.Version).To(gomega.Equal("test-version"), fmt.Sprintf("unexpected build identity: %#v", report.Build))
		gomega.Expect(report.Build.Commit).To(gomega.Equal("abc123"), fmt.Sprintf("unexpected build identity: %#v", report.Build))
		gomega.Expect(report.Build.Date).To(gomega.Equal("2026-06-20T00:00:00Z"), fmt.Sprintf("unexpected build identity: %#v", report.Build))
		gomega.Expect(report.Build.SourceOrDevBuild).To(gomega.BeFalse(), fmt.Sprintf("complete build identity should not be marked source/dev: %#v", report.Build))

	})
	ginkgo.It("harness audit behavior summarizes intent loop events without raw payload leakage", func() {
		root := ginkgo.GinkgoT().TempDir()
		logPath := filepath.Join(root, "logs", "telemetry", "intent-loop.jsonl")
		writeWorkspaceFile(logPath, strings.Join([]string{
			`{"type":"idiom.intent_loop.start","session_id":"intent-session-1","max_cycles":3,"contract_hash":"sha256:abc","prompt":"SECRET raw prompt body"}`,
			`{"type":"idiom.intent_loop.cycle_start","session_id":"intent-session-1","cycle_number":1,"cycle_prompt_sha256":"sha256:def"}`,
			`{"type":"idiom.intent_loop.cycle_complete","session_id":"intent-session-1","cycle_number":1,"gap_count":2,"remaining_gaps":[{"requirement":"verification:go test ./...","status":"failed","detail":"SECRET file body and private stdout"},{"requirement":"acceptance:coverage-threshold","status":"not_met","detail":"customer-acme private details"}]}`,
			`{"type":"run_event","role":"intent_loop","content":"{\"type\":\"idiom.intent_loop.cycle_start\",\"session_id\":\"intent-session-1\",\"cycle_number\":2,\"prompt\":\"SECRET nested prompt\"}"}`,
			`{"type":"run_event","role":"intent_loop","content":"{\"type\":\"idiom.intent_loop.cycle_complete\",\"session_id\":\"intent-session-1\",\"cycle_number\":2,\"gap_count\":0,\"remaining_gaps\":[],\"file_body\":\"SECRET nested file body\"}"}`,
			`{"type":"idiom.intent_loop.complete","session_id":"intent-session-1","total_cycles":2,"changed_paths":["internal/workflows/intentloop/state.go"],"raw_transcript":"SECRET transcript"}`,
			"",
		}, "\n"))

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Path: "logs/telemetry", Limit: 5})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_audit returned error: %v\n%s", result.Error, result.Output))

		for _, leaked := range []string{
			"SECRET",
			"private stdout",
			"customer-acme",
			"raw prompt body",
			"nested file body",
			"transcript",
			"go test ./...",
			"coverage-threshold",
		} {
			gomega.Expect(result.Output).NotTo(gomega.ContainSubstring(leaked), fmt.Sprintf("intent-loop audit leaked raw payload marker %q: %s", leaked, result.Output))

		}

		var report harnessAuditReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())

		intentLoop := report.LogBehavior.IntentLoop
		gomega.Expect(intentLoop).NotTo(gomega.BeNil(), fmt.Sprintf("expected intent-loop audit rollup, got %#v", report.LogBehavior))
		gomega.Expect(intentLoop.LogsHit).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected intent-loop audit rollup: %#v", intentLoop))
		gomega.Expect(intentLoop.Sessions).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected intent-loop audit rollup: %#v", intentLoop))
		gomega.Expect(intentLoop.Starts).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected intent-loop audit rollup: %#v", intentLoop))
		gomega.Expect(intentLoop.CycleStarts).To(gomega.BeNumerically("==", 2), fmt.Sprintf("unexpected intent-loop audit rollup: %#v", intentLoop))
		gomega.Expect(intentLoop.CycleCompletes).To(gomega.BeNumerically("==", 2), fmt.Sprintf("unexpected intent-loop audit rollup: %#v", intentLoop))
		gomega.Expect(intentLoop.Completions).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected intent-loop audit rollup: %#v", intentLoop))
		gomega.Expect(intentLoop.TotalReportedCycles).To(gomega.BeNumerically("==", 2), fmt.Sprintf("unexpected intent-loop audit rollup: %#v", intentLoop))
		gomega.Expect(intentLoop.TotalReportedGaps).To(gomega.BeNumerically("==", 2), fmt.Sprintf("unexpected intent-loop audit rollup: %#v", intentLoop))
		gomega.Expect(intentLoop.GapRequirementCounts["verification"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected sanitized gap class counts, got %#v", intentLoop))
		gomega.Expect(intentLoop.GapRequirementCounts["acceptance"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected sanitized gap class counts, got %#v", intentLoop))
		gomega.Expect(intentLoop.GapStatusCounts["failed"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected sanitized gap class counts, got %#v", intentLoop))
		gomega.Expect(intentLoop.GapStatusCounts["not_met"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected sanitized gap class counts, got %#v", intentLoop))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("intent_loop=1 starts/2 cycle_completes/1 completions/2 gaps"), fmt.Sprintf("expected summary to include intent-loop counters, got %q", result.Summary))

	})
	ginkgo.It("harness audit behavior summarizes feature planner lifecycle without raw payload leakage", func() {
		root := ginkgo.GinkgoT().TempDir()
		logPath := filepath.Join(root, "logs", "telemetry", "feature-planner.jsonl")
		writeWorkspaceFile(logPath, strings.Join([]string{
			`{"type":"idiom.feature_planner.plan_accepted","session_id":"fp-1","fields":{"plan_artifact_id":"artifact-1","plan_revision":2,"plan_digest":"sha256:plan","workspace_fingerprint":"sha256:workspace","raw_prompt":"SECRET prompt"}}`,
			`{"type":"idiom.feature_planner.plan_approved","session_id":"fp-1","event_id":"approval-1","fields":{"plan_artifact_id":"artifact-1","plan_revision":2,"plan_digest":"sha256:plan","workspace_fingerprint":"sha256:workspace","approval_event_id":"approval-1","model_notes":"SECRET approval notes"}}`,
			`{"type":"idiom.feature_planner.planner_question_superseded_by_revision","session_id":"fp-1","fields":{"question_id":"q-source","raw_revision":"SECRET operator revision"}}`,
			`{"type":"idiom.feature_planner.atom_dispatched","session_id":"fp-1","atom_id":"atom-1","fields":{"run_id":"atom-1-run-1","prompt":"SECRET implementor prompt"}}`,
			`{"type":"idiom.feature_planner.atom_completed","session_id":"fp-1","atom_id":"atom-1","fields":{"verification_closure_event_id":"verification-artifact-1-atom-1-r2","verification_status":"passed","plan_artifact_id":"artifact-1","plan_revision":2,"plan_digest":"sha256:plan","workspace_fingerprint":"sha256:workspace","approval_event_id":"approval-1","raw_output":"SECRET command output"}}`,
			`{"type":"idiom.feature_planner.release_ready","session_id":"fp-1","event_id":"release-artifact-1-r2","fields":{"release_status":"ready","verification_closure_event_ids":["verification-artifact-1-atom-1-r2"],"plan_artifact_id":"artifact-1","plan_revision":2,"plan_digest":"sha256:plan","workspace_fingerprint":"sha256:workspace","approval_event_id":"approval-1","raw_release_notes":"SECRET release notes"}}`,
			"",
		}, "\n"))

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Path: "logs/telemetry", Limit: 5})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_audit returned error: %v\n%s", result.Error, result.Output))
		gomega.Expect(result.Output).NotTo(gomega.ContainSubstring("SECRET"), fmt.Sprintf("feature-planner audit leaked raw payload: %s", result.Output))

		var report harnessAuditReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())
		gomega.Expect(report.LogBehavior).NotTo(gomega.BeNil(), fmt.Sprintf("feature planner lifecycle missing from report: %#v", report.LogBehavior))
		gomega.Expect(report.LogBehavior.FeaturePlanner).NotTo(gomega.BeNil(), fmt.Sprintf("feature planner lifecycle missing from report: %#v", report.LogBehavior))

		lifecycle := report.LogBehavior.FeaturePlanner
		gomega.Expect(lifecycle.PlanApprovedEvents).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected feature planner lifecycle summary: %#v", lifecycle))
		gomega.Expect(lifecycle.ExecutionHandoffEvents).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected feature planner lifecycle summary: %#v", lifecycle))
		gomega.Expect(lifecycle.QuestionAnswerEvents).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected feature planner lifecycle summary: %#v", lifecycle))
		gomega.Expect(lifecycle.AtomTerminalEvents).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected feature planner lifecycle summary: %#v", lifecycle))
		gomega.Expect(lifecycle.ReleaseReadyEvents).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected feature planner lifecycle summary: %#v", lifecycle))
		gomega.Expect(lifecycle.VerificationStatuses["passed"]).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected feature planner lifecycle summary: %#v", lifecycle))
		gomega.Expect(lifecycle.ArtifactIDs).To(gomega.ContainElement("artifact-1"), fmt.Sprintf("lifecycle identifiers missing: %#v", lifecycle))
		gomega.Expect(lifecycle.PlanDigests).To(gomega.ContainElement("sha256:plan"), fmt.Sprintf("lifecycle identifiers missing: %#v", lifecycle))
		gomega.Expect(lifecycle.ApprovalEvents).To(gomega.ContainElement("approval-1"), fmt.Sprintf("lifecycle identifiers missing: %#v", lifecycle))
		gomega.Expect(lifecycle.VerificationClosures).To(gomega.ContainElement("verification-artifact-1-atom-1-r2"), fmt.Sprintf("lifecycle identifiers missing: %#v", lifecycle))
		gomega.Expect(harnessAuditFindingByID(report.Findings, "feature_planner_lifecycle_binding_missing")).To(gomega.BeNil())

	})
	ginkgo.It("harness audit behavior flags feature planner lifecycle missing bindings", func() {
		root := ginkgo.GinkgoT().TempDir()
		logPath := filepath.Join(root, "logs", "telemetry", "feature-planner-missing.jsonl")
		writeWorkspaceFile(logPath, strings.Join([]string{
			`{"type":"idiom.feature_planner.plan_approved","session_id":"fp-1","event_id":"approval-1","fields":{"approval":"harness_contract_approval"}}`,
			`{"type":"idiom.feature_planner.atom_completed","session_id":"fp-1","atom_id":"atom-1","fields":{"verification_status":"passed"}}`,
			"",
		}, "\n"))

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Path: "logs/telemetry", Limit: 5})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_audit returned error: %v\n%s", result.Error, result.Output))

		var report harnessAuditReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())

		finding := harnessAuditFindingByID(report.Findings, "feature_planner_lifecycle_binding_missing")
		gomega.Expect(finding).NotTo(gomega.BeNil(), fmt.Sprintf("expected missing lifecycle binding finding, got %#v", report.Findings))
		gomega.Expect(finding.DetailCount).To(gomega.BeNumerically("==", 2), fmt.Sprintf("expected missing lifecycle binding finding, got %#v", report.Findings))

	})
	ginkgo.It("harness audit behavior flags feature planner lifecycle correlation mismatch", func() {
		root := ginkgo.GinkgoT().TempDir()
		logPath := filepath.Join(root, "logs", "telemetry", "feature-planner-mismatch.jsonl")
		writeWorkspaceFile(logPath, strings.Join([]string{
			`{"type":"idiom.feature_planner.plan_approved","session_id":"fp-1","event_id":"approval-1","fields":{"plan_artifact_id":"artifact-1","plan_revision":2,"plan_digest":"sha256:plan","workspace_fingerprint":"sha256:workspace","approval_event_id":"approval-1"}}`,
			`{"type":"idiom.feature_planner.atom_completed","session_id":"fp-1","atom_id":"atom-1","fields":{"verification_closure_event_id":"verification-artifact-1-atom-1-r2","verification_status":"passed","plan_artifact_id":"artifact-1","plan_revision":2,"plan_digest":"sha256:plan","workspace_fingerprint":"sha256:workspace","approval_event_id":"approval-1"}}`,
			`{"type":"idiom.feature_planner.release_ready","session_id":"fp-1","event_id":"release-artifact-1-r2","fields":{"release_status":"ready","verification_closure_event_ids":["verification-artifact-1-atom-1-r2"],"plan_artifact_id":"artifact-1","plan_revision":2,"plan_digest":"sha256:other","workspace_fingerprint":"sha256:workspace","approval_event_id":"approval-2"}}`,
			"",
		}, "\n"))

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Path: "logs/telemetry", Limit: 5})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_audit returned error: %v\n%s", result.Error, result.Output))

		var report harnessAuditReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())

		finding := harnessAuditFindingByID(report.Findings, "feature_planner_lifecycle_correlation_failed")
		gomega.Expect(finding).NotTo(gomega.BeNil(), fmt.Sprintf("expected lifecycle correlation finding, got %#v", report.Findings))
		gomega.Expect(finding.DetailCount).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected lifecycle correlation finding, got %#v", report.Findings))

	})
	ginkgo.It("harness audit behavior flags feature planner missing coverage authority without raw leakage", func() {
		root := ginkgo.GinkgoT().TempDir()
		logPath := filepath.Join(root, "logs", "telemetry", "feature-planner-coverage.jsonl")
		writeWorkspaceFile(logPath, strings.Join([]string{
			`{"type":"idiom.feature_planner.standalone_context_blocked","session_id":"fp-coverage","fields":{"validation_diagnostic":"standalone feature plan missing behavioral_coverage_draft with BDD scenarios, variants/invariants, and concrete TDD traceability","raw_plan":"SECRET raw model plan"}}`,
			"",
		}, "\n"))

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Path: "logs/telemetry", Limit: 5})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_audit returned error: %v\n%s", result.Error, result.Output))

		for _, leaked := range []string{"SECRET", "raw model plan", "standalone feature plan missing behavioral_coverage_draft"} {
			gomega.Expect(result.Output).NotTo(gomega.ContainSubstring(leaked), fmt.Sprintf("feature-planner coverage audit leaked raw diagnostic marker %q: %s", leaked, result.Output))

		}

		var report harnessAuditReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())

		lifecycle := report.LogBehavior.FeaturePlanner
		gomega.Expect(lifecycle).NotTo(gomega.BeNil(), fmt.Sprintf("expected coverage authority block rollup, got %#v", lifecycle))
		gomega.Expect(lifecycle.CoverageAuthorityBlocks).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected coverage authority block rollup, got %#v", lifecycle))
		gomega.Expect(lifecycle.PlanRejectedEvents).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected coverage authority block rollup, got %#v", lifecycle))

		finding := harnessAuditFindingByID(report.Findings, "feature_planner_coverage_authority_missing")
		gomega.Expect(finding).NotTo(gomega.BeNil(), fmt.Sprintf("expected missing coverage authority finding, got %#v", report.Findings))
		gomega.Expect(finding.DetailCount).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected missing coverage authority finding, got %#v", report.Findings))

	})
	ginkgo.It("ranker learning findings ignore evidence only recovery no ops", func() {
		root := ginkgo.GinkgoT().TempDir()
		for i := 0; i < 4; i++ {
			if _, err := comprehension.RecordVerifiedRankerEvidence(ginkgo.GinkgoT().Context(), root, comprehension.VerifiedRankerLearningRequest{
				Source:   comprehension.LearningSourceRecoveryReconciliation,
				Files:    []string{"internal/workflows/recovery/packet.go"},
				Score:    1,
				Reason:   comprehension.LearningReasonVerifiedRecovery,
				Verified: true,
				Evidence: comprehension.LearningEvidenceRef{
					Kind:     comprehension.LearningEvidenceKindRecoveryReconciliation,
					Verified: true,
					Files:    []string{"internal/workflows/recovery/packet.go"},
				},
			}); err != nil {
				ginkgo.Fail(fmt.Sprintf("record recovery learning evidence: %v", err))
			}
		}

		got := buildRankerLearningAudit(ginkgo.GinkgoT().Context(), root)
		for _, finding := range rankerLearningFindings(got) {
			gomega.Expect(finding.ID).NotTo(gomega.Equal("ranker_learning_mostly_noop"), fmt.Sprintf("evidence-only recovery no-op events should not trigger mutable no-op warning: %#v", finding))

		}
	})
	ginkgo.It("ranker learning audit surfaces accepted and rejected decisions without reason details", func() {
		root := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Setenv(comprehension.RankerLearningEnv, "1")

		accepted, _, err := comprehension.ApplyVerifiedRankerLearning(ginkgo.GinkgoT().Context(), root, comprehension.VerifiedRankerLearningRequest{
			Source:   "eval",
			Action:   "boost",
			Files:    []string{"internal/runtime/agent/runner.go"},
			Score:    0.9,
			Reason:   "swe_groundedness_oracle",
			Verified: true,
			Evidence: comprehension.LearningEvidenceRef{
				Kind:     "swe_groundedness_oracle",
				Verified: true,
			},
			VerificationEvidence: &comprehension.VerificationEvidence{
				EditPaths:           []string{"internal/runtime/agent/runner.go"},
				VerificationCommand: "go test ./...",
				VerificationPassed:  true,
			},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(accepted.State).To(gomega.Equal(comprehension.LearningStateAccepted))

		rejected, _, err := comprehension.ApplyVerifiedRankerLearning(ginkgo.GinkgoT().Context(), root, comprehension.VerifiedRankerLearningRequest{
			Source: "eval",
			Action: "boost",
			Files:  []string{"internal/runtime/agent/runner.go"},
			Score:  0.9,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(rejected.State).To(gomega.Equal(comprehension.LearningStateRejected))

		got := buildRankerLearningAudit(ginkgo.GinkgoT().Context(), root)
		gomega.Expect(got.LearningSummary).NotTo(gomega.BeNil(), fmt.Sprintf("learning summary missing"))
		gomega.Expect(got.LearningSummary.TotalEvents).To(gomega.BeNumerically("==", 2), fmt.Sprintf("learning event counts missing: %#v", got.LearningSummary))
		gomega.Expect(got.LearningSummary.AcceptedCount).To(gomega.BeNumerically("==", 1), fmt.Sprintf("learning event counts missing: %#v", got.LearningSummary))
		gomega.Expect(got.LearningSummary.RejectedCount).To(gomega.BeNumerically("==", 1), fmt.Sprintf("learning event counts missing: %#v", got.LearningSummary))
		gomega.Expect(got.LearningSummary.ReasonCounts).To(gomega.BeEmpty(), "aggregate audit must not project per-event reasons")
		gomega.Expect(got.LearningSummary.AcceptedReasonCounts).To(gomega.BeEmpty(), "aggregate audit must not project accepted-event reasons")
		gomega.Expect(got.LearningSummary.RejectedReasonCounts).To(gomega.BeEmpty(), "aggregate audit must not project rejected-event reasons")

		summary := harnessAuditSummary(harnessAuditReport{
			Purpose:        "behavior",
			RankerLearning: got,
		})
		gomega.Expect(summary).To(gomega.ContainSubstring("ranker_learning_events=2 accepted=1 rejected=1"), fmt.Sprintf("audit summary missing privacy-safe learning metrics: %s", summary))

	})
	ginkgo.It("harness audit summary includes extended gate coverage", func() {
		summary := harnessAuditSummary(harnessAuditReport{
			Purpose: "behavior",
			ContractCoverage: &autonomy.EventAuditReport{
				TotalEvents: 3,
				GateCoverage: autonomy.GateEventCoverage{
					DecisionEvents:                  2,
					DecisionCountsByPackID:          map[string]int{"pack-alpha": 2},
					DecisionCountsByEnforcementMode: map[string]int{"enforce": 1, "observe_only": 1},
					EventsWithObservedFacts:         1,
					EventsWithSafeAlternatives:      1,
					MissingPackIDRefs:               []autonomy.EventAuditRef{{Reference: "pack_id"}},
				},
			},
		})

		for _, want := range []string{
			"contract_events=3",
			"gate_decisions=2 packs=1 enforcement_modes=2 observed_facts=1 safe_alternatives=1 missing_gate_fields=1",
		} {
			gomega.Expect(summary).To(gomega.ContainSubstring(want), fmt.Sprintf("audit summary missing %q: %s", want, summary))

		}
	})
	ginkgo.It("ranker learning questions mention dormant sources", func() {
		root := ginkgo.GinkgoT().TempDir()
		got := buildRankerLearningAudit(ginkgo.GinkgoT().Context(), root)
		questions := rankerLearningQuestions(got)
		gomega.Expect(questions).NotTo(gomega.BeEmpty(), "expected questions for dormant sources")

		joined := ""
		for _, q := range questions {
			joined += q.Question + "|"
		}

		for _, want := range []string{"verified_edit", "harness_feedback", "audit_watch"} {
			gomega.Expect(joined).To(gomega.ContainSubstring(want), fmt.Sprintf("expected question for dormant %q in %s", want, joined))

		}
	})
	ginkgo.It("ranker learning idle finding explains expected fresh state", func() {
		findings := rankerLearningFindings(&harnessAuditRankerLearning{})
		finding := harnessAuditFindingByID(findings, "ranker_learning_idle")
		gomega.Expect(finding).NotTo(gomega.BeNil(), fmt.Sprintf("expected idle ranker finding, got %#v", findings))
		gomega.Expect(finding.Evidence).To(gomega.ContainSubstring("expected before"), fmt.Sprintf("idle ranker finding should not imply a broken system: %#v", finding))
		gomega.Expect(finding.Recommendation).To(gomega.ContainSubstring("no action needed"), fmt.Sprintf("idle ranker finding should not imply a broken system: %#v", finding))

	})
	ginkgo.It("ranker learning dormant sources stay in recommendations", func() {
		findings := rankerLearningFindings(&harnessAuditRankerLearning{
			WeightsFileExists: true,
			LearningLogExists: true,
			DormantSources:    []string{"verified_edit"},
		})
		gomega.Expect(harnessAuditFindingByID(findings, "ranker_learning_dormant_sources")).To(gomega.BeNil(), fmt.Sprintf("dormant trigger recommendations should not be top-level findings: %#v", findings))

	})
	ginkgo.It("split historical harness audit findings", func() {
		current, historical := splitHistoricalHarnessAuditFindings([]harnessAuditFinding{
			{ID: "freshness_refresh_failures"},
			{ID: "context_cache_unresolved_gaps", Severity: "info"},
			{ID: "historical_behavior_patterns"},
			{ID: "context_cache_historical_stale_rows"},
		})
		gomega.Expect(current).To(gomega.HaveLen(2), fmt.Sprintf("unexpected current findings: %#v", current))
		gomega.Expect(slices.ContainsFunc(current, func(finding harnessAuditFinding) bool { return finding.ID == "freshness_refresh_failures" })).To(gomega.BeTrue(), fmt.Sprintf("unexpected current findings: %#v", current))
		gomega.Expect(slices.ContainsFunc(current, func(finding harnessAuditFinding) bool { return finding.ID == "context_cache_unresolved_gaps" })).To(gomega.BeTrue(), fmt.Sprintf("unexpected current findings: %#v", current))
		gomega.Expect(historical).To(gomega.HaveLen(2), fmt.Sprintf("unexpected historical findings: %#v", historical))

	})
	ginkgo.It("contract coverage findings carry actionable non raw refs", func() {
		report := &autonomy.EventAuditReport{
			Findings: []autonomy.EventAuditFinding{{
				ID:          "contract_event_privacy_forbidden_raw_content",
				Severity:    "high",
				Evidence:    "2 forbidden raw-content field(s) or value marker(s) in autonomy event metadata",
				EventIDs:    []string{"event-z", "event-a", "event-a"},
				ContractIDs: []string{"contract-b", "contract-a"},
				DetailCount: 2,
				Details: []autonomy.EventAuditFindingDetail{{
					EventID:    "event-a",
					Type:       "tool_policy_decision",
					Sequence:   2,
					ContractID: "contract-a",
					Field:      "policy.arguments_summary",
					Reason:     "raw content marker",
				}},
			}},
		}

		findings := contractCoverageFindings(report)
		finding := harnessAuditFindingByID(findings, "contract_event_privacy_forbidden_raw_content")
		gomega.Expect(finding).NotTo(gomega.BeNil(), fmt.Sprintf("expected privacy finding, got %#v", findings))

		if got, want := finding.EventIDs, []string{"event-a", "event-z"}; !slices.Equal(got, want) {
			ginkgo.Fail(fmt.Sprintf("event IDs should be bounded sorted refs, got %#v want %#v", got, want))
		}
		if got, want := finding.ContractIDs, []string{"contract-a", "contract-b"}; !slices.Equal(got, want) {
			ginkgo.Fail(fmt.Sprintf("contract IDs should be bounded sorted refs, got %#v want %#v", got, want))
		}
		gomega.Expect(finding.DetailCount).To(gomega.BeNumerically("==", 2), fmt.Sprintf("finding should carry bounded source-owner detail refs, got %#v", finding))
		gomega.Expect(finding.Details).To(gomega.HaveLen(1), fmt.Sprintf("finding should carry bounded source-owner detail refs, got %#v", finding))
		gomega.Expect(finding.Details[0].Field).To(gomega.Equal("policy.arguments_summary"), fmt.Sprintf("finding should carry bounded source-owner detail refs, got %#v", finding))
		gomega.Expect(finding.Details[0].Reason).To(gomega.Equal("raw content marker"), fmt.Sprintf("finding should carry bounded source-owner detail refs, got %#v", finding))

		for _, forbidden := range []string{"raw_prompt", "customer-acme", "SECRET"} {
			detailJSON, _ := json.Marshal(finding.Details)
			gomega.Expect(finding.Recommendation).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("finding leaked forbidden raw marker %q: %#v", forbidden, finding))
			gomega.Expect(strings.Join(finding.EventIDs, ",")).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("finding leaked forbidden raw marker %q: %#v", forbidden, finding))
			gomega.Expect(string(detailJSON)).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("finding leaked forbidden raw marker %q: %#v", forbidden, finding))

		}
		gomega.Expect(finding.Recommendation).To(gomega.ContainSubstring("event_ids=event-a,event-z"), fmt.Sprintf("recommendation should point to non-raw refs and owner action, got %q", finding.Recommendation))
		gomega.Expect(finding.Recommendation).To(gomega.ContainSubstring("redact or remove raw-content metadata"), fmt.Sprintf("recommendation should point to non-raw refs and owner action, got %q", finding.Recommendation))

	})
	ginkgo.It("freshness health findings ignore log derived history", func() {
		historical := buildFreshnessHealthAudit(&eval.ContextStats{
			FreshnessBlockers:        1,
			FreshnessRefreshFailures: 1,
			StaleUseBlocked:          1,
		}, nil, true)
		current := buildFreshnessHealthAudit(nil, nil, false)
		gomega.Expect(freshnessHealthFindings(&current)).To(gomega.BeEmpty(), fmt.Sprintf("empty current freshness health should not report findings"))
		gomega.Expect(historical.TelemetryBlockers).To(gomega.BeNumerically("==", 1), fmt.Sprintf("historical telemetry should remain in freshness health: %#v", historical))
		gomega.Expect(historical.StaleUseBlocked).To(gomega.BeNumerically("==", 1), fmt.Sprintf("historical telemetry should remain in freshness health: %#v", historical))

		if findings := freshnessHealthFindings(&historical); len(findings) != 0 {
			ginkgo.Fail(fmt.Sprintf("historical-only telemetry should not raise current findings: %#v", findings))
		}
	})
	ginkgo.It("freshness health findings report current log derived blockers", func() {
		findings := freshnessHealthFindings(&harnessAuditFreshnessHealth{
			TelemetryBlockers:        1,
			TelemetryRefreshFailures: 1,
			TelemetryAllowStale:      1,
			StaleUseBlocked:          1,
		})
		for _, id := range []string{
			"freshness_allow_stale_invoked",
			"freshness_refresh_failures",
			"freshness_log_blockers",
			"stale_use_blockers_observed",
		} {
			gomega.Expect(harnessAuditFindingByID(findings, id)).NotTo(gomega.BeNil(), fmt.Sprintf("expected current log-derived finding %q, got %#v", id, findings))

		}
	})
	ginkgo.It("freshness health findings ignore recovered current log blockers", func() {
		health := buildFreshnessHealthAudit(&eval.ContextStats{
			FreshnessBlockers:        1,
			FreshnessRefreshFailures: 1,
			StaleUseBlocked:          1,
		}, &harnessAuditFreshnessWindow{
			LatestState:               "fresh",
			ResolvedBlockers:          1,
			ResolvedRefreshFailures:   1,
			ResolvedStaleUse:          1,
			UnresolvedBlockers:        0,
			UnresolvedRefreshFailures: 0,
			UnresolvedStaleUse:        0,
		}, false)
		gomega.Expect(health.TelemetryBlockers).To(gomega.BeNumerically("==", 0), fmt.Sprintf("recovered blockers should not remain unresolved: %#v", health))
		gomega.Expect(health.TelemetryRefreshFailures).To(gomega.BeNumerically("==", 0), fmt.Sprintf("recovered blockers should not remain unresolved: %#v", health))
		gomega.Expect(health.StaleUseBlocked).To(gomega.BeNumerically("==", 0), fmt.Sprintf("recovered blockers should not remain unresolved: %#v", health))
		gomega.Expect(health.TelemetryResolvedBlockers).To(gomega.BeNumerically("==", 1), fmt.Sprintf("recovered blockers should remain visible in health: %#v", health))
		gomega.Expect(health.TelemetryResolvedRefreshFailures).To(gomega.BeNumerically("==", 1), fmt.Sprintf("recovered blockers should remain visible in health: %#v", health))
		gomega.Expect(health.ResolvedStaleUseBlocked).To(gomega.BeNumerically("==", 1), fmt.Sprintf("recovered blockers should remain visible in health: %#v", health))
		gomega.Expect(health.TelemetryLatestState).To(gomega.Equal("fresh"), fmt.Sprintf("recovered blockers should remain visible in health: %#v", health))

		if findings := freshnessHealthFindings(&health); len(findings) != 0 {
			ginkgo.Fail(fmt.Sprintf("recovered blockers should not raise active findings: %#v", findings))
		}
	})
	ginkgo.It("audit current freshness window treats later fresh result as recovery", func() {
		window := auditCurrentFreshnessWindow("", []eval.PerfReport{
			{
				LogPath: "a.jsonl",
				Context: &eval.ContextStats{
					FreshnessBlockers:        1,
					FreshnessRefreshFailures: 1,
					StaleUseBlocked:          1,
				},
			},
			{
				LogPath: "b.jsonl",
				Context: &eval.ContextStats{
					FreshnessReadThroughResults: 1,
					FreshResults:                1,
					StaleUseFresh:               1,
				},
			},
		})
		gomega.Expect(window).NotTo(gomega.BeNil(), "expected freshness window")
		gomega.Expect(window.LatestState).To(gomega.Equal("fresh"), fmt.Sprintf("later fresh result should clear unresolved blockers: %#v", window))
		gomega.Expect(window.UnresolvedBlockers).To(gomega.BeNumerically("==", 0), fmt.Sprintf("later fresh result should clear unresolved blockers: %#v", window))
		gomega.Expect(window.UnresolvedRefreshFailures).To(gomega.BeNumerically("==", 0), fmt.Sprintf("later fresh result should clear unresolved blockers: %#v", window))
		gomega.Expect(window.UnresolvedStaleUse).To(gomega.BeNumerically("==", 0), fmt.Sprintf("later fresh result should clear unresolved blockers: %#v", window))
		gomega.Expect(window.ResolvedBlockers).To(gomega.BeNumerically("==", 1), fmt.Sprintf("recovered blockers should be retained as resolved evidence: %#v", window))
		gomega.Expect(window.ResolvedRefreshFailures).To(gomega.BeNumerically("==", 1), fmt.Sprintf("recovered blockers should be retained as resolved evidence: %#v", window))
		gomega.Expect(window.ResolvedStaleUse).To(gomega.BeNumerically("==", 1), fmt.Sprintf("recovered blockers should be retained as resolved evidence: %#v", window))
		gomega.Expect(window.FreshResultsAfterBlocker).To(gomega.BeNumerically("==", 1), fmt.Sprintf("recovered blockers should be retained as resolved evidence: %#v", window))

	})
	ginkgo.It("freshness health findings report in process refresh failures", func() {
		findings := freshnessHealthFindings(&harnessAuditFreshnessHealth{RefreshFailures: 1})
		gomega.Expect(harnessAuditFindingByID(findings, "freshness_refresh_failures")).NotTo(gomega.BeNil(), fmt.Sprintf("in-process refresh failure should remain actionable, got %#v", findings))

	})
	ginkgo.It("freshness health findings evidence filters historical only telemetry", func() {
		findings := freshnessHealthFindings(&harnessAuditFreshnessHealth{
			RefreshFailures:          1,
			TelemetryRefreshFailures: 3,
			AllowStaleInvocations:    1,
			TelemetryAllowStale:      2,
			TelemetryHistoricalOnly:  true,
		})
		refresh := harnessAuditFindingByID(findings, "freshness_refresh_failures")
		gomega.Expect(refresh).NotTo(gomega.BeNil(), fmt.Sprintf("refresh evidence should not label historical telemetry as current: %#v", findings))
		gomega.Expect(refresh.Evidence).To(gomega.ContainSubstring("1 in-process and 0 current log-derived"), fmt.Sprintf("refresh evidence should not label historical telemetry as current: %#v", findings))

		allowStale := harnessAuditFindingByID(findings, "freshness_allow_stale_invoked")
		gomega.Expect(allowStale).NotTo(gomega.BeNil(), fmt.Sprintf("allow-stale evidence should not label historical telemetry as current: %#v", findings))
		gomega.Expect(allowStale.Evidence).To(gomega.ContainSubstring("1 in-process and 0 current log-derived"), fmt.Sprintf("allow-stale evidence should not label historical telemetry as current: %#v", findings))

	})
	ginkgo.It("harness audit perf report time falls back to log timestamp", func() {
		root := ginkgo.GinkgoT().TempDir()
		logPath := filepath.Join(root, "logs", "telemetry", "tool-context_pack-latest.jsonl")
		gomega.Expect(os.MkdirAll(filepath.Dir(logPath), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(logPath, []byte(`{"ts":"2026-05-24T11:35:23.495627-04:00","type":"tool_result"}`+"\n"), 0o644)).To(gomega.Succeed())

		got, ok := harnessAuditPerfReportTime(root, eval.PerfReport{
			SessionID: "tool-context_pack-latest",
			LogPath:   "logs/telemetry/tool-context_pack-latest.jsonl",
		})
		gomega.Expect(ok).To(gomega.BeTrue(), "expected timestamp from log body")

		want := time.Date(2026, 5, 24, 15, 35, 23, 495627000, time.UTC)
		gomega.Expect(got.Equal(want)).To(gomega.BeTrue(), fmt.Sprintf("timestamp = %s, want %s", got, want))

	})
	ginkgo.It("harness audit perf report time prefers log timestamp over local filename", func() {
		root := ginkgo.GinkgoT().TempDir()
		logPath := filepath.Join(root, "logs", "conversations", "20260526-040000.000.jsonl")
		gomega.Expect(os.MkdirAll(filepath.Dir(logPath), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(logPath, []byte(`{"ts":"2026-05-26T04:00:00-04:00","type":"session_start"}`+"\n"), 0o644)).To(gomega.Succeed())

		got, ok := harnessAuditPerfReportTime(root, eval.PerfReport{
			SessionID: "20260526-040000",
			LogPath:   "logs/conversations/20260526-040000.000.jsonl",
		})
		gomega.Expect(ok).To(gomega.BeTrue(), "expected timestamp from log body")

		want := time.Date(2026, 5, 26, 8, 0, 0, 0, time.UTC)
		gomega.Expect(got.Equal(want)).To(gomega.BeTrue(), fmt.Sprintf("timestamp = %s, want %s", got, want))

	})
	ginkgo.It("context cache findings treat historical stale rows as history", func() {
		findings := contextCacheFindings(&harnessAuditContextCache{
			Rows:              10,
			Fresh:             1,
			Stale:             9,
			CurrentRows:       1,
			CurrentFresh:      1,
			CurrentStale:      0,
			HistoricalRows:    9,
			StaleRatio:        0.9,
			CurrentStaleRatio: 0,
		})
		gomega.Expect(harnessAuditFindingByID(findings, "context_cache_mostly_stale")).To(gomega.BeNil(), fmt.Sprintf("historical stale rows should not report active cache as mostly stale: %#v", findings))

		finding := harnessAuditFindingByID(findings, "context_cache_historical_stale_rows")
		gomega.Expect(finding).NotTo(gomega.BeNil(), fmt.Sprintf("expected historical stale row finding, got %#v", findings))
		gomega.Expect(finding.Evidence).To(gomega.ContainSubstring("historical"), fmt.Sprintf("historical stale finding should explain active cache state: %#v", finding))
		gomega.Expect(finding.Evidence).To(gomega.ContainSubstring("current-index rows: fresh=1 stale=0"), fmt.Sprintf("historical stale finding should explain active cache state: %#v", finding))

	})
	ginkgo.It("context cache findings report current stale rows as actionable", func() {
		findings := contextCacheFindings(&harnessAuditContextCache{
			Rows:              4,
			Fresh:             1,
			Stale:             3,
			CurrentRows:       4,
			CurrentFresh:      1,
			CurrentStale:      3,
			StaleRatio:        0.75,
			CurrentStaleRatio: 0.75,
		})
		finding := harnessAuditFindingByID(findings, "context_cache_mostly_stale")
		gomega.Expect(finding).NotTo(gomega.BeNil(), fmt.Sprintf("expected active stale cache finding, got %#v", findings))
		gomega.Expect(finding.Severity).To(gomega.Equal("low"), fmt.Sprintf("current stale finding should be scoped to current rows: %#v", finding))
		gomega.Expect(finding.Evidence).To(gomega.ContainSubstring("current-index"), fmt.Sprintf("current stale finding should be scoped to current rows: %#v", finding))

	})
	ginkgo.It("context cache findings ignore historical contract gap", func() {
		findings := contextCacheFindings(&harnessAuditContextCache{
			Rows:                2,
			ContractRows:        1,
			CurrentRows:         1,
			CurrentContractRows: 1,
			HistoricalRows:      1,
		})
		gomega.Expect(harnessAuditFindingByID(findings, "context_cache_contract_gap")).To(gomega.BeNil(), fmt.Sprintf("historical no-contract rows should not raise active contract gap: %#v", findings))

	})
	ginkgo.It("context cache findings report current contract gap", func() {
		findings := contextCacheFindings(&harnessAuditContextCache{
			Rows:                2,
			ContractRows:        1,
			CurrentRows:         1,
			CurrentContractRows: 0,
			HistoricalRows:      1,
		})
		finding := harnessAuditFindingByID(findings, "context_cache_contract_gap")
		gomega.Expect(finding).NotTo(gomega.BeNil(), fmt.Sprintf("current no-contract row should raise active contract gap, got %#v", findings))
		gomega.Expect(finding.Evidence).To(gomega.ContainSubstring("current graph_views"), fmt.Sprintf("contract gap evidence should be scoped to current rows: %#v", finding))

	})
	ginkgo.It("context cache findings explain current unresolved gap reasons", func() {
		findings := contextCacheFindings(&harnessAuditContextCache{
			Rows:                                  8,
			Fresh:                                 2,
			Stale:                                 6,
			CurrentRows:                           2,
			CurrentFresh:                          2,
			UnresolvedGapRows:                     8,
			CurrentUnresolvedGapRows:              2,
			CurrentFreshUnresolvedGapRows:         2,
			UnresolvedGapReasonCounts:             map[string]int{"stale_runtime_trace": 8},
			CurrentUnresolvedGapReasonCounts:      map[string]int{"stale_runtime_trace": 2},
			CurrentFreshUnresolvedGapReasonCounts: map[string]int{"stale_runtime_trace": 2},
		})
		finding := harnessAuditFindingByID(findings, "context_cache_unresolved_gaps")
		gomega.Expect(finding).NotTo(gomega.BeNil(), fmt.Sprintf("expected current unresolved gap finding, got %#v", findings))
		gomega.Expect(finding.Evidence).To(gomega.ContainSubstring("current fresh"), fmt.Sprintf("unresolved gap finding should explain current reason counts: %#v", finding))
		gomega.Expect(finding.Evidence).To(gomega.ContainSubstring("stale_runtime_trace=2"), fmt.Sprintf("unresolved gap finding should explain current reason counts: %#v", finding))

	})
	ginkgo.It("build context cache audit reports non current rows", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/auditcache\n\ngo 1.22\n"), 0o644)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "x.go"), []byte("package auditcache\n\nfunc X() string { return \"x\" }\n"), 0o644)).To(gomega.Succeed())

		build, err := comprehension.Build(ginkgo.GinkgoT().Context(), comprehension.BuildOptions{Root: root, Language: "go"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Build: %v", err))

		current := comprehension.ViewKey{IndexID: build.IndexID, Task: "current", Mode: "plan"}
		gomega.Expect(comprehension.SaveView(root, current, comprehension.PackResult{PackID: current.ID(), IndexID: build.IndexID, CandidateFiles: []comprehension.PackFile{{Path: "x.go"}}})).To(gomega.Succeed())

		old := comprehension.ViewKey{IndexID: "old-index", Task: "old", Mode: "plan"}
		gomega.Expect(comprehension.SaveView(root, old, comprehension.PackResult{PackID: old.ID(), IndexID: "old-index", CandidateFiles: []comprehension.PackFile{{Path: "x.go"}}})).To(gomega.Succeed())

		cache := buildContextCacheAudit(ginkgo.GinkgoT().Context(), root)
		gomega.Expect(cache).NotTo(gomega.BeNil(), "expected cache audit")
		gomega.Expect(cache.Rows).To(gomega.BeNumerically("==", 2), fmt.Sprintf("expected one non-current row in cache audit, got %#v", cache))
		gomega.Expect(cache.Fresh).To(gomega.BeNumerically("==", 2), fmt.Sprintf("expected one non-current row in cache audit, got %#v", cache))
		gomega.Expect(cache.Stale).To(gomega.BeNumerically("==", 0), fmt.Sprintf("expected one non-current row in cache audit, got %#v", cache))
		gomega.Expect(cache.NonCurrent).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected one non-current row in cache audit, got %#v", cache))
		gomega.Expect(cache.CurrentIndexID).To(gomega.Equal(build.IndexID), fmt.Sprintf("expected one non-current row in cache audit, got %#v", cache))
		gomega.Expect(cache.CurrentRows).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected current/historical rows in cache audit, got %#v", cache))
		gomega.Expect(cache.CurrentFresh).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected current/historical rows in cache audit, got %#v", cache))
		gomega.Expect(cache.CurrentStale).To(gomega.BeNumerically("==", 0), fmt.Sprintf("expected current/historical rows in cache audit, got %#v", cache))
		gomega.Expect(cache.HistoricalRows).To(gomega.BeNumerically("==", 1), fmt.Sprintf("expected current/historical rows in cache audit, got %#v", cache))

	})
	// TestContextCacheAuditIncludesSavingsFields verifies that the
	// harness audit context cache report includes the Atom 6 token/cost
	// savings fields.
	ginkgo.It("context cache audit includes savings fields", func() {
		// buildContextCacheAudit reads from ReadViewStats which uses
		// the process-local atomic counters. The counters are populated
		// by RenderCompactPackProjection (full tokens saved) and
		// LoadView/LoadViewByIDStatus (rediscovery avoided).
		// Verify the fields are present with expected types even when
		// the SQLite store is absent (zero values).
		root := ginkgo.GinkgoT().TempDir()
		cache := buildContextCacheAudit(ginkgo.GinkgoT().Context(), root)

		if cache == nil {
			ginkgo.Skip("no SQLite store available; savings counters reported as zero via ReadViewStats")
		}
		// Fields must be present (zero when no activity).
		gomega.Expect(cache.EstimatedFullTokensSaved < 0).To(gomega.BeFalse(), fmt.Sprintf("EstimatedFullTokensSaved must be >= 0, got %d", cache.EstimatedFullTokensSaved))
		gomega.Expect(cache.EstimatedRediscoveryTokensAvoided < 0).To(gomega.BeFalse(), fmt.Sprintf("EstimatedRediscoveryTokensAvoided must be >= 0, got %d", cache.EstimatedRediscoveryTokensAvoided))

		ginkgo.GinkgoWriter.Printf("savings fields: full_tokens_saved=%d, rediscovery_avoided=%d\n",
			cache.EstimatedFullTokensSaved, cache.EstimatedRediscoveryTokensAvoided)
	})
	// TestHarnessAuditSummaryIncludesContextCacheMetrics verifies that
	// the audit summary string includes context cache metrics with the
	// new savings fields (Atom 6).
	ginkgo.It("harness audit summary includes context cache metrics", func() {
		summary := harnessAuditSummary(harnessAuditReport{
			Purpose: "behavior",
			ContextCache: &harnessAuditContextCache{
				Rows:                              5,
				SessionHits:                       3,
				HitRate:                           0.6,
				EstimatedFullTokensSaved:          1234,
				EstimatedRediscoveryTokensAvoided: 5678,
			},
		})
		for _, want := range []string{
			"context_cache_rows=5",
			"hits=3",
			"hit_rate=60%",
			"tokens_saved=1234",
			"rediscovery_avoided=5678",
		} {
			gomega.Expect(summary).To(gomega.ContainSubstring(want), fmt.Sprintf("audit summary missing %q: %s", want, summary))

		}
	})
})

// TestContextCacheSavingsFinding verifies that the context_cache_savings
// finding is emitted when savings metrics are non-zero, and that the
// finding ID, severity, and evidence fields are correctly structured.
var _ = ginkgo.Describe("context cache savings finding", func() {

	tests := []struct {
		name                 string
		cache                harnessAuditContextCache
		expectSavingsFinding bool
		expectContains       []string
	}{
		{
			name: "both savings counters non-zero",
			cache: harnessAuditContextCache{
				Rows:                              5,
				SessionHits:                       3,
				SessionMisses:                     2,
				EstimatedFullTokensSaved:          1000,
				EstimatedRediscoveryTokensAvoided: 2000,
				CurrentRows:                       5,
			},
			expectSavingsFinding: true,
			expectContains:       []string{"tokens_saved=1000", "rediscovery_avoided=2000"},
		},
		{
			name: "only token savings non-zero",
			cache: harnessAuditContextCache{
				Rows:                     5,
				SessionHits:              3,
				SessionMisses:            2,
				EstimatedFullTokensSaved: 1000,
				CurrentRows:              5,
			},
			expectSavingsFinding: true,
			expectContains:       []string{"tokens_saved=1000"},
		},
		{
			name: "only rediscovery savings non-zero",
			cache: harnessAuditContextCache{
				Rows:                              5,
				SessionHits:                       3,
				SessionMisses:                     2,
				EstimatedRediscoveryTokensAvoided: 2000,
				CurrentRows:                       5,
			},
			expectSavingsFinding: true,
			expectContains:       []string{"rediscovery_avoided=2000"},
		},
		{
			name: "zero savings — no finding",
			cache: harnessAuditContextCache{
				Rows:          5,
				SessionHits:   2,
				SessionMisses: 3,
				CurrentRows:   5,
			},
			expectSavingsFinding: false,
		},
	}

	for _, tt := range tests {
		ginkgo.It(tt.name, func() {
			findings := contextCacheFindings(&tt.cache)
			savingsFinding := harnessAuditFindingByID(findings, "context_cache_savings")
			if tt.expectSavingsFinding {
				gomega.Expect(savingsFinding).NotTo(gomega.BeNil(), "expected context_cache_savings finding")
			} else {
				gomega.Expect(savingsFinding).To(gomega.BeNil(), fmt.Sprintf("unexpected context_cache_savings finding: %+v", savingsFinding))
			}

			if savingsFinding != nil {
				gomega.Expect(savingsFinding.Severity).To(gomega.Equal("info"), fmt.Sprintf("savings finding severity = %q, want info", savingsFinding.Severity))

				for _, want := range tt.expectContains {
					gomega.Expect(savingsFinding.Recommendation).To(gomega.ContainSubstring(want), fmt.Sprintf("savings recommendation missing %q: %s", want, savingsFinding.Recommendation))

				}
			}
		})
	}
})
var _ = ginkgo.Describe("harness audit", func() {
	ginkgo.It("harness audit reports child lifecycle integrity", func() {
		root := ginkgo.GinkgoT().TempDir()
		logPath := filepath.Join(root, "logs", "telemetry", "child_lifecycle.jsonl")
		invocation := harnessAuditTestChildInvocation("inv-a")
		terminal := harnessAuditTestChildTerminal("term-a", invocation.InvocationID, autonomy.ChildResultEvidencePacket)

		lines := []string{
			mustHarnessAuditChildLifecycleLine("parent_child_invocation", invocation),
			mustHarnessAuditChildLifecycleLine("child_terminal", terminal),
		}
		writeWorkspaceFile(logPath, strings.Join(lines, "\n")+"\n")

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "behavior", Path: "logs/telemetry", Limit: 5})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_audit returned error: %v\n%s", result.Error, result.Output))

		var report harnessAuditReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())
		gomega.Expect(report.LogBehavior).NotTo(gomega.BeNil(), fmt.Sprintf("expected child lifecycle report, got %#v", report.LogBehavior))
		gomega.Expect(report.LogBehavior.ChildLifecycle).NotTo(gomega.BeNil(), fmt.Sprintf("expected child lifecycle report, got %#v", report.LogBehavior))
		gomega.Expect(report.LogBehavior.ChildLifecycle.Invocations).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected lifecycle counts: %#v", report.LogBehavior.ChildLifecycle))
		gomega.Expect(report.LogBehavior.ChildLifecycle.Terminals).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected lifecycle counts: %#v", report.LogBehavior.ChildLifecycle))
		gomega.Expect(harnessAuditFindingByID(report.Findings, "child_lifecycle_terminal_without_acceptance")).NotTo(gomega.BeNil(), fmt.Sprintf("expected missing acceptance finding, got %#v", report.Findings))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("child_lifecycle=2 records/1 invocations/1 terminals/0 acceptances"), fmt.Sprintf("expected child lifecycle summary, got %q", result.Summary))

	})
	ginkgo.It("audit child lifecycle logs accepts linked chain", func() {
		root := ginkgo.GinkgoT().TempDir()
		logPath := filepath.Join(root, "logs", "telemetry", "child_lifecycle.jsonl")
		invocation := harnessAuditTestChildInvocation("inv-linked")
		terminal := harnessAuditTestChildTerminal("term-linked", invocation.InvocationID, autonomy.ChildResultEvidencePacket)
		acceptance := harnessAuditTestChildAcceptance("accept-linked", invocation, terminal, autonomy.ChildAcceptanceAccepted)

		lines := []string{
			mustHarnessAuditChildLifecycleLine("parent_child_invocation", invocation),
			mustHarnessAuditChildLifecycleLine("child_terminal", terminal),
			mustHarnessAuditChildLifecycleLine("child_acceptance", acceptance),
		}
		writeWorkspaceFile(logPath, strings.Join(lines, "\n")+"\n")

		report := auditChildLifecycleLogs(filepath.Dir(logPath), []string{logPath}, func(path string) string { return path })
		gomega.Expect(report).NotTo(gomega.BeNil(), "expected child lifecycle report")
		gomega.Expect(report.Invocations).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected counts: %#v", report))
		gomega.Expect(report.Terminals).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected counts: %#v", report))
		gomega.Expect(report.Acceptances).To(gomega.BeNumerically("==", 1), fmt.Sprintf("unexpected counts: %#v", report))

		if findings := childLifecycleFindings(report); len(findings) != 0 {
			ginkgo.Fail(fmt.Sprintf("expected no child lifecycle findings, got %#v", findings))
		}
	})
	ginkgo.It("audit child lifecycle logs finds missing patch artifact and orphan acceptance", func() {
		root := ginkgo.GinkgoT().TempDir()
		logPath := filepath.Join(root, "logs", "telemetry", "child_lifecycle.jsonl")
		invocation := harnessAuditTestChildInvocation("inv-patch")
		terminal := harnessAuditTestChildTerminal("term-patch", invocation.InvocationID, autonomy.ChildResultCandidatePatch)
		terminal.ArtifactRefs = []string{"patch:missing"}
		orphanAcceptance := harnessAuditTestChildAcceptance("accept-orphan", invocation, terminal, autonomy.ChildAcceptanceRecordedOnly)
		orphanAcceptance.TerminalID = "term-missing"

		lines := []string{
			mustHarnessAuditChildLifecycleLine("parent_child_invocation", invocation),
			mustHarnessAuditChildLifecycleLine("child_terminal", terminal),
			mustHarnessAuditChildLifecycleLine("child_acceptance", orphanAcceptance),
		}
		writeWorkspaceFile(logPath, strings.Join(lines, "\n")+"\n")

		report := auditChildLifecycleLogs(filepath.Dir(logPath), []string{logPath}, func(path string) string { return path })
		gomega.Expect(report).NotTo(gomega.BeNil(), "expected child lifecycle report")
		gomega.Expect(report.MissingPatchArtifacts).To(gomega.HaveLen(1), fmt.Sprintf("expected missing patch artifact, got %#v", report.MissingPatchArtifacts))
		gomega.Expect(report.AcceptancesWithoutTerminal).To(gomega.HaveLen(1), fmt.Sprintf("expected orphan acceptance terminal, got %#v", report.AcceptancesWithoutTerminal))

		findings := childLifecycleFindings(report)
		for _, id := range []string{"child_lifecycle_missing_patch_artifact", "child_lifecycle_acceptance_without_source"} {
			gomega.Expect(harnessAuditFindingByID(findings, id)).NotTo(gomega.BeNil(), fmt.Sprintf("expected finding %s, got %#v", id, findings))

		}
	})
	ginkgo.It("audit child lifecycle logs finds mismatched patch artifact owner", func() {
		root := ginkgo.GinkgoT().TempDir()
		logPath := filepath.Join(root, "logs", "telemetry", "child_lifecycle.jsonl")
		invocation := harnessAuditTestChildInvocation("inv-patch-owner")
		terminal := harnessAuditTestChildTerminal("term-patch-owner", invocation.InvocationID, autonomy.ChildResultCandidatePatch)
		terminal.ArtifactRefs = []string{"patch:patch-other"}
		artifact := autonomy.CandidatePatchArtifact{
			PatchID:         "patch-other",
			InvocationID:    "other-invocation",
			ChildContractID: "other-child",
			BaseSHA:         "base",
			ChangedPaths:    []string{"internal/runtime/agent/example.go"},
			DiffStat:        autonomy.PatchDiffStat{Files: 1},
			PatchPath:       "workbranch://patch",
			PatchDigest:     "sha256:patch",
			CreatedAt:       time.Now().UTC(),
		}

		lines := []string{
			mustHarnessAuditChildLifecycleLine("parent_child_invocation", invocation),
			mustHarnessAuditChildLifecycleLine("candidate_patch_artifact", artifact),
			mustHarnessAuditChildLifecycleLine("child_terminal", terminal),
			mustHarnessAuditChildLifecycleLine("child_acceptance", harnessAuditTestChildAcceptance("accept-patch-owner", invocation, terminal, autonomy.ChildAcceptanceRecordedOnly)),
		}
		writeWorkspaceFile(logPath, strings.Join(lines, "\n")+"\n")

		report := auditChildLifecycleLogs(filepath.Dir(logPath), []string{logPath}, func(path string) string { return path })
		gomega.Expect(report).NotTo(gomega.BeNil(), "expected child lifecycle report")
		gomega.Expect(report.MismatchedLinks).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected mismatched patch artifact link, got %#v", report))

		if finding := harnessAuditFindingByID(childLifecycleFindings(report), "child_lifecycle_mismatched_link"); finding == nil {
			ginkgo.Fail(fmt.Sprintf("expected mismatched link finding"))
		}
	})
	ginkgo.It("audit child lifecycle logs finds mismatched acceptance links", func() {
		root := ginkgo.GinkgoT().TempDir()
		logPath := filepath.Join(root, "logs", "telemetry", "child_lifecycle.jsonl")
		invocation := harnessAuditTestChildInvocation("inv-mismatch")
		terminal := harnessAuditTestChildTerminal("term-mismatch", invocation.InvocationID, autonomy.ChildResultEvidencePacket)
		acceptance := harnessAuditTestChildAcceptance("accept-mismatch", invocation, terminal, autonomy.ChildAcceptanceAccepted)
		acceptance.ChildContractID = "child-other"

		lines := []string{
			mustHarnessAuditChildLifecycleLine("parent_child_invocation", invocation),
			mustHarnessAuditChildLifecycleLine("child_terminal", terminal),
			mustHarnessAuditChildLifecycleLine("child_acceptance", acceptance),
		}
		writeWorkspaceFile(logPath, strings.Join(lines, "\n")+"\n")

		report := auditChildLifecycleLogs(filepath.Dir(logPath), []string{logPath}, func(path string) string { return path })
		gomega.Expect(report).NotTo(gomega.BeNil(), "expected child lifecycle report")
		gomega.Expect(report.MismatchedLinks).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected mismatched lifecycle link, got %#v", report))

		if finding := harnessAuditFindingByID(childLifecycleFindings(report), "child_lifecycle_mismatched_link"); finding == nil {
			ginkgo.Fail(fmt.Sprintf("expected mismatched link finding"))
		}
	})
	ginkgo.It("audit child lifecycle logs finds invalid plan execute lifecycle event", func() {
		root := ginkgo.GinkgoT().TempDir()
		logPath := filepath.Join(root, "logs", "telemetry", "child_lifecycle.jsonl")
		line, err := json.Marshal(map[string]any{
			"type":  "idiom.plan_execute.child_terminal_invalid",
			"error": "invalid child terminal record",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal invalid event: %v", err))

		writeWorkspaceFile(logPath, string(line)+"\n")

		report := auditChildLifecycleLogs(filepath.Dir(logPath), []string{logPath}, func(path string) string { return path })
		gomega.Expect(report).NotTo(gomega.BeNil(), "expected child lifecycle report")
		gomega.Expect(report.InvalidRecords).To(gomega.HaveLen(1), fmt.Sprintf("expected invalid record, got %#v", report))

		if finding := harnessAuditFindingByID(childLifecycleFindings(report), "child_lifecycle_invalid_record"); finding == nil {
			ginkgo.Fail(fmt.Sprintf("expected invalid record finding"))
		}
	})
	ginkgo.It("audit child lifecycle logs finds invalid verification acceptance", func() {
		root := ginkgo.GinkgoT().TempDir()
		logPath := filepath.Join(root, "logs", "telemetry", "child_lifecycle.jsonl")
		record := autonomy.VerificationAcceptanceRecord{
			VerificationAcceptanceID: "verify-a",
			ParentContractID:         "parent-a",
			ObligationRefs:           []string{"obligation:post_write_verification"},
			CommandRef:               "command:go_test",
			CommandFingerprint:       "sha256:abc",
			WorkspaceRef:             "workspace:root",
			BaseSHA:                  "base",
			HeadSHA:                  "head",
			ExitStatus:               1,
			ResultRef:                "result:go_test",
			Decision:                 autonomy.VerificationAcceptanceAccepted,
			ReasonCode:               "exit_zero_required",
		}

		writeWorkspaceFile(logPath, mustHarnessAuditChildLifecycleLine("verification_acceptance", record)+"\n")

		report := auditChildLifecycleLogs(filepath.Dir(logPath), []string{logPath}, func(path string) string { return path })
		gomega.Expect(report).NotTo(gomega.BeNil(), "expected child lifecycle report")
		gomega.Expect(report.VerificationAcceptanceViolations).To(gomega.HaveLen(1), fmt.Sprintf("expected verification violation, got %#v", report))

		if finding := harnessAuditFindingByID(childLifecycleFindings(report), "verification_acceptance_invalid"); finding == nil {
			ginkgo.Fail(fmt.Sprintf("expected verification acceptance finding"))
		}
	})
	ginkgo.It("audit child lifecycle logs finds verification acceptance without result", func() {
		root := ginkgo.GinkgoT().TempDir()
		logPath := filepath.Join(root, "logs", "telemetry", "child_lifecycle.jsonl")
		record := autonomy.VerificationAcceptanceRecord{
			VerificationAcceptanceID: "verify-missing-result",
			ParentContractID:         "parent-a",
			ObligationRefs:           []string{"obligation:planner_owned_verification"},
			CommandRef:               "command:missing",
			CommandFingerprint:       "sha256:missing",
			WorkspaceRef:             "workspace:root",
			BaseSHA:                  "base",
			HeadSHA:                  "head",
			ExitStatus:               0,
			ResultRef:                "verification_result:missing",
			Decision:                 autonomy.VerificationAcceptanceAccepted,
			ReasonCode:               "verification_passed",
		}

		writeWorkspaceFile(logPath, mustHarnessAuditChildLifecycleLine("verification_acceptance", record)+"\n")

		report := auditChildLifecycleLogs(filepath.Dir(logPath), []string{logPath}, func(path string) string { return path })
		gomega.Expect(report).NotTo(gomega.BeNil(), "expected child lifecycle report")
		gomega.Expect(report.VerificationAcceptancesWithoutResult).To(gomega.HaveLen(1), fmt.Sprintf("expected missing verification result, got %#v", report))

		if finding := harnessAuditFindingByID(childLifecycleFindings(report), "verification_acceptance_without_result"); finding == nil {
			ginkgo.Fail(fmt.Sprintf("expected missing verification result finding"))
		}
	})
	ginkgo.It("audit child lifecycle logs accepts verification acceptance with result", func() {
		root := ginkgo.GinkgoT().TempDir()
		logPath := filepath.Join(root, "logs", "telemetry", "child_lifecycle.jsonl")
		command := "go test ./pkg"
		resultText := strings.Join([]string{"verification", command, "", "passed", "", ""}, "\x00")
		record := autonomy.VerificationAcceptanceRecord{
			VerificationAcceptanceID: "verify-with-result",
			ParentContractID:         "parent-a",
			ObligationRefs:           []string{"obligation:planner_owned_verification"},
			CommandRef:               "command:" + harnessAuditShortHash(command),
			CommandFingerprint:       "sha256:" + harnessAuditShortHash(command),
			WorkspaceRef:             "workspace:root",
			BaseSHA:                  "base",
			HeadSHA:                  "head",
			ExitStatus:               0,
			ResultRef:                "verification_result:" + harnessAuditShortHash(resultText),
			Decision:                 autonomy.VerificationAcceptanceAccepted,
			ReasonCode:               "verification_passed",
		}
		verificationEvent, err := json.Marshal(map[string]any{
			"type":    "idiom.plan_execute.verification",
			"command": command,
			"status":  "passed",
			"summary": "",
			"error":   "",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal verification event: %v", err))

		lines := []string{
			string(verificationEvent),
			mustHarnessAuditChildLifecycleLine("verification_acceptance", record),
		}
		writeWorkspaceFile(logPath, strings.Join(lines, "\n")+"\n")

		report := auditChildLifecycleLogs(filepath.Dir(logPath), []string{logPath}, func(path string) string { return path })
		gomega.Expect(report).NotTo(gomega.BeNil(), "expected child lifecycle report")
		gomega.Expect(report.VerificationAcceptancesWithoutResult).To(gomega.BeEmpty(), fmt.Sprintf("did not expect missing verification result, got %#v", report.VerificationAcceptancesWithoutResult))

	})
})

func harnessAuditTestChildInvocation(id string) autonomy.ParentChildInvocationRecord {
	return autonomy.ParentChildInvocationRecord{
		InvocationID:     id,
		ParentContractID: "parent-" + id,
		ChildContractID:  "child-" + id,
		Role:             "child_read_only:explorer",
		Lane:             "implement",
		Phase:            "child_read_only",
		WorkspaceMode:    autonomy.ChildWorkspaceNoWrite,
		ContractSummary: autonomy.ChildContractSummary{
			PathRoleDigest:   "sha256:path",
			ToolPolicyDigest: "sha256:tool",
			ObligationDigest: "sha256:obligation",
		},
		RuntimeEnvelope: autonomy.ChildRuntimeEnvelope{
			BackendKind: "llm_client",
			ModelAlias:  "qwen-flash",
			ModelRole:   "child_read_only:explorer",
			RoleResolution: autonomy.ChildRoleResolution{
				RequestedRole: "child_read_only:explorer",
				ResolvedRole:  "child_read_only:explorer",
				Alias:         "qwen-flash",
				Source:        autonomy.RoleResolutionSourceRoleTag,
				OK:            true,
			},
			CapacityAdmission: autonomy.ChildCapacityAdmission{
				State:       autonomy.CapacityAdmissionAcquired,
				Enforcement: autonomy.CapacityAdmissionOpportunistic,
			},
			ToolTransportMode:   autonomy.ChildTransportTextual,
			ParserTransportMode: autonomy.ChildTransportTextual,
		},
		CreatedAt: time.Now().UTC(),
	}
}

func harnessAuditTestChildTerminal(id string, invocationID string, resultKind autonomy.ChildResultKind) autonomy.ChildTerminalRecord {
	record := autonomy.ChildTerminalRecord{
		TerminalID:         id,
		InvocationID:       invocationID,
		ChildContractID:    "child-" + invocationID,
		ResultKind:         resultKind,
		Status:             autonomy.ChildTerminalCompleted,
		EvidenceRefs:       []string{"tool_result:context_pack:1"},
		ObjectiveAlignment: autonomy.ChildObjectiveAccepted,
	}
	if resultKind == autonomy.ChildResultCandidatePatch {
		record.EvidenceRefs = nil
		record.ChangedPaths = []string{"internal/runtime/agent/example.go"}
	}
	return record
}

func harnessAuditTestChildAcceptance(id string, invocation autonomy.ParentChildInvocationRecord, terminal autonomy.ChildTerminalRecord, decision autonomy.ChildAcceptanceDecision) autonomy.ChildAcceptanceRecord {
	return autonomy.ChildAcceptanceRecord{
		AcceptanceID:         id,
		ParentContractID:     invocation.ParentContractID,
		ChildContractID:      invocation.ChildContractID,
		InvocationID:         invocation.InvocationID,
		TerminalID:           terminal.TerminalID,
		ResultKind:           terminal.ResultKind,
		Decision:             decision,
		AcceptedEvidenceRefs: []string{"tool_result:context_pack:1"},
		AlignmentBasis: autonomy.ChildAlignmentBasis{
			DelegatedObjectiveRef:  "objective:task",
			EffectiveScopeRefs:     []string{"scope:repo"},
			AcceptedSourceBodyRefs: []string{"source_body:target"},
			Evaluator:              autonomy.AlignmentEvaluatorHostContract,
		},
		ReasonCode: "objective_aligned",
	}
}

func mustHarnessAuditChildLifecycleLine(role string, record any) string {
	ginkgo.GinkgoHelper()

	switch typed := record.(type) {
	case autonomy.ParentChildInvocationRecord:
		typed.Normalize()
		record = typed
	case autonomy.ChildTerminalRecord:
		typed.Normalize()
		record = typed
	case autonomy.ChildAcceptanceRecord:
		typed.Normalize()
		record = typed
	case autonomy.CandidatePatchArtifact:
		typed.Normalize()
		record = typed
	case autonomy.VerificationAcceptanceRecord:
		typed.Normalize()
		record = typed
	}

	data, err := json.Marshal(record)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal child lifecycle record: %v", err))

	wrapped, err := json.Marshal(map[string]string{
		"type":    "message",
		"role":    role,
		"content": string(data),
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal wrapped child lifecycle record: %v", err))

	return string(wrapped)
}

func harnessAuditFindingByID(findings []harnessAuditFinding, id string) *harnessAuditFinding {
	for i := range findings {
		if findings[i].ID == id {
			return &findings[i]
		}
	}

	return nil
}
