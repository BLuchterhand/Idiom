package productworkflow

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe(
	"Product workflow transaction recovery",
	ginkgo.Serial,
	ginkgo.Label("development-scenario", "e2e", "integration", "smoke", "race-contract"),
	func() {
		ginkgo.DescribeTable("classifies durable transition-journal recovery branches",
			func(testCase workflowTransitionRecoveryCase) {
				fixture := newWorkflowTransitionRecoveryFixture()
				fixture.writeWorkflowGeneration(testCase.workflowGeneration)
				fixture.writeEventGeneration(testCase.eventGeneration)

				root := openWorkflowRecoveryRoot(fixture.scenario.sourceRoot)
				ginkgo.DeferCleanup(root.Close)
				fixture.writeTransaction(root, testCase.transaction, testCase.manifestState)

				lease := &productWorkflowMutationLease{root: root, effortRoot: fixture.scenario.sourceRoot}
				recoveryErr := recoverProductWorkflowTransitionLocked(lease)
				if testCase.wantError == "" {
					gomega.Expect(recoveryErr).NotTo(gomega.HaveOccurred())
				} else {
					gomega.Expect(recoveryErr).To(gomega.MatchError(gomega.ContainSubstring(testCase.wantError)))
				}

				workflowBody, err := readProductWorkflowTransitionRegularFile(root, WorkflowStateFileName)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				gomega.Expect(workflowBody).To(gomega.Equal(fixture.workflowBytes(testCase.wantWorkflowGeneration)))
				gomega.Expect(countWorkflowRecoveryEventID(fixture.ledgerPath(), fixture.event.EventID)).To(gomega.Equal(testCase.wantEventCount))

				_, transactionErr := root.Lstat(productWorkflowTransitionTransactionRelPath)
				if testCase.wantTransactionRetained {
					gomega.Expect(transactionErr).NotTo(gomega.HaveOccurred(), "the failed recovery must retain its transaction path for repair")
				} else {
					gomega.Expect(os.IsNotExist(transactionErr)).To(gomega.BeTrue(), "the completed or aborted recovery must remove its transaction path, got %v", transactionErr)
				}
			},
			ginkgo.Entry("does nothing when the transaction path is absent", workflowTransitionRecoveryCase{
				transaction:            workflowRecoveryTransactionAbsent,
				workflowGeneration:     workflowRecoveryGenerationPrior,
				eventGeneration:        workflowRecoveryEventAbsent,
				wantWorkflowGeneration: workflowRecoveryGenerationPrior,
			}),
			ginkgo.Entry("aborts a transaction directory without a durable manifest", workflowTransitionRecoveryCase{
				transaction:            workflowRecoveryTransactionDirectoryOnly,
				workflowGeneration:     workflowRecoveryGenerationPrior,
				eventGeneration:        workflowRecoveryEventAbsent,
				wantWorkflowGeneration: workflowRecoveryGenerationPrior,
			}),
			ginkgo.Entry("aborts a prepared manifest while the prior workflow remains authoritative", workflowTransitionRecoveryCase{
				transaction:            workflowRecoveryTransactionManifest,
				manifestState:          productWorkflowTransitionStatePrepared,
				workflowGeneration:     workflowRecoveryGenerationPrior,
				eventGeneration:        workflowRecoveryEventAbsent,
				wantWorkflowGeneration: workflowRecoveryGenerationPrior,
			}),
			ginkgo.Entry("completes a prepared candidate whose event is absent", workflowTransitionRecoveryCase{
				transaction:            workflowRecoveryTransactionManifest,
				manifestState:          productWorkflowTransitionStatePrepared,
				workflowGeneration:     workflowRecoveryGenerationCandidate,
				eventGeneration:        workflowRecoveryEventAbsent,
				wantWorkflowGeneration: workflowRecoveryGenerationCandidate,
				wantEventCount:         1,
			}),
			ginkgo.Entry("completes a prepared candidate without duplicating its exact event", workflowTransitionRecoveryCase{
				transaction:            workflowRecoveryTransactionManifest,
				manifestState:          productWorkflowTransitionStatePrepared,
				workflowGeneration:     workflowRecoveryGenerationCandidate,
				eventGeneration:        workflowRecoveryEventExact,
				wantWorkflowGeneration: workflowRecoveryGenerationCandidate,
				wantEventCount:         1,
			}),
			ginkgo.Entry("completes a committed candidate whose event append was interrupted", workflowTransitionRecoveryCase{
				transaction:            workflowRecoveryTransactionManifest,
				manifestState:          productWorkflowTransitionStateCommitted,
				workflowGeneration:     workflowRecoveryGenerationCandidate,
				eventGeneration:        workflowRecoveryEventAbsent,
				wantWorkflowGeneration: workflowRecoveryGenerationCandidate,
				wantEventCount:         1,
			}),
			ginkgo.Entry("retains a committed manifest that reverted to the prior workflow", workflowTransitionRecoveryCase{
				transaction:             workflowRecoveryTransactionManifest,
				manifestState:           productWorkflowTransitionStateCommitted,
				workflowGeneration:      workflowRecoveryGenerationPrior,
				eventGeneration:         workflowRecoveryEventAbsent,
				wantError:               "committed product workflow transition reverted to its previous workflow generation",
				wantWorkflowGeneration:  workflowRecoveryGenerationPrior,
				wantTransactionRetained: true,
			}),
			ginkgo.Entry("retains a prepared manifest when workflow matches neither generation", workflowTransitionRecoveryCase{
				transaction:             workflowRecoveryTransactionManifest,
				manifestState:           productWorkflowTransitionStatePrepared,
				workflowGeneration:      workflowRecoveryGenerationDivergent,
				eventGeneration:         workflowRecoveryEventAbsent,
				wantError:               "workflow state matches neither transition journal generation",
				wantWorkflowGeneration:  workflowRecoveryGenerationDivergent,
				wantTransactionRetained: true,
			}),
			ginkgo.Entry("retains a candidate when its EventID belongs to a conflicting event", workflowTransitionRecoveryCase{
				transaction:             workflowRecoveryTransactionManifest,
				manifestState:           productWorkflowTransitionStatePrepared,
				workflowGeneration:      workflowRecoveryGenerationCandidate,
				eventGeneration:         workflowRecoveryEventConflicting,
				wantError:               "already belongs to a different event",
				wantWorkflowGeneration:  workflowRecoveryGenerationCandidate,
				wantEventCount:          1,
				wantTransactionRetained: true,
			}),
			ginkgo.Entry("retains a non-directory transaction path for repair", workflowTransitionRecoveryCase{
				transaction:             workflowRecoveryTransactionRegularFile,
				workflowGeneration:      workflowRecoveryGenerationPrior,
				eventGeneration:         workflowRecoveryEventAbsent,
				wantError:               "transaction path must be a directory",
				wantWorkflowGeneration:  workflowRecoveryGenerationPrior,
				wantTransactionRetained: true,
			}),
			ginkgo.Entry("retains a malformed manifest for repair", workflowTransitionRecoveryCase{
				transaction:             workflowRecoveryTransactionMalformedManifest,
				workflowGeneration:      workflowRecoveryGenerationPrior,
				eventGeneration:         workflowRecoveryEventAbsent,
				wantError:               "read product workflow transition journal",
				wantWorkflowGeneration:  workflowRecoveryGenerationPrior,
				wantTransactionRetained: true,
			}),
		)

		ginkgo.It("rejects fresh transition EventID reuse without changing either authority", func() {
			fixture := newWorkflowTransitionRecoveryFixture()
			fixture.writeWorkflowGeneration(workflowRecoveryGenerationCandidate)
			fixture.writeEventGeneration(workflowRecoveryEventExact)
			beforeLedger := readWorkflowRecoveryFile(fixture.ledgerPath())

			lease, err := openProductWorkflowMutationLease(fixture.scenario.sourceRoot)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			ginkgo.DeferCleanup(lease.Close)
			err = writeProductWorkflowTransition(lease, fixture.candidate, fixture.event)

			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("already exists")))
			gomega.Expect(readWorkflowRecoveryFile(fixture.ledgerPath())).To(gomega.Equal(beforeLedger))
			gomega.Expect(readWorkflowRecoveryFile(filepath.Join(fixture.scenario.sourceRoot, WorkflowStateFileName))).To(gomega.Equal(fixture.candidateBytes))
		})

		ginkgo.It("retains its migration fence until rename-parent durability is recovered", func() {
			scenario := newWorkflowTransactionScenario("audit/product/events.jsonl")
			options := scenario.migrationOptions()
			originalSyncDir := productStatusMigrationSyncDir
			ginkgo.DeferCleanup(func() {
				productStatusMigrationSyncDir = originalSyncDir
			})

			targetParent := filepath.Clean(filepath.Dir(scenario.targetRoot))
			sourceParent := filepath.Clean(filepath.Dir(scenario.sourceRoot))
			failFirstSourceSync := true
			var syncedParents []string
			productStatusMigrationSyncDir = func(path string) error {
				cleanPath := filepath.Clean(path)
				syncedParents = append(syncedParents, cleanPath)
				if failFirstSourceSync && cleanPath == sourceParent {
					failFirstSourceSync = false

					return os.ErrPermission
				}

				return originalSyncDir(path)
			}

			interrupted := ApplyProductStatusMigration(options)
			gomega.Expect(interrupted.Status).To(gomega.Equal(ProductWorkflowStatusApplied), fmt.Sprintf("unexpected interrupted result: %#v", interrupted))
			gomega.Expect(interrupted.CommitWarning).To(gomega.ContainSubstring("forward recovery required"))
			gomega.Expect(syncedParents).To(gomega.ContainElements(targetParent, sourceParent))
			gomega.Expect(filepath.Join(scenario.targetRoot, productStatusMigrationPendingMarkerName)).To(gomega.BeAnExistingFile())
			gomega.Expect(filepath.Join(scenario.effortsRoot, productStatusMigrationTransactionRelPath)).To(gomega.BeADirectory())

			beforeWorkflow := readWorkflowRecoveryFile(filepath.Join(scenario.targetRoot, WorkflowStateFileName))
			beforeEvents := readWorkflowRecoveryFile(filepath.Join(scenario.targetRoot, scenario.eventsPath))
			beforeIndex := readWorkflowRecoveryFile(filepath.Join(scenario.effortsRoot, "index.json"))

			ordinaryMutation := ApplyProductWorkflowTransition(ProductWorkflowTransitionApplyOptions{
				EffortRoot:            scenario.targetRoot,
				Action:                ProductWorkflowActionApprove,
				UpdatedAt:             "2026-07-12T14:02:00Z",
				EventID:               "evt-must-not-commit-while-migrating",
				ExpectedStateRevision: scenario.workflowState.StateRevision,
			})
			gomega.Expect(ordinaryMutation.Status).To(gomega.Equal(ProductWorkflowStatusBlocked))
			gomega.Expect(workflowRecoveryTransitionHasBlocker(ordinaryMutation, "product_workflow_lock_error")).To(gomega.BeTrue(), fmt.Sprintf("expected migration fence blocker, got %#v", ordinaryMutation.Blockers))
			gomega.Expect(readWorkflowRecoveryFile(filepath.Join(scenario.targetRoot, WorkflowStateFileName))).To(gomega.Equal(beforeWorkflow))
			gomega.Expect(readWorkflowRecoveryFile(filepath.Join(scenario.targetRoot, scenario.eventsPath))).To(gomega.Equal(beforeEvents))
			gomega.Expect(readWorkflowRecoveryFile(filepath.Join(scenario.effortsRoot, "index.json"))).To(gomega.Equal(beforeIndex))

			recovered := ApplyProductStatusMigration(options)
			gomega.Expect(recovered.Status).To(gomega.Equal(ProductWorkflowStatusApplied), fmt.Sprintf("unexpected recovery result: %#v", recovered))
			gomega.Expect(recovered.Blockers).To(gomega.BeEmpty())
			gomega.Expect(recovered.CommitWarning).To(gomega.ContainSubstring("recovered an interrupted product status migration"))
			gomega.Expect(workflowRecoveryPathCount(syncedParents, targetParent)).To(gomega.BeNumerically(">=", 2))
			gomega.Expect(workflowRecoveryPathCount(syncedParents, sourceParent)).To(gomega.BeNumerically(">=", 2))
			gomega.Expect(filepath.Join(scenario.targetRoot, productStatusMigrationPendingMarkerName)).NotTo(gomega.BeAnExistingFile())
			gomega.Expect(filepath.Join(scenario.effortsRoot, productStatusMigrationTransactionRelPath)).NotTo(gomega.BeADirectory())
			gomega.Expect(countWorkflowRecoveryEventID(filepath.Join(scenario.targetRoot, scenario.eventsPath), options.MigrationEventID)).To(gomega.Equal(1))
			gomega.Expect(filepath.Join(scenario.targetRoot, productWorkflowDefaultEventsPath)).NotTo(gomega.BeAnExistingFile())

			lease, err := openProductWorkflowMutationLease(scenario.targetRoot)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			lease.Close()
		})

		ginkgo.It("fails closed when both migration names have equal bytes but distinct filesystem identity", func() {
			scenario := newWorkflowTransactionScenario("events.jsonl")
			journal := prepareWorkflowRecoveryMigrationJournal(scenario)
			writeWorkflowRecoveryMarker(scenario.sourceRoot, journal.manifest)

			writeWorkflowRecoveryFile(
				filepath.Join(scenario.targetRoot, WorkflowStateFileName),
				readWorkflowRecoveryFile(filepath.Join(scenario.sourceRoot, WorkflowStateFileName)),
			)
			writeWorkflowRecoveryFile(
				filepath.Join(scenario.targetRoot, scenario.eventsPath),
				readWorkflowRecoveryFile(filepath.Join(scenario.sourceRoot, scenario.eventsPath)),
			)
			writeWorkflowRecoveryMarker(scenario.targetRoot, journal.manifest)

			sourceBefore := readWorkflowRecoveryFile(filepath.Join(scenario.sourceRoot, WorkflowStateFileName))
			targetBefore := readWorkflowRecoveryFile(filepath.Join(scenario.targetRoot, WorkflowStateFileName))
			indexBefore := readWorkflowRecoveryFile(filepath.Join(scenario.effortsRoot, "index.json"))

			result := ApplyProductStatusMigration(scenario.migrationOptions())
			gomega.Expect(result.Status).To(gomega.Equal(ProductWorkflowStatusBlocked), fmt.Sprintf("distinct both-name generations must not reconcile: %#v", result))
			gomega.Expect(workflowRecoveryMigrationHasBlocker(result, "status_migration_recovery_error")).To(gomega.BeTrue())
			gomega.Expect(workflowRecoveryMigrationBlockerText(result)).To(gomega.ContainSubstring("same_file=false"))
			gomega.Expect(readWorkflowRecoveryFile(filepath.Join(scenario.sourceRoot, WorkflowStateFileName))).To(gomega.Equal(sourceBefore))
			gomega.Expect(readWorkflowRecoveryFile(filepath.Join(scenario.targetRoot, WorkflowStateFileName))).To(gomega.Equal(targetBefore))
			gomega.Expect(readWorkflowRecoveryFile(filepath.Join(scenario.effortsRoot, "index.json"))).To(gomega.Equal(indexBefore))
			gomega.Expect(filepath.Join(scenario.effortsRoot, productStatusMigrationTransactionRelPath)).To(gomega.BeADirectory())
			gomega.Expect(filepath.Join(scenario.sourceRoot, productStatusMigrationPendingMarkerName)).To(gomega.BeAnExistingFile())
			gomega.Expect(filepath.Join(scenario.targetRoot, productStatusMigrationPendingMarkerName)).To(gomega.BeAnExistingFile())
		})

		ginkgo.It("binds migration recovery to the exact effort-local pending marker authority", func() {
			scenario := newWorkflowTransactionScenario("events.jsonl")
			journal := prepareWorkflowRecoveryMigrationJournal(scenario)
			wrongMarker := journal.manifest
			wrongMarker.SourceFolderPath = "unstarted/" + scenario.workflowState.EffortID
			writeWorkflowRecoveryMarker(scenario.sourceRoot, wrongMarker)

			gomega.Expect(os.MkdirAll(filepath.Dir(scenario.targetRoot), 0o755)).To(gomega.Succeed())
			gomega.Expect(os.Rename(scenario.sourceRoot, scenario.targetRoot)).To(gomega.Succeed())
			workflowBefore := readWorkflowRecoveryFile(filepath.Join(scenario.targetRoot, WorkflowStateFileName))
			eventsBefore := readWorkflowRecoveryFile(filepath.Join(scenario.targetRoot, scenario.eventsPath))
			indexBefore := readWorkflowRecoveryFile(filepath.Join(scenario.effortsRoot, "index.json"))

			result := ApplyProductStatusMigration(scenario.migrationOptions())
			gomega.Expect(result.Status).To(gomega.Equal(ProductWorkflowStatusBlocked), fmt.Sprintf("mismatched marker must not recover: %#v", result))
			gomega.Expect(workflowRecoveryMigrationHasBlocker(result, "status_migration_recovery_error")).To(gomega.BeTrue())
			gomega.Expect(workflowRecoveryMigrationBlockerText(result)).To(gomega.ContainSubstring("does not match efforts-root journal authority"))
			gomega.Expect(readWorkflowRecoveryFile(filepath.Join(scenario.targetRoot, WorkflowStateFileName))).To(gomega.Equal(workflowBefore))
			gomega.Expect(readWorkflowRecoveryFile(filepath.Join(scenario.targetRoot, scenario.eventsPath))).To(gomega.Equal(eventsBefore))
			gomega.Expect(readWorkflowRecoveryFile(filepath.Join(scenario.effortsRoot, "index.json"))).To(gomega.Equal(indexBefore))
			gomega.Expect(filepath.Join(scenario.effortsRoot, productStatusMigrationTransactionRelPath)).To(gomega.BeADirectory())
			gomega.Expect(filepath.Join(scenario.targetRoot, productStatusMigrationPendingMarkerName)).To(gomega.BeAnExistingFile())
		})

		ginkgo.It("rejects missing and overlapping event authorities before migration mutation", func() {
			cases := []struct {
				name          string
				artifactPaths map[string]string
				message       string
			}{
				{
					name:          "missing event authority",
					artifactPaths: map[string]string{"briefs": "briefs"},
					message:       "must be a safe relative path",
				},
				{
					name:          "equal to fixed workflow authority",
					artifactPaths: map[string]string{"events": WorkflowStateFileName},
					message:       "overlaps workflow state path",
				},
				{
					name:          "descendant of fixed brief authority",
					artifactPaths: map[string]string{"events": "briefs/events.jsonl"},
					message:       "overlaps brief authority path",
				},
				{
					name: "ancestor of another artifact authority",
					artifactPaths: map[string]string{
						"events":  "records",
						"reports": "records/report.json",
					},
					message: "overlaps artifact_paths.reports",
				},
				{
					name: "descendant of another artifact authority",
					artifactPaths: map[string]string{
						"events":  "records/events.jsonl",
						"reports": "records",
					},
					message: "overlaps artifact_paths.reports",
				},
			}

			for _, testCase := range cases {
				ginkgo.By(testCase.name, func() {
					scenario := newWorkflowTransactionScenario("audit/product/events.jsonl")
					state := scenario.workflowState
					state.ArtifactPaths = testCase.artifactPaths
					writeWorkflowRecoveryJSON(filepath.Join(scenario.sourceRoot, WorkflowStateFileName), state)

					workflowBefore := readWorkflowRecoveryFile(filepath.Join(scenario.sourceRoot, WorkflowStateFileName))
					eventsBefore := readWorkflowRecoveryFile(filepath.Join(scenario.sourceRoot, scenario.eventsPath))
					indexBefore := readWorkflowRecoveryFile(filepath.Join(scenario.effortsRoot, "index.json"))
					result := ApplyProductStatusMigration(scenario.migrationOptions())

					gomega.Expect(result.Status).To(gomega.Equal(ProductWorkflowStatusBlocked), fmt.Sprintf("unsafe authority must fail closed: %#v", result))
					gomega.Expect(workflowRecoveryMigrationHasBlocker(result, "invalid_events_artifact_path")).To(gomega.BeTrue(), fmt.Sprintf("missing typed authority blocker: %#v", result.Blockers))
					gomega.Expect(workflowRecoveryMigrationBlockerText(result)).To(gomega.ContainSubstring(testCase.message))
					gomega.Expect(readWorkflowRecoveryFile(filepath.Join(scenario.sourceRoot, WorkflowStateFileName))).To(gomega.Equal(workflowBefore))
					gomega.Expect(readWorkflowRecoveryFile(filepath.Join(scenario.sourceRoot, scenario.eventsPath))).To(gomega.Equal(eventsBefore))
					gomega.Expect(readWorkflowRecoveryFile(filepath.Join(scenario.effortsRoot, "index.json"))).To(gomega.Equal(indexBefore))
				})
			}
		})
	},
)

