package productworkflow

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	plancontract "idiom/internal/contracts/plan"
)

var _ = ginkgo.Describe(
	"source-satisfied product delivery",
	ginkgo.Serial,
	ginkgo.Label("development-scenario", "e2e", "integration", "smoke", "race-contract"),
	func() {
		ginkgo.It("records validated delivery authority idempotently after artifact completion", func() {
			fixture := newCompletionEffortFixture(false)
			completed, err := CompleteEffort(fixture.effortRoot)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(completed.Status).To(gomega.Equal(ProductWorkflowStatusOK), "blockers=%v", completed.Blockers)

			before, err := LoadWorkflowState(fixture.effortRoot)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			genericHandoff, err := LoadImplementationHandoff(fixture.effortRoot)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(genericHandoff.SourceSatisfiedDelivery).To(gomega.BeNil(), "generic artifact completion must not imply source satisfaction")

			completion := validSourceSatisfiedDeliveryCompletion("delivery-session", "internal/example.go")
			request := SourceSatisfiedDeliveryRequest{
				EffortRoot:       fixture.effortRoot,
				ExpectedEffortID: before.EffortID,
				ExpectedTaskID:   before.TaskID,
				Completion:       completion,
			}
			recorded, err := RecordSourceSatisfiedDelivery(request)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(recorded.StateRevision).To(gomega.Equal(before.StateRevision + 1))
			gomega.Expect(recorded.CurrentPhase).To(gomega.Equal(string(PhaseComplete)))
			gomega.Expect(recorded.SourceSatisfiedDelivery).NotTo(gomega.BeNil())
			gomega.Expect(recorded.SourceSatisfiedDelivery.EffortID).To(gomega.Equal(recorded.EffortID))
			gomega.Expect(recorded.SourceSatisfiedDelivery.TaskID).To(gomega.Equal(recorded.TaskID))
			gomega.Expect(recorded.SourceSatisfiedDelivery.Outcome).To(gomega.Equal(plancontract.PlanExecuteTerminalOutcomeSourceSatisfied))
			gomega.Expect(recorded.SourceSatisfiedDelivery.EvidencePaths).To(gomega.Equal([]string{"internal/example.go"}))

			repeated, err := RecordSourceSatisfiedDelivery(request)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(repeated.StateRevision).To(gomega.Equal(recorded.StateRevision), "the same completion must not create another workflow revision")
			gomega.Expect(repeated.LastEventID).To(gomega.Equal(recorded.LastEventID))
			ledgerPath := sourceSatisfiedDeliveryLedgerPath(recorded, fixture.effortRoot)
			matches := sourceSatisfiedDeliveryEventMatches(ledgerPath, recorded.LastEventID)
			gomega.Expect(matches).To(gomega.HaveLen(1), "idempotent replay must preserve exactly one authoritative event")
			var recordedEvent ProductWorkflowTransitionEvent
			gomega.Expect(json.Unmarshal(matches[0].raw, &recordedEvent)).To(gomega.Succeed())
			gomega.Expect(recordedEvent.Event).To(gomega.Equal(string(ProductWorkflowEventSourceSatisfiedRecorded)))
			gomega.Expect(recordedEvent.EffortID).To(gomega.Equal(recorded.EffortID))
			gomega.Expect(recordedEvent.TaskID).To(gomega.Equal(recorded.TaskID))
			gomega.Expect(recordedEvent.Details.CompletionDigest).To(gomega.Equal(recorded.SourceSatisfiedDelivery.CompletionDigest))
			gomega.Expect(recordedEvent.Details.SessionID).To(gomega.Equal(recorded.SourceSatisfiedDelivery.SessionID))

			handoff, err := LoadImplementationHandoff(fixture.effortRoot)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(handoff.SourceSatisfiedDelivery).NotTo(gomega.BeNil())
			gomega.Expect(handoff.SourceSatisfiedDelivery).To(gomega.Equal(recorded.SourceSatisfiedDelivery))
			gomega.Expect(handoff.SourceSatisfiedDelivery).NotTo(gomega.BeIdenticalTo(recorded.SourceSatisfiedDelivery))
			verifiedDelivery := handoff.VerifiedSourceSatisfiedDelivery()
			gomega.Expect(verifiedDelivery).To(gomega.Equal(recorded.SourceSatisfiedDelivery))
			gomega.Expect(verifiedDelivery).NotTo(gomega.BeIdenticalTo(handoff.SourceSatisfiedDelivery))

			transplanted := recorded
			transplantedCloseout := *recorded.SourceSatisfiedDelivery
			transplantedCloseout.EffortID = "different-effort"
			transplanted.SourceSatisfiedDelivery = &transplantedCloseout
			gomega.Expect(validateWorkflowState(transplanted)).To(gomega.MatchError(gomega.ContainSubstring("effort/task mismatch")))
		})

		ginkgo.DescribeTable(
			"rejects stale or ungrounded delivery authority without mutating workflow state",
			func(completeArtifacts bool, mutate func(*SourceSatisfiedDeliveryRequest), errorText string) {
				fixture := newCompletionEffortFixture(false)
				if completeArtifacts {
					completed, err := CompleteEffort(fixture.effortRoot)
					gomega.Expect(err).NotTo(gomega.HaveOccurred())
					gomega.Expect(completed.Status).To(gomega.Equal(ProductWorkflowStatusOK), "blockers=%v", completed.Blockers)
				}
				state, err := LoadWorkflowState(fixture.effortRoot)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				before, err := os.ReadFile(filepath.Join(fixture.effortRoot, WorkflowStateFileName))
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				request := SourceSatisfiedDeliveryRequest{
					EffortRoot:       fixture.effortRoot,
					ExpectedEffortID: state.EffortID,
					ExpectedTaskID:   state.TaskID,
					Completion:       validSourceSatisfiedDeliveryCompletion("rejected-session", "internal/example.go"),
				}
				mutate(&request)

				_, err = RecordSourceSatisfiedDelivery(request)
				gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring(errorText)))
				after, readErr := os.ReadFile(filepath.Join(fixture.effortRoot, WorkflowStateFileName))
				gomega.Expect(readErr).NotTo(gomega.HaveOccurred())
				gomega.Expect(after).To(gomega.Equal(before))
			},
			ginkgo.Entry("before artifact completion", false, func(*SourceSatisfiedDeliveryRequest) {}, "requires workflow phase \"complete\""),
			ginkgo.Entry("mismatched effort", true, func(request *SourceSatisfiedDeliveryRequest) {
				request.ExpectedEffortID = "stale-effort"
			}, "effort/task mismatch"),
			ginkgo.Entry("mismatched task", true, func(request *SourceSatisfiedDeliveryRequest) {
				request.ExpectedTaskID = "stale-task"
			}, "effort/task mismatch"),
			ginkgo.Entry("missing current-state evidence", true, func(request *SourceSatisfiedDeliveryRequest) {
				request.Completion.SourceSatisfiedEvidence = nil
			}, "evidence failed validation"),
			ginkgo.Entry("non-source terminal outcome", true, func(request *SourceSatisfiedDeliveryRequest) {
				request.Completion.Status = plancontract.PlanExecuteTerminalStatusCompleted
				request.Completion.Outcome = plancontract.PlanExecuteTerminalOutcomeCompleted
			}, "requires success/source_satisfied terminal authority"),
		)

		ginkgo.It("commits through the configured event authority and fails closed when that ledger is missing", func() {
			fixture := newCompletionEffortFixture(false)
			completed, err := CompleteEffort(fixture.effortRoot)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(completed.Status).To(gomega.Equal(ProductWorkflowStatusOK), "blockers=%v", completed.Blockers)

			state, err := LoadWorkflowState(fixture.effortRoot)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			originalLedger := sourceSatisfiedDeliveryLedgerPath(state, fixture.effortRoot)
			customRel := filepath.ToSlash(filepath.Join("audit", "product", "delivery-events.jsonl"))
			customLedger := filepath.Join(fixture.effortRoot, filepath.FromSlash(customRel))
			gomega.Expect(os.MkdirAll(filepath.Dir(customLedger), 0o755)).To(gomega.Succeed())
			gomega.Expect(os.Rename(originalLedger, customLedger)).To(gomega.Succeed())
			state.ArtifactPaths["events"] = customRel
			writeSourceSatisfiedWorkflowState(fixture.effortRoot, state)

			request := SourceSatisfiedDeliveryRequest{
				EffortRoot:       fixture.effortRoot,
				ExpectedEffortID: state.EffortID,
				ExpectedTaskID:   state.TaskID,
				Completion:       validSourceSatisfiedDeliveryCompletion("custom-ledger-session", "internal/custom.go"),
			}
			recorded, err := RecordSourceSatisfiedDelivery(request)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(sourceSatisfiedDeliveryEventMatches(customLedger, recorded.LastEventID)).To(gomega.HaveLen(1))
			gomega.Expect(originalLedger).NotTo(gomega.BeAnExistingFile(), "the historical default must not be recreated")
			handoff, err := LoadImplementationHandoff(fixture.effortRoot)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(handoff.SourceSatisfiedDelivery).To(gomega.Equal(recorded.SourceSatisfiedDelivery))

			gomega.Expect(os.Remove(customLedger)).To(gomega.Succeed())
			before := readSourceSatisfiedWorkflowState(fixture.effortRoot)
			_, err = RecordSourceSatisfiedDelivery(request)
			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("verify recorded source-satisfied event")))
			gomega.Expect(artifactWriteCommitted(err)).To(gomega.BeFalse())
			gomega.Expect(readSourceSatisfiedWorkflowState(fixture.effortRoot)).To(gomega.Equal(before))
			gomega.Expect(filepath.Join(fixture.effortRoot, productWorkflowTransitionTransactionRelPath)).NotTo(gomega.BeAnExistingFile())
		})

		ginkgo.It("rejects a missing configured ledger before publishing source-satisfied state", func() {
			fixture := newCompletionEffortFixture(false)
			completed, err := CompleteEffort(fixture.effortRoot)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(completed.Status).To(gomega.Equal(ProductWorkflowStatusOK), "blockers=%v", completed.Blockers)
			state, err := LoadWorkflowState(fixture.effortRoot)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(os.Remove(sourceSatisfiedDeliveryLedgerPath(state, fixture.effortRoot))).To(gomega.Succeed())
			before := readSourceSatisfiedWorkflowState(fixture.effortRoot)

			_, err = RecordSourceSatisfiedDelivery(SourceSatisfiedDeliveryRequest{
				EffortRoot:       fixture.effortRoot,
				ExpectedEffortID: state.EffortID,
				ExpectedTaskID:   state.TaskID,
				Completion:       validSourceSatisfiedDeliveryCompletion("missing-ledger-session", "internal/missing.go"),
			})
			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("inspect transition event ledger")))
			gomega.Expect(artifactWriteCommitted(err)).To(gomega.BeFalse())
			gomega.Expect(readSourceSatisfiedWorkflowState(fixture.effortRoot)).To(gomega.Equal(before))
			gomega.Expect(filepath.Join(fixture.effortRoot, productWorkflowTransitionTransactionRelPath)).NotTo(gomega.BeAnExistingFile())
		})

		ginkgo.It("rejects an EventID collision before publishing workflow authority", func() {
			fixture := newCompletionEffortFixture(false)
			completed, err := CompleteEffort(fixture.effortRoot)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(completed.Status).To(gomega.Equal(ProductWorkflowStatusOK), "blockers=%v", completed.Blockers)
			state, err := LoadWorkflowState(fixture.effortRoot)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			completion := validSourceSatisfiedDeliveryCompletion("collision-session", "internal/collision.go")
			closeout, err := sourceSatisfiedDeliveryCloseout(completion, state.EffortID, state.TaskID)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			eventID := sourceSatisfiedDeliveryEventID(closeout.CompletionDigest)
			ledgerPath := sourceSatisfiedDeliveryLedgerPath(state, fixture.effortRoot)
			collision, err := json.Marshal(ProductWorkflowTransitionEvent{
				Event:     string(ProductWorkflowEventTransitionApplied),
				EventID:   eventID,
				EffortID:  state.EffortID,
				TaskID:    state.TaskID,
				Timestamp: completion.TS,
				Source:    string(ProductWorkflowEventSourceTransitionApply),
				Action:    "different-content",
				Details: ProductWorkflowTransitionEventDetail{
					FromState: state.CurrentState, ToState: state.CurrentState, StateRevision: state.StateRevision + 1,
				},
			})
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			ledger, err := os.ReadFile(ledgerPath)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			ledger = append(ledger, collision...)
			ledger = append(ledger, '\n')
			gomega.Expect(os.WriteFile(ledgerPath, ledger, 0o644)).To(gomega.Succeed())
			before := readSourceSatisfiedWorkflowState(fixture.effortRoot)

			_, err = RecordSourceSatisfiedDelivery(SourceSatisfiedDeliveryRequest{
				EffortRoot:       fixture.effortRoot,
				ExpectedEffortID: state.EffortID,
				ExpectedTaskID:   state.TaskID,
				Completion:       completion,
			})
			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("already belongs to a different event")))
			gomega.Expect(artifactWriteCommitted(err)).To(gomega.BeFalse())
			gomega.Expect(readSourceSatisfiedWorkflowState(fixture.effortRoot)).To(gomega.Equal(before))
			gomega.Expect(filepath.Join(fixture.effortRoot, productWorkflowTransitionTransactionRelPath)).NotTo(gomega.BeAnExistingFile())
		})

		ginkgo.It("reconciles an exact pre-existing EventID without duplicating the ledger line", func() {
			fixture := newCompletionEffortFixture(false)
			completed, err := CompleteEffort(fixture.effortRoot)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(completed.Status).To(gomega.Equal(ProductWorkflowStatusOK), "blockers=%v", completed.Blockers)
			state, err := LoadWorkflowState(fixture.effortRoot)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			completion := validSourceSatisfiedDeliveryCompletion("event-ahead-session", "internal/event-ahead.go")
			closeout, err := sourceSatisfiedDeliveryCloseout(completion, state.EffortID, state.TaskID)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			event := sourceSatisfiedDeliveryEvent(state, closeout, state.StateRevision+1)
			line, err := json.Marshal(event)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			ledgerPath := sourceSatisfiedDeliveryLedgerPath(state, fixture.effortRoot)
			ledger, err := os.ReadFile(ledgerPath)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			ledger = append(ledger, line...)
			ledger = append(ledger, '\n')
			gomega.Expect(os.WriteFile(ledgerPath, ledger, 0o644)).To(gomega.Succeed())

			recorded, err := RecordSourceSatisfiedDelivery(SourceSatisfiedDeliveryRequest{
				EffortRoot:       fixture.effortRoot,
				ExpectedEffortID: state.EffortID,
				ExpectedTaskID:   state.TaskID,
				Completion:       completion,
			})
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(recorded.LastEventID).To(gomega.Equal(event.EventID))
			gomega.Expect(sourceSatisfiedDeliveryEventMatches(ledgerPath, event.EventID)).To(gomega.HaveLen(1))
		})

		ginkgo.DescribeTable(
			"fails handoff loading closed when durable event authority diverges from source-satisfied state",
			func(mutate func(string, WorkflowState, string), errorText string) {
				fixture, recorded, ledgerPath := recordedSourceSatisfiedDeliveryFixture()
				mutate(fixture.effortRoot, recorded, ledgerPath)

				_, err := LoadImplementationHandoff(fixture.effortRoot)
				gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring(errorText)))
			},
			ginkgo.Entry("deleted configured ledger", func(_ string, _ WorkflowState, ledgerPath string) {
				gomega.Expect(os.Remove(ledgerPath)).To(gomega.Succeed())
			}, "required file missing"),
			ginkgo.Entry("corrupt configured ledger", func(_ string, _ WorkflowState, ledgerPath string) {
				gomega.Expect(os.WriteFile(ledgerPath, []byte("{not-json\n"), 0o644)).To(gomega.Succeed())
			}, "parse events.jsonl"),
			ginkgo.Entry("event identity-corrupt configured ledger", func(_ string, _ WorkflowState, ledgerPath string) {
				body, err := os.ReadFile(ledgerPath)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				body = append(body, []byte("{}\n")...)
				gomega.Expect(os.WriteFile(ledgerPath, body, 0o644)).To(gomega.Succeed())
			}, "normalized non-empty event"),
			ginkgo.Entry("duplicate exact event", func(_ string, state WorkflowState, ledgerPath string) {
				matches := sourceSatisfiedDeliveryEventMatches(ledgerPath, state.LastEventID)
				body, err := os.ReadFile(ledgerPath)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				body = append(body, matches[0].raw...)
				body = append(body, '\n')
				gomega.Expect(os.WriteFile(ledgerPath, body, 0o644)).To(gomega.Succeed())
			}, "event authority is invalid"),
			ginkgo.Entry("conflicting duplicate event ID", func(_ string, state WorkflowState, ledgerPath string) {
				appendMutatedSourceSatisfiedEvent(ledgerPath, state.LastEventID, func(event *ProductWorkflowTransitionEvent) {
					event.Details.SessionID = "conflicting-session"
				})
			}, "event authority is invalid"),
			ginkgo.Entry("wrong last event ID", func(effortRoot string, state WorkflowState, _ string) {
				state.LastEventID = "evt-product-source-satisfied-" + string(bytes.Repeat([]byte{'0'}, 64))
				writeSourceSatisfiedWorkflowState(effortRoot, state)
			}, "event authority is invalid"),
			ginkgo.Entry("event effort mismatch", func(_ string, state WorkflowState, ledgerPath string) {
				rewriteSourceSatisfiedEvent(ledgerPath, state.LastEventID, func(event *ProductWorkflowTransitionEvent) {
					event.EffortID = "other-effort"
				})
			}, "event authority is invalid"),
			ginkgo.Entry("event task mismatch", func(_ string, state WorkflowState, ledgerPath string) {
				rewriteSourceSatisfiedEvent(ledgerPath, state.LastEventID, func(event *ProductWorkflowTransitionEvent) {
					event.TaskID = "other-task"
				})
			}, "event authority is invalid"),
			ginkgo.Entry("event session mismatch", func(_ string, state WorkflowState, ledgerPath string) {
				rewriteSourceSatisfiedEvent(ledgerPath, state.LastEventID, func(event *ProductWorkflowTransitionEvent) {
					event.Details.SessionID = "other-session"
				})
			}, "event authority is invalid"),
			ginkgo.Entry("event digest mismatch", func(_ string, state WorkflowState, ledgerPath string) {
				rewriteSourceSatisfiedEvent(ledgerPath, state.LastEventID, func(event *ProductWorkflowTransitionEvent) {
					event.Details.CompletionDigest = "sha256:" + string(bytes.Repeat([]byte{'f'}, 64))
				})
			}, "event authority is invalid"),
			ginkgo.Entry("event timestamp mismatch", func(_ string, state WorkflowState, ledgerPath string) {
				rewriteSourceSatisfiedEvent(ledgerPath, state.LastEventID, func(event *ProductWorkflowTransitionEvent) {
					event.Timestamp = "2026-07-12T12:00:01Z"
				})
			}, "event authority is invalid"),
			ginkgo.Entry("event lifecycle mismatch", func(_ string, state WorkflowState, ledgerPath string) {
				rewriteSourceSatisfiedEvent(ledgerPath, state.LastEventID, func(event *ProductWorkflowTransitionEvent) {
					event.Details.FromState = WorkflowStateImplementing
				})
			}, "event authority is invalid"),
			ginkgo.Entry("event revision mismatch", func(_ string, state WorkflowState, ledgerPath string) {
				rewriteSourceSatisfiedEvent(ledgerPath, state.LastEventID, func(event *ProductWorkflowTransitionEvent) {
					event.Details.StateRevision++
				})
			}, "event authority is invalid"),
			ginkgo.Entry("unknown event payload field", func(_ string, state WorkflowState, ledgerPath string) {
				matches := sourceSatisfiedDeliveryEventMatches(ledgerPath, state.LastEventID)
				var payload map[string]any
				gomega.Expect(json.Unmarshal(matches[0].raw, &payload)).To(gomega.Succeed())
				payload["untrusted_projection"] = true
				replacement, err := json.Marshal(payload)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				rewriteSourceSatisfiedEventRaw(ledgerPath, state.LastEventID, replacement)
			}, "event authority is invalid"),
		)

		ginkgo.It("rejects idempotent replay unless the existing event is byte-exact authority", func() {
			fixture, recorded, ledgerPath := recordedSourceSatisfiedDeliveryFixture()
			matches := sourceSatisfiedDeliveryEventMatches(ledgerPath, recorded.LastEventID)
			var payload map[string]any
			gomega.Expect(json.Unmarshal(matches[0].raw, &payload)).To(gomega.Succeed())
			payload["untrusted_projection"] = true
			replacement, err := json.Marshal(payload)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			rewriteSourceSatisfiedEventRaw(ledgerPath, recorded.LastEventID, replacement)
			before := readSourceSatisfiedWorkflowState(fixture.effortRoot)

			_, err = RecordSourceSatisfiedDelivery(SourceSatisfiedDeliveryRequest{
				EffortRoot:       fixture.effortRoot,
				ExpectedEffortID: recorded.EffortID,
				ExpectedTaskID:   recorded.TaskID,
				Completion:       validSourceSatisfiedDeliveryCompletion("authority-session", "internal/authority.go"),
			})
			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("already belongs to a different event")))
			gomega.Expect(readSourceSatisfiedWorkflowState(fixture.effortRoot)).To(gomega.Equal(before))
		})

		ginkgo.DescribeTable(
			"rejects retained-digest tampering of every durable closeout authority field",
			func(mutate func(*SourceSatisfiedDeliveryCloseout)) {
				closeout, err := sourceSatisfiedDeliveryCloseout(
					validSourceSatisfiedDeliveryCompletion("tamper-session", "internal/tamper.go"),
					"tamper-effort",
					"tamper-task",
				)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				originalDigest := closeout.CompletionDigest
				mutate(&closeout)
				gomega.Expect(closeout.CompletionDigest).To(gomega.Equal(originalDigest))
				gomega.Expect(ValidateSourceSatisfiedDeliveryCloseout(closeout)).NotTo(gomega.Succeed())
			},
			ginkgo.Entry("effort identity", func(closeout *SourceSatisfiedDeliveryCloseout) { closeout.EffortID = "other-effort" }),
			ginkgo.Entry("task identity", func(closeout *SourceSatisfiedDeliveryCloseout) { closeout.TaskID = "other-task" }),
			ginkgo.Entry("outcome", func(closeout *SourceSatisfiedDeliveryCloseout) {
				closeout.Outcome = plancontract.PlanExecuteTerminalOutcomeCompleted
			}),
			ginkgo.Entry("session identity", func(closeout *SourceSatisfiedDeliveryCloseout) { closeout.SessionID = "other-session" }),
			ginkgo.Entry("evidence paths", func(closeout *SourceSatisfiedDeliveryCloseout) {
				closeout.EvidencePaths = []string{"internal/other.go"}
			}),
			ginkgo.Entry("typed evidence refs", func(closeout *SourceSatisfiedDeliveryCloseout) {
				closeout.Evidence = []plancontract.SourceSatisfiedEvidenceRef{{
					Path: "internal/other.go",
					Kind: plancontract.SourceSatisfiedEvidenceKindHarnessObservedSource,
				}}
			}),
			ginkgo.Entry("stable timestamp", func(closeout *SourceSatisfiedDeliveryCloseout) { closeout.RecordedAt = "2026-07-12T12:00:01Z" }),
		)

		ginkgo.DescribeTable(
			"recovers every publication seam after an effort-root swap",
			func(boundary productWorkflowTransitionPublicationBoundary, wantCommitted bool) {
				fixture := newCompletionEffortFixture(false)
				completed, err := CompleteEffort(fixture.effortRoot)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				gomega.Expect(completed.Status).To(gomega.Equal(ProductWorkflowStatusOK), "blockers=%v", completed.Blockers)
				state, err := LoadWorkflowState(fixture.effortRoot)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				request := SourceSatisfiedDeliveryRequest{
					EffortRoot:       fixture.effortRoot,
					ExpectedEffortID: state.EffortID,
					ExpectedTaskID:   state.TaskID,
					Completion:       validSourceSatisfiedDeliveryCompletion("restart-session", "internal/restart.go"),
				}
				movedRoot := fixture.effortRoot + "-moved"
				swapped := false
				originalHook := productWorkflowTransitionPublicationBoundaryHook
				ginkgo.DeferCleanup(func() { productWorkflowTransitionPublicationBoundaryHook = originalHook })
				productWorkflowTransitionPublicationBoundaryHook = func(observed productWorkflowTransitionPublicationBoundary) {
					if swapped || observed != boundary {
						return
					}
					gomega.Expect(os.Rename(fixture.effortRoot, movedRoot)).To(gomega.Succeed())
					gomega.Expect(os.Mkdir(fixture.effortRoot, 0o755)).To(gomega.Succeed())
					gomega.Expect(os.WriteFile(filepath.Join(fixture.effortRoot, "replacement-marker"), []byte("replacement\n"), 0o644)).To(gomega.Succeed())
					swapped = true
				}

				_, err = RecordSourceSatisfiedDelivery(request)
				gomega.Expect(swapped).To(gomega.BeTrue())
				gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("no longer names the locked effort root")))
				gomega.Expect(artifactWriteCommitted(err)).To(gomega.Equal(wantCommitted), "boundary=%d err=%v", boundary, err)
				gomega.Expect(filepath.Join(fixture.effortRoot, "replacement-marker")).To(gomega.BeAnExistingFile())
				gomega.Expect(filepath.Join(fixture.effortRoot, WorkflowStateFileName)).NotTo(gomega.BeAnExistingFile())

				productWorkflowTransitionPublicationBoundaryHook = nil
				gomega.Expect(os.RemoveAll(fixture.effortRoot)).To(gomega.Succeed())
				gomega.Expect(os.Rename(movedRoot, fixture.effortRoot)).To(gomega.Succeed())
				recovered, retryErr := RecordSourceSatisfiedDelivery(request)
				gomega.Expect(retryErr).NotTo(gomega.HaveOccurred())
				gomega.Expect(recovered.SourceSatisfiedDelivery).NotTo(gomega.BeNil())
				gomega.Expect(recovered.LastEventID).To(gomega.Equal(sourceSatisfiedDeliveryEventID(recovered.SourceSatisfiedDelivery.CompletionDigest)))
				gomega.Expect(sourceSatisfiedDeliveryEventMatches(
					sourceSatisfiedDeliveryLedgerPath(recovered, fixture.effortRoot),
					recovered.LastEventID,
				)).To(gomega.HaveLen(1))
				gomega.Expect(filepath.Join(fixture.effortRoot, productWorkflowTransitionTransactionRelPath)).NotTo(gomega.BeAnExistingFile())
			},
			ginkgo.Entry("before transaction entry", productWorkflowTransitionBeforeTransactionEntry, false),
			ginkgo.Entry("before prepared manifest", productWorkflowTransitionBeforePreparedManifest, false),
			ginkgo.Entry("before workflow state", productWorkflowTransitionBeforeWorkflowState, false),
			ginkgo.Entry("before event ledger", productWorkflowTransitionBeforeEventLedger, true),
			ginkgo.Entry("before committed manifest", productWorkflowTransitionBeforeCommittedManifest, true),
			ginkgo.Entry("before transaction cleanup", productWorkflowTransitionBeforeTransactionCleanup, true),
		)
	},
)

