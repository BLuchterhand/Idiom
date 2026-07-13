package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	plancontract "idiom/internal/contracts/plan"
	planartifact "idiom/internal/contracts/planartifact"
	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.Describe("Runner durable mutation state", func() {
	ginkgo.It("retains prerequisite intent only after bound-scope mutation admission", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "internal"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "internal", "target.go"), []byte("package internal\n"), 0o600)).To(gomega.Succeed())
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		run := newRunnerRunFixture(&Runner{
			Workspace: manager,
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "implementation",
				Contract:          "implementation_result",
				RequiredEditPaths: []string{"internal/target.go"},
			},
		})
		gomega.Expect(run.state.initializationErr).NotTo(gomega.HaveOccurred())

		allowed := workspace.ToolRequest{
			Tool: workspace.ToolNameEditFile, Path: "internal/target.go",
			OldText: "package internal", NewText: "package internal // updated", Description: "update package declaration",
		}
		_, deferred := run.implementationWritePrerequisiteDeferralResult(allowed, nil)
		gomega.Expect(deferred).To(gomega.BeTrue())

		outside := allowed
		outside.Path = "internal/outside.go"
		_, deferred = run.implementationWritePrerequisiteDeferralResult(outside, nil)
		gomega.Expect(deferred).To(gomega.BeFalse())
	})

	ginkgo.It("binds aliases once and records a partial-error receipt before completion observes it", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "real"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "real", "target.txt"), []byte("one\ntwo\nthree\n"), 0o600)).To(gomega.Succeed())
		if err := os.Symlink("real", filepath.Join(root, "alias")); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		run := newRunnerRunFixture(&Runner{
			Workspace: manager,
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily: "implementation", Contract: "implementation_result", PermissionProfile: "guarded_workspace_write",
				RequiredEditPaths: []string{"alias/target.txt"},
			},
		})
		gomega.Expect(run.state.initializationErr).NotTo(gomega.HaveOccurred())
		gomega.Expect(run.state.mutation.canonicalScopeProjection.RequiredCoverageIDs).To(gomega.Equal([]string{"real/target.txt"}))
		readTarget := manager.Execute(run.state.ctx, workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: "alias/target.txt"})
		gomega.Expect(readTarget.Error).To(gomega.BeEmpty())

		patch := strings.Join([]string{
			"*** Begin Patch",
			"*** Update File: alias/target.txt", "@@", "-one", "+ONE",
			"*** Update File: real/target.txt", "@@", "-two", "+TWO",
			"*** Update File: alias/target.txt", "@@", "-three", "+THREE",
			"*** End Patch",
		}, "\n")
		request := workspace.ToolRequest{Tool: workspace.ToolNameApplyPatch, Content: patch}
		partial := manager.Execute(run.state.ctx, request)
		gomega.Expect(partial.Error).NotTo(gomega.BeEmpty())
		gomega.Expect(workspaceWriteResultCommitted(request, partial)).To(gomega.BeTrue())
		gomega.Expect(toolResultMadeProgress(request, partial)).To(gomega.BeTrue(), "partial Error still carries a committed workspace effect")
		recorder := runnerTurnRecorder{runner: run}
		recorder.record(1, request, partial, "tool_result", runnerRecordOptions{})

		gomega.Expect(run.state.mutation.commitReceipts).To(gomega.HaveLen(1))
		gomega.Expect(run.state.committedMutationEffectIDs()).To(gomega.Equal([]string{"real/target.txt"}))
		gomega.Expect(run.state.missingRequiredMutationCoverageIDs()).To(gomega.BeEmpty())
		gomega.Expect(run.state.unresolvedMutationTargetIDs()).To(gomega.Equal([]string{"real/target.txt"}), "same-receipt failure must dominate its partial effect")

		repairRequest := workspace.ToolRequest{Tool: workspace.ToolNameWriteFile, Path: "alias/target.txt", Content: "ONE\nTWO\nTHREE\n"}
		repair := manager.Execute(run.state.ctx, repairRequest)
		gomega.Expect(repair.Error).To(gomega.BeEmpty())
		recorder.record(2, repairRequest, repair, "tool_result", runnerRecordOptions{})
		gomega.Expect(run.state.unresolvedMutationTargetIDs()).To(gomega.BeEmpty(), "later clean receipt must resolve prior pending target")
		gomega.Expect(run.unsupportedClaimedChangedPaths("Changed paths:\n- alias/target.txt", run.state.collectedResults)).To(gomega.BeEmpty(), "captured presentation aliases must map to the committed canonical effect")
	})

	ginkgo.It("uses receipt effects, not ChangedPaths, for progress and final evidence", func() {
		request := workspace.ToolRequest{Tool: workspace.ToolNameApplyPatch}
		spoof := workspace.ToolResult{
			Tool: workspace.ToolNameApplyPatch, ChangedPaths: []string{"pkg/spoof.go"}, Summary: "claimed write",
		}
		gomega.Expect(workspaceWriteResultCommitted(request, spoof)).To(gomega.BeFalse())
		gomega.Expect(toolResultMadeProgress(request, spoof)).To(gomega.BeFalse())

		recovery := completionGateRecoveryState{ActiveViolation: completionGateActiveViolation{
			Class: completionGateViolationMutation, PendingContractPaths: []string{"pkg/spoof.go"}, AttemptsSinceProgress: 1,
		}}
		gomega.Expect(recovery.ObserveEvidenceProgress(request, spoof, nil).MadeProgress()).To(gomega.BeFalse())
		gomega.Expect(recovery.ActiveViolation.PendingContractPaths).To(gomega.Equal([]string{"pkg/spoof.go"}))
		gomega.Expect(recovery.ActiveViolation.AttemptsSinceProgress).To(gomega.Equal(1))

		run := newRunnerRunFixture(&Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "implementation", Contract: "implementation_result", PermissionProfile: "guarded_workspace_write",
		}})
		projection := run.finalEvidenceProjection("Changed paths:\n- pkg/spoof.go", []workspace.ToolResult{spoof})
		gomega.Expect(projection.ChangedPaths).To(gomega.BeEmpty())
		gomega.Expect(projection.ObservedPaths).NotTo(gomega.ContainElement("pkg/spoof.go"))
		run.state.runtimeBrief.Objective = "Did you add a test to cover this?"
		gomega.Expect(run.validationStatusAnswerRejectionReason("I added a test in pkg/spoof_test.go.", []workspace.ToolResult{spoof})).To(
			gomega.ContainSubstring("no successful write to a test path"),
		)
	})

	ginkgo.It("contracts only cleanly committed partial targets and leaves failed targets pending", func() {
		projection := workspace.MutationCommitProjection{
			Effects:          []workspace.MutationCommitEffectProjection{{CanonicalID: "pkg/a.go", Kind: workspace.MutationCommitReplacedFile, Role: workspace.MutationTargetFile}},
			PostimageFileIDs: []string{"pkg/a.go"}, AffectedFileIDs: []string{"pkg/a.go"},
			FailedTargets: []workspace.MutationTargetProjection{{CanonicalID: "pkg/b.go", Role: workspace.MutationTargetFile, PreimageClass: workspace.MutationPreimageRequired}},
		}
		recovery := completionGateRecoveryState{ActiveViolation: completionGateActiveViolation{
			Class: completionGateViolationMutation, PendingContractPaths: []string{"pkg/a.go", "pkg/b.go"}, AttemptsSinceProgress: 1,
		}}
		progress := recovery.observeMutationProgressIDs(committedMutationEffectIDsForProjection(projection))
		gomega.Expect(progress.SatisfiedContractPaths).To(gomega.Equal([]string{"pkg/a.go"}))
		gomega.Expect(progress.PendingContractPaths).To(gomega.Equal([]string{"pkg/b.go"}))
		gomega.Expect(recovery.ActiveViolation.PendingContractPaths).To(gomega.Equal([]string{"pkg/b.go"}))
		gomega.Expect(recovery.ActiveViolation.AttemptsSinceProgress).To(gomega.BeZero())
		gomega.Expect(changedPathsRequiringGitHistoryForReceipts(
			[]workspace.MutationCommitProjection{projection},
			nil,
		)).To(gomega.Equal([]string{"pkg/a.go"}), "partial Error must not erase committed history obligations")
	})

	ginkgo.It("does not turn a post-write source window for a new file into a history obligation", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "existing.go"), []byte("package sample\n"), 0o600)).To(gomega.Succeed())
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})

		newWrite := manager.Execute(context.Background(), workspace.ToolRequest{
			Tool: workspace.ToolNameWriteFile, Path: "new.go", Content: "package sample\n", Description: "create source file",
		})
		gomega.Expect(newWrite.Error).To(gomega.BeEmpty())
		newRead := manager.Execute(context.Background(), workspace.ToolRequest{
			Tool: workspace.ToolNameReadFile, Path: "new.go", StartLine: 1, EndLine: 1,
		})
		gomega.Expect(newRead.Error).To(gomega.BeEmpty())
		gomega.Expect(changedPathsRequiringGitHistory([]workspace.ToolResult{newWrite, newRead})).To(gomega.BeEmpty())

		existingWrite := manager.Execute(context.Background(), workspace.ToolRequest{
			Tool: workspace.ToolNameWriteFile, Path: "existing.go", Content: "package sample\n\nfunc Existing() {}\n", Description: "update existing source file",
		})
		gomega.Expect(existingWrite.Error).To(gomega.BeEmpty())
		gomega.Expect(changedPathsRequiringGitHistory([]workspace.ToolResult{existingWrite})).To(gomega.Equal([]string{"existing.go"}))
	})

	ginkgo.It("treats only the latest committed removal as an already-removed target", func() {
		state := &runnerRunState{
			mutation: runnerMutationState{
				commitReceipts: []workspace.MutationCommitProjection{{
					Effects: []workspace.MutationCommitEffectProjection{{
						CanonicalID: "temp.md", Kind: workspace.MutationCommitRemovedFile, Role: workspace.MutationTargetSource,
					}},
				}},
			},
		}
		gomega.Expect(state.mutationTargetsAlreadyRemoved([]string{"temp.md"})).To(gomega.BeTrue())
		gomega.Expect(state.mutationTargetsAlreadyRemoved([]string{"temp.md", "other.md"})).To(gomega.BeFalse())

		state.mutation.commitReceipts = append(state.mutation.commitReceipts, workspace.MutationCommitProjection{
			Effects: []workspace.MutationCommitEffectProjection{{
				CanonicalID: "temp.md", Kind: workspace.MutationCommitReplacedFile, Role: workspace.MutationTargetFile,
			}},
		})
		gomega.Expect(state.mutationTargetsAlreadyRemoved([]string{"temp.md"})).To(gomega.BeFalse(), "a later recreation must supersede the earlier removal")
	})

	ginkgo.It("requires project verification for removed files and detects whole-file test deletion", func() {
		root := ginkgo.GinkgoT().TempDir()
		target := "pkg/removed_test.go"
		gomega.Expect(os.MkdirAll(filepath.Join(root, "pkg"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, target), []byte("package pkg\n\nfunc TestRemoved(t *testing.T) {}\n"), 0o600)).To(gomega.Succeed())
		initRunnerTestGitRepo(ginkgo.GinkgoT(), root)
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		brief := workerruntime.WorkerRuntimeBrief{TaskFamily: "implementation", Contract: "implementation_result", PermissionProfile: "guarded_workspace_write", VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker", AllowedEditPaths: []string{target}}
		run := newRunnerRunFixture(&Runner{Workspace: manager, RuntimeBrief: brief})
		request := workspace.ToolRequest{Tool: workspace.ToolNameApplyPatch, Content: strings.Join([]string{
			"*** Begin Patch", "*** Delete File: " + target, "*** End Patch",
		}, "\n")}
		removed := manager.Execute(run.state.ctx, request)
		gomega.Expect(removed.Error).To(gomega.BeEmpty())
		receipt := removed.CommitReceipt().DurableProjection()
		gomega.Expect(receipt.PostimageFileIDs).To(gomega.BeEmpty())
		gomega.Expect(receipt.AffectedFileIDs).To(gomega.Equal([]string{target}))

		ledger := postWriteVerificationLedgerForReceipts([]workspace.ToolResult{removed}, brief, []workspace.MutationCommitProjection{receipt})
		gomega.Expect(ledger.PendingPostimagePaths).To(gomega.BeEmpty())
		gomega.Expect(ledger.PendingProjectPaths).To(gomega.Equal([]string{target}))

		diagnostics := workspace.ToolResult{Tool: workspace.ToolNameSourceSanity, Summary: "diagnostics passed", Facts: &toolfacts.ToolResultFacts{DiagnosticEvidencePaths: []string{target}}}
		ledger = postWriteVerificationLedgerForReceipts([]workspace.ToolResult{removed, diagnostics}, brief, []workspace.MutationCommitProjection{receipt})
		gomega.Expect(ledger.PendingProjectPaths).To(gomega.Equal([]string{target}), "per-file diagnostics cannot verify a removed file")
		patchHygiene := shellCommandResultForTest("git diff --check -- "+target, "", "")
		patchHygiene.Facts.VerificationEvidence = []toolfacts.VerificationEvidence{{
			Kind: toolfacts.VerificationEvidenceGitDiffCheck, Paths: []string{target},
		}}
		ledger = postWriteVerificationLedgerForReceipts([]workspace.ToolResult{removed, patchHygiene}, brief, []workspace.MutationCommitProjection{receipt})
		gomega.Expect(ledger.PendingProjectPaths).To(gomega.Equal([]string{target}), "patch hygiene cannot verify the package after a removed file")
		projectCheck := shellCommandResultForTest("go test ./...", "ok", "")
		ledger = postWriteVerificationLedgerForReceipts([]workspace.ToolResult{removed, projectCheck}, brief, []workspace.MutationCommitProjection{receipt})
		gomega.Expect(ledger.Pending()).To(gomega.BeFalse())

		run.state.mutation.commitReceipts = []workspace.MutationCommitProjection{receipt}
		gomega.Expect(run.removedTestDeclarations([]workspace.ToolResult{removed})).To(gomega.ContainElement("func TestRemoved"))
		gomega.Expect(run.changedTestPaths([]workspace.ToolResult{removed})).To(gomega.Equal([]string{target}), "removed tests remain typed affected-file evidence")
	})

	ginkgo.It("accepts exact unbound canonical claims and rejects unsupported siblings", func() {
		run := newRunnerRunFixture(&Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "implementation", Contract: "implementation_result", PermissionProfile: "guarded_workspace_write",
		}})
		run.state.mutation.commitReceipts = []workspace.MutationCommitProjection{{
			Effects:          []workspace.MutationCommitEffectProjection{{CanonicalID: "pkg/actual.go", Kind: workspace.MutationCommitReplacedFile, Role: workspace.MutationTargetFile}},
			PostimageFileIDs: []string{"pkg/actual.go"}, AffectedFileIDs: []string{"pkg/actual.go"},
		}}
		gomega.Expect(run.unsupportedClaimedChangedPaths("Changed paths:\n- pkg/actual.go", nil)).To(gomega.BeEmpty())
		gomega.Expect(run.unsupportedClaimedChangedPaths("Changed paths:\n- pkg/sibling.go", nil)).To(gomega.Equal([]string{"pkg/sibling.go"}))
		projection := run.finalEvidenceProjection("Changed paths:\n- pkg/actual.go", nil)
		gomega.Expect(projection.ChangedPaths).To(gomega.Equal([]string{"pkg/actual.go"}))
		gomega.Expect(projection.claimedPathsCover(projection.ChangedPaths)).To(gomega.BeTrue())
		run.state.runtimeBrief.VerificationContract = "tests_passed_or_failed_or_skipped_with_blocker"
		acceptance := run.evaluateFinalAnswerAcceptance("Changed paths:\n- pkg/actual.go\n\nVerification: not run.", nil)
		gomega.Expect(acceptance.Decision).To(gomega.Equal(finalAnswerAcceptanceRejected), "resumed receipt effects must keep verification pending without ToolResult ChangedPaths")
		gomega.Expect(acceptance.BlockerKind).To(gomega.Equal(string(turnViolationRequiredEvidenceUnmet)))
		rejection, rejected := run.mutationCompletionRejection("Changed paths:\n- pkg/actual.go\n\nVerification: not run.", nil)
		gomega.Expect(rejected).To(gomega.BeTrue())
		envelope, typed := completionGateRecoveryEnvelopeFromResult(rejection)
		gomega.Expect(typed).To(gomega.BeTrue())
		gomega.Expect(envelope.InvariantID).To(gomega.Equal(completionGateInvariantPostWriteVerification))
		gomega.Expect(envelope.PendingPaths).To(gomega.Equal([]string{"pkg/actual.go"}), "recovery must project the same receipt-owned paths that made verification pending")
		prompt, ok := run.completionGateRecoveryPrompt(nil)
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(prompt).To(gomega.ContainSubstring("pkg/actual.go"), "receipt-only recovery must render the same canonical pending path")
		gomega.Expect(prompt).NotTo(gomega.ContainSubstring("unresolved for: changed source files"))
	})

	ginkgo.It("validates submit-plan directory children through the bound presentation mapping", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "real"), 0o755)).To(gomega.Succeed())
		if err := os.Symlink("real", filepath.Join(root, "alias")); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		run := newRunnerRunFixture(&Runner{
			Workspace: manager,
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily: PlanThenExecutePlannerTaskFamily, RequiredEditPaths: []string{"alias"},
			},
		})
		gomega.Expect(run.state.initializationErr).NotTo(gomega.HaveOccurred())

		guard := run.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
			Tool: workspace.ToolNameSubmitPlan,
			Edits: []planartifact.PlanEdit{{
				Path: "alias/child.go", ReplaceAll: true, NewText: "package real\n",
			}},
		})
		gomega.Expect(guard.Intercepted).To(gomega.BeFalse())
	})

	ginkgo.It("keeps same-receipt failures pending and lets only a later effect resolve them", func() {
		state := runnerRunState{mutation: runnerMutationState{commitReceipts: []workspace.MutationCommitProjection{{
			Effects:       []workspace.MutationCommitEffectProjection{{CanonicalID: "pkg/a.go"}},
			FailedTargets: []workspace.MutationTargetProjection{{CanonicalID: "pkg/a.go"}},
		}}}}
		gomega.Expect(state.unresolvedMutationTargetIDs()).To(gomega.Equal([]string{"pkg/a.go"}))

		state.mutation.commitReceipts = append(state.mutation.commitReceipts, workspace.MutationCommitProjection{
			Effects: []workspace.MutationCommitEffectProjection{{CanonicalID: "pkg/a.go"}},
		})
		gomega.Expect(state.unresolvedMutationTargetIDs()).To(gomega.BeEmpty())
	})

	ginkgo.It("restores deferred rejected intent without a receipt and resolves it only after a fresh commit", func() {
		state := runnerRunState{}
		recovered := &recoverycontract.ImplementationState{
			UnresolvedRejectedMutationPaths: []string{"pkg/deferred.go"},
		}
		gomega.Expect(state.restoreDurableMutationState(nil, recovered)).To(gomega.Succeed())
		gomega.Expect(state.unresolvedMutationTargetIDs()).To(gomega.Equal([]string{"pkg/deferred.go"}))

		run := &runExecution{runner: &Runner{}, state: state}
		gomega.Expect(run.implementationCompletionState(nil).UnresolvedRejectedMutationPaths).
			To(gomega.Equal([]string{"pkg/deferred.go"}), "resumed lifecycle projection must retain typed deferred intent")

		state.mutation.commitReceipts = append(state.mutation.commitReceipts, workspace.MutationCommitProjection{
			Effects: []workspace.MutationCommitEffectProjection{{CanonicalID: "pkg/deferred.go"}},
		})
		gomega.Expect(state.unresolvedMutationTargetIDs()).To(gomega.BeEmpty(), "only a later clean receipt resolves restored intent")
	})

	ginkgo.It("preserves workspace durable projections exactly across the recovery translation boundary", func() {
		scope := workspace.CanonicalMutationScopeProjection{
			RequiredCoverageIDs: []string{"real/a.go"},
			Rules: []workspace.CanonicalMutationScopeRuleProjection{{
				Kind: workspace.MutationScopeExactFile, Source: workspace.MutationScopeRequiredPath, CanonicalID: "real/a.go",
			}},
			Presentations: []workspace.CanonicalMutationScopePresentationProjection{{
				PresentedPath: "alias/a.go", CanonicalID: "real/a.go", Source: workspace.MutationScopeRequiredPath,
			}},
		}
		receipt := workspace.MutationCommitProjection{
			Scope: &scope,
			Effects: []workspace.MutationCommitEffectProjection{{
				CanonicalID: "real/a.go", Kind: workspace.MutationCommitReplacedFile, Role: workspace.MutationTargetFile,
			}},
			PostimageFileIDs: []string{"real/a.go"},
			AffectedFileIDs:  []string{"real/a.go"},
			FailedTargets: []workspace.MutationTargetProjection{{
				CanonicalID: "real/b.go", Role: workspace.MutationTargetDestination, PreimageClass: workspace.MutationPreimageAbsent,
			}},
			UnattemptedTargets: []workspace.MutationTargetProjection{{
				CanonicalID: "real/c.go", Role: workspace.MutationTargetDestination, PreimageClass: workspace.MutationPreimageAbsent,
			}},
		}

		workspaceScopeJSON, err := json.Marshal(scope)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		recoveryScopeJSON, err := json.Marshal(recoveryCanonicalMutationScopeProjection(scope))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(recoveryScopeJSON).To(gomega.MatchJSON(workspaceScopeJSON))

		workspaceReceiptJSON, err := json.Marshal(receipt)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		recoveryReceipt := recoveryMutationCommitProjection(receipt)
		recoveryReceiptJSON, err := json.Marshal(recoveryReceipt)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(recoveryReceiptJSON).To(gomega.MatchJSON(workspaceReceiptJSON))
		gomega.Expect(workspaceMutationCommitProjection(recoveryReceipt)).To(gomega.Equal(receipt))
	})

	ginkgo.It("ignores ChangedPaths spoofing for scoped and unscoped completion", func() {
		spoof := []workspace.ToolResult{{
			Tool: workspace.ToolNameWriteFile, ChangedPaths: []string{"pkg/spoof.go"}, Summary: "claimed write",
		}}
		unscoped := newRunnerRunFixture(&Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "implementation", Contract: "implementation_result", PermissionProfile: "guarded_workspace_write",
		}})
		gomega.Expect(unscoped.implementationCompletionState(spoof).ChangedPaths).To(gomega.BeEmpty())

		scopedState := runnerRunState{mutation: runnerMutationState{
			bound: true,
			canonicalScopeProjection: workspace.CanonicalMutationScopeProjection{
				RequiredCoverageIDs: []string{"pkg/required.go"},
			},
		}}
		scoped := &runExecution{runner: &Runner{}, state: scopedState}
		completion := scoped.implementationCompletionState(spoof)
		gomega.Expect(completion.ChangedPaths).To(gomega.BeEmpty())
		gomega.Expect(completion.MissingRequiredPaths).To(gomega.Equal([]string{"pkg/required.go"}))
	})

	ginkgo.It("classifies invalid and mismatched recovered state with typed errors", func() {
		state := runnerRunState{mutation: runnerMutationState{
			bound: true,
			canonicalScopeProjection: workspace.CanonicalMutationScopeProjection{
				RequiredCoverageIDs: []string{"pkg/a.go"},
			},
		}}
		scope := recoveryCanonicalMutationScopeProjection(state.mutation.canonicalScopeProjection)
		invalid := &recoverycontract.ImplementationState{
			CanonicalMutationScope: &scope,
			CommitReceipts: []recoverycontract.MutationCommitProjection{{
				Scope: &scope,
				Effects: []recoverycontract.MutationCommitEffectProjection{{
					CanonicalID: "pkg/a.go", Kind: "forged", Role: "file",
				}},
			}},
		}
		gomega.Expect(IsRecoveredMutationStateInvalid(state.restoreDurableMutationState(nil, invalid))).To(gomega.BeTrue())

		mismatch := &recoverycontract.ImplementationState{
			CanonicalMutationScope: &recoverycontract.CanonicalMutationScopeProjection{RequiredCoverageIDs: []string{"pkg/b.go"}},
		}
		gomega.Expect(IsRecoveredMutationStateMismatch(state.restoreDurableMutationState(nil, mismatch))).To(gomega.BeTrue())

		unbound := runnerRunState{}
		forgedScope := &recoverycontract.ImplementationState{
			CanonicalMutationScope: &recoverycontract.CanonicalMutationScopeProjection{},
			CommitReceipts: []recoverycontract.MutationCommitProjection{{
				Scope: &recoverycontract.CanonicalMutationScopeProjection{},
			}},
		}
		gomega.Expect(IsRecoveredMutationStateMismatch(unbound.restoreDurableMutationState(nil, forgedScope))).To(gomega.BeTrue())
	})

	ginkgo.It("restores a valid receipt and rejects a forged out-of-scope effect through workspace authority", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(filepath.Join(root, "a.go"), []byte("package sample\n"), 0o600)).To(gomega.Succeed())
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		ctx, canonicalScope, err := manager.BindMutationScope(context.Background(), workspace.MutationScopePaths{
			RequiredPaths: []string{"a.go"},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		workspaceScope := canonicalScope.DurableProjection()
		recoveryScope := recoveryCanonicalMutationScopeProjection(workspaceScope)
		validReceipt := workspace.MutationCommitProjection{
			Scope: &workspaceScope,
			Effects: []workspace.MutationCommitEffectProjection{{
				CanonicalID: "a.go", Kind: workspace.MutationCommitReplacedFile, Role: workspace.MutationTargetFile,
			}},
			PostimageFileIDs: []string{"a.go"},
			AffectedFileIDs:  []string{"a.go"},
		}
		state := runnerRunState{ctx: ctx, mutation: runnerMutationState{
			bound: true, canonicalScope: canonicalScope, canonicalScopeProjection: workspaceScope,
		}}
		recovered := &recoverycontract.ImplementationState{
			CanonicalMutationScope: &recoveryScope,
			CommitReceipts:         []recoverycontract.MutationCommitProjection{recoveryMutationCommitProjection(validReceipt)},
		}
		gomega.Expect(state.restoreDurableMutationState(manager, recovered)).To(gomega.Succeed())
		restoredJSON, err := json.Marshal(state.mutation.commitReceipts[0])
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		validJSON, err := json.Marshal(validReceipt)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(restoredJSON).To(gomega.MatchJSON(validJSON))

		forged := validReceipt
		forged.Effects = []workspace.MutationCommitEffectProjection{{
			CanonicalID: "outside.go", Kind: workspace.MutationCommitReplacedFile, Role: workspace.MutationTargetFile,
		}}
		forged.PostimageFileIDs = []string{"outside.go"}
		forged.AffectedFileIDs = []string{"outside.go"}
		recovered.CommitReceipts = []recoverycontract.MutationCommitProjection{recoveryMutationCommitProjection(forged)}
		gomega.Expect(IsRecoveredMutationStateInvalid(state.restoreDurableMutationState(manager, recovered))).To(gomega.BeTrue())
	})

	ginkgo.DescribeTable("validates required snippets from completion-only workspace snapshots",
		func(body string, expectedMissing []string) {
			root := ginkgo.GinkgoT().TempDir()
			gomega.Expect(os.WriteFile(filepath.Join(root, "target.txt"), []byte("before\n"), 0o600)).To(gomega.Succeed())
			manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
			var events []RunEvent
			brief := workerruntime.WorkerRuntimeBrief{
				TaskFamily: "implementation", Contract: "implementation_result", PermissionProfile: "guarded_workspace_write",
				RequiredEditPaths: []string{"target.txt"},
				RequiredEditSnippets: []plancontract.RequiredEditSnippet{{
					ID: "required-marker", Path: "target.txt", Content: "required marker",
				}},
			}
			run := newRunnerRunFixture(&Runner{
				Workspace: manager, RuntimeBrief: brief,
				OnEvent: func(event RunEvent) { events = append(events, event) },
			})
			gomega.Expect(run.state.initializationErr).NotTo(gomega.HaveOccurred())
			request := workspace.ToolRequest{Tool: workspace.ToolNameWriteFile, Path: "target.txt", Content: body}
			result := manager.Execute(run.state.ctx, request)
			gomega.Expect(result.Error).To(gomega.BeEmpty())
			(&runnerTurnRecorder{runner: run}).record(1, request, result, "tool_result", runnerRecordOptions{})

			completion := run.implementationCompletionState(run.state.collectedResults)
			gomega.Expect(completion.MissingRequiredSnippetIDs).To(gomega.Equal(expectedMissing))
			auditEvents := 0
			for _, event := range events {
				if event.Role == "workspace_snapshot_audit" {
					auditEvents++
				}
				gomega.Expect(event.Content).NotTo(gomega.ContainSubstring("required marker"), "snapshot audit must never retain the inspected body")
			}
			gomega.Expect(auditEvents).To(gomega.BeNumerically(">", 0), "completion validation must use the audited workspace snapshot boundary")
		},
		ginkgo.Entry("recognizes a committed postimage containing the snippet", "before\nrequired marker\nafter\n", []string(nil)),
		ginkgo.Entry("keeps a committed postimage missing the snippet open", "before\nafter\n", []string{"required-marker"}),
	)

	ginkgo.It("validates a snippet in a postimage committed before a partial workspace error", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "real"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "real", "target.txt"), []byte("one\ntwo\nthree\n"), 0o600)).To(gomega.Succeed())
		if err := os.Symlink("real", filepath.Join(root, "alias")); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		run := newRunnerRunFixture(&Runner{Workspace: manager, RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "implementation", Contract: "implementation_result", PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"alias/target.txt"},
			RequiredEditSnippets: []plancontract.RequiredEditSnippet{{
				ID: "partial-marker", Path: "alias/target.txt", Content: "ONE",
			}},
		}})
		gomega.Expect(run.state.initializationErr).NotTo(gomega.HaveOccurred())
		patch := strings.Join([]string{
			"*** Begin Patch",
			"*** Update File: alias/target.txt", "@@", "-one", "+ONE",
			"*** Update File: real/target.txt", "@@", "-two", "+TWO",
			"*** Update File: alias/target.txt", "@@", "-three", "+THREE",
			"*** End Patch",
		}, "\n")
		request := workspace.ToolRequest{Tool: workspace.ToolNameApplyPatch, Content: patch}
		result := manager.Execute(run.state.ctx, request)
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty())
		gomega.Expect(result.CommitReceipt()).NotTo(gomega.BeNil())
		(&runnerTurnRecorder{runner: run}).record(1, request, result, "tool_result", runnerRecordOptions{})

		completion := run.implementationCompletionState(run.state.collectedResults)
		gomega.Expect(completion.MissingRequiredSnippetIDs).To(gomega.BeEmpty())
		gomega.Expect(completion.UnresolvedRejectedMutationPaths).To(gomega.ContainElement("real/target.txt"))
	})

	ginkgo.It("fails closed when completion-only snippet inspection lacks required read approval", func() {
		root := ginkgo.GinkgoT().TempDir()
		target := "protected.txt"
		gomega.Expect(os.WriteFile(filepath.Join(root, target), []byte("before\n"), 0o600)).To(gomega.Succeed())
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		manager.SetPermissionRules(map[string][]toolpolicy.Rule{
			workspace.ToolNameReadFile: {{Pattern: target, Decision: toolpolicy.Ask}},
		})
		var events []RunEvent
		run := newRunnerRunFixture(&Runner{
			Workspace: manager,
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily: "implementation", Contract: "implementation_result", PermissionProfile: "guarded_workspace_write",
				RequiredEditPaths: []string{target},
				RequiredEditSnippets: []plancontract.RequiredEditSnippet{{
					ID: "protected-marker", Path: target, Content: "required marker",
				}},
			},
			OnEvent: func(event RunEvent) { events = append(events, event) },
		})
		request := workspace.ToolRequest{Tool: workspace.ToolNameWriteFile, Path: target, Content: "required marker\n"}
		result := manager.Execute(run.state.ctx, request)
		gomega.Expect(result.Error).To(gomega.BeEmpty())
		(&runnerTurnRecorder{runner: run}).record(1, request, result, "tool_result", runnerRecordOptions{})

		gomega.Expect(run.implementationCompletionState(run.state.collectedResults).MissingRequiredSnippetIDs).To(gomega.Equal([]string{"protected-marker"}))
		var audit string
		for _, event := range events {
			if event.Role == "workspace_snapshot_audit" {
				audit += event.Content
			}
		}
		gomega.Expect(audit).To(gomega.ContainSubstring(string(workspace.WorkspaceSnapshotReasonApprovalAbsent)))
		gomega.Expect(audit).NotTo(gomega.ContainSubstring("required marker"))
	})

	ginkgo.It("revalidates required snippets against the current workspace after resume", func() {
		root := ginkgo.GinkgoT().TempDir()
		target := "target.txt"
		gomega.Expect(os.WriteFile(filepath.Join(root, target), []byte("before\n"), 0o600)).To(gomega.Succeed())
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		brief := workerruntime.WorkerRuntimeBrief{
			TaskFamily: "implementation", Contract: "implementation_result", PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{target},
			RequiredEditSnippets: []plancontract.RequiredEditSnippet{{
				ID: "resume-marker", Path: target, Content: "required marker",
			}},
		}
		first := newRunnerRunFixture(&Runner{Workspace: manager, RuntimeBrief: brief})
		request := workspace.ToolRequest{Tool: workspace.ToolNameWriteFile, Path: target, Content: "required marker\n"}
		result := manager.Execute(first.state.ctx, request)
		gomega.Expect(result.Error).To(gomega.BeEmpty())
		(&runnerTurnRecorder{runner: first}).record(1, request, result, "tool_result", runnerRecordOptions{})
		scope, receipts := first.state.durableMutationState()
		seed := recoverycontract.ImplementationState{CanonicalMutationScope: scope, CommitReceipts: receipts}

		resumed := newRunnerRunFixture(&Runner{
			Workspace: manager, RuntimeBrief: brief, SeedImplementationState: &seed,
		})
		gomega.Expect(resumed.state.initializationErr).NotTo(gomega.HaveOccurred())
		gomega.Expect(resumed.implementationCompletionState(nil).MissingRequiredSnippetIDs).To(gomega.BeEmpty())

		gomega.Expect(os.WriteFile(filepath.Join(root, target), []byte("marker removed after receipt\n"), 0o600)).To(gomega.Succeed())
		gomega.Expect(resumed.implementationCompletionState(nil).MissingRequiredSnippetIDs).To(gomega.Equal([]string{"resume-marker"}),
			"completion must re-read current rooted state instead of trusting the resumed receipt alone")
	})

	ginkgo.It("carries receipt-only semantic verification paths through recovery progress", func() {
		root := ginkgo.GinkgoT().TempDir()
		target := "pkg/target.go"
		gomega.Expect(os.MkdirAll(filepath.Join(root, "pkg"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, target), []byte("package pkg\n"), 0o600)).To(gomega.Succeed())
		manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
		brief := workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{target},
			VerificationPlan:  []string{"go test ./..."},
		}

		first := newRunnerRunFixture(&Runner{Workspace: manager, RuntimeBrief: brief})
		request := workspace.ToolRequest{
			Tool: workspace.ToolNameWriteFile, Path: target,
			Content: "package pkg\n\nfunc Updated() {}\n", Description: "add updated implementation",
		}
		written := manager.Execute(first.state.ctx, request)
		gomega.Expect(written.Error).To(gomega.BeEmpty())
		(&runnerTurnRecorder{runner: first}).record(1, request, written, "tool_result", runnerRecordOptions{})
		scope, receipts := first.state.durableMutationState()

		resumed := newRunnerRunFixture(&Runner{
			Workspace: manager, RuntimeBrief: brief,
			SeedImplementationState: &recoverycontract.ImplementationState{
				CanonicalMutationScope: scope,
				CommitReceipts:         receipts,
			},
		})
		gomega.Expect(resumed.state.initializationErr).NotTo(gomega.HaveOccurred())

		patchHygiene := shellCommandResultForTest("git diff --check -- "+target, "", "")
		patchHygiene.Facts.VerificationEvidence = []toolfacts.VerificationEvidence{{
			Kind: toolfacts.VerificationEvidenceGitDiffCheck, Paths: []string{target},
		}}
		semantic := resumed.semanticVerificationLedger([]workspace.ToolResult{patchHygiene})
		gomega.Expect(semantic.PendingPaths).To(gomega.Equal([]string{target}))
		gomega.Expect(resumed.postWriteVerificationLedger([]workspace.ToolResult{patchHygiene}).Pending()).To(gomega.BeFalse(),
			"patch hygiene should leave only the semantic source-verification obligation")
		unrelated := workspace.ToolResult{
			Tool: workspace.ToolNameGoDiagnostics, Summary: "unrelated diagnostics passed",
			Facts: &toolfacts.ToolResultFacts{DiagnosticEvidencePaths: []string{"pkg/other.go"}},
		}
		gomega.Expect(resumed.semanticVerificationLedger([]workspace.ToolResult{patchHygiene, unrelated}).PendingPaths).To(
			gomega.Equal([]string{target}), "an unrelated successful verifier must not clear the receipt-backed source path",
		)

		rejection, rejected := resumed.mutationCompletionRejection(
			"Changed paths:\n- "+target+"\n\nVerification: git diff --check passed.",
			[]workspace.ToolResult{patchHygiene, unrelated},
		)
		gomega.Expect(rejected).To(gomega.BeTrue())
		envelope, typed := completionGateRecoveryEnvelopeFromResult(rejection)
		gomega.Expect(typed).To(gomega.BeTrue())
		gomega.Expect(envelope.InvariantID).To(gomega.Equal(completionGateInvariantSemanticVerification))
		gomega.Expect(envelope.PendingPaths).To(gomega.Equal([]string{target}),
			"the semantic envelope must retain receipt-backed paths after resume")

		resumed.state.recordCompletionGateRejection(rejection)
		verification := workspace.ToolResult{
			Tool: workspace.ToolNameGoDiagnostics, Summary: "target diagnostics passed",
			Facts: &toolfacts.ToolResultFacts{DiagnosticEvidencePaths: []string{target}},
		}
		progress := resumed.observeCompletionGateProgress(workspace.ToolRequest{
			Tool: workspace.ToolNameGoDiagnostics, Path: target,
		}, verification)
		gomega.Expect(progress.MadeProgress()).To(gomega.BeTrue())
		gomega.Expect(progress.SatisfiedContractPaths).To(gomega.Equal([]string{target}))
		gomega.Expect(resumed.state.completionGateRecovery.hasActiveViolation()).To(gomega.BeFalse(),
			"successful semantic verification must resolve the exact resumed path obligation")
	})

})