type workflowRecoveryTransaction string
type workflowRecoveryGeneration string
type workflowRecoveryEventGeneration string

const (
	workflowRecoveryTransactionAbsent            workflowRecoveryTransaction     = "absent"
	workflowRecoveryTransactionDirectoryOnly     workflowRecoveryTransaction     = "directory_without_manifest"
	workflowRecoveryTransactionManifest          workflowRecoveryTransaction     = "manifest"
	workflowRecoveryTransactionRegularFile       workflowRecoveryTransaction     = "regular_file"
	workflowRecoveryTransactionMalformedManifest workflowRecoveryTransaction     = "malformed_manifest"
	workflowRecoveryGenerationPrior              workflowRecoveryGeneration      = "prior"
	workflowRecoveryGenerationCandidate          workflowRecoveryGeneration      = "candidate"
	workflowRecoveryGenerationDivergent          workflowRecoveryGeneration      = "divergent"
	workflowRecoveryEventAbsent                  workflowRecoveryEventGeneration = "absent"
	workflowRecoveryEventExact                   workflowRecoveryEventGeneration = "exact"
	workflowRecoveryEventConflicting             workflowRecoveryEventGeneration = "conflicting"
)

type workflowTransitionRecoveryCase struct {
	transaction             workflowRecoveryTransaction
	manifestState           string
	workflowGeneration      workflowRecoveryGeneration
	eventGeneration         workflowRecoveryEventGeneration
	wantError               string
	wantWorkflowGeneration  workflowRecoveryGeneration
	wantEventCount          int
	wantTransactionRetained bool
}

