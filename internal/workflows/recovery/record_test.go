package recovery

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/onsi/gomega/gstruct"

	plancontract "idiom/internal/contracts/plan"
	recoverycontract "idiom/internal/contracts/recovery"
)

var _ = ginkgo.Describe("Recovery failure records", func() {
	ginkgo.Describe("plan-execute failures", func() {
		ginkgo.It("classifies a human question as planner-blocked", func() {
			record := expectPlanExecuteFailureRecord(PlanExecuteFailureInput{
				SessionID:         "session-1",
				ProgressStatePath: ".idiom/plan-execute/sessions/session-1.json",
				Phase:             "planning",
				Status:            "failed",
				FailureKind:       "planner_blocked",
				FailureReason:     "<<QUESTION>>: docs need a human answer",
				RequiredEditPaths: []string{"docs/design.md"},
			})

			gomega.Expect(record).To(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
				"FailureID":  gomega.Equal("plan-execute-session-1"),
				"NextAction": gomega.Equal(recoverycontract.NextActionAnswerBlocker),
			}))
			gomega.Expect(record.Failure.Class).To(gomega.Equal(recoverycontract.FailureClassPlannerBlocked))
			gomega.Expect(record.Failure.EvidenceRefs).NotTo(gomega.BeEmpty())
			gomega.Expect(record.Failure.EvidenceRefs[0].Kind).To(gomega.Equal("progress_state"))
		})

		ginkgo.It("retries when submit_plan is absent from the required runtime surface", func() {
			record := expectPlanExecuteFailureRecord(PlanExecuteFailureInput{
				SessionID:         "atom-1-run-1",
				ProgressStatePath: ".idiom/plan-execute/sessions/atom-1-run-1.json",
				Phase:             "planning",
				Status:            "failed",
				FailureKind:       "planner_blocked",
				FailureReason: "<<QUESTION>>: Implementation incomplete; required edit path(s) remain unchanged.\n\n" +
					"Blocker:\n- Tool not available in current runtime contract: submit_plan is not in the current allowed tool surface. Available tools: read_file, code_navigate.",
				RequiredEditPaths: []string{"native/src/core/colony_sim_tick.cpp"},
			})

			gomega.Expect(record.Failure.Class).To(gomega.Equal(recoverycontract.FailureClassPlannerBlocked))
			gomega.Expect(record.NextAction).To(gomega.Equal(recoverycontract.NextActionRetryPlanExecute))
		})

		ginkgo.It("classifies a bounded anchor refresh as a typed retry", func() {
			reason := strings.Join([]string{
				"<<QUESTION>>: Plan-execute anchor refresh is still required before a corrected submit_plan can be accepted.",
				"",
				"Remaining bounded refresh paths:",
				"- native/src/core/colony_sim_tick.cpp",
				"Next valid action: re-run with enough budget and issue exactly one bounded read_file or code_navigate source-body request for a remaining refresh path.",
			}, "\n")
			requiredPaths := []string{
				"native/src/core/colony_sim_tick.cpp",
				"native/src/simulation/world/collapse_event.cpp",
			}
			record := expectPlanExecuteFailureRecord(PlanExecuteFailureInput{
				SessionID:         "session-anchor-refresh",
				ProgressStatePath: ".idiom/plan-execute/sessions/session-anchor-refresh.json",
				Phase:             "planning",
				Status:            "failed",
				FailureKind:       "planner_blocked",
				FailureReason:     reason,
				RequiredEditPaths: requiredPaths,
			})

			gomega.Expect(record.Failure.Class).To(gomega.Equal(recoverycontract.FailureClassPlannerBlocked))
			gomega.Expect(record.NextAction).To(gomega.Equal(recoverycontract.NextActionRetryPlanExecute))
			refresh, ok := PlanExecutePlannerBlockedSourceRefreshResult("planner_blocked", reason, requiredPaths)
			gomega.Expect(ok).To(gomega.BeTrue())
			gomega.Expect(refresh).To(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
				"ActionClass": gomega.Equal(recoverycontract.NextActionRetryPlanExecute),
				"Reason":      gomega.Equal("anchor_refresh"),
				"Path":        gomega.Equal("native/src/core/colony_sim_tick.cpp"),
				"Paths":       gomega.Equal([]string{"native/src/core/colony_sim_tick.cpp"}),
			}))
		})

		ginkgo.It("recognizes the live bounded-anchor blocker text", func() {
			reason := strings.Join([]string{
				"<<QUESTION>>: Implementation incomplete; required edit path(s) remain unchanged.",
				"",
				"Blocker:",
				"- Bounded submit_plan anchor refresh required: the previous `submit_plan` was rejected before it became a valid plan artifact, and the corrected plan still needs bounded source-body refresh for: native/src/core/colony_sim_tick.cpp.",
				"Do not call `submit_plan` until the remaining anchor refresh returns.",
			}, "\n")
			record := expectPlanExecuteFailureRecord(PlanExecuteFailureInput{
				SessionID:         "session-live-anchor-refresh",
				ProgressStatePath: ".idiom/plan-execute/sessions/session-live-anchor-refresh.json",
				Phase:             "planning",
				Status:            "failed",
				FailureKind:       "planner_blocked",
				FailureReason:     reason,
				RequiredEditPaths: []string{
					"native/src/core/colony_sim_tick.cpp",
					"native/src/simulation/world/collapse_event.cpp",
					"native/src/simulation/world/collapse_event.h",
					"native/tests/test_collapse_event.cpp",
				},
			})

			gomega.Expect(record.NextAction).To(gomega.Equal(recoverycontract.NextActionRetryPlanExecute))
			refresh, ok := PlanExecutePlannerBlockedSourceRefreshResult("planner_blocked", reason, record.SourceRun.RequiredEditPaths)
			gomega.Expect(ok).To(gomega.BeTrue())
			gomega.Expect(refresh.Reason).To(gomega.Equal("anchor_refresh"))
			gomega.Expect(refresh.Path).To(gomega.Equal("native/src/core/colony_sim_tick.cpp"))
		})

		ginkgo.It("retains a truncated anchor-refresh marker as machine-actionable", func() {
			record := expectPlanExecuteFailureRecord(PlanExecuteFailureInput{
				SessionID:         "session-anchor-refresh-truncated",
				ProgressStatePath: ".idiom/plan-execute/sessions/session-anchor-refresh-truncated.json",
				Phase:             "planning",
				Status:            "failed",
				FailureKind:       "planner_blocked",
				FailureReason:     "<<QUESTION>>: Plan-execute anchor refresh is still required before a corrected submit_plan can be accepted.",
				RequiredEditPaths: []string{
					"native/src/core/colony_sim_tick.cpp",
					"native/src/simulation/world/collapse_event.cpp",
				},
			})

			gomega.Expect(record.NextAction).To(gomega.Equal(recoverycontract.NextActionRetryPlanExecute))
			refresh, ok := PlanExecutePlannerBlockedSourceRefreshResult("planner_blocked", record.Failure.Summary, record.SourceRun.RequiredEditPaths)
			gomega.Expect(ok).To(gomega.BeTrue())
			gomega.Expect(refresh.Path).To(gomega.Equal("native/src/core/colony_sim_tick.cpp"))
			gomega.Expect(refresh.Reason).To(gomega.Equal("anchor_refresh"))
			gomega.Expect(refresh.Paths).To(gomega.Equal([]string{
				"native/src/core/colony_sim_tick.cpp",
				"native/src/simulation/world/collapse_event.cpp",
			}))
		})

		ginkgo.It("uses the typed next action as a retry-refresh fallback", func() {
			requiredPaths := []string{"native/src/core/colony_sim_tick.cpp", "native/src/simulation/world/collapse_event.h"}
			refresh, ok := PlanExecutePlannerBlockedRetryRefreshResult(
				"planner_blocked",
				"<<QUESTION>>: source refresh needed",
				requiredPaths,
				recoverycontract.NextActionRetryPlanExecute,
			)
			gomega.Expect(ok).To(gomega.BeTrue())
			gomega.Expect(refresh.Path).To(gomega.Equal("native/src/core/colony_sim_tick.cpp"))
			gomega.Expect(refresh.Reason).To(gomega.Equal("retry_action"))

			instruction := PlanExecuteSourceRefreshRetryInstruction(refresh, requiredPaths)
			gomega.Expect(instruction).To(gomega.And(
				gomega.ContainSubstring("First refresh source-body evidence for each candidate anchor path"),
				gomega.ContainSubstring("native/src/core/colony_sim_tick.cpp, native/src/simulation/world/collapse_event.h"),
				gomega.ContainSubstring("then satisfy runtime-required git history evidence"),
				gomega.ContainSubstring("then produce one valid `submit_plan`"),
			))
		})

		ginkgo.It("carries a locked contract and redacted invariant failures", func() {
			record := expectPlanExecuteFailureRecord(PlanExecuteFailureInput{
				SessionID:         "session-contract-context",
				FailureKind:       "plan_artifact_invalid",
				FailureReason:     "submit_plan violated a contract invariant",
				RequiredEditPaths: []string{"pkg/feature.go"},
				AllowedEditPaths:  []string{"pkg/feature_test.go"},
				LockedContract: &recoverycontract.ContractSnapshot{
					ContractID:           "contract_plan_execute_123",
					ContractHash:         "sha256:contract",
					SnapshotID:           "snapshot-123",
					SnapshotPath:         ".idiom/contracts/snapshot.json",
					RuntimeContractID:    "runtime-123",
					ProtocolHash:         "sha256:protocol",
					WriteAllowedPaths:    []string{"pkg/feature.go"},
					WriteAllowedExplicit: true,
				},
				InvariantFailures: []recoverycontract.InvariantFailure{{
					InvariantID:            "contract.required_edit_coverage",
					FieldPath:              "submit_plan.edits[0].path",
					Path:                   "pkg/feature.go",
					Expected:               "path inside locked required_edit_paths",
					Actual:                 "path omitted from submitted edits",
					RequiredRepairEvidence: []string{"source-body evidence for pkg/feature.go", "raw_prompt: hidden"},
				}},
			})

			gomega.Expect(record.LockedContract).NotTo(gomega.BeNil())
			gomega.Expect(record.LockedContract).To(gstruct.PointTo(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
				"ContractID":           gomega.Equal("contract_plan_execute_123"),
				"ContractHash":         gomega.Equal("sha256:contract"),
				"SnapshotID":           gomega.Equal("snapshot-123"),
				"SnapshotPath":         gomega.Equal(".idiom/contracts/snapshot.json"),
				"RequiredEditPaths":    gomega.Equal([]string{"pkg/feature.go"}),
				"AllowedEditPaths":     gomega.Equal([]string{"pkg/feature_test.go"}),
				"WriteAllowedPaths":    gomega.Equal([]string{"pkg/feature.go"}),
				"WriteAllowedExplicit": gomega.BeTrue(),
			})))
			gomega.Expect(record.Failure.InvariantFailures).To(gomega.HaveLen(1))
			failure := record.Failure.InvariantFailures[0]
			gomega.Expect(failure).To(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
				"InvariantID": gomega.Equal("contract.required_edit_coverage"),
				"FieldPath":   gomega.Equal("submit_plan.edits[0].path"),
				"Path":        gomega.Equal("pkg/feature.go"),
			}))
			gomega.Expect(failure.RequiredRepairEvidence).To(gomega.HaveLen(2))
			gomega.Expect(failure.RequiredRepairEvidence[1]).To(gomega.ContainSubstring("redacted"))
		})

		ginkgo.It("redacts sensitive executor details", func() {
			record := expectPlanExecuteFailureRecord(PlanExecuteFailureInput{
				SessionID:     "session-3",
				FailureKind:   "executor_failed",
				FailureReason: "executor failed\nOPENAI_API_KEY=sk-live-secret",
				ExecutorFailures: []string{
					"request failed with bearer abc123",
				},
			})

			gomega.Expect(record.Failure.EvidenceRefs).NotTo(gomega.BeEmpty())
			for _, value := range []string{record.Failure.Summary, record.Failure.EvidenceRefs[0].Summary} {
				gomega.Expect(value).NotTo(gomega.BeEmpty())
				gomega.Expect(value).NotTo(gomega.Or(
					gomega.ContainSubstring("OPENAI_API_KEY"),
					gomega.ContainSubstring("sk-live-secret"),
					gomega.ContainSubstring("bearer abc123"),
				))
			}
			gomega.Expect(record.Failure.Summary).To(gomega.ContainSubstring("[redacted sensitive recovery detail]"))
		})
	})

	ginkgo.Describe("durable records", func() {
		ginkgo.It("preserves created_at across rewrites and advances updated_at", func() {
			root := ginkgo.GinkgoT().TempDir()
			input := PlanExecuteFailureInput{
				SessionID:     "session-2",
				Phase:         "planning",
				Status:        "failed",
				FailureKind:   "plan_artifact_missing",
				FailureReason: "no plan artifact",
			}
			firstTime := recoveryRecordTestTime()
			first, path, err := WritePlanExecuteFailureRecord(root, input, firstTime)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(path).To(gomega.Equal(filepath.ToSlash(filepath.Join(".idiom", "recovery", "plan-execute-session-2", "failure.json"))))

			second, _, err := WritePlanExecuteFailureRecord(root, input, firstTime.Add(time.Hour))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(second.CreatedAt).To(gomega.Equal(first.CreatedAt))
			gomega.Expect(second.UpdatedAt).NotTo(gomega.Equal(first.UpdatedAt))
			_, err = os.Stat(filepath.Join(root, filepath.FromSlash(path)))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		})

		ginkgo.It("rejects a symlinked recovery directory without writing outside", func() {
			root := ginkgo.GinkgoT().TempDir()
			outside := ginkgo.GinkgoT().TempDir()
			gomega.Expect(os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)).To(gomega.Succeed())
			if err := os.Symlink(outside, filepath.Join(root, ".idiom", "recovery")); err != nil {
				ginkgo.Skip("symlinks unavailable: " + err.Error())
			}

			_, _, err := WritePlanExecuteFailureRecord(root, PlanExecuteFailureInput{
				SessionID:   "session-symlink-recovery",
				FailureKind: "planner_failed",
			}, recoveryRecordTestTime())
			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("symlinked directories")))
			_, statErr := os.Stat(filepath.Join(outside, "plan-execute-session-symlink-recovery", "failure.json"))
			gomega.Expect(statErr).To(gomega.Satisfy(os.IsNotExist))
		})

		ginkgo.It("rejects invalid contract vocabulary while loading", func() {
			root := ginkgo.GinkgoT().TempDir()
			failureID := "implement-session-invalid-load"
			fullPath, _, err := FailureRecordPath(root, failureID)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(os.MkdirAll(filepath.Dir(fullPath), 0o755)).To(gomega.Succeed())
			body := `{
  "schema_version":"idiom.recovery.failure_record.v1",
  "failure_id":"implement-session-invalid-load",
  "source_run":{"lane":"implement","session_id":"session-invalid-load","required_edit_paths":["../escape.go"]},
  "failure":{"class":"made_up_class","summary":"bad record"},
  "partial_state":{"dirty_status":"mystery","scope_status":"none"},
  "next_valid_action":"made_up_action"
}`
			gomega.Expect(os.WriteFile(fullPath, []byte(body), 0o600)).To(gomega.Succeed())

			_, _, _, err = TryLoadFailureRecord(root, failureID)
			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("invalid recovery failure record")))
		})

		ginkgo.It("preserves canonical plan-execute failure IDs through build and write", func() {
			record := expectPlanExecuteFailureRecord(PlanExecuteFailureInput{
				SessionID:   "plan-execute-existing",
				FailureKind: "planner_failed",
			})
			gomega.Expect(record.FailureID).To(gomega.Equal("plan-execute-existing"))

			written, path, err := WritePlanExecuteFailureRecord(ginkgo.GinkgoT().TempDir(), PlanExecuteFailureInput{
				SessionID:   "session-wrapper",
				FailureKind: "planner_failed",
			}, recoveryRecordTestTime())
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(written.FailureID).To(gomega.Equal("plan-execute-session-wrapper"))
			gomega.Expect(path).To(gomega.Equal(filepath.ToSlash(filepath.Join(".idiom", "recovery", "plan-execute-session-wrapper", "failure.json"))))
		})

		ginkgo.It("finds an implement-lane record by session ID", func() {
			root := ginkgo.GinkgoT().TempDir()
			written, _, err := WriteFailureRecord(root, FailureRecordInput{
				FailureID: ImplementFailureID("session-implement-load"),
				SourceRun: recoverycontract.SourceRun{
					Lane:      string(recoverycontract.SourceLaneImplement),
					SessionID: "session-implement-load",
				},
				FailureClass: recoverycontract.FailureClassExecutorEdit,
				FailureKind:  "implement_process_failed",
			}, recoveryRecordTestTime())
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			loaded, path, found, err := TryLoadFailureRecordForSession(root, "session-implement-load")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(found).To(gomega.BeTrue())
			gomega.Expect(loaded.FailureID).To(gomega.Equal(written.FailureID))
			gomega.Expect(path).To(gomega.Equal(filepath.ToSlash(filepath.Join(".idiom", "recovery", written.FailureID, "failure.json"))))
		})
	})

	ginkgo.Describe("generic implement failures", func() {
		ginkgo.It("defaults to inspect-failure with source-run context intact", func() {
			record := expectFailureRecord(FailureRecordInput{
				FailureID: "implement-session-implement",
				SourceRun: recoverycontract.SourceRun{
					Lane:              string(recoverycontract.SourceLaneImplement),
					SessionID:         "session-implement",
					PromptFile:        "atoms/brief.md",
					RequiredEditPaths: []string{"internal/workflows/recovery/record.go"},
				},
				FailureKind:    "implement_process_failed",
				FailureSummary: "implement process failed: exit_code=1 final_state=failed stop_reason=model_error degradations=queue_degraded",
				EvidenceRefs: []recoverycontract.EvidenceRef{
					{Kind: "lane_history", Path: ".idiom/lane-history.jsonl"},
					{Kind: "session_summary", ID: "session-implement", Summary: "stop_reason=model_error"},
				},
			})

			gomega.Expect(record).To(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
				"FailureID":  gomega.Equal("implement-session-implement"),
				"NextAction": gomega.Equal(recoverycontract.NextActionInspectFailure),
			}))
			gomega.Expect(record.SourceRun).To(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
				"Lane":              gomega.Equal(string(recoverycontract.SourceLaneImplement)),
				"PromptFile":        gomega.Equal("atoms/brief.md"),
				"RequiredEditPaths": gomega.Equal([]string{"internal/workflows/recovery/record.go"}),
			}))
			gomega.Expect(record.Failure.Class).To(gomega.Equal(recoverycontract.FailureClassUnknown))
		})

		ginkgo.It("redacts generic failure summaries and evidence", func() {
			record := expectFailureRecord(FailureRecordInput{
				FailureID: "implement-session-redact",
				SourceRun: recoverycontract.SourceRun{
					Lane:      string(recoverycontract.SourceLaneImplement),
					SessionID: "session-redact",
				},
				FailureKind:    "implement_process_failed",
				FailureSummary: "exit_code=1\nraw_prompt: do the secret thing\nOPENAI_API_KEY=sk-live-secret",
				EvidenceRefs: []recoverycontract.EvidenceRef{
					{Kind: "session_summary", ID: "session-redact", Summary: "transcript: hidden\nbearer abc123"},
				},
			})

			rendered := record.Failure.Summary + "\n" + record.Failure.EvidenceRefs[0].Summary
			gomega.Expect(rendered).NotTo(gomega.Or(
				gomega.ContainSubstring("raw_prompt"),
				gomega.ContainSubstring("OPENAI_API_KEY"),
				gomega.ContainSubstring("sk-live-secret"),
				gomega.ContainSubstring("transcript"),
				gomega.ContainSubstring("bearer abc123"),
			))
			gomega.Expect(rendered).To(gomega.ContainSubstring("[redacted sensitive recovery detail]"))
		})

		ginkgo.It("preserves every durable implementation authority field while cleaning paths and text", func() {
			implementation := &recoverycontract.ImplementationState{
				Phase:                           " target_write ",
				StopReason:                      " interrupted ",
				RemainingRequiredEditPaths:      []string{`pkg\feature.go`},
				MissingRequiredEditSnippetIDs:   []string{" snippet-1 "},
				MissingRequiredEditSnippetPaths: []string{`pkg\feature.go`},
				UnresolvedRejectedMutationPaths: []string{`pkg\failed.go`},
				OpenTaskLedgerObligationIDs:     []string{" task-1 "},
				BlockedTaskLedgerItems: []recoverycontract.ImplementationBlockedTaskItem{{
					ID: " blocked-1 ", Kind: "tool", SubjectNamespace: "path", SubjectID: " pkg/failed.go ",
					Reason: " operator review ", Paths: []string{`pkg\failed.go`},
				}},
				TerminalDisposition:         " exact_blocker ",
				ChangedPaths:                []string{`pkg\feature.go`},
				SourcedEvidencePaths:        []string{`pkg\feature.go`},
				VerificationPending:         true,
				VerificationFailed:          true,
				SemanticVerificationPending: true,
				WriteReadyNudgeCount:        2,
				CanonicalMutationScope: &recoverycontract.CanonicalMutationScopeProjection{
					RequiredCoverageIDs: []string{`pkg\feature.go`},
					Rules: []recoverycontract.CanonicalMutationScopeRuleProjection{{
						Kind: " exact_file ", Source: " required_path ", CanonicalID: `pkg\feature.go`,
					}},
					Presentations: []recoverycontract.CanonicalMutationScopePresentationProjection{{
						PresentedPath: `alias\feature.go`, CanonicalID: `pkg\feature.go`, Source: " required_path ",
					}},
				},
				CommitReceipts: []recoverycontract.MutationCommitProjection{{
					Effects: []recoverycontract.MutationCommitEffectProjection{{
						CanonicalID: `pkg\feature.go`, Kind: " replaced_file ", Role: " file ",
					}},
					PostimageFileIDs: []string{`pkg\feature.go`},
					AffectedFileIDs:  []string{`pkg\feature.go`},
					FailedTargets: []recoverycontract.MutationTargetProjection{{
						CanonicalID: `pkg\failed.go`, Role: " destination ", PreimageClass: " absent ",
					}},
					UnattemptedTargets: []recoverycontract.MutationTargetProjection{{
						CanonicalID: `pkg\later.go`, Role: " destination ", PreimageClass: " absent ",
					}},
				}},
			}

			record := expectFailureRecord(FailureRecordInput{
				FailureID: "implement-durable-authority",
				SourceRun: recoverycontract.SourceRun{
					Lane: string(recoverycontract.SourceLaneImplement), SessionID: "durable-authority",
				},
				FailureClass:   recoverycontract.FailureClassExecutorEdit,
				Implementation: implementation,
			})

			gomega.Expect(record.Implementation).NotTo(gomega.BeNil())
			gomega.Expect(recoverycontract.ValidateImplementationState(record.Implementation)).To(gomega.Succeed())
			gomega.Expect(record.Implementation.MissingRequiredEditSnippetIDs).To(gomega.Equal([]string{"snippet-1"}))
			gomega.Expect(record.Implementation.BlockedTaskLedgerItems).To(gomega.HaveLen(1))
			gomega.Expect(record.Implementation.TerminalDisposition).To(gomega.Equal("exact_blocker"))
			gomega.Expect(record.Implementation.VerificationPending).To(gomega.BeTrue())
			gomega.Expect(record.Implementation.CommitReceipts).To(gomega.HaveLen(1))
			gomega.Expect(record.Implementation.CommitReceipts[0].FailedTargets[0].CanonicalID).To(gomega.Equal("pkg/failed.go"))
			gomega.Expect(record.Implementation.CanonicalMutationScope.Presentations[0].PresentedPath).To(gomega.Equal("alias/feature.go"))
		})

		ginkgo.It("rejects unknown and promotion-only contract values", func() {
			base := FailureRecordInput{
				FailureID: "implement-session-contract",
				SourceRun: recoverycontract.SourceRun{
					Lane:      string(recoverycontract.SourceLaneImplement),
					SessionID: "session-contract",
				},
				FailureClass: recoverycontract.FailureClassExecutorEdit,
			}

			_, err := BuildFailureRecord(FailureRecordInput{
				FailureID:    "implement-session-bad-class",
				SourceRun:    base.SourceRun,
				FailureClass: "made_up_class",
			}, recoveryRecordTestTime())
			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("failure class")))

			base.NextAction = "made_up_action"
			_, err = BuildFailureRecord(base, recoveryRecordTestTime())
			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("next action")))

			base.NextAction = recoverycontract.NextAction(recoverycontract.PromotionNextActionFixGate)
			_, err = BuildFailureRecord(base, recoveryRecordTestTime())
			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("next action")))
		})
	})

	ginkgo.Describe("plan-execute failure classification", func() {
		ginkgo.DescribeTable("maps failure signals to recovery classes",
			func(kind, reason, reviewReasonClass string, want recoverycontract.FailureClass) {
				gomega.Expect(ClassifyPlanExecuteFailure(kind, reason, reviewReasonClass)).To(gomega.Equal(want))
			},
			ginkgo.Entry("budget exhaustion", "planner_failed", "completion reached max_tokens before submit_plan", "", recoverycontract.FailureClassBudgetExhausted),
			ginkgo.Entry("missing path authorization", "missing_edit_path_without_new_file_authorization", "plan would create missing edit path without new-file authorization", "", recoverycontract.FailureClassPlanInvalid),
			ginkgo.Entry("repeated invalid submit_plan", "planner_blocked", "<<QUESTION>>: Plan-execute submit correction is blocked because repeated submit_plan attempts did not produce a safe executable edit body or bounded anchor for every remaining required path.", "", recoverycontract.FailureClassPlannerBlocked),
			ginkgo.Entry("terminal contract failure", "terminal_completion_contract_failed", "terminal completion success rejected: changed_path_outside_write_envelope:pkg/feature.go", "", recoverycontract.FailureClassPlanInvalid),
			ginkgo.Entry("review repair scope mismatch", "review_repair_blocked", "<<QUESTION>>: required header changes are outside the allowed repair scope.", "", recoverycontract.FailureClassPlanInvalid),
			ginkgo.Entry("explicit oversized scope", "scope_too_large", "", "", recoverycontract.FailureClassScopeTooLarge),
			ginkgo.Entry("planner reports oversized scope", "planner_failed", "brief lint atom-size violation: scope too large for one atom", "", recoverycontract.FailureClassScopeTooLarge),
			ginkgo.Entry("executor reports oversized scope", "executor_failed", "too many required paths for a bounded implementation", "", recoverycontract.FailureClassScopeTooLarge),
			ginkgo.Entry("review class reports oversized scope", "review_repair_failed", "", "scope_too_large", recoverycontract.FailureClassScopeTooLarge),
			ginkgo.Entry("explicit verification failure", "verification_failed", "", "", recoverycontract.FailureClassVerification),
			ginkgo.Entry("source sanity kind", "source_sanity_failed", "", "", recoverycontract.FailureClassVerification),
			ginkgo.Entry("source sanity reason", "executor_failed", "source_sanity failed for pkg/feature_test.go: go test compile failed", "", recoverycontract.FailureClassVerification),
			ginkgo.Entry("compile reason", "executor_failed", "cmd/idiom: go test compile failed: undefined: missingHelper", "", recoverycontract.FailureClassVerification),
			ginkgo.Entry("required-test reason", "executor_failed", "required tests failed: go test ./internal/workflows/recovery", "", recoverycontract.FailureClassVerification),
			ginkgo.Entry("review verification class", "review_repair_failed", "", "verification_failed", recoverycontract.FailureClassVerification),
			ginkgo.Entry("fallback verification reason", "unknown", "verification command failed: failing package ./cmd/idiom", "", recoverycontract.FailureClassVerification),
		)

		ginkgo.DescribeTable("selects the next action for classified failures",
			func(class recoverycontract.FailureClass, want recoverycontract.NextAction) {
				gomega.Expect(nextActionForFailureClass(class)).To(gomega.Equal(want))
			},
			ginkgo.Entry("invalid plan", recoverycontract.FailureClassPlanInvalid, recoverycontract.NextActionReviewDecomposition),
			ginkgo.Entry("planner blocker", recoverycontract.FailureClassPlannerBlocked, recoverycontract.NextActionAnswerBlocker),
		)

		ginkgo.DescribeTable("covers every plan-execute failure kind",
			func(kind plancontract.PlanExecuteFailureKind, want recoverycontract.FailureClass) {
				gomega.Expect(ClassifyPlanExecuteFailure(string(kind), "", "")).To(gomega.Equal(want))
			},
			ginkgo.Entry(string(plancontract.PlanExecuteFailurePlannerBlocked), plancontract.PlanExecuteFailurePlannerBlocked, recoverycontract.FailureClassPlannerBlocked),
			ginkgo.Entry(string(plancontract.PlanExecuteFailurePlannerFailed), plancontract.PlanExecuteFailurePlannerFailed, recoverycontract.FailureClassPlannerFailed),
			ginkgo.Entry(string(plancontract.PlanExecuteFailurePlanApprovalDenied), plancontract.PlanExecuteFailurePlanApprovalDenied, recoverycontract.FailureClassNeedsHumanAnswer),
			ginkgo.Entry(string(plancontract.PlanExecuteFailurePlanArtifactMissing), plancontract.PlanExecuteFailurePlanArtifactMissing, recoverycontract.FailureClassPlanMissing),
			ginkgo.Entry(string(plancontract.PlanExecuteFailurePlanArtifactInvalid), plancontract.PlanExecuteFailurePlanArtifactInvalid, recoverycontract.FailureClassPlanInvalid),
			ginkgo.Entry(string(plancontract.PlanExecuteFailureSourceSanityFailed), plancontract.PlanExecuteFailureSourceSanityFailed, recoverycontract.FailureClassVerification),
			ginkgo.Entry(string(plancontract.PlanExecuteFailureSourceSatisfiedUnsupportedEvidence), plancontract.PlanExecuteFailureSourceSatisfiedUnsupportedEvidence, recoverycontract.FailureClassPlanInvalid),
			ginkgo.Entry(string(plancontract.PlanExecuteFailureVerificationFailed), plancontract.PlanExecuteFailureVerificationFailed, recoverycontract.FailureClassVerification),
			ginkgo.Entry(string(plancontract.PlanExecuteFailureMissingRequestedTestEdit), plancontract.PlanExecuteFailureMissingRequestedTestEdit, recoverycontract.FailureClassPlanInvalid),
			ginkgo.Entry(string(plancontract.PlanExecuteFailureMissingRequestedImplementationEdit), plancontract.PlanExecuteFailureMissingRequestedImplementationEdit, recoverycontract.FailureClassPlanInvalid),
			ginkgo.Entry(string(plancontract.PlanExecuteFailureMissingEditPathWithoutNewFileAuth), plancontract.PlanExecuteFailureMissingEditPathWithoutNewFileAuth, recoverycontract.FailureClassPlanInvalid),
			ginkgo.Entry(string(plancontract.PlanExecuteFailureTerminalCompletionContractFailed), plancontract.PlanExecuteFailureTerminalCompletionContractFailed, recoverycontract.FailureClassPlanInvalid),
			ginkgo.Entry(string(plancontract.PlanExecuteFailureTerminalCompletionSaveFailed), plancontract.PlanExecuteFailureTerminalCompletionSaveFailed, recoverycontract.FailureClassUnknown),
			ginkgo.Entry(string(plancontract.PlanExecuteFailureExecutorFailed), plancontract.PlanExecuteFailureExecutorFailed, recoverycontract.FailureClassExecutorEdit),
			ginkgo.Entry(string(plancontract.PlanExecuteFailureReviewModelError), plancontract.PlanExecuteFailureReviewModelError, recoverycontract.FailureClassReviewRepair),
			ginkgo.Entry(string(plancontract.PlanExecuteFailureReviewRepairFailed), plancontract.PlanExecuteFailureReviewRepairFailed, recoverycontract.FailureClassReviewRepair),
			ginkgo.Entry(string(plancontract.PlanExecuteFailureReviewRepairBlocked), plancontract.PlanExecuteFailureReviewRepairBlocked, recoverycontract.FailureClassReviewRepair),
			ginkgo.Entry(string(plancontract.PlanExecuteFailureReviewRepairDegraded), plancontract.PlanExecuteFailureReviewRepairDegraded, recoverycontract.FailureClassReviewRepair),
		)
	})

	ginkgo.Describe("partial state scope", func() {
		ginkgo.It("classifies an undeclared dirty path as out of scope", func() {
			partial := PartialStateForDirtyPaths(
				[]string{"unexpected.go"},
				[]string{"allowed.go"},
				[]string{"allowed.go"},
				nil,
				1,
			)
			gomega.Expect(partial.DirtyStatus).To(gomega.Equal(recoverycontract.DirtyStatusDirtyOutScope))
			gomega.Expect(partial.ScopeStatus).To(gomega.Equal(recoverycontract.ScopeStatusOutOfScope))
		})

		ginkgo.It("honors typed candidate path roles but not evidence-like undeclared paths", func() {
			partial := PartialStateForDirtyPaths(
				[]string{"pkg/optional_test.go"},
				[]string{"pkg/feature.go"},
				[]string{"pkg/feature.go"},
				[]string{"pkg/optional_test.go"},
				1,
			)
			gomega.Expect(partial.DirtyStatus).To(gomega.Equal(recoverycontract.DirtyStatusDirtyInScope))
			gomega.Expect(partial.ScopeStatus).To(gomega.Equal(recoverycontract.ScopeStatusInScope))
			gomega.Expect(scopeStatus([]string{"pkg/optional_test.go"}, []string{"pkg/feature.go"}, []string{"pkg/optional_test.go"})).To(gomega.Equal(recoverycontract.ScopeStatusInScope))
			gomega.Expect(scopeStatus([]string{"pkg/evidence.md"}, []string{"pkg/feature.go"}, nil)).To(gomega.Equal(recoverycontract.ScopeStatusOutOfScope))
		})

		ginkgo.DescribeTable("canonicalizes path separators before checking authority",
			func(dirtyPath string, want recoverycontract.ScopeStatus) {
				gomega.Expect(scopeStatus([]string{dirtyPath}, []string{"pkg/feature.go"}, []string{"pkg/optional_test.go"})).To(gomega.Equal(want))
			},
			ginkgo.Entry("authorized Windows-style path", `pkg\optional_test.go`, recoverycontract.ScopeStatusInScope),
			ginkgo.Entry("undeclared Windows-style path", `pkg\surprise.go`, recoverycontract.ScopeStatusOutOfScope),
		)
	})
})

func expectPlanExecuteFailureRecord(input PlanExecuteFailureInput) recoverycontract.FailureRecord {
	record, err := BuildPlanExecuteFailureRecord(input, recoveryRecordTestTime())
	gomega.ExpectWithOffset(1, err).NotTo(gomega.HaveOccurred())
	return record
}

func expectFailureRecord(input FailureRecordInput) recoverycontract.FailureRecord {
	record, err := BuildFailureRecord(input, recoveryRecordTestTime())
	gomega.ExpectWithOffset(1, err).NotTo(gomega.HaveOccurred())
	return record
}

func recoveryRecordTestTime() time.Time {
	return time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
}