func validSourceSatisfiedDeliveryCompletion(sessionID string, path string) plancontract.PlanExecuteTerminalCompletionRecord {
	return plancontract.NormalizePlanExecuteTerminalCompletionRecord(plancontract.PlanExecuteTerminalCompletionRecord{
		SchemaVersion:                plancontract.PlanExecuteTerminalCompletionSchemaVersion,
		Type:                         plancontract.PlanExecuteTerminalCompletionRecordType,
		TS:                           "2026-07-12T12:00:00Z",
		SessionID:                    sessionID,
		Phase:                        plancontract.PlanExecuteProgressPhaseComplete,
		Status:                       plancontract.PlanExecuteTerminalStatusSourceSatisfied,
		TerminalClass:                plancontract.PlanExecuteTerminalClassSuccess,
		Outcome:                      plancontract.PlanExecuteTerminalOutcomeSourceSatisfied,
		RequiredEditPaths:            []string{path},
		SourceSatisfiedEvidencePaths: []string{path},
		SourceSatisfiedEvidence: []plancontract.SourceSatisfiedEvidenceRef{{
			Path: path,
			Kind: plancontract.SourceSatisfiedEvidenceKindHarnessObservedSource,
		}},
	})
}

func recordedSourceSatisfiedDeliveryFixture() (completionEffortFixture, WorkflowState, string) {
	ginkgo.GinkgoHelper()

	fixture := newCompletionEffortFixture(false)
	completed, err := CompleteEffort(fixture.effortRoot)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(completed.Status).To(gomega.Equal(ProductWorkflowStatusOK), "blockers=%v", completed.Blockers)
	state, err := LoadWorkflowState(fixture.effortRoot)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	recorded, err := RecordSourceSatisfiedDelivery(SourceSatisfiedDeliveryRequest{
		EffortRoot:       fixture.effortRoot,
		ExpectedEffortID: state.EffortID,
		ExpectedTaskID:   state.TaskID,
		Completion:       validSourceSatisfiedDeliveryCompletion("authority-session", "internal/authority.go"),
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	return fixture, recorded, sourceSatisfiedDeliveryLedgerPath(recorded, fixture.effortRoot)
}

func appendMutatedSourceSatisfiedEvent(
	ledgerPath string,
	eventID string,
	mutate func(*ProductWorkflowTransitionEvent),
) {
	ginkgo.GinkgoHelper()

	matches := sourceSatisfiedDeliveryEventMatches(ledgerPath, eventID)
	gomega.Expect(matches).To(gomega.HaveLen(1))
	var event ProductWorkflowTransitionEvent
	gomega.Expect(json.Unmarshal(matches[0].raw, &event)).To(gomega.Succeed())
	mutate(&event)
	line, err := json.Marshal(event)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	body, err := os.ReadFile(ledgerPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	if len(body) > 0 && body[len(body)-1] != '\n' {
		body = append(body, '\n')
	}
	body = append(body, line...)
	body = append(body, '\n')
	gomega.Expect(os.WriteFile(ledgerPath, body, 0o644)).To(gomega.Succeed())
}

func rewriteSourceSatisfiedEvent(
	ledgerPath string,
	eventID string,
	mutate func(*ProductWorkflowTransitionEvent),
) {
	ginkgo.GinkgoHelper()

	matches := sourceSatisfiedDeliveryEventMatches(ledgerPath, eventID)
	gomega.Expect(matches).To(gomega.HaveLen(1))
	var event ProductWorkflowTransitionEvent
	gomega.Expect(json.Unmarshal(matches[0].raw, &event)).To(gomega.Succeed())
	mutate(&event)
	replacement, err := json.Marshal(event)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	rewriteSourceSatisfiedEventRaw(ledgerPath, eventID, replacement)
}

func rewriteSourceSatisfiedEventRaw(ledgerPath string, eventID string, replacement []byte) {
	ginkgo.GinkgoHelper()

	body, err := os.ReadFile(ledgerPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	lines := bytes.Split(body, []byte{'\n'})
	replaced := false
	for i, line := range lines {
		var identity struct {
			EventID string `json:"event_id"`
		}
		if json.Unmarshal(bytes.TrimSpace(line), &identity) == nil && identity.EventID == eventID {
			lines[i] = append([]byte(nil), replacement...)
			replaced = true
		}
	}
	gomega.Expect(replaced).To(gomega.BeTrue())
	gomega.Expect(os.WriteFile(ledgerPath, bytes.Join(lines, []byte{'\n'}), 0o644)).To(gomega.Succeed())
}

func sourceSatisfiedDeliveryLedgerPath(state WorkflowState, effortRoot string) string {
	ginkgo.GinkgoHelper()
	relPath, err := productWorkflowEventsPath(state)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	return filepath.Join(effortRoot, relPath)
}

func sourceSatisfiedDeliveryEventMatches(path string, eventID string) []productWorkflowEventEnvelope {
	ginkgo.GinkgoHelper()
	body, err := os.ReadFile(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	matches, err := productWorkflowEventEnvelopesByID(body, eventID)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	return matches
}

func writeSourceSatisfiedWorkflowState(effortRoot string, state WorkflowState) {
	ginkgo.GinkgoHelper()
	body, err := json.MarshalIndent(state, "", "  ")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	body = append(body, '\n')
	gomega.Expect(os.WriteFile(filepath.Join(effortRoot, WorkflowStateFileName), body, 0o644)).To(gomega.Succeed())
}

func readSourceSatisfiedWorkflowState(effortRoot string) []byte {
	ginkgo.GinkgoHelper()
	body, err := os.ReadFile(filepath.Join(effortRoot, WorkflowStateFileName))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	return body
}