type workflowTransitionRecoveryFixture struct {
	scenario       *workflowTransactionScenario
	candidate      WorkflowState
	event          ProductWorkflowTransitionEvent
	manifest       productWorkflowTransitionManifest
	priorBytes     []byte
	candidateBytes []byte
	divergentBytes []byte
}

func newWorkflowTransitionRecoveryFixture() *workflowTransitionRecoveryFixture {
	ginkgo.GinkgoHelper()

	scenario := newWorkflowTransactionScenario("audit/product/events.jsonl")
	prior := scenario.workflowState
	prior.CurrentPhase = "phase-planning"
	prior.CurrentState = WorkflowStatePlanned
	prior.NextValidActions = []string{ProductWorkflowActionSubmitForHumanVerification}
	priorBytes := marshalWorkflowRecoveryJSON(prior)
	writeWorkflowRecoveryFile(filepath.Join(scenario.sourceRoot, WorkflowStateFileName), priorBytes)
	scenario.workflowState = prior

	candidate := prior
	candidate.CurrentPhase = "phase-human-verification"
	candidate.CurrentState = WorkflowStateHumanVerification
	candidate.StateRevision++
	candidate.LastEventID = "evt-transition-restart-001"
	candidate.UpdatedAt = "2026-07-12T14:01:00Z"
	candidate.NextValidActions = []string{ProductWorkflowActionApproveForImplementation}
	candidateBytes := marshalWorkflowRecoveryJSON(candidate)

	divergent := candidate
	divergent.Title = "Divergent workflow generation"
	divergentBytes := marshalWorkflowRecoveryJSON(divergent)
	event := ProductWorkflowTransitionEvent{
		Event:     string(ProductWorkflowEventTransitionApplied),
		EventID:   candidate.LastEventID,
		EffortID:  candidate.EffortID,
		TaskID:    candidate.TaskID,
		Timestamp: candidate.UpdatedAt,
		Source:    string(ProductWorkflowEventSourceTransitionApply),
		Actor:     ProductWorkflowActionOwnerOperator,
		Action:    ProductWorkflowActionSubmitForHumanVerification,
		Details: ProductWorkflowTransitionEventDetail{
			FromState:     prior.CurrentState,
			ToState:       candidate.CurrentState,
			StateRevision: candidate.StateRevision,
		},
	}
	manifest := productWorkflowTransitionManifest{
		SchemaVersion:           productWorkflowTransitionManifestSchema,
		State:                   productWorkflowTransitionStatePrepared,
		PreviousWorkflowDigest:  productWorkflowTransitionDigest(priorBytes),
		CandidateWorkflowDigest: productWorkflowTransitionDigest(candidateBytes),
		EventsPath:              filepath.ToSlash(scenario.eventsPath),
		Event:                   event,
	}

	return &workflowTransitionRecoveryFixture{
		scenario:       scenario,
		candidate:      candidate,
		event:          event,
		manifest:       manifest,
		priorBytes:     priorBytes,
		candidateBytes: candidateBytes,
		divergentBytes: divergentBytes,
	}
}

func (fixture *workflowTransitionRecoveryFixture) ledgerPath() string {
	return filepath.Join(fixture.scenario.sourceRoot, fixture.scenario.eventsPath)
}

func (fixture *workflowTransitionRecoveryFixture) workflowBytes(generation workflowRecoveryGeneration) []byte {
	ginkgo.GinkgoHelper()

	switch generation {
	case workflowRecoveryGenerationPrior:
		return fixture.priorBytes
	case workflowRecoveryGenerationCandidate:
		return fixture.candidateBytes
	case workflowRecoveryGenerationDivergent:
		return fixture.divergentBytes
	default:
		ginkgo.Fail(fmt.Sprintf("unknown workflow recovery generation %q", generation))

		return nil
	}
}

func (fixture *workflowTransitionRecoveryFixture) writeWorkflowGeneration(generation workflowRecoveryGeneration) {
	ginkgo.GinkgoHelper()

	writeWorkflowRecoveryFile(
		filepath.Join(fixture.scenario.sourceRoot, WorkflowStateFileName),
		fixture.workflowBytes(generation),
	)
}

func (fixture *workflowTransitionRecoveryFixture) writeEventGeneration(generation workflowRecoveryEventGeneration) {
	ginkgo.GinkgoHelper()

	switch generation {
	case workflowRecoveryEventAbsent:
		return
	case workflowRecoveryEventExact:
		appendWorkflowRecoveryTransitionEvent(fixture.ledgerPath(), fixture.event)
	case workflowRecoveryEventConflicting:
		conflicting := fixture.event
		conflicting.Actor = "conflicting-operator"
		appendWorkflowRecoveryTransitionEvent(fixture.ledgerPath(), conflicting)
	default:
		ginkgo.Fail(fmt.Sprintf("unknown transition event recovery generation %q", generation))
	}
}

func (fixture *workflowTransitionRecoveryFixture) writeTransaction(
	root *os.Root,
	transaction workflowRecoveryTransaction,
	manifestState string,
) {
	ginkgo.GinkgoHelper()

	switch transaction {
	case workflowRecoveryTransactionAbsent:
		return
	case workflowRecoveryTransactionDirectoryOnly:
		gomega.Expect(root.Mkdir(productWorkflowTransitionTransactionRelPath, 0o700)).To(gomega.Succeed())
	case workflowRecoveryTransactionManifest:
		manifest := fixture.manifest
		manifest.State = manifestState
		lease := &productWorkflowMutationLease{root: root, effortRoot: fixture.scenario.sourceRoot}
		gomega.Expect(prepareProductWorkflowTransition(lease, manifest)).To(gomega.Succeed())
	case workflowRecoveryTransactionRegularFile:
		writeWorkflowRecoveryFile(
			filepath.Join(fixture.scenario.sourceRoot, productWorkflowTransitionTransactionRelPath),
			[]byte("not a transaction directory\n"),
		)
	case workflowRecoveryTransactionMalformedManifest:
		gomega.Expect(root.Mkdir(productWorkflowTransitionTransactionRelPath, 0o700)).To(gomega.Succeed())
		writeWorkflowRecoveryFile(
			filepath.Join(
				fixture.scenario.sourceRoot,
				productWorkflowTransitionTransactionRelPath,
				productWorkflowTransitionManifestFileName,
			),
			[]byte("{not-json\n"),
		)
	default:
		ginkgo.Fail(fmt.Sprintf("unknown transition recovery transaction %q", transaction))
	}
}

func appendWorkflowRecoveryTransitionEvent(path string, event ProductWorkflowTransitionEvent) {
	ginkgo.GinkgoHelper()

	body := readWorkflowRecoveryFile(path)
	if len(body) > 0 && body[len(body)-1] != '\n' {
		body = append(body, '\n')
	}
	eventBody, err := json.Marshal(event)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	body = append(body, eventBody...)
	body = append(body, '\n')
	writeWorkflowRecoveryFile(path, body)
}

type workflowTransactionScenario struct {
	effortsRoot   string
	sourceRoot    string
	targetRoot    string
	eventsPath    string
	workflowState WorkflowState
}

type workflowRecoveryMigrationJournal struct {
	manifest productStatusMigrationManifest
}

func newWorkflowTransactionScenario(eventsPath string) *workflowTransactionScenario {
	ginkgo.GinkgoHelper()

	effortID := "transaction-recovery"
	effortsRoot := filepath.Join(ginkgo.GinkgoT().TempDir(), "docs", "product-workflows", "efforts")
	sourceRoot := filepath.Join(effortsRoot, WorkflowStatusBucketStarted, effortID)
	targetRoot := filepath.Join(effortsRoot, WorkflowStatusBucketClosed, effortID)
	state := WorkflowState{
		SchemaVersion:        "idiom.product_workflow_state.v1",
		EffortID:             effortID,
		TaskID:               "TASK-TRANSACTION-RECOVERY",
		Title:                "Recover a product workflow transaction",
		CreatedAt:            "2026-07-12T14:00:00Z",
		UpdatedAt:            "2026-07-12T14:00:00Z",
		StateRevision:        7,
		LastEventID:          "evt-seed-007",
		WorkspaceFingerprint: "idiom@transaction-recovery",
		StatusBucket:         WorkflowStatusBucketStarted,
		CurrentPhase:         "phase-review",
		CurrentState:         WorkflowStateReviewing,
		ArtifactPaths:        map[string]string{"events": filepath.ToSlash(eventsPath)},
		Context: WorkflowContext{
			ContextPackID:  "pack-transaction-recovery",
			FreshnessState: "current",
		},
		Coverage:         CoverageState{CoverageTier: "unit"},
		Privacy:          PrivacyState{RedactionStatus: "applied"},
		FolderPath:       "docs/product-workflows/efforts/started/" + effortID,
		NextValidActions: []string{ProductWorkflowActionApprove},
		DurabilityLevel:  "source_controlled",
	}
	index := EffortIndex{
		SchemaVersion: "idiom.effort_index.v1",
		UpdatedAt:     state.UpdatedAt,
		Efforts: []EffortIndexEntry{{
			EffortID:     state.EffortID,
			Title:        state.Title,
			StatusBucket: state.StatusBucket,
			CurrentPhase: state.CurrentPhase,
			CurrentState: state.CurrentState,
			FolderPath:   "started/" + state.EffortID,
			TaskID:       state.TaskID,
			CreatedAt:    state.CreatedAt,
			PriorPaths:   []string{},
		}},
	}

	writeWorkflowRecoveryJSON(filepath.Join(effortsRoot, "index.json"), index)
	writeWorkflowRecoveryJSON(filepath.Join(sourceRoot, WorkflowStateFileName), state)
	writeWorkflowRecoveryFile(
		filepath.Join(sourceRoot, filepath.FromSlash(eventsPath)),
		[]byte(`{"event":"workflow_state_updated","event_id":"evt-seed-007","effort_id":"transaction-recovery","task_id":"TASK-TRANSACTION-RECOVERY","timestamp":"2026-07-12T14:00:00Z","source":"fixture","details":{"state_revision":7}}`+"\n"),
	)
	writeWorkflowRecoveryFile(filepath.Join(sourceRoot, "README.md"), []byte("# Transaction recovery\n"))

	return &workflowTransactionScenario{
		effortsRoot:   effortsRoot,
		sourceRoot:    sourceRoot,
		targetRoot:    targetRoot,
		eventsPath:    filepath.FromSlash(eventsPath),
		workflowState: state,
	}
}

func (scenario *workflowTransactionScenario) migrationOptions() ProductStatusMigrationApplyOptions {
	return ProductStatusMigrationApplyOptions{
		EffortRoot:                 scenario.sourceRoot,
		EffortsRoot:                scenario.effortsRoot,
		TargetStatusBucket:         WorkflowStatusBucketClosed,
		UpdatedAt:                  "2026-07-12T14:10:00Z",
		MigrationEventID:           "evt-status-migration-restart-001",
		WorkspaceFingerprint:       "idiom@transaction-recovered",
		ExpectedStateRevision:      scenario.workflowState.StateRevision,
		ExpectedSourceStatusBucket: scenario.workflowState.StatusBucket,
	}
}

func prepareWorkflowRecoveryMigrationJournal(scenario *workflowTransactionScenario) workflowRecoveryMigrationJournal {
	ginkgo.GinkgoHelper()

	plan := PlanProductStatusMigrationFromEffortRoot(scenario.migrationOptions())
	gomega.Expect(plan.Status).To(gomega.Equal(ProductWorkflowStatusPlanned), fmt.Sprintf("prepare migration plan: %#v", plan))
	gomega.Expect(plan.Blockers).To(gomega.BeEmpty())
	plan.Event.Event = "status_migration_applied"
	plan.Event.Source = "product_status_migration_apply"

	previousWorkflow := readWorkflowRecoveryFile(filepath.Join(scenario.sourceRoot, WorkflowStateFileName))
	previousEvents := readWorkflowRecoveryFile(filepath.Join(scenario.sourceRoot, scenario.eventsPath))
	previousIndex := readWorkflowRecoveryFile(filepath.Join(scenario.effortsRoot, "index.json"))
	candidateWorkflow := marshalWorkflowRecoveryJSON(plan.UpdatedWorkflowState)
	candidateEvents, err := appendProductStatusMigrationEvent(previousEvents, plan.Event)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	candidateIndex := marshalWorkflowRecoveryJSON(plan.UpdatedEffortIndex)
	candidates := productStatusMigrationCandidates{
		workflow: candidateWorkflow,
		events:   candidateEvents,
		index:    candidateIndex,
	}
	manifest := productStatusMigrationManifest{
		SchemaVersion:           productStatusMigrationManifestSchema,
		State:                   productStatusMigrationTransactionPrepared,
		SourceFolderPath:        plan.SourceFolderPath,
		TargetFolderPath:        plan.TargetFolderPath,
		EventsPath:              filepath.ToSlash(scenario.eventsPath),
		EffortID:                plan.EffortID,
		EventID:                 plan.Event.EventID,
		PreviousWorkflowDigest:  productStatusMigrationDigest(previousWorkflow),
		CandidateWorkflowDigest: productStatusMigrationDigest(candidateWorkflow),
		PreviousEventsDigest:    productStatusMigrationDigest(previousEvents),
		CandidateEventsDigest:   productStatusMigrationDigest(candidateEvents),
		PreviousIndexDigest:     productStatusMigrationDigest(previousIndex),
		CandidateIndexDigest:    productStatusMigrationDigest(candidateIndex),
	}

	root := openWorkflowRecoveryRoot(scenario.effortsRoot)
	gomega.Expect(prepareProductStatusMigration(root, manifest, candidates)).To(gomega.Succeed())
	gomega.Expect(root.Close()).To(gomega.Succeed())

	return workflowRecoveryMigrationJournal{manifest: manifest}
}

func writeWorkflowRecoveryMarker(effortRoot string, manifest productStatusMigrationManifest) {
	ginkgo.GinkgoHelper()

	root := openWorkflowRecoveryRoot(effortRoot)
	gomega.Expect(writeProductStatusMigrationPendingMarker(root, manifest)).To(gomega.Succeed())
	gomega.Expect(root.Close()).To(gomega.Succeed())
}

func openWorkflowRecoveryRoot(path string) *os.Root {
	ginkgo.GinkgoHelper()

	root, err := os.OpenRoot(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	return root
}

func writeWorkflowRecoveryJSON(path string, value any) {
	ginkgo.GinkgoHelper()

	writeWorkflowRecoveryFile(path, marshalWorkflowRecoveryJSON(value))
}

func marshalWorkflowRecoveryJSON(value any) []byte {
	ginkgo.GinkgoHelper()

	body, err := json.MarshalIndent(value, "", "  ")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	return append(body, '\n')
}

func writeWorkflowRecoveryFile(path string, body []byte) {
	ginkgo.GinkgoHelper()

	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, body, 0o644)).To(gomega.Succeed())
}

func readWorkflowRecoveryFile(path string) []byte {
	ginkgo.GinkgoHelper()

	body, err := os.ReadFile(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	return body
}

func countWorkflowRecoveryEventID(path, eventID string) int {
	ginkgo.GinkgoHelper()

	file, err := os.Open(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	defer file.Close()

	count := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		var envelope struct {
			EventID string `json:"event_id"`
		}
		gomega.Expect(json.Unmarshal(scanner.Bytes(), &envelope)).To(gomega.Succeed())
		if envelope.EventID == eventID {
			count++
		}
	}
	gomega.Expect(scanner.Err()).NotTo(gomega.HaveOccurred())

	return count
}

func workflowRecoveryPathCount(paths []string, want string) int {
	count := 0
	for _, path := range paths {
		if filepath.Clean(path) == filepath.Clean(want) {
			count++
		}
	}

	return count
}

func workflowRecoveryTransitionHasBlocker(result ProductWorkflowTransitionApplyResult, code string) bool {
	for _, blocker := range result.Blockers {
		if blocker.Code == code {
			return true
		}
	}

	return false
}

func workflowRecoveryMigrationHasBlocker(result ProductStatusMigrationPlan, code string) bool {
	for _, blocker := range result.Blockers {
		if blocker.Code == code {
			return true
		}
	}

	return false
}

func workflowRecoveryMigrationBlockerText(result ProductStatusMigrationPlan) string {
	var messages []string
	for _, blocker := range result.Blockers {
		messages = append(messages, blocker.Message)
	}

	return strings.Join(messages, "\n")
}
